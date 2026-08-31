// 本文件是测试夹具：一个全内存的凭据提供方。
//
// 源: packages/credentials/credentials/tests/memory.ts
//
// 它的另一个作用是**证明这条接缝装得起来**：下面这个结构体内嵌一个 [Notifier]
// 就同时满足了 [Observer] 的两个订阅方法和自己要调的两个 Notify 方法，
// 也就是包文档里说的那套装配。编译得过本身就是那段说明的证据。

package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
)

// errMemoryEmptyValue 是这个夹具拒绝空值时给出的错误。
//
// 源: packages/credentials/credentials/tests/memory.ts:45
//
// 「空值不许存」是接缝定的规则（见 [Provider.Set]），但错误本身由提供方给：
// DSH 那边同样是每个提供方自己抛，接缝上没有共用的错误词汇。
var errMemoryEmptyValue = errors.New("memory credentials: 空值不能存，清除请用 Unset")

// memoryCredentials 是一个永远可写的 memory 来源层，值从构造时的种子来。
//
// 源: packages/credentials/credentials/tests/memory.ts:13-17
type memoryCredentials struct {
	*Notifier

	store   map[Ref]string
	records map[Key]Record
}

// 编译期断言：这个夹具确实是一个完整的 [Provider]。
var _ Provider = (*memoryCredentials)(nil)

// newMemoryCredentials 造一个夹具。seed 是引用那一半的初始内容。
//
// 源: packages/credentials/credentials/tests/memory.ts:21-24
func newMemoryCredentials(logger *slog.Logger, seed map[Ref]string) *memoryCredentials {
	return &memoryCredentials{
		Notifier: NewNotifier(logger),
		store:    maps.Clone(defaulted(seed)),
		records:  map[Key]Record{},
	}
}

func defaulted(seed map[Ref]string) map[Ref]string {
	if seed == nil {
		return map[Ref]string{}
	}
	return seed
}

// Resolve 源: packages/credentials/credentials/tests/memory.ts:26-31
func (m *memoryCredentials) Resolve(_ context.Context, ref Ref) (Resolved, bool, error) {
	value, present := m.store[ref]
	if !present || value == "" {
		return Resolved{}, false, nil
	}
	return Resolved{Value: value, Source: "memory"}, true, nil
}

// Describe 源: packages/credentials/credentials/tests/memory.ts:33-41
func (m *memoryCredentials) Describe(_ context.Context, ref Ref) (Info, error) {
	value, present := m.store[ref]
	if !present || value == "" {
		return Info{Configured: false, Writable: true}, nil
	}
	return Info{Configured: true, Source: "memory", Writable: true}, nil
}

// Set 源: packages/credentials/credentials/tests/memory.ts:43-50
func (m *memoryCredentials) Set(_ context.Context, ref Ref, value string) error {
	if value == "" {
		return errMemoryEmptyValue
	}
	m.store[ref] = value
	m.NotifyReferenceUpdated(ref)
	return nil
}

// Unset 源: packages/credentials/credentials/tests/memory.ts:52-57
func (m *memoryCredentials) Unset(_ context.Context, ref Ref) error {
	if _, present := m.store[ref]; !present {
		return nil
	}
	delete(m.store, ref)
	m.NotifyReferenceUpdated(ref)
	return nil
}

// ReadRecord 源: packages/credentials/credentials/tests/memory.ts:59-61
func (m *memoryCredentials) ReadRecord(_ context.Context, key Key) (Record, bool, error) {
	record, present := m.records[key]
	return record, present, nil
}

// DescribeRecord 源: packages/credentials/credentials/tests/memory.ts:63-68
func (m *memoryCredentials) DescribeRecord(_ context.Context, key Key) (RecordInfo, error) {
	record, present := m.records[key]
	if !present {
		return RecordInfo{Configured: false, Writable: true}, nil
	}
	return RecordInfo{Configured: true, Kind: record.Kind(), Writable: true}, nil
}

// ListRecords 源: packages/credentials/credentials/tests/memory.ts:70-72
//
// 按地址排序输出：DSH 那边 Map 的迭代顺序就是插入顺序，Go 的 map 是随机的，
// 不排的话用例只能一条条捞，捞法本身会掩盖「少给了一条」这种错。
func (m *memoryCredentials) ListRecords(context.Context) ([]RecordEntry, error) {
	entries := make([]RecordEntry, 0, len(m.records))
	for key, record := range m.records {
		entries = append(entries, RecordEntry{Key: key, Kind: record.Kind()})
	}
	slices.SortFunc(entries, func(a, b RecordEntry) int {
		switch {
		case a.Key < b.Key:
			return -1
		case a.Key > b.Key:
			return 1
		default:
			return 0
		}
	})
	return entries, nil
}

// ModifyRecord 源: packages/credentials/credentials/tests/memory.ts:74-84
func (m *memoryCredentials) ModifyRecord(
	ctx context.Context, key Key, mutate Mutator,
) (Record, bool, error) {
	current, present := m.records[key]
	next, write, err := mutate(ctx, current, present)
	if err != nil {
		return nil, false, fmt.Errorf("memory credentials: 修改记录 %q 失败：%w", string(key), err)
	}
	if !write {
		return current, present, nil
	}
	m.records[key] = next
	m.NotifyRecordUpdated(key)
	return next, true, nil
}

// DeleteRecord 源: packages/credentials/credentials/tests/memory.ts:86-91
func (m *memoryCredentials) DeleteRecord(_ context.Context, key Key) error {
	if _, present := m.records[key]; !present {
		return nil
	}
	delete(m.records, key)
	m.NotifyRecordUpdated(key)
	return nil
}
