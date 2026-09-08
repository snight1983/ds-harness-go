// 本文件的作用：任务板那四件工具——新建一条、列一页、读一条整份值、按版本号改一条。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:285-387

package agentteamtool

import (
	"context"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/tools"
)

// 这四个是任务板那组工具在模型那边的名字。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:286,310,342,357
const (
	// TaskCreateTool 在板上新建一条没主的待办。
	TaskCreateTool = "team_task_create"
	// TaskListTool 列板上的任务，可以筛可以翻页。
	TaskListTool = "team_task_list"
	// TaskGetTool 读一条任务此刻的整份值。
	TaskGetTool = "team_task_get"
	// TaskUpdateTool 按版本号改一条任务。
	TaskUpdateTool = "team_task_update"
)

// 这四条是那几件工具给模型看的说明，一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:287,311,343,358
const (
	taskCreateDescription = "Create one unowned pending task on the shared Team task board."

	taskListDescription = "List shared tasks, including readiness, owner, revision, blockers, and " +
		"write-scope warnings."

	taskGetDescription = "Read the complete latest value of one shared task before changing or executing it."

	taskUpdateDescription = "Compare-and-set a shared task action using the latest revision from " +
		"team_task_get or team_task_list."
)

// 这几条是那几个参数给模型看的说明。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:289-295,314-321,345,360-372
const (
	subjectDescription     = "Concise task title."
	detailDescription      = "Complete task details and acceptance criteria."
	blockedByDescription   = "Task ids that must complete first."
	writeScopesDescription = "Advisory workspace-relative file or directory prefixes this task expects to modify."

	statusFilterDescription = "Optional exact status filter."
	ownerFilterDescription  = "Optional member-name filter; use unowned for tasks without an owner."
	readyFilterDescription  = "Optional readiness filter."
	cursorDescription       = "Zero-based result offset. Defaults to 0."
	limitDescription        = "Number of rows, 1 through 100. Defaults to 50."

	taskIDDescription = "Shared task id."

	expectedRevisionDescription = "Current task revision used as the CAS precondition."
	actionDescription           = "Task transition to apply."
	editSubjectDescription      = "Replacement title for edit."
	editDetailDescription       = "Replacement details for edit."
	dependenciesDescription     = "Complete blocker list for set_dependencies."
	editScopesDescription       = "Replacement advisory write scopes for edit."
	ownerDescription            = "Member name for Lead-only reassign; omit to unassign."
)

// 这两句是翻页参数越界时给模型看的话，一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:332-333
//
// 新增: DSH 那两处抛的是不带码的 `new Error`。本包一律带
// [github.com/snight1983/ds-harness-go/feature/agentteam.CodeInvalidArgument]——
// 话原样，码补上，这样宿主那边分派失败不必按字符串比对。
const (
	invalidCursor = "cursor must be a non-negative safe integer"
	invalidLimit  = "limit must be an integer from 1 through 100"
)

// invalidRevision 是 expected_revision 不是一个安全整数时给模型看的话。
//
// 新增: DSH 那边这个参数声明成 integer 就完了，越界值原样送进服务，撞上一次必不相等
// 的版本号比对，模型看见的是「版本号过期了，重读再来」——而它其实是把数写错了。
// 本包在这一层拦住，报的是参数不合法。
const invalidRevision = "expected_revision must be a safe integer"

// unownedFilter 是 owner 那个筛子里表示「没主」的那个值。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:328
const unownedFilter = "unowned"

// 这两个是翻页那两个参数没写时的默认值。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:330-331
const (
	defaultCursor = 0
	defaultLimit  = 50
)

// maxLimit 是一页最多几行。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:333
const maxLimit = 100

// createTaskArgs 是 team_task_create 的参数。
type createTaskArgs struct {
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	BlockedBy   []string `json:"blocked_by"`
	WriteScopes []string `json:"write_scopes"`
}

// listTaskArgs 是 team_task_list 的参数。
//
// 每一个都是指针，因为这五个全是可选的筛子，「没写」和「写了零值」是两件事：
// ready 写 false 是要筛出没就绪的那些，不写是不按就绪筛。
type listTaskArgs struct {
	Status *string  `json:"status"`
	Owner  *string  `json:"owner"`
	Ready  *bool    `json:"ready"`
	Cursor *float64 `json:"cursor"`
	Limit  *float64 `json:"limit"`
}

// getTaskArgs 是 team_task_get 的参数。
type getTaskArgs struct {
	TaskID string `json:"task_id"`
}

// updateTaskArgs 是 team_task_update 的参数。
//
// 后五个是指针，因为它们是「改这一项」而不是「把这一项设成空」——DSH 那边靠
// `args.x === undefined ? {} : { x: args.x }` 分辨，Go 里靠 nil 分辨。
type updateTaskArgs struct {
	TaskID           string    `json:"task_id"`
	ExpectedRevision float64   `json:"expected_revision"`
	Action           string    `json:"action"`
	Subject          *string   `json:"subject"`
	Description      *string   `json:"description"`
	BlockedBy        *[]string `json:"blocked_by"`
	WriteScopes      *[]string `json:"write_scopes"`
	Owner            *string   `json:"owner"`
}

