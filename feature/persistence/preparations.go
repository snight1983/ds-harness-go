// 本文件的作用：一批还没发布的会话在编排器手里的那个池子——同一个身份的冷读
// 只做一次并共享出去、一次独占的预留怎么开怎么收、以及那些没人要的「已就绪」
// 条目按最近使用淘汰。
//
// 源: packages/session/session-persistence/src/preparations.ts

package persistence

import (
	"context"
	"fmt"
	"slices"
	"sync"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// preparedSource 是一次冷读的成果：一个还没发布的活会话，加上认领它要用到的
// 那几样账目。
//
// 源: packages/session/session-persistence/src/coordinator.ts:562-586
//
// 新增: DSH 把这个形状写成 PreparedSessionSource<TornMarker> 泛型，并让
// SessionPreparations 泛型化在它上面（`Source extends PreparedSource`）。
// Go 这边两者同包、而且**只有一处实例化**，所以直接用具名类型——泛型在这里
// 只会把每一个方法签名都变长，换不来任何一个第二种用法。
type preparedSource struct {
	// inspection 是这份存档的逻辑视图，读那条路直接把它交出去。
	inspection Inspection
	// live 是从这份视图恢复出来、还没发布的那个会话。
	live *coresession.Session
	// revision 是读到这份前缀时观察到的变更令牌，用来核对它有没有过期。
	revision Revision
	// baseSeq 是这份存档现存最早一条事件的 seq，nextSeq 是它之后下一条要写的 seq。
	//
	// 新增: 上游这两个数都从 `inspection.events.length` 现算，靠的是「日志从 0 起、
	// 一条不删」。见 [sessionState.baseSeq]。
	baseSeq int
	nextSeq int
	// sessionLength 是刚恢复出来时那个会话的日志长度，用来判断它有没有被写过。
	sessionLength int
	// tornMarker 非 nil 表示存档尾巴上还挂着一截要截掉的坏字节。
	tornMarker any
	// closers 是补平中断回合要追加的那几条收尾事件。
	closers []sessionlog.Event
}

// commitResult 是一次提交成功之后交回来的两样东西。
//
// 源: packages/session/session-persistence/src/preparations.ts:78
//
// 提交返回 nil 而不报错表示「这一份已经不作数了，回去重来一遍」——它和报错
// 是两件事：报错要往上抛，重来是正常控制流。
type commitResult struct {
	source *preparedSource
	state  *sessionState
}

// preparationLoader 是一次冷读。
//
// 它**不收 ctx**：这次读是同一个身份上所有等待方共享的，某一个等待方走了不该
// 把它掐掉。等待方自己的取消由 [awaitShared] 就地兑现，见那里的说明。
type preparationLoader func() (*preparedSource, error)

// preparationPhase 是一个池内条目所处的阶段。
//
// 源: packages/session/session-persistence/src/preparations.ts:12
type preparationPhase int

const (
	// phaseLoading 是冷读还在跑。
	phaseLoading preparationPhase = iota
	// phaseReady 是读完了、没人独占，可以被共享也可以被淘汰。
	phaseReady
	// phaseCommitting 是有人正在提交这一份的持久化修复。
	phaseCommitting
	// phaseReserved 是有人独占着这一份，等着把那个会话发布出去。
	phaseReserved
)

// preparationEntry 是池子里一个身份的条目。
//
// 源: packages/session/session-persistence/src/preparations.ts:14-22
type preparationEntry struct {
	id sessionlog.SessionID

	// loaded 在冷读落定时关闭；关掉之后 result 和 err 不再变，读它们不必上锁。
	//
	// 新增: DSH 那个字段是 `result: Promise<Source>`，一个 promise 同时装着
	// 「好了没有」和「结果是什么」。Go 里拆成一个信号通道加两个字段——通道能
	// 和一个 ctx 一起 select，而 promise 那套 await 做不到就地取消。
	loaded chan struct{}
	result *preparedSource
	err    error

	phase preparationPhase
	// source 是池子当前认的那一份；提交成功之后会被换成提交后的那一份。
	source      *preparedSource
	reservation *preparationReservation

	// settled 在这个条目离开 committing / reserved 时关闭。
	//
	// 它是 [preparations.reserve] 里那圈等待的唤醒源：一个后到的预留必须等
	// 前一个独占者收手。**恒非 nil**——造条目时先塞一个已经关掉的通道，
	// 于是等待方不必判空，也就没有一条「等待器丢了」的不可达分支。
	// DSH 那边有那条分支，并且拿 v8 的忽略注释标着。
	settled chan struct{}
}

// preparationReservation 是一份被独占持有的准备成果，加上它提交出来的持久化状态。
//
// 源: packages/session/session-persistence/src/preparations.ts:31-36（SessionPreparationReservation）
type preparationReservation struct {
	entry  *preparationEntry
	source *preparedSource
	state  *sessionState
}

// preparations 是一个编排器自己的那个池子：冷读共享、独占预留、就绪条目按
// 最近使用淘汰。
//
// 源: packages/session/session-persistence/src/preparations.ts:38-352（SessionPreparations）
//
// 新增: DSH 靠 JS Map 的插入顺序当 LRU 队列（`delete` 完再 `set` 就把一个键
// 挪到末尾）。Go 的 map 没有顺序，所以另立一条 order 切片——理由和
// [github.com/snight1983/ds-harness-go/harness/session.Store] 那条一模一样：这个顺序**是语义**，
// 淘汰谁全看它。
type preparations struct {
	// mutex 守住下面每一个字段，以及每个条目上那些可变字段。
	//
	// 新增: DSH 是单线程的，这些转移之间不可能插进来别的东西。Go 里池子会被
	// 好几条 goroutine 同时碰（写路径的观察者、读路径的调用方、冷读那条
	// goroutine 自己），所以每一次转移都要拿着这把锁做完。
	// **冷读和提交都在锁外跑**：它们要花时间，攥着锁跑会把整个池子锁死。
	mutex    sync.Mutex
	capacity int
	entries  map[sessionlog.SessionID]*preparationEntry
	// order 是那些条目按最近碰过的先后排的身份，末尾最新。
	order []sessionlog.SessionID
}

// newPreparations 造一个容量为 capacity 的池子。
func newPreparations(capacity int) *preparations {
	return &preparations{
		capacity: capacity,
		entries:  map[sessionlog.SessionID]*preparationEntry{},
	}
}

// has 问池子此刻认不认识这个还没发布的身份。
//
// 源: packages/session/session-persistence/src/preparations.ts:42-44
func (p *preparations) has(id sessionlog.SessionID) bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	_, ok := p.entries[id]
	return ok
}

