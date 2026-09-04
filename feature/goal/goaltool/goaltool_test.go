// 本文件的作用：把这三件目标工具钉在它们那几条真会出错的边上——那道执行期的资格闸、
// 从宽到严的更新阶梯、那道轮数闸，以及一次自动轮次收尾时捎出去的那条指令。
//
// # 这些测试防的是什么错
//
//   - **模型自己批给自己一个目标**。create、edit、pause、resume 必须坐在一条**人自己
//     写的**、落在顶层 agent 上的输入上；少了任何一半，一个模型就能在自动轮次里
//     给自己开一份新预算。
//   - **一份授权被转给了另一个目标或者另一次修订**。准入轮次那份授权只对发它的
//     那个目标、那次修订、那一轮成立，对不上就不该算数。
//   - **blocked 变成一句随口说的话**。自动轮次里报 blocked 必须熬够部署方规定的
//     轮数；这道闸松了，一个目标可以在第一轮就把自己判死。
//   - **收尾之后模型没话可说**。自动轮次里的 complete / blocked 要捎一条收尾指令
//     出去，否则那一轮的最后一句话是一份 JSON，人看不到任何交代。
//   - **一次直接的人类回合被塞了那条指令**。人就在对面，那条「现在写收尾消息」
//     只会让模型对着人念一遍它自己的规矩。
//   - **输出被 HTML 转义**。目标描述是人写的自由文本，`<` 变成 `<` 之后模型
//     看见的是一句和原文长得不一样的话。
//   - **半装上去**。模型手上有一件建得了目标、却没有那段策略指引管着的工具，
//     而那段指引正是「什么时候才该建目标」的唯一说明。

package goaltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// ---- 假件 ----

// stubAgent 是一个握着**真会话**的假 agent。
//
// 会话必须是真的：本包全部授权判断读的都是那条日志，拿一份手搓的事件切片糊弄
// 过去，等于跳过了 [github.com/snight1983/ds-harness-go/harness/session.Session.Append] 那道信封校验——
// 而那正是「这些事件真会落进日志」的唯一保证。
type stubAgent struct {
	id     sessionlog.SessionID
	own    *scope.Scope
	log    *coresession.Session
	status agent.Status
}

// newStubAgent 造一个带真会话、真作用域的假 agent，默认在跑。
func newStubAgent(t *testing.T, id string, own *scope.Scope) *stubAgent {
	t.Helper()
	sessionID := sessionlog.SessionID(id)
	header := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: sessionID, CreatedAt: 1}
	log, err := coresession.NewSession(sessionID, coresession.Options{
		Header: &header,
		Now:    func() int64 { return 1 },
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return &stubAgent{id: sessionID, own: own, log: log, status: agent.StatusRunning}
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                      { return a.status }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return a.log }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Followup(llm.Message)                                      {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// append 往这条日志里写一条事件。
func (a *stubAgent) append(t *testing.T, event sessionlog.Event) {
	t.Helper()
	if _, err := a.log.Append(event); err != nil {
		t.Fatalf("往日志里写 %s 失败：%v", event.Type, err)
	}
}

// stubAgents 是那张可摆布的 agent 注册表。
type stubAgents struct {
	live  map[sessionlog.SessionID]agent.Agent
	roots []agent.Agent
}

func (a *stubAgents) Get(id sessionlog.SessionID) (agent.Agent, bool) {
	found, present := a.live[id]
	return found, present
}

func (a *stubAgents) Roots() []agent.Agent { return a.roots }

// stubService 是那台记账的假目标服务。
type stubService struct {
	// view 是每一次成功调用交回的那份视图。
	view *goal.View

	// 这几个非 nil 时对应那条路直接失败。
	getErr      error
	createErr   error
	editErr     error
	pauseErr    error
	resumeErr   error
	completeErr error
	blockErr    error

	// ops 按顺序记下被调到的那些方法名。
	ops []string
	// owners 记下每一次调用交进来的那个 agent：权柄凭的是谁，靠它验。
	owners []agent.Agent
	// refs 记下每一次改动带的那份 CAS 身份。
	refs []goal.Ref
	// creates / edits / blocks 记下各自那份入参。
	creates []goal.CreateRequest
	edits   []goal.EditRequest
	blocks  []goal.BlockReason
}

func (s *stubService) record(op string, owner agent.Agent) {
	s.ops = append(s.ops, op)
	s.owners = append(s.owners, owner)
}

func (s *stubService) Get(owner agent.Agent) (*goal.View, error) {
	s.record("get", owner)
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.view, nil
}

func (s *stubService) Create(owner agent.Agent, request goal.CreateRequest) (*goal.View, error) {
	s.record("create", owner)
	s.creates = append(s.creates, request)
	if s.createErr != nil {
		return nil, s.createErr
	}
	return s.view, nil
}

func (s *stubService) Edit(owner agent.Agent, ref goal.Ref, request goal.EditRequest) (*goal.View, error) {
	s.record("edit", owner)
	s.refs = append(s.refs, ref)
	s.edits = append(s.edits, request)
	if s.editErr != nil {
		return nil, s.editErr
	}
	return s.view, nil
}

func (s *stubService) Pause(owner agent.Agent, ref goal.Ref) (*goal.View, error) {
	s.record("pause", owner)
	s.refs = append(s.refs, ref)
	if s.pauseErr != nil {
		return nil, s.pauseErr
	}
	return s.view, nil
}

func (s *stubService) Resume(owner agent.Agent, ref goal.Ref) (*goal.View, error) {
	s.record("resume", owner)
	s.refs = append(s.refs, ref)
	if s.resumeErr != nil {
		return nil, s.resumeErr
	}
	return s.view, nil
}

func (s *stubService) Complete(owner agent.Agent, ref goal.Ref) (*goal.View, error) {
	s.record("complete", owner)
	s.refs = append(s.refs, ref)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return s.view, nil
}

func (s *stubService) Block(
	owner agent.Agent, ref goal.Ref, reason goal.BlockReason,
) (*goal.View, error) {
	s.record("block", owner)
	s.refs = append(s.refs, ref)
	s.blocks = append(s.blocks, reason)
	if s.blockErr != nil {
		return nil, s.blockErr
	}
	return s.view, nil
}

// ---- 事件 ----

// turnStart 造一条回合开始事件。本包只认它的类型和位置，负载不解。
func turnStart(turn int) sessionlog.Event {
	return sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: json.RawMessage(fmt.Sprintf(`{"turn":%d}`, turn)),
	}
}

// turnEnd 造一条回合结束事件。
func turnEnd(turn int) sessionlog.Event {
	return sessionlog.Event{
		Type: sessionlog.EventTurnEnd,
		Data: json.RawMessage(fmt.Sprintf(`{"turn":%d}`, turn)),
	}
}

// userEvent 造一条带某个来源的用户消息事件。
//
// 带上表面标记：一条真的用户消息一定是上表面的，少了它那台会话当场拒收。
func userEvent(t *testing.T, source llm.MessageSource) sessionlog.Event {
	t.Helper()
	message := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "go on"}}, source)
	encoded, err := json.Marshal(sessionlog.UserMessageData{Message: message})
	if err != nil {
		t.Fatalf("排用户消息失败：%v", err)
	}
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      encoded,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// goalRoundEvent 造一条由某个准入轮次发出的用户消息事件。
func goalRoundEvent(t *testing.T, source goal.Source) sessionlog.Event {
	t.Helper()
	carried, err := source.MessageSource()
	if err != nil {
		t.Fatalf("包目标来源失败：%v", err)
	}
	return userEvent(t, carried)
}

