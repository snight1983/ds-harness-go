// 本文件的作用：这一层里那件**会改日志**的事——把选中的一段表面真的压成一个
// 摘要节点，落成 start / summary / 替换消息 / end 这一组带括号的事务。
//
// 源: packages/compaction/compaction-basic/src/region.ts:152-254

package basic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Meter 是本事务要的那一小片计量器。
//
// 源: packages/compaction/compaction-basic/src/region.ts:28（dependencies.meter）
//
// 新增: DSH 收一整个 `TokenMeter` 服务，实际只用到 `measure()` 和
// `estimateMessage()` 两个方法，而且 `measure()` 的返回里只读 `.nodes`。
// 本仓库真的那个计量器（feature/tokenmeter）的 Measure 收的是一份投影视图加一份
// 请求头，签名对不上这里要的东西，所以按「消费方自己声明它需要的那一小片」
// 现声明这两个方法——成例是 [PricedNode] 和 [Streamer]。装配方现包一层适配。
type Meter interface {
	// PriceSurface 给当前表面上每个节点定价，按表面顺序，和
	// [coresession.Session.SurfaceNodes] 一一对上。
	//
	// **每次调用都要交出一份新的切片**：本事务会把一次调用的结果留在手里，
	// 拿它和总结之后再算一次的结果比对，两次共享同一个底层数组的话那个比对
	// 恒真，一次表面改写就再也拦不住了。
	PriceSurface(live *coresession.Session) ([]PricedNode, error)

	// EstimateMessage 按同一把尺子估一条消息值多少 token。
	EstimateMessage(message llm.Message) (int, error)
}

// Summarize 是这次事务要调的那个总结钩子。
//
// 源: packages/compaction/compaction-basic/src/region.ts:29
//
// DSH 把它写成 `RegionDependencies` 上的一个方法，为的是让引擎能覆盖它
// （子类改摘要来源）。Go 这边是个函数字段，同一个效果：装配方填
// [SummarizeWithLLM] 的一个闭包，测试填一台假的。
//
// 新增: 末尾那个可选的 AbortSignal 换成头一个参数的 ctx，理由和本块别处相同。
type Summarize func(
	ctx context.Context,
	input SummarizationInput,
	agent compaction.AgentContext,
) (SummaryResult, error)

// RegionDeps 是这次事务要的两样外部能力。
//
// 源: packages/compaction/compaction-basic/src/region.ts:27-30
type RegionDeps struct {
	// Meter 是给表面定价、也给裹好的检查点估价的那把尺子。
	Meter Meter
	// Summarize 是真的把摘要做出来的那一步。
	Summarize Summarize
}

// Stability 是「一份摘要在总结期间还算不算数」的判定口径。
//
// 源: packages/compaction/compaction-basic/src/region.ts:78-82
//
// 两档的差别是**别处**的改动算不算数：整表面那一档要求总结期间会话一个节点
// 都没动过，选中段那一档只要求被遮的那一段还是原来那个可替换的目标。
// 前者给自动压缩用（它跑在一个回合里，表面本来就不该有别的写入），
// 后者给人工压缩用（空闲期里别的层仍可能往后面追加可见节点）。
type Stability string

const (
	// StabilityWholeSurface 要求整个表面在总结期间一个节点都没变。
	StabilityWholeSurface Stability = "whole-surface"
	// StabilitySelectedSpan 只要求选中的那一段仍然是同一个可替换的目标。
	StabilitySelectedSpan Stability = "selected-span"
)

// TransactionOptions 是这次事务的括号归属、稳定性口径和收尾动作。
//
// 源: packages/compaction/compaction-basic/src/region.ts:53-62
type TransactionOptions struct {
	// Standalone 为真表示这是两个回合之间的一次独立事务（人工压缩），
	// 括号上的归属排成 null；为假表示它必须裹在一个开着的回合里。
	//
	// 新增: DSH 是 `owner: 'current-turn' | null`，两个取值。这里是一个布尔，
	// 理由和 [compaction.StartData.Standalone] 逐字相同——那个字段就是它的去处。
	Standalone bool
	// Stability 是这次总结要经得起哪一档表面改动，必填。
	Stability Stability
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// Flush 是括号**成功合上之后**那次可选的持久化检查点；nil 表示不做。
	//
	// 新增: DSH 是 `flush?: () => Promise<void>`。这里多收一个 ctx，理由和本仓库
	// 每一处异步接缝相同：一次落库要跟着这次请求一起被取消掉。
	Flush func(ctx context.Context) error
	// NewID 铸这次事务的身份；nil 时用 uuid.NewString。
	//
	// 新增: DSH 直接调 `randomUUID()`。做成可注入的字段是本仓库的一贯做法
	// （成例是 llmretry 和 userapproval），为的是让用例能钉住写进日志的那个 ID，
	// 而不是反过来从日志里把它读出来再断言。
	NewID func() string
}

