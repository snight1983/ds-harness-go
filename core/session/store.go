// 本文件的作用：装着活会话的那张表——把一个会话登记进来、公布出去、再摘出去，
// 以及挂在它上面的那四组观察者。
//
// 源: packages/core/session/src/index.ts:37-93、780-1157

package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/core/scope"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// CreatedObserver 是一次会话公布的观察者，**有否决权**。
//
// 源: packages/core/session/src/index.ts:44-53（`session/created`）
//
// 返回错误（或者 panic）会让 [Store.Announce] 失败。调用方交出去的那个摘除函数
// 随即把这次登记回滚掉，并配对地发出一次 disposed——所以一个否决掉创建的观察者
// 不会留下一个「登记了但没人知道」的会话。
//
// 新增: DSH 那边监听器可以是 async 函数（返回值被隐式当成 void），于是它多一条
// 「返回的 promise 拒绝了只能记日志，来不及否决」的路。Go 里签名就是同步返回
// error，那条路不存在——想异步的观察者自己起 goroutine，那和 DSH 里 async 监听器
// 的实际效果一样：来不及否决。
type CreatedObserver func(ctx context.Context, session *Session) error

// DisposedObserver 是「一个已公布的会话离开了存储」的观察者。
//
// 源: packages/core/session/src/index.ts:54-63（`session/disposed`）
//
// 只观察，不否决：这条边是配对通知，它已经发生了。观察者 panic 被逐个兜住记日志。
type DisposedObserver func(session *Session)

// EventObserver 是提交之后的追加广播。
//
// 源: packages/core/session/src/index.ts:64-77（`session/event`）
//
// 观察者名单在事件进日志**之前**取好，回调在它**之后**跑：这条广播改变不了
// 这次已经提交的追加。观察者 panic 被逐个兜住记日志。
//
// 持久化插件就挂在这上面——所以它不能阻塞，热路径不为 I/O 等待，缓冲是插件自己的事。
type EventObserver func(session *Session, event sessionlog.Event)

// FlushObserver 是要等的耐久检查点。
//
// 源: packages/core/session/src/index.ts:78-91（`session/flush`）
//
// 所有观察者并行跑，全部结束之后才返回；没有否决这回事——[Store.Flush] 交出的是
// 「谁先按登记顺序失败了」，而不是「谁拦下了这次刷盘」。
type FlushObserver func(ctx context.Context, session *Session) error

// storeLayer 是一个作用域在这套观察者里的全部贡献。
//
// 源: packages/core/session/src/index.ts:37-93（四个 cordis 事件）
//
// 新增: DSH 靠 cordis 的作用域派发过滤监听器，本仓库统一换成
// [scope.Layers]：全局层加各作用域的覆盖层，派发时按载体作用域的父链取并集。
type storeLayer struct {
	created  *scope.AnonymousEntries[CreatedObserver]
	disposed *scope.AnonymousEntries[DisposedObserver]
	events   *scope.AnonymousEntries[EventObserver]
	flush    *scope.AnonymousEntries[FlushObserver]
}

// newStoreLayer 造一层。
func newStoreLayer() *storeLayer {
	return &storeLayer{
		created:  scope.NewAnonymousEntries[CreatedObserver](),
		disposed: scope.NewAnonymousEntries[DisposedObserver](),
		events:   scope.NewAnonymousEntries[EventObserver](),
		flush:    scope.NewAnonymousEntries[FlushObserver](),
	}
}

// IsEmpty 表示这一层四张表全空了，[scope.Layers] 靠它回收空层。
func (l *storeLayer) IsEmpty() bool {
	return l.created.IsEmpty() && l.disposed.IsEmpty() &&
		l.events.IsEmpty() && l.flush.IsEmpty()
}

