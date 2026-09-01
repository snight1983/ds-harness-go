// 本文件的作用：钉住「持久化编排器**就是**一个 agent 工厂能接的会话持久化」这件事。
//
// 为什么单独拿一个文件来钉：这两个模块各自都有厚实的测试，但它们之间那道接缝
// 一个断言都没有——于是 [Coordinator.Prepare] 交回 `*coresession.Preparation`、
// 而 agentloop 那边声明的是 `*coresession.Session`，两边各自绿了很久，装配的人
// 才在编译期撞上。这不是文档问题，是两个已实现的模块装不到一起。
//
// 下面那句 var 就是那个缺掉的断言，后面的用例把这条接缝真的走一遍：存档里有东西
// → 列得出来 → 准备得出来 → 发布之后接着写 → 退场 → 再准备一次读得回刚写的。
package persistence

import (
	"context"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agentloop"
	"github.com/snight1983/ds-harness-go/session"
)

// 编译期确认这个编排器直接就能当 agent 工厂的会话持久化用，不需要中间再垫一层壳。
var _ agentloop.SessionPersistence = (*Coordinator)(nil)

// TestCoordinatorDrivesTheAgentLoopSeam 把续跑要走的那条路整个跑一遍。
//
// 全程只经由 [agentloop.SessionPersistence] 这个接口调用，不碰 [Coordinator] 上
// 别的方法——这样断言的是「工厂能拿到的那点能力够不够用」，而不是「编排器行不行」。
func TestCoordinatorDrivesTheAgentLoopSeam(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("续跑接缝")
	h.backend.seed(testHeader(t, id), []session.Event{
		turnStart(t, 0, 1), userEvent(t, 1, "重启之前说的话"), turnEnd(t, 2, 1),
	}, nil)

	var persistence agentloop.SessionPersistence = h.Coordinator

	headers, err := persistence.List(t.Context())
	if err != nil {
		t.Fatalf("列存档失败：%v", err)
	}
	// 工厂拿这一条区分「这个存档根本不存在」和「读它的时候出事了」，
	// 列不出来它就会把一段真实的历史当成新会话盖掉。
	if len(headers) != 1 || headers[0].ID != id {
		t.Fatalf("该列出那一个落地的会话，实际 %v", headers)
	}

	preparation, err := persistence.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	live := preparation.Session()
	// 数用户消息而不是数总条数：编排器会在续跑那份视图后面补一条
	// `session/end-seed` 边界，那是它的事，接缝这边不该钉死它。
	if got := countUserMessages(live.Events()); got != 1 {
		t.Fatalf("准备出来的会话该带着存档里那一条用户消息，实际 %d 条", got)
	}

	// 工厂那边是 setupAndPublish：先发布，再无条件释放准备期。
	leave, err := h.sessions.Enter(h.owner, live)
	if err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if err := h.sessions.Announce(t.Context(), live); err != nil {
		t.Fatalf("公布失败：%v", err)
	}
	preparation.Release()

	if _, err := live.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      userEvent(t, 0, "重启之后说的话").Data,
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		t.Fatalf("续跑之后追加失败：%v", err)
	}
	h.settle(t, live)

	// 退场，模拟这个 agent 被拆掉。
	if err := leave(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("退场失败：%v", err)
	}

	// 再走一遍同一条路：这次该读回续跑期间写的那一条。释放没还回预留的话，
	// 这里会卡在「会话还活着」或者一直等下去。
	again, err := persistence.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("第二次准备失败：%v", err)
	}
	defer again.Release()

	if got := countUserMessages(again.Session().Events()); got != 2 {
		t.Fatalf("第二次准备该读回两条用户消息（含续跑期间写的那条），实际 %d 条", got)
	}
}

// countUserMessages 数一份日志里的用户消息。
func countUserMessages(events []session.Event) int {
	count := 0
	for _, event := range events {
		if event.Type == session.EventUserMessage {
			count++
		}
	}
	return count
}

// TestCoordinatorReleaseReturnsTheReservation 钉住释放真的把身份还了回去。
//
// 这是工厂那边所有释放动作的**唯一**理由。不还，一次半路失败的续跑就把这个
// 会话身份永久扣住：之后每一次同名续跑都撞「还活着」或者一直等，而现场只看得到
// 「卡住了」。
func TestCoordinatorReleaseReturnsTheReservation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("放弃了的准备")
	h.backend.seed(testHeader(t, id), []session.Event{
		turnStart(t, 0, 1), userEvent(t, 1, "甲"), turnEnd(t, 2, 1),
	}, nil)

	var persistence agentloop.SessionPersistence = h.Coordinator

	preparation, err := persistence.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	// 一次没走到发布就放弃的续跑。
	preparation.Release()
	// 幂等：工厂那边 defer 和显式释放会同时够到同一段准备期。
	preparation.Release()

	again, err := persistence.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("放弃之后该还能再准备一次，实际：%v", err)
	}
	defer again.Release()

	if again.Session().ID() != id {
		t.Fatalf("再准备出来的身份是 %q", string(again.Session().ID()))
	}
}
