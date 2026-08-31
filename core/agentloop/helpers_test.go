// 本文件的作用：本包测试共用的那些小工具——一个走得可预测的时钟、一个空的
// 会话存储、几种作用域，以及把负载排成事件的那几个造型函数。

package agentloop

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// fixedClock 是一个走得可预测的时钟：每读一次加一毫秒。
//
// 用原子加而不是裸变量，理由和 core/session 那边同名的工具一样：存储在锁外面
// 读它，一个真实的时钟本来就得并发安全。
func fixedClock() func() int64 {
	tick := int64(1000)
	return func() int64 {
		return atomic.AddInt64(&tick, 1)
	}
}

// newStore 造一个用固定时钟的空会话存储。
func newStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore(session.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	return store
}

// rootScope 造一个没有身份的根作用域，用完自动释放。
func rootScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// namedScope 造一个有身份的作用域，用完自动释放。
func namedScope(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// liveSession 在存储里建一个已公布的会话。
func liveSession(
	t *testing.T,
	store *session.Store,
	owner *scope.Scope,
	id sessionlog.SessionID,
	options session.CreateOptions,
) *session.Session {
	t.Helper()
	live, err := store.Create(context.Background(), owner, id, options)
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	return live
}

// mustData 把一份负载排成字节，排不出去当场失败。
func mustData(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// userMessageEvent 造一条上表面的用户消息事件，来源由调用方给。
func userMessageEvent(t *testing.T, content llm.Content, source llm.MessageSource) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      mustData(t, sessionlog.UserMessageData{Message: llm.NewUserMessage(content, source)}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// runtimeContextEvent 造一条**本投影自己那种**的运行期上下文事件。
func runtimeContextEvent(t *testing.T, text string) sessionlog.Event {
	t.Helper()
	return userMessageEvent(t,
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{Plugin: RuntimeContextSource})
}

// foreignUserEvent 造一条**别人**的用户消息事件，用来验投影不认领它。
func foreignUserEvent(t *testing.T, text string) sessionlog.Event {
	t.Helper()
	return userMessageEvent(t, llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
}

// appendEvent 往活会话上追加一条事件，失败当场停。
func appendEvent(t *testing.T, live *session.Session, event sessionlog.Event) sessionlog.Event {
	t.Helper()
	appended, err := live.Append(event)
	if err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
	return appended
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

// textOf 取出一条消息里唯一那块文本，取不出来当场失败。
func textOf(t *testing.T, message llm.Message) string {
	t.Helper()
	if len(message.Content) != 1 {
		t.Fatalf("这条消息不是恰好一块内容：%#v", message.Content)
	}
	block, ok := message.Content[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("这条消息那块内容不是文本：%#v", message.Content[0])
	}
	return block.Text
}
