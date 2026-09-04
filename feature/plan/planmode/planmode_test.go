// 本文件的作用：把带状态的那一侧钉住——一次选择在什么时候落进日志、什么时候挂起、
// 那段指引跟着谁走、退出工具的五道门、以及 `/plan` 四种结局各回哪句话。
//
// 逐条对着 DSH 的 tests/plan-mode.spec.ts 里那几组带装配的用例走。纯回放那一侧
// （折叠、投影、不变量）在 projection_test.go。
//
// # 这些测试防的是什么错
//
//   - **一次选择在回合中途就落进了日志**。那会让正在跑的那一步的请求装配看见一个
//     和它开工时不一样的模式，模型手上的指引和它刚被告知的那句话对不上。
//   - **旁白没跟上**。用户在界面上按的那一下模型看不见；不补一句的话，模型会继续
//     按上一次被告知的模式办事，而用户以为自己已经切过去了。
//   - **退出工具在计划模式关着的时候能调**。那等于给了模型一条无中生有的退出路径。
//   - **一次「继续规划」被读成了同意**。用户的反馈被吞掉、计划模式还退了，是这
//     整件事里最坏的一种误读。
//   - **`/plan` 的四种结局说成同一句话**。「已经关了」和「正在关，下一步生效」对
//     用户是两件事，合成一句会让他以为自己那一下没按上。
package planmode_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/feature/interaction/userquestions"
	"github.com/snight1983/ds-harness-go/feature/plan/planmode"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
	"github.com/snight1983/ds-harness-go/tools"
)

// testSection 是这些用例里那段部署方拥有的计划指引。
//
// 内容不重要，重要的是它非空且认得出来：这些用例只问「它在不在这次装配里」。
const testSection = "PLAN POLICY: think first, do not edit."

// stubAgent 是一个只把会话摆在那儿、并把 Steer/Inject 记下来的假 agent。
//
// 记下这两条路是本文件的核心手段：旁白和 `/plan` 带的那段话都不落日志，
// 只能从这里看见。
type stubAgent struct {
	id       sessionlog.SessionID
	owner    *scope.Scope
	sess     *coresession.Session
	steered  []llm.Message
	injected []llm.Message
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.owner }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool) {}
func (a *stubAgent) Followup(llm.Message)                      {}
func (a *stubAgent) Steer(message llm.Message)                 { a.steered = append(a.steered, message) }
func (a *stubAgent) Inject(message llm.Message)                { a.injected = append(a.injected, message) }
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)    {}

// append 往这个 agent 的日志里写一条事件。
func (a *stubAgent) append(t *testing.T, event sessionlog.Event) {
	t.Helper()
	if _, err := a.sess.Append(event); err != nil {
		t.Fatalf("追加 %s 失败：%v", event.Type, err)
	}
}

// world 是一次带装配的用例要的全部家当。
//
// 五个注册表都是真的：这些用例问的正是「装上去之后别人看见了什么」，
// 用假注册表的话钉住的就只是本包内部的调用次序。
type world struct {
	t           *testing.T
	root        *scope.Scope
	agents      *agent.Registry
	tools       *tools.Runtime
	prompts     *systemprompt.Registry
	commands    *commands.Runtime
	projections *projection.Registry
	questions   *userquestions.Service
	controller  *planmode.Controller
	agent       *stubAgent

	// answer 是提问服务下一次要交出来的答案。
	answer userquestions.Answer
	// askErr 非 nil 时提问服务当场失败，answer 不算数。
	askErr error
	// asked 记下这道评审被问出去的那一份请求。
	asked *userquestions.Request
}

// Ask 让 world 自己扮那个提问提供方。
func (w *world) Ask(_ context.Context, request userquestions.Request) (userquestions.Answer, error) {
	copied := request
	w.asked = &copied
	if w.askErr != nil {
		return userquestions.Answer{}, w.askErr
	}
	return w.answer, nil
}

// Append 让 world 自己扮命令执行器要的那条会话日志。
func (w *world) Append(kind sessionlog.EventType, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.agent.sess.Append(sessionlog.Event{Type: kind, Data: payload})
	return err
}