// inspect 看一份准备成果，同一个身份上正在跑的那次冷读会被共享。
//
// 源: packages/session/session-persistence/src/preparations.ts:53-65
func (p *preparations) inspect(
	ctx context.Context,
	id sessionlog.SessionID,
	load preparationLoader,
) (*preparedSource, error) {
	entry := p.entryFor(id, load)
	if err := awaitShared(ctx, entry.loaded); err != nil {
		return nil, err
	}
	if entry.err != nil {
		return nil, entry.err
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	// 条目还在、而且没人独占时顺手把它挪到队尾：看过一眼就是用过一次。
	if p.entries[id] == entry && entry.phase == phaseReady {
		p.touchLocked(entry)
	}
	// 条目已经被换掉时它的 source 不再更新，这时候交回这次冷读自己的成果——
	// 调用方要的是「那一刻存档长什么样」，那份读出来的视图仍然作数。
	if entry.source != nil {
		return entry.source, nil
	}
	return entry.result, nil
}

// reserve 独占一份已就绪的成果，独占之前先把它那次持久化修复提交掉。
//
// 源: packages/session/session-persistence/src/preparations.ts:75-123
//
// 三种返回：拿到预留、报错、以及**两个都为 nil**——最后这个表示这一份在等待
// 期间被作废了（存档变了、或者别人先把它认领走了），调用方回到自己那圈重试里
// 重来一遍。它不是错误，是这个池子的正常控制流。
func (p *preparations) reserve(
	ctx context.Context,
	id sessionlog.SessionID,
	load preparationLoader,
	commit func(*preparedSource) (*commitResult, error),
) (*preparationReservation, error) {
	entry := p.entryFor(id, load)
	if err := awaitShared(ctx, entry.loaded); err != nil {
		return nil, err
	}
	if entry.err != nil {
		return nil, entry.err
	}

	// 等到这个条目没人独占为止。每一轮都重新确认条目还是原来那个：中途被换掉
	// 就说明这一份已经不作数了。
	for {
		p.mutex.Lock()
		if p.entries[id] != entry {
			p.mutex.Unlock()
			return nil, nil
		}
		if entry.phase == phaseReady {
			break // 带着锁跳出去，紧接着的那几行必须和这次判断在同一个临界区里
		}
		settled := entry.settled
		p.mutex.Unlock()
		if err := awaitShared(ctx, settled); err != nil {
			return nil, err
		}
	}
	source := entry.source
	entry.phase = phaseCommitting
	entry.settled = make(chan struct{})
	p.mutex.Unlock()

	committed, err := commit(source)
	if err != nil {
		p.mutex.Lock()
		p.removeLocked(entry)
		p.mutex.Unlock()
		return nil, err
	}
	if committed == nil {
		// 提交自己判定这一份该重来。条目整个摘掉，下一轮会重新冷读。
		p.mutex.Lock()
		p.removeLocked(entry)
		p.mutex.Unlock()
		return nil, nil
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry.source = committed.source
	if err := ctx.Err(); err != nil {
		// 修复已经落盘了，扔掉这个条目等于让下一个人再读一遍——所以放回就绪，
		// 只是这一次的调用方不要了。
		p.makeReadyLocked(entry)
		return nil, err
	}
	if p.entries[id] != entry {
		return nil, nil
	}
	reservation := &preparationReservation{
		entry:  entry,
		source: committed.source,
		state:  committed.state,
	}
	entry.phase = phaseReserved
	entry.reservation = reservation
	return reservation, nil
}

// reservationFor 交出**恰好这个会话**的那份预留，别名一律拒掉。
//
// 源: packages/session/session-persistence/src/preparations.ts:130-139
//
// 三种返回：拿到预留、两个都为 nil（这个身份池子里没有，是一个全新的会话）、
// 报错（池子里有这个身份，但它不是一份等着这个会话去发布的预留）。最后一种
// 是真正要拦的事：两个不同的 Session 对象顶着同一个身份要发布出去。
func (p *preparations) reservationFor(live *coresession.Session) (*preparationReservation, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	id := live.ID()
	entry, ok := p.entries[id]
	if !ok {
		return nil, nil
	}
	if entry.phase == phaseReserved &&
		entry.reservation != nil &&
		entry.source != nil &&
		entry.source.live == live {
		return entry.reservation, nil
	}
	return nil, fmt.Errorf(
		"发布不了会话 %q：这个身份已经归一份持久化状态所有", string(id))
}

// attach 在那个会话真的认上之后把这份预留消掉。
//
// 源: packages/session/session-persistence/src/preparations.ts:145-151
func (p *preparations) attach(reservation *preparationReservation) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry := reservation.entry
	if p.entries[entry.id] != entry || entry.reservation != reservation {
		return fmt.Errorf("会话 %q 的准备成果已经不在预留状态了", string(entry.id))
	}
	p.removeLocked(entry)
	return nil
}

// discard 把一份预留消掉——调用方只要那份看过的视图，不打算发布那个会话。
//
// 源: packages/session/session-persistence/src/preparations.ts:157-161
//
// 和 [preparations.attach] 的差别只在过期时怎么办：那边报错（有人拿着一个
// 会话要发布，而池子已经不认它了，这是真出事了），这边什么都不做（视图已经
// 拿到手，池子认不认它都不影响这次读的结果）。
func (p *preparations) discard(reservation *preparationReservation) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry := reservation.entry
	if p.entries[entry.id] != entry || entry.reservation != reservation {
		return
	}
	p.removeLocked(entry)
}

