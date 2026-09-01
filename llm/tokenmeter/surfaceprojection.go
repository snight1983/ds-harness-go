// 本文件的作用：投影单元用的那份 **O(1)** 表面折叠，以及它赖以成立的影子价协议。
//
// 为什么不共用 surfacefold.go 那一份：投影的状态要序列化进检查点落盘，而那份状态
// 里存着表面上**每一个**节点的估价，长度随会话线性增长。这里的状态只有一个可选的
// 认领单，无论会话多长都是那么大。
//
// 代价是：一次替换要知道「被换掉那一段值多少」，而这里没有那张表。补偿它的就是
// 影子价协议——压缩在写下替换之前，先写一条 compaction/summary 或 compaction/prune
// 事件，把自己即将盖掉的那段区间和它的估价一起记在日志里。折叠只需要记住紧挨着的
// 上一张认领单。
//
// 源: packages/llm/token-meter/src/surface-projection.ts

package tokenmeter

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/session"
)

// ShadowPriceClaim 是一张「我马上要盖掉这段，它值这么多」的认领单。
//
// 源: packages/llm/token-meter/src/surface-projection.ts:26-38（ShadowPriceClaim）
//
// 它由压缩那边写下的 compaction/summary ／ compaction/prune 事件举起来，
// 只对**紧挨着的下一条**事件有效。
type ShadowPriceClaim struct {
	// Start 是认领的区间起始 seq（含）。
	Start int `json:"start"`
	// End 是认领的区间结束 seq（含）。
	End int `json:"end"`
	// Tokens 是压缩那边给这段区间记下的估价。
	Tokens int `json:"tokens"`
}

// surfaceTokensFold 是投影侧折进一条事件之后的结果。
//
// 源: packages/llm/token-meter/src/surface-projection.ts:40-46（SurfaceTokensFold）
type surfaceTokensFold struct {
	// deltaTokens 是这一步带来的净变化，带符号。
	deltaTokens int
	// claim 是折完之后仍然举着的认领单；nil 表示没有。
	claim *ShadowPriceClaim
}

// foldSurfaceProjection 把一条事件折进投影侧的表面记账。
//
// 源: packages/llm/token-meter/src/surface-projection.ts:41-94
//
// 认领单的三种去向，每一条都要看清楚：
//
//   - **举起**：读到 compaction/summary 或 compaction/prune，记下它声明的区间和估价，
//     这一步自己不产生任何变化。
//   - **过期**：读到别的任何事件（包括一次普通的追加）都会把它放掉。「紧挨着」
//     这个要求就是这么实现的：中间隔了一条事件的认领单不再作数。
//   - **兑掉**：读到一次替换，且认领的区间和替换声明的区间**完全一致**，
//     用认领的估价当被换掉那一段的价钱。
//
// 有意做成不对称的两种失败：
//
//   - 一次替换**没有**任何认领单在手：折 0，不报错。这条日志来自影子价协议
//     落地**之前**的版本，那时候压缩不写这两条事件。报错会让一份老会话根本
//     打不开；折 0 只是让这个投影从此偏一点，而它本来就是个会偏的估算。
//   - 一次替换**有**认领单但区间对不上：报错。这说明写日志的一方**在用**这个
//     协议，却把它用错了——那是一个真的缺陷，咽下去只会让偏差无声地攒起来。
func foldSurfaceProjection(claim *ShadowPriceClaim, event session.Event) (surfaceTokensFold, error) {
	switch event.Type {
	case compaction.EventCompactionSummary:
		data, err := compaction.DecodeSummary(event)
		if err != nil {
			return surfaceTokensFold{}, err
		}
		return surfaceTokensFold{claim: &ShadowPriceClaim{
			Start:  data.ShadowedRange.Start,
			End:    data.ShadowedRange.End,
			Tokens: data.ShadowedTokenCount,
		}}, nil
	case compaction.EventCompactionPrune:
		data, err := compaction.DecodePrune(event)
		if err != nil {
			return surfaceTokensFold{}, err
		}
		return surfaceTokensFold{claim: &ShadowPriceClaim{
			Start:  data.ShadowedRange.Start,
			End:    data.ShadowedRange.End,
			Tokens: data.ShadowedTokenCount,
		}}, nil
	}

	if !session.IsSurfaceEvent(event) {
		return surfaceTokensFold{}, nil
	}

	tokens := 0
	message, derived, err := session.DeriveEventMessage(event)
	if err != nil {
		return surfaceTokensFold{}, err
	}
	if derived {
		if tokens, err = EstimateMessage(message); err != nil {
			return surfaceTokensFold{}, err
		}
	}

	if event.SurfaceOp.SurfaceOpKind() == session.OpAppend {
		return surfaceTokensFold{deltaTokens: tokens}, nil
	}
	replace, isReplace := event.SurfaceOp.(session.ReplaceOp)
	if !isReplace {
		return surfaceTokensFold{}, fmt.Errorf(
			"token 表面：seq %d 带的表面操作认不得：%q", event.Seq, event.SurfaceOp.SurfaceOpKind())
	}

	if claim == nil {
		// 协议落地之前的日志：认了这一次的偏差，把这个投影继续往下推。
		return surfaceTokensFold{}, nil
	}
	if claim.Start != replace.Start || claim.End != replace.End {
		return surfaceTokensFold{}, fmt.Errorf(
			"token 表面：seq %d 对区间 %d-%d 的替换旁边没有对应的影子价（手上那张认领的是 %d-%d）",
			event.Seq, replace.Start, replace.End, claim.Start, claim.End)
	}
	return surfaceTokensFold{deltaTokens: tokens - claim.Tokens}, nil
}

// foldSurfaceProjectionLenient 是 [foldSurfaceProjection] 给投影单元用的那一面：
// 出错时降级成「这一笔不记、手上那张认领单放掉」。
//
// 新增: DSH 那边 foldSurfaceProjection 是直接抛的，而它的调用方——投影单元的
// apply——也就跟着抛了出去。Go 这边 [projection.Definition].Apply 的签名里根本
// 没有错误这一路：那是投影那个包定下来的（见它的包文档），因为一次折叠横在
// 「事件已经提交」和「读的一方看到的值」中间，让它失败就等于让一条已经落定的
// 事件把整个会话读不开。
//
// 所以这里把两种失败合并成同一种降级：不记这一笔增量。这和 DSH 处理
// 「没有认领单」那一支的行为逐字相同（那一支本来就是折 0），区别只是
// 「认领单区间对不上」这个**协议被用错了**的信号在投影这一侧丢掉了。
// 它没有整个消失——服务那一侧的 [foldSurfaceTokens] 仍然会因为区间在表面上
// 不成立而报错，而 [TokenMeter.Measure] 走的正是那条路。
func foldSurfaceProjectionLenient(claim *ShadowPriceClaim, event session.Event) surfaceTokensFold {
	fold, err := foldSurfaceProjection(claim, event)
	if err != nil {
		return surfaceTokensFold{}
	}
	return fold
}
