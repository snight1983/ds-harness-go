// 本文件的作用：那个准备池自己的用例——冷读怎么共享、独占怎么开怎么收、
// 已就绪的条目怎么按最近使用淘汰，以及每一条「这一份不作数了」的岔路。
//
// # 这些测试防的是什么错
//
//   - 同一个身份上并发地读，却各读各的：白读一遍不说，两份视图还会打架。
//   - 一个等待方走掉时把那次共享的冷读一起掐了，连累其余还在等的人。
//   - 一份已经被独占的成果还被人扔掉或者顺手拿走，让那次独占落空。
//   - 提交判定「这一份该重来」时条目没被摘掉，下一轮拿到的还是那份旧的。
//   - 独占期间放事件写进去，等那个会话真发布出来 seed 和存档就对不上了。
//   - 淘汰时把 loading／committing／reserved 的条目也算进容量，扔掉一个
//     正被人用着的；或者数对了却在挑人时把一个正被占着的挑出来扔掉。
//   - 一次冷读跑在半路上条目就被作废，收尾时却按「还是我那一份」去动池子，
//     把接替它的那一份连累掉。
//
// 源: packages/session/session-persistence/src/preparations.ts

package persistence

import (
	"context"
	"errors"
	"sync"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// sourceFor 排一份最小可用的准备成果出来。
//
// 池子本身只把 [preparedSource] 当一个不透明的值传来传去（除了
// [preparations.reservationFor] 要比 live 那一个字段），所以除了那一处，
// 用例里都不必真去恢复一个会话。
func sourceFor(id sessionlog.SessionID, revision Revision) *preparedSource {
	return &preparedSource{
		inspection: Inspection{Meta: sessionlog.SessionHeader{
			Version: sessionlog.FormatVersion, ID: id, CreatedAt: 1,
		}},
		revision: revision,
	}
}

// loaderOf 造一次立刻成功的冷读。
func loaderOf(source *preparedSource) preparationLoader {
	return func() (*preparedSource, error) { return source, nil }
}

// commitOf 造一次立刻成功的提交。
func commitOf(source *preparedSource) func(*preparedSource) (*commitResult, error) {
	return func(*preparedSource) (*commitResult, error) {
		return &commitResult{source: source, state: &sessionState{}}, nil
	}
}

func TestPreparations冷读只做一次并共享出去(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("共享")
	source := sourceFor(id, "r1")

	// 冷读挂在这里，两个调用方都得等它。
	gate := make(chan struct{})
	var loads int
	var mutex sync.Mutex
	load := func() (*preparedSource, error) {
		mutex.Lock()
		loads++
		mutex.Unlock()
		<-gate
		return source, nil
	}

	results := make([]*preparedSource, 2)
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			got, err := pool.inspect(t.Context(), id, load)
			if err != nil {
				t.Errorf("看第 %d 次失败：%v", index, err)
				return
			}
			results[index] = got
		}(index)
	}
	// 等两条都进了池子再放行，否则第二条可能在第一条建条目之前就跑完了。
	for !pool.has(id) {
	}
	close(gate)
	group.Wait()

	mutex.Lock()
	got := loads
	mutex.Unlock()
	if got != 1 {
		t.Fatalf("冷读该只做一次，做了 %d 次", got)
	}
	for index, result := range results {
		if result != source {
			t.Fatalf("第 %d 个调用方该拿到同一份成果", index)
		}
	}
}

