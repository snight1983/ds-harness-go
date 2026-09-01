// 本文件的作用：这个包的装配面——它要的那台目标服务、那张 agent 注册表、
// 从作用域钥匙找回 agent 的那条路，以及自动轮次里报 blocked 的那道轮数闸。
//
// 源: packages/goal/tool-goal/src/index.ts:22-34,126-132,187-193

package goaltool

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/goal/tool-goal/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-goal"

// PluginName 是这个包露面时用的名字：那条收尾指令的消息来源就报它。
//
// 源: packages/goal/tool-goal/src/index.ts:22,320
const PluginName = "tool-goal"

// DefaultBlockedAfterConsecutiveRounds 是部署方没给时那道轮数闸的位置。
//
// 源: packages/goal/tool-goal/src/index.ts:33,127
const DefaultBlockedAfterConsecutiveRounds = 3

// Agents 是本包要用的那一小块 agent 注册表能力。
//
// 新增: DSH 靠 `inject = ['agents', ...]` 拿到整个注册表。Go 里只声明用得着的
// 那两个方法（窄口子的理由同 [github.com/snight1983/ds-harness-go/goal/goal.Agents]）：一个回答「我手里
// 这个 agent 此刻还是注册表里那一个吗」，一个回答「它是不是一个顶层 agent」。
type Agents interface {
	// Get 按标识取此刻活着的那个 agent。
	Get(id session.SessionID) (agent.Agent, bool)
	// Roots 给出所有活着的顶层 agent。
	Roots() []agent.Agent
}

// Service 是这三件工具用得到的那一块目标服务。
//
// 新增: DSH 注入整个 `ctx.goals`。这里只写出真正被调到的那七个方法，交进来的
// [github.com/snight1983/ds-harness-go/goal/goal.Service] 自然满足它（窄口子的理由同
// [github.com/snight1983/ds-harness-go/subagent/controltool.Service]）。少掉的两个是
// [github.com/snight1983/ds-harness-go/goal/goal.Service.Clear] 和 Disarm：那两条路归**生命周期持有者**
// 走，不归模型——一个能自己 clear 掉目标的模型等于一个没有预算的模型。
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
	// Complete 把当前目标标记为完成。
	Complete(owner agent.Agent, ref goal.Ref) (*goal.View, error)
	// Block 把一个 active 的目标标记为撞墙。
	Block(owner agent.Agent, ref goal.Ref, reason goal.BlockReason) (*goal.View, error)
}

// Config 是这三件工具的装配面。
//
// 源: packages/goal/tool-goal/src/index.ts:26-34
type Config struct {
	// Service 是那台目标服务，必填。
	Service Service

	// Agents 是那张 agent 注册表，必填。
	//
	// 授权全靠它：一次调用够不够格，取决于调用方此刻是不是注册表里那个确切的活
	// agent，以及它是不是一个顶层 agent。
	Agents Agents

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/subagent/controltool.Config.AgentOf] 逐字相同。
	//
	// 和 [github.com/snight1983/ds-harness-go/jobs/jobstool.Config.AgentOf] 不一样的是**查不回来就是错**：
	// 目标是按 agent 记的，一个无身份的调用方没有目标可言，也就没有任何一件本包的
	// 工具对它成立。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// BlockedAfterConsecutiveRounds 是模型在一个自动轮次里自报 blocked 之前，
	// 同一个卡点至少要熬过的轮数；0 表示按
	// [DefaultBlockedAfterConsecutiveRounds]。
	//
	// 新增: DSH 是 `blockedAfterConsecutiveRounds?: number`，缺省 3。Go 用零值当
	// 「没给」在这里不丢东西：0 本身是一个非法的闸位（那等于没有闸），两边都不是
	// 一个能生效的配置。
	//
	// 它只管**自动轮次**那条路：一次直接的人类回合里报 blocked 从来不看它——人
	// 说卡住了就是卡住了，不需要熬轮数。
	BlockedAfterConsecutiveRounds int
}

// Deps 是装这三件工具那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/jobs/jobstool.Deps] 一致。
type Deps struct {
	// Tools 是工具运行时，必填。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，必填。
	Prompts *systemprompt.Registry
}

// Controller 是攥着那台服务、并且知道怎么把这三件工具装上一个作用域的那个对象。
//
// 源: packages/goal/tool-goal/src/index.ts:187-193
//
// 新增: 它**没有**互斥锁，也不该有：本包一个字节的可变状态都不持有。每一次调用
// 读的是会话日志和 agent 注册表，写的是那台目标服务——两边各自罩着自己的锁。
type Controller struct {
	service      Service
	agents       Agents
	agentOf      func(agent *scope.Key) (agent.Agent, error)
	blockedAfter int
}

// New 造一个控制器，把默认值填上并把那几条装配规矩查一遍。
//
// 源: packages/goal/tool-goal/src/index.ts:126-132
func New(config Config) (*Controller, error) {
	blockedAfter := config.BlockedAfterConsecutiveRounds
	if blockedAfter == 0 {
		blockedAfter = DefaultBlockedAfterConsecutiveRounds
	}
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("goaltool: 需要一台目标服务")
	case config.Agents == nil:
		return nil, fmt.Errorf("goaltool: 需要一张 agent 注册表")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("goaltool: 需要一条从作用域钥匙找回 agent 的路")
	case blockedAfter < 1:
		// 闸位数的是**轮次**。DSH 那边还要查 Number.isSafeInteger，因为 JS 的
		// number 装得下 Infinity 和小数——一个 Infinity 让 blocked 永远报不出来，
		// 一个小数指不到任何一轮。Go 的 int 天生是有限整数，所以只剩下这一条。
		return nil, fmt.Errorf("goaltool: BlockedAfterConsecutiveRounds 至少是 1 轮，收到 %d", blockedAfter)
	}
	return &Controller{
		service:      config.Service,
		agents:       config.Agents,
		agentOf:      config.AgentOf,
		blockedAfter: blockedAfter,
	}, nil
}
