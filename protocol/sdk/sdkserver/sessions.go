// 本文件的作用：会话那一套方法在这台服务器上各自怎么办——开一条、接着跑一条、
// 从一条分出来、改名、列出来、找、叫停、换模型、改排队、翻历史。
//
// 源: packages/api/session-controller/src/commands.ts,
// packages/api/session-controller/src/list.ts,
// packages/api/session-controller/src/history.ts,
// packages/api/session-controller/src/control.ts
//
// 新增: DSH 那一侧这十件事挂在 `api/gateway` 那条 `@Remote` 通道上，理由见
// sdkprotocol/session.go 的文件头。这里落在这条 SDK 线上，而门面后面**一件新能力
// 都没造**：列出、找、分出来、翻历史走
// [github.com/snight1983/ds-harness-go/feature/sessionquery]，改名走
// [github.com/snight1983/ds-harness-go/feature/sessiontitle]，换模型走
// [github.com/snight1983/ds-harness-go/harness/agent.ModelSelectionRef]，改队和叫停
// 走那个 agent 自己的方法。每一个方法都只做三件事：验入参、转调、把结果折成线上的形状。

package sdkserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/protocol/sdk/sdkprotocol"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// DefaultHistoryMaxMessages 是翻一页历史时默认装几条人和模型的消息。
//
// 源: packages/api/session-controller/src/history.ts:32（DEFAULT_MAX_MESSAGES）
const DefaultHistoryMaxMessages = 50

// CreateSession 开一条会话。
//
// 源: packages/api/session-controller/src/commands.ts:60-111（create）
//
// 没给标识就起一个。它走的是和 `session/prompt` 完全同一条创建路，所以先建后发和
// 直接发第一轮输入建出来的是同一种会话——两条路分叉的话，"先建一条再喂"这种用法
// 会拿到一个和别人不一样的会话，而那种差别要到很久以后才现形。
func (s *Server) CreateSession(
	ctx context.Context, params sdkprotocol.SessionCreateParams,
) (sdkprotocol.SessionCreateResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionCreateResult{}, err
	}
	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = newSessionID()
	}
	if _, err := s.getOrCreateSession(ctx, sessionID); err != nil {
		return sdkprotocol.SessionCreateResult{}, err
	}
	return sdkprotocol.SessionCreateResult{SessionID: sessionID}, nil
}

// ResumeSession 把一条落地的会话读回来接着跑。
//
// 源: packages/api/session-controller/src/commands.ts:32-58（resolveAgent 的 resume 那一支）
//
// 读日志、验重放、把 agent 重新铸出来这一整套都在
// [github.com/snight1983/ds-harness-go/harness/agent.Registry.Resume] 后面，本方法
// 只把它接到线上。
func (s *Server) ResumeSession(
	ctx context.Context, params sdkprotocol.SessionResumeParams,
) (sdkprotocol.SessionResumeResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionResumeResult{}, err
	}
	if params.SessionID == "" {
		return sdkprotocol.SessionResumeResult{}, fmt.Errorf("sdkserver: session/resume 要一个会话标识")
	}
	_, err := s.newSession(ctx, params.SessionID, func(
		ctx context.Context, owner *scope.Scope, current route, setup agent.Setup,
	) (agent.Handle, error) {
		return s.config.Agents.Resume(ctx, owner, agent.ResumeOptions{
			ResumeSessionID: sessionlog.SessionID(params.SessionID),
			AgentOptions:    current.options,
			Setup:           setup,
		})
	})
	if err != nil {
		return sdkprotocol.SessionResumeResult{}, err
	}
	return sdkprotocol.SessionResumeResult{SessionID: params.SessionID}, nil
}

