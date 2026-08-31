// 本文件的作用：把这件工具的全部可观察行为钉住——给模型看的那份 schema 长什么样、
// 参数往接缝那边翻成了什么、答案翻回来又是什么、以及接缝报的错怎么落地。
//
// 逐条对着 DSH 的 tests/tool-ask-user.spec.ts 走。那边靠 cordis 把整套服务装起来，
// 这里换成一个假的 [askuser.Asker]：这件工具自己不判断任何事，所以要钉的是它和
// 接缝之间那份翻译，而不是接缝的判断——那些在 interaction/userquestions 里已经钉过。
//
// # 这些测试防的是什么错
//
//   - **schema 长出了新字段**。选项只准有 label 和 description：多一个 value 或者
//     recommended，一份离开界面的答案就不再自解释，而模型也会开始按下标作答。
//   - **翻译把可选字段弄丢了**。header、multi_select、custom 任何一个漏译，界面
//     画出来的东西就和模型想问的不是一回事。
//   - **接缝报的错被这一层重新包装了一遍**。那样 Failure.Info 里的代号就没了，
//     下游只能回去解析错误文本。
package askuser_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/interaction/askuser"
	"ds-harness-go/interaction/userquestions"
	"ds-harness-go/llm"
)

// stubAsker 是一道把每次请求都记下来的假接缝。
type stubAsker struct {
	seen   []userquestions.Request
	answer userquestions.Answer
	// cancelled 记下每一次提问时那个上下文取消了没有。
	cancelled []bool
	// failWith 非 nil 时，每一次提问都以它失败。
	failWith error
}

// Ask 记下这次请求，交出事先摆好的那份答案。
func (a *stubAsker) Ask(ctx context.Context, request userquestions.Request) (userquestions.Answer, error) {
	a.seen = append(a.seen, request)
	a.cancelled = append(a.cancelled, ctx.Err() != nil)
	if a.failWith != nil {
		return userquestions.Answer{}, a.failWith
	}
	return a.answer, nil
}

// harness 是一次工具测试要的全套家当。
type harness struct {
	runtime *tools.Runtime
	agent   *scope.Key
	asker   *stubAsker
}

// newHarness 造一个装好 ask_user_question 的运行时。
func newHarness(t *testing.T, answer userquestions.Answer) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	asker := &stubAsker{answer: answer}
	tool, err := askuser.New(askuser.Config{Questions: asker})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	if _, err := tool.Install(context.Background(), runtime, scope.NewRoot()); err != nil {
		t.Fatalf("装这件工具失败：%v", err)
	}
	return &harness{runtime: runtime, agent: scope.NewKey("agent"), asker: asker}
}

// call 跑一次由 agent 发起的 ask_user_question。
func (h *harness) call(args string) tools.Result {
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("ask-1"),
		Name:      askuser.ToolName,
		Arguments: json.RawMessage(args),
		Agent:     h.agent,
	})
}

// definition 取出装进去的那份定义，用来直接调那几条走不到运行时的分支。
func (h *harness) definition(t *testing.T) *tools.Definition {
	t.Helper()
	found, ok := h.runtime.Get(askuser.ToolName, h.agent)
	if !ok {
		t.Fatalf("装上之后该查得到 %q", askuser.ToolName)
	}
	return found
}

// textOf 把一份内容拼成可断言的纯文本。
func textOf(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "")
}

// propertyOf 从一个对象节点上取一个具名属性。
func propertyOf(t *testing.T, node tools.Node, name string) tools.Node {
	t.Helper()
	for _, property := range node.Properties {
		if property.Name == name {
			return property.Schema
		}
	}
	t.Fatalf("该有属性 %q，拿到 %+v", name, node.Properties)
	return tools.Node{}
}

// names 列出一个对象节点上声明了哪些属性。
func names(node tools.Node) []string {
	list := make([]string, 0, len(node.Properties))
	for _, property := range node.Properties {
		list = append(list, property.Name)
	}
	return list
}

