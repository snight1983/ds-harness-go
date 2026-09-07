// 本文件的作用：回合大纲那个投影单元——它的状态、它的纯转移、以及怎么登记。
//
// 源: packages/session/session-turn-outline/src/projection.ts:85-136

package turnoutline

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// ProjectionKey 是回合大纲占的投影键。
//
// 源: packages/session/session-turn-outline/src/projection.ts:86
const ProjectionKey = "turnOutline"

// projectionStateVersion 是大纲状态的作废版本号。
//
// 新增: DSH 那边是 2——它有过一版旧形状，要把旧行作废掉。本仓库这个键是新的，
// 盘上一行都没有，所以从 1 起。这个数只在本仓库自己写的检查点之间有意义，
// 和 DSH 的行对不上也不需要对得上：两边的状态字节本来就不是一种东西。
const projectionStateVersion = 1

// Entry 是一个已开始的回合在大纲上的那一行。
//
// 源: packages/session/session-turn-outline/src/types.ts:12-22（TurnOutlineEntry）
type Entry struct {
	// Turn 是宿主给的回合号。
	Turn int `json:"turn"`
	// Seq 是这个回合 turn/start 那条事件的 seq，往回翻窗口时以它为落点。
	Seq int `json:"seq"`
	// Prompt 是这个回合第一条用户消息的预览；还没等到之前是空串。
	Prompt string `json:"prompt"`
	// Response 是这个回合最终回复的预览；回合结束前是空串。
	Response string `json:"response"`
}

// outlineState 是大纲单元的状态：已定稿的那些行，加上开着的那个回合的回复草稿。
//
// 源: packages/session/session-turn-outline/src/types.ts:24-38（TurnOutlineState）
//
// 草稿只在宿主这一侧：视图交出去的只有 Turns。它缓着当前回合最新的那条带文字的
// 助手消息，等 turn/end 才定稿——理由是一个回合里助手可能说很多次，而列表卡片
// 要的是最后那次。
type outlineState struct {
	// Turns 按回合号升序，严格递增。
	Turns []Entry `json:"turns"`
	// Draft 是开着的那个回合最新的回复预览；不在回合里时是空串。
	Draft string `json:"draft"`
}

// RegisterProjection 把大纲单元登进注册表，返回把它注销的函数。
//
// 源: packages/session/session-turn-outline/src/index.ts:24-28
//
// 新增: DSH 那边这是挂在插件 fiber 上的一次 ctx.sessionProjections.register，
// fiber 没了登记就没了。Go 没有那个容器，于是和 sessiontitle.RegisterProjection、
// todo.RegisterProjection 一样，成了装配方手上的一次显式调用。
//
// 关于「变了没有」那个返回值，这里有一处必须说清的取舍：DSH 靠两道闸——
// 折叠返回同一个引用挡住驱动，视图的引用没变再挡住变更流。[projection.Definition.Apply]
// 只有一个 bool，所以本包让它表达的是**视图变了没有**，不是状态变了没有：
// 一条只更新草稿的助手消息返回 false，可 [projection.Registry.Drive] 照样把新状态
// 存进单元格（它无条件写 cell.state）。结果就是包文档说的「一个回合最多推三次」，
// 而检查点里的草稿始终是最新的。
//
// 返回的注销函数是幂等的。
func RegisterProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("turnoutline: 需要一个投影注册表")
	}
	return projection.Register(registry, projection.Definition[outlineState]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() outlineState { return outlineState{Turns: []Entry{}} },
		Apply:        applyOutline,
		DecodeState:  decodeState,
		// 视图就是那些行本身，和 DSH 的介质形状一致（成例见 todo.RegisterProjection）。
		// 交出去的是活切片，安全的前提是本包从不原地改它——每一次改都先复制。
		View: func(state outlineState) any { return state.Turns },
	})
}

// applyOutline 是大纲那个纯转移。
//
// 源: packages/session/session-turn-outline/src/projection.ts:90-132
func applyOutline(state outlineState, event sessionlog.Event) (outlineState, bool) {
	switch event.Type {
	case sessionlog.EventTurnStart:
		return applyTurnStart(state, event)
	case sessionlog.EventUserMessage:
		return applyUserMessage(state, event)
	case sessionlog.EventAssistantMessage:
		return applyAssistantMessage(state, event)
	case sessionlog.EventTurnEnd:
		return applyTurnEnd(state)
	default:
		return state, false
	}
}

// applyTurnStart 开一行新的。
//
// 源: packages/session/session-turn-outline/src/projection.ts:95-105
func applyTurnStart(state outlineState, event sessionlog.Event) (outlineState, bool) {
	var data sessionlog.TurnStartData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		// 折叠没有出错这条路（成例见 todo.applyProjection）：一条读不回来的边界
		// 只能被跳过。真正拦坏事件的是 [sessionlog.ValidateEvent]。
		return state, false
	}
	// 不推进回合号的边界一律不开新行：一来大纲要保持有序，二来一个重试的回合
	// 应该把预览填回原来那一行，而不是另起一行。
	if last, ok := lastEntry(state.Turns); ok && data.Turn <= last.Turn {
		return state, false
	}
	turns := make([]Entry, len(state.Turns), len(state.Turns)+1)
	copy(turns, state.Turns)
	turns = append(turns, Entry{Turn: data.Turn, Seq: event.Seq})
	return outlineState{Turns: turns, Draft: ""}, true
}

