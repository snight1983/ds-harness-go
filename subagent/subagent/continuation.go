// 本文件的作用：可续子 agent 那台内部编排机的**词汇与骨架**——稳定的孩子 id、
// 描述符持久化、活化准入、那张活着的所有权图、冷恢复、孩子优先的处置，以及把
// 结清交回给父。公开操作在 continuationops.go，拆解与血统在 continuationdrain.go，
// 物化与结清守望在 continuationactivation.go。
//
// 源: packages/subagent/subagent/src/continuation.ts
//
// 一个可续孩子有**一份**耐久会话，以及至多一个进程内的[activation]——那是一个被
// 重建出来的孩子 agent 的一段驻留轮次。活化不是请求、不是结果、不是取消、也不是
// 任务边界：它可以跑很多个 FIFO 回合，而且在它建起来的后代还在跑的时候一直驻留。
// agent 的收件箱是唯一的回合队列，所以这个管理器只管驻留，回合的次序与执行全归
// agent 循环。可续这条路上不建任何任务、也不建任何中间的、带结果的包装。
//
// 正因为驻留只有这个管理器结束得了，告诉父「孩子结清了」也是它的活儿。一个外部的
// `subagent/end` 监听者做不对这件事：那份负载里没有父是谁，到那时孩子的句柄已经
// 处置掉了，而唤醒父自己那个结清守望的放权也已经跑过了。见 notifySettlement。

package subagent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// ReportDelivery 是部署对一份被接受的孩子汇报的排期策略。
//
// 源: packages/subagent/subagent/src/continuation.ts:101-102（SubagentReportDelivery）
type ReportDelivery string

const (
	// DeliveryQuiet 把汇报注入下一个前置步骤，**不**唤醒父。
	DeliveryQuiet ReportDelivery = "quiet"
	// DeliveryNextStep 把汇报引导进父最近的那个步骤，会唤醒它。
	DeliveryNextStep ReportDelivery = "next-step"
)

// ReportOptions 是一个可续孩子向它直系父汇报时的那几样。
//
// 源: packages/subagent/subagent/src/continuation.ts:104-110（SubagentReportOptions）
//
// 新增: DSH 这里还有一个 signal 字段。Go 的取消是 ctx，走参数不走结构体。
type ReportOptions struct {
	// Delivery 是已经解算好的父侧排期策略。
	Delivery ReportDelivery
}

// ContinuableStartSpec 是调用方要起一个可续后台孩子时说的话。
//
// 源: packages/subagent/subagent/src/continuation.ts:112-131（ContinuableStartSpec）
type ContinuableStartSpec struct {
	// Provider 是那个提供方名字，它那份可续创建能力立起这个孩子。
	Provider string
	// Label 是这次首派发的短 `description`，作为孩子的创建名持久下来。
	Label string
	// ChildID 是调用方**预留**的孩子身份；空串表示交给管理器分配 UUID。
	// 给了它，一份耐久的父记录就能在孩子物化之前先把配额记下来，不必再来
	// 第二次身份握手。
	ChildID session.SessionID
	// Request 是那次派发请求。孩子的稳定 id、耐久描述符和组合都由管理器自己解算。
	//
	// 新增: DSH 这里的类型是 `Omit<SubagentStartRequest, 'label'|'signal'|'outputSchema'>`。
	// Go 表达不了「减掉几个字段」，所以整个 [StartRequest] 原样嵌进来，而这三项
	// 在这条路上**不读**：Label 走上面那个字段（它是这次派发自己的名字，
	// 不是请求的一部分），取消是 ctx，而结构化输出属于一次性那条路——一个可续
	// 孩子跑很多个回合，没有「那一次的结构化结果」这回事。
	Request StartRequest
}

// ContinuableStart 是一个可续孩子接下它的初始提示词之后交回来的那两个身份。
//
// 源: packages/subagent/subagent/src/continuation.ts:133-139（ContinuableStart）
type ContinuableStart struct {
	// ChildID 是那个耐久的孩子会话 id，跨活化稳定。
	ChildID session.SessionID
	// MessageID 是那条被接受的初始提示词在收件箱里的消息 id。
	MessageID llm.MessageID
}

