// 本文件的作用：把一次**一次性**子 agent 运行结清成一份后台作业结局。
//
// 源: packages/subagent/subagent/src/run-settlement.ts
//
// 只有一次性那条后台路用得上作业：可续的孩子没有运行、没有「那一次的结果」、
// 也没有作业层面的取消，它每一个回合都从自己的收件箱走。

package subagent

import (
	"context"
	"strings"

	"ds-harness-go/jobs/jobs"
	"ds-harness-go/llm"
)

// finalText 把孩子最后那段输出里的文本块摊平成作业的最终文本。
//
// 源: packages/subagent/subagent/src/run-settlement.ts:14-19
//
// 非文本块（图片、工具调用……）在这里丢掉不是疏忽：一件作业的最终输出是一段
// 给模型读的字，而这条接缝不负责替它渲染别的形态。
func finalText(blocks llm.Content) string {
	var builder strings.Builder
	for _, block := range blocks {
		if text, ok := block.(llm.TextBlock); ok {
			builder.WriteString(text.Text)
		}
	}
	return builder.String()
}

// failureDetail 把一个失败的终止原因和提供方那段可选的细节排成一行。
//
// 源: packages/subagent/subagent/src/run-settlement.ts:22-27
func failureDetail(result Result) string {
	if result.Diagnostic == "" {
		return string(result.StopReason)
	}
	return string(result.StopReason) + "; diagnostic: " + result.Diagnostic
}

// runOutcome 把孩子的结果映成作业结局：跑完了带上最终文本，被取消算 killed，
// 其余一律 failed 且**不带**残缺输出。
//
// 源: packages/subagent/subagent/src/run-settlement.ts:34-49
//
// 那个 default 分支接住的是后端自己加的终止原因（[StopReason] 是个开放的具名
// 字符串类型）。把一个认不出的终态判成失败，而不是把残缺输出当成功报上去——
// 后者会让父 agent 拿着半截答案继续往下走。
func runOutcome(result Result) jobs.Outcome {
	switch result.StopReason {
	case StopCompleted:
		return jobs.Outcome{Status: jobs.StatusCompleted, Output: finalText(result.Output)}
	case StopAborted:
		return jobs.Outcome{Status: jobs.StatusKilled}
	default:
		return jobs.Outcome{Status: jobs.StatusFailed, Detail: failureDetail(result)}
	}
}

// SettleRun 等孩子的结果、处置这次运行，再交出那份作业结局。
//
// 源: packages/subagent/subagent/src/run-settlement.ts:57-70
//
// 结果失败和处置失败都变成 failed；两样都失败时两段细节都留住——一次「结果没拿到」
// 加一次「资源没放掉」是两个各自要人去看的故障，只报后一个会把前一个盖住。
//
// 新增: DSH 是 `settleRun(run)`，取消从 run 自己那个 promise 走。Go 里
// [Run.Result] 收一个 ctx，所以这里把调用方的取消一路传下去；处置则用
// [context.WithoutCancel]，理由是**放资源这件事不许被取消掉**——一次已经被取消
// 的等待恰恰是最需要把孩子收干净的时候。
func SettleRun(ctx context.Context, run Run) jobs.Outcome {
	outcome := jobs.Outcome{Status: jobs.StatusFailed}
	if result, err := run.Result(ctx); err != nil {
		outcome.Detail = err.Error()
	} else {
		outcome = runOutcome(result)
	}
	if err := run.Dispose(context.WithoutCancel(ctx)); err != nil {
		prefix := ""
		if outcome.Detail != "" {
			prefix = outcome.Detail + "; "
		}
		return jobs.Outcome{Status: jobs.StatusFailed, Detail: prefix + "dispose failed: " + err.Error()}
	}
	return outcome
}
