// 本文件的作用：把这条线上的通道钉在它真会出错的几处——坏行、非对象帧、认不出的
// 方法名、断开时还等着的那些请求，以及那批线上类型的 JSON 形状。
//
// # 这些测试防的是什么错
//
//   - **一行坏 JSON 把整条会话弄死**。线的另一端是别人写的 SDK：一行断在半路的
//     JSON、一行混进 stdout 的调试输出，都得跳过去接着跑。
//   - **对面最后一帧被吃掉**。最后一行没有换行符时 [bufio.Reader.ReadBytes] 会连着
//     数据一起交回 io.EOF，先看错误再解那一行就会把它丢了。
//   - **认不出的方法名回成 -32603**。客户端靠 -32601 区分「你这个版本没有这个方法」
//     和「你这边炸了」，折错码会让它去重试一件永远不会成立的事。
//   - **两个 goroutine 交叉写把两帧搅在一行里**。按行分帧的协议里这等于两帧一起废掉。
//   - **shutdown 的结果排成了 null**。协议上它写死是 `{}`。
//   - **一行永远不来的换行符把内存吃光**。上限之外的那一行按坏行处理，连接照常。
//   - **同时在办多少件由对面说了算**。名额满了读循环停下来等，压力还给对面。
//   - **两支 image 只按 type 分**。分开它们的是 data 这个键在不在，不是 type。
//   - **MaxTokens 的「没给」和「给了 0」混成一件事**。后者是坏输入，前者是常态。
//   - **LastAssistantMessage 空切片和缺席混成一件事**。前者是「跑出来了但内容为空」。

package sdkprotocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// ---- 手脚架 ----

// pipes 是一对接起来的通道：两端各自读对方写的东西。
type pipes struct {
	clientToServer *io.PipeWriter
	serverToClient *io.PipeWriter
}

// wire 把两条 [LineTransport] 背对背接起来，一条当客户端一条当服务端。
//
// 用真的 [io.Pipe] 而不是内存缓冲：分帧、并发写、以及「关掉一头另一头读到 EOF」
// 这三件事只有在真的流上才试得出来。
type wire struct {
	client *LineTransport
	server *LineTransport
	pipes  pipes
}

func connect(t *testing.T, server Handlers, client Handlers) *wire {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	clientReader, clientWriter := io.Pipe()
	serverReader, serverWriter := io.Pipe()
	live := &wire{
		// 客户端读服务端写的那条，写客户端往服务端发的那条。
		client: NewLineTransport(ctx, serverReader, clientWriter, client),
		server: NewLineTransport(ctx, clientReader, serverWriter, server),
		pipes:  pipes{clientToServer: clientWriter, serverToClient: serverWriter},
	}
	t.Cleanup(func() {
		live.client.Close()
		live.server.Close()
		_ = clientWriter.Close()
		_ = serverWriter.Close()
	})
	return live
}

// idleInput 是一条**永远不结束、也永远没有东西来**的输入。
//
// 只测发出去那一侧的时候必须用它而不是一个空 [strings.Reader]：空的读者当场就是
// EOF，通道随即断开，于是每一次 Notify 都因为「连接已经关了」而失败——那样的测试
// 看起来是绿的，测到的却完全不是它自称在测的东西。
func idleInput(t *testing.T) io.Reader {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	return reader
}

// echoHandlers 是一套把方法名和入参原样回给对面的处理器。
func echoHandlers(seen chan<- string) Handlers {
	return Handlers{
		Request: func(_ context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case "echo":
				return map[string]any{"method": method, "params": params}, nil
			case "boom":
				return nil, errors.New("处理器炸了")
			case "empty":
				return nil, nil
			}
			return nil, fmt.Errorf("%w: %s", ErrMethodNotFound, method)
		},
		Notification: func(_ context.Context, method string, _ json.RawMessage) {
			if seen != nil {
				seen <- method
			}
		},
	}
}

// ---- 分帧 ----