// InterruptAuthorityKind 说的是一次打断请求凭的是哪一种权。
type InterruptAuthorityKind string

const (
	// AuthorityUser 是一个人类客户端出示的那个耐久直系父地址。
	AuthorityUser InterruptAuthorityKind = "user"
	// AuthorityAncestor 是那个确切的活 agent 对象，它必须出现在目标记下来的血统里。
	AuthorityAncestor InterruptAuthorityKind = "ancestor"
)

// InterruptAuthority 是一次打断请求被准入所凭的那份权。
//
// 源: packages/subagent/subagent/src/continuation.ts:141-148（SubagentInterruptAuthority）
//
// 新增: DSH 是 `{kind:'user', parentSessionId} | {kind:'ancestor', agent}` 两支按
// kind 判别的联合。Go 没有判别联合，和 [DescriptorData]、[ListEntry] 是同一种做法：
// **一个**结构体加一个 Kind 字段，哪些字段属于哪一支写在各自的注释里。
type InterruptAuthority struct {
	// Kind 是这份权属于哪一支。
	Kind InterruptAuthorityKind
	// ParentSessionID 是那个耐久的直系父地址。只有 [AuthorityUser] 有。
	ParentSessionID session.SessionID
	// Agent 是那个确切的活祖先。只有 [AuthorityAncestor] 有。
	Agent agent.Agent
}

// FollowupOptions 是对一个可续孩子做后续投递时的那几样。
//
// 源: packages/subagent/subagent/src/continuation.ts:150-156（SubagentFollowupOptions）
//
// 新增: DSH 这里还有一个 signal 字段。Go 的取消是 ctx，走参数不走结构体。
type FollowupOptions struct {
	// Source 是留在被投递消息上的耐久归属；它**不**授予任何权限。
	Source llm.MessageSource
}

// activationState 是一个可续孩子的驻留状态，从 agent 的静止程度和它名下那组孩子
// 推出来，而不是第二台状态机。
//
// 源: packages/subagent/subagent/src/continuation.ts:156-165
type activationState string

const (
	// stateRunning 是这个 agent 有在跑的准入或者回合，或者收件箱里有要唤醒的活儿。
	stateRunning activationState = "running"
	// stateWaiting 是这个 agent 静下来了，但它名下还有没处置掉的孩子。
	stateWaiting activationState = "waiting"
	// stateSettled 是静下来了、名下每个孩子也都处置了，于是管理器处置这个句柄、
	// 把活化摘掉。
	stateSettled activationState = "settled"
)

// continuationHost 是管理器要从拥有它的那个服务那儿拿到的两个钩子。
//
// 源: packages/subagent/subagent/src/continuation.ts:167-188
//
// 由**依赖方**在这里声明，于是管理器说清楚它到底要什么，而不是反过来依赖整个运行时。
// 包内私有：本包之外没有任何消费方提供 host。
type continuationHost interface {
	// prepareContinuable 解算一个提供方那份可续创建贡献；提供方不认识、或者没有
	// 这个能力时报错。
	prepareContinuable(ctx context.Context, name string, request ContinuableCreateRequest) (ContinuableCreateSpec, error)
	// observeActivation 造出一段活化驻留轮次的生命周期观察者。
	observeActivation(provider string, childID session.SessionID, parent agent.Agent) *activationObserver
}

// disposalTx 是一次被记下来的处置事务。
//
// 源: packages/subagent/subagent/src/continuation.ts:245-251
//
// 新增: DSH 那里是一个 `Promise<void> | undefined` 字段，「在不在」就是准入闸，
// 反复 await 拿到同一份结局。Go 里换成一个指针加一个一次性关闭的通道：指针在不在
// 仍旧是那道闸（在管理器的锁里同步赋上），而 done 关掉之后 err 就定死了，
// 于是每一个汇合过来的释放方共享同一次拆解。
type disposalTx struct {
	// done 在这次处置有结局之后关闭。
	done chan struct{}
	// err 在 done 关闭之后只读。
	err error
}

// newDisposalTx 开一次还没有结局的处置事务。
func newDisposalTx() *disposalTx { return &disposalTx{done: make(chan struct{})} }

