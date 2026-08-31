// 本文件的作用：把派发管线上那些**只在出岔子的时候才走到**的岔路钉住——
// 登记时给了 nil、瀑布自己抛了错、调用方在半路撤了、投影函数炸了、
// 工具在调用进行到一半被注销了。
//
// # 这些岔路的错法
//
//   - **让一条规则抛出来的错误变成 panic 或者被吞掉**。策略规则是第三方代码，
//     它抛错是一件正常的事；那次调用该失败，但整个 agent 循环不该跟着倒。
//   - **报错了中止的种类**。执行体起没起步决定了这次调用有没有留下副作用：
//     报成 ABORTED_BEFORE_DISPATCH 会让上层放心地重试一次其实已经写过盘的操作。
//   - **在取消面前把一次成功交出去**。调用方已经不要这个结果了，把它当成功提交
//     等于让一次被撤销的操作留在会话记录里。
//   - **让中止把已经发生过的上下文一起抹掉**。一个复合工具已经派发出去的子调用
//     捎回来的话，不会因为外层被取消就变得不曾说过。
//   - **让一次失败的结果还带着值或者回合结束标记**。失败没有权威值可言，
//     而一次失败绝不该有权力结束整个 agent 回合。
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// newAgent 造一个有身份的作用域，失败就让测试挂掉。
func newAgent(t *testing.T, name string) (*scope.Scope, *scope.Key) {
	t.Helper()
	key := scope.NewKey(name)
	agent, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatalf("造 %q 的作用域失败：%v", name, err)
	}
	return agent, key
}

// cancellingApproval 是一个在答复之前先把调用方的 ctx 撤掉的审批接缝。
type cancellingApproval struct {
	cancel  context.CancelFunc
	outcome tools.ApprovalOutcome
}

func (a *cancellingApproval) Request(context.Context, tools.ApprovalRequest) (tools.ApprovalOutcome, error) {
	a.cancel()
	return a.outcome, nil
}

// errorCode 取出一份失败结果的机器可读代号，没有身份就是空串。
func errorCode(result tools.Result) string {
	if result.Error == nil || result.Error.Info == nil {
		return ""
	}
	return result.Error.Info.Code
}

func TestRuleRegistrationRejectsNil(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	background := context.Background()

	if _, err := runtime.PreExecute(background, root, nil); err == nil {
		t.Fatal("nil 的执行前规则该被拒绝")
	}
	if _, err := runtime.AroundDispatch(background, root, nil); err == nil {
		t.Fatal("nil 的绕派发规则该被拒绝")
	}
	if _, err := runtime.PostExecute(background, root, nil); err == nil {
		t.Fatal("nil 的执行后规则该被拒绝")
	}
	if _, err := runtime.ObserveResult(background, root, nil); err == nil {
		t.Fatal("nil 的结果观察者该被拒绝")
	}
	if _, err := runtime.Guard(background, root, nil); err == nil {
		t.Fatal("nil 的守卫该被拒绝")
	}
}

func TestErrorsCarryTheirSentinelAndText(t *testing.T) {
	t.Parallel()
	notFound := &tools.NotFoundError{ToolName: "missing"}
	if !errors.Is(notFound, tools.ErrToolNotFound) {
		t.Fatal("找不到工具的错误该认得出哨兵")
	}
	if notFound.Error() != `unknown tool "missing"` {
		t.Fatalf("措辞不对：%q", notFound.Error())
	}
	// 名字其实是认识的、只是不能这么直接调时，要把该走的那条路一起说出来——
	// 模型看到「不认识」和看到「换条路走」会做出完全不同的下一步。
	routed := &tools.NotFoundError{ToolName: "inner", ReachableFrom: "call it through run_batch"}
	if routed.Error() != `unknown tool "inner": call it through run_batch` {
		t.Fatalf("措辞不对：%q", routed.Error())
	}

	outputErr := &tools.OutputError{ToolName: "echo", Violations: []string{"a", "b"}}
	if !errors.Is(outputErr, tools.ErrInvalidToolOutput) {
		t.Fatal("输出错误该认得出哨兵")
	}
	if outputErr.Error() != `tool "echo" returned invalid output: a; b` {
		t.Fatalf("措辞不对：%q", outputErr.Error())
	}

	argsErr := &tools.ArgsError{Violations: []string{"a", "b"}}
	if !errors.Is(argsErr, tools.ErrInvalidArgs) {
		t.Fatal("参数错误该认得出哨兵")
	}
	if argsErr.Error() != "invalid arguments: a; b" {
		t.Fatalf("措辞不对：%q", argsErr.Error())
	}
}

