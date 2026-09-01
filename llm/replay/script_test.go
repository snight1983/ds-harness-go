// 本文件的作用：把「一份录下来的日志怎么变成剧本」这条路上的每一步各验一遍——
// 读日志、读头、切调用、以及那份旁挂文件在读进来的那一刻要挡住的每一种坏形状。

package replay

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

func TestParseSessionLogSkipsTheHeaderAndBlankLines(t *testing.T) {
	text := headerLine(t, "s1", 0, 0) + "\n\n" +
		chunkLine(t, 1, 1, 1, llm.TextDeltaChunk{Index: 0, Text: "a"}) + "\n\n"
	events, err := ParseSessionLog(text)
	if err != nil {
		t.Fatalf("读日志失败：%v", err)
	}
	if len(events) != 1 || events[0].Type != session.EventAssistantChunk {
		t.Fatalf("要一条 assistant/chunk，实际 %+v", events)
	}
}

func TestParseSessionLogRejectsARowThatIsNotAnObject(t *testing.T) {
	// `null` 解成一张空表、`[1,2]` 解出类型错——两条不同的路，同一句话。
	for name, row := range map[string]string{"null": "null", "数组": "[1,2]"} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSessionLog(sessionJSONL(headerLine(t, "s1", 0, 0), row))
			if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "第 2 行") {
				t.Fatalf("要一句指到第 2 行的 ErrMalformedFixture，实际 %v", err)
			}
		})
	}
}

func TestEntryKindsAreSealed(t *testing.T) {
	// 三条条目各自报得出自己的 kind，而那个封印方法只有本包实现得了。
	for _, entry := range []Entry{ChunksEntry{}, ThrowEntry{}, HangEntry{}} {
		entry.sealedEntry()
	}
	want := []EntryKind{EntryChunks, EntryThrow, EntryHang}
	for index, entry := range []Entry{ChunksEntry{}, ThrowEntry{}, HangEntry{}} {
		if entry.Kind() != want[index] {
			t.Fatalf("第 %d 条条目的 kind 要 %s，实际 %s", index, want[index], entry.Kind())
		}
	}
}

func TestParseSessionLogRejectsARowThatIsNotJSON(t *testing.T) {
	_, err := ParseSessionLog(headerLine(t, "s1", 0, 0) + "\n{oops\n")
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("要一句「不是合法 JSON」，实际 %v", err)
	}
}

func TestParseSessionLogRejectsARowThatDecodesToNoEvent(t *testing.T) {
	// 一条带了信封之外字段的记录：解码器那一层报错，本包把行号补上去。
	line := `{"type":"turn/start","seq":1,"time":0,"data":{"turn":1},"bogus":1}`
	_, err := ParseSessionLog(sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "第 2 行") {
		t.Fatalf("要一句指到第 2 行的 ErrMalformedFixture，实际 %v", err)
	}
}

func TestParseSessionLogExpandsAPackedChunkRow(t *testing.T) {
	row := mustJSON(t, map[string]any{
		"type":  "text-chunks",
		"seq0":  1,
		"time0": 0,
		"data":  map[string]any{"turn": 1, "step": 1, "index": 0, "dt": []int{0, 0}, "texts": []string{"a", "b", "c"}},
	})
	events, err := ParseSessionLog(sessionJSONL(headerLine(t, "s1", 0, 0), row))
	if err != nil {
		t.Fatalf("读打包行失败：%v", err)
	}
	if len(events) != 3 {
		t.Fatalf("一行打包行要展开成 3 条事件，实际 %d", len(events))
	}
	for index, want := range []string{"a", "b", "c"} {
		var data session.AssistantChunkData
		if err := json.Unmarshal(events[index].Data, &data); err != nil {
			t.Fatalf("读第 %d 条负载失败：%v", index, err)
		}
		delta, ok := data.Chunk.(llm.TextDeltaChunk)
		if !ok || delta.Text != want {
			t.Fatalf("第 %d 条要 %q，实际 %+v", index, want, data.Chunk)
		}
	}
}

func TestParseSessionLogSynthesizesOmittedEnvelopes(t *testing.T) {
	// 投影出来的夹具把 seq/time 省了；补出来的序号要从上一条解出来的事件往下接。
	ordinary := `{"type":"turn/start","data":{"turn":1}}`
	packed := mustJSON(t, map[string]any{
		"type": "text-chunks",
		"data": map[string]any{"turn": 1, "step": 1, "index": 0, "dt": []int{3}, "texts": []string{"a", "b"}},
	})
	events, err := ParseSessionLog(sessionJSONL(headerLine(t, "s1", 7, 0), ordinary, packed))
	if err != nil {
		t.Fatalf("读省了信封的夹具失败：%v", err)
	}
	wantSeq := []int{0, 1, 2}
	if len(events) != len(wantSeq) {
		t.Fatalf("要 %d 条事件，实际 %d", len(wantSeq), len(events))
	}
	for index, want := range wantSeq {
		if events[index].Seq != want {
			t.Fatalf("第 %d 条的 seq 要 %d，实际 %d", index, want, events[index].Seq)
		}
	}
}

