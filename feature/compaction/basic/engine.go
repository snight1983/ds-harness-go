// 本文件的作用：把前面那四样（配置、挑区间、总结、事务）拼成一个真的压缩后端——
// 什么时候压、超窗那一路怎么绕开平常那条线、压完还在线上怎么再来一次，
// 以及一次人工压缩怎么占住空闲期、失败之后怎么分类。
//
// 源: packages/compaction/compaction-basic/src/index.ts

package basic

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
)

// PressureMeter 是引擎要的那一小片计量器：比事务那一层多一个「总量」。
//
// 源: packages/compaction/compaction-basic/src/index.ts:266-267（ctx.tokenMeter）
//
// 新增: DSH 一次 `meter.measure()` 同时拿到 `.nodes` 和 `.totalTokens` 两样。
// 本仓库把它拆成两个方法，是因为**用它们的不是同一层**：事务只按节点定价
// （[Meter]），压力判定还要连系统提示和工具表那份基线一起算进去，而那份基线
// 只有引擎关心。拆开之后事务在类型上就读不到总量，也就不可能拿总量去做
// 一次本该按节点做的判断。
//
// 代价是引擎在一轮里会分两次问计量器。真的那台计量器按日志代数缓存，
// 同一代问两次只折一次；就算不缓存，一次压缩本来就要发一趟模型请求，
// 多折一遍是可以忽略的。
type PressureMeter interface {
	Meter

	// TotalTokens 交出这段会话**下一次请求信封**的总估价：系统提示、工具表,
	// 加上表面上全部节点。压力判定和 [Spec.ThresholdTokens] 比的就是它。
	TotalTokens(live *coresession.Session) (int, error)
}

// ModelInfoResolver 是引擎要的那一小片模型目录：问出某个模型的窗口有多大。
//
// 源: packages/compaction/compaction-basic/src/index.ts:293（ctx.llm.resolveModelInfo）
//
// 新增: DSH 从 cordis 上取整个 `ctx.llm` 服务。这里摆成一个单方法接口明着传进来，
// 签名和 [llm.Runtime.ResolveModelInfo] 逐字相同，于是一个真的运行时结构上就满足
// 它，装配方直接填进去。做法和 [Streamer]、[Meter] 那两处相同。
//
// 它和 [Streamer] 没有合成一个，是因为**只有压力那一路要它**：超窗那一路和
// [CompactRegion]、[CompactNow] 都用不上窗口大小。合起来等于让一份只做人工压缩的
// 装配也得备一份模型目录。
type ModelInfoResolver interface {
	// ResolveModelInfo 解出某条路由上那个模型的完整信息。
	ResolveModelInfo(ctx context.Context, provider string, model string) (llm.ResolvedModelInfo, error)
}