// newWorld 把控制器和五个注册表一次装齐。
func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()

	root, err := scope.New(scope.NewKey("root"), scope.Options{})
	if err != nil {
		t.Fatalf("造根作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = root.Dispose(context.Background()) })

	agentScope, err := scope.New(scope.NewKey("agent"), scope.Options{Parent: root.Key()})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = agentScope.Dispose(context.Background()) })

	const id sessionlog.SessionID = "s1"
	sess, err := coresession.NewSession(id, coresession.Options{})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	stub := &stubAgent{id: id, owner: agentScope, sess: sess}

	w := &world{t: t, root: root, agent: stub}

	w.agents, err = agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	detach, err := w.agents.Register(ctx, stub, nil)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	w.tools, err = tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具注册表失败：%v", err)
	}
	w.prompts, err = systemprompt.NewRegistry(ctx, root, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	w.commands, err = commands.NewRuntime(commands.Options{
		LogOf: func(*scope.Key) (commands.Log, error) { return w, nil },
	})
	if err != nil {
		t.Fatalf("造命令注册表失败：%v", err)
	}
	w.projections = projection.NewRegistry()

	w.questions = userquestions.New(userquestions.Config{
		CallerStatus: func(*scope.Key) userquestions.CallerStatus { return userquestions.CallerRoot },
	})
	release, err := w.questions.RegisterProvider(w)
	if err != nil {
		t.Fatalf("登记提问提供方失败：%v", err)
	}
	t.Cleanup(release)

	w.controller, err = planmode.New(planmode.Config{
		Section: testSection,
		AgentOf: func(key *scope.Key) (agent.Agent, error) {
			if key == agentScope.Key() {
				return stub, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	return w
}

// install 把五条胳膊装上去，返回摘除函数。
func (w *world) install() func() {
	w.t.Helper()
	undo, err := w.controller.Install(w.t.Context(), w.root, w.deps())
	if err != nil {
		w.t.Fatalf("装控制器失败：%v", err)
	}
	w.t.Cleanup(func() { _ = undo(context.Background()) })
	return func() { _ = undo(context.Background()) }
}

// deps 是这次装配那份完整的协作者。
func (w *world) deps() planmode.Deps {
	return planmode.Deps{
		Agents:      w.agents,
		Tools:       w.tools,
		Prompts:     w.prompts,
		Commands:    w.commands,
		Projections: w.projections,
		Questions:   w.questions,
	}
}

// key 是这个 agent 那把作用域钥匙。
func (w *world) key() *scope.Key { return w.agent.owner.Key() }

// view 把这个会话当下的日志包成投影注册表读得懂的视图。
//
// 活会话本身不是 [projection.SessionView]（它没有 NextSeq），所以这里借
// projection_test.go 里那个只读的壳。
func (w *world) view() *fakeSession {
	return &fakeSession{id: w.agent.id, events: w.agent.sess.Events()}
}

// modes 交出日志里那几条 plan/mode 记下的状态，按次序。
func (w *world) modes() []bool {
	var states []bool
	for _, event := range w.agent.sess.Events() {
		if event.Type != planmode.EventMode {
			continue
		}
		var payload planmode.ModeData
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			w.t.Fatalf("读 plan/mode 负载失败：%v", err)
		}
		states = append(states, payload.Active)
	}
	return states
}

// planSection 装一次系统提示词，交出 `plan:policy` 那一段的正文。
func (w *world) planSection(key *scope.Key) string {
	w.t.Helper()
	assembly, err := w.prompts.Assemble(w.t.Context(), systemprompt.AssembleContext{Scope: key})
	if err != nil {
		w.t.Fatalf("装提示词失败：%v", err)
	}
	for _, section := range assembly.Sections {
		if section.Name == "plan:policy" {
			return section.Text
		}
	}
	return ""
}

// runPreStep 跑一次步骤准入瀑布，里层交出调用方给的那个提议。
func (w *world) runPreStep(enter bool) agent.PreStepDecision {
	w.t.Helper()
	decision, err := w.agents.ResolvePreStep(
		w.t.Context(),
		agent.PreStep{Agent: w.agent},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.PreStepDecision{Enter: enter}, nil
		},
	)
	if err != nil {
		w.t.Fatalf("跑步骤准入失败：%v", err)
	}
	return decision
}

// runExit 调一次退出工具。
func (w *world) runExit(plan string) tools.Result {
	w.t.Helper()
	args, err := json.Marshal(map[string]string{"plan": plan})
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	return w.tools.Execute(w.t.Context(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      planmode.ExitToolName,
		Arguments: args,
		Agent:     w.key(),
	})
}

// runSlash 跑一行 `/plan`。
func (w *world) runSlash(line string) commands.Result {
	w.t.Helper()
	execution, err := w.commands.Execute(w.t.Context(), w.key(), line, nil)
	if err != nil {
		w.t.Fatalf("跑命令失败：%v", err)
	}
	if execution == nil {
		w.t.Fatalf("这一行该被认成一条命令：%q", line)
	}
	return execution.Result
}

// approval 造一份「同意」的答案。
func approval() userquestions.Answer {
	return userquestions.Answer{Answers: []userquestions.AnswerItem{{
		ID:       "plan-review",
		Selected: []string{"Approve"},
	}}}
}

// text 把一段内容拍平成一句话，方便断言。
func text(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if textBlock, ok := block.(llm.TextBlock); ok {
			parts = append(parts, textBlock.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// openTurn 往日志里开一个回合。
func openTurn(t *testing.T, w *world) {
	t.Helper()
	w.agent.append(t, sessionlog.Event{Type: sessionlog.EventTurnStart})
}

func TestNewRejectsAConfigThatCannotWork(t *testing.T) {
	t.Parallel()

	agentOf := func(*scope.Key) (agent.Agent, error) { return nil, nil }
	// 一段空指引意味着计划模式开着却什么都没告诉模型，那和没开的唯一区别是
	// 用户以为它开着。全是空白同理。
	for _, blank := range []string{"", "   \n\t "} {
		if _, err := planmode.New(planmode.Config{Section: blank, AgentOf: agentOf}); !errors.Is(err, planmode.ErrInvalidConfig) {
			t.Fatalf("空指引 %q 该被拒，拿到 %v", blank, err)
		}
	}
	if _, err := planmode.New(planmode.Config{Section: testSection}); !errors.Is(err, planmode.ErrInvalidConfig) {
		t.Fatalf("没有找 agent 的路该被拒，拿到 %v", err)
	}
	if _, err := planmode.New(planmode.Config{Section: testSection, AgentOf: agentOf}); err != nil {
		t.Fatalf("一份齐全的配置不该被拒：%v", err)
	}
}

func TestInstallNeedsTheThreeRequiredRegistries(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	for _, missing := range []struct {
		what string
		deps planmode.Deps
	}{
		{"agent 注册表", planmode.Deps{Tools: w.tools, Prompts: w.prompts}},
		{"工具注册表", planmode.Deps{Agents: w.agents, Prompts: w.prompts}},
		{"提示词注册表", planmode.Deps{Agents: w.agents, Tools: w.tools}},
	} {
		_, err := w.controller.Install(t.Context(), w.root, missing.deps)
		if !errors.Is(err, planmode.ErrInvalidConfig) {
			t.Fatalf("缺%s该装不上，拿到 %v", missing.what, err)
		}
	}
}

func TestTheThreeOptionalArmsCanBeAbsent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	// 一个没有命令入口、没有投影、也没有人能评审的装配（无头的、只跑模型的）
	// 不该因此整个装不上。
	undo, err := w.controller.Install(t.Context(), w.root, planmode.Deps{
		Agents:  w.agents,
		Tools:   w.tools,
		Prompts: w.prompts,
	})
	if err != nil {
		t.Fatalf("只给三个必需的该装得上：%v", err)
	}
	// 工具表在进出计划模式时必须纹丝不动，所以退出工具照样在。
	if _, ok := w.tools.Get(planmode.ExitToolName, w.key()); !ok {
		t.Fatal("退出工具该一直挂在工具表上")
	}
	// 没有投影注册表时 plan 这个键整个不在，而不是取某个值。
	if _, ok := w.projections.StateOf(w.view(), planmode.ProjectionKey); ok {
		t.Fatal("没给投影注册表时 plan 这个键不该在")
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘除不该失败：%v", err)
	}
}

func TestUndoTakesEveryArmBackOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	undo := w.install()

	if _, ok := w.tools.Get(planmode.ExitToolName, w.key()); !ok {
		t.Fatal("装上之后退出工具该在")
	}
	if _, ok := w.commands.Find(w.key(), planmode.CommandName); !ok {
		t.Fatal("装上之后 /plan 该在")
	}
	undo()
	if _, ok := w.tools.Get(planmode.ExitToolName, w.key()); ok {
		t.Fatal("摘掉之后退出工具不该还在")
	}
	if _, ok := w.commands.Find(w.key(), planmode.CommandName); ok {
		t.Fatal("摘掉之后 /plan 不该还在")
	}
	if _, ok := w.projections.StateOf(w.view(), planmode.ProjectionKey); ok {
		t.Fatal("摘掉之后 plan 这个键不该还在")
	}
}

func TestAControllerCannotBeInstalledAgainAfterUndo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()()

	// disposed 是整个值上的一面旗：装第二份的话，摘掉其中一份就会让另一份的
	// 评审全部开始报「服务已经被重载」。
	if _, err := w.controller.Install(t.Context(), w.root, w.deps()); !errors.Is(err, planmode.ErrInvalidConfig) {
		t.Fatalf("摘掉之后不该再装得上，拿到 %v", err)
	}
}

func TestSetBetweenTurnsCommitsRightAway(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	outcome, err := w.controller.Set(w.agent, true)
	if err != nil {
		t.Fatalf("切换不该失败：%v", err)
	}
	if outcome != planmode.OutcomeCommitted {
		t.Fatalf("回合之间该当场落盘，拿到 %v", outcome)
	}
	// 回合之间没有下一个回合之内的步骤前置来接这次选择，所以它只能立刻写下去。
	if got := w.modes(); len(got) != 1 || !got[0] {
		t.Fatalf("日志上该恰好有一条开着的 plan/mode，拿到 %v", got)
	}
	if state := w.controller.Get(w.agent); !state.Active || state.Pending != nil {
		t.Fatalf("落盘之后不该还挂着，拿到 %+v", state)
	}
	// 第一次请求之前不通知：模型还没被告知过任何模式，没有可纠正的印象。
	if len(w.agent.injected) != 0 {
		t.Fatalf("第一条请求头之前不该有旁白，拿到 %d 条", len(w.agent.injected))
	}
}

func TestSetNarratesOnceTheModelHasBeenToldAMode(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	// 一条请求头就是「模型上一次被告知的是哪一种模式」那个基准。
	w.agent.append(t, sessionlog.Event{Type: sessionlog.EventRequestHeader})
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("切换不该失败：%v", err)
	}
	if len(w.agent.injected) != 1 {
		t.Fatalf("该补一句旁白，拿到 %d 条", len(w.agent.injected))
	}
	if got := text(w.agent.injected[0].Content); got != "The user switched this session to plan mode." {
		t.Fatalf("旁白说错了：%q", got)
	}

	// 再切回去，旁白反过来说。
	w.agent.append(t, sessionlog.Event{Type: sessionlog.EventRequestHeader})
	if _, err := w.controller.Set(w.agent, false); err != nil {
		t.Fatalf("切回去不该失败：%v", err)
	}
	if len(w.agent.injected) != 2 {
		t.Fatalf("切回去也该补一句，拿到 %d 条", len(w.agent.injected))
	}
	if got := text(w.agent.injected[1].Content); got != "The user switched this session back to the default mode." {
		t.Fatalf("反向旁白说错了：%q", got)
	}
}

func TestSetInsideAnOpenTurnWaitsForTheStepBoundary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	w.agent.append(t, sessionlog.Event{Type: sessionlog.EventRequestHeader})
	openTurn(t, w)

	outcome, err := w.controller.Set(w.agent, true)
	if err != nil {
		t.Fatalf("切换不该失败：%v", err)
	}
	if outcome != planmode.OutcomeQueued {
		t.Fatalf("回合之内该挂起，拿到 %v", outcome)
	}
	// 一次选择在回合中途落进日志，会让正在跑的那一步的请求装配看见一个和它
	// 开工时不一样的模式。所以这里日志上必须什么都还没有。
	if got := w.modes(); len(got) != 0 {
		t.Fatalf("挂起期间不该写日志，拿到 %v", got)
	}
	if len(w.agent.injected) != 0 {
		t.Fatalf("挂起期间不该旁白，拿到 %d 条", len(w.agent.injected))
	}

	// 一个被拒的步骤接不住它：那一步不会有请求装配，指引没地方生效。
	if decision := w.runPreStep(false); decision.Enter {
		t.Fatal("里层说不进就是不进")
	}
	if got := w.modes(); len(got) != 0 {
		t.Fatalf("被拒的步骤不该落盘，拿到 %v", got)
	}

	// 下一个被接受的步骤前置才落盘，并且把旁白挂进这一步的消息里。
	decision := w.runPreStep(true)
	if !decision.Enter {
		t.Fatal("里层说进就该进")
	}
	if got := w.modes(); len(got) != 1 || !got[0] {
		t.Fatalf("步骤开头该落一条开着的 plan/mode，拿到 %v", got)
	}
	if len(decision.Messages) != 1 {
		t.Fatalf("该把旁白挂进这一步，拿到 %d 条", len(decision.Messages))
	}
	if got := text(decision.Messages[0].Content); got != "The user switched this session to plan mode." {
		t.Fatalf("旁白说错了：%q", got)
	}
	if state := w.controller.Get(w.agent); !state.Active || state.Pending != nil {
		t.Fatalf("落盘之后不该还挂着，拿到 %+v", state)
	}
}

