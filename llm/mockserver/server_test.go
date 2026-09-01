// 本文件的作用：验线路上真正发生了什么——起真服务器、发真请求、读真字节。
//
// 这些用例都不 mock HTTP。本包存在的意义就是「线路上到底怎么断的」，用一个假的
// ResponseWriter 去验它等于把要考的东西换掉了。

package mockserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/llm/mockserver"
)

// startServer 起一台服务器并挂上收场。
func startServer(t *testing.T, options mockserver.Options) *mockserver.Server {
	t.Helper()
	server, err := mockserver.Start(options)
	if err != nil {
		t.Fatalf("起服务器失败：%v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

// newClient 造一个每次都用新连接的客户端。
//
// DisableKeepAlives 是必须的：复用连接时 [net/http] 会在对端重置后**悄悄重发**
// 一次请求，于是一条 connection_reset 的剧本会被消费两次，记录里凭空多出一条。
func newClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
}

// chatRequest 描述一次聊天补全请求；零值就是一次普通的合法请求。
type chatRequest struct {
	path string
	key  string
	body string
	// hasKey 区分「不带 Authorization」和「带一个空的」。
	hasKey bool
}

// postChat 发一次聊天补全请求。调用方负责关掉 Body。
func postChat(ctx context.Context, server *mockserver.Server, call chatRequest) (*http.Response, error) {
	path := call.path
	if path == "" {
		path = "/v1/chat/completions"
	}
	body := call.body
	if body == "" {
		body = `{"model":"mock","messages":[],"stream":true}`
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	// GetBody 一置空，这个请求就不可重放了。[http.NewRequestWithContext] 看见
	// *strings.Reader 会顺手填上它，而可重放正是上面那条重发规则的前提。
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/json")
	if call.hasKey {
		request.Header.Set("Authorization", "Bearer "+call.key)
	}
	return newClient().Do(request)
}

// readChat 发一次请求并把整个响应体读完。
func readChat(t *testing.T, server *mockserver.Server, call chatRequest) (*http.Response, string) {
	t.Helper()
	response, err := postChat(context.Background(), server, call)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return response, string(body)
}

// waitForOutcome 等某一次请求走到结局。
//
// 结局是处理器协程写的，而客户端这一侧拿到最后一个字节和服务器写完记录之间没有
// 顺序保证。直接断言会变成一个偶尔红的用例，那种用例比没有还糟。
func waitForOutcome(t *testing.T, server *mockserver.Server, attempt int) mockserver.RequestRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := server.Requests()
		if len(records) >= attempt && records[attempt-1].Outcome != "" {
			return records[attempt-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("等第 %d 次请求的结局超时，当前记录：%+v", attempt, records)
		}
		time.Sleep(time.Millisecond)
	}
}

// eventLog 攒下遥测，攒的时候上锁——事件是从各个处理器协程上来的。
type eventLog struct {
	mu     sync.Mutex
	events []mockserver.Event
}

func (log *eventLog) record(event mockserver.Event) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *eventLog) snapshot() []mockserver.Event {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]mockserver.Event(nil), log.events...)
}

