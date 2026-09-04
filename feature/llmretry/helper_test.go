// 本文件的作用：本包测试共用的事件构造、几份现成的策略，以及一个只实现了本包
// 用得着那两个方法的假会话。
package llmretry

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// marshalPayload 把一个负载排成事件字节。
func marshalPayload(t *testing.T, payload any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return data
}

// eventAt 造一条只有类型、序号和时刻的事件。
func eventAt(seq int, kind sessionlog.EventType, data json.RawMessage) sessionlog.Event {
	return sessionlog.Event{
		Type:      kind,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      data,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// rawEvent 造一条负载逐字由调用方给的事件。
//
// 不变量那几条要验的正是**排不出去的形状**（比如 always 档带着 maxRetries），
// 而 [RetryData.MarshalJSON] 会当场拒掉它们，所以那些事件只能这样手写。
func rawEvent(seq int, kind sessionlog.EventType, raw string) sessionlog.Event {
	return eventAt(seq, kind, json.RawMessage(raw))
}

// turnStart 排一条 turn/start。
func turnStart(t *testing.T, seq, turn int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventTurnStart, marshalPayload(t, sessionlog.TurnStartData{Turn: turn}))
}

// turnEnd 排一条 turn/end。
//
// 负载是裸的：本包从头到尾不解这条事件的负载，只看它的类型。
func turnEnd(seq int) sessionlog.Event {
	return rawEvent(seq, sessionlog.EventTurnEnd, `{"turn":1,"reason":{"kind":"completed"}}`)
}

// stepStart 排一条 step/start。
func stepStart(t *testing.T, seq, turn, step int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventStepStart,
		marshalPayload(t, sessionlog.StepStartData{Turn: turn, Step: step}))
}

// stepEnd 排一条 step/end。
func stepEnd(t *testing.T, seq, turn, step int) sessionlog.Event {
	t.Helper()

	return eventAt(seq, sessionlog.EventStepEnd,
		marshalPayload(t, sessionlog.StepEndData{Turn: turn, Step: step}))
}

// header 排一条 request/header，只填本包会去读的那一个字段。
func header(seq int, provider string) sessionlog.Event {
	return rawEvent(seq, sessionlog.EventRequestHeader, fmt.Sprintf(
		`{"header":{"config":{"provider":%s,"model":"m-1"}},"reason":"initial"}`,
		strconv.Quote(provider)))
}

// retryEvent 排一条 llm/retry。
func retryEvent(t *testing.T, seq int, data RetryData) sessionlog.Event {
	t.Helper()

	return eventAt(seq, EventRetry, marshalPayload(t, data))
}

// startedEvent 排一条 llm/retry-started。
func startedEvent(t *testing.T, seq int, data RetryStartedData) sessionlog.Event {
	t.Helper()

	return eventAt(seq, EventRetryStarted, marshalPayload(t, data))
}

// testLogger 造一个把诊断丢掉的日志器。
//
// 本包在 always 档吞下游错误时会留一句 warn（见 [installation.recover]），
// 那句话不进测试输出，免得一条正常的用例看起来像是出了事。
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()

	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startedFor 造一条和这次排期配对的 llm/retry-started 负载。
func startedFor(data RetryData) RetryStartedData {
	return RetryStartedData{
		RetryID: data.RetryID,
		Turn:    data.Turn,
		Step:    data.Step,
		Retry:   data.Retry,
	}
}

// sampleFailure 是一份形状合规的失败。
func sampleFailure() llm.Failure {
	return llm.Failure{Message: "上游超时了", Code: "TIMEOUT"}
}

// openStepLog 造一段「回合 1 步骤 1 开着、路由到 provider」的前缀。
func openStepLog(t *testing.T, provider string) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{
		turnStart(t, 1, 1),
		stepStart(t, 2, 1, 1),
		header(3, provider),
	}
}

// normalRetry 造一条落在 [openStepLog] 那个步骤上的 normal 档重试负载。
func normalRetry(retryID RetryID, retry, maxRetries int) RetryData {
	return RetryData{
		RetryID:       retryID,
		Turn:          1,
		Step:          1,
		Provider:      "甲",
		Mode:          llm.RetryNormal,
		PolicyKey:     "k-1",
		Retry:         retry,
		MaxRetries:    maxRetries,
		HasMaxRetries: true,
		Delay:         500 * time.Millisecond,
		Failure:       sampleFailure(),
	}
}

// normalPolicy 造一份 normal 档策略，退避固定、抖动关掉。
//
// 抖动关掉是为了让排出来的那段延时可以逐字断言：这几条用例验的是**别的**东西，
// 抖动只会让断言变成一个区间。抖动本身由 [TestLocalDelayJitters] 单独验。
func normalPolicy(maxRetries int) llm.ResolvedRetryPolicy {
	return llm.ResolvedRetryPolicy{
		Mode: llm.RetryNormal,
		ResolvedRetryBackoff: llm.ResolvedRetryBackoff{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     10 * time.Second,
			JitterRatio:  0,
		},
		MaxRetries:     maxRetries,
		RetryableCodes: []string{"TIMEOUT", "RATE_LIMIT"},
	}
}

// alwaysPolicy 造一份 always 档策略，退避同 [normalPolicy]。
func alwaysPolicy() llm.ResolvedRetryPolicy {
	return llm.ResolvedRetryPolicy{
		Mode: llm.RetryAlways,
		ResolvedRetryBackoff: llm.ResolvedRetryBackoff{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     10 * time.Second,
			JitterRatio:  0,
		},
	}
}

// fakeSession 是一份只实现了 [sessionAppender] 的会话。
type fakeSession struct {
	mutex sync.Mutex
	log   []sessionlog.Event
	// failOn 为非空时，追加这种类型的事件会失败。
	failOn sessionlog.EventType
	// onAppend 在事件已经进了日志之后调用，锁外——和
	// [github.com/snight1983/ds-harness-go/harness/session.Session] 通知观察者的时机一样。
	onAppend func(event sessionlog.Event)
}

func (f *fakeSession) Events() []sessionlog.Event {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]sessionlog.Event(nil), f.log...)
}

func (f *fakeSession) Append(candidate sessionlog.Event) (sessionlog.Event, error) {
	f.mutex.Lock()
	if candidate.Type == f.failOn {
		f.mutex.Unlock()
		return sessionlog.Event{}, fmt.Errorf("假会话拒收 %s", candidate.Type)
	}
	candidate.Seq = len(f.log) + 1
	candidate.Time = int64(candidate.Seq) * 1000
	f.log = append(f.log, candidate)
	onAppend := f.onAppend
	f.mutex.Unlock()

	if onAppend != nil {
		onAppend(candidate)
	}
	return candidate, nil
}

// types 交出日志里那几条事件的类型，给断言用。
func (f *fakeSession) types() []sessionlog.EventType {
	kinds := make([]sessionlog.EventType, 0, len(f.log))
	for _, event := range f.Events() {
		kinds = append(kinds, event.Type)
	}
	return kinds
}
