// 本文件的作用：把那份可丢弃的定时器投影钉在它真会出错的边上——一次性怎么插在
// 固定频率前面、同一刻到期的两条按什么出场、哪一种失败该永久熄火哪一种不该、
// 两道屏障各自漏掉会怎样、跟进消息和 dispatch 事件的先后，以及认领不到空闲期时
// 为什么不许上定时器重试。
//
// # 这些测试防的是什么错
//
//   - **一批固定频率把一条到期的一次性挤掉**。一次性各自是独立的一件事，攒成一批
//     送过去模型就分不清先后了。这条次序在 [decide] 里只是一个提前 return，很容易
//     在「顺手合并两个循环」时消失。
//   - **同一刻到期的两条按 map 顺序出场**。用户唯一能预期的顺序是创建先后；这里
//     靠的是一次**稳定**排序，换成 sort.Slice 就会随机。
//   - **折不动的日志被一直重试**。日志坏了不会自己好，每次重试只会把同一条告警刷
//     满，而且每一次都白折一遍。
//   - **一次算不出来的判断被当成日志坏了**。判断依赖此刻的墙上时钟，下一次可能就
//     好了；把它也熄火掉，等于让一次瞬时故障永久关掉这个会话的提醒。
//   - **先写 dispatch 事件再发跟进消息**。这个次序反过来之后，一次崩溃会留下「已经
//     投过了」的记录而模型根本没收到——那条提醒就永远丢了。重复投一次远比丢掉好。
//   - **认领不到空闲期时上定时器重试**。重试多少次都一样认领不到，正确的做法是等
//     它自己静下来。
//   - **这份投影替一段已经不归它管的会话说话**。根被换掉之后它必须闭嘴。
//
// 说明: 本文件里的 Runtime 用例一律**直接调 driveOnce**，不走 Start 那条协程。
// 驱动循环本身只是「收到信号就跑一次」，而每一条要验的规则都在 driveOnce 里；
// 让协程参与只会把确定的断言变成一场和调度器的赛跑。

package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 替身 ----

// stubAgents 是那张可摆布的 agent 注册表。
type stubAgents struct {
	mutex sync.Mutex
	// live 是此刻按标识查得到的那些 agent。
	live map[sessionlog.SessionID]agent.Agent
	// roots 是此刻的顶层 agent。
	roots []agent.Agent

	// created 和 status 是此刻挂着的那些观察者；[Install] 那一侧的用例靠 emit
	// 系列方法从这里把事件放出去。
	created []agent.CreatedObserver
	status  []agent.StatusObserver
	// createdErr / statusErr 非 nil 时，对应那次登记直接失败。
	createdErr error
	statusErr  error
	// stopCreatedErr / stopStatusErr 是退订函数交回的错。
	stopCreatedErr error
	stopStatusErr  error
	// stopped 数退订函数被叫过几次，用来验摘的次序。
	stopped []string
	// onStopStatus 非 nil 时在摘空闲钩子的那一刻被叫一次。用例拿它回看那一刻工具
	// 还在不在，验「先摘钩子再摘工具」这条次序。
	onStopStatus func()
}

// newEmptyStubAgents 造一张空的注册表：[Install] 那一侧的用例要从「一个都没有」
// 开始，再自己把 agent 放上台。
func newEmptyStubAgents() *stubAgents {
	return &stubAgents{live: map[sessionlog.SessionID]agent.Agent{}}
}

// newStubAgents 造一张只装着这一个根的注册表。
func newStubAgents(owner agent.Agent) *stubAgents {
	return &stubAgents{
		live:  map[sessionlog.SessionID]agent.Agent{owner.ID(): owner},
		roots: []agent.Agent{owner},
	}
}

func (a *stubAgents) Get(id sessionlog.SessionID) (agent.Agent, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	found, present := a.live[id]
	return found, present
}

func (a *stubAgents) Roots() []agent.Agent {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]agent.Agent(nil), a.roots...)
}

func (a *stubAgents) OnCreated(
	_ context.Context, _ *scope.Scope, observer agent.CreatedObserver,
) (func(context.Context) error, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.createdErr != nil {
		return nil, a.createdErr
	}
	a.created = append(a.created, observer)
	return func(context.Context) error {
		a.mutex.Lock()
		a.stopped = append(a.stopped, "created")
		failure := a.stopCreatedErr
		a.mutex.Unlock()
		return failure
	}, nil
}

