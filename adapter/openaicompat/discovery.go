// 本文件的作用：回答配置界面上那个「去把这个端点有哪些模型拉回来」的动作——
// 按用户正在编的那份草稿去问一次端点的模型清单，把答案当候选元数据交回去。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts
//
// 这里问到的东西一样都不存：请求带的是一份还在编的草稿，回答是界面拿去让用户
// 采纳的候选。settings.yaml 仍然是唯一决定一条路由服务什么的东西。
//
// 新增: DSH 那条「路由在内置目录里就直接从目录作答、一次网络都不发」的短路
// （discovery.ts:201-211）整个不存在——那份目录连同 pi-ai 一起不移，见包注释。
// 这边**每一次**问询都走线上，而这正是这个动作存在的那种场景：网关和自建服务器
// 本来就没有任何内置目录描述得了。
//
// 新增: 也不会拿这条路由自己配置里的模型清单来作答。那份清单就是用户此刻正在编
// 的东西，把它原样交回去等于什么都没发现。

package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
)

// listableAPI 是这个适配器读得懂其模型清单的那套线上协议名。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:38-41
//
// 新增: DSH 那里是一个装着两条协议的集合（openai-completions 与 openai-responses），
// 因为它的适配器同时说这两套。这边只说一套，所以集合塌成一个常量：一份点了别的
// 协议的草稿要的根本不是这个适配器，交 DISCOVERY_UNSUPPORTED 让界面退回手工录入，
// 好过拿这套协议去问一个说别的话的端点、再把 401 报成「凭据不对」。
const listableAPI = "openai-completions"

// maxDiscoveryResponseBytes 是一次模型清单回答的字节上限，超了就拒。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:50
//
// 端点是用户随手打进表单的那个 URL，所以上限压在**真读到的**字节上，而不是
// 服务端自己声称的长度。截断了的清单解不出来，所以超了是拒而不是截。
const maxDiscoveryResponseBytes = 4 * 1024 * 1024

// maxDiscoveryCapacity 是一个清单条目报出来的容量数还当真的上界。
//
// 新增: DSH 只判「是整数且为正」——JS 的数字本来就是 float64，超出安全整数范围的
// 值在那边照样是个数。Go 这边要把它落进 int，而一个 1e300 的 float64 转 int 的
// 结果是未定义的。上界取 int32 的极限：没有任何真实模型的上下文容量或者输出上限
// 接近这个数，超过它的只能是端点报错了，当成「没披露」处理。
const maxDiscoveryCapacity = math.MaxInt32

// listingEntry 是一份 OpenAI 兼容 GET /models 回答里的一条。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:53-62
//
// 新增: 字段一律是 any 而不是确切类型。这是**故意**的：这些字段的形状由端点说了算，
// 一个把 context_length 报成字符串的网关不该让整条清单解不出来。用 any 之后
// 「类型不对」和「没这个字段」在 [label] / [discoveryCapacity] 那里是同一件事
// ——跳过——这正是 DSH 那两个辅助函数收 unknown 的原因。
type listingEntry struct {
	ID any `json:"id"`
	// 下面这些是网关常见的扩展字段，官方的清单里没有。
	Name            any `json:"name"`
	DisplayName     any `json:"display_name"`
	ContextWindow   any `json:"context_window"`
	ContextLength   any `json:"context_length"`
	MaxTokens       any `json:"max_tokens"`
	MaxOutputTokens any `json:"max_output_tokens"`
}

// discoveryCapacity 交出候选里第一个能用的正整数容量，一个都没有时交 0。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:65-70
//
// 0 表示「端点没披露」，和 [llm.DiscoveredModel] 那两个容量字段的口径一致。
func discoveryCapacity(candidates ...any) int {
	for _, candidate := range candidates {
		// encoding/json 把所有数字解成 float64，所以这一次断言就覆盖了整数和小数
		// 两种写法；不是整数的（比如 4096.5）在下一步被判掉。
		number, isNumber := candidate.(float64)
		if !isNumber {
			continue
		}
		if number != math.Trunc(number) || number <= 0 || number > maxDiscoveryCapacity {
			continue
		}
		return int(number)
	}
	return 0
}

// label 交出候选里第一个非空字符串，一个都没有时交空串。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:73-78
func label(candidates ...any) string {
	for _, candidate := range candidates {
		text, isText := candidate.(string)
		if isText && text != "" {
			return text
		}
	}
	return ""
}

// listingURL 把端点和清单路径接起来。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:86-88
//
// 端点当成前缀拼，不是当成一个基地址去解析：一份写着
// https://gateway.example/openai/v1 的部署路径要把它那几段留住，而按 URL 解析
// 会把它们丢掉。
func listingURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/models"
}

// discoveryFailed 报一次问询失败。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:98
func discoveryFailed(message string, cause error) *llm.Error {
	return llm.NewError(message, "DISCOVERY_FAILED", cause)
}

