// 本文件的作用：一个已打开的域的运行期——权威内存态、那条**唯一**的写链、变更事件的发出。
//
// 源: packages/storage/storage-domain/src/domain.ts:1-10
//
// 三条不变量，整个包都建在它们上面：
//
//  1. **读是同步的**，直接从内存拿，不碰介质。
//  2. **写按顺序过同一条链**：先等后端确认落盘，**再**改内存，**再**发事件。
//  3. **落盘失败就什么都不动**：内存不变，事件不发。读到的东西和介质上的东西不会分叉。
//
// 第 2 条的次序不能换。先改内存再落盘的话，一次失败的写会在内存里留下一个介质上
// 根本不存在的值，而重启之后它凭空消失；先发事件再落盘的话，订阅者会收到一次
// 没有发生过的变更，并且照着它去做后续的事。

package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ds-harness-go/storage"
)

// record 是内存里一条记录的两面：登记方的类型化值，和它的 JSON 投影。
//
// 新增: DSH 只留解析后的值，事件里带的也是它。这里两面都留，各有各的用处：
// typed 面给 [Table] 的读，raw 面给 [Changed] 和不变量检查。
// 两面在同一次写里一起换掉，所以它们永远说的是同一件事——这正是本包那条
// 不变量（事件里的值 == 此刻的内存态）检查的东西。
type record struct {
	typed any
	raw   json.RawMessage
}

// tableState 是一张表的内存态。
type tableState struct {
	spec TableSpec
	// records 由 Domain.mutex 保护。
	records map[string]record
}

// Domain 是一个已打开的域。
//
// 源: packages/storage/storage-domain/src/domain.ts:96-119,140-277
//
// 零值不可用，只能由 [Facility.Open] 建出来。
//
// 新增: DSH 那边 Domain<S> 是一个由声明推导出类型的接口，DomainImpl 是它背后
// 那个不带类型的运行期，中间隔着一次类型擦除。Go 的类型参数不能这么用（方法带不了
// 类型参数，而表的记录类型是逐表不同的），所以拆法反过来：**Domain 本身不带类型**，
// 类型落在 [Table] 和 [Global] 这两个由 [TableOf] / [GlobalOf] 取出来的句柄上。
type Domain struct {
	facility *Facility
	spec     Spec
	unit     storage.KVUnit

	// writeMutex 就是这个域那条唯一的写链。
	//
	// 新增: DSH 用一条 promise 链排队，还得特意让每一环都 settle
	// （`result.then(noop, noop)`），否则一次失败的写会毒化后面所有人。
	// Go 的互斥量天生没有这个问题：失败的那次解锁了就完事。
	//
	// 唯一的差别是 DSH 的链严格先进先出，而 Go 的互斥量不保证等待者顺序。
	// 这一点不影响任何一条契约：删除和更新都在**轮到自己的那一刻**重新看内存，
	// 而两个真并发的调用之间本来就没有「谁先」可言。
	writeMutex sync.Mutex

	// gate 保护 closing，并且和 inflight 一起构成「新写拒掉、已排队的写放行」这道闸。
	//
	// 新增: DSH 在入队那一刻同步查 disposing，天然分得清「已经进队列了」和
	// 「关闭开始之后才来的」。Go 里 sync.Mutex 问不出「有几个人在等」，
	// 所以这件事显式地记：进队之前先在 gate 底下登记（inflight.Add），
	// 关闭时先立起 closing 再 inflight.Wait——立起之后不可能再有人 Add，
	// 因为 Add 和 closing 的检查在同一把锁底下。
	gate     sync.Mutex
	closing  bool
	inflight sync.WaitGroup

	closeOnce sync.Once
	closeErr  error

	mutex  sync.RWMutex
	closed bool
	// tables 建好之后**这张表本身**不再变（变的是每张表里的 records），
	// 所以按表名取 tableState 不需要加锁。
	tables map[string]*tableState
	global record
}

// Name 是这个域的域名。
//
// 源: packages/storage/storage-domain/src/domain.ts:98-99
func (d *Domain) Name() string { return d.spec.Name }

