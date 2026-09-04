// 本文件的作用：默认的那个 agent 驱动——它按回合排队、在步骤边界上收输入，
// 而每一次模型请求都是从会话日志推导出来的。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515

package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"runtime/debug"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// Deps 是驱动一个循环要用到的那几样运行期设施。
//
// 新增: DSH 这些东西全挂在 cordis 的那个 loopCtx 上（ctx.agents、ctx.llm、
// ctx.tools、ctx.systemPrompt、ctx.session），一个万能容器。Go 里没有那个容器，
// 也不想造一个，所以它们是显式的字段——谁需要什么，签名上看得见。
type Deps struct {
	// Agents 是登记着那十二类观察者的注册表，循环的每一个扩展点都从它派发。
	Agents *agent.Registry
	// Sessions 是会话存储，运行期上下文投影靠它挂事件观察者。
	Sessions *session.Store
	// LLM 是模型运行时，请求从它这里准备和派发。
	LLM *llm.Runtime
	// Tools 是工具运行时，一个步骤请求的那些调用交给它。
	Tools *tools.Runtime
	// SystemPrompt 是系统提示词注册表，每个步骤边界装配一次。
	SystemPrompt *systemprompt.Registry
	// MaxParallelToolCalls 读出当下的并行池上限；为 nil 时用
	// [DefaultMaxParallelToolCalls]。
	//
	// 新增: DSH 那边这是一次读透设置（ctx.settings 的 getter），每一组工具调用
	// 起步之前重新读一遍。Go 里同一件事就是一个函数——本包不认识设置系统，
	// 装配循环的那一层负责把它接上去。
	MaxParallelToolCalls func() int
}

// phaseKind 是一个 agent 当下处在哪一相。
//
// 源: packages/core/agent-loop/src/agent.ts:38-46
type phaseKind int

const (
	// phaseIdle 是没有活动在跑。
	phaseIdle phaseKind = iota
	// phaseMaintenance 是一件不是回合的维护活儿占着这个 agent。
	phaseMaintenance
	// phaseRunning 是一个驱动正在跑回合。
	phaseRunning
)

// phase 是那三相合成的一个结构体。
//
// 源: packages/core/agent-loop/src/agent.ts:38-46
//
// 新增: DSH 是一个三支的可判别联合，每支各带各的字段。Go 里做成一个结构体加一个
// 判别标签，因为这个值要在 [ReactLoopAgent] 上被就地改写（DSH 直接 phase.turn = turn、
// phase.abort = new AbortController()），而一个接口值改不了里面的字段。
// 哪几个字段在哪一相有意义，见各字段注释。
type phase struct {
	// kind 是当下这一相。
	kind phaseKind
	// lastTurn 是最后开过的那个回合号，在 idle 和 maintenance 相有意义。
	lastTurn int
	// turn、step 是当下跑到第几回合第几步，只在 running 相有意义。
	turn, step int
	// ctx 是这段活动的取消口，在 maintenance 和 running 相有意义。
	//
	// 新增: DSH 是 AbortController，signal 传给底下每一层。Go 里同一件事是一个
	// 可取消的 [context.Context] 加它的 cancel，跑在里面的每一层顺着第一个参数
	// 拿到同一次取消。
	ctx context.Context
	// cancel 关掉 ctx，并把原因带上，在 maintenance 和 running 相有意义。
	cancel context.CancelCauseFunc
	// wakeRequested 是一次没法当场送达的唤醒上的膛，在 maintenance 和 running
	// 相有意义。
	wakeRequested bool
}

// statusOf 把一相投影成对外可见的状态。
//
// 源: packages/core/agent-loop/src/agent.ts:99-101
func statusOf(current phase) agent.Status {
	if current.kind == phaseRunning {
		return agent.StatusRunning
	}
	return agent.StatusIdle
}

// CancelError 是一次取消骑在 [context.CancelCauseFunc] 上的样子。
//
// 新增: DSH 直接 abort.abort(cause)——AbortSignal.reason 是 any，一个结构化的原因
// 原样放得进去。Go 的取消原因必须是一个 error，所以这里包一层。包着的那个原因
// 后面要原样写进 turn/end，见 [cancelCauseOf]。
type CancelError struct {
	// Cause 是那份要记进日志的、结构化的取消原因。
	Cause sessionlog.TurnEndCancelCause
}

// Error 让这个类型是一条错误。
func (e *CancelError) Error() string {
	if e.Cause == nil {
		// 走不到：本包只在 [ReactLoopAgent.Cancel] 里造它，那里的原因来自调用方，
		// 而接口值为 nil 的取消没有意义。
		return "harness/agentloop: 回合被取消"
	}
	return fmt.Sprintf("harness/agentloop: 回合被取消（%s）", e.Cause.CancelCauseKind())
}

// cancelCauseOf 从一个已取消的 ctx 上把那份结构化原因摸回来。
// 第二个返回值为假表示这不是本包发起的取消。
func cancelCauseOf(ctx context.Context) (sessionlog.TurnEndCancelCause, bool) {
	var carrier *CancelError
	if errors.As(context.Cause(ctx), &carrier) && carrier.Cause != nil {
		return carrier.Cause, true
	}
	return nil, false
}

// abortedErr 是 DSH 那句 signal.throwIfAborted() 在 Go 里的样子：没取消就是 nil，
// 取消了就是那条带原因的错误。
func abortedErr(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	return context.Cause(ctx)
}

// ReactLoopAgent 驱动一个会话跨过它的回合和步骤边界。
//
// 源: packages/core/agent-loop/src/agent.ts:69-545（ReactLoopAgent）
//
// 它实现 [github.com/snight1983/ds-harness-go/harness/agent.Agent]。除了「现在跑到第几回合第几步」之外
// 它自己身上不留状态——日志是权威的，重建一个 agent 就是从日志重建。
//
// # 并发
//
// 新增: DSH 是单线程 JS，这个类里一个同步原语都没有。Go 这边 [Send]、[Cancel]、
// [RunMaintenance] 由外面那些 goroutine 调，而驱动自己跑在另一个 goroutine 上，
// 所以 phase、requestHeaderLogged、activityDone 以及**所有对收件箱的访问**都由
// 本类型这一把锁护住。
//
// [github.com/snight1983/ds-harness-go/harness/agent.Inbox] 自己不加锁，那是它那个包写下的规矩
// （「只该被那个 agent 的循环碰」），所以串行化这件事只能由这里来做。
//
// 通知一律在锁外发：收件箱那三个回调是在 Splice／Claim **里面**同步叫起来的，
// 而 DSH 允许一个观察者在这中间反手调 [ReactLoopAgent.Cancel]（见 [Send] 里
// wakingAfterAbort 那句注释）。回调若在锁里直接派发，那条重入路径当场自锁。
// 所以回调只往 pending 上排闭包，等锁松开之后再统一发出去——这既保住了 DSH 的
// 次序（分类先于插入、观察者先于唤醒），也和本仓库「观察者一律在锁外调用」
// 那条通行规矩一致。
type ReactLoopAgent struct {
	// deps 是那几样运行期设施。
	deps Deps
	// base 是这个 agent 的根上下文，驱动的取消根从它派生。
	base context.Context
	// id 是它和会话共用的那一个身份。
	id sessionlog.SessionID
	// options 是它那些请求用的提供方路由与模型。
	options agent.Options
	// session 是它驱动的那个活会话。
	session *session.Session
	// scope 是这个 agent 的登记边界，也是派发过滤的键。
	scope *scope.Scope
	// inbox 是它自己那份「还没跑的活儿」的投影。
	inbox *agent.Inbox
	// runtimeContext 是把动态运行期上下文投影进会话表面的那一个。
	runtimeContext *RuntimeContextProjection

	// mutex 护住下面那几个字段以及所有对 inbox 的访问，理由见类型注释。
	mutex sync.Mutex
	// phase 是当下那一相。
	phase phase
	// requestHeaderLogged 表示**这个循环实例**已经写过它那条初始／恢复的请求头锚点。
	requestHeaderLogged bool
	// activityDone 在当下这段活动收敛时关闭；[WhenIdle] 等的就是它。
	//
	// 新增: DSH 是 Promise.withResolvers<void>()。Go 里同一件事是一个只关不写的
	// channel。没有活动时它是一个**已经关掉**的 channel，所以 WhenIdle 立刻返回。
	activityDone chan struct{}
	// pending 是排着队、等锁松开之后再发的那些通知，理由见类型注释。
	pending []func()
}

