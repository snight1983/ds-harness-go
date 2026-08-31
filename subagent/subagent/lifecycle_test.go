// 本文件的作用：那对生命周期边的测试——兜住异常的发射器、作用域分层派发、
// 一次性运行的观察者、可续活化的观察者，以及「这一轮为什么结束」那条判据。

package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/session"
)

// newEmitter 造一个把警告攒进切片、而不是往 slog 里写的发射器。
func newEmitter(t *testing.T) (*lifecycleEmitter, func() []string) {
	t.Helper()
	emitter, err := newLifecycleEmitter(nil)
	if err != nil {
		t.Fatalf("造发射器失败：%v", err)
	}
	var mutex sync.Mutex
	var warnings []string
	emitter.warn = func(message string) {
		mutex.Lock()
		defer mutex.Unlock()
		warnings = append(warnings, message)
	}
	return emitter, func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), warnings...)
	}
}

// ---- 兜住异常 ----

// 一个 panic 的观察者既不该穿出去，也不该饿着排在它后面的同侪。
func TestEmitStartContainsAPanickingObserver(t *testing.T) {
	emitter, warnings := newEmitter(t)
	runtime := newRuntime(t)
	runtime.emitLifecycle = emitter
	owner := rootScope(t)
	ctx := context.Background()

	var seen int
	if _, err := runtime.OnStart(ctx, owner, func(RunInfo, agent.Agent) { panic("炸了") }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnStart(ctx, owner, func(RunInfo, agent.Agent) { seen++ }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	emitter.emitStart(RunInfo{ID: "child"}, nil)
	if seen != 1 {
		t.Fatalf("排在 panic 后面的观察者该照常被叫到，实际叫了 %d 次", seen)
	}
	logged := warnings()
	if len(logged) != 1 || !strings.Contains(logged[0], "subagent/start") {
		t.Fatalf("该记下一条 start 观察者的 panic，实际 %#v", logged)
	}
}

// 发射器自带那条告警路把 panic 写进 logger，logger 为 nil 时写进 slog 的默认处。
// 本包别处的测试一律把 warn 整个换掉，所以这两支只有这里走得到。
func TestLifecycleEmitterWarnsThroughItsOwnLogger(t *testing.T) {
	for name, given := range map[string]bool{"给了 logger": true, "没给 logger": false} {
		t.Run(name, func(t *testing.T) {
			var written strings.Builder
			handler := slog.NewTextHandler(&written, nil)

			var (
				emitter *lifecycleEmitter
				err     error
			)
			if given {
				emitter, err = newLifecycleEmitter(slog.New(handler))
			} else {
				previous := slog.Default()
				slog.SetDefault(slog.New(handler))
				t.Cleanup(func() { slog.SetDefault(previous) })
				emitter, err = newLifecycleEmitter(nil)
			}
			if err != nil {
				t.Fatalf("造发射器失败：%v", err)
			}

			remove := emitter.layers.Global().start.Append(func(RunInfo, agent.Agent) { panic("炸了") })
			t.Cleanup(remove)
			emitter.emitStart(RunInfo{ID: "child"}, nil)

			if !strings.Contains(written.String(), "炸了") {
				t.Fatalf("那次 panic 该被记下来，实际 %q", written.String())
			}
		})
	}
}

// 「提供方被摘掉」是从一个处置器里发出来的，那里的 panic 尤其不能把拆解打断。
func TestEmitProviderRemovedContainsAPanickingObserver(t *testing.T) {
	emitter, warnings := newEmitter(t)
	runtime := newRuntime(t)
	runtime.emitLifecycle = emitter

	if _, err := runtime.OnProviderRemoved(context.Background(), rootScope(t), func(string) {
		panic("炸了")
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	emitter.emitProviderRemoved("spawn")
	if logged := warnings(); len(logged) != 1 {
		t.Fatalf("该记下一条 provider-removed 的 panic，实际 %#v", logged)
	}
}

// 终止那条边同样被兜住。
func TestEmitEndContainsAPanickingObserver(t *testing.T) {
	emitter, warnings := newEmitter(t)
	runtime := newRuntime(t)
	runtime.emitLifecycle = emitter

	if _, err := runtime.OnEnd(context.Background(), rootScope(t), func(RunEndInfo, agent.Agent) {
		panic("炸了")
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	emitter.emitEnd(RunEndInfo{ID: "child", StopReason: StopCompleted}, nil)
	if logged := warnings(); len(logged) != 1 {
		t.Fatalf("该记下一条 end 的 panic，实际 %#v", logged)
	}
}

// warn 为 nil 时那条 panic 只是被丢掉，不该反过来把发射器自己弄崩。
func TestContainWithoutAWarnSinkStillSwallowsThePanic(t *testing.T) {
	emitter, err := newLifecycleEmitter(nil)
	if err != nil {
		t.Fatalf("造发射器失败：%v", err)
	}
	emitter.warn = nil
	emitter.contain("test", func() { panic("炸了") })
}

// ---- 作用域分层 ----

// 挂在有身份作用域上的观察者只看得见由那个作用域（或它的子孙）发起的派发，
// 挂在无身份作用域上的看得见每一次。
func TestLifecycleDispatchHonoursScopeLayering(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()

	parent := newFakeAgent(t, "parent", nil, "")
	sibling := newFakeAgent(t, "sibling", nil, "")
	child := newFakeAgent(t, "child", parent.Scope().Key(), parent.ID())

	var global, scoped []session.SessionID
	if _, err := runtime.OnStart(ctx, rootScope(t), func(info RunInfo, _ agent.Agent) {
		global = append(global, info.ID)
	}); err != nil {
		t.Fatalf("登记全局观察者失败：%v", err)
	}
	if _, err := runtime.OnStart(ctx, parent.Scope(), func(info RunInfo, _ agent.Agent) {
		scoped = append(scoped, info.ID)
	}); err != nil {
		t.Fatalf("登记带作用域的观察者失败：%v", err)
	}

	for _, carrier := range []agent.Agent{parent, sibling, child, nil} {
		id := session.SessionID("nobody")
		if carrier != nil {
			id = carrier.ID()
		}
		runtime.emitLifecycle.emitStart(RunInfo{ID: id}, carrier)
	}

	wantGlobal := []session.SessionID{"parent", "sibling", "child", "nobody"}
	if !equalIDs(global, wantGlobal) {
		t.Fatalf("全局层该看见每一次派发，实际 %#v", global)
	}
	// 兄弟那次和没有载体那次都落在 parent 那条父链外面。
	if !equalIDs(scoped, []session.SessionID{"parent", "child"}) {
		t.Fatalf("带作用域的观察者只该看见自己那条链，实际 %#v", scoped)
	}
}

// 提供方那两条边没有父这个载体，所以只发给全局层。
func TestProviderEdgesOnlyReachTheGlobalLayer(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()
	carrier := newFakeAgent(t, "parent", nil, "")

	var global, scoped int
	if _, err := runtime.OnProviderAdded(ctx, rootScope(t), func(Provider) error {
		global++
		return nil
	}); err != nil {
		t.Fatalf("登记全局观察者失败：%v", err)
	}
	if _, err := runtime.OnProviderAdded(ctx, carrier.Scope(), func(Provider) error {
		scoped++
		return nil
	}); err != nil {
		t.Fatalf("登记带作用域的观察者失败：%v", err)
	}

	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn"})
	if global != 1 || scoped != 0 {
		t.Fatalf("这条边只该走全局层，实际 global=%d scoped=%d", global, scoped)
	}
}

// 一个没有作用域的父解不出那把载体键，于是这条边退回只发全局层——解不出载体
// 不等于这条边发不出去。
func TestLifecycleEdgesFallBackToTheGlobalLayerForAScopelessParent(t *testing.T) {
	runtime := newRuntime(t)
	// 顺带把那份不变量检查装上：start 这条边先报给检查、再走观察者，两段都要过。
	armed(t, runtime)
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn"})

	var global int
	if _, err := runtime.OnStart(context.Background(), rootScope(t), func(RunInfo, agent.Agent) {
		global++
	}); err != nil {
		t.Fatalf("登记全局观察者失败：%v", err)
	}

	scopeless := agentAtDepth(t, "parent", 0)
	scopeless.scope = nil
	if _, err := runtime.Start(context.Background(), "spawn", StartRequest{Parent: scopeless}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	if global != 1 {
		t.Fatalf("全局层那个观察者该收到这条边，实际 %d", global)
	}
}

// ---- 「提供方来了」那条不被兜住的边 ----

// 一个观察者报错就当场停下：那次登记已经注定要卷回去，再往下通告没有意义。
func TestEmitProviderAddedStopsAtTheFirstFailure(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	ctx := context.Background()
	refusal := errors.New("不收")

	var later int
	if _, err := runtime.OnProviderAdded(ctx, owner, func(Provider) error { return refusal }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnProviderAdded(ctx, owner, func(Provider) error {
		later++
		return nil
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	_, err := runtime.RegisterProvider(ctx, owner, &fakeProvider{name: "spawn"})
	if !errors.Is(err, refusal) {
		t.Fatalf("观察者的错误该原样出来，实际 %v", err)
	}
	if later != 0 {
		t.Fatal("报错之后排在后面的观察者不该被叫到")
	}
	if _, found := runtime.GetProvider("spawn"); found {
		t.Fatal("这次登记该被卷回去")
	}
}

// 卷回去那一下会发「提供方被摘掉」，好让已经收到「来了」的观察者收支平衡。
func TestAFailedRegistrationAnnouncesTheRemoval(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	ctx := context.Background()

	var removed []string
	if _, err := runtime.OnProviderRemoved(ctx, owner, func(name string) {
		removed = append(removed, name)
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnProviderAdded(ctx, owner, func(Provider) error {
		return errors.New("不收")
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	if _, err := runtime.RegisterProvider(ctx, owner, &fakeProvider{name: "spawn"}); err == nil {
		t.Fatal("这次登记该失败")
	}
	if len(removed) != 1 || removed[0] != "spawn" {
		t.Fatalf("卷回去该通告一次摘除，实际 %#v", removed)
	}
}

// ---- 一次性运行的观察者 ----

// start 是同步发的，end 由那条 goroutine 在结局出来之后发。
func TestObserveRunEmitsStartSynchronouslyAndEndOnSettlement(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()
	release := make(chan struct{})
	ended := make(chan RunEndInfo, 1)

	var started []RunInfo
	if _, err := runtime.OnStart(ctx, rootScope(t), func(info RunInfo, _ agent.Agent) {
		started = append(started, info)
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnEnd(ctx, rootScope(t), func(info RunEndInfo, _ agent.Agent) { ended <- info }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	run := &fakeRun{
		id:      "child",
		release: release,
		result:  Result{StopReason: StopMaxTokens, Output: textContent("说完了")},
	}
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn", run: run})

	if _, err := runtime.Start(ctx, "spawn", StartRequest{}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	if len(started) != 1 || started[0].ID != "child" {
		t.Fatalf("start 该在开工返回之前就发出去，实际 %#v", started)
	}
	select {
	case leaked := <-ended:
		t.Fatalf("结局还没出来就不该发 end，实际 %#v", leaked)
	default:
	}

	close(release)
	end := <-ended
	if end.RunID != started[0].RunID {
		t.Fatalf("两条边该共用同一个运行身份，实际 %q 对 %q", end.RunID, started[0].RunID)
	}
	if end.StopReason != StopMaxTokens || textOf(end.LastAssistantMessage) != "说完了" {
		t.Fatalf("end 该带上运行自己的结局，实际 %#v", end)
	}
}

// 结局取不到时那条 end 照发，因为一条发出去的 start 必须有它配对的 end。
func TestObserveRunEmitsAnErrorEndWhenTheResultFails(t *testing.T) {
	runtime := newRuntime(t)
	ended := make(chan RunEndInfo, 1)
	if _, err := runtime.OnEnd(context.Background(), rootScope(t), func(info RunEndInfo, _ agent.Agent) {
		ended <- info
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	run := &fakeRun{id: "child", resultErr: errors.New("塌了")}
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn", run: run})

	if _, err := runtime.Start(context.Background(), "spawn", StartRequest{}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	if end := <-ended; end.StopReason != StopError {
		t.Fatalf("取不到结局该报 %s，实际 %s", StopError, end.StopReason)
	}
}

// 开工那个 ctx 被取消不该催出一条假的终止边：运行的所有权已经转给持有方了。
func TestObserveRunOutlivesTheStartContext(t *testing.T) {
	runtime := newRuntime(t)
	ended := make(chan RunEndInfo, 1)
	if _, err := runtime.OnEnd(context.Background(), rootScope(t), func(info RunEndInfo, _ agent.Agent) {
		ended <- info
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	release := make(chan struct{})
	run := &fakeRun{id: "child", release: release, result: Result{StopReason: StopCompleted}}
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn", run: run})

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := runtime.Start(ctx, "spawn", StartRequest{}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	cancel()

	close(release)
	if end := <-ended; end.StopReason != StopCompleted {
		t.Fatalf("开工 ctx 的取消不该改写结局，实际 %s", end.StopReason)
	}
}

// Local 记的是 start 兑现那一刻 LocalAgent 在不在。
func TestObserveRunRecordsWhetherTheChildIsLocal(t *testing.T) {
	runtime := newRuntime(t)
	started := make(chan RunInfo, 1)
	if _, err := runtime.OnStart(context.Background(), rootScope(t), func(info RunInfo, _ agent.Agent) {
		started <- info
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	local := newFakeAgent(t, "child", nil, "")
	run := &fakeRun{id: "child", local: local, result: Result{StopReason: StopCompleted}}
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn", run: run})

	if _, err := runtime.Start(context.Background(), "spawn", StartRequest{}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	if info := <-started; !info.Local || info.Provider != "spawn" {
		t.Fatalf("该记下这是个本地孩子、来自 spawn，实际 %#v", info)
	}
}

// ---- 可续活化的观察者 ----

// 这一轮的遥测只能来自它自己那截后缀：冷恢复会回放更早的回合，读整个会话会报出
// 上一轮的答案。
func TestActivationObserverReadsOnlyItsOwnEpoch(t *testing.T) {
	emitter, _ := newEmitter(t)
	earlier := append(
		steppedTurn(t, 1, session.MaxTokensTurnEnd{}),
		assistantMessage(t, 1, textContent("上一轮")),
	)
	child := &fakeAgent{
		id:      "child",
		scope:   keyedScope(t, "child", nil),
		session: newFreeSession(t, "child", "parent", earlier),
	}

	before := len(child.Session().Events())
	observer := newActivationObserver(emitter, "spawn", "child", nil)
	observer.start(child)
	if observer.boundary != before {
		t.Fatalf("边界该是这一轮开始那一刻的日志长度 %d，实际 %d", before, observer.boundary)
	}

	for _, event := range append(
		steppedTurn(t, 2, session.CompletedTurnEnd{}),
		assistantMessage(t, 2, textContent("这一轮")),
	) {
		appendEvent(t, child, event)
	}
	observer.capture(child)

	if observer.captured.StopReason != StopCompleted {
		t.Fatalf("该读这一轮自己的结局，实际 %s", observer.captured.StopReason)
	}
	if got := textOf(observer.captured.Output); got != "这一轮" {
		t.Fatalf("该读这一轮自己的输出，实际 %q", got)
	}
}

// 一个驻留过、但一个回合都没开的轮次拍出来的是「做完了」而不是「出错了」。
func TestActivationObserverCapturesAnEmptyEpochAsCompleted(t *testing.T) {
	emitter, _ := newEmitter(t)
	child := &fakeAgent{
		id:      "child",
		scope:   keyedScope(t, "child", nil),
		session: newFreeSession(t, "child", "parent", nil),
	}
	observer := newActivationObserver(emitter, "spawn", "child", nil)
	observer.start(child)
	observer.capture(child)

	if observer.captured.StopReason != StopCompleted || observer.captured.Output != nil {
		t.Fatalf("空轮次该是「做完了、没输出」，实际 %#v", observer.captured)
	}
}

// 拆解失败盖过这一轮自己的结局，并且扣下它的输出。
func TestActivationObserverTerminalIsOverriddenByATeardownFailure(t *testing.T) {
	emitter, _ := newEmitter(t)
	observer := newActivationObserver(emitter, "spawn", "child", nil)
	observer.captured = activationTerminal{StopReason: StopCompleted, Output: textContent("答案")}

	if got := observer.terminal(nil); got.StopReason != StopCompleted || textOf(got.Output) != "答案" {
		t.Fatalf("拆解成功该交出这一轮自己的事实，实际 %#v", got)
	}
	got := observer.terminal(errors.New("拆不掉"))
	if got.StopReason != StopError || got.Output != nil {
		t.Fatalf("拆解失败该盖过结局并扣下输出，实际 %#v", got)
	}
}

// settle 发出的那条边和这一轮那次 start 共用同一个运行身份。
func TestActivationObserverSettlePairsWithItsStart(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()
	var started []RunInfo
	var ended []RunEndInfo
	if _, err := runtime.OnStart(ctx, rootScope(t), func(info RunInfo, _ agent.Agent) {
		started = append(started, info)
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnEnd(ctx, rootScope(t), func(info RunEndInfo, _ agent.Agent) {
		ended = append(ended, info)
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	child := newFakeAgent(t, "child", nil, "parent")
	observer := runtime.observeActivation("spawn", "child", nil)
	observer.start(child)
	observer.capture(child)
	observer.settle(nil)

	if len(started) != 1 || len(ended) != 1 {
		t.Fatalf("该恰好各发一条，实际 start=%d end=%d", len(started), len(ended))
	}
	if ended[0].RunID != started[0].RunID {
		t.Fatalf("两条边该共用同一个运行身份，实际 %q 对 %q", ended[0].RunID, started[0].RunID)
	}
	if !started[0].Local || ended[0].Provider != "spawn" {
		t.Fatalf("可续孩子该报成本地的 spawn 孩子，实际 %#v / %#v", started[0], ended[0])
	}
}

// ---- 这一轮为什么结束 ----

func TestEpochStopReasonMapsEachTurnEndReason(t *testing.T) {
	cases := map[string]struct {
		reason session.TurnEndReason
		wanted StopReason
	}{
		"完成":   {session.CompletedTurnEnd{}, StopCompleted},
		"撞天花板": {session.MaxTokensTurnEnd{}, StopMaxTokens},
		"被停":   {session.AbortedTurnEnd{Reason: session.UserCancel{}}, StopAborted},
		"被打断":  {session.InterruptedTurnEnd{}, StopAborted},
		"出错":   {session.ErrorTurnEnd{}, StopError},
		"被拒":   {session.BlockedTurnEnd{}, StopRefusal},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := epochStopReason(steppedTurn(t, 1, testCase.reason)); got != testCase.wanted {
				t.Fatalf("该报 %s，实际 %s", testCase.wanted, got)
			}
		})
	}
}

// 「压根没有交代得了消耗的回合」和「干干净净地收尾」共用一条规矩。
func TestEpochStopReasonTreatsAnUnaccountedLogAsCompleted(t *testing.T) {
	if got := epochStopReason(nil); got != StopCompleted {
		t.Fatalf("空日志该报 %s，实际 %s", StopCompleted, got)
	}
	// 一个开了没进步骤、然后干净收尾的回合交代不了任何消耗。
	events := []session.Event{
		event(t, session.EventTurnStart, session.TurnStartData{Turn: 1}),
		turnEnd(t, 1, session.CompletedTurnEnd{}),
	}
	if got := epochStopReason(events); got != StopCompleted {
		t.Fatalf("交代不了消耗的日志该报 %s，实际 %s", StopCompleted, got)
	}
}

// 一次取消在任何回合开起来之前就把输入拿走了：没有哪条 turn/end 描述得了它，
// 只有收件箱那条记账说得出。
func TestEpochStopReasonReportsWorkDroppedBeforeAnyTurn(t *testing.T) {
	events := []session.Event{canceledSplice(t)}
	if got := epochStopReason(events); got != StopAborted {
		t.Fatalf("没跑就被丢掉的活儿该报 %s，实际 %s", StopAborted, got)
	}
}

// 一个干净收尾的回合**之后**又被取消掉的活儿，仍旧是这一轮被停了。
func TestEpochStopReasonReportsWorkDroppedAfterACleanTurn(t *testing.T) {
	events := append(steppedTurn(t, 1, session.CompletedTurnEnd{}), canceledSplice(t))
	if got := epochStopReason(events); got != StopAborted {
		t.Fatalf("收尾之后又被丢掉的活儿该报 %s，实际 %s", StopAborted, got)
	}
}

// 一次**记下来的失败**压过一次取消：停掉一个本来就已经失败的孩子，不会把它的
// 失败变成一次取消。
func TestEpochStopReasonKeepsARecordedFailureOverACancellation(t *testing.T) {
	events := append(steppedTurn(t, 1, session.ErrorTurnEnd{}), canceledSplice(t))
	if got := epochStopReason(events); got != StopError {
		t.Fatalf("记下来的失败该压过取消，实际 %s", got)
	}
}

// 一个说不出名字的结局当成成功，会把失败的活儿报成完成。
func TestEpochStopReasonRejectsAnUnknownReason(t *testing.T) {
	events := append(
		steppedTurn(t, 1, session.CompletedTurnEnd{})[:2],
		event(t, session.EventTurnEnd, map[string]any{"turn": 1, "reason": map[string]any{"kind": "未来的变体"}}),
	)
	if got := epochStopReason(events); got != StopError {
		t.Fatalf("不认识的结局该报 %s，实际 %s", StopError, got)
	}
}

// 读不回来的 turn/end 负载同样按失败处理。
func TestEpochStopReasonRejectsAnUnreadableTurnEnd(t *testing.T) {
	events := append(
		steppedTurn(t, 1, session.CompletedTurnEnd{})[:2],
		session.Event{Type: session.EventTurnEnd, Data: json.RawMessage(`{"turn":1,"reason":`)},
	)
	if got := epochStopReason(events); got != StopCompleted {
		t.Fatalf("读不回来的 turn/end 交代不了消耗，该退回 %s，实际 %s", StopCompleted, got)
	}
}

// ---- 本文件用到的小工具 ----

// canceledSplice 造一条「被取消、且什么都没留下」的收件箱改动。
func canceledSplice(t *testing.T) session.Event {
	t.Helper()
	return event(t, agent.EventInboxSpliced, agent.SplicedData{
		Target:       agent.NextTurn,
		RemovedCount: 1,
		Canceled:     true,
	})
}

// appendEvent 把一条事件追进孩子自己的日志。
func appendEvent(t *testing.T, child *fakeAgent, source session.Event) {
	t.Helper()
	if _, err := child.session.Append(source); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
}

// equalIDs 比两串会话身份。
func equalIDs(got, wanted []session.SessionID) bool {
	if len(got) != len(wanted) {
		return false
	}
	for index := range got {
		if got[index] != wanted[index] {
			return false
		}
	}
	return true
}
