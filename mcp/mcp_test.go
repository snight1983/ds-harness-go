// 本文件的作用：把这个包接到一台**真的** MCP 服务器上跑一遍——连上去、把工具注册进来、
// 隔着注册表调一次、对方改了清单跟着换代、关掉之后工具消失。
//
// 逐条对着 DSH 的 tests/mcp-client.spec.ts 走。那边用一台内存里的假服务器，
// 这里用 Go SDK 自己的服务器加 [net/http/httptest]：真的走一遍 Streamable HTTP，
// 因为本包和对方之间那份契约（分页、通知、`isError`、内容块）全在线上，
// 拿一个假的会话对象是验不出来的。
//
// # 这些测试防的是什么错
//
//   - **分页只取第一页**。对方工具一多，模型就只看得见前几件，而且症状随对方的
//     页大小变，本地怎么试都试不出来。
//   - **换代换到一半**。模型看见半代工具，调到的那一件可能已经不在对面了。
//   - **注册冲突之后留下残骸**。回滚不干净的话，下一次同步会撞上自己上一次的注册。
//   - **服务器名占用表不还回去**。同一个名字关掉再连就永远失败，只能重启进程。
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"ds-harness-go/attachment"
	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// objectSchema 是给夹具工具用的那份最小入参 schema。
//
// AddTool 要求根上是 object，本包这边也只有 object 说得出口，所以两边都满足。
const objectSchema = `{"type":"object","properties":{"text":{"type":"string"}}}`

// quietLogger 把测试里那些「连不上」「放弃重连」的日志压掉。
//
// 那些行本身是被测行为的一部分，但它们会淹掉 go test 的输出；本包已经在断言里
// 检查了对应的**行为**，所以日志本身不必打出来。
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// fixture 是一台跑在 httptest 上的真 MCP 服务器。
type fixture struct {
	// server 是那台 MCP 服务器，测试中途可以往里加减工具。
	server *sdk.Server
	// http 是承载它的 HTTP 服务器。
	http *httptest.Server
	// headers 记下最后一次请求带来的请求头，自定义请求头那条靠它验。
	headers http.Header
	// mutex 护住 headers；SDK 客户端会并发发几条请求。
	mutex sync.Mutex
	// brokenHits 数的是坏掉期间挡下了多少条请求。
	brokenHits atomic.Int64
	// broken 为真时这台服务器对每一条请求都回 404。
	//
	// 断线要靠一个**确定**的信号来造：CloseClientConnections 只是掐断在途的 TCP，
	// SDK 的可续传逻辑会自己接回去，监督者根本不知道断过。回 404 才让客户端认定
	// 这个会话没了，把 Wait 放回来。
	broken atomic.Bool
}

// url 是这台服务器的 MCP 端点。
func (f *fixture) url() string { return f.http.URL }

// lastHeaders 交回最后一次请求的请求头。
func (f *fixture) lastHeaders() http.Header {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.headers.Clone()
}

// newFixture 起一台 MCP 服务器。
//
// PageSize 故意设成 1：本包那条分页循环只有在对方真的分页时才走得到，
// 而一台默认配置的服务器一页能装下所有工具。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, &sdk.ServerOptions{
		PageSize: 1,
		Logger:   quietLogger(),
	})
	handler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Logger: quietLogger()},
	)
	f := &fixture{server: server}
	f.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mutex.Lock()
		f.headers = r.Header.Clone()
		f.mutex.Unlock()
		if f.broken.Load() {
			f.brokenHits.Add(1)
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.http.Close)
	return f
}

// addText 装一件回一段固定文本的工具。
func (f *fixture) addText(name, text string) {
	f.server.AddTool(
		&sdk.Tool{Name: name, Description: name + " 的说明", InputSchema: json.RawMessage(objectSchema)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil
		},
	)
}

// addHandler 装一件自定义行为的工具。
func (f *fixture) addHandler(name string, handler sdk.ToolHandler) {
	f.server.AddTool(
		&sdk.Tool{Name: name, Description: name + " 的说明", InputSchema: json.RawMessage(objectSchema)},
		handler,
	)
}

// harness 是一次连接测试要的全套家当。
type harness struct {
	fixture *fixture
	host    *Host
	runtime *tools.Runtime
	owner   *scope.Scope
	agent   *scope.Key
}

