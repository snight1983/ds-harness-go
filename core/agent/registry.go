// 本文件的作用：装着活 agent 的那张表——把一个 agent 登记进来、公布出去、
// 再摘出去，那十二组观察者的登记与派发，以及循环那一层交上来的造法。
//
// 源: packages/core/agent/src/index.ts:53-214、244-298、360-617
//
// 这里的登记／公布／摘除三步和 [ds-harness-go/core/session.Store] 逐字同构，
// 那边的每一条理由（为什么 Enter 不发公布、为什么 announced 在派发之前就置真、
// 为什么一个过期的摘除能力删不掉后来那一次同名生命周期）在这里原样成立，
// 不再重复，只标出两边真正不一样的地方。

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// Setup 是在一个还没公布的 agent 上组装它那个世界的回调。
//
// 源: packages/core/agent/src/index.ts:53-71（AgentSetupCommit 与 AgentSetup）
//
// 工厂铸出 agentScope 之后、把会话和 agent 登记进去**之前**跑它，所以观察者
// 永远看不到一个只装配了一半的世界。挂在 agentScope 上的一切（作用域内的工具、
// 提示词段落与变量、监听器、子插件）在 agent/created、session/created、
// agent/session-start、以及第一次提示词装配之前就已经在位。
//
// 返回的 commit 可以是 nil；不是 nil 的话，工厂在登记公布**紧邻之前**同步调它，
// 好让一份可变的供给在那个确切的边界上再验一次。setup 报错、commit 报错、
// 或者 owner 被处置，整个事务回滚，两个身份一个都不公布。
//
// **setup 只组装，不驱动**：这个回调是同进程里可信的代码，拿得到整个作用域，
// 所以这是一条约定而不是运行期限制。要驱动就等创建返回之后。
//
// 新增: DSH 是 `AgentSetupCommit { commit(): void }` 一个只有一个方法的接口。
// Go 里返回一个可以为 nil 的 `func() error` 说的是同一件事，而且实现方不必为它
// 单独定义一个类型。
type Setup func(ctx context.Context, agentScope *scope.Scope) (commit func() error, err error)

// CreateOptions 是通过工厂造一个新 agent 时给的东西。
//
// 源: packages/core/agent/src/index.ts:73-133（CreateAgentOptions）
//
// 新增: DSH 把会话那几项裹在一个可选的 `meta` 对象里。这里摊平了，理由和
// [ds-harness-go/core/session.CreateOptions] 上那条逐字相同——那层嵌套不承载语义。
//
// 新增: DSH 的 `signal`（只在创建期间有效的取消口）在 Go 里是
// [Registry.Create] 的第一个 [context.Context]，规矩和本仓库每一处一样。
type CreateOptions struct {
	// SessionID 是 agent 和会话共用的那个活身份，必填。
	SessionID sessionlog.SessionID

	// Cwd 是这个会话的工作目录，必须是本机上的绝对路径。
	Cwd string
	// ParentSession 是分叉来源的标识。
	ParentSession sessionlog.SessionID
	// SeedLength 是耐久的分叉血统边界。
	SeedLength int
	// Origin 非空表示这是一个子 agent 的会话。
	Origin sessionlog.Origin
	// DelegationDepth 是委派层数，根会话是 0。
	DelegationDepth int
	// AgentPreset 是建出这个会话的那份 agent 预设名。
	AgentPreset string

	// Seed 是回放／分叉用的初始历史。
	//
	// 一次分叉给的是父会话日志里一段**回合完整**的前缀：必须从 seq 0 起连续、
	// 只带无损 JSON、且没有开着的回合／步骤或者悬空的工具调用。工厂把它交给
	// 会话那道耐久校验边界，在公布之前。
	Seed []sessionlog.Event

	// AgentOptions 是这个 agent 自己的提供方路由与模型。
	AgentOptions Options

	// Setup 是创建期的世界组装，可以为 nil。
	Setup Setup
}

// ResumeOptions 是在一段持久化会话上续跑一个 agent 时给的东西。
//
// 源: packages/core/agent/src/index.ts:135-156（ResumeAgentOptions）
type ResumeOptions struct {
	// ResumeSessionID 是要读回来、并用作活身份的那个持久化会话标识。
	ResumeSessionID sessionlog.SessionID
	// AgentOptions 是这个 agent 自己的提供方路由与模型。
	AgentOptions Options
	// Setup 是续跑期的世界组装，可以为 nil。
	//
	// 持久化先读回来，工厂才铸 agentScope 并跑 setup，此时重建出来的会话和 agent
	// 都还没公布。约定和 [CreateOptions.Setup] 完全一样。
	Setup Setup
}

