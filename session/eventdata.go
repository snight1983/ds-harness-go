// 本文件的作用：把 [Event.Data] 那段原始字节按 [Event.Type] 解成具名字段。
//
// 源: packages/core/session/src/types.ts:236-337

package session

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/llm"
)

// EventData 是一条事件的负载。
//
// 源: packages/core/session/src/types.ts:210-320（SessionEventMap）
//
// 这个联合是**开放**的：本包登记的十三个变体之外，一切落进 [RawData]，
// 原始字节原样保管。理由见包文档「事件数据为什么是 json.RawMessage」。
type EventData interface {
	// EventType 是这个负载属于哪种事件。
	EventType() EventType

	// sealedEventData 把实现方封在本包内。
	sealedEventData()
}

// TurnStartData 是 [EventTurnStart] 的负载。
type TurnStartData struct {
	// Turn 是被打开的那个回合号。
	Turn int `json:"turn"`
}

// EventType 实现 [EventData]。
func (TurnStartData) EventType() EventType { return EventTurnStart }

func (TurnStartData) sealedEventData() {}

// TurnEndData 是 [EventTurnEnd] 的负载。
type TurnEndData struct {
	// Turn 是被关掉的那个回合号。
	Turn int
	// Reason 是它为什么结束。
	Reason TurnEndReason
}

// EventType 实现 [EventData]。
func (TurnEndData) EventType() EventType { return EventTurnEnd }

func (TurnEndData) sealedEventData() {}

// turnEndWire 是回合结束负载在介质上的样子。
type turnEndWire struct {
	Turn   int             `json:"turn"`
	Reason json.RawMessage `json:"reason"`
}

// MarshalJSON 排出这个负载。
func (d TurnEndData) MarshalJSON() ([]byte, error) {
	if d.Reason == nil {
		return nil, malformed("关掉的回合必须带一个结束理由")
	}
	reason, err := json.Marshal(d.Reason)
	if err != nil {
		return nil, wrapMalformed("回合结束理由排不出去", err)
	}
	return json.Marshal(turnEndWire{Turn: d.Turn, Reason: reason})
}

// UnmarshalJSON 把一段字节读回这个负载。
func (d *TurnEndData) UnmarshalJSON(data []byte) error {
	var wire turnEndWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return wrapMalformed("回合结束负载读不回来", err)
	}
	if wire.Reason == nil {
		return malformed("关掉的回合必须带一个结束理由")
	}
	reason, err := UnmarshalTurnEndReason(wire.Reason)
	if err != nil {
		return err
	}
	d.Turn, d.Reason = wire.Turn, reason
	return nil
}

// StepStartData 是 [EventStepStart] 的负载。
type StepStartData struct {
	// Turn 是这个步骤所属的回合号。
	Turn int `json:"turn"`
	// Step 是被打开的那个步骤号。
	Step int `json:"step"`
}

// EventType 实现 [EventData]。
func (StepStartData) EventType() EventType { return EventStepStart }

func (StepStartData) sealedEventData() {}

// StepEndData 是 [EventStepEnd] 的负载。
type StepEndData struct {
	// Turn 是这个步骤所属的回合号。
	Turn int `json:"turn"`
	// Step 是被关掉的那个步骤号。
	Step int `json:"step"`
}

// EventType 实现 [EventData]。
func (StepEndData) EventType() EventType { return EventStepEnd }

func (StepEndData) sealedEventData() {}

// UserMessageData 是 [EventUserMessage] 的负载。
//
// 源: packages/core/session/src/types.ts:264
//
// 新增: DSH 那边这个负载**就是**那条消息本身，没有外层对象。Go 里用内嵌：
// [llm.Message] 自己的 MarshalJSON 与 UnmarshalJSON 被提升上来，排出去、
// 读回来的字节和 DSH 完全一致，不需要在这里再抄一遍消息的介质形状。
type UserMessageData struct {
	llm.Message
}

// EventType 实现 [EventData]。
func (UserMessageData) EventType() EventType { return EventUserMessage }

func (UserMessageData) sealedEventData() {}

// AssistantChunkData 是 [EventAssistantChunk] 的负载。
type AssistantChunkData struct {
	// Turn 是这个分块所属的回合号。
	Turn int
	// Step 是这个分块所属的步骤号。
	Step int
	// Chunk 是那个原始流式分块。
	Chunk llm.StreamChunk
}

// EventType 实现 [EventData]。
func (AssistantChunkData) EventType() EventType { return EventAssistantChunk }

func (AssistantChunkData) sealedEventData() {}

// assistantChunkWire 是助手分块负载在介质上的样子。
type assistantChunkWire struct {
	Turn  int             `json:"turn"`
	Step  int             `json:"step"`
	Chunk json.RawMessage `json:"chunk"`
}

