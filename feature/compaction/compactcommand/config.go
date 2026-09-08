// 本文件的作用：这个包的装配面——它要的那道压缩接缝、从作用域钥匙找回那一小片
// agent 的路，以及把 `/compact` 装上一个作用域再干净地摘下来的那两步。
//
// 源: packages/compaction/command-compact/src/index.ts:10-11, 84-106

package compactcommand

import (
	"context"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/scope"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/compaction/command-compact/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-command-compact"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/compaction/command-compact/src/index.ts:10
const PluginName = "command-compact"

// CommandName 是这条命令不带斜杠的名字。
//
// 源: packages/compaction/command-compact/src/index.ts:102
const CommandName = "compact"

// commandDescription 是发现界面上那句人读的摘要。
//
// 源: packages/compaction/command-compact/src/index.ts:103
const commandDescription = "Compact older conversation history"

// Config 是这条命令的装配面。
//
// 源: packages/compaction/command-compact/src/index.ts:11（inject）
type Config struct {
	// Engine 是那道压缩接缝，必填。
	Engine compaction.Engine

	// AgentOf 从一把作用域钥匙找到压缩要的那一小片 agent，必填。
	//
	// 新增: DSH 的 invocation.agent 就是 agent 对象本身，`compactNow` 直接收它。
	// Go 这边它是一把不透明的钥匙，而 [compaction.ManualAgentContext] 还要一个
	// [compaction.Maintainer]——那道空闲闸是人工压缩和正在跑的回合串起来的唯一办法。
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/feature/goal/goalcommand.Config.AgentOf] 逐字相同。
	//
	// 查不回来是**错**，不是一个错误结果：那不是用户能改的事（他敲的这行字本身
	// 没毛病），是装配没接对，该一路抛给调用方。
	AgentOf func(agent *scope.Key) (compaction.ManualAgentContext, error)
}

// Deps 是装这条命令那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/feature/goal/goalcommand.Deps] 一致。
type Deps struct {
	// Commands 是命令注册表，必填。
	Commands *commands.Runtime
}

// Controller 是攥着那道接缝、并且知道怎么把 `/compact` 装上一个作用域的那个对象。
//
// 源: packages/compaction/command-compact/src/index.ts:84-106
//
// 它唯一的可变状态是「此刻有几次 /compact 正在跑」，那是给摘除时等干净用的，
// 不是一把互斥锁：并发由压缩接缝那条持久的 compaction/start 挡着。
type Controller struct {
	engine  compaction.Engine
	agentOf func(agent *scope.Key) (compaction.ManualAgentContext, error)

	// mutex 护住 running。
	mutex sync.Mutex
	// idle 在 running 落回 0 时叫醒等着摘除的那一方。
	idle *sync.Cond
	// running 是此刻有几次 /compact 正在跑。
	//
	// 新增: DSH 是一个装着未落定 Promise 的 Set，摘除时 `Promise.allSettled`。
	// Go 里处理器是同步调用，没有那个可以攥在手里的凭证，所以退成一个计数。
	// 不用 [sync.WaitGroup]：注销和最后一次 Add 之间有一个真实存在的窗口
	//（注册表已经把定义交出去了、处理器还没进来），而在 Wait 已经开始之后 Add
	// 是 WaitGroup 明令禁止的用法。
	running int
}

// New 造一个控制器，把那两条装配规矩查一遍。
//
// 新增: DSH 这个包一个配置项都没有（apply 只收 ctx），因为那两样东西是 cordis
// 注入的。Go 这边它们是显式的字段，所以有一道装配校验；缺哪一样都当场拒，
// 而不是等到人敲下第一条 `/compact` 才在处理器里空指针。
func New(config Config) (*Controller, error) {
	switch {
	case config.Engine == nil:
		return nil, fmt.Errorf("compactcommand: 装 /compact 需要一道压缩接缝")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("compactcommand: 装 /compact 需要一条从作用域钥匙找回 agent 的路")
	}
	controller := &Controller{engine: config.Engine, agentOf: config.AgentOf}
	controller.idle = sync.NewCond(&controller.mutex)
	return controller, nil
}

// Install 把 `/compact` 装上一个作用域，返回摘掉它的函数。
//
// 源: packages/compaction/command-compact/src/index.ts:96-105（ctx.effect）
//
// 摘除**先注销、再等**：注销之后不会有新的 /compact 进来，然后等已经进去的那几次
// 跑完。DSH 用 `yield` 的次序表达同一件事——它的复合拆卸是后进先出，排水那一句
// 先 yield，于是最后才跑。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	if deps.Commands == nil {
		return nil, fmt.Errorf("compactcommand: 装 /compact 需要一张命令注册表")
	}
	undo, err := deps.Commands.Register(ctx, owner, commands.Definition{
		Name:        CommandName,
		Description: commandDescription,
		Handler:     c.run,
	})
	if err != nil {
		return nil, fmt.Errorf("compactcommand: 装 /compact 失败：%w", err)
	}
	return func(undoCtx context.Context) error {
		undoErr := undo(undoCtx)
		c.drain()
		return undoErr
	}, nil
}

// enter 记下又有一次 /compact 进来了。
func (c *Controller) enter() {
	c.mutex.Lock()
	c.running++
	c.mutex.Unlock()
}

// leave 记下一次 /compact 跑完了，并在最后一次跑完时叫醒等着摘除的那一方。
func (c *Controller) leave() {
	c.mutex.Lock()
	c.running--
	if c.running == 0 {
		c.idle.Broadcast()
	}
	c.mutex.Unlock()
}

// drain 等到一次 /compact 都不剩。
func (c *Controller) drain() {
	c.mutex.Lock()
	for c.running > 0 {
		c.idle.Wait()
	}
	c.mutex.Unlock()
}
