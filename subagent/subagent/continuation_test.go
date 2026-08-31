// 本文件的作用：可续子 agent 那台编排机的共用装配（假宿主、假 agent 工厂、
// 一个真会阻塞的孩子 agent），以及那几件词汇本身的测试——处置事务、结清那一行
// 说辞、取消的说法，和构造时那几道门与登记次序。

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
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// ---- 假宿主 ----

// fakeHost 是那台管理器要的两个钩子的最小实现。
//
// 生命周期观察者拿真的：activationObserver 会去读孩子自己那段日志，一个假的
// 观察者会把「结清时到底交回了什么停止原因」这件事整个测没。
type fakeHost struct {
	emitter *lifecycleEmitter
	// prepare 非 nil 时由它解算那份可续创建贡献；nil 表示不带 seed 地放行。
	prepare func(context.Context, string, ContinuableCreateRequest) (ContinuableCreateSpec, error)

	mu sync.Mutex
	// prepared 是历次可续创建解算的目标提供方名，按调用顺序。
	prepared []string
}

func (h *fakeHost) prepareContinuable(
	ctx context.Context,
	name string,
	request ContinuableCreateRequest,
) (ContinuableCreateSpec, error) {
	h.mu.Lock()
	h.prepared = append(h.prepared, name)
	h.mu.Unlock()

	if h.prepare != nil {
		return h.prepare(ctx, name, request)
	}
	return ContinuableCreateSpec{}, nil
}

func (h *fakeHost) observeActivation(
	provider string,
	childID session.SessionID,
	parent agent.Agent,
) *activationObserver {
	return newActivationObserver(h.emitter, provider, childID, parent)
}

// ---- 会阻塞的孩子 agent ----

// childAgent 是一个**真的会停在 WhenIdle 上**的孩子。
//
// [fakeAgent.WhenIdle] 当场返回 nil，那会让结清守望在物化刚落地、初始提示词还
// 没投进去的那一刻就判定「静止」，把孩子拆掉。真 agent 不是这样：它的静止要等
// 循环退出。所以这里换成一个通道，测试自己决定这个孩子什么时候静下来。
type childAgent struct {
	*fakeAgent

	mu sync.Mutex
	// idle 关掉之后 WhenIdle 才返回。
	idle chan struct{}
	// quiet 是 idle 已经关过的一次性标记。
	quiet bool

	cancels   []session.TurnEndCancelCause
	followups []llm.Message
	steers    []llm.Message
	injects   []llm.Message
}

func newChildAgent(id session.SessionID, agentScope *scope.Scope, live *coresession.Session) *childAgent {
	return &childAgent{
		fakeAgent: &fakeAgent{
			id:      id,
			scope:   agentScope,
			session: live,
			status:  agent.StatusIdle,
		},
		idle: make(chan struct{}),
	}
}

func (c *childAgent) WhenIdle(ctx context.Context) error {
	select {
	case <-c.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// settle 让这个孩子静下来，幂等。
func (c *childAgent) settle() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.quiet {
		return
	}
	c.quiet = true
	close(c.idle)
}

// Cancel 记下这次取消，并且只在**没有**留住收件箱时把循环停到静止。
//
// 这两支分得开是要紧的：拆解那一路是不留收件箱的取消，它必须真的把这个孩子停下来，
// 否则 finishDisposal 里那次 WhenIdle(context.Background()) 会把测试挂死；而打断
// 留住收件箱，停的只是当下这段活动，孩子还要接着干排着的活儿，所以它不静止。
func (c *childAgent) Cancel(cause session.TurnEndCancelCause, options agent.CancelOptions) {
	c.mu.Lock()
	c.cancels = append(c.cancels, cause)
	c.mu.Unlock()

	if !options.KeepInbox {
		c.settle()
	}
}

func (c *childAgent) Followup(message llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.followups = append(c.followups, message)
}

func (c *childAgent) Steer(message llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.steers = append(c.steers, message)
}

func (c *childAgent) Inject(message llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.injects = append(c.injects, message)
}
func (c *childAgent) Prepend(llm.Message, agent.InboxTarget) {}

// delivered 交出这个 agent 收到的三路投递，各自一份拷贝。
func (c *childAgent) delivered() (followups, steers, injects []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]llm.Message(nil), c.followups...),
		append([]llm.Message(nil), c.steers...),
		append([]llm.Message(nil), c.injects...)
}

