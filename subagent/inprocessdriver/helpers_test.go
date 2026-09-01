// 本文件的作用：这一包测试共用的装配——作用域、会话事件、一个真的会停在 WhenIdle
// 上的孩子 agent、一份把整条创建路走完的假 agent 造法，以及把它们连起来的夹具。

package inprocessdriver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// testAbsolutePath 是一条在本机上确实绝对的路径。
//
// 理由和 core/agent 那一处逐字相同：写死哪一边的字面量都会让另一个平台上的测试
// 变成假通过。
var testAbsolutePath = filepath.Join(os.TempDir(), "ds-harness-go-inprocessdriver-test")

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

// steppedTurn 造一个「进过模型步骤、然后按 reason 收尾」的完整回合。
//
// [github.com/snight1983/ds-harness-go/core/agent.FoldConsumedWork] 只把这样的回合认成「交代得了消耗」，
// 所以本包每一处要 HasEnd 为真的用例都从这里取事件。
func steppedTurn(t *testing.T, turn int, reason session.TurnEndReason) []session.Event {
	t.Helper()
	return []session.Event{
		event(t, session.EventTurnStart, session.TurnStartData{Turn: turn}),
		event(t, session.EventStepStart, session.StepStartData{Turn: turn, Step: 0}),
		event(t, session.EventTurnEnd, session.TurnEndData{Turn: turn, Reason: reason}),
	}
}

// assistantMessage 造一条带内容的 assistant/message 事件。
//
// 带上 AppendOp 是必须的：这条事件够格上表面，[session.SurfaceOpOf] 要求这种事件
// 一定带着自己的表面操作。
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

// newFreeSession 造一个游离会话，给那个手工摆出来的父用。
func newFreeSession(t *testing.T, id session.SessionID) *coresession.Session {
	t.Helper()
	header := session.SessionHeader{ID: id, Cwd: testAbsolutePath}
	live, err := coresession.NewSession(id, coresession.Options{Header: &header, Now: fixedClock()})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return live
}

// ---- 假 agent ----

// fakeAgent 是一个只为满足 [github.com/snight1983/ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
type fakeAgent struct {
	id      session.SessionID
	scope   *scope.Scope
	session *coresession.Session
	options agent.Options
}

func (a *fakeAgent) ID() session.SessionID                                  { return a.id }
func (a *fakeAgent) Options() agent.Options                                 { return a.options }
func (a *fakeAgent) Session() *coresession.Session                          { return a.session }
func (a *fakeAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *fakeAgent) Scope() *scope.Scope                                    { return a.scope }
func (a *fakeAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *fakeAgent) WhenIdle(context.Context) error                         { return nil }
func (a *fakeAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *fakeAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *fakeAgent) Followup(llm.Message)                                   {}
func (a *fakeAgent) Steer(llm.Message)                                      {}
func (a *fakeAgent) Inject(llm.Message)                                     {}
func (a *fakeAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *fakeAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// newParentAgent 造一个手工摆出来的父：这台驱动只读它的 id、作用域、会话头和选项。
func newParentAgent(t *testing.T, id string) *fakeAgent {
	t.Helper()
	sessionID := session.SessionID(id)
	return &fakeAgent{
		id:      sessionID,
		scope:   keyedScope(t, id, nil),
		session: newFreeSession(t, sessionID),
		options: agent.Options{Provider: "p", Model: "m"},
	}
}

// ---- 会阻塞的孩子 agent ----

// childAgent 是一个**真的会停在 WhenIdle 上**的孩子：静不静下来由测试说了算。
//
// 当场返回的 WhenIdle 会让驱动在提示词刚投进去的那一刻就去读结果，把「等孩子跑完」
// 这条边整个测没。
type childAgent struct {
	*fakeAgent

	mutex sync.Mutex
	// idle 关掉之后 WhenIdle 才返回。
	idle chan struct{}
	// quiet 是 idle 已经关过的一次性标记。
	quiet bool
	// idleErr 非 nil 时 WhenIdle 交回这个错。
	idleErr error

	cancels   []session.TurnEndCancelCause
	followups []llm.Message

	// onFollowup 在提示词投进来的那一刻跑，用来摆出这个孩子跑完之后的日志。
	onFollowup func(*childAgent)
}

func newChildAgent(id session.SessionID, agentScope *scope.Scope, live *coresession.Session) *childAgent {
	return &childAgent{
		fakeAgent: &fakeAgent{id: id, scope: agentScope, session: live},
		idle:      make(chan struct{}),
	}
}

func (c *childAgent) WhenIdle(ctx context.Context) error {
	select {
	case <-c.idle:
		c.mutex.Lock()
		defer c.mutex.Unlock()
		return c.idleErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// settle 让这个孩子静下来，幂等。
func (c *childAgent) settle() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.quiet {
		return
	}
	c.quiet = true
	close(c.idle)
}

func (c *childAgent) Cancel(cause session.TurnEndCancelCause, _ agent.CancelOptions) {
	c.mutex.Lock()
	c.cancels = append(c.cancels, cause)
	c.mutex.Unlock()
	// 一次取消把这个孩子停到静止——驱动那一路正等在 WhenIdle 上。
	c.settle()
}

func (c *childAgent) Followup(message llm.Message) {
	c.mutex.Lock()
	c.followups = append(c.followups, message)
	hook := c.onFollowup
	c.mutex.Unlock()
	if hook != nil {
		hook(c)
	}
}

// delivered 交出这个孩子收到的那些跟进消息。
func (c *childAgent) delivered() []llm.Message {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]llm.Message(nil), c.followups...)
}

