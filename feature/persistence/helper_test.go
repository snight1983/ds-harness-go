// 本文件的作用：本包测试共用的那几个事件和头的搭建器。
//
// 源: packages/session/session-persistence/src/coordinator.ts

package persistence

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// testHeader 造一份版本正确的存储头。
func testHeader(t testing.TB, id sessionlog.SessionID) sessionlog.SessionHeader {
	t.Helper()

	return sessionlog.SessionHeader{
		Version:   sessionlog.FormatVersion,
		ID:        id,
		CreatedAt: 1,
	}
}

// userEvent 排一条用户消息事件出来。
func userEvent(t testing.TB, seq int, text string) sessionlog.Event {
	t.Helper()

	payload, err := json.Marshal(sessionlog.UserMessageData{Message: llm.Message{
		ID:      llm.MessageID("u" + text),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}})
	if err != nil {
		t.Fatalf("用户消息负载排不出去：%v", err)
	}
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Seq:       seq,
		Time:      int64(seq),
		Data:      payload,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// turnStart 排一条回合开始事件出来。
func turnStart(t testing.TB, seq, turn int) sessionlog.Event {
	t.Helper()

	payload, err := json.Marshal(sessionlog.TurnStartData{Turn: turn})
	if err != nil {
		t.Fatalf("回合开始负载排不出去：%v", err)
	}
	return sessionlog.Event{Type: sessionlog.EventTurnStart, Seq: seq, Time: int64(seq), Data: payload}
}

// turnEnd 排一条正常完成的回合结束事件出来。
func turnEnd(t testing.TB, seq, turn int) sessionlog.Event {
	t.Helper()

	payload, err := json.Marshal(sessionlog.TurnEndData{
		Turn: turn, Reason: sessionlog.CompletedTurnEnd{},
	})
	if err != nil {
		t.Fatalf("回合结束负载排不出去：%v", err)
	}
	return sessionlog.Event{Type: sessionlog.EventTurnEnd, Seq: seq, Time: int64(seq), Data: payload}
}
