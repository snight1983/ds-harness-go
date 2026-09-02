// 本文件的作用：把一个后端接到一批活会话上的那个编排器——它怎么造出来、怎么挂到
// 运行时上、拆下来的时候怎么把在途的写排干，以及那四条来自会话存储的观察者各自
// 转成本层的哪一件事。
//
// 源: packages/session/session-persistence/src/coordinator.ts:588-758
//
// 编排本身分在几个文件里：本文件是骨架和生命期，coordinator_chain.go 是那把按
// 身份串行的锁和读路径，coordinator_prepare.go 是准备／认领那一路，
// coordinator_write.go 是写路径。拆开只是为了读，它们是同一个类型。

package persistence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// CoordinatorOptions 是编排器的两个可调参数。
//
// 源: packages/session/session-persistence/src/coordinator.ts:84-89
//
// 新增: DSH 那两个字段是 `?: number`，构造时逐个查 `Number.isSafeInteger`、
// 查下界、查上界。Go 这边类型已经把「是不是整数」钉死了，只剩取值范围；而
// **零一律表示「没给」**——两个字段的默认值都不是零，所以拿零当没给不会把任何
// 一种明确的意思静默改写掉（对照 compaction/basic 那几个必须用指针的字段）。
type CoordinatorOptions struct {
	// PreparedSessionCacheSize 是那个准备池留着备用的就绪条目上限；
	// 零表示用 [DefaultPreparedSessionCacheSize]，负数报错。
	PreparedSessionCacheSize int

	// WriteBatchMaxDelay 是一批写最多攒多久；零表示用
	// [DefaultWriteBatchMaxDelay]，负数报错。
	//
	// 新增: DSH 还有一条 `> MAX_WRITE_BATCH_DELAY_MS` 的上界。那个常量整条
	// OUT_OF_SCOPE（见 doc.go），理由是它挡的是「毫秒数写成了秒数」这类
	// 数量级笔误，而 Go 这边是 [time.Duration]，单位由类型带着，那种笔误
	// 在类型上就不成立。
	WriteBatchMaxDelay time.Duration

	// MaxStoredEvents 是一份存档最多留几条事件；零表示用
	// [DefaultMaxStoredEvents]，负数报错。
	//
	// 新增: 上游没有这一条，理由见 [DefaultMaxStoredEvents]。**没有关掉它的
	// 取值**：「日志的头部会被删」是权威前提（docs/session-log-limit.md 的决定
	// 第 13 条），留一个关掉的口子就等于留一条谁都可以退回旧前提的路。
	// 后端实现不了 [TrimmingBackend] 时这个数不起作用，那是介质的能力问题，
	// 不是一个开关。
	MaxStoredEvents int
}

// Sessions 是本层要的那一小片会话存储。
//
// 源: packages/session/session-persistence/src/coordinator.ts:600-604（ctx.sessions）
//
// 新增: DSH 从 cordis 容器里拿整个 `ctx.sessions` 服务。Go 没有那个容器，所以
// 摆成一个窄接口明着传进来——窄到只剩本层真正用得着的那几条。
// 一个真的 [github.com/snight1983/ds-harness-go/core/session.Store] 结构上就满足它。
// 成例是 compaction/basic.Agents。
type Sessions interface {
	// Get 按身份找一个活会话。
	Get(id session.SessionID) (*coresession.Session, bool)

	// List 列出此刻所有活会话，装载时补种要用。
	List() []*coresession.Session

	// PrepareRestored 从一份存档前缀造一个还没发布的会话。
	PrepareRestored(id session.SessionID, options coresession.RestoreOptions) (*coresession.Session, error)

	// OnCreated 登记一个「一个会话进了存储」的观察者。
	OnCreated(ctx context.Context, owner *scope.Scope, observer coresession.CreatedObserver) (func(context.Context) error, error)

	// OnEvent 登记一个「一条事件提交进日志了」的观察者。
	OnEvent(ctx context.Context, owner *scope.Scope, observer coresession.EventObserver) (func(context.Context) error, error)

	// OnFlush 登记一个「有人要求把这段会话落到耐久层」的观察者。
	OnFlush(ctx context.Context, owner *scope.Scope, observer coresession.FlushObserver) (func(context.Context) error, error)

	// OnDisposed 登记一个「一个会话退场了」的观察者。
	OnDisposed(ctx context.Context, owner *scope.Scope, observer coresession.DisposedObserver) (func(context.Context) error, error)
}

