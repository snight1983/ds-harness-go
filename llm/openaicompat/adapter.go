// 本文件的作用：适配器本身——一次解算冻下来的那份快照、每条路由那个 HTTP 客户端，
// 以及一次流式调用从选路、建连到收尾的全过程。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts

package openaicompat

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/util/timeout"
)

// StreamIdleTimeoutCode 是「提供方在流读到一半时哑了太久」这条超时的代号。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:347
const StreamIdleTimeoutCode = "LLM_STREAM_IDLE_TIMEOUT"

// RequestTimeoutCode 是「这次请求总共花的时间超了路由的上限」这条超时的代号。
//
// 新增: DSH 没有这个代号——它的 timeoutMs 是交给 pi-ai 的一个旋钮，超时由那个库
// 自己实现，从这边看不见也归不了因。这边自己发请求，就得自己把这条超时挂上；
// 挂成一条**可归因**的（[timeout.Deadline]）而不是一个裸 deadline，是因为
// 「我这层超时了」和「上游取消了」在收尾时的处理完全不同，见 [failureOf]。
const RequestTimeoutCode = "LLM_REQUEST_TIMEOUT"

// AdapterOptions 是造一个 [Adapter] 要交的那几个钩子。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:73-101
//
// 新增: DSH 那份还有一个必填的 auth（pi-ai 的 CredentialStore + AuthContext）。
// 那两样服务的是 pi-ai 自己的登录与刷新流程，而 OAuth 登录整条不做（见包注释），
// 所以这个字段连同它的类型一起消失。这条路上凭据只有一个来源：ResolveAPIKey。
type AdapterOptions struct {
	// Profiles 交出当下这份验过的路由表，每次操作问一遍。
	//
	// 返回的 [Profiles] 用 == 比身份：同一份配置解算出来的那一份要**原样**再交出来，
	// 否则每一次操作都会重建一遍快照，见 [Adapter.current]。
	Profiles func() Profiles
	// ResolveAPIKey 解算某条已经落定的路由的凭据，一次流式调用问一次、并在那次
	// 调用里冻住。
	//
	// 源: packages/llm/llm-pi-ai/src/adapter.ts:76-84
	//
	// 交出空串表示这条路由不认证——本地推理服务器正是这么跑的。一条**写了**
	// apiKeyEnv 却解不出来的路由应当由这个钩子自己报 MISSING_CREDENTIAL，
	// 而不是悄悄退回成不认证。
	ResolveAPIKey func(ctx context.Context, provider string, profile ResolvedProviderProfile) (string, error)
	// ResolveAttachments 在请求那一刻解算那个可选的持久附件服务；nil 或者返回 nil
	// 表示没有，于是历史里出现任何一张图都会被拒。
	ResolveAttachments func() attachment.Store
	// OnReplayDegrade 看着一条助手历史消息因为它那份重放状态本构建用不了而退回
	// 到中立转换。nil 表示不关心。
	OnReplayDegrade func(provider, model, reason string)
	// Identity 是每次请求都要带上的产品身份；零值表示 [llm.DefaultAppIdentity]。
	Identity llm.AppIdentity
}

// Adapter 是这条协议上的多提供方适配器。每次操作都重读一遍当下的路由表，
// 所以配置改了不必重启就能落到下一次请求上。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:216-221
type Adapter struct {
	options AdapterOptions

	// mutex 守着 cached。适配器会被多个会话并发调用，而快照的重建不是幂等的
	// ——每重建一次就多一份连接池。
	mutex  sync.Mutex
	cached *snapshot
}

var (
	_ llm.Adapter           = (*Adapter)(nil)
	_ llm.ProviderDescriber = (*Adapter)(nil)
	_ llm.RetryPolicyOwner  = (*Adapter)(nil)
	_ llm.ModelLister       = (*Adapter)(nil)
	_ llm.ModelResolver     = (*Adapter)(nil)
	_ llm.CallPreparer      = (*Adapter)(nil)
)