// ForkSession 从一条会话某个已经收尾的回合处分出一条新的。
//
// 源: packages/api/session-controller/src/commands.ts:187-275（fork）
//
// 分叉点由 [forkCut] 算，工作区**继承来源那一条**而不是这条线握手时那一条：一条
// 分出来的会话和它的来源谈的是同一件事，把它挪进别的工作区会让两者在任何一张按
// 工作区筛的表上分家。
func (s *Server) ForkSession(
	ctx context.Context, params sdkprotocol.SessionForkParams,
) (sdkprotocol.SessionForkResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionForkResult{}, err
	}
	queries, err := s.requireQueries("session/fork")
	if err != nil {
		return sdkprotocol.SessionForkResult{}, err
	}
	if params.SessionID == "" {
		return sdkprotocol.SessionForkResult{}, fmt.Errorf("sdkserver: session/fork 要一个来源会话标识")
	}
	if params.AtSeq != nil && *params.AtSeq < 0 {
		return sdkprotocol.SessionForkResult{}, fmt.Errorf(
			"sdkserver: session/fork 的 atSeq 不能是负数，给的是 %d", *params.AtSeq)
	}

	snapshot, err := queries.ReadSession(ctx, sessionlog.SessionID(params.SessionID))
	if err != nil {
		return sdkprotocol.SessionForkResult{}, fmt.Errorf(
			"sdkserver: 读不回分叉来源 %s：%w", params.SessionID, err)
	}
	cut, err := forkCut(params.SessionID, snapshot.Events, params.AtSeq)
	if err != nil {
		return sdkprotocol.SessionForkResult{}, err
	}

	forkID := params.ForkSessionID
	if forkID == "" {
		forkID = newSessionID()
	}
	seed := slices.Clone(snapshot.Events[:cut])
	// 分出来的会话继承来源那些事件**连同它们的 seq**，所以它的起点是来源的起点——
	// 来源的头部被弹过一截时那个数不是 0，理由见
	// [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions.BaseSeq]。
	baseSeq := 0
	if len(snapshot.Events) > 0 {
		baseSeq = snapshot.Events[0].Seq
	}
	if _, err := s.newSession(ctx, forkID, func(
		ctx context.Context, owner *scope.Scope, current route, setup agent.Setup,
	) (agent.Handle, error) {
		return s.config.Agents.Create(ctx, owner, agent.CreateOptions{
			SessionID:     sessionlog.SessionID(forkID),
			WorkspaceID:   snapshot.Session.WorkspaceID,
			ParentSession: snapshot.Session.ID,
			Seed:          seed,
			SeedLength:    len(seed),
			BaseSeq:       baseSeq,
			AgentOptions:  current.options,
			Setup:         setup,
		})
	}); err != nil {
		return sdkprotocol.SessionForkResult{}, err
	}
	return sdkprotocol.SessionForkResult{SessionID: forkID}, nil
}

// forkCut 算出分出去的那一段前缀有**几条**事件。
//
// 源: packages/api/session-controller/src/commands.ts:210-228
//
// 两步：先挑一条 turn/end 当边界——给了 atSeq 就取**它之后**（含）第一条，没给或者
// 它已经越过日志末尾就取最后一条；再把切点从那条边界往后推，一直推到下一个
// turn/start 之前。第二步是要害：一个回合收尾之后、下一个回合开张之前的那些事件
// （落地下来的工具产物、压缩标记之类）属于**前**一个回合，甩掉它们会让分出来的会话
// 缺一截自己继承的历史。
//
// 新增: DSH 那两段代码把 seq 直接当下标使（`events[cut]`、`boundary.seq + 1`），
// 因为它那边日志从 0 起、一条不删，两者恒等。本仓库的日志会从最老的一头被弹掉一截
// （见 docs/session-log-limit.md），所以这里全程用下标，只在诊断里报 seq。
func forkCut(sessionID string, events []sessionlog.Event, atSeq *int) (int, error) {
	lastSeq := -1
	if len(events) > 0 {
		lastSeq = events[len(events)-1].Seq
	}

	boundary := -1
	if atSeq != nil {
		for index, event := range events {
			if event.Type == sessionlog.EventTurnEnd && event.Seq >= *atSeq {
				boundary = index
				break
			}
		}
	}
	if boundary < 0 && (atSeq == nil || *atSeq > lastSeq) {
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Type == sessionlog.EventTurnEnd {
				boundary = index
				break
			}
		}
	}
	if boundary < 0 {
		if atSeq != nil && *atSeq <= lastSeq {
			return 0, fmt.Errorf(
				"sdkserver: 会话 %s 里包住第 %d 条事件的那个回合还没收尾，分不出来",
				sessionID, *atSeq)
		}
		return 0, fmt.Errorf("sdkserver: 会话 %s 里没有一个收了尾的回合可以分", sessionID)
	}

	cut := boundary + 1
	for cut < len(events) && events[cut].Type != sessionlog.EventTurnStart {
		cut++
	}
	return cut, nil
}

