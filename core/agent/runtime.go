// 本文件的作用：一个活 agent 对外的那张面——它的选项、状态、以及循环那几个
// 扩展点上来回传的那些小值。
//
// 源: packages/core/agent/src/runtime-types.ts:1-144

package agent

import (
	"context"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// Options 是建一个 agent 时给的那几样。
//
// 源: packages/core/agent/src/runtime-types.ts:24-31
//
// 人设不在这里：那是系统提示词的段落，见 [ds-harness-go/core/systemprompt]。
//
// 新增: DSH 这个接口是**可合并扩展**的（插件用 declare module 往上加字段）。
// Go 没有声明合并，所以它就是这三个字段；别的包要带自己的 agent 级配置，
// 走它自己那份作用域登记，而不是往这个结构体上挤。
type Options struct {
	// Provider 是提供方路由，调用那一刻必须有一个登记过的适配器认领它。
	Provider string
	// Model 是模型标识，由选中的那个提供方适配器解释。
	Model string
	// MaxTokens 是每次对话模型请求的输出上限；0 表示不设，由适配器决定。
	MaxTokens int
}

// CancelOptions 是 [Agent.Cancel] 的选项。
//
// 源: packages/core/agent/src/runtime-types.ts:33-41
type CancelOptions struct {
	// KeepInbox 为真表示留下排队的和引导的收件箱条目，别丢。
	//
	// 正在跑的那个回合照样被中止，但还没开跑的活儿活到下一个回合，
	// 也不会记一条取消的收件箱改动。
	KeepInbox bool
}

// Status 是一个 agent 的生命周期状态，每次跃迁都会报一次。
//
// 源: packages/core/agent/src/runtime-types.ts:43-50
//
// 处置（disposal）会把 agent 从注册表里摘掉，它**不是**第三个可观察状态。
type Status string

const (
	// StatusIdle 表示没有驱动在跑。
	StatusIdle Status = "idle"
	// StatusRunning 从「唤醒输入开始跑可取消的前置步骤」那一刻起算，
	// 一直持续到驱动排干、收尾、或者落下检查点。
	StatusRunning Status = "running"
)

// PreStepDecision 是「这个提议的步骤进不进、带着哪些消息进」。
//
// 源: packages/core/agent/src/runtime-types.ts:52-55
//
// 新增: DSH 是 `{kind:'reject'} | {kind:'enter'; messages}` 两支联合。Go 里做成
// 一个结构体，用 [RejectStep] 和 [EnterStep] 两个构造器产出——两个字段的组合里
// 只有「Enter 为假且 Messages 为空」和「Enter 为真」有意义，那个判别标签在 Go
// 里就是 Enter 本身。
//
// **零值是拒绝**。这是有意的：一个观察者要么明确说进、要么什么都不做，
// 而「什么都不做」在这条链上必须是「别动这个提议」——真正的默认值由瀑布最里层
// 那个 next 给出，观察者只在想改主意时才自己造一个。
type PreStepDecision struct {
	// Enter 为真表示进这个步骤。
	Enter bool
	// Messages 是进去时带的那些消息，只在 Enter 为真时有意义。
	Messages []llm.Message
}

// RejectStep 造一个「不进这个步骤」的决定。
func RejectStep() PreStepDecision { return PreStepDecision{} }

// EnterStep 造一个「带着这些消息进这个步骤」的决定。
func EnterStep(messages []llm.Message) PreStepDecision {
	return PreStepDecision{Enter: true, Messages: messages}
}

// RequestErrorAction 是一个认领了模型请求恢复的观察者交出来的动作。
//
// 源: packages/core/agent/src/runtime-types.ts:57-58
//
// 新增: DSH 是 `{kind:'retry'} | undefined`。Go 里零值就是那个 undefined——
// 「不认领，这次失败是终局」。
type RequestErrorAction struct {
	// Retry 为真表示这个观察者认领了恢复，循环该再试一次。
	Retry bool
}

// SessionStartSource 是一段会话生命周期为什么开始的。
//
// 源: packages/core/agent/src/runtime-types.ts:60-61
type SessionStartSource string

const (
	// StartStartup 是带 seed 新建出来的。
	StartStartup SessionStartSource = "startup"
	// StartResume 是从持久化存储里读回来的。
	StartResume SessionStartSource = "resume"
	// StartClear 是清空历史之后重新开的。
	StartClear SessionStartSource = "clear"
	// StartCompact 是一次压缩之后重新开的。
	StartCompact SessionStartSource = "compact"
)

// Agent 是一个活 agent 对外的样子。
//
// 源: packages/core/agent/src/runtime-types.ts:63-144
//
// 本包不实现它——实现在循环那一层，见包文档。这里定的是那份契约，好让消费方
// 不必依赖具体的循环包。
type Agent interface {
	// ID 是它和 Session 共用的那一个身份。
	ID() sessionlog.SessionID

	// Options 是它那些请求用的提供方路由与模型。
	Options() Options

	// Session 是它驱动的那个活会话，日志是耐久的唯一事实。
	Session() *session.Session

	// Inbox 是它自己那份「还没跑的活儿」的投影。
	Inbox() *Inbox

	// Status 是当下的生命周期状态，每次跃迁都会被报一次。
	Status() Status

	// Scope 是这个 agent 那把作用域钥匙：挂在它上面的贡献都是 agent 局部的，
	// 处置时跟着散掉，之后再登记会被拒。
	//
	// 新增: DSH 这里是 `ctx: Context`（一个 agent 作用域的 cordis 上下文），
	// 它同时兼三件事：登记贡献的地方、派发过滤的键、以及读 ctx.agent 的载体。
	// Go 里前两件由 [scope.Scope] 做（登记要它本身，过滤要它的 [scope.Key]），
	// 第三件由 [CurrentInitiator] 做。
	Scope() *scope.Scope

	// Cancel 清掉排队和引导的活儿（除非 KeepInbox），并中止正在跑的那个回合
	// 或者回合之间那个任务。同一段活动上第一个来的原因算数。
	//
	// 没有活动在跑时这是个空操作，**不会**给之后的活儿上膛。
	Cancel(cause sessionlog.TurnEndCancelCause, options CancelOptions)

	// WhenIdle 等到整个 agent 这一层的活动静下来。
	//
	// 它跟得住「在被观察到的那个驱动退休之前就起了的替补活儿」，但它认不出
	// 任何一条具体消息的落地。
	//
	// 新增: DSH 是 `whenIdle(): Promise<void>`，取消靠外面那个 signal。Go 里
	// 第一个参数是 [context.Context]，它同时是那个取消口。
	WhenIdle(ctx context.Context) error

	// RunMaintenance 从真正的空闲期跑一件不是回合的维护活儿。
	//
	// 认领到那个空闲期之后任务立刻开跑；之后来的唤醒输入留在收件箱里等它落地，
	// 而对外的状态一直是 [StatusIdle]。WhenIdle 跟得住这个任务，也跟得住排在
	// 它后面被放出来的那些唤醒活儿。
	//
	// 已经有回合在驱动、或者已经有另一件维护活儿占着这个 agent 时，当场报错。
	//
	// 新增: DSH 是 `runMaintenance<T>(task): Promise<T>`，泛型把任务的产出原样
	// 带回来。Go 的接口方法不能带类型参数，所以这里只交出错误——要产出的调用方
	// 自己在闭包里接住。这不是能力上的损失：DSH 那个 T 也只是原样透传。
	RunMaintenance(ctx context.Context, task func(context.Context) error) error

	// Send 把一条认了身份的输入送进某条收件箱边界，并决定要不要顺便唤醒驱动。
	//
	// 在一次已经生效的取消之后送进来的唤醒输入，会排进下一个回合，等那段被中止
	// 的活动收敛到空闲时再跑；被 [sessionlog.DisposedCancel] 取消掉的则一直停着。
	// 已经空闲时送进来的唤醒**总是**开出它自己那个回合边界，哪怕它那条消息在
	// 驱动认领之前就被清掉了。
	Send(message llm.Message, target InboxTarget, wakeup bool)

	// Followup 排一个普通的后续回合并唤醒驱动。这条消息会成为它自己那个回合里
	// 唯一的普通消息。
	Followup(message llm.Message)

	// Steer 往最近的那个步骤递一条引导。空闲的驱动会因此开一个回合；正在跑的
	// 驱动在它下一个步骤边界上消费掉它。
	//
	// 一个被拒的步骤会把引导留在收件箱里等下一次唤醒；取消或者处置可能丢掉
	// 还没跑的引导。
	Steer(message llm.Message)

	// Inject 往下一个前置步骤排一份模型可见的上下文，**不**唤醒驱动。
	//
	// 正在跑的驱动在最近的那个后续步骤边界上认领它；空闲的驱动把它留着，
	// 等后续或者引导来唤醒。它可能赶不上一个前置步骤已经认领完批次的请求。
	// 取消或者处置可能丢掉还没跑的上下文。
	Inject(message llm.Message)

	// Prepend 把一条消息放回某条收件箱边界的**队头**，并且**不**唤醒驱动。
	//
	// 这是给「一个步骤被拒了，它认领走的那批消息得放回去」这件事用的：那批消息比
	// 队里现有的任何一条都先到，接回队尾会把人写话的先后颠倒过来。
	//
	// 新增: DSH 没有这条——它那些插件直接改 `agent.inbox`，单线程事件循环保证了
	// 不会有人和它们交错。Go 这边观察者跑在各自的协程上，而 [Inbox] 自己不带锁
	// （它的改动里夹着会话追加和那三条通知，加锁会把重入变成死锁），所以任何一次
	// 改动都必须走这个 agent 自己那把锁。[Inbox] 那一族改动方法因此只当只读投影
	// 用，见 [Agent.Inbox] 的注释。
	Prepend(message llm.Message, target InboxTarget)
}
