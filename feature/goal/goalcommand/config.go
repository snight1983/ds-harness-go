// 本文件的作用：这个包的装配面——它要的那一小块目标服务、从作用域钥匙找回 agent
// 的那条路、以及把 `/goal` 装上一个作用域的那一步。
//
// 源: packages/goal/command-goal/src/index.ts:12-13, 189-196

package goalcommand

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/goal/command-goal/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-command-goal"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/goal/command-goal/src/index.ts:12
const PluginName = "command-goal"

// CommandName 是这条命令不带斜杠的名字。
//
// 源: packages/goal/command-goal/src/index.ts:191
const CommandName = "goal"

// Service 是这条命令用得到的那一块目标服务。
//
// 新增: DSH 靠 `inject = ['commands', 'goals']` 注入整个 `ctx.goals`。这里只写出
// 真正被调到的那六个方法（窄口子的理由同 [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Service]），
// 交进来的 [github.com/snight1983/ds-harness-go/feature/goal.Service] 自然满足它。
//
// 和那一套比，这里**多**一个 [Service.Clear]、**少**掉 Complete 和 Block：清目标
// 是人的权力不是模型的，而报完成和报阻塞是模型向人交代，人不需要向自己交代。
type Service interface {
	// Get 读一个确切的活 agent 此刻的目标，没有就交回 nil。
	Get(owner agent.Agent) (*goal.View, error)
	// Create 建一个目标并且当场点亮它。
	Create(owner agent.Agent, request goal.CreateRequest) (*goal.View, error)
	// Edit 只换目标描述和轮数上限。
	Edit(owner agent.Agent, ref goal.Ref, request goal.EditRequest) (*goal.View, error)
	// Pause 把一个 active 的目标停下。
	Pause(owner agent.Agent, ref goal.Ref) (*goal.View, error)
	// Resume 把一个停住的目标重新推起来。
	Resume(owner agent.Agent, ref goal.Ref) (*goal.View, error)
	// Clear 清掉当前目标，留下一块带修订号的墓碑。
	Clear(owner agent.Agent, ref goal.Ref) (goal.Ref, error)
}

// Config 是这条命令的装配面。
//
// 源: packages/goal/command-goal/src/index.ts:189
type Config struct {
	// Service 是那台目标服务，必填。
	Service Service

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 invocation.agent 就是 agent 对象本身。Go 这边它是一把不透明的
	// 钥匙，所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Config.AgentOf] 逐字相同。
	//
	// 查不回来就是错，不是一个错误结果：那不是用户能改的事（他敲的这行字本身没
	// 毛病），是装配没接对，该一路抛给调用方。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
}

// Deps 是装这条命令那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Deps] 一致。
type Deps struct {
	// Commands 是命令注册表，必填。
	Commands *commands.Runtime
}

// Controller 是攥着那台服务、并且知道怎么把 `/goal` 装上一个作用域的那个对象。
//
// 源: packages/goal/command-goal/src/index.ts:189-196
//
// 它造出来之后就**不再变**，所以不带锁：本包一个字节的可变状态都不持有。每一次
// 调用读写的都是那台目标服务，那边罩着自己的锁。
type Controller struct {
	service Service
	agentOf func(agent *scope.Key) (agent.Agent, error)
}

// New 造一个控制器，把那两条装配规矩查一遍。
//
// 源: packages/goal/command-goal/src/index.ts:189
//
// 新增: DSH 这个包一个配置项都没有（apply 只收 ctx），因为那两样东西是 cordis
// 注入的。Go 这边它们是显式的字段，所以有一道装配校验；缺哪一样都当场拒，
// 而不是等到人敲下第一条 `/goal` 才在处理器里空指针。
func New(config Config) (*Controller, error) {
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("goalcommand: 装 /goal 需要一台目标服务")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("goalcommand: 装 /goal 需要一条从作用域钥匙找回 agent 的路")
	}
	return &Controller{service: config.Service, agentOf: config.AgentOf}, nil
}

// Install 把 `/goal` 装上一个作用域，返回摘掉它的函数。
//
// 源: packages/goal/command-goal/src/index.ts:188-196（apply）
//
// 只有一步，所以没有反序回滚那一套：登记失败时什么都没装上，直接把错误交回去。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	if deps.Commands == nil {
		return nil, fmt.Errorf("goalcommand: 装 /goal 需要一张命令注册表")
	}
	undo, err := deps.Commands.Register(ctx, owner, commands.Definition{
		Name:        CommandName,
		Description: "set or view the goal for a long-running task",
		Input: &commands.InputDescriptor{
			Hint:   "[<objective>|clear|edit <objective>|pause|resume]",
			Images: true,
		},
		Handler: c.run,
	})
	if err != nil {
		return nil, fmt.Errorf("goalcommand: 装 /goal 失败：%w", err)
	}
	return undo, nil
}
