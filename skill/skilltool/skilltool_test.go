// 本文件的作用：把这三条通路上那几件事钉住——目录什么时候发、发的是哪一份、
// `/名字` 那个手势认什么不认什么，以及 skill 工具在哪几种情形下失败。
//
// 逐条对着 DSH 的 tests/tool-skill.spec.ts 走。
//
// # 这些测试防的是什么错
//
//   - **目录发多了**。同一份清单每个步骤重发一遍，会把模型的上文塞满，还让它
//     以为技能表一直在变。
//   - **目录发少了**。技能变了却不重发，模型手上那份清单是过期的，它会去调
//     一件已经没有的技能。
//   - **一份残缺的发现被当成「技能变少了」发出去**。那等于告诉模型那些技能没了。
//   - **模型拿到一份它调不动的清单**。工具被遮蔽或者限制掉之后目录还在发。
//   - **`/名字` 认了不是用户说的话**。别的来源伪造得出这个手势就等于任意注入。
//   - **一份不许被模型调的技能被整篇交出去**。两次查找之间的一次变更是有窗口的。
package skilltool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/skill"
	"ds-harness-go/skill/skilltool"
)

// callerCwd 是这些用例里调用方那个工作目录。
//
// 会话头要求工作目录是绝对路径，而「绝对」在 Windows 上要带盘符，所以这个值
// 现算，不写字面量。它不需要真的存在：本包只把它原样递给注册表。
var callerCwd = absDir("skilltool-workspace")

// absDir 把一个名字变成当前平台上的绝对路径。
func absDir(name string) string {
	path, err := filepath.Abs(name)
	if err != nil {
		panic(err)
	}
	return path
}

// stubAgent 是一个只把会话和作用域摆在那儿的假 agent。
//
// 本包除了「这个 agent 的作用域是哪个、它的会话日志和表面长什么样、工作目录
// 在哪」之外不碰 agent 的任何东西，别的方法在这里全是哑的。
type stubAgent struct {
	id   session.SessionID
	own  *scope.Scope
	sess *coresession.Session
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}

// Inject 在这里必须炸。目录和注入都走步骤边界那条瀑布，绝不该改道去 Inject——
// 那条路绕开了「这一步提议了哪些消息」这个语义，会在别的步骤上冒出来。
func (a *stubAgent) Inject(llm.Message) { panic("步骤边界上的目录不许走 agent.Inject()") }
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubCatalog 是一台按用例摆好的假技能注册表。
type stubCatalog struct {
	t *testing.T

	// skills 是当前这一份清单，按目录顺序。
	skills []skill.Definition
	// incomplete 为真表示这次发现没跑完。
	incomplete bool
	// missing 里的名字在 Get 那一路上交回 nil，模拟两次查找之间技能没了。
	missing map[string]bool
	// downgraded 里的名字在 Get 交回来的那份定义上把模型调用关掉，
	// 模拟两次查找之间策略被改了。
	downgraded map[string]bool
	// gets 按调用顺序记下 Get 被问过哪些名字。
	gets []string
	// seenCwd 记下最后一次读注册表时那个工作目录。
	seenCwd string
}

// List 交出当前这份清单。
func (c *stubCatalog) List(_ context.Context, options skill.ViewOptions) ([]skill.Summary, error) {
	c.seenCwd = options.Cwd
	summaries := make([]skill.Summary, 0, len(c.skills))
	for _, definition := range c.skills {
		summaries = append(summaries, definition.Summary)
	}
	return summaries, nil
}

// Snapshot 交出当前这份清单，外加「这次发现跑完了没有」。
func (c *stubCatalog) Snapshot(ctx context.Context, options skill.ViewOptions) (skill.CatalogSnapshot, error) {
	summaries, err := c.List(ctx, options)
	if err != nil {
		return skill.CatalogSnapshot{}, err
	}
	return skill.CatalogSnapshot{Skills: summaries, Complete: !c.incomplete}, nil
}

// Get 把一份技能读成完整正文。
func (c *stubCatalog) Get(_ context.Context, name string, options skill.ViewOptions) (*skill.Definition, error) {
	c.seenCwd = options.Cwd
	c.gets = append(c.gets, name)
	if c.missing[name] {
		return nil, nil
	}
	for _, definition := range c.skills {
		if definition.Name != name {
			continue
		}
		loaded := definition
		if c.downgraded[name] {
			loaded.Invocation.ModelInvocable = false
		}
		return &loaded, nil
	}
	return nil, nil
}

// bothWays 是「模型和人都能调」那份策略。
func bothWays() skill.InvocationPolicy {
	return skill.InvocationPolicy{ModelInvocable: true, UserInvocable: true}
}

// definitionOf 造一份技能。
func definitionOf(name, description, content string, policy skill.InvocationPolicy) skill.Definition {
	return skill.Definition{
		Summary: skill.Summary{
			Name:        name,
			Description: description,
			Invocation:  policy,
			Source:      "runtime",
			Provider:    "runtime",
		},
		Content: content,
	}
}

