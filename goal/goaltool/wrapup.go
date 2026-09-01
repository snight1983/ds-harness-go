// 本文件的作用：一次自动轮次报出 complete 或者 blocked 之后，塞给模型的那段
// 收尾指令。
//
// 源: packages/goal/tool-goal/src/wrapup.ts

package goaltool

import "github.com/snight1983/ds-harness-go/llm"

// grounding 是两段收尾指令共用的那句「只说日志里立得住的事」。
//
// 源: packages/goal/tool-goal/src/wrapup.ts:5-7
//
// 这句话是给模型看的，一个字都不许改译。结尾那个空格是刻意的：它和后面那句话
// 拼在同一段里。
const grounding = "Report only what earlier rounds and tool results in this session actually establish; " +
	"when a detail is not in the session, say so instead of inventing it. "

// completeInstruction 是 complete 那一支的正文，夹在 heading 和结束标签之间。
//
// 源: packages/goal/tool-goal/src/wrapup.ts:22-27
const completeInstruction = "The goal is marked complete and this autonomous run is ending. Write the closing " +
	"message to the user now: state the outcome, summarize what was done and how it was " +
	"verified, and point to the concrete results (files, commits, or other artifacts). " +
	grounding +
	"Note anything the user should review or do next. Address the user directly. Do not " +
	"call any more tools in this run; further work waits for the user's next instruction.\n"

// blockedInstruction 是 blocked 那一支的正文。
//
// 源: packages/goal/tool-goal/src/wrapup.ts:32-38
const blockedInstruction = "The goal is marked blocked and this autonomous run is ending. Write the closing " +
	"message to the user now: state what has been completed so far, describe the concrete " +
	"blocking condition and what you tried, and say exactly what you need from the user to " +
	"continue. " +
	grounding +
	"Address the user directly. Do not call any more tools in this run; further work " +
	"waits for the user's next instruction.\n"

// quoteJSON 把一句自由文本排成一个 JSON 字符串字面量，**不**做 HTML 转义。
//
// 新增: DSH 用的是 JSON.stringify，它不把 < > & 转成 < 这类写法；
// [encoding/json.Marshal] 默认转。目标描述和阻塞原因都是人和模型写的自由文本，
// 里面出现尖括号一点都不稀奇——而这段字节是直接摆进模型上下文里给它读的，多出来
// 的转义只会让它看见一句和原文长得不一样的话。理由同
// [github.com/snight1983/ds-harness-go/goal/goal] 里那个同名的辅助函数。
//
// 排不出去是不可能的：入参是一个 Go 字符串，[encoding/json] 对任何字节序列都排
// 得出来（非法 UTF-8 会被换成替换字符，不会失败）。所以这里吞掉那个错误而不是
// 把它一路带上去——带上去只会在渲染路径上留下一条永远走不到的分支。
func quoteJSON(value string) string {
	encoded, _ := marshalNoEscape(value)
	return string(encoded)
}

// renderWrapupContext 排出那段收尾指令：blockedReason 是空串就是 complete 那一支。
//
// 源: packages/goal/tool-goal/src/wrapup.ts:17-41
//
// 新增: DSH 靠 `blockedReason?: string` 的 undefined 分支。Go 这边用空串当判别，
// 因为这两支的唯一调用点（见 [Controller.runUpdate]）在走到这里之前已经把
// blocked 那一支的原因验成非空白了——一个空串在这里只可能来自 complete。
func renderWrapupContext(objective, blockedReason string) llm.Content {
	heading := "Objective: " + quoteJSON(objective) + "\n"
	var text string
	if blockedReason == "" {
		text = "<goal_complete>\n" + heading + completeInstruction + "</goal_complete>"
	} else {
		text = "<goal_blocked>\n" + heading +
			"Blocked: " + quoteJSON(blockedReason) + "\n" +
			blockedInstruction + "</goal_blocked>"
	}
	return llm.Content{llm.TextBlock{Text: text}}
}
