// 本文件的作用：团队状态落在介质上的三张表，以及本包打开的那份 [domain.Spec]。
//
// 新增: DSH 那边没有对应文件——它的团队状态是从队长那条会话日志折出来的一份内存
// 投影（packages/experimental/agent-team/src/projection.ts）。落到介质上是本包的
// 分歧点，理由见包文档。

package agentteam

import (
	"fmt"
	"sort"

	"github.com/snight1983/ds-harness-go/storage/domain"
)

// 域的身份：名字、格式版本、那三张表。
//
// 版本从 1 起数：这个域没有 DSH 对应物，所以没有要对齐的历史版本号。
const (
	// DomainName 是这个域在存储中枢上的名字。
	DomainName = "agent_team"
	// DomainVersion 是这个域的格式版本。
	DomainVersion = 1
	// RosterTable 是花名册那张表，一支团队一条记录。
	RosterTable = "teams"
	// BoardTable 是任务板那张表，一支团队一条记录。
	BoardTable = "boards"
	// MessageTable 是消息那张表，一条消息一条记录。
	MessageTable = "messages"
)

// Roster 是一支团队的花名册落盘时的整份值。
//
// 整支团队的成员是**一条**记录而不是一人一条，理由和任务板一样：起一个队友要同时
// 查重名、查人数上限、再追一行，三件事必须看同一份值。
// [github.com/snight1983/ds-harness-go/storage/domain] 不提供跨记录事务，
// 所以把它们收进一条记录，一次条件写就是一次原子改动。
type Roster struct {
	// Team 是这支团队的身份，等于记录键。
	Team TeamID `json:"team"`
	// Lead 是队长那个会话的身份。
	Lead string `json:"lead"`
	// Members 是队友们，按加入次序排。
	Members []Member `json:"members,omitempty"`
}

// Validate 校验一份花名册。
func (r Roster) Validate() error {
	if r.Team == "" {
		return fmt.Errorf("花名册没有团队身份")
	}
	if r.Lead == "" {
		return fmt.Errorf("花名册没有队长")
	}
	names := make(map[string]bool, len(r.Members))
	ids := make(map[string]bool, len(r.Members))
	for _, member := range r.Members {
		if member.ID == "" {
			return fmt.Errorf("花名册上有一行没有会话身份")
		}
		if _, err := ValidateMemberName(member.Name); err != nil {
			return fmt.Errorf("花名册上的名字 %q 不合式：%w", member.Name, err)
		}
		if names[member.Name] {
			return fmt.Errorf("花名册上有两个队友都叫 %q", member.Name)
		}
		if ids[string(member.ID)] {
			return fmt.Errorf("花名册上有两行都指向会话 %q", member.ID)
		}
		switch member.Phase {
		case PhaseProvisioning, PhaseActive, PhaseFailed:
		default:
			return fmt.Errorf("队友 %q 的状态 %q 不认得", member.Name, member.Phase)
		}
		switch member.Context {
		case ContextFresh, ContextFork:
		default:
			return fmt.Errorf("队友 %q 的上文来源 %q 不认得", member.Name, member.Context)
		}
		if member.Provider == "" {
			return fmt.Errorf("队友 %q 没有说用哪个提供方起的", member.Name)
		}
		names[member.Name] = true
		ids[string(member.ID)] = true
	}
	return nil
}

// Member 按名字找一行。
func (r Roster) Member(name string) (Member, bool) {
	for _, member := range r.Members {
		if member.Name == name {
			return member, true
		}
	}
	return Member{}, false
}

// MemberByID 按会话身份找一行。
func (r Roster) MemberByID(id string) (Member, bool) {
	for _, member := range r.Members {
		if string(member.ID) == id {
			return member, true
		}
	}
	return Member{}, false
}

// Clone 深复制一份花名册，好让改它那次回调不会改到原来那份。
func (r Roster) Clone() Roster {
	r.Members = append([]Member(nil), r.Members...)
	return r
}

// Board 是一支团队的任务板落盘时的整份值。
//
// **整块板是一条记录**。这是本包最要紧的一个取舍：
//
//	一条任务一条记录   改一条任务只写一条，板大了也不互相挤；代价是任务之间的边要
//	                   放进另一张表，而改一条任务往往要同时改它和它的邻居——
//	                   [github.com/snight1983/ds-harness-go/storage/domain] 没有跨表
//	                   事务，于是那两次写之间会有一个图不成立的瞬间。
//	整块板一条记录     边住在任务值里面（[Task.BlockedBy]），改一条任务就是对一把键
//	                   的一次条件写，整图校验（[ValidateGraph]）在回调里对内存中的板
//	                   跑一遍，跨任务的原子性白拿。
//
// 选后者。代价是每次改都要读写整块板，而且并发改板会撞条件写——所以有
// [Config.MaxTasks] 这道上限，也所以撞穿之后报的是 [CodeBoardContended]。
type Board struct {
	// Team 是这块板属于哪支团队，等于记录键。
	Team TeamID `json:"team"`
	// Tasks 是板上的任务，删掉的也在里面（见 [TaskDeleted]）。
	Tasks []Task `json:"tasks,omitempty"`
	// NextTaskNumber 是下一条任务的序号，从 1 起数。
	//
	// 它单独记而不是从 len(Tasks) 算：删掉的任务仍占着自己那个序号，
	// 照长度算会让一个已经被别人引用过的身份重新发出去。
	NextTaskNumber int `json:"nextTaskNumber"`
}

