// 本文件的作用：这个包朝外的那一面——模型看到的那件工具（描述、入参、输出契约、
// 渲染）、执行那一步在真开循环之前把的几道关，以及把它和那段用法指引一起装上一个
// 作用域的 Install。
//
// 源: packages/workflow/tool-ralph/src/index.ts:179-232, 379-383, 394-479

package toolralph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// toolDescription 是模型在工具表里看到的那段话。
//
// 源: packages/workflow/tool-ralph/src/index.ts:179-184
//
// 逐字照抄。它一半的篇幅在划边界——「只有直接的人明说要 Ralph 或者要换人迭代
// 的时候才用」「普通的长活儿归 goal 那几件工具」。这件工具很贵（一轮一个孩子），
// 而模型看见「反复推进直到完成」是会主动去够的，所以那两句得写在描述里，光靠
// 系统提示词那一段不够。
const toolDescription = "Run a foreground fresh-agent Ralph loop toward one immutable objective. " +
	"Use only when the direct human explicitly asks for Ralph or fresh-agent iteration. Each round " +
	"opens a new child with no parent conversation or prior child session; the shared workspace is " +
	"long-term memory, and only a bounded structured report crosses rounds. The call returns when " +
	"a worker reports completion or a concrete blocker, or at the round limit. Ordinary long-running same-session work " +
	"belongs to goal tools."

// sectionText 是那段用法指引的正文。
//
// 源: packages/workflow/tool-ralph/src/index.ts:410
//
// 逐字照抄。中间那句「Completion and blockers are worker reports, not independent
// evaluation」是本包那条「不替孩子作证」的立场在提示词里的落点。
const sectionText = "Use the ralph tool ONLY when the direct human explicitly asks for a Ralph loop " +
	"or fresh-agent iterative execution. Each Ralph round starts a fresh child with no conversation " +
	"seed and uses the shared workspace as durable memory. Completion and blockers are worker reports, " +
	"not independent evaluation. Use same-session goal tools for ordinary long-running objectives, " +
	"and plain subagents or workflows for bounded delegation and fan-out."

// 这两句是那两个入参在 schema 里的说明。
//
// 源: packages/workflow/tool-ralph/src/index.ts:419, 423
const (
	objectiveDescription = "The immutable completion objective for every fresh Ralph round."
	maxRoundsDescription = "Optional positive safe-integer round cap, bounded by the deployment ceiling."
)

// callArgs 是模型交上来的那两个入参。
//
// 源: packages/workflow/tool-ralph/src/index.ts:75-78
//
// MaxRounds 是指针：「没写」和「写了 0」完全是两件事——前者取部署的天花板，
// 后者是一次要当场拒掉的错。
type callArgs struct {
	Objective string `json:"objective"`
	MaxRounds *int   `json:"maxRounds"`
}

// outputValue 是这件工具那份权威结果值。
//
// 源: packages/workflow/tool-ralph/src/index.ts:379-383
//
// 新增: DSH 那份里还有一个 runId，指的是那次 workflow 运行。本包没有那个引擎，
// 也就没有那么一次运行可以指——每一轮的孩子各有各的运行 id，硬挑一个当代表是编的。
// 所以这个字段整个去掉，而不是填一个空串。
type outputValue struct {
	// AgentsStarted 是这次调用一共起了几个孩子，等于开了几轮。
	AgentsStarted int `json:"agentsStarted"`
	// Result 是那份终局值。
	Result runResult `json:"result"`
}

// resolveMaxRounds 把模型挑的那个上限对着部署的天花板解算一遍。
//
// 源: packages/workflow/tool-ralph/src/index.ts:208-217
//
// 没写就是天花板本身。挑得比天花板大是错，不是悄悄夹到天花板：模型是按它以为的
// 轮数在规划这件事的，夹了它不知道，会拿着一份跑不完的计划去写目标。那两句话是
// 给模型看的，所以是英文。
func resolveMaxRounds(requested *int, ceiling int) (int, error) {
	if requested == nil {
		return ceiling, nil
	}
	value := *requested
	if value < 1 {
		return 0, errors.New("Ralph maxRounds must be a positive safe integer")
	}
	if value > ceiling {
		return 0, fmt.Errorf("Ralph maxRounds %d exceeds the deployment ceiling %d", value, ceiling)
	}
	return value, nil
}

