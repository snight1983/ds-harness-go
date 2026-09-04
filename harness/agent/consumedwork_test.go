package agent

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/llm"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// turnStart 造一条回合开始事件。
func turnStart(t *testing.T, turn int) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: data(t, sessionlog.TurnStartData{Turn: turn}),
	}
}

// stepStart 造一条步骤开始事件。
func stepStart(t *testing.T, turn, step int) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventStepStart,
		Data: data(t, sessionlog.StepStartData{Turn: turn, Step: step}),
	}
}

// turnEnd 造一条回合结束事件。
func turnEnd(t *testing.T, turn int, reason sessionlog.TurnEndReason) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventTurnEnd,
		Data: data(t, sessionlog.TurnEndData{Turn: turn, Reason: reason}),
	}
}

// spliced 造一条收件箱改动事件。
func spliced(t *testing.T, splice SplicedData) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{Type: EventInboxSpliced, Data: data(t, splice)}
}

// claim 造一条「认领」形状的改动：纯删除，不打取消标记。
func claim(t *testing.T, count int) sessionlog.Event {
	t.Helper()
	return spliced(t, SplicedData{Target: NextTurn, Start: 0, RemovedCount: count})
}

// cancel 造一条「取消」形状的改动：纯删除，打取消标记。
func cancel(t *testing.T, count int) sessionlog.Event {
	t.Helper()
	return spliced(t, SplicedData{Target: NextTurn, Start: 0, RemovedCount: count, Canceled: true})
}

// TestFoldConsumedWorkEmpty 一段空日志什么都没消耗。
func TestFoldConsumedWorkEmpty(t *testing.T) {
	work := FoldConsumedWork(nil)
	if work.HasEnd || work.DroppedUnrun {
		t.Fatalf("空日志不该交代出任何消耗：%+v", work)
	}
}

// TestFoldConsumedWorkSteppedTurn 一个进过模型步骤的回合，它那条 turn/end 就是
// 交代——哪怕它是 completed 结束的。
func TestFoldConsumedWorkSteppedTurn(t *testing.T) {
	events := seqOf(
		turnStart(t, 1),
		stepStart(t, 1, 1),
		turnEnd(t, 1, sessionlog.CompletedTurnEnd{}),
	)
	work := FoldConsumedWork(events)
	if !work.HasEnd || work.End.Seq != 2 {
		t.Fatalf("该认下那条 turn/end：%+v", work)
	}
	if work.DroppedUnrun {
		t.Fatal("没有取消，不该报丢弃")
	}
}

// TestFoldConsumedWorkClaimWithoutStep 一个认领了输入却一个步骤都没进的回合：
// completed 不算交代，别的理由都算。
//
// 源: packages/core/agent/src/consumed-work.ts:33-58
func TestFoldConsumedWorkClaimWithoutStep(t *testing.T) {
	cases := map[string]struct {
		reason    sessionlog.TurnEndReason
		accounted bool
	}{
		"completed 不算":  {sessionlog.CompletedTurnEnd{}, false},
		"blocked 算":     {sessionlog.BlockedTurnEnd{}, true},
		"aborted 算":     {sessionlog.AbortedTurnEnd{Reason: sessionlog.UserCancel{}}, true},
		"error 算":       {sessionlog.ErrorTurnEnd{}, true},
		"interrupted 算": {sessionlog.InterruptedTurnEnd{}, true},
		"说不出名字的结局也算": {
			sessionlog.UnknownTurnEnd{
				Kind: "vanished",
				Raw:  json.RawMessage(`{"kind":"vanished"}`),
			},
			true,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			events := seqOf(
				turnStart(t, 1),
				claim(t, 1),
				turnEnd(t, 1, testCase.reason),
			)
			work := FoldConsumedWork(events)
			if work.HasEnd != testCase.accounted {
				t.Fatalf("交代与否不对：HasEnd=%v", work.HasEnd)
			}
		})
	}
}

// TestFoldConsumedWorkEmptyTurnAccountsForNothing 一个既没进步骤也没认领的空回合
// 什么都交代不了——这正是回合词汇自己回答不了的那件事。
func TestFoldConsumedWorkEmptyTurnAccountsForNothing(t *testing.T) {
	events := seqOf(
		turnStart(t, 1),
		turnEnd(t, 1, sessionlog.BlockedTurnEnd{}),
	)
	if work := FoldConsumedWork(events); work.HasEnd {
		t.Fatalf("空回合不该被当成交代：%+v", work)
	}
}

// TestFoldConsumedWorkCancelAfterTurn 回合关掉**之后**的那次取消才是没人认的。
func TestFoldConsumedWorkCancelAfterTurn(t *testing.T) {
	events := seqOf(
		turnStart(t, 1),
		stepStart(t, 1, 1),
		turnEnd(t, 1, sessionlog.CompletedTurnEnd{}),
		cancel(t, 1),
	)
	work := FoldConsumedWork(events)
	if !work.HasEnd || !work.DroppedUnrun {
		t.Fatalf("回合之后那次取消该报丢弃：%+v", work)
	}
}

