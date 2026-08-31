// 本文件的作用：缓存本身——怎么建、两级读怎么读、什么时候往介质上写一次，
// 以及那三个写触发点各自的理由。
//
// 源: packages/session/session-projection-cache/src/index.ts:36-297

package projectioncache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ds-harness-go/session"
	"ds-harness-go/session/persistence"
	"ds-harness-go/session/projection"
	"ds-harness-go/storage/domain"
)

// LiveSession 是这个缓存从一个活会话身上要看的全部东西。
//
// 新增: DSH 收的是循环那一块（DESIGN.md 第八节第 6 块）的 Session 具体类型。
// 本包在第 2 块，所以按 Go 的办法收接口不收具体类型——第 6 块的活会话满足它，
// 而本包不必等到那时候才能写完、才能被测。
//
// 它比 [projection.SessionView] 多一个 [LiveSession.Header]：检查点行要绑在
// 这段日志的身份上，见 [Identity]。
type LiveSession interface {
	projection.SessionView

	// Header 是这个会话不可变的存储元数据。
	Header() session.SessionHeader
}

// Options 是建一个 [Cache] 需要的东西。
//
// 源: packages/session/session-projection-cache/src/index.ts:42-52
type Options struct {
	// Registry 是投影单元表，折叠和取切面都靠它。必填。
	Registry *projection.Registry

	// Store 是持久的会话存储，冷读从它那里按水位续读尾巴。必填。
	Store persistence.Store

	// Flush 把一个活会话已提交的事件推到耐久，返回时它们必须真的落盘了。必填。
	//
	// 这是一道**落盘屏障**，不是一个可选优化。检查点切面是在调用它之前取的，
	// 所以先落日志再落缓存行，保证的是缓存永远不会跑到日志前面去：一次崩溃
	// 可以让缓存落在日志后面（下次多折一段尾巴），但不能让缓存里出现一段
	// 从「任何已存日志都不包含的事件」折出来的幽灵状态。没有这道屏障，那种
	// 幽灵值会以一个完全正常的水位堂堂正正地被端出去。
	//
	// 新增: DSH 那边这一步是 `if (ctx.sessions.get(id) === session)
	// await ctx.sessions.flush(session)`——它要先从 cordis 上取活会话表，
	// 核对这个会话是不是还挂在那个 id 底下，再刷。那张表是第 6 块的东西。
	// Go 这边做成钩子：装配方本来就是持有那张表的那一方，「这个会话还在不在」
	// 这个问题只有它答得了。**会话已经脱离时这个钩子必须是空操作**——那时候
	// 表里的条目已经没了，持久化自己的退休排空会把剩下的写完。
	Flush func(live LiveSession) error

	// WriteEveryEvents 是两个必写点之间，攒够多少条已提交事件就强制写一次检查点。
	//
	// 必填且至少为 1，**没有缺省值**，和 DSH 一致：它是一个部署选择，
	// 没有哪个值是普遍正确的。猜一个默认值等于替装配方做了一个它自己都不知道
	// 做过的决定，而这个决定的代价（写太密就是每条事件一次 fsync，写太疏就是
	// 崩溃之后重放很长一段）只有部署方衡量得了。
	WriteEveryEvents int

	// WriteInterval 是两个必写点之间，一份脏检查点最多能不写多久。
	//
	// 必填且为正，理由同 [Options.WriteEveryEvents]。它管的是另一种脏法：
	// 事件不多但间隔很长（比如一次跑了十分钟的工具调用），光靠计数永远不触发。
	WriteInterval time.Duration

	// Background 是节流触发的那些后台写用的 context，留空用 context.Background()。
	//
	// 新增: DSH 那边 `void this.flushSoft(...)` 是一次脱离的异步调用，没有取消
	// 这个概念。Go 里写下去要一个 context，而触发它的那条路（计数到阈值、
	// 间隔到点）手上没有任何调用方的 context——事件早就提交完了，发起它的那次
	// 请求可能已经结束。所以后台写的 context 是**这个缓存自己的寿命**：
	// 装配方在构造时给一个，卸载时取消它，就等于停掉所有还没跑完的后台写。
	Background context.Context

	// Logger 记那几件 fail-soft 路径上咽下去的事，留空用 slog.Default()。
	//
	// 留空**不是**丢弃：这里记的正是没人会主动去查、却必须留下痕迹的那类事
	// ——一次写丢了、一条缓存行解不开。它们的症状都只是「慢一点」。
	Logger *slog.Logger
}

