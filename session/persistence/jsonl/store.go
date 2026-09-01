// 本文件的作用：把这个后端和编排器接成使用方看到的那道 [persistence.Store]。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:117-141
// 源: packages/session/session-persistence-jsonl/src/index.ts:203-292

package jsonl

import (
	"context"
	"errors"
	"log/slog"

	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// Deps 是造一个 [Store] 要的那几样协作者。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:117-141
//
// 新增: DSH 从 cordis 容器里拿 ctx.sessions 和词汇。本仓库不用容器，所以明着传。
type Deps struct {
	// Sessions 是那张活会话表，必填。
	Sessions persistence.Sessions

	// Vocabulary 是这个存储认得的事件词汇；零值表示只认核心那一套。
	Vocabulary session.Vocabulary

	// Logger 收那些只在本进程里有意义的诊断；nil 就用 [slog.Default]。
	Logger *slog.Logger
}

// Store 是这个 JSONL 后端对外的那道服务面。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:203-292
//
// 分工照 [persistence.Store] 上写的那条来：Locate、SupportsRawArtifacts、
// ReadRaw、ListSnapshots 只碰盘上的字节，归 [Backend]；Create、Append、Load、
// Inspect、ReadFrom、List 要跨「活会话」和「存档」两边对账，归
// [persistence.Coordinator]。本类型不自己做判断，只把调用分到该去的那一边——
// 任何一条被它「顺手改一下」的语义，都会变成两套并存的规矩。
type Store struct {
	backend     *Backend
	coordinator *persistence.Coordinator
}

var _ persistence.Store = (*Store)(nil)

// New 造一个 JSONL 会话存储。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:117-141
func New(deps Deps, config Config) (*Store, error) {
	if deps.Sessions == nil {
		return nil, errors.New("session/persistence/jsonl: 需要一张活会话表")
	}
	backend, err := NewBackend(config)
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
		})
	if err != nil {
		return nil, err
	}
	return &Store{backend: backend, coordinator: coordinator}, nil
}

// Backend 交出盘上那一层，给那些要直接摸字节的诊断和测试用。
func (s *Store) Backend() *Backend { return s.backend }

// Install 把写路径挂到运行时上，交回把它整个摘下来的函数。
//
// 见 [persistence.Coordinator.Install]：那条「排干先登记、观察者后登记」的次序
// 在那里，本方法只是转一手。
func (s *Store) Install(
	ctx context.Context, owner *scope.Scope,
) (func(context.Context) error, error) {
	return s.coordinator.Install(ctx, owner)
}

// Locate 给出某个会话那份独立存档在哪。
func (s *Store) Locate(meta session.SessionHeader) (persistence.Location, bool) {
	return s.backend.Locate(meta)
}

// SupportsRawArtifacts 恒真：这个后端就是逐会话一份文件。
func (s *Store) SupportsRawArtifacts() bool { return true }

// ReadRaw 逐字原样读出一个会话那份日志的文本。
func (s *Store) ReadRaw(
	ctx context.Context, id session.SessionID,
) (persistence.RawArtifact, error) {
	return s.backend.ReadRaw(ctx, id)
}

// ListSnapshots 列举已落地的会话，各带一个便宜的变更令牌。
func (s *Store) ListSnapshots(ctx context.Context) ([]persistence.Snapshot, error) {
	return s.backend.ListSnapshots(ctx)
}

// Create 登记一个新会话的元数据。
//
// 落地是懒的：一个建了但从没追加过的会话在盘上什么都不留，所以它不会出现在
// [Store.List] 里。
func (s *Store) Create(ctx context.Context, meta session.SessionHeader) error {
	return s.coordinator.Create(ctx, meta)
}

// Append 持久化一批事件。
func (s *Store) Append(
	ctx context.Context, id session.SessionID, events []session.Event,
) error {
	return s.coordinator.Append(ctx, id, events)
}

// Load 读出一份已补平的逻辑视图，并把该做的冷恢复落盘。
func (s *Store) Load(
	ctx context.Context, id session.SessionID,
) (persistence.Inspection, error) {
	return s.coordinator.Load(ctx, id)
}

// Inspect 看一个不可变的逻辑会话，但不落盘恢复、也不发布它。
func (s *Store) Inspect(
	ctx context.Context, id session.SessionID,
) (persistence.Inspection, error) {
	return s.coordinator.Inspect(ctx, id)
}

// ReadFrom 读出 fromSeq 起的那些存档事件。
func (s *Store) ReadFrom(
	ctx context.Context, id session.SessionID, fromSeq int,
) (persistence.StoredSuffix, error) {
	return s.coordinator.ReadFrom(ctx, id, fromSeq)
}

// List 从元数据出发轻量列举，不解整份日志。
func (s *Store) List(ctx context.Context) ([]session.SessionHeader, error) {
	return s.coordinator.List(ctx)
}

// Prepare 从一份存档造一个还没发布的活会话。
//
// 它**不在** [persistence.Store] 上，理由写在那个接口的末尾：它要一张活会话表，
// 而那个接口是照着后端去实现的。
func (s *Store) Prepare(
	ctx context.Context, id session.SessionID,
) (*coresession.Preparation, error) {
	return s.coordinator.Prepare(ctx, id)
}