// cancelled 交出历次取消的理由。
func (c *childAgent) cancelled() []session.TurnEndCancelCause {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]session.TurnEndCancelCause(nil), c.cancels...)
}

// appendAll 把这些事件挨个追加到孩子自己的日志上。
func (c *childAgent) appendAll(t *testing.T, events []session.Event) {
	t.Helper()
	for _, each := range events {
		if _, err := c.session.Append(each); err != nil {
			t.Fatalf("追加事件失败：%v", err)
		}
	}
}

// ---- 假 agent 造法 ----

// fakeFactory 把整条真创建路走完：铸孩子自己的作用域、在活会话表里建会话、跑调用方
// 那份 setup 并提交、进注册表、公布。少任何一步，那笔描述符前置步骤观察者和那份
// 结构化捕获都测不到。
type fakeFactory struct {
	agents   *agent.Registry
	sessions *coresession.Store

	mutex sync.Mutex
	// creates 是历次造法请求，按调用顺序。
	creates []agent.CreateOptions
	// children 是造出来的那些孩子，按会话 id。
	children map[session.SessionID]*childAgent
	// createErr 非 nil 时创建当场失败。
	createErr error
	// disposeErr 非 nil 时每一个句柄的处置都报这个错；真的摘除照旧走完。
	disposeErr error
	// onChild 在一个孩子已经公布、句柄还没交出去的那一刻跑。
	onChild func(*childAgent)
}

func newFactory(agents *agent.Registry, sessions *coresession.Store) *fakeFactory {
	return &fakeFactory{
		agents:   agents,
		sessions: sessions,
		children: map[session.SessionID]*childAgent{},
	}
}

// created 交出历次创建请求。
func (f *fakeFactory) created() []agent.CreateOptions {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]agent.CreateOptions(nil), f.creates...)
}

// only 交出唯一那个造出来的孩子，不恰好一个就当场失败。
func (f *fakeFactory) only(t *testing.T) *childAgent {
	t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if len(f.children) != 1 {
		t.Fatalf("该恰好造出一个孩子，实际 %d 个", len(f.children))
	}
	for _, child := range f.children {
		return child
	}
	return nil
}