// dirtyState 是一个活会话的节流记账，只对活着的会话存在，脱离时丢掉。
//
// 源: packages/session/session-projection-cache/src/index.ts:54-60
type dirtyState struct {
	// pending 是上一次耐久写之后又提交了多少条事件。
	pending int
	// timer 是间隔触发器；nil 表示当前没有触发器等着。
	timer *time.Timer
	// generation 是触发器的代数，用来认出一个已经过气的定时器回调。
	//
	// 新增: DSH 的 clearTimeout 在单线程里是确定的，清掉了就一定不会再跑。
	// Go 的 [time.Timer.Stop] 返回假时回调可能已经在跑、正卡在 mu 上，所以
	// 回调进来先看自己的代数还是不是当前那一代。同样的写法见
	// [persistence.WriteBehind]。
	generation uint64
}

// Cache 是耐久投影缓存：介质上每个会话一条记录，加上活会话的节流写记账。
//
// 源: packages/session/session-projection-cache/src/index.ts:71-287
//
// 零值不可用，用 [New] 建。它可以被多个 goroutine 同时使用。
type Cache struct {
	registry   *projection.Registry
	store      persistence.Store
	flush      func(live LiveSession) error
	every      int
	interval   time.Duration
	background context.Context
	logger     *slog.Logger
	table      *domain.Table[Record]

	mu     sync.Mutex
	dirty  map[session.SessionID]*dirtyState
	closed bool
}

// errUnrelatedIdentity 是「这条缓存记录绑的是另一段日志」，只在包内当回退理由用。
var errUnrelatedIdentity = errors.New("缓存记录绑定的日志身份和存档里的对不上")

// New 在一个**已经按 [Spec] 打开**的域上建一个缓存。
//
// 源: packages/session/session-projection-cache/src/index.ts:83-89
//
// 新增: 域由装配方打开、也由装配方关闭，理由见包文档第 1 条。这个域是不是
// 按 [Spec] 打开的，由这里取表的那一步核对出来——表名或者记录类型对不上就
// 建不出缓存。
func New(opened *domain.Domain, options Options) (*Cache, error) {
	switch {
	case opened == nil:
		return nil, fmt.Errorf("session/projectioncache: 建缓存需要一个已经打开的域")
	case options.Registry == nil:
		return nil, fmt.Errorf("session/projectioncache: 建缓存需要一张投影单元表")
	case options.Store == nil:
		return nil, fmt.Errorf("session/projectioncache: 建缓存需要一个会话存储")
	case options.Flush == nil:
		// 见 [Options.Flush]：没有它，缓存可能跑到日志前面去。
		return nil, fmt.Errorf("session/projectioncache: 建缓存需要一道落盘屏障（Flush）")
	case options.WriteEveryEvents < 1:
		return nil, fmt.Errorf("session/projectioncache: WriteEveryEvents 至少是 1，给的是 %d",
			options.WriteEveryEvents)
	case options.WriteInterval <= 0:
		return nil, fmt.Errorf("session/projectioncache: WriteInterval 必须是正的，给的是 %s",
			options.WriteInterval)
	}

	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		return nil, err
	}

	background := options.Background
	if background == nil {
		background = context.Background()
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Cache{
		registry:   options.Registry,
		store:      options.Store,
		flush:      options.Flush,
		every:      options.WriteEveryEvents,
		interval:   options.WriteInterval,
		background: background,
		logger:     logger,
		table:      table,
		dirty:      map[session.SessionID]*dirtyState{},
	}, nil
}

