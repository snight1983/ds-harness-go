// 本文件的作用：把这台注册表钉在它和进程内那台**真正分岔**的地方——账本跨副本、
// 执行资源不跨副本。
//
// # 这些测试防的是什么错
//
//   - **跨副本读没活**。这是整个包存在的理由：A 副本起的作业，B 副本 List 得出来、
//     Get 得到状态。这一条塌了，这个包和 localjobs 就没有区别。
//   - **别的副本上的作业被假装办成了**。B 停不了 A 手里的活儿，它必须**报错**，
//     而且要点名它在谁那儿；报「没有这件作业」是假话，报成功更糟——调用方会以为
//     一件还在跑的活儿已经停了。
//   - **发号在两个副本之间撞车**。号是靠对键本身的条件写抢的，谁赢谁拿；
//     输的那一方必须换个号，不能覆盖。
//   - **并发上限只护住一台机器**。上限护的是属主，一个会话在 A 上开满了，
//     在 B 上就不该再开得动。
//   - **上一次进程留下的记录永远挂在 running 上**。没人会去动它们，它们会一直
//     占着属主的名额。
//   - **围墙漏了**。id 是可预测的（`bash-1`），边界完全建在属主授权上。
package domainjobs

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
	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// testWait 是每一处「等一件本该马上发生的事」的上限，到了就是这台注册表卡住了。
const testWait = 5 * time.Second

// pollFast 让跨副本那条轮询路在用例里跑得完，理由见 [Config.ForeignPollInterval]。
const pollFast = 2 * time.Millisecond

// quiet 造一台什么都不往外写的日志器：好几条路径故意去触发警告。
func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- 假件 ----

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent。
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
func (a *stubAgent) Remove(llm.MessageID)                                   {}
func (a *stubAgent) Replace(llm.MessageID, llm.Message)                     {}

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

// fakeProducer 是一个能被逐步驱动的假生产方。
type fakeProducer struct {
	done chan jobs.Outcome
	// startErr 不为 nil 时起活儿就失败。
	startErr error

	// keepRunningAfterCancel 让取消只记账不结算，用来看住 stopping 那个中间态。
	//
	// 缺省是**取消即结算**：一个守规矩的生产方收到取消之后总会送出结局，
	// 而拆除要等到结局才走得完。默认不结算的话每个用例的清理都要挂满超时。
	keepRunningAfterCancel bool

	mutex sync.Mutex
	// stream 非 nil 时这是一件流式作业，每读一次交出一段。
	stream []string
	// streaming 决定钩子里带不带 ReadOutput。
	streaming bool
	// cancels 按顺序记下每一次取消收到的理由。
	cancels []string
}

func newProducer() *fakeProducer {
	return &fakeProducer{done: make(chan jobs.Outcome, 1)}
}

func (p *fakeProducer) spec(kind jobs.JobKind, label string, owner agent.Agent) jobs.Start {
	return jobs.Start{Kind: kind, Label: label, Owner: owner, Run: p.run}
}

func (p *fakeProducer) run() (jobs.Hooks, error) {
	if p.startErr != nil {
		return jobs.Hooks{}, p.startErr
	}
	hooks := jobs.Hooks{Cancel: p.cancel, Done: p.done}
	if p.streaming {
		hooks.ReadOutput = p.readOutput
	}
	return hooks, nil
}

func (p *fakeProducer) cancel(reason string) error {
	p.mutex.Lock()
	p.cancels = append(p.cancels, reason)
	settle := !p.keepRunningAfterCancel
	p.mutex.Unlock()
	if settle {
		// done 缓冲 1，所以这一送不会挡住调取消的那一方。
		select {
		case p.done <- jobs.Outcome{Status: jobs.StatusKilled, Detail: reason}:
		default:
		}
	}
	return nil
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

func (p *fakeProducer) cancelReasons() []string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]string(nil), p.cancels...)
}

// ---- 脚手架 ----

// cluster 是一份**共享介质**，几台注册表开在它上面就是几个副本。
//
// 这正是这个包要被压住的那个形状：账本是同一份，进程不是同一个。
type cluster struct {
	t      *testing.T
	medium *storagetest.MemoryMedium
	agents *stubAgents
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	return &cluster{
		t:      t,
		medium: storagetest.NewMemoryMedium(),
		agents: &stubAgents{live: map[session.SessionID]agent.Agent{}},
	}
}

