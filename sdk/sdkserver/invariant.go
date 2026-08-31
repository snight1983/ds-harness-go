// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/sdk/server/src/invariant.ts

package sdkserver

import (
	"context"
	"fmt"

	"ds-harness-go/invariants"
)

// InvariantPluginName 是这个不变量伴生插件的名字。
//
// 源: packages/sdk/server/src/invariant.ts:13
const InvariantPluginName = "sdk-jsonrpc-server-invariant"

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/sdk/server/src/invariant.ts:28-29
//
// 装进去的检查是**空的**（DSH invariant.ts:21 那个 `() => {}`），这是刻意的：这一层是
// 一个呈现适配器，它不拥有任何耐久的、属于本包的事件流——它转发的每一条边都归发出
// 那条边的那个包所有，也在那边被检查。这一层自己的协议映射由边界测试和回放测试覆盖。
//
// 那为什么还要登记？占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被
// 漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("sdkserver: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