// entry 是一个会话在存储里那一份登记的全部可变状态。
//
// 源: packages/core/session/src/index.ts:399-410（SessionEntry）
//
// 除了 id、session、carrierKey、store 四个造出来就不再变的字段，其余全部由
// [Store.mutex] 守着。
type entry struct {
	id         sessionlog.SessionID
	session    *Session
	carrierKey *scope.Key
	store      *Store

	// entered 是 [Store.Enter] 交出去那个摘除函数的一次性标记。
	entered bool
	// announced 表示创建公布**开始过**（不是成功过）。它决定摘除时发不发配对的
	// disposed：一次被否决的公布已经让部分观察者看见了这个会话，那条配对的边
	// 必须补上。
	announced bool
	// announcing 表示公布正在进行。
	announcing bool
	// detachRequested 是在公布或发布窗口里提出、等窗口关掉再执行的摘除。
	detachRequested bool
}

// StoreOptions 是造一个 [Store] 的选项。
//
// 新增: DSH 的 SessionStore 是 cordis Service，logger 从 ctx 上取、时钟直接调
// Date.now()。Go 里没有那个隐式容器，两样都显式传进来。
type StoreOptions struct {
	// Logger 用来报告观察者自己 panic 的事故，为 nil 时用 [slog.Default]。
	Logger *slog.Logger

	// Now 交出当下的 Unix 纪元毫秒，为 nil 时用 [time.Now]。
	//
	// 它被传给这个存储建出来的每一个会话，理由和 [Options.Now] 一样：
	// 不可替换的话测试里断言不了时间戳。
	Now func() int64
}

// Store 是活会话的内存表（DSH 那边的 `ctx.sessions`）。
//
// 源: packages/core/session/src/index.ts:786-800
//
// 这里**故意不做持久化**：持久化插件挂在 [Store.OnEvent] 上收事件，在
// [Store.OnFlush] 和摘除时落盘。这条分工是本仓库 session/persistence 那一族包
// 存在的前提。
//
// 新增: 这个类型有自己的互斥锁。它和 [Session] 的锁**从不同时持有**，锁的次序
// 规矩写在 [Session] 的类型注释里。
type Store struct {
	layers *scope.Layers[*storeLayer]
	logger *slog.Logger
	now    func() int64

	// mutex 守住下面三个字段。观察者一律在锁外调用。
	mutex sync.Mutex
	// sessions 是按标识查登记。
	sessions map[sessionlog.SessionID]*entry
	// order 是登记进来的先后。
	//
	// 新增: DSH 用的是 JS 的 Map，它自带插入顺序，[Store.List] 直接遍历就是
	// 创建顺序。Go 的 map 没有顺序，而这个顺序**是语义**（DSH 的文档写明
	// 「in creation order」），所以另存一份。
	order []*entry
	// counter 是自动铸造标识用的计数。
	counter int
}

// NewStore 造一个空存储。
//
// 源: packages/core/session/src/index.ts:795-806
//
// 新增: DSH 的构造函数里那一段 `ctx.inject(['typert'], …)` 把会话注册成一种
// typert 查找类型（让工具参数上写 `session: SessionId` 时能自动解析成 Session）。
// typert 整套不移，理由和本仓库其他几处逐字相同，所以这里没有对应物。
func NewStore(options StoreOptions) (*Store, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	// onChange 传 nil：DSH 那边没有「会话观察者名单变了」这回事，也没有任何东西
	// 需要为此重算缓存。
	layers, err := scope.NewLayers(
		func(*scope.Key) (*storeLayer, error) { return newStoreLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		// 照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	return &Store{
		layers:   layers,
		logger:   logger,
		now:      now,
		sessions: map[sessionlog.SessionID]*entry{},
	}, nil
}

// OnCreated 登记一个创建观察者，返回撤销这次登记的函数。
//
// 源: packages/core/session/src/index.ts:44-53
//
// owner 决定这次登记落在哪一层：[scope.NewRoot] 造的作用域没有身份，落全局层，
// 看得见每一个会话；有身份的作用域落它自己那一层，只看得见从它（或它的子孙）
// 那里登记进来的会话。四个登记方法这条规矩完全一样。
func (s *Store) OnCreated(
	ctx context.Context,
	owner *scope.Scope,
	observer CreatedObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("core/session: 创建观察者不能是 nil")
	}
	return s.layers.Effect(ctx, owner, func(layer *storeLayer) (func(), error) {
		return layer.created.Append(observer), nil
	}, scope.EffectOptions{Label: "sessions.OnCreated()"})
}

