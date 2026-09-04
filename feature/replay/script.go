// 本文件的作用：剧本本身——一次录下来的调用长什么样、一段会话日志怎么读回事件、
// 怎么从事件推出剧本，以及那份旁挂文件怎么验。
//
// 源: packages/test-support/llm-replay/src/index.ts

package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// EntryKind 是一条录下来的调用的判别标签。
type EntryKind string

const (
	// EntryChunks 是一次正常吐完的流。
	EntryChunks EntryKind = "chunks"
	// EntryThrow 是一次先吐了若干块、然后抛出来的流。
	EntryThrow EntryKind = "throw"
	// EntryHang 是一次挂住等取消的流。
	EntryHang EntryKind = "hang"
)

// Entry 是一次录下来的模型调用。
//
// 源: packages/test-support/llm-replay/src/index.ts:37-45
//
// [ThrowEntry] 可以在失败之前先放一段前缀分块；[HangEntry] 演的是取消。推导出来的
// [ChunksEntry] 来自普通的模型流、以及那些明确标了记号的本地压缩调用；旁挂文件供得出
// 这三种里的任何一种。
//
// 新增: DSH 那边是一个带 kind 字段的可判别联合。Go 这边照本仓库对联合的一贯做法办
// （成例见 [github.com/snight1983/ds-harness-go/llm.StreamChunk] 和 [github.com/snight1983/ds-harness-go/llm.ContentBlock]）：
// 一个封闭接口加三个具体类型，于是「漏了一个分支」由类型开关的 default 当场接住，
// 正是 DSH 那句 assertNever 想要的效果。
type Entry interface {
	// Kind 是这条条目的判别标签。
	Kind() EntryKind

	// sealedEntry 把实现方封在本包内。
	sealedEntry()
}

// ChunksEntry 是一次正常吐完的流：把这些分块按序交出去就完了。
type ChunksEntry struct {
	// Chunks 是这次调用录下来的那串分块，最后一块是 finish。
	Chunks []llm.StreamChunk
}

// Kind 实现 [Entry]。
func (ChunksEntry) Kind() EntryKind { return EntryChunks }

func (ChunksEntry) sealedEntry() {}

// ThrowEntry 是一次先吐了若干块、然后抛出来的流。
//
// 前缀分块照样交出去，好让循环看到它当初活着时看到的那半截输出；然后交出那个录下来
// 的错误（比如提供方的 401，或者吐了一半之后的 STREAM_CLOSED）。
type ThrowEntry struct {
	// Chunks 是抛出来之前吐掉的那些分块；可以是空的。
	Chunks []llm.StreamChunk
	// Message 是那个错误的说明文字。
	Message string
	// Code 是那个错误的机器可读代码。
	Code string
}

// Kind 实现 [Entry]。
func (ThrowEntry) Kind() EntryKind { return EntryThrow }

func (ThrowEntry) sealedEntry() {}

// HangEntry 是一次挂住等取消的流。
type HangEntry struct {
	// ReadyFile 是前缀分块被取走之后、开始等取消之前落下的那个标记文件；
	// 空串表示不落。
	ReadyFile string
}

// Kind 实现 [Entry]。
func (HangEntry) Kind() EntryKind { return EntryHang }

func (HangEntry) sealedEntry() {}

// MarshalJSON 把这条条目排成旁挂文件里的那个形状。
func (e ChunksEntry) MarshalJSON() ([]byte, error) {
	chunks := e.Chunks
	if chunks == nil {
		chunks = []llm.StreamChunk{}
	}
	return json.Marshal(struct {
		Kind   EntryKind         `json:"kind"`
		Chunks []llm.StreamChunk `json:"chunks"`
	}{Kind: EntryChunks, Chunks: chunks})
}

// MarshalJSON 把这条条目排成旁挂文件里的那个形状。
func (e ThrowEntry) MarshalJSON() ([]byte, error) {
	chunks := e.Chunks
	if chunks == nil {
		chunks = []llm.StreamChunk{}
	}
	return json.Marshal(struct {
		Kind    EntryKind         `json:"kind"`
		Chunks  []llm.StreamChunk `json:"chunks"`
		Message string            `json:"message"`
		Code    string            `json:"code"`
	}{Kind: EntryThrow, Chunks: chunks, Message: e.Message, Code: e.Code})
}

