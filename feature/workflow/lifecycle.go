// 本文件的作用：六条生命周期边——观察者的类型、按作用域分层的登记，以及那个
// 兜住 panic 的发射器。
//
// 源: packages/workflow/workflow/src/index.ts:31-91（事件声明）、175-201（emitWorkflowEvent）

package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
)

// 六种观察者。每一个都**只观察**：负载里没有运行句柄，返回值和 panic 都改变不了
// 那次运行。
//
// 源: packages/workflow/workflow/src/index.ts:36-90
//
// 新增: 每个观察者末尾都多一个 parent。DSH 那边这六条边发在引擎自己那个 cordis
// 上下文上，「谁听得见」由那个上下文的子树决定，事件负载里因此不需要发起方。Go 没有
// 那个环境上下文，一个观察者要想只看见自己那一支派出去的运行，唯一的路子就是让边
// 带上这次运行的父 agent 并按它的作用域派发。这和
// [github.com/snight1983/ds-harness-go/feature/subagent] 那条接缝遇到的是同一个问题，
// 解法也逐字相同。父可以是 nil：作用域已经散了的时候只发全局层。
type (
	// StartObserver 是 `workflow/start` 的观察者：meta 验过了，脚本正文马上要跑。
	StartObserver func(info RunInfo, parent agent.Agent)
	// PhaseObserver 是 `workflow/phase` 的观察者，title 是脚本给的那个标题，原样。
	PhaseObserver func(info RunInfo, title string, parent agent.Agent)
	// LogObserver 是 `workflow/log` 的观察者，message 是脚本说的那句话，原样。
	LogObserver func(info RunInfo, message string, parent agent.Agent)
	// AgentStartObserver 是 `workflow/agent-start` 的观察者：一次 agent() 调用立起了
	// 一个已发布的孩子。
	AgentStartObserver func(info RunInfo, child AgentInfo, parent agent.Agent)
	// AgentEndObserver 是 `workflow/agent-end` 的观察者：那次 agent() 调用结清了。
	AgentEndObserver func(info RunInfo, child AgentEndInfo, parent agent.Agent)
	// EndObserver 是 `workflow/end` 的观察者：这次运行结清了，任何停止原因都算。
	EndObserver func(info RunInfo, result ResultInfo, parent agent.Agent)
)

// lifecycleLayer 是一个作用域在这六张观察者表里的全部贡献。
type lifecycleLayer struct {
	start      *scope.AnonymousEntries[StartObserver]
	phase      *scope.AnonymousEntries[PhaseObserver]
	log        *scope.AnonymousEntries[LogObserver]
	agentStart *scope.AnonymousEntries[AgentStartObserver]
	agentEnd   *scope.AnonymousEntries[AgentEndObserver]
	end        *scope.AnonymousEntries[EndObserver]
}

// newLifecycleLayer 造一层。
func newLifecycleLayer() *lifecycleLayer {
	return &lifecycleLayer{
		start:      scope.NewAnonymousEntries[StartObserver](),
		phase:      scope.NewAnonymousEntries[PhaseObserver](),
		log:        scope.NewAnonymousEntries[LogObserver](),
		agentStart: scope.NewAnonymousEntries[AgentStartObserver](),
		agentEnd:   scope.NewAnonymousEntries[AgentEndObserver](),
		end:        scope.NewAnonymousEntries[EndObserver](),
	}
}

// IsEmpty 表示这一层六张表全空了，[scope.Layers] 靠它回收空层。
func (l *lifecycleLayer) IsEmpty() bool {
	return l.start.IsEmpty() && l.phase.IsEmpty() && l.log.IsEmpty() &&
		l.agentStart.IsEmpty() && l.agentEnd.IsEmpty() && l.end.IsEmpty()
}

