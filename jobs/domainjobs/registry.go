// 本文件的作用：这台注册表本身——发号、围墙、并发上限、结算、等待，以及
// 「这件活儿在谁那儿」这条贯穿始终的分岔。
//
// 新增: 整个文件都是本仓库自有的。它和
// [github.com/snight1983/ds-harness-go/jobs/localjobs] 那台注册表实现的是同一条缝
// （[jobs.Registry]），但账本从进程内存搬到了域表，于是每一条读写都要问一句
// 「这条记录是不是我起的」——那台的每一个方法都可以假定「是」，这台不能。
//
// 三条不变量：
//
//  1. **记录先于活儿**：[Registry.Start] 先把一条 running 记录条件写进账本，
//     再调 [jobs.Start.Run]。反过来的话中间崩一下就有一件跑着的活儿在账本上不存在，
//     而没有任何一个副本知道该去停它。
//  2. **执行资源不跨副本**：[jobs.Hooks] 只在起这件作业的那个进程手里。别的副本
//     读得到记录、看得见状态，但读不了实时输出、停不了它，此时**报错**而不是
//     假装成功。
//  3. **账本是权威**：状态、结局、汇报标记全都落在表上。本副本内存里那张句柄表
//     只放执行资源，一个字的状态都不放。

package domainjobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/util/timeout"
)

// TaskWaitTimeout 是那个把「等到点了」和「调用方自己取消了」分开的超时代号。
//
// 源: packages/jobs/jobs-local/src/index.ts:24-25（TASK_WAIT_TIMEOUT）
//
// 和 [github.com/snight1983/ds-harness-go/jobs/localjobs.TaskWaitTimeout] 同值：
// 它是 [github.com/snight1983/ds-harness-go/util/timeout] 那一侧用来认领期限的代号，
// 两台注册表在这件事上没有分歧。
const TaskWaitTimeout = "TASK_WAIT_TIMEOUT"

// localHandle 是一件**本副本起的**作业的执行资源。它不带任何状态——状态在账本上。
type localHandle struct {
	// ready 在 [jobs.Start.Run] 交回钩子、这张句柄填好之后才立起来。
	//
	// 记录先于活儿（见文件头第 1 条），所以从条件写落定到钩子到手之间有一小段
	// 「账本上是 running、执行资源还没到手」的窗口。这个标志把那一段和
	// 「这件活儿在别的副本上」分开说：两者都读不到实时输出，但原因完全不同。
	ready bool
	// owner 是那个确切的生命周期属主，无主作业为 nil。
	//
	// 账本上只有会话 id（见 [Record.OwnerSession]）。投递监听器要的是那个确切实例，
	// 而它只有起这件作业的副本手里有。
	owner      agent.Agent
	cancel     func(reason string) error
	readOutput func() string

	// settled 在终态记录提交之后关掉，把本副本的等待者一起放开。
	settled chan struct{}
	// waiters 是当下还在等的人数，结算时靠它认领那次汇报。
	waiters int
}

// Registry 是那台把账本放在域表上的作业注册表。
type Registry struct {
	runner                    jobs.RunnerID
	agents                    Agents
	now                       func() time.Time
	logger                    *slog.Logger
	maxConcurrentJobsPerOwner int
	foreignPollInterval       time.Duration

	dom   *domain.Domain
	table *domain.Table[Record]

	// layers 把控制器和两种监听器按登记它们的作用域分层放，语义同
	// [github.com/snight1983/ds-harness-go/jobs/localjobs]，见 subscribe.go。
	layers *scope.Layers[*jobLayer]

	// mutex 罩着下面这几个字段，以及每一张 [localHandle] 的可变部分。
	mutex sync.Mutex
	// handles 只放**本副本**起的那些作业的执行资源。
	handles map[jobs.JobID]*localHandle
	// listenersClosed 在服务开始拆除时置上，此后不再宣布完成。
	listenersClosed bool
	// ownerCleanups 是挂到各属主作用域上的那些清理，映射到各自那个摘除函数。
	ownerCleanups map[agent.Agent]func(context.Context) error
}

// Registry 必须满足那条缝。
var _ jobs.Registry = (*Registry)(nil)

