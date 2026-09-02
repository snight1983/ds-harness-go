// 本文件的作用：编排器的写路径——一个活会话怎么入册、一份准备成果怎么接到它
// 身上、缓冲着的写怎么落下去、一个会话退场时怎么收拾，以及「这个身份在存档里
// 已经有了」的那几种情形各自怎么处置。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1140-1362

package persistence

import (
	"fmt"

	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// initFor 拿到这个活会话的那份运行期状态，没有就现建一个并把入册发出去。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1164-1183
//
// 表项在**入册发出去之前**就先插进 c.live：两条并发的调用只有一条能建出那份
// 状态来，另一条拿到的是同一份。DSH 是单线程的，那边不存在这个交错。
func (c *Coordinator) initFor(live *coresession.Session) *liveState {
	c.mutex.Lock()
	if existing, ok := c.live[live]; ok {
		c.mutex.Unlock()
		return existing
	}
	state := &liveState{ready: make(chan struct{})}
	state.writes = c.newWriteBehind(live, state)
	c.live[live] = state
	c.mutex.Unlock()

	reservation, err := c.preparations.reservationFor(live)
	if err != nil {
		// 这个身份在准备池里，但手上这个会话不是那份预留等着发布的那一个——
		// 两个不同的会话顶着同一个身份要发布，是真出事了。
		state.finish(err)
		return state
	}
	if reservation != nil {
		c.attachPrepared(live, state, reservation)
		return state
	}

	// 那个会话拥有这份稳定的快照，本层只负责把它写出去。
	seed := live.Events()
	go func() {
		state.finish(c.serialize(c.background, live.ID(), func() error {
			return c.onCreated(live, seed)
		}))
	}()
	return state
}

// attachPrepared 把**恰好这一个**准备好的会话接上，并只写它还没发布的那截后缀。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1186-1208
//
// 新增: DSH 那四条不匹配是直接 throw 的，而 initFor 是同步调用、它的调用方是
// 会话创建那条观察者。Go 这边把它落进 [liveState.err]，第一次写或者第一次刷盘
// 会撞上——理由和 [Coordinator.onSessionCreated] 上写的一样：不让一次磁盘对账
// 的结论卡住会话创建那条同步路径。
func (c *Coordinator) attachPrepared(
	live *coresession.Session,
	state *liveState,
	reservation *preparationReservation,
) {
	source, persisted := reservation.source, reservation.state

	c.mutex.Lock()
	mismatch := source.live != live ||
		persisted.owner != nil ||
		!persisted.started ||
		persisted.nextSeq != source.nextSeq ||
		live.FirstLiveSeq() != persisted.nextSeq
	nextSeq := persisted.nextSeq
	c.mutex.Unlock()
	if mismatch {
		state.finish(fmt.Errorf(
			"session/persistence: 会话 %q 的准备成果和它的持久化状态已经对不上了", string(live.ID())))
		return
	}

	_, unpublished, err := splitAt(live.ID(), live.Events(), nextSeq)
	if err != nil {
		state.finish(err)
		return
	}
	var suffix []session.Event
	for _, event := range unpublished {
		suffix = append(suffix, cloneEvent(event))
	}

	if err := c.preparations.attach(reservation); err != nil {
		state.finish(err)
		return
	}
	c.mutex.Lock()
	persisted.owner = live
	c.mutex.Unlock()

	if len(suffix) == 0 {
		state.finish(nil)
		return
	}
	go func() {
		state.finish(c.serialize(c.background, live.ID(), func() error {
			return c.appendCore(c.background, live.ID(), suffix)
		}))
	}()
}

// onCreated 在一个会话进了存储时，把这个后端的内存状态和它对上。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1237-1294
//
// 按「这个后端认不认得这个身份」和「磁盘上有没有一份存档」分成四种：
//
//  1. 已经认得 —— 什么都不做（或者认领一份没主的状态，或者收回一个真被抛弃的
//     身份），都不成立就当成撞号拒掉。
//  2. 不认得，磁盘上**有**一份、同一个工作目录、而且是活会话事件的一个按 seq
//     对齐的**前缀** —— 收编它，并把活会话超出前缀的那截写下去。
//  3. 不认得，磁盘上有一份但工作目录不同、或者不是前缀 —— 撞号，拒掉。
//  4. 不认得、磁盘上也没有 —— 真的是一个新会话：登记元数据（懒的），
//     并把它的 seed 写一次。
func (c *Coordinator) onCreated(live *coresession.Session, seed []session.Event) error {
	id := live.ID()

	handled, err := c.reconcileTracked(live, seed)
	if err != nil || handled {
		return err
	}

	// 情形 2／3：先把这个身份在存储里解一次，让收编那一步在做任何修复或者
	// 立状态之前先拒掉工作目录不符。
	stored, err := c.backend.LoadStored(c.background, id)
	if err == nil {
		return c.adoptLivePrefix(live, seed, stored)
	}
	if !isNotFound(err) {
		return err
	}

	// 情形 4。
	meta := live.Header()
	if err := c.createCore(c.background, meta); err != nil {
		return err
	}
	// 把这份状态和这个活会话绑起来，之后另一个会话再拿同一个身份进来就会在
	// 情形 1 里被认出是撞号，而不是静默当成什么都没发生。
	if created := c.stateOf(id); created != nil {
		c.mutex.Lock()
		created.owner = live
		c.mutex.Unlock()
	}
	if len(seed) > 0 {
		return c.appendCore(c.background, id, cloneEvents(seed))
	}
	return nil
}

// reconcileTracked 是 [Coordinator.onCreated] 的情形 1。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1239-1270
//
// 第一个返回值为真表示这一趟到此为止；为假表示册子上那份状态已经被清掉了，
// 接着按情形 2／3／4 走。
func (c *Coordinator) reconcileTracked(live *coresession.Session, seed []session.Event) (bool, error) {
	id := live.ID()
	tracked := c.stateOf(id)
	if tracked == nil {
		return false, nil
	}

	c.mutex.Lock()
	owner, trackedCwd, materialized := tracked.owner, tracked.meta.Cwd, tracked.materialized
	nextSeq, started := tracked.nextSeq, tracked.started
	c.mutex.Unlock()

	if owner == live {
		// 走不到：initFor 按会话对象去重，同一个对象重入不了。
		return true, nil
	}
	if owner == nil {
		// 一份没主的状态，来自公开的 Create／Load。**第一个**活会话可以认领它,
		// 但工作目录和 seed 两样都得对上。工作目录不同是撞号不是认领——认下来
		// 就等于拿存档头里那个工作目录去写这个活会话的事件。seed 那道则保证
		// 活会话的事件真的复现得出已经落盘的那段前缀；否则一个重用了同一个
		// 身份的新会话，它开头那几条事件会被当成「已经写过了」滤掉。
		if trackedCwd != live.Header().Cwd {
			return true, fmt.Errorf(
				"session/persistence: 会话 %q 已经存在于另一个工作目录下（存档：%q，活的：%q），撞号了",
				string(id), trackedCwd, live.Header().Cwd)
		}
		matches, err := c.seedMatchesPersisted(id, seed, nextSeq, started)
		if err != nil {
			return true, err
		}
		if !matches {
			return true, fmt.Errorf(
				"session/persistence: 会话 %q 已经落盘到 seq %d，和这个活会话对不上，撞号了",
				string(id), nextSeq)
		}
		c.mutex.Lock()
		tracked.owner = live
		c.mutex.Unlock()
		// 把 seed 里超出已落盘前缀的那一截写下去。构造 seed 不会发
		// session/event，所以那条攒批的缓冲永远看不到它们。
		fresh := seed
		if started {
			_, fresh, err = splitAt(id, seed, nextSeq)
			if err != nil {
				return true, err
			}
		}
		if len(fresh) > 0 {
			return true, c.appendCore(c.background, id, cloneEvents(fresh))
		}
		return true, nil
	}

	// 有主，而且不是手上这一个。只有在那个主人**什么都没留下**的时候才让位：
	// 没落过盘、缓冲里也没压着东西——那说明它是一个真被抛弃的身份。
	c.mutex.Lock()
	ownerState := c.live[owner]
	c.mutex.Unlock()
	if !materialized && (ownerState == nil || !ownerState.writes.HasWork()) {
		c.mutex.Lock()
		if c.states[id] == tracked {
			delete(c.states, id)
		}
		c.mutex.Unlock()
		return false, nil
	}
	return true, fmt.Errorf(
		"session/persistence: 会话 %q 在这个后端里已经绑在另一个活会话上了，撞号了", string(id))
}

// adoptLivePrefix 把一份存档前缀收编成一个活会话的历史（热替换／重载）。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1302-1324
//
// **不走冷准备那条路**：那条路会把开着的回合当成中断掉的去补收尾，而这里那个
// 活会话仍然是权威，它待会儿可能真的会把那条 step／turn 的收尾追加上来。
// 所以这里只做「截掉坏尾巴」那一半修复，不补任何收尾事件。
//
// 新增: 这里不能直接用 [CheckStored]。DSH 把工作目录那道检查**夹在**身份和
// 版本之间，而 CheckStored 是身份→版本→词汇一条道走完的。次序在这里是有意义的：
// 一个工作目录不符的撞号该被当成撞号报出来，而不是先被一条「格式版本不认得」
// 盖住——后者会让人以为换个版本就能读。
func (c *Coordinator) adoptLivePrefix(
	live *coresession.Session,
	seed []session.Event,
	stored StoredPrefix,
) error {
	id := live.ID()
	if err := CheckStoredIdentity(id, stored.Meta); err != nil {
		return err
	}
	if stored.Meta.Cwd != live.Header().Cwd {
		return fmt.Errorf(
			"session/persistence: 会话 %q 已经存在于另一个工作目录下（存档：%q，活的：%q），撞号了",
			string(id), stored.Meta.Cwd, live.Header().Cwd)
	}
	if err := CheckStoredVersion(stored.Meta); err != nil {
		return locateRefusal(c.backend, stored.Meta, err)
	}
	if err := CheckStoredVocabulary(stored.Meta, stored.Events, c.vocabulary); err != nil {
		return locateRefusal(c.backend, stored.Meta, err)
	}

	covers, err := SeedCoversPrefix(seed, stored.Events)
	if err != nil {
		return err
	}
	if !covers {
		return fmt.Errorf(
			"session/persistence: 会话 %q 在磁盘上那份日志和这个活会话对不上，撞号了", string(id))
	}

	// 只截不补：那个开着的回合在这里**不**收尾。
	if stored.TornMarker != nil {
		if err := c.backend.CommitRepair(c.background, stored.Meta, stored.TornMarker, nil); err != nil {
			return err
		}
	}
	storedNext := nextSeqOf(stored.Events, stored.BaseSeq)
	c.putState(id, &sessionState{
		meta:         stored.Meta,
		baseSeq:      stored.BaseSeq,
		nextSeq:      storedNext,
		started:      true,
		materialized: true,
		owner:        live,
	})
	_, fresh, err := splitAt(id, seed, storedNext)
	if err != nil {
		return err
	}
	if len(fresh) > 0 {
		return c.appendCore(c.background, id, cloneEvents(fresh))
	}
	return nil
}

// seedMatchesPersisted 问一个活会话的 seed 复不复现得出已经落盘的那一段
//（也就是 seq 小于 nextSeq 的那些事件）。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1215-1222
//
// started 为假（还什么都没落盘）时无条件算对上。
//
// 新增: 上游那道判据是 `cursor === 0`，靠的是「日志从 0 起」。见
// [sessionState.started]。
func (c *Coordinator) seedMatchesPersisted(
	id session.SessionID,
	seed []session.Event,
	nextSeq int,
	started bool,
) (bool, error) {
	if !started {
		return true, nil
	}
	stored, err := c.backend.LoadStored(c.background, id)
	if err != nil {
		if isNotFound(err) {
			// 走不到：起点定下来了就意味着这个会话已经落过盘，那它就存在。
			return false, nil
		}
		return false, err
	}
	if err := CheckStoredIdentity(id, stored.Meta); err != nil {
		return false, err
	}
	prefix, _, err := splitAt(id, stored.Events, nextSeq)
	if err != nil {
		return false, err
	}
	return SeedCoversPrefix(seed, prefix)
}

// retire 在一个会话退场时把它刷干净、并从册子上划掉。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1140-1151
//
// 新增: 那条退场登记在**发出去之前**就先记进 c.retirements。DSH 是先拿到
// promise 再 set，中间没有别的东西插得进来；Go 里那趟活儿跑在自己的 goroutine
// 上，先发后记的话会留出一个窗口，[Coordinator.drain] 和
// [Coordinator.waitForRetirement] 在那个窗口里都看不见它。
func (c *Coordinator) retire(live *coresession.Session) {
	c.mutex.Lock()
	if _, ok := c.live[live]; !ok {
		c.mutex.Unlock()
		return
	}
	pending := &retirement{done: make(chan struct{})}
	c.retirements[live.ID()] = pending
	c.mutex.Unlock()

	go func() {
		err := c.retireCore(live)
		c.mutex.Lock()
		if c.retirements[live.ID()] == pending {
			delete(c.retirements, live.ID())
		}
		close(pending.done)
		c.idle.Broadcast()
		c.mutex.Unlock()
		if err != nil {
			c.logger.Warn("会话退场失败",
				"后端", c.backend.Name(), "会话", string(live.ID()), "错误", err)
		}
	}()
}

// retireCore 把**恰好这一个**退场了的会话所拥有的状态排干、放掉。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1154-1161
//
// 刷不下去就到此为止，不把状态划掉：划掉等于把那些还没落盘的事件连同它们的
// 游标一起丢了，之后谁也说不清磁盘上那份存档停在哪儿。
//
// 新增: 划掉之前先 [WriteBehind.Close] 封一次。刷完到划掉之间是有缝的——那一趟
// 刷跑在 retire 自己的 goroutine 上，缝里落进来的 Enqueue 会排进一个马上就要
// 被划掉的控制器里，然后随着表项一起消失。封不上就说明缝里真进了东西，
// 那时候和刷不下去一样处理：不划，留着。
func (c *Coordinator) retireCore(live *coresession.Session) error {
	if err := c.flush(live); err != nil {
		return err
	}
	id := live.ID()
	return c.serialize(c.background, id, func() error {
		c.mutex.Lock()
		state, tracked := c.live[live]
		c.mutex.Unlock()
		if tracked {
			if err := state.writes.Close(); err != nil {
				return fmt.Errorf("会话 %s 退场：%w", id, err)
			}
		}

		c.mutex.Lock()
		defer c.mutex.Unlock()
		delete(c.live, live)
		if state, ok := c.states[id]; ok && state.owner == live {
			delete(c.states, id)
		}
		return nil
	})
}

// flush 把这个会话缓冲着的写立刻落下去。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1326-1338
//
// 它**不收 ctx**，和 [WriteBehindOptions.Write] 一样：一次耐久写做到一半被掐掉,
// 留下的是一份说不清停在哪儿的存档。要停就在它开始之前停。
//
// 那两次 CancelAutomaticWait 不是重复：第一次把已经排上的自动等待收掉，
// 第二次收的是「等入册的那段时间里又排上的最后一次」。
func (c *Coordinator) flush(live *coresession.Session) error {
	state := c.initFor(live)
	state.writes.CancelAutomaticWait()
	if err := state.wait(); err != nil {
		state.writes.CancelAutomaticWait()
		return err
	}
	return state.writes.Flush()
}

// newWriteBehind 给一个活会话造那条攒批的写。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1341-1352
func (c *Coordinator) newWriteBehind(live *coresession.Session, state *liveState) *WriteBehind {
	id := live.ID()
	return NewWriteBehind(WriteBehindOptions{
		MaxDelay: c.writeBatchMaxDelay,
		Write: func(batch []session.Event) error {
			// 先等入册：入册那一趟自己就可能写掉一段 seed，写这一批之前
			// 必须先知道游标停在哪儿。
			if err := state.wait(); err != nil {
				return err
			}
			return c.serialize(c.background, id, func() error {
				return c.appendLiveBatch(id, batch)
			})
		},
		ReportBackgroundFailure: func(err error) {
			c.logger.Warn("后台写失败，缓冲的事件留着",
				"后端", c.backend.Name(), "会话", string(id), "错误", err)
		},
	})
}

// appendLiveBatch 写一批缓冲里的事件，先把入册那一趟已经存过的那几条滤掉。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1355-1361
func (c *Coordinator) appendLiveBatch(id session.SessionID, batch []session.Event) error {
	var nextSeq int
	var started bool
	if state := c.stateOf(id); state != nil {
		c.mutex.Lock()
		nextSeq, started = state.nextSeq, state.started
		c.mutex.Unlock()
	}
	fresh := batch
	if started {
		var err error
		if _, fresh, err = splitAt(id, batch, nextSeq); err != nil {
			return err
		}
	}
	return c.appendCore(c.background, id, fresh)
}

// splitAt 把一段按 seq 连续的事件切成两半：seq 小于 at 的在前，大于等于的在后。
//
// at 落在这一段之前（那一截已经被弹掉了）时整段都算「在后」，落在这一段之后时
// 整段都算「在前」——两种都不是错误，见 docs/session-log-limit.md 的原则第 4 条。
// 落在这一段**之内**却对不上号才是日志真坏了。
//
// 新增: 上游这两处是 `events.slice(cursor)` 和 `events.slice(0, cursor)`，因为它的
// cursor 同时是条数和 seq。本仓库的日志会从最老的一头弹出事件，下标得当场从 seq
// 减出来，而且减完要核一遍——原则第 2 条。
func splitAt(id session.SessionID, events []session.Event, at int) (before, from []session.Event, err error) {
	if len(events) == 0 {
		return nil, nil, nil
	}
	index := at - events[0].Seq
	if index <= 0 {
		return nil, events, nil
	}
	if index >= len(events) {
		return events, nil, nil
	}
	if events[index].Seq != at {
		return nil, nil, fmt.Errorf(
			"session/persistence: 会话 %q 的事件不连续：下标 %d 上应该是 seq %d，实际是 %d",
			string(id), index, at, events[index].Seq)
	}
	return events[:index], events[index:], nil
}

// nextSeqOf 给出这一段之后下一条事件的 seq：末条加一，空段就是 baseSeq。
//
// 新增: 上游这个数就是 `events.length`。日志的起点成为变量之后它不再是长度。
func nextSeqOf(events []session.Event, baseSeq int) int {
	if len(events) == 0 {
		return baseSeq
	}
	return events[len(events)-1].Seq + 1
}

// cloneEvents 复制一整批事件，让这一批和调用方手上那份彻底分开。
func cloneEvents(events []session.Event) []session.Event {
	batch := make([]session.Event, len(events))
	for index, event := range events {
		batch[index] = cloneEvent(event)
	}
	return batch
}
