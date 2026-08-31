// 本文件的作用：那台进程内的作业注册表本身——发号、围墙、并发上限、结算、
// 等待，以及属主释放和服务释放两条拆除路。
//
// 源: packages/jobs/jobs-local/src/index.ts

package localjobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/jobs/jobs"
	"ds-harness-go/session"
	"ds-harness-go/util/timeout"
)

// TaskWaitTimeout 是那个把「等到点了」和「调用方自己取消了」分开的超时代号。
//
// 源: packages/jobs/jobs-local/src/index.ts:25
const TaskWaitTimeout = "TASK_WAIT_TIMEOUT"

// defaultMaxConcurrentJobsPerOwner 是每个属主的活跃作业数默认上限。
//
// 源: packages/jobs/jobs-local/src/index.ts:28
const defaultMaxConcurrentJobsPerOwner = 10

// Agents 是这台注册表用得到的那一小块 agent 登记簿。
//
// 新增: DSH 从 cordis 上取 `ctx.get('agents')`。这里只写出真正被调到的那个方法，
// 装配方交进来的 [ds-harness-go/core/agent.Registry] 自然满足它。
type Agents interface {
	// Get 按会话 id 找那个**当下登记着**的 agent 实例。
	Get(id session.SessionID) (agent.Agent, bool)
}

// Config 是这台注册表的装配面。
//
// 源: packages/jobs/jobs-local/src/index.ts:31-37
type Config struct {
	// MaxConcurrentJobsPerOwner 是同一个属主（或者那个共用的无主桶）里 running
	// 加 stopping 的上限，0 表示用默认值 10。
	MaxConcurrentJobsPerOwner int
	// Agents 是 agent 登记簿。只有**有主**作业用得到它：属主清理必须挂在那个
	// 确切的活实例上，所以开工前要先核对交进来的就是登记着的那一个。
	//
	// 为 nil 时无主作业照常能跑，有主作业开工即被拒——和 DSH 那句
	// 「background job ownership requires the agent registry」是同一件事。
	Agents Agents
	// Now 是取时刻的那只手，为 nil 时用 [time.Now]。
	//
	// 新增: DSH 直接调 Date.now()。做成可换的一只手是本仓库的成例
	// （见 ds-harness-go/workspace 那台注册表），测试因此不必靠真的时钟。
	Now func() time.Time
	// Logger 用来报告监听器自己抛出来的错误和生产方的契约违反，为 nil 时用
	// [slog.Default]。
	Logger *slog.Logger
}

// jobLayer 是一个作用域在这张表里的全部贡献：从它挂上来的作业控制器，以及登记在
// 它那里的两种监听器。三张表都是匿名的——一次贡献由它自己那个撤销函数认定，
// 从来不靠一个后来者能顶掉的名字。
//
// 源: packages/jobs/jobs-local/src/index.ts:76-84
type jobLayer struct {
	// controllers 是挂在这一层的那些作业控制器，值是它们的诊断标签。
	//
	// 新增: DSH 存的是 `Symbol(name)`，为的是「重名的仍旧互相独立」。Go 里
	// [ds-harness-go/core/scope.AnonymousEntries] 本来就是每次追加各发一个撤销
	// 函数，独立性已经有了，所以直接存那个标签字符串。
	controllers *scope.AnonymousEntries[string]
	// listeners 是登记在这一层的完成监听器。
	listeners *scope.AnonymousEntries[jobs.DoneListener]
	// changed 是登记在这一层的「可见集合变了」观察者。
	changed *scope.AnonymousEntries[jobs.ChangedListener]
}

// newJobLayer 造一层。
func newJobLayer() *jobLayer {
	return &jobLayer{
		controllers: scope.NewAnonymousEntries[string](),
		listeners:   scope.NewAnonymousEntries[jobs.DoneListener](),
		changed:     scope.NewAnonymousEntries[jobs.ChangedListener](),
	}
}

// IsEmpty 表示这一层的每一张表都空了，[scope.Layers] 靠它回收空层。
//
// 源: packages/jobs/jobs-local/src/index.ts:81-83
func (l *jobLayer) IsEmpty() bool {
	return l.controllers.IsEmpty() && l.listeners.IsEmpty() && l.changed.IsEmpty()
}

