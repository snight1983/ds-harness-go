// 本文件验模型问询那一层：一个端点的清单怎么被读成候选、哪些行会被跳过，
// 以及这次问询到底问了哪个地址、带了哪把密钥。
//
// 这里的服务器是现造的 [net/http/httptest]，不是 llm/mockserver——后者只答
// /chat/completions，而这一层要考的是 GET /models。

package openaicompat

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds-harness-go/llm"
)

// probeRecord 记下模拟端点收到的那次问询。
type probeRecord struct {
	path   string
	query  string
	method string
	header http.Header
}

// listingServer 起一台按给定状态码和正文作答的模拟端点，并记下收到的那次请求。
func listingServer(t *testing.T, status int, body string, seen *probeRecord) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if seen != nil {
			seen.path = request.URL.Path
			seen.query = request.URL.RawQuery
			seen.method = request.Method
			seen.header = request.Header.Clone()
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestListingURLKeepsPathSegments 验端点是当前缀拼上去的，不是当基地址解析的。
//
// 一份写着 https://gateway.example/openai/v1 的部署，那几段路径是它路由的一部分；
// 按 URL 解析会把它们丢掉，于是问询打到了一个根本不存在的地址上。
func TestListingURLKeepsPathSegments(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{baseURL: "https://gateway.example/v1", want: "https://gateway.example/v1/models"},
		{baseURL: "https://gateway.example/openai/v1", want: "https://gateway.example/openai/v1/models"},
		// 结尾的斜杠去掉，不然会拼出一个双斜杠的路径。
		{baseURL: "https://gateway.example/v1/", want: "https://gateway.example/v1/models"},
		{baseURL: "https://gateway.example/v1///", want: "https://gateway.example/v1/models"},
	}
	for _, testCase := range cases {
		if got := listingURL(testCase.baseURL); got != testCase.want {
			t.Errorf("%q 该拼成 %q，得到 %q", testCase.baseURL, testCase.want, got)
		}
	}
}

// TestDiscoveryCapacityTakesOnlyUsableIntegers 逐条验一个容量数在什么情况下还当真。
//
// 这些数是端点自己报的，形状全凭它高兴：判不掉的话，一个 1e300 会被转成一个
// 未定义的 int，然后当成某条模型的上下文容量落进界面。
func TestDiscoveryCapacityTakesOnlyUsableIntegers(t *testing.T) {
	cases := []struct {
		name       string
		candidates []any
		want       int
	}{
		{name: "正整数", candidates: []any{float64(4096)}, want: 4096},
		{name: "不是整数", candidates: []any{4096.5}, want: 0},
		{name: "零", candidates: []any{float64(0)}, want: 0},
		{name: "负数", candidates: []any{float64(-1)}, want: 0},
		{name: "大得离谱", candidates: []any{float64(maxDiscoveryCapacity) + 1}, want: 0},
		{name: "刚好在上界", candidates: []any{float64(maxDiscoveryCapacity)}, want: math.MaxInt32},
		// 端点把数报成了字符串：类型不对和没这个字段是同一件事——跳过。
		{name: "报成字符串", candidates: []any{"4096"}, want: 0},
		{name: "什么都没有", candidates: nil, want: 0},
		{name: "跳过不能用的取下一个", candidates: []any{"4096", nil, float64(8192)}, want: 8192},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := discoveryCapacity(testCase.candidates...); got != testCase.want {
				t.Errorf("该是 %d，得到 %d", testCase.want, got)
			}
		})
	}
}

// TestLabelTakesFirstNonEmptyString 验名字取候选里第一个非空字符串。
func TestLabelTakesFirstNonEmptyString(t *testing.T) {
	if got := label(nil, "", float64(3), "Zeta", "Alpha"); got != "Zeta" {
		t.Errorf("该取第一个非空字符串，得到 %q", got)
	}
	if got := label(nil, "", float64(3)); got != "" {
		t.Errorf("一个能用的都没有时该是空串，得到 %q", got)
	}
}

