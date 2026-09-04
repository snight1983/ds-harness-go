// 本文件的作用：那条循环本身——每一轮那段发给孩子的提示词怎么排、一轮怎么开一个
// 全新的孩子并把它的报告收回来、以及三种收场（报完成、报阻塞、轮次用光）各自在
// 哪里跳出来。
//
// 源: packages/workflow/tool-ralph/src/index.ts:90-177（那段固定脚本）
//
// 新增: DSH 那边这一整件事是一段交给 workflow 引擎跑的 JavaScript 字符串
// （RALPH_SCRIPT）。那个引擎在本仓库裁了 OUT_OF_SCOPE，理由见 [doc.go]；它在这件事
// 上唯一的产出就是「按顺序开几个孩子、把上一份报告带给下一个」，那在 Go 里就是
// 下面这个 for 循环。

package toolralph

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
)

// RunStatus 是一整次 Ralph 调用的收场。
//
// 源: packages/workflow/tool-ralph/src/index.ts:59
type RunStatus string

const (
	// RunComplete 表示某一轮那个孩子报了完成。
	RunComplete RunStatus = "complete"
	// RunBlocked 表示某一轮那个孩子报了一件挡路的事。
	RunBlocked RunStatus = "blocked"
	// RunBudgetLimited 表示轮次用光了，而最后那一轮还说有活儿没干完。
	RunBudgetLimited RunStatus = "budget-limited"
)

// runResult 是一次跑完了的 Ralph 调用交回父手上的那份终局值。
//
// 源: packages/workflow/tool-ralph/src/index.ts:61-65
//
// 字段名照抄 DSH，因为它们直接进那份工具输出 schema，也直接进 [renderResult]。
type runResult struct {
	// Status 是这次调用的收场。
	Status RunStatus `json:"status"`
	// RoundsStarted 是一共开了几轮。
	RoundsStarted int `json:"roundsStarted"`
	// Report 是最后那一轮的报告。
	Report RoundReport `json:"report"`
}

// roundFailure 是「某一轮那个孩子没能交出一份报告」这件事。
//
// 源: packages/workflow/tool-ralph/src/index.ts:67-71, 465
//
// 新增: DSH 那边它是终局值的一个变体（status: 'round-failed'），因为它得穿过引擎
// 那道边界从脚本里传出来，而脚本只能返回值、不能把一个 Error 送过去。Go 这边没有
// 那道边界，所以它就是一个 error：这条路的终点本来就是 `throw new Error(...)`
// （index.ts:465），中间那一段「排成值、再读回来、再抛掉」纯粹是为了过那道边界。
//
// 新增: Cause 是 DSH 没有的。那边孩子失败的原因留在了 worker 线程里，脚本只看到
// `agent(...)` 交回 null，于是父只知道「孩子挂了」不知道为什么。这边它就在手上，
// 扔掉纯属白扔。
type roundFailure struct {
	// round 是失败的那一轮。
	round int
	// lastReport 是上一轮留下的那份交接；第一轮就失败时是 nil。
	lastReport *RoundReport
	// cause 是那个孩子为什么没交出报告。
	cause error
	// maxChars 是渲染这句话时的字数上限，造它的那一刻从装配里带下来。
	maxChars int
}

// Error 把这次失败排成给模型看的那段话。
//
// 源: packages/workflow/tool-ralph/src/index.ts:386-392, 465
func (f *roundFailure) Error() string {
	return renderRoundFailure(f)
}

// Unwrap 交出那个孩子失败的原因，好让调用方对着它做 [errors.Is]。
func (f *roundFailure) Unwrap() error {
	return f.cause
}

// firstRoundHandoff 是第一轮那份「上一轮交接」的占位文字。
//
// 源: packages/workflow/tool-ralph/src/index.ts:154
//
// 破折号是 DSH 原文里的 U+2014，照抄——这段字是提示词的一部分。
const firstRoundHandoff = "(none — this is the first round)"

