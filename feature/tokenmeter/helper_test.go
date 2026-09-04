// 本文件的作用：本包测试共用的那几样：一个假会话，加上造各种事件的小工具。

package tokenmeter

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// fakeSession 是一个只把日志摆在那儿的会话，满足 projection.SessionView。
type fakeSession struct {
	id     sessionlog.SessionID
	events []sessionlog.Event
}

func (s *fakeSession) ID() sessionlog.SessionID { return s.id }

func (s *fakeSession) Events() []sessionlog.Event { return s.events }

func (s *fakeSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}

// newSession 造一个假会话，事件的 seq 就是它们的下标。
func newSession(events ...sessionlog.Event) *fakeSession {
	return trimmedSession(0, events...)
}

// trimmedSession 造一个假会话，日志的起点是 base：一份从最老的一头被弹掉一截的
// 日志（见 docs/session-log-limit.md）读回来就是这个样子，seq 不再等于下标。
func trimmedSession(base int, events ...sessionlog.Event) *fakeSession {
	for index := range events {
		events[index].Seq = base + index
	}
	return &fakeSession{id: "s", events: events}
}

// append 往假会话后面再追几条，seq 接着排。
func (s *fakeSession) append(events ...sessionlog.Event) {
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
func userEvent(t *testing.T, text string) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      mustJSON(t, sessionlog.UserMessageData{Message: textMessage("u"+text, llm.RoleUser, llm.UserSource{}, text)}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// assistantEvent 造一条追加进表面的助手消息。usage 为 nil 表示适配器没报记账。
func assistantEvent(t *testing.T, turn, step int, text string, usage *llm.TokenUsage) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventAssistantMessage,
		Data: mustJSON(t, sessionlog.AssistantMessageData{
			Turn: turn, Step: step,
			Message: textMessage("a"+text, llm.RoleAssistant, llm.ModelSource{}, text),
			Usage:   usage,
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// chunkEvent 造一条助手分块事件。它不上表面。
func chunkEvent(t *testing.T, turn, step int, chunk llm.StreamChunk) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventAssistantChunk,
		Data: mustJSON(t, sessionlog.AssistantChunkData{Turn: turn, Step: step, Chunk: chunk}),
	}
}

// textChunks 造出「开块、一段文本、关块」这三条分块事件。
func textChunks(t *testing.T, turn, step int, text string) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{
		chunkEvent(t, turn, step, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}),
		chunkEvent(t, turn, step, llm.TextDeltaChunk{Index: 0, Text: text}),
		chunkEvent(t, turn, step, llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: text}}),
	}
}

// stepStartEvent 造一条 step/start。
func stepStartEvent(t *testing.T, turn, step int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{Type: sessionlog.EventStepStart, Data: mustJSON(t, sessionlog.StepStartData{Turn: turn, Step: step})}
}

// stepEndEvent 造一条 step/end。
func stepEndEvent(t *testing.T, turn, step int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{Type: sessionlog.EventStepEnd, Data: mustJSON(t, sessionlog.StepEndData{Turn: turn, Step: step})}
}

// headerEvent 造一条 request/header。
func headerEvent(t *testing.T, header sessionlog.EpochHeader) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventRequestHeader,
		Data: mustJSON(t, sessionlog.RequestHeaderData{Header: header, Reason: sessionlog.HeaderInitial}),
	}
}

// contextEvent 造一条 request/context。
func contextEvent(t *testing.T, window int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventRequestContext,
		Data: mustJSON(t, sessionlog.RequestContextData{RequestContext: sessionlog.RequestContext{
			Provider: "p", Model: "m", ContextWindow: window,
		}}),
	}
}

// summaryEvent 造一条举起影子价的 compaction/summary。
func summaryEvent(t *testing.T, start, end, tokens int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
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
func replacementEvent(t *testing.T, start, end int, text string) sessionlog.Event {
	t.Helper()

	sources := make([]int, 0, end-start+1)
	for seq := start; seq <= end; seq++ {
		sources = append(sources, seq)
	}
	event := userEvent(t, text)
	event.SurfaceOp = sessionlog.ReplaceOp{Start: start, End: end}
	event.SourceEventSeqs = sources
	return event
}

// simpleHeader 造一份带系统提示的请求头。
func simpleHeader(system string) sessionlog.EpochHeader {
	return sessionlog.EpochHeader{System: system}
}
