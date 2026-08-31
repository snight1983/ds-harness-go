// 本文件的作用：把 OpenAI 兼容线上协议那串扁平的增量翻成 harness 的分块协议，
// 把 token 记账对上口径，以及把提供方五花八门的失败归一成那几个稳定码。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts

package openaicompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"ds-harness-go/llm"
)

// reasoningContentField 是推理文本在增量里的那个字段名。
//
// 新增: DSH 那边推理是 pi-ai 的 thinking_* 事件，由那个库从各家协议里认出来。
// OpenAI 自己的 Chat Completions 协议**没有**这个字段——它的推理不外露。
// 但说这套协议的那些提供方（DeepSeek 最先，之后 vLLM、SGLang、Ollama、
// OpenRouter 都跟上了）一致地用 delta.reasoning_content 这个额外字段吐推理，
// 而 openai-go 把认不得的字段收在 JSON.ExtraFields 里，正好取得到。
//
// 认不出来的代价是推理文本整段消失（不会串进正文，因为它压根不在 content 里），
// 所以这里认它，而不是要求每条路由自己配。
const reasoningContentField = "reasoning_content"

// mapUsage 把线上的 token 记账翻成 harness 的口径。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:22-29
//
// 新增: DSH 拿的是 pi-ai 已经归一好的四个数。这条协议上要自己减：prompt_tokens
// 是**含**缓存命中的总数（DeepSeek 就是这么报的），而 [llm.TokenUsage] 要求
// InputTokens／CacheReadTokens／CacheWriteTokens 三者互不重叠、加起来才是计费输入。
// 不减的话缓存命中会被算两遍，一段长会话的输入统计会虚高到离谱。
//
// 减完钳在 0：一个把 cached_tokens 报得比 prompt_tokens 还大的提供方不该让
// 输入计数变成负数——负数会一路串进预算和压缩触发的算式里。
func mapUsage(usage openai.CompletionUsage) llm.TokenUsage {
	cacheRead := int(usage.PromptTokensDetails.CachedTokens)
	cacheWrite := int(usage.PromptTokensDetails.CacheWriteTokens)
	return llm.TokenUsage{
		InputTokens:      max(0, int(usage.PromptTokens)-cacheRead-cacheWrite),
		OutputTokens:     int(usage.CompletionTokens),
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		// 线上的 reasoning_tokens 是 completion_tokens 的**一部分**，不是另加的一笔。
		// 这里照搬这个口径：OutputTokens 已经含它，这一项只是把其中多少花在推理上
		// 单独说出来。
		ReasoningTokens: int(usage.CompletionTokensDetails.ReasoningTokens),
	}
}

// httpErrorCode 是从 HTTP 状态码到稳定失败码的那张表。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:39-65
//
// 新增: DSH 只能对着一段文本做正则——它上面那条 XXX 注释写着，pi-ai 把原始错误
// 拍平成了 error.message，状态码和 cause 链在到达它之前就没了，所以它「只剩下
// 在这里匹配几个干巴巴的词」，并且明说「如果哪天 pi-ai 转发原始 Error，就改成
// 按 code/cause 分类」。这条路本来就拿得到状态码（[openai.Error].StatusCode），
// 所以这里就是那个「改成按码分类」的版本，文本匹配只当状态码缺席时的兜底。
func httpErrorCode(status int, detail string) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "AUTH"
	case status == http.StatusTooManyRequests:
		// 额度用光和请求太密都可能是 429，但两者的修法完全相反：前者重试一万次
		// 也不会好，后者等一会就好。所以先问文本是不是在说额度。
		if llm.IsQuotaExceededError(detail) {
			return llm.QuotaExceededCode
		}
		return "RATE_LIMIT"
	case status == http.StatusRequestEntityTooLarge:
		// 请求体被拒（网关或者提供方的体积上限）：原样重发一次不可能成功，
		// 所以这是「请求不合法」而不是「暂时不行」。
		return "INVALID_REQUEST"
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		if llm.IsContextWindowExceededError(detail) {
			return llm.ContextWindowExceededCode
		}
		return "INVALID_REQUEST"
	case status == http.StatusPaymentRequired:
		return llm.QuotaExceededCode
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return "TIMEOUT"
	case status >= 500:
		return "SERVER"
	case status >= 400:
		return "INVALID_REQUEST"
	}
	return ""
}

// transportWording 认的是连接在半路断掉的那几族说法。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:49-63
//
// 只有拿不到状态码时才走到这里——拿不到状态码，本身就说明这次失败发生在
// 收到响应头之前或者响应读到一半，也就是传输层。文本匹配是为了把它和
// 「超时」分开，因为这两者在重试策略上的退避不一样。
var transportWording = regexp.MustCompile(
	`(?i)\b(?:network|connection|socket|refused|reset|unexpected EOF|broken pipe|` +
		`no such host|terminated|premature close|stream ended)\b|\bECONN[A-Z]+\b`)