// roundPrompt 排出发给某一轮那个孩子的整段提示词。
//
// 源: packages/workflow/tool-ralph/src/index.ts:155-162
//
// 六段话逐字照抄，段与段之间空一行（DSH 的 join('\n\n')）。它们是给模型看的，
// 所以是英文；每一段各自堵着一种很具体的跑偏：
//
//   - 第一段告诉孩子它是谁，并且明说**不许**再调 ralph——不挡的话它会再开一层
//     Ralph，每一层都在同一个工作区上动手。
//   - 第二段是那个一个字都不变的目标。
//   - 第三段是「第几轮、一共几轮」，让孩子自己判断该收还是该铺开。
//   - 第四段把长期记忆钉死在工作区上，并明说上一份报告只是交接、要拿工作区去核。
//   - 第五段是上一轮那份交接。
//   - 第六段是那三种定性各自的判据，跟 [validateReport] 里那几条一一对应。
//
// previous 为 nil 表示这是第一轮。它必须是纯函数：同样的入参排出同样的字，
// 测试就是这么钉它的。
func roundPrompt(objective string, round, maxRounds int, previous *RoundReport) string {
	prior := firstRoundHandoff
	if previous != nil {
		prior = encodeReport(*previous)
	}
	paragraphs := []string{
		"You are one fresh worker in a foreground Ralph loop. You receive no parent conversation and no prior child session. Do not call the ralph tool: this round already is its worker.",
		"Immutable objective:\n" + objective,
		"Ralph round: " + strconv.Itoa(round) + " of " + strconv.Itoa(maxRounds) + ".",
		"The shared workspace and its current working tree are the long-term memory and source of truth. Inspect them before acting, preserve existing work, perform concrete in-scope work, and verify what you change. Treat the previous report only as a bounded handoff; confirm it against the workspace.",
		"Previous structured handoff:\n" + prior,
		"Return one report with exact normalized strings. Use status continue with at least one nextSteps entry while useful work remains; complete only with concrete evidence and no nextSteps; blocked only when no meaningful progress is possible without human input or an external-state change. blocker must be empty unless blocked.",
	}
	return strings.Join(paragraphs, "\n\n")
}

// runLoop 把那条循环推起来，直到有人报完成、报阻塞，或者轮次用光。
//
// 源: packages/workflow/tool-ralph/src/index.ts:151-176
//
// 三条出口：complete 和 blocked 当场跳出来，continue 把这一份存下来接着下一轮；
// 循环走完就是 budget-limited，交回最后那份 continue 报告。
//
// 新增: DSH 那句 `return { status: 'budget-limited', …, report: previous }` 在
// maxRounds 至少是 1、且每一轮不是跳出来就是留下一份 continue 的前提下，previous
// 必然已经有值了（maxRounds < 1 在 [resolveMaxRounds] 就被拒了）。Go 这边它是指针，
// 所以那个不可能的分支写成一句大声失败，而不是悄悄交回一份零值报告。
func (c *Controller) runLoop(
	ctx context.Context,
	subagents Subagents,
	parent agent.Agent,
	objective string,
	maxRounds int,
) (runResult, int, error) {
	var previous *RoundReport
	for round := 1; round <= maxRounds; round++ {
		prompt := roundPrompt(objective, round, maxRounds, previous)
		report, err := c.runRound(ctx, subagents, parent, prompt, round)
		if err != nil {
			var failure *roundFailure
			if errors.As(err, &failure) {
				failure.lastReport = previous
			}
			return runResult{}, round, err
		}
		switch report.Status {
		case RoundComplete:
			return runResult{Status: RunComplete, RoundsStarted: round, Report: report}, round, nil
		case RoundBlocked:
			return runResult{Status: RunBlocked, RoundsStarted: round, Report: report}, round, nil
		}
		previous = &report
	}
	if previous == nil {
		return runResult{}, maxRounds, errors.New("Ralph loop ended without any round report")
	}
	return runResult{
		Status:        RunBudgetLimited,
		RoundsStarted: maxRounds,
		Report:        *previous,
	}, maxRounds, nil
}

// runRound 开一个全新的孩子跑一轮，把它那份验过的报告收回来。
//
// 源: packages/workflow/tool-ralph/src/index.ts:163-171
//
// 分得清清楚楚的两种错：
//
//   - 孩子**没交出**报告（开工失败、跑挂了、被取消、结构化结果是空的）是一次
//     [roundFailure]，它带着上一轮那份交接一起呈给父——那是这次调用留下来的
//     唯一成果，扔了就白跑了。DSH 里这条对应 `rawReport === null`。
//   - 孩子交出来的报告**不合规矩**是一个普通的硬错。那说明这条路上有人没按契约
//     办事（提供方没验 schema、或者本包的 schema 和校验对不上），带着一份交接
//     把它糊过去只会让下一次更难查。
func (c *Controller) runRound(
	ctx context.Context,
	subagents Subagents,
	parent agent.Agent,
	prompt string,
	round int,
) (RoundReport, error) {
	schema := reportSchema()
	run, err := subagents.Start(ctx, c.provider, subagent.StartRequest{
		Label:        "Ralph round " + strconv.Itoa(round),
		Prompt:       llm.Content{llm.TextBlock{Text: prompt}},
		Parent:       parent,
		OutputSchema: &schema,
	})
	if err != nil {
		return RoundReport{}, c.roundFailed(round, err)
	}
	structured, err := c.settleRound(ctx, run)
	if err != nil {
		return RoundReport{}, c.roundFailed(round, err)
	}
	report, err := readReport(structured, "", c.maxHandoffChars)
	if err != nil {
		return RoundReport{}, fmt.Errorf("Ralph round %d: %w", round, err)
	}
	return report, nil
}

