// 本文件的作用：从**一个已经结束的回合**那一段日志里，折出提供方亲口报过的
// 精确账——这一个回合发过几次请求、每一次各花了多少、走的是哪几条路由。
//
// 它和这个包别的东西是两条路。别处量的是「整份日志此刻值多少」，用的是那把固定
// 尺子，答案永远是**估**的；这里一个字都不估，只把提供方报回来的数加起来，
// 任何一处证不出来就整份不给。
//
// 源: packages/llm/token-meter/src/turn-usage.ts

package tokenmeter

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/feature/llmretry"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TurnUsageRoute 是一次计费尝试走的那条提供方／模型路由。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:5-9（TurnTokenUsageRoute）
type TurnUsageRoute struct {
	// Provider 是提供方路由键。
	Provider string
	// Model 是提供方的模型标识。
	Model string
}

// TurnUsage 是一个回合里**每一次尝试**的提供方精确记账之和。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:11-26（TurnTokenUsage）
//
// 「每一次尝试」是字面意思：一次重试过的步骤会把两次请求都算进来，因为两次都
// 真的计过费。
//
// 新增: DSH 那边 CacheRead／CacheWrite／Reasoning 三个字段是**可选**的，含义是
// 「每一次尝试都报了这个桶才给」。Go 的 [llm.TokenUsage] 三个桶都是普通的 int、
// 没有缺失态（那条取舍记在它自己的字段说明上），所以「有没有报过」这件事在这里
// 根本表达不出来，三个数于是无条件都在。Routes 那一条保留可选：来路是消息上的
// [llm.ModelSource]，那**是**一个真的可能缺的东西。
type TurnUsage struct {
	// UncachedInputTokens 是所有尝试没命中缓存的输入之和。
	UncachedInputTokens int
	// OutputTokens 是所有尝试的输出之和。
	OutputTokens int
	// TotalTokens 是所有尝试「提示词加输出」的精确总和。
	TotalTokens int
	// CacheReadTokens 是所有尝试从缓存读到的输入之和。
	CacheReadTokens int
	// CacheWriteTokens 是所有尝试写进缓存的输入之和。
	CacheWriteTokens int
	// ReasoningTokens 是所有尝试的推理 token 之和，它是输出的一个子集。
	ReasoningTokens int
	// Routes 是这些尝试走过的那些路由，去重后按首次出现排序。
	//
	// 只有**每一次**计费尝试都能说清自己走的哪条路由时才有；有一次说不清就是 nil。
	// 半张路由表比没有更糟：读的人会以为那就是全部。
	Routes []TurnUsageRoute
}

// turnAttempt 是一次尝试规整之后的记账。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:28-36（NormalizedAttempt）
type turnAttempt struct {
	usage llm.TokenUsage
	// route 是这次尝试的路由；hasRoute 为假时无意义。
	route    TurnUsageRoute
	hasRoute bool
}

// attemptPhase 是一次尝试在生命周期里的位置。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:38-56（AttemptState）
type attemptPhase int

const (
	// attemptIdle 表示当下没有尝试开着。
	attemptIdle attemptPhase = iota
	// attemptOpen 表示一次尝试开着，还没落定。
	attemptOpen
	// attemptFinishClosed 表示一次尝试被一个失败／中止的 finish 分块收了账，
	// 但那个步骤还没关。
	attemptFinishClosed
	// attemptSettled 表示这次尝试的账已经落定，落定方式见 settledByRetry。
	attemptSettled
)

// attemptState 是这台状态机当下的样子。
type attemptState struct {
	phase attemptPhase
	turn  int
	step  int
	// sample 是这次尝试到目前为止收到的用量；nil 表示还没收到。
	sample *llm.TokenUsage
	// settledByRetry 为真表示这次尝试是被一条 llm/retry 落定的，
	// 也就是**只有它**允许一条 llm/retry-started 把同一次尝试重新打开。
	settledByRetry bool
}

// sameAttempt 判断状态机盯着的是不是这条事件说的那次尝试。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:156-162（sameAttempt）
func (s attemptState) sameAttempt(turn, step int) bool {
	return s.turn == turn && s.step == step
}