// CachedSnapshot 是零 I/O 那一级：直接从已经读进内存的那条记录看一份读切，
// 一条日志都不读。第一个返回值为 nil 表示这个会话没有可用的记录。
//
// 源: packages/session/session-projection-cache/src/index.ts:107-130
//
// meta 是调用方手上那份会话头，它同时是**身份见证**：一条绑着另一段日志的
// 记录在这里就被整条挡掉，见 [Identity]。
//
// 给出来的值旧到上一次耐久检查点那一刻，但绝不会是错的。整块只带**一个**水位
// ——所有被端出来的行里最低的那个，也就是「每个值至少截止到这里是新的」。
// 少报是安全的（客户端那边按高 seq 覆盖低 seq），多报会让一个旧值压住一次推送。
func (c *Cache) CachedSnapshot(meta session.SessionHeader) (*projection.Snapshot, error) {
	record, ok, err := c.recordFor(meta.ID, IdentityOf(meta))
	if err != nil || !ok {
		return nil, err
	}

	values := c.registry.ViewCheckpoint(record.Rows)
	if len(values) == 0 {
		return nil, nil
	}

	asOfSeq := 0
	first := true
	for key := range values {
		// values 的键必然是 record.Rows 的子集：ViewCheckpoint 只端得出它见过的行。
		if seq := record.Rows[key].Seq; first || seq < asOfSeq {
			asOfSeq, first = seq, false
		}
	}
	return &projection.Snapshot{AsOfSeq: asOfSeq, Values: values}, nil
}

// ColdSnapshot 不读整份日志地冷读一个已落地会话的投影：缓存行加上从恢复地板起
// 的一截尾巴，折完写回去，于是下一次冷读起点更近。
//
// 源: packages/session/session-projection-cache/src/index.ts:154-196
//
// 一条被缩短了的日志（崩溃修复截过尾）作废掉的缓存行会触发一次从 seq 0 的整读
// ——阶梯最慢的那一级，但仍然不会崩。这个会话根本没有落地的日志时，
// 错误从持久化那一侧原样穿过来（[persistence.ErrSessionNotFound]）。
func (c *Cache) ColdSnapshot(ctx context.Context, id session.SessionID) (projection.Snapshot, error) {
	record, hasRecord, err := c.table.Get(string(id))
	if err != nil {
		return projection.Snapshot{}, err
	}
	var cached projection.Checkpoint
	if hasRecord {
		cached = record.Rows
	}

	floor, ok := c.registry.RestoreFloor(cached)
	if !ok {
		// 一个单元都没登记：没有可折的东西。但「没有这个会话」这条契约在这条
		// 路上也得成立，所以照样探一次——探不到就报错，探到了就给出空切面
		// 在这份日志末尾的水位。
		probe, probeErr := c.store.ReadFrom(ctx, id, 0)
		if probeErr != nil {
			return projection.Snapshot{}, probeErr
		}
		return projection.Snapshot{AsOfSeq: lastSeq(probe.Events), Values: map[string]any{}}, nil
	}

	tail, err := c.store.ReadFrom(ctx, id, floor)
	if err != nil {
		return projection.Snapshot{}, err
	}

	// 尾巴里那份存档头是身份见证：一条绑着另一段生命的记录（同 id 重建、
	// 存储被换掉）在它的任何一行有机会做种之前就被整条丢掉。
	meta := tail.Meta
	var restored projection.Restored
	reason := error(nil)
	if hasRecord && record.Identity != IdentityOf(tail.Meta) {
		reason = errUnrelatedIdentity
	} else {
		restored, reason = c.registry.Restore(cached, tail.Events, floor)
	}

	if reason != nil {
		// 能走到这里的理由有三种：记录属于另一段日志、某一行落在给进来那截尾巴
		// 或者日志末尾之外、以及某一行的状态解不开。整读会把全部检查点种子撤掉，
		// 让每个单元从 Init 重折一遍——这一定是对的，只是慢。
		//
		// 新增: DSH 的 catch 是空的。第三种理由说明**这个构建自己**写坏了那一行，
		// 而它的症状只是「每次冷读都慢一点」，没有任何人会去查。回退照旧
		// （缓存不该因为自己坏了就让读失败），但必须留下痕迹。见包文档第 4 条。
		c.logger.Warn("session/projectioncache: 缓存行用不了，退回整读",
			slog.String("session", string(id)),
			slog.Any("error", reason))

		whole, wholeErr := c.store.ReadFrom(ctx, id, 0)
		if wholeErr != nil {
			return projection.Snapshot{}, wholeErr
		}
		restored, err = c.registry.Restore(nil, whole.Events, 0)
		if err != nil {
			return projection.Snapshot{}, err
		}
		meta = whole.Meta
	}

	c.putSoft(ctx, id, IdentityOf(meta), restored.Checkpoint, "冷读之后写回")
	return restored.Snapshot, nil
}