// TestMalformedLinesAreSkipped 钉住坏行跳过去、后面那一帧照样送到。
//
// 这是本包唯一没有交给那个库的一层存在的理由：库自带的帧编解码在这里会关掉连接。
func TestMalformedLinesAreSkipped(t *testing.T) {
	t.Parallel()
	for _, junk := range []string{
		"",                       // 空行
		"   ",                    // 全是空白
		"{断在半路",                  // 不是合法 JSON
		`["batch"]`,              // 是合法 JSON 但不是对象
		`"just a string"`,        // 标量
		`{"jsonrpc":"2.0"}`,      // 是对象但既不像请求也不像响应
		"npm WARN 混进来的调试输出",      // 根本不是 JSON
		`{"id":1,"result":null}`, // 一个没人等着的响应
	} {
		t.Run(junk, func(t *testing.T) {
			t.Parallel()
			live := connect(t, echoHandlers(nil), Handlers{})
			if _, err := live.pipes.clientToServer.Write([]byte(junk + "\n")); err != nil {
				t.Fatalf("写坏行失败：%v", err)
			}
			var result map[string]any
			if err := live.client.Request(t.Context(), "echo", map[string]any{"a": 1}, &result); err != nil {
				t.Fatalf("坏行之后那一帧该照样送到：%v", err)
			}
			if result["method"] != "echo" {
				t.Fatalf("回来的不是那一帧：%#v", result)
			}
		})
	}
}

// TestTheLastFrameWithoutANewlineStillArrives 钉住对面最后一帧不带换行符也读得到。
//
// [bufio.Reader.ReadBytes] 在这种时候连着数据一起交回 io.EOF；先判错误再解那一行
// 就会把它丢掉，而「最后一帧」恰恰常常是最要紧的那一帧。
func TestTheLastFrameWithoutANewlineStillArrives(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	reader := strings.NewReader(`{"jsonrpc":"2.0","method":"session.status"}`)
	transport := NewLineTransport(t.Context(), reader, io.Discard, echoHandlers(seen))
	t.Cleanup(func() { transport.Close() })
	select {
	case method := <-seen:
		if method != MethodSessionStatus {
			t.Fatalf("收到的方法名不对：%q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("最后那一帧被吃掉了")
	}
}

// TestConcurrentWritesStayOnTheirOwnLines 钉住并发写不会把两帧搅在一行里。
//
// 服务端那一侧本来就是并发写的：会话事件、状态变化、子 agent 生命周期各来各的，
// 一旦交叉，按行分帧的对面会一次废掉两帧。
func TestConcurrentWritesStayOnTheirOwnLines(t *testing.T) {
	t.Parallel()
	const senders = 8
	const each = 25
	var buffer syncBuffer
	transport := NewLineTransport(t.Context(), idleInput(t), &buffer, Handlers{})
	t.Cleanup(func() { transport.Close() })
	var group sync.WaitGroup
	for sender := range senders {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range each {
				if err := transport.Notify(t.Context(), MethodSessionEvent,
					map[string]any{"sender": sender, "index": index}); err != nil {
					t.Errorf("发第 %d/%d 条失败：%v", sender, index, err)
					return
				}
			}
		}()
	}
	group.Wait()
	lines := strings.Split(strings.TrimRight(buffer.String(), "\n"), "\n")
	if len(lines) != senders*each {
		t.Fatalf("该正好 %d 行，实际 %d 行", senders*each, len(lines))
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("有一行不是完整的一帧：%q", line)
		}
	}
}

// syncBuffer 是一个能被多个 goroutine 一起写的缓冲。
type syncBuffer struct {
	mutex sync.Mutex
	text  strings.Builder
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.text.Write(data)
}

func (b *syncBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.text.String()
}

// ---- 上限 ----

