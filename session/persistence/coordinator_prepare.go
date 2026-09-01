// 本文件的作用：编排器的读路径——准备一个待发布的会话、加载一份逻辑视图、
// 以及不落地任何修复的查看，加上它们底下那次冷读和那次提交。
//
// 源: packages/session/session-persistence/src/coordinator.ts:720-819、890-990

package persistence

import (
	"context"
	"errors"
	"fmt"

	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// Prepare 准备并独占一个续跑要用的、还没发布的会话。
//
// 源: packages/session/session-persistence/src/coordinator.ts:720-747
//
// 那圈循环收敛的条件是「读到这一份到核对令牌之间，存档一直没变」。外面一直有
// 写入方在写的时候它会一直重来——那是对的，一个正在被写的存档本来就准备不出
// 一个能续跑的会话。
func (c *Coordinator) Prepare(ctx context.Context, id session.SessionID) (*coresession.Preparation, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := c.waitForRetirement(ctx, id); err != nil {
			return nil, err
		}
		if _, live := c.sessions.Get(id); live {
			return nil, fmt.Errorf("session/persistence: 会话 %q 还活着，准备不了它", string(id))
		}

		reservation, err := c.preparations.reserve(
			ctx, id, c.preparationLoaderFor(id), c.committerFor(ctx, id))
		if err != nil {
			return nil, err
		}
		if reservation == nil {
			continue
		}
		// 拿到独占之后再看一眼：等待期间可能有人把同一个身份发布出去了。
		// 这时候要把预留**当成不可复用的**放回去——那份视图和一个活会话
		// 已经不是同一段历史了。
		if _, live := c.sessions.Get(id); live {
			c.preparations.release(reservation, false)
			return nil, fmt.Errorf("session/persistence: 会话 %q 还活着，准备不了它", string(id))
		}

		return coresession.NewPreparation(reservation.source.live, coresession.PreparationOptions{
			Release: func() {
				// 可复用的判据有两条，缺一不可：这个身份还没被哪个活会话认领，
				// 而且那个待发布的会话从准备好到现在一条事件都没多。
				c.mutex.Lock()
				owned := reservation.state.owner != nil
				c.mutex.Unlock()
				reusable := !owned &&
					len(reservation.source.live.Events()) == reservation.source.sessionLength
				c.preparations.release(reservation, reusable)
			},
		}), nil
	}
}

// Load 提交一次修复，并交回它那份逻辑视图，但**不发布**那个会话。
//
// 源: packages/session/session-persistence/src/coordinator.ts:756-775
func (c *Coordinator) Load(ctx context.Context, id session.SessionID) (Inspection, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Inspection{}, err
		}
		if err := c.waitForRetirement(ctx, id); err != nil {
			return Inspection{}, err
		}
		if live, ok := c.sessions.Get(id); ok {
			return c.loadLiveSnapshot(live)
		}

		reservation, err := c.preparations.reserve(
			ctx, id, c.preparationLoaderFor(id), c.committerFor(ctx, id))
		if err != nil {
			return Inspection{}, err
		}
		if reservation == nil {
			continue
		}
		// 这条路只要那份视图，不发布任何东西，所以预留当场消掉——
		// 用 discard 而不是 release：那个待发布的会话没人要了。
		c.preparations.discard(reservation)
		if live, ok := c.sessions.Get(id); ok {
			return c.loadLiveSnapshot(live)
		}
		return reservation.source.inspection, nil
	}
}

// Inspect 看一份逻辑视图，既不发布、也不落地任何修复。
//
// 源: packages/session/session-persistence/src/coordinator.ts:787-819
//
// 和 [Coordinator.Load] 的差别在于它**不提交**：一份过期的就绪成果会被扔掉重读,
// 而一份正在提交或者已经被续跑独占的成果仍然归人家，这条路只是借用它那份
// 不可变视图。
func (c *Coordinator) Inspect(ctx context.Context, id session.SessionID) (Inspection, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Inspection{}, err
		}
		if err := c.waitForRetirement(ctx, id); err != nil {
			return Inspection{}, err
		}
		if live, ok := c.sessions.Get(id); ok {
			return c.inspectLive(live), nil
		}

		inspection, done, err := c.inspectOnce(ctx, id)
		if err != nil {
			return Inspection{}, err
		}
		if done {
			return inspection, nil
		}
	}
}

