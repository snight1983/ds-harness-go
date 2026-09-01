// 本文件的作用：适配器那道接缝——一个提供方适配器**必须**实现的那一个方法、
// 它**可以**另外实现的那几件事、以及运行时替它兜底的那几个默认。
//
// 源: packages/llm/llm/src/index.ts:177-260
//
// 新增: DSH 那边是一个抽象基类 LlmAdapter：一个抽象方法 stream，另外五个带默认
// 实现的方法。Go 这边拆成「一个最小接口 + 五个可选接口 + 五个运行时侧的兜底函数」，
// 理由是**内嵌不是继承**：如果照抄成一个可内嵌的 BaseAdapter 结构体，
// BaseAdapter.PrepareCall 里那句 this.resolveModel 只会调到 BaseAdapter 自己那份
// ResolveModel，永远调不到外层类型覆盖的那一份——Go 的方法集没有虚派发。那不是
// 写法上的差别，是行为上的差别：DSH 的 prepareCall 默认实现明确要走覆盖后的
// resolveModel。把派发交给运行时（[AdapterPrepareCall] 去问 [AdapterResolveModel]，
// 后者再去问可选接口）之后，覆盖就重新生效了。

package llm

import (
	"context"
	"iter"
)

// Adapter 是把本装置的消息与流式词汇接到某一家提供方线上协议的那个东西。
//
// 源: packages/llm/llm/src/index.ts:187-275（LlmAdapter）
//
// 用 [Runtime.RegisterAdapter] 把它登记到若干条提供方路由上。**每一次发往提供方
// 的 HTTP 请求都必须带上 [AttributionHeaders] 给的那些头**；直发 HTTP 的适配器
// 在拼请求时加，走第三方库的适配器在库的请求头钩子里加。
type Adapter interface {
	// Stream 把一次模型调用按原始分块流出来。这是唯一必须实现的方法。
	//
	// 源: packages/llm/llm/src/index.ts:254-259
	//
	// 新增: DSH 返回 `AsyncIterable<StreamChunk>`，一次派发失败和一次迭代中途的
	// 失败在那边都表现为「抛出来」，只是抛出的时机不同。Go 这边分成两处：
	// 派发不出去（选路、建连、拼请求失败）交第二个返回值，流走到一半才失败的
	// 跟在那一块后面从序列里交出来。两处运行时都收得住，见 [Runtime.Stream]。
	// 这和本仓库 fs.FileSystem.StreamText 那条接缝是同一个形状。
	//
	// 取消走 ctx，实现必须在它取消之后尽快停下来。
	Stream(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error)
}

// ProviderDescriber 是一个适配器**可以**实现的：描述它拥有的某条提供方路由。
//
// 源: packages/llm/llm/src/index.ts:192-199
//
// 不实现时运行时兜底成 {ID: provider, Name: provider}，见 [AdapterProviderInfo]。
type ProviderDescriber interface {
	// ProviderInfo 描述一条路由的显示元数据，它的 ID 必须**恰好等于** provider。
	ProviderInfo(provider string) ProviderInfo
}

// RetryPolicyOwner 是一个适配器**可以**实现的：交出它随某条路由一起捕获的重试策略。
//
// 源: packages/llm/llm/src/index.ts:201-208
//
// 第二个返回值为假表示这条路由用普通默认，等价于 DSH 那边返回 undefined。
type RetryPolicyOwner interface {
	// ProviderRetryPolicy 交出这条路由自己拥有的重试策略。
	ProviderRetryPolicy(provider string) (ResolvedRetryPolicy, bool)
}

// ModelLister 是一个适配器**可以**实现的：列出它当下愿意为某条路由公告的模型。
//
// 源: packages/llm/llm/src/index.ts:210-219
//
// 结果是**参考性的**：适配器照样收得下清单里没有的模型 id，消费方绝不能把
// 「不在清单里」变成一次请求拒绝。
type ModelLister interface {
	// ListModels 按适配器自己偏好的次序列出模型。
	ListModels(ctx context.Context, provider string) ([]ModelInfo, error)
}

// ModelResolver 是一个适配器**可以**实现的：解算某一个确切模型的全部元数据。
//
// 源: packages/llm/llm/src/index.ts:221-236
//
// 这次问询和那份参考目录无关，也不校验请求路由。不实现时运行时兜底成
// {Provider: provider, ID: model, Name: model}，见 [AdapterResolveModel]。
type ModelResolver interface {
	// ResolveModel 交出这条精确路由上的身份，以及知道的上下文、调用默认、推理档位。
	ResolveModel(ctx context.Context, provider, model string) (ResolvedModelInfo, error)
}

