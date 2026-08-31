// 本文件的作用：两种子 agent 形态共用的那对生命周期边——兜住异常的发射器、
// 一次性运行的观察者，以及可续活化的观察者。
//
// 源: packages/subagent/subagent/src/lifecycle.ts
//
// 对外的负载契约（[RunInfo]、[RunEndInfo]）和这条接缝其余那些面向调用方的类型
// 一起放在 types.go；本文件只拥有实现，外加续接管理器消费的那个包内私有的
// [activationObserver]。把内部的控制接口挡在发布出去的那张面之外是有意的：
// 那个观察者 start／capture／settle 的次序是本文件和一个包内调用方之间的约定，
// 不是一个插件可以依赖的东西。

package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/google/uuid"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// StartObserver 是 `subagent/start` 那条边的观察者。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:80
//
// 只观察：返回的错误被逐个记下来兜住，改变不了那次运行。
//
// 新增: parent 是派活的那个父 agent，[RunInfo] 里没有它。DSH 那边它是监听器的
// `this`——派发载体本身就绑在回调上（sdk/server/src/server.ts:87-88 的
// `function (this: Scoped<SubagentRuntime>)` 加 carrierKeyOf 就是在读它）。Go 的
// 函数值没有这个绑定，所以只能当一个参数交出来；不交的话，一个装在全局层上的
// 观察者能看见每一次运行，却说不出是谁派的。
//
// 它可以是 nil：没有父、或者父的作用域已经散了。
type StartObserver func(info RunInfo, parent agent.Agent)

// EndObserver 是 `subagent/end` 那条边的观察者。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:81
//
// parent 的约定同 [StartObserver]。
type EndObserver func(info RunEndInfo, parent agent.Agent)

// ProviderAddedObserver 是「一个提供方在注册表里认得出来了」的观察者。
//
// 源: packages/subagent/subagent/src/index.ts:145-150
//
// 只有它交回错误：这条边发在一次登记的中途，一个报错的观察者要把那次登记整个
// 卷回去（DSH 那句注释写明「A throwing added-listener unwinds the yielded rollback」）。
// 其余三条边都发在既成事实之后，观察者说什么都改变不了那件事，所以都不带错误通道。
type ProviderAddedObserver func(provider Provider) error

// ProviderRemovedObserver 是「一个提供方被摘掉了」的观察者，带那个提供方的名字。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:82
//
// 它从一个处置器里发出来，所以观察者里的 panic 尤其不能把拆解打断。
type ProviderRemovedObserver func(provider string)

// lifecycleLayer 是一个作用域在这四张观察者表里的全部贡献。
//
// 新增: DSH 靠 cordis 的作用域派发过滤监听器，本仓库统一换成
// [ds-harness-go/core/scope.Layers]——全局层加各作用域的覆盖层，派发时按载体作用域的
// 父链取并集（成例见 [ds-harness-go/core/agent] 那十二个事件）。
type lifecycleLayer struct {
	start           *scope.AnonymousEntries[StartObserver]
	end             *scope.AnonymousEntries[EndObserver]
	providerAdded   *scope.AnonymousEntries[ProviderAddedObserver]
	providerRemoved *scope.AnonymousEntries[ProviderRemovedObserver]
}

// newLifecycleLayer 造一层。
func newLifecycleLayer() *lifecycleLayer {
	return &lifecycleLayer{
		start:           scope.NewAnonymousEntries[StartObserver](),
		end:             scope.NewAnonymousEntries[EndObserver](),
		providerAdded:   scope.NewAnonymousEntries[ProviderAddedObserver](),
		providerRemoved: scope.NewAnonymousEntries[ProviderRemovedObserver](),
	}
}

// IsEmpty 表示这一层四张表全空了，[scope.Layers] 靠它回收空层。
func (l *lifecycleLayer) IsEmpty() bool {
	return l.start.IsEmpty() && l.end.IsEmpty() &&
		l.providerAdded.IsEmpty() && l.providerRemoved.IsEmpty()
}