// facility 在这份共享介质上再开一台域设施，也就是再造一个「进程」。
func (c *cluster) facility() *domain.Facility {
	c.t.Helper()
	hub := storage.New()
	if _, err := hub.Backend.Register("main", storagetest.NewMemoryBackend(c.medium)); err != nil {
		c.t.Fatalf("注册后端失败：%v", err)
	}
	facility, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		c.t.Fatalf("建域设施失败：%v", err)
	}
	return facility
}

// seed 绕开注册表，直接往共享账本里写一条记录。
//
// 只给那几条要「造出一个注册表自己造不出来的账本状态」的用例用——接管那条尤其：
// 一条上一次进程留下的 running 记录，按定义没有任何活着的注册表写得出来。
func (c *cluster) seed(record Record) {
	c.t.Helper()
	ctx := c.t.Context()
	opened, err := c.facility().Open(ctx, Spec())
	if err != nil {
		c.t.Fatalf("打开作业域失败：%v", err)
	}
	defer func() {
		if closeErr := opened.Close(ctx); closeErr != nil {
			c.t.Fatalf("关闭作业域失败：%v", closeErr)
		}
	}()
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		c.t.Fatalf("取作业表失败：%v", err)
	}
	if err := table.Put(ctx, string(record.ID), record); err != nil {
		c.t.Fatalf("写种子记录失败：%v", err)
	}
}

// replica 是一台装好了的注册表，外加两条能观察到通知的 channel。
type replica struct {
	t        *testing.T
	registry *Registry
	root     *scope.Scope
	settled  chan jobs.Snapshot
	changes  chan agent.Agent
}

// join 在这份共享介质上起一个副本，并在全局层挂上一个控制器和两个观察者。
//
// 挂控制器是**必须**的：没有任何控制器服务的属主一件活儿都开不了。
func (c *cluster) join(runner jobs.RunnerID, tune func(*Config)) *replica {
	c.t.Helper()
	config := Config{
		Runner:              runner,
		Facility:            c.facility(),
		Agents:              c.agents,
		ForeignPollInterval: pollFast,
		Logger:              quiet(),
	}
	if tune != nil {
		tune(&config)
	}
	registry, err := New(c.t.Context(), config)
	if err != nil {
		c.t.Fatalf("造注册表失败：%v", err)
	}
	r := &replica{
		t:        c.t,
		registry: registry,
		root:     scope.NewRoot(),
		settled:  make(chan jobs.Snapshot, 64),
		changes:  make(chan agent.Agent, 256),
	}
	c.t.Cleanup(func() {
		// 清理这条路上给一个上限：拆除要等每一件活儿落定，一个不送结局的生产方
		// 否则会把整个用例挂死在这儿而不是报出来。[Registry.awaitAll] 认这个 ctx。
		ctx, cancel := context.WithTimeout(context.Background(), testWait)
		defer cancel()
		_ = r.root.Dispose(ctx)
		_ = registry.Dispose(ctx)
	})
	if _, err := registry.AttachController(c.t.Context(), r.root, "test-controller"); err != nil {
		c.t.Fatalf("挂控制器失败：%v", err)
	}
	if _, err := registry.OnJobDone(c.t.Context(), r.root, func(snapshot jobs.Snapshot, _ agent.Agent) {
		select {
		case r.settled <- snapshot:
		default:
		}
	}); err != nil {
		c.t.Fatalf("挂完成监听器失败：%v", err)
	}
	if _, err := registry.OnJobsChanged(c.t.Context(), r.root, func(owner agent.Agent) {
		select {
		case r.changes <- owner:
		default:
		}
	}); err != nil {
		c.t.Fatalf("挂变化观察者失败：%v", err)
	}
	return r
}

func (r *replica) start(spec jobs.Start) jobs.JobID {
	r.t.Helper()
	id, err := r.registry.Start(r.t.Context(), spec)
	if err != nil {
		r.t.Fatalf("开工失败：%v", err)
	}
	return id
}

func (r *replica) list(caller agent.Agent) []jobs.Snapshot {
	r.t.Helper()
	snapshots, err := r.registry.List(r.t.Context(), caller)
	if err != nil {
		r.t.Fatalf("列作业失败：%v", err)
	}
	return snapshots
}

