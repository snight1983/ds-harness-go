// 本文件的作用：钉住「一个回合花了多少」这份精确账——它只把提供方亲口报过的数
// 加起来，任何一处证不出来就整份不给。
//
// # 这些测试防的是什么错
//
//   - 一次重试被算成一次请求，于是那次失败的尝试白花的钱从账上消失了；
//   - 一次尝试从一份用量采样反推出来，于是残缺的日志也能凑出一个数——那个数
//     会和别处那些估算混在一起，读的人再也分不出哪个是提供方说的；
//   - 路由表只报得出一半却照样交出去，读的人会以为那就是全部；
//   - 一份自相矛盾的记账（负数、推理比输出还多）被将就着加进总数。

package tokenmeter

import (
	"testing"

	"github.com/snight1983/ds-harness-go/feature/llmretry"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// turnStartEvent 造一条 turn/start。
func turnStartEvent(t *testing.T, turn int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: mustJSON(t, sessionlog.TurnStartData{Turn: turn}),
	}
}

// turnEndEvent 造一条正常收尾的 turn/end。
func turnEndEvent(t *testing.T, turn int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventTurnEnd,
		Data: mustJSON(t, sessionlog.TurnEndData{Turn: turn, Reason: sessionlog.CompletedTurnEnd{}}),
	}
}

