// 本文件的作用：编排器上那些只有把册子或者后端摆成特定样子才走得到的岔路——
// 对账时读不动存档、准备途中令牌变了、修复写不下去、退场卡住时几条读路各自等在
// 哪儿，以及一份准备好的会话真的被发布出去时那两种收场。
//
// # 这些测试防的是什么错
//
//   - 对账时那条「只比游标以内那一段」的夹取被写成整段比，让一份更长的存档
//     无谓地被当成撞号。
//   - 一次「读到的这一份在提交之前就变了」被当成失败报上去，而不是回去重来。
//   - 一次修复写不下去却被当成修好了，之后那份存档的坏尾巴再也没人管。
//   - 一次退场还没收手时就去准备／加载／查看／读，读到一份正要被划掉的状态。
//   - 一份准备好的会话发布出去时，它那截还没落盘的后缀被漏掉或者写重。
//   - 后台那条攒批写撞上入册的错时把缓冲里的事件丢掉。
//
// 源: packages/session/session-persistence/src/coordinator.ts

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// endSeed 排一条封种标记出来。
//
// 一个活会话的日志在 seed 之后必有这么一条；把它一并摆进存档里，是为了搭出
// 「磁盘上那份已经**整个**覆盖了活会话的日志」这种局面——只有那时候要补的
// 后缀才真的是空的。
func endSeed(t *testing.T, seq int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{Type: sessionlog.EventSessionEndSeed, Seq: seq, Time: int64(seq)}
}

// appendLive 往一个活会话上追加一条用户消息，好让它的缓冲里压着东西。
func appendLive(t *testing.T, live *coresession.Session, seq int, text string) {
	t.Helper()

	if _, err := live.Append(sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      userEvent(t, seq, text).Data,
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
}

func TestCoordinator认领没主状态时没有要补的后缀(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("没什么要补的")
	if err := h.Create(t.Context(), testHeader(t, id)); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	// 不带 seed 的活会话，日志是空的：那份没主的状态什么都没落过盘（游标为零,
	// 无条件算对上），而要补的后缀也是空的。两头都是零，这一趟该什么都不写。
	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)

	if got := len(h.backend.storedEvents(id)); got != 0 {
		t.Fatalf("没有任何事件可写，不该落盘，拿到 %d 条", got)
	}
	if h.stateOf(id) == nil {
		t.Fatal("这份没主的状态该被认领下来")
	}
}

func TestCoordinator收编一份空存档(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("空存档")
	h.backend.seed(testHeader(t, id), nil, nil)

	// 磁盘上有一份头、但一条事件都没有；活会话也是空的。这是收编那条路上
	// 「没有超出前缀的后缀」那一支。
	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)

	state := h.stateOf(id)
	if state == nil {
		t.Fatal("该被收编下来")
	}
	if got := len(h.backend.storedEvents(id)); got != 0 {
		t.Fatalf("不该凭空写出事件来，拿到 %d 条", got)
	}
}

func TestCoordinator对账时读不动存档(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("对账读不动")
	seed := []sessionlog.Event{userEvent(t, 0, "甲")}

	// 摆一份「已经落过一条」的没主状态出来，好让对账那一步真的去读存档。
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), nextSeq: 1, started: true, materialized: true}
	h.mutex.Unlock()

	boom := errors.New("盘坏了")
	h.backend.fail(boom, nil, nil, nil)

	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	if err := h.flush(live); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条读错，拿到 %v", err)
	}
}

func TestCoordinator对账时存档里的身份对不上(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("对账身份不符")
	seed := []sessionlog.Event{userEvent(t, 0, "甲")}

	h.backend.mutex.Lock()
	h.backend.logs[id] = &memoryLog{
		meta:     testHeader(t, sessionlog.SessionID("别人的")),
		events:   cloneEvents(seed),
		revision: 1,
	}
	h.backend.mutex.Unlock()
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), nextSeq: 1, started: true, materialized: true}
	h.mutex.Unlock()

	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	if err := h.flush(live); err == nil {
		t.Fatal("存档里的身份对不上该被拒掉")
	}
}

