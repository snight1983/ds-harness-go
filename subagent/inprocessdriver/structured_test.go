// 本文件的作用：把那份结构化捕获钉在它真正的判定上——提交跟着**权威结果**走、
// 参数不合法不留痕、捕获之后这次运行就收口、嵌套派发得等外层那次传输、以及那句
// 指令只属于孩子自己那个作用域。

package inprocessdriver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// answerSchema 是这些用例要求孩子交出来的那份结构：恰好一个 answer 字符串。
func answerSchema() tools.Node {
	closed := false
	return tools.Node{
		Type:                 tools.TypeObject,
		Properties:           []tools.Property{{Name: "answer", Schema: tools.Node{Type: tools.TypeString}}},
		Required:             []string{"answer"},
		AdditionalProperties: &closed,
	}
}

// echoDefinition 造一个「把 text 原样吐回来」的普通工具，用来观察那道守卫怎么落在
// 捕获工具**之外**的调用上。
func echoDefinition(name string) *tools.Definition {
	closed := false
	return &tools.Definition{
		Name:        name,
		Description: name + " 回声",
		Parameters: tools.Node{
			Type:                 tools.TypeObject,
			Properties:           []tools.Property{{Name: "text", Schema: tools.Node{Type: tools.TypeString}}},
			Required:             []string{"text"},
			AdditionalProperties: &closed,
		},
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

// structuredFixture 是一次挂好的结构化运行：工具运行时、提示词注册表、孩子那个
// 作用域，和那份捕获句柄。
type structuredFixture struct {
	runtime    *tools.Runtime
	prompt     *systemprompt.Registry
	child      *scope.Scope
	attachment *StructuredAttachment
}

// newStructuredFixture 在一个孩子作用域上挂好那份捕获。
func newStructuredFixture(t *testing.T) *structuredFixture {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompt, err := systemprompt.NewRegistry(t.Context(), rootScope(t), systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	child := keyedScope(t, "child", nil)
	attachment, err := AttachStructuredRuntime(t.Context(), child, StructuredServices{
		Tools:        runtime,
		SystemPrompt: prompt,
	}, answerSchema())
	if err != nil {
		t.Fatalf("挂结构化运行时失败：%v", err)
	}
	return &structuredFixture{runtime: runtime, prompt: prompt, child: child, attachment: attachment}
}

// capture 代表这个孩子调一次捕获工具。parent 非零表示这是一次嵌套派发。
func (f *structuredFixture) capture(
	t *testing.T,
	callID llm.CallID,
	args string,
	parent tools.ExecutionToken,
) tools.Result {
	t.Helper()
	return f.runtime.Execute(t.Context(), tools.ExecutionInput{
		CallID:    callID,
		Name:      StructuredOutputTool,
		Arguments: json.RawMessage(args),
		Agent:     f.child.Key(),
		Parent:    parent,
	})
}

// register 把一个工具登记到孩子那个作用域上。
func (f *structuredFixture) register(t *testing.T, definition *tools.Definition) {
	t.Helper()
	if _, err := f.runtime.Register(t.Context(), f.child, definition); err != nil {
		t.Fatalf("注册 %q 失败：%v", definition.Name, err)
	}
}

// answerOf 从一份捕获里读出那个 answer，读不出来当场失败。
func answerOf(t *testing.T, captured json.RawMessage) string {
	t.Helper()
	var parsed struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(captured, &parsed); err != nil {
		t.Fatalf("读捕获失败：%v", err)
	}
	return parsed.Answer
}

func TestStructuredCaptureCommitsAfterTheAuthoritativeResult(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)

	result := fixture.capture(t, "c1", `{"answer":"42"}`, tools.ExecutionToken{})
	if result.IsError {
		t.Fatalf("一次合法的捕获不该失败：%+v", result.Error)
	}
	if !result.ConcludesTurn {
		t.Fatal("捕获工具该宣布这个回合到此为止")
	}

	captured, ok := fixture.attachment.Captured()
	if !ok {
		t.Fatal("那条权威结果成功之后该提交这次捕获")
	}
	if answer := answerOf(t, captured); answer != "42" {
		t.Fatalf("提交的该是这次调用的参数，实际 %q", answer)
	}
}

func TestStructuredCaptureIgnoresInvalidArguments(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)

	// 缺必填字段：运行时在进执行体**之前**就验掉了，执行体压根没跑过。
	result := fixture.capture(t, "c1", `{}`, tools.ExecutionToken{})
	if !result.IsError {
		t.Fatal("参数对不上 schema 的调用该失败")
	}
	if _, ok := fixture.attachment.Captured(); ok {
		t.Fatal("一次失败的调用不该留下捕获")
	}

	// 而且这次运行没有因此收口——孩子还能再交一份合法的。
	retry := fixture.capture(t, "c2", `{"answer":"42"}`, tools.ExecutionToken{})
	if retry.IsError {
		t.Fatalf("参数失败之后该还能再交一次：%+v", retry.Error)
	}
	captured, ok := fixture.attachment.Captured()
	if !ok || answerOf(t, captured) != "42" {
		t.Fatalf("重试那次该被提交，实际 ok=%v", ok)
	}
}

func TestStructuredGuardClosesTheRunAfterCapture(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)
	fixture.register(t, echoDefinition("echo"))

	if result := fixture.capture(t, "c1", `{"answer":"42"}`, tools.ExecutionToken{}); result.IsError {
		t.Fatalf("第一次捕获不该失败：%+v", result.Error)
	}

	again := fixture.capture(t, "c2", `{"answer":"43"}`, tools.ExecutionToken{})
	if !again.IsError || !strings.Contains(again.Error.Message, "structured output already recorded") {
		t.Fatalf("捕获之后再调捕获工具该被守卫拦下：%+v", again.Error)
	}

	// 守卫拦的是**所有**工具：这次运行已经完了，后来的调用一个都不许跑。
	echoed := fixture.runtime.Execute(t.Context(), tools.ExecutionInput{
		CallID:    "c3",
		Name:      "echo",
		Arguments: json.RawMessage(`{"text":"hi"}`),
		Agent:     fixture.child.Key(),
	})
	if !echoed.IsError || !strings.Contains(echoed.Error.Message, "structured output already recorded") {
		t.Fatalf("捕获之后别的工具也该被拦下：%+v", echoed.Error)
	}

	// 第一次那份捕获纹丝不动。
	captured, ok := fixture.attachment.Captured()
	if !ok || answerOf(t, captured) != "42" {
		t.Fatalf("后来那些被拦下的调用不该改掉已提交的值，实际 ok=%v", ok)
	}
}

