// 本文件的作用：拿任意字节当 SSE 流喂进块装配那一层。
//
// 为什么这一层要 fuzz：线上那串增量是**模型提供方**发来的，不是本仓库能约束的。
// 各家 OpenAI 兼容端点在字段上各有各的走样，而这一层要从那串扁平增量里推出
// 块的边界——块的开合是靠状态机推出来的，不是线上直接给的。一条错位的
// block/end、一个开了没关的块，会让上面那层把两次响应的文本拼在一起。
// 用例只能举出见过的走样，没见过的那些正是要防的。

package openaicompat

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/snight1983/ds-harness-go/llm"
)

func FuzzStreamChunks(f *testing.F) {
	f.Add("")
	f.Add("data: [DONE]\n\n")
	f.Add(`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n")
	f.Add(`data: {"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":"stop"}]}` + "\n\n")
	f.Add(`data: {"choices":[{"index":0,"delta":{"reasoning_content":"想"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"content":"答"}}]}` + "\n\n")
	f.Add(`data: {"choices":[{"index":0,"delta":{"tool_calls":[` +
		`{"index":0,"id":"c1","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]}` + "\n\n")
	// 只有第 1 路：这个适配器从不设 n>1，多出来的路必须被整条忽略。
	f.Add(`data: {"choices":[{"index":1,"delta":{"content":"别人的话"}}]}` + "\n\n")
	f.Add(`data: {"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}` + "\n\n")
	f.Add("data: 不是 json\n\n")
	f.Add("data: {\n\n")
	f.Add("garbage without any framing")

	f.Fuzz(func(t *testing.T, body string) {
		response := &http.Response{
			Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:   io.NopCloser(strings.NewReader(body)),
		}
		stream := ssestream.NewStream[openai.ChatCompletionChunk](ssestream.NewDecoder(response), nil)

		open := map[int]llm.BlockType{}
		var chunks []llm.StreamChunk
		var failure error
		for chunk, err := range streamChunks(stream, "fuzz-model", 0) {
			if err != nil {
				failure = err
				break
			}
			chunks = append(chunks, chunk)

			switch typed := chunk.(type) {
			case llm.BlockStartChunk:
				// 同一个下标开两次，上面那层就有两个块共用一个身份了。
				if _, already := open[typed.Index]; already {
					t.Fatalf("下标 %d 被开了两次", typed.Index)
				}
				open[typed.Index] = typed.BlockType
			case llm.BlockEndChunk:
				kind, isOpen := open[typed.Index]
				if !isOpen {
					t.Fatalf("下标 %d 没开过就收了", typed.Index)
				}
				// 收的那一块必须和开的时候说的是同一类：不一致的话，一段推理
				// 会被当成正文交上去。
				if got := typed.Block.BlockType(); got != kind {
					t.Fatalf("下标 %d 开的时候是 %v，收的时候是 %v", typed.Index, kind, got)
				}
				delete(open, typed.Index)
			case llm.TextDeltaChunk:
				if kind := open[typed.Index]; kind != llm.BlockText {
					t.Fatalf("下标 %d 上来了一条正文增量，但那一块是 %v", typed.Index, kind)
				}
			case llm.ReasoningDeltaChunk:
				if kind := open[typed.Index]; kind != llm.BlockReasoning {
					t.Fatalf("下标 %d 上来了一条推理增量，但那一块是 %v", typed.Index, kind)
				}
			}
		}

		if failure != nil {
			// 流在半路断了。已经吐出去的块是真收到过的，不撤回，所以这时候
			// 允许有块还开着——收尾那两下根本没跑到。
			return
		}
		// 正常收完：一个块都不许还开着。开着的块在上面那层是一段永远不结束的文本。
		if len(open) != 0 {
			t.Fatalf("流正常结束了，但还有 %d 块开着：%v", len(open), open)
		}
		// 正常收完一定以 usage 加 finish 收尾，那是这个迭代器对调用方的承诺。
		if len(chunks) < 2 {
			t.Fatalf("正常结束该至少有 usage 和 finish 两条，实际 %d 条", len(chunks))
		}
		if _, ok := chunks[len(chunks)-2].(llm.UsageChunk); !ok {
			t.Fatalf("倒数第二条该是用量，实际 %T", chunks[len(chunks)-2])
		}
		if _, ok := chunks[len(chunks)-1].(llm.FinishChunk); !ok {
			t.Fatalf("最后一条该是终止，实际 %T", chunks[len(chunks)-1])
		}
	})
}
