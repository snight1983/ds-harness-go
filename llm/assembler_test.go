package llm

import (
	"encoding/json"
	"errors"
	"testing"
)

// pushAll 把一串分块按顺序喂进去，省掉每条测试里那个循环。
func pushAll(assembler *BlockAssembler, chunks ...StreamChunk) {
	for _, chunk := range chunks {
		assembler.Push(chunk)
	}
}

// TestAssemblerAssemblesInStreamOrder 走一遍最普通的那条流：两块内容按各自下标
// 第一次出现的次序排，文本按增量到达的次序拼。
func TestAssemblerAssemblesInStreamOrder(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		BlockStartChunk{Index: 0, BlockType: BlockText},
		TextDeltaChunk{Index: 0, Text: "你"},
		BlockStartChunk{Index: 1, BlockType: BlockReasoning},
		ReasoningDeltaChunk{Index: 1, Text: "想了想"},
		TextDeltaChunk{Index: 0, Text: "好"},
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "你好"}},
		BlockEndChunk{Index: 1, Block: ReasoningBlock{Text: "想了想"}},
		FinishChunk{Reason: StopFinish{}},
	)

	blocks, err := assembler.Blocks()
	if err != nil {
		t.Fatalf("装配不该出错：%v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("该装出两块，得到 %d 块", len(blocks))
	}
	if text := blocks[0].(TextBlock); text.Text != "你好" {
		t.Fatalf("第一块该是拼起来的文本，得到 %q", text.Text)
	}
	if reasoning := blocks[1].(ReasoningBlock); reasoning.Text != "想了想" {
		t.Fatalf("第二块该是推理文本，得到 %q", reasoning.Text)
	}
}

// TestAssemblerAcceptsDeltaOnlyProtocol 钉住那句「容得下只发增量的协议」：
// 一条一个 block-start 都不发的流照样装得出块，块的类型由第一条增量定。
func TestAssemblerAcceptsDeltaOnlyProtocol(t *testing.T) {
	assembler := NewBlockAssembler()
	name := "read"
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "光有增量"},
		ToolCallDeltaChunk{Index: 1, ID: "c-1", Name: &name, ArgumentsDelta: `{"a":`},
		ToolCallDeltaChunk{Index: 1, ID: "c-1", ArgumentsDelta: `1}`},
	)

	blocks, err := assembler.Blocks()
	if err != nil {
		t.Fatalf("装配不该出错：%v", err)
	}
	if text := blocks[0].(TextBlock); text.Text != "光有增量" {
		t.Fatalf("文本块该由增量定型，得到 %q", text.Text)
	}
	call := blocks[1].(ToolCallBlock)
	if call.ID != "c-1" || call.Name != "read" || call.Arguments != `{"a":1}` {
		t.Fatalf("工具调用块拼错了：%+v", call)
	}
}

// TestAssemblerKeepsFirstToolCallName 钉住名字只认第一份非空的那一次。
// 提供方常在第一条增量里给名字、后面几条只带参数片段（Name 为 nil），
// 照抄进去会把名字擦成空串。
func TestAssemblerKeepsFirstToolCallName(t *testing.T) {
	assembler := NewBlockAssembler()
	name := "read"
	blank := ""
	pushAll(assembler,
		ToolCallDeltaChunk{Index: 0, ID: "c-1", Name: &name},
		ToolCallDeltaChunk{Index: 0, ID: "c-1", Name: nil, ArgumentsDelta: "{}"},
		ToolCallDeltaChunk{Index: 0, ID: "c-1", Name: &blank},
	)

	blocks, _ := assembler.Blocks()
	if call := blocks[0].(ToolCallBlock); call.Name != "read" {
		t.Fatalf("名字该留住第一份非空的，得到 %q", call.Name)
	}
}

// TestAssemblerSynthesizesToolCallID 钉住一个适配器一个 id 都没给过时那条兜底：
// 没有 id 的工具调用是一次派发不出去的调用，工具结果对不回来。
func TestAssemblerSynthesizesToolCallID(t *testing.T) {
	assembler := NewBlockAssembler()
	assembler.Push(ToolCallDeltaChunk{Index: 3, ArgumentsDelta: "{}"})

	blocks, _ := assembler.Blocks()
	if call := blocks[0].(ToolCallBlock); call.ID != "call-3" {
		t.Fatalf("该按下标兜一个 id，得到 %q", call.ID)
	}
}

