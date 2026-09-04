// 本文件验工具调用的调度：参数怎么解、独占怎么形成屏障、并行池的上限、
// 结果和上下文按模型次序落地，以及取消之后那些没起步的调用怎么补齐。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:1-289

package agentloop

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// fakeAgent 是一个只回答三件事的发起者：身份、日志、作用域钥匙。
//
// [ExecuteToolCalls] 只问这三件（Session 落事件、Scope().Key() 做派发过滤的键），
// 其余方法只是为了满足 [agent.Agent] 那份契约。用真的 [ReactLoopAgent] 也行，
// 但那要连带装出一整个循环，而这一批用例一个循环里的东西都不碰。
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

// toolWorld 是这一批用例的舞台：一个工具运行时、一个活会话，加上一个带着它们的
// 发起者上下文。
type toolWorld struct {
	runtime *tools.Runtime
	live    *session.Session
	owner   *scope.Scope
	ctx     context.Context
}

// newToolWorld 造一个舞台，工具注册在根作用域上（也就是全局可见）。
func newToolWorld(t *testing.T) *toolWorld {
	t.Helper()
	owner := rootScope(t)
	store := newStore(t)
	live := liveSession(t, store, owner, "s", session.CreateOptions{})
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	initiator := &fakeAgent{id: live.ID(), live: live, owner: owner}
	return &toolWorld{
		runtime: runtime,
		live:    live,
		owner:   owner,
		ctx:     agent.WithInitiator(context.Background(), initiator),
	}
}

// register 把一份工具定义注册进这个舞台。
func (w *toolWorld) register(t *testing.T, definition *tools.Definition) {
	t.Helper()
	if _, err := w.runtime.Register(context.Background(), w.owner, definition); err != nil {
		t.Fatalf("注册工具 %q 失败：%v", definition.Name, err)
	}
}

// noArgsSchema 是一个不收任何参数的对象根 schema。
func noArgsSchema() tools.Node { return tools.Node{Type: tools.TypeObject} }

// namedTool 造一个不收参数、回声出自己名字的工具。
//
// concurrencySafe 为真时它可以并行；为假时**不声明** IsConcurrencySafe，
// 于是按 tools 包的规矩它是独占的。
func namedTool(
	name string,
	concurrencySafe bool,
	body func(ctx context.Context, exec *tools.RunContext) error,
) *tools.Definition {
	definition := &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  noArgsSchema(),
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
	if concurrencySafe {
		definition.IsConcurrencySafe = func(json.RawMessage) bool { return true }
	}
	return definition
}

// callBlock 造一次模型产出的工具调用块。
func callBlock(id llm.CallID, name string) llm.ToolCallBlock {
	return llm.ToolCallBlock{ID: id, Name: name, Arguments: "{}"}
}

// resultTexts 按日志次序取出每一份 tool/result 给模型看的正文。
//
// 一条工具结果消息的内容是一块 [llm.ToolResultBlock]，工具那份内容裹在它里面，
// 所以这里要拆两层。
func resultTexts(t *testing.T, live *session.Session) []string {
	t.Helper()
	var texts []string
	for _, event := range live.Events() {
		if event.Type != sessionlog.EventToolResult {
			continue
		}
		data := decodeToolResult(t, event)
		for _, outer := range data.Message.Content {
			result, ok := outer.(llm.ToolResultBlock)
			if !ok {
				t.Fatalf("工具结果消息那块内容不是 tool-result：%#v", outer)
			}
			for _, block := range result.Content {
				if text, isText := block.(llm.TextBlock); isText {
					texts = append(texts, text.Text)
				}
			}
		}
	}
	return texts
}

// decodeToolResult 解出一条 tool/result 的负载。
func decodeToolResult(t *testing.T, event sessionlog.Event) sessionlog.ToolResultData {
	t.Helper()
	decoded, err := sessionlog.DecodeData(event)
	if err != nil {
		t.Fatalf("解 tool/result 负载失败：%v", err)
	}
	data, ok := decoded.(sessionlog.ToolResultData)
	if !ok {
		t.Fatalf("这条事件不是 tool/result：%#v", decoded)
	}
	return data
}

// eventTypes 按次序取出日志里那些事件的类型。
func eventTypes(live *session.Session) []sessionlog.EventType {
	events := live.Events()
	kinds := make([]sessionlog.EventType, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Type)
	}
	return kinds
}

