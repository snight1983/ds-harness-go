// 本文件的作用：get_goal、create_goal、update_goal 这三件工具的本体——它们给模型
// 看的说明、那份共用的紧凑输出、update_goal 那道从宽到严的资格阶梯，以及把这一整套
// 连同那段策略指引装上一个作用域的那一步。
//
// 源: packages/goal/tool-goal/src/index.ts:45-338

package goaltool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/goal/goal"
	"ds-harness-go/llm"
)

// 这三个是三件工具在模型那边的名字。
//
// 源: packages/goal/tool-goal/src/index.ts:196,208,235
const (
	// GetToolName 读当前这个目标。
	GetToolName = "get_goal"
	// CreateToolName 建一个目标。
	CreateToolName = "create_goal"
	// UpdateToolName 改当前这个目标的状态。
	UpdateToolName = "update_goal"
)

// SectionName 和 SectionOrder 是那段策略指引在系统提示词里的位置。
//
// 源: packages/goal/tool-goal/src/index.ts:190-191
const (
	SectionName  = "tool:goal"
	SectionOrder = 114
)

// 这五个是 update_goal 的 action 取值。
//
// 源: packages/goal/tool-goal/src/index.ts:41-43
const (
	actionEdit     = "edit"
	actionPause    = "pause"
	actionResume   = "resume"
	actionComplete = "complete"
	actionBlocked  = "blocked"
)

// updateActions 是那份 enum 白名单，顺序和 DSH 一致。
//
// 源: packages/goal/tool-goal/src/index.ts:43
var updateActions = []string{actionEdit, actionPause, actionResume, actionComplete, actionBlocked}

// blockCode 是模型自报阻塞时落进 [ds-harness-go/goal/goal.BlockReason] 的那个分类。
//
// 源: packages/goal/tool-goal/src/index.ts:310
//
// 它把「模型说它卡住了」和别的阻塞源（提供方限额、配置预算、执行错误）分开——
// 那些共用同一个耐久阶段，只靠这个码分流。
const blockCode = "model-reported"

// createDescription 是 create_goal 给模型看的说明，一个字都不许改译。
//
// 源: packages/goal/tool-goal/src/index.ts:45-49
const createDescription = "Create one persisted same-session completion goal when the current direct human request " +
	"is a long-running objective that should continue across autonomous goal rounds. You may " +
	"infer that intent without requiring the user to say \"create a goal\". Do not use this for " +
	"trivial single-turn work. Execution rejects non-human and subagent authority."

// getDescription 是 get_goal 给模型看的说明。
//
// 源: packages/goal/tool-goal/src/index.ts:51-54
const getDescription = "Read the current same-session goal, including its exact id/revision, objective, phase, completed " +
	"continuation rounds, round limit, blocker reason when present, and whether another continuation is armed. " +
	"Call this before updating a goal."

// updateDescription 是 update_goal 给模型看的说明。
//
// 源: packages/goal/tool-goal/src/index.ts:236-239
const updateDescription = "Update the exact current goal revision. edit, pause, and resume require a direct " +
	"top-level human request. During an automatic continuation of the current goal, complete " +
	"and blocked are also allowed. blocked is rejected before the configured minimum round count; the model remains " +
	"responsible for judging that the same condition persisted across those rounds and must explain it in blocked_reason."

// 这六条是那些参数给模型看的说明。
//
// 源: packages/goal/tool-goal/src/index.ts:214,218,241-254
const (
	objectiveDescription     = "The concrete completion objective inferred from the direct human request."
	maxRoundsDescription     = "Optional positive safe-integer limit on automatic continuation rounds."
	goalIDDescription        = "Exact id returned by get_goal."
	revisionDescription      = "Exact positive revision returned by get_goal."
	actionDescription        = "edit | pause | resume | complete | blocked"
	editObjectiveDescription = "Replacement objective; valid only with action edit."
	editMaxRoundsDescription = "Replacement cap; valid only with action edit."
	blockedReasonDescription = "Concrete blocking condition; required only with action blocked."
)

