// 本文件的作用：本包全部测试共用的那几样替身——一个握着真会话真收件箱的 agent、
// 一张能自己发事件的 agent 注册表、一道可摆布的会话面，以及把它们和一台**真的**
// 目标服务接在一起的那个台架。
//
// # 这些助手防的是什么错
//
//   - **拿一台假目标服务糊弄过去**。本包所有分支的判定依据都是「此刻这份目标状态
//     长什么样」——阶段、修订号、活化、已开轮数。这四样的演化规则写在
//     [github.com/snight1983/ds-harness-go/goal/goal] 里，不在本包。用替身就等于把那份规则重抄一遍，
//     然后测的是抄件而不是本包。所以台架接的是真的 [goal.Service]。
//   - **拿一个假收件箱糊弄过去**。本包那三条收件箱观察者上判定的是「这条消息是不是
//     我发出去的那一条」，而 [driver.restoreOtherClaimed] 还会真的往队里 prepend。
//     真的 [agent.Inbox] 会把每一次改动写进日志并且验重，替身验不出重。
//   - **让 Followup 只是记个账**。真实接线里 [agent.Agent.Followup] 是**同步**触发
//     inserted 观察者的，而 [driver.queueRound] 那条「预定先立、消息后发」的次序
//     正是为这件事写的。testAgent 照这个次序接线，那条注释才验得到。
//   - **靠驱动协程去跑用例**。大部分用例直接叫 [driver.driveOnce]，不开协程：
//     一台自己跑着的驱动会让断言变成一场赛跑。协程那条路由专门的用例验。

package goalrounddriver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// errTestRegister 是那些「这条观察者装不上」的用例摆出来的失败。
var errTestRegister = errors.New("测试：这条观察者装不上")

