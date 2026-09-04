// 本文件的作用：把一串连续的助手增量分块压成一行存储记录，以及把它原样展开回来。
//
// 源: packages/core/session/src/chunk-rows.ts

package sessionlog

import (
	"encoding/json"
	"math"

	"github.com/snight1983/ds-harness-go/llm"
)

// 提供方按 token 大小往外流增量，所以一段日志里会有好几百行几乎一样的事件，
// 它们的 JSON 信封比负载本身大得多（在一次真实会话上量到约 56 倍）。
// 本文件把每一串连续的、同一块上的同类增量分块压成**一行**存储记录，
// 再把行展开回一模一样的原事件。
//
// 存储行是一套**持久编码**的词汇，不是会话事件：它们从不进 [Event] 的序列，
// 在事件类型表里没有条目，而且用不带斜杠的裸标签，读的人不会把它们和事件的
// 分类混起来。编码方只认**完全**认得出的形状——认不全的一律原样存，
// 未知字段或者将来的新分块变体丢的是压缩率，不是数据。解码方先验后展开，
// 遇到一行标了行标签却坏掉的值就当场报错，而不是默默丢掉一整串。

// RowType 是一行存储记录的裸标签。
type RowType string

const (
	// RowTextChunks 是一串可见文本增量。
	RowTextChunks RowType = "text-chunks"
	// RowReasoningChunks 是一串推理内容增量。
	RowReasoningChunks RowType = "reasoning-chunks"
	// RowToolCallChunks 是一串工具调用参数增量。
	RowToolCallChunks RowType = "tool-call-chunks"
)

// minRun 是一串至少要多长才值得压。
//
// 源: packages/core/session/src/chunk-rows.ts:77
//
// 低于它，一行记录的信封和它替掉的那几行事件差不多大。这是一个**格式常量**，
// 不是可调参数：两种排布解出来完全一样，改它不会让已经存下的日志失效。
const minRun = 3

// deltaEvent 是一条被编码方认下来的增量分块事件，字段已经拆好。
type deltaEvent struct {
	// event 是拆它出来的那条原事件。攒够不了一串时要把它原样写回去，
	// 而调用方交进来的切片的 seq 不保证从哪里起、也不保证没缺口，
	// 拿 seq 去反算下标是错的，所以这里直接把它带着。
	event Event
	kind  llm.ChunkType
	seq   int
	time  int64
	turn  int
	step  int
	index int
	// text 只在文本与推理增量上有意义。
	text string
	// id、name、args 只在工具调用增量上有意义。
	id   llm.CallID
	name *string
	args string
}

// classify 判断一条事件能不能压，能压就把它拆好。
//
// 源: packages/core/session/src/chunk-rows.ts:96-123
//
// 只有**整个形状**都在白名单里（信封没有多余字段、data 恰好三个键、
// chunk 恰好是认得的那几种形状）才算数，否则原样存。
//
// 新增: DSH 要逐个检查信封的键，因为它拿到的是 JSON.parse 出来的对象。
// Go 这边信封已经是 [Event] 结构体了，「多余字段」在这里的对应物是
// 三个可选字段有没有被用上——一条带了 Ignorable、SurfaceOp 或 SourceEventSeqs
// 的分块事件压进行里就会丢掉那些字段，所以它不压。
func classify(event Event) (deltaEvent, bool) {
	if event.Type != EventAssistantChunk || event.Seq < 0 {
		return deltaEvent{}, false
	}
	if event.Ignorable || event.SurfaceOp != nil || event.SourceEventSeqs != nil {
		return deltaEvent{}, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &fields); err != nil || len(fields) != 3 {
		return deltaEvent{}, false
	}
	if fields["turn"] == nil || fields["step"] == nil || fields["chunk"] == nil {
		return deltaEvent{}, false
	}
	var turn, step int
	if json.Unmarshal(fields["turn"], &turn) != nil || json.Unmarshal(fields["step"], &step) != nil {
		return deltaEvent{}, false
	}

	var chunkFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["chunk"], &chunkFields); err != nil {
		return deltaEvent{}, false
	}
	var kind llm.ChunkType
	if chunkFields["type"] == nil || json.Unmarshal(chunkFields["type"], &kind) != nil {
		return deltaEvent{}, false
	}

	switch kind {
	case llm.ChunkTextDelta, llm.ChunkReasoningDelta:
		if len(chunkFields) != 3 || chunkFields["index"] == nil || chunkFields["text"] == nil {
			return deltaEvent{}, false
		}
		var chunk llm.TextDeltaChunk
		if json.Unmarshal(fields["chunk"], &chunk) != nil {
			return deltaEvent{}, false
		}
		return deltaEvent{
			event: event,
			kind:  kind, seq: event.Seq, time: event.Time,
			turn: turn, step: step, index: chunk.Index, text: chunk.Text,
		}, true

	case llm.ChunkToolCallDelta:
		hasName := chunkFields["name"] != nil
		wanted := 4
		if hasName {
			wanted = 5
		}
		if len(chunkFields) != wanted ||
			chunkFields["index"] == nil || chunkFields["id"] == nil || chunkFields["argumentsDelta"] == nil {
			return deltaEvent{}, false
		}
		var chunk llm.ToolCallDeltaChunk
		if json.Unmarshal(fields["chunk"], &chunk) != nil {
			return deltaEvent{}, false
		}
		return deltaEvent{
			event: event,
			kind:  kind, seq: event.Seq, time: event.Time,
			turn: turn, step: step, index: chunk.Index,
			id: chunk.ID, name: chunk.Name, args: chunk.ArgumentsDelta,
		}, true

	default:
		// 白名单之外一律落到这里：块的起止、用量、结束，以及将来任何新的分块变体，
		// 都保持一条事件一行。
		return deltaEvent{}, false
	}
}

