// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/session-query/tool-session-query/src/invariant.ts

package querytool

import (
	"context"
	"fmt"

	"ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/session-query/tool-session-query/src/invariant.ts:20-30
//
// 装进去的检查是**空的**，这是刻意的，不是没写完：这五件工具全都只读，
// 不往日志里写任何事件，也不持有任何跨记录的可变关系——真正要验的东西
// （工具名不许撞、参数 schema 得是对象根、提示词段落名不许撞）全都由它们
// 各自注册的那个表在登记那一刻验完了。
//
// 那为什么还要登记？为了**占住这个名字**。注册表按包名分区，一个包如果从来
// 没登记过，别的包就可以用同一个名字登记一批检查而不被察觉；而这个名字一旦
// 占住，登记本身也就成了一份「这个包检查过了、结论是无需检查」的记录，
// 和「这个包被漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("querytool: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
