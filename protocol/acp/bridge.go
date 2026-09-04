// 本文件的作用：这座桥本身——它攥着哪些会话、五个协议方法各自怎么办、运行时那三条
// 边怎么翻成线上的话、一次提示词从准入到结算走的那条状态机、以及收摊的次序。
//
// 源: packages/acp/acp/src/index.ts

package acp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
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
	errSessionClosing  = errors.New("acp: 这条 ACP 会话正在关掉")
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

	// control 是这条会话那份模型选择，为 nil 表示这条线没挂 LLM 目录。
	//
	// 源: packages/acp/acp/src/session.ts:101
	control *ModelControl

	// pendingSelections 记的是已经排进收件箱、还没被认领走的那几条消息各自钉的路由。
	//
	// 源: packages/acp/acp/src/session.ts:105
	//
	// 它在认领那一刻被取走并清掉：从那时起这份选择由 [ModelControl] 按回合钉着。
	pendingSelections map[llm.MessageID]agent.ModelSelection

	// closing 一置起来这条会话就不再接新提示词、也不再改配置。
	//
	// 源: packages/acp/acp/src/session.ts:104, 473-475
	closing bool

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
	// activating 是那几条正在续跑半路上、还没进 sessions 的会话标识。
	//
	// 源: packages/acp/acp/src/index.ts:107（activating）
	//
	// 一次续跑要先翻存档、再建 agent，那两步之间这条会话在两张表里都查不到。这张表
	// 补上那段空当：不然两次同时到的 session/resume 会在同一段存档上各建一个 agent，
	// 而 `session/list` 也会把一条马上就要活过来的会话报成可续的。
	activating map[sessionlog.SessionID]struct{}
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
// 后两条各自跟着一样可选的协作者走：没挂 LLM 目录就没有配置项可推，没挂审批服务就是
// 这条线上没人拍板，这座桥也就不参与。
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
	if b.config.Models != nil {
		steps = append(steps, func() (func(context.Context) error, error) {
			return b.config.Models.OnAdaptersUpdated(ctx, owner, b.onAdaptersUpdated)
		})
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

// onSessionEvent 把**已提交**的助手消息、推理、工具调用与结果翻上线，顺便记下相关
// 回合的结束理由并放掉那个回合上的模型钉住。
//
// 源: packages/acp/acp/src/session.ts:229-268（onSessionEvent）
//
// 原始流片段、计划、标题、重试标记仍然一条都不发：那些是呈现数据，不属于这条自动化
// 线。发出去的这四样都是**已提交**的耐久事实，DSH 上游发的也正是这四样。每条会话一
// 条投递链，好让块与消息的先后次序扛得住中途那些异步的附件读取。
//
// 新增: DSH 那个 `finally` 是为了「助手那一支抛了也照样记结束理由」。Go 这边几种事件
// 类型互斥，一条事件不可能既是 assistant/message 又是 turn/end，所以那层 finally 收敛
// 成了并列的分支。
//
// 新增: 下面那几处类型断言测不到。[sessionlog.DecodeData] 按事件类型派发，
// `assistant/message` 解出来的**只可能**是 [sessionlog.AssistantMessageData]，另外三种
// 同理——要走到那几个 `return` 上，得先把事件类型和负载类型的对应关系改错。那正是留着
// 它们的理由：DSH 那边是 TS 的判别联合，编译器替它挡住了这件事；Go 这边 `any` 挡不住，
// 而一次静默的错配会让这条线**无声地**少发一条更新、或者少记一次回合结束。
func (b *Bridge) onSessionEvent(sess *coresession.Session, event sessionlog.Event) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record, ok := b.sessions[sess.ID()]
	if !ok || record.agent.Session() != sess {
		return
	}
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		b.config.warn(fmt.Sprintf("acp: %s 解不开：%v", event.Type, err))
		return
	}
	switch event.Type {
	case sessionlog.EventAssistantMessage:
		message, typed := data.(sessionlog.AssistantMessageData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, message.Turn), func(ctx context.Context) ([]wire.SessionUpdate, error) {
			return AssistantUpdates(ctx, b.config.Attachments, b.config.Meter, sess, message)
		})
	case sessionlog.EventToolCall:
		call, typed := data.(sessionlog.ToolCallData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, call.Turn), func(context.Context) ([]wire.SessionUpdate, error) {
			return []wire.SessionUpdate{ToolCallUpdate(call)}, nil
		})
	case sessionlog.EventToolResult:
		result, typed := data.(sessionlog.ToolResultData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, result.Turn), func(ctx context.Context) ([]wire.SessionUpdate, error) {
			update, onWire, err := ToolResultUpdate(ctx, b.config.Attachments, result)
			if err != nil || !onWire {
				return nil, err
			}
			return []wire.SessionUpdate{update}, nil
		})
	case sessionlog.EventTurnEnd:
		end, typed := data.(sessionlog.TurnEndData)
		if !typed {
			return
		}
		inflight := record.inflight
		if inflight != nil && inflight.hasTurn && inflight.turn == end.Turn {
			inflight.endReason = end.Reason
		}
		// 这个回合的每一步都跑完了，钉住的那份路由到此为止——之后装配用的是这条会话
		// 当下**配置成**的那一份。ReleaseTurn 只认这个确切的回合号，所以一次迟到的
		// turn/end 放不掉后面那个回合的钉住。
		if record.control != nil {
			record.control.ReleaseTurn(end.Turn)
		}
	}
}

