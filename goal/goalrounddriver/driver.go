// 本文件的作用：一个 agent 身上那台续推驱动——它记着「我预定了哪一轮、那一轮走到
// 哪一道边界了」，并且在每一道边界上重新问一遍这份预定还成不成立。
//
// 源: packages/goal/goal-round-driver/src/index.ts:21-241

package goalrounddriver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"

	"ds-harness-go/core/agent"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/goal/goal"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// attemptPhase 是一次预定走到了哪一道边界。
//
// 源: packages/goal/goal-round-driver/src/index.ts:32
type attemptPhase string

const (
	// phaseQueued 表示消息进了收件箱，驱动还没认领。
	phaseQueued attemptPhase = "queued"
	// phaseClaimed 表示驱动为某个步骤把它取走了，步骤还没进。
	phaseClaimed attemptPhase = "claimed"
	// phaseAdmitted 表示它已经作为一条 user/message 落进了日志。
	phaseAdmitted attemptPhase = "admitted"
)

// roundIdentity 是一次续推在「哪个目标的哪次修订的第几轮」上的身份。
//
// 源: packages/goal/goal-round-driver/src/index.ts:22-26
type roundIdentity struct {
	goalID   goal.ID
	revision int
	round    int
}

// roundAttempt 是一次已经预定出去、还没走完那四道边界的续推。
//
// 源: packages/goal/goal-round-driver/src/index.ts:29-35
//
// content 留一份是为了比对：收件箱那几条边只交回一条消息，本包必须能判断它到底是
// 不是自己刚才发出去的那一条。只比 messageID 不够——别人完全可以拿同一个身份塞一份
// 别的内容进来，而模型读到的是内容不是身份。
type roundAttempt struct {
	roundIdentity
	messageID llm.MessageID
	content   llm.Content
	phase     attemptPhase
	cancelled bool
	stale     bool
}

// driverDeps 是一台驱动要的那几样协作者。
type driverDeps struct {
	agents   Agents
	goals    Goals
	sessions Sessions
	logger   *slog.Logger
}

// driver 是一个 agent 身上那台续推驱动。
//
// 源: packages/goal/goal-round-driver/src/index.ts:37-46
//
// 新增: DSH 那份 DriverState 是裸字段加一条 promise 链——单线程事件循环保证了
// 处理器和 drive() 不会交错。Go 这边观察者跑在追加方那条协程上、驱动自己是另一条，
// 所以状态由 mutex 罩住，而重复触发靠一个容量为一的信号 channel 合并（成例见
// [ds-harness-go/schedule/schedule.Runtime]）。
type driver struct {
	agent agent.Agent
	deps  driverDeps

	// ctx 是这台驱动自己那条链，收摊时被取消。
	//
	// 它**不带发起者**：本包推出去的轮次不属于任何一次外来调用。挂上一个发起者会让
	// 下游把这一轮记成是某个人触发的，而那正是目标工具那几道资格闸要分开的两件事。
	ctx    context.Context
	cancel context.CancelFunc

	// requests 是那个合并信号，容量一。
	requests chan struct{}
	// finished 在驱动协程退出时关掉。
	finished chan struct{}
	// stopOnce 让收摊幂等。
	stopOnce sync.Once

	mutex           sync.Mutex
	started         bool
	stopping        bool
	attempt         *roundAttempt
	competingQueued bool
	needsCheckpoint bool
}

// newDriver 造一台还没开动的驱动。
func newDriver(parent context.Context, owner agent.Agent, deps driverDeps) *driver {
	ctx, cancel := context.WithCancel(agent.WithoutInitiator(parent))
	return &driver{
		agent:    owner,
		deps:     deps,
		ctx:      ctx,
		cancel:   cancel,
		requests: make(chan struct{}, 1),
		finished: make(chan struct{}),
	}
}

// start 开起驱动协程。
//
// 不顺手请求第一次驱动：DSH 那边一台驱动的第一次触发一律来自一条真事件
// （agent 转空闲、目标改了），装上去的那一刻什么都不推。照搬——装的时候 agent 可能
// 正跑着，这时候推一次只是白折一遍。
func (d *driver) start() {
	d.mutex.Lock()
	if d.stopping || d.started {
		d.mutex.Unlock()
		return
	}
	d.started = true
	d.mutex.Unlock()
	go d.loop()
}

