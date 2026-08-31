// 本文件的作用：这座桥本身——它攥着哪些会话、五个协议方法各自怎么办、运行时那三条
// 边怎么翻成线上的话、一次提示词从准入到结算走的那条状态机、以及收摊的次序。
//
// 源: packages/acp/acp/src/index.ts

package acp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/google/uuid"

	wire "github.com/coder/acp-go-sdk"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// errPromptCancelled 与 errBridgeDisposed 是中止一次准入的两个原因。
//
// 源: packages/acp/acp/src/index.ts:431, 463
//
// 它们只当 [context.CancelCauseFunc] 的理由用，不上线：准入被中止之后走的是取消那条
// 结算路，报出去的是 `cancelled`，不是这两句话。
var (
	errPromptCancelled = errors.New("acp: 这次 ACP 提示词被取消了")
	errBridgeDisposed  = errors.New("acp: 这座 ACP 桥正在收摊")
)

// invalidParams 造一个「客户端给错了东西」的线上错误。
//
// 源: packages/acp/acp/src/index.ts:61-63
//
// 新增: DSH 把那句细节塞进线上错误的**消息**里。Go 这边用 SDK 自己那个构造函数，
// 细节落在 `data` 上——JSON-RPC 本来就把自由格式的细节放在那里，而
// [github.com/coder/acp-go-sdk.RequestError.Error] 渲染的是整个信封（code、message、
// data 一并），所以对面和日志两侧都看得见它。
func invalidParams(detail string) *wire.RequestError {
	return wire.NewInvalidParams(detail)
}

// internalError 造一个「这一端没办成」的线上错误，理由同 [invalidParams]。
//
// 源: packages/acp/acp/src/index.ts:66-68
func internalError(detail string) *wire.RequestError {
	return wire.NewInternalError(detail)
}

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

// sessionRecord 是一个会话在协议这一层的状态。
//
// 源: packages/acp/acp/src/index.ts:86-114
type sessionRecord struct {
	// agent 是这条会话背后那个活 agent。
	agent agent.Agent
	// dispose 拆掉它，一次性。
	dispose func(context.Context) error

	// outputTail 是助手输出那条有序投递链的**当下**这一节：它在这一节送完之后关掉。
	//
	// 新增: DSH 用 `outputTail: Promise<void>`，每来一条消息就 `.then` 接一节上去。
	// Go 里没有 promise，这里用同一个形状的 channel 链：每一节自己起一个协程，先等上
	// 一节关掉再干活，干完关掉自己那个。于是「等当下这条尾巴」就是 `<-record.outputTail`，
	// 而"当下"这两个字要在锁里读——读晚一点会多等几节，读早了会漏掉已经排上的那几节。
	outputTail chan struct{}

	// inflight 是那一次半路上的提示词，nil 表示这条会话此刻空着。
	inflight *inflightPrompt
}

// Bridge 是架在一套跑着的运行时和一条 ACP 连接之间的那座桥。
//
// 源: packages/acp/acp/src/index.ts:121-129
//
// 它同时是 [github.com/coder/acp-go-sdk.Agent] 的实现（那 11 个方法见本文件下半段）。
// [Install] 之后它开始收运行时那三条边并且往对面转发；[Quiesce] 之后它把自己建出来的
// agent、那几条订阅全部拆掉，**一次**——之后再来的请求都被拒。
type Bridge struct {
	config Config

	// mutex 罩着下面这一整块可变状态，也罩着每一条 [sessionRecord] 和
	// [inflightPrompt] 上那些可变字段。
	//
	// 那条连接的请求处理器是并发的，所以两次 `session/prompt` 真的会同时进来，
	// 一次 `session/cancel` 也真的会和一次准入交错——DSH 靠 JS 的单线程免掉了这件事。
	mutex sync.Mutex
	// peer 是 [Install] 交进来的那条通道。
	peer Peer
	// owner 是 [Install] 交进来的那个作用域，这座桥建出来的 agent 挂在它上面。
	owner *scope.Scope
	// notifyCtx 是发通知用的上下文，见 [Bridge.Install] 上那条说明。
	notifyCtx context.Context
	// closed 一置起来就不再接新会话、也不再接新提示词。
	closed bool
	// imagePromptEnabled 是握手那一刻算出来的那句声明，之后每一次准入都照它判。
	imagePromptEnabled bool
	// sessions 是这座桥自己建出来的那些会话。
	sessions map[sessionlog.SessionID]*sessionRecord
	// disposers 是那几条订阅的撤销函数，按装上的次序排。
	disposers []func(context.Context) error

	// quiesceOnce / quiesceErr 让收摊只真的跑一次，之后每次都交回同一个结论。
	//
	// 源: packages/acp/acp/src/index.ts:451-452（`quiescing ??=`）
	quiesceOnce sync.Once
	quiesceErr  error
}