// cancelled 交出历次取消的理由。
func (c *childAgent) cancelled() []session.TurnEndCancelCause {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]session.TurnEndCancelCause(nil), c.cancels...)
}

// ---- 假 agent 工厂 ----

// fakeFactory 是登进 agent 注册表的那份造法。
//
// 它把整条真路走完：铸孩子自己的作用域、在活会话表里建会话、跑调用方那份 setup
// 并提交、进注册表、公布。少任何一步，管理器上那几道以「这个孩子活着吗」为
// 前提的检查就测不到。
type fakeFactory struct {
	t        *testing.T
	agents   *agent.Registry
	sessions *coresession.Store
	store    *fakePersistence

	mu sync.Mutex
	// creates、resumes 是历次造法请求，按调用顺序。
	creates []agent.CreateOptions
	resumes []agent.ResumeOptions
	// children 是造出来的那些孩子，按会话 id。
	children map[session.SessionID]*childAgent
	// createErr、resumeErr 非 nil 时对应那条路当场失败。
	createErr error
	resumeErr error
	// disposeErr 非 nil 时每一个句柄的处置都报这个错。真的摘除和作用域释放照旧走完，
	// 于是测试收尾还是干净的——这里要的只是那条失败往上汇总的路。
	disposeErr error
	// onPublished 在一个孩子已经公布、句柄还没交出去的那一刻跑，用来在「物化成功」
	// 和「投递」这两步之间插进动作。管理器在这个窗口里没有别的接缝可挂。
	onPublished func(session.SessionID)
}

func newFactory(t *testing.T, agents *agent.Registry, sessions *coresession.Store, store *fakePersistence) *fakeFactory {
	return &fakeFactory{
		t: t, agents: agents, sessions: sessions, store: store,
		children: map[session.SessionID]*childAgent{},
	}
}

// child 交出某个 id 那个造出来的孩子。
func (f *fakeFactory) child(id session.SessionID) *childAgent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.children[id]
}

// created 交出历次创建请求。
func (f *fakeFactory) created() []agent.CreateOptions {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]agent.CreateOptions(nil), f.creates...)
}

// resumed 交出历次续跑请求。
func (f *fakeFactory) resumed() []agent.ResumeOptions {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]agent.ResumeOptions(nil), f.resumes...)
}

func (f *fakeFactory) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	f.mu.Lock()
	f.creates = append(f.creates, options)
	failure := f.createErr
	f.mu.Unlock()

	if failure != nil {
		return agent.Handle{}, failure
	}
	return f.publish(ctx, owner, options.SessionID, coresession.CreateOptions{
		Seed:            options.Seed,
		Cwd:             options.Cwd,
		ParentSession:   options.ParentSession,
		SeedLength:      options.SeedLength,
		Origin:          options.Origin,
		DelegationDepth: options.DelegationDepth,
		AgentPreset:     options.AgentPreset,
	}, options.AgentOptions, options.Setup)
}

func (f *fakeFactory) Resume(
	ctx context.Context,
	owner *scope.Scope,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	f.mu.Lock()
	f.resumes = append(f.resumes, options)
	failure := f.resumeErr
	f.mu.Unlock()

	if failure != nil {
		return agent.Handle{}, failure
	}
	loaded, err := f.store.Inspect(ctx, options.ResumeSessionID)
	if err != nil {
		return agent.Handle{}, err
	}
	// 一个续跑起来的会话，它整段存下来的日志就是它的 seed，所以 SeedLength 是
	// 全长；而那条耐久血统边界原样从存档那份头里带回来。
	return f.publish(ctx, owner, options.ResumeSessionID, coresession.CreateOptions{
		Seed:            loaded.Events,
		Cwd:             loaded.Meta.Cwd,
		ParentSession:   loaded.Meta.ParentSession,
		SeedLength:      loaded.Meta.SeedLength,
		Origin:          loaded.Meta.Origin,
		DelegationDepth: loaded.Meta.DelegationDepth,
	}, options.AgentOptions, options.Setup)
}