// normalizeTurnUsage 把一份提供方用量规整成一次尝试的记账，不成立时第二个返回值为假。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:76-119（normalizeUsage）
//
// 新增: DSH 那个函数一大半在对账**缺失的桶**和一个提供方自报的 totalTokens：
// 缓存桶缺了就要靠 totalTokens 反推提示词侧，两样都缺就不给。Go 的
// [llm.TokenUsage] 五个桶都在、且没有 totalTokens 这个字段，那半边逻辑因此
// 没有产出方——精确总量在这里只能是四个互不重叠的桶之和，没有第二个说法可以
// 拿来对账。留下的是那些**在 Go 里仍然可能不成立**的检查：负数，以及推理
// token 比输出还多。
func normalizeTurnUsage(usage llm.TokenUsage, route TurnUsageRoute, hasRoute bool) (turnAttempt, bool) {
	switch {
	case usage.InputTokens < 0, usage.OutputTokens < 0,
		usage.CacheReadTokens < 0, usage.CacheWriteTokens < 0, usage.ReasoningTokens < 0:
		return turnAttempt{}, false
	case usage.ReasoningTokens > usage.OutputTokens:
		// 推理 token 是输出的子集，比输出还多说明这份记账自相矛盾。
		return turnAttempt{}, false
	}
	return turnAttempt{usage: usage, route: route, hasRoute: hasRoute}, true
}

// messageRoute 从一条助手消息的来源上读出它走的那条路由。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:71-74（messageRoute）
//
// 只有模型来源、且提供方和模型两样都不是空串时才算说清了。
func messageRoute(message llm.Message) (TurnUsageRoute, bool) {
	model, ok := message.Source.(llm.ModelSource)
	if !ok || model.Provider == "" || model.Model == "" {
		return TurnUsageRoute{}, false
	}
	return TurnUsageRoute{Provider: model.Provider, Model: model.Model}, true
}

// aggregateAttempts 把一个回合里每一次尝试的记账加起来。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:121-154（aggregateAttempts）
//
// 新增: DSH 每一步加法都过一遍 safeSum，因为 JS 的数超过 2^53 就不再精确。
// Go 的 int 是 64 位精确整数，这些计数又都先验过非负，要溢出得有约 9.2×10^18 个
// token，所以那道检查在这里没有产出方。
func aggregateAttempts(attempts []turnAttempt) (TurnUsage, bool) {
	if len(attempts) == 0 {
		return TurnUsage{}, false
	}

	var usage TurnUsage
	attributed := true
	seen := map[TurnUsageRoute]struct{}{}
	for _, attempt := range attempts {
		usage.UncachedInputTokens += attempt.usage.InputTokens
		usage.OutputTokens += attempt.usage.OutputTokens
		usage.CacheReadTokens += attempt.usage.CacheReadTokens
		usage.CacheWriteTokens += attempt.usage.CacheWriteTokens
		usage.ReasoningTokens += attempt.usage.ReasoningTokens
		if !attempt.hasRoute {
			attributed = false
			continue
		}
		if _, duplicate := seen[attempt.route]; !duplicate {
			seen[attempt.route] = struct{}{}
			usage.Routes = append(usage.Routes, attempt.route)
		}
	}
	// 四个桶互不重叠（见 [llm.TokenUsage]），推理那一份已经含在输出里，不另加。
	usage.TotalTokens = usage.UncachedInputTokens + usage.CacheReadTokens +
		usage.CacheWriteTokens + usage.OutputTokens
	if !attributed {
		usage.Routes = nil
	}
	return usage, true
}

