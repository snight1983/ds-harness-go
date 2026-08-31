// 本文件的作用：两个和提供方用量有关的投影单元——累计账单（tokenUsage）和
// 当下占用（contextPressure），以及它们各自的状态形状。
//
// 源: packages/llm/token-meter/src/usage-projection.ts

package tokenmeter

import (
	"encoding/json"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/projection"
)

// TokenUsageProjectionKey 是累计用量那个单元占的投影键。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:120
const TokenUsageProjectionKey = "tokenUsage"

// ContextPressureProjectionKey 是上下文占用那个单元占的投影键。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:175
const ContextPressureProjectionKey = "contextPressure"

// tokenUsageStateVersion 是累计用量状态的作废版本号。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:121
const tokenUsageStateVersion = 1

// contextPressureStateVersion 是上下文占用状态的作废版本号。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:176
//
// 跟着 DSH 从 4 开始：这个键在 DSH 侧已经改过三次形状，而落盘的检查点行是按
// （键，版本）认的。改回 1 会让一批本该被丢掉的旧行重新变得「看起来还能用」。
const contextPressureStateVersion = 4

// usageSample 是某一个回合／步骤上最近一次采到的那份用量。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:56-60
type usageSample struct {
	// Turn 是这次采样属于哪个回合。
	Turn int `json:"turn"`
	// Step 是这次采样属于哪个步骤。
	Step int `json:"step"`
	// Buckets 是这次采样报的那四个桶。
	Buckets TokenUsageView `json:"buckets"`
}

// tokenUsageState 是累计用量单元的状态。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:54-63
type tokenUsageState struct {
	// Totals 是到目前为止的累计。
	Totals TokenUsageView `json:"totals"`
	// Last 是最近一次采样；nil 表示还没有过采样。
	//
	// 只留**一格**，靠的是日志上那条不变量：同一个回合／步骤的多次用量上报
	// 一定是挨着的。一旦下一个步骤开起来了，一份合法的日志就不会再回头报
	// 更早那个步骤的用量。
	Last *usageSample `json:"last"`
}

// contextPressureState 是上下文占用单元的状态。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:95-107
//
// 新增: 三个可选字段用指针加 omitempty，不用零值当缺失。这里必须分得开：
// ContextWindow 为 0 和「没有适配器公告过容量」在界面上是两件事（前者根本不该
// 出现），而 PressureTokens 为 0 和「还没有过任何一次用量采样」更是两件事
// ——后者要让整个占用读数缺席，前者要显示成 0。
type contextPressureState struct {
	// ContextWindow 是最近记下的那条路由的容量。
	ContextWindow *int `json:"contextWindow,omitempty"`
	// PressureTokens 是最近一次采样报的提示词规模。
	PressureTokens *int `json:"pressureTokens,omitempty"`
	// SurfaceTokens 是当前表面的滚动估价。
	SurfaceTokens int `json:"surfaceTokens"`
	// SampledSurfaceTokens 是采到 PressureTokens 那一刻表面的估价。
	//
	// 它是那次采样的**基准**：视图里的投影值就是拿当前表面减掉它，
	// 再加到那次采样上。
	SampledSurfaceTokens *int `json:"sampledSurfaceTokens,omitempty"`
	// Claim 是手上举着的那张影子价认领单，见 [ShadowPriceClaim]。
	Claim *ShadowPriceClaim `json:"claim,omitempty"`
}

// bucketsFrom 把一份提供方用量摊成那四个互不重叠的桶。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:19-24
func bucketsFrom(usage llm.TokenUsage) TokenUsageView {
	return TokenUsageView{
		UncachedInputTokens: usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheWriteTokens:    usage.CacheWriteTokens,
	}
}