func TestParseSessionHeaderReadsIdentityOrderAndSeed(t *testing.T) {
	header, err := ParseSessionHeader(sessionJSONL(headerLine(t, "child", 7, 4)))
	if err != nil {
		t.Fatalf("读头失败：%v", err)
	}
	if header.ID != "child" || header.CreatedAt != 7 || header.SeedLength != 4 {
		t.Fatalf("头读错了：%+v", header)
	}
}

func TestParseSessionHeaderFallsBackOnAnEmptyBuffer(t *testing.T) {
	header, err := ParseSessionHeader("")
	if err != nil {
		t.Fatalf("空文本不该报错：%v", err)
	}
	if header != (session.SessionHeader{}) {
		t.Fatalf("空文本要交零值头，实际 %+v", header)
	}
}

func TestParseSessionHeaderRejectsAHeaderThatDoesNotDecode(t *testing.T) {
	// 夹具里的头是录出来的，它长错了只可能是夹具坏了——所以当场失败而不是回落默认值。
	_, err := ParseSessionHeader(`{"type":"session","id":42}` + "\n")
	if !errors.Is(err, ErrMalformedFixture) {
		t.Fatalf("要 ErrMalformedFixture，实际 %v", err)
	}
}

// deriveFrom 把一份夹具文本读成剧本，读日志那一步失败当场判死。
func deriveFrom(t *testing.T, text string) ([]Entry, error) {
	t.Helper()
	events, err := ParseSessionLog(text)
	if err != nil {
		t.Fatalf("读日志失败：%v", err)
	}
	return DeriveScript(events)
}

func TestDeriveScriptGroupsOneFinishedStreamIntoOneEntry(t *testing.T) {
	script, err := deriveFrom(t, callsJSONL(t, "s1", 0, textChunks()))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	want := []Entry{ChunksEntry{Chunks: textChunks()}}
	if !reflect.DeepEqual(script, want) {
		t.Fatalf("剧本不对：%+v", script)
	}
}

func TestDeriveScriptSeparatesRetriesThatShareOneTurnAndStep(t *testing.T) {
	failed := []llm.StreamChunk{
		llm.UsageChunk{Usage: llm.TokenUsage{}},
		llm.FinishChunk{Reason: llm.ErrorFinish{Failure: llm.Failure{Message: "empty", Code: "EMPTY_RESPONSE"}}},
	}
	var lines []string
	seq := 1
	for _, chunk := range append(append([]llm.StreamChunk{}, failed...), textChunks()...) {
		lines = append(lines, chunkLine(t, seq, 1, 1, chunk))
		seq++
	}
	script, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), lines...))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	if len(script) != 2 {
		t.Fatalf("同一个 turn/step 上的两次调用要切成两条，实际 %d", len(script))
	}
}

func TestDeriveScriptProducesOneEntryPerCall(t *testing.T) {
	script, err := deriveFrom(t, callsJSONL(t, "s1", 0, textChunks(), shortChunks("two")))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	want := []Entry{ChunksEntry{Chunks: textChunks()}, ChunksEntry{Chunks: shortChunks("two")}}
	if !reflect.DeepEqual(script, want) {
		t.Fatalf("剧本不对：%+v", script)
	}
}

func TestDeriveScriptIgnoresOtherEventsAndAnEmptyLog(t *testing.T) {
	lines := []string{`{"type":"turn/start","seq":1,"time":0,"data":{"turn":1}}`}
	seq := 2
	for _, chunk := range textChunks() {
		lines = append(lines, chunkLine(t, seq, 1, 1, chunk))
		seq++
	}
	script, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), lines...))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	if len(script) != 1 {
		t.Fatalf("只该有一条，实际 %d", len(script))
	}
	empty, err := DeriveScript(nil)
	if err != nil || empty != nil {
		t.Fatalf("空日志要交空剧本，实际 %v / %v", empty, err)
	}
}

func TestDeriveScriptRejectsAnAssistantChunkPayloadItCannotRead(t *testing.T) {
	// 信封是好的、负载空着：那道校验在推导这一步，不在读日志那一步。
	line := `{"type":"assistant/chunk","seq":1,"time":0,"data":{}}`
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "assistant/chunk") {
		t.Fatalf("要一句点名 assistant/chunk 的 ErrMalformedFixture，实际 %v", err)
	}
}

