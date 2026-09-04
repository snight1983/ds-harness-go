// 本文件的作用：包私有的工作区实体——[Workspace] 唯一的那份实现，以及它写入落盘的那条唯一路径。
//
// 源: packages/workspace/workspace/src/entity.ts

package workspace

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// entityHost 是实体写入时要用到的那几件登记册自有的机械。
//
// 源: packages/workspace/workspace/src/entity.ts:34-63
//
// 实体**看不见登记册本身**，只看见：打开的那张表、[Workspace.SessionIDs] 投影
// 背后那份会话归属索引、挂载时的会话头读取、以及文件系统和时钟。
//
// 新增: DSH 那边这个接口是导出的（entity.ts 是一个独立模块），但包入口没有
// 再把它转发出去，所以消费方看不到它。Go 里一个包就是一层，不导出即可，
// 那次「导出但不转发」的动作在这里消失了。
type entityHost interface {
	// table 取到打开的那张 workspaces 表；登记册还没起来时报 [CodeNotStarted]。
	table() (*domain.Table[Record], error)
	// sessionWorkspaceID 从索引里读一个会话的归属工作区标识。
	//
	// 第二个返回值为假表示这个会话的头此刻取不到。
	//
	// 新增: DSH 这一位叫 sessionTargetKey，读的是「这个会话的工作目录解析出来的
	// 目标标识」。归属判据换成一次标识相等之后，这一位读的就是会话头上那个 id
	// 本身，一次文件系统调用都不发，见 [sessionlog.SessionHeader.WorkspaceID]。
	sessionWorkspaceID(id sessionlog.SessionID) (sessionlog.WorkspaceID, bool)
	// readSessionHeader 为挂载验证读一份会话头。
	//
	// 确凿地没有这个会话时报 [CodeUnknownSession]，后端故障时报 [CodeStorageFailed]。
	readSessionHeader(ctx context.Context, id sessionlog.SessionID) (sessionlog.SessionHeader, error)
	// fileSystem 取到这次装配的文件系统接缝。
	fileSystem() fs.FileSystem
	// now 取当前时刻，用来盖 [Record.UpdatedAt]。
	now() time.Time
	// beginWrite 声明「接下来这一次对 id 那条记录的写是登记册发起的」，
	// 返回撤销这次声明的函数。
	//
	// 新增: DSH 没有这一位。它是本包那条不变量的记账面，理由见 [RegisterInvariants]：
	// 实体缓存没了之后，「有人绕过登记册写了 workspaces 表」这件事只剩这一种
	// 不碰介质、也不会误报的判法。
	beginWrite(id WorkspaceID) func()
}

// errUnchanged 是写链上的中止哨兵：更新函数发现这条记录不需要改时抛它，
// 只有 [entity.mutate] 会看见。
//
// 源: packages/workspace/workspace/src/entity.ts:66
var errUnchanged = errors.New("workspace: 记录无需改动（内部哨兵）")

// entity 是 [Workspace] 唯一的实现，只由 [Registry] 构造。
//
// 源: packages/workspace/workspace/src/entity.ts:68-221（WorkspaceEntity）
//
// 新增: DSH 那边实体自己攥着一份 `record`。本包**不攥**：它只有一把表键，
// 每一次取值都现读一次介质。理由和 storage/domain 删掉内存权威态是同一条
// （见那个包 domain.go 开头）——一份留在进程里的副本，在另一个副本改了那条
// 记录之后就是错的，而它错了不会有任何一步报错。
//
// 少掉那份副本，也就少掉那把保护它的 [sync.RWMutex]：实体现在没有任何可变状态，
// 天然可以被多个 goroutine 同时用。
type entity struct {
	host entityHost
	id   WorkspaceID
}

// 编译期确认：实体真的满足对外那个接口。
var _ Workspace = (*entity)(nil)

// newEntity 造一个指着某条记录的句柄。
//
// 源: packages/workspace/workspace/src/entity.ts:77-83
//
// 新增: 它**不核对这条记录在不在**。句柄是一把键，不是一份存在性证明；
// 真正的答案在每一次读的那一刻由介质给出（[CodeWorkspaceGone]）。
// 造句柄时先探一次，只会白花一次往返，而且探完到用之间那条记录照样可能没。
func newEntity(host entityHost, id WorkspaceID) *entity {
	return &entity{host: host, id: id}
}