// newHarness 造一个装好注册表和宿主、但还没连的家当。
func newHarness(t *testing.T, admit ImageAdmission) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	host, err := NewHost(Options{Tools: runtime, ImageAdmission: admit, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造宿主失败：%v", err)
	}
	return &harness{
		fixture: newFixture(t),
		host:    host,
		runtime: runtime,
		owner:   scope.NewRoot(),
		agent:   scope.NewKey("agent"),
	}
}

// connect 连上夹具服务器，并且登记好收尾。
func (h *harness) connect(t *testing.T, config Config) *Connection {
	t.Helper()
	if config.ServerName == "" {
		config.ServerName = "files"
	}
	if config.URL == "" {
		config.URL = h.fixture.url()
	}
	connection, err := h.host.Connect(context.Background(), h.owner, config)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("关连接出错：%v", err)
		}
	})
	return connection
}

// names 是当前这个 agent 看得见的全部工具名。
func (h *harness) names() []string { return h.runtime.KnownNames(h.agent) }

// call 隔着注册表调一次工具。
func (h *harness) call(t *testing.T, name, args string) tools.Result {
	t.Helper()
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("call-1"),
		Name:      name,
		Arguments: json.RawMessage(args),
		Agent:     h.agent,
	})
}

// waitFor 等一个条件成立，超时就让测试失败。
//
// 换代是异步的（`tools/list_changed` 到了之后监督者才重新同步），所以只能轮询；
// 轮询间隔短、上限宽，是为了在慢机器上也不假失败。
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等不到：%s", what)
}

// ---------------------------------------------------------------- 连接与注册

func TestConnectRegistersEveryPageOfTools(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.fixture.addText("beta", "乙")
	h.fixture.addText("gamma", "丙")
	h.connect(t, Config{})

	got := h.names()
	want := []string{"mcp__files__alpha", "mcp__files__beta", "mcp__files__gamma"}
	for _, name := range want {
		if !containsName(got, name) {
			t.Fatalf("分页没有抽干，少了 %q：%#v", name, got)
		}
	}
	definition, ok := h.runtime.Get("mcp__files__alpha", h.agent)
	if !ok {
		t.Fatal("取不到刚注册的定义")
	}
	if definition.Description != "alpha 的说明" {
		t.Fatalf("说明没带过来：%q", definition.Description)
	}
	if definition.Parameters.Type != tools.TypeObject || len(definition.Parameters.Properties) != 1 {
		t.Fatalf("入参 schema 没解对：%#v", definition.Parameters)
	}
}

func TestConnectRejectsDuplicateServerName(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	first := h.connect(t, Config{ServerName: "files"})

	_, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(),
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("重名应当被拒：%v", err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("拒绝的说法不对：%v", err)
	}
	// 先来的那一个一根汗毛都不能动。
	if !containsName(h.names(), "mcp__files__alpha") {
		t.Fatal("重名把先来的那条连接的工具弄没了")
	}

	// 关掉之后名字要还回去，不然这个名字就永久烧掉了。
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("关连接出错：%v", err)
	}
	again, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(),
	})
	if err != nil {
		t.Fatalf("名字没还回去：%v", err)
	}
	if err := again.Close(context.Background()); err != nil {
		t.Fatalf("关第二条连接出错：%v", err)
	}
}

func TestConnectRejectsBadArguments(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.host.Connect(context.Background(), nil, Config{
		ServerName: "files", URL: h.fixture.url(),
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域应当被拒：%v", err)
	}
	if _, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "不合法", URL: h.fixture.url(),
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("非法服务器名应当被拒：%v", err)
	}
	if _, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(),
		Reconnect: ReconnectConfig{InitialDelay: time.Minute, MaxDelay: time.Second},
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("配错的重连策略应当被拒：%v", err)
	}
	// 策略验在占名之前：那次失败不该把名字烧掉。
	h.fixture.addText("alpha", "甲")
	h.connect(t, Config{ServerName: "files"})
}

func TestNewHostRequiresRegistry(t *testing.T) {
	if _, err := NewHost(Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有注册表应当被拒：%v", err)
	}
	host, err := NewHost(Options{Tools: &tools.Runtime{}})
	if err != nil {
		t.Fatalf("造宿主失败：%v", err)
	}
	if host.logger == nil {
		t.Fatal("没有补上默认日志")
	}
}

