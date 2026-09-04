// 本文件的作用：压缩这道接缝本身——为什么要压、人工压缩会怎么失败、
// 一个压缩后端要收哪一小片 agent，以及它必须交出哪三个方法。
//
// 源: packages/compaction/compaction/src/index.ts:24-172

package compaction

import (
	"context"
	"fmt"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
)

// Trigger 是自动策略为什么在问后端「要不要压一次」。
//
// 源: packages/compaction/compaction/src/index.ts:24-25（CompactionTrigger）
type Trigger string

const (
	// TriggerPressure 是历史长到该压了，但还没有真的撑爆。
	TriggerPressure Trigger = "pressure"
	// TriggerContextOverflow 是上下文已经装不下了。
	TriggerContextOverflow Trigger = "context-overflow"
)

// ManualErrorCode 是一次显式的人工压缩请求可以预期的那几类失败。
//
// 源: packages/compaction/compaction/src/index.ts:27-34（ManualCompactionErrorCode）
type ManualErrorCode string

const (
	// ManualErrorBusy 是已经有一次压缩或者一个回合占着。
	//
	// 它是唯一一个自动压缩路径上也会报出来的：那道持久锁两条路共用。
	ManualErrorBusy ManualErrorCode = "busy"
	// ManualErrorCancelled 是这次请求在做完之前被取消了。
	ManualErrorCancelled ManualErrorCode = "cancelled"
	// ManualErrorChanged 是会话在做的过程中变了，这次的结果已经不对得上了。
	ManualErrorChanged ManualErrorCode = "changed"
	// ManualErrorSummary 是总结那一步本身失败了。
	ManualErrorSummary ManualErrorCode = "summary"
	// ManualErrorCommit 是把替换落到表面上那一步失败了。
	ManualErrorCommit ManualErrorCode = "commit"
	// ManualErrorPersistence 是持久化那一步失败了。
	ManualErrorPersistence ManualErrorCode = "persistence"
)

// ManualError 是一次人工压缩里可以预期的、能直接当作人工命令结果回给用户的失败。
//
// 源: packages/compaction/compaction/src/index.ts:41-57
//
// 新增: DSH 是一个 `extends Error` 的类，靠 `instanceof` 认。Go 这边是一个
// 普通的错误类型，调用方用 errors.As 取出 [ManualError.Code] 再分派。
// 分类留在 Code 上而不是拆成六个哨兵值，是因为这六个取值会**原样进人工命令的
// 结果**——它们是给上层照着写提示语用的一张封闭的单子，而不是六种给
// errors.Is 分派的独立失败。
type ManualError struct {
	// Code 是这次失败的分类。
	Code ManualErrorCode
	// Message 是后端给出的那句诊断，原样保留。
	Message string
	// Cause 是原本那条失败；没有就是 nil。
	Cause error
}

// Error 实现 error。
func (e *ManualError) Error() string {
	return fmt.Sprintf("compaction: 人工压缩失败（%s）：%s", e.Code, e.Message)
}

// Unwrap 交出原本那条失败，让 errors.Is 和 errors.As 能一路查下去。
func (e *ManualError) Unwrap() error { return e.Cause }

// NewManualError 造一条分了类的人工压缩失败。
func NewManualError(code ManualErrorCode, message string, cause error) *ManualError {
	return &ManualError{Code: code, Message: message, Cause: cause}
}

