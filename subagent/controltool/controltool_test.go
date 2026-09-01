// 本文件的作用：把这三件控制工具钉在它们那几条真会出错的边上——装配面缺什么就不许
// 开工、「这次调用落在哪个孩子身上」那条钥匙查回去的路、投出去的收件人和归属确实到了
// 服务那边、打断凭的是那个确切的活调用方，以及列举那一步的投影和渲染。
//
// # 这些测试防的是什么错
//
//   - **权柄凭错了人**。send_message 的父权和 interrupt_agent 的祖先权都必须是
//     那个确切的活调用方；由参数或者别的什么东西决定收件人，等于谁都能操控谁。
//   - **一次性孩子漏进列表**。它接不上 send_message，摆给模型就是一条走不通的路。
//   - **状态读的是描述符而不是活登记**。一个已经不在登记里的孩子被说成 idle，
//     模型会把一段接得上的对话当成一份等着被取走的结果。
//   - **半装上去**。模型手上有一件能投递、却没有任何办法叫停的控制工具。
//   - **空列表排成 null**。模型看见 null 读不出「一个都没有」。

package controltool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [github.com/snight1983/ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包只读它的 ID 和 Status，别的方法全是哑的。
type stubAgent struct {
	id     session.SessionID
	status agent.Status
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return a.status }
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
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// followupCall 是服务收到的那一次后续投递。
type followupCall struct {
	parent  agent.Agent
	childID session.SessionID
	content llm.Content
	options subagent.FollowupOptions
}

// interruptCall 是服务收到的那一次打断请求。
type interruptCall struct {
	target    session.SessionID
	authority subagent.InterruptAuthority
}

// stubService 是那台记账的假子 agent 运行时。
type stubService struct {
	// messageID 是投递成功时交回的那条消息标识。
	messageID llm.MessageID
	// followupErr 不为 nil 时每次后续投递都失败。
	followupErr error
	// interruptErr 不为 nil 时每次打断都失败。
	interruptErr error
	// childrenErr / descendantsErr 不为 nil 时对应那次列举失败。
	childrenErr    error
	descendantsErr error

	// children / descendants 是两次列举各自交回的那几行。
	children    []subagent.ListEntry
	descendants []subagent.DescendantListEntry

	// followups / interrupts 按顺序记下每一次调用。
	followups  []followupCall
	interrupts []interruptCall
}

func (s *stubService) Followup(
	_ context.Context,
	parent agent.Agent,
	childID session.SessionID,
	content llm.Content,
	options subagent.FollowupOptions,
) (llm.MessageID, error) {
	s.followups = append(s.followups, followupCall{
		parent: parent, childID: childID, content: content, options: options,
	})
	if s.followupErr != nil {
		return "", s.followupErr
	}
	return s.messageID, nil
}

func (s *stubService) Interrupt(target session.SessionID, authority subagent.InterruptAuthority) error {
	s.interrupts = append(s.interrupts, interruptCall{target: target, authority: authority})
	return s.interruptErr
}

func (s *stubService) ListChildren(context.Context, session.SessionID) ([]subagent.ListEntry, error) {
	if s.childrenErr != nil {
		return nil, s.childrenErr
	}
	return s.children, nil
}

func (s *stubService) ListDescendants(
	context.Context,
	session.SessionID,
) ([]subagent.DescendantListEntry, error) {
	if s.descendantsErr != nil {
		return nil, s.descendantsErr
	}
	return s.descendants, nil
}

// stubAgents 是那份假的活 agent 登记。
type stubAgents map[session.SessionID]agent.Agent

func (a stubAgents) Get(id session.SessionID) (agent.Agent, bool) {
	live, ok := a[id]
	return live, ok
}

// ---- 装配 ----

// world 是一台装好的控制工具周边。
type world struct {
	t *testing.T

	service *stubService
	agents  stubAgents
	tools   *tools.Runtime
	// owner 是那两个控制器往上装的那个作用域。
	owner *scope.Scope
	// caller 是那个发起调用的父。
	caller *stubAgent
}

// scopeOf 造一个有身份的顶层作用域，用完自动释放。
func scopeOf(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func newWorld(t *testing.T) *world {
	t.Helper()
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	return &world{
		t:       t,
		service: &stubService{messageID: "msg-1"},
		agents:  stubAgents{},
		tools:   toolRuntime,
		owner:   scopeOf(t, "owner"),
		caller:  &stubAgent{id: "parent", status: agent.StatusIdle},
	}
}

// agentOf 是那条从钥匙查回 agent 的路：只认这台周边那个作用域。
func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key != w.owner.Key() {
		return nil, errors.New("这把钥匙不属于那个调用方")
	}
	return w.caller, nil
}

func (w *world) config() Config {
	return Config{Service: w.service, AgentOf: w.agentOf}
}

func (w *world) listConfig() ListConfig {
	return ListConfig{Service: w.service, Agents: w.agents, AgentOf: w.agentOf}
}

// controller 造一个控制器并把那两件工具装上去。
func (w *world) controller() *Controller {
	w.t.Helper()
	controller, err := New(w.config())
	if err != nil {
		w.t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

// listController 造一个列举控制器。
func (w *world) listController() *ListController {
	w.t.Helper()
	controller, err := NewListAgents(w.listConfig())
	if err != nil {
		w.t.Fatalf("造列举控制器失败：%v", err)
	}
	return controller
}

// call 走真运行时调一次工具。
func (w *world) call(name string, args any) tools.Result {
	w.t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	return w.tools.Execute(w.t.Context(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      name,
		Arguments: encoded,
		Agent:     w.owner.Key(),
	})
}

// execOn 造一份落在某把钥匙上的执行上下文。
func execOn(key *scope.Key) *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{Agent: key}}}
}

