// 本文件的作用：把 tools 包里那些「反过来也说得通、但反过来就错了」的规则钉住。
//
// # 这个包的错法
//
//   - **把限制施加到作用域自己注册的工具上**。一份点名「这个子 agent 能用哪些能力」的
//     过滤器，如果连它自己那层注册的上报工具一起摘掉，子 agent 就没法答话了。
//     DSH 早期正是把这条豁免读成了「全局层豁免」，等工具搬到 agent 平面上就悄悄失效。
//   - **让守卫能放行**。守卫是最后一道闸，只能拒绝；能放行的话注册顺序就成了语义，
//     而且一道安全闸会被后登记的一道悄悄撤掉。
//   - **给模型看的清单和真正能跑的清单分头算**。一旦分开，就会出现「提示词里写着有，
//     调过去说不认识」。两者必须走同一个 viewOf。
//   - **取消时报错了中止的种类**。执行体起没起步决定了这次调用有没有副作用，
//     报反了会让重试逻辑把一次已经落了盘的写操作再来一遍。
//   - **让绕派发的包装函数换掉值却不重新验**。内容必须是**那个值**渲染出来的；
//     跳过重验等于让包装函数绕开工具自己声明的输出契约。
//   - **让一个观察者的 panic 弄失败一次成功的调用**。观察者是旁路。
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// objectSchema 造一个只有一个必填字符串属性的对象根 schema。
func objectSchema(property string) tools.Node {
	return tools.Node{
		Type:       tools.TypeObject,
		Properties: []tools.Property{{Name: property, Schema: tools.Node{Type: tools.TypeString}}},
		Required:   []string{property},
	}
}

// echoTool 造一个把入参里的 text 原样回声出去的工具。
func echoTool(name string) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name + " 回声",
		Parameters:  objectSchema("text"),
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: string(value)}}, nil
			},
		},
		Execute: func(_ context.Context, args json.RawMessage, _ *tools.RunContext) (json.RawMessage, error) {
			var parsed struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &parsed); err != nil {
				return nil, err
			}
			return json.Marshal(parsed.Text)
		},
	}
}

// newRuntime 造一个运行时，登记失败就直接让测试挂掉。
func newRuntime(t *testing.T, options tools.Options) *tools.Runtime {
	t.Helper()
	runtime, err := tools.NewRuntime(options)
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	return runtime
}

// mustRegister 注册一个工具，失败就让测试挂掉。
func mustRegister(t *testing.T, runtime *tools.Runtime, owner *scope.Scope, definition *tools.Definition) func(context.Context) error {
	t.Helper()
	undo, err := runtime.Register(context.Background(), owner, definition)
	if err != nil {
		t.Fatalf("注册 %q 失败：%v", definition.Name, err)
	}
	return undo
}

// call 造一份最小的调用输入。
func call(name, text string, agent *scope.Key) tools.ExecutionInput {
	arguments, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		panic(err)
	}
	return tools.ExecutionInput{CallID: llm.CallID("c1"), Name: name, Arguments: arguments, Agent: agent}
}

// text 把一份内容拼成可断言的纯文本。
func text(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// note 造一条插件来源的上下文消息。
func note(body string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: body}}, llm.PluginSource{Plugin: "test"})
}

func TestRegisterAndExecuteRoundTrip(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if string(result.Value) != `"hi"` {
		t.Fatalf("权威值不对：%s", result.Value)
	}
	if text(result.Content) != `"hi"` {
		t.Fatalf("渲染出来的内容不对：%q", text(result.Content))
	}
}

func TestRegisterRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()
	root := scope.NewRoot()
	cases := map[string]*tools.Definition{
		"nil 定义":      nil,
		"空名字":         {Name: "", Execute: echoTool("x").Execute, Output: echoTool("x").Output, Parameters: objectSchema("text")},
		"没有执行体":       {Name: "x", Output: echoTool("x").Output, Parameters: objectSchema("text")},
		"参数不是对象根":     {Name: "x", Execute: echoTool("x").Execute, Output: echoTool("x").Output, Parameters: tools.Node{Type: tools.TypeString}},
		"没有 Render":   {Name: "x", Execute: echoTool("x").Execute, Parameters: objectSchema("text"), Output: tools.OutputDefinition{Schema: tools.Node{Type: tools.TypeString}}},
		"Timeout 是负数": {Name: "x", Execute: echoTool("x").Execute, Output: echoTool("x").Output, Parameters: objectSchema("text"), Timeout: -1},
	}
	for label, definition := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, tools.Options{})
			if _, err := runtime.Register(context.Background(), root, definition); !errors.Is(err, tools.ErrInvalidDefinition) {
				t.Fatalf("这份定义该被拒：%v", err)
			}
		})
	}
}

func TestScopedRegistrationShadowsGlobalAndStaysPrivate(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	siblingKey := scope.NewKey("sibling")
	private := echoTool("private")
	shadow := echoTool("echo")
	shadow.Description = "被盖住的那一份"
	mustRegister(t, runtime, agent, private)
	mustRegister(t, runtime, agent, shadow)

	if definition, ok := runtime.Get("echo", agentKey); !ok || definition.Description != "被盖住的那一份" {
		t.Fatalf("agent 自己注册的那份该盖住全局的：%+v", definition)
	}
	if definition, ok := runtime.Get("echo", nil); !ok || definition.Description == "被盖住的那一份" {
		t.Fatalf("全局视图不该看见 agent 的覆盖：%+v", definition)
	}
	if _, ok := runtime.Get("private", siblingKey); ok {
		t.Fatal("兄弟 agent 不该看得见另一个 agent 私有的工具")
	}

	// 继承面在前、自己注册的在后，这个顺序会进提示词缓存的键，不能乱。
	var names []string
	for _, schema := range runtime.Schemas(agentKey) {
		names = append(names, schema.Name)
	}
	if !slices.Equal(names, []string{"echo", "private"}) {
		t.Fatalf("可见工具的顺序不对：%v", names)
	}
}

func TestDuplicateRegistrationIsRejected(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.Register(context.Background(), root, echoTool("echo")); err == nil {
		t.Fatal("同名的全局注册该被拒")
	}
}

func TestUnregisterRestoresVisibility(t *testing.T) {
	t.Parallel()
	changes := 0
	runtime := newRuntime(t, tools.Options{OnChange: func() { changes++ }})
	undo := mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销注册失败：%v", err)
	}
	if _, ok := runtime.Get("echo", nil); ok {
		t.Fatal("撤销之后就不该再看得见了")
	}
	if changes != 2 {
		t.Fatalf("注册和撤销各该发一次变更通知，实际 %d 次", changes)
	}
}

func TestGuardRegistrationDoesNotNotifyChange(t *testing.T) {
	t.Parallel()
	changes := 0
	runtime := newRuntime(t, tools.Options{OnChange: func() { changes++ }})
	if _, err := runtime.Guard(context.Background(), scope.NewRoot(), func(tools.Execution) string { return "" }); err != nil {
		t.Fatalf("登记守卫失败：%v", err)
	}
	if changes != 0 {
		t.Fatalf("守卫不改变可见性，不该发变更通知，实际 %d 次", changes)
	}
}

func TestRestrictFiltersInheritedButNotOwnRegistrations(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("shared"))
	mustRegister(t, runtime, scope.NewRoot(), echoTool("dangerous"))

	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	mustRegister(t, runtime, agent, echoTool("report"))
	if _, err := runtime.Restrict(context.Background(), agent, tools.Restriction{Allow: []string{"shared"}}); err != nil {
		t.Fatalf("登记限制失败：%v", err)
	}

	if _, ok := runtime.Get("dangerous", agentKey); ok {
		t.Fatal("allow 之外的继承工具该被摘掉")
	}
	if _, ok := runtime.Get("shared", agentKey); !ok {
		t.Fatal("allow 点名的继承工具该留下")
	}
	// 这条是关键：过滤器只管继承面，不许把这个作用域自己注册的上报工具一起摘掉。
	if _, ok := runtime.Get("report", agentKey); !ok {
		t.Fatal("作用域自己注册的工具不受限制影响")
	}
	// 被摘掉的名字仍然是「认识的名字」，提示词里点它的名不算写错。
	if !slices.Contains(runtime.KnownNames(agentKey), "dangerous") {
		t.Fatalf("KnownNames 该保留被摘掉的名字：%v", runtime.KnownNames(agentKey))
	}
}

