// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/subagent/tool-subagent/src/invariant.ts

package subagenttool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/subagent/tool-subagent/src/invariant.ts:43-49（apply）
//
// 装进去的检查是**空的**，这是刻意的，不是没写完：本包只是那条子 agent 接缝面向
// 模型的一层适配，自己不开任何独立的生命周期流；执行之间那些关系归它调的那道能力
// 接缝所有，也由它去验。
//
// 那为什么还要登记？理由和 [github.com/snight1983/ds-harness-go/feature/subagent/controltool.RegisterInvariants]
// 逐字相同：占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被漏掉了」
// 区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("subagenttool: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
