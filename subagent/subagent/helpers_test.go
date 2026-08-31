// 本文件的作用：这一包测试共用的小工具——造作用域、造会话事件、造假 agent，
// 以及一个可以按测试需要调形状的假提供方和假运行。

package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// testAbsolutePath 是一条在本机上确实绝对的路径。
//
// 理由和 core/agent 那一处逐字相同：写死哪一边的字面量都会让另一个平台上的
// 测试变成假通过。
var testAbsolutePath = filepath.Join(os.TempDir(), "ds-harness-go-subagent-test")

// fixedClock 是一个走得可预测的时钟：每读一次加一毫秒。
func fixedClock() func() int64 {
	tick := int64(1000)
	return func() int64 { return atomic.AddInt64(&tick, 1) }
}

// rootScope 造一个没有身份的作用域（落全局层），用完自动释放。
func rootScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// keyedScope 造一个有身份的作用域，用完自动释放。parent 为 nil 表示它自己是顶层。
func keyedScope(t *testing.T, label string, parent *scope.Key) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{Parent: parent})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// data 把一份负载排成字节，排不出去当场失败。
func data(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// event 造一条会话事件。
func event(t *testing.T, kind session.EventType, payload any) session.Event {
	t.Helper()
	return session.Event{Type: kind, Data: data(t, payload)}
}

// turnEnd 造一条带确切结束理由的 turn/end。
func turnEnd(t *testing.T, turn int, reason session.TurnEndReason) session.Event {
	t.Helper()
	return event(t, session.EventTurnEnd, session.TurnEndData{Turn: turn, Reason: reason})
}

// steppedTurn 造一个「进过模型步骤、然后按 reason 收尾」的完整回合。
//
// [ds-harness-go/core/agent.FoldConsumedWork] 只把这样的回合认成「交代得了消耗」，
// 所以本包每一处要 HasEnd 为真的用例都从这里取事件。
func steppedTurn(t *testing.T, turn int, reason session.TurnEndReason) []session.Event {
	t.Helper()
	return []session.Event{
		event(t, session.EventTurnStart, session.TurnStartData{Turn: turn}),
		event(t, session.EventStepStart, session.StepStartData{Turn: turn, Step: 0}),
		turnEnd(t, turn, reason),
	}
}

// assistantMessage 造一条带内容的 assistant/message 事件。content 为 nil 造的是
// 一条内容为空的消息（循环补记用量时的那种）。
//
// 带上 AppendOp 是必须的：这条事件够格上表面，[session.SurfaceOpOf] 要求这种事件
// 一定带着自己的表面操作，否则一份拿它当 seed 的日志根本立不起来。
func assistantMessage(t *testing.T, turn int, content llm.Content) session.Event {
	t.Helper()
	built := event(t, session.EventAssistantMessage, session.AssistantMessageData{
		Turn:    turn,
		Message: llm.NewAssistantMessage(content, llm.Provenance{Provider: "p", Model: "m"}),
	})
	built.SurfaceOp = session.AppendOp{}
	return built
}

// textContent 造一段只有一块文本的内容。
func textContent(body string) llm.Content { return llm.Content{llm.TextBlock{Text: body}} }

// textOf 把一段内容里的文本块拼起来，好让断言只比字符串。
func textOf(content llm.Content) string {
	var joined string
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			joined += text.Text
		}
	}
	return joined
}