// TestParseToolArgumentsTurnsNothingIntoAnEmptyObject 钉住模型一个字都没写时得到 `{}`。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:104-106
//
// 交一段空字节下去的话，参数校验读到的是「不是合法 JSON」而不是「少了必填项」，
// 于是一个本来只差一句「你漏了 path」的调用，拿回去的是一句关于 JSON 语法的诊断。
func TestParseToolArgumentsTurnsNothingIntoAnEmptyObject(t *testing.T) {
	t.Parallel()

	if got := string(parseToolArguments("")); got != "{}" {
		t.Errorf("空参数该解成空对象，实际 %s", got)
	}
}

// TestParseToolArgumentsPassesValidJSONThroughByteForByte 钉住合法参数原样穿过。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:107-108
//
// 解出来再排回去会重排键，而这段字节会**原样落进会话日志**（tool/call 的
// arguments 字段）。重排一次，重放出来的调用就和当初发生的那次不是同一份记录。
func TestParseToolArgumentsPassesValidJSONThroughByteForByte(t *testing.T) {
	t.Parallel()

	// 这段参数的键不是字典序：解成 map 再排回去一定会动它们。
	raw := `{"path":"/a","offset":3}`
	if got := string(parseToolArguments(raw)); got != raw {
		t.Errorf("参数没有逐字节穿过：\n想要 %s\n实际 %s", raw, got)
	}
}

// TestParseToolArgumentsWrapsUnparseableArgumentsAsAJSONString 钉住解不动的参数
// 被包成一个 JSON 字符串，而不是被丢掉或者原样当字节交出去。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:109
//
// 这一路的参数类型是 [encoding/json.RawMessage]，它必须是合法 JSON——原样交出去
// 会让下游每一次排它的地方都失败，而那些失败离真正的原因（模型写坏了参数）很远。
// 包成字符串之后它仍然是一个非对象的值，参数 schema 照样拒收，而那句诊断里
// 看得见模型原本写了什么。
func TestParseToolArgumentsWrapsUnparseableArgumentsAsAJSONString(t *testing.T) {
	t.Parallel()

	got := parseToolArguments("{not json")
	if !json.Valid(got) {
		t.Fatalf("解不动的参数该被包成合法 JSON，实际 %s", got)
	}
	var text string
	if err := json.Unmarshal(got, &text); err != nil {
		t.Fatalf("该是一个 JSON 字符串：%v", err)
	}
	if text != "{not json" {
		t.Errorf("模型原本写的东西没留住：%q", text)
	}
}

// TestExecuteToolCallsNeedsARuntimeAndAContextSink 钉住两件必需品缺一不可。
//
// 没有运行时就派发不了；没有上下文接收方，已提交结果捎回来的那些上下文会被
// 悄悄丢掉——而那是模型下一步该看见的东西，丢了没有任何征兆。
func TestExecuteToolCallsNeedsARuntimeAndAContextSink(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	if _, err := ExecuteToolCalls(world.ctx, nil, 4, 1, 1, nil, func(llm.Message) {}); err == nil {
		t.Error("没有工具运行时该失败")
	}
	if _, err := ExecuteToolCalls(world.ctx, world.runtime, 4, 1, 1, nil, nil); err == nil {
		t.Error("没有上下文接收方该失败")
	}
}

// TestExecuteToolCallsNeedsAnInitiator 钉住读不出发起者就当场失败。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:64
//
// 发起者身上挂着这些调用和结果要落进去的那份日志。没有它就不知道该往哪写，
// 而「往哪都不写照样跑完」会让一整批工具调用在日志里彻底消失。
func TestExecuteToolCallsNeedsAnInitiator(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	_, err := ExecuteToolCalls(
		context.Background(), world.runtime, 4, 1, 1,
		[]llm.ToolCallBlock{callBlock("c1", "echo")},
		func(llm.Message) {})
	if err == nil {
		t.Error("没有发起者该失败")
	}
}

// TestExecuteToolCallsPairsEveryCallWithAResult 钉住每一次调用都配得上一份结果，
// 而且那份结果引用得到自己那条调用。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:262-289
//
// 派生历史要求这两者成对。少一份结果，这段日志喂回给模型时就是一次悬空的调用；
// 而结果不引用调用的话，重放的一方拼不出「这份输出是哪次请求的答复」。
func TestExecuteToolCallsPairsEveryCallWithAResult(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	world.register(t, namedTool("a", true, nil))
	world.register(t, namedTool("b", true, nil))

	concluded, err := ExecuteToolCalls(
		world.ctx, world.runtime, 4, 1, 1,
		[]llm.ToolCallBlock{callBlock("c1", "a"), callBlock("c2", "b")},
		func(llm.Message) {})
	if err != nil {
		t.Fatalf("调度失败：%v", err)
	}
	if concluded {
		t.Error("没有结果宣布回合结束")
	}

	var calls, results int
	for _, event := range world.live.Events() {
		switch event.Type {
		case sessionlog.EventToolCall:
			calls++
		case sessionlog.EventToolResult:
			results++
			data := decodeToolResult(t, event)
			if data.Turn != 1 || data.Step != 1 {
				t.Errorf("结果记错了位置：turn=%d step=%d", data.Turn, data.Step)
			}
			if len(event.SourceEventSeqs) != 1 {
				t.Errorf("结果该恰好引用一条调用：%v", event.SourceEventSeqs)
			}
		}
	}
	if calls != 2 || results != 2 {
		t.Errorf("该有两次调用两份结果，实际 %d/%d", calls, results)
	}
	if got := resultTexts(t, world.live); len(got) != 2 || got[0] != `"a"` || got[1] != `"b"` {
		t.Errorf("结果没有按模型次序落地：%q", got)
	}
}

