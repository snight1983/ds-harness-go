// 本文件的作用：把这三道检查点的全部可观察行为钉住——什么时候刷、什么时候不刷、
// 以及刷不下去的时候下游到底有没有起步。
//
// # 这些测试防的是什么错
//
//   - **刷盘变成「顺便」的**。检查点没过还让适配器把请求发出去、还让工具体动手，
//     等于亲手制造一段没人记得的历史：钱花了、文件写了，而恢复出来的日志里没有
//     这回事。所以每一条失败用例都同时验两件事：报了错，**并且**下游一次都没起步。
//   - **该放过的地方也刷**。一次不属于任何会话的辅助调用、一次嵌套的子工具调用，
//     都没有新的东西可耐久。多刷一次是拿一次 fsync 换零信息，而 fsync 在热路径上。
//   - **步骤边界那一道刷失败却让步骤照进**。零值 [agent.PreStepDecision] 就是拒绝，
//     一处写错就变成在一份已经不可信的日志上继续往下写。
//   - **装到一半算装上了**。三条只装上两条比一条都没装更坏：调用方拿到的是错误，
//     不会去猜哪几条还留在上面。
//
// # 计时怎么做到不靠运气
//
// 不用 sleep。要观察「取消正好落在刷盘那一段里」时，取消动作写在**刷盘观察者
// 自己身上**——它一定在派发之前跑完，这是结构决定的，不是时长决定的。

package checkpointpolicy_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/checkpointpolicy"
)

// errFlush 是「刷盘刷不下去」这件事在测试里的样子。
var errFlush = errors.New("耐久层坏了")

// fakeAgent 是一个只回答三件事的发起者：身份、日志、作用域。
//
// 这一层只问这三件（Session() 是要刷的那份日志，Scope() 是登记要用的载体），
// 其余方法只为满足 [agent.Agent] 那份契约。模板取自 core/agentloop 的同名类型。
type fakeAgent struct {
	id    sessionlog.SessionID
	live  *session.Session
	owner *scope.Scope
}

func (a *fakeAgent) ID() sessionlog.SessionID  { return a.id }
func (a *fakeAgent) Options() agent.Options    { return agent.Options{} }
func (a *fakeAgent) Session() *session.Session { return a.live }
func (a *fakeAgent) Inbox() *agent.Inbox       { return nil }
func (a *fakeAgent) Status() agent.Status      { return agent.Status("") }
func (a *fakeAgent) Scope() *scope.Scope       { return a.owner }

func (a *fakeAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions)         {}
func (a *fakeAgent) WhenIdle(context.Context) error                                    { return nil }
func (a *fakeAgent) RunMaintenance(context.Context, func(context.Context) error) error { return nil }
func (a *fakeAgent) Send(llm.Message, agent.InboxTarget, bool)                         {}
func (a *fakeAgent) Followup(llm.Message)                                              {}
func (a *fakeAgent) Steer(llm.Message)                                                 {}
func (a *fakeAgent) Inject(llm.Message)                                                {}
func (a *fakeAgent) Prepend(llm.Message, agent.InboxTarget)                            {}

// recordingAdapter 是最里面那道适配器边界，只记自己被派发了几次。
//
// 它必须写成指针类型：[llm.Runtime] 拿两个 [llm.Adapter] 接口值直接比较，
// 一个带字段的结构体值参与比较是可以的，但本仓库别处的假适配器一律用指针，
// 这里跟着走以免将来加了函数字段就炸。
type recordingAdapter struct{ calls atomic.Int64 }

func (a *recordingAdapter) Stream(
	_ context.Context,
	_ llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	a.calls.Add(1)
	return func(yield func(llm.StreamChunk, error) bool) {
		yield(llm.FinishChunk{Reason: llm.StopFinish{}}, nil)
	}, nil
}

// flushHook 是一次刷盘真正要干的事，装在原子指针里。
//
// 要原子：[session.Store.Flush] 是在**另一个 goroutine** 里调观察者的，而测试
// 从自己那条 goroutine 上换钩子。
type flushHook struct {
	run func(context.Context, *session.Session) error
}