// continues 判断 next 能不能接在以 prev 结尾的那一串后面（同类已由调用方保证）。
//
// 源: packages/core/session/src/chunk-rows.ts:136-151
func continues(prev, next deltaEvent) bool {
	if next.seq != prev.seq+1 {
		return false
	}
	// 新增: DSH 在这里挡的是「两个安全整数时间戳相减会被 float64 四舍五入」。
	// int64 的减法在不溢出时逐位精确，所以这里只剩下溢出这一种情况要挡。
	if subOverflows(next.time, prev.time) {
		return false
	}
	if next.turn != prev.turn || next.step != prev.step || next.index != prev.index {
		return false
	}
	if next.kind != llm.ChunkToolCallDelta {
		return true
	}
	// 工具名必须在**有无**和**取值**上都一致——一串混着的压不进一行。
	if next.id != prev.id || (prev.name == nil) != (next.name == nil) {
		return false
	}
	return prev.name == nil || *prev.name == *next.name
}

// textRunData 是文本与推理行的负载：一个成员一条，从不拼起来——token 的边界是数据。
type textRunData struct {
	Turn  int      `json:"turn"`
	Step  int      `json:"step"`
	Index int      `json:"index"`
	Dt    []int64  `json:"dt"`
	Texts []string `json:"texts"`
}

// toolCallRunData 是工具调用行的负载：一串里不变的那份调用身份，加每个成员的原始参数片段。
type toolCallRunData struct {
	Turn  int        `json:"turn"`
	Step  int        `json:"step"`
	Index int        `json:"index"`
	Dt    []int64    `json:"dt"`
	ID    llm.CallID `json:"id"`
	// Name 只在每个成员都带、且取值一致时才有。
	Name *string  `json:"name,omitempty"`
	Args []string `json:"args"`
}

// chunkRowWire 是一行存储记录在介质上的样子。
//
// seq0 与 time0 锚住第一个成员：第 k 个成员的 seq 是 seq0 + k，
// 时间是 time0 加上前 k 个间隔。间隔可以是负的——两条事件之间墙上时钟可能倒退。
type chunkRowWire struct {
	Type  RowType         `json:"type"`
	Seq0  int             `json:"seq0"`
	Time0 int64           `json:"time0"`
	Data  json.RawMessage `json:"data"`
}