func (a *stubAgents) OnStatus(
	_ context.Context, _ *scope.Scope, observer agent.StatusObserver,
) (func(context.Context) error, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.statusErr != nil {
		return nil, a.statusErr
	}
	a.status = append(a.status, observer)
	return func(context.Context) error {
		a.mutex.Lock()
		a.stopped = append(a.stopped, "status")
		failure := a.stopStatusErr
		probe := a.onStopStatus
		a.mutex.Unlock()
		// 探针在锁外面叫：它多半要回看别处的状态。
		if probe != nil {
			probe()
		}
		return failure
	}, nil
}

// emitCreated 把一个新上台的 agent 放给每一个创建观察者。
func (a *stubAgents) emitCreated(ctx context.Context, created agent.Agent) error {
	a.mutex.Lock()
	observers := append([]agent.CreatedObserver(nil), a.created...)
	a.mutex.Unlock()
	for _, observer := range observers {
		if err := observer(ctx, created); err != nil {
			return err
		}
	}
	return nil
}

// emitStatus 放一次状态跃迁。
func (a *stubAgents) emitStatus(reported agent.Agent, status agent.Status) {
	a.mutex.Lock()
	observers := append([]agent.StatusObserver(nil), a.status...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(reported, status)
	}
}

// admit 把一个 agent 放进注册表，并让它成为一个根。
func (a *stubAgents) admit(candidate agent.Agent) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.live[candidate.ID()] = candidate
	a.roots = append(a.roots, candidate)
}

// stopOrder 是那些退订函数被叫过的先后。
func (a *stubAgents) stopOrder() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]string(nil), a.stopped...)
}

// forget 把这个根从注册表里摘掉，模拟「它已经不权威了」。
func (a *stubAgents) forget() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.live = map[sessionlog.SessionID]agent.Agent{}
	a.roots = nil
}

// runtimeWorld 是一次 Runtime 用例要的全部家当。
type runtimeWorld struct {
	t        *testing.T
	owner    *stubAgent
	agents   *stubAgents
	sessions *stubSessions
	runtime  *Runtime
	now      time.Time
}

func newRuntimeWorld(t *testing.T) *runtimeWorld {
	t.Helper()
	root := scopeOf(t, "runtime-root", nil)
	owner := newStubAgent(t, "owner", root, nil)
	world := &runtimeWorld{
		t: t, owner: owner, agents: newStubAgents(owner),
		sessions: newStubSessions(), now: baseNow,
	}
	world.runtime = newRuntime(t.Context(), owner, runtimeDeps{
		agents:       world.agents,
		sessions:     world.sessions,
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:          func() time.Time { return world.now },
		transactions: newTransactions(),
	})
	// 这里**不**起驱动协程：一次成功的投递结束时会再请求一次驱动，协程跑着就会在
	// 用例做断言的同时又驱动一遍。想验 Start 本身的用例自己叫一次。收尾照样走
	// Dispose——它分得清协程起没起过。
	t.Cleanup(world.runtime.Dispose)
	return world
}

// seed 把一串改动直接落进这个 agent 的日志。
func (w *runtimeWorld) seed(payloads ...string) {
	w.t.Helper()
	for _, payload := range payloads {
		if _, err := w.owner.log.Append(changeEvent(payload)); err != nil {
			w.t.Fatalf("往日志里塞事件失败：%v", err)
		}
	}
}

// drive 跑一次完整的驱动。
func (w *runtimeWorld) drive() { w.runtime.driveOnce(w.t.Context()) }

// dispatchCount 数这条日志里的 dispatch 改动。
func (w *runtimeWorld) dispatchCount() int {
	w.t.Helper()
	count := 0
	for _, event := range w.owner.events() {
		if event.Type != EventChange {
			continue
		}
		change, err := DecodeChange(event.Data)
		if err != nil {
			w.t.Fatalf("日志里有一条读不动的改动：%v", err)
		}
		if change.Operation == OpDispatch {
			count++
		}
	}
	return count
}

