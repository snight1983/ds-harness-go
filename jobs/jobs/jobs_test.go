// 本文件的作用：把这条缝自己带的那两样东西钉住——终态判定，以及那条作业快照
// 不变量（包括它装载那一刻就要走一遍历史、注销之后就必须闭嘴这两条）。
//
// # 这些测试防的是什么错
//
//   - **终态集合写漏一个**。stopping 被当成终态，等待者会被提前放开；一个真的终态
//     被漏掉，作业永远结算不了。
//   - **id 不是注册表发的那个形状**。id 是可预测的，边界靠属主授权；一个形状不对的
//     id 说明发号那一段坏了，而授权正建在它身上。
//   - **finishedAt 和状态对不上**。一件说自己 running 却盖了完成时刻的作业，
//     会让「按时刻算耗时」和「按状态判活着」两种读法各说各话。
//   - **属主会话和结算属主对不上**。围墙就是这个字段，它错了等于围墙开在别处。
//   - **注销之后还在查**。一条不该再查的检查继续在别人的结算路径上抛，会把一台
//     好好的注册表拖垮。
package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/invariants"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包只读它的 ID，别的方法全是哑的。
type stubAgent struct {
	id session.SessionID
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return nil }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubRegistry 是一台只做记账的假注册表：不变量伴生只用得到 List 和 OnJobDone，
// 剩下七个方法在这里就是为了满足 [Registry] 这个接口。
type stubRegistry struct {
	// unowned 是 List(nil) 交回的那几行。
	unowned []Snapshot
	// subscribeErr 不为 nil 时订阅完成失败。
	subscribeErr error
	// listeners 是还挂着的那些完成监听器。
	listeners []DoneListener
	// owners 记下每一次订阅交进来的那个作用域。
	owners []*scope.Scope
	// disposed 记下退订被调了几次。
	disposed int
}

func (r *stubRegistry) List(caller agent.Agent) []Snapshot {
	if caller != nil {
		return nil
	}
	return r.unowned
}

func (r *stubRegistry) OnJobDone(
	_ context.Context,
	owner *scope.Scope,
	listener DoneListener,
) (func(context.Context) error, error) {
	if r.subscribeErr != nil {
		return nil, r.subscribeErr
	}
	r.owners = append(r.owners, owner)
	r.listeners = append(r.listeners, listener)
	return func(context.Context) error {
		r.disposed++
		r.listeners = nil
		return nil
	}, nil
}

// announce 把一次结算推给所有还挂着的监听器。
func (r *stubRegistry) announce(snapshot Snapshot, owner agent.Agent) {
	for _, listener := range r.listeners {
		listener(snapshot, owner)
	}
}

func (r *stubRegistry) Start(Start) (JobID, error)               { return "", nil }
func (r *stubRegistry) Get(JobID, agent.Agent) (Snapshot, error) { return Snapshot{}, nil }
func (r *stubRegistry) Read(JobID, agent.Agent) (Read, error)    { return Read{}, nil }
func (r *stubRegistry) Kill(JobID, agent.Agent, string) (KillResult, error) {
	return KillRequested, nil
}

func (r *stubRegistry) Wait(
	context.Context, JobID, time.Duration, agent.Agent,
) (Snapshot, error) {
	return Snapshot{}, nil
}

func (r *stubRegistry) OnJobsChanged(
	context.Context, *scope.Scope, ChangedListener,
) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

func (r *stubRegistry) AttachController(
	context.Context, *scope.Scope, string,
) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

// ---- 脚手架 ----

// startedAt 是所有快照共用的那个开工时刻；具体是哪一刻不重要，非零才重要。
var startedAt = time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)

// healthy 造一份挑不出毛病的快照，让每个用例只改自己要坏的那一处。
func healthy() Snapshot {
	return Snapshot{
		ID:        "bash-1",
		Kind:      KindBash,
		Label:     "ls -la",
		Status:    StatusRunning,
		StartedAt: startedAt,
	}
}

