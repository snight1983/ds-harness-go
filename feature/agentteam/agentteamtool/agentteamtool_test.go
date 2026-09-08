// 本文件的作用：把这十件工具钉在它们那几条真会出错的边上——那道成员资格闸、
// 起队友的队长闸、等待那两步的先后、翻页那两个数，以及输出的形状。
//
// # 这些测试防的是什么错
//
//   - **一个不属于这支团队的会话读到了花名册**。那份名单里带着发消息和改派任务要用的
//     名字，泄出去等于把另一支团队的收件箱交了出去。
//   - **一个队友起了队友**。agentteam.Service.Spawn 收的是「谁当队长」，队友叫它不会
//     报错，会另起一支以自己为队长的团队——这道闸只在本包这一层，塌了没人接得住。
//   - **等待那两步的先后颠倒**。先短路再验时限的话，一次带着非法时限的调用永远
//     看不见自己把时限写错了。
//   - **短路那一支还是去等了**。没有活着的同侪时占着一个回合空等，队长就卡在那儿。
//   - **翻页把最后一页也带上 nextCursor**。模型会照着它再翻一次，拿回一页空的。
//   - **输出被 HTML 转义**。任务标题和队友职责是人写的自由文本，`<` 变成 `&lt;`
//     之后模型看见的是一句和原文长得不一样的话。
//   - **diagnostics 这个键时有时无**。模型每一轮看见的键集合必须一样，否则它得自己去
//     分辨「没有这个键」和「这个键是空的」。
//   - **半装上去**。模型手上有一件起得了队友、却没有那段指引管着的工具，而那段指引
//     正是「什么时候才该起队友」的唯一说明。

package agentteamtool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// teamID 是每个用例里那支团队的身份。
const teamID agentteam.TeamID = "team-1"