// requestDrive 请求跑一遍驱动；已经排着一次时就地合并。
//
// 源: packages/goal/goal-round-driver/src/index.ts:208-241
//
// 合并是安全的，因为每一次驱动都从头把当前状态问一遍：两次紧挨着的请求和一次的
// 结果完全一样。
func (d *driver) requestDrive() {
	d.mutex.Lock()
	stopping := d.stopping
	d.mutex.Unlock()
	if stopping {
		return
	}
	select {
	case d.requests <- struct{}{}:
	default:
	}
}

// loop 是那条驱动协程：一次一件，绝不并发。
//
// 源: packages/goal/goal-round-driver/src/index.ts:215-225
func (d *driver) loop() {
	defer close(d.finished)
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.requests:
			d.driveOnce(d.ctx)
		}
	}
}

// warn 记一条只在本进程里有意义的诊断。
//
// 源: packages/goal/goal-round-driver/src/index.ts:71-73
func (d *driver) warn(what string, err error) {
	d.deps.logger.Warn("goal-round-driver: "+what, "agent", string(d.agent.ID()), "err", err)
}

// isLive 问「注册表里那个标识此刻还是这一个 agent 吗」。
//
// 源: packages/goal/goal-round-driver/src/index.ts:98
//
// 同一段会话被重新开起来时，标识还在而实例已经换了。这时候这台驱动必须闭嘴——
// 它继续说话只会往一段不归它管的会话里写东西。
func (d *driver) isLive() bool {
	current, present := d.deps.agents.Get(d.agent.ID())
	return present && current == d.agent
}

// currentGoal 只在这个确切的 agent 还活着时读它的目标。
//
// 源: packages/goal/goal-round-driver/src/index.ts:96-100
//
// 交回 nil 表示「此刻没有目标，或者这个 agent 已经不作数了」。错误原样带上去：
// 调用方各自决定它是该记一条日志还是该把这台驱动打回未活化。
func (d *driver) currentGoal() (*goal.View, error) {
	if !d.isLive() {
		return nil, nil
	}
	return d.deps.goals.Get(d.agent)
}

// readyToDrive 问「这一次驱动此刻该不该往下走」。
//
// 源: packages/goal/goal-round-driver/src/index.ts:102-109
//
// 五条全要成立：这条链还没被取消、没在收摊、注册表里还是这一个实例、agent 正空闲、
// 而且没有别人的提示词排在前面。最后一条是让路：人自己发的话永远排在自动续推前面。
func (d *driver) readyToDrive() bool {
	d.mutex.Lock()
	stopping := d.stopping
	competing := d.competingQueued
	d.mutex.Unlock()
	return d.ctx.Err() == nil && !stopping && d.isLive() &&
		d.agent.Status() == agent.StatusIdle && !competing
}

// readyAfterCheckpoint 在那次落盘等待之后重新问一遍每一条条件。
//
// 源: packages/goal/goal-round-driver/src/index.ts:111-114
//
// 多问的那一条是 needsCheckpoint：落盘settle 的这段时间里可能又来了一次目标改动，
// 那次改动该有它自己的检查点，不该被这一次顺手带过去。
func (d *driver) readyAfterCheckpoint() bool {
	if !d.readyToDrive() {
		return false
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return !d.needsCheckpoint
}

// disarm 收回续推资格，但**不动**耐久阶段。
//
// 源: packages/goal/goal-round-driver/src/index.ts:116-124
//
// 本包每一条「出了岔子」的路都收在这里：把目标留在原地，只是不再自动推它。人下次
// 来看的时候目标还是 active，重新 resume 一下就接着走——而如果这里改的是耐久阶段，
// 那一次进程内的意外就变成了一条写死在日志里的结论。
func (d *driver) disarm() {
	view, err := d.currentGoal()
	if err != nil {
		d.warn("打回未活化前读目标失败", err)
		return
	}
	if view == nil || view.Activation != goal.Armed {
		return
	}
	if _, err := d.deps.goals.Disarm(d.agent); err != nil {
		d.warn("打回未活化失败", err)
	}
}

// takeCheckpointNeeded 取走并清掉那面「该落一次盘了」的旗子。
func (d *driver) takeCheckpointNeeded() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	needed := d.needsCheckpoint
	d.needsCheckpoint = false
	return needed
}

