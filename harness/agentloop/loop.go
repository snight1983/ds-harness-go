// 本文件的作用：具体那个 agent 循环工厂——它铸出带作用域的 [ReactLoopAgent]，
// 经 agent 与会话两张注册表把它们公布出去，并拥有它们那条按次序的拆除链。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/settings"
)

// ---- 启动器指定的会话身份 ----

// LauncherAgentIdentity 是启动器为一个配置出来的 agent 选定的那一个会话身份。
//
// 源: packages/core/agent-loop/src/index.ts:245-256（LauncherAgentIdentity）
//
// Resume 把「读回已有的持久化历史」和「用这个确切身份新建一个会话」分开，
// 这两件事在配置里对应 resumeSessionId 和 sessionId 两个键。
type LauncherAgentIdentity struct {
	// ID 是要新建或者续跑的那个确切会话标识。
	ID sessionlog.SessionID
	// Resume 为真表示读回已有的持久化历史，而不是新建这个会话。
	Resume bool
}

// ConfiguredAgentIdentities 是按配置项的 id 索引的那些启动器身份。
//
// 源: packages/core/agent-loop/src/index.ts:258-259（ConfiguredAgentIdentities）
//
// 新增: DSH 那边这份表是启动器在任何 Loader 条目挂载之前经
// `ctx.provide(CONFIGURED_AGENT_IDENTITIES_KEY, ...)` 放到 cordis 上下文上的，
// 目的是「让身份不经过配置键，这样一份改掉模型路由的覆盖配置就冲不掉它」。
// Go 里没有那个万能上下文，这份表就是 [Config.LauncherIdentities] 一个字段；
// 「覆盖配置冲不掉它」在 Go 里由 [applyLauncherIdentities] 的执行次序保证——
// 它在校验之前跑，且**两个身份键一起换掉**。
// 也因此 DSH 那个 CONFIGURED_AGENT_IDENTITIES_KEY 常量在这里没有对应物。
type ConfiguredAgentIdentities map[string]LauncherAgentIdentity

// applyLauncherIdentities 把启动器指定的身份盖到配置出来的那些 agent 上。
//
// 源: packages/core/agent-loop/src/index.ts:213-233
//
// 每一个被启动器点名的条目，**两个身份键一起换掉**——这样一个配置里写的身份
// 永远不可能和一个启动器给的身份并存。没被点名的条目原样保留自己的身份。
func applyLauncherIdentities(
	agents []ConfiguredAgent,
	identities ConfiguredAgentIdentities,
) []ConfiguredAgent {
	if identities == nil {
		return agents
	}
	applied := make([]ConfiguredAgent, len(agents))
	for index, configured := range agents {
		identity, named := identities[configured.ID]
		if !named {
			applied[index] = configured
			continue
		}
		configured.SessionID = ""
		configured.ResumeSessionID = ""
		if identity.Resume {
			configured.ResumeSessionID = identity.ID
		} else {
			configured.SessionID = identity.ID
		}
		applied[index] = configured
	}
	return applied
}

// ---- 设置 ----

// SettingsNamespace 是本包那个设置小节的命名空间。
//
// 源: packages/core/agent-loop/src/index.ts:292-293（AGENT_LOOP_SETTINGS_NAMESPACE）
var SettingsNamespace = mustNamespace("agent-loop")

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: DSH 的 settingsNamespace() 在 TS 里就是一次品牌化转换，编译期定死。
// Go 里 [settings.NewNamespace] 会验一遍并返回错误，而这里的入参是一个包级常量
// 字面量——它不合法说明本包写错了，不是运行期可以恢复的情况。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("harness/agentloop: 命名空间字面量不合法：%v", err))
	}
	return namespace
}

// Settings 是本包里那几个由用户拥有的字段。
//
// 源: packages/core/agent-loop/src/index.ts:295-303（AgentLoopSettings）
//
// 它**刻意**是 [Config] 的一个真子集：Agents 是一份开机时消费一次的组装清单，
// 存下来的改动只会看起来像是生效了。
type Settings struct {
	// MaxParallelToolCalls 是每个步骤里同时在飞的并行安全调用上限。
	MaxParallelToolCalls int `json:"maxParallelToolCalls"`
}

// ---- 配置 ----

// ConfiguredAgent 是配置里声明的一个开机就起的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:260-271
type ConfiguredAgent struct {
	// ID 是这一项在日志里的稳定标签，也是铸新身份时的前缀。
	ID string
	// SessionID 是可选的稳定身份：重挂时读回它已经落地的历史，第一次用则新建它。
	SessionID sessionlog.SessionID
	// WorkspaceID 是新建会话时归属的工作区登记；空串表示不属于任何工作区。
	WorkspaceID sessionlog.WorkspaceID
	// ResumeSessionID 是要续跑的那段持久化会话，给了就不新建。
	ResumeSessionID sessionlog.SessionID
	// Options 是这个 agent 自己的提供方路由与模型。
	Options agent.Options
}