// scopeOf 造一个有身份的作用域，用完自动释放。
func scopeOf(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域 %s 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// quietLogger 是一台把诊断全丢掉的日志器。
//
// 本包每一条「出了岔子」的路都会记一条 warn，而那些路正是用例要走的。不静音的话
// 一次 go test 会刷出几百行本来就预期之内的告警，真正的失败被淹在里面。
func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ---- 假 agent ----

// testAgent 是一个握着真会话、真收件箱的 agent。
//
// 它只在两处偏离真实实现：状态由用例直接摆布（真实的状态归循环那一层管），
// 以及 Cancel 只记账不真的掐（本包只关心自己有没有叫它）。
type testAgent struct {
	id     session.SessionID
	own    *scope.Scope
	log    *coresession.Session
	inbox  *agent.Inbox
	agents *testAgents

	mutex   sync.Mutex
	status  agent.Status
	cancels int
	idleErr error

	// inboxMu 把这个 agent 那只收件箱上的每一次改动和每一次读都串起来。
	//
	// [agent.Inbox] 自己不带锁，真实实现（core/agentloop 那台）是拿 agent 那把锁
	// 串的。台架这边同样要串：用例在自己那条协程上读队列，而被测的驱动在另一条
	// 协程上往里追消息——不串就是一次实打实的数据竞争。
	//
	// 它跟上面那把 mutex 分开：那把在 Cancel/Status 里被按住，而收件箱的改动会
	// 内联发出那三条通知，通知又会回头调进这个 agent，共用一把必然死锁。
	inboxMu sync.Mutex
}

// newTestAgent 造一个接进这张注册表的 agent，并把它放上台。
func newTestAgent(t *testing.T, id string, owner *scope.Scope, agents *testAgents) *testAgent {
	t.Helper()
	sessionID := session.SessionID(id)
	header := session.SessionHeader{Version: session.FormatVersion, ID: sessionID, CreatedAt: 1}
	log, err := coresession.NewSession(sessionID, coresession.Options{
		Header: &header,
		Now:    func() int64 { return 1 },
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	made := &testAgent{id: sessionID, own: owner, log: log, agents: agents, status: agent.StatusIdle}
	// 收件箱那三条通知直接接到注册表的广播上：真实接线里正是注册表把它们转出去的，
	// 而本包那三条观察者就挂在注册表那一头。
	inbox, err := agent.NewInbox(log, agent.InboxNotifications{
		Inserted:  func(message llm.Message) { agents.emitInboxInserted(made, message) },
		Discarded: func(message llm.Message) { agents.emitInboxDiscarded(made, message) },
		Claimed:   func(message llm.Message, turn int) { agents.emitInboxClaimed(made, message, turn) },
	})
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}
	made.inbox = inbox
	agents.put(made)
	return made
}

func (a *testAgent) ID() session.SessionID                     { return a.id }
func (a *testAgent) Options() agent.Options                    { return agent.Options{} }
func (a *testAgent) Session() *coresession.Session             { return a.log }
func (a *testAgent) Inbox() *agent.Inbox                       { return a.inbox }
func (a *testAgent) Scope() *scope.Scope                       { return a.own }
func (a *testAgent) Steer(llm.Message)                         {}
func (a *testAgent) Inject(llm.Message)                        {}
func (a *testAgent) Send(llm.Message, agent.InboxTarget, bool) {}

func (a *testAgent) Status() agent.Status {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.status
}

// setStatus 把状态摆到某一个值，**不**发观察者——发不发由用例自己决定。
func (a *testAgent) setStatus(status agent.Status) {
	a.mutex.Lock()
	a.status = status
	a.mutex.Unlock()
}

func (a *testAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {
	a.mutex.Lock()
	a.cancels++
	a.mutex.Unlock()
}

// canceled 数 Cancel 被叫过几次。
func (a *testAgent) canceled() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.cancels
}

func (a *testAgent) WhenIdle(context.Context) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.idleErr
}

func (a *testAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// Followup 走真收件箱：追进 next-turn，于是 inserted 在这次调用返回**之前**发出去。
func (a *testAgent) Followup(message llm.Message) {
	if err := a.appendInbox(agent.NextTurn, message); err != nil {
		panic("测试 agent 的 Followup 追不进收件箱：" + err.Error())
	}
}

// Prepend 把消息放回某条边界的队头。
//
// 真实实现在 agent 自己那把锁里做这件事并把失败报到出错观察者上；台架这边只要
// 串住就够了——本包唯一一次会失败的放回（同一个身份放两遍）在用例里是拿队列长度
// 验的，不是拿这条错验的。
func (a *testAgent) Prepend(message llm.Message, target agent.InboxTarget) {
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	_, _ = a.inbox.Splice(target, 0, 0, []llm.Message{message})
}

// ---- 串起来的收件箱出入口 ----
//
// 用例一律走这几个，别直接碰 a.inbox：直接碰就绕开了 inboxMu。

func (a *testAgent) queuedTurn() []llm.Message {
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	return a.inbox.NextTurn()
}

func (a *testAgent) queuedStep() []llm.Message {
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	return a.inbox.NextStep()
}

func (a *testAgent) appendInbox(target agent.InboxTarget, message llm.Message) error {
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	return a.inbox.Append(target, message)
}

func (a *testAgent) claimInbox(target agent.InboxTarget, turn int) ([]llm.Message, error) {
	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()
	return a.inbox.Claim(target, turn)
}

// appendEvent 往这条日志里直接写一条事件，绕开本包。
func (a *testAgent) appendEvent(t *testing.T, event session.Event) session.Event {
	t.Helper()
	written, err := a.log.Append(event)
	if err != nil {
		t.Fatalf("往日志里写 %s 失败：%v", event.Type, err)
	}
	return written
}

// sessionWithID 另起一段带同一个标识的会话。
//
// 用来摆「同一个 agent 换过一段会话」这件事：标识对得上，但不是驱动开工时握着的
// 那一段。
func sessionWithID(t *testing.T, id session.SessionID) *coresession.Session {
	t.Helper()
	header := session.SessionHeader{Version: session.FormatVersion, ID: id, CreatedAt: 2}
	made, err := coresession.NewSession(id, coresession.Options{
		Header: &header,
		Now:    func() int64 { return 2 },
	})
	if err != nil {
		t.Fatalf("另起一段会话失败：%v", err)
	}
	return made
}

// ---- 假注册表 ----

// testAgents 是那张可摆布的 agent 注册表，兼本包那几条观察者的广播口。
type testAgents struct {
	mutex sync.Mutex
	live  map[session.SessionID]agent.Agent
	order []agent.Agent

	created        []agent.CreatedObserver
	disposed       []agent.DisposedObserver
	status         []agent.StatusObserver
	sessionStart   []agent.SessionStartObserver
	errors         []agent.ErrorObserver
	inboxInserted  []agent.InboxObserver
	inboxClaimed   []agent.InboxClaimedObserver
	inboxDiscarded []agent.InboxObserver
	preStep        []agent.PreStepObserver

	// failOn 是「装到这个名字的观察者时报错」；空表示每一条都装得上。
	failOn string
	// stopped 数退订函数被叫过几次。
	stopped int
	// stopErr 是退订函数交回的错。
	stopErr error
}

// newTestAgents 造一张空注册表。
func newTestAgents() *testAgents {
	return &testAgents{live: map[session.SessionID]agent.Agent{}}
}

func (a *testAgents) Get(id session.SessionID) (agent.Agent, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	found, present := a.live[id]
	return found, present
}

func (a *testAgents) List() []agent.Agent {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]agent.Agent(nil), a.order...)
}

