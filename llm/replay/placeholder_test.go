// 本文件的作用：`{{fromRequest:<正则>}}` 那一套——语料怎么攒、哪一次匹配赢、
// 花括号量词怎么和结束符共存，以及三种当场失败。

package replay

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/llm"
)

// requestMessages 是那份埋了两个 goal id 的请求，后一个才是活的那个。
func requestMessages() []llm.Message {
	return []llm.Message{llm.NewUserMessage(llm.Content{
		llm.TextBlock{Text: `stale {"goal":{"id":"goal-old"}} then {"goal":{"id":"goal-42ab"}}`},
	}, llm.UserSource{})}
}

// scriptedCall 是一次把参数写死在剧本里的工具调用，参数串里可以埋占位符。
func scriptedCall(argumentsDelta string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockToolCall},
		llm.ToolCallDeltaChunk{Index: 0, ID: "c1", ArgumentsDelta: argumentsDelta},
		llm.BlockEndChunk{Index: 0, Block: llm.ToolCallBlock{ID: "c1", Name: "update_goal", Arguments: argumentsDelta}},
		llm.FinishChunk{Reason: llm.ToolCallsFinish{}},
	}
}

// resolvedArguments 解算一条剧本条目，交回那块 tool-call-delta 上的参数串。
func resolvedArguments(t *testing.T, argumentsDelta string) string {
	t.Helper()
	resolved, err := ResolveScriptedEntry(ChunksEntry{Chunks: scriptedCall(argumentsDelta)}, requestMessages())
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	entry, ok := resolved.(ChunksEntry)
	if !ok {
		t.Fatalf("解算完还该是 chunks 条目，实际 %T", resolved)
	}
	delta, ok := entry.Chunks[1].(llm.ToolCallDeltaChunk)
	if !ok {
		t.Fatalf("第二块该是 tool-call-delta，实际 %T", entry.Chunks[1])
	}
	return delta.ArgumentsDelta
}

func TestResolveScriptedEntryTakesTheLastMatchesCaptureGroup(t *testing.T) {
	got := resolvedArguments(t, `{"goal_id":"{{fromRequest:"id":"(goal-[^"]+)"}}","revision":1}`)
	if got != `{"goal_id":"goal-42ab","revision":1}` {
		t.Fatalf("解算结果不对：%s", got)
	}
}

func TestResolveScriptedEntryFallsBackToTheWholeMatch(t *testing.T) {
	got := resolvedArguments(t, `{"goal_id":"{{fromRequest:goal-[0-9a-z]+}}"}`)
	if got != `{"goal_id":"goal-42ab"}` {
		t.Fatalf("解算结果不对：%s", got)
	}
}

func TestResolveScriptedEntryKeepsATrailingBraceQuantifierInThePattern(t *testing.T) {
	// 一串连续 `}` 的最后两个才是结束符，于是模式可以拿 `{4}` 这样的量词收尾。
	got := resolvedArguments(t, `{"goal_id":"{{fromRequest:goal-[0-9a-z]{4}}}"}`)
	if got != `{"goal_id":"goal-42ab"}` {
		t.Fatalf("解算结果不对：%s", got)
	}
}

func TestResolveScriptedEntryKeepsAGroupThatMatchedTheEmptyString(t *testing.T) {
	// 一个匹配到空串的捕获组和一个根本没参与的捕获组必须分得开：前者交空串。
	got := resolvedArguments(t, `{"goal_id":"x{{fromRequest:goal-42ab(z*)}}"}`)
	if got != `{"goal_id":"x"}` {
		t.Fatalf("匹配到空串的捕获组该交空串，实际 %s", got)
	}
}

func TestResolveScriptedEntryResolvesEveryScriptedStringField(t *testing.T) {
	// 占位符埋在 tool-call-delta 和 block-end 两处，两处都要被换掉。
	resolved, err := ResolveScriptedEntry(
		ChunksEntry{Chunks: scriptedCall(`{"goal_id":"{{fromRequest:goal-[0-9a-z]+}}"}`)}, requestMessages())
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("排结果失败：%v", err)
	}
	if strings.Count(string(encoded), "goal-42ab") != 2 {
		t.Fatalf("两处都该换掉，实际 %s", encoded)
	}
}

