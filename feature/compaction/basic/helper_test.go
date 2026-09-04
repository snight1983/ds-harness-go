// 本文件的作用：本包测试共用的构造。
//
// 挑区间和读日志尾巴这两件事都要在一段**真的事件**上才说得清（表面上哪些节点、
// 哪一刀配平、日志尾巴上还有没有开着的括号），所以这里的构造器都按
// 「一条一条排出去，seq 由调用方给」的样子写。

package basic

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// intOf 交出一个指向该值的指针，给那几个「零有意义」的配置字段用。
func intOf(value int) *int { return &value }

// boolOf 交出一个指向该值的指针，给 [Config.Auto] 用。
func boolOf(value bool) *bool { return &value }

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
func eventAt(seq int, kind sessionlog.EventType, data json.RawMessage) sessionlog.Event {
	return sessionlog.Event{
		Type:      kind,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      data,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// logEventAt 造一条**不上表面**的事件。
func logEventAt(seq int, kind sessionlog.EventType, data json.RawMessage) sessionlog.Event {
	return sessionlog.Event{Type: kind, Seq: seq, Time: int64(seq) * 1000, Data: data}
}

// turnStart 排一条 turn/start。
func turnStart(t *testing.T, seq int, turn int) sessionlog.Event {
	t.Helper()

	return logEventAt(seq, sessionlog.EventTurnStart,
		marshalPayload(t, sessionlog.TurnStartData{Turn: turn}))
}

// turnEnd 排一条 turn/end。
//
// 负载是一段裸 JSON：本包从头到尾不解这条事件的负载，只看它的类型。
func turnEnd(seq int) sessionlog.Event {
	return logEventAt(seq, sessionlog.EventTurnEnd, json.RawMessage(`{"turn":1}`))
}

// endSeed 排一条 session/end-seed。
func endSeed(seq int) sessionlog.Event {
	return logEventAt(seq, sessionlog.EventSessionEndSeed, json.RawMessage(`{}`))
}

// compactionStart 排一条 compaction/start。
func compactionStart(t *testing.T, seq int, turn int) sessionlog.Event {
	t.Helper()

	return logEventAt(seq, compaction.EventCompactionStart,
		marshalPayload(t, compaction.StartData{CompactionID: "c-1", Turn: turn}))
}

// compactionEnd 排一条 compaction/end。
func compactionEnd(t *testing.T, seq int, turn int) sessionlog.Event {
	t.Helper()

	return logEventAt(seq, compaction.EventCompactionEnd,
		marshalPayload(t, compaction.EndData{CompactionID: "c-1", Turn: turn}))
}

// userText 排一条用户自己说的话，追加进表面。
func userText(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventUserMessage, marshalPayload(t, sessionlog.UserMessageData{
		Message: llm.Message{
			ID:      llm.MessageID("u"),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.UserSource{},
		},
	}))
}

// assistantCalls 排一条带 n 次工具调用的助手消息。
func assistantCalls(t *testing.T, seq int, calls int) sessionlog.Event {
	t.Helper()

	content := llm.Content{llm.TextBlock{Text: "我来查一下"}}
	for i := range calls {
		content = append(content, llm.ToolCallBlock{
			ID:        llm.CallID("call-" + string(rune('a'+i))),
			Name:      "read",
			Arguments: `{}`,
		})
	}
	return eventAt(seq, sessionlog.EventAssistantMessage, marshalPayload(t, sessionlog.AssistantMessageData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "a",
			Role:    llm.RoleAssistant,
			Content: content,
			Source:  llm.ModelSource{},
		},
	}))
}

// toolResult 排一条工具结果，配对 assistantCalls 里第 index 次调用。
func toolResult(t *testing.T, seq int, index int) sessionlog.Event {
	t.Helper()

	callID := llm.CallID("call-" + string(rune('a'+index)))
	return eventAt(seq, sessionlog.EventToolResult, marshalPayload(t, sessionlog.ToolResultData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "t",
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: "工具结果"}},
			Source:  llm.ToolSource{CallID: callID},
		},
	}))
}

// viewOf 把一串事件拼成一份表面视图，节点就是这些事件本身。
func viewOf(events ...sessionlog.Event) compaction.SurfaceView {
	nodes := make([]int, 0, len(events))
	for _, event := range events {
		nodes = append(nodes, event.Seq)
	}
	base := 0
	if len(events) > 0 {
		base = events[0].Seq
	}
	return compaction.SurfaceView{Nodes: nodes, Events: events, BaseSeq: base}
}

// pricedAll 给一份表面上每个节点都定同一个价。
func pricedAll(view compaction.SurfaceView, tokens int) []PricedNode {
	priced := make([]PricedNode, 0, len(view.Nodes))
	for _, seq := range view.Nodes {
		priced = append(priced, PricedNode{Seq: seq, Tokens: tokens})
	}
	return priced
}
