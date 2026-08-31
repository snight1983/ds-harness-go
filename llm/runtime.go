// 本文件的作用：抽象的 llm 服务本体——一张适配器注册表、一份可配置提供方目录、
// 一组模型发现，加上那个能被 llm/stream 瀑布拦截的流式模型调用入口。
//
// 源: packages/llm/llm/src/index.ts:262-1026
//
// 新增: DSH 那边 LlmRuntime extends Service，登记走 ctx.effect、通知走 cordis 事件、
// 瀑布走 ctx.waterfall。Go 这边一样也不照抄：生命周期挂在显式传进来的
// [ds-harness-go/core/scope.Scope] 上，通知是一张显式的观察者表，瀑布是一张
// 有序的规则表加一次递归下钻——和本仓库 core/agent、core/session 完全一致。

package llm

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sync"

	"ds-harness-go/core/scope"
	"ds-harness-go/invariants"
)

// 这些是本运行时自己会挂出来的失败码。它们和 [ContextWindowExceededCode] 那一族
// 不同：那一族说的是**提供方**那边出了什么事，这一族说的是**调用方或者适配器**
// 把本装置的约定用错了。
//
// 源: packages/llm/llm/src/index.ts:373-1010
const (
	// InvalidAdapterCode 表示这次适配器登记本身不成立：一条路由都没给、
	// 路由名是空串、或者适配器交回来的路由元数据不合格。
	//
	// 源: packages/llm/llm/src/index.ts:373、405、411
	InvalidAdapterCode = "INVALID_ADAPTER"

	// DuplicateAdapterCode 表示要登记的某条路由已经有别的适配器了。
	//
	// 源: packages/llm/llm/src/index.ts:407
	DuplicateAdapterCode = "DUPLICATE_ADAPTER"

	// RegistrationDisposedCode 表示一次已经释放的登记还想再改自己那份名单。
	//
	// 源: packages/llm/llm/src/index.ts:389、506
	RegistrationDisposedCode = "REGISTRATION_DISPOSED"

	// InvalidDirectoryCode 表示这次可配置提供方声明本身不成立。
	//
	// 源: packages/llm/llm/src/index.ts:473、476、492
	InvalidDirectoryCode = "INVALID_DIRECTORY"

	// DuplicateDirectoryCode 表示某条可配置提供方已经被声明过了。
	//
	// 源: packages/llm/llm/src/index.ts:480
	DuplicateDirectoryCode = "DUPLICATE_DIRECTORY"

	// InvalidDiscoveryCode 表示这次模型发现登记或者问询本身不成立。
	//
	// 源: packages/llm/llm/src/index.ts:537、570
	InvalidDiscoveryCode = "INVALID_DISCOVERY"

	// DuplicateDiscoveryCode 表示这个设置命名空间已经有模型发现了。
	//
	// 源: packages/llm/llm/src/index.ts:540
	DuplicateDiscoveryCode = "DUPLICATE_DISCOVERY"

	// NoDiscoveryCode 表示这个设置命名空间没登记过模型发现。
	//
	// 源: packages/llm/llm/src/index.ts:565
	NoDiscoveryCode = "NO_DISCOVERY"

	// NoAdapterCode 表示这条提供方路由上没有适配器。
	//
	// 源: packages/llm/llm/src/index.ts:873
	NoAdapterCode = "NO_ADAPTER"

	// InvalidCatalogCode 表示适配器交回来的参考目录里有不合格或者重复的条目。
	//
	// 源: packages/llm/llm/src/index.ts:623
	InvalidCatalogCode = "INVALID_CATALOG"

	// InvalidModelInfoCode 表示适配器交回来的确切模型身份不合格。
	//
	// 源: packages/llm/llm/src/index.ts:681
	InvalidModelInfoCode = "INVALID_MODEL_INFO"

	// InvalidModelContextCode 表示适配器交回来的上下文容量不合格。
	//
	// 源: packages/llm/llm/src/index.ts:688
	InvalidModelContextCode = "INVALID_MODEL_CONTEXT"

	// InvalidModelMaxTokensCode 表示适配器交回来的默认输出上限不合格。
	//
	// 源: packages/llm/llm/src/index.ts:699
	InvalidModelMaxTokensCode = "INVALID_MODEL_MAX_TOKENS"

	// InvalidModelReasoningCode 表示适配器交回来的推理档位元数据不合格。
	//
	// 源: packages/llm/llm/src/index.ts:716、731、744
	InvalidModelReasoningCode = "INVALID_MODEL_REASONING"

	// UnsupportedReasoningEffortCode 表示这条精确路由不支持那个推理档位。
	//
	// 源: packages/llm/llm/src/index.ts:794、803
	UnsupportedReasoningEffortCode = "UNSUPPORTED_REASONING_EFFORT"

	// InvalidPreparedCallCode 表示一份准备好的调用被用了第二次，或者它的配置在
	// 派发之前被改过。
	//
	// 源: packages/llm/llm/src/index.ts:852、857、922
	InvalidPreparedCallCode = "INVALID_PREPARED_CALL"

	// AbortedCode 是适配器用来说「这次是被取消的，不是失败」的那个码。
	//
	// 源: packages/llm/llm/src/index.ts:1007
	//
	// 新增: DSH 那边它只是 index.ts 里的一个字面量。这里立成常量，因为适配器
	// 要照着挂这个码，而一个只在别人源码里出现过的字符串没法被当成约定。
	AbortedCode = "ABORTED"
)

