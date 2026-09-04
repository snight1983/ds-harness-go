// 本文件的作用：把这一层装到运行时上——要哪几张面、装哪四个观察者、
// 以及摘下来的时候按什么次序撤。
//
// 源: packages/context/agent-instructions/src/index.ts:80-103

package instructions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// Agents 是本包要的那张 agent 注册表。
//
// 源: packages/context/agent-instructions/src/index.ts:322、305（ctx.on）
//
// 新增: DSH 从 cordis 容器里按事件名挂监听器。Go 没有那个容器，所以摆成一个窄接口
// 明着传进来——窄到只剩本包真正用得着的那两条，好让测试能替换它，也好让读的人一眼
// 看见本包到底碰了注册表的哪些面。先例是 goal/goalrounddriver.Agents。
type Agents interface {
	// OnPreStep 是本层往对话里补那条工作区上下文的地方。
	OnPreStep(ctx context.Context, owner *scope.Scope, observer agent.PreStepObserver) (func(context.Context) error, error)

	// OnDisposed 是本层把一个 agent 的会话状态扔掉的地方。
	//
	// 新增: DSH 这五张表全是 WeakMap，键是 Session 或者 Agent 对象，靠 JS 的
	// 垃圾回收来清理，所以那边**没有**这条观察者。Go 没有弱引用表，状态按会话标识
	// 存在一张普通 map 里，就必须有人来删——这条边就是删它的地方。
	OnDisposed(ctx context.Context, owner *scope.Scope, observer agent.DisposedObserver) (func(context.Context) error, error)
}

// Sessions 是本包要的那道会话日志广播。
//
// 源: packages/context/agent-instructions/src/index.ts:295（`session/event`）
type Sessions interface {
	// OnEvent 登记一个「一条事件提交进日志了」的观察者。
	OnEvent(ctx context.Context, owner *scope.Scope, observer coresession.EventObserver) (func(context.Context) error, error)
}

// ToolResults 是本包要的那道工具结果广播。
//
// 源: packages/context/agent-instructions/src/index.ts:350（`tools/result`）
type ToolResults interface {
	// ObserveResult 登记一个「一次工具调用有结果了」的观察者。
	ObserveResult(ctx context.Context, owner *scope.Scope, observer tools.ResultObserver) (func(context.Context) error, error)
}

// Deps 是装这一层要的那几样协作者。
//
// 源: packages/context/agent-instructions/src/index.ts:80（`apply(ctx, config)` 的 ctx）
//
// 名字叫 Deps 不叫 Config，是因为 [Config] 已经被本包自己那份配置值占了；
// 先例是 plan/planmode、goal/goaltool 和 context/timecontext。
type Deps struct {
	// Agents 是那张 agent 注册表，必填。
	Agents Agents
	// Sessions 是那道会话日志广播，必填。
	Sessions Sessions
	// Tools 是那道工具结果广播，必填。
	Tools ToolResults

	// FS 是读指令文件的那道接缝，必填。
	//
	// 新增: DSH 是 `ctx.get('fs')`，取不到就整层静默失灵（`compose` 直接返回
	// undefined）。这里它是装配时的必填项：一个「装上了但什么都不做」的上下文层
	// 是查不出来的故障，而装不上去是当场就能看见的。
	FS fs.FileSystem

	// WorkspaceRoot 给出一个会话那份工作区在 [fs.FileSystem] 命名空间里的根路径，
	// 必填。第二个返回值为假表示这个会话不属于任何工作区，这一层于是什么都不说。
	//
	// 新增: DSH 直接读会话头上的 `cwd`，那是一条宿主机路径。本仓库的会话头记的是
	// 一个不透明的工作区标识（见 [sessionlog.SessionHeader.WorkspaceID]），从标识换到
	// 根路径这一步归装配方做——它才认识工作区登记册，而本包不该认识。
	//
	// 新增: 交回的是**路径串**而不是 [fs.Target]，因为本包唯一真正需要一棵树的
	// 地方（[FindProjectRoot] 和 [AncestorChain]）要逐级往上走，而 [fs.Target]
	// 上没有可上行的坐标：[fs.Target.TargetKey] 不透明，[fs.Target.DisplayPath]
	// 按 fs 包的规定只能给人看。这条路径和 [fs.FileSystem.Resolve] 收的是同一个
	// 命名空间，和宿主机没有关系。
	WorkspaceRoot func(ctx context.Context, workspaceID sessionlog.WorkspaceID) (string, bool, error)

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 `exec.agent` 直接就是 Agent 对象。Go 这边
	// [tools.Execution.Agent] 是一把 [scope.Key]（见 tools 的说明），
	// 所以要多一步查回来。查不回来记一行警告并丢掉这次触碰——一次读文件的副作用
	// 补不上，代价是模型少看见一次指令变更；让整条工具结果路径出错，代价大得多。
	// 和 [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Config.AgentOf] 的取舍相反，理由就在这里。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
}