// EngineDeps 是 [Engine] 跑起来要的那几样外部能力。
//
// 源: packages/compaction/compaction-basic/src/index.ts:104（static inject）
//
// 新增: DSH 的 `static inject = ['llm', 'tokenMeter', 'sessions']` 是一句声明，
// 真正的绑定由 cordis 在装载时完成，而 `toolResultPruner` 是运行期
// `ctx.get()` 出来的可选兄弟服务。Go 里没有那个容器，四样一律摆成字段：
// 必填的两样为 nil 时 [NewEngine] 当场报错，可选的两样为 nil 就是「没有」。
type EngineDeps struct {
	// Meter 是那把固定的尺子，必填。表面定价、检查点估价和压力判定共用它——
	// 共用是有讲究的：压力线是拿它量出来的，缩减效果也得拿同一把尺子量，
	// 换一把就可能出现「按 A 尺该压、按 B 尺压完反而更大」。
	Meter PressureMeter

	// Models 是模型目录，压力那一路必填。
	Models ModelInfoResolver

	// Stream 是发那次总结调用的接缝。Summarize 为 nil 时必填。
	Stream Streamer

	// Summarize 是替换掉默认总结的那个钩子；nil 表示用 [SummarizeWithLLM]。
	//
	// 源: packages/compaction/compaction-basic/src/index.ts:236-246
	//
	// 新增: DSH 把 `summarize()` 写成一个 protected 方法，子类覆盖它就换掉了
	// 摘要来源，而 `compactRegion` 通过 `this.summarize(...)` 动态分派拿到覆盖后的
	// 那一个。Go 没有方法覆盖，同一个效果做成一个函数字段：填了就用填的那个，
	// 没填就回落到默认实现。两者的可替换范围逐字相同——**只有摘要这一步**能换，
	// 重放和落库的策略仍然钉死，所以每一次定价都还是同一台计量器算的。
	Summarize Summarize

	// Prune 是那一遍不过模型的剪枝；nil 表示这份装配里没有剪枝器。
	//
	// 源: packages/compaction/compaction-basic/src/index.ts:281（ctx.get('toolResultPruner')）
	//
	// 新增: DSH 是一个运行期取出来的可选兄弟服务，取不到就跳过这一步——
	// 为的是让 compaction-basic 单独也拼得起来。Go 里同一件事就是一个可以为 nil
	// 的函数字段（成例是 [TransactionOptions.Flush]）。
	//
	// 摆成函数而不是接口，是因为真的那个剪枝器
	// （compaction/toolresultpruner.Pruner.PruneSession）还要收一台计量器——
	// 那是本仓库明着传依赖的做法，而装配方手上正好有 [EngineDeps.Meter]，
	// 现包一层闭包把它绑上去就行。写成接口反而要本包去 import 那个包，
	// 把一条**可选**的依赖变成一条编译期的硬边。
	Prune func(live *coresession.Session) error

	// Flush 是人工压缩合上括号之后那次持久化检查点；nil 表示不做。
	//
	// 源: packages/compaction/compaction-basic/src/index.ts:396-401（ctx.sessions.flush）
	Flush func(ctx context.Context, live *coresession.Session) error

	// NewID 铸每次压缩事务的身份；nil 时由事务那一层用 uuid。
	NewID func() string
}

// Engine 是那个默认的压缩后端。
//
// 源: packages/compaction/compaction-basic/src/index.ts:103-429
//
// 新增: DSH 是 `class BasicCompactionEngine extends CompactionEngine`，构造时
// 顺手把自己挂到 `ctx.compaction` 上，并且在 `auto` 开着时**在构造函数里**注册
// 那几个观察者。Go 这边拆成两件事：本类型只是一个满足 [compaction.Engine] 的值，
// 挂观察者由 [Install] 单独做。拆开的理由是那几个观察者要一个活的 agent 注册表
// 和一段 scope 生命期，而这两样和「压一次」这件事本身无关——一份只想手动调
// [Engine.CompactNow] 的装配不该被迫先备一个注册表。
//
// 本类型自己**不带任何可变状态**：那几张 DSH 挂在 WeakMap 上的重试计数
// （overflowRetries / overflowAgents）和那份「已经警告过的路由」都归 [Install]，
// 因为它们是自动压缩那条链路的状态，不是压缩本身的。
type Engine struct {
	config ResolvedConfig
	deps   EngineDeps
}

// 编译期确认它真的是一个压缩后端。
var _ compaction.Engine = (*Engine)(nil)

// NewEngine 用一份验过的配置和一组依赖造一个后端。
//
// 源: packages/compaction/compaction-basic/src/index.ts:126-130
//
// 新增: DSH 的构造函数收的是一份**没验过**的配置，自己调 `resolveConfig`。
// Go 这边 [ResolvedConfig] 的唯一入口是 [Config.Resolve]，所以这里收的已经是
// 验过的那一份——一份没验过的配置在类型上就传不进来。
func NewEngine(config ResolvedConfig, deps EngineDeps) (*Engine, error) {
	if deps.Meter == nil {
		return nil, configFailure("引擎要一台计量器")
	}
	if deps.Summarize == nil && deps.Stream == nil {
		return nil, configFailure("引擎要么给一个 Summarize 钩子，要么给一个 Stream 接缝")
	}
	return &Engine{config: config, deps: deps}, nil
}

// Config 交出这个后端手上那份验过的配置。
//
// 源: packages/compaction/compaction-basic/src/index.ts:119-120
func (e *Engine) Config() ResolvedConfig { return e.config }