// ---- 装配 ----

// world 是一次用例要的全部家当。
type world struct {
	t          *testing.T
	root       *scope.Scope
	agentScope *scope.Scope
	service    *stubService
	agents     *stubAgents
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

// activeView 造一份「在推进中」的目标视图。
//
// Activation 一定要填：那份输出契约要求 goal 那一支必须带 activation，空串会被
// omitempty 抹掉，于是这份结果对不上任何一支，真运行时当场拒收。
func activeView(objective string, roundsStarted int) *goal.View {
	return &goal.View{
		Snapshot: goal.Snapshot{
			Ref:           goal.Ref{ID: "g1", Revision: 1},
			Objective:     objective,
			Phase:         goal.PhaseActive,
			MaxGoalRounds: 10,
		},
		RoundsStarted: roundsStarted,
		CreatedAt:     1000,
		UpdatedAt:     2000,
		Activation:    goal.Armed,
	}
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
	caller := newStubAgent(t, "caller", agentScope)
	return &world{
		t:          t,
		root:       root,
		agentScope: agentScope,
		service:    &stubService{view: activeView("ship the thing", 0)},
		agents: &stubAgents{
			live:  map[sessionlog.SessionID]agent.Agent{caller.ID(): caller},
			roots: []agent.Agent{caller},
		},
		caller:  caller,
		tools:   runtime,
		prompts: prompts,
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
	return Config{Service: w.service, Agents: w.agents, AgentOf: w.agentOf}
}

func (w *world) deps() Deps {
	return Deps{Tools: w.tools, Prompts: w.prompts}
}

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

// install 造一个控制器并把整套装上根作用域。
func (w *world) install(shape func(*Config)) *Controller {
	w.t.Helper()
	controller := w.controller(shape)
	undo, err := controller.Install(w.t.Context(), w.root, w.deps())
	if err != nil {
		w.t.Fatalf("装控制器失败：%v", err)
	}
	w.t.Cleanup(func() { _ = undo(context.Background()) })
	return controller
}

// humanTurn 开一个带人类输入的回合。
func (w *world) humanTurn() {
	w.t.Helper()
	w.caller.append(w.t, turnStart(1))
	w.caller.append(w.t, userEvent(w.t, llm.UserSource{}))
}

// goalTurn 开一个由某个准入轮次推起来的回合。
//
// 那条 goal/change 是真会夹在中间的噪音（一次准入本身就写了一条改动），摆在这里
// 是为了让那两道闸走一遍「这条事件根本不是用户消息」的路——它们问的是「这一轮里
// **有没有**一条够格的输入」，遇上别的事件必须接着往下找，不是当场收工。
func (w *world) goalTurn(source goal.Source) {
	w.t.Helper()
	w.caller.append(w.t, turnStart(1))
	w.caller.append(w.t, sessionlog.Event{Type: goal.EventChange, Data: json.RawMessage(`{}`)})
	w.caller.append(w.t, goalRoundEvent(w.t, source))
}

// matchingGoalTurn 开一个和当前视图逐字对得上的准入轮次。
func (w *world) matchingGoalTurn() {
	w.t.Helper()
	view := w.service.view
	w.goalTurn(goal.Source{GoalID: view.ID, Revision: view.Revision, Round: view.RoundsStarted})
}

// ctx 是一份把这个调用方认成发起者的上下文——真驱动交给工具体的正是这样一份。
func (w *world) ctx() context.Context {
	return agent.WithInitiator(w.t.Context(), w.caller)
}

// execOn 造一份落在这个世界那把 agent 钥匙上的执行上下文。
func (w *world) execOn() *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: w.agentScope.Key()},
	}}
}

// get / create / update 直接调三条工具体：断言落在 [Code] 上，不落在那些英文文案上。
func (w *world) get(controller *Controller) (json.RawMessage, error) {
	return controller.readGoal(w.ctx(), json.RawMessage(`{}`), w.execOn())
}

func (w *world) create(controller *Controller, args any) (json.RawMessage, error) {
	return controller.createGoal(w.ctx(), w.encode(args), w.execOn())
}

func (w *world) update(controller *Controller, args any) (json.RawMessage, error) {
	return controller.runUpdate(w.ctx(), w.encode(args), w.execOn())
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

// dispatch 走**真运行时**派发一次调用：入参 schema、输出 schema、渲染、以及那些
// 推迟出去的上下文，只有这条路上才全都算数。
func (w *world) dispatch(name string, args any) tools.Result {
	w.t.Helper()
	return w.tools.Execute(w.ctx(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      name,
		Arguments: w.encode(args),
		Agent:     w.agentScope.Key(),
	})
}

// ---- 断言助手 ----

// expectCode 断言这次失败带的是某个确切的码。
func expectCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("本该被拒（%s），却成功了", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("报的是 %v，本该是 %s", err, want)
	}
}

// expectGoalCode 断言这次失败是那台目标服务那一侧的码。
func expectGoalCode(t *testing.T, err error, want goal.ErrorCode) {
	t.Helper()
	var failure *goal.Error
	if !errors.As(err, &failure) {
		t.Fatalf("交回的是 %T，本该是 *goal.Error：%v", err, err)
	}
	if failure.Code != want {
		t.Fatalf("报的是 %s，本该是 %s", failure.Code, want)
	}
}

