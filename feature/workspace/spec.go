// 本文件的作用：工作区这个域的声明——一条记录长什么样、登记册的全局状态长什么样，
// 以及 [Registry] 打开的那份 [domain.Spec]。
//
// 源: packages/workspace/workspace/src/spec.ts

package workspace

import (
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// 域的身份：名字、格式版本、那张唯一的表。
//
// 源: packages/workspace/workspace/src/spec.ts:67-75
//
// 版本从 DSH 原样抄过来（2）。它不是从头数的：介质上盖着的版本号和它对不上
// 就打不开，而这三个值一起构成了「这份介质是不是本包写的」这个判断。
const (
	// DomainName 是这个域在存储中枢上的名字。
	DomainName = "workspace"
	// DomainVersion 是这个域的格式版本。
	DomainVersion = 2
	// TableName 是那张工作区记录表的名字。
	TableName = "workspaces"
)

// Record 是一条工作区记录落盘时的样子。
//
// 源: packages/workspace/workspace/src/spec.ts:30-31（WorkspaceRecord）
//
// 新增: DSH 那边只有一个 `path` 字段，同时充当身份和展示，因为它的范式是
// realpath 出来的那条本机绝对路径。本包把两者拆成 [Record.TargetKey] 和
// [Record.DisplayPath]，理由见包文档：远端后端的身份不是一条路径。
type Record struct {
	// TargetKey 是这个目录的身份，也是本包的唯一性范式。不透明，不许解析。
	TargetKey fs.TargetKey `json:"targetKey"`
	// DisplayPath 是给人看的那条路径，建的时候盖上，之后不重写。
	DisplayPath string `json:"displayPath"`
	// Title 是展示标题；允许和别的工作区重名，也允许是空串。
	Title string `json:"title"`
	// SessionIDs 是有序的候选账目，**数组顺序就是展示顺序**。
	//
	// 它是「候选」不是「成员」：真正的归属还要再过一遍会话头验证，见包文档。
	SessionIDs []sessionlog.SessionID `json:"sessionIds"`
	// CreatedAt 是建这条记录的时刻。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是最后一次落盘写入的时刻。
	UpdatedAt time.Time `json:"updatedAt"`
}

// Operation 是 [PendingMutation] 说的那一次未完成的写是哪一种。
//
// 源: packages/workspace/workspace/src/spec.ts:37-40
type Operation string

const (
	// OperationCreate 表示当时正在建一个工作区。
	OperationCreate Operation = "create"
	// OperationDelete 表示当时正在删一个工作区。
	OperationDelete Operation = "delete"
)

// PendingMutation 是一次「两次写」中途留下的可恢复标记。
//
// 源: packages/workspace/workspace/src/spec.ts:37-40
//
// 它在记录与次序**可能分叉之前**就先落盘，所以启动时分得清「一次被打断的登记册操作」
// 和「介质自己坏了」——后者没有任何解释，只能报错，见 [Registry.Open]。
//
// 新增: DSH 那边是一个按 operation 判别的联合，两个分支的载荷**完全一样**
// （都只有一个 workspaceId）。Go 里判别联合的对应物是密封接口，但那套机械
// 只有在分支载荷不同时才买得到东西；这里两个分支形状相同，所以塌成一个结构体，
// 判别字段就是普通字段。表达力一点没少：合法取值由 [PendingMutation.Validate] 守着。
type PendingMutation struct {
	// Operation 是那次没做完的写是建还是删。
	Operation Operation `json:"operation"`
	// WorkspaceID 是那次写针对的工作区。
	WorkspaceID WorkspaceID `json:"workspaceId"`
}

// DomainState 是登记册落盘的全局状态。
//
// 源: packages/workspace/workspace/src/spec.ts:59-60（WorkspaceDomainState）
type DomainState struct {
	// Initialized 区分「一个合法的空登记册」和「还没做过那一次历史 bootstrap」。
	//
	// 少了它，一个真的没有任何工作区的部署每次启动都会重跑一遍 bootstrap。
	Initialized bool `json:"initialized"`
	// WorkspaceIDs 是权威的展示次序。
	WorkspaceIDs []WorkspaceID `json:"workspaceIds"`
	// ArchivedSessionIDs 是登记册全局的归档集合，叠在工作区账目**之上**。
	//
	// 一个被归档的会话**保留**它在 [Record.SessionIDs] 里的位置（取消归档要能
	// 还原到原位），所以这个集合从不参与「一个会话只能归一个工作区」那条不变量。
	ArchivedSessionIDs []sessionlog.SessionID `json:"archivedSessionIds"`
	// PendingMutation 是那次没做完的写；nil 表示上一次停机时没有悬着的写。
	//
	// 新增: DSH 是可选属性，Go 里对应物是指针。这里不能用「零值即缺席」那一套：
	// 一个零值的 [PendingMutation] 会被 [Registry.Open] 当成一次真的待恢复操作，
	// 然后去删一个 id 为空串的记录。
	PendingMutation *PendingMutation `json:"pendingMutation,omitempty"`
}

// initialDomainState 是这个域第一次被写之前供出去的值。
//
// 源: packages/workspace/workspace/src/spec.ts:72
//
// Initialized 为假是这里的全部意义：它让第一次打开知道自己还欠一次历史 bootstrap。
func initialDomainState() DomainState {
	return DomainState{
		Initialized:        false,
		WorkspaceIDs:       []WorkspaceID{},
		ArchivedSessionIDs: []sessionlog.SessionID{},
	}
}

// Validate 校验一条工作区记录。
//
// 源: packages/workspace/workspace/src/spec.ts:21-27（那份 zod schema）
//
// 新增: zod 的 `z.object({path: z.string(), ...})` 会拒掉**缺字段**，但收下空串。
// Go 把 JSON 解进结构体的时候，缺字段和空值塌成同一个零值，分不开。所以这里的
// 分界线改成按「这个值能不能用」来划：
//
//   - TargetKey、DisplayPath 空 → 拒。一条不知道自己指着哪个目录的记录没法比、
//     没法 Stat，留着它只会让下一个读到它的人去猜。
//   - CreatedAt、UpdatedAt 是零时刻 → 拒。bootstrap 拿它排序，一个零时刻会把这条
//     记录永远排到最后，而那是一次静默的错序，不是一次报错。
//   - Title 空 → 收。空标题在 DSH 那边就是合法的（`z.string()` 收空串），
//     而一个没有标题的工作区仍然完全可用。
//   - SessionIDs 里有空串 → 拒。空串不是合法的会话 id，它进了账目就会永远
//     筛不出去，也永远删不掉（调用方拿不到那个 id）。
func (r Record) Validate() error {
	if r.TargetKey == "" {
		return fmt.Errorf("工作区记录没有目标标识")
	}
	if r.DisplayPath == "" {
		return fmt.Errorf("工作区记录没有展示路径")
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("工作区记录没有创建时刻")
	}
	if r.UpdatedAt.IsZero() {
		return fmt.Errorf("工作区记录没有更新时刻")
	}
	for index, id := range r.SessionIDs {
		if id == "" {
			return fmt.Errorf("工作区记录的候选账目第 %d 项是空的会话 id", index)
		}
	}
	return nil
}

// Validate 校验一条待恢复标记。
//
// 源: packages/workspace/workspace/src/spec.ts:43-57（workspaceDomainState）
func (p PendingMutation) Validate() error {
	if p.Operation != OperationCreate && p.Operation != OperationDelete {
		return fmt.Errorf("待恢复标记的操作 %q 不在 create/delete 里", p.Operation)
	}
	if p.WorkspaceID == "" {
		return fmt.Errorf("待恢复标记没有说是哪个工作区")
	}
	return nil
}

// Validate 校验登记册的全局状态。
//
// 源: packages/workspace/workspace/src/spec.ts:51-56
//
// 这里只查**这一份值自己**说得通不说得通。「次序里的 id 在表里有没有」那类
// 跨表检查在 [Registry] 的状态校验里，因为域这一层的校验函数拿不到那张表。
func (s DomainState) Validate() error {
	for index, id := range s.WorkspaceIDs {
		if id == "" {
			return fmt.Errorf("登记册次序第 %d 项是空的工作区 id", index)
		}
	}
	for index, id := range s.ArchivedSessionIDs {
		if id == "" {
			return fmt.Errorf("归档集合第 %d 项是空的会话 id", index)
		}
	}
	if s.PendingMutation != nil {
		return s.PendingMutation.Validate()
	}
	return nil
}

// Spec 是工作区这个域的静态声明：一张 workspaces 表加一个全局槽。
//
// 源: packages/workspace/workspace/src/spec.ts:67-75
//
// 新增: DSH 那边 `workspaceDomainSpec` 是一个模块级的常量对象。这里是函数，
// 因为 [domain.DefineGlobal] 收的 initial 是一个**值**，而这个值里带着切片——
// 一个包级变量会让所有调用方共用同一个底层数组。函数每次现造一份，
// 谁也改不到谁。
func Spec() domain.Spec {
	return domain.Spec{
		Name:    DomainName,
		Version: DomainVersion,
		Global: domain.DefineGlobal(initialDomainState(), func(state DomainState) error {
			return state.Validate()
		}),
		Tables: []domain.TableSpec{
			domain.DefineTable(TableName, func(record Record) error {
				return record.Validate()
			}),
		},
	}
}