// ---- 假件 ----

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent。
type stubAgent struct {
	id  sessionlog.SessionID
	own *scope.Scope
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusRunning }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Followup(llm.Message)                                      {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}
func (a *stubAgent) Remove(llm.MessageID)                                      {}
func (a *stubAgent) Replace(llm.MessageID, llm.Message)                        {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// waitCall 是 [stubService.Wait] 收到的那一次。
type waitCall struct {
	who     agentteam.Caller
	timeout time.Duration
}

// stubService 是那台记账的假团队服务。
//
// 它**不**复刻团队服务的行为，只记下自己被怎么调的：本包的活儿是把模型给的参数
// 折成一次调用，再把结果折回给模型看的样子，那台服务自己做什么归它自己的用例管。
type stubService struct {
	// caller 是解算发起人交回的那一位。
	caller agentteam.Caller
	// callerErr 不为 nil 时解算发起人一律失败。
	callerErr error

	// member 是起队友和列花名册交回的那一行。
	member agentteam.MemberView
	// taskView 是那几件任务工具交回的那一条。
	taskView agentteam.TaskView
	// taskViews 是列任务交回的那一份。
	taskViews []agentteam.TaskView
	// peers 是数同侪交回的那个数。
	peers int
	// waitResult 是等待交回的那一份。
	waitResult agentteam.WaitResult
	// sendResult 是发信交回的那一份。
	sendResult agentteam.SendResult
	// previous 是打断交回的那一档。
	previous agentteam.MemberStatus

	// 这几个非 nil 时对应那条路直接失败。
	spawnErr error
	sendErr  error
	waitErr  error

	// 这几份是记下来的调用。
	spawns  []agentteam.SpawnRequest
	sends   []agentteam.SendRequest
	waits   []waitCall
	creates []agentteam.CreateTaskRequest
	updates []agentteam.UpdateTaskRequest
}

func (s *stubService) ResolveCaller(
	context.Context, agentteam.TeamID, sessionlog.SessionID,
) (agentteam.Caller, error) {
	if s.callerErr != nil {
		return agentteam.Caller{}, s.callerErr
	}
	return s.caller, nil
}

func (s *stubService) Spawn(
	_ context.Context, _ sessionlog.SessionID, request agentteam.SpawnRequest,
) (agentteam.MemberView, error) {
	s.spawns = append(s.spawns, request)
	if s.spawnErr != nil {
		return agentteam.MemberView{}, s.spawnErr
	}
	return s.member, nil
}

func (s *stubService) Members(context.Context, agentteam.TeamID) ([]agentteam.MemberView, error) {
	return []agentteam.MemberView{s.member}, nil
}

func (s *stubService) Interrupt(
	context.Context, agentteam.Caller, string,
) (agentteam.MemberStatus, error) {
	return s.previous, nil
}

func (s *stubService) Send(
	_ context.Context, _ agentteam.Caller, request agentteam.SendRequest,
) (agentteam.SendResult, error) {
	s.sends = append(s.sends, request)
	if s.sendErr != nil {
		return agentteam.SendResult{}, s.sendErr
	}
	return s.sendResult, nil
}

func (s *stubService) Wait(
	_ context.Context, who agentteam.Caller, timeout time.Duration,
) (agentteam.WaitResult, error) {
	s.waits = append(s.waits, waitCall{who: who, timeout: timeout})
	if s.waitErr != nil {
		return agentteam.WaitResult{}, s.waitErr
	}
	return s.waitResult, nil
}

func (s *stubService) ActivePeers(context.Context, agentteam.Caller) (int, error) {
	return s.peers, nil
}

func (s *stubService) CreateTask(
	_ context.Context, _ agentteam.Caller, request agentteam.CreateTaskRequest,
) (agentteam.TaskView, error) {
	s.creates = append(s.creates, request)
	return s.taskView, nil
}

func (s *stubService) Tasks(context.Context, agentteam.TeamID) ([]agentteam.TaskView, error) {
	return s.taskViews, nil
}

func (s *stubService) Task(
	context.Context, agentteam.TeamID, agentteam.TaskID,
) (agentteam.TaskView, error) {
	return s.taskView, nil
}

func (s *stubService) UpdateTask(
	_ context.Context, _ agentteam.Caller, request agentteam.UpdateTaskRequest,
) (agentteam.TaskView, error) {
	s.updates = append(s.updates, request)
	return s.taskView, nil
}

// ---- 装配 ----

// world 是一次用例要的全部家当。
type world struct {
	t          *testing.T
	root       *scope.Scope
	agentScope *scope.Scope
	service    *stubService
	caller     *stubAgent
	tools      *tools.Runtime
	prompts    *systemprompt.Registry
}

// scopeOf 造一个有身份的作用域，用完自动释放。
func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	owner, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %s 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := scopeOf(t, "root", nil)
	agentScope := scopeOf(t, "agent", root)
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompts, err := systemprompt.NewRegistry(t.Context(), root, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	return &world{
		t:          t,
		root:       root,
		agentScope: agentScope,
		caller:     &stubAgent{id: "session-lead", own: agentScope},
		tools:      runtime,
		prompts:    prompts,
		service: &stubService{
			caller: agentteam.Caller{Team: teamID, ID: "session-lead", Name: agentteam.LeadName, Lead: true},
			member: agentteam.MemberView{
				ID: "session-worker", Name: "worker", Role: agentteam.RoleTeammate,
				Status: agentteam.StatusIdle,
			},
			taskView: agentteam.TaskView{
				Task: agentteam.Task{
					ID: "task-1", Rev: 3, Subject: "做这件", Description: "做完它",
					Status: agentteam.TaskPending,
				},
			},
			sendResult: agentteam.SendResult{Message: "message-1", Phase: agentteam.MessageQueued},
			previous:   agentteam.StatusRunning,
		},
	}
}

// agentOf 是那条从钥匙查回 agent 的路：只认这个世界那把 agent 钥匙。
func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key != w.agentScope.Key() {
		return nil, errors.New("这把钥匙不属于任何一个 agent")
	}
	return w.caller, nil
}

func (w *world) config() Config {
	return Config{Service: w.service, Team: teamID, AgentOf: w.agentOf}
}

func (w *world) deps() Deps { return Deps{Tools: w.tools, Prompts: w.prompts} }

// controller 造一个控制器。
func (w *world) controller(shape func(*Config)) *Controller {
	w.t.Helper()
	config := w.config()
	if shape != nil {
		shape(&config)
	}
	controller, err := New(config)
	if err != nil {
		w.t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

// execOn 造一份落在这个世界那把 agent 钥匙上的执行上下文。
func (w *world) execOn() *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: w.agentScope.Key()},
	}}
}