// RenameSession 按一个人给的标题给会话改名。
//
// 源: packages/api/session-controller/src/commands.ts:156-186（rename）
//
// 归一化、那条硬约束、以及把事件追加进日志全在 [Config.Rename] 后面；没挂它就整条
// 拒，说法照录 DSH 那句「这份部署没挂标题服务」。
func (s *Server) RenameSession(
	ctx context.Context, params sdkprotocol.SessionRenameParams,
) (sdkprotocol.SessionRenameResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionRenameResult{}, err
	}
	if s.config.Rename == nil {
		return sdkprotocol.SessionRenameResult{}, fmt.Errorf(
			"sdkserver: 改名办不了，这份部署没挂标题服务")
	}
	handle, err := s.liveSession(params.SessionID)
	if err != nil {
		return sdkprotocol.SessionRenameResult{}, err
	}
	snapshot, err := s.config.Rename(ctx, handle.Agent, params.Title)
	if err != nil {
		return sdkprotocol.SessionRenameResult{}, fmt.Errorf(
			"sdkserver: 会话 %s 改名失败：%w", params.SessionID, err)
	}
	return sdkprotocol.SessionRenameResult{
		Title:     snapshot.Title,
		EventSeq:  snapshot.EventSeq,
		UpdatedAt: snapshot.UpdatedAt,
	}, nil
}

// ListSessions 列出这套运行时看得见的每一条会话。
//
// 源: packages/api/session-controller/src/list.ts
//
// 看得见的是**整套运行时**，不只是这条线自己开出来的那些——这一点和那四条通知流
// 一致（见 [Server.Install]）。顺序由引擎定死，本方法不重排。
func (s *Server) ListSessions(
	ctx context.Context, _ sdkprotocol.SessionListParams,
) (sdkprotocol.SessionListResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionListResult{}, err
	}
	queries, err := s.requireQueries("session/list")
	if err != nil {
		return sdkprotocol.SessionListResult{}, err
	}
	records, err := queries.ListSessions(ctx)
	if err != nil {
		return sdkprotocol.SessionListResult{}, fmt.Errorf("sdkserver: 列会话失败：%w", err)
	}
	titles, err := s.readTitles(ctx, queries, records)
	if err != nil {
		return sdkprotocol.SessionListResult{}, err
	}
	sessions := make([]sdkprotocol.SessionSummary, 0, len(records))
	for _, record := range records {
		sessions = append(sessions, summaryOf(record, titles))
	}
	return sdkprotocol.SessionListResult{Sessions: sessions}, nil
}