// OnDisposed 登记一个摘除观察者，返回撤销这次登记的函数。
//
// 源: packages/core/session/src/index.ts:54-63
func (s *Store) OnDisposed(
	ctx context.Context,
	owner *scope.Scope,
	observer DisposedObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("core/session: 摘除观察者不能是 nil")
	}
	return s.layers.Effect(ctx, owner, func(layer *storeLayer) (func(), error) {
		return layer.disposed.Append(observer), nil
	}, scope.EffectOptions{Label: "sessions.OnDisposed()"})
}

// OnEvent 登记一个追加观察者，返回撤销这次登记的函数。
//
// 源: packages/core/session/src/index.ts:64-77
func (s *Store) OnEvent(
	ctx context.Context,
	owner *scope.Scope,
	observer EventObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("core/session: 追加观察者不能是 nil")
	}
	return s.layers.Effect(ctx, owner, func(layer *storeLayer) (func(), error) {
		return layer.events.Append(observer), nil
	}, scope.EffectOptions{Label: "sessions.OnEvent()"})
}

// OnFlush 登记一个耐久检查点观察者，返回撤销这次登记的函数。
//
// 源: packages/core/session/src/index.ts:78-91
func (s *Store) OnFlush(
	ctx context.Context,
	owner *scope.Scope,
	observer FlushObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("core/session: 刷盘观察者不能是 nil")
	}
	return s.layers.Effect(ctx, owner, func(layer *storeLayer) (func(), error) {
		return layer.flush.Append(observer), nil
	}, scope.EffectOptions{Label: "sessions.OnFlush()"})
}

// CreateOptions 是建一个新会话时给的东西。
//
// 源: packages/core/session/src/types.ts:96-117（CreateSessionOptions）
//
// 新增: DSH 把除 seed 之外的字段裹在一个可选的 `meta` 对象里。这里摊平了：
// 那层嵌套在 DSH 里**不承载任何语义**——`meta` 整个不给、和给了一个每项都不填的
// `meta`，产生的头一模一样。摊平之后少一个只在调用点存在的中间结构体。
type CreateOptions struct {
	// Seed 是构造用的回放／分叉事件，语义见 [Options.Seed]（nil 和空切片不是
	// 一回事）。
	Seed []sessionlog.Event

	// Cwd 是这个会话的工作目录，必须是本机上的绝对路径。存储后端拿它做目录键。
	Cwd string

	// ParentSession 是分叉来源的标识。
	ParentSession sessionlog.SessionID

	// CreatedAt 是创建时刻的 Unix 纪元毫秒；零表示没给，由存储盖上当下。
	//
	// 新增: DSH 用 `createdAt?: number` 区分「没给」和「给了 0」。Go 里另加一个
	// 指针只为了表达 1970 年那一毫秒，代价大于收益——真实的创建时刻不会是它，
	// 而**存下来的**头走的是 [Store.PrepareRestored]，那条路原样保管不经过这里。
	CreatedAt int64

	// SeedLength 是耐久的分叉血统边界，要显式给。
	//
	// 源: packages/core/session/src/types.ts:110-113
	//
	// 它**不能**由 len(Seed) 推出来：一个续跑起来的会话，它的 seed 是自己完整的
	// 存储日志，而这个字段说的是当初从父会话继承了多长的前缀。
	SeedLength int

	// Origin 非空表示这是一个子 agent 的会话，取值只有
	// [github.com/snight1983/ds-harness-go/session.OriginSubagent]。
	Origin sessionlog.Origin

	// DelegationDepth 是委派层数，根会话是 0。
	DelegationDepth int

	// AgentPreset 是建出这个会话的那份 agent 预设名。
	AgentPreset string
}