// encode 把一份入参排成字节。
func (w *world) encode(args any) json.RawMessage {
	w.t.Helper()
	if raw, ok := args.(json.RawMessage); ok {
		return raw
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	return encoded
}

// call 直接调一条工具体。
func (w *world) call(
	body func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error),
	args any,
) (json.RawMessage, error) {
	w.t.Helper()
	return body(w.t.Context(), w.encode(args), w.execOn())
}

// decode 把一份结果解回某种形状。
func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("解结果失败：%v（%s）", err, raw)
	}
	return value
}

// expectCode 断言这次失败带的是某个确切的码。
func expectCode(t *testing.T, err error, want agentteam.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("本该被拒（%s），却成功了", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("报的是 %v，本该是 %s", err, want)
	}
}

// ---- 成员资格 ----

// TestEveryToolResolvesTheCallerFirst 钉住那道成员资格闸：十件工具一件都不许在
// 解算发起人之前动那台服务。
func TestEveryToolResolvesTheCallerFirst(t *testing.T) {
	t.Parallel()

	bodies := map[string]func(*Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error){
		SpawnTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.spawn
		},
		ListAgentsTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.listAgents
		},
		WaitTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.wait
		},
		InterruptTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.interrupt
		},
		TaskCreateTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.createTask
		},
		TaskListTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.listTasks
		},
		TaskGetTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.getTask
		},
		TaskUpdateTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return c.updateTask
		},
		SendMessageTool: func(c *Controller) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return func(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
				return c.send(ctx, args, exec, SendMessageTool, agentteam.DeliveryQuiet)
			}
		},
	}
	for name, pick := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.service.callerErr = &agentteam.Error{Code: agentteam.CodeNotMember, Message: "不是这支团队的人"}
			if _, err := w.call(pick(w.controller(nil)), json.RawMessage(`{}`)); !errors.Is(err, agentteam.CodeNotMember) {
				t.Fatalf("非成员该被拦在 %s 外面，拿到 %v", name, err)
			}
		})
	}
}

// TestToolsNeedACallingAgent 钉住那条「没落在 agent 上就没权可凭」。
func TestToolsNeedACallingAgent(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	if _, err := controller.listAgents(t.Context(), json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("没有执行上下文还能列花名册")
	}
	empty := &tools.RunContext{}
	if _, err := controller.listAgents(t.Context(), json.RawMessage(`{}`), empty); err == nil {
		t.Fatal("执行上下文没落在 agent 上还能列花名册")
	}
}

// ---- 起队友 ----

// TestSpawnIsLeadOnly 钉住那道只在本包这一层的队长闸：agentteam.Service.Spawn 收的是
// 「谁当队长」，队友叫它会另起一支以自己为队长的团队，而不是报错。
func TestSpawnIsLeadOnly(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.caller = agentteam.Caller{Team: teamID, ID: "session-worker", Name: "worker"}
	_, err := w.call(w.controller(nil).spawn, spawnArgs{Name: "helper", Description: "帮忙", Prompt: "开工"})
	expectCode(t, err, agentteam.CodeLeadRequired)
	if len(w.service.spawns) != 0 {
		t.Fatalf("被拒了还去起了队友：%+v", w.service.spawns)
	}
}

// TestSpawnPicksTheProviderByContext 钉住那条上文来源决定提供方：fork 那一支要从队长
// 此刻的上文分一支，和全新开一个会话不是同一条起法。
func TestSpawnPicksTheProviderByContext(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		given    string
		provider string
		source   agentteam.MemberContext
	}{
		"没写就当全新": {given: "", provider: DefaultFreshProvider, source: agentteam.ContextFresh},
		"写了全新":   {given: string(agentteam.ContextFresh), provider: DefaultFreshProvider, source: agentteam.ContextFresh},
		"写了分一支":  {given: string(agentteam.ContextFork), provider: DefaultForkProvider, source: agentteam.ContextFork},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			_, err := w.call(w.controller(nil).spawn, spawnArgs{
				Name: "helper", Description: "帮忙", Prompt: "开工", Context: want.given,
			})
			if err != nil {
				t.Fatalf("起队友不该失败：%v", err)
			}
			got := w.service.spawns[0]
			if got.Provider != want.provider || got.Context != want.source {
				t.Fatalf("该用 %s/%s，拿到 %s/%s", want.provider, want.source, got.Provider, got.Context)
			}
		})
	}
}