// TestExecuteToolCallsCommitsInModelOrderEvenWhenDispatchFinishesOutOfOrder 钉住
// 派发可以重叠，但落地必须按模型给的次序。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:141-166
//
// 这是本文件那句「派发可以重叠，结果一律按次序落地」的核心。按 settle 的先后落地
// 的话，同一批调用在两次运行里会产生两份不同的日志——重放读出来的历史和当初模型
// 看到的不是一回事，而且事后从任何一侧都查不出来。
//
// 这条用例同时也是「并行池真的重叠」的证据：慢的那个要等快的那个跑完才动得了，
// 两者不重叠就是死锁。
func TestExecuteToolCallsCommitsInModelOrderEvenWhenDispatchFinishesOutOfOrder(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	gate := make(chan struct{})

	var mutex sync.Mutex
	var finished []string
	record := func(name string) {
		mutex.Lock()
		defer mutex.Unlock()
		finished = append(finished, name)
	}

	world.register(t, namedTool("slow", true, func(ctx context.Context, _ *tools.RunContext) error {
		<-gate
		record("slow")
		return nil
	}))
	world.register(t, namedTool("fast", true, func(context.Context, *tools.RunContext) error {
		record("fast")
		close(gate)
		return nil
	}))

	if _, err := ExecuteToolCalls(
		world.ctx, world.runtime, 2, 1, 1,
		[]llm.ToolCallBlock{callBlock("c1", "slow"), callBlock("c2", "fast")},
		func(llm.Message) {}); err != nil {
		t.Fatalf("调度失败：%v", err)
	}

	mutex.Lock()
	order := append([]string(nil), finished...)
	mutex.Unlock()
	if len(order) != 2 || order[0] != "fast" {
		t.Fatalf("这条用例要求快的那个先 settle，否则它测不到想测的东西：%v", order)
	}
	if got := resultTexts(t, world.live); len(got) != 2 || got[0] != `"slow"` || got[1] != `"fast"` {
		t.Errorf("结果该按模型次序落地，实际 %q", got)
	}
}

// TestExecuteToolCallsNeverExceedsTheParallelCap 钉住并行池的上限。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:180-183
//
// 这个上限是本机资源的闸：一批二十次的文件搜索一起放出去，会把机器压垮，
// 而模型完全有可能一次就要这么多。
func TestExecuteToolCallsNeverExceedsTheParallelCap(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	var live, peak int64
	world.register(t, namedTool("wide", true, func(context.Context, *tools.RunContext) error {
		current := atomic.AddInt64(&live, 1)
		for {
			seen := atomic.LoadInt64(&peak)
			if current <= seen || atomic.CompareAndSwapInt64(&peak, seen, current) {
				break
			}
		}
		// 给重叠一个真实发生的机会：没有这一下，四次调用可能一次接一次地跑完，
		// 于是这条用例的上界断言变成假通过。
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt64(&live, -1)
		return nil
	}))

	calls := []llm.ToolCallBlock{
		callBlock("c1", "wide"), callBlock("c2", "wide"),
		callBlock("c3", "wide"), callBlock("c4", "wide"),
	}
	if _, err := ExecuteToolCalls(world.ctx, world.runtime, 2, 1, 1, calls, func(llm.Message) {}); err != nil {
		t.Fatalf("调度失败：%v", err)
	}
	if got := atomic.LoadInt64(&peak); got > 2 {
		t.Errorf("并发数超过了上限：%d", got)
	}
	if len(resultTexts(t, world.live)) != 4 {
		t.Error("四次调用该都有结果")
	}
}