// New 开出这个域并造一台注册表。
//
// 它会把**上一次这个副本**留在账本上的那些活着的记录接管掉，见 [Registry.takeover]。
//
// 域的生命周期归这台注册表：[Registry.Dispose] 会把它关掉。装配方交进来的是设施
// 而不是一个已经打开的域，理由见 [Config.Facility]。
func New(ctx context.Context, config Config) (*Registry, error) {
	resolved, err := config.resolve()
	if err != nil {
		return nil, err
	}
	// onChange 交 nil：没有任何东西从层里派生缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*jobLayer, error) { return newJobLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		return nil, err
	}

	opened, err := resolved.Facility.Open(ctx, Spec())
	if err != nil {
		return nil, fmt.Errorf("domainjobs: 打不开作业域：%w", err)
	}
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		// 开出来的域这条路上没人再会用它，不关就是一个一直占着域名的句柄。
		// 关闭本身的失败不覆盖原因。
		if closeErr := opened.Close(ctx); closeErr != nil {
			resolved.Logger.Warn("domainjobs: 打开失败后关闭域也失败", "error", closeErr)
		}
		return nil, fmt.Errorf("domainjobs: 作业域里取不到 %q 表：%w", TableName, err)
	}

	registry := &Registry{
		runner:                    resolved.Runner,
		agents:                    resolved.Agents,
		now:                       resolved.Now,
		logger:                    resolved.Logger,
		maxConcurrentJobsPerOwner: resolved.MaxConcurrentJobsPerOwner,
		foreignPollInterval:       resolved.ForeignPollInterval,
		dom:                       opened,
		table:                     table,
		layers:                    layers,
		handles:                   make(map[jobs.JobID]*localHandle),
		ownerCleanups:             make(map[agent.Agent]func(context.Context) error),
	}
	if err := registry.takeover(ctx); err != nil {
		if closeErr := opened.Close(ctx); closeErr != nil {
			resolved.Logger.Warn("domainjobs: 接管失败后关闭域也失败", "error", closeErr)
		}
		return nil, err
	}
	return registry, nil
}

// takeover 把上一次这个副本留下的那些活着的记录判成 failed。
//
// 一条盖着**我这个** [Config.Runner]、状态还是 running 或者 stopping 的记录，
// 在我刚起来的这一刻只可能有一个来历：上一次这个副本的进程没了，而它手里那份
// 执行资源跟着没了。不接管的话那些记录永远挂在 running 上——没有任何一个副本
// 会去动它们（别人认得出那不是自己的），它们会一直占着属主的并发名额。
//
// 只碰自己这个 runner 的记录：别的副本正跑着的那些活儿关我什么事。
func (r *Registry) takeover(ctx context.Context) error {
	entries, err := r.table.Entries(ctx)
	if err != nil {
		return fmt.Errorf("domainjobs: 接管上一次的记录时列不出账本：%w", err)
	}
	for _, entry := range entries {
		if entry.Value.Runner != r.runner || !entry.Value.IsActive() {
			continue
		}
		updated, updateErr := r.table.Update(ctx, entry.Key, func(current Record) (Record, error) {
			if current.Status.IsTerminal() {
				return current, nil
			}
			current.Status = jobs.StatusFailed
			current.Detail = "runner restarted; job was lost with the previous process"
			current.FinishedAt = r.now()
			// 不动 Reported：这条记录有没有被人取走过，是上一次进程的事实，
			// 这次接管没有资格替它回答。
			return current, nil
		})
		if updateErr != nil {
			return fmt.Errorf("domainjobs: 接管作业 %s 失败：%w", entry.Key, updateErr)
		}
		r.logger.Warn("domainjobs: 接管了上一次进程留下的作业，已判为失败",
			"job", updated.ID, "runner", r.runner)
	}
	return nil
}

// ---- 开工 ----

// Start 先把访问、校验、属主清理和并发上限过一遍，条件写下一条 running 记录，
// 再去起活儿。
//
// 次序不能换，见文件头第 1 条。[jobs.Start.Run] 出错时那条刚写下的记录会被删掉，
// 所以「预检拒掉不留任何痕迹」这条契约照旧成立。
func (r *Registry) Start(ctx context.Context, spec jobs.Start) (jobs.JobID, error) {
	if err := r.admit(spec); err != nil {
		return "", err
	}
	record, err := r.claim(ctx, spec)
	if err != nil {
		return "", err
	}

	// 先把句柄立起来（还没就绪），这样本副本的 Read/Kill 在下面那段窗口里说得出
	// 「它正在起」，而不是错报成「它在别的副本上」，见 [localHandle.ready]。
	handle := &localHandle{owner: spec.Owner, settled: make(chan struct{})}
	r.mutex.Lock()
	r.handles[record.ID] = handle
	r.mutex.Unlock()

	// 起活儿在锁外：生产方那一下可能很慢，也可能回头调这台注册表。
	hooks, err := spec.Run()
	if err != nil {
		r.abandon(ctx, record.ID)
		return "", err
	}
	if hooks.Cancel == nil || hooks.Done == nil {
		// 同 localjobs：没有 Cancel 就停不掉，没有 Done 就永远结算不了。此时资源
		// 已经起来了，收拾它是生产方的事，和 Run 自己出错那条路一致。
		r.abandon(ctx, record.ID)
		return "", fmt.Errorf("domainjobs: 生产方交回的钩子缺了 Cancel 或者 Done")
	}

	r.mutex.Lock()
	handle.cancel = hooks.Cancel
	handle.readOutput = hooks.ReadOutput
	handle.ready = true
	r.mutex.Unlock()

	go r.collect(record.ID, handle, hooks.Done)
	// 登记落定了、且从这里往后不可能再失败，所以可见集合是真的变了。
	r.notifyChanged(spec.Owner)
	return record.ID, nil
}