// taskIDs 把模型给的那串字符串折成任务号。
func taskIDs(values []string) []agentteam.TaskID {
	ids := make([]agentteam.TaskID, 0, len(values))
	for _, value := range values {
		ids = append(ids, agentteam.TaskID(value))
	}
	return ids
}

// newTaskCreateTool 造那件 team_task_create 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:285-307
func (c *Controller) newTaskCreateTool() *tools.Definition {
	return &tools.Definition{
		Name:        TaskCreateTool,
		Description: taskCreateDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "subject", Schema: tools.Node{
					Type: tools.TypeString, Description: subjectDescription,
				}},
				{Name: "description", Schema: tools.Node{
					Type: tools.TypeString, Description: detailDescription,
				}},
				{Name: "blocked_by", Schema: withDescription(stringArray(), blockedByDescription)},
				{Name: "write_scopes", Schema: withDescription(stringArray(), writeScopesDescription)},
			},
			Required: []string{"subject", "description"},
		},
		Output:  jsonOutput[taskWire](taskSchema()),
		Execute: c.createTask,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input createTaskArgs
			_ = json.Unmarshal(args, &input)
			return present("Create Team task", tools.CallOther, input.Subject)
		},
	}
}

// createTask 是 team_task_create 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:299-306
func (c *Controller) createTask(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, TaskCreateTool)
	if err != nil {
		return nil, err
	}
	var input createTaskArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	view, err := c.service.CreateTask(ctx, who, agentteam.CreateTaskRequest{
		Subject:     input.Subject,
		Description: input.Description,
		BlockedBy:   taskIDs(input.BlockedBy),
		WriteScopes: input.WriteScopes,
	})
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(task(view))
}

// newTaskListTool 造那件 team_task_list 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:309-339
func (c *Controller) newTaskListTool() *tools.Definition {
	return &tools.Definition{
		Name:        TaskListTool,
		Description: taskListDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "status", Schema: tools.Node{
					Type: tools.TypeString,
					Enum: enum(
						string(agentteam.TaskPending), string(agentteam.TaskInProgress),
						string(agentteam.TaskCompleted),
					),
					Description: statusFilterDescription,
				}},
				{Name: "owner", Schema: tools.Node{
					Type: tools.TypeString, Description: ownerFilterDescription,
				}},
				{Name: "ready", Schema: tools.Node{
					Type: tools.TypeBoolean, Description: readyFilterDescription,
				}},
				{Name: "cursor", Schema: tools.Node{
					Type: tools.TypeInteger, Description: cursorDescription,
				}},
				{Name: "limit", Schema: tools.Node{
					Type: tools.TypeInteger, Description: limitDescription,
				}},
			},
		},
		Output:  jsonOutput[taskListWire](taskListValueSchema()),
		Execute: c.listTasks,
		PresentCall: func(json.RawMessage) tools.CallView {
			return present("List Team tasks", tools.CallRead, nil)
		},
	}
}

// taskListValueSchema 排出 team_task_list 的输出契约。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:125-132
func taskListValueSchema() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "tasks", Schema: tools.Node{Type: tools.TypeArray, Items: ptr(taskSchema())}},
			{Name: "nextCursor", Schema: tools.Node{Type: tools.TypeInteger}},
		},
		Required:             []string{"tasks"},
		AdditionalProperties: &closed,
	}
}

// keep 判一条任务过不过得了模型给的那三个筛子。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:327-329
func keep(view agentteam.TaskView, input listTaskArgs) bool {
	if input.Status != nil && string(view.Status) != *input.Status {
		return false
	}
	if input.Owner != nil {
		if *input.Owner == unownedFilter {
			if view.OwnerName != "" {
				return false
			}
		} else if view.OwnerName != *input.Owner {
			return false
		}
	}
	return input.Ready == nil || view.Ready == *input.Ready
}

// listTasks 是 team_task_list 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:324-338
//
// 次序跟着 DSH：先筛完再验翻页那两个数。看着别扭，但它决定了模型看见哪一句话——
// 反过来的话一次筛不出东西的调用会先报越界，而模型改的其实该是筛子。
func (c *Controller) listTasks(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, TaskListTool)
	if err != nil {
		return nil, err
	}
	var input listTaskArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	views, err := c.service.Tasks(ctx, who.Team)
	if err != nil {
		return nil, err
	}
	filtered := make([]taskWire, 0, len(views))
	for _, view := range views {
		if keep(view, input) {
			filtered = append(filtered, task(view))
		}
	}

	cursor := defaultCursor
	if input.Cursor != nil {
		value, ok := safeInteger(*input.Cursor)
		if !ok || value < 0 {
			return nil, &agentteam.Error{Code: agentteam.CodeInvalidArgument, Message: invalidCursor}
		}
		cursor = value
	}
	limit := defaultLimit
	if input.Limit != nil {
		value, ok := safeInteger(*input.Limit)
		if !ok || value < 1 || value > maxLimit {
			return nil, &agentteam.Error{Code: agentteam.CodeInvalidArgument, Message: invalidLimit}
		}
		limit = value
	}

	page := taskListWire{Tasks: []taskWire{}}
	if cursor < len(filtered) {
		end := min(cursor+limit, len(filtered))
		page.Tasks = filtered[cursor:end]
	}
	if cursor+limit < len(filtered) {
		page.NextCursor = ptr(cursor + limit)
	}
	return marshalNoEscape(page)
}

