// 本文件验适配器那一层：一次解算冻下来的快照怎么被复用、路由描述读的是哪一份
// 配置，以及一次流式调用在真的 HTTP 上走完全程之后交出什么。
//
// 这一层起真服务器（llm/mockserver）而不是打桩，因为要考的恰恰是**连出去**这一段
// ——端点怎么拼、请求头带了什么、SDK 会不会自己偷偷重试、超时归因分不分得清
// ——而这些东西一旦打桩就全都看不见了。

package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"strings"
	"testing"
	"time"

	"ds-harness-go/attachment"
	"ds-harness-go/llm"
	"ds-harness-go/llm/mockserver"
)

// profilesOf 落定一份只有一条路由的配置，失败就让用例当场停下。
func profilesOf(t *testing.T, provider string, profile ProviderProfile) Profiles {
	t.Helper()
	profiles, err := ResolveProfiles(map[string]ProviderProfile{provider: profile})
	if err != nil {
		t.Fatalf("这条路由本该服务得了：%v", err)
	}
	return profiles
}

// fixed 把一份路由表包成 [AdapterOptions].Profiles 那个钩子。
//
// 交出的一直是同一个值，这正是 [Adapter.current] 那次按身份记忆化要的前提。
func fixed(profiles Profiles) func() Profiles {
	return func() Profiles { return profiles }
}

// newAdapter 造一个跑在给定路由表上的适配器，可选地改几个钩子。
func newAdapter(t *testing.T, source func() Profiles, mutate ...func(*AdapterOptions)) *Adapter {
	t.Helper()
	options := AdapterOptions{
		Profiles: source,
		ResolveAPIKey: func(context.Context, string, ResolvedProviderProfile) (string, error) {
			return "", nil
		},
	}
	for _, apply := range mutate {
		apply(&options)
	}
	adapter, err := NewAdapter(options)
	if err != nil {
		t.Fatalf("造不出适配器：%v", err)
	}
	return adapter
}

// startMock 起一台按剧本演的模拟服务器，用例结束时关掉。
func startMock(t *testing.T, options mockserver.Options) *mockserver.Server {
	t.Helper()
	server, err := mockserver.Start(options)
	if err != nil {
		t.Fatalf("起不了模拟服务器：%v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

// routeTo 造一条指向某台模拟服务器的路由。
//
// 端点带上 /v1：模拟服务器根路径和 /v1 都收，而真实网关几乎都在 /v1 上，
// 用它同时验了 [listingURL] 之外那条「端点当前缀拼」的约定没被 SDK 改掉。
func routeTo(server *mockserver.Server) ProviderProfile {
	return ProviderProfile{
		BaseURL:             server.BaseURL() + "/v1",
		StreamIdleTimeoutMs: 5000,
		Models:              []ModelProfile{{ID: "m", ContextWindow: 1000}},
	}
}

// request 造一次最小的流式请求。
func request(provider string) llm.GenerateOptions {
	return llm.GenerateOptions{
		Provider: provider,
		Model:    "m",
		Messages: []llm.Message{
			llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "hi"}}, llm.UserSource{}),
		},
	}
}

// drain 把一次流式调用读完，交出块和终结它的那条错误。
func drain(sequence iter.Seq2[llm.StreamChunk, error]) ([]llm.StreamChunk, error) {
	var chunks []llm.StreamChunk
	for chunk, err := range sequence {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// failureCode 取出一条错误里的稳定失败码。
func failureCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("这里本该有一条错误")
	}
	var carrier *llm.Error
	if !errors.As(err, &carrier) {
		t.Fatalf("这本该是一条 llm 失败：%v", err)
	}
	return carrier.Failure.Code
}

// streamText 把一串块里的正文拼回去。
func streamText(chunks []llm.StreamChunk) string {
	var text strings.Builder
	for _, chunk := range chunks {
		if delta, isText := chunk.(llm.TextDeltaChunk); isText {
			text.WriteString(delta.Text)
		}
	}
	return text.String()
}