// RestoreOptions 是从持久化存储里读回一个会话时给的东西。
//
// 源: packages/core/session/src/types.ts:119-130（RestoredSessionOptions）
//
// 这两份数据的**所有权交给这次调用**：它们是刚从存储里读出来、别处没有别名的
// 一份图，验过之后原样接手，不复制。
//
// 新增: DSH 那个 `seedSource: 'persistence'` 判别标签不移——它在 TS 里的作用是
// 把联合类型的两支分开，Go 这边这件事由 [Store.Prepare] 与
// [Store.PrepareRestored] 两个入口做了，一个再写一遍的标签只会多出一种「标签
// 填错」的失败态。
type RestoreOptions struct {
	// Seed 是存下来的那份完整事件日志。
	Seed []sessionlog.Event

	// Header 是存下来的那份会话头，原样接手（版本、标识、血统都照验）。
	Header sessionlog.SessionHeader
}

// Create 建一个会话，登记进存储并公布出去，摘除挂在 owner 上。
//
// 源: packages/core/session/src/index.ts:808-845
//
// id 为空表示让存储自己铸一个 `session-<n>`。
//
// 一个会话必须**和它的循环按次序**一起拆掉的时候（循环最后那几条事件要在存储
// 登记消失之前发布出去）**不要用这个方法**——用 [Store.Prepare] +
// [Store.Enter] + [Store.Announce] 把会话的生命周期折进那个 agent 自己的那一次
// 登记里。理由是本仓库里 owner.Defer 的撤销是**后进先出**的一条链，而两次独立的
// 登记之间没有这个保证。
//
// 公布失败时这次创建整个不算数：摘除跑掉，配对的 disposed 发出去，返回的是
// 公布那条错误和撤销过程中的错误一起。
func (s *Store) Create(
	ctx context.Context,
	owner *scope.Scope,
	id sessionlog.SessionID,
	options CreateOptions,
) (*Session, error) {
	session, err := s.Prepare(id, options)
	if err != nil {
		return nil, err
	}
	detach, err := s.Enter(owner, session)
	if err != nil {
		return nil, err
	}
	// 摘除先挂上去、再公布，理由和 DSH 那句注释一样：一个否决掉创建的观察者要能
	// 把这次登记回滚掉，而不是留下一份没人持有的登记加它那套发布钩子。
	// DSH 靠 generator effect「抛出时释放已经 yield 过的 disposer」拿到这件事，
	// Go 里就是下面这个显式的 errors.Join。
	release, err := owner.Defer("sessions.Create()", detach)
	if err != nil {
		return nil, errors.Join(err, detach(ctx))
	}
	if err := s.Announce(ctx, session); err != nil {
		return nil, errors.Join(err, release(ctx))
	}
	return session, nil
}

// Prepare 造一个会话但**不**登记进存储：铸标识、查重名、装好那份耐久的头。
//
// 源: packages/core/session/src/index.ts:847-902
//
// 和 [Store.Enter] + [Store.Announce] 配套用：一个自己持有复合登记的调用方
// （agent 工厂）把会话的生命周期折进它那**一次**登记里，于是作用域一释放，
// 会话和 agent 是一条按次序拆的链，而不是两个抢着拆的兄弟——后者会在驱动的
// 收尾事件提交之前把发布钩子摘掉，那几条事件就丢了。
//
// 这里查到的重名**不是**权威边界：从这里到 [Store.Enter] 之间调用方可以做任意
// 事情（包括另一次创建），所以真正说了算的那道检查在 Enter 里。这里先查一次是
// 为了让「这个名字已经有人了」在**造这个会话之前**就报出来。
func (s *Store) Prepare(id sessionlog.SessionID, options CreateOptions) (*Session, error) {
	sessionID, err := s.mintID(id)
	if err != nil {
		return nil, err
	}
	createdAt := options.CreatedAt
	if createdAt == 0 {
		createdAt = s.now()
	}
	header := sessionlog.SessionHeader{
		Version:         sessionlog.FormatVersion,
		ID:              sessionID,
		CreatedAt:       createdAt,
		Cwd:             options.Cwd,
		ParentSession:   options.ParentSession,
		SeedLength:      options.SeedLength,
		Origin:          options.Origin,
		DelegationDepth: options.DelegationDepth,
		AgentPreset:     options.AgentPreset,
	}
	return NewSession(sessionID, Options{Seed: options.Seed, Header: &header, Now: s.now})
}