// 编译期断言：这个类型实现那张契约。
var _ agent.Agent = (*ReactLoopAgent)(nil)

// NewReactLoopAgent 在一个活会话上装一个驱动。
//
// 源: packages/core/agent-loop/src/agent.ts:80-97
//
// ctx 是这个 agent 的根上下文：它的值一路传下去，而**它的取消不传给驱动**，
// 理由见 [ReactLoopAgent.newActivityContext]。parent 是这个 agent 作用域挂上去的
// 外层作用域键，顶层的传 nil。
//
// 造出来的 agent 还没登记进注册表——那一步由造它的工厂做，好让「先把拆除挂上、
// 再公布」这条次序拿得住，见 [github.com/snight1983/ds-harness-go/harness/agent.Registry.Enter]。
func NewReactLoopAgent(
	ctx context.Context,
	deps Deps,
	parent *scope.Key,
	id sessionlog.SessionID,
	options agent.Options,
	live *session.Session,
) (*ReactLoopAgent, error) {
	if deps.Agents == nil || deps.Sessions == nil || deps.LLM == nil ||
		deps.Tools == nil || deps.SystemPrompt == nil {
		return nil, errors.New("harness/agentloop: 装一个循环要有注册表、会话存储、模型、工具和系统提示词五样")
	}
	if live == nil {
		return nil, errors.New("harness/agentloop: 装一个循环要有一个活会话")
	}
	if sessionID := live.ID(); id != sessionID {
		return nil, fmt.Errorf("harness/agentloop: agent 身份是 %q，会话身份是 %q", string(id), string(sessionID))
	}

	own, err := scope.New(scope.NewKey("agent:"+string(id)), scope.Options{Parent: parent})
	if err != nil {
		return nil, fmt.Errorf("harness/agentloop: 建 agent 作用域失败：%w", err)
	}

	lastTurn, err := lastTurnOf(live)
	if err != nil {
		return nil, err
	}

	done := make(chan struct{})
	close(done)
	loop := &ReactLoopAgent{
		deps:         deps,
		base:         ctx,
		id:           id,
		options:      options,
		session:      live,
		scope:        own,
		phase:        phase{kind: phaseIdle, lastTurn: lastTurn},
		activityDone: done,
	}

	// 收件箱那三个回调只排队不派发，理由见类型注释。它们必然是在持锁期间被叫到的
	// ——本类型对 inbox 的每一次触碰都在锁里。
	inbox, err := agent.NewInbox(live, agent.InboxNotifications{
		Inserted: func(message llm.Message) {
			loop.queueNotify(func() { _ = deps.Agents.ReportInboxInserted(loop, message) })
		},
		Discarded: func(message llm.Message) {
			loop.queueNotify(func() { _ = deps.Agents.ReportInboxDiscarded(loop, message) })
		},
		Claimed: func(message llm.Message, turn int) {
			loop.queueNotify(func() { _ = deps.Agents.ReportInboxClaimed(loop, message, turn) })
		},
	})
	if err != nil {
		return nil, fmt.Errorf("harness/agentloop: 建收件箱失败：%w", err)
	}
	loop.inbox = inbox

	projection, err := NewRuntimeContextProjection(ctx, own, deps.Sessions, live)
	if err != nil {
		return nil, err
	}
	loop.runtimeContext = projection
	return loop, nil
}

// lastTurnOf 从日志里读出最后开过的那个回合号；一条 turn/start 都没有就是 0。
//
// 源: packages/core/agent-loop/src/agent.ts:92
func lastTurnOf(live *session.Session) (int, error) {
	events := live.Events()
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != sessionlog.EventTurnStart {
			continue
		}
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			return 0, fmt.Errorf("harness/agentloop: turn/start 的负载读不回来：%w", err)
		}
		payload, ok := data.(sessionlog.TurnStartData)
		if !ok {
			// 走不到：这条事件的类型就是 turn/start，解码按类型分派。
			return 0, errors.New("harness/agentloop: turn/start 解出来不是回合负载")
		}
		return payload.Turn, nil
	}
	return 0, nil
}

// ---- 排队的通知 ----

// queueNotify 把一条通知排进待发队列。调用时必须拿着 [ReactLoopAgent.mutex]。
func (a *ReactLoopAgent) queueNotify(notify func()) {
	a.pending = append(a.pending, notify)
}

// takeNotifyLocked 取走待发队列。调用时必须拿着 [ReactLoopAgent.mutex]。
func (a *ReactLoopAgent) takeNotifyLocked() []func() {
	pending := a.pending
	a.pending = nil
	return pending
}

// runNotifications 在锁外把排好的那些通知发出去。
//
// 注册表报回来的错误一律吞掉，这是有意的：这些通知是**已经发生的事实**的广播，
// 一条广播不出去（比如这个 agent 还没公布、或者状态没变）改变不了那件事已经
// 发生。DSH 那边 emit 本来就不返回任何东西。
func runNotifications(pending []func()) {
	for _, notify := range pending {
		notify()
	}
}

// ---- 那张契约 ----

// ID 是它和会话共用的那一个身份。
func (a *ReactLoopAgent) ID() sessionlog.SessionID { return a.id }

// Options 是它那些请求用的提供方路由与模型。
func (a *ReactLoopAgent) Options() agent.Options { return a.options }

// Session 是它驱动的那个活会话。
func (a *ReactLoopAgent) Session() *session.Session { return a.session }

// Scope 是这个 agent 的登记边界。
func (a *ReactLoopAgent) Scope() *scope.Scope { return a.scope }

// Inbox 是它自己那份「还没跑的活儿」的投影。
//
// 只读着看。要往里放东西走 [ReactLoopAgent.Send] 那一族——它们在这个 agent 的锁里
// 动收件箱，直接改这个投影绕开了那把锁，见类型注释的并发那一节。
func (a *ReactLoopAgent) Inbox() *agent.Inbox { return a.inbox }

