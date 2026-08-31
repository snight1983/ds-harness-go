// 本文件验那个默认驱动本身：它怎么构造、怎么把输入排进收件箱的三条边界、
// 一个回合在日志上留下的那副骨架，以及取消、重试、撞上限这几条岔路。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515
//
// 整组用例一律**从真的循环走**：喂进去的是一段脚本化的模型流，验的是落在会话
// 日志上的事实。直接调那些不导出的函数只在它们本身就是一条独立判断时才做
//（[statusOf]、[cancelCauseOf]、[requestProposal] 这几个）。
//
// 派发不需要任何适配器：舞台在 [llm.Runtime] 上装了一条流规则，它不调 next 就
// 把脚本吐回去。于是 [ReactLoopAgent.buildRequest] 里那次 PrepareCall 必然报
// NO_ADAPTER，循环走的是 prepared == nil 那条路——也就是「中间件服务一条没登记的
// 路由」那一支。

package agentloop

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// loopSetup 是造一个舞台时那几处可调的地方，零值就是最平常的那一套。
type loopSetup struct {
	// create 是建那个活会话用的选项，用来喂种子日志。
	create session.CreateOptions
	// options 是这个 agent 的路由；零值时用 甲/m-1。
	options agent.Options
	// maxParallelToolCalls 接上那个并行池上限；为 nil 时循环用自己的默认值。
	maxParallelToolCalls func() int
}

// loopWorld 是这一批用例的舞台：五样运行期设施、一个活会话、一个装好的循环，
// 外加一段脚本化的模型流和几本流水账。
type loopWorld struct {
	owner   *scope.Scope
	agents  *agent.Registry
	store   *session.Store
	models  *llm.Runtime
	tools   *tools.Runtime
	prompts *systemprompt.Registry
	live    *session.Session
	loop    *ReactLoopAgent

	// mutex 护住下面那几本账：流规则跑在驱动那条 goroutine 上，而用例在自己
	// 那条上读它们。
	mutex sync.Mutex
	// script 按请求次序一份份被取走；取完之后一律回一句平常的文本。
	script [][]llm.StreamChunk
	// requests 是被派发出去的每一份请求，按次序。
	requests []llm.GenerateOptions
	// failures 是 agent/error 那条广播收到的每一次失败。
	failures []agent.TurnError
	// statuses 是对外可见的状态跃迁，按次序。
	statuses []agent.Status
	// onRequest 在一次请求的流**被交出去之前**调用，不持锁。
	onRequest func(index int)
	// onChunk 在每一个分块被下游吃掉之后调用，不持锁。
	onChunk func(position int)
}

// newLoopWorld 造一个舞台：五样设施、一个活会话、一个已经公布的循环。
//
// 循环必须公布（[agent.Registry.Announce]），否则注册表认不出它是活的，
// 每一条瀑布都会以 ErrAgentNotLive 收场——那不是任何一条用例想验的东西。
func newLoopWorld(t *testing.T, setup loopSetup) *loopWorld {
	t.Helper()
	ctx := context.Background()

	owner := rootScope(t)
	store := newStore(t)
	live := liveSession(t, store, owner, "s", setup.create)

	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompts, err := systemprompt.NewRegistry(ctx, owner, systemprompt.Options{OmitHarnessIdentity: true})
	if err != nil {
		t.Fatalf("造系统提示词注册表失败：%v", err)
	}

	world := &loopWorld{
		owner:   owner,
		agents:  agents,
		store:   store,
		models:  llm.NewRuntime(llm.RuntimeOptions{}),
		tools:   toolRuntime,
		prompts: prompts,
		live:    live,
	}

	detachStream, err := world.models.OnStream(ctx, owner, world.serve)
	if err != nil {
		t.Fatalf("装流规则失败：%v", err)
	}
	t.Cleanup(func() { _ = detachStream(ctx) })

	detachError, err := agents.OnError(ctx, owner, func(failure agent.TurnError) {
		world.mutex.Lock()
		defer world.mutex.Unlock()
		world.failures = append(world.failures, failure)
	})
	if err != nil {
		t.Fatalf("装错误观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detachError(ctx) })

	detachStatus, err := agents.OnStatus(ctx, owner, func(_ agent.Agent, status agent.Status) {
		world.mutex.Lock()
		defer world.mutex.Unlock()
		world.statuses = append(world.statuses, status)
	})
	if err != nil {
		t.Fatalf("装状态观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detachStatus(ctx) })

	options := setup.options
	if options.Provider == "" && options.Model == "" {
		options = agent.Options{Provider: "甲", Model: "m-1"}
	}
	loop, err := NewReactLoopAgent(ctx, world.deps(setup), nil, live.ID(), options, live)
	if err != nil {
		t.Fatalf("装循环失败：%v", err)
	}
	world.loop = loop

	detachAgent, err := agents.Enter(loop, nil)
	if err != nil {
		t.Fatalf("登记循环失败：%v", err)
	}
	t.Cleanup(func() {
		_ = detachAgent(ctx)
		_ = loop.Scope().Dispose(ctx)
	})
	if err := agents.Announce(ctx, loop); err != nil {
		t.Fatalf("公布循环失败：%v", err)
	}
	return world
}

// deps 把这个舞台上的五样设施装成一份依赖。
func (w *loopWorld) deps(setup loopSetup) Deps {
	return Deps{
		Agents:               w.agents,
		Sessions:             w.store,
		LLM:                  w.models,
		Tools:                w.tools,
		SystemPrompt:         w.prompts,
		MaxParallelToolCalls: setup.maxParallelToolCalls,
	}
}

// serve 是那条流规则：记下这次请求，按次序取一份脚本吐回去，不调 next。
func (w *loopWorld) serve(
	_ context.Context,
	options llm.GenerateOptions,
	_ func(context.Context) (iter.Seq2[llm.StreamChunk, error], error),
) (iter.Seq2[llm.StreamChunk, error], error) {
	w.mutex.Lock()
	index := len(w.requests)
	w.requests = append(w.requests, options)
	chunks := textReply("好")
	if index < len(w.script) {
		chunks = w.script[index]
	}
	onRequest, onChunk := w.onRequest, w.onChunk
	w.mutex.Unlock()

	// 在交出这条流**之前**调：一条想在任何分块之前取消的用例，靠的就是这个时机。
	if onRequest != nil {
		onRequest(index)
	}
	return func(yield func(llm.StreamChunk, error) bool) {
		for position, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
			// yield 返回之后这个分块已经落进日志、也已经喂给装配器了，
			// 所以在这里取消，验的正是「安全前缀」那条路。
			if onChunk != nil {
				onChunk(position)
			}
		}
	}, nil
}

// settle 等这个 agent 这一层的活动全部静下来。
func (w *loopWorld) settle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.loop.WhenIdle(ctx); err != nil {
		t.Fatalf("等 agent 静下来失败：%v", err)
	}
}

// run 送一条后续回合的输入，然后等它跑完。
func (w *loopWorld) run(t *testing.T, text string) {
	t.Helper()
	w.loop.Followup(userMessage(text))
	w.settle(t)
}

// requestsSent 交出被派发出去的那些请求的一份快照。
func (w *loopWorld) requestsSent() []llm.GenerateOptions {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return append([]llm.GenerateOptions(nil), w.requests...)
}

// reportedFailures 交出 agent/error 那条广播收到的失败的一份快照。
func (w *loopWorld) reportedFailures() []agent.TurnError {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return append([]agent.TurnError(nil), w.failures...)
}

// setScript 摆好接下来几次请求各自的那段流。
func (w *loopWorld) setScript(script ...[]llm.StreamChunk) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.script = script
}

// ---- 造分块的那几个 ----

// userMessage 造一条平常的用户消息。
func userMessage(text string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
}

// textReply 是一段吐完一句话就正常收尾的流。
func textReply(text string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: text},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
}