// admit 是开工前那些**不碰账本**的预检：控制器围墙、声明校验、属主清理。
//
// 源: packages/jobs/jobs-local/src/index.ts:132-145
//
// 并发上限不在这里，它要读账本，和发号是同一次读，见 [Registry.claim]。
func (r *Registry) admit(spec jobs.Start) error {
	if !r.servesOwner(spec.Owner) {
		return errors.New("background jobs unavailable: no job controller serves this agent " +
			"(load github.com/snight1983/ds-harness-go/jobs/jobstool in its composition)")
	}
	if spec.Kind == "" {
		return errors.New("invalid job kind: expected a non-empty string")
	}
	if strings.Contains(string(spec.Kind), "-") {
		// 新增: DSH 那边种类是一个声明合并出来的字面量联合，写不出带横杠的值。
		// Go 这边它是一个开放的具名字符串（见 [jobs.JobKind]），而这台注册表要从
		// id 里把序号解回来（见 [ordinalOf]）——`a-b-3` 的种类到底是 `a` 还是 `a-b`
		// 没法判，所以在入口就把这个歧义挡掉。
		return fmt.Errorf("invalid job kind %q: must not contain '-'", spec.Kind)
	}
	if spec.Label == "" {
		return errors.New("invalid job label: expected a non-empty string")
	}
	if spec.OutputLimitBytes < 0 {
		return fmt.Errorf("invalid outputLimitBytes: expected a non-negative byte count, got %d", spec.OutputLimitBytes)
	}
	if spec.Run == nil {
		return errors.New("domainjobs: 开工声明缺了 Run")
	}
	if spec.Owner != nil {
		return r.ensureOwnerCleanup(spec.Owner)
	}
	return nil
}

// claim 抢一个号并把那条 running 记录写进账本。
//
// 发号不靠共享计数器：每一轮读一次全表，取这一类当下的最大序号，然后对
// `<种类>-<最大号+1>` 这把键做一次 [domain.Table.Create]。撞车（另一个副本刚好也在
// 开这一类的活儿）由后端那条 [storage.CodeStaleRevision] 说出来，重读重试。
// 谁的条件写落了地，这个号就是谁的。
//
// 并发上限和最大序号是同一次读算出来的：它们要的是同一份账本状态，分两次读的话
// 中间那个窗口会让上限判在一份和发号不同的世界上。
func (r *Registry) claim(ctx context.Context, spec jobs.Start) (Record, error) {
	ownerSession := sessionOf(spec.Owner)
	var lastConflict error
	for attempt := 0; attempt < startAttempts; attempt++ {
		entries, err := r.table.Entries(ctx)
		if err != nil {
			return Record{}, fmt.Errorf("domainjobs: 开工前读不了账本：%w", err)
		}
		active, highest := 0, 0
		for _, entry := range entries {
			current := entry.Value
			if current.OwnerSession == ownerSession && current.IsActive() {
				active++
			}
			if ordinal, ok := ordinalOf(current.ID, spec.Kind); ok && ordinal > highest {
				highest = ordinal
			}
		}
		if active >= r.maxConcurrentJobsPerOwner {
			// 这个数是跨副本算的，见 [Config.MaxConcurrentJobsPerOwner]。
			return Record{}, fmt.Errorf("background job limit reached for this owner (limit: %d); "+
				"use job_kill to stop an unneeded job, wait for it to finish, then retry",
				r.maxConcurrentJobsPerOwner)
		}

		record := Record{
			ID:               jobs.JobID(fmt.Sprintf("%s-%d", spec.Kind, highest+1)),
			Kind:             spec.Kind,
			Runner:           r.runner,
			Label:            spec.Label,
			OutputLimitBytes: spec.OutputLimitBytes,
			OwnerSession:     ownerSession,
			Status:           jobs.StatusRunning,
			StartedAt:        r.now(),
		}
		err = r.table.Create(ctx, string(record.ID), record)
		if err == nil {
			return record, nil
		}
		if !isStaleRevision(err) {
			return Record{}, fmt.Errorf("domainjobs: 登记作业 %s 失败：%w", record.ID, err)
		}
		lastConflict = err
	}
	return Record{}, fmt.Errorf("domainjobs: 连着 %d 次抢号都被别的副本抢先，这一类正在被高频开工：%w",
		startAttempts, lastConflict)
}