// AdaptersUpdatedObserver 是适配器拓扑变动时被叫到的观察者。
//
// 源: packages/llm/llm/src/index.ts:329（cordis 事件 llm/adapters-updated）
//
// 它**没有否决权**：叫到它的时候那次改动已经提交了。
type AdaptersUpdatedObserver func()

// StreamRule 是 llm/stream 瀑布上的一层。
//
// 源: packages/llm/llm/src/index.ts:993-998
//
// next 走向更里面那一层，最里面是适配器边界本身。先登记的在外面——这条次序和
// 本仓库每一条瀑布一致。
//
// 新增: DSH 的 ctx.waterfall 是在 LlmRuntime 上派发的，而它不是一个作用域载体，
// 所以 dsh-scope 那层按作用域过滤的覆盖落不下来，**每一个**登记过的监听器都会跑。
// 这就是这里用一张平的有序表、而不是用 [scope.Layers] 的原因：分层表按载体过滤，
// 会把登记在具名作用域上的监听器藏起来，那是另一种行为。
type StreamRule func(
	ctx context.Context,
	options GenerateOptions,
	next func(context.Context) (iter.Seq2[StreamChunk, error], error),
) (iter.Seq2[StreamChunk, error], error)

// adapterRegistration 是一条路由上活着的那份登记。
//
// 源: packages/llm/llm/src/index.ts:1013-1017
type adapterRegistration struct {
	adapter     Adapter
	provider    ProviderInfo
	retryPolicy ResolvedRetryPolicy
}

// preparedDispatch 是一次已经绑好代的派发，[Runtime.PrepareCall] 造，
// 适配器边界用。
//
// 源: packages/llm/llm/src/index.ts:1019-1024
type preparedDispatch struct {
	registration *adapterRegistration
	config       CallConfig
	modelInfo    ResolvedModelInfo
	dispatch     func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error)
}

// RuntimeOptions 是造一个运行时时给的东西。
type RuntimeOptions struct {
	// Logger 是观察者失败时留下诊断的地方，为 nil 时用 slog.Default()。
	Logger *slog.Logger
}

// Runtime 是抽象的 llm 服务：一张适配器注册表加一个流式模型调用 API。
//
// 源: packages/llm/llm/src/index.ts:307-1000
//
// 新增: 三张表加两张观察者表都由一把互斥锁护着——DSH 是单线程 JS，不需要。
// 观察者一律在锁**外面**叫，理由和 core/agent 那边逐字相同：一个观察者反手来问
// [Runtime.ListProviders] 是完全正当的，在锁里叫它就是自锁。
type Runtime struct {
	logger *slog.Logger

	mutex sync.Mutex
	// adapters 是路由键到活登记的映射，adapterOrder 是它们的登记次序。
	//
	// 新增: DSH 用一个 JS Map，它自己保插入顺序，而 [Runtime.ListProviders] 明写
	// 「按登记次序」。Go 的 map 不保任何顺序，所以这个次序数组是必须的——和
	// core/agent 的 Registry 那份 order 出于同一个理由。
	adapters     map[string]*adapterRegistration
	adapterOrder []string
	// directory 与 directoryOrder 同理，次序是声明次序。
	directory      map[string]ConfigurableProvider
	directoryOrder []string
	// discoveries 按设置命名空间存模型发现，它没有次序可言（只按键取）。
	discoveries map[string]ModelDiscovery

	// observers 是拓扑观察者，streamRules 是 llm/stream 瀑布的各层。
	observers   *scope.AnonymousEntries[AdaptersUpdatedObserver]
	streamRules *scope.AnonymousEntries[StreamRule]

	// fail 是本包那份不变量检查装上之后留下的报告口，没装时为 nil。
	// 由 [RegisterInvariants] 写，见 llm/invariant.go。
	//
	// 新增: DSH 那边这份检查是靠 ctx.on('llm/stream', ..., {prepend: true}) 挂上去的，
	// 也就是往瀑布最外面插一层。这里给它一个专门的位置而不是往 streamRules 里塞，
	// 因为 [scope.AnonymousEntries] 只能追加到最里面，而这份检查**必须**在最外面：
	// 它要验的是消费方最终看到的那条流，不是适配器吐出来的那条。这和 fs 包
	// 让 [ds-harness-go/fs.Policy] 自己攥着一个 fail 字段是同一个搬法。
	fail invariants.Fail
}

