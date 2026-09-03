// 本文件的作用：本包测试共用的替身与素材——会话查询来源、标题读取方，
// 以及几种事件的构造。

package sessionref

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/sessionquery"
)

// fakeSessions 是一份可以按会话定制失败的会话查询来源替身。
type fakeSessions struct {
	records    []sessionquery.Record
	surfaces   map[session.SessionID]sessionquery.SurfaceSnapshot
	listErr    error
	surfaceErr map[session.SessionID]error

	// beforeSurface 在每次读表面之前被调用，用来在读的中途撤掉 ctx。
	beforeSurface func(id session.SessionID)
	// ignoreCancel 为真时读表面不看 ctx，用来演一个从缓存直接答上来、
	// 压根没注意到取消的后端——那时取消要由调用方在读完之后自己查出来。
	ignoreCancel bool
	// listCalls 记这份替身被列举了几次。
	listCalls int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		surfaces:   map[session.SessionID]sessionquery.SurfaceSnapshot{},
		surfaceErr: map[session.SessionID]error{},
	}
}

// put 往替身里放一个会话，同时登记它的表面。
func (f *fakeSessions) put(id session.SessionID, workspace session.WorkspaceID, createdAt int64, events []session.Event) {
	header := session.SessionHeader{Version: 1, ID: id, CreatedAt: createdAt, WorkspaceID: workspace}
	f.records = append(f.records, sessionquery.Record{Header: header, Live: true})
	captured, capturedAny := 0, false
	if len(events) > 0 {
		captured, capturedAny = events[len(events)-1].Seq, true
	}
	f.surfaces[id] = sessionquery.SurfaceSnapshot{
		Session:            header,
		CapturedThroughSeq: captured,
		CapturedAny:        capturedAny,
		Events:             events,
	}
}

func (f *fakeSessions) ListSessions(ctx context.Context) ([]sessionquery.Record, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.records, ctx.Err()
}

func (f *fakeSessions) ReadSurface(ctx context.Context, id session.SessionID) (sessionquery.SurfaceSnapshot, error) {
	if f.beforeSurface != nil {
		f.beforeSurface(id)
	}
	if err := f.surfaceErr[id]; err != nil {
		return sessionquery.SurfaceSnapshot{}, err
	}
	snapshot, ok := f.surfaces[id]
	if !ok {
		return sessionquery.SurfaceSnapshot{}, errNotFound
	}
	if f.ignoreCancel {
		return snapshot, nil
	}
	return snapshot, ctx.Err()
}

// putRaw 往替身里放一个会话，事件由调用方给，不按最后一条推捕获点。
func (f *fakeSessions) putRaw(id session.SessionID, events []session.Event) {
	header := session.SessionHeader{Version: 1, ID: id}
	f.records = append(f.records, sessionquery.Record{Header: header, Live: true})
	f.surfaces[id] = sessionquery.SurfaceSnapshot{Session: header, Events: events}
}

// errNotFound 是替身里「没这个会话」那条错误。
var errNotFound = errString("没有这个会话")

// errString 是一个最小的哨兵错误类型，省得为几条测试引进别的包。
type errString string

func (e errString) Error() string { return string(e) }

// fakeTitles 是一份按会话给标题的读取方替身。
type fakeTitles struct {
	titles map[session.SessionID]string
	err    error
	// short 为真时故意少还一条，用来走「条数对不上」那一支。
	short bool
}

func (f *fakeTitles) ReadTitles(ctx context.Context, ids []session.SessionID) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	titles := make([]string, 0, len(ids))
	for _, id := range ids {
		titles = append(titles, f.titles[id])
	}
	if f.short && len(titles) > 0 {
		titles = titles[:len(titles)-1]
	}
	return titles, nil
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

// userEvent 排一条用户自己说的话。
func userEvent(t *testing.T, seq int, text string) session.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("u"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	})
}

// injectedEvent 排一条别的层注入的用户角色消息，它不该进投影。
func injectedEvent(t *testing.T, seq int, text string) session.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("i"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.PluginSource{Plugin: "workspace-instructions"},
	})
}

// checkpointEvent 排一条压缩检查点消息，它在投影里永远不会被整条丢掉。
func checkpointEvent(t *testing.T, seq int, text string) session.Event {
	t.Helper()

	return messageEvent(t, seq, llm.Message{
		ID:      llm.MessageID("c"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.PluginSource{Plugin: compaction.CheckpointPlugin},
	})
}

// messageEvent 把一条用户角色消息包成事件。
func messageEvent(t *testing.T, seq int, message llm.Message) session.Event {
	t.Helper()

	return session.Event{
		Type:      session.EventUserMessage,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      marshalPayload(t, session.UserMessageData{Message: message}),
		SurfaceOp: session.AppendOp{},
	}
}

// assistantEvent 排一条助手消息。
func assistantEvent(t *testing.T, seq int, blocks ...llm.ContentBlock) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventAssistantMessage,
		Seq:  seq,
		Time: int64(seq) * 1000,
		Data: marshalPayload(t, session.AssistantMessageData{
			Turn: 1, Step: 1,
			Message: llm.Message{
				ID: "a", Role: llm.RoleAssistant, Content: blocks, Source: llm.ModelSource{},
			},
		}),
		SurfaceOp: session.AppendOp{},
	}
}

// toolResultEvent 排一条工具结果，它不进引用。
func toolResultEvent(t *testing.T, seq int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventToolResult,
		Seq:  seq,
		Time: int64(seq) * 1000,
		Data: marshalPayload(t, session.ToolResultData{
			Turn: 1, Step: 1,
			Message: llm.Message{
				ID:      "t",
				Role:    llm.RoleUser,
				Content: llm.Content{llm.TextBlock{Text: "工具结果，不该进引用"}},
				Source:  llm.ToolSource{CallID: "call-1"},
			},
		}),
		SurfaceOp: session.AppendOp{},
	}
}

// snapshotOf 把一串事件包成一份表面快照。
func snapshotOf(id session.SessionID, workspace session.WorkspaceID, events []session.Event) sessionquery.SurfaceSnapshot {
	captured, capturedAny := 0, false
	if len(events) > 0 {
		captured, capturedAny = events[len(events)-1].Seq, true
	}
	return sessionquery.SurfaceSnapshot{
		Session:            session.SessionHeader{Version: 1, ID: id, WorkspaceID: workspace},
		CapturedThroughSeq: captured,
		CapturedAny:        capturedAny,
		Events:             events,
	}
}