// toolReply 是一段要求调一次工具的流。
func toolReply(id llm.CallID, name string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.ToolCallDeltaChunk{Index: 0, ID: id, Name: &name, ArgumentsDelta: "{}"},
		llm.FinishChunk{Reason: llm.ToolCallsFinish{}},
	}
}

// maxTokensReply 是一段撞上输出上限的流。
func maxTokensReply(text string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: text},
		llm.FinishChunk{Reason: llm.MaxTokensFinish{}},
	}
}

// errorReply 是一段以失败收尾的流。
func errorReply(message string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.FinishChunk{Reason: llm.ErrorFinish{Failure: llm.Failure{Message: message, Code: "BOOM"}}},
	}
}

// ---- 读日志的那几个 ----

// turnEndReasons 按次序取出每一条 turn/end 的结束理由。
func turnEndReasons(t *testing.T, live *session.Session) []sessionlog.TurnEndReason {
	t.Helper()
	var reasons []sessionlog.TurnEndReason
	for _, event := range live.Events() {
		if event.Type != sessionlog.EventTurnEnd {
			continue
		}
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			t.Fatalf("turn/end 读不回来：%v", err)
		}
		payload, ok := data.(sessionlog.TurnEndData)
		if !ok {
			t.Fatalf("turn/end 解出来不是回合负载：%#v", data)
		}
		reasons = append(reasons, payload.Reason)
	}
	return reasons
}

// onlyTurnEndKind 断言日志里恰好一条 turn/end，并交出它那个判别标签。
func onlyTurnEndKind(t *testing.T, live *session.Session) sessionlog.TurnEndReasonKind {
	t.Helper()
	reasons := turnEndReasons(t, live)
	if len(reasons) != 1 {
		t.Fatalf("该恰好关掉一个回合，实际 %d 个", len(reasons))
	}
	return reasons[0].TurnEndReasonKind()
}

// countEvents 数某一类事件出现了几次。
func countEvents(live *session.Session, want sessionlog.EventType) int {
	count := 0
	for _, event := range live.Events() {
		if event.Type == want {
			count++
		}
	}
	return count
}

// assistantMessages 按次序取出每一条助手消息的负载。
func assistantMessages(t *testing.T, live *session.Session) []sessionlog.AssistantMessageData {
	t.Helper()
	var messages []sessionlog.AssistantMessageData
	for _, event := range live.Events() {
		if event.Type != sessionlog.EventAssistantMessage {
			continue
		}
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			t.Fatalf("assistant/message 读不回来：%v", err)
		}
		payload, ok := data.(sessionlog.AssistantMessageData)
		if !ok {
			t.Fatalf("assistant/message 解出来不是助手消息负载：%#v", data)
		}
		messages = append(messages, payload)
	}
	return messages
}

// ---- 构造 ----

// TestNewReactLoopAgentNeedsEveryCollaborator 钉住五样设施、一个活会话、一个对得上
// 的身份，缺一不可。
//
// 这几样每一样都是循环在跑到一半时才会用到的：少了任何一样，失败会在第一个回合
// 中途冒出来，而那时日志上已经开了一个关不掉的回合。在构造这道边界上拦下来，
// 是唯一能让「装得上」等于「跑得动」的地方。
func TestNewReactLoopAgentNeedsEveryCollaborator(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	ctx := context.Background()
	full := world.deps(loopSetup{})
	options := agent.Options{Provider: "甲", Model: "m-1"}

	missing := map[string]func(Deps) Deps{
		"没有 agent 注册表": func(d Deps) Deps { d.Agents = nil; return d },
		"没有会话存储":       func(d Deps) Deps { d.Sessions = nil; return d },
		"没有模型运行时":      func(d Deps) Deps { d.LLM = nil; return d },
		"没有工具运行时":      func(d Deps) Deps { d.Tools = nil; return d },
		"没有系统提示词":      func(d Deps) Deps { d.SystemPrompt = nil; return d },
	}
	for label, strip := range missing {
		t.Run(label, func(t *testing.T) {
			if _, err := NewReactLoopAgent(
				ctx, strip(full), nil, world.live.ID(), options, world.live); err == nil {
				t.Errorf("%s 该被拒", label)
			}
		})
	}

	if _, err := NewReactLoopAgent(ctx, full, nil, world.live.ID(), options, nil); err == nil {
		t.Error("没有活会话该被拒")
	}
	if _, err := NewReactLoopAgent(ctx, full, nil, "另一个", options, world.live); err == nil {
		t.Error("agent 身份和会话身份对不上该被拒")
	}
}