// faulted 问这份投影此刻是不是已经永久熄火了。
func (w *runtimeWorld) faulted() bool {
	w.runtime.mutex.Lock()
	defer w.runtime.mutex.Unlock()
	return w.runtime.faulted
}

// armed 问此刻是不是上着一个定时器。
func (w *runtimeWorld) armed() bool {
	w.runtime.mutex.Lock()
	defer w.runtime.mutex.Unlock()
	return w.runtime.timer != nil
}

// ---- decide ----

// foldOf 把一串改动折成状态，用于直接喂给 decide。
func foldOf(t *testing.T, payloads ...string) Folded {
	t.Helper()
	events := make([]sessionlog.Event, 0, len(payloads))
	for _, payload := range payloads {
		events = append(events, changeEvent(payload))
	}
	return mustFold(t, events, 0)
}

func TestDecideReturnsEarliestFutureTargetWhenNothingIsDue(t *testing.T) {
	folded := foldOf(t,
		createJSON(atRecordJSON),    // 13:00
		createJSON(afterRecordJSON), // 12:00
	)
	decision, err := decide(folded, baseNow.Add(-time.Hour))
	if err != nil {
		t.Fatalf("本该算得出：%v", err)
	}
	if decision.kind != decisionWait || !decision.hasTarget {
		t.Fatalf("得到的是 %+v", decision)
	}
	// 挑的是**最早**的那个，不是清单里第一条。
	if !decision.target.Equal(mustParseInstant(t, "2026-08-30T12:00:00.000Z")) {
		t.Fatalf("挑到的目标是 %v", decision.target)
	}
}

func TestDecideReportsNoTargetOnEmptyState(t *testing.T) {
	// 一条活着的都没有：hasTarget 为假，调用方据此**不**上定时器。上一个没有目标
	// 的定时器等于每隔一段就白折一遍日志。
	decision, err := decide(Folded{}, baseNow)
	if err != nil {
		t.Fatalf("本该算得出：%v", err)
	}
	if decision.kind != decisionWait || decision.hasTarget {
		t.Fatalf("空状态得到的是 %+v", decision)
	}
}

func TestDecidePrefersOneShotOverDueEveryBatch(t *testing.T) {
	// every 排在前面而且更早到期，但该先出场的仍然是那条一次性的。
	folded := foldOf(t,
		createJSON(everyRecordJSON), // every, 12:00
		createJSON(atRecordJSON),    // at, 13:00
	)
	decision, err := decide(folded, mustParseInstant(t, "2026-08-30T14:00:00.000Z"))
	if err != nil {
		t.Fatalf("本该算得出：%v", err)
	}
	if decision.kind != decisionOneShot || decision.record.ID != "schedule-2" {
		t.Fatalf("挑出来的是 %+v", decision)
	}
}

func TestDecideKeepsCreationOrderAmongSimultaneousOneShots(t *testing.T) {
	// 两条一次性同一刻到期。挑走的必须是先被创建的那条——这是一次**稳定**排序的
	// 结果，换成不稳定的排序这条断言就会时对时错。
	first := `{"id":"schedule-1","kind":"at","prompt":"先建的",` +
		`"scheduledAt":"2026-08-30T12:00:00.000Z"}`
	second := `{"id":"schedule-2","kind":"at","prompt":"后建的",` +
		`"scheduledAt":"2026-08-30T12:00:00.000Z"}`
	folded := foldOf(t, createJSON(first), createJSON(second))
	decision, err := decide(folded, baseNow)
	if err != nil {
		t.Fatalf("本该算得出：%v", err)
	}
	if decision.record.ID != "schedule-1" {
		t.Fatalf("挑走的是 %q，本该是先建的那条", decision.record.ID)
	}
}