// PrepareRestored 造一个会话但不登记进存储，并**接手**这些持久化产物的所有权。
//
// 源: packages/core/session/src/index.ts:869-871（`seedSource === 'persistence'` 那一支）
//
// 和 [Store.Prepare] 的差别只有两条：事件不复制（调用方交出所有权），以及头是
// 存下来的那一份、原样接手而不是现场合成。
func (s *Store) PrepareRestored(id sessionlog.SessionID, options RestoreOptions) (*Session, error) {
	sessionID, err := s.mintID(id)
	if err != nil {
		return nil, err
	}
	return RestoreSession(sessionID, options.Seed, options.Header, s.now)
}

// mintID 定下这次创建用的标识：没给就铸一个没被占的，给了就查一遍重名。
//
// 源: packages/core/session/src/index.ts:848-858
func (s *Store) mintID(requested sessionlog.SessionID) (sessionlog.SessionID, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if requested == "" {
		for {
			s.counter++
			minted := sessionlog.SessionID(fmt.Sprintf("session-%d", s.counter))
			if _, taken := s.sessions[minted]; !taken {
				return minted, nil
			}
		}
	}
	if _, taken := s.sessions[requested]; taken {
		return "", fmt.Errorf("%w: session %q already exists", ErrSessionExists, string(requested))
	}
	return requested, nil
}

// Enter 把一个 [Store.Prepare] 出来的会话登记进存储，返回摘除它的函数。
//
// 源: packages/core/session/src/index.ts:904-947
//
// 它**不**发创建公布：调用方先把这个摘除函数挂到自己的作用域上，然后才调
// [Store.Announce]，好让一个否决掉创建的观察者把这次登记回滚掉。
//
// 重名在这里**再查一遍**，而且这一次才是权威的：Prepare 和 Enter 都是公开的
// 跨包原语，两者之间调用方可以插进任意工作（包括另一次创建），一个过期的
// prepared 会话绝不能盖掉一份已经活着的同名登记——它那个摘除函数之后会把**真正
// 那一个**删掉。[Store.Create] 和 agent 工厂是背靠背调的，永远踩不到这条，
// 但一个公开的 API 不能假设调用方都这么用。
//
// owner 决定这个会话的**载体作用域**：按作用域登记的观察者只看得见从它（或它的
// 子孙）那里登记进来的会话。它和摘除函数挂在哪儿是两件事——摘除挂哪儿由调用方
// 自己决定。
func (s *Store) Enter(owner *scope.Scope, session *Session) (func(context.Context) error, error) {
	if owner == nil {
		return nil, errors.New("core/session: 登记一个会话需要一个载体作用域")
	}
	if session == nil {
		return nil, errors.New("core/session: 登记的会话不能是 nil")
	}
	id := session.ID()

	s.mutex.Lock()
	if _, taken := s.sessions[id]; taken {
		s.mutex.Unlock()
		return nil, fmt.Errorf("%w: session %q already exists", ErrSessionExists, string(id))
	}
	e := &entry{
		id:         id,
		session:    session,
		carrierKey: owner.Key(),
		store:      s,
		entered:    true,
	}
	if !session.attach(e) {
		s.mutex.Unlock()
		return nil, fmt.Errorf("%w: session %q is already attached to a store", ErrAlreadyAttached, string(id))
	}
	s.sessions[id] = e
	s.order = append(s.order, e)
	s.mutex.Unlock()

	return func(ctx context.Context) error {
		s.detach(e)
		return nil
	}, nil
}

