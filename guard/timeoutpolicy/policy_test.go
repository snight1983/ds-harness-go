// 本文件的作用：把这一层的全部可观察行为钉住——什么时候放行、什么时候换结果、
// 以及「是我超时了」和「是上游撤了」这两件事怎么分开。
//
// 逐条对着 DSH 的 tests/timeout-policy.spec.ts 走，只有一条在 Go 这边**不存在**：
// 那边有一条「执行后瀑布要看得见调用方原来那条信号」，因为 DSH 把信号写在 exec
// 这个对象的字段上，包完必须还原回去。Go 这边取消是**参数**：派生出来的 ctx 只递给
// next，函数一返回就没人拿得到它了，而 [tools.PostRule] 的签名里压根没有 ctx。
// 没有要还原的东西，也就没有能失败的还原——写一条这样的测试是在测语言，不是测代码。
//
// # 这些测试防的是什么错
//
//   - **把上游取消读成自己超时**。用户按了停止，模型却收到一份「超时了」的结果，
//     于是它体贴地换个更小的输入重试一遍——用户明确要停的那件事又跑了一次。
//   - **反过来，把自己的超时读成上游取消**。超时是可重试的，取消不是；报错了
//     模型就不会重试一次本该重试的调用。
//   - **给没声明预算的工具偷偷装一条期限**。那等于替工具作者做了一个他没做过的决定。
//   - **抢在执行体前面返回**。这一层停不下任何东西；结果换了但活儿还在跑的话，
//     调用方以为这次调用结束了，实际上它还占着资源。
//
// # 计时怎么做到不靠运气
//
// 不用假时钟。工具写成**协作式**的：它一直等到 ctx 结束才返回。于是「期限先到」
// 这件事不是靠 sleep 赌出来的，而是由工具本身的形状保证的——期限没到，工具就不返回。
package timeoutpolicy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/guard/timeoutpolicy"
	"ds-harness-go/llm"
)

// budget 是协作式工具用的那个短预算。
//
// 短是为了让测试跑得快，但**测试的判定和它的长短无关**：工具要等到 ctx 结束才返回，
// 所以期限赢不赢是结构决定的，不是时长决定的。挑一个整毫秒数是因为它会出现在
// 诊断文本里（`timed out after 20ms`），断言得写得出来。
const budget = 20 * time.Millisecond

// executor 是一份工具定义的执行体。
type executor func(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error)

// tool 造一份带预算的工具定义。timeout 为 0 表示这个工具没声明预算。
func tool(name string, timeout time.Duration, run executor) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Timeout:     timeout,
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{Text: text}}, nil
			},
		},
		Execute: run,
	}
}

// cooperative 是一个只在 ctx 结束之后才收敛的执行体，模拟一个真的转发了取消的工具。
//
// entered 非 nil 时，它在进门那一刻关掉——测试靠这个知道「执行体已经跑起来了」，
// 从而可以在一个确定的时刻去撤调用方的 ctx。
func cooperative(entered chan<- struct{}) executor {
	return func(ctx context.Context, _ json.RawMessage, _ *tools.RunContext) (json.RawMessage, error) {
		if entered != nil {
			close(entered)
		}
		<-ctx.Done()
		return json.Marshal("stopped cooperatively")
	}
}

// callerMark 是插在调用方 ctx 上的一枚记号，用来验证派生出来的那条确实是它的子孙。
//
// 空结构体做键是 context 的惯例：它保证不会和别的包挂的键撞上。
type callerMark struct{}

// captureCtx 是一个把自己拿到的 ctx 存下来、然后正常返回的执行体。
//
// 存下来的是 ctx 本身而不是它的某个属性：这一层「碰没碰调用方那条 ctx」是
// **身份**问题，DSH 那边同样是拿 `toBe(upstream)` 断言的。[timeout.Deadline]
// 在没有预算时原样返回 parent，所以身份比较在两边给出同一个答案。
func captureCtx(into *context.Context) executor {
	return func(ctx context.Context, _ json.RawMessage, _ *tools.RunContext) (json.RawMessage, error) {
		*into = ctx
		return json.Marshal("ok")
	}
}

