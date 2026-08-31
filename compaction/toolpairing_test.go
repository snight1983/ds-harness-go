// 本文件的作用：验工具配对平衡算得准——哪一刀切下去不会把一次调用和它的结果劈开，
// 以及那份增量状态在表面变长、被替换、换了一份状态去对的时候都不会算错。

package compaction

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestBalance一段没有工具调用的表面处处配平(t *testing.T) {
	t.Parallel()

	view := viewOf(1, userText(t, 0, "你好"), userText(t, 1, "再问一句"))
	var index BalanceIndex

	for _, seq := range view.Nodes {
		before, err := index.BalancedBefore(view, seq)
		if err != nil {
			t.Fatalf("seq %d 前面那一刀算不出来：%v", seq, err)
		}
		after, err := index.BalancedAfter(view, seq)
		if err != nil {
			t.Fatalf("seq %d 后面那一刀算不出来：%v", seq, err)
		}
		if !before || !after {
			t.Fatalf("seq %d：前 %v 后 %v，都该是配平的", seq, before, after)
		}
	}
}

func TestBalance调用和结果之间那一刀不配平(t *testing.T) {
	t.Parallel()

	// 0 用户 / 1 助手（一次调用）/ 2 工具结果 / 3 用户
	view := viewOf(1,
		userText(t, 0, "查一下"),
		assistantCalls(t, 1, 1),
		toolResult(t, 2),
		userText(t, 3, "谢谢"),
	)
	var index BalanceIndex

	for _, item := range []struct {
		seq    int
		after  bool
		reason string
	}{
		{0, true, "用户消息之后还没有调用开着"},
		{1, false, "调用开着，结果还在后面"},
		{2, true, "结果到了，调用收口"},
		{3, true, "表面尾巴之后"},
	} {
		got, err := index.BalancedAfter(view, item.seq)
		if err != nil {
			t.Fatalf("seq %d 算不出来：%v", item.seq, err)
		}
		if got != item.after {
			t.Fatalf("seq %d 之后那一刀算成 %v，要的是 %v（%s）", item.seq, got, item.after, item.reason)
		}
	}

	// 「某个节点之前那一刀」就是「上一个节点之后那一刀」。
	before, err := index.BalancedBefore(view, 2)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if before {
		t.Fatal("工具结果之前那一刀被算成配平的了")
	}
}

func TestBalance一条消息里的多次调用要等结果凑齐(t *testing.T) {
	t.Parallel()

	// 数的是**内容块**，所以一条带两次调用的助手消息要两条结果才收口。
	view := viewOf(1,
		assistantCalls(t, 0, 2),
		toolResult(t, 1),
		toolResult(t, 2),
	)
	var index BalanceIndex

	for seq, want := range map[int]bool{0: false, 1: false, 2: true} {
		got, err := index.BalancedAfter(view, seq)
		if err != nil {
			t.Fatalf("seq %d 算不出来：%v", seq, err)
		}
		if got != want {
			t.Fatalf("seq %d 之后那一刀算成 %v，要的是 %v", seq, got, want)
		}
	}
}

func TestBalance表面变长只往前折不重建(t *testing.T) {
	t.Parallel()

	first := viewOf(1, assistantCalls(t, 0, 1))
	var index BalanceIndex
	if got, err := index.BalancedAfter(first, 0); err != nil || got {
		t.Fatalf("第一次：got=%v err=%v", got, err)
	}

	grown := viewOf(1, assistantCalls(t, 0, 1), toolResult(t, 1))
	got, err := index.BalancedAfter(grown, 1)
	if err != nil {
		t.Fatalf("接着折算不出来：%v", err)
	}
	if !got {
		t.Fatal("结果到了却还算成不配平")
	}
	// 前面那一段的结论不该被这次追加改掉。
	if again, err := index.BalancedAfter(grown, 0); err != nil || again {
		t.Fatalf("旧结论变了：got=%v err=%v", again, err)
	}
}

func TestBalance代数变了就整个重建(t *testing.T) {
	t.Parallel()

	before := viewOf(1, assistantCalls(t, 0, 1), toolResult(t, 1), userText(t, 2, "继续"))
	var index BalanceIndex
	if _, err := index.BalancedAfter(before, 2); err != nil {
		t.Fatalf("算不出来：%v", err)
	}

	// 一次替换把前两个节点换成一条带调用的摘要，节点数没变、代数加了一。
	// 拿旧状态去对新表面，必须整个重算而不是接着往前折。
	after := SurfaceView{
		Nodes:      []int{3, 2},
		Generation: 2,
		Events:     []session.Event{userText(t, 2, "继续"), assistantCalls(t, 3, 1)},
		BaseSeq:    2,
	}

	got, err := index.BalancedAfter(after, 3)
	if err != nil {
		t.Fatalf("重建之后算不出来：%v", err)
	}
	if got {
		t.Fatal("重建之后那次调用没被数进去")
	}
}

func TestBalance表面缩短了也整个重建(t *testing.T) {
	t.Parallel()

	long := viewOf(1, assistantCalls(t, 0, 1), toolResult(t, 1), userText(t, 2, "继续"))
	var index BalanceIndex
	if _, err := index.BalancedAfter(long, 2); err != nil {
		t.Fatalf("算不出来：%v", err)
	}

	// 已处理的节点比表面还多，说明前面那一段被替换掉了。代数没变也要重建。
	short := viewOf(1, assistantCalls(t, 0, 1))
	got, err := index.BalancedAfter(short, 0)
	if err != nil {
		t.Fatalf("重建之后算不出来：%v", err)
	}
	if got {
		t.Fatal("重建之后那次调用没被数进去")
	}
	if _, err := index.BalancedAfter(short, 1); !errors.Is(err, ErrSurfaceCorrupt) {
		t.Fatalf("已经不在表面上的 seq 却查得到：%v", err)
	}
}