// 这几句是拒收一次 update_goal 时给模型看的话，一个字都不许改译。
//
// 源: packages/goal/tool-goal/src/index.ts:149,267,276,288,293,297
const (
	invalidRef       = "goal_id must be non-empty and revision must be a positive safe integer"
	blockedOnlyField = "blocked_reason is valid only with action blocked"
	editOnlyFields   = "objective and max_goal_rounds are valid only with action edit"
	editOnlyOrBlock  = editOnlyFields + "; " + blockedOnlyField
	blockedRequired  = "blocked_reason is required with action blocked"
)

// invalidMaxRounds 是那台目标服务拒收一个非法轮数上限时给模型看的话。
//
// 源: packages/goal/goal/src/index.ts:141-144
//
// 新增: DSH 那边这句话只在域里出现——模型给的 `number` 原样送进去，非整数由域拒。
// Go 的 [ds-harness-go/goal/goal.CreateRequest.MaxGoalRounds] 是 *int，装不下 2.5，
// 所以那次拒收挪到了本层。字节要和域一模一样，否则同一个模型在两边看见两句话。
const invalidMaxRounds = "maxGoalRounds must be a positive safe integer"

// guidancePrefix 和 guidanceSuffix 夹着那个部署方选的轮数闸，拼出整段策略指引。
//
// 源: packages/goal/tool-goal/src/index.ts:113-123
const (
	guidancePrefix = "Use goal tools for one long-running completion objective in the current session. " +
		"create_goal may infer goal intent from a direct human request in any language; do not " +
		"create a goal for routine single-turn work. Call get_goal before update_goal and copy its " +
		"exact goal_id and revision. After session resume or fork, an active goal is disarmed: when " +
		"a human asks to continue or resume in any wording or language, use update_goal action " +
		"resume to rearm it. Mark complete only when the objective is actually achieved. Mark " +
		"blocked only after the same blocking condition persists for at least "
	guidanceSuffix = "consecutive goal rounds, and report that concrete condition in blocked_reason; " +
		"difficulty, uncertainty, or useful remaining work is not blocked."
)

// guidance 把那道轮数闸排进策略指引。
//
// 源: packages/goal/tool-goal/src/index.ts:113-123
//
// 那个数字后面**跟着一个空格**：DSH 的模板串在 `${blockedAfter} ` 之后才换的行，
// 少掉它两个词会粘在一起。
func guidance(blockedAfter int) string {
	return guidancePrefix + fmt.Sprintf("%d ", blockedAfter) + guidanceSuffix
}

// maxSafeInteger 是模型给的 JSON number 还能逐个数清楚的最大整数，同时夹住 int 的
// 宽度——32 位平台上 int 装不下 2^53。
//
// 源: packages/goal/tool-goal/src/index.ts:147（Number.isSafeInteger）
const maxSafeInteger = min(1<<53-1, math.MaxInt)

// safeInteger 把模型给的那个 JSON number 折成一个安全整数。
//
// 新增: 走 float64 而不是 [encoding/json.Number]，理由同
// [ds-harness-go/schedule/schedule.safeInteger]：JS 那边 3.0 和 3 是同一个数，
// 换成 json.Number 的话 "3.0" 解不出整数，于是同一个模型在两边表现不一样。
func safeInteger(value float64) (int, bool) {
	if value != math.Trunc(value) || math.Abs(value) > float64(maxSafeInteger) {
		return 0, false
	}
	return int(value), true
}

// marshalNoEscape 把一个值排成 JSON，**不**做 HTML 转义。
//
// 新增: DSH 那份输出是 JSON.stringify 排的，它不把 < > & 转成 < 这类写法；
// [encoding/json.Marshal] 默认转。目标描述和阻塞原因都是人和模型写的自由文本，
// 而这份字节直接摆进模型上下文里给它读——多出来的转义只会让它看见一句和原文长得
// 不一样的话。理由同 [ds-harness-go/goal/goal.marshalNoEscape]。
//
// 本包实际交给它的只有两种值：一个 Go 字符串，和一份只由字符串与整数组成的
// [goalWire]。两种都排得出来，所以除了那三条工具体（它们一句 return 就把错误转手
// 给了运行时），别的调用点一律把这个错误吞掉——那些地方多写一条永远走不到的分支，
// 只会让读的人以为它会发生。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// createArgs 是 create_goal 那份已经拆开的入参。
//
// MaxGoalRounds 是指针，因为「没写」和「写了 0」在这里不是一回事：没写走部署方的
// 默认轮数，写了 0 该被当场拒掉。类型取 float64 而不是整数，是为了让 DSH 那边
// `type: 'number'` 收得下的小数在这里同样收得下——一个被 Go 的解码器拒掉、却被 DSH
// 接受的入参，会让同一个模型在两边看见两种失败。
type createArgs struct {
	Objective     string   `json:"objective"`
	MaxGoalRounds *float64 `json:"max_goal_rounds"`
}

