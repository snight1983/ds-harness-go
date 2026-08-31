// 本文件的作用：把 scope 包的每一条行为钉住，尤其是那些「反过来也说得通、
// 但反过来就错了」的方向性规则。
//
// # 这个包的错法
//
//   - **让事件往下走**。挂在 agent 上的监听器如果收得到预设的事件，那它就收得到
//     它兄弟的事件——而兄弟之间本来就该互相看不见。方向由 [scope.Admits] 独家决定，
//     所以两个方向各钉一条断言，缺一不可。
//   - **撤销撤错了那一份**。一项登记撤销之后同名的东西又登记了一遍，旧的 undo 再被调到时
//     必须什么都不做。少了这条判断，一个迟到的 undo 会把新登记的那份删掉，
//     而且从计数上看不出任何异常。
//   - **释放之后单项 disposer 还去动链表**。作用域整体释放会把链表清空，此时一个
//     迟到的单项 disposer 若仍去 Remove，会把链表长度改成负数。
//   - **回收一个本来就存在的空层**。一次失败的登记只能收拾它自己造出来的东西，
//     不能因为某一层此刻碰巧是空的就把别人的层删掉。
//   - **合并时把被覆盖的名字挪到末尾**。位置就是语义（工具的排列、提示片段的先后），
//     覆盖只该改值。
package scope_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/core/scope"
)

// recorder 记录一串先后发生的动作名，用来钉顺序。
type recorder struct {
	mutex  sync.Mutex
	events []string
}

func (r *recorder) push(name string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.events = append(r.events, name)
}

func (r *recorder) snapshot() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.events)
}

// mark 造一个只记一笔名字的清理函数。
func (r *recorder) mark(name string) func(context.Context) error {
	return func(context.Context) error {
		r.push(name)
		return nil
	}
}

