// 本文件的作用：会话那一套方法的方法名和形状——开一条、接着跑一条、从一条分出来、
// 改名、列出来、找、叫停、换模型、改排队、翻历史。
//
// 源: packages/api/session-controller/src/types.ts, packages/api/session-controller/src/index.ts:208-374
//
// 新增: DSH 那一侧这十件事走的是 `api/gateway` 那条 `@Remote` 通道，不是这条 SDK 线
// （理由见 docs/portmap/decisions.md 的 api/* 一节：那条通道没有客户端，本仓库不做）。
// 所以这里取的是**方法表的形状**——每一件事要哪些入参、交回什么——把它们落在这条
// 已经有客户端的线上，而不是再开一条通道。

package sdkprotocol

import (
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// 这十个是会话那一套方法的方法名。
//
// 源: packages/api/session-controller/src/index.ts:208-374（那一串 @Remote 名字）
//
// 名字一律带 `session/` 前缀，和已有的 [MethodSessionPrompt] 排在一起；DSH 那边的
// `@Remote('list')` 之类没有前缀，因为它的命名空间由 `ctx.sessionController` 这个
// 服务名承担，而 JSON-RPC 的方法名是平的。
const (
	// MethodSessionCreate 开一条会话，不带任何输入。
	MethodSessionCreate = "session/create"
	// MethodSessionResume 把一条落地的会话读回来接着跑。
	MethodSessionResume = "session/resume"
	// MethodSessionFork 从一条会话某个已经收尾的回合处分出一条新的。
	MethodSessionFork = "session/fork"
	// MethodSessionRename 给一条会话按一个人给的标题改名。
	MethodSessionRename = "session/rename"
	// MethodSessionList 列出这套运行时看得见的每一条会话。
	MethodSessionList = "session/list"
	// MethodSessionSearch 按一段文字跨会话找。
	MethodSessionSearch = "session/search"
	// MethodSessionCancel 叫停一条会话上正在跑的那个回合。
	MethodSessionCancel = "session/cancel"
	// MethodSessionSelectModel 换一条会话下一步要用的模型。
	MethodSessionSelectModel = "session/select-model"
	// MethodSessionUpdateQueue 改一条会话还没跑的那些排队消息。
	MethodSessionUpdateQueue = "session/update-queue"
	// MethodSessionHistory 往回翻一页历史。
	MethodSessionHistory = "session/history"
)

// SessionCreateParams 是开一条会话的入参。
//
// 源: packages/api/session-controller/src/types.ts（SessionCreateRequest）
type SessionCreateParams struct {
	// SessionID 是要用的会话标识；空串表示由服务端起一个。
	//
	// 新增: DSH 那边一律由服务端 `randomUUID()` 起名，客户端说不上话。这条线不一样：
	// [SessionPromptParams.SessionID] 从一开始就是客户端给的（一个没见过的标识当场
	// 把会话建出来），所以这里让客户端接着说得上话，同时留出「你替我起一个」那一支。
	SessionID string `json:"sessionId,omitempty"`
}

// SessionCreateResult 交回这条会话的标识。
type SessionCreateResult struct {
	// SessionID 是建出来的那条会话。
	SessionID string `json:"sessionId"`
}

// SessionResumeParams 是把一条落地会话读回来接着跑的入参。
//
// 源: packages/api/session-controller/src/commands.ts（resume 那条路的 resolveAgent）
type SessionResumeParams struct {
	// SessionID 是要接着跑的那条落地会话。
	SessionID string `json:"sessionId"`
}

// SessionResumeResult 交回接着跑起来的那条会话的标识。
type SessionResumeResult struct {
	// SessionID 是活起来的那条会话；它等于入参里那一个。
	SessionID string `json:"sessionId"`
}

// SessionForkParams 是从一条会话分出一条新的的入参。
//
// 源: packages/api/session-controller/src/commands.ts:187-275（fork）
type SessionForkParams struct {
	// SessionID 是分叉来源。
	SessionID string `json:"sessionId"`
	// AtSeq 是想在哪条事件处分开；分叉点会被推到**包住它的那个回合收尾之后**。
	//
	// 指针的理由：没给这个字段表示「从最后一个收了尾的回合分」，而给 0 表示
	// 「从包住第 0 条事件的那个回合分」，两者是两件事。
	AtSeq *int `json:"atSeq,omitempty"`
	// ForkSessionID 是新会话要用的标识；空串表示由服务端起一个。
	ForkSessionID string `json:"forkSessionId,omitempty"`
}

// SessionForkResult 交回分出来那条会话的标识。
type SessionForkResult struct {
	// SessionID 是分出来的那条新会话。
	SessionID string `json:"sessionId"`
}

// SessionRenameParams 是按一个人给的标题改名的入参。
//
// 源: packages/api/session-controller/src/commands.ts:156-186（rename）
type SessionRenameParams struct {
	// SessionID 是要改名的会话。
	SessionID string `json:"sessionId"`
	// Title 是人给的那个标题。
	Title string `json:"title"`
}

// SessionRenameResult 交回落下去之后的那个标题。
type SessionRenameResult struct {
	// Title 是归一化之后真正落进日志的那个标题。
	Title string `json:"title"`
	// EventSeq 是记下这次改名的那条事件的 seq。
	EventSeq int `json:"eventSeq"`
	// UpdatedAt 是那条事件的时刻，Unix 纪元毫秒。
	UpdatedAt int64 `json:"updatedAt"`
}

// SessionListParams 是列出会话的入参。
//
// 源: packages/api/session-controller/src/list.ts
//
// 新增: 是个空对象。DSH 那边这条路带工作区过滤和一份排序口径，两样在这条线上都
// 落不下来——工作区在这里只是握手那条 cwd 换出来的一个标识（见
// [github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver.WorkspaceLookup]），
// 而排序由引擎那侧定死（同一份语料每次列出来的顺序都一样）。留一个空结构体而不是
// 干脆不收参数，是为了以后加字段时不必改方法签名。
type SessionListParams struct{}

// SessionListResult 是列出来的那些会话。
type SessionListResult struct {
	// Sessions 是每一条会话的摘要，顺序由引擎定死。
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary 是一条会话在列表上的那一行。
//
// 源: packages/api/session-controller/src/types.ts（SessionListEntry）
type SessionSummary struct {
	// SessionID 是这条会话的标识。
	SessionID string `json:"sessionId"`
	// Title 是它此刻的标题；没有过标题时是空串，靠 Titled 分开。
	Title string `json:"title,omitempty"`
	// Titled 说这条会话有没有过标题。
	//
	// 新增: 一个空标题和一个没有标题在界面上是两件事，见
	// [github.com/snight1983/ds-harness-go/feature/sessionquery.TitleObservation]。
	Titled bool `json:"titled"`
	// CreatedAt 是建这条会话的时刻，Unix 纪元毫秒。
	CreatedAt int64 `json:"createdAt"`
	// ParentSession 是它分叉自哪条会话；空串表示不是分出来的。
	ParentSession string `json:"parentSession,omitempty"`
	// Live 表示它此刻在活会话表里。
	Live bool `json:"live"`
	// Persisted 表示它此刻在持久化后端里落着。
	//
	// Live 和 Persisted 可以同时为真：一条正被推进、同时也已经落了一段的会话就是。
	Persisted bool `json:"persisted"`
}

// SessionSearchParams 是跨会话找的入参。
//
// 源: packages/api/session-controller/src/index.ts:219-228（search）
type SessionSearchParams struct {
	// Query 是要找的那段文字，一律当数据看，永远不当可执行的检索语法。
	Query string `json:"query"`
	// Limit 是这一页最多几条；0 表示交给后端定。
	Limit int `json:"limit,omitempty"`
	// Cursor 是上一页交回来的续页令牌。
	Cursor string `json:"cursor,omitempty"`
}

// SessionSearchResult 是一页检索结果。
type SessionSearchResult struct {
	// Hits 是这一页命中的那些会话，已经排好。
	Hits []SessionSearchHit `json:"hits"`
	// NextCursor 是续页令牌；空串表示这是最后一页。
	NextCursor string `json:"nextCursor,omitempty"`
}

// SessionSearchHit 是一条命中的会话，带它里面最强的那一条命中。
//
// 源: packages/session-query/session-query/src/types.ts（SessionSearchHit）
type SessionSearchHit struct {
	// Session 是命中的那条会话的摘要。
	Session SessionSummary `json:"session"`
	// Seq 是最强那条命中事件在会话里的序号。
	Seq int `json:"seq"`
	// Snippet 是那条事件里给人看的一小段。
	Snippet string `json:"snippet"`
}

// SessionCancelParams 是叫停一个回合的入参。
//
// 源: packages/api/session-controller/src/control.ts（cancel）
type SessionCancelParams struct {
	// SessionID 是要叫停的会话。
	SessionID string `json:"sessionId"`
	// KeepQueue 为真表示别清掉还没跑的排队消息，只中止正在跑的那一段。
	KeepQueue bool `json:"keepQueue,omitempty"`
}

// SessionCancelResult 是叫停的回执。
//
// 空对象：叫停是异步的，它只说明这次请求被收下了，不说明那个回合已经收敛。
type SessionCancelResult struct{}

// SessionSelectModelParams 是换模型的入参。
//
// 源: packages/api/session-controller/src/commands.ts:118-154（selectModel）
type SessionSelectModelParams struct {
	// SessionID 是要换模型的会话。
	SessionID string `json:"sessionId"`
	// Provider 是登记过的那个提供方路由键。
	Provider string `json:"provider"`
	// Model 是那个提供方自己拥有的模型标识。
	Model string `json:"model"`
	// ReasoningEffort 是可选的推理档位；没给这个字段表示不选档位。
	ReasoningEffort *llm.ReasoningEffortID `json:"reasoningEffort,omitempty"`
}

// SessionSelectModelResult 交回真正生效的那一份选择。
//
// 它是**解算过**的那一份，不是入参原样：适配器可能把自己的默认落实进去。
type SessionSelectModelResult struct {
	// Provider 是生效的提供方。
	Provider string `json:"provider"`
	// Model 是生效的模型。
	Model string `json:"model"`
	// ReasoningEffort 是生效的推理档位；空串表示没选。
	ReasoningEffort llm.ReasoningEffortID `json:"reasoningEffort,omitempty"`
}

// QueueOp 是对排队消息的一次改动的种类。
//
// 源: packages/api/session-controller/src/commands.ts（updateQueue）
//
// 新增: DSH 那边是三个各自独立的字段，靠「哪个给了」判别。Go 里做成一个显式的标签，
// 因为「三个里恰好给一个」这条约束在 JSON 上没人守得住，而一个认不出的标签当场就红。
type QueueOp string

const (
	// QueueRemove 把一条还没跑的消息从队里拿掉。
	QueueRemove QueueOp = "remove"
	// QueueReplace 原地换掉一条还没跑的消息，位置不变。
	QueueReplace QueueOp = "replace"
	// QueuePrepend 把一条消息放到队头。
	QueuePrepend QueueOp = "prepend"
)

// SessionUpdateQueueParams 是改排队消息的入参。
type SessionUpdateQueueParams struct {
	// SessionID 是要改队的会话。
	SessionID string `json:"sessionId"`
	// Op 是这次改动的种类。
	Op QueueOp `json:"op"`
	// MessageID 是被改的那条消息；[QueuePrepend] 那一支不看它。
	MessageID llm.MessageID `json:"messageId,omitempty"`
	// ContentBlocks 是新内容，[QueueReplace] 和 [QueuePrepend] 两支要它。
	//
	// 形状和 [SessionPromptParams.ContentBlocks] 完全一样，内联图片走同一次准入。
	ContentBlocks PromptContent `json:"contentBlocks,omitempty"`
}

// SessionUpdateQueueResult 交回改完之后那条队。
type SessionUpdateQueueResult struct {
	// NextTurn 是排着队等各自回合的那些消息的身份，按队里的次序。
	NextTurn []llm.MessageID `json:"nextTurn"`
	// NextStep 是等下一个步骤边界的那些消息的身份，按队里的次序。
	NextStep []llm.MessageID `json:"nextStep"`
}

// SessionHistoryParams 是往回翻一页历史的入参。
//
// 源: packages/api/session-controller/src/history.ts:52-79（page）
type SessionHistoryParams struct {
	// SessionID 是要翻的会话。
	SessionID string `json:"sessionId"`
	// BeforeSeq 是「翻这一条**之前**的那一页」；没给表示从最新那头翻起。
	//
	// 指针的理由和 [SessionForkParams.AtSeq] 一样：没给和给 0 是两件事。
	BeforeSeq *int `json:"beforeSeq,omitempty"`
	// MaxMessages 是这一页最多装几条人和模型的消息；0 表示用服务端的缺省。
	//
	// 页是按**消息**切的不是按事件切的，见 [SessionHistoryResult]。
	MaxMessages int `json:"maxMessages,omitempty"`
}

// SessionHistoryResult 是一页历史。
//
// 源: packages/api/session-controller/src/history.ts:290-314（paginate）
//
// 切页的口径是**消息对齐**的：从这一页的尾巴往回数满 MaxMessages 条人或模型的消息，
// 停在那条消息所属的那一组的开头。所以一页永远不会把一条消息劈成两半，也不会把
// 装配出那条消息的那些流式分块甩到上一页去。
type SessionHistoryResult struct {
	// Session 是和 Events 出自同一次观察的会话头。
	Session sessionlog.SessionHeader `json:"session"`
	// Events 是这一页的事件，按 seq 升序，连续无洞。
	Events []sessionlog.Event `json:"events"`
	// HasMore 为真表示这一页前面还有。
	HasMore bool `json:"hasMore"`
}