// consumeAttempt 把手里那份预定交出去：有就撤掉它、要一次新的检查点、并再排一次驱动。
//
// 源: packages/goal/goal-round-driver/src/index.ts:156-162
//
// 这是那条「一次只准有一份预定」的闸。走到这里说明上一轮的消息已经落地（agent 转
// 空闲了），于是这台驱动先把账清干净、把那一轮记进耐久状态的动作交给下一次落盘，
// 然后才允许自己去想下一轮。
func (d *driver) consumeAttempt() bool {
	d.mutex.Lock()
	if d.attempt == nil {
		d.mutex.Unlock()
		return false
	}
	d.attempt = nil
	d.needsCheckpoint = true
	d.mutex.Unlock()
	d.requestDrive()
	return true
}

// driveOnce 跑一遍：先在静默点上把已经准入的活儿结掉，再至多预定下一轮。
//
// 源: packages/goal/goal-round-driver/src/index.ts:137-205
func (d *driver) driveOnce(ctx context.Context) {
	if !d.readyToDrive() {
		return
	}

	if d.takeCheckpointNeeded() {
		// 落盘在锁外做：它是真正的 I/O，握着锁等它会把每一条观察者都堵在门口。
		if _, err := d.deps.sessions.Flush(ctx, d.agent.Session()); err != nil {
			d.warn("落盘检查点失败", err)
			d.disarm()
			return
		}
		// 这段等待里可能来了一次改动或者一条普通提示词。让它拿到自己那个检查点、
		// 自己那个回合，本包这一次就此打住。
		if !d.readyAfterCheckpoint() {
			return
		}
	}

	if d.consumeAttempt() {
		return
	}

	view, err := d.currentGoal()
	if err != nil {
		d.warn("读当前目标失败", err)
		d.disarm()
		return
	}
	if view == nil || view.Phase != goal.PhaseActive || view.Activation != goal.Armed {
		return
	}
	if view.RoundsStarted >= view.MaxGoalRounds {
		d.blockRounds(view)
		return
	}
	d.queueRound(view)
}

// blockRounds 把一个耗尽了轮数预算的目标停在 blocked 上。
//
// 源: packages/goal/goal-round-driver/src/index.ts:166-172
//
// 停成 blocked 而不是打回未活化：轮数用完是一条该留在日志里的结论，人下次来看时
// 要能读到「它是撞上限停的」，而不是「它不知怎么就不动了」。
func (d *driver) blockRounds(view *goal.View) {
	reason := goal.BlockReason{
		Code:    "round-limit",
		Message: fmt.Sprintf("Goal reached its configured limit of %d rounds.", view.MaxGoalRounds),
	}
	if _, err := d.deps.goals.Block(d.agent, view.Ref, reason); err != nil {
		d.warn("按轮数上限停住目标失败", err)
	}
}

