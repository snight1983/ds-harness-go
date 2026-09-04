// 本文件的作用：list_agents 这件工具——它给模型看的说明、那份两支联合的返回 schema、
// 把服务那几行投影成模型看得懂的条目的那一步，以及把它单独装上一个作用域的那一步。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:17-192

package controltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// ListAgentsTool 是那件列举工具在模型那边的名字。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:17（name）
const ListAgentsTool = "list_agents"

// 这两个是 scope 参数认得的取值。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:20
const (
	// ScopeChildren 只列直接孩子，是不给时的默认值。
	ScopeChildren = "children"
	// ScopeDescendants 按稳定的先序走完整棵树。
	ScopeDescendants = "descendants"
)

// 这两个是一条条目属于哪一支。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:32,41
const (
	entryChild      = "child"
	entryDiagnostic = "diagnostic"
)

// 这三个是一个孩子在模型那边的状态。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:35
const (
	// statusRunning 表示这个 agent 此刻正在干活。
	statusRunning = "running"
	// statusIdle 表示它装载着、但停在两个回合之间（可能正等着它自己起的那些 agent）。
	statusIdle = "idle"
	// statusReady 表示活的登记里已经没有它了，只剩存储里那份——接得上，但不是终局，
	// 也不是一份等着被取走的结果。
	statusReady = "ready"
)

// listAgentsDescription 是 list_agents 给模型看的说明。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:94-105
const listAgentsDescription = "List your continuable background subagents by durable id and label. Use it to recall " +
	"which ones you started, not to poll for completion — you are told when one finishes. Status comes from the " +
	"live registry: running means the agent is working right now, idle means it is loaded but between turns (it " +
	"may be waiting on agents it started), and ready means it exists only in storage — resumable, not terminal, " +
	"and not a result waiting to be collected; a `send_message` starts a new turn on the same conversation, and a " +
	"direct child remains a `send_message` candidate in every status. The snapshot is not a delivery promise — " +
	"`send_message` performs the authoritative check and may still fail. Children that could not be read are " +
	"reported as diagnostics instead of being silently dropped. Scope `descendants` walks the whole tree below " +
	"you in stable pre-order, annotating each entry with its durable direct-parent session id and depth. You may " +
	"use `send_message` only for depth-1 entries; deeper entries are candidates for `interrupt_agent` only."

// scopeDescription 是那个唯一参数的说明。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:110
const scopeDescription = "children (default) lists direct children only; " +
	"descendants walks the complete tree below you."

// emptyListing 是一次数为零的列举渲染出来的那句话。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:149
const emptyListing = "(no subagents)"

// missingListAgent 是 list_agents 没落在一个 agent 上时给模型的话。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:168
const missingListAgent = "list_agents requires a calling agent (exec.agent was undefined)"

// ListingService 是这件工具用得到的那一小块子 agent 运行时。
//
// 新增: 窄口子的理由同 [Service]。这两个方法都不装载也不恢复任何 agent。
type ListingService interface {
	// ListChildren 列举一个父那些有会话的直接子 agent。
	ListChildren(ctx context.Context, parentSessionID sessionlog.SessionID) ([]subagent.ListEntry, error)
	// ListDescendants 按稳定的先序列举一个根底下完整的那棵树。
	ListDescendants(ctx context.Context, rootSessionID sessionlog.SessionID) ([]subagent.DescendantListEntry, error)
}

// Agents 是那份活 agent 登记里本包用得到的那一格。
//
// 新增: DSH 注入整个 `ctx.agents`。这里只写出 get 那一个，装配方交进来的
// [github.com/snight1983/ds-harness-go/harness/agent.Registry] 自然满足它。
type Agents interface {
	// Get 按会话 id 找那个活着的 agent；第二个返回值是在不在。
	Get(id sessionlog.SessionID) (agent.Agent, bool)
}

// ListConfig 是这件工具的装配面。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:18
type ListConfig struct {
	// Service 是那台子 agent 运行时，必填。
	Service ListingService
	// Agents 是那份活 agent 登记，状态那一列靠它精化，必填。
	Agents Agents
	// AgentOf 从一把作用域钥匙找到那个 agent，必填。理由同 [Config.AgentOf]。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
}

