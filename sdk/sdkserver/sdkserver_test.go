// 本文件的作用：把这台服务器的三个请求、四条通知、以及收摊那条次序敏感的路，
// 各自用一台真的运行时压一遍。
//
// # 这些测试防的是什么错
//
//   - 装配面缺一样却拖到第一个请求进来才空指针——四个必填协作者各有一条用例。
//   - 装了两次、或者握了两次手：后者会再挂一次兜底适配器，把第一次那次挂载的
//     撤销函数覆盖掉，从此再也卸不掉。
//   - 同一个会话标识上并发的两次 `session/prompt` 建出两个 agent。
//   - 收摊时漏掉一个还在半路上的创建：那个 agent 会活过这次拆解，没人拆得掉它。
//   - 一次只重载 agent 循环的重启把 agent 拆了、会话记录还在，于是 Followup 投进
//     一个已经死掉的 agent，悄无声息。
//   - 认不出的方法名回 -32603，让客户端把「你没有这个方法」当成「你炸了」去重试。
//   - 四条通知转错：把非本地的子 agent 报出去、把顶层会话当成子会话报出去、
//     或者把一个说不出名字的 agent 状态报成空闲（对面会以为这一轮结束了）。
//   - 一条发不出去的通知把发出那条边的追加或拆解带崩。

package sdkserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/invariants"
	"ds-harness-go/llm"
	"ds-harness-go/sdk/sdkprotocol"
	sessionlog "ds-harness-go/session"
	"ds-harness-go/subagent/subagent"
)

// absolutePath 是一条在本机上确实绝对的路径；写死哪一边的字面量都会让另一个平台上的
// 用例变成假通过。
var absolutePath = filepath.Join(os.TempDir(), "ds-harness-go-sdkserver")

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

// ---- 假对面 ----

// note 是记下来的一条通知。
type note struct {
	method string
	params any
}

// recordingPeer 把每一条发出去的通知记下来，并且可以被要求发不出去。
type recordingPeer struct {
	mutex sync.Mutex
	notes []note
	fail  error
}

func (p *recordingPeer) Request(context.Context, string, any, any) error {
	return errors.New("这条协议上服务端不发请求")
}

func (p *recordingPeer) Notify(_ context.Context, method string, params any) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.notes = append(p.notes, note{method: method, params: params})
	return nil
}

// taken 交出到目前为止记下来的那些通知。
func (p *recordingPeer) taken() []note {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]note(nil), p.notes...)
}

// only 交出唯一一条某个方法名的通知，多于一条或一条都没有时当场失败。
func (p *recordingPeer) only(t *testing.T, method string) any {
	t.Helper()
	var found []any
	for _, one := range p.taken() {
		if one.method == method {
			found = append(found, one.params)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s 该恰好有一条，实际 %d 条（全部：%v）", method, len(found), p.taken())
	}
	return found[0]
}

// ---- 假提供方名册 ----

type stubProviders struct{ entries []llm.ProviderInfo }

func (s stubProviders) ListProviders() []llm.ProviderInfo { return s.entries }

// ---- 假 agent ----

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent；它当场就静。
type stubAgent struct {
	id      sessionlog.SessionID
	scope   *scope.Scope
	live    *coresession.Session
	options agent.Options

	mutex     sync.Mutex
	followups []llm.Message
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                    { return a.options }
func (a *stubAgent) Session() *coresession.Session                             { return a.live }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.scope }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusIdle }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}
func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *stubAgent) Followup(message llm.Message) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.followups = append(a.followups, message)
}

func (a *stubAgent) delivered() []llm.Message {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]llm.Message(nil), a.followups...)
}

// ---- 记账的造法 ----

// scriptedFactory 把整条真创建路走完，并且可以被要求在建之前先卡住或者直接报错。
type scriptedFactory struct {
	agents   *agent.Registry
	sessions *coresession.Store

	mutex   sync.Mutex
	creates []agent.CreateOptions
	fail    error
	// gate 非 nil 时，每一次创建都先等它开——收摊那几条竞态用例靠它把创建卡在半路。
	gate chan struct{}
	// enter 非 nil 时，每一次创建在卡住之前先往它上面报一声，好让用例确知这一次创建
	// **已经**在半路上了（pending 已经上膛），而不是靠猜。
	enter chan struct{}
	// disposeFail 非 nil 时，建出来的句柄拆不掉。
	disposeFail error
}

