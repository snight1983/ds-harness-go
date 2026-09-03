// 本文件的作用：一个已打开的域的运行期——读穿到介质、那条**唯一**的写链、变更事件的发出。
//
// 源: packages/storage/storage-domain/src/domain.ts:1-10
//
// 三条不变量，整个包都建在它们上面：
//
//  1. **本包不持有权威状态**：每一次读都穿到介质。
//  2. **写按顺序过同一条链**：先等后端确认落盘，**再**发事件。
//  3. **落盘失败就什么都不动**：事件不发。
//
// 新增: 第 1 条是本仓库改掉的。DSH 那边写的是「读是同步的，直接从内存拿，不碰介质」，
// 因为它是一个单进程的桌面工具——进程内存就是权威，没有第二个写者。这个服务是多副本
// 部署的：A 副本写下的记录，B 副本的内存里不会有；照着内存回答，B 会说「没有这个东西」。
// 所以权威搬回介质，内存里一份都不留。
//
// 代价是每一次读都是一次往返，所以所有的读都收 ctx——它们会失败、会超时、会被取消，
// 而一个不收 ctx 的读没地方说这三件事。
//
// 第 2 条的次序不能换：先发事件再落盘的话，订阅者会收到一次没有发生过的变更，
// 并且照着它去做后续的事。
//
// 第 2 条那条写链只把**本副本**的写串起来。它挡不住别的副本——那件事由
// [Table.Update] 上的条件写挡，见那里。

package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/snight1983/ds-harness-go/storage"
)

// updateAttempts 是 [Table.Update] 撞上别的副本时重试几次。
//
// 新增: 上限存在的理由是「一直重试」和「死循环」在现场分不出来：一条被别的副本
// 高频改写的记录会让调用方永远停在这里，而它看起来只是慢。撞上限之后原样交回
// 后端那条 [storage.CodeStaleRevision]，调用方自己决定是重来还是放弃。
//
// 取 5 是因为每一轮都真的重读了一次最新值：连着五次都恰好被别人插进来，
// 说明这条记录本来就在被抢，再多试几次也换不来别的结果。
const updateAttempts = 5

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
	// 这一点不影响任何一条契约：删除和更新都在**轮到自己的那一刻**才去读介质，
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

	// mutex 只保护 closed 这一个字段。
	//
	// 新增: 它以前还保护那几张内存表。表没了之后剩下的就只有这个标志位——
	// 留着一把锁而不是换成原子布尔，是因为「读到 closed」和「拒掉这次调用」
	// 之间不能被 [Domain.runClose] 插进来。
	mutex  sync.RWMutex
	closed bool
	// tables 是按表名索引的声明，建好之后不再变，所以取它不需要加锁。
	tables map[string]TableSpec
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
func (d *Domain) RawRecord(
	ctx context.Context, table, key string,
) (json.RawMessage, storage.Revision, bool, error) {
	if _, declared := d.tables[table]; !declared {
		return nil, "", false, fmt.Errorf("storage/domain: 域 %q 没有声明表 %q", d.spec.Name, table)
	}
	if err := d.live(); err != nil {
		return nil, "", false, err
	}
	return d.unit.ReadRecord(ctx, table, key)
}

// RawGlobal 读全局值的 JSON 投影，**不带类型**，用途同 [Domain.RawRecord]。
//
// 介质上还没写过全局槽时交回的是声明里那份初值的投影，和 [Global.Get] 一致——
// 这两条路要是对同一个「还没写过」给出不同的答案，不变量检查会抓着一个假问题不放。
func (d *Domain) RawGlobal(ctx context.Context) (json.RawMessage, storage.Revision, error) {
	if d.spec.Global == nil {
		return nil, "", fmt.Errorf("storage/domain: 域 %q 没有声明全局槽", d.spec.Name)
	}
	if err := d.live(); err != nil {
		return nil, "", err
	}
	raw, revision, err := d.unit.ReadGlobal(ctx)
	if err != nil {
		return nil, "", err
	}
	if isJSONNull(raw) {
		return d.spec.Global.initialRaw, "", nil
	}
	return raw, revision, nil
}

