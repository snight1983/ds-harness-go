// 本文件的作用：这座桥本身——它攥着哪些会话、怎么装上去、运行时那几条边怎么订上
// 又怎么撤掉，以及握手、登出和收摊这几个不落在具体一条会话上的协议方法。
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
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
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