// world 是这一批用例的舞台：三个运行时、一个活会话、一个已登记的发起者，
// 外加一个能被测试摆布的刷盘观察者。
type world struct {
	owner       *scope.Scope
	store       *session.Store
	live        *session.Session
	llmRuntime  *llm.Runtime
	adapter     *recordingAdapter
	toolRuntime *tools.Runtime
	registry    *agent.Registry
	initiator   *fakeAgent
	// ctx 已经盖上了发起者，工具那一道要靠它找会话。
	ctx context.Context

	flushes atomic.Int64
	hook    atomic.Pointer[flushHook]
}

// newWorld 造一个装好了这三道检查点的舞台。
func newWorld(t *testing.T) *world {
	t.Helper()

	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })

	store, err := session.NewStore(session.StoreOptions{})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	live, err := store.Create(context.Background(), owner, "s1", session.CreateOptions{})
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	llmRuntime := llm.NewRuntime(llm.RuntimeOptions{})
	adapter := &recordingAdapter{}
	if _, err := llmRuntime.RegisterAdapter(
		context.Background(), owner, []string{"prov"}, adapter); err != nil {
		t.Fatalf("注册适配器失败：%v", err)
	}

	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}

	registry, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}

	w := &world{
		owner:       owner,
		store:       store,
		live:        live,
		llmRuntime:  llmRuntime,
		adapter:     adapter,
		toolRuntime: toolRuntime,
		registry:    registry,
		initiator:   &fakeAgent{id: live.ID(), live: live, owner: owner},
	}
	w.ctx = agent.WithInitiator(context.Background(), w.initiator)

	// 必须有观察者：一个观察者都没有的存储上 Flush 什么都不做也不报错，那样这
	// 一批用例全都会假绿。
	if _, err := store.OnFlush(context.Background(), owner,
		func(flushCtx context.Context, sess *session.Session) error {
			w.flushes.Add(1)
			if hook := w.hook.Load(); hook != nil {
				return hook.run(flushCtx, sess)
			}
			return nil
		}); err != nil {
		t.Fatalf("装刷盘观察者失败：%v", err)
	}

	detach, err := w.registry.Register(context.Background(), w.initiator, nil)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	undo, err := checkpointpolicy.Install(
		context.Background(), owner, store, llmRuntime, toolRuntime, registry)
	if err != nil {
		t.Fatalf("装检查点失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	return w
}

// setHook 换掉刷盘观察者要干的事。
func (w *world) setHook(run func(context.Context, *session.Session) error) {
	w.hook.Store(&flushHook{run: run})
}

// failFlush 让往后每一次刷盘都失败。
func (w *world) failFlush() {
	w.setHook(func(context.Context, *session.Session) error { return errFlush })
}

// stream 发一次带会话身份的模型请求。
func (w *world) stream(t *testing.T) (iter.Seq2[llm.StreamChunk, error], error) {
	t.Helper()
	return w.llmRuntime.Stream(w.ctx, llm.GenerateOptions{
		Provider:  "prov",
		Model:     "mod",
		SessionID: llm.SessionID(w.live.ID()),
	})
}

// drain 把一条流读到底，中途的失败当场报出来。
func drain(t *testing.T, stream iter.Seq2[llm.StreamChunk, error]) {
	t.Helper()
	for _, err := range stream {
		if err != nil {
			t.Fatalf("读流失败：%v", err)
		}
	}
}

// echoTool 造一个回声工具，body 非 nil 时在执行体里跑一趟。
func echoTool(
	name string,
	body func(ctx context.Context, exec *tools.RunContext) error,
) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: string(value)}}, nil
			},
		},
		Execute: func(ctx context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
			if body != nil {
				if err := body(ctx, exec); err != nil {
					return nil, err
				}
			}
			return json.Marshal(name)
		},
	}
}

// register 把一份工具定义装进这个舞台。
func (w *world) register(t *testing.T, definition *tools.Definition) {
	t.Helper()
	if _, err := w.toolRuntime.Register(context.Background(), w.owner, definition); err != nil {
		t.Fatalf("注册工具 %q 失败：%v", definition.Name, err)
	}
}

// call 造一份最小的顶层调用输入。
func call(name string) tools.ExecutionInput {
	return tools.ExecutionInput{
		CallID:    llm.CallID("c1"),
		Name:      name,
		Arguments: json.RawMessage(`{}`),
	}
}