// newFreeSession 造一个游离会话，seed 由调用方给（nil 表示不给）。
//
// seed 的序号在这里补：coresession 要求它从 0 起连续，而本包各处那些 fixture
// 是按「这一段日志说了什么」写的，逐条手写序号只会让它们更难读。
func newFreeSession(t *testing.T, id session.SessionID, parent session.SessionID, seed []session.Event) *coresession.Session {
	t.Helper()
	seed = append([]session.Event(nil), seed...)
	for index := range seed {
		seed[index].Seq = index
	}
	header := session.SessionHeader{
		ID:            id,
		Cwd:           testAbsolutePath,
		ParentSession: parent,
		SeedLength:    len(seed),
	}
	live, err := coresession.NewSession(id, coresession.Options{
		Seed:   seed,
		Header: &header,
		Now:    fixedClock(),
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return live
}

// fakeAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包读它的地方只有三处：ID（父身份与血统）、Scope（作用域派发的载体）、
// Session（血统链、日志后缀、驻留状态），其余方法一律空操作。
type fakeAgent struct {
	id      session.SessionID
	scope   *scope.Scope
	session *coresession.Session
	options agent.Options
	inbox   *agent.Inbox

	// status 由测试直接改，用来摆出 running／idle 两种驻留观察结果。
	mutex  sync.Mutex
	status agent.Status
}

// newFakeAgent 造一个挂在自己那把作用域钥匙上的假 agent。
func newFakeAgent(t *testing.T, id string, parentScope *scope.Key, parentSession session.SessionID) *fakeAgent {
	t.Helper()
	sessionID := session.SessionID(id)
	return &fakeAgent{
		id:      sessionID,
		scope:   keyedScope(t, id, parentScope),
		session: newFreeSession(t, sessionID, parentSession, nil),
		status:  agent.StatusIdle,
	}
}

func (a *fakeAgent) ID() session.SessionID          { return a.id }
func (a *fakeAgent) Options() agent.Options         { return a.options }
func (a *fakeAgent) Session() *coresession.Session  { return a.session }
func (a *fakeAgent) Inbox() *agent.Inbox            { return a.inbox }
func (a *fakeAgent) Scope() *scope.Scope            { return a.scope }
func (a *fakeAgent) WhenIdle(context.Context) error { return nil }

func (a *fakeAgent) Status() agent.Status {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.status
}

func (a *fakeAgent) setStatus(status agent.Status) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.status = status
}

func (a *fakeAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}

func (a *fakeAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *fakeAgent) Send(llm.Message, agent.InboxTarget, bool) {}
func (a *fakeAgent) Followup(llm.Message)                      {}
func (a *fakeAgent) Steer(llm.Message)                         {}
func (a *fakeAgent) Inject(llm.Message)                        {}
func (a *fakeAgent) Prepend(llm.Message, agent.InboxTarget)    {}

// fakeRun 是一个可以按测试需要摆结局的一次性运行。
type fakeRun struct {
	id     session.SessionID
	local  agent.Agent
	result Result
	// resultErr 非 nil 时 Result 报这个错。
	resultErr error
	// release 关掉之后 Result 才交出结局；nil 表示当场就有。
	release chan struct{}

	disposals atomic.Int32
	// disposeErr 非 nil 时 Dispose 报这个错。
	disposeErr error
}

func (r *fakeRun) ID() session.SessionID { return r.id }
func (r *fakeRun) LocalAgent() agent.Agent {
	if r.local == nil {
		return nil
	}
	return r.local
}

func (r *fakeRun) Result(ctx context.Context) (Result, error) {
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if r.resultErr != nil {
		return Result{}, r.resultErr
	}
	return r.result, nil
}

func (r *fakeRun) Dispose(context.Context) error {
	r.disposals.Add(1)
	return r.disposeErr
}

// fakeProvider 是一个形状全可调的提供方。
type fakeProvider struct {
	name         string
	capabilities Capabilities
	inherits     bool

	// run 是 Start 交回的那个运行；nil 表示现造一个空的。
	run Run
	// startErr 非 nil 时 Start 报这个错。
	startErr error
	// onStart 在每次 Start 时被叫到，用来把服务传下来的请求录下来。
	onStart func(ResolvedStartRequest)

	// prepare 非 nil 时这个提供方实现 [ContinuablePreparer]（见 preparingProvider）。
	prepare func(context.Context, ContinuableCreateRequest) (ContinuableCreateSpec, error)
}

func (p *fakeProvider) Name() string                { return p.name }
func (p *fakeProvider) Capabilities() Capabilities  { return p.capabilities }
func (p *fakeProvider) InheritsParentContext() bool { return p.inherits }

func (p *fakeProvider) Start(_ context.Context, request ResolvedStartRequest) (Run, error) {
	if p.onStart != nil {
		p.onStart(request)
	}
	if p.startErr != nil {
		return nil, p.startErr
	}
	if p.run != nil {
		return p.run, nil
	}
	return &fakeRun{id: "child", result: Result{StopReason: StopCompleted}}, nil
}

// preparingProvider 是一个**额外**实现了 [ContinuablePreparer] 的假提供方。
//
// 「有没有这个能力」在 Go 里就是「实不实现这个接口」，所以两种形态必须是两个类型：
// 给 [fakeProvider] 加一个总在的方法会让「不支持可续」那条路测不到。
type preparingProvider struct{ fakeProvider }

func (p *preparingProvider) PrepareContinuable(
	ctx context.Context,
	request ContinuableCreateRequest,
) (ContinuableCreateSpec, error) {
	if p.prepare == nil {
		return ContinuableCreateSpec{}, nil
	}
	return p.prepare(ctx, request)
}

// newRuntime 造一台不带续接能力的服务（[RuntimeOptions.Continuation] 的 Agents 为 nil）。
func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(RuntimeOptions{})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	return runtime
}

// register 把一个提供方登记进服务，返回摘除它的函数。
func register(t *testing.T, runtime *Runtime, owner *scope.Scope, provider Provider) func(context.Context) error {
	t.Helper()
	dispose, err := runtime.RegisterProvider(context.Background(), owner, provider)
	if err != nil {
		t.Fatalf("登记提供方失败：%v", err)
	}
	return dispose
}
