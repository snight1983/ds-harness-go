// 本文件的作用：花名册和收件箱那六件工具——起队友、发消息、排回合、列成员、
// 等动静、打断队友。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:173-283

package agentteamtool

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/tools"
)

// 这六个是花名册和收件箱那组工具在模型那边的名字。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:174,222-223,226,236,271
const (
	// SpawnTool 起一个队友。
	SpawnTool = "spawn_teammate"
	// SendMessageTool 安静地给一个队友放一条消息。
	SendMessageTool = "send_message"
	// FollowupTool 给一个队友放一条消息并且排一个回合。
	FollowupTool = "followup_task"
	// ListAgentsTool 列出队长和所有队友。
	ListAgentsTool = "list_agents"
	// WaitTool 等这支团队下一次有动静。
	WaitTool = "wait_agent"
	// InterruptTool 打断一个队友此刻那段活动。
	InterruptTool = "interrupt_agent"
)

// 这六条是那几件工具给模型看的说明，一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:175,205-206,227,237,272
const (
	spawnDescription = "Create one named, durable teammate. Only the Team Lead may call this tool."

	sendMessageDescription = "Send durable information to another Team member without starting an idle member."

	followupDescription = "Send a durable follow-up task to another Team member and start a turn when needed."

	listAgentsDescription = "List the Lead and every durable teammate with current runtime status."

	waitDescription = "Wait for the next teammate status, mailbox, or shared-task change after this call starts. " +
		"This never wakes inactive members and returns noProgress immediately when no other member is running or " +
		"provisioning. Re-list after wakeup or timeout instead of polling."

	interruptDescription = "Interrupt one teammate's current turn while preserving its pending inbox. Team Lead only."
)

// 这几条是那几个参数给模型看的说明。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:177-183,208-209,240-241,274
const (
	spawnNameDescription        = "Unique lower-kebab-case teammate name."
	spawnDescriptionDescription = "Short description of the delegated responsibility."
	spawnPromptDescription      = "Complete initial task for the teammate."
	spawnContextDescription     = "fresh starts without Lead history; fork inherits completed Lead turns. Defaults to fresh."

	targetDescription  = "Team member name, or lead."
	messageDescription = "Self-contained message for the target."

	timeoutDescription = "Wait duration in milliseconds, from 10000 through 3600000. Defaults to 30000."

	interruptTargetDescription = "Teammate name."
)

// noActivePeerReason 和 noActivePeerMessage 是那条「等下去也没用」的短路说明，
// 一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:40,261
const (
	noActivePeerReason  = "no-active-peer"
	noActivePeerMessage = "No other Team member is running or provisioning. wait_agent cannot make progress or " +
		"wake inactive teammates. Re-list with list_agents and team_task_list, then use followup_task to wake each " +
		"required inactive teammate before waiting again."
)

// 这三个是等待时限那副界，单位是毫秒。
//
// 源: packages/experimental/agent-team/src/activity.ts:23、
// packages/experimental/tool-agent-team/src/index.ts:241,247
const (
	minTimeoutMillis     = 10_000
	maxTimeoutMillis     = 3_600_000
	defaultTimeoutMillis = 30_000
)

// invalidTimeout 是等待时限越界时给模型看的话，一个字都不许改译。
//
// 源: packages/experimental/agent-team/src/activity.ts:24
//
// 新增: DSH 那句话在服务里，工具层遇到越界值原样转手过去，由服务抛。本包的
// [github.com/snight1983/ds-harness-go/feature/agentteam.Service.Wait] 只拒非正数，
// 那副界刻意留在工具层（见它的说明），所以这句话跟着挪了过来。字节要和 DSH
// 一模一样，否则同一个模型在两边看见两句话。
const invalidTimeout = "timeoutMs must be an integer from 10000 through 3600000"