// newRegistry 造一个全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// violation 跑一段会违例的代码，交出那条违例。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func violation(t *testing.T, run func()) *invariants.Error {
	t.Helper()
	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		run()
	}()
	if caught == nil {
		t.Fatal("该抛出一条违例")
	}
	return caught
}

// ---- 状态 ----

func TestOnlyTheThreeSettledStatusesAreTerminal(t *testing.T) {
	t.Parallel()
	// running 和 stopping 都还没落定：把 stopping 当终态会提前放开等待者。
	for _, status := range []JobStatus{StatusCompleted, StatusKilled, StatusFailed} {
		if !status.IsTerminal() {
			t.Errorf("%q 该是终态", status)
		}
	}
	for _, status := range []JobStatus{StatusRunning, StatusStopping, ""} {
		if status.IsTerminal() {
			t.Errorf("%q 不该是终态", status)
		}
	}
}

// ---- 快照校验 ----

func TestValidateSnapshotAcceptsAWellFormedRecord(t *testing.T) {
	t.Parallel()
	// 活着的无主作业：不填属主会话，也不盖完成时刻。
	if err := ValidateSnapshot(healthy(), nil); err != nil {
		t.Fatalf("一份干净的快照不该报错：%v", err)
	}

	// 落定的有主作业：属主会话对得上，完成时刻不早于开工。
	settled := healthy()
	settled.OwnerSession = "session-7"
	settled.Status = StatusCompleted
	settled.FinishedAt = startedAt
	if err := ValidateSnapshot(settled, &stubAgent{id: "session-7"}); err != nil {
		t.Fatalf("一份落定的快照不该报错：%v", err)
	}
}

func TestValidateSnapshotRejectsEveryMalformedRecord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*Snapshot)
		owner   agent.Agent
		message string
	}{
		{
			name:    "id 少了种类前缀",
			mutate:  func(s *Snapshot) { s.ID = "subagent-1" },
			message: "must be \"bash-\" followed by a positive ordinal",
		},
		{
			name:    "种类是空的",
			mutate:  func(s *Snapshot) { s.Kind = ""; s.ID = "-1" },
			message: "followed by a positive ordinal",
		},
		{
			name:    "序号不是整数",
			mutate:  func(s *Snapshot) { s.ID = "bash-1.5" },
			message: "followed by a positive ordinal",
		},
		{
			name:    "序号从 0 起",
			mutate:  func(s *Snapshot) { s.ID = "bash-0" },
			message: "followed by a positive ordinal",
		},
		{
			name:    "序号没写",
			mutate:  func(s *Snapshot) { s.ID = "bash-" },
			message: "followed by a positive ordinal",
		},
		{
			name:    "标签是空的",
			mutate:  func(s *Snapshot) { s.Label = "" },
			message: "label must be non-empty",
		},
		{
			name:    "开工时刻没盖",
			mutate:  func(s *Snapshot) { s.StartedAt = time.Time{} },
			message: "startedAt must be set",
		},
		{
			name:    "活着却盖了完成时刻",
			mutate:  func(s *Snapshot) { s.FinishedAt = startedAt.Add(time.Second) },
			message: "finishedAt must be present exactly for a terminal status",
		},
		{
			name:    "落定却没盖完成时刻",
			mutate:  func(s *Snapshot) { s.Status = StatusFailed },
			message: "finishedAt must be present exactly for a terminal status",
		},
		{
			name: "完成时刻早于开工",
			mutate: func(s *Snapshot) {
				s.Status = StatusKilled
				s.FinishedAt = startedAt.Add(-time.Second)
			},
			message: "finishedAt must be no earlier than startedAt",
		},
		{
			name:    "无主作业却填了属主会话",
			mutate:  func(s *Snapshot) { s.OwnerSession = "session-7" },
			message: "ownerSession does not match its completion owner",
		},
		{
			name:    "属主会话和结算属主对不上",
			mutate:  func(s *Snapshot) { s.OwnerSession = "session-7" },
			owner:   &stubAgent{id: "session-8"},
			message: "ownerSession does not match its completion owner",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			snapshot := healthy()
			testCase.mutate(&snapshot)
			err := ValidateSnapshot(snapshot, testCase.owner)
			if err == nil {
				t.Fatal("该报一条违例")
			}
			if !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("违例说的是 %q，该提到 %q", err, testCase.message)
			}
		})
	}
}