func TestPreparations冷读失败时条目会被摘掉(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("读不出来")
	boom := errors.New("读不出来")

	if _, err := pool.inspect(t.Context(), id, func() (*preparedSource, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("该原样交回冷读那条错，拿到 %v", err)
	}
	if pool.has(id) {
		t.Fatal("读失败之后条目不该还留在池子里")
	}
}

func TestPreparations等待方走掉不掐那次共享的冷读(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("等不及")
	source := sourceFor(id, "r1")

	gate := make(chan struct{})
	done := make(chan struct{})
	load := func() (*preparedSource, error) {
		<-gate
		close(done)
		return source, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		for !pool.has(id) {
		}
		cancel()
	}()
	if _, err := pool.inspect(ctx, id, load); !errors.Is(err, context.Canceled) {
		t.Fatalf("等待方走掉该拿到取消，拿到 %v", err)
	}

	// 那次冷读仍然跑得完——它是共享的，不该被某一个等待方连累。
	close(gate)
	<-done
}

func TestPreparations独占之后写会被拦下(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("独占")
	source := sourceFor(id, "r1")

	if err := pool.assertWritable(id); err != nil {
		t.Fatalf("池子里没有这个身份时不该拦：%v", err)
	}

	reservation, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
	if err != nil {
		t.Fatalf("独占失败：%v", err)
	}
	if reservation == nil {
		t.Fatal("该拿到一份预留")
	}
	if err := pool.assertWritable(id); err == nil {
		t.Fatal("独占期间该拦下写")
	}
	// 独占着的那一份不许被顺手拿走，也不许被当成过期的扔掉。
	if got := pool.takeReady(id); got != nil {
		t.Fatal("独占着的条目不该被摘走")
	}
	if got := pool.discardReady(id, source); got != discardRetained {
		t.Fatalf("独占着的条目该是「留着」，拿到 %v", got)
	}

	if err := pool.attach(reservation); err != nil {
		t.Fatalf("认上失败：%v", err)
	}
	if pool.has(id) {
		t.Fatal("认上之后条目该被消掉")
	}
	if err := pool.attach(reservation); err == nil {
		t.Fatal("同一份预留认第二次该报错")
	}
}

func TestPreparations提交判定重来时条目被摘掉(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("重来")
	source := sourceFor(id, "r1")

	reservation, err := pool.reserve(t.Context(), id, loaderOf(source),
		func(*preparedSource) (*commitResult, error) { return nil, nil })
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if reservation != nil {
		t.Fatal("提交判定重来时不该交回预留")
	}
	if pool.has(id) {
		t.Fatal("判定重来之后条目该被摘掉，好让下一轮重新冷读")
	}
}

func TestPreparations提交失败时条目被摘掉(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("提交不了")
	source := sourceFor(id, "r1")
	boom := errors.New("提交不了")

	_, err := pool.reserve(t.Context(), id, loaderOf(source),
		func(*preparedSource) (*commitResult, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("该原样交回提交那条错，拿到 %v", err)
	}
	if pool.has(id) {
		t.Fatal("提交失败之后条目该被摘掉")
	}
}

func TestPreparations冷读失败时独占也交回那条错(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("独占前就读不出来")
	boom := errors.New("读不出来")

	_, err := pool.reserve(t.Context(), id,
		func() (*preparedSource, error) { return nil, boom },
		commitOf(nil))
	if !errors.Is(err, boom) {
		t.Fatalf("该原样交回冷读那条错，拿到 %v", err)
	}
}

func TestPreparations提交完发现调用方走了就放回就绪(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("提交完才走")
	source := sourceFor(id, "r1")

	ctx, cancel := context.WithCancel(t.Context())
	_, err := pool.reserve(ctx, id, loaderOf(source),
		func(*preparedSource) (*commitResult, error) {
			// 修复已经落盘了，这时候调用方才撤。
			cancel()
			return &commitResult{source: source, state: &sessionState{}}, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("该拿到取消，拿到 %v", err)
	}
	// 那次修复已经落盘，扔掉条目等于让下一个人白读一遍，所以它该还在、而且就绪。
	if got := pool.takeReady(id); got != source {
		t.Fatal("条目该被放回就绪，好让下一个人直接用")
	}
}

func TestPreparations放回和扔掉那两条岔路(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)

	// 还能再用：放回就绪，下一个人摘得走。
	reusable := sessionlog.SessionID("还能用")
	source := sourceFor(reusable, "r1")
	reservation, err := pool.reserve(t.Context(), reusable, loaderOf(source), commitOf(source))
	if err != nil || reservation == nil {
		t.Fatalf("独占失败：%v", err)
	}
	pool.release(reservation, true)
	if got := pool.takeReady(reusable); got != source {
		t.Fatal("放回就绪的那一份该摘得走")
	}

	// 已经被写过：整个摘掉，免得下一个人拿到一份和存档对不上的视图。
	spent := sessionlog.SessionID("写过了")
	spentSource := sourceFor(spent, "r1")
	spentReservation, err := pool.reserve(t.Context(), spent, loaderOf(spentSource), commitOf(spentSource))
	if err != nil || spentReservation == nil {
		t.Fatalf("独占失败：%v", err)
	}
	pool.release(spentReservation, false)
	if pool.has(spent) {
		t.Fatal("写过的那一份该整个摘掉")
	}
	// 再放一次是空操作，不该 panic 也不该动别人的条目。
	pool.release(spentReservation, true)
	pool.discard(spentReservation)
}

func TestPreparations只要视图那条路把预留消掉(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("只要视图")
	source := sourceFor(id, "r1")

	reservation, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
	if err != nil || reservation == nil {
		t.Fatalf("独占失败：%v", err)
	}
	pool.discard(reservation)
	if pool.has(id) {
		t.Fatal("消掉之后条目不该还在")
	}
}

func TestPreparations存档变了就把成果作废(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("变了")
	source := sourceFor(id, "r1")

	if _, err := pool.inspect(t.Context(), id, loaderOf(source)); err != nil {
		t.Fatalf("看失败：%v", err)
	}
	pool.invalidate(id)
	if pool.has(id) {
		t.Fatal("作废之后条目不该还在")
	}
	// 池子里没有这个身份时再作废一次是空操作。
	pool.invalidate(id)
}

func TestPreparations扔掉过期成果的那三种结果(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("过期")
	source := sourceFor(id, "r1")

	if got := pool.discardReady(id, source); got != discardMissing {
		t.Fatalf("池子里没有时该是「没有」，拿到 %v", got)
	}
	if _, err := pool.inspect(t.Context(), id, loaderOf(source)); err != nil {
		t.Fatalf("看失败：%v", err)
	}
	// 「恰好那一份」：手上拿的不是池子里当前那一份时，不能顺手把新的扔掉。
	if got := pool.discardReady(id, sourceFor(id, "r2")); got != discardMissing {
		t.Fatalf("对不上的那一份该是「没有」，拿到 %v", got)
	}
	if !pool.has(id) {
		t.Fatal("对不上的时候不该动池子里那一份")
	}
	if got := pool.discardReady(id, source); got != discardDiscarded {
		t.Fatalf("对得上的该被扔掉，拿到 %v", got)
	}
	if pool.has(id) {
		t.Fatal("扔掉之后条目不该还在")
	}
}

func TestPreparations没就绪条目时摘不走(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	if got := pool.takeReady(sessionlog.SessionID("没有")); got != nil {
		t.Fatal("池子里没有这个身份时该交回 nil")
	}
}

func TestPreparations发布别名会被拒掉(t *testing.T) {
	t.Parallel()

	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	pool := newPreparations(5)
	id := sessionlog.SessionID("别名")

	live, err := sessions.PrepareRestored(id, coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: id, CreatedAt: 1},
	})
	if err != nil {
		t.Fatalf("恢复不出会话：%v", err)
	}

	// 池子里没有这个身份：是一个全新的会话，两个都为 nil。
	reservation, err := pool.reservationFor(live)
	if err != nil || reservation != nil {
		t.Fatalf("全新的会话该两个都为 nil，拿到 %v / %v", reservation, err)
	}

	// 池子里有这个身份，但它不是一份等着这个会话去发布的预留——这是撞号。
	source := sourceFor(id, "r1")
	if _, err := pool.inspect(t.Context(), id, loaderOf(source)); err != nil {
		t.Fatalf("看失败：%v", err)
	}
	if _, err := pool.reservationFor(live); err == nil {
		t.Fatal("别名该被拒掉")
	}
}

