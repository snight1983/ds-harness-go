// 本文件的作用：编排器那把「按身份排队」的串行锁，以及骑在它上面的那几条基本
// 操作——登记一个新身份、把一批事件写下去、认领一个还没入册的身份，和两条不改
// 任何东西的存档读。
//
// 源: packages/session/session-persistence/src/coordinator.ts:632-720、820-890、1010-1044

package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/session"
)

// acquire 占住这个身份上那把串行锁，占不到就等，等不及就带着 ctx 的错回来。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1010-1033
//
// holders 在**排队之前**就加上：表项在不在是 [Coordinator.drain] 判断静止的
// 依据，一个还在排队的等待方也算在途活儿。
func (c *Coordinator) acquire(ctx context.Context, id session.SessionID) (*chainEntry, error) {
	c.mutex.Lock()
	entry, ok := c.chains[id]
	if !ok {
		entry = &chainEntry{lock: make(chan struct{}, 1)}
		c.chains[id] = entry
	}
	entry.holders++
	c.mutex.Unlock()

	select {
	case entry.lock <- struct{}{}:
		return entry, nil
	case <-ctx.Done():
		c.releaseHolder(id, entry)
		return nil, ctx.Err()
	}
}

// releaseChain 让出那把锁，并把自己从排队里划掉。
func (c *Coordinator) releaseChain(id session.SessionID, entry *chainEntry) {
	<-entry.lock
	c.releaseHolder(id, entry)
}

// releaseHolder 把一个排队者划掉，最后一个走的人顺手把表项删掉。
//
// 删表项不是为了省内存，是为了让 [Coordinator.drain] 那圈等待有一个能收敛的
// 判据：chains 空了才叫静止。
func (c *Coordinator) releaseHolder(id session.SessionID, entry *chainEntry) {
	c.mutex.Lock()
	entry.holders--
	if entry.holders == 0 && c.chains[id] == entry {
		delete(c.chains, id)
	}
	c.idle.Broadcast()
	c.mutex.Unlock()
}

// serialize 把一件活儿排到这个身份的队尾去做。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1010-1033
//
// 新增: DSH 是把新活儿接到上一个 promise 后面，于是排队严格是**先来先得**。
// Go 的通道没有这个保证：几个等待方谁先醒是运行时说了算的。放掉这条保证是
// 安全的，因为所有依赖新鲜度的调用方本来就要自己再确认一遍——
// [Coordinator.Prepare]、[Coordinator.Load]、[Coordinator.Inspect] 各自套在一圈
// 重试里，[Coordinator.adopt] 也是，而 [Coordinator.commitPrepared] 还要核对
// 一次变更令牌。抢输了的那一次会自己再来。
//
// 新增: DSH 那个 `tail` 会把上一件活儿的失败吞掉，好让队里下一个人不被前一个
// 人的错连坐。Go 里根本没有这条边——一件活儿的错就地返给它自己的调用方,
// 那把锁在 defer 里无条件让出去。
//
// **不许在拿着 [Coordinator.mutex] 的时候调它**：它会阻塞。
func (c *Coordinator) serialize(ctx context.Context, id session.SessionID, op func() error) error {
	entry, err := c.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer c.releaseChain(id, entry)
	// 排到队头时再看一眼：排队本身可能花掉很久，而调用方可能早就不要了。
	if err := ctx.Err(); err != nil {
		return err
	}
	return op()
}

// waitForRetirement 等这个身份上正在跑的那次退场收手。
//
// 源: packages/session/session-persistence/src/coordinator.ts:993-998
//
// 退场期间那个身份还挂在册子上，这时候去准备或者读会读到一份正要被划掉的状态。
func (c *Coordinator) waitForRetirement(ctx context.Context, id session.SessionID) error {
	c.mutex.Lock()
	pending, ok := c.retirements[id]
	c.mutex.Unlock()
	if !ok {
		return nil
	}
	return awaitShared(ctx, pending.done)
}

// stateOf 读这个身份在册子上那份状态；没有就返回 nil。
func (c *Coordinator) stateOf(id session.SessionID) *sessionState {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.states[id]
}

// putState 把一份状态记进册子。
func (c *Coordinator) putState(id session.SessionID, state *sessionState) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.states[id] = state
}

// Create 登记一份游离的会话元数据，等第一次追加时才真的落盘。
//
// 源: packages/session/session-persistence/src/coordinator.ts:632-643
//
// 新增: DSH 先 `snapshotJsonValue(meta)` 深拷一份、顺带验它排得成 JSON。Go 里
// [github.com/snight1983/ds-harness-go/session.SessionHeader] 是一个全是标量的结构体，按值传就是拷贝,
// 「排不成 JSON」在类型上不成立，所以只剩 createdAt 那条取值检查。
func (c *Coordinator) Create(ctx context.Context, meta session.SessionHeader) error {
	if meta.CreatedAt < 0 {
		return fmt.Errorf("session/persistence: 会话元数据的 CreatedAt 不能是负数（给的是 %d）", meta.CreatedAt)
	}
	return c.serialize(ctx, meta.ID, func() error { return c.createCore(ctx, meta) })
}

