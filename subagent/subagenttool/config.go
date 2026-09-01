// 本文件的作用：这个包的装配面——点哪个提供方、那件工具叫什么、后台那条路开不开
// 以及走哪一种，加上每个孩子都带上的那几样默认值。
//
// 源: packages/subagent/tool-subagent/src/index.ts:22-99, 276-286

package subagenttool

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/subagent/tool-subagent/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-subagent"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/subagent/tool-subagent/src/index.ts:42（name）
const PluginName = "tool-subagent"

// DefaultToolName 是没写 [Config.ToolName] 时那件工具的名字。
//
// 源: packages/subagent/tool-subagent/src/index.ts:83
const DefaultToolName = "subagent"

// DefaultMaxDepth 是没写深度上限时的那个递归预算。
//
// 源: packages/subagent/tool-subagent/src/index.ts:98
const DefaultMaxDepth = 3

// SectionOrder 是那段后台指引在提示词里的位次：排在「有节制地派发」（116）之后、
// 「孩子怎么汇报」之前。
//
// 源: packages/subagent/tool-subagent/src/index.ts:26
//
// 新增: DSH 那个值是 **116.5**，因为它要挤在 tool-ralph 的 116 和
// tool-subagent-report 的 117 中间。Go 的
// [github.com/snight1983/ds-harness-go/core/systemprompt.PromptSection.Order] 是 int，116 和 117 之间
// 没有空位，所以这里取 117，并把
// [github.com/snight1983/ds-harness-go/subagent/reporttool.SectionOrder] 顺推到 118。次序这件事只有
// **相对关系**是有意义的，而这一步把每一对的先后都原样保住了。
const SectionOrder = 117

// BackgroundMode 是后台那条路的两种走法。
//
// 源: packages/subagent/tool-subagent/src/index.ts:48
type BackgroundMode string

const (
	// ModeOneShot 让调用默认走前台；它的后台结果要靠 job_output 去收。
	ModeOneShot BackgroundMode = "one-shot"
	// ModeContinuable 让调用默认走后台，当场交回那个耐久的孩子 id，
	// 并且把孩子那段对话留着供后面的回合接着用。
	//
	// 它要提供方实现 [github.com/snight1983/ds-harness-go/subagent/subagent.ContinuablePreparer]，
	// 装不上就大声失败。
	ModeContinuable BackgroundMode = "continuable"
)

// Subagents 是这件工具用得到的那一块子 agent 接缝。
//
// 新增: DSH 注入整个 `ctx.subagents`。这里只写出真正被调到的那五个方法，交进来的
// [github.com/snight1983/ds-harness-go/subagent/subagent.Runtime] 自然满足它（窄口子的理由同
// [github.com/snight1983/ds-harness-go/subagent/controltool.Service]）。
type Subagents interface {
	// GetProvider 找一个登记着的提供方。
	GetProvider(name string) (subagent.Provider, bool)
	// Start 立起一个一次性孩子。
	Start(ctx context.Context, name string, request subagent.StartRequest) (subagent.Run, error)
	// StartContinuable 立起一个耐久的可续孩子并投出它的初始提示词。
	StartContinuable(ctx context.Context, spec subagent.ContinuableStartSpec) (subagent.ContinuableStart, error)
	// OnProviderAdded 登记一个「提供方来了」的观察者。
	OnProviderAdded(
		ctx context.Context,
		owner *scope.Scope,
		observer subagent.ProviderAddedObserver,
	) (func(context.Context) error, error)
	// OnProviderRemoved 登记一个「提供方走了」的观察者。
	OnProviderRemoved(
		ctx context.Context,
		owner *scope.Scope,
		observer subagent.ProviderRemovedObserver,
	) (func(context.Context) error, error)
}

// Jobs 是一次性后台那条路要的那一点作业注册表。
//
// 新增: DSH 是 `ctx.get('jobs')`——一次可选取用，没装就在派发时报错。Go 里它是
// [Deps.Jobs]，为 nil 时同一条路报同一句话。只写出 Start 一个方法，因为这个包
// **只生产不消费**：收结果那件事归 job_output，不归它。
type Jobs interface {
	// Start 登记一件作业并交回它的 id。
	Start(spec jobs.Start) (jobs.JobID, error)
}

