// 本文件的作用：跑一次 `/compact`——那条只有「不带参数」一说的语法、把接缝的结局
// 排成一句人读的话，以及那六个分好类的失败各自对应哪一句。
//
// 源: packages/compaction/command-compact/src/index.ts:13-78

package compactcommand

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
)

// usage 是敲了参数时回的那一句。
//
// 源: packages/compaction/command-compact/src/index.ts:13
const usage = "Usage: /compact (no arguments)"

// nothingToCompact 是这段会话还没有可压的历史时回的那一句。
//
// 源: packages/compaction/command-compact/src/index.ts:67
const nothingToCompact = "No compactable history yet."

// run 跑一次不带参数的人工压缩。
//
// 源: packages/compaction/command-compact/src/index.ts:58-78（executeCompact）
func (c *Controller) run(ctx context.Context, invocation commands.Invocation) (commands.Result, error) {
	c.enter()
	defer c.leave()

	if strings.TrimSpace(invocation.RawInput) != "" {
		return commands.Result{Kind: commands.ResultError, Text: usage}, nil
	}

	target, err := c.agentOf(invocation.Agent)
	if err != nil {
		return commands.Result{}, fmt.Errorf("compactcommand: /compact 找不到发起它的那个 agent：%w", err)
	}

	result, compacted, err := c.engine.CompactNow(ctx, target, string(invocation.ID))
	if err != nil {
		return expectedFailure(err)
	}
	if !compacted {
		return commands.Result{Kind: commands.ResultSuccess, Text: nothingToCompact}, nil
	}
	// 指向那条 compaction/summary：它自己带着被遮的那一段、摘要本身和那次调用的
	// 事实，比这句话丰富得多，界面愿意的话可以照着它铺开。
	summary := result.SummarySeq
	return commands.Result{
		Kind: commands.ResultSuccess,
		Text: fmt.Sprintf("Compacted %d history items (~%d tokens).",
			len(result.ShadowedSeqs), result.ShadowedTokenCount),
		SourceEventSeq: &summary,
	}, nil
}

// expectedFailure 把一条预期之内的压缩失败排成一句给人的回话；认不出来的原样抛回去。
//
// 源: packages/compaction/command-compact/src/index.ts:23-55
//
// 新增: DSH 在这之前还有一句 `if (invocation.signal.aborted) return 'Compaction cancelled.'`。
// Go 这边它一个字都到不了界面：[github.com/snight1983/ds-harness-go/feature/interaction/commands.Runtime]
// 在处理器返回之后**无条件**再问一次 ctx，取消永远赢，那条结果会被丢掉换成一条
// 取消错误。撤回这件事在这里只有一条活着的路——接缝自己把它分类成
// [compaction.ManualErrorCancelled]，而那一支回的正是同一句话。
//
// 新增: DSH 那个 default 分支是 `assertNever`，TypeScript 保证走不到。
// [compaction.ManualErrorCode] 是个开放的字符串类型，所以这里走得到——
// 走到就说明有人往那张封闭的单子上加了一项而没管这边，那是一次故障，
// 该原样抛给调用方，不该编一句话糊过去。
func expectedFailure(err error) (commands.Result, error) {
	var failure *compaction.ManualError
	if !errors.As(err, &failure) {
		return commands.Result{}, err
	}
	var text string
	switch failure.Code {
	case compaction.ManualErrorBusy:
		text = "Compaction is unavailable because this process has an active compaction, or the agent is not idle."
	case compaction.ManualErrorCancelled:
		text = "Compaction cancelled."
	case compaction.ManualErrorChanged:
		text = "The history selected for compaction changed before it could be replaced. " +
			"The conversation is unchanged; the attempt is recorded in the session log."
	case compaction.ManualErrorSummary:
		text = "Compaction could not produce a useful summary. " +
			"The conversation is unchanged; the attempt is recorded in the session log."
	case compaction.ManualErrorCommit:
		text = "Compaction did not finish cleanly; some session history may have changed. " +
			"Inspect the current session state before retrying."
	case compaction.ManualErrorPersistence:
		text = "Compaction finished, but the session could not be saved."
	default:
		return commands.Result{}, err
	}
	return commands.Result{Kind: commands.ResultError, Text: text}, nil
}
