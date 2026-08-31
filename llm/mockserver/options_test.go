// 本文件的作用：验配置校验——哪些配置起不来，以及没配的那些落到了什么值上。
//
// 源: packages/test-support/llm-mock-server/tests/server.spec.ts:334-359

package mockserver_test

import (
	"strings"
	"testing"
	"time"

	"ds-harness-go/llm/mockserver"
)

// TestStartRejectsInvalidOptions 是那张拒绝表。
//
// 源: packages/test-support/llm-mock-server/tests/server.spec.ts:335-354
//
// DSH 那张表有 19 行，这里只剩下还能违反的那些。差额不是把校验丢了，是「零值即
// 默认」这个选择让一部分校验在 Go 里无从违反——逐条理由见 [TestUnsetOptionsFallBackToDefaults]
// 和 [mockserver.Options] 的文档。另一头补上了 DSH 靠 TypeScript 字面量联合在
// 编译期挡掉、Go 只能在运行期挡的那两条（剧本里的坏行为名、权重表里的坏行为名）。
func TestStartRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		options mockserver.Options
		reason  string
	}{
		{
			"空剧本",
			mockserver.Options{},
			"Sequence 不能为空",
		},
		{
			// 新增: DSH 这一条由 TypeScript 的字面量联合在编译期担保，resolveOptions
			// 一个字都没查。[mockserver.Behavior] 只是个 string，认不出来的名字会从
			// 演出那个 switch 底下漏过去，客户端收到一个空的 200 而剧本已经被消费掉。
			"剧本里有不认识的行为",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess, "nope"}},
			`第 1 条是不认识的行为 "nope"`,
		},
		{
			"端口是负数",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, Port: -1},
			"Port 必须落在",
		},
		{
			"端口越界",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, Port: 65536},
			"Port 必须落在",
		},
		{
			"分片大小是负数",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, ChunkSize: -1},
			"ChunkSize 不能是负数",
		},
		{
			"分片停顿是负数",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, ChunkDelay: -time.Millisecond},
			"ChunkDelay 必须落在",
		},
		{
			"断开停顿越界",
			mockserver.Options{
				Sequence:        []mockserver.Behavior{mockserver.BehaviorSuccess},
				DisconnectDelay: mockserver.MaxTimerDelay + time.Millisecond,
			},
			"DisconnectDelay 必须落在",
		},
		{
			"重试建议是负数",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, RetryAfter: -time.Second},
			"RetryAfter 必须落在",
		},
		{
			"工具参数不是合法 JSON",
			mockserver.Options{Sequence: []mockserver.Behavior{mockserver.BehaviorSuccess}, ToolArguments: "{"},
			"ToolArguments 必须是合法 JSON",
		},
		{
			// 同样是 TypeScript 编译期挡掉、Go 只能运行期挡的一条。
			"权重挂在抽象行为上",
			mockserver.Options{
				Sequence:      []mockserver.Behavior{mockserver.BehaviorRandom},
				RandomWeights: map[mockserver.Behavior]float64{mockserver.BehaviorRandom: 1},
			},
			"不是一种具体行为",
		},
		{
			"权重是负数",
			mockserver.Options{
				Sequence:      []mockserver.Behavior{mockserver.BehaviorRandom},
				RandomWeights: map[mockserver.Behavior]float64{mockserver.BehaviorSuccess: -1},
			},
			"必须是非负的有限数",
		},
		{
			"权重全是零",
			mockserver.Options{
				Sequence:      []mockserver.Behavior{mockserver.BehaviorRandom},
				RandomWeights: map[mockserver.Behavior]float64{mockserver.BehaviorSuccess: 0},
			},
			"至少要有一项正权重",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server, err := mockserver.Start(testCase.options)
			if err == nil {
				_ = server.Close()
				t.Fatalf("%+v 该被拒", testCase.options)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("理由里该有 %q，得到 %q", testCase.reason, err.Error())
			}
		})
	}
}

// TestUnsetOptionsFallBackToDefaults 验「零值即默认」真的落到了那些默认值上。
//
// 新增: DSH 用 `field?:` 把「没配」和「配成空」分得清清楚楚，于是它可以拒绝空的
// successText。Go 的结构体零值表达不了这个差别，本包选的是零值即默认。这条用例
// 是那个选择的对账单：DSH 拒绝的那些输入在这里不报错，但也**不能**因此变成空串
// 或零延时溜进演出里——那才是真的把校验丢了。
func TestUnsetOptionsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	server := startServer(t, mockserver.Options{
		Sequence:      []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText:   "",
		PartialText:   "",
		ReasoningText: "",
		ChunkSize:     0,
		ChunkDelay:    0,
		RetryAfter:    0,
		ToolName:      "",
		ToolArguments: "",
	})

	_, body := readChat(t, server, chatRequest{})

	// 默认文案被默认的分片大小切成了 8 个码点一片，所以线路上找不到完整的那句话。
	// 逐片断言一次把两个默认值一起钉住：文案是 "mock response recovered"（而不是
	// 一次「发了个空字符串」的成功），分片大小是 8（而不是 0——0 会让切分循环
	// 原地打转，正是 DSH 拒绝 chunkSize: 0 的理由）。
	for _, fragment := range []string{"mock res", "ponse re", "covered"} {
		if !strings.Contains(body, `"content":"`+fragment+`"`) {
			t.Errorf("线路上少了分片 %q，全文是 %s", fragment, body)
		}
	}

	record := waitForOutcome(t, server, 1)
	// 三片正文 + 一条收尾 + [DONE]。
	if record.ChunksSent != 5 {
		t.Errorf("该发 5 条事件，发了 %d 条", record.ChunksSent)
	}
}
