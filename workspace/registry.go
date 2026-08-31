// 本文件的作用：登记册本身——打开、崩溃恢复、历史 bootstrap、次序、归档，
// 以及实体写入时要用到的那几件登记册自有的机械。
//
// 源: packages/workspace/workspace/src/index.ts

package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"ds-harness-go/fs"
	"ds-harness-go/session"
	"ds-harness-go/storage/domain"
)

// Config 是打开一个 [Registry] 需要的东西。
//
// 源: packages/workspace/workspace/src/index.ts:93（`static inject`）、:114-116
//
// 新增: DSH 那边这些全从 cordis 容器上取（`this.ctx.storageDomain`、
// `this.ctx.sessionPersistence`、`this.ctx.get('sessions')`、`this.ctx.logger`）。
// Go 里没有那个容器，装配方把它们显式填进来。哪些必需、哪些可空，
// 照抄 DSH 的 inject 与 ctx.get 之分：inject 的必需，ctx.get 的可空。
type Config struct {
	// Domain 是域设施，登记册从它那里打开自己那个域。必填。
	Domain *domain.Facility

	// Persistence 是已落地会话的只读列举面。必填。
	//
	// 源: packages/workspace/workspace/src/index.ts:93（inject 里的 sessionPersistence）
	//
	// **不能为 nil**，理由见 [Persistence]：拿不到它的时候，「历史是空的」
	// 这个结论会被当真，然后把 initialized 标记盖下去，从此那些会话再也收不进来。
	Persistence Persistence

	// FS 是文件系统接缝，路径的唯一性范式由它拥有。必填。
	//
	// 新增: DSH 直接用 node:fs 的 realpath 和 stat，没有这个口子。
	// 本包为什么改成走接缝，见包文档。
	FS fs.FileSystem

	// Live 是此刻活着的那些会话；nil 表示这次装配没有活会话表。
	//
	// 源: packages/workspace/workspace/src/index.ts:264,593,616（`this.ctx.get('sessions')`）
	//
	// 可空是照抄 DSH 的 `ctx.get`：没有它的时候本包只看已落地的那些会话。
	Live LiveSessions

	// NewID 生成新工作区的 id；留空回落到 uuid。
	//
	// 源: packages/workspace/workspace/src/index.ts:293,458（`randomUUID()`）
	NewID func() string

	// Now 取当前时刻；留空回落到 [time.Now]。
	//
	// 源: packages/workspace/workspace/src/index.ts:294,486（`new Date()`）
	Now func() time.Time

	// Logger 记「候选被筛掉了」「删成了但标记没清掉」这类事，留空用 slog.Default()。
	//
	// 留空**不是**丢弃：这里记的正是没人会主动去查、却必须留下痕迹的那类事。
	// 要静音的装配方显式递一个装着 slog.DiscardHandler 的 logger。
	Logger *slog.Logger
}

// Registry 是工作区登记册。
//
// 源: packages/workspace/workspace/src/index.ts:85-658
//
// 零值不可用，请用 [Open]。
//
// 新增: DSH 那边写操作靠一条 promise 链（`operationTail`，index.ts:102,648-657）
// 串起来，因为 JS 是单线程的，那条链就是它的互斥。Go 里对应物是
// [Registry.opMutex]——一把把「建/删/挪位/归档」串成一列的锁。
//
// 两把锁分工不同，不许合并：opMutex 串的是**一次完整的登记册操作**（它中间要落好几次盘），
// mutex 保的是那几张内存表。合并的话，一次落盘的整个时长里读方法全被挡住，
// 而 [Workspace.SessionIDs] 这样的读会在域的写链上被回调（见 [filterAccounted]），
// 于是一次写会等自己持有的锁——死锁。
type Registry struct {
	facility    *domain.Facility
	persistence Persistence
	filesystem  fs.FileSystem
	live        LiveSessions
	newID       func() string
	clock       func() time.Time
	logger      *slog.Logger

	dom       *domain.Domain
	workspace *domain.Table[Record]
	global    *domain.Global[DomainState]

	// opMutex 把登记册的写操作串成一列，顶替 DSH 的 operationTail。
	opMutex sync.Mutex

	// mutex 保下面这几张内存表和 state。
	mutex sync.RWMutex
	// started 为假表示还没打开或者已经关了，此时一切使用都报 [CodeNotStarted]。
	started bool
	state   DomainState
	// entities 是「工作区 id → 实体」的缓存，和落盘的那张表互为镜像。
	//
	// 源: packages/workspace/workspace/src/index.ts:98
	entities map[WorkspaceID]*entity
	// headers 是会话头索引，键是会话 id。
	//
	// 源: packages/workspace/workspace/src/index.ts:99
	headers map[session.SessionID]session.SessionHeader
	// sessionTargets 是「会话 id → 它工作目录解析出来的目标」，归属判据读这张表。
	//
	// 源: packages/workspace/workspace/src/index.ts:100（sessionPaths）
	//
	// 新增: DSH 存的是一条规范化过的路径串，本包存整个 [fs.Target]：
	// 比的时候用 [fs.Target.TargetKey]，bootstrap 建记录时还要用
	// [fs.Target.DisplayPath] 填 [Record.DisplayPath]。存一个 key 就得再解析一次
	// 才能拿到展示路径，而那次解析完全可能给出和这次不同的结果。
	sessionTargets map[session.SessionID]fs.Target
	// invalidSessions 记「这个会话的工作目录为什么用不了」，只用于诊断日志。
	//
	// 源: packages/workspace/workspace/src/index.ts:101（invalidSessionPaths）
	invalidSessions map[session.SessionID]string
}

// 编译期确认：登记册真的能当实体的宿主。
var _ entityHost = (*Registry)(nil)

