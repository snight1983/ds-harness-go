// 本文件的作用：回合与步骤那套关系约束——谁必须开着、谁必须先关、悬空调用怎么记。
//
// 源: packages/core/session/src/invariant.ts

package session

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/llm"
)

func TestNewTraceStartsBeforeTheFirstEvent(t *testing.T) {
	t.Parallel()

	trace := NewTrace()
	if trace.LastSeq != -1 {
		t.Fatalf("空账的 LastSeq 该是 -1，实际 %d", trace.LastSeq)
	}
	if trace.NextTurn != 1 || trace.NextStep != 1 {
		t.Fatalf("空账该从回合 1／步骤 1 起：%d %d", trace.NextTurn, trace.NextStep)
	}
	if trace.TurnIsOpen || trace.StepIsOpen {
		t.Fatalf("空账上不该有开着的回合或步骤")
	}
	if trace.PendingCalls == nil {
		t.Fatalf("悬空调用集合该是建好的")
	}
}

func TestTraceCloneDetachesThePendingSet(t *testing.T) {
	t.Parallel()

	trace := NewTrace()
	trace.PendingCalls["c1"] = struct{}{}

	clone := trace.Clone()
	clone.PendingCalls["c2"] = struct{}{}

	if _, leaked := trace.PendingCalls["c2"]; leaked {
		t.Fatalf("复制出来的账和原来那份共用了同一个集合")
	}
}

func TestValidateWalksAWellFormedTurn(t *testing.T) {
	t.Parallel()

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantChunkEvent(t, 2, 1, 1),
		assistantMessageEvent(t, 3, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "c1", Name: "read", Arguments: "{}"},
		}),
		toolCallEvent(t, 4, 1, 1, "c1", "read"),
		toolResultEvent(t, 5, 1, 1, "c1", "done"),
		stepEndEvent(t, 6, 1, 1),
		turnEndEvent(t, 7, 1),
	}

	trace, err := ValidateLog(events)
	if err != nil {
		t.Fatalf("这段日志该验得过：%v", err)
	}
	if trace.TurnIsOpen || trace.StepIsOpen {
		t.Fatalf("走完之后回合和步骤都该关着")
	}
	if trace.LastSeq != 7 {
		t.Fatalf("LastSeq 不对：%d", trace.LastSeq)
	}
	if trace.NextTurn != 2 || trace.NextStep != 2 {
		t.Fatalf("下一个编号不对：回合 %d 步骤 %d", trace.NextTurn, trace.NextStep)
	}
	if len(trace.PendingCalls) != 0 {
		t.Fatalf("走完之后不该还有悬空调用：%v", trace.PendingCalls)
	}
}

func TestValidateRefusesASeqThatDoesNotMoveForward(t *testing.T) {
	t.Parallel()

	trace := NewTrace()
	transition, err := trace.Validate(turnStartEvent(t, 5, 1))
	if err != nil {
		t.Fatalf("第一条该验得过：%v", err)
	}
	trace.Apply(transition)

	for _, seq := range []int{5, 4, 0} {
		if _, err := trace.Validate(turnStartEvent(t, seq, 2)); !errors.Is(err, ErrTraceViolation) {
			t.Fatalf("seq %d 该被拒，实际 %v", seq, err)
		}
	}
}

func TestValidateEnforcesTheTurnAndStepNesting(t *testing.T) {
	t.Parallel()

	cases := map[string][]Event{
		"回合还开着就再开一个": {
			turnStartEvent(t, 0, 1),
			turnStartEvent(t, 1, 2),
		},
		"回合号跳了": {
			turnStartEvent(t, 0, 2),
		},
		"关的不是开着的那个回合": {
			turnStartEvent(t, 0, 1),
			turnEndEvent(t, 1, 2),
		},
		"没有回合开着就来关": {
			turnEndEvent(t, 0, 1),
		},
		"步骤还开着就关回合": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 1),
			turnEndEvent(t, 2, 1),
		},
		"没有回合就开步骤": {
			stepStartEvent(t, 0, 1, 1),
		},
		"步骤说的回合不是开着的那个": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 2, 1),
		},
		"步骤还开着就再开一个": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 1),
			stepStartEvent(t, 2, 1, 2),
		},
		"步骤号跳了": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 2),
		},
		"关的不是开着的那个步骤": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 1),
			stepEndEvent(t, 2, 1, 2),
		},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ValidateLog(events); !errors.Is(err, ErrTraceViolation) {
				t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
			}
		})
	}
}