// TestNewAdapterRejectsMissingHooks 验少了必填钩子在造的时候就被拒。
//
// 不在这里拒的话，第一次 nil 解引用发生在某个会话发请求的时候，而那时候的堆栈
// 说的是「一次请求失败了」，不是「装配少写了一个字段」。
func TestNewAdapterRejectsMissingHooks(t *testing.T) {
	if _, err := NewAdapter(AdapterOptions{}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("少了 Profiles 该被拒：%v", err)
	}
	_, err := NewAdapter(AdapterOptions{Profiles: fixed(Profiles{})})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("少了 ResolveAPIKey 该被拒：%v", err)
	}
}

// TestAdapterSnapshotFollowsProfileIdentity 验快照按路由表的身份记忆化。
//
// 每重建一次快照就多一份连接池，所以「配置没变」必须认得出来；而配置真的变了时
// 又必须整份换新，否则新端点会被老客户端发出去。
func TestAdapterSnapshotFollowsProfileIdentity(t *testing.T) {
	first := profilesOf(t, "acme", minimalProfile())
	current := first
	adapter := newAdapter(t, func() Profiles { return current })

	snap := adapter.current()
	if again := adapter.current(); again != snap {
		t.Error("路由表没变却重建了一份快照")
	}
	current = profilesOf(t, "acme", minimalProfile())
	if swapped := adapter.current(); swapped == snap {
		t.Error("路由表换了一份，快照却还是老的")
	}
}

// TestAdapterDescribesRoutes 验那几个只读描述方法读的是当下这份配置。
func TestAdapterDescribesRoutes(t *testing.T) {
	profile := minimalProfile()
	profile.DisplayName = "Acme Gateway"
	profile.Models = []ModelProfile{{ID: "zeta"}, {ID: "alpha"}}
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)))

	if info := adapter.ProviderInfo("acme"); info.Name != "Acme Gateway" || info.ID != "acme" {
		t.Errorf("路由描述不对：%+v", info)
	}
	// 不归自己的路由仍然要答得出来，只是没有标签可贴。
	if info := adapter.ProviderInfo("nobody"); info.Name != "nobody" {
		t.Errorf("不认得的路由该回落到路由键：%+v", info)
	}
	if _, owned := adapter.ProviderRetryPolicy("nobody"); owned {
		t.Error("不认得的路由不该有重试策略")
	}
	if policy, owned := adapter.ProviderRetryPolicy("acme"); !owned || policy.Mode == "" {
		t.Errorf("这条路由该有一份解算过的策略：%+v", policy)
	}

	models, err := adapter.ListModels(t.Context(), "acme")
	if err != nil {
		t.Fatalf("列模型失败：%v", err)
	}
	if len(models) != 2 || models[0].ID != "zeta" || models[1].ID != "alpha" {
		t.Errorf("模型清单的次序被重排了：%+v", models)
	}
	if _, err := adapter.ListModels(t.Context(), "nobody"); failureCode(t, err) != "NO_ADAPTER" {
		t.Errorf("不归自己的路由该报 NO_ADAPTER：%v", err)
	}
	if _, err := adapter.ResolveModel(t.Context(), "acme", "nope"); failureCode(t, err) != "UNKNOWN_MODEL" {
		t.Errorf("没配过的模型该报 UNKNOWN_MODEL：%v", err)
	}
	resolved, err := adapter.ResolveModel(t.Context(), "acme", "zeta")
	if err != nil || resolved.ID != "zeta" {
		t.Errorf("解算模型不对：%+v %v", resolved, err)
	}
}