func TestStructuredNestedCaptureWaitsForItsOuterTransport(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)

	var inner tools.Result
	var committedInsideOuter bool
	composite := echoDefinition("composite")
	composite.Execute = func(ctx context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		inner = fixture.capture(t, "c2", `{"answer":"42"}`, exec.Token)
		_, committedInsideOuter = fixture.attachment.Captured()
		return json.Marshal("done")
	}
	fixture.register(t, composite)

	outer := fixture.runtime.Execute(t.Context(), tools.ExecutionInput{
		CallID:    "c1",
		Name:      "composite",
		Arguments: json.RawMessage(`{"text":"go"}`),
		Agent:     fixture.child.Key(),
	})
	if inner.IsError {
		t.Fatalf("嵌套的捕获调用不该失败：%+v", inner.Error)
	}
	if committedInsideOuter {
		t.Fatal("外层那次传输还没结清，这份嵌套捕获不该已经提交")
	}
	if outer.IsError {
		t.Fatalf("外层调用不该失败：%+v", outer.Error)
	}

	captured, ok := fixture.attachment.Captured()
	if !ok || answerOf(t, captured) != "42" {
		t.Fatalf("外层成功之后该提交这份嵌套捕获，实际 ok=%v", ok)
	}
}

func TestStructuredNestedCaptureIsDiscardedWhenTheOuterTransportFails(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)

	composite := echoDefinition("composite")
	composite.Execute = func(ctx context.Context, _ json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		if result := fixture.capture(t, "c2", `{"answer":"42"}`, exec.Token); result.IsError {
			t.Errorf("嵌套的捕获调用不该失败：%+v", result.Error)
		}
		return nil, errors.New("外层传输炸了")
	}
	fixture.register(t, composite)

	outer := fixture.runtime.Execute(t.Context(), tools.ExecutionInput{
		CallID:    "c1",
		Name:      "composite",
		Arguments: json.RawMessage(`{"text":"go"}`),
		Agent:     fixture.child.Key(),
	})
	if !outer.IsError {
		t.Fatal("外层调用该失败")
	}
	if _, ok := fixture.attachment.Captured(); ok {
		t.Fatal("外层那次传输失败了，这份嵌套捕获不该算数")
	}
}

func TestStructuredInstructionIsRegisteredOnTheChildScope(t *testing.T) {
	t.Parallel()
	fixture := newStructuredFixture(t)

	assembly, err := fixture.prompt.Assemble(t.Context(), systemprompt.AssembleContext{Scope: fixture.child.Key()})
	if err != nil {
		t.Fatalf("装配孩子的提示词失败：%v", err)
	}
	var instruction string
	for _, section := range assembly.Sections {
		if section.Name == "tool:"+StructuredOutputTool {
			instruction = section.Text
		}
	}
	if instruction != StructuredOutputInstruction {
		t.Fatalf("孩子该看到那句结构化输出指令，实际 %q", instruction)
	}

	// 这笔登记归孩子自己那个作用域：别人的装配里不该有它。
	other := keyedScope(t, "other", nil)
	elsewhere, err := fixture.prompt.Assemble(t.Context(), systemprompt.AssembleContext{Scope: other.Key()})
	if err != nil {
		t.Fatalf("装配另一个作用域的提示词失败：%v", err)
	}
	for _, section := range elsewhere.Sections {
		if section.Name == "tool:"+StructuredOutputTool {
			t.Fatal("那句指令不该漏到别的作用域上")
		}
	}
}

func TestAttachStructuredRuntimeRequiresItsServices(t *testing.T) {
	t.Parallel()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompt, err := systemprompt.NewRegistry(t.Context(), rootScope(t), systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}

	for _, testCase := range []struct {
		name     string
		services StructuredServices
	}{
		{name: "没有工具运行时", services: StructuredServices{SystemPrompt: prompt}},
		{name: "没有提示词注册表", services: StructuredServices{Tools: runtime}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := AttachStructuredRuntime(t.Context(), keyedScope(t, "child", nil), testCase.services, answerSchema())
			if !errors.Is(err, subagent.ErrInvalidRequest) {
				t.Fatalf("装配不成立该报这条接缝的哨兵错误，实际 %v", err)
			}
		})
	}
}