func (f *scriptedFactory) created() []agent.CreateOptions {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]agent.CreateOptions(nil), f.creates...)
}

func (f *scriptedFactory) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	f.mutex.Lock()
	f.creates = append(f.creates, options)
	failure, gate, enter, disposeFail := f.fail, f.gate, f.enter, f.disposeFail
	f.mutex.Unlock()

	if enter != nil {
		enter <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	if failure != nil {
		return agent.Handle{}, failure
	}

	agentScope, err := scope.New(scope.NewKey(string(options.SessionID)), scope.Options{Parent: owner.Key()})
	if err != nil {
		return agent.Handle{}, err
	}
	live, err := f.sessions.Create(ctx, agentScope, options.SessionID, coresession.CreateOptions{
		Cwd:           options.Cwd,
		ParentSession: options.ParentSession,
		SeedLength:    options.SeedLength,
	})
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	child := &stubAgent{id: options.SessionID, scope: agentScope, live: live, options: options.AgentOptions}

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
		once.Do(func() {
			failure = errors.Join(detach(ctx), agentScope.Dispose(ctx), disposeFail)
		})
		return failure
	}}, nil
}

func (f *scriptedFactory) Resume(context.Context, *scope.Scope, agent.ResumeOptions) (agent.Handle, error) {
	return agent.Handle{}, errors.New("这台装配不走续跑")
}

// ---- 装配 ----

// live 是一台装好的服务器和它周边那几样真协作者。
type live struct {
	server   *Server
	peer     *recordingPeer
	agents   *agent.Registry
	sessions *coresession.Store
	subs     *subagent.Runtime
	factory  *scriptedFactory
	owner    *scope.Scope
}

// newLive 装一台服务器：真的 agent 注册表、真的会话存储、真的子 agent 运行时，
// 假的只有那条通道和 agent 本身。
//
// 订阅落在 [scope.NewRoot] 造的那个作用域上——全局层，看得见运行时里的每一个会话，
// 这正是这台服务器该有的视野。
func newLive(t *testing.T, mutate func(*Config)) *live {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: tickingClock()})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	factory := &scriptedFactory{agents: agents, sessions: sessions}
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}
	subs, err := subagent.NewRuntime(subagent.RuntimeOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造子 agent 运行时失败：%v", err)
	}

	peer := &recordingPeer{}
	config := Config{
		Peer:      peer,
		Agents:    agents,
		Sessions:  sessions,
		Subagents: subs,
		Providers: stubProviders{entries: []llm.ProviderInfo{{ID: "known", Name: "known"}}},
		Logger:    quiet,
	}
	if mutate != nil {
		mutate(&config)
	}
	server, err := New(config)
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}

	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	shutdown, err := server.Install(t.Context(), owner)
	if err != nil {
		t.Fatalf("装服务器失败：%v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	return &live{
		server:   server,
		peer:     peer,
		agents:   agents,
		sessions: sessions,
		subs:     subs,
		factory:  factory,
		owner:    owner,
	}
}

// handshake 走一次成功的握手，提供方用名册里已有的那个（走不到兜底那条路）。
func (l *live) handshake(t *testing.T) {
	t.Helper()
	if _, err := l.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd:      absolutePath,
		Provider: "known",
		Model:    "m",
	}); err != nil {
		t.Fatalf("握手不该失败：%v", err)
	}
}

