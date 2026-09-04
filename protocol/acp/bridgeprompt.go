// 本文件的作用：一次提示词从准入到结算走的那条状态机——在飞的那一笔记什么、
// 取消和出错怎么收、以及回合停下来时报给对面的是哪个停止原因。
//
// 源: packages/acp/acp/src/index.ts

package acp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// inflightPrompt 是一次正在半路上的提示词：从准入、排队、跑完，到结算成一个停止原因。
//
// 源: packages/acp/acp/src/index.ts:93-113
//
// 除了 done / admissionDone 这两个 channel 和那两个 [sync.Once]，它每一个字段都由
// [Bridge.mutex] 罩着。
type inflightPrompt struct {
	// done 在这次提示词结算之后关掉；stopReason 与 err 在那之前写好。
	//
	// 新增: DSH 用一对 Promise.withResolvers。Go 里「一次性的、写完再让别人读」正是
	// 关一个 channel 的语义，[sync.Once] 保证只结算一次。
	done       chan struct{}
	settleOnce sync.Once
	stopReason wire.StopReason
	err        error

	// admissionDone 在准入这一段（含已经开跑的那次附件写入）落地之后关掉。
	admissionDone  chan struct{}
	admissionOnce  sync.Once
	abortAdmission context.CancelCauseFunc

	// messageID 只在富内容准入成功、消息造出来之后才有值。
	messageID llm.MessageID
	// messageQueued 记的是这条提示词进没进 agent 那条耐久收件箱的区间。
	messageQueued bool
	// turn / hasTurn 是这条消息被认领走的那个回合。
	turn    int
	hasTurn bool
	// endReason 是那个相关回合的结束理由，在 turn/end 记下、在整体空闲时结算。
	endReason sessionlog.TurnEndReason
	// cancelRequested 记的是有没有人要求取消这一次。
	cancelRequested bool
	// settlementStarted 保证结算那条协程只起一次。
	settlementStarted bool
	// outputError 是这次提示词那个回合名下的已提交输出转不动。
	outputError error
	// agentError 是相关回合之外、整个区间上的失败。
	agentError error
}

// finishAdmission 关掉准入那道闸，可以重复调用。
func (p *inflightPrompt) finishAdmission() {
	p.admissionOnce.Do(func() { close(p.admissionDone) })
}

// finish 结算这一次，第一个来的算数。
func (p *inflightPrompt) finish(reason wire.StopReason, err error) {
	p.settleOnce.Do(func() {
		p.stopReason = reason
		p.err = err
		close(p.done)
	})
}

// turnOwner 交出这个回合名下那次半路上的提示词，没有就是 nil。
//
// 源: packages/acp/acp/src/index.ts:233
//
// 只有当下这次提示词**那个**回合名下的输出，转不动时才算它的失败：别的回合是这个
// agent 上自主活动的事，和对面正在等的那次答复无关。
//
// 调用方必须攥着 [Bridge.mutex]。
func turnOwner(record *sessionRecord, turn int) *inflightPrompt {
	inflight := record.inflight
	if inflight != nil && inflight.hasTurn && inflight.turn == turn {
		return inflight
	}
	return nil
}

// settleAfterQuiescenceLocked 起那条结算协程，一次提示词只起一条。
//
// 源: packages/acp/acp/src/index.ts:169-216
//
// 调用方必须攥着 [Bridge.mutex]。
func (b *Bridge) settleAfterQuiescenceLocked(record *sessionRecord, inflight *inflightPrompt) {
	if inflight.settlementStarted {
		return
	}
	inflight.settlementStarted = true
	go b.settle(record, inflight)
}

// settle 等准入、agent 活动、有序的助手投递三样**全部**静下来，才结算这一次提示词。
//
// 源: packages/acp/acp/src/index.ts:175-215
//
// 那条尾巴要在等到空闲之后才读：session/event 是在 agent 变空闲**之前**同步排上去的，
// 所以这时候读到的"当下这条尾巴"已经包含了这一轮每一节已提交的输出。
func (b *Bridge) settle(record *sessionRecord, inflight *inflightPrompt) {
	<-inflight.admissionDone

	b.mutex.Lock()
	queued := inflight.messageQueued
	ctx := b.notifyCtx
	b.mutex.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}

	if queued {
		if err := record.agent.WhenIdle(ctx); err != nil {
			b.clearAndFinish(record, inflight, internalError(fmt.Sprintf("prompt settlement failed: %v", err)))
			return
		}
		b.mutex.Lock()
		tail := record.outputTail
		b.mutex.Unlock()
		<-tail
	}

	b.mutex.Lock()
	if record.inflight != inflight {
		// 这个位子已经被这次确切的结算之外的什么东西清掉了。
		b.mutex.Unlock()
		return
	}
	record.inflight = nil
	cancelRequested := inflight.cancelRequested
	outputError, agentError, end := inflight.outputError, inflight.agentError, inflight.endReason
	b.mutex.Unlock()

	switch {
	case cancelRequested:
		inflight.finish(wire.StopReasonCancelled, nil)
	case outputError != nil:
		inflight.finish("", internalError(fmt.Sprintf("assistant output delivery failed: %v", outputError)))
	case agentError != nil:
		inflight.finish("", internalError(fmt.Sprintf("turn failed: %v", agentError)))
	case end == nil:
		// 这条提示词的回合从来没关过：这一端说不出它是怎么结束的，只能报取消。
		inflight.finish(wire.StopReasonCancelled, nil)
	default:
		inflight.finish(promptStopReason(end))
	}
}