// TestReadListingSkipsUnusableRows 验一行坏数据不会把整份清单赖掉。
//
// 跳过而不是失败：一个端点混进一行读不懂的东西，不该让它剩下那些能用的模型
// 也一起发现不了。
func TestReadListingSkipsUnusableRows(t *testing.T) {
	models, err := readListing([]byte(`{"data":[
		"这一行压根不是个对象",
		{"id":""},
		{"id":123},
		{"id":"zeta","display_name":"Zeta","context_length":8192,"max_tokens":1024},
		{"id":"alpha","name":"Alpha","display_name":"忽略我","context_window":4096,
			"context_length":1,"max_output_tokens":512,"max_tokens":1}
	]}`))
	if err != nil {
		t.Fatalf("这份清单本该读得出来：%v", err)
	}
	want := []llm.DiscoveredModel{
		{ID: "zeta", Name: "Zeta", ContextWindow: 8192, MaxTokens: 1024},
		// name 压过 display_name，context_window 压过 context_length，
		// max_output_tokens 压过 max_tokens。
		{ID: "alpha", Name: "Alpha", ContextWindow: 4096, MaxTokens: 512},
	}
	if len(models) != len(want) {
		t.Fatalf("条数不对：得到 %d 条 %+v", len(models), models)
	}
	for index := range want {
		if models[index] != want[index] {
			t.Errorf("第 %d 条不对：得到 %+v，要 %+v", index, models[index], want[index])
		}
	}
}

// TestReadListingRequiresADataArray 验没有 data 数组的回答被当成读不懂。
//
// 走到这一步的多半是端点说的根本不是这套协议（比如一份 HTML 错误页），而这时候
// 唯一有用的话是「手工录入」，不是把一份空清单交给界面。
func TestReadListingRequiresADataArray(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `不是 JSON`, `[]`, `{"models":[{"id":"m"}]}`} {
		if _, err := readListing([]byte(body)); err == nil {
			t.Errorf("%q 本该被拒", body)
		} else if code := failureCode(t, err); code != "DISCOVERY_FAILED" {
			t.Errorf("%q 的失败码不对：%q", body, code)
		}
	}
	// 一份 data 是空数组的回答是合法的：这个端点就是一个模型都没公告。
	models, err := readListing([]byte(`{"data":[]}`))
	if err != nil {
		t.Fatalf("空清单本该读得出来：%v", err)
	}
	if len(models) != 0 {
		t.Errorf("空清单该解出 0 条，得到 %d 条", len(models))
	}
}

// boundedResponse 造一份能交给 [readBoundedBody] 的回答。
func boundedResponse(contentLength int64, body string) *http.Response {
	return &http.Response{ContentLength: contentLength, Body: io.NopCloser(strings.NewReader(body))}
}

// TestReadBoundedBodyHoldsTheLineBothWays 验字节上限两头都把得住。
//
// 声称的长度是给老实服务端用的——一个字节都不用传就被挡回去；真正把住界限的是
// 读到的总量，因为一个少报（或者干脆分块传输）的服务端事先什么都不说。
func TestReadBoundedBodyHoldsTheLineBothWays(t *testing.T) {
	body, err := readBoundedBody(boundedResponse(2, "ok"), "https://gateway.example/v1/models")
	if err != nil || string(body) != "ok" {
		t.Fatalf("一份正常的回答本该读得下来：%q %v", body, err)
	}

	// 声称自己超了：连正文都不该读。
	_, err = readBoundedBody(
		boundedResponse(maxDiscoveryResponseBytes+1, "short"), "https://gateway.example/v1/models")
	if err == nil {
		t.Fatal("声称超了上限的回答本该被拒")
	}
	if code := failureCode(t, err); code != "DISCOVERY_FAILED" {
		t.Errorf("失败码不对：%q", code)
	}

	// 什么都没声称（分块传输就是 -1），但真读出来超了。
	_, err = readBoundedBody(
		boundedResponse(-1, strings.Repeat("a", maxDiscoveryResponseBytes+1)),
		"https://gateway.example/v1/models")
	if err == nil {
		t.Fatal("真读出来超了上限的回答本该被拒")
	}
	// 刚好压在线上的读得下来：上限是「超了才拒」而不是「到了就拒」。
	body, err = readBoundedBody(
		boundedResponse(-1, strings.Repeat("a", maxDiscoveryResponseBytes)),
		"https://gateway.example/v1/models")
	if err != nil || len(body) != maxDiscoveryResponseBytes {
		t.Fatalf("刚好压线的回答本该读得下来：%d %v", len(body), err)
	}
}