// Status 是当下对外可见的生命周期状态。
//
// 源: packages/core/agent-loop/src/agent.ts:99-101
func (a *ReactLoopAgent) Status() agent.Status {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return statusOf(a.phase)
}

// setPhaseLocked 落一相，并把它那次对外可见的状态跃迁排进待发通知。
//
// 源: packages/core/agent-loop/src/agent.ts:103-111
//
// 调用时必须拿着 [ReactLoopAgent.mutex]。
func (a *ReactLoopAgent) setPhaseLocked(next phase) {
	previous := statusOf(a.phase)
	a.phase = next
	status := statusOf(next)
	if status == previous {
		return
	}
	a.queueNotify(func() { _ = a.deps.Agents.ReportStatus(a, status) })
}

// Send 把一条认了身份的输入送进某条收件箱边界，并决定要不要顺便唤醒驱动。
//
// 源: packages/core/agent-loop/src/agent.ts:113-120
func (a *ReactLoopAgent) Send(message llm.Message, target agent.InboxTarget, wakeup bool) {
	a.mutex.Lock()
	// 一次唤醒进不了一段已经被取消的活动，所以它开的是下一个回合。分类要在插入
	// **之前**做完：一个从 splice 观察者里反手调过来的取消不能把它重新归类。
	wakingAfterAbort := wakeup && a.phase.kind != phaseIdle && a.phase.ctx.Err() != nil
	resolved := target
	if wakingAfterAbort {
		resolved = agent.NextTurn
	}
	// math.MaxInt 是 DSH 那个 Infinity：收件箱把起点夹进 [0, 长度]，所以它就是「排到最后」。
	_, err := a.inbox.Splice(resolved, math.MaxInt, 0, []llm.Message{message})
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if err != nil {
		// DSH 这里 splice 会抛，于是那次唤醒根本不发生。同样地：这条消息没进去，
		// 就没有它要唤醒的活儿。
		a.reportError(fmt.Errorf("harness/agentloop: 往收件箱送消息失败：%w", err))
		return
	}
	if wakeup {
		a.wakeDriver(wakingAfterAbort)
	}
}

// Followup 排一个普通的后续回合并唤醒驱动。
//
// 源: packages/core/agent-loop/src/agent.ts:122-124
func (a *ReactLoopAgent) Followup(message llm.Message) { a.Send(message, agent.NextTurn, true) }

// Steer 往最近的那个步骤递一条引导。
//
// 源: packages/core/agent-loop/src/agent.ts:126-128
func (a *ReactLoopAgent) Steer(message llm.Message) { a.Send(message, agent.NextStep, true) }

// Inject 往下一个前置步骤排一份模型可见的上下文，不唤醒驱动。
//
// 源: packages/core/agent-loop/src/agent.ts:130-132
func (a *ReactLoopAgent) Inject(message llm.Message) { a.Send(message, agent.NextStep, false) }

// Prepend 把一条消息放回某条边界的队头，不唤醒驱动。
//
// 新增: DSH 那些插件直接调 `agent.inbox.prepend(...)`。这边收件箱只当只读投影，
// 所以那条动作得从这里走一遍这把锁——理由见 [github.com/snight1983/ds-harness-go/harness/agent.Agent.Prepend]。
// 通知照 [ReactLoopAgent.Send] 的老规矩排到锁外再发：一条通知完全可能反手再调进来。
func (a *ReactLoopAgent) Prepend(message llm.Message, target agent.InboxTarget) {
	a.mutex.Lock()
	_, err := a.inbox.Splice(target, 0, 0, []llm.Message{message})
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if err != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 往收件箱队头放消息失败：%w", err))
	}
}

// Remove 从收件箱里拿掉一条还没跑的消息。
//
// 新增: 和 [ReactLoopAgent.Prepend] 同一个理由——收件箱只当只读投影，这条动作
// 得从这里走一遍这把锁，见 [github.com/snight1983/ds-harness-go/harness/agent.Agent.Remove]。
func (a *ReactLoopAgent) Remove(messageID llm.MessageID) {
	a.mutex.Lock()
	_, err := a.inbox.Remove(messageID)
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if err != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 从收件箱里拿掉消息失败：%w", err))
	}
}

// Replace 原地换掉一条还没跑的消息。
//
// 新增: 理由同 [ReactLoopAgent.Remove]。
func (a *ReactLoopAgent) Replace(messageID llm.MessageID, newMessage llm.Message) {
	a.mutex.Lock()
	_, err := a.inbox.Replace(messageID, newMessage)
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if err != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 换掉收件箱里那条消息失败：%w", err))
	}
}

// Cancel 清掉排队和引导的活儿（除非 KeepInbox），并中止正在跑的那段活动。
//
// 源: packages/core/agent-loop/src/agent.ts:134-140
//
// 新增: DSH 那边 inbox.clear() 会抛，抛了就跳过后面那次 abort——结果是一个
// 清不干净、又中止不掉的 agent。这里反过来：清空失败报出去，中止照做。
// 「这个回合停下来」是取消的**主要**承诺，收件箱清不干净只是次要损失。
func (a *ReactLoopAgent) Cancel(cause sessionlog.TurnEndCancelCause, options agent.CancelOptions) {
	a.mutex.Lock()
	var clearErr error
	if !options.KeepInbox {
		clearErr = a.inbox.Clear()
		if a.phase.kind != phaseIdle {
			a.phase.wakeRequested = false
		}
	}
	var cancel context.CancelCauseFunc
	if a.phase.kind != phaseIdle {
		cancel = a.phase.cancel
	}
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if clearErr != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 清空收件箱失败：%w", clearErr))
	}
	if cancel != nil {
		// context.CancelCauseFunc 只认第一个原因，正是「同一段活动上第一个来的算数」。
		cancel(&CancelError{Cause: cause})
	}
}

// RunMaintenance 从真正的空闲期跑一件不是回合的维护活儿。
//
// 源: packages/core/agent-loop/src/agent.ts:142-162
//
// 新增: ctx 是调用方那条链。维护活儿在 Go 里是**同步**跑完的（DSH 那边是一个
// 立刻返回的 promise），调用方的整段等待都在这个函数里，所以拿它当取消的父节点
// 既对得上语义，也让一次外层超时能穿透进来。这个 agent 自己的 [Cancel] 则通过
// 派生出来的那个 cancel 关它。
func (a *ReactLoopAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	if task == nil {
		return errors.New("harness/agentloop: 维护活儿不能是 nil")
	}

	a.mutex.Lock()
	if a.phase.kind != phaseIdle {
		a.mutex.Unlock()
		return fmt.Errorf("harness/agentloop: agent %q 已经有活儿在跑了", string(a.id))
	}
	lastTurn := a.phase.lastTurn
	done := make(chan struct{})
	jobCtx, cancel := context.WithCancelCause(ctx)
	jobCtx = agent.WithInitiator(jobCtx, a)
	a.setPhaseLocked(phase{kind: phaseMaintenance, lastTurn: lastTurn, ctx: jobCtx, cancel: cancel})
	a.activityDone = done
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	err := task(jobCtx)
	cancel(context.Canceled)

	a.mutex.Lock()
	wakeRequested := a.phase.wakeRequested
	a.setPhaseLocked(phase{kind: phaseIdle, lastTurn: lastTurn})
	hasPending := a.inbox.HasPending()
	pending = a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	// 唤醒**先于**关掉这段活动：先关的话，一个刚好在这两句之间调 WhenIdle 的人会
	// 看见「静下来了」，而那个被上膛的驱动其实马上就要起来。
	if wakeRequested && hasPending {
		a.wakeDriver(false)
	}
	close(done)
	return err
}