// newRuntime 造一个空运行时，失败就让测试挂掉。
func newRuntime(t *testing.T) *tools.Runtime {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	return runtime
}

// install 把这一层装到全局作用域上，返回撤销它的函数。
func install(t *testing.T, runtime *tools.Runtime) func(context.Context) error {
	t.Helper()
	undo, err := timeoutpolicy.Install(context.Background(), runtime, scope.NewRoot())
	if err != nil {
		t.Fatalf("装超时规则失败：%v", err)
	}
	return undo
}

// register 注册一份定义，失败就让测试挂掉。
func register(t *testing.T, runtime *tools.Runtime, definition *tools.Definition) {
	t.Helper()
	if _, err := runtime.Register(context.Background(), scope.NewRoot(), definition); err != nil {
		t.Fatalf("注册 %q 失败：%v", definition.Name, err)
	}
}

// call 造一份最小的调用输入。
func call(name string) tools.ExecutionInput {
	return tools.ExecutionInput{CallID: llm.CallID("c1"), Name: name, Arguments: json.RawMessage(`{}`)}
}

// errorCode 取出一份失败结果的机器可读代号，没有身份就是空串。
func errorCode(result tools.Result) string {
	if result.Error == nil || result.Error.Info == nil {
		return ""
	}
	return result.Error.Info.Code
}

// text 把一份内容拼成可断言的纯文本。
func text(content llm.Content) string {
	if len(content) != 1 {
		return ""
	}
	block, ok := content[0].(llm.TextBlock)
	if !ok {
		return ""
	}
	return block.Text
}

// assertTimedOut 断言一份结果就是这一层换上去的那一份，三个字段一个都不能少。
//
// 三个字段并排断言是有意的：下游按 code 路由、人按 message 读、模型只看得见 content，
// 少断言任何一个，都可能让一次「code 对了但模型看到一片空白」的回归溜过去。
func assertTimedOut(t *testing.T, result tools.Result) {
	t.Helper()
	const message = "tool call timed out after 20ms"
	if !result.IsError {
		t.Fatalf("这次调用该是失败的：%+v", result)
	}
	if result.Error == nil || result.Error.Message != message {
		t.Fatalf("失败说明不对：%+v", result.Error)
	}
	if result.Error.Info == nil || result.Error.Info.Name != "ToolTimeoutError" || result.Error.Info.Code != timeoutpolicy.Code {
		t.Fatalf("失败身份不对：%+v", result.Error.Info)
	}
	if got := text(result.Content); got != "Error: "+message {
		t.Fatalf("给模型看的内容不对：%q", got)
	}
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	if _, err := timeoutpolicy.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有注册表就装不上，该报错")
	}
}

func TestCodeIsTheOwnedConstant(t *testing.T) {
	t.Parallel()
	if timeoutpolicy.Code != "TOOL_TIMEOUT" {
		t.Fatalf("代号变了：%q", timeoutpolicy.Code)
	}
}

func TestNoBudgetDelegatesWithoutADeadline(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	var seen context.Context
	register(t, runtime, tool("probe", 0, captureCtx(&seen)))

	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := runtime.Execute(caller, call("probe"))
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if seen != caller {
		t.Fatal("没声明预算的工具该原样拿到调用方那条 ctx")
	}
}

func TestUnknownToolPassesThrough(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)

	result := runtime.Execute(context.Background(), call("missing"))
	if !result.IsError {
		t.Fatal("调一个不存在的工具该失败")
	}
	if code := errorCode(result); code == timeoutpolicy.Code {
		t.Fatal("查不到定义的时候不该现编一个超时，该让「找不到这个工具」原样报出来")
	}
}

