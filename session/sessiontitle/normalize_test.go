// 本文件的作用：洗白与截断那几个纯函数的测试。这里的重点不是「常见输入洗完好看」，
// 而是那些**恶意或者畸形**的输入洗完不留下任何能骗人的东西。

package sessiontitle

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 这几个不可见字符一律绕一道 rune 转换造出来，不直接打进字符串字面量。
//
// 两条理由：U+FEFF 只许出现在源文件的第一个字符，写在别处 Go 编译器会当场报
// illegal byte order mark；而 C1 那几个（U+0080-U+009F）以及零宽、方向控制字符
// 直接躺在源码里，任何一次经手这份文件的编辑器、补丁工具或者剪贴板都可能悄悄
// 把它们吃掉——那样测试会照常通过，只是它已经不再测原来那件事了。
var (
	bom        = string(rune(0xfeff)) // 字节序标记
	csi8       = string(rune(0x9b))   // 八位形式的 CSI
	osc8       = string(rune(0x9d))   // 八位形式的 OSC
	nel        = string(rune(0x85))   // NEL：是空白，但落在控制字符区间里
	c1Control  = string(rune(0x86))   // 一个没有别的含义的 C1 控制符
	rtlCover   = string(rune(0x202e)) // 从右往左覆盖
	zeroWidth  = string(rune(0x200b)) // 零宽空格
	fullSpace  = string(rune(0x3000)) // 全角空格
	noBreakSep = string(rune(0xa0))   // 不换行空格
)

func TestNormalizeSessionTitleStripsTerminalEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"SGR 配色", "\x1b[31m红色标题\x1b[0m", "红色标题"},
		{"八位 CSI", csi8 + "31m八位", "八位"},
		{"OSC 改窗口标题", "\x1b]0;被劫持的标题\x07正文", "正文"},
		{"八位 OSC", osc8 + "0;劫持\x1b\\正文", "正文"},
		{"没有终止符的 OSC", "正文前\x1b]0;吃到结尾", "正文前"},
		{"两字符 ESC", "\x1bM前进", "前进"},
		{"裸控制字符", "标\x00题\x07", "标题"},
		{"C1 控制字符", "标" + c1Control + "题", "标题"},
		{"从右往左覆盖", "report" + rtlCover + "gnp.txt", "reportgnp.txt"},
		{"零宽空格", "标" + zeroWidth + "题", "标题"},
		{"字节序标记", bom + "标题", "标题"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeSessionTitle(test.input, 100); got != test.want {
				t.Fatalf("洗完是 %q，要的是 %q", got, test.want)
			}
		})
	}
}

// U+0085（NEL）落在 controlCharacter 的 \x7f-\x9f 区间里，会先被**删掉**，
// 于是两边的字符粘在一起，而不是折成一个空格。这条测试把那个顺序钉住——
// 它正是 whitespaceRun 那份手写字符集里没有 U+0085 的理由。
func TestNormalizeSessionTitleNelIsDeletedNotFolded(t *testing.T) {
	if got := NormalizeSessionTitle("ab"+nel+"cd", 100); got != "abcd" {
		t.Fatalf("洗完是 %q，要的是 %q", got, "abcd")
	}
}

func TestNormalizeSessionTitleFoldsUnicodeWhitespace(t *testing.T) {
	// U+3000 是中文输入法打出来的全角空格，U+00A0 是从网页复制粘贴常带的不换行
	// 空格。Go 的 \s 两个都不认，所以这条测试盯的就是那份手写字符集。
	input := "一" + fullSpace + "二" + noBreakSep + "三 四\t\t五"
	want := "一 二 三 四 五"
	if got := NormalizeSessionTitle(input, 100); got != want {
		t.Fatalf("洗完是 %q，要的是 %q", got, want)
	}
}

func TestNormalizeSessionTitleTrimsAndCollapses(t *testing.T) {
	if got := NormalizeSessionTitle("   前   后   ", 100); got != "前 后" {
		t.Fatalf("洗完是 %q，要的是 %q", got, "前 后")
	}
}