// queueRound 排出下一轮的提示词并把它送进收件箱。
//
// 源: packages/goal/goal-round-driver/src/index.ts:174-204
//
// 预定先立、消息后发，这个次序不能反：收件箱那几条边是**同步**回调的，
// [agent.Agent.Followup] 还没返回，inserted 就已经跑过一遍了。先发后立的话那次
// 回调看到的是一份空预定，于是它会把本包自己发的消息当成别人抢跑的提示词。
func (d *driver) queueRound(view *goal.View) {
	round := view.RoundsStarted + 1
	content := RenderRoundPrompt(view, round)
	identity := roundIdentity{goalID: view.ID, revision: view.Revision, round: round}
	source, err := goal.Source{GoalID: view.ID, Revision: view.Revision, Round: round}.MessageSource()
	if err != nil {
		d.failToQueue(view, round, err)
		return
	}
	message := llm.NewUserMessage(content, source)

	d.mutex.Lock()
	if d.stopping {
		d.mutex.Unlock()
		return
	}
	d.attempt = &roundAttempt{
		roundIdentity: identity,
		messageID:     message.ID,
		content:       message.Content,
		phase:         phaseQueued,
	}
	d.mutex.Unlock()

	// 新增: DSH 这里裹着 try/catch——它那个 followup 会抛。Go 的
	// [agent.Agent.Followup] 不交回错误（见 core/agent 那份接口），所以那条捕获
	// 分支在这一侧没有对应物；唯一还可能失败的是上面那次来源编码，它走
	// [driver.failToQueue]，用的正是 DSH 那条 queue-failed 的收尾。
	d.agent.Followup(message)
}

// failToQueue 走 DSH 那条「这一轮排不出去」的收尾：记一条日志，并把目标停住。
//
// 源: packages/goal/goal-round-driver/src/index.ts:193-204
//
// 停之前重新读一遍目标并逐项对照：这中间目标可能已经被人改掉或者停掉了，
// 那时候再往上盖一条 queue-failed，盖掉的是一条比它更权威的结论。
func (d *driver) failToQueue(view *goal.View, round int, cause error) {
	d.mutex.Lock()
	d.attempt = nil
	d.mutex.Unlock()
	d.warn(fmt.Sprintf("排不出第 %d 轮", round), cause)

	latest, err := d.currentGoal()
	if err != nil {
		d.warn("排队失败后读目标失败", err)
		return
	}
	if latest == nil || latest.ID != view.ID || latest.Revision != view.Revision ||
		latest.Phase != goal.PhaseActive || latest.Activation != goal.Armed {
		return
	}
	reason := goal.BlockReason{
		Code:    "queue-failed",
		Message: fmt.Sprintf("Could not queue goal round %d: %s", round, cause),
	}
	if _, err := d.deps.goals.Block(d.agent, latest.Ref, reason); err != nil {
		d.warn("排队失败后停住目标失败", err)
	}
}

// ---- 那几道边界上的比对 ----

// roundSource 把一条消息的来源读成一次自动续推的身份；不是就交回 false。
//
// 源: packages/goal/goal-round-driver/src/index.ts:48-51
//
// round 必须为正：目标那一层还会用 round 为 0 的来源发别的东西，那些不是自动轮次。
func roundSource(source llm.MessageSource) (goal.Source, bool) {
	parsed, ok := goal.ParseSource(source)
	if !ok || parsed.Round <= 0 {
		return goal.Source{}, false
	}
	return parsed, true
}

// goalAside 问一份来源是不是目标那一层发的「非轮次」消息——kind 是 goal，但 round
// 为 0（比如收尾指令）。
//
// 源: packages/goal/goal-round-driver/src/index.ts:127-128
//
// 新增: DSH 那边 source 是判别联合，`source.round === 0` 直接读得到。Go 这边
// [goal.ParseSource] 是**严格**的——它把 round 为 0 的来源一并判成「读不出来」
// （见 goal/goal 里 parseSourceStrict 那道 `parsed.Round < 1`），拿它来问这个问题
// 永远得到 false，那条跳过就成了死码。所以这里自己松着读一遍：只要 kind 对得上、
// 载荷解得开，round 是几就是几。
func goalAside(source llm.MessageSource) bool {
	unknown, ok := source.(llm.UnknownSource)
	if !ok || unknown.Kind != llm.SourceKind(goal.SourceKind) {
		return false
	}
	var parsed goal.Source
	if err := json.Unmarshal(unknown.Raw, &parsed); err != nil {
		return false
	}
	return parsed.Round == 0
}

// sameRound 问一份来源是不是本包预定的那一轮。
//
// 源: packages/goal/goal-round-driver/src/index.ts:53-58
func sameRound(source goal.Source, identity roundIdentity) bool {
	return source.GoalID == identity.goalID &&
		source.Revision == identity.revision &&
		source.Round == identity.round
}

