// 本文件的作用：一次调用从备好到发出去——备好的那份句柄，以及流式请求怎么落到
// 具体适配器上、失败怎么变成一个分片。
//
// 源: packages/llm/llm/src/index.ts:262-1026

package llm

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sync"
)

// PreparedCall 是一次配置和适配器登记一起解算出来的模型调用。
//
// 源: packages/llm/llm/src/index.ts:157-177（PreparedLlmCall）
//
// 新增: DSH 交出来的是一个 Object.freeze 过的字面量。Go 这边是一个字段全不导出、
// 只给读取方法的指针类型——访问器就是 Object.freeze 在 Go 里的等价物：拿到它的人
// 读得到每一样,但改不动任何一样。
type PreparedCall struct {
	runtime         *Runtime
	config          CallConfig
	retryPolicy     ResolvedRetryPolicy
	modelContext    *ModelContext
	inputModalities []ModelModality
	adapterDefaults CallConfigAdapterDefaults
	dispatch        preparedDispatch

	mutex      sync.Mutex
	dispatched bool
}

// Config 交出那份摘下来的、适配器默认已经落实进去的配置。
//
// 源: packages/llm/llm/src/index.ts:157-158
func (p *PreparedCall) Config() CallConfig { return p.config.Clone() }

// RetryPolicy 交出随适配器登记一起捕获的那份重试策略。
//
// 源: packages/llm/llm/src/index.ts:159-160
func (p *PreparedCall) RetryPolicy() ResolvedRetryPolicy { return p.retryPolicy }

// ModelContext 交出随这次登记一起解算出来的上下文容量。第二个返回值为假表示不知道。
//
// 源: packages/llm/llm/src/index.ts:161-162
func (p *PreparedCall) ModelContext() (ModelContext, bool) {
	if p.modelContext == nil {
		return ModelContext{}, false
	}
	return *p.modelContext, true
}

// InputModalities 交出随这一代适配器派发一起捕获的确切模型模态。
// 为 nil 表示适配器没说。
//
// 源: packages/llm/llm/src/index.ts:163-164
func (p *PreparedCall) InputModalities() []ModelModality {
	if p.inputModalities == nil {
		return nil
	}
	return slices.Clone(p.inputModalities)
}

// AdapterDefaults 说明生效配置里哪几个字段是被捕获的那个适配器落实的，
// 而不是调用方提议的。
//
// 源: packages/llm/llm/src/index.ts:165-166
func (p *PreparedCall) AdapterDefaults() CallConfigAdapterDefaults { return p.adapterDefaults }

// Stream 通过准备时捕获的那份登记，把这次调用派发出去，**只能派发一次**。
//
// 源: packages/llm/llm/src/index.ts:850-867
//
// 请求里那几个调用配置字段必须和 [PreparedCall.Config] 一致；重用或者对不上
// 都以 INVALID_PREPARED_CALL 失败。
func (p *PreparedCall) Stream(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
	p.mutex.Lock()
	if p.dispatched {
		p.mutex.Unlock()
		return nil, NewError("a prepared LLM call can only be dispatched once", InvalidPreparedCallCode, nil)
	}
	if !CallConfigEquals(options.CallConfig(), p.config) {
		p.mutex.Unlock()
		return nil, NewError("prepared LLM call config changed before adapter dispatch", InvalidPreparedCallCode, nil)
	}
	p.dispatched = true
	p.mutex.Unlock()
	return p.runtime.streamWithRegistration(ctx, options, &p.dispatch)
}

// PrepareCall 在某条路由当下那份适配器登记上解算一次调用。
//
// 源: packages/llm/llm/src/index.ts:816-869
//
// 交出来的那个一次性句柄从头到尾攥着同一份登记，所以热更新没法把一个适配器的
// 能力结果和另一个适配器凑到一起。
func (r *Runtime) PrepareCall(ctx context.Context, config CallConfig) (*PreparedCall, error) {
	registration, err := r.registration(config.Provider)
	if err != nil {
		return nil, err
	}
	adapterCall, err := AdapterPrepareCall(ctx, registration.adapter, config.Provider, config.Model)
	if err != nil {
		return nil, err
	}
	if adapterCall.Stream == nil {
		return nil, NewError(
			fmt.Sprintf("adapter returned a prepared call without a stream entry point for provider %q", config.Provider),
			InvalidAdapterCode, nil)
	}
	modelInfo, err := normalizeModelInfo(registration.provider.ID, config.Model, adapterCall.Model)
	if err != nil {
		return nil, err
	}
	resolvedConfig, err := resolveCallWithInfo(config, modelInfo)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedCall{
		runtime:         r,
		config:          resolvedConfig.Clone(),
		retryPolicy:     registration.retryPolicy,
		inputModalities: modelInfo.InputModalities,
		adapterDefaults: CallConfigAdapterDefaults{
			ReasoningEffort: config.ReasoningEffort == "" && resolvedConfig.ReasoningEffort != "",
			MaxTokens:       config.MaxTokens == 0 && resolvedConfig.MaxTokens != 0,
		},
		dispatch: preparedDispatch{
			registration: registration,
			config:       resolvedConfig,
			modelInfo:    modelInfo,
			dispatch:     adapterCall.Stream,
		},
	}
	if modelInfo.Context != nil {
		modelContext := *modelInfo.Context
		prepared.modelContext = &modelContext
	}
	return prepared, nil
}