// textOf 把一份内容里那些文本块拼起来。
func textOf(content llm.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ---- 装配面 ----

func TestNewRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*Config){
		"没有目标服务":           func(config *Config) { config.Service = nil },
		"没有 agent 注册表":     func(config *Config) { config.Agents = nil },
		"没有从钥匙找回 agent 的路": func(config *Config) { config.AgentOf = nil },
		"轮数闸是负的":           func(config *Config) { config.BlockedAfterConsecutiveRounds = -1 },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.config()
			damage(&config)
			if _, err := New(config); err == nil {
				t.Fatal("缺件或者矛盾的装配面还造得出控制器")
			}
		})
	}
}

// TestNewFillsInTheDefaultThreshold 钉住那条零值当「没给」：0 本身是一个非法闸位，
// 拿它当默认值不丢东西。
func TestNewFillsInTheDefaultThreshold(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	if controller.blockedAfter != DefaultBlockedAfterConsecutiveRounds {
		t.Fatalf("默认闸位是 %d", controller.blockedAfter)
	}

	explicit := w.controller(func(config *Config) { config.BlockedAfterConsecutiveRounds = 7 })
	if explicit.blockedAfter != 7 {
		t.Fatalf("部署方给的闸位被改成了 %d", explicit.blockedAfter)
	}
}

// TestGuidanceCarriesTheThreshold 钉住那个数字后面那个空格：少掉它两个词会粘在一起。
func TestGuidanceCarriesTheThreshold(t *testing.T) {
	t.Parallel()

	text := guidance(5)
	if !strings.Contains(text, "at least 5 consecutive goal rounds") {
		t.Fatalf("那道闸没排进指引：\n%s", text)
	}
}

// ---- 错误 ----

// TestTheCodeIsItsOwnError 钉住那条 errors.Is 的路：分类码自己就是一个 error，
// 调用方不必先 errors.As 出一个结构体再比字段。零值收方那两条也一并钉住——一个
// 会 panic 的 Error() 会把一次本该被报出来的失败变成一次崩溃。
func TestTheCodeIsItsOwnError(t *testing.T) {
	t.Parallel()

	if CodeDriverRequired.Error() != string(CodeDriverRequired) {
		t.Fatalf("码自己那句话是 %q", CodeDriverRequired.Error())
	}

	var absent *Error
	if absent.Error() != "" {
		t.Fatalf("空错误那句话是 %q", absent.Error())
	}
	if absent.Is(CodeDriverRequired) {
		t.Fatal("空错误认下了一个码")
	}

	failure := fail(CodeInvalidUpdate, "%s", invalidRef)
	if failure.Is(errors.New("别的错")) {
		t.Fatal("认下了一个不是码的目标")
	}
	if !errors.Is(failure, CodeInvalidUpdate) || errors.Is(failure, CodeBlockThreshold) {
		t.Fatalf("码比对不成立：%v", failure)
	}
}

// ---- 执行期的资格闸 ----

// TestExecutionRejectsACallerItCannotVouchFor 钉住那道执行期闸的每一条边：查不回来、
// 不在注册表里、不是那个实例、不在跑、以及这条调用链上的发起者不是它本人。
func TestExecutionRejectsACallerItCannotVouchFor(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		damage func(*world)
		shape  func(*Config)
		exec   func(*world) *tools.RunContext
		ctx    func(*world) context.Context
		want   Code
	}{
		"压根没落在 agent 上": {
			exec: func(*world) *tools.RunContext { return nil },
			want: CodeAgentRequired,
		},
		"执行上下文里没有钥匙": {
			exec: func(*world) *tools.RunContext { return &tools.RunContext{} },
			want: CodeAgentRequired,
		},
		"这把钥匙查不回 agent": {
			exec: func(w *world) *tools.RunContext {
				return &tools.RunContext{Execution: tools.Execution{
					ExecutionInput: tools.ExecutionInput{Agent: w.root.Key()},
				}}
			},
			want: CodeDriverRequired,
		},
		// 查回来一个 nil 和查不回来在这里必须是同一件事：分开报只会告诉模型注册表
		// 里还有没有别的东西。
		"查回来的是 nil": {
			shape: func(config *Config) {
				config.AgentOf = func(*scope.Key) (agent.Agent, error) { return nil, nil }
			},
			want: CodeDriverRequired,
		},
		"不在活注册表里": {
			damage: func(w *world) { delete(w.agents.live, w.caller.ID()) },
			want:   CodeDriverRequired,
		},
		"注册表里是另一个实例": {
			damage: func(w *world) {
				w.agents.live[w.caller.ID()] = newStubAgent(w.t, "caller", w.agentScope)
			},
			want: CodeDriverRequired,
		},
		"调用方没在跑": {
			damage: func(w *world) { w.caller.status = agent.StatusIdle },
			want:   CodeDriverRequired,
		},
		"这条链上没有发起者": {
			ctx:  func(w *world) context.Context { return w.t.Context() },
			want: CodeDriverRequired,
		},
		"发起者是别人": {
			ctx: func(w *world) context.Context {
				other := newStubAgent(w.t, "other", w.agentScope)
				return agent.WithInitiator(w.t.Context(), other)
			},
			want: CodeDriverRequired,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.humanTurn()
			controller := w.controller(testCase.shape)
			if testCase.damage != nil {
				testCase.damage(w)
			}
			runCtx := w.ctx()
			if testCase.ctx != nil {
				runCtx = testCase.ctx(w)
			}
			exec := w.execOn()
			if testCase.exec != nil {
				exec = testCase.exec(w)
			}
			// 三条工具体各走一遍：这道闸是每一件工具自己先过的第一关，漏掉任何
			// 一件都等于给它开了一条不用认身份的后门。
			bodies := map[string]func() (json.RawMessage, error){
				GetToolName: func() (json.RawMessage, error) {
					return controller.readGoal(runCtx, json.RawMessage(`{}`), exec)
				},
				CreateToolName: func() (json.RawMessage, error) {
					return controller.createGoal(runCtx, json.RawMessage(`{"objective":"x"}`), exec)
				},
				UpdateToolName: func() (json.RawMessage, error) {
					return controller.runUpdate(runCtx, json.RawMessage(
						`{"goal_id":"g1","revision":1,"action":"pause"}`), exec)
				},
			}
			for toolName, body := range bodies {
				_, err := body()
				if !errors.Is(err, testCase.want) {
					t.Fatalf("%s 报的是 %v，本该是 %s", toolName, err, testCase.want)
				}
			}
		})
	}
}

