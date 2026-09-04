// 本文件的作用：压缩这一层的词汇——四条 compaction/* 事件的类型与负载，
// 以及一次成功压缩交出来的结果。
//
// 源: packages/compaction/compaction/src/types.ts

package compaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// 四条 compaction/* 事件的类型。
//
// 源: packages/compaction/compaction/src/types.ts:16-90
//
// 它们**都不上表面**：一次压缩对模型可见历史的改动，是紧跟在 compaction/summary
// （或 compaction/prune）后面那条替换用的 user/message 做的。这四条只进日志，
// 记的是那次改动的锁、输入和计价。
const (
	// EventCompactionStart 标记一次压缩开始，并且一直持有锁到 [EventCompactionEnd]。
	EventCompactionStart sessionlog.EventType = "compaction/start"
	// EventCompactionSummary 记下做完的摘要、它的输入、以及那次模型调用的事实。
	EventCompactionSummary sessionlog.EventType = "compaction/summary"
	// EventCompactionEnd 标记一次压缩结束并放锁。
	EventCompactionEnd sessionlog.EventType = "compaction/end"
	// EventCompactionPrune 记下一次不过模型的裁剪替换的影子价格。
	EventCompactionPrune sessionlog.EventType = "compaction/prune"
)

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: DSH 靠 `declare module` 把这四个类型合并进 `SessionEventMap`，全局登记表
// 因此自动认得它们。Go 没有声明合并，[sessionlog.Vocabulary] 也是个闭合的值，
// 所以改成由本包交出这张单子，装配方自己拼：
//
//	vocabulary := sessionlog.CoreVocabulary().With(compaction.EventTypes()...)
//
// 不这么做的话，一段带压缩的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{
		EventCompactionStart,
		EventCompactionSummary,
		EventCompactionEnd,
		EventCompactionPrune,
	}
}

// ShadowedRange 是被遮住那一段的头尾。
//
// 源: packages/compaction/compaction/src/types.ts:100-108
//
// 它是一对**表面位置**，不是一个 seq 数值区间：一次替换会把一个 seq 很大的
// 摘要节点落在一段更早的位置上，于是 Start 完全可能大于 End。真正权威的是
// [SummaryData.ShadowedSeqs]，那是按表面顺序排的全体被遮节点。
type ShadowedRange struct {
	// Start 是被遮那一段第一个表面节点的 seq。
	Start int `json:"start"`
	// End 是被遮那一段最后一个表面节点的 seq。
	End int `json:"end"`
}

// StartData 是 [EventCompactionStart] 的负载。
//
// 源: packages/compaction/compaction/src/types.ts:20-24
type StartData struct {
	// CompactionID 是这次事务的身份。
	CompactionID ID
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// Turn 是这次压缩归属的回合号；Standalone 为真时无意义。
	Turn int
	// Standalone 为真表示这是两个回合之间的一次独立人工事务。
	//
	// 新增: DSH 是 `turn: number | null`，null 就是这个意思。Go 这边拆成一个值
	// 加一个布尔，理由和 timecontext.Reading.HasPrevious 逐字相同：回合号从 1 起，
	// 拿 0 当「没有」看着能用，但那是在给自己埋一个「哪个零值算数」的坑。
	Standalone bool
}

// startWire 是 [StartData] 在介质上的样子。
type startWire struct {
	CompactionID    ID              `json:"compactionId"`
	SourceCommandID string          `json:"sourceCommandId,omitempty"`
	Turn            json.RawMessage `json:"turn"`
}

// MarshalJSON 把这条负载排出去。
func (d StartData) MarshalJSON() ([]byte, error) {
	return json.Marshal(startWire{
		CompactionID:    d.CompactionID,
		SourceCommandID: d.SourceCommandID,
		Turn:            encodeOwner(d.Turn, d.Standalone),
	})
}

// UnmarshalJSON 把一段字节读回这条负载。
func (d *StartData) UnmarshalJSON(data []byte) error {
	var wire startWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%s：%w", ErrMalformedEvent, EventCompactionStart, err)
	}
	turn, standalone, err := decodeOwner(EventCompactionStart, wire.Turn)
	if err != nil {
		return err
	}
	d.CompactionID = wire.CompactionID
	d.SourceCommandID = wire.SourceCommandID
	d.Turn, d.Standalone = turn, standalone
	return nil
}