func TestEveryExecutionGetsItsOwnToken(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))

	var seen []tools.ExecutionToken
	if _, err := runtime.PreExecute(context.Background(), root,
		func(exec tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			seen = append(seen, exec.Token)
			return next()
		}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	runtime.Execute(context.Background(), call("echo", "a", nil))
	runtime.Execute(context.Background(), call("echo", "b", nil))
	if len(seen) != 2 {
		t.Fatalf("该看见两次调用：%d", len(seen))
	}
	if seen[0].IsZero() || seen[1].IsZero() {
		t.Fatal("发出去的 token 不该是零值——零值表示「没有」")
	}
	if seen[0] == seen[1] {
		t.Fatal("两次调用该拿到两个不同的 token")
	}
	if !strings.HasPrefix(seen[0].String(), "tool-exec-") {
		t.Fatalf("token 的写法不便于诊断：%q", seen[0].String())
	}
	// 零值 token 就是「这不是一次嵌套派发」，它得说得出自己是零值。
	var zero tools.ExecutionToken
	if !zero.IsZero() {
		t.Fatal("零值 token 该说自己是零值")
	}
}

func TestCancellationBeforeTheGate(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("该报「派发前就被撤了」：%+v", result.Error)
	}
}

func TestCancellationWhileWaitingForApproval(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	approval := &cancellingApproval{cancel: cancel, outcome: tools.ApprovalCancelled}
	runtime := newRuntime(t, tools.Options{Approval: approval})
	agent, agentKey := newAgent(t, "agent")
	mustRegister(t, runtime, agent, echoTool("echo"))
	if _, err := runtime.PreExecute(context.Background(), agent,
		func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			return tools.PreDecision{Kind: tools.PreAsk, Reason: "要问一下"}, nil
		}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	// 审批那一路是等得起的：人还没答复调用方就撤了，这次调用一件事都没跑过，
	// 所以报「派发前就被撤了」，而不是把「用户拒绝了」这句话记下来。
	result := runtime.Execute(ctx, call("echo", "hi", agentKey))
	if !result.IsError || errorCode(result) != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("该报「派发前就被撤了」：%+v", result.Error)
	}
}

func TestCancellationBetweenTheGateAndDispatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PreExecute(context.Background(), root,
		func(_ tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			decision, err := next()
			cancel()
			return decision, err
		}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("该报「派发前就被撤了」：%+v", result.Error)
	}
}

func TestPreRuleErrorBecomesAFailedResult(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PreExecute(context.Background(), root,
		func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			return tools.PreDecision{}, errors.New("规则自己炸了")
		}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || !strings.Contains(result.Error.Message, "规则自己炸了") {
		t.Fatalf("规则抛的错该变成这次调用的失败：%+v", result.Error)
	}
}

func TestAskWithoutAnAgentHasNowhereToRouteTo(t *testing.T) {
	t.Parallel()
	approval := &approvalStub{outcome: tools.ApprovalAllowedOnce}
	runtime := newRuntime(t, tools.Options{Approval: approval})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PreExecute(context.Background(), root,
		func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			return tools.PreDecision{Kind: tools.PreAsk}, nil
		}); err != nil {
		t.Fatalf("登记执行前规则失败：%v", err)
	}

	// 有审批通道但这次调用没有 agent：问题不在于人怎么答，而在于这句话送不到任何人手上。
	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || !strings.Contains(result.Error.Message, "no agent to route it through") {
		t.Fatalf("该说清楚是没处可问：%+v", result.Error)
	}
	if approval.seen.ToolName != "" {
		t.Fatal("这种情况根本不该去打扰审批通道")
	}
}