// release 把一份还没发布、而且还能再用的预留放回就绪队列。
//
// 源: packages/session/session-persistence/src/preparations.ts:168-182
//
// reusable 为假就整个摘掉：那说明这一份已经被写过了（那个会话上多了事件），
// 留着它下一个人会拿到一份和存档对不上的视图。
func (p *preparations) release(reservation *preparationReservation, reusable bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry := reservation.entry
	if p.entries[entry.id] != entry ||
		entry.reservation != reservation ||
		entry.phase != phaseReserved {
		return
	}
	if !reusable {
		p.removeLocked(entry)
		return
	}
	entry.reservation = nil
	p.makeReadyLocked(entry)
}

// invalidate 在存档变了之后扔掉这个身份的准备成果。
//
// 源: packages/session/session-persistence/src/preparations.ts:188-191
func (p *preparations) invalidate(id sessionlog.SessionID) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if entry, ok := p.entries[id]; ok {
		p.removeLocked(entry)
	}
}

// discardOutcome 是 [preparations.discardReady] 的三种结果。
//
// 源: packages/session/session-persistence/src/preparations.ts:199
type discardOutcome int

const (
	// discardMissing 表示池子里没有这一份了，或者当前那一份不是调用方看见的那个。
	discardMissing discardOutcome = iota
	// discardRetained 表示这一份正被人独占着，没有动它。
	discardRetained
	// discardDiscarded 表示这一份已经被扔掉了。
	discardDiscarded
)