func TestResolveScriptedEntryReturnsTheSameEntryWhenNoPlaceholderAppears(t *testing.T) {
	entry := ChunksEntry{Chunks: textChunks()}
	resolved, err := ResolveScriptedEntry(entry, requestMessages())
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if _, same := resolved.(ChunksEntry); !same {
		t.Fatalf("不带占位符的条目该原样交回，实际 %T", resolved)
	}
}

func TestResolveScriptedEntryReachesEveryValueShapeInTheScript(t *testing.T) {
	// 数组、对象、非字符串叶子这三支都得走到：hang 条目的 readyFile 埋一个占位符，
	// 而它旁边的 kind 是普通字符串、chunks 里的下标是数字。
	entry := HangEntry{ReadyFile: `ready-{{fromRequest:goal-[0-9a-z]+}}`}
	resolved, err := ResolveScriptedEntry(entry, requestMessages())
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	hang, ok := resolved.(HangEntry)
	if !ok || hang.ReadyFile != "ready-goal-42ab" {
		t.Fatalf("readyFile 没换对：%+v", resolved)
	}
}

func TestResolveScriptedEntryFailsLoudOnEveryUnresolvablePlaceholder(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		want      string
	}{
		{"一个都没匹配上", `{"id":"{{fromRequest:task-[0-9]+}}"}`, "一个都没匹配上"},
		{"模式编译不了", `{"id":"{{fromRequest:(goal-}}"}`, "编译不了"},
		{"占位符没闭合", `{"id":"{{fromRequest:goal-1"}`, "没闭合"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveScriptedEntry(ChunksEntry{Chunks: scriptedCall(testCase.arguments)}, requestMessages())
			if !errors.Is(err, ErrScriptedPlaceholder) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("要一句带 %q 的 ErrScriptedPlaceholder，实际 %v", testCase.want, err)
			}
		})
	}
}

func TestResolveScriptedEntryRevalidatesTheResolvedEntry(t *testing.T) {
	// 一次替换可以把一个字段换成解释不了的东西——这里那个捕获组匹配到空串，于是
	// throw 条目的 message 变成空串，而空 message 是读条目那道校验挡着的。
	entry := ThrowEntry{Message: `{{fromRequest:goal-42ab(z*)}}`, Code: "AUTH"}
	_, err := ResolveScriptedEntry(entry, requestMessages())
	if !errors.Is(err, ErrInvalidOverride) {
		t.Fatalf("解算完那道校验该把它挡住，实际 %v", err)
	}
}

func TestCollectStringsSkipsObjectKeysAndKeepsWritingOrder(t *testing.T) {
	// 键不算叶子；顺序就是这份 JSON 的书写顺序，而「最后一次匹配赢」正靠这个顺序。
	leaves, err := collectStrings([]byte(`{"b":"first","a":["second",3,null,true],"c":{"d":"third"}}`), nil)
	if err != nil {
		t.Fatalf("攒语料失败：%v", err)
	}
	want := []string{"first", "second", "third"}
	if len(leaves) != len(want) {
		t.Fatalf("语料不对：%v", leaves)
	}
	for index, value := range want {
		if leaves[index] != value {
			t.Fatalf("第 %d 个叶子要 %q，实际 %q", index, value, leaves[index])
		}
	}
}

func TestCollectStringsRejectsACorpusItCannotRead(t *testing.T) {
	if _, err := collectStrings([]byte(`{"a":@}`), nil); !errors.Is(err, ErrScriptedPlaceholder) {
		t.Fatalf("要 ErrScriptedPlaceholder，实际 %v", err)
	}
}

func TestSubstituteValueRejectsEveryShapeItCannotRead(t *testing.T) {
	// 三支分别按首字符分流，坏在里头的那一份各自报各自的话。
	for name, raw := range map[string]string{
		"字符串": `"\q"`,
		"数组":  `[oops]`,
		"对象":  `{oops}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := substituteValue(json.RawMessage(raw), "语料"); !errors.Is(err, ErrScriptedPlaceholder) {
				t.Fatalf("要 ErrScriptedPlaceholder，实际 %v", err)
			}
		})
	}
}

func TestSubstituteValueLeavesAnEmptyOrScalarValueAlone(t *testing.T) {
	for _, raw := range []string{"", "  ", "42", "null", "true"} {
		got, err := substituteValue(json.RawMessage(raw), "语料")
		if err != nil || string(got) != raw {
			t.Fatalf("%q 该原样交回，实际 %q / %v", raw, got, err)
		}
	}
}