// MarshalJSON 把这条条目排成旁挂文件里的那个形状。
//
// ReadyFile 是空串时那个键根本不出现，好让一趟往返回得到同一份文档——
// 读的那一侧对 hang 条目的键集是精确匹配的。
func (e HangEntry) MarshalJSON() ([]byte, error) {
	if e.ReadyFile == "" {
		return json.Marshal(struct {
			Kind EntryKind `json:"kind"`
		}{Kind: EntryHang})
	}
	return json.Marshal(struct {
		Kind      EntryKind `json:"kind"`
		ReadyFile string    `json:"readyFile"`
	}{Kind: EntryHang, ReadyFile: e.ReadyFile})
}

// ErrInvalidOverride 是「那份旁挂文件本身不成立」。
var ErrInvalidOverride = errors.New("llm-replay: 旁挂文件不成立")

// ErrUnrecoverableScript 是「这段日志推不出剧本，场景得交一份旁挂文件」。
var ErrUnrecoverableScript = errors.New("llm-replay: 这段日志推不出剧本")

// ErrMalformedFixture 是「这份夹具读不回来」。
var ErrMalformedFixture = errors.New("llm-replay: 夹具读不回来")

// invalidOverride 拼一句指得到位置的旁挂文件错误。
//
// 源: packages/test-support/llm-replay/src/index.ts:427-429
func invalidOverride(file, location, detail string) error {
	return fmt.Errorf("%w：%s：%s %s", ErrInvalidOverride, file, location, detail)
}

// packedRowTypes 是三种打包过的分块行标签，它们的信封用 seq0/time0 而不是 seq/time。
//
// 源: packages/test-support/llm-replay/src/index.ts:29
var packedRowTypes = map[sessionlog.RowType]bool{
	sessionlog.RowTextChunks:      true,
	sessionlog.RowReasoningChunks: true,
	sessionlog.RowToolCallChunks:  true,
}

// ParseSessionLog 把一份会话 .jsonl 的正文读成它那串事件。
//
// 源: packages/test-support/llm-replay/src/index.ts:171-224（parseSessionLog）
//
// 第 0 行是会话头（一条 `{type:"session",…}` 记录），此后每一个非空行是一条
// [github.com/snight1983/ds-harness-go/sessionlog.Event] 或者一行打包过的分块行（展开回它那串事件，于是一份
// 开着 packChunks 录下来的夹具推得出同一份剧本）。头被跳过；坏掉的行当场失败。
//
// 投影出来的夹具会把事件信封省掉，所以解码之前先把缺的 seq/time 补上，调用方拿到的
// 仍旧是完整的事件值。
func ParseSessionLog(text string) ([]sessionlog.Event, error) {
	var events []sessionlog.Event
	nextSeq := 0
	headerSkipped := false
	for index, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !headerSkipped {
			headerSkipped = true
			continue
		}
		fields, err := decodeRecordFields(line, index+1)
		if err != nil {
			return nil, err
		}
		fillEnvelope(fields, nextSeq)
		record, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("%w：会话快照第 %d 行排不回去：%w", ErrMalformedFixture, index+1, err)
		}
		decoded, err := sessionlog.DecodeStorageRecord(record)
		if err != nil {
			return nil, fmt.Errorf("%w：会话快照第 %d 行：%w", ErrMalformedFixture, index+1, err)
		}
		events = append(events, decoded...)
		nextSeq += len(decoded)
	}
	return events, nil
}

// decodeRecordFields 把一行读成它的字段表，并把「不是合法 JSON」和「不是一个对象」
// 分成两句话——DSH 那边靠 JSON.parse 抛不抛来分，Go 这边靠错误类型。
func decodeRecordFields(line string, lineNumber int) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, fmt.Errorf("%w：会话快照第 %d 行不是合法 JSON：%w", ErrMalformedFixture, lineNumber, err)
		}
		return nil, fmt.Errorf("%w：会话快照第 %d 行必须是一个 JSON 对象", ErrMalformedFixture, lineNumber)
	}
	if fields == nil {
		return nil, fmt.Errorf("%w：会话快照第 %d 行必须是一个 JSON 对象", ErrMalformedFixture, lineNumber)
	}
	return fields, nil
}

// fillEnvelope 把一条投影记录缺掉的信封字段补上。
//
// 打包行的信封字段叫 seq0/time0，普通事件叫 seq/time；补的序号从上一条已解出来的
// 事件往下接，于是一份整份都省了信封的夹具解出来仍旧是连号的。
func fillEnvelope(fields map[string]json.RawMessage, nextSeq int) {
	seqKey, timeKey := "seq", "time"
	if tag, ok := fields["type"]; ok {
		var name sessionlog.RowType
		if json.Unmarshal(tag, &name) == nil && packedRowTypes[name] {
			seqKey, timeKey = "seq0", "time0"
		}
	}
	if _, ok := fields[seqKey]; !ok {
		fields[seqKey] = json.RawMessage(strconv.Itoa(nextSeq))
	}
	if _, ok := fields[timeKey]; !ok {
		fields[timeKey] = json.RawMessage("0")
	}
}

