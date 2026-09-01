// 本文件的作用：起监听、认路由、选行为、记录结局，以及把服务器干净地关掉。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:617-738

package mockserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Server 是一台跑着的模拟服务器，以及它记下来的请求。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:156-168（MockLlmServer）
type Server struct {
	resolved   resolvedOptions
	baseURL    string
	port       int
	listener   net.Listener
	httpServer *http.Server

	// mu 护着游标、随机源和全部请求记录。
	//
	// 新增: DSH 跑在单线程事件循环上，这些状态不需要任何保护。Go 的 [http.Server]
	// 一个连接一个协程，同一台服务器上的两个请求是真并发的——剧本游标要是不锁，
	// 两个同时进来的请求会拿到同一条剧本，而剧本的意义正是「一次请求消费一条」。
	mu      sync.Mutex
	cursor  int
	random  func() float64
	records []*RequestRecord

	// closing 在 [Server.Close] 开始时关闭，用来叫醒挂着不动的 stall 处理器。
	closing   chan struct{}
	closeOnce sync.Once
	closeErr  error
	handlers  sync.WaitGroup
	serveDone chan struct{}
}

// Start 起一台按剧本演故障的聊天补全服务器，端口绑好之后才返回。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:617-738
//
// 只有 POST 到以 /chat/completions 结尾的路径才消费剧本；方法、路径、令牌、
// 请求体 JSON 这四道不合格的请求按普通 4xx 回掉，剧本游标不动。
//
// 新增: DSH 那边返回的是 Promise，绑定失败会 reject。Go 换成 (值, error)，
// 而且**监听器是先建好再返回的**——TS 靠 server.listen 的回调，Go 直接
// [net.Listen] 拿到端口再把 [http.Server] 挂上去，端口号在返回时一定是真的。
func Start(options Options) (*Server, error) {
	resolved, err := resolveOptions(options)
	if err != nil {
		return nil, err
	}

	address := net.JoinHostPort(resolved.host, strconv.Itoa(resolved.port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("mockserver: 监听 %s 失败：%w", address, err)
	}
	boundPort := listener.Addr().(*net.TCPAddr).Port

	server := &Server{
		resolved: resolved,
		// 新增: TS 手写了「IPv6 就加方括号」那一步。Go 的 [net.JoinHostPort] 本来
		// 就按这条规则拼，不用自己判地址族。
		baseURL:   "http://" + net.JoinHostPort(resolved.host, strconv.Itoa(boundPort)),
		port:      boundPort,
		listener:  listener,
		random:    seededRandom(resolved.randomSeed),
		closing:   make(chan struct{}),
		serveDone: make(chan struct{}),
	}
	server.httpServer = &http.Server{Handler: server}

	go func() {
		defer close(server.serveDone)
		_ = server.httpServer.Serve(listener)
	}()
	return server, nil
}

// BaseURL 是不带 /v1 的根地址；根路径和 /v1 两种聊天补全路径都收。
func (s *Server) BaseURL() string { return s.baseURL }

// Port 是真正绑上的端口，含操作系统分配的那种。
func (s *Server) Port() int { return s.port }

// RandomSeed 是随机行为选择用的种子，含本包自己生成的那一个。
//
// 一次跑挂了的随机长跑要能重放，靠的就是把这个数记下来再传回 [Options.RandomSeed]。
func (s *Server) RandomSeed() uint32 { return s.resolved.randomSeed }

// Requests 交出按到达顺序排列的请求记录快照。
//
// 新增: TS 交的是那个活数组，测试直接看着它变。Go 这边记录会被多个处理器协程
// 并发改写，交活的等于把数据竞争送出去，所以交副本；想看最新状态就再取一次。
func (s *Server) Requests() []RequestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := make([]RequestRecord, len(s.records))
	for index, record := range s.records {
		snapshot[index] = *record
	}
	return snapshot
}

// Close 停止接受新请求、掐断挂着的连接，然后等所有处理器退干净。可以重复调用。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:715-719
//
// 顺序是有讲究的：先关 closing 把 stall 那种挂着不动的处理器叫醒（它们会自己把
// 连接掐掉），再关监听器和其余连接，最后等处理器协程全部返回。反过来的话
// [http.Server.Close] 不会去管一个正阻塞着的处理器，Close 返回之后测试进程里
// 还留着活的协程，`go test -race` 会在别处报出来。
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.closing)
		s.closeErr = s.httpServer.Close()
		s.handlers.Wait()
		<-s.serveDone
	})
	return s.closeErr
}

// emit 把一条遥测交给观察者。观察者自己炸了不影响线路上的行为。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:291-297
//
// 新增: TS 那边观察者失败表现为抛异常，用 try/catch 吞掉。Go 里对应的是 panic，
// 用 recover 吞掉。吞掉的理由和那边一样：遥测是旁观者，一个写坏了的观察者不该
// 把被测那一侧看到的供应商行为改掉——那会让人去查一个根本不存在的协议问题。
func (s *Server) emit(event Event) {
	if s.resolved.onEvent == nil {
		return
	}
	defer func() { _ = recover() }()
	s.resolved.onEvent(event)
}

