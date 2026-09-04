// 本文件的作用：本包测试共用的替身与素材——活会话表、持久化后端、各类事件。

package sessionquery

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// fakeLive 是一张可以在测试中途被改动的活会话表。
type fakeLive struct {
	mutex   sync.Mutex
	sources map[sessionlog.SessionID]LogicalSource
	order   []sessionlog.SessionID
}

func newFakeLive() *fakeLive {
	return &fakeLive{sources: map[sessionlog.SessionID]LogicalSource{}}
}

// put 往表里放一份活会话；重复放同一个 id 只更新内容，不改列举顺序。
func (f *fakeLive) put(header sessionlog.SessionHeader, events []sessionlog.Event) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if _, ok := f.sources[header.ID]; !ok {
		f.order = append(f.order, header.ID)
	}
	f.sources[header.ID] = LogicalSource{Header: header, Events: events}
}

func (f *fakeLive) Get(id sessionlog.SessionID) (LogicalSource, bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	source, ok := f.sources[id]
	return source, ok
}

func (f *fakeLive) List() []LogicalSource {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	list := make([]LogicalSource, 0, len(f.order))
	for _, id := range f.order {
		list = append(list, f.sources[id])
	}
	return list
}

// fakeStore 是一个可以按 id 定制失败的持久化后端替身。
type fakeStore struct {
	headers      []sessionlog.SessionHeader
	events       map[sessionlog.SessionID][]sessionlog.Event
	listErr      error
	inspectErr   map[sessionlog.SessionID]error
	inspectMeta  map[sessionlog.SessionID]sessionlog.SessionHeader
	afterList    func()
	afterInspect func(id sessionlog.SessionID)

	mutex        sync.Mutex
	listCalls    int
	inspectCalls int
	livePeak     int
	liveNow      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		events:     map[sessionlog.SessionID][]sessionlog.Event{},
		inspectErr: map[sessionlog.SessionID]error{},
	}
}

// put 往后端里放一份落地会话。
func (f *fakeStore) put(header sessionlog.SessionHeader, events []sessionlog.Event) {
	f.headers = append(f.headers, header)
	f.events[header.ID] = events
}

func (f *fakeStore) List(ctx context.Context) ([]sessionlog.SessionHeader, error) {
	f.mutex.Lock()
	f.listCalls++
	f.mutex.Unlock()

	if f.listErr != nil {
		return nil, f.listErr
	}
	// 先取 ctx 的状态再放钩子：afterList 里撤掉 ctx 时，这一次列举仍然算成功，
	// 好让调用方那一侧「列举之后再查一次取消」的那道检查有机会被走到。
	err := ctx.Err()
	if f.afterList != nil {
		f.afterList()
	}
	return f.headers, err
}

func (f *fakeStore) Inspect(ctx context.Context, id sessionlog.SessionID) (persistence.Inspection, error) {
	f.mutex.Lock()
	f.inspectCalls++
	f.liveNow++
	f.livePeak = max(f.livePeak, f.liveNow)
	f.mutex.Unlock()
	defer func() {
		f.mutex.Lock()
		f.liveNow--
		f.mutex.Unlock()
	}()

	if err := f.inspectErr[id]; err != nil {
		return persistence.Inspection{}, err
	}
	if f.afterInspect != nil {
		f.afterInspect(id)
	}
	header, ok := headerWithID(f.headers, id)
	if !ok {
		return persistence.Inspection{}, notFound(string(id))
	}
	if meta, ok := f.inspectMeta[id]; ok {
		header = meta
	}
	return persistence.Inspection{Meta: header, Events: f.events[id]}, nil
}

// testHeader 排一个会话头出来。
func testHeader(id sessionlog.SessionID, createdAt int64) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{Version: 1, ID: id, CreatedAt: createdAt, WorkspaceID: "ws-1"}
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

// userEvent 排一条追加进表面的用户消息。
func userEvent(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventUserMessage,
		Seq:  seq,
		Time: int64(seq) * 1000,
		Data: marshalPayload(t, sessionlog.UserMessageData{Message: llm.Message{
			ID:      llm.MessageID("u" + text),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.UserSource{},
		}}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// replacingUserEvent 排一条盖掉 [start, end] 这段表面的用户消息。
func replacingUserEvent(t *testing.T, seq int, text string, start, end int, shadowed ...int) sessionlog.Event {
	t.Helper()

	event := userEvent(t, seq, text)
	event.SurfaceOp = sessionlog.ReplaceOp{Start: start, End: end}
	event.SourceEventSeqs = shadowed
	return event
}

// assistantEvent 排一条追加进表面的助手消息。
func assistantEvent(t *testing.T, seq, turn, step int, content llm.Content) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventAssistantMessage,
		Seq:  seq,
		Time: int64(seq) * 1000,
		Data: marshalPayload(t, sessionlog.AssistantMessageData{
			Turn: turn, Step: step,
			Message: llm.Message{ID: "a", Role: llm.RoleAssistant, Content: content, Source: llm.ModelSource{}},
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// plainEvent 排一条带任意负载、不上表面的事件。
func plainEvent(t *testing.T, kind sessionlog.EventType, seq int, payload any) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{Type: kind, Seq: seq, Time: int64(seq) * 1000, Data: marshalPayload(t, payload)}
}

// singleUserLog 是一份只有一条用户消息的合法日志。
func singleUserLog(t *testing.T, text string) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{userEvent(t, 0, text)}
}

// replacementLog 是一份「两条追加、第三条把它们一起盖掉」的合法日志。
func replacementLog(t *testing.T) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{
		userEvent(t, 0, "第一条"),
		userEvent(t, 1, "第二条"),
		replacingUserEvent(t, 2, "盖住前两条", 0, 1, 0, 1),
	}
}

// evictedHeadLog 是 [replacementLog] 那份日志整体挪到 baseSeq 起的样子：
// 一份被弹掉过头部的存档，第一条的 seq 不是 0（见 docs/session-log-limit.md 原则第 1 条）。
func evictedHeadLog(t *testing.T, baseSeq int) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{
		userEvent(t, baseSeq, "第一条"),
		userEvent(t, baseSeq+1, "第二条"),
		replacingUserEvent(t, baseSeq+2, "盖住前两条", baseSeq, baseSeq+1, baseSeq, baseSeq+1),
	}
}

// requireCode 断言一条错误带着预期的分类码。
func requireCode(t *testing.T, err error, want Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("想要 %s，实际没报错", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("错误码不对：想要 %s，实际 %v", want, err)
	}
}
