// 本文件的作用：回放这几组测试共用的东西——照录出来的样子拼一份 session.jsonl、
// 一台空运行时加一个用完就释放的作用域，以及把一条流抽干的那两个小工具。
//
// 夹具是**按字节**拼出来的而不是拿结构体排出来的：本包读的是一道持久边界，
// 测试该盯着那道边界上真正躺着的字节，而不是盯着自己刚排出去的那份结构。

package replay

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
)

// textChunks 是一次正常吐完的调用，五块，最后一块是 finish。
func textChunks() []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: 0, Text: "hi"},
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "hi"}},
		llm.UsageChunk{Usage: llm.TokenUsage{InputTokens: 1, OutputTokens: 1}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
}

// shortChunks 是另一次正常吐完的调用，拿来和 [textChunks] 区分「第几次」。
func shortChunks(text string) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: 0, Text: text},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
}

// mustJSON 把一个值排成一行 JSON，排不出去当场把这条用例判死。
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("排 JSON 失败：%v", err)
	}
	return string(data)
}

// headerLine 拼一行会话头；seedLength 为 0 时那个键根本不出现。
func headerLine(t *testing.T, id string, createdAt int64, seedLength int) string {
	t.Helper()
	fields := map[string]any{"type": "session", "version": 0, "id": id, "createdAt": createdAt}
	if seedLength != 0 {
		fields["seedLength"] = seedLength
	}
	return mustJSON(t, fields)
}

// chunkLine 拼一行 assistant/chunk 事件。
func chunkLine(t *testing.T, seq, turn, step int, chunk llm.StreamChunk) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"type": "assistant/chunk",
		"seq":  seq,
		"time": 0,
		"data": map[string]any{"turn": turn, "step": step, "chunk": chunk},
	})
}

// sessionJSONL 把一行头和若干行正文拼成一份夹具。
func sessionJSONL(header string, lines ...string) string {
	return strings.Join(append([]string{header}, lines...), "\n") + "\n"
}

// callsJSONL 把若干次调用拼成一份夹具：第 n 次调用落在 (1, n+1) 上。
func callsJSONL(t *testing.T, id string, createdAt int64, calls ...[]llm.StreamChunk) string {
	t.Helper()
	var lines []string
	seq := 1
	for step, chunks := range calls {
		for _, chunk := range chunks {
			lines = append(lines, chunkLine(t, seq, 1, step+1, chunk))
			seq++
		}
	}
	return sessionJSONL(headerLine(t, id, createdAt, 0), lines...)
}

// writeFile 把一份文本落到给定路径。
func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写夹具 %s 失败：%v", path, err)
	}
	return path
}

// fixtureDir 交回这条用例自己的临时目录。
func fixtureDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeCalls 把若干次调用落成一份夹具，交回它的路径。
func writeCalls(t *testing.T, dir, name, id string, createdAt int64, calls ...[]llm.StreamChunk) string {
	t.Helper()
	return writeFile(t, filepath.Join(dir, name), callsJSONL(t, id, createdAt, calls...))
}

// testScope 造一个用完自动释放的作用域。
func testScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// drain 把一条流抽干，交回那些分块和终止时的错误。
func drain(sequence iter.Seq2[llm.StreamChunk, error]) ([]llm.StreamChunk, error) {
	var chunks []llm.StreamChunk
	for chunk, err := range sequence {
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// streamContext 走一遍真的运行时瀑布，把那次调用的分块和错误交回来。
//
// 两条装法在「失败从哪儿出来」上不一样，所以这里两处都收：兜底监听器那条路
// （没配提供方目录）的失败从 [github.com/snight1983/ds-harness-go/llm.Runtime.Stream] 的第二个返回值出来，
// 适配器那条路的失败被运行时归一成一个终止分块。
func streamContext(
	ctx context.Context,
	runtime *llm.Runtime,
	options llm.GenerateOptions,
) ([]llm.StreamChunk, error) {
	sequence, err := runtime.Stream(ctx, options)
	if err != nil {
		return nil, err
	}
	return drain(sequence)
}

// streamOnce 是不带取消的 [streamContext]。
func streamOnce(runtime *llm.Runtime, options llm.GenerateOptions) ([]llm.StreamChunk, error) {
	return streamContext(context.Background(), runtime, options)
}

// anonymousOptions 拼一份不带会话 id 的请求。
func anonymousOptions() llm.GenerateOptions {
	return llm.GenerateOptions{Provider: "m", Model: "m"}
}

// liveOptions 拼一份带会话 id 的请求。
func liveOptions(id llm.SessionID) llm.GenerateOptions {
	return llm.GenerateOptions{Provider: "m", Model: "m", SessionID: id}
}

// installReplay 把回放装到一台新运行时上，交回那台运行时和句柄。
func installReplay(t *testing.T, config Config) (*llm.Runtime, *Handle) {
	t.Helper()
	runtime := llm.NewRuntime(llm.RuntimeOptions{})
	handle, err := Install(context.Background(), testScope(t), runtime, config)
	if err != nil {
		t.Fatalf("装回放失败：%v", err)
	}
	return runtime, handle
}