// trackedJob 是注册表自己那份**可变**的每作业记录，绝不交出去（交出去的是
// [snapshotOf] 造的新快照）。
//
// 源: packages/jobs/jobs-local/src/index.ts:40-63
type trackedJob struct {
	id               jobs.JobID
	kind             jobs.JobKind
	label            string
	outputLimitBytes int
	// owner 是那个确切的生命周期属主；按会话 id 的授权是从它推出来的。
	owner      agent.Agent
	cancel     func(reason string) error
	readOutput func() string

	status     jobs.JobStatus
	detail     string
	output     string
	startedAt  time.Time
	finishedAt time.Time
	reported   bool

	// settled 在终态记录提交、等待者被放开的那一刻关掉。
	//
	// 新增: DSH 那边是一个 `settled` promise 加一张 waitResolvers 表。Go 里关一次
	// channel 就把所有等待者一起放开，那张表因此不存在。
	settled chan struct{}
	// waiters 是当下还在等的人数。结算那一刻只要还有人在等，这件作业就当场标成
	// 已汇报——紧接着跑的完成汇报方因此不会为一份马上要被取走的结果再发一次通知。
	waiters int
}

// Registry 是那台进程内的作业注册表。
//
// 源: packages/jobs/jobs-local/src/index.ts:91
type Registry struct {
	maxConcurrentJobsPerOwner int
	agents                    Agents
	now                       func() time.Time
	logger                    *slog.Logger

	// layers 把控制器和两种监听器按登记它们的作用域分层放。
	//
	// 源: packages/jobs/jobs-local/src/index.ts:104-116
	//
	// 一台注册表要服务所有组合，一张平表就会拿「整个进程」去回答一个「这个属主」
	// 的问题：某一份预设装的作业控制会替一个自己压根没装控制的 agent 把 Start
	// 撑开，而一次结算会打到每一份预设的监听器上。分层让这两次读都变成按属主的。
	// 没有任何东西从层里派生缓存，所以这里不需要变更通知。
	layers *scope.Layers[*jobLayer]

	// mutex 罩着下面这几个字段，以及每一条 [trackedJob] 的可变部分。
	//
	// 新增: DSH 是单线程 JS，这一层保护在那边不存在。见本包 doc.go 里
	// 「这台注册表是并发安全的」那一节。
	mutex sync.Mutex
	// store 按 id 找记录。
	store map[jobs.JobID]*trackedJob
	// order 是登记顺序。
	//
	// 新增: DSH 那边 store 是一个 JS Map，遍历天生按插入顺序，而 [jobs.Registry.List]
	// 的契约要的正是这个顺序。Go 的 map 遍历顺序是随机的，所以顺序单独记一份。
	order []*trackedJob
	// counters 是每一类的发号计数。
	counters map[jobs.JobKind]int
	// listenersClosed 在服务开始拆除时置上，此后不再announce完成。
	listenersClosed bool
	// ownerCleanups 是挂到各属主作用域上的那些清理，映射到各自那个摘除函数。
	ownerCleanups map[agent.Agent]func(context.Context) error
}

// Registry 必须满足那条缝。
var _ jobs.Registry = (*Registry)(nil)

// New 造一台进程内注册表。
//
// 源: packages/jobs/jobs-local/src/index.ts:123-129
//
// 新增: DSH 的构造函数里那一句 `ctx.effect(() => () => this.disposeAll())` 把服务
// 拆除挂在了自己的上下文上。Go 里没有那个隐式上下文，拆除是显式的 [Registry.Dispose]，
// 由装配方登记到自己的作用域上。
func New(config Config) (*Registry, error) {
	if config.MaxConcurrentJobsPerOwner < 0 {
		return nil, fmt.Errorf("localjobs: 每属主并发上限不能是负数，收到 %d", config.MaxConcurrentJobsPerOwner)
	}
	limit := config.MaxConcurrentJobsPerOwner
	if limit == 0 {
		limit = defaultMaxConcurrentJobsPerOwner
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// onChange 交 nil：没有任何东西从层里派生缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*jobLayer, error) { return newJobLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		// 它是 scope 那一侧的签名，本包无权改；照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	return &Registry{
		maxConcurrentJobsPerOwner: limit,
		agents:                    config.Agents,
		now:                       now,
		logger:                    logger,
		layers:                    layers,
		store:                     make(map[jobs.JobID]*trackedJob),
		counters:                  make(map[jobs.JobKind]int),
		ownerCleanups:             make(map[agent.Agent]func(context.Context) error),
	}, nil
}

// ---- 开工 ----