// prompt 排一轮用户输入。
func (l *live) prompt(t *testing.T, sessionID string) sdkprotocol.SessionPromptResult {
	t.Helper()
	result, err := l.server.Prompt(t.Context(), sdkprotocol.SessionPromptParams{
		SessionID:     sessionID,
		ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("排一轮输入不该失败：%v", err)
	}
	return result
}

// stallOneCreation 起一次会卡在半路的 `session/prompt`，等到它**确实**卡住了才返回。
//
// 交回那道闸和那次排队的结论。闸一关，被卡住的那次创建就往下走。
//
// 这两件事必须分开等：先等创建真的进了造法（那时 pending 已经上膛），再让用例去开收摊
// ——反过来的话，收摊可能在创建上膛之前就把门关上了，测的就变成另一条路了。
func (l *live) stallOneCreation(t *testing.T, sessionID string) (chan struct{}, chan error) {
	t.Helper()
	gate := make(chan struct{})
	enter := make(chan struct{})
	l.factory.mutex.Lock()
	l.factory.gate = gate
	l.factory.enter = enter
	l.factory.mutex.Unlock()

	failed := make(chan error, 1)
	go func() {
		_, err := l.server.Prompt(context.Background(), sdkprotocol.SessionPromptParams{
			SessionID:     sessionID,
			ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
		})
		failed <- err
	}()
	<-enter
	return gate, failed
}

// awaitShuttingDown 等到收摊真的把门关上。
//
// 收摊把那个开关置起来之后才去等半路上那些创建，所以这个自旋一定停得下来——它等的
// 正是「开关已经置起来了」这一刻，不是一段猜出来的时长。
func (l *live) awaitShuttingDown() {
	for {
		l.server.mutex.Lock()
		down := l.server.shuttingDown
		l.server.mutex.Unlock()
		if down {
			return
		}
		runtime.Gosched()
	}
}

// ---- 装配面 ----

func TestNewRejectsAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	full := Config{
		Peer:      &recordingPeer{},
		Agents:    &agent.Registry{},
		Sessions:  &coresession.Store{},
		Subagents: &subagent.Runtime{},
	}
	cases := map[string]func(*Config){
		"没有通道":          func(c *Config) { c.Peer = nil },
		"没有 agent 注册表":  func(c *Config) { c.Agents = nil },
		"没有会话存储":        func(c *Config) { c.Sessions = nil },
		"没有子 agent 运行时": func(c *Config) { c.Subagents = nil },
	}
	for name, strip := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := full
			strip(&config)
			if _, err := New(config); err == nil {
				t.Fatalf("%s 该在造的时候就拒", name)
			}
		})
	}
	if _, err := New(full); err != nil {
		t.Fatalf("四样齐了该造得出来：%v", err)
	}
}

func TestInstallNeedsAnOwnerScope(t *testing.T) {
	t.Parallel()
	server, err := New(Config{
		Peer:      &recordingPeer{},
		Agents:    &agent.Registry{},
		Sessions:  &coresession.Store{},
		Subagents: &subagent.Runtime{},
	})
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}
	if _, err := server.Install(t.Context(), nil); err == nil {
		t.Fatal("没有作用域该装不上")
	}
}

func TestInstallHappensOnlyOnce(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	if _, err := fixture.server.Install(t.Context(), scope.NewRoot()); err == nil {
		t.Fatal("装第二次该被拒")
	}
}

// 一个已经散掉的作用域挂不上订阅，那时这台服务器必须干干净净地退回没装过的样子——
// 否则它会留下一条往一台永远不会开工的服务器上转发的边。
func TestInstallRollsBackWhenSubscribingFails(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	second, err := New(Config{
		Peer:      fixture.peer,
		Agents:    fixture.agents,
		Sessions:  fixture.sessions,
		Subagents: fixture.subs,
	})
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}
	dead := scopeOf(t, "dead")
	if err := dead.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if _, err := second.Install(t.Context(), dead); err == nil {
		t.Fatal("挂不上订阅该装不上")
	}
	// 退回去了：换一个活着的作用域再装一次该成。
	if _, err := second.Install(t.Context(), scope.NewRoot()); err != nil {
		t.Fatalf("退回之后该再装得上：%v", err)
	}
	if err := second.Shutdown(t.Context()); err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
}

// ---- 握手 ----

func TestInitializeRecordsTheSharedRoute(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	limit := 4096
	result, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd:       absolutePath,
		Provider:  "known",
		Model:     "m",
		MaxTokens: &limit,
	})
	if err != nil {
		t.Fatalf("握手不该失败：%v", err)
	}
	if result.ServerInfo.Name != sdkprotocol.ServerName || result.ServerInfo.Version != ServerVersion {
		t.Fatalf("服务端身份不对：%+v", result.ServerInfo)
	}

	fixture.prompt(t, "s1")
	created := fixture.factory.created()
	if len(created) != 1 {
		t.Fatalf("该建出一个 agent，实际 %d 个", len(created))
	}
	want := agent.Options{Provider: "known", Model: "m", MaxTokens: limit}
	if created[0].AgentOptions != want {
		t.Fatalf("这条线上的路由该照握手记下的那份，实际 %+v", created[0].AgentOptions)
	}
	if created[0].Cwd != absolutePath {
		t.Fatalf("工作目录该是握手给的那条，实际 %q", created[0].Cwd)
	}
}

