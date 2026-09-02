// 本文件的作用：活着的会话本身——一份只增不改的事件日志，加上挂在它上面的
// 那几份增量折叠（请求头、路由元数据、派生消息）。
//
// 源: packages/core/session/src/index.ts:416-758

package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// Options 是造一个游离会话时给的东西。
//
// 源: packages/core/session/src/index.ts:471-479（Session.create 的三个参数）
type Options struct {
	// Seed 是构造用的回放／分叉事件。
	//
	// **nil 和长度为零的非 nil 切片不是一回事**：nil 表示压根没给 seed，
	// 长度为零表示给了一份空 seed。后者照样会补上那条 session/end-seed 标记
	//（[Store.Fork] 分叉一个空会话走的就是这条路），前者不补。这是本仓库
	// 到处在用的那条 nil 与空切片之分的又一次应用。
	Seed []sessionlog.Event

	// Header 是存储那一侧给的创建元数据；nil 表示没给，这里合成一份最小的。
	Header *sessionlog.SessionHeader

	// Now 交出当下的 Unix 纪元毫秒；nil 用 [time.Now]。
	//
	// 新增: DSH 直接调 Date.now()。做成可替换的理由和本仓库
	// session/telemetry 那一处逐字相同——不然测试里断言不了时间戳。
	Now func() int64

	// BaseSeq 是 Seed 第一条事件应有的 seq，也是这份日志的起点；默认 0。
	//
	// 新增: 存储会从最老的一头弹出事件（见 docs/session-log-limit.md），一份续跑
	// 起来的日志因此可能从 500 起。它由**装配方显式给**，不从 `Seed[0].Seq` 推：
	// 推的话就等于放弃对第一条的校验，一份 seq 写错了的 seed 会被读成「起点在那儿」
	// 而不是被拒——分叉一个子会话时那正是最需要拦住的错。起点是存储那一侧的事实，
	// 只有拿着存档的人说得出来。
	BaseSeq int
}

// Session 是一个事件溯源的会话：一份只增不改的
// [github.com/snight1983/ds-harness-go/session.Event] 日志。
//
// 源: packages/core/session/src/index.ts:416-425
//
// 活的实例从 [Store.Create] 拿，游离的实例从 [NewSession] 或 [RestoreSession] 拿。
// 用一份已有的日志做 seed 就是回放或者分叉一个会话。
//
// 新增: 这个类型有自己的互斥锁，理由见包文档。锁的次序规矩是**存储的锁可以在
// 会话的锁之前拿，不许在它之后拿**：追加路径收集观察者时走的是
// [github.com/snight1983/ds-harness-go/core/scope.Layers]，那一层自己并发安全，不碰存储的锁，
// 于是「会话锁 → 存储锁」这条边根本不存在。
type Session struct {
	// mutex 守住下面每一个字段。观察者一律在锁外调用。
	mutex sync.Mutex

	header       sessionlog.SessionHeader
	firstLiveSeq int
	now          func() int64

	// baseSeq 是 log[0] 的 seq，也就是这份日志现存最早一条的序号。
	//
	// 新增: 上游把「下标就是 seq」当成不变量，于是这个值恒为 0、根本不存在。
	// 本仓库的存储会从最老的一头弹出事件（见 docs/session-log-limit.md），一份续跑
	// 起来的日志因此可能从 500 起。少了这个字段，[Session.Append] 会把下一条的
	// seq 算成 len(log) 而不是 500+len(log)，新事件**静默撞掉**已有的那一段。
	baseSeq int
	// log 是那份只增不改的日志，下标加上 baseSeq 等于 seq。
	log []sessionlog.Event
	// surface 是表面的唯一增量持有者，同时也是追加边界上的校验器。
	surface *sessionlog.SurfaceFolder
	// eventsSnapshot 是 [Session.Events] 上一次交出去的那份复制，追加时作废。
	eventsSnapshot []sessionlog.Event

	// headerFold 是请求头事件的增量折叠，headerFoldSeq 是它走到的日志位置。
	headerFold    sessionlog.EpochHeader
	headerFoldOK  bool
	headerFoldSeq int

	// contextFold 是 request/context 事件的增量折叠。
	contextFold    sessionlog.RequestContext
	contextFoldOK  bool
	contextFoldSeq int

	// derived 是派生消息的缓存，derivedNodes 是它投影过的表面节点数，
	// derivedGeneration 是它当初依据的那一代表面。
	derived           []llm.Message
	derivedNodes      int
	derivedGeneration int

	// entry 是这个会话在某个存储里的登记；nil 表示它是游离的。
	//
	// 新增: DSH 用一张模块私有的 WeakMap 把会话映到它的登记上，为的是让
	// Session 在公开面上和 SessionStore 无关。Go 里两者同包，一个不导出的
	// 字段做的是同一件事，见包文档。
	entry *entry

	// publishing 表示这次追加正处在「已提交、观察者还在跑」的窗口里。
	//
	// 新增: DSH 那个标记叫 appending，只挡住一件事——某个 session/event 观察者
	// 在回调里又对同一个会话追加。Go 这边它同时挡住另一个 goroutine 同时追加。
	// 一个会话的日志只该有一个写者，见包文档。
	publishing bool
}