// TestFoldConsumedWorkCancelBeforeTurnIsAccounted 回合关掉之前丢的东西由那个回合
// 自己的结局交代，不该再报一次。
func TestFoldConsumedWorkCancelBeforeTurnIsAccounted(t *testing.T) {
	events := seqOf(
		cancel(t, 1),
		turnStart(t, 1),
		stepStart(t, 1, 1),
		turnEnd(t, 1, sessionlog.CompletedTurnEnd{}),
	)
	if work := FoldConsumedWork(events); work.DroppedUnrun {
		t.Fatalf("回合之前那次取消已经被交代掉了：%+v", work)
	}
}

// TestFoldConsumedWorkCancelWithoutTurn 一次「在任何回合开起来之前就把输入拿走」的
// 取消，没有任何 turn/end 描述得了它——这是 DroppedUnrun 存在的全部理由。
func TestFoldConsumedWorkCancelWithoutTurn(t *testing.T) {
	work := FoldConsumedWork(seqOf(cancel(t, 2)))
	if work.HasEnd {
		t.Fatal("没有回合，不该有 End")
	}
	if !work.DroppedUnrun {
		t.Fatal("该报丢弃")
	}
}

// TestFoldConsumedWorkReplaceIsNotDropped 一次**替换**把活儿以新身份留在了待办里，
// 所以它不算丢。
func TestFoldConsumedWorkReplaceIsNotDropped(t *testing.T) {
	events := seqOf(spliced(t, SplicedData{
		Target:       NextTurn,
		RemovedCount: 1,
		Inserted:     []llm.Message{text("换过的")},
		Canceled:     true,
	}))
	if work := FoldConsumedWork(events); work.DroppedUnrun {
		t.Fatalf("替换不算丢：%+v", work)
	}
}

// TestFoldConsumedWorkPureInsertIsNotDropped 一次纯插入（RemovedCount 为 0）
// 什么都没消耗。
func TestFoldConsumedWorkPureInsertIsNotDropped(t *testing.T) {
	events := seqOf(spliced(t, SplicedData{
		Target:   NextTurn,
		Inserted: []llm.Message{text("新来的")},
		Canceled: true,
	}))
	if work := FoldConsumedWork(events); work.DroppedUnrun {
		t.Fatalf("纯插入不该报丢弃：%+v", work)
	}
}

// TestFoldConsumedWorkClaimOutsideTurn 一次落在任何回合之外的纯删除既不算认领
// 也不算取消——认领永远发生在一个回合里面。
func TestFoldConsumedWorkClaimOutsideTurn(t *testing.T) {
	events := seqOf(
		claim(t, 1),
		turnStart(t, 1),
		turnEnd(t, 1, sessionlog.BlockedTurnEnd{}),
	)
	work := FoldConsumedWork(events)
	if work.HasEnd || work.DroppedUnrun {
		t.Fatalf("回合外那次删除不该被记成任何东西：%+v", work)
	}
}

// TestFoldConsumedWorkKeepsLastAccountingTurn 有多个交代得了的回合时，认的是
// 最后那一个。
func TestFoldConsumedWorkKeepsLastAccountingTurn(t *testing.T) {
	events := seqOf(
		turnStart(t, 1),
		stepStart(t, 1, 1),
		turnEnd(t, 1, sessionlog.CompletedTurnEnd{}),
		turnStart(t, 2),
		stepStart(t, 2, 1),
		turnEnd(t, 2, sessionlog.CompletedTurnEnd{}),
	)
	work := FoldConsumedWork(events)
	if !work.HasEnd || work.End.Seq != 5 {
		t.Fatalf("该认最后那个回合：%+v", work)
	}
}

// TestFoldConsumedWorkSkipsUnreadablePayloads 负载读不回来的事件被跳过而不是
// 让整趟折叠失败——这个折叠是一次尽力而为的交代，调用它的地方没有「拒绝这段
// 日志」这个选项。
func TestFoldConsumedWorkSkipsUnreadablePayloads(t *testing.T) {
	broken := json.RawMessage(`"不是个对象"`)
	events := seqOf(
		sessionlog.Event{Type: sessionlog.EventTurnStart, Data: broken},
		sessionlog.Event{Type: sessionlog.EventStepStart, Data: broken},
		sessionlog.Event{Type: EventInboxSpliced, Data: broken},
		sessionlog.Event{Type: sessionlog.EventTurnEnd, Data: broken},
		turnStart(t, 1),
		stepStart(t, 1, 1),
		turnEnd(t, 1, sessionlog.CompletedTurnEnd{}),
	)
	work := FoldConsumedWork(events)
	if !work.HasEnd || work.End.Seq != 6 {
		t.Fatalf("坏负载该被跳过，好的那个回合该认下来：%+v", work)
	}
}

// seqOf 把一串事件按下标盖上 seq。
func seqOf(events ...sessionlog.Event) []sessionlog.Event {
	numbered := make([]sessionlog.Event, len(events))
	for index, event := range events {
		event.Seq = index
		numbered[index] = event
	}
	return numbered
}