// publish 是两条造法共用的那半段：作用域、会话、setup、登记、公布。
func (f *fakeFactory) publish(
	ctx context.Context,
	owner *scope.Scope,
	id session.SessionID,
	create coresession.CreateOptions,
	agentOptions agent.Options,
	setup agent.Setup,
) (agent.Handle, error) {
	agentScope, err := scope.New(scope.NewKey(string(id)), scope.Options{Parent: owner.Key()})
	if err != nil {
		return agent.Handle{}, err
	}
	live, err := f.sessions.Create(ctx, agentScope, id, create)
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if setup != nil {
		commit, err := setup(ctx, agentScope)
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
	child := newChildAgent(id, agentScope, live)
	child.options = agentOptions

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

	f.mu.Lock()
	f.children[id] = child
	planted := f.disposeErr
	published := f.onPublished
	f.mu.Unlock()

	if published != nil {
		published(id)
	}

	var once sync.Once
	return agent.Handle{Agent: child, Dispose: func(ctx context.Context) error {
		var failure error
		once.Do(func() {
			// 次序：先从注册表摘掉，再放掉作用域——作用域上挂着会话那次摘除，
			// 而结清路上的最后一次刷盘要求那份会话还登记着。
			failure = errors.Join(planted, detach(ctx), agentScope.Dispose(ctx))
		})
		return failure
	}}, nil
}

// ---- 装配 ----

// continuationFixture 是一台装好的续接管理器和它周围那几样东西。
type continuationFixture struct {
	manager  *ContinuationManager
	agents   *agent.Registry
	sessions *coresession.Store
	store    *fakePersistence
	factory  *fakeFactory
	host     *fakeHost
	owner    *scope.Scope

	// retires 是那些手工造出来的父各自的摘除动作，按会话 id；每一个都只走一遍。
	retires map[session.SessionID]func()
}

// newContinuation 装一台带持久化的续接管理器。
func newContinuation(t *testing.T) *continuationFixture {
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
	emitter, err := newLifecycleEmitter(quiet)
	if err != nil {
		t.Fatalf("造生命周期发射器失败：%v", err)
	}

	owner := keyedScope(t, "subagents", nil)
	store := newPersistence()
	factory := newFactory(t, agents, sessions, store)
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}
	host := &fakeHost{emitter: emitter}

	// 工具运行时也装上：不装的话「给孩子设工具范围」那一路会在组装孩子时被拒，
	// 而那正是可续孩子要覆盖的一条边。
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	// 摆一件真存在的全局工具：工具范围那道校验会拒绝点到不存在的名字，所以
	// 一份空的运行时连合法的限制都收不下。
	if _, err := toolRuntime.Register(t.Context(), owner, &tools.Definition{
		Name:        "read",
		Description: "read",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				return textContent(string(value)), nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`""`), nil
		},
	}); err != nil {
		t.Fatalf("注册工具失败：%v", err)
	}

	manager, err := NewContinuationManager(ContinuationDeps{
		Owner:       owner,
		Agents:      agents,
		Sessions:    sessions,
		Persistence: store,
		Setups:      NewActivationSetupRegistry(),
		Composition: ChildCompositionServices{
			SystemPrompt: promptRegistry(t, ""),
			Tools:        toolRuntime,
		},
		Logger: quiet,
	}, host)
	if err != nil {
		t.Fatalf("造续接管理器失败：%v", err)
	}
	return &continuationFixture{
		manager: manager, agents: agents, sessions: sessions,
		store: store, factory: factory, host: host, owner: owner,
		retires: map[session.SessionID]func(){},
	}
}

