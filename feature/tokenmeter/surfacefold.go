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

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// meterNode 是这份折叠自己留着的一个表面节点。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:25-35（MeterSurfaceNode）
//
// 它比对外那份 [SurfaceNode] 多两样，都是为了让一张图能按路由重新定价：那次出现
// 的持久引用，以及「把每一次图片出现的结构价都刨掉之后」这个节点还剩多少。
// 有了这两样，[priceSurface] 就能在不重新派生消息的前提下，把图片那一份换成
// 路由自己报的价。
type meterNode struct {
	// seq 是这个节点对应事件的 seq。
	seq int
	// heuristicTokens 是这个节点在那把固定尺子下的估价。
	heuristicTokens int
	// imageFreeTokens 是刨掉每一次图片出现的结构价之后剩下的部分。
	imageFreeTokens int
	// images 是这个节点里每一次图片出现的持久引用，按消息顺序；没有图时为空。
	images []attachment.ImageRef
}

// surfaceTokenFold 是折进一条表面事件之后的结果。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:37-47（SurfaceTokenPlan）
//
// 新增: DSH 的 SurfaceTokenPlan 还带一个 deltaTokens（这一步的带符号净变化）。
// 那个字段在这里没有产出方：表面总价是**跟着路由变**的，所以它由
// [priceSurface] 在每一次测量时按当下那条路由现算，重放状态里不再留一个
// 路由无关的累计数给它加。
type surfaceTokenFold struct {
	// tokens 是这条事件自己派生出的那条消息在那把固定尺子下的估价。
	tokens int
	// nodes 是折叠之后的整张表面节点表，**一份新的**，见函数注释。
	nodes []meterNode
}

// collectImages 递归收集每一次图片出现，并把它们的结构价加起来。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:49-61（collectImages）
func collectImages(blocks llm.Content, images []attachment.ImageRef) ([]attachment.ImageRef, int, error) {
	structuralTokens := 0
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.ImageBlock:
			images = append(images, typed.Attachment)
			structural, err := estimateStructuralBlock(block)
			if err != nil {
				return nil, 0, err
			}
			structuralTokens += structural
		case llm.ToolResultBlock:
			nested, nestedTokens, err := collectImages(typed.Content, images)
			if err != nil {
				return nil, 0, err
			}
			images = nested
			structuralTokens += nestedTokens
		}
	}
	return images, structuralTokens, nil
}