// TestRequestHeadersAttributionWins 验部署方写的头盖不掉归属头。
//
// 比较按小写走：HTTP 的字段名不分大小写，一个写成 User-Agent 的部署头照样是在改
// user-agent，而一条路由不该有办法让发出去的请求不再自报家门。
func TestRequestHeadersAttributionWins(t *testing.T) {
	identity := llm.AppIdentity{Product: "p", Version: "1", URL: "https://example"}
	headers := requestHeaders(map[string]string{
		"User-Agent": "hijacked",
		"X-Tenant":   "acme",
	}, identity)
	if headers["user-agent"] != llm.UserAgent(identity) {
		t.Errorf("归属头没赢：%v", headers)
	}
	if _, kept := headers["User-Agent"]; kept {
		t.Errorf("换了个大小写写的归属头该被丢掉：%v", headers)
	}
	if headers["X-Tenant"] != "acme" {
		t.Errorf("别的部署头该原样留着：%v", headers)
	}
}

// TestStreamAssemblesASuccessfulResponse 验一次成功的调用在真 HTTP 上走完全程。
//
// 顺带验了两件只在真线路上看得见的事：请求确实落在 /v1/chat/completions 上，
// 而且 include_usage 真的点了（不点的话最后那条带用量的分块根本不会来）。
func TestStreamAssemblesASuccessfulResponse(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText: "hello there",
		ChunkSize:   4,
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	chunks, err := drain(sequence)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	if text := streamText(chunks); text != "hello there" {
		t.Errorf("拼出来的正文不对：%q", text)
	}
	if start, isStart := chunks[0].(llm.BlockStartChunk); !isStart || start.BlockType != llm.BlockText {
		t.Errorf("第一块该是正文块的开始：%#v", chunks[0])
	}
	if finish, isFinish := chunks[len(chunks)-1].(llm.FinishChunk); !isFinish ||
		finish.Reason != (llm.StopFinish{}) {
		t.Errorf("最后一块该是正常收尾：%#v", chunks[len(chunks)-1])
	}
	usage, isUsage := chunks[len(chunks)-2].(llm.UsageChunk)
	if !isUsage || usage.Usage.InputTokens == 0 {
		t.Errorf("倒数第二块该是一份非零的用量：%#v", chunks[len(chunks)-2])
	}

	records := server.Requests()
	if len(records) != 1 {
		t.Fatalf("一次调用该只发一次请求，发了 %d 次", len(records))
	}
	if records[0].Path != "/v1/chat/completions" {
		t.Errorf("端点没有按前缀拼：%q", records[0].Path)
	}
	body, _ := records[0].Body.(map[string]any)
	options, _ := body["stream_options"].(map[string]any)
	if options["include_usage"] != true {
		t.Errorf("请求没点 include_usage：%v", body["stream_options"])
	}
}

// TestStreamAssemblesReasoning 验推理文本在真线路上被读了出来。
//
// 这条用例守的是 [blockAssembly.applyDelta] 里那个额外字段：推理走的是
// reasoning_content，它不在 openai-go 的类型里，读错一步整段推理就无声消失，
// 而正文照样是对的——没有这一条，那种退化在测试里看不出来。
func TestStreamAssemblesReasoning(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence:      []mockserver.Behavior{mockserver.BehaviorReasoningSuccess},
		ReasoningText: "thinking hard",
		SuccessText:   "answer",
		ChunkSize:     5,
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	chunks, err := drain(sequence)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	var reasoning string
	for _, chunk := range chunks {
		if end, isEnd := chunk.(llm.BlockEndChunk); isEnd {
			if block, isReasoning := end.Block.(llm.ReasoningBlock); isReasoning {
				reasoning = block.Text
			}
		}
	}
	if reasoning != "thinking hard" {
		t.Errorf("推理文本没拼出来：%q", reasoning)
	}
	if text := streamText(chunks); text != "answer" {
		t.Errorf("正文不对：%q", text)
	}
}