func (r *replica) get(id jobs.JobID, caller agent.Agent) jobs.Snapshot {
	r.t.Helper()
	snapshot, err := r.registry.Get(r.t.Context(), id, caller)
	if err != nil {
		r.t.Fatalf("读快照失败：%v", err)
	}
	return snapshot
}

// finish 送出一个结局，并等到起这件活儿的那个副本把它记进账本。
//
// 同步点是那条完成通知：它在记录提交、变化宣布完之后才跑。
func (r *replica) finish(producer *fakeProducer, outcome jobs.Outcome) jobs.Snapshot {
	r.t.Helper()
	producer.done <- outcome
	select {
	case snapshot := <-r.settled:
		return snapshot
	case <-time.After(testWait):
		r.t.Fatal("等结算通知超时")
		return jobs.Snapshot{}
	}
}

// newOwner 造一个带作用域、并且登记在册的属主。
func (c *cluster) newOwner(id session.SessionID) *stubAgent {
	c.t.Helper()
	own, err := scope.New(scope.NewKey(string(id)), scope.Options{})
	if err != nil {
		c.t.Fatalf("造属主作用域失败：%v", err)
	}
	owner := &stubAgent{id: id, own: own}
	c.agents.live[id] = owner
	c.t.Cleanup(func() { _ = own.Dispose(context.Background()) })
	return owner
}

// ids 把一串快照压成它们的 id，让断言只看顺序。
func ids(snapshots []jobs.Snapshot) []jobs.JobID {
	out := make([]jobs.JobID, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, snapshot.ID)
	}
	return out
}

// ---- 靶心：跨副本 ----

// TestAnotherReplicaSeesTheJob 是这个包存在的理由：A 起的活儿，B 看得见。
func TestAnotherReplicaSeesTheJob(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "sleep 1", nil))

	if got := ids(b.list(nil)); len(got) != 1 || got[0] != id {
		t.Fatalf("B 副本该看得见 A 起的那件作业，列出来的是 %v", got)
	}
	snapshot := b.get(id, nil)
	if snapshot.Status != jobs.StatusRunning {
		t.Fatalf("B 副本读到的状态是 %q，该是 running", snapshot.Status)
	}
	if snapshot.Runner != "replica-a" {
		t.Fatalf("这件作业该记着它在 replica-a 上，记的是 %q", snapshot.Runner)
	}
	if snapshot.Label != "sleep 1" {
		t.Fatalf("标签该跨副本读得到，读到的是 %q", snapshot.Label)
	}
}

// TestAnotherReplicaRefusesToKillOrReadALiveJob 钉住那句「这儿办不了」。
//
// 它必须点名那个执行副本，而且**不能**说成「没有这件作业」——后者会让调用方
// 以为自己记错了 id，然后去做完全不相干的事。
func TestAnotherReplicaRefusesToKillOrReadALiveJob(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	producer := newProducer()
	producer.streaming = true
	producer.stream = []string{"第一段"}
	id := a.start(producer.spec(jobs.KindBash, "tail -f log", nil))

	_, readErr := b.registry.Read(t.Context(), id, nil)
	if readErr == nil {
		t.Fatal("B 副本读不到 A 手里那件活儿的实时输出，这里必须报错")
	}
	if !strings.Contains(readErr.Error(), "replica-a") {
		t.Fatalf("报错该点名它在 replica-a 上，收到 %v", readErr)
	}
	if strings.Contains(readErr.Error(), "unknown job") {
		t.Fatalf("这件作业是存在的，不许说成不认识，收到 %v", readErr)
	}

	result, killErr := b.registry.Kill(t.Context(), id, nil, "算了")
	if killErr == nil {
		t.Fatalf("B 副本停不了 A 手里那件活儿，这里必须报错，收到结果 %q", result)
	}
	if !strings.Contains(killErr.Error(), "replica-a") {
		t.Fatalf("报错该点名它在 replica-a 上，收到 %v", killErr)
	}
	if len(producer.cancelReasons()) != 0 {
		t.Fatalf("生产方根本不该被碰到，却收到了取消 %v", producer.cancelReasons())
	}
	// 状态一个字都没变：一次办不到的 kill 不许留下任何痕迹。
	if got := b.get(id, nil).Status; got != jobs.StatusRunning {
		t.Fatalf("那次被拒的 kill 之后状态成了 %q，该还是 running", got)
	}
}

