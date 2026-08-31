// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/sdk/protocol/src/invariant.ts

package sdkprotocol

import (
	"context"
	"fmt"

	"ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/sdk/protocol/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-sdk-protocol"

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/sdk/protocol/src/invariant.ts:29
//
// 装进去的检查是**空的**，这是刻意的：本包是一个纯粹的线上协议库（一条通道加一批
// 类型声明），不拥有任何事件流，也不折任何可变的数据关系——线两端各自的行为由各自
// 那个包负责。
//
// 那为什么还要登记？占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被
// 漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("sdkprotocol: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