// SummaryData 是 [EventCompactionSummary] 的负载。
//
// 源: packages/compaction/compaction/src/types.ts:34-77
//
// 它紧挨着那条替换用的 user/message，而这个相邻是**有约的**：这里记的几个
// shadowed* 字段就是那条替换的影子价格，消费方靠「替换的前一条」把两者配起来，
// 不必自己留每个节点的价格。
type SummaryData struct {
	// CompactionID 是这次事务的身份。
	CompactionID ID
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// Summary 是做出来的那份摘要内容。
	Summary llm.Content
	// ShadowedRange 是被遮那一段的头尾表面位置。
	ShadowedRange ShadowedRange
	// ShadowedSeqs 是全体被遮的表面节点，按表面顺序。
	ShadowedSeqs []int
	// ShadowedTokenCount 是被遮内容在计量器那把固定尺子下的估价。
	ShadowedTokenCount int
	// Provider 是写这份摘要的提供方路由。
	Provider string
	// Model 是写这份摘要的模型。
	//
	// 落库是为了让「这份摘要是哪个模型写的」有一个持久的答案，也让那次一次性
	// 请求光凭日志加代码就能重建出来。
	Model string
	// MaxTokens 是那次总结调用发出去的生成上限；0 表示没有上限。
	//
	// 新增: DSH 是 `maxTokens?: number`。这里 0 当「没有」用得起来，因为一个
	// 上限为 0 的生成调用本身没有意义——它和「不设上限」不会撞车。
	MaxTokens int
	// Usage 是提供方报回来的这次总结请求的用量；nil 表示没报。
	Usage *llm.TokenUsage
	// RawOutput 是提供方完整的原始输出，后端把它投影成安全摘要之前的样子；
	// nil 表示这条事件没带。
	RawOutput llm.Content
	// LLMStreamCall 为真表示这次总结**恰好**是一次走本上下文 LLM 接缝的调用。
	//
	// 它为真时 [SummaryData.RawOutput] 必须有——那正是这个标记的意义：
	// 标了它，就等于承诺这条日志里有那次调用完整的原始输出可查。
	// 排出去时这一条当场校验，见 [SummaryData.MarshalJSON]。
	LLMStreamCall bool
}

// summaryWire 是 [SummaryData] 在介质上的样子。
//
// RawOutput 和 LLMStreamCall 用指针，为的是把「没这个键」和「这个键的值是空的」
// 分开：DSH 的交叉类型里 `rawOutput: []` 配 `llmStreamCall: true` 是合法的，
// 而 Go 的 omitempty 会把空切片和 nil 一起省掉，那样一次往返就把它改写成
// 「没带原始输出」，于是再读回来变成一条违规的事件。
type summaryWire struct {
	CompactionID       ID              `json:"compactionId"`
	SourceCommandID    string          `json:"sourceCommandId,omitempty"`
	Summary            llm.Content     `json:"summary"`
	ShadowedRange      ShadowedRange   `json:"shadowedRange"`
	ShadowedSeqs       []int           `json:"shadowedSeqs"`
	ShadowedTokenCount int             `json:"shadowedTokenCount"`
	Provider           string          `json:"provider"`
	Model              string          `json:"model"`
	MaxTokens          int             `json:"maxTokens,omitempty"`
	Usage              *llm.TokenUsage `json:"usage,omitempty"`
	RawOutput          *llm.Content    `json:"rawOutput,omitempty"`
	LLMStreamCall      *bool           `json:"llmStreamCall,omitempty"`
}

// MarshalJSON 把这条负载排出去。
//
// 标了 [SummaryData.LLMStreamCall] 却没有原始输出时当场报错，而不是排成一条
// 少一个键的事件：那种事件读回来是违规的，而写它的那一刻没有任何地方会报警。
func (d SummaryData) MarshalJSON() ([]byte, error) {
	wire := summaryWire{
		CompactionID:       d.CompactionID,
		SourceCommandID:    d.SourceCommandID,
		Summary:            d.Summary,
		ShadowedRange:      d.ShadowedRange,
		ShadowedSeqs:       d.ShadowedSeqs,
		ShadowedTokenCount: d.ShadowedTokenCount,
		Provider:           d.Provider,
		Model:              d.Model,
		MaxTokens:          d.MaxTokens,
		Usage:              d.Usage,
	}
	if d.RawOutput != nil {
		wire.RawOutput = &d.RawOutput
	}
	if d.LLMStreamCall {
		if d.RawOutput == nil {
			return nil, fmt.Errorf("%w：%s 标了 llmStreamCall 就必须带上 rawOutput",
				ErrInvariantViolated, EventCompactionSummary)
		}
		marked := true
		wire.LLMStreamCall = &marked
	}
	return json.Marshal(wire)
}