// SearchSessions 按一段文字跨会话找。
//
// 源: packages/api/session-controller/src/index.ts:219-228（search）
//
// 检索本身、排序、以及那个游标全在
// [github.com/snight1983/ds-harness-go/feature/sessionquery.Engine.SearchSessions]
// 后面。这条线上不给会话**内**检索：那件事的门面是查询工具，一次跨会话的检索交回
// 每条会话里最强的那一条命中就够定位了。
func (s *Server) SearchSessions(
	ctx context.Context, params sdkprotocol.SessionSearchParams,
) (sdkprotocol.SessionSearchResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionSearchResult{}, err
	}
	queries, err := s.requireQueries("session/search")
	if err != nil {
		return sdkprotocol.SessionSearchResult{}, err
	}
	page, err := queries.SearchSessions(ctx, sessionquery.SearchRequest{
		Query:  params.Query,
		Limit:  params.Limit,
		Cursor: sessionquery.SearchCursor(params.Cursor),
	})
	if err != nil {
		return sdkprotocol.SessionSearchResult{}, fmt.Errorf("sdkserver: 检索会话失败：%w", err)
	}
	records := make([]sessionquery.Record, 0, len(page.Items))
	for _, hit := range page.Items {
		records = append(records, hit.Record)
	}
	titles, err := s.readTitles(ctx, queries, records)
	if err != nil {
		return sdkprotocol.SessionSearchResult{}, err
	}
	hits := make([]sdkprotocol.SessionSearchHit, 0, len(page.Items))
	for _, hit := range page.Items {
		hits = append(hits, sdkprotocol.SessionSearchHit{
			Session: summaryOf(hit.Record, titles),
			Seq:     hit.BestMatch.Seq,
			Snippet: hit.BestMatch.Snippet,
		})
	}
	return sdkprotocol.SessionSearchResult{
		Hits:       hits,
		NextCursor: string(page.NextCursor),
	}, nil
}

// CancelSession 叫停一条会话上正在跑的那个回合。
//
// 源: packages/api/session-controller/src/control.ts（cancel）
//
// 它是异步的：这个方法返回只说明这次请求被收下了，那个回合收敛与否从
// `session.event` 那条通知流里看。
func (s *Server) CancelSession(
	ctx context.Context, params sdkprotocol.SessionCancelParams,
) (sdkprotocol.SessionCancelResult, error) {
	_ = ctx
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionCancelResult{}, err
	}
	handle, err := s.liveSession(params.SessionID)
	if err != nil {
		return sdkprotocol.SessionCancelResult{}, err
	}
	handle.Agent.Cancel(sessionlog.UserCancel{}, agent.CancelOptions{KeepInbox: params.KeepQueue})
	return sdkprotocol.SessionCancelResult{}, nil
}

// SelectModel 换一条会话下一步要用的模型。
//
// 源: packages/api/session-controller/src/commands.ts:118-154（selectModel）
//
// 先把这份选择拿去真解算一遍再装上：不解的话，一份路由不开的选择会静静地装进去，
// 到下一个回合才炸——而那时错误已经落在会话历史里了。装上的是**解算过**的那一份，
// 所以交回去的也是它。
//
// 新增: DSH 装完还会顺手 `agentDefaultModel.saveSelection(selected)`，失败只记一行。
// 这里没有那一步：那句话把一次会话级的选择写成了整份部署的默认，于是这条线上一个
// 客户端换了模型，别的每一条线、每一条会话下次都跟着变。DSH 那样做说得通，是因为
// 它的控制器服务的是一个桌面应用，"上次挑的那个模型"本来就是那份部署的默认；这条
// SDK 线不是——它的路由在握手时就定死了，一条线一份。
func (s *Server) SelectModel(
	ctx context.Context, params sdkprotocol.SessionSelectModelParams,
) (sdkprotocol.SessionSelectModelResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionSelectModelResult{}, err
	}
	if s.config.LLM == nil {
		return sdkprotocol.SessionSelectModelResult{}, fmt.Errorf(
			"sdkserver: 换模型办不了，这条线上没挂 LLM 服务")
	}
	if _, err := s.liveSession(params.SessionID); err != nil {
		return sdkprotocol.SessionSelectModelResult{}, err
	}
	selection, ok := s.selectionFor(params.SessionID)
	if !ok {
		return sdkprotocol.SessionSelectModelResult{}, fmt.Errorf(
			"sdkserver: 换模型办不了，这份部署没挂提示词注册表")
	}
	var effort llm.ReasoningEffortID
	if params.ReasoningEffort != nil {
		effort = *params.ReasoningEffort
	}
	resolved, err := s.config.LLM.ResolveCallConfig(ctx, llm.CallConfig{
		Provider:        params.Provider,
		Model:           params.Model,
		ReasoningEffort: effort,
	})
	if err != nil {
		return sdkprotocol.SessionSelectModelResult{}, fmt.Errorf(
			"sdkserver: 解不开提供方 %q 模型 %q 的调用配置：%w", params.Provider, params.Model, err)
	}
	selection.Select(agent.ModelSelection{
		Provider:        resolved.Provider,
		Model:           resolved.Model,
		ReasoningEffort: resolved.ReasoningEffort,
	})
	return sdkprotocol.SessionSelectModelResult{
		Provider:        resolved.Provider,
		Model:           resolved.Model,
		ReasoningEffort: resolved.ReasoningEffort,
	}, nil
}

