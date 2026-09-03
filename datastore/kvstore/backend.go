// 本文件的作用：适配本体——一个 storage 后端就是一份介质，一个 storage 单元
// 就是一个记录集，加上两套失败词汇之间的那次翻译。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package kvstore

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/snight1983/ds-harness-go/datastore"
	"github.com/snight1983/ds-harness-go/storage"
)

// Backend 是一个开在某份介质上的键值后端。
//
// 它同时也是自己的键值操作组（见 [Backend.KV]），和内存参考后端一个做法：
// 这一层没有第二种数据形态要分开，多一个类型只是多一次转发。
type Backend struct {
	medium *datastore.Medium

	mutex  sync.Mutex
	opened []*kvUnit
	closed bool
}

// 编译期确认这个后端确实提供键值形态。写反了的话调用方会在 [storage.KV]
// 那里拿到 false，而那看起来像是「这个后端根本没注册上」。
var _ storage.KVProvider = (*Backend)(nil)

// Open 在一份介质上打开后端。
//
// config 是介质的配置：连接池、方言、命名空间、池子的那几个数，见 [datastore.Config]。
// 连接池**归后端所有**，[Backend.Close] 会把它关掉。
func Open(ctx context.Context, config datastore.Config) (*Backend, error) {
	medium, err := datastore.Open(ctx, config)
	if err != nil {
		return nil, translate(err)
	}
	return &Backend{medium: medium}, nil
}

// KV 让这个后端满足 [storage.KVProvider]。操作组就是后端自己。
func (b *Backend) KV() storage.KVFacet { return b }

// Close 关掉所有还开着的单元，然后释放介质。**幂等**。
func (b *Backend) Close(ctx context.Context) error {
	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return nil
	}
	b.closed = true
	units := b.opened
	b.opened = nil
	b.mutex.Unlock()

	for _, unit := range units {
		_ = unit.records.Close(ctx)
		unit.markClosed()
	}
	return translate(b.medium.Close(ctx))
}

// Open 打开一个单元。
func (b *Backend) Open(
	ctx context.Context, descriptor storage.KVUnitDescriptor,
) (storage.KVUnit, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return nil, &storage.Error{Code: storage.CodeClosed, Message: "键值后端已经关闭"}
	}
	b.mutex.Unlock()

	records, err := b.medium.OpenRecords(ctx, datastore.RecordSpec{
		Name:      descriptor.Name,
		Version:   descriptor.Version,
		Tables:    descriptor.Tables,
		Singleton: descriptor.HasGlobal,
	})
	if err != nil {
		return nil, translate(err)
	}

	unit := &kvUnit{backend: b, records: records, hasGlobal: descriptor.HasGlobal}

	b.mutex.Lock()
	if b.closed {
		// 打开的途中后端被关掉了：这个单元不许留下，否则它会挂在一份已经释放的
		// 介质上，而 [Backend.Close] 那一遍已经走过去了。
		b.mutex.Unlock()
		_ = records.Close(ctx)
		return nil, &storage.Error{Code: storage.CodeClosed, Message: "键值后端已经关闭"}
	}
	b.opened = append(b.opened, unit)
	b.mutex.Unlock()
	return unit, nil
}

// release 把一个单元从「已打开」名单里摘掉。
func (b *Backend) release(unit *kvUnit) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for index, opened := range b.opened {
		if opened == unit {
			b.opened = append(b.opened[:index], b.opened[index+1:]...)
			return
		}
	}
}

// kvUnit 是一个已打开的单元：一个记录集，加一层词汇翻译。
type kvUnit struct {
	backend   *Backend
	records   *datastore.RecordUnit
	hasGlobal bool

	mutex  sync.Mutex
	closed bool
}

var _ storage.KVUnit = (*kvUnit)(nil)

// markClosed 由后端在自己关闭时调用：只标记，不去动后端那张名单——
// 后端那边正在整张名单一起清掉，这里再摘一遍既多余、又要反向去拿后端的锁。
func (u *kvUnit) markClosed() {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	u.closed = true
}

func (u *kvUnit) errClosed() error {
	return &storage.Error{Code: storage.CodeClosed, Message: "单元 " + u.records.Name() + " 已经关闭"}
}

func (u *kvUnit) live() error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return u.errClosed()
	}
	return nil
}

// LoadAll 读出当前的完整快照。
func (u *kvUnit) LoadAll(ctx context.Context) (storage.Snapshot, error) {
	if err := u.live(); err != nil {
		return storage.Snapshot{}, err
	}
	snapshot, err := u.records.Snapshot(ctx)
	if err != nil {
		return storage.Snapshot{}, translate(err)
	}
	return storage.Snapshot{Tables: snapshot.Tables, Global: snapshot.Singleton}, nil
}

// LoadTable 只读出其中一张表的全部记录。
func (u *kvUnit) LoadTable(ctx context.Context, table string) (map[string]json.RawMessage, error) {
	if err := u.live(); err != nil {
		return nil, err
	}
	records, err := u.records.SnapshotTable(ctx, table)
	if err != nil {
		return nil, translate(err)
	}
	return records, nil
}

