// 本文件的作用：这台驱动自己那两种失败的措辞——「调用方给的装配不成立」和
// 「取消在孩子公布之前就赢了」。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:73-76

package inprocessdriver

import (
	"fmt"

	"ds-harness-go/subagent/subagent"
)

// errInvalidRequestf 造一条「调用方给的东西本身不成立」，认得出
// [ds-harness-go/subagent/subagent.ErrInvalidRequest]。
//
// 新增: DSH 那边这些是解构 cordis 上下文时自然抛出的 TypeError——服务不在场就是
// 属性不在。Go 这一侧「在不在场」是装配方手上有没有那个值，所以由本包自己检出来，
// 并且挂上那条接缝的哨兵错误，好让上游一句 errors.Is 就分得清是谁的错。
func errInvalidRequestf(format string, args ...any) error {
	return fmt.Errorf("%w：%s", subagent.ErrInvalidRequest, fmt.Sprintf(format, args...))
}

// prePublicationAbort 是取消在孩子公布之前赢下这次开工时报的那条错。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:73-76
//
// 这一路**没有**运行交出去，所以调用方不必处置任何东西，也不会有生命周期边发出去。
//
// 新增: DSH 抛的是一条光的 Error，没有码——它不是上游要分情况处理的运行期结局。
// Go 这边同样不给码，但把 ctx 那条取消原因包进去，好让 errors.Is(err,
// context.Canceled) 仍旧成立。
func prePublicationAbort(cause error) error {
	return fmt.Errorf("子 agent 请求在孩子公布之前被取消了：%w", cause)
}