func TestSuccessStreamsTheWholeTextAndRecordsTheRequest(t *testing.T) {
	t.Parallel()
	log := &eventLog{}
	server := startServer(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		APIKey:      "mock-key",
		SuccessText: "recovered",
		ChunkSize:   3,
		OnEvent:     log.record,
	})

	response, body := readChat(t, server, chatRequest{key: "mock-key", hasKey: true})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码是 %d，要的是 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Content-Type 是 %q，要的是 text/event-stream", got)
	}
	for _, marker := range []string{
		`"content":"rec"`, `"content":"ove"`, `"content":"red"`,
		`"finish_reason":"stop"`, "data: [DONE]",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("响应体里没有 %q：\n%s", marker, body)
		}
	}

	record := waitForOutcome(t, server, 1)
	if record.Attempt != 1 || record.Behavior != mockserver.BehaviorSuccess {
		t.Errorf("记录是 %+v，要的是第 1 次 success", record)
	}
	if record.Path != "/v1/chat/completions" {
		t.Errorf("路径是 %q", record.Path)
	}
	if record.ChunksSent != 5 {
		t.Errorf("发了 %d 条事件，要的是 5（3 段正文 + 收尾 + [DONE]）", record.ChunksSent)
	}
	if record.Outcome != mockserver.OutcomeCompleted {
		t.Errorf("结局是 %q", record.Outcome)
	}
	if !record.HasBody {
		t.Error("HasBody 是假的，但这次请求带了请求体")
	}
	decoded, isObject := record.Body.(map[string]any)
	if !isObject || decoded["model"] != "mock" {
		t.Errorf("请求体解析成了 %#v", record.Body)
	}

	events := log.snapshot()
	if len(events) != 2 {
		t.Fatalf("发了 %d 条遥测，要的是 2 条", len(events))
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("编码遥测失败：%v", err)
	}
	want := `[{"type":"request","attempt":1,"scriptBehavior":"success","behavior":"success",` +
		`"path":"/v1/chat/completions"},` +
		`{"type":"result","attempt":1,"scriptBehavior":"success","behavior":"success",` +
		`"outcome":"completed","chunksSent":5}]`
	if string(encoded) != want {
		t.Errorf("遥测的 JSON 形状是\n%s\n要的是\n%s", encoded, want)
	}
}

func TestRootPathWorksAndAPanickingObserverDoesNotChangeTheWire(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorEmpty},
		OnEvent:  func(mockserver.Event) { panic("观察者自己炸了") },
	})

	response, body := readChat(t, server, chatRequest{path: "/chat/completions"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码是 %d", response.StatusCode)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("响应体里没有 [DONE]：\n%s", body)
	}
	record := waitForOutcome(t, server, 1)
	if record.Path != "/chat/completions" || record.Outcome != mockserver.OutcomeCompleted {
		t.Errorf("记录是 %+v", record)
	}
}

func TestStreamsThatEndWithoutATerminalCompletion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		behavior mockserver.Behavior
		chunks   int
		marker   string
		hasDone  bool
	}{
		{mockserver.BehaviorEmptyBody, 0, "", false},
		{mockserver.BehaviorStreamEOF, 1, `"role":"assistant"`, false},
		{mockserver.BehaviorPartialEOF, 1, "discarded partial response", false},
		{mockserver.BehaviorMalformedJSON, 2, "data: {not-json", true},
		{mockserver.BehaviorMalformedEvent, 2, `"choices":[null]`, true},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.behavior), func(t *testing.T) {
			t.Parallel()
			server := startServer(t, mockserver.Options{
				Sequence:  []mockserver.Behavior{testCase.behavior},
				ChunkSize: 100,
			})
			response, body := readChat(t, server, chatRequest{})
			if response.StatusCode != http.StatusOK {
				t.Fatalf("状态码是 %d", response.StatusCode)
			}
			if testCase.marker != "" && !strings.Contains(body, testCase.marker) {
				t.Errorf("响应体里没有 %q：\n%s", testCase.marker, body)
			}
			if strings.Contains(body, "[DONE]") != testCase.hasDone {
				t.Errorf("[DONE] 的有无不对：\n%s", body)
			}
			record := waitForOutcome(t, server, 1)
			if record.ChunksSent != testCase.chunks {
				t.Errorf("发了 %d 条事件，要的是 %d", record.ChunksSent, testCase.chunks)
			}
			if record.Outcome != mockserver.OutcomeCompleted {
				t.Errorf("结局是 %q，要的是 completed——按剧本演完了就是演完了", record.Outcome)
			}
		})
	}
}