// errorCode 取出一份失败结果的机器可读代号，没有身份就是空串。
func errorCode(result tools.Result) string {
	if result.Error == nil || result.Error.Info == nil {
		return ""
	}
	return result.Error.Info.Code
}

// preStep 跑一趟步骤准入瀑布，最里面那层永远说「进」。
func (w *world) preStep(t *testing.T) (agent.PreStepDecision, error) {
	t.Helper()
	return w.registry.ResolvePreStep(
		w.ctx,
		agent.PreStep{Agent: w.initiator, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.PreStepDecision{Enter: true}, nil
		})
}

// ---- 模型请求那一道 ----

// 刷盘要在 [llm.Runtime.Stream] **返回**的时候就已经做完，而不是拖到消费方来拉
// 第一块。这正是这一层和 DSH 那个 async generator 的差别，也是包文档承诺的那条。
func TestTheModelCheckpointFlushesBeforeStreamReturns(t *testing.T) {
	w := newWorld(t)

	stream, err := w.stream(t)
	if err != nil {
		t.Fatalf("请求发不出去：%v", err)
	}
	if got := w.flushes.Load(); got != 1 {
		t.Fatalf("Stream 返回时刷了 %d 次，要的是 1 次", got)
	}
	if got := w.adapter.calls.Load(); got != 0 {
		t.Fatalf("还没读流适配器就被派发了 %d 次", got)
	}
	drain(t, stream)
	if got := w.adapter.calls.Load(); got != 1 {
		t.Fatalf("读完流适配器被派发了 %d 次，要的是 1 次", got)
	}
}

// 刷不下去这次请求就一个字都不许发出去：报错，而且适配器根本没有被构造出来的
// 机会——返回的流是 nil，连读都读不了。
func TestAFailedFlushKeepsTheModelRequestFromLeaving(t *testing.T) {
	w := newWorld(t)
	w.failFlush()

	stream, err := w.stream(t)
	if err == nil {
		t.Fatal("刷盘失败了请求还是发出去了")
	}
	if !errors.Is(err, errFlush) {
		t.Fatalf("报出来的不是刷盘那条错：%v", err)
	}
	if stream != nil {
		t.Fatal("刷盘失败还交出了一条流")
	}
	if got := w.adapter.calls.Load(); got != 0 {
		t.Fatalf("适配器被派发了 %d 次，一次都不该有", got)
	}
}

// 一次不带会话身份的辅助调用（起标题、压缩摘要都是这样）没有日志可刷，原样放过。
func TestARequestWithoutASessionIsNotFlushed(t *testing.T) {
	w := newWorld(t)
	w.failFlush()

	stream, err := w.llmRuntime.Stream(w.ctx, llm.GenerateOptions{Provider: "prov", Model: "mod"})
	if err != nil {
		t.Fatalf("请求发不出去：%v", err)
	}
	drain(t, stream)
	if got := w.flushes.Load(); got != 0 {
		t.Fatalf("刷了 %d 次，一次都不该有", got)
	}
	if got := w.adapter.calls.Load(); got != 1 {
		t.Fatalf("适配器被派发了 %d 次，要的是 1 次", got)
	}
}

// 一个已经不在存储里活着的 id 同样没有日志可刷。这条和上一条分开写：它们走的是
// 规则里两个不同的分支，合成一条的话有一个坏了另一个会替它遮住。
func TestARequestNamingAnUnknownSessionIsNotFlushed(t *testing.T) {
	w := newWorld(t)
	w.failFlush()

	stream, err := w.llmRuntime.Stream(w.ctx, llm.GenerateOptions{
		Provider:  "prov",
		Model:     "mod",
		SessionID: llm.SessionID("从来没存在过"),
	})
	if err != nil {
		t.Fatalf("请求发不出去：%v", err)
	}
	drain(t, stream)
	if got := w.flushes.Load(); got != 0 {
		t.Fatalf("刷了 %d 次，一次都不该有", got)
	}
}

// ---- 顶层工具调用那一道 ----