// readBoundedBody 读一份回答的正文，超了上限就拒。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:96-131
//
// 先看声称的长度，好让一个老实的服务端在一个字节都没传之前就被挡回去；真正把住
// 界限的是读到的总量，因为一个少报（或者干脆流式）的服务端事先什么都不说。
//
// 新增: DSH 手写了一段「一块块读、边读边累加、最后拼起来」的循环。Go 这边
// [io.LimitReader] 加 [io.ReadAll] 就是同一件事：多读一个字节，读回来的长度
// 超了上限就说明源还没完。
func readBoundedBody(response *http.Response, url string) ([]byte, error) {
	oversized := func() error {
		return discoveryFailed(fmt.Sprintf("%s answered with more than %d bytes", url, maxDiscoveryResponseBytes), nil)
	}
	// ContentLength 为 -1 表示服务端没说（分块传输就是这样），那就只能靠下面读。
	if response.ContentLength > maxDiscoveryResponseBytes {
		return nil, oversized()
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryResponseBytes+1))
	if err != nil {
		return nil, discoveryFailed(fmt.Sprintf("could not read the reply from %s", url), err)
	}
	if len(body) > maxDiscoveryResponseBytes {
		return nil, oversized()
	}
	return body, nil
}

// readListing 读一份 OpenAI 兼容的清单回答。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:138-162
//
// 没有可用 id 的条目跳过而不是让整次问询失败：一行坏数据不该把一个能用的端点
// 剩下那些模型也一起赖掉。
//
// 新增: 每一条先收成一段 [json.RawMessage] 再单独解，为的正是这一条——把一条
// 压根不是对象的行（清单里混进一个字符串）也归进「跳过」，而不是让整份回答解不出来。
func readListing(body []byte) ([]llm.DiscoveredModel, error) {
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return nil, discoveryFailed(
			`the endpoint's model listing has no "data" array; enter this provider's models by hand`, err)
	}
	models := make([]llm.DiscoveredModel, 0, len(envelope.Data))
	for _, raw := range envelope.Data {
		var entry listingEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		id := label(entry.ID)
		if id == "" {
			continue
		}
		models = append(models, llm.DiscoveredModel{
			ID:            id,
			Name:          label(entry.Name, entry.DisplayName),
			ContextWindow: discoveryCapacity(entry.ContextWindow, entry.ContextLength),
			MaxTokens:     discoveryCapacity(entry.MaxOutputTokens, entry.MaxTokens),
		})
	}
	return models, nil
}

// usableProbeKey 收下这一次问询要用的密钥，或者在拼头之前就拒掉它。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:172-181
//
// 不在这里拒的话，下面那次请求会因为头里带不了这个字符而失败，然后被归成
// 「连不上这个端点」——把一次本地的、说得清原因的毛病栽到网络头上。
func usableProbeKey(raw string) (string, error) {
	checked := llm.NormalizeAPIKey(raw)
	if checked.OK {
		return checked.Value, nil
	}
	if checked.Reason == llm.APIKeyEmpty {
		return "", llm.NewError(
			"this provider's API key is blank; enter it on the Models page, or clear it to probe unauthenticated",
			llm.InvalidCredentialCode, nil)
	}
	return "", llm.NewError(
		"this provider's API key contains characters no HTTP header can carry; paste the raw key only",
		llm.InvalidCredentialCode, nil)
}

// discoveryEndpoint 定下这次问询要问哪个端点。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:212-218
//
// 新增: 草稿没写端点、但点名的那条路由已经配好了的话，用那条路由自己的端点。
// DSH 走不到这一步——那种路由在上面就被内置目录短路掉了——而那份目录不在之后，
// 一条已经配好的路由如果只因为界面手上没有端点就问不了，等于把这个动作从
// 「重新拉一遍这条路由的模型」这个正当场景里撤掉。界面编的是一份**脱敏**的档案，
// 端点和密钥都不在它手上，所以这一条和下面那条「拿存下来的密钥」是同一件事。
func discoveryEndpoint(snap *snapshot, request llm.ModelDiscoveryRequest) (string, error) {
	if request.BaseURL != "" {
		return request.BaseURL, nil
	}
	if profile, owned := snap.profiles.Get(request.Provider); owned && profile.BaseURL != "" {
		return profile.BaseURL, nil
	}
	return "", discoveryFailed(fmt.Sprintf(
		"this build ships no catalog for provider %q, so its models can only come from its endpoint;"+
			" set a baseURL, or enter this provider's models by hand", request.Provider), nil)
}