// TestExecuteToolCallsTreatsAnUndeclaredToolAsExclusive 钉住独占调用不和任何兄弟重叠。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:130-137、181-186
//
// 「不声明就是独占」是 tools 包定的保守默认。这里验的是循环这一侧真的照办：
// 一个会改磁盘的工具和一个读同一处的工具一起跑，读出来的是半份状态。
func TestExecuteToolCallsTreatsAnUndeclaredToolAsExclusive(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	var live, peakDuringExclusive int64
	track := func() {
		current := atomic.AddInt64(&live, 1)
		if current > atomic.LoadInt64(&peakDuringExclusive) {
			atomic.StoreInt64(&peakDuringExclusive, current)
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&live, -1)
	}
	world.register(t, namedTool("safe", true, func(context.Context, *tools.RunContext) error {
		track()
		return nil
	}))
	world.register(t, namedTool("solo", false, func(context.Context, *tools.RunContext) error {
		track()
		return nil
	}))

	calls := []llm.ToolCallBlock{
		callBlock("c1", "safe"), callBlock("c2", "solo"), callBlock("c3", "safe"),
	}
	if _, err := ExecuteToolCalls(world.ctx, world.runtime, 4, 1, 1, calls, func(llm.Message) {}); err != nil {
		t.Fatalf("调度失败：%v", err)
	}
	if got := atomic.LoadInt64(&peakDuringExclusive); got != 1 {
		t.Errorf("独占调用形成的屏障没生效，最高并发 %d", got)
	}
	if got := resultTexts(t, world.live); len(got) != 3 ||
		got[0] != `"safe"` || got[1] != `"solo"` || got[2] != `"safe"` {
		t.Errorf("三次调用该按模型次序全部落地：%q", got)
	}
}

// TestExecuteToolCallsHandsCommittedContextsToTheSink 钉住已提交结果捎回来的上下文
// 交给了接收方，而且按提交次序。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:157-160
//
// 循环把它们挪进下一步的收件箱，在**步骤边界**上生效。丢掉的话，一个靠捎话
// 传指令的工具（比如「接下来请只读不写」）会静默失效。
func TestExecuteToolCallsHandsCommittedContextsToTheSink(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	world.register(t, namedTool("first", true, func(_ context.Context, exec *tools.RunContext) error {
		exec.DeferContext(llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "from-first"}}, llm.UserSource{}))
		return nil
	}))
	world.register(t, namedTool("second", true, func(_ context.Context, exec *tools.RunContext) error {
		exec.DeferContext(llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "from-second"}}, llm.UserSource{}))
		return nil
	}))

	var received []string
	if _, err := ExecuteToolCalls(
		world.ctx, world.runtime, 4, 1, 1,
		[]llm.ToolCallBlock{callBlock("c1", "first"), callBlock("c2", "second")},
		func(message llm.Message) { received = append(received, textOf(t, message)) }); err != nil {
		t.Fatalf("调度失败：%v", err)
	}
	if len(received) != 2 || received[0] != "from-first" || received[1] != "from-second" {
		t.Errorf("捎回来的上下文该按提交次序交出去：%q", received)
	}
}

// TestExecuteToolCallsReportsAResultThatConcludesTheTurn 钉住「这个回合到此为止」
// 这件事传得回调用方。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:97-99
//
// 丢掉这一位的后果是循环接着发下一次请求——而那个工具（比如一个把答复交回给
// 用户的收尾工具）说的正是「别再发了」。
func TestExecuteToolCallsReportsAResultThatConcludesTheTurn(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	world.register(t, namedTool("done", true, func(_ context.Context, exec *tools.RunContext) error {
		exec.ConcludeTurn()
		return nil
	}))
	world.register(t, namedTool("plain", true, nil))

	concluded, err := ExecuteToolCalls(
		world.ctx, world.runtime, 4, 1, 1,
		[]llm.ToolCallBlock{callBlock("c1", "plain"), callBlock("c2", "done")},
		func(llm.Message) {})
	if err != nil {
		t.Fatalf("调度失败：%v", err)
	}
	if !concluded {
		t.Error("有一份结果宣布了回合结束，该报上来")
	}
}