// updateArgs 是 update_goal 那份已经拆开的入参。
//
// 这里三个可选字段都**不是**指针：DSH 那边 hasText 和 hasRoundCap 把空串和 0
// 当成「严格 schema 下的填充值」一并放过（见 src/index.ts:135-142），Go 的零值
// 恰好就是那两个填充值，所以零值判别和 DSH 逐字同义。
type updateArgs struct {
	GoalID        string  `json:"goal_id"`
	Revision      float64 `json:"revision"`
	Action        string  `json:"action"`
	Objective     string  `json:"objective"`
	MaxGoalRounds float64 `json:"max_goal_rounds"`
	BlockedReason string  `json:"blocked_reason"`
}

// goalPayload 是那份紧凑输出里 goal 那一支的形状，键名和键序都和 DSH 逐字相同。
//
// 源: packages/goal/tool-goal/src/index.ts:59-69
type goalPayload struct {
	ID            goal.ID           `json:"id"`
	Revision      int               `json:"revision"`
	Objective     string            `json:"objective"`
	Phase         goal.Phase        `json:"phase"`
	RoundsStarted int               `json:"roundsStarted"`
	MaxGoalRounds int               `json:"maxGoalRounds"`
	BlockedReason *goal.BlockReason `json:"blockedReason,omitempty"`
}

// goalWire 是三件工具共用的那份权威结果值。
//
// 源: packages/goal/tool-goal/src/index.ts:57-70
//
// 新增: DSH 那边这是 `{goal:null}` 和 `{goal:{…}, activation}` 两支联合。Go 里合成
// 一个结构体：Goal 是 nil 时排出来就是 `{"goal":null}`，activation 那个键靠
// omitempty 整个消失——两支的字节因此和 DSH 逐字相同，不必自己写一个 MarshalJSON。
type goalWire struct {
	Goal       *goalPayload    `json:"goal"`
	Activation goal.Activation `json:"activation,omitempty"`
}

// goalValue 把一份目标视图折成那份紧凑结果；没有当前目标就是那支空的。
//
// 源: packages/goal/tool-goal/src/index.ts:157-173
//
// activation 是一次**观察**，不是回放状态：它从不落盘（见
// [ds-harness-go/goal/goal.Activation]），交给模型只是为了让它知道这个目标此刻
// 会不会自己往下推。
func goalValue(view *goal.View) goalWire {
	if view == nil {
		return goalWire{}
	}
	payload := goalPayload{
		ID:            view.ID,
		Revision:      view.Revision,
		Objective:     view.Objective,
		Phase:         view.Phase,
		RoundsStarted: view.RoundsStarted,
		MaxGoalRounds: view.MaxGoalRounds,
	}
	if view.BlockedReason != nil {
		// 复制一份再交出去：[ds-harness-go/goal/goal.View] 里那是一个导出的指针，
		// 直接转手意味着这份结果和那台服务共享同一块可写内存。
		reason := *view.BlockedReason
		payload.BlockedReason = &reason
	}
	return goalWire{Goal: &payload, Activation: view.Activation}
}