func TestConnectFailOnStartupError(t *testing.T) {
	h := newHarness(t, nil)
	dead := "http://127.0.0.1:1/mcp"

	_, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName:         "files",
		URL:                dead,
		FailOnStartupError: true,
		Reconnect:          ReconnectConfig{Disabled: true},
	})
	if err == nil {
		t.Fatal("首次连不上却没报错")
	}
	if !strings.Contains(err.Error(), "initial connection or tool synchronization failed") {
		t.Fatalf("报错的说法不对：%v", err)
	}
	// 失败那条连接已经清理干净了，名字要能再用。
	connection, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files",
		URL:        dead,
		Reconnect:  ReconnectConfig{Disabled: true},
	})
	if err != nil {
		t.Fatalf("不要求启动成功时不该报错：%v", err)
	}
	if len(h.names()) != 0 {
		t.Fatalf("一条没连上的连接注册了工具：%#v", h.names())
	}
	if err := connection.Close(context.Background()); err != nil {
		t.Fatalf("关连接出错：%v", err)
	}
}

func TestConnectCancelledDuringStartup(t *testing.T) {
	h := newHarness(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.host.Connect(ctx, h.owner, Config{
		ServerName: "files",
		URL:        "http://127.0.0.1:1/mcp",
		Reconnect:  ReconnectConfig{Disabled: true},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("撤回应当如实交出去：%v", err)
	}
	// 撤回时要把整条连接拆掉，名字跟着还回去。
	if err := h.host.claim("files"); err != nil {
		t.Fatalf("撤回之后名字没还回去：%v", err)
	}
}

func TestCloseUnregistersTools(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	connection, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(),
	})
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	if !containsName(h.names(), "mcp__files__alpha") {
		t.Fatal("连上之后工具没进来")
	}
	if err := connection.Close(context.Background()); err != nil {
		t.Fatalf("关连接出错：%v", err)
	}
	if len(h.names()) != 0 {
		t.Fatalf("关掉之后工具还在：%#v", h.names())
	}
	// 多关几次是安全的。
	if err := connection.Close(context.Background()); err != nil {
		t.Fatalf("重复关闭出错：%v", err)
	}
}

func TestCloseReportsCallerCancellation(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	connection, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(),
	})
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connection.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("调用方的撤回应当如实转出去：%v", err)
	}
	// 撤回归撤回，工具还是撤干净了：监督者用的是一个不带取消的 ctx。
	if len(h.names()) != 0 {
		t.Fatalf("撤回让工具留在了注册表里：%#v", h.names())
	}
}

// ---------------------------------------------------------------- 换代

func TestToolListChangedResyncs(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.connect(t, Config{})

	h.fixture.addText("beta", "乙")
	waitFor(t, "新工具进来", func() bool { return containsName(h.names(), "mcp__files__beta") })

	h.fixture.server.RemoveTools("alpha")
	waitFor(t, "撤掉的工具消失", func() bool { return !containsName(h.names(), "mcp__files__alpha") })
	if !containsName(h.names(), "mcp__files__beta") {
		t.Fatal("换代把还在的那一件也撤掉了")
	}
}

func TestSyncRollsBackOnRegistrationConflict(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.fixture.addText("beta", "乙")

	// 先有人占了这台服务器的命名空间：换代那一步一定会撞上它。
	if _, err := h.runtime.Register(context.Background(), h.owner, &tools.Definition{
		Name:       "mcp__files__beta",
		Parameters: tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) { return nil, nil },
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}); err != nil {
		t.Fatalf("占名失败：%v", err)
	}

	_, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(), FailOnStartupError: true,
		Reconnect: ReconnectConfig{Disabled: true},
	})
	if err == nil {
		t.Fatal("撞名却连成功了")
	}
	// 回滚要干净：alpha 是在 beta 之前注册进去的，它必须被撤回。
	if containsName(h.names(), "mcp__files__alpha") {
		t.Fatalf("回滚不干净，留下了半代工具：%#v", h.names())
	}
}

// TestSyncSwallowsRegistrationConflictWhenNotStrict 盯住不严格那一档：撞名之后
// 这条连接照样建起来，只是一件工具都没有。
//
// 这是 DSH 的 registrationFailure: 'contain'。它要紧在：一台占了别人命名空间的
// 服务器不该把整个宿主拖下水，但模型也不该看见半代工具。
func TestSyncSwallowsRegistrationConflictWhenNotStrict(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.fixture.addText("beta", "乙")
	if _, err := h.runtime.Register(context.Background(), h.owner, &tools.Definition{
		Name:       "mcp__files__beta",
		Parameters: tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) { return nil, nil },
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}); err != nil {
		t.Fatalf("占名失败：%v", err)
	}

	h.connect(t, Config{Reconnect: ReconnectConfig{Disabled: true}})
	if containsName(h.names(), "mcp__files__alpha") {
		t.Fatalf("咽下去之后不该留下半代工具：%#v", h.names())
	}
}