// createCore 是 [Coordinator.Create] 排到队头之后做的事。
//
// 源: packages/session/session-persistence/src/coordinator.ts:645-659
//
// 三道拦：册子上已经有、准备池里已经有、以及存档里已经有。第三道是关键——
// 加载和续跑**只按身份**认一个会话，磁盘上已经躺着一份的时候再造一份，
// 之后续跑谁就说不准了。
func (c *Coordinator) createCore(ctx context.Context, meta session.SessionHeader) error {
	if c.stateOf(meta.ID) != nil || c.preparations.has(meta.ID) {
		return fmt.Errorf("session/persistence: 会话 %q 在这个后端里已经存在了", string(meta.ID))
	}
	_, err := c.backend.LoadStored(ctx, meta.ID)
	switch {
	case err == nil:
		return fmt.Errorf(
			"session/persistence: 会话 %q 在磁盘上已经有一份日志了，去加载或者续跑它，别再造一次",
			string(meta.ID))
	case !errors.Is(err, ErrSessionNotFound):
		return err
	}
	// 纯懒：只记下这个意图，第一次追加之前磁盘上什么都不会有。
	c.putState(meta.ID, &sessionState{meta: meta, cursor: 0, materialized: false})
	return nil
}

// Append 把一批事件耐久地写下去。
//
// 源: packages/session/session-persistence/src/coordinator.ts:665-680
//
// 新增: DSH 在排队**之前**先把整批深拷一份，注释写明理由是「先检查再
// structuredClone 会把带存取器的值重读一遍，可能把一个古怪的值净化成一份看着
// 合法的记录」。Go 这边同一件事更简单：[cloneEvent] 把那两处会共享的引用
// （Data 字节和 SourceEventSeqs）复制掉，于是调用方之后怎么改自己那份切片都
// 改不动这一批。
func (c *Coordinator) Append(ctx context.Context, id session.SessionID, events []session.Event) error {
	batch := make([]session.Event, len(events))
	for index, event := range events {
		batch[index] = cloneEvent(event)
	}
	return c.serialize(ctx, id, func() error { return c.appendCore(ctx, id, batch) })
}

// appendCore 是所有写路径的汇合点：公开接口、活会话那条攒批写、以及装载时的
// 前缀认领，最后都落到这里。
//
// 源: packages/session/session-persistence/src/coordinator.ts:682-711
//
// 传进来的那一批**必须已经是复制过的**：这里不再拷。
func (c *Coordinator) appendCore(ctx context.Context, id session.SessionID, events []session.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := c.preparations.assertWritable(id); err != nil {
		return err
	}
	state := c.stateOf(id)
	if state == nil {
		adopted, err := c.adopt(ctx, id)
		if err != nil {
			return err
		}
		state = adopted
	}

	c.mutex.Lock()
	cursor, meta, materialized := state.cursor, state.meta, state.materialized
	c.mutex.Unlock()

	// 连续性契约：每条事件的 seq 必须接得上存档的尾巴。
	for index, event := range events {
		if event.Seq != cursor+index {
			return fmt.Errorf(
				"session/persistence: 会话 %q 的这一批 seq 对不上：第 %d 条应该是 %d，给的是 %d",
				string(id), index, cursor+index, event.Seq)
		}
	}

	if err := c.backend.AppendBatch(ctx, meta, events, materialized); err != nil {
		return err
	}
	// 那次耐久写就是这次事务：一提交就把「已经落地」和游标一起推上去。
	c.mutex.Lock()
	state.materialized = true
	state.cursor = cursor + len(events)
	c.mutex.Unlock()
	c.preparations.invalidate(id)
	return nil
}

// adopt 把一个册子上还没有的身份认领进来——读一遍存档、修一次、记上状态。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1036-1044
//
// 那圈循环是为了对付「读到的这一份在提交之前就变了」：提交自己会核对变更令牌,
// 对不上就交回 nil，这时候整个再读一遍。
//
// 新增: 多了一句 ctx 检查。DSH 那个 `for(;;)` 没有出口——它靠「外部写入方总会
// 停下来」收敛。Go 里一条已经取消的 ctx 会让这圈空转到天荒地老，所以每一轮
// 开头先看一眼。
func (c *Coordinator) adopt(ctx context.Context, id session.SessionID) (*sessionState, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := c.preparations.takeReady(id)
		if source == nil {
			prepared, err := c.prepareCore(ctx, id)
			if err != nil {
				return nil, err
			}
			source = prepared
		}
		committed, err := c.commitPrepared(ctx, source)
		if err != nil {
			return nil, err
		}
		if committed != nil {
			return committed.state, nil
		}
	}
}

