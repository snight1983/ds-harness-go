// 本文件的作用：把这台进程内注册表钉在它那几条真会出错的边上——围墙、准入、
// 先到先得的结算、等待的三种出口，以及属主释放和服务释放两条拆除路。
//
// # 这些测试防的是什么错
//
//   - **围墙漏了**。id 是可预测的（`bash-1`），边界完全建在属主授权上。这一条塌了，
//     一个 agent 就能读、能停另一个会话的活儿。
//   - **预检不彻底就把活儿起了**。准入必须在生产方跑起来**之前**拒掉，否则拒绝
//     会留下一份没人认领、也没人收得走的执行资源。
//   - **生产方不守规矩就把注册表拖住**。done 被关掉却不给值、或者给一个非终态的
//     结局，两种都会让等待者和拆除永远等下去。
//   - **结算不是先到先得**。拆除已经强判失败了，一个姗姗来迟的结局还能把它盖掉，
//     那条「已经报出去的失败」就成了假话。
//   - **通知投给了链外**。一个属主每装一份预设就多读一条完成通知，而完成通知会
//     开模型回合——那是直接烧钱。
//   - **拆除卡死**。一个返回了却不结算的生产方能把整条释放链停住。
//   - **监听器抛出来的东西掀翻了已经发生的提交**。
package localjobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [github.com/snight1983/ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包只读它的 ID 和 Scope，别的方法全是哑的。
type stubAgent struct {
	id  session.SessionID
	own *scope.Scope
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubAgents 是那份只回答「这个 id 底下当下登记着谁」的假登记簿。
type stubAgents struct {
	live map[session.SessionID]agent.Agent
}

func (r *stubAgents) Get(id session.SessionID) (agent.Agent, bool) {
	live, ok := r.live[id]
	return live, ok
}

// fakeProducer 是一个能被逐步驱动的假生产方：结局什么时候送、取消怎么反应、
// 流式输出下一段是什么，全由测试说了算。
type fakeProducer struct {
	// done 是那条结局 channel，缓冲 1 所以送结局不会挡住送的那一方。
	done chan jobs.Outcome
	// startErr 不为 nil 时起活儿就失败。
	startErr error
	// omitCancel / omitDone 造一份缺手的钩子。
	omitCancel bool
	omitDone   bool

	mutex sync.Mutex
	// stream 非 nil 时这是一件流式作业，每读一次交出一段。
	stream []string
	// streaming 决定钩子里带不带 ReadOutput。
	streaming bool
	// cancels 按顺序记下每一次取消收到的理由。
	cancels []string
	// cancelErr 不为 nil 时取消原样抛出去。
	cancelErr error
	// cancelHook 在记账之后跑，让测试能在取消里当场把作业结算掉。
	cancelHook func(reason string)
}

func newProducer() *fakeProducer {
	return &fakeProducer{done: make(chan jobs.Outcome, 1)}
}

// spec 造一份指向这个生产方的开工声明。
func (p *fakeProducer) spec(kind jobs.JobKind, label string, owner agent.Agent) jobs.Start {
	return jobs.Start{Kind: kind, Label: label, Owner: owner, Run: p.run}
}

func (p *fakeProducer) run() (jobs.Hooks, error) {
	if p.startErr != nil {
		return jobs.Hooks{}, p.startErr
	}
	hooks := jobs.Hooks{Cancel: p.cancel, Done: p.done}
	if p.omitCancel {
		hooks.Cancel = nil
	}
	if p.omitDone {
		hooks.Done = nil
	}
	if p.streaming {
		hooks.ReadOutput = p.readOutput
	}
	return hooks, nil
}

func (p *fakeProducer) cancel(reason string) error {
	p.mutex.Lock()
	p.cancels = append(p.cancels, reason)
	hook, err := p.cancelHook, p.cancelErr
	p.mutex.Unlock()
	if hook != nil {
		hook(reason)
	}
	return err
}

func (p *fakeProducer) readOutput() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if len(p.stream) == 0 {
		return ""
	}
	next := p.stream[0]
	p.stream = p.stream[1:]
	return next
}

// cancelReasons 给出到目前为止收到的那些取消理由。
func (p *fakeProducer) cancelReasons() []string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]string(nil), p.cancels...)
}

// ---- 脚手架 ----

// testWait 是每一处「等一件本该马上发生的事」的上限，到了就是这台注册表卡住了。
const testWait = 5 * time.Second

// quiet 造一台什么都不往外写的日志器：本包好几条路径故意去触发警告。
func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// harness 是一台装好了的注册表，外加两条能观察到通知的 channel。
type harness struct {
	t        *testing.T
	registry *Registry
	// root 是那个挂着控制器和两个监听器的无身份作用域。
	root *scope.Scope
	// settled 每来一次完成通知就推一份快照。
	settled chan jobs.Snapshot
	// changes 每来一次「可见集合变了」就推一次那个属主。
	changes chan agent.Agent
}

// newHarness 造一台注册表，并在全局层挂上一个控制器和两个观察者。
//
// 挂控制器是**必须**的：没有任何控制器服务的属主一件活儿都开不了。
func newHarness(t *testing.T, config Config) *harness {
	t.Helper()
	h := newBareHarness(t, config)
	h.attach(scope.NewRoot())
	return h
}

