// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/skill/tool-skill/src/invariant.ts

package skilltool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/skill/tool-skill/src/invariant.ts:17-29
//
// 装进去的检查是**空的**，这是刻意的，不是没写完：本包是那台技能注册表面向
// 模型的一层适配，自己不开任何独立的生命周期流；真正要验的那些执行关系归它
// 调的那道能力接缝所有。它往日志里写的目录和注入消息也都是自足的用户消息，
// 不和别的记录结成跨记录的关系。
//
// 那为什么还要登记？理由和 [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.RegisterInvariants]
// 逐字相同：占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被漏掉了」
// 区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("skilltool: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