// ---- 流式调用 ----

// Stream 把一次模型调用按原始分块（token 级增量）流出来。
//
// 源: packages/llm/llm/src/index.ts:974-987
//
// 只有同一个适配器实例同时拥有一条重放状态的历史路由和这次的目标路由时，
// 那份重放状态才留得下来。适配器选择在整个异步的确切模型解算和派发期间都是钉死的。
// **适配器选择、派发、迭代三处的失败都变成终止的 error 或者 aborted 结束分块**；
// 中间件、嵌套调用、清理、以及下游消费方的失败照样报出来。
//
// 新增: 返回值第二格是 llm/stream 瀑布上某一层**构造时**的失败。适配器那边的
// 失败一律不走这里——它们是分块。
func (r *Runtime) Stream(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
	return r.streamWithRegistration(ctx, options, nil)
}

// streamWithRegistration 跑 llm/stream 那条瀑布，最里面是适配器边界。
//
// 源: packages/llm/llm/src/index.ts:989-999
func (r *Runtime) streamWithRegistration(
	ctx context.Context,
	options GenerateOptions,
	prepared *preparedDispatch,
) (iter.Seq2[StreamChunk, error], error) {
	r.mutex.Lock()
	var rules []StreamRule
	for rule := range r.streamRules.Values() {
		rules = append(rules, rule)
	}
	fail := r.fail
	r.mutex.Unlock()

	var walk func(index int, ctx context.Context) (iter.Seq2[StreamChunk, error], error)
	walk = func(index int, ctx context.Context) (iter.Seq2[StreamChunk, error], error) {
		if index >= len(rules) {
			return r.adapterStream(ctx, options, prepared), nil
		}
		return rules[index](ctx, options, func(ctx context.Context) (iter.Seq2[StreamChunk, error], error) {
			return walk(index+1, ctx)
		})
	}
	stream, err := walk(0, ctx)
	if err != nil || fail == nil {
		return stream, err
	}
	// 文法检查裹在**最外面**：它要验的是消费方最终看到的那条流，一层中间件把流
	// 改坏了同样得被抓住。这就是 DSH 那个 prepend: true 的意思，见 [RegisterInvariants]。
	return validateStream(stream, fail), nil
}