// List 从元数据出发轻量列举已落地的会话，不解整份日志。
//
// 源: packages/session/session-persistence/src/index.ts:265-270
//
// 这一条纯转给后端，编排器不插手：列举读的是**存档**，不跨「活会话」和「存档」
// 两边对账，所以它没有 [Coordinator.Load] 那些次序和独占的讲究。上游同理——
// list 是 SessionPersistence 上的抽象方法，由后端那一侧实现，编排器不碰。
//
// 摆在编排器上的理由只有一个：[github.com/snight1983/ds-harness-go/core/agentloop.SessionPersistence]
// 要 Prepare 和 List 两个方法，而 Prepare 只有编排器有。不给这一条，一个
// 装配方就得自己再拼一个既转 Prepare 又转 List 的壳，而那正是这个接缝原本
// 装不起来的原因。
//
// 一个建了但从没追加过的会话可能不在列举里（后端可以懒落地），这一条和
// [Store.Create] 上写的是同一个口径。
func (c *Coordinator) List(ctx context.Context) ([]session.SessionHeader, error) {
	return c.backend.List(ctx)
}

// inspectOnce 是 [Coordinator.Inspect] 那圈循环里的一轮。
//
// 源: packages/session/session-persistence/src/coordinator.ts:791-818
//
// 第二个返回值为假表示这一轮没有结论，回去重来。
//
// 新增: DSH 那一轮整个包在一个 try/catch 里，catch 的处理是「先看看这个身份
// 是不是刚刚活过来了，活了就改读那个活会话，否则把错抛出去」。Go 里没有 catch,
// 所以拆成一个方法，每一处失败都走同一段收尾。
func (c *Coordinator) inspectOnce(ctx context.Context, id session.SessionID) (Inspection, bool, error) {
	// fallback 是那段收尾：一次失败在「这个身份刚被发布出来」时不算失败。
	fallback := func(err error) (Inspection, bool, error) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Inspection{}, false, err
		}
		if live, ok := c.sessions.Get(id); ok {
			return c.inspectLive(live), true, nil
		}
		return Inspection{}, false, err
	}

	source, err := c.preparations.inspect(ctx, id, c.preparationLoaderFor(id))
	if err != nil {
		return fallback(err)
	}
	if live, ok := c.sessions.Get(id); ok {
		return c.inspectLive(live), true, nil
	}

	var current bool
	err = c.serialize(ctx, id, func() error {
		var inner error
		current, inner = c.isPreparedSourceCurrent(ctx, source)
		return inner
	})
	if err != nil {
		return fallback(err)
	}
	if live, ok := c.sessions.Get(id); ok {
		return c.inspectLive(live), true, nil
	}
	if current {
		return source.inspection, true, nil
	}
	// 令牌对不上：这一份该扔。扔不掉是因为有人正独占着它——那就借用它那份
	// 视图，那仍然是一段读得出来的、自洽的历史，而且强行重读也读不出别的
	// 东西来（独占者还没提交，存档就是这一份）。
	if c.preparations.discardReady(id, source) == discardRetained {
		return source.inspection, true, nil
	}
	return Inspection{}, false, nil
}

