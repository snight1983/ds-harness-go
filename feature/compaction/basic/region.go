// 本文件的作用：这一层里两件**只读**的事——从表面上挑出该压哪一段，
// 以及从日志里读出「现在还有没有一次压缩开着」。
//
// 源: packages/compaction/compaction-basic/src/region.ts

package basic

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// PricedNode 是表面上一个节点和它在计量器那把固定尺子下的估价。
//
// 新增: DSH 收一整份 `TokenMeasurement`（token-meter 那个包的类型），而
// [SelectCompactableRange] 只用到里面 `nodes` 那一段的 seq 和 tokens 两个字段。
// token-meter 归 docs/DESIGN.md 第八节第 6 块，从这里去 import 它会把移植顺序
// 倒过来；按本仓库「消费方自己声明它需要的那一小片」的一贯做法（成例是
// compaction.SurfaceView），现声明这个两字段的值。
type PricedNode struct {
	// Seq 是这个表面节点对应事件的 seq。
	Seq int
	// Tokens 是它的估价。
	Tokens int
}

// SelectCompactableRange 挑出下一段该压的表面区间：从表面头上起，一直到
// 「再往后就动到要保留的尾巴了」为止，并且那一刀不许把一次工具调用和它的结果劈开。
//
// 源: packages/compaction/compaction-basic/src/region.ts:98-134
//
// priced 必须和 view.Nodes 一一对上——那是同一个表面的两种视角，对不上说明
// 计量器算的是另一份表面，这时报 [compaction.ErrSurfaceCorrupt]。
//
// 第二个返回值为假表示**没有可压的区间**：要么表面是空的，要么整个表面都在
// 保留预算里，要么往前找不到一处配平的下刀点。这三种都不是错误。
func SelectCompactableRange(
	view compaction.SurfaceView,
	balance *compaction.BalanceIndex,
	priced []PricedNode,
	retainTokens int,
) (compaction.ShadowedRange, bool, error) {
	if len(priced) == 0 {
		return compaction.ShadowedRange{}, false, nil
	}
	if len(view.Nodes) != len(priced) {
		return compaction.ShadowedRange{}, false, fmt.Errorf(
			"%w：计量器算的是 %d 个表面节点，当前表面上有 %d 个",
			compaction.ErrSurfaceCorrupt, len(priced), len(view.Nodes))
	}
	for index, node := range priced {
		if view.Nodes[index] != node.Seq {
			return compaction.ShadowedRange{}, false, fmt.Errorf(
				"%w：表面第 %d 个节点是 seq %d，计量器算的是 seq %d",
				compaction.ErrSurfaceCorrupt, index, view.Nodes[index], node.Seq)
		}
	}

	// 从尾巴往回攒，攒够保留预算就停：keepFromIdx 是第一个要留下的节点。
	// 至少走一轮，所以 retainTokens 为 0（超窗补救那一路）时也会留下最后一个节点。
	accumulated := 0
	keepFromIdx := len(priced)
	for index := len(priced) - 1; index >= 0; index-- {
		accumulated += priced[index].Tokens
		keepFromIdx = index
		if accumulated >= retainTokens {
			break
		}
	}
	if keepFromIdx == 0 {
		return compaction.ShadowedRange{}, false, nil
	}

	// 往前退到一处配平的下刀点。退到头都不配平，说明整个表面上唯一一刀
	// 就是表面头，那等于什么都不压。
	for keepFromIdx > 0 {
		balanced, err := balance.BalancedBefore(view, view.Nodes[keepFromIdx])
		if err != nil {
			return compaction.ShadowedRange{}, false, err
		}
		if balanced {
			break
		}
		keepFromIdx--
	}
	if keepFromIdx == 0 {
		return compaction.ShadowedRange{}, false, nil
	}

	return compaction.ShadowedRange{
		Start: view.Nodes[0],
		End:   view.Nodes[keepFromIdx-1],
	}, true, nil
}

