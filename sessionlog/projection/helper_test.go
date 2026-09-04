// 本文件的作用：本包测试共用的那几样：一个假会话、几个形状不同的单元、
// 以及造事件的小工具。

package projection

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// fakeSession 是一个只把日志摆在那儿的会话。
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

// newSession 造一个带着这些事件的假会话。
func newSession(id sessionlog.SessionID, events ...sessionlog.Event) *fakeSession {
	return &fakeSession{id: id, events: events}
}

// userEvent 造一条会被计数单元数进去的事件。
func userEvent(seq int) sessionlog.Event {
	return sessionlog.Event{Type: sessionlog.EventUserMessage, Seq: seq, Data: json.RawMessage(`{}`)}
}

// otherEvent 造一条计数单元不关心的事件。
func otherEvent(seq int) sessionlog.Event {
	return sessionlog.Event{Type: sessionlog.EventTodoWrite, Seq: seq, Data: json.RawMessage(`{}`)}
}

// countState 是计数单元的状态。
type countState struct {
	Count int `json:"count"`
}

// countUnit 是一个数 user/message 的客户端可见单元；别的事件一律报「没变」。
func countUnit(key string, version int) Definition[countState] {
	return Definition[countState]{
		Key:          key,
		StateVersion: version,
		Init:         func() countState { return countState{} },
		Apply: func(state countState, event sessionlog.Event) (countState, bool) {
			if event.Type != sessionlog.EventUserMessage {
				return state, false
			}
			state.Count++
			return state, true
		},
		DecodeState: StrictDecoder[countState](),
		View:        func(state countState) any { return state.Count },
	}
}

// hostOnlyUnit 是同一份折叠，但没有客户端视图。
func hostOnlyUnit(key string, version int) Definition[countState] {
	definition := countUnit(key, version)
	definition.View = nil
	return definition
}

// errDecode 是解码单元安排出来的那次失败。
var errDecode = errors.New("这行读不动")

// undecodableUnit 是一个状态永远读不回来的单元。
func undecodableUnit(key string) Definition[countState] {
	definition := countUnit(key, 0)
	definition.DecodeState = func(json.RawMessage) (countState, error) { return countState{}, errDecode }
	return definition
}

// badState 是一个排不出去的状态：函数字段没法进 JSON。
type badState struct {
	Bad func() `json:"bad"`
}

// unmarshalableUnit 是一个状态排不出去的单元。
func unmarshalableUnit(key string) Definition[badState] {
	return Definition[badState]{
		Key:          key,
		StateVersion: 0,
		Init:         func() badState { return badState{Bad: func() {}} },
		Apply:        func(state badState, _ sessionlog.Event) (badState, bool) { return state, false },
		DecodeState:  func(json.RawMessage) (badState, error) { return badState{}, nil },
		View:         func(badState) any { return nil },
	}
}

// mustRegister 登记一个单元，登记失败就判测试失败。
func mustRegister[S any](t *testing.T, registry *Registry, definition Definition[S]) func() {
	t.Helper()

	dispose, err := Register(registry, definition)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	return dispose
}

// countRow 造一条计数单元的检查点行。
func countRow(t *testing.T, version, seq, count int) CheckpointRow {
	t.Helper()

	encoded, err := json.Marshal(countState{Count: count})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	return CheckpointRow{Ver: version, Seq: seq, Val: encoded}
}
