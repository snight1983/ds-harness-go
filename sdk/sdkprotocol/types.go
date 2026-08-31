// 本文件的作用：这条线上那三对请求/结果、四种通知的确切形状，以及它们各自的方法名。
//
// 源: packages/sdk/protocol/src/types.ts

package sdkprotocol

import (
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/subagent/subagent"
)

// ServerName 是线上稳定的服务端身份，握手时原样交回去。
//
// 源: packages/sdk/server/src/server.ts:124
//
// 它是**协议的一部分**：客户端拿它认这条线的对面是谁，改一个字就是换协议。
const ServerName = "deepseek-harness-sdk-runtime"

// 这四个是服务端往客户端发的通知的方法名。
//
// 源: packages/sdk/protocol/src/types.ts:93-98
//
// 新增: DSH 那边是 HarnessSdkNotificationMap，一个「方法名 → 负载类型」的类型级映射，
// 用来在 notify 的调用点上把两者对齐。Go 没有那种映射，也不需要——把方法名写成常量、
// 让每个负载类型自己带一句「我配哪个方法名」，得到的是同一份对齐，而且这份对齐在
// 运行期也读得到。
const (
	// MethodSessionEvent 是一条会话日志事件，边记边发。
	MethodSessionEvent = "session.event"
	// MethodSessionStatus 是一个会话上那个活 agent 的整体状态变了。
	MethodSessionStatus = "session.status"
	// MethodSubagentStarted 是运行时内部新开了一个子会话。
	MethodSubagentStarted = "subagent.started"
	// MethodSubagentFinished 是一次进程内的子 agent 跑完了。
	MethodSubagentFinished = "subagent.finished"
)

// 这三个是客户端往服务端发的请求的方法名。
//
// 源: packages/sdk/protocol/src/types.ts:101-105
const (
	// MethodInitialize 是进程级的一次握手。
	MethodInitialize = "initialize"
	// MethodSessionPrompt 是一个会话上的一轮用户输入。
	MethodSessionPrompt = "session/prompt"
	// MethodShutdown 是收摊。
	MethodShutdown = "shutdown"
)

// InitializeParams 是进程级那次握手的入参。
//
// 源: packages/sdk/protocol/src/types.ts:16-25
type InitializeParams struct {
	// Cwd 记在每一个 SDK 建出来的会话头上。
	Cwd string `json:"cwd"`
	// Provider 是 SDK 建出来的每个 agent 跑在哪条提供方路线上。
	Provider string `json:"provider"`
	// Model 是 SDK 建出来的每个 agent 跑哪个模型。
	Model string `json:"model"`
	// MaxTokens 是可选的输出上限，SDK 建出来的 agent 和它们进程内的后代都继承它。
	//
	// 指针是必需的：「没给这个字段」和「给了 0」在这里是两回事，后者是坏输入
	// （[HarnessSdkJsonRpcServer.Initialize] 当场拒），前者是常态。
	MaxTokens *int `json:"maxTokens,omitempty"`
}

// InitializeResult 是握手交回去的那份线上稳定的身份。
//
// 源: packages/sdk/protocol/src/types.ts:28-31
type InitializeResult struct {
	// ServerInfo 是服务端的名字和版本。
	ServerInfo ServerInfo `json:"serverInfo"`
}

// ServerInfo 是服务端的名字和版本。
//
// 源: packages/sdk/protocol/src/types.ts:30
//
// 新增: DSH 那边是一个写在 InitializeResult 里的匿名对象字面量。Go 里给它一个名字，
// 因为一个匿名结构体在跨包的构造点上没法写出来。
type ServerInfo struct {
	// Name 是线上稳定的服务端身份，见 [ServerName]。
	Name string `json:"name"`
	// Version 是服务端版本。
	Version string `json:"version"`
}

// SessionPromptParams 是一个 SDK 会话上的一轮用户输入。
//
// 源: packages/sdk/protocol/src/types.ts:34-39
type SessionPromptParams struct {
	// SessionID 是 SDK 那侧的会话标识；一个没见过的标识会当场把 agent 和会话一起建出来。
	SessionID string `json:"sessionId"`
	// ContentBlocks 是这一轮的内容块，原样成为那条用户消息。
	ContentBlocks llm.Content `json:"contentBlocks"`
}