func TestTransportIsForciblyBroken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		behavior      mockserver.Behavior
		seesHeaders   bool
		chunksBefore  int
		expectedError string
	}{
		{mockserver.BehaviorConnectionReset, false, 0, "连一个响应头都不该收到"},
		{mockserver.BehaviorStreamDisconnect, true, 0, "响应体不该读得完"},
		{mockserver.BehaviorPartialDisconnect, true, 1, "响应体不该读得完"},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.behavior), func(t *testing.T) {
			t.Parallel()
			server := startServer(t, mockserver.Options{
				Sequence:           []mockserver.Behavior{testCase.behavior},
				DisconnectDelay:    20 * time.Millisecond,
				HasDisconnectDelay: true,
				PartialText:        "half",
			})

			response, err := postChat(context.Background(), server, chatRequest{})
			if !testCase.seesHeaders {
				if err == nil {
					response.Body.Close()
					t.Fatalf("%s", testCase.expectedError)
				}
			} else {
				if err != nil {
					t.Fatalf("响应头就没收到：%v", err)
				}
				defer response.Body.Close()
				if _, err := io.ReadAll(response.Body); err == nil {
					t.Fatalf("%s", testCase.expectedError)
				}
			}

			record := waitForOutcome(t, server, 1)
			if record.Outcome != mockserver.OutcomeReset {
				t.Errorf("结局是 %q，要的是 reset", record.Outcome)
			}
			if record.ChunksSent != testCase.chunksBefore {
				t.Errorf("断开前发了 %d 条事件，要的是 %d", record.ChunksSent, testCase.chunksBefore)
			}
		})
	}
}

func TestStallHoldsTheStreamAndCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorStall},
	})

	ctx, cancel := context.WithCancel(context.Background())
	response, err := postChat(ctx, server, chatRequest{})
	if err != nil {
		cancel()
		t.Fatalf("请求失败：%v", err)
	}
	if response.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("状态码是 %d", response.StatusCode)
	}
	if record := waitForOutcome(t, server, 1); record.Outcome != mockserver.OutcomeStalled {
		t.Errorf("结局是 %q，要的是 stalled", record.Outcome)
	}

	cancel()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Error("挂死的流不该读得完")
	}
	response.Body.Close()

	if err := server.Close(); err != nil {
		t.Errorf("第一次关服务器失败：%v", err)
	}
	if err := server.Close(); err != nil {
		t.Errorf("第二次关服务器失败：%v", err)
	}
}

func TestServerCloseWakesAStalledHandler(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorStall},
	})

	response, err := postChat(context.Background(), server, chatRequest{})
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer response.Body.Close()
	waitForOutcome(t, server, 1)

	// Close 要能在有限时间里回来。挂死的处理器等的就是 Close，Close 又等处理器
	// 退干净——这两句话如果没有一句先让步，这里会永远停住。
	done := make(chan error, 1)
	go func() { done <- server.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("关服务器失败：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("关服务器卡住了：挂死的处理器没有被叫醒")
	}
}

func TestClientLeavingMidFlightIsRecorded(t *testing.T) {
	t.Parallel()
	behaviors := []mockserver.Behavior{
		mockserver.BehaviorSlowSuccess,
		mockserver.BehaviorStreamDisconnect,
		mockserver.BehaviorPartialDisconnect,
	}
	for _, behavior := range behaviors {
		t.Run(string(behavior), func(t *testing.T) {
			t.Parallel()
			server := startServer(t, mockserver.Options{
				Sequence:           []mockserver.Behavior{behavior},
				ChunkDelay:         100 * time.Millisecond,
				HasChunkDelay:      true,
				DisconnectDelay:    100 * time.Millisecond,
				HasDisconnectDelay: true,
				ChunkSize:          1,
			})

			ctx, cancel := context.WithCancel(context.Background())
			response, err := postChat(ctx, server, chatRequest{})
			if err != nil {
				cancel()
				t.Fatalf("请求失败：%v", err)
			}
			cancel()
			_, _ = io.ReadAll(response.Body)
			response.Body.Close()

			if record := waitForOutcome(t, server, 1); record.Outcome != mockserver.OutcomeClientClosed {
				t.Errorf("结局是 %q，要的是 client_closed", record.Outcome)
			}
		})
	}
}

