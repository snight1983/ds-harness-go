// 本文件的作用：这个包的装配面——两个等待预算、通知怎么送达、唤醒预算，以及
// 它自己那台窄口子的作业服务和几个协作者。
//
// 源: packages/jobs/tool-jobs/src/index.ts:21-53, 205-222

package jobstool

import (
	"context"
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/jobs/tool-jobs/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-jobs"

// PluginName 是这个包在两处露面时用的名字：挂控制器时那个诊断标签，以及完成通知
// 那条消息的来源。
//
// 源: packages/jobs/tool-jobs/src/index.ts:21,260,287
const PluginName = "tool-jobs"

// CompletionDelivery 是一件作业结算之后，一个**已经闲下来**的属主怎么收到那条通知：
// wakeup 为它开一个回合，quiet 就让它挂在那儿，等别的东西把这个属主唤醒。忙着的
// 属主两种都是注入。
//
// 源: packages/jobs/tool-jobs/src/index.ts:23-28（CompletionDelivery）
type CompletionDelivery string

const (
	// DeliveryQuiet 不为一条通知开回合。
	DeliveryQuiet CompletionDelivery = "quiet"
	// DeliveryWakeup 为一条通知开回合，受唤醒预算约束。
	DeliveryWakeup CompletionDelivery = "wakeup"
)

// 这几个是配置项没写时的默认值。
//
// 源: packages/jobs/tool-jobs/src/index.ts:48-53
const (
	// DefaultWaitTimeout 是 job_output 只写了 wait、没写 timeout_ms 时那段等待。
	DefaultWaitTimeout = 30 * time.Second
	// DefaultMaxWaitTimeout 是任何一次等待的硬上限。
	DefaultMaxWaitTimeout = 10 * time.Minute
	// DefaultMaxConsecutiveWakes 是一个属主连着被唤醒几次之后通知降级成注入。
	DefaultMaxConsecutiveWakes = 3
)

// Service 是这三件工具用得到的那一块作业注册表。
//
// 新增: DSH 注入整个 `ctx.jobs`。这里只写出真正被调到的那七个方法，交进来的
// [github.com/snight1983/ds-harness-go/feature/jobs.Registry] 自然满足它（窄口子的理由同
// [github.com/snight1983/ds-harness-go/feature/subagent/controltool.Service]）。少掉的两个是
// [github.com/snight1983/ds-harness-go/feature/jobs.Registry.Start] 和 OnJobsChanged，这不是省字：
// **本包不是生产方**，它一件作业都起不了；那条「变了」的流也归别人。
type Service interface {
	// List 列出调用方看得见的那些作业。
	List(ctx context.Context, caller agent.Agent) ([]jobs.Snapshot, error)
	// Get 取一份不消费的快照。
	Get(ctx context.Context, id jobs.JobID, caller agent.Agent) (jobs.Snapshot, error)
	// Read 取下一段增量，或者结算之后那份幂等的最终输出。
	Read(ctx context.Context, id jobs.JobID, caller agent.Agent) (jobs.Read, error)
	// Kill 请求取消。
	Kill(ctx context.Context, id jobs.JobID, caller agent.Agent, reason string) (jobs.KillResult, error)
	// Wait 等到结算或者超时。
	Wait(ctx context.Context, id jobs.JobID, timeout time.Duration, caller agent.Agent) (jobs.Snapshot, error)
	// OnJobDone 登记一个按作用域圈定的完成监听器。
	OnJobDone(
		ctx context.Context,
		owner *scope.Scope,
		listener jobs.DoneListener,
	) (func(context.Context) error, error)
	// AttachController 挂上那个「生产方开工必需」的控制器。
	AttachController(
		ctx context.Context,
		owner *scope.Scope,
		name string,
	) (func(context.Context) error, error)
}

// Agents 是唤醒预算的那条补给线：它只需要知道「一条用户自己写的输入被认领走了」。
//
// 新增: DSH 挂在 cordis 事件总线的 `agent/inbox/claimed` 上。Go 里那条事件是
// [github.com/snight1983/ds-harness-go/harness/agent.Registry.OnInboxClaimed]，做法参照
// [github.com/snight1983/ds-harness-go/feature/subagent] 里那处订阅。
//
// 只有 [DeliveryWakeup] 用得上它：quiet 之下没有东西花掉预算，也就没有东西需要
// 把它补回来。
type Agents interface {
	// OnInboxClaimed 登记一个「消息在它那个已开回合里被认领走」的观察者。
	OnInboxClaimed(
		ctx context.Context,
		owner *scope.Scope,
		observer agent.InboxClaimedObserver,
	) (func(context.Context) error, error)
}

// Config 是这三件工具的装配面。
//
// 源: packages/jobs/tool-jobs/src/index.ts:31-53
type Config struct {
	// Service 是那台作业注册表，必填。
	Service Service

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/feature/subagent/controltool.Config.AgentOf] 逐字相同。
	//
	// 和那边不一样的是**查不回来不是错**：这三件工具对一个无身份的调用方照样
	// 成立，它看得见的就是那些无主作业——那是最紧的一档可见范围，不是最松的。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// WaitTimeout 是 job_output 只写了 wait、没写 timeout_ms 时那段等待，
	// 零值取 [DefaultWaitTimeout]。
	//
	// 新增: DSH 是 `waitTimeoutMs?: number`。Go 里时长就是 [time.Duration]。
	WaitTimeout time.Duration

	// MaxWaitTimeout 是任何一次等待的硬上限：模型给的 timeout_ms 再大也夹到它，
	// 零值取 [DefaultMaxWaitTimeout]。
	MaxWaitTimeout time.Duration

	// CompletionDelivery 决定一条通知会不会为一个闲着的属主开一个回合，
	// 空串取 [DeliveryWakeup]。
	CompletionDelivery CompletionDelivery

	// MaxConsecutiveWakes 是一个属主连着被唤醒几个回合之后，下一条通知降级成
	// 注入；任何一条用户自己写的输入被认领时清零。零值取
	// [DefaultMaxConsecutiveWakes]。
	//
	// 它收住的是那条自激链：一个被唤醒的回合起了一件作业，那件作业结算时又把
	// 它唤醒。
	MaxConsecutiveWakes int
}