// NewSession 造一个游离会话：验一遍借来的 seed 与存储元数据，各自复制一份。
//
// 源: packages/core/session/src/index.ts:471-479（Session.create）
//
// 「借来的」是关键：seed 里每一条事件都会被
// [github.com/snight1983/ds-harness-go/session.Event.Clone] 复制，调用方留着的那些切片之后怎么改
// 都改不动这份日志。
func NewSession(id sessionlog.SessionID, options Options) (*Session, error) {
	return newSession(id, options, false)
}

// RestoreSession 造一个游离会话，并**接手**这些持久化产物的所有权。
//
// 源: packages/core/session/src/index.ts:484-495（Session.fromRestore）
//
// 落盘格式、事件信封、序号连续性、表面转移、头字段全部照验，但这些事件
// **不复制**：调用方交出的是刚从存储里读出来、别处没有别名的一份图。
// 想留着自己那一份就用 [NewSession]。
//
// baseSeq 是 seed 第一条应有的 seq，也就是这份存档的起点，见 [Options.BaseSeq]；
// 存储那一侧由 StoredPrefix 的同名字段交出来。
//
// 新增: DSH 在这条路上验完之后原地 freezeRestoredObject。Go 里「不许改」由
// 「调用方交出了所有权」兑现，见包文档。
func RestoreSession(
	id sessionlog.SessionID,
	seed []sessionlog.Event,
	baseSeq int,
	header sessionlog.SessionHeader,
	now func() int64,
) (*Session, error) {
	return newSession(id, Options{Seed: seed, BaseSeq: baseSeq, Header: &header, Now: now}, true)
}

