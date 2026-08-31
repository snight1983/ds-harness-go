// 本文件的作用：把这一层的判定和措辞全部钉住——什么时候提醒、提醒说什么、
// 什么会把链断掉、以及它怎么和别的执行后规则相处。
//
// 逐条对着 DSH 的 tests/repeat-tool-reminder.spec.ts 走，除了两条在 Go 这边
// **写不出来**的：那边要拒绝一个「不是整数的阈值」和一个「带小数的字数上限」，
// 而这两个字段在 Go 里都是 int。写不出来的输入不需要验。
//
// # 这些测试防的是什么错
//
//   - **把不是循环的重复报成循环**。分页遍历、轮询、按顺序写十个文件，看起来
//     都像重复。判定的键必须同时包含工具名和完整参数，少一样就会误报。
//   - **让一个无关工具把链甩掉**。模型只要在两次同样的调用中间插一次
//     `todo_write`，这一层就再也追不上它了——所以没被跟踪的调用必须是透明的。
//   - **把别人挂的上下文冲掉**。这一层是往前面**插**一条，不是覆盖；
//     被它插过的裁决，原有的反馈、替换值、上下文一个都不能少。
//   - **在一个 agent 上数出另一个 agent 的账**。两个 agent 各调各的，
//     谁也不该因为对方而收到提醒。
package repeattoolreminder_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/guard/repeattoolreminder"
	"ds-harness-go/llm"
)

// harness 是一次测试要的全套家当：注册表、装好的这一层、和一个有身份的作用域。
type harness struct {
	runtime  *tools.Runtime
	reminder *repeattoolreminder.Reminder
	agent    *scope.Key
}

// newHarness 造一个注册表，装上这一层，注册一批工具。
func newHarness(t *testing.T, config repeattoolreminder.Config, toolNames ...string) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	reminder, err := repeattoolreminder.New(config)
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := reminder.Install(context.Background(), runtime, scope.NewRoot()); err != nil {
		t.Fatalf("装这一层失败：%v", err)
	}
	for _, name := range toolNames {
		if _, err := runtime.Register(context.Background(), scope.NewRoot(), anyArgsTool(name)); err != nil {
			t.Fatalf("注册 %q 失败：%v", name, err)
		}
	}
	return &harness{runtime: runtime, reminder: reminder, agent: scope.NewKey("agent")}
}

// anyArgsTool 造一个收任意参数、回一句 ok 的工具。
//
// 参数 schema 不加约束是有意的：这一层的判定看的是模型**写了什么**，
// 不是那份参数合不合法，所以测试不该被参数校验挡在门外。
func anyArgsTool(name string) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: "ok"}}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.Marshal("ok")
		},
	}
}

// run 跑一次调用，交出它最终那份结果。
func (h *harness) run(t *testing.T, name, arguments string) tools.Result {
	t.Helper()
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      name,
		Arguments: json.RawMessage(arguments),
		Agent:     h.agent,
	})
}

// reminders 把一份结果里这一层插的那些提醒的正文取出来。
//
// 按来源筛而不是按位置取：这一层的承诺是「插在最前面且带自己的来源标签」，
// 按位置断言会让一次「标签掉了」的回归溜过去。
func reminders(result tools.Result) []string {
	var texts []string
	for _, message := range result.AdditionalContexts {
		source, ok := message.Source.(llm.PluginSource)
		if !ok || source.Plugin != "repeat-tool-reminder" {
			continue
		}
		for _, block := range message.Content {
			if typed, ok := block.(llm.TextBlock); ok {
				texts = append(texts, typed.Text)
			}
		}
	}
	return texts
}

// only 断言一次调用恰好收到一条提醒，并交出它的正文。
func only(t *testing.T, result tools.Result) string {
	t.Helper()
	texts := reminders(result)
	if len(texts) != 1 {
		t.Fatalf("该收到恰好一条提醒，拿到 %d 条：%v", len(texts), texts)
	}
	return texts[0]
}