// routedAssistantEvent 造一条说得清自己走哪条路由的助手消息。
func routedAssistantEvent(t *testing.T, turn, step int, provider, model string, usage *llm.TokenUsage) sessionlog.Event {
	t.Helper()

	source := llm.ModelSource{Provenance: llm.Provenance{Provider: provider, Model: model}}
	return sessionlog.Event{
		Type: sessionlog.EventAssistantMessage,
		Data: mustJSON(t, sessionlog.AssistantMessageData{
			Turn: turn, Step: step,
			Message: textMessage("a", llm.RoleAssistant, source, "hi"),
			Usage:   usage,
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// retryEvent 造一条 llm/retry：一次已经排好期、还没开始等的重试。
func retryEvent(t *testing.T, turn, step, retry int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: llmretry.EventRetry,
		Data: mustJSON(t, llmretry.RetryData{
			RetryID: "r1", Turn: turn, Step: step, Provider: "p",
			Mode: llm.RetryAlways, PolicyKey: "k", Retry: retry,
			Failure: llm.Failure{Message: "上游超时了", Code: "TIMEOUT"},
		}),
	}
}

// retryStartedEvent 造一条 llm/retry-started：那段等待熬过去了。
func retryStartedEvent(t *testing.T, turn, step, retry int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: llmretry.EventRetryStarted,
		Data: mustJSON(t, llmretry.RetryStartedData{RetryID: "r1", Turn: turn, Step: step, Retry: retry}),
	}
}

// 一个干净的单步回合：账就是那一次尝试报的数，路由也说得清。
func TestDeriveTurnUsageSumsOneCleanAttempt(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{
		InputTokens: 100, OutputTokens: 20,
		CacheReadTokens: 5, CacheWriteTokens: 3, ReasoningTokens: 8,
	}
	got, ok := DeriveTurnUsage([]sessionlog.Event{
		turnStartEvent(t, 0),
		stepStartEvent(t, 0, 0),
		chunkEvent(t, 0, 0, llm.UsageChunk{Usage: usage}),
		routedAssistantEvent(t, 0, 0, "p", "m", &usage),
		stepEndEvent(t, 0, 0),
		turnEndEvent(t, 0),
	})
	if !ok {
		t.Fatal("一个干净的回合该证得出来")
	}
	if got.UncachedInputTokens != 100 || got.OutputTokens != 20 ||
		got.CacheReadTokens != 5 || got.CacheWriteTokens != 3 || got.ReasoningTokens != 8 {
		t.Fatalf("五个桶该原样加起来：%+v", got)
	}
	// 推理那一份已经含在输出里，不另加。
	if want := 100 + 5 + 3 + 20; got.TotalTokens != want {
		t.Fatalf("精确总量该是四个互不重叠的桶之和：想要 %d，实际 %d", want, got.TotalTokens)
	}
	if len(got.Routes) != 1 || got.Routes[0] != (TurnUsageRoute{Provider: "p", Model: "m"}) {
		t.Fatalf("这一次尝试的路由该说得清：%+v", got.Routes)
	}
}

// 流中途那条用量分块和它后面那条落定消息报的常常是同一份，落定那份更权威——
// 是**替换**不是叠加。
func TestDeriveTurnUsagePrefersTheSettledSample(t *testing.T) {
	t.Parallel()

	streamed := llm.TokenUsage{InputTokens: 100, OutputTokens: 3}
	settled := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	got, ok := DeriveTurnUsage([]sessionlog.Event{
		turnStartEvent(t, 0),
		stepStartEvent(t, 0, 0),
		chunkEvent(t, 0, 0, llm.UsageChunk{Usage: streamed}),
		routedAssistantEvent(t, 0, 0, "p", "m", &settled),
		stepEndEvent(t, 0, 0),
		turnEndEvent(t, 0),
	})
	if !ok {
		t.Fatal("该证得出来")
	}
	if got.OutputTokens != 20 {
		t.Fatalf("落定那份该盖掉流里那份：%d", got.OutputTokens)
	}
}

// 一次重试是**两次计费尝试**：失败那次照样计过费，两次的账都要加进来。
func TestDeriveTurnUsageCountsARetryAsTwoBilledAttempts(t *testing.T) {
	t.Parallel()

	failed := llm.TokenUsage{InputTokens: 100, OutputTokens: 2}
	succeeded := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	got, ok := DeriveTurnUsage([]sessionlog.Event{
		turnStartEvent(t, 0),
		stepStartEvent(t, 0, 0),
		chunkEvent(t, 0, 0, llm.UsageChunk{Usage: failed}),
		retryEvent(t, 0, 0, 1),
		retryStartedEvent(t, 0, 0, 1),
		chunkEvent(t, 0, 0, llm.UsageChunk{Usage: succeeded}),
		routedAssistantEvent(t, 0, 0, "p", "m", &succeeded),
		stepEndEvent(t, 0, 0),
		turnEndEvent(t, 0),
	})
	if !ok {
		t.Fatal("一次重试过的步骤该证得出来")
	}
	if want := failed.InputTokens + succeeded.InputTokens; got.UncachedInputTokens != want {
		t.Fatalf("失败那次的账也要算：想要 %d，实际 %d", want, got.UncachedInputTokens)
	}
	// 失败那次说不清自己走的哪条路由（它没交出助手消息），路由表因此整份不给。
	if got.Routes != nil {
		t.Fatalf("有一次尝试说不清路由，整份路由表该不给：%+v", got.Routes)
	}
}

// 一次失败／中止的收尾就是那次尝试的终点：它照样计费，而后面不会再有助手消息。
func TestDeriveTurnUsageClosesAnAttemptOnAFailedFinish(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 40, OutputTokens: 1}
	for name, reason := range map[string]llm.FinishReason{
		"失败": llm.ErrorFinish{Failure: llm.Failure{Message: "炸了", Code: "BOOM"}},
		"中止": llm.AbortedFinish{Failure: llm.Failure{Message: "中止了", Code: llm.AbortedCode}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := DeriveTurnUsage([]sessionlog.Event{
				turnStartEvent(t, 0),
				stepStartEvent(t, 0, 0),
				chunkEvent(t, 0, 0, llm.UsageChunk{Usage: usage}),
				chunkEvent(t, 0, 0, llm.FinishChunk{Reason: reason}),
				stepEndEvent(t, 0, 0),
				turnEndEvent(t, 0),
			})
			if !ok {
				t.Fatal("一次失败的尝试照样计过费，该证得出来")
			}
			if got.UncachedInputTokens != 40 || got.OutputTokens != 1 {
				t.Fatalf("失败那次的账该收进来：%+v", got)
			}
			if got.Routes != nil {
				t.Fatalf("没有助手消息就说不清路由：%+v", got.Routes)
			}
		})
	}
}

