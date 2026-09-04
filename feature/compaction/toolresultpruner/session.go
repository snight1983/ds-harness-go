// 本文件的作用：在一份稳定的表面快照上把每一条超预算的工具结果真的砍一遍——
// 每条替换前面同步地贴一条 compaction/prune 记下被遮节点的估价。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:136-183

package toolresultpruner

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Estimator 是本包要的那一小片计量器：给一条消息定个价。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:46
//
//	（static inject = ['tokenMeter']）
//
// 新增: DSH 从 cordis 上取整个 `ctx.tokenMeter` 服务，[Pruner.PruneSession] 实际
// 只调 `estimateMessage()` 一个方法。这里摆成一个单方法接口明着传进来，做法和
// compaction/basic 里 [basic.Meter]、[basic.Streamer] 那两处逐字相同：签名照着真
// 计量器写，于是它结构上就满足这个接口，装配方直接填进去，不用现包一层适配。
//
// 它是 [Pruner.PruneSession] 的参数而不是 [Pruner] 的字段，因为 [Pruner] 剩下
// 那三个方法一个都用不上它——挂成字段就等于让 [New] 的调用方为了砍一段文本先
// 备一个计量器。
type Estimator interface {
	// EstimateMessage 按那把固定的尺子给一条消息估个价。
	EstimateMessage(message llm.Message) (int, error)
}

// PruneSession 在一份稳定的表面快照上，把每一条超预算的工具结果都砍一遍。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:136-183
//
// 每次替换保留原事件负载的其余部分，只换掉那条工具结果块的正文，并把被遮的那个
// 节点列进 sourceEventSeqs——重放因此拿得回这次替换的输入。替换前面**同步地**
// 紧贴一条 compaction/prune，它按 estimator 给出被遮节点的估价：这就是那条共用的
// 影子价格约定里不过模型的那一半，纯消费方靠「紧挨着」这个相邻关系把价钱减掉，
// 不用自己给每个节点存一份状态。所以这两条中间一个字都不许插。
//
// 快照是**先照完再动手**的：先把当前表面上的工具结果全数记下来，再一条条替换。
// 边遍历边追加会让新写进去的替换件自己也进遍历范围，而它按定义已经在预算之内了。
//
// 中途某一条写不进去时前面已经落地的那些**留着不回滚**，理由和 compaction/basic
// 那条事务相同：做砸了的那一半本身就是要在日志里看得见的。所以出错时第一个返回值
// 仍然是到那一刻为止的真实账目，不是零值。
func (p *Pruner) PruneSession(
	live *coresession.Session,
	estimator Estimator,
) (PruneResult, error) {
	events := live.Events()
	base := baseSeqOf(events)

	type candidate struct {
		seq   int
		data  sessionlog.ToolResultData
		block llm.ToolResultBlock
	}
	candidates := make([]candidate, 0, len(events))
	for _, seq := range live.SurfaceNodes() {
		index := seq - base
		// 表面上的 seq 是验过的、连续的日志引用，所以这一条报不出来。留着而不是
		// 断言掉：真对不上的话下面会拿错一条事件去当被砍的目标，而那是**静默**的。
		if index < 0 || index >= len(events) || events[index].Seq != seq {
			return PruneResult{}, fmt.Errorf(
				"%w：表面上的 seq %d 在日志里找不到对应的事件", compaction.ErrSurfaceCorrupt, seq)
		}
		event := events[index]
		if event.Type != sessionlog.EventToolResult {
			continue
		}
		decoded, err := sessionlog.DecodeData(event)
		if err != nil {
			return PruneResult{}, fmt.Errorf("%w：seq %d 的 tool/result：%w",
				compaction.ErrMalformedEvent, seq, err)
		}
		data, ok := decoded.(sessionlog.ToolResultData)
		if !ok {
			// 不可达：[sessionlog.DecodeData] 按 Type 分发，tool/result 只会得到这一种
			// 负载。理由和 compaction/basic 里 decodeTurnStart 那一处相同。
			return PruneResult{}, fmt.Errorf("%w：seq %d 声称是 tool/result，负载却是 %T",
				compaction.ErrMalformedEvent, seq, decoded)
		}
		// DSH 直接 `message.content[0]` 取那唯一一块。这里查一遍类型：一条
		// tool/result 的消息按 [llm.NewToolResultMessage] 只有一块结果块，
		// 形状不对就说明这条事件不是那个构造器产出的，跳过比猜安全。
		if len(data.Message.Content) != 1 {
			continue
		}
		block, ok := data.Message.Content[0].(llm.ToolResultBlock)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{seq: seq, data: data, block: block})
	}

	result := PruneResult{}
	for _, item := range candidates {
		content, pruned, err := p.PruneContent(item.block.Content)
		if err != nil {
			return result, err
		}
		if !pruned {
			continue
		}
		charsBefore := p.MeasureContent(item.block.Content)
		charsAfter := p.MeasureContent(content)

		shadowedTokens, err := estimator.EstimateMessage(item.data.Message)
		if err != nil {
			return result, fmt.Errorf("compaction/toolresultpruner：seq %d 的估价算不出来：%w",
				item.seq, err)
		}
		if _, err := appendPayload(live, compaction.EventCompactionPrune, compaction.PruneData{
			ShadowedRange:      compaction.ShadowedRange{Start: item.seq, End: item.seq},
			ShadowedSeqs:       []int{item.seq},
			ShadowedTokenCount: shadowedTokens,
		}); err != nil {
			return result, err
		}

		replaced := item.data
		replaced.Message = item.data.Message.Clone()
		block := item.block
		block.Content = content
		replaced.Message.Content = llm.Content{block}
		replacement, err := appendPayloadWith(live, sessionlog.EventToolResult, replaced,
			sessionlog.ReplaceOp{Start: item.seq, End: item.seq}, []int{item.seq})
		if err != nil {
			return result, err
		}

		result.Pruned = append(result.Pruned, PrunedEntry{
			OriginalSeq:    item.seq,
			ReplacementSeq: replacement.Seq,
			CallID:         item.block.ToolCallID,
			CharsBefore:    charsBefore,
			CharsAfter:     charsAfter,
		})
		result.CharsRemoved += charsBefore - charsAfter
	}
	return result, nil
}