// settle 记下结局并放开每一个等的人。只调一次。
func (d *disposalTx) settle(err error) {
	d.err = err
	close(d.done)
}

// wait 等这次处置有结局；调用方取消时报 [CodeCancelled]。
func (d *disposalTx) wait(ctx context.Context) error {
	select {
	case <-d.done:
		return d.err
	case <-ctx.Done():
		return NewError("等一个子 agent 拆解完时被取消了", CodeCancelled, ctx.Err())
	}
}

// activation 是一个被重建出来的可续孩子 agent 的一段驻留轮次。
//
// 源: packages/subagent/subagent/src/continuation.ts:190-252
//
// 它直接拥有那个已公布的 agent 句柄；管理器那把私有的活化所有者作用域是它结构上
// 的主人。
//
// 新增: 除 childID／parentSession／provider／handle／observer 之外，其余字段都归
// [ContinuationManager.mutex] 管——DSH 是单线程的，一把锁都不需要。
type activation struct {
	// childID 是这一段轮次所属的那个耐久孩子。
	childID session.SessionID
	// parentSession 是那个耐久的直系父。存下来，是因为结清投递必须在孩子句柄
	// 已经没了之后还解得出那个父：ancestry 答不了这件事（一张身份集合不承载
	// 「哪个是直系父」），而孩子自己那份头只有透过一个已经被处置放掉的句柄才够得着。
	parentSession session.SessionID
	// provider 是记在耐久描述符里的那个提供方名字。
	provider string
	// handle 是留住的那个活 agent 句柄，结清时**恰好处置一次**。
	handle agent.Handle
	// observer 是发出这一轮 start／end 两条边的生命周期观察者。
	observer *activationObserver

	// ancestry 是这次活化物化那一刻观察到的、确切的活 agent 血统。
	//
	// 新增: DSH 是 `WeakSet<Agent>`，弱成员让一个中间祖先离开注册表时不必留住它
	// 的运行时。Go 没有弱引用，这里是一张普通的 map——它的寿命被这次活化本身
	// 圈住（活化一摘掉整张表就没了），所以留住那几个祖先的时间不会超过驻留本身。
	ancestry map[agent.Agent]struct{}
	// ownedChildren 是这次活化名下那些孩子活化的会话 id。一份会话至多一个活的活化，
	// 所以这个 id 就认得出那个活孩子，不需要第二个运行期化身引用。非空会挡住结清。
	ownedChildren map[session.SessionID]struct{}
	// disposal 是那次被记下来的处置事务。**在不在就是准入闸**：处置一开始它就被
	// 同步赋上，于是没有哪一次投递能挤进一个正在拆的句柄，而一次赛跑中的投递会
	// 先等它、再去冷恢复一次新的活化。
	disposal *disposalTx
	// accepted 是已经被接受、但管理器还没看着它离开收件箱的那些唤醒消息 id。
	// 在 followup 和「准入它的那一跳」之间，agent 的状态仍旧是 idle，所以结清
	// 绝不能把那个空档当成静止。
	accepted map[llm.MessageID]struct{}
	// announced 表示有没有任何一次对这个孩子的投递被接受过。一次在首次接受之前
	// 就被回滚的物化，是一个「调用方被告知它不存在」的孩子，所以它的拆解不欠父
	// 任何结清交代。
	announced bool
	// poke 在结清守望要重新观察一次静止时被换掉。
	//
	// 新增: DSH 是 `PromiseWithResolvers<void>`。Go 里是一个关掉就算数的通道，
	// 唤醒时关掉它、并在锁里换上一个新的。
	poke chan struct{}
}

// materialization 是一次被准入的物化，以及它在那道同步准入边界上观察到的、确切的
// 活血统。
//
// 源: packages/subagent/subagent/src/continuation.ts:264-269
//
// 留住那几个身份，就让一次带范围的拆解在中途某个 agent 离开注册表之后仍旧等得下去。
type materialization struct {
	lineage []agent.Agent
	// settled 在这次物化走到公布或者回滚之后关闭。
	//
	// 新增: DSH 是一个 `Promise<void>`。这里只需要「完没完」，所以一个一次性
	// 关闭的通道就够，不必背一份结局。
	settled chan struct{}
}