func TestDecideBatchesEveryRemindersWithOneOccurrenceEach(t *testing.T) {
	// 两条固定频率都过期了，而且各自错过了好几次。成批送过去，每条只带**最后**
	// 那一次——补响错过的那些是这个包最不该做的事。
	slow := `{"id":"schedule-1","kind":"every","prompt":"慢的",` +
		`"everySeconds":600,"scheduledAt":"2026-08-30T12:00:00.000Z"}`
	fast := `{"id":"schedule-2","kind":"every","prompt":"快的",` +
		`"everySeconds":300,"scheduledAt":"2026-08-30T12:00:00.000Z"}`
	folded := foldOf(t, createJSON(slow), createJSON(fast))
	decision, err := decide(folded, mustParseInstant(t, "2026-08-30T12:47:00.000Z"))
	if err != nil {
		t.Fatalf("本该算得出：%v", err)
	}
	if decision.kind != decisionEvery || len(decision.reminders) != 2 {
		t.Fatalf("挑出来的是 %+v", decision)
	}
	if decision.acceptedAt != "2026-08-30T12:47:00.000Z" {
		t.Fatalf("这次决定的时刻是 %q", decision.acceptedAt)
	}
	// 12:40 是十分钟那条最后一次该响的，12:45 是五分钟那条的。
	if decision.reminders[0].OccurrenceAt != "2026-08-30T12:40:00.000Z" ||
		decision.reminders[1].OccurrenceAt != "2026-08-30T12:45:00.000Z" {
		t.Fatalf("两条带的时刻是 %q / %q",
			decision.reminders[0].OccurrenceAt, decision.reminders[1].OccurrenceAt)
	}
}

func TestDecideReportsUnparsableTarget(t *testing.T) {
	_, err := decide(Folded{Active: []Record{
		{ID: "schedule-1", Kind: KindAt, Prompt: "p", ScheduledAt: "nope"},
	}}, baseNow)
	expectLogError(t, err, "读不动的 scheduledAt")
}

// ---- driveOnce 上的那两道屏障 ----

func TestDriveOnceStopsAtThePreBarrier(t *testing.T) {
	// 前置屏障没过就什么都不做：一次投递不许建立在一段随时会消失的前缀上。
	world := newRuntimeWorld(t)
	world.seed(createJSON(afterRecordJSON))
	world.sessions.err = errors.New("盘满了")
	world.now = baseNow.Add(time.Hour)

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("前置屏障没过却投了")
	}
	if world.armed() {
		t.Fatal("前置屏障没过还上了定时器")
	}
	// 这一种失败**不**永久熄火：盘满了是会好的。
	if world.faulted() {
		t.Fatal("前置屏障失败不该永久熄火")
	}
}

func TestDriveOnceArmsATimerWhenNothingIsDue(t *testing.T) {
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = baseNow // 目标在 13:00，还没到

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("没到点却投了")
	}
	if !world.armed() {
		t.Fatal("没到点本该上一个定时器")
	}
}

func TestDriveOnceDispatchesAnOverdueOneShot(t *testing.T) {
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")

	world.drive()
	if world.dispatchCount() != 1 {
		t.Fatalf("落了 %d 条 dispatch", world.dispatchCount())
	}
	texts := world.owner.followupTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "[SCHEDULE REMINDER]") {
		t.Fatalf("发出去的跟进消息是 %v", texts)
	}
	// 投完这条一次性就没了，所以再驱动一次不会有第二条。
	world.drive()
	if world.dispatchCount() != 1 {
		t.Fatalf("又投了一次，现在是 %d 条", world.dispatchCount())
	}
}

func TestDriveOnceDispatchesTheWholeEveryBatchAtOnce(t *testing.T) {
	// 两条固定频率同时到期：一次驱动写下两条 dispatch，但只发**一条**跟进消息。
	world := newRuntimeWorld(t)
	world.seed(
		createJSON(`{"id":"schedule-1","kind":"every","prompt":"甲",`+
			`"everySeconds":300,"scheduledAt":"2026-08-30T12:00:00.000Z"}`),
		createJSON(`{"id":"schedule-2","kind":"every","prompt":"乙",`+
			`"everySeconds":300,"scheduledAt":"2026-08-30T12:00:00.000Z"}`),
	)
	world.now = mustParseInstant(t, "2026-08-30T12:06:00.000Z")

	world.drive()
	if world.dispatchCount() != 2 {
		t.Fatalf("落了 %d 条 dispatch，本该是两条", world.dispatchCount())
	}
	texts := world.owner.followupTexts()
	if len(texts) != 1 {
		t.Fatalf("发了 %d 条跟进消息，本该合成一条", len(texts))
	}
	if !strings.Contains(texts[0], "[SCHEDULE REMINDER BATCH]") {
		t.Fatalf("那条跟进消息是 %q", texts[0])
	}
}