// NewRuntime 造一个空的运行时。
func NewRuntime(options RuntimeOptions) *Runtime {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		logger:      logger,
		adapters:    map[string]*adapterRegistration{},
		directory:   map[string]ConfigurableProvider{},
		discoveries: map[string]ModelDiscovery{},
		observers:   scope.NewAnonymousEntries[AdaptersUpdatedObserver](),
		streamRules: scope.NewAnonymousEntries[StreamRule](),
	}
}

// ---- 观察与拦截 ----

// OnAdaptersUpdated 登记一个拓扑观察者，返回撤销这次登记的函数。
//
// 源: packages/llm/llm/src/index.ts:329
func (r *Runtime) OnAdaptersUpdated(
	ctx context.Context,
	owner *scope.Scope,
	observer AdaptersUpdatedObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnAdaptersUpdated 需要一个观察者", ErrInvalidConfig)
	}
	return r.attach(owner, "llm.OnAdaptersUpdated()", func() func() {
		return r.observers.Append(observer)
	})
}

// OnStream 往 llm/stream 瀑布上加一层，返回撤销这次登记的函数。
//
// 源: packages/llm/llm/src/index.ts:993-998
func (r *Runtime) OnStream(
	ctx context.Context,
	owner *scope.Scope,
	rule StreamRule,
) (func(context.Context) error, error) {
	if rule == nil {
		return nil, fmt.Errorf("%w：OnStream 需要一条规则", ErrInvalidConfig)
	}
	return r.attach(owner, "llm.OnStream()", func() func() {
		return r.streamRules.Append(rule)
	})
}

// attach 是上面两个登记共用的那段「登记 + 把撤销挂到 owner 上」。
//
// 挂不上去（owner 已经释放）就把刚登记的那份撤掉再报错：静默接受的话这份登记
// 永远没人负责撤销，也就是泄漏了。这条和 [scope.Layers.Effect] 的处理一致。
func (r *Runtime) attach(owner *scope.Scope, label string, register func() func()) (func(context.Context) error, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：%s 需要一个持有它的作用域", ErrInvalidConfig, label)
	}
	undo := register()
	dispose, err := owner.Defer(label, func(context.Context) error {
		undo()
		return nil
	})
	if err != nil {
		undo()
		return nil, err
	}
	return dispose, nil
}

// emitAdaptersUpdated 通知拓扑观察者，不让一个坏掉的监听器否决已经提交的改动。
//
// 源: packages/llm/llm/src/index.ts:323-355
//
// 三条规则和 [ds-harness-go/credentials.Notifier] 的 fanOut 逐字相同：每一个都跑到、
// 普通失败只记日志、不变量违例留第一条等跑完再抛。
//
// 新增: DSH 还有一支专门接住「监听器是个 async 函数、它返回的 Promise 被拒绝」的
// 情况。Go 里没有这一支——观察者是同步的，一次失败只会是 panic，而 panic 一定
// 发生在这里的调用栈上。
//
// **必须在锁外面调。**
func (r *Runtime) emitAdaptersUpdated() {
	var observers []AdaptersUpdatedObserver
	r.mutex.Lock()
	for observer := range r.observers.Values() {
		observers = append(observers, observer)
	}
	fail, violation := r.fail, r.registryViolation()
	r.mutex.Unlock()

	// 在锁外面报：fail 是 panic，在临界区里抛会把这把锁永远留在锁着的状态。
	if fail != nil && violation != "" {
		fail(violation)
	}

	var invariantFailure *invariants.Error
	for _, observer := range observers {
		func() {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if failure, isInvariant := recovered.(*invariants.Error); isInvariant {
					if invariantFailure == nil {
						invariantFailure = failure
					}
					return
				}
				r.logger.Warn("llm: 一个 llm/adapters-updated 观察者失败了",
					slog.Any("panic", recovered))
			}()
			observer()
		}()
	}
	if invariantFailure != nil {
		panic(invariantFailure)
	}
}

// ---- 适配器登记 ----

// AdapterRegistration 是一次活着的适配器登记：能释放，也能原子地换掉整份路由名单。
//
// 源: packages/llm/llm/src/index.ts:262-284
type AdapterRegistration struct {
	runtime *Runtime
	adapter Adapter
	dispose func(context.Context) error

	// owned 是这次登记当下攥着的那些路由，Replace 会重写它。
	// released 说明释放跑过没有——owned 为空说明不了这件事，因为
	// Replace(nil) 合法地留下一次「活着但一条路由都没有」的登记。
	//
	// 这两个字段由 runtime.mutex 护着，和那三张表用同一把锁。
	owned    []string
	released bool
}