// requireFreshProvider 查点名的那条路线是不是真的意味着一个全新的、结构化的孩子。
//
// 源: packages/workflow/tool-ralph/src/index.ts:220-232
//
// 三条缺一不可，而且缺哪一条都得当场大声说，绝不能降级往下跑：
//
//   - 没登记：那就压根开不出孩子来。
//   - 不支持结构化输出：那份轮次报告是轮与轮之间**唯一**的载荷，没有它这条循环
//     就退化成「反复叫一个人从头干同一件事」。
//   - 会把父的上下文带给孩子：那就正好把 Ralph 存在的理由抵消掉了——它整件事就是
//     为了让每一轮那个人看不见前面那堆脏东西。
//
// 这道关落在**执行**那一刻而不是装配那一刻（DSH 也是），因为提供方可以在装配之后
// 才登记上来。那几句话是给模型看的，所以是英文。
func requireFreshProvider(subagents Subagents, name string) error {
	provider, present := subagents.GetProvider(name)
	if !present {
		return fmt.Errorf("Ralph subagent provider %q is not registered", name)
	}
	if !provider.Capabilities().OutputSchema {
		return fmt.Errorf("Ralph subagent provider %q does not support structured output", name)
	}
	if provider.InheritsParentContext() {
		return fmt.Errorf(
			"Ralph subagent provider %q inherits parent context; Ralph requires a fresh provider", name)
	}
	return nil
}

// parentOf 把这次执行落在的那把钥匙换成那个活 agent。
//
// 源: packages/workflow/tool-ralph/src/index.ts:438-441
//
// 查不回来是错，理由写在 [Config.AgentOf] 上。那句话给模型看，所以是英文；
// 括号里那半句照抄 DSH，虽然 Go 这边没有 undefined 这回事——它是给读日志的人
// 对上游文档用的。
func (c *Controller) parentOf(exec *tools.RunContext) (agent.Agent, error) {
	if exec == nil || exec.Agent == nil {
		return nil, errors.New("Ralph tool requires a calling agent (exec.agent was undefined)")
	}
	parent, err := c.agentOf(exec.Agent)
	if err != nil || parent == nil {
		return nil, errors.New("Ralph tool requires a calling agent (exec.agent was undefined)")
	}
	return parent, nil
}

// runResultSchema 是那份终局值的契约。
//
// 源: packages/workflow/tool-ralph/src/index.ts:379-383
//
// 新增: DSH 那份 schema 里 result 是 `{ type: 'json' }`——一个不透明的口子，
// 形状全靠 readRunResult 在运行时兜。本包这边形状是**已知**的（[runResult] 就在
// 眼前），所以把它写出来：工具运行时那道输出校验因此能替本包把关，而不是等到
// 渲染那一步才发现值不对。
func runResultSchema() tools.Node {
	closed := false
	statuses := make([]json.RawMessage, 0, 3)
	for _, status := range []RunStatus{RunComplete, RunBlocked, RunBudgetLimited} {
		// 排的是一个具名字符串类型，排不失败。
		raw, _ := json.Marshal(string(status))
		statuses = append(statuses, raw)
	}
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "status", Schema: tools.Node{Type: tools.TypeString, Enum: statuses}},
			{Name: "roundsStarted", Schema: tools.Node{Type: tools.TypeInteger}},
			{Name: "report", Schema: reportSchema()},
		},
		Required:             []string{"status", "roundsStarted", "report"},
		AdditionalProperties: &closed,
	}
}

// outputSchema 是这件工具那份权威结果值的契约。
//
// 源: packages/workflow/tool-ralph/src/index.ts:427-431
func outputSchema() tools.Node {
	closed := false
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "agentsStarted", Schema: tools.Node{Type: tools.TypeInteger}},
			{Name: "result", Schema: runResultSchema()},
		},
		Required:             []string{"agentsStarted", "result"},
		AdditionalProperties: &closed,
	}
}