// CompactIfNeeded 就一个明确的触发原因考虑一次自动压缩。
//
// 源: packages/compaction/compaction-basic/src/index.ts:258-332
//
// 两条触发共用一件事：定价看的都是**最近一次落库的、带路由的请求信封**。
// 差别在于超窗那一条绕开了平常那条压力线和保留尾巴——提供方已经确认装不下了，
// 这时候还去问「到线了没」是没有意义的，要的是**一次有用的、配平的缩减**。
//
// 这段会话还没发过一次带路由的请求时第二个返回值是 false：没有路由就没有窗口，
// 也就折算不出压力线。这不是错误，是一段刚开头的会话的正常样子。
func (e *Engine) CompactIfNeeded(
	ctx context.Context,
	agent compaction.AgentContext,
	trigger compaction.Trigger,
) (compaction.Result, bool, error) {
	target, ok, err := routedTarget(agent.Session)
	if err != nil {
		return compaction.Result{}, false, err
	}
	if !ok {
		return compaction.Result{}, false, nil
	}

	// 一次调用配一份配对状态：这一路上要问好几次「这一刀劈不劈得开工具调用」
	// （挑区间一次、事务里前后各一次），共享一份省掉重复的整表面重建。
	//
	// 新增: DSH 把它挂在一张以 Session 为键的 WeakMap 上，跨调用也复用。这里
	// 每次现造一份：[compaction.BalanceIndex] 的零值可用，代数对不上本来就要
	// 整个重建，而两次压缩之间表面必然已经变过——那份跨调用的缓存**恒定失效**。
	// 换来的是本类型一个可变字段都不带，也就不必为它上锁。
	balance := &compaction.BalanceIndex{}

	switch trigger {
	case compaction.TriggerContextOverflow:
		return e.compactOverflow(ctx, agent, balance)
	case compaction.TriggerPressure:
		return e.compactPressure(ctx, agent, e.config.ForTarget(target), balance)
	default:
		// 新增: DSH 是一句 `assertNever(trigger)`，靠 TS 的封闭联合在编译期
		// 保证走不到。Go 的具名字符串类型是开的，一个手写的 [compaction.Trigger]
		// 进得来，所以这里是一条真的运行期失败。
		return compaction.Result{}, false, fmt.Errorf(
			"compaction-basic：触发原因 %q 不是 %q 或者 %q",
			trigger, compaction.TriggerPressure, compaction.TriggerContextOverflow)
	}
}

// compactOverflow 是超窗补救那一路：不看压力线、不留尾巴，强行做一次配平的缩减。
//
// 源: packages/compaction/compaction-basic/src/index.ts:283-291
//
// 保留预算传 0 而不是策略里那个数：提供方已经确认这一份装不下了，按平常的尾巴
// 去留只会挑出一段更短的区间、缩得更少，于是下一次请求接着超。传 0 之后
// [SelectCompactableRange] 仍然至少留下最后一个节点，所以这一刀不会把表面清空。
func (e *Engine) compactOverflow(
	ctx context.Context,
	agent compaction.AgentContext,
	balance *compaction.BalanceIndex,
) (compaction.Result, bool, error) {
	if err := e.prune(agent.Session); err != nil {
		return compaction.Result{}, false, err
	}
	priced, err := e.deps.Meter.PriceSurface(agent.Session)
	if err != nil {
		return compaction.Result{}, false, err
	}
	region, ok, err := SelectCompactableRange(surfaceViewOf(agent.Session), balance, priced, 0)
	if err != nil || !ok {
		return compaction.Result{}, false, err
	}
	result, err := e.compactRegion(ctx, balance, region, agent)
	if err != nil {
		return compaction.Result{}, false, err
	}
	return result, true, nil
}