// TestExecutionRequiresAnOpenTurn 钉住那条从日志尾巴往回找的边界：先撞上 turn/end
// 说明这次调用不在任何驱动之下，走的是别人硬凑出来的一条路。
func TestExecutionRequiresAnOpenTurn(t *testing.T) {
	t.Parallel()

	t.Run("一条边界都没有", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		_, err := w.get(w.controller(nil))
		expectCode(t, err, CodeDriverRequired)
	})

	t.Run("最近那个回合已经关了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.caller.append(t, turnEnd(1))
		_, err := w.get(w.controller(nil))
		expectCode(t, err, CodeDriverRequired)
	})

	t.Run("回合还开着", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		if _, err := w.get(w.controller(nil)); err != nil {
			t.Fatalf("回合开着还被拒：%v", err)
		}
	})
}

// ---- 直接人类回合 ----

// TestDirectHumanNeedsATopLevelAgentAndAHumanMessage 钉住那两道缺一不可的条件：
// 子 agent 那条链上的输入来自它的父，不来自人；不带 user 来源的消息也一样不算。
func TestDirectHumanNeedsATopLevelAgentAndAHumanMessage(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*world){
		"调用方不是顶层 agent": func(w *world) { w.agents.roots = nil },
		"这一轮里只有一条插件消息": func(w *world) {
			w.caller.append(w.t, turnStart(2))
			w.caller.append(w.t, userEvent(w.t, llm.PluginSource{Plugin: "somebody-else"}))
		},
		"这一轮里那条用户消息读不回来": func(w *world) {
			w.caller.append(w.t, turnStart(2))
			// 合法 JSON、错的形状：解不出一条用户消息，于是它不算数。这条路防的是
			// 「一条坏掉的事件被当成一次人类授权」。
			w.caller.append(w.t, sessionlog.Event{
				Type:      sessionlog.EventUserMessage,
				Data:      json.RawMessage(`[]`),
				SurfaceOp: sessionlog.AppendOp{},
			})
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			if name == "调用方不是顶层 agent" {
				w.humanTurn()
			}
			arrange(w)
			_, err := w.create(w.controller(nil), map[string]any{"objective": "x"})
			expectCode(t, err, CodeAuthorityRequired)
		})
	}
}

// ---- get_goal ----

// TestGetGoalReadsTheCurrentGoal 钉住那两支输出：没有目标时是 `{"goal":null}`，
// 有目标时带上那份紧凑快照和那个进程局部的续推资格。
func TestGetGoalReadsTheCurrentGoal(t *testing.T) {
	t.Parallel()

	t.Run("没有当前目标", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.view = nil
		raw, err := w.get(w.controller(nil))
		if err != nil {
			t.Fatalf("读目标失败：%v", err)
		}
		if string(raw) != `{"goal":null}` {
			t.Fatalf("空的那一支排成了 %s", raw)
		}
	})

	t.Run("有当前目标", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		reason := goal.BlockReason{Code: blockCode, Message: "upstream is down"}
		w.service.view.Phase = goal.PhaseBlocked
		w.service.view.BlockedReason = &reason
		raw, err := w.get(w.controller(nil))
		if err != nil {
			t.Fatalf("读目标失败：%v", err)
		}
		want := `{"goal":{"id":"g1","revision":1,"objective":"ship the thing","phase":"blocked",` +
			`"roundsStarted":0,"maxGoalRounds":10,"blockedReason":{"code":"model-reported",` +
			`"message":"upstream is down"}},"activation":"armed"}`
		if string(raw) != want {
			t.Fatalf("排出来的是\n%s\n本该是\n%s", raw, want)
		}
	})

	t.Run("服务那边失败就原样报上去", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.getErr = errors.New("读不动")
		if _, err := w.get(w.controller(nil)); err == nil {
			t.Fatal("服务失败了还成功")
		}
	})
}

// TestGoalValueDetachesTheBlockReason 钉住那次复制：交出去的结果不许和那台服务
// 共享同一块可写内存。
func TestGoalValueDetachesTheBlockReason(t *testing.T) {
	t.Parallel()

	view := activeView("x", 0)
	view.Phase = goal.PhaseBlocked
	view.BlockedReason = &goal.BlockReason{Code: blockCode, Message: "stuck"}
	wire := goalValue(view)
	if wire.Goal.BlockedReason == view.BlockedReason {
		t.Fatal("交出去的阻塞原因和视图共享同一块内存")
	}
	wire.Goal.BlockedReason.Message = "changed"
	if view.BlockedReason.Message != "stuck" {
		t.Fatal("穿过结果改到了视图")
	}
}

// ---- create_goal ----

// TestCreateGoalForwardsTheRequest 钉住那两件事：权柄凭的是那个确切的活调用方，
// 以及「没给轮数上限」和「给了一个具体数」在服务那边分得开。
func TestCreateGoalForwardsTheRequest(t *testing.T) {
	t.Parallel()

	t.Run("不带轮数上限", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		if _, err := w.create(w.controller(nil), map[string]any{"objective": "ship it"}); err != nil {
			t.Fatalf("建目标失败：%v", err)
		}
		if len(w.service.creates) != 1 {
			t.Fatalf("服务收到 %d 次建目标", len(w.service.creates))
		}
		request := w.service.creates[0]
		if request.Objective != "ship it" || request.MaxGoalRounds != nil {
			t.Fatalf("交到服务的是 %+v", request)
		}
		if w.service.owners[0] != w.caller {
			t.Fatal("权柄凭的不是那个确切的活调用方")
		}
	})

	t.Run("带轮数上限", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.create(w.controller(nil), map[string]any{
			"objective": "ship it", "max_goal_rounds": 12,
		})
		if err != nil {
			t.Fatalf("建目标失败：%v", err)
		}
		rounds := w.service.creates[0].MaxGoalRounds
		if rounds == nil || *rounds != 12 {
			t.Fatalf("轮数上限交成了 %v", rounds)
		}
	})
}

