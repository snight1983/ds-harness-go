// 本文件的作用：这一包测试共用的造事件、造存储、造作用域的小工具。

package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// testAbsolutePath 是一条在本机上确实绝对的路径。
//
// 不能写死 `/tmp` 或者 `C:\tmp` 这样的字面量：validateSessionHeader 用的是
// filepath.IsAbs，它跟着平台走，写死哪一边都会让另一边的测试变成假通过。
var testAbsolutePath = filepath.Join(os.TempDir(), "ds-harness-go-session-test")

// data 把一份负载排成字节，排不出去当场失败。
func data(t testing.TB, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// userEvent 造一条上表面的用户消息事件。
func userEvent(t testing.TB, text string) sessionlog.Event {
	t.Helper()
	message := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      data(t, sessionlog.UserMessageData{Message: message}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// assistantEvent 造一条上表面的助手消息事件。
func assistantEvent(t testing.TB, turn, step int, text string) sessionlog.Event {
	t.Helper()
	content := llm.Content{llm.TextBlock{Text: text}}
	if text == "" {
		content = nil
	}
	message := llm.NewAssistantMessage(content, llm.Provenance{Provider: "p", Model: "m"})
	return sessionlog.Event{
		Type: sessionlog.EventAssistantMessage,
		Data: data(t, sessionlog.AssistantMessageData{
			Turn: turn, Step: step, Message: message,
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// toolResultEvent 造一条上表面的工具结果事件。
func toolResultEvent(t testing.TB, turn, step int, callID llm.CallID) sessionlog.Event {
	t.Helper()
	message := llm.NewToolResultMessage(callID, llm.Content{llm.TextBlock{Text: "ok"}}, false)
	return sessionlog.Event{
		Type: sessionlog.EventToolResult,
		Data: data(t, sessionlog.ToolResultData{
			Turn: turn, Step: step, Message: message,
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// headerEvent 造一条请求头快照事件。
func headerEvent(t testing.TB, provider, model string) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventRequestHeader,
		Data: data(t, sessionlog.RequestHeaderData{
			Header: sessionlog.EpochHeader{
				Config: llm.CallConfig{Provider: provider, Model: model},
			},
			Reason: sessionlog.HeaderInitial,
		}),
	}
}

// turnStart 造一条回合开始事件。
func turnStart(turn int) sessionlog.Event {
	return sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: json.RawMessage(`{"turn":` + itoa(turn) + `}`),
	}
}

// turnEnd 造一条回合结束事件。本包不解它的负载，所以只填回合号就够。
func turnEnd(turn int) sessionlog.Event {
	return sessionlog.Event{
		Type: sessionlog.EventTurnEnd,
		Data: json.RawMessage(`{"turn":` + itoa(turn) + `}`),
	}
}

// itoa 是给上面两个字面量拼装用的，避免为了一个数字引进 strconv。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// seedOf 把一串事件按下标盖上 seq，凑成一份合法的构造 seed。
func seedOf(events ...sessionlog.Event) []sessionlog.Event {
	seed := make([]sessionlog.Event, len(events))
	for index, event := range events {
		event.Seq = index
		seed[index] = event
	}
	return seed
}

// fixedClock 是一个走得可预测的时钟：每读一次加一毫秒。
//
// 用原子加而不是裸变量：一个存储的时钟会被并发的创建同时读到（[Store.Prepare]
// 在锁外调它），一个真实的时钟本来就得并发安全，测试用的这个也一样。
func fixedClock() func() int64 {
	tick := int64(1000)
	return func() int64 {
		return atomic.AddInt64(&tick, 1)
	}
}

// newStore 造一个用固定时钟的空存储。
func newStore(t testing.TB) *Store {
	t.Helper()
	store, err := NewStore(StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造存储失败：%v", err)
	}
	return store
}

// rootScope 造一个没有身份的作用域，用完自动释放。
func rootScope(t testing.TB) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// agentScope 造一个有身份的作用域，用完自动释放。
func agentScope(t testing.TB, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// liveSession 在存储里建一个已公布的会话。
func liveSession(t testing.TB, store *Store, owner *scope.Scope, id sessionlog.SessionID, options CreateOptions) *Session {
	t.Helper()
	session, err := store.Create(context.Background(), owner, id, options)
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	return session
}