// read 现读一次这条工作区记录。
//
// 记录不在介质上时报 [CodeWorkspaceGone]，后端自己失败时报 [CodeStorageFailed]
// ——两者必须分得开，见 [CodeWorkspaceGone]。
func (e *entity) read(ctx context.Context) (Record, error) {
	table, err := e.host.table()
	if err != nil {
		return Record{}, err
	}
	record, found, err := table.Get(ctx, string(e.id))
	if err != nil {
		return Record{}, wrap(CodeStorageFailed, err, "读工作区 %q 失败", e.id)
	}
	if !found {
		return Record{}, fail(CodeWorkspaceGone, "工作区 %q 已经不在介质上了", e.id)
	}
	return record, nil
}

// ID 实现 [Workspace.ID]。
func (e *entity) ID() WorkspaceID { return e.id }

// TargetKey 实现 [Workspace.TargetKey]。
func (e *entity) TargetKey(ctx context.Context) (fs.TargetKey, error) {
	record, err := e.read(ctx)
	return record.TargetKey, err
}

// Path 实现 [Workspace.Path]。
//
// 源: packages/workspace/workspace/src/entity.ts:85-87
func (e *entity) Path(ctx context.Context) (string, error) {
	record, err := e.read(ctx)
	return record.DisplayPath, err
}

// Title 实现 [Workspace.Title]。
//
// 源: packages/workspace/workspace/src/entity.ts:89-91
func (e *entity) Title(ctx context.Context) (string, error) {
	record, err := e.read(ctx)
	return record.Title, err
}

// CreatedAt 实现 [Workspace.CreatedAt]。
//
// 源: packages/workspace/workspace/src/entity.ts:93-95
func (e *entity) CreatedAt(ctx context.Context) (time.Time, error) {
	record, err := e.read(ctx)
	return record.CreatedAt, err
}

// UpdatedAt 实现 [Workspace.UpdatedAt]。
//
// 源: packages/workspace/workspace/src/entity.ts:97-99
func (e *entity) UpdatedAt(ctx context.Context) (time.Time, error) {
	record, err := e.read(ctx)
	return record.UpdatedAt, err
}

// SessionIDs 实现 [Workspace.SessionIDs]：把落盘的候选账目筛一遍。
//
// 源: packages/workspace/workspace/src/entity.ts:101-103
//
// 交出来的是一份新切片，调用方拿去排序、追加都碰不到别处。
func (e *entity) SessionIDs(ctx context.Context) ([]sessionlog.SessionID, error) {
	record, err := e.read(ctx)
	if err != nil {
		return nil, err
	}
	return filterAccounted(e.host, e.id, record), nil
}

// filterAccounted 是那条归属判据：id 在账目里，且它会话头上的工作区标识就是本工作区。
//
// 源: packages/workspace/workspace/src/entity.ts:102
// 源: packages/workspace/workspace/src/entity.ts:207-209
//
// 抽出来是因为它在两处一字不差地出现：读投影时筛一遍，写落盘时裁一遍。
// 两处必须是同一条判据——否则读出来的和存下去的会慢慢分叉。
//
// 新增: workspaceID 是单传的形参而不是从 record 上取，因为 [Record] 里没有 id
// 这一项——那是它在表里的键。
func filterAccounted(host entityHost, workspaceID WorkspaceID, record Record) []sessionlog.SessionID {
	kept := make([]sessionlog.SessionID, 0, len(record.SessionIDs))
	for _, id := range record.SessionIDs {
		if owner, ok := host.sessionWorkspaceID(id); ok && owner == workspaceID {
			kept = append(kept, id)
		}
	}
	return kept
}

// SetTitle 实现 [Workspace.SetTitle]。
//
// 源: packages/workspace/workspace/src/entity.ts:105-107
func (e *entity) SetTitle(ctx context.Context, title string) error {
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if current.Title == title {
			return current, false, nil
		}
		current.Title = title
		return current, true, nil
	})
}