// spawnParent 造一个**活着的**父 agent：会话进活会话表，agent 进注册表。
//
// 管理器每一道血统检查都要求父是注册表里那个确切的对象，所以父不能是一个
// 游离的假 agent。
func (f *continuationFixture) spawnParent(t *testing.T, id session.SessionID, parent session.SessionID) *childAgent {
	t.Helper()
	agentScope := keyedScope(t, string(id), f.owner.Key())
	live, err := f.sessions.Create(t.Context(), agentScope, id, coresession.CreateOptions{
		Cwd:           testAbsolutePath,
		ParentSession: parent,
	})
	if err != nil {
		t.Fatalf("建父会话失败：%v", err)
	}
	built := newChildAgent(id, agentScope, live)
	detach, err := f.agents.Register(t.Context(), built, nil)
	if err != nil {
		t.Fatalf("登记父 agent 失败：%v", err)
	}
	var once sync.Once
	retire := func() { once.Do(func() { _ = detach(context.Background()) }) }
	f.retires[id] = retire
	t.Cleanup(retire)
	return built
}

// retire 把某个手工造出来的父从注册表里摘掉，模拟「父已经不在了」。
func (f *continuationFixture) retire(t *testing.T, id session.SessionID) {
	t.Helper()
	retire, found := f.retires[id]
	if !found {
		t.Fatalf("没有 %q 这个手工造出来的父", id)
	}
	retire()
}

// startChild 起一个可续孩子，并断言它起来了。
func (f *continuationFixture) startChild(
	t *testing.T,
	parent agent.Agent,
	childID session.SessionID,
	prompt string,
) ContinuableStart {
	t.Helper()
	started, err := f.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  childID,
		Request:  StartRequest{Prompt: textContent(prompt), Parent: parent},
	})
	if err != nil {
		t.Fatalf("起可续孩子失败：%v", err)
	}
	return started
}

// livingActivation 交出某个孩子那份活的活化，没有就当场失败。
func (f *continuationFixture) livingActivation(t *testing.T, childID session.SessionID) *activation {
	t.Helper()
	f.manager.mutex.Lock()
	defer f.manager.mutex.Unlock()

	live, found := f.manager.activations[childID]
	if !found {
		t.Fatalf("孩子 %q 该有一份活的活化", childID)
	}
	return live
}

// resident 说这个孩子此刻在不在驻留。
func (f *continuationFixture) resident(childID session.SessionID) bool {
	f.manager.mutex.Lock()
	defer f.manager.mutex.Unlock()

	_, found := f.manager.activations[childID]
	return found
}

// ---- 处置事务 ----

func TestDisposalTxHandsTheSameOutcomeToEveryWaiter(t *testing.T) {
	transaction := newDisposalTx()
	broken := errors.New("拆不干净")
	transaction.settle(broken)

	for range 2 {
		if err := transaction.wait(t.Context()); !errors.Is(err, broken) {
			t.Fatalf("每一个等的人该拿同一个结局，实际 %v", err)
		}
	}
}

// 等的人自己被取消时报 [CodeCancelled]，而这次处置本身照旧往下走。
func TestDisposalTxWaitStopsWhenTheWaiterIsCancelled(t *testing.T) {
	transaction := newDisposalTx()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if code := codeOf(transaction.wait(ctx)); code != CodeCancelled {
		t.Fatalf("该报 %s，实际 %s", CodeCancelled, code)
	}
	transaction.settle(nil)
	if err := transaction.wait(t.Context()); err != nil {
		t.Fatalf("这次处置本身该照旧结清，实际 %v", err)
	}
}

// ---- 结清那一行说辞 ----

// 每一种停止原因都有自己那句话，而且都点得出是哪个孩子。
func TestSettlementSummarySpeaksForEveryStopReason(t *testing.T) {
	for _, reason := range []StopReason{
		StopCompleted, StopAborted, StopMaxTokens, StopRefusal, StopError,
	} {
		summary := settlementSummary("child", reason)
		if !strings.Contains(summary, "child") {
			t.Fatalf("%s 那句话该点出是哪个孩子，实际 %q", reason, summary)
		}
	}
}