// sameQueued 问收件箱交回的这条消息是不是本包发出去的那一条，连内容一起对。
//
// 源: packages/goal/goal-round-driver/src/index.ts:60-63
func sameQueued(content llm.Content, source llm.MessageSource, attempt *roundAttempt) bool {
	parsed, ok := roundSource(source)
	return ok && sameRound(parsed, attempt.roundIdentity) &&
		reflect.DeepEqual(content, attempt.content)
}

// withAttempt 在锁里拿到当前那份预定跑一段；没有预定就什么都不做。
//
// 那几条收件箱观察者全走这里：它们要读一份预定再当场改它，中间不能被驱动协程插进来。
func (d *driver) withAttempt(run func(attempt *roundAttempt)) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.attempt != nil {
		run(d.attempt)
	}
}

// ---- 那几条观察者 ----

// onStatus 是状态跃迁那条边：只关心转回空闲的那一刻。
//
// 源: packages/goal/goal-round-driver/src/index.ts:259-277
//
// 转空闲意味着上一段活动收敛了。这时候如果本包那份预定还停在 queued/claimed，
// 或者已经被判了取消，说明那一轮**没能**走完——那就把目标停住（pause），
// 让人来决定要不要接着推。不停住的话下一次驱动会原地再推一遍同一轮，而它凭什么
// 认为这一次会不一样。
func (d *driver) onStatus(status agent.Status) {
	if status != agent.StatusIdle {
		return
	}
	d.mutex.Lock()
	d.competingQueued = false
	attempt := d.attempt
	unfinished := attempt != nil &&
		(attempt.phase == phaseQueued || attempt.phase == phaseClaimed || attempt.cancelled)
	if unfinished {
		d.attempt = nil
	}
	d.mutex.Unlock()

	if unfinished {
		d.pauseUnfinished()
	}
	d.requestDrive()
}

// pauseUnfinished 把一轮没走完的续推收成一次 pause。
//
// 源: packages/goal/goal-round-driver/src/index.ts:264-274
func (d *driver) pauseUnfinished() {
	view, err := d.currentGoal()
	if err != nil {
		d.warn("停住被取消的目标前读目标失败", err)
		d.disarm()
		return
	}
	if view == nil || view.Phase != goal.PhaseActive || view.Activation != goal.Armed {
		return
	}
	if _, err := d.deps.goals.Pause(d.agent, view.Ref); err != nil {
		d.warn("停住被取消的目标失败", err)
		d.disarm()
	}
}

// onSessionStart 是会话生命周期起跑那条边：把这一段的调度状态全部清零。
//
// 源: packages/goal/goal-round-driver/src/index.ts:253-258
//
// 清的只是进程本地那点账。目标本身归 [ds-harness-go/goal/goal] 那台服务管，
// 它在同一条边上把活化打回未活化——所以这里清完之后本包不会立刻推什么。
func (d *driver) onSessionStart() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.attempt = nil
	d.competingQueued = false
	d.needsCheckpoint = false
}

// onGoalChanged 是目标改动那条边：要一次落盘检查点，然后重算。
//
// 源: packages/goal/goal-round-driver/src/index.ts:278-282
//
// 每一次改动都要检查点，是因为下一轮的提示词里写着这次改动的结果（目标描述、
// 轮数上限）。让一条模型读得到的指令跑在它所依据的那条记录前面，是这套东西里最
// 难查的一类不一致。
func (d *driver) onGoalChanged() {
	d.mutex.Lock()
	d.needsCheckpoint = true
	d.mutex.Unlock()
	d.requestDrive()
}