func TestSelectingTheStateThatIsAlreadyPendingIsANoop(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	openTurn(t, w)

	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("第一次不该失败：%v", err)
	}
	outcome, err := w.controller.Set(w.agent, true)
	if err != nil {
		t.Fatalf("第二次不该失败：%v", err)
	}
	if outcome != planmode.OutcomeNoop {
		t.Fatalf("重复选已经挂着的那个状态是空操作，拿到 %v", outcome)
	}
}

func TestSelectingBackTheLoggedStateCancelsThePendingOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	openTurn(t, w)

	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("挂起不该失败：%v", err)
	}
	// 日志上本来就是关着的，所以这一下是把那次挂起的进入撤掉，不是一次新的退出。
	outcome, err := w.controller.Set(w.agent, false)
	if err != nil {
		t.Fatalf("撤销不该失败：%v", err)
	}
	if outcome != planmode.OutcomeCancelled {
		t.Fatalf("方向相反的那一下该读作撤销，拿到 %v", outcome)
	}
	// 撤销之后步骤前置不该再写任何东西：那次选择已经不存在了。
	w.runPreStep(true)
	if got := w.modes(); len(got) != 0 {
		t.Fatalf("撤销之后不该落盘，拿到 %v", got)
	}
}

func TestSetRejectsAMissingAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.controller.Set(nil, true); err == nil {
		t.Fatal("没有 agent 该报错")
	}
	if state := w.controller.Get(nil); state.Active || state.Pending != nil {
		t.Fatalf("没有 agent 时读数该是零值，拿到 %+v", state)
	}
}