// TestExecuteToolCallsSynthesizesResultsForCallsItNeverStarted 钉住取消之后
// 每一次模型调用仍然拿到一份结果。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:248-259
//
// 补的不是装样子：一次取消留下的日志照样要能喂回给模型。少一份结果，那次调用
// 就是悬空的——大多数提供方会直接拒收这样一段历史，于是这个会话再也发不出请求。
func TestExecuteToolCallsSynthesizesResultsForCallsItNeverStarted(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	world.register(t, namedTool("never", true, func(context.Context, *tools.RunContext) error {
		t.Error("取消之后不该有任何执行体跑起来")
		return nil
	}))

	cancelled, cancel := context.WithCancel(world.ctx)
	cancel()

	calls := []llm.ToolCallBlock{
		callBlock("c1", "never"), callBlock("c2", "never"), callBlock("c3", "never"),
	}
	if _, err := ExecuteToolCalls(cancelled, world.runtime, 4, 1, 1, calls, func(llm.Message) {}); err != nil {
		t.Fatalf("被取消的调度不该返回错误：%v", err)
	}

	kinds := eventTypes(world.live)
	want := []sessionlog.EventType{
		sessionlog.EventToolCall, sessionlog.EventToolResult,
		sessionlog.EventToolCall, sessionlog.EventToolResult,
		sessionlog.EventToolCall, sessionlog.EventToolResult,
	}
	if len(kinds) != len(want) {
		t.Fatalf("日志形状不对：%v", kinds)
	}
	for index, kind := range want {
		if kinds[index] != kind {
			t.Fatalf("日志形状不对：%v", kinds)
		}
	}
	for _, event := range world.live.Events() {
		if event.Type != sessionlog.EventToolResult {
			continue
		}
		data := decodeToolResult(t, event)
		if data.Error == nil || data.Error.Code != tools.CodeAbortedBeforeDispatch {
			t.Errorf("补出来的结果该说明执行体没起步：%#v", data.Error)
		}
	}
}

// TestExecuteToolCallsStopsStartingCallsOnceCancelled 钉住取消之后不再起步新的调用，
// 但已经起步的那些照样提交。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:188-233
//
// 已经起步的调用可能已经产生了副作用（写了文件、发了请求）。把它们的结果丢掉，
// 日志里就没有那次副作用的记录，而下一次重放会以为它从没发生过。
func TestExecuteToolCallsStopsStartingCallsOnceCancelled(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	cancellable, cancel := context.WithCancel(world.ctx)
	t.Cleanup(cancel)

	var started int64
	world.register(t, namedTool("solo", false, func(context.Context, *tools.RunContext) error {
		if atomic.AddInt64(&started, 1) == 1 {
			// 第一次调用跑到一半，整个回合被取消。
			cancel()
		}
		return nil
	}))

	calls := []llm.ToolCallBlock{
		callBlock("c1", "solo"), callBlock("c2", "solo"), callBlock("c3", "solo"),
	}
	if _, err := ExecuteToolCalls(cancellable, world.runtime, 4, 1, 1, calls, func(llm.Message) {}); err != nil {
		t.Fatalf("被取消的调度不该返回错误：%v", err)
	}
	if got := atomic.LoadInt64(&started); got != 1 {
		t.Errorf("取消之后不该再起步新的调用，实际起步了 %d 次", got)
	}
	if got := len(resultTexts(t, world.live)); got != 3 {
		t.Errorf("三次模型调用该都有结果，实际 %d 份", got)
	}
}

// TestExecuteToolCallsRecordsTheModelArgumentsVerbatim 钉住落进日志的参数是模型
// 写出来的那一段原文，不是解析、规范化之后的产物。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:261-265
//
// tool/call 是那次请求的耐久记录。记规范化之后的版本，等于把「模型到底写了什么」
// 这件事从日志里抹掉——而那正是排查一次参数错误时唯一有用的东西。
func TestExecuteToolCallsRecordsTheModelArgumentsVerbatim(t *testing.T) {
	t.Parallel()

	world := newToolWorld(t)
	world.register(t, namedTool("plain", true, nil))

	block := callBlock("c1", "plain")
	block.Arguments = "{not json"
	if _, err := ExecuteToolCalls(
		world.ctx, world.runtime, 4, 1, 1,
		[]llm.ToolCallBlock{block}, func(llm.Message) {}); err != nil {
		t.Fatalf("调度失败：%v", err)
	}

	for _, event := range world.live.Events() {
		if event.Type != sessionlog.EventToolCall {
			continue
		}
		decoded, err := sessionlog.DecodeData(event)
		if err != nil {
			t.Fatalf("解 tool/call 负载失败：%v", err)
		}
		data, ok := decoded.(sessionlog.ToolCallData)
		if !ok {
			t.Fatalf("这条事件不是 tool/call：%#v", decoded)
		}
		if data.Arguments != "{not json" {
			t.Errorf("模型写的参数没有原样记下来：%q", data.Arguments)
		}
		return
	}
	t.Fatal("日志里没有 tool/call")
}

func (a *fakeAgent) Remove(llm.MessageID) {}

func (a *fakeAgent) Replace(llm.MessageID, llm.Message) {}