// AgentContext 是压缩要的那一小片 agent：一个改得动的活会话，加上这段对话
// 此刻的路由。
//
// 源: packages/compaction/compaction/src/index.ts:59-63
//
// 新增: DSH 是 `{ session; options: { provider?; model? } }`，其中 options 是
// agent 那份完整选项的一个可选子集。Go 这边把那两个字段摊平：agent.Options
// 还带着一个 MaxTokens，而压缩用不上它；为两个字符串从这里去 import
// harness/agent，等于把循环那一整块（docs/DESIGN.md 第八节第 6 块）拖进来。
//
// Session 没有再收窄的余地——[SurfaceView] 那种「只声明要的那一小片」在这里
// 做不到：压缩**要改它**，追加四条事件、换掉表面，那需要活对象本身。
// 装配方现拼一个值就行：
//
//	compaction.AgentContext{
//		Session:  live.Session(),
//		Provider: live.Options().Provider,
//		Model:    live.Options().Model,
//	}
type AgentContext struct {
	// Session 是被压的那段会话。压缩往它的日志里追加事件、并换掉它的表面。
	Session *coresession.Session
	// Provider 是这段对话此刻的提供方路由。
	//
	// 后端拿它当摘要路由的回落：没有单独配摘要模型时，用对话自己这一个。
	Provider string
	// Model 是这段对话此刻的模型，用途同上。
	Model string
}

// Maintainer 是「从真正的空闲期跑一件不是回合的活儿」这个能力。
//
// 源: packages/compaction/compaction/src/index.ts:71-78
//
// 新增: DSH 把 `runMaintenance` 直接挂在 `ManualCompactAgentContext` 上，靠接口
// 继承拿到 `CompactionAgentContext` 的两个字段。Go 这边拆成一个单方法接口，
// 由 [ManualAgentContext] 持有——签名和 harness/agent 里 Agent.RunMaintenance
// **逐字相同**，于是一个真的 agent 结构上就满足它，装配方直接把它填进去，
// 不用现包一层适配。
//
// DSH 那个 `runMaintenance<T>` 的泛型这里没有：Go 的接口方法带不了类型参数，
// 要产出的调用方自己在闭包里接住。这不是能力上的损失，取舍和 harness/agent
// 那一处逐字相同。
type Maintainer interface {
	// RunMaintenance 认领一段真正的空闲期把 task 跑完。
	//
	// 之后来的唤醒输入留在收件箱里按 FIFO 等它落地。已经有回合在驱动、
	// 或者已经有另一件维护活儿占着时当场报错。
	RunMaintenance(ctx context.Context, task func(context.Context) error) error
}

// ManualAgentContext 是一次显式的人工压缩要的那一小片 agent。
//
// 源: packages/compaction/compaction/src/index.ts:65-79
//
// 比 [AgentContext] 多的就是 [Maintainer]，而它是必须的：那道持久的
// compaction/start 标记只挡得住**别的压缩事务**，挡不住一个正在跑的回合。
// 人工压缩要和驱动串起来，只能靠 agent 自己那道空闲闸。
//
// 新增: DSH 是接口继承，Go 这边是嵌入 [AgentContext] 再加一个接口字段。
type ManualAgentContext struct {
	AgentContext
	// Maintainer 是那个 agent 本身，必填。
	Maintainer Maintainer
}