// settlementSummary 是告诉父「一个后台孩子完了、以及为什么」的那一行，用的是父
// 自己那套任务词汇。
//
// 源: packages/subagent/subagent/src/continuation.ts:290-318
//
// 给模型看的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线。
func settlementSummary(childID session.SessionID, stopReason StopReason) string {
	subject := fmt.Sprintf("Background subagent %s", childID)
	switch stopReason {
	case StopCompleted:
		return subject + " finished and will do no further work unless you send it more."
	case StopAborted:
		return subject + " was stopped before it finished."
	case StopMaxTokens:
		return subject + " ran out of room before it finished."
	case StopRefusal:
		// 一次前置步骤的拒绝——一个钩子的 deny、一个策略插件——丢掉了孩子已经
		// 认领下来的输入，所以父绝不能把这件任务当成做完了。
		return subject + " declined the task."
	case StopError:
		return subject + " failed before it finished."
	default:
		// [StopReason] 是一个开放的具名字符串类型，后端可以加自己的取值；
		// 一个叫不出名字的结局报成「没做完」，而不是无声地当成成功。
		return fmt.Sprintf("%s ended abnormally (%s) before it finished.", subject, stopReason)
	}
}

// ContinuationDeps 是续接管理器要用到的那几样服务。
//
// 新增: DSH 从它那个 cordis 上下文上现取 `ctx.agents`、`ctx.get('sessions')`、
// `ctx.get('sessionPersistence')`。Go 没有那个容器，「在不在场」就是装配方手上
// 有没有这个值，所以做成一个显式的结构体（和 [ListingServices]、
// [ChildCompositionServices] 同一种做法）。
type ContinuationDeps struct {
	// Owner 是拥有这个管理器的那把作用域；活化所有者作用域挂在它下面，
	// 排干也登记在它上面。必填。
	Owner *scope.Scope
	// Agents 是 agent 注册表，孩子的创建、恢复、查活都走它。必填。
	Agents *agent.Registry
	// Sessions 是活会话表，占 id 时要查它。必填。
	Sessions *coresession.Store
	// Persistence 是会话持久化；nil 表示这套部署起不了可续孩子
	// （报 [CodePersistenceUnavailable]）。
	Persistence persistence.Store
	// Setups 是那张可续孩子装配登记表。必填。
	Setups *ActivationSetupRegistry
	// Approval 是审批服务；nil 表示这套部署没组装审批能力，那份派发策略就不种。
	Approval *userapproval.Service
	// Composition 是挂孩子组合要用的那几样服务。
	Composition ChildCompositionServices
	// Logger 记那几件 fail-soft 路径上咽下去的事：拆解失败、结清通知没投出去、
	// 最后那次刷盘没成。为 nil 时用 [log/slog.Default]。
	Logger *slog.Logger
}

// errInvalidRequestf 是本文件这几处「调用方给错了东西」的统一说法。
func errInvalidRequestf(format string, args ...any) error {
	return fmt.Errorf("%w：%s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

// continuationCancelled 在下一个取消检查点把一次续接操作停掉；没取消时返回 nil。
//
// 源: packages/subagent/subagent/src/continuation.ts:429, 443（signal.throwIfAborted）
func continuationCancelled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return NewError("子 agent 续接操作被取消了", CodeCancelled, err)
	}
	return nil
}

// warn 记一条 fail-soft 路径上的诊断。
func (m *ContinuationManager) warn(message string, args ...any) {
	logger := m.deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(message, args...)
}