// applyUserMessage 给最新那一行填上提示词预览。
//
// 源: packages/session/session-turn-outline/src/projection.ts:106-116
func applyUserMessage(state outlineState, event sessionlog.Event) (outlineState, bool) {
	var data sessionlog.UserMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return state, false
	}
	if data.Source == nil || data.Source.SourceKind() != llm.SourceUser {
		return state, false
	}
	// 只有最新那一行还可能在等它的开场白；同一个回合里后来的人话是插话，
	// 不该顶掉第一句。
	last, ok := lastEntry(state.Turns)
	if !ok || last.Prompt != "" {
		return state, false
	}
	prompt := preview(data.Content, PromptPreviewMaxChars)
	if prompt == "" {
		return state, false
	}
	turns := slices.Clone(state.Turns)
	turns[len(turns)-1].Prompt = prompt
	return outlineState{Turns: turns, Draft: state.Draft}, true
}

// applyAssistantMessage 更新草稿——只动宿主这一侧，视图不变。
//
// 源: packages/session/session-turn-outline/src/projection.ts:117-122
func applyAssistantMessage(state outlineState, event sessionlog.Event) (outlineState, bool) {
	var data sessionlog.AssistantMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return state, false
	}
	draft := preview(data.Message.Content, ResponsePreviewMaxChars)
	if draft == "" || draft == state.Draft {
		return state, false
	}
	// 返回 false 而状态确实变了，这是本包唯一一处这么做的地方，理由见
	// [RegisterProjection]。
	return outlineState{Turns: state.Turns, Draft: draft}, false
}

// applyTurnEnd 把草稿定稿成最新那一行的回复预览。
//
// 源: packages/session/session-turn-outline/src/projection.ts:123-131
//
// 这里不读 [sessionlog.TurnEndData]：结束理由和大纲无关，一个被打断的回合
// 照样定稿它中断前说出来的那些话。
func applyTurnEnd(state outlineState) (outlineState, bool) {
	if state.Draft == "" {
		return state, false
	}
	last, ok := lastEntry(state.Turns)
	if !ok || last.Response == state.Draft {
		// 没有行可定稿、或者定的和已有的一样：只把草稿清掉，视图不变。
		return outlineState{Turns: state.Turns, Draft: ""}, false
	}
	turns := slices.Clone(state.Turns)
	turns[len(turns)-1].Response = state.Draft
	return outlineState{Turns: turns, Draft: ""}, true
}

// lastEntry 取最新那一行；没有行时第二个返回值是 false。
func lastEntry(turns []Entry) (Entry, bool) {
	if len(turns) == 0 {
		return Entry{}, false
	}
	return turns[len(turns)-1], true
}

// decodeState 把一份落过盘的大纲状态读回来。
//
// 源: packages/session/session-turn-outline/src/projection.ts:61-80（两个 zod schema）
//
// 除了「不许有多出来的字段」，还得验 DSH 用 superRefine 验的那件事：回合号严格
// 递增。理由是这份字节来自盘上，而折叠**依赖**这个顺序——它只看最后一行。
// 一份乱序的状态不会报错，只会把往后每一个回合的预览都填到错的行上。
func decodeState(data json.RawMessage) (outlineState, error) {
	state, err := projection.StrictDecoder[outlineState]()(data)
	if err != nil {
		return outlineState{}, err
	}
	if state.Turns == nil {
		return outlineState{}, errors.New("turnoutline: 检查点里的 turns 不许是 null")
	}
	previous := -1
	for _, entry := range state.Turns {
		// previous 从 -1 起，所以这一条同时管住了「回合号非负」。
		if entry.Turn <= previous {
			return outlineState{}, fmt.Errorf("turnoutline: 回合号必须严格递增，%d 跟在 %d 后面", entry.Turn, previous)
		}
		if entry.Seq < 0 {
			return outlineState{}, fmt.Errorf("turnoutline: 回合 %d 的 seq 不许是负数：%d", entry.Turn, entry.Seq)
		}
		if err := boundPreview(entry.Prompt, PromptPreviewMaxChars); err != nil {
			return outlineState{}, fmt.Errorf("turnoutline: 回合 %d 的提示词预览%w", entry.Turn, err)
		}
		if err := boundPreview(entry.Response, ResponsePreviewMaxChars); err != nil {
			return outlineState{}, fmt.Errorf("turnoutline: 回合 %d 的回复预览%w", entry.Turn, err)
		}
		previous = entry.Turn
	}
	if err := boundPreview(state.Draft, ResponsePreviewMaxChars); err != nil {
		return outlineState{}, fmt.Errorf("turnoutline: 草稿%w", err)
	}
	return state, nil
}

// boundPreview 判一段预览有没有超过它的字数预算。
func boundPreview(text string, limit int) error {
	if count := utf8.RuneCountInString(text); count > limit {
		return fmt.Errorf("超过 %d 个字：%d", limit, count)
	}
	return nil
}