// abandon 把一条刚写下、活儿却没起来的记录撤掉。
//
// 删除失败只记日志：这次开工本来就要报错，而那个错误说的是活儿为什么起不来，
// 比「善后没做干净」更该被调用方看见。留下的那条记录会被下一次 [Registry.takeover]
// 判成失败——它盖的是本副本的 runner，而本副本手里没有它。
func (r *Registry) abandon(ctx context.Context, id jobs.JobID) {
	r.mutex.Lock()
	delete(r.handles, id)
	r.mutex.Unlock()
	if _, err := r.table.Delete(ctx, string(id)); err != nil {
		r.logger.Warn("domainjobs: 活儿没起来，撤销那条记录也失败了", "job", id, "error", err)
	}
}

// collect 守着生产方那条结局 channel，把第一个（也是唯一一个）结局交给结算。
//
// 源: packages/jobs/jobs-local/src/index.ts:178-185
//
// 关掉而不送值、以及一个非终态的结局，都被兜成 failed，理由同
// [github.com/snight1983/ds-harness-go/jobs/localjobs] 那一份。
func (r *Registry) collect(id jobs.JobID, handle *localHandle, done <-chan jobs.Outcome) {
	outcome, ok := <-done
	switch {
	case !ok:
		r.logger.Warn("domainjobs: 生产方关掉了 done 却没给结局（违反生产方契约）", "job", id)
		outcome = jobs.Outcome{
			Status: jobs.StatusFailed,
			Detail: "producer closed done without an outcome (producer contract violation)",
		}
	case !outcome.Status.IsTerminal():
		r.logger.Warn("domainjobs: 生产方给了一个非终态的结局（违反生产方契约）", "job", id, "status", outcome.Status)
		outcome = jobs.Outcome{
			Status: jobs.StatusFailed,
			Detail: fmt.Sprintf("producer reported non-terminal status %q (producer contract violation)", outcome.Status),
		}
	}
	r.settle(id, handle, outcome)
}

// settle 把终态结局提交进账本，放开等待者，最后才宣布完成。
//
// 源: packages/jobs/jobs-local/src/index.ts:416-440
//
// 新增: 这条协程活得比起它的那次 [Registry.Start] 长——那次调用早就返回了，
// 它的 ctx 多半已经取消。所以提交用 [context.Background]：一件已经跑完的活儿，
// 它的结局必须落进账本，否则那条记录会永远挂在 running 上，而账本是跨副本的
// 唯一权威。
//
// 完成放在最后宣布，因为一个汇报方可能当场就开一个模型回合：这次结算的其余每一个
// 观察者都必须已经看过那条提交了的记录。
func (r *Registry) settle(id jobs.JobID, handle *localHandle, outcome jobs.Outcome) {
	ctx := context.Background()

	r.mutex.Lock()
	waiters := handle.waiters
	owner := handle.owner
	closed := r.listenersClosed
	r.mutex.Unlock()

	already := false
	updated, err := r.table.Update(ctx, string(id), func(current Record) (Record, error) {
		already = current.Status.IsTerminal()
		if already {
			return current, nil
		}
		current.Status = outcome.Status
		current.Detail = outcome.Detail
		current.Output = outcome.Output
		current.FinishedAt = r.now()
		if waiters > 0 {
			// 结算这一刻还有人在等，这件作业就当场标成已汇报——紧接着跑的完成
			// 汇报方因此不会为一份马上要被取走的结果再发一次通知。
			current.Reported = true
		}
		return current, nil
	})
	if err != nil {
		// 提交不上去是介质的事，本副本除了大声说出来做不了别的：等待者仍旧被放开
		// （底下那次 close），否则它们会一直等到自己的期限。
		r.logger.Error("domainjobs: 提交作业结局失败，那条记录会停在非终态上", "job", id, "error", err)
	}

	r.mutex.Lock()
	select {
	case <-handle.settled:
	default:
		close(handle.settled)
	}
	r.mutex.Unlock()

	if err != nil || already {
		return
	}
	r.notifyChanged(owner)
	if closed {
		return
	}
	snapshot := updated.Snapshot()
	for _, listener := range r.listenersFor(owner) {
		r.callDone(listener, snapshot, owner)
	}
}

