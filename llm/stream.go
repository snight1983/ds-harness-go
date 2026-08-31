// 本文件的作用：一次模型调用产出的那些事实——失败、用量、为什么停、以及适配器
// 吐出来的那串流式分块。
//
// 源: packages/llm/llm/src/types.ts:39-51（LlmFailure）
// 源: packages/llm/llm/src/types.ts:111-141（FinishReason、TokenUsage）
// 源: packages/llm/llm/src/types.ts:283-302（ReplayEnvelope）
// 源: packages/llm/llm/src/types.ts:304-324（StreamChunk）
// 源: packages/llm/llm/src/message.ts:243-261（isTokenDelta）

package llm

import (
	"encoding/json"
	"fmt"
)

// Failure 是一次提供方或传输失败的、可序列化的事实；重不重试由策略决定。
//
// 源: packages/llm/llm/src/types.ts:39-51
//
// 新增: 它**不是** Go 的 error，也不实现 error 接口。理由是它的身份是「一条会被
// 写进会话日志、再读回来的事实」——一条读回来的失败没有栈、没有 Unwrap 链，
// 把它做成 error 只会让调用方以为可以 errors.Is 它。要传播失败用本包的哨兵错误，
// 要**记录**失败用这个值。
type Failure struct {
	// Message 是人能读的那句提供方／传输失败描述。
	Message string `json:"message"`
	// Code 是与提供方无关的、供机器路由的稳定码。
	Code string `json:"code"`
	// Status 是提供方返回的 HTTP 状态码，没有时为 0。
	//
	// 新增: DSH 是 status?: number。这里用 0 表示「没有」：HTTP 状态码里没有 0，
	// 所以零值不与任何真实取值撞车，多一层指针换不到任何区分能力。
	Status int `json:"status,omitempty"`
	// ProviderRetryAfterMs 是提供方要求的等待毫秒数，没有或不合法时为 0。理由同 Status。
	ProviderRetryAfterMs int `json:"providerRetryAfterMs,omitempty"`
	// RequestID 是提供方签发的请求标识，只为诊断留着。空串表示没有。
	RequestID ProviderRequestID `json:"requestId,omitempty"`
}

// TokenUsage 是一次模型调用的 token 记账。
//
// 源: packages/llm/llm/src/types.ts:132-141
//
// 几个计数是**互不重叠**的：InputTokens 只算没命中缓存的输入，命中缓存的那部分
// 单独记在 CacheReadTokens／CacheWriteTokens 上，计费输入等于三者之和。
// 提供方把缓存命中折进一个总的 prompt 计数时（DeepSeek 的 prompt_tokens 就是），
// 由适配器减出来。
//
// 新增: 后三个 DSH 是可选的。这里用普通的 int：一个「没报缓存命中」的提供方和一个
// 「报了零次缓存命中」的提供方，对着这几个数做加法的人来说是同一件事。
type TokenUsage struct {
	// InputTokens 是没命中缓存的输入 token 数。
	InputTokens int `json:"inputTokens"`
	// OutputTokens 是输出 token 数。
	OutputTokens int `json:"outputTokens"`
	// CacheReadTokens 是从缓存里读到的输入 token 数。
	CacheReadTokens int `json:"cacheReadTokens,omitempty"`
	// CacheWriteTokens 是写进缓存的输入 token 数。
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
	// ReasoningTokens 是推理过程消耗的 token 数。
	ReasoningTokens int `json:"reasoningTokens,omitempty"`
}

// ReplayEnvelope 是适配器私有的、无损的 JSON 状态，用来重放一次成功的响应。
//
// 源: packages/llm/llm/src/types.ts:283-302
//
// 它由终止的 finish 分块带出来，存在装配好的那条助手消息的模型来源上
// （[Provenance.ReplayState]）。两半对本运行时都是不透明的，共享的只有这个**切分**：
// 于是装配可以在不读任何一半的前提下，把存下来的元数据和存下来的内容对齐。
type ReplayEnvelope struct {
	// Response 是响应级别的适配器私有元数据（各种 id、提供方原生的停止原因）。
	Response json.RawMessage `json:"response"`
	// Blocks 是每块一条的适配器私有元数据，按块第一次出现的流顺序排列。
	//
	// 装配丢掉一块时同时丢掉同一位置的这一条；条数和实际发出的块数对不上时整个信封作废。
	// 元数据与块结构无关的适配器不填这个字段，那样信封原封不动地穿过装配。
	Blocks []json.RawMessage `json:"blocks,omitempty"`
}