// TestTheFinalOutputIsReadableFromAnyReplica 钉住落定之后那条路：结局在账本上，
// 谁都读得到。
//
// 这是「实时输出读不到」的另一面——如果落定之后还读不到，那么一件跑在别的副本上
// 的活儿就永远交不出结果，这个包也就白写了。
func TestTheFinalOutputIsReadableFromAnyReplica(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "echo hi", nil))
	a.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted, Detail: "exit code: 0", Output: "hi\n"})

	read, err := b.registry.Read(t.Context(), id, nil)
	if err != nil {
		t.Fatalf("落定之后 B 副本该读得到那份最终输出：%v", err)
	}
	if read.Text != "hi\n" {
		t.Fatalf("读到的输出是 %q，该是 %q", read.Text, "hi\n")
	}
	if read.Snapshot.Status != jobs.StatusCompleted || read.Snapshot.Detail != "exit code: 0" {
		t.Fatalf("读到的状态是 %q/%q，该是 completed/exit code: 0", read.Snapshot.Status, read.Snapshot.Detail)
	}
	if !read.Snapshot.Reported {
		t.Fatal("一次终态读该把这件作业标成已汇报")
	}
	// 幂等：再读一次还是同一份，不会被消费掉。
	again, err := b.registry.Read(t.Context(), id, nil)
	if err != nil || again.Text != "hi\n" {
		t.Fatalf("最终输出该是幂等的，第二次读到 (%q, %v)", again.Text, err)
	}
}

// TestAKillOnAnotherReplicaIsAcceptedOnceTheJobIsFinished 钉住那条例外：
// 落定之后谁都停得了——因为已经没什么可停的了，那是一次被接受的空操作。
func TestAKillOnAnotherReplicaIsAcceptedOnceTheJobIsFinished(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "echo hi", nil))
	a.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted})

	result, err := b.registry.Kill(t.Context(), id, nil, "算了")
	if err != nil {
		t.Fatalf("停一件已经落定的作业不该报错：%v", err)
	}
	if result != jobs.KillAlreadyFinished {
		t.Fatalf("结果是 %q，该是 already-finished", result)
	}
}

// TestIdsDoNotCollideAcrossReplicas 钉住发号那条：号是抢来的，不是各算各的。
//
// 两个副本各起一件 bash，第二个必须换个号——它不能覆盖第一个，也不能拿到同一个 id。
func TestIdsDoNotCollideAcrossReplicas(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	first := a.start(newProducer().spec(jobs.KindBash, "一", nil))
	second := b.start(newProducer().spec(jobs.KindBash, "二", nil))

	if first != "bash-1" || second != "bash-2" {
		t.Fatalf("两个副本发出来的号是 %q 和 %q，该是 bash-1 和 bash-2", first, second)
	}
	if got := ids(a.list(nil)); len(got) != 2 {
		t.Fatalf("账本上该有两件作业，A 副本列出来的是 %v", got)
	}
}

// TestTheConcurrencyLimitIsSharedAcrossReplicas 钉住上限护的是属主不是机器。
func TestTheConcurrencyLimitIsSharedAcrossReplicas(t *testing.T) {
	t.Parallel()

	tune := func(config *Config) { config.MaxConcurrentJobsPerOwner = 2 }
	c := newCluster(t)
	a := c.join("replica-a", tune)
	b := c.join("replica-b", tune)

	a.start(newProducer().spec(jobs.KindBash, "一", nil))
	b.start(newProducer().spec(jobs.KindBash, "二", nil))

	// 名额在 A 上被占掉一个、在 B 上被占掉一个，账本上就是两个，谁都开不了第三件。
	if _, err := a.registry.Start(t.Context(), newProducer().spec(jobs.KindBash, "三", nil)); err == nil {
		t.Fatal("名额在账本上已经满了，A 副本不该再开得动")
	}
	if _, err := b.registry.Start(t.Context(), newProducer().spec(jobs.KindBash, "四", nil)); err == nil {
		t.Fatal("名额在账本上已经满了，B 副本也不该再开得动")
	}
}