func TestATopLevelToolCallFlushesThenRuns(t *testing.T) {
	w := newWorld(t)
	var entered atomic.Int64
	var flushesAtEntry atomic.Int64
	w.register(t, echoTool("probe", func(context.Context, *tools.RunContext) error {
		entered.Add(1)
		flushesAtEntry.Store(w.flushes.Load())
		return nil
	}))

	if result := w.toolRuntime.Execute(w.ctx, call("probe")); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if got := entered.Load(); got != 1 {
		t.Fatalf("执行体跑了 %d 次，要的是 1 次", got)
	}
	// 执行体进门时刷盘必须**已经**做完了，这就是「派发前耐久」那句话的全部内容。
	if got := flushesAtEntry.Load(); got != 1 {
		t.Fatalf("执行体进门时刷过 %d 次，要的是 1 次", got)
	}
}

func TestAFailedFlushKeepsTheToolBodyFromStarting(t *testing.T) {
	w := newWorld(t)
	var entered atomic.Int64
	w.register(t, echoTool("probe", func(context.Context, *tools.RunContext) error {
		entered.Add(1)
		return nil
	}))
	w.failFlush()

	result := w.toolRuntime.Execute(w.ctx, call("probe"))
	if !result.IsError {
		t.Fatal("刷盘失败了这次调用还是成功了")
	}
	if !strings.Contains(result.Error.Message, errFlush.Error()) {
		t.Fatalf("失败正文里没有刷盘那条因由：%q", result.Error.Message)
	}
	if got := entered.Load(); got != 0 {
		t.Fatalf("执行体跑了 %d 次，一次都不该有", got)
	}
}

// 一次工具调用在自己体内再派发工具时不再刷：外层进来时已经刷过了。
func TestANestedToolCallDoesNotFlushAgain(t *testing.T) {
	w := newWorld(t)
	var innerRan atomic.Int64
	w.register(t, echoTool("inner", func(context.Context, *tools.RunContext) error {
		innerRan.Add(1)
		return nil
	}))
	w.register(t, echoTool("outer", func(ctx context.Context, exec *tools.RunContext) error {
		nested := tools.ExecutionInput{
			CallID:    llm.CallID("c2"),
			Name:      "inner",
			Arguments: json.RawMessage(`{}`),
			Parent:    exec.Token,
		}
		if result := w.toolRuntime.Execute(ctx, nested); result.IsError {
			return errors.New("嵌套调用失败了：" + result.Error.Message)
		}
		return nil
	}))

	if result := w.toolRuntime.Execute(w.ctx, call("outer")); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if got := innerRan.Load(); got != 1 {
		t.Fatalf("里层跑了 %d 次，要的是 1 次", got)
	}
	if got := w.flushes.Load(); got != 1 {
		t.Fatalf("刷了 %d 次，要的是 1 次——里层不该再刷", got)
	}
}

// 认不出发起者就原样放过。这不是宽容：没有发起者就没有会话，也就没有日志可刷。
func TestAToolCallWithoutAnInitiatorIsNotFlushed(t *testing.T) {
	w := newWorld(t)
	var entered atomic.Int64
	w.register(t, echoTool("probe", func(context.Context, *tools.RunContext) error {
		entered.Add(1)
		return nil
	}))
	w.failFlush()

	if result := w.toolRuntime.Execute(context.Background(), call("probe")); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if got := w.flushes.Load(); got != 0 {
		t.Fatalf("刷了 %d 次，一次都不该有", got)
	}
	if got := entered.Load(); got != 1 {
		t.Fatalf("执行体跑了 %d 次，要的是 1 次", got)
	}
}

// 取消正好落在刷盘那一段里：这次调用换成规范的「派发前中止」，执行体一次都不起步。
//
// 取消动作写在刷盘观察者身上，所以「取消发生在刷完之后、派发之前」是结构保证的。
func TestACancellationDuringTheFlushAbortsBeforeDispatch(t *testing.T) {
	w := newWorld(t)
	var entered atomic.Int64
	w.register(t, echoTool("probe", func(context.Context, *tools.RunContext) error {
		entered.Add(1)
		return nil
	}))

	callCtx, cancel := context.WithCancel(w.ctx)
	defer cancel()
	w.setHook(func(context.Context, *session.Session) error {
		cancel()
		return nil
	})

	result := w.toolRuntime.Execute(callCtx, call("probe"))
	if !result.IsError {
		t.Fatal("取消之后这次调用还是成功了")
	}
	if got := errorCode(result); got != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("失败代号是 %q，要的是 %q", got, tools.CodeAbortedBeforeDispatch)
	}
	if got := entered.Load(); got != 0 {
		t.Fatalf("执行体跑了 %d 次，一次都不该有", got)
	}
}

