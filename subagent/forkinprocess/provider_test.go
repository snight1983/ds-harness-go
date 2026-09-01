// 本文件的作用：把 fork 提供方钉在它那一条真逻辑上——那段回合完整的前缀切到哪里，
// 以及这段前缀确实作为种子到达了 agent 造法和可续创建那两条路。

package forkinprocess

import (
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/internal/providertest"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

func TestNewFallsBackToTheDefaultProviderName(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		given string
		want  string
	}{
		"空串取默认值": {given: "", want: "fork"},
		"给了就用给的": {given: "fork-2", want: "fork-2"},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := New(each.given, providertest.New(t).Services()).Name(); got != each.want {
				t.Fatalf("名字该是 %q，实际 %q", each.want, got)
			}
		})
	}
}

func TestProviderAnnouncesEveryStartTimeCapability(t *testing.T) {
	t.Parallel()

	provider := New("", providertest.New(t).Services())
	want := subagent.Capabilities{OutputSchema: true, DepthLimit: true, ToolFilter: true, Persona: true}
	if got := provider.Capabilities(); got != want {
		t.Fatalf("能力该是 %+v，实际 %+v", want, got)
	}
	if !provider.InheritsParentContext() {
		t.Fatal("fork 出来的孩子该继承父上下文")
	}
}

// 前缀止于最后一条 turn/end：在飞的那个回合不配对，拿它当孩子会话重放不合法。
func TestCompletedTurnPrefixStopsAtTheLastCompletedTurn(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		arrange func(*testing.T, *providertest.StubAgent)
		want    int
	}{
		"一个回合都没有": {
			arrange: func(*testing.T, *providertest.StubAgent) {},
			want:    0,
		},
		"只有一个在飞的回合": {
			arrange: func(t *testing.T, parent *providertest.StubAgent) { parent.AppendOpenTurn(t, 0) },
			want:    0,
		},
		"一个完成的回合": {
			arrange: func(t *testing.T, parent *providertest.StubAgent) { parent.AppendCompletedTurn(t, 0) },
			want:    3,
		},
		"两个完成的回合": {
			arrange: func(t *testing.T, parent *providertest.StubAgent) {
				parent.AppendCompletedTurn(t, 0)
				parent.AppendCompletedTurn(t, 1)
			},
			want: 6,
		},
		"完成的回合后面还挂着一个在飞的": {
			arrange: func(t *testing.T, parent *providertest.StubAgent) {
				parent.AppendCompletedTurn(t, 0)
				parent.AppendOpenTurn(t, 1)
			},
			want: 3,
		},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := providertest.New(t)
			each.arrange(t, harness.Parent)

			prefix := completedTurnPrefix(harness.Parent)
			if len(prefix) != each.want {
				t.Fatalf("前缀该有 %d 条，实际 %d 条", each.want, len(prefix))
			}
			if each.want == 0 {
				// 空前缀必须是 nil 而不是长度为零的切片：驱动那边靠这个区分
				// 「全新的孩子」和「一段长度为零的继承前缀」。
				if prefix != nil {
					t.Fatal("没有已完成回合时该交回 nil，而不是空切片")
				}
				return
			}
			if last := prefix[len(prefix)-1]; last.Type != session.EventTurnEnd {
				t.Fatalf("前缀该收在 turn/end 上，实际收在 %q", last.Type)
			}
			// 从 seq 0 起连续，这是那份种子的持久契约。
			for index, each := range prefix {
				if each.Seq != index {
					t.Fatalf("第 %d 条的 seq 是 %d，前缀该从 0 起连续", index, each.Seq)
				}
			}
			// 容量掐到长度：拿到这段种子的人一次 append 不许写进父那份共享快照。
			if cap(prefix) != len(prefix) {
				t.Fatalf("前缀的容量该等于长度 %d，实际 %d", len(prefix), cap(prefix))
			}
			snapshot := harness.Parent.Events()
			appended := append(prefix, snapshot[0]) //nolint:gocritic // 就是要试着写越界那一格
			if len(snapshot) > len(prefix) && &appended[len(prefix)] == &snapshot[len(prefix)] {
				t.Fatal("往种子上追加写进了父那份共享快照")
			}
		})
	}
}

// 这条是这个包唯一真正会出错的地方：那段前缀得**真的**作为种子到达造法，
// 而不是算完就扔。
func TestStartSeedsTheChildWithTheCompletedTurnPrefix(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)
	harness.Parent.AppendCompletedTurn(t, 0)
	harness.Parent.AppendOpenTurn(t, 1)

	run, err := New("", harness.Services()).Start(t.Context(), harness.Request("干活", "fork"))
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	t.Cleanup(func() { _ = run.Dispose(t.Context()) })

	created := harness.OnlyCreate(t)
	if len(created.Seed) != 3 {
		t.Fatalf("该拿 3 条种子建孩子，实际 %d 条", len(created.Seed))
	}
	if last := created.Seed[len(created.Seed)-1]; last.Type != session.EventTurnEnd {
		t.Fatalf("种子该收在 turn/end 上，实际收在 %q", last.Type)
	}
}

// 父那边一个回合都没完成时，fork 出来的孩子和 spawn 出来的一样是全新的。
func TestStartCreatesAFreshChildWhenNoTurnHasCompleted(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)
	harness.Parent.AppendOpenTurn(t, 0)

	run, err := New("", harness.Services()).Start(t.Context(), harness.Request("干活", "fork"))
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	t.Cleanup(func() { _ = run.Dispose(t.Context()) })

	if seed := harness.OnlyCreate(t).Seed; seed != nil {
		t.Fatalf("没有已完成回合时不该给种子，实际给了 %d 条", len(seed))
	}
}

func TestPrepareContinuableCapturesTheSamePrefix(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)
	harness.Parent.AppendCompletedTurn(t, 0)
	harness.Parent.AppendOpenTurn(t, 1)

	spec, err := New("", harness.Services()).PrepareContinuable(t.Context(), subagent.ContinuableCreateRequest{
		SessionID: "child",
		Parent:    harness.Parent,
	})
	if err != nil {
		t.Fatalf("可续预备失败：%v", err)
	}
	if len(spec.Seed) != 3 {
		t.Fatalf("该贡献 3 条种子，实际 %d 条", len(spec.Seed))
	}
}

func TestPrepareContinuableContributesNoSeedWithoutHistory(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)

	spec, err := New("", harness.Services()).PrepareContinuable(t.Context(), subagent.ContinuableCreateRequest{
		SessionID: "child",
		Parent:    harness.Parent,
	})
	if err != nil {
		t.Fatalf("可续预备失败：%v", err)
	}
	if spec.Seed != nil {
		t.Fatalf("没有历史时不该给种子，实际给了 %d 条", len(spec.Seed))
	}
}