func TestRegistersAModelFacingSchema(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)

	if definition.Name != askuser.ToolName {
		t.Fatalf("名字不对：%q", definition.Name)
	}
	if !strings.Contains(definition.Description, "stable id") {
		t.Fatalf("那段说明该点明 id 要稳定：%q", definition.Description)
	}
	if strings.Join(definition.Parameters.Required, ",") != "questions" {
		t.Fatalf("questions 该是必填：%+v", definition.Parameters.Required)
	}

	questions := propertyOf(t, definition.Parameters, "questions")
	if questions.Type != tools.TypeArray || questions.Items == nil {
		t.Fatalf("questions 该是一个数组：%+v", questions)
	}
	item := *questions.Items
	for _, want := range []struct {
		name string
		kind tools.SchemaType
	}{
		{"id", tools.TypeString},
		{"question", tools.TypeString},
		{"header", tools.TypeString},
		{"options", tools.TypeArray},
		{"multi_select", tools.TypeBoolean},
	} {
		if got := propertyOf(t, item, want.name); got.Type != want.kind {
			t.Fatalf("%s 该是 %s，拿到 %s", want.name, want.kind, got.Type)
		}
	}

	options := propertyOf(t, item, "options")
	if options.Items == nil {
		t.Fatal("options 该有条目 schema")
	}
	// 选项只准有这两个字段。多一个 value、recommended 或者 preview，一份离开界面的
	// 答案就不再自解释——答案里回来的是标签本身，别的都要靠界面在场才能翻译。
	if got := strings.Join(names(*options.Items), ","); got != "label,description" {
		t.Fatalf("选项的字段该恰好是 label 和 description，拿到 %q", got)
	}
}

func TestAsksTheSeamAndProjectsTheAnswerToText(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{
		Answers: []userquestions.AnswerItem{{ID: "pkg", Selected: []string{"pnpm"}}},
	})

	result := h.call(`{"questions":[{
		"id":"pkg","question":"Which package manager should I use?",
		"options":[{"label":"pnpm","description":"Use pnpm workspaces."}]
	}]}`)
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if got := textOf(result.Content); got != `{"answers":[{"id":"pkg","selected":["pnpm"]}]}` {
		t.Fatalf("给模型看的该是那份答案的原文：%q", got)
	}

	if len(h.asker.seen) != 1 {
		t.Fatalf("该恰好问一次，拿到 %d 次", len(h.asker.seen))
	}
	request := h.asker.seen[0]
	if request.Agent != h.agent {
		t.Fatal("该把发起这次调用的那个 agent 一起交给接缝")
	}
	want := userquestions.Item{
		ID:       "pkg",
		Question: "Which package manager should I use?",
		Options:  []userquestions.Option{{Label: "pnpm", Description: "Use pnpm workspaces."}},
	}
	if len(request.Questions) != 1 {
		t.Fatalf("该恰好交一个问题：%+v", request.Questions)
	}
	got := request.Questions[0]
	if got.ID != want.ID || got.Question != want.Question || got.Header != "" ||
		got.MultiSelect || len(got.Options) != 1 || got.Options[0] != want.Options[0] {
		t.Fatalf("翻给接缝的那个问题不对：%+v", got)
	}
}

func TestPassesRecommendedLabelsThroughUntouched(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{
		Answers: []userquestions.AnswerItem{{ID: "pkg", Selected: []string{"pnpm (Recommended)"}}},
	})

	// 推荐写在标签文本里，schema 上没有对应字段——这一层不许替模型解释它。
	if result := h.call(`{"questions":[{
		"id":"pkg","question":"Which package manager should I use?",
		"options":[{"label":"pnpm (Recommended)"},{"label":"npm"}]
	}]}`); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}

	options := h.asker.seen[0].Questions[0].Options
	if len(options) != 2 || options[0].Label != "pnpm (Recommended)" || options[1].Label != "npm" {
		t.Fatalf("那两个标签该原样过去：%+v", options)
	}
	if options[0].Description != "" || options[1].Description != "" {
		t.Fatalf("没写说明就该是空的：%+v", options)
	}
}