// lifecycleEmitter 是这条接缝每一条边都经由它发出去的那个兜住异常的发射器。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:78-124
//
// 每一个观察者各自被兜住：一次 panic 被记下来，既不会饿着同侪观察者、不会改变
// 那次运行，也不会——对于从处置器里发出的「提供方被摘掉」——把拆解打断。
//
// 运行那两条边带着发起派发的那个父，作用域派发按它认层；提供方那两条边没有父
// 这个载体，只发给全局层。
// 「提供方来了」是唯一一条**不**被兜住的边，理由见 [ProviderAddedObserver]。
type lifecycleEmitter struct {
	layers *scope.Layers[*lifecycleLayer]
	// warn 记下一个观察者的 panic；nil 表示丢掉。
	warn func(message string)
	// check 是本包那份不变量检查装上之后留下的报告口，没装时为 nil。
	// 由 [RegisterInvariants] 写，见 invariant.go。
	//
	// 新增: DSH 那份检查挂在 cordis 的 `internal/dispatch` 钩子上，它跑在这几个
	// 监听器**之外**，所以它抛出来的东西不会被这里的兜底吃掉。Go 这边给它一个
	// 专门的位置而不是让它去当一个普通观察者，正是为了保住这一点——一条被
	// [lifecycleEmitter.contain] 吞成一行日志的不变量检查等于没有检查。
	// 这和 [ds-harness-go/llm.Runtime] 攥着一个 fail 字段是同一个搬法。
	check atomic.Pointer[lifecycleInvariant]
}

// newLifecycleEmitter 造一个发射器，观察者的 panic 记到 logger 上。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:126-131
//
// 新增: DSH 那个工厂收的是 cordis 上下文和一个「把某个父折成派发载体」的函数，
// 两样都是它那套事件系统的东西。本仓库的作用域分层自带载体解算（见 carrierKey），
// 所以这里只剩下「往哪儿记诊断」这一件事；logger 为 nil 时用 [log/slog.Default]。
func newLifecycleEmitter(logger *slog.Logger) (*lifecycleEmitter, error) {
	// onChange 传 nil：没有任何东西为「这条接缝的观察者名单变了」重算缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*lifecycleLayer, error) { return newLifecycleLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		return nil, err
	}
	return &lifecycleEmitter{
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

// carrierKey 交出一个父 agent 对应的那把作用域键；父为 nil 时交回 nil（只发全局层）。
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
	emitter *lifecycleEmitter,
	key *scope.Key,
	pick func(*lifecycleLayer) *scope.AnonymousEntries[T],
) []T {
	var observers []T
	for observer := range pick(emitter.layers.Global()).Values() {
		observers = append(observers, observer)
	}
	if key == nil {
		return observers
	}
	for _, layer := range emitter.layers.ChainLayers(key) {
		for observer := range pick(layer).Values() {
			observers = append(observers, observer)
		}
	}
	return observers
}

// contain 跑一个观察者，把它的 panic 兜住记下来。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:110-120
//
// 新增: DSH 还要额外接住「返回了一个 rejected 的 promise」——它那边的监听器可以是
// 异步的。Go 的观察者是同步函数，没有第二种失败通道，所以只剩 recover 这一处。
func (e *lifecycleEmitter) contain(name string, run func()) {
	defer func() {
		if recovered := recover(); recovered != nil && e.warn != nil {
			e.warn(fmt.Sprintf("subagent: %s 观察者 panic 了：%v", name, recovered))
		}
	}()
	run()
}

// emitStart 发 `subagent/start`。
func (e *lifecycleEmitter) emitStart(info RunInfo, parent agent.Agent) {
	if check := e.check.Load(); check != nil {
		check.runStarted(info)
	}
	observers := collectLifecycle(e, carrierKey(parent), func(l *lifecycleLayer) *scope.AnonymousEntries[StartObserver] {
		return l.start
	})
	for _, observer := range observers {
		e.contain("subagent/start", func() { observer(info, parent) })
	}
}

// emitEnd 发 `subagent/end`。
func (e *lifecycleEmitter) emitEnd(info RunEndInfo, parent agent.Agent) {
	if check := e.check.Load(); check != nil {
		check.runEnded(info)
	}
	observers := collectLifecycle(e, carrierKey(parent), func(l *lifecycleLayer) *scope.AnonymousEntries[EndObserver] {
		return l.end
	})
	for _, observer := range observers {
		e.contain("subagent/end", func() { observer(info, parent) })
	}
}