// put 把一个 agent 放上台。
func (a *testAgents) put(member agent.Agent) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, present := a.live[member.ID()]; !present {
		a.order = append(a.order, member)
	}
	a.live[member.ID()] = member
}

// drop 把一个 agent 从台上摘掉。
func (a *testAgents) drop(id session.SessionID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.live, id)
	for index, member := range a.order {
		if member.ID() == id {
			a.order = append(a.order[:index], a.order[index+1:]...)
			break
		}
	}
}

// register 是那九条登记胳膊共用的实现：按 failOn 决定装不装得上。
func (a *testAgents) register(label string, attach func()) (func(context.Context) error, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.failOn == label {
		return nil, errTestRegister
	}
	attach()
	return func(context.Context) error {
		a.mutex.Lock()
		a.stopped++
		failure := a.stopErr
		a.mutex.Unlock()
		return failure
	}, nil
}

func (a *testAgents) OnCreated(
	_ context.Context, _ *scope.Scope, observer agent.CreatedObserver,
) (func(context.Context) error, error) {
	return a.register("created", func() { a.created = append(a.created, observer) })
}

func (a *testAgents) OnDisposed(
	_ context.Context, _ *scope.Scope, observer agent.DisposedObserver,
) (func(context.Context) error, error) {
	return a.register("disposed", func() { a.disposed = append(a.disposed, observer) })
}

func (a *testAgents) OnStatus(
	_ context.Context, _ *scope.Scope, observer agent.StatusObserver,
) (func(context.Context) error, error) {
	return a.register("status", func() { a.status = append(a.status, observer) })
}

func (a *testAgents) OnSessionStart(
	_ context.Context, _ *scope.Scope, observer agent.SessionStartObserver,
) (func(context.Context) error, error) {
	return a.register("session-start", func() { a.sessionStart = append(a.sessionStart, observer) })
}

func (a *testAgents) OnError(
	_ context.Context, _ *scope.Scope, observer agent.ErrorObserver,
) (func(context.Context) error, error) {
	return a.register("error", func() { a.errors = append(a.errors, observer) })
}

func (a *testAgents) OnInboxInserted(
	_ context.Context, _ *scope.Scope, observer agent.InboxObserver,
) (func(context.Context) error, error) {
	return a.register("inbox-inserted", func() { a.inboxInserted = append(a.inboxInserted, observer) })
}