// MarshalJSON 排出这个负载。
func (d AssistantChunkData) MarshalJSON() ([]byte, error) {
	if d.Chunk == nil {
		return nil, malformed("助手分块事件必须带一个分块")
	}
	chunk, err := json.Marshal(d.Chunk)
	if err != nil {
		return nil, wrapMalformed("流式分块排不出去", err)
	}
	return json.Marshal(assistantChunkWire{Turn: d.Turn, Step: d.Step, Chunk: chunk})
}

// UnmarshalJSON 把一段字节读回这个负载。
func (d *AssistantChunkData) UnmarshalJSON(data []byte) error {
	var wire assistantChunkWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return wrapMalformed("助手分块负载读不回来", err)
	}
	if wire.Chunk == nil {
		return malformed("助手分块事件必须带一个分块")
	}
	chunk, err := llm.UnmarshalStreamChunk(wire.Chunk)
	if err != nil {
		return err
	}
	d.Turn, d.Step, d.Chunk = wire.Turn, wire.Step, chunk
	return nil
}

// AssistantMessageData 是 [EventAssistantMessage] 的负载。
//
// 源: packages/core/session/src/types.ts:277
type AssistantMessageData struct {
	// Turn 是这条消息所属的回合号。
	Turn int `json:"turn"`
	// Step 是这条消息所属的步骤号。
	Step int `json:"step"`
	// Message 是这个步骤装配好的助手消息。
	Message llm.Message `json:"message"`
	// Usage 是这个步骤的 token 记账；nil 表示适配器没报。
	//
	// 新增: 这里用指针，因为「适配器没报记账」和「适配器报了一份全零的记账」
	// 是两件事——后者说明这次调用确实没花 token，前者说明我们不知道。
	Usage *llm.TokenUsage `json:"usage,omitempty"`
	// Interrupted 为真表示这是一次流到一半被取消的回合定格下来的前缀。
	//
	// 没派出去的工具调用不在里面。有了这个标记就不必再从回合边界反推中断。
	// 一个被取消的回合要是压根没有这样一条事件，说明它一个字都没流出来。
	//
	// 新增: DSH 那边这个字段的类型是可选的字面量真，也就是「要么在、值只能是 true，
	// 要么不在」。Go 里 bool 就是那个意思，false 就是键不在。
	Interrupted bool `json:"interrupted,omitempty"`
}

// EventType 实现 [EventData]。
func (AssistantMessageData) EventType() EventType { return EventAssistantMessage }

func (AssistantMessageData) sealedEventData() {}

// ToolCallData 是 [EventToolCall] 的负载。
//
// 源: packages/core/session/src/types.ts:283
type ToolCallData struct {
	// Turn 是这次调用所属的回合号。
	Turn int `json:"turn"`
	// Step 是这次调用所属的步骤号。
	Step int `json:"step"`
	// CallID 把这次调用和它的结果配起来。
	CallID llm.CallID `json:"callId"`
	// Name 是被请求的工具名。
	Name string `json:"name"`
	// Arguments 是模型原样产出的参数 JSON 字符串，**没解过**。
	//
	// 保持字符串而不是解成对象：模型产出的字节里可能有重复键、有本包读不懂的
	// 结构，解一遍再排回去就不是模型说的那句话了。
	Arguments string `json:"arguments"`
}

// EventType 实现 [EventData]。
func (ToolCallData) EventType() EventType { return EventToolCall }

func (ToolCallData) sealedEventData() {}

// ToolError 是一次工具失败的内部身份。
//
// 源: packages/core/session/src/types.ts:299
type ToolError struct {
	// Name 是错误类名。
	Name string `json:"name"`
	// Code 是错误码。
	Code string `json:"code"`
}

// ToolResultData 是 [EventToolResult] 的负载。
//
// 源: packages/core/session/src/types.ts:295-301
type ToolResultData struct {
	// Turn 是这次结果所属的回合号。
	Turn int `json:"turn"`
	// Step 是这次结果所属的步骤号。
	Step int `json:"step"`
	// Message 是这次调用面向模型的结果消息。
	Message llm.Message `json:"message"`
	// Error 是这次失败的内部身份；nil 表示没失败。
	Error *ToolError `json:"error,omitempty"`
	// Meta 是工具私有的展示负载，核心不认识它的形状。
	//
	// 产出它的那个工具自己拥有这个形状，也自己读回去。nil 表示工具没挂。
	//
	// 新增: DSH 是 JsonValue，靠 isJsonValue 在写入时验一遍。这里是
	// json.RawMessage：能排出去就是合法 JSON，这道验证由 encoding/json 自己做，
	// 见包文档里 json.ts 那一条。
	Meta json.RawMessage `json:"meta,omitempty"`
}

// EventType 实现 [EventData]。
func (ToolResultData) EventType() EventType { return EventToolResult }

func (ToolResultData) sealedEventData() {}

// TodoWriteData 是 [EventTodoWrite] 的负载。
type TodoWriteData struct {
	// Todos 是整份待办清单的快照。
	Todos []TodoItem `json:"todos"`
}