func TestDispatchRuleErrorBecomesAFailedResult(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(context.Context, tools.Execution, func(context.Context) (tools.Result, error)) (tools.Result, error) {
			return tools.Result{}, errors.New("包装函数炸了")
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || !strings.Contains(result.Error.Message, "包装函数炸了") {
		t.Fatalf("包装函数抛的错该变成这次调用的失败：%+v", result.Error)
	}
}

func TestCancellationAfterTheBodyFinishedKeepsItsContexts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	definition := echoTool("echo")
	definition.Execute = func(_ context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		exec.DeferContext(note("子调用捎回来的话"))
		return json.RawMessage(`"done"`), nil
	}
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, definition)
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(inner context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
			result, err := next(inner)
			cancel()
			return result, err
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	// 执行体已经跑过了，所以是 ABORTED 而不是 ABORTED_BEFORE_DISPATCH——
	// 这次调用可能已经留下了副作用，上层不能当它没发生过。
	if !result.IsError || errorCode(result) != tools.CodeAborted {
		t.Fatalf("该报「已经起步之后被撤」：%+v", result.Error)
	}
	if len(result.AdditionalContexts) != 1 {
		t.Fatalf("已经说过的话不该因为取消就消失：%v", result.AdditionalContexts)
	}
	if result.Value != nil || result.ConcludesTurn {
		t.Fatalf("失败的结果不该带值或者回合结束标记：%s / %v", result.Value, result.ConcludesTurn)
	}
}

func TestCancellationBeforeTheWrapperCallsThrough(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(inner context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
			cancel()
			return next(inner)
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("执行体一步都没跑，该报「派发前就被撤了」：%+v", result.Error)
	}
}

func TestCancellationWhenTheWrapperSkippedTheBodyEntirely(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(context.Context, tools.Execution, func(context.Context) (tools.Result, error)) (tools.Result, error) {
			// 一个缓存型的包装：不跑执行体，直接把上次的答案交回来。
			cancel()
			return tools.Result{Value: json.RawMessage(`"缓存的"`)}, nil
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	// 值是包装函数直接给的，执行体从没起步，所以仍然是 ABORTED_BEFORE_DISPATCH。
	if !result.IsError || errorCode(result) != tools.CodeAbortedBeforeDispatch {
		t.Fatalf("执行体没起步就该这么报：%+v", result.Error)
	}
}

func TestCancellationDuringPostExecute(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PostExecute(context.Background(), root,
		func(_ tools.Execution, _ tools.Result, next func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			decision, err := next()
			cancel()
			return decision, err
		}); err != nil {
		t.Fatalf("登记执行后规则失败：%v", err)
	}

	result := runtime.Execute(ctx, call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeAborted {
		t.Fatalf("执行体已经跑过了，该报 ABORTED：%+v", result.Error)
	}
}

func TestPostRuleErrorBecomesAFailedResult(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PostExecute(context.Background(), root,
		func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			return tools.PostDecision{}, errors.New("执行后规则炸了")
		}); err != nil {
		t.Fatalf("登记执行后规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || !strings.Contains(result.Error.Message, "执行后规则炸了") {
		t.Fatalf("执行后规则抛的错该变成这次调用的失败：%+v", result.Error)
	}
}

func TestPostRuleContradictionsAreRejected(t *testing.T) {
	t.Parallel()
	t.Run("不能同时换值和换内容", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		mustRegister(t, runtime, root, echoTool("echo"))
		// 内容必须是**那个值**渲染出来的。两样一起换，等于让规则自己声明一份
		// 值和内容对不上的结果，那条不变式就没了。
		registerPost(t, runtime, root, tools.PostDecision{
			Value:   json.RawMessage(`"另一个"`),
			Content: llm.Content{llm.TextBlock{Text: "对不上的内容"}},
		})
		result := runtime.Execute(context.Background(), call("echo", "hi", nil))
		if !result.IsError || !strings.Contains(result.Error.Message, "不能同时替换值和内容") {
			t.Fatalf("该被拒绝：%+v", result.Error)
		}
	})

	t.Run("不能给失败的结果换上一个值", func(t *testing.T) {
		t.Parallel()
		runtime := newRuntime(t, tools.Options{})
		root := scope.NewRoot()
		definition := echoTool("echo")
		definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return nil, errors.New("执行体失败了")
		}
		mustRegister(t, runtime, root, definition)
		registerPost(t, runtime, root, tools.PostDecision{Value: json.RawMessage(`"补一个"`)})
		result := runtime.Execute(context.Background(), call("echo", "hi", nil))
		if !result.IsError || !strings.Contains(result.Error.Message, "不能给失败的结果换上一个值") {
			t.Fatalf("该被拒绝：%+v", result.Error)
		}
	})
}