func TestNormalizeSessionTitleEmptyWhenNothingSurvives(t *testing.T) {
	for _, input := range []string{"", "   ", "\x1b[31m\x1b[0m", zeroWidth + zeroWidth, "\x1b]0;只有一段被截断的 OSC"} {
		if got := NormalizeSessionTitle(input, 100); got != "" {
			t.Fatalf("输入 %q 洗完是 %q，要的是空串", input, got)
		}
	}
}

func TestNormalizeSessionTitleTrimsTrailingSpaceAfterTruncation(t *testing.T) {
	// 截断正好切在一个空格后面时，那个空格不许留下——一个以空格结尾的标题在
	// 列表里看不出来，但它在比较和搜索时是另一个字符串。
	if got := NormalizeSessionTitle("ab cd", 3); got != "ab" {
		t.Fatalf("洗完是 %q，要的是 %q", got, "ab")
	}
}

func TestTruncateTitleUtf8NeverSplitsACodePoint(t *testing.T) {
	// 「中」是三个字节。任何一个预算下截出来的东西都必须是合法 UTF-8。
	input := "中文标题"
	for budget := 1; budget <= len(input)+2; budget++ {
		got := TruncateTitleUtf8(input, budget)
		if !utf8.ValidString(got) {
			t.Fatalf("预算 %d 截出了非法 UTF-8：%q", budget, got)
		}
		if len(got) > budget {
			t.Fatalf("预算 %d 截出了 %d 个字节：%q", budget, len(got), got)
		}
		if !strings.HasPrefix(input, got) {
			t.Fatalf("预算 %d 截出的 %q 不是原文的前缀", budget, got)
		}
	}
}

func TestTruncateTitleUtf8NoCapWhenBudgetNotPositive(t *testing.T) {
	input := "一段不该被截的长标题"
	for _, budget := range []int{0, -1} {
		if got := TruncateTitleUtf8(input, budget); got != input {
			t.Fatalf("预算 %d 下截成了 %q，要的是原样交回", budget, got)
		}
	}
}

func TestTruncateTitleUtf8KeepsExactFit(t *testing.T) {
	input := "中"
	if got := TruncateTitleUtf8(input, len(input)); got != input {
		t.Fatalf("正好放得下却截成了 %q", got)
	}
}

func TestFallbackSessionTitleTakesLeadingWords(t *testing.T) {
	got := FallbackSessionTitle("one two three four five six", 3, 100)
	if got != "one two three" {
		t.Fatalf("兜底是 %q，要的是 %q", got, "one two three")
	}
}

func TestFallbackSessionTitleAppliesBothGates(t *testing.T) {
	// 词数那道过得去，字节数那道过不去：两道闸门都要生效。
	got := FallbackSessionTitle("aaaa bbbb cccc", 3, 9)
	if got != "aaaa bbbb" {
		t.Fatalf("兜底是 %q，要的是 %q", got, "aaaa bbbb")
	}
}

func TestFallbackSessionTitleChineseHasNoWordBoundaries(t *testing.T) {
	// 中文没有词间空格，洗完就是一整个「词」，所以词数那道闸门形同虚设，真正
	// 起作用的只有字节数。九个字节正好是三个汉字。
	got := FallbackSessionTitle("帮我把这段代码改成并发的", 3, 9)
	if got != "帮我把" {
		t.Fatalf("兜底是 %q，要的是 %q", got, "帮我把")
	}
}

func TestFallbackSessionTitleEmptyWhenNothingSurvives(t *testing.T) {
	for _, input := range []string{"", "   ", "\x1b[0m"} {
		if got := FallbackSessionTitle(input, 5, 40); got != "" {
			t.Fatalf("输入 %q 的兜底是 %q，要的是空串", input, got)
		}
	}
}

func TestFallbackSessionTitleStripsEscapesBeforeCounting(t *testing.T) {
	// 转义序列不许占掉词数预算：先剥再数。
	got := FallbackSessionTitle("\x1b[31mone\x1b[0m two three four", 3, 100)
	if got != "one two three" {
		t.Fatalf("兜底是 %q，要的是 %q", got, "one two three")
	}
}