// TestCreateGoalRejectsANonIntegerRoundCap 钉住那句挪到本层的拒收：字节要和域里
// 那句一模一样，否则同一个模型在两边看见两句话。
func TestCreateGoalRejectsANonIntegerRoundCap(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]float64{
		"小数":  2.5,
		"太大了": 1e300,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.humanTurn()
			_, err := w.create(w.controller(nil), map[string]any{
				"objective": "ship it", "max_goal_rounds": value,
			})
			expectGoalCode(t, err, goal.CodeInvalidMaxRounds)
			if err.Error() != invalidMaxRounds {
				t.Fatalf("那句话变成了 %q", err.Error())
			}
			if len(w.service.creates) != 0 {
				t.Fatal("被拒的入参还是送到服务那边去了")
			}
		})
	}
}

// TestCreateGoalRequiresADirectHuman 钉住那条「模型不许自己批给自己一份新预算」：
// 一个自动往下推的轮次建不出目标。
func TestCreateGoalRequiresADirectHuman(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.matchingGoalTurn()
	_, err := w.create(w.controller(nil), map[string]any{"objective": "ship it"})
	expectCode(t, err, CodeAuthorityRequired)
}

// TestCreateGoalReportsTheDecodeAndServiceFailures 钉住那两条往上抛的路。
func TestCreateGoalReportsTheDecodeAndServiceFailures(t *testing.T) {
	t.Parallel()

	t.Run("入参解不开", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		if _, err := w.create(w.controller(nil), json.RawMessage(`[`)); err == nil {
			t.Fatal("坏参数还成功")
		}
	})

	t.Run("服务那边失败", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.createErr = errors.New("建不出来")
		if _, err := w.create(w.controller(nil), map[string]any{"objective": "x"}); err == nil {
			t.Fatal("服务失败了还成功")
		}
	})
}

// ---- update_goal：那份 CAS 身份 ----

// TestUpdateRejectsAMalformedIdentity 钉住那道照抄闸：一个被 trim 之后才对得上的 id
// 说明模型是从别处抄过来的，而这道闸要的正是它照抄 get_goal 交出来的那一份。
func TestUpdateRejectsAMalformedIdentity(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"id 是空的":   {"goal_id": "", "revision": 1, "action": actionPause},
		"id 带首尾空白": {"goal_id": " g1 ", "revision": 1, "action": actionPause},
		"修订号是 0":   {"goal_id": "g1", "revision": 0, "action": actionPause},
		"修订号是负的":   {"goal_id": "g1", "revision": -1, "action": actionPause},
		"修订号是小数":   {"goal_id": "g1", "revision": 1.5, "action": actionPause},
		"修订号大得数不清": {"goal_id": "g1", "revision": 1e300, "action": actionPause},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.humanTurn()
			_, err := w.update(w.controller(nil), args)
			expectCode(t, err, CodeInvalidUpdate)
			if err.Error() != invalidRef {
				t.Fatalf("那句话变成了 %q", err.Error())
			}
		})
	}
}

// TestUpdateReportsADecodeFailure 钉住那条解不开就往上抛的路。
func TestUpdateReportsADecodeFailure(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.humanTurn()
	if _, err := w.update(w.controller(nil), json.RawMessage(`[`)); err == nil {
		t.Fatal("坏参数还成功")
	}
}

// ---- update_goal：edit ----

// TestUpdateEditReplacesTheFieldsThatWereGiven 钉住那两个填充值：空串和 0 是严格
// schema 下的占位，一律当成「没给」，不许把目标描述清空。
func TestUpdateEditReplacesTheFieldsThatWereGiven(t *testing.T) {
	t.Parallel()

	t.Run("两个都换", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit,
			"objective": "ship it better", "max_goal_rounds": 20,
		})
		if err != nil {
			t.Fatalf("改目标失败：%v", err)
		}
		request := w.service.edits[0]
		if request.Objective == nil || *request.Objective != "ship it better" {
			t.Fatalf("新描述交成了 %v", request.Objective)
		}
		if request.MaxGoalRounds == nil || *request.MaxGoalRounds != 20 {
			t.Fatalf("新上限交成了 %v", request.MaxGoalRounds)
		}
		if w.service.refs[0] != (goal.Ref{ID: "g1", Revision: 1}) {
			t.Fatalf("CAS 身份交成了 %+v", w.service.refs[0])
		}
	})

	t.Run("一个都没给就原样交给域去拒", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit,
		})
		if err != nil {
			t.Fatalf("这一层不该拦它：%v", err)
		}
		request := w.service.edits[0]
		if request.Objective != nil || request.MaxGoalRounds != nil {
			t.Fatalf("填充值被当成了真值：%+v", request)
		}
	})
}

// TestUpdateEditRefusesTheFieldsThatDoNotBelong 钉住那道字段闸和那道人类闸。
func TestUpdateEditRefusesTheFieldsThatDoNotBelong(t *testing.T) {
	t.Parallel()

	t.Run("带了阻塞原因", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit,
			"objective": "x", "blocked_reason": "nope",
		})
		expectCode(t, err, CodeInvalidUpdate)
		if err.Error() != blockedOnlyField {
			t.Fatalf("那句话变成了 %q", err.Error())
		}
	})

	t.Run("轮数上限不是整数", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit, "max_goal_rounds": 2.5,
		})
		expectGoalCode(t, err, goal.CodeInvalidMaxRounds)
	})

	t.Run("不是一条直接的人类回合", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.matchingGoalTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit, "objective": "x",
		})
		expectCode(t, err, CodeAuthorityRequired)
	})

	t.Run("服务那边失败", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.editErr = errors.New("改不动")
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionEdit, "objective": "x",
		})
		if err == nil {
			t.Fatal("服务失败了还成功")
		}
	})
}

// ---- update_goal：pause / resume ----