func TestPreparations就绪条目超编时淘汰最久没碰的(t *testing.T) {
	t.Parallel()

	pool := newPreparations(1)
	first := sessionlog.SessionID("甲")
	second := sessionlog.SessionID("乙")

	if _, err := pool.inspect(t.Context(), first, loaderOf(sourceFor(first, "r1"))); err != nil {
		t.Fatalf("看甲失败：%v", err)
	}
	if _, err := pool.inspect(t.Context(), second, loaderOf(sourceFor(second, "r1"))); err != nil {
		t.Fatalf("看乙失败：%v", err)
	}
	if pool.has(first) {
		t.Fatal("容量是一，甲该被淘汰掉")
	}
	if !pool.has(second) {
		t.Fatal("刚碰过的乙该留着")
	}
}

func TestPreparations独占着的条目不算进容量(t *testing.T) {
	t.Parallel()

	pool := newPreparations(1)
	held := sessionlog.SessionID("被占着")
	source := sourceFor(held, "r1")
	reservation, err := pool.reserve(t.Context(), held, loaderOf(source), commitOf(source))
	if err != nil || reservation == nil {
		t.Fatalf("独占失败：%v", err)
	}

	// 容量是一，但被占着的那个不该被这一次就绪挤掉：容量管的是「留着备用的
	// 有多少」，不是「池子里一共有多少」。
	other := sessionlog.SessionID("另一个")
	if _, err := pool.inspect(t.Context(), other, loaderOf(sourceFor(other, "r1"))); err != nil {
		t.Fatalf("看另一个失败：%v", err)
	}
	if !pool.has(held) {
		t.Fatal("被独占着的条目不该被淘汰")
	}
	if !pool.has(other) {
		t.Fatal("刚就绪的那个该在")
	}
}