// 认不出的结局报成「没做完」，而不是无声地当成成功。
func TestSettlementSummaryTreatsAnUnknownReasonAsAbnormal(t *testing.T) {
	summary := settlementSummary("child", StopReason("从来没见过"))
	if !strings.Contains(summary, "abnormally") || !strings.Contains(summary, "从来没见过") {
		t.Fatalf("认不出的结局该报成没做完并带上那个取值，实际 %q", summary)
	}
	if strings.Contains(summary, "finished and will do no further work") {
		t.Fatal("认不出的结局绝不该说成做完了")
	}
}

// ---- 取消的说法 ----

func TestContinuationCancelledOnlySpeaksWhenTheCallerIsGone(t *testing.T) {
	if err := continuationCancelled(t.Context()); err != nil {
		t.Fatalf("没取消该是 nil，实际 %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := continuationCancelled(ctx)
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("原因该还认得出来，实际 %v", err)
	}
}

// ---- 构造 ----

// 那四样必填服务和宿主，缺哪一样都当场拒绝。
func TestNewContinuationManagerRefusesAnIncompleteAssembly(t *testing.T) {
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	whole := ContinuationDeps{
		Owner:    keyedScope(t, "subagents", nil),
		Agents:   agents,
		Sessions: sessions,
		Setups:   NewActivationSetupRegistry(),
	}

	for name, strip := range map[string]func(*ContinuationDeps){
		"没有拥有它的作用域":    func(deps *ContinuationDeps) { deps.Owner = nil },
		"没有 agent 注册表": func(deps *ContinuationDeps) { deps.Agents = nil },
		"没有活会话表":       func(deps *ContinuationDeps) { deps.Sessions = nil },
		"没有装配登记表":      func(deps *ContinuationDeps) { deps.Setups = nil },
	} {
		t.Run(name, func(t *testing.T) {
			deps := whole
			strip(&deps)
			if _, err := NewContinuationManager(deps, &fakeHost{}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("该被拒，实际 %v", err)
			}
		})
	}
	if _, err := NewContinuationManager(whole, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有宿主该被拒，实际 %v", err)
	}
}

// 持久化和审批都可以不在场：管理器照样立得起来，只是起不了可续孩子。
func TestNewContinuationManagerStandsWithoutPersistence(t *testing.T) {
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	manager, err := NewContinuationManager(ContinuationDeps{
		Owner:    keyedScope(t, "subagents", nil),
		Agents:   agents,
		Sessions: sessions,
		Setups:   NewActivationSetupRegistry(),
	}, &fakeHost{})
	if err != nil {
		t.Fatalf("造续接管理器失败：%v", err)
	}
	store, err := manager.requirePersistence()
	if code := codeOf(err); code != CodePersistenceUnavailable {
		t.Fatalf("该报 %s，实际 %s", CodePersistenceUnavailable, code)
	}
	if store != nil {
		t.Fatalf("不在场时不该交回一个存档，实际 %#v", store)
	}
}

// 拥有它的那把作用域一处置，先排干、再放掉那把私有的活化所有者作用域。
//
// 次序倒过来的话，那些活化句柄会被结构性地拆掉，而「孩子优先」那条次序就绕过去了。
func TestDisposingTheOwnerDrainsBeforeReleasingTheActivationScope(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	child := fixture.factory.child("child")
	if child == nil {
		t.Fatal("该造出那个孩子")
	}
	if err := fixture.owner.Dispose(context.Background()); err != nil {
		t.Fatalf("处置拥有它的作用域失败：%v", err)
	}
	if fixture.resident("child") {
		t.Fatal("排干之后不该还有驻留")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("排干之后那个孩子不该还在注册表里")
	}
	// 排干走的是孩子优先那条路：它先被取消，而不是被作用域结构性地拆掉。
	if len(child.cancelled()) == 0 {
		t.Fatal("排干该先取消那个孩子")
	}
}