// newBareHarness 造一台**一个控制器都没挂**的注册表，专给准入那几条用。
func newBareHarness(t *testing.T, config Config) *harness {
	t.Helper()
	if config.Logger == nil {
		config.Logger = quiet()
	}
	registry, err := New(config)
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	h := &harness{
		t:        t,
		registry: registry,
		root:     scope.NewRoot(),
		settled:  make(chan jobs.Snapshot, 64),
		changes:  make(chan agent.Agent, 256),
	}
	t.Cleanup(func() { _ = h.root.Dispose(context.Background()) })
	// 两个观察者挂在全局层：它们要看得见每一个属主。
	if _, err := registry.OnJobDone(t.Context(), h.root, func(snapshot jobs.Snapshot, _ agent.Agent) {
		select {
		case h.settled <- snapshot:
		default:
		}
	}); err != nil {
		t.Fatalf("挂完成监听器失败：%v", err)
	}
	if _, err := registry.OnJobsChanged(t.Context(), h.root, func(owner agent.Agent) {
		select {
		case h.changes <- owner:
		default:
		}
	}); err != nil {
		t.Fatalf("挂变化观察者失败：%v", err)
	}
	return h
}

// attach 在某个作用域上挂一个作业控制器。
func (h *harness) attach(owner *scope.Scope) func(context.Context) error {
	h.t.Helper()
	detach, err := h.registry.AttachController(h.t.Context(), owner, "test-controller")
	if err != nil {
		h.t.Fatalf("挂控制器失败：%v", err)
	}
	return detach
}

// start 开一件活儿，失败就当场终止这个用例。
func (h *harness) start(spec jobs.Start) jobs.JobID {
	h.t.Helper()
	id, err := h.registry.Start(spec)
	if err != nil {
		h.t.Fatalf("开工失败：%v", err)
	}
	return id
}

// finish 送出一个结局，并等到那条收集协程把它记进去。
//
// 同步点是那条完成通知：它在记录提交、变化announce完之后才跑，所以收到它就等于
// 这次结算已经全都看得见了。
func (h *harness) finish(producer *fakeProducer, outcome jobs.Outcome) jobs.Snapshot {
	h.t.Helper()
	producer.done <- outcome
	select {
	case snapshot := <-h.settled:
		return snapshot
	case <-time.After(testWait):
		h.t.Fatal("等结算通知超时")
		return jobs.Snapshot{}
	}
}

// get 读一份快照，失败就当场终止这个用例。
func (h *harness) get(id jobs.JobID, caller agent.Agent) jobs.Snapshot {
	h.t.Helper()
	snapshot, err := h.registry.Get(id, caller)
	if err != nil {
		h.t.Fatalf("读快照失败：%v", err)
	}
	return snapshot
}

// newOwner 造一个带作用域、并且登记在册的属主。
func newOwner(t *testing.T, id session.SessionID, agents *stubAgents) *stubAgent {
	t.Helper()
	own, err := scope.New(scope.NewKey(string(id)), scope.Options{})
	if err != nil {
		t.Fatalf("造属主作用域失败：%v", err)
	}
	owner := &stubAgent{id: id, own: own}
	agents.live[id] = owner
	return owner
}

// newAgents 造一份空的登记簿。
func newAgents() *stubAgents {
	return &stubAgents{live: map[session.SessionID]agent.Agent{}}
}

// ids 把一批快照拍平成 id，好写断言。
func ids(snapshots []jobs.Snapshot) []jobs.JobID {
	out := make([]jobs.JobID, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, snapshot.ID)
	}
	return out
}

// ---- 装配 ----

func TestNewRefusesANegativeConcurrencyLimit(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{MaxConcurrentJobsPerOwner: -1}); err == nil {
		t.Fatal("负的并发上限该被拒绝")
	}
}

func TestNewFillsInItsDefaults(t *testing.T) {
	t.Parallel()
	// 全套默认值：默认时钟、默认日志器、默认上限 10。日志器那一条只要不炸就够了
	// ——它的默认值是 slog.Default()，一个 nil 会在第一次警告时当场空指针。
	h := newHarness(t, Config{})
	producers := make([]*fakeProducer, 0, defaultMaxConcurrentJobsPerOwner)
	for range defaultMaxConcurrentJobsPerOwner {
		producer := newProducer()
		producers = append(producers, producer)
		h.start(producer.spec(jobs.KindBash, "ls", nil))
	}
	if _, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", nil)); err == nil {
		t.Fatal("第 11 件该被默认上限挡住")
	}
	// 默认时钟盖过表：开工时刻不是零值。
	if h.get("bash-1", nil).StartedAt.IsZero() {
		t.Fatal("默认时钟该盖上开工时刻")
	}
	// 收干净，免得那些协程挂到用例结束。
	for _, producer := range producers {
		h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})
	}
}

func TestTheRegistrySatisfiesTheSeam(t *testing.T) {
	t.Parallel()
	// 编译期就该成立，这里只是把它写成一句会被读到的断言。
	var _ jobs.Registry = (*Registry)(nil)
}

// ---- 准入 ----

func TestStartRefusesAnOwnerNoControllerServes(t *testing.T) {
	t.Parallel()
	// 一个控制器都没挂：活儿开不了，也不该有任何执行资源被起起来。
	h := newBareHarness(t, Config{})
	producer := newProducer()
	_, err := h.registry.Start(producer.spec(jobs.KindBash, "ls", nil))
	if err == nil || !strings.Contains(err.Error(), "no job controller serves this agent") {
		t.Fatalf("报的是 %v，该说没有控制器服务这个属主", err)
	}
	if len(producer.cancelReasons()) != 0 || len(h.registry.List(nil)) != 0 {
		t.Fatal("被拒的开工不该留下任何痕迹")
	}
}

