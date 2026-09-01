// 本文件的作用：一个装在内存里的**参考键值后端**，只在测试里用。
//
// 它存在的理由有三个。一是**证明那套共用一致性测试不是空的**——一份谁都能过的契约测试
// 和没有测试是一回事，而只有真的跑过一个实现，才知道每一条断言确实会执行到。
// 二是给 storage 包自己的用例（注册表、中枢、形态解析）一个真的后端用，
// 而不是一个所有方法都返回 nil 的空壳。
//
// 三是给**每一个数据形态包**（领域层是第一个，后面还有）一份可用的后端。
// 这一条是它从 storage 包的 _test.go 里搬到这里的原因：_test.go 只属于自己那个包，
// 别的包导不到，于是每个形态包都得自己再抄一份两百多行的假后端——而两份「一样的」
// 假后端会在没人察觉的时候分叉，届时两个包对同一条契约的理解就不一样了。
//
// 「介质」由 [MemoryMedium] 扮演：后端关掉之后它还在，于是「进程重启后重新打开」
// 这件事可以在没有文件系统的情况下被观察到——而持久性那几条断言全靠这个。

package storagetest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/storage"
)

// MemoryMedium 是一份介质，对应磁盘上的那棵文件树或那个数据库文件。
//
// 它的生命周期比后端长：后端 Close 之后它原样还在，重新开一个后端就能读回去。
type MemoryMedium struct {
	mutex sync.Mutex
	units map[string]*memoryUnitData
}

// NewMemoryMedium 建一份空介质。
func NewMemoryMedium() *MemoryMedium {
	return &MemoryMedium{units: map[string]*memoryUnitData{}}
}

// memoryUnitData 是介质上一个单元的全部内容，包括盖在上面的版本号。
//
// 这里自带一把锁，而不是靠打开它的那个单元的锁：介质比单元活得久，
// [MemoryMedium.Table] 这类「绕过单元直接看介质」的检查随时可能和一次写并发，
// 而那两条路径必须落在同一把锁底下才不是数据竞争。
type memoryUnitData struct {
	mutex   sync.Mutex
	version int
	tables  map[string]map[string]json.RawMessage
	global  json.RawMessage
}

// unit 取出介质上的单元，还没有就按描述符建一个。
//
// 版本号对不上时**什么也不动**就返回错误——契约要求一次被拒绝的打开不能碰数据。
func (m *MemoryMedium) unit(descriptor storage.KVUnitDescriptor) (*memoryUnitData, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if existing, ok := m.units[descriptor.Name]; ok {
		if existing.version != descriptor.Version {
			return nil, &storage.Error{
				Code: storage.CodeVersionMismatch,
				Message: fmt.Sprintf("单元 %q 介质上盖的是版本 %d，这次要开的是 %d",
					descriptor.Name, existing.version, descriptor.Version),
			}
		}
		return existing, nil
	}

	data := &memoryUnitData{
		version: descriptor.Version,
		tables:  map[string]map[string]json.RawMessage{},
	}
	// 声明过的表一律先建成空 map：契约要求它们**在场且为空**，而不是缺席。
	for _, table := range descriptor.Tables {
		data.tables[table] = map[string]json.RawMessage{}
	}
	m.units[descriptor.Name] = data
	return data, nil
}

// Table 直接从介质上读一张表，绕开所有已打开的单元，返回一份拷贝。
//
// 用例要它是为了分清「落盘了」和「只改了内存」：数据形态层的核心不变量是
// **先落盘、再改内存**，而只要读还是从单元走，两者的观察结果就永远一样，
// 这条断言也就永远压不住任何东西。单元或表不存在时返回 nil。
func (m *MemoryMedium) Table(unit, table string) map[string]json.RawMessage {
	m.mutex.Lock()
	data, ok := m.units[unit]
	m.mutex.Unlock()

	if !ok {
		return nil
	}

	data.mutex.Lock()
	defer data.mutex.Unlock()

	records, declared := data.tables[table]
	if !declared {
		return nil
	}
	copied := make(map[string]json.RawMessage, len(records))
	for key, value := range records {
		copied[key] = append(json.RawMessage(nil), value...)
	}
	return copied
}

// Global 直接从介质上读全局槽，理由同 [MemoryMedium.Table]。从没写过时返回 nil。
func (m *MemoryMedium) Global(unit string) json.RawMessage {
	m.mutex.Lock()
	data, ok := m.units[unit]
	m.mutex.Unlock()

	if !ok {
		return nil
	}

	data.mutex.Lock()
	defer data.mutex.Unlock()

	if data.global == nil {
		return nil
	}
	return append(json.RawMessage(nil), data.global...)
}

