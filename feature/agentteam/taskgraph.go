// 本文件的作用：任务之间那张前置关系图的整图校验。
//
// 源: packages/experimental/agent-team/src/task-graph.ts

package agentteam

import "fmt"

// GraphViolation 是前置关系图上被拒掉的那种关系。
//
// 源: packages/experimental/agent-team/src/task-graph.ts:5-6
//
// 分成三种而不是一律「非法依赖」，是因为它们对模型的意思不一样：少一条前置是
// 「你写的那个 id 不存在」，重复是「你把同一条写了两遍」，成圈是「这几条互相等」。
// 三者各自对应本包的一个 [Code]，见 [GraphViolation.Code]。
type GraphViolation string

const (
	// ViolationMissing 是引用的前置不在板上，或者已经删了。
	ViolationMissing GraphViolation = "missing"
	// ViolationDuplicate 是同一条任务把同一条前置写了两遍。
	ViolationDuplicate GraphViolation = "duplicate"
	// ViolationCycle 是这几条任务互相等，包括自己等自己。
	ViolationCycle GraphViolation = "cycle"
)

// Code 是这种违规对应的失败分类。
//
// 源: packages/experimental/agent-team/src/task-board.ts:24-31（违规到错误码的映射）
func (v GraphViolation) Code() Code {
	switch v {
	case ViolationMissing:
		return CodeTaskNotFound
	case ViolationCycle:
		return CodeTaskDependencyCycle
	default:
		return CodeInvalidArgument
	}
}

// GraphError 是一次整图校验没过。
//
// 源: packages/experimental/agent-team/src/task-graph.ts:8-17（TeamTaskGraphError）
type GraphError struct {
	// Violation 是哪一种违规。
	Violation GraphViolation
	// Message 是给人读的那句话。
	Message string
}

func (e *GraphError) Error() string {
	return fmt.Sprintf("agentteam: %s（%s）", e.Message, e.Violation)
}

// Is 让 errors.Is(err, CodeTaskDependencyCycle) 这种写法直接成立，
// 不必先把违规译成码。
func (e *GraphError) Is(target error) bool {
	code, ok := target.(Code)
	return ok && code == e.Violation.Code()
}

// ValidateGraph 把一条候选任务放进当前这批任务里，然后校验整张图。
//
// 源: packages/experimental/agent-team/src/task-graph.ts:20-69（assertTaskGraphCandidate）
//
// 校验的是**整张图**而不只是被改的那一条，理由是一次改动可以从远处把图弄坏：
// 把 A 的前置指向 B，坏掉的可能是 C→A→B→C 这个圈，而 C 那条这次根本没动。
//
// 删掉的任务不参与：它们既不算前置，也不检查自己的前置。所以删一条任务会让指向它
// 的那些变成「少一条前置」，这正是删除那条路要先查 [CodeTaskHasDependents] 的原因。
//
// candidate 传零值（[Task.ID] 为空）表示只校验现有这批，不放新的进去。
func ValidateGraph(current []Task, candidate Task) error {
	tasks := make(map[TaskID]Task, len(current)+1)
	for _, task := range current {
		tasks[task.ID] = task
	}
	if candidate.ID != "" {
		tasks[candidate.ID] = candidate
	}

	for _, task := range tasks {
		if task.Status == TaskDeleted {
			continue
		}
		seen := make(map[TaskID]bool, len(task.BlockedBy))
		for _, blockerID := range task.BlockedBy {
			if blockerID == task.ID {
				return &GraphError{Violation: ViolationCycle, Message: fmt.Sprintf("任务 %q 不能挡自己", task.ID)}
			}
			if seen[blockerID] {
				return &GraphError{Violation: ViolationDuplicate, Message: fmt.Sprintf("任务 %q 把前置 %q 写了两遍", task.ID, blockerID)}
			}
			blocker, found := tasks[blockerID]
			if !found || blocker.Status == TaskDeleted {
				return &GraphError{Violation: ViolationMissing, Message: fmt.Sprintf("任务 %q 的前置 %q 不在板上或者已经删了", task.ID, blockerID)}
			}
			seen[blockerID] = true
		}
	}

	// 一趟带路径标记的深搜找圈。visiting 是当前这条路上的，visited 是已经确认没问题
	// 的——没有 visited 的话，一张宽的图会被重复走成指数次。
	visiting := make(map[TaskID]bool, len(tasks))
	visited := make(map[TaskID]bool, len(tasks))
	var visit func(id TaskID) error
	visit = func(id TaskID) error {
		if visiting[id] {
			return &GraphError{Violation: ViolationCycle, Message: fmt.Sprintf("任务前置绕成了一个圈，%q 在圈上", id)}
		}
		if visited[id] {
			return nil
		}
		task, found := tasks[id]
		if !found || task.Status == TaskDeleted {
			return nil
		}
		visiting[id] = true
		for _, blockerID := range task.BlockedBy {
			if err := visit(blockerID); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	// 按身份排过序再走，好让同一张坏图每次报的是同一条边——否则 map 的随机顺序会让
	// 一个失败的用例每次说不同的话。
	for _, task := range sortTasks(tasks) {
		if err := visit(task.ID); err != nil {
			return err
		}
	}
	return nil
}

// BlockersCleared 说一条任务的前置是不是都做完了。
//
// 源: packages/experimental/agent-team/src/task-board.ts:246-248（taskReady）
//
// 它只看前置，不看这条任务自己的状态——「能不能认领」还要求它是
// [TaskPending]，那一层判断在认领那条路上。
func BlockersCleared(task Task, byID map[TaskID]Task) bool {
	for _, blockerID := range task.BlockedBy {
		blocker, found := byID[blockerID]
		if !found || blocker.Status != TaskCompleted {
			return false
		}
	}
	return true
}