func TestInitializeRejectsANonPositiveMaxTokens(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	zero := 0
	if _, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "known", Model: "m", MaxTokens: &zero,
	}); err == nil {
		t.Fatal("输出上限为 0 该被拒")
	}
}

func TestInitializeNeedsAnInstalledServer(t *testing.T) {
	t.Parallel()
	server, err := New(Config{
		Peer:      &recordingPeer{},
		Agents:    &agent.Registry{},
		Sessions:  &coresession.Store{},
		Subagents: &subagent.Runtime{},
	})
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}
	if _, err := server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "known", Model: "m",
	}); err == nil {
		t.Fatal("还没装上该握不了手")
	}
}

// 重来一次会再挂一次兜底适配器，把第一次那次挂载的撤销函数覆盖掉，从此再也卸不掉。
func TestInitializeRefusesASecondHandshake(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	if _, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "known", Model: "m",
	}); err == nil {
		t.Fatal("握第二次手该被拒")
	}
}

func TestInitializeRefusesAnUnclaimedProviderWithoutAFallback(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	if _, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "nobody", Model: "m",
	}); err == nil {
		t.Fatal("没有适配器认领、又没有兜底钩子，该被拒")
	}
}

// 没挂 LLM 服务时一律当作「没有适配器认领」，于是照样走兜底那条路。
func TestInitializeMountsTheFallbackAdapterWhenUnclaimed(t *testing.T) {
	t.Parallel()
	var mounted, unmounted atomic.Int64
	fixture := newLive(t, func(c *Config) {
		c.Providers = nil
		c.MountAdapter = func(context.Context, string) (func(context.Context) error, error) {
			mounted.Add(1)
			return func(context.Context) error { unmounted.Add(1); return nil }, nil
		}
	})
	fixture.handshake(t)
	if mounted.Load() != 1 {
		t.Fatalf("该兜底挂一次适配器，实际 %d 次", mounted.Load())
	}
	if err := fixture.server.Shutdown(t.Context()); err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
	if unmounted.Load() != 1 {
		t.Fatalf("收摊该把兜底适配器卸掉，实际 %d 次", unmounted.Load())
	}
}

func TestInitializeReportsAFailedFallbackMount(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, func(c *Config) {
		c.MountAdapter = func(context.Context, string) (func(context.Context) error, error) {
			return nil, errors.New("挂不上")
		}
	})
	if _, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "nobody", Model: "m",
	}); err == nil {
		t.Fatal("兜底挂载失败该把握手带失败")
	}
}

// ---- 排输入 ----

func TestPromptCreatesTheSessionOnFirstSight(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)

	first := fixture.prompt(t, "s1")
	if first.MessageID == "" {
		t.Fatal("该交回一条消息的身份")
	}
	fixture.prompt(t, "s1")
	if got := len(fixture.factory.created()); got != 1 {
		t.Fatalf("同一个会话标识只该建一次，实际 %d 次", got)
	}
	handle, ok := fixture.agents.Get("s1")
	if !ok {
		t.Fatal("那个 agent 该在注册表里")
	}
	if got := len(handle.(*stubAgent).delivered()); got != 2 {
		t.Fatalf("两轮输入该都投进去，实际 %d 条", got)
	}
}

// 同一个会话标识上并发的几次创建必须合成一次，否则第二个 agent 会把第一个从
// sessions 里挤掉，成为一个没人拆得掉的孤儿。
func TestConcurrentPromptsShareOneCreation(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	gate := make(chan struct{})
	fixture.factory.mutex.Lock()
	fixture.factory.gate = gate
	fixture.factory.mutex.Unlock()

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = fixture.server.Prompt(context.Background(), sdkprotocol.SessionPromptParams{
				SessionID:     "s1",
				ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
			})
		}()
	}
	close(gate)
	wait.Wait()
	if got := len(fixture.factory.created()); got != 1 {
		t.Fatalf("并发的几次创建该合成一次，实际 %d 次", got)
	}
}