// TestStreamAssemblesToolCalls 验分两帧发来的工具参数被拼了回去。
func TestStreamAssemblesToolCalls(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence:      []mockserver.Behavior{mockserver.BehaviorToolCallSuccess},
		ToolName:      "read",
		ToolArguments: `{"path":"a.txt"}`,
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	chunks, err := drain(sequence)
	if err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	var call llm.ToolCallBlock
	for _, chunk := range chunks {
		if end, isEnd := chunk.(llm.BlockEndChunk); isEnd {
			if block, isCall := end.Block.(llm.ToolCallBlock); isCall {
				call = block
			}
		}
	}
	if call.Name != "read" || call.Arguments != `{"path":"a.txt"}` || call.ID == "" {
		t.Errorf("工具调用没拼回去：%+v", call)
	}
	if finish, isFinish := chunks[len(chunks)-1].(llm.FinishChunk); !isFinish ||
		finish.Reason != (llm.ToolCallsFinish{}) {
		t.Errorf("该以「要调工具」收尾：%#v", chunks[len(chunks)-1])
	}
}

// TestStreamClassifiesUpstreamFailures 验建连阶段的失败按状态码归了类。
//
// 顺带钉住那个「SDK 不许自己重试」的开关：每条用例都断言只发了一次请求。看得见的
// 重试归 agent 恢复层所有，SDK 再悄悄重试一遍的话，上层看到的一次失败背后其实是
// 好几次真实请求，退避的节奏和账单都对不上了。
func TestStreamClassifiesUpstreamFailures(t *testing.T) {
	cases := []struct {
		name     string
		behavior mockserver.Behavior
		want     string
	}{
		{name: "凭据不对", behavior: mockserver.BehaviorAuthError, want: "AUTH"},
		{name: "请求太密", behavior: mockserver.BehaviorRateLimit, want: "RATE_LIMIT"},
		{name: "额度用光", behavior: mockserver.BehaviorQuotaExceeded, want: llm.QuotaExceededCode},
		{name: "上下文溢出", behavior: mockserver.BehaviorContextOverflow, want: llm.ContextWindowExceededCode},
		{name: "上游炸了", behavior: mockserver.BehaviorServerError, want: "SERVER"},
		{name: "请求不合法", behavior: mockserver.BehaviorInvalidRequest, want: "INVALID_REQUEST"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{testCase.behavior}})
			adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

			// 建连阶段的失败当场交出来，不是从序列里交出来——这正是
			// [llm.Adapter].Stream 那道接缝说的分工。
			_, err := adapter.Stream(t.Context(), request("acme"))
			if got := failureCode(t, err); got != testCase.want {
				t.Errorf("失败码不对：得到 %q，要 %q", got, testCase.want)
			}
			if records := server.Requests(); len(records) != 1 {
				t.Errorf("一次调用该只发一次请求，发了 %d 次", len(records))
			}
		})
	}
}

// TestStreamReportsIdleTimeout 验上游发完头就哑了会被归成本层那条空闲超时。
//
// 归因要紧：这条超时和「调用方取消了」在重试上的处理完全相反，而两者在底下看到的
// 都是一句 context canceled。
func TestStreamReportsIdleTimeout(t *testing.T) {
	server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorStall}})
	profile := routeTo(server)
	profile.StreamIdleTimeoutMs = 150
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发本该成功——头是发过来了的：%v", err)
	}
	_, err = drain(sequence)
	if got := failureCode(t, err); got != "TIMEOUT" {
		t.Errorf("哑掉的上游该报 TIMEOUT，得到 %q：%v", got, err)
	}
	if !strings.Contains(err.Error(), "went silent") {
		t.Errorf("诊断该说清是上游哑了：%v", err)
	}
}

// TestStreamReportsCallerAbort 验调用方自己取消时报的是 ABORTED 而不是某条超时。
func TestStreamReportsCallerAbort(t *testing.T) {
	server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := adapter.Stream(ctx, request("acme"))
	if got := failureCode(t, err); got != "ABORTED" {
		t.Errorf("取消该报 ABORTED，得到 %q：%v", got, err)
	}
}