// NewAdapter 造一个适配器，拒掉少了必填钩子的选项。
//
// 新增: DSH 的构造函数只是把 options 存下来——TS 的类型系统保证了那两个必填钩子
// 一定在。Go 这边函数字段的零值是 nil，不在这里拒就会在第一次请求时 nil 解引用，
// 而那时候的堆栈指的是「某个会话发请求失败了」，而不是「装配少写了一个字段」。
func NewAdapter(options AdapterOptions) (*Adapter, error) {
	if options.Profiles == nil {
		return nil, fmt.Errorf("%w：AdapterOptions.Profiles 不能是 nil", ErrInvalidConfig)
	}
	if options.ResolveAPIKey == nil {
		return nil, fmt.Errorf("%w：AdapterOptions.ResolveAPIKey 不能是 nil", ErrInvalidConfig)
	}
	if options.Identity == (llm.AppIdentity{}) {
		options.Identity = llm.DefaultAppIdentity()
	}
	return &Adapter{options: options}, nil
}

// snapshot 是一次解算冻下来的那份视图：路由表，加上正好为这些路由造的那些客户端。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:64-70
//
// 一次操作在自己的第一个 IO 之前就把整份快照捕获住，之后全程只读它。配置变了会造
// 一份**新的**快照而不是改这一份，所以「回复到一半换了模型」只会在下一步生效，
// 绝不会在正在飞的这一步里生效——这正是接缝那道 [llm.CallPreparer] 冻结能一路
// 守到底的原因。
type snapshot struct {
	// profiles 是造出这份快照的那张路由表，也是它的身份。
	profiles Profiles
	// services 是按路由键索引的聊天补全服务；发布之后谁都不再改。
	services map[string]openai.ChatCompletionService
}

// current 交出当下这份路由表对应的快照，按身份记忆化。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:229-236
//
// 配置没变就认得出来（[Profiles] 用 == 比身份），变了就整份换新，而某次操作已经
// 捕获的那一份在它握着的时候一动不动。
func (a *Adapter) current() *snapshot {
	profiles := a.options.Profiles()
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.cached != nil && a.cached.profiles == profiles {
		return a.cached
	}
	services := make(map[string]openai.ChatCompletionService, profiles.Len())
	profiles.All(func(provider string, profile ResolvedProviderProfile) bool {
		services[provider] = newChatService(profile, a.options.Identity)
		return true
	})
	a.cached = &snapshot{profiles: profiles, services: services}
	return a.cached
}

// newChatService 为一条路由造那个聊天补全服务。
//
// 新增: 这里**不用** [openai.NewClient]，而是直接造它底下那个 ChatCompletionService。
// 理由是 NewClient 会先铺一层从进程环境里读来的默认值（client.go:68-96）：
// OPENAI_BASE_URL、OPENAI_API_KEY、OPENAI_ADMIN_KEY、OPENAI_ORG_ID、
// OPENAI_PROJECT_ID，以及 OPENAI_CUSTOM_HEADERS——最后那个能往每一次请求上塞
// **任意条数、任意名字**的请求头。前几个还能一个个盖掉，最后那个盖不掉：数量和
// 名字都不知道。而这个适配器服务的是手工声明的路由，一条路由发什么、带什么头，
// 只能由这份配置说了算；让宿主机上一个碰巧存在的环境变量改写每一条路由的请求，
// 是这份配置根本没法解释的行为。禁用环境默认那个开关（requestconfig.
// WithEnvironmentDefaultsDisabled）在 internal 包里，外面调不到，所以绕开
// NewClient 是唯一干净的做法。
//
// 代价是也拿不到 openai-go 自己那个默认 http.Client，所以下面自己造一个。
func newChatService(profile ResolvedProviderProfile, identity llm.AppIdentity) openai.ChatCompletionService {
	options := []option.RequestOption{
		option.WithHTTPClient(newHTTPClient(profile.StreamIdleTimeout)),
		// 端点只能来自这条路由。不铺任何默认 baseURL，是为了让一条配错了的路由
		// 连不上任何东西，而不是安安静静地连上 api.openai.com。
		option.WithBaseURL(profile.BaseURL),
		// 源: packages/llm/llm-pi-ai/src/adapter.ts:126-127
		//
		// 「看得见的重试」归 agent 恢复层所有；一次适配器调用就是一次 SDK 尝试。
		// SDK 自己再悄悄重试一遍的话，上层看到的一次失败背后其实是好几次真实请求，
		// 退避的节奏和账单都对不上了。
		option.WithMaxRetries(0),
	}
	headers := requestHeaders(profile.Headers, identity)
	// 按名字排序落下去。头名字在 HTTP 里不分大小写，而 option.WithHeader 走的是
	// http.Header.Set（会规范化名字），所以一份把 X-Foo 和 x-foo 都写了的配置里，
	// 谁最后落下谁赢。Go 的 map 无序，不排的话同一份配置每次启动的行为可能不一样。
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		options = append(options, option.WithHeader(name, headers[name]))
	}
	return openai.NewChatCompletionService(options...)
}

