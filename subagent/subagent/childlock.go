// 本文件的作用：按耐久孩子 id 把投递、放权、处置这几件事串成一条线的那把锁。
//
// 源: packages/subagent/subagent/src/continuation.ts:325-347

package subagent

import (
	"context"
	"sync"

	"ds-harness-go/session"
)

// childLock 把同一个耐久孩子上的那些临界区串起来，不同孩子之间互不相干。
//
// 源: packages/subagent/subagent/src/continuation.ts:326-347
//
// 新增: DSH 拿一张 `Map<SessionId, Promise>` 把后来的操作 `.then` 挂在上一条尾巴
// 后面，于是它天然是 **FIFO**（谁先排上谁先跑），而且尾巴上要专门吞掉 rejection、
// 免得一次失败的临界区把毫不相干的后来者一起拒掉。
//
// Go 这边找过现成的轮子：golang.org/x/sync 里 errgroup 是「一组并发活儿一起等」，
// semaphore 是「计数配额」，singleflight 是「同一个键上的并发**相同**调用合成一次」
// ——singleflight 合并的是重复请求，而这里要的是把**不同**的操作依次放行，语义
// 正好不是一回事。剩下的就是一把按键分的互斥锁，标准库的 sync 加一张 map 就够，
// 引一个包反而更重。
//
// 因此这里**不是** FIFO：等在同一个孩子上的几方靠 select 抢那一次唤醒，次序不定。
// 这一条成立，是因为本包每一个临界区进去之后都会把自己的前提重新验一遍（还在不在
// 收活、这个 id 占没占上、活化还在不在、有没有开始处置），所以「谁先进」不承载
// 任何正确性——DSH 那条链的次序也只是它那个实现顺带有的性质，不是它依赖的东西。
//
// 失败不会污染这把锁：Go 的错误从临界区照实返回，没有那条要吞掉的 rejection 尾巴。
type childLock struct {
	mutex sync.Mutex
	// held 是当下被占着的那些孩子；值那个通道在放开时关闭，等的人以此醒来。
	held map[session.SessionID]chan struct{}
}

// newChildLock 造一把空锁。
func newChildLock() *childLock {
	return &childLock{held: map[session.SessionID]chan struct{}{}}
}

// acquire 占下一个孩子，交回一个幂等的放开函数。
//
// 等待期间调用方取消时报 [CodeCancelled]，并且**没有**占上——所以这一路上
// 不许调那个放开函数，签名上第一个返回值为 nil 已经把这件事说死了。
func (l *childLock) acquire(ctx context.Context, childID session.SessionID) (func(), error) {
	for {
		l.mutex.Lock()
		waiter, busy := l.held[childID]
		if !busy {
			l.held[childID] = make(chan struct{})
			l.mutex.Unlock()
			var once sync.Once
			return func() { once.Do(func() { l.release(childID) }) }, nil
		}
		l.mutex.Unlock()
		select {
		case <-waiter:
			// 上一位放开了，回去重抢——中间可能已经被第三方占走，所以是重抢不是直接进。
		case <-ctx.Done():
			return nil, NewError("子 agent 操作在等这个孩子的锁时被取消了", CodeCancelled, ctx.Err())
		}
	}
}

// release 放开一个孩子，并唤醒等在它上面的那些人。
func (l *childLock) release(childID session.SessionID) {
	l.mutex.Lock()
	waiter, held := l.held[childID]
	delete(l.held, childID)
	l.mutex.Unlock()
	if held {
		close(waiter)
	}
}
