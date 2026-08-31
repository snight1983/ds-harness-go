// 本文件的作用：日志里回合与步骤那套关系约束的检查器——谁必须开着、谁必须先关。
//
// 源: packages/core/session/src/invariant.ts

package session

import (
	"encoding/json"
	"fmt"
	"maps"

	"ds-harness-go/llm"
)

// Trace 是一个会话为了做关系检查而记的那点账。
//
// 源: packages/core/session/src/invariant.ts:22-30
//
// 新增: DSH 那边这份账挂在一张以 Session 对象为键的 WeakMap 上，因为它要给一个
// 活着的对象挂旁路状态。本包的 Trace 就是一个普通的值，谁拥有它谁自己拿着；
// 把它接到本仓库的 invariants 注册表上是循环那一块的事。
//
// 零值不可用，用 [NewTrace] 建。
type Trace struct {
	// LastSeq 是已经收下的最后一条事件的 seq。
	LastSeq int
	// OpenTurn 是当前开着的回合号；TurnIsOpen 为假时无意义。
	OpenTurn   int
	TurnIsOpen bool
	// OpenStep 是当前开着的步骤号；StepIsOpen 为假时无意义。
	OpenStep   int
	StepIsOpen bool
	// NextTurn 是下一个回合该用的编号。
	NextTurn int
	// NextStep 是当前回合里下一个步骤该用的编号。
	NextStep int
	// PendingCalls 是这个步骤里已发出、还没等到结果的那些调用。
	PendingCalls map[llm.CallID]struct{}
}

// NewTrace 建一份空的账。
//
// 源: packages/core/session/src/invariant.ts:198-205
func NewTrace() Trace {
	return Trace{
		LastSeq:      -1,
		NextTurn:     1,
		NextStep:     1,
		PendingCalls: map[llm.CallID]struct{}{},
	}
}

// Clone 深复制这份账。
func (t Trace) Clone() Trace {
	t.PendingCalls = maps.Clone(t.PendingCalls)
	return t
}

// pendingCallChange 是一次转移对悬空调用集合的改动。
type pendingCallChange int

const (
	// pendingNone 不动那个集合。
	pendingNone pendingCallChange = iota
	// pendingAdd 加一个调用。
	pendingAdd
	// pendingDelete 删一个调用。
	pendingDelete
	// pendingClear 清空。
	pendingClear
)

// Transition 是一条已经验过的事件对这份账的、还没落下去的改动。
//
// 源: packages/core/session/src/invariant.ts:32-39
//
// 分成「验」和「落」两步，是因为一条事件在发布前可能被别的监听方否决：
// 验是纯的，扔掉一次转移不会让这份账往前走。
type Transition struct {
	lastSeq            int
	openTurn, openStep int
	turnIsOpen         bool
	stepIsOpen         bool
	nextTurn, nextStep int
	pendingChange      pendingCallChange
	pendingCallID      llm.CallID
}

// requireOpenStep 断言一条步骤范围内的事件点的是当前开着的那个回合和步骤。
//
// 源: packages/core/session/src/invariant.ts:42-52
func (t Trace) requireOpenStep(kind EventType, turn, step int) error {
	if t.TurnIsOpen && t.StepIsOpen && t.OpenTurn == turn && t.OpenStep == step {
		return nil
	}
	return fmt.Errorf("%w：%s 点的是回合 %d／步骤 %d，但开着的是 %s",
		ErrTraceViolation, kind, turn, step, t.describeOpen())
}

// describeOpen 把当前开着的回合与步骤说成一句人话。
func (t Trace) describeOpen() string {
	turn, step := "无", "无"
	if t.TurnIsOpen {
		turn = fmt.Sprintf("%d", t.OpenTurn)
	}
	if t.StepIsOpen {
		step = fmt.Sprintf("%d", t.OpenStep)
	}
	return fmt.Sprintf("回合 %s／步骤 %s", turn, step)
}

