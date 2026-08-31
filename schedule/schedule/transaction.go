// 本文件的作用：同一个 agent 上的提醒读写为什么必须一件一件来，以及在 Go 里
// 这件事该用什么做。
//
// 源: packages/schedule/schedule/src/transaction.ts

package schedule

import (
	"context"
	"sync"

	"ds-harness-go/core/agent"
)

// transactions 是「一个 agent 一把闸」。
//
// 源: packages/schedule/schedule/src/transaction.ts:5
//
// 为什么要串行：本包每一次改动都是「过屏障 → 折日志 → 落事件 → 再过屏障」这么
// 一整段。两段并排跑的话，两边都会折到同一份日志、都算出同一个下一个 id，然后
// 各写一条 create——回放时那就是一次 id 重用，整条日志当场作废。
//
// 新增: DSH 拿 `WeakMap<Agent, Promise<void>>` 把每一次操作接在前一次的尾巴上，
// 靠垃圾回收把死掉的 agent 抹掉。Go 里同一件事就是一把每个 agent 一份的闸，
// 拿一个容量为一的 channel 做——比 [sync.Mutex] 多的那一点是它**认 ctx**：
// 一个还没轮到自己就已经被取消的调用可以当场退出，那正是 DSH 在轮到自己之后
// 才补做的那次取消检查，而且早做一步。
//
// 闸上带引用计数，最后一个用它的人走掉时整条记录删掉：那是 WeakMap 那条弱引用
// 在 Go 里的等价物——没有它，一台长跑的进程会为每一个来过又走了的 agent 永久
// 留一条记录。
type transactions struct {
	mutex sync.Mutex
	gates map[agent.Agent]*transactionGate
}

// transactionGate 是一个 agent 的那把闸。
type transactionGate struct {
	// slot 装得下一个令牌；拿到令牌的那一个独占这个 agent。
	slot chan struct{}
	// refs 是此刻正拿着或者正在等这把闸的调用数。
	refs int
}

// newTransactions 造一份空的闸表。
func newTransactions() *transactions {
	return &transactions{gates: make(map[agent.Agent]*transactionGate)}
}

// acquire 取出（必要时新建）这个 agent 的闸，并把它的引用计数加一。
func (t *transactions) acquire(owner agent.Agent) *transactionGate {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	gate, present := t.gates[owner]
	if !present {
		gate = &transactionGate{slot: make(chan struct{}, 1)}
		t.gates[owner] = gate
	}
	gate.refs++
	return gate
}

// release 把引用计数减一，减到零就把这条记录抹掉。
func (t *transactions) release(owner agent.Agent) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	gate, present := t.gates[owner]
	if !present {
		return
	}
	gate.refs--
	if gate.refs <= 0 {
		delete(t.gates, owner)
	}
}

// run 等到这个 agent 上前一件事做完，然后独占地跑这一件。
//
// 源: packages/schedule/schedule/src/transaction.ts:7-23
//
// ctx 在**等**的那一段里就生效：还没轮到自己就被取消的调用交回
// [context.Cause]，一个字节都不会写出去。
func (t *transactions) run(
	ctx context.Context,
	owner agent.Agent,
	operation func(context.Context) error,
) error {
	gate := t.acquire(owner)
	defer t.release(owner)
	select {
	case gate.slot <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-gate.slot }()
	return operation(ctx)
}