// Config 是这个循环工厂的配置。
//
// 源: packages/core/agent-loop/src/index.ts:310-328（Config）
type Config struct {
	// MaxParallelToolCalls 是每个步骤里同时在飞的并行安全调用上限。
	// 1 表示串行；0 表示没给，用 [DefaultMaxParallelToolCalls]。
	//
	// 新增: DSH 是 `maxParallelToolCalls?: number`，分得开「没给」和「给了 0」。
	// Go 里这里用 0 表示没给，理由和 [github.com/snight1983/ds-harness-go/llm.CallConfig].MaxTokens
	// 那一条一样：上限为零的池一个工具调用都跑不动，没人会那么要求，
	// 所以零值不和任何真实取值撞车。
	MaxParallelToolCalls int

	// Agents 是插件启动时就创建或者续跑的那些 agent。
	Agents []ConfiguredAgent

	// LauncherIdentities 是启动器指定的那些会话身份，按 [ConfiguredAgent.ID] 索引。
	// 为 nil 表示每一项都保留自己配置里的身份。
	LauncherIdentities ConfiguredAgentIdentities

	// Settings 是那份设置提供方，为 nil 表示本部署不让用户改并行上限——那时
	// 上限锁在 MaxParallelToolCalls 解出来的那个值上。
	//
	// 新增: DSH 的 installSettingsSection 拿的是 cordis 上下文，设置服务在不在
	// 由 cordis 决定。Go 里它是一个显式的、可以为 nil 的依赖。
	Settings *settings.Provider

	// Persistence 是会话持久化，为 nil 表示本部署没接持久化——那时
	// [AgentLoop.Resume] 报错，配置里那些要续跑的项也起不来。
	Persistence SessionPersistence

	// Logger 用来报配置驱动的启动失败，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// SessionPersistence 是本包**用得上的那一小块**会话持久化能力。
//
// 源: packages/core/agent-loop/src/index.ts:28（`import type { SessionPersistence }`）
//
// 新增: 这是一个**消费方一侧**声明的接口，不是从持久化那个包引来的。DSH 引的是
// 那边的服务类型，而 Go 这边 [github.com/snight1983/ds-harness-go/feature/persistence.Store] 到目前为止
// 还没有 Prepare（它那份文档说明协调器、会话预备件和 Store.Prepare 一起推迟到收尾
// 那一块做）。在消费方这边按**实际用到的两个方法**声明接口，是 Go 的常规做法：
// 本包因此不必等那个包写完，而那个包写完之后，只要方法名对得上就直接满足这里。
type SessionPersistence interface {
	// Prepare 读回一段持久化会话，交出一段**还没登记进存储**的准备期。
	//
	// 交出来的会话由调用方负责公布或者丢弃，两条路都要调
	// [github.com/snight1983/ds-harness-go/harness/session.Preparation.Release]：
	// 提供方（比如持久化编排器）在准备期里攥着一份预留，公布成了它被接手、
	// 半路放弃了它要还回去。释放是幂等的，所以直接 defer 就行。
	Prepare(ctx context.Context, id sessionlog.SessionID) (*session.Preparation, error)

	// List 列出所有落了地的会话头。
	//
	// [AgentLoop.restoreOrCreateConfigured] 拿它区分「这个存档根本不存在」
	// 和「读它的时候出事了」。
	List(ctx context.Context) ([]sessionlog.SessionHeader, error)
}

// ---- 配置校验 ----

// resolveMaxParallelToolCalls 在拥有这份配置的边界上定下那个部署级的调度上限。
//
// 源: packages/core/agent-loop/src/index.ts:132-139
func resolveMaxParallelToolCalls(value int) (int, error) {
	if value == 0 {
		return DefaultMaxParallelToolCalls, nil
	}
	if value < 1 {
		return 0, errors.New("maxParallelToolCalls must be a positive integer")
	}
	return value, nil
}

// assertAgentOptions 拒掉一个在请求介质上表达不出来的输出上限。
//
// 源: packages/core/agent-loop/src/index.ts:141-147
//
// 新增: DSH 查的是 `Number.isSafeInteger(maxTokens) && maxTokens > 0`——JS 的
// number 是浮点，非整数和超出安全整数范围的值都得挡。Go 的 int 天生是整数，
// 所以只剩下正负这一条。0 在 Go 这边表示「不设」（见 [agent.Options].MaxTokens），
// 对应 DSH 的 undefined，照样放行。
func assertAgentOptions(options agent.Options) error {
	if options.MaxTokens < 0 {
		return errors.New("agent maxTokens must be a positive safe integer")
	}
	return nil
}

// validateConfiguredAgents 在任何配置出来的 agent 起步之前，拒掉这份配置自己内部的身份冲突。
//
// 源: packages/core/agent-loop/src/index.ts:277-293
func validateConfiguredAgents(agents []ConfiguredAgent) error {
	exactIdentities := make(map[sessionlog.SessionID]string, len(agents))
	for _, configured := range agents {
		hasResumeID := configured.ResumeSessionID != ""
		if configured.SessionID != "" && hasResumeID {
			return fmt.Errorf("agent %q: sessionId and resumeSessionId are mutually exclusive", configured.ID)
		}
		exactIdentity := configured.SessionID
		if hasResumeID {
			exactIdentity = configured.ResumeSessionID
		}
		if exactIdentity == "" {
			continue
		}
		if firstID, taken := exactIdentities[exactIdentity]; taken {
			return fmt.Errorf("agents %q and %q use duplicate exact session identity %q",
				firstID, configured.ID, string(exactIdentity))
		}
		exactIdentities[exactIdentity] = configured.ID
	}
	return nil
}

// ---- 配置启动失败的通报 ----

// ConfigStartFailedObserver 收一次「一个声明式的 agent 项在公布出活 agent 之前就失败了」。
//
// 源: packages/core/agent-loop/src/index.ts:170-179
//
// 那些为这个身份缓存活儿的消费方拿这个瞬时信号去拒掉那些活儿，而不是永远等下去。
// 工厂正常拆除时被取消掉的那次启动**不**通报。
//
// 新增: DSH 是 cordis 的一条 `emit` 事件。Go 里按本仓库一贯的规矩换成显式的
// 观察者登记，见 [AgentLoop.OnConfigStartFailed]。
type ConfigStartFailedObserver func(sessionID sessionlog.SessionID, err error)

// ---- 工厂级归属 ----

// factoryOwnership 是工厂这一级的归属：活着的那些 agent 的拆除，加上配置驱动的启动活儿。
//
// 源: packages/core/agent-loop/src/index.ts:95-146（FactoryOwnership）
//
// 新增: DSH 那个类里有三样东西——一个 accepting 标志、一个 AbortController（拆除
// 开始时以 `agent loop is not active` 中止）、以及一个 `Promise.withResolvers<void>`
// （waitWhileActive 用来在拆除开始时停止等待）。Go 里后两样合成**一个**
// [context.WithCancelCause]：Done() 顶那个 promise，Cause() 顶那个中止原因。
// DSH 需要两个对象，是因为 AbortSignal 的 reason 和一个 resolve 掉的 promise
// 在 JS 里是两种东西。
//
// 新增: DSH 还查 `!INACTIVE_STATES.has(this.fiber.state)`——那是 cordis 的纤程状态。
// Go 里没有纤程，「这个工厂还接不接活」完全由 accepting 这一位说了算，
// 而它由拥有这个工厂的那个作用域在拆除时翻掉。
type factoryOwnership struct {
	mutex     sync.Mutex
	accepting bool
	// liveAgents 里是那些活着的 agent 各自那份**共享的**拆除函数。
	// 用 map 而不是切片：一份拆除跑完之后要按身份摘掉，见 track 交出来的 untrack。
	liveAgents map[*agentTeardown]struct{}

	teardown context.Context
	cancel   context.CancelCauseFunc

	// startup 等的是那些在任何 agent 存在之前就开跑的配置启动活儿。
	startup sync.WaitGroup
}

// agentTeardown 是一份活 agent 的共享拆除，取地址当身份用。
type agentTeardown struct {
	dispose func(context.Context) error
}

// errLoopNotActive 是工厂拆除时盖到那个归属上下文上的原因。
//
// 源: packages/core/agent-loop/src/index.ts:82
var errLoopNotActive = errors.New("agent loop is not active")

// newFactoryOwnership 造一份工厂归属。
func newFactoryOwnership() *factoryOwnership {
	teardown, cancel := context.WithCancelCause(context.Background())
	return &factoryOwnership{
		accepting:  true,
		liveAgents: make(map[*agentTeardown]struct{}),
		teardown:   teardown,
		cancel:     cancel,
	}
}

// isActive 判这个工厂还接不接新的生命周期。
//
// 源: packages/core/agent-loop/src/index.ts:55-57
func (o *factoryOwnership) isActive() bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.accepting
}

