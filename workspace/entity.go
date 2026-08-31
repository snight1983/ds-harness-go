// 本文件的作用：包私有的工作区实体——[Workspace] 唯一的那份实现，以及它写入落盘的那条唯一路径。
//
// 源: packages/workspace/workspace/src/entity.ts

package workspace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"ds-harness-go/fs"
	"ds-harness-go/session"
	"ds-harness-go/storage/domain"
)

// entityHost 是实体写入时要用到的那几件登记册自有的机械。
//
// 源: packages/workspace/workspace/src/entity.ts:34-63
//
// 实体**看不见登记册本身**，只看见：打开的那张表、[Workspace.SessionIDs] 投影
// 背后那份会话目标索引、挂载时的会话头读取、以及文件系统和时钟。
//
// 新增: DSH 那边这个接口是导出的（entity.ts 是一个独立模块），但包入口没有
// 再把它转发出去，所以消费方看不到它。Go 里一个包就是一层，不导出即可，
// 那次「导出但不转发」的动作在这里消失了。
type entityHost interface {
	// table 取到打开的那张 workspaces 表；登记册还没起来时报 [CodeNotStarted]。
	table() (*domain.Table[Record], error)
	// sessionTargetKey 从索引里读一个会话所在目录的目标标识。
	//
	// 第二个返回值为假表示这个会话的头没有、或者它的工作目录识别不出一个存在的目录。
	sessionTargetKey(id session.SessionID) (fs.TargetKey, bool)
	// readSessionHeader 为挂载验证读一份会话头。
	//
	// 确凿地没有这个会话时报 [CodeUnknownSession]，后端故障时报 [CodeStorageFailed]。
	readSessionHeader(ctx context.Context, id session.SessionID) (session.SessionHeader, error)
	// rememberSessionTarget 把一次刚验过的目标写进投影索引。
	rememberSessionTarget(id session.SessionID, target fs.Target)
	// fileSystem 取到这次装配的文件系统接缝。
	fileSystem() fs.FileSystem
	// now 取当前时刻，用来盖 [Record.UpdatedAt]。
	now() time.Time
}

// errUnchanged 是写链上的中止哨兵：更新函数发现这条记录不需要改时抛它，
// 只有 [entity.mutate] 会看见。
//
// 源: packages/workspace/workspace/src/entity.ts:66
var errUnchanged = errors.New("workspace: 记录无需改动（内部哨兵）")

// entity 是 [Workspace] 唯一的实现，只由 [Registry] 构造。
//
// 源: packages/workspace/workspace/src/entity.ts:69-221
//
// 新增: DSH 那边 `record` 是一个普通的私有字段，因为 JS 是单线程的。
// Go 里它被读方法和写路径同时碰，所以加一把 [sync.RWMutex]：
// 读方法拿读锁取一份快照，[entity.mutate] 在落盘成功之后拿写锁换掉它。
// 锁**不覆盖**落盘那一段——那会把域的写链和这把锁串成一条更长的临界区，
// 而次序本来就由域的写链保证。
type entity struct {
	host entityHost
	id   WorkspaceID

	mu     sync.RWMutex
	record Record
}

// 编译期确认：实体真的满足对外那个接口。
var _ Workspace = (*entity)(nil)

// newEntity 造一个实体。record 是刚读回来或者刚写下去的那份校验过的快照。
//
// 源: packages/workspace/workspace/src/entity.ts:77-83
func newEntity(host entityHost, id WorkspaceID, record Record) *entity {
	return &entity{host: host, id: id, record: record}
}

// snapshot 取一份当前记录的浅快照。
//
// 切片是共享的：调用方**不许**原地改它，这条和 [domain.Table] 上那条同源
// （记录是不可变数据，要换就走写路径）。本包内部的用法都只读它。
func (e *entity) snapshot() Record {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.record
}

// ID 实现 [Workspace.ID]。
func (e *entity) ID() WorkspaceID { return e.id }

// TargetKey 实现 [Workspace.TargetKey]。
func (e *entity) TargetKey() fs.TargetKey { return e.snapshot().TargetKey }

// Path 实现 [Workspace.Path]。
//
// 源: packages/workspace/workspace/src/entity.ts:85-87
func (e *entity) Path() string { return e.snapshot().DisplayPath }

// Title 实现 [Workspace.Title]。
//
// 源: packages/workspace/workspace/src/entity.ts:89-91
func (e *entity) Title() string { return e.snapshot().Title }

// CreatedAt 实现 [Workspace.CreatedAt]。
//
// 源: packages/workspace/workspace/src/entity.ts:93-95
func (e *entity) CreatedAt() time.Time { return e.snapshot().CreatedAt }

// UpdatedAt 实现 [Workspace.UpdatedAt]。
//
// 源: packages/workspace/workspace/src/entity.ts:97-99
func (e *entity) UpdatedAt() time.Time { return e.snapshot().UpdatedAt }

// SessionIDs 实现 [Workspace.SessionIDs]：把候选账目同步地筛一遍。
//
// 源: packages/workspace/workspace/src/entity.ts:101-103
//
// 交出来的是一份新切片，不是记录里那一条：调用方拿去排序、追加都碰不到内部状态。
func (e *entity) SessionIDs() []session.SessionID {
	record := e.snapshot()
	return filterAccounted(e.host, record)
}

