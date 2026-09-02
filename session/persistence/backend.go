// 本文件的作用：编排层和一个具体介质之间那道缝——一个后端必须提供的原始能力，
// 以及三样可选能力怎么问出来。
//
// 源: packages/session/session-persistence/src/coordinator.ts:112-214

package persistence

import (
	"context"
	"time"

	"github.com/snight1983/ds-harness-go/session"
)

// 编排层的三个默认策略值。
//
// 源: packages/session/session-persistence/src/coordinator.ts:26-32
const (
	// DefaultPreparedSessionCacheSize 是一个编排器留着备用的、已经准备好但还没
	// 发布的会话的条数上限。
	//
	// 留着它们是因为「看一眼」和「接着干」经常是同一个人前后脚做的两件事，
	// 第二次不必再读一遍盘、再验一遍。
	DefaultPreparedSessionCacheSize = 5

	// DefaultWriteBatchMaxDelay 是一个空闲的写队列收到活儿之后，最多有意等多久
	// 才开始往下写。
	//
	// 新增: DSH 是 DEFAULT_WRITE_BATCH_MAX_DELAY_MS = 200 那个毫秒数，
	// Go 这边直接是 [time.Duration]——[WriteBehind] 收的就是 Duration，
	// 单位不必再靠名字里的 Ms 来记。
	DefaultWriteBatchMaxDelay = 200 * time.Millisecond

	// DefaultMaxStoredEvents 是一份存档最多留几条事件，超了就从**最老**那头丢。
	//
	// 新增: 上游没有这一条——它的日志只追加、永不删除，而那正是本仓库推翻掉的
	// 前提（见 docs/session-log-limit.md 的决定第 1、13 条）。取 1000 是因为
	// 全量事件的用途是审计而不是恢复：拼给模型的那一串由压缩自己管窗口
	// （compaction/basic/config.go:17-22），够不够长不取决于这个数。
	DefaultMaxStoredEvents = 1000
)

// Backend 是编排层和一个具体介质之间的存储契约：编排要用到的那一小组
// 持久化原语。后端把这些实现在文件、行、对象存储……上；别的一切
// （攒批、串行化、游标、认领活会话、崩溃修复的次序、销毁时的静默）都由编排层提供。
//
// 源: packages/session/session-persistence/src/coordinator.ts:118-219（PersistenceBackend）
//
// 一个第三方后端也可以绕过这道缝，直接实现 [Store]。
//
// 新增: 每个方法第一个参数是 ctx，取代 DSH 的可选 AbortSignal。
type Backend interface {
	// Name 是这个后端的名字，出现在销毁失败的汇总错误里。
	Name() string

	// LoadStored 按 id 读出一份物理前缀，扫遍这个后端所有的存储范围。
	//
	// 存储里没有这个身份时返回 [ErrSessionNotFound]——那是正常控制流，
	// 建会话前的撞号探测就靠它。
	//
	// 交出来的头和事件图必须是**新的、互不别名、后端自己也不再持有**的：
	// 准备那一步会就地冻结并发布它们。
	//
	// 返回的 Revision 必须标识恰好这些值，而且和 [Backend.ReadStoredRevision]
	// 用同一套表示。TornMarker 非 nil 当且仅当后面挂着一截要截掉的坏尾巴。
	//
	// BaseSeq 必须填：它是这份存档现存最早一条事件的 seq，存档为空时是下一条要写的
	// seq。不弹出事件的后端填 0，会弹的后端填它当前的起点。见
	// [StoredPrefix.BaseSeq]。
	LoadStored(ctx context.Context, id session.SessionID) (StoredPrefix, error)

	// ReadStoredRevision 只读出一个会话当前的变更令牌，不读它的事件日志。
	//
	// 身份不存在时返回 [ErrSessionNotFound]。
	ReadStoredRevision(ctx context.Context, id session.SessionID) (Revision, error)

	// AppendBatch 把一批 seq **连续**的事件持久化下去，materialized 为假时
	// 先把这个会话落地。
	//
	// 落地那一下和第一批事件必须**原子**提交：崩在两者中间不许留下一个
	// 「落地了但一条事件都没有」的会话——那样的存档在 [LoadStored] 眼里
	// 是一个合法的空会话，而它其实是半截。
	//
	// 返回时这一批必须已经真的落盘。
	AppendBatch(
		ctx context.Context,
		meta session.SessionHeader,
		events []session.Event,
		materialized bool,
	) error

	// CommitRepair 把一次崩溃修复落盘：截掉坏尾巴（torn 非 nil 时），
	// 追加收尾事件（closers 非空时）。
	//
	// **不要求**原子：一个文件后端完全可以先截断再追加，各自 fsync 一次。
	// 装载（截断 + 补收尾）和认领活会话（只截断、closers 为空）都走这里。
	CommitRepair(
		ctx context.Context,
		meta session.SessionHeader,
		torn any,
		closers []session.Event,
	) error

	// List 列出所有已落地会话的元数据。
	List(ctx context.Context) ([]session.SessionHeader, error)
}

