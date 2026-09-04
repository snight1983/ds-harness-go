// 本文件的作用：把这个后端和编排器接成使用方看到的那道 [persistence.Store]。
//
// 新增: 整个文件都是本仓库自有的。那道分工线（哪些归后端、哪些归编排器）写在
// [persistence.Store] 上，不是本包定的，所以这里只做转发。

package sessionstore

import (
	"context"
	"errors"
	"log/slog"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Deps 是造一个 [Store] 要的那几样协作者。
type Deps struct {
	// Sessions 是那张活会话表，必填。
	Sessions persistence.Sessions

	// Vocabulary 是这个存储认得的事件词汇；零值表示只认核心那一套。
	Vocabulary sessionlog.Vocabulary

	// Logger 收那些只在本进程里有意义的诊断；nil 就用 [slog.Default]。
	Logger *slog.Logger
}

// Store 是这个后端对外的那道服务面。
//
// 分工照 [persistence.Store] 上写的那条来：SupportsRawArtifacts、ReadRaw、
// ListSnapshots 只碰介质，归 [Backend]；Create、Append、Load、Inspect、ReadFrom、
// List 要跨「活会话」和「存档」两边对账，归 [persistence.Coordinator]。
// 本类型不自己做判断，只把调用分到该去的那一边。
type Store struct {
	backend     *Backend
	coordinator *persistence.Coordinator
}

var _ persistence.Store = (*Store)(nil)

// New 造一个落在数据库上的会话存储。
func New(ctx context.Context, deps Deps, config Config) (*Store, error) {
	if deps.Sessions == nil {
		return nil, errors.New("adapter/datastore/sessionstore: 需要一张活会话表")
	}
	backend, err := NewBackend(ctx, config)
	if err != nil {
		return nil, err
	}
	coordinator, err := persistence.NewCoordinator(
		persistence.CoordinatorDeps{
			Backend:    backend,
			Sessions:   deps.Sessions,
			Vocabulary: deps.Vocabulary,
			Logger:     deps.Logger,
		},
		persistence.CoordinatorOptions{
			PreparedSessionCacheSize: config.PreparedSessionCacheSize,
			WriteBatchMaxDelay:       config.WriteBatchMaxDelay,
			MaxStoredEvents:          config.MaxStoredEvents,
		})
	if err != nil {
		// 编排层建不起来时这道介质就没人收了：平时收它的是
		// [persistence.Coordinator.Install] 那条排空路径，而它压根没生出来。
		// 数据库连接池不会自己散，漏一次就一直占着。
		return nil, errors.Join(err, backend.Close(ctx))
	}
	return &Store{backend: backend, coordinator: coordinator}, nil
}

// Backend 交出介质那一层，给那些要直接摸它的诊断和测试用。
func (s *Store) Backend() *Backend { return s.backend }

// Install 把写路径挂到运行时上，交回把它整个摘下来的函数。
//
// 见 [persistence.Coordinator.Install]：那条「排干先登记、观察者后登记」的次序
// 在那里，本方法只是转一手。收介质也在那条路上——[Backend] 满足
// [persistence.ClosableBackend]，编排层在静默排空之后会调它。
func (s *Store) Install(
	ctx context.Context, owner *scope.Scope,
) (func(context.Context) error, error) {
	return s.coordinator.Install(ctx, owner)
}

// Locate 恒假：所有会话装在同一份介质里，没有「这个会话那份存档」可指。
//
// 这个方法在 [persistence.Store] 上，所以必须有；[persistence.LocatingBackend]
// 那道**可选**的缝则整条不实现，见 [Backend]。
func (s *Store) Locate(sessionlog.SessionHeader) (persistence.Location, bool) {
	return persistence.Location{}, false
}

// SupportsRawArtifacts 恒假：一份行式存储里没有「那个会话的原始字节」这回事。
func (s *Store) SupportsRawArtifacts() bool { return false }

// ReadRaw 恒以 [persistence.ErrRawArtifactsUnsupported] 拒绝。
//
// 调用方先问 [Store.SupportsRawArtifacts]；问过了还调就得到这条。
func (s *Store) ReadRaw(
	context.Context, sessionlog.SessionID,
) (persistence.RawArtifact, error) {
	return persistence.RawArtifact{}, persistence.ErrRawArtifactsUnsupported
}

// ListSnapshots 列举已落地的会话，各带一个便宜的变更令牌。
func (s *Store) ListSnapshots(ctx context.Context) ([]persistence.Snapshot, error) {
	return s.backend.ListSnapshots(ctx)
}

// Create 登记一个新会话的元数据。
//
// 落地是懒的：一个建了但从没追加过的会话在介质上什么都不留，所以它不会出现在
// [Store.List] 里。
func (s *Store) Create(ctx context.Context, meta sessionlog.SessionHeader) error {
	return s.coordinator.Create(ctx, meta)
}

// Append 持久化一批事件。
func (s *Store) Append(
	ctx context.Context, id sessionlog.SessionID, events []sessionlog.Event,
) error {
	return s.coordinator.Append(ctx, id, events)
}

// Load 读出一份已补平的逻辑视图，并把该做的冷恢复落盘。
func (s *Store) Load(
	ctx context.Context, id sessionlog.SessionID,
) (persistence.Inspection, error) {
	return s.coordinator.Load(ctx, id)
}

// Inspect 看一个不可变的逻辑会话，但不落盘恢复、也不发布它。
func (s *Store) Inspect(
	ctx context.Context, id sessionlog.SessionID,
) (persistence.Inspection, error) {
	return s.coordinator.Inspect(ctx, id)
}

// ReadFrom 读出 fromSeq 起的那些存档事件。
func (s *Store) ReadFrom(
	ctx context.Context, id sessionlog.SessionID, fromSeq int,
) (persistence.StoredSuffix, error) {
	return s.coordinator.ReadFrom(ctx, id, fromSeq)
}

// List 从元数据出发轻量列举，不解整份日志。
func (s *Store) List(ctx context.Context) ([]sessionlog.SessionHeader, error) {
	return s.coordinator.List(ctx)
}

// Prepare 从一份存档造一个还没发布的活会话。
//
// 它**不在** [persistence.Store] 上，理由写在那个接口的末尾：它要一张活会话表，
// 而那个接口是照着后端去实现的。
func (s *Store) Prepare(
	ctx context.Context, id sessionlog.SessionID,
) (*coresession.Preparation, error) {
	return s.coordinator.Prepare(ctx, id)
}