func wantOrder(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("顺序不对：得到 %v，期望 %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 键与父链
// ---------------------------------------------------------------------------

func TestKeyIdentityIsPointerNotLabel(t *testing.T) {
	t.Parallel()

	first := scope.NewKey("agent")
	second := scope.NewKey("agent")

	if first == second {
		t.Fatal("两个同名的键必须是两个不同的作用域")
	}
	if first.Label() != "agent" || second.Label() != "agent" {
		t.Fatalf("Label 应当原样给出诊断名，得到 %q / %q", first.Label(), second.Label())
	}
	// 同名的两个键必须能在报错里区分开，否则一条「父链成环」的信息会指向
	// 两个看起来一模一样的名字。
	if first.String() == second.String() {
		t.Fatalf("两个同名的键的 String 必须不同，都是 %q", first.String())
	}
	if !strings.Contains(first.String(), "agent") {
		t.Fatalf("String 里应当带上诊断名，得到 %q", first.String())
	}
	unlabeled := scope.NewKey("")
	if strings.Contains(unlabeled.String(), "@") {
		t.Fatalf("没有诊断名时不该出现 name@addr 的形态，得到 %q", unlabeled.String())
	}

	var missing *scope.Key
	if missing.String() != "scope.Key(nil)" {
		t.Fatalf("nil 键的 String 不对：%q", missing.String())
	}
	if missing.Label() != "" {
		t.Fatalf("nil 键的 Label 应当是空串，得到 %q", missing.Label())
	}
}

func TestParentChainWalksToRootAndRejectsCycles(t *testing.T) {
	t.Parallel()

	preset := scope.NewKey("preset")
	agent := scope.NewKey("agent")

	if _, err := scope.BindParent(agent, preset); err != nil {
		t.Fatalf("绑定父作用域失败：%v", err)
	}

	if got := scope.ParentOf(agent); got != preset {
		t.Fatalf("agent 的父作用域应当是 preset，得到 %v", got)
	}
	if got := scope.ParentOf(preset); got != nil {
		t.Fatalf("preset 是根，父作用域应当是 nil，得到 %v", got)
	}
	if got := scope.ParentOf(nil); got != nil {
		t.Fatalf("nil 键没有父作用域，得到 %v", got)
	}
	if got := scope.ChainOf(agent); !slices.Equal(got, []*scope.Key{agent, preset}) {
		t.Fatalf("链应当是近的在前 [agent preset]，得到 %v", got)
	}
	if got := scope.ChainOf(nil); len(got) != 0 {
		t.Fatalf("nil 键的链应当是空的，得到 %v", got)
	}

	// preset 已经是 agent 的祖先，反过来接就成环。
	if _, err := scope.BindParent(preset, agent); !errors.Is(err, scope.ErrParentCycle) {
		t.Fatalf("期望 ErrParentCycle，得到 %v", err)
	}
	// 自己接自己是最短的环。
	if _, err := scope.BindParent(preset, preset); !errors.Is(err, scope.ErrParentCycle) {
		t.Fatalf("自环期望 ErrParentCycle，得到 %v", err)
	}
	// 被拒之后 preset 仍然是根：失败的绑定绝不能留下半截状态。
	if got := scope.ParentOf(preset); got != nil {
		t.Fatalf("被拒的绑定不该改动父链，得到 %v", got)
	}

	if _, err := scope.BindParent(nil, preset); err == nil {
		t.Fatal("键为 nil 时应当报错")
	}
	if _, err := scope.BindParent(agent, nil); err == nil {
		t.Fatal("父作用域为 nil 时应当报错")
	}
}

func TestRebindOnlyThroughTheOriginalBinding(t *testing.T) {
	t.Parallel()

	presetA := scope.NewKey("preset-a")
	presetB := scope.NewKey("preset-b")
	agent := scope.NewKey("agent")

	binding, err := scope.BindParent(agent, presetA)
	if err != nil {
		t.Fatalf("首次绑定失败：%v", err)
	}

	// 已经绑过的键，外面再也接不动——归属只能由当初绑定的那一方改。
	if _, err := scope.BindParent(agent, presetB); !errors.Is(err, scope.ErrKeyAlreadyBound) {
		t.Fatalf("期望 ErrKeyAlreadyBound，得到 %v", err)
	}

	if err := binding.Rebind(presetB); err != nil {
		t.Fatalf("持有句柄改链失败：%v", err)
	}
	if got := scope.ChainOf(agent); !slices.Equal(got, []*scope.Key{agent, presetB}) {
		t.Fatalf("改链后链应当是 [agent preset-b]，得到 %v", got)
	}

	// 改链保留成环检查：父作用域不能认自己的后代当爹。
	child := scope.NewKey("child")
	if _, err := scope.BindParent(child, agent); err != nil {
		t.Fatalf("绑定 child 失败：%v", err)
	}
	if err := binding.Rebind(child); !errors.Is(err, scope.ErrParentCycle) {
		t.Fatalf("改链成环期望 ErrParentCycle，得到 %v", err)
	}

	if err := binding.Rebind(nil); err == nil {
		t.Fatal("改链目标为 nil 时应当报错")
	}
	var empty *scope.ParentBinding
	if err := empty.Rebind(presetA); err == nil {
		t.Fatal("空句柄改链应当报错")
	}
}

func TestAdmitsFlowsUpTheChainNeverDown(t *testing.T) {
	t.Parallel()

	preset := scope.NewKey("preset")
	agent := scope.NewKey("agent")
	other := scope.NewKey("other-preset")
	if _, err := scope.BindParent(agent, preset); err != nil {
		t.Fatalf("绑定失败：%v", err)
	}

	// 在 agent 上分派：它自己的标签和它祖先的标签都收得到；兄弟根收不到。
	if !scope.Admits(nil, agent) {
		t.Fatal("无标签的监听器必须收到所有事件")
	}
	if !scope.Admits(agent, agent) {
		t.Fatal("同一个作用域的监听器必须收到")
	}
	if !scope.Admits(preset, agent) {
		t.Fatal("祖先作用域的监听器必须收到后代的事件")
	}
	if scope.Admits(other, agent) {
		t.Fatal("另一条链上的作用域不该收到")
	}

	// 在 preset 上分派：agent 的标签在分派键**下面**，必须收不到。
	// 收到了就意味着事件会往下走，兄弟之间就互相看得见了。
	if scope.Admits(agent, preset) {
		t.Fatal("事件只往上走：后代作用域的监听器不该收到祖先的事件")
	}
	if !scope.Admits(preset, preset) {
		t.Fatal("分派键自己的监听器必须收到")
	}
	if !scope.Admits(nil, preset) {
		t.Fatal("无标签的监听器必须收到")
	}

	// 无作用域的分派：只有无标签的监听器收得到。
	if !scope.Admits(nil, nil) {
		t.Fatal("无作用域的分派，无标签的监听器仍然收得到")
	}
	if scope.Admits(agent, nil) {
		t.Fatal("无作用域的分派，带标签的监听器一律收不到")
	}
}

// ---------------------------------------------------------------------------
// 载体
// ---------------------------------------------------------------------------

type probe struct{ value int }

func TestCarrierRoutesWithoutExposingTheSubject(t *testing.T) {
	t.Parallel()

	key := scope.NewKey("carrier")
	subject := &probe{value: 1}
	carrier := scope.Target(subject, key)

	if !scope.IsCarrier(carrier) {
		t.Fatal("Target 造出来的东西必须被认作载体")
	}
	if got, ok := scope.CarrierKeyOf(carrier); !ok || got != key {
		t.Fatalf("载体的路由键不对：key=%v ok=%v", got, ok)
	}
	if carrier.Key() != key {
		t.Fatalf("Carrier.Key 不对：%v", carrier.Key())
	}
	if scope.IsCarrier(subject) {
		t.Fatal("被承载对象自己不是载体")
	}
	if _, ok := scope.CarrierKeyOf(subject); ok {
		t.Fatal("非载体的第二个返回值必须是 false")
	}

	// 「无作用域的载体」和「压根不是载体」必须分得开：光看键都是 nil，
	// 而分派侧的检查恰恰靠这个区分——本该带载体分派的事件没带载体是个错误，
	// 不是「它没有作用域」。
	unkeyed := scope.Target(subject, nil)
	got, ok := scope.CarrierKeyOf(unkeyed)
	if !ok {
		t.Fatal("无作用域的载体仍然是载体")
	}
	if got != nil {
		t.Fatalf("无作用域的载体的键应当是 nil，得到 %v", got)
	}

	if !carrier.Admits(nil) {
		t.Fatal("无标签的监听器必须收到")
	}
	if carrier.Admits(scope.NewKey("elsewhere")) {
		t.Fatal("别处的标签不该收到")
	}

	var missing *scope.Carrier[*probe]
	if missing.Key() != nil {
		t.Fatal("nil 载体的键应当是 nil")
	}
	if missing.Admits(nil) {
		t.Fatal("nil 载体不该准入任何监听器")
	}
}

func TestTargetFilteredRunsTheBaseFilterFirstWithTheSubject(t *testing.T) {
	t.Parallel()

	key := scope.NewKey("filtered")
	subject := &probe{value: 7}

	var receivedSubject *probe
	var receivedTag *scope.Key
	carrier := scope.TargetFiltered(subject, key, func(got *probe, tag *scope.Key) bool {
		receivedSubject = got
		receivedTag = tag
		return false
	})

	// 基础过滤器否决了就直接不收，作用域规则不再参与——哪怕标签正好等于分派键。
	if carrier.Admits(key) {
		t.Fatal("基础过滤器否决之后不该再看作用域规则")
	}
	// DSH 靠 filter.call(base, ctx) 保证 this 仍是被承载对象；Go 没有 this，
	// 改成把被承载对象显式传进去，这一条断言就是那个测试的等价物。
	if receivedSubject != subject {
		t.Fatalf("基础过滤器应当拿到被承载对象本身，得到 %v", receivedSubject)
	}
	if receivedTag != key {
		t.Fatalf("基础过滤器应当拿到监听器标签，得到 %v", receivedTag)
	}

	passing := scope.TargetFiltered(subject, key, func(*probe, *scope.Key) bool { return true })
	if !passing.Admits(key) {
		t.Fatal("基础过滤器放行后，同作用域的监听器应当收到")
	}
	if passing.Admits(scope.NewKey("elsewhere")) {
		t.Fatal("基础过滤器放行不等于作用域规则失效")
	}
}

// ---------------------------------------------------------------------------
// 作用域的所有权边界
// ---------------------------------------------------------------------------

func TestScopeDisposeRunsTeardownsLastInFirstOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := &recorder{}

	outer := scope.NewRoot()
	if outer.Key() != nil {
		t.Fatal("NewRoot 造的作用域没有身份")
	}
	if _, err := outer.Defer("outer", events.mark("outer")); err != nil {
		t.Fatalf("登记 outer 失败：%v", err)
	}

	nested, err := scope.New(scope.NewKey("nested"), scope.Options{})
	if err != nil {
		t.Fatalf("造嵌套作用域失败：%v", err)
	}
	if _, err := nested.Defer("scope", events.mark("scope")); err != nil {
		t.Fatalf("登记 scope 失败：%v", err)
	}
	// 把嵌套作用域的释放嵌进外层的拆解顺序里：Dispose 本身就是幂等的，
	// 直接登记进去即可，不需要 DSH 那个额外的 rawDispose 入口。
	if _, err := outer.Defer("nested", nested.Dispose); err != nil {
		t.Fatalf("登记 nested 失败：%v", err)
	}
	if _, err := outer.Defer("inner", events.mark("inner")); err != nil {
		t.Fatalf("登记 inner 失败：%v", err)
	}

	if err := outer.Dispose(ctx); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	// 后登记的先跑：inner → nested（连带 scope）→ outer。
	wantOrder(t, events.snapshot(), []string{"inner", "scope", "outer"})
}

func TestScopeDisposeIsIdempotentAndSharedAcrossGoroutines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	gate := make(chan struct{})
	var runs int
	boom := errors.New("拆解失败")

	owner := scope.NewRoot()
	if _, err := owner.Defer("slow", func(context.Context) error {
		<-gate
		runs++
		return boom
	}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	const callers = 4
	results := make([]error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for index := range callers {
		go func() {
			defer group.Done()
			results[index] = owner.Dispose(ctx)
		}()
	}

	close(gate)
	group.Wait()

	if runs != 1 {
		t.Fatalf("清理只该跑一次，实际跑了 %d 次", runs)
	}
	for index, err := range results {
		// 并发调用等同一次完成，并且拿到同一个错误。
		if !errors.Is(err, boom) {
			t.Fatalf("第 %d 个调用方拿到的错误不对：%v", index, err)
		}
	}
	if err := owner.Dispose(ctx); !errors.Is(err, boom) {
		t.Fatalf("重复调用应当拿到同一个错误，得到 %v", err)
	}
}

func TestScopeDisposeRunsEveryTeardownAndJoinsFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := &recorder{}
	first := errors.New("第一个失败")
	second := errors.New("第二个失败")

	owner := scope.NewRoot()
	for _, item := range []struct {
		label string
		err   error
	}{{"a", first}, {"b", nil}, {"c", second}} {
		if _, err := owner.Defer(item.label, func(context.Context) error {
			events.push(item.label)
			return item.err
		}); err != nil {
			t.Fatalf("登记 %s 失败：%v", item.label, err)
		}
	}

	err := owner.Dispose(ctx)
	// 前一项失败就跳过后面的话，剩下的资源就全泄漏了，所以三项都必须跑到。
	wantOrder(t, events.snapshot(), []string{"c", "b", "a"})
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("两个失败都应当被合起来返回，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "a:") || !strings.Contains(err.Error(), "c:") {
		t.Fatalf("错误里应当带上出错那一项的标签，得到 %q", err.Error())
	}
}

func TestScopeDeferDisposerIsIdempotentAndSurvivesLateCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := &recorder{}

	owner := scope.NewRoot()
	release, err := owner.Defer("single", events.mark("single"))
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := owner.Defer("rest", events.mark("rest")); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if got := owner.Effects(); !slices.Equal(got, []string{"single", "rest"}) {
		t.Fatalf("Effects 应当按登记顺序给出，得到 %v", got)
	}

	if err := release(ctx); err != nil {
		t.Fatalf("单项释放失败：%v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatalf("重复的单项释放不该报错：%v", err)
	}
	if got := owner.Effects(); !slices.Equal(got, []string{"rest"}) {
		t.Fatalf("释放掉的那一项应当从 Effects 里摘掉，得到 %v", got)
	}

	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("整体释放失败：%v", err)
	}
	// 已经单独释放过的那一项不该在整体释放时再跑一遍。
	wantOrder(t, events.snapshot(), []string{"single", "rest"})

	// 释放之后才被调到的单项 disposer 必须什么都不做：作用域整体释放时链表已经清空，
	// 此时再去 Remove 一个不在链上的元素会把链表长度改成负数。
	if err := release(ctx); err != nil {
		t.Fatalf("释放之后的单项 disposer 不该报错：%v", err)
	}
	if got := owner.Effects(); len(got) != 0 {
		t.Fatalf("整体释放之后 Effects 应当是空的，得到 %v", got)
	}
}

func TestScopeDeferAfterDisposeIsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := scope.NewRoot()
	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放失败：%v", err)
	}

	// 静默接受的话这份清理永远跑不到，也就是那份资源泄漏了，且没有任何症状。
	if _, err := owner.Defer("late", func(context.Context) error { return nil }); !errors.Is(err, scope.ErrScopeDisposed) {
		t.Fatalf("期望 ErrScopeDisposed，得到 %v", err)
	}
	if _, err := owner.Defer("nil", nil); err == nil {
		t.Fatal("清理函数为 nil 时应当报错")
	}
}

func TestScopeDisposeAfterSingleReleaseKeepsTheListConsistent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	owner := scope.NewRoot()
	release, err := owner.Defer("only", func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	// 先整体释放、再调单项 disposer——这是链表长度被改成负数的那条路径。
	if err := release(ctx); err != nil {
		t.Fatalf("迟到的单项释放不该报错：%v", err)
	}
	if got := owner.Effects(); len(got) != 0 {
		t.Fatalf("Effects 应当是空的，得到 %v", got)
	}
}

func TestNewRequiresAKeyAndBindsTheParent(t *testing.T) {
	t.Parallel()

	if _, err := scope.New(nil, scope.Options{}); err == nil {
		t.Fatal("键为 nil 时应当报错（无身份的所有权边界请用 NewRoot）")
	}

	// 绑父作用域失败时不该造出一个半吊子的作用域。
	root := scope.NewKey("root")
	if _, err := scope.New(root, scope.Options{Parent: root}); !errors.Is(err, scope.ErrParentCycle) {
		t.Fatalf("自环期望 ErrParentCycle，得到 %v", err)
	}

	preset := scope.NewKey("preset")
	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{Parent: preset})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	if agent.Key() != agentKey {
		t.Fatalf("Key 不对：%v", agent.Key())
	}
	if got := scope.ParentOf(agentKey); got != preset {
		t.Fatalf("Options.Parent 应当在作用域可用之前就绑好，得到 %v", got)
	}
	// 绑定句柄留在内部，外面再也接不动。
	if _, err := scope.BindParent(agentKey, scope.NewKey("other")); !errors.Is(err, scope.ErrKeyAlreadyBound) {
		t.Fatalf("期望 ErrKeyAlreadyBound，得到 %v", err)
	}

	var missing *scope.Scope
	if missing.Key() != nil {
		t.Fatal("nil 作用域的键应当是 nil")
	}
}