func TestPreparations后到的独占要等前一个收手(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("排队")
	source := sourceFor(id, "r1")

	reservation, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
	if err != nil || reservation == nil {
		t.Fatalf("头一次独占失败：%v", err)
	}

	// 第二个人来的时候前一个还占着，他得等——这里让他直接撞上取消。
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	second, err := pool.reserve(ctx, id, loaderOf(source), commitOf(source))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("等不及该拿到取消，拿到 %v / %v", second, err)
	}

	// 前一个放手之后，第二个人就排得上了。
	pool.release(reservation, true)
	again, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
	if err != nil || again == nil {
		t.Fatalf("前一个放手之后该排得上：%v", err)
	}
}

func TestPreparations等着独占时条目被换掉就重来(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("等着等着没了")
	source := sourceFor(id, "r1")

	reservation, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
	if err != nil || reservation == nil {
		t.Fatalf("头一次独占失败：%v", err)
	}

	// 第二个人排在后面等；前一个直接把条目整个摘掉，于是他该拿到「重来」。
	ready := make(chan struct{})
	type outcome struct {
		reservation *preparationReservation
		err         error
	}
	results := make(chan outcome, 1)
	go func() {
		close(ready)
		got, err := pool.reserve(t.Context(), id, loaderOf(source), commitOf(source))
		results <- outcome{got, err}
	}()
	<-ready
	pool.release(reservation, false)

	got := <-results
	if got.err != nil || got.reservation != nil {
		t.Fatalf("条目被换掉该是「重来」（两个都为 nil），拿到 %v / %v", got.reservation, got.err)
	}
}

func TestPreparations就绪的条目不拦写(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("就绪着")
	if _, err := pool.inspect(t.Context(), id, loaderOf(sourceFor(id, "r1"))); err != nil {
		t.Fatalf("看失败：%v", err)
	}

	// 拦写只拦独占：一份留着备用的就绪成果不该把写路径堵住。
	if err := pool.assertWritable(id); err != nil {
		t.Fatalf("就绪的条目不该拦写：%v", err)
	}
}

