// 本文件的作用：把 spawn 提供方钉在它那几条边上——名字的默认值、它自称的能力、
// 「不继承父上下文」这句话和它实际给出去的种子对不对得上，以及可续预备什么都不贡献。

package spawninprocess

import (
	"testing"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/feature/subagent/providertest"
)

func TestNewFallsBackToTheDefaultProviderName(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		given string
		want  string
	}{
		"空串取默认值": {given: "", want: "spawn"},
		"给了就用给的": {given: "spawn-2", want: "spawn-2"},
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
	if provider.InheritsParentContext() {
		t.Fatal("spawn 出来的孩子不该继承父上下文")
	}
}

// 这条是这个包唯一真正会出错的地方：spawn 的孩子必须**全新**开局。父那边摆着一个
// 已经完成的回合，正是 fork 会拿去做种的那一段——spawn 一条都不许带走。
func TestStartCreatesAFreshChildEvenWhenTheParentHasHistory(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)
	harness.Parent.AppendCompletedTurn(t, 0)
	if len(harness.Parent.Events()) == 0 {
		t.Fatal("这条用例要父那边确实有历史，否则它什么都没证明")
	}

	run, err := New("", harness.Services()).Start(t.Context(), harness.Request("干活", "spawn"))
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	t.Cleanup(func() { _ = run.Dispose(t.Context()) })

	created := harness.OnlyCreate(t)
	if created.Seed != nil {
		t.Fatalf("spawn 不该给种子，实际给了 %d 条", len(created.Seed))
	}
}

func TestPrepareContinuableContributesNoSeed(t *testing.T) {
	t.Parallel()

	harness := providertest.New(t)
	harness.Parent.AppendCompletedTurn(t, 0)

	spec, err := New("", harness.Services()).PrepareContinuable(t.Context(), subagent.ContinuableCreateRequest{
		SessionID: "child",
		Parent:    harness.Parent,
	})
	if err != nil {
		t.Fatalf("可续预备失败：%v", err)
	}
	if spec.Seed != nil {
		t.Fatalf("spawn 的可续预备不该给种子，实际给了 %d 条", len(spec.Seed))
	}
}
