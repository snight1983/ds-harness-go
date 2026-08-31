// 本文件的作用：把这一整套装上去——那十条观察者、每个 agent 一台驱动，
// 以及摘下来时为什么必须先让驱动收摊、再撤观察者。
//
// 源: packages/goal/goal-round-driver/src/index.ts:76-95、243-331、416-444

package goalrounddriver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/goal/goal"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// PluginName 是本包在那套插件命名里占的位置。
//
// 源: packages/goal/goal-round-driver/src/index.ts:18
//
// 新增: DSH 那是 cordis 的 `export const name`，容器靠它认插件。Go 没有那个容器，
// 这个常量留着只为让日志和不变量注册表里的名字跟 DSH 对得上。
const PluginName = "goal-round-driver"

// PackageName 是本包在不变量注册表里占的那个名字。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-goal-round-driver"

// Agents 是本包要的那张 agent 注册表。
//
// 源: packages/goal/goal-round-driver/src/index.ts:19 (inject 'agents')
//
// 新增: DSH 从 cordis 容器里按名字取。Go 没有容器，所以摆成一个窄接口明着传进来——
// 窄到只剩本包真正用得着的那几条，好让测试能替换它，也好让读的人一眼看见本包到底
// 碰了注册表的哪些面。
type Agents interface {
	// Get 按标识取此刻活着的那个 agent。
	Get(id session.SessionID) (agent.Agent, bool)
	// List 列出此刻在场的全部 agent。
	List() []agent.Agent

	OnCreated(ctx context.Context, owner *scope.Scope, observer agent.CreatedObserver) (func(context.Context) error, error)
	OnDisposed(ctx context.Context, owner *scope.Scope, observer agent.DisposedObserver) (func(context.Context) error, error)
	OnStatus(ctx context.Context, owner *scope.Scope, observer agent.StatusObserver) (func(context.Context) error, error)
	OnSessionStart(ctx context.Context, owner *scope.Scope, observer agent.SessionStartObserver) (func(context.Context) error, error)
	OnError(ctx context.Context, owner *scope.Scope, observer agent.ErrorObserver) (func(context.Context) error, error)
	OnInboxInserted(ctx context.Context, owner *scope.Scope, observer agent.InboxObserver) (func(context.Context) error, error)
	OnInboxClaimed(ctx context.Context, owner *scope.Scope, observer agent.InboxClaimedObserver) (func(context.Context) error, error)
	OnInboxDiscarded(ctx context.Context, owner *scope.Scope, observer agent.InboxObserver) (func(context.Context) error, error)
	OnPreStep(ctx context.Context, owner *scope.Scope, observer agent.PreStepObserver) (func(context.Context) error, error)
}

// Goals 是本包要的那台目标服务。
//
// 源: packages/goal/goal-round-driver/src/index.ts:19 (inject 'goals')
//
// 本包只读目标、只用 disarm/pause/block 这三种收尾——它一次都不 create、不 edit、
// 不 complete。那三件事要么开出新预算、要么宣布结果，都不该由一台自动驱动做主。
type Goals interface {
	Get(owner agent.Agent) (*goal.View, error)
	Disarm(owner agent.Agent) (*goal.View, error)
	Pause(owner agent.Agent, ref goal.Ref) (*goal.View, error)
	Block(owner agent.Agent, ref goal.Ref, reason goal.BlockReason) (*goal.View, error)
	OnChanged(ctx context.Context, owner *scope.Scope, observer goal.ChangedObserver) (func(context.Context) error, error)
}

// Sessions 是本包要的那道会话面。
//
// 源: packages/goal/goal-round-driver/src/index.ts:19 (inject 'sessions')
type Sessions interface {
	// Flush 跑一次共享的落盘检查点。
	Flush(ctx context.Context, live *coresession.Session) (bool, error)
	// OnEvent 登记一个「一条事件提交进日志了」的观察者。
	OnEvent(ctx context.Context, owner *scope.Scope, observer coresession.EventObserver) (func(context.Context) error, error)
}

// Config 是装这一套要的那几样协作者。
//
// 源: packages/goal/goal-round-driver/src/index.ts:19 (inject)
type Config struct {
	// Agents 是那张 agent 注册表。
	Agents Agents
	// Goals 是那台目标服务。
	Goals Goals
	// Sessions 是那道落盘屏障加日志广播。
	Sessions Sessions
	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
}