// onInboxInserted 是「一条消息进了收件箱」那条边。
//
// 源: packages/goal/goal-round-driver/src/index.ts:284-291
//
// 只看 next-turn 那一头：next-step 上的东西是引导和上下文，它们不跟本包抢回合。
// 进来的不是本包那一条，就立起 competingQueued 让路，并且把还排着队的那份预定
// 判成过时——它已经不可能是这个回合里唯一的那条普通消息了。
func (d *driver) onInboxInserted(message llm.Message) {
	if !slices.ContainsFunc(d.agent.Inbox().NextTurn(), func(candidate llm.Message) bool {
		return candidate.ID == message.ID
	}) {
		return
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.attempt != nil && sameQueued(message.Content, message.Source, d.attempt) {
		return
	}
	d.competingQueued = true
	if d.attempt != nil && d.attempt.phase == phaseQueued {
		d.attempt.stale = true
	}
}

// onInboxClaimed 是「驱动为某个步骤取走了一条消息」那条边。
//
// 源: packages/goal/goal-round-driver/src/index.ts:292-298
func (d *driver) onInboxClaimed(message llm.Message) {
	d.withAttempt(func(attempt *roundAttempt) {
		if sameQueued(message.Content, message.Source, attempt) {
			attempt.phase = phaseClaimed
		}
	})
}

// onInboxDiscarded 是「一条消息被丢掉了」那条边。
//
// 源: packages/goal/goal-round-driver/src/index.ts:299-305
//
// 这里只做记号不做处置：真正的收尾在下一次转空闲时（见 [driver.onStatus]）。
// 丢弃常常是一次取消的一部分，那次取消还没收敛完，此刻去改目标只会跟它打架。
func (d *driver) onInboxDiscarded(message llm.Message) {
	d.withAttempt(func(attempt *roundAttempt) {
		if sameQueued(message.Content, message.Source, attempt) {
			attempt.cancelled = true
		}
	})
}

// onSessionEvent 是会话日志那条广播：认准入，也认这个回合是怎么收的场。
//
// 源: packages/goal/goal-round-driver/src/index.ts:307-331
func (d *driver) onSessionEvent(event session.Event) {
	switch event.Type {
	case session.EventUserMessage:
		d.onUserMessageEvent(event)
	case session.EventTurnEnd:
		d.onTurnEndEvent(event)
	}
}

// onUserMessageEvent 认那条「我预定的消息落进日志了」。
//
// 源: packages/goal/goal-round-driver/src/index.ts:312-316
//
// 落进日志才算 admitted，也才算这一轮真的开出去了。读不回来的事件直接放过：
// 本包只想知道「是不是我那条」，一条坏掉的事件显然不是。
func (d *driver) onUserMessageEvent(event session.Event) {
	var data session.UserMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return
	}
	d.withAttempt(func(attempt *roundAttempt) {
		if data.Message.ID == attempt.messageID {
			attempt.phase = phaseAdmitted
		}
	})
}

// onTurnEndEvent 按这个回合的收场决定是记个号还是当场打回未活化。
//
// 源: packages/goal/goal-round-driver/src/index.ts:317-327
//
// 撞上 max-tokens 一律打回未活化：那是模型自己没说完，再推一轮多半还是撞同一堵墙，
// 而每一轮都要真花钱。取消则分两种——本包那条已经认领或准入的消息被取消了，记个号
// 等转空闲时收成一次 pause；不是本包的取消（或者本包压根没有预定），说明是外面
// 有人按了停，那就交出自动权。
func (d *driver) onTurnEndEvent(event session.Event) {
	var data session.TurnEndData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return
	}
	// 新增: 这一条走不到——[session.TurnEndData] 那份 UnmarshalJSON 已经把「没带收场
	// 理由」判成读不回来了，上面那条 return 会先接住。留着是因为那道拒收写在 session
	// 包里，本包并不拥有它；哪天它松了口，这里少了这条就会在下一行空指针解引用。
	if data.Reason == nil {
		return
	}
	switch data.Reason.TurnEndReasonKind() {
	case session.ReasonMaxTokens:
		d.disarm()
	case session.ReasonAborted:
		d.mutex.Lock()
		attempt := d.attempt
		inflight := attempt != nil &&
			(attempt.phase == phaseClaimed || attempt.phase == phaseAdmitted)
		if inflight {
			attempt.cancelled = true
		}
		d.mutex.Unlock()
		if !inflight {
			d.disarm()
		}
	}
}

// ---- 前置步骤那道闸 ----