// goalValueSchema 排出那份紧凑结果的输出契约。
//
// 源: packages/goal/tool-goal/src/index.ts:72-110
//
// 新增: DSH 把「必填」写在每个属性自己身上（`required: true`），
// [ds-harness-go/core/tools.Node] 按 JSON Schema 本来的样子写在对象上（Required）。
// 两边表达的是同一件事。
func goalValueSchema() tools.Node {
	closed := false
	enum := func(values ...string) []json.RawMessage {
		raw := make([]json.RawMessage, 0, len(values))
		for _, value := range values {
			encoded, _ := json.Marshal(value)
			raw = append(raw, encoded)
		}
		return raw
	}
	return tools.Node{
		OneOf: []tools.Node{
			{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "goal", Schema: tools.Node{Type: tools.TypeNull}},
				},
				Required:             []string{"goal"},
				AdditionalProperties: &closed,
			},
			{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "goal", Schema: tools.Node{
						Type: tools.TypeObject,
						Properties: []tools.Property{
							{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
							{Name: "revision", Schema: tools.Node{Type: tools.TypeInteger}},
							{Name: "objective", Schema: tools.Node{Type: tools.TypeString}},
							{Name: "phase", Schema: tools.Node{
								Type: tools.TypeString,
								Enum: enum(
									string(goal.PhaseActive), string(goal.PhasePaused),
									string(goal.PhaseBlocked), string(goal.PhaseComplete),
								),
							}},
							{Name: "roundsStarted", Schema: tools.Node{Type: tools.TypeInteger}},
							{Name: "maxGoalRounds", Schema: tools.Node{Type: tools.TypeInteger}},
							{Name: "blockedReason", Schema: tools.Node{
								Type: tools.TypeObject,
								Properties: []tools.Property{
									{Name: "code", Schema: tools.Node{Type: tools.TypeString}},
									{Name: "message", Schema: tools.Node{Type: tools.TypeString}},
								},
								Required:             []string{"code", "message"},
								AdditionalProperties: &closed,
							}},
						},
						Required: []string{
							"id", "revision", "objective", "phase", "roundsStarted", "maxGoalRounds",
						},
						AdditionalProperties: &closed,
					}},
					{Name: "activation", Schema: tools.Node{
						Type: tools.TypeString,
						Enum: enum(string(goal.Armed), string(goal.Disarmed)),
					}},
				},
				Required:             []string{"goal", "activation"},
				AdditionalProperties: &closed,
			},
		},
	}
}

// goalOutput 是三件工具共用的那份输出声明。
//
// 源: packages/goal/tool-goal/src/index.ts:176-179
//
// 渲染就是把那个权威值原样排成一行 JSON——和 DSH 的 `JSON.stringify(value)` 一样。
// 先解回结构体再排一遍，是为了钉住键序：模型每一轮看见的形状必须一模一样，
// 否则提示词缓存的键每次都不同。
func goalOutput() tools.OutputDefinition {
	return tools.OutputDefinition{
		Schema: goalValueSchema(),
		Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
			var decoded goalWire
			if err := json.Unmarshal(value, &decoded); err != nil {
				return nil, err
			}
			encoded, _ := marshalNoEscape(decoded)
			return llm.Content{llm.TextBlock{Text: string(encoded)}}, nil
		},
	}
}

// present 是这三件工具共用的那张待定卡片。
//
// 源: packages/goal/tool-goal/src/index.ts:182-184
//
// 新增: DSH 的 rawInput 是 unknown，一个裸字符串或者裸数字直接就能放；Go 侧它是
// [encoding/json.RawMessage]，所以先排一遍，排不出去就留空——呈现是纯函数，
// 不许失败。rawInput 为 nil 表示这件工具压根不放原始输入。
func present(title string, kind tools.CallKind, rawInput any) tools.CallView {
	view := tools.GenericCallView{Title: title, Kind: kind}
	if rawInput != nil {
		// 排不出去在这里不可能：调用点给的只有字符串和数字。真出了岔子就当没给——
		// 呈现是纯函数，不许失败。
		view.RawInput, _ = json.Marshal(rawInput)
	}
	return view
}

// goalRef 从模型给的参数里造出那份 compare-and-set 身份。
//
// 源: packages/goal/tool-goal/src/index.ts:145-154
//
// 拒收带首尾空白的 id 是刻意的：一个被 trim 之后才对得上的 id 说明模型是从别处
// 抄过来的，而这道闸要的正是它**照抄** get_goal 交出来的那一份。
func goalRef(goalID string, revision float64) (goal.Ref, error) {
	rounds, ok := safeInteger(revision)
	if goalID == "" || goalID != strings.TrimSpace(goalID) || !ok || rounds < 1 {
		return goal.Ref{}, fail(CodeInvalidUpdate, invalidRef)
	}
	return goal.Ref{ID: goal.ID(goalID), Revision: rounds}, nil
}

