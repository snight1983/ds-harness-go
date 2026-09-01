// 本文件的作用：把自动压缩挂到一个跑着的运行时上——步骤边界上按压力压一次、
// 提供方确认超窗之后补救一次并要求重试，以及那个重试计数什么时候归零。
//
// 源: packages/compaction/compaction-basic/src/index.ts:137-224

package basic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// Agents 是本层要的那张 agent 注册表。
//
// 源: packages/compaction/compaction-basic/src/index.ts:147、167、179（ctx.on）
//
// 新增: DSH 从 cordis 容器里按事件名挂监听器。Go 没有那个容器，所以摆成一个窄
// 接口明着传进来——窄到只剩本层真正用得着的那几条，好让测试能替换它，也好让读
// 的人一眼看见本层到底碰了注册表的哪些面。成例是 context/instructions.Agents。
type Agents interface {
	// OnPreStep 是按压力压一次的地方：每个步骤边界上重算。
	OnPreStep(ctx context.Context, owner *scope.Scope, observer agent.PreStepObserver) (func(context.Context) error, error)

	// OnStatus 是超窗重试计数归零的地方之一：回到空闲就是一段新的开始。
	OnStatus(ctx context.Context, owner *scope.Scope, observer agent.StatusObserver) (func(context.Context) error, error)

	// OnRequestError 是超窗补救的地方。
	OnRequestError(ctx context.Context, owner *scope.Scope, observer agent.RequestErrorObserver) (func(context.Context) error, error)

	// OnDisposed 是把这个 agent 那份计数扔掉的地方。
	//
	// 新增: DSH 那两张表是 WeakMap，键是 Agent 和 Session 对象，靠 JS 的垃圾回收
	// 清理，所以那边**没有**这条观察者。Go 没有弱引用表，计数存在一张普通 map 里，
	// 就必须有人来删——这条边就是删它的地方。理由和 context/instructions 那一处
	// 逐字相同。
	OnDisposed(ctx context.Context, owner *scope.Scope, observer agent.DisposedObserver) (func(context.Context) error, error)
}

// Sessions 是本层要的那道会话日志广播。
//
// 源: packages/compaction/compaction-basic/src/index.ts:54-61（`session/event`）
type Sessions interface {
	// OnEvent 登记一个「一条事件提交进日志了」的观察者。
	OnEvent(ctx context.Context, owner *scope.Scope, observer coresession.EventObserver) (func(context.Context) error, error)
}

// InstallDeps 是装自动压缩要的那几样协作者。
//
// 源: packages/compaction/compaction-basic/src/index.ts:104（static inject）、138
//
// 名字叫 InstallDeps 不叫 Deps，是因为本包已经有一个 [EngineDeps]——两者收的
// 东西完全不同：那一份是「压一次要什么」，这一份是「挂到哪儿去」。
type InstallDeps struct {
	// Engine 是真的去压的那个后端，必填。
	Engine *Engine
	// Agents 是那张 agent 注册表，必填。
	Agents Agents
	// Sessions 是那道会话日志广播，必填。
	Sessions Sessions
	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
}

// installer 是这一次 [Install] 的全部可变状态。
//
// 源: packages/compaction/compaction-basic/src/index.ts:122-124
type installer struct {
	engine *Engine
	logger *slog.Logger

	mutex sync.Mutex
	// warned 是已经警告过的那些路由，键是 [Target.Key]。
	//
	// 压力在**每一个步骤边界**上都会重算一次，而一份配错了的压力参数每次都会
	// 以同样的方式失败。不去重的话，一条配置错误会按步骤数刷屏，把别的诊断顶掉。
	warned map[string]struct{}
	// overflowRetries 是每个 agent 这一串超窗补救已经做过几次。
	//
	// 新增: DSH 是两张 WeakMap——计数那张的键是 Agent，另一张
	// （overflowAgents）拿 Session 换回 Agent，因为清零那条观察者只拿得到会话。
	// Go 这边键统一成会话标识，第二张表整个消失：[agent.Agent.ID] 的注释写明
	// 它和 Session 共用同一个身份，所以「按 agent 存」和「按会话存」是同一件事。
	overflowRetries map[session.SessionID]int
}

// Install 把自动压缩装到运行时上，交回把它整个摘下来的函数。
//
// 源: packages/compaction/compaction-basic/src/index.ts:129、137-224
//
// 配置里 Auto 是关的时候一条观察者都不挂，交回一个空的摘除函数——和 DSH 那句
// `if (this.config.auto) this._registerAutomaticCompaction()` 是同一件事。
// 手工那条路（[Engine.CompactNow]、[Engine.CompactRegion]）不受它影响：
// Auto 管的是「要不要有人自动来叫」，不是「能不能压」。
//
// 新增: DSH 在 [Engine] 的构造函数里做这件事。Go 这边单独摆出来，理由写在
// [Engine] 上：那几条观察者要一张注册表和一段作用域生命期，而一份只手动压缩的
// 装配不该被迫先备一个注册表。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	deps InstallDeps,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, errors.New("compaction/basic: 需要一个持有这次登记的作用域")
	}
	switch {
	case deps.Engine == nil:
		return nil, errors.New("compaction/basic: 需要一个压缩后端")
	case deps.Agents == nil:
		return nil, errors.New("compaction/basic: 需要一张 agent 注册表")
	case deps.Sessions == nil:
		return nil, errors.New("compaction/basic: 需要一道会话日志广播")
	}
	if !deps.Engine.config.Auto {
		return func(context.Context) error { return nil }, nil
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	install := &installer{
		engine:          deps.Engine,
		logger:          logger,
		warned:          map[string]struct{}{},
		overflowRetries: map[session.SessionID]int{},
	}
	return install.observe(ctx, owner, deps)
}

