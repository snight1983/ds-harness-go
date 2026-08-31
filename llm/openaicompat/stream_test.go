// 本文件验线上那串扁平增量怎么变成 harness 的分块协议：块的边界是推出来的、
// token 记账要自己减、终止原因和失败码各归各的。

package openaicompat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"ds-harness-go/llm"
)

// decodeUsage 按一段线上 JSON 造一份用量。
//
// 直接填结构体也行，但那样就绕过了 openai-go 自己的解码——而这一层要验的正是
// 「线上那几个字段落到哪里去了」，所以从字节开始。
func decodeUsage(t *testing.T, raw string) openai.CompletionUsage {
	t.Helper()
	var usage openai.CompletionUsage
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		t.Fatalf("解不开这份用量：%v", err)
	}
	return usage
}

// TestMapUsageSubtractsCacheFromPrompt 验缓存命中不被算两遍。
//
// prompt_tokens 是**含**缓存命中的总数（DeepSeek 就是这么报的），而
// [llm.TokenUsage] 要求三者互不重叠、加起来才是计费输入。不减的话，一段长会话的
// 输入统计会虚高到离谱，而预算和压缩触发都是照着它算的。
func TestMapUsageSubtractsCacheFromPrompt(t *testing.T) {
	usage := mapUsage(decodeUsage(t, `{
		"prompt_tokens": 1000,
		"completion_tokens": 120,
		"prompt_tokens_details": {"cached_tokens": 700, "cache_write_tokens": 100},
		"completion_tokens_details": {"reasoning_tokens": 40}
	}`))
	if usage.InputTokens != 200 {
		t.Errorf("输入该减掉缓存那两笔，得到 %d", usage.InputTokens)
	}
	if usage.CacheReadTokens != 700 || usage.CacheWriteTokens != 100 {
		t.Errorf("缓存记账不对：%d/%d", usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	// reasoning_tokens 是 completion_tokens 的一部分，不是另加的一笔。
	if usage.OutputTokens != 120 || usage.ReasoningTokens != 40 {
		t.Errorf("输出记账不对：%d/%d", usage.OutputTokens, usage.ReasoningTokens)
	}
	if total := usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens; total != 1000 {
		t.Errorf("三者加起来该等于 prompt_tokens，得到 %d", total)
	}
}

// TestMapUsageClampsAtZero 验缓存数报得比总数还大时输入不会变成负数。
//
// 负数会一路串进预算和压缩触发的算式里，那比丢掉一次记账严重得多。
func TestMapUsageClampsAtZero(t *testing.T) {
	usage := mapUsage(decodeUsage(t, `{
		"prompt_tokens": 100,
		"prompt_tokens_details": {"cached_tokens": 900}
	}`))
	if usage.InputTokens != 0 {
		t.Errorf("输入该钳在 0，得到 %d", usage.InputTokens)
	}
}

// TestMapFinishReason 逐条验线上终止原因翻成了什么。
func TestMapFinishReason(t *testing.T) {
	full := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	cases := []struct {
		name          string
		reason        string
		hasContent    bool
		usage         llm.TokenUsage
		contextWindow int
		want          llm.FinishReason
	}{
		{
			name: "正常收尾", reason: "stop", hasContent: true, usage: full,
			want: llm.StopFinish{},
		},
		{
			name: "答长了", reason: "length", hasContent: true, usage: full, contextWindow: 1000,
			want: llm.MaxTokensFinish{},
		},
		{
			name: "要调工具", reason: "tool_calls", hasContent: true,
			want: llm.ToolCallsFinish{},
		},
		{
			// 老的函数调用协议用的是这个词，指的是同一件事。
			name: "老协议的函数调用", reason: "function_call", hasContent: true,
			want: llm.ToolCallsFinish{},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := mapFinishReason(testCase.reason, "m", testCase.hasContent, testCase.usage, testCase.contextWindow)
			if got != testCase.want {
				t.Errorf("终止原因不对：%#v", got)
			}
		})
	}
}

// finishFailure 取出一个错误型终止原因里的失败事实。
func finishFailure(t *testing.T, reason llm.FinishReason) llm.Failure {
	t.Helper()
	failed, isError := reason.(llm.ErrorFinish)
	if !isError {
		t.Fatalf("这本该是一次失败的收尾，得到 %#v", reason)
	}
	return failed.Failure
}

// TestMapFinishReasonEmptyStopIsAFailure 验一次「答完了但什么都没产出」被当成失败。
//
// 那是提供方的一次退化完成，不是一条内容为空的成功助手消息——后者会被落库，
// 然后这个回合就带着一条空消息往下走了。
func TestMapFinishReasonEmptyStopIsAFailure(t *testing.T) {
	failure := finishFailure(t, mapFinishReason("stop", "m", false, llm.TokenUsage{}, 0))
	if failure.Code != llm.EmptyResponseCode {
		t.Errorf("失败码不对：%q", failure.Code)
	}
}

// TestMapFinishReasonLengthWithNoRoomIsOverflow 验「一个 token 都没输出就撞上 length，
// 而输入已经把窗口填满了」被认成上下文溢出。
//
// 这两者的修法完全相反——溢出要压缩历史，答长了要调高 max_tokens——所以必须分开。
func TestMapFinishReasonLengthWithNoRoomIsOverflow(t *testing.T) {
	usage := llm.TokenUsage{InputTokens: 900, CacheReadTokens: 100, OutputTokens: 0}
	failure := finishFailure(t, mapFinishReason("length", "m", false, usage, 1000))
	if failure.Code != llm.ContextWindowExceededCode {
		t.Errorf("失败码不对：%q", failure.Code)
	}

	// 只要还吐出了一个 token，那就是答长了，不是没地方写。
	if got := mapFinishReason("length", "m", true, llm.TokenUsage{
		InputTokens: 1000, OutputTokens: 1,
	}, 1000); got != (llm.MaxTokensFinish{}) {
		t.Errorf("产出过内容的 length 该是答长了，得到 %#v", got)
	}
	// 目录没给容量时不做这项判定：没有窗口就没有「填满」可言。
	if got := mapFinishReason("length", "m", false, usage, 0); got != (llm.MaxTokensFinish{}) {
		t.Errorf("没有窗口时该退回成答长了，得到 %#v", got)
	}
}

// TestMapFinishReasonContentFilter 验内容被拦下来有自己的码。
//
// 它不可重试——同样的输入会被同样地拦住——所以不能并进 SERVER 那一族。
func TestMapFinishReasonContentFilter(t *testing.T) {
	failure := finishFailure(t, mapFinishReason("content_filter", "m", true, llm.TokenUsage{}, 0))
	if failure.Code != "CONTENT_FILTER" {
		t.Errorf("失败码不对：%q", failure.Code)
	}
}

// TestMapFinishReasonUnknownIsTruncation 验认不得的（含空串）一律当成流被截断。
//
// 空串的意思是流在提供方给出 finish_reason 之前就断了，那是传输层的事，
// 所以它得落在一个可重试的码上。
func TestMapFinishReasonUnknownIsTruncation(t *testing.T) {
	for _, reason := range []string{"", "wat"} {
		failure := finishFailure(t, mapFinishReason(reason, "m", true, llm.TokenUsage{}, 0))
		if failure.Code != "TRANSPORT" {
			t.Errorf("终止原因 %q 的失败码不对：%q", reason, failure.Code)
		}
	}
}

// apiError 造一条带状态码的上游错误。
//
// Request 和 Response 都得填上：[openai.Error].Error() 是无条件解引用它们的
// （apierror.go:40-42），而 SDK 自己签发的每一条都带着它们。少填一个，考的就不再是
// 归类逻辑而是一次 nil 解引用。
func apiError(status int, message string) *openai.Error {
	request, err := http.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", nil)
	if err != nil {
		panic(err)
	}
	return &openai.Error{
		StatusCode: status,
		Message:    message,
		Request:    request,
		Response:   &http.Response{StatusCode: status, Header: http.Header{}},
	}
}

// TestClassifyErrorByStatus 验有状态码时以状态码为准。
//
// 这正是 DSH 那条 XXX 注释说的「哪天拿得到原始 Error 就改成按码分类」的版本：
// 文本匹配只在拿不到状态码时兜底。
func TestClassifyErrorByStatus(t *testing.T) {
	cases := []struct {
		status  int
		message string
		want    string
	}{
		{status: http.StatusUnauthorized, want: "AUTH"},
		{status: http.StatusForbidden, want: "AUTH"},
		{status: http.StatusTooManyRequests, want: "RATE_LIMIT"},
		{status: http.StatusRequestEntityTooLarge, want: "INVALID_REQUEST"},
		{status: http.StatusBadRequest, want: "INVALID_REQUEST"},
		{status: http.StatusUnprocessableEntity, want: "INVALID_REQUEST"},
		{status: http.StatusPaymentRequired, want: llm.QuotaExceededCode},
		{status: http.StatusRequestTimeout, want: "TIMEOUT"},
		{status: http.StatusGatewayTimeout, want: "TIMEOUT"},
		{status: http.StatusInternalServerError, want: "SERVER"},
		{status: http.StatusBadGateway, want: "SERVER"},
		{status: http.StatusNotFound, want: "INVALID_REQUEST"},
	}
	for _, testCase := range cases {
		failure := classifyError(apiError(testCase.status, "something went wrong"))
		if failure.Code != testCase.want {
			t.Errorf("状态码 %d 该归成 %q，得到 %q", testCase.status, testCase.want, failure.Code)
		}
		if failure.Status != testCase.status {
			t.Errorf("状态码没落进失败事实：%d", failure.Status)
		}
	}
}

// TestClassifyErrorSplitsQuotaFromRateLimit 验 429 上额度用光和请求太密分得开。
//
// 两者的修法完全相反：前者重试一万次也不会好，后者等一会就好。
func TestClassifyErrorSplitsQuotaFromRateLimit(t *testing.T) {
	quota := classifyError(apiError(http.StatusTooManyRequests, "You exceeded your current quota"))
	if quota.Code != llm.QuotaExceededCode {
		t.Errorf("说额度的 429 该归成额度用光，得到 %q", quota.Code)
	}
	dense := classifyError(apiError(http.StatusTooManyRequests, "Rate limit reached for requests"))
	if dense.Code != "RATE_LIMIT" {
		t.Errorf("说频率的 429 该归成请求太密，得到 %q", dense.Code)
	}
}

// TestClassifyErrorSplitsOverflowFromBadRequest 验 400 上上下文溢出分得出来。
func TestClassifyErrorSplitsOverflowFromBadRequest(t *testing.T) {
	overflow := classifyError(apiError(http.StatusBadRequest,
		"This model's maximum context length is 8192 tokens"))
	if overflow.Code != llm.ContextWindowExceededCode {
		t.Errorf("说超长的 400 该归成上下文溢出，得到 %q", overflow.Code)
	}
}

// TestClassifyErrorKeepsProviderRequestID 验提供方签发的请求标识被留下来。
//
// 对着它去问提供方的日志和账单，是唯一能把「我这边看到的失败」和「他们那边记的
// 那次请求」对上的东西。
func TestClassifyErrorKeepsProviderRequestID(t *testing.T) {
	err := apiError(http.StatusInternalServerError, "boom")
	err.Response.Header.Set("X-Request-Id", "req-42")
	if failure := classifyError(err); string(failure.RequestID) != "req-42" {
		t.Errorf("请求标识没留下来：%q", failure.RequestID)
	}
}

// TestClassifyErrorFallsBackToWording 验拿不到状态码时按文本归类。
//
// 拿不到状态码本身就说明这次失败发生在收到响应头之前或者响应读到一半，也就是
// 传输层；文本匹配只用来把它和「超时」分开，因为两者的退避不一样。
func TestClassifyErrorFallsBackToWording(t *testing.T) {
	cases := []struct {
		message string
		want    string
	}{
		{message: "connection reset by peer", want: "TRANSPORT"},
		{message: "unexpected EOF", want: "TRANSPORT"},
		{message: "dial tcp: no such host", want: "TRANSPORT"},
		{message: "context deadline exceeded", want: "TIMEOUT"},
		{message: "request timed out", want: "TIMEOUT"},
		{message: "maximum context length is 8192 tokens", want: llm.ContextWindowExceededCode},
		{message: "you exceeded your current quota", want: llm.QuotaExceededCode},
		// 谁也没认出来的失败重来一次多半还是同样地失败，所以它落在一个不可重试的码上。
		{message: "the model said no", want: "PROVIDER_ERROR"},
	}
	for _, testCase := range cases {
		failure := classifyError(errors.New(testCase.message))
		if failure.Code != testCase.want {
			t.Errorf("%q 该归成 %q，得到 %q", testCase.message, testCase.want, failure.Code)
		}
		if failure.Status != 0 {
			t.Errorf("%q 不该带状态码，得到 %d", testCase.message, failure.Status)
		}
	}
}

// sseStream 把一段 SSE 文本包成一条能读的流。
//
// 这是这一层唯一需要的「假」：要考的是解码之后那串增量怎么被拼成块，而拼装本身
// 一个字节的网络都不需要。
func sseStream(body string) *ssestream.Stream[openai.ChatCompletionChunk] {
	response := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	return ssestream.NewStream[openai.ChatCompletionChunk](ssestream.NewDecoder(response), nil)
}

// sseEvent 把一份分块 JSON 包成一条 SSE 事件。
//
// 负载里的换行先抹掉：SSE 的一条 data 只能占一行，而用例里那几段 JSON 为了读得下去
// 是折了行的。不抹的话折行处会被当成事件边界，解出来的是半截 JSON。
func sseEvent(payload string) string {
	return "data: " + strings.NewReplacer("\n", "", "\t", "").Replace(payload) + "\n\n"
}

// collectChunks 把一条流读完，交出块和那条终结它的错误。
func collectChunks(t *testing.T, body, model string, contextWindow int) ([]llm.StreamChunk, error) {
	t.Helper()
	var chunks []llm.StreamChunk
	var failure error
	for chunk, err := range streamChunks(sseStream(body), model, contextWindow) {
		if err != nil {
			failure = err
			break
		}
		chunks = append(chunks, chunk)
	}
	return chunks, failure
}

// TestStreamChunksAssemblesTextAndReasoning 验推理块和正文块的边界是从「字段变了」
// 推出来的。
//
// 这条协议没有块的边界事件——一条 delta 里只有 content、reasoning_content、
// tool_calls 三样——所以开始和结束只能这么推。
func TestStreamChunksAssemblesTextAndReasoning(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"reasoning_content":"thin"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"reasoning_content":"king"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"content":"he"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"content":"llo"},"finish_reason":"stop"}]}`) +
		sseEvent(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`) +
		"data: [DONE]\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	want := []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockReasoning},
		llm.ReasoningDeltaChunk{Index: 0, Text: "thin"},
		llm.ReasoningDeltaChunk{Index: 0, Text: "king"},
		llm.BlockEndChunk{Index: 0, Block: llm.ReasoningBlock{Text: "thinking"}},
		llm.BlockStartChunk{Index: 1, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: 1, Text: "he"},
		llm.TextDeltaChunk{Index: 1, Text: "llo"},
		llm.BlockEndChunk{Index: 1, Block: llm.TextBlock{Text: "hello"}},
		llm.UsageChunk{Usage: llm.TokenUsage{InputTokens: 10, OutputTokens: 2}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
	assertChunks(t, chunks, want)
}