// Handle 是一个 agent 加上拆掉它的那个能力。
//
// 源: packages/core/agent/src/index.ts:158-175（AgentHandle）
//
// Dispose 是一个**能力**：在消费方里只有攥着它的那一位拆得掉这个 agent。登记了
// 造法的那个提供方在结构上也是主人——作用域内的 agent 依赖它那份服务 API，
// 提供方卸载会停掉并排干它造出来的每一个活句柄。
//
// Dispose 停掉循环、等它退出、把 agent 从注册表里摘掉、把会话从存储里移走，
// 最后才拆掉它那个作用域世界。
//
// [Registry.Get] 交出来的仍然只是一个光的 [Agent]——句柄只给造它的那个消费方。
type Handle struct {
	// Agent 是造出来的那一个。
	Agent Agent
	// Dispose 拆掉这个 agent，一次性。
	Dispose func(context.Context) error
}

// Factory 是循环那一层交给注册表的 agent 造法。
//
// 源: packages/core/agent/src/index.ts:177-214（AgentFactory）
//
// 它定在这个包上，好让消费方（ACP 桥、子 agent、作业）只对着 [Registry] 编程，
// 不必依赖具体那个循环包。
//
// 新增: DSH 的两个方法第一个参数是 `ownerCtx`——调用方那个 cordis 上下文，它同时
// 是「谁拥有这次事务和这个活句柄」和「effect 挂在哪儿」。Go 里这两件事分开写：
// ctx 是取消与超时，owner 是那个作用域。DSH 那句「实现不许从工厂对象自己的登记
// 上下文推断归属」在这里是结构性的——工厂拿不到别的作用域。
type Factory interface {
	// CreateAgent 在调用方给的会话身份上造一个新 agent。
	//
	// 它跑完 setup、调掉那个 commit、把会话和 agent 都登记进去、按次序发出两条
	// 创建通知、发 agent/session-start，然后才起循环。整条路是回滚兜住的，但在
	// 后面某个观察者失败之前已经送出去的通知照样被看见了——每一条开始过的创建
	// 公布都会在回滚时配对地发出 agent/disposed 或者 session/disposed。
	CreateAgent(ctx context.Context, owner *scope.Scope, options CreateOptions) (Handle, error)

	// Resume 备好一段持久化会话并在它上面续跑一个 agent。
	//
	// 公布走的是和 CreateAgent 一样的 setup-commit 与次序边界。持久化服务不在位
	// 时它报错。
	Resume(ctx context.Context, owner *scope.Scope, options ResumeOptions) (Handle, error)
}

// factorySlot 把造法包一层，好让「撤销登记」比的是**这一次**登记而不是两个接口
// 值相不相等。
//
// 源: packages/core/agent/src/index.ts:240-242（FactorySlot）
//
// 新增: DSH 那个包装挡的是 cordis 在调用方上下文已知之前就追踪这个字段。Go 里
// 没有那回事，但它换来另一样东西：两个 [Factory] 接口值用 == 比，动态类型不可
// 比较（比如里面带切片）时会 panic。比指针不会。
type factorySlot struct{ target Factory }

// entry 是一个 agent 在注册表里那一份登记的全部可变状态。
//
// 源: packages/core/agent/src/index.ts:220-231（AgentEntry）
//
// 除了 id、agent、owner、carrierKey 四个造出来就不再变的字段，其余全部由
// [Registry.mutex] 守着。
type entry struct {
	id         sessionlog.SessionID
	agent      Agent
	carrierKey *scope.Key

	// owner 是「哪个活 agent 的作用域造出了这一个」，可以为 nil（顶层的运行时根）。
	//
	// 这是**运行期**的归属，和会话那份耐久的分叉血统是两回事：一个续跑起来的
	// 分叉照样可能是根。
	owner Agent

	// entered 是 [Registry.Enter] 交出去那个摘除函数的一次性标记。
	entered bool
	// announced 表示创建公布**开始过**（不是成功过）。
	announced bool
	// announcing 表示公布正在进行。
	announcing bool
	// detachRequested 是在公布窗口里提出、等窗口关掉再执行的摘除。
	detachRequested bool

	// status 是最近一次报过的状态，hasStatus 为假时它没有意义。
	//
	// 源: packages/core/agent/src/invariant.ts:15-22
	//
	// 新增: DSH 把这条不变式放在一个伴生插件里，用一个 `WeakMap<Agent, AgentStatus>`
	// 记上一次的状态，撞上就在开发构建里 fail()。这里把它挪进登记本身——同一份
	// 状态，而且随着登记一起消失，不必再为「agent 什么时候能被回收」操心。
	//
	// **第一次报任何状态都不算重复**：DSH 那个 WeakMap 上还没有这个 agent 的条目，
	// 对应到这里就是 hasStatus 为假。
	status    Status
	hasStatus bool
}