// ---------------------------------------------------------------------------
// 具名登记表
// ---------------------------------------------------------------------------

func TestNamedEntriesOrderLookupAndExactIdempotentUndo(t *testing.T) {
	t.Parallel()

	duplicate := errors.New("调用方自己的重名错误")
	var askedFor []string
	entries := scope.NewNamedEntries[int](func(name string) error {
		askedFor = append(askedFor, name)
		return duplicate
	})

	undoA, err := entries.Insert("a", 1)
	if err != nil {
		t.Fatalf("插入 a 失败：%v", err)
	}
	undoB, err := entries.Insert("b", 2)
	if err != nil {
		t.Fatalf("插入 b 失败：%v", err)
	}

	if got := slices.Collect(entries.Keys()); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Keys 应当按插入顺序，得到 %v", got)
	}
	if got := slices.Collect(entries.Values()); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("Values 应当按插入顺序，得到 %v", got)
	}
	if value, ok := entries.Get("a"); !ok || value != 1 {
		t.Fatalf("Get(a) 不对：value=%d ok=%v", value, ok)
	}
	if _, ok := entries.Get("missing"); ok {
		t.Fatal("Get 不存在的名字第二个返回值必须是 false")
	}
	if !entries.Has("b") || entries.Has("missing") {
		t.Fatal("Has 判断不对")
	}
	if entries.IsEmpty() || entries.Len() != 2 {
		t.Fatalf("IsEmpty/Len 不对：%v / %d", entries.IsEmpty(), entries.Len())
	}

	// 重名的诊断由调用方给：只有它知道重的是什么。
	if _, err := entries.Insert("a", 3); !errors.Is(err, duplicate) {
		t.Fatalf("重名应当返回调用方给的错误，得到 %v", err)
	}
	if !slices.Equal(askedFor, []string{"a"}) {
		t.Fatalf("诊断函数应当拿到重的那个名字，得到 %v", askedFor)
	}

	// 撤销之后同名的东西又登记了一遍，旧的 undo 再被调到时必须什么都不做——
	// 否则它会把新登记的那一份删掉，而且从计数上看不出任何异常。
	undoA()
	if _, err := entries.Insert("a", 3); err != nil {
		t.Fatalf("撤销之后重新插入 a 失败：%v", err)
	}
	undoA()
	if value, ok := entries.Get("a"); !ok || value != 3 {
		t.Fatalf("旧的 undo 不该删掉新登记的那一份，得到 value=%d ok=%v", value, ok)
	}

	undoB()
	got := map[string]int{}
	var order []string
	for name, value := range entries.All() {
		order = append(order, name)
		got[name] = value
	}
	if !slices.Equal(order, []string{"a"}) || got["a"] != 3 {
		t.Fatalf("All 的结果不对：order=%v got=%v", order, got)
	}
}