// TestAssemblerBlockEndIsAuthoritative 钉住 block-end 带来那一块是权威的：
// 它之后迟到的增量一律丢掉，重复的 block-end 也不顶掉第一次那份。这条守的是
// 「流式吐出去的和最后装配出来的说的是同一件事」。
func TestAssemblerBlockEndIsAuthoritative(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		BlockStartChunk{Index: 0, BlockType: BlockText},
		TextDeltaChunk{Index: 0, Text: "定稿"},
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "定稿"}},
		TextDeltaChunk{Index: 0, Text: "迟到的"},
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "第二次关"}},
	)

	blocks, _ := assembler.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("重复的 block-end 不该多开一块，得到 %d 块", len(blocks))
	}
	if text := blocks[0].(TextBlock); text.Text != "定稿" {
		t.Fatalf("该留住第一次关掉时那一块，得到 %q", text.Text)
	}
}

// TestAssemblerIgnoresLateDeltasOnEveryClosedType 把「迟到的增量丢掉」这条在
// 三种攒得出来的类型上各走一遍——三个 case 里的那句提前 return 各有各的一行。
func TestAssemblerIgnoresLateDeltasOnEveryClosedType(t *testing.T) {
	assembler := NewBlockAssembler()
	name := "read"
	pushAll(assembler,
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "文本"}},
		TextDeltaChunk{Index: 0, Text: "迟到"},
		BlockEndChunk{Index: 1, Block: ReasoningBlock{Text: "推理"}},
		ReasoningDeltaChunk{Index: 1, Text: "迟到"},
		BlockEndChunk{Index: 2, Block: ToolCallBlock{ID: "c-1", Name: "read", Arguments: "{}"}},
		ToolCallDeltaChunk{Index: 2, ID: "改过的", Name: &name, ArgumentsDelta: "迟到"},
	)

	blocks, _ := assembler.Blocks()
	if text := blocks[0].(TextBlock); text.Text != "文本" {
		t.Fatalf("文本块被迟到的增量改了，得到 %q", text.Text)
	}
	if reasoning := blocks[1].(ReasoningBlock); reasoning.Text != "推理" {
		t.Fatalf("推理块被迟到的增量改了，得到 %q", reasoning.Text)
	}
	if call := blocks[2].(ToolCallBlock); call.ID != "c-1" || call.Arguments != "{}" {
		t.Fatalf("工具调用块被迟到的增量改了：%+v", call)
	}
}

// TestAssemblerDropsToolCallsOnMaxTokens 钉住撞上输出上限时丢掉工具调用。
// 一个参数被截断的调用是执行不了的，留着它就是让循环拿一份半截 JSON 去派发。
func TestAssemblerDropsToolCallsOnMaxTokens(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "说了一半"},
		ToolCallDeltaChunk{Index: 1, ID: "c-1", ArgumentsDelta: `{"path":"/a`},
		FinishChunk{Reason: MaxTokensFinish{}},
	)

	blocks, err := assembler.Blocks()
	if err != nil {
		t.Fatalf("装配不该出错：%v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("被截断的工具调用该丢掉，得到 %d 块", len(blocks))
	}
	if _, isText := blocks[0].(TextBlock); !isText {
		t.Fatalf("留下的该是那块文本，得到 %T", blocks[0])
	}
}

// TestAssemblerRejectsUnassemblableOpenBlock 钉住一块「既没被 block-end 关掉、
// 类型又不是那三种攒得出来的」会让整趟装配报错，而不是悄悄少一块。
func TestAssemblerRejectsUnassemblableOpenBlock(t *testing.T) {
	assembler := NewBlockAssembler()
	assembler.Push(BlockStartChunk{Index: 0, BlockType: BlockImage})

	if _, err := assembler.Blocks(); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("该报 ErrMalformedValue，得到 %v", err)
	}
	if _, err := assembler.Message(UserSource{}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("Message 该把那条错误带出来，得到 %v", err)
	}
	if _, _, err := assembler.ReplayState(); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("ReplayState 该把那条错误带出来，得到 %v", err)
	}
}

// TestAssemblerPanicsOnUnknownChunk 钉住那句 panic：[StreamChunk] 是封了口的接口，
// 一个不认识的具体类型只可能来自本包自己，那是 bug 不是坏数据。
func TestAssemblerPanicsOnUnknownChunk(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("喂一个本包不认识的分块该 panic")
		}
	}()
	NewBlockAssembler().Push(unknownChunk{})
}

