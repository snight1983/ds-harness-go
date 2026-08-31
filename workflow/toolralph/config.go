// 本文件的作用：这个包的装配面——派给哪个提供方、一次调用最多推几轮、一份交接和
// 一份终局文本各自的字数上限，以及把这些默认值填上、把装配规矩查一遍的那一步。
//
// 源: packages/workflow/tool-ralph/src/index.ts:19-47, 186-205

package toolralph

import (
	"context"
	"fmt"
	"strings"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/subagent/subagent"
)

// PluginName 是这个包露面时用的名字。
//
// 源: packages/workflow/tool-ralph/src/index.ts:19
const PluginName = "tool-ralph"

// PackageName 是这个包在不变量注册表里占的名字，和 DSH 的包名保持一致。
//
// 源: packages/workflow/tool-ralph/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-ralph"

// ToolName 是模型看到的工具名。
//
// 源: packages/workflow/tool-ralph/src/index.ts:413
const ToolName = "ralph"

// SectionName 是那段用法指引的段名。
//
// 源: packages/workflow/tool-ralph/src/index.ts:408
const SectionName = "tool:ralph"

// SectionOrder 是那段用法指引在提示词里的位次。
//
// 源: packages/workflow/tool-ralph/src/index.ts:409
const SectionOrder = 116

// DefaultSubagentProvider 是没写 [Config.SubagentProvider] 时点名的那个提供方。
//
// 源: packages/workflow/tool-ralph/src/index.ts:36
const DefaultSubagentProvider = "spawn"

// DefaultMaxRounds 是没写 [Config.MaxRounds] 时的轮次天花板。
//
// 源: packages/workflow/tool-ralph/src/index.ts:37
const DefaultMaxRounds = 256

// DefaultMaxHandoffChars 是没写 [Config.MaxHandoffChars] 时，一份结构化交接的字数上限。
//
// 源: packages/workflow/tool-ralph/src/index.ts:38
const DefaultMaxHandoffChars = 16_384

// DefaultMaxResultChars 是没写 [Config.MaxResultChars] 时，那份交回父手上的终局
// 文本的字数上限。
//
// 源: packages/workflow/tool-ralph/src/index.ts:39
const DefaultMaxResultChars = 16_384

// Subagents 是这件工具用得到的那一块子 agent 接缝。
//
// 新增: DSH 注入整个 `ctx.subagents`。这里只写出真正被调到的那两个方法，交进来的
// [ds-harness-go/subagent/subagent.Runtime] 自然满足它（窄口子的理由同
// [ds-harness-go/subagent/subagenttool.Subagents]）。
//
// 本包**只起一次性孩子**：Ralph 那条循环的立身之本就是每一轮都从零开始，
// 一个可续的孩子会把上一轮的会话带过来，那正是它要躲开的东西。
type Subagents interface {
	// GetProvider 找一个登记着的提供方。
	GetProvider(name string) (subagent.Provider, bool)
	// Start 立起一个一次性孩子。
	Start(ctx context.Context, name string, request subagent.StartRequest) (subagent.Run, error)
}

// Config 是这件工具的装配面。
//
// 源: packages/workflow/tool-ralph/src/index.ts:23-40
//
// 新增: DSH 那四个数值字段是 `number` 加一份 schemastery 声明（`.step(1).min(1)
// .max(MAX_SAFE_INTEGER).default(…)`），装配路径和直接调 apply 那条路径都靠
// [resolveConfig] 再验一遍。Go 里它们是 int：`step(1)` 由类型担着，`max` 由类型
// 担着（int 本身就是安全整数域），只剩「没写」和「不许为负」两件事要办——前者
// 用零值表示、由 [New] 填上默认值，后者由 [New] 当场拒掉。
type Config struct {
	// SubagentProvider 是每一轮开孩子时点名的提供方，空串取 [DefaultSubagentProvider]。
	//
	// 它必须是一个**真的全新**的提供方：装配时会查它支不支持结构化输出、以及它
	// 会不会把父的上下文带给孩子（见 [Controller.requireFreshProvider]）。
	SubagentProvider string

	// MaxRounds 是这个装配允许的轮次天花板，也是模型没点名时的默认值；
	// 0 取 [DefaultMaxRounds]。模型可以在这个数以内挑一个更小的，挑不出更大的。
	MaxRounds int

	// MaxHandoffChars 是一份结构化轮次报告排成 JSON 之后的字数上限，
	// 0 取 [DefaultMaxHandoffChars]。超了那一轮就算写坏了，整次调用失败。
	//
	// 这道闸是 Ralph 的另一半：不设上限的话，一个孩子可以把整段对话塞进 summary，
	// 「每轮换一个干净的人」就退化成了「换个地方接着堆上下文」。
	MaxHandoffChars int

	// MaxResultChars 是交回父手上那份终局文本的字数上限，0 取 [DefaultMaxResultChars]。
	// 超了截断并接一句省略标记，见 [boundResult]。
	MaxResultChars int

	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [ds-harness-go/subagent/subagenttool.Config.AgentOf] 逐字相同。
	//
	// 这里**查不回来就是错**：那个父 agent 是每一轮那个孩子的属主——派发深度、
	// 血统、工作目录全从它推出来，而工作区正是 Ralph 唯一的长期记忆。DSH 那句
	// `Ralph tool requires a calling agent` 是同一条判断。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
}