// ParseSessionHeader 从 .jsonl 的头行读出回放要的身份、次序和 seed 边界。
//
// 源: packages/test-support/llm-replay/src/index.ts:226-240（parseSessionHeader）
//
// 新增: DSH 手挑 id / createdAt / seedLength 三个字段并逐个 typeof 判类型、类型不对
// 就静默回落到默认值，是因为它那边没有一个认得这份头的解码器。Go 这边有，所以整行
// 直接落进 [github.com/snight1983/ds-harness-go/sessionlog.SessionHeader]，一份类型不对的头**当场失败**而不是
// 悄悄变成默认值——夹具里的头是录出来的，它长错了只可能是夹具坏了。
//
// 整份文本一个非空行都没有时交回零值头，和 DSH 拿 `{}` 兜底同义。
func ParseSessionHeader(text string) (sessionlog.SessionHeader, error) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var header sessionlog.SessionHeader
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return sessionlog.SessionHeader{}, fmt.Errorf("%w：会话头读不回来：%w", ErrMalformedFixture, err)
		}
		return header, nil
	}
	return sessionlog.SessionHeader{}, nil
}

// DeriveScript 从一段录好的会话日志重建出那份按 stream() 调用切开的剧本。
//
// 源: packages/test-support/llm-replay/src/index.ts:242-312（deriveReplayScript）
//
// 把 assistant/chunk 在每一个 finish 处切开，靠 turn 和 step 的变化发现上一次调用
// 没有收尾。一条明确标了「它是一次本地 LLM 流式调用」的 compaction/summary，在它
// 那个日志位置上由完整的 rawOutput 重造成一次规范的成功流。缺终止块意味着那次流是
// 抛出来的，于是推导拒绝，场景必须交一份旁挂文件。循环重试时几次调用可以共用同一个
// turn 和 step。
func DeriveScript(events []sessionlog.Event) ([]Entry, error) {
	var script []Entry
	var currentKey string
	var current []llm.StreamChunk

	flush := func(key string, chunks []llm.StreamChunk) error {
		if len(chunks) == 0 {
			return nil
		}
		if chunks[len(chunks)-1].ChunkType() != llm.ChunkFinish {
			return fmt.Errorf("%w：模型调用 %s 没有以 finish 分块收尾（那次流是抛出来的），"+
				"这个场景需要一份 replay.override.json 旁挂文件", ErrUnrecoverableScript, key)
		}
		script = append(script, ChunksEntry{Chunks: chunks})
		return nil
	}

	for _, event := range events {
		if event.Type == compaction.EventCompactionSummary {
			if err := flush(currentKey, current); err != nil {
				return nil, err
			}
			currentKey, current = "", nil
			entry, ok, err := compactionEntry(event)
			if err != nil {
				return nil, err
			}
			if ok {
				script = append(script, entry)
			}
			continue
		}
		if event.Type != sessionlog.EventAssistantChunk {
			continue
		}
		var data sessionlog.AssistantChunkData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, fmt.Errorf("%w：assistant/chunk 的负载读不回来：%w", ErrMalformedFixture, err)
		}
		key := strconv.Itoa(data.Turn) + "/" + strconv.Itoa(data.Step)
		if len(current) > 0 && key != currentKey {
			// 这里 flush 必然报错：current 还非空，就意味着它最后一块不是 finish。
			if err := flush(currentKey, current); err != nil {
				return nil, err
			}
		}
		if len(current) == 0 {
			currentKey = key
		}
		current = append(current, data.Chunk)
		if data.Chunk.ChunkType() == llm.ChunkFinish {
			if err := flush(currentKey, current); err != nil {
				return nil, err
			}
			currentKey, current = "", nil
		}
	}
	if err := flush(currentKey, current); err != nil {
		return nil, err
	}
	return script, nil
}