// silent 断言一次调用一条提醒都没收到。
func silent(t *testing.T, result tools.Result) {
	t.Helper()
	if texts := reminders(result); len(texts) != 0 {
		t.Fatalf("这次不该有提醒，拿到：%v", texts)
	}
}

func TestEscalatesFromGentleToDetailed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{}, "read")

	silent(t, h.run(t, "read", `{"path":"a"}`))
	silent(t, h.run(t, "read", `{"path":"a"}`))
	gentle := only(t, h.run(t, "read", `{"path":"a"}`))
	if !strings.HasPrefix(gentle, "You are repeating the exact same tool call") {
		t.Fatalf("第三次该说那句客气话，拿到：%q", gentle)
	}

	silent(t, h.run(t, "read", `{"path":"a"}`))
	detailed := only(t, h.run(t, "read", `{"path":"a"}`))
	for _, want := range []string{"Repeated tool call detected", "- tool: read", "- consecutive_calls: 5", `- arguments: {"path":"a"}`} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("第五次那句详细提醒里少了 %q：\n%s", want, detailed)
		}
	}
}

func TestGentleTierFollowsTheSmallestThreshold(t *testing.T) {
	t.Parallel()
	// 故意乱序给，验证这一层自己排好——「最小的那一级说得客气些」是规则的本意。
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{4, 2}}, "read")

	silent(t, h.run(t, "read", `{}`))
	if gentle := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(gentle, "You are repeating") {
		t.Fatalf("第二次该是客气那一句，拿到：%q", gentle)
	}
	silent(t, h.run(t, "read", `{}`))
	if detailed := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(detailed, "Repeated tool call detected") {
		t.Fatalf("第四次该是详细那一句，拿到：%q", detailed)
	}
}

func TestPreviewCapsTheQuotedArgumentsButNotTheDetection(t *testing.T) {
	t.Parallel()
	// 要两级：只有详细那一句才引用参数，而最小的一级永远说的是那句客气话。
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2, 3}, ArgumentsPreviewChars: 12}, "write")
	body := strings.Repeat("x", 200)
	arguments := `{"body":"` + body + `"}`

	silent(t, h.run(t, "write", arguments))
	only(t, h.run(t, "write", arguments))
	detailed := only(t, h.run(t, "write", arguments))
	if !strings.Contains(detailed, `- arguments: {"body":"xxx… (+199 more chars)`) {
		t.Fatalf("参数没按上限截断：\n%s", detailed)
	}
	if strings.Contains(detailed, body) {
		t.Fatal("完整的参数不该原样搭车进提醒")
	}

	// 判定仍然用完整的串：只有末尾一个字符不同的两份参数是两次不同的调用，
	// 所以链断了，不该再有提醒。
	silent(t, h.run(t, "write", `{"body":"`+body+`y"}`))
}

func TestADifferentTrackedCallResetsTheChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read", "write")

	silent(t, h.run(t, "read", `{}`))
	silent(t, h.run(t, "write", `{}`))
	silent(t, h.run(t, "read", `{}`))
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("换回来之后重新数到 2 才该提醒，拿到：%q", got)
	}
}

func TestExcludedCallsAreTransparent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}, Exclude: []string{"todo_*"}}, "read", "todo_write")

	silent(t, h.run(t, "read", `{}`))
	// 被排除的这次既不计数，也不断链。
	silent(t, h.run(t, "todo_write", `{}`))
	silent(t, h.run(t, "todo_write", `{}`))
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("插在中间的无关调用不该把链甩掉，拿到：%q", got)
	}
}

func TestIncludeTracksOnlyMatchingTools(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}, Include: []string{"mcp_*"}}, "read", "mcp_query")

	silent(t, h.run(t, "read", `{}`))
	silent(t, h.run(t, "read", `{}`))
	silent(t, h.run(t, "read", `{}`))

	silent(t, h.run(t, "mcp_query", `{}`))
	if got := only(t, h.run(t, "mcp_query", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("命中 include 的工具该被跟踪，拿到：%q", got)
	}
}

