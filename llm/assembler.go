// 本文件的作用：把一条原始分块流一块一块地装配成完整的内容块和一条助手消息。
// 这是本装置唯一那份规范的装配算法——循环一边喂它、一边把原始分块照原样记进
// 日志留着重放，两边不会各自算出不同的结果。
//
// 源: packages/llm/llm/src/assembler.ts:1-207

package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// partialBlock 是一块还在攒的内容。
//
// 源: packages/llm/llm/src/assembler.ts:15-23
type partialBlock struct {
	// blockType 是这块的类型，由 block-start 或者第一条增量定下来。
	blockType BlockType
	// text 是攒到现在的文本，text/reasoning 两种块用。
	text strings.Builder
	// toolCallID、toolCallName、toolCallArguments 是 tool-call 那种块攒的三样。
	toolCallID        CallID
	toolCallName      string
	toolCallArguments strings.Builder
	// block 由 block-end 填上——它是权威的，一填上这块就冻住了。
	block ContentBlock
	// closed 说明 block 那个字段填过没有。
	//
	// 新增: DSH 直接看 partial.block 是不是 undefined。Go 的接口零值也是 nil，
	// 本可以照做，但 [ContentBlock] 是一个接口，一个装着零值结构体的非 nil 接口
	// 和一个 nil 接口在 == nil 上表现不同，多一位布尔比让每个读者都想清楚这件事
	// 便宜得多。
	closed bool
}

// BlockAssembler 把原始的 [StreamChunk] 一块一块攒成完整的 [ContentBlock] 和最后
// 那条助手 [Message]。
//
// 源: packages/llm/llm/src/assembler.ts:25-207
//
// 循环一边喂它、一边把原始分块记进日志留着重放，流结束之后读 [BlockAssembler.Blocks]／
// [BlockAssembler.Message]／[BlockAssembler.Usage]／[BlockAssembler.Finish]；
// 取消把流截断时读 [BlockAssembler.InterruptedBlocks]。
//
// 它容得下只发增量、不发 block-start/block-end 的协议；一个已经被 block-end 关掉的
// 下标之后再来的增量一律忽略（那是一条畸形的流），于是一个行为不端的适配器既撑不大
// 内存，也弄不脏一块已经完成的内容。
//
// 新增: 这个类型不是并发安全的。DSH 那边是单线程 JS，天然没有这个问题；Go 这边
// 不加锁，是因为它的用法只有一种——一个 goroutine 顺着一条流从头喂到尾，喂完了
// 才读。要是有第二个 goroutine 边喂边读，它读到的就是一份半截状态，那不是锁能
// 修好的事。
type BlockAssembler struct {
	// partials 是每个下标那块攒到现在的状态。
	partials map[int]*partialBlock
	// order 是各个下标第一次出现的次序——装配出来的块按它排。
	//
	// 新增: DSH 用一个 JS Map 加一个 order 数组，Map 本身也保插入顺序，那个数组
	// 是为了让「顺序」这件事在代码里看得见。Go 的 map 不保任何顺序，所以这个数组
	// 在这里不是好看，是必须。
	order []int
	// usage、hasUsage 是 usage 分块带来的记账。
	usage    TokenUsage
	hasUsage bool
	// finish 是 finish 分块带来的结束原因，nil 表示流还没给过。
	finish FinishReason
	// replayState 是终止 finish 分块带来的重放信封，nil 表示没有。
	replayState *ReplayEnvelope
}

// NewBlockAssembler 造一个空的装配器。
func NewBlockAssembler() *BlockAssembler {
	return &BlockAssembler{partials: make(map[int]*partialBlock)}
}