// promptStopReason 把一个回合结束理由折成**提示词这一层**的结局。
//
// 源: packages/acp/acp/src/index.ts:201-207
//
// 和 [TurnEndToStopReason] 差在一处：撞上输出上限在回合那一层是 `max_tokens`，在提示词
// 这一层不是——它和别的非终结性结束一样，只是一次寻常的静默，报 `end_turn`。报 error 的
// 那种在这里变成一个线上错误，因为那一轮根本没有结局可言。
func promptStopReason(end sessionlog.TurnEndReason) (wire.StopReason, error) {
	if failure, isError := end.(sessionlog.ErrorTurnEnd); isError {
		return "", internalError(fmt.Sprintf("turn failed: %s", failure.Error.Message))
	}
	if end.TurnEndReasonKind() == sessionlog.ReasonMaxTokens {
		return wire.StopReasonEndTurn, nil
	}
	return TurnEndToStopReason(end), nil
}

// clearAndFinish 清掉这次提示词占的位子并把它结算成一个失败。
//
// 源: packages/acp/acp/src/index.ts:210-214
func (b *Bridge) clearAndFinish(record *sessionRecord, inflight *inflightPrompt, err error) {
	b.mutex.Lock()
	if record.inflight != inflight {
		b.mutex.Unlock()
		return
	}
	record.inflight = nil
	b.mutex.Unlock()
	inflight.finish("", err)
}

// Prompt 把一轮用户输入准入、排进这个会话，然后一直等到它结算出一个停止原因。
//
// 源: packages/acp/acp/src/index.ts:335-423
//
// 那个"一次一条"的位子在**第一次异步操作之前**就先占上，好让并发的提示词和一次取消都
// 看得见「准入真的在半路上」。
func (b *Bridge) Prompt(ctx context.Context, params wire.PromptRequest) (wire.PromptResponse, error) {
	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return wire.PromptResponse{}, internalError("the ACP bridge has been disposed")
	}
	record, known := b.sessions[sessionlog.SessionID(params.SessionId)]
	if !known {
		b.mutex.Unlock()
		return wire.PromptResponse{}, invalidParams(fmt.Sprintf("unknown session: %s", params.SessionId))
	}
	if record.closing {
		b.mutex.Unlock()
		return wire.PromptResponse{}, invalidParams(fmt.Sprintf("session is closing: %s", params.SessionId))
	}
	if record.inflight != nil {
		b.mutex.Unlock()
		return wire.PromptResponse{}, invalidParams("a prompt is already in flight for this session")
	}
	// 新增: DSH 那个 AbortController 是独立的。这里从请求那个 ctx 派生，于是连接一断，
	// 半路上的准入跟着停——这是 Go 这一侧白得的一条，DSH 的 signal 接不上它的传输层。
	admissionCtx, abortAdmission := context.WithCancelCause(ctx)
	inflight := &inflightPrompt{
		done:           make(chan struct{}),
		admissionDone:  make(chan struct{}),
		abortAdmission: abortAdmission,
	}
	record.inflight = inflight
	target, imageEnabled := record.agent, b.imagePromptEnabled
	// 这份抓拍取在准入**之前**：它是这条提示词自己那份路由。准入这一段可能跑很久（富
	// 内容要落成耐久附件），那期间对面的一次改模型该算下一条提示词的。
	var selection agent.ModelSelection
	hasSelection := false
	if record.control != nil {
		selection, hasSelection = record.control.Snapshot()
	}
	b.mutex.Unlock()

	admissionFailure := b.admit(admissionCtx, record, target, params.Prompt, imageEnabled, inflight, selection, hasSelection)
	inflight.finishAdmission()
	abortAdmission(nil)

	b.mutex.Lock()
	cancelRequested := inflight.cancelRequested
	if !cancelRequested && admissionFailure != nil {
		record.inflight = nil
		b.mutex.Unlock()
		return wire.PromptResponse{}, admissionFailure
	}
	b.settleAfterQuiescenceLocked(record, inflight)
	b.mutex.Unlock()

	// 新增: DSH 在这里 `await completion.promise`，没有别的出口。Go 这边照样只等结算：
	// 一条提示词的结局由结算那条协程说了算，请求这一侧提前跑掉只会让对面收到一个和
	// 运行时里真实发生的事对不上的答复。
	<-inflight.done
	if inflight.err != nil {
		return wire.PromptResponse{}, inflight.err
	}
	return wire.PromptResponse{StopReason: inflight.stopReason}, nil
}