// TestAnOverlongFrameIsSkippedAndTheNextOneStillArrives 钉住超长的一行按坏行处理。
//
// DSH 是 `this.buffer += chunk` 无限攒着的：一条永远不带换行符的输入能把进程的内存
// 吃光。丢掉之后接着读、而不是把连接关掉，是为了和「坏行跳过去」保持同一条规矩——
// 对本端来说，一行超长和一行断在半路是同一件事。
func TestAnOverlongFrameIsSkippedAndTheNextOneStillArrives(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 2)
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	transport := NewLineTransportWith(t.Context(), reader, io.Discard, echoHandlers(seen),
		TransportOptions{MaxFrameBytes: 256})
	t.Cleanup(func() { transport.Close() })

	// 一帧本身合法、只是太长；它必须被丢掉而不是被办掉。
	overlong := fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":{"pad":%q}}`,
		MethodSessionStatus, strings.Repeat("x", 4096))
	if _, err := writer.Write([]byte(overlong + "\n")); err != nil {
		t.Fatalf("写超长那一帧失败：%v", err)
	}
	if _, err := writer.Write([]byte(`{"jsonrpc":"2.0","method":"session.event"}` + "\n")); err != nil {
		t.Fatalf("写下一帧失败：%v", err)
	}
	select {
	case method := <-seen:
		// 先到的必须是**后**发的那一帧：超长的那一帧压根没被办。
		if method != MethodSessionEvent {
			t.Fatalf("超长那一帧不该被办：%q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超长那一帧之后这条线该还活着")
	}
}

// TestAFrameRightAtTheCapStillArrives 钉住上限是「超过才丢」而不是「够到就丢」。
func TestAFrameRightAtTheCapStillArrives(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	frame := `{"jsonrpc":"2.0","method":"session.event"}` + "\n"
	transport := NewLineTransportWith(t.Context(), strings.NewReader(frame), io.Discard,
		echoHandlers(seen), TransportOptions{MaxFrameBytes: len(frame)})
	t.Cleanup(func() { transport.Close() })
	select {
	case method := <-seen:
		if method != MethodSessionEvent {
			t.Fatalf("收到的方法名不对：%q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("正好卡在上限上的那一帧该照样送到")
	}
}

// TestConcurrentFramesStayUnderTheCeiling 钉住同时在办的帧数不超过那条上限。
//
// DSH 对每一行 `void this.handleLine(line)` 一发了之，同时在办多少件完全由对面说了算。
// 这里名额满了之后读循环停下来等，于是压力顺着流还给对面，而不是在本端堆 goroutine。
func TestConcurrentFramesStayUnderTheCeiling(t *testing.T) {
	t.Parallel()
	const ceiling = 2
	const frames = 20
	var live, peak atomic.Int64
	release := make(chan struct{})
	arrived := make(chan struct{}, frames)

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	transport := NewLineTransportWith(t.Context(), reader, io.Discard, Handlers{
		Notification: func(context.Context, string, json.RawMessage) {
			now := live.Add(1)
			for {
				was := peak.Load()
				if now <= was || peak.CompareAndSwap(was, now) {
					break
				}
			}
			arrived <- struct{}{}
			<-release
			live.Add(-1)
		},
	}, TransportOptions{MaxConcurrentFrames: ceiling})
	t.Cleanup(func() { transport.Close() })

	written := make(chan error, 1)
	go func() {
		for range frames {
			if _, err := writer.Write([]byte(`{"jsonrpc":"2.0","method":"session.event"}` + "\n")); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()

	// 等名额确实被占满，再放它们过去。占满这件事本身就是「上限起作用了」的证据：
	// 没有上限的话这一批会一次全部进来。
	for range ceiling {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("头几帧该被办起来")
		}
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatalf("写那一批失败：%v", err)
	}
	for range frames - ceiling {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("这一批该全部办完，实际只办了 %d 帧", frames-int(live.Load()))
		}
	}
	if peak.Load() > ceiling {
		t.Fatalf("同时在办的帧数该不超过 %d，实际到过 %d", ceiling, peak.Load())
	}
}

// TestZeroLimitsFallBackToTheDefaults 钉住两条上限的零值走的是默认值那条路。
func TestZeroLimitsFallBackToTheDefaults(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	transport := NewLineTransportWith(t.Context(),
		strings.NewReader(`{"jsonrpc":"2.0","method":"session.event"}`+"\n"),
		io.Discard, echoHandlers(seen), TransportOptions{})
	t.Cleanup(func() { transport.Close() })
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("零值该退到默认上限，而不是把一切都挡下来")
	}
}

// TestNegativeLimitsMeanNoCeiling 钉住负数表示不设限。
func TestNegativeLimitsMeanNoCeiling(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	long := fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":{"pad":%q}}`,
		MethodSessionEvent, strings.Repeat("x", 1<<20))
	transport := NewLineTransportWith(t.Context(), strings.NewReader(long+"\n"), io.Discard,
		echoHandlers(seen), TransportOptions{MaxFrameBytes: -1, MaxConcurrentFrames: -1})
	t.Cleanup(func() { transport.Close() })
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("不设限时一帧多长都该送到")
	}
}