// validReservation 问「这条已经递到步骤门口的提示词，此刻还是一份成立的预定吗」。
//
// 源: packages/goal/goal-round-driver/src/index.ts:333-347
//
// **失败即关门**：八条里差一条就不放行。这道闸是本包对模型上下文的最后一道保护——
// 走过这里的每一条续推指令，都必须由此刻这份活着的目标状态亲自背书。
func (d *driver) validReservation(content llm.Content, source goal.Source) (bool, error) {
	d.mutex.Lock()
	stopping := d.stopping
	attempt := d.attempt
	ok := attempt != nil && attempt.phase == phaseClaimed && !attempt.stale
	if ok {
		ok = sameRound(source, attempt.roundIdentity) && reflect.DeepEqual(content, attempt.content)
	}
	d.mutex.Unlock()
	if d.ctx.Err() != nil || stopping || !ok {
		return false, nil
	}

	view, err := d.currentGoal()
	if err != nil {
		return false, err
	}
	return view != nil && view.ID == source.GoalID && view.Revision == source.Revision &&
		view.Phase == goal.PhaseActive && view.Activation == goal.Armed &&
		source.Round == view.RoundsStarted+1, nil
}

// dropReservation 撤掉这份预定——只在它就是 source 说的那一轮时才撤。
//
// 源: packages/goal/goal-round-driver/src/index.ts:363-367
//
// 多问一句「是不是同一轮」，挡的是一次迟到的拒绝把别人刚立起来的那份新预定顺手
// 抹掉。
func (d *driver) dropReservation(source goal.Source) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.attempt != nil && sameRound(source, d.attempt.roundIdentity) {
		d.attempt.stale = true
		d.attempt = nil
	}
}

// clearReservation 无条件撤掉这份预定。
func (d *driver) clearReservation() {
	d.mutex.Lock()
	d.attempt = nil
	d.mutex.Unlock()
}

// restoreOtherClaimed 把这个步骤里**别人**那些已经被认领走的消息放回收件箱。
//
// 源: packages/goal/goal-round-driver/src/index.ts:126-135
//
// 本包否掉一个步骤时否的是整批消息，而那一批里可能夹着别人的引导和上下文——
// 它们已经从收件箱里被取走了，这个步骤一被拒它们就到此为止（见 core/agent 里
// [agent.InboxClaimedObserver] 的说明）。所以这里把它们逐条放回去。
//
// 反序遍历加 prepend：把原来的相对次序原样还原回队头。跳过还在队里的那些，
// 免得同一条消息在收件箱里出现两次。
//
// 新增: DSH 直接改 `agent.inbox`，这边走 [agent.Agent.Prepend]。本包这条路跑在
// agent 那条循环的协程上，而本包自己那台驱动在另一条协程上调 Followup——[agent.Inbox]
// 不带锁，两边直接动它就是一次数据竞争。Prepend 是在 agent 自己那把锁里放的。
func (d *driver) restoreOtherClaimed(messages []llm.Message, mine llm.MessageID) {
	inbox := d.agent.Inbox()
	queued := append(inbox.NextStep(), inbox.NextTurn()...)
	for _, message := range slices.Backward(messages) {
		if message.ID == mine {
			continue
		}
		if goalAside(message.Source) {
			continue
		}
		if slices.ContainsFunc(queued, func(candidate llm.Message) bool {
			return candidate.ID == message.ID
		}) {
			continue
		}
		d.agent.Prepend(message, agent.NextStep)
	}
}