func TestProjectsCustomAnswersAndMultiSelectChoices(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{Answers: []userquestions.AnswerItem{
		{ID: "targets", Selected: []string{"tests", "docs"}, Custom: "release notes"},
		{ID: "labels-only", Selected: []string{"tests"}},
		{ID: "notes", Selected: nil, Custom: "ship today"},
	}})

	result := h.call(`{"questions":[
		{"id":"targets","question":"What should I update?",
		 "options":[{"label":"tests"},{"label":"docs"}],"multi_select":true},
		{"id":"labels-only","question":"Which labels should I keep?",
		 "options":[{"label":"tests"},{"label":"docs"}],"multi_select":true},
		{"id":"notes","question":"Any note?"}
	]}`)
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}

	// 没选任何东西时 selected 必须编码成 []：输出 schema 说它必填，一个 null
	// 会让这份值装不回那份 schema。
	const want = `{"answers":[` +
		`{"id":"targets","selected":["tests","docs"],"custom":"release notes"},` +
		`{"id":"labels-only","selected":["tests"]},` +
		`{"id":"notes","selected":[],"custom":"ship today"}]}`
	if got := string(result.Value); got != want {
		t.Fatalf("那份值不对：\n拿到 %s\n要   %s", got, want)
	}
	if got := textOf(result.Content); got != want {
		t.Fatalf("给模型看的该是同一份原文：%q", got)
	}

	if !h.asker.seen[0].Questions[0].MultiSelect || h.asker.seen[0].Questions[2].MultiSelect {
		t.Fatalf("multi_select 该逐题翻过去：%+v", h.asker.seen[0].Questions)
	}
}

func TestPassesTheOptionalHeaderThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{
		Answers: []userquestions.AnswerItem{{ID: "continue", Selected: []string{"ok"}}},
	})

	result := h.call(`{"questions":[{"id":"continue","header":"Confirm","question":"Continue?"}]}`)
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if got := textOf(result.Content); got != `{"answers":[{"id":"continue","selected":["ok"]}]}` {
		t.Fatalf("给模型看的那份答案不对：%q", got)
	}
	if got := h.asker.seen[0].Questions[0].Header; got != "Confirm" {
		t.Fatalf("那个标题该翻过去，拿到 %q", got)
	}
}

func TestPassesTheCancellationThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 取消在 Go 里是 Execute 的第一个参数（DSH 那边是请求对象上的 signal），
	// 它必须**原样**一路传到接缝：接缝那道门就是靠它拒掉一次已经取消的提问的，
	// 中途换成一个新的背景上下文，那道门就永远看不到取消。
	if _, err := definition.Execute(ctx,
		json.RawMessage(`{"questions":[{"id":"a","question":"?"}]}`),
		&tools.RunContext{}); err != nil {
		t.Fatalf("这道假接缝不会失败：%v", err)
	}
	if len(h.asker.cancelled) != 1 || !h.asker.cancelled[0] {
		t.Fatalf("接缝该看见那个已经取消的上下文：%+v", h.asker.cancelled)
	}
}

func TestReturnsTheSeamsStructuredError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	h.asker.failWith = &userquestions.Error{
		Code:    userquestions.CodeNoProvider,
		Message: "no user-questions provider is registered",
	}

	result := h.call(`{"questions":[{"id":"continue","question":"Continue?"}]}`)
	if !result.IsError || result.Error == nil {
		t.Fatalf("接缝报错时这次调用该失败：%+v", result)
	}
	// 代号必须原样落进 Failure.Info：这一层要是把错误重新包装一遍，下游就只剩
	// 解析错误文本这一条路了。
	if result.Error.Info == nil ||
		result.Error.Info.Name != "UserQuestionError" ||
		result.Error.Info.Code != userquestions.CodeNoProvider {
		t.Fatalf("那条错误的身份该原样抄进 Info：%+v", result.Error.Info)
	}
	if result.Error.Message != "no user-questions provider is registered" {
		t.Fatalf("那句话该原样交出来：%q", result.Error.Message)
	}
}

func TestDoesNotJudgeAnEmptyBatchItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	h.asker.failWith = &userquestions.Error{
		Code:    userquestions.CodeEmptyQuestions,
		Message: "ask_user_question requires at least one question",
	}

	// 一批空问题这一层不拦：拒绝的话术只该有一份，界面自己发起的请求和模型发起的
	// 请求要撞同一堵墙。这里要钉的就是它**确实**走到了接缝。
	result := h.call(`{"questions":[]}`)
	if !result.IsError || result.Error == nil || result.Error.Info == nil ||
		result.Error.Info.Code != userquestions.CodeEmptyQuestions {
		t.Fatalf("该把这一批交给接缝去拒：%+v", result)
	}
	if len(h.asker.seen) != 1 || len(h.asker.seen[0].Questions) != 0 {
		t.Fatalf("该原样交一批空问题过去：%+v", h.asker.seen)
	}
}

