// 本文件的作用：把那条续推提示词钉死——它排出来的字节、它对目标描述的转义口径，
// 以及它必须是纯的这件事。
//
// # 这些测试防的是什么错
//
//   - **悄悄改掉排出来的字节**。这段文本同时是那道不变量的比对基准
//     （见 [RegisterInvariants]）：改一个字符，历史日志里每一条续推消息都会在下次
//     装载时被判成伪造。所以这里逐字节断言整段，而不是断言它「包含某某关键词」。
//   - **让 HTML 转义混进目标描述**。[encoding/json.Marshal] 默认把 < > & 换成
//     < 这类写法，而这份字节是直接摆进模型上下文里给它读的。一句
//     「compare <b> and <i>」被转义之后，模型看见的就不是人写的那句话了。
//   - **让它变得不纯**。同样的入参排两遍必须逐字节相同——那道不变量靠重跑它来判定
//     日志里那条消息是不是本包写的。

package goalrounddriver

import (
	"reflect"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/llm"
)

// textOf 把一份内容折成它唯一那个文本块里的字符串。
func textOf(t *testing.T, content llm.Content) string {
	t.Helper()
	if len(content) != 1 {
		t.Fatalf("这份内容有 %d 个块，本该只有一个", len(content))
	}
	block, ok := content[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("第一个块是 %T，本该是 llm.TextBlock", content[0])
	}
	return block.Text
}

// viewOf 造一份只填了本条提示词用得着那三样的目标视图。
func viewOf(objective string, maxRounds int) *goal.View {
	return &goal.View{
		Snapshot: goal.Snapshot{
			Ref:           goal.Ref{ID: "goal-1", Revision: 1},
			Objective:     objective,
			Phase:         goal.PhaseActive,
			MaxGoalRounds: maxRounds,
		},
		Activation: goal.Armed,
	}
}

func TestRenderRoundPromptRendersTheExactBytes(t *testing.T) {
	want := "<goal_round>\n" +
		"Objective: \"ship the release\"\n" +
		"Round: 2/7\n\n" +
		"Continue working toward the objective in this same session. Treat the current workspace, " +
		"tool results, and durable session state as authoritative; inspect them instead of assuming " +
		"earlier narration is still current. Make concrete progress and verify the result. Before " +
		"claiming completion, gather evidence that the whole objective is achieved, read the current " +
		"goal, and mark it complete. If work remains, leave the goal active for the next round. Follow " +
		"the configured goal-tool policy before reporting a blocker.\n" +
		"</goal_round>"

	got := textOf(t, RenderRoundPrompt(viewOf("ship the release", 7), 2))
	if got != want {
		t.Fatalf("排出来的提示词不对：\n拿到：%q\n本该：%q", got, want)
	}
}

func TestRenderRoundPromptKeepsMarkupInTheObjective(t *testing.T) {
	got := textOf(t, RenderRoundPrompt(viewOf("compare <b> & <i>", 3), 1))
	if !strings.Contains(got, `Objective: "compare <b> & <i>"`) {
		t.Fatalf("目标描述里的标记被转义了：%q", got)
	}
}

func TestRenderRoundPromptQuotesControlCharacters(t *testing.T) {
	got := textOf(t, RenderRoundPrompt(viewOf("line one\nline \"two\"", 3), 1))
	if !strings.Contains(got, `Objective: "line one\nline \"two\""`) {
		t.Fatalf("换行和引号本该被排成 JSON 字面量：%q", got)
	}
}

func TestRenderRoundPromptIsPure(t *testing.T) {
	view := viewOf("stay the same", 4)
	first := RenderRoundPrompt(view, 3)
	second := RenderRoundPrompt(view, 3)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同样的入参排出了不一样的东西：\n第一次：%#v\n第二次：%#v", first, second)
	}
}

func TestQuoteJSONSurvivesInvalidUTF8(t *testing.T) {
	// 目标描述是自由文本，理论上可能带着一段非法 UTF-8。[quoteJSON] 吞掉了那个错误
	// （见它自己的注释），所以这里验的是它吞得起：交回的仍旧是一个合法的 JSON 字面量，
	// 而不是空串。
	quoted := quoteJSON(string([]byte{0xff, 0xfe}))
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		t.Fatalf("非法 UTF-8 排出来的不是一个 JSON 字符串字面量：%q", quoted)
	}
}

func TestMarshalNoEscapeReportsUnencodableValues(t *testing.T) {
	// 一个 channel 排不出 JSON。[marshalNoEscape] 是共享的辅助函数，它必须把这种
	// 失败原样交出去，而不是自己吞掉——[quoteJSON] 敢吞是因为它的入参只可能是字符串。
	if _, err := marshalNoEscape(make(chan int)); err == nil {
		t.Fatal("排一个 channel 本该失败")
	}
}