// leadOnlySpawn 是一个队友想起队友时给它看的话。
//
// 新增: [github.com/snight1983/ds-harness-go/feature/agentteam.Service.Spawn] 收的是
// 「谁当队长」而不是「谁在发起」——一个队友叫它会另起一支以自己为队长的团队，
// 而不是报错。DSH 那边 spawnTeammate 自己查这件事
// （packages/experimental/agent-team/src/roster.ts:250-252），本包挪到这一层查。
// 话的口气跟着
// [github.com/snight1983/ds-harness-go/feature/agentteam.Service.Interrupt] 那句走。
const leadOnlySpawn = "只有队长能起队友"

// maxSafeInteger 是模型给的 JSON number 还能逐个数清楚的最大整数，同时夹住 int 的
// 宽度——32 位平台上 int 装不下 2^53。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:250,332-333（Number.isSafeInteger）
const maxSafeInteger = min(1<<53-1, math.MaxInt)

// safeInteger 把模型给的那个 JSON number 折成一个安全整数。
//
// 新增: 走 float64 而不是 [encoding/json.Number]，理由同
// [github.com/snight1983/ds-harness-go/feature/goal/goaltool.safeInteger]：JS 那边
// 3.0 和 3 是同一个数，换成 json.Number 的话 "3.0" 解不出整数，于是同一个模型在
// 两边表现不一样。
func safeInteger(value float64) (int, bool) {
	if value != math.Trunc(value) || math.Abs(value) > float64(maxSafeInteger) {
		return 0, false
	}
	return int(value), true
}

// spawnArgs 是 spawn_teammate 的参数。
type spawnArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Context     string `json:"context"`
}

// messageArgs 是 send_message 和 followup_task 共用的参数。
type messageArgs struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

// waitArgs 是 wait_agent 的参数。
//
// TimeoutMillis 是指针，因为「没写」要落到默认的三十秒，而写了 0 该当场越界拒掉。
type waitArgs struct {
	TimeoutMillis *float64 `json:"timeout_ms"`
}

// interruptArgs 是 interrupt_agent 的参数。
type interruptArgs struct {
	Target string `json:"target"`
}

// newSpawnTool 造那件 spawn_teammate 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:173-199
func (c *Controller) newSpawnTool() *tools.Definition {
	return &tools.Definition{
		Name:        SpawnTool,
		Description: spawnDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "name", Schema: tools.Node{
					Type: tools.TypeString, Description: spawnNameDescription,
				}},
				{Name: "description", Schema: tools.Node{
					Type: tools.TypeString, Description: spawnDescriptionDescription,
				}},
				{Name: "prompt", Schema: tools.Node{
					Type: tools.TypeString, Description: spawnPromptDescription,
				}},
				{Name: "context", Schema: tools.Node{
					Type:        tools.TypeString,
					Enum:        enum(string(agentteam.ContextFresh), string(agentteam.ContextFork)),
					Description: spawnContextDescription,
				}},
			},
			Required: []string{"name", "description", "prompt"},
		},
		Output:  jsonOutput[spawnWire](spawnValueSchema()),
		Execute: c.spawn,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input spawnArgs
			_ = json.Unmarshal(args, &input)
			return present("Spawn teammate", tools.CallOther, input.Name)
		},
	}
}

// spawnValueSchema 排出 spawn_teammate 的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:81-87
func spawnValueSchema() tools.Node {
	return tools.Node{
		Type:                 tools.TypeObject,
		Properties:           []tools.Property{{Name: "member", Schema: memberSchema()}},
		Required:             []string{"member"},
		AdditionalProperties: &closed,
	}
}

// spawn 是 spawn_teammate 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:187-198
//
// 上文来源决定用哪个提供方：fork 那一支要从队长此刻的上文分一支，
// 和全新开一个会话不是同一条起法。
func (c *Controller) spawn(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, SpawnTool)
	if err != nil {
		return nil, err
	}
	if !who.Lead {
		return nil, &agentteam.Error{Code: agentteam.CodeLeadRequired, Message: leadOnlySpawn}
	}
	var input spawnArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	source := agentteam.MemberContext(input.Context)
	if input.Context == "" {
		source = agentteam.ContextFresh
	}
	provider := c.freshProvider
	if source == agentteam.ContextFork {
		provider = c.forkProvider
	}
	view, err := c.service.Spawn(ctx, who.ID, agentteam.SpawnRequest{
		Name:        input.Name,
		Description: input.Description,
		Provider:    provider,
		Context:     source,
		Prompt:      llm.Content{llm.TextBlock{Text: input.Prompt}},
	})
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(spawnWire{Member: member(view)})
}