// compactPressure 是步骤边界那一路：先折算这条路由的压力线，到线了才动手。
//
// 源: packages/compaction/compaction-basic/src/index.ts:293-331
//
// 剪枝那一步排在挑区间**之前**：它不花钱也不过模型，先把它落地，再决定这一次
// 要总结哪一段。落完之后重新量一遍——光靠剪枝就降到线下的话，这一步就省掉了
// 一次总结调用。
//
// 压完还在线上就再来一次，最多 [Policy.CompactionRetries] 次追加尝试。全都用完
// 还在线上时报错而不是默默返回：那意味着这份配置或者这段历史上，表面压缩这个
// 手段已经不管用了（比如一个大得离谱的保留单元），而**安静地放过去**的后果是
// 下一次请求被提供方拒掉，那时候已经查不到是这里没压下来。
func (e *Engine) compactPressure(
	ctx context.Context,
	agent compaction.AgentContext,
	policy TargetPolicy,
	balance *compaction.BalanceIndex,
) (compaction.Result, bool, error) {
	live := agent.Session
	info, err := e.deps.Models.ResolveModelInfo(ctx, policy.Target.Provider, policy.Target.Model)
	if err != nil {
		return compaction.Result{}, false, err
	}
	// 这一句排在窗口检查**之前**，和 DSH 逐字同序：一次压缩还开着的时候，
	// 就算窗口也没配好，先报出来的也该是那把没放的锁——它才是这一刻真正拦住
	// 开工的那件事，而配置问题下一个步骤边界上照样报得出来。
	if err := CheckNoActiveCompaction(live.Events(), "自动压力压缩"); err != nil {
		return compaction.Result{}, false, err
	}
	key := policy.Target.Key()
	if info.Context == nil {
		return compaction.Result{}, false, targetPressureFailure(key,
			"%s 没有上下文容量：给那个适配器模型配上 contextWindow", key)
	}
	spec, err := policy.Spec(info.Context.ContextWindow)
	if err != nil {
		return compaction.Result{}, false, err
	}

	total, err := e.deps.Meter.TotalTokens(live)
	if err != nil {
		return compaction.Result{}, false, err
	}
	if total < spec.ThresholdTokens {
		return compaction.Result{}, false, nil
	}
	if e.deps.Prune != nil {
		if err := e.prune(live); err != nil {
			return compaction.Result{}, false, err
		}
		if total, err = e.deps.Meter.TotalTokens(live); err != nil {
			return compaction.Result{}, false, err
		}
		if total < spec.ThresholdTokens {
			return compaction.Result{}, false, nil
		}
	}

	var result compaction.Result
	compacted := false
	for attempt := 0; attempt <= spec.CompactionRetries; attempt++ {
		priced, err := e.deps.Meter.PriceSurface(live)
		if err != nil {
			return compaction.Result{}, false, err
		}
		region, ok, err := SelectCompactableRange(
			surfaceViewOf(live), balance, priced, spec.RetainTokens)
		if err != nil {
			return compaction.Result{}, false, err
		}
		if !ok {
			if !compacted {
				return compaction.Result{}, false, nil
			}
			// 不可达：上一轮换上去的那条检查点消息本身就是一个可压的节点，
			// 所以压过一次之后一定还挑得出区间。留着是因为**摘要那个钩子是可换的**,
			// 而一个换掉的钩子理论上能产出一条挑不中的替换消息。DSH 那边也标了
			// 同一条不可达。
			break
		}
		if result, err = e.compactRegion(ctx, balance, region, agent); err != nil {
			return compaction.Result{}, false, err
		}
		compacted = true
		if total, err = e.deps.Meter.TotalTokens(live); err != nil {
			return compaction.Result{}, false, err
		}
		if total < spec.ThresholdTokens {
			return result, true, nil
		}
	}

	return compaction.Result{}, false, fmt.Errorf(
		"compaction-basic：压了 %d 次之后仍然在压力线上（估计 %d 个 token ≥ 压力线 %d）",
		spec.CompactionRetries+1, total, spec.ThresholdTokens)
}

// CompactRegion 强行把一段表面节点压成一个摘要节点。
//
// 源: packages/compaction/compaction-basic/src/index.ts:343-358
//
// 稳定性口径是整表面那一档：这条路是给一个跑着的回合用的，总结期间表面本来
// 就不该有别的写入方。
func (e *Engine) CompactRegion(
	ctx context.Context,
	start int,
	end int,
	agent compaction.AgentContext,
) (compaction.Result, error) {
	return e.compactRegion(ctx, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: start, End: end}, agent)
}

