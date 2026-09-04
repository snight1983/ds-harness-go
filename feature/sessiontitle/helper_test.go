// 本文件的作用：本包测试共用的那几样——一个能被追加的假会话、一个可编程的
// 假生成器、以及造各种事件的小工具。

package sessiontitle

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// fakeSession 是一个把日志摆在那儿、并且接得住追加的会话。
//
// 它带自己的锁：[Service] 会从后台 goroutine 里读它、追加它，一个不带锁的假会话
// 会让 -race 下的测试报出一堆和被测代码无关的数据竞争。
type fakeSession struct {
	mu     sync.Mutex
	id     sessionlog.SessionID
	header sessionlog.SessionHeader
	events []sessionlog.Event
	// appendErr 非 nil 时每一次追加都失败，用来测追加失败那几条分支。
	appendErr error
	// clock 是下一条被追加事件的时间戳，每追加一次加一。
	clock int64
	// appended 非 nil 时，每一条经 [fakeSession.Append] 落下来的事件都往这里送
	// 一份。它给的是「这条事件已经在日志上了」那个时刻。
	//
	// 测试要等的正是这一刻，而不是「生成器返回了」：提交发生在生成器返回**之后**，
	// 而 [Service.Close] 会把还没提交的那次生成取消掉。在生成器里面发信号再立刻
	// 关服务，是在赌提交能抢在取消前面跑完。
	appended chan sessionlog.Event
}

func newSession(events ...sessionlog.Event) *fakeSession {
	for index := range events {
		events[index].Seq = index
	}
	return &fakeSession{id: "s1", events: events, clock: 1000}
}

func (s *fakeSession) ID() sessionlog.SessionID { return s.id }

func (s *fakeSession) Header() sessionlog.SessionHeader { return s.header }

func (s *fakeSession) Events() []sessionlog.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 交一份拷贝出去：被测代码会在遍历它的同时（在别的 goroutine 里）追加。
	return append([]sessionlog.Event(nil), s.events...)
}

// NextSeq 只给 [projection.SessionView] 用——[Session] 本身不要这个方法。
func (s *fakeSession) NextSeq() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *fakeSession) Append(kind sessionlog.EventType, data any) error {
	s.mu.Lock()
	if s.appendErr != nil {
		s.mu.Unlock()
		return s.appendErr
	}
	raw, err := json.Marshal(data)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	event := sessionlog.Event{
		Type: kind,
		Seq:  len(s.events),
		Time: s.clock,
		Data: raw,
	}
	s.events = append(s.events, event)
	s.clock++
	notify := s.appended
	s.mu.Unlock()

	// 在锁外发：被测代码是拿着自己那把锁调进来的，收信号的那一头多半马上又要
	// 回头读这条日志。
	if notify != nil {
		notify <- event
	}
	return nil
}

// watchAppends 打开落盘通知，交出那条通道。缓冲给得比任何一个测试会追加的条数
// 都宽，免得没人收的时候把被测代码堵在追加里。
func (s *fakeSession) watchAppends() chan sessionlog.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appended = make(chan sessionlog.Event, 32)
	return s.appended
}

// waitForTitle 等到下一条**来路是 kind** 的标题事件真的落到日志上，交出它的正文。
//
// 要挑来路是因为兜底那条也会从同一条通道上过去：等「下一条标题」会当场拿到兜底，
// 而测试想看的是它后面那条。
func waitForTitle(t *testing.T, appended <-chan sessionlog.Event, kind SourceKind) EventData {
	t.Helper()

	for {
		select {
		case event := <-appended:
			if event.Type != EventSessionTitle {
				continue
			}
			data, err := decodeEventData(event)
			if err != nil {
				t.Fatalf("标题事件读不回来：%v", err)
			}
			if data.Source.Kind != kind {
				continue
			}
			return data
		case <-time.After(10 * time.Second):
			t.Fatalf("等一条来路是 %q 的标题落盘等超时了", kind)
			return EventData{}
		}
	}
}

// append 往假会话后面直接摆几条造好的事件，seq 接着排。它绕开 [fakeSession.Append]，
// 用来铺测试的初始日志。
func (s *fakeSession) append(events ...sessionlog.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, event := range events {
		event.Seq = len(s.events)
		s.events = append(s.events, event)
	}
}

// titles 交出这条日志上全部标题事件的正文，按日志顺序。
func (s *fakeSession) titles(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, event := range s.Events() {
		if event.Type != EventSessionTitle {
			continue
		}
		data, err := decodeEventData(event)
		if err != nil {
			t.Fatalf("标题事件 %d 读不回来：%v", event.Seq, err)
		}
		out = append(out, data.Title)
	}
	return out
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

// userEvent 造一条真人打的用户消息。
func userEvent(t *testing.T, text string) sessionlog.Event {
	t.Helper()

	return messageEvent(t, llm.UserSource{}, llm.Content{llm.TextBlock{Text: text}})
}

// messageEvent 造一条来源和内容都由调用方定的 user/message。
func messageEvent(t *testing.T, source llm.MessageSource, content llm.Content) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventUserMessage,
		Data: mustJSON(t, sessionlog.UserMessageData{Message: llm.Message{
			ID:      "m",
			Role:    llm.RoleUser,
			Content: content,
			Source:  source,
		}}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// titleEvent 造一条标题事件。
func titleEvent(t *testing.T, data EventData) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{Type: EventSessionTitle, Data: mustJSON(t, data)}
}

// headerEvent 造一条钉住路由的 request/header。
func headerEvent(t *testing.T, provider, model string) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventRequestHeader,
		Data: mustJSON(t, sessionlog.RequestHeaderData{
			Header: sessionlog.EpochHeader{Config: llm.CallConfig{Provider: provider, Model: model}},
			Reason: sessionlog.HeaderInitial,
		}),
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

// testConfig 是一份够用的、过得了 [Config.Validate] 的配置。
func testConfig() Config {
	return Config{FallbackMaxWords: 5, FallbackMaxBytes: 40, MaxTitleBytes: 80}
}

// newTestService 建一个服务，并把它的关闭挂进测试收尾。
func newTestService(t *testing.T, config Config) *Service {
	t.Helper()

	service, err := New(config)
	if err != nil {
		t.Fatalf("服务建不起来：%v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// fakeProvider 是一个由测试完全操控的标题生成器。
type fakeProvider struct {
	id        ProviderID
	automatic AutomaticMode
	// generate 是这次调用真正干的事；为 nil 表示回一个固定的结果。
	generate func(ctx context.Context, request ProviderRequest) (ProviderResult, error)

	mu    sync.Mutex
	calls []ProviderRequest
}

func newProvider(id ProviderID, mode AutomaticMode) *fakeProvider {
	return &fakeProvider{id: id, automatic: mode}
}

func (p *fakeProvider) ID() ProviderID { return p.id }

func (p *fakeProvider) Automatic() AutomaticMode { return p.automatic }

func (p *fakeProvider) Generate(ctx context.Context, request ProviderRequest) (ProviderResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, request)
	p.mu.Unlock()

	if p.generate != nil {
		return p.generate(ctx, request)
	}
	if len(request.Messages) == 0 {
		return ProviderResult{}, nil
	}
	last := request.Messages[len(request.Messages)-1]
	return ProviderResult{Title: "生成的标题", MessageSeqs: []int{last.Seq}}, nil
}

// callCount 交出这个生成器被调过几次。
func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}