func TestValidateResetsTheStepCounterOnEachTurn(t *testing.T) {
	t.Parallel()

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		stepEndEvent(t, 2, 1, 1),
		stepStartEvent(t, 3, 1, 2),
		stepEndEvent(t, 4, 1, 2),
		turnEndEvent(t, 5, 1),
		turnStartEvent(t, 6, 2),
		// 新回合的步骤号从 1 重新起。
		stepStartEvent(t, 7, 2, 1),
	}

	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("这段日志该验得过：%v", err)
	}
}

func TestValidateRequiresAnOpenStepForTheStepScopedEvents(t *testing.T) {
	t.Parallel()

	// 这四种事件都必须点中当前开着的那个回合和步骤。
	cases := map[string]Event{
		"助手分块": assistantChunkEvent(t, 2, 1, 9),
		"助手消息": assistantMessageEvent(t, 2, 1, 9, llm.Content{llm.TextBlock{Text: "x"}}),
		"工具调用": toolCallEvent(t, 2, 1, 9, "c1", "read"),
		"工具结果": toolResultEvent(t, 2, 1, 9, "c1", "done"),
	}

	for name, stray := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			events := []Event{
				turnStartEvent(t, 0, 1),
				stepStartEvent(t, 1, 1, 1),
				stray,
			}
			if _, err := ValidateLog(events); !errors.Is(err, ErrTraceViolation) {
				t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
			}
		})
	}
}

func TestValidateRefusesAToolResultWithoutAPriorCall(t *testing.T) {
	t.Parallel()

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		toolResultEvent(t, 2, 1, 1, "c1", "done"),
	}
	if _, err := ValidateLog(events); !errors.Is(err, ErrTraceViolation) {
		t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
	}
}

func TestValidateLetsThroughASyntheticNotStartedResult(t *testing.T) {
	t.Parallel()

	// 「从没开始过」的补写结果没有在先的 tool/call，那正是它存在的理由。
	synthetic := syntheticResultEvent(t, 2, 1, 1, "c1", ToolNotStarted, true)

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		synthetic,
	}
	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("补出来的没开始结果该放过去：%v", err)
	}

	t.Run("换成别的恢复码就不放", func(t *testing.T) {
		t.Parallel()

		stray := syntheticResultEvent(t, 2, 1, 1, "c1", ToolOutcomeUnknown, true)
		events := []Event{turnStartEvent(t, 0, 1), stepStartEvent(t, 1, 1, 1), stray}
		if _, err := ValidateLog(events); !errors.Is(err, ErrTraceViolation) {
			t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
		}
	})

	t.Run("不是错误结果就不放", func(t *testing.T) {
		t.Parallel()

		stray := syntheticResultEvent(t, 2, 1, 1, "c1", ToolNotStarted, false)
		events := []Event{turnStartEvent(t, 0, 1), stepStartEvent(t, 1, 1, 1), stray}
		if _, err := ValidateLog(events); !errors.Is(err, ErrTraceViolation) {
			t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
		}
	})
}

func TestValidateLetsAToolResultRewriteSitOutsideItsStep(t *testing.T) {
	t.Parallel()

	// 一次内容重写引用的是它替换掉的那条事件，是这个回合持久的工作产物，
	// 不是原来那次调用的第二次执行——所以它不必点中当前那个步骤。
	rewrite := toolResultEvent(t, 5, 9, 9, "c1", "trimmed")
	rewrite.SurfaceOp = ReplaceOp{Start: 4, End: 4}
	rewrite.SourceEventSeqs = []int{4}

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "c1", Name: "read", Arguments: "{}"},
		}),
		toolCallEvent(t, 3, 1, 1, "c1", "read"),
		toolResultEvent(t, 4, 1, 1, "c1", "raw"),
		rewrite,
	}
	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("内容重写该放过去：%v", err)
	}

	t.Run("但重写不能落在任何回合之外", func(t *testing.T) {
		t.Parallel()

		stray := toolResultEvent(t, 0, 1, 1, "c1", "trimmed")
		stray.SurfaceOp = ReplaceOp{Start: 0, End: 0}
		if _, err := ValidateLog([]Event{stray}); !errors.Is(err, ErrTraceViolation) {
			t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
		}
	})
}