func TestPatternMetacharactersAreLiteral(t *testing.T) {
	t.Parallel()
	// `.` 在正则里是「任意字符」，在这套写法里只是一个点。
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}, Exclude: []string{"a.b"}}, "a.b", "axb")

	silent(t, h.run(t, "a.b", `{}`))
	silent(t, h.run(t, "a.b", `{}`))

	silent(t, h.run(t, "axb", `{}`))
	if got := only(t, h.run(t, "axb", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("axb 不该被 a.b 这条模式排除掉，拿到：%q", got)
	}
}

func TestCanonicalizationIgnoresPropertyOrderDeeply(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")

	silent(t, h.run(t, "read", `{"a":1,"nested":{"x":1,"y":2}}`))
	if got := only(t, h.run(t, "read", `{"nested":{"y":2,"x":1},"a":1}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("只是键序不同的两份参数该算同一次调用，拿到：%q", got)
	}
}

func TestMalformedArgumentsFallBackToTheRawString(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")

	// 这一条只能直接叫 Observe：[tools.ExecutionInput.Arguments] 的约定是
	// 「已经解析过、是一段合法 JSON」，所以坏参数在到达执行后瀑布之前就被挡掉了，
	// 走 Execute 根本喂不进来。而这条兜底防的是**别的接线方式**——Observe 是导出的，
	// 谁都能拿一份没经过注册表校验的调用来叫它。
	observe := func(arguments string) bool {
		_, remind := h.reminder.Observe(tools.Execution{ExecutionInput: tools.ExecutionInput{
			CallID:    llm.CallID("c"),
			Name:      "read",
			Arguments: json.RawMessage(arguments),
			Agent:     h.agent,
		}})
		return remind
	}

	// 解不动的参数按原始串比。两次一样的坏串算重复，不一样的不算。
	if observe(`{oops`) {
		t.Fatal("第一次不该有提醒")
	}
	if !observe(`{oops`) {
		t.Fatal("两次同样的坏参数该算重复")
	}
	if observe(`{other`) {
		t.Fatal("换了一份坏参数就是换了一次调用，链该断掉")
	}
}

func TestChainsAreKeyedPerAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	other := scope.NewKey("other")
	arguments := json.RawMessage(`{}`)

	silent(t, h.run(t, "read", `{}`))
	// 另一个 agent 调同一件事：它自己的链才刚开始。
	elsewhere := h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID: llm.CallID("c"), Name: "read", Arguments: arguments, Agent: other,
	})
	silent(t, elsewhere)
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("别的 agent 调了什么不该影响这条链，拿到：%q", got)
	}
}

func TestUserInterjectionResetsTheChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")

	silent(t, h.run(t, "read", `{}`))
	h.reminder.NoticeStep(h.agent, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "换个思路"}}, llm.UserSource{}),
	})
	silent(t, h.run(t, "read", `{}`))
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("插话之后该重新数，拿到：%q", got)
	}
}

func TestNonUserMessagesDoNotResetTheChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")

	silent(t, h.run(t, "read", `{}`))
	// 插件自己注入的上下文不是用户插话，链不该断。
	h.reminder.NoticeStep(h.agent, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "某个插件说的"}}, llm.PluginSource{Plugin: "other"}),
	})
	h.reminder.NoticeStep(nil, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "没有 agent"}}, llm.UserSource{}),
	})
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("非用户来源不该把链断掉，拿到：%q", got)
	}
}

func TestDeniedCallsStillCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	if _, err := h.runtime.Guard(context.Background(), scope.NewRoot(), func(tools.Execution) string {
		return "不许调这个"
	}); err != nil {
		t.Fatalf("装守卫失败：%v", err)
	}

	first := h.run(t, "read", `{}`)
	if !first.IsError {
		t.Fatal("被守卫拒掉的调用该是失败的")
	}
	silent(t, first)
	// 一个反复撞同一道拒绝的模型，正是最值得打断的那种循环。
	if got := only(t, h.run(t, "read", `{}`)); !strings.HasPrefix(got, "You are repeating") {
		t.Fatalf("被拒绝的调用也该计数，拿到：%q", got)
	}
}

func TestDirectExecutesWithoutAnAgentAreIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")

	for i := 0; i < 5; i++ {
		result := h.runtime.Execute(context.Background(), tools.ExecutionInput{
			CallID: llm.CallID("c"), Name: "read", Arguments: json.RawMessage(`{}`),
		})
		if result.IsError {
			t.Fatalf("这次调用不该失败：%+v", result.Error)
		}
		silent(t, result)
	}
}

func TestFoldsOntoADownstreamBlockAndKeepsItsFeedback(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	theirs := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "别人挂的"}}, llm.PluginSource{Plugin: "other"})
	if _, err := h.runtime.PostExecute(context.Background(), scope.NewRoot(),
		func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			return tools.PostDecision{
				Kind:               tools.PostBlock,
				Feedback:           llm.Content{llm.TextBlock{Text: "拦下了"}},
				AdditionalContexts: []llm.Message{theirs},
			}, nil
		}); err != nil {
		t.Fatalf("装执行后规则失败：%v", err)
	}

	h.run(t, "read", `{}`)
	result := h.run(t, "read", `{}`)
	if !result.IsError {
		t.Fatal("被拦下的调用该是失败的")
	}
	if got := textOf(result.Content); got != "拦下了" {
		t.Fatalf("下游那段反馈该原样留着，拿到：%q", got)
	}
	if len(result.AdditionalContexts) != 2 {
		t.Fatalf("该是提醒加别人那条，一共两条，拿到 %d 条", len(result.AdditionalContexts))
	}
	if !strings.HasPrefix(textOf(result.AdditionalContexts[0].Content), "You are repeating") {
		t.Fatal("提醒该插在最前面")
	}
	if textOf(result.AdditionalContexts[1].Content) != "别人挂的" {
		t.Fatal("别人挂的那条该原样留在后面")
	}
}

func TestPreservesADownstreamValueReplacement(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	if _, err := h.runtime.PostExecute(context.Background(), scope.NewRoot(),
		func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			return tools.PostDecision{Kind: tools.PostAccept, Value: json.RawMessage(`"replaced"`)}, nil
		}); err != nil {
		t.Fatalf("装执行后规则失败：%v", err)
	}

	h.run(t, "read", `{}`)
	result := h.run(t, "read", `{}`)
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if string(result.Value) != `"replaced"` {
		t.Fatalf("下游换的值该留着，拿到：%s", result.Value)
	}
	if len(reminders(result)) != 1 {
		t.Fatal("换了值也该照样收到提醒")
	}
}

func TestDownstreamErrorPassesThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	boom := errors.New("下游炸了")
	if _, err := h.runtime.PostExecute(context.Background(), scope.NewRoot(),
		func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			return tools.PostDecision{}, boom
		}); err != nil {
		t.Fatalf("装执行后规则失败：%v", err)
	}

	h.run(t, "read", `{}`)
	// 下游抛错的时候没有裁决可以往上挂，这一层原样把错误传出去。
	result := h.run(t, "read", `{}`)
	if !result.IsError {
		t.Fatal("下游抛错该让这次调用失败")
	}
	silent(t, result)
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	reminder, err := repeattoolreminder.New(repeattoolreminder.Config{})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := reminder.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有注册表就装不上，该报错")
	}
}

func TestConfigValidationFailsLoud(t *testing.T) {
	t.Parallel()
	cases := map[string]repeattoolreminder.Config{
		"阈值给了个空切片":                  {Thresholds: []int{}},
		"阈值小于 2":                    {Thresholds: []int{1}},
		"阈值重复":                      {Thresholds: []int{3, 3}},
		"ArgumentsPreviewChars 是负数": {ArgumentsPreviewChars: -1},
	}
	for label, config := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			_, err := repeattoolreminder.New(config)
			if err == nil {
				t.Fatal("这份配置该被拒绝")
			}
			if !errors.Is(err, repeattoolreminder.ErrInvalidConfig) {
				t.Fatalf("该认得出哨兵：%v", err)
			}
		})
	}
}

// textOf 把一份内容拼成可断言的纯文本。
func textOf(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "\n")
}
