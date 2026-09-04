// 本文件的作用：send_message 和 interrupt_agent 这两件工具——它们给模型看的说明、
// 那两条把请求原样递给子 agent 运行时的执行体，以及把它们一起装上一个作用域的那一步。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:18-120

package controltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/subagent/tool-subagent-control/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-subagent-control"

// SendMessageTool 是那件投递后续消息的工具在模型那边的名字。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:27
const SendMessageTool = "send_message"

// InterruptTool 是那件请求打断的工具在模型那边的名字。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:80
const InterruptTool = "interrupt_agent"

// sendMessageDescription 是 send_message 给模型看的说明。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:28-33
const sendMessageDescription = "Send a message to a background subagent by its subagent id, continuing the same " +
	"conversation. It becomes the subagent's next turn: if it is still working, the message waits until its " +
	"current turn finishes, so it cannot redirect work already underway. This call returns no answer from the " +
	"subagent — only confirmation that the message was delivered — so use it to give it more work. A failure " +
	"means the message was NOT delivered."

// subagentIDDescription 是 send_message 那个收件人参数的说明。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:38
const subagentIDDescription = "The subagent id returned when the background subagent was started."

// messageDescription 是 send_message 那个正文参数的说明。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:43
const messageDescription = "The message to deliver to the subagent."

// interruptDescription 是 interrupt_agent 给模型看的说明。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:81-87
const interruptDescription = "Request cancellation of a background agent's current turn by its agent id. The target " +
	"may be your direct child or a deeper agent created under you. Only the current turn stops: messages already " +
	"queued for the agent stay parked until a later send_message, agents it started keep running, and the agent " +
	"itself stays available for follow-ups. This call returns as soon as the stop request is accepted, so the " +
	"target may keep running briefly; interrupting an agent that already finished is an accepted no-op."

// agentIDDescription 是 interrupt_agent 那个目标参数的说明。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:92
const agentIDDescription = "The agent id of the running agent to interrupt."

// missingSendMessageAgent 是 send_message 没落在一个 agent 上时给模型的话。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:63
const missingSendMessageAgent = "send_message requires a calling agent (exec.agent was undefined)"

// missingInterruptAgent 是 interrupt_agent 没落在一个 agent 上时给模型的话。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:112
const missingInterruptAgent = "interrupt_agent requires a calling agent (exec.agent was undefined)"

// Service 是这两件工具用得到的那一小块子 agent 运行时。
//
// 新增: DSH 注入整个 `ctx.subagents`。这里只写出真正被调到的那两个方法，装配方交
// 进来的 [github.com/snight1983/ds-harness-go/feature/subagent.Runtime] 自然满足它（窄口子的理由同
// [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.Service]）。
type Service interface {
	// Followup 把一条后续消息投给一个可续孩子的下一个 FIFO 回合。
	Followup(
		ctx context.Context,
		parent agent.Agent,
		childID sessionlog.SessionID,
		content llm.Content,
		options subagent.FollowupOptions,
	) (llm.MessageID, error)
	// Interrupt 打断一个活着的可续孩子当下那段活动。
	//
	// 新增: 这个方法**不收 ctx**，和 [github.com/snight1983/ds-harness-go/feature/subagent.Runtime.Interrupt]
	// 一样：打断是发完就返回的，它自己不等任何东西，没有可取消的等待。
	Interrupt(targetSessionID sessionlog.SessionID, authority subagent.InterruptAuthority) error
}

// Config 是这两件工具的装配面。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:19
type Config struct {
	// Service 是那台子 agent 运行时，必填。
	Service Service
	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和
	// [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.Config.AgentOf] 逐字相同。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
}

// Deps 是装这两件工具那一刻要交进来的东西。
//
// 新增: DSH 从 cordis 上下文上直接取 `ctx.tools`。Go 没有那个容器，所以显式交进来，
// 形状和 [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.Deps] 一致。
type Deps struct {
	// Tools 是工具运行时，那两件工具登记在它上面，必填。
	Tools *tools.Runtime
}

// Controller 是攥着那台服务、并且知道怎么把那两件工具装上一个作用域的那个对象。
type Controller struct {
	service Service
	agentOf func(agent *scope.Key) (agent.Agent, error)
}

// New 造一个控制器。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:25
func New(config Config) (*Controller, error) {
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("controltool: 需要一台子 agent 运行时")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("controltool: 需要一条从作用域钥匙找回 agent 的路")
	}
	return &Controller{service: config.Service, agentOf: config.AgentOf}, nil
}

// sendMessageArgs 是 send_message 的参数。
type sendMessageArgs struct {
	SubagentID sessionlog.SessionID `json:"subagent_id"`
	Message    string               `json:"message"`
}

// sendMessageResult 是 send_message 的结果。
type sendMessageResult struct {
	MessageID llm.MessageID `json:"messageId"`
}

// interruptArgs 是 interrupt_agent 的参数。
type interruptArgs struct {
	AgentID sessionlog.SessionID `json:"agent_id"`
}

// interruptResult 是 interrupt_agent 的结果。
//
// 新增: 这里没有 omitempty。`accepted` 恒为 true，可 Go 的 false 零值要是被省掉，
// 那份 additionalProperties:false 的 schema 就少了一个 required 字段。
type interruptResult struct {
	Accepted bool `json:"accepted"`
}