// Open 打开登记册：开域 → 完成被标记点名的那次写 → 校验介质 → 必要时跑一次历史
// bootstrap → 建好实体缓存。
//
// 源: packages/workspace/workspace/src/index.ts:119-140（`[Service.init]`）
//
// 返回时内存态已经和介质一致，读可以立刻开始。中途任何一步失败都会把已经开出来的域
// 关掉——一个开着却没人持有的域会一直占着那个域名。
//
// 新增: DSH 那边这段逻辑在 `[Service.init]` 里，由 cordis 在服务变活之前调用；
// 域的关闭挂在 `ctx.effect` 上。Go 里打开是显式的这一个函数，关闭是显式的
// [Registry.Close]。
func Open(ctx context.Context, config Config) (*Registry, error) {
	if config.Domain == nil {
		return nil, fail(CodeInvalidConfig, "打开工作区登记册需要一个域设施")
	}
	if config.Persistence == nil {
		// 见 [Config.Persistence]：这个依赖是必需的，缺了它一次列举失败会被
		// 当成「历史是空的」。
		return nil, fail(CodeInvalidConfig, "打开工作区登记册需要一个会话持久化列举面")
	}
	if config.FS == nil {
		return nil, fail(CodeInvalidConfig, "打开工作区登记册需要一个文件系统")
	}

	registry := &Registry{
		facility:        config.Domain,
		persistence:     config.Persistence,
		filesystem:      config.FS,
		live:            config.Live,
		newID:           config.NewID,
		clock:           config.Now,
		logger:          config.Logger,
		entities:        map[WorkspaceID]*entity{},
		headers:         map[session.SessionID]session.SessionHeader{},
		sessionTargets:  map[session.SessionID]fs.Target{},
		invalidSessions: map[session.SessionID]string{},
	}
	if registry.newID == nil {
		registry.newID = uuid.NewString
	}
	if registry.clock == nil {
		registry.clock = time.Now
	}
	if registry.logger == nil {
		registry.logger = slog.Default()
	}

	opened, err := config.Domain.Open(ctx, Spec())
	if err != nil {
		return nil, wrap(CodeStorageFailed, err, "打不开工作区域")
	}
	if err := registry.start(ctx, opened); err != nil {
		// 开出来的域这条路上没人再会用它，不关就是一个一直占着域名的句柄。
		// 关闭本身的失败**不覆盖**原因：调用方要看的是介质为什么用不了。
		if closeErr := opened.Close(ctx); closeErr != nil {
			registry.logger.Warn("workspace: 打开失败后关闭域也失败",
				slog.Any("error", closeErr))
		}
		return nil, err
	}
	return registry, nil
}

// start 是 [Open] 里域开出来之后的那一段，单拎出来是为了让失败路径只写一次关域。
//
// 源: packages/workspace/workspace/src/index.ts:122-139
func (r *Registry) start(ctx context.Context, opened *domain.Domain) error {
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		return wrap(CodeStorageFailed, err, "工作区域里取不到 %q 表", TableName)
	}
	global, err := domain.GlobalOf[DomainState](opened)
	if err != nil {
		return wrap(CodeStorageFailed, err, "工作区域里取不到全局槽")
	}
	state, err := global.Get()
	if err != nil {
		return wrap(CodeStorageFailed, err, "读不到工作区域的全局状态")
	}

	r.mutex.Lock()
	r.dom = opened
	r.workspace = table
	r.global = global
	r.state = state
	r.started = true
	r.mutex.Unlock()

	if err := r.recoverPendingMutation(ctx); err != nil {
		return err
	}
	if err := r.validateStoredState(r.snapshotState()); err != nil {
		return err
	}

	if !r.snapshotState().Initialized {
		headers, listErr := r.persistence.List(ctx)
		if listErr != nil {
			return wrap(CodeStorageFailed, listErr, "列举已落地会话失败，无法完成历史 bootstrap")
		}
		r.replaceHeaderIndex(ctx, headers)
		if err := r.bootstrap(ctx, headers); err != nil {
			return err
		}
	} else {
		size, sizeErr := table.Size()
		if sizeErr != nil {
			return wrap(CodeStorageFailed, sizeErr, "读不到工作区表的记录条数")
		}
		if size > 0 {
			// 一个已经 bootstrap 过、却一条工作区都没有的登记册不需要会话头索引：
			// 没有记录就没有候选账目要筛，那次列举纯属白花一次 I/O。
			headers, listErr := r.persistence.List(ctx)
			if listErr != nil {
				return wrap(CodeStorageFailed, listErr, "列举已落地会话失败")
			}
			r.replaceHeaderIndex(ctx, headers)
		}
	}

	r.indexLiveSessions(ctx)
	if err := r.validateStoredState(r.snapshotState()); err != nil {
		return err
	}
	if err := r.rebuildEntities(); err != nil {
		return err
	}
	r.reportFilteredCandidates()
	return nil
}

// Close 关掉登记册：关域，然后把自己标成没启动。
//
// 新增: DSH 那边域的关闭挂在 `ctx.effect(() => () => domain.close())`
// （index.ts:121）上，由 cordis 在插件卸载时调用。Go 里由拿到句柄的人显式调。
//
// **幂等**：已经关了的登记册再关一次是空操作。
func (r *Registry) Close(ctx context.Context) error {
	r.mutex.Lock()
	if !r.started {
		r.mutex.Unlock()
		return nil
	}
	r.started = false
	opened := r.dom
	r.entities = map[WorkspaceID]*entity{}
	r.mutex.Unlock()

	if err := opened.Close(ctx); err != nil {
		return wrap(CodeStorageFailed, err, "关闭工作区域失败")
	}
	return nil
}