// newSession 是两条构造路径共用的那一段。
//
// 源: packages/core/session/src/index.ts:493-544
func newSession(id sessionlog.SessionID, options Options, restore bool) (*Session, error) {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	baseSeq := options.BaseSeq
	if baseSeq < 0 {
		return nil, fmt.Errorf("%w: 日志的起点不能是负数，给的是 %d", ErrInvalidSeed, baseSeq)
	}
	session := &Session{now: now, baseSeq: baseSeq, surface: sessionlog.NewSurfaceFolder(baseSeq)}

	// 恢复路径先验头：这条路上头是和事件一起从存储里读出来的，头不对就说明
	// 这份归档整个不该打开，没必要再去验后面几千条事件。新建路径的头是调用方
	// 现场给的，DSH 把它留到 seed 之后再验，这里照抄那个次序。
	if restore {
		if options.Header == nil {
			return nil, fmt.Errorf("%w: 恢复一个会话必须给出它存下来的头", ErrInvalidHeader)
		}
		if err := validateSessionHeader(id, *options.Header); err != nil {
			return nil, err
		}
		session.header = *options.Header
	}

	if options.Seed != nil {
		// seed 按**和 Append 一模一样**的那套约束验：一次回放或分叉不能造出
		// 一份没有任何持久化后端存得下的活日志。少了这道检查，一份坏 seed 只会
		// 在后来某次刷盘被后端拒收时才浮出来，或者干脆表现为活日志和磁盘悄悄
		// 分了岔。
		for index, source := range options.Seed {
			event := source
			if !restore {
				event = source.Clone()
			}
			if err := validateSeedEvent(event, index); err != nil {
				return nil, err
			}
			location := fmt.Sprintf("seed event at index %d", index)
			if err := validateSupportedRequestHeader(event.Type, event.Data, location, ErrInvalidSeed); err != nil {
				return nil, err
			}
			if event.Seq != baseSeq+index {
				return nil, fmt.Errorf(
					"%w: %s has seq %d (expected %d); seed must be contiguous from %d",
					ErrInvalidSeed, location, event.Seq, baseSeq+index, baseSeq,
				)
			}
			// seed 里每一条都走和一次活追加、和一次整段折叠完全相同的那道转移。
			// Push 是原子的：验不过时表面一动不动，这条事件也不会留下。
			if _, _, err := session.surface.Push(event); err != nil {
				return nil, fmt.Errorf("%w: invalid seed event at index %d: %w", ErrInvalidSeed, index, err)
			}
			session.log = append(session.log, event)
		}
	}

	session.firstLiveSeq = baseSeq + len(session.log)
	if !restore {
		header, err := snapshotSessionHeader(id, options.Header, now)
		if err != nil {
			return nil, err
		}
		session.header = header
	}

	// 标记在这里补，好让某个后端捕获这份创建 seed 时它已经在 events 里了——
	// 装载时不必再写一次。已经以它结尾的 seed 不再补：一个冷会话是被第一次
	// 触碰时唤醒的，反复打开同一个会话不能让它的日志每开一次长一条。
	needsMarker := options.Seed != nil &&
		(len(session.log) == 0 || session.log[len(session.log)-1].Type != sessionlog.EventSessionEndSeed)
	if needsMarker {
		if _, err := session.Append(sessionlog.Event{Type: sessionlog.EventSessionEndSeed}); err != nil {
			// 走不到：这条事件负载为空、不上表面、类型也不是请求头，
			// Append 里每一条失败路径都够不着它。照实转出去比在这里假设它不会
			// 失败诚实。
			return nil, err
		}
	}
	return session, nil
}

// ID 是这个会话的标识，取自它那份耐久头里唯一的一份。
//
// 源: packages/core/session/src/index.ts:444-446
func (s *Session) ID() sessionlog.SessionID { return s.header.ID }

// Header 是这个会话游离的创建元数据（格式版本、工作目录、血统、seed 边界）。
//
// 源: packages/core/session/src/index.ts:428-438
//
// 它由存储那一侧在 [Store.Create] 时给出；没有存储给头时这里合成一份最小的
// （盖上当下的 [github.com/snight1983/ds-harness-go/session.FormatVersion]），所以它**总是**在。
// 它不进事件日志：那是存储的事，不是可回放的对话状态。
//
// 造出来之后不再变，所以读它不必上锁。
func (s *Session) Header() sessionlog.SessionHeader { return s.header }