// Push 把一个分块喂进装配状态，chunk 要按流顺序来。
//
// 源: packages/llm/llm/src/assembler.ts:44-95
//
// 新增: DSH 的 switch 末尾有一句 assertNever(chunk)，那是 TS 用来在编译期证明
// 「所有变体都处理到了」的技巧。Go 这边 [StreamChunk] 是一个封了口的接口，
// 一个不认识的具体类型只可能来自本包自己，所以这里落到 default 就是本包的 bug，
// 直接 panic——和本仓库 fs/objectstore 那边处理「不可能发生的操作」是同一个立场。
func (a *BlockAssembler) Push(chunk StreamChunk) {
	switch typed := chunk.(type) {
	case BlockStartChunk:
		if _, present := a.partials[typed.Index]; !present {
			a.open(typed.Index, typed.BlockType)
		}
	case TextDeltaChunk:
		partial := a.ensure(typed.Index, BlockText)
		if partial.closed {
			return // 已经被 block-end 关掉了，迟到的增量丢掉
		}
		partial.text.WriteString(typed.Text)
	case ReasoningDeltaChunk:
		partial := a.ensure(typed.Index, BlockReasoning)
		if partial.closed {
			return
		}
		partial.text.WriteString(typed.Text)
	case ToolCallDeltaChunk:
		partial := a.ensure(typed.Index, BlockToolCall)
		if partial.closed {
			return
		}
		partial.toolCallID = typed.ID
		if typed.Name != nil && *typed.Name != "" {
			partial.toolCallName = *typed.Name
		}
		partial.toolCallArguments.WriteString(typed.ArgumentsDelta)
	case BlockEndChunk:
		partial := a.ensure(typed.Index, typed.Block.BlockType())
		// 第一次关掉的那次算数：忽略重复的关闭，才能让流式吐出去的和最后装配出来的
		// 那一块说的是同一件事。
		if partial.closed {
			return
		}
		partial.block = typed.Block
		partial.closed = true
	case UsageChunk:
		a.usage = typed.Usage
		a.hasUsage = true
	case FinishChunk:
		a.finish = typed.Reason
		a.replayState = typed.ReplayState
	default:
		panic(fmt.Sprintf("llm: BlockAssembler.Push 收到本包不认识的分块类型 %T", chunk))
	}
}

// open 新起一块，并把它的下标记进次序。
func (a *BlockAssembler) open(index int, blockType BlockType) *partialBlock {
	partial := &partialBlock{blockType: blockType}
	a.partials[index] = partial
	a.order = append(a.order, index)
	return partial
}

// ensure 取出某个下标那块，没有就按 blockType 新起一块。
//
// 源: packages/llm/llm/src/assembler.ts:97-105
//
// 这就是「容得下只发增量的协议」那句话的实现：没有 block-start 也能开块。
func (a *BlockAssembler) ensure(index int, blockType BlockType) *partialBlock {
	if partial, present := a.partials[index]; present {
		return partial
	}
	return a.open(index, blockType)
}

// assemble 把一块攒到现在的状态定成一个完整的内容块。
//
// 源: packages/llm/llm/src/assembler.ts:107-120
//
// 第二个返回值为假表示这块的类型本包装配不出来——它既没被 block-end 关掉、
// 类型又不是那三种攒得出来的。
//
// 新增: DSH 那一支抛错。这里交一个 bool 出去，因为两个调用方要的行为不一样：
// [BlockAssembler.Blocks] 交出错误，[BlockAssembler.InterruptedBlocks] 直接跳过。
// DSH 靠 interruptedBlocks 先自己判一次类型来绕开那次抛错，那等于把同一份
// 「哪些类型攒得出来」的知识写了两遍。
func (a *BlockAssembler) assembleBlock(partial *partialBlock, index int) (ContentBlock, bool) {
	if partial.closed {
		return partial.block, true
	}
	switch partial.blockType {
	case BlockText:
		return TextBlock{Text: partial.text.String()}, true
	case BlockReasoning:
		return ReasoningBlock{Text: partial.text.String()}, true
	case BlockToolCall:
		id := partial.toolCallID
		if id == "" {
			// 适配器一个 id 都没给过时兜一个出来：工具结果要靠它和调用对上，
			// 没有 id 的调用就是一次派发不出去的调用。
			id = CallID(fmt.Sprintf("call-%d", index))
		}
		return ToolCallBlock{ID: id, Name: partial.toolCallName, Arguments: partial.toolCallArguments.String()}, true
	default:
		return nil, false
	}
}

// assembled 是「所有见过的块里，哪些留哪些丢」这个唯一的决定。
//
// 源: packages/llm/llm/src/assembler.ts:129-149
//
// 撞上输出上限的截断要丢掉那些没法安全执行的工具调用；吐出去的块和重放元数据
// 都从这一个结果推出来，所以两边不可能各说各话。
func (a *BlockAssembler) assembled() ([]ContentBlock, *ReplayEnvelope, error) {
	all := make([]ContentBlock, 0, len(a.order))
	for _, index := range a.order {
		partial := a.partials[index]
		block, ok := a.assembleBlock(partial, index)
		if !ok {
			return nil, nil, fmt.Errorf(
				"%w：装配不出一块没结束的 %q 类型内容", ErrMalformedValue, partial.blockType)
		}
		all = append(all, block)
	}

	// kept 为 nil 表示这次不丢任何块；非 nil 时每一位对应 all 里同一位置那块。
	var kept []bool
	if a.Finish().FinishKind() == FinishMaxTokens {
		kept = make([]bool, len(all))
		for index, block := range all {
			kept[index] = block.BlockType() != BlockToolCall
		}
	}

	blocks := all
	if kept != nil {
		blocks = make([]ContentBlock, 0, len(all))
		for index, block := range all {
			if kept[index] {
				blocks = append(blocks, block)
			}
		}
	}

	envelope := a.replayState
	if envelope == nil || envelope.Blocks == nil {
		return blocks, envelope, nil
	}
	if len(envelope.Blocks) != len(all) {
		// 条数对不上就整个信封作废：一份对不齐的元数据比没有元数据更坏，
		// 它会把某一块的私有状态安到另一块头上。
		return blocks, nil, nil
	}
	if kept == nil || len(blocks) == len(all) {
		return blocks, envelope, nil
	}
	pruned := ReplayEnvelope{Response: envelope.Response, Blocks: make([]json.RawMessage, 0, len(blocks))}
	for index, entry := range envelope.Blocks {
		if kept[index] {
			pruned.Blocks = append(pruned.Blocks, entry)
		}
	}
	return blocks, &pruned, nil
}