// Create 为一个已存在的目录建一个工作区，或者交出已经在那个目录上的那一个。
//
// 源: packages/workspace/workspace/src/index.ts:158-164
//
// 路径先过一遍 [fs.FileSystem.Resolve]，解析不出来或者不是目录一律报
// [CodeInvalidPath]。同一个目标标识重复调用交出已有的那一个，**不改它的标题**。
// 新建的工作区排在展示次序最前面。
//
// title 留空时取展示路径的最后一段。不同目录允许重名。
func (r *Registry) Create(ctx context.Context, path string, title string) (Workspace, error) {
	target, err := r.resolveDirectory(ctx, path)
	if err != nil {
		return nil, err
	}
	var created Workspace
	err = r.enqueue(ctx, func() error {
		entity, createErr := r.createCanonical(ctx, target, title)
		if createErr != nil {
			return createErr
		}
		created = entity
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// Get 按 id 找一个工作区；第二个返回值为假表示这个 id 不认识。
//
// 源: packages/workspace/workspace/src/index.ts:171-173
func (r *Registry) Get(id WorkspaceID) (Workspace, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	found, ok := r.entities[id]
	if !ok {
		return nil, false
	}
	return found, true
}

// List 按落盘的展示次序同步地交出全部工作区，**不碰持久化**。
//
// 源: packages/workspace/workspace/src/index.ts:181-189
//
// 每个实体的 [Workspace.SessionIDs] 已经被会话头索引筛过了，这里不再做任何过滤。
//
// 新增: DSH 那边 list() 是同步的，次序里指到一个不存在的工作区时直接 throw。
// Go 里返回 [CodeInconsistentState]——那确实是一次介质不一致，而不是一个空列表。
func (r *Registry) List() ([]Workspace, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	if !r.started {
		return nil, r.notStarted()
	}
	list := make([]Workspace, 0, len(r.state.WorkspaceIDs))
	for _, id := range r.state.WorkspaceIDs {
		found, ok := r.entities[id]
		if !ok {
			return nil, fail(CodeInconsistentState,
				"登记册次序指向一个缓存里没有的工作区 %q", id)
		}
		list = append(list, found)
	}
	return list, nil
}

// Delete 删掉一条工作区登记，**目录和所有会话日志一条不动**。
//
// 源: packages/workspace/workspace/src/index.ts:199-201
//
// 返回值是「删之前它在不在」。不认识的 id 是幂等的空操作。
func (r *Registry) Delete(ctx context.Context, id WorkspaceID) (bool, error) {
	deleted := false
	err := r.enqueue(ctx, func() error {
		var deleteErr error
		deleted, deleteErr = r.deleteKnown(ctx, id)
		return deleteErr
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// InsertBefore 在落盘的展示次序里挪一个工作区，语义同 DOM 的 insertBefore。
//
// 源: packages/workspace/workspace/src/index.ts:210-225
//
// 给了锚点就落在它前面，锚点为空串就挪到末尾。工作区或锚点不在次序里的，
// 报 [CodeOrderInvalid] 且不写。返回提交之后的完整次序。
//
// 新增: beforeID 用空串表示「没给锚点」，和 [Workspace.InsertSessionBefore] 同一条约定。
func (r *Registry) InsertBefore(ctx context.Context, id, beforeID WorkspaceID) ([]WorkspaceID, error) {
	var order []WorkspaceID
	err := r.enqueue(ctx, func() error {
		state := r.snapshotState()
		if !slices.Contains(state.WorkspaceIDs, id) {
			return fail(CodeOrderInvalid, "挪不动工作区 %q：它不在登记册次序里", id)
		}
		if beforeID != "" && !slices.Contains(state.WorkspaceIDs, beforeID) {
			return fail(CodeOrderInvalid, "挪不到工作区 %q 前面：那个锚点不在登记册次序里", beforeID)
		}
		if beforeID == id {
			order = slices.Clone(state.WorkspaceIDs)
			return nil
		}
		next := insertBeforeWorkspace(state.WorkspaceIDs, id, beforeID)
		if slices.Equal(next, state.WorkspaceIDs) {
			order = slices.Clone(state.WorkspaceIDs)
			return nil
		}
		state.WorkspaceIDs = next
		if err := r.setState(ctx, state); err != nil {
			return err
		}
		order = slices.Clone(next)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// insertBeforeWorkspace 把 id 从次序里摘出来再插到锚点前面；锚点为空串就插到末尾。
//
// 源: packages/workspace/workspace/src/index.ts:218-220
func insertBeforeWorkspace(ids []WorkspaceID, id, before WorkspaceID) []WorkspaceID {
	without := make([]WorkspaceID, 0, len(ids))
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

// ArchivedSessionIDs 是登记册全局的归档集合，按归档顺序。
//
// 源: packages/workspace/workspace/src/index.ts:233-235
//
// 归档**从不动工作区账目**：一个被归档的会话保留它在 [Record.SessionIDs] 里的位置，
// 取消归档才能还原到原位。
func (r *Registry) ArchivedSessionIDs() []session.SessionID {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return slices.Clone(r.state.ArchivedSessionIDs)
}

// ArchiveSession 落盘地归档一个会话。
//
// 源: packages/workspace/workspace/src/index.ts:244-255
//
// 这个会话必须存在（活着的，或者已落地的）；它归不归某个工作区无关紧要。
// 已经归档过的 id 直接返回，不写。会话确凿地不存在时报 [CodeUnknownSession]——
// 持久化后端自己出故障时报的是那条原始错误，绝不塌成这个码。
func (r *Registry) ArchiveSession(ctx context.Context, sessionID session.SessionID) error {
	return r.enqueue(ctx, func() error {
		// 操作串成一列，所以这里的「先查后写」不会和另一次归档交错。
		state := r.snapshotState()
		if slices.Contains(state.ArchivedSessionIDs, sessionID) {
			return nil
		}
		known, err := r.sessionKnown(ctx, sessionID)
		if err != nil {
			return err
		}
		if !known {
			return fail(CodeUnknownSession,
				"归档不了会话 %q：活会话和会话持久化里都没有它", sessionID)
		}
		state = r.snapshotState()
		state.ArchivedSessionIDs = append(slices.Clone(state.ArchivedSessionIDs), sessionID)
		return r.setState(ctx, state)
	})
}

// ResolveByPath 按目录找工作区，**不建也不改**任何东西。
//
// 源: packages/workspace/workspace/src/index.ts:277-283
//
// 路径解析不出来报 [CodeInvalidPath]；解析得出来但没人认领的目录，
// 第二个返回值为假。
func (r *Registry) ResolveByPath(ctx context.Context, path string) (Workspace, bool, error) {
	target, err := r.filesystem.Resolve(ctx, path, "")
	if err != nil {
		return nil, false, wrap(CodeInvalidPath, err, "路径 %q 解析不出来", path)
	}
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	for _, id := range r.state.WorkspaceIDs {
		found, ok := r.entities[id]
		if ok && found.snapshot().TargetKey == target.TargetKey {
			return found, true, nil
		}
	}
	return nil, false, nil
}

// resolveDirectory 把一条路径解析成一个**确实存在的目录**的目标。
//
// 源: packages/workspace/workspace/src/index.ts:159-162
//
// 新增: DSH 用 realpath 加 stat，两处失败都原样往上抛。这里三种失败都归到
// [CodeInvalidPath]——它们对调用方是同一件事：这条路径不能拿来建工作区。
// 唯独后端自己出故障（Stat 报错）归 [CodeStorageFailed]，那是可以重试的。
func (r *Registry) resolveDirectory(ctx context.Context, path string) (fs.Target, error) {
	target, err := r.filesystem.Resolve(ctx, path, "")
	if err != nil {
		return fs.Target{}, wrap(CodeInvalidPath, err, "路径 %q 解析不出来，建不了工作区", path)
	}
	info, found, err := r.filesystem.Stat(ctx, target)
	if err != nil {
		return fs.Target{}, wrap(CodeStorageFailed, err, "查路径 %q 时文件系统出错", target.DisplayPath)
	}
	if !found || info.Type != fs.TypeDirectory {
		return fs.Target{}, fail(CodeInvalidPath, "路径 %q 不是一个存在的目录，建不了工作区", target.DisplayPath)
	}
	return target, nil
}

// createCanonical 是建工作区那条**可恢复的两次写**，必须在 [Registry.enqueue] 里跑。
//
// 源: packages/workspace/workspace/src/index.ts:285-356
//
// 三步，每一步失败都把前面的撤干净：
//
//  1. 先写待恢复标记（记录与次序还没分叉，此时崩了两边都还是老样子）；
//  2. 写记录（崩了：标记还在，启动时按标记删掉这条孤儿记录）；
//  3. 写次序并清掉标记（崩了：同上）。
//
// 实体在**落盘之前**就进缓存，这是本包那条不变量的前提：一次针对 workspaces 表的
// put 事件发出时，缓存里必须已经有对应的实体（见 [RegisterInvariants]）。
//
// 新增: 回滚也失败的场合，DSH 抛 AggregateError（index.ts:321-325 等三处），
// Go 里用 errors.Join 把两条挂到 [Error.Cause] 上。两者是同一件事：
// 原本的失败和善后的失败谁都不许被另一个盖掉。
func (r *Registry) createCanonical(ctx context.Context, target fs.Target, title string) (*entity, error) {
	if existing, ok := r.entityByTargetKey(target.TargetKey); ok {
		return existing, nil
	}

	table, err := r.table()
	if err != nil {
		return nil, err
	}
	state := r.snapshotState()
	id := WorkspaceID(r.newID())
	now := r.now()
	name := title
	if name == "" {
		name = defaultTitle(target.DisplayPath)
	}
	record := Record{
		TargetKey:   target.TargetKey,
		DisplayPath: target.DisplayPath,
		Title:       name,
		SessionIDs:  []session.SessionID{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	created := newEntity(r, id, record)
	r.putEntity(id, created)

	pending := state
	pending.PendingMutation = &PendingMutation{Operation: OperationCreate, WorkspaceID: id}
	if err := r.setState(ctx, pending); err != nil {
		r.dropEntity(id)
		return nil, err
	}

	if err := table.Put(ctx, string(id), record); err != nil {
		r.dropEntity(id)
		if rollbackErr := r.setState(ctx, state); rollbackErr != nil {
			return nil, wrap(CodeStorageFailed, errors.Join(err, rollbackErr),
				"工作区 %q 的记录写入和待恢复标记回滚都失败了", id)
		}
		return nil, wrap(CodeStorageFailed, err, "工作区 %q 的记录写不下去", id)
	}

	committed := DomainState{
		Initialized:        true,
		WorkspaceIDs:       append([]WorkspaceID{id}, state.WorkspaceIDs...),
		ArchivedSessionIDs: state.ArchivedSessionIDs,
	}
	if err := r.setState(ctx, committed); err != nil {
		r.dropEntity(id)
		if _, rollbackErr := table.Delete(ctx, string(id)); rollbackErr != nil {
			return nil, wrap(CodeStorageFailed, errors.Join(err, rollbackErr),
				"工作区 %q 的次序写入和记录回滚都失败了；待恢复标记还在，启动时会接着收尾", id)
		}
		if rollbackErr := r.setState(ctx, state); rollbackErr != nil {
			return nil, wrap(CodeStorageFailed, errors.Join(err, rollbackErr),
				"工作区 %q 的次序写入和待恢复标记回滚都失败了", id)
		}
		return nil, wrap(CodeStorageFailed, err, "工作区 %q 的次序写不下去", id)
	}
	return created, nil
}

// deleteKnown 是删工作区那条可恢复的两次写，必须在 [Registry.enqueue] 里跑。
//
// 源: packages/workspace/workspace/src/index.ts:358-401
//
// 次序**先于**记录删除更新，所以一次失败的记录删除可以把次序还原回去、把实体重新发布出来。
// 记录删掉之后清标记那一步失败**不报错**：删除在表写入那一刻就已经提交、也已经广播出去了，
// 在请求的效果已经成真之后再报失败，只会让调用方去撤一件撤不掉的事。标记留着，
// 下一次启动或者下一次操作会把它收掉。
func (r *Registry) deleteKnown(ctx context.Context, id WorkspaceID) (bool, error) {
	r.mutex.RLock()
	found, ok := r.entities[id]
	r.mutex.RUnlock()
	if !ok {
		return false, nil
	}

	table, err := r.table()
	if err != nil {
		return false, err
	}
	state := r.snapshotState()
	next := DomainState{
		Initialized:        true,
		WorkspaceIDs:       slices.DeleteFunc(slices.Clone(state.WorkspaceIDs), func(candidate WorkspaceID) bool { return candidate == id }),
		ArchivedSessionIDs: state.ArchivedSessionIDs,
	}
	marked := next
	marked.PendingMutation = &PendingMutation{Operation: OperationDelete, WorkspaceID: id}
	if err := r.setState(ctx, marked); err != nil {
		return false, err
	}

	// 实体在记录删除**之前**离开缓存，这是本包那条不变量的另一半：
	// 缓存还在发布它的时候宣布它被删了，说明有人绕过了登记册。
	r.dropEntity(id)
	if _, err := table.Delete(ctx, string(id)); err != nil {
		r.putEntity(id, found)
		if rollbackErr := r.setState(ctx, state); rollbackErr != nil {
			// 落盘的标记仍然说「这次删除要继续做完」，缓存必须站在那个可恢复的方向上，
			// 而不是重新发布一条已经不在落盘次序里的记录。
			r.dropEntity(id)
			return false, wrap(CodeStorageFailed, errors.Join(err, rollbackErr),
				"工作区 %q 的记录删除和登记册次序回滚都失败了", id)
		}
		return false, wrap(CodeStorageFailed, err, "工作区 %q 的记录删不掉", id)
	}

	if err := r.setState(ctx, next); err != nil {
		r.logger.Warn("workspace: 工作区删掉了，但它的待恢复标记没能清掉",
			slog.String("workspace", string(id)),
			slog.Any("error", err))
	}
	return true, nil
}

// recoverPendingMutation 完成落盘状态**明确点名**的那一次写。
//
// 源: packages/workspace/workspace/src/index.ts:408-424
//
// 它绝不从一条记录的样子去猜它当初是怎么来的：解释不了的次序与表的分叉留给
// [Registry.validateStoredState] 大声报错。标记点名的工作区还在次序里，
// 说明介质被别处改过，报 [CodeInconsistentState]。
func (r *Registry) recoverPendingMutation(ctx context.Context) error {
	state := r.snapshotState()
	pending := state.PendingMutation
	if pending == nil {
		return nil
	}
	if slices.Contains(state.WorkspaceIDs, pending.WorkspaceID) {
		return fail(CodeInconsistentState,
			"工作区域不一致：待恢复的 %s 操作点名的工作区 %q 还在登记册次序里",
			pending.Operation, pending.WorkspaceID)
	}
	table, err := r.table()
	if err != nil {
		return err
	}
	if _, err := table.Delete(ctx, string(pending.WorkspaceID)); err != nil {
		return wrap(CodeStorageFailed, err, "收尾待恢复的工作区 %q 时删记录失败", pending.WorkspaceID)
	}
	return r.setState(ctx, DomainState{
		Initialized:        state.Initialized,
		WorkspaceIDs:       state.WorkspaceIDs,
		ArchivedSessionIDs: state.ArchivedSessionIDs,
	})
}

// bootstrapGroup 是 bootstrap 时按目标标识聚起来的一堆会话头。
//
// 源: packages/workspace/workspace/src/index.ts:73-77
type bootstrapGroup struct {
	target  fs.Target
	headers []session.SessionHeader
	// newestAt 是这一组里最新那条会话头的创建时刻（Unix 毫秒），拿来定新工作区的排序位。
	newestAt int64
}

// bootstrap 是那一次性的历史收编：把已落地会话按工作目录聚成工作区。
//
// 源: packages/workspace/workspace/src/index.ts:426-508
//
// 只在 [DomainState.Initialized] 为假时跑一次。跑完盖上 initialized 标记，
// 从此这段代码在这份介质上再也不执行——所以它必须在**拿得到完整会话列表**时才跑，
// 这正是 [Config.Persistence] 必需的原因。
//
// 一个会话只会被收进一个工作区（accounted 那张表），已经有账目的候选不重复收编。
func (r *Registry) bootstrap(ctx context.Context, headers []session.SessionHeader) error {
	table, err := r.table()
	if err != nil {
		return err
	}
	state := r.snapshotState()
	groups := r.groupHeaders(headers)

	byTargetKey := map[fs.TargetKey]WorkspaceID{}
	accounted := map[session.SessionID]WorkspaceID{}
	entries, err := table.Entries()
	if err != nil {
		return wrap(CodeStorageFailed, err, "读不到工作区表")
	}
	for _, entry := range entries {
		id := WorkspaceID(entry.Key)
		byTargetKey[entry.Value.TargetKey] = id
		for _, sessionID := range entry.Value.SessionIDs {
			accounted[sessionID] = id
		}
	}

	for _, group := range groups {
		id, exists := byTargetKey[group.target.TargetKey]
		if !exists {
			sessionIDs := make([]session.SessionID, 0, len(group.headers))
			for _, header := range group.headers {
				if _, taken := accounted[header.ID]; !taken {
					sessionIDs = append(sessionIDs, header.ID)
				}
			}
			if len(sessionIDs) == 0 {
				continue
			}
			id = WorkspaceID(r.newID())
			// 建时刻取这一组里最新那条会话的创建时刻，而不是「现在」：
			// bootstrap 收编的是历史，一份历史的年纪不该是它被收编的那一刻。
			createdAt := time.UnixMilli(group.newestAt).UTC()
			record := Record{
				TargetKey:   group.target.TargetKey,
				DisplayPath: group.target.DisplayPath,
				Title:       defaultTitle(group.target.DisplayPath),
				SessionIDs:  sessionIDs,
				CreatedAt:   createdAt,
				UpdatedAt:   createdAt,
			}
			if err := table.Put(ctx, string(id), record); err != nil {
				return wrap(CodeStorageFailed, err, "bootstrap 写工作区 %q 失败", id)
			}
			byTargetKey[group.target.TargetKey] = id
			for _, sessionID := range sessionIDs {
				accounted[sessionID] = id
			}
			continue
		}

		current, found, err := table.Get(string(id))
		if err != nil {
			return wrap(CodeStorageFailed, err, "bootstrap 读工作区 %q 失败", id)
		}
		if !found {
			return fail(CodeInconsistentState, "bootstrap 时工作区 %q 从表里消失了", id)
		}
		// 历史候选排在前面，已有账目里没被历史提到的那些跟在后面：
		// 收编不该打乱人手已经排过的次序，只往前面补历史。
		historical := make([]session.SessionID, 0, len(group.headers))
		for _, header := range group.headers {
			holder, taken := accounted[header.ID]
			if !taken || holder == id {
				historical = append(historical, header.ID)
			}
		}
		merged := slices.Clone(historical)
		for _, sessionID := range current.SessionIDs {
			if !slices.Contains(historical, sessionID) {
				merged = append(merged, sessionID)
			}
		}
		if slices.Equal(current.SessionIDs, merged) {
			continue
		}
		now := r.now()
		if _, err := table.Update(ctx, string(id), func(record Record) (Record, error) {
			record.SessionIDs = merged
			record.UpdatedAt = now
			return record, nil
		}); err != nil {
			return wrap(CodeStorageFailed, err, "bootstrap 更新工作区 %q 失败", id)
		}
		for _, sessionID := range historical {
			accounted[sessionID] = id
		}
	}

	order, err := r.bootstrapOrder(table, groups, state.WorkspaceIDs)
	if err != nil {
		return err
	}
	if !slices.Equal(state.WorkspaceIDs, order) {
		// 先写一次仍然 initialized=false 的次序：这一步和下一步之间崩掉时，
		// 下次启动会重新跑一遍 bootstrap，而重跑要看到的是这份新次序。
		if err := r.setState(ctx, DomainState{
			Initialized:        false,
			WorkspaceIDs:       order,
			ArchivedSessionIDs: state.ArchivedSessionIDs,
		}); err != nil {
			return err
		}
	}
	return r.setState(ctx, DomainState{
		Initialized:        true,
		WorkspaceIDs:       order,
		ArchivedSessionIDs: state.ArchivedSessionIDs,
	})
}

// groupHeaders 把会话头按目标标识聚起来，组内按创建时刻从新到旧，组间按最新时刻从新到旧。
//
// 源: packages/workspace/workspace/src/index.ts:429-442
// 源: packages/workspace/workspace/src/index.ts:82-83（compareHeaders）
//
// 工作目录识别不出目标的会话头直接跳过——它们没有可归属的目录。
func (r *Registry) groupHeaders(headers []session.SessionHeader) []bootstrapGroup {
	r.mutex.RLock()
	targets := make(map[session.SessionID]fs.Target, len(r.sessionTargets))
	for id, target := range r.sessionTargets {
		targets[id] = target
	}
	r.mutex.RUnlock()

	grouped := map[fs.TargetKey]*bootstrapGroup{}
	for _, header := range headers {
		target, ok := targets[header.ID]
		if !ok {
			continue
		}
		group, exists := grouped[target.TargetKey]
		if !exists {
			grouped[target.TargetKey] = &bootstrapGroup{target: target, headers: []session.SessionHeader{header}}
			continue
		}
		group.headers = append(group.headers, header)
	}

	groups := make([]bootstrapGroup, 0, len(grouped))
	for _, group := range grouped {
		sort.SliceStable(group.headers, func(i, j int) bool {
			left, right := group.headers[i], group.headers[j]
			if left.CreatedAt != right.CreatedAt {
				return left.CreatedAt > right.CreatedAt
			}
			return left.ID < right.ID
		})
		group.newestAt = group.headers[0].CreatedAt
		groups = append(groups, *group)
	}
	// 组间次序：最新的在前；同刻按目标标识排，只为让结果可复现。
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].newestAt != groups[j].newestAt {
			return groups[i].newestAt > groups[j].newestAt
		}
		return groups[i].target.TargetKey < groups[j].target.TargetKey
	})
	return groups
}

// bootstrapOrder 定 bootstrap 之后的展示次序。
//
// 源: packages/workspace/workspace/src/index.ts:491-502
//
// 三级判据：先按「这个目录最新一条会话的时刻」（没有会话的按记录自己的建时刻）从新到旧，
// 再按上一份次序里的位置，最后按 id。第二级是关键——它让一次重跑不会把
// 已经排好的工作区打散。
func (r *Registry) bootstrapOrder(
	table *domain.Table[Record],
	groups []bootstrapGroup,
	prior []WorkspaceID,
) ([]WorkspaceID, error) {
	groupRank := make(map[fs.TargetKey]int64, len(groups))
	for _, group := range groups {
		groupRank[group.target.TargetKey] = group.newestAt
	}
	priorRank := make(map[WorkspaceID]int, len(prior))
	for index, id := range prior {
		priorRank[id] = index
	}
	rankOf := func(id WorkspaceID) int {
		if index, ok := priorRank[id]; ok {
			return index
		}
		// 上一份次序里没有的排在有的后面。
		return len(prior) + 1
	}

	entries, err := table.Entries()
	if err != nil {
		return nil, wrap(CodeStorageFailed, err, "读不到工作区表")
	}
	sort.SliceStable(entries, func(i, j int) bool {
		leftID, rightID := WorkspaceID(entries[i].Key), WorkspaceID(entries[j].Key)
		leftTime, ok := groupRank[entries[i].Value.TargetKey]
		if !ok {
			leftTime = entries[i].Value.CreatedAt.UnixMilli()
		}
		rightTime, ok := groupRank[entries[j].Value.TargetKey]
		if !ok {
			rightTime = entries[j].Value.CreatedAt.UnixMilli()
		}
		if leftTime != rightTime {
			return leftTime > rightTime
		}
		if rankOf(leftID) != rankOf(rightID) {
			return rankOf(leftID) < rankOf(rightID)
		}
		return leftID < rightID
	})

	order := make([]WorkspaceID, 0, len(entries))
	for _, entry := range entries {
		order = append(order, WorkspaceID(entry.Key))
	}
	return order, nil
}

// validateStoredState 查落盘状态和表对不对得上，对不上一律报错**而不修**。
//
// 源: packages/workspace/workspace/src/index.ts:510-551
//
// 查五件事：次序不许重复；次序里的 id 表里必须有；已 bootstrap 过的登记册里
// 表和次序条数必须相等；一个目标标识只能被一个工作区认领；一个会话只能出现在
// 一个工作区的候选账目里。
//
// 能被解释的分叉由 [PendingMutation] 明确点名并在 [Registry.recoverPendingMutation]
// 里收尾，走到这里的都是解释不了的——猜一次「大概本来该是什么样」，
// 会把一次可查的事故变成一份看上去正常的坏数据。
func (r *Registry) validateStoredState(state DomainState) error {
	table, err := r.table()
	if err != nil {
		return err
	}
	order := make(map[WorkspaceID]struct{}, len(state.WorkspaceIDs))
	for _, id := range state.WorkspaceIDs {
		if _, repeated := order[id]; repeated {
			return fail(CodeInconsistentState, "工作区域不一致：登记册次序里工作区 %q 出现了两次", id)
		}
		_, found, getErr := table.Get(string(id))
		if getErr != nil {
			return wrap(CodeStorageFailed, getErr, "读工作区 %q 失败", id)
		}
		if !found {
			return fail(CodeInconsistentState, "工作区域不一致：登记册次序指向一条不存在的工作区 %q", id)
		}
		order[id] = struct{}{}
	}

	entries, err := table.Entries()
	if err != nil {
		return wrap(CodeStorageFailed, err, "读不到工作区表")
	}
	if state.Initialized && len(order) != len(entries) {
		for _, entry := range entries {
			if _, listed := order[WorkspaceID(entry.Key)]; !listed {
				return fail(CodeInconsistentState,
					"工作区域不一致：工作区 %q 不在登记册次序里", entry.Key)
			}
		}
	}

	claimed := map[fs.TargetKey]WorkspaceID{}
	accounted := map[session.SessionID]WorkspaceID{}
	for _, entry := range entries {
		id := WorkspaceID(entry.Key)
		if holder, taken := claimed[entry.Value.TargetKey]; taken {
			return fail(CodeInconsistentState,
				"工作区域不一致：目录 %q 同时被工作区 %q 和工作区 %q 认领",
				entry.Value.DisplayPath, holder, id)
		}
		claimed[entry.Value.TargetKey] = id
		for _, sessionID := range entry.Value.SessionIDs {
			if holder, taken := accounted[sessionID]; taken {
				return fail(CodeInconsistentState,
					"工作区域不一致：会话 %q 同时记在工作区 %q 和工作区 %q 的账目里",
					sessionID, holder, id)
			}
			accounted[sessionID] = id
		}
	}
	return nil
}

// rebuildEntities 按落盘次序重建整份实体缓存。
//
// 源: packages/workspace/workspace/src/index.ts:553-559
func (r *Registry) rebuildEntities() error {
	table, err := r.table()
	if err != nil {
		return err
	}
	state := r.snapshotState()
	rebuilt := make(map[WorkspaceID]*entity, len(state.WorkspaceIDs))
	for _, id := range state.WorkspaceIDs {
		record, found, getErr := table.Get(string(id))
		if getErr != nil {
			return wrap(CodeStorageFailed, getErr, "读工作区 %q 失败", id)
		}
		if !found {
			return fail(CodeInconsistentState, "登记册次序指向一条不存在的工作区 %q", id)
		}
		rebuilt[id] = newEntity(r, id, record)
	}
	r.mutex.Lock()
	r.entities = rebuilt
	r.mutex.Unlock()
	return nil
}

// replaceHeaderIndex 整份换掉会话头索引。
//
// 源: packages/workspace/workspace/src/index.ts:561-566
func (r *Registry) replaceHeaderIndex(ctx context.Context, headers []session.SessionHeader) {
	r.mutex.Lock()
	r.headers = make(map[session.SessionID]session.SessionHeader, len(headers))
	r.sessionTargets = map[session.SessionID]fs.Target{}
	r.invalidSessions = map[session.SessionID]string{}
	r.mutex.Unlock()
	r.indexHeaders(ctx, headers)
}

// indexHeaders 把一批会话头逐条索引进来。
//
// 源: packages/workspace/workspace/src/index.ts:568-570
func (r *Registry) indexHeaders(ctx context.Context, headers []session.SessionHeader) {
	for _, header := range headers {
		r.indexHeader(ctx, header)
	}
}

// indexHeader 索引一条会话头：记下它本身，并解析它的工作目录。
//
// 源: packages/workspace/workspace/src/index.ts:572-590
//
// 解析不出来、不是目录、或者根本没有工作目录的，只在 invalidSessions 里留一条原因，
// **不报错**：一条用不了的会话头不该让整个登记册打不开，它只是筛不进任何工作区而已。
func (r *Registry) indexHeader(ctx context.Context, header session.SessionHeader) {
	r.mutex.Lock()
	r.headers[header.ID] = header
	delete(r.sessionTargets, header.ID)
	r.mutex.Unlock()

	if header.Cwd == "" {
		r.markInvalidSession(header.ID, "会话头里没有工作目录")
		return
	}
	target, err := r.filesystem.Resolve(ctx, header.Cwd, "")
	if err != nil {
		r.markInvalidSession(header.ID, fmt.Sprintf("工作目录 %q 解析不出来", header.Cwd))
		return
	}
	info, found, err := r.filesystem.Stat(ctx, target)
	if err != nil {
		r.markInvalidSession(header.ID, fmt.Sprintf("查工作目录 %q 时文件系统出错：%v", header.Cwd, err))
		return
	}
	if !found || info.Type != fs.TypeDirectory {
		r.markInvalidSession(header.ID, fmt.Sprintf("工作目录 %q 不是一个存在的目录", header.Cwd))
		return
	}
	r.rememberSessionTarget(header.ID, target)
}

// markInvalidSession 记下一个会话的工作目录为什么用不了。
func (r *Registry) markInvalidSession(id session.SessionID, reason string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.invalidSessions[id] = reason
}

// indexLiveSessions 把此刻活着的那些会话的会话头也索引进来。
//
// 源: packages/workspace/workspace/src/index.ts:592-596
//
// 活会话的会话头**盖过**落地的那一份：一个刚建出来还没落盘的会话只在这里能看见。
func (r *Registry) indexLiveSessions(ctx context.Context) {
	if r.live == nil {
		return
	}
	r.indexHeaders(ctx, r.live.Headers())
}

// reportFilteredCandidates 把「落盘的候选账目里筛不出来的那些」写进日志。
//
// 源: packages/workspace/workspace/src/index.ts:598-613
//
// 这是纯诊断：被筛掉的候选**不会**被抹掉（目录可能只是临时被移走了），
// 它们在这个工作区下一次真实写入时才被裁掉（见 [entity.mutate]）。
// 没有这条日志的话，一个工作区「会话凭空少了几条」是完全静默的。
func (r *Registry) reportFilteredCandidates() {
	r.mutex.RLock()
	type filtered struct {
		workspace WorkspaceID
		session   session.SessionID
		reason    string
	}
	var reports []filtered
	for _, id := range r.state.WorkspaceIDs {
		found, ok := r.entities[id]
		if !ok {
			continue
		}
		record := found.snapshot()
		for _, sessionID := range record.SessionIDs {
			target, resolved := r.sessionTargets[sessionID]
			if resolved && target.TargetKey == record.TargetKey {
				continue
			}
			reason, known := r.invalidSessions[sessionID]
			if !known {
				if _, indexed := r.headers[sessionID]; indexed {
					reason = fmt.Sprintf("它的工作目录 %q 不是这个工作区的 %q",
						target.DisplayPath, record.DisplayPath)
				} else {
					reason = "找不到它的会话头"
				}
			}
			reports = append(reports, filtered{workspace: id, session: sessionID, reason: reason})
		}
	}
	r.mutex.RUnlock()

	for _, report := range reports {
		r.logger.Warn("workspace: 一个候选会话没能通过归属判据",
			slog.String("workspace", string(report.workspace)),
			slog.String("session", string(report.session)),
			slog.String("reason", report.reason))
	}
}

// sessionKnown 判断一个会话是不是**确凿地**存在：活着、在索引里、或者在一次
// 新鲜的持久化列举里。
//
// 源: packages/workspace/workspace/src/index.ts:263-268
//
// 只有确凿的没有才返回 false。列举本身失败时把那条错误原样往上报——
// 后端故障绝不许冒充「这个会话不存在」，那会让一次磁盘掉线变成一次拒绝归档。
func (r *Registry) sessionKnown(ctx context.Context, id session.SessionID) (bool, error) {
	if r.live != nil {
		if _, alive := r.live.Header(id); alive {
			return true, nil
		}
	}
	r.mutex.RLock()
	_, indexed := r.headers[id]
	r.mutex.RUnlock()
	if indexed {
		return true, nil
	}

	headers, err := r.persistence.List(ctx)
	if err != nil {
		return false, wrap(CodeStorageFailed, err, "列举已落地会话失败，判断不了会话 %q 在不在", id)
	}
	r.indexHeaders(ctx, headers)

	r.mutex.RLock()
	defer r.mutex.RUnlock()
	_, indexed = r.headers[id]
	return indexed, nil
}

// enqueue 把一次登记册操作排进那条唯一的操作序列。
//
// 源: packages/workspace/workspace/src/index.ts:648-657
//
// 每次操作之前先跑一遍 [Registry.recoverPendingMutation]：一次已经提交的删除
// 可能只剩清标记没做，那个标记必须在下一次建/删把它覆盖掉之前收掉。
func (r *Registry) enqueue(ctx context.Context, operation func() error) error {
	r.opMutex.Lock()
	defer r.opMutex.Unlock()

	if !r.running() {
		return r.notStarted()
	}
	if err := r.recoverPendingMutation(ctx); err != nil {
		return err
	}
	return operation()
}

// setState 落盘地换掉全局状态，成功之后才换内存里那一份。
//
// 源: packages/workspace/workspace/src/index.ts:643-646
//
// 次序不能反：先换内存的话，一次失败的写会在内存里留下一个介质上根本不存在的状态。
func (r *Registry) setState(ctx context.Context, state DomainState) error {
	r.mutex.RLock()
	global := r.global
	started := r.started
	r.mutex.RUnlock()
	if !started {
		return r.notStarted()
	}
	if err := global.Set(ctx, state); err != nil {
		return wrap(CodeStorageFailed, err, "工作区域的全局状态写不下去")
	}
	r.mutex.Lock()
	r.state = state
	r.mutex.Unlock()
	return nil
}

// snapshotState 取一份当前全局状态的浅快照。
//
// 源: packages/workspace/workspace/src/index.ts:638-641（requireState）
func (r *Registry) snapshotState() DomainState {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.state
}

// running 报告登记册此刻是不是开着的。
func (r *Registry) running() bool {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.started
}

// notStarted 是「登记册还没起来」那句话。
//
// 源: packages/workspace/workspace/src/index.ts:634,639
func (r *Registry) notStarted() error {
	return fail(CodeNotStarted, "工作区登记册还没打开（或者已经关了）")
}

// entityByTargetKey 按目标标识找实体。
//
// 源: packages/workspace/workspace/src/index.ts:286-288
func (r *Registry) entityByTargetKey(key fs.TargetKey) (*entity, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	for _, id := range r.state.WorkspaceIDs {
		if found, ok := r.entities[id]; ok && found.snapshot().TargetKey == key {
			return found, true
		}
	}
	// 次序里还没有、但缓存里已经放进去的那一条（建工作区的三步之间）也要认出来。
	for _, found := range r.entities {
		if found.snapshot().TargetKey == key {
			return found, true
		}
	}
	return nil, false
}

// putEntity 把一个实体放进缓存。
func (r *Registry) putEntity(id WorkspaceID, e *entity) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.entities[id] = e
}

// dropEntity 把一个实体从缓存里拿掉。
func (r *Registry) dropEntity(id WorkspaceID) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	delete(r.entities, id)
}

// table 实现 [entityHost.table]。
//
// 源: packages/workspace/workspace/src/index.ts:633-636（requireTable）
func (r *Registry) table() (*domain.Table[Record], error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	if !r.started {
		return nil, r.notStartedLocked()
	}
	return r.workspace, nil
}

// notStartedLocked 和 [Registry.notStarted] 是同一句话，区别只在于调用方已经持锁。
func (r *Registry) notStartedLocked() error {
	return fail(CodeNotStarted, "工作区登记册还没打开（或者已经关了）")
}

// sessionTargetKey 实现 [entityHost.sessionTargetKey]。
//
// 源: packages/workspace/workspace/src/index.ts:106
func (r *Registry) sessionTargetKey(id session.SessionID) (fs.TargetKey, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	target, ok := r.sessionTargets[id]
	if !ok {
		return "", false
	}
	return target.TargetKey, true
}

// rememberSessionTarget 实现 [entityHost.rememberSessionTarget]。
//
// 源: packages/workspace/workspace/src/index.ts:108-111
func (r *Registry) rememberSessionTarget(id session.SessionID, target fs.Target) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.sessionTargets[id] = target
	delete(r.invalidSessions, id)
}

// fileSystem 实现 [entityHost.fileSystem]。
func (r *Registry) fileSystem() fs.FileSystem { return r.filesystem }

// now 实现 [entityHost.now]。
//
// 时刻一律取 UTC：落盘的时间戳带着本机时区的话，同一份介质在两台机器上读出来
// 不是同一个瞬间的字面量，而 bootstrap 要拿它排序。
func (r *Registry) now() time.Time { return r.clock().UTC() }

// readSessionHeader 实现 [entityHost.readSessionHeader]。
//
// 源: packages/workspace/workspace/src/index.ts:615-631
//
// 三级：活会话 → 索引里的缓存 → 现列举一次持久化。都没有才报 [CodeUnknownSession]；
// 列举本身失败报 [CodeStorageFailed]，理由同 [Registry.sessionKnown]。
func (r *Registry) readSessionHeader(ctx context.Context, id session.SessionID) (session.SessionHeader, error) {
	if r.live != nil {
		if header, alive := r.live.Header(id); alive {
			r.mutex.Lock()
			r.headers[id] = header
			r.mutex.Unlock()
			return header, nil
		}
	}
	r.mutex.RLock()
	cached, ok := r.headers[id]
	r.mutex.RUnlock()
	if ok {
		return cached, nil
	}

	headers, err := r.persistence.List(ctx)
	if err != nil {
		return session.SessionHeader{}, wrap(CodeStorageFailed, err,
			"列举已落地会话失败，验证不了会话 %q", id)
	}
	r.indexHeaders(ctx, headers)

	r.mutex.RLock()
	defer r.mutex.RUnlock()
	header, found := r.headers[id]
	if !found {
		return session.SessionHeader{}, fail(CodeUnknownSession,
			"验证不了会话 %q：会话持久化里没有它", id)
	}
	return header, nil
}

// defaultTitle 取展示路径的最后一段当默认标题。
//
// 源: packages/workspace/workspace/src/index.ts:290,462（`basename()`）
//
// 新增: DSH 用 node:path 的 basename，那是一个**按宿主机平台**分隔符切的函数。
// 本包的展示路径由 [fs.FileSystem] 后端给出，后端所在的平台不一定是宿主机平台
// （见 [fs.FileSystem.FileURL] 那段），所以这里两种分隔符都切。
// 切不出东西时回落到整条展示路径——一个空标题虽然合法，但读的人什么也看不出来。
func defaultTitle(displayPath string) string {
	trimmed := strings.TrimRight(displayPath, "/\\")
	if trimmed == "" {
		return displayPath
	}
	at := strings.LastIndexAny(trimmed, "/\\")
	if at < 0 {
		return trimmed
	}
	// trimmed 的末尾已经不是分隔符了，所以这一段一定非空。
	return trimmed[at+1:]
}
