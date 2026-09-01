// 本文件的作用：服务自己那份**逐节点**的表面折叠。它把表面上每一个节点的估价
// 都留着，因为 [TokenMeter.Measure] 交出去的结果里要带上那张表——压缩那边挑下刀点
// 就是照着它挑的。
//
// 这份折叠的状态是 O(表面)。三个投影单元**有意不共用**它：投影的状态要写进检查点
// 落盘，一份随会话长度线性增长的状态会把检查点撑爆，所以它们走的是另一条 O(1) 的
// 影子价路线，见 surfaceprojection.go。同一件事有两份实现是**故意**的。
//
// 源: packages/llm/token-meter/src/surface-fold.ts

package tokenmeter

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/session"
)

// surfaceTokenFold 是折进一条表面事件之后的结果。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:37-47（SurfaceTokenPlan）
type surfaceTokenFold struct {
	// tokens 是这条事件自己派生出的那条消息的估价。
	tokens int
	// nodes 是折叠之后的整张表面节点表，**一份新的**，见函数注释。
	nodes []SurfaceNode
	// deltaTokens 是这一步带来的净变化，带符号：一次替换换掉的可能比换上的贵。
	deltaTokens int
}

// foldSurfaceTokens 把一条表面事件折进当前的节点表。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:77-107（planSurfaceTokens）
//
// 它是**全的**，并且交出来的一定是一份新分配的节点表：出错时调用方手上那份
// 一个字节都没被动过。DSH 那边靠 `[...nodes]` 加 splice 做到这件事，理由一样
// ——这份状态是跨事件累积的，一次半途失败的折叠会让它从此和日志对不上。
//
// 派生不出消息的事件（内容为空、只为携带用量而存在的那条 assistant/message）
// 估价为 0，但**照样占一个表面节点**：它在表面上是真实存在的一格，
// 压缩那边要按格数和 seq 去对表。
func foldSurfaceTokens(nodes []SurfaceNode, event session.Event) (surfaceTokenFold, error) {
	operation, eligible, err := session.SurfaceOpOf(event)
	if err != nil {
		return surfaceTokenFold{}, err
	}
	if !eligible {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 的事件 %q 不上表面，折不进来", event.Seq, event.Type)
	}

	tokens := 0
	// 第二个返回值为假表示这条事件派生不出消息，那就是 0 个 token——
	// DSH 那边是 `message === null ? 0 : estimateMessage(message)`。
	message, derived, err := session.DeriveEventMessage(event)
	if err != nil {
		return surfaceTokenFold{}, err
	}
	if derived {
		if tokens, err = EstimateMessage(message); err != nil {
			return surfaceTokenFold{}, err
		}
	}

	if operation.SurfaceOpKind() == session.OpAppend {
		next := make([]SurfaceNode, len(nodes), len(nodes)+1)
		copy(next, nodes)
		next = append(next, SurfaceNode{Seq: event.Seq, Tokens: tokens})
		return surfaceTokenFold{tokens: tokens, nodes: next, deltaTokens: tokens}, nil
	}

	replace, isReplace := operation.(session.ReplaceOp)
	if !isReplace {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 带的表面操作认不得：%q", event.Seq, operation.SurfaceOpKind())
	}

	// 区间的两端都必须是当前表面上真实存在的节点，而且起点不能在终点后面。
	// 对不上说明这份节点表算的根本不是当前这个表面——继续折下去只会把一个
	// 错得离谱的数悄悄传下去，所以在这里断掉。
	startIndex := indexOfSeq(nodes, replace.Start)
	endIndex := indexOfSeq(nodes, replace.End)
	if startIndex < 0 || endIndex < 0 || startIndex > endIndex {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 的替换声明的当前区间 %d-%d 在表面上不成立",
			event.Seq, replace.Start, replace.End)
	}

	removed := 0
	for _, node := range nodes[startIndex : endIndex+1] {
		removed += node.Tokens
	}
	next := make([]SurfaceNode, 0, len(nodes)-(endIndex-startIndex))
	next = append(next, nodes[:startIndex]...)
	next = append(next, SurfaceNode{Seq: event.Seq, Tokens: tokens})
	next = append(next, nodes[endIndex+1:]...)
	return surfaceTokenFold{tokens: tokens, nodes: next, deltaTokens: tokens - removed}, nil
}

// indexOfSeq 找出某个 seq 在节点表里的下标，找不到给 -1。
func indexOfSeq(nodes []SurfaceNode, seq int) int {
	for index, node := range nodes {
		if node.Seq == seq {
			return index
		}
	}
	return -1
}