// TestUpdateSuspendTakesNoOtherFields 钉住那条「带了字段就让它重来一次」：与其挑
// 一个猜它想干什么，不如把这次调用整个拒掉。
func TestUpdateSuspendTakesNoOtherFields(t *testing.T) {
	t.Parallel()

	t.Run("pause 和 resume 各走各的那条路", func(t *testing.T) {
		t.Parallel()
		for _, action := range []string{actionPause, actionResume} {
			w := newWorld(t)
			w.humanTurn()
			_, err := w.update(w.controller(nil), map[string]any{
				"goal_id": "g1", "revision": 1, "action": action,
			})
			if err != nil {
				t.Fatalf("%s 失败：%v", action, err)
			}
			if w.service.ops[0] != action {
				t.Fatalf("%s 走到了 %s", action, w.service.ops[0])
			}
		}
	})

	for name, extra := range map[string]map[string]any{
		"带了新描述":  {"objective": "x"},
		"带了新上限":  {"max_goal_rounds": 4},
		"带了阻塞原因": {"blocked_reason": "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.humanTurn()
			args := map[string]any{"goal_id": "g1", "revision": 1, "action": actionPause}
			for key, value := range extra {
				args[key] = value
			}
			_, err := w.update(w.controller(nil), args)
			expectCode(t, err, CodeInvalidUpdate)
			if err.Error() != editOnlyOrBlock {
				t.Fatalf("那句话变成了 %q", err.Error())
			}
		})
	}

	t.Run("不是一条直接的人类回合", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.matchingGoalTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionResume,
		})
		expectCode(t, err, CodeAuthorityRequired)
	})

	t.Run("服务那边失败", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.pauseErr = errors.New("停不下来")
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionPause,
		})
		if err == nil {
			t.Fatal("服务失败了还成功")
		}
	})
}

// ---- update_goal：complete / blocked ----

// TestWrapupAcceptsEitherAuthority 钉住那一档必须松开的授权：一个自动往下推的目标
// 如果只有人才能宣布它结束，那它就永远结束不了，只能耗光轮数预算。
func TestWrapupAcceptsEitherAuthority(t *testing.T) {
	t.Parallel()

	t.Run("直接的人类回合", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionComplete,
		})
		if err != nil {
			t.Fatalf("收尾失败：%v", err)
		}
		if w.service.ops[0] != "complete" {
			t.Fatalf("走到了 %s", w.service.ops[0])
		}
	})

	t.Run("当前那个准入轮次", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 3)
		w.matchingGoalTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionComplete,
		})
		if err != nil {
			t.Fatalf("收尾失败：%v", err)
		}
	})
}

// TestWrapupRejectsAnAuthorityThatDoesNotMatch 钉住那三样都得对上：对不上意味着那条
// 目标消息说的是另一个目标、另一次修订或者另一轮，那份授权不该转给这一次。
func TestWrapupRejectsAnAuthorityThatDoesNotMatch(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*world){
		"这一轮里什么授权都没有": func(w *world) { w.caller.append(w.t, turnStart(1)) },
		"目标身份对不上": func(w *world) {
			w.goalTurn(goal.Source{GoalID: "other", Revision: 1, Round: 3})
		},
		"修订号对不上": func(w *world) {
			w.goalTurn(goal.Source{GoalID: "g1", Revision: 9, Round: 3})
		},
		"轮号对不上": func(w *world) {
			w.goalTurn(goal.Source{GoalID: "g1", Revision: 1, Round: 1})
		},
		"此刻读不回来目标": func(w *world) {
			w.matchingGoalTurn()
			w.service.getErr = errors.New("读不动")
		},
		"此刻压根没有目标": func(w *world) {
			w.matchingGoalTurn()
			w.service.view = nil
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.service.view = activeView("ship the thing", 3)
			arrange(w)
			_, err := w.update(w.controller(nil), map[string]any{
				"goal_id": "g1", "revision": 1, "action": actionComplete,
			})
			expectCode(t, err, CodeAuthorityRequired)
			if err.Error() != missingWrapup {
				t.Fatalf("那句话变成了 %q", err.Error())
			}
		})
	}
}

// TestWrapupRefusesTheFieldsThatDoNotBelong 钉住那三道字段闸。
func TestWrapupRefusesTheFieldsThatDoNotBelong(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		extra map[string]any
		want  string
	}{
		"complete 带了新描述": {
			extra: map[string]any{"action": actionComplete, "objective": "x"},
			want:  editOnlyFields,
		},
		"complete 带了新上限": {
			extra: map[string]any{"action": actionComplete, "max_goal_rounds": 4},
			want:  editOnlyFields,
		},
		"blocked 带了新描述": {
			extra: map[string]any{"action": actionBlocked, "objective": "x", "blocked_reason": "y"},
			want:  editOnlyFields,
		},
		"complete 带了阻塞原因": {
			extra: map[string]any{"action": actionComplete, "blocked_reason": "y"},
			want:  blockedOnlyField,
		},
		"blocked 一个字都没说": {
			extra: map[string]any{"action": actionBlocked},
			want:  blockedRequired,
		},
		"blocked 只说了空白": {
			extra: map[string]any{"action": actionBlocked, "blocked_reason": "   "},
			want:  blockedRequired,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.humanTurn()
			args := map[string]any{"goal_id": "g1", "revision": 1}
			for key, value := range testCase.extra {
				args[key] = value
			}
			_, err := w.update(w.controller(nil), args)
			expectCode(t, err, CodeInvalidUpdate)
			if err.Error() != testCase.want {
				t.Fatalf("那句话变成了 %q，本该是 %q", err.Error(), testCase.want)
			}
		})
	}
}

// TestBlockedIsGatedByTheRoundThreshold 钉住那道轮数闸：它只管自动轮次那条路——
// 人说卡住了就是卡住了，不需要熬轮数。
func TestBlockedIsGatedByTheRoundThreshold(t *testing.T) {
	t.Parallel()

	t.Run("自动轮次里还没熬够", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 2)
		w.matchingGoalTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionBlocked,
			"blocked_reason": "upstream is down",
		})
		expectCode(t, err, CodeBlockThreshold)
		if !strings.Contains(err.Error(), "at least 3") || !strings.Contains(err.Error(), "is 2") {
			t.Fatalf("那句话没把两个数字都说清楚：%q", err.Error())
		}
		if len(w.service.blocks) != 0 {
			t.Fatal("被闸住的调用还是落到服务那边去了")
		}
	})

	t.Run("自动轮次里熬够了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 3)
		w.matchingGoalTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionBlocked,
			"blocked_reason": "upstream is down",
		})
		if err != nil {
			t.Fatalf("熬够了还被拒：%v", err)
		}
		reason := w.service.blocks[0]
		if reason.Code != blockCode || reason.Message != "upstream is down" {
			t.Fatalf("阻塞原因落成了 %+v", reason)
		}
	})

	t.Run("人说卡住了就不看轮数", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 0)
		w.humanTurn()
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionBlocked,
			"blocked_reason": "the vendor never shipped the key",
		})
		if err != nil {
			t.Fatalf("人说卡住了还被拒：%v", err)
		}
	})

	t.Run("服务那边失败", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.blockErr = errors.New("记不下")
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionBlocked, "blocked_reason": "x",
		})
		if err == nil {
			t.Fatal("服务失败了还成功")
		}
	})

	t.Run("complete 那边服务失败", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.humanTurn()
		w.service.completeErr = errors.New("记不下")
		_, err := w.update(w.controller(nil), map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionComplete,
		})
		if err == nil {
			t.Fatal("服务失败了还成功")
		}
	})
}

