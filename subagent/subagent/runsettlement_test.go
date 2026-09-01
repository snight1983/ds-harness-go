// 本文件的作用：把 [SettleRun] 钉在它那几条真会出错的边上——哪个终止原因映成哪个
// 终态、残缺输出会不会被当成功报上去、以及两样都失败时细节会不会被盖掉。
//
// # 这些测试防的是什么错
//
//   - **残缺输出被当成跑完了**。只有 [StopCompleted] 才带最终文本；别的原因一律
//     failed 且不带输出，否则父 agent 会拿着半截答案继续往下走。
//   - **认不出的终止原因被放行**。[StopReason] 是开放的，后端可以加自己的取值；
//     一个没见过的终态必须落到失败那一侧。
//   - **处置失败盖掉结果失败**。这是两个各自要人去看的故障，只报后一个等于把
//     前一个藏了。
//   - **取消之后就不收拾孩子了**。放资源这件事不许被调用方的取消带走。

package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/llm"
)

func TestSettleRunMapsEveryStopReasonToAnOutcome(t *testing.T) {
	t.Parallel()

	output := llm.Content{
		llm.TextBlock{Text: "答案是 "},
		llm.ImageBlock{},
		llm.TextBlock{Text: "42"},
	}

	cases := []struct {
		name   string
		result Result
		want   jobs.Outcome
	}{
		{
			"跑完了带上摊平的文本",
			Result{StopReason: StopCompleted, Output: output},
			jobs.Outcome{Status: jobs.StatusCompleted, Output: "答案是 42"},
		},
		{
			"被取消算 killed 且不带细节",
			Result{StopReason: StopAborted, Output: output, Diagnostic: "无所谓"},
			jobs.Outcome{Status: jobs.StatusKilled},
		},
		{
			"模型或传输失败",
			Result{StopReason: StopError, Output: output},
			jobs.Outcome{Status: jobs.StatusFailed, Detail: "error"},
		},
		{
			"撞上 token 天花板时带上提供方的细节",
			Result{StopReason: StopMaxTokens, Diagnostic: "5 万个 token"},
			jobs.Outcome{Status: jobs.StatusFailed, Detail: "max-tokens; diagnostic: 5 万个 token"},
		},
		{
			"孩子拒绝了这件事",
			Result{StopReason: StopRefusal},
			jobs.Outcome{Status: jobs.StatusFailed, Detail: "refusal"},
		},
		{
			"后端自己加的终止原因也算失败",
			Result{StopReason: StopReason("quota-exhausted"), Output: output},
			jobs.Outcome{Status: jobs.StatusFailed, Detail: "quota-exhausted"},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			run := &fakeRun{id: "child", result: item.result}
			got := SettleRun(t.Context(), run)
			if got != item.want {
				t.Fatalf("结清出来的是 %+v，要的是 %+v", got, item.want)
			}
			if run.disposals.Load() != 1 {
				t.Fatalf("这次运行被处置了 %d 回", run.disposals.Load())
			}
		})
	}
}

// TestSettleRunTurnsAResultFailureIntoFailed 钉住那条边：只有这条接缝表达不成
// 停止原因的基础设施故障才会走 error，而它照样得结成一份结局，不能把错抛给
// 作业注册表——[jobs.Hooks.Done] 那条契约不接受「没有结局」。
func TestSettleRunTurnsAResultFailureIntoFailed(t *testing.T) {
	t.Parallel()

	run := &fakeRun{id: "child", resultErr: errors.New("传输塌了")}
	got := SettleRun(t.Context(), run)
	if got.Status != jobs.StatusFailed || got.Detail != "传输塌了" {
		t.Fatalf("结清出来的是 %+v", got)
	}
	if run.disposals.Load() != 1 {
		t.Fatalf("结果没拿到就不收拾孩子了：处置了 %d 回", run.disposals.Load())
	}
}

// TestSettleRunKeepsBothDetailsWhenDisposalAlsoFails 钉住那条不许盖掉的路。
func TestSettleRunKeepsBothDetailsWhenDisposalAlsoFails(t *testing.T) {
	t.Parallel()

	run := &fakeRun{
		id:         "child",
		resultErr:  errors.New("传输塌了"),
		disposeErr: errors.New("端口没关"),
	}
	got := SettleRun(t.Context(), run)
	if got.Status != jobs.StatusFailed {
		t.Fatalf("状态是 %q", got.Status)
	}
	if !strings.Contains(got.Detail, "传输塌了") || !strings.Contains(got.Detail, "dispose failed: 端口没关") {
		t.Fatalf("两段细节没都留住：%q", got.Detail)
	}
}

// TestADisposalFailureAloneStillFails 钉住那条单独的路：结果本身没问题，
// 但资源没放掉——这仍然是一次要人去看的故障。
func TestADisposalFailureAloneStillFails(t *testing.T) {
	t.Parallel()

	run := &fakeRun{
		id:         "child",
		result:     Result{StopReason: StopCompleted, Output: llm.Content{llm.TextBlock{Text: "好了"}}},
		disposeErr: errors.New("端口没关"),
	}
	got := SettleRun(t.Context(), run)
	if got.Status != jobs.StatusFailed || got.Detail != "dispose failed: 端口没关" {
		t.Fatalf("结清出来的是 %+v", got)
	}
}

// TestACancelledWaitStillReleasesTheChild 钉住那条最要紧的边：调用方的取消
// 只掐等待，不许把「把孩子收干净」一起掐掉。
func TestACancelledWaitStillReleasesTheChild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	run := &fakeRun{id: "child", release: make(chan struct{})}
	cancel()

	got := SettleRun(ctx, run)
	if got.Status != jobs.StatusFailed {
		t.Fatalf("取消之后结清成了 %+v", got)
	}
	if run.disposals.Load() != 1 {
		t.Fatalf("取消之后没收拾孩子：处置了 %d 回", run.disposals.Load())
	}
}