// TestWaitPollsAJobRunningOnAnotherReplica 钉住跨副本那条等待路。
func TestWaitPollsAJobRunningOnAnotherReplica(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "sleep", nil))

	waited := make(chan jobs.Snapshot, 1)
	go func() {
		snapshot, err := b.registry.Wait(context.Background(), id, testWait, nil)
		if err != nil {
			return
		}
		waited <- snapshot
	}()

	a.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted, Detail: "exit code: 0"})
	select {
	case snapshot := <-waited:
		if snapshot.Status != jobs.StatusCompleted {
			t.Fatalf("B 副本等回来的状态是 %q，该是 completed", snapshot.Status)
		}
	case <-time.After(testWait):
		t.Fatal("B 副本轮询等待没等到那次结算")
	}
}

// TestWaitOnAForeignJobReturnsTheLiveSnapshotWhenItTimesOut 钉住跨副本等待的
// 超时出口：等到点不是错，交回当下那份快照。
func TestWaitOnAForeignJobReturnsTheLiveSnapshotWhenItTimesOut(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)

	id := a.start(newProducer().spec(jobs.KindBash, "sleep", nil))
	snapshot, err := b.registry.Wait(t.Context(), id, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("等到点不是错：%v", err)
	}
	if snapshot.Status != jobs.StatusRunning {
		t.Fatalf("等到点交回的状态是 %q，该是 running", snapshot.Status)
	}
}

// ---- 接管 ----

// TestStartupTakesOverJobsLeftByThePreviousProcess 钉住那条：一条盖着我自己
// runner、却还挂在 running 上的记录，只可能是上一次进程留下的。
func TestStartupTakesOverJobsLeftByThePreviousProcess(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	c.seed(Record{
		ID:        "bash-1",
		Kind:      jobs.KindBash,
		Runner:    "replica-a",
		Label:     "上一次进程留下的",
		Status:    jobs.StatusRunning,
		StartedAt: time.Now().Add(-time.Hour),
	})
	// 别的副本正跑着的那条不许碰。
	c.seed(Record{
		ID:        "bash-2",
		Kind:      jobs.KindBash,
		Runner:    "replica-b",
		Label:     "别人正跑着的",
		Status:    jobs.StatusRunning,
		StartedAt: time.Now().Add(-time.Hour),
	})

	a := c.join("replica-a", nil)
	mine := a.get("bash-1", nil)
	if mine.Status != jobs.StatusFailed {
		t.Fatalf("上一次进程留下的那条该被判成 failed，现在是 %q", mine.Status)
	}
	if mine.FinishedAt.IsZero() {
		t.Fatal("判成终态就必须盖上完成时刻")
	}
	theirs := a.get("bash-2", nil)
	if theirs.Status != jobs.StatusRunning {
		t.Fatalf("别的副本正跑着的那条不该被碰，现在是 %q", theirs.Status)
	}
}

// TestTakeoverFreesTheOwnerQuota 钉住不接管的那个后果：名额被永远占着。
func TestTakeoverFreesTheOwnerQuota(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	c.seed(Record{
		ID:        "bash-1",
		Kind:      jobs.KindBash,
		Runner:    "replica-a",
		Label:     "上一次进程留下的",
		Status:    jobs.StatusRunning,
		StartedAt: time.Now().Add(-time.Hour),
	})
	a := c.join("replica-a", func(config *Config) { config.MaxConcurrentJobsPerOwner = 1 })

	// 名额上限是 1，那条记录要是还占着，这一件就开不动。
	if _, err := a.registry.Start(t.Context(), newProducer().spec(jobs.KindBash, "新的", nil)); err != nil {
		t.Fatalf("接管之后名额该放开了：%v", err)
	}
}

// ---- 账本本身 ----

// TestStartLeavesNothingBehindWhenTheProducerFails 钉住那条补偿：记录先于活儿，
// 所以活儿起不来的时候那条记录必须被撤掉。
func TestStartLeavesNothingBehindWhenTheProducerFails(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	producer.startErr = errors.New("端口被占了")
	if _, err := a.registry.Start(t.Context(), producer.spec(jobs.KindBash, "起不来", nil)); err == nil {
		t.Fatal("生产方起不来，开工该失败")
	}
	if got := ids(a.list(nil)); len(got) != 0 {
		t.Fatalf("活儿没起来就不该在账本上留下任何东西，却留下了 %v", got)
	}
	// 号也该还给下一个人：撤掉的那条不占号。
	if id := a.start(newProducer().spec(jobs.KindBash, "这次起得来", nil)); id != "bash-1" {
		t.Fatalf("撤销之后重新发的号是 %q，该还是 bash-1", id)
	}
}

