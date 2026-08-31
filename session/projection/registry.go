// 本文件的作用：单元登记表本身——怎么登记、怎么把每一条已提交的事件推给所有
// 单元、以及从活会话上读出来的那三种面。
//
// 源: packages/session/session-projection/src/index.ts:163-495

package projection

import (
	"encoding/json"
	"fmt"
	"sync"

	"ds-harness-go/session"
)

// ChangeListener 是变更流的听众：一个会话上的一个单元的值变了。
//
// 源: packages/session/session-projection/src/index.ts:84-94
//
// value 是那个单元的 [Definition.View] 输出；seq 是发出这一声时那个单元的水位，
// 也就是引起这次变化的那条事件的 seq。
//
// 听众在 [Registry.Drive] 里被同步调到，而且**不持有本包的锁**，所以它可以
// 回头调 [Registry] 上的任何方法。它不该阻塞——推进整份日志的那条路正等着它返回。
type ChangeListener func(view SessionView, key string, value any, seq int)

// Registry 是投影单元表和它的驱动。
//
// 源: packages/session/session-projection/src/index.ts:163-495
//
// 每一条已提交的事件都要过一遍每个已登记单元的 [Definition.Apply]（急切驱动），
// 一个客户端可见的单元报告自己变了，就带着视图通知变更流。
//
// 单元格是懒建的：一个在事件已经流过之后才登记的单元、或者一个比这张表更老的
// 会话，会在第一次被碰到（推进或读取）的时候把 [Definition.Init] 在内存里的
// 日志上折一遍。
//
// 登记是有寿命的：注销函数一调，这个键就从后续的推进和读切里消失，
// 客户端把它读成「这个能力不在」。共用一个键的登记方是**计数**的，
// 同一个包挂在 N 个会话上就登记 N 次，键活到最后一个注销为止。
//
// 零值不可用，用 [NewRegistry] 建。它可以被多个 goroutine 同时使用，
// 但同一个会话的事件必须由**一个**调用方按 seq 顺序推进——那本来就是会话
// 自己的活，日志的追加顺序就是它。
type Registry struct {
	mu            sync.Mutex
	registrations map[string]*registration
	listeners     map[uint64]ChangeListener
	nextListener  uint64
}

// NewRegistry 建一张空表。
func NewRegistry() *Registry {
	return &Registry{
		registrations: map[string]*registration{},
		listeners:     map[uint64]ChangeListener{},
	}
}

// Register 把一个域的单元登进表里，返回的函数把它注销掉。
//
// 源: packages/session/session-projection/src/index.ts:195-263
//
// 注销函数是幂等的：多调几次和调一次一样。
//
// 新增: DSH 那边登记是挂在调用方 fiber 上的一个 effect，fiber 被销毁就自动
// 注销。Go 没有 fiber，所以按 Go 的老办法把注销函数交回给调用方
// （典型用法是紧跟一个 defer）。DSH 那个「注销函数只会被成功登记跑一次」的
// 前提在 Go 里不成立——一个 defer 加一次显式调用就破了它，而破了之后引用计数
// 会变成负数、把别人的键删掉，所以这里用 [sync.Once] 钉死。
//
// 它是一个函数不是方法，因为 Go 的方法不能带自己的类型参数。
func Register[S any](registry *Registry, definition Definition[S]) (func(), error) {
	switch {
	case definition.Key == "":
		return nil, fmt.Errorf("%w：投影键不许是空的", ErrInvalidDefinition)
	case definition.StateVersion < 0:
		return nil, fmt.Errorf("%w：投影键 %q 的状态版本号必须非负，给的是 %d",
			ErrInvalidDefinition, definition.Key, definition.StateVersion)
	case definition.Init == nil:
		return nil, fmt.Errorf("%w：投影键 %q 没给 Init", ErrInvalidDefinition, definition.Key)
	case definition.Apply == nil:
		return nil, fmt.Errorf("%w：投影键 %q 没给 Apply", ErrInvalidDefinition, definition.Key)
	case definition.DecodeState == nil:
		return nil, fmt.Errorf("%w：投影键 %q 没给 DecodeState", ErrInvalidDefinition, definition.Key)
	}

	erased := erase(definition)

	registry.mu.Lock()
	existing, ok := registry.registrations[erased.key]
	if ok {
		if existing.def.stateVersion != erased.stateVersion {
			registry.mu.Unlock()
			return nil, fmt.Errorf("%w：投影键 %q 已经登记在状态版本 %d 上，不能再按版本 %d 共用它",
				ErrStateVersionConflict, erased.key, existing.def.stateVersion, erased.stateVersion)
		}
		existing.refs++
	} else {
		registry.registrations[erased.key] = &registration{
			def:   erased,
			cells: map[session.SessionID]*unitCell{},
			refs:  1,
		}
	}
	registry.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			defer registry.mu.Unlock()

			// 这里不判「键还在不在」。[sync.Once] 保证这段每次成功的登记只跑一次，
			// 而删掉一个键的地方全世界只有这一段，所以它数进去的那条登记必然还在。
			// DSH 那边同样的位置挂着一条 `v8 ignore` 注释，理由一模一样。真要是
			// 哪天不在了，那是引用计数被别处改坏了——在这里当场炸掉比默默漏掉一次
			// 注销要好，后者会让一个早就该消失的投影继续出现在读切里。
			live := registry.registrations[erased.key]
			live.refs--
			if live.refs == 0 {
				delete(registry.registrations, erased.key)
			}
		})
	}, nil
}

