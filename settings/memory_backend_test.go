// 本文件是测试夹具：一个全内存的设置后端。
//
// 源: packages/settings/settings/tests/memory.ts
//
// 它的另一个作用是**证明这条接缝装得起来**：一个只实现了三个方法的结构体
// 就是一个完整的 [Backend]，其余四百多行的行为由 [Provider] 提供。
// 编译得过本身就是包文档里那段「抽象的少、写好的多」的证据。

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// errBackendOffline 是夹具模拟一次落盘失败时给的错误。
var errBackendOffline = errors.New("memory settings: 存储不可达")

// memoryBackend 是一个全内存后端。
//
// 源: packages/settings/settings/tests/memory.ts:11-31
type memoryBackend struct {
	// mutex 保护下面全部字段。夹具会被 Provider 从多个 goroutine 碰到
	// （并发写用例就是冲着这个来的），不上锁的话 -race 会先炸在夹具身上。
	mutex sync.Mutex

	document  map[string]any
	persisted []persistedCall
	writable  bool
	// persistDelay 是人为的落盘延迟，让用例能把两次并发写叠在一起。
	persistDelay time.Duration
	// persistErr 非 nil 时每次落盘都失败。
	persistErr error
	// loadErr 非 nil 时 Load 失败。
	loadErr error
}

// persistedCall 是观察到的一次落盘，按顺序记下来。
//
// 源: packages/settings/settings/tests/memory.ts:15
type persistedCall struct {
	namespace Namespace
	section   map[string]any
}

var _ Backend = (*memoryBackend)(nil)

// newMemoryBackend 造一个夹具，document 是「存储里已有的东西」。
func newMemoryBackend(t *testing.T, document map[string]any) *memoryBackend {
	t.Helper()

	return &memoryBackend{document: detachForTest(t, document), writable: true}
}

// Writable 源: packages/settings/settings/tests/memory.ts:33-35
func (m *memoryBackend) Writable() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.writable
}

// Load 源: packages/settings/settings/tests/memory.ts:37-39
func (m *memoryBackend) Load(context.Context) (map[string]any, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return cloneForTest(m.document), nil
}

// Persist 源: packages/settings/settings/tests/memory.ts:41-47
func (m *memoryBackend) Persist(_ context.Context, ns Namespace, section map[string]any) error {
	m.mutex.Lock()
	delay, failure := m.persistDelay, m.persistErr
	m.mutex.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if failure != nil {
		return failure
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.persisted = append(m.persisted, persistedCall{namespace: ns, section: cloneForTest(section)})
	m.document[string(ns)] = cloneForTest(section)
	return nil
}

// pushExternal 模拟一次绕过本服务的外部编辑：换掉存储，再让服务发布它。
//
// 源: packages/settings/settings/tests/memory.ts:49-53
func (m *memoryBackend) pushExternal(p *Provider, document map[string]any) {
	m.mutex.Lock()
	m.document = cloneForTest(document)
	snapshot := cloneForTest(document)
	m.mutex.Unlock()

	p.Publish(snapshot, SourceProvider)
}

// calls 返回观察到的落盘序列的副本。
func (m *memoryBackend) calls() []persistedCall {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return append([]persistedCall(nil), m.persisted...)
}

// stored 读夹具存储里某个命名空间当前的段。
func (m *memoryBackend) stored(ns Namespace) map[string]any {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	section, _ := m.document[string(ns)].(map[string]any)
	return cloneForTest(section)
}

// cloneForTest 在夹具内部脱钩一份数据。夹具必须和调用方共享不了内存，
// 否则一次「存储里没变」会因为共享了同一个 map 而看起来变了。
func cloneForTest(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		panic(err)
	}
	if copied == nil {
		return map[string]any{}
	}
	return copied
}

// detachForTest 是 cloneForTest 的会报错版本，给构造路径用。
func detachForTest(t *testing.T, value map[string]any) map[string]any {
	t.Helper()

	if value == nil {
		return map[string]any{}
	}
	return cloneForTest(value)
}
