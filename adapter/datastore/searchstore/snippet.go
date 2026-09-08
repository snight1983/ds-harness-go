// 本文件的作用：摘录——从命中的那段正文里切出给人看的一小段。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:269-316（makeSnippet、normalizeMarkedText）

package searchstore

import (
	"strings"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
)

// ellipsis 是摘录被切掉一头时补的那个记号。
const ellipsis = "…"

// snippet 从正文里切出一段围着命中处的文字。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:269-316（makeSnippet、normalizeMarkedText）
//
// 正文先把连续空白折成一个空格再切：一条事件的正文里常有大段换行和缩进，不折的话
// 一段 240 个字的摘录可能有一大半是空白，人看不出命中在哪。折过之后，摘录的长度
// 才和「看得见多少字」对得上。
//
// 定位命中处用的是 [sessionquery.CompileTextFilter]——和事件文本过滤器同一套语义，
// 于是「这条为什么被搜出来」和「摘录圈的是哪一段」说的是同一件事。编不出来或者对
// 不上时退回正文开头：一条命中宁可给一段不居中的摘录，也不该整条丢掉——摘录只是给
// 人看的，它切在哪儿不影响这条命中作不作数。
func snippet(text, query string, maxChars int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if maxChars <= 0 || utf8.RuneCountInString(folded) <= maxChars {
		return folded
	}

	start, end := 0, 0
	if pattern, err := sessionquery.CompileTextFilter(query); err == nil {
		if at := pattern.FindStringIndex(folded); at != nil {
			start = utf8.RuneCountInString(folded[:at[0]])
			end = utf8.RuneCountInString(folded[:at[1]])
		}
	}

	// 把窗口摆在命中处的正中间；命中本身比窗口还长时就从它的开头切起。
	total := utf8.RuneCountInString(folded)
	from := start - max((maxChars-(end-start))/2, 0)
	from = min(max(from, 0), total-maxChars)

	window := runeSlice(folded, from, from+maxChars)
	if from > 0 {
		window = ellipsis + window
	}
	if from+maxChars < total {
		window += ellipsis
	}
	return window
}

// runeSlice 按字（而不是按字节）切出 [from, to) 那一段。
//
// 新增: 按字节切会把一个多字节的字劈成两半，交出去的就不是合法的 UTF-8 了。中文
// 每个字三个字节，这件事在这个仓库里是常态而不是边角。
func runeSlice(text string, from, to int) string {
	runes := []rune(text)
	return string(runes[max(from, 0):min(to, len(runes))])
}