// Blocks 按流顺序装配出到现在为止见过的所有块。
//
// 源: packages/llm/llm/src/assembler.ts:151-159
//
// 每个见过的下标出一块，只有撞上输出上限的截断会丢掉那些没法安全执行的工具调用。
// 一块还开着的内容从它攒下来的增量装配出来；一个既没被 block-end 关掉、类型又
// 认不出来的块让整趟装配报 [ErrMalformedValue]。
func (a *BlockAssembler) Blocks() ([]ContentBlock, error) {
	blocks, _, err := a.assembled()
	return blocks, err
}

// InterruptedBlocks 装配出一条被打断的流可以安全定稿的那个前缀：关掉的和还开着的
// text/reasoning 块里，内容不全是空白的那些，按流顺序排。
//
// 源: packages/llm/llm/src/assembler.ts:161-178
//
// 工具调用一律不要，因为打断发生在派发之前，留一个下来就得替它编一份结果。
// 还开着的、类型认不出来的块同样不要。
func (a *BlockAssembler) InterruptedBlocks() []ContentBlock {
	var kept []ContentBlock
	for _, index := range a.order {
		partial := a.partials[index]
		blockType := partial.blockType
		if partial.closed {
			blockType = partial.block.BlockType()
		}
		if blockType != BlockText && blockType != BlockReasoning {
			continue
		}
		block, ok := a.assembleBlock(partial, index)
		if !ok {
			// 走不到：上面那一句已经把类型筛成 text/reasoning 两种，
			// [BlockAssembler.assembleBlock] 对这两种都装配得出来。
			continue
		}
		if text, isText := block.(TextBlock); isText && strings.TrimSpace(text.Text) != "" {
			kept = append(kept, block)
			continue
		}
		if reasoning, isReasoning := block.(ReasoningBlock); isReasoning && strings.TrimSpace(reasoning.Text) != "" {
			kept = append(kept, block)
		}
	}
	return kept
}

// Usage 交出 usage 分块带来的记账。第二个返回值为假表示还没来过。
//
// 源: packages/llm/llm/src/assembler.ts:180-183
func (a *BlockAssembler) Usage() (TokenUsage, bool) {
	return a.usage, a.hasUsage
}

// Finish 交出 finish 分块带来的结束原因；流没给过时是 [StopFinish]。
//
// 源: packages/llm/llm/src/assembler.ts:185-188
func (a *BlockAssembler) Finish() FinishReason {
	if a.finish == nil {
		return StopFinish{}
	}
	return a.finish
}

// ReplayState 交出终止 finish 分块带来的重放元数据，每块一条的那些条目跟着
// [BlockAssembler.Blocks] 一起裁剪。第二个返回值为假表示没有，或者信封里的条目
// 和实际发出的块对不齐。
//
// 源: packages/llm/llm/src/assembler.ts:190-197
func (a *BlockAssembler) ReplayState() (ReplayEnvelope, bool, error) {
	_, envelope, err := a.assembled()
	if err != nil || envelope == nil {
		return ReplayEnvelope{}, false, err
	}
	return *envelope, true, nil
}

// AssemblerSource 是装配器给自己造的那条消息署的默认来源。
//
// 源: packages/llm/llm/src/assembler.ts:204
const AssemblerSource = "dsh-llm/assembler"

// Message 交出装配好的那条助手消息，source 是它的产出方署名。
//
// 源: packages/llm/llm/src/assembler.ts:199-206
//
// 新增: DSH 那边 source 有默认值 {kind:'plugin', plugin:'dsh-llm/assembler'}。
// Go 没有默认参数，要那一份就传 PluginSource{Plugin: [AssemblerSource]}。
func (a *BlockAssembler) Message(source MessageSource) (Message, error) {
	blocks, err := a.Blocks()
	if err != nil {
		return Message{}, err
	}
	return NewMessage(RoleAssistant, blocks, source), nil
}
