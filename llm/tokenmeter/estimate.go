// 本文件的作用：这个包唯一的那套定价启发式。整包每一处「这段内容值多少 token」
// 的问题最后都落到这里，包括服务自己的逐节点折叠、三个投影单元、以及压缩那边
// 记在事件里的影子价。
//
// 它是一套**固定密度**的估算，不是分词器：按字符数除以一个常数，再给每一块加一份
// 结构开销。它必然不准——CJK 会被低估，JSON schema 会被高估——而整个包的设计正是
// 建立在「它不准」这个前提上的：真正的数字来自提供方报回来的用量，这套启发式只
// 负责度量**两次用量采样之间**的那段增量。
//
// 源: packages/llm/token-meter/src/estimate.ts

package tokenmeter

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// charsPerToken 是这套启发式的密度：多少个字符算一个 token。
//
// 源: packages/llm/token-meter/src/estimate.ts:13
const charsPerToken = 4

// blockOverhead 是每一个内容块的结构开销。
//
// 源: packages/llm/token-meter/src/estimate.ts:16
//
// 线上每一块都要带判别标签、括号、逗号这些东西，它们同样占 token，
// 而它们的量和块里那段文本的长短无关，所以是一笔固定的加项。
const blockOverhead = 4

// RoleOverhead 是每一条消息的角色开销。
//
// 源: packages/llm/token-meter/src/estimate.ts:18-19（ROLE_OVERHEAD）
//
// 它导出，因为一次响应的定价（[TokenMeter.Measure] 里那次提供方助手内容的估价）
// 要在内容价之外自己补上这一笔。
const RoleOverhead = 4

// ceilTokens 把一个字符数按固定密度换成 token 数，向上取整。
//
// 新增: DSH 那边是 Math.ceil(n / CHARS_PER_TOKEN)。Go 的整数除法向下取整，
// 所以这里写成先加再除的那个惯用法。
func ceilTokens(chars int) int {
	return (chars + charsPerToken - 1) / charsPerToken
}

// textChars 数一段文本有多少个字符。
//
// 新增: DSH 那边是 JS 的 String.prototype.length，数的是 **UTF-16 码元**；Go 的
// len(string) 数的是 **UTF-8 字节**。两者对 ASCII 一致，对中文差三倍——一段中文
// 按字节数会被定价成按字数的三倍。
//
// 这里数的是**码点**（utf8.RuneCountInString）：它对基本多文种平面内的每一个字符
// 都和 UTF-16 码元数一致，也就是和 DSH 逐字相同；只有辅助平面（emoji、罕用汉字）
// 上 DSH 数 2 而这里数 1。选它而不是字节数，是因为常数的名字就叫「每 token 多少
// **字符**」——换成字节数之后这套启发式的密度会随语言漂移，而它存在的全部意义
// 就是「不管什么内容都用同一个密度」。
func textChars(text string) int {
	return utf8.RuneCountInString(text)
}

// EstimateContent 给一串内容块估价。
//
// 源: packages/llm/token-meter/src/estimate.ts:32-61（estimateContent）
//
// 新增: DSH 这个函数不会抛。Go 这边会返回错误，因为落进兜底那一支的块要靠
// json.Marshal 量长度，而 [llm.UnknownBlock] 的 MarshalJSON 在原始字节不是合法
// JSON 时是会失败的。把它咽掉（比如退回成只算结构开销）会让一条读不出来的块
// 静悄悄地按 4 个 token 计价，然后一路串进预算和压缩触发的算式里。
func EstimateContent(content llm.Content) (int, error) {
	tokens := 0
	for _, block := range content {
		switch typed := block.(type) {
		case llm.TextBlock:
			tokens += ceilTokens(textChars(typed.Text)) + blockOverhead
		case llm.ReasoningBlock:
			tokens += ceilTokens(textChars(typed.Text)) + blockOverhead
		case llm.ToolCallBlock:
			// 工具名和参数原文各自计价：参数是模型写的那串 JSON 文本，
			// 它多半比工具名长得多，但两者都真的会进请求体。
			tokens += ceilTokens(textChars(typed.Name)) +
				ceilTokens(textChars(typed.Arguments)) + blockOverhead
		case llm.ToolResultBlock:
			// 工具结果里装的还是内容块，递归下去。外面这一层自己也占一份结构开销。
			nested, err := EstimateContent(typed.Content)
			if err != nil {
				return 0, err
			}
			tokens += nested + blockOverhead
		default:
			// 源: packages/llm/token-meter/src/estimate.ts:44-47
			//
			// DSH 那边这一支的理由是 ContentBlockMap 可以被插件合并扩展，认不得的块
			// 按它排成 JSON 之后的结构长度保守计价。Go 这边落进来的是
			// [llm.ImageBlock] 和 [llm.UnknownBlock]：前者本包不认识（DSH 的联合里
			// 也没有它的分支），后者就是 DSH 说的那种扩展块。
			//
			// 排出来的字节和 JSON.stringify 不会逐字节相同（键的顺序、空白），
			// 但这是一套固定密度的启发式，差几个字符不改变它的性质。
			raw, err := json.Marshal(block)
			if err != nil {
				return 0, fmt.Errorf("token 估价：这一块排不成 JSON：%w", err)
			}
			tokens += blockOverhead + ceilTokens(textChars(string(raw)))
		}
	}
	return tokens, nil
}

// EstimateMessage 给一条消息估价：内容价加上一份角色开销。
//
// 源: packages/llm/token-meter/src/estimate.ts:63-70（estimateMessage）
func EstimateMessage(message llm.Message) (int, error) {
	content, err := EstimateContent(message.Content)
	if err != nil {
		return 0, err
	}
	return content + RoleOverhead, nil
}

// EstimateSystemTokens 给请求头里的系统提示估价。
//
// 源: packages/llm/token-meter/src/estimate.ts:72-80（estimateSystemTokens）
//
// 新增: DSH 那边收的是 EpochHeader | undefined，头不在或者 system 不在都算 0。
// Go 这边 [session.EpochHeader] 是值类型、System 是空串表示没有，所以「头不在」
// 和「一份空头」算出来是同一个 0——这两件事在**定价**上本来就没有区别。
// 它们的区别只在 [TokenMeter.Measure] 里那次锚点复用判定上，由那边自己拿着
// 存在位去分。
func EstimateSystemTokens(header session.EpochHeader) int {
	if header.System == "" {
		return 0
	}
	return ceilTokens(textChars(header.System)) + RoleOverhead
}

// EstimateToolsTokens 给请求头里那张工具表估价。
//
// 源: packages/llm/token-meter/src/estimate.ts:82-90（estimateToolsTokens）
//
// 整张表一起排成 JSON 再量长度，而不是逐个工具加起来：工具 schema 是原样进请求体的
// 一整段 JSON，它的定价对象就是那段文本本身。
func EstimateToolsTokens(header session.EpochHeader) (int, error) {
	if len(header.Tools) == 0 {
		return 0, nil
	}
	raw, err := json.Marshal(header.Tools)
	if err != nil {
		return 0, fmt.Errorf("token 估价：工具表排不成 JSON：%w", err)
	}
	return ceilTokens(textChars(string(raw))) + blockOverhead, nil
}

// EstimateHeader 给整个请求头估价：系统提示加工具表。
//
// 源: packages/llm/token-meter/src/estimate.ts:92-99（estimateHeader）
func EstimateHeader(header session.EpochHeader) (int, error) {
	tools, err := EstimateToolsTokens(header)
	if err != nil {
		return 0, err
	}
	return EstimateSystemTokens(header) + tools, nil
}