// EventType 实现 [EventData]。
func (TodoWriteData) EventType() EventType { return EventTodoWrite }

func (TodoWriteData) sealedEventData() {}

// RequestHeaderData 是 [EventRequestHeader] 的负载。
type RequestHeaderData struct {
	// Header 是下一次请求的完整请求头。
	Header EpochHeader `json:"header"`
	// Reason 是这份快照为什么被追加。
	Reason RequestHeaderReason `json:"reason"`
}

// EventType 实现 [EventData]。
func (RequestHeaderData) EventType() EventType { return EventRequestHeader }

func (RequestHeaderData) sealedEventData() {}

// RequestContextData 是 [EventRequestContext] 的负载。
//
// 新增: 和 [UserMessageData] 一样，DSH 那边这个负载**就是**那份路由元数据本身，
// 用内嵌把它的字段直接摊在介质的顶层。
type RequestContextData struct {
	RequestContext
}

// EventType 实现 [EventData]。
func (RequestContextData) EventType() EventType { return EventRequestContext }

func (RequestContextData) sealedEventData() {}

// EndSeedData 是 [EventSessionEndSeed] 的负载：空的。
//
// 位置和 [Event.Time] 就是它的全部意思。
type EndSeedData struct{}

// EventType 实现 [EventData]。
func (EndSeedData) EventType() EventType { return EventSessionEndSeed }

func (EndSeedData) sealedEventData() {}

// MarshalJSON 排出一个空对象。
func (EndSeedData) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

// RawData 收着一条本包没登记的事件的负载，**原样**保管它的字节。
//
// 新增: DSH 的登记表里有 48 个事件类型，本包只实现其中 13 个。一个读到
// compaction/summary 的构建正确的做法是原样保管，不是解成「未知」再排回去时
// 丢字段——日志是持久的，被丢掉的字段没有第二次机会。
type RawData struct {
	// Type 是这条事件的类型。
	Type EventType
	// Raw 是这个负载完整的原始字节。
	Raw json.RawMessage
}

// EventType 实现 [EventData]。
func (d RawData) EventType() EventType { return d.Type }

func (RawData) sealedEventData() {}

// MarshalJSON 把原始字节原样送回去。
func (d RawData) MarshalJSON() ([]byte, error) {
	if len(d.Raw) == 0 {
		return []byte(`{}`), nil
	}
	return append(json.RawMessage(nil), d.Raw...), nil
}

// DecodeData 按 [Event.Type] 把 [Event.Data] 解成一个具名负载。
//
// 认不出来的类型落进 [RawData]，**不报错**：一个类型本构建认不认识，
// 由 [CheckVocabulary] 按 [Event.Ignorable] 单独判，那件事和「能不能解开」无关。
// 解码是按需的，读一份日志不会因为里面有一条读不懂的事件而整个失败。
func DecodeData(event Event) (EventData, error) {
	payload := event.Data
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	switch event.Type {
	case EventTurnStart:
		return decodeInto[TurnStartData](payload, event.Type)
	case EventTurnEnd:
		return decodeInto[TurnEndData](payload, event.Type)
	case EventStepStart:
		return decodeInto[StepStartData](payload, event.Type)
	case EventStepEnd:
		return decodeInto[StepEndData](payload, event.Type)
	case EventUserMessage:
		return decodeInto[UserMessageData](payload, event.Type)
	case EventAssistantChunk:
		return decodeInto[AssistantChunkData](payload, event.Type)
	case EventAssistantMessage:
		return decodeInto[AssistantMessageData](payload, event.Type)
	case EventToolCall:
		return decodeInto[ToolCallData](payload, event.Type)
	case EventToolResult:
		return decodeInto[ToolResultData](payload, event.Type)
	case EventTodoWrite:
		return decodeInto[TodoWriteData](payload, event.Type)
	case EventRequestHeader:
		return decodeInto[RequestHeaderData](payload, event.Type)
	case EventRequestContext:
		return decodeInto[RequestContextData](payload, event.Type)
	case EventSessionEndSeed:
		return EndSeedData{}, nil
	default:
		return RawData{
			Type: event.Type,
			Raw:  append(json.RawMessage(nil), event.Data...),
		}, nil
	}
}

// decodeInto 把一段负载解进 T，再把解出来的**值**（而不是指针）送回去。
//
// 新增: 十二个分支都是同样的「声明零值、取地址解码、返回值」三步，泛型把它收成
// 一行。返回值而不是指针，是因为所有变体的方法都定义在值上：调用方拿到的东西
// 可以直接断言成 [TurnStartData] 而不是 *TurnStartData。
func decodeInto[T EventData](payload json.RawMessage, kind EventType) (EventData, error) {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, wrapMalformed("事件 "+string(kind)+" 的负载读不回来", err)
	}
	return value, nil
}