// CallPreparer 是一个适配器**可以**实现的：把确切模型元数据和之后那次派发绑在
// 同一代上。
//
// 源: packages/llm/llm/src/index.ts:238-252
//
// 会随配置变化的适配器覆盖它，好让「准备」和「派发」之间的一次设置改动没法把
// 这一代的能力和另一代的端点凑到一起。不实现时运行时兜底成「解算一次模型 +
// 直接走 [Adapter.Stream]」，见 [AdapterPrepareCall]。
type CallPreparer interface {
	// PrepareCall 交出这一代的模型元数据和它那个一次性的流入口。
	PrepareCall(ctx context.Context, provider, model string) (PreparedAdapterCall, error)
}

// PreparedAdapterCall 是适配器某一代模型解算结果，绑着它最终那次流调用。
//
// 源: packages/llm/llm/src/index.ts:179-185（PreparedAdapterCall）
type PreparedAdapterCall struct {
	// Model 是和 Stream 同一代的那份确切模型元数据。
	Model ResolvedModelInfo
	// Stream 走那一代派发，不再重读任何动态连接事实。
	//
	// 为 nil 时运行时按 INVALID_ADAPTER 拒绝：一份准备好的调用发不出去，
	// 就不是一份准备好的调用。
	Stream func(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error)
}

// AdapterProviderInfo 问适配器要一条路由的显示元数据，它答不上来就兜底。
//
// 源: packages/llm/llm/src/index.ts:197-199
func AdapterProviderInfo(adapter Adapter, provider string) ProviderInfo {
	if describer, ok := adapter.(ProviderDescriber); ok {
		return describer.ProviderInfo(provider)
	}
	return ProviderInfo{ID: provider, Name: provider}
}

// AdapterRetryPolicy 问适配器要一条路由自己的重试策略。第二个返回值为假表示
// 用普通默认。
//
// 源: packages/llm/llm/src/index.ts:206-208
func AdapterRetryPolicy(adapter Adapter, provider string) (ResolvedRetryPolicy, bool) {
	if owner, ok := adapter.(RetryPolicyOwner); ok {
		return owner.ProviderRetryPolicy(provider)
	}
	return ResolvedRetryPolicy{}, false
}

// AdapterListModels 问适配器要它为某条路由公告的模型，它答不上来就是空清单。
//
// 源: packages/llm/llm/src/index.ts:217-219
func AdapterListModels(ctx context.Context, adapter Adapter, provider string) ([]ModelInfo, error) {
	if lister, ok := adapter.(ModelLister); ok {
		return lister.ListModels(ctx, provider)
	}
	return nil, nil
}

// AdapterResolveModel 问适配器要一个确切模型的元数据，它答不上来就兜底成身份三件套。
//
// 源: packages/llm/llm/src/index.ts:230-236
func AdapterResolveModel(ctx context.Context, adapter Adapter, provider, model string) (ResolvedModelInfo, error) {
	if resolver, ok := adapter.(ModelResolver); ok {
		return resolver.ResolveModel(ctx, provider, model)
	}
	return ResolvedModelInfo{ModelInfo: ModelInfo{Provider: provider, ID: model, Name: model}}, nil
}

// AdapterPrepareCall 问适配器要一次绑代的调用，它答不上来就解算一次模型、
// 并把派发直接接到 [Adapter.Stream] 上。
//
// 源: packages/llm/llm/src/index.ts:247-252
//
// 兜底这一支走的是 [AdapterResolveModel] 而不是本文件里那份内联默认——这正是
// 本文件开头说的那件事：DSH 的默认 prepareCall 调的是**被覆盖之后**的
// resolveModel，只有把这一跳也交给运行时，覆盖才落得到实处。
func AdapterPrepareCall(ctx context.Context, adapter Adapter, provider, model string) (PreparedAdapterCall, error) {
	if preparer, ok := adapter.(CallPreparer); ok {
		return preparer.PrepareCall(ctx, provider, model)
	}
	resolved, err := AdapterResolveModel(ctx, adapter, provider, model)
	if err != nil {
		return PreparedAdapterCall{}, err
	}
	return PreparedAdapterCall{Model: resolved, Stream: adapter.Stream}, nil
}
