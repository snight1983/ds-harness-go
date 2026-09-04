// 本文件的作用：适配本体——一个会话就是一条流，加上两套词汇之间的那次翻译，
// 以及头的编解码落在哪一侧。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// BackendName 是这个后端在编排层的诊断和收尾错误里露出来的名字。
const BackendName = "session-persistence-datastore"

// UnitVersion 是这个单元里的值的格式版本，盖在 datastore 的单元登记处。
//
// 新增: 它说的是「一条条目的负载是 [sessionlog.Event] 的第几版编码」，和
// [sessionlog.SessionHeader.Version] 说的「这份日志是第几版」不是一回事——
// 后者盖在每个会话自己的头上，由编排层校验。盖着别的号一律拒绝：
// 这套格式还没发布过，没有迁移这一说。
const UnitVersion = 1

// DefaultUnit 是 [Config.Unit] 留空时用的单元名。
const DefaultUnit = "sessions"

// Config 是这个后端的部署配置。
type Config struct {
	// Medium 是介质的配置：连接池、方言、命名空间、池子的那几个数。
	//
	// 连接池**归后端所有**：[Backend.Close] 会把它关掉。这是从
	// [persistence.ClosableBackend] 那条契约来的——编排层静默排空之后会调它，
	// 那一下不把介质释放掉，池子就会随着后端一个个泄漏。
	Medium datastore.Config

	// Unit 是会话日志落在哪个单元里，留空则是 [DefaultUnit]。
	//
	// 同一份介质里可以有别的单元（比如键值中枢的那些），它们互不相干。
	Unit string

	// PreparedSessionCacheSize 转给编排器，见
	// [persistence.CoordinatorOptions.PreparedSessionCacheSize]。
	PreparedSessionCacheSize int

	// WriteBatchMaxDelay 转给编排器，见
	// [persistence.CoordinatorOptions.WriteBatchMaxDelay]。
	WriteBatchMaxDelay time.Duration

	// MaxStoredEvents 转给编排器，见
	// [persistence.CoordinatorOptions.MaxStoredEvents]。
	MaxStoredEvents int

	// Logger 收那些只在本进程里有意义的诊断；nil 就用 [slog.Default]。
	Logger *slog.Logger
}

// Backend 是把会话日志写进一份介质的持久化后端。
//
// 要一个能直接用的 [persistence.Store]，用 [New]。
type Backend struct {
	medium *datastore.Medium
	log    *datastore.LogUnit
	logger *slog.Logger
}

// 这四行钉住这个后端真的填满了那四道缝。
var (
	_ persistence.Backend         = (*Backend)(nil)
	_ persistence.SeekableBackend = (*Backend)(nil)
	_ persistence.ClosableBackend = (*Backend)(nil)
	_ persistence.TrimmingBackend = (*Backend)(nil)
)

// NewBackend 在一份介质上打开这个后端。
//
// 新增: 打不开就是打不开，当场说，不做成一个延迟到第一次读写才浮出来的东西。
func NewBackend(ctx context.Context, config Config) (*Backend, error) {
	unit := config.Unit
	if unit == "" {
		unit = DefaultUnit
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	medium, err := datastore.Open(ctx, config.Medium)
	if err != nil {
		return nil, err
	}
	log, err := medium.OpenLog(ctx, datastore.LogSpec{Name: unit, Version: UnitVersion})
	if err != nil {
		// 单元开不起来就把介质还回去：不还的话，一次失败的构造会留下一个
		// 谁也够不着、也关不掉的连接池。
		_ = medium.Close(ctx)
		return nil, err
	}
	return &Backend{medium: medium, log: log, logger: logger}, nil
}

// Name 是这个后端的名字。
func (b *Backend) Name() string { return BackendName }

// Close 释放介质。
func (b *Backend) Close(ctx context.Context) error {
	_ = b.log.Close(ctx)
	return b.medium.Close(ctx)
}

// ---- 头的编解码 ----

// encodeHeader 把一份头排成流上的那段不透明字节。
func encodeHeader(meta sessionlog.SessionHeader) (json.RawMessage, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("adapter/datastore/sessionstore: 会话 %q 的头排不出去：%w", string(meta.ID), err)
	}
	return payload, nil
}