func (a *testAgents) OnInboxClaimed(
	_ context.Context, _ *scope.Scope, observer agent.InboxClaimedObserver,
) (func(context.Context) error, error) {
	return a.register("inbox-claimed", func() { a.inboxClaimed = append(a.inboxClaimed, observer) })
}

func (a *testAgents) OnInboxDiscarded(
	_ context.Context, _ *scope.Scope, observer agent.InboxObserver,
) (func(context.Context) error, error) {
	return a.register("inbox-discarded", func() { a.inboxDiscarded = append(a.inboxDiscarded, observer) })
}

func (a *testAgents) OnPreStep(
	_ context.Context, _ *scope.Scope, observer agent.PreStepObserver,
) (func(context.Context) error, error) {
	return a.register("pre-step", func() { a.preStep = append(a.preStep, observer) })
}

// emitInboxInserted 把一次插入放给每一个观察者。
func (a *testAgents) emitInboxInserted(owner agent.Agent, message llm.Message) {
	a.mutex.Lock()
	observers := append([]agent.InboxObserver(nil), a.inboxInserted...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(owner, message)
	}
}

// emitInboxClaimed 把一次认领放给每一个观察者。
func (a *testAgents) emitInboxClaimed(owner agent.Agent, message llm.Message, turn int) {
	a.mutex.Lock()
	observers := append([]agent.InboxClaimedObserver(nil), a.inboxClaimed...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(owner, message, turn)
	}
}

// emitInboxDiscarded 把一次丢弃放给每一个观察者。
func (a *testAgents) emitInboxDiscarded(owner agent.Agent, message llm.Message) {
	a.mutex.Lock()
	observers := append([]agent.InboxObserver(nil), a.inboxDiscarded...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(owner, message)
	}
}

// emitCreated 把一次创建放给每一个观察者。
func (a *testAgents) emitCreated(t *testing.T, made agent.Agent) {
	t.Helper()
	a.mutex.Lock()
	observers := append([]agent.CreatedObserver(nil), a.created...)
	a.mutex.Unlock()
	for _, observer := range observers {
		if err := observer(context.Background(), made); err != nil {
			t.Fatalf("创建观察者报错：%v", err)
		}
	}
}

// emitDisposed 把一次处置放给每一个观察者。
func (a *testAgents) emitDisposed(gone agent.Agent) {
	a.mutex.Lock()
	observers := append([]agent.DisposedObserver(nil), a.disposed...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(gone)
	}
}

// emitStatus 把一次状态跃迁放给每一个观察者。
func (a *testAgents) emitStatus(owner agent.Agent, status agent.Status) {
	a.mutex.Lock()
	observers := append([]agent.StatusObserver(nil), a.status...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(owner, status)
	}
}

// emitSessionStart 把一次会话起跑放给每一个观察者。
func (a *testAgents) emitSessionStart(owner agent.Agent, source agent.SessionStartSource) {
	a.mutex.Lock()
	observers := append([]agent.SessionStartObserver(nil), a.sessionStart...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(owner, source)
	}
}

// emitError 把一次回合失败放给每一个观察者。
func (a *testAgents) emitError(failure agent.TurnError) {
	a.mutex.Lock()
	observers := append([]agent.ErrorObserver(nil), a.errors...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(failure)
	}
}

// emitPreStep 把一个提议中的步骤放给唯一那个观察者。
func (a *testAgents) emitPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	a.mutex.Lock()
	observers := append([]agent.PreStepObserver(nil), a.preStep...)
	a.mutex.Unlock()
	if len(observers) == 0 {
		return next(ctx)
	}
	return observers[0](ctx, step, next)
}

// unsubscribed 数退订函数一共被叫过几次。
func (a *testAgents) unsubscribed() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.stopped
}

