// 本文件的作用：这个插件的配置形状，以及把一份随手写的提供方路由表验成「每条
// 路由都服务得了」的那一步。
//
// 源: packages/llm/llm-pi-ai/src/config.ts

package openaicompat

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"ds-harness-go/credentials"
	"ds-harness-go/llm"
)

// DefaultStreamIdleTimeout 是一次流式读还挂着时，允许提供方空闲多久的默认值。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:43
const DefaultStreamIdleTimeout = 300 * time.Second

// DefaultMaxRequestImageBytes 是每次请求里 base64 图片负载的默认上限。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:54
//
// 历史里的每张图都会被重新编码进**每一次**请求体，所以一段没有上限的对话迟早
// 会撑破提供方或者网关的请求体上限，那之后这个会话再也完成不了一次请求。20MiB
// 这个默认值在 base64 膨胀之后放得下十五张 1MiB 的请求版本，同时给系统提示词、
// 历史、工具和 JSON 本身留出余量。网关更严的部署按路由把它调低。
const DefaultMaxRequestImageBytes = 20 * 1024 * 1024

// DefaultRequestImagePixelBudget 是每个请求版本的默认总像素预算，
// 正好放得下一张 2048px 的规范化附件。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:56
const DefaultRequestImagePixelBudget = 2048 * 2048

// DefaultRequestImageMaxBytes 是每个请求版本在 base64 膨胀之前的默认原始字节上限。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:58
const DefaultRequestImageMaxBytes = 1024 * 1024

// DefaultContextWindow 是配置没给上下文容量的模型按多大算。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:61
const DefaultContextWindow = 262_144

// DefaultMaxTokens 是配置没给输出能力的模型按多大算。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:64
const DefaultMaxTokens = 32_768

// DefaultInput 交出配置没声明模态的模型按什么算。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:76
//
// 文本是每条支持的协议都一定载得动的底线，所以这是「没有声明」而不是对端点的
// 一次猜测：没有任何办法去问一个网关它收哪些模态，而两种猜错的代价不一样。
// 报小了，图在**被挂上去之前**就被拒，诊断里点得出是哪个模型；报大了，一张图
// 会被收下、然后被提供方在半个回合中间拒掉，而那时候消息已经落库了，会话会一直
// 重复一次不可能成功的请求。
func DefaultInput() []llm.ModelModality { return []llm.ModelModality{llm.ModalityText} }