func TestDriveOncePutsTheFollowupBeforeTheDispatchEvent(t *testing.T) {
	// 这条守的是那个次序：跟进消息发出去之前不许有 dispatch 事件落盘。用一个在
	// Followup 里回看日志的探针来验——那一刻日志里必须还是干净的。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")

	seenAtFollowup := -1
	world.owner.onFollowup = func() { seenAtFollowup = world.dispatchCount() }

	world.drive()
	if seenAtFollowup != 0 {
		t.Fatalf("发跟进消息的那一刻日志里已经有 %d 条 dispatch 了", seenAtFollowup)
	}
	if world.dispatchCount() != 1 {
		t.Fatalf("投完之后是 %d 条", world.dispatchCount())
	}
}

func TestDriveOnceStopsAtThePostBarrier(t *testing.T) {
	// 后置屏障没过时那条 dispatch 已经写下了——它必须留着。抹掉它才是错的：
	// 那条提醒确实已经送到模型手上了。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	calls := 0
	world.runtime.sessions = flushFunc(func() (bool, error) {
		calls++
		return calls == 1, nil
	})

	world.drive()
	if calls != 2 {
		t.Fatalf("走了 %d 次屏障，本该是前后各一次", calls)
	}
	if world.dispatchCount() != 1 {
		t.Fatalf("后置屏障没过就把 dispatch 抹掉了，现在是 %d 条", world.dispatchCount())
	}
}

// ---- 熄火与不熄火 ----

func TestReadFoldedLatchesFaultPermanently(t *testing.T) {
	// 同一条记录被删了两遍：折不动就永久熄火，而且**此后一次都不再折**。
	world := newRuntimeWorld(t)
	world.seed(
		createJSON(atRecordJSON),
		`{"version":1,"operation":"delete","id":"schedule-2"}`,
		`{"version":1,"operation":"delete","id":"schedule-2"}`,
	)

	world.drive()
	if !world.faulted() {
		t.Fatal("折不动的日志本该让它永久熄火")
	}
	if world.armed() {
		t.Fatal("熄火之后不该还上着定时器")
	}
	// 熄火之后连触发请求都不再排队。
	before := world.sessions.flushCalls()
	world.runtime.RequestDrive()
	if world.sessions.flushCalls() != before {
		t.Fatal("熄火之后还在走屏障")
	}
}

func TestDecideOrLogDoesNotLatchFault(t *testing.T) {
	// 判断失败取决于此刻的墙上时钟，下一次可能就好了，所以它**不**熄火。
	world := newRuntimeWorld(t)
	folded := Folded{Active: []Record{
		{ID: "schedule-1", Kind: KindAt, Prompt: "p", ScheduledAt: "nope"},
	}}
	if _, ok := world.runtime.decideOrLog(folded, baseNow); ok {
		t.Fatal("这次判断本该失败")
	}
	if world.faulted() {
		t.Fatal("一次判断失败不该永久熄火")
	}
}

// ---- 认领不到空闲期 ----

func TestDriveOnceWaitsForIdleInsteadOfRetryingWhenTheClaimFails(t *testing.T) {
	// 认领不到时不许上定时器：重试多少次都一样认领不到。正确的做法是挂一个等空闲
	// 的协程，等它自己静下来再重新请求一次驱动。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.owner.maintenanceErr = errors.New("正忙着跑一个回合")
	gate := make(chan struct{})
	world.owner.whenIdle = gate

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("认领不到却投了")
	}
	if world.armed() {
		t.Fatal("认领不到时上了定时器")
	}

	// 放它过门：那条协程会请求一次新的驱动，于是信号里排上了一个令牌。
	world.owner.maintenanceErr = nil
	close(gate)
	world.runtime.waiters.Wait()
	select {
	case <-world.runtime.requests:
	default:
		t.Fatal("等到空闲之后本该请求一次新的驱动")
	}
}