// timeoutWording 认的是超时那一族说法。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:48
var timeoutWording = regexp.MustCompile(`(?i)\btime(?:d)?\s*out\b|\bdeadline exceeded\b`)

// classifyError 把一条上游错误归一成一份 [llm.Failure]。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:39-65
//
// 归一的口径：先看 HTTP 状态码（有就以它为准），没有就看文本。落到最后那个
// PROVIDER_ERROR 的都不在默认可重试集合里——一条谁也没认出来的失败重来一次
// 多半还是同样地失败，而白等一轮退避是有代价的。
func classifyError(err error) llm.Failure {
	failure := llm.Failure{Message: err.Error(), Code: "PROVIDER_ERROR"}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		failure.Status = apiErr.StatusCode
		if apiErr.Message != "" {
			failure.Message = apiErr.Message
		}
		if apiErr.Response != nil {
			// 提供方签发的请求标识只为诊断留着——对着它去问提供方的日志和账单，
			// 是唯一能把「我这边看到的失败」和「他们那边记的那次请求」对上的东西。
			if id := apiErr.Response.Header.Get("X-Request-Id"); id != "" {
				failure.RequestID = llm.ProviderRequestID(id)
			}
		}
		if code := httpErrorCode(apiErr.StatusCode, failure.Message); code != "" {
			failure.Code = code
			return failure
		}
	}

	detail := failure.Message
	switch {
	case llm.IsContextWindowExceededError(detail):
		failure.Code = llm.ContextWindowExceededCode
	case llm.IsQuotaExceededError(detail):
		failure.Code = llm.QuotaExceededCode
	case timeoutWording.MatchString(detail):
		failure.Code = "TIMEOUT"
	case transportWording.MatchString(detail):
		failure.Code = "TRANSPORT"
	}
	return failure
}

// mapFinishReason 把线上的 finish_reason 翻成 harness 的终止原因。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:76-116
//
// hasContent 说的是这次响应到底有没有产出内容块；contextWindow 是目录里这个模型的
// 上下文容量，用来把「一个 token 都没输出的 length」认成上下文溢出。
func mapFinishReason(
	reason string,
	model string,
	hasContent bool,
	usage llm.TokenUsage,
	contextWindow int,
) llm.FinishReason {
	switch reason {
	case "stop":
		// 一次正常结束却一个内容块都没有，是提供方的一次退化完成，不是一条
		// 「内容为空」的成功助手消息。
		//
		// 源: packages/llm/llm-pi-ai/src/stream.ts:92-104
		if !hasContent {
			return llm.ErrorFinish{Failure: llm.Failure{
				Message: fmt.Sprintf("model %q returned a completed response with no content", model),
				Code:    llm.EmptyResponseCode,
			}}
		}
		return llm.StopFinish{}
	case "length":
		// 一个 token 都没输出就撞上了 length，而输入已经把窗口填满了：模型压根
		// 没有地方写回答，这是上下文溢出而不是「答长了」。两者的修法完全不同
		// ——前者要压缩历史，后者要调高 max_tokens——所以必须分开。
		//
		// 源: packages/llm/llm-pi-ai/src/stream.ts:77-89
		//
		// 新增: DSH 那半边是 pi-ai 的 isContextOverflow，那个函数的判据在库里，
		// 读不到，所以这里只写下这一条**自己说得清**的判据：零输出 + 输入不小于
		// 窗口。DSH 文档里另提到的「stop 且用量超过窗口也算溢出」这一支不要——
		// 一次产出了内容的 stop 是一次成功的响应，把它判成失败会让一个本来答完了的
		// 回合失败掉。
		if contextWindow > 0 && usage.OutputTokens == 0 &&
			usage.InputTokens+usage.CacheReadTokens+usage.CacheWriteTokens >= contextWindow {
			return llm.ErrorFinish{Failure: llm.Failure{
				Message: fmt.Sprintf(
					"model %q had no room left to answer: the request filled its %d-token context window",
					model, contextWindow),
				Code: llm.ContextWindowExceededCode,
			}}
		}
		return llm.MaxTokensFinish{}
	case "tool_calls", "function_call":
		return llm.ToolCallsFinish{}
	case "content_filter":
		// 新增: pi-ai 的停止原因联合里没有这一支，所以 DSH 没有对应的分支。
		// 这条协议上它是一个正经取值：提供方把内容拦下了。它不是可重试的
		// ——同样的输入会被同样地拦住——所以给一个自己的码，而不是并进 SERVER。
		return llm.ErrorFinish{Failure: llm.Failure{
			Message: fmt.Sprintf("model %q stopped because its content filter fired", model),
			Code:    "CONTENT_FILTER",
		}}
	}
	// 源: packages/llm/llm-pi-ai/src/stream.ts:210
	//
	// 认不得的（含空串：流在提供方给出 finish_reason 之前就断了）一律当成流被截断。
	// 这是传输层的事，不是模型的事，所以是可重试的 TRANSPORT。
	return llm.ErrorFinish{Failure: llm.Failure{
		Message: fmt.Sprintf("model %q ended the stream with finish reason %q", model, reason),
		Code:    "TRANSPORT",
	}}
}