// Deps 是装这件工具那一刻要交进来的协作者。
//
// 新增: DSH 从 cordis 上下文上按 inject 取（tools、workflowEngine、subagents、
// systemPrompt）。Go 没有那个容器，所以显式交进来；四样里 workflowEngine 那一样
// 不在，理由见本包的包文档。
type Deps struct {
	// Tools 是工具运行时，必填。
	Tools *tools.Runtime
	// Subagents 是那条子 agent 接缝，必填。
	Subagents Subagents
	// Prompts 是系统提示词注册表，必填。
	Prompts *systemprompt.Registry
}

// Controller 是攥着这一次装配的默认值和上限、并且知道怎么把那条循环推起来的对象。
//
// 源: packages/workflow/tool-ralph/src/index.ts:42-47, 405-479
//
// 它造出来之后就**不再变**，所以不带锁也不带那份 Deps：那条子 agent 接缝由
// [Controller.Install] 交给它装出来的那件工具（见 [Controller.newTool] 里捕获的
// 闭包），一路传到 [Controller.runLoop]。这跟
// [ds-harness-go/subagent/subagenttool.Controller] 不同——那台要在提供方来来去去
// 时装装摘摘，所以必须攥着 Deps；本包一次都不需要。
type Controller struct {
	provider        string
	maxRounds       int
	maxHandoffChars int
	maxResultChars  int
	agentOf         func(agent *scope.Key) (agent.Agent, error)
}

// New 造一个控制器，把默认值填上并把那几条装配规矩查一遍。
//
// 源: packages/workflow/tool-ralph/src/index.ts:186-205
//
// 那几句话是给运维看的，但字段名照抄 DSH，因为它们就是照着配置字段写的。
func New(config Config) (*Controller, error) {
	provider := config.SubagentProvider
	if provider == "" {
		provider = DefaultSubagentProvider
	}
	maxRounds := config.MaxRounds
	if maxRounds == 0 {
		maxRounds = DefaultMaxRounds
	}
	maxHandoffChars := config.MaxHandoffChars
	if maxHandoffChars == 0 {
		maxHandoffChars = DefaultMaxHandoffChars
	}
	maxResultChars := config.MaxResultChars
	if maxResultChars == 0 {
		maxResultChars = DefaultMaxResultChars
	}
	switch {
	case config.AgentOf == nil:
		return nil, fmt.Errorf("toolralph: 需要一条从作用域钥匙找回 agent 的路")
	case provider != strings.TrimSpace(provider):
		return nil, fmt.Errorf("toolralph: SubagentProvider 前后不许带空白，拿到 %q", provider)
	case maxRounds < 1:
		return nil, fmt.Errorf("toolralph: MaxRounds 必须是正整数，拿到 %d", maxRounds)
	case maxHandoffChars < 1:
		return nil, fmt.Errorf("toolralph: MaxHandoffChars 必须是正整数，拿到 %d", maxHandoffChars)
	case maxResultChars < 1:
		return nil, fmt.Errorf("toolralph: MaxResultChars 必须是正整数，拿到 %d", maxResultChars)
	}
	return &Controller{
		provider:        provider,
		maxRounds:       maxRounds,
		maxHandoffChars: maxHandoffChars,
		maxResultChars:  maxResultChars,
		agentOf:         config.AgentOf,
	}, nil
}