// textOf 把一份内容里那些文本块拼起来。
func textOf(content llm.Content) string {
	var parts []string
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
		"没有子 agent 运行时":    func(config *Config) { config.Service = nil },
		"没有从钥匙找回 agent 的路": func(config *Config) { config.AgentOf = nil },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.config()
			damage(&config)
			if _, err := New(config); err == nil {
				t.Fatal("缺件还造得出控制器")
			}
		})
	}
}

func TestNewListAgentsRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*ListConfig){
		"没有子 agent 运行时":    func(config *ListConfig) { config.Service = nil },
		"没有活 agent 登记":     func(config *ListConfig) { config.Agents = nil },
		"没有从钥匙找回 agent 的路": func(config *ListConfig) { config.AgentOf = nil },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.listConfig()
			damage(&config)
			if _, err := NewListAgents(config); err == nil {
				t.Fatal("缺件还造得出列举控制器")
			}
		})
	}
}

func TestInstallNeedsAToolRuntime(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	if _, err := w.controller().Install(t.Context(), w.owner, Deps{}); err == nil {
		t.Fatal("没有工具运行时还装得上那两件控制工具")
	}
	if _, err := w.listController().Install(t.Context(), w.owner, ListDeps{}); err == nil {
		t.Fatal("没有工具运行时还装得上 list_agents")
	}
}

// 三件工具一起装一起摘：留一件在那儿等于模型手上有一条别人都不知道的路。
func TestTheThreeToolsAppearAndDisappearWithTheInstallation(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	releaseControl, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools})
	if err != nil {
		t.Fatalf("装那两件控制工具失败：%v", err)
	}
	releaseList, err := w.listController().Install(t.Context(), w.owner, ListDeps{Tools: w.tools})
	if err != nil {
		t.Fatalf("装 list_agents 失败：%v", err)
	}

	for _, name := range []string{SendMessageTool, InterruptTool, ListAgentsTool} {
		if _, ok := w.tools.Get(name, w.owner.Key()); !ok {
			t.Fatalf("装完看不见 %s", name)
		}
	}

	if err := errors.Join(releaseList(t.Context()), releaseControl(t.Context())); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	for _, name := range []string{SendMessageTool, InterruptTool, ListAgentsTool} {
		if _, ok := w.tools.Get(name, w.owner.Key()); ok {
			t.Fatalf("释放之后 %s 还在", name)
		}
	}
}

// 后一件装不上要把前一件摘回去：半装上去意味着模型手上有一件能投递、却没有任何
// 办法叫停的控制工具。
func TestInstallRollsBackWhenALaterToolCannotBeRegistered(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	// 同一个作用域上先占掉 interrupt_agent 这个名字，第二件登记必然撞名。
	placeholder := &tools.Definition{
		Name:        InterruptTool,
		Description: "占位",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) { return nil, nil },
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return nil, nil
		},
	}
	if _, err := w.tools.Register(t.Context(), w.owner, placeholder); err != nil {
		t.Fatalf("先占位那次登记失败：%v", err)
	}

	if _, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools}); err == nil {
		t.Fatal("撞名了还装得上")
	}
	if _, ok := w.tools.Get(SendMessageTool, w.owner.Key()); ok {
		t.Fatalf("%s 装上去了却没被滚回去", SendMessageTool)
	}
}