// filterAccounted 是那条归属判据：id 在账目里，且它的目标标识等于本工作区的。
//
// 源: packages/workspace/workspace/src/entity.ts:102
// 源: packages/workspace/workspace/src/entity.ts:207-209
//
// 抽出来是因为它在两处一字不差地出现：读投影时筛一遍，写落盘时裁一遍。
// 两处必须是同一条判据——否则读出来的和存下去的会慢慢分叉。
func filterAccounted(host entityHost, record Record) []session.SessionID {
	kept := make([]session.SessionID, 0, len(record.SessionIDs))
	for _, id := range record.SessionIDs {
		if key, ok := host.sessionTargetKey(id); ok && key == record.TargetKey {
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
// 已经在快照的账目里时**跳过验证**：那个工作目录事实在它第一次挂上来时就查过，
// 而两个输入（落盘的会话头 cwd、工作区的目标标识）都是不可变的。
// 归属本身仍然在写链上的那一刻决定（见 [entity.mutate]），不在这份快照上决定。
func (e *entity) AttachSession(ctx context.Context, sessionID session.SessionID) error {
	record := e.snapshot()
	if !slices.Contains(record.SessionIDs, sessionID) {
		if err := e.validateAttach(ctx, record, sessionID); err != nil {
			return err
		}
	}
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if slices.Contains(current.SessionIDs, sessionID) {
			return current, false, nil
		}
		current.SessionIDs = append([]session.SessionID{sessionID}, current.SessionIDs...)
		return current, true, nil
	})
}

// validateAttach 查一个还没挂上来的会话的工作目录事实。
//
// 源: packages/workspace/workspace/src/entity.ts:115-144
//
// 新增: DSH 那边只把 realpath 包在 try 里，`stat` 抛出来的会原样往上冒。
// 这里把两类分开报：后端自己出故障是 [CodeStorageFailed]，
// 「解析得出来但不是一个存在的目录」是 [CodeAttachRejected]。
// 分开是有必要的——前者调用方重试一次可能就好了，后者重试多少次都一样。
func (e *entity) validateAttach(ctx context.Context, record Record, sessionID session.SessionID) error {
	header, err := e.host.readSessionHeader(ctx, sessionID)
	if err != nil {
		return err
	}
	if header.Cwd == "" {
		return fail(CodeAttachRejected,
			"会话 %q 挂不到工作区 %q：它落盘的会话头里没有工作目录，无从验证",
			sessionID, record.DisplayPath)
	}
	target, err := e.host.fileSystem().Resolve(ctx, header.Cwd, "")
	if err != nil {
		return wrap(CodeAttachRejected, err,
			"会话 %q 挂不到工作区 %q：它的工作目录 %q 解析不出来，无从验证",
			sessionID, record.DisplayPath, header.Cwd)
	}
	info, found, err := e.host.fileSystem().Stat(ctx, target)
	if err != nil {
		return wrap(CodeStorageFailed, err,
			"验证会话 %q 的工作目录 %q 时文件系统出错", sessionID, header.Cwd)
	}
	if !found || info.Type != fs.TypeDirectory {
		return fail(CodeAttachRejected,
			"会话 %q 挂不到工作区 %q：它的工作目录 %q 不是一个存在的目录",
			sessionID, record.DisplayPath, header.Cwd)
	}
	if target.TargetKey != record.TargetKey {
		return fail(CodeAttachRejected,
			"会话 %q 挂不到工作区 %q：它的工作目录落在 %q",
			sessionID, record.DisplayPath, target.DisplayPath)
	}
	e.host.rememberSessionTarget(sessionID, target)
	return nil
}

// InsertSessionBefore 实现 [Workspace.InsertSessionBefore]。
//
// 源: packages/workspace/workspace/src/entity.ts:151-172
//
// 整段判断都跑在写链上：账目里有没有这两个 id，是在**轮到它的那一刻**看的，
// 不是在调用方手上那份快照上看的。
func (e *entity) InsertSessionBefore(ctx context.Context, sessionID, beforeSessionID session.SessionID) error {
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
func insertBeforeSession(ids []session.SessionID, id, before session.SessionID) []session.SessionID {
	without := make([]session.SessionID, 0, len(ids))
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
func (e *entity) DetachSession(ctx context.Context, sessionID session.SessionID) error {
	return e.mutate(ctx, func(current Record) (Record, bool, error) {
		if !slices.Contains(current.SessionIDs, sessionID) {
			return current, false, nil
		}
		next := make([]session.SessionID, 0, len(current.SessionIDs)-1)
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
func (e *entity) Status(ctx context.Context) Status {
	record := e.snapshot()
	info, found, err := e.host.fileSystem().Stat(ctx, fs.Target{
		TargetKey:   record.TargetKey,
		DisplayPath: record.DisplayPath,
	})
	if err != nil || !found || info.Type != fs.TypeDirectory {
		return StatusMissingDir
	}
	return StatusOK
}

// mutate 是唯一的写路径：在域的写链上跑 fn，顺手裁掉过不了归属判据的候选，
// 盖上 [Record.UpdatedAt]，然后换掉本地快照。
//
// 源: packages/workspace/workspace/src/entity.ts:190-220
//
// fn 看到的是**轮到它的那一刻**的值，所以挂载/摘除的幂等判断对排队中的写是
// 无竞争的。fn 用第二个返回值说「我什么都没改」，那时候如果裁剪也一无所获，
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

	next, err := table.Update(ctx, string(e.id), func(current Record) (Record, error) {
		changed, dirty, fnErr := fn(current)
		if fnErr != nil {
			return current, fnErr
		}
		kept := filterAccounted(e.host, changed)
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
		return wrap(CodeStorageFailed, err, "工作区 %q 写不下去", e.id)
	}

	e.mu.Lock()
	e.record = next
	e.mu.Unlock()
	return nil
}
