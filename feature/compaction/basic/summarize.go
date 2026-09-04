// 本文件的作用：真的把那次总结请求发出去——按上一次路由请求的样子重放一遍
// 对话前缀，末尾接上那条总结指令，收回来的东西投影成一份只有文字的安全摘要。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:110-224

package basic

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/llm"
)

// Streamer 是本文件要的那一小片 LLM 接缝。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:151（ctx.llm.stream）
//
// 新增: DSH 从 cordis 上取整个 `ctx.llm` 服务，实际只调 `stream()` 一个方法。
// 这里摆成一个单方法接口明着传进来——签名和 [llm.Runtime.Stream] 逐字相同，
// 所以一个真的运行时结构上就满足它，装配方直接填进去。写窄一点还有一个后果：
// 本包在类型上就发不出别的模型请求，一次总结**只可能**是这一次调用。
type Streamer interface {
	// Stream 发一次流式模型请求。
	Stream(ctx context.Context, options llm.GenerateOptions) (iter.Seq2[llm.StreamChunk, error], error)
}

// compactionPlugin 是那条总结指令消息的来源署名。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:157
//
// 字面量照抄 DSH 的包名：它会跟着那条消息一起被提供方看见，也会进重放脚本，
// 换掉它等于换掉一个协议里的字面量。
const compactionPlugin = "dsh-compaction-basic"

// compactionInstruction 是那条总结指令。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:31-66
//
// 它作为**最后一条用户消息**发出去，而不是另起一个总结专用的系统提示词。
// 这是有讲究的：把这段对话自己的系统提示词、工具表和前面那串消息原样摆在它
// 前面，这次额外的调用就恰好是上一次路由请求的一个前缀，提供方那边的 KV 缓存
// 因此是复用而不是作废。换成独立的系统提示词，整个前缀就变了。
//
// 英文原样照抄，一个字都没动：这是给模型读的指令，而且里面那对
// [summaryOpenTag]／[summaryCloseTag] 要让下一次总结认出「上面那段是一份更早的
// 检查点」。翻译它等于换掉一个协议里的字面量。
const compactionInstruction = `You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.

Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write "(none)" for an empty section — never drop a section.

## Primary Request and Intent
- [the user's original and evolving goals; quote verbatim where the exact wording matters]

## Key Technical Concepts
- [technologies, frameworks, patterns, and conventions in play]

## Files and Code
- [exact path: why it matters, key changes or snippets]

## Errors and Fixes
- [error: how it was resolved, plus any related user feedback]

## Pending Jobs
- [explicitly requested work not yet completed]

## Current Work
- [precisely what was in progress at this checkpoint]

## Next Step
- [the single next action, directly in line with the most recent request, or "(none)"]

## Critical Context
- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]

Rules:
- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.
- Capture user feedback and explicit instructions faithfully, especially corrections.
- Do NOT mention this summarization request or that the context was compacted.
- Output only the checkpoint text: do not call any tool or take any other action.
- If the conversation already contains a ` + summaryOpenTag + ` block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.`

// SummarizeWithLLM 跑一次复用缓存的总结调用：重放对话前缀，末尾接上那条总结
// 指令，收回来的输出投影成一份只有文字的安全摘要。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:110-182
//
// policy 只用到 [Policy.Summarization] 和 [Policy.MaxTokens] 两个字段。
// 收整份 [Policy] 而不是那两个值，是为了让调用方没有机会把「配了摘要路由」和
// 「配了生成上限」拆开传——这两样在 DSH 那边是同一个私有的 SummaryConfig。
//
// 新增: DSH 收一整个 `Agent`，用到的只有 `session.requestHeader()`、
// `session.id` 和 `options.provider/model` 三样，正好就是 [compaction.AgentContext]。
// 取消从末尾那个可选的 AbortSignal 换成头一个参数的 ctx，理由同接缝那一层。
func SummarizeWithLLM(
	ctx context.Context,
	stream Streamer,
	policy Policy,
	input SummarizationInput,
	agent compaction.AgentContext,
) (SummaryResult, error) {
	target, err := summarizationTarget(policy, agent)
	if err != nil {
		return SummaryResult{}, err
	}

	messages := make([]llm.Message, 0, len(input.Messages)+1)
	messages = append(messages, input.Messages...)
	messages = append(messages, llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: compactionInstruction}},
		llm.PluginSource{Plugin: compactionPlugin},
	))

	options := llm.GenerateOptions{
		Provider:  target.Provider,
		Model:     target.Model,
		Messages:  messages,
		System:    input.System,
		Tools:     input.Tools,
		MaxTokens: policy.MaxTokens,
		SessionID: llm.SessionID(agent.Session.ID()),
		Purpose:   llm.PurposeCompaction,
	}

	assembler := llm.NewBlockAssembler()
	chunks, err := stream.Stream(ctx, options)
	if err != nil {
		return SummaryResult{}, fmt.Errorf("compaction-basic：总结请求发不出去：%w", err)
	}
	for chunk, err := range chunks {
		if err != nil {
			return SummaryResult{}, fmt.Errorf("compaction-basic：总结这一路读断了：%w", err)
		}
		assembler.Push(chunk)
	}
	if err := finishError(assembler.Finish()); err != nil {
		return SummaryResult{}, err
	}

	rawOutput, err := assembler.Blocks()
	if err != nil {
		return SummaryResult{}, fmt.Errorf("compaction-basic：总结的输出装不起来：%w", err)
	}
	summary, err := summaryText(rawOutput)
	if err != nil {
		return SummaryResult{}, err
	}

	result := SummaryResult{
		Summary:       summary,
		Provider:      options.Provider,
		Model:         options.Model,
		MaxTokens:     policy.MaxTokens,
		RawOutput:     rawOutput,
		LLMStreamCall: true,
	}
	if usage, reported := assembler.Usage(); reported {
		result.Usage = &usage
	}
	return result, nil
}