// TestCallAfterTheServerIsGone 盯住「工具还挂着、服务器已经没了」这一路。
//
// 重连关掉之后那批工具仍然注册着（DSH 明说这是有意的：撤掉的话模型连「这件事
// 做不了」都说不出来）。那么调用它就必须给模型一份失败结果，而不是崩掉。
func TestCallAfterTheServerIsGone(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.connect(t, Config{Reconnect: ReconnectConfig{Disabled: true}})
	h.fixture.broken.Store(true)
	h.fixture.http.CloseClientConnections()

	result := h.call(t, "mcp__files__alpha", `{}`)
	if !result.IsError {
		t.Fatalf("服务器没了却算成功：%#v", result)
	}
}

// TestCloseDoesNotWaitOutTheBackoff 盯住「关连接时监督者正睡在退避里」这一路。
//
// 退避可以长到几十秒，而关闭是调用方在等的事：它必须立刻回来，不能等睡完。
func TestCloseDoesNotWaitOutTheBackoff(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	connection := h.connect(t, Config{Reconnect: ReconnectConfig{
		InitialDelay: 30 * time.Second,
		MaxDelay:     time.Minute,
		MaxAttempts:  1000,
	}})
	h.fixture.broken.Store(true)
	h.fixture.http.CloseClientConnections()
	waitFor(t, "客户端撞上坏掉的服务器", func() bool { return h.fixture.brokenHits.Load() > 0 })

	closed := make(chan error, 1)
	go func() { closed <- connection.Close(context.Background()) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("关连接出错：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("关连接等在退避里没回来")
	}
}

func TestSyncRejectsUnparseableInputSchema(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.server.AddTool(
		// 根上是 object，所以对方那一侧收得下；properties 不是对象，本包这边说不出口。
		&sdk.Tool{Name: "broken", InputSchema: json.RawMessage(`{"type":"object","properties":"不是对象"}`)},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return nil, nil },
	)
	_, err := h.host.Connect(context.Background(), h.owner, Config{
		ServerName: "files", URL: h.fixture.url(), FailOnStartupError: true,
		Reconnect: ReconnectConfig{Disabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "input schema is unsupported") {
		t.Fatalf("说不出口的入参 schema 应当让整次同步失败：%v", err)
	}
}

// ---------------------------------------------------------------- 调用

func TestCallRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addHandler("echo", func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: "收到 " + string(request.Params.Arguments)}},
			StructuredContent: map[string]any{"ok": true},
		}, nil
	})
	h.connect(t, Config{})

	result := h.call(t, "mcp__files__echo", `{"text":"甲"}`)
	if result.IsError {
		t.Fatalf("调用失败：%#v", result.Error)
	}
	var value Result
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("权威值解不开：%v", err)
	}
	if len(value.Content) != 1 || wireType(value.Content[0]) != blockText {
		t.Fatalf("权威值里的内容不对：%s", result.Value)
	}
	if string(value.StructuredContent) != `{"ok":true}` {
		t.Fatalf("结构化返回值丢了：%s", result.Value)
	}
	text := textOf(t, result.Content)
	if len(text) != 1 || !strings.Contains(text[0], `收到 {"text":"甲"}`) {
		t.Fatalf("投影给模型的文本不对：%#v", text)
	}
}

func TestCallCoercesNonObjectArguments(t *testing.T) {
	h := newHarness(t, nil)
	var seen string
	h.fixture.addHandler("echo", func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		seen = string(request.Params.Arguments)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	})
	h.connect(t, Config{})
	definition, ok := h.runtime.Get("mcp__files__echo", h.agent)
	if !ok {
		t.Fatal("取不到定义")
	}

	// 直接调执行体，不走注册表：本装置的管线在派发之前就把非对象参数挡下了
	// （报「"arguments" must be an object」），所以这条退化只对程序化直调的人生效。
	// 它仍然要留着——[tools.Definition.Execute] 是一个公开字段，DSH 那边这条也在。
	if _, err := definition.Execute(context.Background(), json.RawMessage(`null`), nil); err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if seen != "{}" {
		t.Fatalf("非对象参数没有退回空对象：%q", seen)
	}
}