// TableNames 返回这个域声明的表名，按字典序排好，供诊断使用。
func (d *Domain) TableNames() []string {
	names := make([]string, 0, len(d.tables))
	for name := range d.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RawRecord 读一条记录的 JSON 投影，**不带类型**。
//
// 源: packages/storage/storage-domain/src/index.ts:158-167
//
// 这是诊断面，对应 DSH 那边 DomainFacility.get 交出来的那个不带类型的 DomainImpl：
// 本包自己的不变量检查要拿它和事件里的值对，而检查按定义不知道记录是什么 Go 类型。
// 正常的使用方手上有 [Table] 句柄，读那个。
func (d *Domain) RawRecord(table, key string) (json.RawMessage, bool, error) {
	state, declared := d.tables[table]
	if !declared {
		return nil, false, fmt.Errorf("storage/domain: 域 %q 没有声明表 %q", d.spec.Name, table)
	}
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	if d.closed {
		return nil, false, d.closedError()
	}
	stored, exists := state.records[key]
	if !exists {
		return nil, false, nil
	}
	return stored.raw, true, nil
}

// RawGlobal 读全局值的 JSON 投影，**不带类型**，用途同 [Domain.RawRecord]。
func (d *Domain) RawGlobal() (json.RawMessage, error) {
	if d.spec.Global == nil {
		return nil, fmt.Errorf("storage/domain: 域 %q 没有声明全局槽", d.spec.Name)
	}
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	if d.closed {
		return nil, d.closedError()
	}
	return d.global.raw, nil
}

// Close 关掉这个域。
//
// 源: packages/storage/storage-domain/src/domain.ts:110-118,226-244
//
// 次序是：立刻拒掉新的写 → 把**已经排上队**的写排干（它们的事件照发）→
// 释放后端单元 → 从这里开始读也拒掉 → 把域名让出来，可以重新打开。
//
// **幂等**：重复调用共用同一次拆解，后到的调用会等它做完再返回同一个结果。
//
// 生命周期归**拿到这个句柄的那一方**——设施不替谁管。设施卸载时会把还开着的
// 域一起关掉（见 [Facility.CloseAll]），那是兜底，不是常规路径。
func (d *Domain) Close(ctx context.Context) error {
	d.closeOnce.Do(func() { d.closeErr = d.runClose(ctx) })
	return d.closeErr
}

func (d *Domain) runClose(ctx context.Context) error {
	d.gate.Lock()
	d.closing = true
	d.gate.Unlock()

	// 排干：closing 立起来之后不会再有人登记进来，所以这个等待一定会结束
	// （前提是订阅者守住了「不许阻塞」那一条，见 [ChangedListener]）。
	d.inflight.Wait()

	err := d.unit.Close(ctx)

	// 新增: 单元关失败也照样把域标成关闭、照样把名字让出来。
	// DSH 那边这一步是 await，失败就直接抛出去，closed 和 onClosed 都跑不到——
	// 于是这个域**既写不进去**（closing 已经立起来了）**也重新打开不了**
	// （名字还被占着），整个进程只能重启。让出名字之后，重开会在后端那一层
	// 大声失败（单元还没释放），那是一个看得见的错误，比一个卡死的名字好。
	d.mutex.Lock()
	d.closed = true
	d.mutex.Unlock()
	d.facility.onClosed(d.spec.Name)

	if err != nil {
		return fmt.Errorf("storage/domain: 域 %q 释放后端单元失败：%w", d.spec.Name, err)
	}
	return nil
}

// enqueue 把一次写排进这个域的写链。
//
// 源: packages/storage/storage-domain/src/domain.ts:263-270
//
// 关闭开始之后来的写当场被拒；已经登记进来的写会跑完。
func (d *Domain) enqueue(job func() error) error {
	d.gate.Lock()
	if d.closing {
		d.gate.Unlock()
		return d.closedError()
	}
	d.inflight.Add(1)
	d.gate.Unlock()
	defer d.inflight.Done()

	d.writeMutex.Lock()
	defer d.writeMutex.Unlock()

	return job()
}

// closedError 是「这个域已经关了」那句话。
//
// 源: packages/storage/storage-domain/src/domain.ts:272-276
func (d *Domain) closedError() error {
	return newError(CodeClosed, "域 %q 已经关了", d.spec.Name)
}

// emitChanged 发一条已经落盘的变更通知。
//
// 源: packages/storage/storage-domain/src/domain.ts:246-261
//
// 订阅者炸掉只记日志，不往上报：提交点已经过了，介质和内存都拿着新值，
// 一次已经成功的写不该因为旁边有人看崩了就变成失败。
func (d *Domain) emitChanged(change Changed) {
	d.facility.emit(change)
}

// Entry 是 [Table.Entries] 里的一条。
//
// 新增: DSH 那边 entries() 给的是 [K, V] 二元组的迭代器。Go 没有元组，
// 具名结构体比 struct{string; V} 更好读，也让调用方能写 e.Key / e.Value。
type Entry[V any] struct {
	Key   string
	Value V
}

// Table 是一张已声明表的类型化句柄。
//
// 源: packages/storage/storage-domain/src/domain.ts:37-90
//
// 记录是**不可变数据**：读到的就是存着的那个值，没有防御性复制。
// 值里带指针或者切片的话，原地改它会绕过整条写链（介质上还是旧的，事件也不会发），
// 要换就走 [Table.Put] 或 [Table.Update]。
type Table[V any] struct {
	domain *Domain
	state  *tableState
}

// TableOf 取出一张已声明表的类型化句柄。
//
// 源: packages/storage/storage-domain/src/domain.ts:104-108,211-223
//
// 没声明过的表名，或者 V 和声明时的记录类型对不上，都返回错误——两者都是调用方的 bug。
//
// 新增: DSH 靠 `keyof S['tables']` 和条件类型在编译期挡住这两件事，一个字都不用跑。
// Go 的方法带不了类型参数，表名又是运行期字符串，所以只能在运行期核对；
// 核对的是 reflect.TypeFor[V]() 和声明时记下的那个类型，不核对的话
// 一个写错的 V 会在读到第一条记录时才 panic，而那时候离声明处已经很远了。
//
// 句柄本身不带状态，每次取都新建一个是廉价的；它们指向同一份内存态，
// 所以和 DSH「重复调用返回同一个实例」在行为上没有差别。
func TableOf[V any](d *Domain, name string) (*Table[V], error) {
	state, declared := d.tables[name]
	if !declared {
		return nil, fmt.Errorf("storage/domain: 域 %q 没有声明表 %q（声明了的：%s）",
			d.spec.Name, name, describeNames(d.TableNames()))
	}
	want := reflect.TypeFor[V]()
	if state.spec.valueType != want {
		return nil, fmt.Errorf("storage/domain: 域 %q 的表 %q 记录类型是 %s，这次要的是 %s",
			d.spec.Name, name, state.spec.valueType, want)
	}
	return &Table[V]{domain: d, state: state}, nil
}

// Get 读一条记录，同步地从内存里拿。
//
// 源: packages/storage/storage-domain/src/domain.ts:44-48,287-290
//
// 第二个返回值是「在不在」。域已经关掉时返回 [CodeClosed]。
//
// 新增: DSH 的 get 只返回值，关掉之后 throw。Go 这边多一个 error 返回值，
// 而不是「关掉之后一律返回不存在」——后者会让一个拿着过期句柄的调用方
// 安静地读到一张空表，然后照着这个结论往下走，而它永远不会知道自己读的是一个死域。
func (t *Table[V]) Get(key string) (V, bool, error) {
	var zero V
	t.domain.mutex.RLock()
	defer t.domain.mutex.RUnlock()
	if t.domain.closed {
		return zero, false, t.domain.closedError()
	}
	stored, exists := t.state.records[key]
	if !exists {
		return zero, false, nil
	}
	// 断言必然成立：records 里的 typed 全部由这张表的 decode/encode 产出，
	// 而 [TableOf] 已经核对过 V 就是声明时的类型。
	typed, _ := stored.typed.(V)
	return typed, true, nil
}

// Keys 返回当前的全部记录键，是一份**快照**。
//
// 源: packages/storage/storage-domain/src/domain.ts:56-61,297-300
//
// 新增: 按字典序排好。DSH 给的是 Map 的插入序；Go 的 map 遍历顺序是**故意随机**的，
// 原样交出去的话同一份数据两次调用给出的顺序都不一样，翻页、诊断输出和测试断言
// 都没法用。排序是 Go 这边唯一稳定又不用额外记账的顺序。
func (t *Table[V]) Keys() ([]string, error) {
	t.domain.mutex.RLock()
	defer t.domain.mutex.RUnlock()
	if t.domain.closed {
		return nil, t.domain.closedError()
	}
	keys := make([]string, 0, len(t.state.records))
	for key := range t.state.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// Entries 返回当前的全部记录，是一份**快照**，顺序同 [Table.Keys]。
//
// 源: packages/storage/storage-domain/src/domain.ts:50-55,292-295
//
// 快照而不是活视图：拿到之后排队中的写照样落地，但手上这一份不会在遍历途中变形。
func (t *Table[V]) Entries() ([]Entry[V], error) {
	t.domain.mutex.RLock()
	defer t.domain.mutex.RUnlock()
	if t.domain.closed {
		return nil, t.domain.closedError()
	}
	keys := make([]string, 0, len(t.state.records))
	for key := range t.state.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]Entry[V], 0, len(keys))
	for _, key := range keys {
		typed, _ := t.state.records[key].typed.(V)
		entries = append(entries, Entry[V]{Key: key, Value: typed})
	}
	return entries, nil
}