func TestBudgetedToolReceivesADeadline(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	var seen context.Context
	register(t, runtime, tool("probe", 10*time.Second, captureCtx(&seen)))

	caller, cancel := context.WithCancel(context.WithValue(context.Background(), callerMark{}, true))
	defer cancel()
	if result := runtime.Execute(caller, call("probe")); result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	if seen == caller {
		t.Fatal("声明了预算的工具该拿到一条派生出来的 ctx，而不是调用方那条")
	}
	if seen.Value(callerMark{}) != true {
		t.Fatal("派生出来的那条必须是调用方那条的子孙，否则调用方的取消就被摘掉了")
	}
}

func TestFastToolKeepsItsOwnResult(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	register(t, runtime, tool("fast", 10*time.Second, func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
		return json.Marshal("ok")
	}))

	result := runtime.Execute(context.Background(), call("fast"))
	if result.IsError {
		t.Fatalf("按时返回的工具不该被换掉：%+v", result.Error)
	}
	if string(result.Value) != `"ok"` {
		t.Fatalf("权威值不对：%s", result.Value)
	}
	if got := text(result.Content); got != "ok" {
		t.Fatalf("给模型看的内容不对：%q", got)
	}
}

func TestDeadlineReplacesACooperativeResult(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	register(t, runtime, tool("slow", budget, cooperative(nil)))

	assertTimedOut(t, runtime.Execute(context.Background(), call("slow")))
}

func TestDeadlineReplacesAProviderOwnedAbortError(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	// 一个转发了取消、并且**用自己的错误**表达取消的工具（网页抓取那一类的形状）。
	// 它的错误在模型那边同样读不出「是超时」，所以一样要换掉。
	register(t, runtime, tool("aborter", budget, func(ctx context.Context, _ json.RawMessage, _ *tools.RunContext) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, errors.New("web fetch aborted")
	}))

	assertTimedOut(t, runtime.Execute(context.Background(), call("aborter")))
}

func TestUpstreamCancelKeepsTheRegistryAbort(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	entered := make(chan struct{})
	// 预算给得足够长，长到它绝不可能先到——这样「谁先取消」就只有一个答案。
	register(t, runtime, tool("slow", time.Hour, cooperative(entered)))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-entered
		cancel()
	}()
	result := runtime.Execute(ctx, call("slow"))

	if !result.IsError {
		t.Fatalf("被撤掉的调用不该当成功交出去：%+v", result)
	}
	if code := errorCode(result); code != tools.CodeAborted {
		t.Fatalf("上游撤销该原样报成 %q，拿到的是 %q", tools.CodeAborted, code)
	}
}

func TestDeadlineWinsOverALaterUpstreamCancel(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	install(t, runtime)
	sawAbort := make(chan struct{})
	release := make(chan struct{})
	// 这个工具在看见取消之后还要收拾一会儿。那段时间里调用方也撤了——
	// 但先到的是期限，结论就该是超时。
	register(t, runtime, tool("slow-cleanup", budget, func(ctx context.Context, _ json.RawMessage, _ *tools.RunContext) (json.RawMessage, error) {
		<-ctx.Done()
		close(sawAbort)
		<-release
		return json.Marshal("cleanup complete")
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-sawAbort
		cancel()
		close(release)
	}()

	assertTimedOut(t, runtime.Execute(ctx, call("slow-cleanup")))
}

func TestUndoRemovesTheRule(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(t)
	undo := install(t, runtime)
	var seen context.Context
	register(t, runtime, tool("probe", 10*time.Second, captureCtx(&seen)))

	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.Execute(caller, call("probe"))
	if seen == caller {
		t.Fatal("装上之后该换成一条派生的 ctx")
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	runtime.Execute(caller, call("probe"))
	if seen != caller {
		t.Fatal("撤销之后这一层就不该再碰调用方那条 ctx 了")
	}
}