// decodeHeader 把流上那段字节解回一份头。
//
// 解不回来是**这份存档坏了**，不是一次普通的失败：本包写下去的一定解得回来。
func decodeHeader(id sessionlog.SessionID, payload json.RawMessage) (sessionlog.SessionHeader, error) {
	var meta sessionlog.SessionHeader
	if err := json.Unmarshal(payload, &meta); err != nil {
		return sessionlog.SessionHeader{}, &persistence.CorruptionError{
			ID:    id,
			Cause: fmt.Errorf("会话头解不回来：%w", err),
		}
	}
	// id 不写在头里也能从流名推出来，但两边都有的时候必须一致：不一致说明有人
	// 绕过本包直接改过介质，那时候按哪一份都是猜。
	if meta.ID != id {
		return sessionlog.SessionHeader{}, &persistence.CorruptionError{
			ID:    id,
			Cause: fmt.Errorf("流名是 %q，头里写的却是 %q", string(id), string(meta.ID)),
		}
	}
	return meta, nil
}

// decodeEvents 把一段条目解回事件。
func decodeEvents(id sessionlog.SessionID, entries []datastore.Entry) ([]sessionlog.Event, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	events := make([]sessionlog.Event, 0, len(entries))
	for _, entry := range entries {
		var event sessionlog.Event
		if err := json.Unmarshal(entry.Payload, &event); err != nil {
			return nil, &persistence.CorruptionError{ID: id, Cause: fmt.Errorf(
				"seq %d 那条 event 的负载解不回来：%w", entry.Seq, err)}
		}
		// seq 在条目上和在负载里各存了一份，这里对一遍。两者不一致说明有人绕过
		// 本包直接改过介质——那时候按哪一份都是猜，只能拒。
		if int64(event.Seq) != entry.Seq {
			return nil, &persistence.CorruptionError{ID: id, Cause: fmt.Errorf(
				"条目上的 seq 是 %d，负载里写的却是 %d", entry.Seq, event.Seq)}
		}
		events = append(events, event)
	}
	return events, nil
}

// notFound 把「这条流不在」翻成编排层认的那条正常控制流。
func notFound(err error) error {
	if errors.Is(err, datastore.ErrStreamNotFound) {
		return persistence.ErrSessionNotFound
	}
	return err
}

// ---- 读 ----

// LoadStored 按身份读出一份物理前缀。
//
// TornMarker 恒为 nil：一次写就是一个事务，要么整批提交要么一条都没有，
// 这个后端造不出断尾。
func (b *Backend) LoadStored(
	ctx context.Context, id sessionlog.SessionID,
) (persistence.StoredPrefix, error) {
	segment, err := b.log.Load(ctx, string(id), 0)
	if err != nil {
		return persistence.StoredPrefix{}, notFound(err)
	}
	meta, err := decodeHeader(id, segment.Head)
	if err != nil {
		return persistence.StoredPrefix{}, err
	}
	events, err := decodeEvents(id, segment.Entries)
	if err != nil {
		return persistence.StoredPrefix{}, err
	}
	return persistence.StoredPrefix{
		Meta:     meta,
		Events:   events,
		BaseSeq:  int(segment.BaseSeq),
		Revision: persistence.Revision(segment.Revision),
	}, nil
}

// LoadStoredFrom 读出头，加上 seq 不小于 fromSeq 的那些事件，不读整份日志。
//
// 新增: 这正是这个后端实现 [persistence.SeekableBackend] 的理由——按 seq 寻址在
// 日志集上是走主键的一句读，不必像顺序介质那样先把前面全解一遍。
func (b *Backend) LoadStoredFrom(
	ctx context.Context, id sessionlog.SessionID, fromSeq int,
) (persistence.StoredSuffix, error) {
	if fromSeq < 0 {
		return persistence.StoredSuffix{}, fmt.Errorf(
			"%w：LoadStoredFrom 的 fromSeq 不能是负数（给的是 %d）", persistence.ErrMalformedSeq, fromSeq)
	}
	segment, err := b.log.Load(ctx, string(id), int64(fromSeq))
	if err != nil {
		return persistence.StoredSuffix{}, notFound(err)
	}
	meta, err := decodeHeader(id, segment.Head)
	if err != nil {
		return persistence.StoredSuffix{}, err
	}
	events, err := decodeEvents(id, segment.Entries)
	if err != nil {
		return persistence.StoredSuffix{}, err
	}
	// 交的是**整份存档**的起点，不是这一截后缀的起点：读的一方要靠它分清
	// 「请求的水位早就被弹掉了」和「那一段压根没写过」。
	return persistence.StoredSuffix{Meta: meta, Events: events, BaseSeq: int(segment.BaseSeq)}, nil
}

// ReadStoredRevision 只读出一个会话当前的变更令牌，不读它的事件。
func (b *Backend) ReadStoredRevision(
	ctx context.Context, id sessionlog.SessionID,
) (persistence.Revision, error) {
	revision, err := b.log.ReadRevision(ctx, string(id))
	if err != nil {
		return "", notFound(err)
	}
	return persistence.Revision(revision), nil
}