// ---- 分流 ----

// TestParamsCollapseToAnObject 钉住数组和标量的 params 塌成空对象。
//
// 处理器全都按具名字段解 params：一个数组解进结构体是一次类型错误，塌成空对象之后
// 走的则是「必填字段没给」那条正常的路。
func TestParamsCollapseToAnObject(t *testing.T) {
	t.Parallel()
	for name, params := range map[string]any{
		"数组":     []int{1, 2},
		"标量":     42,
		"字符串":    "文本",
		"没有这个字段": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			live := connect(t, echoHandlers(nil), Handlers{})
			var result struct {
				Params json.RawMessage `json:"params"`
			}
			if err := live.client.Request(t.Context(), "echo", params, &result); err != nil {
				t.Fatalf("这一帧该送到：%v", err)
			}
			if string(result.Params) != "{}" {
				t.Fatalf("该塌成空对象，实际 %s", result.Params)
			}
		})
	}
}

// TestUnknownMethodIsMethodNotFound 钉住认不出的方法名回的是 -32601。
//
// 客户端靠这个码区分「你这个版本没有这个方法」和「你这边炸了」：折成 -32603 会让它
// 去重试一件永远不会成立的事。
func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	t.Parallel()
	live := connect(t, echoHandlers(nil), Handlers{})
	err := live.client.Request(t.Context(), "session/nonexistent", map[string]any{}, nil)
	var response *ResponseError
	if !errors.As(err, &response) {
		t.Fatalf("该是一个错误帧，实际 %v", err)
	}
	if response.Code != jsonrpc2.CodeMethodNotFound {
		t.Fatalf("该是 -32601，实际 %d", response.Code)
	}
	if !strings.Contains(response.Message, "session/nonexistent") {
		t.Fatalf("那句话该点出方法名，实际 %q", response.Message)
	}
}

// TestHandlerFailureIsInternalError 钉住处理器自己炸了折成 -32603。
func TestHandlerFailureIsInternalError(t *testing.T) {
	t.Parallel()
	live := connect(t, echoHandlers(nil), Handlers{})
	err := live.client.Request(t.Context(), "boom", map[string]any{}, nil)
	var response *ResponseError
	if !errors.As(err, &response) {
		t.Fatalf("该是一个错误帧，实际 %v", err)
	}
	if response.Code != jsonrpc2.CodeInternalError {
		t.Fatalf("该是 -32603，实际 %d", response.Code)
	}
	if response.Message != "处理器炸了" {
		t.Fatalf("那句话该原样带过来，实际 %q", response.Message)
	}
}

// TestNoRequestHandlerIsAlsoMethodNotFound 钉住本端根本不接请求时同样回 -32601。
//
// 不接请求和接了但没有这个方法，对客户端是同一件事：这边办不了。
func TestNoRequestHandlerIsAlsoMethodNotFound(t *testing.T) {
	t.Parallel()
	live := connect(t, Handlers{}, Handlers{})
	err := live.client.Request(t.Context(), "initialize", map[string]any{}, nil)
	var response *ResponseError
	if !errors.As(err, &response) || response.Code != jsonrpc2.CodeMethodNotFound {
		t.Fatalf("该是 -32601，实际 %v", err)
	}
}