// 编译期确认这座桥就是 SDK 要的那个 agent 实现。
var _ wire.Agent = (*Bridge)(nil)

// Install 把这座桥接到运行时和那条连接上，并交回收摊的那个函数。
//
// 源: packages/acp/acp/src/index.ts:222-285, 523
//
// owner 是这座桥建出来的 agent 和那几条订阅共同的主人。交一个 [scope.NewRoot] 造的
// 作用域进来，那几条订阅落全局层，看得见整套运行时里的每一个会话——这正是 DSH 的行为。
//
// peer 就是装配方拿这座桥造出来的那条连接（见包文档"装配的次序"那一节）。
//
// 新增: ctx 被记下来当发通知用的上下文。运行时那几条边的观察者本身不带
// [context.Context]（会话追加和收件箱认领两条是同步回调），而 [Peer] 那两个方法要一个。
// 记下来的这一个管的是「这座桥还在不在岗」，不是任何一次转发的时限。
//
// 交回的函数就是 [Bridge.Quiesce]：DSH 那个 `ctx.effect(() => quiesce, 'acp.connection')`
// 挂上去的正是它。
func (b *Bridge) Install(ctx context.Context, owner *scope.Scope, peer Peer) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, fmt.Errorf("acp: 装一座 ACP 桥需要一个作用域")
	case peer == nil:
		return nil, fmt.Errorf("acp: 装一座 ACP 桥需要一条往对面发东西的通道")
	}
	b.mutex.Lock()
	if b.owner != nil {
		b.mutex.Unlock()
		return nil, fmt.Errorf("acp: 这座 ACP 桥已经装上了")
	}
	b.owner, b.peer, b.notifyCtx = owner, peer, ctx
	b.mutex.Unlock()

	if err := b.subscribe(ctx, owner); err != nil {
		// 装到一半就失败：已经挂上的那几条当场摘掉，别留下一条会往一座永远不会开工的
		// 桥上转发的边。
		_ = b.releaseSubscriptions(ctx)
		b.mutex.Lock()
		b.owner, b.peer, b.notifyCtx = nil, nil, nil
		b.mutex.Unlock()
		return nil, err
	}
	return b.Quiesce, nil
}

// subscribe 按 DSH 那个 apply 的次序挂上那几条订阅，逐条记进 disposers。
//
// 源: packages/acp/acp/src/index.ts:222, 254, 260, 271
//
// 审批那一条只在挂了审批服务时才装：没挂就是这条线上没人拍板，这座桥也就不参与。
func (b *Bridge) subscribe(ctx context.Context, owner *scope.Scope) error {
	steps := []func() (func(context.Context) error, error){
		func() (func(context.Context) error, error) {
			return b.config.Sessions.OnEvent(ctx, owner, b.onSessionEvent)
		},
		func() (func(context.Context) error, error) {
			return b.config.Agents.OnInboxClaimed(ctx, owner, b.onInboxClaimed)
		},
		func() (func(context.Context) error, error) {
			return b.config.Agents.OnError(ctx, owner, b.onAgentError)
		},
	}
	if b.config.Approvals != nil {
		steps = append(steps, func() (func(context.Context) error, error) {
			return b.config.Approvals.RegisterAnswerer(ctx, owner, b.answerApproval)
		})
	}
	for _, step := range steps {
		dispose, err := step()
		if err != nil {
			return fmt.Errorf("acp: 挂订阅失败：%w", err)
		}
		b.mutex.Lock()
		b.disposers = append(b.disposers, dispose)
		b.mutex.Unlock()
	}
	return nil
}