func TestRestrictIntersectsAlongTheChain(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	for _, name := range []string{"a", "b", "c"} {
		mustRegister(t, runtime, root, echoTool(name))
	}
	presetKey := scope.NewKey("preset")
	preset, err := scope.New(presetKey, scope.Options{})
	if err != nil {
		t.Fatalf("造预设作用域失败：%v", err)
	}
	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{Parent: presetKey})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	if _, err := runtime.Restrict(context.Background(), preset, tools.Restriction{Allow: []string{"a", "b"}}); err != nil {
		t.Fatalf("预设限制失败：%v", err)
	}
	if _, err := runtime.Restrict(context.Background(), agent, tools.Restriction{Deny: []string{"b"}}); err != nil {
		t.Fatalf("agent 限制失败：%v", err)
	}

	var names []string
	for _, schema := range runtime.Schemas(agentKey) {
		names = append(names, schema.Name)
	}
	if !slices.Equal(names, []string{"a"}) {
		t.Fatalf("整条链上的限制该取交集：%v", names)
	}
}

func TestRestrictRejectsBadUsage(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}

	if _, err := runtime.Restrict(context.Background(), scope.NewRoot(), tools.Restriction{Deny: []string{"echo"}}); !errors.Is(err, tools.ErrInvalidRestriction) {
		t.Fatal("全局限制该被拒——那件事该由「不给它注册」来表达")
	}
	if _, err := runtime.Restrict(context.Background(), agent, tools.Restriction{}); !errors.Is(err, tools.ErrInvalidRestriction) {
		t.Fatal("空过滤器该被拒")
	}
	if _, err := runtime.Restrict(context.Background(), agent, tools.Restriction{Deny: []string{"typo"}}); !errors.Is(err, tools.ErrInvalidRestriction) {
		t.Fatal("点到不存在的工具该被拒")
	}
}

func TestUnknownToolReportsItsCode(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	result := runtime.Execute(context.Background(), call("missing", "hi", nil))
	if !result.IsError || result.Error.Info == nil || result.Error.Info.Code != tools.CodeUnknownTool {
		t.Fatalf("该是一次 UNKNOWN_TOOL：%+v", result.Error)
	}
}