// TestStreamCarriesCredentialAndHeaders 验凭据和部署头真的发到了线路上。
//
// 凭据是**每次请求**解算的（引用语义要求解不出来就当场失败），所以它只能作为一次
// 请求的选项落下去，而不是烤进那个跟着快照走的客户端——这条用例证明那条路是通的。
func TestStreamCarriesCredentialAndHeaders(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess},
		APIKey:   "secret-token",
	})
	profile := routeTo(server)
	profile.Headers = map[string]string{"X-Tenant": "acme"}
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)),
		func(options *AdapterOptions) {
			options.ResolveAPIKey = func(context.Context, string, ResolvedProviderProfile) (string, error) {
				return "secret-token", nil
			}
		})

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	records := server.Requests()
	if len(records) != 1 {
		t.Fatalf("该只发一次请求，发了 %d 次", len(records))
	}
	if records[0].Header.Get("X-Tenant") != "acme" {
		t.Errorf("部署头没发出去：%v", records[0].Header)
	}
	if agent := records[0].Header.Get("User-Agent"); !strings.Contains(agent, "deepseek-harness") {
		t.Errorf("归属头没发出去：%q", agent)
	}
}

// TestStreamSurfacesCredentialFailure 验凭据钩子自己报的失败原样交出去。
//
// 一条写了 apiKeyEnv 却解不出来的路由必须响亮地失败，而不是退回成不认证：
// 悄悄不带身份发出去的请求，不是这份配置说的那件事。
func TestStreamSurfacesCredentialFailure(t *testing.T) {
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())),
		func(options *AdapterOptions) {
			options.ResolveAPIKey = func(context.Context, string, ResolvedProviderProfile) (string, error) {
				return "", llm.NewError("no credential", "MISSING_CREDENTIAL", nil)
			}
		})
	_, err := adapter.Stream(t.Context(), request("acme"))
	if got := failureCode(t, err); got != "MISSING_CREDENTIAL" {
		t.Errorf("凭据失败该原样交出来，得到 %q：%v", got, err)
	}
}

// TestStreamMapsRequestOptions 验那几个请求参数落到了线上该在的字段上。
func TestStreamMapsRequestOptions(t *testing.T) {
	server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})
	profile := routeTo(server)
	profile.Models[0].ReasoningEfforts = map[llm.ReasoningEffortID]string{ThinkingHigh: "high"}
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)))

	temperature := 0.25
	options := request("acme")
	options.Temperature = &temperature
	options.MaxTokens = 128
	options.Stop = []string{"STOP"}
	options.ReasoningEffort = ThinkingHigh

	sequence, err := adapter.Stream(t.Context(), options)
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	body, _ := server.Requests()[0].Body.(map[string]any)
	if body["temperature"] != 0.25 {
		t.Errorf("温度没落下来：%v", body["temperature"])
	}
	// 用 max_tokens 而不是 max_completion_tokens：网关和本地推理服务器普遍只认前者。
	if body["max_tokens"] != float64(128) {
		t.Errorf("输出上限没落在 max_tokens 上：%v", body)
	}
	if _, wrongField := body["max_completion_tokens"]; wrongField {
		t.Errorf("不该发 max_completion_tokens：%v", body)
	}
	stop, _ := body["stop"].([]any)
	if len(stop) != 1 || stop[0] != "STOP" {
		t.Errorf("停止串没落下来：%v", body["stop"])
	}
	if body["reasoning_effort"] != "high" {
		t.Errorf("推理档位没按线上拼法发出去：%v", body["reasoning_effort"])
	}
}

// TestStreamRejectsUnsupportedReasoningEffort 验点了一个模型不提供的档位当场被拒。
//
// 一份配错了的配置就该在请求时被点出来，而不是被悄悄改成别的档位发出去。
func TestStreamRejectsUnsupportedReasoningEffort(t *testing.T) {
	profile := minimalProfile()
	profile.Models[0].ReasoningEfforts = map[llm.ReasoningEffortID]string{ThinkingHigh: "high"}
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", profile)))

	options := request("acme")
	options.ReasoningEffort = ThinkingLow
	_, err := adapter.Stream(t.Context(), options)
	if got := failureCode(t, err); got != "UNSUPPORTED_REASONING_EFFORT" {
		t.Errorf("没提供的档位该被拒，得到 %q：%v", got, err)
	}
}

