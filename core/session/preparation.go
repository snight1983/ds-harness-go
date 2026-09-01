// 本文件的作用：一个还没公布的会话，加上让它可用的那份提供方状态，合起来算一段
// 有主的生命期。
//
// 源: packages/core/session/src/preparation.ts:1-48

package session

import "sync"

// PreparationOptions 是一段准备期里提供方那一侧的行为。
//
// 源: packages/core/session/src/preparation.ts:8-12（SessionPreparationOptions）
type PreparationOptions struct {
	// Release 在这个会话**没有**被公布时放掉提供方自己攥着的状态，可以为 nil。
	//
	// 提供方自己决定这一步是把会话还回缓存池还是直接丢掉。公布那一步可能已经把
	// 这份状态接手走了，于是这个回调什么都不用做——所以它必须能被调而不出事。
	Release func()
}

// Preparation 是**确切的**一个未公布会话，加上让它保持可用的那份提供方状态。
//
// 源: packages/core/session/src/preparation.ts:14-48
//
// 它存在的理由是所有权：[Store.Prepare] 造出来的会话在 [Store.Enter] 之前不属于
// 任何存储，而造它的那一方（一个会话提供方、一个缓存池）手上可能还攥着别的东西。
// 这个类型把「这个会话」和「那份状态」绑成一段生命期，好让「公布成了」和
// 「半路放弃了」两条路都只需要调同一个 [Preparation.Release]。
//
// 新增: DSH 实现的是 TS 的 `Disposable`（`using` 语句自动调 `[Symbol.dispose]`）。
// Go 没有那个语法，对应物就是 `defer preparation.Release()`——同样的位置、同样的
// 一次性。释放是同步且幂等的，这一条两边一样。
type Preparation struct {
	session *Session
	release func()

	once sync.Once
}

// NewPreparation 把一个未公布的会话裹进一段准备期。
//
// 源: packages/core/session/src/preparation.ts:36-38（静态的 create）
//
// 新增: DSH 的构造函数是私有的、只能走 `SessionPreparation.create`。Go 里
// 不导出的字段已经让「只能从这里造」成立，不需要再多一个静态方法名。
func NewPreparation(session *Session, options PreparationOptions) *Preparation {
	return &Preparation{session: session, release: options.Release}
}

// Session 是这段准备期里那个确切的会话，拿它去做装配和公布。
//
// 源: packages/core/session/src/preparation.ts:22-23
func (p *Preparation) Session() *Session { return p.session }

// Release 放掉提供方那份状态，**恰好一次**。
//
// 源: packages/core/session/src/preparation.ts:41-47
//
// 重复调是安全的（这正是它能直接 defer 的原因）：公布成功那条路上调用方照样会
// 走到 defer，那一次就该什么都不做。
//
// 新增: DSH 用一个 released 布尔字段做一次性，因为 JS 是单线程的。Go 里换成
// [sync.Once]——一段准备期可能被公布路径和 defer 两处碰到，一个裸布尔在这里是
// 数据竞争。
func (p *Preparation) Release() {
	if p.release == nil {
		return
	}
	p.once.Do(p.release)
}