// MemoryBackend 是开在一份介质上的后端。同一份介质可以先后开好几个（模拟进程重启）。
type MemoryBackend struct {
	medium *MemoryMedium

	mutex  sync.Mutex
	opened map[string]*memoryUnit
	closed bool
}

// NewMemoryBackend 在一份介质上开一个后端。
func NewMemoryBackend(medium *MemoryMedium) *MemoryBackend {
	return &MemoryBackend{medium: medium, opened: map[string]*memoryUnit{}}
}

// KV 让这个后端满足 [storage.KVProvider]。操作组就是后端自己。
func (b *MemoryBackend) KV() storage.KVFacet { return b }

// Close 关掉所有还开着的单元并释放介质。**幂等**。
func (b *MemoryBackend) Close(context.Context) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true
	for _, unit := range b.opened {
		unit.markClosed()
	}
	b.opened = map[string]*memoryUnit{}
	return nil
}

// Open 打开一个单元。
func (b *MemoryBackend) Open(_ context.Context, descriptor storage.KVUnitDescriptor) (storage.KVUnit, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.closed {
		return nil, &storage.Error{Code: storage.CodeClosed, Message: "后端已经关闭"}
	}
	if _, already := b.opened[descriptor.Name]; already {
		return nil, &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 已经打开了，没关就再开一次是调用方的 bug", descriptor.Name),
		}
	}

	data, err := b.medium.unit(descriptor)
	if err != nil {
		return nil, err
	}

	unit := &memoryUnit{backend: b, descriptor: descriptor, data: data}
	b.opened[descriptor.Name] = unit
	return unit, nil
}

// memoryUnit 是一个已打开的单元。
type memoryUnit struct {
	backend    *MemoryBackend
	descriptor storage.KVUnitDescriptor

	mutex  sync.Mutex
	data   *memoryUnitData
	closed bool
}

// markClosed 由后端在自己关闭时调用，此时后端的锁已经拿在手上。
func (u *memoryUnit) markClosed() {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	u.closed = true
}

// errClosed 是关掉之后所有调用共用的那个拒绝。
func (u *memoryUnit) errClosed() error {
	return &storage.Error{
		Code:    storage.CodeClosed,
		Message: fmt.Sprintf("单元 %q 已经关闭", u.descriptor.Name),
	}
}

func (u *memoryUnit) LoadAll(context.Context) (storage.Snapshot, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return storage.Snapshot{}, u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	// 逐条拷贝出去。直接把介质上那几个 map 交出来的话，调用方往里写一条，
	// 介质就被改了——而那次改动绕过了所有的持久化和原子性保证。
	tables := make(map[string]map[string]json.RawMessage, len(u.data.tables))
	for table, records := range u.data.tables {
		copied := make(map[string]json.RawMessage, len(records))
		for key, value := range records {
			copied[key] = append(json.RawMessage(nil), value...)
		}
		tables[table] = copied
	}

	var global json.RawMessage
	if u.data.global != nil {
		global = append(json.RawMessage(nil), u.data.global...)
	}
	return storage.Snapshot{Tables: tables, Global: global}, nil
}

func (u *memoryUnit) PutRecord(_ context.Context, table, key string, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	records, declared := u.data.tables[table]
	if !declared {
		return &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明过表 %q", u.descriptor.Name, table),
		}
	}
	records[key] = append(json.RawMessage(nil), value...)
	return nil
}

func (u *memoryUnit) DeleteRecord(_ context.Context, table, key string) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	// 表没声明过时不报错：删是幂等的，而「删一个不存在的东西」就是什么也不做。
	delete(u.data.tables[table], key)
	return nil
}

func (u *memoryUnit) SetGlobal(_ context.Context, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}
	if !u.descriptor.HasGlobal {
		return &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明全局槽", u.descriptor.Name),
		}
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	u.data.global = append(json.RawMessage(nil), value...)
	return nil
}

// Close 释放这个单元。**幂等**，且把它从后端那张「已打开」表里摘掉，
// 摘掉之后同名单元才重新开得起来。
func (u *memoryUnit) Close(context.Context) error {
	u.mutex.Lock()
	if u.closed {
		u.mutex.Unlock()
		return nil
	}
	u.closed = true
	u.mutex.Unlock()

	u.backend.mutex.Lock()
	defer u.backend.mutex.Unlock()

	if u.backend.opened[u.descriptor.Name] == u {
		delete(u.backend.opened, u.descriptor.Name)
	}
	return nil
}

// 编译期确认这个参考实现确实提供键值形态。写反了的话所有基于它的用例
// 都会在 [storage.KV] 那里拿到 false，而那看起来像是被测代码的问题。
var _ storage.KVProvider = (*MemoryBackend)(nil)