// world 是一个装好了控制器、工具表和 agent 注册表的世界。
type world struct {
	t          *testing.T
	root       *scope.Scope
	agentScope *scope.Scope
	catalog    *stubCatalog
	controller *skilltool.Controller
	agent      *stubAgent
	tools      *tools.Runtime
	agents     *agent.Registry
}

// newWorld 造一个调用方在 [callerCwd] 里的世界。
func newWorld(t *testing.T, catalog *stubCatalog, config skilltool.Config) *world {
	t.Helper()
	catalog.t = t
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

	const id session.SessionID = "caller"
	sess, err := coresession.NewSession(id, coresession.Options{
		Header: &session.SessionHeader{ID: id, Cwd: callerCwd},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	stub := &stubAgent{id: id, own: agentScope, sess: sess}

	config.Skills = catalog
	config.AgentOf = func(key *scope.Key) (agent.Agent, error) {
		if key == agentScope.Key() {
			return stub, nil
		}
		return nil, nil
	}
	controller, err := skilltool.New(config)
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}

	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具注册表失败：%v", err)
	}
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	detach, err := agents.Register(ctx, stub, nil)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	return &world{
		t: t, root: root, agentScope: agentScope, catalog: catalog,
		controller: controller, agent: stub, tools: runtime, agents: agents,
	}
}

// install 把工具和两条胳膊装上去，返回摘除函数。
func (w *world) install() func() {
	w.t.Helper()
	undo, err := w.controller.Install(w.t.Context(), w.root, skilltool.Deps{
		Tools: w.tools, Agents: w.agents,
	})
	if err != nil {
		w.t.Fatalf("装控制器失败：%v", err)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		_ = undo(context.Background())
	}
	w.t.Cleanup(remove)
	return remove
}

// propose 跑一次步骤准入瀑布，里层原样交出调用方给的那个提议。
func (w *world) propose(messages []llm.Message) agent.PreStepDecision {
	w.t.Helper()
	decision, err := w.agents.ResolvePreStep(
		w.t.Context(),
		agent.PreStep{Agent: w.agent, Messages: messages, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.EnterStep(messages), nil
		},
	)
	if err != nil {
		w.t.Fatalf("跑步骤准入失败：%v", err)
	}
	return decision
}

// fireStep 跑一次步骤准入，并把进来的那些消息落进会话日志——真实的 agent 循环
// 就是这么做的，目录那条胳膊下一次读发布史读的正是这些事件。
func (w *world) fireStep() agent.PreStepDecision {
	w.t.Helper()
	decision := w.propose(nil)
	for _, message := range decision.Messages {
		w.appendUserMessage(message, session.AppendOp{})
	}
	return decision
}

// appendUserMessage 往日志里落一条上表面的用户消息。
func (w *world) appendUserMessage(message llm.Message, op session.SurfaceOp) session.Event {
	w.t.Helper()
	data, err := json.Marshal(session.UserMessageData{Message: message})
	if err != nil {
		w.t.Fatalf("排用户消息失败：%v", err)
	}
	appended, err := w.agent.sess.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      data,
		SurfaceOp: op,
	})
	if err != nil {
		w.t.Fatalf("追加用户消息失败：%v", err)
	}
	return appended
}

// openTurn 开一个回合，并落一条用户消息——目录那条胳膊不要求这个形状，但真实
// 日志里步骤永远开在回合之内，用例照着摆。
func (w *world) openTurn() {
	w.t.Helper()
	data, err := json.Marshal(map[string]int{"turn": 1})
	if err != nil {
		w.t.Fatalf("排回合负载失败：%v", err)
	}
	if _, err := w.agent.sess.Append(session.Event{Type: session.EventTurnStart, Data: data}); err != nil {
		w.t.Fatalf("追加 turn/start 失败：%v", err)
	}
	w.appendUserMessage(userSay("turn 1"), session.AppendOp{})
}

// catalogEvents 挑出日志里那些**读得懂的**本包目录消息。
func (w *world) catalogEvents() []llm.Message {
	w.t.Helper()
	var found []llm.Message
	for _, event := range w.agent.sess.Events() {
		if event.Type != session.EventUserMessage {
			continue
		}
		var data session.UserMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		if !readableCatalog(data.Message.Source) {
			continue
		}
		found = append(found, data.Message)
	}
	return found
}

// readableCatalog 判一条来源是不是一份读得懂的本包目录。
//
// 用例这一侧独立判一遍，不借本包那个未导出的读法：两边都错成同一个样子的话，
// 这些断言就什么都测不到了。
func readableCatalog(source llm.MessageSource) bool {
	plugin, ok := source.(llm.PluginSource)
	if !ok || plugin.Plugin != skilltool.CatalogPlugin {
		return false
	}
	var wire struct {
		Entries []struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(plugin.Extra, &wire); err != nil || wire.Entries == nil {
		return false
	}
	for _, entry := range wire.Entries {
		if entry.Name == nil || entry.Description == nil {
			return false
		}
	}
	return true
}

// userSay 造一条用户自己说的话。
func userSay(text string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
}

// pluginSay 造一条带任意产出方名字的注入消息。
func pluginSay(plugin, text string) llm.Message {
	return llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{Plugin: plugin, Context: llm.InstructionsContext{}},
	)
}