// RegistryOptions 是造一个 [Registry] 的选项。
//
// 新增: DSH 的 AgentRegistry 是 cordis Service，logger 从 ctx 上取。Go 里没有那个
// 隐式容器，显式传进来。
type RegistryOptions struct {
	// Logger 用来报告观察者自己 panic 的事故，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Registry 是活 agent 的内存表（DSH 那边的 `ctx.agents`）。
//
// 源: packages/core/agent/src/index.ts:244-298
//
// 造 agent 不在这里：那是循环那一层的事，由 [Registry.SetFactory] 把它的
// [Factory] 登记进来，[Registry.Create] 与 [Registry.Resume] 转交过去。
//
// 新增: 这个类型有自己的互斥锁，规矩和 [ds-harness-go/core/session.Store] 逐字
// 相同：**观察者一律在锁外调用**，于是一个观察者回头读同一张表不会自锁。
type Registry struct {
	layers *scope.Layers[*registryLayer]
	logger *slog.Logger

	// mutex 守住下面三个字段。观察者一律在锁外调用。
	mutex sync.Mutex
	// agents 是按标识查登记。
	agents map[sessionlog.SessionID]*entry
	// order 是登记进来的先后。
	//
	// 新增: DSH 用的是 JS 的 Map，它自带插入顺序，list()/roots() 直接遍历就是
	// 登记顺序。Go 的 map 没有顺序，而这个顺序**是语义**（DSH 的文档写明
	// 「in registration order」），所以另存一份。
	order []*entry
	// factory 是当下那份造法，nil 表示还没有人登记过。
	factory *factorySlot
}

// NewRegistry 造一张空表。
//
// 源: packages/core/agent/src/index.ts:266-298
//
// 新增: DSH 的构造函数里那一段 typert 登记与 ctx.accessor('agent') 都不移，
// 理由见包文档。
func NewRegistry(options RegistryOptions) (*Registry, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// onChange 传 nil：DSH 那边没有「agent 观察者名单变了」这回事，也没有任何东西
	// 需要为此重算缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*registryLayer, error) { return newRegistryLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		return nil, err
	}
	return &Registry{
		layers: layers,
		logger: logger,
		agents: map[sessionlog.SessionID]*entry{},
	}, nil
}

// ---- 登记 ----

// OnCreated 登记一个创建观察者，返回撤销这次登记的函数。
//
// 源: packages/core/agent/src/runtime-types.ts:148-159
//
// owner 决定这次登记落在哪一层：[scope.NewRoot] 造的作用域没有身份，落全局层，
// 看得见每一个 agent；有身份的作用域落它自己那一层，只看得见挂在它（或它的子孙）
// 那里的 agent。下面十二个登记方法这条规矩完全一样。
func (r *Registry) OnCreated(ctx context.Context, owner *scope.Scope, observer CreatedObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnCreated 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.created.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnCreated()"})
}

// OnDisposed 登记一个摘除观察者，返回撤销这次登记的函数。
//
// 源: packages/core/agent/src/runtime-types.ts:160-168
func (r *Registry) OnDisposed(ctx context.Context, owner *scope.Scope, observer DisposedObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnDisposed 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.disposed.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnDisposed()"})
}

// OnStatus 登记一个状态观察者，返回撤销这次登记的函数。
//
// 源: packages/core/agent/src/runtime-types.ts:169-178
//
// 报的是跃迁的**落点**。同一个状态连着报两次是循环那一层的缺陷，[Registry.ReportStatus]
// 自己会拦（[ErrStatusNoop]），所以观察者不必自己去重。
func (r *Registry) OnStatus(ctx context.Context, owner *scope.Scope, observer StatusObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnStatus 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.status.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnStatus()"})
}

// OnInboxInserted 登记一个「消息进了收件箱」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:179-186
func (r *Registry) OnInboxInserted(ctx context.Context, owner *scope.Scope, observer InboxObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnInboxInserted 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.inboxInserted.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnInboxInserted()"})
}

// OnInboxDiscarded 登记一个「消息被丢出收件箱」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:198-205
func (r *Registry) OnInboxDiscarded(ctx context.Context, owner *scope.Scope, observer InboxObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnInboxDiscarded 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.inboxDiscarded.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnInboxDiscarded()"})
}

// OnInboxClaimed 登记一个「消息在它那个已开回合里被认领走」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:187-197
func (r *Registry) OnInboxClaimed(ctx context.Context, owner *scope.Scope, observer InboxClaimedObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnInboxClaimed 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.inboxClaimed.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnInboxClaimed()"})
}

// OnSessionStart 登记一个会话生命周期开始的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:206-217
func (r *Registry) OnSessionStart(ctx context.Context, owner *scope.Scope, observer SessionStartObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnSessionStart 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.sessionStart.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnSessionStart()"})
}

// OnPreStep 把一个观察者挂到步骤准入那条瀑布上。
//
// 源: packages/core/agent/src/runtime-types.ts:219-231
func (r *Registry) OnPreStep(ctx context.Context, owner *scope.Scope, observer PreStepObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnPreStep 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.preStep.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnPreStep()"})
}