// ProviderProfile 是一条提供方路由的配置；[Config].Providers 的那个键**就是**路由。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:88-176
//
// 新增: DSH 那份里有九个字段是 pi-ai 自己类型上的东西——api、modelOverrides、
// compat、thinkingBudgets、cacheRetention、transport、websocketConnectTimeoutMs，
// 以及 modelOverrides 那一整条路。它们连同 pi-ai 一起不移，理由见包注释。
// 剩下的字段逐条对着 DSH 走。
//
// 新增: 每个字段都带 json 标签，键名逐字照 DSH 的 schema（config.ts:88-176）。
// 这个类型会作为一个设置命名空间的形状登记出去，而 [ds-harness-go/settings.Register]
// 是拿 encoding/json 把它来回过一遍的，理由同 [ModelProfile]。两个时限字段也因此
// 从 [time.Duration] 改成毫秒整数：一个 time.Duration 在 JSON 里是**纳秒**，
// 写配置的人要为 30 秒打出 30000000000。毫秒整数加 `Ms` 后缀是本仓库对 JSON 上
// 时限的既有写法（session/stats、[llm.Failure].ProviderRetryAfterMs），也正好
// 和 DSH 的 timeoutMs / streamIdleTimeoutMs 逐字对上。
type ProviderProfile struct {
	// APIKeyEnv 是每次请求时通过凭据面解析的那条凭据引用（一个环境变量名）。
	//
	// 空表示这条路由不认证。DSH 那边空表示「交给 pi-ai 自己的环境发现」——
	// 这边没有那个库，所以空就是字面上的不带 Authorization 头，本地模型正是
	// 这么跑的。
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
	// DisplayName 是配置界面上显示的名字；空表示用路由键。
	DisplayName string `json:"displayName,omitempty"`
	// BaseURL 是这条路由上所有模型的端点，必填。
	//
	// 新增: DSH 那边可以不填，缺省取内置目录里这条提供方的端点。没有内置目录
	// 之后没有东西能兜底，所以它成了必填。
	BaseURL string `json:"baseURL"`
	// Models 是这条路由的模型清单，必填且非空，理由同 BaseURL。
	Models []ModelProfile `json:"models"`
	// DefaultContextWindow 是这条路由上没写 contextWindow 的模型按多大算；
	// 零表示 [DefaultContextWindow]。
	//
	// 天生是个猜测，所以网关服务的是更小的模型时在这里改。
	DefaultContextWindow int `json:"defaultContextWindow,omitempty"`
	// DefaultMaxTokens 是这条路由上没写 maxTokens 的模型按多大算；
	// 零表示 [DefaultMaxTokens]。
	//
	// 它给模型定的是能力；它自己**永远不会**变成一次请求的上限。
	DefaultMaxTokens int `json:"defaultMaxTokens,omitempty"`
	// DefaultInput 是这条路由上没声明模态的模型收哪些模态；
	// nil 表示 [DefaultInput]。
	//
	// 它是兜底不是覆盖，而且**不能是空的**——底下没有别的东西能替它回答了。
	// 一个网关服务的全是收图的模型时，在这里声明一次 [text, image] 就够了，
	// 不必写在每条模型上。
	//
	// 不写 omitempty，理由和 [ModelProfile].ReasoningEfforts 那条相同：nil 走默认，
	// 非 nil 但空要被拒，而 omitempty 会把后者变成前者。
	DefaultInput []llm.ModelModality `json:"defaultInput"`
	// Headers 是挂在这条路由每次请求上的额外请求头；归属头（见
	// [llm.AttributionHeaders]）在重名时赢。
	Headers map[string]string `json:"headers,omitempty"`
	// Reasoning 是这条路由的默认推理档位；空表示保留提供方自己的默认。
	//
	// 它落进 [llm.ModelReasoningInfo].DefaultEffort，只对**提供这一档**的模型
	// 生效，见 [ResolvedProviderProfile.ModelInfo]。
	Reasoning llm.ReasoningEffortID `json:"reasoning,omitempty"`
	// TimeoutMs 是单次 HTTP 请求的超时（毫秒）；零表示不设总时限，
	// 只靠 StreamIdleTimeoutMs。
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// StreamIdleTimeoutMs 是一次流式读还挂着时允许提供方空闲多久（毫秒）；
	// 零表示 [DefaultStreamIdleTimeout]。
	StreamIdleTimeoutMs int `json:"streamIdleTimeoutMs,omitempty"`
	// MaxRequestImageBytes 是每次请求 base64 图片负载的上限；零表示
	// [DefaultMaxRequestImageBytes]。
	//
	// 一次请求累计的图片超了它，最老的那几张会被换成文字占位，直到这次请求塞得下
	// ——于是一段很长的会话还在完成请求，而不是被请求体上限一直拒掉。
	MaxRequestImageBytes int `json:"maxRequestImageBytes,omitempty"`
	// RequestImagePixelBudget 是每个确定性内联请求版本的总像素预算；零表示
	// [DefaultRequestImagePixelBudget]。
	RequestImagePixelBudget int `json:"requestImagePixelBudget,omitempty"`
	// RequestImageMaxBytes 是每个确定性内联请求版本的原始字节上限；零表示
	// [DefaultRequestImageMaxBytes]。
	RequestImageMaxBytes int `json:"requestImageMaxBytes,omitempty"`
	// RetryPolicy 是这条路由自己拥有的模型请求重试策略；nil 表示 normal 档
	// 加五次重试的整套默认。
	RetryPolicy *RetryPolicy `json:"retryPolicy,omitempty"`
}