// Clone 深复制这个信封。
func (e ReplayEnvelope) Clone() ReplayEnvelope {
	e.Response = append(json.RawMessage(nil), e.Response...)
	if e.Blocks != nil {
		blocks := make([]json.RawMessage, len(e.Blocks))
		for index, block := range e.Blocks {
			blocks[index] = append(json.RawMessage(nil), block...)
		}
		e.Blocks = blocks
	}
	return e
}

// FinishKind 是一次模型响应停下来的原因的判别标签。
//
// 源: packages/llm/llm/src/types.ts:116-122
//
// 它不是封闭的：本包认识下面五个，读到别的会落进 [UnknownFinish]，理由见包文档。
type FinishKind string

const (
	// FinishStop 是模型自己说完了。
	FinishStop FinishKind = "stop"
	// FinishToolCalls 是模型停下来等工具结果。
	FinishToolCalls FinishKind = "tool-calls"
	// FinishMaxTokens 是撞上了输出上限。
	FinishMaxTokens FinishKind = "max-tokens"
	// FinishAborted 是这次调用被中止了。
	FinishAborted FinishKind = "aborted"
	// FinishError 是这次调用失败了。
	FinishError FinishKind = "error"
)

// FinishReason 是一次模型响应为什么停下来。
//
// 源: packages/llm/llm/src/types.ts:111-125
//
// 新增: 封闭接口加 Unknown 变体，理由和 [ContentBlock] 逐字相同，见包文档。
//
// 它是个联合而不是一个「带 Kind 和一个可选 Failure 字段」的结构体，守的是同一句话：
// 只有 aborted 和 error 带失败事实，另外三个**没有**失败可言。摊平成结构体的话，
// 一个 kind 是 stop 却带着 failure 的值就写得出来。
type FinishReason interface {
	// FinishKind 是这个原因的判别标签。
	FinishKind() FinishKind

	// sealedFinishReason 把实现方封在本包内。
	sealedFinishReason()
}

// StopFinish 是模型自己说完了，没有额外字段。
type StopFinish struct{}

// FinishKind 实现 [FinishReason]。
func (StopFinish) FinishKind() FinishKind { return FinishStop }

func (StopFinish) sealedFinishReason() {}

// ToolCallsFinish 是模型停下来等工具结果，没有额外字段。
type ToolCallsFinish struct{}

// FinishKind 实现 [FinishReason]。
func (ToolCallsFinish) FinishKind() FinishKind { return FinishToolCalls }

func (ToolCallsFinish) sealedFinishReason() {}

// MaxTokensFinish 是撞上了输出上限，没有额外字段。
type MaxTokensFinish struct{}

// FinishKind 实现 [FinishReason]。
func (MaxTokensFinish) FinishKind() FinishKind { return FinishMaxTokens }

func (MaxTokensFinish) sealedFinishReason() {}

// AbortedFinish 是这次调用被中止了，带着中止的事实。
type AbortedFinish struct {
	// Failure 是这次中止的可序列化事实。
	Failure Failure
}

// FinishKind 实现 [FinishReason]。
func (AbortedFinish) FinishKind() FinishKind { return FinishAborted }

func (AbortedFinish) sealedFinishReason() {}

// ErrorFinish 是这次调用失败了，带着失败的事实。
type ErrorFinish struct {
	// Failure 是这次失败的可序列化事实。
	Failure Failure
}

// FinishKind 实现 [FinishReason]。
func (ErrorFinish) FinishKind() FinishKind { return FinishError }

func (ErrorFinish) sealedFinishReason() {}

// UnknownFinish 是一个本构建不认识的停止原因，原样保管。
//
// 新增: 理由和 [UnknownBlock] 逐字相同。
type UnknownFinish struct {
	// Kind 是这个原因自称的类别。
	Kind FinishKind
	// Raw 是这个原因完整的原始 JSON。
	Raw json.RawMessage
}

// FinishKind 实现 [FinishReason]，给出它自称的类别。
func (r UnknownFinish) FinishKind() FinishKind { return r.Kind }

func (UnknownFinish) sealedFinishReason() {}

// 下面是五种停止原因在介质上的样子。判别标签的键是 kind 而不是 type，照 DSH 写。
type bareFinishWire struct {
	Kind FinishKind `json:"kind"`
}

type failedFinishWire struct {
	Kind    FinishKind `json:"kind"`
	Failure Failure    `json:"failure"`
}

// MarshalJSON 把这个原因连同判别标签一起排出去。
func (StopFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(bareFinishWire{Kind: FinishStop})
}