// newGetTool 造那件 get_goal 工具。
//
// 源: packages/goal/tool-goal/src/index.ts:195-205
func (c *Controller) newGetTool() *tools.Definition {
	return &tools.Definition{
		Name:        GetToolName,
		Description: getDescription,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output:      goalOutput(),
		Execute:     c.readGoal,
		PresentCall: func(json.RawMessage) tools.CallView {
			return present("Read current goal", tools.CallRead, nil)
		},
	}
}

// readGoal 是 get_goal 的体。
//
// 源: packages/goal/tool-goal/src/index.ts:200-203
//
// 读也要过那道执行期闸：一个不在自己驱动回合里的调用方连「当前目标是什么」都不该
// 问得出来——那份答案里带着改状态要用的 id 和修订号。
func (c *Controller) readGoal(
	ctx context.Context,
	_ json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	execution, err := c.execution(ctx, exec)
	if err != nil {
		return nil, err
	}
	view, err := c.service.Get(execution.Agent)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(goalValue(view))
}

// newCreateTool 造那件 create_goal 工具。
//
// 源: packages/goal/tool-goal/src/index.ts:207-232
func (c *Controller) newCreateTool() *tools.Definition {
	return &tools.Definition{
		Name:        CreateToolName,
		Description: createDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "objective", Schema: tools.Node{
					Type: tools.TypeString, Description: objectiveDescription,
				}},
				{Name: "max_goal_rounds", Schema: tools.Node{
					Type: tools.TypeNumber, Description: maxRoundsDescription,
				}},
			},
			Required: []string{"objective"},
		},
		Output:  goalOutput(),
		Execute: c.createGoal,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input createArgs
			_ = json.Unmarshal(args, &input)
			return present("Create goal", tools.CallOther, input.Objective)
		},
	}
}

// createGoal 是 create_goal 的体。
//
// 源: packages/goal/tool-goal/src/index.ts:222-229
func (c *Controller) createGoal(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	execution, err := c.execution(ctx, exec)
	if err != nil {
		return nil, err
	}
	if err := c.requireDirectHuman(execution); err != nil {
		return nil, err
	}
	var input createArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	request := goal.CreateRequest{Objective: input.Objective}
	if input.MaxGoalRounds != nil {
		rounds, ok := safeInteger(*input.MaxGoalRounds)
		if !ok {
			return nil, &goal.Error{Code: goal.CodeInvalidMaxRounds, Message: invalidMaxRounds}
		}
		request.MaxGoalRounds = &rounds
	}
	view, err := c.service.Create(execution.Agent, request)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(goalValue(view))
}

// newUpdateTool 造那件 update_goal 工具。
//
// 源: packages/goal/tool-goal/src/index.ts:234-337
func (c *Controller) newUpdateTool() *tools.Definition {
	actions := make([]json.RawMessage, 0, len(updateActions))
	for _, action := range updateActions {
		encoded, _ := json.Marshal(action)
		actions = append(actions, encoded)
	}
	return &tools.Definition{
		Name:        UpdateToolName,
		Description: updateDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "goal_id", Schema: tools.Node{
					Type: tools.TypeString, Description: goalIDDescription,
				}},
				{Name: "revision", Schema: tools.Node{
					Type: tools.TypeNumber, Description: revisionDescription,
				}},
				{Name: "action", Schema: tools.Node{
					Type: tools.TypeString, Enum: actions, Description: actionDescription,
				}},
				{Name: "objective", Schema: tools.Node{
					Type: tools.TypeString, Description: editObjectiveDescription,
				}},
				{Name: "max_goal_rounds", Schema: tools.Node{
					Type: tools.TypeNumber, Description: editMaxRoundsDescription,
				}},
				{Name: "blocked_reason", Schema: tools.Node{
					Type: tools.TypeString, Description: blockedReasonDescription,
				}},
			},
			Required: []string{"goal_id", "revision", "action"},
		},
		Output:  goalOutput(),
		Execute: c.runUpdate,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input updateArgs
			_ = json.Unmarshal(args, &input)
			return present(updateTitle(input.Action), tools.CallOther, updateRawInput(input))
		},
	}
}

