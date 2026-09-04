// 本文件的作用：会话数字这个单元的全部内容——端出去的那份数字、折叠过程中
// 攒着的那些在途边界，以及把一条条事件折进去的那个纯转移。
//
// 源: packages/session/session-stats/src/projection.ts

package sessionstats

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// Key 是这个单元占的那个投影键。
//
// 源: packages/session/session-stats/src/projection.ts:113
const Key = "sessionStats"

// StateVersion 是落盘状态的作废版本号，见 [projection.Definition.StateVersion]。
//
// 源: packages/session/session-stats/src/projection.ts:114
const StateVersion = 1

// Figures 是整份日志折出来的那组会话数字，也就是客户端拿到的视图。
//
// 源: packages/session/session-stats/src/types.ts:22-39
//
// 它和客户端窗口那份折叠逐字段同名：一套没装这个单元的装配可以整块回退到
// 那边去算，不需要把字段名再翻译一遍。
type Figures struct {
	// Turns 是至少关掉过一个步骤的回合数。被拒掉的、空的回合不算。
	Turns int `json:"turns"`

	// Steps 是关掉的步骤数（[sessionlog.EventStepEnd] 的条数）——跑完的、失败的、
	// 被取消的一律各算一个。
	Steps int `json:"steps"`

	// LLMMs 是模型墙上时间的总和，毫秒：每个装配出了消息的步骤，
	// 从 [sessionlog.EventStepStart] 到 [sessionlog.EventAssistantMessage] 那一段。
	LLMMs int64 `json:"llmMs"`

	// ToolMs 是工具墙上时间的总和，毫秒：按 callId 配上对的
	// [sessionlog.EventToolCall] → [sessionlog.EventToolResult] 那一段。
	ToolMs int64 `json:"toolMs"`

	// TTFTMs 是首字延迟的总和，毫秒：步骤开始到第一个非空增量分块那一段，
	// 在 [Figures.TTFTSteps] 个步骤上累加。
	TTFTMs int64 `json:"ttftMs"`

	// TTFTSteps 是记下了首字时刻的步骤数。
	TTFTSteps int `json:"ttftSteps"`

	// DecodeMs 是解码墙上时间的总和，毫秒：首字到装配好的消息那一段，
	// 只在同时报了输出 token 的步骤上累加。
	DecodeMs int64 `json:"decodeMs"`

	// DecodeTokens 是同一批步骤上提供方报的输出 token 总和。
	DecodeTokens int `json:"decodeTokens"`
}

// OpenStep 是当前那个还开着的步骤的边界事实。
//
// 源: packages/session/session-stats/src/projection.ts:60
type OpenStep struct {
	// Turn 是这个步骤所属的回合号。
	Turn int `json:"turn"`
	// Step 是这个步骤号。
	Step int `json:"step"`
	// StartTime 是 [sessionlog.EventStepStart] 那条事件的墙上时刻。
	StartTime int64 `json:"startTime"`
	// FirstTokenTime 是这个步骤第一个非空增量分块的墙上时刻；nil 表示还没有。
	//
	// 新增: DSH 那边是 `number | null`，Go 里用指针表达同一件事——零毫秒是一个
	// 合法时刻，不能拿它当「没有」。
	FirstTokenTime *int64 `json:"firstTokenTime"`
}

// State 是折叠状态：那组数字，加上它们累加所依赖的在途边界。
//
// 源: packages/session/session-stats/src/projection.ts:56-63
//
// [Figures] 是内嵌的，所以它那八个字段在介质上是平铺的，和 DSH 那份
// `interface SessionStatsState extends SessionStatsTotals` 排出来的字节一致。
type State struct {
	Figures

	// LastTurn 是最后一个被数进去的 [sessionlog.EventStepEnd] 所属的回合号；
	// nil 表示还没数过任何步骤。
	//
	// 回合号由宿主指派、在一个会话内单调，所以「这是不是一个新回合的第一个
	// 关闭步骤」只要一个槽就判得出来，不必留一份见过的回合集合。
	LastTurn *int `json:"lastTurn"`

	// OpenStep 是那个开着的步骤；步骤之外、或者它的消息已经装配好之后为 nil。
	OpenStep *OpenStep `json:"openStep"`

	// PendingCalls 是结果还没落地的那些工具调用的派发时刻，按 callId。
	PendingCalls map[llm.CallID]int64 `json:"pendingCalls"`
}

