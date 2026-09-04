// 本文件的作用：挂在注册表上的那十二组观察者——各自的签名、失败语义，
// 以及它们合起来的那一层。
//
// 源: packages/core/agent/src/runtime-types.ts:146-292

package agent

import (
	"context"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// CreatedObserver 是一次 agent 公布的观察者，**有否决权**。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/created`）
//
// 返回错误（或者 panic）会让 [Registry.Announce] 失败。调用方交出去的那个摘除
// 函数随即把这次登记回滚掉，并配对地发出一次 disposed——所以一个否决掉创建的
// 观察者不会留下一份「登记了但没人知道」的 agent。
//
// 装配只管组装，不管驱动：真正的第一个启动扩展点是 [SessionStartObserver]。
//
// 新增: DSH 那边监听器可以是 async 函数，于是它多一条「返回的 promise 拒绝了
// 只能记日志，来不及否决」的路。Go 里签名就是同步返回 error，那条路不存在。
type CreatedObserver func(ctx context.Context, agent Agent) error

// DisposedObserver 是「一个已公布的 agent 离开了注册表」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/disposed`）
//
// 只观察，不否决：这条边是配对通知，它已经发生了。观察者 panic 被逐个兜住记日志。
//
// 循环那一层在驱动静默、作用域登记散完**之后**、会话摘除**之前**报它。自己造
// 注册表的调用方自己负责那个次序约定。
type DisposedObserver func(agent Agent)

// StatusObserver 是状态跃迁的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/status`）
//
// 报的是**跃迁的落点**，不是来处。观察者 panic 被逐个兜住记日志。
type StatusObserver func(agent Agent, status Status)

// InboxObserver 是一条消息进出收件箱的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:179-186、198-205
//
// inserted 和 discarded 共用这一个签名。它们分别登记（[Registry.OnInboxInserted]
// 与 [Registry.OnInboxDiscarded]），只是形状一样。
type InboxObserver func(agent Agent, message llm.Message)

// InboxClaimedObserver 是「一条消息在它那个已开回合里被认领走」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/inbox/claimed`）
//
// 提议的那个步骤如果被拒，被认领的这条消息就到此为止：它既不会被丢弃、也不会
// 被重新报成一条 user/message，而那个回合会不进步骤就关掉。
type InboxClaimedObserver func(agent Agent, message llm.Message, turn int)

// SessionStartObserver 是「会话生命周期开始了」的观察者，第一个回合之前一次。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/session-start`）
//
// 这是通知不是否决；一个生命周期持有者提出的处置会在驱动起跑之前被重新检查。
// 想往里塞模型可见的上下文就调 [Agent.Inject]。
type SessionStartObserver func(agent Agent, source SessionStartSource)

// PreStep 是一个提议中的步骤，交给 [PreStepObserver] 过目。
//
// 源: packages/core/agent/src/runtime-types.ts:220-231
type PreStep struct {
	// Agent 是提议这个步骤的那一个。
	Agent Agent
	// Messages 是为这个步骤从收件箱里取出来的那些消息。
	Messages []llm.Message
	// Turn 是将会拥有这个步骤的回合。
	Turn int
	// Step 是循环提议的那个步骤号。
	Step int
}

// PreStepObserver 决定一个提议中的步骤进不进、带着哪些消息进。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/pre-step`，瀑布）
//
// **先登记的在外层**，一层层往里套，最里面那个 next 交出机器本来的提议。这条
// 次序照抄 cordis 的 waterfall（vendor/cordis/src/events.ts:234-243，它从名单头上
// shift，而登记是 push），本仓库另外几条瀑布（tools、harness/systemprompt）
// 也是这一条。
//
// 调 next 就是「保留当下这个提议」——它把里面那一层的决定原样交回来，观察者可以
// 直接返回它，也可以自己造一个 [RejectStep] 或者 [EnterStep] 换掉。不调 next
// 就否掉了后面所有人，包括机器本来那个提议。
//
// 新增: DSH 那个 signal 参数在 Go 里是第一个 [context.Context]，规矩和本仓库
// 每一处一样。它是当下这个回合的取消口。
type PreStepObserver func(ctx context.Context, step PreStep, next func(context.Context) (PreStepDecision, error)) (PreStepDecision, error)

// Request 是一次将要发出的模型调用，交给 [RequestObserver] 过目。
//
// 源: packages/core/agent/src/runtime-types.ts:232-244
type Request struct {
	// Agent 是发这次调用的那一个。
	Agent Agent
	// Turn 是当下开着的那个回合号。
	Turn int
	// Step 是这次请求所属的步骤号。
	Step int
}

// RequestObserver 换掉那份已经定下来的调用配置。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/request`，瀑布）
//
// 调 next 拿到「机器本来会用的那一份」——第一次请求是 agent 选项，之后是日志里
// 那份请求头；返回一份新的就切过去。
//
// 模型可见的**内容**必须走记进日志的那些通道：这条瀑布改不了消息。
type RequestObserver func(ctx context.Context, request Request, next func(context.Context) (llm.CallConfig, error)) (llm.CallConfig, error)

// RequestFailure 是一次失败了的模型请求尝试，交给 [RequestErrorObserver] 过目。
//
// 源: packages/core/agent/src/runtime-types.ts:245-260
type RequestFailure struct {
	// Agent 是这次请求失败的那一个。
	Agent Agent
	// Turn 是装着这次失败请求的回合。
	Turn int
	// Step 是装着这次失败尝试的步骤。
	Step int
	// Provider 是这次失败请求选中的那个提供方。
	Provider string
	// Failure 是在最后那道适配器边界上规整出来的可序列化事实。
	Failure llm.Failure
	// RetryPolicy 是服务这次请求的那份适配器登记的重试策略。
	//
	// HasRetryPolicy 为假时它没有意义。
	RetryPolicy llm.ResolvedRetryPolicy
	// HasRetryPolicy 表示确实有这么一份策略。
	//
	// 新增: DSH 是 `ResolvedRetryPolicy | undefined`。Go 里这个结构体的零值是
	// 一份合法的策略（全都不重试），和「没有策略」区分不开，所以另拿一位说。
	HasRetryPolicy bool
}

// RequestErrorObserver 在循环重试或者收掉步骤之前，处理一次失败的模型请求尝试。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/request-error`，瀑布）
//
// 认领了恢复的观察者**不调 next**，直接返回 `RequestErrorAction{Retry: true}`；
// 想往下传就调 next。默认的零值让这次失败成为终局。
type RequestErrorObserver func(ctx context.Context, failure RequestFailure, next func(context.Context) (RequestErrorAction, error)) (RequestErrorAction, error)

// TurnStoppingObserver 是回合就要关掉时的串行观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/turn-stopping`，串行）
//
// 到这里时模型不欠任何回应了：没有活着的工具调用，也没有新的引导。边界提交
// **之前**等它跑完——一个有意见的观察者就自己 [Agent.Steer]，机器随后重读
// 收件箱：有新引导就再跑一个步骤，没有就关掉回合。
//
// **数据说了算**，所以观察者的登记顺序改变不了结果。反向的那个控制（提前收掉
// 一圈工具循环）也是数据：一条带 concludesTurn 的工具结果会在它那个步骤上结束
// 回合。这个结论从不短路已经提交的下一步活儿——同一步里的 additionalContexts
// 或者赛跑进来的引导照样会跑，回合只在那个收件箱排干之后才关。
type TurnStoppingObserver func(ctx context.Context, agent Agent, turn int) error

// TurnError 是一次在回合里冒出来的失败。
//
// 源: packages/core/agent/src/runtime-types.ts:279-290
type TurnError struct {
	// Agent 是出错的那一个。
	Agent Agent
	// Turn 是失败冒出来的那个回合。
	Turn int
	// Step 是失败冒出来的那个步骤。
	Step int
	// Err 是那个失败本身，原样。
	Err error
}

// ErrorObserver 是「一个步骤或者回合出错了」的观察者。
//
// 源: packages/core/agent/src/runtime-types.ts:19-20（`agent/error`）
//
// 机器在这里报失败，哪怕那个错误在回合里找不到一个位置留下耐久记录。
// 只观察，panic 被逐个兜住记日志。
type ErrorObserver func(failure TurnError)

// registryLayer 是一个作用域在这套观察者里的全部贡献。
//
// 源: packages/core/agent/src/runtime-types.ts:146-292（十二个 cordis 事件）
//
// 新增: DSH 靠 cordis 的作用域派发过滤监听器，本仓库统一换成 [scope.Layers]：
// 全局层加各作用域的覆盖层，派发时按载体作用域的父链取并集。
type registryLayer struct {
	created        *scope.AnonymousEntries[CreatedObserver]
	disposed       *scope.AnonymousEntries[DisposedObserver]
	status         *scope.AnonymousEntries[StatusObserver]
	inboxInserted  *scope.AnonymousEntries[InboxObserver]
	inboxDiscarded *scope.AnonymousEntries[InboxObserver]
	inboxClaimed   *scope.AnonymousEntries[InboxClaimedObserver]
	sessionStart   *scope.AnonymousEntries[SessionStartObserver]
	preStep        *scope.AnonymousEntries[PreStepObserver]
	request        *scope.AnonymousEntries[RequestObserver]
	requestError   *scope.AnonymousEntries[RequestErrorObserver]
	turnStopping   *scope.AnonymousEntries[TurnStoppingObserver]
	turnErrored    *scope.AnonymousEntries[ErrorObserver]
}

// newRegistryLayer 造一层。
func newRegistryLayer() *registryLayer {
	return &registryLayer{
		created:        scope.NewAnonymousEntries[CreatedObserver](),
		disposed:       scope.NewAnonymousEntries[DisposedObserver](),
		status:         scope.NewAnonymousEntries[StatusObserver](),
		inboxInserted:  scope.NewAnonymousEntries[InboxObserver](),
		inboxDiscarded: scope.NewAnonymousEntries[InboxObserver](),
		inboxClaimed:   scope.NewAnonymousEntries[InboxClaimedObserver](),
		sessionStart:   scope.NewAnonymousEntries[SessionStartObserver](),
		preStep:        scope.NewAnonymousEntries[PreStepObserver](),
		request:        scope.NewAnonymousEntries[RequestObserver](),
		requestError:   scope.NewAnonymousEntries[RequestErrorObserver](),
		turnStopping:   scope.NewAnonymousEntries[TurnStoppingObserver](),
		turnErrored:    scope.NewAnonymousEntries[ErrorObserver](),
	}
}

// IsEmpty 表示这一层十二张表全空了，[scope.Layers] 靠它回收空层。
func (l *registryLayer) IsEmpty() bool {
	return l.created.IsEmpty() && l.disposed.IsEmpty() && l.status.IsEmpty() &&
		l.inboxInserted.IsEmpty() && l.inboxDiscarded.IsEmpty() && l.inboxClaimed.IsEmpty() &&
		l.sessionStart.IsEmpty() && l.preStep.IsEmpty() && l.request.IsEmpty() &&
		l.requestError.IsEmpty() && l.turnStopping.IsEmpty() && l.turnErrored.IsEmpty()
}