// Start 起一件活儿并原子地登记它。
//
// 源: packages/jobs/jobs-local/src/index.ts:131-190
func (r *Registry) Start(spec jobs.Start) (jobs.JobID, error) {
	if err := r.admit(spec); err != nil {
		return "", err
	}
	// 起活儿在锁外：生产方那一下可能很慢，也可能回头调这台注册表。它出错的话
	// 什么都不会被登记，收拾半开的资源是生产方自己的事。
	hooks, err := spec.Run()
	if err != nil {
		return "", err
	}
	if hooks.Cancel == nil || hooks.Done == nil {
		// 新增: DSH 靠类型保证这两只手在场。Go 里 nil 进得来，而且两种都不是能
		// 拖到运行时再发现的错：没有 Cancel 就停不掉，没有 Done 就永远结算不了，
		// 拆除会当场卡死。宁可在这里就拒绝——此时资源已经起来了，收拾它同样是
		// 生产方的事，和 Run 自己出错那条路一致。
		return "", fmt.Errorf("localjobs: 生产方交回的钩子缺了 Cancel 或者 Done")
	}

	r.mutex.Lock()
	r.counters[spec.Kind]++
	job := &trackedJob{
		id:               jobs.JobID(fmt.Sprintf("%s-%d", spec.Kind, r.counters[spec.Kind])),
		kind:             spec.Kind,
		label:            spec.Label,
		outputLimitBytes: spec.OutputLimitBytes,
		owner:            spec.Owner,
		cancel:           hooks.Cancel,
		readOutput:       hooks.ReadOutput,
		status:           jobs.StatusRunning,
		startedAt:        r.now(),
		settled:          make(chan struct{}),
	}
	r.store[job.id] = job
	r.order = append(r.order, job)
	r.mutex.Unlock()

	go r.collect(job, hooks.Done)
	// 登记落定了、且从这里往后不可能再失败，所以可见集合是真的变了。
	r.notifyChanged(job.owner)
	return job.id, nil
}

// admit 是开工前那一整套预检：访问、校验、属主清理、并发上限。
//
// 源: packages/jobs/jobs-local/src/index.ts:132-148
//
// 拒掉的话不会留下任何作业 id 或者执行资源——[Start] 正是靠「预检全过了才调
// Run」做到这一条的。
func (r *Registry) admit(spec jobs.Start) error {
	if !r.servesOwner(spec.Owner) {
		return errors.New("background jobs unavailable: no job controller serves this agent " +
			"(load ds-harness-go/jobs/jobstool in its composition)")
	}
	if spec.Kind == "" {
		return errors.New("invalid job kind: expected a non-empty string")
	}
	if spec.Label == "" {
		return errors.New("invalid job label: expected a non-empty string")
	}
	if spec.OutputLimitBytes < 0 {
		// 新增: DSH 那边这个字段缺席表示不设上限，给了就必须是正整数。Go 里
		// 零值就是「不设上限」，所以只有负数是错的。
		return fmt.Errorf("invalid outputLimitBytes: expected a non-negative byte count, got %d", spec.OutputLimitBytes)
	}
	if spec.Run == nil {
		return errors.New("localjobs: 开工声明缺了 Run")
	}
	if spec.Owner != nil {
		if err := r.ensureOwnerCleanup(spec.Owner); err != nil {
			return err
		}
	}
	r.mutex.Lock()
	active := 0
	for _, job := range r.order {
		if job.owner == spec.Owner && (job.status == jobs.StatusRunning || job.status == jobs.StatusStopping) {
			active++
		}
	}
	r.mutex.Unlock()
	if active >= r.maxConcurrentJobsPerOwner {
		return fmt.Errorf("background job limit reached for this owner (limit: %d); "+
			"use job_kill to stop an unneeded job, wait for it to finish, then retry",
			r.maxConcurrentJobsPerOwner)
	}
	return nil
}

// collect 守着生产方那条结局 channel，把第一个（也是唯一一个）结局交给结算。
//
// 源: packages/jobs/jobs-local/src/index.ts:178-185
//
// 新增: DSH 那边是 `hooks.done.then(onOutcome, onRejected)`，reject 被兜成 failed。
// Go 的 channel 没有 reject 这回事，对应物是**关掉而不送值**，所以这里判的是 ok。
// 一个非终态的结局同样被兜成 failed：[jobs.Outcome] 那条契约把「拿 IsTerminal 挡住」
// 交给了实现方，而放一个 running 的结局进去会让这件作业永远结算不了。
func (r *Registry) collect(job *trackedJob, done <-chan jobs.Outcome) {
	outcome, ok := <-done
	switch {
	case !ok:
		r.logger.Warn("jobs: 生产方关掉了 done 却没给结局（违反生产方契约）", "job", job.id)
		outcome = jobs.Outcome{
			Status: jobs.StatusFailed,
			Detail: "producer closed done without an outcome (producer contract violation)",
		}
	case !outcome.Status.IsTerminal():
		r.logger.Warn("jobs: 生产方给了一个非终态的结局（违反生产方契约）", "job", job.id, "status", outcome.Status)
		outcome = jobs.Outcome{
			Status: jobs.StatusFailed,
			Detail: fmt.Sprintf("producer reported non-terminal status %q (producer contract violation)", outcome.Status),
		}
	}
	r.settle(job, outcome)
}

