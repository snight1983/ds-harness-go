// 本文件的作用：验砍的那一刀落在码点边界上、非文本的块原样留着顺序不动，
// 以及在预算之内的内容一个字都不动。

package toolresultpruner

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"ds-harness-go/llm"
)

// smallPruner 造一个预算很小的 [Pruner]，让用例不用堆几千个字符。
func smallPruner(t *testing.T) *Pruner {
	t.Helper()

	pruner, err := New(Config{ThresholdChars: 50, HeadChars: intOf(4), TailChars: intOf(3)})
	if err != nil {
		t.Fatalf("造不出来：%v", err)
	}
	return pruner
}

func TestNew配置验不过就造不出来(t *testing.T) {
	t.Parallel()

	pruner, err := New(Config{ThresholdChars: -1})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("报的是 %v", err)
	}
	if pruner != nil {
		t.Fatal("验不过还是造出来了")
	}
}

func TestConfig交出验过的那份预算(t *testing.T) {
	t.Parallel()

	got := smallPruner(t).Config()
	if got != (ResolvedConfig{ThresholdChars: 50, HeadChars: 4, TailChars: 3}) {
		t.Fatalf("交出来的是 %+v", got)
	}
}

func TestMeasureContent只数文本块的码点(t *testing.T) {
	t.Parallel()

	// 一个 emoji 是一个码点。按 UTF-16 码元数会数成两个，按字节数会数成四个，
	// 两种都会让砍的那一刀落错地方。
	got := smallPruner(t).MeasureContent(llm.Content{
		llm.TextBlock{Text: "a😀b"},
		llm.ReasoningBlock{Text: "这一块不算"},
		llm.ToolResultBlock{Content: llm.Content{llm.TextBlock{Text: "嵌进去的也不算"}}},
	})
	if got != 3 {
		t.Fatalf("数出来 %d 个码点", got)
	}
}

func TestPruneContent在预算之内就不动(t *testing.T) {
	t.Parallel()

	blocks := llm.Content{llm.TextBlock{Text: strings.Repeat("a", 50)}}
	got, pruned, err := smallPruner(t).PruneContent(blocks)
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	// 「没砍」和「砍出了一条空正文」是两件完全不同的事，靠这个布尔分开。
	if pruned || got != nil {
		t.Fatalf("砍了：%+v（有没有砍：%v）", got, pruned)
	}
}

func TestPruneContent留头留尾且不劈开一个字符(t *testing.T) {
	t.Parallel()

	got, pruned, err := smallPruner(t).PruneContent(
		llm.Content{llm.TextBlock{Text: strings.Repeat("😀", 60)}})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !pruned || len(got) != 1 {
		t.Fatalf("砍出来的是 %+v", got)
	}
	text, ok := got[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("砍出来的是 %T", got[0])
	}
	want := strings.Repeat("😀", 4) + PruneMarker + strings.Repeat("😀", 3)
	if text.Text != want {
		t.Fatalf("砍出来的是 %q", text.Text)
	}
	// 切在码点边界上，所以不会砍出替换字符。
	if strings.ContainsRune(text.Text, utf8.RuneError) {
		t.Fatalf("砍出乱码了：%q", text.Text)
	}
}

func TestPruneContent非文本的块原样留着顺序不动(t *testing.T) {
	t.Parallel()

	reasoning := llm.ReasoningBlock{Text: "不该被砍的富块"}
	call := llm.ToolCallBlock{ID: "nested", Name: "nested", Arguments: "{}"}
	got, pruned, err := smallPruner(t).PruneContent(llm.Content{
		llm.TextBlock{Text: strings.Repeat("A", 40)},
		reasoning,
		llm.TextBlock{Text: strings.Repeat("B", 30)},
		call,
		llm.TextBlock{Text: strings.Repeat("C", 30)},
	})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !pruned {
		t.Fatal("没砍")
	}
	// 中间那块 B 整个落在被砍的区间里，于是它空了、不留；标记只插一次，
	// 插在第一块和被砍区间相交的地方。
	want := llm.Content{
		llm.TextBlock{Text: "AAAA" + PruneMarker},
		reasoning,
		call,
		llm.TextBlock{Text: "CCC"},
	}
	if len(got) != len(want) {
		t.Fatalf("砍出来 %d 块：%+v", len(got), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 块是 %+v，要的是 %+v", index, got[index], want[index])
		}
	}
}

func TestPruneContent头尾都不留也砍得下去(t *testing.T) {
	t.Parallel()

	marker := utf8.RuneCountInString(PruneMarker)
	pruner, err := New(Config{ThresholdChars: marker, HeadChars: intOf(0), TailChars: intOf(0)})
	if err != nil {
		t.Fatalf("造不出来：%v", err)
	}
	got, pruned, err := pruner.PruneContent(
		llm.Content{llm.TextBlock{Text: strings.Repeat("x", 100)}})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !pruned || len(got) != 1 || got[0] != (llm.TextBlock{Text: PruneMarker}) {
		t.Fatalf("砍出来的是 %+v", got)
	}
	if pruner.MeasureContent(got) != marker {
		t.Fatalf("砍完是 %d 个码点", pruner.MeasureContent(got))
	}
}

func TestPruneContent砍完永远压在线下(t *testing.T) {
	t.Parallel()

	// 砍完还在线上的话，下一趟又会来砍同一条结果，而它已经砍无可砍了。
	// 这一条是配置校验在守的，这里从外面再确认一遍。
	pruner := smallPruner(t)
	for name, blocks := range map[string]llm.Content{
		"一整块":     {llm.TextBlock{Text: strings.Repeat("a", 500)}},
		"很多小块":    make(llm.Content, 0),
		"文本夹着非文本": {llm.TextBlock{Text: strings.Repeat("a", 100)}, llm.ReasoningBlock{Text: "富块"}, llm.TextBlock{Text: strings.Repeat("b", 100)}},
	} {
		if name == "很多小块" {
			for range 100 {
				blocks = append(blocks, llm.TextBlock{Text: "abcde"})
			}
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := pruner.MeasureContent(blocks)
			got, pruned, err := pruner.PruneContent(blocks)
			if err != nil {
				t.Fatalf("报了：%v", err)
			}
			if !pruned {
				t.Fatal("没砍")
			}
			after := pruner.MeasureContent(got)
			if after > pruner.Config().ThresholdChars || after >= before {
				t.Fatalf("砍完是 %d 个码点，砍之前是 %d", after, before)
			}
		})
	}
}

func TestPruneContent不改原来那份内容(t *testing.T) {
	t.Parallel()

	// 原件还要留在日志里当被遮的那条，被就地改掉的话，重放就恢复不出砍的输入了。
	original := llm.TextBlock{Text: strings.Repeat("a", 100)}
	blocks := llm.Content{original}
	if _, _, err := smallPruner(t).PruneContent(blocks); err != nil {
		t.Fatalf("报了：%v", err)
	}
	if len(blocks) != 1 || blocks[0] != original {
		t.Fatalf("原来那份变成了 %+v", blocks)
	}
}