func TestDisposingASessionDropsItsPendingSelection(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	openTurn(t, w)

	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("挂起不该失败：%v", err)
	}
	w.controller.OnSessionDisposed(w.agent.id)
	if state := w.controller.Get(w.agent); state.Pending != nil {
		t.Fatalf("会话散掉之后不该还留着挂起，拿到 %+v", state)
	}
}

func TestThePolicySectionFollowsTheSelectedState(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	if got := w.planSection(w.key()); got != "" {
		t.Fatalf("关着的时候不该有指引，拿到 %q", got)
	}

	openTurn(t, w)
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("挂起不该失败：%v", err)
	}
	// 挂起的选择盖过日志：一次在回合中途选下的进入，从**下一次**装配起就要带上
	// 指引，而那次装配就发生在把它落盘的那个步骤前置紧后面。
	if got := w.planSection(w.key()); got != testSection {
		t.Fatalf("挂起的进入该让指引立刻出现，拿到 %q", got)
	}
}

func TestThePolicySectionIsAbsentWhenTheAssemblyHasNoAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	openTurn(t, w)
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("挂起不该失败：%v", err)
	}

	// 认不出这次装配算谁的：那种装配没有会话，也就没有可折的计划状态。
	if got := w.planSection(nil); got != "" {
		t.Fatalf("没有作用域时该贡献空串，拿到 %q", got)
	}
	if got := w.planSection(scope.NewKey("stranger")); got != "" {
		t.Fatalf("认不出的钥匙该贡献空串，拿到 %q", got)
	}
}

