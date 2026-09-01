// 本文件的作用：一份崩在半路的日志补出来的那几条收尾事件，以及它们为什么必须是确定的。
//
// 源: packages/core/session/src/repair.ts

package session

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestTheTwoRepairTextsAreNotTheSameSentence(t *testing.T) {
	t.Parallel()

	// 这两句话的措辞差别就是这整个模块存在的理由：一句说「不知道有没有生效，
	// 别乱重试」，一句说「压根没开始，需要就重试」。哪天有人把它们合并成一句，
	// 这条断言必须当场红掉。
	if outcomeUnknownText == notStartedText {
		t.Fatalf("两段收尾文案被写成同一句了，这个模块就白做了")
	}
	if outcomeUnknownText == "" || notStartedText == "" {
		t.Fatalf("收尾文案不许是空的")
	}
	if ToolNotStarted == ToolOutcomeUnknown {
		t.Fatalf("两个恢复码不许一样")
	}
}

func TestInterruptedTurnClosersLeavesABalancedLogAlone(t *testing.T) {
	t.Parallel()

	cases := map[string][]Event{
		"空日志": nil,
		"回合已经关掉了": {
			turnStartEvent(t, 0, 1),
			turnEndEvent(t, 1, 1),
		},
		"根本没有回合": {userMessageEvent(t, 0, "hi")},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := InterruptedTurnClosers(events)
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if len(got) != 0 {
				t.Fatalf("平衡的日志不该补出东西：%#v", got)
			}
		})
	}
}

func TestInterruptedTurnClosersClosesTheStepBeforeTheTurn(t *testing.T) {
	t.Parallel()

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
	}
	events[1].Time = 77

	got, err := InterruptedTurnClosers(events)
	if err != nil {
		t.Fatalf("补不出来：%v", err)
	}
	if len(got) != 2 {
		t.Fatalf("该补两条（步骤结束、回合结束），实际 %d 条", len(got))
	}
	if got[0].Type != EventStepEnd || got[1].Type != EventTurnEnd {
		t.Fatalf("步骤的边界必须排在回合前面：%v %v", got[0].Type, got[1].Type)
	}
	if got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("seq 该接着日志往下排：%d %d", got[0].Seq, got[1].Seq)
	}
	if got[0].Time != 77 || got[1].Time != 77 {
		t.Fatalf("时间戳该复用最后一条真事件的：%d %d", got[0].Time, got[1].Time)
	}

	var reason TurnEndData
	if err := json.Unmarshal(got[1].Data, &reason); err != nil {
		t.Fatalf("回合结束的负载读不回来：%v", err)
	}
	if reason.Reason.TurnEndReasonKind() != ReasonInterrupted {
		t.Fatalf("补出来的回合结束理由该是中途死掉，实际 %q", reason.Reason.TurnEndReasonKind())
	}
}

func TestInterruptedTurnClosersTellsAStartedCallFromAnUnstartedOne(t *testing.T) {
	t.Parallel()

	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "started", Name: "read", Arguments: "{}"},
			llm.ToolCallBlock{ID: "cold", Name: "write", Arguments: "{}"},
		}),
		toolCallEvent(t, 3, 1, 1, "started", "read"),
	}

	got, err := InterruptedTurnClosers(events)
	if err != nil {
		t.Fatalf("补不出来：%v", err)
	}
	if len(got) != 4 {
		t.Fatalf("该补四条（两条结果 + 步骤结束 + 回合结束），实际 %d 条", len(got))
	}

	started := decodeToolResult(t, got[0])
	if started.Error == nil || started.Error.Code != ToolOutcomeUnknown {
		t.Fatalf("记为开始过的调用该判结果未知：%#v", started.Error)
	}
	if text := resultText(t, started); text != outcomeUnknownText {
		t.Fatalf("结果未知的文案不对：%q", text)
	}
	if !reflect.DeepEqual(got[0].SourceEventSeqs, []int{3}) {
		t.Fatalf("记为开始过的调用该引用它那条 tool/call 的 seq：%v", got[0].SourceEventSeqs)
	}

	cold := decodeToolResult(t, got[1])
	if cold.Error == nil || cold.Error.Code != ToolNotStarted {
		t.Fatalf("没记为开始过的调用该判没开始：%#v", cold.Error)
	}
	if text := resultText(t, cold); text != notStartedText {
		t.Fatalf("没开始的文案不对：%q", text)
	}
	if got[1].SourceEventSeqs != nil {
		t.Fatalf("没开始过的调用没有可引用的来源：%v", got[1].SourceEventSeqs)
	}

	for index, closer := range got[:2] {
		if closer.SurfaceOp == nil || closer.SurfaceOp.SurfaceOpKind() != OpAppend {
			t.Fatalf("第 %d 条补写结果该是追加进表面的：%#v", index, closer.SurfaceOp)
		}
	}
}