func TestNamedEntriesFallsBackToItsOwnDuplicateError(t *testing.T) {
	t.Parallel()

	entries := scope.NewNamedEntries[int](nil)
	if _, err := entries.Insert("a", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}

	_, err := entries.Insert("a", 2)
	var duplicate *scope.DuplicateNameError
	if !errors.As(err, &duplicate) {
		t.Fatalf("没给诊断函数时应当兜底成 DuplicateNameError，得到 %v", err)
	}
	if duplicate.Name != "a" {
		t.Fatalf("兜底错误里的名字不对：%q", duplicate.Name)
	}
	if !strings.Contains(duplicate.Error(), "a") {
		t.Fatalf("兜底错误的文案里应当带上名字，得到 %q", duplicate.Error())
	}
}

func TestNamedEntriesIterationIsASnapshot(t *testing.T) {
	t.Parallel()

	entries := scope.NewNamedEntries[int](nil)
	if _, err := entries.Insert("a", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}

	next, stop := iter.Pull(entries.Keys())
	defer stop()

	if name, ok := next(); !ok || name != "a" {
		t.Fatalf("第一项不对：name=%q ok=%v", name, ok)
	}
	if _, err := entries.Insert("b", 2); err != nil {
		t.Fatalf("遍历途中插入失败：%v", err)
	}
	// 这是本包和 DSH 之间一处刻意的差异：DSH 交出去的是实时迭代器，看得见后来的插入；
	// 这里先在锁内拷一份顺序再交出去，遍历期间的插入看不见，换来的是不会在回调里死锁。
	if _, ok := next(); ok {
		t.Fatal("快照语义：遍历期间的插入不该被看见")
	}
	// 新起一轮遍历才看得到。
	if got := slices.Collect(entries.Keys()); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("新一轮遍历应当看到全部，得到 %v", got)
	}
}

func TestNamedEntriesIterationStopsEarly(t *testing.T) {
	t.Parallel()

	entries := scope.NewNamedEntries[int](nil)
	if _, err := entries.Insert("a", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}
	if _, err := entries.Insert("b", 2); err != nil {
		t.Fatalf("插入失败：%v", err)
	}

	var keys []string
	for name := range entries.Keys() {
		keys = append(keys, name)
		break
	}
	var values []int
	for value := range entries.Values() {
		values = append(values, value)
		break
	}
	var pairs []string
	for name := range entries.All() {
		pairs = append(pairs, name)
		break
	}
	if !slices.Equal(keys, []string{"a"}) || !slices.Equal(values, []int{1}) || !slices.Equal(pairs, []string{"a"}) {
		t.Fatalf("提前退出应当立刻停下：%v %v %v", keys, values, pairs)
	}
}

// ---------------------------------------------------------------------------
// 匿名登记表
// ---------------------------------------------------------------------------