// registration 是一条观察者登记：装它的那句，和它自己的名字。
type registration struct {
	label  string
	attach func() (func(context.Context) error, error)
}

// observe 按次序装齐那四条观察者，任何一条装不上就把已经装上的按反序撤掉。
//
// 源: packages/compaction/compaction-basic/src/index.ts:147-223
func (i *installer) observe(
	ctx context.Context,
	owner *scope.Scope,
	deps InstallDeps,
) (func(context.Context) error, error) {
	registrations := []registration{
		{"前置步骤", func() (func(context.Context) error, error) {
			return deps.Agents.OnPreStep(ctx, owner, i.onPreStep)
		}},
		{"状态", func() (func(context.Context) error, error) {
			return deps.Agents.OnStatus(ctx, owner, i.onStatus)
		}},
		{"会话事件", func() (func(context.Context) error, error) {
			return deps.Sessions.OnEvent(ctx, owner, i.onSessionEvent)
		}},
		{"请求失败", func() (func(context.Context) error, error) {
			return deps.Agents.OnRequestError(ctx, owner, i.onRequestError)
		}},
		{"处置", func() (func(context.Context) error, error) {
			return deps.Agents.OnDisposed(ctx, owner, i.onDisposed)
		}},
	}

	var undo []func(context.Context) error
	for _, entry := range registrations {
		stop, err := entry.attach()
		if err != nil {
			for _, back := range slices.Backward(undo) {
				_ = back(context.WithoutCancel(ctx))
			}
			return nil, fmt.Errorf("compaction/basic: 装%s观察者失败：%w", entry.label, err)
		}
		undo = append(undo, stop)
	}
	return func(undoCtx context.Context) error {
		failures := make([]error, 0, len(undo))
		for _, stop := range slices.Backward(undo) {
			failures = append(failures, stop(undoCtx))
		}
		i.mutex.Lock()
		clear(i.warned)
		clear(i.overflowRetries)
		i.mutex.Unlock()
		return errors.Join(failures...)
	}, nil
}

// onPreStep 在每个步骤边界上按压力压一次。
//
// 源: packages/compaction/compaction-basic/src/index.ts:147-165
//
// **压不动也照样让这个步骤走下去**：一次压缩失败是「历史长了、这次没缩下来」，
// 而把步骤拦掉是「这个回合当场死掉」。后者严重得多，而且历史长了这件事下一个
// 边界上还会再试一次。真的已经装不下时，超窗那条路仍然会接住它。
func (i *installer) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	if ctx.Err() == nil {
		i.compactForPressure(ctx, step.Agent)
	}
	return next(ctx)
}

// compactForPressure 跑一次压力压缩，并把结果或者失败记下来。
//
// 源: packages/compaction/compaction-basic/src/index.ts:151-162
func (i *installer) compactForPressure(ctx context.Context, live agent.Agent) {
	result, ok, err := i.engine.CompactIfNeeded(ctx, agentContextOf(live), compaction.TriggerPressure)
	if err == nil {
		if ok {
			i.logResult(result, "步骤压力")
		}
		return
	}
	// 同一条路由的压力参数配错了只吵一次，理由见 [installer.warned]。
	var pressure *TargetPressureError
	if errors.As(err, &pressure) && !i.markWarned(pressure.TargetKey) {
		return
	}
	i.logger.Warn("步骤压缩失败，这个回合照常走下去", "错误", err)
}

// onStatus 在这个 agent 回到空闲时把它那串超窗补救的计数清掉。
//
// 源: packages/compaction/compaction-basic/src/index.ts:167-169
func (i *installer) onStatus(live agent.Agent, status agent.Status) {
	if status != agent.StatusIdle {
		return
	}
	i.clearRetries(live.ID())
}

// onSessionEvent 在一条助手消息落地时把这段会话的超窗计数清掉。
//
// 源: packages/compaction/compaction-basic/src/index.ts:173-177
//
// 一次**成功的**回应就是一串新补救的开头，哪怕后面的工具调用把同一个回合接着
// 往下带进另一次请求。不清的话，一个工具用得多的长回合会把额度慢慢耗光，
// 之后真撞上超窗时已经没有补救次数可用了。
//
// 新增: DSH 先拿 session 去 overflowAgents 里换回那个 agent，换不到就不动。
// Go 这边键就是会话标识，删一个不存在的键本来就是空操作，那层查表整个消失。
func (i *installer) onSessionEvent(live *coresession.Session, event session.Event) {
	if event.Type != session.EventAssistantMessage {
		return
	}
	i.clearRetries(live.ID())
}