// ---- 收尾指令 ----

// TestWrapupContextRidesOnlyOnAGoalRound 钉住那条只捎给自动轮次的指令：一次直接的
// 人类回合里，人就在对面，模型接着说话是本来就会发生的事。
func TestWrapupContextRidesOnlyOnAGoalRound(t *testing.T) {
	t.Parallel()

	t.Run("自动轮次里的 complete", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 3)
		w.install(nil)
		w.matchingGoalTurn()

		result := w.dispatch(UpdateToolName, map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionComplete,
		})
		if result.IsError {
			t.Fatalf("派发失败：%+v", result.Error)
		}
		if len(result.AdditionalContexts) != 1 {
			t.Fatalf("捎出去 %d 条上下文", len(result.AdditionalContexts))
		}
		message := result.AdditionalContexts[0]
		text := textOf(message.Content)
		if !strings.Contains(text, "<goal_complete>") ||
			!strings.Contains(text, `Objective: "ship the thing"`) ||
			!strings.Contains(text, completeInstruction) {
			t.Fatalf("那条收尾指令排成了：\n%s", text)
		}
		source, ok := message.Source.(llm.PluginSource)
		if !ok || source.Plugin != PluginName {
			t.Fatalf("那条消息的来源是 %+v", message.Source)
		}
		notice, ok := source.Context.(llm.NoticeContext)
		if !ok || notice.Summary != "complete: ship the thing" {
			t.Fatalf("那行陈述是 %+v", source.Context)
		}
	})

	t.Run("自动轮次里的 blocked", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.service.view = activeView("ship the thing", 3)
		w.install(nil)
		w.matchingGoalTurn()

		result := w.dispatch(UpdateToolName, map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionBlocked,
			"blocked_reason": "upstream is down",
		})
		if result.IsError {
			t.Fatalf("派发失败：%+v", result.Error)
		}
		text := textOf(result.AdditionalContexts[0].Content)
		if !strings.Contains(text, "<goal_blocked>") ||
			!strings.Contains(text, `Blocked: "upstream is down"`) ||
			!strings.Contains(text, blockedInstruction) {
			t.Fatalf("那条收尾指令排成了：\n%s", text)
		}
	})

	t.Run("直接人类回合里不捎", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.install(nil)
		w.humanTurn()

		result := w.dispatch(UpdateToolName, map[string]any{
			"goal_id": "g1", "revision": 1, "action": actionComplete,
		})
		if result.IsError {
			t.Fatalf("派发失败：%+v", result.Error)
		}
		if len(result.AdditionalContexts) != 0 {
			t.Fatalf("人类回合里还捎了 %d 条上下文", len(result.AdditionalContexts))
		}
	})
}

// ---- 输出与呈现 ----

// TestTheOutputIsNotHTMLEscaped 钉住那条不转义：目标描述是人写的自由文本，`<` 变成
// 反斜杠 u003c 之后，模型看见的是一句和原文长得不一样的话。
func TestTheOutputIsNotHTMLEscaped(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.view = activeView(`fix <div> & "quotes"`, 0)
	w.install(nil)
	w.humanTurn()

	result := w.dispatch(GetToolName, map[string]any{})
	if result.IsError {
		t.Fatalf("派发失败：%+v", result.Error)
	}
	text := textOf(result.Content)
	for _, escaped := range []string{"u003c", "u003e", "u0026"} {
		if strings.Contains(text, escaped) {
			t.Fatalf("输出被转义了：%s", text)
		}
	}
	if !strings.Contains(text, `fix <div> & \"quotes\"`) {
		t.Fatalf("原文没原样排出来：%s", text)
	}
}

// TestWrapupContextQuotesWithoutEscaping 钉住收尾指令里那两处引号：它和输出走的是
// 同一条不转义的路。
func TestWrapupContextQuotesWithoutEscaping(t *testing.T) {
	t.Parallel()

	text := textOf(renderWrapupContext(`fix <div> & "quotes"`, ""))
	if strings.Contains(text, "u003c") || strings.Contains(text, "u0026") {
		t.Fatalf("收尾指令被转义了：%s", text)
	}
}

// TestGoalOutputRejectsAValueItCannotDecode 钉住那条解不开就报上去的路：一份排不成
// 那个形状的值不许被当成一次成功渲染。
func TestGoalOutputRejectsAValueItCannotDecode(t *testing.T) {
	t.Parallel()

	if _, err := goalOutput().Render(nil, json.RawMessage(`[`)); err == nil {
		t.Fatal("坏值还渲染得出来")
	}
}