// releaseSubscriptions 按装上的**反**序摘掉那几条订阅。
func (b *Bridge) releaseSubscriptions(ctx context.Context) error {
	b.mutex.Lock()
	disposers := b.disposers
	b.disposers = nil
	b.mutex.Unlock()

	var failures []error
	for index := len(disposers) - 1; index >= 0; index-- {
		if err := disposers[index](ctx); err != nil {
			failures = append(failures, fmt.Errorf("acp: 摘订阅失败：%w", err))
		}
	}
	return errors.Join(failures...)
}

// ownedRecordLocked 交出一个 agent 在这座桥名下的那条记录，同 id 的冒名者一律不认。
//
// 源: packages/acp/acp/src/index.ts:132-135
//
// 调用方必须攥着 [Bridge.mutex]。
func (b *Bridge) ownedRecordLocked(live agent.Agent) *sessionRecord {
	record, ok := b.sessions[live.ID()]
	if !ok || record.agent != live {
		return nil
	}
	return record
}

// notify 把一条会话更新发出去，发不出去只记一行。
//
// 源: packages/acp/acp/src/index.ts:148-156
//
// 通知本来就是单向的：一条发失败的更新改变不了运行时里已经发生的那件事，更不该把发出
// 这条边的那次追加带崩。
func (b *Bridge) notify(ctx context.Context, notification wire.SessionNotification) {
	b.mutex.Lock()
	peer := b.peer
	b.mutex.Unlock()
	if peer == nil {
		// 还没装上（或已经拆干净了）：这条线上没有对面可发。
		return
	}
	if err := peer.SessionUpdate(ctx, notification); err != nil {
		b.config.warn(fmt.Sprintf("acp: session/update 发不出去：%v", err))
	}
}

// onSessionEvent 只往线上发**已提交**的助手文本和图，顺便记下相关回合的结束理由。
//
// 源: packages/acp/acp/src/index.ts:222-252
//
// 原始流片段、推理、工具、计划、标题、重试标记一条都不发：那些是呈现和轨迹数据，不
// 属于这条自动化线。每条会话一条投递链，好让块与消息的先后次序扛得住中途那些异步的
// 附件读取。
//
// 新增: DSH 那个 `finally` 是为了「助手那一支抛了也照样记结束理由」。Go 这边两种事件
// 类型互斥，一条事件不可能既是 assistant/message 又是 turn/end，所以那层 finally 收敛
// 成了并列的两个分支。
//
// 新增: 下面那两处类型断言测不到，这是本包唯一两条没被覆盖的语句（其余 99.6% 全覆盖）。
// [sessionlog.DecodeData] 按事件类型派发，`assistant/message` 解出来的**只可能**是
// [sessionlog.AssistantMessageData]，`turn/end` 同理——要走到那两个 `return` 上，得先把
// 事件类型和负载类型的对应关系改错。那正是留着它们的理由：DSH 那边是 TS 的判别联合，
// 编译器替它挡住了这件事；Go 这边 `any` 挡不住，而一次静默的错配会让这条线**无声地**
// 少发一条助手消息、或者少记一次回合结束。宁可多这两行。
func (b *Bridge) onSessionEvent(sess *coresession.Session, event sessionlog.Event) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record, ok := b.sessions[sess.ID()]
	if !ok || record.agent.Session() != sess {
		return
	}
	switch event.Type {
	case sessionlog.EventAssistantMessage:
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			b.config.warn(fmt.Sprintf("acp: assistant/message 解不开：%v", err))
			return
		}
		message, typed := data.(sessionlog.AssistantMessageData)
		if !typed {
			return
		}
		b.scheduleDeliveryLocked(record, message)
	case sessionlog.EventTurnEnd:
		data, err := sessionlog.DecodeData(event)
		if err != nil {
			b.config.warn(fmt.Sprintf("acp: turn/end 解不开：%v", err))
			return
		}
		end, typed := data.(sessionlog.TurnEndData)
		if !typed {
			return
		}
		inflight := record.inflight
		if inflight != nil && inflight.hasTurn && inflight.turn == end.Turn {
			inflight.endReason = end.Reason
		}
	}
}