// ---- 收件箱 ----

// TestSendCarriesTheDelivery 钉住那两件共用一个体的工具各自的送法。
func TestSendCarriesTheDelivery(t *testing.T) {
	t.Parallel()

	cases := map[string]agentteam.Delivery{
		SendMessageTool: agentteam.DeliveryQuiet,
		FollowupTool:    agentteam.DeliveryWakeup,
	}
	for name, delivery := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			controller := w.controller(nil)
			raw, err := controller.send(
				t.Context(),
				w.encode(messageArgs{Target: "worker", Message: "接着干"}),
				w.execOn(), name, delivery,
			)
			if err != nil {
				t.Fatalf("发消息不该失败：%v", err)
			}
			if got := w.service.sends[0].Delivery; got != delivery {
				t.Fatalf("该按 %s 送，拿到 %s", delivery, got)
			}
			wire := decode[sendWire](t, raw)
			if wire.Status != statusQueued || wire.MessageID != "message-1" {
				t.Fatalf("结果不对：%+v", wire)
			}
		})
	}
}

// TestSendStatusMapsBothTerminalPhases 钉住那两档终态各自译成哪个字。
func TestSendStatusMapsBothTerminalPhases(t *testing.T) {
	t.Parallel()

	if got := sendStatus(agentteam.MessageDelivered); got != statusAccepted {
		t.Fatalf("送到了该报 %s，拿到 %s", statusAccepted, got)
	}
	if got := sendStatus(agentteam.MessageQueued); got != statusQueued {
		t.Fatalf("还排着该报 %s，拿到 %s", statusQueued, got)
	}
}

// ---- 等待 ----

// TestWaitChecksTheTimeoutBeforeShortCircuiting 钉住那条次序。反过来的话，一次带着
// 非法时限的调用会先拿到那条「没人可等」的短路，于是模型永远看不见自己把时限写错了。
func TestWaitChecksTheTimeoutBeforeShortCircuiting(t *testing.T) {
	t.Parallel()

	for _, millis := range []float64{0, 9_999, 3_600_001, 30_000.5} {
		w := newWorld(t)
		w.service.peers = 0
		_, err := w.call(w.controller(nil).wait, waitArgs{TimeoutMillis: &millis})
		expectCode(t, err, agentteam.CodeInvalidTimeout)
		var failure *agentteam.Error
		if errors.As(err, &failure) && failure.Message != invalidTimeout {
			t.Fatalf("那句话必须和 DSH 一模一样，拿到 %q", failure.Message)
		}
	}
}

// TestWaitShortCircuitsWithoutPeers 钉住那条短路：没有活着的同侪就当场回话，
// 不去占着一个回合空等。
func TestWaitShortCircuitsWithoutPeers(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.peers = 0
	raw, err := w.call(w.controller(nil).wait, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("等待不该失败：%v", err)
	}
	wire := decode[waitWire](t, raw)
	if wire.NoProgress == nil || wire.NoProgress.Reason != noActivePeerReason {
		t.Fatalf("该短路，拿到 %+v", wire)
	}
	if wire.NoProgress.Message != noActivePeerMessage {
		t.Fatalf("那句话必须和 DSH 一模一样，拿到 %q", wire.NoProgress.Message)
	}
	if len(w.service.waits) != 0 {
		t.Fatalf("短路了还去等了：%+v", w.service.waits)
	}
}

// TestWaitDefaultsToThirtySeconds 钉住那条默认时限，并钉住真等待那一支不带短路说明。
func TestWaitDefaultsToThirtySeconds(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.peers = 1
	w.service.waitResult = agentteam.WaitResult{TimedOut: true}
	raw, err := w.call(w.controller(nil).wait, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("等待不该失败：%v", err)
	}
	if got := w.service.waits[0].timeout; got != defaultTimeoutMillis*time.Millisecond {
		t.Fatalf("没写时限该等 %d 毫秒，拿到 %s", defaultTimeoutMillis, got)
	}
	wire := decode[waitWire](t, raw)
	if !wire.TimedOut || wire.NoProgress != nil {
		t.Fatalf("到点那一支不该带短路说明：%+v", wire)
	}
}