// OnChanged 订阅变更流，返回的函数退订，幂等。
//
// 源: packages/session/session-projection/src/index.ts:265-279
func (r *Registry) OnChanged(listener ChangeListener) func() {
	r.mu.Lock()
	token := r.nextListener
	r.nextListener++
	r.listeners[token] = listener
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			delete(r.listeners, token)
		})
	}
}

// StateOf 读一个单元当前的宿主状态，不去算别的单元的视图。
// 第二个返回值为假表示这个键没登记。
//
// 源: packages/session/session-projection/src/index.ts:281-295
//
// 返回的是**活引用**，调用方不许改动它。
func (r *Registry) StateOf(view SessionView, key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, ok := r.registrations[key]
	if !ok {
		return nil, false
	}
	return r.cellForLocked(reg, view).state, true
}

// Snapshot 是对一个会话上所有已登记的客户端可见单元的一次一致读切，
// 从水位缓存里读（缺的单元格现折）。
//
// 源: packages/session/session-projection/src/index.ts:297-313
//
// 它是完全同步的：每一个值和 AsOfSeq 都反映同一个日志位置。
func (r *Registry) Snapshot(view SessionView) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	values := map[string]any{}
	for _, reg := range r.registrations {
		if reg.def.view == nil {
			continue
		}
		cell := r.cellForLocked(reg, view)
		values[reg.def.key] = reg.def.view(cell.state)
	}
	return Snapshot{AsOfSeq: view.NextSeq() - 1, Values: values}
}

// Checkpoint 给出一个会话上每一个已登记单元的状态级检查点，
// 从水位缓存里读（缺的单元格现折）。
//
// 源: packages/session/session-projection/src/index.ts:315-340
//
// 这是持久投影缓存的写侧：返回的行就是落盘那条
// (sessionId, key, ver, seq, val) 记录去掉外面两个键之后的部分。
//
// 每一个 Val 都是**排出去的字节**，绝不是活引用：水位缓存是这张表权威的可变
// 状态，一个拿到活引用的调用方能顺着它把后面每一次读切都改坏。顺带把
// 「状态必须是纯 JSON」这条单元契约变成了一条会报错的检查——排不出去就在这里
// 报，而不是等落盘那一刻。
func (r *Registry) Checkpoint(view SessionView) (Checkpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rows := Checkpoint{}
	for _, reg := range r.registrations {
		cell := r.cellForLocked(reg, view)
		encoded, err := json.Marshal(cell.state)
		if err != nil {
			return nil, fmt.Errorf("投影键 %q 的状态排不出去：%w", reg.def.key, err)
		}
		rows[reg.def.key] = CheckpointRow{
			Ver: reg.def.stateVersion,
			Seq: cell.observedSeq,
			Val: encoded,
		}
	}
	return rows, nil
}