// ---- send_message ----

func TestSendMessageDeliversToTheNamedChildOnBehalfOfTheCaller(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	if _, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools}); err != nil {
		t.Fatalf("装那两件控制工具失败：%v", err)
	}

	result := w.call(SendMessageTool, sendMessageArgs{SubagentID: "child-7", Message: "再查一遍"})
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	want := "message queued as the next turn for subagent child-7"
	if got := textOf(result.Content); got != want {
		t.Fatalf("给模型的话该是 %q，实际 %q", want, got)
	}

	if len(w.service.followups) != 1 {
		t.Fatalf("该恰好投一次，实际 %d 次", len(w.service.followups))
	}
	delivered := w.service.followups[0]
	// 父那份权凭的是那个确切的活调用方，不是参数。
	if delivered.parent != w.caller {
		t.Fatalf("代表的不是那个调用方，实际 %#v", delivered.parent)
	}
	if delivered.childID != "child-7" {
		t.Fatalf("收件人该是 child-7，实际 %q", delivered.childID)
	}
	if got := textOf(delivered.content); got != "再查一遍" {
		t.Fatalf("投出去的正文不对：%q", got)
	}
	// 归属必须是协调方那一份，且记着发信人是谁。
	source, ok := delivered.options.Source.(llm.PluginSource)
	if !ok {
		t.Fatalf("归属该是一份插件来源，实际 %#v", delivered.options.Source)
	}
	if source.Plugin != subagent.CoordinatorPlugin {
		t.Fatalf("归属该是 %q，实际 %q", subagent.CoordinatorPlugin, source.Plugin)
	}
	if !strings.Contains(string(source.Extra), string(w.caller.id)) {
		t.Fatalf("归属里没记下发信人：%s", source.Extra)
	}
}

func TestSendMessageSurfacesADeliveryFailureAsAToolError(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.followupErr = errors.New("那个孩子接不上")
	if _, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools}); err != nil {
		t.Fatalf("装那两件控制工具失败：%v", err)
	}

	if result := w.call(SendMessageTool, sendMessageArgs{SubagentID: "child-7", Message: "在的"}); !result.IsError {
		t.Fatal("投递失败了这次调用还算成功")
	}
}

// 归属造不出来就不许投：一条没有发信人的协调消息在孩子那边追不回是谁说的。
func TestSendMessageRefusesWhenTheAttributionCannotBeBuilt(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.caller.id = ""

	_, err := w.controller().sendMessage(
		t.Context(),
		json.RawMessage(`{"subagent_id":"child-7","message":"在的"}`),
		execOn(w.owner.Key()),
	)
	if err == nil {
		t.Fatal("造不出归属还投得出去")
	}
	if len(w.service.followups) != 0 {
		t.Fatalf("造不出归属不该投出去，实际投了 %d 次", len(w.service.followups))
	}
}

// ---- interrupt_agent ----

func TestInterruptAsksTheServiceWithTheExactLiveCaller(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	if _, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools}); err != nil {
		t.Fatalf("装那两件控制工具失败：%v", err)
	}

	result := w.call(InterruptTool, interruptArgs{AgentID: "deep-3"})
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	want := "interrupt requested for agent deep-3"
	if got := textOf(result.Content); got != want {
		t.Fatalf("给模型的话该是 %q，实际 %q", want, got)
	}

	if len(w.service.interrupts) != 1 {
		t.Fatalf("该恰好请求一次，实际 %d 次", len(w.service.interrupts))
	}
	requested := w.service.interrupts[0]
	if requested.target != "deep-3" {
		t.Fatalf("目标该是 deep-3，实际 %q", requested.target)
	}
	// 祖先那份权凭的是那个确切的活调用方，这件工具自己不添任何权。
	if requested.authority.Kind != subagent.AuthorityAncestor {
		t.Fatalf("凭的该是祖先权，实际 %q", requested.authority.Kind)
	}
	if requested.authority.Agent != w.caller {
		t.Fatalf("凭的不是那个调用方，实际 %#v", requested.authority.Agent)
	}
}