func TestIPv6ListenerProducesABracketedBaseURL(t *testing.T) {
	t.Parallel()
	server, err := mockserver.Start(mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess},
		Host:     "::1",
	})
	if err != nil {
		t.Skipf("这台机器起不了 IPv6 回环监听：%v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if !strings.HasPrefix(server.BaseURL(), "http://[::1]:") {
		t.Fatalf("根地址是 %q，方括号没加上", server.BaseURL())
	}
	response, _ := readChat(t, server, chatRequest{})
	if response.StatusCode != http.StatusOK {
		t.Errorf("状态码是 %d", response.StatusCode)
	}
}

func TestReasoningToolCallsMaxTokensSlowAndWrongContentType(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{
			mockserver.BehaviorReasoningSuccess,
			mockserver.BehaviorToolCallSuccess,
			mockserver.BehaviorMaxTokens,
			mockserver.BehaviorSlowSuccess,
			mockserver.BehaviorWrongContentType,
		},
		SuccessText:   "answer",
		ReasoningText: "think",
		ToolName:      "lookup",
		ToolArguments: `{"id":7}`,
		ChunkDelay:    time.Millisecond,
		HasChunkDelay: true,
		ChunkSize:     2,
	})

	bodies := make([]string, 0, 5)
	contentTypes := make([]string, 0, 5)
	for range 5 {
		response, body := readChat(t, server, chatRequest{})
		contentTypes = append(contentTypes, response.Header.Get("Content-Type"))
		bodies = append(bodies, body)
	}

	checks := []struct {
		index  int
		marker string
	}{
		{0, `"reasoning_content":"th"`},
		{1, `"name":"lookup"`},
		{1, `"arguments":"{\"id"`},
		{1, `"finish_reason":"tool_calls"`},
		{2, `"finish_reason":"length"`},
		{3, `"finish_reason":"stop"`},
	}
	for _, check := range checks {
		if !strings.Contains(bodies[check.index], check.marker) {
			t.Errorf("第 %d 个响应体里没有 %q：\n%s", check.index+1, check.marker, bodies[check.index])
		}
	}
	if contentTypes[4] != "application/json" {
		t.Errorf("wrong_content_type 的内容类型是 %q", contentTypes[4])
	}

	for attempt := 1; attempt <= 5; attempt++ {
		if record := waitForOutcome(t, server, attempt); record.Outcome != mockserver.OutcomeCompleted {
			t.Errorf("第 %d 次的结局是 %q", attempt, record.Outcome)
		}
	}
}

func TestStructuredHTTPErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		behavior mockserver.Behavior
		status   int
		marker   string
	}{
		{mockserver.BehaviorRateLimit, http.StatusTooManyRequests, "mock rate limit"},
		{mockserver.BehaviorServerError, http.StatusInternalServerError, "mock server error"},
		{mockserver.BehaviorServiceUnavailable, http.StatusServiceUnavailable, "mock service unavailable"},
		{mockserver.BehaviorAuthError, http.StatusUnauthorized, "mock authentication failed"},
		{mockserver.BehaviorInvalidRequest, http.StatusBadRequest, "mock invalid request"},
		{mockserver.BehaviorContextOverflow, http.StatusBadRequest, "context_length_exceeded"},
		{mockserver.BehaviorQuotaExceeded, http.StatusTooManyRequests, "insufficient_quota"},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.behavior), func(t *testing.T) {
			t.Parallel()
			server := startServer(t, mockserver.Options{
				Sequence:   []mockserver.Behavior{testCase.behavior},
				RetryAfter: 1001 * time.Millisecond,
				RequestID:  "mock-request-1",
			})
			response, body := readChat(t, server, chatRequest{})
			if response.StatusCode != testCase.status {
				t.Errorf("状态码是 %d，要的是 %d", response.StatusCode, testCase.status)
			}
			if !strings.Contains(body, testCase.marker) {
				t.Errorf("响应体里没有 %q：%s", testCase.marker, body)
			}
			if got := response.Header.Get("X-Request-Id"); got != "mock-request-1" {
				t.Errorf("x-request-id 是 %q", got)
			}
			retryAfter := response.Header.Get("Retry-After")
			if testCase.behavior == mockserver.BehaviorRateLimit {
				// 1001 毫秒向上取整是 2 秒：建议的重试间隔宁可长一点也不能短。
				if retryAfter != "2" {
					t.Errorf("Retry-After 是 %q，要的是 2", retryAfter)
				}
			} else if retryAfter != "" {
				t.Errorf("只有限流该带 Retry-After，这里带了 %q", retryAfter)
			}
			if record := waitForOutcome(t, server, 1); record.Outcome != mockserver.OutcomeCompleted {
				t.Errorf("结局是 %q", record.Outcome)
			}
		})
	}
}