// onPreStep 是前置步骤那条瀑布：本包那条续推消息进不进这个步骤，由它说了算。
//
// 源: packages/goal/goal-round-driver/src/index.ts:349-414
//
// 这批消息里没有本包的东西就直接放行，一个字都不问。
func (d *driver) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	index := slices.IndexFunc(step.Messages, func(message llm.Message) bool {
		_, ok := roundSource(message.Source)
		return ok
	})
	if index < 0 {
		return next(ctx)
	}
	submitted := step.Messages[index]
	source, _ := roundSource(submitted.Source)

	if !d.checkedValid(submitted.Content, source) {
		d.dropReservation(source)
		d.restoreOtherClaimed(step.Messages, submitted.ID)
		d.requestDrive()
		return agent.RejectStep(), nil
	}

	decision, err := next(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return agent.RejectStep(), err
		}
		// 下游有人抛了，整个步骤提议作废。趁那个「空转一圈的回合」还没回到空闲，
		// 先把预定撤掉，好让下一次驱动能重新排这一轮。
		d.clearReservation()
		d.requestDrive()
		return agent.RejectStep(), err
	}
	if ctx.Err() != nil {
		if decision.Enter {
			d.restoreOtherClaimed(decision.Messages, submitted.ID)
		}
		return decision, nil
	}
	if !decision.Enter {
		return d.blockRejected(source), nil
	}
	// 再验一遍：跑 next 的这段时间里，下游任何一个人都可能改了目标。
	if !d.checkedValid(submitted.Content, source) {
		d.clearReservation()
		d.restoreOtherClaimed(decision.Messages, submitted.ID)
		d.requestDrive()
		return agent.RejectStep(), nil
	}
	return decision, nil
}

// checkedValid 跑一次 [driver.validReservation]，把它的错误收成「不成立」。
//
// 源: packages/goal/goal-round-driver/src/index.ts:355-361、400-406
//
// 读不回目标状态时判**不成立**，而不是判成立：这道闸的默认答案必须是拒绝。
func (d *driver) checkedValid(content llm.Content, source goal.Source) bool {
	valid, err := d.validReservation(content, source)
	if err != nil {
		d.warn("前置步骤核对失败", err)
		d.disarm()
		return false
	}
	return valid
}

// blockRejected 收下「下游把这个步骤拒了」这件事，并把目标停成 blocked。
//
// 源: packages/goal/goal-round-driver/src/index.ts:388-399
//
// 这一支和 [driver.pauseUnfinished] 不同，停的是 blocked 而不是 pause：一次被拒的
// 步骤说明有人明确不让这一轮跑，那是一条该留在日志里让人看见的结论。
func (d *driver) blockRejected(source goal.Source) agent.PreStepDecision {
	d.clearReservation()
	view, err := d.currentGoal()
	if err != nil {
		d.warn("步骤被拒后读目标失败", err)
		return agent.RejectStep()
	}
	if view == nil || view.ID != source.GoalID || view.Revision != source.Revision ||
		view.Phase != goal.PhaseActive || view.Activation != goal.Armed {
		return agent.RejectStep()
	}
	reason := goal.BlockReason{
		Code:    "prompt-rejected",
		Message: "Goal round was rejected before entering its step.",
	}
	if _, err := d.deps.goals.Block(d.agent, view.Ref, reason); err != nil {
		d.warn("步骤被拒后停住目标失败", err)
	}
	return agent.RejectStep()
}

// ---- 收摊 ----

// stop 停掉这台驱动：交出自动权，把还在飞的那一轮掐掉，然后等协程收干净。
//
// 源: packages/goal/goal-round-driver/src/index.ts:425-443
//
// 掐那个还在跑的回合是必须的：本包发出去的消息此刻可能正被一个步骤消费，而本包
// 马上就不在了——没人再来给它收尾，那个回合会带着一条谁都不负责的续推指令跑完。
func (d *driver) stop(ctx context.Context) {
	d.stopOnce.Do(func() {
		d.mutex.Lock()
		d.stopping = true
		started := d.started
		attempt := d.attempt
		if attempt != nil {
			attempt.stale = true
		}
		d.mutex.Unlock()

		d.disarm()
		if attempt != nil && d.agent.Status() == agent.StatusRunning {
			d.agent.Cancel(session.ParentCancel{}, agent.CancelOptions{})
			if err := d.agent.WhenIdle(ctx); err != nil {
				d.warn("等 agent 静下来失败", err)
			}
		}

		d.cancel()
		if started {
			<-d.finished
		}
	})
}

// sessionOf 是那条会话广播的收信口：只认这个 agent 自己那段会话。
//
// 源: packages/goal/goal-round-driver/src/index.ts:308-310
func (d *driver) owns(live *coresession.Session) bool {
	return d.isLive() && d.agent.Session() == live
}