// scheduleLocked 把一节待发的更新接到这条会话那条投递链的尾巴上。
//
// 源: packages/acp/acp/src/session.ts:232-266
//
// build 在这条链轮到自己的时候才跑：附件读取是异步的，先转完再排队会让两条消息的
// 块次序跟着读取快慢乱掉。owner 为 nil 表示这一节转不动时不算在任何一次提示词头上。
//
// 调用方必须攥着 [Bridge.mutex]。接上去这一步在锁里做完，所以两条同时到的事件拿到的
// 是两个前后相接的位置，谁也插不到谁前面去。
func (b *Bridge) scheduleLocked(
	record *sessionRecord,
	owner *inflightPrompt,
	build func(context.Context) ([]wire.SessionUpdate, error),
) {
	previous := record.outputTail
	next := make(chan struct{})
	record.outputTail = next

	sessionID := record.agent.ID()
	go func() {
		defer close(next)
		<-previous
		b.deliver(sessionID, build, owner)
	}()
}

// deliver 转出这一节的那几条更新并按序发出去。
//
// 源: packages/acp/acp/src/session.ts:236-243
//
// 转换整节一起做：DSH 的 `assistantUpdates` 是一个交回整个数组的异步函数，一个块转
// 不动就整条消息一条都不发。失败记在那次提示词名下并写一行日志。
func (b *Bridge) deliver(
	sessionID sessionlog.SessionID,
	build func(context.Context) ([]wire.SessionUpdate, error),
	owner *inflightPrompt,
) {
	b.mutex.Lock()
	ctx := b.notifyCtx
	b.mutex.Unlock()
	if ctx == nil {
		return
	}
	updates, err := build(ctx)
	if err != nil {
		b.mutex.Lock()
		if owner != nil && owner.outputError == nil {
			owner.outputError = err
		}
		b.mutex.Unlock()
		b.config.warn(fmt.Sprintf("acp: 助手输出转不动：%v", err))
		return
	}
	for _, update := range updates {
		b.notify(ctx, wire.SessionNotification{SessionId: wire.SessionId(sessionID), Update: update})
	}
}

// onAdaptersUpdated 在 LLM 拓扑变了之后，把每条会话那份配置项状态重新推给对面。
//
// 源: packages/acp/acp/src/session.ts:270-284（topologyChanged）
//
// 选项在链**外**算：翻目录要问适配器，那一步可能很慢，不该把这条会话的助手输出堵住。
// 算完之后那条通知才排上链，于是它和已经排在前面的输出之间仍然是有序的。
// 两处失败都只记一行：一份推不出去的配置改变不了运行时里已经变了的拓扑。
func (b *Bridge) onAdaptersUpdated() {
	b.mutex.Lock()
	ctx := b.notifyCtx
	records := make([]*sessionRecord, 0, len(b.sessions))
	for _, record := range b.sessions {
		if record.control != nil {
			records = append(records, record)
		}
	}
	b.mutex.Unlock()
	if ctx == nil {
		return
	}
	for _, record := range records {
		go b.pushConfigOptions(ctx, record)
	}
}