// UnmarshalJSON 把一段字节读回这条负载。
func (d *SummaryData) UnmarshalJSON(data []byte) error {
	var wire summaryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%s：%w", ErrMalformedEvent, EventCompactionSummary, err)
	}
	if wire.LLMStreamCall != nil {
		// DSH 那半个交叉类型是 `llmStreamCall: true`，另一半是 `llmStreamCall?: never`：
		// 写死的 false 两边都不满足，它排不出来，也就不该读得进来。
		if !*wire.LLMStreamCall {
			return fmt.Errorf("%w：%s 的 llmStreamCall 只能是 true 或者根本不写",
				ErrMalformedEvent, EventCompactionSummary)
		}
		if wire.RawOutput == nil {
			return fmt.Errorf("%w：%s 标了 llmStreamCall 却没带 rawOutput",
				ErrInvariantViolated, EventCompactionSummary)
		}
	}

	*d = SummaryData{
		CompactionID:       wire.CompactionID,
		SourceCommandID:    wire.SourceCommandID,
		Summary:            wire.Summary,
		ShadowedRange:      wire.ShadowedRange,
		ShadowedSeqs:       wire.ShadowedSeqs,
		ShadowedTokenCount: wire.ShadowedTokenCount,
		Provider:           wire.Provider,
		Model:              wire.Model,
		MaxTokens:          wire.MaxTokens,
		Usage:              wire.Usage,
		LLMStreamCall:      wire.LLMStreamCall != nil,
	}
	if wire.RawOutput != nil {
		d.RawOutput = *wire.RawOutput
	}
	return nil
}

// EndData 是 [EventCompactionEnd] 的负载。
//
// 源: packages/compaction/compaction/src/types.ts:79-83
type EndData struct {
	// CompactionID 是这次事务的身份，和 [EventCompactionStart] 上的那个一样。
	CompactionID ID
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// Turn 是这次压缩归属的回合号；Standalone 为真时无意义。
	Turn int
	// Standalone 为真表示这是两个回合之间的一次独立人工事务。
	Standalone bool
	// Error 记下一次没成功的尝试；空表示这次压缩成功了。
	//
	// 它是本包唯一一处「空字符串就是没有」用得起来的地方：一条空的错误说明
	// 和没有错误说明是同一件事，而回合号那边不是。
	Error string
}

// endWire 是 [EndData] 在介质上的样子。
type endWire struct {
	CompactionID    ID              `json:"compactionId"`
	SourceCommandID string          `json:"sourceCommandId,omitempty"`
	Turn            json.RawMessage `json:"turn"`
	Error           string          `json:"error,omitempty"`
}

// MarshalJSON 把这条负载排出去。
func (d EndData) MarshalJSON() ([]byte, error) {
	return json.Marshal(endWire{
		CompactionID:    d.CompactionID,
		SourceCommandID: d.SourceCommandID,
		Turn:            encodeOwner(d.Turn, d.Standalone),
		Error:           d.Error,
	})
}

// UnmarshalJSON 把一段字节读回这条负载。
func (d *EndData) UnmarshalJSON(data []byte) error {
	var wire endWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%s：%w", ErrMalformedEvent, EventCompactionEnd, err)
	}
	turn, standalone, err := decodeOwner(EventCompactionEnd, wire.Turn)
	if err != nil {
		return err
	}
	d.CompactionID = wire.CompactionID
	d.SourceCommandID = wire.SourceCommandID
	d.Turn, d.Standalone = turn, standalone
	d.Error = wire.Error
	return nil
}

// PruneData 是 [EventCompactionPrune] 的负载。
//
// 源: packages/compaction/compaction/src/types.ts:85-90
//
// 它是那条共用的影子价格约定里、不过模型的那一半：一条 replace 事件的价格由
// **紧挨在它前面**的那条计价事件给出——过模型的是 [EventCompactionSummary]，
// 不过模型的是这一条。所以替换必须同步地紧跟在它后面追加。
type PruneData struct {
	// ShadowedRange 是被换掉那一段的头尾表面位置。
	ShadowedRange ShadowedRange `json:"shadowedRange"`
	// ShadowedSeqs 是全体被遮的表面节点，按表面顺序。
	ShadowedSeqs []int `json:"shadowedSeqs"`
	// ShadowedTokenCount 是被遮内容在计量器那把固定尺子下的估价。
	ShadowedTokenCount int `json:"shadowedTokenCount"`
}