// TestUsableProbeKeyRejectsBeforeBuildingTheHeader 验密钥在拼进请求头之前就被检查。
//
// 不在这里拒的话，下面那次请求会因为头里带不了这个字符而失败，然后被归成
// 「连不上这个端点」——把一次本地的、说得清原因的毛病栽到网络头上。
func TestUsableProbeKeyRejectsBeforeBuildingTheHeader(t *testing.T) {
	key, err := usableProbeKey("  sk-probe  ")
	if err != nil || key != "sk-probe" {
		t.Fatalf("一把正常的密钥该被收下并去掉首尾空白：%q %v", key, err)
	}

	blank, err := usableProbeKey("   ")
	if blank != "" || err == nil {
		t.Fatal("空白密钥本该被拒")
	}
	if code := failureCode(t, err); code != llm.InvalidCredentialCode {
		t.Errorf("空白密钥的失败码不对：%q", code)
	}
	if !strings.Contains(err.Error(), "blank") {
		t.Errorf("空白密钥的诊断没说它是空的：%v", err)
	}

	_, err = usableProbeKey("sk-\n-probe")
	if err == nil {
		t.Fatal("带非法字符的密钥本该被拒")
	}
	if code := failureCode(t, err); code != llm.InvalidCredentialCode {
		t.Errorf("非法字符的失败码不对：%q", code)
	}
	// 两种毛病共用一个码，所以诊断必须分得开——它们的修法完全不同。
	if !strings.Contains(err.Error(), "no HTTP header can carry") {
		t.Errorf("非法字符的诊断没说清是字符的问题：%v", err)
	}
}

// TestDiscoveryEndpointFallsBackToTheConfiguredRoute 验草稿没写端点时用那条路由自己的。
//
// 界面编的是一份**脱敏**的档案，端点和密钥都不在它手上；不兜这一下的话，
// 「重新拉一遍这条已经配好的路由的模型」这个正当场景就问不了了。
func TestDiscoveryEndpointFallsBackToTheConfiguredRoute(t *testing.T) {
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))
	snap := adapter.current()

	// 草稿写了端点：以草稿为准，因为那正是用户此刻要试的那一个。
	endpoint, err := discoveryEndpoint(snap, llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: "https://draft.example/v1"})
	if err != nil || endpoint != "https://draft.example/v1" {
		t.Fatalf("草稿里的端点该赢：%q %v", endpoint, err)
	}

	endpoint, err = discoveryEndpoint(snap, llm.ModelDiscoveryRequest{Provider: "acme"})
	if err != nil || endpoint != "https://gateway.example/v1" {
		t.Fatalf("该兜到这条路由自己的端点：%q %v", endpoint, err)
	}

	// 既没写端点、这条路由也没配：这个装置没有内置目录，所以问不了。
	_, err = discoveryEndpoint(snap, llm.ModelDiscoveryRequest{Provider: "unknown"})
	if err == nil {
		t.Fatal("一条哪儿都问不到的问询本该被拒")
	}
	if code := failureCode(t, err); code != "DISCOVERY_FAILED" {
		t.Errorf("失败码不对：%q", code)
	}
	if !strings.Contains(err.Error(), `"unknown"`) {
		t.Errorf("诊断没有点名这条路由：%v", err)
	}
}

// TestDiscoverModelsRejectsProtocolsItCannotRead 验点了别的协议的草稿在发请求之前
// 就被退回去。
//
// 拿这套协议去问一个说别的话的端点，换回来的多半是一个 401，然后被报成「凭据不对」
// ——把用户往一条查不出东西的路上引。
func TestDiscoverModelsRejectsProtocolsItCannotRead(t *testing.T) {
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))
	_, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", API: "anthropic-messages", BaseURL: "https://gateway.example/v1"})
	if code := failureCode(t, err); code != "DISCOVERY_UNSUPPORTED" {
		t.Errorf("失败码不对：%q", code)
	}
	// 没点协议的草稿照问：网关说这套话的可能性压倒性地高。
	if _, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", API: listableAPI, BaseURL: "https://gateway.example/v1"}); err != nil {
		if code := failureCode(t, err); code == "DISCOVERY_UNSUPPORTED" {
			t.Error("点名了本适配器这套协议的草稿被当成读不懂了")
		}
	}
}