// scheduleDeliveryLocked 把一条已提交的助手消息接到这条会话那条投递链的尾巴上。
//
// 源: packages/acp/acp/src/index.ts:227-244
//
// 调用方必须攥着 [Bridge.mutex]。接上去这一步在锁里做完，所以两条同时到的消息拿到的
// 是两个前后相接的位置，谁也插不到谁前面去。
func (b *Bridge) scheduleDeliveryLocked(record *sessionRecord, data sessionlog.AssistantMessageData) {
	// 只有当下这次提示词**那个**回合名下的输出，转不动时才算它的失败。
	var owner *inflightPrompt
	if inflight := record.inflight; inflight != nil && inflight.hasTurn && inflight.turn == data.Turn {
		owner = inflight
	}
	previous := record.outputTail
	next := make(chan struct{})
	record.outputTail = next

	sessionID := record.agent.ID()
	content := data.Message.Content
	go func() {
		defer close(next)
		<-previous
		b.deliver(sessionID, content, owner)
	}()
}

// deliver 把一条助手消息的每一个块翻成线上内容并按序发出去。
//
// 源: packages/acp/acp/src/index.ts:230-244
//
// 一个块转不动就整条消息到此为止（DSH 那个 promise 一 reject，for 循环就出来了），失败
// 记在那次提示词名下并写一行日志——它改变不了已经发出去的那几块。
func (b *Bridge) deliver(sessionID sessionlog.SessionID, content llm.Content, owner *inflightPrompt) {
	b.mutex.Lock()
	ctx := b.notifyCtx
	b.mutex.Unlock()
	if ctx == nil {
		return
	}
	for _, block := range content {
		converted, onWire, err := AssistantBlockToACP(ctx, b.config.Attachments, block)
		if err != nil {
			b.mutex.Lock()
			if owner != nil && owner.outputError == nil {
				owner.outputError = err
			}
			b.mutex.Unlock()
			b.config.warn(fmt.Sprintf("acp: 助手输出转不动：%v", err))
			return
		}
		if !onWire {
			continue
		}
		b.notify(ctx, wire.SessionNotification{
			SessionId: wire.SessionId(sessionID),
			Update: wire.SessionUpdate{
				AgentMessageChunk: &wire.SessionUpdateAgentMessageChunk{Content: converted},
			},
		})
	}
}

// onInboxClaimed 把这次提示词那条消息落到的那个回合号记下来。
//
// 源: packages/acp/acp/src/index.ts:254-258
//
// 之后的结束理由、输出失败归属、以及"区间内还是区间外的错误"三件事，全靠这个回合号
// 认人。
func (b *Bridge) onInboxClaimed(live agent.Agent, message llm.Message, turn int) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record := b.ownedRecordLocked(live)
	if record == nil || record.inflight == nil || record.inflight.messageID != message.ID {
		return
	}
	record.inflight.turn = turn
	record.inflight.hasTurn = true
}

// onAgentError 认领一次落在**相关回合之外**的区间失败，并当场开始结算。
//
// 源: packages/acp/acp/src/index.ts:260-266
//
// 相关回合**之内**的失败不走这里：那种失败会在 turn/end 上留下一个 error 理由，由结算
// 那一步读出来。还没进耐久收件箱的提示词也不算——那时候这个 agent 上跑的一切都和它
// 无关。
func (b *Bridge) onAgentError(failure agent.TurnError) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record := b.ownedRecordLocked(failure.Agent)
	if record == nil {
		return
	}
	inflight := record.inflight
	if inflight == nil || !inflight.messageQueued {
		return
	}
	if inflight.hasTurn && inflight.turn == failure.Turn {
		return
	}
	inflight.agentError = failure.Err
	b.settleAfterQuiescenceLocked(record, inflight)
}