// Result 是一次成功压缩交出来的结果。
//
// 源: packages/compaction/compaction/src/types.ts:92-119（CompactionResult）
//
// 它不进日志，所以没有 json 标签：三个 seq 是刚刚追加进去的那三条事件的位置，
// 调用方拿它去定位自己这次做了什么。
type Result struct {
	// CompactionID 是这次事务的身份。
	CompactionID ID
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// StartSeq 是追加进去的那条 compaction/start 的 seq。
	StartSeq int
	// SummarySeq 是追加进去的那条 compaction/summary 的 seq。
	SummarySeq int
	// EndSeq 是追加进去的那条 compaction/end 的 seq。
	EndSeq int
	// Summary 是后端做出来的那份摘要内容。
	Summary llm.Content
	// ShadowedRange 是被遮那一段的头尾表面位置。
	ShadowedRange ShadowedRange
	// ShadowedSeqs 是全体被遮的表面节点，按表面顺序。
	ShadowedSeqs []int
	// ShadowedTokenCount 是被遮内容的估价。
	ShadowedTokenCount int
}

// encodeOwner 把「归属哪个回合」排成介质上的 turn 字段。
func encodeOwner(turn int, standalone bool) json.RawMessage {
	if standalone {
		return json.RawMessage(`null`)
	}
	return json.RawMessage(strconv.Itoa(turn))
}

// decodeOwner 把介质上的 turn 字段读回一个回合号加一个布尔。
//
// 这个键**缺了就是错**，不是当成独立事务。理由：一条归属被静默补成默认值的
// 压缩括号，正是本包这条不变量存在的原因——它会让一次压缩看起来发生在
// 另一个位置，而那条日志本身读得回来。DSH 那边同样拒（`undefined !== openTurn`
// 在 validateOwner 里怎么走都不成立），只是它拒在不变量那一侧。
func decodeOwner(kind sessionlog.EventType, raw json.RawMessage) (int, bool, error) {
	if len(raw) == 0 {
		return 0, false, fmt.Errorf("%w：%s 必须写明 turn——一个回合号，或者 null 表示独立事务",
			ErrMalformedEvent, kind)
	}
	if string(raw) == "null" {
		return 0, true, nil
	}
	var turn int
	if err := json.Unmarshal(raw, &turn); err != nil {
		return 0, false, fmt.Errorf("%w：%s 的 turn 既不是回合号也不是 null：%w",
			ErrMalformedEvent, kind, err)
	}
	return turn, false, nil
}

// decodePayload 把一条事件的负载解进 T，先确认它的类型对得上。
//
// 新增: DSH 靠声明合并让 `SessionEvent<'compaction/start'>` 这种写法在编译期就
// 把负载类型收窄了。[sessionlog.EventData] 是个**封闭**接口（带一个不可导出的方法），
// 本包的四种负载进不去那个联合，所以 [sessionlog.DecodeData] 只会把它们交成
// [sessionlog.RawData]。于是这里直接读 [sessionlog.Event.Data]，自己查一遍类型。
func decodePayload[T any](event sessionlog.Event, kind sessionlog.EventType) (T, error) {
	var decoded T
	if event.Type != kind {
		return decoded, fmt.Errorf("%w：seq %d 是 %s，不是 %s",
			ErrMalformedEvent, event.Seq, event.Type, kind)
	}
	payload := event.Data
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return decoded, wrapDecode(event, kind, err)
	}
	return decoded, nil
}

// wrapDecode 给一条解码失败补上它在日志里的位置。
//
// 负载自己的 UnmarshalJSON 已经挂过本包的哨兵了，这里就不再挂第二个：
// 一条错误同时满足 [ErrMalformedEvent] 和 [ErrInvariantViolated]，
// 调用方 errors.Is 两边都成立，也就分不出该按哪一种处理。
func wrapDecode(event sessionlog.Event, kind sessionlog.EventType, err error) error {
	if errors.Is(err, ErrMalformedEvent) || errors.Is(err, ErrInvariantViolated) {
		return fmt.Errorf("seq %d：%w", event.Seq, err)
	}
	return fmt.Errorf("%w：seq %d 的 %s：%w", ErrMalformedEvent, event.Seq, kind, err)
}

// DecodeStart 读回一条 compaction/start 的负载。
func DecodeStart(event sessionlog.Event) (StartData, error) {
	return decodePayload[StartData](event, EventCompactionStart)
}

// DecodeSummary 读回一条 compaction/summary 的负载。
func DecodeSummary(event sessionlog.Event) (SummaryData, error) {
	return decodePayload[SummaryData](event, EventCompactionSummary)
}

// DecodeEnd 读回一条 compaction/end 的负载。
func DecodeEnd(event sessionlog.Event) (EndData, error) {
	return decodePayload[EndData](event, EventCompactionEnd)
}

// DecodePrune 读回一条 compaction/prune 的负载。
func DecodePrune(event sessionlog.Event) (PruneData, error) {
	return decodePayload[PruneData](event, EventCompactionPrune)
}