// ---- 读 ----

// List 列出调用方自己的和无主的那些作业，任何副本都看得到。
//
// 顺序是开工时刻，同刻再按序号——那正是 [jobs.Registry.List] 那条「登记顺序」。
// 不能直接用表里那个键序：键是字典序的，`bash-10` 会排在 `bash-2` 前面。
func (r *Registry) List(ctx context.Context, caller agent.Agent) ([]jobs.Snapshot, error) {
	entries, err := r.table.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("domainjobs: 列不出作业：%w", err)
	}
	callerSession := sessionOf(caller)
	visible := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.Value.OwnerSession == "" || entry.Value.OwnerSession == callerSession {
			visible = append(visible, entry.Value)
		}
	}
	slices.SortStableFunc(visible, func(a, b Record) int {
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.Compare(b.StartedAt)
		}
		left, _ := ordinalOf(a.ID, a.Kind)
		right, _ := ordinalOf(b.ID, b.Kind)
		return left - right
	})

	snapshots := make([]jobs.Snapshot, 0, len(visible))
	for _, record := range visible {
		snapshots = append(snapshots, record.Snapshot())
	}
	return snapshots, nil
}

// Get 交回一份不消费的快照，既不动读游标也不动汇报状态。
func (r *Registry) Get(ctx context.Context, id jobs.JobID, caller agent.Agent) (jobs.Snapshot, error) {
	record, err := r.reach(ctx, id, caller)
	if err != nil {
		return jobs.Snapshot{}, err
	}
	return record.Snapshot(), nil
}

// Read 读下一段流式增量，或者结算之后那份幂等的最终输出。
//
// 三条路，见文件头第 2 条：本副本握着句柄就读活的；没句柄但记录已经落定，就交回
// 账本上那份最终输出（它是幂等的，任何副本都读得到）；没句柄而记录还活着，就报错
// ——实时输出只有那个执行副本手里有，交一段空文本会让调用方以为这件活儿没产出。
func (r *Registry) Read(ctx context.Context, id jobs.JobID, caller agent.Agent) (jobs.Read, error) {
	record, err := r.reach(ctx, id, caller)
	if err != nil {
		return jobs.Read{}, err
	}
	handle := r.readyHandle(id)
	text := ""
	switch {
	case handle != nil && handle.readOutput != nil:
		// 流式那一类：游标在生产方手里，读一次就是取走上次之后的新东西。
		text = handle.readOutput()
	case record.Status.IsTerminal():
		// 只有最终输出那一类：落定之后每次都交回同一份，永远不会被消费掉。
		text = record.Output
	case handle == nil:
		return jobs.Read{}, r.notHereError(id, record, "读不到它的实时输出")
	}
	if record.Status.IsTerminal() {
		marked, markErr := r.markReported(ctx, id)
		if markErr != nil {
			return jobs.Read{}, markErr
		}
		record = marked
	}
	return jobs.Read{Text: text, Snapshot: record.Snapshot()}, nil
}

// ---- 停 ----

// Kill 请求取消，然后把作业标成 stopping 和已汇报。
//
// 停得了它的只有握着执行资源的那个副本，别的副本报错而不是假装成功，
// 见文件头第 2 条。
func (r *Registry) Kill(
	ctx context.Context,
	id jobs.JobID,
	caller agent.Agent,
	reason string,
) (jobs.KillResult, error) {
	record, err := r.reach(ctx, id, caller)
	if err != nil {
		return "", err
	}
	if record.Status.IsTerminal() {
		if _, markErr := r.markReported(ctx, id); markErr != nil {
			return "", markErr
		}
		return jobs.KillAlreadyFinished, nil
	}
	handle := r.readyHandle(id)
	if handle == nil {
		return "", r.notHereError(id, record, "停不了它")
	}

	// 先取消：它出错的话生命周期和通知状态都保持原样，这一条是 [jobs.Registry.Kill]
	// 的契约。取消在锁外调。
	if err := handle.cancel(reason); err != nil {
		return "", err
	}

	// 放锁调 cancel 的那一小段里，生产方可能已经把这件作业结算掉了，所以这次
	// 转 stopping 是有条件的：一个终态不许被 stopping 覆盖。
	if _, err := r.table.Update(ctx, string(id), func(current Record) (Record, error) {
		if !current.Status.IsTerminal() {
			current.Status = jobs.StatusStopping
		}
		current.Reported = true
		return current, nil
	}); err != nil {
		return "", fmt.Errorf("domainjobs: 记录作业 %s 的取消请求失败：%w", id, err)
	}
	r.notifyChanged(handle.owner)
	return jobs.KillRequested, nil
}