// track 记下一个活 agent 那份共享的拆除，直到它跑过为止；返回摘掉这一份的函数。
//
// 源: packages/core/agent-loop/src/index.ts:59-63
func (o *factoryOwnership) track(dispose func(context.Context) error) func() {
	handle := &agentTeardown{dispose: dispose}

	o.mutex.Lock()
	o.liveAgents[handle] = struct{}{}
	o.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			o.mutex.Lock()
			delete(o.liveAgents, handle)
			o.mutex.Unlock()
		})
	}
}

// trackStartup 把一件在任何 agent 存在之前就开跑的配置启动活儿并进来。
//
// 源: packages/core/agent-loop/src/index.ts:65-70
//
// 新增: DSH 那边收的是一个 Promise，并在它落定时从集合里摘掉自己。Go 里同一件事
// 就是 [sync.WaitGroup]——本包只需要「拆除时等它们跑完」，不需要枚举它们。
func (o *factoryOwnership) trackStartup(job func()) {
	o.startup.Add(1)
	go func() {
		defer o.startup.Done()
		job()
	}()
}

// waitWhileActive 等这件活儿跑完，或者在工厂拆除开始时不再等。
//
// 源: packages/core/agent-loop/src/index.ts:77-79
func (o *factoryOwnership) waitWhileActive(done <-chan struct{}) {
	select {
	case <-done:
	case <-o.teardown.Done():
	}
}