// newActivityContext 造一段活动自己的取消根。
//
// 新增: DSH 是 new AbortController()——一个**结构上独立**的取消源，和造它的那次
// 调用没有任何父子关系。Go 里同一件事是 [context.WithoutCancel]：值照样往下传，
// 取消不往下传。这不只是为了对齐 DSH，也是必需的——驱动活得比调 [Send] 的那次
// 调用长得多，挂在调用方 ctx 上会被那次调用返回时的取消带走。
//
// 顺带地，因为父节点不可取消，Go 不会为它起传播 goroutine，所以回合尾巴上
// **丢掉**旧的那个 cancel（DSH 就是丢掉，不是调用）不泄漏任何东西。
func (a *ReactLoopAgent) newActivityContext() (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(a.base))
	return agent.WithInitiator(ctx, a), cancel
}

// wakeDriver 起一个驱动，或者把这次唤醒上膛，留到维护活儿／被中止的活动收敛时再放。
//
// 源: packages/core/agent-loop/src/agent.ts:164-193
//
// wakeAfterAbort 是 [Send] 在插入**之前**做出的那次分类，见那里的注释。
func (a *ReactLoopAgent) wakeDriver(wakeAfterAbort bool) {
	a.mutex.Lock()
	if a.phase.kind != phaseIdle {
		// 维护活儿和被中止的驱动送不到这次唤醒：上膛，等收敛时重放。活着的驱动
		// 自己会去认领排队的活儿；处置从不上膛，所以拆除不会去等一个模型回合。
		cause, ok := cancelCauseOf(a.phase.ctx)
		disposed := ok && cause.CancelCauseKind() == sessionlog.CancelDisposed
		if !disposed && (a.phase.kind == phaseMaintenance || wakeAfterAbort) {
			a.phase.wakeRequested = true
		}
		a.mutex.Unlock()
		return
	}

	lastTurn := a.phase.lastTurn
	done := make(chan struct{})
	driverCtx, cancel := a.newActivityContext()
	a.activityDone = done
	a.setPhaseLocked(phase{
		kind:     phaseRunning,
		lastTurn: lastTurn,
		turn:     lastTurn,
		step:     0,
		ctx:      driverCtx,
		cancel:   cancel,
	})
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	go func() {
		// 关在 kick 之后：kick 收尾时可能又起一个驱动并装上新的 activityDone，
		// 那一份必须先就位，WhenIdle 才跟得住这次接力。
		defer close(done)
		// 新增: 最后一道网。回合正文那一层已经兜过一次
		//（见 [ReactLoopAgent.runTurnBody]），这里接的是漏出来的那些——kick 自己的
		// 收尾、turn 里正文之外的几段。它们炸了这个 agent 就地报废，但一个
		// goroutine 里没人接的 panic 是**整个进程**没，而这个包是嵌在别人的服务里
		// 跑的：一个 agent 的事故不许变成所有用户的事故。
		//
		// 这一层只报，不试图把相收回 idle——走到这儿说明状态机自己的收尾都没跑完，
		// 它手上那些字段处在什么样子无从知道，再去动只会把一次事故变成一次说谎。
		defer func() {
			if recovered := recover(); recovered != nil {
				_ = a.deps.Agents.ReportError(agent.TurnError{
					Agent: a,
					Err: fmt.Errorf(
						"harness/agentloop: agent %q 的驱动 panic 了，这个 agent 就此停住：%v\n%s",
						string(a.id), recovered, debug.Stack(),
					),
				})
			}
		}()
		a.kick()
	}()
}

// WhenIdle 等到整个 agent 这一层的活动静下来。
//
// 源: packages/core/agent-loop/src/agent.ts:195-200
//
// 新增: DSH 是 `do { await (activity = this.activityDone) } while (activity !== this.activityDone)`
// ——等完之后再看一眼那个句柄换没换，换了就说明有替补活儿接上了，接着等。
// Go 这里逐字是同一件事，只是等待的东西从 promise 换成 channel，并且多一个
// ctx 作为等待本身的取消口。
func (a *ReactLoopAgent) WhenIdle(ctx context.Context) error {
	for {
		a.mutex.Lock()
		activity := a.activityDone
		a.mutex.Unlock()

		select {
		case <-activity:
		case <-ctx.Done():
			return context.Cause(ctx)
		}

		a.mutex.Lock()
		current := a.activityDone
		a.mutex.Unlock()
		if current == activity {
			return nil
		}
	}
}

// reportError 在这次失败所在的那个活边界上报一次。
//
// 源: packages/core/agent-loop/src/agent.ts:202-208
//
// 调用时**不能**拿着 [ReactLoopAgent.mutex]：观察者可能反手调回本类型。
func (a *ReactLoopAgent) reportError(err error) {
	a.mutex.Lock()
	turn, step := a.phase.lastTurn, 0
	if a.phase.kind == phaseRunning {
		turn, step = a.phase.turn, a.phase.step
	}
	a.mutex.Unlock()
	_ = a.deps.Agents.ReportError(agent.TurnError{Agent: a, Turn: turn, Step: step, Err: err})
}

// kick 是一个驱动的全部生命：一个接一个跑回合，直到没有下一个。
//
// 源: packages/core/agent-loop/src/agent.ts:210-223
//
// 失败和取消都在这道驱动边界上兜住——它们已经在各自冒出来的地方报过、也已经写进
// 日志了，再往外抛没有人接。
func (a *ReactLoopAgent) kick() {
	for {
		more, err := a.turn()
		if err != nil || !more {
			break
		}
	}

	a.mutex.Lock()
	if a.phase.kind != phaseRunning {
		// 走不到：一个驱动从起步到这道边界一直拥有 running 这一相。
		a.mutex.Unlock()
		return
	}
	turn, wakeRequested := a.phase.turn, a.phase.wakeRequested
	a.setPhaseLocked(phase{kind: phaseIdle, lastTurn: turn})
	hasPending := a.inbox.HasPending()
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)

	if wakeRequested && hasPending {
		a.wakeDriver(false)
	}
}

// turnExit 是一个回合正文是怎么收场的。
//
// 新增: DSH 用 `return false` 和 `break` 两种控制流表达这件事——前者跳过回合尾巴、
// 驱动收工，后者走尾巴、可能再开一个回合。Go 里正文被拆成了单独一个函数
// （因为 turn/end 那次追加要在它之后无条件发生），控制流跨不出去，所以变成一个返回值。
type turnExit int

const (
	// turnExitStop 表示这个回合之后驱动就收工。
	turnExitStop turnExit = iota
	// turnExitTail 表示走回合尾巴，队列里还有活儿就再开一个回合。
	turnExitTail
)