// OnRequest 把一个观察者挂到调用配置那条瀑布上。
//
// 源: packages/core/agent/src/runtime-types.ts:232-244
func (r *Registry) OnRequest(ctx context.Context, owner *scope.Scope, observer RequestObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnRequest 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.request.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnRequest()"})
}

// OnRequestError 把一个观察者挂到请求失败恢复那条瀑布上。
//
// 源: packages/core/agent/src/runtime-types.ts:245-260
func (r *Registry) OnRequestError(ctx context.Context, owner *scope.Scope, observer RequestErrorObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnRequestError 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.requestError.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnRequestError()"})
}

// OnTurnStopping 登记一个回合收尾边界上的串行观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:261-278
func (r *Registry) OnTurnStopping(ctx context.Context, owner *scope.Scope, observer TurnStoppingObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnTurnStopping 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.turnStopping.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnTurnStopping()"})
}

// OnError 登记一个「步骤或者回合出错了」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:279-290
func (r *Registry) OnError(ctx context.Context, owner *scope.Scope, observer ErrorObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("%w：OnError 需要一个观察者", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *registryLayer) (func(), error) {
		return layer.turnErrored.Append(observer), nil
	}, scope.EffectOptions{Label: "agents.OnError()"})
}

// ---- 造法 ----

// SetFactory 登记那份 agent 造法，返回撤销这次登记的函数。
//
// 源: packages/core/agent/src/index.ts:360-388
//
// 一张表只认一份造法：已经有一份在位时报 [ErrFactoryAlreadySet]。撤销之后可以
// 再登记，那是循环那一层重载时的正常路径。
//
// 新增: DSH 把这次登记裹进 `ctx.effect(...)`，撤销函数由 cordis 交出来，而且
// 它那句注释强调「必须交出**那一个**函数」以便复合 effect 按位置嵌套拆除。
// Go 里作用域拆除的次序由调用方自己 [scope.Scope.Defer] 的先后决定，所以这里
// 只把撤销交出去，挂哪儿由调用方定——和 [Registry.Enter] 同一条规矩。
func (r *Registry) SetFactory(factory Factory) (func(context.Context) error, error) {
	if factory == nil {
		return nil, fmt.Errorf("%w：SetFactory 需要一份造法", ErrInvalidRegistration)
	}
	slot := &factorySlot{target: factory}

	r.mutex.Lock()
	if r.factory != nil {
		r.mutex.Unlock()
		return nil, ErrFactoryAlreadySet
	}
	r.factory = slot
	r.mutex.Unlock()

	return func(context.Context) error {
		r.mutex.Lock()
		defer r.mutex.Unlock()
		// 比指针：一次过期的撤销清不掉后来那一份登记。
		if r.factory == slot {
			r.factory = nil
		}
		return nil
	}, nil
}

// requireFactory 取出当下那份造法。
//
// 源: packages/core/agent/src/index.ts:390-394
func (r *Registry) requireFactory() (Factory, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.factory == nil {
		return nil, ErrNoFactory
	}
	return r.factory.target, nil
}

// Create 通过登记着的那份造法造一个新 agent 并公布出去。
//
// 源: packages/core/agent/src/index.ts:396-415
//
// 它和 [Registry.Enter]（记下一个**已经造好**的 agent）是两件事：这一个连它的
// 会话一起造出来。没有造法、或者创建／setup 失败时报错。交回来的 [Handle] 让
// 主人拆得掉**恰好这一个** agent。
//
// owner 是拥有这次事务和那个活句柄的作用域，由调用方显式交进来。
func (r *Registry) Create(ctx context.Context, owner *scope.Scope, options CreateOptions) (Handle, error) {
	factory, err := r.requireFactory()
	if err != nil {
		return Handle{}, err
	}
	return factory.CreateAgent(ctx, owner, options)
}

// Resume 读回一段持久化会话，并通过登记着的那份造法在它上面续跑一个 agent。
//
// 源: packages/core/agent/src/index.ts:417-430
func (r *Registry) Resume(ctx context.Context, owner *scope.Scope, options ResumeOptions) (Handle, error) {
	factory, err := r.requireFactory()
	if err != nil {
		return Handle{}, err
	}
	return factory.Resume(ctx, owner, options)
}

// ---- 登记、公布、摘除 ----

// Register 把一个已经造好的活 agent 登记进来并当场公布，返回摘除它的函数。
//
// 源: packages/core/agent/src/index.ts:432-457
//
// 这是普通调用方那条路：一次调用走完 [Registry.Enter] 加 [Registry.Announce]。
// 公布被否决时这次登记整个不算数——摘除跑掉，配对的 disposed 发出去，返回的是
// 公布那条错误。
//
// owner 是这个 agent 运行期的主人（造出它的那个父 agent），顶层的根传 nil。
// 它和会话那份耐久的分叉血统无关。
//
// 一个 agent 必须**和它的循环按次序**一起拆掉的时候不要用这个方法——用
// [Registry.Enter] + [Registry.Announce] 把摘除折进那条复合拆除链里，理由和
// [ds-harness-go/core/session.Store.Create] 上那条逐字相同。
func (r *Registry) Register(ctx context.Context, agent Agent, owner Agent) (func(context.Context) error, error) {
	detach, err := r.Enter(agent, owner)
	if err != nil {
		return nil, err
	}
	if err := r.Announce(ctx, agent); err != nil {
		return nil, errors.Join(err, detach(ctx))
	}
	return detach, nil
}

