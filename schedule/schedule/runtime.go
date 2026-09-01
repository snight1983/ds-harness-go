// 本文件的作用：那份可丢弃的定时器投影——谁到期了怎么挑、一次驱动都做了什么、
// 认领不到空闲期时怎么办，以及为什么这里面几乎每一步都要把日志重折一遍。
//
// 源: packages/schedule/schedule/src/runtime.ts

package schedule

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	sessionlog "github.com/snight1983/ds-harness-go/session"

	"github.com/snight1983/ds-harness-go/llm"
)

// MaxTimerDelay 是一段定时器一次最多等多久。
//
// 源: packages/schedule/schedule/src/runtime.ts:21-22（MAX_TIMER_DELAY_MS）
//
// 新增: DSH 这个数是 Node 的 setTimeout 那个 32 位限制。Go 的
// [time.Timer] 没有这个限制，但这里照样分段：**每一次醒来都重新看一眼墙上时钟**
// 才是它真正的价值。定时器走的是单调时钟，机器休眠、时区改动、系统时间被人往前
// 拨，这几种情况下单调时钟和墙上时钟会分家，一段睡满一个月的定时器醒来时算出的
// 目标就已经不对了。分段之后每一段醒来都重折一次日志、重算一次决定，错得再多也
// 只错一段。
const MaxTimerDelay = 2_147_483_647 * time.Millisecond

// Agents 是本包要用的那一小块 agent 注册表能力。
//
// 新增: 同 [Sessions]，只声明用得着的那几个方法。前两个合起来回答的是同一个问题：
// 「我手里这个 agent 此刻还是那个权威的根吗」；后两个是 [Install] 那一侧用的，
// 一个用来发现新的根，一个用来在它转回空闲时补一次重算。
type Agents interface {
	// Get 按标识取此刻活着的那个 agent。
	Get(id sessionlog.SessionID) (agent.Agent, bool)
	// Roots 给出此刻活着的所有顶层 agent。
	Roots() []agent.Agent
	// OnCreated 登记一个「有新 agent 上台」的观察者。
	OnCreated(ctx context.Context, owner *scope.Scope, observer agent.CreatedObserver) (func(context.Context) error, error)
	// OnStatus 登记一个状态跃迁观察者。
	OnStatus(ctx context.Context, owner *scope.Scope, observer agent.StatusObserver) (func(context.Context) error, error)
}

// decisionKind 是一次到期判断得出的三种结论。
//
// 源: packages/schedule/schedule/src/runtime.ts:29-32
type decisionKind int

const (
	// decisionWait 表示这一刻没有该响的，只需要（可能）上一个定时器。
	decisionWait decisionKind = iota
	// decisionOneShot 表示有一条一次性提醒到期了。
	decisionOneShot
	// decisionEvery 表示有一批固定频率提醒到期了。
	decisionEvery
)

// dueDecision 是一次到期判断的完整结论。
//
// 源: packages/schedule/schedule/src/runtime.ts:29-32
//
// 新增: DSH 是三支判别联合。这里是一个带 kind 判别的结构体，理由同 [Record]。
type dueDecision struct {
	kind decisionKind
	// record 只在 [decisionOneShot] 上。
	record Record
	// reminders 和 acceptedAt 只在 [decisionEvery] 上。
	reminders  []DueEveryReminder
	acceptedAt string
	// target 只在 [decisionWait] 上；hasTarget 为假表示一条活着的提醒都没有。
	target    time.Time
	hasTarget bool
}