func TestAnonymousEntriesKeepsEqualValuesIndependent(t *testing.T) {
	t.Parallel()

	entries := scope.NewAnonymousEntries[string]()
	if !entries.IsEmpty() {
		t.Fatal("新表应当是空的")
	}

	undoFirst := entries.Append("same")
	undoSecond := entries.Append("same")

	// 同一个值登记两次就是两份登记，撤销其中一份不影响另一份。
	if got := slices.Collect(entries.Values()); !slices.Equal(got, []string{"same", "same"}) {
		t.Fatalf("两份独立的登记都该在，得到 %v", got)
	}
	if entries.Len() != 2 {
		t.Fatalf("Len 应当是 2，得到 %d", entries.Len())
	}

	undoFirst()
	undoFirst()
	if got := slices.Collect(entries.Values()); !slices.Equal(got, []string{"same"}) {
		t.Fatalf("撤销一份之后另一份必须还在，得到 %v", got)
	}

	undoSecond()
	if !entries.IsEmpty() {
		t.Fatal("两份都撤销之后表应当空了")
	}

	// 提前退出。
	entries.Append("x")
	entries.Append("y")
	var seen []string
	for value := range entries.Values() {
		seen = append(seen, value)
		break
	}
	if !slices.Equal(seen, []string{"x"}) {
		t.Fatalf("提前退出应当立刻停下，得到 %v", seen)
	}
}

// ---------------------------------------------------------------------------
// 分层存储
// ---------------------------------------------------------------------------

// testLayer 是一层里同时有具名表和匿名表的最小实现：只有两张表都空了，
// 这一层才算空——回收的判定就靠这个。
type testLayer struct {
	named     *scope.NamedEntries[int]
	anonymous *scope.AnonymousEntries[string]
}

func newTestLayer(key *scope.Key) *testLayer {
	owner := "global"
	if key != nil {
		owner = "scoped"
	}
	return &testLayer{
		named: scope.NewNamedEntries[int](func(name string) error {
			return errors.New(owner + " 重名: " + name)
		}),
		anonymous: scope.NewAnonymousEntries[string](),
	}
}

func (l *testLayer) IsEmpty() bool { return l.named.IsEmpty() && l.anonymous.IsEmpty() }

func pickNamed(layer *testLayer) *scope.NamedEntries[int] { return layer.named }

// insertNamed 造一个往具名表里插一项的 action。
func insertNamed(name string, value int) func(*testLayer) (func(), error) {
	return func(layer *testLayer) (func(), error) { return layer.named.Insert(name, value) }
}

func mergedPairs(t *testing.T, layers *scope.Layers[*testLayer], key *scope.Key) []string {
	t.Helper()
	var pairs []string
	for _, item := range scope.MergeNamed(layers, key, pickNamed) {
		pairs = append(pairs, item.Name+"="+strconv.Itoa(item.Value))
	}
	return pairs
}

func TestLayersBuildTheGlobalLayerEagerlyAndNeverCreateOnRead(t *testing.T) {
	t.Parallel()

	var created []string
	layers, err := scope.NewLayers(func(key *scope.Key) (*testLayer, error) {
		created = append(created, key.Label())
		return newTestLayer(key), nil
	}, nil)
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	key := scope.NewKey("agent")
	if _, err := layers.Global().named.Insert("a", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}
	if _, err := layers.Global().named.Insert("shared", 2); err != nil {
		t.Fatalf("插入失败：%v", err)
	}

	if !slices.Equal(created, []string{""}) {
		t.Fatalf("只该建出全局层，得到 %v", created)
	}
	if _, ok := layers.Peek(nil); ok {
		t.Fatal("Peek(nil) 不该给出任何层")
	}
	if _, ok := layers.Peek(key); ok {
		t.Fatal("读不该把覆盖层建出来")
	}
	if got := mergedPairs(t, layers, key); !slices.Equal(got, []string{"a=1", "shared=2"}) {
		t.Fatalf("合并结果不对：%v", got)
	}
	if got := layers.ChainLayers(key); len(got) != 0 {
		t.Fatalf("链上还没有覆盖层，得到 %v", got)
	}
	// 一次查询把层建出来的话，「这个作用域有没有自己的贡献」就再也问不出真话了。
	if !slices.Equal(created, []string{""}) {
		t.Fatalf("读之后仍然只该有全局层，得到 %v", created)
	}

	if _, err := scope.NewLayers[*testLayer](nil, nil); err == nil {
		t.Fatal("createLayer 为 nil 时应当报错")
	}
	boom := errors.New("建层失败")
	if _, err := scope.NewLayers(func(*scope.Key) (*testLayer, error) { return nil, boom }, nil); !errors.Is(err, boom) {
		t.Fatalf("全局层建不出来时应当当场报错，得到 %v", err)
	}
}