// dispose 停掉这个工厂：不再接活、把归属上下文取消掉、把每一个活 agent 拆掉、
// 等所有启动活儿落定。
//
// 源: packages/core/agent-loop/src/index.ts:81-89
func (o *factoryOwnership) dispose(ctx context.Context) error {
	o.mutex.Lock()
	o.accepting = false
	pending := make([]*agentTeardown, 0, len(o.liveAgents))
	for handle := range o.liveAgents {
		pending = append(pending, handle)
	}
	o.mutex.Unlock()

	o.cancel(errLoopNotActive)

	// 每个 agent 的拆除各自跑到底，一份失败不能把其余的落下——它们各自持有的
	// 注册表登记必须都摘掉，否则那些身份永远占着。
	var failures []error
	for _, handle := range pending {
		if err := handle.dispose(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	o.startup.Wait()
	return errors.Join(failures...)
}

// ---- 竞速 ----

// raceAbort 跑一件可能拖很久的活儿，并在 ctx 取消时立刻不再等它。
//
// 源: packages/core/agent-loop/src/index.ts:92-130（raceAbort 与 raceAbortCall）
//
// release 非 nil 时，一份在取消之后才到的结果会交给它——DSH 那个
// releaseAbandoned 参数说的是同一件事：一个「已经没人要了」的资源必须有人收，
// 否则它就是一次静默的泄漏。
//
// 新增: DSH 把 raceAbort（等一个已经在跑的 promise）和 raceAbortCall（起一件活儿
// 再等）分成两个函数，因为 JS 里「起」和「等」天然分开。Go 里起一件并发的活儿
// 必然要开一个 goroutine，两者合成一个函数反而更少一处走样。
func raceAbort[T any](
	ctx context.Context,
	id sessionlog.SessionID,
	run func() (T, error),
	release func(T),
) (T, error) {
	var zero T
	if err := abortCause(ctx, id); err != nil {
		return zero, err
	}

	type outcome struct {
		value T
		err   error
	}
	// 缓冲一格：竞速输掉之后这个 goroutine 照样要能把结果放下并退出，
	// 否则它会一直挂在发送上，泄漏一个 goroutine 外加它扣着的那份资源。
	results := make(chan outcome, 1)
	go func() {
		value, err := run()
		results <- outcome{value: value, err: err}
	}()

	select {
	case done := <-results:
		return done.value, done.err
	case <-ctx.Done():
		if release != nil {
			go func() {
				done := <-results
				if done.err == nil {
					release(done.value)
				}
			}()
		}
		return zero, abortCause(ctx, id)
	}
}

// abortCause 把一个已经取消的上下文翻译成这条路上该抛的那个错误；没取消时返回 nil。
//
// 源: packages/core/agent-loop/src/index.ts:94-97
//
// DSH 那段是「reason 本来就是 Error 就原样抛，否则包一层 `agent "<id>" creation
// aborted`」。Go 里 [context.Cause] 交出来的一定是 error，所以只剩下「有没有一个
// 比 context.Canceled 更有信息量的原因」这一层判断。
func abortCause(ctx context.Context, id sessionlog.SessionID) error {
	if ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("agent %q creation aborted: %w", string(id), cause)
	}
	return cause
}

// ---- 工厂 ----

// AgentLoop 是那份具体的 agent 造法，也是驱动这一层的服务。
//
// 源: packages/core/agent-loop/src/index.ts:351-774（AgentLoop）
//
// 新增: DSH 那个类继承 cordis 的 Service、带一个 static inject 依赖清单和一份
// schemastery 的运行期 Config schema。Go 里前两样由 [New] 的显式参数顶掉，
// 第三样由 [Config] 这个结构体加上 [New] 里那几道校验顶掉——一份 Go 结构体本身
// 就是那个 schema，剩下的只有 schema 表达不了的那些跨字段规则。
type AgentLoop struct {
	deps   Deps
	owner  *scope.Scope
	logger *slog.Logger

	// config 是已经盖过启动器身份、也校验过的那一份。
	config Config
	// settingsScope 非 nil 时，并行上限每次都从它读透。
	settingsScope *settings.Scope[Settings]
	// staticCap 是设置不在位时锁住的那个上限。
	staticCap int

	ownership *factoryOwnership

	// byScope 把一个作用域键映射回它那个活 agent。
	//
	// 新增: DSH 靠 cordis 从上下文上直接取 `ctx.agent`——那三个系统提示词变量
	// （provider、model）读的是 `context.agent?...`，而 [Registry.Enter]
	// 要的那个 owner 读的是 `ownerCtx.agent`。Go 里作用域上挂不了值，所以工厂
	// 自己维护这张表：公布时填，拆除时清。查不到就是 DSH 那个 `?.` 的短路。
	agentsMutex sync.Mutex
	byScope     map[*scope.Key]*ReactLoopAgent

	startFailed *scope.AnonymousEntries[ConfigStartFailedObserver]
}

// AgentLoop 就是那份造法本身，编译期钉住这件事。
var _ agent.Factory = (*AgentLoop)(nil)

// New 装一个循环工厂：登记造法、装上那三个系统提示词变量，然后把配置里那些
// agent 起起来。返回的函数拆掉这一整套。
//
// 源: packages/core/agent-loop/src/index.ts:318-382（constructor）
//
// owner 是拥有这个工厂的作用域，工厂造出来的每一个 agent 的作用域都挂在它下面。
//
// 新增: deps.MaxParallelToolCalls 由这里**接管**——本包这一层正是「把设置接上去」
// 的那一层（见 [Deps].MaxParallelToolCalls 的字段说明），调用方填的任何值都会被
// 换成工厂自己那个读透函数。
func New(ctx context.Context, deps Deps, owner *scope.Scope, config Config) (*AgentLoop, func(context.Context) error, error) {
	if deps.Agents == nil || deps.Sessions == nil || deps.LLM == nil ||
		deps.Tools == nil || deps.SystemPrompt == nil {
		return nil, nil, errors.New("harness/agentloop: 装一个循环工厂要有注册表、会话存储、模型、工具和系统提示词五样")
	}
	if owner == nil {
		return nil, nil, errors.New("harness/agentloop: 装一个循环工厂要有一个持有它的作用域")
	}

	staticCap, err := resolveMaxParallelToolCalls(config.MaxParallelToolCalls)
	if err != nil {
		return nil, nil, err
	}
	config.Agents = applyLauncherIdentities(config.Agents, config.LauncherIdentities)
	if err := validateConfiguredAgents(config.Agents); err != nil {
		return nil, nil, err
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	loop := &AgentLoop{
		deps:        deps,
		owner:       owner,
		logger:      logger,
		config:      config,
		staticCap:   staticCap,
		ownership:   newFactoryOwnership(),
		byScope:     make(map[*scope.Key]*ReactLoopAgent),
		startFailed: scope.NewAnonymousEntries[ConfigStartFailedObserver](),
	}
	loop.deps.MaxParallelToolCalls = loop.maxParallelToolCalls

	// 拆除是后进先出的一条链，所以这一条登记在最前面、跑在最后面：造法先摘掉
	// （不再有人能从这里要新 agent），三个变量再摘掉，最后才把在飞的那些 agent
	// 排干。反过来的话，排干过程中的最后几个步骤会装不出系统提示词。
	var teardown []func(context.Context) error
	keep := func(undo func(context.Context) error, err error) error {
		if err != nil {
			return err
		}
		teardown = append(teardown, undo)
		return nil
	}
	unwind := func(ctx context.Context) error {
		var failures []error
		for index := len(teardown) - 1; index >= 0; index-- {
			if err := teardown[index](ctx); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	fail := func(err error) (*AgentLoop, func(context.Context) error, error) {
		return nil, nil, errors.Join(err, unwind(ctx))
	}

	if err := keep(owner.Defer("agentLoop.transactions()", loop.ownership.dispose)); err != nil {
		return fail(err)
	}

	if config.Settings != nil {
		settingsScope, undo, err := settings.Register(config.Settings, SettingsNamespace,
			Settings{MaxParallelToolCalls: staticCap}, &settings.Options[Settings]{
				Applies: settings.AppliesLive,
				// schema 那一层只管得住「是个正整数」，而整条规则归
				// resolveMaxParallelToolCalls 拥有；在这里拒掉一次坏改动，
				// 跑着的调度器就停在上一个好值上，而不是等到下一组工具调用才炸。
				Validate: func(value Settings) error {
					_, err := resolveMaxParallelToolCalls(value.MaxParallelToolCalls)
					return err
				},
			})
		if err != nil {
			return fail(fmt.Errorf("harness/agentloop: 登记设置小节失败：%w", err))
		}
		loop.settingsScope = settingsScope
		teardown = append(teardown, func(context.Context) error { undo(); return nil })
	}

	if err := keep(deps.Agents.SetFactory(loop)); err != nil {
		return fail(fmt.Errorf("harness/agentloop: 登记 agent 造法失败：%w", err))
	}

	// 新增: DSH 在这里还登记第三个变量 `cwd`，把宿主机工作目录摆给模型看。本仓库
	// 没有这一项：服务端没有工作目录（见 [sessionlog.SessionHeader.WorkspaceID]），
	// 告诉模型「你的工作目录是 /x」是一句谎话——它会照着去拼路径、去猜相对位置，
	// 而那些路径在这套部署里指不到任何东西。换成工作区标识也不行：那是一个不透明
	// 的 id，对模型只是噪音。
	for name, read := range map[string]func(*ReactLoopAgent) string{
		"provider": func(a *ReactLoopAgent) string { return a.Options().Provider },
		"model":    func(a *ReactLoopAgent) string { return a.Options().Model },
	} {
		if err := keep(deps.SystemPrompt.Variable(ctx, owner, name, loop.agentVariable(read))); err != nil {
			return fail(fmt.Errorf("harness/agentloop: 登记系统提示词变量 %q 失败：%w", name, err))
		}
	}

	loop.startConfiguredAgents(ctx)
	return loop, unwind, nil
}

// agentVariable 把一个「从 agent 上读一个字段」的函数包成系统提示词变量。
//
// 源: packages/core/agent-loop/src/index.ts:377-379
//
// 装配的作用域上没有 agent（比如一次不属于任何 agent 的装配）时交出 nil，
// 那正是 DSH 那三行里 `context.agent?.` 短路成 undefined 的意思。
func (l *AgentLoop) agentVariable(read func(*ReactLoopAgent) string) systemprompt.VariableProvider {
	return func(_ context.Context, assemble systemprompt.AssembleContext) (*string, error) {
		live := l.agentForScope(assemble.Scope)
		if live == nil {
			return nil, nil
		}
		value := read(live)
		return &value, nil
	}
}

// agentForScope 查一个作用域键上那个活 agent。
func (l *AgentLoop) agentForScope(key *scope.Key) *ReactLoopAgent {
	if key == nil {
		return nil
	}
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	return l.byScope[key]
}

// maxParallelToolCalls 读出当下的并行上限，设置在位时每次都读透。
//
// 源: packages/core/agent-loop/src/index.ts:330-334
//
// DSH 那段注释写的是「tool-calls.ts 在每一组开头解构它，所以一次提交过的改动
// 只影响下一组，不打扰在飞的那一组」——本包 [ReactLoopAgent.maxParallelToolCalls]
// 的调用位置就在同一个地方。
func (l *AgentLoop) maxParallelToolCalls() int {
	if l.settingsScope == nil {
		return l.staticCap
	}
	resolved, err := resolveMaxParallelToolCalls(l.settingsScope.Get().MaxParallelToolCalls)
	if err != nil {
		// 走不到：Validate 已经把坏值挡在提交之前了。真漏进来一个的话，
		// 停在那个静态上限上比让调度器拿一个非法的池宽度跑要好。
		return l.staticCap
	}
	return resolved
}

// OnConfigStartFailed 登记一个「配置驱动的启动失败了」的观察者。
//
// 源: packages/core/agent-loop/src/index.ts:160-179
func (l *AgentLoop) OnConfigStartFailed(observer ConfigStartFailedObserver) (func(), error) {
	if observer == nil {
		return nil, errors.New("harness/agentloop: OnConfigStartFailed 需要一个观察者")
	}
	return l.startFailed.Append(observer), nil
}

// ---- 配置驱动的启动 ----

// startConfiguredAgents 把配置里那些 agent 起起来。
//
// 源: packages/core/agent-loop/src/index.ts:381-381（constructor 末尾那个循环）
func (l *AgentLoop) startConfiguredAgents(ctx context.Context) {
	for _, configured := range l.config.Agents {
		if configured.ResumeSessionID == "" {
			l.startFreshConfigured(ctx, configured)
			continue
		}
		l.startResumingConfigured(ctx, configured)
	}
}

// startFreshConfigured 起一个不续跑的配置项。
//
// 源: packages/core/agent-loop/src/index.ts:384-397（`resumeSessionId` 为空那一支）
func (l *AgentLoop) startFreshConfigured(ctx context.Context, configured ConfiguredAgent) {
	configuredID := configured.SessionID
	if configuredID == "" {
		configuredID = sessionlog.SessionID(fmt.Sprintf("%s-session-%s", configured.ID, uuid.NewString()))
	}

	// 只有**配置里明确给了身份**的那些项才走持久化：一个现铸的随机身份不可能
	// 已经落过地，去读它只会白等一次后端往返。
	if configured.SessionID == "" || l.config.Persistence == nil {
		if _, err := l.Create(ctx, configuredID, configured.Options, configured.WorkspaceID); err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "restore", configuredID, err)
		}
		return
	}

	l.ownership.trackStartup(func() {
		if err := l.restoreOrCreateConfigured(ctx, configuredID, configured); err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "restore", configuredID, err)
		}
	})
}

