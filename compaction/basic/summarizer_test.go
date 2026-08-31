// 本文件的作用：验检查点的那层框子摆得对——前言和开标签在同一块里，
// 摘要原样在中间，闭标签单独一块在最后。

package basic

import (
	"strings"
	"testing"

	"ds-harness-go/llm"
)

func TestFrameSummary裹上前言和一对标签(t *testing.T) {
	t.Parallel()

	summary := llm.Content{llm.TextBlock{Text: "第一段"}, llm.TextBlock{Text: "第二段"}}
	framed := FrameSummary(summary)

	if len(framed) != len(summary)+2 {
		t.Fatalf("裹出来 %d 块", len(framed))
	}
	head, ok := framed[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("头一块是 %T", framed[0])
	}
	if !strings.HasPrefix(head.Text, checkpointPreamble) {
		t.Fatalf("头一块是 %q", head.Text)
	}
	// 前言和开标签之间空一行：这三块的边界会原样进模型看到的文本。
	if !strings.HasSuffix(head.Text, "\n\n"+summaryOpenTag) {
		t.Fatalf("头一块是 %q", head.Text)
	}
	if framed[1] != summary[0] || framed[2] != summary[1] {
		t.Fatalf("中间那两块是 %+v", framed[1:3])
	}
	tail, ok := framed[len(framed)-1].(llm.TextBlock)
	if !ok || tail.Text != summaryCloseTag {
		t.Fatalf("最后一块是 %+v", framed[len(framed)-1])
	}
}

func TestFrameSummary摘要是空的也把框子摆全(t *testing.T) {
	t.Parallel()

	// 空框子仍然是一条合法的检查点消息：下一次总结靠那对标签认出
	// 「上面那段是一份更早的检查点」，少了它就认不出来。
	framed := FrameSummary(nil)
	if len(framed) != 2 {
		t.Fatalf("裹出来 %d 块", len(framed))
	}
}

func TestFrameSummary不改原来那份摘要(t *testing.T) {
	t.Parallel()

	// 原来那份摘要还要原样落进 compaction/summary 事件，被就地改掉的话，
	// 日志里记着的就不是模型实际写出来的东西了。
	summary := llm.Content{llm.TextBlock{Text: "第一段"}}
	FrameSummary(summary)
	if len(summary) != 1 || summary[0] != (llm.TextBlock{Text: "第一段"}) {
		t.Fatalf("原来那份变成了 %+v", summary)
	}
}