// unknownChunk 是一个只在测试里存在的、本包 Push 认不出来的分块。它实现得了
// [StreamChunk] 是因为这是包内测试——从包外没人造得出来，那正是封口要的效果。
type unknownChunk struct{}

func (unknownChunk) ChunkType() ChunkType { return "从没见过的" }

func (unknownChunk) sealedStreamChunk() {}

// TestAssemblerInterruptedBlocksKeepsOnlyProse 钉住打断之后能安全定稿的那个前缀：
// 只要 text/reasoning，全空白的不要，工具调用一律不要（打断发生在派发之前，
// 留一个下来就得替它编一份结果），认不出类型的开着的块也不要。
func TestAssemblerInterruptedBlocksKeepsOnlyProse(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "关掉的文本"}},
		ReasoningDeltaChunk{Index: 1, Text: "还开着的推理"},
		TextDeltaChunk{Index: 2, Text: "   \n\t "},
		ToolCallDeltaChunk{Index: 3, ID: "c-1", ArgumentsDelta: "{}"},
		BlockStartChunk{Index: 4, BlockType: BlockImage},
	)

	blocks := assembler.InterruptedBlocks()
	if len(blocks) != 2 {
		t.Fatalf("该只留两块，得到 %d 块：%+v", len(blocks), blocks)
	}
	if text := blocks[0].(TextBlock); text.Text != "关掉的文本" {
		t.Fatalf("第一块不对，得到 %q", text.Text)
	}
	if reasoning := blocks[1].(ReasoningBlock); reasoning.Text != "还开着的推理" {
		t.Fatalf("第二块不对，得到 %q", reasoning.Text)
	}
	if NewBlockAssembler().InterruptedBlocks() != nil {
		t.Fatal("什么都没喂过时该是 nil")
	}
}

// TestAssemblerUsageAndFinish 钉住这两个读取面的「还没来过」表示法：用量靠第二个
// 返回值，结束原因靠 [StopFinish] 兜底。
func TestAssemblerUsageAndFinish(t *testing.T) {
	assembler := NewBlockAssembler()

	if _, present := assembler.Usage(); present {
		t.Fatal("没喂过 usage 时第二个返回值该是 false")
	}
	if assembler.Finish().FinishKind() != FinishStop {
		t.Fatalf("没喂过 finish 时该兜底成 stop，得到 %q", assembler.Finish().FinishKind())
	}

	assembler.Push(UsageChunk{Usage: TokenUsage{InputTokens: 7, OutputTokens: 11}})
	assembler.Push(FinishChunk{Reason: ToolCallsFinish{}})

	usage, present := assembler.Usage()
	if !present || usage.InputTokens != 7 || usage.OutputTokens != 11 {
		t.Fatalf("用量不对：%+v present=%v", usage, present)
	}
	if assembler.Finish().FinishKind() != FinishToolCalls {
		t.Fatalf("结束原因不对，得到 %q", assembler.Finish().FinishKind())
	}
}

// TestAssemblerReplayStateAbsent 钉住没有信封时第二个返回值是 false，而不是
// 交出一份空信封——重放方要分得清「没有重放状态」和「重放状态是空的」。
func TestAssemblerReplayStateAbsent(t *testing.T) {
	assembler := NewBlockAssembler()
	assembler.Push(FinishChunk{Reason: StopFinish{}})

	if _, present, err := assembler.ReplayState(); present || err != nil {
		t.Fatalf("没有信封时该是 present=false, err=nil，得到 %v %v", present, err)
	}
}

// TestAssemblerReplayStateWithoutPerBlockEntries 钉住一份只有整体响应、没有每块条目
// 的信封原样交出——那种信封不需要跟着裁剪。
func TestAssemblerReplayStateWithoutPerBlockEntries(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "文本"},
		FinishChunk{
			Reason:      StopFinish{},
			ReplayState: &ReplayEnvelope{Response: json.RawMessage(`{"raw":1}`)},
		},
	)

	envelope, present, err := assembler.ReplayState()
	if err != nil || !present {
		t.Fatalf("该交出那份信封，得到 present=%v err=%v", present, err)
	}
	if string(envelope.Response) != `{"raw":1}` {
		t.Fatalf("整体响应被改动了，得到 %s", envelope.Response)
	}
}