// adapterStream 是最后那道适配器边界。适配器选择、派发、迭代器构造、以及迭代
// 中途的失败，全都变成一个终止失败分块。中间件和下游消费方的失败照旧报出去。
//
// 源: packages/llm/llm/src/index.ts:893-972
//
// 新增: DSH 那边是一个 async generator，函数体在第一次 next() 之前一行都不跑。
// Go 这边由 [iter.Seq2] 的闭包做到同一件事：所有的选择与派发都写在 yield 循环
// **里面**，所以一层中间件拿到这个序列而不去 range 它的话，一次提供方往返都不会
// 发生。
//
// 新增: DSH 那个 finally 里的 iterator.return?.() 在 Go 里没有对应物——range-over-func
// 的协议本身就保证消费方提前 break 时，内层序列的 defer 会跑到。
//
// 新增: 用掉之后再 range 一次什么都不出，和一个已经跑完的 async generator 再被
// for-await 一次的行为一样。这不是并发安全的类型：一条流由一个 goroutine 从头
// 读到尾。
func (r *Runtime) adapterStream(
	ctx context.Context,
	options GenerateOptions,
	prepared *preparedDispatch,
) iter.Seq2[StreamChunk, error] {
	spent := false
	return func(yield func(StreamChunk, error) bool) {
		if spent {
			return
		}
		spent = true
		stream, err := r.dispatchStream(ctx, options, prepared)
		if err != nil {
			yield(adapterFailureChunk(ctx, err), nil)
			return
		}
		for chunk, chunkErr := range stream {
			if chunkErr != nil {
				yield(adapterFailureChunk(ctx, chunkErr), nil)
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

// dispatchStream 选适配器、解算确切模型、落实配置、投影图片，最后把请求交出去。
//
// 源: packages/llm/llm/src/index.ts:902-938
//
// 这里报的每一条错都由 [Runtime.adapterStream] 变成一个终止失败分块。
func (r *Runtime) dispatchStream(
	ctx context.Context,
	options GenerateOptions,
	prepared *preparedDispatch,
) (iter.Seq2[StreamChunk, error], error) {
	registration := (*adapterRegistration)(nil)
	if prepared != nil {
		registration = prepared.registration
	} else {
		selected, err := r.registration(options.Provider)
		if err != nil {
			return nil, err
		}
		registration = selected
	}
	adapter := registration.adapter

	var modelInfo ResolvedModelInfo
	var resolvedConfig CallConfig
	var dispatch func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error)
	if prepared == nil {
		adapterCall, err := AdapterPrepareCall(ctx, adapter, options.Provider, options.Model)
		if err != nil {
			return nil, err
		}
		if adapterCall.Stream == nil {
			return nil, NewError(
				fmt.Sprintf("adapter returned a prepared call without a stream entry point for provider %q", options.Provider),
				InvalidAdapterCode, nil)
		}
		modelInfo, err = normalizeModelInfo(registration.provider.ID, options.Model, adapterCall.Model)
		if err != nil {
			return nil, err
		}
		resolvedConfig, err = resolveCallWithInfo(options.CallConfig(), modelInfo)
		if err != nil {
			return nil, err
		}
		dispatch = adapterCall.Stream
	} else {
		modelInfo = prepared.modelInfo
		resolvedConfig = prepared.config
		dispatch = prepared.dispatch
	}

	matched := CallConfigEquals(options.CallConfig(), resolvedConfig)
	if prepared != nil && !matched {
		// 走不到：[PreparedCall.Stream] 进来之前先拿同一个判据比过一次，而
		// 一条 [StreamRule] 只按值收到 options、没法改它。这一句拦的是本包自己
		// 以后开出别的入口——一次配置漂移意味着适配器收到的请求和它准备时
		// 说好的那次不是一回事。
		return nil, NewError("prepared LLM call config changed before adapter dispatch", InvalidPreparedCallCode, nil)
	}
	resolved := options
	if !matched {
		resolved = options.withCallConfig(resolvedConfig)
	}
	// InputModalities 为 nil 表示适配器没说，那就不投影；一份**显式**列出来、
	// 却没有图片的清单是一条否定能力，这时候才把历史里的图片投影成文本。
	if modelInfo.InputModalities != nil && !slices.Contains(modelInfo.InputModalities, ModalityImage) &&
		messagesHaveImage(resolved.Messages) {
		resolved.Messages = ProjectImagesForTextModel(resolved.Messages)
	}
	return dispatch(ctx, r.forAdapter(resolved, adapter))
}

// messagesHaveImage 判这串消息里有没有图片。
//
// 源: packages/llm/llm/src/index.ts:932
func messagesHaveImage(messages []Message) bool {
	for _, message := range messages {
		if ContentHasImage(message.Content) {
			return true
		}
	}
	return false
}

// withCallConfig 把一份解算好的调用配置盖回请求上。
//
// 源: packages/llm/llm/src/index.ts:925-929（`{...options, ...resolvedConfig}`）
//
// 新增: DSH 靠对象展开一次盖过去。Go 这边要逐个字段写，因为 [CallConfig] 和
// [GenerateOptions] 是两个标称类型——这也正是 [GenerateOptions.CallConfig] 存在的
// 理由，两边成对。
func (o GenerateOptions) withCallConfig(config CallConfig) GenerateOptions {
	o.Provider = config.Provider
	o.Model = config.Model
	o.ReasoningEffort = config.ReasoningEffort
	o.Temperature = config.Temperature
	o.MaxTokens = config.MaxTokens
	o.Stop = config.Stop
	return o
}

// forAdapter 摘掉那些历史路由归别的适配器所有的重放状态。
//
// 源: packages/llm/llm/src/index.ts:877-891
//
// 新增: 这里拿两个 [Adapter] 接口值直接比较，所以**适配器实现必须是可比较的
// 类型**——实践中就是指针接收者，本仓库每一个服务实现都是。一个直接以不可比较
// 的结构体值（带 map 或者切片字段）作为适配器登记进来的类型会让这次比较 panic。
// DSH 那边比的是对象引用，天然做得到；Go 这里把这条要求写出来。
func (r *Runtime) forAdapter(options GenerateOptions, adapter Adapter) GenerateOptions {
	changed := false
	messages := make([]Message, len(options.Messages))
	for index, message := range options.Messages {
		messages[index] = message
		if message.Role != RoleAssistant {
			continue
		}
		source, isModel := message.ModelSource()
		if !isModel || source.ReplayState == nil {
			continue
		}
		r.mutex.Lock()
		owner, present := r.adapters[source.Provider]
		r.mutex.Unlock()
		if present && owner.adapter == adapter {
			continue
		}
		stripped := message.Clone()
		stripped.Source = ModelSource{Provenance: Provenance{
			Provider: source.Provider,
			Model:    source.Model,
		}}
		messages[index] = stripped
		changed = true
	}
	if !changed {
		return options
	}
	options.Messages = messages
	return options
}

// adapterFailureChunk 把适配器那边的一次失败，变成流协议里的终止结局。
//
// 源: packages/llm/llm/src/index.ts:1002-1011
//
// 新增: DSH 看的是 options.signal?.aborted，Go 这边看的是 ctx.Err()——同一件事，
// 走的是本仓库统一的那个取消口。
func adapterFailureChunk(ctx context.Context, err error) StreamChunk {
	failure := NormalizeFailure(err)
	if ctx.Err() != nil || failure.Code == AbortedCode {
		return FinishChunk{Reason: AbortedFinish{Failure: failure}}
	}
	return FinishChunk{Reason: ErrorFinish{Failure: failure}}
}
