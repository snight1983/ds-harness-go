// Package providertest 是进程内子 agent 提供方共用的测试装配。
//
// spawn 与 fork 两个提供方各自只有几十行，可它们那几行里唯一真正会出错的地方——
// 「谁给种子、谁不给」——只有走完一整条真的创建路才看得见：种子是在
// [ds-harness-go/core/agent.Registry] 调造法的那一刻记下来的。所以这里把注册表、
// 活会话表、一份记账的造法和一个手工摆出来的父装成一台，两个提供方共用。
//
// # 为什么它不是 _test.go
//
// 这台装配要被**两个包**导入，而 Go 的 _test.go 只属于自己那个包，导不出去。
// 所以它是一个普通包，像标准库的 net/http/httptest 一样导入 testing
// （成例见 ds-harness-go/storage/storagetest）。它落在 internal 下面，
// 只有 ds-harness-go/subagent/... 进得来。
//
// # 这里的孩子当场就静
//
// [ds-harness-go/subagent/inprocessdriver] 自己那一包的用例要的是一个真的会停在
// WhenIdle 上的孩子，好把「等孩子跑完」那条边压住。这里不要：那条边归那一包，
// 这里只问「造法收到的那份 CreateOptions 长什么样」，而它在孩子静不静之前就记下了。
package providertest

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

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/subagent/inprocessdriver"
	"ds-harness-go/subagent/subagent"
)

// absolutePath 是一条在本机上确实绝对的路径。
//
// 写死哪一边的字面量都会让另一个平台上的测试变成假通过。
var absolutePath = filepath.Join(os.TempDir(), "ds-harness-go-providertest")

// tickingClock 是一个走得可预测的时钟：每读一次加一毫秒。
func tickingClock() func() int64 {
	tick := int64(1000)
	return func() int64 { return atomic.AddInt64(&tick, 1) }
}

// scopeOf 造一个有身份的顶层作用域，用完自动释放。
func scopeOf(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// payloadOf 把一份负载排成字节，排不出去当场失败。
func payloadOf(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// TextContent 造一段只有一块文本的内容。
func TextContent(body string) llm.Content { return llm.Content{llm.TextBlock{Text: body}} }

// ---- 假 agent ----

// StubAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 它当场就静：WhenIdle 立刻返回。
type StubAgent struct {
	id      session.SessionID
	scope   *scope.Scope
	live    *coresession.Session
	options agent.Options
}

func (a *StubAgent) ID() session.SessionID                                  { return a.id }
func (a *StubAgent) Options() agent.Options                                 { return a.options }
func (a *StubAgent) Session() *coresession.Session                          { return a.live }
func (a *StubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *StubAgent) Scope() *scope.Scope                                    { return a.scope }
func (a *StubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *StubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *StubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *StubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *StubAgent) Followup(llm.Message)                                   {}
func (a *StubAgent) Steer(llm.Message)                                      {}
func (a *StubAgent) Inject(llm.Message)                                     {}
func (a *StubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *StubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// Append 把一条事件追加到这个 agent 自己的日志上。
//
// 不给 Seq：会话自己按追加次序发号，于是「seq 等于数组下标」这条契约在这些用例里
// 是真的成立的——fork 那段前缀切法正是靠它。
func (a *StubAgent) Append(t *testing.T, kind session.EventType, value any) session.Event {
	t.Helper()
	appended, err := a.live.Append(session.Event{Type: kind, Data: payloadOf(t, value)})
	if err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
	return appended
}

// AppendCompletedTurn 追加一个「开了、进过模型步骤、正常收尾」的完整回合。
func (a *StubAgent) AppendCompletedTurn(t *testing.T, turn int) {
	t.Helper()
	a.Append(t, session.EventTurnStart, session.TurnStartData{Turn: turn})
	a.Append(t, session.EventStepStart, session.StepStartData{Turn: turn, Step: 0})
	a.Append(t, session.EventTurnEnd, session.TurnEndData{Turn: turn, Reason: session.CompletedTurnEnd{}})
}

// AppendOpenTurn 追加一个**还在飞**的回合：开了、进过步骤，但没收尾。
func (a *StubAgent) AppendOpenTurn(t *testing.T, turn int) {
	t.Helper()
	a.Append(t, session.EventTurnStart, session.TurnStartData{Turn: turn})
	a.Append(t, session.EventStepStart, session.StepStartData{Turn: turn, Step: 0})
}

// Events 交出这个 agent 当前的日志。
func (a *StubAgent) Events() []session.Event { return a.live.Events() }

// ---- 记账的造法 ----

// recordingFactory 把整条真创建路走完，并把每一次收到的 CreateOptions 记下来。
type recordingFactory struct {
	agents   *agent.Registry
	sessions *coresession.Store

	mutex   sync.Mutex
	creates []agent.CreateOptions
}

func (f *recordingFactory) created() []agent.CreateOptions {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]agent.CreateOptions(nil), f.creates...)
}

func (f *recordingFactory) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	f.mutex.Lock()
	f.creates = append(f.creates, options)
	f.mutex.Unlock()

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
	child := &StubAgent{id: options.SessionID, scope: agentScope, live: live, options: options.AgentOptions}

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

	var once sync.Once
	return agent.Handle{Agent: child, Dispose: func(ctx context.Context) error {
		var failure error
		once.Do(func() { failure = errors.Join(detach(ctx), agentScope.Dispose(ctx)) })
		return failure
	}}, nil
}