// TestDiscoverModelsProbesTheEndpoint 验一次问询真的问出去了，而且问的是对的地址、
// 带着该带的头。
func TestDiscoverModelsProbesTheEndpoint(t *testing.T) {
	var seen probeRecord
	server := listingServer(t, http.StatusOK, `{"data":[{"id":"m","context_window":4096}]}`, &seen)
	identity := llm.AppIdentity{Product: "harness-test", Version: "1.2.3", URL: "https://example.invalid"}
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())),
		func(options *AdapterOptions) { options.Identity = identity })

	models, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: server.URL + "/openai/v1", APIKey: "sk-probe"})
	if err != nil {
		t.Fatalf("这次问询本该成功：%v", err)
	}
	if len(models) != 1 || models[0].ID != "m" || models[0].ContextWindow != 4096 {
		t.Fatalf("交回来的候选不对：%+v", models)
	}

	if seen.method != http.MethodGet {
		t.Errorf("问询该是一次 GET，得到 %q", seen.method)
	}
	// 端点里那几段路径要留住，而且不该被塞进查询串。
	if seen.path != "/openai/v1/models" || seen.query != "" {
		t.Errorf("问的地址不对：%q?%q", seen.path, seen.query)
	}
	if got := seen.header.Get("Authorization"); got != "Bearer sk-probe" {
		t.Errorf("表单里那把密钥没带上：%q", got)
	}
	if got := seen.header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept 头不对：%q", got)
	}
	// 归属头照发：这一次问询和别的请求一样要自报家门。
	if got := seen.header.Get("User-Agent"); got != llm.UserAgent(identity) {
		t.Errorf("归属头没带上：%q", got)
	}
}

// TestDiscoverModelsPrefersTheFormKeyOverTheStoredOne 验表单里打进来的密钥赢过存量。
//
// 那把正是用户此刻要试的一把，也可能就是用来换掉那把正在失败的存量密钥的。
func TestDiscoverModelsPrefersTheFormKeyOverTheStoredOne(t *testing.T) {
	var seen probeRecord
	server := listingServer(t, http.StatusOK, `{"data":[{"id":"m"}]}`, &seen)
	profile := minimalProfile()
	profile.APIKeyEnv = "ACME_KEY"
	resolved := 0
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)), func(options *AdapterOptions) {
		options.ResolveAPIKey = func(context.Context, string, ResolvedProviderProfile) (string, error) {
			resolved++
			return "sk-stored", nil
		}
	})

	if _, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: server.URL + "/v1", APIKey: "sk-form"}); err != nil {
		t.Fatalf("这次问询本该成功：%v", err)
	}
	if got := seen.header.Get("Authorization"); got != "Bearer sk-form" {
		t.Errorf("表单里那把密钥没赢：%q", got)
	}
	// 表单给了密钥的问询根本不该去解存量凭据。
	if resolved != 0 {
		t.Errorf("表单已经给了密钥，却还去解了 %d 次存量凭据", resolved)
	}

	if _, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: server.URL + "/v1"}); err != nil {
		t.Fatalf("这次问询本该成功：%v", err)
	}
	if got := seen.header.Get("Authorization"); got != "Bearer sk-stored" {
		t.Errorf("表单没给密钥时该用存量那把：%q", got)
	}
}

// TestDiscoverModelsProbesUnauthenticatedWhenThereIsNoKey 验一把密钥都没有时不带
// Authorization 头。
//
// 本地推理服务器正是这么问的；硬塞一个空的 Bearer 反而会被一部分服务端当成凭据不对。
func TestDiscoverModelsProbesUnauthenticatedWhenThereIsNoKey(t *testing.T) {
	var seen probeRecord
	server := listingServer(t, http.StatusOK, `{"data":[{"id":"m"}]}`, &seen)

	// 这条路由声明自己不认证（没写 apiKeyEnv），所以连解凭据这一步都不该走。
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())),
		func(options *AdapterOptions) {
			options.ResolveAPIKey = func(context.Context, string, ResolvedProviderProfile) (string, error) {
				t.Error("一条不认证的路由不该去解凭据")
				return "", nil
			}
		})
	if _, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: server.URL + "/v1"}); err != nil {
		t.Fatalf("这次问询本该成功：%v", err)
	}
	if _, carried := seen.header["Authorization"]; carried {
		t.Errorf("不认证的问询带上了凭据头：%q", seen.header.Get("Authorization"))
	}

	// 压根没配过的那条路由同理：没有存量密钥可问。
	if _, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "brand-new", BaseURL: server.URL + "/v1"}); err != nil {
		t.Fatalf("一条还没配过的路由本该问得了：%v", err)
	}
	if _, carried := seen.header["Authorization"]; carried {
		t.Error("一条还没配过的路由带上了凭据头")
	}
}