func TestTheRegistryRejectsWhatTheSchemaCanExpress(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"少了 id":             `{"questions":[{"question":"?"}]}`,
		"少了 question":       `{"questions":[{"id":"a"}]}`,
		"选项少了 label":        `{"questions":[{"id":"a","question":"?","options":[{"description":"x"}]}]}`,
		"multi_select 不是布尔": `{"questions":[{"id":"a","question":"?","multi_select":"yes"}]}`,
		"根本没给 questions":    `{}`,
	}
	for label, args := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, userquestions.Answer{})

			result := h.call(args)
			if !result.IsError {
				t.Fatalf("这份参数该被 schema 那道门拒掉：%s", args)
			}
			if len(h.asker.seen) != 0 {
				t.Fatal("被 schema 拒掉的调用不该惊动接缝")
			}
		})
	}
}

func TestAcceptsAQuestionCarryingUnknownFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{
		Answers: []userquestions.AnswerItem{{ID: "a", Selected: []string{"ok"}}},
	})

	// 问题这个对象上 additionalProperties 是 true：模型多写了字段，宁可放进来由
	// 接缝去看，也不要在这道门上把整批问题拒掉——问人本来就是它卡住了才做的事。
	if result := h.call(`{"questions":[{"id":"a","question":"?","preview":"x"}]}`); result.IsError {
		t.Fatalf("多一个字段不该被拒：%+v", result.Error)
	}
	if len(h.asker.seen) != 1 {
		t.Fatalf("该照常问出去：%+v", h.asker.seen)
	}
}

func TestPresentsTheCallWithTheRawQuestions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)

	view, ok := definition.PresentCall(json.RawMessage(
		`{"questions":[{"id":"a","question":"?"}]}`)).(tools.GenericCallView)
	if !ok {
		t.Fatal("该是一张通用卡片")
	}
	if view.Title != "Ask the user" || view.Kind != tools.CallOther {
		t.Fatalf("卡片的标题或类别不对：%+v", view)
	}
	if string(view.RawInput) != `[{"id":"a","question":"?"}]` {
		t.Fatalf("那批问题该原样进卡片：%s", view.RawInput)
	}
}

func TestPresentsATitleOnlyCardWhenTheArgumentsAreUnreadable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)

	// 呈现必须是纯函数：读不出那批问题就只给标题，绝不去碰别的地方。
	view, ok := definition.PresentCall(json.RawMessage(`nope`)).(tools.GenericCallView)
	if !ok {
		t.Fatal("该是一张通用卡片")
	}
	if view.Title != "Ask the user" || view.RawInput != nil {
		t.Fatalf("该只剩标题：%+v", view)
	}
}

func TestRenderRefusesAValueItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)

	if _, err := definition.Output.Render(nil, json.RawMessage(`nope`)); err == nil {
		t.Fatal("读不回来的值该报错，而不是折成一句空话")
	}
}

func TestExecuteRefusesArgumentsItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userquestions.Answer{})
	definition := h.definition(t)

	if _, err := definition.Execute(context.Background(), json.RawMessage(`nope`),
		&tools.RunContext{}); err == nil {
		t.Fatal("读不回来的参数该报错")
	}
	if len(h.asker.seen) != 0 {
		t.Fatal("参数都读不回来就不该惊动接缝")
	}
}

func TestUndoRemovesTheTool(t *testing.T) {
	t.Parallel()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	tool, err := askuser.New(askuser.Config{Questions: &stubAsker{}})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	undo, err := tool.Install(context.Background(), runtime, scope.NewRoot())
	if err != nil {
		t.Fatalf("装这件工具失败：%v", err)
	}
	if _, ok := runtime.Get(askuser.ToolName, nil); !ok {
		t.Fatal("前置条件：装上之后查得到")
	}

	if err := undo(context.Background()); err != nil {
		t.Fatalf("注销失败：%v", err)
	}
	if _, ok := runtime.Get(askuser.ToolName, nil); ok {
		t.Fatal("注销之后就不该再查得到")
	}
}

func TestNewRejectsAConfigWithoutASeam(t *testing.T) {
	t.Parallel()
	_, err := askuser.New(askuser.Config{})
	if !errors.Is(err, askuser.ErrInvalidConfig) {
		t.Fatalf("没有接缝该被拒：%v", err)
	}
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	tool, err := askuser.New(askuser.Config{Questions: &stubAsker{}})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	if _, err := tool.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有注册表该被拒")
	}
}
