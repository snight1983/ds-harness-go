// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/workflow/tool-ralph/src/invariant.ts

package toolralph

import (
	"context"
	"fmt"

	"ds-harness-go/invariants"
)

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/workflow/tool-ralph/src/invariant.ts:21-29
//
// 装进去的检查是**空的**，这是刻意的，不是没写完。DSH 那句原话是「this model-facing
// orchestration adapter owns no independent event stream; workflow and subagent owners
// validate the runs and child lifecycles it starts」——本包只是一层面向模型的编排适配，
// 自己不开任何独立的事件流；它起的那些孩子的生命周期归那条子 agent 接缝所有，
// 也由它去验。
//
// 新增: DSH 那句话里还提到 workflow 那个属主。本包没有那一层（脚本引擎裁了
// OUT_OF_SCOPE，见 [doc.go]），所以那半句在这里落空——而它落空**不影响**这条结论：
// 那条循环唯一会碰的持久状态就是每一轮那个孩子的会话，那本来就归子 agent 那边验。
//
// 那为什么还要登记？理由和
// [ds-harness-go/subagent/subagenttool.RegisterInvariants] 逐字相同：占住这个包名，
// 并且让「检查过了、结论是无需检查」和「这个包被漏掉了」区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("toolralph: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
