// 本文件的作用：把这一整套装上去——为每一个**新上台的根 agent** 造一份定时器投影、
// 在它自己那把作用域钥匙上装三件工具、再挂一个「它一转空闲就补一次重算」的钩子，
// 以及这几样摘下来时为什么必须按那个确切的反序走。
//
// 源: packages/schedule/schedule/src/index.ts

package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// Config 是装这一套要的那几样协作者。
//
// 源: packages/schedule/schedule/src/index.ts:35 (inject)
//
// 新增: DSH 那边这几样从 cordis 容器里按名字取（agents、sessions、tools、
// sessionPersistence）。Go 没有容器，所以摆成一个结构体明着传进来——顺带把
// 「少给一样会怎样」从一次运行期的属性缺失变成 [Install] 当场报的错。
type Config struct {
	// Agents 是那张 agent 注册表：用来发现新的根、判断谁还活着、听状态跃迁。
	Agents Agents
	// Sessions 是那道共享落盘屏障。
	Sessions Sessions
	// Tools 是工具运行时，三件工具装在它上面。
	Tools *tools.Runtime
	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
	// Now 是墙上时钟的取样口；不给就用 [time.Now]。
	//
	// 留成可替换的是为了测试：这一整套的行为全挂在「此刻几点」上，不能替的话
	// 每一条固定频率的用例都得真等一个间隔过去。
	Now func() time.Time
}

// ownerState 是一个根 agent 身上装着的那一套。
type ownerState struct {
	runtime  *Runtime
	teardown func(context.Context) error
}

// installation 是这一次 [Install] 的全部可变状态。
//
// 源: packages/schedule/schedule/src/index.ts:41-42
type installation struct {
	config Config
	// base 是给装上去的那些东西用的长命 ctx。
	//
	// 新增: 创建观察者收到的那条 ctx 只活到这一次事件派发结束，而这里装上去的
	// 投影和工具要活到这个 agent 被摘掉为止。拿它去派生，投影会在事件派发一结束
	// 就被连坐取消。
	base context.Context
	// transactions 是那张「一个 agent 一把闸」的表，所有 agent 共用一张。
	//
	// 一张而不是一个 agent 一张：那三件工具和那份投影必须共用同一把闸，否则两边
	// 会各折各的日志、各写各的 create（见 [transactions]）。表本身是按 agent 分的，
	// 所以共用一张不会让两个 agent 互相挡着。
	transactions *transactions

	mutex sync.Mutex
	// stopping 一旦立起来就不再接新的 agent。
	stopping bool
	// owners 是此刻装着这一套的那些根 agent。
	owners map[agent.Agent]*ownerState
}

// Install 把 schedule 装上去，交回把它整个摘下来的函数。
//
// 源: packages/schedule/schedule/src/index.ts:42-84（apply）
//
// owner 决定这次登记落在哪一层，规矩和本仓库别处一样：[scope.NewRoot] 造出来的
// 作用域没有身份，落全局层，看得见每一个 agent；有身份的只看得见它那条链下面的。
//
// **只管装上去之后才上台的根 agent**：装之前就已经在跑的那些一个都不补。这是
// DSH 那边写在文档里的选择，照搬——补装意味着要为一段已经跑了一半的会话凭空造出
// 一份投影，而那份日志里可能已经有到期很久的提醒，补装的一瞬间它们会一起炸出来。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	config Config,
) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, errors.New("schedule: 需要一个持有这次登记的作用域")
	case config.Agents == nil:
		return nil, errors.New("schedule: 需要一个 agent 注册表")
	case config.Sessions == nil:
		return nil, errors.New("schedule: 需要一个会话仓库")
	case config.Tools == nil:
		return nil, errors.New("schedule: 需要一个工具运行时")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	install := &installation{
		config:       config,
		base:         context.WithoutCancel(ctx),
		transactions: newTransactions(),
		owners:       map[agent.Agent]*ownerState{},
	}
	stopCreated, err := config.Agents.OnCreated(ctx, owner, install.onCreated)
	if err != nil {
		return nil, fmt.Errorf("schedule: 装创建观察者失败：%w", err)
	}
	return func(undoCtx context.Context) error {
		return install.dispose(undoCtx, stopCreated)
	}, nil
}

// onCreated 是那个创建观察者：为一个新上台的根 agent 装齐这一套。
//
// 源: packages/schedule/schedule/src/index.ts:45-67
//
// 三种情况直接放过：正在收摊、这个 agent 已经装过了、它不是根。只给根装是因为
// 提醒是**会话本地**的，而一个子 agent 的会话随它那次调用一起结束——给它装出来的
// 提醒不会有人收。
//
// 装不上就把这次创建否掉。DSH 那边这个处理器抛出去也是同样的效果，而且这样才对：
// 一个装不上提醒的 agent 照样上台，模型会看到三件工具不在，或者更糟——工具在、
// 投影不在，于是提醒永远只写进日志、一次都不响。
func (i *installation) onCreated(ctx context.Context, created agent.Agent) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.stopping {
		return nil
	}
	if _, present := i.owners[created]; present {
		return nil
	}
	if !isRoot(i.config.Agents, created) {
		return nil
	}

	runtime := newRuntime(i.base, created, runtimeDeps{
		agents:       i.config.Agents,
		sessions:     i.config.Sessions,
		logger:       i.config.Logger,
		now:          i.config.Now,
		transactions: i.transactions,
	})
	teardown, err := i.attach(created, runtime)
	if err != nil {
		// 投影还没 Start，Dispose 只是把那条 ctx 掐掉；不 Dispose 会漏掉那条 ctx。
		runtime.Dispose()
		return err
	}
	runtime.Start()
	i.owners[created] = &ownerState{runtime: runtime, teardown: teardown}
	// ctx 在这里只用来判「这次创建还作数吗」：上面那几步全是同步的，但注册表的
	// 观察者是可以被取消的，取消之后再把这一套留在表里就没人来摘了。
	if err := ctx.Err(); err != nil {
		delete(i.owners, created)
		i.tearDownOne(context.WithoutCancel(ctx), created, runtime, teardown)
		return context.Cause(ctx)
	}
	return nil
}