// ListDeps 是装这件工具那一刻要交进来的东西。
type ListDeps struct {
	// Tools 是工具运行时，那件工具登记在它上面，必填。
	Tools *tools.Runtime
}

// ListController 是攥着那几样东西、并且知道怎么把 list_agents 装上一个作用域的
// 那个对象。
//
// 新增: 它和 [Controller] 是**两个**控制器，对应 DSH 那两个分开的插件。分开的
// 理由写在 DSH 那份模块注释里：一套部署可以只登记续接投递而不外露发现。
type ListController struct {
	service ListingService
	agents  Agents
	agentOf func(agent *scope.Key) (agent.Agent, error)
}

// NewListAgents 造一个列举控制器。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:91
func NewListAgents(config ListConfig) (*ListController, error) {
	switch {
	case config.Service == nil:
		return nil, fmt.Errorf("controltool: 需要一台子 agent 运行时")
	case config.Agents == nil:
		return nil, fmt.Errorf("controltool: 需要一份活 agent 登记")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("controltool: 需要一条从作用域钥匙找回 agent 的路")
	}
	return &ListController{service: config.Service, agents: config.Agents, agentOf: config.AgentOf}, nil
}

// listAgentsArgs 是这件工具的参数。
//
// 新增: DSH 那边 scope 可缺席，缺席时补 'children'。Go 的零值是空串，
// [resolveScope] 把空串当成缺席，取值和 DSH 那句 `request.scope ?? 'children'` 一样。
type listAgentsArgs struct {
	Scope string `json:"scope,omitempty"`
}

// resolveScope 把模型那份可缺席的请求解成一个必然有值的范围。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:48-50
func resolveScope(scope string) string {
	if scope == "" {
		return ScopeChildren
	}
	return scope
}

// listedEntry 是交给模型的一条条目。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:30-45
//
// 新增: DSH 是 `{kind:'child',...} | {kind:'diagnostic',...}` 两支按 kind 判别的
// 联合。Go 没有判别联合，和 [github.com/snight1983/ds-harness-go/feature/subagent.ListEntry] 是同一种
// 做法：**一个**结构体加一个 Kind 字段。那份 schema 仍旧是两支封闭的 oneOf，所以
// 不属于本支的字段一律 omitempty——少一个键就是那一支该有的形状。
type listedEntry struct {
	// Kind 是这一条属于哪一支。两支都有。
	Kind string `json:"kind"`
	// ID 是这个孩子耐久的会话 id。两支都有。
	ID sessionlog.SessionID `json:"id"`

	// Label 是描述符上那个耐久的创建名。只有 child 有。
	//
	// 这里不带 omitempty：一个可续孩子的 label 必然非空（见
	// [github.com/snight1983/ds-harness-go/feature/subagent.ListEntry.Label]），而 child 那一支
	// 要求它在场。diagnostic 那一支走的是 [diagnosticEntry]，压根没有这个字段。
	Label string `json:"label"`
	// Status 是活登记这一刻给出的状态。只有 child 有。
	Status string `json:"status"`

	// Parent 是这一条耐久的直接父。只有 descendants 那个范围有。
	Parent sessionlog.SessionID `json:"parent,omitempty"`
	// Depth 是离调用方有几条边；直接孩子是 1。只有 descendants 那个范围有。
	//
	// omitempty 在这里是安全的：深度从 1 起算，0 只可能是「没给位置」。
	Depth int `json:"depth,omitempty"`
}

// diagnosticEntry 是 diagnostic 那一支。
//
// 新增: 它和 [listedEntry] 分成两个结构体，为的是让 additionalProperties:false
// 那道封闭真的成立——一个带着空 label/status 键的诊断行会被那份 schema 判为多字段。
type diagnosticEntry struct {
	Kind   string                    `json:"kind"`
	ID     sessionlog.SessionID      `json:"id"`
	Reason subagent.DiagnosticReason `json:"reason"`
	Parent sessionlog.SessionID      `json:"parent,omitempty"`
	Depth  int                       `json:"depth,omitempty"`
}