func TestBalance一份状态可以拿去对另一份表面(t *testing.T) {
	t.Parallel()

	// DSH 那边这份状态挂在以 Session 为键的 WeakMap 上。这里是个普通的值，
	// 拿去对另一份表面也不会算错——代数对不上就整个重建。
	var index BalanceIndex
	first := viewOf(1, assistantCalls(t, 0, 1))
	if _, err := index.BalancedAfter(first, 0); err != nil {
		t.Fatalf("算不出来：%v", err)
	}

	other := viewOf(7, userText(t, 0, "另一个会话"))
	got, err := index.BalancedAfter(other, 0)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if !got {
		t.Fatal("换了一份表面还带着上一份的账")
	}
}

func TestBalance表面上的seq在日志里找不到(t *testing.T) {
	t.Parallel()

	for name, view := range map[string]SurfaceView{
		"基准之前": {
			Nodes: []int{2}, Generation: 1,
			Events: []session.Event{userText(t, 5, "只拿到一段后缀")}, BaseSeq: 5,
		},
		"越过尾巴": {
			Nodes: []int{0, 1}, Generation: 1,
			Events: []session.Event{userText(t, 0, "只有一条")}, BaseSeq: 0,
		},
		"下标对上了但 seq 不对": {
			Nodes: []int{0}, Generation: 1,
			Events: []session.Event{userText(t, 3, "错位")}, BaseSeq: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var index BalanceIndex
			if _, err := index.BalancedAfter(view, view.Nodes[0]); !errors.Is(err, ErrSurfaceCorrupt) {
				t.Fatalf("BalancedAfter 报的是 %v", err)
			}
			var before BalanceIndex
			if _, err := before.BalancedBefore(view, view.Nodes[0]); !errors.Is(err, ErrSurfaceCorrupt) {
				t.Fatalf("BalancedBefore 报的是 %v", err)
			}
		})
	}
}

func TestBalance工具结果没有在先的调用(t *testing.T) {
	t.Parallel()

	view := viewOf(1, toolResult(t, 0))
	var index BalanceIndex
	if _, err := index.BalancedAfter(view, 0); !errors.Is(err, ErrSurfaceCorrupt) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestBalance坏掉的追加不会留下折了一半的状态(t *testing.T) {
	t.Parallel()

	// 先折进去一段好的。
	good := viewOf(1, assistantCalls(t, 0, 1), toolResult(t, 1))
	var index BalanceIndex
	if _, err := index.BalancedAfter(good, 1); err != nil {
		t.Fatalf("算不出来：%v", err)
	}

	// 再接一段读不回来的尾巴。
	broken := viewOf(1,
		assistantCalls(t, 0, 1), toolResult(t, 1),
		logEventAt(2, session.EventAssistantMessage, json.RawMessage(`{`)),
	)
	broken.Nodes = []int{0, 1, 2}
	if _, err := index.BalancedAfter(broken, 2); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}

	// 状态必须还停在坏掉之前那一步，不能是一个既不是旧的也不是新的中间态。
	if got, err := index.BalancedAfter(good, 1); err != nil || !got {
		t.Fatalf("旧结论坏了：got=%v err=%v", got, err)
	}
	if _, err := index.BalancedAfter(good, 2); !errors.Is(err, ErrSurfaceCorrupt) {
		t.Fatalf("坏掉的那个节点被留在状态里了：%v", err)
	}
}

func TestBalance表面尾巴之后那一刀(t *testing.T) {
	t.Parallel()

	// N 个节点的表面有 N+1 刀，最后一项是尾巴之后那一刀。
	view := viewOf(1, userText(t, 0, "你好"))
	var index BalanceIndex
	got, err := index.BalancedAfter(view, 0)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if !got {
		t.Fatal("尾巴之后那一刀该是配平的")
	}
}

func TestEventDelta只认助手消息和工具结果(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		event session.Event
		want  int
	}{
		"两次调用的助手消息": {assistantCalls(t, 0, 2), 2},
		"没有调用的助手消息": {assistantCalls(t, 0, 0), 0},
		"工具结果":      {toolResult(t, 0), -1},
		"用户消息":      {userText(t, 0, "你好"), 0},
		"回合边界":      {turnStart(t, 0, 1), 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := eventDelta(item.event)
			if err != nil {
				t.Fatalf("算不出来：%v", err)
			}
			if got != item.want {
				t.Fatalf("算成 %d，要的是 %d", got, item.want)
			}
		})
	}
}

func TestEventDelta助手消息读不回来(t *testing.T) {
	t.Parallel()

	event := eventAt(0, session.EventAssistantMessage, json.RawMessage(`{"message":42}`))
	if _, err := eventDelta(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestBalance非工具内容块不算数(t *testing.T) {
	t.Parallel()

	// 数的是 ToolCallBlock，别的块（文本、推理）不动这个数。
	event := eventAt(0, session.EventAssistantMessage, marshalPayload(t, session.AssistantMessageData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID:   "a",
			Role: llm.RoleAssistant,
			Content: llm.Content{
				llm.TextBlock{Text: "先想一下"},
				llm.ToolCallBlock{ID: "call-a", Name: "read", Arguments: `{}`},
				llm.TextBlock{Text: "顺便说一句"},
			},
			Source: llm.ModelSource{},
		},
	}))
	got, err := eventDelta(event)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if got != 1 {
		t.Fatalf("数成 %d 次调用", got)
	}
}