func TestAScopedControllerServesOnlyTheAgentsComposedUnderIt(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newBareHarness(t, Config{Agents: agents})

	inside := newOwner(t, "session-inside", agents)
	h.attach(inside.own)

	// 挂在这个 agent 自己作用域上的控制器服务得了它。
	h.start(newProducer().spec(jobs.KindBash, "ls", inside))

	// 无主作业落在全局链上，而全局层是空的：没人服务它。
	if _, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", nil)); err == nil {
		t.Fatal("无主作业不该被一个圈了作用域的控制器服务")
	}

	// 另一个 agent 不在那条链上，同样没人服务。
	outside := newOwner(t, "session-outside", agents)
	if _, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", outside)); err == nil {
		t.Fatal("链外的 agent 不该被服务")
	}
}

func TestStartValidatesTheSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    jobs.Start
		message string
	}{
		{
			name:    "种类是空的",
			spec:    jobs.Start{Label: "ls", Run: newProducer().run},
			message: "invalid job kind",
		},
		{
			name:    "标签是空的",
			spec:    jobs.Start{Kind: jobs.KindBash, Run: newProducer().run},
			message: "invalid job label",
		},
		{
			name:    "输出上限是负数",
			spec:    jobs.Start{Kind: jobs.KindBash, Label: "ls", OutputLimitBytes: -1, Run: newProducer().run},
			message: "invalid outputLimitBytes",
		},
		{
			name:    "没给起活儿的手",
			spec:    jobs.Start{Kind: jobs.KindBash, Label: "ls"},
			message: "缺了 Run",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, Config{})
			_, err := h.registry.Start(testCase.spec)
			if err == nil || !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("报的是 %v，该提到 %q", err, testCase.message)
			}
			if len(h.registry.List(nil)) != 0 {
				t.Fatal("被拒的开工不该留下记录")
			}
		})
	}
}

func TestStartSurfacesAProducerThatCannotStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	refused := errors.New("起不来")
	producer := newProducer()
	producer.startErr = refused
	if _, err := h.registry.Start(producer.spec(jobs.KindBash, "ls", nil)); !errors.Is(err, refused) {
		t.Fatalf("报的是 %v，该原样带上生产方那条错", err)
	}
	if len(h.registry.List(nil)) != 0 {
		t.Fatal("起不来的活儿不该被登记")
	}
}

func TestStartRejectsHooksMissingCancelOrDone(t *testing.T) {
	t.Parallel()
	// 两种都不是能拖到运行时再发现的错：没有 Cancel 就停不掉，没有 Done 就永远
	// 结算不了，拆除会当场卡死。
	for _, testCase := range []struct {
		name    string
		prepare func(*fakeProducer)
	}{
		{name: "缺 Cancel", prepare: func(p *fakeProducer) { p.omitCancel = true }},
		{name: "缺 Done", prepare: func(p *fakeProducer) { p.omitDone = true }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, Config{})
			producer := newProducer()
			testCase.prepare(producer)
			if _, err := h.registry.Start(producer.spec(jobs.KindBash, "ls", nil)); err == nil {
				t.Fatal("缺手的钩子该被拒绝")
			}
			if len(h.registry.List(nil)) != 0 {
				t.Fatal("被拒的开工不该留下记录")
			}
		})
	}
}

func TestStartNumbersJobsPerKindAndAnnouncesTheChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	first := h.start(newProducer().spec(jobs.KindBash, "ls", nil))
	second := h.start(newProducer().spec(jobs.KindBash, "pwd", nil))
	other := h.start(newProducer().spec(jobs.KindSubagent, "查一下", nil))
	if first != "bash-1" || second != "bash-2" || other != "subagent-1" {
		t.Fatalf("发的号是 %q/%q/%q", first, second, other)
	}
	// 登记顺序就是 List 的顺序。
	if got := ids(h.registry.List(nil)); len(got) != 3 || got[0] != "bash-1" || got[2] != "subagent-1" {
		t.Fatalf("列出来的是 %v", got)
	}
	// 每一次登记都让可见集合真的变了。
	for range 3 {
		select {
		case <-h.changes:
		case <-time.After(testWait):
			t.Fatal("登记该announce一次变化")
		}
	}
}

func TestStartEnforcesTheConcurrencyLimitPerOwner(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{MaxConcurrentJobsPerOwner: 2, Agents: agents})
	owner := newOwner(t, "session-1", agents)

	first := newProducer()
	h.start(first.spec(jobs.KindBash, "one", owner))
	h.start(newProducer().spec(jobs.KindBash, "two", owner))

	_, err := h.registry.Start(newProducer().spec(jobs.KindBash, "three", owner))
	if err == nil || !strings.Contains(err.Error(), "background job limit reached") {
		t.Fatalf("报的是 %v，该说到上限了", err)
	}

	// 上限按属主算：无主那个桶是另一份额度。
	h.start(newProducer().spec(jobs.KindBash, "unowned", nil))

	// 落定的那件不再占额度。
	h.finish(first, jobs.Outcome{Status: jobs.StatusCompleted})
	h.start(newProducer().spec(jobs.KindBash, "three", owner))
}