// TestLastTurnOfReadsTheLatestTurnStart 钉住恢复出来的回合号取的是**最后**一条
// turn/start，不是第一条。
//
// 源: packages/core/agent-loop/src/agent.ts:92
//
// 取错了的话，一个恢复出来的循环会从一个已经用过的回合号接着往下开，于是日志里
// 出现两个同号的回合——从那以后，任何按回合号归拢事件的读法都是错的。
func TestLastTurnOfReadsTheLatestTurnStart(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	owner := rootScope(t)

	empty := liveSession(t, store, owner, "空的", session.CreateOptions{})
	if last, err := lastTurnOf(empty); err != nil || last != 0 {
		t.Fatalf("一条 turn/start 都没有时该是 0：last=%d err=%v", last, err)
	}

	live := liveSession(t, store, owner, "跑过的", session.CreateOptions{Seed: seedOf(
		turnStartEvent(t, 1),
		turnStartEvent(t, 2),
		foreignUserEvent(t, "后面还有别的事件"),
	)})
	last, err := lastTurnOf(live)
	if err != nil {
		t.Fatalf("读最后一个回合号失败：%v", err)
	}
	if last != 2 {
		t.Errorf("该读到 2，实际 %d", last)
	}
}

// turnStartEvent 造一条回合开始事件。
func turnStartEvent(t *testing.T, turn int) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: mustData(t, sessionlog.TurnStartData{Turn: turn}),
	}
}

// TestALoopResumesFromTheLastTurnInItsLog 钉住恢复出来的循环接着上一个回合号往下开。
//
// 这是上一条那个纯函数在真实路径上的落点：种子日志里已经有一个回合 3，那么这个
// 循环开的第一个回合必须是 4。
func TestALoopResumesFromTheLastTurnInItsLog(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{create: session.CreateOptions{Seed: seedOf(
		turnStartEvent(t, 3),
	)}})
	world.run(t, "接着来")

	var turns []int
	for _, event := range world.live.Events() {
		if event.Type != sessionlog.EventTurnStart {
			continue
		}
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			t.Fatalf("turn/start 读不回来：%v", err)
		}
		turns = append(turns, data.(sessionlog.TurnStartData).Turn)
	}
	if len(turns) != 2 || turns[1] != 4 {
		t.Errorf("该接着开第 4 个回合，实际 %v", turns)
	}
}

// TestStatusOfProjectsTheRunningPhaseOnly 钉住只有跑回合那一相对外是 running。
//
// 源: packages/core/agent-loop/src/agent.ts:99-101
//
// 维护活儿故意不算 running：它不是一个回合，把它报成 running 会让每一个等
// 「模型答完了没有」的外部观察者误判。
func TestStatusOfProjectsTheRunningPhaseOnly(t *testing.T) {
	t.Parallel()

	if got := statusOf(phase{kind: phaseIdle}); got != agent.StatusIdle {
		t.Errorf("空闲该是 idle，实际 %q", got)
	}
	if got := statusOf(phase{kind: phaseMaintenance}); got != agent.StatusIdle {
		t.Errorf("维护该是 idle，实际 %q", got)
	}
	if got := statusOf(phase{kind: phaseRunning}); got != agent.StatusRunning {
		t.Errorf("跑回合该是 running，实际 %q", got)
	}
}

// TestCancelErrorNamesItsCause 钉住那条错误话里点得出取消的来路。
//
// 它是取消原因骑在 [context.CancelCauseFunc] 上的唯一载体：一条不说来路的错误，
// 在日志和诊断里就分不出「使用者按了停」和「载体被销毁了」。
func TestCancelErrorNamesItsCause(t *testing.T) {
	t.Parallel()

	carried := (&CancelError{Cause: sessionlog.UserCancel{}}).Error()
	if carried != "core/agentloop: 回合被取消（user）" {
		t.Errorf("该点出 user 这条来路，实际 %q", carried)
	}
	if bare := (&CancelError{}).Error(); bare == "" {
		t.Error("没有原因时也该有一句话")
	}
}

// TestCancelCauseOfIgnoresForeignCancellations 钉住只有本包包过的那种取消才交得出
// 结构化原因。
//
// 别人的取消（一次外层超时、一条 context.Canceled）在日志里不是 aborted：
// 认下它等于伪造一条本包从没写过的取消来路。
func TestCancelCauseOfIgnoresForeignCancellations(t *testing.T) {
	t.Parallel()

	own, cancelOwn := context.WithCancelCause(context.Background())
	cancelOwn(&CancelError{Cause: sessionlog.UserCancel{}})
	cause, ok := cancelCauseOf(own)
	if !ok || cause.CancelCauseKind() != sessionlog.CancelUser {
		t.Errorf("本包的取消该交出 user：cause=%#v ok=%v", cause, ok)
	}

	foreign, cancelForeign := context.WithCancelCause(context.Background())
	cancelForeign(errors.New("别人关的"))
	if _, ok := cancelCauseOf(foreign); ok {
		t.Error("别人的取消不该被认成本包的")
	}

	if _, ok := cancelCauseOf(context.Background()); ok {
		t.Error("没取消时不该交出原因")
	}
}

// TestAbortedErrIsQuietUntilCancelled 钉住 [abortedErr] 只在真取消了时才出声，
// 而且带着原因。
func TestAbortedErrIsQuietUntilCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	if err := abortedErr(ctx); err != nil {
		t.Errorf("没取消时该是 nil，实际 %v", err)
	}
	carrier := &CancelError{Cause: sessionlog.UserCancel{}}
	cancel(carrier)
	if err := abortedErr(ctx); !errors.Is(err, error(carrier)) {
		t.Errorf("取消之后该交出那份原因，实际 %v", err)
	}
}

// TestMaxParallelToolCallsFallsBackToThePackageDefault 钉住没接设置时用本包的默认值。
//
// 装配循环的那一层可以不认识设置系统（见 [Deps.MaxParallelToolCalls] 的注释），
// 那时这个上限必须仍然是一个正数——为 0 的话一次工具调用都派不出去。
func TestMaxParallelToolCallsFallsBackToThePackageDefault(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	if got := world.loop.maxParallelToolCalls(); got != DefaultMaxParallelToolCalls {
		t.Errorf("该用默认值 %d，实际 %d", DefaultMaxParallelToolCalls, got)
	}

	wired := newLoopWorld(t, loopSetup{maxParallelToolCalls: func() int { return 3 }})
	if got := wired.loop.maxParallelToolCalls(); got != 3 {
		t.Errorf("接上之后该读出 3，实际 %d", got)
	}
}