func TestScriptExhaustionFailsLoudAndRepeatLastRepeats(t *testing.T) {
	t.Parallel()
	exhausted := startServer(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText: "once",
	})
	readChat(t, exhausted, chatRequest{})
	response, body := readChat(t, exhausted, chatRequest{})
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("剧本用完之后状态码是 %d，要的是 500", response.StatusCode)
	}
	if !strings.Contains(body, "mock script exhausted") {
		t.Errorf("响应体是 %s", body)
	}
	waitForOutcome(t, exhausted, 2)
	records := exhausted.Requests()
	if records[0].Behavior != mockserver.BehaviorSuccess ||
		records[1].Behavior != mockserver.BehaviorScriptExhausted {
		t.Errorf("行为序列是 %q / %q", records[0].Behavior, records[1].Behavior)
	}

	repeating := startServer(t, mockserver.Options{
		Sequence:   []mockserver.Behavior{mockserver.BehaviorEmpty},
		RepeatLast: true,
	})
	readChat(t, repeating, chatRequest{})
	readChat(t, repeating, chatRequest{})
	waitForOutcome(t, repeating, 2)
	for index, record := range repeating.Requests() {
		if record.Behavior != mockserver.BehaviorEmpty {
			t.Errorf("第 %d 次演的是 %q，要的是 empty", index+1, record.Behavior)
		}
	}
}

func TestRandomSelectionReplaysFromTheSeed(t *testing.T) {
	t.Parallel()
	options := mockserver.Options{
		Sequence:      []mockserver.Behavior{mockserver.BehaviorRandom},
		RepeatLast:    true,
		RandomSeed:    42,
		HasRandomSeed: true,
		RandomWeights: map[mockserver.Behavior]float64{
			mockserver.BehaviorSuccess: 1,
			mockserver.BehaviorEmpty:   1,
		},
		SuccessText: "random success",
	}
	first := startServer(t, options)
	second := startServer(t, options)

	const attempts = 12
	for range attempts {
		readChat(t, first, chatRequest{})
		readChat(t, second, chatRequest{})
	}
	waitForOutcome(t, first, attempts)
	waitForOutcome(t, second, attempts)

	if first.RandomSeed() != 42 || second.RandomSeed() != 42 {
		t.Fatalf("种子是 %d / %d", first.RandomSeed(), second.RandomSeed())
	}
	chosen := map[mockserver.Behavior]int{}
	firstRecords, secondRecords := first.Requests(), second.Requests()
	for index := range attempts {
		if firstRecords[index].Behavior != secondRecords[index].Behavior {
			t.Fatalf("第 %d 次两台选了 %q 和 %q，同种子必须选出同一串",
				index+1, firstRecords[index].Behavior, secondRecords[index].Behavior)
		}
		if firstRecords[index].ScriptBehavior != mockserver.BehaviorRandom {
			t.Errorf("第 %d 次的剧本条目是 %q，要的是 random", index+1, firstRecords[index].ScriptBehavior)
		}
		chosen[firstRecords[index].Behavior]++
	}
	if len(chosen) != 2 || chosen[mockserver.BehaviorSuccess] == 0 || chosen[mockserver.BehaviorEmpty] == 0 {
		t.Errorf("挑出来的行为是 %v，两项等权的权重表该两种都出现", chosen)
	}
}