func TestOwnedJobsRequireTheAgentRegistry(t *testing.T) {
	t.Parallel()
	// 属主清理必须挂在那个确切的活实例上，没有登记簿就核不了，只能拒。
	h := newHarness(t, Config{})
	own, err := scope.New(scope.NewKey("session-1"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	_, err = h.registry.Start(newProducer().spec(jobs.KindBash, "ls", &stubAgent{id: "session-1", own: own}))
	if err == nil || !strings.Contains(err.Error(), "requires the agent registry") {
		t.Fatalf("报的是 %v，该说缺 agent 登记簿", err)
	}
}

func TestOwnedJobsMustBeTheLiveRegisteredInstance(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})

	// 压根没登记过。
	own, err := scope.New(scope.NewKey("ghost"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	_, err = h.registry.Start(newProducer().spec(jobs.KindBash, "ls", &stubAgent{id: "ghost", own: own}))
	if err == nil || !strings.Contains(err.Error(), "not the registered agent instance") {
		t.Fatalf("报的是 %v，该说这不是登记着的那个实例", err)
	}

	// 同一个 id，另一个实例——这正是「属主已经被换掉了」的样子。
	live := newOwner(t, "session-1", agents)
	stale := &stubAgent{id: live.id, own: own}
	_, err = h.registry.Start(newProducer().spec(jobs.KindBash, "ls", stale))
	if err == nil || !strings.Contains(err.Error(), "not the registered agent instance") {
		t.Fatalf("报的是 %v，该拒掉那个过期实例", err)
	}
}

func TestOwnedJobsNeedAnOwnerScopeToHangCleanupOn(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	naked := &stubAgent{id: "session-1"}
	agents.live[naked.id] = naked
	_, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", naked))
	if err == nil || !strings.Contains(err.Error(), "no scope") {
		t.Fatalf("报的是 %v，该说没有作用域可挂清理", err)
	}
}

func TestStartRefusesAnOwnerWhoseScopeIsAlreadyDisposed(t *testing.T) {
	t.Parallel()
	// 一个正在释放的作用域会拒绝新的清理，而没有清理就等于这件作业无人收尾。
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)
	if err := owner.own.Dispose(t.Context()); err != nil {
		t.Fatalf("释放属主作用域失败：%v", err)
	}
	if _, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", owner)); err == nil {
		t.Fatal("挂不上清理就该拒绝开工")
	}
	if len(h.registry.List(owner)) != 0 {
		t.Fatal("被拒的开工不该留下记录")
	}
}

// ---- 生产方契约 ----

func TestAProducerThatClosesDoneWithoutAnOutcomeIsFailed(t *testing.T) {
	t.Parallel()
	// 关掉却不给值是 Go 这边「done 被 reject 了」的对应物：兜成 failed，否则
	// 等待者和拆除都会永远挂着。
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))
	close(producer.done)

	select {
	case snapshot := <-h.settled:
		if snapshot.ID != id || snapshot.Status != jobs.StatusFailed {
			t.Fatalf("结算成了 %q/%q", snapshot.ID, snapshot.Status)
		}
		if !strings.Contains(snapshot.Detail, "producer contract violation") {
			t.Fatalf("细节写的是 %q", snapshot.Detail)
		}
	case <-time.After(testWait):
		t.Fatal("等结算超时")
	}
}

func TestANonTerminalOutcomeIsForcedToFailed(t *testing.T) {
	t.Parallel()
	// 放一个 running 的结局进去，这件作业就永远结算不了。
	h := newHarness(t, Config{})
	producer := newProducer()
	h.start(producer.spec(jobs.KindBash, "ls", nil))
	snapshot := h.finish(producer, jobs.Outcome{Status: jobs.StatusRunning})
	if snapshot.Status != jobs.StatusFailed {
		t.Fatalf("结算成了 %q，该被兜成 failed", snapshot.Status)
	}
	if !strings.Contains(snapshot.Detail, "non-terminal status") {
		t.Fatalf("细节写的是 %q", snapshot.Detail)
	}
}

// ---- 围墙 ----

func TestListShowsUnownedWorkAndOnlyTheCallersOwn(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	mine := newOwner(t, "session-mine", agents)
	yours := newOwner(t, "session-yours", agents)

	h.start(newProducer().spec(jobs.KindBash, "unowned", nil))
	h.start(newProducer().spec(jobs.KindBash, "mine", mine))
	h.start(newProducer().spec(jobs.KindBash, "yours", yours))

	if got := ids(h.registry.List(mine)); len(got) != 2 || got[0] != "bash-1" || got[1] != "bash-2" {
		t.Fatalf("我看到的是 %v，该只有无主的和我自己的", got)
	}
	if got := ids(h.registry.List(nil)); len(got) != 1 || got[0] != "bash-1" {
		t.Fatalf("没有身份的调用方看到的是 %v，该只有无主的", got)
	}
}

func TestGetRefusesUnknownIDsAndOtherSessions(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	mine := newOwner(t, "session-mine", agents)
	yours := newOwner(t, "session-yours", agents)
	id := h.start(newProducer().spec(jobs.KindBash, "mine", mine))

	if _, err := h.registry.Get("bash-99", mine); err == nil || !strings.Contains(err.Error(), "unknown job") {
		t.Fatalf("报的是 %v，该说不认得", err)
	}
	if _, err := h.registry.Get(id, yours); err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("报的是 %v，该说这是别人的", err)
	}
	// 没有身份的调用方永远够不着一件有主作业。
	if _, err := h.registry.Get(id, nil); err == nil {
		t.Fatal("无身份的调用方不该够得着有主作业")
	}
	if h.get(id, mine).Label != "mine" {
		t.Fatal("属主自己该读得到")
	}
}