// Validate 验一条候选事件，**不改动**这份账。
//
// 源: packages/core/session/src/invariant.ts:55-166
//
// 新增: DSH 的 fail 不一定抛（注册方可以只记不抛），所以那边一条事件能攒下
// 好几条违反再继续往下走。Go 这边返回**第一条**违反就停：一份日志只要违反了
// 一条关系约束，后面的判断就都建立在一个已经不成立的状态上，多报几条只会
// 让真正的第一现场淹在噪声里。
//
// 上下文类和插件私有的只进日志的事件可以在两次模型执行之间追加；
// 核心的执行事件保留它们显式的回合关系。
func (t Trace) Validate(event Event) (Transition, error) {
	if event.Seq <= t.LastSeq {
		return Transition{}, fmt.Errorf("%w：seq 必须严格递增，%d 出现在 %d 之后",
			ErrTraceViolation, event.Seq, t.LastSeq)
	}
	transition := Transition{
		lastSeq:    event.Seq,
		openTurn:   t.OpenTurn,
		openStep:   t.OpenStep,
		turnIsOpen: t.TurnIsOpen,
		stepIsOpen: t.StepIsOpen,
		nextTurn:   t.NextTurn,
		nextStep:   t.NextStep,
	}

	switch event.Type {
	case EventTurnStart:
		var data TurnStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("回合开始事件的负载读不回来", err)
		}
		if t.TurnIsOpen {
			return Transition{}, fmt.Errorf("%w：回合 %d 还开着就来了 turn/start %d",
				ErrTraceViolation, t.OpenTurn, data.Turn)
		}
		if data.Turn != t.NextTurn {
			return Transition{}, fmt.Errorf("%w：turn/start 期望回合 %d，实际 %d",
				ErrTraceViolation, t.NextTurn, data.Turn)
		}
		transition.openTurn, transition.turnIsOpen = data.Turn, true
		transition.nextStep = 1

	case EventTurnEnd:
		var data TurnEndData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("回合结束事件的负载读不回来", err)
		}
		if !t.TurnIsOpen || t.OpenTurn != data.Turn {
			return Transition{}, fmt.Errorf("%w：turn/end %d 对不上开着的 %s",
				ErrTraceViolation, data.Turn, t.describeOpen())
		}
		if t.StepIsOpen {
			return Transition{}, fmt.Errorf("%w：步骤 %d 还开着就来了 turn/end %d",
				ErrTraceViolation, t.OpenStep, data.Turn)
		}
		transition.turnIsOpen = false
		transition.nextTurn++

	case EventStepStart:
		var data StepStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("步骤开始事件的负载读不回来", err)
		}
		if !t.TurnIsOpen || t.OpenTurn != data.Turn {
			return Transition{}, fmt.Errorf("%w：step/start 说自己在回合 %d，但开着的是 %s",
				ErrTraceViolation, data.Turn, t.describeOpen())
		}
		if t.StepIsOpen {
			return Transition{}, fmt.Errorf("%w：步骤 %d 还开着就来了 step/start %d",
				ErrTraceViolation, t.OpenStep, data.Step)
		}
		if data.Step != t.NextStep {
			return Transition{}, fmt.Errorf("%w：回合 %d 的 step/start 期望步骤 %d，实际 %d",
				ErrTraceViolation, data.Turn, t.NextStep, data.Step)
		}
		transition.openStep, transition.stepIsOpen = data.Step, true

	case EventStepEnd:
		var data StepEndData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("步骤结束事件的负载读不回来", err)
		}
		if err := t.requireOpenStep(EventStepEnd, data.Turn, data.Step); err != nil {
			return Transition{}, err
		}
		transition.pendingChange = pendingClear
		transition.stepIsOpen = false
		transition.nextStep++

	case EventAssistantChunk:
		var data AssistantChunkData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("助手分块事件的负载读不回来", err)
		}
		if err := t.requireOpenStep(EventAssistantChunk, data.Turn, data.Step); err != nil {
			return Transition{}, err
		}

	case EventAssistantMessage:
		var data AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("助手消息事件的负载读不回来", err)
		}
		if err := t.requireOpenStep(EventAssistantMessage, data.Turn, data.Step); err != nil {
			return Transition{}, err
		}

	case EventToolCall:
		var data ToolCallData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("工具调用事件的负载读不回来", err)
		}
		if err := t.requireOpenStep(EventToolCall, data.Turn, data.Step); err != nil {
			return Transition{}, err
		}
		transition.pendingChange, transition.pendingCallID = pendingAdd, data.CallID

	case EventToolResult:
		var data ToolResultData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, wrapMalformed("工具结果事件的负载读不回来", err)
		}
		// 一次内容重写在别处已经验过了，而且它引用了自己替换掉的那条事件。
		// 那是这个回合持久的工作产物，不是原来那次调用的第二次执行。
		if event.SurfaceOp == nil || event.SurfaceOp.SurfaceOpKind() != OpAppend {
			if !t.TurnIsOpen {
				return Transition{}, fmt.Errorf("%w：工具结果的表面替换追加在了任何回合之外",
					ErrTraceViolation)
			}
			break
		}
		if err := t.requireOpenStep(EventToolResult, data.Turn, data.Step); err != nil {
			return Transition{}, err
		}
		source, ok := data.Message.Source.(llm.ToolSource)
		if !ok {
			return Transition{}, fmt.Errorf("%w：seq %d 的工具结果消息的来路不是一次工具调用",
				ErrMalformedValue, event.Seq)
		}
		if _, pending := t.PendingCalls[source.CallID]; !pending && !isSyntheticNotStarted(data) {
			return Transition{}, fmt.Errorf("%w：%s 的 tool/result 在这个步骤里没有在先的 tool/call",
				ErrTraceViolation, source.CallID)
		}
		transition.pendingChange, transition.pendingCallID = pendingDelete, source.CallID

	case EventUserMessage:
		// 不受约束。

	case EventSessionEndSeed:
		// 不受约束：一份不平衡的 seed 合法地把它落在一个开着的回合里。

	case EventTodoWrite, EventRequestHeader, EventRequestContext:
		if !t.TurnIsOpen {
			return Transition{}, fmt.Errorf("%w：%s 追加在了任何回合之外（核心执行事件必须被回合包住）",
				ErrTraceViolation, event.Type)
		}

	default:
		// 可合并扩展的那些事件，关系约束归拥有它们的那个包。
	}

	return transition, nil
}