// Enter 把一个已经造好的 agent 登记进表里但**不**公布，返回摘除它的函数。
//
// 源: packages/core/agent/src/index.ts:459-509
//
// 这是那条按次序拆生命周期用的进阶原语，异步的 agent 工厂靠它：先在 agent 还没
// 公布的时候把 setup 跑完，再把这个摘除闭包装进它那条预先装好的复合拆除链，
// 然后才调 [Registry.Announce]。普通调用方用 [Registry.Register]。
//
// agent 的身份必须和它那个会话的身份一致，对不上报 [ErrIdentityMismatch]——
// 两个身份从来就该是同一个，对不上说明造它的那一方有缺陷。
//
// 这里是**权威的**撞名边界：并发的 create／resume 可以都备好，但只有一份公布得了。
//
// 交回来的闭包是幂等的：它摘掉**恰好这一份**登记，并在公布过的情况下发一次配对的
// disposed，观察者的事故逐个兜住。从一个同步的创建观察者里调它，摘除和那条配对的
// disposed 会等到那次创建派发退栈之后再做。
func (r *Registry) Enter(agent Agent, owner Agent) (func(context.Context) error, error) {
	if agent == nil {
		return nil, fmt.Errorf("%w：登记的 agent 不能是 nil", ErrInvalidRegistration)
	}
	agentScope := agent.Scope()
	if agentScope == nil {
		return nil, fmt.Errorf("%w：登记一个 agent 需要一个载体作用域", ErrInvalidRegistration)
	}
	id := agent.ID()
	session := agent.Session()
	if session == nil {
		return nil, fmt.Errorf("%w：agent %q 没有会话", ErrIdentityMismatch, string(id))
	}
	if sessionID := session.ID(); id != sessionID {
		return nil, fmt.Errorf("%w：agent 身份是 %q，会话身份是 %q",
			ErrIdentityMismatch, string(id), string(sessionID))
	}

	r.mutex.Lock()
	if _, taken := r.agents[id]; taken {
		r.mutex.Unlock()
		return nil, fmt.Errorf("%w：agent %q", ErrAgentAlreadyExists, string(id))
	}
	e := &entry{
		id:    id,
		agent: agent,
		// 载体是 agent 自己那把作用域钥匙——DSH 的 scopeTarget(agent, agent)。
		// 主体就是手上这个 agent，所以派发的作用域过滤和「是谁调的 Enter」无关。
		carrierKey: agentScope.Key(),
		owner:      owner,
		entered:    true,
	}
	r.agents[id] = e
	r.order = append(r.order, e)
	r.mutex.Unlock()

	return func(context.Context) error {
		r.detach(e)
		return nil
	}, nil
}

// detach 是 [Registry.Enter] 交出去那个摘除函数的实现，一次性。
//
// 源: packages/core/agent/src/index.ts:490-508
func (r *Registry) detach(e *entry) {
	r.mutex.Lock()
	if !e.entered {
		r.mutex.Unlock()
		return
	}
	e.entered = false
	if e.announcing {
		e.detachRequested = true
		r.mutex.Unlock()
		return
	}
	announced := r.detachEnteredLocked(e)
	r.mutex.Unlock()

	if announced {
		r.emitDisposed(e)
	}
}