// admit 验这轮输入、把富内容落成耐久附件、造出消息并排进 agent 的收件箱。
//
// 源: packages/acp/acp/src/index.ts:366-401
//
// 投递之前和之后各查一次注册表：不给一个已经退休的目的地写富内容，而 agent 循环的一次
// 重载可能和存储写入赛跑。
func (b *Bridge) admit(
	ctx context.Context,
	record *sessionRecord,
	target agent.Agent,
	prompt []wire.ContentBlock,
	imageEnabled bool,
	inflight *inflightPrompt,
	selection agent.ModelSelection,
	hasSelection bool,
) error {
	if !b.isLive(target) {
		return internalError("prompt was not queued: the agent was disposed outside the bridge")
	}
	content, err := AdmitPrompt(ctx, b.config.Attachments, b.config.Models, target, prompt, imageEnabled)
	if err != nil {
		return mapAdmissionError(err)
	}
	if err := ctx.Err(); err != nil {
		return mapAdmissionError(err)
	}
	if !b.isLive(target) {
		return internalError("prompt was not queued: the agent was disposed outside the bridge")
	}

	message := llm.NewUserMessage(content, llm.UserSource{})
	b.mutex.Lock()
	inflight.messageID = message.ID
	inflight.messageQueued = true
	// 这条消息的路由记在会话上而不是这次提示词上：认领它的是收件箱，而收件箱认领哪
	// 一条不归这次请求管——它可能在这次请求早就返回之后才被取走。
	if hasSelection && record.control != nil {
		record.pendingSelections[message.ID] = selection
	}
	b.mutex.Unlock()

	// 新增: DSH 那句注释说「这最后一次中止检查和 followup 之间不许夹任何 await」——它
	// 靠 JS 的单线程把这两步做成原子的。Go 里做不到：一次并发的 session/cancel 真的会挤
	// 进来，而它看见 messageQueued 还是假就不会去动这个 agent，于是这条迟到的消息没人
	// 清。所以这里换一条等价的路——投递之后再读一次那面旗，是就自己补一次取消。
	// [agent.Agent.Cancel] 在没有活动在跑时是个空操作，所以重复一次不会有别的后果。
	target.Followup(message)
	b.mutex.Lock()
	lateCancel := inflight.cancelRequested
	b.mutex.Unlock()
	if lateCancel {
		target.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
	}
	return nil
}

// isLive 问注册表「这条记录指着的还是不是活着的那一个 agent」。
//
// 源: packages/acp/acp/src/index.ts:369, 382
func (b *Bridge) isLive(target agent.Agent) bool {
	live, ok := b.config.Agents.Get(target.ID())
	return ok && live == target
}

// mapAdmissionError 把一次准入失败折成线上那两个错误码里的一个。
//
// 源: packages/acp/acp/src/index.ts:409-417
func mapAdmissionError(err error) error {
	var content *ContentError
	if errors.As(err, &content) {
		if content.Kind == ContentInvalid {
			return invalidParams(content.Message)
		}
		return internalError(content.Message)
	}
	var request *wire.RequestError
	if errors.As(err, &request) {
		return request
	}
	return internalError(fmt.Sprintf("prompt was not queued: %v", err))
}

// Cancel 停掉一个会话上正在跑的活儿。
//
// 源: packages/acp/acp/src/index.ts:425-439
//
// 准入**不是** agent 的活儿：这条提示词进耐久收件箱之前，这个 agent 上别的生产者照常
// 活着；而在根本没有提示词的时候，取消照旧打向这个 agent 上那些自主的活动。
func (b *Bridge) Cancel(_ context.Context, params wire.CancelNotification) error {
	b.mutex.Lock()
	record, known := b.sessions[sessionlog.SessionID(params.SessionId)]
	if !known {
		b.mutex.Unlock()
		return nil
	}
	inflight := record.inflight
	if inflight != nil {
		inflight.cancelRequested = true
		inflight.abortAdmission(errPromptCancelled)
		b.settleAfterQuiescenceLocked(record, inflight)
	}
	target := record.agent
	cancelAgent := inflight == nil || inflight.messageQueued
	b.mutex.Unlock()

	if cancelAgent {
		target.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
	}
	return nil
}