// ---- 假会话面 ----

// testSessions 是那道可摆布的落盘屏障加日志广播。
type testSessions struct {
	mutex sync.Mutex
	// flushes 数 Flush 被叫过几次。
	flushes int
	// flushErr 非 nil 时每一次 Flush 都报它。
	flushErr error
	// beforeFlush 在每次 Flush 真正返回之前跑一下：用来模拟「落盘这段时间里
	// 又发生了别的事」。
	beforeFlush func()
	// observers 是登记进来的那些日志观察者。
	observers []coresession.EventObserver
	// failOnEvent 为真时 OnEvent 直接报错。
	failOnEvent bool
	// stopped 数退订函数被叫过几次。
	stopped int
}

func (s *testSessions) Flush(_ context.Context, _ *coresession.Session) (bool, error) {
	s.mutex.Lock()
	s.flushes++
	failure := s.flushErr
	hook := s.beforeFlush
	s.mutex.Unlock()
	if hook != nil {
		hook()
	}
	if failure != nil {
		return false, failure
	}
	return true, nil
}

func (s *testSessions) OnEvent(
	_ context.Context, _ *scope.Scope, observer coresession.EventObserver,
) (func(context.Context) error, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.failOnEvent {
		return nil, errTestRegister
	}
	s.observers = append(s.observers, observer)
	return func(context.Context) error {
		s.mutex.Lock()
		s.stopped++
		s.mutex.Unlock()
		return nil
	}, nil
}

// emitEvent 把一条日志事件放给每一个观察者。
func (s *testSessions) emitEvent(live *coresession.Session, event session.Event) {
	s.mutex.Lock()
	observers := append([]coresession.EventObserver(nil), s.observers...)
	s.mutex.Unlock()
	for _, observer := range observers {
		observer(live, event)
	}
}

// flushed 数 Flush 被叫过几次。
func (s *testSessions) flushed() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.flushes
}

// ---- 会报错的目标服务 ----

// failingGoals 在一台**真的**目标服务外面套一层，让指定的那几次改动报错。
//
// 本包每一条改动都跟着一条「它失败了怎么办」的路——打回未活化、记一条告警、
// 或者接着往下收尾。那几条路只有在服务真的报错时才走得到，而真服务在台架这套
// 布景里不会失败（agent 活着、目标也在）。套一层比手搓一台假服务省：读的那一面
// 仍旧是真规则，只有被点名的那次改动是假的。
type failingGoals struct {
	Goals
	disarmErr error
	pauseErr  error
	blockErr  error
}

func (g failingGoals) Disarm(owner agent.Agent) (*goal.View, error) {
	if g.disarmErr != nil {
		return nil, g.disarmErr
	}
	return g.Goals.Disarm(owner)
}

func (g failingGoals) Pause(owner agent.Agent, ref goal.Ref) (*goal.View, error) {
	if g.pauseErr != nil {
		return nil, g.pauseErr
	}
	return g.Goals.Pause(owner, ref)
}

func (g failingGoals) Block(
	owner agent.Agent, ref goal.Ref, reason goal.BlockReason,
) (*goal.View, error) {
	if g.blockErr != nil {
		return nil, g.blockErr
	}
	return g.Goals.Block(owner, ref, reason)
}

// ---- 台架 ----

// harness 是一次用例的全部布景：一张注册表、一个 agent、一台**真的**目标服务、
// 一道会话面。
type harness struct {
	t        *testing.T
	scope    *scope.Scope
	agents   *testAgents
	goals    *goal.Service
	sessions *testSessions
	owner    *testAgent
}