func TestTheExitToolIsOnlyAvailableInPlanMode(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	result := w.runExit("# A plan\n\nDo the thing.")
	if !result.IsError {
		t.Fatal("计划模式关着的时候这件工具该失败")
	}
	if !strings.Contains(result.Error.Message, "only available in plan mode") {
		t.Fatalf("该说清楚为什么，拿到 %q", result.Error.Message)
	}
	if w.asked != nil {
		t.Fatal("这道门在提问之前，不该已经问出去了")
	}
}

func TestTheExitToolNeedsAPlanThatStartsWithAHeading(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}

	// 一份从二级标题起头的计划是模型没有照说明写，当场退回去比让它进评审好——
	// 用户会看见一张标题空着的卡片。
	for _, bad := range []string{"", "   ", "no heading at all", "## A plan", "#no space"} {
		result := w.runExit(bad)
		if !result.IsError {
			t.Fatalf("计划 %q 该被退回", bad)
		}
		if !strings.Contains(result.Error.Message, "markdown plan starting with a # heading") {
			t.Fatalf("该说清楚要什么，拿到 %q", result.Error.Message)
		}
	}
	if w.asked != nil {
		t.Fatal("这道门也在提问之前")
	}
}

func TestApprovingThePlanQueuesTheExitWithoutNarrating(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	w.agent.append(t, sessionlog.Event{Type: sessionlog.EventRequestHeader})
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}
	w.agent.injected = nil
	w.answer = approval()

	result := w.runExit("# Ship it\n\nStep one.")
	if result.IsError {
		t.Fatalf("一次干净的同意不该失败：%+v", result.Error)
	}
	var value struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("读返回值失败：%v", err)
	}
	if !value.Approved {
		t.Fatalf("这件工具唯一的成功走向就是被同意，拿到 %s", result.Value)
	}

	// 计划指引在这一批工具调用剩下的部分里继续有效：日志上还开着。
	if got := w.modes(); len(got) != 1 || !got[0] {
		t.Fatalf("同意那一刻不该落盘，拿到 %v", got)
	}
	state := w.controller.Get(w.agent)
	if state.Pending == nil || *state.Pending {
		t.Fatalf("该挂着一次退出，拿到 %+v", state)
	}

	// 落盘在下一个被接受的步骤前置上，而且**不**旁白：那次调用的结果本身已经把
	// 这件事讲清楚了，再补一句是同一件事说两遍。
	decision := w.runPreStep(true)
	if got := w.modes(); len(got) != 2 || got[1] {
		t.Fatalf("步骤开头该落一条关着的 plan/mode，拿到 %v", got)
	}
	if len(decision.Messages) != 0 {
		t.Fatalf("退出工具那一次不该旁白，拿到 %d 条", len(decision.Messages))
	}
	if len(w.agent.injected) != 0 {
		t.Fatalf("退出工具那一次不该注入，拿到 %d 条", len(w.agent.injected))
	}
}