// CompactNow 在还没到自动压力线的时候显式压掉有用的历史。
//
// 源: packages/compaction/compaction-basic/src/index.ts:368-420
//
// 保留预算传 0：这是人明着要的一次压缩，按平常那条尾巴去留会让它经常一段都挑
// 不出来，而那种「点了没反应」正是人工命令最不该有的表现。
//
// 新增: DSH 用 `AbortSignal.any([agentSignal, signal])` 把这次请求的取消和那个
// agent 自己的取消并起来。Go 里不用并：[compaction.Maintainer.RunMaintenance]
// 交给任务的那条 ctx 本来就是从传进去的 ctx 派生的，两边任意一侧取消都到得了
// 任务手上。分类那一半照样分得开——外层那条 ctx 自己断了就是这次请求被取消，
// 否则就是那个 agent 把这段空闲期收回去了。
//
// 新增: DSH 那个 `try { return agent.runMaintenance(...) } catch { throw busy }`
// 只接得住**同步**抛出来的「占着」，任务自己的失败走的是 promise 的拒绝。Go 里
// 两者混在同一个返回值里，所以照 feature/schedule 那一处的成例：任务**永远
// 交回 nil**，真正的结论由闭包捞出来，于是 RunMaintenance 的错就只剩一种含义。
func (e *Engine) CompactNow(
	ctx context.Context,
	agent compaction.ManualAgentContext,
	sourceCommandID string,
) (compaction.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return compaction.Result{}, false, err
	}

	var (
		result    compaction.Result
		compacted bool
		failure   error
		claimed   context.Context
	)
	if err := agent.Maintainer.RunMaintenance(ctx, func(claimCtx context.Context) error {
		claimed = claimCtx
		result, compacted, failure = e.compactUnderClaim(claimCtx, agent, sourceCommandID)
		return nil
	}); err != nil {
		return compaction.Result{}, false, compaction.NewManualError(compaction.ManualErrorBusy,
			"人工压缩要一个空闲、且没有排着唤醒活儿的 agent", err)
	}
	if failure == nil {
		return result, compacted, nil
	}
	// 这次请求自己被取消了：原样交回那条取消，不裹成人工失败——调用方要的是
	// 「是我取消的」这个事实，不是一个分了类的结果。
	if err := ctx.Err(); err != nil {
		return compaction.Result{}, false, err
	}
	if claimed != nil && claimed.Err() != nil {
		return compaction.Result{}, false, compaction.NewManualError(
			compaction.ManualErrorCancelled, "人工压缩被取消了", failure)
	}
	return compaction.Result{}, false, failure
}

// compactUnderClaim 是拿到那段空闲期之后真的做的事。
//
// 源: packages/compaction/compaction-basic/src/index.ts:375-411
func (e *Engine) compactUnderClaim(
	ctx context.Context,
	agent compaction.ManualAgentContext,
	sourceCommandID string,
) (compaction.Result, bool, error) {
	if err := ctx.Err(); err != nil {
		return compaction.Result{}, false, err
	}
	live := agent.Session
	priced, err := e.deps.Meter.PriceSurface(live)
	if err != nil {
		return compaction.Result{}, false, err
	}
	balance := &compaction.BalanceIndex{}
	region, ok, err := SelectCompactableRange(surfaceViewOf(live), balance, priced, 0)
	if err != nil || !ok {
		return compaction.Result{}, false, err
	}

	var flush func(context.Context) error
	if e.deps.Flush != nil {
		flush = func(ctx context.Context) error { return e.deps.Flush(ctx, live) }
	}
	result, err := CompactSurfaceRegion(ctx, e.regionDeps(), balance, region, agent.AgentContext,
		TransactionOptions{
			Standalone:      true,
			Stability:       StabilitySelectedSpan,
			SourceCommandID: sourceCommandID,
			Flush:           flush,
			NewID:           e.deps.NewID,
		})
	if err != nil {
		return compaction.Result{}, false, err
	}
	return result, true, nil
}