// seededCatalog 造一条冒充本包目录的消息，extra 原样当那份出处的字节。
func seededCatalog(t *testing.T, text string, extra string) llm.Message {
	t.Helper()
	return llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{
			Plugin:  skilltool.CatalogPlugin,
			Context: llm.CatalogContext{},
			Extra:   json.RawMessage(extra),
		},
	)
}

// textOf 把一条消息里那些文本块拼起来。
func textOf(message llm.Message) string {
	var parts []string
	for _, block := range message.Content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// invocationNames 挑出一份提议里那些「用户明确调起」注入点的是哪些技能。
func invocationNames(t *testing.T, decision agent.PreStepDecision) []string {
	t.Helper()
	var names []string
	for _, message := range decision.Messages {
		plugin, ok := message.Source.(llm.PluginSource)
		if !ok || plugin.Plugin != skilltool.InvocationPlugin {
			continue
		}
		var source skill.InvocationSource
		if err := json.Unmarshal(plugin.Extra, &source); err != nil {
			t.Fatalf("读注入出处失败：%v", err)
		}
		names = append(names, source.Name)
	}
	return names
}

// sourceKinds 按顺序列出一份提议里每条消息的产出方。
func sourceKinds(decision agent.PreStepDecision) []string {
	kinds := make([]string, 0, len(decision.Messages))
	for _, message := range decision.Messages {
		if plugin, ok := message.Source.(llm.PluginSource); ok {
			kinds = append(kinds, plugin.Plugin)
			continue
		}
		kinds = append(kinds, string(message.Source.SourceKind()))
	}
	return kinds
}

// execute 调一次 skill 工具，交回运行时那份结果。
func (w *world) execute(name string) tools.Result {
	w.t.Helper()
	args, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	return w.tools.Execute(w.t.Context(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      skilltool.ToolName,
		Arguments: args,
		Agent:     w.agentScope.Key(),
	})
}

// TestTheToolAppearsAndDisappearsWithTheInstallation 钉住这件工具的装摘。
func TestTheToolAppearsAndDisappearsWithTheInstallation(t *testing.T) {
	w := newWorld(t, &stubCatalog{}, skilltool.Config{})
	remove := w.install()

	definition, ok := w.tools.Get(skilltool.ToolName, w.agentScope.Key())
	if !ok {
		t.Fatalf("工具表里没有 %s", skilltool.ToolName)
	}
	view := definition.PresentCall(json.RawMessage(`{"name":"project-skill"}`))
	generic, ok := view.(tools.GenericCallView)
	if !ok {
		t.Fatalf("卡片应该是通用卡片，拿到 %T", view)
	}
	if generic.Title != "Load skill project-skill" || generic.Kind != tools.CallRead {
		t.Fatalf("卡片不对：%+v", generic)
	}
	if string(generic.RawInput) != `"project-skill"` {
		t.Fatalf("卡片入参不对：%s", generic.RawInput)
	}

	remove()
	if _, ok := w.tools.Get(skilltool.ToolName, w.agentScope.Key()); ok {
		t.Fatalf("摘掉之后工具表里还有 %s", skilltool.ToolName)
	}
}

// TestTheFirstCatalogCarriesOnlyNamesAndDescriptions 钉住第一份目录的内容。
//
// 说明要折空白、要截、要转义；whenToUse、source、资源路径、正文一个字都不许
// 出现——目录是一份路由用的摘要，多带一个字段就等于把技能的内情提前泄给模型。
func TestTheFirstCatalogCarriesOnlyNamesAndDescriptions(t *testing.T) {
	long := strings.Repeat("Long   description ", 5)
	catalog := &stubCatalog{skills: []skill.Definition{
		{
			Summary: skill.Summary{
				Name:         "z-skill",
				Description:  long,
				WhenToUse:    "Never render this routing hint.",
				Invocation:   bothWays(),
				Source:       "secret-source",
				Provider:     "runtime",
				ResourceBase: skill.DirectoryBase{Path: "/secret/path"},
			},
			Content: "Secret body.",
		},
		definitionOf("a-skill", "Use {{placeholder}} <safely> & carefully.", "A body.", bothWays()),
		definitionOf("model-only-skill", "Model-only skill.", "Model-only body.",
			skill.InvocationPolicy{ModelInvocable: true}),
		definitionOf("user-only-skill", "User-only skill.", "User-only body.",
			skill.InvocationPolicy{UserInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{CatalogDescriptionMaxLength: 50})
	w.install()

	decision := w.propose(nil)
	if len(decision.Messages) != 1 {
		t.Fatalf("这一步应该只多出目录那一条，拿到 %d 条", len(decision.Messages))
	}
	text := textOf(decision.Messages[0])
	want := strings.Join([]string{
		"<available_skills>",
		"- `z-skill`: Long description Long description Long descript...",
		"- `a-skill`: Use {{placeholder}} &lt;safely&gt; &amp; carefully.",
		"- `model-only-skill`: Model-only skill.",
		"</available_skills>",
	}, "\n")
	if !strings.Contains(text, want) {
		t.Fatalf("目录正文不对：\n%s", text)
	}
	for _, leaked := range []string{
		"whenToUse", "Never render this routing hint.", "secret-source",
		"/secret/path", "Secret body", "user-only-skill",
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("目录里漏了 %q：\n%s", leaked, text)
		}
	}
	if w.catalog.seenCwd != callerCwd {
		t.Fatalf("读注册表用的工作目录不对：%q", w.catalog.seenCwd)
	}
}

// TestTheCatalogSitsBeforeEverythingTheModelMustActOn 钉住那两条胳膊的先后。
//
// 别的插件那条注入在前，目录在它之后，「用户明确调起」那份技能正文在最后——
// 模型必须照着做的材料离它自己的回答最近。
func TestTheCatalogSitsBeforeEverythingTheModelMustActOn(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		// 目录里得有一件模型能调的，否则它是空的、压根不会发布，这一步就只剩三条。
		definitionOf("listed-demo", "Listed demo", "Listed body.",
			skill.InvocationPolicy{ModelInvocable: true}),
		definitionOf("hidden-demo", "User-only demo", "Say the magic word: PINEAPPLE.",
			skill.InvocationPolicy{UserInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	// 一条登记得更晚的胳膊落在瀑布的**里层**，它加的东西排在本包两条之前。
	remove, err := w.agents.OnPreStep(t.Context(), w.root, func(
		ctx context.Context,
		_ agent.PreStep,
		next func(context.Context) (agent.PreStepDecision, error),
	) (agent.PreStepDecision, error) {
		decision, err := next(ctx)
		if err != nil || !decision.Enter {
			return decision, err
		}
		return agent.EnterStep(append(decision.Messages, pluginSay("later-contribution", "later"))), nil
	})
	if err != nil {
		t.Fatalf("挂陪跑胳膊失败：%v", err)
	}
	t.Cleanup(func() { _ = remove(context.Background()) })

	decision := w.propose([]llm.Message{userSay("/hidden-demo what does this do")})

	kinds := sourceKinds(decision)
	if len(kinds) != 4 {
		t.Fatalf("这一步应该有四条消息，拿到 %v", kinds)
	}
	if kinds[0] != string(llm.SourceUser) || kinds[1] != "later-contribution" {
		t.Fatalf("认领的那批和陪跑那条没排在最前：%v", kinds)
	}
	if kinds[2] != skilltool.CatalogPlugin || kinds[3] != skilltool.InvocationPlugin {
		t.Fatalf("目录应该排在注入之前：%v", kinds)
	}
	body := textOf(decision.Messages[3])
	if !strings.Contains(body, `<skill_content name="hidden-demo">`) ||
		!strings.Contains(body, "Say the magic word: PINEAPPLE.") {
		t.Fatalf("注入的正文不对：\n%s", body)
	}
	if strings.Contains(body, "what does this do") {
		t.Fatalf("用户自己那句话不该进注入：\n%s", body)
	}
}

// TestAnEmptyCatalogIsNeverPublishedBeforeTheFirstOne 钉住那条空基线。
//
// 一次会话如果整个过程中都没有模型能调的技能，模型不该看见一份空清单。
func TestAnEmptyCatalogIsNeverPublishedBeforeTheFirstOne(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("user-only-skill", "User-only skill", "User-only body.",
			skill.InvocationPolicy{UserInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()

	w.fireStep()
	w.fireStep()

	if events := w.catalogEvents(); len(events) != 0 {
		t.Fatalf("不该发出任何目录，拿到 %d 条", len(events))
	}
}

// TestAnIncompleteDiscoveryKeepsTheLastGoodCatalog 钉住那次残缺的发现。
//
// 把一份跑了一半的目录当成「技能变少了」发出去，会让模型以为那些技能没了。
func TestAnIncompleteDiscoveryKeepsTheLastGoodCatalog(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("stable-skill", "Stable skill", "Stable body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()
	w.fireStep()
	if events := w.catalogEvents(); len(events) != 1 {
		t.Fatalf("第一份目录没发出去，拿到 %d 条", len(events))
	}

	// 这一份少了那件技能，但这次发现没跑完。
	catalog.skills = nil
	catalog.incomplete = true
	w.fireStep()

	if events := w.catalogEvents(); len(events) != 1 {
		t.Fatalf("残缺的发现不该改动目录，拿到 %d 条", len(events))
	}
}

// TestTheCatalogRepublishesOnlyWhenItsEntriesChange 钉住替换与墓碑。
func TestTheCatalogRepublishesOnlyWhenItsEntriesChange(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("first-skill", "First skill", "First body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()

	w.fireStep()
	if events := w.catalogEvents(); len(events) != 1 {
		t.Fatalf("第一份目录没发出去，拿到 %d 条", len(events))
	}

	// 条目没变：不重发。
	w.fireStep()
	if events := w.catalogEvents(); len(events) != 1 {
		t.Fatalf("条目没变时不该重发，拿到 %d 条", len(events))
	}

	catalog.skills = append(catalog.skills,
		definitionOf("second-skill", "Second skill", "Second body.", bothWays()))
	w.fireStep()
	events := w.catalogEvents()
	if len(events) != 2 {
		t.Fatalf("加了一件技能之后该发一份替换，拿到 %d 条", len(events))
	}
	addition := textOf(events[1])
	if !strings.Contains(addition, "first-skill") || !strings.Contains(addition, "second-skill") {
		t.Fatalf("替换目录必须是完整的一份：\n%s", addition)
	}
	if !strings.Contains(addition, "replaces every earlier available-skills list") {
		t.Fatalf("替换目录必须说出早先那些清单作废：\n%s", addition)
	}

	catalog.skills = nil
	w.fireStep()
	events = w.catalogEvents()
	if len(events) != 3 {
		t.Fatalf("技能全没了之后该发一份墓碑，拿到 %d 条", len(events))
	}
	removal := textOf(events[2])
	if !strings.Contains(removal, "No skills are currently available") {
		t.Fatalf("墓碑措辞不对：\n%s", removal)
	}
	if strings.Contains(removal, "first-skill") || strings.Contains(removal, "second-skill") {
		t.Fatalf("墓碑里不该再出现早先的名字：\n%s", removal)
	}

	// 空了之后再跑一步：还是空的，不再发。
	w.fireStep()
	if events := w.catalogEvents(); len(events) != 3 {
		t.Fatalf("空目录不该反复发，拿到 %d 条", len(events))
	}
}

// TestACatalogAlreadyProposedForThisStepIsDedupedOrReplaced 钉住同一步里的那条。
//
// 这条胳膊每个步骤都跑，同一个提议被重新过一遍时不该把那条消息换成一条新 id 的。
func TestACatalogAlreadyProposedForThisStepIsDedupedOrReplaced(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("first-skill", "First skill", "First body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()
	w.fireStep()
	published := w.catalogEvents()
	if len(published) != 1 {
		t.Fatalf("第一份目录没发出去，拿到 %d 条", len(published))
	}
	initial := published[0]

	// 表面上那份已经是这一份了：这一步里再挂一条是多余的，撤掉。
	duplicate := w.propose([]llm.Message{initial})
	if !duplicate.Enter || len(duplicate.Messages) != 0 {
		t.Fatalf("重复的那条该被撤掉，拿到 %+v", sourceKinds(duplicate))
	}

	catalog.skills = append(catalog.skills,
		definitionOf("second-skill", "Second skill", "Second body.", bothWays()))
	companion := userSay("keep this message")
	replaced := w.propose([]llm.Message{companion, initial})
	if !replaced.Enter || len(replaced.Messages) != 2 {
		t.Fatalf("该原地换掉那条目录，拿到 %v", sourceKinds(replaced))
	}
	if replaced.Messages[0].ID != companion.ID {
		t.Fatalf("陪跑那条不该被动过")
	}
	if replaced.Messages[1].ID == initial.ID {
		t.Fatalf("换上去的目录该是一条新消息")
	}
	if !strings.Contains(textOf(replaced.Messages[1]), "second-skill") {
		t.Fatalf("换上去的不是新那一份：\n%s", textOf(replaced.Messages[1]))
	}
}

// TestAProposedCatalogMatchingTheSnapshotIsLeftAlone 钉住「正好一样就别动」。
func TestAProposedCatalogMatchingTheSnapshotIsLeftAlone(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("first-skill", "First skill", "First body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	proposed := seededCatalog(t, "<available_skills>\n- `first-skill`: First skill\n</available_skills>",
		`{"entries":[{"name":"first-skill","description":"First skill"}]}`)

	decision := w.propose([]llm.Message{proposed})

	if !decision.Enter || len(decision.Messages) != 1 || decision.Messages[0].ID != proposed.ID {
		t.Fatalf("已经对上的那条该原样留着，拿到 %v", sourceKinds(decision))
	}
}

// TestAStaleProposedCatalogIsRemovedBeforeTheEmptyBaseline 钉住那条过期提议。
//
// 一条读不懂的、盖着本包名字的消息**不是**本包的目录，不许动它。
func TestAStaleProposedCatalogIsRemovedBeforeTheEmptyBaseline(t *testing.T) {
	w := newWorld(t, &stubCatalog{}, skilltool.Config{})
	w.install()
	malformed := seededCatalog(t, "preserve unreadable claimed context", `{}`)
	stale := seededCatalog(t, "<available_skills>\n- `stale-skill`: Stale skill\n</available_skills>",
		`{"entries":[{"name":"stale-skill","description":"Stale skill"}]}`)

	decision := w.propose([]llm.Message{malformed, stale})

	if !decision.Enter || len(decision.Messages) != 1 || decision.Messages[0].ID != malformed.ID {
		t.Fatalf("只该撤掉那条过期目录，拿到 %v", sourceKinds(decision))
	}
}

// TestAMalformedDurableCatalogNeverFailsTheStep 钉住那些读不回来的日志记录。
//
// 日志可能是恢复的、分叉的、或者外部写进来的。在步骤监听器里抛错会让这个会话
// 之后每一个回合都失败，所以读不懂的记录一律当成「不是本包的目录」。
func TestAMalformedDurableCatalogNeverFailsTheStep(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("live-skill", "Live skill", "Live body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()
	for _, extra := range []string{
		`{}`,
		`{"entries":null}`,
		`{"entries":"not-an-array"}`,
		`{"entries":[null]}`,
		`{"entries":[{"name":"x"}]}`,
		`{"entries":[{"description":"no name"}]}`,
	} {
		w.appendUserMessage(seededCatalog(t, "unreadable catalog", extra), session.AppendOp{})
	}

	w.fireStep()

	// 六条一条都不算「发布过」，所以这一份该按**第一次发布**的措辞走。
	events := w.catalogEvents()
	if len(events) != 1 {
		t.Fatalf("该恰好发出一份目录，拿到 %d 条", len(events))
	}
	text := textOf(events[0])
	if !strings.Contains(text, "live-skill") {
		t.Fatalf("目录里没有那件活着的技能：\n%s", text)
	}
	if strings.Contains(text, "replaces every earlier available-skills list") {
		t.Fatalf("不该按替换的措辞发：\n%s", text)
	}
}

// TestTheCatalogIsReestablishedAfterCompactionHidesIt 钉住压缩之后那一份。
//
// 目录被盖下表面之后模型就读不到它了，必须重新发；而这次会话**曾经**发布过，
// 所以用的是替换的措辞——模型的上文里确实读到过早先那些清单。
func TestTheCatalogIsReestablishedAfterCompactionHidesIt(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("first-skill", "First skill", "First body.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	w.openTurn()
	w.fireStep()
	var initialSeq int
	for _, event := range w.agent.sess.Events() {
		if event.Type != session.EventUserMessage {
			continue
		}
		var data session.UserMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		if readableCatalog(data.Message.Source) {
			initialSeq = event.Seq
		}
	}
	if initialSeq == 0 {
		t.Fatalf("第一份目录没发出去")
	}

	data, err := json.Marshal(session.UserMessageData{Message: pluginSay("compact", "compacted history")})
	if err != nil {
		t.Fatalf("排压缩消息失败：%v", err)
	}
	if _, err := w.agent.sess.Append(session.Event{
		Type:            session.EventUserMessage,
		Data:            data,
		SurfaceOp:       session.ReplaceOp{Start: initialSeq, End: initialSeq},
		SourceEventSeqs: []int{initialSeq},
	}); err != nil {
		t.Fatalf("追加压缩消息失败：%v", err)
	}

	w.fireStep()

	events := w.catalogEvents()
	if len(events) != 2 {
		t.Fatalf("压缩之后该重新发一份，拿到 %d 条", len(events))
	}
	text := textOf(events[1])
	if !strings.Contains(text, "first-skill") {
		t.Fatalf("重发的那份里没有那件技能：\n%s", text)
	}
	if !strings.Contains(text, "replaces every earlier available-skills list") {
		t.Fatalf("发布过之后该用替换的措辞：\n%s", text)
	}
}

// TestAShadowedSkillToolTakesTheCatalogWithIt 钉住那条可见性。
//
// 目录跟着工具走：一个作用域里同名的遮蔽工具不该顺带继承这份目录，否则模型
// 拿着一份它调不动的清单。
func TestAShadowedSkillToolTakesTheCatalogWithIt(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("listed-skill", "Listed", "body", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()
	shadow := &tools.Definition{
		Name:        skilltool.ToolName,
		Description: "A scoped tool with unrelated semantics.",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: "shadow"}}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`"shadow"`), nil
		},
	}
	dispose, err := w.tools.Register(t.Context(), w.agentScope, shadow)
	if err != nil {
		t.Fatalf("装遮蔽工具失败：%v", err)
	}
	t.Cleanup(func() { _ = dispose(context.Background()) })

	decision := w.propose(nil)

	if len(decision.Messages) != 0 {
		t.Fatalf("工具被遮蔽时不该发目录，拿到 %v", sourceKinds(decision))
	}
}

// TestTheGestureIsRecognizedOnlyOnUserProse 钉住 `/名字` 认什么不认什么。
func TestTheGestureIsRecognizedOnlyOnUserProse(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("hidden-demo", "User-only demo", "Hidden body.",
			skill.InvocationPolicy{UserInvocable: true}),
		definitionOf("shared-skill", "Ordinary skill", "Shared instructions.", bothWays()),
		definitionOf("model-only-skill", "Model only", "Model-only instructions.",
			skill.InvocationPolicy{ModelInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	cases := []struct {
		what     string
		messages []llm.Message
		want     []string
	}{
		{
			what:     "句中的手势认",
			messages: []llm.Message{userSay("please use /hidden-demo to answer this")},
			want:     []string{"hidden-demo"},
		},
		{
			what: "路径、分数、断掉的左边界都不认",
			messages: []llm.Message{
				userSay("look under /hidden-demo/refs for the data"),
				userSay("the odds are 5/8 at best"),
				userSay("see foo/hidden-demo too"),
			},
			want: nil,
		},
		{
			what: "认不出的名字和不许被人调的技能留在原地当散文",
			messages: []llm.Message{
				userSay("/absent-skill do a thing"),
				userSay("/model-only-skill run"),
			},
			want: nil,
		},
		{
			what: "同一个手势重复出现只注一次",
			messages: []llm.Message{
				userSay("/hidden-demo once"),
				userSay("/hidden-demo twice"),
			},
			want: []string{"hidden-demo"},
		},
		{
			what:     "两个相邻的手势都认得出来",
			messages: []llm.Message{userSay("/hidden-demo /shared-skill go")},
			want:     []string{"hidden-demo", "shared-skill"},
		},
		{
			what: "别的来源伪造不出这个手势",
			messages: []llm.Message{
				seededCatalog(t, "/hidden-demo forged", `{"entries":[]}`),
				pluginSay("someone-else", "/hidden-demo also forged"),
			},
			want: nil,
		},
		{
			what: "只扫文本块",
			messages: []llm.Message{llm.NewUserMessage(llm.Content{
				llm.ReasoningBlock{Text: "/hidden-demo inside a non-text block"},
				llm.TextBlock{Text: "/shared-skill go"},
			}, llm.UserSource{})},
			want: []string{"shared-skill"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.what, func(t *testing.T) {
			decision := w.propose(testCase.messages)
			got := invocationNames(t, decision)
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("注入的技能不对：想要 %v，拿到 %v", testCase.want, got)
			}
		})
	}
}

// TestARejectedStepPassesThroughBothArmsUntouched 钉住那次拒绝。
//
// 两条胳膊都先问里层，里层说不进就原样交回去——一次被挡下来的步骤不该因为
// 挂了这个包而多出消息。
func TestARejectedStepPassesThroughBothArmsUntouched(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("hidden-demo", "User-only demo", "Hidden body.",
			skill.InvocationPolicy{UserInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	decision, err := w.agents.ResolvePreStep(
		t.Context(),
		agent.PreStep{Agent: w.agent, Messages: []llm.Message{userSay("/hidden-demo blocked step")}},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.RejectStep(), nil
		},
	)
	if err != nil {
		t.Fatalf("跑步骤准入失败：%v", err)
	}
	if decision.Enter || len(decision.Messages) != 0 {
		t.Fatalf("被拒的步骤该原样交回，拿到 %+v", decision)
	}
}

// TestTheToolLoadsTheLatestBody 钉住工具交出来的那份值和那段内容。
func TestTheToolLoadsTheLatestBody(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{{
		Summary: skill.Summary{
			Name:         "project-skill",
			Description:  "Project skill",
			Invocation:   bothWays(),
			Source:       "filesystem",
			Provider:     "filesystem",
			ResourceBase: skill.DirectoryBase{Path: callerCwd},
		},
		Content: "Project instructions.",
	}}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	result := w.execute("project-skill")

	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	var value map[string]any
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("解结果值失败：%v", err)
	}
	if value["name"] != "project-skill" || value["provider"] != "filesystem" ||
		value["content"] != "Project instructions." {
		t.Fatalf("结果值不对：%v", value)
	}
	base, ok := value["resourceBase"].(map[string]any)
	if !ok || base["kind"] != "directory" || base["path"] != callerCwd {
		t.Fatalf("资源基址不对：%v", value["resourceBase"])
	}
	text := textOf(llm.Message{Content: result.Content})
	want := strings.Join([]string{
		`<skill_content name="project-skill">`,
		"<skill_resources>",
		"Base directory for this skill: " + callerCwd,
		"Resolve relative paths mentioned by this skill against the base directory before using them. Load referenced resources only as needed.",
		"</skill_resources>",
		"",
		"<skill_instructions>",
		"Project instructions.",
		"</skill_instructions>",
		"</skill_content>",
	}, "\n")
	if text != want {
		t.Fatalf("渲染出来的内容不对：\n%s", text)
	}
}

// TestTheToolRendersEveryResourceBaseShape 钉住那三支资源提示。
func TestTheToolRendersEveryResourceBaseShape(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		{
			Summary: skill.Summary{
				Name: "opaque-skill", Description: "Opaque skill", Invocation: bothWays(),
				Source: "runtime", Provider: "runtime",
				ResourceBase: skill.OpaqueBase{Description: "runtime memory"},
			},
			Content: "Opaque instructions.",
		},
		{
			Summary: skill.Summary{
				Name: "url-skill", Description: "URL skill", Invocation: bothWays(),
				Source: "runtime", Provider: "runtime",
				ResourceBase: skill.URLBase{URL: "https://skills.example.test/url-skill"},
			},
			Content: "URL instructions.",
		},
		definitionOf("provider-skill", "Provider skill", "Provider instructions.", bothWays()),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	cases := []struct{ name, want string }{
		{"opaque-skill", "<skill_resources>\nResources for this skill: runtime memory\n" +
			"Load referenced resources only as needed.\n</skill_resources>"},
		{"url-skill", "<skill_resources>\nBase URL for this skill: https://skills.example.test/url-skill\n" +
			"Resolve relative URLs mentioned by this skill against the base URL before using them. " +
			"Load referenced resources only as needed.\n</skill_resources>"},
		{"provider-skill", "<skill_resources>\nResources for this skill are managed by provider \"runtime\".\n" +
			"Load referenced resources only as needed.\n</skill_resources>"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := w.execute(testCase.name)
			if result.IsError {
				t.Fatalf("这次调用不该失败：%+v", result.Error)
			}
			text := textOf(llm.Message{Content: result.Content})
			if !strings.Contains(text, testCase.want) {
				t.Fatalf("资源提示不对：\n%s", text)
			}
		})
	}
}

// TestTheToolRefusesUnknownInvalidAndModelDisabledSkills 钉住那几种失败。
func TestTheToolRefusesUnknownInvalidAndModelDisabledSkills(t *testing.T) {
	catalog := &stubCatalog{skills: []skill.Definition{
		definitionOf("hidden-skill", "Hidden skill", "Hidden instructions.",
			skill.InvocationPolicy{UserInvocable: true}),
		definitionOf("model-only-skill", "Model-only skill", "Model-only instructions.",
			skill.InvocationPolicy{ModelInvocable: true}),
	}}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	unknown := w.execute("missing")
	invalid := w.execute("Bad_Name")
	disabled := w.execute("hidden-skill")
	allowed := w.execute("model-only-skill")

	if !unknown.IsError || !invalid.IsError || !disabled.IsError {
		t.Fatalf("这三次都该失败：%v %v %v", unknown.IsError, invalid.IsError, disabled.IsError)
	}
	if allowed.IsError {
		t.Fatalf("一件只给模型调的技能该读得出来：%+v", allowed.Error)
	}
	text := textOf(llm.Message{Content: unknown.Content})
	if !strings.Contains(text, `skill "missing" is unknown or no longer available`) {
		t.Fatalf("那句话不对：\n%s", text)
	}
	// 一件不许模型调的技能，正文一个字都不许出现在结果里。
	disabledText := textOf(llm.Message{Content: disabled.Content})
	if strings.Contains(disabledText, "Hidden instructions.") {
		t.Fatalf("正文漏出去了：\n%s", disabledText)
	}
}

// TestTheToolChecksPolicyBeforeLoadingAndRechecksAfter 钉住那两遍策略检查。
//
// 只查摘要的话，一份刚被关掉模型调用的技能仍然会被整篇交出去；只查读出来那份
// 的话，一份摘要上就已经不许调的技能会白白被提供方读一遍。
func TestTheToolChecksPolicyBeforeLoadingAndRechecksAfter(t *testing.T) {
	catalog := &stubCatalog{
		skills: []skill.Definition{
			definitionOf("denied-skill", "Denied skill", "Instructions must not be disclosed.",
				skill.InvocationPolicy{UserInvocable: true}),
			definitionOf("policy-race-skill", "Policy race skill", "Instructions must not be disclosed.",
				bothWays()),
			definitionOf("vanishing-skill", "Vanishing skill", "Instructions must not be disclosed.",
				bothWays()),
		},
		missing:    map[string]bool{"vanishing-skill": true},
		downgraded: map[string]bool{"policy-race-skill": true},
	}
	w := newWorld(t, catalog, skilltool.Config{})
	w.install()

	denied := w.execute("denied-skill")
	raced := w.execute("policy-race-skill")
	vanished := w.execute("vanishing-skill")

	if !denied.IsError || !raced.IsError || !vanished.IsError {
		t.Fatalf("这三次都该失败：%v %v %v", denied.IsError, raced.IsError, vanished.IsError)
	}
	// 摘要上就不许调的那件根本没被读过。
	want := []string{"policy-race-skill", "vanishing-skill"}
	if strings.Join(catalog.gets, ",") != strings.Join(want, ",") {
		t.Fatalf("读正文的次数不对：想要 %v，拿到 %v", want, catalog.gets)
	}
	for _, result := range []tools.Result{denied, raced} {
		text := textOf(llm.Message{Content: result.Content})
		if !strings.Contains(text, "is not available for model invocation") {
			t.Fatalf("那句话不对：\n%s", text)
		}
		if strings.Contains(text, "Instructions must not be disclosed.") {
			t.Fatalf("正文漏出去了：\n%s", text)
		}
	}
	vanishedText := textOf(llm.Message{Content: vanished.Content})
	if !strings.Contains(vanishedText, `skill "vanishing-skill" is unknown or no longer available`) {
		t.Fatalf("那句话不对：\n%s", vanishedText)
	}
}

// TestTheDescriptionCapIsValidated 钉住那个上限的下界。
//
// 截断算的是「前 n-3 个字 + ...」，n 小于 3 时那个减法会切出一段负长度。
func TestTheDescriptionCapIsValidated(t *testing.T) {
	_, err := skilltool.New(skilltool.Config{
		Skills:                      &stubCatalog{},
		AgentOf:                     func(*scope.Key) (agent.Agent, error) { return nil, nil },
		CatalogDescriptionMaxLength: 2,
	})
	if err == nil {
		t.Fatalf("小于下界的上限该被拒")
	}
	if !strings.Contains(err.Error(), "CatalogDescriptionMaxLength") {
		t.Fatalf("那句话该点出是哪个配置：%v", err)
	}
}