// finishRecord 给一条记录定下结局并发出结果遥测。先到的那个结局算数。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:331-346
//
// 「先到的算数」不是省事：一个客户端提前走掉的请求，随后还会走到处理器自己的
// 收尾分支上，两边都想写结局。真实发生的是前一件事，后一件只是它的后果。
func (s *Server) finishRecord(record *RequestRecord, outcome Outcome) {
	s.mu.Lock()
	if record.Outcome != "" {
		s.mu.Unlock()
		return
	}
	record.Outcome = outcome
	event := ResultEvent{
		Attempt:        record.Attempt,
		ScriptBehavior: record.ScriptBehavior,
		Behavior:       record.Behavior,
		Outcome:        outcome,
		ChunksSent:     record.ChunksSent,
	}
	s.mu.Unlock()
	s.emit(event)
}

// selectBehavior 消费一条剧本，并把 random 展开成一种具体行为。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:632-646
func (s *Server) selectBehavior() (scriptBehavior, behavior Behavior) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursor < len(s.resolved.sequence) {
		scriptBehavior = s.resolved.sequence[s.cursor]
	} else if s.resolved.repeatLast {
		scriptBehavior = s.resolved.lastBehavior
	} else {
		scriptBehavior = BehaviorScriptExhausted
	}
	s.cursor++
	behavior = scriptBehavior
	if scriptBehavior == BehaviorRandom {
		behavior = chooseRandomBehavior(s.resolved.randomWeights, s.random)
	}
	return scriptBehavior, behavior
}

// ServeHTTP 是全部请求的入口：先过四道门，过了才消费剧本。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:648-698
//
// 新增: DSH 那个处理器是喂给 createServer 的一个闭包，除了它自己那台服务器谁也
// 拿不到。Go 这边导出成 [http.Handler]，多得两件事：调用方可以把这台模拟服务器
// 挂进自己的 [net/http/httptest.Server]（换一套 TLS、加一层中间件都行），以及那些
// 只关心「处理器面对某种 ResponseWriter 会怎么办」的用例不必真起监听器——重置类
// 行为在一个不实现 [http.Hijacker] 的 writer 上会走到上报失败那条路，而 DSH 的
// 同一段兜底在 Node 下根本构造不出来，只能标成不计覆盖。
func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.handlers.Add(1)
	defer s.handlers.Done()

	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasSuffix(request.URL.Path, "/chat/completions") {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if s.resolved.apiKey != "" && request.Header.Get("Authorization") != "Bearer "+s.resolved.apiKey {
		writeJSONError(writer, http.StatusUnauthorized, nil, "invalid mock bearer token", "", "invalid_api_key")
		return
	}
	body, hasBody, err := readJSONBody(request.Body)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, nil, "request body must be valid JSON", "", "invalid_json")
		return
	}

	scriptBehavior, behavior := s.selectBehavior()
	record := &RequestRecord{
		ScriptBehavior: scriptBehavior,
		Behavior:       behavior,
		Path:           request.URL.Path,
		Header:         request.Header.Clone(),
		Body:           body,
		HasBody:        hasBody,
	}
	s.mu.Lock()
	s.records = append(s.records, record)
	record.Attempt = len(s.records)
	s.mu.Unlock()

	s.emit(RequestEvent{
		Attempt:        record.Attempt,
		ScriptBehavior: scriptBehavior,
		Behavior:       behavior,
		Path:           record.Path,
	})

	// 客户端半路走掉的守望者。
	//
	// 源: packages/test-support/llm-mock-server/src/index.ts:685-689
	//
	// 新增: TS 挂在 response 的 close 事件上。Go 这边等价物是请求上下文——但它
	// 在处理器返回时也会被取消，所以要拿 handlerDone 把「客户端走了」和「我自己
	// 干完了」分开。handlerDone 由 defer 关闭，严格早于 net/http 取消上下文，
	// 因此干完活的正常路径不会被误记成客户端断开。
	handlerDone := make(chan struct{})
	defer close(handlerDone)
	go func() {
		select {
		case <-request.Context().Done():
			s.finishRecord(record, OutcomeClientClosed)
		case <-handlerDone:
		}
	}()

	exchange := &exchange{server: s, record: record, request: request, writer: writer}
	if err := exchange.run(); err != nil {
		s.finishRecord(record, OutcomeServerError)
		writeJSONError(writer, http.StatusInternalServerError, nil, "mock server handler failed", "", "MOCK_HANDLER_FAILED")
	}
}

// readJSONBody 读完请求体并解析。空请求体是合法的，交出「没有请求体」。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:299-304
func readJSONBody(reader io.Reader) (any, bool, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// providerError 是失败响应的正文形状，照 OpenAI 的错误信封写。
type providerError struct {
	Error providerErrorBody `json:"error"`
}

// providerErrorBody 是那个信封里的内容。
type providerErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code"`
}

// writeJSONError 写一条 OpenAI 形状的失败响应。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:348-365
//
// extra 里放随行为而定的头（限流的 Retry-After、可选的 x-request-id）。
func writeJSONError(writer http.ResponseWriter, status int, extra map[string]string, message, kind, code string) {
	header := writer.Header()
	header.Set("Content-Type", "application/json")
	for name, value := range extra {
		header.Set(name, value)
	}
	writer.WriteHeader(status)
	// 写失败只可能是客户端已经走了，那件事由守望者记账，这里没有别的补救可做。
	_ = json.NewEncoder(writer).Encode(providerError{
		Error: providerErrorBody{Message: message, Type: kind, Code: code},
	})
}