// emitProviderAdded 发 `subagent/provider-added`，一有观察者报错就当场停下并报出去。
//
// 源: packages/subagent/subagent/src/index.ts:404
//
// 不兜异常：这条边发在一次登记的中途，它的失败**就是**那次登记的失败。
// 一个观察者报错之后排在它后面的那些就不叫了——那次登记已经注定要卷回去，
// 再往下通告一个马上就要消失的提供方没有意义。
func (e *lifecycleEmitter) emitProviderAdded(provider Provider) error {
	if check := e.check.Load(); check != nil {
		check.providerAdded(provider)
	}
	observers := collectLifecycle(e, nil, func(l *lifecycleLayer) *scope.AnonymousEntries[ProviderAddedObserver] {
		return l.providerAdded
	})
	for _, observer := range observers {
		if err := observer(provider); err != nil {
			return err
		}
	}
	return nil
}

// emitProviderRemoved 发 `subagent/provider-removed`。
func (e *lifecycleEmitter) emitProviderRemoved(provider string) {
	if check := e.check.Load(); check != nil {
		check.providerRemoved(provider)
	}
	observers := collectLifecycle(e, nil, func(l *lifecycleLayer) *scope.AnonymousEntries[ProviderRemovedObserver] {
		return l.providerRemoved
	})
	for _, observer := range observers {
		e.contain("subagent/provider-removed", func() { observer(provider) })
	}
}

// observeRun 为一次被接受的**一次性**运行发出那对 start／end 边，交回同一个运行。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:133-160
//
// 新增: DSH 在派发 start **之前**就把终止观察者挂到 run.result 那个 promise 上，
// 并特意注释说 promise 的回调仍旧排在这次同步的 start 之后，于是 start → end 的
// 次序保住。Go 这边 [Run.Result] 是一个阻塞方法，所以那个次序换一种方式兑现：
// 先同步发 start，再起一条 goroutine 去等结果。这条 goroutine 跟着 ctx 走；
// ctx 取消时 Result 交回错误，那条 end 边照发（StopError），因为一条发出去的 start
// 必须有它配对的 end。
func observeRun(ctx context.Context, emitter *lifecycleEmitter, provider string, parent agent.Agent, run Run) Run {
	identity := RunInfo{
		RunID:    RunID(uuid.NewString()),
		Provider: provider,
		ID:       run.ID(),
		Local:    run.LocalAgent() != nil,
	}
	emitter.emitStart(identity, parent)
	go func() {
		end := RunEndInfo{
			RunID:    identity.RunID,
			Provider: identity.Provider,
			ID:       identity.ID,
			Local:    identity.Local,
		}
		result, err := run.Result(ctx)
		if err != nil {
			end.StopReason = StopError
		} else {
			end.StopReason = result.StopReason
			// 一份都没有时把这个字段留空，和可续那种轮次保持一致。
			end.LastAssistantMessage = result.Output
		}
		emitter.emitEnd(end, parent)
	}()
	return run
}

// activationTerminal 是一次活化的驻留轮次怎么结束的，终止那条生命周期边和管理器
// 自己那次交回父的汇报读的是同一份。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:30-35
type activationTerminal struct {
	// StopReason 是这一轮最后那个普通回合为什么结束；拆解失败时是 StopError。
	StopReason StopReason
	// Output 是这一轮最后那段助手内容；一份都没产出、或者失败时是 nil。
	Output llm.Content
}

// activationObserver 是一次可续活化的驻留轮次的生命周期观察者，好让可续的孩子发出
// 和一次性运行一模一样的那对 start／end。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:42-73
//
// 包内私有：续接管理器是唯一的消费方，它调用的次序是包内的约定，不是一个发布出去的
// 扩展点。
type activationObserver struct {
	emitter  *lifecycleEmitter
	identity RunInfo
	parent   agent.Agent
	// boundary 是这一轮开始那一刻孩子日志的长度。一次冷恢复会回放更早的回合，
	// 所以这一轮的遥测必须只来自它真的产出的那段后缀——读整个会话会在这一轮
	// 一个回合都没开的时候，报出上一轮的答案。
	boundary int
	// captured 由 capture 填上，处置那条路一定在 settle 之前跑它，所以一个驻留过的
	// 轮次到那时一定已经有了自己的事实。
	captured activationTerminal
}

// newActivationObserver 造一次可续活化驻留轮次的观察者。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:172-177
//
// 观察方看到的是和一次性运行同一套词汇，于是一个孩子的开始与结清照样观察得到，
// 而管理器究竟是物化了它、唤醒了它、还是冷恢复了它，不外露。驻留之前就失败的创建
// **一条边都不发**——凭空造一条会报出一个这个孩子从来没有过的生命周期。
func newActivationObserver(emitter *lifecycleEmitter, provider string, childID session.SessionID, parent agent.Agent) *activationObserver {
	return &activationObserver{
		emitter: emitter,
		identity: RunInfo{
			RunID:    RunID(uuid.NewString()),
			Provider: provider,
			ID:       childID,
			Local:    true,
		},
		parent:   parent,
		captured: activationTerminal{StopReason: StopCompleted},
	}
}

