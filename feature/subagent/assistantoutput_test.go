// 本文件的作用：「一个孩子最后说了什么」那条选法的测试——最后一条非空助手消息
// 说了算，没有就退到攒下来的流式文本，坏负载不贡献也不报错。

package subagent

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestFinalAssistantOutputPrefersTheLastNonEmptyMessage(t *testing.T) {
	events := []sessionlog.Event{
		assistantMessage(t, 1, textContent("第一段")),
		assistantMessage(t, 2, textContent("第二段")),
	}
	if got := textOf(FinalAssistantOutput(events)); got != "第二段" {
		t.Fatalf("该选最后那条非空消息，实际 %q", got)
	}
}

// 一条内容为空的助手消息只在「循环在一个撞了 token 天花板的步骤之后补记用量」时
// 出现，它绝不该顶掉更早那份真的输出。
func TestFinalAssistantOutputIgnoresEmptyMessages(t *testing.T) {
	events := []sessionlog.Event{
		assistantMessage(t, 1, textContent("真的输出")),
		assistantMessage(t, 2, nil),
	}
	if got := textOf(FinalAssistantOutput(events)); got != "真的输出" {
		t.Fatalf("空消息不该顶掉更早的输出，实际 %q", got)
	}
}

func TestFinalAssistantOutputFallsBackToStreamedText(t *testing.T) {
	events := []sessionlog.Event{
		event(t, sessionlog.EventAssistantChunk, sessionlog.AssistantChunkData{
			Turn: 1, Step: 0, Chunk: llm.TextDeltaChunk{Text: "前"},
		}),
		event(t, sessionlog.EventAssistantChunk, sessionlog.AssistantChunkData{
			Turn: 1, Step: 0, Chunk: llm.TextDeltaChunk{Text: "后"},
		}),
	}
	if got := textOf(FinalAssistantOutput(events)); got != "前后" {
		t.Fatalf("一条非空消息都没有时该拼流式文本，实际 %q", got)
	}
}

// 有了终稿之后，流式兜底就不再被读——攒下来的片段是同一段话的碎片，拼上去会重复。
func TestFinalAssistantOutputPrefersMessageOverStream(t *testing.T) {
	events := []sessionlog.Event{
		event(t, sessionlog.EventAssistantChunk, sessionlog.AssistantChunkData{
			Turn: 1, Step: 0, Chunk: llm.TextDeltaChunk{Text: "碎片"},
		}),
		assistantMessage(t, 1, textContent("终稿")),
	}
	if got := textOf(FinalAssistantOutput(events)); got != "终稿" {
		t.Fatalf("有终稿就不该退到流式兜底，实际 %q", got)
	}
}

func TestFinalAssistantOutputIsNilWhenNothingWasProduced(t *testing.T) {
	if output := FinalAssistantOutput(nil); output != nil {
		t.Fatalf("什么都没有时该交回 nil，实际 %#v", output)
	}
	// 一条不贡献的事件（回合边界）也不该凭空造出一份输出。
	if output := FinalAssistantOutput(steppedTurn(t, 1, sessionlog.CompletedTurnEnd{})); output != nil {
		t.Fatalf("回合边界不该贡献输出，实际 %#v", output)
	}
}

// 负载读不出来在这里当**没贡献**处理：判日志成不成立是 session 那道边界的事，
// 在这条选法里报错只会让一次本来能收场的运行多一种收不了场的方式。
func TestFinalAssistantOutputSkipsUndecodablePayloads(t *testing.T) {
	events := []sessionlog.Event{
		assistantMessage(t, 1, textContent("好的那条")),
		{Type: sessionlog.EventAssistantMessage, Data: json.RawMessage(`{"turn":`)},
		{Type: sessionlog.EventAssistantChunk, Data: json.RawMessage(`not json`)},
	}
	if got := textOf(FinalAssistantOutput(events)); got != "好的那条" {
		t.Fatalf("坏负载该被跳过，实际 %q", got)
	}
}

// 非文本增量（比如推理块的增量）不进流式兜底：兜底攒的是助手**说出来**的文本。
func TestAssistantOutputFoldIgnoresNonTextChunks(t *testing.T) {
	var fold AssistantOutputFold
	fold.Push(event(t, sessionlog.EventAssistantChunk, sessionlog.AssistantChunkData{
		Turn: 1, Step: 0, Chunk: llm.ReasoningDeltaChunk{Text: "想了想"},
	}))
	if output := fold.Collect(); output != nil {
		t.Fatalf("非文本增量不该贡献输出，实际 %#v", output)
	}
}

func TestAssistantOutputFoldPushTextIgnoresEmptyFragments(t *testing.T) {
	var fold AssistantOutputFold
	fold.PushText("")
	if output := fold.Collect(); output != nil {
		t.Fatalf("空片段不该造出输出，实际 %#v", output)
	}
	fold.PushText("有货")
	if got := textOf(fold.Collect()); got != "有货" {
		t.Fatalf("非空片段该被攒下来，实际 %q", got)
	}
}

// 不认识的事件类型一律不贡献。
func TestAssistantOutputFoldIgnoresUnrelatedEvents(t *testing.T) {
	var fold AssistantOutputFold
	fold.Push(sessionlog.Event{Type: "test/unrelated", Data: json.RawMessage(`{}`)})
	if output := fold.Collect(); output != nil {
		t.Fatalf("不相干的事件不该贡献输出，实际 %#v", output)
	}
}
