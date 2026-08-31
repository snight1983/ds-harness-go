// 本文件的作用：钉住那套固定密度启发式的算术，以及它在哪些地方**故意**不准。

package tokenmeter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestCeilTokensRoundsUp(t *testing.T) {
	t.Parallel()

	cases := map[int]int{0: 0, 1: 1, 3: 1, 4: 1, 5: 2, 8: 2, 9: 3}
	for chars, want := range cases {
		if got := ceilTokens(chars); got != want {
			t.Fatalf("%d 个字符该换成 %d 个 token，实际 %d", chars, want, got)
		}
	}
}

// 这条钉的是 [textChars] 那个注释里的承诺：中文按**字**数，不按字节数。
// 写死成 len(string) 的话下面这三个字会被算成九个字符、也就是三个 token。
func TestTextCharsCountsRunesNotBytes(t *testing.T) {
	t.Parallel()

	if got := textChars("中文字"); got != 3 {
		t.Fatalf("三个汉字该数出 3 个字符，实际 %d", got)
	}
	if got, want := ceilTokens(textChars("中文字")), 1; got != want {
		t.Fatalf("三个汉字该定价成 %d 个 token，实际 %d", want, got)
	}
}

func TestEstimateContentPricesEachBlockKind(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		content llm.Content
		want    int
	}{
		"空内容一分不要": {content: nil, want: 0},
		// ceil(5/4)=2，加一份块开销。
		"文本块": {content: llm.Content{llm.TextBlock{Text: "hello"}}, want: 2 + blockOverhead},
		// 推理块和文本块同价。
		"推理块": {content: llm.Content{llm.ReasoningBlock{Text: "hello"}}, want: 2 + blockOverhead},
		// 工具名 ceil(2/4)=1，参数 ceil(9/4)=3。
		"工具调用块": {
			content: llm.Content{llm.ToolCallBlock{ID: "c1", Name: "ls", Arguments: `{"a":123}`}},
			want:    1 + 3 + blockOverhead,
		},
		// 里层文本 2+4，外层自己再加一份块开销。
		"工具结果块递归下去": {
			content: llm.Content{llm.ToolResultBlock{
				ToolCallID: "c1",
				Content:    llm.Content{llm.TextBlock{Text: "hello"}},
			}},
			want: (2 + blockOverhead) + blockOverhead,
		},
		"多块相加": {
			content: llm.Content{llm.TextBlock{Text: "hello"}, llm.TextBlock{Text: "hello"}},
			want:    2*(2+blockOverhead) + 0,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := EstimateContent(testCase.content)
			if err != nil {
				t.Fatalf("估价不该失败：%v", err)
			}
			if got != testCase.want {
				t.Fatalf("估价不对：想要 %d，实际 %d", testCase.want, got)
			}
		})
	}
}

// 认不得的块走排成 JSON 再量长度那一支，所以它的价随内容长短走，
// 不是一个写死的结构开销。
func TestEstimateContentPricesUnknownBlocksByTheirJSON(t *testing.T) {
	t.Parallel()

	short, err := EstimateContent(llm.Content{llm.UnknownBlock{Kind: "x", Raw: json.RawMessage(`{"type":"x"}`)}})
	if err != nil {
		t.Fatalf("短块估价不该失败：%v", err)
	}
	long, err := EstimateContent(llm.Content{llm.UnknownBlock{
		Kind: "x",
		Raw:  json.RawMessage(`{"type":"x","payload":"` + strings.Repeat("z", 200) + `"}`),
	}})
	if err != nil {
		t.Fatalf("长块估价不该失败：%v", err)
	}
	if short <= blockOverhead {
		t.Fatalf("认不得的块至少要算上它自己那段 JSON：%d", short)
	}
	if long <= short {
		t.Fatalf("长块该比短块贵：短 %d，长 %d", short, long)
	}
}