func TestWaitForIdleKeepsOnlyOneWaiter(t *testing.T) {
	// 连着几次认领失败只该挂一条等空闲的协程；每次都挂一条会在一次长回合里攒出
	// 一大把，它们醒来时又会各请求一次驱动。
	world := newRuntimeWorld(t)
	gate := make(chan struct{})
	world.owner.whenIdle = gate

	world.runtime.waitForIdle()
	world.runtime.waitForIdle()
	world.runtime.waitForIdle()

	close(gate)
	world.runtime.waiters.Wait()
	// 三次里只有第一次真的挂上去了，所以只请求了一次驱动；而信号容量是一，
	// 这里能确认的是它没有因为多挂几条而卡住。
	world.runtime.mutex.Lock()
	waiting := world.runtime.idleWaiting
	world.runtime.mutex.Unlock()
	if waiting {
		t.Fatal("那条协程收完之后标志位没放回去")
	}
}

func TestWaitForIdleDoesNotRequestADriveWhenTheWaitItselfFails(t *testing.T) {
	// 等空闲失败说明这条链已经不作数了（多半是处置把 ctx 掐了）。这时候再请求一次
	// 驱动，是往一段已经收摊的会话上又排一件活儿。
	world := newRuntimeWorld(t)
	world.owner.whenIdleErr = errors.New("这条链已经废了")

	world.runtime.waitForIdle()
	world.runtime.waiters.Wait()
	select {
	case <-world.runtime.requests:
		t.Fatal("等空闲失败之后还请求了一次驱动")
	default:
	}
}

func TestWaitForIdleIsAnoopAfterDispose(t *testing.T) {
	// 收摊之后再挂一条等空闲的协程，等于让一段本该结束的活儿在后台复活。
	world := newRuntimeWorld(t)
	world.runtime.Dispose()
	world.runtime.waitForIdle()
	world.runtime.mutex.Lock()
	waiting := world.runtime.idleWaiting
	world.runtime.mutex.Unlock()
	if waiting {
		t.Fatal("收摊之后还挂上了一条等空闲的协程")
	}
}

// ---- 拿到空闲期之后那次重来 ----

func TestDispatchUnderClaimRefoldsAndBacksOffWhenTheReminderIsGone(t *testing.T) {
	// 从外面那次判断到这里，中间隔着一次认领。这段时间里用户可能刚把这条提醒删掉。
	// 拿上一次的结论直接投，投的就是一个已经过时的决定。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	// 认领成功的那一刻把它删掉，正好落在两次判断之间。
	world.owner.onMaintenance = func() {
		world.seed(`{"version":1,"operation":"delete","id":"schedule-2"}`)
	}

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("投了一条已经被删掉的提醒")
	}
	if len(world.owner.followupTexts()) != 0 {
		t.Fatal("为一条已经被删掉的提醒发了跟进消息")
	}
}

func TestDispatchUnderClaimArmsATimerWhenTheReminderMovedIntoTheFuture(t *testing.T) {
	// 另一种「过时了」：那条提醒还在，但已经不到期了。这时候该重新上一个定时器，
	// 而不是当作没事发生——不上的话，下一次醒来要等到别的什么东西碰巧触发。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.owner.onMaintenance = func() {
		// 认领成功之后时钟被往回拨了：那条 13:00 的提醒又变成未来的了。
		world.now = baseNow
	}

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("投了一条还没到点的提醒")
	}
	if !world.armed() {
		t.Fatal("重判之后发现还没到点，本该重新上一个定时器")
	}
}

func TestDispatchUnderClaimStaysSilentWhenTheRootStoppedBeingAuthoritative(t *testing.T) {
	// 认领拿到手了，但这一刻它已经不是那个权威的根了。外面那道检查过的时候还是，
	// 所以这一道不是重复的。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.owner.onMaintenance = world.agents.forget

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("已经不权威了却还是投了")
	}
}

func TestDispatchUnderClaimLatchesFaultWhenTheLogBreaksUnderTheClaim(t *testing.T) {
	// 认领之后重折时才发现日志坏了：和外面那次一样永久熄火。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.owner.onMaintenance = func() {
		world.seed(
			`{"version":1,"operation":"delete","id":"schedule-2"}`,
			`{"version":1,"operation":"delete","id":"schedule-2"}`,
		)
	}

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("日志坏了却还是投了")
	}
	if !world.faulted() {
		t.Fatal("认领之后折不动，本该永久熄火")
	}
}

// ---- Start ----

func TestStartOnlyEverRunsOneDriveLoop(t *testing.T) {
	// 起第二条协程会让 loop 里那句 `defer close(r.finished)` 二次关闭当场 panic，
	// 而 panic 发生在一条后台协程上——整个进程会跟着走。
	world := newRuntimeWorld(t)
	world.runtime.Start()
	world.runtime.Start()
	world.runtime.Dispose()
}