// FirstLiveSeq 是**本进程内**追加的第一个 seq，也就是构造 seed 的长度
// （没有 seed 就是 0）。
//
// 源: packages/core/session/src/index.ts:452-470
//
// 比它小的那些事件是从构造进来的——回放、分叉、或者续跑——从来没有在
// [Store.OnEvent] 那条广播上发布过（构造 seed 不发），所以那些「拿重放日志当
// 发布的替代品」的消费方（遥测的接管）该从这里起步。
//
// 它和 [github.com/snight1983/ds-harness-go/session.SessionHeader.SeedLength] 不是一回事：后者是
// **耐久**的分叉血统边界。一个续跑起来的会话，它的构造 seed 是自己完整的存储
// 日志，而它的头里留着当初分叉那个值——这个字段说的是进程内的构造事实。
//
// 它本身不持久化：一个带 seed 的会话把它投影成日志里那条 session/end-seed。
// 读**存储下来的**历史的消费方读的是那条事件。找**最后**一条，而不是「在
// 这个 seq 上的那条」——已经以标记结尾的 seed 不会被重新标记，所以重新打开
// 一个没动过的会话时，那条事件的 seq 比这个字段小。进程内优先用这个字段：
// 它在标记落盘之前就是准的。
func (s *Session) FirstLiveSeq() int { return s.firstLiveSeq }

// Seq 是下一条事件的序号：起点加上日志长度。
//
// 源: packages/core/session/src/index.ts:564-566
//
// 新增: 上游这里写的是 `this.log.length`，连注释都把「seq 等于日志长度」当成契约。
// 本仓库的日志会从最老的一头被弹掉一截（见 docs/session-log-limit.md），长度因此
// 不再等于下一个序号，这里改成从 [Session.BaseSeq] 起算。
func (s *Session) Seq() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.baseSeq + len(s.log)
}

// BaseSeq 是这份日志现存最早一条事件的序号；日志为空时等于 [Session.Seq]。
//
// 新增: 见 [Session.baseSeq]。拿 seq 去切 [Session.Events] 交出来那份切片的一方
// 必须先减掉它，而且减完要核 `events[index].Seq == seq`。
func (s *Session) BaseSeq() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.baseSeq
}

// Events 是这份只增不改日志的一份快照。
//
// 源: packages/core/session/src/index.ts:553-562
//
// 交回的切片自己是一份复制：之后的追加长不了一个调用方已经拿在手里的数组。
// 同一份快照在下一次追加之前会被重复交出，所以**把它当只读的**——里面那些
// 事件的 Data 字节是共享的。要一份自己拥有的就
// [github.com/snight1983/ds-harness-go/session.Event.Clone]。
//
// 新增: DSH 靠 deepFreeze 让「改不动」成为运行期事实。Go 里没有这回事，
// 这条契约和本仓库每一处 json.RawMessage 一样，是写在文档里的。
func (s *Session) Events() []sessionlog.Event {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.eventsLocked()
}

// eventsLocked 是 [Session.Events] 拿着锁的那一半。
func (s *Session) eventsLocked() []sessionlog.Event {
	if s.eventsSnapshot == nil {
		// slices.Clone 给出的容量恰好等于长度，于是任何一方往它上面 append
		// 都会重新分配，碰不到另一方拿着的那一份。
		s.eventsSnapshot = slices.Clone(s.log)
	}
	return s.eventsSnapshot
}

// SurfaceNodes 是当前表面上那些事件的 seq，按模型可见的顺序。
//
// 源: packages/core/session/src/index.ts:725（Session.surface）
//
// 新增: DSH 那边 `session.surface` 交出去的是折叠器本身，用一个只读接口
// SessionSurface 挡住写。Go 这边直接把它的两个读方法搬到会话上——交出折叠器
// 会连 Push 一起交出去，而那是本会话独占的写路径。
func (s *Session) SurfaceNodes() []int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.surface.Nodes()
}

// SurfaceReplaceGeneration 是已落定的位置替换次数，单调递增。
//
// 源: packages/core/session/src/index.ts:427-430
func (s *Session) SurfaceReplaceGeneration() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.surface.ReplaceGeneration()
}