// renderedEntry 是渲染那一步读回来的形状：两支的字段合在一起，缺的就是零值。
//
// 新增: DSH 的 render 拿到的是那个联合本身。Go 这边渲染读的是自己刚排出去的 JSON，
// 所以要一个把两支都读得进来的形状。
type renderedEntry struct {
	Kind   string               `json:"kind"`
	ID     sessionlog.SessionID `json:"id"`
	Label  string               `json:"label"`
	Status string               `json:"status"`
	Reason string               `json:"reason"`
	Parent sessionlog.SessionID `json:"parent"`
	Depth  int                  `json:"depth"`
}

// statusOf 拿活 agent 登记精化一个候选的状态。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:59-63
//
// 登记里没有它就是 [statusReady]：接得上，但不把一段停着的对话摆成一份等着被取走
// 的终局结果。
func statusOf(agents Agents, id sessionlog.SessionID) string {
	live, ok := agents.Get(id)
	if !ok {
		return statusReady
	}
	if live.Status() == agent.StatusRunning {
		return statusRunning
	}
	return statusIdle
}

// project 把服务那一行投影成模型看得懂的条目；一次性孩子交回 nil 表示略过。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:66-85
//
// position 不为 nil 时这一条带上它在树里的位置，那正是 descendants 那个范围。
//
// 新增: DSH 交回 `ListAgentsEntry | undefined` 再 filter 掉 undefined。Go 这边
// 交回 `any`（两支是两个结构体），nil 表示这一行不进结果。
func project(agents Agents, entry subagent.ListEntry, position *subagent.DescendantListEntry) any {
	var parent sessionlog.SessionID
	var depth int
	if position != nil {
		parent, depth = position.ParentID, position.Depth
	}
	if entry.Kind == subagent.EntryDiagnostic {
		return diagnosticEntry{
			Kind:   entryDiagnostic,
			ID:     entry.ID,
			Reason: entry.Reason,
			Parent: parent,
			Depth:  depth,
		}
	}
	// 一次性孩子接不上 send_message，所以模型永远不会选它；发现那一步为了找到挂在
	// 它底下的可续后代，仍旧走过了它。
	if entry.Mode != subagent.ModeContinuable {
		return nil
	}
	return listedEntry{
		Kind:   entryChild,
		ID:     entry.ID,
		Label:  entry.Label,
		Status: statusOf(agents, entry.ID),
		Parent: parent,
		Depth:  depth,
	}
}

// newDefinition 造那件 list_agents 工具。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:92-191
func (c *ListController) newDefinition() *tools.Definition {
	closed := false
	position := []tools.Property{
		{Name: "parent", Schema: tools.Node{Type: tools.TypeString}},
		{Name: "depth", Schema: tools.Node{Type: tools.TypeNumber}},
	}
	return &tools.Definition{
		Name:        ListAgentsTool,
		Description: listAgentsDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name: "scope",
				Schema: tools.Node{
					Type:        tools.TypeString,
					Enum:        []json.RawMessage{jsonOf(ScopeChildren), jsonOf(ScopeDescendants)},
					Description: scopeDescription,
				},
			}},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeArray,
				Items: &tools.Node{OneOf: []tools.Node{
					{
						Type: tools.TypeObject,
						Properties: append([]tools.Property{
							{Name: "kind", Schema: tools.Node{
								Type: tools.TypeString,
								Enum: []json.RawMessage{jsonOf(entryChild)},
							}},
							{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
							{Name: "label", Schema: tools.Node{Type: tools.TypeString}},
							{Name: "status", Schema: tools.Node{
								Type: tools.TypeString,
								Enum: []json.RawMessage{
									jsonOf(statusRunning), jsonOf(statusIdle), jsonOf(statusReady),
								},
							}},
						}, position...),
						Required:             []string{"kind", "id", "label", "status"},
						AdditionalProperties: &closed,
					},
					{
						Type: tools.TypeObject,
						Properties: append([]tools.Property{
							{Name: "kind", Schema: tools.Node{
								Type: tools.TypeString,
								Enum: []json.RawMessage{jsonOf(entryDiagnostic)},
							}},
							{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
							{Name: "reason", Schema: tools.Node{
								Type: tools.TypeString,
								Enum: []json.RawMessage{
									jsonOf(string(subagent.DiagnosticCorrupt)),
									jsonOf(string(subagent.DiagnosticUnsupported)),
									jsonOf(string(subagent.DiagnosticUnavailable)),
								},
							}},
						}, position...),
						Required:             []string{"kind", "id", "reason"},
						AdditionalProperties: &closed,
					},
				}},
			},
			Render: renderListing,
		},
		Execute: c.list,
	}
}