// assertChunks 逐块比对，不一样就点出是第几块。
func assertChunks(t *testing.T, got, want []llm.StreamChunk) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("块数不对：得到 %d 块 %#v，要 %d 块", len(got), got, len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("第 %d 块不对：得到 %#v，要 %#v", index, got[index], want[index])
		}
	}
}

// TestStreamChunksAssemblesToolCalls 验工具调用块按线上 index 分开、按首次出现的
// 次序收尾。
func TestStreamChunksAssemblesToolCalls(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"content":"sure"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"tool_calls":[
			{"index":0,"id":"call-a","function":{"name":"read","arguments":"{\"p\":"}}]}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"tool_calls":[
			{"index":0,"function":{"arguments":"1}"}}]}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"tool_calls":[
			{"index":1,"id":"call-b","function":{"name":"write","arguments":"{}"}}]},
			"finish_reason":"tool_calls"}]}`) +
		"data: [DONE]\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	read, write := "read", "write"
	want := []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: 0, Text: "sure"},
		// 工具调用一开始，文本那一块就到头了。
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "sure"}},
		llm.BlockStartChunk{Index: 1, BlockType: llm.BlockToolCall},
		llm.ToolCallDeltaChunk{Index: 1, ID: "call-a", Name: &read, ArgumentsDelta: `{"p":`},
		llm.ToolCallDeltaChunk{Index: 1, ID: "call-a", Name: &read, ArgumentsDelta: "1}"},
		llm.BlockStartChunk{Index: 2, BlockType: llm.BlockToolCall},
		llm.ToolCallDeltaChunk{Index: 2, ID: "call-b", Name: &write, ArgumentsDelta: "{}"},
		llm.BlockEndChunk{Index: 1, Block: llm.ToolCallBlock{
			ID: "call-a", Name: "read", Arguments: `{"p":1}`}},
		llm.BlockEndChunk{Index: 2, Block: llm.ToolCallBlock{
			ID: "call-b", Name: "write", Arguments: "{}"}},
		llm.UsageChunk{},
		llm.FinishChunk{Reason: llm.ToolCallsFinish{}},
	}
	if len(chunks) != len(want) {
		t.Fatalf("块数不对：得到 %d 块 %#v", len(chunks), chunks)
	}
	for index := range want {
		// 工具名是指针，逐字段比不了整块，所以这两处单独比指向的值。
		gotCall, isCall := chunks[index].(llm.ToolCallDeltaChunk)
		wantCall, wantIsCall := want[index].(llm.ToolCallDeltaChunk)
		if isCall && wantIsCall {
			if gotCall.Index != wantCall.Index || gotCall.ID != wantCall.ID ||
				gotCall.ArgumentsDelta != wantCall.ArgumentsDelta {
				t.Errorf("第 %d 块的工具调用增量不对：%#v", index, gotCall)
			}
			if gotCall.Name == nil || *gotCall.Name != *wantCall.Name {
				t.Errorf("第 %d 块的工具名不对：%v", index, gotCall.Name)
			}
			continue
		}
		if chunks[index] != want[index] {
			t.Errorf("第 %d 块不对：得到 %#v，要 %#v", index, chunks[index], want[index])
		}
	}
}

// TestStreamChunksIgnoresOtherChoices 验只读第 0 路。
//
// 这个适配器从不设 n>1；多出来的路是提供方自己多给的，把它们的增量掺进同一串块里
// 会拼出一段谁也没说过的话。
func TestStreamChunksIgnoresOtherChoices(t *testing.T) {
	body := sseEvent(`{"choices":[
		{"index":0,"delta":{"content":"kept"}},
		{"index":1,"delta":{"content":"dropped"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`) +
		"data: [DONE]\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	for _, chunk := range chunks {
		if delta, isText := chunk.(llm.TextDeltaChunk); isText && delta.Text != "kept" {
			t.Errorf("第 0 路之外的增量被读进来了：%q", delta.Text)
		}
	}
	if end, isEnd := chunks[len(chunks)-3].(llm.BlockEndChunk); isEnd {
		if text, isText := end.Block.(llm.TextBlock); isText && text.Text != "kept" {
			t.Errorf("拼出来的正文掺进了别的路：%q", text.Text)
		}
	}
}

// TestStreamChunksKeepsLastUsage 验没带用量的分块不会把已经收到的用量清掉。
//
// 判的是「这条分块有没有 usage 字段」而不是「usage 是不是零」：一次零 token 的响应
// 和一条没带用量的分块不是同一件事。
func TestStreamChunksKeepsLastUsage(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":7,"completion_tokens":1}}`) +
		sseEvent(`{"choices":[]}`) +
		"data: [DONE]\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	usage, isUsage := chunks[len(chunks)-2].(llm.UsageChunk)
	if !isUsage {
		t.Fatalf("倒数第二块该是用量，得到 %#v", chunks[len(chunks)-2])
	}
	if usage.Usage.InputTokens != 7 || usage.Usage.OutputTokens != 1 {
		t.Errorf("用量被后面那条空分块清掉了：%+v", usage.Usage)
	}
}

// TestExtraFieldsAreNeverMarkedValid 钉住 openai-go 对额外字段的那个判定。
//
// [respjson.Field.Valid] 对**每一个**额外字段都是 false：额外字段没有可对照的类型，
// 收集它们的那一步一律标成 invalid，而 Valid 的判据是 status > invalid
// （respjson.go:47-63）。[blockAssembly.applyDelta] 读 reasoning_content 时因此不能
// 拿 Valid 当门禁——用了的话那整段就是死代码，推理文本会一声不响地整段消失。
//
// 这条用例存在的意义是：哪天 SDK 改了这个判定，这里会先失败，而不是等到某个部署
// 发现自己的推理文本多了一份。
func TestExtraFieldsAreNeverMarkedValid(t *testing.T) {
	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(
		`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking","content":"hi"}}]}`), &chunk); err != nil {
		t.Fatalf("解不开这条分块：%v", err)
	}
	field, present := chunk.Choices[0].Delta.JSON.ExtraFields[reasoningContentField]
	if !present {
		t.Fatal("额外字段压根没被收下来")
	}
	if field.Valid() {
		t.Error("SDK 现在把额外字段标成 valid 了——stream.go 里那条注释该跟着改")
	}
	if field.Raw() != `"thinking"` {
		t.Errorf("额外字段的原文不对：%q", field.Raw())
	}
}

// TestStreamChunksSkipsUnreadableReasoningField 验推理字段形状不对时跳过而不是读废
// 整条流。
//
// reasoning_content 是协议外的额外字段，各家的形状不保证一致。
func TestStreamChunksSkipsUnreadableReasoningField(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"reasoning_content":{"nested":true}}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`) +
		"data: [DONE]\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	for _, chunk := range chunks {
		if start, isStart := chunk.(llm.BlockStartChunk); isStart && start.BlockType == llm.BlockReasoning {
			t.Error("形状不对的推理字段不该开出一块推理")
		}
	}
	if len(chunks) == 0 {
		t.Fatal("整条流被一个额外字段读废了")
	}
}

// TestStreamChunksReportsBrokenStream 验流在半路断了时最后一项是那条错误。
//
// 已经吐出去的块是真的收到过的，不撤回。
func TestStreamChunksReportsBrokenStream(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"content":"par"}}]}`) +
		"data: {not json\n\n"

	chunks, err := collectChunks(t, body, "m", 0)
	if err == nil {
		t.Fatal("这条流本该报错")
	}
	if len(chunks) == 0 {
		t.Error("断流之前收到的块被撤回了")
	}
	for _, chunk := range chunks {
		if _, isFinish := chunk.(llm.FinishChunk); isFinish {
			t.Error("断掉的流不该给出终止块")
		}
	}
}

// TestStreamChunksStopsWhenCallerStops 验调用方提前不取了时生产侧就地停下。
func TestStreamChunksStopsWhenCallerStops(t *testing.T) {
	body := sseEvent(`{"choices":[{"index":0,"delta":{"content":"a"}}]}`) +
		sseEvent(`{"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":"stop"}]}`) +
		"data: [DONE]\n\n"

	count := 0
	for range streamChunks(sseStream(body), "m", 0) {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("提前跳出之后还在往外吐块：走了 %d 次", count)
	}
}
