// 本文件的作用：这座桥的会话台账——开一条、恢复一条、列出来、关掉，以及每条会话
// 各自那份模型选择和配置项。
//
// 源: packages/acp/acp/src/index.ts

package acp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

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