// decide 从此刻的状态里挑出一条到期的一次性提醒、一整批到期的固定频率提醒，
// 或者下一次该醒来的时刻。
//
// 源: packages/schedule/schedule/src/runtime.ts:34-69
//
// 一次性**优先于**固定频率，而且一次只放一条：一次性提醒各自是一件独立的事，
// 攒成一批送过去模型就分不清先后了。固定频率那一批反过来必须一起送——几条同频率
// 的提醒常常在同一秒到期，各发一条会把模型连着打断好几次。
//
// 排序是「先按目标时刻，再按创建先后」：两条同时到期的提醒该按它们被创建的顺序
// 出场，那是用户唯一能预期的顺序。
func decide(folded Folded, now time.Time) (dueDecision, error) {
	type entry struct {
		record Record
		target time.Time
	}
	entries := make([]entry, 0, len(folded.Active))
	for _, record := range folded.Active {
		target, err := ParseInstant(record.ScheduledAt)
		if err != nil {
			return dueDecision{}, err
		}
		entries = append(entries, entry{record: record, target: target})
	}

	due := make([]entry, 0, len(entries))
	for _, each := range entries {
		if !each.target.After(now) {
			due = append(due, each)
		}
	}
	// 稳定排序，所以同一个目标时刻上留下来的就是创建先后。
	sort.SliceStable(due, func(left, right int) bool {
		return due[left].target.Before(due[right].target)
	})

	for _, each := range due {
		if each.record.Kind != KindEvery {
			return dueDecision{kind: decisionOneShot, record: each.record}, nil
		}
	}

	reminders := make([]DueEveryReminder, 0, len(due))
	for _, each := range due {
		occurrence, err := ResolveEveryOccurrence(each.record, now)
		if err != nil {
			return dueDecision{}, err
		}
		reminders = append(reminders, DueEveryReminder{
			Record: each.record, OccurrenceAt: occurrence.OccurrenceAt,
		})
	}
	if len(reminders) > 0 {
		return dueDecision{
			kind: decisionEvery, reminders: reminders, acceptedAt: FormatInstant(now),
		}, nil
	}

	decision := dueDecision{kind: decisionWait}
	for _, each := range entries {
		if each.target.After(now) && (!decision.hasTarget || each.target.Before(decision.target)) {
			decision.target, decision.hasTarget = each.target, true
		}
	}
	return decision, nil
}

// Runtime 是**一个确切的根 agent** 那份进程内的、可丢弃的提醒投影。
//
// 源: packages/schedule/schedule/src/runtime.ts:76-324
//
// 「可丢弃」是这整个类型的立身之本：它手里没有任何一份别处没有的事实，随时可以
// 被扔掉重建，扔掉不损失任何东西。真正的事实一直在会话日志里。
//
// 新增: DSH 靠 promise 链把重复触发合并起来。Go 这边是一条驱动协程加一个容量为一
// 的信号 channel：并发触发自然合并成一次，而且「上一次还在跑」和「有新的触发」
// 这两件事不需要各留一个标志位——channel 里那个令牌同时表达了它们。
type Runtime struct {
	agent        agent.Agent
	agents       Agents
	sessions     Sessions
	logger       *slog.Logger
	now          func() time.Time
	transactions *transactions

	// ctx 是这份投影自己那条链，处置时被取消。
	//
	// 它**不带发起者**：本包起的活儿不属于任何一次外来调用，挂上一个发起者会让
	// 下游把这次投递记成是某个人触发的。
	ctx    context.Context
	cancel context.CancelFunc

	// requests 是那个合并信号，容量一。
	requests chan struct{}
	// finished 在驱动协程退出时关掉。
	finished chan struct{}
	// waiters 罩着那些等空闲的协程。
	waiters sync.WaitGroup
	// disposeOnce 让处置幂等。
	disposeOnce sync.Once

	mutex       sync.Mutex
	timer       *time.Timer
	started     bool
	stopping    bool
	faulted     bool
	idleWaiting bool
}

// runtimeDeps 是造一份投影要的协作者。
type runtimeDeps struct {
	agents       Agents
	sessions     Sessions
	logger       *slog.Logger
	now          func() time.Time
	transactions *transactions
}

// newRuntime 造一份还没开动的投影；[Runtime.Start] 才开始第一次驱动。
func newRuntime(parent context.Context, owner agent.Agent, deps runtimeDeps) *Runtime {
	ctx, cancel := context.WithCancel(agent.WithoutInitiator(parent))
	return &Runtime{
		agent:        owner,
		agents:       deps.agents,
		sessions:     deps.sessions,
		logger:       deps.logger,
		now:          deps.now,
		transactions: deps.transactions,
		ctx:          ctx,
		cancel:       cancel,
		requests:     make(chan struct{}, 1),
		finished:     make(chan struct{}),
	}
}