func TestValidateTracksThePendingCallsAcrossTheStep(t *testing.T) {
	t.Parallel()

	trace := NewTrace()
	push := func(event Event) {
		t.Helper()

		transition, err := trace.Validate(event)
		if err != nil {
			t.Fatalf("seq %d 验不过：%v", event.Seq, err)
		}
		trace.Apply(transition)
	}

	push(turnStartEvent(t, 0, 1))
	push(stepStartEvent(t, 1, 1, 1))
	push(toolCallEvent(t, 2, 1, 1, "c1", "read"))
	push(toolCallEvent(t, 3, 1, 1, "c2", "read"))
	if len(trace.PendingCalls) != 2 {
		t.Fatalf("该悬着两个调用：%v", trace.PendingCalls)
	}

	push(toolResultEvent(t, 4, 1, 1, "c1", "done"))
	if _, still := trace.PendingCalls["c1"]; still {
		t.Fatalf("结果落下来之后 c1 不该还悬着")
	}
	if len(trace.PendingCalls) != 1 {
		t.Fatalf("该只剩一个悬着的调用：%v", trace.PendingCalls)
	}

	// 步骤一关，这个步骤里剩下的悬空调用一次清干净。
	push(stepEndEvent(t, 5, 1, 1))
	if len(trace.PendingCalls) != 0 {
		t.Fatalf("步骤关掉之后不该还有悬空调用：%v", trace.PendingCalls)
	}
}

func TestValidateWrapsTheContextEventsInATurn(t *testing.T) {
	t.Parallel()

	// 这三种是核心执行事件，必须被一个开着的回合包住。
	for _, kind := range []EventType{EventTodoWrite, EventRequestHeader, EventRequestContext} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			stray := Event{Type: kind, Seq: 0, Data: json.RawMessage(`{}`)}
			if _, err := ValidateLog([]Event{stray}); !errors.Is(err, ErrTraceViolation) {
				t.Fatalf("想要 ErrTraceViolation，实际 %v", err)
			}

			inside := Event{Type: kind, Seq: 1, Data: json.RawMessage(`{}`)}
			if _, err := ValidateLog([]Event{turnStartEvent(t, 0, 1), inside}); err != nil {
				t.Fatalf("回合里的该放过去：%v", err)
			}
		})
	}
}

func TestValidateLeavesTheUnconstrainedEventsAlone(t *testing.T) {
	t.Parallel()

	// 用户消息、seed 的结尾，以及任何可合并扩展的类型，都不受这套约束管。
	events := []Event{
		userMessageEvent(t, 0, "hi"),
		{Type: EventSessionEndSeed, Seq: 1, Data: json.RawMessage(`{}`)},
		{Type: "compaction/summary", Seq: 2, Data: json.RawMessage(`{"n":1}`)},
	}
	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("这几种不该被拦：%v", err)
	}
}