// discardReady 扔掉**恰好那一份**已经过期的就绪成果，不打扰正独占着它的人。
//
// 源: packages/session/session-persistence/src/preparations.ts:199-205
//
// 「恰好那一份」是关键：调用方是先拿到 expected、再去核对令牌的，核对期间池子
// 里那一份可能已经换成了新的——那时候要扔的东西已经不在了，不能顺手把新的
// 那份扔掉。
func (p *preparations) discardReady(id sessionlog.SessionID, expected *preparedSource) discardOutcome {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry, ok := p.entries[id]
	if !ok || entry.source != expected {
		return discardMissing
	}
	if entry.phase != phaseReady {
		return discardRetained
	}
	p.removeLocked(entry)
	return discardDiscarded
}

// assertWritable 在一个还没发布的会话独占着这个身份时拒掉写。
//
// 源: packages/session/session-persistence/src/preparations.ts:211-216
//
// 独占期间那份游标状态是**这次预留说了算**的：这时候放一批事件写进去，
// 等那个会话真发布出来，它的 seed 和存档就对不上了。
func (p *preparations) assertWritable(id sessionlog.SessionID) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry, ok := p.entries[id]
	if !ok {
		return nil
	}
	if entry.phase == phaseCommitting || entry.phase == phaseReserved {
		return fmt.Errorf(
			"会话 %q 的持久化准备成果正被独占，这期间追加不了事件", string(id))
	}
	return nil
}

// takeReady 把一个已就绪的条目直接摘走，交给一次已经串行化了的认领。
//
// 源: packages/session/session-persistence/src/preparations.ts:223-228
//
// 没有就绪条目时返回 nil：调用方自己现读一遍。
func (p *preparations) takeReady(id sessionlog.SessionID) *preparedSource {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	entry, ok := p.entries[id]
	if !ok || entry.phase != phaseReady || entry.source == nil {
		return nil
	}
	p.removeLocked(entry)
	return entry.source
}

// entryFor 拿到这个身份的条目，没有就现建一个并把冷读发出去。
//
// 源: packages/session/session-persistence/src/preparations.ts:230-264
//
// 新增: DSH 在这里**同步**调 `load()`，注释写明是为了让同一个 tick 里那次
// 串行化的追加排在这次读后面。Go 这边 load 要花时间、而且这一段拿着池子的锁，
// 所以只能另开一条 goroutine——那个「同一 tick 排队」的保证随之消失。
// 丢掉它是安全的：所有依赖新鲜度的调用方（[Coordinator.Prepare]、
// [Coordinator.Load]、[Coordinator.Inspect]）本来就套在一圈重试里，
// 抢输了的那一次会自己再来一遍。
func (p *preparations) entryFor(id sessionlog.SessionID, load preparationLoader) *preparationEntry {
	p.mutex.Lock()
	if existing, ok := p.entries[id]; ok {
		p.mutex.Unlock()
		return existing
	}
	entry := &preparationEntry{
		id:      id,
		loaded:  make(chan struct{}),
		phase:   phaseLoading,
		settled: closedChannel(),
	}
	p.entries[id] = entry
	p.order = append(p.order, id)
	p.mutex.Unlock()

	go func() {
		source, err := load()
		p.mutex.Lock()
		entry.result, entry.err = source, err
		switch {
		case err != nil:
			p.removeLocked(entry)
		case p.entries[id] == entry:
			entry.source = source
			p.makeReadyLocked(entry)
		}
		// 关在最后：关掉之后 result 和 err 就被别的 goroutine 无锁读了，
		// 所以它们必须先在这把锁里写完。
		close(entry.loaded)
		p.mutex.Unlock()
	}()
	return entry
}

