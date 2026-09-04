// 本文件的作用：钉住一次调用的参数字节上限——缺省值不改变现有行为、
// 超限时拒得干净、以及那道上限关得掉。
//
// 这道上限是本仓库自有的（见 [tools.ErrArgsTooLarge] 上的「新增」）。上游的参数
// 来自模型响应，尺寸受模型自己的输出上限约束；本仓库是服务端运行时，同一个入口
// 还接协议层、子 Agent、回放和直接调派发的宿主代码，那几条路上没有任何一处
// 保证载荷是模型写的。所以这里要验的不是「上限存在」，而是：
//
//   - 超限走的是**失败结果**这条路，不是 panic、也不是把大载荷放进去；
//   - 拒绝发生在 [json.Valid] 之前，不然一份几十兆的垃圾要先被整个走一遍；
//   - 失败结果对模型的呈现和别的参数错误一致（同一个代号），对宿主则能用
//     [errors.Is] 单独认出来。

package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// oversizedArguments 造一份 n 字节的**合法** JSON 参数。
//
// 合法是有意的：如果拿一串垃圾字节去试，用例就分不清这次拒绝是尺寸挡的还是
// 形状挡的，而两者的先后顺序正是要钉的东西之一。
func oversizedArguments(n int) json.RawMessage {
	// {"text":"…"} 这个壳是 11 个字节。
	const wrapper = 11
	if n < wrapper {
		panic("要求的字节数比最小的合法参数还小")
	}
	raw := `{"text":"` + strings.Repeat("x", n-wrapper) + `"}`
	return json.RawMessage(raw)
}

func TestArgumentCap缺省值放得过正常参数(t *testing.T) {
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	result := runtime.Execute(context.Background(), call("echo", "hi", nil))

	if result.IsError {
		t.Fatalf("一份正常参数不该被上限挡住：%+v", result.Error)
	}
	if got := text(result.Content); got != `"hi"` {
		t.Fatalf("内容不对：%q", got)
	}
}

// 缺省上限是一兆。真实工具里最宽的那些（把一整段代码贴进去的编辑类）在十万字节
// 这个量级，所以这一条验的是「留了一个数量级的余量」不是空话。
func TestArgumentCap缺省值放得过十万字节的参数(t *testing.T) {
	runtime := newRuntime(t, tools.Options{})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	input := call("echo", "", nil)
	input.Arguments = oversizedArguments(100_000)

	result := runtime.Execute(context.Background(), input)

	if result.IsError {
		t.Fatalf("十万字节在缺省上限之内，不该被挡：%+v", result.Error)
	}
}

func TestArgumentCap超限时拒得干净(t *testing.T) {
	runtime := newRuntime(t, tools.Options{MaxArgumentBytes: 64})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	input := call("echo", "", nil)
	input.Arguments = oversizedArguments(200)

	result := runtime.Execute(context.Background(), input)

	if !result.IsError {
		t.Fatal("超过上限的参数应当被拒")
	}
	if result.Value != nil {
		t.Fatalf("失败结果不该带值：%s", result.Value)
	}
	if result.ConcludesTurn {
		t.Fatal("失败结果不该有权力结束回合")
	}
	if result.Error == nil || result.Error.Info == nil {
		t.Fatalf("失败应当带结构化身份：%+v", result.Error)
	}
	// 对模型来说这就是「你给的参数不对」的一种，所以代号和别的参数错误一样。
	if result.Error.Info.Code != tools.CodeInvalidArgs {
		t.Fatalf("代号应当是 %s，实际是 %s", tools.CodeInvalidArgs, result.Error.Info.Code)
	}
	// 拒绝的理由要说得出是尺寸，不然宿主只能去猜。
	if !strings.Contains(result.Error.Message, "200") || !strings.Contains(result.Error.Message, "64") {
		t.Fatalf("这句话里应当既有实际字节数也有上限：%q", result.Error.Message)
	}
}

// 宿主要能把「参数太大」和别的参数错误分开：前者是配额问题，重试同一份载荷
// 没有意义；后者是模型写错了，改一改再来是有意义的。
func TestArgumentCap两条哨兵都认得出(t *testing.T) {
	err := error(&tools.ArgsTooLargeError{ToolName: "echo", Bytes: 200, Limit: 64})

	if !errors.Is(err, tools.ErrArgsTooLarge) {
		t.Fatal("应当认得出 ErrArgsTooLarge")
	}
	if !errors.Is(err, tools.ErrInvalidArgs) {
		t.Fatal("应当同时认得出 ErrInvalidArgs——对模型来说这就是参数不对")
	}
}

func TestArgumentCap负数把这一层关掉(t *testing.T) {
	runtime := newRuntime(t, tools.Options{MaxArgumentBytes: -1})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	input := call("echo", "", nil)
	input.Arguments = oversizedArguments(2 << 20)

	result := runtime.Execute(context.Background(), input)

	if result.IsError {
		t.Fatalf("上限关着的时候两兆也该放过去：%+v", result.Error)
	}
}

// 尺寸要先于形状判。这一条靠一份**又大又不合法**的参数来分辨：两道都会拒，
// 但只有尺寸那道先跑，报出来的才是尺寸。
func TestArgumentCap尺寸判在形状之前(t *testing.T) {
	runtime := newRuntime(t, tools.Options{MaxArgumentBytes: 64})
	mustRegister(t, runtime, scope.NewRoot(), echoTool("echo"))

	input := call("echo", "", nil)
	input.Arguments = json.RawMessage(strings.Repeat("{", 200))

	result := runtime.Execute(context.Background(), input)

	if !result.IsError {
		t.Fatal("这份参数怎么算都该被拒")
	}
	if !strings.Contains(result.Error.Message, "over the 64 byte limit") {
		t.Fatalf("应当报尺寸而不是形状：%q", result.Error.Message)
	}
}
