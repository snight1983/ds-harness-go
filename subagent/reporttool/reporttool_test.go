// 本文件的作用：把 report 工具钉在它那几条真会出错的边上——装配面缺什么就不许开工、
// 那两笔登记同生共死（包括装工具失败要把指引滚回去）、投出去的内容和排期策略确实
// 到了服务那边，以及「这次调用落在哪个孩子身上」这条钥匙查回去的路。
//
// # 这些测试防的是什么错
//
//   - **指引留在那儿、工具没装上**。孩子会读到一段说明一件它根本调不动的工具的
//     提示词，然后一直试着去调。
//   - **排期策略被吞掉**。写成 quiet 却按 next-step 投，等于每份汇报都把父唤醒；
//     反过来则是父再也醒不过来。这两种毛病在现场都看不出是谁干的。
//   - **投给了别人**。孩子本身就是那份权证，收件人不许由参数决定。
//   - **登记露到孩子作用域外面**。父和兄弟看得见 report 就等于谁都能往上投。

package reporttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/invariants"
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/subagent/subagent"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包除了「把它原样递给服务」之外不碰它的任何东西，所以这些方法全是哑的。
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

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// reportCall 是服务收到的那一次投递。
type reportCall struct {
	child   agent.Agent
	content llm.Content
	options subagent.ReportOptions
}

// stubService 是那台记账的假子 agent 运行时。
type stubService struct {
	// messageID 是投递成功时交回的那条消息标识。
	messageID llm.MessageID
	// reportErr 不为 nil 时每次投递都失败。
	reportErr error
	// registerErr 不为 nil 时登记贡献失败。
	registerErr error

	// reports 按顺序记下每一次投递。
	reports []reportCall
	// registered 是被登记进来的那份贡献。
	registered subagent.ActivationSetupContribution
	// undone 记下那个撤销函数被调过。
	undone bool
}

func (s *stubService) ReportFrom(
	_ context.Context,
	child agent.Agent,
	content llm.Content,
	options subagent.ReportOptions,
) (llm.MessageID, error) {
	s.reports = append(s.reports, reportCall{child: child, content: content, options: options})
	if s.reportErr != nil {
		return "", s.reportErr
	}
	return s.messageID, nil
}

func (s *stubService) RegisterContinuableSetup(
	contribution subagent.ActivationSetupContribution,
) (func(context.Context) error, error) {
	if s.registerErr != nil {
		return nil, s.registerErr
	}
	s.registered = contribution
	return func(context.Context) error {
		s.undone = true
		return nil
	}, nil
}

// ---- 装配 ----