// EntryState 是一次压缩开工之前要从日志尾巴上读出来的三件事。
//
// 源: packages/compaction/compaction-basic/src/region.ts:64-68
//
// 新增: DSH 的 `unmatchedCompactionStart` 装的是整条事件，而它只被读了 `.seq`。
// 这里只留 seq，多带一个布尔表示有没有——理由和 compaction.StartData.Standalone
// 那一处相同：拿 0 当「没有」在一段从头开始的日志里恰好会撞车（seq 0 是合法的）。
type EntryState struct {
	// OpenTurn 是当前开着的回合号；TurnIsOpen 为假时无意义。
	OpenTurn int
	// TurnIsOpen 表示日志尾巴上有一个还没关的回合。
	TurnIsOpen bool
	// UnmatchedStartSeq 是最近一条没有配对 end 的 compaction/start 的 seq；
	// HasUnmatchedStart 为假时无意义。
	UnmatchedStartSeq int
	// HasUnmatchedStart 表示有一个压缩括号还开着。
	HasUnmatchedStart bool
	// LatestEndSeedSeq 是最近一道 session/end-seed 的 seq；HasEndSeed 为假时无意义。
	LatestEndSeedSeq int
	// HasEndSeed 表示这段日志里有过种子边界。
	HasEndSeed bool
}

// InspectEntryState 从日志尾巴往回扫，读出开着的回合、开着的压缩括号、
// 和最近一道种子边界。
//
// 源: packages/compaction/compaction-basic/src/region.ts:517-550
//
// 三件事各自独立地停：回合状态看第一条 turn/start 或 turn/end 就定了，
// 压缩状态看第一条 compaction/start 或 compaction/end 就定了，
// 种子边界看第一条就定了。三样都定下来才提前收工。
func InspectEntryState(events []sessionlog.Event) (EntryState, error) {
	var state EntryState
	turnKnown, compactionKnown := false, false
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if !state.HasEndSeed && event.Type == sessionlog.EventSessionEndSeed {
			state.LatestEndSeedSeq, state.HasEndSeed = event.Seq, true
		}
		if !compactionKnown {
			switch event.Type {
			case compaction.EventCompactionStart:
				state.UnmatchedStartSeq, state.HasUnmatchedStart = event.Seq, true
				compactionKnown = true
			case compaction.EventCompactionEnd:
				compactionKnown = true
			}
		}
		if !turnKnown {
			switch event.Type {
			case sessionlog.EventTurnStart:
				turn, err := decodeTurnStart(event)
				if err != nil {
					return EntryState{}, err
				}
				state.OpenTurn, state.TurnIsOpen = turn, true
				turnKnown = true
			case sessionlog.EventTurnEnd:
				turnKnown = true
			}
		}
		if turnKnown && compactionKnown && state.HasEndSeed {
			break
		}
	}
	return state, nil
}

// CheckNoActiveCompaction 拒掉一次「上一个压缩括号还开着」的开工。
//
// 源: packages/compaction/compaction-basic/src/region.ts:302-314（assertNoActiveCompaction）
//
// 例外是那个开着的括号排在最近一道 session/end-seed **之前**：种子边界之前的
// 日志是继承来的，那边的括号在这一侧永远等不到它的 compaction/end，
// 不该把当前这个会话一直锁着。
//
// stage 只进错误文案，说明是哪一步撞上了这把锁。
func CheckNoActiveCompaction(events []sessionlog.Event, stage string) error {
	state, err := InspectEntryState(events)
	if err != nil {
		return err
	}
	return state.CheckInactive(stage)
}

// CheckInactive 是 [CheckNoActiveCompaction] 里判定的那一半，
// 给已经读过一次日志尾巴的调用方复用。
//
// 源: packages/compaction/compaction-basic/src/region.ts:286-298
func (s EntryState) CheckInactive(stage string) error {
	if !s.HasUnmatchedStart {
		return nil
	}
	if s.HasEndSeed && s.LatestEndSeedSeq > s.UnmatchedStartSeq {
		return nil
	}
	return compaction.NewManualError(compaction.ManualErrorBusy,
		fmt.Sprintf("%s：seq %d 那次压缩还开着，会话的压缩锁没放", stage, s.UnmatchedStartSeq), nil)
}

// decodeTurnStart 读回一条 turn/start 的回合号。
func decodeTurnStart(event sessionlog.Event) (int, error) {
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		return 0, fmt.Errorf("%w：seq %d 的 turn/start：%w",
			compaction.ErrMalformedEvent, event.Seq, err)
	}
	start, ok := data.(sessionlog.TurnStartData)
	if !ok {
		// 不可达：[sessionlog.DecodeData] 按 Type 分发，turn/start 只会得到这一种负载。
		// 留着它而不是断言掉，理由和 compaction.decodeTurnStart 那一处相同——
		// 看漏一条 turn/start 会让这里把一个开着的回合读成「没有回合」，
		// 于是一次自动压缩被当成人工的独立事务，而日志本身读得回来。
		return 0, fmt.Errorf("%w：seq %d 声称是 turn/start，负载却是 %T",
			compaction.ErrMalformedEvent, event.Seq, data)
	}
	return start.Turn, nil
}