// RetryPolicy 是一条路由在配置里写下来的重试策略。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:170-175
//
// 新增: 它是 [llm.RetryPolicyConfig] 的一份**设置形状**的孪生，字段逐一对应。
// 多这一层的理由只有一个：那个类型是 llm 包的，没有 json 标签，而且它的三个
// 时限字段是 [time.Duration]（在 JSON 里是纳秒）。给 llm 那份加标签会连带改动
// 一个已经写完、有测试、还被 llm/llmretry 用着的包，只为了让这一个插件的设置
// 界面好看——代价和收益完全不成比例。所以孪生一份放在这里，由 [RetryPolicy.config]
// 翻过去。
//
// 每个字段的含义、取值范围和默认值全部由 [llm.ResolveRetryPolicy] 说了算，
// 这里不重复也不预先校验：预先校验会让同一条毛病有两份措辞不同的诊断。
type RetryPolicy struct {
	// Mode 是重试档位，必填；能写的是 normal 与 always。
	//
	// 源: packages/llm/llm/src/retry-policy.ts:39、51
	Mode llm.RetryMode `json:"mode"`
	// MaxRetries 是第一次请求之后还能重试几次；不写表示默认 5。
	// Mode 不是 normal 时它不生效。
	//
	// 是指针，好把「没写」和「写了 0」（一次都不重试）分开。
	MaxRetries *int `json:"maxRetries,omitempty"`
	// RetryableCodes 是这一档认的那些稳定失败码；不写表示用默认集合。
	// Mode 不是 normal 时它不生效。
	//
	// 不写 omitempty：不写这个键和写一个空清单是两件事（后者会被解算拒掉），
	// 理由同 [ModelProfile].ReasoningEfforts。
	RetryableCodes []string `json:"retryableCodes"`
	// Backoff 是本地指数退避与抖动。
	Backoff Backoff `json:"backoff,omitzero"`
}

// Backoff 是一条路由在配置里写下来的退避。
//
// 源: packages/llm/llm/src/retry-policy.ts:27-34
//
// 新增: 存在的理由同 [RetryPolicy]——[llm.BackoffConfig] 的两个时限是
// [time.Duration]，在 JSON 里是纳秒。
type Backoff struct {
	// InitialDelayMs 是第一段延时（毫秒）；零表示没给，默认 500。
	InitialDelayMs int `json:"initialDelayMs,omitempty"`
	// MaxDelayMs 是延时上限（毫秒）；零表示没给，默认 10000。
	MaxDelayMs int `json:"maxDelayMs,omitempty"`
	// JitterRatio 是围绕 1 的对称随机倍率范围；不写表示默认 0.1。
	//
	// 是指针，因为 0 是一个有意义的取值（完全不抖动），理由见
	// [llm.BackoffConfig].JitterRatio。
	JitterRatio *float64 `json:"jitterRatio,omitempty"`
}

// config 把这份设置形状翻成 [llm.ResolveRetryPolicy] 收的那种。
//
// nil 收下之后仍然交 nil：那表示这条路由没写策略，整套默认由解算那一步给出。
func (p *RetryPolicy) config() *llm.RetryPolicyConfig {
	if p == nil {
		return nil
	}
	return &llm.RetryPolicyConfig{
		Mode:           p.Mode,
		MaxRetries:     p.MaxRetries,
		RetryableCodes: slices.Clone(p.RetryableCodes),
		Backoff: llm.BackoffConfig{
			InitialDelay: time.Duration(p.Backoff.InitialDelayMs) * time.Millisecond,
			MaxDelay:     time.Duration(p.Backoff.MaxDelayMs) * time.Millisecond,
			JitterRatio:  p.Backoff.JitterRatio,
		},
	}
}