// 这条钉的是 [EstimateContent] 为什么要返回错误：一块排不出 JSON 的内容
// 不许被静悄悄地按结构开销计价。
func TestEstimateContentFailsOnABlockThatCannotBeMarshalled(t *testing.T) {
	t.Parallel()

	_, err := EstimateContent(llm.Content{llm.UnknownBlock{Kind: "x", Raw: json.RawMessage(`{`)}})
	if err == nil {
		t.Fatal("一块排不出 JSON 的内容该让估价失败")
	}
	if !errors.Is(err, llm.ErrMalformedValue) {
		t.Fatalf("该把底下那个错误裹出来：%v", err)
	}
}

func TestEstimateMessageAddsTheRoleOverhead(t *testing.T) {
	t.Parallel()

	message := textMessage("m", llm.RoleUser, llm.UserSource{}, "hello")
	content, err := EstimateContent(message.Content)
	if err != nil {
		t.Fatalf("内容估价不该失败：%v", err)
	}
	got, err := EstimateMessage(message)
	if err != nil {
		t.Fatalf("消息估价不该失败：%v", err)
	}
	if got != content+RoleOverhead {
		t.Fatalf("消息价该是内容价加一份角色开销：想要 %d，实际 %d", content+RoleOverhead, got)
	}
}

// 一条空内容的消息**照样**要算一份角色开销：它在表面上是真实存在的一格。
// 这和 [TokenMeter.estimateProviderAssistant] 对空内容算 0 有意分家。
func TestEstimateMessageOfEmptyContentIsTheRoleOverhead(t *testing.T) {
	t.Parallel()

	got, err := EstimateMessage(llm.Message{ID: "m", Role: llm.RoleAssistant, Source: llm.ModelSource{}})
	if err != nil {
		t.Fatalf("估价不该失败：%v", err)
	}
	if got != RoleOverhead {
		t.Fatalf("空消息该只值一份角色开销 %d，实际 %d", RoleOverhead, got)
	}
}

func TestEstimateHeaderSplitsIntoSystemAndTools(t *testing.T) {
	t.Parallel()

	header := session.EpochHeader{
		System: "you are a helpful assistant",
		Tools: []llm.ToolSchema{{
			Name: "ls", Description: "list files",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
	}

	system := EstimateSystemTokens(header)
	if system == 0 {
		t.Fatal("有系统提示就不该估成 0")
	}
	tools, err := EstimateToolsTokens(header)
	if err != nil {
		t.Fatalf("工具表估价不该失败：%v", err)
	}
	if tools == 0 {
		t.Fatal("有工具表就不该估成 0")
	}

	whole, err := EstimateHeader(header)
	if err != nil {
		t.Fatalf("整头估价不该失败：%v", err)
	}
	if whole != system+tools {
		t.Fatalf("整头该等于两半之和：想要 %d，实际 %d", system+tools, whole)
	}
}

// 「头不在」和「一份空头」在定价上就该是同一个 0——两者的区别只活在
// [TokenMeter.Measure] 的锚点比对里。
func TestEstimateHeaderOfAnEmptyHeaderIsZero(t *testing.T) {
	t.Parallel()

	got, err := EstimateHeader(session.EpochHeader{})
	if err != nil {
		t.Fatalf("估价不该失败：%v", err)
	}
	if got != 0 {
		t.Fatalf("一份空头该值 0，实际 %d", got)
	}
	if got := EstimateSystemTokens(session.EpochHeader{}); got != 0 {
		t.Fatalf("没有系统提示该值 0，实际 %d", got)
	}
}

// 工具表排不出 JSON 的时候要报错，而不是当成「这次请求没带工具」。
func TestEstimateToolsTokensFailsOnAnUnmarshalableSchema(t *testing.T) {
	t.Parallel()

	header := session.EpochHeader{Tools: []llm.ToolSchema{{
		Name: "bad", Parameters: json.RawMessage(`{`),
	}}}
	if _, err := EstimateToolsTokens(header); err == nil {
		t.Fatal("一张排不出去的工具表该让估价失败")
	}
	if _, err := EstimateHeader(header); err == nil {
		t.Fatal("整头估价该把工具表那个错误带出来")
	}
}
