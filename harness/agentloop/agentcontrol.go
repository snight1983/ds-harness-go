// 本文件的作用：外面怎么支使这个 Agent——投message、改收件箱里那几条、取消、
// 跑一次维护，以及这些动作怎么把驱动线程叫醒、怎么等它闲下来。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

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
