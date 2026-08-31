// 本文件的作用：两张按插入顺序排的登记表，供作用域感知的注册表共用。
//
// 源: packages/core/scope/src/store.ts:1-150
//
// 两张表都遵守同一套约定：
//
//   - **顺序是插入顺序**，因为下游要按登记的先后决定谁先生效（工具的排列、
//     系统提示片段的拼接顺序）。Go 的 map 没有顺序，所以顺序必须自己存。
//   - **值是借来的**，表不拥有它们，也不复制它们。
//   - 每一次成功的登记都换回一个**幂等的、只撤销这一次登记**的 undo。
//     「只撤销这一次」很重要：撤销之后同名的东西又登记了一遍，旧的 undo 再被调到时
//     必须什么都不做，否则它会把新的那一份删掉。

package scope

import (
	"container/list"
	"iter"
	"sync"
)

// namedEntry 是 [NamedEntries] 里的一项。名字也存一份，是为了 [NamedEntries.All]
// 能在不回头查索引的情况下把名字给出去。
type namedEntry[V any] struct {
	name  string
	value V
}

// NamedEntries 是一张按插入顺序排的具名登记表，**重名怎么报错由调用方决定**。
//
// 源: packages/core/scope/src/store.ts:23-105
//
// 重名的诊断信息交给调用方给（构造时传进来的那个函数），是因为只有调用方知道
// 重的是什么：「工具 echo 已经注册过了」比「名字 echo 重复」有用得多，
// 而这张表自己压根不知道它装的是工具。
//
// 新增: 内部是「双向链表存顺序 + map 存索引」。用 container/list 而不是切片，
// 是因为一项登记在整表清空之前被单独撤销是常事，链表删除是 O(1) 且不打乱其余顺序；
// 切片要么留一堆墓碑，要么每删一次搬一遍后面的元素。
type NamedEntries[V any] struct {
	duplicateError func(name string) error

	mutex sync.Mutex
	order *list.List // 元素是 *namedEntry[V]
	index map[string]*list.Element
}

// NewNamedEntries 造一张具名登记表。duplicateError 为 nil 时用一条兜底的错误。
func NewNamedEntries[V any](duplicateError func(name string) error) *NamedEntries[V] {
	return &NamedEntries[V]{
		duplicateError: duplicateError,
		order:          list.New(),
		index:          map[string]*list.Element{},
	}
}

// Insert 登记一个在这张表里唯一的名字，返回只撤销这一次登记的幂等 undo。
//
// 源: packages/core/scope/src/store.ts:37-54
//
// 新增: DSH 那边重名是抛异常。Go 这边返回 error——重名是调用方传进来的数据决定的，
// 不该把进程 panic 掉。理由同 [BindParent]。
func (e *NamedEntries[V]) Insert(name string, value V) (func(), error) {
	e.mutex.Lock()

	if _, exists := e.index[name]; exists {
		e.mutex.Unlock()
		if e.duplicateError != nil {
			return nil, e.duplicateError(name)
		}
		return nil, &DuplicateNameError{Name: name}
	}

	element := e.order.PushBack(&namedEntry[V]{name: name, value: value})
	e.index[name] = element
	e.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.mutex.Lock()
			defer e.mutex.Unlock()
			// 只有当索引里那一项**仍然是这次登记的那个元素**时才删。
			// 撤销之后同名的东西又登记了一遍的话，索引指向的是新元素，这里就该什么都不做——
			// 否则这个旧 undo 会把新登记的那一份删掉。
			if current, exists := e.index[name]; exists && current == element {
				delete(e.index, name)
				e.order.Remove(element)
			}
		})
	}, nil
}

// DuplicateNameError 是 [NewNamedEntries] 没拿到诊断函数时的兜底重名错误。
type DuplicateNameError struct {
	Name string
}

func (err *DuplicateNameError) Error() string {
	return "scope: 名字 " + err.Name + " 在这张登记表里已经存在"
}

// Get 读一个名字对应的值。
//
// 源: packages/core/scope/src/store.ts:56-63
func (e *NamedEntries[V]) Get(name string) (V, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	element, exists := e.index[name]
	if !exists {
		var zero V
		return zero, false
	}
	return element.Value.(*namedEntry[V]).value, true
}

// Has 判断一个名字在不在表里。
//
// 源: packages/core/scope/src/store.ts:65-72
func (e *NamedEntries[V]) Has(name string) bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	_, exists := e.index[name]
	return exists
}

