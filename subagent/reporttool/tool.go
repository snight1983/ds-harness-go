// 本文件的作用：那件 report 工具本身——它给模型看的说明、那段用法指引、
// 往一个孩子作用域里装的那两笔登记，以及把这份贡献挂上可续装配表的那一步。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:16-140

package reporttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/subagent/tool-subagent-report/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-subagent-report"

// ToolName 是这件工具在模型那边的名字。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:66
const ToolName = "report"

// SectionName 是那段用法指引在系统提示词里的段名。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:55
const SectionName = "tool:" + ToolName

// SectionOrder 把那段指引排在一个可续孩子可能带的每一段按工具的小节**之后**。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:24
//
// 新增: DSH 这个值是 117，而 tool-subagent 那段用的是 **116.5**——它要挤在
// tool-ralph 的 116 和这一段中间。Go 的
// [github.com/snight1983/ds-harness-go/core/systemprompt.PromptSection.Order] 是 int，116 和 117 之间
// 没有空位，所以 [github.com/snight1983/ds-harness-go/subagent/subagenttool.SectionOrder] 占了 117，
// 这一段顺推到 118。次序只有**相对关系**是有意义的，每一对的先后都没变。
const SectionOrder = 118

// DefaultDelivery 是没指定时那份被接受的汇报在父那边的排期策略。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:37
//
// 新增: DSH 是 schemastery 的 `.default('next-step')`，默认值在 apply 里解析配置
// 时补。Go 里补默认值的地方是 [New]，这个常量是它的取值。
const DefaultDelivery = subagent.DeliveryNextStep

// promptText 是那段用法指引的正文。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:57-61
const promptText = "Deliver your result with the report tool before you finish: call it once with a self-contained " +
	"answer. The agent that started you shares your workspace but does not automatically receive your " +
	"transcript, tool output, or reasoning, so a closing remark such as \"done\" leaves it nothing it can " +
	"use. Report earlier as well whenever a partial finding changes what that agent should do next; " +
	"reporting never ends your turn."

// toolDescription 是这件工具给模型看的说明。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:67-73
const toolDescription = "Report selected content to the agent that started you. Call this once before you finish, " +
	"with a self-contained final result, and earlier for progress or findings that change what that agent does " +
	"next. That agent shares your workspace but does not automatically receive your transcript, tool " +
	"output, or reasoning, so finishing your work is not itself a result. Reporting does not end your " +
	"turn or finish your work, and only your direct parent receives it. A failed call may still have " +
	"arrived, so do not blindly repeat it."

// outputDescription 是那个唯一参数的说明。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:78
const outputDescription = "Actionable content for your parent; " +
	"summarize conclusions and reference relevant shared paths."

// missingAgent 是这次执行没落在一个 agent 上时给模型的话。
//
// 新增: DSH 那边 `exec.agent as Agent` 是一句断言——它靠「这件工具只装在孩子作用域
// 上」这条事实保证一定有 agent，断错了就是运行期 TypeError。Go 里那把钥匙查不回去
// 是一个正常的返回值，所以写成一条工具错误：这件事只有装配出错时才发生，让模型看见
// 一句话，好过让整个回合炸掉。
const missingAgent = "the report tool requires an agent-bound caller"

// Service 是本包用得到的那一小块子 agent 运行时。
//
// 新增: DSH 注入整个 `ctx.subagents`。这里只写出真正被调到的那两个方法，装配方交
// 进来的 [github.com/snight1983/ds-harness-go/subagent/subagent.Runtime] 自然满足它（窄口子的理由同
// [github.com/snight1983/ds-harness-go/sessionquery/querytool.Service]）。
type Service interface {
	// ReportFrom 把一个活着的可续孩子选出来的内容投给它耐久的直系父。
	ReportFrom(
		ctx context.Context,
		child agent.Agent,
		content llm.Content,
		options subagent.ReportOptions,
	) (llm.MessageID, error)
	// RegisterContinuableSetup 把一份能力组合进每一个可续孩子还没公布的创建窗口。
	RegisterContinuableSetup(
		contribution subagent.ActivationSetupContribution,
	) (func(context.Context) error, error)
}

// Config 是这个包的装配面。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:27-38
type Config struct {
	// Service 是那台子 agent 运行时，必填。
	Service Service
	// Tools 是工具运行时，那件 report 工具登记在它上面，必填。
	//
	// 新增: DSH 从孩子那个 cordis 上下文上直接取 `childCtx.tools`。Go 没有那个容器，
	// 所以服务经这里显式交进来（成例见
	// [github.com/snight1983/ds-harness-go/subagent/subagent.ChildCompositionServices]）。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，那段指引登记在它上面，必填。
	Prompts *systemprompt.Registry
	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/sessionquery/querytool.Config.AgentOf] 逐字相同。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
	// Delivery 是被接受的汇报在父那边的排期策略；空串取 [DefaultDelivery]。
	Delivery subagent.ReportDelivery
}

// Controller 是攥着这几样东西、并且知道怎么把那两笔登记装进一个孩子的那个对象。
type Controller struct {
	service  Service
	tools    *tools.Runtime
	prompts  *systemprompt.Registry
	agentOf  func(agent *scope.Key) (agent.Agent, error)
	delivery subagent.ReportDelivery
	// definition 造一次用很多回：每个孩子装的是同一件工具，只是 owner 不同。
	definition *tools.Definition
}

