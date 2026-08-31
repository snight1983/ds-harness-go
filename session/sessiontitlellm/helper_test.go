// 本文件的作用：本包测试共用的那几样——一个会记账的假会话、一个由测试摆布的
// 假模型运行时、以及造流式分块的小工具。

package sessiontitlellm

import (
	"context"
	"encoding/json"
	"iter"
	"sync"
	"testing"
	"time"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/sessiontitle"
)

// fakeSession 只做两件事：报自己的 id，以及把追加下来的事件记着。
//
// 它带自己的锁：本包的生成器会从调用方那个 goroutine 里追加，而测试可能在别的
// goroutine 里读。
type fakeSession struct {
	mu sync.Mutex
	id session.SessionID
	// appendErr 非 nil 时每一次追加都失败。
	appendErr error
	// appends 记的是追加进来的**原值**，好让测试不必再读一次 JSON 就能验它。
	appends []appended
	// events 是这条日志本身，追加下来的东西排成 JSON 之后也进这里。
	events []session.Event
	// notify 非 nil 时，每一条落下来的事件都往这里送一份，给要等异步提交的测试用。
	notify chan session.Event
}

// appended 是一条被记下来的追加。
type appended struct {
	kind session.EventType
	data any
}

func newSession() *fakeSession { return &fakeSession{id: "s1"} }

func (s *fakeSession) ID() session.SessionID { return s.id }

func (s *fakeSession) Header() session.SessionHeader { return session.SessionHeader{} }

func (s *fakeSession) Events() []session.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 交一份拷贝出去：被测代码会在遍历它的同时（在别的 goroutine 里）追加。
	return append([]session.Event(nil), s.events...)
}

func (s *fakeSession) Append(kind session.EventType, data any) error {
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
	event := session.Event{Type: kind, Seq: len(s.events), Data: raw}
	s.appends = append(s.appends, appended{kind: kind, data: data})
	s.events = append(s.events, event)
	notify := s.notify
	s.mu.Unlock()

	// 在锁外发：被测代码是拿着自己那把锁调进来的。
	if notify != nil {
		notify <- event
	}
	return nil
}

// seed 往日志后面直接摆几条造好的事件，seq 接着排。它绕开 [fakeSession.Append]，
// 用来铺测试的初始日志。
func (s *fakeSession) seed(events ...session.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, event := range events {
		event.Seq = len(s.events)
		s.events = append(s.events, event)
	}
}

// watchAppends 打开落盘通知，交出那条通道。
func (s *fakeSession) watchAppends() chan session.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.notify = make(chan session.Event, 32)
	return s.notify
}

// requests 交出这条日志上全部的标题请求记录。
func (s *fakeSession) requests(t *testing.T) []RequestEventData {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []RequestEventData
	for _, entry := range s.appends {
		if entry.kind != EventTitleLLMRequest {
			continue
		}
		data, ok := entry.data.(RequestEventData)
		if !ok {
			t.Fatalf("请求记录的类型是 %T", entry.data)
		}
		out = append(out, data)
	}
	return out
}

// fakeStreamer 是一个由测试完全操控的模型运行时。
type fakeStreamer struct {
	// open 是这次调用真正干的事；为 nil 表示回一条吐「生成的标题」的正常流。
	open func(ctx context.Context, options llm.GenerateOptions) (iter.Seq2[llm.StreamChunk, error], error)

	mu    sync.Mutex
	calls []llm.GenerateOptions
}

func (s *fakeStreamer) Stream(
	ctx context.Context,
	options llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	s.mu.Lock()
	s.calls = append(s.calls, options)
	s.mu.Unlock()

	if s.open != nil {
		return s.open(ctx, options)
	}
	return textStream("生成的标题"), nil
}

// lastCall 交出最后一次派发出去的那份请求。
func (s *fakeStreamer) lastCall(t *testing.T) llm.GenerateOptions {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.calls) == 0 {
		t.Fatal("一次都没派发出去")
	}
	return s.calls[len(s.calls)-1]
}