// SessionPromptResult 是一轮输入的入队回执。
//
// 源: packages/sdk/protocol/src/types.ts:42-45
//
// 它只说明「这条消息已经进了队列、身份是这个」，**不**说明这一轮跑出了什么——
// 之后的动静从 [MethodSessionEvent] 那条通知流里看。
type SessionPromptResult struct {
	// MessageID 是那条排上队的用户消息的身份。
	MessageID llm.MessageID `json:"messageId"`
}

// RunStatus 是按部署口径映射出来的 SDK 结果：接受了是 ok，别的都是 error。
//
// 源: packages/sdk/protocol/src/types.ts:48
type RunStatus string

const (
	// RunOK 表示这次跑出的结果被接受。
	RunOK RunStatus = "ok"
	// RunError 表示这次跑没跑出可接受的结果。
	RunError RunStatus = "error"
)

// AgentStatus 是一个会话上那个活 agent 的整体状态。
//
// 源: packages/sdk/protocol/src/types.ts:63
//
// 新增: DSH 那边是内联的字面量联合 `'idle' | 'running'`。Go 里给它一个具名类型，
// 好让服务端那边的转换有个落点。
type AgentStatus string

const (
	// AgentIdle 表示这个 agent 此刻没在跑。
	AgentIdle AgentStatus = "idle"
	// AgentRunning 表示这个 agent 此刻在跑。
	AgentRunning AgentStatus = "running"
)

// SessionEventNotification 是 [MethodSessionEvent] 的负载：一条会话日志事件。
//
// 源: packages/sdk/protocol/src/types.ts:51-56
//
// 它覆盖运行时里的**每一个**会话，不只是 SDK 建出来的那些。
type SessionEventNotification struct {
	// SessionID 是这条事件属于哪个会话。
	SessionID string `json:"sessionId"`
	// Event 是完整的那个事件信封。
	Event session.Event `json:"event"`
}

// SessionStatusNotification 是 [MethodSessionStatus] 的负载。
//
// 源: packages/sdk/protocol/src/types.ts:59-64
type SessionStatusNotification struct {
	// SessionID 是哪个会话上的活 agent 变了状态。
	SessionID string `json:"sessionId"`
	// Status 是转换之后的整体状态。
	Status AgentStatus `json:"status"`
}

// SubagentStartedNotification 是 [MethodSubagentStarted] 的负载：运行时内部建了一个子会话。
//
// 源: packages/sdk/protocol/src/types.ts:67-72
type SubagentStartedNotification struct {
	// ParentSessionID 是派活的那个会话。
	ParentSessionID string `json:"parentSessionId"`
	// ChildSessionID 是新建的那个子会话。
	ChildSessionID string `json:"childSessionId"`
}

// SubagentFinishedNotification 是 [MethodSubagentFinished] 的负载：一次**进程内**的
// 子 agent 跑完了。
//
// 源: packages/sdk/protocol/src/types.ts:75-90
//
// 远端跑的那些一律不报：这条协议只说得清自己这个进程里发生的事。
type SubagentFinishedNotification struct {
	// Provider 是跑这个孩子的子 agent 提供方名字。
	Provider string `json:"provider"`
	// AgentID 是这个孩子的 agent 标识；本地跑的时候它等于 ChildSessionID。
	AgentID string `json:"agentId"`
	// ParentSessionID 是派活的那个会话。
	ParentSessionID string `json:"parentSessionId"`
	// ChildSessionID 是那个子会话。
	ChildSessionID string `json:"childSessionId"`
	// Status 是按部署口径映射出来的结果。
	Status RunStatus `json:"status"`
	// StopReason 是提供方报上来的停止原因。
	StopReason subagent.StopReason `json:"stopReason"`
	// LastAssistantMessage 是这个孩子挑出来的那段助手输出；它一个字都没产出时为 nil。
	//
	// nil 和长度为零的切片必须分得开：DSH 那边前者是「这个字段根本没出现」，
	// 后者是「跑出来了但内容是空的」，`omitempty` 把这一条原样带过来。
	LastAssistantMessage llm.Content `json:"lastAssistantMessage,omitempty"`
}