// ---- 等 ----

// Wait 等到结算或者超时，不取消这件作业。
//
// 本副本起的那些走 [localHandle.settled]，零轮询。别的副本上的那些没有那条
// channel，只能按 [Config.ForeignPollInterval] 去问账本。
//
// 新增: 形参叫 limit 而不是 timeout，让路给同名的
// [github.com/snight1983/ds-harness-go/util/timeout] 包。
func (r *Registry) Wait(
	ctx context.Context,
	id jobs.JobID,
	limit time.Duration,
	caller agent.Agent,
) (jobs.Snapshot, error) {
	record, err := r.reach(ctx, id, caller)
	if err != nil {
		return jobs.Snapshot{}, err
	}
	if limit <= 0 {
		return jobs.Snapshot{}, fmt.Errorf("invalid wait timeout: expected a positive duration, got %s", limit)
	}
	if record.Status.IsTerminal() {
		return r.finishWait(ctx, id, record)
	}
	if handle := r.readyHandle(id); handle != nil {
		return r.waitLocal(ctx, id, handle, limit)
	}
	return r.waitForeign(ctx, id, limit)
}

// waitLocal 等本副本那条结算 channel。
func (r *Registry) waitLocal(
	ctx context.Context, id jobs.JobID, handle *localHandle, limit time.Duration,
) (jobs.Snapshot, error) {
	r.mutex.Lock()
	handle.waiters++
	settled := handle.settled
	r.mutex.Unlock()

	waitCtx, stop := timeout.Deadline(ctx, limit, TaskWaitTimeout)
	defer stop()
	var waitErr error
	select {
	case <-settled:
	case <-waitCtx.Done():
		// 等到点了不是错：交回当下那份快照，这是 [jobs.Registry.Wait] 的契约。
		if timeout.OfContext(waitCtx, TaskWaitTimeout) == nil {
			waitErr = errors.New("wait aborted")
		}
	}

	r.mutex.Lock()
	handle.waiters--
	r.mutex.Unlock()

	// 一次已经欠给这个等待者的终态，不该被一次同时发生的取消抢走：select 在两边都
	// 就绪时是随机挑的，所以这里回去问那份**权威状态**——账本。
	record, err := r.reach(ctx, id, nil)
	if err != nil {
		if waitErr != nil {
			return jobs.Snapshot{}, waitErr
		}
		return jobs.Snapshot{}, err
	}
	if waitErr != nil && !record.Status.IsTerminal() {
		return jobs.Snapshot{}, waitErr
	}
	return r.finishWait(ctx, id, record)
}

// waitForeign 轮询账本，等一件跑在别的副本上的作业落定。
//
// 新增: 本副本那条路是零轮询的（一条 channel），这条没有——两个进程之间唯一的
// 共同点就是那张表。间隔取值的理由见 [defaultForeignPollInterval]。
func (r *Registry) waitForeign(
	ctx context.Context, id jobs.JobID, limit time.Duration,
) (jobs.Snapshot, error) {
	waitCtx, stop := timeout.Deadline(ctx, limit, TaskWaitTimeout)
	defer stop()
	ticker := time.NewTicker(r.foreignPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if timeout.OfContext(waitCtx, TaskWaitTimeout) == nil {
				return jobs.Snapshot{}, errors.New("wait aborted")
			}
			// 等到点了：交回当下那份快照。这次读用**外层** ctx，期限已经过了的
			// waitCtx 读什么都失败。
			record, err := r.reach(ctx, id, nil)
			if err != nil {
				return jobs.Snapshot{}, err
			}
			return r.finishWait(ctx, id, record)
		case <-ticker.C:
			record, err := r.reach(waitCtx, id, nil)
			if err != nil {
				return jobs.Snapshot{}, err
			}
			if record.Status.IsTerminal() {
				return r.finishWait(ctx, id, record)
			}
		}
	}
}

// finishWait 认领那次汇报并交回快照：这个等待者会把终态带走，汇报方不必再发一次通知。
func (r *Registry) finishWait(ctx context.Context, id jobs.JobID, record Record) (jobs.Snapshot, error) {
	if !record.Status.IsTerminal() {
		return record.Snapshot(), nil
	}
	marked, err := r.markReported(ctx, id)
	if err != nil {
		return jobs.Snapshot{}, err
	}
	return marked.Snapshot(), nil
}

// ---- 围墙与账本小工具 ----