// ---- 读 ----

// List 按登记顺序列出调用方自己的和无主的那些作业。
//
// 源: packages/jobs/jobs-local/src/index.ts:192-197
func (r *Registry) List(caller agent.Agent) []jobs.Snapshot {
	var callerSession session.SessionID
	if caller != nil {
		callerSession = caller.ID()
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	visible := make([]jobs.Snapshot, 0, len(r.order))
	for _, job := range r.order {
		if job.owner == nil || job.owner.ID() == callerSession {
			visible = append(visible, snapshotOf(job))
		}
	}
	return visible
}

// Get 交回一份不消费的快照。
//
// 源: packages/jobs/jobs-local/src/index.ts:199-203
func (r *Registry) Get(id jobs.JobID, caller agent.Agent) (jobs.Snapshot, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	job, err := r.reachLocked(id, caller)
	if err != nil {
		return jobs.Snapshot{}, err
	}
	return snapshotOf(job), nil
}

// Read 读下一段流式增量，或者结算之后那份幂等的最终输出。
//
// 源: packages/jobs/jobs-local/src/index.ts:205-213
func (r *Registry) Read(id jobs.JobID, caller agent.Agent) (jobs.Read, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	job, err := r.reachLocked(id, caller)
	if err != nil {
		return jobs.Read{}, err
	}
	text := ""
	switch {
	case job.readOutput != nil:
		// 流式那一类：游标在生产方手里，读一次就是取走上次之后的新东西。
		text = job.readOutput()
	case job.status.IsTerminal():
		// 只有最终输出那一类：落定之后每次都交回同一份，永远不会被消费掉。
		text = job.output
	}
	if job.status.IsTerminal() {
		job.reported = true
	}
	return jobs.Read{Text: text, Snapshot: snapshotOf(job)}, nil
}

// ---- 停 ----

// Kill 请求取消，然后把作业标成 stopping 和已汇报。
//
// 源: packages/jobs/jobs-local/src/index.ts:215-228
func (r *Registry) Kill(id jobs.JobID, caller agent.Agent, reason string) (jobs.KillResult, error) {
	r.mutex.Lock()
	job, err := r.reachLocked(id, caller)
	if err != nil {
		r.mutex.Unlock()
		return "", err
	}
	if job.status.IsTerminal() {
		job.reported = true
		r.mutex.Unlock()
		return jobs.KillAlreadyFinished, nil
	}
	cancel := job.cancel
	r.mutex.Unlock()

	// 先取消：它出错的话生命周期和通知状态都保持原样，这一条是 [jobs.Registry.Kill]
	// 的契约。取消在锁外调，理由见本包 doc.go。
	if err := cancel(reason); err != nil {
		return "", err
	}

	r.mutex.Lock()
	// 新增: 放锁调 cancel 的那一小段里，生产方可能已经把这件作业结算掉了。
	// DSH 单线程下不存在这个窗口；这里必须重新确认，否则一个终态会被 stopping 覆盖。
	if !job.status.IsTerminal() {
		job.status = jobs.StatusStopping
	}
	job.reported = true
	owner := job.owner
	r.mutex.Unlock()

	r.notifyChanged(owner)
	return jobs.KillRequested, nil
}

// Wait 等到结算或者超时，不取消这件作业。
//
// 源: packages/jobs/jobs-local/src/index.ts:230-279
//
// 新增: 形参叫 limit 而不是 timeout，让路给同名的 [ds-harness-go/util/timeout] 包。
func (r *Registry) Wait(
	ctx context.Context,
	id jobs.JobID,
	limit time.Duration,
	caller agent.Agent,
) (jobs.Snapshot, error) {
	r.mutex.Lock()
	job, err := r.reachLocked(id, caller)
	if err != nil {
		r.mutex.Unlock()
		return jobs.Snapshot{}, err
	}
	if limit <= 0 {
		r.mutex.Unlock()
		return jobs.Snapshot{}, fmt.Errorf("invalid wait timeout: expected a positive duration, got %s", limit)
	}
	pending := !job.status.IsTerminal()
	if pending {
		job.waiters++
	}
	settled := job.settled
	r.mutex.Unlock()

	if pending {
		waitErr := r.await(ctx, settled, limit)
		r.mutex.Lock()
		job.waiters--
		// 新增: 一次已经欠给这个等待者的终态，不该被一次同时发生的取消抢走。
		// DSH 那边靠「结算同步放开每一个等待者、每个被放开的等待者在同一段同步
		// 里摘掉自己那个 abort 监听」拿到这条保证；Go 的 select 在两边都就绪时
		// 是随机挑的，所以这里改成回到锁里问那份**权威状态**——它和 settled
		// 那次关闭是同一把锁下的同一次提交，比在锁外偷看 channel 更准。
		if waitErr != nil && !job.status.IsTerminal() {
			r.mutex.Unlock()
			return jobs.Snapshot{}, waitErr
		}
	} else {
		r.mutex.Lock()
	}
	defer r.mutex.Unlock()
	// 落定了就认领那次汇报：这个等待者会把终态带走，汇报方不必再发一次通知。
	if job.status.IsTerminal() {
		job.reported = true
	}
	return snapshotOf(job), nil
}

// await 等到结算、等到点、或者被调用方取消。等到点不是错——交回当下那份快照。
//
// 源: packages/jobs/jobs-local/src/index.ts:247-275
func (r *Registry) await(ctx context.Context, settled <-chan struct{}, limit time.Duration) error {
	// 带作用域的期限把「等到点了」和「调用方取消了」分开，并且每一条出口都停表。
	waitCtx, stop := timeout.Deadline(ctx, limit, TaskWaitTimeout)
	defer stop()
	select {
	case <-settled:
		return nil
	case <-waitCtx.Done():
		// 等到点了不是错：交回当下那份快照，这是 [jobs.Registry.Wait] 的契约。
		// 「结算和取消撞在一起」那一局由 [Registry.Wait] 回到锁里裁决。
		if timeout.OfContext(waitCtx, TaskWaitTimeout) != nil {
			return nil
		}
		return errors.New("wait aborted")
	}
}

// ---- 挂东西上去 ----

// OnJobDone 登记一个按作用域圈定的完成监听器。
//
// 源: packages/jobs/jobs-local/src/index.ts:281-287
func (r *Registry) OnJobDone(
	ctx context.Context,
	owner *scope.Scope,
	listener jobs.DoneListener,
) (func(context.Context) error, error) {
	if listener == nil {
		return nil, fmt.Errorf("localjobs: OnJobDone 需要一个监听器")
	}
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.listeners.Append(listener), nil
	}, scope.EffectOptions{Label: "jobs.OnJobDone()"})
}