func TestDeriveScriptRejectsAGroupWithoutATerminalFinish(t *testing.T) {
	line := chunkLine(t, 1, 2, 3, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText})
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if !errors.Is(err, ErrUnrecoverableScript) || !strings.Contains(err.Error(), "2/3") {
		t.Fatalf("要一句点名 2/3 的 ErrUnrecoverableScript，实际 %v", err)
	}
}

func TestDeriveScriptRejectsAnUnfinishedCallBeforeANewStep(t *testing.T) {
	lines := []string{
		chunkLine(t, 1, 1, 1, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}),
		chunkLine(t, 2, 1, 2, llm.FinishChunk{Reason: llm.StopFinish{}}),
	}
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), lines...))
	if !errors.Is(err, ErrUnrecoverableScript) || !strings.Contains(err.Error(), "1/1") {
		t.Fatalf("要一句点名 1/1 的 ErrUnrecoverableScript，实际 %v", err)
	}
}

// summaryLine 拼一行 compaction/summary。
func summaryLine(t *testing.T, seq int, fields map[string]any) string {
	t.Helper()
	data := map[string]any{
		"compactionId":       "replay-compaction",
		"shadowedRange":      map[string]any{"start": 1, "end": 1},
		"shadowedSeqs":       []int{1},
		"shadowedTokenCount": 20,
		"provider":           "mock",
		"model":              "mock",
	}
	for key, value := range fields {
		data[key] = value
	}
	return mustJSON(t, map[string]any{"type": "compaction/summary", "seq": seq, "time": 0, "data": data})
}

func TestDeriveScriptRebuildsAMarkedCompactionCall(t *testing.T) {
	block := map[string]any{"type": "text", "text": "durable checkpoint"}
	line := summaryLine(t, 1, map[string]any{
		"summary":       []any{block},
		"rawOutput":     []any{block},
		"llmStreamCall": true,
		"usage":         map[string]any{"inputTokens": 9, "outputTokens": 2},
	})
	script, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	want := []Entry{ChunksEntry{Chunks: []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText},
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "durable checkpoint"}},
		llm.UsageChunk{Usage: llm.TokenUsage{InputTokens: 9, OutputTokens: 2}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}}
	if !reflect.DeepEqual(script, want) {
		t.Fatalf("重造出来的摘要流不对：%+v", script)
	}
}

func TestDeriveScriptRebuildsAMarkedCompactionCallWithoutUsage(t *testing.T) {
	block := map[string]any{"type": "text", "text": "summary without usage"}
	line := summaryLine(t, 1, map[string]any{
		"summary": []any{block}, "rawOutput": []any{block}, "llmStreamCall": true,
	})
	script, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if err != nil {
		t.Fatalf("推导失败：%v", err)
	}
	entry, ok := script[0].(ChunksEntry)
	if !ok || len(entry.Chunks) != 3 {
		t.Fatalf("没用量时要三块，实际 %+v", script)
	}
}

func TestDeriveScriptIgnoresAnUnmarkedCompactionSummary(t *testing.T) {
	block := map[string]any{"type": "text", "text": "remote summary"}
	for name, fields := range map[string]map[string]any{
		"没有 rawOutput": {"summary": []any{block}},
		"没标记":          {"summary": []any{block}, "rawOutput": []any{block}},
	} {
		t.Run(name, func(t *testing.T) {
			line := summaryLine(t, 1, fields)
			script, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
			if err != nil || script != nil {
				t.Fatalf("这条摘要不该进剧本，实际 %v / %v", script, err)
			}
		})
	}
}

func TestDeriveScriptRejectsACompactionPayloadItCannotRead(t *testing.T) {
	// 信封是好的、负载里的 llmStreamCall 却是个字符串：那道校验在推导这一步。
	line := summaryLine(t, 1, map[string]any{"llmStreamCall": "yes"})
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "compaction/summary") {
		t.Fatalf("要一句点名 compaction/summary 的 ErrMalformedFixture，实际 %v", err)
	}
}

func TestDeriveScriptRejectsAMarkedCompactionCallWithoutRawOutput(t *testing.T) {
	line := summaryLine(t, 1, map[string]any{
		"summary": []any{map[string]any{"type": "text", "text": "missing"}}, "llmStreamCall": true,
	})
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), line))
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "rawOutput") {
		t.Fatalf("要一句点名 rawOutput 的 ErrMalformedFixture，实际 %v", err)
	}
}

func TestDeriveScriptRejectsAnUnfinishedCallAtACompactionBoundary(t *testing.T) {
	lines := []string{
		chunkLine(t, 1, 1, 1, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}),
		summaryLine(t, 2, map[string]any{"summary": []any{map[string]any{"type": "text", "text": "x"}}}),
	}
	_, err := deriveFrom(t, sessionJSONL(headerLine(t, "s1", 0, 0), lines...))
	if !errors.Is(err, ErrUnrecoverableScript) || !strings.Contains(err.Error(), "1/1") {
		t.Fatalf("要一句点名 1/1 的 ErrUnrecoverableScript，实际 %v", err)
	}
}