// ContinuationManager 是 `ctx.subagents` 背后那台可续子 agent 编排机。
//
// 源: packages/subagent/subagent/src/continuation.ts:350-1619（SubagentContinuationManager）
//
// 工具 schema 和宿主适配器都是这一份契约的消费方；前台那条一次性派发仍旧调
// [Provider.Start]，一步都不进这条生命周期。
type ContinuationManager struct {
	deps ContinuationDeps
	host continuationHost
	// locks 把每个耐久孩子上的投递、放权、处置串成一条线。
	locks *childLock
	// ownerScope 是每一个活化句柄结构上的主人。
	ownerScope *scope.Scope

	// mutex 管下面这几张表，以及活化上那些可变字段。
	//
	// 新增: DSH 是单线程的，这一把锁在那边没有对应物。规矩和
	// [ActivationSetupRegistry] 一样：**用户给的函数和一切会阻塞的调用一律在锁外**，
	// 锁里只做原子的簿记。
	mutex sync.Mutex
	// activations 是孩子会话 id 到它那个活的活化。进程内的，绝不耐久。
	activations map[session.SessionID]*activation
	// materializations 是排干之前被准入的那些物化，一路跟到公布或者回滚。
	materializations map[*materialization]struct{}
	// closingScopes 是那些宿主拆解已经开始的确切根，以及在每个根底下观察到的
	// 活血统成员。条目一直留到那个确切的根离开 agent 注册表为止，于是它整段拆解
	// 期间准入都是关的，而又不会毒到一个后来的同 id 替身。
	closingScopes map[agent.Agent]map[agent.Agent]struct{}
	draining      bool
}

// NewContinuationManager 造一台续接管理器，并把它挂到拥有它的那把作用域上。
//
// 源: packages/subagent/subagent/src/continuation.ts:372-392
//
// 普通的作用域清理**倒着**跑，那个次序表达不了这张动态的孩子图。所以这里先登记
// 那把私有作用域的结构性处置、**后**登记排干：倒着展开于是先排干、再放掉作用域。
// 把清理和 agent 句柄挂在同一把作用域上，会让结构性的句柄处置绕过「孩子优先」那条
// 次序。
func NewContinuationManager(deps ContinuationDeps, host continuationHost) (*ContinuationManager, error) {
	if deps.Owner == nil {
		return nil, fmt.Errorf("%w：可续子 agent 管理器需要一把拥有它的作用域", ErrInvalidRequest)
	}
	if deps.Agents == nil {
		return nil, fmt.Errorf("%w：可续子 agent 管理器需要 agent 注册表", ErrInvalidRequest)
	}
	if deps.Sessions == nil {
		return nil, fmt.Errorf("%w：可续子 agent 管理器需要活会话表", ErrInvalidRequest)
	}
	if deps.Setups == nil {
		return nil, fmt.Errorf("%w：可续子 agent 管理器需要装配登记表", ErrInvalidRequest)
	}
	if host == nil {
		return nil, fmt.Errorf("%w：可续子 agent 管理器需要一个宿主", ErrInvalidRequest)
	}
	ownerScope, err := scope.New(scope.NewKey("subagents.activationOwner"), scope.Options{Parent: deps.Owner.Key()})
	if err != nil {
		// 走不到：每次 NewKey 都铸一把新的，所以撞不上；父那把作用域是不是还在，
		// 这一步也不管（那由下面那笔登记认出来）。
		return nil, err
	}
	manager := &ContinuationManager{
		deps:             deps,
		host:             host,
		locks:            newChildLock(),
		ownerScope:       ownerScope,
		activations:      map[session.SessionID]*activation{},
		materializations: map[*materialization]struct{}{},
		closingScopes:    map[agent.Agent]map[agent.Agent]struct{}{},
	}
	// 一个离开注册表的根不再关着任何准入；条目留到那一刻为止，正好避开「一个后来
	// 的同 id 替身继承了前任那条关闸」。
	//
	// 这个 disposer **有意**丢掉：owner 就是拥有这个管理器的那把作用域，
	// 作用域一处置这次登记就跟着没了。
	if _, err := deps.Agents.OnDisposed(context.Background(), deps.Owner, func(disposed agent.Agent) {
		manager.mutex.Lock()
		defer manager.mutex.Unlock()
		delete(manager.closingScopes, disposed)
	}); err != nil {
		return nil, err
	}
	// 次序要紧：结构性处置先登记、排干后登记，倒着展开时才是「先排干、后放作用域」。
	// 走不到（下面两笔）：Defer 只在这把作用域已经处置时失败，而那种 owner 上面
	// 那笔 OnDisposed 就已经挡下了。
	if _, err := deps.Owner.Defer("subagents.activationOwner()", ownerScope.Dispose); err != nil {
		return nil, err
	}
	if _, err := deps.Owner.Defer("subagents.continuations()", manager.Drain); err != nil {
		return nil, err
	}
	return manager, nil
}