// reach 读一条记录并核对调用方够不够得着它。
//
// 源: packages/jobs/jobs-local/src/index.ts:345-360
//
// 围墙就是这一条：有属主的作业只有会话 id 对得上的调用方够得着。id 是可预测的，
// 所以边界是**授权**，不是保密。caller 交 nil 表示这是一次内部调用（等待循环里
// 的重读），围墙在进来的那一次已经查过了。
func (r *Registry) reach(ctx context.Context, id jobs.JobID, caller agent.Agent) (Record, error) {
	record, found, err := r.table.Get(ctx, string(id))
	if err != nil {
		return Record{}, fmt.Errorf("domainjobs: 读作业 %s 失败：%w", id, err)
	}
	if !found {
		return Record{}, fmt.Errorf("unknown job %s", id)
	}
	if record.OwnerSession == "" {
		return record, nil
	}
	if record.OwnerSession != sessionOf(caller) {
		return Record{}, fmt.Errorf("job %s belongs to another session", id)
	}
	return record, nil
}

// markReported 把一条记录标成已汇报，交回标完之后那一份。
func (r *Registry) markReported(ctx context.Context, id jobs.JobID) (Record, error) {
	updated, err := r.table.Update(ctx, string(id), func(current Record) (Record, error) {
		current.Reported = true
		return current, nil
	})
	if err != nil {
		return Record{}, fmt.Errorf("domainjobs: 标记作业 %s 已汇报失败：%w", id, err)
	}
	return updated, nil
}

// readyHandle 取本副本那份**已就绪**的执行资源，没有就交回 nil。
func (r *Registry) readyHandle(id jobs.JobID) *localHandle {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	handle, ok := r.handles[id]
	if !ok || !handle.ready {
		return nil
	}
	return handle
}

// notHereError 是「这件活儿不在本副本手里」那句话，点名它在谁那儿。
//
// 「正在起」和「在别人那儿」分开说：前者是本副本自己那一小段窗口（见
// [localHandle.ready]），调用方重试一下就好；后者要换个副本才办得到。
func (r *Registry) notHereError(id jobs.JobID, record Record, what string) error {
	if record.Runner == r.runner {
		return fmt.Errorf("job %s is still starting on this runner; retry in a moment", id)
	}
	return fmt.Errorf("job %s runs on runner %s; this replica %s", id, record.Runner, what)
}

// sessionOf 取一个 agent 的会话 id，nil 交回空串（也就是那个共用的无主桶）。
func sessionOf(who agent.Agent) session.SessionID {
	if who == nil {
		return ""
	}
	return who.ID()
}

// ordinalOf 把 `<种类>-N` 里那个 N 解出来。
//
// 种类不匹配、或者后缀不是一个正整数，都交回 false——那样的键不是这台注册表发的，
// 不该参与发号。
func ordinalOf(id jobs.JobID, kind jobs.JobKind) (int, bool) {
	suffix, cut := strings.CutPrefix(string(id), string(kind)+"-")
	if !cut {
		return 0, false
	}
	ordinal, err := strconv.Atoi(suffix)
	if err != nil || ordinal <= 0 {
		return 0, false
	}
	return ordinal, true
}

// isStaleRevision 说这个错误是不是后端那条「有人抢先动了」。
func isStaleRevision(err error) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == storage.CodeStaleRevision
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
		return errors.New("background job ownership requires the agent registry " +
			"(load github.com/snight1983/ds-harness-go/core/agent)")
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

// disposeOwned 取消并等到某一个确切 agent 名下那些**本副本起的**作业落定。
//
// 源: packages/jobs/jobs-local/src/index.ts:467-475
//
// 新增: DSH 那台注册表在这一步还把记录从表里摘掉。这里不摘：那张表是跨副本的
// 持久账本，一条终态记录是别的副本（以及重启之后的本副本）唯一还能读到这件活儿
// 结局的地方，属主没了不该让它凭空消失。落定之后它们不再占并发名额，也不会再
// 出现在任何人的活跃视图里，「摘掉」原本要的效果由终态本身给出。
//
// 账本的保留策略（终态记录留多久、谁来清）本轮不做，见 doc.go。
func (r *Registry) disposeOwned(ctx context.Context, owner agent.Agent) error {
	r.mutex.Lock()
	owned := make(map[jobs.JobID]*localHandle)
	for id, handle := range r.handles {
		if handle.ready && handle.owner == owner {
			owned[id] = handle
		}
	}
	r.mutex.Unlock()
	if len(owned) == 0 {
		return nil
	}
	r.cancelForTeardown(ctx, owned, "owner disposed")
	return r.awaitAll(ctx, owned)
}