// TestNilResultIsAnEmptyObject 钉住交回 nil 的处理器排出来的是 `{}` 而不是 null。
//
// `shutdown` 那条路的结果在协议上写死是空对象；排成 null 会让按对象解的客户端炸掉。
func TestNilResultIsAnEmptyObject(t *testing.T) {
	t.Parallel()
	live := connect(t, echoHandlers(nil), Handlers{})
	var result json.RawMessage
	if err := live.client.Request(t.Context(), "empty", map[string]any{}, &result); err != nil {
		t.Fatalf("这一帧该送到：%v", err)
	}
	if string(result) != "{}" {
		t.Fatalf("该是一个空对象，实际 %s", result)
	}
}

// TestNotificationsGetNoReply 钉住通知走的是通知那一支：不回话，也不占一个请求 id。
func TestNotificationsGetNoReply(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	live := connect(t, echoHandlers(seen), Handlers{})
	if err := live.client.Notify(t.Context(), MethodSubagentStarted,
		SubagentStartedNotification{ParentSessionID: "父", ChildSessionID: "子"}); err != nil {
		t.Fatalf("发通知失败：%v", err)
	}
	select {
	case method := <-seen:
		if method != MethodSubagentStarted {
			t.Fatalf("收到的方法名不对：%q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("那条通知没送到")
	}
}

// TestNotificationsWithoutAHandlerAreDropped 钉住没人接的通知被丢掉、连接照常。
func TestNotificationsWithoutAHandlerAreDropped(t *testing.T) {
	t.Parallel()
	live := connect(t, Handlers{Request: echoHandlers(nil).Request}, Handlers{})
	if err := live.client.Notify(t.Context(), MethodSessionEvent, map[string]any{}); err != nil {
		t.Fatalf("发通知失败：%v", err)
	}
	var result map[string]any
	if err := live.client.Request(t.Context(), "echo", map[string]any{}, &result); err != nil {
		t.Fatalf("丢掉一条通知之后这条线该还活着：%v", err)
	}
}

// ---- 断开 ----

// TestClosingFailsPendingRequests 钉住关掉之后还等着的请求当场被打回。
//
// 悄悄挂着等一个永远不来的响应，是把一次明确的掉线变成一次静默的卡死。
func TestClosingFailsPendingRequests(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	live := connect(t, Handlers{
		Request: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, nil
		},
	}, Handlers{})
	failed := make(chan error, 1)
	go func() { failed <- live.client.Request(context.Background(), "hang", map[string]any{}, nil) }()
	// 等那次请求确实发出去了，再关。
	time.Sleep(50 * time.Millisecond)
	live.client.Close()
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("那次还等着的请求该被打回")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("那次还等着的请求没被打回")
	}
}

// TestCloseIsIdempotent 钉住关第二次不炸。
//
// 收摊这条路上关闭会被走到两次（一次是 shutdown 请求，一次是作用域释放），第二次
// 炸掉会把一次正常的收摊变成一次失败。
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	live := connect(t, Handlers{}, Handlers{})
	live.client.Close()
	live.client.Close()
}

// TestDoneClosesWhenTheOtherEndGoesAway 钉住对端走掉之后 Done 那个通道关上。
func TestDoneClosesWhenTheOtherEndGoesAway(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	transport := NewLineTransport(t.Context(), reader, io.Discard, Handlers{})
	t.Cleanup(func() { transport.Close() })
	if err := writer.Close(); err != nil {
		t.Fatalf("关掉写那头失败：%v", err)
	}
	select {
	case <-transport.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("对端走掉之后 Done 该关上")
	}
}

// TestRequestOnADeadTransportFails 钉住往一条已经关掉的通道上发东西是错误不是静默。
func TestRequestOnADeadTransportFails(t *testing.T) {
	t.Parallel()
	live := connect(t, Handlers{}, Handlers{})
	live.client.Close()
	if err := live.client.Request(t.Context(), "echo", map[string]any{}, nil); err == nil {
		t.Fatal("往一条死了的通道上发请求该报错")
	}
	if err := live.client.Notify(t.Context(), MethodSessionEvent, map[string]any{}); err == nil {
		t.Fatal("往一条死了的通道上发通知该报错")
	}
}

