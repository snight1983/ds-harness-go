// 本文件的作用：钉住两份表面折叠——服务那份逐节点的，和投影那份 O(1) 的影子价
// 折叠——在同一串事件上给出同一个总价，以及它们在失败时各自的姿势。

package tokenmeter

import (
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// foldAll 把一串事件依次折进服务那份逐节点折叠，交出最后的节点表和总价。
func foldAll(t *testing.T, events []sessionlog.Event) ([]SurfaceNode, int) {
	t.Helper()

	var nodes []SurfaceNode
	total := 0
	for _, event := range events {
		if !sessionlog.IsSurfaceEvent(event) {
			continue
		}
		fold, err := foldSurfaceTokens(nodes, event, 0)
		if err != nil {
			t.Fatalf("seq %d 折不进来：%v", event.Seq, err)
		}
		nodes, total = fold.nodes, total+fold.deltaTokens
	}
	return nodes, total
}

func TestFoldSurfaceTokensAppendsOneNodePerEvent(t *testing.T) {
	t.Parallel()

	view := newSession(userEvent(t, "hello"), assistantEvent(t, 0, 0, "hi", nil))
	nodes, total := foldAll(t, view.events)

	if len(nodes) != 2 {
		t.Fatalf("两条上表面的事件该留下两个节点，实际 %d 个", len(nodes))
	}
	if nodes[0].Seq != 0 || nodes[1].Seq != 1 {
		t.Fatalf("节点该按 seq 排：%v", nodes)
	}
	if sum := nodes[0].Tokens + nodes[1].Tokens; sum != total {
		t.Fatalf("总价该等于逐节点之和：想要 %d，实际 %d", sum, total)
	}
}

// 不上表面的事件折不进来，而且要报错——静悄悄地忽略它会让调用方以为
// 自己已经把这条记进去了。
func TestFoldSurfaceTokensRejectsAnEventThatIsNotOnTheSurface(t *testing.T) {
	t.Parallel()

	if _, err := foldSurfaceTokens(nil, stepStartEvent(t, 0, 0), 0); err == nil {
		t.Fatal("一条不上表面的事件不该折得进来")
	}
}

func TestFoldSurfaceTokensSplicesAReplacement(t *testing.T) {
	t.Parallel()

	view := newSession(userEvent(t, "aaaaaaaa"), userEvent(t, "bbbbbbbb"), userEvent(t, "c"))
	nodes, before := foldAll(t, view.events)

	replacement := replacementEvent(t, 0, 1, "s")
	replacement.Seq = 3
	fold, err := foldSurfaceTokens(nodes, replacement, 0)
	if err != nil {
		t.Fatalf("替换该折得进来：%v", err)
	}

	if len(fold.nodes) != 2 {
		t.Fatalf("两个节点换成一个之后该剩两个，实际 %d 个", len(fold.nodes))
	}
	if fold.nodes[0].Seq != 3 || fold.nodes[1].Seq != 2 {
		t.Fatalf("替换该原地占住被换那一段的位置：%v", fold.nodes)
	}
	removed := nodes[0].Tokens + nodes[1].Tokens
	if want := fold.tokens - removed; fold.deltaTokens != want {
		t.Fatalf("净变化该是新价减旧价：想要 %d，实际 %d", want, fold.deltaTokens)
	}
	if before+fold.deltaTokens >= before {
		t.Fatalf("把两段长的换成一段短的该让总价掉下来：折前 %d，净变化 %d", before, fold.deltaTokens)
	}
}

// 折叠出错时调用方手上那张节点表一个字节都不许被动过。
func TestFoldSurfaceTokensLeavesTheCallerTableUntouchedOnFailure(t *testing.T) {
	t.Parallel()

	view := newSession(userEvent(t, "hello"))
	nodes, _ := foldAll(t, view.events)

	broken := replacementEvent(t, 7, 9, "s")
	broken.Seq = 1
	if _, err := foldSurfaceTokens(nodes, broken, 0); err == nil {
		t.Fatal("表面上不存在的区间不该折得进来")
	}
	if len(nodes) != 1 || nodes[0].Seq != 0 {
		t.Fatalf("失败的折叠动了调用方的节点表：%v", nodes)
	}
}

// 一次替换指着的老节点被 FIFO 弹掉之后，这份折叠不许再报错。
//
// 这是本仓库和上游分家的地方：上游的日志一条不删，所以「端点不在表面上」只可能是
// 日志坏了。本仓库的日志会从最老的一头被弹掉一截，而压缩写的 session/replace 恰好
// 指着最老那批——也就是恰好指着先被弹掉的那批。同一条降级 sessionlog 那份表面折叠
// （surface.go 的 replacementRange）早就做了，这一份是后补的。
func TestFoldSurfaceTokensDegradesWhenTheReplacedNodesWereTrimmedAway(t *testing.T) {
	t.Parallel()

	// 这一段日志从 seq 40 起，表面上只有 40 和 41 两个节点。
	nodes := []SurfaceNode{{Seq: 40, Tokens: 11}, {Seq: 41, Tokens: 22}}

	t.Run("两端都被弹掉时降级成一次追加", func(t *testing.T) {
		t.Parallel()

		replacement := replacementEvent(t, 8, 9, "s")
		replacement.Seq = 42
		fold, err := foldSurfaceTokens(nodes, replacement, 40)
		if err != nil {
			t.Fatalf("两端都被弹掉了，该降级而不是报错：%v", err)
		}
		if len(fold.nodes) != 3 || fold.nodes[2].Seq != 42 {
			t.Fatalf("该在末尾追加一个节点、一个都不换走：%v", fold.nodes)
		}
		if fold.deltaTokens != fold.tokens {
			t.Fatalf("没有节点被换走，净变化该等于它自己的估价：想要 %d，实际 %d",
				fold.tokens, fold.deltaTokens)
		}
	})

	t.Run("只有起点被弹掉时区间收到表面最前端", func(t *testing.T) {
		t.Parallel()

		// 起点 39 已经被弹掉，终点 40 还在：现存节点的 seq 都晚于 39，所以
		// 从表面最前端切起。
		replacement := replacementEvent(t, 39, 40, "s")
		replacement.Seq = 42
		fold, err := foldSurfaceTokens(nodes, replacement, 40)
		if err != nil {
			t.Fatalf("起点被弹掉了，该降级而不是报错：%v", err)
		}
		if len(fold.nodes) != 2 || fold.nodes[0].Seq != 42 || fold.nodes[1].Seq != 41 {
			t.Fatalf("该换走 40 那一个、原地占住它的位置：%v", fold.nodes)
		}
		if want := fold.tokens - 11; fold.deltaTokens != want {
			t.Fatalf("换走的只有 40 那一格：想要 %d，实际 %d", want, fold.deltaTokens)
		}
	})

	t.Run("端点不小于起点却不在表面上，照旧报错", func(t *testing.T) {
		t.Parallel()

		// 40 和 41 都还在日志范围里，可 43 不在表面上——这不是被弹掉，
		// 是这份节点表算的根本不是当前这个表面。
		replacement := replacementEvent(t, 40, 43, "s")
		replacement.Seq = 42
		if _, err := foldSurfaceTokens(nodes, replacement, 40); err == nil {
			t.Fatal("没被弹掉却定位不到的端点该照旧报错")
		}
	})
}

// 影子价协议走完整的时候，投影那份 O(1) 折叠和服务那份逐节点折叠
// 在**每一个事件边界**上都给出同一个总价。这是整个 surfaceprojection.go
// 存在的前提，所以要逐条对。
func TestBothFoldsAgreeAtEveryEventBoundary(t *testing.T) {
	t.Parallel()

	view := newSession(
		userEvent(t, "aaaaaaaaaaaa"),
		assistantEvent(t, 0, 0, "bbbbbbbb", nil),
		userEvent(t, "cccc"),
	)
	nodes, _ := foldAll(t, view.events)
	// 影子价按服务那份折叠算出来的真实价钱写下去——压缩那边就是这么记的。
	shadowed := nodes[0].Tokens + nodes[1].Tokens
	view.append(summaryEvent(t, 0, 1, shadowed), replacementEvent(t, 0, 1, "s"))

	var serviceNodes []SurfaceNode
	serviceTotal := 0
	projectionTotal := 0
	var claim *ShadowPriceClaim

	for _, event := range view.events {
		fold, err := foldSurfaceProjection(claim, event)
		if err != nil {
			t.Fatalf("seq %d 投影侧折不进来：%v", event.Seq, err)
		}
		projectionTotal += fold.deltaTokens
		claim = fold.claim

		if sessionlog.IsSurfaceEvent(event) {
			serviceFold, err := foldSurfaceTokens(serviceNodes, event, 0)
			if err != nil {
				t.Fatalf("seq %d 服务侧折不进来：%v", event.Seq, err)
			}
			serviceNodes, serviceTotal = serviceFold.nodes, serviceTotal+serviceFold.deltaTokens
		}

		if serviceTotal != projectionTotal {
			t.Fatalf("seq %d 之后两份折叠对不上：服务侧 %d，投影侧 %d",
				event.Seq, serviceTotal, projectionTotal)
		}
	}
}

func TestShadowPriceClaimExpiresAfterOneEvent(t *testing.T) {
	t.Parallel()

	fold, err := foldSurfaceProjection(nil, summaryEvent(t, 0, 1, 40))
	if err != nil {
		t.Fatalf("摘要事件该举起认领单：%v", err)
	}
	if fold.claim == nil || fold.claim.Tokens != 40 {
		t.Fatalf("认领单没举起来：%+v", fold.claim)
	}
	if fold.deltaTokens != 0 {
		t.Fatalf("举认领单这一步自己不该产生变化：%d", fold.deltaTokens)
	}

	// 中间隔一条普通事件，认领单就该过期。
	next := userEvent(t, "x")
	next.Seq = 5
	after, err := foldSurfaceProjection(fold.claim, next)
	if err != nil {
		t.Fatalf("普通追加该折得进来：%v", err)
	}
	if after.claim != nil {
		t.Fatalf("隔了一条事件的认领单该过期：%+v", after.claim)
	}
}

// 两种失败有意做成不对称：没有认领单是老日志，折 0 放过；
// 有认领单但区间对不上是协议被用错了，报错。
func TestReplacementWithoutAClaimDriftsWhileAMismatchedClaimFails(t *testing.T) {
	t.Parallel()

	replacement := replacementEvent(t, 0, 1, "s")
	replacement.Seq = 4

	t.Run("协议落地之前的日志折 0 放过", func(t *testing.T) {
		t.Parallel()

		fold, err := foldSurfaceProjection(nil, replacement)
		if err != nil {
			t.Fatalf("没有认领单的替换不该报错：%v", err)
		}
		if fold.deltaTokens != 0 || fold.claim != nil {
			t.Fatalf("该原地不动：%+v", fold)
		}
	})

	t.Run("区间对不上的认领单当场报错", func(t *testing.T) {
		t.Parallel()

		_, err := foldSurfaceProjection(&ShadowPriceClaim{Start: 0, End: 9, Tokens: 40}, replacement)
		if err == nil {
			t.Fatal("认领的区间和替换声明的对不上该报错")
		}
	})
}

// 宽容那一面把两种失败合并成同一种降级：这一笔不记、认领单放掉。
func TestLenientFoldDegradesInsteadOfFailing(t *testing.T) {
	t.Parallel()

	replacement := replacementEvent(t, 0, 1, "s")
	replacement.Seq = 4

	fold := foldSurfaceProjectionLenient(&ShadowPriceClaim{Start: 0, End: 9, Tokens: 40}, replacement)
	if fold.deltaTokens != 0 || fold.claim != nil {
		t.Fatalf("出错时该降级成什么都不记：%+v", fold)
	}

	// 能折的照样照折，宽容不等于什么都不干。
	appended := userEvent(t, "hello")
	appended.Seq = 1
	if got := foldSurfaceProjectionLenient(nil, appended); got.deltaTokens <= 0 {
		t.Fatalf("一次普通追加该照常记账：%+v", got)
	}
}