// snapshot 取一份当前顺序的快照。
//
// 新增: 这是本包和 DSH 之间一处**刻意的**语义差异。DSH 直接把 JS Map 的实时迭代器
// 交出去，于是一个还没读完的迭代器能看见后来的插入；它为此还得在表清空时换一个新 Map，
// 好把老迭代器和后来的内容隔开——那是在给「实时」这件事打补丁，不是在提供能力。
//
// Go 这边遍历的过程中会调到使用方的代码，而这张表是有锁的：在锁里回调使用方，
// 使用方一旦回头碰这张表就是死锁。所以先在锁内拷一份顺序，再在锁外交出去。
// 代价是遍历期间的插入看不见，收益是不会死锁、也不会在遍历中途被并发修改弄乱。
func (e *NamedEntries[V]) snapshot() []*namedEntry[V] {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	items := make([]*namedEntry[V], 0, e.order.Len())
	for element := e.order.Front(); element != nil; element = element.Next() {
		items = append(items, element.Value.(*namedEntry[V]))
	}
	return items
}

// Keys 按插入顺序遍历名字。
//
// 源: packages/core/scope/src/store.ts:74-80
func (e *NamedEntries[V]) Keys() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, item := range e.snapshot() {
			if !yield(item.name) {
				return
			}
		}
	}
}

// Values 按插入顺序遍历值。
//
// 源: packages/core/scope/src/store.ts:90-96
func (e *NamedEntries[V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, item := range e.snapshot() {
			if !yield(item.value) {
				return
			}
		}
	}
}

// All 按插入顺序遍历名字和值。
//
// 源: packages/core/scope/src/store.ts:82-88
//
// 新增: 名字叫 All 而不是照抄 Entries，是因为 Go 1.23 之后 iter.Seq2 的惯例名就是 All，
// range 一个 All() 是 Go 使用方一眼就懂的写法。
func (e *NamedEntries[V]) All() iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		for _, item := range e.snapshot() {
			if !yield(item.name, item.value) {
				return
			}
		}
	}
}

// IsEmpty 判断这张表一项都没有。
//
// 源: packages/core/scope/src/store.ts:98-104
func (e *NamedEntries[V]) IsEmpty() bool { return e.Len() == 0 }

// Len 给出表里的项数。
//
// 新增: DSH 只有 isEmpty。Go 这边多给一个 Len，因为 snapshot 已经要用它预分配，
// 而使用方问「有几项」时不该被迫去 range 一遍。
func (e *NamedEntries[V]) Len() int {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.order.Len()
}

// AnonymousEntries 是一张按插入顺序排的匿名登记表，**每一次登记都是独立的一份**。
//
// 源: packages/core/scope/src/store.ts:107-150
//
// 「独立」是这张表和 [NamedEntries] 的唯一区别：同一个值登记两次就是两份登记，
// 撤销其中一份不影响另一份。用得上它的是那些没有名字、只有先后的东西
// （中间件、拦截器、监听器）——它们本来就允许重复。
//
// 新增: DSH 用一个新造的 Symbol 当键来做到这一点。Go 这边链表元素自己就是唯一的，
// 不需要另造一个身份。
type AnonymousEntries[V any] struct {
	mutex sync.Mutex
	order *list.List // 元素是 V
}

// NewAnonymousEntries 造一张匿名登记表。
func NewAnonymousEntries[V any]() *AnonymousEntries[V] {
	return &AnonymousEntries[V]{order: list.New()}
}

// Append 追加一份独立的登记，返回只撤销这一次追加的幂等 undo。
//
// 源: packages/core/scope/src/store.ts:117-133
func (e *AnonymousEntries[V]) Append(value V) func() {
	e.mutex.Lock()
	element := e.order.PushBack(value)
	e.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.mutex.Lock()
			defer e.mutex.Unlock()
			e.order.Remove(element)
		})
	}
}

// Values 按插入顺序遍历值。快照语义，理由见 [NamedEntries] 的 snapshot。
//
// 源: packages/core/scope/src/store.ts:135-141
func (e *AnonymousEntries[V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		e.mutex.Lock()
		items := make([]V, 0, e.order.Len())
		for element := e.order.Front(); element != nil; element = element.Next() {
			items = append(items, element.Value.(V))
		}
		e.mutex.Unlock()

		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

// IsEmpty 判断这张表一项都没有。
//
// 源: packages/core/scope/src/store.ts:143-149
func (e *AnonymousEntries[V]) IsEmpty() bool { return e.Len() == 0 }

// Len 给出表里的项数。理由同 [NamedEntries.Len]。
func (e *AnonymousEntries[V]) Len() int {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.order.Len()
}