// addReplacing 把一份新采样加进累计，同时把同一个步骤上一次采样的那笔减掉。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:32-41
//
// previous 为 nil 表示这是这个步骤的第一次采样，那就只加不减。
func addReplacing(totals TokenUsageView, previous *TokenUsageView, next TokenUsageView) TokenUsageView {
	var back TokenUsageView
	if previous != nil {
		back = *previous
	}
	return TokenUsageView{
		UncachedInputTokens: totals.UncachedInputTokens - back.UncachedInputTokens + next.UncachedInputTokens,
		OutputTokens:        totals.OutputTokens - back.OutputTokens + next.OutputTokens,
		CacheReadTokens:     totals.CacheReadTokens - back.CacheReadTokens + next.CacheReadTokens,
		CacheWriteTokens:    totals.CacheWriteTokens - back.CacheWriteTokens + next.CacheWriteTokens,
	}
}

// pressureFrom 算一次请求的提示词侧压力：输入加缓存读写，**不含输出**。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:76-77
//
// 不含输出，所以这个数在一个回合正在流式产出的整段时间里是不动的，
// 要等下一次请求报回自己的用量才往前走一格。这正是它作为「上下文占用」
// 而不是「本次花费」的原因。
func pressureFrom(usage llm.TokenUsage) int {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
}

// usageOf 取出一条事件为它那个步骤报的用量。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:80-85
//
// 两个来路：流中途的那条用量分块（它早，而且能挺过这次请求后面的失败），
// 以及落定的那条助手消息（它是同一个回合／步骤的最终值）。
func usageOf(event session.Event) (turn int, step int, usage llm.TokenUsage, present bool) {
	switch event.Type {
	case session.EventAssistantChunk:
		var data session.AssistantChunkData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return 0, 0, llm.TokenUsage{}, false
		}
		chunk, isUsage := data.Chunk.(llm.UsageChunk)
		if !isUsage {
			return 0, 0, llm.TokenUsage{}, false
		}
		return data.Turn, data.Step, chunk.Usage, true
	case session.EventAssistantMessage:
		var data session.AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return 0, 0, llm.TokenUsage{}, false
		}
		if data.Usage == nil {
			return 0, 0, llm.TokenUsage{}, false
		}
		return data.Turn, data.Step, *data.Usage, true
	}
	return 0, 0, llm.TokenUsage{}, false
}

// tokenUsageDefinition 是累计用量那个单元。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:119-151
//
// 用量分块给出一份早的采样——它能挺过这次请求后面的失败；落定的助手消息给出
// 同一个回合／步骤的最终采样。同一个步骤的重复上报**替换**掉前一次，
// 而不是叠加上去。
func tokenUsageDefinition() projection.Definition[tokenUsageState] {
	return projection.Definition[tokenUsageState]{
		Key:          TokenUsageProjectionKey,
		StateVersion: tokenUsageStateVersion,
		Init:         func() tokenUsageState { return tokenUsageState{} },
		Apply:        applyTokenUsage,
		DecodeState:  projection.StrictDecoder[tokenUsageState](),
		View:         func(state tokenUsageState) any { return state.Totals },
	}
}

// applyTokenUsage 是累计用量那个纯转移。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:124-149
func applyTokenUsage(state tokenUsageState, event session.Event) (tokenUsageState, bool) {
	turn, step, usage, present := usageOf(event)
	if !present {
		return state, false
	}

	buckets := bucketsFrom(usage)
	var previous *TokenUsageView
	if state.Last != nil && state.Last.Turn == turn && state.Last.Step == step {
		previous = &state.Last.Buckets
	}
	// 同一个步骤重复报了一模一样的一份：什么都没变。不判这一下的话，
	// 一次流中途的用量分块和它后面那条落定消息（两者报的常常逐字相同）
	// 会往变更流上推两条一样的值。
	if previous != nil && *previous == buckets {
		return state, false
	}

	return tokenUsageState{
		Totals: addReplacing(state.Totals, previous, buckets),
		Last:   &usageSample{Turn: turn, Step: step, Buckets: buckets},
	}, true
}