// installation 是这一次 [Install] 的全部可变状态。
//
// 源: packages/goal/goal-round-driver/src/index.ts:77
type installation struct {
	config Config
	// base 是给那些驱动用的长命 ctx。
	//
	// 新增: 创建观察者收到的那条 ctx 只活到这一次事件派发结束，而一台驱动要活到这个
	// agent 被摘掉为止。拿它去派生，驱动会在事件派发一结束就被连坐取消。
	base context.Context

	mutex    sync.Mutex
	stopping bool
	drivers  map[agent.Agent]*driver
}

// Install 把续推驱动装上去，交回把它整个摘下来的函数。
//
// 源: packages/goal/goal-round-driver/src/index.ts:76-95、416-444
//
// 装上去的第一件事是把**已经在场**的每一个 agent 打回未活化。这不是保守，是这套
// 东西的立身之本：一台生命周期驱动绝不从上一个生产方那里继承一份看不见的自动授权。
// 不这么做的话，换一次驱动实现就等于凭空替人批准了一批还点着的目标接着自动跑。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	config Config,
) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, errors.New("goalrounddriver: 需要一个持有这次登记的作用域")
	case config.Agents == nil:
		return nil, errors.New("goalrounddriver: 需要一个 agent 注册表")
	case config.Goals == nil:
		return nil, errors.New("goalrounddriver: 需要一台目标服务")
	case config.Sessions == nil:
		return nil, errors.New("goalrounddriver: 需要一个会话仓库")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	install := &installation{
		config:  config,
		base:    context.WithoutCancel(ctx),
		drivers: map[agent.Agent]*driver{},
	}
	stop, err := install.observe(ctx, owner)
	if err != nil {
		return nil, err
	}
	// 观察者装齐之后才扫在场的那批：反过来的话，扫到一半新上台的 agent 谁都不管。
	for _, live := range config.Agents.List() {
		install.driverFor(live).disarm()
	}
	return func(undoCtx context.Context) error {
		return install.dispose(undoCtx, stop)
	}, nil
}

// registration 是一条观察者登记：装它的那句，和它自己的名字。
type registration struct {
	label  string
	attach func() (func(context.Context) error, error)
}

// observe 按次序装齐那十条观察者，任何一条装不上就把已经装上的按反序撤掉。
//
// 源: packages/goal/goal-round-driver/src/index.ts:246-414
func (i *installation) observe(
	ctx context.Context,
	owner *scope.Scope,
) (func(context.Context) error, error) {
	config := i.config
	registrations := []registration{
		{"出错", func() (func(context.Context) error, error) {
			return config.Agents.OnError(ctx, owner, func(failure agent.TurnError) {
				i.driverFor(failure.Agent).disarm()
			})
		}},
		{"创建", func() (func(context.Context) error, error) {
			return config.Agents.OnCreated(ctx, owner, func(_ context.Context, created agent.Agent) error {
				i.driverFor(created)
				return nil
			})
		}},
		{"处置", func() (func(context.Context) error, error) {
			return config.Agents.OnDisposed(ctx, owner, i.onDisposed)
		}},
		{"会话起跑", func() (func(context.Context) error, error) {
			return config.Agents.OnSessionStart(ctx, owner, func(live agent.Agent, _ agent.SessionStartSource) {
				i.driverFor(live).onSessionStart()
			})
		}},
		{"状态跃迁", func() (func(context.Context) error, error) {
			return config.Agents.OnStatus(ctx, owner, func(live agent.Agent, status agent.Status) {
				i.driverFor(live).onStatus(status)
			})
		}},
		{"目标改动", func() (func(context.Context) error, error) {
			return config.Goals.OnChanged(ctx, owner, func(live agent.Agent, _ goal.Changed) {
				i.driverFor(live).onGoalChanged()
			})
		}},
		{"收件箱入队", func() (func(context.Context) error, error) {
			return config.Agents.OnInboxInserted(ctx, owner, func(live agent.Agent, message llm.Message) {
				i.driverFor(live).onInboxInserted(message)
			})
		}},
		{"收件箱认领", func() (func(context.Context) error, error) {
			return config.Agents.OnInboxClaimed(ctx, owner, func(live agent.Agent, message llm.Message, _ int) {
				i.driverFor(live).onInboxClaimed(message)
			})
		}},
		{"收件箱丢弃", func() (func(context.Context) error, error) {
			return config.Agents.OnInboxDiscarded(ctx, owner, func(live agent.Agent, message llm.Message) {
				i.driverFor(live).onInboxDiscarded(message)
			})
		}},
		{"会话事件", func() (func(context.Context) error, error) {
			return config.Sessions.OnEvent(ctx, owner, i.onSessionEvent)
		}},
		{"前置步骤", func() (func(context.Context) error, error) {
			return config.Agents.OnPreStep(ctx, owner, i.onPreStep)
		}},
	}

	var undo []func(context.Context) error
	unwind := func(cause error, label string) (func(context.Context) error, error) {
		for _, stop := range slices.Backward(undo) {
			_ = stop(context.WithoutCancel(ctx))
		}
		return nil, fmt.Errorf("goalrounddriver: 装%s观察者失败：%w", label, cause)
	}
	for _, entry := range registrations {
		stop, err := entry.attach()
		if err != nil {
			return unwind(err, entry.label)
		}
		undo = append(undo, stop)
	}
	return func(undoCtx context.Context) error {
		failures := make([]error, 0, len(undo))
		for _, stop := range slices.Backward(undo) {
			failures = append(failures, stop(undoCtx))
		}
		return errors.Join(failures...)
	}, nil
}

