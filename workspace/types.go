// 本文件的作用：本包对外的类型词汇——工作区 id、消费方看得见的那个工作区接口，
// 以及本包从别处借的两块窄能力。
//
// 源: packages/workspace/workspace/src/types.ts

package workspace

import (
	"context"
	"time"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/session"
)

// WorkspaceID 标识一条工作区记录。
//
// 源: packages/workspace/workspace/src/types.ts:15
//
// 它是现生成的 uuid，**永远不是路径**：路径的规范形会被重写（换一个 fs 后端、
// 目录被移走再挂回来），而一个被别处引用的锚点必须一直不动。
//
// DSH 那边是 `Branded<'WorkspaceId'>` 加一个恒等构造函数。Go 的具名 string 类型
// 天生是标称类型，构造就是 workspace.WorkspaceID(s) 这个语言自带的转换。
//
// 新增: 类型本体住在 session 包，这里是别名。会话头要带着它
// （[session.SessionHeader.WorkspaceID] 就是归属判据的那一侧），而 workspace
// 认识 session、session 不认识 workspace。别名而不是另开一个具名类型，
// 是因为两处说的必须是同一个东西——两个类型之间来回转换的地方，
// 迟早会有一处转反了而编译器不吭声。
type WorkspaceID = session.WorkspaceID

// Status 是一个工作区目录此刻可不可用。
//
// 源: packages/workspace/workspace/src/types.ts:103
//
// 新增: DSH 那边是 `'ok' | 'missing-dir'` 这个字符串字面量联合。Go 里对应物是
// 具名 string 类型加两个常量；取值原样保留，因为它会被上层原样渲染给人看。
type Status string

const (
	// StatusOK 表示 [Workspace.Path] 此刻存在，而且是一个目录。
	StatusOK Status = "ok"
	// StatusMissingDir 表示此刻拿不到那个目录——不存在、不是目录、或者后端答不上来。
	StatusMissingDir Status = "missing-dir"
)

// Workspace 是一个工作区：一个目录上的稳定 id、一个展示标题、一串有序的会话候选账目。
//
// 源: packages/workspace/workspace/src/types.ts:25-112（Workspace）
//
// 归属要同时满足「id 在账目里」和「会话头的工作目录解析到同一个目标」两件事，
// 见包文档。消费方只看见这个接口，实现是包私有的。
//
// 新增: DSH 那边是一个带 readonly 属性的 interface，属性访问就是取值。
// Go 的接口里放不了字段，所以那五个只读属性成了五个取值方法。做成接口而不是
// 导出一个结构体，为的是同一件事：这条记录**只能**通过下面那几个写方法改，
// 而那几个方法每一次都要落盘。一个导出的结构体挡不住调用方直接改字段——
// 改完内存里变了、介质上没变，而且没有任何一步会报错。
//
// 新增: 除 [Workspace.ID] 外的每一个取值方法都收 ctx、也都会失败。域的权威
// 搬回介质之后（见 storage/domain 的 domain.go 开头），一次取值就是一次真的
// 往返：它会超时、会撞上后端故障、也会发现这条记录已经被别的副本删掉了
// （[CodeWorkspaceGone]）。签名上不写出来，这三件事就只能被塞进零值里蒙混过去。
//
// 因此**这几个取值方法之间没有原子性**：连着读 [Workspace.Title] 和
// [Workspace.SessionIDs]，中间可以夹着另一个副本的一次写，读到的是两个时刻的值。
// 要一份自洽的多字段快照，眼下没有这条路——真需要的时候再加，而不是现在
// 先摆一个没人调的方法在这儿。
type Workspace interface {
	// ID 是这条记录稳定的 id。
	//
	// 新增: 只有它不收 ctx。它交出来的是这个句柄自己那把表键——建句柄的那一刻
	// 就定死了，不是记录上的字段，所以读它一个字节的介质都不碰。
	ID() WorkspaceID

	// TargetKey 是这个工作区目录的**身份**，也就是本包的唯一性范式。
	//
	// 新增: DSH 那边身份和展示是同一个字段（realpath 出来的那条路径）。
	// 本包把两者分开了，理由见包文档。这个值是不透明的，**不许解析、不许拼接**。
	TargetKey(ctx context.Context) (fs.TargetKey, error)

	// Path 是这个工作区目录给人看的那条路径。
	//
	// 源: packages/workspace/workspace/src/types.ts:32
	//
	// 它是建工作区那一刻 [fs.Target.DisplayPath] 的原样，之后**再也不重写**，
	// 哪怕目录已经不见了（见 [Workspace.Status]）。
	Path(ctx context.Context) (string, error)

	// Title 是展示标题。建的时候默认取 [Workspace.Path] 的最后一段；允许重名。
	//
	// 源: packages/workspace/workspace/src/types.ts:35
	Title(ctx context.Context) (string, error)

	// CreatedAt 是建这条记录的时刻，盖上之后永不重写。
	//
	// 源: packages/workspace/workspace/src/types.ts:38
	CreatedAt(ctx context.Context) (time.Time, error)

	// UpdatedAt 是最后一次落盘写入的时刻（建也算一次）。
	//
	// 源: packages/workspace/workspace/src/types.ts:41
	UpdatedAt(ctx context.Context) (time.Time, error)

	// SessionIDs 是过了会话头验证的会话，按人手排定的次序。
	//
	// 源: packages/workspace/workspace/src/types.ts:51
	//
	// 新挂上的会话排在最前，显式挪位走 [Workspace.InsertSessionBefore]，
	// **活动量永远不重排它**。落盘的候选账目在这里被筛一遍：会话头找不到、
	// 解析出来的目标和本工作区对不上的，一律不交出去。
	// 被筛掉的那些会在这个工作区下一次真实写入时被顺手裁掉。
	SessionIDs(ctx context.Context) ([]session.SessionID, error)

	// SetTitle 落盘地换一个展示标题。
	//
	// 源: packages/workspace/workspace/src/types.ts:58
	SetTitle(ctx context.Context, title string) error

	// AttachSession 把一个会话插到候选账目的最前面。
	//
	// 源: packages/workspace/workspace/src/types.ts:71
	//
	// 已经在账目里的 id 不写（除了每一次被接受的写入都要做的那次候选裁剪）。
	// 一个新 id 的会话头工作目录必须解析到一个存在的目录，且等于本工作区的
	// [Workspace.TargetKey]；id 不认识、工作目录缺失或解析不出来、对不上的，
	// 一律拒绝且不写。
	AttachSession(ctx context.Context, sessionID session.SessionID) error

	// InsertSessionBefore 在人手次序里挪动一个已在账目里的会话，语义同 DOM 的 insertBefore。
	//
	// 源: packages/workspace/workspace/src/types.ts:85
	//
	// 给了锚点就落在它前面，没给就挪到末尾。只有被挪的那个 id 换位置。
	// 会话或锚点不在账目里的，报 [CodeMoveInvalid] 且不写；挪到原地的不写。
	//
	// 新增: DSH 的 beforeSessionId 是可选参数。Go 没有可选参数，这里用**空串**
	// 表示「没给锚点，挪到末尾」。空串不是一个合法的会话 id，所以这个映射不丢信息，
	// 和 [session.SessionHeader.ParentSession] 用空串表示「不是分叉来的」是同一条约定。
	InsertSessionBefore(ctx context.Context, sessionID, beforeSessionID session.SessionID) error

	// DetachSession 把一个会话从账目里摘掉，幂等；**永不动**那个会话自己的日志。
	//
	// 源: packages/workspace/workspace/src/types.ts:95
	DetachSession(ctx context.Context, sessionID session.SessionID) error

	// Status 现查一次目录在不在，不走缓存。
	//
	// 源: packages/workspace/workspace/src/types.ts:103
	//
	// 目录不见了**绝不改动这条记录**——它可能只是被临时移走了。
	//
	// 查目录的三种失败（不存在、不是目录、文件系统后端自己出错）仍旧一律归到
	// [StatusMissingDir]，不走 error。这条照抄 DSH（entity.ts:183-187 写明了理由）：
	// 调用方问的是「此刻这个目录能不能用」，而这三种情况的答案都是「不能」。
	// 给它们一个 error 分支，只会让每一个调用点都写一遍同样的 err != nil → missing-dir。
	//
	// 新增: error 这一支是为**另一件事**留的——读不到这条工作区记录本身
	// （记录被别的副本删了、域后端出故障）。那不是「目录不见了」，把它折进
	// [StatusMissingDir] 会让一次数据库掉线在界面上显示成「你的目录没了」。
	Status(ctx context.Context) (Status, error)
}