func TestPromptReportsAFailedCreation(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.factory.mutex.Lock()
	fixture.factory.fail = errors.New("建不出来")
	fixture.factory.mutex.Unlock()

	if _, err := fixture.server.Prompt(t.Context(), sdkprotocol.SessionPromptParams{
		SessionID: "s1", ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
	}); err == nil {
		t.Fatal("建不出 agent 该把这次排队带失败")
	}
}

func TestPromptNeedsAnInstalledServer(t *testing.T) {
	t.Parallel()
	server, err := New(Config{
		Peer:      &recordingPeer{},
		Agents:    &agent.Registry{},
		Sessions:  &coresession.Store{},
		Subagents: &subagent.Runtime{},
	})
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}
	if _, err := server.Prompt(t.Context(), sdkprotocol.SessionPromptParams{SessionID: "s1"}); err == nil {
		t.Fatal("还没装上该排不了输入")
	}
}

// 一次只重载 agent 循环的重启会把循环里的 agent 拆掉而这条会话记录活下来；一个被拆掉
// 的 agent 收下 Followup 之后什么都不会发生，所以投递之前必须先验一遍。
func TestPromptRefusesASessionWhoseAgentDiedElsewhere(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "s1")

	fixture.server.mutex.Lock()
	handle := fixture.server.sessions["s1"]
	fixture.server.mutex.Unlock()
	if err := handle.Dispose(t.Context()); err != nil {
		t.Fatalf("在服务器之外拆掉那个 agent 失败：%v", err)
	}

	if _, err := fixture.server.Prompt(t.Context(), sdkprotocol.SessionPromptParams{
		SessionID: "s1", ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
	}); err == nil {
		t.Fatal("agent 在服务器之外被拆掉了，该拒这次投递")
	}
}

// ---- 收摊 ----

func TestShutdownDisposesEverythingItBuilt(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "s1")
	fixture.prompt(t, "s2")

	if err := fixture.server.Shutdown(t.Context()); err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
	for _, id := range []sessionlog.SessionID{"s1", "s2"} {
		if _, live := fixture.agents.Get(id); live {
			t.Fatalf("%s 该被拆掉了", id)
		}
	}
	// 订阅也摘了：这之后运行时里的动静一条都不该再转出去。
	before := len(fixture.peer.taken())
	if _, err := fixture.sessions.Create(t.Context(), scopeOf(t, "after"), "s3", coresession.CreateOptions{
		Cwd: absolutePath, ParentSession: "s1",
	}); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	if got := len(fixture.peer.taken()); got != before {
		t.Fatalf("摘完订阅之后不该再有通知，实际多了 %d 条", got-before)
	}
}

func TestShutdownRunsOnlyOnceAndKeepsItsVerdict(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.factory.mutex.Lock()
	fixture.factory.disposeFail = errors.New("拆不掉")
	fixture.factory.mutex.Unlock()
	fixture.prompt(t, "s1")

	first := fixture.server.Shutdown(t.Context())
	if first == nil {
		t.Fatal("拆不掉的 agent 该把收摊带失败")
	}
	if second := fixture.server.Shutdown(t.Context()); !errors.Is(second, first) {
		t.Fatalf("第二次收摊该交回同一个结论，实际 %v", second)
	}
}

func TestShutdownReportsAFailedUnmount(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, func(c *Config) {
		c.MountAdapter = func(context.Context, string) (func(context.Context) error, error) {
			return func(context.Context) error { return errors.New("卸不掉") }, nil
		}
	})
	if _, err := fixture.server.Initialize(t.Context(), sdkprotocol.InitializeParams{
		Cwd: absolutePath, Provider: "nobody", Model: "m",
	}); err != nil {
		t.Fatalf("握手失败：%v", err)
	}
	if err := fixture.server.Shutdown(t.Context()); err == nil {
		t.Fatal("卸不掉兜底适配器该把收摊带失败")
	}
}