// RegisterAdapter 把一个适配器登记到给定的那些提供方路由上。
//
// 源: packages/llm/llm/src/index.ts:356-394
//
// 全有或者全无：任何一条路由已经有适配器了，整次登记以 DUPLICATE_ADAPTER 失败，
// 注册表一个字都不动。owner 释放时这次登记跟着释放。
func (r *Runtime) RegisterAdapter(
	ctx context.Context,
	owner *scope.Scope,
	providers []string,
	adapter Adapter,
) (*AdapterRegistration, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterAdapter 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if adapter == nil {
		return nil, NewError("llm: RegisterAdapter 需要一个适配器", InvalidAdapterCode, nil)
	}
	if len(providers) == 0 {
		return nil, NewError("an adapter must register at least one provider", InvalidAdapterCode, nil)
	}
	handle := &AdapterRegistration{runtime: r, adapter: adapter}

	r.mutex.Lock()
	prepared, err := r.prepareRoutes(providers, adapter, nil)
	if err != nil {
		r.mutex.Unlock()
		return nil, err
	}
	r.commitRoutes(handle, prepared)
	r.mutex.Unlock()
	r.emitAdaptersUpdated()

	dispose, err := owner.Defer("llm.RegisterAdapter()", func(context.Context) error {
		handle.release()
		return nil
	})
	if err != nil {
		handle.release()
		return nil, err
	}
	handle.dispose = dispose
	return handle, nil
}

// Release 放掉这次登记当下攥着的每一条路由。重复调用没有额外效果。
//
// 源: packages/llm/llm/src/index.ts:267、375-380
func (h *AdapterRegistration) Release(ctx context.Context) error {
	return h.dispose(ctx)
}

// Replace 把这次登记的路由名单原子地换成 providers，适配器实例不变。
//
// 源: packages/llm/llm/src/index.ts:269-283、385-392
//
// 候选名单先整份验过——和别的适配器撞车、名字是空串、或者路由元数据不合格，
// 都会报错并且**原样留下当下那份名单**；换的那一下是一段临界区，所以没有任何
// 请求看得见中间的空档。这里空名单是合法的（一段被清空的配置持有零条路由、
// 但登记还活着），初次登记不行。
//
// 这次登记已经释放之后再调，报 REGISTRATION_DISPOSED：它的路由已经没了、
// 它的释放也已经跑过，此刻再放进去的东西不会有人负责摘出来。
func (h *AdapterRegistration) Replace(providers []string) error {
	r := h.runtime
	r.mutex.Lock()
	if h.released {
		r.mutex.Unlock()
		return NewError("a disposed adapter registration cannot replace its routes", RegistrationDisposedCode, nil)
	}
	prepared, err := r.prepareRoutes(providers, h.adapter, h.owned)
	if err != nil {
		r.mutex.Unlock()
		return err
	}
	r.commitRoutes(h, prepared)
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
	return nil
}

// release 是释放那一下的本体，[AdapterRegistration.Release] 和 owner 释放共用它。
func (h *AdapterRegistration) release() {
	r := h.runtime
	r.mutex.Lock()
	if h.released {
		// 走不到，理由同 [DirectoryRegistration.withdraw] 里那一句：
		// 进这个函数的两条路都经由 core/scope 那份只跑一次的撤销。
		r.mutex.Unlock()
		return
	}
	h.released = true
	r.dropRoutes(h.owned)
	h.owned = nil
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
}

// prepareRoutes 验一份候选路由名单，把这次登记已经攥着的那些当成可用。
//
// 源: packages/llm/llm/src/index.ts:396-423
//
// 一个字都不写：候选被拒之后注册表和进来时一模一样。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) prepareRoutes(providers []string, adapter Adapter, owned []string) ([]*adapterRegistration, error) {
	unique := map[string]struct{}{}
	registrations := make([]*adapterRegistration, 0, len(providers))
	for _, provider := range providers {
		if provider == "" {
			return nil, NewError("adapter provider names must be non-empty", InvalidAdapterCode, nil)
		}
		_, seen := unique[provider]
		_, taken := r.adapters[provider]
		if seen || (taken && !slices.Contains(owned, provider)) {
			return nil, NewError(
				fmt.Sprintf("an adapter for provider %q is already registered", provider),
				DuplicateAdapterCode, nil)
		}
		info := AdapterProviderInfo(adapter, provider)
		if info.ID != provider || info.Name == "" {
			return nil, NewError(
				fmt.Sprintf("adapter metadata for provider %q must preserve its id and have a non-empty name", provider),
				InvalidAdapterCode, nil)
		}
		unique[provider] = struct{}{}
		retryPolicy, owns := AdapterRetryPolicy(adapter, provider)
		if !owns {
			resolved, err := ResolveRetryPolicy(nil, fmt.Sprintf("llm: provider %q retryPolicy", provider))
			if err != nil {
				// 走不到：nil 配置解算的就是那份普通默认，它不会不合法。
				return nil, err
			}
			retryPolicy = resolved
		}
		registrations = append(registrations, &adapterRegistration{
			adapter:     adapter,
			provider:    ProviderInfo{ID: info.ID, Name: info.Name},
			retryPolicy: retryPolicy,
		})
	}
	return registrations, nil
}