func TestGatesRejectWithoutConsumingTheScript(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess},
		APIKey:   "expected",
	})

	method, err := newClient().Get(server.BaseURL() + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET 失败：%v", err)
	}
	method.Body.Close()
	if method.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET 的状态码是 %d，要的是 405", method.StatusCode)
	}
	if got := method.Header.Get("Allow"); got != "POST" {
		t.Errorf("Allow 是 %q", got)
	}

	route, _ := readChat(t, server, chatRequest{path: "/v1/other", key: "expected", hasKey: true})
	if route.StatusCode != http.StatusNotFound {
		t.Errorf("走错路径的状态码是 %d，要的是 404", route.StatusCode)
	}
	auth, _ := readChat(t, server, chatRequest{key: "wrong", hasKey: true})
	if auth.StatusCode != http.StatusUnauthorized {
		t.Errorf("令牌不对的状态码是 %d，要的是 401", auth.StatusCode)
	}
	badJSON, _ := readChat(t, server, chatRequest{key: "expected", hasKey: true, body: "{"})
	if badJSON.StatusCode != http.StatusBadRequest {
		t.Errorf("坏 JSON 的状态码是 %d，要的是 400", badJSON.StatusCode)
	}
	if records := server.Requests(); len(records) != 0 {
		t.Fatalf("四道门都不该消费剧本，却记下了 %d 条", len(records))
	}

	// 空请求体是合法的，剧本照样被消费。
	empty, err := http.NewRequest(http.MethodPost, server.BaseURL()+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("造请求失败：%v", err)
	}
	empty.Header.Set("Authorization", "Bearer expected")
	response, err := newClient().Do(empty)
	if err != nil {
		t.Fatalf("空请求体的请求失败：%v", err)
	}
	defer response.Body.Close()
	_, _ = io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Errorf("空请求体的状态码是 %d，要的是 200", response.StatusCode)
	}
	record := waitForOutcome(t, server, 1)
	if record.Behavior != mockserver.BehaviorSuccess {
		t.Errorf("演的是 %q", record.Behavior)
	}
	if record.HasBody || record.Body != nil {
		t.Errorf("没有请求体的那次记成了 HasBody=%v Body=%#v", record.HasBody, record.Body)
	}
}

func TestJSONNullBodyIsNotTheSameAsNoBody(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence:   []mockserver.Behavior{mockserver.BehaviorEmpty},
		RepeatLast: true,
	})
	readChat(t, server, chatRequest{body: "null"})
	record := waitForOutcome(t, server, 1)
	if !record.HasBody {
		t.Error("请求体是一个 JSON null，HasBody 必须为真——它和「没有请求体」是两回事")
	}
	if record.Body != nil {
		t.Errorf("Body 是 %#v，要的是 nil", record.Body)
	}
}

func TestRequestHeadersAreCaptured(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorEmpty},
	})
	readChat(t, server, chatRequest{key: "captured", hasKey: true})
	record := waitForOutcome(t, server, 1)
	if got := record.Header.Get("Authorization"); got != "Bearer captured" {
		t.Errorf("记下来的 Authorization 是 %q", got)
	}
}

func TestHandlerFailureIsReportedWhenTheConnectionCannotBeTakenOver(t *testing.T) {
	t.Parallel()
	// 用一个不实现 [http.Hijacker] 的 ResponseWriter 直接调处理器：重置类行为
	// 抢不到连接，处理器只能上报自己出了岔子。DSH 对应的那段兜底在 Node 下构造
	// 不出来，所以它被标成不计覆盖；Go 这边它是可达的，就该验。
	server := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorConnectionReset},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock"}`))
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("状态码是 %d，要的是 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "MOCK_HANDLER_FAILED") {
		t.Errorf("响应体是 %s", recorder.Body.String())
	}
	// 结局仍然是 reset：连接确实被判了死刑，只是执行不下去。先到的那个算数。
	record := waitForOutcome(t, server, 1)
	if record.Outcome != mockserver.OutcomeReset {
		t.Errorf("结局是 %q", record.Outcome)
	}
}