func TestShutdownReportsAFailedSubscriptionRelease(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.server.mutex.Lock()
	fixture.server.disposers = append(fixture.server.disposers, func(context.Context) error {
		return errors.New("摘不掉")
	})
	fixture.server.mutex.Unlock()
	if err := fixture.server.Shutdown(t.Context()); err == nil {
		t.Fatal("摘不掉订阅该把收摊带失败")
	}
}

func TestShutdownRefusesNewSessions(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	if err := fixture.server.Shutdown(t.Context()); err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
	if _, err := fixture.server.Prompt(t.Context(), sdkprotocol.SessionPromptParams{
		SessionID: "s1", ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}},
	}); err == nil {
		t.Fatal("收摊之后该不再接新会话")
	}
}

// 一个卡在半路上的创建：收摊必须等它落地，落地之后那个 agent 必须当场就地拆掉——
// 收摊已经把 sessions 清了，再记进去就没人拆得掉它。
func TestShutdownWaitsForAnInFlightCreationAndDisposesIt(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	gate, failed := fixture.stallOneCreation(t, "s1")

	done := make(chan error, 1)
	go func() { done <- fixture.server.Shutdown(context.Background()) }()
	fixture.awaitShuttingDown()
	close(gate)

	if err := <-failed; err == nil {
		t.Fatal("收摊途中建出来的会话该把这次排队带失败")
	}
	if err := <-done; err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
	if _, alive := fixture.agents.Get("s1"); alive {
		t.Fatal("收摊途中建出来的那个 agent 该当场就地拆掉")
	}
}

func TestShutdownReportsAnInFlightCreationThatCannotBeDisposed(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.factory.mutex.Lock()
	fixture.factory.disposeFail = errors.New("拆不掉")
	fixture.factory.mutex.Unlock()
	gate, failed := fixture.stallOneCreation(t, "s1")

	done := make(chan error, 1)
	go func() { done <- fixture.server.Shutdown(context.Background()) }()
	fixture.awaitShuttingDown()
	close(gate)

	if err := <-failed; err == nil {
		t.Fatal("拆不掉的那一个该报出拆解失败")
	}
	<-done
}

// ---- 派活 ----

func TestHandleRequestDispatchesTheThreeMethods(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	if fixture.server.Handlers().Notification != nil {
		t.Fatal("这条协议上服务端不收通知")
	}
	raw := func(value any) json.RawMessage {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("排负载失败：%v", err)
		}
		return encoded
	}

	if _, err := fixture.server.HandleRequest(t.Context(), sdkprotocol.MethodInitialize, raw(
		sdkprotocol.InitializeParams{Cwd: absolutePath, Provider: "known", Model: "m"},
	)); err != nil {
		t.Fatalf("initialize 该派得出去：%v", err)
	}
	if _, err := fixture.server.HandleRequest(t.Context(), sdkprotocol.MethodSessionPrompt, raw(
		sdkprotocol.SessionPromptParams{SessionID: "s1", ContentBlocks: llm.Content{llm.TextBlock{Text: "hi"}}},
	)); err != nil {
		t.Fatalf("session/prompt 该派得出去：%v", err)
	}
	result, err := fixture.server.HandleRequest(t.Context(), sdkprotocol.MethodShutdown, raw(struct{}{}))
	if err != nil {
		t.Fatalf("shutdown 该派得出去：%v", err)
	}
	if result != (struct{}{}) {
		t.Fatalf("shutdown 的结果在协议上写死是空对象，实际 %v", result)
	}
}

// 认不出的方法名要落成 -32601。回 -32603 会让客户端把「你这个版本没有这个方法」
// 当成「你这边炸了」，然后去重试一件永远不会成立的事。
func TestHandleRequestReportsAnUnknownMethodAsMethodNotFound(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	_, err := fixture.server.HandleRequest(t.Context(), "nope", json.RawMessage(`{}`))
	if !errors.Is(err, sdkprotocol.ErrMethodNotFound) {
		t.Fatalf("该是「认不出的方法名」，实际 %v", err)
	}
}

func TestHandleRequestReportsUndecodableParams(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	for _, method := range []string{sdkprotocol.MethodInitialize, sdkprotocol.MethodSessionPrompt} {
		if _, err := fixture.server.HandleRequest(t.Context(), method, json.RawMessage(`[`)); err == nil {
			t.Fatalf("%s 的入参解不动该报错", method)
		}
	}
}