// CoordinatorDeps 是造一个编排器要的那几样协作者。
//
// 源: packages/session/session-persistence/src/coordinator.ts:589-604（构造函数参数）
type CoordinatorDeps struct {
	// Backend 是真的落盘的那一层，必填。
	Backend Backend

	// Sessions 是那张活会话表，必填。
	Sessions Sessions

	// Vocabulary 是这个后端认得的事件词汇；零值表示只认核心那一套。
	//
	// 新增: DSH 那个参数是 `vocabulary?: SessionVocabulary`，缺省回落到
	// 核心词汇。Go 里 [github.com/snight1983/ds-harness-go/session.Vocabulary] 是值类型、零值是
	// 一份空词汇，所以「没给」由 `len(Types()) == 0` 认出来——一份**空的**
	// 词汇和「没给」在这里是同一件事：一个一种事件都不认的后端存不下任何
	// 会话，那不是一份有意义的配置。
	Vocabulary session.Vocabulary

	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
}

// sessionState 是编排器记着的、某个身份在**存档那一侧**的样子。
//
// 源: packages/session/session-persistence/src/coordinator.ts:218-235
type sessionState struct {
	// meta 是这份存档的头。
	meta session.SessionHeader

	// baseSeq 是这份存档现存最早一条事件的 seq，nextSeq 是下一条要写的 seq。
	//
	// 新增: 上游只有一个 `cursor`，注释写的是「已经落盘的事件数，**也就是**下一条
	// 要写的 seq」——两者相等的前提是日志从 0 起、一条不删。本仓库的日志会从最老的
	// 一头弹出事件（见 docs/session-log-limit.md），条数和 seq 就此分家：拿条数当 seq
	// 会把已经落盘的那一段再写一遍，拿 seq 当下标会切错位置，两样都不报错。
	baseSeq int
	nextSeq int

	// started 表示上面那两个数已经定下来了。
	//
	// 新增: 上游拿 `cursor === 0` 当「还什么都没落盘」。新前提下 0 既可能是一个
	// 真起点、也可能根本不是起点，「还没定」得单独说，见 docs/session-log-limit.md
	// 的原则第 3 条。
	started bool

	// materialized 表示这个身份在后端那边已经真的存在了（不只是一份内存里的意图）。
	materialized bool
	// owner 非 nil 表示这个身份此刻归那个活会话所有；nil 表示没有活会话认领它。
	owner *coresession.Session
}

// liveState 是一个活会话在编排器这边的运行期状态。
//
// 源: packages/session/session-persistence/src/coordinator.ts:237-241
//
// 新增: DSH 那个字段是 `init: Promise<void>`，一个 promise 同时装着「入册完了
// 没有」和「入册成功没有」。Go 里拆成一个信号通道加一个错误字段，做法和
// [preparationEntry.loaded] 那一处逐字相同。
type liveState struct {
	// ready 在入册落定时关闭；关掉之后 err 不再变，读它不必上锁。
	ready chan struct{}
	err   error
	// once 保证 finish 只落定一次——入册那条路和 attachPrepared 那条路
	// 在极端交错下都可能走到它。
	once sync.Once

	// writes 是这个会话那条攒批的写。
	writes *WriteBehind
}

// finish 把入册的结论落定，重复调是安全的。
func (s *liveState) finish(err error) {
	s.once.Do(func() {
		s.err = err
		close(s.ready)
	})
}

// wait 等入册落定，交回它的结论。
//
// 它**不收 ctx**：入册是这个会话上所有写入方共享的一件事，某一个写入方走了不该
// 把它掐掉——而且它本来就跑在一条不可取消的后台 ctx 上。
func (s *liveState) wait() error {
	<-s.ready
	return s.err
}