// compactionEntry 把一条 compaction/summary 变成它那次调用的条目；
// 第二个返回值为假表示这条摘要不是走 LLM 接缝做出来的，剧本里没有它的位置。
//
// 源: packages/test-support/llm-replay/src/index.ts:249-273
//
// 新增: 这里读的是一个只有三个字段的窄结构，而不是整份
// [github.com/snight1983/ds-harness-go/feature/compaction.SummaryData]。理由和 DSH 那句「JSONL 解码跨的是一道没有
// 类型的持久边界」是同一条：推导只关心那三件事，拿整份负载去解会让一条与回放无关的
// 字段变化把整段推导拖垮。
func compactionEntry(event sessionlog.Event) (Entry, bool, error) {
	var persisted struct {
		LLMStreamCall bool            `json:"llmStreamCall"`
		RawOutput     *llm.Content    `json:"rawOutput"`
		Usage         *llm.TokenUsage `json:"usage"`
	}
	if err := json.Unmarshal(event.Data, &persisted); err != nil {
		return nil, false, fmt.Errorf("%w：compaction/summary 的负载读不回来：%w", ErrMalformedFixture, err)
	}
	if !persisted.LLMStreamCall {
		return nil, false, nil
	}
	if persisted.RawOutput == nil {
		return nil, false, fmt.Errorf(
			"%w：compaction/summary 标了它是一次 LLM 流式调用却没带 rawOutput", ErrMalformedFixture)
	}
	var chunks []llm.StreamChunk
	for index, block := range *persisted.RawOutput {
		chunks = append(chunks,
			llm.BlockStartChunk{Index: index, BlockType: block.BlockType()},
			llm.BlockEndChunk{Index: index, Block: block})
	}
	if persisted.Usage != nil {
		chunks = append(chunks, llm.UsageChunk{Usage: *persisted.Usage})
	}
	chunks = append(chunks, llm.FinishChunk{Reason: llm.StopFinish{}})
	return ChunksEntry{Chunks: chunks}, true, nil
}

// OverridePatch 是增补形式旁挂文件里的一条定位补丁。
//
// 源: packages/test-support/llm-replay/src/index.ts:314-325（ReplayOverridePatch）
//
// 它把推导出来的第 At 次调用（0 基）换成 Entry；At 等于推导长度时是追加——那是一次
// 事后才录下来的调用，比如一次注入的瞬时失败之后那次重试。
type OverridePatch struct {
	// At 是这条补丁打在第几次调用上（0 基）；等于推导长度时追加。
	At int
	// Entry 是那个位置上的替换（或追加）条目。
	Entry Entry
}

// OverrideDoc 是一份旁挂文件读回来的样子。
//
// 源: packages/test-support/llm-replay/src/index.ts:327-333（ReplayOverrideDoc）
//
// 新增: DSH 那边是 `ReplayEntry[] | { patches }` 这个联合。Go 这边合成一个结构体，
// 靠 Replacement 是不是 nil 来分：整份替换和空替换（一份**零次调用**的剧本）因此仍旧
// 分得开，而这正是 `[]` 和 `{"patches":[]}` 在 DSH 那边的区别。
type OverrideDoc struct {
	// Replacement 非 nil 表示这份文件是整份替换，它就是那份剧本。
	Replacement []Entry
	// Patches 是增补形式的那些补丁；Replacement 为 nil 时才有意义。
	Patches []OverridePatch
}