// detachEnteredLocked 把这一份**确切的**登记从表里摘掉，交出「它公布过没有」。
//
// 源: packages/core/agent/src/index.ts:511-525
//
// 调用时必须拿着 [Registry.mutex]。返回真表示调用方要在锁外发一次配对的 disposed。
//
// 一次在公布之前就回滚掉的插入从来没有对外创建过，为它发一条 disposed 等于凭空
// 造出一条不可能的生命周期边。
func (r *Registry) detachEnteredLocked(e *entry) bool {
	e.detachRequested = false
	// 一个过期的摘除能力删不掉后来那一次同名生命周期。
	//
	// 单线程下测不到：这份登记还在表里的时候 [Registry.Enter] 就把同名的第二次挡住
	// 了，而摘除本身由 e.entered 保证只跑一次——两条合起来，这里读到的永远是自己。
	// 留着它是因为这两条各自成立的理由不同，将来任何一条松动都会让它变成活路。
	if r.agents[e.id] != e {
		return false
	}
	delete(r.agents, e.id)
	for index, candidate := range r.order {
		if candidate == e {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	return e.announced
}

// Announce 为一个已登记的 agent 发出**恰好一次**创建公布。
//
// 源: packages/core/agent/src/index.ts:527-576
//
// 和 [Registry.Enter] 分开，是为了让调用方先把摘除挂上去（回滚安全，见 Enter）。
//
// 一个观察者失败就当场停下并把错误交出去——这是**否决**：调用方拿着的摘除函数
// 随即把这次登记撤掉，并配对地发一次 disposed。已经跑过的那几个观察者看见过这个
// agent，所以那条配对的边必须补上，这也是 announced 在派发**之前**就置真的理由。
func (r *Registry) Announce(ctx context.Context, agent Agent) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}

	r.mutex.Lock()
	if e.announced || e.announcing {
		r.mutex.Unlock()
		return fmt.Errorf("%w：agent %q", ErrAlreadyAnnounced, string(e.id))
	}
	// 先立标记再派发：一个观察者不能在回调里递归地再公布一次，而一次跑到一半就被
	// 否决的公布，回滚时照样要配对地发出 disposed。
	e.announced = true
	e.announcing = true
	r.mutex.Unlock()

	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[CreatedObserver] {
		return layer.created
	})
	var failure error
	for _, observer := range observers {
		if err := callCreatedObserver(ctx, observer, agent); err != nil {
			failure = fmt.Errorf("agent %q：创建观察者否决了这次公布：%w", string(e.id), err)
			break
		}
	}

	r.mutex.Lock()
	e.announcing = false
	announced := false
	if e.detachRequested {
		announced = r.detachEnteredLocked(e)
	}
	r.mutex.Unlock()
	if announced {
		r.emitDisposed(e)
	}
	return failure
}

// emitDisposed 发出那条配对的拆除通知，观察者的事故逐个兜住。
//
// 源: packages/core/agent/src/index.ts:527-540
func (r *Registry) emitDisposed(e *entry) {
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[DisposedObserver] {
		return layer.disposed
	})
	for _, observer := range observers {
		r.callNotify(e.id, "摘除", func() { observer(e.agent) })
	}
}

// ---- 查询 ----

// Get 查一个活着的 agent。
//
// 源: packages/core/agent/src/index.ts:578-585
func (r *Registry) Get(id sessionlog.SessionID) (Agent, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	e, live := r.agents[id]
	if !live {
		return nil, false
	}
	return e.agent, true
}

// IsOwnedBy 问「这个活 agent 是不是从 owner 那个作用域里造出来的」。
//
// 源: packages/core/agent/src/index.ts:587-597
//
// 运行期归属和会话那份耐久血统各管各的，所以哪怕两个不相干的提供方复用了同一个
// 标识，这个答案也不含糊。
func (r *Registry) IsOwnedBy(id sessionlog.SessionID, owner Agent) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	e, live := r.agents[id]
	return live && e.owner == owner
}

// List 给出所有活着的 agent，按登记进来的先后。
//
// 源: packages/core/agent/src/index.ts:599-605
//
// 交回的切片是新的，改它动不了注册表。
func (r *Registry) List() []Agent {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	agents := make([]Agent, 0, len(r.order))
	for _, e := range r.order {
		agents = append(agents, e.agent)
	}
	return agents
}

// Roots 给出所有活着的顶层 agent，按登记进来的先后。
//
// 源: packages/core/agent/src/index.ts:607-617
//
// 顶层的意思是「造它的时候没有一个拥有它的 agent 作用域」。耐久的会话血统不影响
// 这条运行期关系，所以一个续跑起来的分叉照样可能是根。
func (r *Registry) Roots() []Agent {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var roots []Agent
	for _, e := range r.order {
		if e.owner == nil {
			roots = append(roots, e.agent)
		}
	}
	return roots
}

// liveEntryFor 取出这个 agent **确切的**那份活登记。
func (r *Registry) liveEntryFor(agent Agent) (*entry, error) {
	if agent == nil {
		return nil, fmt.Errorf("%w：agent 是 nil", ErrAgentNotLive)
	}
	id := agent.ID()

	r.mutex.Lock()
	defer r.mutex.Unlock()
	e, live := r.agents[id]
	if !live || e.agent != agent {
		return nil, fmt.Errorf("%w：agent %q", ErrAgentNotLive, string(id))
	}
	return e, nil
}

// ---- 派发 ----

// ReportStatus 报一次状态跃迁。
//
// 源: packages/core/agent/src/runtime-types.ts:169-178
//
// 源: packages/core/agent/src/invariant.ts:15-22
//
// 同一个状态连着报两次报 [ErrStatusNoop]，而且**不派发**：那是循环那一层的缺陷，
// 一条不动的跃迁传下去只会让每一个观察者各自去重一遍。第一次报任何状态都不算
// 重复，理由见 [entry.status]。
//
// 新增: DSH 那条不变式只在开发构建里 fail()，生产构建照发。这里一律拦——一个
// 只在某种构建下成立的检查，等于两套语义。
func (r *Registry) ReportStatus(agent Agent, status Status) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}

	r.mutex.Lock()
	if e.hasStatus && e.status == status {
		r.mutex.Unlock()
		return fmt.Errorf("%w：agent %q 又报了一次 %q", ErrStatusNoop, string(e.id), status)
	}
	e.status = status
	e.hasStatus = true
	r.mutex.Unlock()

	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[StatusObserver] {
		return layer.status
	})
	for _, observer := range observers {
		r.callNotify(e.id, "状态", func() { observer(agent, status) })
	}
	return nil
}