// ---- 读 ----

func TestReadDrainsAStreamAndRepeatsAFinalOutput(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})

	// 流式那一类：游标在生产方手里，读一次就取走上次之后的新东西。
	streaming := newProducer()
	streaming.streaming = true
	streaming.stream = []string{"第一段", "第二段"}
	streamID := h.start(streaming.spec(jobs.KindBash, "tail -f", nil))

	read, err := h.registry.Read(streamID, nil)
	if err != nil || read.Text != "第一段" {
		t.Fatalf("第一次读到 %q（%v）", read.Text, err)
	}
	// 活着的时候读不该把它标成已汇报。
	if read.Snapshot.Reported {
		t.Fatal("活着的时候读不该认领汇报")
	}
	if read, _ = h.registry.Read(streamID, nil); read.Text != "第二段" {
		t.Fatalf("第二次读到 %q", read.Text)
	}

	// 只有最终输出那一类：活着时是空串，落定之后每次都交回同一份。
	batch := newProducer()
	batchID := h.start(batch.spec(jobs.KindBash, "ls", nil))
	if read, _ = h.registry.Read(batchID, nil); read.Text != "" {
		t.Fatalf("还没落定就读出了 %q", read.Text)
	}
	h.finish(batch, jobs.Outcome{Status: jobs.StatusCompleted, Output: "全部输出"})
	for range 2 {
		read, err = h.registry.Read(batchID, nil)
		if err != nil || read.Text != "全部输出" {
			t.Fatalf("终态读到 %q（%v）", read.Text, err)
		}
		// 一次终态读认领那次汇报。
		if !read.Snapshot.Reported {
			t.Fatal("终态读该把这件作业标成已汇报")
		}
	}

	if _, err := h.registry.Read("bash-99", nil); err == nil {
		t.Fatal("不认得的 id 该报错")
	}
}

// ---- 停 ----

func TestKillCancelsAndMarksStoppingAndReported(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "sleep 100", nil))

	result, err := h.registry.Kill(id, nil, "用户不要了")
	if err != nil || result != jobs.KillRequested {
		t.Fatalf("停的结果是 %q（%v）", result, err)
	}
	if reasons := producer.cancelReasons(); len(reasons) != 1 || reasons[0] != "用户不要了" {
		t.Fatalf("生产方收到的理由是 %v", reasons)
	}
	snapshot := h.get(id, nil)
	if snapshot.Status != jobs.StatusStopping || !snapshot.Reported {
		t.Fatalf("停完是 %q/reported=%v", snapshot.Status, snapshot.Reported)
	}

	// 收干净。
	h.finish(producer, jobs.Outcome{Status: jobs.StatusKilled})
}

func TestKillOnASettledJobIsAnAcceptedNoop(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})

	result, err := h.registry.Kill(id, nil, "")
	if err != nil || result != jobs.KillAlreadyFinished {
		t.Fatalf("停一件落定的作业得到 %q（%v）", result, err)
	}
	if len(producer.cancelReasons()) != 0 {
		t.Fatal("落定之后不该再去打扰生产方")
	}
	// 空操作也认领那次汇报：调用方已经知道它完了。
	if !h.get(id, nil).Reported {
		t.Fatal("该被标成已汇报")
	}
}

func TestKillPropagatesAProducerErrorAndChangesNothing(t *testing.T) {
	t.Parallel()
	// 取消出错时生命周期和通知状态都保持原样，这是 Kill 的契约。
	h := newHarness(t, Config{})
	producer := newProducer()
	refused := errors.New("停不掉")
	producer.cancelErr = refused
	id := h.start(producer.spec(jobs.KindBash, "sleep 100", nil))

	if _, err := h.registry.Kill(id, nil, "试试"); !errors.Is(err, refused) {
		t.Fatalf("报的是 %v，该原样带上生产方那条错", err)
	}
	snapshot := h.get(id, nil)
	if snapshot.Status != jobs.StatusRunning || snapshot.Reported {
		t.Fatalf("状态被动过了：%q/reported=%v", snapshot.Status, snapshot.Reported)
	}
}

func TestKillDoesNotOverwriteAStatusSettledWhileCancelRan(t *testing.T) {
	t.Parallel()
	// 取消在锁外调，所以生产方可以在那一小段里就把作业结算掉。DSH 单线程下不存在
	// 这个窗口；这里必须重新确认，否则一个终态会被 stopping 盖掉。
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "sleep 100", nil))
	producer.cancelHook = func(string) {
		producer.done <- jobs.Outcome{Status: jobs.StatusKilled, Detail: "当场停了"}
		select {
		case <-h.settled:
		case <-time.After(testWait):
			t.Error("等取消里那次结算超时")
		}
	}

	if _, err := h.registry.Kill(id, nil, "停"); err != nil {
		t.Fatalf("停失败：%v", err)
	}
	if snapshot := h.get(id, nil); snapshot.Status != jobs.StatusKilled {
		t.Fatalf("终态被盖成了 %q", snapshot.Status)
	}
}

// ---- 等 ----

func TestWaitRejectsANonPositiveTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))
	if _, err := h.registry.Wait(t.Context(), id, 0, nil); err == nil ||
		!strings.Contains(err.Error(), "invalid wait timeout") {
		t.Fatalf("报的是 %v，该说超时不合法", err)
	}
	if _, err := h.registry.Wait(t.Context(), "bash-99", time.Minute, nil); err == nil {
		t.Fatal("不认得的 id 该报错")
	}
	if _, err := h.registry.Kill("bash-99", nil, ""); err == nil {
		t.Fatal("停一个不认得的 id 该报错")
	}
	// 收干净。
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})
}

func TestWaitOnASettledJobReturnsAtOnceAndClaimsTheReport(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})

	snapshot, err := h.registry.Wait(t.Context(), id, time.Minute, nil)
	if err != nil || snapshot.Status != jobs.StatusCompleted || !snapshot.Reported {
		t.Fatalf("等到的是 %q/reported=%v（%v）", snapshot.Status, snapshot.Reported, err)
	}
}

func TestASettlementWithAWaiterClaimsTheReportBeforeListenersRun(t *testing.T) {
	t.Parallel()
	// 结算那一刻只要还有人在等，这件作业就当场标成已汇报——紧接着跑的完成汇报方
	// 因此不会为一份马上要被取走的结果再发一次通知。
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))

	waited := make(chan jobs.Snapshot, 1)
	go func() {
		snapshot, err := h.registry.Wait(t.Context(), id, testWait, nil)
		if err != nil {
			t.Errorf("等失败：%v", err)
		}
		waited <- snapshot
	}()

	// 等到那个等待者真的挂上去：它挂上之前送结局就测不到这条。
	waitForWaiter(t, h.registry, id)
	producer.done <- jobs.Outcome{Status: jobs.StatusCompleted, Output: "好了"}

	select {
	case snapshot := <-h.settled:
		if !snapshot.Reported {
			t.Fatal("有人在等的结算，监听器看到的那份就该已经是已汇报")
		}
	case <-time.After(testWait):
		t.Fatal("等结算通知超时")
	}
	select {
	case snapshot := <-waited:
		if snapshot.Status != jobs.StatusCompleted {
			t.Fatalf("等到的是 %q", snapshot.Status)
		}
	case <-time.After(testWait):
		t.Fatal("等待者没被放开")
	}
}

// waitForWaiter 等到这件作业上确实挂着至少一个等待者。
//
// 直接读那个受锁保护的计数，不靠任何时间假设。
func waitForWaiter(t *testing.T, registry *Registry, id jobs.JobID) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for {
		registry.mutex.Lock()
		waiters := registry.store[id].waiters
		registry.mutex.Unlock()
		if waiters > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("等待者一直没挂上去")
		}
	}
}

func TestWaitReturnsTheLiveSnapshotWhenItTimesOut(t *testing.T) {
	t.Parallel()
	// 等到点了不是错：交回当下那份快照，而且这件作业没被取消。
	h := newHarness(t, Config{})
	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "sleep 100", nil))

	snapshot, err := h.registry.Wait(t.Context(), id, time.Millisecond, nil)
	if err != nil {
		t.Fatalf("等到点该交回快照而不是报错：%v", err)
	}
	if snapshot.Status != jobs.StatusRunning || snapshot.Reported {
		t.Fatalf("等到点后是 %q/reported=%v", snapshot.Status, snapshot.Reported)
	}
	if len(producer.cancelReasons()) != 0 {
		t.Fatal("等到点不该取消这件作业")
	}
}

func TestWaitAbortsWhenTheCallerCancels(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	id := h.start(newProducer().spec(jobs.KindBash, "sleep 100", nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.registry.Wait(ctx, id, time.Minute, nil); err == nil ||
		!strings.Contains(err.Error(), "wait aborted") {
		t.Fatalf("报的是 %v，该说等待被中止", err)
	}
	// 中止不改这件作业的任何状态。
	if snapshot := h.get(id, nil); snapshot.Status != jobs.StatusRunning || snapshot.Reported {
		t.Fatalf("中止之后是 %q/reported=%v", snapshot.Status, snapshot.Reported)
	}
}

// ---- 通知的可见范围 ----

func TestNoticesNeverReachOutsideTheOwnersChain(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newBareHarness(t, Config{Agents: agents})
	h.attach(scope.NewRoot())

	mine := newOwner(t, "session-mine", agents)
	stranger := newOwner(t, "session-stranger", agents)

	// 两个只看得见自己那条链的观察者。
	minesDone := make(chan jobs.Snapshot, 4)
	if _, err := h.registry.OnJobDone(t.Context(), mine.own, func(s jobs.Snapshot, _ agent.Agent) {
		minesDone <- s
	}); err != nil {
		t.Fatalf("挂监听器失败：%v", err)
	}
	strangersDone := make(chan jobs.Snapshot, 4)
	strangersChanged := make(chan agent.Agent, 8)
	if _, err := h.registry.OnJobDone(t.Context(), stranger.own, func(s jobs.Snapshot, _ agent.Agent) {
		strangersDone <- s
	}); err != nil {
		t.Fatalf("挂监听器失败：%v", err)
	}
	if _, err := h.registry.OnJobsChanged(t.Context(), stranger.own, func(o agent.Agent) {
		strangersChanged <- o
	}); err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}

	// 陌生人自己那条链上的变化照收不误——圈的是可见范围，不是把它整个关掉。
	minesChanged := make(chan agent.Agent, 8)
	if _, err := h.registry.OnJobsChanged(t.Context(), mine.own, func(o agent.Agent) {
		minesChanged <- o
	}); err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}

	producer := newProducer()
	h.start(producer.spec(jobs.KindBash, "ls", mine))
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})

	if len(minesDone) != 1 {
		t.Fatalf("属主自己那条链上收到了 %d 条完成，该是 1 条", len(minesDone))
	}
	if len(minesChanged) != 2 {
		t.Fatalf("属主自己那条链上收到了 %d 条变化，该是登记和结算那 2 条", len(minesChanged))
	}
	if len(strangersDone) != 0 || len(strangersChanged) != 0 {
		t.Fatalf("链外收到了 %d 条完成、%d 条变化，一条都不该有",
			len(strangersDone), len(strangersChanged))
	}
}