func TestPostRuleValueReplacementNeedsTheToolStillThere(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	undo := mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.PostExecute(context.Background(), root,
		func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
			// 这次调用跑到一半，别人把这个工具注销了。换值要按**它的**输出契约重验，
			// 契约都没了就只能报失败——绝不能拿另一份定义的契约凑合。
			if err := undo(context.Background()); err != nil {
				return tools.PostDecision{}, err
			}
			return tools.PostDecision{Value: json.RawMessage(`"另一个"`)}, nil
		}); err != nil {
		t.Fatalf("登记执行后规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeUnknownTool {
		t.Fatalf("该报找不到工具：%+v", result.Error)
	}
}

func TestDispatchRuleValueReplacementNeedsTheToolStillThere(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	undo := mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(inner context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
			result, err := next(inner)
			if err != nil {
				return result, err
			}
			if err := undo(context.Background()); err != nil {
				return tools.Result{}, err
			}
			result.Value = json.RawMessage(`"换过的"`)
			return result, nil
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeUnknownTool {
		t.Fatalf("该报找不到工具：%+v", result.Error)
	}
}

func TestDispatchRuleFailureDropsValueAndTurnFlag(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(context.Context, tools.Execution, func(context.Context) (tools.Result, error)) (tools.Result, error) {
			return tools.Result{
				IsError:       true,
				Error:         &tools.Failure{Message: "包装函数判它失败"},
				Value:         json.RawMessage(`"还带着的值"`),
				ConcludesTurn: true,
				Content:       llm.Content{llm.TextBlock{Text: "失败了"}},
			}, nil
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError {
		t.Fatal("这次调用该是失败的")
	}
	// 失败没有权威值可言，而一次失败绝不该有权力结束整个 agent 回合。
	if result.Value != nil || result.ConcludesTurn {
		t.Fatalf("失败的结果不该带值或者回合结束标记：%s / %v", result.Value, result.ConcludesTurn)
	}
}

func TestFailureWithoutDetailsStillGetsAMessage(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	if _, err := runtime.AroundDispatch(context.Background(), root,
		func(context.Context, tools.Execution, func(context.Context) (tools.Result, error)) (tools.Result, error) {
			// 一份只把 IsError 立起来、什么都没填的结果：本包必须自己把细节补齐，
			// 否则下游读 Error.Message 就是解引用一个 nil。
			return tools.Result{IsError: true}, nil
		}); err != nil {
		t.Fatalf("登记绕派发规则失败：%v", err)
	}

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || result.Error == nil || result.Error.Message == "" {
		t.Fatalf("失败的结果必须带上一句话：%+v", result.Error)
	}
}

func TestBlockedFeedbackBecomesTheFailureMessage(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		feedback llm.Content
		wanted   string
	}{
		"多段文本按行拼起来": {
			feedback: llm.Content{llm.TextBlock{Text: "第一句"}, llm.TextBlock{Text: "第二句"}},
			wanted:   "第一句\n第二句",
		},
		"不是文本的块只记它的类别": {
			feedback: llm.Content{llm.ReasoningBlock{Text: "想了想"}},
			wanted:   "[reasoning content]",
		},
		"一段反馈都没有就用兜底那句": {
			feedback: nil,
			wanted:   "tool result blocked by post-execute policy",
		},
		"反馈是一段空文本也用兜底那句": {
			feedback: llm.Content{llm.TextBlock{Text: ""}},
			wanted:   "tool result blocked by post-execute policy",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, tools.Options{})
			root := scope.NewRoot()
			mustRegister(t, runtime, root, echoTool("echo"))
			registerPost(t, runtime, root, tools.PostDecision{
				Kind:     tools.PostBlock,
				Feedback: testCase.feedback,
			})
			result := runtime.Execute(context.Background(), call("echo", "hi", nil))
			if !result.IsError {
				t.Fatal("被拦下的结果该是失败")
			}
			// 模型看到的和日志里记的必须是同一件事，所以失败信息从那段反馈里读，
			// 不另写一句。
			if result.Error.Message != testCase.wanted {
				t.Fatalf("失败信息不对：\n实际 %q\n期望 %q", result.Error.Message, testCase.wanted)
			}
		})
	}
}