func TestInvalidArgumentsAreRejectedBeforeTheBody(t *testing.T) {
	t.Parallel()
	invoked := false
	definition := echoTool("echo")
	inner := definition.Execute
	definition.Execute = func(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		invoked = true
		return inner(ctx, args, exec)
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	input := call("echo", "hi", nil)
	input.Arguments = json.RawMessage(`{"text":42}`)
	result := runtime.Execute(context.Background(), input)
	if !result.IsError || result.Error.Info == nil || result.Error.Info.Code != tools.CodeInvalidArgs {
		t.Fatalf("该是一次 INVALID_ARGS：%+v", result.Error)
	}
	if invoked {
		t.Fatal("参数不合法时执行体不该被调起来")
	}
}

func TestMalformedArgumentsFailBeforeThePolicyPipeline(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	seen := false
	if _, err := runtime.PreExecute(context.Background(), scope.NewRoot(), func(_ tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
		seen = true
		return next()
	}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	input := call("echo", "hi", nil)
	input.Arguments = json.RawMessage(`{`)
	result := runtime.Execute(context.Background(), input)
	if !result.IsError || result.Error.Info.Code != tools.CodeInvalidArgs {
		t.Fatalf("该是一次 INVALID_ARGS：%+v", result.Error)
	}
	if seen {
		t.Fatal("连参数都读不成 JSON 的调用不该进策略管线")
	}
}

func TestInvalidOutputIsRejected(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		return json.RawMessage(`42`), nil
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error.Info.Code != tools.CodeInvalidToolOutput {
		t.Fatalf("该是一次 INVALID_TOOL_OUTPUT：%+v", result.Error)
	}
}

func TestToolPanicBecomesAFailedResult(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		panic("下标越界")
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || !strings.Contains(result.Error.Message, "下标越界") {
		t.Fatalf("执行体的 panic 该变成这一次调用的失败：%+v", result.Error)
	}
}

func TestRenderFailureBecomesAnOutputError(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Output.Render = func(json.RawMessage, json.RawMessage) (llm.Content, error) {
		return nil, errors.New("渲染炸了")
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error.Info.Code != tools.CodeInvalidToolOutput {
		t.Fatalf("投影失败该算这个工具的输出违规：%+v", result.Error)
	}
}

func TestPresentationMetaOnlyForTopLevelCalls(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Output.PresentationMeta = func(json.RawMessage, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"card":"echo"}`), nil
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	top := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if string(top.Meta) != `{"card":"echo"}` {
		t.Fatalf("顶层调用该算出呈现载荷：%s", top.Meta)
	}

	// 嵌在复合工具下面的调用没有自己的卡片，算出来的东西无处可显示。
	var nested tools.Result
	composite := echoTool("composite")
	composite.Execute = func(ctx context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		inner := call("echo", "hi", nil)
		inner.CallID = "c2"
		inner.Parent = exec.Token
		nested = runtime.Execute(ctx, inner)
		return json.Marshal("done")
	}
	mustRegister(t, runtime, scope.NewRoot(), composite)
	if result := runtime.Execute(context.Background(), call("composite", "go", nil)); result.IsError {
		t.Fatalf("复合调用不该失败：%+v", result.Error)
	}
	if nested.Meta != nil {
		t.Fatalf("嵌套调用不该算呈现载荷：%s", nested.Meta)
	}
}

func TestGuardsCanOnlyDeny(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.Guard(context.Background(), root, func(tools.Execution) string { return "策略不允许" }); err != nil {
		t.Fatalf("登记守卫失败：%v", err)
	}
	// 后登记的守卫「不表态」，它没有任何办法把前一道的拒绝改回允许。
	if _, err := runtime.Guard(context.Background(), root, func(tools.Execution) string { return "" }); err != nil {
		t.Fatalf("登记守卫失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error.Message != "策略不允许" {
		t.Fatalf("该被守卫拦下：%+v", result.Error)
	}
	if result.Error.Info != nil {
		t.Fatal("守卫的拒绝理由不是一个有身份的错误类，不该带 Info")
	}
}

func TestPreRuleCanDenyAndShortCircuit(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	inner := false
	if _, err := runtime.PreExecute(context.Background(), root, func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
		return tools.PreDecision{Kind: tools.PreDeny, Reason: "先登记的说了算"}, nil
	}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}
	if _, err := runtime.PreExecute(context.Background(), root, func(_ tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
		inner = true
		return next()
	}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error.Message != "先登记的说了算" {
		t.Fatalf("该被执行前规则拒掉：%+v", result.Error)
	}
	if inner {
		t.Fatal("不调 next 就该短路，后面登记的规则一条都不该跑")
	}
}

// approvalStub 是一个说好了答什么就答什么的审批接缝。
type approvalStub struct {
	outcome tools.ApprovalOutcome
	err     error
	seen    tools.ApprovalRequest
}

func (a *approvalStub) Request(_ context.Context, request tools.ApprovalRequest) (tools.ApprovalOutcome, error) {
	a.seen = request
	return a.outcome, a.err
}

func TestAskResolvesThroughTheApprovalSeam(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label   string
		outcome tools.ApprovalOutcome
		err     error
		allowed bool
		reason  string
	}{
		{label: "这一次允许", outcome: tools.ApprovalAllowedOnce, allowed: true},
		{label: "用户说不行", outcome: tools.ApprovalRejected, reason: `the user rejected tool "echo"`},
		{label: "询问被取消", outcome: tools.ApprovalCancelled, reason: `approval for tool "echo" was cancelled`},
		{label: "没有能问的人", outcome: tools.ApprovalUnavailable, reason: `tool "echo" requires approval, but no approval channel is available`},
		{label: "接缝自己报错", outcome: tools.ApprovalAllowedOnce, err: errors.New("断线"), reason: `tool "echo" requires approval, but no approval channel is available`},
	}
	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			t.Parallel()
			approval := &approvalStub{outcome: testCase.outcome, err: testCase.err}
			runtime := newRuntime(t, tools.Options{Approval: approval})
			agentKey := scope.NewKey("agent")
			root := scope.NewRoot()
			mustRegister(t, runtime, root, echoTool("echo"))
			if _, err := runtime.PreExecute(context.Background(), root, func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
				return tools.PreDecision{Kind: tools.PreAsk, Reason: "这个工具会写盘"}, nil
			}); err != nil {
				t.Fatalf("登记执行前规则失败：%v", err)
			}

			result := runtime.Execute(context.Background(), call("echo", "hi", agentKey))
			if testCase.allowed {
				if result.IsError {
					t.Fatalf("拿到「这一次允许」就该跑：%+v", result.Error)
				}
			} else if result.Error.Message != testCase.reason {
				t.Fatalf("拒绝理由不对：%q", result.Error.Message)
			}
			if approval.seen.ToolName != "echo" || approval.seen.Reason != "这个工具会写盘" || approval.seen.Agent != agentKey {
				t.Fatalf("送到审批面前的请求不对：%+v", approval.seen)
			}
		})
	}
}

func TestAskDegradesToDenyWithoutAnApprovalChannel(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		approval tools.Approval
		agent    *scope.Key
		reason   string
	}{
		"没装审批接缝": {
			agent:  scope.NewKey("agent"),
			reason: "这个工具会写盘",
		},
		"调用没有 agent 可路由": {
			approval: &approvalStub{outcome: tools.ApprovalAllowedOnce},
			reason:   `tool "echo" requires approval, but the call has no agent to route it through`,
		},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, tools.Options{Approval: testCase.approval})
			root := scope.NewRoot()
			mustRegister(t, runtime, root, echoTool("echo"))
			if _, err := runtime.PreExecute(context.Background(), root, func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
				return tools.PreDecision{Kind: tools.PreAsk, Reason: "这个工具会写盘"}, nil
			}); err != nil {
				t.Fatalf("登记执行前规则失败：%v", err)
			}
			result := runtime.Execute(context.Background(), call("echo", "hi", testCase.agent))
			if !result.IsError || result.Error.Message != testCase.reason {
				t.Fatalf("该降级成拒绝：%+v", result.Error)
			}
		})
	}
}

func TestPostRuleAcceptsReplacesAndBlocks(t *testing.T) {
	t.Parallel()
	t.Run("换内容", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		mustRegister(t, runtime, root, echoTool("echo"))
		registerPost(t, runtime, root, tools.PostDecision{Content: llm.Content{llm.TextBlock{Text: "换过的"}}})
		result := runtime.Execute(context.Background(), call("echo", "hi", nil))
		if text(result.Content) != "换过的" || string(result.Value) != `"hi"` {
			t.Fatalf("只该换内容，值不动：%q / %s", text(result.Content), result.Value)
		}
	})
	t.Run("换值会重新渲染", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		mustRegister(t, runtime, root, echoTool("echo"))
		registerPost(t, runtime, root, tools.PostDecision{Value: json.RawMessage(`"另一个"`)})
		result := runtime.Execute(context.Background(), call("echo", "hi", nil))
		if string(result.Value) != `"另一个"` || text(result.Content) != `"另一个"` {
			t.Fatalf("换值之后内容该是新值渲染出来的：%s / %q", result.Value, text(result.Content))
		}
	})
	t.Run("两个都换是编程错误", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		mustRegister(t, runtime, root, echoTool("echo"))
		registerPost(t, runtime, root, tools.PostDecision{
			Content: llm.Content{llm.TextBlock{Text: "x"}},
			Value:   json.RawMessage(`"y"`),
		})
		if result := runtime.Execute(context.Background(), call("echo", "hi", nil)); !result.IsError {
			t.Fatal("同时换值和换内容该失败")
		}
	})
	t.Run("拦下来", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		mustRegister(t, runtime, root, echoTool("echo"))
		registerPost(t, runtime, root, tools.PostDecision{
			Kind:     tools.PostBlock,
			Feedback: llm.Content{llm.TextBlock{Text: "路径不在允许的范围里"}},
		})
		result := runtime.Execute(context.Background(), call("echo", "hi", nil))
		if !result.IsError || result.Error.Message != "路径不在允许的范围里" {
			t.Fatalf("失败信息该从那段反馈里读出来：%+v", result.Error)
		}
		if result.Value != nil {
			t.Fatal("失败的结果不许带着值")
		}
	})
}

// registerPost 登记一条一律给出同一个裁决的执行后规则。
func registerPost(t *testing.T, runtime *tools.Runtime, owner *scope.Scope, decision tools.PostDecision) {
	t.Helper()
	if _, err := runtime.PostExecute(context.Background(), owner, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return decision, nil
	}); err != nil {
		t.Fatalf("登记执行后规则失败：%v", err)
	}
}

func TestDeferredContextsComeBeforePolicyContexts(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(_ context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		exec.DeferContext(note("工具说的"))
		return json.Marshal("hi")
	}
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, definition)
	registerPost(t, runtime, root, tools.PostDecision{AdditionalContexts: []llm.Message{note("策略说的")}})

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if len(result.AdditionalContexts) != 2 {
		t.Fatalf("两条上下文都该在：%d", len(result.AdditionalContexts))
	}
	if text(result.AdditionalContexts[0].Content) != "工具说的" {
		t.Fatalf("工具推迟的那条该在前面：%q", text(result.AdditionalContexts[0].Content))
	}
}

func TestBlockDiscardsToolDeferredContexts(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(_ context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		exec.DeferContext(note("工具说的"))
		return json.Marshal("hi")
	}
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, definition)
	registerPost(t, runtime, root, tools.PostDecision{
		Kind:               tools.PostBlock,
		Feedback:           llm.Content{llm.TextBlock{Text: "不行"}},
		AdditionalContexts: []llm.Message{note("策略说的")},
	})

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if len(result.AdditionalContexts) != 1 || text(result.AdditionalContexts[0].Content) != "策略说的" {
		t.Fatalf("被拦下的调用只该留下拦它的那一方显式给的上下文：%+v", result.AdditionalContexts)
	}
}

func TestConcludeTurnRidesOnlySuccess(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(_ context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		exec.ConcludeTurn()
		return json.Marshal("hi")
	}
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, definition)

	if result := runtime.Execute(context.Background(), call("echo", "hi", nil)); !result.ConcludesTurn {
		t.Fatal("成功的结果该带上回合结束的标记")
	}

	registerPost(t, runtime, root, tools.PostDecision{
		Kind:     tools.PostBlock,
		Feedback: llm.Content{llm.TextBlock{Text: "不行"}},
	})
	if result := runtime.Execute(context.Background(), call("echo", "hi", nil)); result.ConcludesTurn {
		t.Fatal("失败的结果不许带回合结束的标记")
	}
}

func TestCancellationBeforeDispatch(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || result.Error.Info.Code != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("执行体还没起步就取消，该报 ABORTED_BEFORE_DISPATCH：%+v", result.Error)
	}
}

func TestCancellationAfterTheBodyStarted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	definition := echoTool("echo")
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		// 活儿已经干了一半：取消到达时它自己收敛，结果仍然要按「已经起步」报。
		cancel()
		return json.Marshal("hi")
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || result.Error.Info.Code != tools.CodeAborted {
		t.Fatalf("执行体起过步，该报 ABORTED：%+v", result.Error)
	}
}

func TestCancellationDoesNotSupersedeAFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	definition := echoTool("echo")
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		cancel()
		return nil, errors.New("工具自己失败了")
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if result.Error.Message != "工具自己失败了" {
		t.Fatalf("取消只盖成功的结果，工具自己的结构化失败要留住：%+v", result.Error)
	}
}

func TestDispatchRuleValueReplacementIsRevalidated(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root, func(ctx context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
		result, err := next(ctx)
		if err != nil {
			return result, err
		}
		result.Value = json.RawMessage(`123`)
		return result, nil
	}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error.Info.Code != tools.CodeInvalidToolOutput {
		t.Fatalf("包装函数换了值就要按输出契约重新验：%+v", result.Error)
	}
}

func TestDispatchRuleContentEditSurvives(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root, func(ctx context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
		result, err := next(ctx)
		if err != nil {
			return result, err
		}
		result.Content = llm.Content{llm.TextBlock{Text: "包装函数改过的"}}
		return result, nil
	}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if text(result.Content) != "包装函数改过的" {
		t.Fatalf("值没被动过，内容的改写该保留：%q", text(result.Content))
	}
}

func TestDispatchRulesRunOutermostFirst(t *testing.T) {
	t.Parallel()
	var order []string
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	for _, label := range []string{"先登记的", "后登记的"} {
		if _, err := runtime.AroundDispatch(context.Background(), root, func(ctx context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
			order = append(order, label)
			return next(ctx)
		}); err != nil {
			t.Fatalf("登记绕派发规则失败：%v", err)
		}
	}

	runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !slices.Equal(order, []string{"先登记的", "后登记的"}) {
		t.Fatalf("先登记的该在外层：%v", order)
	}
}

func TestFinalizeContentIsSnapshottedAtCallStart(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	definition := echoTool("echo")
	definition.FinalizeContent = func(_ tools.Execution, _ tools.Result) llm.Content {
		return llm.Content{llm.TextBlock{Text: "收尾改过的"}}
	}
	var undo func(context.Context) error
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		// 跑到一半这个工具被注销了：收尾仍然该用调用开始时快照下来的那一份。
		if err := undo(context.Background()); err != nil {
			return nil, err
		}
		return json.Marshal("hi")
	}
	undo = mustRegister(t, runtime, root, definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if text(result.Content) != "收尾改过的" {
		t.Fatalf("该用快照下来的那个收尾函数：%q", text(result.Content))
	}
}

func TestFinalizeContentPanicKeepsTheOriginalContent(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.FinalizeContent = func(tools.Execution, tools.Result) llm.Content { panic("收尾炸了") }
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if result.IsError || text(result.Content) != `"hi"` {
		t.Fatalf("收尾 panic 该保留原内容：%+v", result)
	}
}

func TestObserversGetPrivateCopiesAndTheirPanicsAreContained(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.ObserveResult(context.Background(), root, func(_ tools.Execution, result tools.Result) {
		result.Content[0] = llm.TextBlock{Text: "被观察者改过了"}
		panic("观察者炸了")
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	var seen string
	if _, err := runtime.ObserveResult(context.Background(), root, func(_ tools.Execution, result tools.Result) {
		seen = text(result.Content)
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if result.IsError {
		t.Fatalf("观察者 panic 不该弄失败一次成功的调用：%+v", result.Error)
	}
	if seen != `"hi"` {
		t.Fatalf("每个观察者拿到的都该是自己那一份副本：%q", seen)
	}
	if text(result.Content) != `"hi"` {
		t.Fatalf("调用方拿到的结果不该被观察者改动：%q", text(result.Content))
	}
}

func TestScopedRulesOnlySeeTheirOwnAgent(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	agentKey := scope.NewKey("agent")
	agent, err := scope.New(agentKey, scope.Options{})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	if _, err := runtime.PreExecute(context.Background(), agent, func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
		return tools.PreDecision{Kind: tools.PreDeny, Reason: "只管这个 agent"}, nil
	}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	if result := runtime.Execute(context.Background(), call("echo", "hi", agentKey)); !result.IsError {
		t.Fatal("挂在这个 agent 上的规则该管到它自己的调用")
	}
	if result := runtime.Execute(context.Background(), call("echo", "hi", scope.NewKey("other"))); result.IsError {
		t.Fatalf("别的 agent 的调用不该被它看见：%+v", result.Error)
	}
}

func TestExecutionModeFailsClosed(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()

	safe := echoTool("safe")
	safe.IsConcurrencySafe = func(json.RawMessage) bool { return true }
	unsafe := echoTool("unsafe")
	unsafe.IsConcurrencySafe = func(json.RawMessage) bool { return false }
	exploding := echoTool("exploding")
	exploding.IsConcurrencySafe = func(json.RawMessage) bool { panic("判定炸了") }
	for _, definition := range []*tools.Definition{safe, unsafe, exploding, echoTool("undeclared")} {
		mustRegister(t, runtime, root, definition)
	}

	cases := map[string]tools.ExecutionModeKind{
		"safe":       tools.ModeParallel,
		"unsafe":     tools.ModeExclusive,
		"exploding":  tools.ModeExclusive,
		"undeclared": tools.ModeExclusive,
		"missing":    tools.ModeExclusive,
	}
	for name, want := range cases {
		if got := runtime.ExecutionMode(call(name, "hi", nil)); got != want {
			t.Fatalf("%s 的调度方式该是 %s，实际 %s", name, want, got)
		}
	}
}

func TestSchemasHideExecutionAndPresentationDetails(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	definition := echoTool("echo")
	definition.Timeout = 1
	mustRegister(t, runtime, scope.NewRoot(), definition)

	schemas := runtime.Schemas(nil)
	if len(schemas) != 1 {
		t.Fatalf("该只有一个 schema：%d", len(schemas))
	}
	if !json.Valid(schemas[0].Parameters) || !strings.Contains(string(schemas[0].Parameters), `"text"`) {
		t.Fatalf("参数 schema 排得不对：%s", schemas[0].Parameters)
	}
	if strings.Contains(string(schemas[0].Parameters), "imeout") {
		t.Fatal("超时预算永远不该发给模型")
	}
}

func TestStagedSchedulerMatchesExecute(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))
	ctx := context.Background()

	prepared := runtime.Prepare(ctx, call("echo", "hi", nil))
	if prepared.Kind != tools.StageDispatch {
		t.Fatalf("这次调用该过闸去派发：%s", prepared.Kind)
	}
	dispatched := runtime.Dispatch(ctx, prepared.Exec)
	if dispatched.Kind != tools.StagePostResult {
		t.Fatalf("派发完该还要过执行后瀑布：%s", dispatched.Kind)
	}
	result := runtime.Finalize(ctx, prepared.Exec, dispatched.Result)
	if result.IsError || string(result.Value) != `"hi"` {
		t.Fatalf("分段跑出来的结果该和一口气跑一样：%+v", result)
	}
}

func TestRootCallIDDefaultsToTheCallID(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	var seen tools.Execution
	definition := echoTool("echo")
	definition.Execute = func(_ context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		seen = exec.Execution
		return json.Marshal("hi")
	}
	mustRegister(t, runtime, scope.NewRoot(), definition)

	runtime.Execute(context.Background(), call("echo", "hi", nil))
	if seen.RootCallID != seen.CallID {
		t.Fatalf("根调用的 RootCallID 该补成 CallID：%q / %q", seen.RootCallID, seen.CallID)
	}
	if seen.Token.IsZero() {
		t.Fatal("本包该给每次调用发一个 token")
	}
}