// answerApproval 把一次要人拍板的问话转给对面，只给一次性的两档。
//
// 源: packages/acp/acp/src/index.ts:271-285
//
// 这条线是给 ACP 客户端走的**机器策略**通道。它绝不从一个认不出的回答里推出一份耐久的
// 授权：不是 allow-once 就是拒。
//
// 新增: DSH 用 `request.agent` 直接查那张会话表——它那边 agent 就是键。Go 里
// [ds-harness-go/core/tools.ApprovalRequest.Agent] 是一把 [scope.Key]，所以这里按那把钥匙
// 扫一遍自己名下的会话。这张表就是这条连接开出来的那些会话，量级是个位数。
func (b *Bridge) answerApproval(
	ctx context.Context,
	request tools.ApprovalRequest,
	next func() (tools.ApprovalOutcome, error),
) (tools.ApprovalOutcome, error) {
	b.mutex.Lock()
	var record *sessionRecord
	for _, candidate := range b.sessions {
		if candidate.agent.Scope().Key() == request.Agent {
			record = candidate
			break
		}
	}
	peer := b.peer
	b.mutex.Unlock()

	if record == nil || peer == nil || request.CallID == "" {
		return next()
	}
	response, err := peer.RequestPermission(ctx, wire.RequestPermissionRequest{
		SessionId: wire.SessionId(record.agent.ID()),
		ToolCall:  wire.ToolCallUpdate{ToolCallId: wire.ToolCallId(request.CallID)},
		Options: []wire.PermissionOption{
			{OptionId: optionAllowOnce, Name: "Allow once", Kind: wire.PermissionOptionKindAllowOnce},
			{OptionId: optionRejectOnce, Name: "Reject", Kind: wire.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		return "", fmt.Errorf("acp: 问对面要许可失败：%w", err)
	}
	switch {
	case response.Outcome.Cancelled != nil:
		return tools.ApprovalCancelled, nil
	case response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == optionAllowOnce:
		return tools.ApprovalAllowedOnce, nil
	default:
		return tools.ApprovalRejected, nil
	}
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

// Initialize 握手：算出这条线能不能**如实**声明支持内联图，并交回这一端的身份。
//
// 源: packages/acp/acp/src/index.ts:290-302
//
// 单版本服务端：规范说的"支持就回同一个版本，否则回自己支持的最新版本"，在这里是同
// 一个答案。
func (b *Bridge) Initialize(ctx context.Context, _ wire.InitializeRequest) (wire.InitializeResponse, error) {
	enabled := SupportsImagePrompts(ctx, b.config.Attachments, b.config.Models, b.config.Provider, b.config.Model)
	b.mutex.Lock()
	b.imagePromptEnabled = enabled
	b.mutex.Unlock()
	return wire.InitializeResponse{
		ProtocolVersion: wire.ProtocolVersionNumber,
		AgentInfo:       &wire.Implementation{Name: AgentName, Version: AgentVersion},
		AgentCapabilities: wire.AgentCapabilities{
			PromptCapabilities: wire.PromptCapabilities{Image: enabled, Audio: false, EmbeddedContext: false},
		},
		AuthMethods: []wire.AuthMethod{},
	}, nil
}

// Authenticate 什么都不做。
//
// 源: packages/acp/acp/src/index.ts:304-306
//
// 这条线上的客户端是**受信任的**：认证由承载它的那条通道负责，不由协议这一层。
func (b *Bridge) Authenticate(context.Context, wire.AuthenticateRequest) (wire.AuthenticateResponse, error) {
	return wire.AuthenticateResponse{}, nil
}

// NewSession 按这条线共用的那份路由开一个全新的会话。
//
// 源: packages/acp/acp/src/index.ts:308-333
//
// 不组预设：ACP 这一束把面向模型的那几行留在宿主平面上，所以这个 agent 从全局层读它们。
// 要配名册的部署得先在这里接上一份（DSH agent-presets 的 README "Composing a child
// agent" 那一节）。
func (b *Bridge) NewSession(ctx context.Context, params wire.NewSessionRequest) (wire.NewSessionResponse, error) {
	b.mutex.Lock()
	closed, owner := b.closed, b.owner
	provider, model := b.config.Provider, b.config.Model
	b.mutex.Unlock()

	if closed {
		return wire.NewSessionResponse{}, internalError("the ACP bridge has been disposed")
	}
	if owner == nil {
		return wire.NewSessionResponse{}, internalError("the ACP bridge has not been installed")
	}
	if err := validateSessionParams(params); err != nil {
		return wire.NewSessionResponse{}, err
	}

	sessionID := sessionlog.SessionID(uuid.NewString())
	handle, err := b.config.Agents.Create(ctx, owner, agent.CreateOptions{
		SessionID:    sessionID,
		Cwd:          params.Cwd,
		AgentOptions: agent.Options{Provider: provider, Model: model},
	})
	if err != nil {
		return wire.NewSessionResponse{}, internalError(fmt.Sprintf("session/new failed: %v", err))
	}

	b.mutex.Lock()
	if b.closed {
		// 一次真的连接关闭挤在了创建的半路上：这一个再记进表里就没人拆得掉它。
		b.mutex.Unlock()
		if disposeErr := handle.Dispose(ctx); disposeErr != nil {
			b.config.warn(fmt.Sprintf("acp: 收摊途中建出来的会话 %s 拆不掉：%v", sessionID, disposeErr))
		}
		return wire.NewSessionResponse{}, internalError("connection closed during session/new")
	}
	tail := make(chan struct{})
	close(tail)
	b.sessions[sessionID] = &sessionRecord{
		agent:      handle.Agent,
		dispose:    handle.Dispose,
		outputTail: tail,
	}
	b.mutex.Unlock()
	return wire.NewSessionResponse{SessionId: wire.SessionId(sessionID)}, nil
}

// validateSessionParams 拒掉这条自动化契约之外的那几样会话特性。
//
// 源: packages/acp/acp/src/index.ts:538-545
func validateSessionParams(params wire.NewSessionRequest) error {
	switch {
	case !filepath.IsAbs(params.Cwd):
		return invalidParams(fmt.Sprintf("cwd must be an absolute path: %s", params.Cwd))
	case len(params.AdditionalDirectories) > 0:
		return invalidParams("additionalDirectories is not supported")
	case len(params.McpServers) > 0:
		return invalidParams("mcpServers is not supported")
	}
	return nil
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
	b.mutex.Unlock()

	admissionFailure := b.admit(admissionCtx, target, params.Prompt, imageEnabled, inflight)
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
	target agent.Agent,
	prompt []wire.ContentBlock,
	imageEnabled bool,
	inflight *inflightPrompt,
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

// 下面这六个方法这一端不办。
//
// 新增: DSH 那个 agent 对象只实现五个方法，TS 的 SDK 对没装的方法自己回 -32601。Go 的
// [github.com/coder/acp-go-sdk.Agent] 是一个 11 方法的接口，一个都不能少，所以这六个在
// 这里显式交回 [github.com/coder/acp-go-sdk.NewMethodNotFound]——线上仍然是 -32601，和
// DSH 逐字相同。
//
// 它们每一个都由一项这座桥**从不声明**的能力把着（session 的 close / list / resume、
// 会话配置项、会话模式、以及登出），所以一个守规矩的客户端根本不会来问。

// Logout 不办：这条线不做认证，见 [Bridge.Authenticate]。
func (b *Bridge) Logout(context.Context, wire.LogoutRequest) (wire.LogoutResponse, error) {
	return wire.LogoutResponse{}, wire.NewMethodNotFound("session/logout")
}

// CloseSession 不办：这座桥不声明 sessionCapabilities.close。
func (b *Bridge) CloseSession(context.Context, wire.CloseSessionRequest) (wire.CloseSessionResponse, error) {
	return wire.CloseSessionResponse{}, wire.NewMethodNotFound("session/close")
}

// ListSessions 不办：这座桥不声明 sessionCapabilities.list。
func (b *Bridge) ListSessions(context.Context, wire.ListSessionsRequest) (wire.ListSessionsResponse, error) {
	return wire.ListSessionsResponse{}, wire.NewMethodNotFound("session/list")
}

// ResumeSession 不办：这座桥不声明 sessionCapabilities.resume。
func (b *Bridge) ResumeSession(context.Context, wire.ResumeSessionRequest) (wire.ResumeSessionResponse, error) {
	return wire.ResumeSessionResponse{}, wire.NewMethodNotFound("session/resume")
}

// SetSessionConfigOption 不办：这座桥一个会话配置项都不摆出来。
func (b *Bridge) SetSessionConfigOption(context.Context, wire.SetSessionConfigOptionRequest) (wire.SetSessionConfigOptionResponse, error) {
	return wire.SetSessionConfigOptionResponse{}, wire.NewMethodNotFound("session/set_config_option")
}

// SetSessionMode 不办：这座桥一个会话模式都不摆出来。
func (b *Bridge) SetSessionMode(context.Context, wire.SetSessionModeRequest) (wire.SetSessionModeResponse, error) {
	return wire.SetSessionModeResponse{}, wire.NewMethodNotFound("session/set_mode")
}

// Quiesce 把这座桥建出来的东西全部收掉，**只真的跑一次**。
//
// 源: packages/acp/acp/src/index.ts:451-510
//
// 运行时照常活着：拆掉的只有这座桥自己建的 agent 和它挂的那几条订阅。
func (b *Bridge) Quiesce(ctx context.Context) error {
	b.quiesceOnce.Do(func() { b.quiesceErr = b.performQuiesce(ctx) })
	return b.quiesceErr
}

// performQuiesce 是收摊真正做的那几步，次序是定死的。
//
// 源: packages/acp/acp/src/index.ts:453-508
//
// 先把这座桥自己的活儿停掉，**在任何一次等待之前**：一次后代排干可能卡在持久化或者
// 带作用域的清理上，顶层那些 agent 不能在那整段时间里继续跑模型和工具。
//
// 然后守住和一次寻常提示词一样的边界：一次已经在写的富准入必须先停下来，每一节已提交
// 输出的转换必须在附件服务还在的时候排干。session/event 是在空闲**之前**同步排上去的。
//
// 摘订阅排在输出排干之后：这座桥名下最后一节输出得先送出去。可续的子 agent 活得比开出
// 它们的那个回合久，它们的活化自己拥有后代的拆解，所以在拆掉顶层 agent **之前**孩子
// 优先地排干这几棵树——不然会留下一个攥着已经被主人放掉的运行时的后代，而共用这套运行
// 时的另一个前端还活着。
//
// 新增: DSH 用 Promise.allSettled 并发拆并聚成一个 AggregateError。Go 这边顺着来一遍，
// 失败用 [errors.Join] 攒着，理由和 [ds-harness-go/sdk/sdkserver] 那条逐字相同：拆解本来
// 就是 I/O 少、次序敏感的收尾，并发省不下什么，却让"谁先拆完"变成不确定的。
func (b *Bridge) performQuiesce(ctx context.Context) error {
	b.mutex.Lock()
	b.closed = true
	records := make([]*sessionRecord, 0, len(b.sessions))
	pending := make([]*inflightPrompt, 0, len(b.sessions))
	for _, record := range b.sessions {
		records = append(records, record)
		inflight := record.inflight
		pending = append(pending, inflight)
		if inflight != nil {
			inflight.cancelRequested = true
			inflight.abortAdmission(errBridgeDisposed)
			b.settleAfterQuiescenceLocked(record, inflight)
		}
	}
	clear(b.sessions)
	b.mutex.Unlock()

	for _, record := range records {
		record.agent.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
	}

	var failures []error
	for index, record := range records {
		if inflight := pending[index]; inflight != nil {
			<-inflight.admissionDone
		}
		if err := record.agent.WhenIdle(ctx); err != nil {
			failures = append(failures, fmt.Errorf("acp: 等会话 %s 静下来失败：%w", record.agent.ID(), err))
		}
		b.mutex.Lock()
		tail := record.outputTail
		b.mutex.Unlock()
		<-tail
	}

	if err := b.releaseSubscriptions(ctx); err != nil {
		failures = append(failures, err)
	}

	if b.config.Subagents != nil {
		parents := make([]agent.Agent, 0, len(records))
		for _, record := range records {
			parents = append(parents, record.agent)
		}
		if err := b.config.Subagents.DrainContinuableDescendants(ctx, parents); err != nil {
			b.config.warn(fmt.Sprintf("acp: 可续子 agent 拆解失败：%v", err))
		}
	}

	for _, record := range records {
		if err := record.dispose(ctx); err != nil {
			failures = append(failures, fmt.Errorf("acp: 拆会话 %s 失败：%w", record.agent.ID(), err))
		}
	}
	return errors.Join(failures...)
}