// List 列出所有已落地会话的元数据。
func (b *Backend) List(ctx context.Context) ([]sessionlog.SessionHeader, error) {
	snapshots, err := b.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	headers := make([]sessionlog.SessionHeader, 0, len(snapshots))
	for _, snapshot := range snapshots {
		headers = append(headers, snapshot.Header)
	}
	return headers, nil
}

// ListSnapshots 列出已落地的会话，各带一个便宜的变更令牌。
//
// 新增: 排序在这里做，不在 datastore 那边——那一层不认识头里有个 created_at，
// 它只排得出流名。按建立时间排是使用方要的顺序，所以它归使用方这一侧。
func (b *Backend) ListSnapshots(ctx context.Context) ([]persistence.Snapshot, error) {
	streams, err := b.log.List(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]persistence.Snapshot, 0, len(streams))
	for _, stream := range streams {
		meta, err := decodeHeader(sessionlog.SessionID(stream.Name), stream.Head)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, persistence.Snapshot{
			Header:   meta,
			Revision: persistence.Revision(stream.Revision),
		})
	}
	sort.SliceStable(snapshots, func(left, right int) bool {
		if snapshots[left].Header.CreatedAt != snapshots[right].Header.CreatedAt {
			return snapshots[left].Header.CreatedAt < snapshots[right].Header.CreatedAt
		}
		return snapshots[left].Header.ID < snapshots[right].Header.ID
	})
	return snapshots, nil
}

// ---- 写 ----

// AppendBatch 把一批 seq 连续的事件持久化下去，materialized 为假时先把这个会话落地。
//
// 落地那一下和第一批事件在同一个事务里提交，兑现
// [persistence.Backend.AppendBatch] 要求的原子性。
func (b *Backend) AppendBatch(
	ctx context.Context,
	meta sessionlog.SessionHeader,
	events []sessionlog.Event,
	materialized bool,
) error {
	return b.write(ctx, meta, events, !materialized)
}

// CommitRepair 把一次崩溃修复落盘。
//
// 这个后端不发断尾凭据（见 [Backend.LoadStored]），所以 torn 只可能是 nil；
// 非 nil 说明编排器把别的后端的凭据递错了地方，那时候照着一个由别人算出来的值
// 去截断是最坏的选择。
func (b *Backend) CommitRepair(
	ctx context.Context,
	meta sessionlog.SessionHeader,
	torn any,
	closers []sessionlog.Event,
) error {
	if torn != nil {
		return fmt.Errorf(
			"adapter/datastore/sessionstore: 收到一张断尾凭据（%T），而这个后端从不发断尾凭据", torn)
	}
	if len(closers) == 0 {
		return nil
	}
	return b.write(ctx, meta, closers, false)
}

// write 是两条写路共用的那一次追加。
func (b *Backend) write(
	ctx context.Context,
	meta sessionlog.SessionHeader,
	events []sessionlog.Event,
	materialize bool,
) error {
	head, err := encodeHeader(meta)
	if err != nil {
		return err
	}
	entries := make([]datastore.Entry, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("adapter/datastore/sessionstore: 会话 %q 的 seq %d 那条 event 排不出去：%w",
				string(meta.ID), event.Seq, err)
		}
		entries = append(entries, datastore.Entry{Seq: int64(event.Seq), Payload: payload})
	}

	err = b.log.Append(ctx, datastore.AppendRequest{
		Stream:       string(meta.ID),
		Head:         head,
		EnsureStream: materialize,
		Entries:      entries,
	})
	switch {
	case errors.Is(err, datastore.ErrHeadConflict):
		// 真正的撞号由编排层挡（建会话前先探一次，认领时还比工作目录和 seed），
		// 这里兜的是「同一个 id 底下换了一份头」。
		return fmt.Errorf(
			"adapter/datastore/sessionstore: 会话 %q 已经落地了，而且那份头和要落地的这份不一样，撞号了",
			string(meta.ID))
	case errors.Is(err, datastore.ErrStreamNotFound):
		return fmt.Errorf("%w：会话 %q 还没落地，写不进去", persistence.ErrSessionNotFound, string(meta.ID))
	}
	return err
}

// TrimBefore 丢掉一个会话里 seq 严格小于 beforeSeq 的那些事件。
func (b *Backend) TrimBefore(ctx context.Context, id sessionlog.SessionID, beforeSeq int) error {
	if beforeSeq < 0 {
		return fmt.Errorf(
			"%w：TrimBefore 的 beforeSeq 不能是负数（给的是 %d）", persistence.ErrMalformedSeq, beforeSeq)
	}
	return notFound(b.log.TrimBefore(ctx, string(id), int64(beforeSeq)))
}
