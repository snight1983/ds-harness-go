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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/snight1983/ds-harness-go/storage"
)

// mediumSequence 给每一份介质发一个进程内唯一的编号。
//
// 修订标识里拌进它，两份各自独立的介质就永远发不出相等的令牌——契约要求
// 一个「从 A 读、拿去 B 写」的调用方撞上的是「对不上」，而不是一次静默的成功。
// 这和 [github.com/snight1983/ds-harness-go/datastore] 里那份实现同源同形。
var mediumSequence atomic.Int64

// globalSlot 是全局槽在令牌里占的那一段。
//
// 它故意不合 [storage.ValidUnitName]，所以永远不会和某张真表的那一段撞上。
const globalSlot = "@global"

// MemoryMedium 是一份介质，对应磁盘上的那棵文件树或那个数据库文件。
//
// 它的生命周期比后端长：后端 Close 之后它原样还在，重新开一个后端就能读回去。
type MemoryMedium struct {
	// instance 是这份介质的编号，只进修订标识，不对外露出。
	instance string

	mutex sync.Mutex
	units map[string]*memoryUnitData
}

// NewMemoryMedium 建一份空介质。
func NewMemoryMedium() *MemoryMedium {
	return &MemoryMedium{
		instance: strconv.FormatInt(mediumSequence.Add(1), 10),
		units:    map[string]*memoryUnitData{},
	}
}

// memoryUnitData 是介质上一个单元的全部内容，包括盖在上面的版本号。
//
// 这里自带一把锁，而不是靠打开它的那个单元的锁：介质比单元活得久，
// [MemoryMedium.Table] 这类「绕过单元直接看介质」的检查随时可能和一次写并发，
// 而那两条路径必须落在同一把锁底下才不是数据竞争。
type memoryUnitData struct {
	mutex   sync.Mutex
	version int
	tables  map[string]map[string]memoryRecord
	// global 是全局槽。nil 表示从没写过——那和「写过一段 JSON null」是两件事。
	global *memoryRecord
}

// memoryRecord 是介质上的一条记录：值，加上它此刻的计数。
//
// 计数从 1 起数，每一次成功的写加一，**即使写进去的值和原来一模一样**。
// 不加的话，一个「读到某一版、写了个相同的值」的序列会让下一次条件写误判成
// 「没人动过」。
type memoryRecord struct {
	value   json.RawMessage
	counter int64
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
		tables:  map[string]map[string]memoryRecord{},
	}
	// 声明过的表一律先建成空 map：契约要求它们**在场且为空**，而不是缺席。
	for _, table := range descriptor.Tables {
		data.tables[table] = map[string]memoryRecord{}
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
	for key, record := range records {
		copied[key] = append(json.RawMessage(nil), record.value...)
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
	return append(json.RawMessage(nil), data.global.value...)
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
		for key, record := range records {
			copied[key] = append(json.RawMessage(nil), record.value...)
		}
		tables[table] = copied
	}

	var global json.RawMessage
	if u.data.global != nil {
		global = append(json.RawMessage(nil), u.data.global.value...)
	}
	return storage.Snapshot{Tables: tables, Global: global}, nil
}

func (u *memoryUnit) LoadTable(_ context.Context, table string) (map[string]json.RawMessage, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return nil, u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	// 没声明过的表和空表交出的是同一样东西：一张空 map。理由见 [storage.KVUnit]。
	copied := map[string]json.RawMessage{}
	for key, record := range u.data.tables[table] {
		copied[key] = append(json.RawMessage(nil), record.value...)
	}
	return copied, nil
}

// revisionOf 把一条记录的计数折成对外的修订标识。
func (u *memoryUnit) revisionOf(slot string, counter int64) storage.Revision {
	return storage.Revision(u.backend.medium.instance + ":" + u.descriptor.Name + ":" + slot +
		":" + strconv.FormatInt(counter, 10))
}

// counterOf 把一个修订标识折回计数。第二个返回值为 false 表示它不是这个槽发出来的。
//
// 别处发的令牌**当作对不上处理，不当作格式错误**：一个拿着 A 介质的令牌去 B 介质写的
// 调用方，它真正的问题是「我以为我读过这条记录」，而那正是前置条件要拦的事。
func (u *memoryUnit) counterOf(slot string, revision storage.Revision) (int64, bool) {
	rest, ok := strings.CutPrefix(string(revision),
		u.backend.medium.instance+":"+u.descriptor.Name+":"+slot+":")
	if !ok {
		return 0, false
	}
	counter, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return counter, true
}

