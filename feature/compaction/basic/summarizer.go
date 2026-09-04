// 本文件的作用：这一层里和总结调用有关、但**不需要真的发出去**的那一半——
// 一次总结要喂进去的东西长什么样、总结回来的东西长什么样，
// 以及把一份摘要裹成落在表面上的那条检查点消息。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts

package basic

import "github.com/snight1983/ds-harness-go/llm"

// 裹在检查点节点里那份结构化摘要外面的一对标签。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:21-22
//
// 英文原样照抄：这两个标签会进模型看的文本，也会被下一次总结认出来
// （「上面那段是一份更早的检查点」），翻译它们等于换掉一个协议里的字面量。
const (
	summaryOpenTag  = "<compacted-summary>"
	summaryCloseTag = "</compacted-summary>"
)

// checkpointPreamble 是摆在摘要前面、把它说成「已经成立的背景」的那句话。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:69-70
//
// 同样英文原样照抄，理由同上：它是给模型读的。
const checkpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

// SummarizationInput 是要被压掉的那一段对话表面，按它原本被路由出去的样子重放。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:78-85
//
// 系统提示词、工具表、前面那串消息都照原样重放一遍，不是为了让模型多看点东西，
// 而是为了让这次额外的总结请求**恰好是**上一次路由请求的一个前缀——那样提供方
// 那边的前缀缓存才命中得上，只有末尾那条总结指令是新的。
type SummarizationInput struct {
	// System 是这段对话自己的系统提示词；空串表示那次请求本来就没有系统提示词。
	//
	// 新增: DSH 是 `system?: string`。这里空串当「没有」用得起来，因为一个空的
	// 系统提示词和不带系统提示词发出去是同一个请求。
	System string
	// Tools 是这段对话自己的工具表；nil 表示那次请求本来就没带工具。
	Tools []llm.ToolSchema
	// Messages 是被遮那一段，按表面顺序，排在总结指令前面。
	Messages []llm.Message
}

// SummaryResult 是一次总结交出来的安全摘要，外加那次调用的信封原样。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:87-108（SummaryResult）
//
// 这几个字段直接落进 [compaction.SummaryData] 的尾巴，所以两边的语义要对齐：
// LLMStreamCall 为真就必须带 RawOutput，那一条在摘要事件排出去的那一刻校验
// （见 compaction.SummaryData.MarshalJSON）。这里不再重复校验一遍——同一条规则
// 有两处判定，早晚会分岔，而落库那一处是绕不过去的那一处。
//
// 新增: DSH 是「共同字段」和「rawOutput/llmStreamCall 二选一」的交叉类型。
// Go 没有这种类型运算，摊成平的结构体，靠上面那条不变量把两种形状分开——
// 和 compaction.SummaryData 同一个办法。
type SummaryResult struct {
	// Summary 是投影成纯文本之后的安全摘要内容。
	Summary llm.Content
	// Provider 是实际写这份摘要的提供方路由。
	Provider string
	// Model 是实际写这份摘要的模型。
	Model string
	// MaxTokens 是那次调用发出去的生成上限；0 表示没设上限。
	MaxTokens int
	// Usage 是提供方报回来的这次总结请求的用量；nil 表示没报。
	Usage *llm.TokenUsage
	// RawOutput 是提供方完整的原始输出，投影成安全摘要之前的样子；nil 表示没有。
	RawOutput llm.Content
	// LLMStreamCall 为真表示这份摘要**恰好**来自一次走本上下文 LLM 接缝的调用，
	// 此时 RawOutput 必须有。
	LLMStreamCall bool
}

// FrameSummary 把一份摘要裹成落在表面上的那条检查点消息的内容。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:189-195
//
// 前言和开标签合在同一块文本里（中间空一行），后面跟摘要本身，最后一块是闭标签
// ——照 DSH 的排法，因为这三块的边界会原样进模型看到的文本。
func FrameSummary(summary llm.Content) llm.Content {
	framed := make(llm.Content, 0, len(summary)+2)
	framed = append(framed, llm.TextBlock{Text: checkpointPreamble + "\n\n" + summaryOpenTag})
	framed = append(framed, summary...)
	return append(framed, llm.TextBlock{Text: summaryCloseTag})
}