// driverFor 取这个 agent 身上那台驱动，没有就当场造一台并开起来。
//
// 源: packages/goal/goal-round-driver/src/index.ts:80-94
//
// 懒建而不是只在 created 那条边上建：本包那些观察者都是全局装的，它们完全可能先
// 收到一个「装之前就已经在场」的 agent 的事件。收摊之后一律交回一台立着 stopping
// 的空壳而不是 nil——调用点全是观察者，它们没有一条能处理 nil 的路，而给它们一台
// 什么都不做的驱动，语义恰好就是「本包已经不管这个 agent 了」。
func (i *installation) driverFor(live agent.Agent) *driver {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if existing, present := i.drivers[live]; present {
		return existing
	}
	made := newDriver(i.base, live, driverDeps{
		agents:   i.config.Agents,
		goals:    i.config.Goals,
		sessions: i.config.Sessions,
		logger:   i.config.Logger,
	})
	if i.stopping {
		made.stopping = true
		// 这台空壳永远不会 start()，也就永远没人替它收 ctx；不在这里掐掉就是一条泄漏。
		made.cancel()
		return made
	}
	made.start()
	i.drivers[live] = made
	return made
}

// lookup 只取已经建过的那台驱动，不建新的。
func (i *installation) lookup(live agent.Agent) (*driver, bool) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	found, present := i.drivers[live]
	return found, present
}

// onDisposed 是处置那条边：把这台驱动从表里摘掉并停干净。
//
// 源: packages/goal/goal-round-driver/src/index.ts:252
//
// 不在这里 disarm：agent 都已经离开注册表了，目标服务那一侧读不回它，
// 那次调用只会白报一条 GOAL_AGENT_NOT_LIVE。
func (i *installation) onDisposed(gone agent.Agent) {
	i.mutex.Lock()
	found, present := i.drivers[gone]
	delete(i.drivers, gone)
	i.mutex.Unlock()
	if present {
		found.stop(context.WithoutCancel(i.base))
	}
}

// onSessionEvent 把日志广播派给拥有这段会话的那台驱动。
//
// 源: packages/goal/goal-round-driver/src/index.ts:307-331
//
// 只找已经建过的驱动，不为一条广播凭空建一台：会话仓库里躺着的会话不一定都有
// agent 在驱动它们（比如一段只是被读出来看看的历史）。
func (i *installation) onSessionEvent(live *coresession.Session, event session.Event) {
	owner, present := i.config.Agents.Get(live.ID())
	if !present {
		return
	}
	found, present := i.lookup(owner)
	if !present || !found.owns(live) {
		return
	}
	found.onSessionEvent(event)
}

// onPreStep 把前置步骤那条瀑布派给这个 agent 身上的驱动。
//
// 源: packages/goal/goal-round-driver/src/index.ts:349-414
func (i *installation) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	return i.driverFor(step.Agent).onPreStep(ctx, step, next)
}

// dispose 收摊：先让每一台驱动收干净，再撤掉那些观察者。
//
// 源: packages/goal/goal-round-driver/src/index.ts:423-443
//
// 这个次序是 DSH 那个 `yield` 摆出来的，照搬，而且必须是这个次序：驱动收摊时会掐
// 一个正在跑的回合并等它静下来，那段等待里还会走过 turn/end、状态跃迁这些边——
// 观察者要是先撤了，那些边上没人给这台正在退场的驱动收尾。
func (i *installation) dispose(ctx context.Context, stop func(context.Context) error) error {
	i.mutex.Lock()
	i.stopping = true
	drivers := i.drivers
	i.drivers = map[agent.Agent]*driver{}
	i.mutex.Unlock()

	for _, live := range drivers {
		live.stop(ctx)
	}
	return stop(ctx)
}