// Write 立刻给一个活会话写一份耐久检查点。两个必写点都走它，测试和别的载体也可以调。
//
// 源: packages/session/session-projection-cache/src/index.ts:132-152
//
// 它**不是** fail-soft 的：走 fail-soft 那几条路的调用方自己把失败咽掉。
func (c *Cache) Write(ctx context.Context, live LiveSession) error {
	// 记账先清，再取切面。
	//
	// 新增: DSH 的顺序反过来（先 checkpoint 再 markClean），因为它是单线程的
	// ——那两句之间不可能有事件提交进来，两者等价。Go 里它们是两把不同的锁：
	// 先取切面再清账的话，正好落在中间的那条事件会被清掉计数却又不在切面里，
	// 于是它的脏一直没人记得。反过来先清账，最坏情况是同一条事件被多数一次
	// （切面已经包含它了，账上还记着），代价是多写一次，方向是安全的那一边。
	c.markClean(live.ID())

	rows, err := c.registry.Checkpoint(live)
	if err != nil {
		return err
	}

	// 落盘屏障：切面在上面取完了，所以在这里刷日志，保证切面里的每一条事件都
	// 先于这条缓存行落到耐久上。见 [Options.Flush]。
	if err := c.flush(live); err != nil {
		return err
	}
	return c.put(ctx, live.ID(), IdentityOf(live.Header()), rows)
}

// Observe 记一条已经提交的事件，该写的时候在后台写一次检查点。
//
// 源: packages/session/session-projection-cache/src/index.ts:200-219
//
// 由持有活会话的那一层在事件**提交之后**调。它不阻塞：真正的写在后台跑，
// 失败只记一条日志——一次缓存写失败不该让提交事件的那条路跟着失败。
//
// 三个触发点：[session.EventTurnEnd] 是必写点（多数读要的就是回合结束时那份
// 值），计数和间隔两个阈值节流回合中间那一串。
func (c *Cache) Observe(live LiveSession, event session.Event) {
	c.mu.Lock()
	if c.closed {
		// 关掉之后一条都不写，回合结束那个必写点也不例外：会话比缓存活得长，
		// 而一个已经卸载的缓存不该再往介质上写。见 [Cache.Close]。
		c.mu.Unlock()
		return
	}
	if event.Type == session.EventTurnEnd {
		c.mu.Unlock()
		go c.writeSoft(live, "回合结束")
		return
	}
	state, tracked := c.dirty[live.ID()]
	if !tracked {
		state = &dirtyState{}
		c.dirty[live.ID()] = state
	}
	state.pending++
	if state.pending >= c.every {
		c.mu.Unlock()
		go c.writeSoft(live, "攒够了事件")
		return
	}
	if state.timer == nil {
		state.generation++
		generation := state.generation
		state.timer = time.AfterFunc(c.interval, func() { c.onInterval(live, generation) })
	}
	c.mu.Unlock()
}

// Detach 是活会话变冷的那一刻：写掉最后一份检查点，丢掉它的节流记账。
//
// 源: packages/session/session-projection-cache/src/index.ts:225-229
//
// 这之后这个会话的读全部走 [Cache.ColdSnapshot]，所以这是最后一次机会。
//
// 新增: DSH 那边是即发即忘，Go 这边同步写完并把失败交回调用方，理由见包文档
// 第 3 条。记账无论写成没写成都丢掉——会话已经走了，留着只是泄漏。
func (c *Cache) Detach(ctx context.Context, live LiveSession) error {
	err := c.Write(ctx, live)

	c.mu.Lock()
	defer c.mu.Unlock()
	if state, tracked := c.dirty[live.ID()]; tracked {
		if state.timer != nil {
			state.timer.Stop()
		}
		delete(c.dirty, live.ID())
	}
	return err
}