// Lifecycle 是这条接缝那六条边的登记处兼发射器。
//
// 源: packages/workflow/workflow/src/index.ts:175-186（emitWorkflowEvent）
//
// 引擎实现方攥一个，在该发边的地方调那几个 Emit 方法；宿主把同一个交给观察方去登记。
//
// 新增: DSH 那边发边是抽象类 WorkflowEngine 上的一个 protected 方法，子类继承了就
// 有。Go 没有继承，所以它单立成一个值。分开还有一个好处：宿主可以只把这个值交给
// 观察方，而不必把引擎本身交出去——一个能装观察者的插件不该顺手就能开工作流。
type Lifecycle struct {
	layers *scope.Layers[*lifecycleLayer]
	// warn 记一个观察者的 panic；nil 表示丢掉。
	warn func(message string)
	// check 是本包那份不变量检查装上之后留下的报告口，没装时为 nil。
	// 由 [RegisterInvariants] 写，见 invariant.go。
	//
	// 新增: DSH 那份检查挂在 cordis 的 internal/dispatch 钩子上，它跑在这几个监听器
	// **之外**，所以它抛出来的东西不会被这里的兜底吃掉。Go 这边给它一个专门的位置而
	// 不是让它去当一个普通观察者，正是为了保住这一点——一条被 [Lifecycle.contain]
	// 吞成一行日志的不变量检查等于没有检查。搬法同
	// [github.com/snight1983/ds-harness-go/feature/subagent] 那条接缝。
	check atomic.Pointer[lifecycleInvariant]
}

// NewLifecycle 造一个发射器，观察者的 panic 记到 logger 上；logger 为 nil 时用
// [log/slog.Default]。
func NewLifecycle(logger *slog.Logger) (*Lifecycle, error) {
	// onChange 传 nil：没有任何东西为「这条接缝的观察者名单变了」重算缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*lifecycleLayer, error) { return newLifecycleLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		return nil, err
	}
	return &Lifecycle{
		layers: layers,
		warn: func(message string) {
			if logger == nil {
				slog.Default().Warn(message)
				return
			}
			logger.Warn(message)
		},
	}, nil
}

// ---- 登记 ----

// 六个登记方法的规矩完全一样：owner 决定这次登记落在哪一层——没有身份的作用域落
// 全局层，看得见每一次派发；有身份的作用域落它自己那一层，只看得见由它（或它的
// 子孙）名下的父 agent 发起的那些运行。返回的函数撤销这次登记，owner 释放时也会
// 自动撤销。

// OnStart 登记一个 `workflow/start` 观察者。
func (l *Lifecycle) OnStart(ctx context.Context, owner *scope.Scope, observer StartObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnStart 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.start.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnStart()"})
}

// OnPhase 登记一个 `workflow/phase` 观察者。
func (l *Lifecycle) OnPhase(ctx context.Context, owner *scope.Scope, observer PhaseObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnPhase 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.phase.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnPhase()"})
}

// OnLog 登记一个 `workflow/log` 观察者。
func (l *Lifecycle) OnLog(ctx context.Context, owner *scope.Scope, observer LogObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnLog 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.log.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnLog()"})
}

// OnAgentStart 登记一个 `workflow/agent-start` 观察者。
func (l *Lifecycle) OnAgentStart(ctx context.Context, owner *scope.Scope, observer AgentStartObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnAgentStart 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.agentStart.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnAgentStart()"})
}

// OnAgentEnd 登记一个 `workflow/agent-end` 观察者。
func (l *Lifecycle) OnAgentEnd(ctx context.Context, owner *scope.Scope, observer AgentEndObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnAgentEnd 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.agentEnd.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnAgentEnd()"})
}

// OnEnd 登记一个 `workflow/end` 观察者。
func (l *Lifecycle) OnEnd(ctx context.Context, owner *scope.Scope, observer EndObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, fmt.Errorf("workflow: OnEnd 需要一个观察者")
	}
	return l.layers.Effect(ctx, owner, func(layer *lifecycleLayer) (func(), error) {
		return layer.end.Append(observer), nil
	}, scope.EffectOptions{Label: "workflow.OnEnd()"})
}

// ---- 派发 ----

// carrierKey 交出一个父 agent 对应的那把作用域键；父为 nil、或者它的作用域已经散了
// 的时候交回 nil（只发全局层）。
func carrierKey(parent agent.Agent) *scope.Key {
	if parent == nil {
		return nil
	}
	agentScope := parent.Scope()
	if agentScope == nil {
		return nil
	}
	return agentScope.Key()
}