// errSurfaceChanged 标记「这份摘要建立在一份已经不成立的表面上」。
//
// 源: packages/compaction/compaction-basic/src/region.ts:75
//
// 新增: DSH 是一个模块私有的 `class SurfaceChangedError`，靠 instanceof 认。
// Go 这边是一个不可导出的哨兵，用 %w 裹进去、errors.Is 认回来。不导出是照抄：
// 它只在 [manualFailure] 那一处被分开，对外表现成
// [compaction.ManualErrorChanged]，而不是让调用方自己去判。
var errSurfaceChanged = errors.New("compaction-basic：表面变了")

// 事务失败发生在哪一步。这两个取值只决定人工那一侧怎么分类。
//
// 源: packages/compaction/compaction-basic/src/region.ts:85-88
const (
	stageSummary = "summary"
	stageCommit  = "commit"
)

// surfaceSelection 是一段验过的、按表面位置算的闭区间。
//
// 源: packages/compaction/compaction-basic/src/region.ts:33-39
type surfaceSelection struct {
	// Region 是这一段的头尾表面位置。
	Region compaction.ShadowedRange
	// StartIdx 和 EndIdx 是这一段在表面节点数组里的下标。
	StartIdx int
	EndIdx   int
	// ShadowedSeqs 是这一段全体节点的 seq，按表面顺序。
	ShadowedSeqs []int
}

// preparedCompaction 是一段选中的区间，加上它那一刻的估价和重放出来的输入。
//
// 源: packages/compaction/compaction-basic/src/region.ts:42-47
type preparedCompaction struct {
	surfaceSelection
	// Priced 是**整个**表面那一刻的估价，整表面那一档的稳定性判定拿它做基准。
	Priced []PricedNode
	// Selected 是选中那一段的估价，选中段那一档拿它做基准。
	Selected []PricedNode
	// ShadowedTokenCount 是选中那一段的估价之和。
	ShadowedTokenCount int
	// Input 是重放出来、要喂给总结的那一段对话。
	Input SummarizationInput
}

// summarizedCompaction 是做完摘要、也裹好了检查点消息的一段压缩。
//
// 源: packages/compaction/compaction-basic/src/region.ts:49-51
type summarizedCompaction struct {
	preparedCompaction
	SummaryResult
	// Checkpoint 是要落到表面上、换掉那一段的那条用户消息。
	Checkpoint llm.Message
}

// stabilityCheck 是「这份摘要还能不能换掉它建立时的那一段」这个判定。
//
// 源: packages/compaction/compaction-basic/src/region.ts:78-82
type stabilityCheck func(
	deps RegionDeps,
	balance *compaction.BalanceIndex,
	live *coresession.Session,
	prepared preparedCompaction,
) error