// TestCancellingARequestStopsWaiting 钉住 ctx 取消之后那次等待当场结束。
func TestCancellingARequestStopsWaiting(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	live := connect(t, Handlers{
		Request: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, nil
		},
	}, Handlers{})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := live.client.Request(ctx, "hang", map[string]any{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("该以 ctx 的理由结束，实际 %v", err)
	}
}

// TestWriteFailuresSurface 钉住写不出去的时候错误交回给调用方。
func TestWriteFailuresSurface(t *testing.T) {
	t.Parallel()
	transport := NewLineTransport(t.Context(), idleInput(t), brokenWriter{}, Handlers{})
	t.Cleanup(func() { transport.Close() })
	if err := transport.Notify(t.Context(), MethodSessionEvent, map[string]any{}); err == nil {
		t.Fatal("写不出去该交回错误")
	}
}

// TestUnserializableParamsSurface 钉住排不出去的入参交回错误而不是写半帧出去。
func TestUnserializableParamsSurface(t *testing.T) {
	t.Parallel()
	var buffer syncBuffer
	transport := NewLineTransport(t.Context(), idleInput(t), &buffer, Handlers{})
	t.Cleanup(func() { transport.Close() })
	if err := transport.Notify(t.Context(), MethodSessionEvent, make(chan int)); err == nil {
		t.Fatal("排不出去的入参该交回错误")
	}
	if buffer.String() != "" {
		t.Fatalf("一个字节都不该写出去，实际 %q", buffer.String())
	}
}

// brokenWriter 是一个每次写都失败的输出。
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("管子断了") }

// ---- 线上类型 ----

// TestInitializeParamsKeepsTheDifferenceBetweenAbsentAndZero 钉住 MaxTokens 的两种「没有」分得开。
//
// 「没给」是常态（服务端不设上限），「给了 0」是坏输入（服务端当场拒）。混成一件事
// 会让一次该被拒的握手默默跑起来。
func TestInitializeParamsKeepsTheDifferenceBetweenAbsentAndZero(t *testing.T) {
	t.Parallel()
	var absent InitializeParams
	if err := json.Unmarshal([]byte(`{"cwd":"/w","provider":"p","model":"m"}`), &absent); err != nil {
		t.Fatalf("解不动：%v", err)
	}
	if absent.MaxTokens != nil {
		t.Fatalf("没给该是 nil，实际 %v", *absent.MaxTokens)
	}
	var zero InitializeParams
	if err := json.Unmarshal([]byte(`{"cwd":"/w","provider":"p","model":"m","maxTokens":0}`), &zero); err != nil {
		t.Fatalf("解不动：%v", err)
	}
	if zero.MaxTokens == nil || *zero.MaxTokens != 0 {
		t.Fatalf("给了 0 该是一个指向 0 的指针，实际 %v", zero.MaxTokens)
	}
	encoded, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if strings.Contains(string(encoded), "maxTokens") {
		t.Fatalf("没给的时候线上不该有这个字段：%s", encoded)
	}
}

// TestSubagentFinishedKeepsTheDifferenceBetweenAbsentAndEmpty 钉住那段助手输出的两种「空」分得开。
//
// nil 是「这个孩子一个字都没产出」，长度为零的切片是「跑出来了但内容为空」。
func TestSubagentFinishedKeepsTheDifferenceBetweenAbsentAndEmpty(t *testing.T) {
	t.Parallel()
	absent := SubagentFinishedNotification{
		Provider:        "本地",
		AgentID:         "子",
		ParentSessionID: "父",
		ChildSessionID:  "子",
		Status:          RunOK,
		StopReason:      subagent.StopCompleted,
	}
	encoded, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if strings.Contains(string(encoded), "lastAssistantMessage") {
		t.Fatalf("没产出的时候线上不该有这个字段：%s", encoded)
	}
	present := absent
	present.LastAssistantMessage = llm.Content{llm.TextBlock{Text: "做完了"}}
	encoded, err = json.Marshal(present)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back SubagentFinishedNotification
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("解不动：%v", err)
	}
	if len(back.LastAssistantMessage) != 1 {
		t.Fatalf("那段输出该往返得回来，实际 %#v", back.LastAssistantMessage)
	}
	if text, ok := back.LastAssistantMessage[0].(llm.TextBlock); !ok || text.Text != "做完了" {
		t.Fatalf("那段输出的内容不对：%#v", back.LastAssistantMessage[0])
	}
}

