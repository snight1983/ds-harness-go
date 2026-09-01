// 本文件的作用：上下文组成那个投影单元——系统提示和工具表来自最新那份请求头，
// 对话那一段来自活着的表面。三个数用的是和 [TokenMeter.Measure] **同一套**估算，
// 所以它们和那边的启发式口径逐字对得上。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts

package tokenmeter

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

// ContextBreakdownProjectionKey 是上下文组成那个单元占的投影键。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts:56
const ContextBreakdownProjectionKey = "contextBreakdown"

// contextBreakdownStateVersion 是上下文组成状态的作废版本号。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts:57
const contextBreakdownStateVersion = 2

// contextBreakdownState 是上下文组成单元的状态。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts:26-36
//
// 它是**固定的几个数**，所以落盘的检查点在整个会话生命周期里都是 O(1) 的。
// 这正是它和服务那边逐节点折叠分家的理由，见 surfaceprojection.go 的文件注释。
type contextBreakdownState struct {
	// SystemTokens 是最新那份请求头里系统提示的估价。
	SystemTokens int `json:"systemTokens"`
	// ToolsTokens 是最新那份请求头里工具表的估价。
	ToolsTokens int `json:"toolsTokens"`
	// MessageTokens 是当前这条模型可见表面的估价。
	MessageTokens int `json:"messageTokens"`
	// Claim 是手上举着的那张影子价认领单，见 [ShadowPriceClaim]。
	Claim *ShadowPriceClaim `json:"claim,omitempty"`
}

// contextBreakdownDefinition 是上下文组成那个单元。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts:55-85
//
// 信封那两个数按 request/header last-wins；消息那个数骑在
// [foldSurfaceProjection] 上——和占用那个单元用的是同一份 O(1) 折叠——所以在
// 一份完整走过影子价协议的日志上，它在**每一个事件边界**都等于
// [Measurement.SurfaceTokens]，而一次压缩会按日志里记下的影子价把它缩下去。
// 一次没有认领单的替换保持原值不动。
func contextBreakdownDefinition() projection.Definition[contextBreakdownState] {
	return projection.Definition[contextBreakdownState]{
		Key:          ContextBreakdownProjectionKey,
		StateVersion: contextBreakdownStateVersion,
		Init:         func() contextBreakdownState { return contextBreakdownState{} },
		Apply:        applyContextBreakdown,
		DecodeState:  projection.StrictDecoder[contextBreakdownState](),
		View: func(state contextBreakdownState) any {
			return ContextBreakdownView{
				SystemTokens:  state.SystemTokens,
				ToolsTokens:   state.ToolsTokens,
				MessageTokens: state.MessageTokens,
			}
		},
	}
}

// applyContextBreakdown 是上下文组成那个纯转移。
//
// 源: packages/llm/token-meter/src/breakdown-projection.ts:60-80
func applyContextBreakdown(state contextBreakdownState, event session.Event) (contextBreakdownState, bool) {
	fold := foldSurfaceProjectionLenient(state.Claim, event)
	next := state

	if event.Type == session.EventRequestHeader {
		var data session.RequestHeaderData
		if err := json.Unmarshal(event.Data, &data); err == nil {
			// 先规范化再估价：一份「工具表是空数组」的头和一份「没有工具表」的头
			// 说的是同一件事，不先对齐的话同一次请求会因为写法不同算出两个数。
			header := session.CanonicalHeader(data.Header)
			// 工具表排不成 JSON 只可能来自一份已经坏掉的日志。投影的折叠不能报错
			// （理由见 [foldSurfaceProjectionLenient]），所以这里保持上一份估价
			// ——把它归零会让界面显示成「这次请求没带工具」，那比偏一点严重得多。
			if tools, err := EstimateToolsTokens(header); err == nil {
				next.SystemTokens = EstimateSystemTokens(header)
				next.ToolsTokens = tools
			}
		}
	}

	next.MessageTokens += fold.deltaTokens
	next.Claim = fold.claim

	changed := next.SystemTokens != state.SystemTokens ||
		next.ToolsTokens != state.ToolsTokens ||
		fold.deltaTokens != 0 ||
		state.Claim != nil || fold.claim != nil
	return next, changed
}
