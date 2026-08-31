// 本文件的作用：本包测试共用的那几样：一个假会话，加上造各种事件的小工具。

package tokenmeter

import (
	"encoding/json"
	"testing"

	"ds-harness-go/compaction"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// fakeSession 是一个只把日志摆在那儿的会话，满足 projection.SessionView。
type fakeSession struct {
	id     session.SessionID
	events []session.Event
}

func (s *fakeSession) ID() session.SessionID { return s.id }

func (s *fakeSession) Events() []session.Event { return s.events }

func (s *fakeSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}

// newSession 造一个假会话，事件的 seq 就是它们的下标。
func newSession(events ...session.Event) *fakeSession {
	for index := range events {
		events[index].Seq = index
	}
	return &fakeSession{id: "s", events: events}
}

// append 往假会话后面再追几条，seq 接着排。
func (s *fakeSession) append(events ...session.Event) {
	for _, event := range events {
		event.Seq = len(s.events)
		s.events = append(s.events, event)
	}
}

// mustJSON 把负载排出去，排不出去就判测试失败。
func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return raw
}

// textMessage 造一条只有一块文本的消息。
func textMessage(id string, role llm.Role, source llm.MessageSource, text string) llm.Message {
	return llm.Message{
		ID:      llm.MessageID(id),
		Role:    role,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  source,
	}
}

// userEvent 造一条追加进表面的用户消息。
func userEvent(t *testing.T, text string) session.Event {
	t.Helper()

	return session.Event{
		Type:      session.EventUserMessage,
		Data:      mustJSON(t, session.UserMessageData{Message: textMessage("u"+text, llm.RoleUser, llm.UserSource{}, text)}),
		SurfaceOp: session.AppendOp{},
	}
}

// assistantEvent 造一条追加进表面的助手消息。usage 为 nil 表示适配器没报记账。
func assistantEvent(t *testing.T, turn, step int, text string, usage *llm.TokenUsage) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventAssistantMessage,
		Data: mustJSON(t, session.AssistantMessageData{
			Turn: turn, Step: step,
			Message: textMessage("a"+text, llm.RoleAssistant, llm.ModelSource{}, text),
			Usage:   usage,
		}),
		SurfaceOp: session.AppendOp{},
	}
}

// chunkEvent 造一条助手分块事件。它不上表面。
func chunkEvent(t *testing.T, turn, step int, chunk llm.StreamChunk) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventAssistantChunk,
		Data: mustJSON(t, session.AssistantChunkData{Turn: turn, Step: step, Chunk: chunk}),
	}
}

// textChunks 造出「开块、一段文本、关块」这三条分块事件。
func textChunks(t *testing.T, turn, step int, text string) []session.Event {
	t.Helper()

	return []session.Event{
		chunkEvent(t, turn, step, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}),
		chunkEvent(t, turn, step, llm.TextDeltaChunk{Index: 0, Text: text}),
		chunkEvent(t, turn, step, llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: text}}),
	}
}

// stepStartEvent 造一条 step/start。
func stepStartEvent(t *testing.T, turn, step int) session.Event {
	t.Helper()

	return session.Event{Type: session.EventStepStart, Data: mustJSON(t, session.StepStartData{Turn: turn, Step: step})}
}

// stepEndEvent 造一条 step/end。
func stepEndEvent(t *testing.T, turn, step int) session.Event {
	t.Helper()

	return session.Event{Type: session.EventStepEnd, Data: mustJSON(t, session.StepEndData{Turn: turn, Step: step})}
}

// headerEvent 造一条 request/header。
func headerEvent(t *testing.T, header session.EpochHeader) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventRequestHeader,
		Data: mustJSON(t, session.RequestHeaderData{Header: header, Reason: session.HeaderInitial}),
	}
}

// contextEvent 造一条 request/context。
func contextEvent(t *testing.T, window int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventRequestContext,
		Data: mustJSON(t, session.RequestContextData{RequestContext: session.RequestContext{
			Provider: "p", Model: "m", ContextWindow: window,
		}}),
	}
}

// summaryEvent 造一条举起影子价的 compaction/summary。
func summaryEvent(t *testing.T, start, end, tokens int) session.Event {
	t.Helper()

	return session.Event{
		Type: compaction.EventCompactionSummary,
		Data: mustJSON(t, compaction.SummaryData{
			CompactionID:       "c1",
			Summary:            llm.Content{llm.TextBlock{Text: "摘要"}},
			ShadowedRange:      compaction.ShadowedRange{Start: start, End: end},
			ShadowedSeqs:       []int{start, end},
			ShadowedTokenCount: tokens,
			Provider:           "p",
			Model:              "m",
		}),
	}
}

// replacementEvent 造一条把 start-end 换成一条用户消息的替换事件。
func replacementEvent(t *testing.T, start, end int, text string) session.Event {
	t.Helper()

	sources := make([]int, 0, end-start+1)
	for seq := start; seq <= end; seq++ {
		sources = append(sources, seq)
	}
	event := userEvent(t, text)
	event.SurfaceOp = session.ReplaceOp{Start: start, End: end}
	event.SourceEventSeqs = sources
	return event
}

// simpleHeader 造一份带系统提示的请求头。
func simpleHeader(system string) session.EpochHeader {
	return session.EpochHeader{System: system}
}