// commitRoutes 在一段临界区里把这次登记的路由换成备好的那些。
//
// 源: packages/llm/llm/src/index.ts:425-440
//
// 摘掉再放回去之间没有任何观察者能插进来看一眼。通知在锁外面发，所以这个函数
// 自己不发——调用方解锁之后调 [Runtime.emitAdaptersUpdated]。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) commitRoutes(handle *AdapterRegistration, registrations []*adapterRegistration) {
	r.dropRoutes(handle.owned)
	handle.owned = handle.owned[:0]
	for _, registration := range registrations {
		id := registration.provider.ID
		r.adapters[id] = registration
		r.adapterOrder = append(r.adapterOrder, id)
		handle.owned = append(handle.owned, id)
	}
}

// dropRoutes 把这些路由从表和次序里一起摘掉。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) dropRoutes(providers []string) {
	for _, provider := range providers {
		delete(r.adapters, provider)
	}
	r.adapterOrder = slices.DeleteFunc(r.adapterOrder, func(id string) bool {
		return slices.Contains(providers, id)
	})
}

// ListProviders 列出有适配器的那些提供方路由，按登记次序。
//
// 源: packages/llm/llm/src/index.ts:442-448
func (r *Runtime) ListProviders() []ProviderInfo {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	providers := make([]ProviderInfo, 0, len(r.adapterOrder))
	for _, id := range r.adapterOrder {
		providers = append(providers, r.adapters[id].provider)
	}
	return providers
}

// registration 取出一条路由上的活登记。
//
// 源: packages/llm/llm/src/index.ts:871-875
func (r *Runtime) registration(provider string) (*adapterRegistration, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	registration, present := r.adapters[provider]
	if !present {
		return nil, NewError(fmt.Sprintf("no adapter registered for provider %q", provider), NoAdapterCode, nil)
	}
	return registration, nil
}

// ---- 可配置提供方目录 ----

// DirectoryRegistration 是一次活着的可配置提供方声明，是
// [AdapterRegistration] 在目录那一侧的对应物。
//
// 源: packages/llm/llm/src/index.ts:286-305
type DirectoryRegistration struct {
	runtime  *Runtime
	dispose  func(context.Context) error
	held     []ConfigurableProvider
	disposed bool
}

// RegisterConfigurableProviders 声明一批适配器插件可以靠配置激活的提供方路由。
//
// 源: packages/llm/llm/src/index.ts:450-511
//
// 全有或者全无：空清单、不合格的条目、或者任何一条已经被别人声明过的提供方，
// 都会报错，其余的一条也不会被登记。owner 释放时这次声明跟着撤销。
func (r *Runtime) RegisterConfigurableProviders(
	ctx context.Context,
	owner *scope.Scope,
	entries []ConfigurableProvider,
) (*DirectoryRegistration, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterConfigurableProviders 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if len(entries) == 0 {
		return nil, NewError(
			"a configurable-provider registration must declare at least one provider",
			InvalidDirectoryCode, nil)
	}
	handle := &DirectoryRegistration{runtime: r}
	if err := handle.commit(entries); err != nil {
		return nil, err
	}
	dispose, err := owner.Defer("llm.RegisterConfigurableProviders()", func(context.Context) error {
		handle.withdraw()
		return nil
	})
	if err != nil {
		handle.withdraw()
		return nil, err
	}
	handle.dispose = dispose
	return handle, nil
}

// Release 撤走这次声明当下攥着的每一条目录条目。重复调用没有额外效果。
//
// 源: packages/llm/llm/src/index.ts:291、495-500
func (h *DirectoryRegistration) Release(ctx context.Context) error {
	return h.dispose(ctx)
}

