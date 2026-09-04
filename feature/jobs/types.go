// 本文件的作用：作业生产方、注册表和控制器三边共用的那些类型——id、状态、种类、
// 生产方交出来的那组钩子，以及交给监听器和工具看的那份只读投影。
//
// 源: packages/jobs/jobs/src/types.ts, packages/jobs/jobs/src/brand.ts

package jobs

import (
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// JobID 认一件后台作业。注册表发的是 `<种类>-N`；id 是可预测的，所以边界靠属主
// 授权，不靠保密。
//
// 源: packages/jobs/jobs/src/brand.ts:19,26-28
//
// 新增: DSH 是 `Branded<'JobId'>` 加一个 `JobId(id)` 打标函数，因为 TS 的
// `string` 之间可以随便互赋。Go 的具名字符串类型天生就不和别的具名类型互赋，
// 那个打标函数因此就是一次显式转换 `JobID(raw)`，不必单独写一个。
type JobID string

// RunnerID 认一个**执行副本**：起了这件作业、并且握着它那份执行资源的那个进程。
//
// 新增: DSH 没有这个概念——它整台注册表就是一个进程里的一张 map，作业只可能跑在
// 「这里」。本仓库要多副本部署，账本是共享的而执行资源不是：一件作业的记录每个
// 副本都读得到，但 [Hooks.Cancel] 和 [Hooks.ReadOutput] 只在起它的那个副本手里。
// 这个字段就是那句「它在谁那儿」，[Registry.Read] 和 [Registry.Kill] 靠它把
// 「我这儿办不了」和「没有这件作业」分开说。
//
// 值由装配方给（部署里那个副本标识），本包不解释它，只要求非空。
type RunnerID string

// JobStatus 是一件作业的生命周期状态：running、可能经过 stopping，然后**恰好**
// 落到一个终态。生产方自己那些事实归 [Snapshot.Detail]。
//
// 源: packages/jobs/jobs/src/types.ts:17
type JobStatus string

const (
	// StatusRunning 表示活儿还在跑。
	StatusRunning JobStatus = "running"
	// StatusStopping 表示已经请求过取消，还没落到终态。
	StatusStopping JobStatus = "stopping"
	// StatusCompleted 是「跑完了」这个终态。
	StatusCompleted JobStatus = "completed"
	// StatusKilled 是「被取消了」这个终态。
	StatusKilled JobStatus = "killed"
	// StatusFailed 是「坏掉了」这个终态。
	StatusFailed JobStatus = "failed"
)

// IsTerminal 说这个状态是不是三个终态之一。
//
// 源: packages/jobs/jobs/src/invariant.ts:9
func (s JobStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusKilled || s == StatusFailed
}

// JobKind 是生产方自己定的作业种类，同时也是 id 前缀。
//
// 源: packages/jobs/jobs/src/types.ts:23-29
//
// 新增: DSH 是一个 `JobKindMap` 接口加声明合并，插件靠合并往里加种类，`JobKind`
// 是它值的联合。Go 没有声明合并，这里就是一个**开放的**具名字符串类型：底下那
// 两个常量是本仓库内建的两种，别的包直接写自己的字符串常量即可。注册表本来就把
// 每个值当成一个不透明的 id 命名空间，所以这个开放性和 DSH 那份声明合并等价。
type JobKind string

const (
	// KindBash 是 shell 命令那一类。
	//
	// 源: packages/jobs/jobs/src/types.ts:24
	KindBash JobKind = "bash"
	// KindSubagent 是委派给子 agent 那一类。
	//
	// 源: packages/jobs/jobs/src/types.ts:25
	KindSubagent JobKind = "subagent"
)

// Outcome 是生产方经 [Hooks.Done] 交出来的那份终局结果。
//
// 源: packages/jobs/jobs/src/types.ts:31-39（JobOutcome）
type Outcome struct {
	// Status 是这件活儿怎么结束的：跑完了、被取消了、还是坏掉了。
	//
	// DSH 那边这个字段的类型只有那三个终态；Go 里它就是 [JobStatus]，
	// 由实现方拿 [JobStatus.IsTerminal] 挡住一个非终态的结局。
	Status JobStatus
	// Detail 是那一类自己的状态细节，渲染进状态行（`exit code: 3`、`max-tokens`）。
	// 空串表示生产方没给。
	Detail string
	// Output 是没有 [Hooks.ReadOutput] 那类作业的最终输出；流式的那类不填。
	Output string
}

// Start 是交给 [Registry.Start] 的那份生产方声明。运行时先把访问、校验和属主清理
// 都过一遍，再去调 [Start.Run]；生产方拥有执行资源，运行时拥有身份和生命周期状态。
//
// 源: packages/jobs/jobs/src/types.ts:46-69
type Start struct {
	// Kind 是生产方那一类，同时也是 id 前缀（`bash`、`subagent`……）。
	Kind JobKind
	// Label 是给模型看的一行标签（那条命令；那份委派说明）。
	Label string
	// OutputLimitBytes 是每一份完整的完成通知或者输出读取的 UTF-8 字节上限，
	// 含控制器加上去的状态元信息。0 表示不设上限。
	OutputLimitBytes int
	// Owner 是那个活着的属主 agent。访问由它的会话 id 围起来，它被释放时这件
	// 作业会被取消并等到位。交进来的这个实例必须**就是**当下登记在那个 agent id
	// 底下的那一个。不给属主就是一件无主作业，直到服务释放为止对谁都开放。
	Owner agent.Agent
	// Run 在预检通过之后把活儿起起来，并且**同步**交回它的钩子。只会被调一次；
	// 它出错的话什么都不会被登记，而生产方必须自己把已经起了一半的资源收干净。
	//
	// 新增: DSH 是 `run(): JobHooks`，起不来就 throw。Go 里那个 throw 就是这个
	// error。
	Run func() (Hooks, error)
}

// Hooks 是运行时用来控制和观察生产方那份活儿的几只手。
//
// 源: packages/jobs/jobs/src/types.ts:71-91（JobHooks）
type Hooks struct {
	// Cancel 请求终止。必须是同步的、幂等的，并且最终让 [Hooks.Done] 落地；
	// 它自己出错要原样往上抛。reason 空串表示没给理由，非空则原样转给生产方。
	//
	// 新增: DSH 是 `cancel(reason?: string): void`，出错就 throw。Go 里那个
	// throw 就是这个 error——[Registry.Kill] 那条契约说「生产方出错要原样传上去
	// 且不改作业状态」，所以它必须是一个返回值而不是一次 panic。
	Cancel func(reason string) error
	// Done 在生产方**把资源放掉之后**送出那一份结局，不是活儿跑完就送。
	// 每件作业只送一个值。
	//
	// 新增: DSH 是 `done: Promise<JobOutcome>`，而且规定它「不许 reject，
	// 运行时把一次 reject 转成 failed」。Go 的 channel 没有 reject 这回事，
	// 对应物是**关掉而不送值**：实现方收到 `outcome, ok := <-Done` 里的
	// ok 为 false 时，按 DSH 那条把它判成 [StatusFailed]。
	Done <-chan Outcome
	// ReadOutput 取走上一次调用之后新产生的输出。截断和溢出提示由生产方自己
	// 排版。为 nil 表示这是一件只有最终输出的作业；每件作业只有一个消费游标。
	ReadOutput func() string
}

// Snapshot 是一件作业的只读投影，交给监听器和工具是安全的——每次调用都是一个
// 新对象，绝不是注册表里那份活状态。
//
// 源: packages/jobs/jobs/src/types.ts:97-128
type Snapshot struct {
	// ID 是注册表发的那个 id（`<种类>-N`）。
	ID JobID
	// Kind 是登记时那个生产方种类。
	Kind JobKind
	// Runner 是起这件作业的那个执行副本，见 [RunnerID]。
	//
	// 新增: 单进程那台实现（github.com/snight1983/ds-harness-go/adapter/localjobs）
	// 也要填一个非空值——它只有一个副本，但「这件作业在谁那儿」这个问题在那里
	// 一样成立，留空会让消费方以为这个字段可有可无。
	Runner RunnerID
	// Label 是生产方给的那行标签。
	Label string
	// OutputLimitBytes 是生产方定的那个上限，0 表示不设。
	OutputLimitBytes int
	// OwnerSession 是拿来授权和关联的属主会话 id，无主作业时是空串。
	// 完成监听器另外经 [DoneListener] 拿到那个确切的 [github.com/snight1983/ds-harness-go/harness/agent.Agent]。
	OwnerSession sessionlog.SessionID
	// Status 是当下的生命周期状态。
	Status JobStatus
	// Detail 是那一类自己的状态细节，生产方给过之后才有（通常是终态那一刻）。
	Detail string
	// StartedAt 是这件作业被登记的时刻。
	//
	// 新增: DSH 是 epoch 毫秒整数。Go 里时刻就是 [time.Time]，「缺席」由
	// [time.Time.IsZero] 表示，不需要一个额外的可选包装。要发到线上去的地方
	// （比如那件面向模型的作业工具）再折回毫秒整数。
	StartedAt time.Time
	// FinishedAt 是这件作业落定的时刻；running/stopping 期间是零值。
	FinishedAt time.Time
	// Reported 表示一次 kill、read、wait 或者拆除取消已经汇报过、或者已经答应
	// 要汇报那个终态了。完成汇报方看见它就不再发多余的通知。
	//
	// 拆除那条路也认领它：属主或者服务正在被销毁，已经没有读者了——一个「收到
	// 通知就开一个回合」的汇报方否则会为每一层拆除各花掉一次模型请求。
	Reported bool
}

// Read 是 [Registry.Read] 交回的输出和读后状态。
//
// 源: packages/jobs/jobs/src/types.ts:130-140（JobRead）
type Read struct {
	// Text 对流式那类是上次读之后的增量；对只有最终输出那类，活着时是空串，
	// 落定之后是那份终局输出（或者空串）——幂等，永远不会被消费掉。
	Text string
	// Snapshot 是读这一刻的状态。
	Snapshot Snapshot
}

// DoneListener 是完成回调，带着开工时交进来的那个确切属主；无主作业时 owner 是 nil。
//
// 源: packages/jobs/jobs/src/types.ts:146-149
//
// 新增: DSH 那边它可以返回一个 promise，运行时观察但不等它。Go 里「不等」就是
// 实现方自己起一个协程或者干脆同步调，签名上不必留返回值。
type DoneListener func(snapshot Snapshot, owner agent.Agent)

// ChangedListener 观察「某一个属主那份 [Registry.List] 结果」发生了变化。
//
// 源: packages/jobs/jobs/src/types.ts:160
//
// 它按属主而不是按作业发，一来变化可能是一次**移除**（那是任何按作业的记录都
// 表达不了的），二来它的消费方本来就要把整份可见集合重读一遍。
//
// owner 为 nil 表示变的是一件无主作业，于是每一个调用方的可见集合都跟着变了。
type ChangedListener func(owner agent.Agent)