// OnJobsChanged 登记一个按作用域圈定的「可见集合变了」观察者。
//
// 源: packages/jobs/jobs-local/src/index.ts:289-295
func (r *Registry) OnJobsChanged(
	ctx context.Context,
	owner *scope.Scope,
	listener jobs.ChangedListener,
) (func(context.Context) error, error) {
	if listener == nil {
		return nil, fmt.Errorf("localjobs: OnJobsChanged 需要一个观察者")
	}
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.changed.Append(listener), nil
	}, scope.EffectOptions{Label: "jobs.OnJobsChanged()"})
}

// AttachController 挂上一个按作用域圈定的作业控制器。
//
// 源: packages/jobs/jobs-local/src/index.ts:297-305
func (r *Registry) AttachController(
	ctx context.Context,
	owner *scope.Scope,
	name string,
) (func(context.Context) error, error) {
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.controllers.Append(name), nil
	}, scope.EffectOptions{Label: "jobs.AttachController()"})
}

// servesOwner 说有没有一个够得着的控制器，收得走也停得下这个属主的活儿。
//
// 源: packages/jobs/jobs-local/src/index.ts:315-319
//
// 全局层里是所有从无身份作用域挂上来的控制器——宿主组合自己那套控制——所以它
// 服务每一个属主；一个圈了作用域的控制器只服务组合在它底下的那些 agent。
func (r *Registry) servesOwner(owner agent.Agent) bool {
	if !r.layers.Global().controllers.IsEmpty() {
		return true
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		if !layer.controllers.IsEmpty() {
			return true
		}
	}
	return false
}

// scopeKeyOf 取一个属主的作用域钥匙，无主作业交回 nil。
//
// 新增: DSH 是 `scopeOf(owner.ctx)`。Go 里 agent 自己就带着那把钥匙。
func scopeKeyOf(owner agent.Agent) *scope.Key {
	if owner == nil {
		return nil
	}
	return owner.Scope().Key()
}