// AttachSession 实现 [Workspace.AttachSession]。
//
// 源: packages/workspace/workspace/src/entity.ts:109-149
//
// 已经在账目里时**跳过验证**：这条归属在它第一次挂上来时就判过，
// 而两个输入（落盘的会话头上那个工作区标识、本工作区的 id）都是不可变的。
// 归属本身仍然在写链上的那一刻决定（见 [entity.mutate]），不在这次预读上决定。
func (e *entity) AttachSession(ctx context.Context, sessionID sessionlog.SessionID) error {
	record, err := e.read(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(record.SessionIDs, sessionID) {
		if err := e.validateAttach(ctx, sessionID); err != nil {
			return err
		}
	}
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if slices.Contains(current.SessionIDs, sessionID) {
			return current, false, nil
		}
		current.SessionIDs = append([]sessionlog.SessionID{sessionID}, current.SessionIDs...)
		return current, true, nil
	})
}

// validateAttach 判一个还没挂上来的会话属不属于本工作区。
//
// 源: packages/workspace/workspace/src/entity.ts:115-144
//
// 新增: DSH 那边这里是三步——把会话头的 cwd realpath 一次、stat 一次确认它是个
// 存在的目录、再拿解析出来的目标标识和本工作区的比。本仓库的会话头记的不是位置
// 而是归属（见 [sessionlog.SessionHeader.WorkspaceID]），三步塌成一次相等，
// 一次文件系统调用都不发。
//
// 跟着消失的还有 [CodeStorageFailed] 这一支：它当初只可能从那次 stat 里来。
// 读会话头本身出故障仍然报它，那是 readSessionHeader 自己的事。
func (e *entity) validateAttach(ctx context.Context, sessionID sessionlog.SessionID) error {
	header, err := e.host.readSessionHeader(ctx, sessionID)
	if err != nil {
		return err
	}
	if header.WorkspaceID != e.id {
		return fail(CodeAttachRejected,
			"会话 %q 挂不到工作区 %q：它的会话头记的是工作区 %q",
			sessionID, e.id, header.WorkspaceID)
	}
	return nil
}

// InsertSessionBefore 实现 [Workspace.InsertSessionBefore]。
//
// 源: packages/workspace/workspace/src/entity.ts:151-172
//
// 整段判断都跑在写链上：账目里有没有这两个 id，是在**轮到它的那一刻**看的，
// 不是在调用方手上那份快照上看的。
func (e *entity) InsertSessionBefore(ctx context.Context, sessionID, beforeSessionID sessionlog.SessionID) error {
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if !slices.Contains(current.SessionIDs, sessionID) {
			return current, false, fail(CodeMoveInvalid,
				"会话 %q 在工作区 %q 里挪不动：它不在账目里", sessionID, current.DisplayPath)
		}
		if beforeSessionID != "" && !slices.Contains(current.SessionIDs, beforeSessionID) {
			return current, false, fail(CodeMoveInvalid,
				"会话 %q 在工作区 %q 里挪不到 %q 前面：那个锚点不在账目里",
				sessionID, current.DisplayPath, beforeSessionID)
		}
		if beforeSessionID == sessionID {
			return current, false, nil
		}
		next := insertBeforeSession(current.SessionIDs, sessionID, beforeSessionID)
		if slices.Equal(next, current.SessionIDs) {
			return current, false, nil
		}
		current.SessionIDs = next
		return current, true, nil
	})
}

// insertBeforeSession 把 id 从序列里摘出来，再插到锚点前面；锚点为空串就插到末尾。
//
// 源: packages/workspace/workspace/src/entity.ts:165-167
func insertBeforeSession(ids []sessionlog.SessionID, id, before sessionlog.SessionID) []sessionlog.SessionID {
	without := make([]sessionlog.SessionID, 0, len(ids))
	for _, candidate := range ids {
		if candidate != id {
			without = append(without, candidate)
		}
	}
	at := len(without)
	if before != "" {
		at = slices.Index(without, before)
	}
	return slices.Insert(without, at, id)
}