// MarshalJSON 把这个原因连同判别标签一起排出去。
func (ToolCallsFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(bareFinishWire{Kind: FinishToolCalls})
}

// MarshalJSON 把这个原因连同判别标签一起排出去。
func (MaxTokensFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(bareFinishWire{Kind: FinishMaxTokens})
}

// MarshalJSON 把这个原因连同判别标签和失败事实一起排出去。
func (r AbortedFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(failedFinishWire{Kind: FinishAborted, Failure: r.Failure})
}

// MarshalJSON 把这个原因连同判别标签和失败事实一起排出去。
func (r ErrorFinish) MarshalJSON() ([]byte, error) {
	return json.Marshal(failedFinishWire{Kind: FinishError, Failure: r.Failure})
}

// MarshalJSON 把这个原因原样吐回去，理由同 [UnknownBlock.MarshalJSON]。
func (r UnknownFinish) MarshalJSON() ([]byte, error) {
	if !json.Valid(r.Raw) {
		return nil, fmt.Errorf("%w：不认识的停止原因没有原始字节", ErrMalformedValue)
	}
	return r.Raw, nil
}

// UnmarshalFinishReason 把一段字节读回一个 [FinishReason]。
//
// 不认识的标签收进 [UnknownFinish]，不报错。
func UnmarshalFinishReason(data []byte) (FinishReason, error) {
	var tagged bareFinishWire
	if err := json.Unmarshal(data, &tagged); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}

	switch tagged.Kind {
	case FinishStop:
		return StopFinish{}, nil

	case FinishToolCalls:
		return ToolCallsFinish{}, nil

	case FinishMaxTokens:
		return MaxTokensFinish{}, nil

	case FinishAborted:
		var wire failedFinishWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return AbortedFinish{Failure: wire.Failure}, nil

	case FinishError:
		var wire failedFinishWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ErrorFinish{Failure: wire.Failure}, nil

	default:
		return UnknownFinish{
			Kind: tagged.Kind,
			Raw:  append(json.RawMessage(nil), data...),
		}, nil
	}
}

// ChunkType 是流式分块的判别标签。
//
// 源: packages/llm/llm/src/types.ts:312-324
type ChunkType string

const (
	// ChunkBlockStart 宣告某个下标上开始了一块新内容。
	ChunkBlockStart ChunkType = "block-start"
	// ChunkTextDelta 是可见文本的一小段增量。
	ChunkTextDelta ChunkType = "text-delta"
	// ChunkReasoningDelta 是推理内容的一小段增量。
	ChunkReasoningDelta ChunkType = "reasoning-delta"
	// ChunkToolCallDelta 是一次工具调用参数的一小段增量。
	ChunkToolCallDelta ChunkType = "tool-call-delta"
	// ChunkBlockEnd 带着某个下标上装配好的那一整块。
	ChunkBlockEnd ChunkType = "block-end"
	// ChunkUsage 带着这次调用的 token 记账。
	ChunkUsage ChunkType = "usage"
	// ChunkFinish 是终止分块，带着停止原因。
	ChunkFinish ChunkType = "finish"
)

// StreamChunk 是适配器吐出来的原始流式协议里的一块。
//
// 源: packages/llm/llm/src/types.ts:304-324
//
// 块下标把交错的增量对应起来，[BlockEndChunk] 带着装配好的那一块。
// 适配器在终止的 finish 之前发出用量、之后什么都不发；工具参数始终是原始 JSON 串。
// 适配器实现可以抛错，但运行时会在把失败暴露给消费方之前，把它归一成一个终止的
// error 或 aborted finish。
//
// 新增: 这个联合**没有** Unknown 变体，另外三个有。理由是它不是持久化格式，
// 是适配器和运行时之间一条封闭的、进程内的协议：两端在同一次构建里，一个读不懂的
// 标签只可能是编程错误，而不是「一份更新版本写下的数据」。所以
// [UnmarshalStreamChunk] 遇到不认识的标签返回 [ErrUnknownChunkType]。
type StreamChunk interface {
	// ChunkType 是这一块的判别标签。
	ChunkType() ChunkType

	// sealedStreamChunk 把实现方封在本包内。
	sealedStreamChunk()
}

// BlockStartChunk 宣告某个下标上开始了一块新内容。
type BlockStartChunk struct {
	// Index 是这一块在本次响应里的下标。
	Index int
	// BlockType 是即将到来的那一块的类型。
	BlockType BlockType
}

// ChunkType 实现 [StreamChunk]。
func (BlockStartChunk) ChunkType() ChunkType { return ChunkBlockStart }