// TestStreamRejectsImagesItCannotSend 验带图的请求在两道门上各自被拒。
//
// 两道门说的是不同的事：模型收不收图是**能力**，附件服务在不在是**这次部署**
// 有没有能力把那张图交出去。两者都会让请求发不出去，但改法完全不同。
func TestStreamRejectsImagesItCannotSend(t *testing.T) {
	withImage := request("acme")
	withImage.Messages = []llm.Message{
		llm.NewUserMessage(llm.Content{llm.ImageBlock{}}, llm.UserSource{}),
	}

	textOnly := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))
	_, err := textOnly.Stream(t.Context(), withImage)
	if got := failureCode(t, err); got != "UNSUPPORTED_CONTENT" {
		t.Errorf("不收图的模型该拒掉带图的请求，得到 %q：%v", got, err)
	}

	profile := minimalProfile()
	profile.Models[0].Input = []llm.ModelModality{llm.ModalityText, llm.ModalityImage}
	seeing := newAdapter(t, fixed(profilesOf(t, "acme", profile)))
	_, err = seeing.Stream(t.Context(), withImage)
	if got := failureCode(t, err); got != "UNSUPPORTED_CONTENT" {
		t.Errorf("没有附件服务时该拒掉带图的请求，得到 %q：%v", got, err)
	}
	if !strings.Contains(err.Error(), "attachment service") {
		t.Errorf("第二道门的诊断该说清缺的是附件服务：%v", err)
	}

	// 挂上一个附件服务之后，拒不拒就由内容本身说了算了。
	present := newAdapter(t, fixed(profilesOf(t, "acme", profile)),
		func(options *AdapterOptions) {
			options.ResolveAttachments = func() attachment.Store { return nil }
		})
	_, err = present.Stream(t.Context(), withImage)
	if got := failureCode(t, err); got != "UNSUPPORTED_CONTENT" {
		t.Errorf("交出 nil 的附件钩子等同于没有，得到 %q：%v", got, err)
	}
}

// TestPrepareCallFreezesTheSnapshot 验「准备」和「派发」之间的一次配置改动落不到
// 已经准备好的那次调用上。
//
// 这正是 [llm.CallPreparer] 那道冻结存在的理由：一次回复读到一半时用户换了模型，
// 换的那一下不该把这一步的能力和另一代的端点凑到一起。用两台服务器来证明它——
// 只要请求落在第一台上，就说明冻住的是整份快照，不只是模型元数据。
func TestPrepareCallFreezesTheSnapshot(t *testing.T) {
	first := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})
	second := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})

	current := profilesOf(t, "acme", routeTo(first))
	adapter := newAdapter(t, func() Profiles { return current })

	prepared, err := adapter.PrepareCall(t.Context(), "acme", "m")
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	current = profilesOf(t, "acme", routeTo(second))

	sequence, err := prepared.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	if len(first.Requests()) != 1 {
		t.Error("准备好的那次调用没有落在它冻住的那个端点上")
	}
	if len(second.Requests()) != 0 {
		t.Error("换配置之后的端点抢走了一次已经准备好的调用")
	}
	if prepared.Model.ID != "m" {
		t.Errorf("准备结果里的模型不对：%+v", prepared.Model)
	}
}

// TestNewChatServiceIgnoresProcessEnvironment 验路由的端点不会被进程环境改写。
//
// openai-go 的 NewClient 会先铺一层从环境里读来的默认值，其中 OPENAI_BASE_URL
// 会换掉端点。这个适配器服务的是手工声明的路由——一条路由发到哪儿，只能由这份
// 配置说了算，让宿主机上一个碰巧存在的环境变量改写它，是这份配置根本没法解释的行为。
func TestNewChatServiceIgnoresProcessEnvironment(t *testing.T) {
	server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})
	t.Setenv("OPENAI_BASE_URL", "https://not-this-one.example/v1")
	t.Setenv("OPENAI_API_KEY", "env-key")

	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))
	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	records := server.Requests()
	if len(records) != 1 {
		t.Fatalf("请求跑到别的端点去了：只收到 %d 次", len(records))
	}
	// 环境里那把密钥也不该被捡起来：这条路由声明的是不认证。
	if token := records[0].Header.Get("Authorization"); token != "" {
		t.Errorf("不认证的路由带上了 Authorization：%q", token)
	}
}