// newHTTPClient 造这条路由自己那个 HTTP 客户端。
//
// 新增: 整件事在 DSH 那边归 pi-ai 的 transport 管。这边自己发请求，所以自己造。
//
// ResponseHeaderTimeout 取这条路由的空闲上限：「请求已经写完了、响应头还没来」
// 本来就是一次**上游空闲**，而这条路由已经声明过自己愿意等多久上游不吭声了，
// 所以这里不需要再发明一个数字。不设它的话，一个收下连接却永远不回话的服务端
// 会把这次请求永远挂住——而看门狗只在**等下一个值**的时候才计时，建连这一段
// 它管不着（[timeout.Watchdog] 的定时器只在 [timeout.Receive] 里开着）。
//
// http.DefaultTransport 被别人包过（比如 otelhttp）时克隆不了，那就原样用它、
// 跳过这条超时：把追踪链路拆掉的代价比这条超时更大。这一支照抄 openai-go 自己
// 的取舍（default_http_client.go:24-31）。
func newHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	transport, cloneable := http.DefaultTransport.(*http.Transport)
	if !cloneable {
		return &http.Client{Transport: http.DefaultTransport}
	}
	clone := transport.Clone()
	clone.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Transport: clone}
}

// requestHeaders 把部署方写的那些头和归属头合起来，重名时归属头赢。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:202-209
//
// 归属头的名字是本装置拥有的（见 [llm.AttributionHeaders]），一条路由不该有办法
// 把它换掉——否则发出去的请求就不再自报家门了。比较按小写走，因为 HTTP 的字段名
// 不分大小写，一个写成 User-Agent 的部署头照样是在改 user-agent。
func requestHeaders(headers map[string]string, identity llm.AppIdentity) map[string]string {
	attribution := llm.AttributionHeaders(identity)
	reserved := make(map[string]struct{}, len(attribution))
	for name := range attribution {
		reserved[strings.ToLower(name)] = struct{}{}
	}
	merged := make(map[string]string, len(headers)+len(attribution))
	for name, value := range headers {
		if _, collides := reserved[strings.ToLower(name)]; collides {
			continue
		}
		merged[name] = value
	}
	maps.Copy(merged, attribution)
	return merged
}

// profileOf 在一份快照里找出一条路由的配置，找不到就是「这条路由不归我」。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:239-245
func profileOf(snap *snapshot, provider string) (ResolvedProviderProfile, error) {
	profile, owned := snap.profiles.Get(provider)
	if !owned {
		return ResolvedProviderProfile{}, llm.NewError(
			fmt.Sprintf("the openai-compatible adapter does not own provider %q", provider), "NO_ADAPTER", nil)
	}
	return profile, nil
}