// detach 是 [Store.Enter] 交出去那个摘除函数的实现，一次性。
//
// 源: packages/core/session/src/index.ts:936-946
//
// 一个生命周期观察者手里可能攥着这个能力。公布或者发布还没走完的时候不能真摘：
// 那会在同步的创建派发、或者一次追加的观察者还在跑的时候，把登记和发布钩子抽掉。
// 记下来，等窗口关掉再补上那条配对的 disposed。
func (s *Store) detach(e *entry) {
	s.mutex.Lock()
	if !e.entered {
		s.mutex.Unlock()
		return
	}
	e.entered = false
	// isPublishing 拿的是会话的锁，这里存储的锁正拿着——次序合规，见 [Session]
	// 的类型注释。
	if e.announcing || e.session.isPublishing() {
		e.detachRequested = true
		s.mutex.Unlock()
		return
	}
	announced := s.detachEnteredLocked(e)
	s.mutex.Unlock()

	if announced {
		s.emitDisposed(e)
	}
}

// detachEnteredLocked 把这一份**确切的**登记从表里摘掉，交出「它公布过没有」。
//
// 源: packages/core/session/src/index.ts:949-958
//
// 调用时必须拿着 [Store.mutex]。返回真表示调用方要在锁外发一次配对的 disposed。
//
// 那道 `s.sessions[e.id] != e` 的身份检查挡的是：一个过期的摘除能力不许动后来
// 那一次同名生命周期的观察者和存储。
func (s *Store) detachEnteredLocked(e *entry) bool {
	e.detachRequested = false
	if s.sessions[e.id] != e {
		return false
	}
	delete(s.sessions, e.id)
	for index, candidate := range s.order {
		if candidate == e {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	e.session.release(e)
	return e.announced
}

// Announce 为一个已登记的会话发出**恰好一次**创建公布。
//
// 源: packages/core/session/src/index.ts:960-1000
//
// 和 [Store.Enter] 分开，是为了让调用方先把摘除挂上去（回滚安全，见 Enter）。
//
// 一个观察者失败就当场停下并把错误交出去——这是**否决**：调用方拿着的摘除函数
// 随即把这次登记撤掉，并配对地发一次 disposed。已经跑过的那几个观察者看见过这个
// 会话，所以那条配对的边必须补上，这也是 announced 在派发**之前**就置真的理由。
func (s *Store) Announce(ctx context.Context, session *Session) error {
	e, err := s.liveEntryFor(session)
	if err != nil {
		return err
	}

	s.mutex.Lock()
	if e.announced || e.announcing {
		s.mutex.Unlock()
		return fmt.Errorf("%w: session %q was already announced", ErrAlreadyAnnounced, string(e.id))
	}
	// 先立标记再派发：一个观察者不能在回调里递归地再公布一次，而一次跑到一半就被
	// 否决的公布，回滚时照样要配对地发出 disposed。
	e.announced = true
	e.announcing = true
	s.mutex.Unlock()

	observers := collectObservers(s, e.carrierKey, func(layer *storeLayer) *scope.AnonymousEntries[CreatedObserver] {
		return layer.created
	})
	var failure error
	for _, observer := range observers {
		if err := callCreatedObserver(ctx, observer, session); err != nil {
			failure = fmt.Errorf("session %q: session/created listener rejected: %w", string(e.id), err)
			break
		}
	}

	s.mutex.Lock()
	e.announcing = false
	announced := false
	if e.detachRequested && !e.session.isPublishing() {
		announced = s.detachEnteredLocked(e)
	}
	s.mutex.Unlock()
	if announced {
		s.emitDisposed(e)
	}
	return failure
}

// emitDisposed 发出那条配对的拆除通知，观察者的事故逐个兜住。
//
// 源: packages/core/session/src/index.ts:1002-1011
func (s *Store) emitDisposed(e *entry) {
	observers := collectObservers(s, e.carrierKey, func(layer *storeLayer) *scope.AnonymousEntries[DisposedObserver] {
		return layer.disposed
	})
	for _, observer := range observers {
		s.callDisposedObserver(e.id, observer, e.session)
	}
}

// afterPublish 关掉一次追加的发布窗口之后，把窗口里提出的摘除补上。
//
// 源: packages/core/session/src/index.ts:650-656（append 的 finally 那一段）
//
// 由 [Session.finishPublishing] 在放掉会话的锁**之后**调，所以这里拿存储的锁
// 不会和会话的锁叠上。
func (s *Store) afterPublish(e *entry) {
	s.mutex.Lock()
	announced := false
	if e.detachRequested && !e.announcing {
		announced = s.detachEnteredLocked(e)
	}
	s.mutex.Unlock()
	if announced {
		s.emitDisposed(e)
	}
}

// Flush 跑一遍要等的耐久检查点，交出「有没有观察者参与」。
//
// 源: packages/core/session/src/index.ts:1013-1050
//
// 这是刷盘**唯一**的入口：载体作用域归存储所有，所以调用方（每请求一次的检查点
// 策略、目标轮次驱动的空闲检查点、拆除时的排空、以及读存储前先刷一次的消费方）
// 都要从这里过，而不是自己去派发一次。一个主人，一种写法。
//
// 观察者并行跑，全部结束之后才返回；返回的是**按登记顺序**第一个失败的那条。
//
// 新增: DSH 用 Promise.allSettled。Go 这边是 goroutine 加 WaitGroup，结果按登记
// 下标存回去——「第一个失败」说的是登记顺序，不是先后到达顺序，否则同一批观察者
// 每次跑出来的诊断会不一样。
func (s *Store) Flush(ctx context.Context, session *Session) (bool, error) {
	e, err := s.liveEntryFor(session)
	if err != nil {
		return false, err
	}
	observers := collectObservers(s, e.carrierKey, func(layer *storeLayer) *scope.AnonymousEntries[FlushObserver] {
		return layer.flush
	})
	if len(observers) == 0 {
		return false, nil
	}

	failures := make([]error, len(observers))
	var group sync.WaitGroup
	for index, observer := range observers {
		group.Add(1)
		go func() {
			defer group.Done()
			failures[index] = callFlushObserver(ctx, observer, session)
		}()
	}
	group.Wait()

	for _, failure := range failures {
		if failure != nil {
			return true, failure
		}
	}
	return true, nil
}

// Get 查一个活着的会话。
//
// 源: packages/core/session/src/index.ts:1060-1062
func (s *Store) Get(id sessionlog.SessionID) (*Session, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	e, live := s.sessions[id]
	if !live {
		return nil, false
	}
	return e.session, true
}

// List 给出所有活着的会话，按登记进来的先后。
//
// 源: packages/core/session/src/index.ts:1064-1070
//
// 交回的切片是新的，改它动不了存储。
func (s *Store) List() []*Session {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	sessions := make([]*Session, 0, len(s.order))
	for _, e := range s.order {
		sessions = append(sessions, e.session)
	}
	return sessions
}

// liveEntryFor 取出这个会话**确切的**那份活登记；游离的、已摘除的、或者属于
// 另一个存储的，一律拒。
//
// 源: packages/core/session/src/index.ts:1052-1058
func (s *Store) liveEntryFor(session *Session) (*entry, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: 会话是 nil", ErrNotLive)
	}
	// attachedEntry 自己拿会话的锁并在返回前放掉，这里再拿存储的锁——两把锁不叠。
	e := session.attachedEntry()

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if e == nil || s.sessions[e.id] != e {
		return nil, fmt.Errorf("%w: session %q is not live in this store", ErrNotLive, string(session.ID()))
	}
	return e, nil
}

// eventObservers 取这个载体作用域上该收到追加广播的那些观察者。
//
// 它**不拿存储的锁**：[scope.Layers] 和 [scope.AnonymousEntries] 各自并发安全。
// 这一点是刻意的——追加路径正拿着会话的锁调它（[Session.commit]），要是这里再去
// 拿存储的锁，就多出一条「会话锁 → 存储锁」的边，和 [Store.detach] 那条方向相反。
func (s *Store) eventObservers(key *scope.Key) []EventObserver {
	return collectObservers(s, key, func(layer *storeLayer) *scope.AnonymousEntries[EventObserver] {
		return layer.events
	})
}

// collectObservers 把全局层和载体作用域父链上各层的同一张表叠成一份名单。
//
// 源: packages/core/session/src/index.ts:374-377（collectSessionCallbacks）
//
// 顺序是全局在前、远祖次之、载体自己最后——和本仓库其他几处作用域派发一致。
func collectObservers[T any](
	store *Store,
	key *scope.Key,
	pick func(*storeLayer) *scope.AnonymousEntries[T],
) []T {
	var observers []T
	for observer := range pick(store.layers.Global()).Values() {
		observers = append(observers, observer)
	}
	if key == nil {
		return observers
	}
	for _, layer := range store.layers.ChainLayers(key) {
		for observer := range pick(layer).Values() {
			observers = append(observers, observer)
		}
	}
	return observers
}

// callCreatedObserver 跑一个创建观察者，把它的 panic 转成一条否决。
//
// panic 也算否决不是随手定的：这个观察者的返回值**就是**否决权，而一个 panic 到
// 底是「拒绝」还是「我这儿坏了」分不出来。当成拒绝的话最坏是白建一次会话；
// 当成放行的话，一个装配到一半就炸了的持久化插件会让这个会话被当成已经落好盘的。
func callCreatedObserver(ctx context.Context, observer CreatedObserver, session *Session) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("观察者 panic 了: %v", recovered)
		}
	}()
	return observer(ctx, session)
}