// makeReadyLocked 把一个条目转成就绪：唤醒等着独占的人，并把它挪到队尾。
//
// 源: packages/session/session-persistence/src/preparations.ts:266-274
func (p *preparations) makeReadyLocked(entry *preparationEntry) {
	if p.entries[entry.id] != entry {
		return
	}
	entry.phase = phaseReady
	settleLocked(entry)
	p.touchLocked(entry)
}

// removeLocked 把一个条目从池子里摘掉，并唤醒等着它的人。
//
// 源: packages/session/session-persistence/src/preparations.ts:276-283
func (p *preparations) removeLocked(entry *preparationEntry) {
	if p.entries[entry.id] != entry {
		return
	}
	delete(p.entries, entry.id)
	p.order = slices.DeleteFunc(p.order, func(id sessionlog.SessionID) bool { return id == entry.id })
	settleLocked(entry)
}

// touchLocked 把一个条目挪到队尾，并在就绪条目超编时淘汰最久没碰过的那一个。
//
// 源: packages/session/session-persistence/src/preparations.ts:285-298
//
// 只数、也只淘汰**就绪**的那些：loading 的还没读完，committing / reserved 的
// 有人正拿着，扔哪一个都会把一次正在进行的操作打断。容量管的是「留着备用的
// 有多少」，不是「池子里一共有多少」。
func (p *preparations) touchLocked(entry *preparationEntry) {
	p.order = slices.DeleteFunc(p.order, func(id sessionlog.SessionID) bool { return id == entry.id })
	p.order = append(p.order, entry.id)

	ready := 0
	for _, candidate := range p.entries {
		if candidate.phase == phaseReady {
			ready++
		}
	}
	if ready <= p.capacity {
		return
	}
	for _, id := range p.order {
		candidate := p.entries[id]
		if candidate == nil || candidate.phase != phaseReady {
			continue
		}
		p.removeLocked(candidate)
		return
	}
}

// settleLocked 唤醒等着这个条目让出独占的人，重复调是安全的。
func settleLocked(entry *preparationEntry) {
	select {
	case <-entry.settled:
	default:
		close(entry.settled)
	}
}

// closedChannel 造一个已经关掉的信号通道。
func closedChannel() chan struct{} {
	channel := make(chan struct{})
	close(channel)
	return channel
}

// awaitShared 等一件共享的活儿落定，或者等这一次的 ctx 先断掉。
//
// 源: packages/session/session-persistence/src/preparations.ts:354-396（observeQueuedAbort）
//
// 新增: DSH 那个函数叫 observeQueuedAbort，做的是同一件事——给一个排在队里的
// 观察者一份**就地**的取消视图，而**不去掐掉那件共享的活儿**：那件活儿是别人
// 也在等的，一个等待方走了不该连累其余的人。DSH 为此要手工装拆 abort 监听器、
// 用一个 settled 标志防重、还要把非 Error 的取消理由原样透传；Go 里这一切就是
// 一次 select。
//
// 新增: DSH 那个 `started()` 谓词整个消失。它在 DSH 里的用途是「活儿已经越过
// 取消临界点之后就不再理会 abort」，而 Go 里同一件事的写法是**过了那个点就
// 不再 select ctx.Done()**——临界点由调用点的结构表达，不必再传一个谓词进来。
//
// 先单独探一次 done：一件已经干完的活儿不该被报成「取消了」。裸 select 在两边
// 都就绪时是随机挑的，那会让「读完了但 ctx 恰好也断了」这种情况的结果不确定。
func awaitShared(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