// LiveSessions 是此刻在内存里被推进的那些会话。
//
// 源: packages/workspace/workspace/src/index.ts:264,593,616（`this.ctx.get('sessions')`）
//
// 新增: DSH 从 `ctx.sessions` 这个 cordis 服务上取活会话表，而且它是**可选**的
// （`ctx.get` 而不是 inject）。本仓库不用 cordis，活会话类型本身也还没移植
// （DESIGN 第八节第 6 块），所以这里只声明本包真正用得着的东西：会话头，
// 按 id 取一份或者全部列出来。挂空表示这次装配没有活会话表，那样本包就只看落地的那些。
//
// 新增: 方法叫 Header / Headers 而不是 Get / List，是为了不和
// [sessionquery.LiveSessions] 撞名。第 6 块的活会话表要同时满足两个包的这两个接口，
// 而那边的 Get 交出的是带事件的 LogicalSource，本包只要会话头——同名不同签名，
// 一个类型上写不下两份。取不同的名字，一个实现就能把两边都接上。
type LiveSessions interface {
	// Header 取一个活会话的会话头；第二个返回值为假表示这个 id 不活着。
	Header(id session.SessionID) (session.SessionHeader, bool)
	// Headers 列举此刻所有活会话的会话头。
	Headers() []session.SessionHeader
}

// Persistence 是本包用得着的那一小块持久化后端能力。
//
// 源: packages/workspace/workspace/src/index.ts:93（`static inject = [... 'sessionPersistence']`）
//
// 新增: DSH 用 `inject` 声明这是一个**必需**依赖，并且在类文档里写明了理由：
// 「an unavailable peer can never be mistaken for an empty history and commit
// the initialized marker」——拿不到它的时候，一次「历史是空的」的结论会被当真，
// 然后把 initialized 标记盖下去，从此那些会话再也不会被 bootstrap 收编。
// 所以 [Config.Persistence] 不能为 nil。
//
// 只声明 List 一个方法，是因为本包永远不该写会话：收一个只读接口，
// 就让「写不了」变成编译期事实。[persistence.Store] 结构上天然满足它。
type Persistence interface {
	// List 列举所有已落地会话的头。
	List(ctx context.Context) ([]session.SessionHeader, error)
}
