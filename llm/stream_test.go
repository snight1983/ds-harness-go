// 本文件验一次调用产出的那些事实：停止原因这个联合、七种流式分块的往返、
// 「这一块算不算模型真的吐了东西」的判据，以及重放信封的深复制。
//
// 源: packages/llm/llm/src/types.ts:39-51、111-141、283-324
// 源: packages/llm/llm/src/message.ts:243-261

package llm

import (
	"encoding/json"
	"errors"
	"testing"
)

// sampleFailure 是测试里反复用的那次失败，字段填满好让往返有东西可比。
func sampleFailure() Failure {
	return Failure{
		Message:              "upstream is overloaded",
		Code:                 "overloaded",
		Status:               429,
		ProviderRetryAfterMs: 1500,
		RequestID:            "req-7",
	}
}

// stringPointer 造一个指向给定串的指针，专门用来表达 [ToolCallDeltaChunk.Name] 的「给了」。
func stringPointer(text string) *string { return &text }

// TestFinishKindIsTheReasonItself 钉住五个判别标签各归各的类型。
//
// 源: packages/llm/llm/src/types.ts:116-122
func TestFinishKindIsTheReasonItself(t *testing.T) {
	t.Parallel()

	for want, reason := range map[FinishKind]FinishReason{
		FinishStop:      StopFinish{},
		FinishToolCalls: ToolCallsFinish{},
		FinishMaxTokens: MaxTokensFinish{},
		FinishAborted:   AbortedFinish{},
		FinishError:     ErrorFinish{},
	} {
		if got := reason.FinishKind(); got != want {
			t.Errorf("%#v 的标签该是 %q，实际 %q", reason, want, got)
		}
	}
	if got := (UnknownFinish{Kind: "future"}).FinishKind(); got != "future" {
		t.Errorf("不认识的原因该自称 %q，实际 %q", "future", got)
	}
}

// TestEveryFinishReasonSurvivesTheRoundTrip 逐个钉住五种停止原因排出去再读回来还是自己。
//
// 带失败的那两种要单独摆，因为只有它们带 Failure——联合而不是「带可选字段的结构体」
// 守的就是这件事，一个 stop 写不出 failure 来。
func TestEveryFinishReasonSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]FinishReason{
		"说完了":   StopFinish{},
		"等工具":   ToolCallsFinish{},
		"撞上限":   MaxTokensFinish{},
		"被中止":   AbortedFinish{Failure: sampleFailure()},
		"中止无细节": AbortedFinish{},
		"失败":    ErrorFinish{Failure: sampleFailure()},
		"失败只有码": ErrorFinish{Failure: Failure{Message: "boom", Code: "internal"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			back, err := UnmarshalFinishReason(data)
			if err != nil {
				t.Fatalf("读回来不该失败：%v", err)
			}
			if back != original {
				t.Errorf("读回来不是自己：想要 %#v，实际 %#v", original, back)
			}
			again, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("再排一次不该失败：%v", err)
			}
			if string(again) != string(data) {
				t.Errorf("往返不闭合：\n第一次 %s\n第二次 %s", data, again)
			}
		})
	}
}