func (BlockStartChunk) sealedStreamChunk() {}

// TextDeltaChunk 是可见文本的一小段增量。
type TextDeltaChunk struct {
	// Index 是这一块在本次响应里的下标。
	Index int
	// Text 是这一段增量的文本。
	Text string
}

// ChunkType 实现 [StreamChunk]。
func (TextDeltaChunk) ChunkType() ChunkType { return ChunkTextDelta }

func (TextDeltaChunk) sealedStreamChunk() {}

// ReasoningDeltaChunk 是推理内容的一小段增量。
type ReasoningDeltaChunk struct {
	// Index 是这一块在本次响应里的下标。
	Index int
	// Text 是这一段增量的文本。
	Text string
}

// ChunkType 实现 [StreamChunk]。
func (ReasoningDeltaChunk) ChunkType() ChunkType { return ChunkReasoningDelta }

func (ReasoningDeltaChunk) sealedStreamChunk() {}

// ToolCallDeltaChunk 是一次工具调用参数的一小段增量。
type ToolCallDeltaChunk struct {
	// Index 是这一块在本次响应里的下标。
	Index int
	// ID 是提供方签发的调用标识。
	ID CallID
	// Name 是被调用的工具名；nil 表示这一段没有携带工具名。
	//
	// 新增: 这是本包里唯一一个用指针表达可选的字段。理由在 [IsTokenDelta]：
	// 那里判的是 `name !== undefined`，也就是说一个**空串的工具名**同样算一次
	// token 增量，而「没带工具名」不算。空串和缺失在这一个字段上是两件事，
	// 所以必须用指针，不能用空串代替。
	Name *string
	// ArgumentsDelta 是这一段增量的原始 JSON 串片段。
	ArgumentsDelta string
}

// ChunkType 实现 [StreamChunk]。
func (ToolCallDeltaChunk) ChunkType() ChunkType { return ChunkToolCallDelta }

func (ToolCallDeltaChunk) sealedStreamChunk() {}

// BlockEndChunk 带着某个下标上装配好的那一整块。
type BlockEndChunk struct {
	// Index 是这一块在本次响应里的下标。
	Index int
	// Block 是装配好的那一块内容。
	Block ContentBlock
}

// ChunkType 实现 [StreamChunk]。
func (BlockEndChunk) ChunkType() ChunkType { return ChunkBlockEnd }

func (BlockEndChunk) sealedStreamChunk() {}

// UsageChunk 带着这次调用的 token 记账。
type UsageChunk struct {
	// Usage 是这次调用的 token 记账。
	Usage TokenUsage
}

// ChunkType 实现 [StreamChunk]。
func (UsageChunk) ChunkType() ChunkType { return ChunkUsage }

func (UsageChunk) sealedStreamChunk() {}

// FinishChunk 是终止分块。
type FinishChunk struct {
	// Reason 是这次响应为什么停下来。
	Reason FinishReason
	// ReplayState 是一次成功响应的重放元数据；nil 表示适配器没给。
	//
	// 新增: 这里用指针，因为一个「没给重放状态」的适配器和一个「给了个空信封」的
	// 适配器不是同一件事——后者会让装配以为有零条块级元数据要对齐。
	ReplayState *ReplayEnvelope
}

// ChunkType 实现 [StreamChunk]。
func (FinishChunk) ChunkType() ChunkType { return ChunkFinish }

func (FinishChunk) sealedStreamChunk() {}

// IsTokenDelta 判断这一块是不是真的携带了增量内容。
//
// 源: packages/llm/llm/src/message.ts:243-261
//
// 它是「首个 token」这类计时的判据：一个下标为 0 的 block-start、一条用量、
// 一个空的文本增量都不算模型真的吐出了东西。
func IsTokenDelta(chunk StreamChunk) bool {
	switch typed := chunk.(type) {
	case TextDeltaChunk:
		return typed.Text != ""
	case ReasoningDeltaChunk:
		return typed.Text != ""
	case ToolCallDeltaChunk:
		return typed.ArgumentsDelta != "" || typed.Name != nil
	default:
		return false
	}
}

// 下面是七种分块在介质上的样子。
type blockStartChunkWire struct {
	Type      ChunkType `json:"type"`
	Index     int       `json:"index"`
	BlockType BlockType `json:"blockType"`
}

type textDeltaChunkWire struct {
	Type  ChunkType `json:"type"`
	Index int       `json:"index"`
	Text  string    `json:"text"`
}