// TestDiscoverModelsSurfacesCredentialFailure 验解存量凭据失败时原样报出去。
//
// 那条错误自己就说得清是哪把密钥、该去哪儿改，比包成一句「问不到这个端点」有用得多。
func TestDiscoverModelsSurfacesCredentialFailure(t *testing.T) {
	profile := minimalProfile()
	profile.APIKeyEnv = "ACME_KEY"
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)), func(options *AdapterOptions) {
		options.ResolveAPIKey = func(context.Context, string, ResolvedProviderProfile) (string, error) {
			return "", llm.NewError("no credential for ACME_KEY", llm.InvalidCredentialCode, nil)
		}
	})
	_, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: "https://gateway.example/v1"})
	if code := failureCode(t, err); code != llm.InvalidCredentialCode {
		t.Errorf("失败码不对：%q", code)
	}
}

// TestDiscoverModelsReportsUpstreamStatus 验端点答了个错误码时把它报出来，
// 并且只在像凭据问题的那两个码上提凭据。
//
// 在一个 500 上让用户去翻密钥，是把排错往反方向推。
func TestDiscoverModelsReportsUpstreamStatus(t *testing.T) {
	cases := []struct {
		status int
		hints  bool
	}{
		{status: http.StatusUnauthorized, hints: true},
		{status: http.StatusForbidden, hints: true},
		{status: http.StatusInternalServerError, hints: false},
		{status: http.StatusNotFound, hints: false},
	}
	for _, testCase := range cases {
		server := listingServer(t, testCase.status, `{"error":"nope"}`, nil)
		adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))
		_, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
			Provider: "acme", BaseURL: server.URL + "/v1"})
		if code := failureCode(t, err); code != "DISCOVERY_FAILED" {
			t.Errorf("状态码 %d 的失败码不对：%q", testCase.status, code)
		}
		if hinted := strings.Contains(err.Error(), "check the API key"); hinted != testCase.hints {
			t.Errorf("状态码 %d 提不提凭据判错了：%v", testCase.status, err)
		}
		// 上游那个状态码本身得出现在诊断里，不然没法判是谁拒的。
		if !strings.Contains(err.Error(), http.StatusText(testCase.status)) &&
			!strings.Contains(err.Error(), "answered") {
			t.Errorf("诊断里没有上游那次回答：%v", err)
		}
	}
}

// TestDiscoverModelsReportsUnreachableEndpoint 验连不上时归成问询失败而不是崩掉。
func TestDiscoverModelsReportsUnreachableEndpoint(t *testing.T) {
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))
	// 127.0.0.1:1 上不会有人应答，而且它是本机，不会真发出去一个包。
	_, err := adapter.DiscoverModels(t.Context(), llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: "http://127.0.0.1:1/v1"})
	if code := failureCode(t, err); code != "DISCOVERY_FAILED" {
		t.Errorf("失败码不对：%q", code)
	}
}

// TestDiscoverModelsReportsCallerAbort 验调用方取消时交出的是取消，不是「问不到」。
//
// 一次用户自己按掉的问询报成端点有毛病，会让人跑去查一个根本没坏的端点。
func TestDiscoverModelsReportsCallerAbort(t *testing.T) {
	server := listingServer(t, http.StatusOK, `{"data":[{"id":"m"}]}`, nil)
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := adapter.DiscoverModels(ctx, llm.ModelDiscoveryRequest{
		Provider: "acme", BaseURL: server.URL + "/v1"})
	if code := failureCode(t, err); code != "ABORTED" {
		t.Errorf("失败码不对：%q", code)
	}
}