// openBlock 是正在装配中的那一块。
//
// 新增: DSH 完全不需要这个东西——pi-ai 已经把线上的扁平增量整理成了
// text_start/text_delta/text_end 这种带边界的事件。这条协议没有边界事件：
// 一条 delta 里只有 content、reasoning_content、tool_calls 三样，块的开始和结束
// 得由**字段发生变化**这件事自己推出来。这个结构就是那次推导的状态。
type openBlock struct {
	// index 是这一块在本次响应里的下标。
	index int
	// kind 是这一块的类型。
	kind llm.BlockType
	// text 是到目前为止收到的文本（工具调用时是参数原文）。
	text strings.Builder
	// callID 与 callName 只有工具调用块用得上。
	callID   string
	callName string
}

// blockAssembly 是一次响应装配过程中的全部状态。
type blockAssembly struct {
	// next 是下一块要用的下标。
	next int
	// current 是当前开着的文本或推理块；nil 表示没有。
	current *openBlock
	// calls 是按线上 tool_calls[].index 索引的那些工具调用块。
	calls map[int64]*openBlock
	// callOrder 是工具调用块按首次出现的次序，收尾时按它逐块收。
	callOrder []int64
}

// closeCurrent 收掉当前开着的文本或推理块。
func (a *blockAssembly) closeCurrent(emit func(llm.StreamChunk)) {
	if a.current == nil {
		return
	}
	block := a.current
	a.current = nil
	if block.kind == llm.BlockReasoning {
		emit(llm.BlockEndChunk{Index: block.index, Block: llm.ReasoningBlock{Text: block.text.String()}})
		return
	}
	emit(llm.BlockEndChunk{Index: block.index, Block: llm.TextBlock{Text: block.text.String()}})
}

// openText 保证当前开着的是指定类型的块，需要时先把上一块收掉。
func (a *blockAssembly) openText(kind llm.BlockType, emit func(llm.StreamChunk)) *openBlock {
	if a.current != nil && a.current.kind == kind {
		return a.current
	}
	a.closeCurrent(emit)
	block := &openBlock{index: a.next, kind: kind}
	a.next++
	a.current = block
	emit(llm.BlockStartChunk{Index: block.index, BlockType: kind})
	return block
}

// closeCalls 按首次出现的次序收掉所有工具调用块。
func (a *blockAssembly) closeCalls(emit func(llm.StreamChunk)) {
	for _, wireIndex := range a.callOrder {
		block := a.calls[wireIndex]
		emit(llm.BlockEndChunk{Index: block.index, Block: llm.ToolCallBlock{
			ID:   llm.CallID(block.callID),
			Name: block.callName,
			// 参数是模型写的那串原文，原样交出去。它随时可能不是合法 JSON，
			// 而那件事该由工具那一侧在解析时报，不该在这里把一次响应读废。
			Arguments: block.text.String(),
		}})
	}
}

// hasContent 判这次响应到底产出了没有。
func (a *blockAssembly) hasContent() bool { return a.next > 0 }