// ResolvedProviderProfile 是一份验过的配置：路由已经盖上去，适配器自己拥有的
// 那些默认值全都落实了。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:179-210
type ResolvedProviderProfile struct {
	// Provider 是路由键，也就是配置里那个字典键。
	Provider string
	// DisplayName 是给选择器和配置界面的显示名，保证非空。
	DisplayName string
	// BaseURL 是这条路由的端点，保证非空。
	BaseURL string
	// APIKeyRef 是验过的凭据引用；空表示这条路由不认证。
	APIKeyRef credentials.Ref
	// Models 是落定的模型，按配置里的书写次序排着。
	Models []ResolvedModel
	// ConfiguredMaxTokens 是这份配置显式选下来的每次请求输出上限，按模型 id 索引。
	//
	// 源: packages/llm/llm-pi-ai/src/config.ts:205-209
	//
	// 接缝只会把它落进一次**自己没点上限**的请求，所以一个从模型能力上兜出来的
	// 数字绝不能出现在这里。
	ConfiguredMaxTokens map[string]int
	// Headers 是这条路由的额外请求头，本包自己拥有的一份。
	Headers map[string]string
	// Reasoning 是这条路由的默认推理档位；空表示保留提供方自己的默认。
	Reasoning llm.ReasoningEffortID
	// Timeout 是单次 HTTP 请求的超时；零表示不设总时限。
	Timeout time.Duration
	// StreamIdleTimeout 是提供方空闲上限，落实完保证为正。
	StreamIdleTimeout time.Duration
	// MaxRequestImageBytes 落实完保证为正。
	MaxRequestImageBytes int
	// RequestImagePixelBudget 落实完保证为正。
	RequestImagePixelBudget int
	// RequestImageMaxBytes 落实完保证为正。
	RequestImageMaxBytes int
	// RetryPolicy 是随这条路由一起在登记那一刻捕获的那份不可变策略。
	RetryPolicy llm.ResolvedRetryPolicy
}

// Model 按 id 找出这条路由上的一条模型。
func (p ResolvedProviderProfile) Model(id string) (ResolvedModel, bool) {
	for _, model := range p.Models {
		if model.ID == id {
			return model, true
		}
	}
	return ResolvedModel{}, false
}

// ModelInfo 把一条模型描述成 llm 那一层的元数据。
//
// 源: packages/llm/llm-pi-ai/src/adapter.ts（那段 resolveModel 造出来的返回值）
func (p ResolvedProviderProfile) ModelInfo(model ResolvedModel) llm.ResolvedModelInfo {
	info := llm.ResolvedModelInfo{
		ModelInfo: llm.ModelInfo{
			Provider:        p.Provider,
			ID:              model.ID,
			Name:            model.Name,
			InputModalities: slices.Clone(model.Input),
		},
		Context:          &llm.ModelContext{ContextWindow: model.ContextWindow},
		DefaultMaxTokens: p.ConfiguredMaxTokens[model.ID],
	}
	if len(model.ReasoningEfforts) == 0 {
		return info
	}
	reasoning := llm.ModelReasoningInfo{Efforts: make([]llm.ReasoningEffortInfo, 0, len(model.ReasoningEfforts))}
	for _, effort := range model.ReasoningEfforts {
		reasoning.Efforts = append(reasoning.Efforts, llm.ReasoningEffortInfo{
			ID:   effort.ID,
			Name: string(effort.ID),
		})
	}
	// 路由的默认档位只在**这个模型确实提供它**的时候落下去。路由是按整条路由写的，
	// 而同一条路由上放着一个推理模型和一个只提供 low/high 的模型是完全正常的；
	// 把一个它不提供的档位报成默认，等于让每一次不点档位的请求都撞
	// UNSUPPORTED_REASONING_EFFORT。
	if _, offered := model.Effort(p.Reasoning); offered {
		reasoning.DefaultEffort = p.Reasoning
	}
	info.Reasoning = &reasoning
	return info
}

