// 本文件的作用：把「某个凭据提交了一次变更」这件事发给订阅者，并且**兜住**订阅者的失败。
//
// 源: packages/credentials/credentials/src/index.ts:258-313

package credentials

import (
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RefListener 观察一个引用的提交变更。
//
// 源: packages/credentials/credentials/src/types.ts:75
//
// 同步且自持：它在变更**已经提交之后**被就地调用，不许阻塞，也不许回头去调
// 同一个提供方的写方法（那会在通知的调用栈上再套一层通知）。
type RefListener func(ref Ref)

// RecordListener 观察一条记录的提交变更，约束同 [RefListener]。
//
// 源: packages/credentials/credentials/src/types.ts:87
type RecordListener func(key Key)

// Notifier 是那张订阅表加上它的分发规则，也就是 DSH 抽象基类里已经写好的那三个成员。
//
// 源: packages/credentials/credentials/src/index.ts:258-307
//
// 提供方**内嵌**它就同时拿到 [Observer]（给消费方订阅）和两个 Notify 方法
// （给自己在提交之后调）。哪一半该由谁调，见包文档里「内嵌把 protected 变成了公开」。
//
// 零值不可用，请用 [NewNotifier]。
type Notifier struct {
	logger *slog.Logger

	// mutex 保护下面两张表。
	//
	// 新增: DSH 是单线程 JS，订阅表不需要任何并发保护。Go 里订阅、退订、
	// 分发会来自不同的 goroutine，所以这一层是 Go 侧的必需品。
	mutex     sync.Mutex
	nextID    uint64
	refs      []refSubscription
	recordSub []recordSubscription
}

type refSubscription struct {
	id       uint64
	listener RefListener
}

type recordSubscription struct {
	id       uint64
	listener RecordListener
}

// NewNotifier 造一个通知器。
//
// logger 留空用 slog.Default()——**不是**丢弃。这里记的是「一个订阅者炸了但提交是好的」，
// 正是没人会主动去查、却必须留下痕迹的那类事，默认静音等于把它们藏起来。
// 要静音的调用方显式递一个装着 slog.DiscardHandler 的 logger。
func NewNotifier(logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{logger: logger}
}

// SubscribeReference 实现 [Observer]。
//
// 新增: 订阅按登记顺序保存在切片里而不是 map 里。Go 的 map 迭代顺序是随机的，
// 用 map 会让分发顺序每次都不一样——而 DSH 的分发是按登记顺序的，
// 一个依赖「我比另一个监听器先跑」的组合会变成随机通过的测试。
func (n *Notifier) SubscribeReference(listener RefListener) func() {
	if listener == nil {
		return func() {}
	}

	n.mutex.Lock()
	id := n.nextID
	n.nextID++
	n.refs = append(n.refs, refSubscription{id: id, listener: listener})
	n.mutex.Unlock()

	return func() {
		n.mutex.Lock()
		defer n.mutex.Unlock()
		for index, subscription := range n.refs {
			if subscription.id == id {
				n.refs = append(n.refs[:index:index], n.refs[index+1:]...)
				return
			}
		}
	}
}

// SubscribeRecord 实现 [Observer]，其余同 [Notifier.SubscribeReference]。
func (n *Notifier) SubscribeRecord(listener RecordListener) func() {
	if listener == nil {
		return func() {}
	}

	n.mutex.Lock()
	id := n.nextID
	n.nextID++
	n.recordSub = append(n.recordSub, recordSubscription{id: id, listener: listener})
	n.mutex.Unlock()

	return func() {
		n.mutex.Lock()
		defer n.mutex.Unlock()
		for index, subscription := range n.recordSub {
			if subscription.id == id {
				n.recordSub = append(n.recordSub[:index:index], n.recordSub[index+1:]...)
				return
			}
		}
	}
}

// NotifyReferenceUpdated 分发一次引用变更。
//
// 源: packages/credentials/credentials/src/index.ts:265-278
//
// **只有拥有这个通知器的提供方该调它，且只在写或重载真的提交之后调**。
// 提交之前调的话，一个坏掉的观察者能让一次已经落盘的变更看起来失败了。
//
// 分发是**兜底**的：每一个订阅者都会跑到，某一个 panic 掉会被记进日志而不改变
// 这次提交的结果——除了不变量违例，见 [Notifier.fanOut]。
func (n *Notifier) NotifyReferenceUpdated(ref Ref) {
	n.fanOut("credentials/reference-updated", string(ref), func(deliver func(func())) {
		n.mutex.Lock()
		listeners := make([]RefListener, 0, len(n.refs))
		for _, subscription := range n.refs {
			listeners = append(listeners, subscription.listener)
		}
		n.mutex.Unlock()

		for _, listener := range listeners {
			deliver(func() { listener(ref) })
		}
	})
}

// NotifyRecordUpdated 分发一次记录变更，条款和 [Notifier.NotifyReferenceUpdated] 完全一样。
//
// 源: packages/credentials/credentials/src/index.ts:280-287
func (n *Notifier) NotifyRecordUpdated(key Key) {
	n.fanOut("credentials/record-updated", string(key), func(deliver func(func())) {
		n.mutex.Lock()
		listeners := make([]RecordListener, 0, len(n.recordSub))
		for _, subscription := range n.recordSub {
			listeners = append(listeners, subscription.listener)
		}
		n.mutex.Unlock()

		for _, listener := range listeners {
			deliver(func() { listener(key) })
		}
	})
}

// fanOut 是两个通知共用的那段兜底分发。
//
// 源: packages/credentials/credentials/src/index.ts:289-313
//
// 三条规则，逐条对齐：
//
//  1. **每一个订阅者都跑到。** 一个订阅者炸掉不许掐断后面的——变更已经提交了，
//     没跑到的那几个从此和存储不一致，而它们永远不会知道。
//  2. **普通失败只记日志，不改变这次提交的结果。** 一次已经落盘的写不该因为
//     有人在旁边看崩了就报失败。
//  3. **不变量违例例外：等所有订阅者都跑完之后重新抛出**，且只抛第一条。
//     不变量违例意味着程序写错了（见 invariants 包），它必须传到发起这次
//     观察的人手里，否则那条检查等于没有。
//
// 新增: DSH 还有第四条——订阅者返回的 Promise 被拒绝时也记日志。Go 里没有这一条，
// 因为监听器是同步的（见 [RefListener]），一次失败只会是 panic，
// 而 panic 一定发生在 deliver 的调用栈上。
//
// 顺带一提，DSH 在文档里提醒「这个事件上的不变量检查不能写成 async 函数，
// 否则重抛到不了发起方」。Go 侧这个坑不存在，因为这里根本没有异步监听器可写。
//
// 参数 dispatch 收一个 deliver 回调，是为了让两个键空间共用这一段而不必把
// [Ref] 和 [Key] 擦成同一个类型：擦掉之后监听器签名就丢了类型，
// 而这两套键空间语法不相交、绝不能互串，正是最不该丢类型的地方。
func (n *Notifier) fanOut(event, subject string, dispatch func(deliver func(func()))) {
	var invariantFailure *invariants.Error

	dispatch(func(call func()) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if failure, isInvariant := recovered.(*invariants.Error); isInvariant {
				// 只留第一条：后面的违例多半是同一个原因的连锁反应，
				// 而抛出去的只能有一个，抛最早的那个离现场最近。
				if invariantFailure == nil {
					invariantFailure = failure
				}
				return
			}
			n.warnListenerFailure(event, subject, recovered)
		}()
		call()
	})

	if invariantFailure != nil {
		panic(invariantFailure)
	}
}

// warnListenerFailure 是订阅者失败时留下的那条诊断。
//
// 源: packages/credentials/credentials/src/index.ts:309-313
//
// **不记 subject 之外的任何内容**这一点是有意的：subject 是引用名或记录地址，
// 两者都不是秘密；而订阅者手上可能正拿着刚解析出来的值，把 panic 的值原样打进日志
// 已经是这条接缝能容忍的上限，再多就有把密钥写进日志的风险。
func (n *Notifier) warnListenerFailure(event, subject string, recovered any) {
	n.logger.Warn("credentials: 一个订阅者处理事件时失败",
		slog.String("event", event),
		slog.String("subject", subject),
		slog.Any("panic", recovered),
	)
}