// modelOf 在一份快照里找出一对确切的路由／模型。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:248-255
func modelOf(snap *snapshot, provider, model string) (ResolvedProviderProfile, ResolvedModel, error) {
	profile, err := profileOf(snap, provider)
	if err != nil {
		return ResolvedProviderProfile{}, ResolvedModel{}, err
	}
	resolved, configured := profile.Model(model)
	if !configured {
		return ResolvedProviderProfile{}, ResolvedModel{}, llm.NewError(
			fmt.Sprintf("provider %q has no configured model %q", provider, model), "UNKNOWN_MODEL", nil)
	}
	return profile, resolved, nil
}

// ProviderInfo 描述一条路由。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:257-262
func (a *Adapter) ProviderInfo(provider string) llm.ProviderInfo {
	// 交配置里那个名字，不是路由键：displayName 存在的意义就是让一次部署能给路由
	// 贴个标签，而一个只有配置界面读得到的标签等于没贴。
	name := provider
	if profile, owned := a.current().profiles.Get(provider); owned {
		name = profile.DisplayName
	}
	return llm.ProviderInfo{ID: provider, Name: name}
}

// ProviderRetryPolicy 交出一条路由在登记那一刻捕获的重试策略。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:264-266
func (a *Adapter) ProviderRetryPolicy(provider string) (llm.ResolvedRetryPolicy, bool) {
	profile, owned := a.current().profiles.Get(provider)
	if !owned {
		return llm.ResolvedRetryPolicy{}, false
	}
	return profile.RetryPolicy, true
}

// ListModels 列出一条路由公告的模型，按配置里的书写次序。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:268-279
func (a *Adapter) ListModels(_ context.Context, provider string) ([]llm.ModelInfo, error) {
	profile, err := profileOf(a.current(), provider)
	if err != nil {
		return nil, err
	}
	models := make([]llm.ModelInfo, 0, len(profile.Models))
	for _, model := range profile.Models {
		models = append(models, llm.ModelInfo{
			Provider:        provider,
			ID:              model.ID,
			Name:            model.Name,
			InputModalities: slices.Clone(model.Input),
		})
	}
	return models, nil
}

// ResolveModel 解算一个确切模型的全部元数据。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:281-290
func (a *Adapter) ResolveModel(_ context.Context, provider, model string) (llm.ResolvedModelInfo, error) {
	profile, resolved, err := modelOf(a.current(), provider, model)
	if err != nil {
		return llm.ResolvedModelInfo{}, err
	}
	return profile.ModelInfo(resolved), nil
}

// PrepareCall 把模型元数据和之后那次派发绑在同一份快照上。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:310-316
//
// 交出去的那个 Stream 闭包握着**这一份**快照，所以「准备」和「派发」之间的一次
// 配置改动没法把这一代的能力和另一代的端点凑到一起。
func (a *Adapter) PrepareCall(_ context.Context, provider, model string) (llm.PreparedAdapterCall, error) {
	snap := a.current()
	profile, resolved, err := modelOf(snap, provider, model)
	if err != nil {
		return llm.PreparedAdapterCall{}, err
	}
	return llm.PreparedAdapterCall{
		Model: profile.ModelInfo(resolved),
		Stream: func(ctx context.Context, options llm.GenerateOptions) (iter.Seq2[llm.StreamChunk, error], error) {
			return a.streamWithSnapshot(ctx, options, snap)
		},
	}, nil
}

// Stream 按当下这份快照发一次流式调用。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:318-320
func (a *Adapter) Stream(
	ctx context.Context,
	options llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	return a.streamWithSnapshot(ctx, options, a.current())
}

