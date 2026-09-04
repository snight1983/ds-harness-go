// 本文件的作用：这条线上那三对请求/结果、四种通知的确切形状，以及它们各自的方法名。
//
// 源: packages/sdk/protocol/src/types.ts

package sdkprotocol

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
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
// 源: packages/sdk/protocol/src/types.ts:16-27
type InitializeParams struct {
	// Cwd 是**客户端那台机器**上的一条工作目录。
	//
	// 新增: DSH 把它原样记进每一个 SDK 建出来的会话头。本仓库不：它在服务端只被
	// 换成一个工作区标识，换完就到此为止，见
	// [github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver.WorkspaceLookup]。
	Cwd string `json:"cwd"`
	// Provider 是 SDK 建出来的每个 agent 跑在哪条提供方路线上。
	Provider string `json:"provider"`
	// Model 是 SDK 建出来的每个 agent 跑哪个模型。
	Model string `json:"model"`
	// ReasoningEffort 是这条 provider/model 路由上可选的推理档位，SDK 建出来的
	// 每个 agent 都照它跑。
	//
	// 源: packages/sdk/protocol/src/types.ts:23-24
	//
	// 指针的理由和下面 MaxTokens 那条一样：DSH 拿「给了空串」当坏输入当场拒
	// （server.ts:136-139），而运行时内部「空串即没选」——两者只有靠「这个字段
	// 出现过没有」才分得开。收进一个具名字符串就把前者悄悄变成了后者。
	ReasoningEffort *llm.ReasoningEffortID `json:"reasoningEffort,omitempty"`
	// MaxTokens 是可选的输出上限，SDK 建出来的 agent 和它们进程内的后代都继承它。
	//
	// 指针是必需的：「没给这个字段」和「给了 0」在这里是两回事，后者是坏输入
	// （[HarnessSdkJsonRpcServer.Initialize] 当场拒），前者是常态。
	MaxTokens *int `json:"maxTokens,omitempty"`
}

// InitializeResult 是握手交回去的那份线上稳定的身份。
//
// 源: packages/sdk/protocol/src/types.ts:29-33（InitializeResult）
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
// 源: packages/sdk/protocol/src/types.ts:35-41（SessionPromptParams）
type SessionPromptParams struct {
	// SessionID 是 SDK 那侧的会话标识；一个没见过的标识会当场把 agent 和会话一起建出来。
	SessionID string `json:"sessionId"`
	// ContentBlocks 是这一轮的内容块。已经耐久的那些原样成为用户消息，内联图片
	// 先走一次附件准入，见 [PromptContent]。
	ContentBlocks PromptContent `json:"contentBlocks"`
}

// EncodedImageBlock 是一张跟着请求内联送进来、还没进附件库的栅格图。
//
// 源: packages/sdk/protocol/src/types.ts:43-50（SdkEncodedImageBlock）
//
// 它**只**存在于线上这一侧：服务端在把这一轮交给 agent 之前把它准入成一条
// [llm.ImageBlock]，会话日志里落下的永远是那条引用，不是这些字节。
type EncodedImageBlock struct {
	// Data 是栅格字节的规范 base64 编码。
	Data string `json:"data"`
	// MimeType 是调用方声称的栅格媒体类型，准入时拿解码后的字节核对。
	//
	// 线上的字段名是 mimeType 而不是 mediaType：DSH 这么写的，改一个字两侧就读不通。
	MimeType attachment.MediaType `json:"mimeType"`
}

// PromptBlock 是一轮 SDK 输入里的一块：要么已经耐久，要么是一张等着准入的内联图。
//
// 源: packages/sdk/protocol/src/types.ts:52-53（SdkPromptContentBlock）
//
// 新增: DSH 是 `ContentBlock | SdkEncodedImageBlock` 一个联合，判别式是那个
// 类型守卫——`type === 'image' && 'data' in block`（server.ts:35-37）。注意它**不是**
// 光看 type：两支的 type 都是 `image`，分开它们的是带 data 还是带 attachment。
// Go 没有联合，所以做成一个恰好带一支的结构体。
type PromptBlock struct {
	// Durable 是一个已经耐久的内容块；Encoded 为 nil 时它有效。
	Durable llm.ContentBlock
	// Encoded 是一张等着准入的内联图；非 nil 时 Durable 为 nil。
	Encoded *EncodedImageBlock
}

// PromptContent 是一轮 SDK 输入的那串块。
//
// 源: packages/sdk/protocol/src/types.ts:40（contentBlocks: SdkPromptContentBlock[]）
type PromptContent []PromptBlock