// startResumingConfigured 起一个要续跑的配置项。
//
// 源: packages/core/agent-loop/src/index.ts:398-410
//
// 新增: DSH 把这一支裹在 `ctx.effect(() => ctx.inject(['sessionPersistence'], ...))`
// 里——持久化服务**将来**挂上来的时候这段才跑，卸载时又停掉。Go 里没有服务动态
// 到场这件事：装配在构造这个工厂之前就定死了，所以持久化不在位是一个**永久**
// 状态，走和其他启动失败同一条通报路，而不是无限等下去。
func (l *AgentLoop) startResumingConfigured(ctx context.Context, configured ConfiguredAgent) {
	if l.config.Persistence == nil {
		l.reportConfiguredStartupFailure(configured.ID, "resume", configured.ResumeSessionID,
			errors.New("cannot resume: session persistence is not configured"))
		return
	}
	l.ownership.trackStartup(func() {
		_, err := l.resumeWith(ctx, l.owner, l.config.Persistence, agent.ResumeOptions{
			ResumeSessionID: configured.ResumeSessionID,
			AgentOptions:    configured.Options,
		})
		if err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "resume", configured.ResumeSessionID, err)
		}
	})
}

// reportConfiguredStartupFailure 把一次兜住了的声明式启动失败通报给那些绑着这个身份的消费方。
//
// 源: packages/core/agent-loop/src/index.ts:384-404
//
// 工厂已经在拆了就不报：那次启动是被这次拆除自己取消掉的，报出去只会让消费方
// 把一次正常卸载当成故障。
func (l *AgentLoop) reportConfiguredStartupFailure(
	configID string,
	action string,
	sessionID sessionlog.SessionID,
	failure error,
) {
	if !l.ownership.isActive() {
		return
	}
	l.logger.Warn("harness/agentloop: 配置驱动的启动失败",
		slog.String("agent", configID),
		slog.String("action", action),
		slog.String("session", string(sessionID)),
		slog.Any("error", failure))

	for observer := range l.startFailed.Values() {
		l.notifyStartFailed(configID, observer, sessionID, failure)
	}
}

// notifyStartFailed 叫一个启动失败观察者，它自己 panic 了就记一条继续叫下一个。
//
// 源: packages/core/agent-loop/src/index.ts:396-403（那两个 try/catch）
//
// 一个观察者的事故不能把其余观察者落下：它们各自为这个身份缓着活儿，
// 少通知一个就是那些活儿永远等下去。
func (l *AgentLoop) notifyStartFailed(
	configID string,
	observer ConfigStartFailedObserver,
	sessionID sessionlog.SessionID,
	failure error,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			l.logger.Warn("harness/agentloop: 启动失败观察者 panic 了",
				slog.String("agent", configID),
				slog.String("session", string(sessionID)),
				slog.Any("panic", recovered))
		}
	}()
	observer(sessionID, failure)
}

