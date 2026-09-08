// 本文件的作用：这十件工具交给模型的那几种结果值，以及它们各自的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:42-149

package agentteamtool

import (
	"bytes"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/tools"
)

// memberWire 是花名册上一行交给模型时的样子。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:47-61（MEMBER_VIEW_SCHEMA）
//
// 新增: 不直接排
// [github.com/snight1983/ds-harness-go/feature/agentteam.MemberView]，因为那份值的
// Diagnostics 带 omitempty——一个没有诊断的成员排出来会少掉这个键，而 DSH 那份
// schema 把它写成必填。落盘的键名可以省，给模型看的形状不能变：模型每一轮看见的
// 键集合必须一样，否则它得自己去分辨「没有这个键」和「这个键是空的」。
//
// DSH 那份 schema 里还有一个 model 字段。本包不排它：
// [github.com/snight1983/ds-harness-go/feature/agentteam.MemberView] 没有这一列——
// 队友用哪个模型归子 Agent 提供方管，花名册不记它，编一个出来只会骗模型。
type memberWire struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Status      string   `json:"status"`
	Description string   `json:"description,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Context     string   `json:"context,omitempty"`
	Diagnostics []string `json:"diagnostics"`
}

// member 把一行花名册折成给模型看的样子。
func member(view agentteam.MemberView) memberWire {
	diagnostics := view.Diagnostics
	if diagnostics == nil {
		diagnostics = []string{}
	}
	return memberWire{
		ID:          string(view.ID),
		Name:        view.Name,
		Role:        string(view.Role),
		Status:      string(view.Status),
		Description: view.Description,
		Provider:    view.Provider,
		Context:     string(view.Context),
		Diagnostics: diagnostics,
	}
}

// members 折一整份花名册。
func members(views []agentteam.MemberView) []memberWire {
	wires := make([]memberWire, 0, len(views))
	for _, view := range views {
		wires = append(wires, member(view))
	}
	return wires
}

// taskWire 是一条共享任务交给模型时的样子。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:64-79（TASK_VIEW_SCHEMA）
//
// 新增: 两个键名和落盘时不一样，是照着 DSH 给模型的那份契约来的：rev 在这里叫
// revision，scopeWarnings 在这里叫 writeScopeWarnings。前者尤其要紧——
// team_task_update 的入参就叫 expected_revision，两处名字对不上会让模型自己去猜
// 该把哪个数填进去。
type taskWire struct {
	ID                 string   `json:"id"`
	Revision           int      `json:"revision"`
	Subject            string   `json:"subject"`
	Description        string   `json:"description"`
	Status             string   `json:"status"`
	OwnerName          string   `json:"ownerName,omitempty"`
	BlockedBy          []string `json:"blockedBy"`
	WriteScopes        []string `json:"writeScopes"`
	Ready              bool     `json:"ready"`
	WriteScopeWarnings []string `json:"writeScopeWarnings"`
}

// task 把一条任务折成给模型看的样子。
func task(view agentteam.TaskView) taskWire {
	blockedBy := make([]string, 0, len(view.BlockedBy))
	for _, id := range view.BlockedBy {
		blockedBy = append(blockedBy, string(id))
	}
	scopes := view.WriteScopes
	if scopes == nil {
		scopes = []string{}
	}
	warnings := view.ScopeWarnings
	if warnings == nil {
		warnings = []string{}
	}
	return taskWire{
		ID:                 string(view.ID),
		Revision:           view.Rev,
		Subject:            view.Subject,
		Description:        view.Description,
		Status:             string(view.Status),
		OwnerName:          view.OwnerName,
		BlockedBy:          blockedBy,
		WriteScopes:        scopes,
		Ready:              view.Ready,
		WriteScopeWarnings: warnings,
	}
}

// spawnWire 是 spawn_teammate 的结果。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:81-87（SPAWN_VALUE_SCHEMA）
type spawnWire struct {
	Member memberWire `json:"member"`
}

// sendWire 是 send_message 和 followup_task 的结果。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:91-98（SEND_VALUE_SCHEMA）
type sendWire struct {
	MessageID string `json:"messageId"`
	Status    string `json:"status"`
}

// 这两个是 [sendWire.Status] 的取值。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:96
//
// 它们对应
// [github.com/snight1983/ds-harness-go/feature/agentteam.MessageDelivered] 和
// [github.com/snight1983/ds-harness-go/feature/agentteam.MessageQueued]，
// 那一档 claimed 到不了这里：一次 Send 交回来的只有这两种终态。
//
// **两种都表示这条消息已经耐久了**，queued 只是说「还没送到收件人跟前」。策略指引
// 里那句 "A successful send is already durable even when its result says queued;
// do not resend it" 说的就是这件事。
const (
	statusAccepted = "accepted"
	statusQueued   = "queued"
)

// sendStatus 把一条消息的落盘档位译成给模型看的那两个字。
func sendStatus(phase agentteam.MessagePhase) string {
	if phase == agentteam.MessageDelivered {
		return statusAccepted
	}
	return statusQueued
}

// waitWire 是 wait_agent 的结果。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:101-115（WAIT_VALUE_SCHEMA）
type waitWire struct {
	TimedOut   bool          `json:"timedOut"`
	NoProgress *progressWire `json:"noProgress,omitempty"`
}

// progressWire 是那条「等下去也没用」的短路说明。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:106-113
//
// 它只在短路那一支出现：一次真的等待不会带它，无论等着了还是到了点。
type progressWire struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// interruptWire 是 interrupt_agent 的结果。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:117-123（INTERRUPT_VALUE_SCHEMA）
type interruptWire struct {
	PreviousStatus string `json:"previousStatus"`
}

// taskListWire 是 team_task_list 的结果。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:125-132（TASK_LIST_VALUE_SCHEMA）
//
// NextCursor 是指针：没有下一页时那个键整个消失，和 DSH 那份可选字段一致。
type taskListWire struct {
	Tasks      []taskWire `json:"tasks"`
	NextCursor *int       `json:"nextCursor,omitempty"`
}

// closed 是那些 additionalProperties:false 的对象共用的那个 false。
//
// [github.com/snight1983/ds-harness-go/tools.Node.AdditionalProperties] 是指针，
// nil 表示「没写」，所以要有一个取得到地址的 false。
var closed = false

// enum 把一串取值排成 schema 里的那份白名单。
func enum(values ...string) []json.RawMessage {
	raw := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		raw = append(raw, encoded)
	}
	return raw
}

// stringArray 是那几个字符串数组字段共用的 schema。
func stringArray() tools.Node {
	return tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}}
}

// memberSchema 排出花名册一行的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:47-61
func memberSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "name", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "role", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(string(agentteam.RoleLead), string(agentteam.RoleTeammate)),
			}},
			{Name: "status", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(
					string(agentteam.StatusRunning), string(agentteam.StatusIdle),
					string(agentteam.StatusInactive), string(agentteam.StatusProvisioning),
					string(agentteam.StatusFailed),
				),
			}},
			{Name: "description", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "provider", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "context", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(string(agentteam.ContextFresh), string(agentteam.ContextFork)),
			}},
			{Name: "diagnostics", Schema: stringArray()},
		},
		Required:             []string{"id", "name", "role", "status", "diagnostics"},
		AdditionalProperties: &closed,
	}
}

// taskSchema 排出一条共享任务的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:64-79
func taskSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "revision", Schema: tools.Node{Type: tools.TypeInteger}},
			{Name: "subject", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "description", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "status", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(
					string(agentteam.TaskPending), string(agentteam.TaskInProgress),
					string(agentteam.TaskCompleted), string(agentteam.TaskDeleted),
				),
			}},
			{Name: "ownerName", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "blockedBy", Schema: stringArray()},
			{Name: "writeScopes", Schema: stringArray()},
			{Name: "ready", Schema: tools.Node{Type: tools.TypeBoolean}},
			{Name: "writeScopeWarnings", Schema: stringArray()},
		},
		Required: []string{
			"id", "revision", "subject", "description", "status",
			"blockedBy", "writeScopes", "ready", "writeScopeWarnings",
		},
		AdditionalProperties: &closed,
	}
}

// jsonOutput 把一份输出契约配上那条「原样排成一行 JSON」的渲染。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:141-149
//
// decode 先把结果解回结构体再排一遍，为的是钉住键序：模型每一轮看见的形状必须
// 一模一样，否则提示词缓存的键每次都不同。做法同
// [github.com/snight1983/ds-harness-go/feature/goal/goaltool] 那份 goalOutput。
func jsonOutput[T any](schema tools.Node) tools.OutputDefinition {
	return tools.OutputDefinition{
		Schema: schema,
		Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
			var decoded T
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, err
			}
			encoded, err := marshalNoEscape(decoded)
			if err != nil {
				return nil, err
			}
			return llm.Content{llm.TextBlock{Text: string(encoded)}}, nil
		},
	}
}

// marshalNoEscape 把一个值排成 JSON，**不**做 HTML 转义。
//
// 新增: DSH 那份输出是 JSON.stringify 排的，它不把 < > & 转成 &lt; 这类写法；
// [encoding/json.Marshal] 默认转。任务标题、正文、队友职责都是人和模型写的自由文本，
// 而这份字节直接摆进模型上下文里给它读——多出来的转义只会让它看见一句和原文长得
// 不一样的话。理由同
// [github.com/snight1983/ds-harness-go/feature/goal/goaltool.marshalNoEscape]。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// present 是这十件工具共用的那张待定卡片。
//
// 新增: DSH 那份工具定义一件都没写 presentCall，于是宿主只能拿工具名和一坨原始
// 入参去显示。本包给每一件配一张卡片：团队工具的调用密度高（一个队长一轮能发好几条
// 消息、改好几条任务），一串只有工具名的记录读不出发生了什么。
func present(title string, kind tools.CallKind, rawInput any) tools.CallView {
	view := tools.GenericCallView{Title: title, Kind: kind}
	if rawInput != nil {
		// 排不出去在这里不可能：调用点给的只有字符串和整数。真出了岔子就当没给——
		// 呈现是纯函数，不许失败。
		view.RawInput, _ = json.Marshal(rawInput)
	}
	return view
}
