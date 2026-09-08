// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/experimental/tool-agent-team/src/invariant.ts

package agentteamtool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/experimental/tool-agent-team/src/invariant.ts:14,16-18
//
// 装进去的检查是**空的**，这是刻意的，不是没写完，理由和 DSH 那句注释逐字相同：
// 耐久关系和授权关系都归 [github.com/snight1983/ds-harness-go/feature/agentteam]
// 那台服务所有，本包只是它面向模型的一层适配，自己不持有任何要守的东西。
//
// 那为什么还要登记？理由同
// [github.com/snight1983/ds-harness-go/feature/subagent/controltool.RegisterInvariants]：
// 占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("agentteamtool: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