// world 是一台装好的 report 工具周边。
type world struct {
	t *testing.T

	service *stubService
	tools   *tools.Runtime
	prompts *systemprompt.Registry
	// child 是那个孩子的作用域：两笔登记都归它。
	child *scope.Scope
	// outsider 是一个跟孩子无关的作用域，用来验那两笔登记没露出去。
	outsider *scope.Scope
	// agentOf 交回的那个孩子。
	childAgent *stubAgent
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
	prompts, err := systemprompt.NewRegistry(t.Context(), scopeOf(t, "prompt-root"), systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	child := scopeOf(t, "child")
	return &world{
		t:          t,
		service:    &stubService{messageID: "msg-1"},
		tools:      toolRuntime,
		prompts:    prompts,
		child:      child,
		outsider:   scopeOf(t, "outsider"),
		childAgent: &stubAgent{id: "child", own: child},
	}
}

// config 交出一份完整的装配面。
func (w *world) config() Config {
	return Config{
		Service: w.service,
		Tools:   w.tools,
		Prompts: w.prompts,
		AgentOf: func(key *scope.Key) (agent.Agent, error) {
			if key != w.child.Key() {
				return nil, errors.New("这把钥匙不是那个孩子的")
			}
			return w.childAgent, nil
		},
	}
}

// controller 造一个控制器，造不出来当场失败。
func (w *world) controller(config Config) *Controller {
	w.t.Helper()
	controller, err := New(config)
	if err != nil {
		w.t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

// sectionText 交出一个作用域装配出来的那段 report 指引；没有就是空串。
func (w *world) sectionText(key *scope.Key) string {
	w.t.Helper()
	assembly, err := w.prompts.Assemble(w.t.Context(), systemprompt.AssembleContext{Scope: key})
	if err != nil {
		w.t.Fatalf("装配提示词失败：%v", err)
	}
	for _, section := range assembly.Sections {
		if section.Name == SectionName {
			return section.Text
		}
	}
	return ""
}

// call 走真运行时调一次 report。
func (w *world) call(key *scope.Key, output string) tools.Result {
	w.t.Helper()
	args, err := json.Marshal(reportArgs{Output: output})
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	return w.tools.Execute(w.t.Context(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      ToolName,
		Arguments: args,
		Agent:     key,
	})
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
		"没有工具运行时":          func(config *Config) { config.Tools = nil },
		"没有系统提示词注册表":       func(config *Config) { config.Prompts = nil },
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

// 排期策略拼错了要当场拦住：漏到运行期的表现是汇报静静地不唤醒父，那种毛病
// 从现场几乎看不出来是配置写错了。
func TestNewResolvesTheDeliveryPolicy(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		given   subagent.ReportDelivery
		want    subagent.ReportDelivery
		refused bool
	}{
		"空串取默认值":   {given: "", want: DefaultDelivery},
		"quiet 照收": {given: subagent.DeliveryQuiet, want: subagent.DeliveryQuiet},
		"next-step 照收": {
			given: subagent.DeliveryNextStep,
			want:  subagent.DeliveryNextStep,
		},
		"认不得的一律拦住": {given: "whenever", refused: true},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.config()
			config.Delivery = each.given

			controller, err := New(config)
			if each.refused {
				if err == nil {
					t.Fatalf("%q 该被拦住", each.given)
				}
				return
			}
			if err != nil {
				t.Fatalf("造控制器失败：%v", err)
			}
			if controller.delivery != each.want {
				t.Fatalf("排期策略该是 %q，实际 %q", each.want, controller.delivery)
			}
		})
	}
}

// ---- 那两笔登记 ----

func TestContributeInstallsBothRegistrationsIntoTheChildScopeOnly(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	release, err := w.controller(w.config()).Contribute(t.Context(), w.child)
	if err != nil {
		t.Fatalf("装配贡献失败：%v", err)
	}

	if _, ok := w.tools.Get(ToolName, w.child.Key()); !ok {
		t.Fatalf("孩子那边看不见 %s", ToolName)
	}
	if got := w.sectionText(w.child.Key()); got != promptText {
		t.Fatalf("孩子那段指引不对：%q", got)
	}
	// 父和兄弟看得见 report 就等于谁都能往上投。
	if _, ok := w.tools.Get(ToolName, w.outsider.Key()); ok {
		t.Fatalf("%s 露到孩子作用域外面去了", ToolName)
	}
	if got := w.sectionText(w.outsider.Key()); got != "" {
		t.Fatalf("那段指引露到孩子作用域外面去了：%q", got)
	}

	if err := release(t.Context()); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	if _, ok := w.tools.Get(ToolName, w.child.Key()); ok {
		t.Fatalf("释放之后 %s 还在", ToolName)
	}
	if got := w.sectionText(w.child.Key()); got != "" {
		t.Fatalf("释放之后那段指引还在：%q", got)
	}
}

// 装工具失败要把指引滚回去：不能给这个孩子留一段说明一件它根本调不动的工具的提示词。
func TestContributeRollsBackTheGuidanceWhenTheToolCannotBeRegistered(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(w.config())
	// 同一个作用域上已经有一件叫 report 的工具了，第二次登记必然撞名。
	if _, err := w.tools.Register(t.Context(), w.child, controller.definition); err != nil {
		t.Fatalf("先占位那次登记失败：%v", err)
	}

	if _, err := controller.Contribute(t.Context(), w.child); err == nil {
		t.Fatal("撞名了还装得上")
	}
	if got := w.sectionText(w.child.Key()); got != "" {
		t.Fatalf("工具没装上，那段指引却留下了：%q", got)
	}
}

// 指引先装：它装不上的话一件工具都还没露出去。
func TestContributeInstallsNothingWhenTheGuidanceFails(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(w.config())
	if _, err := w.prompts.Section(t.Context(), w.child, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText("先来的"),
	}); err != nil {
		t.Fatalf("先占位那段指引失败：%v", err)
	}

	if _, err := controller.Contribute(t.Context(), w.child); err == nil {
		t.Fatal("段名撞了还装得上")
	}
	if _, ok := w.tools.Get(ToolName, w.child.Key()); ok {
		t.Fatalf("指引没装上，%s 却露出去了", ToolName)
	}
}

// ---- 投递 ----