// Config 是这个插件的配置：这个实例拥有的那些提供方路由。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:213-220
type Config struct {
	// Providers 是按提供方路由索引的那些配置。空（或者不给）是**待命**姿态：
	// 适配器挂上去、一条路由都不登记，等设置层递来配置的那一刻再登记。
	Providers map[string]ProviderProfile `json:"providers,omitempty"`
}

// Profiles 是一整套验过的提供方路由。
//
// 新增: DSH 交出来的是一个 Map，靠 JS 对象的插入序拿到「配置书写次序」。Go 的 map
// 没有次序，所以这里包一层，把「按路由键排序」这件事变成唯一的遍历入口——
// [Profiles.Routes] 是它。登记事实、目录条目、诊断里的枚举全都指望这个次序稳定：
// 不稳的话，同一份配置解算两次会给出两串不同的登记事实，于是每一次设置变更都
// 会把整套路由白白重新登记一遍。
//
// 新增: 里面装的是一个**指针**而不是那张 map 本身，为的是让 Profiles 用 == 就能
// 比身份。[Adapter] 靠这个比较决定要不要重建它那份快照，而快照里每条路由都握着
// 一个 openai-go 客户端——那个客户端自带一份克隆出来的 http.Transport，也就是
// 一整个连接池。按内容比较做不到（Go 的 map 不可比较），按内容深比又会把「配置
// 没变」误判成「变了」的反面：两份内容相同、来自两次解算的路由表本来就该复用
// 同一份快照。只有身份比较答得准，而它要求这里是一个可比较的引用。
type Profiles struct {
	table *profileTable
}

// profileTable 是一次解算落下来的那张不可变路由表。
//
// 它单独成一个类型只为一件事：给 [Profiles] 一个可以用 == 比较的身份。解算之后
// 谁都不再改它，所以复制 Profiles 值是安全的——复制出来的那一份和原本是同一张表。
type profileTable struct {
	byRoute map[string]ResolvedProviderProfile
}

// routes 交出底层那张表；零值 Profiles 给的是 nil，读它的每一处都受得住。
func (p Profiles) routes() map[string]ResolvedProviderProfile {
	if p.table == nil {
		return nil
	}
	return p.table.byRoute
}

// Len 交出路由条数。
func (p Profiles) Len() int { return len(p.routes()) }

// Routes 交出所有路由键，按字典序排着。
func (p Profiles) Routes() []string { return slices.Sorted(maps.Keys(p.routes())) }

// Get 按路由键取一份配置。
func (p Profiles) Get(provider string) (ResolvedProviderProfile, bool) {
	profile, ok := p.routes()[provider]
	return profile, ok
}

// All 按 [Profiles.Routes] 的次序遍历所有路由。
func (p Profiles) All(visit func(provider string, profile ResolvedProviderProfile) bool) {
	table := p.routes()
	for _, provider := range p.Routes() {
		if !visit(provider, table[provider]) {
			return
		}
	}
}

// AssertServiceable 拒掉一份这个适配器服务不了的配置。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:349-351
//
// 它登记成设置命名空间的校验器，好让一份服务不了的配置在**被写下的地方**就被拒
// ——设置层的回答里点得出是哪条路由、哪个模型——而不是先存下来、然后悄悄把这个
// 命名空间里的每条路由都停掉。
func AssertServiceable(config Config) error {
	_, err := ResolveProfiles(config.Providers)
	return err
}