func TestCallSurfacesToolError(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addHandler("boom", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "对面炸了"}},
		}, nil
	})
	h.connect(t, Config{})

	result := h.call(t, "mcp__files__boom", `{}`)
	if !result.IsError {
		t.Fatal("对方回 isError 却被当成了成功")
	}
	if !strings.Contains(result.Error.Message, "对面炸了") {
		t.Fatalf("错误文本没带过来：%#v", result.Error)
	}
}

func TestCallWithoutContent(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addHandler("silent", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	h.fixture.addHandler("silentError", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{IsError: true}, nil
	})
	h.connect(t, Config{})

	// Go SDK 把「没有内容」在线上归一成一个空列表，所以走的是「有列表但空」那一支，
	// 也就是 [projectContent] 那句说得出工具名的话。[noOutput] 那一支留给**不是**
	// 这个 SDK 写的服务器——它们真的会把 content 整个省掉，DSH 见过那种。
	result := h.call(t, "mcp__files__silent", `{}`)
	if result.IsError {
		t.Fatalf("调用失败：%#v", result.Error)
	}
	if got := textOf(t, result.Content); len(got) != 1 || got[0] != "(silent returned no model-visible content)" {
		t.Fatalf("一块内容都没给时的说法不对：%#v", got)
	}
	failed := h.call(t, "mcp__files__silentError", `{}`)
	if !failed.IsError ||
		!strings.Contains(failed.Error.Message, "(silentError returned no model-visible content)") {
		t.Fatalf("既没内容又是 isError 时的说法不对：%#v", failed.Error)
	}
}

func TestCallProjectsAdmittedImage(t *testing.T) {
	store := &fakeStore{}
	h := newHarness(t, admitting(store))
	h.fixture.addHandler("shot", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "这是截图"},
			&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		}}, nil
	})
	h.connect(t, Config{})

	result := h.call(t, "mcp__files__shot", `{}`)
	if result.IsError {
		t.Fatalf("调用失败：%#v", result.Error)
	}
	if len(result.Content) != 2 {
		t.Fatalf("投影出来的块数不对：%#v", result.Content)
	}
	image, ok := result.Content[1].(llm.ImageBlock)
	if !ok {
		t.Fatalf("图没有换进模型内容里：%#v", result.Content[1])
	}
	if image.Attachment.MediaType != attachment.MediaTypePNG {
		t.Fatalf("存下来的引用不对：%#v", image.Attachment)
	}
	// 那份权威值一个字节不改：程序化调用方仍然读得到原始的图。
	var value Result
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("权威值解不开：%v", err)
	}
	if len(value.Content) != 2 || wireType(value.Content[1]) != blockImage {
		t.Fatalf("原始的图不在权威值里：%s", result.Value)
	}
}

func TestCallDegradesImageWithoutAdmission(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addHandler("shot", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		}}, nil
	})
	h.connect(t, Config{})

	result := h.call(t, "mcp__files__shot", `{}`)
	if result.IsError {
		t.Fatalf("调用失败：%#v", result.Error)
	}
	got := textOf(t, result.Content)
	if len(got) != 1 || !strings.Contains(got[0], "no attachment store is mounted") {
		t.Fatalf("没装仓库时应当降级成诊断文本：%#v", got)
	}
}