// restoreOrCreateConfigured 在重挂时读回一个已经落地的确切配置身份，第一次用则新建它。
//
// 源: packages/core/agent-loop/src/index.ts:406-428
func (l *AgentLoop) restoreOrCreateConfigured(
	ctx context.Context,
	sessionID sessionlog.SessionID,
	configured ConfiguredAgent,
) error {
	if err := l.waitForDrainingConfiguredIdentity(ctx, sessionID); err != nil {
		return err
	}
	if !l.ownership.isActive() {
		return nil
	}

	_, resumeErr := l.resumeWith(ctx, l.owner, l.config.Persistence, agent.ResumeOptions{
		ResumeSessionID: sessionID,
		AgentOptions:    configured.Options,
	})
	if resumeErr == nil {
		return nil
	}
	if !l.ownership.isActive() {
		return nil
	}

	// 一次读取就是这个身份上的串行屏障——它把急切的写回和生命周期退休都排在
	// 自己后面。只有「存档确实不存在」才回落到第一次创建；损坏和后端故障
	// 照样吵。
	headers, err := l.config.Persistence.List(ctx)
	if err != nil {
		return errors.Join(resumeErr, err)
	}
	for _, header := range headers {
		if header.ID == sessionID {
			return resumeErr
		}
	}

	_, err = l.Create(ctx, sessionID, configured.Options, configured.WorkspaceID)
	return err
}

// waitForDrainingConfiguredIdentity 等一个正在排干的同名生命周期把注册表登记摘干净。
//
// 源: packages/core/agent-loop/src/index.ts:430-451
func (l *AgentLoop) waitForDrainingConfiguredIdentity(ctx context.Context, sessionID sessionlog.SessionID) error {
	// 只有还占着注册表的身份才值得等；一个活得好好的占用者是一次撞名，
	// 那由下面的创建／续跑自己报出来。
	occupied := func() bool {
		if _, live := l.deps.Agents.Get(sessionID); live {
			return true
		}
		_, live := l.deps.Sessions.Get(sessionID)
		return live
	}
	if !occupied() {
		return nil
	}

	released := make(chan struct{})
	var once sync.Once
	checkReleased := func() {
		if !occupied() {
			once.Do(func() { close(released) })
		}
	}

	unwatchAgent, err := l.deps.Agents.OnDisposed(ctx, l.owner, func(agent.Agent) { checkReleased() })
	if err != nil {
		return fmt.Errorf("harness/agentloop: 等身份 %q 排干时挂不上 agent 观察者：%w", string(sessionID), err)
	}
	unwatchSession, err := l.deps.Sessions.OnDisposed(ctx, l.owner, func(*session.Session) { checkReleased() })
	if err != nil {
		return errors.Join(
			fmt.Errorf("harness/agentloop: 等身份 %q 排干时挂不上会话观察者：%w", string(sessionID), err),
			unwatchAgent(ctx))
	}

	// 挂上观察者**之后**再查一次：这两步之间释放掉的话，那两条通知谁都收不到。
	checkReleased()
	l.ownership.waitWhileActive(released)
	return errors.Join(unwatchSession(ctx), unwatchAgent(ctx))
}

// ---- 生命周期装配 ----

// preparedAgent 是备好但还没公布的那一套资源，共享同一份拆除。
//
// 源: packages/core/agent-loop/src/index.ts:205-214（PreparedAgent）
type preparedAgent struct {
	// agent 是造好的那个驱动。
	agent *ReactLoopAgent
	// life 在工厂卸载、调用方取消、或者拆除开始时取消——任何一次 setup 等待
	// 都拿它当尽头。
	life context.Context
	// publish 进两张注册表、公布、报会话开始。
	publish func(ctx context.Context, source agent.SessionStartSource) (agent.Handle, error)
	// dispose 是那条倒着走的拆除：停机器、退注册表、拆作用域。只跑一次。
	dispose func(ctx context.Context) error
}

// untangleKey 标记「这次调用是拆除自己在摘掉自己挂在 owner 上的那条登记」。
//
// 新增: DSH 那边解绑和执行是两件事，摘一条登记不会把它跑一遍。Go 这边
// [scope.Scope.Defer] 交出来的 disposer 是跑完再摘的，所以自摘必须能被认出来。
// 用 ctx 上的记号而不是一个布尔标志位，是因为它只跟着**我们自己发出的那一次**
// 调用走：一次并发到达的 owner 释放带的是它自己的 ctx，读不到这个记号，
// 于是照常排队等同一次静止，而不是被误当成自摘直接放行。
type untangleKey struct{}

