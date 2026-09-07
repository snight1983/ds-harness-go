// 本文件的作用：调用方朝这条接缝要的那件事（开一次运行）、它拿回来的那个活句柄，
// 以及引擎实现方要兑现的那张面。
//
// 源: packages/workflow/workflow/src/runtime-types.ts、packages/workflow/workflow/src/index.ts:157-187

package workflow

import (
	"context"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/harness/agent"
)

// StartRequest 是调用方开一次工作流运行时给的东西。
//
// 源: packages/workflow/workflow/src/runtime-types.ts:19-34
type StartRequest struct {
	// Script 是那段脚本正文。
	Script string
	// Meta 是这个工作流的身份说明，形状由引擎验。
	Meta Meta
	// Args 是原样暴露给脚本的那份输入，可以为 nil。
	//
	// 新增: 类型是 [encoding/json.RawMessage] 而不是 any，理由同 [Result.Value]。
	Args json.RawMessage
	// SubagentProvider 覆盖这次运行整体的子 agent 提供方，空串表示不覆盖。
	SubagentProvider string
	// MaxTotalAgents 是这次运行的子 agent 总数天花板，0 表示不另设、用引擎的默认值。
	//
	// 新增: DSH 那边它是一个可选数字，「没给」和「给 0」分得开。Go 的 int 没有
	// 「不在」，而 0 个子 agent 的工作流退化成一段不许扇出的脚本——那不是任何调用方
	// 会**要**的东西，所以把 0 定义成「不另设」，比多带一个指针清楚。
	MaxTotalAgents int
	// Parent 是这次运行代表谁在跑：脚本扇出的每一个孩子都记在它名下。必填。
	Parent agent.Agent
}

// Run 是一次归持有方所有的活工作流运行。
//
// 源: packages/workflow/workflow/src/runtime-types.ts:40-49
//
// 拿到它的人负责处置它。
type Run interface {
	// ID 是这次运行的 id。
	ID() RunID
	// Meta 是那份已经验过的身份说明，脚本正文跑起来之前就取得到。
	Meta() Meta
	// Result 等这次运行结清，交出它的结局。
	//
	// **脚本层面的失败不从这里报错**：脚本抛了、被取消了，交回的都是一个
	// StopReason 相应的 [Result]，好让消费方把它映成一个 isError 的工具结果。
	//
	// 新增: DSH 是一个「永不 reject」的 Promise 字段，反复 await 拿的是同一份已决的
	// 值。Go 这边是一个收 ctx 的方法：Promise 一造出来就在跑，而 Go 需要一个地方
	// 接住调用方的取消——那次取消从 error 出去，和这次运行自己的结局分得开。
	// 多次调用交回同一份结果。做法同
	// [github.com/snight1983/ds-harness-go/feature/subagent.Run.Result]。
	Result(ctx context.Context) (Result, error)
	// Cancel 取消这次运行和它那些孩子。reason 可以是空串。
	//
	// 发完就返回：取消信号在返回之前已经发出，但脚本可能要跑到它自己观察到那个信号
	// 为止。等它真的静下来用 [Run.Dispose]。
	Cancel(reason string)
	// Dispose 该取消就取消，然后在一个有界的时间里等结清和清理跑完。可以重复调。
	Dispose(ctx context.Context) error
}

// Engine 是工作流这条能力接缝的服务定义。
//
// 源: packages/workflow/workflow/src/index.ts:157-187（WorkflowEngine）
//
// 契约：不成立的请求在**发布之前**就报错，所以一次失败的开工既不留下要调用方处置的
// 运行，也不发任何生命周期边；一个已发布的运行归持有方所有，它的结果不从 error 报
// 脚本失败；取消和处置都是有界的，而处置要连孩子的清理一起等完。
//
// 新增: DSH 那边它是一个抽象类，把发边那件事做成了一个 protected 方法
// （emitWorkflowEvent）让子类继承。Go 没有继承，那件事拆成了 [Lifecycle]：引擎实现
// 自己攥一个，在该发边的地方调它。好处是发边这件能力可以交给宿主去装观察者，而不必
// 让宿主拿到引擎本身。
//
// 本仓库没有任何实现，理由见包文档。
type Engine interface {
	// Start 解析并执行一段工作流脚本，交回那个活的运行。
	//
	// 新增: DSH 那个方法是同步的、不收上下文，取消走请求里那个 AbortSignal。Go 这边
	// 取消统一走 ctx：开工期间（解析、验 meta）的取消由传进来的 ctx 管，发布之后
	// 那次运行自己的取消走 [Run.Cancel] 和 [Run.Dispose]。
	Start(ctx context.Context, request StartRequest) (Run, error)
}