// DeriveTurnUsage 把一个完整回合的尝试生命周期折成一份精确记账。
//
// 源: packages/llm/token-meter/src/turn-usage.ts:173-271（deriveTurnTokenUsage）
//
// events 是**回合本地**的那一段日志，从 turn/start 到 turn/end 为止。第二个返回值
// 为假表示这份账证不出来。
//
// 它对残缺**一点都不宽容**，这是有意的：一次尝试从来不从一份用量采样反推出来，
// 少一条生命周期边界、多一条对不上号的事件、或者哪一次尝试压根没报用量，整份
// 就不给。这个函数存在的全部理由是「这个数是提供方亲口说的」，一旦允许它在缺了
// 一角时给个差不多的数，读它的人就再没有办法把它和别处那些估算区分开了。
//
// 一次重试因此是**两次计费尝试**：llm/retry 把前一次落定，llm/retry-started 把
// 同一个步骤重新打开，两次的账都要报、也都会被加进来。
func DeriveTurnUsage(events []sessionlog.Event) (TurnUsage, bool) {
	var (
		state    attemptState
		attempts []turnAttempt
		turn     int
		hasTurn  bool
		sawEnd   bool
	)

	// closeOpen 把当前这次开着的尝试收账。第二个返回值为假就是整份不给：
	// 一次开着的尝试要落定却没有用量可收，那正是「证不出来」。
	closeOpen := func(route TurnUsageRoute, hasRoute bool) bool {
		if state.phase != attemptOpen || state.sample == nil {
			return false
		}
		attempt, ok := normalizeTurnUsage(*state.sample, route, hasRoute)
		if !ok {
			return false
		}
		attempts = append(attempts, attempt)
		return true
	}

	for _, event := range events {
		if event.Type == sessionlog.EventTurnStart {
			var data sessionlog.TurnStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if hasTurn || state.phase != attemptIdle {
				return TurnUsage{}, false
			}
			turn, hasTurn = data.Turn, true
			continue
		}
		if !hasTurn {
			return TurnUsage{}, false
		}

		switch event.Type {
		case sessionlog.EventTurnEnd:
			var data sessionlog.TurnEndData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase != attemptIdle || sawEnd {
				return TurnUsage{}, false
			}
			sawEnd = true
			continue
		}
		// turn/end 之后还有事件说明这一段根本不是一个回合。
		if sawEnd {
			return TurnUsage{}, false
		}

		switch event.Type {
		case sessionlog.EventStepStart:
			var data sessionlog.StepStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase != attemptIdle {
				return TurnUsage{}, false
			}
			state = attemptState{phase: attemptOpen, turn: turn, step: data.Step}

		case llmretry.EventRetryStarted:
			data, err := llmretry.DecodeRetryStarted(event)
			if err != nil {
				return TurnUsage{}, false
			}
			// 只有被 llm/retry 落定过的那次尝试才允许被重新打开：一次已经交出
			// 助手消息的尝试再被「重开」，说明这段日志的重试链和步骤对不上号。
			if data.Turn != turn || state.phase != attemptSettled || !state.settledByRetry ||
				!state.sameAttempt(data.Turn, data.Step) {
				return TurnUsage{}, false
			}
			state = attemptState{phase: attemptOpen, turn: turn, step: data.Step}

		case sessionlog.EventAssistantChunk:
			var data sessionlog.AssistantChunkData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase != attemptOpen ||
				!state.sameAttempt(data.Turn, data.Step) {
				return TurnUsage{}, false
			}
			switch chunk := data.Chunk.(type) {
			case llm.UsageChunk:
				sample := chunk.Usage
				state.sample = &sample
			case llm.FinishChunk:
				// 失败或中止的收尾就是这次尝试的终点：它照样计费，而后面不会再有
				// 助手消息来落定它。正常收尾不在这里收账，等那条助手消息。
				kind := chunk.Reason.FinishKind()
				if kind != llm.FinishError && kind != llm.FinishAborted {
					continue
				}
				if !closeOpen(TurnUsageRoute{}, false) {
					return TurnUsage{}, false
				}
				state = attemptState{phase: attemptFinishClosed, turn: turn, step: data.Step}
			}

		case sessionlog.EventAssistantMessage:
			var data sessionlog.AssistantMessageData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase != attemptOpen ||
				!state.sameAttempt(data.Turn, data.Step) {
				return TurnUsage{}, false
			}
			// 落定的那条消息上带的用量比流里那份晚、也更权威，盖掉它。
			if data.Usage != nil {
				sample := *data.Usage
				state.sample = &sample
			}
			route, hasRoute := messageRoute(data.Message)
			if !closeOpen(route, hasRoute) {
				return TurnUsage{}, false
			}
			state = attemptState{phase: attemptSettled, turn: turn, step: data.Step}

		case llmretry.EventRetry:
			data, err := llmretry.DecodeRetry(event)
			if err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase == attemptIdle ||
				!state.sameAttempt(data.Turn, data.Step) {
				return TurnUsage{}, false
			}
			// 一次已经落定的尝试不该再排重试；一次还开着的要在这里收账——
			// 那次请求失败了，可它照样计过费。
			if state.phase == attemptSettled {
				return TurnUsage{}, false
			}
			if state.phase == attemptOpen && !closeOpen(TurnUsageRoute{}, false) {
				return TurnUsage{}, false
			}
			state = attemptState{
				phase: attemptSettled, turn: turn, step: data.Step, settledByRetry: true,
			}

		case sessionlog.EventStepEnd:
			var data sessionlog.StepEndData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return TurnUsage{}, false
			}
			if data.Turn != turn || state.phase == attemptIdle ||
				!state.sameAttempt(data.Turn, data.Step) {
				return TurnUsage{}, false
			}
			if state.phase == attemptOpen && !closeOpen(TurnUsageRoute{}, false) {
				return TurnUsage{}, false
			}
			state = attemptState{phase: attemptIdle}
		}
	}

	if !sawEnd || state.phase != attemptIdle {
		return TurnUsage{}, false
	}
	return aggregateAttempts(attempts)
}