// Append 往日志上追加一条事件，并同步通知那些观察者。
//
// 源: packages/core/session/src/index.ts:568-657
//
// candidate 上由调用方填的是 Type、Data、Ignorable、SurfaceOp、SourceEventSeqs
// 这几个字段；Seq 和 Time 由这里盖，所以它们**必须是零值**，填了就报错。
//
// 新增: DSH 的签名是 `append(type, data, ...opts)`，opts 那一项对表面事件是
// 必填、对别的类型被编译器拒收。Go 的类型系统做不到「按第一个参数的取值决定
// 第二个参数在不在」，而 [github.com/snight1983/ds-harness-go/session.Event] 上那两个字段本来就在，
// 所以这里收的就是一条填了一半的事件，见裁决表上 SurfaceIntent 那一行。
// 那条「表面事件必须带 SurfaceOp」的约束由
// [github.com/snight1983/ds-harness-go/session.SurfaceOpOf] 在下面验，DSH 在运行期也验同一条。
//
// 热路径不为 I/O 阻塞——持久化插件自己异步缓冲。事件一旦进了日志这次追加就
// 算提交了：观察者失败被逐个记日志兜住，既不改变返回值，也不妨碍后面的观察者
// 看到同一条已被接受的事件。
//
// 出错时日志一动不动。会出错的是：负载排不成 JSON、旧格式的请求头词汇、
// 表面契约不成立、以及在这道接受／发布边界还开着的时候重入。
func (s *Session) Append(candidate sessionlog.Event) (sessionlog.Event, error) {
	if candidate.Seq != 0 || candidate.Time != 0 {
		// 新增: DSH 那个签名根本给不出这两个字段。Go 里能给，而**默默盖掉**
		// 调用方填的值是最糟的那种处理——调用方以为自己指定了位置，日志里却
		// 是另一个数，两边都不报警。
		return sessionlog.Event{}, fmt.Errorf(
			"%w: 事件 %q 的 Seq 与 Time 由会话盖上，追加时必须留成零值",
			ErrInvalidAppend, candidate.Type,
		)
	}
	if len(candidate.Data) > 0 && !json.Valid(candidate.Data) {
		return sessionlog.Event{}, fmt.Errorf(
			"%w: session event %q carries non-JSON-serializable data",
			ErrInvalidAppend, candidate.Type,
		)
	}
	location := fmt.Sprintf("session event %q", candidate.Type)
	if err := validateSupportedRequestHeader(candidate.Type, candidate.Data, location, ErrInvalidAppend); err != nil {
		return sessionlog.Event{}, err
	}

	event, observers, attached, err := s.commit(candidate)
	if err != nil {
		return sessionlog.Event{}, err
	}
	if attached == nil {
		return event, nil
	}
	for _, observer := range observers {
		attached.store.callEventObserver(attached.id, observer, s, event)
	}
	s.finishPublishing()
	return event, nil
}

// commit 是 [Session.Append] 拿着锁的那一半：定序、验表面、进日志、收观察者。
//
// 源: packages/core/session/src/index.ts:625-647
//
// 新增: DSH 是先收监听器快照、再 push 进日志，因为它那次收集会经过 cordis 的
// 派发检查、是**会抛**的。Go 这边收观察者只是遍历
// [github.com/snight1983/ds-harness-go/core/scope.Layers] 的两层，不会失败，所以次序上没有可观察的
// 差别；这里把它放在表面提交之后，是为了不给一条注定要失败的追加白收一遍。
//
// 也因此这里用的是 Push 而不是「先 ValidateNext 再 Push」：Push 本身就是
// 「验得过才落」的原子操作，中间再插一次 ValidateNext 只会多出一条永远走不到
// 的错误分支。
func (s *Session) commit(candidate sessionlog.Event) (
	event sessionlog.Event,
	observers []EventObserver,
	attached *entry,
	err error,
) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.publishing {
		return sessionlog.Event{}, nil, nil, fmt.Errorf(
			"%w: session append cannot reenter while another append is being published",
			ErrInvalidAppend,
		)
	}

	event = candidate.Clone()
	event.Seq = s.baseSeq + len(s.log)
	event.Time = s.now()

	if _, _, err := s.surface.Push(event); err != nil {
		return sessionlog.Event{}, nil, nil, fmt.Errorf("%w: %w", ErrInvalidAppend, err)
	}
	s.log = append(s.log, event)
	s.eventsSnapshot = nil

	if s.entry != nil {
		observers = s.entry.store.eventObservers(s.entry.carrierKey)
		s.publishing = true
		attached = s.entry
	}
	return event, observers, attached, nil
}