func TestFinalizeContentRefusesAMutatedResult(t *testing.T) {
	h := newHarness(t, admitting(&fakeStore{}))
	h.fixture.addHandler("shot", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		}}, nil
	})
	h.connect(t, Config{})
	definition, ok := h.runtime.Get("mcp__files__shot", h.agent)
	if !ok {
		t.Fatal("取不到定义")
	}

	// 没有记过投影的执行：什么都不换。
	if got := definition.FinalizeContent(tools.Execution{}, tools.Result{}); got != nil {
		t.Fatalf("没记过投影却换了内容：%#v", got)
	}

	// 走完整条管线的那一路：投影准入，模型看见的是图。
	result := h.call(t, "mcp__files__shot", `{}`)
	if _, ok := result.Content[0].(llm.ImageBlock); !ok {
		t.Fatalf("正常那一路本该换成图：%#v", result.Content[0])
	}

	// 下面几路直接问收尾：每一路先跑一次执行体把投影记下来（记完只能取一次），
	// 再拿一份被动过手脚的结果去问它换不换。
	stage := func(t *testing.T) (json.RawMessage, llm.Content) {
		t.Helper()
		value, err := definition.Execute(
			context.Background(), json.RawMessage(`{}`), &tools.RunContext{Execution: tools.Execution{}})
		if err != nil {
			t.Fatalf("执行体出错：%v", err)
		}
		fallback, err := definition.Output.Render(nil, value)
		if err != nil {
			t.Fatalf("渲染出错：%v", err)
		}
		return value, fallback
	}

	// 这次调用被判成失败：失败的结果里不该混进一张图。
	value, fallback := stage(t)
	if got := definition.FinalizeContent(
		tools.Execution{}, tools.Result{IsError: true, Value: value, Content: fallback}); got != nil {
		t.Fatalf("失败的结果不该换成图：%#v", got)
	}

	// 落地的值被人改过：那份投影说的已经不是同一件事了。
	_, fallback = stage(t)
	if got := definition.FinalizeContent(
		tools.Execution{}, tools.Result{Value: json.RawMessage(`{"content":[]}`), Content: fallback}); got != nil {
		t.Fatalf("值被改过还换图：%#v", got)
	}

	// 值没变，但收尾前看到的内容不是 Render 渲染出来的那份：中间有人包过一层。
	value, _ = stage(t)
	if got := definition.FinalizeContent(
		tools.Execution{}, tools.Result{Value: value, Content: llm.Content{llm.TextBlock{Text: "别人写的"}}}); got != nil {
		t.Fatalf("内容被改过还换图：%#v", got)
	}

	// 两样都对得上：这才换。
	value, fallback = stage(t)
	got := definition.FinalizeContent(tools.Execution{}, tools.Result{Value: value, Content: fallback})
	if len(got) == 0 {
		t.Fatal("两样都对得上却没换成图")
	}
	if _, ok := got[len(got)-1].(llm.ImageBlock); !ok {
		t.Fatalf("换出来的不是图：%#v", got)
	}
}

func TestCustomHeadersReachTheServer(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.connect(t, Config{Headers: map[string]string{"X-Tenant": "acme"}})

	if got := h.fixture.lastHeaders().Get("X-Tenant"); got != "acme" {
		t.Fatalf("自定义请求头没送到：%q", got)
	}
}

// ---------------------------------------------------------------- 重连决策

// newSupervisorForPlanning 造一个只用来问 planReconnect 的监督者。
//
// 那个方法只读策略和日志，不碰连接，所以其余字段留零值就够了。
func newSupervisorForPlanning(policy ReconnectPolicy) *supervisor {
	return &supervisor{label: "mcp-client(files)", policy: policy, logger: quietLogger()}
}

func TestPlanReconnectStopsWhenDisabled(t *testing.T) {
	s := newSupervisorForPlanning(ReconnectPolicy{})
	attempts := 0
	var disposers toolDisposers
	if _, keepGoing := s.planReconnect(context.Background(), time.Time{}, &attempts, &disposers); keepGoing {
		t.Fatal("关掉重连之后还想再连")
	}
	// 断掉一条已经连上的连接，走的是另一句话，但同样不再重连。
	if _, keepGoing := s.planReconnect(context.Background(), time.Now(), &attempts, &disposers); keepGoing {
		t.Fatal("关掉重连之后还想再连")
	}
}

func TestPlanReconnectBacksOffThenGivesUp(t *testing.T) {
	s := newSupervisorForPlanning(ReconnectPolicy{
		Enabled:      true,
		InitialDelay: time.Millisecond,
		MaxDelay:     4 * time.Millisecond,
		MaxAttempts:  2,
	})
	attempts := 0
	disposed := 0
	disposers := toolDisposers{func(context.Context) error { disposed++; return nil }}

	delay, keepGoing := s.planReconnect(context.Background(), time.Time{}, &attempts, &disposers)
	if !keepGoing || delay != time.Millisecond {
		t.Fatalf("第一次应当等一个首次延迟：%v / %v", delay, keepGoing)
	}
	delay, keepGoing = s.planReconnect(context.Background(), time.Time{}, &attempts, &disposers)
	if !keepGoing || delay != 2*time.Millisecond {
		t.Fatalf("第二次应当翻一倍：%v / %v", delay, keepGoing)
	}
	if _, keepGoing = s.planReconnect(context.Background(), time.Time{}, &attempts, &disposers); keepGoing {
		t.Fatal("预算用光了还想再连")
	}
	// 彻底放弃时要把工具撤掉：留着它们只会让模型一直调到一条死连接上。
	if disposed != 1 || disposers != nil {
		t.Fatalf("放弃时没有把工具撤干净：%d / %#v", disposed, disposers)
	}
}