func TestNilValueBecomesJSONNull(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Output.Schema = tools.Node{Type: tools.TypeNull}
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		return nil, nil
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	// 「没返回值」和「返回了 JSON null」在 Go 这一侧是同一件事：nil 的 RawMessage。
	// 本包把它规范成 null，好让输出契约有个东西可验。
	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if result.IsError {
		t.Fatalf("不该失败：%+v", result.Error)
	}
	if string(result.Value) != "null" {
		t.Fatalf("值该被规范成 null：%s", result.Value)
	}
}

func TestNonJSONValueIsRejected(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Execute = func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		return json.RawMessage(`{oops`), nil
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeInvalidToolOutput {
		t.Fatalf("该报输出不合法：%+v", result.Error)
	}
	if !strings.Contains(result.Error.Message, "value is not valid JSON") {
		t.Fatalf("该说清楚是哪一种不合法：%q", result.Error.Message)
	}
}

func TestRenderPanicBecomesAnOutputError(t *testing.T) {
	t.Parallel()
	definition := echoTool("echo")
	definition.Output.Render = func(json.RawMessage, json.RawMessage) (llm.Content, error) {
		panic("渲染炸了")
	}
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), definition)

	// 投影函数是工具作者写的第三方代码。它炸了该让这一次调用失败，
	// 而不是把整个进程带走——一个租户的工具不该弄挂所有人的会话。
	result := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if !result.IsError || errorCode(result) != tools.CodeInvalidToolOutput {
		t.Fatalf("该报输出不合法：%+v", result.Error)
	}
	if !strings.Contains(result.Error.Message, "output.render failed: 渲染炸了") {
		t.Fatalf("该说清楚是哪个投影炸了：%q", result.Error.Message)
	}
}

func TestPresentationMetaFailuresAreOutputErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		project func(json.RawMessage, json.RawMessage) (json.RawMessage, error)
		wanted  string
	}{
		"投影自己抛错": {
			project: func(json.RawMessage, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("算不出来")
			},
			wanted: "output.presentationMeta failed: 算不出来",
		},
		"投影 panic": {
			project: func(json.RawMessage, json.RawMessage) (json.RawMessage, error) {
				panic("投影炸了")
			},
			wanted: "output.presentationMeta failed: 投影炸了",
		},
		"投影交出来的不是合法 JSON": {
			project: func(json.RawMessage, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{oops`), nil
			},
			wanted: "output.presentationMeta returned non-lossless JSON",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			definition := echoTool("echo")
			definition.Output.PresentationMeta = testCase.project
			runtime := newRuntime(t, tools.Options{})
			mustRegister(t, runtime, scope.NewRoot(), definition)

			result := runtime.Execute(context.Background(), call("echo", "hi", nil))
			if !result.IsError || errorCode(result) != tools.CodeInvalidToolOutput {
				t.Fatalf("该报输出不合法：%+v", result.Error)
			}
			if !strings.Contains(result.Error.Message, testCase.wanted) {
				t.Fatalf("措辞不对：\n实际 %q\n期望里有 %q", result.Error.Message, testCase.wanted)
			}
		})
	}
}

func TestScopedDuplicateRegistrationSaysWhichScope(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	agent, _ := newAgent(t, "agent")
	mustRegister(t, runtime, agent, echoTool("echo"))

	_, err := runtime.Register(context.Background(), agent, echoTool("echo"))
	if err == nil {
		t.Fatal("同一个作用域里重名该被拒绝")
	}
	// 两句话要分得开：全局重名的出路是「改用 agent 的作用域注册」，
	// 作用域内重名没有那条出路，说反了会把人带沟里。
	if !strings.Contains(err.Error(), "在这个作用域里已经注册过了") {
		t.Fatalf("措辞不对：%v", err)
	}
}

func TestEmptyScopedLayerIsReclaimed(t *testing.T) {
	t.Parallel()
	var changes int
	runtime := newRuntime(t, tools.Options{OnChange: func() { changes++ }})
	agent, agentKey := newAgent(t, "agent")
	undo := mustRegister(t, runtime, agent, echoTool("private"))
	if changes != 1 {
		t.Fatalf("注册该发一次变更通知：%d", changes)
	}
	if _, ok := runtime.Get("private", agentKey); !ok {
		t.Fatal("刚注册的工具该看得见")
	}

	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销注册失败：%v", err)
	}
	if changes != 2 {
		t.Fatalf("撤销也该发一次变更通知：%d", changes)
	}
	// 这一层最后一样东西也没了，它该被回收——留着空层不会改变可见性，
	// 但一个长期运行的进程里 agent 来来去去，空层攒起来就是泄漏。
	if _, ok := runtime.Get("private", agentKey); ok {
		t.Fatal("撤销之后不该还看得见")
	}
	if len(runtime.Schemas(agentKey)) != 0 {
		t.Fatalf("这个 agent 不该还看得见什么：%v", runtime.Schemas(agentKey))
	}
}

func TestRestrictSaysSoWhenThereIsNothingToRestrict(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	agent, _ := newAgent(t, "agent")

	_, err := runtime.Restrict(context.Background(), agent, tools.Restriction{Allow: []string{"echo"}})
	if err == nil {
		t.Fatal("点了不存在的全局工具该被拒绝")
	}
	// 一个全局工具都没有的时候要明说，而不是排出一个空清单让人以为是自己看漏了。
	if !strings.Contains(err.Error(), "（一个也没有）") {
		t.Fatalf("措辞不对：%v", err)
	}
}

func TestGuardsOnlyReachTheirOwnChain(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	root := scope.NewRoot()
	mustRegister(t, runtime, root, echoTool("echo"))
	agent, agentKey := newAgent(t, "agent")
	if _, err := runtime.Guard(context.Background(), agent, func(tools.Execution) string {
		return "这个 agent 不许调"
	}); err != nil {
		t.Fatalf("登记守卫失败：%v", err)
	}

	blocked := runtime.Execute(context.Background(), call("echo", "hi", agentKey))
	if !blocked.IsError || blocked.Error.Message != "这个 agent 不许调" {
		t.Fatalf("这个 agent 该被拦下：%+v", blocked.Error)
	}
	// 没有 agent 的调用走不到那条链上，所以看不见那道守卫。
	free := runtime.Execute(context.Background(), call("echo", "hi", nil))
	if free.IsError {
		t.Fatalf("没有 agent 的调用不该被别人的守卫拦下：%+v", free.Error)
	}
}

func TestCallViewsThatCannotBeEncodedSaySo(t *testing.T) {
	t.Parallel()
	// 卡片里的 rawInput 是工具作者塞进来的原始 JSON，它可能是坏的。
	// 排不出去就得报错，绝不能悄悄发一段坏 JSON 给界面。
	if _, err := json.Marshal(tools.GenericCallView{Title: "坏的", RawInput: raw(`{oops`)}); err == nil {
		t.Fatal("坏的 rawInput 不该排得出去")
	}
}

func TestAskWithoutAnApprovalChannelIsADenial(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reason string
		wanted string
	}{
		// 规则自己说得出理由就用它的：那句话是写给模型看的，比通用文案有信息量。
		"规则给了理由": {reason: "这个工具会写盘", wanted: "这个工具会写盘"},
		// 规则只说「要问」却没说问什么，就得由本包补一句，
		// 否则模型收到的是一句空话，既不知道被拒了也不知道为什么。
		"规则没给理由": {reason: "", wanted: `tool "echo" requires approval (not yet supported)`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// 整个装配里压根没接审批通道：这不是「人说了不行」，是「这里没有能问的人」。
			runtime := newRuntime(t, tools.Options{})
			root := scope.NewRoot()
			mustRegister(t, runtime, root, echoTool("echo"))
			if _, err := runtime.PreExecute(context.Background(), root,
				func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
					return tools.PreDecision{Kind: tools.PreAsk, Reason: testCase.reason}, nil
				}); err != nil {
				t.Fatalf("登记执行前规则失败：%v", err)
			}

			agentKey := scope.NewKey("agent")
			result := runtime.Execute(context.Background(), call("echo", "hi", agentKey))
			if !result.IsError || result.Error.Message != testCase.wanted {
				t.Fatalf("拒绝理由不对：%+v", result.Error)
			}
		})
	}
}

func TestReplacedValuesAreReVerifiedAgainstTheOutputContract(t *testing.T) {
	t.Parallel()
	// echo 声明自己交出来的是字符串。一条规则换上一个数字，就是在违约——
	// 而违约的是**规则**，不是工具的执行体，所以它必须被挡在发给模型之前。
	replacement := json.RawMessage("123")
	cases := map[string]func(*testing.T, *tools.Runtime, *scope.Scope){
		"执行后规则换的值": func(t *testing.T, runtime *tools.Runtime, root *scope.Scope) {
			if _, err := runtime.PostExecute(context.Background(), root,
				func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
					return tools.PostDecision{Value: replacement}, nil
				}); err != nil {
				t.Fatalf("登记执行后规则失败：%v", err)
			}
		},
		"包装函数换的值": func(t *testing.T, runtime *tools.Runtime, root *scope.Scope) {
			if _, err := runtime.AroundDispatch(context.Background(), root,
				func(inner context.Context, _ tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
					result, err := next(inner)
					if err != nil {
						return result, err
					}
					result.Value = replacement
					return result, nil
				}); err != nil {
				t.Fatalf("登记派发包装失败：%v", err)
			}
		},
	}
	for name, install := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, tools.Options{})
			root := scope.NewRoot()
			mustRegister(t, runtime, root, echoTool("echo"))
			install(t, runtime, root)

			result := runtime.Execute(context.Background(), call("echo", "hi", nil))
			if !result.IsError || errorCode(result) != tools.CodeInvalidToolOutput {
				t.Fatalf("换上的值不合契约就该报输出违约：%+v", result.Error)
			}
		})
	}
}

func TestBadDefinitionsAreRejectedAtRegistration(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*tools.Definition){
		// 负预算没有任何一种说得通的读法：它既不是「不设」（那是零），
		// 也不是「立刻超时」。注册就拦下来，别等到调用时才发现。
		"负数超时": func(definition *tools.Definition) { definition.Timeout = -time.Second },
		// 输出契约和参数 schema 一样得先自己立得住：这份契约是每一个成功值的判据，
		// 契约本身不合法的话，头一次调用之前谁也说不清什么样的值才算对。
		"输出 schema 自己就不合法": func(definition *tools.Definition) {
			definition.Output.Schema = tools.Node{Required: []string{"a"}}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runtime := newRuntime(t, tools.Options{})
			definition := echoTool("echo")
			breakIt(definition)
			_, err := runtime.Register(context.Background(), scope.NewRoot(), definition)
			if err == nil || !errors.Is(err, tools.ErrInvalidDefinition) {
				t.Fatalf("这份定义该被拒：%v", err)
			}
		})
	}
}

func TestAncestorLayersContributeInheritedTools(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("global"))

	parentKey := scope.NewKey("parent")
	parent, err := scope.New(parentKey, scope.Options{})
	if err != nil {
		t.Fatalf("造父作用域失败：%v", err)
	}
	childKey := scope.NewKey("child")
	if _, err := scope.New(childKey, scope.Options{Parent: parentKey}); err != nil {
		t.Fatalf("造子作用域失败：%v", err)
	}
	// 注册在**父**那一层：对孩子来说这既不是全局的、也不是它自己的，
	// 而是链上祖先贡献的第三种来源——预设把工具搬到 agent 平面上之后，
	// 子 agent 看得见的东西大多是从这条路来的。
	mustRegister(t, runtime, parent, echoTool("inherited"))

	var names []string
	for _, schema := range runtime.Schemas(childKey) {
		names = append(names, schema.Name)
	}
	if !slices.Equal(names, []string{"global", "inherited"}) {
		t.Fatalf("孩子该继承到祖先那一层的工具：%v", names)
	}
	if _, ok := runtime.Get("inherited", nil); ok {
		t.Fatal("父那一层的工具不该漏进全局视图")
	}
}