// Validate 校验一块板。
func (b Board) Validate() error {
	if b.Team == "" {
		return fmt.Errorf("任务板没有团队身份")
	}
	if b.NextTaskNumber < 1 {
		return fmt.Errorf("任务板的下一个序号 %d 不合法", b.NextTaskNumber)
	}
	ids := make(map[TaskID]bool, len(b.Tasks))
	for _, task := range b.Tasks {
		if task.ID == "" {
			return fmt.Errorf("任务板上有一条任务没有身份")
		}
		if ids[task.ID] {
			return fmt.Errorf("任务板上有两条任务都叫 %q", task.ID)
		}
		if task.Rev < 1 {
			return fmt.Errorf("任务 %q 的版本号 %d 不合法", task.ID, task.Rev)
		}
		switch task.Status {
		case TaskPending, TaskInProgress, TaskCompleted, TaskDeleted:
		default:
			return fmt.Errorf("任务 %q 的状态 %q 不认得", task.ID, task.Status)
		}
		if task.Subject == "" {
			return fmt.Errorf("任务 %q 没有标题", task.ID)
		}
		ids[task.ID] = true
	}
	// 整图这一道放在这里而不只放在改板那条路上，是为了让一份从介质读回来的板也过一遍：
	// 一份被别的版本写坏的板要在装载时就说出来，不能等到某次校验恰好走到它。
	if err := ValidateGraph(b.Tasks, Task{}); err != nil {
		return fmt.Errorf("任务板上的前置关系不成立：%w", err)
	}
	return nil
}

// Task 按身份找一条。
func (b Board) Task(id TaskID) (Task, bool) {
	for _, task := range b.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

// ByID 把板上的任务折成一张按身份索引的表，供 [BlockersCleared] 和整图校验用。
func (b Board) ByID() map[TaskID]Task {
	byID := make(map[TaskID]Task, len(b.Tasks))
	for _, task := range b.Tasks {
		byID[task.ID] = task
	}
	return byID
}

// Clone 深复制一块板，好让改它那次回调不会改到原来那份。
func (b Board) Clone() Board {
	tasks := make([]Task, len(b.Tasks))
	for index, task := range b.Tasks {
		tasks[index] = task.Clone()
	}
	b.Tasks = tasks
	return b
}

// Validate 校验一条消息记录。
func (m Message) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("消息没有身份")
	}
	if m.Team == "" {
		return fmt.Errorf("消息 %q 没有团队身份", m.ID)
	}
	if m.SenderID == "" || m.TargetID == "" {
		return fmt.Errorf("消息 %q 没说清是谁发给谁", m.ID)
	}
	if m.SenderID == m.TargetID {
		return fmt.Errorf("消息 %q 发给了自己", m.ID)
	}
	if m.SenderName == "" {
		return fmt.Errorf("消息 %q 没有发信人的名字", m.ID)
	}
	switch m.Delivery {
	case DeliveryQuiet, DeliveryWakeup:
	default:
		return fmt.Errorf("消息 %q 的送法 %q 不认得", m.ID, m.Delivery)
	}
	switch m.Phase {
	case MessageQueued:
		if m.Claimant != "" {
			return fmt.Errorf("消息 %q 排着队却记着认领人", m.ID)
		}
	case MessageClaimed:
		if m.Claimant == "" || m.ClaimedAt == 0 {
			return fmt.Errorf("消息 %q 被认领了却没说是谁、什么时候", m.ID)
		}
	case MessageDelivered:
	default:
		return fmt.Errorf("消息 %q 的状态 %q 不认得", m.ID, m.Phase)
	}
	if len(m.Content) == 0 {
		return fmt.Errorf("消息 %q 没有正文", m.ID)
	}
	if m.QueuedAt == 0 {
		return fmt.Errorf("消息 %q 没有排队时刻", m.ID)
	}
	return nil
}

// Expired 说一次认领是不是已经超时了，超时的认领可以被别的副本捡走。
//
// 新增: 捡回掉队认领的做法与
// [github.com/snight1983/ds-harness-go/adapter/domainjobs] 那边一致——抓着一件活的
// 副本可能整个没了，没有任何一条通道会告诉别人这件事，只能靠一个时刻加一段宽限期。
func (m Message) Expired(nowUnixMilli int64, ttlMillis int64) bool {
	return m.Phase == MessageClaimed && nowUnixMilli-m.ClaimedAt >= ttlMillis
}

// Spec 是团队这个域的静态声明：三张表，没有全局槽。
//
// 没有全局槽是有意的：这个域的全部状态就是「有哪几支团队、各自的板和消息」，
// 而那正好是三张表自己。
//
// 新增: 是函数不是包级变量，理由同
// [github.com/snight1983/ds-harness-go/feature/authorization.Spec]——[domain.Spec]
// 里带着切片，一个包级变量会让所有调用方共用同一个底层数组。
func Spec() domain.Spec {
	return domain.Spec{
		Name:    DomainName,
		Version: DomainVersion,
		Tables: []domain.TableSpec{
			domain.DefineTable(RosterTable, func(roster Roster) error { return roster.Validate() }),
			domain.DefineTable(BoardTable, func(board Board) error { return board.Validate() }),
			domain.DefineTable(MessageTable, func(message Message) error { return message.Validate() }),
		},
	}
}

// sortTasks 把一张按身份索引的任务表折成按身份排好序的一串。
//
// 新增: Go 的 map 遍历顺序是随机的，而整图校验要在一张坏图上每次报同一条边——
// 否则一个失败的用例每次说不同的话，排障时分不清是两个问题还是一个。
func sortTasks(byID map[TaskID]Task) []Task {
	tasks := make([]Task, 0, len(byID))
	for _, task := range byID {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(left, right int) bool { return tasks[left].ID < tasks[right].ID })
	return tasks
}