// applyDelta 把一条线上增量摊进装配状态，顺路吐出对应的 harness 分块。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:135-190
func (a *blockAssembly) applyDelta(delta openai.ChatCompletionChunkChoiceDelta, emit func(llm.StreamChunk)) {
	// 推理先于正文：一个模型先想完再答，所以推理字段一来就该开推理块；
	// 正文一来再把它收掉。反过来（正文中途冒出推理）在这条协议上没见过，
	// 但同一段逻辑照样处理得了——[blockAssembly.openText] 只认「类型变了」。
	if raw, present := delta.JSON.ExtraFields[reasoningContentField]; present {
		var text string
		// 解不出字符串就跳过：这是一个协议外的额外字段，各家的形状不保证一致，
		// 拿它把整条流读废不值得。
		//
		// 这里**不能**拿 [respjson.Field.Valid] 当门禁：额外字段没有可对照的类型，
		// openai-go 一律把它们标成 invalid（respjson.go:47-51、apijson 那次收集），
		// 于是 Valid 对每一个额外字段都是 false——用它作门禁，这整段就是死代码，
		// 推理文本会一声不响地整段消失。空的 Raw（字段没出现）和 "null" 都解不成
		// 一个非空字符串，所以下面这一句自己就把它们挡掉了。
		if err := json.Unmarshal([]byte(raw.Raw()), &text); err == nil && text != "" {
			block := a.openText(llm.BlockReasoning, emit)
			block.text.WriteString(text)
			emit(llm.ReasoningDeltaChunk{Index: block.index, Text: text})
		}
	}
	if delta.Content != "" {
		block := a.openText(llm.BlockText, emit)
		block.text.WriteString(delta.Content)
		emit(llm.TextDeltaChunk{Index: block.index, Text: delta.Content})
	}
	if len(delta.ToolCalls) == 0 {
		return
	}
	// 工具调用一开始，文本和推理那一块就到头了：这条协议里 tool_calls 之后
	// 不会再回到 content。
	a.closeCurrent(emit)
	for _, call := range delta.ToolCalls {
		block, known := a.calls[call.Index]
		if !known {
			block = &openBlock{index: a.next, kind: llm.BlockToolCall}
			a.next++
			if a.calls == nil {
				a.calls = make(map[int64]*openBlock)
			}
			a.calls[call.Index] = block
			a.callOrder = append(a.callOrder, call.Index)
			emit(llm.BlockStartChunk{Index: block.index, BlockType: llm.BlockToolCall})
		}
		// id 和 name 通常只在这次调用的第一条增量上出现，之后的增量只带参数片段。
		// 但也有提供方每条都重发一遍，所以这里是「非空就更新」而不是「只认第一次」。
		if call.ID != "" {
			block.callID = call.ID
		}
		if call.Function.Name != "" {
			block.callName = call.Function.Name
		}
		block.text.WriteString(call.Function.Arguments)
		chunk := llm.ToolCallDeltaChunk{
			Index:          block.index,
			ID:             llm.CallID(block.callID),
			ArgumentsDelta: call.Function.Arguments,
		}
		// 工具名只在**这一段确实携带了它**时才写。空串和缺失在这个字段上是两件事，
		// 理由见 [llm.ToolCallDeltaChunk].Name。
		if block.callName != "" {
			name := block.callName
			chunk.Name = &name
		}
		emit(chunk)
	}
}

// streamChunks 把一条 SSE 流翻成 harness 的分块序列。
//
// 源: packages/llm/llm-pi-ai/src/stream.ts:127-211
//
// model 是这次请求点的模型 id，只进诊断文案；contextWindow 是目录里它的上下文容量，
// 为 0 表示不做基于用量的溢出判定。
//
// 交出来的序列一定以 usage 加 finish 收尾——除非底层流自己报错，那时最后一项
// 是那条错误。调用方提前不取了的话，[ssestream.Stream] 由调用方关闭。
func streamChunks(
	stream *ssestream.Stream[openai.ChatCompletionChunk],
	model string,
	contextWindow int,
) iter.Seq2[llm.StreamChunk, error] {
	return func(yield func(llm.StreamChunk, error) bool) {
		var assembly blockAssembly
		var usage llm.TokenUsage
		finishReason := ""
		stopped := false
		emit := func(chunk llm.StreamChunk) {
			if stopped {
				return
			}
			if !yield(chunk, nil) {
				stopped = true
			}
		}

		for stream.Next() {
			chunk := stream.Current()
			// 用量在最后一条分块上，而且只有请求里点了 include_usage 才有。
			// 判 Valid 而不是判非零：一次零 token 的响应和一条没带用量的分块
			// 不是同一件事，后者不该把已经收到的用量清掉。
			if chunk.JSON.Usage.Valid() {
				usage = mapUsage(chunk.Usage)
			}
			// 只读第 0 路。这个适配器从不设 n>1，多出来的路是提供方自己多给的，
			// 把它们的增量掺进同一串块里会拼出一段谁也没说过的话。
			for _, choice := range chunk.Choices {
				if choice.Index != 0 {
					continue
				}
				assembly.applyDelta(choice.Delta, emit)
				if choice.FinishReason != "" {
					finishReason = choice.FinishReason
				}
			}
			if stopped {
				return
			}
		}
		if err := stream.Err(); err != nil {
			// 流在半路断了。已经吐出去的块是真的收到过的，不撤回；这条错误
			// 由调用方归一成失败事实。
			yield(nil, err)
			return
		}

		assembly.closeCurrent(emit)
		assembly.closeCalls(emit)
		if stopped {
			return
		}
		emit(llm.UsageChunk{Usage: usage})
		emit(llm.FinishChunk{
			Reason: mapFinishReason(finishReason, model, assembly.hasContent(), usage, contextWindow),
		})
	}
}