// turn 开一个回合，跑完它，然后交出「还要不要再开一个」。
//
// 源: packages/core/agent-loop/src/agent.ts:245-330
func (a *ReactLoopAgent) turn() (bool, error) {
	a.mutex.Lock()
	if a.phase.kind != phaseRunning {
		a.mutex.Unlock()
		err := fmt.Errorf("harness/agentloop: agent %q：没有驱动预约就开回合", string(a.id))
		a.reportError(err)
		return false, err
	}
	// 每个回合各读一次：回合尾巴会换掉这一相里的 ctx，下一个回合等的是新的那个。
	ctx := a.phase.ctx
	turn := a.phase.turn + 1
	a.mutex.Unlock()

	if err := abortedErr(ctx); err != nil {
		return false, err
	}
	if _, err := a.appendEvent(sessionlog.TurnStartData{Turn: turn}, nil, nil); err != nil {
		a.reportError(err)
		return false, err
	}
	a.mutex.Lock()
	a.phase.turn = turn
	a.mutex.Unlock()

	reason, exit, bodyErr := a.runTurnBody(ctx, turn)

	// turn/end 无条件写：一个开了却没关的回合，读日志的人看不出它是怎么收场的。
	//
	// 新增: DSH 这一句在 finally 里，追加失败时那条新错误会**顶掉**正文那条
	//（JS 的 finally 抛出会丢掉在飞的异常）。这里两条都保住：追加失败照样报出去，
	// 但只有在正文没出错时才成为交出去的那一条。根因比收尾的次生故障重要。
	if _, err := a.appendEvent(sessionlog.TurnEndData{Turn: turn, Reason: reason}, nil, nil); err != nil {
		a.reportError(err)
		if bodyErr == nil {
			bodyErr = err
		}
	}
	if bodyErr != nil || exit == turnExitStop {
		return false, bodyErr
	}

	a.mutex.Lock()
	hasPending := a.inbox.HasPending()
	a.mutex.Unlock()
	if !hasPending {
		return false, nil
	}

	// 换一个全新的取消根：上一个上过的膛就此作废——活着的驱动自己去认领队列。
	nextCtx, cancel := a.newActivityContext()
	a.mutex.Lock()
	a.phase.ctx = nextCtx
	a.phase.cancel = cancel
	a.phase.wakeRequested = false
	a.phase.step = 0
	a.mutex.Unlock()
	return true, nil
}

// runTurnBody 跑回合正文，把正文里冒出来的 panic 收成一条普通的失败。
//
// 新增: 上游是 TypeScript，那边一个抛出去的异常会被 agent 边界的 try/catch 接住，
// 于是「正文炸了」和「正文返回了一条错误」本来就是同一条路。Go 里 panic 沿栈往上冲，
// 而驱动跑在自己那个 goroutine 上（见 [ReactLoopAgent.wakeDriver]）——没人接就是
// **整个进程**没，不是这一个会话没。一个嵌在长期运行的服务里的组件不能这样：
// 一个客户端手上的坏存档不该掀掉所有其他用户的会话。
//
// 兜在这一层而不是 goroutine 根上，是为了让 turn/end 照样写得下去。从这里交出去的
// 错误走的是和别的正文失败完全相同的那条路：写 turn/end、报一次 agent/error、
// 驱动收工、相回到 idle。兜在根上的话这个回合会在日志里永远开着，而读日志的人
// 看不出它是怎么收场的。
//
// 收场原因取 [github.com/snight1983/ds-harness-go/sessionlog.ErrorTurnEnd]，和
// turnBody 自己那个 fail 一模一样——对读日志的人来说「正文报了错」和「正文炸了」
// 是同一件事，两者都是这个回合没走完。
//
// 报错不走 [ReactLoopAgent.reportError]，因为它要拿 [ReactLoopAgent.mutex]，
// 而 panic 有可能正是在持锁的那一小段里发生的，那时候再去要一次就是死锁。
// 直接找注册表报，turn 是参数上现成的，step 交 0——精确到哪一步不值得拿一次
// 可能永远等下去的加锁去换。
//
// 兜不住的只剩一种：panic 恰好发生在本 agent 持着自己那把锁的时候。那几段都是
// 纯字段读写加一次收件箱认领，炸的可能性极低；真炸了这个 agent 从此卡住，但
// **进程还活着**，别人的会话照跑。
func (a *ReactLoopAgent) runTurnBody(
	ctx context.Context,
	turn int,
) (reason sessionlog.TurnEndReason, exit turnExit, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf(
			"harness/agentloop: agent %q 的第 %d 个回合正文 panic 了：%v\n%s",
			string(a.id), turn, recovered, debug.Stack(),
		)
		reason = sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}
		exit = turnExitStop
		_ = a.deps.Agents.ReportError(agent.TurnError{Agent: a, Turn: turn, Err: err})
	}()
	return a.turnBody(ctx, turn)
}

// turnBody 跑一个已经开了的回合里那一串步骤，交出这个回合的收场原因。
//
// 源: packages/core/agent-loop/src/agent.ts:262-315
func (a *ReactLoopAgent) turnBody(
	ctx context.Context,
	turn int,
) (sessionlog.TurnEndReason, turnExit, error) {
	var turnEnds sessionlog.TurnEndReason
	target := agent.NextTurn

	// fail 把一条冒出来的错误折成这个回合的收场原因。取消**不报** agent/error：
	// 它不是故障，而且它已经作为 aborted 写进 turn/end 了。
	fail := func(err error) (sessionlog.TurnEndReason, turnExit, error) {
		if ctx.Err() != nil {
			if cause, ok := cancelCauseOf(ctx); ok {
				return sessionlog.AbortedTurnEnd{Reason: cause}, turnExitStop, err
			}
			// 走不到：驱动那个 ctx 的父节点不可取消，唯一关得掉它的是
			// [ReactLoopAgent.Cancel]，而它一定把原因包成 *CancelError 带上。
			// 真落到这儿也不能伪造一个原因——sessionlog.LegacyCancel 是给读旧日志
			// 用的，循环一个字都不许往日志里写它。
			return sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}, turnExitStop, err
		}
		a.reportError(err)
		// 每一次失败都是结构化的：一条 *llm.Error 留着它自己那份事实，别的都摊成
		// UNKNOWN 码加一句文本。
		return sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}, turnExitStop, err
	}

	for {
		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}

		a.mutex.Lock()
		priorStep := a.phase.step
		a.mutex.Unlock()
		step := priorStep + 1

		decision, assembly, err := a.preStep(ctx, target, turn, step)
		if err != nil {
			return fail(err)
		}
		if !decision.Enter {
			return sessionlog.BlockedTurnEnd{}, turnExitStop, nil
		}
		if turnEnds != nil && len(decision.Messages) == 0 {
			return turnEnds, turnExitTail, nil
		}
		// 一条被撤走的唤醒消息、或者一个被改写成空的准入决定，照样拥有这个回合的
		// 开场边界，只是它不花一次模型调用。
		if priorStep == 0 && len(decision.Messages) == 0 {
			return sessionlog.CompletedTurnEnd{}, turnExitStop, nil
		}

		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}
		if _, err := a.appendEvent(sessionlog.StepStartData{Turn: turn, Step: step}, nil, nil); err != nil {
			return fail(err)
		}
		a.mutex.Lock()
		a.phase.step = step
		a.mutex.Unlock()

		stepEnd, err := a.runStep(ctx, turn, step, decision.Messages, assembly)
		if err != nil {
			return fail(err)
		}
		// max-tokens 是粘的：一旦有哪个步骤撞了上限，后面正常收场的步骤不许把这个
		// 回合的结论降回去。
		if turnEnds == nil || turnEnds.TurnEndReasonKind() != sessionlog.ReasonMaxTokens {
			turnEnds = stepEnd
		}

		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}
		if turnEnds != nil && a.nextStepEmpty() {
			if err := a.deps.Agents.TurnStopping(ctx, a, turn); err != nil {
				return fail(err)
			}
			if err := abortedErr(ctx); err != nil {
				return fail(err)
			}
		}
		// 再问一遍：收尾观察者可能刚刚往下一个步骤里放了东西。
		if turnEnds != nil && a.nextStepEmpty() {
			return turnEnds, turnExitTail, nil
		}
		target = agent.NextStep
	}
}