// newHarness 搭一套只装着一个 agent 的布景。
func newHarness(t *testing.T) *harness {
	t.Helper()
	owner := scopeOf(t, "goalrounddriver-test")
	agents := newTestAgents()
	live := newTestAgent(t, "session-1", owner, agents)
	service, err := goal.New(goal.Config{Agents: agents, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造目标服务失败：%v", err)
	}
	return &harness{
		t:        t,
		scope:    owner,
		agents:   agents,
		goals:    service,
		sessions: &testSessions{},
		owner:    live,
	}
}

// config 是把这套布景交给 [Install] 的那份配置。
func (h *harness) config() Config {
	return Config{
		Agents:   h.agents,
		Goals:    h.goals,
		Sessions: h.sessions,
		Logger:   quietLogger(),
	}
}

// deps 是把这套布景交给一台裸驱动的那份协作者。
func (h *harness) deps() driverDeps {
	return driverDeps{
		agents:   h.agents,
		goals:    h.goals,
		sessions: h.sessions,
		logger:   quietLogger(),
	}
}

// driver 造一台还没开动的裸驱动，绑在这套布景那个 agent 上。
func (h *harness) driver() *driver {
	return newDriver(context.Background(), h.owner, h.deps())
}

// driverWithGoals 造一台裸驱动，但把目标服务换成指定的那一台。
func (h *harness) driverWithGoals(goals Goals) *driver {
	deps := h.deps()
	deps.goals = goals
	return newDriver(context.Background(), h.owner, deps)
}

// install 把整套装上去，收摊登记进 t.Cleanup。
func (h *harness) install() func(context.Context) error {
	h.t.Helper()
	stop, err := Install(context.Background(), h.scope, h.config())
	if err != nil {
		h.t.Fatalf("装续推驱动失败：%v", err)
	}
	h.t.Cleanup(func() { _ = stop(context.Background()) })
	return stop
}

// createGoal 建一个带指定轮数上限的目标；建出来就是 active + armed。
func (h *harness) createGoal(objective string, maxRounds int) *goal.View {
	h.t.Helper()
	view, err := h.goals.Create(h.owner, goal.CreateRequest{
		Objective:     objective,
		MaxGoalRounds: &maxRounds,
	})
	if err != nil {
		h.t.Fatalf("建目标失败：%v", err)
	}
	return view
}

// currentGoal 读此刻这个 agent 的目标视图。
func (h *harness) currentGoal() *goal.View {
	h.t.Helper()
	view, err := h.goals.Get(h.owner)
	if err != nil {
		h.t.Fatalf("读目标失败：%v", err)
	}
	return view
}

// ---- 造消息与事件 ----

// roundMessage 造一条本包会认的续推消息。
func roundMessage(t *testing.T, view *goal.View, round int) llm.Message {
	t.Helper()
	source, err := goal.Source{GoalID: view.ID, Revision: view.Revision, Round: round}.MessageSource()
	if err != nil {
		t.Fatalf("包目标来源失败：%v", err)
	}
	return llm.NewUserMessage(RenderRoundPrompt(view, round), source)
}

// plainMessage 造一条没有目标来源的普通用户消息。
func plainMessage(text string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, nil)
}

// userMessageEvent 造一条把这条消息记进日志的 user/message 事件。
func userMessageEvent(t *testing.T, message llm.Message) session.Event {
	t.Helper()
	encoded, err := json.Marshal(session.UserMessageData{Message: message})
	if err != nil {
		t.Fatalf("排用户消息事件失败：%v", err)
	}
	return session.Event{Type: session.EventUserMessage, Data: encoded, SurfaceOp: session.AppendOp{}}
}

// turnEndEvent 造一条带这个理由的 turn/end 事件。
func turnEndEvent(t *testing.T, reason session.TurnEndReason) session.Event {
	t.Helper()
	encoded, err := json.Marshal(session.TurnEndData{Turn: 1, Reason: reason})
	if err != nil {
		t.Fatalf("排回合结束事件失败：%v", err)
	}
	return session.Event{Type: session.EventTurnEnd, Data: encoded}
}

func (a *testAgent) Remove(llm.MessageID) {}

func (a *testAgent) Replace(llm.MessageID, llm.Message) {}