func TestValidateReportsABrokenPayload(t *testing.T) {
	t.Parallel()

	cases := map[string]EventType{
		"回合开始": EventTurnStart,
		"回合结束": EventTurnEnd,
		"步骤开始": EventStepStart,
		"步骤结束": EventStepEnd,
		"助手分块": EventAssistantChunk,
		"助手消息": EventAssistantMessage,
		"工具调用": EventToolCall,
		"工具结果": EventToolResult,
	}

	for name, kind := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			event := Event{Type: kind, Seq: 0, Data: json.RawMessage(`7`), SurfaceOp: AppendOp{}}
			trace := NewTrace()
			if _, err := trace.Validate(event); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

func TestValidateRefusesAToolResultThatIsNotFromATool(t *testing.T) {
	t.Parallel()

	strayed := toolResultEvent(t, 2, 1, 1, "c1", "done")
	var data ToolResultData
	if err := json.Unmarshal(strayed.Data, &data); err != nil {
		t.Fatalf("负载读不回来：%v", err)
	}
	data.Message.Source = llm.UserSource{}
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	strayed.Data = payload

	events := []Event{turnStartEvent(t, 0, 1), stepStartEvent(t, 1, 1, 1), strayed}
	if _, err := ValidateLog(events); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestValidateDoesNotTouchTheTraceItValidates(t *testing.T) {
	t.Parallel()

	// 验是纯的：一条事件可能在发布前被别的监听方否决，扔掉那次转移
	// 不该让这份账往前走。
	trace := NewTrace()
	before := trace.Clone()

	if _, err := trace.Validate(turnStartEvent(t, 0, 1)); err != nil {
		t.Fatalf("该验得过：%v", err)
	}
	if trace.LastSeq != before.LastSeq || trace.TurnIsOpen != before.TurnIsOpen {
		t.Fatalf("验的时候把账改了：%#v", trace)
	}
}

func TestValidateLogRepairsWhatInterruptedTurnClosersProduces(t *testing.T) {
	t.Parallel()

	// 补出来的收尾必须自己也验得过——否则「先补齐再交出去」这条路根本走不通。
	crashed := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "started", Name: "read", Arguments: "{}"},
			llm.ToolCallBlock{ID: "cold", Name: "write", Arguments: "{}"},
		}),
		toolCallEvent(t, 3, 1, 1, "started", "read"),
	}

	if _, err := ValidateLog(crashed); err != nil {
		t.Fatalf("崩掉的那段本身该是自洽的：%v", err)
	}

	repaired := append(append([]Event(nil), crashed...), mustClosers(t, crashed)...)
	trace, err := ValidateLog(repaired)
	if err != nil {
		t.Fatalf("补齐之后该验得过：%v", err)
	}
	if trace.TurnIsOpen || trace.StepIsOpen {
		t.Fatalf("补齐之后回合和步骤都该关着")
	}
	if len(trace.PendingCalls) != 0 {
		t.Fatalf("补齐之后不该还有悬空调用：%v", trace.PendingCalls)
	}

	// 再补一遍，已经平衡的日志补不出东西。
	if again := mustClosers(t, repaired); len(again) != 0 {
		t.Fatalf("补齐之后再补该是空的：%#v", again)
	}
}

// assistantChunkEvent 排一条助手分块事件出来。
func assistantChunkEvent(t *testing.T, seq, turn, step int) Event {
	t.Helper()

	payload, err := json.Marshal(AssistantChunkData{
		Turn: turn, Step: step, Chunk: llm.TextDeltaChunk{Index: 0, Text: "a"},
	})
	if err != nil {
		t.Fatalf("助手分块负载排不出去：%v", err)
	}
	return Event{Type: EventAssistantChunk, Seq: seq, Data: payload}
}

// syntheticResultEvent 排一条带恢复码的补写工具结果出来。
func syntheticResultEvent(
	t *testing.T, seq, turn, step int, callID llm.CallID, code string, isError bool,
) Event {
	t.Helper()

	payload, err := json.Marshal(ToolResultData{
		Turn: turn, Step: step,
		Message: llm.Message{
			ID: "synthetic", Role: llm.RoleUser,
			Source: llm.ToolSource{CallID: callID},
			Content: llm.Content{llm.ToolResultBlock{
				ToolCallID: callID, IsError: isError,
				Content: llm.Content{llm.TextBlock{Text: "x"}},
			}},
		},
		Error: &ToolError{Name: "E", Code: code},
	})
	if err != nil {
		t.Fatalf("补写结果负载排不出去：%v", err)
	}
	return Event{Type: EventToolResult, Seq: seq, Data: payload, SurfaceOp: AppendOp{}}
}