// callDisposedObserver 跑一个摘除观察者，把它的 panic 兜成一条日志。
func (s *Store) callDisposedObserver(id sessionlog.SessionID, observer DisposedObserver, session *Session) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Warn("core/session: 摘除观察者 panic 了",
				"session", string(id), "panic", fmt.Sprint(recovered))
		}
	}()
	observer(session)
}

// callEventObserver 跑一个追加观察者，把它的 panic 兜成一条日志。
//
// 源: packages/core/session/src/index.ts:379-397（invokeContainedSessionObservers）
//
// 事件一旦进了日志这次追加就算提交了，所以这里**只记不报**：一个观察者坏了不能
// 让一条已经被接受的事件回头变成失败，也不能挡住后面的观察者看见它。
func (s *Store) callEventObserver(
	id sessionlog.SessionID,
	observer EventObserver,
	session *Session,
	event sessionlog.Event,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Warn("core/session: 追加观察者 panic 了",
				"session", string(id), "seq", event.Seq, "panic", fmt.Sprint(recovered))
		}
	}()
	observer(session, event)
}

// callFlushObserver 跑一个刷盘观察者，把它的 panic 转成一条失败。
//
// 源: packages/core/session/src/index.ts:1035-1043
//
// 和创建那一侧同理：刷盘是调用方自己的失败边界，一个 panic 掉的持久化插件绝不能
// 被读成「已经落盘了」。
func callFlushObserver(ctx context.Context, observer FlushObserver, session *Session) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("core/session: 刷盘观察者 panic 了: %v", recovered)
		}
	}()
	return observer(ctx, session)
}