// preparationLoaderFor 造这个身份的冷读。
//
// 源: packages/session/session-persistence/src/coordinator.ts:728、763、795
//
// 它跑在 [Coordinator.background] 上而不是调用方那条 ctx 上，理由和
// [preparationLoader] 上写的一样：这次读是同一个身份上所有等待方共享的，
// 某一个等待方走了不该把它掐掉。等待方自己的取消由 [awaitShared] 就地兑现。
// DSH 那三处调用同样**没给** signal，是逐字一致的。
func (c *Coordinator) preparationLoaderFor(id session.SessionID) preparationLoader {
	return func() (*preparedSource, error) {
		var source *preparedSource
		err := c.serialize(c.background, id, func() error {
			var inner error
			source, inner = c.prepareCore(c.background, id)
			return inner
		})
		if err != nil {
			return nil, err
		}
		return source, nil
	}
}

// committerFor 造这个身份的提交。
//
// 源: packages/session/session-persistence/src/coordinator.ts:729
//
// 和冷读相反，这一次**收调用方的 ctx**：提交只服务当前这一个预留者，
// 他不要了就该停。DSH 那一处也确实把 signal 传了进去。
func (c *Coordinator) committerFor(
	ctx context.Context,
	id session.SessionID,
) func(*preparedSource) (*commitResult, error) {
	return func(source *preparedSource) (*commitResult, error) {
		var committed *commitResult
		err := c.serialize(ctx, id, func() error {
			var inner error
			committed, inner = c.commitPrepared(ctx, source)
			return inner
		})
		if err != nil {
			return nil, err
		}
		return committed, nil
	}
}

// prepareCore 读一份冷存档、在内存里补平它、验一遍，造出那个待发布的会话。
//
// 源: packages/session/session-persistence/src/coordinator.ts:892-931
//
// 读不回来那一步的错**原样往上抛**，不裹成损坏：那是「这个身份不在」或者一次
// I/O 失败，和「读回来的东西自己不成立」是两件事。
func (c *Coordinator) prepareCore(ctx context.Context, id session.SessionID) (*preparedSource, error) {
	stored, err := c.backend.LoadStored(ctx, id)
	if err != nil {
		return nil, err
	}
	source, err := c.buildPreparedSource(id, stored)
	if err != nil {
		return nil, wrapCorruption(id, err)
	}
	return source, nil
}

// buildPreparedSource 是 [Coordinator.prepareCore] 里那段「验完再造」，
// 它的每一条失败都会被裹成损坏。
//
// 源: packages/session/session-persistence/src/coordinator.ts:895-923
func (c *Coordinator) buildPreparedSource(id session.SessionID, stored StoredPrefix) (*preparedSource, error) {
	if err := CheckStored(c.backend, id, stored.Meta, stored.Events, c.vocabulary); err != nil {
		return nil, err
	}
	// 中断掉的那些事件本身完整保留，只把缺掉的那几条收尾补出来。
	balanced, closers, err := BalanceStored(stored.Events)
	if err != nil {
		return nil, err
	}
	live, err := c.sessions.PrepareRestored(id, coresession.RestoreOptions{
		Seed:   balanced,
		Header: stored.Meta,
	})
	if err != nil {
		return nil, err
	}
	return &preparedSource{
		inspection: Inspection{Meta: live.Header(), Events: balanced},
		live:       live,
		revision:   stored.Revision,
		// 记的是**那个会话**此刻的日志长度，不是 balanced 的长度：一份 seed
		// 后面还会被补上一条 session/end-seed 标记，两者差一。这个数之后用来
		// 判断「这个待发布的会话有没有被写过」，所以必须是会话自己的口径。
		sessionLength: len(live.Events()),
		tornMarker:    stored.TornMarker,
		closers:       closers,
	}, nil
}