// UpdateQueue 改一条会话还没跑的那些排队消息，并交回改完之后那条队。
//
// 源: packages/api/session-controller/src/commands.ts（updateQueue）
//
// 三支改动都是「找不到那条消息就是空操作」，所以交回去的那条队才是唯一的回执：
// 客户端拿它和自己以为的那条比，就知道这次改动落没落上。
func (s *Server) UpdateQueue(
	ctx context.Context, params sdkprotocol.SessionUpdateQueueParams,
) (sdkprotocol.SessionUpdateQueueResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionUpdateQueueResult{}, err
	}
	handle, err := s.liveSession(params.SessionID)
	if err != nil {
		return sdkprotocol.SessionUpdateQueueResult{}, err
	}
	if err := s.assertLiveAgent(handle, params.SessionID); err != nil {
		return sdkprotocol.SessionUpdateQueueResult{}, err
	}

	switch params.Op {
	case sdkprotocol.QueueRemove:
		if params.MessageID == "" {
			return sdkprotocol.SessionUpdateQueueResult{}, fmt.Errorf(
				"sdkserver: %s 要一个消息标识", sdkprotocol.QueueRemove)
		}
		handle.Agent.Remove(params.MessageID)
	case sdkprotocol.QueueReplace, sdkprotocol.QueuePrepend:
		if params.Op == sdkprotocol.QueueReplace && params.MessageID == "" {
			return sdkprotocol.SessionUpdateQueueResult{}, fmt.Errorf(
				"sdkserver: %s 要一个消息标识", sdkprotocol.QueueReplace)
		}
		content, err := s.durablePromptContent(ctx, params.ContentBlocks)
		if err != nil {
			return sdkprotocol.SessionUpdateQueueResult{}, err
		}
		if len(content) == 0 {
			return sdkprotocol.SessionUpdateQueueResult{}, fmt.Errorf(
				"sdkserver: %s 要一份非空的内容", params.Op)
		}
		// 附件准入是一道会等 I/O 的边，过了它要再验一遍这个 agent 还活着，
		// 理由和 [Server.Prompt] 里那次逐字相同。
		if err := s.assertLiveAgent(handle, params.SessionID); err != nil {
			return sdkprotocol.SessionUpdateQueueResult{}, err
		}
		message := llm.NewUserMessage(content, llm.UserSource{})
		if params.Op == sdkprotocol.QueueReplace {
			handle.Agent.Replace(params.MessageID, message)
		} else {
			handle.Agent.Prepend(message, agent.NextTurn)
		}
	default:
		return sdkprotocol.SessionUpdateQueueResult{}, fmt.Errorf(
			"sdkserver: 认不出的排队改动 %q", params.Op)
	}

	inbox := handle.Agent.Inbox()
	return sdkprotocol.SessionUpdateQueueResult{
		NextTurn: messageIDs(inbox.NextTurn()),
		NextStep: messageIDs(inbox.NextStep()),
	}, nil
}