// callCount 交出派发过几次。
func (s *fakeStreamer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// chunkStream 把几个造好的分块摆成一条流。
func chunkStream(chunks ...llm.StreamChunk) iter.Seq2[llm.StreamChunk, error] {
	return func(yield func(llm.StreamChunk, error) bool) {
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

// textStream 造一条正常收尾的、只吐一块文本的流。
func textStream(text string) iter.Seq2[llm.StreamChunk, error] {
	return chunkStream(textBlock(0, text)...)
}

// textBlock 造出一整块文本的那三个分块。
func textBlock(index int, text string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.BlockStartChunk{Index: index, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: index, Text: text},
		llm.BlockEndChunk{Index: index, Block: llm.TextBlock{Text: text}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
}

// testConfig 是一份够用的、过得了 [Config.Validate] 的配置。
func testConfig() Config {
	return Config{
		TargetWords:         5,
		TargetCJKCharacters: 10,
		MaxInputBytes:       4096,
		MaxOutputTokens:     64,
		Timeout:             10 * time.Second,
	}
}

// newTestProvider 造一个跟着日志路由走的 all-prompts 生成器。
func newTestProvider(t *testing.T, runtime Streamer, config Config) *Provider {
	t.Helper()

	provider, err := NewAllPrompts(runtime, config)
	if err != nil {
		t.Fatalf("生成器造不出来：%v", err)
	}
	return provider
}

// newRequest 造一份交给生成器的输入，路由取日志里那条。
func newRequest(sess sessiontitle.Session, messages ...sessiontitle.UserMessage) sessiontitle.ProviderRequest {
	return sessiontitle.ProviderRequest{
		Session:  sess,
		Messages: messages,
		Route:    &sessiontitle.ModelProvenance{Provider: "prov", Model: "mod"},
	}
}

// systemTextOf 把一次派发里的系统提示词取出来。
func systemTextOf(t *testing.T, options llm.GenerateOptions) string {
	t.Helper()

	return options.System
}

// userTextOf 把一次派发里那条用户消息的正文取出来。
func userTextOf(t *testing.T, options llm.GenerateOptions) string {
	t.Helper()

	if len(options.Messages) != 1 {
		t.Fatalf("派发出去的消息有 %d 条，要的是 1 条", len(options.Messages))
	}
	content := options.Messages[0].Content
	if len(content) != 1 {
		t.Fatalf("那条消息里有 %d 块内容，要的是 1 块", len(content))
	}
	text, ok := content[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("那一块的类型是 %T", content[0])
	}
	return text.Text
}

// userMessageEvent 造一条真人打的 user/message。
func userMessageEvent(t *testing.T, text string) session.Event {
	t.Helper()

	raw, err := json.Marshal(session.UserMessageData{Message: llm.Message{
		ID:      "m",
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}})
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return session.Event{Type: session.EventUserMessage, Data: raw, SurfaceOp: session.AppendOp{}}
}

// requestHeaderEvent 造一条钉住路由的 request/header。
func requestHeaderEvent(t *testing.T, provider, model string) session.Event {
	t.Helper()

	raw, err := json.Marshal(session.RequestHeaderData{
		Header: session.EpochHeader{Config: llm.CallConfig{Provider: provider, Model: model}},
		Reason: session.HeaderInitial,
	})
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return session.Event{Type: session.EventRequestHeader, Data: raw}
}

// waitForTitleEvent 等到一条 [sessiontitle.EventSessionTitle] 落到日志上，
// 并且它的来路是某个生成器，再交出它的正文。
//
// 要等落盘而不是等生成器返回：服务是在生成器返回**之后**才提交的。
func waitForTitleEvent(t *testing.T, appended <-chan session.Event) sessiontitle.EventData {
	t.Helper()

	for {
		select {
		case event := <-appended:
			if event.Type != sessiontitle.EventSessionTitle {
				continue
			}
			var data sessiontitle.EventData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatalf("标题事件读不回来：%v", err)
			}
			if data.Source.Kind != sessiontitle.SourceProvider {
				continue
			}
			return data
		case <-time.After(10 * time.Second):
			t.Fatal("等生成器起的标题落盘等超时了")
			return sessiontitle.EventData{}
		}
	}
}

// framedJSONOf 把装帧后那段提示词里的 JSON 数组读回来。
func framedJSONOf(t *testing.T, framed string) []sessiontitle.UserMessage {
	t.Helper()

	const prefix = "Generate the session title from this JSON array of human messages:\n"
	if len(framed) < len(prefix) || framed[:len(prefix)] != prefix {
		t.Fatalf("装帧后的提示词开头不对：%q", framed)
	}
	var messages []sessiontitle.UserMessage
	if err := json.Unmarshal([]byte(framed[len(prefix):]), &messages); err != nil {
		t.Fatalf("装帧出来的不是合法 JSON：%v", err)
	}
	return messages
}