func TestInterruptSurfacesARefusalAsAToolError(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.interruptErr = errors.New("这个 agent 不在你的血统里")
	if _, err := w.controller().Install(t.Context(), w.owner, Deps{Tools: w.tools}); err != nil {
		t.Fatalf("装那两件控制工具失败：%v", err)
	}

	if result := w.call(InterruptTool, interruptArgs{AgentID: "别人的"}); !result.IsError {
		t.Fatal("服务拒了这次调用还算成功")
	}
}

// ---- 调用方 ----

// 这三件工具只装在有 agent 的地方，所以「查不回去」只发生在装配出错的时候——那时候
// 给模型一句话，好过让整个回合炸掉。
func TestEveryToolRefusesACallThatIsNotBoundToAnAgent(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller()
	listController := w.listController()

	bodies := map[string]struct {
		run  func(*tools.RunContext) error
		want string
	}{
		SendMessageTool: {
			run: func(exec *tools.RunContext) error {
				_, err := controller.sendMessage(t.Context(), json.RawMessage(`{"subagent_id":"a","message":"b"}`), exec)
				return err
			},
			want: missingSendMessageAgent,
		},
		InterruptTool: {
			run: func(exec *tools.RunContext) error {
				_, err := controller.interrupt(t.Context(), json.RawMessage(`{"agent_id":"a"}`), exec)
				return err
			},
			want: missingInterruptAgent,
		},
		ListAgentsTool: {
			run: func(exec *tools.RunContext) error {
				_, err := listController.list(t.Context(), json.RawMessage(`{}`), exec)
				return err
			},
			want: missingListAgent,
		},
	}
	execs := map[string]*tools.RunContext{
		"压根没有执行上下文":     nil,
		"执行上下文里没有钥匙":    {},
		"钥匙查不回任何 agent": execOn(scopeOf(t, "outsider").Key()),
	}
	for toolName, body := range bodies {
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			for name, exec := range execs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					if err := body.run(exec); err == nil || err.Error() != body.want {
						t.Fatalf("该报 %q，实际 %v", body.want, err)
					}
				})
			}
		})
	}
}

// 走运行时那条路的参数一定是合法 JSON（运行时在进执行体之前就验过），所以这条边
// 只有直调才碰得到。它仍旧要有：一个不报错的解析失败会把空 id 当成收件人投出去。
func TestEveryBodyRefusesUnreadableArguments(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller()
	listController := w.listController()
	broken := json.RawMessage(`{`)

	if _, err := controller.sendMessage(t.Context(), broken, execOn(w.owner.Key())); err == nil {
		t.Fatal("send_message 参数读不出来还算成功")
	}
	if _, err := controller.interrupt(t.Context(), broken, execOn(w.owner.Key())); err == nil {
		t.Fatal("interrupt_agent 参数读不出来还算成功")
	}
	if _, err := listController.list(t.Context(), broken, execOn(w.owner.Key())); err == nil {
		t.Fatal("list_agents 参数读不出来还算成功")
	}
	if len(w.service.followups) != 0 || len(w.service.interrupts) != 0 {
		t.Fatal("参数读不出来不该惊动服务")
	}
}

// 渲染那两句话靠的是参数；读不出来要报错，不能渲染成一句没有收件人的话。
func TestEveryRenderRefusesUnreadableArguments(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller()
	broken := json.RawMessage(`{`)

	renders := map[string]func(json.RawMessage, json.RawMessage) (llm.Content, error){
		SendMessageTool: controller.newSendMessage().Output.Render,
		InterruptTool:   controller.newInterrupt().Output.Render,
		ListAgentsTool:  renderListing,
	}
	for name, render := range renders {
		if _, err := render(broken, json.RawMessage(`[]`)); err == nil {
			t.Fatalf("%s 的参数读不出来还渲染得出来", name)
		}
	}
	// list_agents 还多一处：那份结果本身读不出来同样要报错。
	if _, err := renderListing(json.RawMessage(`{}`), broken); err == nil {
		t.Fatal("list_agents 的结果读不出来还渲染得出来")
	}
}

// ---- list_agents ----

// childRow 造一行可续孩子。
func childRow(id session.SessionID, label string) subagent.ListEntry {
	return subagent.ListEntry{
		Kind:  subagent.EntryChild,
		ID:    id,
		Mode:  subagent.ModeContinuable,
		Label: label,
	}
}