// newTaskGetTool 造那件 team_task_get 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:341-354
func (c *Controller) newTaskGetTool() *tools.Definition {
	return &tools.Definition{
		Name:        TaskGetTool,
		Description: taskGetDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name:   "task_id",
				Schema: tools.Node{Type: tools.TypeString, Description: taskIDDescription},
			}},
			Required: []string{"task_id"},
		},
		Output:  jsonOutput[taskWire](taskSchema()),
		Execute: c.getTask,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input getTaskArgs
			_ = json.Unmarshal(args, &input)
			return present("Read Team task", tools.CallRead, input.TaskID)
		},
	}
}

// getTask 是 team_task_get 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:348-353
func (c *Controller) getTask(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, TaskGetTool)
	if err != nil {
		return nil, err
	}
	var input getTaskArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	view, err := c.service.Task(ctx, who.Team, agentteam.TaskID(input.TaskID))
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(task(view))
}

// newTaskUpdateTool 造那件 team_task_update 工具。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:356-387
func (c *Controller) newTaskUpdateTool() *tools.Definition {
	return &tools.Definition{
		Name:        TaskUpdateTool,
		Description: taskUpdateDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "task_id", Schema: tools.Node{
					Type: tools.TypeString, Description: taskIDDescription,
				}},
				{Name: "expected_revision", Schema: tools.Node{
					Type: tools.TypeInteger, Description: expectedRevisionDescription,
				}},
				{Name: "action", Schema: tools.Node{
					Type:        tools.TypeString,
					Enum:        enum(actions()...),
					Description: actionDescription,
				}},
				{Name: "subject", Schema: tools.Node{
					Type: tools.TypeString, Description: editSubjectDescription,
				}},
				{Name: "description", Schema: tools.Node{
					Type: tools.TypeString, Description: editDetailDescription,
				}},
				{Name: "blocked_by", Schema: withDescription(stringArray(), dependenciesDescription)},
				{Name: "write_scopes", Schema: withDescription(stringArray(), editScopesDescription)},
				{Name: "owner", Schema: tools.Node{
					Type: tools.TypeString, Description: ownerDescription,
				}},
			},
			Required: []string{"task_id", "expected_revision", "action"},
		},
		Output:  jsonOutput[taskWire](taskSchema()),
		Execute: c.updateTask,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input updateTaskArgs
			_ = json.Unmarshal(args, &input)
			return present("Update Team task", tools.CallEdit, input.TaskID)
		},
	}
}

// actions 排出那八档动作。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:365
func actions() []string {
	return []string{
		string(agentteam.ActionClaim), string(agentteam.ActionRelease),
		string(agentteam.ActionEdit), string(agentteam.ActionSetDependencies),
		string(agentteam.ActionComplete), string(agentteam.ActionReopen),
		string(agentteam.ActionReassign), string(agentteam.ActionDelete),
	}
}

// updateTask 是 team_task_update 的体。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:375-386
//
// 版本号原样转手给服务：对不上归
// [github.com/snight1983/ds-harness-go/feature/agentteam.CodeTaskStaleRevision] 管，
// 这一层不预判。
func (c *Controller) updateTask(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	who, err := c.caller(ctx, exec, TaskUpdateTool)
	if err != nil {
		return nil, err
	}
	var input updateTaskArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	revision, ok := safeInteger(input.ExpectedRevision)
	if !ok {
		return nil, &agentteam.Error{Code: agentteam.CodeInvalidArgument, Message: invalidRevision}
	}
	request := agentteam.UpdateTaskRequest{
		TaskID:      agentteam.TaskID(input.TaskID),
		ExpectedRev: revision,
		Action:      agentteam.TaskAction(input.Action),
		Subject:     input.Subject,
		Description: input.Description,
		WriteScopes: input.WriteScopes,
	}
	if input.BlockedBy != nil {
		blockedBy := taskIDs(*input.BlockedBy)
		request.BlockedBy = &blockedBy
	}
	if input.Owner != nil {
		request.Owner = *input.Owner
	}
	view, err := c.service.UpdateTask(ctx, who, request)
	if err != nil {
		return nil, err
	}
	return marshalNoEscape(task(view))
}

// withDescription 给一份 schema 配上一句说明。
func withDescription(node tools.Node, description string) tools.Node {
	node.Description = description
	return node
}