// UnmarshalJSON 按 DSH 那个类型守卫把每一块分到两支里去。
func (c *PromptContent) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("sdkprotocol: contentBlocks 解不动：%w", err)
	}
	if raws == nil {
		*c = nil
		return nil
	}
	blocks := make(PromptContent, len(raws))
	for index, raw := range raws {
		block, err := unmarshalPromptBlock(raw)
		if err != nil {
			return err
		}
		blocks[index] = block
	}
	*c = blocks
	return nil
}

// unmarshalPromptBlock 读一块。
//
// 源: packages/sdk/server/src/server.ts:35-37（encodedImage）
//
// data 用 [json.RawMessage] 接是为了对准 DSH 那句 `'data' in block`——它判的是
// **这个键出现过没有**。换成 string 的话 `{"type":"image"}` 和
// `{"type":"image","data":""}` 会长得一样，而后者是一张空图，该被准入那一层拒掉，
// 不该在这里被当成一条耐久引用悄悄放过去。
func unmarshalPromptBlock(raw json.RawMessage) (PromptBlock, error) {
	var probe struct {
		Type llm.BlockType   `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return PromptBlock{}, fmt.Errorf("sdkprotocol: 一块 contentBlocks 解不动：%w", err)
	}
	if probe.Type == llm.BlockImage && probe.Data != nil {
		var encoded EncodedImageBlock
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return PromptBlock{}, fmt.Errorf("sdkprotocol: 一块内联图解不动：%w", err)
		}
		return PromptBlock{Encoded: &encoded}, nil
	}
	durable, err := llm.UnmarshalContentBlock(raw)
	if err != nil {
		return PromptBlock{}, err
	}
	return PromptBlock{Durable: durable}, nil
}

// MarshalJSON 把两支各自排回线上。
//
// 客户端那一侧要它：这条协议的请求是 SDK 发的，本包同时是那一侧的类型来源。
func (c PromptContent) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	raws := make([]json.RawMessage, len(c))
	for index, block := range c {
		encoded, err := block.marshal()
		if err != nil {
			return nil, err
		}
		raws[index] = encoded
	}
	return json.Marshal(raws)
}

// marshal 把一块排回线上；两支都没有是坏值，当场说出来。
func (b PromptBlock) marshal() (json.RawMessage, error) {
	if b.Encoded != nil {
		return json.Marshal(struct {
			Type llm.BlockType `json:"type"`
			EncodedImageBlock
		}{Type: llm.BlockImage, EncodedImageBlock: *b.Encoded})
	}
	if b.Durable == nil {
		return nil, fmt.Errorf("sdkprotocol: 一块 contentBlocks 两支都是空的")
	}
	return json.Marshal(b.Durable)
}

// SessionPromptResult 是一轮输入的入队回执。
//
// 源: packages/sdk/protocol/src/types.ts:55-59（SessionPromptResult）
//
// 它只说明「这条消息已经进了队列、身份是这个」，**不**说明这一轮跑出了什么——
// 之后的动静从 [MethodSessionEvent] 那条通知流里看。
type SessionPromptResult struct {
	// MessageID 是那条排上队的用户消息的身份。
	MessageID llm.MessageID `json:"messageId"`
}

// RunStatus 是按部署口径映射出来的 SDK 结果：接受了是 ok，别的都是 error。
//
// 源: packages/sdk/protocol/src/types.ts:61-62（SdkRunStatus）
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
// 源: packages/sdk/protocol/src/types.ts:64-70（SessionEventNotification）
//
// 它覆盖运行时里的**每一个**会话，不只是 SDK 建出来的那些。
type SessionEventNotification struct {
	// SessionID 是这条事件属于哪个会话。
	SessionID string `json:"sessionId"`
	// Event 是完整的那个事件信封。
	Event sessionlog.Event `json:"event"`
}

// SessionStatusNotification 是 [MethodSessionStatus] 的负载。
//
// 源: packages/sdk/protocol/src/types.ts:72-78（SessionStatusNotification）
type SessionStatusNotification struct {
	// SessionID 是哪个会话上的活 agent 变了状态。
	SessionID string `json:"sessionId"`
	// Status 是转换之后的整体状态。
	Status AgentStatus `json:"status"`
}

// SubagentStartedNotification 是 [MethodSubagentStarted] 的负载：运行时内部建了一个子会话。
//
// 源: packages/sdk/protocol/src/types.ts:80-86（SubagentStartedNotification）
type SubagentStartedNotification struct {
	// ParentSessionID 是派活的那个会话。
	ParentSessionID string `json:"parentSessionId"`
	// ChildSessionID 是新建的那个子会话。
	ChildSessionID string `json:"childSessionId"`
}

// SubagentFinishedNotification 是 [MethodSubagentFinished] 的负载：一次**进程内**的
// 子 agent 跑完了。
//
// 源: packages/sdk/protocol/src/types.ts:88-104（SubagentFinishedNotification）
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
