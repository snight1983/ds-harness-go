// 本文件的作用：使用方看到的那道服务面——一份会话日志怎么建、怎么追加、
// 怎么读回来。
//
// 源: packages/session/session-persistence/src/index.ts:77-233

package persistence

import (
	"context"

	"github.com/snight1983/ds-harness-go/session"
)

// Store 是持久的、只追加的会话存储。
//
// 源: packages/session/session-persistence/src/index.ts:99-283（SessionPersistence）
//
// 实现方保管 seq 连续、能无损排成 JSON 的事件：[Store.Append] 只在真的落盘
// 之后才返回，[Store.Load] 会把一条中途断掉的尾巴补平而**不重写**任何
// 已经提交的事件。
//
// 新增: DSH 那边是一个 cordis 的 Service 抽象类（`ctx.sessionPersistence`），
// 里面 readRaw 和 prepare 两个方法带默认实现、其余是抽象方法。Go 这边是一个
// 纯接口：本仓库不用 cordis，服务由装配方构造好显式传下去。
// 两个带默认实现的方法各自的去处见下面各自的说明。
type Store interface {
	// Locate 给出这个后端为某个会话准备的那份独立存档在哪，不读、不建、
	// 不刷、也不落地它。
	//
	// 第二个返回值为假表示这个后端（比如把所有会话装进一个数据库的那种）
	// 没有逐会话的存档。
	Locate(meta session.SessionHeader) (Location, bool)

	// SupportsRawArtifacts 说明这个后端提不提供逐会话、逐字节原样的存档。
	//
	// 返回真的后端必须让 [Store.ReadRaw] 真的能用。
	SupportsRawArtifacts() bool

	// ReadRaw 逐字原样读出一个会话的后端自有存档文本——后端当初写下的那些字节
	// （已从它的物理编码解出来，比如解压过的 JSONL）。
	//
	// 交出来的是原始文本，不是从解出来的事件重新拼的，所以它保住了后端自己的
	// 序列化选择。调用方先问 [Store.SupportsRawArtifacts]；问过了，
	// [ErrSessionNotFound] 就只表示这个会话没有落地的存档。
	//
	// 这个后端根本不提供原始存档时返回 [ErrRawArtifactsUnsupported]。
	ReadRaw(ctx context.Context, id session.SessionID) (RawArtifact, error)

	// Create 登记一个新会话的元数据。
	//
	// 后端**可以**把物理写推迟到第一次 [Store.Append]（懒落地），那样一个
	// 建了但从没追加过的会话不会出现在 [Store.List] 里——半途放弃的会话
	// 什么都不留下。
	Create(ctx context.Context, meta session.SessionHeader) error

	// Append 持久化一批事件。
	//
	// 守只追加和 seq 连续两条契约：第一条事件的 seq **必须**等于存档里的
	// 下一个 seq（在 [Store.Load] 已经把中途断掉的回合落盘关掉之后）。
	// 事件负载排不成 JSON 时报错，并且要在错误里点出是哪个事件类型。
	Append(ctx context.Context, id session.SessionID, events []session.Event) error

	// Load 读出一份不可变的、已补平的逻辑视图，并把该做的冷恢复落盘。
	//
	// 一个**完整**的、中途断掉的末尾回合会被留下，并且落盘补上缺的工具错误结果、
	// 以及开着的步骤和回合边界；只有**写坏的**那一条末尾记录会被丢掉。
	// 版本不认识、或者已提交前缀损坏，都拒。
	//
	// 实现方**不许**对一个还绑着活会话的身份做崩溃修复：一份平衡的活日志
	// 可以作为持久快照返回，一个开着的活回合则拒。
	//
	// 返回的值可能和活着的、或者已经准备好的不可变状态共享，不许改。
	Load(ctx context.Context, id session.SessionID) (Inspection, error)

	// Inspect 看一个不可变的逻辑会话，但**不**落盘恢复、也**不**发布它。
	//
	// 一个冷的、完整中断的回合只在内存里收到合成的收尾事件，写坏的物理尾巴
	// 原地不动。一个已经活着的会话则给出它当前那份不可变快照，那份快照里
	// 可能有一个开着的回合和它的 session/end-seed 边界。
	//
	// 调用方只借到不可变的头和日志。
	Inspect(ctx context.Context, id session.SessionID) (Inspection, error)

	// ReadFrom 读出 fromSeq 起的那些存档事件——给那些从水位续读的读模型
	// （比如一份只折叠检查点之后那截尾巴的投影缓存）用的原语。
	//
	// 和 [Store.Inspect] 不同，它是一次**脱离**的物理后缀读：没有准备缓存、
	// 不截断坏尾巴、不补合成收尾、也不发布任何编排状态。只有合法连续前缀里的
	// 事件会被返回，所以一段写坏的碎片永远到不了调用方手上。
	//
	// fromSeq 落在存档前缀之外时返回**空事件列表**，不是错误。
	// fromSeq 为负是调用方的错，返回 [ErrMalformedSeq]。
	ReadFrom(ctx context.Context, id session.SessionID, fromSeq int) (StoredSuffix, error)

	// List 从元数据出发轻量列举，不解整份日志。
	List(ctx context.Context) ([]session.SessionHeader, error)

	// ListSnapshots 列举已落地的会话，各带一个便宜的变更令牌。
	//
	// 同一份没变过的日志被反复观察，给出的令牌相同；一次成功的、改动了存储的
	// [Store.Load] 修复会让下次列出来的令牌变掉。令牌还要能区分各自独立的存储，
	// 见 [Revision]。
	ListSnapshots(ctx context.Context) ([]Snapshot, error)
}

// 这个接口里没有 Prepare。
//
// 源: packages/session/session-persistence/src/index.ts:141-159
//
// DSH 的 SessionPersistence.prepare 返回的是一个**未发布的活会话**
// （SessionPreparation），它要先取出 SessionStore、再让它按 seed 造一个
// Session 出来。这件事本包做了，但落在 [Coordinator.Prepare] 上而不是这个
// 接口上：它要一个活的会话存储，而这个接口是照着**后端**去实现的，
// 一个只想把日志写进 SQLite 的人不该被迫先立起一整套会话存储。
//
// 这个位置的取舍在 DSH 那边也是一样的：prepare 是一个**带默认实现**的方法，
// 实现方不必写它，它只是 Load 之上的一层薄壳。