// resolveReasoning 验一个显式点下来的推理档位，不认就拒。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:155-166
//
// 空串表示没点，交出一个空的档位（于是请求体里不写 reasoning_effort）。
//
// 一条路由的 reasoning 默认值落到一个**不提供**这一档的模型上时，这里同样会拒。
// 这是有意的，也和 DSH 一致（adapter.ts:131-139 那段说得很清楚）：描述一个模型
// 能干什么不该因为部署方问了它干不了的事而失败，所以 [ResolvedProviderProfile.ModelInfo]
// 那边遇到这种情况只是不报默认档位；但**请求**这条路上必须拒——一份配错了的配置
// 就该在请求时被点出来，而不是被悄悄改成别的档位发出去。
func resolveReasoning(model ResolvedModel, effort llm.ReasoningEffortID) (ReasoningEffort, error) {
	if effort == "" {
		return ReasoningEffort{}, nil
	}
	offered, supported := model.Effort(effort)
	if !supported {
		return ReasoningEffort{}, llm.NewError(fmt.Sprintf(
			"provider %q model %q does not support reasoning effort %q", model.Provider, model.ID, effort),
			"UNSUPPORTED_REASONING_EFFORT", nil)
	}
	return offered, nil
}

// containsImage 判这次请求的历史里有没有图。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:350
func containsImage(messages []llm.Message) bool {
	for _, message := range messages {
		if llm.ContentHasImage(message.Content) {
			return true
		}
	}
	return false
}

// streamItem 是生产者交给消费者的一格：一块，或者一条把这条流终结掉的错误。
type streamItem struct {
	chunk llm.StreamChunk
	err   error
}