// newMessageTool 造 send_message 或者 followup_task。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:201-223
//
// 两件工具只差一个送法和一句说明，所以共用一个造法——这正是 DSH 那个 messageTool
// 闭包在做的事。
func (c *Controller) newMessageTool(name string, delivery agentteam.Delivery) *tools.Definition {
	description := followupDescription
	title := "Follow up with teammate"
	if delivery == agentteam.DeliveryQuiet {
		description = sendMessageDescription
		title = "Message teammate"
	}
	return &tools.Definition{
		Name:        name,
		Description: description,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "target", Schema: tools.Node{
					Type: tools.TypeString, Description: targetDescription,
				}},
				{Name: "message", Schema: tools.Node{
					Type: tools.TypeString, Description: messageDescription,
				}},
			},
			Required: []string{"target", "message"},
		},
		Output: jsonOutput[sendWire](sendValueSchema()),
		Execute: func(
			ctx context.Context,
			args json.RawMessage,
			exec *tools.RunContext,
		) (json.RawMessage, error) {
			return c.send(ctx, args, exec, name, delivery)
		},
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input messageArgs
			_ = json.Unmarshal(args, &input)
			return present(title, tools.CallOther, input.Target)
		},
	}
}

// sendValueSchema 排出发信那两件工具的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:91-98
func sendValueSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "messageId", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "status", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(statusAccepted, statusQueued),
			}},
		},
		Required:             []string{"messageId", "status"},
		AdditionalProperties: &closed,
	}
}

// send 是 send_message 和 followup_task 共用的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:212-218
func (c *Controller) send(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
	toolName string,
	delivery agentteam.Delivery,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, toolName)
	if err != nil {
		return nil, err
	}
	var input messageArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	result, err := c.service.Send(ctx, who, agentteam.SendRequest{
		Target:   input.Target,
		Content:  llm.Content{llm.TextBlock{Text: input.Message}},
		Delivery: delivery,
	})
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(sendWire{
		MessageID: string(result.Message),
		Status:    sendStatus(result.Phase),
	})
}

// newListAgentsTool 造那件 list_agents 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:225-233
func (c *Controller) newListAgentsTool() *tools.Definition {
	return &tools.Definition{
		Name:        ListAgentsTool,
		Description: listAgentsDescription,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output:      jsonOutput[[]memberWire](tools.Node{Type: tools.TypeArray, Items: ptr(memberSchema())}),
		Execute:     c.listAgents,
		PresentCall: func(json.RawMessage) tools.CallView {
			return present("List Team members", tools.CallRead, nil)
		},
	}
}

// listAgents 是 list_agents 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:230-232
//
// 先解算一次发起人再列：不属于这支团队的调用方连花名册都不该看得到——那份名单里
// 带着发消息和改派任务要用的名字。
func (c *Controller) listAgents(
	ctx context.Context,
	_ json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, ListAgentsTool)
	if err != nil {
		return nil, err
	}
	views, err := c.service.Members(ctx, who.Team)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(members(views))
}

// newWaitTool 造那件 wait_agent 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:235-268
func (c *Controller) newWaitTool() *tools.Definition {
	return &tools.Definition{
		Name:        WaitTool,
		Description: waitDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name:   "timeout_ms",
				Schema: tools.Node{Type: tools.TypeInteger, Description: timeoutDescription},
			}},
		},
		Output:  jsonOutput[waitWire](waitValueSchema()),
		Execute: c.wait,
		PresentCall: func(json.RawMessage) tools.CallView {
			return present("Wait for Team activity", tools.CallOther, nil)
		},
	}
}