type toolCallDeltaChunkWire struct {
	Type           ChunkType `json:"type"`
	Index          int       `json:"index"`
	ID             CallID    `json:"id"`
	Name           *string   `json:"name,omitempty"`
	ArgumentsDelta string    `json:"argumentsDelta"`
}

type blockEndChunkWire struct {
	Type  ChunkType       `json:"type"`
	Index int             `json:"index"`
	Block json.RawMessage `json:"block"`
}

type usageChunkWire struct {
	Type  ChunkType  `json:"type"`
	Usage TokenUsage `json:"usage"`
}

type finishChunkWire struct {
	Type        ChunkType       `json:"type"`
	Reason      json.RawMessage `json:"reason"`
	ReplayState *ReplayEnvelope `json:"replayState,omitempty"`
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c BlockStartChunk) MarshalJSON() ([]byte, error) {
	return json.Marshal(blockStartChunkWire{
		Type:      ChunkBlockStart,
		Index:     c.Index,
		BlockType: c.BlockType,
	})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c TextDeltaChunk) MarshalJSON() ([]byte, error) {
	return json.Marshal(textDeltaChunkWire{Type: ChunkTextDelta, Index: c.Index, Text: c.Text})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c ReasoningDeltaChunk) MarshalJSON() ([]byte, error) {
	return json.Marshal(textDeltaChunkWire{Type: ChunkReasoningDelta, Index: c.Index, Text: c.Text})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c ToolCallDeltaChunk) MarshalJSON() ([]byte, error) {
	return json.Marshal(toolCallDeltaChunkWire{
		Type:           ChunkToolCallDelta,
		Index:          c.Index,
		ID:             c.ID,
		Name:           c.Name,
		ArgumentsDelta: c.ArgumentsDelta,
	})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c BlockEndChunk) MarshalJSON() ([]byte, error) {
	if c.Block == nil {
		return nil, fmt.Errorf("%w：block-end 分块没有内容块", ErrMalformedValue)
	}
	block, err := json.Marshal(c.Block)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blockEndChunkWire{Type: ChunkBlockEnd, Index: c.Index, Block: block})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c UsageChunk) MarshalJSON() ([]byte, error) {
	return json.Marshal(usageChunkWire{Type: ChunkUsage, Usage: c.Usage})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (c FinishChunk) MarshalJSON() ([]byte, error) {
	if c.Reason == nil {
		return nil, fmt.Errorf("%w：finish 分块没有停止原因", ErrMalformedValue)
	}
	reason, err := json.Marshal(c.Reason)
	if err != nil {
		return nil, err
	}
	return json.Marshal(finishChunkWire{
		Type:        ChunkFinish,
		Reason:      reason,
		ReplayState: c.ReplayState,
	})
}

// UnmarshalStreamChunk 把一段字节读回一个 [StreamChunk]。
//
// 不认识的标签返回 [ErrUnknownChunkType]，**不**收进 Unknown 变体，理由见
// [StreamChunk] 的类型注释。
func UnmarshalStreamChunk(data []byte) (StreamChunk, error) {
	var tagged struct {
		Type ChunkType `json:"type"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}

	switch tagged.Type {
	case ChunkBlockStart:
		var wire blockStartChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return BlockStartChunk{Index: wire.Index, BlockType: wire.BlockType}, nil

	case ChunkTextDelta:
		var wire textDeltaChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return TextDeltaChunk{Index: wire.Index, Text: wire.Text}, nil

	case ChunkReasoningDelta:
		var wire textDeltaChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ReasoningDeltaChunk{Index: wire.Index, Text: wire.Text}, nil

	case ChunkToolCallDelta:
		var wire toolCallDeltaChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ToolCallDeltaChunk{
			Index:          wire.Index,
			ID:             wire.ID,
			Name:           wire.Name,
			ArgumentsDelta: wire.ArgumentsDelta,
		}, nil

	case ChunkBlockEnd:
		var wire blockEndChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		block, err := UnmarshalContentBlock(wire.Block)
		if err != nil {
			return nil, err
		}
		return BlockEndChunk{Index: wire.Index, Block: block}, nil

	case ChunkUsage:
		var wire usageChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return UsageChunk{Usage: wire.Usage}, nil

	case ChunkFinish:
		var wire finishChunkWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		reason, err := UnmarshalFinishReason(wire.Reason)
		if err != nil {
			return nil, err
		}
		return FinishChunk{Reason: reason, ReplayState: wire.ReplayState}, nil

	default:
		return nil, fmt.Errorf("%w：%q", ErrUnknownChunkType, tagged.Type)
	}
}
