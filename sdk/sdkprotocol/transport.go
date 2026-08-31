// 本文件的作用：按行分帧的那条 JSON-RPC 2.0 通道——坏行怎么跳过、请求怎么发、
// 收到的请求和通知怎么分流。
//
// 源: packages/sdk/protocol/src/transport.ts

package sdkprotocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/sourcegraph/jsonrpc2"
)

// ResponseError 是对面回过来的一个错误帧，线上的 code 和可选的 data 原样保留。
//
// 源: packages/sdk/protocol/src/transport.ts:18-28
//
// 新增: DSH 那边是一个自己写的 Error 子类。Go 这边直接用那个库的
// [jsonrpc2.Error]——字段一一对上（code / message / data），而且它已经是调用方从
// [errors.As] 里捞得到的形状。留一个别名是为了让本包的使用者不必直接 import 那个库。
type ResponseError = jsonrpc2.Error

// Peer 是往对面发东西的那一面：本包自己用，服务端也照着它写。
//
// 源: packages/sdk/protocol/src/transport.ts:34-49
//
// 新增: DSH 的 request 交回一个 unknown，由调用方自己往下断。Go 这边按惯例收一个
// 结果指针，由 encoding/json 直接填进去——少一次调用方手写的类型断言，而那次断言
// 在 TS 里本来也没有任何静态保障。
type Peer interface {
	// Request 发一次请求并等着它的响应，把结果解进 result（nil 表示不要结果）。
	//
	// 对面回错误帧时交回一个 *[ResponseError]；写不出去或者连接已经断了时交回
	// 别的错误。ctx 取消时这次等待当场结束，不再为一个可能永远不来的响应留状态。
	Request(ctx context.Context, method string, params any, result any) error
	// Notify 发一次通知；params 为 nil 时帧上不带 params 成员。
	Notify(ctx context.Context, method string, params any) error
}

// Handlers 是这条通道收到东西之后往哪儿分。
//
// 源: packages/sdk/protocol/src/transport.ts:99-110
//
// 新增: DSH 那边是 onRequest / onNotification 两个「装上去，后装的盖掉先装的」的
// setter。Go 这边合成一个建构时就交进来的结构体：这条通道在 DSH 里也从来只装一次
// （sdk/server 的 apply 装一次就再也不动），而可变的 setter 意味着每一次分流都得
// 先读一个可能正在被别的 goroutine 改的字段。
type Handlers struct {
	// Request 处理一次收到的请求，交回的值序列化成响应的 result。
	//
	// 交回错误就变成一个错误响应帧。认不出的方法名请交回
	// [ErrMethodNotFound]，别的失败一律折成 -32603。nil 表示本端不接请求，
	// 那时每一个进来的请求都得到 -32601。
	Request func(ctx context.Context, method string, params json.RawMessage) (any, error)
	// Notification 处理一次收到的通知。nil 表示本端不接通知，那时它们被丢掉。
	//
	// 它没有返回值：通知在 JSON-RPC 里本来就没有回话的地方，一次失败无处可去。
	Notification func(ctx context.Context, method string, params json.RawMessage)
}

// ErrMethodNotFound 是「没有这个方法」，[Handlers.Request] 交回它（或者包着它的错误）
// 就得到一个 -32601 而不是 -32603。
//
// 源: packages/sdk/protocol/src/transport.ts:59（Missing request handlers return -32601）
var ErrMethodNotFound = errors.New("jsonrpc: 认不出的方法名")

// LineTransport 是架在调用方自己的那对流上的一条按行分帧的通道。
//
// 源: packages/sdk/protocol/src/transport.ts:62-269
//
// [NewLineTransport] 建出来就已经在读了，[LineTransport.Close] 停掉它并且把还等着
// 的请求全部打回；两头的流都**不**关——它们是调用方的东西（生产上就是本进程的
// stdin/stdout，关掉它们等于把整个进程的输入输出拆了）。
//
// 新增: DSH 那边 start() 和构造是分开的，而且 start 是幂等的。Go 这边合成一步：
// 那个分离在 TS 里的用处是让 cordis 的 effect 决定什么时候挂监听，而这里挂监听就是
// 起一个读循环的 goroutine，它跟这个对象的寿命完全重合。
type LineTransport struct {
	conn *jsonrpc2.Conn
}

// NewLineTransport 在 input 和 output 上架起一条通道并且当场开始读。
//
// 源: packages/sdk/protocol/src/transport.ts:70-82
//
// ctx 管着那个读循环的寿命：它结束时连接跟着关掉。
func NewLineTransport(ctx context.Context, input io.Reader, output io.Writer, handlers Handlers) *LineTransport {
	stream := &lineStream{reader: bufio.NewReader(input), writer: output}
	transport := &LineTransport{}
	transport.conn = jsonrpc2.NewConn(ctx, stream, jsonrpc2.AsyncHandler(&dispatcher{handlers: handlers}))
	return transport
}

// Request 实现 [Peer]。
//
// 源: packages/sdk/protocol/src/transport.ts:121-156
func (t *LineTransport) Request(ctx context.Context, method string, params any, result any) error {
	return t.conn.Call(ctx, method, params, result)
}

// Notify 实现 [Peer]。
//
// 源: packages/sdk/protocol/src/transport.ts:158-160
func (t *LineTransport) Notify(ctx context.Context, method string, params any) error {
	return t.conn.Notify(ctx, method, params)
}