func TestPreparations等冷读时走掉独占也停下(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("等冷读时走掉")
	entered := make(chan struct{})
	gate := make(chan struct{})
	load := func() (*preparedSource, error) {
		close(entered)
		<-gate
		return sourceFor(id, "r1"), nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-entered
		cancel()
	}()
	if _, err := pool.reserve(ctx, id, load, commitOf(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("等冷读时走掉该拿到取消，拿到 %v", err)
	}

	// 那次冷读仍然跑得完：它是共享的，不该被这个等待方连累。
	close(gate)
}

func TestPreparations冷读途中被作废还是交回读到的那一份(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("读着读着被作废")
	source := sourceFor(id, "r1")
	entered := make(chan struct{})
	gate := make(chan struct{})
	load := func() (*preparedSource, error) {
		close(entered)
		<-gate
		return source, nil
	}

	// 冷读还挂着的时候把条目作废掉：收尾那一步就不会再把它挂进池子。
	go func() {
		<-entered
		pool.invalidate(id)
		close(gate)
	}()

	got, err := pool.inspect(t.Context(), id, load)
	if err != nil {
		t.Fatalf("看失败：%v", err)
	}
	// 调用方要的是「那一刻存档长什么样」，这次读出来的视图仍然作数。
	if got != source {
		t.Fatal("该交回这次冷读自己读到的那一份")
	}
	if pool.has(id) {
		t.Fatal("已经作废的条目不该被收尾那一步又挂回池子里")
	}
}

func TestPreparations冷读途中被作废又失败时不碰接替的那一份(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("作废之后才失败")
	fresh := sourceFor(id, "r2")
	boom := errors.New("读不出来")
	entered := make(chan struct{})
	gate := make(chan struct{})
	load := func() (*preparedSource, error) {
		close(entered)
		<-gate
		return nil, boom
	}

	// 作废之后池子里换上一份新的。那条失败的冷读收尾时会去摘条目，摘的必须是
	// 它自己那一份——按身份去摘就会把接替的这一份连累掉。
	swapped := make(chan error, 1)
	go func() {
		<-entered
		pool.invalidate(id)
		_, err := pool.inspect(context.Background(), id, loaderOf(fresh))
		swapped <- err
		close(gate)
	}()

	if _, err := pool.inspect(t.Context(), id, load); !errors.Is(err, boom) {
		t.Fatalf("该原样交回冷读那条错，拿到 %v", err)
	}
	if err := <-swapped; err != nil {
		t.Fatalf("换新的那一份失败：%v", err)
	}
	if got := pool.takeReady(id); got != fresh {
		t.Fatal("接替的那一份该好端端留在池子里")
	}
}

func TestPreparations提交途中条目被换掉就重来(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("提交途中被换掉")
	source := sourceFor(id, "r1")

	// 提交跑在锁外面，这期间存档可能变了。修复虽然落了盘，但这一份已经不作数，
	// 调用方该拿到「重来」而不是一份对不上的预留。
	reservation, err := pool.reserve(t.Context(), id, loaderOf(source),
		func(*preparedSource) (*commitResult, error) {
			pool.invalidate(id)
			return &commitResult{source: source, state: &sessionState{}}, nil
		})
	if err != nil || reservation != nil {
		t.Fatalf("条目被换掉该是「重来」（两个都为 nil），拿到 %v / %v", reservation, err)
	}
}

func TestPreparations提交完既走了又被换掉(t *testing.T) {
	t.Parallel()

	pool := newPreparations(5)
	id := sessionlog.SessionID("又走了又被换掉")
	source := sourceFor(id, "r1")

	ctx, cancel := context.WithCancel(t.Context())
	_, err := pool.reserve(ctx, id, loaderOf(source),
		func(*preparedSource) (*commitResult, error) {
			pool.invalidate(id)
			cancel()
			return &commitResult{source: source, state: &sessionState{}}, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("该拿到取消，拿到 %v", err)
	}
	// 调用方走掉时那条路会把条目放回就绪。可这个条目已经被换掉了，放回等于把
	// 一份过期的视图重新挂上去。
	if pool.has(id) {
		t.Fatal("已经被换掉的条目不该被放回就绪那一步塞回池子")
	}
}

func TestPreparations淘汰时跳过正被占着的条目(t *testing.T) {
	t.Parallel()

	pool := newPreparations(1)
	held := sessionlog.SessionID("被占着")
	source := sourceFor(held, "r1")
	if _, err := pool.reserve(t.Context(), held, loaderOf(source), commitOf(source)); err != nil {
		t.Fatalf("独占失败：%v", err)
	}

	// 被占着的那个排在队首、也就是「最久没碰的」。挑淘汰对象时得跳过它，
	// 往后找到第一个真正就绪的。
	first := sessionlog.SessionID("甲")
	second := sessionlog.SessionID("乙")
	if _, err := pool.inspect(t.Context(), first, loaderOf(sourceFor(first, "r1"))); err != nil {
		t.Fatalf("看甲失败：%v", err)
	}
	if _, err := pool.inspect(t.Context(), second, loaderOf(sourceFor(second, "r1"))); err != nil {
		t.Fatalf("看乙失败：%v", err)
	}

	if !pool.has(held) {
		t.Fatal("被占着的条目排在队首也不该被挑去淘汰")
	}
	if pool.has(first) {
		t.Fatal("跳过被占着的那个之后，甲才是该淘汰的")
	}
	if !pool.has(second) {
		t.Fatal("刚碰过的乙该留着")
	}
}

func TestAwaitShared干完了就不算取消(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// 两边都就绪时不许看运气：一件已经干完的活儿就是干完了。
	if err := awaitShared(ctx, closedChannel()); err != nil {
		t.Fatalf("干完了不该报取消，拿到 %v", err)
	}

	// 还没干完、ctx 又断了，这才是取消。
	if err := awaitShared(ctx, make(chan struct{})); !errors.Is(err, context.Canceled) {
		t.Fatalf("该拿到取消，拿到 %v", err)
	}
}