// live 判这个域此刻还开着没有。
func (d *Domain) live() error {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	if d.closed {
		return d.closedError()
	}
	return nil
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
// 订阅者炸掉只记日志，不往上报：提交点已经过了，介质上拿着的就是新值，
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
// 每次读都从介质上重新解出一个值，所以调用方拿到的那一份是它自己的：原地改它
// 既不会碰到介质，也不会被别的读者看见。要让改动生效就走 [Table.Put] 或 [Table.Update]。
type Table[V any] struct {
	domain *Domain
	spec   TableSpec
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
// 句柄本身不带状态，每次取都新建一个是廉价的；它们读写的是同一份介质，
// 所以和 DSH「重复调用返回同一个实例」在行为上没有差别。
func TableOf[V any](d *Domain, name string) (*Table[V], error) {
	spec, declared := d.tables[name]
	if !declared {
		return nil, fmt.Errorf("storage/domain: 域 %q 没有声明表 %q（声明了的：%s）",
			d.spec.Name, name, describeNames(d.TableNames()))
	}
	want := reflect.TypeFor[V]()
	if spec.valueType != want {
		return nil, fmt.Errorf("storage/domain: 域 %q 的表 %q 记录类型是 %s，这次要的是 %s",
			d.spec.Name, name, spec.valueType, want)
	}
	return &Table[V]{domain: d, spec: spec}, nil
}

// Get 读一条记录，穿到介质上去读。
//
// 源: packages/storage/storage-domain/src/domain.ts:44-48,287-290
//
// 第二个返回值是「在不在」。域已经关掉时返回 [CodeClosed]。
//
// 新增: DSH 的 get 不收 ctx、只返回值，关掉之后 throw。Go 这边收 ctx 是因为这次读
// 真的要往返一趟；多一个 error 返回值而不是「关掉之后一律返回不存在」，
// 是因为后者会让一个拿着过期句柄的调用方安静地读到一张空表，然后照着这个结论
// 往下走，而它永远不会知道自己读的是一个死域。
func (t *Table[V]) Get(ctx context.Context, key string) (V, bool, error) {
	var zero V
	value, _, found, err := t.read(ctx, key)
	if err != nil || !found {
		return zero, false, err
	}
	return value, true, nil
}

// read 读一条记录并解出类型化的值，同时把它此刻的修订标识带回来。
//
// 修订标识只给 [Table.Update] 用：它是读-改-写唯一说得清「我改的是哪一版」的东西。
func (t *Table[V]) read(ctx context.Context, key string) (V, storage.Revision, bool, error) {
	var zero V
	if err := t.domain.live(); err != nil {
		return zero, "", false, err
	}
	raw, revision, found, err := t.domain.unit.ReadRecord(ctx, t.spec.name, key)
	if err != nil {
		return zero, "", false, err
	}
	if !found {
		return zero, "", false, nil
	}
	decoded, err := t.spec.decode(raw)
	if err != nil {
		return zero, "", false, invalidRecord(t.domain.spec.Name, t.spec.name, key, err)
	}
	// 断言必然成立：decode 由这张表的声明产出，而 [TableOf] 已经核对过 V 就是声明时的类型。
	typed, _ := decoded.(V)
	return typed, revision, true, nil
}

// Keys 返回当前的全部记录键，是一份**快照**。
//
// 源: packages/storage/storage-domain/src/domain.ts:56-61,297-300
//
// 新增: 按字典序排好。DSH 给的是 Map 的插入序；Go 的 map 遍历顺序是**故意随机**的，
// 原样交出去的话同一份数据两次调用给出的顺序都不一样，翻页、诊断输出和测试断言
// 都没法用。排序是 Go 这边唯一稳定又不用额外记账的顺序。
func (t *Table[V]) Keys(ctx context.Context) ([]string, error) {
	records, err := t.loadTable(ctx)
	if err != nil {
		return nil, err
	}
	return sortedKeys(records), nil
}

// Entries 返回当前的全部记录，是一份**快照**，顺序同 [Table.Keys]。
//
// 源: packages/storage/storage-domain/src/domain.ts:50-55,292-295
//
// 快照而不是活视图：拿到之后落下去的写不会让手上这一份在遍历途中变形。
func (t *Table[V]) Entries(ctx context.Context) ([]Entry[V], error) {
	records, err := t.loadTable(ctx)
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(records)

	entries := make([]Entry[V], 0, len(keys))
	for _, key := range keys {
		decoded, decodeErr := t.spec.decode(records[key])
		if decodeErr != nil {
			return nil, invalidRecord(t.domain.spec.Name, t.spec.name, key, decodeErr)
		}
		typed, _ := decoded.(V)
		entries = append(entries, Entry[V]{Key: key, Value: typed})
	}
	return entries, nil
}

// Size 是当前的记录条数。
//
// 源: packages/storage/storage-domain/src/domain.ts:63-64,302-305
func (t *Table[V]) Size(ctx context.Context) (int, error) {
	records, err := t.loadTable(ctx)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// loadTable 从介质上取这张表当前的全部记录。
//
// 新增: 走 [storage.KVUnit.LoadTable] 而不是整单元快照。这三个方法要的从来只有
// 一张表，而一个域里可以声明好几张——读整份快照会把其余的表全部白读一遍，
// 那份浪费随域里的表数和它们的大小一起长。
//
// 代价是这一份和同一个域里别的表**不保证同一时刻**。这三个方法本来也不承诺跨表
// 一致：调用方拿到的是一张表的快照，不是一个域的快照。真要跨表一致的，
// 那是另一个方法，本包目前没有人要。
func (t *Table[V]) loadTable(ctx context.Context) (map[string]json.RawMessage, error) {
	if err := t.domain.live(); err != nil {
		return nil, err
	}
	records, err := t.domain.unit.LoadTable(ctx, t.spec.name)
	if err != nil {
		return nil, err
	}
	// 契约要求空表也在场（见 [storage.KVUnit.LoadTable]），所以 nil 只可能是后端违约。
	if records == nil {
		return nil, fmt.Errorf("storage/domain: 域 %q 的表 %q 交回了一个 nil",
			t.domain.spec.Name, t.spec.name)
	}
	return records, nil
}

// sortedKeys 把一份记录的键排好交出去，理由见 [Table.Keys]。
func sortedKeys(records map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Put 持久地写入或覆盖一条记录。
//
// 源: packages/storage/storage-domain/src/domain.ts:66-72,307-313
//
// 写的是**整条记录**，没有部分合并。返回时落盘已经完成、事件已经发过。
//
// **无条件覆盖**：别的副本在这中间写过什么，这一次照盖不误。要「只在没人动过时才写」
// 就走 [Table.Update]。
func (t *Table[V]) Put(ctx context.Context, key string, value V) error {
	return t.domain.enqueue(func() error {
		raw, err := t.spec.encode(value)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 的表 %q 写记录 %q 失败：%w",
				t.domain.spec.Name, t.spec.name, key, err)
		}
		return t.store(ctx, key, raw, nil)
	})
}

// Create 只在这条记录**此刻不存在**时写进去。
//
// 新增: DSH 没有这个方法——它那边一个进程就是全部的写者，「先查一次再 Put」中间
// 没有别人能插进来，所以这件事用 get + put 就表达得了。多副本之下那两步之间正是
// 另一个副本插进来的地方，所以这里落成后端那一次原子的条件写
// （见 [storage.CreateIfAbsent]）。
//
// 已经存在时返回后端那条 [storage.CodeStaleRevision]，**不写也不发事件**。
// 调用方要的正是这个：一次「谁先写进去谁赢」的竞争，输的那一方必须知道自己输了。
func (t *Table[V]) Create(ctx context.Context, key string, value V) error {
	return t.domain.enqueue(func() error {
		raw, err := t.spec.encode(value)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 的表 %q 建记录 %q 失败：%w",
				t.domain.spec.Name, t.spec.name, key, err)
		}
		return t.store(ctx, key, raw, storage.CreateIfAbsent{})
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
		if err := t.domain.live(); err != nil {
			return err
		}
		// 「删之前在不在」由后端在真正执行删除的那一步回答——权威在介质上，
		// 这一侧先读一次再删的话，两步之间别的副本插进来，回答就是错的。
		existed, err := t.domain.unit.DeleteRecord(ctx, t.spec.name, key, nil)
		if err != nil {
			return err
		}
		if !existed {
			return nil
		}
		deleted = true
		t.domain.emitChanged(Changed{
			Domain:    t.domain.spec.Name,
			Table:     t.spec.name,
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
//
// 新增: 写回时带上读到的那一版做条件（见 [storage.ReplaceIfRevision]）。写链只把
// **本副本**的写串起来，别的副本随时可以插在读和写之间；不带条件的话那次写会把
// 对方刚落下的改动整个盖掉，而两边都返回了成功——这正是丢更新。
//
// 条件不成立就重读重试，最多 [updateAttempts] 轮，每一轮 fn 都拿到重读之后的值，
// 所以 fn 可能被调用多次，它不该有副作用。撞上限之后原样交回后端那条
// [storage.CodeStaleRevision]。
func (t *Table[V]) Update(ctx context.Context, key string, fn func(current V) (V, error)) (V, error) {
	var next V
	err := t.domain.enqueue(func() error {
		var lastConflict error
		for attempt := 0; attempt < updateAttempts; attempt++ {
			current, revision, found, err := t.read(ctx, key)
			if err != nil {
				return err
			}
			if !found {
				return newError(CodeMissingKey, "域 %q 的表 %q 里没有记录 %q 可以更新",
					t.domain.spec.Name, t.spec.name, key)
			}

			updated, err := fn(current)
			if err != nil {
				return err
			}
			raw, err := t.spec.encode(updated)
			if err != nil {
				return fmt.Errorf("storage/domain: 域 %q 的表 %q 更新记录 %q 失败：%w",
					t.domain.spec.Name, t.spec.name, key, err)
			}

			err = t.store(ctx, key, raw, storage.ReplaceIfRevision{Revision: revision})
			if err == nil {
				next = updated
				return nil
			}
			// 只有「有人抢先动了」才值得重来。别的失败（介质坏了、连不上、
			// 已经关了）重试多少次都是同一个结果。
			var typed *storage.Error
			if !errors.As(err, &typed) || typed.Code != storage.CodeStaleRevision {
				return err
			}
			lastConflict = err
		}
		return lastConflict
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return next, nil
}

// store 是 [Table.Put] 和 [Table.Update] 共用的那两步：落盘 → 发事件。
//
// expected 为 nil 就是无条件覆盖。调用方必须已经在写链的槽位里
// （也就是在 [Domain.enqueue] 的 job 里）。
func (t *Table[V]) store(
	ctx context.Context, key string, raw json.RawMessage, expected storage.WriteIntent,
) error {
	if err := t.domain.live(); err != nil {
		return err
	}
	revision, err := t.domain.unit.PutRecord(ctx, t.spec.name, key, raw, expected)
	if err != nil {
		return err
	}
	t.domain.emitChanged(Changed{
		Domain:    t.domain.spec.Name,
		Table:     t.spec.name,
		Key:       key,
		Operation: OperationPut,
		Value:     raw,
		Revision:  revision,
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

// Get 读当前的全局值，穿到介质上去读。第一次 [Global.Set] 之前它是声明里的初值。
//
// 源: packages/storage/storage-domain/src/domain.ts:20-25,190-193
//
// 新增: 收 ctx 的理由同 [Table.Get]——这次读真的要往返一趟。
func (g *Global[G]) Get(ctx context.Context) (G, error) {
	value, _, err := g.read(ctx)
	return value, err
}

// read 读全局值并把它此刻的修订标识带回来，用途同 [Table.read]。
//
// 介质上还没写过时交回的是声明里那份初值，修订标识是空串——空串在
// [storage.ReplaceIfRevision] 里对不上任何一版，所以拿它去守卫一次写一定被拒，
// 而那正是想要的：「我读到的是初值」这件事守不住，只能走 [storage.CreateIfAbsent]。
func (g *Global[G]) read(ctx context.Context) (G, storage.Revision, error) {
	var zero G
	if err := g.domain.live(); err != nil {
		return zero, "", err
	}
	raw, revision, err := g.domain.unit.ReadGlobal(ctx)
	if err != nil {
		return zero, "", err
	}
	if isJSONNull(raw) {
		// 断言必然成立：initial 由这个域的声明产出，而 [GlobalOf] 已经核对过 G。
		typed, _ := g.spec.initial.(G)
		return typed, "", nil
	}
	decoded, err := g.spec.decode(raw)
	if err != nil {
		// 两个空串是全局槽在 [RecordSlot] 里的约定，同 [Changed]。
		return zero, "", invalidRecord(g.domain.spec.Name, "", "", err)
	}
	typed, _ := decoded.(G)
	return typed, revision, nil
}

// Set 持久地换掉全局值。
//
// 源: packages/storage/storage-domain/src/domain.ts:27-34,194-198
//
// **第一次 Set 才是把全局槽真正落到介质上的那一刻**——在那之前介质上是空的，
// 读到的是声明里的初值。
func (g *Global[G]) Set(ctx context.Context, value G) error {
	return g.domain.enqueue(func() error {
		if err := g.domain.live(); err != nil {
			return err
		}
		raw, err := g.spec.encode(value)
		if err != nil {
			return fmt.Errorf("storage/domain: 域 %q 写全局值失败：%w", g.domain.spec.Name, err)
		}
		// 无条件覆盖，理由同 [Table.Put]。全局槽没有 Update 那条读-改-写的路，
		// 需要守卫的调用方自己拿 [Domain.RawGlobal] 的修订标识去 SetGlobal。
		revision, err := g.domain.unit.SetGlobal(ctx, raw, nil)
		if err != nil {
			return err
		}
		g.domain.emitChanged(Changed{
			Domain:    g.domain.spec.Name,
			Operation: OperationPut,
			Value:     raw,
			Revision:  revision,
		})
		return nil
	})
}