// streamWithSnapshot 按一份冻住的快照发一次流式调用。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:322-411
//
// 分工按 [llm.Adapter].Stream 那条接缝走：选路、拼请求、建连这一段**当场**做完，
// 失败交第二个返回值；只有读流那一段是懒的，它的失败从序列里交出来。这不只是
// 为了对上接缝的说法——NewStreaming 是同步发请求的（chatcompletion.go:99-110），
// 所以拿到 stream 的那一刻就知道建连成没成，而如果把它推迟到第一次迭代，
// 一个拿到序列却不遍历的调用方会让那个已经打开的响应体一直挂着。
//
// 新增: DSH 在这里拒 options.stop（adapter.ts:326-328），理由是 pi-ai 那层表达不了
// 停止串。这条协议表达得了——stop 是 Chat Completions 自己的字段——所以这边**支持**它。
//
// 新增: options.SessionID 不往线上映射。DSH 把它交给 pi-ai 的 sessionId（那个库
// 自己决定怎么用）。这条协议上装得下它的只有 prompt_cache_key / user / metadata
// 三个字段，而这三个都是 OpenAI 自己的服务端存储与滥用识别标识：一个严格的网关
// 会因为不认得它们而拒掉整个请求，而换来的好处只是一个模型看不见的标签。
// 接缝说的是适配器**可以**映射它，不是必须。
func (a *Adapter) streamWithSnapshot(
	ctx context.Context,
	options llm.GenerateOptions,
	snap *snapshot,
) (iter.Seq2[llm.StreamChunk, error], error) {
	// 一次调用只捕获一次：路由配置、模型记录、客户端全都来自同一份不可变快照，
	// 凭据跟着它们一起冻住。
	//
	// 源: packages/llm/llm-pi-ai/src/adapter.ts:329-340
	profile, model, err := modelOf(snap, options.Provider, options.Model)
	if err != nil {
		return nil, err
	}
	effort := options.ReasoningEffort
	if effort == "" {
		effort = profile.Reasoning
	}
	reasoning, err := resolveReasoning(model, effort)
	if err != nil {
		return nil, err
	}

	// 源: packages/llm/llm-pi-ai/src/adapter.ts:350-357
	var attachments attachment.Store
	if containsImage(options.Messages) {
		if !model.SupportsImages() {
			return nil, llm.NewError(
				fmt.Sprintf("model %q does not support image input", model.ID), "UNSUPPORTED_CONTENT", nil)
		}
		if a.options.ResolveAttachments != nil {
			attachments = a.options.ResolveAttachments()
		}
		if attachments == nil {
			return nil, llm.NewError(
				"image input requires the durable attachment service", "UNSUPPORTED_CONTENT", nil)
		}
	}

	// 源: packages/llm/llm-pi-ai/src/adapter.ts:340
	apiKey, err := a.options.ResolveAPIKey(ctx, options.Provider, profile)
	if err != nil {
		return nil, err
	}

	// 两层超时：外层是这次请求的总时限（可能不设），内层是上游的空闲上限。
	// 内层挂在外层之下，于是 [context.Cause] 在两条超时之间自然分得清谁先到。
	//
	// 源: packages/llm/llm-pi-ai/src/adapter.ts:346-347
	requestCtx, releaseDeadline := timeout.Deadline(ctx, profile.Timeout, RequestTimeoutCode)
	watchdog, err := timeout.NewWatchdog(requestCtx, profile.StreamIdleTimeout, StreamIdleTimeoutCode)
	if err != nil {
		releaseDeadline()
		return nil, err
	}
	streamCtx := watchdog.Context()
	// 派发阶段任何一处提前返回都得把这两层拆掉，否则定时器和子 context 一起泄漏。
	abandon := func(err error) (iter.Seq2[llm.StreamChunk, error], error) {
		failure := a.failureOf(err, streamCtx, ctx)
		watchdog.Stop()
		releaseDeadline()
		return nil, failure
	}

	onReplayDegrade := func(reason string) {
		if a.options.OnReplayDegrade != nil {
			a.options.OnReplayDegrade(options.Provider, options.Model, reason)
		}
	}
	// 源: packages/llm/llm-pi-ai/src/adapter.ts:361-366
	maxImageBytes := profile.MaxRequestImageBytes
	request, err := toContext(streamCtx, options, attachments, onReplayDegrade, &maxImageBytes,
		attachment.RequestPolicy{
			MaxPixels: profile.RequestImagePixelBudget,
			MaxBytes:  profile.RequestImageMaxBytes,
		})
	if err != nil {
		return abandon(err)
	}

	params := openai.ChatCompletionNewParams{
		Messages: request.messages,
		Model:    shared.ChatModel(model.ID),
		Tools:    request.tools,
		// 不点这一下的话，最后那条带用量的分块根本不会来，于是每一次响应的
		// token 记账都是零——预算、压缩触发、账单核对全都没了依据。
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	}
	// 源: packages/llm/llm-pi-ai/src/adapter.ts:369-370
	if options.Temperature != nil {
		params.Temperature = param.NewOpt(*options.Temperature)
	}
	if options.MaxTokens > 0 {
		// 新增: 用 max_tokens 而不是 max_completion_tokens。后者是 OpenAI 自家新模型
		// 上的字段，网关和本地推理服务器（llama.cpp、vLLM、Ollama、DeepSeek）普遍
		// 只认前者；发一个它们不认得的字段，轻的是被忽略（于是这次请求根本没有上限），
		// 重的是整个请求被拒。
		params.MaxTokens = param.NewOpt(int64(options.MaxTokens))
	}
	if len(options.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: slices.Clone(options.Stop)}
	}
	// 空的线上拼法表示这一档什么都不发（只有 off 那一档允许这样），
	// 见 [ModelProfile].ReasoningEfforts。
	if reasoning.Wire != "" {
		params.ReasoningEffort = shared.ReasoningEffort(reasoning.Wire)
	}

	// 凭据是**每次请求**解算的（引用语义要求解不出来就当场失败），而客户端是随
	// 快照走的，所以它只能作为一次请求的选项落下去，而不是烤进客户端。这和 DSH
	// 把它作为请求的 apiKey 覆盖交给 pi-ai 是同一件事（adapter.ts:16-19）。
	var callOptions []option.RequestOption
	if apiKey != "" {
		callOptions = append(callOptions, option.WithAPIKey(apiKey))
	}

	service := snap.services[options.Provider]
	stream := service.NewStreaming(streamCtx, params, callOptions...)
	if err := stream.Err(); err != nil {
		// Close 对一个连响应体都没有的流是安全的（ssestream.go:253-261）。
		_ = stream.Close()
		return abandon(err)
	}

	// 生产者把 [streamChunks] 那串块推到一个无缓冲通道上，消费者拿
	// [timeout.Receive] 去取——看门狗的定时器只在那次取的**期间**开着，
	// 于是消费方自己慢慢处理一块的那段时间不算上游空闲。
	items := make(chan streamItem)
	go func() {
		defer close(items)
		// 关流归生产者所有：消费者那边关会和这边正在进行的读撞在一起。
		defer func() { _ = stream.Close() }()
		for chunk, err := range streamChunks(stream, model.ID, model.ContextWindow) {
			select {
			case items <- streamItem{chunk: chunk, err: err}:
			case <-streamCtx.Done():
				return
			}
		}
	}()

	return func(yield func(llm.StreamChunk, error) bool) {
		// 顺序要紧：releaseDeadline 先注册、后执行，所以看门狗先停。两者都会取消
		// 自己那层 context，于是生产者那个 goroutine 一定退得出来——它要么在
		// select 里等着写，要么在读一条已经被取消的 HTTP 响应。
		defer releaseDeadline()
		defer watchdog.Stop()
		for {
			next, open, err := timeout.Receive(watchdog, items)
			if err != nil {
				yield(nil, a.failureOf(err, streamCtx, ctx))
				return
			}
			if !open {
				return
			}
			if next.err != nil {
				// 流在半路断了。已经吐出去的块是真的收到过的，不撤回。
				yield(nil, a.failureOf(next.err, streamCtx, ctx))
				return
			}
			if !yield(next.chunk, nil) {
				return
			}
		}
	}, nil
}