// Engine 是压缩这道接缝：什么时候压、留哪一段、摘要怎么做，全部归实现方，
// 它还可以自己去用一个单独的计量服务。
//
// 源: packages/compaction/compaction/src/index.ts:81-171
//
// 一次成功的运行把选中的那段表面换成一个摘要节点，并且同一段会话上不许有
// 第二次压缩同时在跑。那条替换用的 user/message 必须盖 [NewCheckpointSource]
// 的章、带上这次事务的 [ID]——消费方靠它认出这条消息是压缩换上去的，
// 也才和后端是谁无关。
//
// 新增: DSH 是 `abstract class CompactionEngine extends Service`，一个类兼两职：
// 它是抽象基类，构造函数又顺手把自己挂到 `ctx.compaction` 上。Go 没有抽象类，
// 也没有那个容器，所以只剩一个接口——一份装配里挂哪个实现由装配方自己拿着。
// `declare module` 那段（把 `compaction` 并进 cordis 的 Context）在 Go 里没有
// 对应物，裁决行留在 docs/portmap/portmap.tsv 里。
//
// 新增: DSH 每个方法末尾收一个 AbortSignal，其中 `compactRegion` 那个还是可选的。
// Go 这边一律是头一个参数 [context.Context]，没有「可以不传」这回事：
// 一次过模型的压缩必须把取消传下去，而一个可选的取消口正是漏传的来源。
type Engine interface {
	// CompactIfNeeded 就一个明确的触发原因考虑一次自动压缩。
	//
	// 源: packages/compaction/compaction/src/index.ts:98-114
	//
	// 压力那条策略看的是最近一次落库的、带路由的请求；超窗那条允许在还没到
	// 平常那条线的时候就强行做一次有用的、配平的缩减。挑不出一段安全的区间时
	// 第二个返回值是 false——一个大得离谱的保留单元、或者请求信封本身太大，
	// 靠表面压缩是修不好的。
	//
	// 新增: DSH 的返回是 `Promise<CompactionResult | null>`。Go 这边拆成一个值
	// 加一个布尔，理由和本仓库别处相同：[Result] 的零值是一份合法的结构体，
	// 拿它当「没压」会和一次三个 seq 恰好都是 0 的真压缩撞车。
	CompactIfNeeded(ctx context.Context, agent AgentContext, trigger Trigger) (Result, bool, error)

	// CompactNow 在还没到自动压力线的时候显式压掉有用的历史。
	//
	// 源: packages/compaction/compaction/src/index.ts:116-141
	//
	// 实现方在任何异步动作**之前**同步地占住那段空闲期（[Maintainer]），
	// 先挑区间、挑不出就一个字都不写，然后在总结之前追加一条独立的
	// compaction/start。那条持久的标记就是这次压缩的锁，一直持有到一次
	// compaction/end 尝试为止。之后来的唤醒提示按 FIFO 照收，只是要等可选的
	// 那次持久化检查点和这段空闲活儿都落地之后才开跑。总结跑着的时候别的层
	// 注入进来的上下文可能夹在这一对标记中间——只有**选中的那一段**必须不变。
	//
	// sourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工命令发起的。
	//
	// 新增: DSH 是 `sourceCommandId?: CommandId`。这里用空串当「没有」，
	// 和 [StartData.SourceCommandID] 那一处是同一个决定，理由也一样：
	// 一条空的命令身份和没有命令身份是同一件事。类型本身归 dsh-commands，
	// 见本包的包文档第 3 条。
	//
	// 预期内的失败交出 [ManualError]：占着、被取消、区间变了、总结或者缩减
	// 没成、提交那一步失败、持久化那一步失败。一次被中止的请求原样保留它的
	// 中止原因。做砸了的尝试**留在日志里看得见**，不回滚。
	CompactNow(ctx context.Context, agent ManualAgentContext, sourceCommandID string) (Result, bool, error)

	// CompactRegion 强行把一段表面节点压成一个摘要节点。
	//
	// 源: packages/compaction/compaction/src/index.ts:143-170
	//
	// start 和 end 是一段**按表面位置**算的闭区间，不是 seq 的数值区间：
	// 一次替换会让可见的 seq 不再单调。两头都必须配平，助手那边的工具调用
	// 才不会和它的结果被劈开——用 [BalanceIndex.BalancedBefore] 和
	// [BalanceIndex.BalancedAfter] 验这两刀。被压的是 agent.Session。
	// 换上去那条 user/message 必须盖 [NewCheckpointSource] 的章、
	// 带上这次事务的 [ID]。
	//
	// 已经有一次压缩开着，或者这段区间不存在、头尾颠倒、任何一头不配平时报错。
	//
	// 这一个没有那个布尔：DSH 那边它的返回就不带 null——调用方是指名道姓地
	// 要压这一段，压不了是错，不是「不必压」。
	CompactRegion(ctx context.Context, start int, end int, agent AgentContext) (Result, error)
}