// ReadRecord 读出单独一条记录，连同它此刻的修订标识。
func (u *kvUnit) ReadRecord(
	ctx context.Context, table, key string,
) (json.RawMessage, storage.Revision, bool, error) {
	if err := u.live(); err != nil {
		return nil, "", false, err
	}
	value, revision, found, err := u.records.Read(ctx, table, key)
	if err != nil {
		return nil, "", false, translate(err)
	}
	return value, storage.Revision(revision), found, nil
}

// ReadGlobal 读出全局单例槽，连同它此刻的修订标识。
func (u *kvUnit) ReadGlobal(ctx context.Context) (json.RawMessage, storage.Revision, error) {
	if err := u.live(); err != nil {
		return nil, "", err
	}
	value, revision, err := u.records.ReadSingleton(ctx)
	if err != nil {
		return nil, "", translate(err)
	}
	return value, storage.Revision(revision), nil
}

// PutRecord 写一条记录，交回写完之后的修订标识。
func (u *kvUnit) PutRecord(
	ctx context.Context, table, key string, value json.RawMessage, expected storage.WriteIntent,
) (storage.Revision, error) {
	if err := u.live(); err != nil {
		return "", err
	}
	guard, err := guardOf(expected)
	if err != nil {
		return "", err
	}
	revision, err := u.records.Put(ctx, table, key, value, guard)
	if err != nil {
		return "", translate(err)
	}
	return storage.Revision(revision), nil
}

// DeleteRecord 删一条记录，交回它删之前在不在。
func (u *kvUnit) DeleteRecord(
	ctx context.Context, table, key string, expected *storage.ReplaceIfRevision,
) (bool, error) {
	if err := u.live(); err != nil {
		return false, err
	}
	var guard *datastore.MustMatch
	if expected != nil {
		guard = &datastore.MustMatch{Revision: datastore.Revision(expected.Revision)}
	}
	existed, err := u.records.Delete(ctx, table, key, guard)
	if err != nil {
		return false, translate(err)
	}
	return existed, nil
}

// SetGlobal 盖上全局单例槽，交回写完之后的修订标识。
func (u *kvUnit) SetGlobal(
	ctx context.Context, value json.RawMessage, expected storage.WriteIntent,
) (storage.Revision, error) {
	if err := u.live(); err != nil {
		return "", err
	}
	guard, err := guardOf(expected)
	if err != nil {
		return "", err
	}
	revision, err := u.records.SetSingleton(ctx, value, guard)
	if err != nil {
		return "", translate(err)
	}
	return storage.Revision(revision), nil
}

// guardOf 把 storage 那套前置条件翻成 datastore 那套。
//
// 新增: 两套词汇形状一样、名字不一样，因为它们是两层各自的公共契约——datastore
// 不认识 storage，storage 也不该被迫用下面那一层的名字。翻译只有这一处。
func guardOf(intent storage.WriteIntent) (datastore.RecordGuard, error) {
	switch typed := intent.(type) {
	case nil:
		return nil, nil
	case storage.CreateIfAbsent:
		return datastore.MustBeAbsent{}, nil
	case storage.ReplaceIfRevision:
		return datastore.MustMatch{Revision: datastore.Revision(typed.Revision)}, nil
	default:
		// WriteIntent 是封闭的：sealedWriteIntent 那个未导出方法让包外造不出第三个
		// 成员。这一支只可能是 storage 自己将来加了成员却忘了在这里翻。
		return nil, &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: "认不出的写前置条件",
		}
	}
}

// Close 释放这个单元。**幂等**。
func (u *kvUnit) Close(ctx context.Context) error {
	u.mutex.Lock()
	if u.closed {
		u.mutex.Unlock()
		return nil
	}
	u.closed = true
	// 先放掉自己的锁再去拿后端的锁：这两把锁只允许「先后端、后单元」一个方向，
	// [Backend.Close] 也是先放掉后端的锁才去 markClosed 的。这里要是握着单元锁
	// 去拿后端锁，就出现了反向的一段，两边撞上就是死锁。
	u.mutex.Unlock()

	err := u.records.Close(ctx)
	u.backend.release(u)
	return translate(err)
}

// translate 把 datastore 的哨兵翻成 storage 那套封闭词汇。
//
// 新增: 翻不出来的原样往上冒。连不上库、事务被中止、死锁——这些在
// [storage.ErrorCode] 里本来就没有位置，硬塞进 CodeMalformedMedium 会让调用方
// 以为介质坏了，然后去做一次它根本不该做的修复。
func translate(err error) error {
	if err == nil {
		return nil
	}
	var code storage.ErrorCode
	switch {
	case errors.Is(err, datastore.ErrVersionMismatch):
		code = storage.CodeVersionMismatch
	case errors.Is(err, datastore.ErrClosed):
		code = storage.CodeClosed
	case errors.Is(err, datastore.ErrStaleRevision):
		code = storage.CodeStaleRevision
	case errors.Is(err, datastore.ErrMalformedName),
		errors.Is(err, datastore.ErrMalformedMedium),
		errors.Is(err, datastore.ErrAlreadyOpen):
		// 「这个名字不对」「介质里的值坏了」「没关就开了第二次」在 storage 那边是
		// 同一个码：调用方对这三种的处置都是「这次打开/这次写不成立」，分不分得开
		// 不改变它做什么。
		code = storage.CodeMalformedMedium
	default:
		return err
	}
	return &storage.Error{Code: code, Message: err.Error(), Err: err}
}
