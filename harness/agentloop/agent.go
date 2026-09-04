// 本文件的作用：这个 Agent 本身——它的依赖、当前处在哪一相、身份与几个只读入口，
// 以及造出来时那一串接线。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515

package agentloop

import (
	"context"
	"errors"
	"fmt"
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