// TestWireTypesSurviveARoundTripOverTheTransport 钉住那几种负载真能从线上过一趟。
//
// 单独测 json.Marshal 证明不了这件事：会话事件带着自己的 MarshalJSON，内容块是一个
// 带判别标签的联合，两者都有各自的往返规矩。
func TestWireTypesSurviveARoundTripOverTheTransport(t *testing.T) {
	t.Parallel()
	received := make(chan json.RawMessage, 1)
	live := connect(t, Handlers{
		Notification: func(_ context.Context, _ string, params json.RawMessage) { received <- params },
	}, Handlers{})
	sent := SessionEventNotification{
		SessionID: "会话",
		Event: session.Event{
			Type: session.EventType("user/message"),
			Seq:  7,
			Time: 1700000000000,
			Data: json.RawMessage(`{"a":1}`),
		},
	}
	if err := live.client.Notify(t.Context(), MethodSessionEvent, sent); err != nil {
		t.Fatalf("发通知失败：%v", err)
	}
	select {
	case params := <-received:
		var back SessionEventNotification
		if err := json.Unmarshal(params, &back); err != nil {
			t.Fatalf("解不动：%v", err)
		}
		if back.SessionID != sent.SessionID || back.Event.Seq != sent.Event.Seq {
			t.Fatalf("往返之后不一样了：%#v", back)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("那条通知没送到")
	}
}

// TestPromptRoundTripsItsContent 钉住一轮输入的内容块从线上过一趟还是原来那些。
func TestPromptRoundTripsItsContent(t *testing.T) {
	t.Parallel()
	sent := SessionPromptParams{
		SessionID:     "会话",
		ContentBlocks: PromptContent{{Durable: llm.TextBlock{Text: "把测试补齐"}}},
	}
	encoded, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back SessionPromptParams
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("解不动：%v", err)
	}
	if len(back.ContentBlocks) != 1 {
		t.Fatalf("内容块该往返得回来，实际 %#v", back.ContentBlocks)
	}
	if text, ok := back.ContentBlocks[0].Durable.(llm.TextBlock); !ok || text.Text != "把测试补齐" {
		t.Fatalf("内容不对：%#v", back.ContentBlocks[0])
	}
}