func TestAThrowingObserverCannotBreakACommitThatAlreadyHappened(t *testing.T) {
	t.Parallel()
	h := newBareHarness(t, Config{})
	h.attach(scope.NewRoot())
	panicking := scope.NewRoot()
	t.Cleanup(func() { _ = panicking.Dispose(context.Background()) })
	if _, err := h.registry.OnJobDone(t.Context(), panicking, func(jobs.Snapshot, agent.Agent) {
		panic("监听器炸了")
	}); err != nil {
		t.Fatalf("挂监听器失败：%v", err)
	}
	if _, err := h.registry.OnJobsChanged(t.Context(), panicking, func(agent.Agent) {
		panic("观察者炸了")
	}); err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}

	producer := newProducer()
	id := h.start(producer.spec(jobs.KindBash, "ls", nil))
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted, Output: "好了"})
	if snapshot := h.get(id, nil); snapshot.Status != jobs.StatusCompleted {
		t.Fatalf("提交被掀翻了：%q", snapshot.Status)
	}
}

func TestSubscribingWithoutAListenerIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{})
	if _, err := h.registry.OnJobDone(t.Context(), h.root, nil); err == nil {
		t.Fatal("没有监听器时该拒绝")
	}
	if _, err := h.registry.OnJobsChanged(t.Context(), h.root, nil); err == nil {
		t.Fatal("没有观察者时该拒绝")
	}
}

func TestDetachingTheLastControllerClosesTheRegistryAgain(t *testing.T) {
	t.Parallel()
	// 摘掉最后一件贡献之后那一层要被回收，靠的正是 jobLayer.IsEmpty。
	agents := newAgents()
	h := newBareHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)
	detach := h.attach(owner.own)

	producer := newProducer()
	h.start(producer.spec(jobs.KindBash, "ls", owner))
	if err := detach(t.Context()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	if _, err := h.registry.Start(newProducer().spec(jobs.KindBash, "ls", owner)); err == nil {
		t.Fatal("控制器都摘光了就不该再能开工")
	}
	// 收干净。
	h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})
}

// ---- 时刻 ----

func TestTimestampsComeFromTheInjectedClock(t *testing.T) {
	t.Parallel()
	moment := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	var ticks int
	h := newHarness(t, Config{Now: func() time.Time {
		ticks++
		return moment.Add(time.Duration(ticks) * time.Second)
	}})
	producer := newProducer()
	h.start(producer.spec(jobs.KindBash, "ls", nil))
	snapshot := h.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})
	if !snapshot.StartedAt.Equal(moment.Add(time.Second)) {
		t.Fatalf("开工时刻是 %s", snapshot.StartedAt)
	}
	if !snapshot.FinishedAt.Equal(moment.Add(2 * time.Second)) {
		t.Fatalf("完成时刻是 %s", snapshot.FinishedAt)
	}
}

// ---- 属主释放 ----

func TestDisposingTheOwnerCancelsAwaitsAndDropsItsJobs(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)

	owned := newProducer()
	// 取消一到就结算：这才是一个守规矩的生产方。
	owned.cancelHook = func(string) { owned.done <- jobs.Outcome{Status: jobs.StatusKilled} }
	ownedID := h.start(owned.spec(jobs.KindBash, "mine", owner))

	unowned := newProducer()
	unownedID := h.start(unowned.spec(jobs.KindBash, "unowned", nil))

	if err := owner.own.Dispose(t.Context()); err != nil {
		t.Fatalf("释放属主失败：%v", err)
	}
	if reasons := owned.cancelReasons(); len(reasons) != 1 || reasons[0] != "owner disposed" {
		t.Fatalf("取消理由是 %v", reasons)
	}
	if _, err := h.registry.Get(ownedID, owner); err == nil {
		t.Fatal("属主释放之后它的作业该被摘掉")
	}
	// 别人的活儿一点都不该被动。
	if h.get(unownedID, nil).Status != jobs.StatusRunning {
		t.Fatal("无主作业不该被属主释放牵连")
	}
	// 拆除取消认领那次终态汇报：没有人会去读它。
	select {
	case snapshot := <-h.settled:
		if !snapshot.Reported {
			t.Fatal("拆除取消该认领那次汇报")
		}
	case <-time.After(testWait):
		t.Fatal("等拆除结算超时")
	}

	// 收干净。
	h.finish(unowned, jobs.Outcome{Status: jobs.StatusCompleted})
}