// pushConfigOptions 算一条会话当下那份配置项状态，然后把它排上那条投递链。
//
// 源: packages/acp/acp/src/session.ts:271-283
func (b *Bridge) pushConfigOptions(ctx context.Context, record *sessionRecord) {
	options, err := record.control.Options(ctx)
	if err != nil {
		b.config.warn(fmt.Sprintf("acp: 会话 %s 的配置项算不出来：%v", record.agent.ID(), err))
		return
	}
	b.mutex.Lock()
	if b.sessions[record.agent.ID()] != record {
		// 这条会话在算选项的这段时间里被关掉了。
		b.mutex.Unlock()
		return
	}
	b.scheduleLocked(record, nil, func(context.Context) ([]wire.SessionUpdate, error) {
		return []wire.SessionUpdate{{
			ConfigOptionUpdate: &wire.SessionConfigOptionUpdate{ConfigOptions: options},
		}}, nil
	})
	b.mutex.Unlock()
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

// onInboxClaimed 把这次提示词那条消息落到的那个回合号记下来，并把这条消息排队时钉的
// 那份路由按到这个回合上。
//
// 源: packages/acp/acp/src/session.ts:286-296（onInboxClaimed）
//
// 之后的结束理由、输出失败归属、以及"区间内还是区间外的错误"三件事，全靠这个回合号
// 认人。
//
// 钉住那一半管的是另一件事：一条消息排队和它被认领之间，对面可以改模型。钉住让这个
// 回合的每一步走的都是排队那一刻那条路由——对面看到的和下一次装配抓的仍然是改完之后
// 那一份，所以回合中途改模型不会把正在跑的这一步换掉。
func (b *Bridge) onInboxClaimed(live agent.Agent, message llm.Message, turn int) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record := b.ownedRecordLocked(live)
	if record == nil {
		return
	}
	if inflight := record.inflight; inflight != nil && inflight.messageID == message.ID {
		inflight.turn = turn
		inflight.hasTurn = true
	}
	if record.control == nil {
		return
	}
	selection, pinned := record.pendingSelections[message.ID]
	if !pinned {
		return
	}
	delete(record.pendingSelections, message.ID)
	record.control.PinTurn(turn, selection)
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
// [github.com/snight1983/ds-harness-go/tools.ApprovalRequest.Agent] 是一把 [scope.Key]，所以这里按那把钥匙
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
// 源: packages/acp/acp/src/index.ts:176-190
//
// 单版本服务端：规范说的"支持就回同一个版本，否则回自己支持的最新版本"，在这里是同
// 一个答案。
//
// 新增: DSH 那份能力表是写死的常量——它的 `inject` 声明了 llm 和 sessionPersistence
// 两个必需服务，所以那几项在它那边永远都在。Go 这边它们是可以为 nil 的装配项（见
// [Config]），所以这里逐项按**真的挂了没有**来声明：一条声明了自己办不到的能力，比
// 一条不声明要糟得多——对面会照着它去发请求，然后收到 -32601。
func (b *Bridge) Initialize(ctx context.Context, _ wire.InitializeRequest) (wire.InitializeResponse, error) {
	enabled := SupportsImagePrompts(ctx, b.config.Attachments, b.config.Models, b.config.Provider, b.config.Model)
	b.mutex.Lock()
	b.imagePromptEnabled = enabled
	b.mutex.Unlock()

	// close 不挂条件：它拆的是这座桥自己攥着的那条记录，不靠任何一样可选的协作者。
	capabilities := wire.AgentCapabilities{
		PromptCapabilities: wire.PromptCapabilities{Image: enabled, Audio: false, EmbeddedContext: false},
		SessionCapabilities: wire.SessionCapabilities{
			Close: &wire.SessionCloseCapabilities{},
		},
	}
	if b.config.MCPServers != nil {
		capabilities.McpCapabilities = wire.McpCapabilities{Http: true}
	}
	if b.config.Persistence != nil {
		capabilities.SessionCapabilities.List = &wire.SessionListCapabilities{}
		capabilities.SessionCapabilities.Resume = &wire.SessionResumeCapabilities{}
	}
	return wire.InitializeResponse{
		ProtocolVersion:   wire.ProtocolVersionNumber,
		AgentInfo:         &wire.Implementation{Name: AgentName, Version: AgentVersion},
		AgentCapabilities: capabilities,
		AuthMethods:       []wire.AuthMethod{},
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
	owner, err := b.activationOwner()
	if err != nil {
		return wire.NewSessionResponse{}, err
	}
	if err := validateWorkspaceParams(params.AdditionalDirectories); err != nil {
		return wire.NewSessionResponse{}, err
	}
	workspaceID, err := b.workspaceOf(ctx, params.Cwd)
	if err != nil {
		return wire.NewSessionResponse{}, err
	}

	fallback := b.fallbackSelection()
	sessionID := sessionlog.SessionID(uuid.NewString())
	control := b.newModelControl(fallback)
	handle, err := b.config.Agents.Create(ctx, owner, agent.CreateOptions{
		SessionID: sessionID,
		// 线上那条 cwd 是客户端那台机器上的写法，服务端认不得。落进会话头的是它换出来
		// 的工作区标识，见 [WorkspaceResolver]——路径到这条边界为止，一个字都不往下传。
		WorkspaceID:  workspaceID,
		AgentOptions: agent.Options{Provider: fallback.Provider, Model: fallback.Model},
		Setup:        b.sessionSetup(control, params.McpServers),
	})
	if err != nil {
		return wire.NewSessionResponse{}, mapActivationError("session/new", err)
	}

	record, err := b.adopt(ctx, sessionID, handle, control)
	if err != nil {
		return wire.NewSessionResponse{}, err
	}
	options, err := b.configOptions(ctx, record)
	if err != nil {
		b.abandon(ctx, sessionID, record)
		return wire.NewSessionResponse{}, err
	}
	return wire.NewSessionResponse{SessionId: wire.SessionId(sessionID), ConfigOptions: options}, nil
}

// activationOwner 查一遍「这座桥现在开得出会话吗」，交出挂新 agent 的那个作用域。
//
// 源: packages/acp/acp/src/index.ts:136-141（assertOpen）
func (b *Bridge) activationOwner() (*scope.Scope, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.closed {
		return nil, internalError("the ACP bridge has been disposed")
	}
	if b.owner == nil {
		return nil, internalError("the ACP bridge has not been installed")
	}
	return b.owner, nil
}

// fallbackSelection 是这条线上每一个会话开局用的那份路由。
//
// 源: packages/acp/acp/src/index.ts:72-75, packages/acp/acp/src/session.ts:120
func (b *Bridge) fallbackSelection() agent.ModelSelection {
	return agent.ModelSelection{Provider: b.config.Provider, Model: b.config.Model}
}

// newModelControl 造这条会话那份模型控制，没挂 LLM 目录时交回 nil。
//
// 源: packages/acp/acp/src/session.ts:118-121
func (b *Bridge) newModelControl(initial agent.ModelSelection) *ModelControl {
	if b.config.Models == nil {
		return nil
	}
	return NewModelControl(b.config.Models, initial, true)
}

// sessionSetup 造那份创建期的世界组装：装上模型选择，挂上这条会话自带的 MCP 服务器。
//
// 源: packages/acp/acp/src/session.ts:122-127, 176-181
//
// 两件事都必须在这里做，而不是在 agent 公布之后：它们决定第一次提示词装配看得见什么。
// 交回的 commit 什么都不做——这两样的拆除都挂在 agentScope 上，而 [agent.Setup] 的约定
// 保证 setup 报错时那个作用域整个被处置，所以这里不必自己回滚。
func (b *Bridge) sessionSetup(control *ModelControl, servers []wire.McpServer) agent.Setup {
	if control == nil && len(servers) == 0 {
		return nil
	}
	return func(ctx context.Context, agentScope *scope.Scope) (func() error, error) {
		if control != nil {
			if _, err := control.Install(ctx, agentScope, b.config.Agents, b.config.Prompts); err != nil {
				return nil, err
			}
		}
		if err := MountMCPServers(ctx, b.config.MCPServers, agentScope, servers); err != nil {
			return nil, err
		}
		return func() error { return nil }, nil
	}
}

// mapActivationError 把一次开会话的失败折成线上那两个错误码里的一个。
//
// 源: packages/acp/acp/src/index.ts:216-220（catch AcpMcpConfigError）
func mapActivationError(method string, err error) error {
	var config *MCPConfigError
	if errors.As(err, &config) {
		return invalidParams(config.Message)
	}
	return internalError(fmt.Sprintf("%s failed: %v", method, err))
}

// adopt 把一个刚活过来的 agent 记进这座桥名下。
//
// 源: packages/acp/acp/src/index.ts:206-214
func (b *Bridge) adopt(
	ctx context.Context,
	sessionID sessionlog.SessionID,
	handle agent.Handle,
	control *ModelControl,
) (*sessionRecord, error) {
	tail := make(chan struct{})
	close(tail)
	record := &sessionRecord{
		agent:             handle.Agent,
		dispose:           handle.Dispose,
		control:           control,
		pendingSelections: map[llm.MessageID]agent.ModelSelection{},
		outputTail:        tail,
	}
	b.mutex.Lock()
	if b.closed {
		// 一次真的连接关闭挤在了创建的半路上：这一个再记进表里就没人拆得掉它。
		b.mutex.Unlock()
		if disposeErr := handle.Dispose(ctx); disposeErr != nil {
			b.config.warn(fmt.Sprintf("acp: 收摊途中建出来的会话 %s 拆不掉：%v", sessionID, disposeErr))
		}
		return nil, internalError("connection closed during session activation")
	}
	b.sessions[sessionID] = record
	b.mutex.Unlock()
	return record, nil
}

// configOptions 算这条会话开局摆出去的那份配置项，没挂 LLM 目录时一项都不摆。
//
// 源: packages/acp/acp/src/index.ts:224-226
func (b *Bridge) configOptions(ctx context.Context, record *sessionRecord) ([]wire.SessionConfigOption, error) {
	if record.control == nil {
		return nil, nil
	}
	options, err := record.control.Options(ctx)
	if err != nil {
		return nil, internalError(fmt.Sprintf("session config options failed: %v", err))
	}
	return options, nil
}

// abandon 撤掉一次开到半路上失败了的会话。
//
// 源: packages/acp/acp/src/index.ts:238-241
func (b *Bridge) abandon(ctx context.Context, sessionID sessionlog.SessionID, record *sessionRecord) {
	b.mutex.Lock()
	if b.sessions[sessionID] == record {
		delete(b.sessions, sessionID)
	}
	b.mutex.Unlock()
	if err := b.closeRecord(ctx, record); err != nil {
		b.config.warn(fmt.Sprintf("acp: 开到半路的会话 %s 收不干净：%v", sessionID, err))
	}
}

// validateWorkspaceParams 拒掉这条自动化契约之外的那几样工作区特性。
//
// 源: packages/acp/acp/src/index.ts:514-524（validateWorkspaceParams）
//
// `session/new` 和 `session/resume` 共用这一条：两边收的是同一个字段，判据也该是同一个。
// mcpServers 不在这里拒——那一支由 [MountMCPServers] 判，因为它认不认得出来取决于这条线
// 上挂没挂 MCP 宿主。
//
// 新增: DSH 这里还查一遍 cwd 绝不绝对。那道检查删掉了：cwd 是**客户端**那台机器上的
// 写法，它长什么样是客户端的事，服务端连它指着什么都不知道，更没有立场规定它的形状。
// 服务端对它只做一件事，就是 [Bridge.workspaceOf] 那次换算。
func validateWorkspaceParams(additionalDirectories []string) error {
	if len(additionalDirectories) > 0 {
		return invalidParams("additionalDirectories is not supported")
	}
	return nil
}

// workspaceOf 把线上那条 cwd 换成一个工作区标识。
//
// 新增: 见 [WorkspaceResolver]。没挂登记册、或者这条 cwd 没人认领，都给空工作区——
// 两者对这条线是同一件事：这个会话不属于任何工作区。
func (b *Bridge) workspaceOf(ctx context.Context, cwd string) (sessionlog.WorkspaceID, error) {
	if b.config.Workspaces == nil {
		return "", nil
	}
	id, found, err := b.config.Workspaces.WorkspaceOf(ctx, cwd)
	if err != nil {
		return "", internalError(fmt.Sprintf("workspace lookup failed: %v", err))
	}
	if !found {
		return "", nil
	}
	return id, nil
}

// workspaceDisplay 给出一个工作区标识拿给客户端看的那条路径；没有就是空串。
func (b *Bridge) workspaceDisplay(ctx context.Context, id sessionlog.WorkspaceID) string {
	if id == "" || b.config.Workspaces == nil {
		return ""
	}
	display, found, err := b.config.Workspaces.WorkspaceDisplay(ctx, id)
	if err != nil || !found {
		return ""
	}
	return display
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

// ResumeSession 在一段落了档的会话上重新活出一个 agent。
//
// 源: packages/acp/acp/src/index.ts:335-386（resumeSession）
//
// 它不回放历史：ACP 的 resume 按定义就是「接着往下跑」，要读回全部消息那是 `session/load`，
// 而这条线不办那个。
//
// 能续的只有顶层会话：子 agent 的会话和分叉出来的会话都由开出它们的那个父亲拥有，一个
// 外部客户端把它们单独拉起来会造出两个都以为自己是那条日志的主人的 agent。
func (b *Bridge) ResumeSession(ctx context.Context, params wire.ResumeSessionRequest) (wire.ResumeSessionResponse, error) {
	if b.config.Persistence == nil {
		return wire.ResumeSessionResponse{}, wire.NewMethodNotFound("session/resume")
	}
	owner, err := b.activationOwner()
	if err != nil {
		return wire.ResumeSessionResponse{}, err
	}
	if err := validateWorkspaceParams(params.AdditionalDirectories); err != nil {
		return wire.ResumeSessionResponse{}, err
	}
	workspaceID, err := b.workspaceOf(ctx, params.Cwd)
	if err != nil {
		return wire.ResumeSessionResponse{}, err
	}

	sessionID := sessionlog.SessionID(params.SessionId)
	release, err := b.beginActivation(sessionID)
	if err != nil {
		return wire.ResumeSessionResponse{}, err
	}
	defer release()

	persisted, err := b.resumableHeader(ctx, sessionID, workspaceID)
	if err != nil {
		return wire.ResumeSessionResponse{}, err
	}

	fallback := b.fallbackSelection()
	control := b.newModelControl(fallback)
	handle, err := b.config.Agents.Resume(ctx, owner, agent.ResumeOptions{
		ResumeSessionID: persisted.ID,
		AgentOptions:    agent.Options{Provider: fallback.Provider, Model: fallback.Model},
		Setup:           b.sessionSetup(control, params.McpServers),
	})
	if err != nil {
		return wire.ResumeSessionResponse{}, mapActivationError("session/resume", err)
	}
	adoptLoggedSelection(handle.Agent, control)

	record, err := b.adopt(ctx, sessionID, handle, control)
	if err != nil {
		return wire.ResumeSessionResponse{}, err
	}
	options, err := b.configOptions(ctx, record)
	if err != nil {
		b.abandon(ctx, sessionID, record)
		return wire.ResumeSessionResponse{}, err
	}
	return wire.ResumeSessionResponse{ConfigOptions: options}, nil
}

// beginActivation 占下一条会话标识的续跑位子，交回让出它的那个函数。
//
// 源: packages/acp/acp/src/index.ts:341-349
//
// 三处都要查：这座桥自己开着的、正在续跑半路上的、以及整套运行时里已经活着的（另一个
// 前端可能就攥着它）。同一条日志上活两个 agent 会把那条日志写坏。
func (b *Bridge) beginActivation(sessionID sessionlog.SessionID) (func(), error) {
	_, live := b.config.Sessions.Get(sessionID)
	b.mutex.Lock()
	defer b.mutex.Unlock()
	_, active := b.sessions[sessionID]
	_, activating := b.activating[sessionID]
	if live || active || activating {
		return nil, invalidParams(fmt.Sprintf("session is already active: %s", sessionID))
	}
	b.activating[sessionID] = struct{}{}
	return func() {
		b.mutex.Lock()
		delete(b.activating, sessionID)
		b.mutex.Unlock()
	}, nil
}

// resumableHeader 从存档里找出这条会话，并判它续不续得动。
//
// 源: packages/acp/acp/src/index.ts:351-366
func (b *Bridge) resumableHeader(
	ctx context.Context,
	sessionID sessionlog.SessionID,
	workspaceID sessionlog.WorkspaceID,
) (sessionlog.SessionHeader, error) {
	headers, err := b.config.Persistence.List(ctx)
	if err != nil {
		return sessionlog.SessionHeader{}, internalError(fmt.Sprintf("session/resume failed: %v", err))
	}
	for _, header := range headers {
		if header.ID != sessionID {
			continue
		}
		if header.Origin == sessionlog.OriginSubagent || header.ParentSession != "" {
			break
		}
		// 新增: DSH 比的是两条工作目录字符串（realpath 之后）。这里比的是两个不透明的
		// 工作区标识：请求那一侧由 [Bridge.workspaceOf] 换出来，存档那一侧就是会话头
		// 上那一个。一次相等，不碰文件系统，也不问跑着这个进程的机器上有没有那个目录。
		if header.WorkspaceID != workspaceID {
			return sessionlog.SessionHeader{}, invalidParams(
				fmt.Sprintf("session cwd does not match: %s", sessionID))
		}
		return header, nil
	}
	return sessionlog.SessionHeader{}, invalidParams(fmt.Sprintf("session is not resumable: %s", sessionID))
}

// adoptLoggedSelection 把续跑出来那条会话记着的那份路由，按回这条会话的模型控制上。
//
// 源: packages/acp/acp/src/session.ts:166-175（AcpSession.resume 里的 selectionFor）
//
// 新增: DSH 在 setup **里面**造这份控制，因为它那个 setup 收得到 agentCtx.agent。Go 的
// [agent.Setup] 只收一个作用域（见 harness/agentloop/loop.go 里 `setup(prepared.life,
// prepared.agent.Scope())` 那一行），而那一刻重建出来的会话还没公布，从存储里也取不到。
// 所以这里挪后一步：先按部署那份路由把控制装上，Resume 交回句柄之后、这条记录被记进
// 表**之前**，再从日志里那份请求头把真正记着的路由按回去。
//
// 那段空当里可能发生的唯一一件事，是这个 agent 自己接着跑一个被打断的回合——那一步会
// 走部署那份路由而不是日志里那份。这是这条移植上剩下的一处可观察差异，逐项记在
// docs/portmap/decisions.md 里。
//
// 读不出请求头（这条日志还没有过一次请求）时什么都不改：那时装上去的那份就是对的。
func adoptLoggedSelection(live agent.Agent, control *ModelControl) {
	if control == nil {
		return
	}
	header, ok, err := live.Session().RequestHeader()
	if err != nil || !ok {
		return
	}
	control.commit(selectionFor(header))
}

// selectionFor 从一份请求头里读出那条会话真正用着的路由。
//
// 源: packages/acp/acp/src/session.ts:60-72（selectionFor）
//
// 推理档位只在它**不是**适配器补出来的默认值时才留下：一个补出来的档位不是这条会话选
// 的，把它按成一次显式选择会让对面在选择器上看到一个自己从来没点过的值。
func selectionFor(header sessionlog.EpochHeader) agent.ModelSelection {
	selection := agent.ModelSelection{Provider: header.Config.Provider, Model: header.Config.Model}
	if header.Config.ReasoningEffort != "" && !header.AdapterDefaults.ReasoningEffort {
		selection.ReasoningEffort = header.Config.ReasoningEffort
	}
	return selection
}

// ListSessions 翻一页存档里**续得动**的那些会话，最新的排前面。
//
// 源: packages/acp/acp/src/index.ts:388-425（listSessions）
//
// 活着的一条都不报：它们要么已经在这条连接上开着（对面自己知道），要么被别人攥着，
// 两种都不该出现在一张「可以续跑」的名单里。
func (b *Bridge) ListSessions(ctx context.Context, params wire.ListSessionsRequest) (wire.ListSessionsResponse, error) {
	if b.config.Persistence == nil {
		return wire.ListSessionsResponse{}, wire.NewMethodNotFound("session/list")
	}
	// cwd 的形状不在这里验，理由见 [validateWorkspaceParams]；给了就换成一个工作区
	// 标识，这一页只留归在那个工作区下的。
	var wanted sessionlog.WorkspaceID
	if params.Cwd != nil {
		resolved, err := b.workspaceOf(ctx, *params.Cwd)
		if err != nil {
			return wire.ListSessionsResponse{}, err
		}
		wanted = resolved
	}
	cursor, hasCursor, err := decodeSessionListCursor(params.Cursor)
	if err != nil {
		return wire.ListSessionsResponse{}, invalidParams(err.Error())
	}
	headers, err := b.config.Persistence.List(ctx)
	if err != nil {
		return wire.ListSessionsResponse{}, internalError(fmt.Sprintf("session/list failed: %v", err))
	}

	b.mutex.Lock()
	busy := make(map[sessionlog.SessionID]struct{}, len(b.sessions)+len(b.activating))
	for id := range b.sessions {
		busy[id] = struct{}{}
	}
	for id := range b.activating {
		busy[id] = struct{}{}
	}
	b.mutex.Unlock()

	entries := make([]sessionListEntry, 0, len(headers))
	for _, header := range headers {
		if _, taken := busy[header.ID]; taken {
			continue
		}
		if _, live := b.config.Sessions.Get(header.ID); live {
			continue
		}
		if header.Origin == sessionlog.OriginSubagent || header.ParentSession != "" {
			continue
		}
		// 新增: 原来这里先筛掉「没有工作目录」的会话，理由是 `session/resume` 要拿那条
		// 路径和请求里的 cwd 比，没有就比不成。换成工作区标识之后这一筛没有了：空串是
		// 一个合法的取值（「不属于任何工作区」），而那次比较照样成立。
		if params.Cwd != nil && header.WorkspaceID != wanted {
			continue
		}
		entries = append(entries, sessionListEntry{
			sessionID:   header.ID,
			workspaceID: header.WorkspaceID,
			createdAt:   header.CreatedAt,
		})
	}
	sortSessionList(entries)

	remaining := entries
	if hasCursor {
		filtered := make([]sessionListEntry, 0, len(entries))
		for _, entry := range entries {
			if isAfterSessionListCursor(entry, cursor) {
				filtered = append(filtered, entry)
			}
		}
		remaining = filtered
	}
	page := remaining
	if size := b.config.sessionListPageSize(); len(page) > size {
		page = page[:size]
	}

	response := wire.ListSessionsResponse{Sessions: make([]wire.SessionInfo, 0, len(page))}
	for _, entry := range page {
		response.Sessions = append(response.Sessions, wire.SessionInfo{
			SessionId: wire.SessionId(entry.sessionID),
			// 线上这一项是给客户端看的那条路径，由登记册按标识给出；没挂登记册、
			// 或者这条会话不属于任何工作区，就是空串。
			Cwd: b.workspaceDisplay(ctx, entry.workspaceID),
		})
	}
	if len(remaining) > len(page) {
		last := page[len(page)-1]
		next := encodeSessionListCursor(sessionListCursor{
			createdAt: last.createdAt,
			sessionID: string(last.sessionID),
		})
		response.NextCursor = &next
	}
	return response, nil
}

// CloseSession 停掉一条会话上的活儿、把它排干、然后拆掉它。
//
// 源: packages/acp/acp/src/index.ts:427-441（closeSession）
//
// 不管收干净没收干净，这条记录都从表里摘掉：一条报了失败还留在表里的会话，对面既
// 用不了也关不掉。
func (b *Bridge) CloseSession(ctx context.Context, params wire.CloseSessionRequest) (wire.CloseSessionResponse, error) {
	sessionID := sessionlog.SessionID(params.SessionId)
	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return wire.CloseSessionResponse{}, internalError("the ACP bridge has been disposed")
	}
	record, known := b.sessions[sessionID]
	if !known {
		b.mutex.Unlock()
		return wire.CloseSessionResponse{}, invalidParams(fmt.Sprintf("unknown session: %s", sessionID))
	}
	b.mutex.Unlock()

	closeErr := b.closeRecord(ctx, record)

	b.mutex.Lock()
	if b.sessions[sessionID] == record {
		delete(b.sessions, sessionID)
	}
	b.mutex.Unlock()

	if closeErr != nil {
		return wire.CloseSessionResponse{}, internalError(fmt.Sprintf("session close failed: %v", closeErr))
	}
	return wire.CloseSessionResponse{}, nil
}

// closeRecord 按定死的次序收掉一条会话：停活儿、排干、冲刷、拆解。
//
// 源: packages/acp/acp/src/session.ts:462-520（AcpSession.close）
//
// 次序是要害。先置 closing，让新的提示词和配置改动当场被拒；再停掉正在跑的活儿；等准入
// 那一段落地（一次正在写的富准入不能被丢在半路）；等这个 agent 空下来；等那条投递链把
// 已提交的输出送完——session/event 是在空闲**之前**同步排上去的，所以这时候读到的尾巴
// 已经包含了这一轮的全部。可续的子 agent 活得比开出它们的回合久，所以在拆掉这个顶层
// agent 之前先孩子优先地排干那片森林。最后冲刷会话日志，再拆 agent。
func (b *Bridge) closeRecord(ctx context.Context, record *sessionRecord) error {
	b.mutex.Lock()
	record.closing = true
	inflight := record.inflight
	if inflight != nil {
		inflight.cancelRequested = true
		inflight.abortAdmission(errSessionClosing)
		b.settleAfterQuiescenceLocked(record, inflight)
	}
	// 和 [Bridge.Cancel] 同一条判据：准入还没落进耐久收件箱时，这个 agent 上跑的东西
	// 和这次提示词无关，不该被它连累。
	cancelAgent := inflight == nil || inflight.messageQueued
	b.mutex.Unlock()

	if cancelAgent {
		record.agent.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{})
	}
	if inflight != nil {
		<-inflight.admissionDone
	}

	var failures []error
	if err := record.agent.WhenIdle(ctx); err != nil {
		failures = append(failures, fmt.Errorf("acp: 等会话 %s 静下来失败：%w", record.agent.ID(), err))
	}
	b.mutex.Lock()
	tail := record.outputTail
	b.mutex.Unlock()
	<-tail

	if b.config.Subagents != nil {
		if err := b.config.Subagents.DrainContinuableDescendants(ctx, []agent.Agent{record.agent}); err != nil {
			b.config.warn(fmt.Sprintf("acp: 可续子 agent 拆解失败：%v", err))
		}
	}
	if _, err := b.config.Sessions.Flush(ctx, record.agent.Session()); err != nil {
		failures = append(failures, fmt.Errorf("acp: 冲刷会话 %s 失败：%w", record.agent.ID(), err))
	}
	if err := record.dispose(ctx); err != nil {
		failures = append(failures, fmt.Errorf("acp: 拆会话 %s 失败：%w", record.agent.ID(), err))
	}

	b.mutex.Lock()
	clear(record.pendingSelections)
	b.mutex.Unlock()
	return errors.Join(failures...)
}

// SetSessionConfigOption 改一个摆出来的会话配置项，交回改完之后那份完整状态。
//
// 源: packages/acp/acp/src/index.ts:443-455（setSessionConfigOption）
func (b *Bridge) SetSessionConfigOption(
	ctx context.Context,
	params wire.SetSessionConfigOptionRequest,
) (wire.SetSessionConfigOptionResponse, error) {
	// 这条线只摆 select 型的两项（模型、推理档位），所以一个布尔值请求指不出任何一个
	// 摆出来的配置项。
	if params.ValueId == nil {
		return wire.SetSessionConfigOptionResponse{}, invalidParams("unsupported session config option value")
	}
	sessionID := sessionlog.SessionID(params.ValueId.SessionId)

	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return wire.SetSessionConfigOptionResponse{}, internalError("the ACP bridge has been disposed")
	}
	record, known := b.sessions[sessionID]
	if !known {
		b.mutex.Unlock()
		return wire.SetSessionConfigOptionResponse{}, invalidParams(fmt.Sprintf("unknown session: %s", sessionID))
	}
	if record.closing {
		b.mutex.Unlock()
		return wire.SetSessionConfigOptionResponse{}, invalidParams(fmt.Sprintf("session is closing: %s", sessionID))
	}
	control := record.control
	b.mutex.Unlock()

	if control == nil {
		return wire.SetSessionConfigOptionResponse{}, wire.NewMethodNotFound("session/set_config_option")
	}
	options, err := control.Set(ctx, params.ValueId.ConfigId, params.ValueId.Value)
	if err != nil {
		var config *ModelConfigError
		if errors.As(err, &config) {
			return wire.SetSessionConfigOptionResponse{}, invalidParams(config.Message)
		}
		return wire.SetSessionConfigOptionResponse{}, internalError(
			fmt.Sprintf("session/set_config_option failed: %v", err))
	}
	return wire.SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

// 下面这两个方法这一端不办。
//
// 新增: DSH 那个 agent 对象只实现它真办的那几个方法，TS 的 SDK 对没装的方法自己回
// -32601。Go 的 [github.com/coder/acp-go-sdk.Agent] 是一个 11 方法的接口，一个都不能少，
// 所以这两个在这里显式交回 [github.com/coder/acp-go-sdk.NewMethodNotFound]——线上仍然是
// -32601，和 DSH 逐字相同。
//
// 两个都由一项这座桥**从不声明**的能力把着（会话模式、以及登出），所以一个守规矩的
// 客户端根本不会来问。

// Logout 不办：这条线不做认证，见 [Bridge.Authenticate]。
func (b *Bridge) Logout(context.Context, wire.LogoutRequest) (wire.LogoutResponse, error) {
	return wire.LogoutResponse{}, wire.NewMethodNotFound("session/logout")
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
// 失败用 [errors.Join] 攒着，理由和 [github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver] 那条逐字相同：拆解本来
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
		// 冲刷排在拆解**之前**：拆掉这个 agent 会把它那条会话从存储里摘走，之后没人
		// 再落得下那几条还攒在缓冲里的事件。
		if _, err := b.config.Sessions.Flush(ctx, record.agent.Session()); err != nil {
			failures = append(failures, fmt.Errorf("acp: 冲刷会话 %s 失败：%w", record.agent.ID(), err))
		}
		if err := record.dispose(ctx); err != nil {
			failures = append(failures, fmt.Errorf("acp: 拆会话 %s 失败：%w", record.agent.ID(), err))
		}
	}
	return errors.Join(failures...)
}