// callerOf 把这次执行落在的那把钥匙换成那个确切的活 agent。
//
// 新增: DSH 那边 `exec.agent` 要么是 Agent 要么是 undefined，一个 if 就判完了。
// Go 这边多一层查回去，而「查不回来」和「压根没落在 agent 上」对模型是同一件事，
// 所以合成一条：这两件工具凭的都是**那个确切的活调用方**，找不到它就没有权可凭。
func (c *Controller) callerOf(exec *tools.RunContext) (agent.Agent, bool) {
	if exec == nil || exec.Agent == nil {
		return nil, false
	}
	caller, err := c.agentOf(exec.Agent)
	if err != nil || caller == nil {
		return nil, false
	}
	return caller, true
}

// newSendMessage 造那件 send_message 工具。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:26-77
func (c *Controller) newSendMessage() *tools.Definition {
	closed := false
	return &tools.Definition{
		Name:        SendMessageTool,
		Description: sendMessageDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{
					Name:   "subagent_id",
					Schema: tools.Node{Type: tools.TypeString, Description: subagentIDDescription},
				},
				{
					Name:   "message",
					Schema: tools.Node{Type: tools.TypeString, Description: messageDescription},
				},
			},
			Required: []string{"subagent_id", "message"},
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
			// 渲染那句话只用参数，不用结果：告诉模型这条消息排到了谁的下一个回合。
			Render: func(args json.RawMessage, _ json.RawMessage) (llm.Content, error) {
				var input sendMessageArgs
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{
					Text: "message queued as the next turn for subagent " + string(input.SubagentID),
				}}, nil
			},
		},
		Execute: c.sendMessage,
	}
}

// sendMessage 是 send_message 的体：把那段文本投给指名的那个孩子。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:59-76
func (c *Controller) sendMessage(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input sendMessageArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	// 父那份权凭的是一个确切的活调用方。
	parent, ok := c.callerOf(exec)
	if !ok {
		return nil, errors.New(missingSendMessageAgent)
	}
	source, err := subagent.NewCoordinatorSource(parent.ID())
	if err != nil {
		return nil, err
	}
	content := llm.Content{llm.TextBlock{Text: input.Message}}
	messageID, err := c.service.Followup(ctx, parent, input.SubagentID, content, subagent.FollowupOptions{
		Source: source,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(sendMessageResult{MessageID: messageID})
}

// newInterrupt 造那件 interrupt_agent 工具。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:79-119
func (c *Controller) newInterrupt() *tools.Definition {
	closed := false
	return &tools.Definition{
		Name:        InterruptTool,
		Description: interruptDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name:   "agent_id",
				Schema: tools.Node{Type: tools.TypeString, Description: agentIDDescription},
			}},
			Required: []string{"agent_id"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{{
					Name:   "accepted",
					Schema: tools.Node{Type: tools.TypeBoolean},
				}},
				Required:             []string{"accepted"},
				AdditionalProperties: &closed,
			},
			Render: func(args json.RawMessage, _ json.RawMessage) (llm.Content, error) {
				var input interruptArgs
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{
					Text: "interrupt requested for agent " + string(input.AgentID),
				}}, nil
			},
		},
		Execute: c.interrupt,
	}
}

// interrupt 是 interrupt_agent 的体：把打断请求原样递给服务。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:108-118
func (c *Controller) interrupt(
	_ context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input interruptArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	// 祖先那份权凭的是一个确切的活调用方。
	caller, ok := c.callerOf(exec)
	if !ok {
		return nil, errors.New(missingInterruptAgent)
	}
	// 准入是服务拿调用方去核目标记下来的血统，这件工具自己不添任何权。
	if err := c.service.Interrupt(input.AgentID, subagent.InterruptAuthority{
		Kind:  subagent.AuthorityAncestor,
		Agent: caller,
	}); err != nil {
		return nil, err
	}
	return json.Marshal(interruptResult{Accepted: true})
}

// Install 把 send_message 和 interrupt_agent 一起装上一个作用域，交回把它们一起
// 摘下来的函数。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:25-120
//
// 后一件装不上就把前一件摘干净再报错：半装上去意味着模型手上有一件能投递、却没有
// 任何办法叫停的控制工具。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	if deps.Tools == nil {
		return nil, fmt.Errorf("controltool: 需要一个工具运行时")
	}
	return installAll(ctx, owner, deps.Tools, []*tools.Definition{c.newSendMessage(), c.newInterrupt()})
}

// installAll 按次序装一组工具，中途失败就把已经装上的按反序摘干净。
//
// 新增: 本包两个控制器共用它。做法和 [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool.Controller.Install]
// 里那段逐字相同，只是抽了出来——那边还要连一段提示词指引，本包没有。
func installAll(
	ctx context.Context,
	owner *scope.Scope,
	runtime *tools.Runtime,
	definitions []*tools.Definition,
) (func(context.Context) error, error) {
	var installed []func(context.Context) error
	// 摘的时候每一笔都要试过再报失败：一笔摘不掉不该让另外几笔留在那儿。
	// [errors.Join] 会把 nil 丢掉，所以这里不必逐个判——一次成功的摘除交回的就是 nil。
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		return errors.Join(failures...)
	}
	for _, definition := range definitions {
		dispose, err := runtime.Register(ctx, owner, definition)
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("controltool: 装 %s 失败：%w", definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
