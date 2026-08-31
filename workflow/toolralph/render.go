// 本文件的作用：交回父手上的那段文字——三种收场各自怎么说、一次轮次失败怎么说，
// 以及那道「说多长都不许超过这个数」的闸。
//
// 源: packages/workflow/tool-ralph/src/index.ts:351-392

package toolralph

import (
	"encoding/json"
	"strconv"
)

// truncationNotice 是被截断时接在末尾的那句标记。
//
// 源: packages/workflow/tool-ralph/src/index.ts:351
const truncationNotice = "\n… [truncated]"

// boundResult 把一段给父看的文字截到字数上限以内，**连那句截断标记一起算**。
//
// 源: packages/workflow/tool-ralph/src/index.ts:354-358
//
// 三段判断照抄：够短就原样交回；上限比标记本身还短就只交回标记的前几个字（这时候
// 已经装不下任何正文了，交回一段空的还不如让人看见这里被截过）；否则留出标记的位置
// 再截。
//
// 新增: 数的是**码点**不是字节，理由同 [jsonChars]。这一步在 Go 里必须显式转
// []rune 再切——直接对 string 切片切的是字节，会把一个多字节的字劈成两半，
// 交出去的就是一段带替换字符的坏文本。
func boundResult(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	notice := []rune(truncationNotice)
	if maxChars <= len(notice) {
		return string(notice[:maxChars])
	}
	return string(runes[:maxChars-len(notice)]) + truncationNotice
}

// indentReport 把一份报告排成那段缩进两格的 JSON，供人和模型直接读。
//
// 源: packages/workflow/tool-ralph/src/index.ts:366, 369, 372, 390（JSON.stringify(…, null, 2)）
//
// 不交回错误，理由同 [encodeReport]：这一步排不失败。
func indentReport(report RoundReport) string {
	encoded, _ := json.MarshalIndent(report, "", "  ")
	return string(encoded)
}

// rounds 把轮数排成 "1 round" / "3 rounds"。
//
// 源: packages/workflow/tool-ralph/src/index.ts:362
func rounds(count int) string {
	if count == 1 {
		return "1 round"
	}
	return strconv.Itoa(count) + " rounds"
}

// renderResult 排出那段终局文字，并且**不**把孩子的自述说成认证。
//
// 源: packages/workflow/tool-ralph/src/index.ts:361-376
//
// 措辞是刻意的：「worker reported completion」而不是「done」。本包一个字的证据都
// 没验过，说成后者就是替孩子作证。
//
// 那个 default 接住的是一个不该出现的收场。DSH 那边 switch 是穷尽的（TS 编译期
// 保证），Go 这边 [RunStatus] 是具名字符串类型，挡不住。让它大声说出来，而不是
// 交回一段空文本。
func renderResult(result runResult, maxChars int) string {
	var text string
	switch result.Status {
	case RunComplete:
		text = "Ralph worker reported completion after " + rounds(result.RoundsStarted) +
			".\nFinal report:\n" + indentReport(result.Report)
	case RunBlocked:
		text = "Ralph worker reported a blocker after " + rounds(result.RoundsStarted) +
			".\nFinal report:\n" + indentReport(result.Report)
	case RunBudgetLimited:
		text = "Ralph reached its " + rounds(result.RoundsStarted) +
			" limit; the worker reported work remaining.\nFinal report:\n" + indentReport(result.Report)
	default:
		text = "Ralph ended with an unknown status (" + string(result.Status) +
			").\nFinal report:\n" + indentReport(result.Report)
	}
	return boundResult(text, maxChars)
}

// renderRoundFailure 排出一次普通的孩子失败，连着最近那份耐久交接一起。
//
// 源: packages/workflow/tool-ralph/src/index.ts:386-392
//
// 那份交接是这次调用留下来的唯一成果：孩子在工作区上真干过的活儿还在那儿，
// 而这段 JSON 是父唯一能看见的、关于「干到哪儿了」的说明。丢掉它，父就只知道
// 「Ralph 挂了」，接不下去。
//
// 新增: 中间那句 Cause 是 DSH 没有的，理由见 [roundFailure]。它单独一行摆在
// 交接前面：那是本包这条接缝的判断，跟孩子自己说的话得分开。
func renderRoundFailure(failure *roundFailure) string {
	text := "Ralph round " + strconv.Itoa(failure.round) +
		" child failed before producing a structured report."
	if failure.cause != nil {
		text += "\nCause: " + failure.cause.Error()
	}
	if failure.lastReport == nil {
		text += "\nNo previous handoff was available."
	} else {
		text += "\nLast successful handoff:\n" + indentReport(*failure.lastReport)
	}
	return boundResult(text, failure.maxChars)
}