// TestSettlementLandsInTheLedger 钉住结局是落在账本上的，不是留在进程内存里。
//
// 断言绕开注册表直接看介质：这两条路要是能给出不同的答案，那份「跨副本可读」
// 就是假的。
func TestSettlementLandsInTheLedger(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "echo hi", nil))
	a.finish(producer, jobs.Outcome{Status: jobs.StatusCompleted, Output: "hi\n"})

	records := c.medium.Table(DomainName, TableName)
	raw, ok := records[string(id)]
	if !ok {
		t.Fatalf("介质上该有 %s 这条记录，有的是 %v", id, records)
	}
	if !strings.Contains(string(raw), `"status":"completed"`) {
		t.Fatalf("介质上那条记录没记下终态：%s", raw)
	}
	if !strings.Contains(string(raw), `"output":"hi\n"`) {
		t.Fatalf("介质上那条记录没记下最终输出：%s", raw)
	}
}

// TestListOrdersByStartTimeNotByKey 钉住那条顺序：键是字典序的，`bash-10` 会排在
// `bash-2` 前面，而 [jobs.Registry.List] 要的是登记顺序。
func TestListOrdersByStartTimeNotByKey(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	// 要开到 bash-11 才能让字典序和登记序分岔，所以上限得抬过默认的 10。
	a := c.join("replica-a", func(config *Config) { config.MaxConcurrentJobsPerOwner = 32 })

	var expected []jobs.JobID
	for i := 0; i < 11; i++ {
		expected = append(expected, a.start(newProducer().spec(jobs.KindBash, "一件", nil)))
	}
	if got := ids(a.list(nil)); !slicesEqual(got, expected) {
		t.Fatalf("列出来的顺序是 %v，该是 %v", got, expected)
	}
}

func slicesEqual(left, right []jobs.JobID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// ---- 围墙 ----

// TestTheOwnerFenceHoldsAcrossReplicas 钉住围墙：id 是可预测的，边界只有属主授权。
func TestTheOwnerFenceHoldsAcrossReplicas(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)
	b := c.join("replica-b", nil)
	mine := c.newOwner("session-mine")
	yours := c.newOwner("session-yours")

	id := a.start(newProducer().spec(jobs.KindBash, "我的活儿", mine))

	if _, err := b.registry.Get(t.Context(), id, yours); err == nil {
		t.Fatal("另一个会话不该够得着这件作业")
	}
	if got := ids(b.list(yours)); len(got) != 0 {
		t.Fatalf("另一个会话的列表里不该有别人的作业，列出来的是 %v", got)
	}
	if got := ids(b.list(mine)); len(got) != 1 || got[0] != id {
		t.Fatalf("属主自己在别的副本上该看得见它，列出来的是 %v", got)
	}
}

// TestAKindWithADashIsRefused 钉住那条歧义闸：id 是 `<种类>-N`，种类里再带横杠
// 就没法把序号解回来了，见 [Registry.admit]。
func TestAKindWithADashIsRefused(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	_, err := a.registry.Start(t.Context(), newProducer().spec("my-kind", "带横杠的种类", nil))
	if err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("带横杠的种类该被拒，收到 %v", err)
	}
}

// ---- 本地那条路 ----

// TestKillOnTheOwningReplicaReachesTheProducer 钉住握着句柄的那个副本是真的停得了。
func TestKillOnTheOwningReplicaReachesTheProducer(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	// 这一条要看的正是取消回来、结局还没到的那个中间态。
	producer.keepRunningAfterCancel = true
	id := a.start(producer.spec(jobs.KindBash, "sleep", nil))

	result, err := a.registry.Kill(t.Context(), id, nil, "用户不要了")
	if err != nil {
		t.Fatalf("本副本停自己的活儿不该失败：%v", err)
	}
	if result != jobs.KillRequested {
		t.Fatalf("结果是 %q，该是 requested", result)
	}
	if reasons := producer.cancelReasons(); len(reasons) != 1 || reasons[0] != "用户不要了" {
		t.Fatalf("生产方收到的取消理由是 %v", reasons)
	}
	snapshot := a.get(id, nil)
	if snapshot.Status != jobs.StatusStopping || !snapshot.Reported {
		t.Fatalf("kill 之后该是 stopping 且已汇报，现在是 %q/%v", snapshot.Status, snapshot.Reported)
	}
	// 看完那个中间态就把结局补上，否则清理要一直等到超时。
	a.finish(producer, jobs.Outcome{Status: jobs.StatusKilled, Detail: "停住了"})
}