// Deps 是装这三件工具那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.Deps] 一致。
type Deps struct {
	// Tools 是工具运行时，必填。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，必填。
	Prompts *systemprompt.Registry
	// Agents 是那条唤醒预算的补给线，只在 [DeliveryWakeup] 之下必填。
	Agents Agents
}

// New 造一个控制器，把默认值填上并把那几条装配规矩查一遍。
//
// 源: packages/jobs/tool-jobs/src/index.ts:205-222
func New(config Config) (*Controller, error) {
	waitDefault := config.WaitTimeout
	if waitDefault == 0 {
		waitDefault = DefaultWaitTimeout
	}
	waitCap := config.MaxWaitTimeout
	if waitCap == 0 {
		waitCap = DefaultMaxWaitTimeout
	}
	delivery := config.CompletionDelivery
	if delivery == "" {
		delivery = DeliveryWakeup
	}
	budget := config.MaxConsecutiveWakes
	if budget == 0 {
		budget = DefaultMaxConsecutiveWakes
	}
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("jobstool: 需要一台作业注册表")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("jobstool: 需要一条从作用域钥匙找回 agent 的路")
	case delivery != DeliveryQuiet && delivery != DeliveryWakeup:
		return nil, fmt.Errorf("jobstool: 认不得的完成投递方式 %q", delivery)
	case waitDefault < time.Millisecond:
		return nil, fmt.Errorf("jobstool: WaitTimeout 至少是 1 毫秒，收到 %v", waitDefault)
	case waitCap < time.Millisecond:
		return nil, fmt.Errorf("jobstool: MaxWaitTimeout 至少是 1 毫秒，收到 %v", waitCap)
	case waitDefault > waitCap:
		// 源: packages/jobs/tool-jobs/src/index.ts:215-217
		return nil, fmt.Errorf("jobstool: WaitTimeout（%v）超过了 MaxWaitTimeout（%v）", waitDefault, waitCap)
	case budget < 1:
		// 预算数的是**回合**。DSH 那边还要查 Number.isSafeInteger，因为 JS 的
		// number 装得下 Infinity 和小数——一个 Infinity 让这个字段存在的意义
		// （收住那条自激链）当场作废，一个小数根本指不到任何一个回合。Go 的 int
		// 天生是有限整数，所以只剩下这一条。
		return nil, fmt.Errorf("jobstool: MaxConsecutiveWakes 至少是 1 个回合，收到 %d", budget)
	}
	return &Controller{
		service:      config.Service,
		agentOf:      config.AgentOf,
		waitDefault:  waitDefault,
		waitCap:      waitCap,
		delivery:     delivery,
		wakeBudget:   budget,
		spentWakes:   make(map[agent.Agent]int),
		wakeCleanups: make(map[agent.Agent]func(context.Context) error),
		outputLimits: make(map[tools.ExecutionToken]int),
	}, nil
}