func TestATeardownCancelThatThrowsForceFailsTheRecord(t *testing.T) {
	t.Parallel()
	// 一个抛出来的取消停不掉活儿，但那条记录必须落定，否则拆除永远等下去。
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)

	producer := newProducer()
	producer.cancelErr = errors.New("停不掉")
	id := h.start(producer.spec(jobs.KindBash, "mine", owner))

	if err := owner.own.Dispose(t.Context()); err != nil {
		t.Fatalf("释放属主失败：%v", err)
	}
	select {
	case snapshot := <-h.settled:
		if snapshot.ID != id || snapshot.Status != jobs.StatusFailed {
			t.Fatalf("结算成了 %q/%q", snapshot.ID, snapshot.Status)
		}
		if !strings.Contains(snapshot.Detail, "work may be orphaned") {
			t.Fatalf("细节写的是 %q", snapshot.Detail)
		}
	case <-time.After(testWait):
		t.Fatal("抛出来的取消该把记录强判失败")
	}
}

func TestALateOutcomeCannotOverwriteATeardownForceFailure(t *testing.T) {
	t.Parallel()
	// 先到先得：拆除已经报出去的那条失败，不许被一个姗姗来迟的结局改写。
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)

	producer := newProducer()
	producer.cancelErr = errors.New("停不掉")
	id := h.start(producer.spec(jobs.KindBash, "mine", owner))
	if err := owner.own.Dispose(t.Context()); err != nil {
		t.Fatalf("释放属主失败：%v", err)
	}
	<-h.settled

	// 记录已经被摘掉了，但那份 trackedJob 还活着，姗姗来迟的结局落在它身上。
	producer.done <- jobs.Outcome{Status: jobs.StatusCompleted, Output: "其实我跑完了"}
	if _, err := h.registry.Get(id, owner); err == nil {
		t.Fatal("被摘掉的作业不该又回来了")
	}
	select {
	case snapshot := <-h.settled:
		t.Fatalf("迟到的结局不该再announce一次：%q", snapshot.Status)
	case <-time.After(50 * time.Millisecond):
	}
}

// ---- 服务释放 ----

func TestServiceDisposeStopsEverythingAndSilencesCompletionListeners(t *testing.T) {
	t.Parallel()
	agents := newAgents()
	h := newHarness(t, Config{Agents: agents})
	owner := newOwner(t, "session-1", agents)

	owned := newProducer()
	owned.cancelHook = func(string) { owned.done <- jobs.Outcome{Status: jobs.StatusKilled} }
	h.start(owned.spec(jobs.KindBash, "mine", owner))

	unowned := newProducer()
	unowned.cancelHook = func(string) { unowned.done <- jobs.Outcome{Status: jobs.StatusKilled} }
	h.start(unowned.spec(jobs.KindBash, "unowned", nil))

	// 开工那两次变化先清掉，好让底下数的是拆除announce的那些。
	drain(h.changes)

	if err := h.registry.Dispose(t.Context()); err != nil {
		t.Fatalf("释放服务失败：%v", err)
	}
	if reasons := owned.cancelReasons(); len(reasons) != 1 || reasons[0] != "jobs service disposed" {
		t.Fatalf("取消理由是 %v", reasons)
	}
	if len(h.registry.List(owner)) != 0 || len(h.registry.List(nil)) != 0 {
		t.Fatal("服务释放之后表该是空的")
	}
	// 完成监听器从拆除那一刻起就闭嘴了：没有读者，也不该再开模型回合。
	select {
	case snapshot := <-h.settled:
		t.Fatalf("拆除之后不该再announce完成：%q", snapshot.ID)
	default:
	}
	// 但「被摘掉」这件事必须announce出去，否则观察者会把那几行一直留着。
	if len(h.changes) == 0 {
		t.Fatal("拆除该announce可见集合的变化")
	}
	// 属主那条清理已经被摘下来了：作用域再释放一次不会再回到这台注册表。
	if err := owner.own.Dispose(t.Context()); err != nil {
		t.Fatalf("再释放属主作用域失败：%v", err)
	}
}

func TestTeardownReportsTheJobsThatNeverStopped(t *testing.T) {
	t.Parallel()
	// DSH 那边是无条件地等下去，一个不守规矩的生产方能把拆除永远卡住。这里认 ctx，
	// 并且把「是谁没停」写进错误里。
	h := newHarness(t, Config{})

	// 一件早就落定的：拆除该整件跳过它，等的时候也不该把它算成没停。
	settledEarly := newProducer()
	settledID := h.start(settledEarly.spec(jobs.KindBash, "settled", nil))
	h.finish(settledEarly, jobs.Outcome{Status: jobs.StatusCompleted})

	stubborn := newProducer()
	stubbornID := h.start(stubborn.spec(jobs.KindBash, "stubborn", nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := h.registry.Dispose(ctx)
	if err == nil || !strings.Contains(err.Error(), string(stubbornID)) {
		t.Fatalf("报的是 %v，该点名那件没停的作业", err)
	}
	if strings.Contains(err.Error(), string(settledID)) {
		t.Fatalf("已经落定的那件不该被点名：%v", err)
	}
	if len(settledEarly.cancelReasons()) != 0 {
		t.Fatal("落定的作业不该在拆除时再被取消一次")
	}
}

// drain 把一条 channel 里已经攒下的东西清掉。
func drain[T any](channel <-chan T) {
	for {
		select {
		case <-channel:
		default:
			return
		}
	}
}

// ---- 不变量 ----

func TestRegisterInvariantsReservesThePackageNameAndChecksNothing(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有不变量注册表时该拒绝")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	// 装得上、拆得掉，中间一条检查都不抛——「结论是无需检查」和「这个包被漏掉了」
	// 因此区分得开。
	undo, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	undo()
}

func (a *stubAgent) Remove(llm.MessageID) {}

func (a *stubAgent) Replace(llm.MessageID, llm.Message) {}