// ReadFrom 读出这段存档里 seq 从 fromSeq 起的那些事件，不改动任何东西。
//
// 源: packages/session/session-persistence/src/coordinator.ts:832-838
//
// 它和写走同一条按身份的队：读到一半被一次追加插进来会读出一份撕开的前缀。
func (c *Coordinator) ReadFrom(ctx context.Context, id session.SessionID, fromSeq int) (StoredSuffix, error) {
	if fromSeq < 0 {
		return StoredSuffix{}, fmt.Errorf(
			"%w: readFrom 的 fromSeq 不能是负数（给的是 %d）", ErrMalformedSeq, fromSeq)
	}
	if err := c.waitForRetirement(ctx, id); err != nil {
		return StoredSuffix{}, err
	}
	var suffix StoredSuffix
	err := c.serialize(ctx, id, func() error {
		var inner error
		suffix, inner = c.readFromCore(ctx, id, fromSeq)
		return inner
	})
	if err != nil {
		return StoredSuffix{}, err
	}
	return suffix, nil
}

// readFromCore 是 [Coordinator.ReadFrom] 排到队头之后做的事。
//
// 源: packages/session/session-persistence/src/coordinator.ts:840-869
//
// 后端带得动 [SeekableBackend] 时只读那一截后缀，否则读整个前缀再往前跳。
// 跳过去那一步之所以是一次下标切片，靠的是「seq 从 0 起连续」这条契约。
func (c *Coordinator) readFromCore(ctx context.Context, id session.SessionID, fromSeq int) (StoredSuffix, error) {
	if err := ctx.Err(); err != nil {
		return StoredSuffix{}, err
	}
	if seekable, ok := Seekable(c.backend); ok {
		suffix, err := seekable.LoadStoredFrom(ctx, id, fromSeq)
		if err != nil {
			return StoredSuffix{}, err
		}
		if err := CheckStoredIdentity(id, suffix.Meta); err != nil {
			return StoredSuffix{}, err
		}
		if err := CheckStoredVersion(suffix.Meta); err != nil {
			return StoredSuffix{}, locateRefusal(c.backend, suffix.Meta, err)
		}
		// 词汇只按这一截后缀验：整段前缀这里根本没读回来，也不该为了验一遍
		// 词汇就把它读回来——那正是 [SeekableBackend] 想省掉的那次读。
		if err := CheckStoredVocabulary(suffix.Meta, suffix.Events, c.vocabulary); err != nil {
			return StoredSuffix{}, locateRefusal(c.backend, suffix.Meta, err)
		}
		return suffix, nil
	}

	whole, err := c.readStoredPrefix(ctx, id)
	if err != nil {
		return StoredSuffix{}, err
	}
	events := whole.Events
	// 新增: DSH 那句 `events.slice(fromSeq)` 在下标越界时给的是空数组，
	// 而 Go 的 `events[fromSeq:]` 会 panic。fromSeq 落在存档之外返回空事件列表
	// 是 [Store.ReadFrom] 上写着的契约，不是错误，所以这里显式夹一下。
	if fromSeq >= len(events) {
		events = nil
	} else {
		events = events[fromSeq:]
	}
	return StoredSuffix{Meta: whole.Meta, Events: events}, nil
}

// readStoredPrefix 读一份物理前缀，不做任何逻辑修复、也不进准备池。
//
// 源: packages/session/session-persistence/src/coordinator.ts:872-888
func (c *Coordinator) readStoredPrefix(ctx context.Context, id session.SessionID) (StoredPrefix, error) {
	if err := ctx.Err(); err != nil {
		return StoredPrefix{}, err
	}
	stored, err := c.backend.LoadStored(ctx, id)
	if err != nil {
		return StoredPrefix{}, err
	}
	if err := ctx.Err(); err != nil {
		return StoredPrefix{}, err
	}
	if err := CheckStored(c.backend, id, stored.Meta, stored.Events, c.vocabulary); err != nil {
		return StoredPrefix{}, err
	}
	return stored, nil
}

// isNotFound 问一条失败是不是「这个身份在存储里没有」。
//
// 新增: DSH 那几处的判据是 `stored === undefined`——「没有」不是一条错误，
// 是一个正常返回值。Go 这边 [Backend.LoadStored] 只有一个错误位可用，所以
// 「没有」由 [ErrSessionNotFound] 这条哨兵表达，判它就是一次 errors.Is。
func isNotFound(err error) bool { return errors.Is(err, ErrSessionNotFound) }

// wrapCorruption 把一次「读回来的东西自己不成立」裹成一条损坏。
//
// 源: packages/session/session-persistence/src/coordinator.ts:900-930（那个 try/catch）
//
// 版本不认得那一条**不裹**：它已经是一条自带落点的、说得清的拒绝
// （[FormatUnsupportedError]），再裹一层损坏只会把「换个版本就能读」误报成
// 「这份存档坏了」。
func wrapCorruption(id session.SessionID, err error) error {
	if errors.Is(err, ErrFormatUnsupported) {
		return err
	}
	return &CorruptionError{ID: id, Cause: err}
}