func TestCoordinator对账只比游标以内那一段(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("只比前一段")
	seed := []sessionlog.Event{userEvent(t, 0, "甲")}

	// 存档比游标长。对账要比的是「游标以内那一段」，把整段拿去比会误判成撞号。
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲"), userEvent(t, 1, "乙")}, nil)
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), nextSeq: 1, started: true, materialized: true}
	h.mutex.Unlock()

	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	if err := h.flush(live); err != nil {
		t.Fatalf("游标以内那一段是对得上的，不该被拒：%v", err)
	}
}

func TestCoordinator入册时读存档读不动(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	boom := errors.New("盘坏了")
	h.backend.fail(boom, nil, nil, nil)

	live := h.createLive(t, sessionlog.SessionID("入册读不动"), coresession.CreateOptions{})
	if err := h.flush(live); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条读错，拿到 %v", err)
	}
}

func TestCoordinator池子里那份不是这个会话的预留(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("别名发布")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 查看一遍把池子焐热：这个身份在池子里留下一份**就绪**的成果，而它不是
	// 一份等着下面这个活会话去发布的预留。
	if _, err := h.Inspect(t.Context(), id); err != nil {
		t.Fatalf("查看失败：%v", err)
	}

	live := h.createLive(t, id, coresession.CreateOptions{})
	if err := h.flush(live); err == nil {
		t.Fatal("池子里那份不是这个会话的预留，该被拒掉")
	}
}

func TestCoordinator把准备好的会话发布出去(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("发布准备好的")
	// 存档已经以封种标记结尾，那个待发布的会话不会再补一条——于是它的日志和
	// 存档一样长，要补的后缀是空的。
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲"), endSeed(t, 1)}, nil)

	prepared, err := h.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	live := prepared.Session()
	detach, err := h.sessions.Enter(h.owner, live)
	if err != nil {
		t.Fatalf("入表失败：%v", err)
	}
	if _, err := h.owner.Defer("发布准备好的", detach); err != nil {
		t.Fatalf("挂摘除失败：%v", err)
	}
	if err := h.sessions.Announce(t.Context(), live); err != nil {
		t.Fatalf("公布失败：%v", err)
	}
	prepared.Release()

	h.settle(t, live)
	if got := len(h.backend.storedEvents(id)); got != 2 {
		t.Fatalf("那截后缀是空的，存档该还是 2 条，拿到 %d 条", got)
	}
}

func TestCoordinator准备成果和持久化状态对不上(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("接不上")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲"), endSeed(t, 1)}, nil)

	prepared, err := h.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	live := prepared.Session()

	// 准备好之后、发布之前，那份状态被别人认领走了。这时候再把准备成果接上去
	// 就等于把两段不同的历史缝在一起。
	other, err := h.sessions.PrepareRestored(sessionlog.SessionID("别的"), coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: testHeader(t, sessionlog.SessionID("别的")),
	})
	if err != nil {
		t.Fatalf("恢复不出占位的会话：%v", err)
	}
	h.mutex.Lock()
	h.states[id].owner = other
	h.mutex.Unlock()

	detach, err := h.sessions.Enter(h.owner, live)
	if err != nil {
		t.Fatalf("入表失败：%v", err)
	}
	if _, err := h.owner.Defer("接不上", detach); err != nil {
		t.Fatalf("挂摘除失败：%v", err)
	}
	if err := h.sessions.Announce(t.Context(), live); err != nil {
		t.Fatalf("公布失败：%v", err)
	}
	prepared.Release()

	if err := h.flush(live); err == nil {
		t.Fatal("准备成果和持久化状态对不上，该被拒掉")
	}
}