// discoveryAPIKey 定下这次问询用哪把密钥，没有就不认证。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:240-241
//
// 表单里打进来的那把赢：它正是用户此刻要试的那一把，也可能就是用来换掉那把
// 正在失败的存量密钥的。存量那把只在这里问一次——过了协议检查之后——所以一次
// 早早就被拒掉的问询不会白白去解一次凭据，也不会为一把它根本用不上的密钥报诊断。
//
// 一把都没有的问询保持不认证，本地推理服务器正是这么问的。
func (a *Adapter) discoveryAPIKey(
	ctx context.Context,
	snap *snapshot,
	request llm.ModelDiscoveryRequest,
) (string, error) {
	if request.APIKey != "" {
		return usableProbeKey(request.APIKey)
	}
	profile, owned := snap.profiles.Get(request.Provider)
	// 这条路由没配、或者配了但声明自己不认证：两种情况下都没有存量密钥可问，
	// 而后者不带 Authorization 头正是它要的。
	if !owned || profile.APIKeyRef == "" {
		return "", nil
	}
	stored, err := a.options.ResolveAPIKey(ctx, request.Provider, profile)
	if err != nil {
		return "", err
	}
	if stored == "" {
		return "", nil
	}
	return usableProbeKey(stored)
}

// DiscoverModels 去问一个端点它公告了哪些模型，按端点给的次序交回来。
//
// 源: packages/llm/llm-pi-ai/src/discovery.ts:195-284
//
// 这个方法的签名就是 [llm.ModelDiscovery]，装配时直接登记进
// [llm.Runtime.RegisterModelDiscovery]。
//
// 新增: DSH 那个 storedApiKey 回调在这里不需要——取存量密钥要的两样东西
// （那条路由的配置、[AdapterOptions.ResolveAPIKey]）本来就都在这个适配器身上，
// 所以它是一个方法而不是一个还要别人往里灌闭包的自由函数。
//
// 新增: 取消走 ctx，不走 DSH 那个 request.signal——[llm.ModelDiscoveryRequest]
// 那边已经按本仓库的规矩把那个字段去掉了。
func (a *Adapter) DiscoverModels(
	ctx context.Context,
	request llm.ModelDiscoveryRequest,
) ([]llm.DiscoveredModel, error) {
	// 源: packages/llm/llm-pi-ai/src/discovery.ts:225-231
	//
	// 一份还没点协议的草稿按这套协议问：网关说这套话的可能性压倒性地高，而
	// 「填了这个字段才让问」等于把这个动作从它存在的那种场景里撤掉。代价是端点
	// 说别的话时那句诊断会指错方向（一个 Anthropic 网关答 401，读起来像凭据不对），
	// 而手工录入始终是退路。
	if request.API != "" && request.API != listableAPI {
		return nil, llm.NewError(fmt.Sprintf(
			"protocol %q has no model listing this build can read; enter this provider's models by hand",
			request.API), "DISCOVERY_UNSUPPORTED", nil)
	}

	snap := a.current()
	baseURL, err := discoveryEndpoint(snap, request)
	if err != nil {
		return nil, err
	}
	url := listingURL(baseURL)
	apiKey, err := a.discoveryAPIKey(ctx, snap, request)
	if err != nil {
		return nil, err
	}

	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// 走到这里说明这个端点根本拼不成一个 URL，那是这份草稿的毛病，不是网络的。
		return nil, discoveryFailed(fmt.Sprintf("%s is not a usable endpoint", url), err)
	}
	probe.Header.Set("Accept", "application/json")
	if apiKey != "" {
		probe.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// 归属头照发：这一次问询和别的请求一样要自报家门。
	for name, value := range llm.AttributionHeaders(a.options.Identity) {
		probe.Header.Set(name, value)
	}

	// 用 http.DefaultClient 而不是 [newHTTPClient]：那个客户端是给一条**已经配好**
	// 的路由造的（它那个 ResponseHeaderTimeout 来自路由的空闲上限），而这次问询的
	// 端点可能压根还没成为一条路由。这里也不需要那条超时——一次 GET 不是流，
	// 取消由 ctx 管。
	response, err := http.DefaultClient.Do(probe)
	if err != nil {
		// 源: packages/llm/llm-pi-ai/src/discovery.ts:253-258
		if ctx.Err() != nil {
			return nil, llm.NewError("model discovery aborted by caller", "ABORTED", err)
		}
		return nil, discoveryFailed("could not reach "+url, err)
	}
	defer func() { _ = response.Body.Close() }()

	// 源: packages/llm/llm-pi-ai/src/discovery.ts:259-264
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		hint := ""
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			hint = "; check the API key"
		}
		return nil, discoveryFailed(fmt.Sprintf("%s answered %d%s", url, response.StatusCode, hint), nil)
	}

	body, err := readBoundedBody(response, url)
	if err != nil {
		// 正文读到一半被取消，交出来的是取消那条原因；调用方拿到的码和请求发出去
		// 之前就被取消时是同一个。
		//
		// 源: packages/llm/llm-pi-ai/src/discovery.ts:268-276
		if ctx.Err() != nil {
			return nil, llm.NewError("model discovery aborted by caller", "ABORTED", err)
		}
		return nil, err
	}
	return readListing(body)
}