// finishPublishing 关掉那扇发布窗口，并把发布期间提出的摘除请求补上。
//
// 源: packages/core/session/src/index.ts:650-656（append 的 finally 那一段）
func (s *Session) finishPublishing() {
	s.mutex.Lock()
	current := s.entry
	s.publishing = false
	s.mutex.Unlock()
	if current != nil {
		current.store.afterPublish(current)
	}
}

// isPublishing 给存储那一侧读这扇窗口开着没有。
//
// 锁的次序见 [Session] 的类型注释：存储的锁可以在这道调用外面拿着。
func (s *Session) isPublishing() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.publishing
}

// attach 把这个会话认到一份存储登记上，已经认过就返回假。
//
// 源: packages/core/session/src/index.ts:915-925（attachments.set 那一段）
//
// 调用方是 [Store.Enter]，它正拿着存储的锁——次序合规，见类型注释。
func (s *Session) attach(e *entry) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.entry != nil {
		return false
	}
	s.entry = e
	return true
}

// release 解开这份**确切的**登记。传进来的不是当前那一份就什么都不做——一个过期
// 的摘除能力不许把后来那一次同名生命周期的登记解掉。
func (s *Session) release(e *entry) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.entry == e {
		s.entry = nil
	}
}

// attachedEntry 给出这个会话当前认着的那份登记，游离的会话返回 nil。
//
// 它只拿会话自己的锁，并在返回前放掉：调用方 [Store.liveEntryFor] 拿到之后才去
// 拿存储的锁，两把锁不叠。
func (s *Session) attachedEntry() *entry {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.entry
}

// RequestHeader 是这段日志最后一条请求头事件之后生效的那份
// [github.com/snight1983/ds-harness-go/session.EpochHeader]——也就是**下一次**请求要拿去比对的那份。
//
// 源: packages/core/session/src/index.ts:664-687
//
// 第二个返回值为假表示还没有过任何一条 request/header 快照。
//
// 它是 [github.com/snight1983/ds-harness-go/session.FoldRequestHeader] 活着的、增量维护的那个形态：
// 每条头事件只折一次，于是每个步骤读一次的代价是 O(新事件)。
func (s *Session) RequestHeader() (sessionlog.EpochHeader, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.headerFoldSeq < len(s.log) {
		folded, ok, err := sessionlog.FoldRequestHeader(s.log[s.headerFoldSeq:], s.headerFold, s.headerFoldOK)
		if err != nil {
			return sessionlog.EpochHeader{}, false, err
		}
		s.headerFold, s.headerFoldOK = folded, ok
		s.headerFoldSeq = len(s.log)
	}
	return s.headerFold, s.headerFoldOK, nil
}

// RequestContext 是最近一次解析出来的路由元数据。
//
// 源: packages/core/session/src/index.ts:689-706
//
// 第二个返回值为假表示还没有过任何一条 request/context 事件。每条事件只折一次。
func (s *Session) RequestContext() (sessionlog.RequestContext, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.contextFoldSeq < len(s.log) {
		for _, event := range s.log[s.contextFoldSeq:] {
			if event.Type != sessionlog.EventRequestContext {
				continue
			}
			var data sessionlog.RequestContextData
			if err := json.Unmarshal(nonEmptyData(event.Data), &data); err != nil {
				return sessionlog.RequestContext{}, false, fmt.Errorf(
					"%w: request/context 事件（seq %d）的负载读不回来: %w",
					ErrInvalidAppend, event.Seq, err,
				)
			}
			s.contextFold, s.contextFoldOK = data.RequestContext, true
		}
		s.contextFoldSeq = len(s.log)
	}
	return s.contextFold, s.contextFoldOK, nil
}

