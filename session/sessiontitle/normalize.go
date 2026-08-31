// 本文件的作用：把一段**不可信**的文本洗成一行能安全显示在终端和列表里的标题，
// 并把它压进一个 UTF-8 字节预算。
//
// 为什么要洗：标题的来源有三条，其中两条不受本进程控制——模型生成的那一条，
// 和用户改名传进来的那一条。一段带 ANSI 转义的文本打到终端上能改颜色、能移动
// 光标、能改窗口标题；一段带 U+202E（从右往左覆盖）的文本能让「report.exe」
// 在列表里显示成「report.txe」。这些字符在标题这个用途上没有任何正当意义，
// 所以一律剥掉，而不是转义或者拒绝。
//
// 为什么按字节数截：标题要落进事件日志、要走 JSON、要进数据库列，这些地方的
// 上限都是字节数不是字符数。按字符截会让一个纯中文标题占掉三倍预算。
//
// 源: packages/session/session-title/src/normalize.ts

package sessiontitle

import (
	"regexp"
	"strings"
)

// 下面五条正则按顺序剥掉五类字符。它们必须按这个顺序跑：OSC 和 CSI 的**内部**
// 就含有落在控制字符区间里的字节，先跑控制字符那条会把转义序列拆成碎片，
// 剩下的方括号和数字会原样留在标题里。
var (
	// oscSequence 是操作系统命令序列，含没有正常终止的那一截。
	//
	// 源: packages/session/session-title/src/normalize.ts:4
	//
	// 新增: DSH 那条写的是 `(?:(?!|\\)[\s\S])*`——一个带否定先行断言的
	// 贪婪星号，意思是「一直吃到第一个终止符前面」。Go 的 regexp 是 RE2，
	// **没有先行断言**。这里换成非贪婪的 `.*?` 加上终止符的选择分支：非贪婪同样
	// 停在第一个能让后面那组匹配上的位置，两者在这个模式上逐字等价。
	//
	// `$` 那一支是有意的：一段被截断的、没有终止符的 OSC 序列（比如日志里只抄到
	// 一半的那种）照样要整段剥掉，而不是把它的载荷当成标题文本留下来。
	//
	// 模式里的码点一律写成 `\x{...}`：`\x{9d}` 这样的 C1 控制符单独一个字节
	// 不是合法 UTF-8，直接写 Go 字符串转义会得到一个编译得过但匹配不上的模式。
	oscSequence = regexp.MustCompile(`(?s)(?:\x{1b}\]|\x{9d}).*?(?:\x{7}|\x{1b}\\|$)`)

	// csiSequence 是控制序列引导符，SGR 配色那些就走这条。
	//
	// 源: packages/session/session-title/src/normalize.ts:6
	csiSequence = regexp.MustCompile(`(?:\x{1b}\[|\x{9b})[0-?]*[ -/]*[@-~]`)

	// escSequence 是剩下那些两字符的 ESC 控制序列。
	//
	// 源: packages/session/session-title/src/normalize.ts:8
	escSequence = regexp.MustCompile(`\x{1b}[@-_]`)

	// controlCharacter 是不算空白的 C0/C1 控制字符。
	//
	// 源: packages/session/session-title/src/normalize.ts:10
	//
	// 区间里有意留出了 \x09（制表）、\x0a（换行）、\x0d（回车）：它们是空白，
	// 交给下面那次空白折叠去变成一个空格，而不是在这里删掉——删掉会让
	// 「第一行\n第二行」粘成「第一行第二行」。
	controlCharacter = regexp.MustCompile(`[\x{0}-\x{8}\x{b}\x{c}\x{e}-\x{1f}\x{7f}-\x{9f}]`)

	// directionalControl 是方向控制和零宽字符——它们能让显示出来的标题骗人。
	//
	// 源: packages/session/session-title/src/normalize.ts:12
	directionalControl = regexp.MustCompile(
		`[\x{200b}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2060}-\x{2064}\x{2066}-\x{206f}\x{feff}]`)

	// whitespaceRun 是要被折成一个空格的空白串。
	//
	// 新增: DSH 那边写的是 `/\s+/gu`。Go 的 `\s` 只认 ASCII 的那五个
	// （`[\t\n\f\r ]`），而 JS 带 u 标志的 `\s` 是整套 Unicode 空白。差别不是
	// 学术问题：U+3000（表意空格）是中文输入法打出来的全角空格，U+00A0
	// （不换行空格）常常从网页复制粘贴进来，两者在 Go 的 `\s` 下都不算空白，
	// 会原样留在标题里、并且各自占掉 3 个和 2 个字节的预算。
	//
	// 所以这里把 JS 那套字符集**逐个写出来**。注意没有 U+0085（NEL）：
	// JS 的 `\s` 不含它，而且它落在上面 controlCharacter 的 \x7f-\x9f 区间里，
	// 走到这一步时已经被删掉了。
	whitespaceRun = regexp.MustCompile(
		`[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
)

// cleanTitleText 剥掉控制字符，交出一行折叠过、两头修剪过的文本。
//
// 源: packages/session/session-title/src/normalize.ts:22-31
//
// 它可能返回空串：一段全是转义序列的输入洗完什么都不剩。调用方必须自己处理
// 这种情况，而不是把空串当成标题写进日志。
func cleanTitleText(input string) string {
	cleaned := oscSequence.ReplaceAllString(input, "")
	cleaned = csiSequence.ReplaceAllString(cleaned, "")
	cleaned = escSequence.ReplaceAllString(cleaned, "")
	cleaned = controlCharacter.ReplaceAllString(cleaned, "")
	cleaned = directionalControl.ReplaceAllString(cleaned, "")
	cleaned = whitespaceRun.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

// trimTrailingSpace 去掉尾部的空格。
//
// 新增: DSH 那边是 `trimEnd()`，修剪的是整套 JS 空白。这里只修剪 U+0020 就够了，
// 因为走到这一步的字符串一定是 [cleanTitleText] 的输出的一段前缀，而那个函数
// 已经把所有空白折成了 U+0020。写成 TrimRight(s, " ") 而不是 TrimRightFunc，
// 是想让「这里只可能有普通空格」这个前提留在代码里。
func trimTrailingSpace(text string) string {
	return strings.TrimRight(text, " ")
}

// TruncateTitleUtf8 把一段文本截进一个 UTF-8 字节预算，且不把任何一个码点劈开。
//
// 源: packages/session/session-title/src/normalize.ts:39-51
//
// 新增: DSH 那边 maxBytes 不是正整数就抛。Go 这边约定 **maxBytes ≤ 0 表示不设上限**，
// 于是这个函数不返回错误。这么改有两个理由：一是 DSH 自己在
// [collectMessages] 那处就拿 Number.MAX_SAFE_INTEGER 当「不设上限」用，说明
// 「不截」是一个真实存在的用法，只是没给它一个名字；二是上限的合法性检查在
// [Config.Validate] 里已经做了一遍（和 DSH 一样），叶子函数再抛一次只会把
// 错误处理摊到每一个调用点上，而那些调用点全都拿的是同一份验过的配置。
func TruncateTitleUtf8(input string, maxBytes int) string {
	if maxBytes <= 0 || len(input) <= maxBytes {
		return input
	}
	// for range 交出来的是每个码点**起始**的字节偏移，所以 input[:cut] 永远切在
	// 码点边界上。要找的是不超过预算的那个最大边界；len(input) 本身也是一个边界，
	// 但上面已经判掉了它超预算的情况。
	cut := 0
	for offset := range input {
		if offset > maxBytes {
			break
		}
		cut = offset
	}
	return input[:cut]
}

// NormalizeSessionTitle 把一个收下来的标题洗干净并压进字节预算。
//
// 源: packages/session/session-title/src/normalize.ts:59-61
//
// 洗完可能是空串——一段纯转义序列的输入就是这个下场。调用方要把空串当成
// 「这个标题不作数」，而不是「标题就叫空」。
func NormalizeSessionTitle(input string, maxBytes int) string {
	return trimTrailingSpace(TruncateTitleUtf8(cleanTitleText(input), maxBytes))
}

// FallbackSessionTitle 从第一条人类消息里推出那个确定性的兜底标题。
//
// 源: packages/session/session-title/src/normalize.ts:70-74
//
// 两道闸门都要过：先按词数取前 maxWords 个，再按字节数截。词数那道是给显示
// 用的（一个二十词的标题在列表里放不下），字节数那道是给存储用的。中文没有
// 词间空格，洗完往往就是一整个「词」，所以对中文实际起作用的只有字节数那道。
//
// 新增: maxWords ≤ 0 同样表示不设上限，理由同 [TruncateTitleUtf8]。
func FallbackSessionTitle(input string, maxWords, maxBytes int) string {
	words := strings.Split(cleanTitleText(input), " ")
	// cleanTitleText 已经把空白折成单个空格并修剪了两头，所以这里只有输入本身
	// 洗完是空串时会得到一个 [""]。滤掉它，免得凑出一个只有空格的标题。
	kept := words[:0]
	for _, word := range words {
		if word != "" {
			kept = append(kept, word)
		}
	}
	if maxWords > 0 && len(kept) > maxWords {
		kept = kept[:maxWords]
	}
	return trimTrailingSpace(TruncateTitleUtf8(strings.Join(kept, " "), maxBytes))
}
