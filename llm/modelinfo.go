// 本文件的作用：提供方与模型的那一套描述性词汇——路由的显示名、模态、可配置的
// 提供方目录、模型发现请求、以及一条精确路由上解算出来的模型元数据。
//
// 源: packages/llm/llm/src/types.ts:143-282
//
// 这里全是**描述**，不是能力：谁都不靠它们决定一次请求发不发得出去。目录成员资格
// 是参考性的，不是请求校验——一个不在 [ModelInfo] 清单里的模型 id 照样能发，
// 因为提供方随时会上新，而适配器手上那份清单只是它当时知道的。

package llm

import "context"

// ProviderInfo 是一条登记过的提供方路由的显示元数据。
//
// 源: packages/llm/llm/src/types.ts:143-149
type ProviderInfo struct {
	// ID 是 [GenerateOptions].Provider 用的那个路由键。
	ID string
	// Name 是给选择器和诊断看的、人能读的提供方名字。
	Name string
}

// ModelModality 是一种提供方模型的输入模态。
//
// 源: packages/llm/llm/src/types.ts:151-158
//
// 新增: DSH 是 ModelModalityMap 加一个取值联合，那个 map 接口存在的唯一理由是
// 让插件用声明合并往里加模态。Go 没有声明合并，所以这里就是一个具名 string
// 类型：新模态由适配器直接写一个新的常量值，不需要先扩一个映射。这条和本包
// [ContentBlock] 那边的处理不一样——那边要留 Unknown 变体，是因为内容块会**写进
// 会话日志**，一个旧版本必须原样保管它读不懂的块；模态只在内存里活一次调用，
// 没人回读它，不认识的值原样带着就行。
type ModelModality string

const (
	// ModalityText 是文本输入。
	ModalityText ModelModality = "text"
	// ModalityImage 是图像输入。
	ModalityImage ModelModality = "image"
)

// ConfigurableProvider 是一条适配器插件**可以**靠配置激活的提供方路由，不管它当下
// 有没有被登记。
//
// 源: packages/llm/llm/src/types.ts:160-186
//
// 配置界面把这份目录和 [Runtime.ListProviders] 合起来，好把每一条可配置的提供方
// 连同它「已激活／还没激活」的状态一起摆出来。
type ConfigurableProvider struct {
	// Provider 是这一条配好之后激活的那个路由键。
	Provider string
	// DisplayName 是给配置界面看的、人能读的提供方名字。
	DisplayName string
	// SettingsNs 是配置这条提供方的那个用户设置命名空间。
	SettingsNs string
	// SettingsPath 是从那个命名空间的段落根出发、走到这条提供方那份档案对象的
	// 路径；整个段落就是那份档案时为空。
	SettingsPath []string
	// Declared 说明拥有它的那个适配器是不是**只**因为配置声明过才知道这条路由
	// ——一个网关或者自建服务器，适配器自己没带任何关于它的东西。
	//
	// nil 表示这个适配器不作这个区分；指向 false 表示它作这个区分、而这条是它
	// 自带的一条。只有适配器答得上来：从外面看，一条用户新加的路由和一条被用户
	// 改正过的自带路由，存下来的档案长得一模一样。
	//
	// 新增: DSH 是 declared?: boolean。这里是 *bool 而不是 bool，因为「不作区分」
	// 和「作区分、这条不是声明来的」是两件不同的事，都要说得出来。
	Declared *bool
}

// Clone 复制一份，好让交出去的那一份改不动这一份。
func (p ConfigurableProvider) Clone() ConfigurableProvider {
	clone := p
	if p.SettingsPath != nil {
		clone.SettingsPath = append([]string(nil), p.SettingsPath...)
	}
	if p.Declared != nil {
		declared := *p.Declared
		clone.Declared = &declared
	}
	return clone
}

// ModelDiscoveryRequest 是对一个配置还没存下来的提供方端点的一次问询。
//
// 源: packages/llm/llm/src/types.ts:188-211
//
// 配置界面送来的是用户正在编、还没保存的那份草稿，所以这个请求直接带着端点和
// 凭据，而不是点一条路由的名字：一条正在被添加的提供方还没有名字可点。
//
// 新增: DSH 那个 signal?: AbortSignal 字段在这里不存在——按本仓库一贯的规矩，
// 取消走 [Runtime.DiscoverModels] 的第一个 context.Context 参数。
type ModelDiscoveryRequest struct {
	// Provider 是这份草稿正在编的那条路由，草稿编的是一条已有路由时才有。
	//
	// 一条路由的适配器如果自己就知道它有哪些模型，就从那份知识里作答，不去问
	// 端点——适配器自己那份登记是更好的答案，而且一次网络往返都不用花。
	Provider string
	// BaseURL 是要问询的那个端点。可以没有，因为一条适配器本来就描述得出来的
	// 路由不需要它；描述不出来的那种必须给一个。
	BaseURL string
	// API 是这个端点说的那套线上协议，草稿点了名的话。
	API string
	// APIKey 是**只给这一次问询用**的凭据；本装置绝不把它存下来。
	APIKey string
}

