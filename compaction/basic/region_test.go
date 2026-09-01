// 本文件的作用：验挑区间那一刀落在该落的地方（既够保留预算、又不劈开一次工具调用），
// 以及从日志尾巴上读出来的那三件事没有读错。

package basic

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/session"
)

func TestSelectCompactableRange挑到保留预算为止(t *testing.T) {
	t.Parallel()

	view := viewOf(
		userText(t, 1, "一"), userText(t, 2, "二"),
		userText(t, 3, "三"), userText(t, 4, "四"),
	)
	// 每个节点 10，保留 25：从尾巴往回攒 4、3、2 三个才够，于是留下的是 2 起，
	// 压掉的是 1 这一个。
	got, ok, err := SelectCompactableRange(view, &compaction.BalanceIndex{}, pricedAll(view, 10), 25)
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !ok {
		t.Fatal("说没有可压的区间")
	}
	if got != (compaction.ShadowedRange{Start: 1, End: 1}) {
		t.Fatalf("挑出来的是 %+v", got)
	}
}

func TestSelectCompactableRange保留预算为零也留下最后一个节点(t *testing.T) {
	t.Parallel()

	// 超窗补救那一路会把保留预算压到 0。整个表面都压掉的话，替换消息之后表面上
	// 只剩一条摘要，模型收到的历史里连最后一次交互都没有了。
	view := viewOf(userText(t, 1, "一"), userText(t, 2, "二"), userText(t, 3, "三"))
	got, ok, err := SelectCompactableRange(view, &compaction.BalanceIndex{}, pricedAll(view, 10), 0)
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !ok || got != (compaction.ShadowedRange{Start: 1, End: 2}) {
		t.Fatalf("挑出来的是 %+v（有没有：%v）", got, ok)
	}
}

func TestSelectCompactableRange往前退到配平的下刀点(t *testing.T) {
	t.Parallel()

	// 表面：文本、两次调用的助手消息、两条结果、文本。
	// 按预算算下来那一刀会落在两条工具结果中间，那样模型会收到一条没有对应调用的
	// 结果，所以要一直往前退到助手消息之前。
	view := viewOf(
		userText(t, 1, "一"),
		assistantCalls(t, 2, 2),
		toolResult(t, 3, 0),
		toolResult(t, 4, 1),
		userText(t, 5, "五"),
	)
	got, ok, err := SelectCompactableRange(view, &compaction.BalanceIndex{}, pricedAll(view, 10), 15)
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !ok {
		t.Fatal("说没有可压的区间")
	}
	if got != (compaction.ShadowedRange{Start: 1, End: 1}) {
		t.Fatalf("挑出来的是 %+v", got)
	}
}

func TestSelectCompactableRange挑不出来的几种(t *testing.T) {
	t.Parallel()

	balanced := viewOf(userText(t, 1, "一"), userText(t, 2, "二"))
	// 一整条助手消息带一次调用、结果在最后：中间任何一刀都不配平，
	// 一路退到表面头，等于什么都不压。
	unbalanced := viewOf(assistantCalls(t, 1, 1), toolResult(t, 2, 0))

	for name, item := range map[string]struct {
		view   compaction.SurfaceView
		priced []PricedNode
		retain int
	}{
		"表面是空的":       {compaction.SurfaceView{}, nil, 100},
		"整个表面都在保留预算里": {balanced, pricedAll(balanced, 10), 1000},
		"往前找不到配平的下刀点": {unbalanced, pricedAll(unbalanced, 10), 5},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := SelectCompactableRange(
				item.view, &compaction.BalanceIndex{}, item.priced, item.retain)
			if err != nil {
				t.Fatalf("报了：%v", err)
			}
			// 这三种都不是错误，只是这一轮没得压。
			if ok || got != (compaction.ShadowedRange{}) {
				t.Fatalf("挑出来的是 %+v（有没有：%v）", got, ok)
			}
		})
	}
}

