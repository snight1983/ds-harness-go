// 本文件的作用：线路上那些 SSE 事件的形状。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:367-372,434-459
//
// 新增: TS 那边这些形状是就地写出来的对象字面量。Go 换成具名结构体，图的是
// **字段顺序**：被测的那一侧多半在按字符串找 "content":"..." 这样的片段，而
// [encoding/json] 对 map 会按键排序、对结构体则照声明序写。用 map 的话
// `{"index":0,"delta":...,"finish_reason":null}` 会被重排成 delta 在前，
// 和真供应商发出来的样子对不上。

package mockserver

// sseChunk 是一个 data: 行里的顶层对象。
type sseChunk struct {
	Choices []sseChoice `json:"choices"`
	Usage   *sseUsage   `json:"usage,omitempty"`
}

// sseChoice 是一路候选的增量。
//
// FinishReason 是指针：没结束时要在线路上写出字面的 null，而不是把这个键省掉——
// 真供应商每一帧都带着它，省掉会让按存在性判断的解析器提前以为流结束了。
type sseChoice struct {
	Index        int     `json:"index"`
	Delta        any     `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// malformedChunk 是 [BehaviorMalformedEvent] 那一帧：JSON 合法，形状不合约定。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:522
//
// 新增: TS 写的是 `{ choices: [null] }`，而 [sseChunk] 的 Choices 是值切片，装不下
// 一个 null。这里另开一个指针切片的形状，好让线路上真的出现 `{"choices":[null]}`——
// 这一帧要考的正是「解析器拿到合法 JSON 但取不到 choices[0].delta 时会怎样」，
// 换成空数组就把题目改掉了。
type malformedChunk struct {
	Choices []*sseChoice `json:"choices"`
}

// sseUsage 是收尾帧里的用量。
type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// contentDelta 是一段正文增量。Content 不带 omitempty：收尾帧发的正是空串。
type contentDelta struct {
	Content string `json:"content"`
}

// roleDelta 是只宣告角色、不带内容的那一帧。
type roleDelta struct {
	Role string `json:"role"`
}

// reasoningDelta 是一段思考内容增量。
type reasoningDelta struct {
	ReasoningContent string `json:"reasoning_content"`
}

// toolCallsDelta 是一帧工具调用增量。
type toolCallsDelta struct {
	ToolCalls []toolCallDelta `json:"tool_calls"`
}

// toolCallDelta 是其中一路工具调用。
//
// ID／Type／Name 只在第一帧出现，续帧靠 Index 认领同一次调用——这正是流式工具
// 调用在线路上的样子，被测那一侧要能把两帧的参数拼回去。
type toolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function toolFunctionDelta `json:"function"`
}

// toolFunctionDelta 是工具调用里的函数名与参数片段。
type toolFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// terminalChunk 造一帧收尾事件：空正文增量、收尾理由、以及用量。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:367-372
func terminalChunk(reason string, outputTokens int) sseChunk {
	return sseChunk{
		Choices: []sseChoice{{
			Index:        0,
			Delta:        contentDelta{Content: ""},
			FinishReason: &reason,
		}},
		Usage: &sseUsage{PromptTokens: 3, CompletionTokens: outputTokens},
	}
}

// toolCallChunks 把一次工具调用的参数**劈成两帧**发。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:434-459
//
// 劈开是重点：真供应商的工具参数就是一片一片来的，一帧发完的话被测那一侧的
// 拼接逻辑根本不会被走到。劈点取参数长度的一半，至少 1，这样短到一两个字符的
// 参数也仍然是两帧。
//
// 劈点不会越界：toolArguments 是合法 JSON，而空串不是合法 JSON，所以它至少一个
// 字节，max(1, n/2) 在 n >= 1 时必定不超过 n。
func toolCallChunks(options resolvedOptions) []sseChunk {
	midpoint := max(1, len(options.toolArguments)/2)
	return []sseChunk{
		{Choices: []sseChoice{{
			Index: 0,
			Delta: toolCallsDelta{ToolCalls: []toolCallDelta{{
				Index:    0,
				ID:       "mock-call-1",
				Type:     "function",
				Function: toolFunctionDelta{Name: options.toolName, Arguments: options.toolArguments[:midpoint]},
			}}},
			FinishReason: nil,
		}}},
		{Choices: []sseChoice{{
			Index: 0,
			Delta: toolCallsDelta{ToolCalls: []toolCallDelta{{
				Index:    0,
				Function: toolFunctionDelta{Arguments: options.toolArguments[midpoint:]},
			}}},
			FinishReason: nil,
		}}},
	}
}

// splitText 按码点把文本切成每片至多 size 个码点。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:306-311
//
// 新增: TS 用 Array.from 拿码点（而不是 UTF-16 码元），Go 用 []rune，两边都是
// 码点。按字节切会把一个多字节字符劈成两半，那样发出去的增量根本不是合法 UTF-8。
func splitText(text string, size int) []string {
	points := []rune(text)
	chunks := make([]string, 0, (len(points)+size-1)/size)
	for index := 0; index < len(points); index += size {
		end := min(index+size, len(points))
		chunks = append(chunks, string(points[index:end]))
	}
	return chunks
}