// SeekableBackend 是一个介质本身能按 seq 寻址的后端。
//
// 源: packages/session/session-persistence/src/coordinator.ts:146-172
//
// 实现了它，[Store.ReadFrom] 的开销就只跟后缀的长度走；没实现的顺序介质
// （比如 JSONL）由编排层退回 [Backend.LoadStored] 加一次前向跳过。
// 这条原语约束的是**返回并重新折叠**多少，不是每个后端物理上读多少。
type SeekableBackend interface {
	Backend

	// LoadStoredFrom 读出头，加上 seq 不小于 fromSeq 的那些存档事件，不读整份日志。
	//
	// 这是**不改动**存储的读：不截断、不补收尾。对严格小于 fromSeq 那一段的
	// 校验只到 seq 连续为止——这条读的契约范围就是后缀。
	//
	// 词汇的拒绝也按同样的范围：只检查交出来的这段后缀。顺序介质那条退路
	// 会解整份存档、并且在任何位置遇到一条不认识的必需事件都拒——
	// 顺序那侧的过度拒绝是接受的，代价比把寻址那侧的读放大要小。
	//
	// fromSeq 由调用方保证非负。身份不存在时返回 [ErrSessionNotFound]。
	//
	// BaseSeq 必须填，而且填的是**整份存档**的起点，不是这一截后缀的起点：
	// 读的一方要靠它分清「请求的水位早就被弹掉了」和「那一段压根没写过」。
	// 见 [StoredSuffix.BaseSeq]。
	LoadStoredFrom(ctx context.Context, id session.SessionID, fromSeq int) (StoredSuffix, error)
}

// LocatingBackend 是一个逐会话各有一份独立存档的后端。
//
// 源: packages/session/session-persistence/src/coordinator.ts:200-206
//
// 它只用来把拒绝诊断（[FormatUnsupportedError]）指到那份原始日志上，
// 所以必须是**无副作用**的：不读、不建、不落地。
// 把所有会话装进一个数据库的后端不实现它。
type LocatingBackend interface {
	Backend

	// Locate 给出这份头对应的存档在哪；第二个返回值为假表示这个会话没有独立存档。
	Locate(meta session.SessionHeader) (Location, bool)
}

// ClosableBackend 是一个有东西要收的后端，比如一个数据库句柄。
//
// 源: packages/session/session-persistence/src/coordinator.ts:208-213
//
// 编排层在静默排空**之后**才调它。一个无状态的文件后端不实现它。
type ClosableBackend interface {
	Backend

	// Close 收掉这个后端占着的东西。
	Close(ctx context.Context) error
}