func TestInterruptedTurnClosersKeepsTheRecordedOrder(t *testing.T) {
	t.Parallel()

	// 顺序必须是记录里的顺序，不能是 map 的遍历顺序：一份「补出来的历史每次
	// 不一样」的日志会让重放和缓存全部失效。多跑几遍，随机的顺序躲不过去。
	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "a", Name: "t", Arguments: "{}"},
			llm.ToolCallBlock{ID: "b", Name: "t", Arguments: "{}"},
			llm.ToolCallBlock{ID: "c", Name: "t", Arguments: "{}"},
			llm.ToolCallBlock{ID: "d", Name: "t", Arguments: "{}"},
			llm.ToolCallBlock{ID: "e", Name: "t", Arguments: "{}"},
		}),
	}
	want := []llm.CallID{"a", "b", "c", "d", "e"}

	for range 20 {
		got, err := InterruptedTurnClosers(events)
		if err != nil {
			t.Fatalf("补不出来：%v", err)
		}
		var order []llm.CallID
		for _, closer := range got {
			if closer.Type != EventToolResult {
				continue
			}
			result, ok := decodeToolResult(t, closer).Message.ToolResult()
			if !ok {
				t.Fatalf("补出来的不是一条工具结果")
			}
			order = append(order, result.ToolCallID)
		}
		if !reflect.DeepEqual(order, want) {
			t.Fatalf("补出来的顺序不是记录里的顺序：%v", order)
		}
	}
}

func TestInterruptedTurnClosersIsByteForByteReproducible(t *testing.T) {
	t.Parallel()

	// 补出来的消息 id 里带 seq 而不是一个新分配的 uuid，正是为了这条：
	// 同一份崩掉的日志补两次得到的字节要一样。
	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "c1", Name: "read", Arguments: "{}"},
		}),
	}

	first, err := json.Marshal(mustClosers(t, events))
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	second, err := json.Marshal(mustClosers(t, events))
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("补两次的字节不一样：\n%s\n%s", first, second)
	}

	closers := mustClosers(t, events)
	id := decodeToolResult(t, closers[0]).Message.ID
	if want := llm.MessageID("interrupted-tool-result-c1-3"); id != want {
		t.Fatalf("补出来的消息 id 不对：想要 %q，实际 %q", want, id)
	}
}

func TestInterruptedTurnClosersForgetsCallsAtEveryBoundary(t *testing.T) {
	t.Parallel()

	declare := func(seq, turn, step int, callID llm.CallID) Event {
		return assistantMessageEvent(t, seq, turn, step, llm.Content{
			llm.ToolCallBlock{ID: callID, Name: "t", Arguments: "{}"},
		})
	}

	cases := map[string][]Event{
		"结果落下来之后不再悬着": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 1),
			declare(2, 1, 1, "c1"),
			toolCallEvent(t, 3, 1, 1, "c1", "t"),
			toolResultEvent(t, 4, 1, 1, "c1", "done"),
		},
		"步骤关掉之后早先的调用漏不进来": {
			turnStartEvent(t, 0, 1),
			stepStartEvent(t, 1, 1, 1),
			declare(2, 1, 1, "c1"),
			stepEndEvent(t, 3, 1, 1),
		},
		"回合重开之后早先的调用漏不进来": {
			turnStartEvent(t, 0, 1),
			declare(1, 1, 1, "c1"),
			turnEndEvent(t, 2, 1),
			turnStartEvent(t, 3, 2),
		},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, closer := range mustClosers(t, events) {
				if closer.Type == EventToolResult {
					t.Fatalf("不该补出工具结果：%s", closer.Data)
				}
			}
		})
	}
}

func TestInterruptedTurnClosersRedeclaresACallAtTheEnd(t *testing.T) {
	t.Parallel()

	// 一个已经收到结果的 id 后来又被声明一次，它应该排在末尾而不是回到原位。
	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "a", Name: "t", Arguments: "{}"},
			llm.ToolCallBlock{ID: "b", Name: "t", Arguments: "{}"},
		}),
		toolResultEvent(t, 3, 1, 1, "a", "done"),
		assistantMessageEvent(t, 4, 1, 1, llm.Content{
			llm.ToolCallBlock{ID: "a", Name: "t", Arguments: "{}"},
		}),
	}

	var order []llm.CallID
	for _, closer := range mustClosers(t, events) {
		if closer.Type != EventToolResult {
			continue
		}
		result, _ := decodeToolResult(t, closer).Message.ToolResult()
		order = append(order, result.ToolCallID)
	}
	if want := []llm.CallID{"b", "a"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("重新声明的调用该排在末尾：想要 %v，实际 %v", want, order)
	}
}

func TestInterruptedTurnClosersOnlySeesTheToolCallBlocks(t *testing.T) {
	t.Parallel()

	// 一条助手消息里通常是「先说一句话，再要一个工具」，那句话不悬着。
	events := []Event{
		turnStartEvent(t, 0, 1),
		stepStartEvent(t, 1, 1, 1),
		assistantMessageEvent(t, 2, 1, 1, llm.Content{
			llm.TextBlock{Text: "let me read it"},
			llm.ToolCallBlock{ID: "c1", Name: "read", Arguments: "{}"},
		}),
	}

	var results int
	for _, closer := range mustClosers(t, events) {
		if closer.Type == EventToolResult {
			results++
		}
	}
	if results != 1 {
		t.Fatalf("只有那一个工具调用块该被补上，实际补了 %d 条", results)
	}
}