// nextStepEmpty 判「下一个步骤那条队列是不是空的」。
func (a *ReactLoopAgent) nextStepEmpty() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return len(a.inbox.NextStep()) == 0
}

// preStep 认领一批消息、装配一次提示词，然后过一遍步骤准入那条瀑布。
//
// 源: packages/core/agent-loop/src/agent.ts:225-243
func (a *ReactLoopAgent) preStep(
	ctx context.Context,
	target agent.InboxTarget,
	turn, step int,
) (agent.PreStepDecision, systemprompt.PromptAssembly, error) {
	var empty systemprompt.PromptAssembly

	a.mutex.Lock()
	if a.phase.kind != phaseRunning {
		a.mutex.Unlock()
		// 走不到：本类型里叫它的地方都先立好了 running 这一相。
		return agent.PreStepDecision{}, empty,
			fmt.Errorf("harness/agentloop: agent %q：在 running 之外提议步骤", string(a.id))
	}
	claimed, err := a.inbox.Claim(target, turn)
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 认领收件箱失败：%w", err)
	}

	assembly, err := a.deps.SystemPrompt.Assemble(ctx, systemprompt.AssembleContext{Scope: a.scope.Key()})
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 装配系统提示词失败：%w", err)
	}
	if err := abortedErr(ctx); err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	sections, err := systemprompt.RenderContextSections(assembly)
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 渲染运行期上下文失败：%w", err)
	}
	snapshot, hasSnapshot := a.runtimeContext.Project(systemprompt.JoinContextSections(sections), sections)

	decision, err := a.deps.Agents.ResolvePreStep(
		ctx,
		agent.PreStep{Agent: a, Messages: claimed, Turn: turn, Step: step},
		func(context.Context) (agent.PreStepDecision, error) {
			// 另起一份切片：claimed 的底层数组可能还有富余容量，直接 append 会把
			// 那块内存写花，而收件箱那一侧还留着同一个数组的别名。
			messages := make([]llm.Message, 0, len(claimed)+1)
			messages = append(messages, claimed...)
			if hasSnapshot {
				messages = append(messages, snapshot)
			}
			return agent.EnterStep(messages), nil
		},
	)
	if err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	if err := abortedErr(ctx); err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	return decision, assembly, nil
}

// runStep 把这一步认领到的消息落进日志，跑完这一步，然后无条件写下 step/end。
//
// 源: packages/core/agent-loop/src/agent.ts:281-293
func (a *ReactLoopAgent) runStep(
	ctx context.Context,
	turn, step int,
	messages []llm.Message,
	assembly systemprompt.PromptAssembly,
) (reason sessionlog.TurnEndReason, err error) {
	// 新增: 和 turn/end 那一处同样的取舍——追加失败照样报出去，但只有正文没出错时
	// 才成为交出去的那一条。DSH 在 finally 里抛，会顶掉根因。
	defer func() {
		if _, appendErr := a.appendEvent(sessionlog.StepEndData{Turn: turn, Step: step}, nil, nil); appendErr != nil {
			a.reportError(appendErr)
			if err == nil {
				err = appendErr
			}
		}
	}()

	for _, message := range messages {
		if _, appendErr := a.appendEvent(sessionlog.UserMessageData{Message: message}, sessionlog.AppendOp{}, nil); appendErr != nil {
			return nil, appendErr
		}
	}
	return a.step(ctx, turn, step, assembly)
}

// step 发一次模型请求，把它的输出落进日志，并派发它要求的那些工具调用。
//
// 源: packages/core/agent-loop/src/agent.ts:332-420
//
// 交出这一步给回合的收场原因；为 nil 表示这个回合还没完（工具跑过了，接着下一步）。
func (a *ReactLoopAgent) step(
	ctx context.Context,
	turn, step int,
	assembly systemprompt.PromptAssembly,
) (sessionlog.TurnEndReason, error) {
	if err := abortedErr(ctx); err != nil {
		return nil, err
	}
	system, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		return nil, fmt.Errorf("harness/agentloop: 渲染系统提示词失败：%w", err)
	}

	for {
		// 每次重试都重新推导一遍：上一次尝试可能已经往日志上留下了痕迹。
		boundaryMessages, err := a.session.DeriveMessages()
		if err != nil {
			return nil, fmt.Errorf("harness/agentloop: 推导边界消息失败：%w", err)
		}
		request, prepared, err := a.buildRequest(ctx, turn, step, assembly.Tools, system, boundaryMessages)
		if err != nil {
			return nil, err
		}

		assembler := llm.NewBlockAssembler()
		var chunkSeqs []int
		streamErr := a.consumeStream(ctx, request, prepared, turn, step, assembler, &chunkSeqs)
		if streamErr != nil {
			// 被打断时把已经吐出来的那个安全前缀定稿，重放才读得通。
			if ctx.Err() != nil {
				if err := a.appendInterrupted(turn, step, request, assembler, chunkSeqs); err != nil {
					return nil, errors.Join(streamErr, err)
				}
			}
			return nil, streamErr
		}

		var failure llm.Failure
		terminal := false
		switch finished := assembler.Finish().(type) {
		case llm.ErrorFinish:
			failure, terminal = finished.Failure, true
		case llm.AbortedFinish:
			failure, terminal = finished.Failure, true
		}
		if terminal {
			retryPolicy, hasRetryPolicy := llm.ResolvedRetryPolicy{}, false
			if prepared != nil {
				retryPolicy, hasRetryPolicy = prepared.RetryPolicy(), true
			}
			action, err := a.deps.Agents.ResolveRequestError(
				ctx,
				agent.RequestFailure{
					Agent: a, Turn: turn, Step: step,
					Provider:       request.Provider,
					Failure:        failure,
					RetryPolicy:    retryPolicy,
					HasRetryPolicy: hasRetryPolicy,
				},
				func(context.Context) (agent.RequestErrorAction, error) {
					return agent.RequestErrorAction{}, nil
				},
			)
			if err != nil {
				return nil, err
			}
			if err := abortedErr(ctx); err != nil {
				return nil, err
			}
			if !action.Retry {
				// 直接造结构体而不是走 [llm.NewError]：那个构造器只收得下 message
				// 和 code，而这份事实里的 status、providerRetryAfterMs、requestId
				// 是上层路由要用的。DSH 那边 new LlmError(msg, code, finish.failure)
				// 的第三个参数就是把整份事实原样带上。
				return nil, &llm.Error{Failure: failure}
			}
			continue
		}

		message, err := assembledMessage(request, assembler)
		if err != nil {
			return nil, err
		}
		data := sessionlog.AssistantMessageData{Turn: turn, Step: step, Message: message}
		if usage, ok := assembler.Usage(); ok {
			data.Usage = &usage
		}
		if _, err := a.appendEvent(data, sessionlog.AppendOp{}, chunkSeqs); err != nil {
			return nil, err
		}
		if _, hitCeiling := assembler.Finish().(llm.MaxTokensFinish); hitCeiling {
			return sessionlog.MaxTokensTurnEnd{}, nil
		}

		var toolCalls []llm.ToolCallBlock
		for _, block := range message.Content {
			if call, ok := block.(llm.ToolCallBlock); ok {
				toolCalls = append(toolCalls, call)
			}
		}
		if len(toolCalls) == 0 {
			return sessionlog.CompletedTurnEnd{}, nil
		}
		concluded, err := ExecuteToolCalls(
			ctx, a.deps.Tools, a.maxParallelToolCalls(), turn, step, toolCalls, a.acceptToolContext,
		)
		if err != nil {
			return nil, err
		}
		if concluded {
			return sessionlog.CompletedTurnEnd{}, nil
		}
		return nil, nil
	}
}