// DeriveMessages 沿着表面维护的那串顺序节点派生出模型消息历史。
//
// 源: packages/core/session/src/index.ts:708-748
//
// 表面是派生历史的**唯一**来源：每一次会产出消息的追加都记下了自己的
// SurfaceOp，于是一条没带标记的原始事件（一个分块、一次回合边界）理所当然地
// 缺席，而一次压缩的 replace 会把被盖住的节点从派生里删掉。逐节点的投影规则
// 是 [github.com/snight1983/ds-harness-go/session.DeriveEventMessage]。
//
// 有缓存：每个表面节点只在第一次见到时投影一次，一次调用的代价是 O(新节点)；
// 一次表面重写（[Session.SurfaceReplaceGeneration] 变了）会整份重建。交回的
// 切片每次都是新的（后来的追加长不了调用方已经拿着的那一份），里面的消息是
// **共享**的，当只读的用。
func (s *Session) DeriveMessages() ([]llm.Message, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	nodes := s.surface.Nodes()
	generation := s.surface.ReplaceGeneration()
	if generation != s.derivedGeneration {
		s.derived = nil
		s.derivedNodes = 0
		s.derivedGeneration = generation
	}
	for _, seq := range nodes[s.derivedNodes:] {
		// 表面节点存的是事件 **seq**，不是日志下标——这两件事只在起点为 0 时
		// 才碰巧一样（`session/surface.go` 往节点里塞的是 plan.seq）。这份日志
		// 会从最老的一头被弹出事件，起点因此可能是 500，直接拿 seq 去切就越界。
		//
		// 减完还要校验（docs/session-log-limit.md 原则第 2 条）：表面是从这份
		// 日志逐条折出来的，每个节点都该指得回一条现存的事件，对不上就是这两份
		// 东西分了岔，如实报错。
		index := seq - s.baseSeq
		if index < 0 || index >= len(s.log) || s.log[index].Seq != seq {
			s.discardDerivedLocked()
			return nil, fmt.Errorf(
				"%w: 表面节点指着 seq %d，这份日志里没有它（起点 %d，共 %d 条）",
				ErrCorruptLog, seq, s.baseSeq, len(s.log),
			)
		}
		message, ok, err := sessionlog.DeriveEventMessage(s.log[index])
		if err != nil {
			s.discardDerivedLocked()
			return nil, err
		}
		// 一个表面节点必是那五种会产出消息的类型之一，但一条内容为空的
		// assistant/message（一个只装着用量的 max-tokens 步骤）派生不出消息，
		// 不能进历史。
		if ok {
			s.derived = append(s.derived, message)
		}
	}
	s.derivedNodes = len(nodes)
	return slices.Clone(s.derived), nil
}

// discardDerivedLocked 把派生缓存丢掉，让下一次调用从头折。
//
// 新增: 缓存是「已经折到第几个节点」加上折出来的那串消息，两者必须同进同退。
// 半路失败时如果只是把错误抛出去，已经追加进 s.derived 的那几条就留在了缓存里，
// 而 s.derivedNodes 没跟着走——下一次调用会从同一个节点重折一遍，把它们**再
// 追加一次**。丢掉整份缓存代价只是下一次多折一趟，而它是幂等的。
func (s *Session) discardDerivedLocked() {
	s.derived = nil
	s.derivedNodes = 0
}

// DeriveEventMessage 是 [github.com/snight1983/ds-harness-go/session.DeriveEventMessage] 挂在会话上的
// 那一面。
//
// 源: packages/core/session/src/index.ts:747-755（deriveEventMessage）
//
// 新增: DSH 那边这个方法的存在理由是「让拿着 session 的人不必再 import 一次
// surface.ts」。Go 里同一个理由成立得弱一些，但删掉它会让一个从
// [Store] 拿到会话的调用方多一次 import，而这个方法自己没有任何状态——
// 保留它是照抄，不是新增。
func (s *Session) DeriveEventMessage(event sessionlog.Event) (llm.Message, bool, error) {
	return sessionlog.DeriveEventMessage(event)
}
