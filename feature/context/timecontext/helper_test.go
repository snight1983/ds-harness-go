// 本文件的作用：本包测试共用的事件构造与几个现成时区。
//
// 这里 import 了 time/tzdata：本包自己不带时区库（理由写在 doc.go 里），
// 但测试要用 Asia/Shanghai 这类真名字来验偏移量，而跑测试的机器不一定有
// zoneinfo。把嵌入版本放在测试二进制里，谁跑都是同一份数据。
package timecontext

import (
	"encoding/json"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// mustLoad 加载一个时区，加载不出来就直接让测试失败。
func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()

	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("时区 %q 加载不出来：%v", name, err)
	}
	return location
}

// marshalPayload 把一个负载排成事件字节。
func marshalPayload(t *testing.T, payload any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return data
}

// eventAt 造一条只有类型、序号和时刻的事件，负载由调用方给。
func eventAt(seq int, kind sessionlog.EventType, data json.RawMessage) sessionlog.Event {
	return sessionlog.Event{
		Type:      kind,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      data,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// turnStart 排一条 turn/start。
func turnStart(t *testing.T, seq int, turn int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventTurnStart, marshalPayload(t, sessionlog.TurnStartData{Turn: turn}))
}

// stepStart 排一条 step/start。
func stepStart(t *testing.T, seq int, turn int, step int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventStepStart,
		marshalPayload(t, sessionlog.StepStartData{Turn: turn, Step: step}))
}

// stepEnd 排一条 step/end。
func stepEnd(t *testing.T, seq int, turn int, step int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventStepEnd,
		marshalPayload(t, sessionlog.StepEndData{Turn: turn, Step: step}))
}

// turnEnd 排一条 turn/end。
//
// 负载给的是一段裸 JSON 而不是 [sessionlog.TurnEndData]：本包从头到尾不解这条
// 事件的负载，只看它的类型，而那个负载要求带一个结束理由。
func turnEnd(seq int) sessionlog.Event {
	return eventAt(seq, sessionlog.EventTurnEnd, json.RawMessage(`{"turn":1}`))
}

// requestHeader 排一条 request/header，理由同 [turnEnd]，负载是裸的。
func requestHeader(seq int) sessionlog.Event {
	return eventAt(seq, sessionlog.EventRequestHeader, json.RawMessage(`{}`))
}

// userText 排一条用户自己说的话。
func userText(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("u"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	})
}

// otherPluginText 排一条**别的**插件注入的用户消息，它不是本包的读数。
func otherPluginText(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("p"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.PluginSource{Plugin: "workspace-instructions"},
	})
}

// assistantText 排一条助手消息。
func assistantText(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventAssistantMessage, marshalPayload(t, sessionlog.AssistantMessageData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "a",
			Role:    llm.RoleAssistant,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.ModelSource{},
		},
	}))
}

// toolResult 排一条工具结果。
func toolResult(t *testing.T, seq int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventToolResult, marshalPayload(t, sessionlog.ToolResultData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:      "t",
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: "工具结果"}},
			Source:  llm.ToolSource{CallID: "call-1"},
		},
	}))
}

// messageEvent 把一条用户角色消息包成事件。
func messageEvent(t *testing.T, seq int, message llm.Message) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventUserMessage, marshalPayload(t, sessionlog.UserMessageData{Message: message}))
}

// readingAt 排一条本包写下的读数，正文由 [RenderText] 现排，时刻单独给。
//
// 落库时刻取 seq*1000，采样时刻由调用方定，所以既能造出一条合规的读数，
// 也能造出「采样晚于落库」这类只有伪造才会出现的形状。
func readingAt(t *testing.T, seq int, reading Reading, location *time.Location) sessionlog.Event {
	t.Helper()

	return readingWithText(t, seq, RenderText(reading, location))
}

// readingWithText 排一条本包署名的读数，正文逐字由调用方给。
func readingWithText(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("r"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  ReadingSource(text),
	})
}

// at 造一个 UTC 时刻，省得每条测试都写一遍 time.Date 的七个参数。
func at(seconds int64) time.Time {
	return time.UnixMilli(seconds * 1000).UTC()
}