// listenersFor 给出该收这次结算的那些完成监听器：先全局层，再属主链上各层。
//
// 源: packages/jobs/jobs-local/src/index.ts:338-342
//
// 链外的监听器属于另一份组合，不许投递——否则这个属主每装一份预设就多读一条通知。
func (r *Registry) listenersFor(owner agent.Agent) []jobs.DoneListener {
	var chosen []jobs.DoneListener
	for listener := range r.layers.Global().listeners.Values() {
		chosen = append(chosen, listener)
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		for listener := range layer.listeners.Values() {
			chosen = append(chosen, listener)
		}
	}
	return chosen
}

// changedFor 给出该收这次变化的那些观察者，取法同 [Registry.listenersFor]。
//
// 源: packages/jobs/jobs-local/src/index.ts:388-392
func (r *Registry) changedFor(owner agent.Agent) []jobs.ChangedListener {
	var chosen []jobs.ChangedListener
	for listener := range r.layers.Global().changed.Values() {
		chosen = append(chosen, listener)
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		for listener := range layer.changed.Values() {
			chosen = append(chosen, listener)
		}
	}
	return chosen
}

// ---- 围墙和投影 ----

// reachLocked 找到一件作业并核对调用方够不够得着它。调用时必须持有锁。
//
// 源: packages/jobs/jobs-local/src/index.ts:345-360
//
// 围墙就是这一条：有属主的作业只有会话 id 对得上的调用方够得着。id 是可预测的，
// 所以边界是**授权**，不是保密。
func (r *Registry) reachLocked(id jobs.JobID, caller agent.Agent) (*trackedJob, error) {
	job, ok := r.store[id]
	if !ok {
		return nil, fmt.Errorf("unknown job %s", id)
	}
	if job.owner == nil {
		return job, nil
	}
	var callerSession session.SessionID
	if caller != nil {
		callerSession = caller.ID()
	}
	if job.owner.ID() != callerSession {
		return nil, fmt.Errorf("job %s belongs to another session", id)
	}
	return job, nil
}

// snapshotOf 从那份可变记录投出一份新的只读快照。调用时必须持有锁。
//
// 源: packages/jobs/jobs-local/src/index.ts:363-377
func snapshotOf(job *trackedJob) jobs.Snapshot {
	snapshot := jobs.Snapshot{
		ID:               job.id,
		Kind:             job.kind,
		Label:            job.label,
		OutputLimitBytes: job.outputLimitBytes,
		Status:           job.status,
		Detail:           job.detail,
		StartedAt:        job.startedAt,
		FinishedAt:       job.finishedAt,
		Reported:         job.reported,
	}
	if job.owner != nil {
		snapshot.OwnerSession = job.owner.ID()
	}
	return snapshot
}

// ---- 通知 ----

// notifyChanged 宣布某一个属主的可见集合变了。每个观察者都被包住：一个观察者
// 不该掀翻一次已经发生了的生命周期提交。
//
// 源: packages/jobs/jobs-local/src/index.ts:398-406
//
// 新增: 必须在**锁外**调。观察者回头调 [Registry.List] 是完全正常的事。
func (r *Registry) notifyChanged(owner agent.Agent) {
	for _, listener := range r.changedFor(owner) {
		r.callChanged(listener, owner)
	}
}

// callChanged 跑一个观察者，把它抛出来的东西关在里面。
func (r *Registry) callChanged(listener jobs.ChangedListener, owner agent.Agent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("jobs: onJobsChanged 观察者抛了", "error", recovered)
		}
	}()
	listener(owner)
}

// callDone 跑一个完成监听器，把它抛出来的东西关在里面。
func (r *Registry) callDone(listener jobs.DoneListener, snapshot jobs.Snapshot, owner agent.Agent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("jobs: onJobDone 监听器抛了", "job", snapshot.ID, "error", recovered)
		}
	}()
	listener(snapshot, owner)
}

// ---- 结算 ----

// settle 记下第一个终态结局，放开等待者，最后才announce完成。
//
// 源: packages/jobs/jobs-local/src/index.ts:416-440
//
// 先到先得护住的是「拆除强判失败」对上「生产方姗姗来迟的结局」这一局。等着的人
// 会让这件作业在监听器跑之前就被标成已汇报。完成放在最后announce，因为一个汇报方
// 可能当场就开一个模型回合：这次结算的其余每一个观察者都必须已经看过那条提交了的记录。
func (r *Registry) settle(job *trackedJob, outcome jobs.Outcome) {
	r.mutex.Lock()
	if job.status.IsTerminal() {
		r.mutex.Unlock()
		return
	}
	job.status = outcome.Status
	job.detail = outcome.Detail
	job.output = outcome.Output
	job.finishedAt = r.now()
	if job.waiters > 0 {
		job.reported = true
	}
	snapshot := snapshotOf(job)
	owner := job.owner
	closed := r.listenersClosed
	// 关掉就是放开所有等待者，同时也是 DSH 那个 markSettled。
	close(job.settled)
	r.mutex.Unlock()

	r.notifyChanged(owner)
	if closed {
		return
	}
	for _, listener := range r.listenersFor(owner) {
		r.callDone(listener, snapshot, owner)
	}
}