// chainEntry 是某个身份上那把串行锁，以及此刻有几个人在排队等它。
//
// 源: packages/session/session-persistence/src/coordinator.ts:601-605（chains）
//
// 新增: DSH 的 `chains` 是一张 id → Promise 的表，靠「把新的活儿接到上一个
// promise 后面」排队，表项在链尾就是自己时删掉。Go 里换成一个容量为一的通道
// 当信号量，另加一个 holders 计数决定什么时候把表项删掉——**表项在不在**是
// [Coordinator.drain] 判断「静下来了没有」的依据，所以它必须准。
type chainEntry struct {
	lock    chan struct{}
	holders int
}

// retirement 是某个身份上一次正在跑的退场。
//
// 源: packages/session/session-persistence/src/coordinator.ts:597-598（retirements）
type retirement struct {
	done chan struct{}
}

// Coordinator 是那个把一个后端接到一批活会话上的编排器。
//
// 源: packages/session/session-persistence/src/coordinator.ts:588-1362
//
// 它**不是**一个 [Store]：一个后端自己实现 Store，把其中大部分转给这里。
// 分工写在 [Store] 上——Locate / ReadRaw / ListSnapshots 是后端自己的事，
// 而 Create / Append / Load / Inspect / ReadFrom / Prepare 这几条要跨「活会话」
// 和「存档」两边对账，那是本类型的事。[Coordinator.List] 是个例外：它按分工
// 属于后端，但为了让编排器**自己**就能满足
// [github.com/snight1983/ds-harness-go/core/agentloop.SessionPersistence]
// 而在这里转了一手，理由写在那个方法上。
//
// 并发口径：mutex 守住下面那四张表，以及每个 [sessionState]、[liveState] 的
// 可变字段。按身份的**次序**由 chains 那把串行锁给，不由 mutex 给。
// 纪律是**绝不拿着 mutex 去调 serialize、后端方法、或者 preparations 上的任何
// 方法**——那三样都会阻塞，攥着这把锁跑会把整个编排器锁死。
type Coordinator struct {
	backend Backend

	// trimmer 是同一个后端的更宽视角，弹不动的后端上它是 nil。
	trimmer TrimmingBackend

	sessions           Sessions
	vocabulary         session.Vocabulary
	logger             *slog.Logger
	writeBatchMaxDelay time.Duration
	maxStoredEvents    int
	preparations       *preparations

	// background 是那些**不该被某一次调用取消**的活儿用的 ctx：攒批的写、
	// 入册、退场。[Install] 会把它换成 `context.WithoutCancel(装载的 ctx)`,
	// 于是它带得上装载方 ctx 上的那些值，却不跟着某一次请求断掉。
	//
	// 新增: DSH 那几处传的是 `undefined`（没有 signal）。Go 里没有「不给
	// ctx」这个选项，所以要一条明确的、不会被掐掉的 ctx。
	background context.Context

	mutex sync.Mutex
	// idle 挂在 mutex 上，[Coordinator.drain] 靠它等到没有在途活儿。
	idle        *sync.Cond
	states      map[session.SessionID]*sessionState
	live        map[*coresession.Session]*liveState
	retirements map[session.SessionID]*retirement
	chains      map[session.SessionID]*chainEntry
}

