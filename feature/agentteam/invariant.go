// 本文件的作用：这个包在不变量注册表里占的那个位置，以及 DSH 那条事件级检查
// 在这里为什么落成了一条空检查。

package agentteam

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/experimental/agent-team/src/invariant.ts:20-36（install、apply）
//
// 装进去的检查是**空的**，这是刻意的：DSH 那条检查在一条会话事件落盘之前，拿一份
// 克隆过的投影状态把它试折一遍，折不动就报违例——它守的是「介质上那份状态永远
// 是一串合法事件折出来的」。这条约定在 Go 这边由别处守着，而且守得更早。
//
// 团队状态在这里不走会话日志的事件流，落在 [Spec] 声明的那三张表上，每张表都带着
// 自己的校验体（[Roster.Validate]、[Board.Validate]、[Message.Validate]）。域这一层
// 在**编码写下去和解码读回来这两头**都会叫它们：一块前置关系绕成圈的板既写不进去，
// 也读不出来。DSH 要在事件流上另立一道检查，正是因为它那份投影状态没有这样一个
// 收口——它的合法性只存在于折叠函数里，不存在于落盘的字节上。
//
// 那为什么还要登记？为了让「检查过了、结论是无需检查」和「这个包被漏掉了」区分得开，
// 理由和 [github.com/snight1983/ds-harness-go/feature/goal/goaltool.RegisterInvariants]
// 逐字相同。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("agentteam: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