// Replace 把这次声明的条目原子地换成 entries。
//
// 源: packages/llm/llm/src/index.ts:292-304、504-509
//
// 条款和 [AdapterRegistration.Replace] 完全一样：整份先验过，被拒就原样留着，
// 换的那一下是一段临界区，空清单在这里合法。已经撤销之后再调报
// REGISTRATION_DISPOSED。
func (h *DirectoryRegistration) Replace(entries []ConfigurableProvider) error {
	return h.commit(entries)
}

// commit 验一份候选目录条目，验完整份再公布。
//
// 源: packages/llm/llm/src/index.ts:460-488
//
// 整份没过之前一个字都不写，这正是让 Replace 成为一次**替换**、而不是一次
// 「先删后加、中间可能把目录晾空」的那条性质。
func (h *DirectoryRegistration) commit(candidates []ConfigurableProvider) error {
	r := h.runtime
	r.mutex.Lock()
	if h.disposed {
		r.mutex.Unlock()
		return NewError("this configurable-provider registration was disposed", RegistrationDisposedCode, nil)
	}
	own := map[string]struct{}{}
	for _, entry := range h.held {
		own[entry.Provider] = struct{}{}
	}
	detached := make([]ConfigurableProvider, 0, len(candidates))
	for _, entry := range candidates {
		if entry.Provider == "" || entry.DisplayName == "" || entry.SettingsNs == "" {
			r.mutex.Unlock()
			return NewError(
				"configurable providers need a non-empty provider, displayName, and settingsNs",
				InvalidDirectoryCode, nil)
		}
		if slices.Contains(entry.SettingsPath, "") {
			r.mutex.Unlock()
			return NewError(
				fmt.Sprintf("configurable provider %q has an empty settingsPath segment", entry.Provider),
				InvalidDirectoryCode, nil)
		}
		_, declared := r.directory[entry.Provider]
		_, mine := own[entry.Provider]
		duplicate := slices.ContainsFunc(detached, func(seen ConfigurableProvider) bool {
			return seen.Provider == entry.Provider
		})
		if (declared && !mine) || duplicate {
			r.mutex.Unlock()
			return NewError(
				fmt.Sprintf("configurable provider %q is already declared", entry.Provider),
				DuplicateDirectoryCode, nil)
		}
		detached = append(detached, entry.Clone())
	}
	r.dropDirectory(h.held)
	for _, entry := range detached {
		r.directory[entry.Provider] = entry
		r.directoryOrder = append(r.directoryOrder, entry.Provider)
	}
	h.held = detached
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
	return nil
}

// withdraw 是撤销那一下的本体。
//
// 源: packages/llm/llm/src/index.ts:495-500
func (h *DirectoryRegistration) withdraw() {
	r := h.runtime
	r.mutex.Lock()
	if h.disposed {
		// 走不到：进这个函数的两条路——[DirectoryRegistration.Release] 和 owner
		// 释放——都经由 core/scope 那份只跑一次的撤销。这一句是第二道闸，
		// 防的是以后有人把它直接接到别处。
		r.mutex.Unlock()
		return
	}
	h.disposed = true
	r.dropDirectory(h.held)
	h.held = nil
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
}

// dropDirectory 把这些条目从目录和它的次序里一起摘掉。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) dropDirectory(entries []ConfigurableProvider) {
	if len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		delete(r.directory, entry.Provider)
		names = append(names, entry.Provider)
	}
	r.directoryOrder = slices.DeleteFunc(r.directoryOrder, func(id string) bool {
		return slices.Contains(names, id)
	})
}

// ListConfigurableProviders 列出每一条被声明过的可配置提供方，按声明次序，
// 不管它当下有没有被登记。
//
// 源: packages/llm/llm/src/index.ts:513-519
func (r *Runtime) ListConfigurableProviders() []ConfigurableProvider {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	entries := make([]ConfigurableProvider, 0, len(r.directoryOrder))
	for _, id := range r.directoryOrder {
		entries = append(entries, r.directory[id].Clone())
	}
	return entries
}

// ---- 模型发现 ----

// RegisterModelDiscovery 表示这个插件愿意代表它拥有的那个设置命名空间去问询
// 提供方端点，返回撤销这次表态的函数。
//
// 源: packages/llm/llm/src/index.ts:521-548
//
// 键是命名空间而不是路由名，因为配置界面手上本来就攥着命名空间（从可配置提供方
// 目录来的），而一条**正在被添加**的提供方还没有名字可点。
func (r *Runtime) RegisterModelDiscovery(
	ctx context.Context,
	owner *scope.Scope,
	settingsNs string,
	discover ModelDiscovery,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterModelDiscovery 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if settingsNs == "" {
		return nil, NewError("model discovery needs a non-empty settings namespace", InvalidDiscoveryCode, nil)
	}
	if discover == nil {
		return nil, NewError("model discovery needs a discover function", InvalidDiscoveryCode, nil)
	}
	r.mutex.Lock()
	if _, present := r.discoveries[settingsNs]; present {
		r.mutex.Unlock()
		return nil, NewError(
			fmt.Sprintf("model discovery for %q is already registered", settingsNs),
			DuplicateDiscoveryCode, nil)
	}
	r.discoveries[settingsNs] = discover
	r.mutex.Unlock()

	undo := func() {
		r.mutex.Lock()
		delete(r.discoveries, settingsNs)
		r.mutex.Unlock()
	}
	dispose, err := owner.Defer("llm.RegisterModelDiscovery()", func(context.Context) error {
		undo()
		return nil
	})
	if err != nil {
		undo()
		return nil, err
	}
	return dispose, nil
}