func TestListChildrenProjectsContinuableChildrenAndDiagnostics(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.agents["busy"] = &stubAgent{id: "busy", status: agent.StatusRunning}
	w.agents["loaded"] = &stubAgent{id: "loaded", status: agent.StatusIdle}
	w.service.children = []subagent.ListEntry{
		childRow("busy", "查资料"),
		childRow("loaded", "写草稿"),
		childRow("cold", "等着的"),
		// 一次性孩子接不上 send_message，模型永远不该看见它。
		{Kind: subagent.EntryChild, ID: "once", Mode: subagent.ModeOneShot, Label: "跑完就没了"},
		{Kind: subagent.EntryDiagnostic, ID: "broken", Reason: subagent.DiagnosticCorrupt},
	}
	if _, err := w.listController().Install(t.Context(), w.owner, ListDeps{Tools: w.tools}); err != nil {
		t.Fatalf("装 list_agents 失败：%v", err)
	}

	result := w.call(ListAgentsTool, listAgentsArgs{})
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	want := strings.Join([]string{
		"busy [running] — 查资料",
		"loaded [idle] — 写草稿",
		"cold [ready] — 等着的",
		"broken [diagnostic: corrupt]",
	}, "\n")
	if got := textOf(result.Content); got != want {
		t.Fatalf("列出来的该是：\n%s\n实际：\n%s", want, got)
	}
	// children 那个范围一行都不该带位置。
	if strings.Contains(string(result.Value), "parent") || strings.Contains(string(result.Value), "depth") {
		t.Fatalf("children 范围带上了位置：%s", result.Value)
	}
}

func TestListDescendantsAnnotatesEveryRowWithItsPosition(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.agents["kid"] = &stubAgent{id: "kid", status: agent.StatusRunning}
	w.service.descendants = []subagent.DescendantListEntry{
		{ListEntry: childRow("kid", "直接的"), ParentID: "parent", Depth: 1},
		{ListEntry: childRow("grandkid", "深一层"), ParentID: "kid", Depth: 2},
		{
			ListEntry: subagent.ListEntry{
				Kind:   subagent.EntryDiagnostic,
				ID:     "unreadable",
				Reason: subagent.DiagnosticUnavailable,
			},
			ParentID: "kid",
			Depth:    2,
		},
		{
			ListEntry: subagent.ListEntry{
				Kind: subagent.EntryChild, ID: "once", Mode: subagent.ModeOneShot, Label: "跑完就没了",
			},
			ParentID: "parent",
			Depth:    1,
		},
	}
	if _, err := w.listController().Install(t.Context(), w.owner, ListDeps{Tools: w.tools}); err != nil {
		t.Fatalf("装 list_agents 失败：%v", err)
	}

	result := w.call(ListAgentsTool, listAgentsArgs{Scope: ScopeDescendants})
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	want := strings.Join([]string{
		"kid [running] parent=parent depth=1 — 直接的",
		"grandkid [ready] parent=kid depth=2 — 深一层",
		"unreadable [diagnostic: unavailable] parent=kid depth=2",
	}, "\n")
	if got := textOf(result.Content); got != want {
		t.Fatalf("列出来的该是：\n%s\n实际：\n%s", want, got)
	}
}

// 一个都没有时交回的必须是一个空数组而不是 null：模型看见 null 读不出「一个都没有」。
func TestListAgentsRendersAnEmptyListingAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	if _, err := w.listController().Install(t.Context(), w.owner, ListDeps{Tools: w.tools}); err != nil {
		t.Fatalf("装 list_agents 失败：%v", err)
	}

	result := w.call(ListAgentsTool, listAgentsArgs{})
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	if got := string(result.Value); got != "[]" {
		t.Fatalf("空列举该排成 []，实际 %s", got)
	}
	if got := textOf(result.Content); got != emptyListing {
		t.Fatalf("给模型的话该是 %q，实际 %q", emptyListing, got)
	}
}

func TestListAgentsSurfacesAListingFailure(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		scope   string
		arrange func(*stubService)
	}{
		"children 那条路":    {scope: ScopeChildren, arrange: func(s *stubService) { s.childrenErr = errors.New("读不动") }},
		"descendants 那条路": {scope: ScopeDescendants, arrange: func(s *stubService) { s.descendantsErr = errors.New("读不动") }},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			each.arrange(w.service)
			if _, err := w.listController().Install(t.Context(), w.owner, ListDeps{Tools: w.tools}); err != nil {
				t.Fatalf("装 list_agents 失败：%v", err)
			}
			if result := w.call(ListAgentsTool, listAgentsArgs{Scope: each.scope}); !result.IsError {
				t.Fatal("列举失败了这次调用还算成功")
			}
		})
	}
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