// updateTitle 是一次 update_goal 在卡片上的标题。
//
// 源: packages/goal/tool-goal/src/index.ts:329
//
// blocked 那一支特意不叫 "Blocked goal"——那读起来像在陈述目标的状态，而卡片说的
// 是这次调用要**做**什么。
func updateTitle(action string) string {
	if action == actionBlocked {
		return "Mark goal"
	}
	runes := []rune(action)
	if len(runes) == 0 {
		// schema 的 enum 挡得住空 action，但呈现是纯函数、参数是模型写的，
		// 走不到不等于写不出来。
		return " goal"
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes) + " goal"
}

// updateRawInput 挑出这次 update_goal 最值得摆在卡片上的那一个参数。
//
// 源: packages/goal/tool-goal/src/index.ts:331-335
//
// 优先级就是「哪一个最说明这次调用在干什么」：阻塞原因 > 新目标描述 > 新轮数上限 >
// 目标 id。
func updateRawInput(input updateArgs) any {
	switch {
	case input.BlockedReason != "":
		return input.BlockedReason
	case input.Objective != "":
		return input.Objective
	case input.MaxGoalRounds != 0:
		return input.MaxGoalRounds
	default:
		return input.GoalID
	}
}

// runUpdate 是 update_goal 的体：那道从宽到严的资格阶梯。
//
// 源: packages/goal/tool-goal/src/index.ts:257-326
func (c *Controller) runUpdate(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	execution, err := c.execution(ctx, exec)
	if err != nil {
		return nil, err
	}
	var input updateArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	ref, err := goalRef(input.GoalID, input.Revision)
	if err != nil {
		return nil, err
	}
	switch input.Action {
	case actionEdit:
		return c.runEdit(execution, ref, input)
	case actionPause, actionResume:
		return c.runSuspend(execution, ref, input)
	default:
		return c.runWrapup(execution, ref, input, exec)
	}
}

// replacements 把那两个可选字段折成一次 edit 的入参。
//
// 源: packages/goal/tool-goal/src/index.ts:260-263
//
// 空串和 0 是严格 schema 下的填充值，一律当成「没给」——这正是 DSH 的 hasText 和
// hasRoundCap 干的事。两个都没给的那次 edit 由域拒（GOAL_INVALID_EDIT），不在本层。
func replacements(input updateArgs) (goal.EditRequest, error) {
	var request goal.EditRequest
	if input.Objective != "" {
		objective := input.Objective
		request.Objective = &objective
	}
	if input.MaxGoalRounds != 0 {
		rounds, ok := safeInteger(input.MaxGoalRounds)
		if !ok {
			return goal.EditRequest{}, &goal.Error{
				Code: goal.CodeInvalidMaxRounds, Message: invalidMaxRounds,
			}
		}
		request.MaxGoalRounds = &rounds
	}
	return request, nil
}

// runEdit 是 action 为 edit 那一支。
//
// 源: packages/goal/tool-goal/src/index.ts:264-271
func (c *Controller) runEdit(
	execution Execution,
	ref goal.Ref,
	input updateArgs,
) (json.RawMessage, error) {
	if err := c.requireDirectHuman(execution); err != nil {
		return nil, err
	}
	if input.BlockedReason != "" {
		return nil, fail(CodeInvalidUpdate, blockedOnlyField)
	}
	request, err := replacements(input)
	if err != nil {
		return nil, err
	}
	view, err := c.service.Edit(execution.Agent, ref, request)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(goalValue(view))
}

// runSuspend 是 action 为 pause 或者 resume 那一支。
//
// 源: packages/goal/tool-goal/src/index.ts:272-284
//
// 这两个动作一个字段都不带：带了就说明模型把 edit 和它们混在一起了，与其挑一个
// 猜它想干什么，不如让它重来一次。
func (c *Controller) runSuspend(
	execution Execution,
	ref goal.Ref,
	input updateArgs,
) (json.RawMessage, error) {
	if err := c.requireDirectHuman(execution); err != nil {
		return nil, err
	}
	if input.Objective != "" || input.MaxGoalRounds != 0 || input.BlockedReason != "" {
		return nil, fail(CodeInvalidUpdate, editOnlyOrBlock)
	}
	var view *goal.View
	var err error
	if input.Action == actionPause {
		view, err = c.service.Pause(execution.Agent, ref)
	} else {
		view, err = c.service.Resume(execution.Agent, ref)
	}
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(goalValue(view))
}