// ---- 步骤边界那一道 ----

func TestThePreStepCheckpointFlushesThenDefersToNext(t *testing.T) {
	w := newWorld(t)

	decision, err := w.preStep(t)
	if err != nil {
		t.Fatalf("步骤准入失败：%v", err)
	}
	if !decision.Enter {
		t.Fatal("这一步该进")
	}
	if got := w.flushes.Load(); got != 1 {
		t.Fatalf("刷了 %d 次，要的是 1 次", got)
	}
}

// 刷不下去这一步就不进：交出来的是零值决定，也就是拒绝。
func TestAFailedFlushRejectsTheStep(t *testing.T) {
	w := newWorld(t)
	w.failFlush()

	decision, err := w.preStep(t)
	if err == nil {
		t.Fatal("刷盘失败了这一步还是进了")
	}
	if !errors.Is(err, errFlush) {
		t.Fatalf("报出来的不是刷盘那条错：%v", err)
	}
	if decision.Enter {
		t.Fatal("刷盘失败还说这一步可以进")
	}
}

// ---- 装与摘 ----

// 少了任何一个协作者都装不上。一个装了一半的检查点比装不上更坏，所以这几条
// 必须在装第一条**之前**就挡住。
func TestInstallRejectsMissingCollaborators(t *testing.T) {
	w := newWorld(t)

	for _, probe := range []struct {
		what string
		call func() (func(context.Context) error, error)
	}{
		{"作用域", func() (func(context.Context) error, error) {
			return checkpointpolicy.Install(context.Background(), nil,
				w.store, w.llmRuntime, w.toolRuntime, w.registry)
		}},
		{"会话存储", func() (func(context.Context) error, error) {
			return checkpointpolicy.Install(context.Background(), w.owner,
				nil, w.llmRuntime, w.toolRuntime, w.registry)
		}},
		{"llm 运行时", func() (func(context.Context) error, error) {
			return checkpointpolicy.Install(context.Background(), w.owner,
				w.store, nil, w.toolRuntime, w.registry)
		}},
		{"工具运行时", func() (func(context.Context) error, error) {
			return checkpointpolicy.Install(context.Background(), w.owner,
				w.store, w.llmRuntime, nil, w.registry)
		}},
		{"agent 注册表", func() (func(context.Context) error, error) {
			return checkpointpolicy.Install(context.Background(), w.owner,
				w.store, w.llmRuntime, w.toolRuntime, nil)
		}},
	} {
		t.Run(probe.what, func(t *testing.T) {
			undo, err := probe.call()
			if err == nil {
				t.Fatalf("少了%s还装上了", probe.what)
			}
			if undo != nil {
				t.Fatal("装不上还交出了摘除函数")
			}
		})
	}
}

// 摘下来之后三道全都不在了：模型请求不再刷，工具调用不再刷，步骤边界也不再刷。
func TestTheDisposerRemovesAllThreeCheckpoints(t *testing.T) {
	w := newWorld(t)
	w.register(t, echoTool("probe", nil))

	undo, err := checkpointpolicy.Install(
		context.Background(), w.owner, w.store, w.llmRuntime, w.toolRuntime, w.registry)
	if err != nil {
		t.Fatalf("再装一份失败：%v", err)
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}

	// 只剩 newWorld 装的那一份，所以每条边界上还是刚好刷一次。
	stream, err := w.stream(t)
	if err != nil {
		t.Fatalf("请求发不出去：%v", err)
	}
	drain(t, stream)
	if result := w.toolRuntime.Execute(w.ctx, call("probe")); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if _, err := w.preStep(t); err != nil {
		t.Fatalf("步骤准入失败：%v", err)
	}
	if got := w.flushes.Load(); got != 3 {
		t.Fatalf("三条边界一共刷了 %d 次，要的是 3 次——摘掉的那份还在刷", got)
	}
}

func (a *fakeAgent) Remove(llm.MessageID) {}

func (a *fakeAgent) Replace(llm.MessageID, llm.Message) {}