// ---- 拆除 ----

// ensureOwnerCleanup 通过那个确切属主的作用域挂上一项被等待的清理。
//
// 源: packages/jobs/jobs-local/src/index.ts:448-464
//
// 挂在属主自己的作用域上，所以它熬得过生产方重载，也会并进 agent 的静默过程；
// 攥住那个摘除函数是为了让服务拆除能把这条跨协程的清理摘下来。
func (r *Registry) ensureOwnerCleanup(owner agent.Agent) error {
	if r.agents == nil {
		return errors.New("background job ownership requires the agent registry (load ds-harness-go/core/agent)")
	}
	registered, ok := r.agents.Get(owner.ID())
	if !ok || registered != owner {
		return fmt.Errorf("agent %q is not the registered agent instance (background job owner must be live)", owner.ID())
	}
	ownerScope := owner.Scope()
	if ownerScope == nil {
		return fmt.Errorf("agent %q has no scope to attach background job cleanup to", owner.ID())
	}

	r.mutex.Lock()
	_, already := r.ownerCleanups[owner]
	r.mutex.Unlock()
	if already {
		return nil
	}

	detach, err := ownerScope.Defer("jobs.ownerCleanup()", func(disposeCtx context.Context) error {
		r.mutex.Lock()
		delete(r.ownerCleanups, owner)
		r.mutex.Unlock()
		return r.disposeOwned(disposeCtx, owner)
	})
	if err != nil {
		return err
	}
	// 挂成功了才记账：一个正在释放的作用域会拒绝新的清理。
	r.mutex.Lock()
	r.ownerCleanups[owner] = detach
	r.mutex.Unlock()
	return nil
}

// disposeOwned 取消、等到终态记录、然后把某一个确切 agent 生命周期名下的作业全摘掉。
//
// 源: packages/jobs/jobs-local/src/index.ts:467-475
func (r *Registry) disposeOwned(ctx context.Context, owner agent.Agent) error {
	r.mutex.Lock()
	owned := make([]*trackedJob, 0, len(r.order))
	for _, job := range r.order {
		if job.owner == owner {
			owned = append(owned, job)
		}
	}
	r.mutex.Unlock()
	if len(owned) == 0 {
		return nil
	}

	r.cancelForTeardown(owned, "owner disposed")
	waitErr := r.awaitAll(ctx, owned)

	r.mutex.Lock()
	r.dropLocked(owned)
	r.mutex.Unlock()
	// 「被摘掉」是唯一一种没有任何按作业的记录能表达的可见集合变化，所以必须在
	// 这里announce，否则一个观察者会把已经没了的那几行一直留着。
	r.notifyChanged(owner)
	return waitErr
}

// Dispose 关掉监听、取消活着的作业、等到结算，再把属主那些清理摘下来。
//
// 源: packages/jobs/jobs-local/src/index.ts:481-500
//
// 新增: DSH 那边这是构造函数里挂上去的 disposeAll，本包做成显式方法由装配方登记。
func (r *Registry) Dispose(ctx context.Context) error {
	r.mutex.Lock()
	// 这个标志就是全部的守卫：每一条层内登记的撤销都属于登记它的那条协程，
	// 这台服务不该在自己出门的路上替它们把东西撤掉。
	r.listenersClosed = true
	all := slices.Clone(r.order)
	r.mutex.Unlock()

	r.cancelForTeardown(all, "jobs service disposed")
	waitErr := r.awaitAll(ctx, all)

	r.mutex.Lock()
	// 刚刚记录一起消失的那些不重复属主。一个观察者登记在它自己那条上下文的层里，
	// 所以一个挂在这台服务之外的消费方在这里仍旧够得着；不announce的话它会把上一次
	// 收到的那几行一直留到下一次重载之后。
	emptied := distinctOwners(all)
	r.store = make(map[jobs.JobID]*trackedJob)
	r.order = nil
	cleanups := make([]func(context.Context) error, 0, len(r.ownerCleanups))
	for _, cleanup := range r.ownerCleanups {
		cleanups = append(cleanups, cleanup)
	}
	r.ownerCleanups = make(map[agent.Agent]func(context.Context) error)
	r.mutex.Unlock()

	for _, owner := range emptied {
		r.notifyChanged(owner)
	}

	// 共用的那张表静下来之后，再把跨协程的属主清理摘掉。此刻它们各自的 disposeOwned
	// 已经无事可做，跑一遍只是为了把那项清理从属主作用域上摘下来。
	failures := []error{waitErr}
	for _, cleanup := range cleanups {
		failures = append(failures, cleanup(ctx))
	}
	return errors.Join(failures...)
}

