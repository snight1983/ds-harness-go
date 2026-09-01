// 本文件的作用：把一次孩子运行收成一份工具结果——一个非正常的终止原因怎么变成
// 一次 isError，那段残缺的答案怎么在报错的同时还是送到父手上，以及收结果和放资源
// 这两件事分别失败时谁盖谁。
//
// 源: packages/subagent/tool-subagent/src/index.ts:111-206

package subagenttool

import (
	"context"
	"errors"
	"strings"

	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// 这几句是一个非 [github.com/snight1983/ds-harness-go/subagent/subagent.StopCompleted] 的终止原因给模型的
// 那句话头。
//
// 源: packages/subagent/tool-subagent/src/index.ts:126-141
const (
	errAborted   = "subagent run was cancelled"
	errFailed    = "subagent run failed"
	errMaxTokens = "subagent run hit its token limit before finishing"
	errRefusal   = "subagent declined the task"
)

// stopReasonError 把一个非正常的终止原因排成那句话头；正常跑完了就是空串。
//
// 源: packages/subagent/tool-subagent/src/index.ts:125-142
//
// 那个 default 接住的是后端自己加的终止原因。把一个认不出的终态当成失败，
// 而不是把残缺输出当成功报上去——后者会让父 agent 拿着半截答案继续往下走。
func stopReasonError(result subagent.Result) string {
	switch result.StopReason {
	case subagent.StopCompleted:
		return ""
	case subagent.StopAborted:
		return errAborted
	case subagent.StopError:
		return errFailed
	case subagent.StopMaxTokens:
		return errMaxTokens
	case subagent.StopRefusal:
		return errRefusal
	default:
		return "subagent run ended abnormally (" + string(result.StopReason) + ")"
	}
}

// contentText 把一段内容里的文本块摊平。
//
// 源: packages/subagent/tool-subagent/src/index.ts:156-159
func contentText(content llm.Content) string {
	var builder strings.Builder
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			builder.WriteString(text.Text)
		}
	}
	return builder.String()
}

// withDiagnosticAndPartialText 在那句话头后面接上提供方写的失败细节和孩子留下的
// 那半截答案。
//
// 源: packages/subagent/tool-subagent/src/index.ts:152-164
//
// 三样东西分三段而不是揉成一句：话头是这条接缝的判断，细节是提供方写的诊断，
// 残缺输出是孩子自己说的话。揉在一起模型就分不清哪句是谁说的了。
func withDiagnosticAndPartialText(headline string, result subagent.Result) string {
	message := headline
	if result.Diagnostic != "" {
		message += "\nDiagnostic: " + result.Diagnostic
	}
	if text := contentText(result.Output); text != "" {
		message += "\nPartial output before the run ended:\n" + text
	}
	return message
}

// settleForegroundRun 收一次前台运行的结果并放掉它，**不**让处置失败盖掉一次
// 各自独立的结果失败。
//
// 源: packages/subagent/tool-subagent/src/index.ts:176-206
//
// 两件事都做、都做完，再决定报什么：这是 DSH 那两次 Promise.allSettled 的意思。
// 顺序上必须先收结果后处置——处置会取消剩下的活，先处置就等于把自己要等的东西
// 掐了。
//
// 新增: DSH 两样都砸时抛 AggregateError。Go 里那就是 [errors.Join]，它同样把
// 两条原因都留在一个 error 里，`errors.Is` 也照样对每一条成立。
func settleForegroundRun(ctx context.Context, run subagent.Run) (llm.Content, error) {
	output, execution := collectForegroundRun(ctx, run)
	// 放资源这件事不许被调用方的取消带走：一次已经被取消的等待恰恰是最需要把孩子
	// 收干净的时候。
	disposal := run.Dispose(context.WithoutCancel(ctx))
	switch {
	case execution != nil && disposal != nil:
		return nil, errors.Join(execution, disposal)
	case execution != nil:
		return nil, execution
	case disposal != nil:
		return nil, disposal
	}
	return output, nil
}

// collectForegroundRun 等这次运行的结果，并把一个非正常的终态折成一个 error。
//
// 源: packages/subagent/tool-subagent/src/index.ts:178-192
//
// 交回 error 会被工具运行时变成一次 isError 的结果。残缺输出不是成功，但那半截
// 答案照样跟着那次报错送到父手上——它是孩子唯一留下来的东西。
func collectForegroundRun(ctx context.Context, run subagent.Run) (llm.Content, error) {
	result, err := run.Result(ctx)
	if err != nil {
		return nil, err
	}
	if headline := stopReasonError(result); headline != "" {
		return nil, errors.New(withDiagnosticAndPartialText(headline, result))
	}
	return result.Output, nil
}

// settleStart 结清一次还没起来的开工，而**不**违背作业生产方那条契约：
// [github.com/snight1983/ds-harness-go/jobs/jobs.Hooks.Done] 只收结局，不收错误。
//
// 源: packages/subagent/tool-subagent/src/index.ts:112-122
//
// 开工阶段砸了要分两种：被取消的算 killed，别的算 failed。DSH 那边多一条
// 「聚合错误不算取消」——因为提供方会把开工失败和回滚失败聚在一起，那种情况下
// 报一个干干净净的 killed 会把「清理没做完」这件事藏掉。Go 这边同一条判断落在
// [errors.Is] 上：一个 [errors.Join] 出来的错，只要里面还有别的原因，
// 它就不只是一次取消。
func settleStart(ctx context.Context, start func(context.Context) (subagent.Run, error)) jobs.Outcome {
	run, err := start(ctx)
	if err == nil {
		return subagent.SettleRun(ctx, run)
	}
	if ctx.Err() != nil && isOnlyCancellation(err, context.Cause(ctx)) {
		return jobs.Outcome{Status: jobs.StatusKilled}
	}
	return jobs.Outcome{Status: jobs.StatusFailed, Detail: err.Error()}
}

// isOnlyCancellation 说明这个错除了取消之外还夹带没夹带别的原因。
//
// 源: packages/subagent/tool-subagent/src/index.ts:118-120
//
// [errors.Join] 出来的错实现了 `Unwrap() []error`，所以「不只是取消」这件事
// 在 Go 里查得出来：拆开看，只要有一条不是取消，那就是一次要人去看的失败，
// 不能报成干干净净的 killed。
//
// cause 是这次取消自己那句话。本仓库里一个被取消的等待交回的是 [context.Cause]
// 而不是 [context.Canceled]（成例见 [github.com/snight1983/ds-harness-go/mcp.Host] 和
// [github.com/snight1983/ds-harness-go/core/systemprompt.Registry]），而 [context.WithCancelCause] 带的
// 那句话是一个普通的 error，[errors.Is] 认不出 [context.Canceled] 来。所以除了
// 那两个哨兵之外还要认它——不然一次**明说了理由**的取消反倒会被报成 failed，
// 理由写得越清楚判得越错。
func isOnlyCancellation(err error, cause error) bool {
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, each := range joined.Unwrap() {
			if !isOnlyCancellation(each, cause) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		(cause != nil && errors.Is(err, cause))
}