// ResolveProfiles 验一套配置，交出一份和调用方脱钩、适合每次请求去读的路由表。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:379-465
//
// 这是唯一那一次显式的解算，所以一份没给的配置在这里就变成空的（待命的）路由集，
// 而不是走某条藏起来的兜底；每条路由的模型也在这里落定一次。
//
// 新增: DSH 头两件事是拒掉 providers 写成数组、以及拒掉三个已经删掉的旧字段
// （provider / maxRetries / maxRetryDelayMs，见 config.ts:354-369）。Go 的类型和
// 结构体写不出这两种形状，那两步连同它们的文案一起不需要。理由和
// [llm.ResolveRetryPolicy] 上那条 validateKeys 逐字相同。
//
// 新增: 遍历按路由键**排序**，不是按配置里的书写次序——Go 的 map 根本留不住那个
// 次序。可观察的差别只有「一份错了好几处的配置先报哪一处」，而排序的答案是稳定的，
// 书写次序的答案在 Go 里会随机。
func ResolveProfiles(providers map[string]ProviderProfile) (Profiles, error) {
	resolved := make(map[string]ResolvedProviderProfile, len(providers))
	for _, provider := range slices.Sorted(maps.Keys(providers)) {
		profile, err := resolveProfile(provider, providers[provider])
		if err != nil {
			return Profiles{}, err
		}
		resolved[provider] = profile
	}
	return Profiles{table: &profileTable{byRoute: resolved}}, nil
}