// compactRegion 是那两条自动路共用的一刀：跟着回合走、整表面那一档稳定性。
//
// 源: packages/compaction/compaction-basic/src/index.ts:349-357
func (e *Engine) compactRegion(
	ctx context.Context,
	balance *compaction.BalanceIndex,
	region compaction.ShadowedRange,
	agent compaction.AgentContext,
) (compaction.Result, error) {
	return CompactSurfaceRegion(ctx, e.regionDeps(), balance, region, agent, TransactionOptions{
		Stability: StabilityWholeSurface,
		NewID:     e.deps.NewID,
	})
}

// regionDeps 把那把固定的尺子和这次的总结钩子绑给事务。
//
// 源: packages/compaction/compaction-basic/src/index.ts:423-428
func (e *Engine) regionDeps() RegionDeps {
	return RegionDeps{Meter: e.deps.Meter, Summarize: e.summarize}
}

// summarize 是那个可换的摘要钩子，没换就跑默认的那次复用缓存的总结调用。
//
// 源: packages/compaction/compaction-basic/src/index.ts:236-246
//
// 摘要路由按**这段对话此刻的路由**去查覆盖表：一份按模型配的策略常常连摘要
// 用哪个模型一起配了，而那条覆盖是挂在对话模型上的，不是挂在摘要模型上的。
func (e *Engine) summarize(
	ctx context.Context,
	input SummarizationInput,
	agent compaction.AgentContext,
) (SummaryResult, error) {
	if e.deps.Summarize != nil {
		return e.deps.Summarize(ctx, input, agent)
	}
	policy, err := e.conversationPolicy(agent)
	if err != nil {
		return SummaryResult{}, err
	}
	return SummarizeWithLLM(ctx, e.deps.Stream, policy, input, agent)
}

// conversationPolicy 按这段对话此刻的路由合并出策略。
//
// 源: packages/compaction/compaction-basic/src/index.ts:241-244
//
// 路由认不出来时用默认策略，而不是报错：摘要那一步自己还会去
// [summarizationTarget] 里回落一次，那里才是真正决定发给谁的地方。
func (e *Engine) conversationPolicy(agent compaction.AgentContext) (Policy, error) {
	target, ok, err := conversationTarget(agent)
	if err != nil {
		return Policy{}, err
	}
	if !ok {
		return e.config.Policy, nil
	}
	return e.config.ForTarget(target).Policy, nil
}

// prune 跑那一遍不过模型的剪枝；没配剪枝器就什么都不做。
func (e *Engine) prune(live *coresession.Session) error {
	if e.deps.Prune == nil {
		return nil
	}
	if err := e.deps.Prune(live); err != nil {
		return fmt.Errorf("compaction-basic：剪枝那一遍失败：%w", err)
	}
	return nil
}

// routedTarget 解出最近一次**落库的**请求信封上那条路由。
//
// 源: packages/compaction/compaction-basic/src/index.ts:52-60
//
// 看落库的那一份而不是 agent 的选项，是因为压力要按**模型真的收到了什么**来
// 判断：系统提示、工具表和那一串消息都在那份信封里，而 agent 选项上只有一个
// 模型名。两个字段任意一个是空的就当没有路由——一份半截的路由查不出窗口。
func routedTarget(live *coresession.Session) (Target, bool, error) {
	header, ok, err := live.RequestHeader()
	if err != nil {
		return Target{}, false, err
	}
	if !ok || header.Config.Provider == "" || header.Config.Model == "" {
		return Target{}, false, nil
	}
	return Target{Provider: header.Config.Provider, Model: header.Config.Model}, true, nil
}

// conversationTarget 解出「这段对话算是哪个模型的」，用来挑一条可选的策略覆盖。
//
// 源: packages/compaction/compaction-basic/src/index.ts:62-71
//
// 比 [routedTarget] 多一层回落：还没发过请求的会话没有信封，这时候用 agent
// 自己那两个选项。它只影响挑哪条覆盖，不影响压力判定——压力那一路要的是窗口，
// 而一个还没发过请求的会话本来也没有压力可言。
func conversationTarget(agent compaction.AgentContext) (Target, bool, error) {
	target, ok, err := routedTarget(agent.Session)
	if err != nil || ok {
		return target, ok, err
	}
	if agent.Provider == "" || agent.Model == "" {
		return Target{}, false, nil
	}
	return Target{Provider: agent.Provider, Model: agent.Model}, true, nil
}