// installer 是这一次 [Install] 的全部可变状态。
//
// 源: packages/context/agent-instructions/src/index.ts:81-103
type installer struct {
	config        ResolvedConfig
	fsys          fs.FileSystem
	workspaceRoot func(context.Context, sessionlog.WorkspaceID) (string, bool, error)
	agentOf       func(*scope.Key) (agent.Agent, error)
	logger        *slog.Logger

	// lifetime 是那些异步投影用的长命 ctx，stop 把它整个掐掉。
	//
	// 新增: DSH 是 `projectionLifecycle` 那个 AbortController。观察者收到的那条 ctx
	// 只活到这一次事件派发结束，而一次投影要活到它自己跑完为止，拿它去派生的话，
	// 投影会在派发一结束就被连坐取消。
	lifetime context.Context
	stop     context.CancelFunc

	mutex sync.Mutex
	// sessions 是每段会话那份状态，键是会话标识。
	//
	// 新增: DSH 那边是五张 WeakMap（版本表、基线准备、投影队尾、步骤开着没有、
	// 攒着的触碰），键是 Session 或者 Agent 对象。Go 没有弱引用表，所以它们合成
	// 一张按标识取的普通 map，清理走 [Agents.OnDisposed]。合成一张而不是照抄五张，
	// 是因为它们的生命周期本来就完全一致，分开只会多出「有的删了有的没删」这种状态。
	sessions map[sessionlog.SessionID]*sessionState
	// touches 是一次工具调用攒下的那些触碰，键是执行 token。
	//
	// 它不按会话分，因为一次嵌套派发的触碰要沿着 [tools.Execution.Parent]
	// 往上并，而那条链和会话没有关系。
	touches map[tools.ExecutionToken][]touch
}

// Install 把工作区指令这一层装上去，交回把它整个摘下来的函数。
//
// 源: packages/context/agent-instructions/src/index.ts:83-358
//
// 装上去之后不会主动去读任何文件：这一层全部由 agent 的步骤边界和工具结果驱动。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	config Config,
	deps Deps,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, errors.New("instructions: 需要一个持有这次登记的作用域")
	}
	install, err := newInstaller(ctx, config, deps)
	if err != nil {
		return nil, err
	}
	undo, err := install.observe(ctx, owner, deps)
	if err != nil {
		install.stop()
		return nil, err
	}
	return func(undoCtx context.Context) error {
		// 先掐投影再撤观察者：反过来的话，最后一条撤掉之后还可能有一次在飞的投影
		// 正往一个已经不归本包管的收件箱里写。
		install.shutdown()
		return undo(undoCtx)
	}, nil
}

// newInstaller 验一遍协作者，造出这一次装配的全部可变状态。
//
// 它和挂观察者那一步分开，是因为「配置和协作者合不合法」跟「瀑布上挂得上挂不上」
// 是两件独立的事：前者不合法时**一条观察者都还没挂**，撤都不用撤。
func newInstaller(ctx context.Context, config Config, deps Deps) (*installer, error) {
	switch {
	case deps.Agents == nil:
		return nil, errors.New("instructions: 需要一张 agent 注册表")
	case deps.Sessions == nil:
		return nil, errors.New("instructions: 需要一道会话日志广播")
	case deps.Tools == nil:
		return nil, errors.New("instructions: 需要一道工具结果广播")
	case deps.FS == nil:
		return nil, errors.New("instructions: 需要一个文件系统")
	case deps.WorkspaceRoot == nil:
		return nil, errors.New("instructions: 需要一条从工作区标识找到根路径的路")
	case deps.AgentOf == nil:
		return nil, errors.New("instructions: 需要一条从作用域钥匙找回 agent 的路")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// 投影那条 ctx 从**装配时**这条派生，而不是从某一次事件派发那条派生：
	// 后者一派发完就取消，而一次投影要活到自己跑完。
	lifetime, stop := context.WithCancel(context.WithoutCancel(ctx))
	return &installer{
		config:        config.Resolve(),
		fsys:          deps.FS,
		workspaceRoot: deps.WorkspaceRoot,
		agentOf:       deps.AgentOf,
		logger:        logger,
		lifetime:      lifetime,
		stop:          stop,
		sessions:      map[sessionlog.SessionID]*sessionState{},
		touches:       map[tools.ExecutionToken][]touch{},
	}, nil
}

// registration 是一条观察者登记：装它的那句，和它自己的名字。
type registration struct {
	label  string
	attach func() (func(context.Context) error, error)
}

// observe 按次序装齐那四条观察者，任何一条装不上就把已经装上的按反序撤掉。
//
// 源: packages/context/agent-instructions/src/index.ts:305-357
func (i *installer) observe(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	registrations := []registration{
		{"会话事件", func() (func(context.Context) error, error) {
			return deps.Sessions.OnEvent(ctx, owner, i.onSessionEvent)
		}},
		{"前置步骤", func() (func(context.Context) error, error) {
			return deps.Agents.OnPreStep(ctx, owner, i.onPreStep)
		}},
		{"工具结果", func() (func(context.Context) error, error) {
			return deps.Tools.ObserveResult(ctx, owner, i.onToolResult)
		}},
		{"处置", func() (func(context.Context) error, error) {
			return deps.Agents.OnDisposed(ctx, owner, i.onDisposed)
		}},
	}

	var undo []func(context.Context) error
	unwind := func(cause error, label string) (func(context.Context) error, error) {
		for _, stop := range slices.Backward(undo) {
			_ = stop(context.WithoutCancel(ctx))
		}
		return nil, fmt.Errorf("instructions: 装%s观察者失败：%w", label, cause)
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

// shutdown 掐掉投影那条命，并把攒着的状态全扔掉。
//
// 源: packages/context/agent-instructions/src/index.ts:88-94（ctx.effect 的收尾）
func (i *installer) shutdown() {
	i.stop()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	clear(i.touches)
	clear(i.sessions)
}

// onDisposed 把这个 agent 那段会话的状态扔掉。
//
// 新增: 整条观察者是 Go 这边独有的，理由见 [Agents.OnDisposed]。
func (i *installer) onDisposed(live agent.Agent) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	delete(i.sessions, live.Session().ID())
}