// summarizationTarget 挑出这次总结该发给谁。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:122-140
//
// 三档回落，顺序有意义：显式配的摘要路由最优先（它就是为了「用一个更便宜的模型
// 写摘要」而存在的）；没配就跟着**最近一次真的路由出去的那份请求头**走，那也是
// 前缀缓存唯一对得上的那一个；再没有才退到 agent 自己那份选项——一段还没发过
// 任何请求的会话只有这一个答案。
func summarizationTarget(policy Policy, agent compaction.AgentContext) (Target, error) {
	if !policy.Summarization.IsZero() {
		return policy.Summarization, nil
	}
	// 请求头读不出来时不报错，往下一档退。DSH 那边是 `?.` 加 `??`，同一个意思：
	// 一段读不出请求头的日志不代表这次总结做不了，它只是少了一个线索。
	if header, ok, err := agent.Session.RequestHeader(); err == nil && ok {
		if header.Config.Provider != "" && header.Config.Model != "" {
			return Target{Provider: header.Config.Provider, Model: header.Config.Model}, nil
		}
	}
	if agent.Provider != "" && agent.Model != "" {
		return Target{Provider: agent.Provider, Model: agent.Model}, nil
	}
	return Target{}, errors.New(
		"compaction-basic：没有可用于总结的 provider/model：" +
			"要么把配置里 summarization 那两个字段都填上，要么先路由出去一次请求，" +
			"要么把 agent 那两个选项都填上")
}

// finishError 把一次终止的总结映射成它那条「失败即失败」的错误。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:198-214
//
// 撞上生成上限也算失败：一份被截断的检查点读起来是完整的，而它丢掉的正是
// 末尾那几节——真让它落到表面上，模型会拿一份缺了尾巴的摘要当作全部历史。
func finishError(finish llm.FinishReason) error {
	switch typed := finish.(type) {
	case llm.ErrorFinish:
		return &llm.Error{Failure: typed.Failure}
	case llm.AbortedFinish:
		return &llm.Error{Failure: typed.Failure}
	case llm.MaxTokensFinish:
		return llm.NewError("summarization truncated at the token cap (incomplete checkpoint)",
			"MAX_TOKENS", nil)
	default:
		return nil
	}
}

// summaryText 拒掉带图的输出，只留文字，然后才拿去合成那条用户消息。
//
// 源: packages/compaction/compaction-basic/src/summarizer.ts:217-224
//
// 拒图不是洁癖：那份摘要要变成一条**落在表面上的用户消息**，之后每一次请求都带着
// 它。一张图在这个位置会被无限次重发，而且下游那些只认文字的路径会静默丢掉它。
//
// 新增: DSH 那句「摘要里一个字都没有」的检查（summarizer.ts:163-165）折进了这里，
// 它和过滤是同一件事的两半：过滤完剩下什么，当场就该判。
func summaryText(blocks llm.Content) (llm.Content, error) {
	if llm.ContentHasImage(blocks) {
		return nil, llm.NewError("compaction summary cannot contain image output",
			"UNSUPPORTED_CONTENT", nil)
	}
	text := make(llm.Content, 0, len(blocks))
	blank := true
	for _, block := range blocks {
		typed, isText := block.(llm.TextBlock)
		if !isText {
			continue
		}
		text = append(text, typed)
		if strings.TrimSpace(typed.Text) != "" {
			blank = false
		}
	}
	if blank {
		return nil, errors.New("compaction-basic：这次总结一个字的摘要都没产出")
	}
	return text, nil
}