func TestCoordinator准备时已经有活着的持久化主人(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("有持久化主人")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 那个主人不在活会话表里（否则 Prepare 头一道就拦掉了），但它确实攥着
	// 这份持久化状态——正是提交那一步要拦的局面。
	other, err := h.sessions.PrepareRestored(sessionlog.SessionID("别的"), coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: testHeader(t, sessionlog.SessionID("别的")),
	})
	if err != nil {
		t.Fatalf("恢复不出占位的会话：%v", err)
	}
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), nextSeq: 1, started: true, materialized: true, owner: other}
	h.mutex.Unlock()

	if _, err := h.Prepare(t.Context(), id); err == nil {
		t.Fatal("已经有一个活着的持久化主人，准备不了它")
	}
}

func TestCoordinator准备途中令牌变了就重来(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("令牌变了")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 头一轮核对令牌时后端交回一个对不上的：那一份作废，整个重来。第二轮
	// 令牌正常，于是准备得出来——这条路要的就是「重来一轮之后仍然收敛」。
	h.backend.mutex.Lock()
	h.backend.revisionSkews = 1
	h.backend.mutex.Unlock()

	prepared, err := h.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("重来一轮之后该准备得出来：%v", err)
	}
	prepared.Release()
}

func TestCoordinator准备时修复写不下去(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("修不了")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, "坏尾巴")
	h.backend.fail(nil, nil, errors.New("修不了"), nil)

	if _, err := h.Prepare(t.Context(), id); err == nil {
		t.Fatal("修复写不下去该被抛上来，不能当成修好了")
	}
}

func TestCoordinator认领时修复写不下去(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("认领时修不了")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, "坏尾巴")
	h.backend.fail(nil, nil, errors.New("修不了"), nil)

	// 追加会先把这个还没入册的身份认领进来，而认领要先把那截坏尾巴修掉。
	if err := h.Append(t.Context(), id, []sessionlog.Event{userEvent(t, 1, "乙")}); err == nil {
		t.Fatal("修复写不下去该被抛上来")
	}
}

func TestCoordinator查看时冷读读不动(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	boom := errors.New("盘坏了")
	h.backend.fail(boom, nil, nil, nil)

	if _, err := h.Inspect(t.Context(), sessionlog.SessionID("查看读不动")); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条读错，拿到 %v", err)
	}
}

func TestCoordinator查看时核对令牌读不动(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("查看核不动")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 先看一遍把池子焐热，再让核对令牌那一步失败——这条路和冷读失败是两处。
	if _, err := h.Inspect(t.Context(), id); err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	boom := errors.New("令牌读不动")
	h.backend.fail(nil, boom, nil, nil)

	if _, err := h.Inspect(t.Context(), id); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条错，拿到 %v", err)
	}
}

func TestCoordinator查看时存档没了(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("看着看着没了")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	if _, err := h.Inspect(t.Context(), id); err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	// 存档整个没了。核对令牌读不到东西时要当成「对不上」，不能当成「还是它」——
	// 否则一份 revision 恰好是空串的成果会把「存档没了」认成没变。
	h.backend.drop(id)

	if _, err := h.Inspect(t.Context(), id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("该报这个身份不在，拿到 %v", err)
	}
}

func TestCoordinator查看时那一份扔不掉就借用它(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("借用独占者的视图")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 先准备一份出来，池子里那一条就被独占着了。
	prepared, err := h.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	defer prepared.Release()

	// 再让核对令牌对不上：这一份按理该扔掉重读，可它正被人独占着，扔不动。
	h.backend.mutex.Lock()
	h.backend.revisionSkews = 1
	h.backend.mutex.Unlock()

	// 这时候借用独占者那份视图——它仍然是一段自洽的历史，而且重读也读不出别的
	// 东西来（独占者还没提交，存档就是这一份）。硬要重来会在这里转不出去。
	got, err := h.Inspect(t.Context(), id)
	if err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("该借到独占者那一份，拿到 %d 条事件", len(got.Events))
	}
}

func TestCoordinator加载一个刷不下去的活会话(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("加载时刷不下去")
	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)

	appendLive(t, live, 0, "甲")
	boom := errors.New("写不下去")
	h.backend.fail(nil, nil, nil, boom)

	// 这条路承诺交回来的是**耐久的**那一份，刷不下去就交不出来。
	if _, err := h.Load(t.Context(), id); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条写错，拿到 %v", err)
	}
	h.backend.fail(nil, nil, nil, nil)
}