// ---- 任务板 ----

// TestListTasksFiltersAndPages 钉住那三个筛子和翻页那两个数。
func TestListTasksFiltersAndPages(t *testing.T) {
	t.Parallel()

	views := []agentteam.TaskView{
		{Task: agentteam.Task{ID: "t1", Status: agentteam.TaskPending}, Ready: true},
		{Task: agentteam.Task{ID: "t2", Status: agentteam.TaskInProgress}, OwnerName: "worker"},
		{Task: agentteam.Task{ID: "t3", Status: agentteam.TaskPending}, Ready: true},
		{Task: agentteam.Task{ID: "t4", Status: agentteam.TaskCompleted}, OwnerName: "worker", Ready: true},
	}
	pending := string(agentteam.TaskPending)
	owner := "worker"
	unowned := unownedFilter
	ready := true
	one := float64(1)

	cases := map[string]struct {
		args listTaskArgs
		want []string
		next *int
	}{
		"不筛就全给":   {args: listTaskArgs{}, want: []string{"t1", "t2", "t3", "t4"}},
		"按状态筛":    {args: listTaskArgs{Status: &pending}, want: []string{"t1", "t3"}},
		"按主人筛":    {args: listTaskArgs{Owner: &owner}, want: []string{"t2", "t4"}},
		"按没主筛":    {args: listTaskArgs{Owner: &unowned}, want: []string{"t1", "t3"}},
		"按就绪筛":    {args: listTaskArgs{Ready: &ready}, want: []string{"t1", "t3", "t4"}},
		"翻一页留个坐标": {args: listTaskArgs{Limit: &one}, want: []string{"t1"}, next: ptr(1)},
		"最后一页不留坐标": {
			args: listTaskArgs{Status: &pending, Cursor: ptr(float64(1)), Limit: &one},
			want: []string{"t3"},
		},
		"翻过了头就空一页": {args: listTaskArgs{Cursor: ptr(float64(99))}, want: []string{}},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.service.taskViews = views
			raw, err := w.call(w.controller(nil).listTasks, want.args)
			if err != nil {
				t.Fatalf("列任务不该失败：%v", err)
			}
			page := decode[taskListWire](t, raw)
			got := make([]string, 0, len(page.Tasks))
			for _, one := range page.Tasks {
				got = append(got, one.ID)
			}
			if strings.Join(got, ",") != strings.Join(want.want, ",") {
				t.Fatalf("该列出 %v，拿到 %v", want.want, got)
			}
			switch {
			case want.next == nil && page.NextCursor != nil:
				t.Fatalf("没有下一页了还留着坐标 %d", *page.NextCursor)
			case want.next != nil && (page.NextCursor == nil || *page.NextCursor != *want.next):
				t.Fatalf("下一页该从 %d 起，拿到 %v", *want.next, page.NextCursor)
			}
		})
	}
}

// TestListTasksRejectsBadPaging 钉住翻页那两个数越界时的那两句话。
func TestListTasksRejectsBadPaging(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args    listTaskArgs
		message string
	}{
		"坐标是负的":  {args: listTaskArgs{Cursor: ptr(float64(-1))}, message: invalidCursor},
		"坐标不是整数": {args: listTaskArgs{Cursor: ptr(1.5)}, message: invalidCursor},
		"一页零行":   {args: listTaskArgs{Limit: ptr(float64(0))}, message: invalidLimit},
		"一页太多行":  {args: listTaskArgs{Limit: ptr(float64(101))}, message: invalidLimit},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			_, err := w.call(w.controller(nil).listTasks, want.args)
			expectCode(t, err, agentteam.CodeInvalidArgument)
			var failure *agentteam.Error
			if errors.As(err, &failure) && failure.Message != want.message {
				t.Fatalf("那句话必须和 DSH 一模一样，拿到 %q", failure.Message)
			}
		})
	}
}