// Config 是这件工具的装配面。
//
// 源: packages/subagent/tool-subagent/src/index.ts:29-79
type Config struct {
	// Provider 是这件工具派活给的那个提供方名字（spawn、fork、acp……），必填。
	//
	// 它是**点名**的：这个包一个装配只服务一个提供方。
	Provider string

	// ToolName 是模型看到的工具名，空串取 [DefaultToolName]。
	// 同一个工具运行时里装两份就得给两个不同的名字。
	ToolName string

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/subagent/controltool.Config.AgentOf] 逐字相同。
	//
	// 这里**查不回来就是错**（和 [github.com/snight1983/ds-harness-go/jobs/jobstool.Config.AgentOf]
	// 相反）：那个父 agent 不是用来定可见范围的，它是这个孩子的属主——
	// 派发深度、血统、工作目录全从它推出来。DSH 那句
	// `subagent tool requires a calling agent` 是同一条判断。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// Logger 用来报「点名的提供方还没登记」这一条，为 nil 时用 [slog.Default]。
	//
	// 新增: DSH 用 ctx.logger。Go 里没有那个隐式容器，做法同
	// [github.com/snight1983/ds-harness-go/core/tools.Options.Logger]。
	Logger *slog.Logger

	// DisableRunInBackground 为真表示这个装配根本不露出 run_in_background：
	// 那个参数从 schema 里消失，而且一次硬写 true 的调用会被拒。
	//
	// 新增: DSH 是 `enableRunInBackground?: boolean`，默认 true。Go 的 bool 零值
	// 是 false，照抄会把默认值反过来，所以这里把它取反命名——这样零值就是 DSH 的
	// 默认行为（露出后台），成例见
	// [github.com/snight1983/ds-harness-go/invariants.Config.Enabled] 那条说明里同一个问题的另一种解法。
	DisableRunInBackground bool

	// BackgroundMode 是后台那条路怎么走，空串取 [ModeOneShot]。
	BackgroundMode BackgroundMode

	// AgentOptions 是给每个孩子的 agent 选项，零值表示不指定、随孩子那条循环的默认值。
	AgentOptions agent.Options

	// Persona 是只给这些孩子的人设，盖掉部署自己那份；空串表示不换。
	// 要提供方的 Persona 能力。
	Persona string

	// ToolFilter 是给每个孩子的工具范围。被滤掉的工具从孩子的提示词里消失**并且**
	// 拒绝执行。要提供方的 ToolFilter 能力；认不出的名字在开工时大声失败。
	//
	// 零值表示不过滤。Allow 和 Deny 都为空、但调用方**确实想过滤**这件事在 Go 里
	// 表达不出来，所以 DSH 那条「配了 toolFilter 却一个名字都没写就拒装」的检查
	// 在这里没有产出方：一个零值就是「不过滤」，不是一个写坏了的过滤器。
	ToolFilter tools.Restriction

	// MaxDepth 是给每个孩子的派发深度绝对上限，nil 取 [DefaultMaxDepth]。
	// 0 是有意义的取值：它禁止一切再派发。要提供方的 DepthLimit 能力，
	// 这一条在**装的时候**就查，不是等到第一次派发。
	//
	// 新增: DSH 是 `maxDepth?: number | 'provider-managed'`，而且「经 loader 装」
	// 和「直接调 apply」两条路上的省略含义不同（前者取 3，后者不设上限）。
	// Go 里 New 就是那个装配点，只能有一种含义，这里选 loader 那一种：**省略取 3**。
	// 理由是另一种含义等于「忘了写这个字段就没有递归预算」，那是一次静默降级。
	MaxDepth *int

	// ProviderManagedDepth 为真表示一个字的上限都不发，递归预算归提供方或者孩子
	// 那个运行时。它对应 DSH 的 `'provider-managed'`，是给一个进程外提供方留的。
	//
	// 它和 [Config.MaxDepth] 只许填一个，两个都填就拒装——那说明调用方对这个孩子
	// 的递归预算归谁管有两种互相矛盾的想法，猜哪一种都是错的。
	ProviderManagedDepth bool
}

// Deps 是装这件工具那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/jobs/jobstool.Deps] 一致。
type Deps struct {
	// Tools 是工具运行时，必填。
	Tools *tools.Runtime
	// Subagents 是那条子 agent 接缝，必填。
	Subagents Subagents
	// Prompts 是系统提示词注册表，必填。
	Prompts *systemprompt.Registry
	// Jobs 是作业注册表，只有一次性后台那条路用得上；为 nil 时那条路报错，
	// 前台和可续两条路照跑。
	Jobs Jobs
}

// New 造一个控制器，把默认值填上并把那几条装配规矩查一遍。
//
// 源: packages/subagent/tool-subagent/src/index.ts:276-286
func New(config Config) (*Controller, error) {
	toolName := config.ToolName
	if toolName == "" {
		toolName = DefaultToolName
	}
	mode := config.BackgroundMode
	if mode == "" {
		mode = ModeOneShot
	}
	maxDepth := config.MaxDepth
	if maxDepth == nil && !config.ProviderManagedDepth {
		depth := DefaultMaxDepth
		maxDepth = &depth
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	switch {
	case config.Provider == "":
		return nil, fmt.Errorf("subagenttool: 需要点名一个提供方")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("subagenttool: 需要一条从作用域钥匙找回 agent 的路")
	case mode != ModeOneShot && mode != ModeContinuable:
		return nil, fmt.Errorf("subagenttool: 认不得的后台走法 %q", mode)
	case config.MaxDepth != nil && config.ProviderManagedDepth:
		return nil, fmt.Errorf("subagenttool: MaxDepth 和 ProviderManagedDepth 只许填一个")
	}
	// 源: packages/subagent/tool-subagent/src/index.ts:279
	//
	// 一个负的上限表达不了任何一个确切的派发深度，所以在这里就拒掉，
	// 而不是每一次派发都被接缝拒一遍。
	if err := subagent.AssertMaxDepth(maxDepth); err != nil {
		return nil, fmt.Errorf("subagenttool: %w", err)
	}
	return &Controller{
		provider:          config.Provider,
		toolName:          toolName,
		agentOf:           config.AgentOf,
		logger:            logger,
		backgroundEnabled: !config.DisableRunInBackground,
		continuable:       mode == ModeContinuable,
		agentOptions:      config.AgentOptions,
		persona:           config.Persona,
		toolFilter:        config.ToolFilter,
		maxDepth:          maxDepth,
	}, nil
}