func TestLayersCreateLazilyShadowInPlaceAndReclaimOnlyAnEmptyAggregate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := scope.NewKey("agent")
	owner, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}

	var created []string
	var changes int
	layers, err := scope.NewLayers(func(selected *scope.Key) (*testLayer, error) {
		created = append(created, selected.Label())
		return newTestLayer(selected), nil
	}, func() error {
		changes++
		return nil
	})
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	if _, err := layers.Global().named.Insert("a", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}
	if _, err := layers.Global().named.Insert("shared", 1); err != nil {
		t.Fatalf("插入失败：%v", err)
	}

	silent := scope.EffectOptions{Silent: true}
	removeNamed, err := layers.Effect(ctx, owner, insertNamed("shared", 2), silent)
	if err != nil {
		t.Fatalf("登记 shared 失败：%v", err)
	}
	removeTail, err := layers.Effect(ctx, owner, insertNamed("c", 3), silent)
	if err != nil {
		t.Fatalf("登记 c 失败：%v", err)
	}
	removeAnonymous, err := layers.Effect(ctx, owner, func(layer *testLayer) (func(), error) {
		return layer.anonymous.Append("kept"), nil
	}, silent)
	if err != nil {
		t.Fatalf("登记匿名项失败：%v", err)
	}

	if !slices.Equal(created, []string{"", "agent"}) {
		t.Fatalf("覆盖层只该在第一次登记时建一次，得到 %v", created)
	}
	// 覆盖只改值，不挪位置：shared 仍然排在 a 和 c 之间。
	if got := mergedPairs(t, layers, key); !slices.Equal(got, []string{"a=1", "shared=2", "c=3"}) {
		t.Fatalf("合并结果不对：%v", got)
	}
	if changes != 0 {
		t.Fatalf("Silent 的登记不该发通知，实际发了 %d 次", changes)
	}

	if err := removeNamed(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, ok := layers.Peek(key); !ok {
		t.Fatal("这一层还有别的登记，不该被回收")
	}
	if got := mergedPairs(t, layers, key); !slices.Equal(got, []string{"a=1", "shared=1", "c=3"}) {
		t.Fatalf("撤销覆盖之后应当露出全局那一份，得到 %v", got)
	}

	if err := removeTail(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	// 具名表空了，但匿名表还有东西——只有整个聚合层空了才回收。
	if _, ok := layers.Peek(key); !ok {
		t.Fatal("匿名表里还有登记，这一层不该被回收")
	}

	if err := removeAnonymous(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, ok := layers.Peek(key); ok {
		t.Fatal("整个聚合层空了就该回收")
	}

	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
}

func TestLayersEffectOrdersActionNotificationUndoAndIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := &recorder{}
	layers, err := scope.NewLayers(func(key *scope.Key) (*testLayer, error) {
		return newTestLayer(key), nil
	}, func() error {
		events.push("notify")
		return nil
	})
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	// 没有身份的作用域落在全局层。
	owner := scope.NewRoot()
	dispose, err := layers.Effect(ctx, owner, func(layer *testLayer) (func(), error) {
		events.push("action")
		undo, err := layer.named.Insert("x", 1)
		if err != nil {
			return nil, err
		}
		return func() {
			events.push("undo")
			undo()
		}, nil
	}, scope.EffectOptions{Label: "store.order"})
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	wantOrder(t, events.snapshot(), []string{"action", "notify"})
	if got := owner.Effects(); !slices.Contains(got, "store.order") {
		t.Fatalf("撤销应当以给定的标签挂在作用域上，得到 %v", got)
	}

	if err := dispose(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if err := dispose(ctx); err != nil {
		t.Fatalf("重复撤销不该报错：%v", err)
	}
	wantOrder(t, events.snapshot(), []string{"action", "notify", "undo", "notify"})
	if !layers.Global().IsEmpty() {
		t.Fatal("撤销之后全局层应当空了")
	}

	if _, err := layers.Effect(ctx, nil, insertNamed("y", 1), scope.EffectOptions{}); err == nil {
		t.Fatal("宿主作用域为 nil 时应当报错")
	}
	if _, err := layers.Effect(ctx, owner, nil, scope.EffectOptions{}); err == nil {
		t.Fatal("action 为 nil 时应当报错")
	}
	// action 成功了却不给撤销，这次登记就永远撤不掉了。
	if _, err := layers.Effect(ctx, owner, func(*testLayer) (func(), error) {
		return nil, nil
	}, scope.EffectOptions{Silent: true}); err == nil {
		t.Fatal("action 不返回撤销函数时应当报错")
	}
}

func TestLayersEffectCleansUpFailuresWithoutDiscardingAnExistingLayer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := scope.NewKey("agent")
	owner, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}

	factoryFails := true
	factoryErr := errors.New("建层失败")
	layers, err := scope.NewLayers(func(selected *scope.Key) (*testLayer, error) {
		if selected != nil && factoryFails {
			return nil, factoryErr
		}
		return newTestLayer(selected), nil
	}, nil)
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	silent := scope.EffectOptions{Silent: true}
	if _, err := layers.Effect(ctx, owner, insertNamed("never", 1), silent); !errors.Is(err, factoryErr) {
		t.Fatalf("期望建层错误，得到 %v", err)
	}
	if _, ok := layers.Peek(key); ok {
		t.Fatal("建层失败后不该留下任何层")
	}

	factoryFails = false
	actionErr := errors.New("登记失败")
	if _, err := layers.Effect(ctx, owner, func(*testLayer) (func(), error) {
		return nil, actionErr
	}, silent); !errors.Is(err, actionErr) {
		t.Fatalf("期望登记错误，得到 %v", err)
	}
	if _, ok := layers.Peek(key); ok {
		t.Fatal("为这次登记新建的空层应当被收走")
	}

	dispose, err := layers.Effect(ctx, owner, insertNamed("kept", 1), silent)
	if err != nil {
		t.Fatalf("登记 kept 失败：%v", err)
	}
	secondErr := errors.New("第二次登记失败")
	if _, err := layers.Effect(ctx, owner, func(*testLayer) (func(), error) {
		return nil, secondErr
	}, silent); !errors.Is(err, secondErr) {
		t.Fatalf("期望第二次登记错误，得到 %v", err)
	}
	// 一次失败的登记只能收拾它自己造出来的东西。
	layer, ok := layers.Peek(key)
	if !ok {
		t.Fatal("已经存在的层不该因为一次失败的登记被删")
	}
	if value, found := layer.named.Get("kept"); !found || value != 1 {
		t.Fatalf("已有的登记应当原封不动，得到 value=%d found=%v", value, found)
	}

	if err := dispose(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
}

func TestLayersEffectRollsBackWhenNotificationFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := scope.NewKey("agent")
	owner, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}

	events := &recorder{}
	notifications := 0
	changeErr := errors.New("通知失败")
	layers, err := scope.NewLayers(func(selected *scope.Key) (*testLayer, error) {
		return newTestLayer(selected), nil
	}, func() error {
		events.push("notify")
		notifications++
		if notifications == 1 {
			return changeErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	_, err = layers.Effect(ctx, owner, func(layer *testLayer) (func(), error) {
		undo, err := layer.named.Insert("rollback", 1)
		if err != nil {
			return nil, err
		}
		return func() {
			events.push("undo")
			undo()
		}, nil
	}, scope.EffectOptions{Label: "store.rollback"})

	if !errors.Is(err, changeErr) {
		t.Fatalf("期望通知错误，得到 %v", err)
	}
	// 撤销走的是和正常释放同一条路径，所以第二次通知也会发出去。
	wantOrder(t, events.snapshot(), []string{"notify", "undo", "notify"})
	if _, ok := layers.Peek(key); ok {
		t.Fatal("回滚之后这一层应当被收走")
	}

	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
}

func TestLayersEffectRefusesADisposedOwnerAndUndoesTheAction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key := scope.NewKey("agent")
	owner, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	if err := owner.Dispose(ctx); err != nil {
		t.Fatalf("释放失败：%v", err)
	}

	var changes int
	layers, err := scope.NewLayers(func(selected *scope.Key) (*testLayer, error) {
		return newTestLayer(selected), nil
	}, func() error {
		changes++
		return nil
	})
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	if _, err := layers.Effect(ctx, owner, insertNamed("late", 1), scope.EffectOptions{}); !errors.Is(err, scope.ErrScopeDisposed) {
		t.Fatalf("期望 ErrScopeDisposed，得到 %v", err)
	}
	// 登记不上就得把 action 已经做过的那部分退回去，而且不能对外发通知——
	// 什么都没生效，通知出去就是一句谎话。
	if _, ok := layers.Peek(key); ok {
		t.Fatal("登记失败之后不该留下任何层")
	}
	if changes != 0 {
		t.Fatalf("失败的登记不该发通知，实际发了 %d 次", changes)
	}
}

func TestChainLayersOrdersFarthestAncestorFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	presetKey := scope.NewKey("preset")
	agentKey := scope.NewKey("agent")

	preset, err := scope.New(presetKey, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	agent, err := scope.New(agentKey, scope.Options{Parent: presetKey})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}

	layers, err := scope.NewLayers(func(selected *scope.Key) (*testLayer, error) {
		return newTestLayer(selected), nil
	}, nil)
	if err != nil {
		t.Fatalf("造分层存储失败：%v", err)
	}

	silent := scope.EffectOptions{Silent: true}
	if _, err := layers.Effect(ctx, preset, insertNamed("shared", 1), silent); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := layers.Effect(ctx, agent, insertNamed("shared", 2), silent); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := layers.Effect(ctx, agent, insertNamed("own", 3), silent); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	presetLayer, _ := layers.Peek(presetKey)
	agentLayer, _ := layers.Peek(agentKey)
	// 远祖在前、本作用域在最后：按这个顺序叠加，最近的那个作用域说了算。
	if got := layers.ChainLayers(agentKey); !slices.Equal(got, []*testLayer{presetLayer, agentLayer}) {
		t.Fatalf("链上的层顺序不对，得到 %d 项", len(got))
	}
	// 反过来看，预设看不到 agent 那一层——注册物只往下继承，不往上冒。
	if got := layers.ChainLayers(presetKey); !slices.Equal(got, []*testLayer{presetLayer}) {
		t.Fatalf("祖先不该看到后代那一层，得到 %d 项", len(got))
	}

	if got := mergedPairs(t, layers, agentKey); !slices.Equal(got, []string{"shared=2", "own=3"}) {
		t.Fatalf("agent 视角的合并结果不对：%v", got)
	}
	if got := mergedPairs(t, layers, presetKey); !slices.Equal(got, []string{"shared=1"}) {
		t.Fatalf("preset 视角的合并结果不对：%v", got)
	}

	if err := errors.Join(agent.Dispose(ctx), preset.Dispose(ctx)); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	if _, ok := layers.Peek(agentKey); ok {
		t.Fatal("作用域释放之后它那一层应当被收走")
	}
	if _, ok := layers.Peek(presetKey); ok {
		t.Fatal("作用域释放之后它那一层应当被收走")
	}
}