// runWrapup 是 action 为 complete 或者 blocked 那一支。
//
// 源: packages/goal/tool-goal/src/index.ts:285-326
//
// 走到这里的 action 只可能是这两个之一：别的取值在 [Controller.runUpdate] 的
// switch 里已经分走了，而 schema 的 enum 挡住了这五个之外的一切——所以这里用
// 「不是 complete 就是 blocked」判别，不再多一道白名单。
func (c *Controller) runWrapup(
	execution Execution,
	ref goal.Ref,
	input updateArgs,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	authority, err := c.completionAuthority(execution)
	if err != nil {
		return nil, err
	}
	if input.Objective != "" || input.MaxGoalRounds != 0 {
		return nil, fail(CodeInvalidUpdate, editOnlyFields)
	}
	complete := input.Action == actionComplete
	if complete && input.BlockedReason != "" {
		return nil, fail(CodeInvalidUpdate, blockedOnlyField)
	}
	if !complete && strings.TrimSpace(input.BlockedReason) == "" {
		return nil, fail(CodeInvalidUpdate, blockedRequired)
	}
	if !complete && authority.Kind == AuthorityGoalRound &&
		authority.Goal.RoundsStarted < c.blockedAfter {
		return nil, fail(CodeBlockThreshold, blockThresholds, c.blockedAfter, authority.Goal.RoundsStarted)
	}
	var view *goal.View
	if complete {
		view, err = c.service.Complete(execution.Agent, ref)
	} else {
		view, err = c.service.Block(execution.Agent, ref, goal.BlockReason{
			Code: blockCode, Message: input.BlockedReason,
		})
	}
	if err != nil {
		return nil, err
	}
	if authority.Kind == AuthorityGoalRound {
		exec.DeferContext(wrapupMessage(input.Action, view.Objective, input.BlockedReason, complete))
	}
	return marshalNoEscape(goalValue(view))
}

// wrapupMessage 造那条捎在终局结果上的收尾指令。
//
// 源: packages/goal/tool-goal/src/index.ts:313-325
//
// 只有自动轮次那一支会捎它：一次直接的人类回合里，人就在对面，模型接着说话是
// 本来就会发生的事，不需要再塞一条指令。
func wrapupMessage(action, objective, blockedReason string, complete bool) llm.Message {
	reason := blockedReason
	if complete {
		reason = ""
	}
	return llm.NewUserMessage(
		renderWrapupContext(objective, reason),
		llm.PluginSource{
			Plugin:  PluginName,
			Context: llm.NoticeContext{Summary: llm.BoundContextSummary(action + ": " + objective)},
		},
	)
}

// Install 把那段策略指引和这三件工具装上一个作用域，交回把它们一起摘下来的函数。
//
// 源: packages/goal/tool-goal/src/index.ts:187-338
//
// 次序照 DSH 的 apply：先指引，再 get_goal、create_goal、update_goal。中途失败就按
// 反序摘干净——半装上去意味着模型手上有一件建得了目标、却没有那段策略指引管着的
// 工具，而那段指引正是「什么时候才该建目标」的唯一说明。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("goaltool: 需要一个工具运行时")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("goaltool: 需要一个系统提示词注册表")
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
	// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
	abort := func(what string, err error) (func(context.Context) error, error) {
		_ = undo(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("goaltool: 装%s失败：%w", what, err)
	}

	remove, err := deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText(guidance(c.blockedAfter)),
	})
	if err != nil {
		return abort("目标策略指引", err)
	}
	installed = append(installed, remove)

	for _, definition := range []*tools.Definition{
		c.newGetTool(), c.newCreateTool(), c.newUpdateTool(),
	} {
		dispose, err := deps.Tools.Register(ctx, owner, definition)
		if err != nil {
			return abort(definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