// ---- 不变量登记 ----

func TestRegisterInvariantsRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil, &stubRegistry{}, scope.NewRoot()); err == nil {
		t.Fatal("没有不变量注册表时该拒绝")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有作业注册表时该拒绝")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), &stubRegistry{}, nil); err == nil {
		t.Fatal("没有作用域时该拒绝")
	}
}

func TestTheInvariantCatchesABadRecordAlreadyInTheRegistry(t *testing.T) {
	t.Parallel()
	// 一台带着坏记录起来的注册表，必须在装载这一刻就响，而不是等下一次结算。
	broken := healthy()
	broken.Label = ""
	jobs := &stubRegistry{unowned: []Snapshot{healthy(), broken}}

	failure := violation(t, func() {
		_, _ = RegisterInvariants(t.Context(), newRegistry(t), jobs, scope.NewRoot())
	})
	if failure.PackageName != PackageName {
		t.Fatalf("违例记在 %q 名下，该是 %q", failure.PackageName, PackageName)
	}
	if !strings.Contains(failure.Message, "label must be non-empty") {
		t.Fatalf("违例说的是 %q", failure.Message)
	}
}

func TestTheInvariantChecksEverySettlementUntilItIsUnregistered(t *testing.T) {
	t.Parallel()
	jobs := &stubRegistry{}
	owner := &stubAgent{id: "session-7"}

	global := scope.NewRoot()
	undo, err := RegisterInvariants(t.Context(), newRegistry(t), jobs, global)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	// 交进去的那个无身份作用域要原样落到订阅上：落在全局层才罩得住每一个属主。
	if len(jobs.owners) != 1 || jobs.owners[0] != global {
		t.Fatalf("订阅拿到的作用域不是交进去的那一个")
	}

	settled := healthy()
	settled.OwnerSession = "session-7"
	settled.Status = StatusCompleted
	settled.FinishedAt = startedAt
	jobs.announce(settled, owner)

	// 属主会话和结算属主对不上就得响。
	settled.OwnerSession = "session-9"
	failure := violation(t, func() { jobs.announce(settled, owner) })
	if !strings.Contains(failure.Message, "ownerSession does not match") {
		t.Fatalf("违例说的是 %q", failure.Message)
	}

	// 注销之后一条不该再查的检查绝不许继续抛。
	undo()
	if jobs.disposed != 1 {
		t.Fatalf("退订被调了 %d 次，该是 1 次", jobs.disposed)
	}
	jobs.announce(settled, owner)
}

func TestTheInvariantPassesTheOwnerScopeThroughToTheSubscription(t *testing.T) {
	t.Parallel()
	// 圈定了作用域的登记，那把钥匙必须原样交给注册表——可见范围是显式的，不是猜的。
	jobs := &stubRegistry{}
	owner, err := scope.New(scope.NewKey("jobs-invariant"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), jobs, owner); err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	if len(jobs.owners) != 1 || jobs.owners[0] != owner {
		t.Fatalf("订阅拿到的作用域不是交进去的那一个")
	}
}

func TestTheInvariantSurfacesASubscriptionFailure(t *testing.T) {
	t.Parallel()
	// 订阅不上就等于这条检查装不上：宁可拒绝装载，也不许留下一条只查了历史的假检查。
	refused := errors.New("订阅被拒")
	_, err := RegisterInvariants(t.Context(), newRegistry(t), &stubRegistry{subscribeErr: refused}, scope.NewRoot())
	if !errors.Is(err, refused) {
		t.Fatalf("报的是 %v，该带上订阅那条错", err)
	}
}