// ReportInboxInserted 报一条消息进了收件箱。
//
// 源: packages/core/agent/src/runtime-types.ts:179-186
func (r *Registry) ReportInboxInserted(agent Agent, message llm.Message) error {
	return r.reportInbox(agent, message, "收件箱插入", func(layer *registryLayer) *scope.AnonymousEntries[InboxObserver] {
		return layer.inboxInserted
	})
}

// ReportInboxDiscarded 报一条消息被丢出了收件箱。
//
// 源: packages/core/agent/src/runtime-types.ts:198-205
func (r *Registry) ReportInboxDiscarded(agent Agent, message llm.Message) error {
	return r.reportInbox(agent, message, "收件箱丢弃", func(layer *registryLayer) *scope.AnonymousEntries[InboxObserver] {
		return layer.inboxDiscarded
	})
}

// reportInbox 是那两条形状相同的收件箱通知共用的实现。
func (r *Registry) reportInbox(
	agent Agent,
	message llm.Message,
	what string,
	pick func(*registryLayer) *scope.AnonymousEntries[InboxObserver],
) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}
	for _, observer := range collectObservers(r, e.carrierKey, pick) {
		r.callNotify(e.id, what, func() { observer(agent, message) })
	}
	return nil
}

// ReportInboxClaimed 报一条消息在它那个已开回合里被认领走了。
//
// 源: packages/core/agent/src/runtime-types.ts:187-197
func (r *Registry) ReportInboxClaimed(agent Agent, message llm.Message, turn int) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[InboxClaimedObserver] {
		return layer.inboxClaimed
	})
	for _, observer := range observers {
		r.callNotify(e.id, "收件箱认领", func() { observer(agent, message, turn) })
	}
	return nil
}

// ReportSessionStart 报一段会话生命周期开始了。
//
// 源: packages/core/agent/src/runtime-types.ts:206-217
func (r *Registry) ReportSessionStart(agent Agent, source SessionStartSource) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[SessionStartObserver] {
		return layer.sessionStart
	})
	for _, observer := range observers {
		r.callNotify(e.id, "会话开始", func() { observer(agent, source) })
	}
	return nil
}

// ReportError 报一次在步骤或者回合里冒出来的失败。
//
// 源: packages/core/agent/src/runtime-types.ts:279-290
//
// 机器在这里报失败，哪怕那个错误在回合里找不到一个位置留下耐久记录。
func (r *Registry) ReportError(failure TurnError) error {
	e, err := r.liveEntryFor(failure.Agent)
	if err != nil {
		return err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[ErrorObserver] {
		return layer.turnErrored
	})
	for _, observer := range observers {
		r.callNotify(e.id, "回合失败", func() { observer(failure) })
	}
	return nil
}

// TurnStopping 跑一遍回合收尾边界上那些串行观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:261-278
//
// 按登记顺序一个个跑完，第一个失败当场停下并交出去——这条边界在提交之前，
// 一个失败的观察者说明收尾这件事本身出了问题。
//
// 新增: 观察者的 panic 转成错误而不是记日志兜掉。理由和创建那一侧同理：这条
// 边界的返回值**就是**「可以关这个回合了」，一个 panic 掉的观察者到底是「我反对」
// 还是「我这儿坏了」分不出来，当成失败最坏是多跑一个步骤。
func (r *Registry) TurnStopping(ctx context.Context, agent Agent, turn int) error {
	e, err := r.liveEntryFor(agent)
	if err != nil {
		return err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[TurnStoppingObserver] {
		return layer.turnStopping
	})
	for _, observer := range observers {
		if err := callTurnStopping(ctx, observer, agent, turn); err != nil {
			return fmt.Errorf("agent %q：回合收尾观察者失败：%w", string(e.id), err)
		}
	}
	return nil
}

// ResolvePreStep 跑步骤准入那条瀑布，交出「这个提议进不进」。
//
// 源: packages/core/agent/src/runtime-types.ts:219-231
//
// base 是最里面那一层，交出机器本来的提议。
func (r *Registry) ResolvePreStep(
	ctx context.Context,
	step PreStep,
	base func(context.Context) (PreStepDecision, error),
) (PreStepDecision, error) {
	e, err := r.liveEntryFor(step.Agent)
	if err != nil {
		return PreStepDecision{}, err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[PreStepObserver] {
		return layer.preStep
	})
	var walk func(index int, ctx context.Context) (PreStepDecision, error)
	walk = func(index int, ctx context.Context) (PreStepDecision, error) {
		if index >= len(observers) {
			return base(ctx)
		}
		return observers[index](ctx, step, func(ctx context.Context) (PreStepDecision, error) {
			return walk(index+1, ctx)
		})
	}
	return walk(0, ctx)
}