// DiscoveredModel 是一个端点自报的一个模型。
//
// 源: packages/llm/llm/src/types.ts:213-226
//
// 除了 id 之外每个字段都是可缺的，因为绝大多数提供方的模型清单只吐一个 id、
// 别的什么都不说；界面采纳其中一条之后，它的适配器需要的那些容量还是欠着的。
//
// 新增: ContextWindow 和 MaxTokens 在 DSH 是可选数字，这里用普通的 int，0 表示
// 端点没披露。理由和本包 [TokenUsage] 那几个计数一样：一个「没说」和一个「说是 0」
// 对采纳它的人来说都是「这个数没法用」。
type DiscoveredModel struct {
	// ID 是这个端点认的模型 id。
	ID string
	// Name 是端点给了名字时的那个人能读的名字。
	Name string
	// ContextWindow 是披露出来的请求加响应合计上下文上限，没披露时为 0。
	ContextWindow int
	// MaxTokens 是披露出来的输出 token 上限，没披露时为 0。
	MaxTokens int
}

// ModelInfo 是一个适配器发现的模型。
//
// 源: packages/llm/llm/src/types.ts:228-243
//
// 目录成员资格是参考性的，不是请求校验：不在清单里的模型 id 照样发得出去。
type ModelInfo struct {
	// Provider 是拥有这条模型记录的那条提供方路由。
	Provider string
	// ID 是传给 [GenerateOptions].Model 的那个模型 id。
	ID string
	// Name 是给选择器看的、人能读的模型名。
	Name string
	// Description 是把它和其它长得差不多的模型区分开的那句话，可以没有。
	Description string
	// InputModalities 是它收的请求模态。
	//
	// nil 表示**不知道**；一份显式给出、但没列某个模态的清单是一条否定能力
	// ——比如一份只有 [ModalityText] 的清单，说的是「这个模型不收图」。
	// 这个区分是 [ProjectImagesForTextModel] 那条路的判据。
	InputModalities []ModelModality
}

// Clone 复制一份，好让交出去的那一份改不动这一份。
func (m ModelInfo) Clone() ModelInfo {
	clone := m
	if m.InputModalities != nil {
		clone.InputModalities = append([]ModelModality(nil), m.InputModalities...)
	}
	return clone
}

// ModelContext 是一条精确的提供方／模型路由上、提供方自己拥有的上下文容量。
//
// 源: packages/llm/llm/src/types.ts:245-249
type ModelContext struct {
	// ContextWindow 是请求加响应合计的 token 上限。
	ContextWindow int
}

// ReasoningEffortInfo 是一档适配器自己拥有的推理档位的显示元数据。
//
// 源: packages/llm/llm/src/types.ts:251-259
type ReasoningEffortInfo struct {
	// ID 是 [GenerateOptions].ReasoningEffort 认的那个不透明稳定值。
	ID ReasoningEffortID
	// Name 是给选择器和诊断看的、人能读的档位名。
	Name string
	// Description 是把它和其它长得差不多的档位区分开的那句话，可以没有。
	Description string
}

// ModelReasoningInfo 是一条精确的提供方／模型路由上可选的那些推理档位。
//
// 源: packages/llm/llm/src/types.ts:261-270
type ModelReasoningInfo struct {
	// Efforts 是支持的那些档位，按适配器自己偏好的展示次序排着。
	Efforts []ReasoningEffortInfo
	// DefaultEffort 是适配器配置的默认档位，调用方没点档位时把它落实进请求。
	// 空串表示保留提供方自己那个默认。
	DefaultEffort ReasoningEffortID
}

// Clone 复制一份，好让交出去的那一份改不动这一份。
func (r ModelReasoningInfo) Clone() ModelReasoningInfo {
	clone := r
	if r.Efforts != nil {
		clone.Efforts = append([]ReasoningEffortInfo(nil), r.Efforts...)
	}
	return clone
}

// ResolvedModelInfo 是拥有它的那个适配器在一条精确路由上解算出来的模型元数据。
//
// 源: packages/llm/llm/src/types.ts:272-280
//
// 新增: DSH 是 `extends LlmModelInfo`。Go 这边是内嵌 [ModelInfo]，行为一样：
// 字段直接提升上来，而一个 [ResolvedModelInfo] 也交得出它内嵌的那份 ModelInfo。
type ResolvedModelInfo struct {
	ModelInfo
	// Context 是提供方自己拥有的上下文容量，知道的话。nil 表示不知道。
	Context *ModelContext
	// DefaultMaxTokens 是适配器配置的每次请求输出上限，调用方没给上限时落实进去。
	// 0 表示没配。
	DefaultMaxTokens int
	// Reasoning 是适配器自己拥有的那些可选推理档位，暴露出来的话。nil 表示没有。
	Reasoning *ModelReasoningInfo
}

// Clone 复制一份，好让交出去的那一份改不动这一份。
func (r ResolvedModelInfo) Clone() ResolvedModelInfo {
	clone := r
	clone.ModelInfo = r.ModelInfo.Clone()
	if r.Context != nil {
		context := *r.Context
		clone.Context = &context
	}
	if r.Reasoning != nil {
		reasoning := r.Reasoning.Clone()
		clone.Reasoning = &reasoning
	}
	return clone
}

// ModelDiscovery 是一个适配器登记进来的模型发现实现。
//
// 源: packages/llm/llm/src/index.ts:314-317
//
// 新增: DSH 那边是一个内联的函数类型。这里给它一个名字，因为
// [Runtime.RegisterModelDiscovery] 的签名读起来会好很多，而且这是本仓库一贯的
// 做法（见 core/systemprompt 那边的 AssembleRule）。
type ModelDiscovery func(ctx context.Context, request ModelDiscoveryRequest) ([]DiscoveredModel, error)