// shutdown 那条路自己会失败，它的错误必须原样交回去当响应，而不是被吞成空对象。
func TestHandleRequestReportsAFailedShutdown(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.server.mutex.Lock()
	fixture.server.disposers = append(fixture.server.disposers, func(context.Context) error {
		return errors.New("摘不掉")
	})
	fixture.server.mutex.Unlock()
	if _, err := fixture.server.HandleRequest(t.Context(), sdkprotocol.MethodShutdown, nil); err == nil {
		t.Fatal("收摊失败该落成一次失败的响应")
	}
}

// ---- 四条通知 ----

func TestSessionEventsAreForwarded(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "s1")

	handle, _ := fixture.agents.Get("s1")
	if _, err := handle.Session().Append(sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: json.RawMessage(`{"turn":0}`),
	}); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}

	payload := fixture.peer.only(t, sdkprotocol.MethodSessionEvent).(sdkprotocol.SessionEventNotification)
	if payload.SessionID != "s1" {
		t.Fatalf("会话标识不对：%q", payload.SessionID)
	}
	if payload.Event.Type != sessionlog.EventTurnStart {
		t.Fatalf("事件该原样转出去，实际 %q", payload.Event.Type)
	}
}

func TestAgentStatusIsForwarded(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "s1")
	handle, _ := fixture.agents.Get("s1")

	if err := fixture.agents.ReportStatus(handle, agent.StatusRunning); err != nil {
		t.Fatalf("报状态失败：%v", err)
	}
	payload := fixture.peer.only(t, sdkprotocol.MethodSessionStatus).(sdkprotocol.SessionStatusNotification)
	if payload.SessionID != "s1" || payload.Status != sdkprotocol.AgentRunning {
		t.Fatalf("状态通知不对：%+v", payload)
	}
}

// 一个说不出名字的状态**不是**空闲：报成空闲会让对面以为这一轮结束了。
func TestUnknownAgentStatusIsReportedAsRunning(t *testing.T) {
	t.Parallel()
	cases := map[agent.Status]sdkprotocol.AgentStatus{
		agent.StatusIdle:    sdkprotocol.AgentIdle,
		agent.StatusRunning: sdkprotocol.AgentRunning,
		agent.Status("???"): sdkprotocol.AgentRunning,
	}
	for status, want := range cases {
		if got := wireStatus(status); got != want {
			t.Fatalf("%q 该翻成 %q，实际 %q", status, want, got)
		}
	}
}

// 顶层会话一条都不发：对面已经知道它自己开的那些会话。
func TestOnlyChildSessionsAreAnnouncedAsSubagentStarts(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "top")

	if _, err := fixture.sessions.Create(t.Context(), scopeOf(t, "child"), "child", coresession.CreateOptions{
		Cwd: absolutePath, ParentSession: "top",
	}); err != nil {
		t.Fatalf("建子会话失败：%v", err)
	}
	payload := fixture.peer.only(t, sdkprotocol.MethodSubagentStarted).(sdkprotocol.SubagentStartedNotification)
	if payload.ParentSessionID != "top" || payload.ChildSessionID != "child" {
		t.Fatalf("血缘不对：%+v", payload)
	}
}

func TestSubagentEndIsForwardedOnlyForLocalRunsWithAKnownParent(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "parent")
	parent, _ := fixture.agents.Get("parent")

	// 非本地的那一次：那个 Local 标记是子 agent 服务在孩子拆解途中拍下来的事实，
	// 光靠对得上的标识说明不了这一次是本地跑的。
	fixture.server.onSubagentEnd(subagent.RunEndInfo{
		Provider: "p", ID: "child", Local: false, StopReason: subagent.StopCompleted,
	}, parent)
	// 父不在了的那一次：ParentSessionID 是必填的，凭空编一个会报出一段假血缘。
	fixture.server.onSubagentEnd(subagent.RunEndInfo{
		Provider: "p", ID: "child", Local: true, StopReason: subagent.StopCompleted,
	}, nil)
	for _, one := range fixture.peer.taken() {
		if one.method == sdkprotocol.MethodSubagentFinished {
			t.Fatalf("这两次都不该发出去：%+v", one)
		}
	}

	fixture.server.onSubagentEnd(subagent.RunEndInfo{
		Provider:             "p",
		ID:                   "child",
		Local:                true,
		StopReason:           subagent.StopCompleted,
		LastAssistantMessage: llm.Content{llm.TextBlock{Text: "done"}},
	}, parent)
	payload := fixture.peer.only(t, sdkprotocol.MethodSubagentFinished).(sdkprotocol.SubagentFinishedNotification)
	if payload.ParentSessionID != "parent" || payload.ChildSessionID != "child" || payload.AgentID != "child" {
		t.Fatalf("血缘不对：%+v", payload)
	}
	if payload.Status != sdkprotocol.RunOK || payload.StopReason != subagent.StopCompleted {
		t.Fatalf("结论不对：%+v", payload)
	}
	if len(payload.LastAssistantMessage) != 1 {
		t.Fatalf("最后那段助手内容该带上：%+v", payload.LastAssistantMessage)
	}
}