// waitValueSchema 排出 wait_agent 的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:101-115
func waitValueSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "timedOut", Schema: tools.Node{Type: tools.TypeBoolean}},
			{Name: "noProgress", Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "reason", Schema: tools.Node{
						Type: tools.TypeString, Const: json.RawMessage(`"` + noActivePeerReason + `"`),
					}},
					{Name: "message", Schema: tools.Node{Type: tools.TypeString}},
				},
				Required:             []string{"reason", "message"},
				AdditionalProperties: &closed,
			}},
		},
		Required:             []string{"timedOut"},
		AdditionalProperties: &closed,
	}
}

// wait 是 wait_agent 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:245-267
//
// 次序是刻意的，DSH 在那儿留了一条注释说明：**先验时限，再看有没有活着的同侪**。
// 反过来的话，一次带着非法时限的调用会先拿到那条「没人可等」的短路，
// 于是模型永远看不见自己把时限写错了。
func (c *Controller) wait(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, WaitTool)
	if err != nil {
		return nil, err
	}
	var input waitArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	millis := defaultTimeoutMillis
	if input.TimeoutMillis != nil {
		value, ok := safeInteger(*input.TimeoutMillis)
		if !ok || value < minTimeoutMillis || value > maxTimeoutMillis {
			return nil, &agentteam.Error{Code: agentteam.CodeInvalidTimeout, Message: invalidTimeout}
		}
		millis = value
	}

	// 本副本上一个在跑或者在开工的同侪都没有，就当场回话，不去占着一个回合空等。
	// 这个数只对本副本成立（见 agentteam.Service.ActivePeers），所以它只用来做
	// 「明显等不到」的短路——数出来大于零也不保证一定等得到。
	peers, err := c.service.ActivePeers(ctx, who)
	if err != nil {
		return nil, err
	}
	if peers == 0 {
		return marshalNoEscape(waitWire{NoProgress: &progressWire{
			Reason:  noActivePeerReason,
			Message: noActivePeerMessage,
		}})
	}

	result, err := c.service.Wait(ctx, who, time.Duration(millis)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(waitWire{TimedOut: result.TimedOut})
}

// newInterruptTool 造那件 interrupt_agent 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:270-283
func (c *Controller) newInterruptTool() *tools.Definition {
	return &tools.Definition{
		Name:        InterruptTool,
		Description: interruptDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name:   "target",
				Schema: tools.Node{Type: tools.TypeString, Description: interruptTargetDescription},
			}},
			Required: []string{"target"},
		},
		Output:  jsonOutput[interruptWire](interruptValueSchema()),
		Execute: c.interrupt,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input interruptArgs
			_ = json.Unmarshal(args, &input)
			return present("Interrupt teammate", tools.CallOther, input.Target)
		},
	}
}

// interruptValueSchema 排出 interrupt_agent 的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:117-123
//
// 那份 enum 里没有 provisioning 和 failed：打断的是一段**正在跑的活动**，
// 而这两档说的是这个队友还没起来或者没起成，压根没有活动可打断。
func interruptValueSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{{
			Name: "previousStatus",
			Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(
					string(agentteam.StatusRunning), string(agentteam.StatusIdle),
					string(agentteam.StatusInactive),
				),
			},
		}},
		Required:             []string{"previousStatus"},
		AdditionalProperties: &closed,
	}
}

// interrupt 是 interrupt_agent 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:277-281
//
// 队长那道闸在服务里查，这里不重复一遍：那件事归
// [github.com/snight1983/ds-harness-go/feature/agentteam.Service.Interrupt]，
// 它还要顺带核一次队长此刻在不在本副本上。
func (c *Controller) interrupt(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, InterruptTool)
	if err != nil {
		return nil, err
	}
	var input interruptArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	previous, err := c.service.Interrupt(ctx, who, input.Target)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(interruptWire{PreviousStatus: string(previous)})
}

// ptr 交出一个值的地址，给那几个要指针的 schema 字段用。
func ptr[T any](value T) *T { return &value }