func (u *memoryUnit) errStale(slot, key string) error {
	if key == "" {
		return &storage.Error{
			Code:    storage.CodeStaleRevision,
			Message: fmt.Sprintf("单元 %q 的 %s 上的前置条件不成立", u.descriptor.Name, slot),
		}
	}
	return &storage.Error{
		Code: storage.CodeStaleRevision,
		Message: fmt.Sprintf("单元 %q 的表 %q 的键 %q 上的前置条件不成立",
			u.descriptor.Name, slot, key),
	}
}

// checkIntent 判一次写的前置条件成不成立。current 为 nil 表示这一行此刻不存在。
//
// 这里判完就地写，中间不放锁——契约要求「不存在才写」在介质上是一次原子操作。
func (u *memoryUnit) checkIntent(
	slot, key string, current *memoryRecord, expected storage.WriteIntent,
) error {
	switch typed := expected.(type) {
	case nil:
		return nil
	case storage.CreateIfAbsent:
		if current != nil {
			return u.errStale(slot, key)
		}
		return nil
	case storage.ReplaceIfRevision:
		if current == nil {
			return u.errStale(slot, key)
		}
		counter, ok := u.counterOf(slot, typed.Revision)
		if !ok || counter != current.counter {
			return u.errStale(slot, key)
		}
		return nil
	default:
		// WriteIntent 是封闭的：这一支只可能是 storage 自己将来加了成员却忘了在这里判。
		return &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("认不出的写前置条件 %T", expected),
		}
	}
}

func (u *memoryUnit) ReadRecord(
	_ context.Context, table, key string,
) (json.RawMessage, storage.Revision, bool, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return nil, "", false, u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	// 没声明过的表在这里同样是「不在」，不是错误——这个方法问的就是「在不在」。
	record, found := u.data.tables[table][key]
	if !found {
		return nil, "", false, nil
	}
	return append(json.RawMessage(nil), record.value...), u.revisionOf(table, record.counter), true, nil
}

func (u *memoryUnit) ReadGlobal(context.Context) (json.RawMessage, storage.Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return nil, "", u.errClosed()
	}
	if !u.descriptor.HasGlobal {
		return nil, "", &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明全局槽", u.descriptor.Name),
		}
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	if u.data.global == nil {
		// 声明了槽但一次都没写过——全新单元的正常状态，不是介质坏了。
		return nil, "", nil
	}
	return append(json.RawMessage(nil), u.data.global.value...),
		u.revisionOf(globalSlot, u.data.global.counter), nil
}

func (u *memoryUnit) PutRecord(
	_ context.Context, table, key string, value json.RawMessage, expected storage.WriteIntent,
) (storage.Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return "", u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	records, declared := u.data.tables[table]
	if !declared {
		return "", &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明过表 %q", u.descriptor.Name, table),
		}
	}

	var current *memoryRecord
	if existing, found := records[key]; found {
		current = &existing
	}
	if err := u.checkIntent(table, key, current, expected); err != nil {
		return "", err
	}

	counter := int64(1)
	if current != nil {
		counter = current.counter + 1
	}
	records[key] = memoryRecord{value: append(json.RawMessage(nil), value...), counter: counter}
	return u.revisionOf(table, counter), nil
}

func (u *memoryUnit) DeleteRecord(
	_ context.Context, table, key string, expected *storage.ReplaceIfRevision,
) (bool, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return false, u.errClosed()
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	// 表没声明过时不报错：删是幂等的，而「删一个不存在的东西」就是什么也不做。
	record, found := u.data.tables[table][key]
	if expected != nil {
		var current *memoryRecord
		if found {
			current = &record
		}
		if err := u.checkIntent(table, key, current, *expected); err != nil {
			return false, err
		}
	}
	if !found {
		return false, nil
	}
	delete(u.data.tables[table], key)
	return true, nil
}

func (u *memoryUnit) SetGlobal(
	_ context.Context, value json.RawMessage, expected storage.WriteIntent,
) (storage.Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return "", u.errClosed()
	}
	if !u.descriptor.HasGlobal {
		return "", &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明全局槽", u.descriptor.Name),
		}
	}

	u.data.mutex.Lock()
	defer u.data.mutex.Unlock()

	if err := u.checkIntent(globalSlot, "", u.data.global, expected); err != nil {
		return "", err
	}

	counter := int64(1)
	if u.data.global != nil {
		counter = u.data.global.counter + 1
	}
	u.data.global = &memoryRecord{value: append(json.RawMessage(nil), value...), counter: counter}
	return u.revisionOf(globalSlot, counter), nil
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