func TestPlanReconnectResetsBudgetAfterAStableConnection(t *testing.T) {
	s := newSupervisorForPlanning(ReconnectPolicy{
		Enabled:      true,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
		MaxAttempts:  2,
	})
	attempts := 5
	var disposers toolDisposers
	// 这条连接撑过了稳定窗口（MaxDelay = 1ms），所以上一次断线事件算翻篇了。
	delay, keepGoing := s.planReconnect(
		context.Background(), time.Now().Add(-time.Second), &attempts, &disposers)
	if !keepGoing || delay != time.Millisecond || attempts != 1 {
		t.Fatalf("失败预算没有从头开始算：%v / %v / %d", delay, keepGoing, attempts)
	}
}

func TestReconnectAfterTheServerComesBack(t *testing.T) {
	h := newHarness(t, nil)
	h.fixture.addText("alpha", "甲")
	h.connect(t, Config{Reconnect: ReconnectConfig{
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		MaxAttempts:  1000,
	}})
	if !containsName(h.names(), "mcp__files__alpha") {
		t.Fatal("连上之后工具没进来")
	}

	// 把服务器弄坏：那条常驻 SSE 流一断，客户端就认定这个会话没了，监督者进重连循环。
	// 掐断在途连接是为了让客户端**立刻**去重连那条流，而不是等它自己发现。
	h.fixture.broken.Store(true)
	h.fixture.http.CloseClientConnections()
	// 要等到客户端真的撞上坏掉的服务器才能把它修好，否则这一局什么都没测到：
	// 修得太快的话客户端那次重试会成功，会话根本没死过。
	waitFor(t, "客户端撞上坏掉的服务器", func() bool { return h.fixture.brokenHits.Load() > 0 })
	// 工具在重连期间**不撤**：预算没用光之前，模型看得见的那一份保持不变。
	if !containsName(h.names(), "mcp__files__alpha") {
		t.Fatal("重连期间工具不该被撤掉")
	}

	// 服务器回来了。这一代新连接要把整份清单重新取一遍，所以中途加的那件工具
	// 也得跟着进来——那正是「重连之后真的重新同步过」的证据。
	h.fixture.addText("beta", "乙")
	h.fixture.broken.Store(false)
	waitFor(t, "重连之后的换代", func() bool { return containsName(h.names(), "mcp__files__beta") })
}

// ---------------------------------------------------------------- 传输

func TestTransportAddsHeadersOnlyWhenAsked(t *testing.T) {
	plain := (&supervisor{config: Config{URL: "http://x"}}).transport()
	if plain.(*sdk.StreamableClientTransport).HTTPClient != nil {
		t.Fatal("没有自定义请求头时不该换掉 HTTP 客户端")
	}
	withHeaders := (&supervisor{config: Config{
		URL: "http://x", Headers: map[string]string{"X-A": "1"},
	}}).transport()
	if withHeaders.(*sdk.StreamableClientTransport).HTTPClient == nil {
		t.Fatal("有自定义请求头时应当换上自己的 HTTP 客户端")
	}
}

// recordingRoundTripper 记下它看到的那条请求。
type recordingRoundTripper struct {
	seen *http.Request
}

func (r *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.seen = request
	return nil, errors.New("测试里不真发")
}

func TestHeaderRoundTripperDoesNotMutateTheOriginal(t *testing.T) {
	base := &recordingRoundTripper{}
	tripper := &headerRoundTripper{base: base, headers: map[string]string{"X-A": "1"}}
	original, err := http.NewRequest(http.MethodGet, "http://x", nil)
	if err != nil {
		t.Fatalf("造请求失败：%v", err)
	}
	if _, err := tripper.RoundTrip(original); err == nil {
		t.Fatal("底下那一层本该报错")
	}
	if original.Header.Get("X-A") != "" {
		t.Fatal("RoundTripper 改了传进来的那条请求")
	}
	if base.seen.Header.Get("X-A") != "1" {
		t.Fatalf("请求头没挂上去：%v", base.seen.Header)
	}
}

// ---------------------------------------------------------------- 小工具

// containsName 判一列工具名里有没有某一个。
func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