// DiscoverModels 问一个提供方端点它公告了哪些模型。
//
// 源: packages/llm/llm/src/index.ts:550-586
//
// 请求描述的是一份**草稿**、不是一条存下来的路由，所以这里既不读也不写设置和
// 凭据——两样都归调用方，而答复只是一份界面可以拿去让人采纳的候选元数据。
func (r *Runtime) DiscoverModels(
	ctx context.Context,
	settingsNs string,
	request ModelDiscoveryRequest,
) ([]DiscoveredModel, error) {
	r.mutex.Lock()
	discover, present := r.discoveries[settingsNs]
	r.mutex.Unlock()
	if !present {
		return nil, NewError(
			fmt.Sprintf("no model discovery is registered for %q", settingsNs),
			NoDiscoveryCode, nil)
	}
	// 两样里得有一样点明「要描述什么」：一条适配器认得的路由，或者一个可以去问的
	// 端点。两样都没有的话，这次问询没有对象。
	if request.Provider == "" && request.BaseURL == "" {
		return nil, NewError("model discovery needs a provider route or a baseURL", InvalidDiscoveryCode, nil)
	}
	discovered, err := discover(ctx, request)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	models := make([]DiscoveredModel, 0, len(discovered))
	for _, model := range discovered {
		if model.ID == "" {
			continue
		}
		if _, duplicate := seen[model.ID]; duplicate {
			continue
		}
		seen[model.ID] = struct{}{}
		models = append(models, model)
	}
	return models, nil
}

// ---- 提供方与模型元数据 ----

// ProviderRetryPolicy 交出登记某条路由时捕获下来的那份重试策略。
//
// 源: packages/llm/llm/src/index.ts:588-595
func (r *Runtime) ProviderRetryPolicy(provider string) (ResolvedRetryPolicy, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return ResolvedRetryPolicy{}, err
	}
	return registration.retryPolicy, nil
}

// ListModels 列出某条已登记路由公告的模型。目录成员资格是参考性的，它既不改
// 路由也不改请求校验。
//
// 源: packages/llm/llm/src/index.ts:602-635
func (r *Runtime) ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return nil, err
	}
	models, err := AdapterListModels(ctx, registration.adapter, provider)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	catalog := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		_, duplicate := seen[model.ID]
		if model.Provider != provider || model.ID == "" || model.Name == "" || duplicate {
			return nil, NewError(
				fmt.Sprintf("adapter returned invalid or duplicate model metadata for provider %q", provider),
				InvalidCatalogCode, nil)
		}
		seen[model.ID] = struct{}{}
		catalog = append(catalog, model.Clone())
	}
	return catalog, nil
}

// ResolveModelInfo 解算并校验拥有某条精确路由的那个适配器交出来的全部元数据。
//
// 源: packages/llm/llm/src/index.ts:637-652
func (r *Runtime) ResolveModelInfo(ctx context.Context, provider, model string) (ResolvedModelInfo, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return ResolvedModelInfo{}, err
	}
	return r.resolveModelInfoFor(ctx, registration, model)
}

// 源: packages/llm/llm/src/index.ts:654-661
func (r *Runtime) resolveModelInfoFor(
	ctx context.Context,
	registration *adapterRegistration,
	model string,
) (ResolvedModelInfo, error) {
	resolved, err := AdapterResolveModel(ctx, registration.adapter, registration.provider.ID, model)
	if err != nil {
		return ResolvedModelInfo{}, err
	}
	return normalizeModelInfo(registration.provider.ID, model, resolved)
}