func TestThePlanReviewCarriesThePlanAndTheApproveIntent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}
	w.answer = approval()

	const plan = "# Ship it\n\nStep one."
	if result := w.runExit(plan); result.IsError {
		t.Fatalf("不该失败：%+v", result.Error)
	}
	if w.asked == nil || len(w.asked.Questions) != 1 {
		t.Fatalf("该恰好问一道题，拿到 %+v", w.asked)
	}
	item := w.asked.Questions[0]
	if item.Detail != plan {
		t.Fatalf("整份计划该原样摆给用户，拿到 %q", item.Detail)
	}
	if len(item.Options) != 2 || item.Options[0].Label != "Approve" || item.Options[1].Label != "Keep planning" {
		t.Fatalf("两个选项该是同意和继续规划，拿到 %+v", item.Options)
	}
	// 那个标签只能有一处定义：两边写岔了的话，界面会把一次同意画成同意、
	// 而本包读成「继续规划」。
	intent, ok := item.Intent.(userquestions.PlanReviewIntent)
	if !ok {
		t.Fatalf("该带上计划裁决这个呈现标记，拿到 %T", item.Intent)
	}
	if intent.Approve != item.Options[0].Label {
		t.Fatalf("标记里那个同意标签该和选项一致：%q vs %q", intent.Approve, item.Options[0].Label)
	}
}