// roundFailed 把一件「这一轮没能交出报告」的事包成 [roundFailure]。
//
// lastReport 由 [Controller.runLoop] 在接住它的时候填——那份交接在这一层还看不到。
func (c *Controller) roundFailed(round int, cause error) *roundFailure {
	return &roundFailure{round: round, cause: cause, maxChars: c.maxResultChars}
}

// settleRound 等这一轮那个孩子结清，交回它那份结构化结果，并且**一定**把它放掉。
//
// 源: packages/workflow/tool-ralph/src/index.ts:163-170, 471-474
//
// 形状和 [github.com/snight1983/ds-harness-go/feature/subagent/subagenttool] 那台前台结算一样，只有一处不同：
// 这里要的是 [subagent.Result.Structured] 而不是那段文本输出。一个 StopCompleted
// 却没留下结构化结果的孩子在这里算失败——那正是 DSH 里 `rawReport === null` 的
// 那种情况。
//
// 处置不许被调用方的取消带走（[context.WithoutCancel]）：一次已经被取消的等待
// 恰恰是最需要把孩子收干净的时候。处置失败**不**盖掉一次各自独立的结果失败。
func (c *Controller) settleRound(ctx context.Context, run subagent.Run) (any, error) {
	structured, execution := collectRound(ctx, run)
	disposal := run.Dispose(context.WithoutCancel(ctx))
	switch {
	case execution != nil && disposal != nil:
		return nil, errors.Join(execution, disposal)
	case execution != nil:
		return nil, execution
	case disposal != nil:
		return nil, disposal
	}
	return structured, nil
}

// collectRound 等这次运行的结果，把一个非正常的终态或者一份缺席的结构化结果
// 折成一个 error。
//
// 源: packages/workflow/tool-ralph/src/index.ts:168-170
func collectRound(ctx context.Context, run subagent.Run) (any, error) {
	result, err := run.Result(ctx)
	if err != nil {
		return nil, err
	}
	if headline := stopReasonError(result); headline != "" {
		return nil, errors.New(withDiagnostic(headline, result))
	}
	if result.Structured == nil {
		return nil, errors.New("Ralph child finished without a structured round report")
	}
	return result.Structured, nil
}

// stopReasonError 把一个非正常的终止原因排成那句话头；正常跑完了就是空串。
//
// 源: packages/subagent/tool-subagent/src/index.ts:125-142
//
// 判断和 [github.com/snight1983/ds-harness-go/feature/subagent/subagenttool] 那份逐条相同，措辞换成 Ralph 的
// 说法。那个 default 接住的是后端自己加的终止原因：把一个认不出的终态当成失败，
// 而不是把一份可能残缺的结构化结果当成一轮成功——后者会让下一轮拿着半截交接接着跑。
func stopReasonError(result subagent.Result) string {
	switch result.StopReason {
	case subagent.StopCompleted:
		return ""
	case subagent.StopAborted:
		return "Ralph round child was cancelled"
	case subagent.StopError:
		return "Ralph round child failed"
	case subagent.StopMaxTokens:
		return "Ralph round child hit its token limit before finishing"
	case subagent.StopRefusal:
		return "Ralph round child declined the task"
	default:
		return "Ralph round child ended abnormally (" + string(result.StopReason) + ")"
	}
}

// withDiagnostic 在那句话头后面接上提供方写的失败细节。
//
// 源: packages/subagent/tool-subagent/src/index.ts:152-160
//
// 新增: 那边还会接上孩子留下的那半截文本输出。这里不接：Ralph 的孩子是结构化的，
// 它那段文本输出不是给父看的东西，而这段话最后会跟着一份上一轮的交接一起呈上去
// （见 [renderRoundFailure]），再塞一段半截自由文本只会把「哪句是谁说的」搅浑。
func withDiagnostic(headline string, result subagent.Result) string {
	if result.Diagnostic == "" {
		return headline
	}
	return headline + "\nDiagnostic: " + result.Diagnostic
}