// failureOf 把一条错误归一成这条接缝交得出去的失败。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts:400-407
//
// 判定次序是有讲究的：一条超时先取消 context、再让底下的读失败，所以错误本身
// 往往只是一句「context canceled」，真正的原因挂在 context 的 cause 上。先问
// context 才认得出是谁先取消的；两条超时都问过了才轮到「是调用方取消的吗」，
// 因为本层超时也会让调用方的 ctx 之外的东西结束，反过来问会把自己的超时报成
// 一次用户取消——而那两者在重试上的处理完全相反。
func (a *Adapter) failureOf(err error, streamCtx, callerCtx context.Context) error {
	if reason := timeoutReason(err, streamCtx, StreamIdleTimeoutCode); reason != nil {
		return llm.NewError(fmt.Sprintf(
			"the openai-compatible provider went silent for %dms", reason.After.Milliseconds()), "TIMEOUT", err)
	}
	if reason := timeoutReason(err, streamCtx, RequestTimeoutCode); reason != nil {
		return llm.NewError(fmt.Sprintf(
			"the openai-compatible request did not finish within %dms", reason.After.Milliseconds()), "TIMEOUT", err)
	}
	if callerCtx.Err() != nil {
		return llm.NewError("the openai-compatible request was aborted by the caller", "ABORTED", err)
	}
	return &llm.Error{Failure: classifyError(err), Cause: err}
}

// timeoutReason 先问错误、再问 context，认不认得出某一条超时。
//
// 新增: DSH 只问信号（timeoutOf(watchdog.signal, ...)），因为它那边取消的原因
// 一定在信号上。Go 这边两处都可能：[timeout.Receive] 直接把 cause 当错误交出来，
// 而底下的 HTTP 读交出来的是一句包着 context.Canceled 的话。所以两处都问。
func timeoutReason(err error, ctx context.Context, code string) *timeout.Reason {
	if reason := timeout.Of(err, code); reason != nil {
		return reason
	}
	return timeout.OfContext(ctx, code)
}