func TestKeepPlanningComesBackAsAnErrorTheModelCanRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}

	cases := []struct {
		what   string
		answer userquestions.Answer
		want   string
	}{
		{
			"选了继续规划",
			userquestions.Answer{Answers: []userquestions.AnswerItem{{ID: "plan-review", Selected: []string{"Keep planning"}}}},
			"The user chose to keep planning; revise the plan and present it again.",
		},
		{
			"一道题都没回",
			userquestions.Answer{},
			"The user chose to keep planning; revise the plan and present it again.",
		},
		{
			// 用户写下那句话就是要模型先读它，把它连同一次退出一起吞掉是最坏的一种误读。
			"同意但带着一句反馈",
			userquestions.Answer{Answers: []userquestions.AnswerItem{{
				ID: "plan-review", Selected: []string{"Approve"}, Custom: "先把迁移拆出来",
			}}},
			"The user chose to keep planning; their feedback: 先把迁移拆出来",
		},
		{
			"同一道评审回来两条",
			userquestions.Answer{Answers: []userquestions.AnswerItem{
				{ID: "plan-review", Selected: []string{"Approve"}},
				{ID: "plan-review", Selected: []string{"Approve"}},
			}},
			"The user chose to keep planning; revise the plan and present it again.",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.what, func(t *testing.T) {
			w.answer = testCase.answer
			result := w.runExit("# Ship it\n\nStep one.")
			if !result.IsError {
				t.Fatal("不是一次干净的同意就该走错误那条路")
			}
			if result.Error.Message != testCase.want {
				t.Fatalf("想要 %q，拿到 %q", testCase.want, result.Error.Message)
			}
			// 没同意就什么都不该挂起。
			if state := w.controller.Get(w.agent); state.Pending != nil {
				t.Fatalf("没同意不该挂起一次退出，拿到 %+v", state)
			}
		})
	}
}

func TestADismissedReviewTellsTheModelToWait(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}
	// 一次被撤掉的评审不是一次失败的评审：用户把发言权收回去，是要说那两个选项
	// 覆盖不了的话。通用那句话里点的是 ask_user_question 这个名字，而模型从来
	// 没调过它。
	w.askErr = &userquestions.Error{Code: userquestions.CodeAskCancelled, Message: "dismissed"}

	result := w.runExit("# Ship it\n\nStep one.")
	if !result.IsError {
		t.Fatal("被撤掉的评审该失败")
	}
	if !strings.Contains(result.Error.Message, "dismissed the plan review to speak instead") {
		t.Fatalf("该翻译成模型读得懂的一句话，拿到 %q", result.Error.Message)
	}
	if strings.Contains(result.Error.Message, "ask_user_question") {
		t.Fatalf("不该点一件模型没调过的工具的名字，拿到 %q", result.Error.Message)
	}
}

func TestAReviewThatOutlivesTheInstallationFails(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	undo := w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}
	w.answer = approval()

	// 一次评审可能活得比这次装配还长。没有了那条步骤前置，一个被同意的选择就
	// 再也没有机会落进日志，所以这里失败掉、让模型再摆一次。
	definition, ok := w.tools.Get(planmode.ExitToolName, w.key())
	if !ok {
		t.Fatal("退出工具该在")
	}
	args, err := json.Marshal(map[string]string{"plan": "# Ship it\n\nStep one."})
	if err != nil {
		t.Fatalf("排参数失败：%v", err)
	}
	undo()
	_, err = definition.Execute(t.Context(), args, &tools.RunContext{
		Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{Agent: w.key()}},
	})
	if err == nil {
		t.Fatal("摘掉之后这次评审该失败")
	}
	if !strings.Contains(err.Error(), "reloaded while the plan was under review") {
		t.Fatalf("该说清楚为什么，拿到 %q", err.Error())
	}
}

func TestTheExitToolRefusesAnAssemblyItCannotResolve(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	// 认不出那把钥匙和「没有调用方」是同一件事：两种情形下都没有可切换的会话。
	definition, ok := w.tools.Get(planmode.ExitToolName, w.key())
	if !ok {
		t.Fatal("退出工具该在")
	}
	args, err := json.Marshal(map[string]string{"plan": "# Ship it\n\nStep one."})
	if err != nil {
		t.Fatalf("排参数失败：%v", err)
	}
	for _, missing := range []struct {
		what string
		key  *scope.Key
	}{
		{"没有调用方", nil},
		{"认不出的钥匙", scope.NewKey("stranger")},
	} {
		_, err := definition.Execute(t.Context(), args, &tools.RunContext{
			Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{Agent: missing.key}},
		})
		if err == nil {
			t.Fatalf("%s该失败", missing.what)
		}
		if !strings.Contains(err.Error(), "requires a calling agent") {
			t.Fatalf("%s该说清楚，拿到 %q", missing.what, err.Error())
		}
	}
}