// start 在这一轮驻留下来之后发出开始那条边。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:190-193
func (o *activationObserver) start(child agent.Agent) {
	o.boundary = len(child.Session().Events())
	o.emitter.emitStart(o.identity, o.parent)
}

// capture 趁孩子还登记着，把那些依赖孩子的终止事实拍下来——句柄一处置就把它摘掉了，
// 而消费方要靠它去读孩子自己的日志和作用域。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:194-202
func (o *activationObserver) capture(child agent.Agent) {
	own := child.Session().Events()[o.boundary:]
	o.captured = activationTerminal{
		StopReason: epochStopReason(own),
		Output:     FinalAssistantOutput(own),
	}
}

// terminal 解算出 [activationObserver.settle] 将要发布的那些终止事实，但不发布。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:203-208
//
// 管理器那次交回父的投递必须跑在「放掉所有权、让父得以结清」之前，而那比终止边更早；
// 两处于是读同一次计算，而不是把那条失败规矩再叙述一遍。
//
// 拆解失败盖过这一轮自己的结局、并且扣下它的输出：一份这套装置没能耐久地放出去的
// 答案，不算结果。failure 为 nil 表示成功。
func (o *activationObserver) terminal(failure error) activationTerminal {
	if failure == nil {
		return o.captured
	}
	return activationTerminal{StopReason: StopError}
}

// settle 在处置的结局明朗之后，**恰好一次**地发出终止那条边，和这一轮那次
// [activationObserver.start] 配对。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:209-217
//
// 只对一个驻留过的轮次调：驻留之前的失败一条边都不发。
func (o *activationObserver) settle(failure error) {
	terminal := o.terminal(failure)
	o.emitter.emitEnd(RunEndInfo{
		RunID:                o.identity.RunID,
		Provider:             o.identity.Provider,
		ID:                   o.identity.ID,
		Local:                o.identity.Local,
		StopReason:           terminal.StopReason,
		LastAssistantMessage: terminal.Output,
	}, o.parent)
}

// epochStopReason 说的是这个孩子这一轮为什么结束，供终止那条生命周期边和管理器
// 自己那次交回父的投递用。
//
// 源: packages/subagent/subagent/src/lifecycle.ts:236-263
//
// **孩子自己的日志说了算**：拆解成功了，一个字都没说模型有没有出错、有没有撞上
// token 天花板、有没有被取消，所以从处置结果反推原因会把失败的活儿报成完成。
//
// [ds-harness-go/core/agent.FoldConsumedWork] 补上光看回合序列拿不到的那两半：
// 哪个回合交代得了这一轮消耗掉的活儿，以及被接受的活儿有没有在那之后被取消、
// 且中间没有任何一个回合开在它上面。一次**记下来的失败**仍旧压过一次取消——
// 停掉一个本来就已经失败的孩子，不会把它的失败变成一次取消。
func epochStopReason(events []session.Event) StopReason {
	work := agent.FoldConsumedWork(events)
	if !work.HasEnd {
		// 干净的收尾和「压根没有交代回合」共用一条规矩：这一轮做完了交给它的事，
		// 除非一个被取消的队列另有说法。
		if work.DroppedUnrun {
			return StopAborted
		}
		return StopCompleted
	}
	var data session.TurnEndData
	if err := json.Unmarshal(work.End.Data, &data); err != nil {
		return StopError
	}
	switch data.Reason.TurnEndReasonKind() {
	case session.ReasonMaxTokens:
		return StopMaxTokens
	case session.ReasonAborted, session.ReasonInterrupted:
		return StopAborted
	case session.ReasonError:
		return StopError
	case session.ReasonBlocked:
		// 一次前置步骤的拒绝——一个钩子说不、一个策略插件说不——把这一轮已经认领的
		// 输入丢掉了：那些活儿是被回绝了，不是做完了。
		return StopRefusal
	case session.ReasonCompleted:
		if work.DroppedUnrun {
			return StopAborted
		}
		return StopCompleted
	default:
		// TurnEndReason 是可被合并扩展的，所以这一支要一个加了新变体的后端才到得了。
		// 把一个说不出名字的结局当成成功，会把失败的活儿报成完成。
		return StopError
	}
}
