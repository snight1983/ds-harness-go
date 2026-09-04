// 本文件的作用：模型运行时本身——它攥着哪些路由、有哪些观察者，以及观察者
// 怎么挂到一个作用域上又怎么撤掉。
//
// 源: packages/llm/llm/src/index.ts:262-1026
//
// 新增: DSH 那边 LlmRuntime extends Service，登记走 ctx.effect、通知走 cordis 事件、
// 瀑布走 ctx.waterfall。Go 这边一样也不照抄：生命周期挂在显式传进来的
// [github.com/snight1983/ds-harness-go/scope.Scope] 上，通知是一张显式的观察者表，瀑布是一张
// 有序的规则表加一次递归下钻——和本仓库 harness/agent、harness/session 完全一致。

package llm

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/scope"
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
// 源: packages/llm/llm/src/index.ts:322-1066（LlmRuntime）
//
// 新增: 三张表加两张观察者表都由一把互斥锁护着——DSH 是单线程 JS，不需要。
// 观察者一律在锁**外面**叫，理由和 harness/agent 那边逐字相同：一个观察者反手来问
// [Runtime.ListProviders] 是完全正当的，在锁里叫它就是自锁。
type Runtime struct {
	logger *slog.Logger

	mutex sync.Mutex
	// adapters 是路由键到活登记的映射，adapterOrder 是它们的登记次序。
	//
	// 新增: DSH 用一个 JS Map，它自己保插入顺序，而 [Runtime.ListProviders] 明写
	// 「按登记次序」。Go 的 map 不保任何顺序，所以这个次序数组是必须的——和
	// harness/agent 的 Registry 那份 order 出于同一个理由。
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
	// 让 [github.com/snight1983/ds-harness-go/fs.Policy] 自己攥着一个 fail 字段是同一个搬法。
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
// 三条规则和 [github.com/snight1983/ds-harness-go/credentials.Notifier] 的 fanOut 逐字相同：每一个都跑到、
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