// DetachSession 实现 [Workspace.DetachSession]。
//
// 源: packages/workspace/workspace/src/entity.ts:174-178
func (e *entity) DetachSession(ctx context.Context, sessionID sessionlog.SessionID) error {
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if !slices.Contains(current.SessionIDs, sessionID) {
			return current, false, nil
		}
		next := make([]sessionlog.SessionID, 0, len(current.SessionIDs)-1)
		for _, candidate := range current.SessionIDs {
			if candidate != sessionID {
				next = append(next, candidate)
			}
		}
		current.SessionIDs = next
		return current, true, nil
	})
}

// Status 实现 [Workspace.Status]。
//
// 源: packages/workspace/workspace/src/entity.ts:180-188
//
// 新增: 这里的目标是拿落盘的那两个字段**直接拼**出来的，没有再走一次
// [fs.FileSystem.Resolve]。这正是 [fs.Target.TargetKey] 存在的意义——
// 它是一个跨进程稳定的身份，重新解析一遍只会白花一次 I/O，
// 而且目录被移走时那次解析会失败，反而查不出「目录不在了」这个结论。
func (e *entity) Status(ctx context.Context) (Status, error) {
	record, err := e.read(ctx)
	if err != nil {
		// 读不到记录和「目录不见了」是两件事，见 [Workspace.Status]。
		return "", err
	}
	info, found, err := e.host.fileSystem().Stat(ctx, fs.Target{
		TargetKey:   record.TargetKey,
		DisplayPath: record.DisplayPath,
	})
	if err != nil || !found || info.Type != fs.TypeDirectory {
		return StatusMissingDir, nil
	}
	return StatusOK, nil
}

// mutate 是唯一的写路径：在域的写链上跑 fn，顺手裁掉过不了归属判据的候选，
// 盖上 [Record.UpdatedAt]。
//
// 源: packages/workspace/workspace/src/entity.ts:190-220
//
// fn 看到的是**轮到它的那一刻**从介质上读回来的值，所以挂载/摘除的幂等判断
// 对排队中的写是无竞争的；别的副本插在读和写之间时 [domain.Table.Update] 会
// 拿着修订号重来一轮，fn 因此可能被跑不止一次，它不该有副作用。
//
// fn 用第二个返回值说「我什么都没改」，那时候如果裁剪也一无所获，
// 就抛 [errUnchanged] 中止这一格——一次真正的空操作既不重写介质，也不发变更事件。
//
// 新增: DSH 用 `changed === current` 这个引用相等来判断 fn 有没有改动。
// Go 里 [Record] 是结构体，赋值即复制，引用相等这个概念不存在；深比较又会把
// 「fn 改了但改成了同一个值」和「fn 没改」混为一谈。所以改成让 fn 自己说，
// 那也正是 DSH 那个 `return record` 想表达的意思，只是明说了出来。
func (e *entity) mutate(ctx context.Context, fn func(current Record) (Record, bool, error)) error {
	table, err := e.host.table()
	if err != nil {
		return err
	}

	// 声明这次写是登记册发起的，见 [entityHost.beginWrite]。变更事件在
	// [domain.Table.Update] 里同步发出，所以它一定落在这个标记还举着的时候。
	defer e.host.beginWrite(e.id)()

	_, err = table.Update(ctx, string(e.id), func(current Record) (Record, error) {
		changed, dirty, fnErr := fn(current)
		if fnErr != nil {
			return current, fnErr
		}
		kept := filterAccounted(e.host, e.id, changed)
		if !dirty && len(kept) == len(current.SessionIDs) {
			return current, errUnchanged
		}
		changed.SessionIDs = kept
		changed.UpdatedAt = e.host.now()
		return changed, nil
	})
	if err != nil {
		if errors.Is(err, errUnchanged) {
			return nil
		}
		var coded *Error
		if errors.As(err, &coded) {
			return err
		}
		// 记录在这次写之前就被别的副本删掉了：那是「这个工作区没了」，
		// 不是一次存储故障，见 [CodeWorkspaceGone]。
		var domainErr *domain.Error
		if errors.As(err, &domainErr) && domainErr.Code == domain.CodeMissingKey {
			return wrap(CodeWorkspaceGone, err, "工作区 %q 已经不在介质上了，写不下去", e.id)
		}
		return wrap(CodeStorageFailed, err, "工作区 %q 写不下去", e.id)
	}
	return nil
}