// consumeStream 把一次请求的流吃完：每个分块先落日志再喂装配器。
//
// 源: packages/core/agent-loop/src/agent.ts:345-353
//
// 先落日志后喂装配器是有意的：日志是权威的，一条没记下来的分块等于没发生过，
// 而一个吃了它的装配器会装出一条日志重放不出来的消息。
func (a *ReactLoopAgent) consumeStream(
	ctx context.Context,
	request llm.GenerateOptions,
	prepared *llm.PreparedCall,
	turn, step int,
	assembler *llm.BlockAssembler,
	chunkSeqs *[]int,
) error {
	var chunks iter.Seq2[llm.StreamChunk, error]
	var err error
	if prepared != nil {
		chunks, err = prepared.Stream(ctx, request)
	} else {
		// 中间件可以服务一条没登记的路由，但终端派发照样要一个适配器。
		chunks, err = a.deps.LLM.Stream(ctx, request)
	}
	if err != nil {
		return err
	}
	if err := abortedErr(ctx); err != nil {
		return err
	}
	for chunk, err := range chunks {
		if err != nil {
			return err
		}
		if err := abortedErr(ctx); err != nil {
			return err
		}
		event, appendErr := a.appendEvent(
			sessionlog.AssistantChunkData{Turn: turn, Step: step, Chunk: chunk}, nil, nil)
		if appendErr != nil {
			return appendErr
		}
		*chunkSeqs = append(*chunkSeqs, event.Seq)
		assembler.Push(chunk)
	}
	return abortedErr(ctx)
}

// appendInterrupted 把一条被打断的流那个可以安全定稿的前缀写进日志。
//
// 源: packages/core/agent-loop/src/agent.ts:355-369
//
// 一个字都没吐出来时什么都不写：一条空的助手消息在重放里是凭空多出来的一轮。
func (a *ReactLoopAgent) appendInterrupted(
	turn, step int,
	request llm.GenerateOptions,
	assembler *llm.BlockAssembler,
	chunkSeqs []int,
) error {
	content := assembler.InterruptedBlocks()
	if len(content) == 0 {
		return nil
	}
	data := sessionlog.AssistantMessageData{
		Turn: turn,
		Step: step,
		Message: llm.NewAssistantMessage(
			content, llm.Provenance{Provider: request.Provider, Model: request.Model}),
		Interrupted: true,
	}
	if usage, ok := assembler.Usage(); ok {
		data.Usage = &usage
	}
	_, err := a.appendEvent(data, sessionlog.AppendOp{}, chunkSeqs)
	return err
}

// assembledMessage 把装配器攒出来的那些块定成一条署了来路的助手消息。
//
// 源: packages/core/agent-loop/src/agent.ts:392-399
func assembledMessage(request llm.GenerateOptions, assembler *llm.BlockAssembler) (llm.Message, error) {
	blocks, err := assembler.Blocks()
	if err != nil {
		return llm.Message{}, fmt.Errorf("harness/agentloop: 装配助手消息失败：%w", err)
	}
	provenance := llm.Provenance{Provider: request.Provider, Model: request.Model}
	envelope, hasReplay, err := assembler.ReplayState()
	if err != nil {
		return llm.Message{}, fmt.Errorf("harness/agentloop: 取重放状态失败：%w", err)
	}
	if hasReplay {
		// 新增: DSH 那边 replayState 是一个原样透传的 JS 值。Go 里
		// [llm.Provenance.ReplayState] 是一段 JSON 字节，所以这里排一次。
		raw, err := json.Marshal(envelope)
		if err != nil {
			return llm.Message{}, fmt.Errorf("harness/agentloop: 排重放状态失败：%w", err)
		}
		provenance.ReplayState = raw
	}
	return llm.NewAssistantMessage(blocks, provenance), nil
}

// maxParallelToolCalls 读出当下的并行池上限。
func (a *ReactLoopAgent) maxParallelToolCalls() int {
	if a.deps.MaxParallelToolCalls == nil {
		return DefaultMaxParallelToolCalls
	}
	return a.deps.MaxParallelToolCalls()
}

// acceptToolContext 收下一份已提交的工具结果捎回来的上下文，排到下一个步骤末尾。
//
// 源: packages/core/agent-loop/src/agent.ts:416
func (a *ReactLoopAgent) acceptToolContext(message llm.Message) {
	a.mutex.Lock()
	_, err := a.inbox.Splice(agent.NextStep, len(a.inbox.NextStep()), 0, []llm.Message{message})
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)
	if err != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 收下工具捎回来的上下文失败：%w", err))
	}
}

// requestProposal 把适配器解析出来的那几个值摘掉，再让插件提议下一次请求的配置。
//
// 源: packages/core/agent-loop/src/agent.ts:54-61
//
// 不摘的话，一个适配器按确切模型填的默认值会在下一次请求里冒充成调用方的选择，
// 于是换了模型也换不掉它。
//
// 新增: DSH 开头那句 `if (header.adapterDefaults === undefined) return header.config`
// 在这里不需要——[sessionlog.EpochHeader.AdapterDefaults] 是值不是指针，
// 「没有」就是两个标记都为假，下面两个 if 自然都不成立。
func requestProposal(header sessionlog.EpochHeader) llm.CallConfig {
	proposal := header.Config.Clone()
	if header.AdapterDefaults.ReasoningEffort {
		proposal.ReasoningEffort = ""
	}
	if header.AdapterDefaults.MaxTokens {
		proposal.MaxTokens = 0
	}
	return proposal
}