func (f *recordingFactory) Resume(context.Context, *scope.Scope, agent.ResumeOptions) (agent.Handle, error) {
	return agent.Handle{}, errors.New("这台装配不走续跑")
}

// ---- 装配 ----

// Harness 是一台装好的提供方周边。
type Harness struct {
	// Parent 是那个手工摆出来的父 agent；fork 的用例往它的日志上追加回合。
	Parent *StubAgent

	agents   *agent.Registry
	sessions *coresession.Store
	factory  *recordingFactory
	prompt   *systemprompt.Registry
	tools    *tools.Runtime
	owner    *scope.Scope
}

// New 把一台提供方要用到的东西全装起来。
func New(t *testing.T) *Harness {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: tickingClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	factory := &recordingFactory{agents: agents, sessions: sessions}
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}
	prompt, err := systemprompt.NewRegistry(t.Context(), scopeOf(t, "prompt-root"), systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}

	parentID := session.SessionID("parent")
	header := session.SessionHeader{ID: parentID, Cwd: absolutePath}
	parentSession, err := coresession.NewSession(parentID, coresession.Options{Header: &header, Now: tickingClock()})
	if err != nil {
		t.Fatalf("造父会话失败：%v", err)
	}

	return &Harness{
		Parent: &StubAgent{
			id:      parentID,
			scope:   scopeOf(t, "parent"),
			live:    parentSession,
			options: agent.Options{Provider: "p", Model: "m"},
		},
		agents:   agents,
		sessions: sessions,
		factory:  factory,
		prompt:   prompt,
		tools:    toolRuntime,
		owner:    scopeOf(t, "subagents"),
	}
}

// Services 交出这台驱动那份装配。
func (h *Harness) Services() inprocessdriver.Services {
	return inprocessdriver.Services{
		Agents: h.agents,
		Owner:  h.owner,
		Composition: subagent.ChildCompositionServices{
			SystemPrompt: h.prompt,
			Tools:        h.tools,
		},
	}
}

// Request 造一份最小的、已解算的一次性开工请求。
func (h *Harness) Request(prompt, providerName string) subagent.ResolvedStartRequest {
	return subagent.ResolvedStartRequest{
		StartRequest: subagent.StartRequest{
			Prompt: TextContent(prompt),
			Parent: h.Parent,
		},
		Descriptor: subagent.DescriptorData{
			Version:  subagent.DescriptorVersion,
			Mode:     subagent.ModeOneShot,
			Provider: providerName,
		},
	}
}

// OnlyCreate 交出唯一那次创建请求，不恰好一次就当场失败。
func (h *Harness) OnlyCreate(t *testing.T) agent.CreateOptions {
	t.Helper()
	creates := h.factory.created()
	if len(creates) != 1 {
		t.Fatalf("该恰好造一个孩子，实际 %d 次", len(creates))
	}
	return creates[0]
}
