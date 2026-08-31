// 本文件的作用：本包测试共用的那几样——一个假活会话、几种假接收器、
// 一个能被断言的日志接收器，以及造事件的那几个小工具。

package telemetry

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// fakeView 是一个只把头、日志和种子长度摆在那儿的假活会话。
type fakeView struct {
	header    session.SessionHeader
	events    []session.Event
	firstLive int
}

func (v *fakeView) ID() session.SessionID { return v.header.ID }

func (v *fakeView) Events() []session.Event { return v.events }

func (v *fakeView) Header() session.SessionHeader { return v.header }

func (v *fakeView) FirstLiveSeq() int { return v.firstLive }

// newView 造一个假活会话：种子长度为零，也就是整份日志都是这次生命周期产出的。
func newView(id session.SessionID, events ...session.Event) *fakeView {
	return &fakeView{
		header: session.SessionHeader{Version: 1, ID: id},
		events: events,
	}
}

// recorder 是一个把每条记录收起来的假接收器。
type recorder struct {
	mu          sync.Mutex
	records     []Record
	shutdowns   int
	shutdownErr error
	// emitPanic 非空时每次 Emit 都拿它 panic。
	emitPanic string
}

func (r *recorder) Emit(record Record) {
	if r.emitPanic != "" {
		panic(r.emitPanic)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, record)
}

func (r *recorder) Shutdown(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.shutdowns++
	return r.shutdownErr
}

// taken 给出收到的全部记录。
func (r *recorder) taken() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Record(nil), r.records...)
}

// count 给出收到了几条记录。
func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.records)
}

// flushingRecorder 是一个还认回合结束提示的假接收器，用来驱动 [Flusher] 那条路。
type flushingRecorder struct {
	*recorder
	mu      sync.Mutex
	flushes int
}

func (r *flushingRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.flushes++
}

// flushed 给出回合结束提示被转过几次。
func (r *flushingRecorder) flushed() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.flushes
}

// logSink 收下协调器打出来的每一条日志，供断言。
//
// 那几条 Warn 是本包兜住失败之后唯一留下的痕迹，「兜住了」和「根本没发生」
// 在别的地方分不出来，只能在这里分。
type logSink struct {
	mu      sync.Mutex
	entries []slog.Record
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, record slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, record.Clone())
	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *logSink) WithGroup(string) slog.Handler { return s }

// messages 给出收到的每一条日志的正文。
func (s *logSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry.Message)
	}
	return out
}

// attr 给出第 index 条日志上某个属性的字符串形式，没有那个属性时给空串。
func (s *logSink) attr(index int, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index >= len(s.entries) {
		return ""
	}
	found := ""
	s.entries[index].Attrs(func(candidate slog.Attr) bool {
		if candidate.Key == key {
			found = candidate.Value.String()
			return false
		}
		return true
	})
	return found
}

// fixture 是一次完整装配：协调器本身，加上能从外面观察到的那几个侧面。
type fixture struct {
	coordinator *Coordinator
	sink        *recorder
	logs        *logSink
}

// newFixture 用给定的规则链装一套协调器出来，ops 记录的时刻固定在 1000。
func newFixture(t *testing.T, rules ...Rule) *fixture {
	t.Helper()

	return newFixtureOn(t, &recorder{}, rules...)
}

// newFixtureOn 是 [newFixture] 的底子，接收器由调用方给。
func newFixtureOn(t *testing.T, sink *recorder, rules ...Rule) *fixture {
	t.Helper()

	logs := &logSink{}
	coordinator, err := New(Options{
		Sink:   sink,
		Rules:  rules,
		Now:    func() int64 { return 1000 },
		Logger: slog.New(logs),
	})
	if err != nil {
		t.Fatalf("建协调器不该失败：%v", err)
	}
	return &fixture{coordinator: coordinator, sink: sink, logs: logs}
}

// quiet 是一个什么都不记的 logger，给那些不看日志的用例用。
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// event 造一条带着这份负载的事件。
func event(t *testing.T, kind session.EventType, seq int, at int64, payload any) session.Event {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return session.Event{Type: kind, Seq: seq, Time: at, Data: data}
}

// userEvent 造一条本包不特别对待的事件。
func userEvent(t *testing.T, seq int, at int64) session.Event {
	t.Helper()

	return event(t, session.EventUserMessage, seq, at,
		session.UserMessageData{Message: llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "hi"}}, llm.UserSource{})})
}

// chunkEvent 造一条助手分块事件。
func chunkEvent(t *testing.T, seq int, at int64, turn, step int) session.Event {
	t.Helper()

	return event(t, session.EventAssistantChunk, seq, at, session.AssistantChunkData{
		Turn: turn, Step: step, Chunk: llm.TextDeltaChunk{Index: 0, Text: "x"},
	})
}

// toolResultEvent 造一条工具结果事件，isError 决定它自己的结果位。
func toolResultEvent(t *testing.T, seq int, at int64, isError bool) session.Event {
	t.Helper()

	return event(t, session.EventToolResult, seq, at, session.ToolResultData{
		Turn: 0, Step: 0,
		Message: llm.NewToolResultMessage("c1", llm.Content{llm.TextBlock{Text: "ok"}}, isError),
	})
}

// turnEndEvent 造一条回合结束事件。
func turnEndEvent(t *testing.T, seq int, at int64, reason session.TurnEndReason) session.Event {
	t.Helper()

	return event(t, session.EventTurnEnd, seq, at,
		session.TurnEndData{Turn: 0, Reason: reason})
}

// broken 造一条负载读不回来的事件——那段字节不是任何一个负载结构体的形状。
func broken(kind session.EventType, seq int) session.Event {
	return session.Event{Type: kind, Seq: seq, Time: 1, Data: json.RawMessage(`[1,2,3]`)}
}