// commitPrepared 把一份准备成果的修复提交掉，并给它立一个还没有主人的耐久游标。
//
// 源: packages/session/session-persistence/src/coordinator.ts:934-963
//
// 返回 nil 而不报错表示「这一份已经不作数了，回去重读」，有两种由头：变更令牌
// 对不上（存档在这中间被人改了），或者刚刚真的落了一次修复（那次修复自己就把
// 令牌推新了，再拿旧视图去配新令牌是错的）。
func (c *Coordinator) commitPrepared(ctx context.Context, source *preparedSource) (*commitResult, error) {
	id := source.inspection.Meta.ID
	cursor := len(source.inspection.Events)

	c.mutex.Lock()
	existing := c.states[id]
	owned := existing != nil && existing.owner != nil
	c.mutex.Unlock()
	if owned {
		return nil, fmt.Errorf("session/persistence: 会话 %q 已经有一个活着的持久化主人了", string(id))
	}

	current, err := c.isPreparedSourceCurrent(ctx, source)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, nil
	}
	if source.tornMarker != nil || len(source.closers) > 0 {
		if err := c.backend.CommitRepair(ctx, source.inspection.Meta, source.tornMarker, source.closers); err != nil {
			return nil, err
		}
		return nil, nil
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	state := c.states[id]
	if state == nil {
		state = &sessionState{}
		c.states[id] = state
	}
	state.meta = source.inspection.Meta
	state.cursor = cursor
	state.materialized = true
	return &commitResult{source: source, state: state}, nil
}

// isPreparedSourceCurrent 问这一份缓存的成果指的还是不是存档当前那个版本。
//
// 源: packages/session/session-persistence/src/coordinator.ts:966-971
//
// 新增: DSH 里存档不存在时 readStoredRevision 给的是 undefined，而
// source.revision 必是一个真令牌，两者天然不等。Go 这边 [Revision] 是字符串,
// 空串就是「不存在」，所以「读不到」那一条要明着当成 false 处理——否则一份
// revision 恰好是空串的成果会把「存档没了」当成「还是它」。
func (c *Coordinator) isPreparedSourceCurrent(ctx context.Context, source *preparedSource) (bool, error) {
	revision, err := c.backend.ReadStoredRevision(ctx, source.inspection.Meta.ID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return false, nil
		}
		return false, err
	}
	return revision == source.revision, nil
}

// loadLiveSnapshot 给一个已经活着的会话交出一份耐久的、不可变的视图。
//
// 源: packages/session/session-persistence/src/coordinator.ts:974-985
//
// 先刷盘再取快照：这条路承诺交回来的是**耐久的**那一份，缓冲里还压着事件时
// 磁盘上和这份视图对不上。
func (c *Coordinator) loadLiveSnapshot(live *coresession.Session) (Inspection, error) {
	events := live.Events()
	if err := c.flush(live); err != nil {
		return Inspection{}, err
	}
	state := c.stateOf(live.ID())
	if state == nil {
		// 走不到：刷盘成功必然已经把这个活会话的耐久状态立起来了。留着而不是
		// 断言掉，是因为它一旦真发生，下面那句会读到一份零值的头，然后**静默**
		// 交回一份没有身份的视图。
		return Inspection{}, fmt.Errorf(
			"session/persistence: 会话 %q 在加载途中丢了持久化状态", string(live.ID()))
	}
	if len(events) == 0 {
		return Inspection{}, fmt.Errorf("%w: 会话 %q", ErrSessionNotFound, string(live.ID()))
	}
	closers, err := session.InterruptedTurnClosers(events)
	if err != nil {
		return Inspection{}, err
	}
	if len(closers) > 0 {
		return Inspection{}, fmt.Errorf(
			"session/persistence: 会话 %q 的回合还开着，这时候加载不了它；"+
				"要么直接用那个活会话，要么等这个回合收尾", string(live.ID()))
	}
	c.mutex.Lock()
	meta := state.meta
	c.mutex.Unlock()
	return Inspection{Meta: meta, Events: events}, nil
}

// inspectLive 从一个已经活着的会话上借一份不可变视图。
//
// 源: packages/session/session-persistence/src/coordinator.ts:988-990
//
// 它**不刷盘、也不拦开着的回合**：查看要的就是「此刻长什么样」，
// 而一个开着的回合正是此刻的样子。
func (c *Coordinator) inspectLive(live *coresession.Session) Inspection {
	return Inspection{Meta: live.Header(), Events: live.Events()}
}