func TestInterruptedTurnClosersReportsABrokenPayload(t *testing.T) {
	t.Parallel()

	cases := map[string][]Event{
		// 回合结束不在这张表里：它只把两个开关拨回去，不读负载。
		"回合开始的负载坏了": {{Type: EventTurnStart, Seq: 0, Data: json.RawMessage(`7`)}},
		"步骤开始的负载坏了": {{Type: EventStepStart, Seq: 0, Data: json.RawMessage(`7`)}},
		"助手消息的负载坏了": {{Type: EventAssistantMessage, Seq: 0, Data: json.RawMessage(`7`)}},
		"工具调用的负载坏了": {{Type: EventToolCall, Seq: 0, Data: json.RawMessage(`7`)}},
		"工具结果的负载坏了": {{Type: EventToolResult, Seq: 0, Data: json.RawMessage(`7`)}},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := InterruptedTurnClosers(events); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

func TestInterruptedTurnClosersRefusesAToolResultThatIsNotFromATool(t *testing.T) {
	t.Parallel()

	strayed := toolResultEvent(t, 0, 1, 1, "c1", "done")
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

	if _, err := InterruptedTurnClosers([]Event{strayed}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestRemoveCallIDDropsEveryCopy(t *testing.T) {
	t.Parallel()

	got := removeCallID([]llm.CallID{"a", "b", "a", "c"}, "a")
	if want := []llm.CallID{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("想要 %v，实际 %v", want, got)
	}
	if got := removeCallID(nil, "a"); len(got) != 0 {
		t.Fatalf("空的顺序表摘完还是空的：%v", got)
	}
}

// mustClosers 补一遍收尾事件，补不出来就让测试当场停。
func mustClosers(t *testing.T, events []Event) []Event {
	t.Helper()

	closers, err := InterruptedTurnClosers(events)
	if err != nil {
		t.Fatalf("补不出来：%v", err)
	}
	return closers
}

// decodeToolResult 把一条工具结果事件的负载读回来。
func decodeToolResult(t *testing.T, event Event) ToolResultData {
	t.Helper()

	if event.Type != EventToolResult {
		t.Fatalf("这条不是工具结果：%q", event.Type)
	}
	var data ToolResultData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("工具结果的负载读不回来：%v", err)
	}
	return data
}

// resultText 取出一条工具结果里那唯一一段文本。
func resultText(t *testing.T, data ToolResultData) string {
	t.Helper()

	result, ok := data.Message.ToolResult()
	if !ok {
		t.Fatalf("这条消息里没有工具结果块")
	}
	if len(result.Content) != 1 {
		t.Fatalf("补出来的结果该只有一块内容，实际 %d 块", len(result.Content))
	}
	text, ok := result.Content[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("补出来的那块不是文本：%T", result.Content[0])
	}
	return text.Text
}

// turnStartEvent 排一条回合开始事件出来。
func turnStartEvent(t *testing.T, seq, turn int) Event {
	t.Helper()

	payload, err := json.Marshal(TurnStartData{Turn: turn})
	if err != nil {
		t.Fatalf("回合开始负载排不出去：%v", err)
	}
	return Event{Type: EventTurnStart, Seq: seq, Data: payload}
}

// turnEndEvent 排一条正常完成的回合结束事件出来。
func turnEndEvent(t *testing.T, seq, turn int) Event {
	t.Helper()

	payload, err := json.Marshal(TurnEndData{Turn: turn, Reason: CompletedTurnEnd{}})
	if err != nil {
		t.Fatalf("回合结束负载排不出去：%v", err)
	}
	return Event{Type: EventTurnEnd, Seq: seq, Data: payload}
}

// stepStartEvent 排一条步骤开始事件出来。
func stepStartEvent(t *testing.T, seq, turn, step int) Event {
	t.Helper()

	payload, err := json.Marshal(StepStartData{Turn: turn, Step: step})
	if err != nil {
		t.Fatalf("步骤开始负载排不出去：%v", err)
	}
	return Event{Type: EventStepStart, Seq: seq, Data: payload}
}

// stepEndEvent 排一条步骤结束事件出来。
func stepEndEvent(t *testing.T, seq, turn, step int) Event {
	t.Helper()

	payload, err := json.Marshal(StepEndData{Turn: turn, Step: step})
	if err != nil {
		t.Fatalf("步骤结束负载排不出去：%v", err)
	}
	return Event{Type: EventStepEnd, Seq: seq, Data: payload}
}

// toolCallEvent 排一条工具调用事件出来。
func toolCallEvent(t *testing.T, seq, turn, step int, callID llm.CallID, name string) Event {
	t.Helper()

	payload, err := json.Marshal(ToolCallData{
		Turn: turn, Step: step, CallID: callID, Name: name, Arguments: "{}",
	})
	if err != nil {
		t.Fatalf("工具调用负载排不出去：%v", err)
	}
	return Event{Type: EventToolCall, Seq: seq, Data: payload}
}