// New 造一个控制器。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:129-140
//
// 新增: 取值合法性 DSH 交给 schemastery 的 `z.union(['quiet','next-step'])`。
// Go 这边 [github.com/snight1983/ds-harness-go/subagent/subagent.ReportDelivery] 是个开放的字符串类型，
// 所以在这里挡一道——一个拼错的排期策略要是漏到运行期，表现是汇报静静地不唤醒父，
// 那种毛病很难从现场看出来。
func New(config Config) (*Controller, error) {
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("reporttool: 需要一台子 agent 运行时")
	case config.Tools == nil:
		return nil, fmt.Errorf("reporttool: 需要一个工具运行时")
	case config.Prompts == nil:
		return nil, fmt.Errorf("reporttool: 需要一个系统提示词注册表")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("reporttool: 需要一条从作用域钥匙找回 agent 的路")
	}
	delivery := config.Delivery
	if delivery == "" {
		delivery = DefaultDelivery
	}
	if delivery != subagent.DeliveryQuiet && delivery != subagent.DeliveryNextStep {
		return nil, fmt.Errorf("reporttool: 认不得排期策略 %q", delivery)
	}

	controller := &Controller{
		service:  config.Service,
		tools:    config.Tools,
		prompts:  config.Prompts,
		agentOf:  config.AgentOf,
		delivery: delivery,
	}
	controller.definition = controller.newDefinition()
	return controller, nil
}

// reportArgs 是这件工具的参数。
type reportArgs struct {
	Output string `json:"output"`
}

// reportResult 是这件工具的结果。
type reportResult struct {
	MessageID llm.MessageID `json:"messageId"`
}

// newDefinition 造那件 report 工具。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:65-104
func (c *Controller) newDefinition() *tools.Definition {
	closed := false
	return &tools.Definition{
		Name:        ToolName,
		Description: toolDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name:   "output",
				Schema: tools.Node{Type: tools.TypeString, Description: outputDescription},
			}},
			Required: []string{"output"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{{
					Name:   "messageId",
					Schema: tools.Node{Type: tools.TypeString},
				}},
				Required:             []string{"messageId"},
				AdditionalProperties: &closed,
			},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var result reportResult
				if err := json.Unmarshal(value, &result); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{
					Text: "report accepted by the agent that started you as message " + string(result.MessageID),
				}}, nil
			},
		},
		Execute: c.execute,
	}
}

// execute 是这件工具的体：把那段文本投给父，交回那条消息的 id。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:94-103
func (c *Controller) execute(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input reportArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	if exec == nil || exec.Agent == nil {
		return nil, errors.New(missingAgent)
	}
	// 带作用域的解算保证这里是那个孩子本人。真正那道权柄边界上，服务还要再核一遍
	// 它确切的活化身份。
	child, err := c.agentOf(exec.Agent)
	if err != nil || child == nil {
		return nil, errors.New(missingAgent)
	}
	content := llm.Content{llm.TextBlock{Text: input.Output}}
	messageID, err := c.service.ReportFrom(ctx, child, content, subagent.ReportOptions{Delivery: c.delivery})
	if err != nil {
		return nil, err
	}
	return json.Marshal(reportResult{MessageID: messageID})
}

// Contribute 把 report 和它那段用法指引装进**一个**可续孩子的作用域。这两笔登记
// 都归那个作用域所有，所以对这个孩子的父和兄弟都不可见。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:38-127（installReportTool）
//
// 新增: 它的签名**恰好**就是 [github.com/snight1983/ds-harness-go/subagent/subagent.ActivationSetupContribution]，
// 所以这个方法值可以直接登记出去，不需要中间再包一层。
func (c *Controller) Contribute(
	ctx context.Context,
	childScope *scope.Scope,
) (func(context.Context) error, error) {
	// 指引先装：它装不上的话那件工具还没露出去，模型不会看见一件没有说明的工具。
	removeSection, err := c.prompts.Section(ctx, childScope, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText(promptText),
	})
	if err != nil {
		return nil, fmt.Errorf("reporttool: 装 %s 指引失败：%w", ToolName, err)
	}
	removeTool, err := c.tools.Register(ctx, childScope, c.definition)
	if err != nil {
		// 装工具这一下失败要把指引滚回去，不能给这个孩子留一段说明一件它没有的工具
		// 的提示词。滚回去要是也失败，两个原因一起报——[errors.Join] 会把 nil 丢掉，
		// 拼出来的东西仍旧 [errors.Is] 得出每一个原因，正是 DSH 那个 AggregateError
		// 的意思。
		return nil, errors.Join(
			fmt.Errorf("reporttool: 装 %s 工具失败：%w", ToolName, err),
			removeSection(ctx),
		)
	}

	// 摘的时候两笔都要试过再报失败：一笔摘不掉不该让另一笔留在那儿。
	return func(releaseCtx context.Context) error {
		return errors.Join(removeTool(releaseCtx), removeSection(releaseCtx))
	}, nil
}

// Install 把这份贡献挂上可续孩子的装配表，交回撤销这次登记的函数。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:129-140
//
// 新增: 本仓库其余工具包的 Install 收 (ctx, owner, deps)，因为它们是直接往一个
// 作用域上装。这一个不是：它登记的是一份**等孩子出生才装**的贡献，作用域是那时候
// 才有的那个孩子的，ctx 也是那时候那次创建的。
func (c *Controller) Install() (func(context.Context) error, error) {
	return c.service.RegisterContinuableSetup(c.Contribute)
}
