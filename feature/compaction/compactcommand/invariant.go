// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/compaction/command-compact/src/invariant.ts

package compactcommand

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/compaction/command-compact/src/invariant.ts:17-29
//
// 装进去的检查是**空的**，这是刻意的，不是没写完。DSH 那句原话是「this command
// adapter owns no state or event stream; the compaction seam owns the balanced durable
// transaction and the command registry owns registration and dispatch lifecycle」——
// 那对配平的持久标记归 [github.com/snight1983/ds-harness-go/feature/compaction] 验，
// 登记与派发的生命周期归 [github.com/snight1983/ds-harness-go/feature/interaction/commands] 验，
// 本包一个字节的状态都不拥有。
//
// 那为什么还要登记？占住这个包名，并且让「检查过了、结论是无需检查」和「这个包
// 被漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("compactcommand: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