// Size 是当前的记录条数。
//
// 源: packages/storage/storage-domain/src/domain.ts:63-64,302-305
func (t *Table[V]) Size() (int, error) {
	t.domain.mutex.RLock()
	defer t.domain.mutex.RUnlock()
	if t.domain.closed {
		return 0, t.domain.closedError()
	}
	return len(t.state.records), nil
}

// Put 持久地写入或覆盖一条记录。
//
// 源: packages/storage/storage-domain/src/domain.ts:66-72,307-313
//
// 写的是**整条记录**，没有部分合并。返回时落盘已经完成、内存已经换掉、事件已经发过。
func (t *Table[V]) Put(ctx context.Context, key string, value V) error {
	return t.domain.enqueue(func() error {
		raw, err := t.state.spec.encode(value)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 的表 %q 写记录 %q 失败：%w",
				t.domain.spec.Name, t.state.spec.name, key, err)
		}
		return t.store(ctx, key, value, raw)
	})
}

// Delete 持久地删掉一条记录。
//
// 源: packages/storage/storage-domain/src/domain.ts:74-80,315-330
//
// 返回值是「删之前它在不在」。不在的话**不写也不发事件**——把一次空操作
// 说成一次变更，会让订阅者以为有东西没了。
func (t *Table[V]) Delete(ctx context.Context, key string) (bool, error) {
	deleted := false
	err := t.domain.enqueue(func() error {
		// 「在不在」在**轮到这一步的时刻**判定，不在调用时刻判定：
		// 排在前面的一次同键写入，这次删除必须看得见。
		t.domain.mutex.RLock()
		_, exists := t.state.records[key]
		t.domain.mutex.RUnlock()
		if !exists {
			return nil
		}
		if err := t.domain.unit.DeleteRecord(ctx, t.state.spec.name, key); err != nil {
			return err
		}
		t.domain.mutex.Lock()
		delete(t.state.records, key)
		t.domain.mutex.Unlock()

		deleted = true
		t.domain.emitChanged(Changed{
			Domain:    t.domain.spec.Name,
			Table:     t.state.spec.name,
			Key:       key,
			Operation: OperationDeleted,
		})
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// Update 在写链上做一次原子的读-改-写。
//
// 源: packages/storage/storage-domain/src/domain.ts:82-89,332-346
//
// fn 看到的是**轮到它的那一刻**的值，所以并发的更新不会交错——这正是它比
// 「自己 Get 再 Put」强的地方，后者中间隔着一个别人能插进来的窗口。
//
// 键不存在时返回 [CodeMissingKey]。fn 返回错误时什么都不写。
func (t *Table[V]) Update(ctx context.Context, key string, fn func(current V) (V, error)) (V, error) {
	var next V
	err := t.domain.enqueue(func() error {
		t.domain.mutex.RLock()
		stored, exists := t.state.records[key]
		t.domain.mutex.RUnlock()
		if !exists {
			return newError(CodeMissingKey, "域 %q 的表 %q 里没有记录 %q 可以更新",
				t.domain.spec.Name, t.state.spec.name, key)
		}
		current, _ := stored.typed.(V)

		updated, err := fn(current)
		if err != nil {
			return err
		}
		raw, err := t.state.spec.encode(updated)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 的表 %q 更新记录 %q 失败：%w",
				t.domain.spec.Name, t.state.spec.name, key, err)
		}
		if err := t.store(ctx, key, updated, raw); err != nil {
			return err
		}
		next = updated
		return nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return next, nil
}

// store 是 [Table.Put] 和 [Table.Update] 共用的那三步：落盘 → 换内存 → 发事件。
//
// 调用方必须已经在写链的槽位里（也就是在 [Domain.enqueue] 的 job 里）。
func (t *Table[V]) store(ctx context.Context, key string, value V, raw json.RawMessage) error {
	if err := t.domain.unit.PutRecord(ctx, t.state.spec.name, key, raw); err != nil {
		return err
	}
	t.domain.mutex.Lock()
	t.state.records[key] = record{typed: value, raw: raw}
	t.domain.mutex.Unlock()

	t.domain.emitChanged(Changed{
		Domain:    t.domain.spec.Name,
		Table:     t.state.spec.name,
		Key:       key,
		Operation: OperationPut,
		Value:     raw,
	})
	return nil
}

// Global 是全局单例槽的类型化句柄。
//
// 源: packages/storage/storage-domain/src/domain.ts:18-35
type Global[G any] struct {
	domain *Domain
	spec   *GlobalSpec
}

// GlobalOf 取出全局单例槽的类型化句柄。
//
// 源: packages/storage/storage-domain/src/domain.ts:92-94,203-209
//
// 没声明全局槽、或者 G 和声明时的类型对不上，都返回错误，理由同 [TableOf]。
func GlobalOf[G any](d *Domain) (*Global[G], error) {
	if d.spec.Global == nil {
		return nil, fmt.Errorf("storage/domain: 域 %q 没有声明全局槽", d.spec.Name)
	}
	want := reflect.TypeFor[G]()
	if d.spec.Global.valueType != want {
		return nil, fmt.Errorf("storage/domain: 域 %q 的全局值类型是 %s，这次要的是 %s",
			d.spec.Name, d.spec.Global.valueType, want)
	}
	return &Global[G]{domain: d, spec: d.spec.Global}, nil
}

// Get 读当前的全局值，同步地从内存里拿。第一次 [Global.Set] 之前它是声明里的初值。
//
// 源: packages/storage/storage-domain/src/domain.ts:20-25,190-193
func (g *Global[G]) Get() (G, error) {
	var zero G
	g.domain.mutex.RLock()
	defer g.domain.mutex.RUnlock()
	if g.domain.closed {
		return zero, g.domain.closedError()
	}
	// 断言必然成立，理由同 [Table.Get]。
	typed, _ := g.domain.global.typed.(G)
	return typed, nil
}

// Set 持久地换掉全局值。
//
// 源: packages/storage/storage-domain/src/domain.ts:27-34,194-198
//
// **第一次 Set 才是把全局槽真正落到介质上的那一刻**——在那之前介质上是空的，
// 读到的是声明里的初值。
func (g *Global[G]) Set(ctx context.Context, value G) error {
	return g.domain.enqueue(func() error {
		raw, err := g.spec.encode(value)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 写全局值失败：%w", g.domain.spec.Name, err)
		}
		if err := g.domain.unit.SetGlobal(ctx, raw); err != nil {
			return err
		}
		g.domain.mutex.Lock()
		g.domain.global = record{typed: value, raw: raw}
		g.domain.mutex.Unlock()

		g.domain.emitChanged(Changed{
			Domain:    g.domain.spec.Name,
			Operation: OperationPut,
			Value:     raw,
		})
		return nil
	})
}
