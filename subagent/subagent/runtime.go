// 本文件的作用：`ctx.subagents` 这条接缝的服务本体——一张按名字的提供方注册表、
// 一条验能力的一次性派发、那几件可续孩子的操作，以及只读的孩子与后代列举。
//
// 源: packages/subagent/subagent/src/index.ts
//
// 和 bash 那条接缝（一个上下文一个执行器，第二次装载直接报错）不一样，这里**多个**
// 提供方共存：各自按唯一的名字登记，调用方点名取用。形状对着的是
// [ds-harness-go/llm.Runtime.RegisterAdapter] 那张适配器注册表，不是单实例的执行器。
//
// 本包扮演的是这条能力接缝的**服务定义**那一角。提供方（spawn／fork／acp）和面向
// 模型的消费方（tool-subagent）各自成包。
//
// 公开操作说的是调用方的意图：[Runtime.Start] 交回一次已发布、归持有方的一次性运行，
// [Runtime.StartContinuable] 立起一个耐久的可续孩子，[Runtime.Followup] 投递后续内容
// 而不外露那个孩子当下驻不驻留。可续孩子**绝不**变成一个 [Run]：续接管理器直接攥着
// 它的 agent 句柄、每一个回合都经它自己的收件箱排队，所以提供方只贡献那份脱离的创建
// 输入，看不到句柄、回合和拆解。孩子与后代的发现直接读活会话表和可选的会话持久化，
// 不需要那台续接运行时。

package subagent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// RuntimeOptions 是造这条接缝的服务时给的东西。
//
// 新增: DSH 那个构造函数从 cordis 上下文上 `ctx.inject(['agents'])`、
// `ctx.inject(['sessionProjections'])` 现取服务，装没装、什么时候装都由容器决定。
// Go 没有那个容器，「在不在场」就是装配方手上有没有这个值，所以三样依赖在这里
// 一次性交清（和 [ListingServices]、[ContinuationDeps] 同一种做法）。
type RuntimeOptions struct {
	// Continuation 是可续孩子那条路要用到的那几样服务。
	//
	// Agents 为 nil 就是 DSH 那个 `inject(['agents'])` 没兑现的情形：这套部署
	// 起不了可续孩子，那几件操作报 [CodeContinuationUnavailable]，而打断和排干
	// 是被接受的空操作（一台没有的管理器不可能攥着任何活化）。
	//
	// 它的 Setups 由这个构造函数自己填上——那张装配登记表归服务所有，
	// 装配方填了也会被盖掉。
	Continuation ContinuationDeps
	// Listing 是只读列举要用的那几样服务，逐字交给 [ListChildren] 与 [ListDescendants]。
	Listing ListingServices
	// Logger 记生命周期观察者的 panic，以及可续那条路上几件 fail-soft 的事故。
	// 为 nil 时用 [log/slog.Default]。
	Logger *slog.Logger
}

// Runtime 是按名字的提供方注册表，外加一次性运行、耐久发现和可续孩子那几件操作。
//
// 源: packages/subagent/subagent/src/index.ts:170-514
type Runtime struct {
	listing ListingServices
	// setups 是部署往每一个还没公布的可续孩子里组合的那些贡献。
	setups *ActivationSetupRegistry
	// emitLifecycle 是这条接缝那个兜住异常的生命周期发射器。
	emitLifecycle *lifecycleEmitter
	// continuations 是可选的续接管理器；nil 表示这套部署没组装 agent 服务。
	//
	// 新增: DSH 那个槽随 `agents` 服务的装卸而生灭，所以它有一个
	// `childCtx.effect` 在纤程处置时把自己解绑。Go 这边它在构造期就定死了——
	// 一台 [Runtime] 的依赖是装配方一次交清的，中途不会变——于是那次解绑没有
	// 对应物，这个字段也就不需要任何锁。
	continuations *ContinuationManager

	// mutex 守住下面那张注册表和它的次序。观察者一律在锁外调用，规矩和
	// [ds-harness-go/core/agent.Registry] 逐字相同：一个观察者反手来问
	// [Runtime.List] 是完全正当的，在锁里叫它就是自锁。
	mutex     sync.Mutex
	providers map[string]Provider
	// order 是登记进来的先后。
	//
	// 新增: DSH 用的是 JS 的 Map，它自带插入顺序，`list()` 直接遍历就是登记次序，
	// 而那份次序**是**它文档写明的语义。Go 的 map 没有顺序，所以另存一份。
	order []string
}