func (f *fakeFactory) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	f.mutex.Lock()
	f.creates = append(f.creates, options)
	failure := f.createErr
	f.mutex.Unlock()

	if failure != nil {
		return agent.Handle{}, failure
	}
	agentScope, err := scope.New(scope.NewKey(string(options.SessionID)), scope.Options{Parent: owner.Key()})
	if err != nil {
		return agent.Handle{}, err
	}
	live, err := f.sessions.Create(ctx, agentScope, options.SessionID, coresession.CreateOptions{
		Seed:            options.Seed,
		Cwd:             options.Cwd,
		ParentSession:   options.ParentSession,
		SeedLength:      options.SeedLength,
		Origin:          options.Origin,
		DelegationDepth: options.DelegationDepth,
		AgentPreset:     options.AgentPreset,
	})
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if options.Setup != nil {
		commit, err := options.Setup(ctx, agentScope)
		if err != nil {
			_ = agentScope.Dispose(context.Background())
			return agent.Handle{}, err
		}
		if commit != nil {
			if err := commit(); err != nil {
				_ = agentScope.Dispose(context.Background())
				return agent.Handle{}, err
			}
		}
	}
	child := newChildAgent(options.SessionID, agentScope, live)
	child.options = options.AgentOptions

	detach, err := f.agents.Enter(child, nil)
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if err := f.agents.Announce(ctx, child); err != nil {
		_ = detach(context.Background())
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}

	f.mutex.Lock()
	f.children[options.SessionID] = child
	planted := f.disposeErr
	hook := f.onChild
	f.mutex.Unlock()

	if hook != nil {
		hook(child)
	}

	var once sync.Once
	return agent.Handle{Agent: child, Dispose: func(ctx context.Context) error {
		var failure error
		once.Do(func() {
			// 处置必须把这个孩子停到静止，否则驱动那一路会一直挂在 WhenIdle 上。
			child.settle()
			// 次序：先从注册表摘掉，再放掉作用域。
			failure = errors.Join(planted, detach(ctx), agentScope.Dispose(ctx))
		})
		return failure
	}}, nil
}

func (f *fakeFactory) Resume(context.Context, *scope.Scope, agent.ResumeOptions) (agent.Handle, error) {
	return agent.Handle{}, errors.New("这一包不走续跑")
}

// ---- 夹具 ----

// fixture 是一台装好的驱动周边：注册表、造法、会话表、提示词、工具、父，和主人作用域。
type fixture struct {
	agents   *agent.Registry
	sessions *coresession.Store
	factory  *fakeFactory
	prompt   *systemprompt.Registry
	tools    *tools.Runtime
	owner    *scope.Scope
	parent   *fakeAgent
}

// newFixture 把一台驱动要的东西全装起来。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	owner := keyedScope(t, "subagents", nil)
	factory := newFactory(agents, sessions)
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}
	prompt, err := systemprompt.NewRegistry(t.Context(), rootScope(t), systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	return &fixture{
		agents:   agents,
		sessions: sessions,
		factory:  factory,
		prompt:   prompt,
		tools:    toolRuntime,
		owner:    owner,
		parent:   newParentAgent(t, "parent"),
	}
}

// services 交出这台驱动那份装配。
func (f *fixture) services() Services {
	return Services{
		Agents: f.agents,
		Owner:  f.owner,
		Composition: subagent.ChildCompositionServices{
			SystemPrompt: f.prompt,
			Tools:        f.tools,
		},
	}
}

// request 造一份最小的、已解算的一次性开工请求。
func (f *fixture) request(prompt string) subagent.ResolvedStartRequest {
	return subagent.ResolvedStartRequest{
		StartRequest: subagent.StartRequest{
			Prompt: textContent(prompt),
			Parent: f.parent,
		},
		Descriptor: subagent.DescriptorData{
			Version:  subagent.DescriptorVersion,
			Mode:     subagent.ModeOneShot,
			Provider: "spawn",
		},
	}
}

// start 起一次运行，失败就当场挂掉，并登记「测完必处置」。
func (f *fixture) start(
	t *testing.T,
	ctx context.Context,
	request subagent.ResolvedStartRequest,
	options RunOptions,
) subagent.Run {
	t.Helper()
	run, err := StartInProcessRun(ctx, f.services(), request, options)
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	t.Cleanup(func() { _ = run.Dispose(context.Background()) })
	return run
}

// completedTurn 是一个「说了一句话然后正常收尾」的孩子。
func completedTurn(t *testing.T, answer string) func(*childAgent) {
	t.Helper()
	return func(child *childAgent) {
		child.appendAll(t, []session.Event{
			event(t, session.EventTurnStart, session.TurnStartData{Turn: 0}),
			event(t, session.EventStepStart, session.StepStartData{Turn: 0, Step: 0}),
			assistantMessage(t, 0, textContent(answer)),
			event(t, session.EventTurnEnd, session.TurnEndData{
				Turn: 0, Reason: session.CompletedTurnEnd{},
			}),
		})
		child.settle()
	}
}

func (a *fakeAgent) Remove(llm.MessageID) {}

func (a *fakeAgent) Replace(llm.MessageID, llm.Message) {}