// TestUpdateTaskForwardsOnlyWhatWasGiven 钉住那几个指针参数：「没给这个字段」和
// 「给了一个空值」是两件事。
func TestUpdateTaskForwardsOnlyWhatWasGiven(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	_, err := w.call(w.controller(nil).updateTask, json.RawMessage(
		`{"task_id":"task-1","expected_revision":3,"action":"edit","subject":""}`,
	))
	if err != nil {
		t.Fatalf("改任务不该失败：%v", err)
	}
	got := w.service.updates[0]
	switch {
	case got.TaskID != "task-1" || got.ExpectedRev != 3 || got.Action != agentteam.ActionEdit:
		t.Fatalf("该原样转手，拿到 %+v", got)
	case got.Subject == nil || *got.Subject != "":
		t.Fatalf("写了空标题该原样送过去，拿到 %v", got.Subject)
	case got.Description != nil || got.BlockedBy != nil || got.WriteScopes != nil:
		t.Fatalf("没写的字段不该编出来：%+v", got)
	case got.Owner != "":
		t.Fatalf("没写主人不该编出来，拿到 %q", got.Owner)
	}
}

// TestUpdateTaskRejectsAnUnsafeRevision 钉住那道版本号闸：越界值送进服务会撞上一次
// 必不相等的比对，模型看见的是「版本号过期了」——而它其实是把数写错了。
func TestUpdateTaskRejectsAnUnsafeRevision(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	_, err := w.call(w.controller(nil).updateTask, json.RawMessage(
		`{"task_id":"task-1","expected_revision":1.5,"action":"claim"}`,
	))
	expectCode(t, err, agentteam.CodeInvalidArgument)
	if len(w.service.updates) != 0 {
		t.Fatalf("被拒了还去改了任务：%+v", w.service.updates)
	}
}

// ---- 输出的形状 ----

// TestOutputKeepsEveryRequiredKey 钉住那几个必填的数组键：模型每一轮看见的键集合
// 必须一样，否则它得自己去分辨「没有这个键」和「这个键是空的」。
func TestOutputKeepsEveryRequiredKey(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	raw, err := w.call(w.controller(nil).listAgents, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("列花名册不该失败：%v", err)
	}
	if !strings.Contains(string(raw), `"diagnostics":[]`) {
		t.Fatalf("没有诊断的成员也该带着这个键：%s", raw)
	}

	raw, err = w.call(w.controller(nil).getTask, getTaskArgs{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("读任务不该失败：%v", err)
	}
	for _, key := range []string{`"blockedBy":[]`, `"writeScopes":[]`, `"writeScopeWarnings":[]`, `"revision":3`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("结果里缺 %s：%s", key, raw)
		}
	}
}

// TestOutputDoesNotEscapeHTML 钉住那条不转义：任务标题是人和模型写的自由文本，
// 多出来的转义只会让模型看见一句和原文长得不一样的话。
func TestOutputDoesNotEscapeHTML(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.taskView.Subject = "把 <div> 和 a && b 修好"
	raw, err := w.call(w.controller(nil).getTask, getTaskArgs{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("读任务不该失败：%v", err)
	}
	// 默认那套转义把 < > & 排成 &lt; 这类写法，所以认那个前缀就够。
	if !strings.Contains(string(raw), "<div> 和 a && b") || strings.Contains(string(raw), `\u00`) {
		t.Fatalf("这份字节被转义了：%s", raw)
	}
}

// ---- 装上去和摘下来 ----

// installedNames 是这十件工具的名字。
func installedNames() []string {
	return []string{
		SpawnTool, SendMessageTool, FollowupTool, ListAgentsTool, WaitTool, InterruptTool,
		TaskCreateTool, TaskListTool, TaskGetTool, TaskUpdateTool,
	}
}

// TestInstallAddsTheGuidanceAndTenTools 钉住那条装配次序和那条摘干净。
func TestInstallAddsTheGuidanceAndTenTools(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	undo, err := w.controller(nil).Install(t.Context(), w.agentScope, w.deps())
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	for _, name := range installedNames() {
		if _, ok := w.tools.Get(name, w.agentScope.Key()); !ok {
			t.Fatalf("%s 没装上", name)
		}
	}

	assembly, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.agentScope.Key()})
	if err != nil {
		t.Fatalf("装配提示词失败：%v", err)
	}
	prompt, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		t.Fatalf("渲染提示词失败：%v", err)
	}
	if !strings.Contains(prompt, "Agent Teams is available in this session") {
		t.Fatalf("那段指引没进提示词：\n%s", prompt)
	}
	identity := "Your Team role is lead; your Team name is lead; Team id is team-1."
	if !strings.Contains(prompt, identity) {
		t.Fatalf("那句身份没跟着进去：\n%s", prompt)
	}

	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	for _, name := range installedNames() {
		if _, ok := w.tools.Get(name, w.agentScope.Key()); ok {
			t.Fatalf("%s 摘掉之后还在", name)
		}
	}
}