// TrimmingBackend 是一个能从最老那头丢弃事件的后端。
//
// 新增: 上游没有这道缝，它的日志只追加、永不删除。本仓库按条数封顶、先进先出
// （docs/session-log-limit.md 的决定第 1、3、4 条），而决定第 3 条明说这件事
// 「单独一个接口承担，不揉进现有写入路径」——所以它是一道**独立的可选缝**，
// 不是 [Backend.AppendBatch] 的一个副作用。写进 AppendBatch 的话，一个只想
// 追加的调用方就没法不丢东西，而「丢多少」是编排层的策略、不是介质的。
//
// 实现不了的后端（比如顺序文件）整条不实现，于是它的存档永远从 0 起。读的一侧
// 本来就不许假设起点是 0（原则第 1 条），所以两种后端在读那边没有分别。
type TrimmingBackend interface {
	Backend

	// TrimBefore 丢掉一个会话里 seq **严格小于** beforeSeq 的那些事件。
	//
	// 丢的必须是连续的一头：本包分「被弹掉了」和「日志真坏了」靠的就是
	// 「现存的是一整段」这条（决定第 15 条），从中间挖走一条会让这两种再也分不开。
	//
	// 它是幂等的：那一段已经不在了就什么也不做，不是错。beforeSeq 越过存档末尾
	// 会把它整个清空，这也**不是**错——那时候起点由「下一条要写的 seq」回答，
	// 见 [StoredPrefix.BaseSeq]。
	//
	// 身份不存在时返回 [ErrSessionNotFound]。beforeSeq 由调用方保证非负。
	TrimBefore(ctx context.Context, id session.SessionID, beforeSeq int) error
}

// Seekable 问一个后端能不能按 seq 寻址。
//
// 新增: DSH 那边这三样是接口上的可选成员（`loadStoredFrom?`），调用点写
// `this.backend.loadStoredFrom?.(...)`。Go 没有可选方法，所以按本仓库
// 既定的办法（见 storage 包的 KV）：主接口 + 一个更宽的接口 + 一个断言函数。
//
// 这里**不**另设 nil 判断，和 storage 的 KV 不同：那边判的是「满足了接口但
// 把形态返回成 nil」，是实现方真能做出来的事；这里断言的目标是接口本身，
// 一次成功的断言必带一个动态类型，判出来的 nil 永远不会成立。至于一个
// (*T)(nil) 装进来——它连 [Backend.Name] 都调不动，问题在装配那一侧，
// 在这里悄悄降级成「不能寻址」只会把它盖住。
func Seekable(backend Backend) (SeekableBackend, bool) {
	seekable, ok := backend.(SeekableBackend)
	return seekable, ok
}

// Locating 问一个后端有没有逐会话的独立存档。
func Locating(backend Backend) (LocatingBackend, bool) {
	locating, ok := backend.(LocatingBackend)
	return locating, ok
}

// Closable 问一个后端有没有东西要收。
func Closable(backend Backend) (ClosableBackend, bool) {
	closable, ok := backend.(ClosableBackend)
	return closable, ok
}

// Trimming 问一个后端能不能从最老那头丢事件。
func Trimming(backend Backend) (TrimmingBackend, bool) {
	trimming, ok := backend.(TrimmingBackend)
	return trimming, ok
}

// LocateWith 用一个后端（如果它认路）定位一份头对应的存档。
//
// 新增: 「有位置就带上、没有就算了」这个动作在造 [FormatUnsupportedError]
// 的地方反复出现，写成一个函数省得每处各写一遍 Locating 加两个分支。
func LocateWith(backend Backend, meta session.SessionHeader) (Location, bool) {
	locating, ok := Locating(backend)
	if !ok {
		return Location{}, false
	}
	return locating.Locate(meta)
}

// 这四行钉住「更宽的接口真的更宽」：哪天有人从 [Backend] 上删掉一个方法、
// 或者把四个可选接口之一改得不再包含它，这里当场编译不过。
var (
	_ Backend = SeekableBackend(nil)
	_ Backend = LocatingBackend(nil)
	_ Backend = ClosableBackend(nil)
	_ Backend = TrimmingBackend(nil)
)