// Start 开起驱动协程，并请求第一次驱动。
//
// 源: packages/schedule/schedule/src/runtime.ts:96-99
//
// 新增: 这里记一笔「起过了」。DSH 那边这一步只是把一个 promise 挂上去，起没起过
// 无关紧要；Go 这边 [Runtime.Dispose] 要等那条协程退出，所以它必须分得清「协程
// 跑着，等它」和「压根没起过，没什么好等的」。顺便让重复 Start 变成一次空操作——
// 起第二条协程会让 loop 里那句 `defer close(r.finished)` 二次关闭当场 panic。
func (r *Runtime) Start() {
	r.mutex.Lock()
	if r.stopping || r.started {
		r.mutex.Unlock()
		return
	}
	r.started = true
	r.mutex.Unlock()
	go r.loop()
	r.RequestDrive()
}

// RequestDrive 在一次改动落定、或者 agent 转回空闲之后，请求重算这份投影。
//
// 源: packages/schedule/schedule/src/runtime.ts:101-125
//
// 已经排着一次请求时这一次就地合并掉：每一次驱动都会把日志重折一遍，所以两次
// 紧挨着的请求和一次的结果完全一样。
func (r *Runtime) RequestDrive() {
	r.mutex.Lock()
	if r.stopping || r.faulted {
		r.mutex.Unlock()
		return
	}
	r.clearTimerLocked()
	r.mutex.Unlock()
	select {
	case r.requests <- struct{}{}:
	default:
	}
}

// Dispose 停掉今后所有的活儿、掐掉定时器，并等每一条还在跑的协程收干净。
//
// 源: packages/schedule/schedule/src/runtime.ts:127-138
//
// 新增: 那次等待要看「驱动协程起过没有」。一份造出来却没 Start 的投影上照样会调到
// 这里——[installation.onCreated] 在装工具失败时就走这条路，为的是把那条 ctx 掐掉。
// 无条件等 finished 会在那里死等一条从来没存在过的协程。
func (r *Runtime) Dispose() {
	r.disposeOnce.Do(func() {
		r.mutex.Lock()
		r.stopping = true
		started := r.started
		r.clearTimerLocked()
		r.mutex.Unlock()
		r.cancel()
		if started {
			<-r.finished
		}
		r.waiters.Wait()
	})
}

// loop 是那条驱动协程：一次一件，绝不并发。
//
// 源: packages/schedule/schedule/src/runtime.ts:140-146
func (r *Runtime) loop() {
	defer close(r.finished)
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.requests:
			_ = r.transactions.run(r.ctx, r.agent, func(ctx context.Context) error {
				r.driveOnce(ctx)
				return nil
			})
		}
	}
}

// isLive 问「这个根的生命周期此刻还权威吗」。
//
// 源: packages/schedule/schedule/src/runtime.ts:158-161
//
// 两条都要问：注册表里那个标识可能已经换成了另一个 agent（同一个会话被重新开起来），
// 而一个还活着的 agent 也可能已经不是根了。任何一条不成立，这份投影就该闭嘴——
// 它继续说话只会往一段不归它管的会话里写东西。
func (r *Runtime) isLive() bool {
	current, present := r.agents.Get(r.agent.ID())
	if !present || current != r.agent {
		return false
	}
	for _, root := range r.agents.Roots() {
		if root == r.agent {
			return true
		}
	}
	return false
}

// isRunnable 问「这份投影此刻还能不能干活」。
//
// 源: packages/schedule/schedule/src/runtime.ts:163-166
func (r *Runtime) isRunnable() bool {
	r.mutex.Lock()
	stopping := r.stopping
	r.mutex.Unlock()
	return !stopping && r.isLive()
}

// clearTimerLocked 掐掉此刻上着的那个定时器；调用方持有 mutex。
//
// 源: packages/schedule/schedule/src/runtime.ts:168-172
func (r *Runtime) clearTimerLocked() {
	if r.timer == nil {
		return
	}
	r.timer.Stop()
	r.timer = nil
}