// normalizeModelInfo 校验并摘下适配器交回来的那份确切模型结果。
//
// 源: packages/llm/llm/src/index.ts:663-754
//
// 新增: DSH 那一版里过半的判断是 `typeof x !== 'string'` 之类的类型检查，在 Go 里
// 由编译器管。剩下的是**取值**判断，一条不少：身份必须原样保住、名字非空、
// 上下文窗口是正数、默认输出上限是正数、推理档位非空且互不重名、默认档位在清单里。
//
// 新增: DSH 判的是 `defaultMaxTokens <= 0`，因为它用 undefined 表示「没配」。
// Go 这边 0 就是「没配」（见 [ResolvedModelInfo]），所以这条判据是 `< 0`。
func normalizeModelInfo(provider, model string, resolved ResolvedModelInfo) (ResolvedModelInfo, error) {
	if resolved.Provider != provider || resolved.ID != model || resolved.Name == "" {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid exact model metadata for provider %q model %q", provider, model),
			InvalidModelInfoCode, nil)
	}
	if resolved.Context != nil && resolved.Context.ContextWindow <= 0 {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid context metadata for provider %q model %q", provider, model),
			InvalidModelContextCode, nil)
	}
	if resolved.DefaultMaxTokens < 0 {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid default maxTokens for provider %q model %q", provider, model),
			InvalidModelMaxTokensCode, nil)
	}
	if resolved.Reasoning != nil {
		if len(resolved.Reasoning.Efforts) == 0 {
			return ResolvedModelInfo{}, NewError(
				fmt.Sprintf("adapter returned invalid reasoning metadata for provider %q model %q", provider, model),
				InvalidModelReasoningCode, nil)
		}
		seen := map[ReasoningEffortID]struct{}{}
		for _, effort := range resolved.Reasoning.Efforts {
			_, duplicate := seen[effort.ID]
			if effort.ID == "" || effort.Name == "" || duplicate {
				return ResolvedModelInfo{}, NewError(
					fmt.Sprintf("adapter returned invalid or duplicate reasoning effort metadata for provider %q model %q",
						provider, model),
					InvalidModelReasoningCode, nil)
			}
			seen[effort.ID] = struct{}{}
		}
		if resolved.Reasoning.DefaultEffort != "" {
			if _, known := seen[resolved.Reasoning.DefaultEffort]; !known {
				return ResolvedModelInfo{}, NewError(
					fmt.Sprintf("adapter returned an unknown default reasoning effort for provider %q model %q",
						provider, model),
					InvalidModelReasoningCode, nil)
			}
		}
	}
	// 能力元数据原样带过去：一次**显式**省掉某个模态是下游预检要据以行动的
	// 否定能力（图片准入那条路）。
	return resolved.Clone(), nil
}

// ---- 调用配置解算 ----

// ResolveCallConfig 拿一份会话调用配置去对确切模型的能力，并把适配器配好的默认
// 落实进去。不支持的显式档位在任何提供方 I/O 之前就被拒；这里不做任何夹取或者
// 别名替换。
//
// 源: packages/llm/llm/src/index.ts:756-768
//
// 这次独立问询**不绑定**之后那次派发；日志和流式必须共用同一份适配器登记时，
// 用 [Runtime.PrepareCall]。
func (r *Runtime) ResolveCallConfig(ctx context.Context, config CallConfig) (CallConfig, error) {
	registration, err := r.registration(config.Provider)
	if err != nil {
		return CallConfig{}, err
	}
	info, err := r.resolveModelInfoFor(ctx, registration, config.Model)
	if err != nil {
		return CallConfig{}, err
	}
	return resolveCallWithInfo(config, info)
}

// resolveCallWithInfo 拿请求控制项去对一份已经绑好的确切模型结果。
//
// 源: packages/llm/llm/src/index.ts:779-814
func resolveCallWithInfo(config CallConfig, info ResolvedModelInfo) (CallConfig, error) {
	resolved := config
	if resolved.MaxTokens == 0 && info.DefaultMaxTokens != 0 {
		resolved.MaxTokens = info.DefaultMaxTokens
	}
	requested := resolved.ReasoningEffort
	if info.Reasoning == nil {
		if requested != "" {
			return CallConfig{}, NewError(
				fmt.Sprintf("provider %q model %q does not support reasoning effort %q",
					config.Provider, config.Model, requested),
				UnsupportedReasoningEffortCode, nil)
		}
		return resolved, nil
	}
	effective := requested
	if effective == "" {
		effective = info.Reasoning.DefaultEffort
	}
	if effective == "" {
		return resolved, nil
	}
	supported := slices.ContainsFunc(info.Reasoning.Efforts, func(effort ReasoningEffortInfo) bool {
		return effort.ID == effective
	})
	if !supported {
		return CallConfig{}, NewError(
			fmt.Sprintf("provider %q model %q does not support reasoning effort %q",
				config.Provider, config.Model, effective),
			UnsupportedReasoningEffortCode, nil)
	}
	resolved.ReasoningEffort = effective
	return resolved, nil
}

// ---- 准备好的调用 ----

// PreparedCall 是一次配置和适配器登记一起解算出来的模型调用。
//
// 源: packages/llm/llm/src/index.ts:155-175
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