// CompactSurfaceRegion 跑那一次压缩事务：把选中的一段表面换成一个摘要节点。
//
// 源: packages/compaction/compaction-basic/src/region.ts:152-254
//
// 挑区间和验区间都是只读的。验日志尾巴和追加 compaction/start 之间**没有任何
// 会让出去的操作**，所以那条落库的开括号就是这次压缩的锁——它在总结让出控制权
// 之前就已经持有了。之后每一次失败都只做**一次** compaction/end 尝试；
// 那次尝试自己失败的话，那个没配对的开括号**故意**留在日志里看得见。
//
// region 收的是 [compaction.ShadowedRange] 而不是两个 int。
// 新增: DSH 是 `start: number, end: number`。这里用那个具名的对子，是因为
// [SelectCompactableRange] 交出来的正好是它——两个函数于是直接接得上，
// 中间不必拆开再按顺序拼回去。[compaction.Engine.CompactRegion] 那一侧仍然是
// 两个 int，由引擎现拼一个。
//
// 新增: DSH 另收一个 `session` 参数，而 agent 里本来就带着同一段会话。
// 这里只收 agent：两个都收等于给调用方留了一个「传进来的会话和 agent 不是同一段」
// 的口子，而那种错在日志上表现成「摘要是照着另一段对话写的」，不会报错。
func CompactSurfaceRegion(
	ctx context.Context,
	deps RegionDeps,
	balance *compaction.BalanceIndex,
	region compaction.ShadowedRange,
	agent compaction.AgentContext,
	options TransactionOptions,
) (compaction.Result, error) {
	live := agent.Session
	if options.Standalone {
		if err := ctx.Err(); err != nil {
			return compaction.Result{}, err
		}
	}

	assertStable, err := stabilityOf(options.Stability)
	if err != nil {
		return compaction.Result{}, err
	}
	selection, err := validateSurfaceRegion(balance, live, region)
	if err != nil {
		return compaction.Result{}, err
	}
	entry, err := InspectEntryState(live.Events())
	if err != nil {
		return compaction.Result{}, err
	}
	if err := entry.CheckInactive("compaction"); err != nil {
		return compaction.Result{}, err
	}

	lifecycle, err := openBracket(entry, options)
	if err != nil {
		return compaction.Result{}, err
	}
	startEvent, err := appendPayload(live, compaction.EventCompactionStart, lifecycle)
	if err != nil {
		return compaction.Result{}, err
	}

	// stage 记的是「失败发生在哪一步」，closing 记的是「摘要和替换已经落进日志、
	// 只差合上括号」。两者在下面那个闭包里被就地改写，闭包退出之后才读。
	stage := stageSummary
	closing, closed := false, false
	var result compaction.Result

	failure := func() error {
		prepared, err := prepareCompaction(deps, live, selection)
		if err != nil {
			return err
		}
		summarized, err := summarizeCompaction(ctx, deps, prepared, agent, lifecycle)
		if err != nil {
			return err
		}
		if options.Standalone {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := assertStable(deps, balance, live, prepared); err != nil {
			return err
		}
		stage = stageCommit
		pending, err := commitCompactionBody(live, startEvent, lifecycle, summarized)
		if err != nil {
			return err
		}
		closing = true
		endEvent, err := appendPayload(live, compaction.EventCompactionEnd, endOf(lifecycle, ""))
		if err != nil {
			return err
		}
		closed = true
		pending.EndSeq = endEvent.Seq
		result = pending
		return nil
	}()

	if failure != nil && !closing {
		// 括号还没开始合，就在这里合一次，把失败原因写进去。
		// 这一次也失败的话，那个没配对的开括号留在日志里——**不回滚**，
		// 因为一次做砸了的压缩本身就是要看得见的。
		closing = true
		if _, closeErr := appendPayload(live, compaction.EventCompactionEnd,
			endOf(lifecycle, failure.Error())); closeErr != nil {
			failure, stage = closeErr, stageCommit
		} else {
			closed = true
		}
	}

	var flushFailure error
	if closed && options.Flush != nil {
		flushFailure = options.Flush(ctx)
	}

	if options.Standalone {
		if err := ctx.Err(); err != nil {
			// 取消原样交回去，不裹成 [compaction.ManualError]：分类是引擎那一层
			// 的事（它才分得清取消来自这次请求还是来自那个 agent）。
			return compaction.Result{}, err
		}
	}
	if failure != nil {
		if options.Standalone {
			return compaction.Result{}, manualFailure(stage, failure)
		}
		return compaction.Result{}, failure
	}
	if flushFailure != nil {
		return compaction.Result{}, compaction.NewManualError(compaction.ManualErrorPersistence,
			"人工压缩的持久化检查点失败", flushFailure)
	}
	// DSH 在这里还有一句 `if (result === undefined) throw ...`，它自己也标了
	// 不可达。Go 这边连那个状态都表达不出来：走到这里 failure 必为 nil，
	// 而 failure 为 nil 只可能是上面那个闭包整条走完、result 已经赋过值。
	return result, nil
}

// stabilityOf 把口径换成对应的那个判定。
//
// 源: packages/compaction/compaction-basic/src/region.ts:191-193
//
// 新增: DSH 的 `stability` 是一个必填的两值联合，编译器保证它有值。Go 的
// 字符串零值是空串，落到哪一档都不对——而**默默按选中段那一档走**是最糟的：
// 那是两档里更宽松的一档，于是一次本该被拦下的表面改写会安静地通过。
func stabilityOf(stability Stability) (stabilityCheck, error) {
	switch stability {
	case StabilityWholeSurface:
		return assertWholeSurfaceUnchanged, nil
	case StabilitySelectedSpan:
		return assertSelectedSpanStable, nil
	default:
		return nil, fmt.Errorf("compaction-basic：稳定性口径 %q 不是 %q 或者 %q",
			stability, StabilityWholeSurface, StabilitySelectedSpan)
	}
}

// openBracket 定下这次事务的身份和归属。
//
// 源: packages/compaction/compaction-basic/src/region.ts:170-186
//
// 独立事务不许在一个开着的回合里做：那样这次压缩换掉的范围会横跨回合边界，
// 而括号只报得出一个归属。反过来，跟着回合走的那一档必须有一个开着的回合——
// 没有的话这条括号根本写不出合法的归属。
func openBracket(entry EntryState, options TransactionOptions) (compaction.StartData, error) {
	newID := options.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	lifecycle := compaction.StartData{
		CompactionID:    compaction.ID(newID()),
		SourceCommandID: options.SourceCommandID,
	}
	if options.Standalone {
		if entry.TurnIsOpen {
			return compaction.StartData{}, compaction.NewManualError(compaction.ManualErrorBusy,
				"人工压缩：这段会话上已经有一个回合开着", nil)
		}
		lifecycle.Standalone = true
		return lifecycle, nil
	}
	if !entry.TurnIsOpen {
		return compaction.StartData{}, errors.New(
			"compaction-basic：没有开着的回合——自动压缩的那几条事件必须裹在一个回合里")
	}
	lifecycle.Turn = entry.OpenTurn
	return lifecycle, nil
}

// manualFailure 把一次已经收好尾的人工尝试分类。
//
// 源: packages/compaction/compaction-basic/src/region.ts:257-277
//
// 新增: DSH 那句 `failure.stage: closing ? 'commit' : stage` 是恒等的——
// closing 只在 stage 已经是 'commit' 之后才为真。这里就只带一个 stage。
func manualFailure(stage string, err error) error {
	if stage == stageCommit {
		return compaction.NewManualError(compaction.ManualErrorCommit,
			"人工压缩没有干净地提交", err)
	}
	if errors.Is(err, errSurfaceChanged) {
		return compaction.NewManualError(compaction.ManualErrorChanged,
			"人工压缩期间被压的那段历史变了", err)
	}
	return compaction.NewManualError(compaction.ManualErrorSummary,
		"人工压缩没能产出一份更小的摘要", err)
}

// validateSurfaceRegion 在任何异步动作之前验一次要压的那一段。
//
// 源: packages/compaction/compaction-basic/src/region.ts:315-336
//
// 两头都必须配平：头上那一刀不许把上一个步骤的工具调用和它的结果劈开，
// 尾巴那一刀同理，而且那个步骤不能还开着。
func validateSurfaceRegion(
	balance *compaction.BalanceIndex,
	live *coresession.Session,
	region compaction.ShadowedRange,
) (surfaceSelection, error) {
	view := surfaceViewOf(live)
	startIdx := slices.Index(view.Nodes, region.Start)
	endIdx := slices.Index(view.Nodes, region.End)
	if startIdx == -1 {
		return surfaceSelection{}, fmt.Errorf(
			"compaction-basic：表面上没有 seq %d，压不了这一段的头", region.Start)
	}
	if endIdx == -1 {
		return surfaceSelection{}, fmt.Errorf(
			"compaction-basic：表面上没有 seq %d，压不了这一段的尾", region.End)
	}
	if startIdx > endIdx {
		return surfaceSelection{}, fmt.Errorf(
			"compaction-basic：头 seq %d（表面第 %d 个）排在尾 seq %d（表面第 %d 个）后面",
			region.Start, startIdx, region.End, endIdx)
	}

	balanced, err := balance.BalancedBefore(view, region.Start)
	if err != nil {
		return surfaceSelection{}, err
	}
	if !balanced {
		return surfaceSelection{}, fmt.Errorf(
			"compaction-basic：头 seq %d 不是一处配平的下刀点（会把一个步骤的工具调用和它的结果劈开）",
			region.Start)
	}
	balanced, err = balance.BalancedAfter(view, region.End)
	if err != nil {
		return surfaceSelection{}, err
	}
	if !balanced {
		return surfaceSelection{}, fmt.Errorf(
			"compaction-basic：尾 seq %d 不是一处配平的下刀点（会劈开一个步骤，或者那个步骤还开着）",
			region.End)
	}

	return surfaceSelection{
		Region:       region,
		StartIdx:     startIdx,
		EndIdx:       endIdx,
		ShadowedSeqs: slices.Clone(view.Nodes[startIdx : endIdx+1]),
	}, nil
}

// prepareCompaction 给一段验过的区间照一张估价快照，并重放出总结要的输入。
//
// 源: packages/compaction/compaction-basic/src/region.ts:339-357
//
// 新增: DSH 那句 `measurement.nodes.slice(startIdx, endIdx + 1)` 越界时只会
// 悄悄给出一段短切片，靠后面逐个比 seq 才发现不对。Go 的切片越界直接 panic，
// 所以下标先验一次；判定的归属不变——那仍然是「表面变了」，不是内部错误。
func prepareCompaction(
	deps RegionDeps,
	live *coresession.Session,
	selection surfaceSelection,
) (preparedCompaction, error) {
	priced, err := deps.Meter.PriceSurface(live)
	if err != nil {
		return preparedCompaction{}, err
	}
	selected, err := sliceSelected(priced, selection)
	if err != nil {
		return preparedCompaction{}, err
	}
	total := 0
	for _, node := range selected {
		total += node.Tokens
	}
	input, err := buildSummarizationInput(live, selection.ShadowedSeqs)
	if err != nil {
		return preparedCompaction{}, err
	}
	return preparedCompaction{
		surfaceSelection:   selection,
		Priced:             priced,
		Selected:           selected,
		ShadowedTokenCount: total,
		Input:              input,
	}, nil
}

// sliceSelected 从整个表面的估价里切出选中那一段，并确认它还是原来那些节点。
func sliceSelected(priced []PricedNode, selection surfaceSelection) ([]PricedNode, error) {
	if selection.EndIdx >= len(priced) {
		return nil, fmt.Errorf("%w：计量器只算出 %d 个表面节点，选中的那一段要到第 %d 个",
			errSurfaceChanged, len(priced), selection.EndIdx)
	}
	selected := priced[selection.StartIdx : selection.EndIdx+1]
	for index, node := range selected {
		if node.Seq != selection.ShadowedSeqs[index] {
			return nil, fmt.Errorf("%w：选中那一段第 %d 个节点该是 seq %d，计量器算的是 seq %d",
				errSurfaceChanged, index, selection.ShadowedSeqs[index], node.Seq)
		}
	}
	return selected, nil
}

// summarizeCompaction 跑一次总结，并把结果裹成那条检查点消息。
//
// 源: packages/compaction/compaction-basic/src/region.ts:360-384
//
// 裹好之后要比被遮的内容**更小**才算数：一份不比原文小的摘要压完之后上下文
// 没省下来，而那一段真历史已经换掉了——这次压缩纯亏。
func summarizeCompaction(
	ctx context.Context,
	deps RegionDeps,
	prepared preparedCompaction,
	agent compaction.AgentContext,
	lifecycle compaction.StartData,
) (summarizedCompaction, error) {
	summary, err := deps.Summarize(ctx, prepared.Input, agent)
	if err != nil {
		return summarizedCompaction{}, err
	}
	source, err := compaction.NewCheckpointSource(compaction.CheckpointSource{
		CompactionID:    lifecycle.CompactionID,
		SourceCommandID: lifecycle.SourceCommandID,
	})
	if err != nil {
		return summarizedCompaction{}, err
	}
	checkpoint := llm.NewUserMessage(FrameSummary(summary.Summary), source)
	framed, err := deps.Meter.EstimateMessage(checkpoint)
	if err != nil {
		return summarizedCompaction{}, err
	}
	if framed >= prepared.ShadowedTokenCount {
		return summarizedCompaction{}, fmt.Errorf(
			"compaction-basic：这份摘要没比被遮的内容小（裹好之后估 %d 个 token，被遮的是 %d 个）",
			framed, prepared.ShadowedTokenCount)
	}
	return summarizedCompaction{
		preparedCompaction: prepared,
		SummaryResult:      summary,
		Checkpoint:         checkpoint,
	}, nil
}

// assertWholeSurfaceUnchanged 拒掉一份建立在任何更早的表面代数上的摘要。
//
// 源: packages/compaction/compaction-basic/src/region.ts:387-396
func assertWholeSurfaceUnchanged(
	deps RegionDeps,
	_ *compaction.BalanceIndex,
	live *coresession.Session,
	prepared preparedCompaction,
) error {
	current, err := deps.Meter.PriceSurface(live)
	if err != nil {
		return err
	}
	if !slices.Equal(current, prepared.Priced) {
		return fmt.Errorf("%w：总结期间这段会话的表面变了", errSurfaceChanged)
	}
	return nil
}

// assertSelectedSpanStable 只要求选中的那一段还在、还连着、估价还一样、两头还配平。
//
// 源: packages/compaction/compaction-basic/src/region.ts:398-424
//
// 它比整表面那一档松一档：在这一段**外面**新长出来的节点仍然可见，
// 不会被这次替换动到，所以它们不该让一份已经做好的摘要作废。
func assertSelectedSpanStable(
	deps RegionDeps,
	balance *compaction.BalanceIndex,
	live *coresession.Session,
	prepared preparedCompaction,
) error {
	current, err := validateSurfaceRegion(balance, live, prepared.Region)
	if err != nil {
		return fmt.Errorf("%w：选中的那一段不再是一个能用的替换目标：%w", errSurfaceChanged, err)
	}
	if !slices.Equal(current.ShadowedSeqs, prepared.ShadowedSeqs) {
		return fmt.Errorf("%w：总结期间选中的那一段变了", errSurfaceChanged)
	}
	priced, err := deps.Meter.PriceSurface(live)
	if err != nil {
		return err
	}
	measured, err := sliceSelected(priced, current)
	if err != nil {
		return err
	}
	if !slices.Equal(measured, prepared.Selected) {
		return fmt.Errorf("%w：总结期间选中的那一段被改写了", errSurfaceChanged)
	}
	return nil
}

// commitCompactionBody 一口气追加摘要事件和那条替换消息，中间不让出去。
//
// 源: packages/compaction/compaction-basic/src/region.ts:427-478
//
// 两条必须紧挨着：一条 replace 的价格由**紧挨在它前面**的那条计价事件给出，
// 消费方按这个相邻关系配对（见 compaction 包的包文档）。中间插进任何一条事件，
// 这次替换的影子价格就再也查不到了。
//
// 新增: DSH 从 `startEvent.data` 上把身份和命令读回来。Go 这边直接收那份
// [compaction.StartData]——它就是刚刚排出去的那一份，再解一次只会多出一条
// 永远走不到的解码失败分支。
func commitCompactionBody(
	live *coresession.Session,
	startEvent sessionlog.Event,
	lifecycle compaction.StartData,
	summarized summarizedCompaction,
) (compaction.Result, error) {
	shadowedSeqs := slices.Clone(summarized.ShadowedSeqs)
	summaryEvent, err := appendPayload(live, compaction.EventCompactionSummary, compaction.SummaryData{
		CompactionID:       lifecycle.CompactionID,
		SourceCommandID:    lifecycle.SourceCommandID,
		Summary:            summarized.Summary,
		ShadowedRange:      summarized.Region,
		ShadowedSeqs:       shadowedSeqs,
		ShadowedTokenCount: summarized.ShadowedTokenCount,
		Provider:           summarized.Provider,
		Model:              summarized.Model,
		MaxTokens:          summarized.MaxTokens,
		Usage:              summarized.Usage,
		RawOutput:          summarized.RawOutput,
		LLMStreamCall:      summarized.LLMStreamCall,
	})
	if err != nil {
		return compaction.Result{}, err
	}

	sources := make([]int, 0, len(shadowedSeqs)+2)
	sources = append(sources, startEvent.Seq, summaryEvent.Seq)
	sources = append(sources, shadowedSeqs...)
	payload, err := json.Marshal(sessionlog.UserMessageData{Message: summarized.Checkpoint})
	if err != nil {
		return compaction.Result{}, fmt.Errorf("compaction-basic：那条检查点消息排不出去：%w", err)
	}
	if _, err := live.Append(sessionlog.Event{
		Type:            sessionlog.EventUserMessage,
		Data:            payload,
		SurfaceOp:       sessionlog.ReplaceOp{Start: summarized.Region.Start, End: summarized.Region.End},
		SourceEventSeqs: sources,
	}); err != nil {
		return compaction.Result{}, fmt.Errorf("compaction-basic：那条检查点消息写不进日志：%w", err)
	}

	// EndSeq 留成零值：括号还没合上，由调用方在 compaction/end 落地之后补。
	return compaction.Result{
		CompactionID:       lifecycle.CompactionID,
		SourceCommandID:    lifecycle.SourceCommandID,
		StartSeq:           startEvent.Seq,
		SummarySeq:         summaryEvent.Seq,
		Summary:            summarized.Summary,
		ShadowedRange:      summarized.Region,
		ShadowedSeqs:       shadowedSeqs,
		ShadowedTokenCount: summarized.ShadowedTokenCount,
	}, nil
}

// buildSummarizationInput 按被遮那一段原本被路由出去的样子重放它。
//
// 源: packages/compaction/compaction-basic/src/region.ts:498-514
//
// 系统提示词、工具表取自最近那份请求头，后面接被遮那些节点自己的派生消息，
// 按表面顺序。总结那一步只在这后面追加一条指令，于是那次调用**恰好是**
// 上一次路由请求的一个前缀，提供方那边的 KV 缓存因此是复用而不是作废。
func buildSummarizationInput(
	live *coresession.Session,
	shadowedSeqs []int,
) (SummarizationInput, error) {
	var input SummarizationInput
	header, ok, err := live.RequestHeader()
	if err != nil {
		return SummarizationInput{}, err
	}
	if ok {
		input.System = header.System
		input.Tools = header.Tools
	}

	events := live.Events()
	base := baseSeqOf(events)
	messages := make([]llm.Message, 0, len(shadowedSeqs))
	for _, seq := range shadowedSeqs {
		index := seq - base
		if index < 0 || index >= len(events) || events[index].Seq != seq {
			// 新增: DSH 直接 `events[seq]!`——它那边 seq 就是数组下标。本仓库的
			// 会话可能只拿着一段后缀（[coresession.Session.FirstLiveSeq]），
			// 所以下标要减掉基准；对不上就是表面和日志不一致，和
			// [compaction.SurfaceView] 那一处报同一条错。
			return SummarizationInput{}, fmt.Errorf(
				"%w：表面上的 seq %d 在日志里找不到对应的事件", compaction.ErrSurfaceCorrupt, seq)
		}
		message, produced, err := live.DeriveEventMessage(events[index])
		if err != nil {
			return SummarizationInput{}, err
		}
		if produced {
			messages = append(messages, message)
		}
	}
	input.Messages = messages
	return input, nil
}

// surfaceViewOf 现拼一份 [compaction.SurfaceView] 给工具配对那一层用。
//
// 这三样是分三次读出来的，中间没有锁。压缩本来就要求这段会话此刻只有一个写入方
// （自动那一档裹在回合里，人工那一档占着 agent 的空闲期），所以这里不额外加锁——
// 加了也只能挡住这一处，挡不住紧接着的那几次追加。
func surfaceViewOf(live *coresession.Session) compaction.SurfaceView {
	events := live.Events()
	return compaction.SurfaceView{
		Nodes:      live.SurfaceNodes(),
		Generation: live.SurfaceReplaceGeneration(),
		Events:     events,
		BaseSeq:    baseSeqOf(events),
	}
}

// baseSeqOf 交出这段事件里头一条的 seq，空日志时是 0。
func baseSeqOf(events []sessionlog.Event) int {
	if len(events) == 0 {
		return 0
	}
	return events[0].Seq
}

// endOf 按开括号那份负载排出配对的闭括号。
func endOf(lifecycle compaction.StartData, failure string) compaction.EndData {
	return compaction.EndData{
		CompactionID:    lifecycle.CompactionID,
		SourceCommandID: lifecycle.SourceCommandID,
		Turn:            lifecycle.Turn,
		Standalone:      lifecycle.Standalone,
		Error:           failure,
	}
}

// appendPayload 把一条 compaction/* 事件排出去并追加进日志。
//
// 新增: [sessionlog.EventData] 是个封闭接口，本包的三种负载进不去那个联合，
// 所以这里自己排一次字节，理由和 compaction.decodePayload 那一处对称。
func appendPayload(
	live *coresession.Session,
	kind sessionlog.EventType,
	payload any,
) (sessionlog.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("compaction-basic：%s 的负载排不出去：%w", kind, err)
	}
	event, err := live.Append(sessionlog.Event{Type: kind, Data: data})
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("compaction-basic：%s 写不进日志：%w", kind, err)
	}
	return event, nil
}