func TestEntriesRoundTripThroughTheirSidecarShape(t *testing.T) {
	for _, entry := range []Entry{
		ChunksEntry{Chunks: textChunks()},
		ChunksEntry{},
		ThrowEntry{Chunks: []llm.StreamChunk{llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}}, Message: "401", Code: "AUTH"},
		ThrowEntry{Message: "401", Code: "AUTH"},
		HangEntry{},
		HangEntry{ReadyFile: "/tmp/ready"},
	} {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("排 %T 失败：%v", entry, err)
		}
		decoded, err := readEntry(encoded, "往返", "entry 0")
		if err != nil {
			t.Fatalf("读 %s 失败：%v", encoded, err)
		}
		if decoded.Kind() != entry.Kind() {
			t.Fatalf("往返之后 kind 变了：%s → %s", entry.Kind(), decoded.Kind())
		}
	}
}

func TestReadOverrideDocRejectsEveryMalformedShape(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"整份不是 JSON", "{oops", "不是合法 JSON"},
		{"既不是数组也不是补丁", `{"not":"array"}`, "必须是一个 Entry 数组"},
		{"patches 不是数组", `{"patches":"nope"}`, "必须是一个 Entry 数组"},
		{"补丁不是对象", `{"patches":[null]}`, "必须恰好带着 at 和 entry"},
		{"补丁少了键", `{"patches":[{"at":0}]}`, "必须恰好带着 at 和 entry"},
		{"at 是负数", `{"patches":[{"at":-1,"entry":{"kind":"hang"}}]}`, "at 必须是一个非负整数"},
		{"at 不是整数", `{"patches":[{"at":1.5,"entry":{"kind":"hang"}}]}`, "at 必须是一个非负整数"},
		{"补丁里的条目坏了", `{"patches":[{"at":0,"entry":42}]}`, "必须是一个对象"},
		{"条目不是对象", `[42]`, "必须是一个对象"},
		{"条目没有 kind", `[{"chunks":[]}]`, "kind 不认识"},
		{"kind 不认识", `[{"kind":"bogus"}]`, `kind "bogus" 不认识`},
		{"chunks 条目多了字段", `[{"kind":"chunks","chunks":[],"extra":true}]`, "chunks 条目字段不对"},
		{"chunks 不是数组", `[{"kind":"chunks","chunks":"nope"}]`, "chunks 必须是一个数组"},
		{"分块标签不认识", `[{"kind":"chunks","chunks":[{"type":"bogus"}]}]`, "chunks[0]"},
		{"throw 多了字段", `[{"kind":"throw","chunks":[],"message":"m","code":"c","extra":1}]`, "throw 条目字段不对"},
		{"throw 的 message 空", `[{"kind":"throw","chunks":[],"message":"","code":"c"}]`, "message 必须是一个非空字符串"},
		{"throw 的 code 空", `[{"kind":"throw","chunks":[],"message":"m","code":""}]`, "code 必须是一个非空字符串"},
		{"throw 的 chunks 坏了", `[{"kind":"throw","chunks":"nope","message":"m","code":"c"}]`, "chunks 必须是一个数组"},
		{"hang 多了字段", `[{"kind":"hang","extra":true}]`, "hang 条目字段不对"},
		{"hang 的 readyFile 不是串", `[{"kind":"hang","readyFile":1}]`, "readyFile 必须是一个非空字符串"},
		{"hang 带 readyFile 又多了字段", `[{"kind":"hang","readyFile":"x","extra":1}]`, "hang 条目字段不对"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := readOverrideDoc([]byte(testCase.doc), "旁挂")
			if !errors.Is(err, ErrInvalidOverride) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("要一句带 %q 的 ErrInvalidOverride，实际 %v", testCase.want, err)
			}
		})
	}
}

func TestReadOverrideDocKeepsAnEmptyReplacementDistinctFromEmptyPatches(t *testing.T) {
	replacement, err := readOverrideDoc([]byte(`[]`), "旁挂")
	if err != nil || replacement.Replacement == nil || len(replacement.Replacement) != 0 {
		t.Fatalf("`[]` 要读成一份零次调用的替换，实际 %+v / %v", replacement, err)
	}
	patches, err := readOverrideDoc([]byte(`{"patches":[]}`), "旁挂")
	if err != nil || patches.Replacement != nil {
		t.Fatalf("`{\"patches\":[]}` 要读成增补，实际 %+v / %v", patches, err)
	}
}