// TestPresentCallShowsWhatTheCallIsDoing 钉住那三张卡片的标题和那条优先级：卡片说的
// 是这次调用要**做**什么，不是目标此刻是什么状态。
func TestPresentCallShowsWhatTheCallIsDoing(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)

	t.Run("get_goal 不放原始输入", func(t *testing.T) {
		t.Parallel()
		view, ok := controller.newGetTool().PresentCall(json.RawMessage(`{}`)).(tools.GenericCallView)
		if !ok {
			t.Fatal("卡片不是通用卡片")
		}
		if view.Title != "Read current goal" || view.Kind != tools.CallRead || view.RawInput != nil {
			t.Fatalf("卡片是 %+v", view)
		}
	})

	t.Run("create_goal 放那句目标描述", func(t *testing.T) {
		t.Parallel()
		view, ok := controller.newCreateTool().
			PresentCall(json.RawMessage(`{"objective":"ship it"}`)).(tools.GenericCallView)
		if !ok {
			t.Fatal("卡片不是通用卡片")
		}
		if view.Title != "Create goal" || string(view.RawInput) != `"ship it"` {
			t.Fatalf("卡片是 %+v", view)
		}
	})

	cases := map[string]struct {
		args      string
		wantTitle string
		wantInput string
	}{
		"edit 摆新描述": {
			args:      `{"action":"edit","goal_id":"g1","objective":"ship it better"}`,
			wantTitle: "Edit goal",
			wantInput: `"ship it better"`,
		},
		"edit 只改上限就摆上限": {
			args:      `{"action":"edit","goal_id":"g1","max_goal_rounds":9}`,
			wantTitle: "Edit goal",
			wantInput: `9`,
		},
		"pause 摆目标 id": {
			args:      `{"action":"pause","goal_id":"g1"}`,
			wantTitle: "Pause goal",
			wantInput: `"g1"`,
		},
		"resume 摆目标 id": {
			args:      `{"action":"resume","goal_id":"g1"}`,
			wantTitle: "Resume goal",
			wantInput: `"g1"`,
		},
		"complete 摆目标 id": {
			args:      `{"action":"complete","goal_id":"g1"}`,
			wantTitle: "Complete goal",
			wantInput: `"g1"`,
		},
		"blocked 摆那条原因": {
			args:      `{"action":"blocked","goal_id":"g1","blocked_reason":"upstream is down"}`,
			wantTitle: "Mark goal",
			wantInput: `"upstream is down"`,
		},
		"action 是空的": {
			args:      `{"action":"","goal_id":"g1"}`,
			wantTitle: " goal",
			wantInput: `"g1"`,
		},
		"参数整个解不开": {
			args:      `[`,
			wantTitle: " goal",
			wantInput: `""`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			card := controller.newUpdateTool().PresentCall(json.RawMessage(testCase.args))
			view, ok := card.(tools.GenericCallView)
			if !ok {
				t.Fatal("卡片不是通用卡片")
			}
			if view.Title != testCase.wantTitle {
				t.Fatalf("标题是 %q，本该是 %q", view.Title, testCase.wantTitle)
			}
			if string(view.RawInput) != testCase.wantInput {
				t.Fatalf("原始输入是 %s，本该是 %s", view.RawInput, testCase.wantInput)
			}
		})
	}
}

// ---- 装上去和摘下来 ----

// TestInstallAddsTheGuidanceAndTheThreeTools 钉住那条装配次序和那条摘干净。
func TestInstallAddsTheGuidanceAndTheThreeTools(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(func(config *Config) { config.BlockedAfterConsecutiveRounds = 4 })
	undo, err := controller.Install(t.Context(), w.root, w.deps())
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}

	names := []string{GetToolName, CreateToolName, UpdateToolName}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); !ok {
			t.Fatalf("%s 没装上", name)
		}
	}
	assembly, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.root.Key()})
	if err != nil {
		t.Fatalf("装配提示词失败：%v", err)
	}
	prompt, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		t.Fatalf("渲染提示词失败：%v", err)
	}
	if !strings.Contains(prompt, "at least 4 consecutive goal rounds") {
		t.Fatalf("那段指引没带着部署方的闸位进提示词：\n%s", prompt)
	}

	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); ok {
			t.Fatalf("%s 摘掉之后还在", name)
		}
	}
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
			if _, err := w.controller(nil).Install(t.Context(), w.root, deps); err == nil {
				t.Fatal("缺件还装得上")
			}
		})
	}
}

// TestInstallUnwindsWhatItAlreadyPutUp 钉住那条反序摘除：半装上去意味着模型手上有
// 一件建得了目标、却没有那段策略指引管着的工具。
func TestInstallUnwindsWhatItAlreadyPutUp(t *testing.T) {
	t.Parallel()

	t.Run("段落名被占了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		release, err := w.prompts.Section(t.Context(), w.root, systemprompt.PromptSection{
			Name: SectionName,
			Text: systemprompt.StaticText("someone else got here first"),
		})
		if err != nil {
			t.Fatalf("占段落名失败：%v", err)
		}
		t.Cleanup(func() { _ = release(context.Background()) })

		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("段落名被占了还装得上")
		}
		for _, name := range []string{GetToolName, CreateToolName, UpdateToolName} {
			if _, ok := w.tools.Get(name, w.root.Key()); ok {
				t.Fatalf("装失败了 %s 还在", name)
			}
		}
	})

	t.Run("工具名被占了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		occupied := &tools.Definition{
			Name:       UpdateToolName,
			Parameters: tools.Node{Type: tools.TypeObject},
			Output: tools.OutputDefinition{
				Schema: tools.Node{Type: tools.TypeObject},
				Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
					return llm.Content{llm.TextBlock{Text: "occupied"}}, nil
				},
			},
			Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
				return json.RawMessage(`{}`), nil
			},
		}
		remove, err := w.tools.Register(t.Context(), w.root, occupied)
		if err != nil {
			t.Fatalf("占名失败：%v", err)
		}
		t.Cleanup(func() { _ = remove(context.Background()) })

		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("名字被占了还装得上")
		}
		for _, name := range []string{GetToolName, CreateToolName} {
			if _, ok := w.tools.Get(name, w.root.Key()); ok {
				t.Fatalf("装失败了 %s 还在", name)
			}
		}
		assembly, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.root.Key()})
		if err != nil {
			t.Fatalf("装配提示词失败：%v", err)
		}
		prompt, err := systemprompt.RenderPrompt(assembly)
		if err != nil {
			t.Fatalf("渲染提示词失败：%v", err)
		}
		if strings.Contains(prompt, "consecutive goal rounds") {
			t.Fatalf("装失败了那段指引还留在提示词里：\n%s", prompt)
		}
	})
}

// ---- 不变量 ----

// 这个包占的是一个空位置。用例只钉两件事：这个位置真的占得下来（于是「检查过了、
// 结论是无需检查」和「这个包被漏掉了」区分得开），以及缺注册表时不假装占到了。
func TestRegisterInvariantsClaimsThePackageNameWithNoCheck(t *testing.T) {
	t.Parallel()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	release, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记不变量失败：%v", err)
	}
	release()

	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表该装不上")
	}
}

func (a *stubAgent) Remove(llm.MessageID) {}

func (a *stubAgent) Replace(llm.MessageID, llm.Message) {}
