// 本文件的作用：本包测试共用的事件构造。
//
// 压缩的每条不变量都要在一段**日志**上才说得清（括号配对、归属、相邻的计价），
// 所以这里的构造器都按「一条一条排出去，seq 由调用方给」的样子写，
// 让每个用例自己拼出它要验的那种形状。

package compaction

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// marshalPayload 把一个负载排成事件字节。
func marshalPayload(t *testing.T, payload any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return data
}

// eventAt 造一条追加进表面的事件。
func eventAt(seq int, kind session.EventType, data json.RawMessage) session.Event {
	return session.Event{
		Type:      kind,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      data,
		SurfaceOp: session.AppendOp{},
	}
}

// logEventAt 造一条**不上表面**的事件——四条 compaction/* 和几种边界都走这里。
func logEventAt(seq int, kind session.EventType, data json.RawMessage) session.Event {
	return session.Event{Type: kind, Seq: seq, Time: int64(seq) * 1000, Data: data}
}

// turnStart 排一条 turn/start。
func turnStart(t *testing.T, seq int, turn int) session.Event {
	t.Helper()

	return logEventAt(seq, session.EventTurnStart,
		marshalPayload(t, session.TurnStartData{Turn: turn}))
}

// turnEnd 排一条 turn/end。
//
// 负载是一段裸 JSON：本包从头到尾不解这条事件的负载，只看它的类型。
func turnEnd(seq int) session.Event {
	return logEventAt(seq, session.EventTurnEnd, json.RawMessage(`{"turn":1}`))
}

// endSeed 排一条 session/end-seed。
func endSeed(seq int) session.Event {
	return logEventAt(seq, session.EventSessionEndSeed, json.RawMessage(`{}`))
}

// compactionStart 排一条 compaction/start。
func compactionStart(t *testing.T, seq int, data StartData) session.Event {
	t.Helper()

	return logEventAt(seq, EventCompactionStart, marshalPayload(t, data))
}

// compactionSummary 排一条 compaction/summary。
func compactionSummary(t *testing.T, seq int, data SummaryData) session.Event {
	t.Helper()

	return logEventAt(seq, EventCompactionSummary, marshalPayload(t, data))
}

// compactionEnd 排一条 compaction/end。
func compactionEnd(t *testing.T, seq int, data EndData) session.Event {
	t.Helper()

	return logEventAt(seq, EventCompactionEnd, marshalPayload(t, data))
}

// summaryOf 造一条最简单的、能过全部字段检查的摘要负载。
func summaryOf(id ID, shadowed ...int) SummaryData {
	return SummaryData{
		CompactionID: id,
		Summary:      llm.Content{llm.TextBlock{Text: "摘要"}},
		ShadowedRange: ShadowedRange{
			Start: shadowed[0],
			End:   shadowed[len(shadowed)-1],
		},
		ShadowedSeqs:       shadowed,
		ShadowedTokenCount: 100,
		Provider:           "openai",
		Model:              "gpt-4o",
	}
}

// userText 排一条用户自己说的话，追加进表面。
func userText(t *testing.T, seq int, text string) session.Event {
	t.Helper()

	return eventAt(seq, session.EventUserMessage, marshalPayload(t, session.UserMessageData{
		Message: llm.Message{
			ID:      llm.MessageID("u"),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.UserSource{},
		},
	}))
}

// replacementWithSource 排一条替换表面的用户消息，来源逐字由调用方给。
//
// 来源单独给，是因为本包在这条事件上验的就是那点出处：不是检查点的替换要原样
// 放过，是检查点的要对上当前开着的那次压缩。
func replacementWithSource(t *testing.T, seq int, start, end int, source llm.MessageSource) session.Event {
	t.Helper()

	event := eventAt(seq, session.EventUserMessage, marshalPayload(t, session.UserMessageData{
		Message: llm.Message{
			ID:      llm.MessageID("c"),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: "摘要"}},
			Source:  source,
		},
	}))
	event.SurfaceOp = session.ReplaceOp{Start: start, End: end}
	return event
}

// checkpointReplacement 排一条盖着本包检查点标记的替换消息。
func checkpointReplacement(t *testing.T, seq int, start, end int, checkpoint CheckpointSource) session.Event {
	t.Helper()

	source, err := NewCheckpointSource(checkpoint)
	if err != nil {
		t.Fatalf("检查点出处造不出来：%v", err)
	}
	return replacementWithSource(t, seq, start, end, source)
}

// assistantCalls 排一条带 n 次工具调用的助手消息。
func assistantCalls(t *testing.T, seq int, calls int) session.Event {
	t.Helper()

	content := llm.Content{llm.TextBlock{Text: "我来查一下"}}
	for i := range calls {
		content = append(content, llm.ToolCallBlock{
			ID:        llm.CallID("call-" + string(rune('a'+i))),
			Name:      "read",
			Arguments: `{}`,
		})
	}
	return eventAt(seq, session.EventAssistantMessage, marshalPayload(t, session.AssistantMessageData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "a",
			Role:    llm.RoleAssistant,
			Content: content,
			Source:  llm.ModelSource{},
		},
	}))
}

// toolResult 排一条工具结果。
func toolResult(t *testing.T, seq int) session.Event {
	t.Helper()

	return eventAt(seq, session.EventToolResult, marshalPayload(t, session.ToolResultData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "t",
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: "工具结果"}},
			Source:  llm.ToolSource{CallID: "call-a"},
		},
	}))
}

// viewOf 把一串事件拼成一份表面视图，节点就是这些事件本身，代数由调用方给。
func viewOf(generation int, events ...session.Event) SurfaceView {
	nodes := make([]int, 0, len(events))
	for _, event := range events {
		nodes = append(nodes, event.Seq)
	}
	base := 0
	if len(events) > 0 {
		base = events[0].Seq
	}
	return SurfaceView{Nodes: nodes, Generation: generation, Events: events, BaseSeq: base}
}