// Close 停掉所有还等着的间隔触发器，丢掉全部节流记账。
//
// 源: packages/session/session-projection-cache/src/index.ts:231-237
//
// 它**不关那个域**——域是装配方打开的，也归装配方关，见包文档第 1 条。
// 关掉之后 [Cache.Observe] 变成空操作：会话比缓存活得长，而一个已经卸载的
// 缓存不该再往介质上写。
//
// 可以重复调用。
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	for _, state := range c.dirty {
		if state.timer != nil {
			state.timer.Stop()
		}
	}
	clear(c.dirty)
}

// onInterval 是间隔触发器到点。
//
// 源: packages/session/session-projection-cache/src/index.ts:216-218
func (c *Cache) onInterval(live LiveSession, generation uint64) {
	c.mu.Lock()
	state, tracked := c.dirty[live.ID()]
	if c.closed || !tracked || state.generation != generation {
		// 这一枪是过气那一代打的，或者缓存已经关了。
		c.mu.Unlock()
		return
	}
	state.timer = nil
	c.mu.Unlock()

	c.writeSoft(live, "间隔到点")
}

// writeSoft 是一次 fail-soft 的耐久检查点：失败只留一条日志。
//
// 源: packages/session/session-projection-cache/src/index.ts:240-251
func (c *Cache) writeSoft(live LiveSession, trigger string) {
	if err := c.Write(c.background, live); err != nil {
		c.logger.Warn("session/projectioncache: 写检查点失败，缓存留在旧位置",
			slog.String("session", string(live.ID())),
			slog.String("trigger", trigger),
			slog.Any("error", err))
	}
}

// markClean 把一个会话的节流记账清零，并取消它等着的触发器。
//
// 源: packages/session/session-projection-cache/src/index.ts:253-262
func (c *Cache) markClean(id session.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, tracked := c.dirty[id]
	if !tracked {
		return
	}
	state.pending = 0
	// 代数照样往前走：Stop 返回假时那个回调可能已经在跑了，靠代数把它认出来。
	state.generation++
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
}

// recordFor 读出一个会话的记录，只在它绑定的日志身份和 expected 一致时才认。
//
// 源: packages/session/session-projection-cache/src/index.ts:101-105
func (c *Cache) recordFor(id session.SessionID, expected Identity) (Record, bool, error) {
	record, ok, err := c.table.Get(string(id))
	if err != nil || !ok {
		return Record{}, false, err
	}
	if record.Identity != expected {
		return Record{}, false, nil
	}
	return record, true, nil
}

// put 整条替换一个会话的记录。
//
// 源: packages/session/session-projection-cache/src/index.ts:264-271
func (c *Cache) put(ctx context.Context, id session.SessionID, identity Identity, rows projection.Checkpoint) error {
	return c.table.Put(ctx, string(id), Record{Identity: identity, Rows: rows})
}

// putSoft 是 fail-soft 的 [Cache.put]：缓存写绝不该让调用方那次读或者那条事件失败。
//
// 源: packages/session/session-projection-cache/src/index.ts:273-280
func (c *Cache) putSoft(ctx context.Context, id session.SessionID, identity Identity,
	rows projection.Checkpoint, what string) {
	if err := c.put(ctx, id, identity, rows); err != nil {
		c.logger.Warn("session/projectioncache: 写检查点失败，缓存留在旧位置",
			slog.String("session", string(id)),
			slog.String("trigger", what),
			slog.Any("error", err))
	}
}

// lastSeq 是一段日志末尾那条事件的 seq，空日志是 -1。
//
// 源: packages/session/session-projection-cache/src/index.ts:176
func lastSeq(events []session.Event) int {
	if len(events) == 0 {
		return -1
	}
	return events[len(events)-1].Seq
}