// Close 停掉读循环并且把还等着的请求全部打回。两头的流都不关。
//
// 源: packages/sdk/protocol/src/transport.ts:87-92
//
// 它不交回错误，和 DSH 的 close() 一样：底下那次关闭唯一可能的失败是
// [jsonrpc2.ErrClosed]（本包的帧编解码 Close 恒成功），而「已经关过了」不是错误——
// 收摊那条路上关闭本来就会被走到两次，一次是 `shutdown` 请求，一次是作用域释放。
func (t *LineTransport) Close() {
	_ = t.conn.Close()
}

// Done 在这条通道断开之后关上，调用方靠它等一次对端 EOF。
//
// 新增: DSH 那边没有对应的东西——它靠 Node 的 'end' 事件，而那个事件是调用方自己
// 挂在流上的。Go 这边流是被这条通道读掉的，所以断开这件事得由它往外说。
func (t *LineTransport) Done() <-chan struct{} { return t.conn.DisconnectNotify() }

// dispatcher 把库交上来的那一帧分给 [Handlers] 里对应的那一个。
//
// 源: packages/sdk/protocol/src/transport.ts:226-238
type dispatcher struct{ handlers Handlers }

// Handle 实现 [jsonrpc2.Handler]。
//
// 通知那一支不回话；请求那一支必须回，包括没人处理的时候——那是一个 -32601。
func (d *dispatcher) Handle(ctx context.Context, conn *jsonrpc2.Conn, request *jsonrpc2.Request) {
	params := json.RawMessage("{}")
	if request.Params != nil {
		params = objectParams(*request.Params)
	}
	if request.Notif {
		if d.handlers.Notification != nil {
			d.handlers.Notification(ctx, request.Method, params)
		}
		return
	}
	if d.handlers.Request == nil {
		d.replyError(ctx, conn, request.ID, jsonrpc2.CodeMethodNotFound,
			fmt.Sprintf("method not found: %s", request.Method))
		return
	}
	result, err := d.handlers.Request(ctx, request.Method, params)
	if err != nil {
		code := int64(jsonrpc2.CodeInternalError)
		if errors.Is(err, ErrMethodNotFound) {
			code = jsonrpc2.CodeMethodNotFound
		}
		d.replyError(ctx, conn, request.ID, code, err.Error())
		return
	}
	// 交回 nil 的处理器要的是一个空对象而不是 JSON 的 null：`shutdown` 那条路的
	// 结果在协议上写死是 `{}`，而 Go 的 nil 会排成 null。
	if result == nil {
		result = struct{}{}
	}
	// 回不出去只可能是连接已经没了，那时这一帧本来也送不到，没有第二个地方可去。
	_ = conn.Reply(ctx, request.ID, result)
}

// replyError 回一个错误帧，写不出去就算了——那说明连接已经没了。
func (d *dispatcher) replyError(ctx context.Context, conn *jsonrpc2.Conn, id jsonrpc2.ID, code int64, message string) {
	_ = conn.ReplyWithError(ctx, id, &jsonrpc2.Error{Code: code, Message: message})
}

// objectParams 把线上的 params 收敛成一个普通对象：数组和标量都塌成 `{}`。
//
// 源: packages/sdk/protocol/src/transport.ts:272-274
//
// 这一层收敛是给处理器省事的：它们全都按具名字段解 params，而一个数组或者标量解
// 进结构体会是一次类型错误，塌成空对象之后走的则是「必填字段没给」那条正常的路。
func objectParams(params json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return json.RawMessage("{}")
	}
	return trimmed
}

// lineStream 是按行分帧、并且**跳过坏行**的那一层帧编解码。
//
// 源: packages/sdk/protocol/src/transport.ts:180-189, 201-208, 260-262
//
// 新增: 这是本包唯一没有交给 github.com/sourcegraph/jsonrpc2 的一层。那个库自带的
// [jsonrpc2.NewPlainObjectStream] 在解不动一行的时候把错误往上交，Conn 收到就把连接
// 关了；而这条协议的另一端是别人写的 SDK，一行断在半路的 JSON 不该让整条会话死掉。
type lineStream struct {
	reader *bufio.Reader
	writer io.Writer
	// write 串行化写：一帧必须整条写出去，两个 goroutine 交叉写会把两帧搅在一行里。
	write sync.Mutex
}

// ReadObject 读到下一个**认得出**的帧为止。
//
// 认不出的行（空行、不是合法 JSON、不是一个对象、以及是对象但既不像请求也不像响应）
// 一律跳过接着读，这一点和 DSH 的 handleLine 逐条对得上。
func (s *lineStream) ReadObject(value any) error {
	for {
		line, err := s.reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 && json.Valid(line) && line[0] == '{' {
			if unmarshalErr := json.Unmarshal(line, value); unmarshalErr == nil {
				return nil
			}
		}
		if err != nil {
			// 最后一行没有换行符时 ReadBytes 会连着数据一起交回 io.EOF，所以这个
			// 判断必须在解完那一行之后——反过来会把对面最后一帧吃掉。
			return err
		}
	}
}

// WriteObject 把一帧写成一行。
func (s *lineStream) WriteObject(object any) error {
	encoded, err := json.Marshal(object)
	if err != nil {
		return err
	}
	s.write.Lock()
	defer s.write.Unlock()
	_, err = s.writer.Write(append(encoded, '\n'))
	return err
}

// Close 什么都不做：两头的流是调用方的东西。
//
// 源: packages/sdk/protocol/src/transport.ts:87-92（close() 只摘监听，不动流）
//
// 生产上它们就是本进程的 stdin/stdout，关掉等于把整个进程的输入输出拆了。
func (s *lineStream) Close() error { return nil }
