// 本文件的作用：那条续推提示词本身——一个纯函数，输入一份目标视图和轮号，
// 输出留在会话历史里的那一个内容块。
//
// 源: packages/goal/goal-round-driver/src/prompt.ts

package goalrounddriver

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/llm"
)

// roundInstruction 是续推提示词里那段不随轮次变化的正文。
//
// 源: packages/goal/goal-round-driver/src/prompt.ts:19-25
//
// 这段话是给模型看的，一个字都不许改译。它同时也是那道不变量的比对基准
// （见 [RegisterInvariants]）：日志里任何一条带 goal 来源的续推消息，内容都必须和
// 这里排出来的逐字节相同，否则那条消息就不是本包写的。
const roundInstruction = "Continue working toward the objective in this same session. Treat the current workspace, " +
	"tool results, and durable session state as authoritative; inspect them instead of assuming " +
	"earlier narration is still current. Make concrete progress and verify the result. Before " +
	"claiming completion, gather evidence that the whole objective is achieved, read the current " +
	"goal, and mark it complete. If work remains, leave the goal active for the next round. Follow " +
	"the configured goal-tool policy before reporting a blocker.\n"

// marshalNoEscape 把一个值排成 JSON，**不**做 HTML 转义。
//
// 新增: DSH 那句 objective 是 JSON.stringify 排的，它不把 < > & 转成 < 这类
// 写法；[encoding/json.Marshal] 默认转。目标描述是人写的自由文本，而这份字节直接
// 摆进模型上下文里给它读——多出来的转义只会让它看见一句和原文长得不一样的话。
// 理由同 [github.com/snight1983/ds-harness-go/goal/goal] 与 [github.com/snight1983/ds-harness-go/goal/goaltool] 里那两个同名的
// 辅助函数。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// quoteJSON 把一句自由文本排成一个 JSON 字符串字面量。
//
// 排不出去是不可能的：入参是一个 Go 字符串，[encoding/json] 对任何字节序列都排得
// 出来（非法 UTF-8 会被换成替换字符，不会失败）。所以这里吞掉那个错误而不是把它
// 一路带上去——带上去只会在这条渲染路径上留下一条永远走不到的分支，而这个函数的
// 调用方（那道不变量）恰恰要求它是纯的。
func quoteJSON(value string) string {
	encoded, _ := marshalNoEscape(value)
	return string(encoded)
}

// RenderRoundPrompt 排出一轮续推的完整指令。
//
// 源: packages/goal/goal-round-driver/src/prompt.ts:12-26
//
// 纯函数，而且必须保持是纯的：那道不变量靠重跑它来判定日志里那条消息是不是本包
// 写的（见 [RegisterInvariants]）。任何一点随时间、随环境变化的东西混进来，
// 都会让一段完全正常的日志在回放时被判成伪造。
func RenderRoundPrompt(view *goal.View, round int) llm.Content {
	text := "<goal_round>\n" +
		"Objective: " + quoteJSON(view.Objective) + "\n" +
		fmt.Sprintf("Round: %d/%d\n\n", round, view.MaxGoalRounds) +
		roundInstruction +
		"</goal_round>"
	return llm.Content{llm.TextBlock{Text: text}}
}