// attach 把三件工具和那个空闲钩子装到这个 agent 自己那把作用域钥匙上。
//
// 源: packages/schedule/schedule/src/index.ts:48-55
//
// 装在 agent 自己那一层，不是装在 [Install] 那个 owner 上：这三件工具只该出现在
// 这一段会话里，而那个空闲钩子也只该听这一个 agent 的跃迁。
func (i *installation) attach(
	owner agent.Agent,
	runtime *Runtime,
) (func(context.Context) error, error) {
	set := &toolSet{
		owner:           owner,
		sessions:        i.config.Sessions,
		transactions:    runtime.transactions,
		now:             i.config.Now,
		onDurableChange: runtime.RequestDrive,
	}
	disposeTools, err := registerTools(i.base, i.config.Tools, owner.Scope(), set)
	if err != nil {
		return nil, err
	}
	stopStatus, err := i.config.Agents.OnStatus(i.base, owner.Scope(), func(
		reported agent.Agent,
		status agent.Status,
	) {
		// 新增: DSH 那个处理器靠闭包捕获 agent，不看是谁报的，于是一个挂在它下面的
		// 子 agent 转空闲也会白触发一次重算。这里多问一句：便宜，而且更准。
		if reported != owner || status != agent.StatusIdle {
			return
		}
		if usesSchedule(owner) {
			runtime.RequestDrive()
		}
	})
	if err != nil {
		// 摘的时候不带调用方的取消：这条路上 ctx 可能已经废了，装上去的还是得收回来。
		_ = disposeTools(context.WithoutCancel(i.base))
		return nil, fmt.Errorf("schedule: 装空闲钩子失败：%w", err)
	}
	// 反序：先摘钩子再摘工具。反过来的话，工具都没了却还留着一个会去请求重算的
	// 钩子，那次重算折出来的是一段没人再改得动的日志。
	return func(undoCtx context.Context) error {
		failures := []error{stopStatus(undoCtx), disposeTools(undoCtx)}
		return errors.Join(failures...)
	}, nil
}

// usesSchedule 问这段会话到底用没用过提醒。
//
// 源: packages/schedule/schedule/src/index.ts:51
//
// 这只是一道省事的闸：一段从没写过 schedule/change 的会话，转空闲时不必把投影叫
// 醒一遍。所以它**不**按 seedLength 切——从父那里继承来的那一段虽然不归这条日志
// 管，但多醒一次的代价只是白折一遍，而漏掉一次的代价是一条提醒不响。
func usesSchedule(owner agent.Agent) bool {
	for _, event := range owner.Session().Events() {
		if event.Type == EventChange {
			return true
		}
	}
	return false
}

// isRoot 问这个 agent 此刻是不是一个顶层 agent。
//
// 源: packages/schedule/schedule/src/index.ts:46
func isRoot(agents Agents, candidate agent.Agent) bool {
	for _, root := range agents.Roots() {
		if root == candidate {
			return true
		}
	}
	return false
}

// tearDownOne 把一个 agent 身上这一套按那个确切的反序摘干净。
//
// 源: packages/schedule/schedule/src/index.ts:56-64
//
// 次序是「钩子和工具先走，投影最后走」：反过来的话，投影没了而工具还在，模型这时候
// 建出来的提醒会写进日志却永远没人来响，而它拿到的是一个成功的结果。
func (i *installation) tearDownOne(
	ctx context.Context,
	owner agent.Agent,
	runtime *Runtime,
	teardown func(context.Context) error,
) error {
	err := teardown(ctx)
	runtime.Dispose()
	if err != nil {
		return fmt.Errorf("schedule: 摘 %s 身上的 schedule 失败：%w", owner.ID(), err)
	}
	return nil
}

// dispose 收摊：不再接新的 agent，然后把已经装上的一个个摘掉。
//
// 源: packages/schedule/schedule/src/index.ts:69-75
//
// 先立 stopping 再停创建观察者，两件事都要做：只停观察者的话，一次正卡在中途的
// 创建照样能把自己塞进表里，而那时候这里已经在遍历表了。
func (i *installation) dispose(ctx context.Context, stopCreated func(context.Context) error) error {
	i.mutex.Lock()
	i.stopping = true
	owners := i.owners
	i.owners = map[agent.Agent]*ownerState{}
	i.mutex.Unlock()

	failures := []error{stopCreated(ctx)}
	for owner, state := range owners {
		failures = append(failures, i.tearDownOne(ctx, owner, state.runtime, state.teardown))
	}
	return errors.Join(failures...)
}
