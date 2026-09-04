// 本文件的作用：把运行时那套"回合怎么结束的"翻成 ACP 线上认得的那几个停止原因。
//
// 源: packages/acp/acp/src/codec.ts

package acp

import (
	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TurnEndToStopReason 把一次回合结束翻成 ACP 那套停止原因里最贴近的那一个。
//
// 源: packages/acp/acp/src/codec.ts:14-34
//
// `cancelled` 这个词在 ACP 上留给**显式的客户端取消**（`session/cancel`）和拆解，
// 那两条都在这个映射之外落定；一个被钩子或者别的主人中止掉的回合是寻常的静默，
// 报 `end_turn`。
//
// 新增: DSH 那个 switch 末尾有一条兜底的 default。Go 这边
// [github.com/snight1983/ds-harness-go/sessionlog.TurnEndReason] 是**开放**联合（读到本构建不认识的标签会落进
// [github.com/snight1983/ds-harness-go/sessionlog.UnknownTurnEnd]），所以这条兜底在这里不是死代码而是真会走到的
// 一条路：一个说不出名字的结束理由不能报成取消——取消是一句确切的话，客户端会据此
// 认定"这一轮是我停的"。
func TurnEndToStopReason(reason sessionlog.TurnEndReason) wire.StopReason {
	if reason == nil {
		return wire.StopReasonEndTurn
	}
	switch reason.TurnEndReasonKind() {
	case sessionlog.ReasonMaxTokens:
		return wire.StopReasonMaxTokens
	case sessionlog.ReasonInterrupted:
		return wire.StopReasonCancelled
	default:
		// completed / aborted / blocked / error 以及一切不认识的标签。
		return wire.StopReasonEndTurn
	}
}
