// 本文件的作用：把一条消息的文本块折成一段收住长度的预览。
//
// 源: packages/session/session-turn-outline/src/projection.ts:35-59（preview）

package turnoutline

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/llm"
)

// PromptPreviewMaxChars 是提示词预览的字数上限：列表卡片上的一行。
//
// 源: packages/session/session-turn-outline/src/projection.ts:28-29（PROMPT_PREVIEW_LIMIT）
const PromptPreviewMaxChars = 50

// ResponsePreviewMaxChars 是回复预览的字数上限：列表卡片上的三行。
//
// 源: packages/session/session-turn-outline/src/projection.ts:30-31（RESPONSE_PREVIEW_LIMIT）
const ResponsePreviewMaxChars = 120

// preview 把内容里的文本块用空格接起来、把连续空白压成一个空格，
// 收进 limit 个字符；砍过就以省略号收尾。
//
// 源: packages/session/session-turn-outline/src/projection.ts:36-59
//
// 新增: DSH 数的是 UTF-16 码元，这里数**字符**（rune），理由见包文档
// 「预览按字算，不按字节算」那一节。
func preview(content llm.Content, limit int) string {
	// 逐块的预算是上限的两倍：空白压缩之后长度只会变短，多留一倍就够抵掉压缩，
	// 而封住每一块是必须的——折叠跑在每一条消息事件上，一个几兆的文本块不该
	// 为了这么短的一段预览被整个拼进来再正规化一遍。
	budget := limit * 2

	var builder strings.Builder
	length := 0
	clipped := false
	for _, block := range content {
		text, isText := block.(llm.TextBlock)
		if !isText {
			continue
		}
		if length >= budget {
			clipped = true
			break
		}
		chunk, cut := firstRunes(text.Text, budget)
		if builder.Len() > 0 {
			builder.WriteByte(' ')
			length++
		}
		builder.WriteString(chunk)
		length += utf8.RuneCountInString(chunk)
		if cut {
			clipped = true
			break
		}
	}

	normalized := strings.Join(strings.Fields(builder.String()), " ")
	runes := []rune(normalized)
	if len(runes) > limit-1 {
		return strings.TrimRightFunc(string(runes[:limit-1]), unicode.IsSpace) + "…"
	}
	if clipped {
		return normalized + "…"
	}
	return normalized
}

// firstRunes 取出前 n 个字符，第二个返回值说后面还有没有。
//
// 新增: DSH 那边是 `text.slice(0, n)` 配 `text.length > n`，两件事都是 O(1)。
// Go 里 []rune(s) 会把整个串搬一遍，而这里的输入正是那个可能有几兆的文本块——
// 所以按字节位置扫到第 n 个字符就停，代价只和 n 有关，和串多长无关。
func firstRunes(s string, n int) (string, bool) {
	count := 0
	for index := range s {
		if count == n {
			return s[:index], true
		}
		count++
	}
	return s, false
}