// 「撞上输出上限」算不算被接受的结果，由部署口径说了算。
func TestRunStatusFollowsTheDeploymentVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason  subagent.StopReason
		lenient bool
		want    sdkprotocol.RunStatus
	}{
		{subagent.StopCompleted, false, sdkprotocol.RunOK},
		{subagent.StopCompleted, true, sdkprotocol.RunOK},
		{subagent.StopMaxTokens, false, sdkprotocol.RunError},
		{subagent.StopMaxTokens, true, sdkprotocol.RunOK},
		{subagent.StopAborted, true, sdkprotocol.RunError},
		{subagent.StopError, true, sdkprotocol.RunError},
		{subagent.StopRefusal, true, sdkprotocol.RunError},
	}
	for _, one := range cases {
		if got := runStatus(one.reason, one.lenient); got != one.want {
			t.Fatalf("%q（宽容=%v）该是 %q，实际 %q", one.reason, one.lenient, one.want, got)
		}
	}
}

// 一条发不出去的通知只该留下一行诊断：它改变不了运行时里已经发生的那件事，更不该
// 把发出这条边的那次追加带崩。
func TestAFailedNotificationNeverBreaksTheEdgeThatEmittedIt(t *testing.T) {
	t.Parallel()
	fixture := newLive(t, nil)
	fixture.handshake(t)
	fixture.prompt(t, "s1")
	fixture.peer.mutex.Lock()
	fixture.peer.fail = errors.New("线断了")
	fixture.peer.mutex.Unlock()

	handle, _ := fixture.agents.Get("s1")
	if _, err := handle.Session().Append(sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: json.RawMessage(`{"turn":0}`),
	}); err != nil {
		t.Fatalf("一条发不出去的通知不该把追加带失败：%v", err)
	}
	if err := fixture.server.Shutdown(t.Context()); err != nil {
		t.Fatalf("一条发不出去的通知不该把收摊带失败：%v", err)
	}
}

// 还没装上（或者已经拆干净了）的时候，这条线上没有对面可发。
func TestNotifyIsSilentBeforeInstall(t *testing.T) {
	t.Parallel()
	peer := &recordingPeer{}
	server, err := New(Config{
		Peer:      peer,
		Agents:    &agent.Registry{},
		Sessions:  &coresession.Store{},
		Subagents: &subagent.Runtime{},
	})
	if err != nil {
		t.Fatalf("造服务器失败：%v", err)
	}
	server.notify(sdkprotocol.MethodSessionStatus, sdkprotocol.SessionStatusNotification{})
	if got := len(peer.taken()); got != 0 {
		t.Fatalf("还没装上不该发东西，实际 %d 条", got)
	}
}

// Logger 为 nil 时走 slog 的默认那一台，只要不炸就行。
func TestWarnFallsBackToTheDefaultLogger(t *testing.T) {
	t.Parallel()
	Config{}.warn("这一行落在默认 logger 上")
}

// ---- 不变量 ----

func TestRegisterInvariantsReservesThePackage(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	dispose, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	t.Cleanup(dispose)
	if !slices.Contains(registry.Registered(), PackageName) {
		t.Fatalf("这个包名该被占住，实际 %v", registry.Registered())
	}
}

func TestRegisterInvariantsNeedsARegistry(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表该装不上")
	}
}