// TestInlineImagesAreToldApartByThePresenceOfData 钉住两支 image 是靠 data 这个**键在不在**分开的。
//
// DSH 那个类型守卫是 `type === 'image' && 'data' in block`：两支的 type 都是 image，
// 分开它们的是带 data 还是带 attachment。光看 type 会把每一张耐久图都当成待准入的
// 内联图；把 data 收进 string 则会让 `{"type":"image","data":""}`（一张空图，该被准入
// 那一层拒掉）和「根本没有 data」长得一样。
func TestInlineImagesAreToldApartByThePresenceOfData(t *testing.T) {
	t.Parallel()
	for name, one := range map[string]struct {
		raw     string
		encoded bool
	}{
		"带 data 的是内联图":   {`{"type":"image","data":"AAAA","mimeType":"image/png"}`, true},
		"data 是空串也还是内联图": {`{"type":"image","data":"","mimeType":"image/png"}`, true},
		"带 attachment 的是耐久引用": {
			`{"type":"image","attachment":{"attachmentId":"a","mediaType":"image/png","bytes":1,"width":1,"height":1}}`,
			false,
		},
		"文本块是耐久的": {`{"type":"text","text":"你好"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var blocks PromptContent
			if err := json.Unmarshal([]byte("["+one.raw+"]"), &blocks); err != nil {
				t.Fatalf("解不动：%v", err)
			}
			if len(blocks) != 1 {
				t.Fatalf("该是一块，实际 %d 块", len(blocks))
			}
			if (blocks[0].Encoded != nil) != one.encoded {
				t.Fatalf("分错支了：%#v", blocks[0])
			}
			if one.encoded == (blocks[0].Durable != nil) {
				t.Fatalf("两支该恰好有一支：%#v", blocks[0])
			}
		})
	}
}

// TestPromptContentKeepsAbsentDistinctFromEmpty 钉住「没给这一串」和「给了空的一串」分得开。
func TestPromptContentKeepsAbsentDistinctFromEmpty(t *testing.T) {
	t.Parallel()
	for name, one := range map[string]struct {
		raw    string
		isNil  bool
		length int
	}{
		"没给":   {"null", true, 0},
		"给了空的": {"[]", false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var blocks PromptContent
			if err := json.Unmarshal([]byte(one.raw), &blocks); err != nil {
				t.Fatalf("解不动：%v", err)
			}
			if (blocks == nil) != one.isNil {
				t.Fatalf("nil 和空切片该分得开：%#v", blocks)
			}
			encoded, err := json.Marshal(blocks)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(encoded) != one.raw {
				t.Fatalf("该原样排回 %s，实际 %s", one.raw, encoded)
			}
		})
	}
}

// TestAPromptBlockWithNeitherArmIsRefused 钉住两支都空的一块当场说出来。
//
// 悄悄排成 null 会让对面读到一个说不出是什么的块，而错在本端。
func TestAPromptBlockWithNeitherArmIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := json.Marshal(PromptContent{{}}); err == nil {
		t.Fatal("两支都空的一块该排不出去")
	}
}

// TestServerNameIsWireStable 钉住那个服务端身份一个字都没变。
//
// 客户端拿它认这条线的对面是谁；它是协议的一部分，不是一句可以随手改的文案。
func TestServerNameIsWireStable(t *testing.T) {
	t.Parallel()
	if ServerName != "deepseek-harness-sdk-runtime" {
		t.Fatalf("这个身份是线上稳定的，改不得：%q", ServerName)
	}
}

// TestMethodNamesAreWireStable 逐条钉住那七个方法名。
func TestMethodNamesAreWireStable(t *testing.T) {
	t.Parallel()
	for got, want := range map[string]string{
		MethodSessionEvent:     "session.event",
		MethodSessionStatus:    "session.status",
		MethodSubagentStarted:  "subagent.started",
		MethodSubagentFinished: "subagent.finished",
		MethodInitialize:       "initialize",
		MethodSessionPrompt:    "session/prompt",
		MethodShutdown:         "shutdown",
	} {
		if got != want {
			t.Fatalf("方法名是线上稳定的，改不得：%q", got)
		}
	}
}

// ---- 不变量 ----

// TestRegisterInvariantsReservesThePackage 钉住这个包名被占住了，而且占的是一个空位置。
//
// 「检查过了、结论是无需检查」和「这个包被漏掉了」必须区分得开，占住名字就是那个区分。
func TestRegisterInvariantsReservesThePackage(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	dispose, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	t.Cleanup(dispose)
	if !slices.Contains(registry.Registered(), PackageName) {
		t.Fatalf("这个包名该被占住，实际 %v", registry.Registered())
	}
}

// TestRegisterInvariantsNeedsARegistry 钉住没有注册表时当场说不行而不是默默什么都不做。
func TestRegisterInvariantsNeedsARegistry(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表该装不上")
	}
}

// TestFramesThatCannotBeMarshalledNeverReachTheStream 钉住排不出去的一帧一个字节都不写。
//
// 这一支从 [LineTransport.Notify] 那边走不到（那个库在交给帧这一层之前先把 params 排
// 掉了），可它是这层帧编解码自己的约定：宁可交回错误，也不能把半帧写到线上——按行
// 分帧的对面会拿这半行去配下一行。
func TestFramesThatCannotBeMarshalledNeverReachTheStream(t *testing.T) {
	t.Parallel()
	var buffer syncBuffer
	stream := &lineStream{reader: bufio.NewReader(idleInput(t)), writer: &buffer}
	if err := stream.WriteObject(make(chan int)); err == nil {
		t.Fatal("排不出去的一帧该交回错误")
	}
	if buffer.String() != "" {
		t.Fatalf("一个字节都不该写出去，实际 %q", buffer.String())
	}
}
