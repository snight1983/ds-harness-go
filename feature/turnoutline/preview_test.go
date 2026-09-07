// 本文件的作用：预览那段收长度的逻辑——逐块的预算、空白压缩、以及按字算不按字节算。

package turnoutline

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/llm"
)

// 一个超大的文本块只读开头那一段，不整个拼进来。
func TestPreviewReadsOnlyABoundedSliceOfAnOversizedBlock(t *testing.T) {
	got := preview(llm.Content{text("giant " + strings.Repeat("g", 500_000))}, PromptPreviewMaxChars)

	if n := utf8.RuneCountInString(got); n != PromptPreviewMaxChars {
		t.Fatalf("该正好收在 %d 个字，实际 %d：%q", PromptPreviewMaxChars, n, got)
	}
	if !strings.HasPrefix(got, "giant g") || !strings.HasSuffix(got, "…") {
		t.Fatalf("形状不对：%q", got)
	}
}

// 连续空白压成一个空格，超预算的以省略号收尾。
func TestPreviewCollapsesWhitespaceAndCapsWithAnEllipsis(t *testing.T) {
	got := preview(llm.Content{
		text("  spaced\n\nprompt\t" + strings.Repeat("p", 80)),
		text("never reached past the budget"),
	}, PromptPreviewMaxChars)

	if n := utf8.RuneCountInString(got); n != PromptPreviewMaxChars {
		t.Fatalf("该正好收在 %d 个字，实际 %d：%q", PromptPreviewMaxChars, n, got)
	}
	if !strings.HasPrefix(got, "spaced prompt p") || !strings.HasSuffix(got, "…") {
		t.Fatalf("形状不对：%q", got)
	}
}

// 一串又短又空的块会在压缩之前就吃光逐块预算：压出来的那段虽短，
// 也得带上省略号说明后面还有没读的。
func TestPreviewMarksTheUnreadRemainderOfManyAiryBlocks(t *testing.T) {
	blocks := make(llm.Content, 0, 41)
	blocks = append(blocks, llm.ToolCallBlock{})
	for index := range 40 {
		blocks = append(blocks, text("w"+strconv.Itoa(index)+strings.Repeat(" ", 20)))
	}

	got := preview(blocks, ResponsePreviewMaxChars)

	if !strings.HasPrefix(got, "w0 w1 ") {
		t.Fatalf("非文本块该被跳过、文本块该按序接起来，实际 %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("后面还有没读的，该以省略号收尾，实际 %q", got)
	}
	if n := utf8.RuneCountInString(got); n >= ResponsePreviewMaxChars {
		t.Fatalf("压完之后该远短于预算，实际 %d：%q", n, got)
	}
}

// 预算数的是字符不是字节：一行中文按字节收会在第十六个字上就砍断。
func TestPreviewCountsCharactersNotBytes(t *testing.T) {
	got := preview(llm.Content{text(strings.Repeat("字", 60))}, PromptPreviewMaxChars)

	if n := utf8.RuneCountInString(got); n != PromptPreviewMaxChars {
		t.Fatalf("该收在 %d 个**字**，实际 %d 个", PromptPreviewMaxChars, n)
	}
	if len(got) <= PromptPreviewMaxChars {
		t.Fatalf("按字节算的话这段本该更长，%d 个字节说明收错了单位", len(got))
	}
}

// 没有文本块、或者只有空白，预览就是空串。
func TestPreviewOfTextlessContentIsEmpty(t *testing.T) {
	for name, content := range map[string]llm.Content{
		"没有块":  nil,
		"没有文本": {llm.ToolCallBlock{}},
		"只有空白": {text(" \t\n ")},
	} {
		t.Run(name, func(t *testing.T) {
			if got := preview(content, PromptPreviewMaxChars); got != "" {
				t.Fatalf("该是空串，实际 %q", got)
			}
		})
	}
}

// 正好卡在预算上的那段原样交出，不带省略号。
func TestPreviewLeavesTextThatFitsAlone(t *testing.T) {
	fits := strings.Repeat("f", PromptPreviewMaxChars-1)
	if got := preview(llm.Content{text(fits)}, PromptPreviewMaxChars); got != fits {
		t.Fatalf("放得下就该原样交出，实际 %q", got)
	}
}

// firstRunes 按字符切，切不动就说后面没有了。
func TestFirstRunesCutsOnCharacterBoundaries(t *testing.T) {
	for name, each := range map[string]struct {
		input string
		n     int
		want  string
		cut   bool
	}{
		"切在多字节字符中间": {input: "一二三", n: 2, want: "一二", cut: true},
		"正好切完":      {input: "一二三", n: 3, want: "一二三", cut: false},
		"比要的还短":     {input: "一二", n: 3, want: "一二", cut: false},
		"一个都不要":     {input: "一二", n: 0, want: "", cut: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, cut := firstRunes(each.input, each.n)
			if got != each.want || cut != each.cut {
				t.Fatalf("想要 (%q, %v)，实际 (%q, %v)", each.want, each.cut, got, cut)
			}
		})
	}
}