// Forget 扔掉一个会话在所有单元上的水位缓存。
//
// 新增: DSH 把单元格挂在 `WeakMap<Session, UnitCell>` 上，会话对象被回收时
// 单元格跟着走。本包按会话身份存单元格（理由见包文档），所以清理点必须显式。
// 会话结束时调它；不调只会多占内存，不会读出错的值——下次再碰到这个身份，
// 单元格会照常在那时的日志上重折一遍。
func (r *Registry) Forget(id session.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, reg := range r.registrations {
		delete(reg.cells, id)
	}
}

// Drive 把一条已提交的事件推过每一个已登记的单元，报告变了的单元通知变更流。
//
// 源: packages/session/session-projection/src/index.ts:473-494
//
// 由会话那一侧在事件**提交之后**调，同一个会话必须按 seq 顺序、由一个调用方调。
func (r *Registry) Drive(view SessionView, event session.Event) {
	type notification struct {
		key   string
		value any
	}

	r.mu.Lock()
	var pending []notification
	var listeners []ChangeListener
	for _, reg := range r.registrations {
		cell, ok := reg.cells[view.ID()]
		if !ok {
			// 流到一半才建单元格：先把这条事件之前的历史折进去，再走正常的门。
			cell = buildCell(reg.def, eventsBefore(view.Events(), event.Seq))
			reg.cells[view.ID()] = cell
		}
		if event.Seq <= cell.observedSeq {
			// 这条已经折进去了。会走到这里是因为有人在「事件追加进日志」和
			// 「这里被调到」之间懒建了这个单元格，那次懒建折的是包含它的完整日志。
			// 再折一次就是把同一条事实数两遍。
			continue
		}
		next, changed := reg.def.apply(cell.state, event)
		cell.state = next
		cell.observedSeq = event.Seq
		if changed && reg.def.view != nil && len(r.listeners) > 0 {
			pending = append(pending, notification{key: reg.def.key, value: reg.def.view(next)})
		}
	}
	if len(pending) > 0 {
		listeners = make([]ChangeListener, 0, len(r.listeners))
		for _, listener := range r.listeners {
			listeners = append(listeners, listener)
		}
	}
	r.mu.Unlock()

	// 通知放到锁外发：听众是外面的代码，它回头调本表上的任何方法都不该死锁。
	for _, item := range pending {
		for _, listener := range listeners {
			listener(view, item.key, item.value, event.Seq)
		}
	}
}

// cellForLocked 读出（或者懒建，折的是内存里的完整日志）一个单元在这个会话上的
// 单元格。调用前必须持有 r.mu。
//
// 源: packages/session/session-projection/src/index.ts:463-471
func (r *Registry) cellForLocked(reg *registration, view SessionView) *unitCell {
	cell, ok := reg.cells[view.ID()]
	if !ok {
		cell = buildCell(reg.def, view.Events())
		reg.cells[view.ID()] = cell
	}
	return cell
}

// buildCell 把一个单元从 Init 在 events 上折一遍，水位落在最后折进去的那条事件上。
//
// 源: packages/session/session-projection/src/index.ts:456-461
func buildCell(def erasedDefinition, events []session.Event) *unitCell {
	state := def.init()
	observed := -1
	for _, event := range events {
		state, _ = def.apply(state, event)
		observed = event.Seq
	}
	return &unitCell{state: state, observedSeq: observed}
}

// eventsBefore 取出 seq 严格小于 boundary 的那一段前缀。
//
// 新增: DSH 写的是 `session.events.slice(0, event.seq)`，靠的是「seq 就是数组
// 下标」这条更强的前提。本仓库的 [session.Trace] 只保证 seq 严格递增，
// 不保证从 0 起密排，所以这里按 seq 比，不按下标切。
func eventsBefore(events []session.Event, boundary int) []session.Event {
	for index, event := range events {
		if event.Seq >= boundary {
			return events[:index]
		}
	}
	return events
}