// SessionHistory 往回翻一页历史。
//
// 源: packages/api/session-controller/src/history.ts:52-79（page）
//
// 这是一次**冷读**：不惊动任何 agent，也不因为翻了一页就把一条落地的会话拉活。
func (s *Server) SessionHistory(
	ctx context.Context, params sdkprotocol.SessionHistoryParams,
) (sdkprotocol.SessionHistoryResult, error) {
	if err := s.requireReady(); err != nil {
		return sdkprotocol.SessionHistoryResult{}, err
	}
	queries, err := s.requireQueries("session/history")
	if err != nil {
		return sdkprotocol.SessionHistoryResult{}, err
	}
	if params.SessionID == "" {
		return sdkprotocol.SessionHistoryResult{}, fmt.Errorf("sdkserver: session/history 要一个会话标识")
	}
	if params.MaxMessages < 0 {
		return sdkprotocol.SessionHistoryResult{}, fmt.Errorf(
			"sdkserver: session/history 的 maxMessages 不能是负数，给的是 %d", params.MaxMessages)
	}
	maxMessages := params.MaxMessages
	if maxMessages == 0 {
		maxMessages = DefaultHistoryMaxMessages
	}
	snapshot, err := queries.ReadSession(ctx, sessionlog.SessionID(params.SessionID))
	if err != nil {
		return sdkprotocol.SessionHistoryResult{}, fmt.Errorf(
			"sdkserver: 读不回会话 %s：%w", params.SessionID, err)
	}
	events, hasMore := paginateHistory(snapshot.Events, params.BeforeSeq, maxMessages)
	return sdkprotocol.SessionHistoryResult{
		Session: snapshot.Session,
		Events:  events,
		HasMore: hasMore,
	}, nil
}

// paginateHistory 从这一页的尾巴往回数满 maxMessages 条消息，切出那一段。
//
// 源: packages/api/session-controller/src/history.ts:290-314（paginate）
//
// 数的只有人和模型那两种消息，而且只数**追加到表面末尾**的那些——一次替换写的是
// 已经在表面上的那一段，把它也数一遍会让这一页装的消息比说好的少。数满之后切点落在
// 那条消息所属那一组的开头（它自己的 seq 和它引用的那些 seq 里最小的一个），所以
// 一页永远不会把一条消息劈成两半，也不会把装配出它的那些流式分块甩到上一页去。
//
// 新增: DSH 通篇拿 seq 当下标使，因为它那边日志从 0 起、一条不删。本仓库的日志会
// 从最老的一头被弹掉一截，所以这里全程用下标，只在和 beforeSeq、SourceEventSeqs
// 打交道时换算一次。
func paginateHistory(
	events []sessionlog.Event, beforeSeq *int, maxMessages int,
) ([]sessionlog.Event, bool) {
	if len(events) == 0 {
		return []sessionlog.Event{}, false
	}
	base := events[0].Seq
	end := len(events)
	if beforeSeq != nil {
		end = min(end, max(0, *beforeSeq-base))
	}

	count := 0
	cut := 0
	for index := end - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != sessionlog.EventUserMessage && event.Type != sessionlog.EventAssistantMessage {
			continue
		}
		if !sessionlog.IsAppendSurfaceEvent(event) {
			continue
		}
		count++
		groupStart := event.Seq
		for _, source := range event.SourceEventSeqs {
			groupStart = min(groupStart, source)
		}
		if count >= maxMessages {
			cut = max(0, groupStart-base)
			break
		}
	}
	return slices.Clone(events[cut:end]), cut > 0
}