func TestSelectCompactableRange计价和表面对不上就拒(t *testing.T) {
	t.Parallel()

	view := viewOf(userText(t, 1, "一"), userText(t, 2, "二"))

	for name, priced := range map[string][]PricedNode{
		"个数对不上":   {{Seq: 1, Tokens: 10}},
		"seq 对不上": {{Seq: 1, Tokens: 10}, {Seq: 7, Tokens: 10}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// 计量器算的是另一份表面，这时候按谁的顺序下刀都是错的。
			_, _, err := SelectCompactableRange(view, &compaction.BalanceIndex{}, priced, 5)
			if !errors.Is(err, compaction.ErrSurfaceCorrupt) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestSelectCompactableRange配平算不出来时把错误交出去(t *testing.T) {
	t.Parallel()

	// 一条负载读不回来的助手消息：配平那一侧算不出这一刀，错误要原样上交，
	// 不能当成「这一刀不配平」继续往前退。
	broken := eventAt(2, session.EventAssistantMessage, json.RawMessage(`{"turn":"一"}`))
	view := viewOf(userText(t, 1, "一"), broken, userText(t, 3, "三"))

	if _, _, err := SelectCompactableRange(
		view, &compaction.BalanceIndex{}, pricedAll(view, 10), 5); err == nil {
		t.Fatal("没报")
	}
}

func TestInspectEntryState读出三件事(t *testing.T) {
	t.Parallel()

	state, err := InspectEntryState([]session.Event{
		endSeed(1),
		compactionStart(t, 2, 1),
		turnStart(t, 3, 7),
	})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if !state.TurnIsOpen || state.OpenTurn != 7 {
		t.Fatalf("回合读成了 %+v", state)
	}
	if !state.HasUnmatchedStart || state.UnmatchedStartSeq != 2 {
		t.Fatalf("压缩括号读成了 %+v", state)
	}
	if !state.HasEndSeed || state.LatestEndSeedSeq != 1 {
		t.Fatalf("种子边界读成了 %+v", state)
	}
}

func TestInspectEntryState三件事各自独立地停(t *testing.T) {
	t.Parallel()

	// 回合已经关了、压缩括号也已经关了，再往前的那些不算数；
	// 而种子边界还没读到，得一直扫到日志头。
	state, err := InspectEntryState([]session.Event{
		endSeed(1),
		turnStart(t, 2, 1),
		compactionStart(t, 3, 1),
		compactionEnd(t, 4, 1),
		turnEnd(5),
	})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if state.TurnIsOpen {
		t.Fatalf("已经关掉的回合读成了开着：%+v", state)
	}
	if state.HasUnmatchedStart {
		t.Fatalf("已经配对的压缩括号读成了开着：%+v", state)
	}
	if !state.HasEndSeed || state.LatestEndSeedSeq != 1 {
		t.Fatalf("种子边界读成了 %+v", state)
	}
}

func TestInspectEntryState三样都定下来就提前收工(t *testing.T) {
	t.Parallel()

	// 头上那条 turn/start 的负载是坏的。三样在它之前就都定下来了，扫不到它，
	// 所以这一趟不该报错——这正是那句提前收工在做的事。
	broken := logEventAt(1, session.EventTurnStart, json.RawMessage(`{"turn":"一"}`))
	state, err := InspectEntryState([]session.Event{
		broken,
		endSeed(2),
		compactionEnd(t, 3, 1),
		turnEnd(4),
	})
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if state.TurnIsOpen || state.HasUnmatchedStart || !state.HasEndSeed {
		t.Fatalf("读出来的是 %+v", state)
	}
}

func TestInspectEntryState回合号读不回来就报(t *testing.T) {
	t.Parallel()

	broken := logEventAt(1, session.EventTurnStart, json.RawMessage(`{"turn":"一"}`))
	if _, err := InspectEntryState([]session.Event{broken}); !errors.Is(err, compaction.ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCheckNoActiveCompaction(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		events []session.Event
		busy   bool
	}{
		"没有开着的括号": {[]session.Event{turnStart(t, 1, 1)}, false},
		"括号还开着":   {[]session.Event{compactionStart(t, 1, 1)}, true},
		"括号已经配对":  {[]session.Event{compactionStart(t, 1, 1), compactionEnd(t, 2, 1)}, false},
		// 种子边界之前的日志是继承来的，那边的括号在这一侧永远等不到它的 end，
		// 不该把当前这个会话一直锁着。
		"开着的括号在种子边界之前": {[]session.Event{compactionStart(t, 1, 1), endSeed(2)}, false},
		"开着的括号在种子边界之后": {[]session.Event{endSeed(1), compactionStart(t, 2, 1)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := CheckNoActiveCompaction(item.events, "开工前")
			if !item.busy {
				if err != nil {
					t.Fatalf("拦了：%v", err)
				}
				return
			}
			var manual *compaction.ManualError
			if !errors.As(err, &manual) {
				t.Fatalf("报的是 %v", err)
			}
			// 上层照着这个码写提示语，所以它必须是「忙」而不是别的。
			if manual.Code != compaction.ManualErrorBusy {
				t.Fatalf("码是 %v", manual.Code)
			}
		})
	}
}

func TestCheckNoActiveCompaction日志读不回来时把错误交出去(t *testing.T) {
	t.Parallel()

	broken := logEventAt(1, session.EventTurnStart, json.RawMessage(`{"turn":"一"}`))
	if err := CheckNoActiveCompaction([]session.Event{broken}, "开工前"); !errors.Is(
		err, compaction.ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
}