// jsonOf 把一个字符串排成一段 JSON 字面量，给 schema 的 enum 用。
//
// 新增: [github.com/snight1983/ds-harness-go/tools.Node.Enum] 是一串 [encoding/json.RawMessage]，
// 因为这个子集允许的取值不止字符串。这里的取值全是字符串常量，排不失败。
func jsonOf(value string) json.RawMessage {
	encoded, _ := json.Marshal(value) //nolint:errchkjson // 字符串常量排不出错
	return encoded
}

// renderListing 把那份数组渲染成给模型看的几行。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:144-162
func renderListing(args json.RawMessage, value json.RawMessage) (llm.Content, error) {
	var request listAgentsArgs
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	var entries []renderedEntry
	if err := json.Unmarshal(value, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return llm.Content{llm.TextBlock{Text: emptyListing}}, nil
	}
	withPosition := resolveScope(request.Scope) == ScopeDescendants
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		// descendants 那一行必然带着位置，children 那些行从不渲染它。
		at := ""
		if withPosition {
			at = " parent=" + string(entry.Parent) + " depth=" + strconv.Itoa(entry.Depth)
		}
		if entry.Kind == entryChild {
			lines = append(lines, string(entry.ID)+" ["+entry.Status+"]"+at+" — "+entry.Label)
			continue
		}
		lines = append(lines, string(entry.ID)+" [diagnostic: "+entry.Reason+"]"+at)
	}
	return llm.Content{llm.TextBlock{Text: strings.Join(lines, "\n")}}, nil
}

// list 是这件工具的体：按范围列一遍，投影，排出去。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:164-190
func (c *ListController) list(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var request listAgentsArgs
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	// 不落在 agent 上的调用方没有会话，也就没有孩子可列。
	if exec == nil || exec.Agent == nil {
		return nil, errors.New(missingListAgent)
	}
	parent, err := c.agentOf(exec.Agent)
	if err != nil || parent == nil {
		return nil, errors.New(missingListAgent)
	}

	// 交回的一定是一个数组：Go 的 nil 切片排出来是 null，那不合那份 schema，
	// 而模型看见 null 也读不出「一个都没有」。
	projected := []any{}
	if resolveScope(request.Scope) == ScopeDescendants {
		entries, err := c.service.ListDescendants(ctx, parent.ID())
		if err != nil {
			return nil, err
		}
		for index := range entries {
			if entry := project(c.agents, entries[index].ListEntry, &entries[index]); entry != nil {
				projected = append(projected, entry)
			}
		}
		return json.Marshal(projected)
	}
	// DSH 那条 default 分支在 Go 里不存在：范围是一个开放的字符串，认不得的取值
	// 由那份 schema 的 enum 在进执行体之前挡住，落到这里的就只剩 children。
	entries, err := c.service.ListChildren(ctx, parent.ID())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if projectedEntry := project(c.agents, entry, nil); projectedEntry != nil {
			projected = append(projected, projectedEntry)
		}
	}
	return json.Marshal(projected)
}

// Install 把 list_agents 装上一个作用域，交回把它摘下来的函数。
//
// 源: packages/subagent/tool-subagent-control/src/list-agents.ts:91-192
func (c *ListController) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps ListDeps,
) (func(context.Context) error, error) {
	if deps.Tools == nil {
		return nil, fmt.Errorf("controltool: 需要一个工具运行时")
	}
	return installAll(ctx, owner, deps.Tools, []*tools.Definition{c.newDefinition()})
}