func TestBehaviorRosterIsClosed(t *testing.T) {
	t.Parallel()
	names := mockserver.Behaviors()
	if len(names) != 24 {
		t.Errorf("剧本里能点的行为有 %d 种，要的是 24 种", len(names))
	}
	names[0] = "被改掉了"
	if mockserver.Behaviors()[0] == "被改掉了" {
		t.Error("Behaviors 交出去的必须是副本，调用方改不动内部名册")
	}
	if mockserver.IsBehavior(mockserver.BehaviorScriptExhausted) {
		t.Error("script_exhausted 不能写进剧本")
	}
	if mockserver.IsConcreteBehavior(mockserver.BehaviorRandom) {
		t.Error("random 不是一种具体行为")
	}
	if !mockserver.IsConcreteBehavior(mockserver.BehaviorSuccess) {
		t.Error("success 是一种具体行为")
	}
	weights := mockserver.DefaultRandomWeights()
	weights[mockserver.BehaviorSuccess] = 0
	if mockserver.DefaultRandomWeights()[mockserver.BehaviorSuccess] == 0 {
		t.Error("DefaultRandomWeights 交出去的必须是副本")
	}
	for behavior := range mockserver.DefaultRandomWeights() {
		if !mockserver.IsConcreteBehavior(behavior) {
			t.Errorf("默认权重里的 %q 不是具体行为", behavior)
		}
	}
}

func TestEveryScriptableBehaviorIsPlayable(t *testing.T) {
	t.Parallel()
	// 这条用例钉住的是 run 里那个 switch 和 [Behaviors] 之间的对应关系：名册上有
	// 而 switch 里没有的行为，会走到那条 default 上报错，而不是安静地回一个空 200。
	for _, behavior := range mockserver.Behaviors() {
		if behavior == mockserver.BehaviorRandom ||
			behavior == mockserver.BehaviorConnectionReset ||
			behavior == mockserver.BehaviorStall {
			continue // 前者不是演法，后两者要断连接，各自有专门的用例。
		}
		t.Run(string(behavior), func(t *testing.T) {
			t.Parallel()
			server := startServer(t, mockserver.Options{
				Sequence:           []mockserver.Behavior{behavior},
				DisconnectDelay:    time.Millisecond,
				HasDisconnectDelay: true,
				ChunkDelay:         time.Millisecond,
				HasChunkDelay:      true,
			})
			response, err := postChat(context.Background(), server, chatRequest{})
			if err != nil {
				t.Fatalf("请求失败：%v", err)
			}
			_, _ = io.ReadAll(response.Body)
			response.Body.Close()

			record := waitForOutcome(t, server, 1)
			if record.Outcome == mockserver.OutcomeServerError {
				t.Fatalf("%q 没有演法", behavior)
			}
			// 第二道保险防的是「case 写了但什么都没演」：正常收尾、200、一个事件
			// 都没发，客户端看到的和漏写一个 case 没有区别。只有 empty_body 可以
			// 长这样，它的定义就是「只有头」。断连类行为不在此列——它们的 200 后面
			// 跟着一个重置，零事件正是要考的东西，所以这里按结局筛而不是按名字筛。
			if record.Outcome == mockserver.OutcomeCompleted &&
				response.StatusCode == http.StatusOK && record.ChunksSent == 0 &&
				behavior != mockserver.BehaviorEmptyBody {
				t.Errorf("%q 正常收尾却一个事件都没发，和漏写一个 case 没有区别", behavior)
			}
		})
	}
}

func TestStartRejectsAnUnusablePort(t *testing.T) {
	t.Parallel()
	first := startServer(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess},
	})
	_, err := mockserver.Start(mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess},
		Port:     first.Port(),
	})
	if err == nil {
		t.Fatal("端口已经被占了，Start 必须失败")
	}
	if !strings.Contains(err.Error(), "监听") {
		t.Errorf("错误信息是 %v，看不出是绑端口失败", err)
	}
}

func TestCLIHelpIsRequestedBeforeAnythingElse(t *testing.T) {
	t.Parallel()
	_, err := mockserver.ParseCLIArgs([]string{"--nonsense", "--help"})
	if !errors.Is(err, mockserver.ErrCLIHelp) {
		t.Errorf("错误是 %v，要的是 ErrCLIHelp——参数写错了的时候人要的正是用法说明", err)
	}
}