// Validate 检查一份状态的每个数落在它该在的范围里。
//
// 源: packages/session/session-stats/src/projection.ts:88-97
//
// 它只在 [projection.Definition.DecodeState] 那条路上跑——也就是字节从盘上
// 回来的时候。折叠本身只会往上加非负的量，加不出越界的值来；越界只可能来自
// 一份被改过的、或者别的构建写下的检查点行。
func (s State) Validate() error {
	nonNegative := []struct {
		name  string
		value int64
	}{
		{"turns", int64(s.Turns)},
		{"steps", int64(s.Steps)},
		{"llmMs", s.LLMMs},
		{"toolMs", s.ToolMs},
		{"ttftMs", s.TTFTMs},
		{"ttftSteps", int64(s.TTFTSteps)},
		{"decodeMs", s.DecodeMs},
		{"decodeTokens", int64(s.DecodeTokens)},
	}
	for _, item := range nonNegative {
		if item.value < 0 {
			return fmt.Errorf("feature/sessionstats: %s 不能是负数，给的是 %d", item.name, item.value)
		}
	}

	if s.LastTurn != nil && *s.LastTurn < 0 {
		return fmt.Errorf("feature/sessionstats: lastTurn 不能是负数，给的是 %d", *s.LastTurn)
	}

	if open := s.OpenStep; open != nil {
		switch {
		case open.Turn < 0:
			return fmt.Errorf("feature/sessionstats: openStep.turn 不能是负数，给的是 %d", open.Turn)
		case open.Step < 0:
			return fmt.Errorf("feature/sessionstats: openStep.step 不能是负数，给的是 %d", open.Step)
		case open.StartTime < 0:
			return fmt.Errorf("feature/sessionstats: openStep.startTime 不能是负数，给的是 %d", open.StartTime)
		case open.FirstTokenTime != nil && *open.FirstTokenTime < 0:
			return fmt.Errorf("feature/sessionstats: openStep.firstTokenTime 不能是负数，给的是 %d",
				*open.FirstTokenTime)
		}
	}

	for callID, dispatched := range s.PendingCalls {
		if dispatched < 0 {
			return fmt.Errorf("feature/sessionstats: pendingCalls[%q] 不能是负数，给的是 %d", callID, dispatched)
		}
	}
	return nil
}

// Definition 交出这个单元，装配方拿它去 [projection.Register]。
//
// 源: packages/session/session-stats/src/projection.ts:128-226（sessionStatsProjectionDefinition）
//
// 新增: DSH 那边这份值是一个模块级常量，注册由 index.ts 那个 cordis 插件代劳。
// Go 里它是一个函数，因为 [State] 里带一张表：一个包级变量会让 Init 交出的那份
// 状态被所有人共用。
func Definition() projection.Definition[State] {
	strict := projection.StrictDecoder[State]()

	return projection.Definition[State]{
		Key:          Key,
		StateVersion: StateVersion,
		Init: func() State {
			return State{PendingCalls: map[llm.CallID]int64{}}
		},
		Apply: apply,
		DecodeState: func(data json.RawMessage) (State, error) {
			state, err := strict(data)
			if err != nil {
				return State{}, err
			}
			if err := state.Validate(); err != nil {
				return State{}, err
			}
			if state.PendingCalls == nil {
				// 盘上那一行缺了这个键（或者写的是 null）。Go 的空表和缺席在
				// 读上完全等价，但往一张 nil 表里写会炸，所以在这里补齐一次，
				// 后面的转移就不必每处都提防它。
				state.PendingCalls = map[llm.CallID]int64{}
			}
			return state, nil
		},
		View: func(state State) any { return state.Figures },
	}
}