// NewRuntime 造一条子 agent 接缝的服务。
//
// 源: packages/subagent/subagent/src/index.ts:182-200
//
// 新增: DSH 那个构造函数还在 `ctx.inject(['sessionProjections'])` 里登记那两个
// 投影单元。Go 里投影登记是一个显式函数 [RegisterProjections]，由装配方在它手上
// 那张注册表上调用——那和 [ListingServices.Projections] 是同一张表，所以这里再登记
// 一次只会撞名。也就是说：**装配方必须自己调 [RegisterProjections]**，否则
// [Runtime.ListChildren] 分不出孩子的耐久身份。
func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	emitter, err := newLifecycleEmitter(options.Logger)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		listing:       options.Listing,
		setups:        NewActivationSetupRegistry(),
		emitLifecycle: emitter,
		providers:     map[string]Provider{},
	}
	// DSH 那个 `inject(['agents'])`：agent 注册表在场，这台管理器才存在。
	if options.Continuation.Agents != nil {
		deps := options.Continuation
		deps.Setups = runtime.setups
		if deps.Logger == nil {
			deps.Logger = options.Logger
		}
		// host 就是这台服务自己：能力解算和生命周期发布都归它，管理器只管编排。
		manager, err := NewContinuationManager(deps, runtime)
		if err != nil {
			return nil, err
		}
		runtime.continuations = manager
	}
	return runtime, nil
}

// ---- 生命周期观察 ----

// OnStart 登记一个 `subagent/start` 观察者，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:151-160
//
// owner 决定这次登记落在哪一层：没有身份的作用域落全局层，看得见每一次派发；
// 有身份的作用域落它自己那一层，只看得见由它（或它的子孙）发起的派发。
// 下面三个登记方法这条规矩完全一样。
func (r *Runtime) OnStart(ctx context.Context, owner *scope.Scope, observer StartObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnStart 需要一个观察者", ErrInvalidRequest)
	}
	return r.emitLifecycle.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.start.Append(observer), nil
	}, scope.EffectOptions{Label: "subagents.OnStart()"})
}

// OnEnd 登记一个 `subagent/end` 观察者，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:161-168
func (r *Runtime) OnEnd(ctx context.Context, owner *scope.Scope, observer EndObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnEnd 需要一个观察者", ErrInvalidRequest)
	}
	return r.emitLifecycle.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.end.Append(observer), nil
	}, scope.EffectOptions{Label: "subagents.OnEnd()"})
}

// OnProviderAdded 登记一个「提供方来了」的观察者，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:141-145
//
// 它是唯一一个交回错误的观察者：报错会把那次 [Runtime.RegisterProvider] 整个卷回去。
func (r *Runtime) OnProviderAdded(ctx context.Context, owner *scope.Scope, observer ProviderAddedObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnProviderAdded 需要一个观察者", ErrInvalidRequest)
	}
	return r.emitLifecycle.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.providerAdded.Append(observer), nil
	}, scope.EffectOptions{Label: "subagents.OnProviderAdded()"})
}

// OnProviderRemoved 登记一个「提供方走了」的观察者，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:146-150
func (r *Runtime) OnProviderRemoved(ctx context.Context, owner *scope.Scope, observer ProviderRemovedObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnProviderRemoved 需要一个观察者", ErrInvalidRequest)
	}
	return r.emitLifecycle.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.providerRemoved.Append(observer), nil
	}, scope.EffectOptions{Label: "subagents.OnProviderRemoved()"})
}

// ---- 提供方注册表 ----

// RegisterProvider 按名字登记一个提供方，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:388-407
//
// 摘掉一个提供方只挡住**新的**开工，绝不撤销已经交到持有方手上的运行。
// owner 释放时这次登记跟着释放。
func (r *Runtime) RegisterProvider(
	ctx context.Context,
	owner *scope.Scope,
	provider Provider,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterProvider 需要一个持有它的作用域", ErrInvalidRequest)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w：RegisterProvider 需要一个提供方", ErrInvalidRequest)
	}
	name := provider.Name()

	r.mutex.Lock()
	if _, taken := r.providers[name]; taken {
		r.mutex.Unlock()
		return nil, NewError(
			`a subagent provider named "`+name+`" is already registered`,
			CodeDuplicateProvider, nil,
		)
	}
	r.providers[name] = provider
	r.order = append(r.order, name)
	r.mutex.Unlock()

	dispose, err := owner.Defer("subagents.registerProvider()", func(context.Context) error {
		r.removeProvider(name, provider)
		return nil
	})
	if err != nil {
		r.removeProvider(name, provider)
		return nil, err
	}
	// 一个报错的「提供方来了」观察者把这次登记卷回去，和本仓库其余登记那种
	// 大声失败保持一致。走 dispose 而不是直接回滚，是为了同时把刚挂上去的那次
	// 作用域登记摘掉。
	//
	// 回滚走 defer 而不是只看返回值：本包那份不变量检查是**panic**着报的
	// （见 [invariants.Fail]），它就跑在这条边的最前面。只认返回值的话，一次
	// 违例会带着一个已经登记进注册表的提供方穿出去，而 DSH 那句
	// 「A throwing added-listener unwinds the yielded rollback」说的正是这种情形。
	announced := false
	defer func() {
		if !announced {
			// **有意**丢掉回滚自己的结果：它交回的永远是 nil，而这次登记的失败
			// 原因是那个观察者，不是拆解。
			_ = dispose(ctx)
		}
	}()
	if err := r.emitLifecycle.emitProviderAdded(provider); err != nil {
		return nil, err
	}
	announced = true
	return dispose, nil
}