// arm 上一段有界的定时器；醒来时重新看一眼墙上时钟。
//
// 源: packages/schedule/schedule/src/runtime.ts:174-181
func (r *Runtime) arm(target time.Time, now time.Time) {
	delay := min(target.Sub(now), MaxTimerDelay)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.stopping || r.faulted {
		return
	}
	r.clearTimerLocked()
	r.timer = time.AfterFunc(delay, r.RequestDrive)
}

// waitForIdle 等一次公开的空闲边界，**不**占着认领权，也不为此另上一个定时器。
//
// 源: packages/schedule/schedule/src/runtime.ts:183-201
//
// 这条路只在认领不到维护期时才走：那说明 agent 正忙着跑一个回合。这时候上定时器
// 重试是错的——重试多少次都一样认领不到，而且每一次都白折一遍日志。等它自己静
// 下来才是对的。
func (r *Runtime) waitForIdle() {
	r.mutex.Lock()
	if r.stopping || r.idleWaiting {
		r.mutex.Unlock()
		return
	}
	r.idleWaiting = true
	r.mutex.Unlock()

	r.waiters.Add(1)
	go func() {
		defer r.waiters.Done()
		err := r.agent.WhenIdle(r.ctx)
		r.mutex.Lock()
		r.idleWaiting = false
		r.mutex.Unlock()
		if err != nil {
			if r.isLive() && r.ctx.Err() == nil {
				r.warn("等空闲失败", err)
			}
			return
		}
		r.RequestDrive()
	}()
}

// warn 记一条只在本进程里有意义的诊断。
//
// 源: packages/schedule/schedule/src/runtime.ts:71-74
func (r *Runtime) warn(what string, err error) {
	r.logger.Warn("schedule: "+what, "agent", string(r.agent.ID()), "err", err)
}

// readFolded 折一遍此刻这份日志，并且把一条坏掉的流**关在这里**。
//
// 源: packages/schedule/schedule/src/runtime.ts:203-217
//
// 折不动就永久熄火（faulted），不再重试：日志坏了是不会自己好的，一直重试只会
// 把同一条告警刷满整个日志。此后这个会话的提醒不再投递，但会话本身照常跑——
// 一条坏掉的提醒流不该把整个 agent 拖下水。
func (r *Runtime) readFolded() (Folded, bool) {
	session := r.agent.Session()
	folded, err := FoldEvents(session.Events(), session.Header().SeedLength)
	if err != nil {
		r.mutex.Lock()
		r.faulted = true
		r.clearTimerLocked()
		r.mutex.Unlock()
		r.warn("提醒日志坏了", err)
		return Folded{}, false
	}
	return folded, true
}

// decideOrLog 算一次到期判断，算不出来时**不**永久熄火。
//
// 源: packages/schedule/schedule/src/runtime.ts:219-228
//
// 和 [Runtime.readFolded] 的区别是有意的：折日志失败说明字节坏了，那是不可恢复的；
// 而一次判断失败取决于此刻的墙上时钟，下一次触发时它可能就好了。
func (r *Runtime) decideOrLog(folded Folded, now time.Time) (dueDecision, bool) {
	decision, err := decide(folded, now)
	if err != nil {
		r.warn("固定频率的判断失败", err)
		return dueDecision{}, false
	}
	return decision, true
}

