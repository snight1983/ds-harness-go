// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/goal/command-goal/src/invariant.ts

package goalcommand

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/goal/command-goal/src/invariant.ts:21-29
//
// 装进去的检查是**空的**，这是刻意的，不是没写完。DSH 那句原话是「this command
// adapter owns no event stream or state projection; accepted mutations are checked by
// the goal domain and command dispatch behavior is covered by package tests」——本包
// 不拥有任何事件流，也不折任何状态投影；准入的每一次改动由
// [github.com/snight1983/ds-harness-go/goal/goal] 那边验，而这一层的断句和分流由本包的测试盯着。
//
// 那为什么还要登记？占住这个包名，并且让「检查过了、结论是无需检查」和「这个包
// 被漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("goalcommand: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