// TestStreamingOutputComesFromTheProducerNotTheLedger 钉住流式那条路：中间过程
// 是一条会被消费掉的游标，它从来不进账本。
func TestStreamingOutputComesFromTheProducerNotTheLedger(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	producer.streaming = true
	producer.stream = []string{"第一段", "第二段"}
	id := a.start(producer.spec(jobs.KindBash, "tail -f", nil))

	first, err := a.registry.Read(t.Context(), id, nil)
	if err != nil || first.Text != "第一段" {
		t.Fatalf("第一次读到 (%q, %v)", first.Text, err)
	}
	second, err := a.registry.Read(t.Context(), id, nil)
	if err != nil || second.Text != "第二段" {
		t.Fatalf("第二次读到 (%q, %v)", second.Text, err)
	}

	records := c.medium.Table(DomainName, TableName)
	if strings.Contains(string(records[string(id)]), "第一段") {
		t.Fatalf("流式的中间过程不该进账本：%s", records[string(id)])
	}
}

// TestWaitOnTheOwningReplicaClaimsTheReport 钉住本副本那条零轮询的等待路，
// 以及它对那次汇报的认领。
func TestWaitOnTheOwningReplicaClaimsTheReport(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "sleep", nil))

	waited := make(chan jobs.Snapshot, 1)
	go func() {
		snapshot, err := a.registry.Wait(context.Background(), id, testWait, nil)
		if err != nil {
			return
		}
		waited <- snapshot
	}()

	// 等这个等待者真的挂上去，否则「结算时有人在等」这条断言压不住任何东西。
	deadline := time.Now().Add(testWait)
	for {
		if handle := a.registry.readyHandle(id); handle != nil {
			a.registry.mutex.Lock()
			waiting := handle.waiters
			a.registry.mutex.Unlock()
			if waiting > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("等待者一直没挂上去")
		}
		time.Sleep(time.Millisecond)
	}

	producer.done <- jobs.Outcome{Status: jobs.StatusCompleted}
	select {
	case snapshot := <-waited:
		if snapshot.Status != jobs.StatusCompleted {
			t.Fatalf("等回来的状态是 %q", snapshot.Status)
		}
		if !snapshot.Reported {
			t.Fatal("结算时还有人在等，这件作业就该当场标成已汇报")
		}
	case <-time.After(testWait):
		t.Fatal("本副本的等待没被结算放开")
	}
}

// TestDisposeCancelsLocalJobsAndKeepsTheirRecords 钉住拆除那条，以及它和
// localjobs 分岔的地方：记录不摘，见 [Registry.disposeOwned]。
func TestDisposeCancelsLocalJobsAndKeepsTheirRecords(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	a := c.join("replica-a", nil)

	producer := newProducer()
	id := a.start(producer.spec(jobs.KindBash, "sleep", nil))

	if err := a.registry.Dispose(t.Context()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	// 域已经关了，所以从这个副本读不到了；换一个副本去看那条记录还在不在。
	b := c.join("replica-b", nil)
	snapshot := b.get(id, nil)
	if snapshot.Status != jobs.StatusKilled {
		t.Fatalf("拆除之后那条记录该是 killed，现在是 %q", snapshot.Status)
	}
}

// TestConfigRefusesAnAssemblyItCannotHonour 钉住装配面那几条规矩。
func TestConfigRefusesAnAssemblyItCannotHonour(t *testing.T) {
	t.Parallel()

	c := newCluster(t)
	cases := []struct {
		name   string
		config Config
	}{
		{"没给执行副本标识", Config{Facility: c.facility()}},
		{"没给域设施", Config{Runner: "replica-a"}},
		{"并发上限是负数", Config{Runner: "replica-a", Facility: c.facility(), MaxConcurrentJobsPerOwner: -1}},
		{"轮询间隔是负数", Config{Runner: "replica-a", Facility: c.facility(), ForeignPollInterval: -time.Second}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(t.Context(), item.config); err == nil {
				t.Fatal("这份装配该被拒")
			}
		})
	}
}