// isSyntheticNotStarted 判断一条工具结果是不是 [InterruptedTurnClosers] 给一次
// 「从没开始过」的调用补出来的。
//
// 源: packages/core/session/src/invariant.ts:138
//
// 这样一条结果没有在先的 tool/call，那正是它存在的理由——不能拿「没有在先调用」
// 去否掉它。
func isSyntheticNotStarted(data ToolResultData) bool {
	if data.Error == nil || data.Error.Code != ToolNotStarted {
		return false
	}
	result, ok := data.Message.ToolResult()
	return ok && result.IsError
}

// Apply 把一次已经验过的转移落到这份账上。
//
// 源: packages/core/session/src/invariant.ts:169-187
func (t *Trace) Apply(transition Transition) {
	t.LastSeq = transition.lastSeq
	t.OpenTurn, t.TurnIsOpen = transition.openTurn, transition.turnIsOpen
	t.OpenStep, t.StepIsOpen = transition.openStep, transition.stepIsOpen
	t.NextTurn, t.NextStep = transition.nextTurn, transition.nextStep
	switch transition.pendingChange {
	case pendingAdd:
		t.PendingCalls[transition.pendingCallID] = struct{}{}
	case pendingDelete:
		delete(t.PendingCalls, transition.pendingCallID)
	case pendingClear:
		clear(t.PendingCalls)
	}
}

// ValidateLog 把一整段日志按关系约束走一遍，返回走完之后的那份账。
//
// 源: packages/core/session/src/invariant.ts:207-214
//
// 这是离线校验的入口：持久化那一层在把一份日志交出去之前用它确认这份日志
// 自己是自洽的。
func ValidateLog(events []Event) (Trace, error) {
	trace := NewTrace()
	for _, event := range events {
		transition, err := trace.Validate(event)
		if err != nil {
			return Trace{}, err
		}
		trace.Apply(transition)
	}
	return trace, nil
}