// removeProvider 把一次登记从注册表里摘掉，并发出「提供方走了」那条边。重复调用
// 没有额外效果。
//
// 认的是那个确切的提供方对象：一次登记撤销之后，同一个名字可能已经被后来的另一个
// 提供方占上了，把它误伤掉会让一次毫不相干的登记失效。
func (r *Runtime) removeProvider(name string, provider Provider) {
	r.mutex.Lock()
	current, present := r.providers[name]
	if !present || current != provider {
		r.mutex.Unlock()
		return
	}
	delete(r.providers, name)
	for index, registered := range r.order {
		if registered == name {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	r.mutex.Unlock()
	r.emitLifecycle.emitProviderRemoved(name)
}

// GetProvider 按名字取一个提供方；第二个返回值为假表示没有。
//
// 源: packages/subagent/subagent/src/index.ts:409-416
func (r *Runtime) GetProvider(name string) (Provider, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	provider, found := r.providers[name]
	return provider, found
}

// List 按登记次序交出已登记的那些提供方名字。
//
// 源: packages/subagent/subagent/src/index.ts:418-424
func (r *Runtime) List() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	return names
}

// expectProvider 取一个提供方派活儿，没有就大声失败。
//
// 源: packages/subagent/subagent/src/index.ts:487-493
func (r *Runtime) expectProvider(name string) (Provider, error) {
	provider, found := r.GetProvider(name)
	if !found {
		return nil, NewError(`no subagent provider registered for "`+name+`"`, CodeNoProvider, nil)
	}
	return provider, nil
}

// ---- 一次性派发 ----

// Start 在点名的提供方上立起一个已发布的孩子。
//
// 源: packages/subagent/subagent/src/index.ts:426-445
//
// 能力检查和语义检查都跑在派发之前。提供方的所有权持续到它兑现为止，所以一次失败
// 既不留下要调用方处置的运行，也不发任何运行生命周期边。发布之后的回合失败与基础
// 设施失败，从交回的那个运行结清。
func (r *Runtime) Start(ctx context.Context, name string, request StartRequest) (Run, error) {
	provider, err := r.expectProvider(name)
	if err != nil {
		return nil, err
	}
	if err := assertCapabilities(provider, request); err != nil {
		return nil, err
	}
	if err := AssertMaxDepth(request.MaxDepth); err != nil {
		return nil, err
	}
	if request.OutputSchema != nil {
		if err := tools.AssertObjectSchema(*request.OutputSchema); err != nil {
			return nil, err
		}
	}
	descriptor, err := SnapshotDescriptor(DescriptorData{
		Mode:     ModeOneShot,
		Provider: name,
		Label:    request.Label,
	})
	if err != nil {
		return nil, err
	}
	run, err := provider.Start(ctx, ResolvedStartRequest{StartRequest: request, Descriptor: descriptor})
	if err != nil {
		return nil, err
	}
	// 新增: 交给 [observeRun] 的是一个**去掉取消**的 ctx。那条 goroutine 等的是这次
	// 运行自己的结局，而运行的所有权在这一刻已经转给持有方了；用调用方那个开工 ctx，
	// 会让「开工调用返回」直接催出一条假的终止边（DSH 那边挂的是 run.result 这个
	// promise，压根没有取消这回事）。值照样带过去。
	return observeRun(context.WithoutCancel(ctx), r.emitLifecycle, name, request.Parent, run), nil
}

// assertCapabilities 拒掉第一样这个提供方不支持、而请求又要了的能力。
//
// 源: packages/subagent/subagent/src/index.ts:508-513
//
// 次序是固定的（结构化输出 → 深度上限 → 工具过滤 → 人设），于是一次要了好几样的
// 请求报出来的永远是同一条。
func assertCapabilities(provider Provider, request StartRequest) error {
	capabilities := provider.Capabilities()
	needs := []struct {
		requested bool
		supported bool
		name      string
	}{
		{request.OutputSchema != nil, capabilities.OutputSchema, "outputSchema"},
		{request.MaxDepth != nil, capabilities.DepthLimit, "depthLimit"},
		// 新增: DSH 靠 `toolFilter !== undefined` 判「要没要」。Go 的
		// [ds-harness-go/core/tools.Restriction] 是值类型，没有「不在」，所以
		// 「两张名单都是 nil」就是没要——和 [ContinuationManager.StartContinuable]
		// 拍描述符时用的是同一条判据。
		{request.ToolFilter.Allow != nil || request.ToolFilter.Deny != nil, capabilities.ToolFilter, "toolFilter"},
		{request.Persona != "", capabilities.Persona, "persona"},
	}
	for _, need := range needs {
		if need.requested && !need.supported {
			return NewError(
				`subagent provider "`+provider.Name()+`" does not support the "`+need.name+`" capability`,
				CodeUnsupportedCapability, nil,
			)
		}
	}
	return nil
}

// ---- 可续孩子 ----

// requireContinuations 解出那台可选的续接管理器，没有就大声失败。
//
// 源: packages/subagent/subagent/src/index.ts:495-504
func (r *Runtime) requireContinuations() (*ContinuationManager, error) {
	if r.continuations == nil {
		return nil, NewError(
			"continuable subagents require the agents service",
			CodeContinuationUnavailable, nil,
		)
	}
	return r.continuations, nil
}

// StartContinuable 立起一个耐久的可续孩子，并投出它的初始提示词。
//
// 源: packages/subagent/subagent/src/index.ts:210-212
//
// 孩子的收件箱一接受那条提示词就返回，**不**等回合开跑、也**不**等那条消息落进
// 会话日志；在那之前的任何失败都两个 id 都不给地报错，并把这个孩子整个卷回去。
func (r *Runtime) StartContinuable(ctx context.Context, spec ContinuableStartSpec) (ContinuableStart, error) {
	manager, err := r.requireContinuations()
	if err != nil {
		return ContinuableStart{}, err
	}
	return manager.StartContinuable(ctx, spec)
}

// Followup 把一条后续消息投给一个可续孩子的下一个 FIFO 回合。
//
// 源: packages/subagent/subagent/src/index.ts:214-236
//
// 驻留着的孩子由它的 agent 收件箱直接接下（顺带唤醒一次 waiting 的活化），
// 不在的那个从它耐久的会话冷恢复出来。收件箱是唯一的队列，所以每一条被接受的
// 消息都有一个可观察的次序。
func (r *Runtime) Followup(
	ctx context.Context,
	parent agent.Agent,
	childID session.SessionID,
	content llm.Content,
	options FollowupOptions,
) (llm.MessageID, error) {
	manager, err := r.requireContinuations()
	if err != nil {
		return "", err
	}
	return manager.Followup(ctx, parent, childID, content, options)
}

// Interrupt 打断一个活着的可续孩子当下那段活动，凭的是一个人类父地址、或者一个
// 确切的活祖先 agent。
//
// 源: packages/subagent/subagent/src/index.ts:238-256
//
// 发完就返回：取消信号在返回之前已经发出，但目标可能要跑到它自己观察到那个信号
// 为止。没认领的待办、那次活化、以及已发布的后代都留着；已经认领走的活儿不重排。
// 目标不在——包括一次性孩子和不认识的 id——是一次被接受的空操作；一套没组装管理器
// 的部署同理，它不可能攥着任何活化。
func (r *Runtime) Interrupt(targetSessionID session.SessionID, authority InterruptAuthority) error {
	if r.continuations == nil {
		return nil
	}
	return r.continuations.Interrupt(targetSessionID, authority)
}

// ReportFrom 把一个活着的可续孩子选出来的内容投给它耐久的直系父。
//
// 源: packages/subagent/subagent/src/index.ts:258-273
//
// 孩子本身就是那份权证，调用方点不了收件人。汇报既不结束这个孩子的回合，
// 也不结束它的活化。
func (r *Runtime) ReportFrom(
	ctx context.Context,
	child agent.Agent,
	content llm.Content,
	options ReportOptions,
) (llm.MessageID, error) {
	manager, err := r.requireContinuations()
	if err != nil {
		return "", err
	}
	return manager.ReportFrom(ctx, child, content, options)
}

// RegisterContinuableSetup 把一份部署能力组合进每一个可续孩子还没公布的那段创建
// 窗口——新建和冷恢复都算，返回撤销这次登记的函数。
//
// 源: packages/subagent/subagent/src/index.ts:275-289
//
// 授予等下一次活化才生效；撤销会当场收回每一次驻留着的安装。
func (r *Runtime) RegisterContinuableSetup(
	contribution ActivationSetupContribution,
) (func(context.Context) error, error) {
	return r.setups.Register(contribution)
}

// DrainContinuableDescendants 关掉几个确切的活父底下的可续准入，同步停掉它们那些
// 看得见的后代活化，然后等那些已经准入的物化落定、并孩子优先地放掉那片森林。
//
// 源: packages/subagent/subagent/src/index.ts:291-304
//
// 那道带范围的闸一直关到每个确切的父离开 agent 注册表为止；不相干的父树照常活着。
// 没组装管理器意味着从来没有物化过任何东西，所以那是一次空操作。
func (r *Runtime) DrainContinuableDescendants(ctx context.Context, parents []agent.Agent) error {
	if r.continuations == nil {
		return nil
	}
	return r.continuations.DrainDescendants(ctx, parents)
}

// DrainContinuableChildren 放掉一个确切的活父那几个选中的、驻留着的直系可续孩子。
//
// 源: packages/subagent/subagent/src/index.ts:306-318
//
// 同一个父其余的孩子照样收活、照样驻留。目标不在、以及没组装管理器，都是被接受的
// 空操作。
func (r *Runtime) DrainContinuableChildren(
	ctx context.Context,
	parent agent.Agent,
	childIDs []session.SessionID,
) error {
	if r.continuations == nil {
		return nil
	}
	return r.continuations.DrainChildren(ctx, parent, childIDs)
}

// ---- 只读发现 ----

// ListChildren 列举一个父那些有会话的直接子 agent，既不装载也不恢复任何 agent，
// 也不需要任何查询服务。
//
// 源: packages/subagent/subagent/src/index.ts:320-359
//
// 这条服务不查任何 agent 登记、活化和提供方。
func (r *Runtime) ListChildren(ctx context.Context, parentSessionID session.SessionID) ([]ListEntry, error) {
	return ListChildren(ctx, r.listing, parentSessionID)
}

// ListDescendants 按稳定的先序列举一个根底下完整的、有会话的子 agent 树。
//
// 源: packages/subagent/subagent/src/index.ts:361-378
//
// 普通会话和一次性孩子照样是遍历节点，所以挂在它们底下的可续后代仍旧发现得到；
// 身份解算、诊断、可选持久化和取消都和 [Runtime.ListChildren] 同一份契约。
func (r *Runtime) ListDescendants(ctx context.Context, rootSessionID session.SessionID) ([]DescendantListEntry, error) {
	return ListDescendants(ctx, r.listing, rootSessionID)
}

// ---- continuationHost ----

// prepareContinuable 解算一个提供方那份脱离的可续创建贡献。
//
// 源: packages/subagent/subagent/src/index.ts:447-464
//
// 「这个提供方实现没实现 [ContinuablePreparer]」**就是**那个能力，所以没实现的
// 会在管理器占下任何孩子资源之前就被拒掉。
func (r *Runtime) prepareContinuable(
	ctx context.Context,
	name string,
	request ContinuableCreateRequest,
) (ContinuableCreateSpec, error) {
	provider, err := r.expectProvider(name)
	if err != nil {
		return ContinuableCreateSpec{}, err
	}
	preparer, capable := provider.(ContinuablePreparer)
	if !capable {
		return ContinuableCreateSpec{}, NewError(
			`subagent provider "`+provider.Name()+
				`" does not support continuable children (no prepareContinuable capability)`,
			CodeUnsupportedCapability, nil,
		)
	}
	return preparer.PrepareContinuable(ctx, request)
}

// observeActivation 造出一段可续活化驻留轮次的生命周期观察者，于是管理器发得出
// 它那两条边，而不必自己拥有事件派发。
//
// 源: packages/subagent/subagent/src/index.ts:466-478
func (r *Runtime) observeActivation(
	provider string,
	childID session.SessionID,
	parent agent.Agent,
) *activationObserver {
	return newActivationObserver(r.emitLifecycle, provider, childID, parent)
}