func TestReportDeliversTheOutputAndNamesTheAcceptedMessage(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	config := w.config()
	config.Delivery = subagent.DeliveryQuiet
	if _, err := w.controller(config).Contribute(t.Context(), w.child); err != nil {
		t.Fatalf("装配贡献失败：%v", err)
	}

	result := w.call(w.child.Key(), "查完了：三个候选都不行")
	if result.IsError {
		t.Fatalf("这次调用该成功，实际：%s", textOf(result.Content))
	}
	want := "report accepted by the agent that started you as message msg-1"
	if got := textOf(result.Content); got != want {
		t.Fatalf("给模型的话该是 %q，实际 %q", want, got)
	}

	if len(w.service.reports) != 1 {
		t.Fatalf("该恰好投一次，实际 %d 次", len(w.service.reports))
	}
	delivered := w.service.reports[0]
	// 孩子本身就是那份权证：收件人是它的父，调用方点不了。
	if delivered.child != w.childAgent {
		t.Fatalf("投的不是那个孩子，实际 %#v", delivered.child)
	}
	if got := textOf(delivered.content); got != "查完了：三个候选都不行" {
		t.Fatalf("投出去的正文不对：%q", got)
	}
	// 排期策略被吞掉的话，一个 quiet 部署会变成每份汇报都把父唤醒。
	if delivered.options.Delivery != subagent.DeliveryQuiet {
		t.Fatalf("排期策略该是 quiet，实际 %q", delivered.options.Delivery)
	}
}

func TestReportSurfacesADeliveryFailureAsAToolError(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.reportErr = errors.New("父已经走了")
	if _, err := w.controller(w.config()).Contribute(t.Context(), w.child); err != nil {
		t.Fatalf("装配贡献失败：%v", err)
	}

	result := w.call(w.child.Key(), "在的")
	if !result.IsError {
		t.Fatal("投递失败了这次调用还算成功")
	}
}

// 这件工具只装在孩子作用域上，所以「查不回去」只发生在装配出错的时候——那时候
// 给模型一句话，好过让整个回合炸掉。
func TestReportRefusesACallThatIsNotBoundToAnAgent(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(w.config())

	cases := map[string]*tools.RunContext{
		"压根没有执行上下文":  nil,
		"执行上下文里没有钥匙": {},
		"钥匙查不回任何 agent": {
			Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{Agent: w.outsider.Key()}},
		},
	}
	for name, exec := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := controller.execute(t.Context(), json.RawMessage(`{"output":"在的"}`), exec)
			if err == nil || err.Error() != missingAgent {
				t.Fatalf("该报 %q，实际 %v", missingAgent, err)
			}
		})
	}
}

// 走运行时这条路的参数一定是合法 JSON（运行时在进执行体之前就验过），所以这条边
// 只有直调才碰得到。它仍旧要有：一个不报错的解析失败会把空字符串当成汇报投出去。
func TestReportRefusesUnreadableArguments(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	if _, err := w.controller(w.config()).execute(t.Context(), json.RawMessage(`{`), nil); err == nil {
		t.Fatal("参数读不出来还算成功")
	}
	if len(w.service.reports) != 0 {
		t.Fatalf("参数读不出来不该投出去，实际投了 %d 次", len(w.service.reports))
	}
}

// 渲染那句话靠的是结果里的 messageId；读不出来要报错，不能悄悄渲染成一句
// 「as message 」然后让模型以为投成功了。
func TestRenderRefusesAnUnreadableResult(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	render := w.controller(w.config()).definition.Output.Render
	if _, err := render(nil, json.RawMessage(`{`)); err == nil {
		t.Fatal("结果读不出来还渲染得出来")
	}
}

// ---- 登记这份贡献 ----

func TestInstallRegistersTheContributionAndUndoRemovesIt(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	undo, err := w.controller(w.config()).Install()
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	if w.service.registered == nil {
		t.Fatal("没有贡献被登记进去")
	}
	// 登记进去的必须是真能装出那两笔的那一个，而不是随便一个签名对得上的函数。
	release, err := w.service.registered(t.Context(), w.child)
	if err != nil {
		t.Fatalf("跑那份被登记的贡献失败：%v", err)
	}
	if _, ok := w.tools.Get(ToolName, w.child.Key()); !ok {
		t.Fatalf("那份被登记的贡献没装上 %s", ToolName)
	}
	if err := release(t.Context()); err != nil {
		t.Fatalf("释放失败：%v", err)
	}

	if err := undo(t.Context()); err != nil {
		t.Fatalf("撤销登记失败：%v", err)
	}
	if !w.service.undone {
		t.Fatal("撤销没落到服务那边")
	}
}

func TestInstallSurfacesARegistrationFailure(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.registerErr = errors.New("这台运行时没组装续接管理器")
	if _, err := w.controller(w.config()).Install(); err == nil {
		t.Fatal("登记失败了还算成功")
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