// prepare 给一个新 agent 造出驱动、作用域和**唯一那一份**倒序拆除。
//
// 源: packages/core/agent-loop/src/index.ts:453-575
//
// 这份拆除在公布**之前**就登记进工厂和 owner 作用域，所以一次装到一半的卸载
// 会把已经建起来的东西全部回滚掉；life 把调用方的取消和生命周期拆除熔在一起，
// 供 setup 期间的等待使用。
//
// # 新增: DSH 那个 SessionPreparation 在这里没有对应物
//
// 源: packages/core/agent-loop/src/index.ts:583、589-597
//
// DSH 用 `using preparation = SessionPreparation.create(...)` 包住备好的会话，
// 靠 `Symbol.dispose` 保证一份没公布成的会话被释放掉。Go 这边不需要：
// [github.com/snight1983/ds-harness-go/harness/session.Store.Prepare] 只读存储那张表来铸身份，
// **一行都不写进去**，所以一个备好却没公布的会话不占任何东西，丢掉它就够了。
func (l *AgentLoop) prepare(
	ctx context.Context,
	owner *scope.Scope,
	id sessionlog.SessionID,
	options agent.Options,
	live *session.Session,
) (*preparedAgent, error) {
	if err := assertAgentOptions(options); err != nil {
		return nil, err
	}
	if !l.ownership.isActive() {
		return nil, errLoopNotActive
	}
	if err := abortCause(ctx, id); err != nil {
		return nil, err
	}

	// 停用熔的是三个主人，各自带自己的原因：调用方的取消、owner 作用域的拆除、
	// 以及工厂拆除。它登记在**任何资源存在之前**，且落在可变的槽位上，
	// 这样一次在作用域还没铸出来时到达的卸载找到的是一个能用的拆除函数，
	// 而不是一处泄漏。
	life, endLife := context.WithCancelCause(context.WithoutCancel(ctx))
	unfuse := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			endLife(abortCause(ctx, id))
		case <-l.ownership.teardown.Done():
			endLife(context.Cause(l.ownership.teardown))
		case <-unfuse:
		}
	}()

	var (
		machineMutex  sync.Mutex
		machine       *ReactLoopAgent
		detachSession func(context.Context) error
		detachAgent   func(context.Context) error
	)
	// ready 在机器造出来（或者造失败）之后关掉。一次并发到达的 owner 拆除
	// 靠它避开「机器还是 nil，什么都没停就往下拆」那个窗口。
	ready := make(chan struct{})
	var closeReady sync.Once

	var (
		untrack       func()
		unfollowOwner func(context.Context) error
		disposeOnce   sync.Once
		disposeErr    error
	)
	// 倒着拆，并且做成一次性的：每一个抢着拆的主人等的都是同一次静止。
	// 先停机器、等它退出、拆掉它那个作用域世界，再退两张注册表，最后收账。
	var sharedDispose func(context.Context) error
	sharedDispose = func(disposeCtx context.Context) error {
		disposeOnce.Do(func() {
			endLife(fmt.Errorf("agent %q lifecycle disposed", string(id)))
			close(unfuse)

			<-ready
			machineMutex.Lock()
			current := machine
			machineMutex.Unlock()

			var failures []error
			if current != nil {
				// 拆除**就是**一次 disposed 因由的取消加上等它静止。此后再送进来的
				// 活儿是发送方的缺陷——注册表马上就要把这个 agent 丢掉了。
				current.Cancel(sessionlog.DisposedCancel{}, agent.CancelOptions{})
				if err := current.WhenIdle(disposeCtx); err != nil {
					failures = append(failures, err)
				}
				if err := current.Scope().Dispose(disposeCtx); err != nil {
					failures = append(failures, err)
				}
				l.forgetAgent(current)
			}
			if detachAgent != nil {
				if err := detachAgent(disposeCtx); err != nil {
					failures = append(failures, err)
				}
			}
			if detachSession != nil {
				if err := detachSession(disposeCtx); err != nil {
					failures = append(failures, err)
				}
			}
			untrack()
			// 摘掉 owner 上那条登记，否则一个长命的 owner 会一直攒着已经拆完的
			// agent 的闭包。[scope.Scope.Defer] 的 disposer 是**跑完再摘**的，
			// 所以这一下会把下面那个回调也叫一遍——用 disposeCtx 上的记号让它
			// 认出「这是拆除自己在摘自己」，否则它会再进一次 disposeOnce，
			// 变成在这次 Do 里面等这次 Do。
			if unfollowOwner != nil {
				if err := unfollowOwner(context.WithValue(
					disposeCtx, untangleKey{}, untangleKey{})); err != nil {
					failures = append(failures, err)
				}
			}
			disposeErr = errors.Join(failures...)
		})
		return disposeErr
	}
	untrack = l.ownership.track(sharedDispose)

	unfollowOwner, err := owner.Defer(fmt.Sprintf("agentLoop.lifecycle(%s)", string(id)),
		func(disposeCtx context.Context) error {
			// 只有上面那一下自摘会带着这个记号。owner 作用域自己释放时走的是
			// 它自己的 ctx，那一路照常拆，并且照常等到同一次静止。
			if disposeCtx.Value(untangleKey{}) != nil {
				return nil
			}
			endLife(fmt.Errorf("agent %q setup aborted: owner disposed during setup", string(id)))
			return sharedDispose(disposeCtx)
		})
	if err != nil {
		untrack()
		close(unfuse)
		endLife(err)
		closeReady.Do(func() { close(ready) })
		return nil, fmt.Errorf("harness/agentloop: 把 agent %q 的生命周期挂到 owner 上失败：%w", string(id), err)
	}

	assertLive := func() error {
		if life.Err() == nil {
			return nil
		}
		return context.Cause(life)
	}

	built, err := NewReactLoopAgent(life, l.deps, owner.Key(), id, options, live)
	machineMutex.Lock()
	machine = built
	machineMutex.Unlock()
	closeReady.Do(func() { close(ready) })
	if err != nil {
		_ = sharedDispose(ctx)
		return nil, err
	}
	if err := assertLive(); err != nil {
		_ = sharedDispose(ctx)
		return nil, err
	}

	publish := func(publishCtx context.Context, source agent.SessionStartSource) (agent.Handle, error) {
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 会话的摘除挂在**这个 agent 自己**的作用域上：会话和循环是一条按次序
		// 拆的链，最后那几条事件必须在存储登记消失之前发布出去。
		detach, err := l.deps.Sessions.Enter(built.Scope(), live)
		if err != nil {
			return agent.Handle{}, err
		}
		detachSession = detach

		detach, err = l.deps.Agents.Enter(built, l.agentForScope(owner.Key()))
		if err != nil {
			return agent.Handle{}, err
		}
		detachAgent = detach

		if err := l.deps.Sessions.Announce(publishCtx, live); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 先记进这张表再公布：一个同步的创建观察者装系统提示词时就该看得见
		// 这个 agent 的 provider／model。
		l.rememberAgent(built)
		if err := l.deps.Agents.Announce(publishCtx, built); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 一个同步的公布／会话开始观察者可能已经开始拆了；机器此刻已经活着
		// （从会话开始这个扩展点投递是通的），所以这里只欠一次活性复查。
		if err := l.deps.Agents.ReportSessionStart(built, source); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		return agent.Handle{Agent: built, Dispose: sharedDispose}, nil
	}
	return &preparedAgent{agent: built, life: life, publish: publish, dispose: sharedDispose}, nil
}

// rememberAgent 把一个刚公布的 agent 记进作用域索引。
func (l *AgentLoop) rememberAgent(live *ReactLoopAgent) {
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	l.byScope[live.Scope().Key()] = live
}

// forgetAgent 把一个拆掉的 agent 从作用域索引里摘掉。
//
// 不摘的话这张表会一直长，而且一个已经死掉的 agent 还能被系统提示词变量读到。
func (l *AgentLoop) forgetAgent(live *ReactLoopAgent) {
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	delete(l.byScope, live.Scope().Key())
}

// ---- 对外入口 ----