// buildRequest 装配出一次完整的模型请求，并把它绑在解算出它那份默认值的那次适配器登记上。
//
// 源: packages/core/agent-loop/src/agent.ts:422-514
//
// 第二个返回值为 nil 表示这条路由当下没有登记着的适配器——中间件仍然可能服务它。
func (a *ReactLoopAgent) buildRequest(
	ctx context.Context,
	turn, step int,
	toolSchemas []llm.ToolSchema,
	system string,
	boundaryMessages []llm.Message,
) (llm.GenerateOptions, *llm.PreparedCall, error) {
	var empty llm.GenerateOptions

	persisted, hasPersisted, err := a.session.RequestHeader()
	if err != nil {
		return empty, nil, fmt.Errorf("harness/agentloop: 读请求头失败：%w", err)
	}
	a.mutex.Lock()
	headerLogged := a.requestHeaderLogged
	a.mutex.Unlock()

	// 一个循环实例从它自己声明的那条路由起步，只把「确实属于这个确切模型、
	// 而且是人选的」那个推理档位恢复回来。后面的步骤重新解算被标记过的默认值。
	seed := llm.CallConfig{
		Provider:        a.options.Provider,
		Model:           a.options.Model,
		ReasoningEffort: a.options.ReasoningEffort,
		MaxTokens:       a.options.MaxTokens,
	}
	if headerLogged {
		seed = requestProposal(persisted)
	} else if seed.ReasoningEffort == "" &&
		hasPersisted &&
		persisted.Config.Provider == a.options.Provider &&
		persisted.Config.Model == a.options.Model &&
		!persisted.AdapterDefaults.ReasoningEffort {
		// 声明出来的档位压过持久化下来的那个，对应 DSH 那句
		// `this.options.reasoningEffort ?? persistedReasoningEffort`（agent.ts:466）：
		// 换了档位重挂的那个实例，要跑的是新档位，不是上次记下来的。
		seed.ReasoningEffort = persisted.Config.ReasoningEffort
	}

	proposed, err := a.deps.Agents.ResolveRequest(
		ctx,
		agent.Request{Agent: a, Turn: turn, Step: step},
		func(context.Context) (llm.CallConfig, error) { return seed.Clone(), nil },
	)
	if err != nil {
		return empty, nil, err
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}
	if proposed.Provider == "" || proposed.Model == "" {
		return empty, nil, fmt.Errorf(
			"harness/agentloop: agent %q 没有 provider/model：给 agent.Options 填上这两项，或者在 agent/request 那条瀑布上一起给出",
			string(a.id))
	}

	config := proposed
	prepared, err := a.deps.LLM.PrepareCall(ctx, proposed)
	if err != nil {
		// 中间件可以服务一条没登记的路由，别的失败照抛。
		var carrier *llm.Error
		if !errors.As(err, &carrier) || carrier.Failure.Code != llm.NoAdapterCode {
			return empty, nil, err
		}
		prepared = nil
	} else {
		config = prepared.Config()
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}

	header := sessionlog.EpochHeader{Config: config, System: system, Tools: toolSchemas}
	if prepared != nil {
		header.AdapterDefaults = prepared.AdapterDefaults()
	}
	header = sessionlog.CanonicalHeader(header)

	if err := a.foldRequestHeader(header, headerLogged); err != nil {
		return empty, nil, err
	}
	if err := a.foldRequestContext(config, prepared); err != nil {
		return empty, nil, err
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}

	return llm.GenerateOptions{
		Provider:        header.Config.Provider,
		Model:           header.Config.Model,
		ReasoningEffort: header.Config.ReasoningEffort,
		Messages:        boundaryMessages,
		System:          header.System,
		Tools:           header.Tools,
		Temperature:     header.Config.Temperature,
		MaxTokens:       header.Config.MaxTokens,
		Stop:            header.Config.Stop,
		SessionID:       llm.SessionID(a.session.ID()),
		AgentLoop:       true,
	}, prepared, nil
}

// foldRequestHeader 只在需要时往日志上添一份请求头快照。
//
// 源: packages/core/agent-loop/src/agent.ts:483-489
//
// 每个循环实例的第一次请求一定写一份锚点（initial 或者 resume），之后只有内容
// 真的变了才写（change）。不写锚点的话，一段恢复出来的日志读不出「这一程是从
// 哪份配置开始的」。
func (a *ReactLoopAgent) foldRequestHeader(header sessionlog.EpochHeader, headerLogged bool) error {
	baseline, hasBaseline, err := a.session.RequestHeader()
	if err != nil {
		return fmt.Errorf("harness/agentloop: 读请求头失败：%w", err)
	}
	if headerLogged {
		if hasBaseline && sessionlog.HeaderEquals(baseline, header) {
			return nil
		}
		_, err := a.appendEvent(
			sessionlog.RequestHeaderData{Header: header, Reason: sessionlog.HeaderChange}, nil, nil)
		return err
	}

	reason := sessionlog.HeaderInitial
	if hasBaseline {
		reason = sessionlog.HeaderResume
	}
	if _, err := a.appendEvent(sessionlog.RequestHeaderData{Header: header, Reason: reason}, nil, nil); err != nil {
		return err
	}
	a.mutex.Lock()
	a.requestHeaderLogged = true
	a.mutex.Unlock()
	return nil
}

// foldRequestContext 只在这条已解析路由的注册期元数据变了时记一条。
//
// 源: packages/core/agent-loop/src/agent.ts:491-502
//
// 新增: DSH 那边是 provider、model、contextWindow 三个字段一路 !== 比过来。
// [sessionlog.RequestContext] 在 Go 里是个可比较的结构体，所以直接比整体——
// 少一处「以后加了字段却忘了加进比较」的地方。
func (a *ReactLoopAgent) foldRequestContext(config llm.CallConfig, prepared *llm.PreparedCall) error {
	requestContext := sessionlog.RequestContext{Provider: config.Provider, Model: config.Model}
	if prepared != nil {
		if modelContext, ok := prepared.ModelContext(); ok {
			requestContext.ContextWindow = modelContext.ContextWindow
		}
	}
	previous, hasPrevious, err := a.session.RequestContext()
	if err != nil {
		return fmt.Errorf("harness/agentloop: 读请求元数据失败：%w", err)
	}
	if hasPrevious && previous == requestContext {
		return nil
	}
	_, err = a.appendEvent(sessionlog.RequestContextData{RequestContext: requestContext}, nil, nil)
	return err
}

// appendEvent 把一份负载排成字节、追加进日志，交出落定的那条事件。
//
// 新增: 本包每一处追加都长一个样（排负载、包错误、追加、再包错误），DSH 那边
// 由 session.append(type, data, options) 这一个方法承担。这里把它收成一个函数，
// 好让每一处调用点只剩下「写的是什么」。
func (a *ReactLoopAgent) appendEvent(
	data sessionlog.EventData,
	surfaceOp sessionlog.SurfaceOp,
	sourceEventSeqs []int,
) (sessionlog.Event, error) {
	eventType := data.EventType()
	payload, err := json.Marshal(data)
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("harness/agentloop: 排 %s 负载失败：%w", eventType, err)
	}
	event, err := a.session.Append(sessionlog.Event{
		Type:            eventType,
		Data:            payload,
		SurfaceOp:       surfaceOp,
		SourceEventSeqs: sourceEventSeqs,
	})
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("harness/agentloop: 追加 %s 失败：%w", eventType, err)
	}
	return event, nil
}