// TestGuidanceNamesTheTeammate 钉住队友那一支：角色和名字都从花名册来。
func TestGuidanceNamesTheTeammate(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.caller = agentteam.Caller{Team: teamID, ID: "session-worker", Name: "worker"}
	text, err := w.controller(nil).guidance(t.Context(), systemprompt.AssembleContext{Scope: w.agentScope.Key()})
	if err != nil {
		t.Fatalf("求指引不该失败：%v", err)
	}
	if !strings.HasSuffix(text, "Your Team role is teammate; your Team name is worker; Team id is team-1.") {
		t.Fatalf("那句身份不对：\n%s", text)
	}
}

// TestGuidanceFailsForANonMember 钉住那条：不属于这支团队的作用域连那段指引都求不出来。
func TestGuidanceFailsForANonMember(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.callerErr = &agentteam.Error{Code: agentteam.CodeNotMember, Message: "不是这支团队的人"}
	_, err := w.controller(nil).guidance(t.Context(), systemprompt.AssembleContext{Scope: w.agentScope.Key()})
	expectCode(t, err, agentteam.CodeNotMember)
}

// TestInstallRefusesAnIncompleteAssembly 钉住那两条缺件。
func TestInstallRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*Deps){
		"没有工具运行时":  func(deps *Deps) { deps.Tools = nil },
		"没有提示词注册表": func(deps *Deps) { deps.Prompts = nil },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			deps := w.deps()
			damage(&deps)
			if _, err := w.controller(nil).Install(t.Context(), w.agentScope, deps); err == nil {
				t.Fatal("缺件还装得上")
			}
		})
	}
}

// TestInstallUnwindsWhatItAlreadyPutUp 钉住那条反序摘除：半装上去意味着模型手上有
// 一件起得了队友、却没有那段指引管着的工具。
func TestInstallUnwindsWhatItAlreadyPutUp(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	occupied := &tools.Definition{
		Name:       TaskUpdateTool,
		Parameters: tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) { return nil, nil },
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}
	release, err := w.tools.Register(t.Context(), w.agentScope, occupied)
	if err != nil {
		t.Fatalf("占工具名失败：%v", err)
	}
	t.Cleanup(func() { _ = release(context.Background()) })

	if _, err := w.controller(nil).Install(t.Context(), w.agentScope, w.deps()); err == nil {
		t.Fatal("工具名被占了还装得上")
	}
	for _, name := range installedNames() {
		if name == TaskUpdateTool {
			continue
		}
		if _, ok := w.tools.Get(name, w.agentScope.Key()); ok {
			t.Fatalf("装失败了 %s 还在", name)
		}
	}
	if _, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.agentScope.Key()}); err != nil {
		t.Fatalf("装配提示词不该失败：%v", err)
	}
}

// TestNewRefusesAnIncompleteConfig 钉住那三条装配规矩。
func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	cases := map[string]func(*Config){
		"没有团队服务":  func(config *Config) { config.Service = nil },
		"没说是哪支团队": func(config *Config) { config.Team = "" },
		"没有查回去的路": func(config *Config) { config.AgentOf = nil },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := w.config()
			damage(&config)
			if _, err := New(config); err == nil {
				t.Fatal("缺件还造得出控制器")
			}
		})
	}
}

// TestRegisterInvariantsTakesThePackageName 钉住那条登记：占住这个包名，并且让
// 「检查过了、结论是无需检查」和「这个包被漏掉了」区分得开。
func TestRegisterInvariantsTakesThePackageName(t *testing.T) {
	t.Parallel()

	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表还登记得上")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	release, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记不变量失败：%v", err)
	}
	release()
}