// requireReady 挡住握手之前进来的每一件事。
//
// 源: packages/sdk/server/src/server.ts:177
//
// 连纯读的那几件也挡：一个还没握手的客户端根本还不在这条线上，让它读得到东西
// 等于让这条线的边界取决于方法名。
func (s *Server) requireReady() error {
	s.mutex.Lock()
	ready := s.initialized
	s.mutex.Unlock()
	if !ready {
		return fmt.Errorf("sdkserver: 这台 SDK 服务器还没握手")
	}
	return nil
}

// requireQueries 交出查询引擎，没挂就说清是哪一条路因此办不了。
func (s *Server) requireQueries(method string) (*sessionquery.Engine, error) {
	if s.config.Queries == nil {
		return nil, fmt.Errorf("sdkserver: %s 办不了，这份部署没挂会话查询引擎", method)
	}
	return s.config.Queries, nil
}

// liveSession 交出这条线上那个会话的句柄。
//
// 新增: DSH 的 `resolveAgent` 找不到活的就当场把它 resume 起来。这里不这么做：
// 叫停、改队、换模型、改名这四件事都是对一个**正在被推进**的会话说的，而一次隐式的
// resume 会把一条落地的会话拉活、起一整套 agent，只为了执行一件对它无事可做的操作。
// 要接着跑就显式 `session/resume`——那条路本来就在。
func (s *Server) liveSession(sessionID string) (agent.Handle, error) {
	if sessionID == "" {
		return agent.Handle{}, fmt.Errorf("sdkserver: 要一个会话标识")
	}
	s.mutex.Lock()
	handle, live := s.sessions[sessionID]
	s.mutex.Unlock()
	if !live {
		return agent.Handle{}, fmt.Errorf("sdkserver: 这条线上没有活着的会话 %s", sessionID)
	}
	return handle, nil
}

// selectionFor 交出这条会话那份模型选择；第二个返回值为假表示没挂提示词注册表。
func (s *Server) selectionFor(sessionID string) (*agent.ModelSelectionRef, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	selection, ok := s.selections[sessionID]
	return selection, ok
}

// newSession 走「这个标识必须还没被这条线占着」那条创建路。
//
// 和 [Server.getOrCreateSession] 的分别只在开头一句：那条路查到已有的就交出去，
// 这条路当场拒。接着跑和分出来都不该悄悄地变成「你已经有一个了」。
func (s *Server) newSession(
	ctx context.Context,
	sessionID string,
	make func(context.Context, *scope.Scope, route, agent.Setup) (agent.Handle, error),
) (agent.Handle, error) {
	s.mutex.Lock()
	switch {
	case s.owner == nil:
		s.mutex.Unlock()
		return agent.Handle{}, fmt.Errorf("sdkserver: 这台 SDK 服务器还没装上")
	case s.shuttingDown:
		s.mutex.Unlock()
		return agent.Handle{}, fmt.Errorf("sdkserver: 这台 SDK 服务器正在收摊")
	}
	if _, live := s.sessions[sessionID]; live {
		s.mutex.Unlock()
		return agent.Handle{}, fmt.Errorf("sdkserver: 这条线上已经有一个会话叫 %s 了", sessionID)
	}
	// 上膛和查那个开关必须在同一段临界区里，理由见 [Server.getOrCreateSession]。
	s.pending.Add(1)
	s.mutex.Unlock()
	defer s.pending.Done()

	created, err, _ := s.creations.Do(sessionID, func() (any, error) {
		return s.adopt(ctx, sessionID, make)
	})
	if err != nil {
		return agent.Handle{}, err
	}
	return created.(agent.Handle), nil
}