// NewCoordinator 造一个编排器。
//
// 源: packages/session/session-persistence/src/coordinator.ts:589-620
func NewCoordinator(deps CoordinatorDeps, options CoordinatorOptions) (*Coordinator, error) {
	switch {
	case deps.Backend == nil:
		return nil, errors.New("session/persistence: 编排器要一个后端")
	case deps.Sessions == nil:
		return nil, errors.New("session/persistence: 编排器要一张活会话表")
	case options.PreparedSessionCacheSize < 0:
		return nil, fmt.Errorf(
			"session/persistence: 准备池容量不能是负数（给的是 %d）",
			options.PreparedSessionCacheSize)
	case options.WriteBatchMaxDelay < 0:
		return nil, fmt.Errorf(
			"session/persistence: 攒批时长不能是负数（给的是 %s）",
			options.WriteBatchMaxDelay)
	case options.MaxStoredEvents < 0:
		return nil, fmt.Errorf(
			"session/persistence: 存档条数上限不能是负数（给的是 %d）",
			options.MaxStoredEvents)
	}

	capacity := options.PreparedSessionCacheSize
	if capacity == 0 {
		capacity = DefaultPreparedSessionCacheSize
	}
	delay := options.WriteBatchMaxDelay
	if delay == 0 {
		delay = DefaultWriteBatchMaxDelay
	}
	maxStoredEvents := options.MaxStoredEvents
	if maxStoredEvents == 0 {
		maxStoredEvents = DefaultMaxStoredEvents
	}
	// 后端弹不弹得动是它自己的形状，问一次记下来，不用每批写都断言一遍。
	trimmer, _ := Trimming(deps.Backend)
	vocabulary := deps.Vocabulary
	if len(vocabulary.Types()) == 0 {
		vocabulary = session.CoreVocabulary()
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	coordinator := &Coordinator{
		backend:            deps.Backend,
		trimmer:            trimmer,
		sessions:           deps.Sessions,
		vocabulary:         vocabulary,
		logger:             logger,
		writeBatchMaxDelay: delay,
		maxStoredEvents:    maxStoredEvents,
		preparations:       newPreparations(capacity),
		background:         context.Background(),
		states:             map[session.SessionID]*sessionState{},
		live:               map[*coresession.Session]*liveState{},
		retirements:        map[session.SessionID]*retirement{},
		chains:             map[session.SessionID]*chainEntry{},
	}
	coordinator.idle = sync.NewCond(&coordinator.mutex)
	return coordinator, nil
}

// Backend 交出这个编排器背后那个后端。
func (c *Coordinator) Backend() Backend { return c.backend }

// registration 是一条观察者登记：装它的那句，和它自己的名字。
type registration struct {
	label  string
	attach func() (func(context.Context) error, error)
}

// Install 把写路径挂到运行时上，交回把它整个摘下来的函数。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1086-1137
//
// **那条排干先登记、观察者后登记**，次序是要紧的：作用域按登记的反序拆，于是
// 拆的时候先摘掉那四条观察者、再跑排干——事件的入口在排干开始之前就关死了，
// 否则最后这一趟排干永远等不到静止（一边排一边有新的写进来）。
// DSH 那边靠 cordis 的同一条反序规则，注释写的是同一件事。
func (c *Coordinator) Install(ctx context.Context, owner *scope.Scope) (func(context.Context) error, error) {
	if owner == nil {
		return nil, errors.New("session/persistence: 需要一个持有这次登记的作用域")
	}
	c.background = context.WithoutCancel(ctx)

	var undo []func(context.Context) error
	drain, err := owner.Defer(c.backend.Name()+" 写路径排干", c.drain)
	if err != nil {
		return nil, fmt.Errorf("session/persistence: 登记排干失败：%w", err)
	}
	undo = append(undo, drain)

	registrations := []registration{
		{"创建", func() (func(context.Context) error, error) {
			return c.sessions.OnCreated(ctx, owner, c.onSessionCreated)
		}},
		{"事件", func() (func(context.Context) error, error) {
			return c.sessions.OnEvent(ctx, owner, c.onSessionEvent)
		}},
		{"刷盘", func() (func(context.Context) error, error) {
			return c.sessions.OnFlush(ctx, owner, c.onSessionFlush)
		}},
		{"销毁", func() (func(context.Context) error, error) {
			return c.sessions.OnDisposed(ctx, owner, c.onSessionDisposed)
		}},
	}
	for _, entry := range registrations {
		stop, err := entry.attach()
		if err != nil {
			for _, back := range slices.Backward(undo) {
				_ = back(context.WithoutCancel(ctx))
			}
			return nil, fmt.Errorf("session/persistence: 装%s观察者失败：%w", entry.label, err)
		}
		undo = append(undo, stop)
	}

	// 补种：装载之前就已经存在的那些活会话不会再发一次「创建」，这里一次性
	// 把它们入册。热替换之后重新装载走的就是这条路。
	for _, live := range c.sessions.List() {
		c.initFor(live)
	}

	return func(undoCtx context.Context) error {
		failures := make([]error, 0, len(undo))
		for _, stop := range slices.Backward(undo) {
			failures = append(failures, stop(undoCtx))
		}
		return errors.Join(failures...)
	}, nil
}

// drain 是拆装时最后跑的那一趟：把每个活会话的缓冲写干净、等到没有在途活儿、
// 再关掉后端。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1091-1112
//
// 关后端那一步在最后，而且**排干失败时它的错要让位**：调用方最想知道的是
// 「有没有东西没写下去」，一个关闭失败盖住那个才是真的坏。
func (c *Coordinator) drain(ctx context.Context) error {
	c.mutex.Lock()
	sessions := make([]*coresession.Session, 0, len(c.live))
	for live := range c.live {
		sessions = append(sessions, live)
	}
	c.mutex.Unlock()

	// 并发刷，逐个收结论——对应 DSH 的 `settledErrors(map(flush))`：一个会话
	// 刷不下去不能让别的会话跟着不刷。
	failures := make([]error, len(sessions))
	var group sync.WaitGroup
	for index, live := range sessions {
		group.Add(1)
		go func(index int, live *coresession.Session) {
			defer group.Done()
			failures[index] = c.flush(live)
		}(index, live)
	}
	group.Wait()

	// 等到那些按身份串行的活儿全都收手。刷盘本身可能又排出新的活儿来，
	// 所以这一圈在刷完之后。
	//
	// 新增: DSH 只等 `chains`。这里连 retirements 一起等，堵的是 Go 独有的
	// 一条缝：一次退场是在自己的 goroutine 里跑的，它还没来得及占上那把串行
	// 锁时 chains 里是看不见它的，光等 chains 会当成已经静止了。
	c.mutex.Lock()
	for len(c.chains) > 0 || len(c.retirements) > 0 {
		c.idle.Wait()
	}
	remaining := make(map[*coresession.Session]*liveState, len(c.live))
	for live, state := range c.live {
		remaining[live] = state
	}
	c.mutex.Unlock()

	// 静止之后再点一遍名：这一趟排干走完，就没有任何一条路会再写这些事件了。
	// 手上还留着东西说明上面那几次刷没能把它们送下去，而一份短了一截的会话日志
	// 事后看不出短——所以宁可让拆解报错，也不能一声不响地走完。见
	// [ErrWritesAbandoned]。
	//
	// 这里只点名不封口：拆完还可能再装一次（热替换就是），而 c.live 里的表项
	// 是跨那次重装留着的，封掉会让重装之后的写全部落进一个已经废掉的控制器。
	for live, state := range remaining {
		if state.writes.HasWork() {
			failures = append(failures, fmt.Errorf("会话 %s：%w", live.ID(), ErrWritesAbandoned))
		}
	}

	drainErr := errors.Join(failures...)
	if drainErr != nil {
		drainErr = fmt.Errorf("%s 排干失败：%w", c.backend.Name(), drainErr)
	}
	if closable, ok := Closable(c.backend); ok {
		if err := closable.Close(ctx); err != nil && drainErr == nil {
			return err
		}
	}
	return drainErr
}

// onSessionCreated 在一个会话进了存储时把它入册。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1115-1117
//
// **不等入册的结论**：入册要读一遍存档，而这条观察者是同步挂在会话创建上的,
// 让创建等一次磁盘读会把那条路径变慢一个数量级。真出错时错留在
// [liveState.err] 里，第一次写或者第一次刷盘会撞上它。
func (c *Coordinator) onSessionCreated(ctx context.Context, live *coresession.Session) error {
	c.initFor(live)
	return nil
}

// onSessionEvent 把一条刚提交的事件塞进这个会话的攒批写。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1119-1121
func (c *Coordinator) onSessionEvent(live *coresession.Session, event session.Event) {
	c.initFor(live).writes.Enqueue(event)
}

// onSessionFlush 把这个会话缓冲着的写立刻落下去。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1123-1125
func (c *Coordinator) onSessionFlush(ctx context.Context, live *coresession.Session) error {
	return c.flush(live)
}

// onSessionDisposed 在一个会话退场时把它刷干净并从册子上划掉。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1127-1129
func (c *Coordinator) onSessionDisposed(live *coresession.Session) { c.retire(live) }