// TestNewHTTPClientTakesTheRouteIdleTimeout 验那个客户端的响应头时限取自这条路由。
//
// 「请求写完了、响应头还没来」本来就是一次上游空闲，而看门狗只在**等下一个值**
// 的时候才计时，建连这一段它管不着——不设这条超时的话，一个收下连接却永远不回话
// 的服务端会把这次请求永远挂住。
func TestNewHTTPClientTakesTheRouteIdleTimeout(t *testing.T) {
	client := newHTTPClient(1234 * time.Millisecond)
	transport, cloned := client.Transport.(*http.Transport)
	if !cloned {
		t.Skip("这次跑的 http.DefaultTransport 被别人包过，这条超时按设计跳过")
	}
	if transport.ResponseHeaderTimeout != 1234*time.Millisecond {
		t.Errorf("响应头时限不对：%v", transport.ResponseHeaderTimeout)
	}
	if transport == http.DefaultTransport {
		t.Error("改的是全局那个 transport")
	}
}

// TestStreamStopsWhenCallerStops 验调用方提前不取了时那次调用就地收摊。
//
// 生产者在自己的协程里推块，消费者不取了之后它必须退得出来，否则每一次半途而废的
// 回复都会漏掉一个协程和一份还开着的响应体。
func TestStreamStopsWhenCallerStops(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText: "one two three four five",
		ChunkSize:   2,
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	count := 0
	for range sequence {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("提前跳出之后还在往外吐块：走了 %d 次", count)
	}
}

// TestStreamReportsBrokenStream 验流读到一半断了时那条错误从序列里交出来。
//
// 建连成了才轮到读，所以这条失败不可能从派发那一步交出去——这正是那道接缝把
// 「当场的失败」和「读到一半的失败」分成两个返回值的原因。
func TestStreamReportsBrokenStream(t *testing.T) {
	server := startMock(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorPartialDisconnect},
		PartialText: "half a sen",
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发本该成功——头是发过来了的：%v", err)
	}
	chunks, err := drain(sequence)
	if err == nil {
		t.Fatal("断掉的流本该报错")
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

// TestStreamRejectsUnknownRoute 验不归自己的路由和没配过的模型在派发前就被拒。
func TestStreamRejectsUnknownRoute(t *testing.T) {
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", minimalProfile())))

	if _, err := adapter.Stream(t.Context(), request("nobody")); failureCode(t, err) != "NO_ADAPTER" {
		t.Error("不归自己的路由该报 NO_ADAPTER")
	}
	options := request("acme")
	options.Model = "nope"
	if _, err := adapter.Stream(t.Context(), options); failureCode(t, err) != "UNKNOWN_MODEL" {
		t.Error("没配过的模型该报 UNKNOWN_MODEL")
	}
}

// TestStreamSendsSystemAndTools 验系统提示词和工具 schema 走到了线上。
func TestStreamSendsSystemAndTools(t *testing.T) {
	server := startMock(t, mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))))

	options := request("acme")
	options.System = "be brief"
	options.Tools = []llm.ToolSchema{{
		Name:        "read",
		Description: "read a file",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}}

	sequence, err := adapter.Stream(t.Context(), options)
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	body, _ := server.Requests()[0].Body.(map[string]any)
	messages, _ := body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("请求里一条消息都没有：%v", body)
	}
	head, _ := messages[0].(map[string]any)
	if head["role"] != "system" {
		t.Errorf("系统提示词该排在最前面：%v", messages[0])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("工具 schema 没发出去：%v", body["tools"])
	}
}
