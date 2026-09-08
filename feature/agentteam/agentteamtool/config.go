// 本文件的作用：这个包的装配面——它要的那台团队服务、这套工具服务的是哪支团队、
// 从作用域钥匙找回 agent 的那条路，以及起队友时用哪个子 Agent 提供方。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:11-28,398-418

package agentteamtool

import (
	"context"
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/experimental/tool-agent-team/src/invariant.ts
const PackageName = "@deepseek-ai/dsh-experimental-tool-agent-team"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:12
const PluginName = "tool-agent-team"

// 这两个是部署方没给时，起队友用的那两个子 Agent 提供方。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:26-27
const (
	// DefaultFreshProvider 是全新开一个会话时用的提供方。
	DefaultFreshProvider = "spawn"
	// DefaultForkProvider 是从队长上文分一支时用的提供方。
	DefaultForkProvider = "fork"
)

// Service 是这十件工具用得到的那一块团队服务。
//
// 新增: DSH 靠 `inject = ['agentTeams', ...]` 拿到整台服务。Go 里只声明真正被调到的
// 那十一个方法（窄口子的理由同
// [github.com/snight1983/ds-harness-go/feature/subagent/controltool.Service]），
// 装配方交进来的 [github.com/snight1983/ds-harness-go/feature/agentteam.Service]
// 天然满足它。
//
// 少掉的是 Deliver 和 Close 那两条：前者归装配方在一个队友活起来时叫，后者归
// 生命周期持有者——一个能把整支团队的域关掉的模型，等于一个能让所有队友一起失忆的模型。
type Service interface {
	// ResolveCaller 把一个会话身份解算成这次操作的发起人。
	ResolveCaller(ctx context.Context, team agentteam.TeamID, id sessionlog.SessionID) (agentteam.Caller, error)
	// Spawn 起一个队友。
	Spawn(ctx context.Context, lead sessionlog.SessionID, request agentteam.SpawnRequest) (agentteam.MemberView, error)
	// Members 列出这支团队。
	Members(ctx context.Context, team agentteam.TeamID) ([]agentteam.MemberView, error)
	// Interrupt 打断一个队友此刻那段活动。
	Interrupt(ctx context.Context, who agentteam.Caller, targetName string) (agentteam.MemberStatus, error)
	// Send 把一条消息排进收件人的收件箱。
	Send(ctx context.Context, who agentteam.Caller, request agentteam.SendRequest) (agentteam.SendResult, error)
	// Wait 等这支团队下一次有动静。
	Wait(ctx context.Context, who agentteam.Caller, timeout time.Duration) (agentteam.WaitResult, error)
	// ActivePeers 数除了发起人之外还有几个成员在跑或者在开工。
	ActivePeers(ctx context.Context, who agentteam.Caller) (int, error)
	// CreateTask 在板上新建一条任务。
	CreateTask(ctx context.Context, who agentteam.Caller, request agentteam.CreateTaskRequest) (agentteam.TaskView, error)
	// Tasks 列出板上没删掉的那些任务。
	Tasks(ctx context.Context, team agentteam.TeamID) ([]agentteam.TaskView, error)
	// Task 读一条任务此刻的整份值。
	Task(ctx context.Context, team agentteam.TeamID, id agentteam.TaskID) (agentteam.TaskView, error)
	// UpdateTask 在板上改一条任务。
	UpdateTask(ctx context.Context, who agentteam.Caller, request agentteam.UpdateTaskRequest) (agentteam.TaskView, error)
}

// Config 是这十件工具的装配面。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:17-28
type Config struct {
	// Service 是那台团队服务，必填。
	Service Service

	// Team 是这套工具服务的那支团队，必填。
	//
	// 新增: DSH 在装的时候现问 `ctx.agentTeams.membership(agent)`——它有一张进程内的
	// 成员关系表，一个 agent 属于哪支团队随时问得到。本包的成员关系在介质上，
	// 而 [Controller.Install] 是同步的，所以团队身份改由装配方在这里写死。
	//
	// 一个控制器因此只服务一支团队。装到不属于这支团队的作用域上不会当场报错，
	// 而是每一次调用都在解算发起人那一步拿到
	// [github.com/snight1983/ds-harness-go/feature/agentteam.CodeNotMember]——
	// 这正是要的：装配是一次性的，成员关系却随时在变。
	Team agentteam.TeamID

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Config.AgentOf] 逐字相同。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// FreshProvider 是 context 为 fresh 时起队友用的提供方，留空用
	// [DefaultFreshProvider]。
	FreshProvider string
	// ForkProvider 是 context 为 fork 时起队友用的提供方，留空用 [DefaultForkProvider]。
	ForkProvider string
}

// Deps 是装这十件工具那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/feature/goal/goaltool.Deps] 一致。
type Deps struct {
	// Tools 是工具运行时，必填。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，那段团队策略指引登记在它上面，必填。
	Prompts *systemprompt.Registry
}

// Controller 是攥着那台服务、并且知道怎么把这十件工具装上一个作用域的那个对象。
//
// 新增: 它**没有**互斥锁，也不该有：本包一个字节的可变状态都不持有，
// 每一次调用读写的都是那台团队服务，而团队状态罩在介质的条件写底下。
type Controller struct {
	service       Service
	team          agentteam.TeamID
	agentOf       func(agent *scope.Key) (agent.Agent, error)
	freshProvider string
	forkProvider  string
}

// New 造一个控制器，把默认值填上并把那几条装配规矩查一遍。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:398-402
func New(config Config) (*Controller, error) {
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("agentteamtool: 需要一台团队服务")
	case config.Team == "":
		return nil, fmt.Errorf("agentteamtool: 需要说明这套工具服务的是哪支团队")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("agentteamtool: 需要一条从作用域钥匙找回 agent 的路")
	}
	controller := &Controller{
		service:       config.Service,
		team:          config.Team,
		agentOf:       config.AgentOf,
		freshProvider: config.FreshProvider,
		forkProvider:  config.ForkProvider,
	}
	if controller.freshProvider == "" {
		controller.freshProvider = DefaultFreshProvider
	}
	if controller.forkProvider == "" {
		controller.forkProvider = DefaultForkProvider
	}
	return controller, nil
}

// caller 把这次执行落在的那把钥匙解算成这支团队里的发起人。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:151-156（callingAgent）
//
// 新增: DSH 那一步只是把 `exec.agent` 从 undefined 里取出来，成员关系另有一张表。
// Go 这边两步并成一步：先从钥匙查回 agent，再拿它的会话身份去问团队服务。
// 「查不回来」和「压根没落在 agent 上」对模型是同一件事——本包每一件工具凭的都是
// 那个确切的活调用方，找不到它就没有权可凭。
func (c *Controller) caller(ctx context.Context, exec *tools.RunContext, toolName string) (agentteam.Caller, error) {
	if exec == nil || exec.Agent == nil {
		return agentteam.Caller{}, fmt.Errorf("%s requires a calling Agent", toolName)
	}
	live, err := c.agentOf(exec.Agent)
	if err != nil {
		return agentteam.Caller{}, err
	}
	if live == nil {
		return agentteam.Caller{}, fmt.Errorf("%s requires a calling Agent", toolName)
	}
	return c.service.ResolveCaller(ctx, c.team, live.ID())
}