// distinctOwners 按首次出现的顺序给出这批作业里那些不重复的属主。
//
// 顺序固定下来是为了让通知的次序可复现——DSH 那边 Set 的迭代顺序天生就是插入序。
func distinctOwners(list []*trackedJob) []agent.Agent {
	seen := make(map[agent.Agent]struct{}, len(list))
	var owners []agent.Agent
	for _, job := range list {
		if _, ok := seen[job.owner]; ok {
			continue
		}
		seen[job.owner] = struct{}{}
		owners = append(owners, job.owner)
	}
	return owners
}

// dropLocked 把这批作业从表里摘掉。调用时必须持有锁。
func (r *Registry) dropLocked(list []*trackedJob) {
	for _, job := range list {
		delete(r.store, job.id)
	}
	r.order = slices.DeleteFunc(r.order, func(job *trackedJob) bool {
		_, alive := r.store[job.id]
		return !alive
	})
}

// cancelForTeardown 拆除时逐件取消，每一件各自被包住。
//
// 源: packages/jobs/jobs-local/src/index.ts:507-531
//
// 取消自己出错的话只把那条记录强行判失败，并报出「活儿可能变成孤儿」；一个返回了
// 却没结算的取消和一次慢停是分不开的，可能因此拖住拆除。
func (r *Registry) cancelForTeardown(list []*trackedJob, reason string) {
	for _, job := range list {
		r.mutex.Lock()
		if job.status.IsTerminal() {
			r.mutex.Unlock()
			continue
		}
		// 拆除时的取消是一次没有调用方的 kill，所以它像 kill 一样认领那次终态汇报。
		// 属主或者服务正在被销毁，没有人会去读那条通知，而一个「收到通知就开一个
		// 回合」的汇报方否则会为每一层拆除各花掉一次模型请求。这一下在生产方跑之前
		// 就定了：底下那次强判失败同样会结算这条记录，所以一个抛出来的取消绝不该
		// 成为唯一一条把「没汇报过的完成」announce进一个正在释放的属主的路。
		job.reported = true
		cancel := job.cancel
		owner := job.owner
		r.mutex.Unlock()

		if err := cancel(reason); err != nil {
			detail := fmt.Sprintf("cancel threw during teardown; work may be orphaned: %v", err)
			r.logger.Warn("jobs: 拆除时取消抛了，记录被强判失败，活儿可能变成孤儿", "job", job.id, "error", err)
			r.settle(job, jobs.Outcome{Status: jobs.StatusFailed, Detail: detail})
			continue
		}

		r.mutex.Lock()
		// 同 [Registry.Kill]：放锁调 cancel 那一小段里生产方可能已经结算了。
		if !job.status.IsTerminal() {
			job.status = jobs.StatusStopping
		}
		r.mutex.Unlock()
		// 拆除要等生产方释放之后才走到结算，而一次慢停可以把那一刻推得很远；
		// 在这里announce这次转变，才不会让观察者在整段窗口里一直看见 running。
		r.notifyChanged(owner)
	}
}

// awaitAll 等这批作业全都落定。
//
// 源: packages/jobs/jobs-local/src/index.ts:470,487（`await Promise.all(...)`）
//
// 新增: DSH 那边是无条件地等下去，一个不守规矩的生产方能把拆除永远卡住。Go 里
// 拆除本来就拿得到一个 ctx，所以这里认它：等不到就带着还没落定的那几件报出来，
// 让上面那条拆除链看得见「是谁没停」，而不是整个进程停在这儿。
func (r *Registry) awaitAll(ctx context.Context, list []*trackedJob) error {
	for _, job := range list {
		select {
		case <-job.settled:
		case <-ctx.Done():
			var stalled []jobs.JobID
			for _, remaining := range list {
				select {
				case <-remaining.settled:
				default:
					stalled = append(stalled, remaining.id)
				}
			}
			return fmt.Errorf("localjobs: 等作业结算时被打断，还没停的有 %v：%w", stalled, ctx.Err())
		}
	}
	return nil
}