// Create 在调用方给的身份上造一个 agent 和它的会话，归属于这个工厂自己的作用域。
//
// 源: packages/core/agent-loop/src/index.ts:580-587
//
// 配置驱动的那条路在进这个边界之前就把一个新铸的组合身份定好了。
func (l *AgentLoop) Create(
	ctx context.Context,
	id sessionlog.SessionID,
	options agent.Options,
	workspaceID sessionlog.WorkspaceID,
) (agent.Agent, error) {
	live, err := l.deps.Sessions.Prepare(id, session.CreateOptions{WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	prepared, err := l.prepare(ctx, l.owner, id, options, live)
	if err != nil {
		return nil, err
	}
	handle, err := prepared.publish(ctx, agent.StartStartup)
	if err != nil {
		return nil, errors.Join(err, prepared.dispose(ctx))
	}
	return handle.Agent, nil
}

// CreateAgent 在调用方给的会话身份上造一个归它所有的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:589-604
func (l *AgentLoop) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	if owner == nil {
		return agent.Handle{}, errors.New("harness/agentloop: 造一个 agent 要有一个持有它的作用域")
	}
	live, err := l.deps.Sessions.Prepare(options.SessionID, session.CreateOptions{
		Seed:            options.Seed,
		WorkspaceID:     options.WorkspaceID,
		ParentSession:   options.ParentSession,
		SeedLength:      options.SeedLength,
		Origin:          options.Origin,
		DelegationDepth: options.DelegationDepth,
		AgentPreset:     options.AgentPreset,
	})
	if err != nil {
		return agent.Handle{}, err
	}
	// 这条路的会话是活会话存储当场造的，提供方那边没有攥着任何待还的状态，
	// 所以准备期没有释放动作——DSH 那句 `SessionPreparation.create(...)`
	// 同样不带 release。裹一层是为了让公布那条路只有一个形状。
	return l.setupAndPublish(ctx, owner, options.SessionID,
		session.NewPreparation(live, session.PreparationOptions{}),
		options.AgentOptions, options.Setup, agent.StartStartup)
}

// setupAndPublish 围着一段到手的准备期备好一个 agent、跑完 setup、把它公布出去。
//
// 源: packages/core/agent-loop/src/index.ts:686-708
//
// 那段准备期在这里**结束**，无论公布成没成：DSH 写的是 `using ownedPreparation
// = preparation`，Go 的对应物就是下面这句 defer。提供方那份状态可能已经被公布
// 那一步接手走了，那时候释放是空操作——[session.Preparation.Release] 保证幂等，
// 所以这里不需要分两条路。
func (l *AgentLoop) setupAndPublish(
	ctx context.Context,
	owner *scope.Scope,
	id sessionlog.SessionID,
	preparation *session.Preparation,
	options agent.Options,
	setup agent.Setup,
	source agent.SessionStartSource,
) (agent.Handle, error) {
	defer preparation.Release()

	prepared, err := l.prepare(ctx, owner, id, options, preparation.Session())
	if err != nil {
		return agent.Handle{}, err
	}

	if setup != nil {
		commit, err := raceAbort(prepared.life, id, func() (func() error, error) {
			return setup(prepared.life, prepared.agent.Scope())
		}, nil)
		if err != nil {
			return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
		}
		if commit != nil {
			if err := commit(); err != nil {
				return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
			}
		}
	}

	handle, err := prepared.publish(ctx, source)
	if err != nil {
		return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
	}
	return handle, nil
}

// Resume 从配置好的那份持久化服务里续跑一个归调用方所有的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:624-635
func (l *AgentLoop) Resume(
	ctx context.Context,
	owner *scope.Scope,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	if l.config.Persistence == nil {
		return agent.Handle{}, errors.New(
			"cannot resume: session persistence is not configured (load a session persistence backend)")
	}
	return l.resumeWith(ctx, owner, l.config.Persistence, options)
}

// resumeWith 走一份显式的持久化句柄续跑，配置驱动那条延后的路用的就是它。
//
// 源: packages/core/agent-loop/src/index.ts:637-710
func (l *AgentLoop) resumeWith(
	ctx context.Context,
	owner *scope.Scope,
	persistence SessionPersistence,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	if owner == nil {
		return agent.Handle{}, errors.New("harness/agentloop: 续跑一个 agent 要有一个持有它的作用域")
	}
	id := options.ResumeSessionID

	// 这次读取可能活得比它的主人还久：把它和调用方的取消、owner 作用域的拆除、
	// 以及工厂拆除三者一起竞速，这样一个永远不落定的后端扣不住这个身份。
	loadCtx, endLoad := context.WithCancelCause(context.WithoutCancel(ctx))
	defer endLoad(nil)
	stopFuse := make(chan struct{})
	defer close(stopFuse)
	go func() {
		select {
		case <-ctx.Done():
			endLoad(abortCause(ctx, id))
		case <-l.ownership.teardown.Done():
			endLoad(context.Cause(l.ownership.teardown))
		case <-stopFuse:
		}
	}()
	unfollowOwner, err := owner.Defer(fmt.Sprintf("agentLoop.resume-load(%s)", string(id)),
		func(context.Context) error {
			endLoad(fmt.Errorf("agent %q setup aborted: owner disposed during setup", string(id)))
			return nil
		})
	if err != nil {
		return agent.Handle{}, fmt.Errorf("harness/agentloop: 把 agent %q 的读取挂到 owner 上失败：%w", string(id), err)
	}

	// 一份在取消之后才到的准备期没人要了，但**不能**就这么扔掉：提供方那边
	// 还攥着一份预留，不还回去这个会话身份就一直被扣着，后面谁都续不了它。
	// 所以竞速输掉那一支也要走 Release——这正是 DSH 那个
	// `(abandoned) => { abandoned[Symbol.dispose]() }` 回调的作用。
	preparation, loadErr := raceAbort(loadCtx, id, func() (*session.Preparation, error) {
		return persistence.Prepare(loadCtx, id)
	}, func(abandoned *session.Preparation) { abandoned.Release() })
	unfollowErr := unfollowOwner(ctx)
	if loadErr != nil {
		return agent.Handle{}, errors.Join(loadErr, unfollowErr)
	}
	if unfollowErr != nil {
		preparation.Release()
		return agent.Handle{}, unfollowErr
	}
	if !l.ownership.isActive() {
		preparation.Release()
		return agent.Handle{}, errLoopNotActive
	}

	return l.setupAndPublish(ctx, owner, id, preparation,
		options.AgentOptions, options.Setup, agent.StartResume)
}