// onRequestError 在提供方确认超窗之后补救一次，并要求重试。
//
// 源: packages/compaction/compaction-basic/src/index.ts:179-223
//
// 只有**表面真的换过**才要求重试：没换过就原样重发，那是一个必然以同样方式
// 失败的死循环。判据是 [coresession.Session.SurfaceReplaceGeneration] 有没有往前走,
// 而不是「压缩这一趟返没返错」——一次不过模型的剪枝可能已经落地了，
// 后面那半总结才失败，那份缩减是耐久的，足以支撑一次重试。
func (i *installer) onRequestError(
	ctx context.Context,
	failure agent.RequestFailure,
	next func(context.Context) (agent.RequestErrorAction, error),
) (agent.RequestErrorAction, error) {
	if failure.Failure.Code != llm.ContextWindowExceededCode || ctx.Err() != nil {
		return next(ctx)
	}
	live := failure.Agent
	target, ok, err := routedTarget(live.Session())
	if err != nil {
		i.logger.Warn("超窗补救读不出这段会话的路由，保留原本那条请求失败", "错误", err)
		return next(ctx)
	}
	if !ok {
		return next(ctx)
	}
	retries := i.retriesOf(live.ID())
	if retries >= i.engine.config.ForTarget(target).MaxOverflowRetries {
		return next(ctx)
	}

	generation := live.Session().SurfaceReplaceGeneration()
	result, compacted, err := i.engine.CompactIfNeeded(
		ctx, agentContextOf(live), compaction.TriggerContextOverflow)
	if err != nil {
		if ctx.Err() == nil && live.Session().SurfaceReplaceGeneration() > generation {
			i.logger.Warn("超窗补救在一次已经落地的缩减之后失败，按替换后的表面重试", "错误", err)
			i.bumpRetries(live.ID(), retries)
			return agent.RequestErrorAction{Retry: true}, nil
		}
		if ctx.Err() != nil {
			i.logger.Warn("超窗补救失败，而且已经被取消，不重试", "错误", err)
		} else {
			i.logger.Warn("超窗补救失败，保留原本那条请求失败", "错误", err)
		}
		return next(ctx)
	}
	if ctx.Err() != nil || live.Session().SurfaceReplaceGeneration() <= generation {
		return next(ctx)
	}
	if compacted {
		i.logResult(result, "超窗补救")
	}
	i.bumpRetries(live.ID(), retries)
	return agent.RequestErrorAction{Retry: true}, nil
}

// onDisposed 把这个 agent 那份计数扔掉。
//
// 新增: 整条观察者是 Go 这边独有的，理由见 [Agents.OnDisposed]。
func (i *installer) onDisposed(live agent.Agent) { i.clearRetries(live.ID()) }

// logResult 记一行「这一次压掉了多少」。
//
// 源: packages/compaction/compaction-basic/src/index.ts:139-145
func (i *installer) logResult(result compaction.Result, trigger string) {
	i.logger.Info("压缩落地",
		"触发", trigger,
		"遮住的节点数", len(result.ShadowedSeqs),
		"起始 seq", result.ShadowedRange.Start,
		"结束 seq", result.ShadowedRange.End,
		"估计省下的 token", result.ShadowedTokenCount)
}

// markWarned 记下这条路由已经警告过；本次是不是第一次由返回值给出。
func (i *installer) markWarned(targetKey string) bool {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if _, seen := i.warned[targetKey]; seen {
		return false
	}
	i.warned[targetKey] = struct{}{}
	return true
}

// retriesOf 读这个 agent 这一串超窗补救已经做过几次。
func (i *installer) retriesOf(id session.SessionID) int {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.overflowRetries[id]
}

// bumpRetries 把计数记成 retries+1。
//
// 收 retries 而不是就地加一，是照抄 DSH 的 `set(agent, retries + 1)`：这一趟
// 用来和上限比的就是那个读数，写回去的必须是同一个数，否则两条并发的补救会
// 各自读到同一个数、却把计数推进两格。
func (i *installer) bumpRetries(id session.SessionID, retries int) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.overflowRetries[id] = retries + 1
}

// clearRetries 把这个 agent 那份计数扔掉。
func (i *installer) clearRetries(id session.SessionID) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	delete(i.overflowRetries, id)
}

// agentContextOf 从一个活 agent 现拼出压缩要的那一小片。
//
// 源: packages/compaction/compaction-basic/src/index.ts:153（compactIfNeeded(agent, ...)）
//
// 新增: DSH 的观察者拿到的就是 Agent 本身，而 [compaction.Engine] 收的是
// [compaction.AgentContext]——那是压缩自己声明的、比 Agent 窄得多的一片，
// 理由写在它上面。这一句就是两者之间那层现拼。
func agentContextOf(live agent.Agent) compaction.AgentContext {
	options := live.Options()
	return compaction.AgentContext{
		Session:  live.Session(),
		Provider: options.Provider,
		Model:    options.Model,
	}
}