// execute 是这件工具的体：把几道关依次过掉，再把那条循环推起来。
//
// 源: packages/workflow/tool-ralph/src/index.ts:437-475
//
// 四道关的次序照抄 DSH，而且**全在开第一个孩子之前**：一次注定跑不成的调用要在
// 一分钱都还没花的时候就被拒掉。
func (c *Controller) execute(
	ctx context.Context,
	subagents Subagents,
	rawArgs json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var args callArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	parent, err := c.parentOf(exec)
	if err != nil {
		return nil, err
	}
	objective := strings.TrimSpace(args.Objective)
	if objective == "" {
		return nil, errors.New("Ralph objective must be a non-empty string")
	}
	maxRounds, err := resolveMaxRounds(args.MaxRounds, c.maxRounds)
	if err != nil {
		return nil, err
	}
	if err := requireFreshProvider(subagents, c.provider); err != nil {
		return nil, err
	}
	result, started, err := c.runLoop(ctx, subagents, parent, objective, maxRounds)
	if err != nil {
		return nil, err
	}
	return json.Marshal(outputValue{AgentsStarted: started, Result: result})
}

// newTool 造那件工具。
//
// 源: packages/workflow/tool-ralph/src/index.ts:410-476
//
// 那条子 agent 接缝由闭包捕获，理由见 [Controller]。
func (c *Controller) newTool(subagents Subagents) *tools.Definition {
	return &tools.Definition{
		Name:        ToolName,
		Description: toolDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "objective", Schema: tools.Node{
					Type:        tools.TypeString,
					Description: objectiveDescription,
				}},
				{Name: "maxRounds", Schema: tools.Node{
					Type:        tools.TypeInteger,
					Description: maxRoundsDescription,
				}},
			},
			Required: []string{"objective"},
		},
		// 没有 IsConcurrencySafe，也就是**不**安全。
		//
		// 新增: DSH 那边同样没写，但那是默认值撞上了；这里是有意的：这件工具的每一轮
		// 都在那个共享工作区上真动手，而工作区正是它唯一的长期记忆。让两次 Ralph
		// 并排跑，等于让两条循环轮流覆盖对方刚写下的东西，而两边都会以为那是自己
		// 上一轮的成果。
		Output: tools.OutputDefinition{
			Schema: outputSchema(),
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var decoded outputValue
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{
					Text: renderResult(decoded.Result, c.maxResultChars),
				}}, nil
			},
		},
		Execute: func(
			ctx context.Context,
			rawArgs json.RawMessage,
			exec *tools.RunContext,
		) (json.RawMessage, error) {
			return c.execute(ctx, subagents, rawArgs, exec)
		},
		PresentCall: func(args json.RawMessage) tools.CallView {
			var decoded callArgs
			_ = json.Unmarshal(args, &decoded)
			return tools.GenericCallView{Title: ToolName, Kind: tools.CallExecute}
		},
	}
}

// Install 把那件工具和那段用法指引一起装上一个作用域，交回把它们一起摘下来的函数。
//
// 源: packages/workflow/tool-ralph/src/index.ts:405-411
//
// 中途失败按反序摘干净，形状和
// [github.com/snight1983/ds-harness-go/feature/subagent/subagenttool.Controller.Install] 一样。
//
// 新增: DSH 那边先登记提示词段再登记工具。这里反过来：工具装不上是这次装配失败的
// 大头（重名、作用域不对），先装它就不用把一段已经登记好的提示词再摘回来。两条
// 生命周期各自独立，次序换了对外看不出差别。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("toolralph: 需要一个工具运行时")
	case deps.Subagents == nil:
		return nil, fmt.Errorf("toolralph: 需要一条子 agent 接缝")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("toolralph: 需要一个系统提示词注册表")
	case owner == nil:
		return nil, fmt.Errorf("toolralph: 需要一个作用域")
	}

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		return errors.Join(failures...)
	}
	fail := func(what string, err error) (func(context.Context) error, error) {
		// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
		_ = undo(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("toolralph: 装%s失败：%w", what, err)
	}

	remove, err := deps.Tools.Register(ctx, owner, c.newTool(deps.Subagents))
	if err != nil {
		return fail("那件 Ralph 工具", err)
	}
	installed = append(installed, remove)

	remove, err = deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText(sectionText),
	})
	if err != nil {
		return fail("那段用法指引", err)
	}
	installed = append(installed, remove)

	return undo, nil
}
