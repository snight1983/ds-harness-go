// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/goal/tool-goal/src/invariant.ts

package goaltool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/goal/tool-goal/src/invariant.ts:19-29
//
// 装进去的检查是**空的**，这是刻意的，不是没写完：本包一个字节的耐久状态都不持有，
// 落得下去的改动全归 [github.com/snight1983/ds-harness-go/goal/goal] 那台服务验；本包唯一拥有的东西是
// 那套授权行为，而行为归包自己的测试验，不归不变量。
//
// 那为什么还要登记？理由和 [github.com/snight1983/ds-harness-go/subagent/controltool.RegisterInvariants]
// 逐字相同：占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被漏掉了」
// 区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("goaltool: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