// readTitles 把这批会话的标题一次折出来，按会话标识索引。
//
// 单个会话的标题折不出来时那一条被跳过（于是它在列表上是「没有标题」），而不是让
// 整次列出失败：一条坏了的标题事件不该把别的每一条会话也从列表上抹掉。
func (s *Server) readTitles(
	ctx context.Context, queries *sessionquery.Engine, records []sessionquery.Record,
) (map[sessionlog.SessionID]sessionquery.TitleObservation, error) {
	if len(records) == 0 {
		return nil, nil
	}
	ids := make([]sessionlog.SessionID, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Header.ID)
	}
	results, err := queries.ReadTitleSnapshots(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("sdkserver: 读会话标题失败：%w", err)
	}
	titles := make(map[sessionlog.SessionID]sessionquery.TitleObservation, len(results))
	for _, result := range results {
		if result.Err != nil {
			continue
		}
		titles[result.SessionID] = result.Value
	}
	return titles, nil
}

// summaryOf 把一条会话记录折成线上那一行。
func summaryOf(
	record sessionquery.Record, titles map[sessionlog.SessionID]sessionquery.TitleObservation,
) sdkprotocol.SessionSummary {
	summary := sdkprotocol.SessionSummary{
		SessionID:     string(record.Header.ID),
		CreatedAt:     record.Header.CreatedAt,
		ParentSession: string(record.Header.ParentSession),
		Live:          record.Live,
		Persisted:     record.Persisted,
	}
	if observed, ok := titles[record.Header.ID]; ok && observed.Titled {
		summary.Title = observed.Title.Title
		summary.Titled = true
	}
	return summary
}

// messageIDs 取一队消息各自的身份，按队里的次序。
func messageIDs(messages []llm.Message) []llm.MessageID {
	ids := make([]llm.MessageID, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

// newSessionID 起一个会话标识。
//
// 源: packages/api/session-controller/src/commands.ts:239（`session-${randomUUID()}`）
func newSessionID() string { return "session-" + uuid.NewString() }

// handleSessionRequest 派会话那一套方法；第二个返回值为假表示这个方法名不归它管。
//
// 它和 [Server.HandleRequest] 分开写，是因为那十个 case 长得一模一样（解参数、
// 转调），混在那三个握手期方法中间会把后者淹掉。
func (s *Server) handleSessionRequest(
	ctx context.Context, method string, params json.RawMessage,
) (any, bool, error) {
	switch method {
	case sdkprotocol.MethodSessionCreate:
		return decodeAnd(ctx, method, params, s.CreateSession)
	case sdkprotocol.MethodSessionResume:
		return decodeAnd(ctx, method, params, s.ResumeSession)
	case sdkprotocol.MethodSessionFork:
		return decodeAnd(ctx, method, params, s.ForkSession)
	case sdkprotocol.MethodSessionRename:
		return decodeAnd(ctx, method, params, s.RenameSession)
	case sdkprotocol.MethodSessionList:
		return decodeAnd(ctx, method, params, s.ListSessions)
	case sdkprotocol.MethodSessionSearch:
		return decodeAnd(ctx, method, params, s.SearchSessions)
	case sdkprotocol.MethodSessionCancel:
		return decodeAnd(ctx, method, params, s.CancelSession)
	case sdkprotocol.MethodSessionSelectModel:
		return decodeAnd(ctx, method, params, s.SelectModel)
	case sdkprotocol.MethodSessionUpdateQueue:
		return decodeAnd(ctx, method, params, s.UpdateQueue)
	case sdkprotocol.MethodSessionHistory:
		return decodeAnd(ctx, method, params, s.SessionHistory)
	default:
		return nil, false, nil
	}
}

// decodeAnd 解一次入参再转调，两步的失败说法在十条路上因此完全一致。
//
// 入参缺席（JSON-RPC 允许不带 params）当空对象看：[sdkprotocol.SessionListParams]
// 本来就是空的，要求客户端为它显式写一个 `{}` 只是无谓的严格。
func decodeAnd[P any, R any](
	ctx context.Context,
	method string,
	params json.RawMessage,
	handle func(context.Context, P) (R, error),
) (any, bool, error) {
	var decoded P
	if len(params) > 0 {
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, true, fmt.Errorf("sdkserver: %s 的入参解不动：%w", method, err)
		}
	}
	result, err := handle(ctx, decoded)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}