// buildRow 给一串攒够了的成员排出那一行。
//
// 源: packages/core/session/src/chunk-rows.ts:154-180
func buildRow(run []deltaEvent) (json.RawMessage, error) {
	first := run[0]
	gaps := make([]int64, 0, len(run)-1)
	for index := 1; index < len(run); index++ {
		gaps = append(gaps, run[index].time-run[index-1].time)
	}

	var (
		rowType RowType
		payload any
	)
	switch first.kind {
	case llm.ChunkToolCallDelta:
		args := make([]string, 0, len(run))
		for _, member := range run {
			args = append(args, member.args)
		}
		rowType = RowToolCallChunks
		payload = toolCallRunData{
			Turn: first.turn, Step: first.step, Index: first.index, Dt: gaps,
			ID: first.id, Name: first.name, Args: args,
		}
	default:
		texts := make([]string, 0, len(run))
		for _, member := range run {
			texts = append(texts, member.text)
		}
		rowType = RowTextChunks
		if first.kind == llm.ChunkReasoningDelta {
			rowType = RowReasoningChunks
		}
		payload = textRunData{
			Turn: first.turn, Step: first.step, Index: first.index, Dt: gaps, Texts: texts,
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, wrapMalformed("分块行的负载排不出去", err)
	}
	return json.Marshal(chunkRowWire{Type: rowType, Seq0: first.seq, Time0: first.time, Data: data})
}

// PackChunkRuns 把一批事件编码成待写的存储记录，一条一行。
//
// 源: packages/core/session/src/chunk-rows.ts:204-243（packChunkRuns）
//
// 每一串至少 [minRun] 条、连续、同类、同一块的白名单增量分块压成一行；
// 别的事件按顺序原样透传。纯函数、无状态——对任何一个切片都安全，
// 包括一串被落盘边界切开的批次（切开的两截各压各的）。
//
// 新增: DSH 返回的是一个 StorageRecord 联合类型的数组，由调用方再各自
// JSON.stringify。这里直接返回排好的字节：Go 里「一行一条 JSON」的自然形状
// 就是一段字节，多立一个只为了让调用方立刻把它排掉的联合类型没有收益。
func PackChunkRuns(events []Event) ([]json.RawMessage, error) {
	var (
		out []json.RawMessage
		run []deltaEvent
	)
	flush := func() error {
		if len(run) >= minRun {
			row, err := buildRow(run)
			if err != nil {
				return err
			}
			out = append(out, row)
			run = nil
			return nil
		}
		for _, member := range run {
			line, err := json.Marshal(member.event)
			if err != nil {
				return wrapMalformed("事件排不出去", err)
			}
			out = append(out, line)
		}
		run = nil
		return nil
	}

	for _, event := range events {
		delta, packable := classify(event)
		if !packable {
			if err := flush(); err != nil {
				return nil, err
			}
			line, err := json.Marshal(event)
			if err != nil {
				return nil, wrapMalformed("事件排不出去", err)
			}
			out = append(out, line)
			continue
		}
		if len(run) > 0 && run[len(run)-1].kind == delta.kind && continues(run[len(run)-1], delta) {
			run = append(run, delta)
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		run = []deltaEvent{delta}
	}
	return out, flush()
}

// DecodeStorageRecord 把一行解出来的 JSON 值还原成它存着的那一条或那一串事件。
//
// 源: packages/core/session/src/chunk-rows.ts:354-370（decodeStorageRecord）
//
// 带行标签的值先验后展开——一行坏掉的记录当场报错，因为那是坏掉的存储，
// 把它当成一条事件收下会默默丢掉一整串。别的值按一条事件读回来。
func DecodeStorageRecord(line json.RawMessage) ([]Event, error) {
	var probe struct {
		Type RowType `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, wrapMalformed("存储记录不是一个对象", err)
	}
	switch probe.Type {
	case RowTextChunks, RowReasoningChunks, RowToolCallChunks:
		return expandRow(line, probe.Type)
	default:
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		return []Event{event}, nil
	}
}

// expandRow 验一行带行标签的记录，再把它展开回一模一样的原事件。
//
// 源: packages/core/session/src/chunk-rows.ts:248-328
func expandRow(line json.RawMessage, tag RowType) ([]Event, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return nil, wrapMalformed("分块行不是一个对象", err)
	}
	if len(fields) != 4 ||
		fields["type"] == nil || fields["seq0"] == nil || fields["time0"] == nil || fields["data"] == nil {
		return nil, malformed("%s 行的信封必须恰好是 {type, seq0, time0, data}", tag)
	}
	var wire chunkRowWire
	if err := json.Unmarshal(line, &wire); err != nil {
		return nil, wrapMalformed("分块行读不回来", err)
	}
	if wire.Seq0 < 0 {
		return nil, malformed("%s 行的 seq0 必须非负，实际 %d", tag, wire.Seq0)
	}

	var dataFields map[string]json.RawMessage
	if err := json.Unmarshal(wire.Data, &dataFields); err != nil {
		return nil, wrapMalformed("分块行的负载不是一个对象", err)
	}

	var (
		turn, step, index int
		gaps              []int64
		members           []string
		makeChunk         func(text string) llm.StreamChunk
	)

	if tag == RowToolCallChunks {
		hasName := dataFields["name"] != nil
		wanted := 6
		if hasName {
			wanted = 7
		}
		if len(dataFields) != wanted || dataFields["turn"] == nil || dataFields["step"] == nil ||
			dataFields["index"] == nil || dataFields["id"] == nil ||
			dataFields["dt"] == nil || dataFields["args"] == nil {
			return nil, malformed("%s 行的负载必须恰好是 {turn, step, index, dt, id, name?, args}", tag)
		}
		var data toolCallRunData
		if err := json.Unmarshal(wire.Data, &data); err != nil {
			return nil, wrapMalformed("工具调用分块行的负载读不回来", err)
		}
		turn, step, index, gaps, members = data.Turn, data.Step, data.Index, data.Dt, data.Args
		makeChunk = func(text string) llm.StreamChunk {
			return llm.ToolCallDeltaChunk{Index: data.Index, ID: data.ID, Name: data.Name, ArgumentsDelta: text}
		}
	} else {
		if len(dataFields) != 5 || dataFields["turn"] == nil || dataFields["step"] == nil ||
			dataFields["index"] == nil || dataFields["dt"] == nil || dataFields["texts"] == nil {
			return nil, malformed("%s 行的负载必须恰好是 {turn, step, index, dt, texts}", tag)
		}
		var data textRunData
		if err := json.Unmarshal(wire.Data, &data); err != nil {
			return nil, wrapMalformed("文本分块行的负载读不回来", err)
		}
		turn, step, index, gaps, members = data.Turn, data.Step, data.Index, data.Dt, data.Texts
		makeChunk = func(text string) llm.StreamChunk {
			if tag == RowReasoningChunks {
				return llm.ReasoningDeltaChunk{Index: index, Text: text}
			}
			return llm.TextDeltaChunk{Index: index, Text: text}
		}
	}

	if len(members) == 0 {
		return nil, malformed("%s 行的成员清单不能是空的", tag)
	}
	if len(gaps) != len(members)-1 {
		return nil, malformed("%s 行的 dt 长度 %d 对不上 %d 个成员", tag, len(gaps), len(members))
	}
	// 重建的边界。编码方只压那些 seq 与时间都在范围内的串，所以一个跑出范围的
	// 中间值不在任何编码方的像里——继续算下去只会得到一个和原值不同的数，
	// 那是一次静默的损坏。
	if len(members)-1 > math.MaxInt-wire.Seq0 {
		return nil, malformed("%s 行的成员 seq 溢出", tag)
	}

	events := make([]Event, 0, len(members))
	timestamp := wire.Time0
	for position, text := range members {
		if position > 0 {
			if addOverflows(timestamp, gaps[position-1]) {
				return nil, malformed("%s 行的成员时间戳溢出", tag)
			}
			timestamp += gaps[position-1]
		}
		payload, err := json.Marshal(AssistantChunkData{Turn: turn, Step: step, Chunk: makeChunk(text)})
		if err != nil {
			return nil, wrapMalformed("展开出来的分块事件排不出去", err)
		}
		events = append(events, Event{
			Type: EventAssistantChunk,
			Seq:  wire.Seq0 + position,
			Time: timestamp,
			Data: payload,
		})
	}
	return events, nil
}

// subOverflows 判断 a - b 会不会溢出 int64。
//
// 新增: DSH 在同一处用 Number.isSafeInteger 挡的是四舍五入，Go 这边要挡的是回绕。
func subOverflows(a, b int64) bool {
	difference := a - b
	return (a >= 0) != (b >= 0) && (difference >= 0) != (a >= 0)
}

// addOverflows 判断 a + b 会不会溢出 int64。
func addOverflows(a, b int64) bool {
	sum := a + b
	return (a >= 0) == (b >= 0) && (sum >= 0) != (a >= 0)
}