// resolveProfile 验一条路由的配置并落实它的默认值。
//
// 源: packages/llm/llm-pi-ai/src/config.ts:387-462
//
// 检查次序照着 DSH 走：一份同时错了好几处的配置，两边报出来的是同一处。
func resolveProfile(provider string, source ProviderProfile) (ResolvedProviderProfile, error) {
	// 源: packages/llm/llm-pi-ai/src/config.ts:389
	if provider == "" {
		return ResolvedProviderProfile{}, fmt.Errorf("%w：提供方名字不能是空的", ErrInvalidConfig)
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:390-392
	//
	// DSH 那句判的是「给了但是空串」，因为不给会走内置目录。这边不给和给空串
	// 是同一件事——都没有端点——所以合成一条。
	if source.BaseURL == "" {
		return ResolvedProviderProfile{}, invalidRoute(provider,
			"没有 baseURL；这个适配器不带内置端点目录，所以每条路由都要写出自己的端点")
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:393-395
	//
	// 这条仍然只判「空串」：不给会兜到路由键上去，那是正当的。
	displayName := source.DisplayName
	if displayName == "" {
		// 取路由键而不是别的什么名字：配置界面上一直显示的就是路由键，一条路由
		// 不该只因为多了一个 displayName 字段就在每个界面上悄悄换个名字。
		//
		// 源: packages/llm/llm-pi-ai/src/config.ts:426-428
		displayName = provider
	}

	// 源: packages/llm/llm-pi-ai/src/config.ts:396-403
	//
	// 新增: DSH 还要求它不超过 MAX_TIMER_DELAY_MS。那是 JS 的 setTimeout 把超过
	// 32 位的延迟悄悄压成 1 毫秒这件实现细节，Go 的 [time.Timer] 没有这个悬崖，
	// 所以那条上界不存在。理由和 [ds-harness-go/util/timeout.NewWatchdog] 上
	// 那条逐字相同。
	streamIdleTimeout := time.Duration(source.StreamIdleTimeoutMs) * time.Millisecond
	if streamIdleTimeout == 0 {
		streamIdleTimeout = DefaultStreamIdleTimeout
	}
	if streamIdleTimeout <= 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 streamIdleTimeout 必须是正数")
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:404-407
	maxRequestImageBytes := source.MaxRequestImageBytes
	if maxRequestImageBytes == 0 {
		maxRequestImageBytes = DefaultMaxRequestImageBytes
	}
	if maxRequestImageBytes < 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 maxRequestImageBytes 必须是正整数")
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:408-411
	requestImagePixelBudget := source.RequestImagePixelBudget
	if requestImagePixelBudget == 0 {
		requestImagePixelBudget = DefaultRequestImagePixelBudget
	}
	if requestImagePixelBudget < 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 requestImagePixelBudget 必须是正整数")
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:412-415
	requestImageMaxBytes := source.RequestImageMaxBytes
	if requestImageMaxBytes == 0 {
		requestImageMaxBytes = DefaultRequestImageMaxBytes
	}
	if requestImageMaxBytes < 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 requestImageMaxBytes 必须是正整数")
	}
	// 源: packages/llm/llm-pi-ai/src/config.ts:416-424
	//
	// 和一条模型自己的 input 不一样：那一条空了还有这里能答，而这里空了底下就
	// 没人了，所以空是拒而不是「没有答案」。
	//
	// 新增: DSH 那边 schema 会给不填的键materialize 一个默认值，于是空清单一定是
	// 有人手打的。Go 这边不填就是 nil，所以 nil 走默认、**非 nil 但空**才是手打的
	// 那一份——和 [ModelProfile.ReasoningEfforts] 用的是同一个区分。
	defaultInput := slices.Clone(source.DefaultInput)
	if source.DefaultInput == nil {
		defaultInput = DefaultInput()
	}
	if len(defaultInput) == 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 defaultInput 至少要点出一种模态")
	}

	defaultContextWindow := source.DefaultContextWindow
	if defaultContextWindow == 0 {
		defaultContextWindow = DefaultContextWindow
	}
	if defaultContextWindow < 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 defaultContextWindow 必须是正整数")
	}
	defaultMaxTokens := source.DefaultMaxTokens
	if defaultMaxTokens == 0 {
		defaultMaxTokens = DefaultMaxTokens
	}
	if defaultMaxTokens < 0 {
		return ResolvedProviderProfile{}, invalidRoute(provider, "的 defaultMaxTokens 必须是正整数")
	}

	// 新增: DSH 把 reasoning 的取值交给 schemastery 的 z.union(THINKING_LEVELS)
	// （config.ts:319）。Go 这边它是个自由字符串，没人替它挡，所以在这里验。
	if source.Reasoning != "" && !isThinkingLevel(source.Reasoning) {
		return ResolvedProviderProfile{}, invalidRoute(provider, fmt.Sprintf(
			"的 reasoning 是一个不认得的档位 %q；能写的是 %v", source.Reasoning, thinkingLevels))
	}

	// 源: packages/llm/llm-pi-ai/src/config.ts:429-439
	catalog, err := resolveRouteModels(routeCatalogRequest{
		provider:             provider,
		models:               source.Models,
		defaultContextWindow: defaultContextWindow,
		defaultMaxTokens:     defaultMaxTokens,
		defaultInput:         defaultInput,
	})
	if err != nil {
		return ResolvedProviderProfile{}, err
	}

	// 源: packages/llm/llm-pi-ai/src/config.ts:445
	var apiKeyRef credentials.Ref
	if source.APIKeyEnv != "" {
		apiKeyRef, err = credentials.NewRef(source.APIKeyEnv)
		if err != nil {
			return ResolvedProviderProfile{}, fmt.Errorf("%w：提供方 %q 的 apiKeyEnv 不是一个合法的凭据引用：%w",
				ErrInvalidConfig, provider, err)
		}
	}

	// 源: packages/llm/llm-pi-ai/src/config.ts:450
	retryPolicy, err := llm.ResolveRetryPolicy(source.RetryPolicy.config(),
		fmt.Sprintf("%s: 提供方 %q 的 retryPolicy", PluginName, provider))
	if err != nil {
		return ResolvedProviderProfile{}, fmt.Errorf("%w：%w", ErrInvalidConfig, err)
	}

	return ResolvedProviderProfile{
		Provider:                provider,
		DisplayName:             displayName,
		BaseURL:                 source.BaseURL,
		APIKeyRef:               apiKeyRef,
		Models:                  catalog.models,
		ConfiguredMaxTokens:     catalog.configuredMaxTokens,
		Headers:                 maps.Clone(source.Headers),
		Reasoning:               source.Reasoning,
		Timeout:                 time.Duration(source.TimeoutMs) * time.Millisecond,
		StreamIdleTimeout:       streamIdleTimeout,
		MaxRequestImageBytes:    maxRequestImageBytes,
		RequestImagePixelBudget: requestImagePixelBudget,
		RequestImageMaxBytes:    requestImageMaxBytes,
		RetryPolicy:             retryPolicy,
	}, nil
}