func TestStartIsAnoopAfterDispose(t *testing.T) {
	// 收摊之后再起协程，等于让一段本该结束的活儿在后台复活；而且那条协程永远等不到
	// 有人来收它。
	world := newRuntimeWorld(t)
	world.runtime.Dispose()
	world.runtime.Start()
	world.runtime.mutex.Lock()
	started := world.runtime.started
	world.runtime.mutex.Unlock()
	if started {
		t.Fatal("收摊之后还起了驱动协程")
	}
}

// ---- 不再权威时闭嘴 ----

func TestDriveOnceRechecksAuthorityAfterThePreBarrier(t *testing.T) {
	// 前置屏障是一次真的 I/O，走它的这段时间里这个根可能已经被换掉了。屏障之后
	// 那道重查不是重复的——省掉它，一次投递就会建立在屏障之前的那个判断上。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.runtime.sessions = flushFunc(func() (bool, error) {
		world.agents.forget()
		return true, nil
	})

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("过屏障时根被换掉了，却还是投了")
	}
}

// ---- 那张闸表 ----

func TestReleasingAnUnknownAgentIsHarmless(t *testing.T) {
	// 闸表是按 agent 分的，减到零就把记录抹掉。一次多余的释放（比如两条路都以为
	// 自己该收尾）不该把表本身弄坏，更不该 panic。
	world := newRuntimeWorld(t)
	newTransactions().release(world.owner)
}

func TestDriveOnceStaysSilentWhenTheRootIsNoLongerAuthoritative(t *testing.T) {
	// 这个 agent 已经不在注册表里了：它继续说话只会往一段不归它管的会话里写东西。
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.agents.forget()

	world.drive()
	if world.dispatchCount() != 0 {
		t.Fatal("已经不权威了却还在投")
	}
	if world.sessions.flushCalls() != 0 {
		t.Fatal("已经不权威了却还走了屏障")
	}
}

func TestIsLiveRejectsAnAgentThatIsAliveButNotARoot(t *testing.T) {
	// 按标识查得到，但它已经不是根了——这也算不权威。两条检查缺一不可。
	world := newRuntimeWorld(t)
	world.agents.mutex.Lock()
	world.agents.roots = nil
	world.agents.mutex.Unlock()
	if world.runtime.isLive() {
		t.Fatal("一个不是根的 agent 被当成了权威的")
	}
}

// ---- 处置 ----

func TestDisposeIsIdempotentAndStopsFurtherWork(t *testing.T) {
	world := newRuntimeWorld(t)
	world.seed(createJSON(atRecordJSON))
	world.now = mustParseInstant(t, "2026-08-30T13:00:00.000Z")
	world.runtime.Start()

	world.runtime.Dispose()
	world.runtime.Dispose() // 第二次必须当场返回，不能卡在那个已经关掉的 channel 上

	before := world.dispatchCount()
	world.runtime.RequestDrive()
	world.drive()
	if world.dispatchCount() != before {
		t.Fatal("处置之后还在投")
	}
}

func TestArmIsAnoopAfterDispose(t *testing.T) {
	// 处置之后上定时器等于让一段本该结束的活儿在后台复活。
	world := newRuntimeWorld(t)
	world.runtime.Dispose()
	world.runtime.arm(baseNow.Add(time.Hour), baseNow)
	if world.armed() {
		t.Fatal("处置之后还上了定时器")
	}
}

func TestArmCapsTheDelayAtMaxTimerDelay(t *testing.T) {
	// 一段睡满一个月的定时器醒来时算出的目标可能早就不对了（机器休眠、系统时间被
	// 往前拨）。分段之后每一段醒来都重折一次，错得再多也只错一段。
	world := newRuntimeWorld(t)
	far := baseNow.Add(100 * 24 * time.Hour)
	if far.Sub(baseNow) <= MaxTimerDelay {
		t.Fatal("这个用例挑的目标没有超过上限，验不到那次截断")
	}
	world.runtime.arm(far, baseNow)
	if !world.armed() {
		t.Fatal("本该上一个定时器")
	}
}
