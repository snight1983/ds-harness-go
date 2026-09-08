// 本文件的作用：共享任务板上那八个动作，以及它们各自的授权与状态迁移。
//
// 源: packages/experimental/agent-team/src/task-board.ts

package agentteam

import (
	"context"
	"fmt"
	"sort"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TaskAction 是共享任务上那八个动作之一。
//
// 源: packages/experimental/agent-team/src/types.ts:166-175（TeamTaskAction）
type TaskAction string

const (
	// ActionClaim 是认领一条没人做的任务。
	ActionClaim TaskAction = "claim"
	// ActionRelease 是把认领的任务放回去。
	ActionRelease TaskAction = "release"
	// ActionEdit 是改标题、正文或者写入范围。
	ActionEdit TaskAction = "edit"
	// ActionSetDependencies 是整份替换前置。
	ActionSetDependencies TaskAction = "set_dependencies"
	// ActionComplete 是做完了。
	ActionComplete TaskAction = "complete"
	// ActionReopen 是把做完的重新打开。
	ActionReopen TaskAction = "reopen"
	// ActionReassign 是队长把一条任务指给某个人，或者收回来。
	ActionReassign TaskAction = "reassign"
	// ActionDelete 是删掉。
	ActionDelete TaskAction = "delete"
)

// CreateTaskRequest 是新建一条任务要给的东西。
//
// 源: packages/experimental/agent-team/src/types.ts:158-164
type CreateTaskRequest struct {
	// Subject 是一句话说清这条任务是什么。
	Subject string
	// Description 是展开说。
	Description string
	// BlockedBy 是挡在它前面的那几条。
	BlockedBy []TaskID
	// WriteScopes 是它打算改哪几处。
	WriteScopes []string
}

// UpdateTaskRequest 是改一条任务要给的东西。
//
// 源: packages/experimental/agent-team/src/types.ts:177-186
//
// 每个动作只看它自己那几个字段，别的一律忽略。带指针的三个是为了分开
// 「没给这个字段」和「给了一个空值」——[ActionEdit] 要靠这个知道调用方是想把正文改成
// 空（不许），还是根本没打算改正文。
type UpdateTaskRequest struct {
	// TaskID 是改哪一条。
	TaskID TaskID
	// ExpectedRev 是调用方手里那份任务的版本号，见 [Task.Rev]。
	ExpectedRev int
	// Action 是做哪个动作。
	Action TaskAction
	// Subject 只在 [ActionEdit] 时看。
	Subject *string
	// Description 只在 [ActionEdit] 时看。
	Description *string
	// WriteScopes 只在 [ActionEdit] 时看。
	WriteScopes *[]string
	// BlockedBy 只在 [ActionSetDependencies] 时看。
	BlockedBy *[]TaskID
	// Owner 只在 [ActionReassign] 时看；空串表示收回来，不指给任何人。
	Owner string
}

// Caller 是发起一次团队操作的那一位，由 [Service.ResolveCaller] 解算出来。
//
// 源: packages/experimental/agent-team/src/roster.ts:70-90（TeamMembership）
//
// 新增: DSH 那边这一位是一个活着的 Agent 对象。本包多副本，发起人只是一个身份加
// 一个名字——它此刻活在哪台机器上，跟板上这次改动没关系。要它是活的那几处
// （起队友、唤醒投递）各自另外去问 [Agents]。
type Caller struct {
	// Team 是哪支团队。
	Team TeamID
	// ID 是发起人那个会话的身份。
	ID sessionlog.SessionID
	// Name 是发起人的名字，队长是 [LeadName]。
	Name string
	// Lead 说这一位是不是队长。
	Lead bool
}

// CreateTask 在板上新建一条任务。
//
// 源: packages/experimental/agent-team/src/task-board.ts:41-72（create）
//
// 上限、发号、整图校验三件事在同一次条件写的回调里做，所以两个副本同时建任务不会
// 拿到同一个身份，也不会一起把板撑过上限。
func (s *Service) CreateTask(ctx context.Context, who Caller, request CreateTaskRequest) (TaskView, error) {
	subject, err := RequiredText(request.Subject, "任务标题", MaxSubjectLength)
	if err != nil {
		return TaskView{}, err
	}
	description, err := RequiredText(request.Description, "任务正文", MaxDescriptionLength)
	if err != nil {
		return TaskView{}, err
	}
	scopes, err := normalizeScopes(request.WriteScopes)
	if err != nil {
		return TaskView{}, err
	}

	var committed Task
	board, err := s.updateBoard(ctx, who.Team, func(board Board) (Board, error) {
		if activeTaskCount(board) >= s.config.MaxTasks {
			return board, newError(CodeTaskLimit, "这块板上的任务已经到 %d 条了", s.config.MaxTasks)
		}
		id := TaskID(fmt.Sprintf("task-%d", board.NextTaskNumber))
		if _, taken := board.Task(id); taken {
			return board, newError(CodeTaskLimit, "任务身份 %q 已经用过了", id)
		}
		blockedBy, err := resolveBlockers(board, request.BlockedBy, "")
		if err != nil {
			return board, err
		}
		task := Task{
			ID:          id,
			Rev:         1,
			Subject:     subject,
			Description: description,
			Status:      TaskPending,
			BlockedBy:   blockedBy,
			WriteScopes: scopes,
		}
		if err := ValidateGraph(board.Tasks, task); err != nil {
			return board, err
		}
		board.Tasks = append(board.Tasks, task)
		board.NextTaskNumber++
		committed = task
		return board, nil
	})
	if err != nil {
		return TaskView{}, err
	}
	return s.taskView(ctx, who.Team, board, committed)
}

// UpdateTask 在板上改一条任务。
//
// 源: packages/experimental/agent-team/src/task-board.ts:105-224（update）
//
// 版本号、授权、迁移、整图校验四件事全在同一次条件写的回调里做。这是本包把整块板
// 做成一条记录换来的：一次改动要同时看这条任务和它的邻居，而它们此刻都在手上。
func (s *Service) UpdateTask(ctx context.Context, who Caller, request UpdateTaskRequest) (TaskView, error) {
	var committed Task
	board, err := s.updateBoard(ctx, who.Team, func(board Board) (Board, error) {
		current, found := board.Task(request.TaskID)
		if !found {
			return board, newError(CodeTaskNotFound, "板上没有任务 %q", request.TaskID)
		}
		if current.Rev != request.ExpectedRev {
			return board, newError(CodeTaskStaleRevision,
				"任务 %q 你手里那份是第 %d 版，板上已经是第 %d 版了，重读一遍再来",
				current.ID, request.ExpectedRev, current.Rev)
		}
		if current.Status == TaskDeleted {
			return board, newError(CodeTaskDeleted, "任务 %q 已经删了", current.ID)
		}
		next, err := s.applyAction(ctx, board, who, current, request)
		if err != nil {
			return board, err
		}
		next.Rev = current.Rev + 1
		if err := ValidateGraph(board.Tasks, next); err != nil {
			return board, err
		}
		for index := range board.Tasks {
			if board.Tasks[index].ID == next.ID {
				board.Tasks[index] = next
				break
			}
		}
		committed = next
		return board, nil
	})
	if err != nil {
		return TaskView{}, err
	}
	return s.taskView(ctx, who.Team, board, committed)
}

// applyAction 把一个动作作用到一条任务上，给出改完之后那一份。
//
// 源: packages/experimental/agent-team/src/task-board.ts:121-215
//
// 它不改板，只算出新的那一条：版本号加一和写回板由 [Service.UpdateTask] 做，
// 好让每个分支只操心自己那件事。
//
// 它跑在板那次条件写的回调里，所以只有 [ActionReassign] 那一路会去读花名册；
// 撞上重跑时花名册跟着重读一遍，改派看到的在岗名单和落盘的那一版是同一时刻的。
func (s *Service) applyAction(ctx context.Context, board Board, who Caller, current Task, request UpdateTaskRequest) (Task, error) {
	byID := board.ByID()
	next := current.Clone()
	// 认领人或者队长——七个动作里有五个走这道，只有认领和改派例外。
	authorizeOwner := func() error {
		if who.Lead || current.Owner == who.ID {
			return nil
		}
		return newError(CodeTaskUnauthorized, "改任务 %q 要它的认领人或者队长的身份", current.ID)
	}

	switch request.Action {
	case ActionClaim:
		if current.Owner != "" && current.Owner != who.ID {
			return Task{}, newError(CodeTaskAlreadyClaimed, "任务 %q 已经有人认领了", current.ID)
		}
		if current.Status != TaskPending || !BlockersCleared(current, byID) {
			return Task{}, newError(CodeTaskBlocked, "任务 %q 还认领不了", current.ID)
		}
		next.Status = TaskInProgress
		next.Owner = who.ID

	case ActionRelease:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		if current.Status != TaskInProgress {
			return Task{}, newError(CodeTaskInvalidTransition, "只有正在做的任务才放得回去")
		}
		next.Status = TaskPending
		next.Owner = ""

	case ActionEdit:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		if request.Subject == nil && request.Description == nil && request.WriteScopes == nil {
			return Task{}, newError(CodeInvalidArgument, "改任务至少要给标题、正文或者写入范围里的一样")
		}
		if request.Subject != nil {
			subject, err := RequiredText(*request.Subject, "任务标题", MaxSubjectLength)
			if err != nil {
				return Task{}, err
			}
			next.Subject = subject
		}
		if request.Description != nil {
			description, err := RequiredText(*request.Description, "任务正文", MaxDescriptionLength)
			if err != nil {
				return Task{}, err
			}
			next.Description = description
		}
		if request.WriteScopes != nil {
			scopes, err := normalizeScopes(*request.WriteScopes)
			if err != nil {
				return Task{}, err
			}
			next.WriteScopes = scopes
		}

	case ActionSetDependencies:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		if request.BlockedBy == nil {
			return Task{}, newError(CodeInvalidArgument, "设置前置要给出那份名单，哪怕是空的")
		}
		blockedBy, err := resolveBlockers(board, *request.BlockedBy, current.ID)
		if err != nil {
			return Task{}, err
		}
		next.BlockedBy = blockedBy

	case ActionComplete:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		if current.Status != TaskInProgress {
			return Task{}, newError(CodeTaskInvalidTransition, "只有正在做的任务才做得完")
		}
		next.Status = TaskCompleted

	case ActionReopen:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		if current.Status != TaskCompleted {
			return Task{}, newError(CodeTaskInvalidTransition, "只有做完的任务才重新打得开")
		}
		next.Status = TaskPending
		next.Owner = ""

	case ActionReassign:
		if !who.Lead {
			return Task{}, newError(CodeLeadRequired, "只有队长能改派任务")
		}
		if current.Status != TaskPending && current.Status != TaskInProgress {
			return Task{}, newError(CodeTaskInvalidTransition, "只有还没做完的任务才改得了派给谁")
		}
		if request.Owner == "" {
			next.Status = TaskPending
			next.Owner = ""
			break
		}
		if !BlockersCleared(current, byID) {
			return Task{}, newError(CodeTaskBlocked, "任务 %q 的前置还没做完", current.ID)
		}
		roster, err := s.roster(ctx, board.Team)
		if err != nil {
			return Task{}, err
		}
		assignee, err := resolveActiveMember(roster, request.Owner)
		if err != nil {
			return Task{}, err
		}
		next.Status = TaskInProgress
		next.Owner = assignee

	case ActionDelete:
		if err := authorizeOwner(); err != nil {
			return Task{}, err
		}
		for _, other := range board.Tasks {
			if other.Status == TaskDeleted || other.ID == current.ID {
				continue
			}
			for _, blockerID := range other.BlockedBy {
				if blockerID == current.ID {
					return Task{}, newError(CodeTaskHasDependents, "任务 %q 还挡着 %q，删不得", current.ID, other.ID)
				}
			}
		}
		next.Status = TaskDeleted

	default:
		return Task{}, newError(CodeInvalidArgument, "不认得任务动作 %q", request.Action)
	}
	return next, nil
}

// resolveBlockers 校验并去重一份前置名单。
//
// 源: packages/experimental/agent-team/src/task-board.ts:227-249（dependencies）
//
// self 传自己那条的身份，好把「自己挡自己」单独说清；新建任务时传空串。
func resolveBlockers(board Board, values []TaskID, self TaskID) ([]TaskID, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[TaskID]bool, len(values))
	result := make([]TaskID, 0, len(values))
	for _, id := range values {
		if self != "" && id == self {
			return nil, newError(CodeTaskDependencyCycle, "任务 %q 不能挡自己", id)
		}
		if seen[id] {
			return nil, newError(CodeInvalidArgument, "前置 %q 写了两遍", id)
		}
		task, found := board.Task(id)
		if !found || task.Status == TaskDeleted {
			return nil, newError(CodeTaskNotFound, "前置任务 %q 不在板上", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

// normalizeScopes 归一化并去重一份写入范围名单。
//
// 源: packages/experimental/agent-team/src/task-board.ts:252-254（writeScopes）
func normalizeScopes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		scope, err := NormalizeWriteScope(value)
		if err != nil {
			return nil, err
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result, nil
}

// activeTaskCount 数板上没删掉的任务。
func activeTaskCount(board Board) int {
	count := 0
	for _, task := range board.Tasks {
		if task.Status != TaskDeleted {
			count++
		}
	}
	return count
}

// Tasks 列出板上没删掉的那些任务，按发号顺序。
//
// 源: packages/experimental/agent-team/src/task-board.ts（list）
//
// 删掉的那些不出现：它们留在板上只是为了不让身份被重发，见 [TaskDeleted]。
// 筛状态、筛认领人、翻页都不在这里——那几件是说给模型听的话，归工具那一层，
// 见 [github.com/snight1983/ds-harness-go/feature/agentteam/agentteamtool] 的 team_task_list。
func (s *Service) Tasks(ctx context.Context, team TeamID) ([]TaskView, error) {
	board, err := s.board(ctx, team)
	if err != nil {
		return nil, err
	}
	views := make([]TaskView, 0, len(board.Tasks))
	for _, task := range board.Tasks {
		if task.Status == TaskDeleted {
			continue
		}
		view, viewErr := s.taskView(ctx, team, board, task)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

// Task 读一条任务此刻的整份值。
//
// 源: packages/experimental/agent-team/src/task-board.ts:80-90（get）
//
// 删掉的那条也读得出来，而且状态如实报 [TaskDeleted]：模型手里可能攥着一个早先从
// team_task_list 上读到的身份，告诉它「这条删了」比告诉它「没有这条」准确。
func (s *Service) Task(ctx context.Context, team TeamID, id TaskID) (TaskView, error) {
	board, err := s.board(ctx, team)
	if err != nil {
		return TaskView{}, err
	}
	task, found := board.ByID()[id]
	if !found {
		return TaskView{}, newError(CodeTaskNotFound, "板上没有任务 %q", id)
	}
	return s.taskView(ctx, team, board, task)
}

// taskView 把一条任务折成给出去的样子：认领人的名字、就绪与否、写入范围撞了谁。
//
// 源: packages/experimental/agent-team/src/task-board.ts:270-297（taskView）
func (s *Service) taskView(ctx context.Context, team TeamID, board Board, task Task) (TaskView, error) {
	view := TaskView{Task: task.Clone()}
	byID := board.ByID()
	view.Ready = task.Status == TaskPending && BlockersCleared(task, byID)

	if task.Owner != "" {
		roster, err := s.roster(ctx, team)
		if err != nil {
			return TaskView{}, err
		}
		if string(task.Owner) == roster.Lead {
			view.OwnerName = LeadName
		} else if member, found := roster.MemberByID(string(task.Owner)); found {
			view.OwnerName = member.Name
		}
	}

	// 写入范围重叠**只是提醒**：它不拦任何一次改动，只把撞上的那几条说给模型听。
	// 只和正在做的任务比——一条还没人认领的任务说不上跟谁抢。
	warnings := make(map[string]bool)
	for _, other := range board.Tasks {
		if other.ID == task.ID || other.Status != TaskInProgress {
			continue
		}
		if scopesCollide(task.WriteScopes, other.WriteScopes) {
			warnings[fmt.Sprintf("写入范围和 %s 撞了", other.ID)] = true
		}
	}
	if len(warnings) > 0 {
		view.ScopeWarnings = make([]string, 0, len(warnings))
		for warning := range warnings {
			view.ScopeWarnings = append(view.ScopeWarnings, warning)
		}
		sort.Strings(view.ScopeWarnings)
	}
	return view, nil
}

// scopesCollide 说两份写入范围里有没有压在一起的。
func scopesCollide(left []string, right []string) bool {
	for _, one := range left {
		for _, other := range right {
			if ScopesOverlap(one, other) {
				return true
			}
		}
	}
	return false
}