// contextPressureDefinition 是上下文占用那个单元。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:174-219
//
// 两个各自 last-wins 的槽：最新那次用量采样给出分子，最新那条 request/context
// 给出分母。两者都是整值，所以重放顺序本身就决定了结果，跨字段的一致性
// 一句都没有承诺——这一对**明确地不是**一次原子的请求观测，
// 见 [ContextPressureView]。
//
// PressureTokens 只算提示词侧，所以一个回合流着的时候它不动。而**除了请求
// 没有别的东西会报用量**，也就是说它看不见一次压缩；所以这个折叠在它旁边
// 另外滚一份表面总价，视图里发布的 ProjectedTokens 是「那次采样 + 采样之后
// 表面的带符号位移」——回答的是下一次请求，而不是上一次。
//
// 那份总价骑在 [foldSurfaceProjection] 上，所以状态是 O(1) 的。**一次用量采样
// 是在同一条事件进表面之前盖章的**，这样一条 assistant/message 锚的正是它
// 自己那次请求看见的那个表面。
func contextPressureDefinition() projection.Definition[contextPressureState] {
	return projection.Definition[contextPressureState]{
		Key:          ContextPressureProjectionKey,
		StateVersion: contextPressureStateVersion,
		Init:         func() contextPressureState { return contextPressureState{} },
		Apply:        applyContextPressure,
		DecodeState:  projection.StrictDecoder[contextPressureState](),
		View:         contextPressureViewOf,
	}
}

// applyContextPressure 是上下文占用那个纯转移。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:179-208
func applyContextPressure(state contextPressureState, event session.Event) (contextPressureState, bool) {
	fold := foldSurfaceProjectionLenient(state.Claim, event)
	next := state
	changed := false

	if event.Type == session.EventRequestContext {
		var data session.RequestContextData
		if err := json.Unmarshal(event.Data, &data); err == nil {
			// 0 是「这条路由没公告容量」，对应 DSH 那边的 undefined。
			var window *int
			if data.ContextWindow > 0 {
				capacity := data.ContextWindow
				window = &capacity
			}
			if !intPointerEquals(window, next.ContextWindow) {
				next.ContextWindow = window
				changed = true
			}
		}
	}

	if _, _, usage, present := usageOf(event); present {
		pressure := pressureFrom(usage)
		samePressure := next.PressureTokens != nil && *next.PressureTokens == pressure
		// 拿采样基准和**当前**表面比：两者已经一致就说明这次采样落在同一个
		// 表面上，重新盖一次章什么都不会变。
		sameBaseline := next.SampledSurfaceTokens != nil && *next.SampledSurfaceTokens == next.SurfaceTokens
		if !samePressure || !sameBaseline {
			sampled := next.SurfaceTokens
			next.PressureTokens = &pressure
			next.SampledSurfaceTokens = &sampled
			changed = true
		}
	}

	// 盖章在后、进表面在前：上面那一段读的还是这条事件加进来之前的表面总价。
	if fold.deltaTokens != 0 {
		next.SurfaceTokens += fold.deltaTokens
		changed = true
	}

	// 认领单的记账：这条事件之前和之后都没有认领单，就什么都不用动。
	// 否则一律换成这次折叠交出来的那一张（可能是 nil，那就是放掉）。
	if state.Claim != nil || fold.claim != nil {
		next.Claim = fold.claim
		changed = true
	}
	return next, changed
}

// contextPressureViewOf 把占用状态折成给客户端看的那份视图。
//
// 源: packages/llm/token-meter/src/usage-projection.ts:209-218
func contextPressureViewOf(state contextPressureState) any {
	view := ContextPressureView{ContextWindow: state.ContextWindow, PressureTokens: state.PressureTokens}
	if state.PressureTokens != nil && state.SampledSurfaceTokens != nil {
		projected := max(0, *state.PressureTokens+state.SurfaceTokens-*state.SampledSurfaceTokens)
		view.ProjectedTokens = &projected
	}
	return view
}

// intPointerEquals 比较两个可选整数：都没给算相等，一边给了算不等，都给了比值。
//
// 新增: 不能直接比指针——两个各自指向 8192 的指针是不同的地址，
// 但它们说的是同一个容量。成例见 [llm.CallConfig] 那边的 float64PointerEquals。
func intPointerEquals(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