// driveOnce 是一次完整的驱动：过屏障、折日志、然后要么上定时器，要么投一次。
//
// 源: packages/schedule/schedule/src/runtime.ts:230-324
//
// 前后两道屏障都是必须的。前面那道保证「我据以做决定的这段日志已经存住了」——
// 不然一次投递可能基于一段随时会消失的前缀。后面那道保证「我刚写下的那条 dispatch
// 已经存住了」——不然进程一崩，这条提醒会在下次开机时再响一遍。
func (r *Runtime) driveOnce(ctx context.Context) {
	r.mutex.Lock()
	r.clearTimerLocked()
	r.mutex.Unlock()
	if !r.isRunnable() {
		return
	}
	if err := flushPersistence(ctx, r.sessions, r.agent.Session()); err != nil {
		if r.isLive() {
			r.warn("前置屏障失败", err)
		}
		return
	}
	if !r.isRunnable() {
		return
	}

	folded, ok := r.readFolded()
	if !ok {
		return
	}
	wakeNow := r.now()
	decision, ok := r.decideOrLog(folded, wakeNow)
	if !ok {
		return
	}
	if decision.kind == decisionWait {
		if decision.hasTarget {
			r.arm(decision.target, wakeNow)
		}
		return
	}

	// 新增: DSH 的 runMaintenance 带回任务那个 boolean，Go 的接口方法不能带类型参数，
	// 所以结论由闭包捞出来，而任务本身**永远交回 nil**。这样一来
	// RunMaintenance 交回的错就只剩一种含义：认领不到那个空闲期。
	dispatched := false
	if err := r.agent.RunMaintenance(ctx, func(claimCtx context.Context) error {
		dispatched = r.dispatchUnderClaim(claimCtx)
		return nil
	}); err != nil {
		if r.isLive() {
			r.waitForIdle()
		}
		return
	}
	if !dispatched {
		return
	}

	if err := flushPersistence(ctx, r.sessions, r.agent.Session()); err != nil {
		if r.isLive() {
			r.warn("投递后置屏障失败", err)
		}
		return
	}
	if r.isRunnable() {
		r.RequestDrive()
	}
}

// dispatchUnderClaim 在拿到那个空闲期之后**重来一遍**：重折、重判、再投。
//
// 源: packages/schedule/schedule/src/runtime.ts:260-311
//
// 为什么要重来：从上面那次判断到这里，中间隔着一次认领。这段时间里模型可能刚跑完
// 一个回合、用户可能刚删掉这条提醒。拿上一次的结论直接投，投的就是一个已经过时的
// 决定。
//
// 交回值是「有没有真的写下 dispatch」，只有真写了外面才需要走后置屏障。
func (r *Runtime) dispatchUnderClaim(ctx context.Context) bool {
	if !r.isRunnable() {
		return false
	}
	claimed, ok := r.readFolded()
	if !ok {
		return false
	}
	decisionNow := r.now()
	decision, ok := r.decideOrLog(claimed, decisionNow)
	if !ok {
		return false
	}
	if decision.kind == decisionWait {
		if decision.hasTarget {
			r.arm(decision.target, decisionNow)
		}
		return false
	}

	text := RenderReminderFraming(decision.record)
	if decision.kind == decisionEvery {
		text = RenderEveryReminderBatchFraming(decision.reminders)
	}
	r.agent.Followup(llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{Plugin: PluginName},
	))

	// 跟进消息**先于** dispatch 事件，次序不能反：先写事件再排消息的话，一次
	// 崩溃会留下「已经投过了」的记录而模型其实没收到，那条提醒就永远丢了。
	// 反过来最坏是重复投一次，那比丢掉一次好。
	if err := r.appendDispatch(decision); err != nil {
		r.mutex.Lock()
		r.faulted = true
		r.clearTimerLocked()
		r.mutex.Unlock()
		r.warn("写 dispatch 事件失败", err)
		return false
	}
	return true
}

// appendDispatch 把这次决定写成一条或一批 dispatch 事件。
//
// 源: packages/schedule/schedule/src/runtime.ts:288-305
func (r *Runtime) appendDispatch(decision dueDecision) error {
	if decision.kind == decisionOneShot {
		return appendChange(r.agent, Change{
			Version: ChangeVersion, Operation: OpDispatch, ID: decision.record.ID,
		})
	}
	for _, reminder := range decision.reminders {
		if err := appendChange(r.agent, Change{
			Version:    ChangeVersion,
			Operation:  OpDispatch,
			ID:         reminder.Record.ID,
			AcceptedAt: decision.acceptedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// appendChange 把一次改动落进这个 agent 的会话日志。
//
// 新增: DSH 是 `session.append(type, data)`。Go 这边 [core/session.Session.Append]
// 收的是一整条事件，而且**要求** Seq 和 Time 都是零——由会话自己盖章。
func appendChange(owner agent.Agent, change Change) error {
	data, err := change.MarshalJSON()
	if err != nil {
		return err
	}
	_, err = owner.Session().Append(sessionlog.Event{Type: EventChange, Data: data})
	return err
}