func TestCoordinator补平存档时负载读不回来(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("补不平")
	// 三道判据只看身份、版本、词汇，不拆负载：这条 turn/start 的类型是认得的，
	// 于是它一路走到补平那一步，才在拆负载时栽下来。
	h.backend.seed(testHeader(t, id), []sessionlog.Event{{
		Type: sessionlog.EventTurnStart, Seq: 0, Time: 0,
		Data: json.RawMessage(`{"turn":"不是个数"}`),
	}}, nil)

	if _, err := h.Prepare(t.Context(), id); err == nil {
		t.Fatal("补不平的存档该被拒掉")
	}
}

func TestCoordinator退场还没收手时几条读路都等着(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("退场卡住")
	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)
	appendLive(t, live, 0, "甲")

	// 让退场那一趟的刷盘停在后端里不动，于是这条退场登记一直挂着。
	hold := make(chan struct{})
	blocked := make(chan struct{})
	h.backend.mutex.Lock()
	h.backend.holdAppend, h.backend.blocked = hold, blocked
	h.backend.mutex.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			h.backend.mutex.Lock()
			h.backend.holdAppend = nil
			h.backend.mutex.Unlock()
			close(hold)
		})
	}
	t.Cleanup(release)

	h.retire(live)
	<-blocked

	// 退场期间那个身份还挂在册子上，这几条路读它会读到一份正要被划掉的状态,
	// 所以它们都得先等那次退场收手。等不及就带着自己那条 ctx 的错回来。
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"读后缀", func(ctx context.Context) error { _, err := h.ReadFrom(ctx, id, 0); return err }},
		{"准备", func(ctx context.Context) error { _, err := h.Prepare(ctx, id); return err }},
		{"加载", func(ctx context.Context) error { _, err := h.Load(ctx, id); return err }},
		{"查看", func(ctx context.Context) error { _, err := h.Inspect(ctx, id); return err }},
	}
	for _, item := range cases {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		err := item.call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s 该等着那次退场，拿到 %v", item.name, err)
		}
	}

	release()
	if err := h.waitForRetirement(t.Context(), id); err != nil {
		t.Fatalf("等退场失败：%v", err)
	}
}

// signalHandler 是一个只做一件事的日志处理器：头一条记录到手时关掉 fired。
type signalHandler struct {
	fired chan struct{}
	once  sync.Once
}

func (h *signalHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *signalHandler) Handle(context.Context, slog.Record) error {
	h.once.Do(func() { close(h.fired) })
	return nil
}

func (h *signalHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *signalHandler) WithGroup(string) slog.Handler { return h }

func TestCoordinator后台写撞上入册的错时只记日志(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	boom := errors.New("盘坏了")
	// 入册要先读一遍存档，读不动它就整个失败——那条结论之后每一次写都会撞上。
	backend.fail(boom, nil, nil, nil)

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	fired := make(chan struct{})
	coordinator, err := NewCoordinator(
		CoordinatorDeps{
			Backend:  backend,
			Sessions: store,
			Logger:   slog.New(&signalHandler{fired: fired}),
		},
		// 攒批时长给一个到得了点的值：这条路要的正是那次**自动**到点的后台写。
		CoordinatorOptions{WriteBatchMaxDelay: time.Millisecond},
	)
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	owner := scope.NewRoot()
	undo, err := coordinator.Install(t.Context(), owner)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}

	live, err := store.Create(t.Context(), owner, sessionlog.SessionID("后台写失败"), coresession.CreateOptions{})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	appendLive(t, live, 0, "甲")

	// 定时器到点之后那次后台写会撞上入册的错。它不往上抛——没人可抛——只记日志,
	// 而缓冲里那条事件留着，等下一次刷盘再试。
	<-fired

	// 拆装那一趟还会再撞一次同一条错，那不算一次新的失败。
	_ = undo(context.WithoutCancel(t.Context()))
}