// apply 是那个纯转移。
//
// 源: packages/session/session-stats/src/projection.ts:129-195
//
// 新增: 负载读不回来时这里当成一条不相干的事件放过去。[projection.Definition.Apply]
// 没有报错的位置，而且也不该有：一条读不回来的负载说明它在被追加的时候就没验，
// 那是追加那一侧的缺陷，一个统计单元没有资格因为它而让整次读失败。
func apply(state State, event sessionlog.Event) (State, bool) {
	switch event.Type {
	case sessionlog.EventStepStart:
		data, ok := decode[sessionlog.StepStartData](event)
		if !ok {
			return state, false
		}
		state.OpenStep = &OpenStep{Turn: data.Turn, Step: data.Step, StartTime: event.Time}
		return state, true

	case sessionlog.EventAssistantChunk:
		data, ok := decode[sessionlog.AssistantChunkData](event)
		if !ok {
			return state, false
		}
		open := state.OpenStep
		if open == nil || open.Turn != data.Turn || open.Step != data.Step {
			return state, false
		}
		if open.FirstTokenTime != nil || !llm.IsTokenDelta(data.Chunk) {
			return state, false
		}
		firstToken := event.Time
		state.OpenStep = &OpenStep{
			Turn: open.Turn, Step: open.Step, StartTime: open.StartTime, FirstTokenTime: &firstToken,
		}
		return state, true

	case sessionlog.EventAssistantMessage:
		data, ok := decode[sessionlog.AssistantMessageData](event)
		if !ok {
			return state, false
		}
		open := state.OpenStep
		if open == nil || open.Turn != data.Turn || open.Step != data.Step {
			return state, false
		}
		// 一个步骤只装配一条消息：这里把边界关掉，于是一条防御性的重复消息
		// 也累加不了第二次。
		state.LLMMs += max(0, event.Time-open.StartTime)
		state.OpenStep = nil
		if open.FirstTokenTime != nil {
			state.TTFTMs += max(0, *open.FirstTokenTime-open.StartTime)
			state.TTFTSteps++
			if tokens, reported := outputTokens(data.Usage); reported {
				state.DecodeMs += max(0, event.Time-*open.FirstTokenTime)
				state.DecodeTokens += tokens
			}
		}
		return state, true

	case sessionlog.EventToolCall:
		data, ok := decode[sessionlog.ToolCallData](event)
		if !ok {
			return state, false
		}
		state.PendingCalls = withCall(state.PendingCalls, data.CallID, event.Time)
		return state, true

	case sessionlog.EventToolResult:
		data, ok := decode[sessionlog.ToolResultData](event)
		if !ok {
			return state, false
		}
		source, isTool := data.Message.Source.(llm.ToolSource)
		if !isTool {
			return state, false
		}
		dispatched, pending := state.PendingCalls[source.CallID]
		if !pending {
			return state, false
		}
		state.ToolMs += max(0, event.Time-dispatched)
		state.PendingCalls = withoutCall(state.PendingCalls, source.CallID)
		return state, true

	case sessionlog.EventStepEnd:
		data, ok := decode[sessionlog.StepEndData](event)
		if !ok {
			return state, false
		}
		if state.LastTurn == nil || *state.LastTurn != data.Turn {
			state.Turns++
		}
		state.Steps++
		turn := data.Turn
		state.LastTurn = &turn
		state.OpenStep = nil
		return state, true

	case sessionlog.EventTurnEnd:
		// 结果始终在它自己的回合内落地，所以回合结束时还挂着的调用属于一个
		// 被取消或者失败的回合。把它们丢掉，免得落盘状态无限长下去。
		if len(state.PendingCalls) == 0 {
			return state, false
		}
		state.PendingCalls = map[llm.CallID]int64{}
		return state, true

	default:
		return state, false
	}
}

// decode 把一条事件的负载读回它对应的负载类型，读不回来时第二个返回值是 false。
func decode[T any](event sessionlog.Event) (T, bool) {
	payload := event.Data
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var data T
	if err := json.Unmarshal(payload, &data); err != nil {
		var zero T
		return zero, false
	}
	return data, true
}

// outputTokens 取出提供方报的输出 token 数；没报或者是负数时第二个返回值是 false。
//
// 源: packages/session/session-stats/src/projection.ts:105-109
//
// 新增: DSH 那个 usageOutputTokens 收的是 unknown，要自己验类型、验有限、
// 验非负。Go 这边前两样由 [sessionlog.AssistantMessageData.Usage] 的类型管住了，
// 剩下的只有非负这一条——它守的是一份来自适配器的记账，那一侧不归本包管。
func outputTokens(usage *llm.TokenUsage) (int, bool) {
	if usage == nil || usage.OutputTokens < 0 {
		return 0, false
	}
	return usage.OutputTokens, true
}

// withCall 复制一份挂着的调用表并记上这一次派发。
//
// 新增: DSH 的每一条转移都靠对象展开产出新值，因为它拿「同一个引用」当
// 「没变」。Go 这边「变没变」是显式返回的，复制表的理由换成了另一条：
// [projection.Definition.Apply] 声明自己是一个纯转移，而 [State] 里那张表是
// 引用语义——就地改会把已经交出去的上一份状态一起改掉。
func withCall(pending map[llm.CallID]int64, callID llm.CallID, at int64) map[llm.CallID]int64 {
	next := make(map[llm.CallID]int64, len(pending)+1)
	for id, dispatched := range pending {
		next[id] = dispatched
	}
	next[callID] = at
	return next
}

// withoutCall 复制一份挂着的调用表并去掉这一条，理由同 [withCall]。
func withoutCall(pending map[llm.CallID]int64, callID llm.CallID) map[llm.CallID]int64 {
	next := make(map[llm.CallID]int64, len(pending))
	for id, dispatched := range pending {
		if id != callID {
			next[id] = dispatched
		}
	}
	return next
}