// analyzeNode 从一条表面事件派生出的消息造一个带价的节点。
//
// 源: packages/llm/token-meter/src/surface-fold.ts:63-75（analyzeNode）
//
// derived 为假表示这条事件派生不出消息（内容为空、只为携带用量而存在的那条
// assistant/message）：那是一个价钱全为 0、也没有图的节点，但它**照样占一格**。
func analyzeNode(seq int, message llm.Message, derived bool) (meterNode, error) {
	if !derived {
		return meterNode{seq: seq}, nil
	}
	heuristicTokens, err := EstimateMessage(message)
	if err != nil {
		return meterNode{}, err
	}
	images, imageStructuralTokens, err := collectImages(message.Content, nil)
	if err != nil {
		return meterNode{}, err
	}
	return meterNode{
		seq:             seq,
		heuristicTokens: heuristicTokens,
		imageFreeTokens: heuristicTokens - imageStructuralTokens,
		images:          images,
	}, nil
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
//
// baseSeq 是这一段日志的起点，只在一次替换的端点定位不到时用得上，理由见下面
// 那段注释。
func foldSurfaceTokens(nodes []meterNode, event sessionlog.Event, baseSeq int) (surfaceTokenFold, error) {
	operation, eligible, err := sessionlog.SurfaceOpOf(event)
	if err != nil {
		return surfaceTokenFold{}, err
	}
	if !eligible {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 的事件 %q 不上表面，折不进来", event.Seq, event.Type)
	}

	// 第二个返回值为假表示这条事件派生不出消息，那就是 0 个 token——
	// DSH 那边是 `message === null ? 0 : estimateMessage(message)`。
	message, derived, err := sessionlog.DeriveEventMessage(event)
	if err != nil {
		return surfaceTokenFold{}, err
	}
	node, err := analyzeNode(event.Seq, message, derived)
	if err != nil {
		return surfaceTokenFold{}, err
	}
	tokens := node.heuristicTokens

	if operation.SurfaceOpKind() == sessionlog.OpAppend {
		next := make([]meterNode, len(nodes), len(nodes)+1)
		copy(next, nodes)
		next = append(next, node)
		return surfaceTokenFold{tokens: tokens, nodes: next}, nil
	}

	replace, isReplace := operation.(sessionlog.ReplaceOp)
	if !isReplace {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 带的表面操作认不得：%q", event.Seq, operation.SurfaceOpKind())
	}

	// 一个端点定位不到得先分两种（docs/session-log-limit.md 原则第 4 条）：它落在
	// baseSeq 之前，那是被 FIFO 弹掉了，正常损耗；它不小于 baseSeq 却仍然不在表面
	// 上，才是这份节点表算的根本不是当前这个表面——那种情况继续折下去只会把一个
	// 错得离谱的数悄悄传下去，所以照旧在这里断掉。
	//
	// 新增: 这条降级是照 [github.com/snight1983/ds-harness-go/sessionlog] 那份表面
	// 折叠（surface.go 的 replacementRange）已经定下的先例做的。同一件事在本仓库
	// 有两份实现是**故意**的（理由见本文件开头），可它们对「日志会被弹头」的认识
	// 必须一致——只有那一份改了的话，一次压缩过、又活得够久的会话会算得出消息、
	// 却算不出它值多少 token。
	startIndex := indexOfSeq(nodes, replace.Start)
	if startIndex < 0 {
		if replace.Start >= baseSeq {
			return surfaceTokenFold{}, fmt.Errorf(
				"token 表面：seq %d 的替换声明的起始 seq %d 不在表面上",
				event.Seq, replace.Start)
		}
		// 起点被弹掉时区间往前收到表面最前端：现存每个节点的 seq 都不小于 baseSeq，
		// 也就都晚于那个被弹掉的起点，所以它们要么本来就在区间里、要么在它右边，
		// 右边那一半由终点的下标切掉。
		startIndex = 0
	}
	endIndex := indexOfSeq(nodes, replace.End)
	if endIndex < 0 {
		if replace.End >= baseSeq {
			return surfaceTokenFold{}, fmt.Errorf(
				"token 表面：seq %d 的替换声明的结束 seq %d 不在表面上",
				event.Seq, replace.End)
		}
		// 终点也被弹掉了（这时起点必然也被弹掉了），整个区间在表面上一点不剩，
		// 这次替换降级成一次追加：没有节点被换走。
		next := make([]meterNode, len(nodes), len(nodes)+1)
		copy(next, nodes)
		next = append(next, node)
		return surfaceTokenFold{tokens: tokens, nodes: next}, nil
	}
	if startIndex > endIndex {
		return surfaceTokenFold{}, fmt.Errorf(
			"token 表面：seq %d 的替换声明的当前区间 %d-%d 在表面上不成立",
			event.Seq, replace.Start, replace.End)
	}

	next := make([]meterNode, 0, len(nodes)-(endIndex-startIndex))
	next = append(next, nodes[:startIndex]...)
	next = append(next, node)
	next = append(next, nodes[endIndex+1:]...)
	return surfaceTokenFold{tokens: tokens, nodes: next}, nil
}

// indexOfSeq 找出某个 seq 在节点表里的下标，找不到给 -1。
func indexOfSeq(nodes []meterNode, seq int) int {
	for index, node := range nodes {
		if node.seq == seq {
			return index
		}
	}
	return -1
}