// readChunks 读一条条目里的那串分块，并验每一块的判别标签认识。
//
// 源: packages/test-support/llm-replay/src/index.ts:431-441
//
// 新增: DSH 拿一个 REPLAY_CHUNK_TYPES 集合逐个比对字符串。Go 这边
// [github.com/snight1983/ds-harness-go/llm.UnmarshalStreamChunk] 本来就对不认识的标签交回
// [github.com/snight1983/ds-harness-go/llm.ErrUnknownChunkType]，那张手抄的集合因此不必存在——而且顺带
// 把每一块的**字段**也验了，DSH 那边只验了标签。
func readChunks(raw json.RawMessage, file, location string) ([]llm.StreamChunk, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, invalidOverride(file, location, "chunks 必须是一个数组")
	}
	chunks := make([]llm.StreamChunk, 0, len(items))
	for index, item := range items {
		chunk, err := llm.UnmarshalStreamChunk(item)
		if err != nil {
			return nil, invalidOverride(file, fmt.Sprintf("%s.chunks[%d]", location, index), err.Error())
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// hasExactKeys 判一个字段表的键集是不是恰好是给定的那几个。
//
// 源: packages/test-support/llm-replay/src/index.ts:423-425
func hasExactKeys(fields map[string]json.RawMessage, keys ...string) bool {
	if len(fields) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

// readEntry 读一条条目，键集精确匹配。
//
// 源: packages/test-support/llm-replay/src/index.ts:443-478
//
// 键集精确匹配而不是宽松解码，是因为一个拼错的字段（`readyfile`、`msg`）在宽松解码
// 下会静默变成默认值，于是那个场景演的是另一件事而没有人知道。
func readEntry(raw json.RawMessage, file, location string) (Entry, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalidOverride(file, location, "必须是一个对象")
	}
	var kind EntryKind
	if tag, ok := fields["kind"]; !ok || json.Unmarshal(tag, &kind) != nil {
		return nil, invalidOverride(file, location, "的 kind 不认识")
	}
	switch kind {
	case EntryChunks:
		if !hasExactKeys(fields, "kind", "chunks") {
			return nil, invalidOverride(file, location, "的 chunks 条目字段不对")
		}
		chunks, err := readChunks(fields["chunks"], file, location)
		if err != nil {
			return nil, err
		}
		return ChunksEntry{Chunks: chunks}, nil
	case EntryThrow:
		if !hasExactKeys(fields, "kind", "chunks", "message", "code") {
			return nil, invalidOverride(file, location, "的 throw 条目字段不对")
		}
		var message, code string
		if json.Unmarshal(fields["message"], &message) != nil || message == "" {
			return nil, invalidOverride(file, location, "的 message 必须是一个非空字符串")
		}
		if json.Unmarshal(fields["code"], &code) != nil || code == "" {
			return nil, invalidOverride(file, location, "的 code 必须是一个非空字符串")
		}
		chunks, err := readChunks(fields["chunks"], file, location)
		if err != nil {
			return nil, err
		}
		return ThrowEntry{Chunks: chunks, Message: message, Code: code}, nil
	case EntryHang:
		readyRaw, given := fields["readyFile"]
		if given {
			if !hasExactKeys(fields, "kind", "readyFile") {
				return nil, invalidOverride(file, location, "的 hang 条目字段不对")
			}
			var readyFile string
			if json.Unmarshal(readyRaw, &readyFile) != nil || readyFile == "" {
				return nil, invalidOverride(file, location, "的 readyFile 必须是一个非空字符串")
			}
			return HangEntry{ReadyFile: readyFile}, nil
		}
		if !hasExactKeys(fields, "kind") {
			return nil, invalidOverride(file, location, "的 hang 条目字段不对")
		}
		return HangEntry{}, nil
	default:
		return nil, invalidOverride(file, location, fmt.Sprintf("的 kind %q 不认识", kind))
	}
}

// readOverrideDoc 读一份旁挂文件：一整份 [Entry] 数组是替换，`{"patches": [...]}`
// 是增补。
//
// 源: packages/test-support/llm-replay/src/index.ts:480-505
func readOverrideDoc(data []byte, file string) (OverrideDoc, error) {
	var probe json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return OverrideDoc{}, invalidOverride(file, "document", "不是合法 JSON")
	}
	trimmed := strings.TrimLeft(string(probe), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return OverrideDoc{}, invalidOverride(file, "document", "不是一个合法的条目数组")
		}
		entries := make([]Entry, 0, len(items))
		for index, item := range items {
			entry, err := readEntry(item, file, fmt.Sprintf("entry %d", index))
			if err != nil {
				return OverrideDoc{}, err
			}
			entries = append(entries, entry)
		}
		return OverrideDoc{Replacement: entries}, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || !hasExactKeys(fields, "patches") {
		return OverrideDoc{}, invalidOverride(file, "document", "必须是一个 Entry 数组或者 { patches: [...] }")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(fields["patches"], &items); err != nil {
		return OverrideDoc{}, invalidOverride(file, "document", "必须是一个 Entry 数组或者 { patches: [...] }")
	}
	patches := make([]OverridePatch, 0, len(items))
	for index, item := range items {
		location := fmt.Sprintf("patch %d", index)
		var patchFields map[string]json.RawMessage
		if err := json.Unmarshal(item, &patchFields); err != nil || !hasExactKeys(patchFields, "at", "entry") {
			return OverrideDoc{}, invalidOverride(file, location, "必须恰好带着 at 和 entry")
		}
		var at int
		if json.Unmarshal(patchFields["at"], &at) != nil || at < 0 {
			return OverrideDoc{}, invalidOverride(file, location, "的 at 必须是一个非负整数")
		}
		entry, err := readEntry(patchFields["entry"], file, location+".entry")
		if err != nil {
			return OverrideDoc{}, err
		}
		patches = append(patches, OverridePatch{At: at, Entry: entry})
	}
	return OverrideDoc{Patches: patches}, nil
}