// Dispose 关掉监听、取消本副本还活着的作业、等到结算，摘掉属主那些清理，最后关域。
//
// 源: packages/jobs/jobs-local/src/index.ts:481-500
//
// 只管本副本手里那些：别的副本正跑着的活儿不归这里停。域是 [New] 开的，所以也由
// 这里关——那是这台注册表持有的唯一一样外部资源。
func (r *Registry) Dispose(ctx context.Context) error {
	r.mutex.Lock()
	// 这个标志就是全部的守卫：每一条层内登记的撤销都属于登记它的那条协程，
	// 这台服务不该在自己出门的路上替它们把东西撤掉。
	r.listenersClosed = true
	all := make(map[jobs.JobID]*localHandle, len(r.handles))
	for id, handle := range r.handles {
		if handle.ready {
			all[id] = handle
		}
	}
	cleanups := make([]func(context.Context) error, 0, len(r.ownerCleanups))
	for _, cleanup := range r.ownerCleanups {
		cleanups = append(cleanups, cleanup)
	}
	r.ownerCleanups = make(map[agent.Agent]func(context.Context) error)
	r.mutex.Unlock()

	r.cancelForTeardown(ctx, all, "jobs service disposed")
	failures := []error{r.awaitAll(ctx, all)}

	// 本副本静下来之后，再把跨协程的属主清理摘掉。此刻它们各自的 disposeOwned
	// 已经无事可做，跑一遍只是为了把那项清理从属主作用域上摘下来。
	for _, cleanup := range cleanups {
		failures = append(failures, cleanup(ctx))
	}
	failures = append(failures, r.dom.Close(ctx))
	return errors.Join(failures...)
}

// cancelForTeardown 拆除时逐件取消，每一件各自被包住。
//
// 源: packages/jobs/jobs-local/src/index.ts:507-531
//
// 取消自己出错的话只把那条记录强行判失败，并报出「活儿可能变成孤儿」；一个返回了
// 却没结算的取消和一次慢停是分不开的，可能因此拖住拆除。
func (r *Registry) cancelForTeardown(ctx context.Context, list map[jobs.JobID]*localHandle, reason string) {
	for _, id := range sortedIDs(list) {
		handle := list[id]
		// 拆除时的取消是一次没有调用方的 kill，所以它像 kill 一样认领那次终态汇报：
		// 属主或者服务正在被销毁，没有人会去读那条通知，而一个「收到通知就开一个
		// 回合」的汇报方否则会为每一层拆除各花掉一次模型请求。这一下在生产方跑之前
		// 就定了。
		stopped, err := r.table.Update(ctx, string(id), func(current Record) (Record, error) {
			if current.Status.IsTerminal() {
				return current, nil
			}
			current.Status = jobs.StatusStopping
			current.Reported = true
			return current, nil
		})
		if err != nil {
			r.logger.Warn("domainjobs: 拆除时记不下取消请求", "job", id, "error", err)
			continue
		}
		if stopped.Status.IsTerminal() {
			continue
		}

		if cancelErr := handle.cancel(reason); cancelErr != nil {
			detail := fmt.Sprintf("cancel threw during teardown; work may be orphaned: %v", cancelErr)
			r.logger.Warn("domainjobs: 拆除时取消抛了，记录被强判失败，活儿可能变成孤儿",
				"job", id, "error", cancelErr)
			r.settle(id, handle, jobs.Outcome{Status: jobs.StatusFailed, Detail: detail})
			continue
		}
		// 拆除要等生产方释放之后才走到结算，而一次慢停可以把那一刻推得很远；
		// 在这里宣布这次转变，才不会让观察者在整段窗口里一直看见 running。
		r.notifyChanged(handle.owner)
	}
}

// awaitAll 等这批作业全都落定。
//
// 新增: 认 ctx——等不到就带着还没停的那几件报出来，让上面那条拆除链看得见
// 「是谁没停」，而不是整个进程停在这儿。
func (r *Registry) awaitAll(ctx context.Context, list map[jobs.JobID]*localHandle) error {
	for _, id := range sortedIDs(list) {
		select {
		case <-list[id].settled:
		case <-ctx.Done():
			var stalled []jobs.JobID
			for _, remaining := range sortedIDs(list) {
				select {
				case <-list[remaining].settled:
				default:
					stalled = append(stalled, remaining)
				}
			}
			return fmt.Errorf("domainjobs: 等作业结算时被打断，还没停的有 %v：%w", stalled, ctx.Err())
		}
	}
	return nil
}

// sortedIDs 把一批句柄的 id 排好，让拆除的次序可复现。
func sortedIDs(list map[jobs.JobID]*localHandle) []jobs.JobID {
	ids := make([]jobs.JobID, 0, len(list))
	for id := range list {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