// ResolveRequest 跑调用配置那条瀑布，交出这次请求真正要用的那一份。
//
// 源: packages/core/agent/src/runtime-types.ts:232-244
//
// base 交出机器本来会用的那一份——第一次请求是 agent 选项，之后是日志里那份请求头。
func (r *Registry) ResolveRequest(
	ctx context.Context,
	request Request,
	base func(context.Context) (llm.CallConfig, error),
) (llm.CallConfig, error) {
	e, err := r.liveEntryFor(request.Agent)
	if err != nil {
		return llm.CallConfig{}, err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[RequestObserver] {
		return layer.request
	})
	var walk func(index int, ctx context.Context) (llm.CallConfig, error)
	walk = func(index int, ctx context.Context) (llm.CallConfig, error) {
		if index >= len(observers) {
			return base(ctx)
		}
		return observers[index](ctx, request, func(ctx context.Context) (llm.CallConfig, error) {
			return walk(index+1, ctx)
		})
	}
	return walk(0, ctx)
}

// ResolveRequestError 跑请求失败恢复那条瀑布，交出「要不要再试一次」。
//
// 源: packages/core/agent/src/runtime-types.ts:245-260
//
// base 交出默认的那个决定——零值，也就是「这次失败是终局」。
func (r *Registry) ResolveRequestError(
	ctx context.Context,
	failure RequestFailure,
	base func(context.Context) (RequestErrorAction, error),
) (RequestErrorAction, error) {
	e, err := r.liveEntryFor(failure.Agent)
	if err != nil {
		return RequestErrorAction{}, err
	}
	observers := collectObservers(r, e.carrierKey, func(layer *registryLayer) *scope.AnonymousEntries[RequestErrorObserver] {
		return layer.requestError
	})
	var walk func(index int, ctx context.Context) (RequestErrorAction, error)
	walk = func(index int, ctx context.Context) (RequestErrorAction, error) {
		if index >= len(observers) {
			return base(ctx)
		}
		return observers[index](ctx, failure, func(ctx context.Context) (RequestErrorAction, error) {
			return walk(index+1, ctx)
		})
	}
	return walk(0, ctx)
}

// ---- 派发的公共零件 ----

// collectObservers 把全局层和载体作用域父链上各层的同一张表叠成一份名单。
//
// 源: packages/core/agent/src/dispatch.ts:107-149（agentEvents 那三个派发口）
//
// 顺序是全局在前、远祖次之、载体自己最后——和本仓库其他几处作用域派发一致。
// 瀑布靠这个顺序定内外层：先登记的在外面。
func collectObservers[T any](
	registry *Registry,
	key *scope.Key,
	pick func(*registryLayer) *scope.AnonymousEntries[T],
) []T {
	var observers []T
	for observer := range pick(registry.layers.Global()).Values() {
		observers = append(observers, observer)
	}
	if key == nil {
		return observers
	}
	for _, layer := range registry.layers.ChainLayers(key) {
		for observer := range pick(layer).Values() {
			observers = append(observers, observer)
		}
	}
	return observers
}

// callCreatedObserver 跑一个创建观察者，把它的 panic 转成一条否决。
//
// panic 也算否决不是随手定的，理由和 [ds-harness-go/core/session.Store.Announce]
// 那一侧逐字相同：这个观察者的返回值**就是**否决权，而一个 panic 到底是「拒绝」
// 还是「我这儿坏了」分不出来。
func callCreatedObserver(ctx context.Context, observer CreatedObserver, agent Agent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("观察者 panic 了：%v", recovered)
		}
	}()
	return observer(ctx, agent)
}

// callTurnStopping 跑一个回合收尾观察者，把它的 panic 转成一条失败。
func callTurnStopping(ctx context.Context, observer TurnStoppingObserver, agent Agent, turn int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("观察者 panic 了：%v", recovered)
		}
	}()
	return observer(ctx, agent, turn)
}

// callNotify 跑一个只通知的观察者，把它的 panic 兜成一条日志。
//
// 源: packages/core/agent/src/index.ts:528-540（emitDisposed 里那两层 try）
//
// 只通知的那八条边一律走这里：事情已经发生了，一个观察者坏了既不能让它回头变成
// 失败，也不能挡住后面的观察者看见它。
func (r *Registry) callNotify(id sessionlog.SessionID, what string, run func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("core/agent: 通知观察者 panic 了",
				"agent", string(id), "通知", what, "panic", fmt.Sprint(recovered))
		}
	}()
	run()
}