// collectLifecycle 按载体作用域的父链把观察者收齐，全局层在前。
func collectLifecycle[T any](
	lifecycle *Lifecycle,
	key *scope.Key,
	pick func(*lifecycleLayer) *scope.AnonymousEntries[T],
) []T {
	var observers []T
	for observer := range pick(lifecycle.layers.Global()).Values() {
		observers = append(observers, observer)
	}
	if key == nil {
		return observers
	}
	for _, layer := range lifecycle.layers.ChainLayers(key) {
		for observer := range pick(layer).Values() {
			observers = append(observers, observer)
		}
	}
	return observers
}

// contain 跑一个观察者，把它的 panic 兜住记下来。
//
// 源: packages/workflow/workflow/src/index.ts:177-185
//
// 每一个观察者各自被兜住：一次 panic 既不会饿着排在它后面的同侪，也改变不了那次
// 运行。DSH 还要额外接住「返回了一个 rejected 的 promise」——它那边的监听器可以是
// 异步的。Go 的观察者是同步函数，没有第二种失败通道，所以只剩 recover 这一处。
func (l *Lifecycle) contain(name string, run func()) {
	defer func() {
		if recovered := recover(); recovered != nil && l.warn != nil {
			l.warn(fmt.Sprintf("workflow: %s 观察者 panic 了：%v", name, recovered))
		}
	}()
	run()
}

// EmitStart 发 `workflow/start`：meta 验过了，脚本正文马上要跑。和 [Lifecycle.EmitEnd]
// 配对。
func (l *Lifecycle) EmitStart(info RunInfo, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.runStarted(info)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[StartObserver] {
		return layer.start
	}) {
		l.contain("workflow/start", func() { observer(info, parent) })
	}
}

// EmitPhase 发 `workflow/phase`：脚本进了一个阶段。只是进度分组，没有执行语义。
func (l *Lifecycle) EmitPhase(info RunInfo, title string, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.observed(info)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[PhaseObserver] {
		return layer.phase
	}) {
		l.contain("workflow/phase", func() { observer(info, title, parent) })
	}
}

// EmitLog 发 `workflow/log`：脚本说了一句话。
func (l *Lifecycle) EmitLog(info RunInfo, message string, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.observed(info)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[LogObserver] {
		return layer.log
	}) {
		l.contain("workflow/log", func() { observer(info, message, parent) })
	}
}

// EmitAgentStart 发 `workflow/agent-start`：一次 agent() 调用立起了一个已发布的孩子。
//
// 源: packages/workflow/workflow/src/index.ts:60-68
//
// 和 [Lifecycle.EmitAgentEnd] 按 Seq 配对。一次**没能**从提供方拿到已发布运行的
// agent() 调用，这一对边一条都不发。
func (l *Lifecycle) EmitAgentStart(info RunInfo, child AgentInfo, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.agentStarted(info, child)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[AgentStartObserver] {
		return layer.agentStart
	}) {
		l.contain("workflow/agent-start", func() { observer(info, child, parent) })
	}
}

// EmitAgentEnd 发 `workflow/agent-end`：那次 agent() 调用结清了。
//
// 源: packages/workflow/workflow/src/index.ts:69-79
//
// 每一个发过 start 的调用，在**任何**停止路径上都恰好发一次：走到引擎终止那条路上
// （worker 过了宽限期被杀掉）由引擎补一条 [AgentCancelled] 出来。
func (l *Lifecycle) EmitAgentEnd(info RunInfo, child AgentEndInfo, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.agentEnded(info, child)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[AgentEndObserver] {
		return layer.agentEnd
	}) {
		l.contain("workflow/agent-end", func() { observer(info, child, parent) })
	}
}

// EmitEnd 发 `workflow/end`：这次运行结清了，任何停止原因都算。
//
// 源: packages/workflow/workflow/src/index.ts:80-89
//
// 和 [Lifecycle.EmitStart] 配对，在结果落定的那一刻**恰好发一次**。负载里
// 故意没有那个结果值，理由见 [ResultInfo]。
func (l *Lifecycle) EmitEnd(info RunInfo, result ResultInfo, parent agent.Agent) {
	if check := l.check.Load(); check != nil {
		check.runEnded(info, result)
	}
	for _, observer := range collectLifecycle(l, carrierKey(parent), func(layer *lifecycleLayer) *scope.AnonymousEntries[EndObserver] {
		return layer.end
	}) {
		l.contain("workflow/end", func() { observer(info, result, parent) })
	}
}