// baseSeqOf 交出这段日志头一条事件的 seq。
//
// 新增: DSH 直接 `session.events[seq]`——它那边 seq 就是数组下标，因为它总是从头
// 持有全部事件。本仓库的会话可能只拿着一段后缀（[coresession.Session.FirstLiveSeq]），
// 所以下标要先减掉这个基准。
func baseSeqOf(events []sessionlog.Event) int {
	if len(events) == 0 {
		return 0
	}
	return events[0].Seq
}

// appendPayload 排一条不上表面的事件并追加进去。
func appendPayload(live *coresession.Session, kind sessionlog.EventType, payload any) (sessionlog.Event, error) {
	return appendPayloadWith(live, kind, payload, nil, nil)
}

// appendPayloadWith 排一条带表面动作的事件并追加进去。
//
// 排不出去和写不进去分成两条诊断：前者是本包自己排的负载有问题，
// 后者是这段会话拒了它——两种的排查方向完全不同。
func appendPayloadWith(
	live *coresession.Session,
	kind sessionlog.EventType,
	payload any,
	op sessionlog.SurfaceOp,
	sources []int,
) (sessionlog.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("compaction/toolresultpruner：%s 的负载排不出去：%w", kind, err)
	}
	event, err := live.Append(sessionlog.Event{
		Type:            kind,
		Data:            data,
		SurfaceOp:       op,
		SourceEventSeqs: sources,
	})
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("compaction/toolresultpruner：%s 写不进日志：%w", kind, err)
	}
	return event, nil
}