// TestRequestProposalStripsAdapterSuppliedDefaults 钉住被标记成适配器默认的那几项
// 在下一次提议里被摘掉。
//
// 源: packages/core/agent-loop/src/agent.ts:54-61
//
// 不摘的话，一个适配器按确切模型填出来的上限会在下一次请求里冒充成调用方的选择，
// 于是换了模型也换不掉它——而日志上看起来像是使用者自己挑的。
func TestRequestProposalStripsAdapterSuppliedDefaults(t *testing.T) {
	t.Parallel()

	header := sessionlog.EpochHeader{
		Config: llm.CallConfig{
			Provider: "甲", Model: "m-1", ReasoningEffort: "high", MaxTokens: 1024,
		},
	}

	kept := requestProposal(header)
	if kept.ReasoningEffort != "high" || kept.MaxTokens != 1024 {
		t.Errorf("没标记成适配器默认的该原样留着，实际 %#v", kept)
	}

	header.AdapterDefaults.ReasoningEffort = true
	header.AdapterDefaults.MaxTokens = true
	stripped := requestProposal(header)
	if stripped.ReasoningEffort != "" || stripped.MaxTokens != 0 {
		t.Errorf("标记过的两项该被摘掉，实际 %#v", stripped)
	}
	if stripped.Provider != "甲" || stripped.Model != "m-1" {
		t.Errorf("路由不该被动，实际 %#v", stripped)
	}
}

// ---- 收件箱那三条边界 ----

// TestSendTargetsTheRightInboxBoundary 钉住 Followup／Steer／Inject 各自落在哪条队列。
//
// 源: packages/core/agent-loop/src/agent.ts:122-132
//
// 这三个分别是「另开一个回合」「插进这一轮的下一步」「悄悄垫一份上下文」。
// 落错队列的后果不是报错而是**时机错**：一条本该等下一个回合的提示挤进当前这一步，
// 模型会在还欠着工具结果的时候看见它。
//
// 断言从一件维护活儿**里面**做：那时这个 agent 不是 idle，于是唤醒只上膛不起跑，
// 收件箱在断言的这一刻不会被一个正在跑的驱动搬空。
func TestSendTargetsTheRightInboxBoundary(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Followup(userMessage("下一个回合"))
		world.loop.Steer(userMessage("这一步"))
		world.loop.Inject(userMessage("垫一份"))

		nextTurn := world.loop.Inbox().NextTurn()
		if len(nextTurn) != 1 || textOf(t, nextTurn[0]) != "下一个回合" {
			t.Errorf("Followup 该排进 next-turn，实际 %#v", nextTurn)
		}
		nextStep := world.loop.Inbox().NextStep()
		if len(nextStep) != 2 ||
			textOf(t, nextStep[0]) != "这一步" || textOf(t, nextStep[1]) != "垫一份" {
			t.Errorf("Steer 和 Inject 该按次序排进 next-step，实际 %#v", nextStep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)
}

// TestPrependPutsTheMessageAtTheHeadOfTheQueue 钉住 Prepend 放在**队头**而不是队尾。
//
// 新增: DSH 没有这个方法（它那些插件直接改 `agent.inbox`）。它存在的理由是「一个
// 步骤被拒了，它认领走的那批消息得原样放回去」——那批消息比队里现有的任何一条都
// 先到，接回队尾会把人写话的先后颠倒过来。所以次序本身就是这个方法的全部意义，
// 这条断言就是它的契约。
//
// 同样从一件维护活儿里面做断言，理由见 [TestSendTargetsTheRightInboxBoundary]。
func TestPrependPutsTheMessageAtTheHeadOfTheQueue(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Steer(userMessage("后到的"))
		world.loop.Prepend(userMessage("先到的"), agent.NextStep)

		nextStep := world.loop.Inbox().NextStep()
		if len(nextStep) != 2 ||
			textOf(t, nextStep[0]) != "先到的" || textOf(t, nextStep[1]) != "后到的" {
			t.Errorf("Prepend 该落在队头，实际 %#v", nextStep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)
}

// TestPrependReportsWhatTheInboxRefused 钉住一次放不进去的 Prepend 走出错观察者。
//
// 新增: 这个方法没有返回值——它的每一个调用点都是观察者，没有一条能接住错误。
// 所以拒收必须从出错那条边出去；吞掉的话，一批放不回去的消息会无声无息地消失。
func TestPrependReportsWhatTheInboxRefused(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	duplicate := userMessage("同一个身份放两遍")
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Prepend(duplicate, agent.NextStep)
		world.loop.Prepend(duplicate, agent.NextStep)
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)

	if got := len(world.loop.Inbox().NextStep()); got != 1 {
		t.Fatalf("队里有 %d 条，本该只放进去一条", got)
	}
	failures := world.reportedFailures()
	if len(failures) != 1 {
		t.Fatalf("出错那条边上报了 %d 次，本该正好一次", len(failures))
	}
	if !strings.Contains(failures[0].Err.Error(), "往收件箱队头放消息失败") {
		t.Fatalf("报的是 %v", failures[0].Err)
	}
}

// TestSendAfterAnAbortRetargetsToTheNextTurn 钉住一次送进已被取消的活动的唤醒
// 改排到下一个回合。
//
// 源: packages/core/agent-loop/src/agent.ts:113-120
//
// 一段已经被取消的活动接不下任何新活儿：把这条引导留在 next-step 上，它会一直
// 等一个永远不会再来的步骤边界。改排到 next-turn，那次上膛的唤醒收敛时它就跑得上。
func TestSendAfterAnAbortRetargetsToTheNextTurn(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		// 先把这段活动自己取消掉，收件箱留着。
		world.loop.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{KeepInbox: true})
		world.loop.Steer(userMessage("取消之后才来的"))

		if got := world.loop.Inbox().NextStep(); len(got) != 0 {
			t.Errorf("取消之后不该再往 next-step 上排，实际 %#v", got)
		}
		nextTurn := world.loop.Inbox().NextTurn()
		if len(nextTurn) != 1 || textOf(t, nextTurn[0]) != "取消之后才来的" {
			t.Errorf("该改排进 next-turn，实际 %#v", nextTurn)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)
}

