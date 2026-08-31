// 本文件的作用：这个包的用例要用的两个假后端。
//
// 参考后端（storagetest.MemoryBackend）能跑通正常路径，但这个包有两类断言它压不住：
// 一类要求后端**不提供键值形态**（[bareBackend]），一类要求某一次落盘**失败**或者
// **卡在半路**（[flakyBackend]）——而后者正是「落盘失败就什么都不动」和
// 「关闭排干已排队的写」这两条不变量唯一的观察窗口。

package domain

import (
	"context"
	"encoding/json"
	"sync"

	"ds-harness-go/storage"
	"ds-harness-go/storage/storagetest"
)

// bareBackend 是一个什么数据形态都不提供的后端。
//
// 它只满足 storage.Backend，不满足 storage.KVProvider——这正是
// [CodeFacetUnsupported] 那条路唯一的触发方式。
type bareBackend struct{}

func (bareBackend) Close(context.Context) error { return nil }

// flakyBackend 包住参考后端，允许逐个方法注入失败，以及在方法入口挂一个钩子。
//
// 钩子是给「关闭要排干已排队的写」那条用的：它让用例能在一次写**正卡在后端里**的
// 那一刻去调 Close，而这是那条断言唯一能被真的观察到的时机。
type flakyBackend struct {
	inner *storagetest.MemoryBackend

	mutex     sync.Mutex
	loadErr   error
	putErr    error
	deleteErr error
	globalErr error
	closeErr  error
	hook      func(op string)
}

func newFlakyBackend(medium *storagetest.MemoryMedium) *flakyBackend {
	return &flakyBackend{inner: storagetest.NewMemoryBackend(medium)}
}

// set 在锁底下改注入项，用例从别的 goroutine 改也安全。
func (b *flakyBackend) set(mutate func(*flakyBackend)) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	mutate(b)
}

// enter 是每个单元方法的入口：先跑钩子，再把这次该返回的注入错误取出来。
func (b *flakyBackend) enter(op string, pick func(*flakyBackend) error) error {
	b.mutex.Lock()
	hook := b.hook
	err := pick(b)
	b.mutex.Unlock()

	if hook != nil {
		hook(op)
	}
	return err
}

func (b *flakyBackend) KV() storage.KVFacet { return b }

func (b *flakyBackend) Close(ctx context.Context) error { return b.inner.Close(ctx) }

func (b *flakyBackend) Open(ctx context.Context, descriptor storage.KVUnitDescriptor) (storage.KVUnit, error) {
	unit, err := b.inner.KV().Open(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	return &flakyUnit{backend: b, inner: unit}, nil
}

type flakyUnit struct {
	backend *flakyBackend
	inner   storage.KVUnit
}

func (u *flakyUnit) LoadAll(ctx context.Context) (storage.Snapshot, error) {
	if err := u.backend.enter("load", func(b *flakyBackend) error { return b.loadErr }); err != nil {
		return storage.Snapshot{}, err
	}
	return u.inner.LoadAll(ctx)
}

func (u *flakyUnit) PutRecord(ctx context.Context, table, key string, value json.RawMessage) error {
	if err := u.backend.enter("put", func(b *flakyBackend) error { return b.putErr }); err != nil {
		return err
	}
	return u.inner.PutRecord(ctx, table, key, value)
}

func (u *flakyUnit) DeleteRecord(ctx context.Context, table, key string) error {
	if err := u.backend.enter("delete", func(b *flakyBackend) error { return b.deleteErr }); err != nil {
		return err
	}
	return u.inner.DeleteRecord(ctx, table, key)
}

func (u *flakyUnit) SetGlobal(ctx context.Context, value json.RawMessage) error {
	if err := u.backend.enter("global", func(b *flakyBackend) error { return b.globalErr }); err != nil {
		return err
	}
	return u.inner.SetGlobal(ctx, value)
}

func (u *flakyUnit) Close(ctx context.Context) error {
	if err := u.backend.enter("close", func(b *flakyBackend) error { return b.closeErr }); err != nil {
		return err
	}
	return u.inner.Close(ctx)
}

// 编译期确认两个假后端各自站在它该站的那一边：一个提供键值形态，一个不提供。
// 写反了的话相关用例会「通过」，但它压的不是它以为的那条路。
var (
	_ storage.Backend    = bareBackend{}
	_ storage.KVProvider = (*flakyBackend)(nil)
)