// 两个步骤走同一条路由时路由表去重，走两条时按首次出现排。
func TestDeriveTurnUsageDeduplicatesRoutesInFirstSeenOrder(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 10, OutputTokens: 1}
	events := []sessionlog.Event{turnStartEvent(t, 0)}
	for step, provider := range []string{"b", "a", "b"} {
		events = append(events,
			stepStartEvent(t, 0, step),
			routedAssistantEvent(t, 0, step, provider, "m", &usage),
			stepEndEvent(t, 0, step),
		)
	}
	events = append(events, turnEndEvent(t, 0))

	got, ok := DeriveTurnUsage(events)
	if !ok {
		t.Fatal("三个步骤该证得出来")
	}
	if got.UncachedInputTokens != 30 {
		t.Fatalf("三个步骤的账都要算：%d", got.UncachedInputTokens)
	}
	want := []TurnUsageRoute{{Provider: "b", Model: "m"}, {Provider: "a", Model: "m"}}
	if len(got.Routes) != len(want) || got.Routes[0] != want[0] || got.Routes[1] != want[1] {
		t.Fatalf("路由该去重、按首次出现排：想要 %+v，实际 %+v", want, got.Routes)
	}
}

// 这一节钉的是「对残缺一点都不宽容」：少一角就整份不给，而不是给个差不多的数。
func TestDeriveTurnUsageRefusesAnIncompleteSlice(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 10, OutputTokens: 1}
	cases := map[string][]sessionlog.Event{
		"没有 turn/start": {
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
			stepEndEvent(t, 0, 0),
			turnEndEvent(t, 0),
		},
		"没有 turn/end": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
			stepEndEvent(t, 0, 0),
		},
		"一次尝试压根没报用量": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", nil),
			stepEndEvent(t, 0, 0),
			turnEndEvent(t, 0),
		},
		"一个回合都没有": {},
		"回合里一次尝试都没有": {
			turnStartEvent(t, 0),
			turnEndEvent(t, 0),
		},
		"turn/end 之后还有事件": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
			stepEndEvent(t, 0, 0),
			turnEndEvent(t, 0),
			stepStartEvent(t, 0, 1),
		},
		"两个回合": {
			turnStartEvent(t, 0),
			turnStartEvent(t, 1),
		},
		"步骤和回合对不上号": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 1, 0),
		},
		"助手消息落在一个没开过的步骤上": {
			turnStartEvent(t, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
		},
		"一次已经落定的尝试又被重开": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
			retryStartedEvent(t, 0, 0, 1),
		},
		"一次已经落定的尝试又排了重试": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &usage),
			retryEvent(t, 0, 0, 1),
		},
		"重试排在一次没报用量的尝试上": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			retryEvent(t, 0, 0, 1),
		},
		"负数记账": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m", &llm.TokenUsage{InputTokens: -1}),
			stepEndEvent(t, 0, 0),
			turnEndEvent(t, 0),
		},
		"推理比输出还多": {
			turnStartEvent(t, 0),
			stepStartEvent(t, 0, 0),
			routedAssistantEvent(t, 0, 0, "p", "m",
				&llm.TokenUsage{InputTokens: 10, OutputTokens: 2, ReasoningTokens: 3}),
			stepEndEvent(t, 0, 0),
			turnEndEvent(t, 0),
		},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, ok := DeriveTurnUsage(events); ok {
				t.Fatal("这份残缺的日志不该证得出账来")
			}
		})
	}
}

// 读不回来的负载一律整份不给：一条排不出来的事件说明这段日志根本不是它自称的样子。
func TestDeriveTurnUsageRefusesAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	if _, ok := DeriveTurnUsage([]sessionlog.Event{
		{Type: sessionlog.EventTurnStart, Data: []byte(`{"turn":"x"}`)},
	}); ok {
		t.Fatal("读不回来的 turn/start 不该证得出账来")
	}
}

// 不认得的事件类型一条都不影响这份账——一个回合里本来就混着别的东西。
func TestDeriveTurnUsageIgnoresUnrelatedEvents(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 10, OutputTokens: 1}
	got, ok := DeriveTurnUsage([]sessionlog.Event{
		turnStartEvent(t, 0),
		userEvent(t, "hello"),
		stepStartEvent(t, 0, 0),
		chunkEvent(t, 0, 0, llm.TextDeltaChunk{Index: 0, Text: "hi"}),
		routedAssistantEvent(t, 0, 0, "p", "m", &usage),
		stepEndEvent(t, 0, 0),
		headerEvent(t, simpleHeader("你是助手")),
		turnEndEvent(t, 0),
	})
	if !ok {
		t.Fatal("混着别的事件不该妨碍这份账")
	}
	if got.UncachedInputTokens != 10 || got.OutputTokens != 1 {
		t.Fatalf("账该只算那一次尝试：%+v", got)
	}
}
