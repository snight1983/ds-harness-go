// 本文件的作用：这一层的全部内容——它拥有的那个代号、装上去的那条绕派发规则，
// 以及期限赢了之后换上去的那份结果。
//
// 源: packages/guard/timeout-policy/src/index.ts:14-81

package timeoutpolicy

import (
	"context"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/timeout"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// Code 是本包拥有的超时代号，一个身份两处用。
//
// 源: packages/guard/timeout-policy/src/index.ts:18-25（TOOL_TIMEOUT）
//
// 一处是**对内**：[timeout.Deadline] 拿它给这条期限打标，[timeout.OfContext] 再拿它
// 问回来。挂上这个代号是嵌套期限能被分辨开的唯一手段——外层某个 `AroundDispatch`
// 包装自己的期限先到了的话，用这个代号问出来是 nil，于是这一层正确地把它读成
// 上游取消，而不是「我超时了」。
//
// 另一处是**对外**：换上去那份结果的 error.info.code 就是它，所以下游的重试策略、
// 沙箱策略和回放都能按它路由。
const Code = "TOOL_TIMEOUT"

// errorName 是换上去那份结果的 error.info.name。
//
// 源: packages/guard/timeout-policy/src/index.ts:47
//
// 不导出：按 DSH 的约定，路由用的是 [Code]，name 只是给人看的那一半。
const errorName = "ToolTimeoutError"

// Install 把这条超时规则装到一个工具注册表上，返回撤销它的函数。
//
// 源: packages/guard/timeout-policy/src/index.ts:57-81
//
// owner 决定它管哪些 agent：[scope.NewRoot] 造的作用域没有身份，规则落全局层、
// 管所有 agent；有身份的作用域只管那条链下面的。这条链由 tools 提供，
// 本包只是照它的规矩登记一次。
//
// runtime 既是登记的去处，也是**读预算的去处**：预算写在工具定义上，而一个工具名
// 在不同 agent 眼里可能解析到不同的定义（作用域注册盖住全局注册），所以每次调用都
// 得按这次调用的 agent 重新查一遍，不能在装的时候查一次存起来。
func Install(ctx context.Context, runtime *tools.Runtime, owner *scope.Scope) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("timeoutpolicy: 需要一个工具注册表")
	}
	rule := func(dispatchCtx context.Context, exec tools.Execution, next func(context.Context) (tools.Result, error)) (tools.Result, error) {
		definition, ok := runtime.Get(exec.Name, exec.Agent)
		// 查不到定义就原样放过：这次派发接下来会以「找不到这个工具」失败，
		// 那句诊断比这一层现编一个超时有用得多。
		if !ok || definition.Timeout <= 0 {
			return next(dispatchCtx)
		}

		// 必须从 dispatchCtx 派生。这是绕派发那条规矩里唯一不许犯的错：
		// 传一个不相干的 ctx 进去等于把调用方的取消摘掉了。
		deadlineCtx, cancel := timeout.Deadline(dispatchCtx, definition.Timeout, Code)
		defer cancel()

		result, err := next(deadlineCtx)
		// 先问再返回：defer 里的 cancel 会给这条 ctx 定一个「已取消」的原因，
		// 但那只在期限**没赢**的时候才会生效（cancel 只有第一次算数），
		// 所以问的时机必须在 defer 跑之前。
		if timeout.OfContext(deadlineCtx, Code) == nil {
			return result, err
		}
		// 期限赢了：执行体已经看见取消并收敛了，它交出来的东西（自己的取消结果，
		// 或者一份跑了一半的值）一律作废，换成模型读得懂的那一份。
		// next 那边的 err 也一并丢掉，理由相同——它描述的是被打断的那次执行，
		// 而这次调用真正的结局是超时。
		return timedOutResult(definition.Timeout.Milliseconds()), nil
	}
	return runtime.AroundDispatch(ctx, owner, rule)
}

// timedOutResult 是期限赢了之后交给模型的那份结果。
//
// 源: packages/guard/timeout-policy/src/index.ts:44-51
//
// 这三个字段是手写的，没有走 tools 里那个内部的失败结果构造器：DSH 这一层
// 也是手写一个对象字面量，而这份形状（`Error: <话>` 的内容块 + 同一句话的 message
// + 带 name/code 的 info）是**这个包对下游的承诺**，不该跟着别的包的内部改法走。
func timedOutResult(afterMilliseconds int64) tools.Result {
	message := fmt.Sprintf("tool call timed out after %dms", afterMilliseconds)
	return tools.Result{
		IsError: true,
		Error: &tools.Failure{
			Message: message,
			Info:    &tools.ErrorInfo{Name: errorName, Code: Code},
		},
		Content: llm.Content{llm.TextBlock{Text: "Error: " + message}},
	}
}