func TestTheExitToolFailsWhenNobodyCanReview(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	// 提问服务不在的时候退出工具**照样注册**——工具表在进出计划模式时必须纹丝不动，
	// 而「有没有人能评审」是调用那一刻才知道的事。
	deps := w.deps()
	deps.Questions = nil
	undo, err := w.controller.Install(t.Context(), w.root, deps)
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}

	result := w.runExit("# Ship it\n\nStep one.")
	if !result.IsError {
		t.Fatal("没人能评审时该失败")
	}
	// 那句失败要告诉模型改让用户自己切模式。
	if !strings.Contains(result.Error.Message, "switch the session mode instead") {
		t.Fatalf("该给模型一条出路，拿到 %q", result.Error.Message)
	}
}

func TestSlashPlanSaysSomethingDifferentForEachOutcome(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	// 回合之间：当场落盘。
	if got := w.runSlash("/plan"); got.Kind != commands.ResultSuccess || got.Text != "Plan mode on. Use /plan off to leave." {
		t.Fatalf("落盘的进入说错了：%+v", got)
	}
	if got := w.runSlash("/plan off"); got.Text != "Plan mode off." {
		t.Fatalf("落盘的退出说错了：%+v", got)
	}
	// 日志上就关着才读作幂等。
	if got := w.runSlash("/plan off"); got.Text != "Plan mode is already inactive." {
		t.Fatalf("幂等的退出说错了：%+v", got)
	}

	// 回合之内：挂起。「已经关了」和「正在关，下一步生效」对用户是两件事。
	openTurn(t, w)
	if got := w.runSlash("/plan"); got.Text != "Entering plan mode (applies from the next step). Use /plan off to leave." {
		t.Fatalf("挂起的进入说错了：%+v", got)
	}
	// 挂着一次进入的时候再 off，撤的是那次进入。
	if got := w.runSlash("/plan off"); got.Text != "Plan mode entry cancelled." {
		t.Fatalf("撤销说错了：%+v", got)
	}
}

func TestSlashPlanOffQueuesWhenPlanModeIsAlreadyLogged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}
	openTurn(t, w)

	if got := w.runSlash("/plan off"); got.Text != "Leaving plan mode (applies from the next step)." {
		t.Fatalf("挂起的退出说错了：%+v", got)
	}
	// 一次已经挂起的退出会让 Set 判成空操作（想要的状态和挂起的那个一样），
	// 但那时日志上仍然开着，回「已经关了」是错的。
	if got := w.runSlash("/plan off"); got.Text != "Leaving plan mode (applies from the next step)." {
		t.Fatalf("重复的退出也该这么说：%+v", got)
	}
}

func TestSlashPlanWithAMessageSteersIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	got := w.runSlash("/plan 先把迁移拆出来")
	if got.Kind != commands.ResultSuccess {
		t.Fatalf("带一段话的 /plan 不该失败：%+v", got)
	}
	// 走 Steer 而不是 Send：这段话是跟着这次模式切换一起来的同一个意图，它该在
	// 下一步就被读到，而不是排到收件箱后面去。
	if len(w.agent.steered) != 1 {
		t.Fatalf("那段话该被引导进去，拿到 %d 条", len(w.agent.steered))
	}
	if body := text(w.agent.steered[0].Content); body != "先把迁移拆出来" {
		t.Fatalf("引导的内容不对：%q", body)
	}
	if len(w.agent.injected) != 0 {
		t.Fatalf("这一次不该走注入，拿到 %d 条", len(w.agent.injected))
	}
}

func TestABareSlashPlanSteersNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()

	w.runSlash("/plan")
	if len(w.agent.steered) != 0 {
		t.Fatalf("裸的 /plan 没有话要带，拿到 %d 条", len(w.agent.steered))
	}
}

func TestOffIsTheOnlyWordThatLeaves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.install()
	if _, err := w.controller.Set(w.agent, true); err != nil {
		t.Fatalf("进计划模式失败：%v", err)
	}

	// `off` 之外的输入一律是「进入」，包括跟着一段话的那一次。
	got := w.runSlash("/plan offline 的时候怎么办")
	if got.Kind != commands.ResultSuccess {
		t.Fatalf("不该失败：%+v", got)
	}
	if planmode.FoldMode(w.agent.sess.Events()) != true {
		t.Fatal("这不是一次退出，计划模式该还开着")
	}
	if len(w.agent.steered) != 1 {
		t.Fatalf("那段话该被带进去，拿到 %d 条", len(w.agent.steered))
	}
}

func (a *stubAgent) Remove(llm.MessageID) {}

func (a *stubAgent) Replace(llm.MessageID, llm.Message) {}