// TestAssemblerReplayStatePrunedWithBlocks 钉住每块一条的重放条目跟着丢掉的工具调用
// 一起裁剪：不裁的话，剩下那块文本会认领本属于工具调用的那份私有状态。
func TestAssemblerReplayStatePrunedWithBlocks(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "说了一半"},
		ToolCallDeltaChunk{Index: 1, ID: "c-1", ArgumentsDelta: `{"path":"/a`},
		FinishChunk{
			Reason: MaxTokensFinish{},
			ReplayState: &ReplayEnvelope{
				Response: json.RawMessage(`{"raw":1}`),
				Blocks:   []json.RawMessage{json.RawMessage(`"文本那份"`), json.RawMessage(`"工具那份"`)},
			},
		},
	)

	envelope, present, err := assembler.ReplayState()
	if err != nil || !present {
		t.Fatalf("该交出那份信封，得到 present=%v err=%v", present, err)
	}
	if len(envelope.Blocks) != 1 || string(envelope.Blocks[0]) != `"文本那份"` {
		t.Fatalf("每块条目该跟着裁剪，得到 %v", envelope.Blocks)
	}
}

// TestAssemblerReplayStateKeptWholeWhenNothingWasDropped 钉住一块都没丢时那份对齐的
// 信封**原样**交出去，条目一条不少、次序一条不动。
//
// 裁剪只在撞上输出上限、真的丢掉了工具调用时才发生。少了这条用例，一次「顺手
// 统一走裁剪那条路」的改动不会被任何东西挡住——而它会在最常见的那条正常收尾上
// 白重排一遍每块条目。
func TestAssemblerReplayStateKeptWholeWhenNothingWasDropped(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "第一块"},
		ReasoningDeltaChunk{Index: 1, Text: "第二块"},
		FinishChunk{
			Reason: StopFinish{},
			ReplayState: &ReplayEnvelope{
				Response: json.RawMessage(`{"raw":1}`),
				Blocks:   []json.RawMessage{json.RawMessage(`"一"`), json.RawMessage(`"二"`)},
			},
		},
	)

	envelope, present, err := assembler.ReplayState()
	if err != nil || !present {
		t.Fatalf("该交出那份信封，得到 present=%v err=%v", present, err)
	}
	if len(envelope.Blocks) != 2 ||
		string(envelope.Blocks[0]) != `"一"` || string(envelope.Blocks[1]) != `"二"` {
		t.Fatalf("一块都没丢时每块条目该原样留着，得到 %v", envelope.Blocks)
	}
}

// TestAssemblerReplayStateDiscardedWhenMisaligned 钉住条数对不上时整个信封作废：
// 一份对不齐的元数据比没有元数据更坏，它会把某一块的私有状态安到另一块头上。
func TestAssemblerReplayStateDiscardedWhenMisaligned(t *testing.T) {
	assembler := NewBlockAssembler()
	pushAll(assembler,
		TextDeltaChunk{Index: 0, Text: "只有一块"},
		FinishChunk{
			Reason: StopFinish{},
			ReplayState: &ReplayEnvelope{
				Response: json.RawMessage(`{"raw":1}`),
				Blocks:   []json.RawMessage{json.RawMessage(`"一"`), json.RawMessage(`"二"`)},
			},
		},
	)

	if _, present, err := assembler.ReplayState(); present || err != nil {
		t.Fatalf("对不齐时整份信封该作废，得到 present=%v err=%v", present, err)
	}
}

// TestAssemblerMessage 钉住交出来的是一条署了名的助手消息，内容就是装配好的那些块。
func TestAssemblerMessage(t *testing.T) {
	assembler := NewBlockAssembler()
	assembler.Push(TextDeltaChunk{Index: 0, Text: "你好"})

	message, err := assembler.Message(PluginSource{Plugin: AssemblerSource})
	if err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if message.Role != RoleAssistant {
		t.Fatalf("角色该是 assistant，得到 %q", message.Role)
	}
	if message.ID == "" {
		t.Fatal("消息该带一个身份")
	}
	source, ok := message.Source.(PluginSource)
	if !ok || source.Plugin != AssemblerSource {
		t.Fatalf("来源该是传进去那一份，得到 %+v", message.Source)
	}
	if text := message.Content[0].(TextBlock); text.Text != "你好" {
		t.Fatalf("内容不对，得到 %q", text.Text)
	}
}