// TestCancelEmptiesTheInboxUnlessAskedToKeepIt 钉住取消默认清空收件箱，KeepInbox
// 时留着。
//
// 源: packages/core/agent-loop/src/agent.ts:134-140
//
// 默认清空是取消的一部分：留着的话，下一次唤醒会把使用者刚刚喊停的那些活儿
// 原样再跑一遍。KeepInbox 是给「只想打断这一轮、活儿还要」的调用方留的。
func TestCancelEmptiesTheInboxUnlessAskedToKeepIt(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Followup(userMessage("排着的"))
		world.loop.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{KeepInbox: true})
		if !world.loop.Inbox().HasPending() {
			t.Error("KeepInbox 时该留着")
		}
		world.loop.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
		if world.loop.Inbox().HasPending() {
			t.Error("默认该清空")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)
}

// ---- 维护活儿 ----

// TestRunMaintenanceRefusesANilTaskAndABusyAgent 钉住维护活儿要有正文，而且只能从
// 真正的空闲期起跑。
//
// 源: packages/core/agent-loop/src/agent.ts:142-162
//
// 它和一个回合共用同一相：让第二件活儿挤进来，两边会互相盖掉对方的取消口，
// 于是谁都取消不掉谁。
func TestRunMaintenanceRefusesANilTaskAndABusyAgent(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	if err := world.loop.RunMaintenance(context.Background(), nil); err == nil {
		t.Error("nil 活儿该被拒")
	}

	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		if err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
			return nil
		}); err == nil {
			t.Error("已经有活儿在跑时该被拒")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
}

// TestRunMaintenanceHandsBackTheTaskError 钉住活儿自己那条错误原样交回调用方。
//
// 维护活儿不是回合：它的失败不写 turn/end、也不广播 agent/error，唯一的去处就是
// 这个返回值。吞掉它，调用方会以为活儿干成了。
func TestRunMaintenanceHandsBackTheTaskError(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	sentinel := errors.New("活儿没干成")
	if err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Errorf("该原样交回那条错误，实际 %v", err)
	}
	if got := len(world.reportedFailures()); got != 0 {
		t.Errorf("维护活儿的失败不该广播成 agent/error，实际 %d 条", got)
	}
}

// TestRunMaintenanceReplaysTheWakeItHeldBack 钉住维护期间被上膛的那次唤醒在活儿
// 收敛之后放出来。
//
// 源: packages/core/agent-loop/src/agent.ts:164-193
//
// 送不到就丢掉的话，一条在维护窗口里到达的输入会永远躺在收件箱里——从外面看
// 就是「发过去了但 agent 一直没动」。
func TestRunMaintenanceReplaysTheWakeItHeldBack(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Followup(userMessage("维护期间来的"))
		if got := countEvents(world.live, sessionlog.EventTurnStart); got != 0 {
			t.Errorf("维护期间不该开回合，实际 %d 个", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)

	if got := countEvents(world.live, sessionlog.EventTurnStart); got != 1 {
		t.Fatalf("收敛之后该补上那一个回合，实际 %d 个", got)
	}
	if got := onlyTurnEndKind(t, world.live); got != sessionlog.ReasonCompleted {
		t.Errorf("那个回合该正常收尾，实际 %q", got)
	}
}

// TestWhenIdleReturnsAtOnceWhenNothingIsRunning 钉住没有活动时 [WhenIdle] 不等。
//
// 源: packages/core/agent-loop/src/agent.ts:195-200
func TestWhenIdleReturnsAtOnceWhenNothingIsRunning(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := world.loop.WhenIdle(ctx); err != nil {
		t.Errorf("空闲时该立刻返回，实际 %v", err)
	}
}

// TestWhenIdleHonoursTheCallersCancellation 钉住等待本身可以被调用方取消。
//
// 那个 ctx 是**等待**的取消口，不是这个 agent 的：取消它只是不等了，跑着的活儿
// 照跑。少了这条，一个等错了对象的调用方会挂到天荒地老。
func TestWhenIdleHonoursTheCallersCancellation(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	release := make(chan struct{})
	waited := make(chan error, 1)

	// 维护这一相对外仍然报 idle（见 [statusOf]），所以「这个 agent 忙起来了没有」
	// 不能靠轮询 [ReactLoopAgent.Status] 来判——那样只会一直读到 idle。这里由
	// 那件活儿自己在进门时说一声。
	entered := make(chan struct{})
	go func() {
		_ = world.loop.RunMaintenance(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	go func() { waited <- world.loop.WhenIdle(ctx) }()
	cancel()

	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("该交出调用方那次取消，实际 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消之后 WhenIdle 该立刻回来")
	}

	close(release)
	world.settle(t)
}

// ---- 一个回合的骨架 ----

// TestOneTurnWritesTheDurableSkeleton 钉住一个平常的回合在日志上留下的那副骨架，
// 按次序一条不差。
//
// 这副骨架就是重放的全部依据：回合和步骤的边界、模型看到的那条输入、这一程的
// 请求头与路由元数据、每一个分块、定稿的助手消息。少任何一条，一段日志都重放
// 不出同一次调用；次序错了，读日志的人会把因果关系读反。
//
// 两条 agent/inbox/spliced 夹在里面不是噪声：收件箱的耐久事实就是这些事件
// （见 [agent.Inbox] 的说明），内存里那两条队列只是它们的投影。第一条是
// [ReactLoopAgent.Followup] 把话塞进来，落在回合开始**之前**——那句话在这个
// 回合被立起来之前就已经在排队了；第二条是 [ReactLoopAgent.preStep] 认领它，
// 落在步骤开始**之前**——认领发生在这一步真正开张之前。次序反过来的话，重放
// 的一方会读成「回合先开了，话才进来」。
func TestOneTurnWritesTheDurableSkeleton(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript(textReply("你好"))
	world.run(t, "在么")

	want := []sessionlog.EventType{
		agent.EventInboxSpliced,
		sessionlog.EventTurnStart,
		agent.EventInboxSpliced,
		sessionlog.EventStepStart,
		sessionlog.EventUserMessage,
		sessionlog.EventRequestHeader,
		sessionlog.EventRequestContext,
		sessionlog.EventAssistantChunk,
		sessionlog.EventAssistantChunk,
		sessionlog.EventAssistantMessage,
		sessionlog.EventStepEnd,
		sessionlog.EventTurnEnd,
	}
	got := eventTypes(world.live)
	if len(got) != len(want) {
		t.Fatalf("事件条数对不上：想要 %v，实际 %v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 条事件对不上：想要 %q，实际 %q", index, want[index], got[index])
		}
	}
	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonCompleted {
		t.Errorf("该正常收尾，实际 %q", kind)
	}

	messages := assistantMessages(t, world.live)
	if len(messages) != 1 || textOf(t, messages[0].Message) != "你好" {
		t.Fatalf("该定稿出那一句回答，实际 %#v", messages)
	}
	if messages[0].Interrupted {
		t.Error("这条消息不是被打断的")
	}

	requests := world.requestsSent()
	if len(requests) != 1 {
		t.Fatalf("该只发一次请求，实际 %d 次", len(requests))
	}
	if !requests[0].AgentLoop {
		t.Error("循环发的请求该带上 AgentLoop 标记")
	}
	if requests[0].SessionID != llm.SessionID(world.live.ID()) {
		t.Errorf("该带上会话身份，实际 %q", requests[0].SessionID)
	}
	if requests[0].Provider != "甲" || requests[0].Model != "m-1" {
		t.Errorf("该按 agent.Options 那条路由发，实际 %q/%q", requests[0].Provider, requests[0].Model)
	}
}

// TestTheStatusGoesRunningThenIdleAroundATurn 钉住一个回合两侧各报一次状态跃迁。
//
// 外面那些「模型在不在答」的指示全靠这两次广播。少了收尾那一次，界面会一直停在
// 转圈上；少了起跑那一次，使用者按下发送之后看不到任何反应。
func TestTheStatusGoesRunningThenIdleAroundATurn(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.run(t, "在么")

	world.mutex.Lock()
	got := append([]agent.Status(nil), world.statuses...)
	world.mutex.Unlock()

	want := []agent.Status{agent.StatusRunning, agent.StatusIdle}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("状态跃迁该是 %v，实际 %v", want, got)
	}
}