// TestFinishWireNamesFollowDSH 钉住停止原因在介质上的字段名，判别键是 kind 不是 type。
//
// 停止原因和内容块用的是**两个不同的判别键**（kind / type），这是 DSH 定下的，
// 不是笔误。写反了会让两侧的会话日志读不通，所以要有一条会失败的断言按住。
func TestFinishWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		reason FinishReason
		wire   string
	}{
		"说完了": {StopFinish{}, `{"kind":"stop"}`},
		"等工具": {ToolCallsFinish{}, `{"kind":"tool-calls"}`},
		"撞上限": {MaxTokensFinish{}, `{"kind":"max-tokens"}`},
		"被中止": {
			AbortedFinish{Failure: Failure{Message: "m", Code: "c"}},
			`{"kind":"aborted","failure":{"message":"m","code":"c"}}`,
		},
		"失败带全部细节": {
			ErrorFinish{Failure: sampleFailure()},
			`{"kind":"error","failure":{"message":"upstream is overloaded","code":"overloaded",` +
				`"status":429,"providerRetryAfterMs":1500,"requestId":"req-7"}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.reason)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestAnUnknownFinishReasonIsKeptVerbatim 钉住不认识的停止原因读得回来、排得出去，逐字节不变。
//
// 理由和内容块那条逐字相同：一个旧构建读一份新构建写的会话日志、再写回去，
// 不许把读不懂的东西抹掉。
func TestAnUnknownFinishReasonIsKeptVerbatim(t *testing.T) {
	t.Parallel()

	raw := `{"kind":"content-filter","policy":"safety","segments":[1,2]}`
	reason, err := UnmarshalFinishReason([]byte(raw))
	if err != nil {
		t.Fatalf("不认识的原因不该报错：%v", err)
	}
	unknown, ok := reason.(UnknownFinish)
	if !ok {
		t.Fatalf("该收进 UnknownFinish，实际 %#v", reason)
	}
	if unknown.Kind != "content-filter" {
		t.Errorf("该自称 content-filter，实际 %q", unknown.Kind)
	}
	data, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != raw {
		t.Errorf("不是逐字节吐回：\n想要 %s\n实际 %s", raw, data)
	}
}

// TestAnUnknownFinishWithoutRawBytesRefusesToMarshal 钉住自己造出来的空 [UnknownFinish] 当场报错。
func TestAnUnknownFinishWithoutRawBytesRefusesToMarshal(t *testing.T) {
	t.Parallel()

	if _, err := json.Marshal(UnknownFinish{Kind: "x"}); !errorIsMalformed(err) {
		t.Errorf("该报 ErrMalformedValue，实际 %v", err)
	}
	if _, err := json.Marshal(UnknownFinish{Kind: "x", Raw: []byte("{oops")}); !errorIsMalformed(err) {
		t.Errorf("原始字节不是合法 JSON 时该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestMalformedFinishBytesAreRefused 钉住排不成形状的字节报 [ErrMalformedValue]。
func TestMalformedFinishBytesAreRefused(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"根本不是 JSON": `{`,
		"标签不是字符串":   `{"kind":7}`,
		"中止的失败不是对象": `{"kind":"aborted","failure":7}`,
		"失败的失败不是对象": `{"kind":"error","failure":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalFinishReason([]byte(data)); !errors.Is(err, ErrMalformedValue) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestChunkTypeIsTheChunkItself 钉住七个判别标签各归各的类型。
//
// 源: packages/llm/llm/src/types.ts:312-324
func TestChunkTypeIsTheChunkItself(t *testing.T) {
	t.Parallel()

	for want, chunk := range map[ChunkType]StreamChunk{
		ChunkBlockStart:     BlockStartChunk{},
		ChunkTextDelta:      TextDeltaChunk{},
		ChunkReasoningDelta: ReasoningDeltaChunk{},
		ChunkToolCallDelta:  ToolCallDeltaChunk{},
		ChunkBlockEnd:       BlockEndChunk{},
		ChunkUsage:          UsageChunk{},
		ChunkFinish:         FinishChunk{},
	} {
		if got := chunk.ChunkType(); got != want {
			t.Errorf("%#v 的标签该是 %q，实际 %q", chunk, want, got)
		}
	}
}

// TestEveryChunkSurvivesTheRoundTrip 逐个钉住七种分块排出去再读回来还是自己。
//
// 工具调用增量摆了三条（没带名字／带空名字／带名字），因为那个字段是本包里唯一
// 一个用指针表达可选的字段，空串和缺失在它身上是两件事——往返必须把这个差别带过去。
func TestEveryChunkSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]StreamChunk{
		"块开始":      BlockStartChunk{Index: 0, BlockType: BlockText},
		"块开始是图":    BlockStartChunk{Index: 3, BlockType: BlockImage},
		"文本增量":     TextDeltaChunk{Index: 1, Text: "hello"},
		"文本增量是空的":  TextDeltaChunk{Index: 1},
		"推理增量":     ReasoningDeltaChunk{Index: 2, Text: "thinking"},
		"工具增量没带名字": ToolCallDeltaChunk{Index: 4, ID: "call-1", ArgumentsDelta: `{"a"`},
		"工具增量带空名字": ToolCallDeltaChunk{Index: 4, ID: "call-1", Name: stringPointer("")},
		"工具增量带名字":  ToolCallDeltaChunk{Index: 4, ID: "call-1", Name: stringPointer("read")},
		"块结束":      BlockEndChunk{Index: 0, Block: TextBlock{Text: "done"}},
		"块结束是工具调用": BlockEndChunk{
			Index: 1,
			Block: ToolCallBlock{ID: "call-1", Name: "read", Arguments: `{"path":"a"}`},
		},
		"用量": UsageChunk{Usage: TokenUsage{InputTokens: 10, OutputTokens: 20}},
		"用量带缓存": UsageChunk{Usage: TokenUsage{
			InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 5,
		}},
		"终止":    FinishChunk{Reason: StopFinish{}},
		"终止带失败": FinishChunk{Reason: ErrorFinish{Failure: sampleFailure()}},
		"终止带重放状态": FinishChunk{
			Reason: StopFinish{},
			ReplayState: &ReplayEnvelope{
				Response: json.RawMessage(`{"id":"r-1"}`),
				Blocks:   []json.RawMessage{json.RawMessage(`{"i":0}`)},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			back, err := UnmarshalStreamChunk(data)
			if err != nil {
				t.Fatalf("读回来不该失败：%v", err)
			}
			again, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("再排一次不该失败：%v", err)
			}
			if string(again) != string(data) {
				t.Errorf("往返不闭合：\n第一次 %s\n第二次 %s", data, again)
			}
		})
	}
}

// TestChunkWireNamesFollowDSH 钉住七种分块在介质上的字段名。
//
// 和内容块那条一样：这些名字是两侧共用的，改一个字都会让适配器和运行时对不上。
func TestChunkWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		chunk StreamChunk
		wire  string
	}{
		"块开始": {
			BlockStartChunk{Index: 0, BlockType: BlockText},
			`{"type":"block-start","index":0,"blockType":"text"}`,
		},
		"文本增量": {
			TextDeltaChunk{Index: 1, Text: "hi"},
			`{"type":"text-delta","index":1,"text":"hi"}`,
		},
		"推理增量": {
			ReasoningDeltaChunk{Index: 1, Text: "hi"},
			`{"type":"reasoning-delta","index":1,"text":"hi"}`,
		},
		"工具增量没带名字": {
			ToolCallDeltaChunk{Index: 2, ID: "c", ArgumentsDelta: "{}"},
			`{"type":"tool-call-delta","index":2,"id":"c","argumentsDelta":"{}"}`,
		},
		"工具增量带空名字": {
			ToolCallDeltaChunk{Index: 2, ID: "c", Name: stringPointer(""), ArgumentsDelta: "{}"},
			`{"type":"tool-call-delta","index":2,"id":"c","name":"","argumentsDelta":"{}"}`,
		},
		"块结束": {
			BlockEndChunk{Index: 0, Block: TextBlock{Text: "a"}},
			`{"type":"block-end","index":0,"block":{"type":"text","text":"a"}}`,
		},
		"用量": {
			UsageChunk{Usage: TokenUsage{InputTokens: 1, OutputTokens: 2}},
			`{"type":"usage","usage":{"inputTokens":1,"outputTokens":2}}`,
		},
		"终止": {
			FinishChunk{Reason: StopFinish{}},
			`{"type":"finish","reason":{"kind":"stop"}}`,
		},
		"终止带重放状态": {
			FinishChunk{Reason: StopFinish{}, ReplayState: &ReplayEnvelope{Response: json.RawMessage(`{"id":1}`)}},
			`{"type":"finish","reason":{"kind":"stop"},"replayState":{"response":{"id":1}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.chunk)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestAnUnknownChunkTypeIsRefused 钉住这个联合是**封闭**的：不认识的标签报错，不收进 Unknown 变体。
//
// 这条和另外三个联合正好相反，是有意的。流式分块不是持久化格式，是适配器和运行时
// 之间一条进程内的协议：两端在同一次构建里，一个读不懂的标签只可能是编程错误，
// 而不是「一份更新版本写下的数据」。所以这里必须炸，不能默默收着。
func TestAnUnknownChunkTypeIsRefused(t *testing.T) {
	t.Parallel()

	_, err := UnmarshalStreamChunk([]byte(`{"type":"audio-delta","index":0}`))
	if !errors.Is(err, ErrUnknownChunkType) {
		t.Fatalf("该报 ErrUnknownChunkType，实际 %v", err)
	}
	if errors.Is(err, ErrMalformedValue) {
		t.Error("不该同时算格式错误：字节是好的，只是类型不认识，两者要采取的行动不一样")
	}
}

// TestMalformedChunkBytesAreRefused 钉住排不成形状的字节报 [ErrMalformedValue]，逐种分块各一条。
func TestMalformedChunkBytesAreRefused(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"根本不是 JSON":    `{`,
		"标签不是字符串":      `{"type":7}`,
		"块开始的下标不是数字":   `{"type":"block-start","index":"x"}`,
		"文本增量的文本不是字符串": `{"type":"text-delta","text":7}`,
		"推理增量的文本不是字符串": `{"type":"reasoning-delta","text":7}`,
		"工具增量的标识不是字符串": `{"type":"tool-call-delta","id":7}`,
		"块结束的下标不是数字":   `{"type":"block-end","index":"x"}`,
		"块结束的块读不回来":    `{"type":"block-end","block":7}`,
		"块结束里的块坏了":     `{"type":"block-end","block":{"type":"text","text":7}}`,
		"用量不是对象":       `{"type":"usage","usage":7}`,
		"终止的下标不是对象":    `{"type":"finish","replayState":7}`,
		"终止的原因读不回来":    `{"type":"finish","reason":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalStreamChunk([]byte(data)); !errors.Is(err, ErrMalformedValue) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestChunksThatCannotBeMarshalledFail 钉住两个只可能由代码造出来的空壳当场报错。
//
// 一个没有内容块的 block-end、一个没有停止原因的 finish，都不可能是读出来的。
// 悄悄排成 `null` 会在日志里留下一个下次读回来是空的分块，那是一次静默的丢失。
func TestChunksThatCannotBeMarshalledFail(t *testing.T) {
	t.Parallel()

	for name, chunk := range map[string]StreamChunk{
		"块结束没有块":    BlockEndChunk{Index: 0},
		"块结束的块排不出去": BlockEndChunk{Index: 0, Block: UnknownBlock{Kind: "x"}},
		"终止没有原因":    FinishChunk{},
		"终止的原因排不出去": FinishChunk{Reason: UnknownFinish{Kind: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := json.Marshal(chunk); !errorIsMalformed(err) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestIsTokenDeltaOnlyCountsRealContent 钉住「首个 token」那类计时的判据。
//
// 源: packages/llm/llm/src/message.ts:243-261
//
// 空名字那一条是这里最要紧的：DSH 判的是 `name !== undefined`，所以一个**空串的
// 工具名**照样算一次增量，而「没带名字」不算。本包用指针表达这个差别，
// 这条用例就是那个指针存在的全部理由。
func TestIsTokenDeltaOnlyCountsRealContent(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		chunk StreamChunk
		want  bool
	}{
		"有内容的文本增量":   {TextDeltaChunk{Text: "a"}, true},
		"空的文本增量":     {TextDeltaChunk{}, false},
		"有内容的推理增量":   {ReasoningDeltaChunk{Text: "a"}, true},
		"空的推理增量":     {ReasoningDeltaChunk{}, false},
		"带参数的工具增量":   {ToolCallDeltaChunk{ArgumentsDelta: "{"}, true},
		"带空名字的工具增量":  {ToolCallDeltaChunk{Name: stringPointer("")}, true},
		"什么都没带的工具增量": {ToolCallDeltaChunk{ID: "c"}, false},
		"块开始":        {BlockStartChunk{}, false},
		"块结束":        {BlockEndChunk{Block: TextBlock{Text: "a"}}, false},
		"用量":         {UsageChunk{Usage: TokenUsage{OutputTokens: 9}}, false},
		"终止":         {FinishChunk{Reason: StopFinish{}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := IsTokenDelta(expectation.chunk); got != expectation.want {
				t.Errorf("该是 %v，实际 %v", expectation.want, got)
			}
		})
	}
}

// TestReplayEnvelopeCloneCopiesTheSlicesToo 钉住重放信封的深复制真的深。
//
// 这个信封是别人用来**重放一次响应**的状态。它被人从背后改掉的话，
// 重放出来的就不再是那次响应了，而且改动发生在离现场很远的地方。
func TestReplayEnvelopeCloneCopiesTheSlicesToo(t *testing.T) {
	t.Parallel()

	t.Run("响应元数据是复制的", func(t *testing.T) {
		t.Parallel()

		original := ReplayEnvelope{Response: json.RawMessage(`{"id":1}`)}
		cloned := original.Clone()
		cloned.Response[0] = 'X'

		if original.Response[0] != '{' {
			t.Errorf("原件被改动了：%s", original.Response)
		}
	})

	t.Run("块级元数据是复制的", func(t *testing.T) {
		t.Parallel()

		original := ReplayEnvelope{Blocks: []json.RawMessage{json.RawMessage(`{"i":0}`)}}
		cloned := original.Clone()
		cloned.Blocks[0][0] = 'X'

		if original.Blocks[0][0] != '{' {
			t.Errorf("原件被改动了：%s", original.Blocks[0])
		}
	})

	t.Run("没有块级元数据时不凭空造一个空清单", func(t *testing.T) {
		t.Parallel()

		// 「适配器的元数据与块结构无关」和「适配器给了零条块级元数据」不是一回事：
		// 后者会让装配以为有零条要对齐。复制不许把前者变成后者。
		if got := (ReplayEnvelope{}).Clone(); got.Blocks != nil {
			t.Errorf("该还是 nil，实际 %#v", got.Blocks)
		}
	})
}