// TestAVetoedPreStepClosesTheTurnAsBlocked 钉住准入被否时这个回合一步都不进，
// 而且以 blocked 收场。
//
// 源: packages/core/agent-loop/src/agent.ts:262-315
//
// 这个回合的开场边界已经在日志上了，所以它必须被关掉；关成 completed 的话，
// 读日志的人会以为模型答过了。
func TestAVetoedPreStepClosesTheTurnAsBlocked(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	detach, err := world.agents.OnPreStep(context.Background(), world.owner,
		func(context.Context, agent.PreStep, func(context.Context) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
			return agent.RejectStep(), nil
		})
	if err != nil {
		t.Fatalf("装准入观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	world.run(t, "会被拦下来")

	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonBlocked {
		t.Errorf("该以 blocked 收场，实际 %q", kind)
	}
	if got := countEvents(world.live, sessionlog.EventStepStart); got != 0 {
		t.Errorf("一个步骤都不该开，实际 %d 个", got)
	}
	if got := len(world.requestsSent()); got != 0 {
		t.Errorf("不该发请求，实际 %d 次", got)
	}
}

// TestAnEmptyFirstStepClosesTheTurnWithoutAModelCall 钉住第一步被改写成空时，
// 这个回合正常收尾但不花一次模型调用。
//
// 源: packages/core/agent-loop/src/agent.ts:271-274
//
// 一条被撤走的唤醒消息就是这个样子。它和被否的准入不一样：那个回合没有被拦，
// 只是没有话要说——所以是 completed 不是 blocked，而且一次请求都不该发出去。
func TestAnEmptyFirstStepClosesTheTurnWithoutAModelCall(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	detach, err := world.agents.OnPreStep(context.Background(), world.owner,
		func(context.Context, agent.PreStep, func(context.Context) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
			return agent.EnterStep(nil), nil
		})
	if err != nil {
		t.Fatalf("装准入观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	world.run(t, "会被撤走")

	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonCompleted {
		t.Errorf("该正常收尾，实际 %q", kind)
	}
	if got := len(world.requestsSent()); got != 0 {
		t.Errorf("不该发请求，实际 %d 次", got)
	}
}

// TestMaxTokensStaysStickyAcrossLaterSteps 钉住撞过上限的回合不会被后面正常收尾的
// 步骤降回 completed。
//
// 源: packages/core/agent-loop/src/agent.ts:296-299
//
// 这个结论是给上层看的：一个被截断的回合意味着输出不完整。让最后一步说了算的话，
// 一次「撞上限 → 观察者补一句 → 正常收尾」的回合在日志里读起来完全正常，
// 而模型其实中途被砍过一刀。
func TestMaxTokensStaysStickyAcrossLaterSteps(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript(maxTokensReply("说了一半"), textReply("补完了"))

	var once sync.Once
	detach, err := world.agents.OnTurnStopping(context.Background(), world.owner,
		func(_ context.Context, running agent.Agent, _ int) error {
			once.Do(func() { running.Inject(userMessage("接着说")) })
			return nil
		})
	if err != nil {
		t.Fatalf("装收尾观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	world.run(t, "讲个长的")

	if got := len(world.requestsSent()); got != 2 {
		t.Fatalf("该跑两步，实际 %d 次请求", got)
	}
	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonMaxTokens {
		t.Errorf("该粘在 max-tokens 上，实际 %q", kind)
	}
}

// TestToolCallsDriveASecondStep 钉住一步里的工具调用会把这个回合推进下一步。
//
// 源: packages/core/agent-loop/src/agent.ts:401-419
//
// 模型要了工具就意味着它还欠着一次回应：这时收掉回合，那些工具结果永远送不回
// 模型手里。
func TestToolCallsDriveASecondStep(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	if _, err := world.tools.Register(context.Background(), world.owner,
		namedTool("回声", true, nil)); err != nil {
		t.Fatalf("注册工具失败：%v", err)
	}
	world.setScript(toolReply("c1", "回声"), textReply("看到结果了"))
	world.run(t, "调个工具")

	if got := len(world.requestsSent()); got != 2 {
		t.Fatalf("该跑两步，实际 %d 次请求", got)
	}
	if got := countEvents(world.live, sessionlog.EventStepStart); got != 2 {
		t.Errorf("该开两个步骤，实际 %d 个", got)
	}
	if got := countEvents(world.live, sessionlog.EventTurnStart); got != 1 {
		t.Errorf("这些都在同一个回合里，实际开了 %d 个回合", got)
	}
	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonCompleted {
		t.Errorf("该正常收尾，实际 %q", kind)
	}
	if got := resultTexts(t, world.live); len(got) != 1 || got[0] != `"回声"` {
		t.Errorf("该有一条工具结果，实际 %#v", got)
	}
}

// TestASecondTurnRunsWhileTheInboxStillHasWork 钉住回合尾巴上队列还有活儿就再开
// 一个回合。
//
// 源: packages/core/agent-loop/src/agent.ts:317-329
//
// 一次 [Inbox.Claim] 只带走队首那一条提示（见那边的契约），所以两条排队的输入
// 本来就该是两个回合。让驱动在第一个回合之后收工，第二条会一直等下一次唤醒。
func TestASecondTurnRunsWhileTheInboxStillHasWork(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	err := world.loop.RunMaintenance(context.Background(), func(context.Context) error {
		world.loop.Followup(userMessage("第一件"))
		world.loop.Followup(userMessage("第二件"))
		return nil
	})
	if err != nil {
		t.Fatalf("维护活儿不该失败：%v", err)
	}
	world.settle(t)

	if got := countEvents(world.live, sessionlog.EventTurnStart); got != 2 {
		t.Errorf("该开两个回合，实际 %d 个", got)
	}
	reasons := turnEndReasons(t, world.live)
	if len(reasons) != 2 {
		t.Fatalf("该关两个回合，实际 %d 个", len(reasons))
	}
	for index, reason := range reasons {
		if reason.TurnEndReasonKind() != sessionlog.ReasonCompleted {
			t.Errorf("第 %d 个回合该正常收尾，实际 %q", index+1, reason.TurnEndReasonKind())
		}
	}
}

// ---- 失败与重试 ----

// TestAFailedRequestClosesTheTurnWithTheFailure 钉住模型报错时那份事实同时进了
// turn/end 和 agent/error，而且 step/end 照写。
//
// 源: packages/core/agent-loop/src/agent.ts:371-390
//
// 两条去处各有各的用处：turn/end 是耐久的事实，agent/error 是给活着的观察者的
// 广播。而 step/end 无条件写——一个开了却没关的步骤，读日志的人分不清它是失败了
// 还是还在跑。
func TestAFailedRequestClosesTheTurnWithTheFailure(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript(errorReply("上游炸了"))
	world.run(t, "会失败")

	reasons := turnEndReasons(t, world.live)
	if len(reasons) != 1 {
		t.Fatalf("该恰好关掉一个回合，实际 %d 个", len(reasons))
	}
	failed, ok := reasons[0].(sessionlog.ErrorTurnEnd)
	if !ok {
		t.Fatalf("该以 error 收场，实际 %#v", reasons[0])
	}
	if failed.Error.Code != "BOOM" || failed.Error.Message != "上游炸了" {
		t.Errorf("该原样留着那份事实，实际 %#v", failed.Error)
	}
	if got := countEvents(world.live, sessionlog.EventStepEnd); got != 1 {
		t.Errorf("step/end 该照写，实际 %d 条", got)
	}
	if got := len(world.reportedFailures()); got == 0 {
		t.Error("该广播一次 agent/error")
	}
}

// TestARetryingObserverGetsASecondAttempt 钉住认领了恢复的观察者能让这一步再试一次。
//
// 源: packages/core/agent-loop/src/agent.ts:371-390
//
// 重试在**同一个步骤**里发生：新的一次尝试重新推导一遍边界消息，所以上一次失败
// 留在日志上的痕迹会被算进去，而不是照着一份过期的历史再问一遍。
func TestARetryingObserverGetsASecondAttempt(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript(errorReply("先炸一次"), textReply("这次好了"))

	var attempts int
	detach, err := world.agents.OnRequestError(context.Background(), world.owner,
		func(_ context.Context, _ agent.RequestFailure, _ func(context.Context) (agent.RequestErrorAction, error)) (agent.RequestErrorAction, error) {
			attempts++
			return agent.RequestErrorAction{Retry: attempts == 1}, nil
		})
	if err != nil {
		t.Fatalf("装请求错误观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	world.run(t, "重试一次")

	if got := len(world.requestsSent()); got != 2 {
		t.Fatalf("该发两次请求，实际 %d 次", got)
	}
	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonCompleted {
		t.Errorf("重试成功之后该正常收尾，实际 %q", kind)
	}
	if got := countEvents(world.live, sessionlog.EventStepStart); got != 1 {
		t.Errorf("重试该发生在同一个步骤里，实际开了 %d 个步骤", got)
	}
}

// TestARouteWithoutAProviderOrModelFailsTheTurn 钉住路由不全时这个回合当场失败。
//
// 源: packages/core/agent-loop/src/agent.ts:466-471
//
// 没有 provider/model 就没有任何一个适配器认领得了这次调用。在装请求这一步失败，
// 是唯一能把那句「去 agent.Options 填上，或者在 agent/request 上给出」的指路话
// 说清楚的地方。
func TestARouteWithoutAProviderOrModelFailsTheTurn(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{options: agent.Options{Provider: "甲"}})
	world.run(t, "没有模型")

	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonError {
		t.Errorf("该以 error 收场，实际 %q", kind)
	}
	if got := len(world.requestsSent()); got != 0 {
		t.Errorf("不该发出请求，实际 %d 次", got)
	}
	if got := len(world.reportedFailures()); got == 0 {
		t.Error("该广播一次 agent/error")
	}
}

// ---- 取消 ----

// TestChunksAreLoggedBeforeTheAssemblerSeesThem 钉住定稿的那条助手消息指回它是从
// 哪几条分块攒出来的。
//
// 源: packages/core/agent-loop/src/agent.ts:345-353
//
// 这条回指是「日志是权威的」那句话的可验证形式：没有它，一条助手消息和产生它的
// 那串分块在日志里是两堆互不相干的事件，谁也证明不了模型真的吐过这些字。
func TestChunksAreLoggedBeforeTheAssemblerSeesThem(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript(textReply("一句话"))
	world.run(t, "在么")

	var chunkSeqs []int
	var message sessionlog.Event
	for _, event := range world.live.Events() {
		switch event.Type {
		case sessionlog.EventAssistantChunk:
			chunkSeqs = append(chunkSeqs, event.Seq)
		case sessionlog.EventAssistantMessage:
			message = event
		}
	}
	if len(chunkSeqs) == 0 {
		t.Fatal("该记下那些分块")
	}
	if len(message.SourceEventSeqs) != len(chunkSeqs) {
		t.Fatalf("回指该盖住每一条分块：想要 %v，实际 %v", chunkSeqs, message.SourceEventSeqs)
	}
	for index, seq := range chunkSeqs {
		if message.SourceEventSeqs[index] != seq {
			t.Fatalf("回指对不上：想要 %v，实际 %v", chunkSeqs, message.SourceEventSeqs)
		}
	}
}

// TestAnInterruptedStreamKeepsTheSafePrefix 钉住流被打断时那个已经吐出来的前缀
// 定稿进日志，并标成 interrupted。
//
// 源: packages/core/agent-loop/src/agent.ts:355-369
//
// 丢掉它的话，模型说过的话在日志里凭空消失，而使用者在界面上已经看见了——
// 下一次请求推导出来的历史就和使用者看到的对不上。
func TestAnInterruptedStreamKeepsTheSafePrefix(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.setScript([]llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: "说到一半"},
		llm.TextDeltaChunk{Index: 0, Text: "就被打断了"},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	})
	world.onChunk = func(position int) {
		if position == 0 {
			world.loop.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
		}
	}
	world.run(t, "打断我")

	messages := assistantMessages(t, world.live)
	if len(messages) != 1 {
		t.Fatalf("该定稿一条被打断的消息，实际 %d 条", len(messages))
	}
	if !messages[0].Interrupted {
		t.Error("该标成 interrupted")
	}
	if got := textOf(t, messages[0].Message); got != "说到一半" {
		t.Errorf("该只留下那个安全前缀，实际 %q", got)
	}

	reasons := turnEndReasons(t, world.live)
	if len(reasons) != 1 {
		t.Fatalf("该恰好关掉一个回合，实际 %d 个", len(reasons))
	}
	aborted, ok := reasons[0].(sessionlog.AbortedTurnEnd)
	if !ok {
		t.Fatalf("该以 aborted 收场，实际 %#v", reasons[0])
	}
	if aborted.Reason.CancelCauseKind() != sessionlog.CancelUser {
		t.Errorf("该记下 user 这条来路，实际 %q", aborted.Reason.CancelCauseKind())
	}
}

// TestAnInterruptedStreamWithNothingYetWritesNoMessage 钉住一个字都没吐出来时
// 不写任何助手消息。
//
// 源: packages/core/agent-loop/src/agent.ts:357-359
//
// 一条空的助手消息在重放里是凭空多出来的一轮：模型什么都没说，历史里却多了
// 一次它的发言。
func TestAnInterruptedStreamWithNothingYetWritesNoMessage(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	world.onRequest = func(int) {
		world.loop.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
	}
	world.run(t, "还没开口就打断")

	if got := countEvents(world.live, sessionlog.EventAssistantMessage); got != 0 {
		t.Errorf("不该写助手消息，实际 %d 条", got)
	}
	if kind := onlyTurnEndKind(t, world.live); kind != sessionlog.ReasonAborted {
		t.Errorf("该以 aborted 收场，实际 %q", kind)
	}
	if got := len(world.reportedFailures()); got != 0 {
		t.Errorf("取消不是故障，不该广播 agent/error，实际 %d 条", got)
	}
}

// ---- 请求头与路由元数据 ----

// TestTheHeaderAnchorIsWrittenOnceThenOnlyOnChange 钉住一个循环实例只写一次锚点，
// 之后内容没变就不再写。
//
// 源: packages/core/agent-loop/src/agent.ts:483-489
//
// 每一步都写一份的话，日志会被这份几乎不变的快照淹掉；一次都不写的话，一段恢复
// 出来的日志读不出这一程是从哪份配置开始的。
func TestTheHeaderAnchorIsWrittenOnceThenOnlyOnChange(t *testing.T) {
	t.Parallel()

	world := newLoopWorld(t, loopSetup{})
	if _, err := world.tools.Register(context.Background(), world.owner,
		namedTool("回声", true, nil)); err != nil {
		t.Fatalf("注册工具失败：%v", err)
	}
	world.setScript(toolReply("c1", "回声"), textReply("说完了"))
	world.run(t, "跑两步")

	if got := len(world.requestsSent()); got != 2 {
		t.Fatalf("这条用例要跑两次请求，实际 %d 次", got)
	}
	if got := countEvents(world.live, sessionlog.EventRequestHeader); got != 1 {
		t.Errorf("请求头该只写一次，实际 %d 条", got)
	}
	if got := countEvents(world.live, sessionlog.EventRequestContext); got != 1 {
		t.Errorf("路由元数据该只写一次，实际 %d 条", got)
	}
}

// TestTheFirstHeaderOnAnExistingLogIsAResume 钉住种子日志里已经有过请求头时，
// 这一程的锚点记成 resume。
//
// 源: packages/core/agent-loop/src/agent.ts:483-489
//
// initial 和 resume 的区别是给读日志的人看的：前者说「这段会话从这里开始」，
// 后者说「这是接上来的一程」。记错了，一段被恢复过很多次的日志看起来像很多段
// 各自独立的会话。
func TestTheFirstHeaderOnAnExistingLogIsAResume(t *testing.T) {
	t.Parallel()

	seeded := sessionlog.EpochHeader{
		Config: llm.CallConfig{Provider: "甲", Model: "m-1"},
		System: "以前那份",
	}
	world := newLoopWorld(t, loopSetup{create: session.CreateOptions{Seed: seedOf(
		headerEvent(t, seeded),
	)}})
	world.run(t, "接着来")

	var reasons []sessionlog.RequestHeaderReason
	for _, event := range world.live.Events() {
		if event.Type != sessionlog.EventRequestHeader {
			continue
		}
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			t.Fatalf("request/header 读不回来：%v", err)
		}
		reasons = append(reasons, data.(sessionlog.RequestHeaderData).Reason)
	}
	if len(reasons) != 2 {
		t.Fatalf("该在种子那条之后再添一条，实际 %d 条", len(reasons))
	}
	if reasons[1] != sessionlog.HeaderResume {
		t.Errorf("这一程的锚点该记成 resume，实际 %q", reasons[1])
	}
}
