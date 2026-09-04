// 本文件的作用：这条接缝面向调用方的那几份契约——请求、结果、能力表，
// 以及插件和宿主观察得到的 `subagent/start` 与 `subagent/end` 负载。
//
// 源: packages/subagent/subagent/src/types.ts
//
// 内部的那些控制接口不放这里，各自跟着实现走（生命周期观察者在 lifecycle.go，
// 续接宿主在 continuation.go），好让本文件是**发布出去的那张面**，
// 而不是一袋子长得像类型的东西。

package subagent

import (
	"context"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// RunID 认一次被接受的子 agent 运行，跨它那一对生命周期事件。
//
// 源: packages/subagent/subagent/src/types.ts:20-29
//
// 新增: DSH 用 Branded<'SubagentRunId'> 加一个同名的铸造函数，把品牌类型在
// 运行期抹平成字符串。Go 的具名类型本来就不和 string 互换，所以那个铸造函数
// 就是一次转换，没有单独存在的必要（成例见 [github.com/snight1983/ds-harness-go/sessionlog.SessionID]）。
type RunID string

// RunInfo 是一次已发布运行的只读身份细节，由 `subagent/start` 带着。
//
// 源: packages/subagent/subagent/src/types.ts:36-50
//
// 一次性的运行和可续孩子的活化轮次共用这份负载，于是观察方看到的是同一套词。
type RunInfo struct {
	// RunID 是和那条配对的终止事件共享的身份。
	RunID RunID `json:"runId"`
	// Provider 是这个孩子最初被建出来时记下的提供方名字。
	//
	// 一次被接受的运行变就绪、或者一次持久化的活化冷恢复时，它可能是空的——
	// 这两条生命周期都不依赖那个提供方还登记着。
	Provider string `json:"provider,omitempty"`
	// ID 是这个孩子 agent 的 id。
	ID sessionlog.SessionID `json:"id"`
	// Local 记的是 start 兑现的那一刻 [Run.LocalAgent] 在不在。
	Local bool `json:"local"`
}

// RunEndInfo 是一次已结清运行的只读结局细节，由 `subagent/end` 带着，
// 靠 RunID 和一条 [RunInfo] 配对。
//
// 源: packages/subagent/subagent/src/types.ts:56-73
type RunEndInfo struct {
	// RunID 是和那条配对的开始事件共享的身份。
	RunID RunID `json:"runId"`
	// Provider 是那条配对的开始事件带的同一个提供方名字。
	Provider string `json:"provider,omitempty"`
	// ID 是这个孩子 agent 的 id。
	ID sessionlog.SessionID `json:"id"`
	// Local 记的是 start 兑现的那一刻 [Run.LocalAgent] 在不在。
	Local bool `json:"local"`
	// StopReason 是那个终止原因。
	StopReason StopReason `json:"stopReason"`
	// LastAssistantMessage 是这个孩子最后那段助手输出，选法和 [Result.Output]
	// 同一条规矩；基础设施层面被拒、或者孩子什么都没产出时它是 nil。
	LastAssistantMessage llm.Content `json:"lastAssistantMessage,omitempty"`
}

// Capabilities 是一个提供方支持哪些**开工期**特性。
//
// 源: packages/subagent/subagent/src/types.ts:86-91
//
// 服务在把活交给 [Provider.Start] 之前查这张表：一个请求要的能力选中的提供方
// 没有，就当场用带码的错误拒掉，而不是收下然后悄悄不做（「大声失败，不许静默降级」）。
//
// 这几个开关说的是**一次性**那条 [Provider.Start] 路——那条路上孩子由提供方组装；
// 可续的孩子由续接管理器自己组装，闸门换成 [Provider] 有没有实现
// [ContinuablePreparer]。每个开关和 [StartRequest] 上的一个选项一一对应：
// DepthLimit 对 MaxDepth，其余同名。
type Capabilities struct {
	// OutputSchema 表示这个提供方管得住「最终结果必须是结构化的」。
	OutputSchema bool
	// DepthLimit 表示这个提供方管得住 MaxDepth。
	DepthLimit bool
	// ToolFilter 表示这个提供方管得住孩子的工具范围。
	ToolFilter bool
	// Persona 表示这个提供方管得住给孩子单独换一套人设。
	Persona bool
}

// StartRequest 是调用方要起一个**一次性**子 agent 时说的话。
//
// 源: packages/subagent/subagent/src/types.ts:100-149
//
// 工具层拿模型给的 `{description, prompt}` 加它自己那份配置拼出它；服务对着
// 点名的提供方验一遍 [Capabilities]、解算出那份持久的描述符，再派给
// [Provider.Start]。
type StartRequest struct {
	// Label 是给一个有会话的一次性孩子持久下来的短展示名；空串表示不给。
	Label string
	// Prompt 是作为孩子那条用户消息交下去的内容。
	Prompt llm.Content
	// Parent 是发起派发的那个 agent。进程内的提供方从它持久的会话状态推出
	// 工作目录、血统和派发深度。
	Parent agent.Agent
	// AgentOptions 是给孩子的 agent 选项；零值表示不指定。
	AgentOptions agent.Options
	// OutputSchema 是一份以对象为根、落在 [github.com/snight1983/ds-harness-go/tools.AssertObjectSchema]
	// 允许的子集里的 JSON Schema。schema 不受支持、或者提供方没有这个能力时，
	// 开工当场被拒。孩子跑成了的话，那个匹配的值成为 [Result.Structured]。
	// nil 表示不要结构化输出。
	OutputSchema *tools.Node
	// MaxDepth 是给这个正在起的孩子的派发深度绝对上限：它算出来的深度必须
	// 小于等于这个非负整数。要 [Capabilities.DepthLimit]，否则开工被拒。
	//
	// 新增: DSH 是 `maxDepth?: number`，「不设上限」和「上限是 0」靠字段在不在
	// 区分，而这两件事完全不同（后者禁止一切派发）。Go 的 int 没有「不在」，
	// 所以用指针。
	MaxDepth *int
	// ToolFilter 是可选的孩子工具范围。要 [Capabilities.ToolFilter]，否则开工被拒。
	// 进程内的后端把它当成孩子创建窗口里一次带作用域的 tools.Restrict：点名的
	// 那些工具从孩子的提示词里消失**并且**拒绝执行（一种可见性），认不出的名字
	// 大声失败。零值表示不过滤。
	ToolFilter tools.Restriction
	// Persona 是可选的、只给这个孩子的人设。要 [Capabilities.Persona]，否则开工被拒。
	// 进程内的后端把它登记成孩子身上一段带作用域的 `deployment:persona`，
	// **盖掉**部署自己那份人设，且只对这一个孩子生效。空串表示不换。
	Persona string
}

// ResolvedStartRequest 是服务解算出持久的孩子描述符之后，交给提供方的那份一次性请求。
//
// 源: packages/subagent/subagent/src/types.ts:159-166（ResolvedSubagentStartRequest）
type ResolvedStartRequest struct {
	StartRequest
	// Descriptor 是一个有会话的提供方要写进孩子日志里的那份脱离的描述符。
	Descriptor DescriptorData
}

// ContinuableCreateRequest 是续接管理器在物化一个可续孩子的**第一次**活化时，
// 向提供方要的东西。
//
// 源: packages/subagent/subagent/src/types.ts:168-185（ContinuableCreateRequest）
//
// 管理器已经把那个持久的孩子身份占下来了、也拥有此后每一个操作，所以这份请求
// 只带着「一个全新的孩子」和「一个带着父历史种子的孩子」之间的那点差别。
type ContinuableCreateRequest struct {
	// SessionID 是占下来的那个持久孩子会话 id，供提供方做诊断。
	SessionID sessionlog.SessionID
	// Parent 是那个发起派发的父 agent，做种子的提供方读它的历史。
	Parent agent.Agent
}

// ContinuableCreateSpec 是提供方对一个可续孩子的创建交出去的那份脱离的贡献。
//
// 源: packages/subagent/subagent/src/types.ts:187-200（ContinuableCreateSpec）
//
// 它是**数据**，绝不是能力：不带 Agent、不带句柄、不带提示词投递、不带结果、
// 不带处置、不带恢复操作——预备之后这个孩子的整条生命周期归续接管理器。
type ContinuableCreateSpec struct {
	// Seed 是要拿来给孩子会话做种的、父日志上那段已完成回合的前缀；
	// nil 表示一个全新的孩子。持久契约和 [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions]
	// 的 Seed 一样：从 seq 0 起连续、无损 JSON、成对闭合。
	Seed []sessionlog.Event
}

// StopReason 说的是一次子 agent 运行为什么结束。
//
// 源: packages/subagent/subagent/src/types.ts:221-222（SubagentStopReason）
//
// 新增: DSH 用一张可合并扩展的 SubagentStopReasonMap，好让某个后端加自己的变体，
// 消费方 switch 剩下 default 接住。Go 没有声明合并，所以它就是一个具名字符串
// 类型：本包给出下面这五个常量，后端要加自己的直接造它自己的取值，消费方的
// switch 照样有 default 接住——那正是那张 map 要的效果，只是不需要那张 map。
type StopReason string

const (
	// StopCompleted 是孩子正常跑完了它那个回合。
	StopCompleted StopReason = "completed"
	// StopAborted 是被请求信号或者处置取消掉了。
	StopAborted StopReason = "aborted"
	// StopError 是模型或者传输失败。
	StopError StopReason = "error"
	// StopMaxTokens 是孩子还没做完就撞上了 token 天花板。
	StopMaxTokens StopReason = "max-tokens"
	// StopRefusal 是孩子拒绝了这件事。
	StopRefusal StopReason = "refusal"
)

// Result 是一次子 agent 运行的终止结局，由 [Run.Result] 解算出来。
//
// 源: packages/subagent/subagent/src/types.ts:224-253（SubagentResult）
type Result struct {
	// Output 是孩子最后那段助手输出：它最后一条**非空**助手消息的内容。
	// 内容为空的消息（包括只带用量的那种）跳过。一条非空消息都没有时，
	// 它是攒下来的助手文本流；两样都没有就是 nil。
	Output llm.Content
	// Structured 是一份被要求的 OutputSchema 真的被满足之后的结构化结果。
	// 要了 schema 不保证它在：孩子失败、或者跑完了却没留下一份合法的捕获时，
	// 提供方可以用 StopError 收场。这个值由提供方对着请求里那份 schema 验过；
	// 这里是 any，因为这条接缝本身不认 schema。nil 表示没有。
	Structured any
	// Diagnostic 是提供方为一个非 StopCompleted 的结果写的、非助手来源的失败细节。
	// 提供方保证这段文字里没有工具入参、文件内容、环境值、凭据和原始协议负载，
	// 并且不超过 4096 个 UTF-8 字节。消费方把它和 Output 分开展示。
	Diagnostic string
	// StopReason 是它为什么结束。非 StopCompleted 意味着 Output 可能是残的。
	StopReason StopReason
}

// Run 是发布之后交出来的**一次性**孩子句柄。
//
// 源: packages/subagent/subagent/src/types.ts:256-282
//
// 提示词投递、回合工作、以及那道边界之后的基础设施故障，全归 [Run.Result]。
// 消费方等那个结果，并且**必须**调 [Run.Dispose] 去取消剩下的活、让它静下来。
// 一次运行就是一次可处置的前台派发，带一个结果；可续的对话没有运行——
// 续接管理器直接拿着它那个 agent 句柄，每一个回合都从孩子自己的收件箱走。
type Run interface {
	// ID 是父作用域里的运行 id。对一个本地运行，它**必须**等于发布出来的那个
	// 孩子会话 id（那个会话的 ParentSession 记着请求里父 agent 的会话 id）；
	// 一个远端提供方铸一个在父命名空间里唯一的 id。
	ID() sessionlog.SessionID
	// LocalAgent 是那个发布出来的、确切的进程内孩子；远端运行是 nil。
	// 它在的时候，它的 id 就是 [Run.ID]；提供方不因此多出任何超过
	// [Run.Dispose] 这份普通契约的所有权含义。
	LocalAgent() agent.Agent
	// Result 等这次运行结清，交出孩子那份终止结果。
	//
	// 孩子层面的失败**不**从这里报错——一次模型或传输失败交回的是一个
	// StopReason 为 StopError 的结果，好让消费方把它映成一个 isError 的工具结果。
	// 只有这条接缝表达不成停止原因的基础设施故障才从 error 出去。
	//
	// 新增: DSH 是一个 `Promise<SubagentResult>` 字段，反复 await 拿的是同一份
	// 已决的值。Go 这边是一个收 ctx 的方法：Promise 一旦造出来就在跑，
	// 而 Go 需要一个地方接住调用方的取消。多次调用交回同一份结果。
	Result(ctx context.Context) (Result, error)
	// Dispose 取消剩下的活、等孩子静下来、放掉资源。可以重复调。
	Dispose(ctx context.Context) error
}

// Provider 是一条登记好的、跑孩子 agent 的传输。
//
// 源: packages/subagent/subagent/src/types.ts:292-331
//
// 提供方是同进程里受信的实现；调用方把描述符和交回来的值当成借来的不可变数据。
// 服务可以为不同的孩子并发地调同一个提供方。提供方自己隔离每次操作的可变状态；
// 一个共享的容量控制器可以让某次操作等一等，但绝不许把它的结清或者清理
// 和一个兄弟绑在一起。
type Provider interface {
	// Name 是注册表里的唯一名字（比如 spawn、fork、acp）。
	Name() string
	// Capabilities 是这个提供方支持的那些开工期特性。
	Capabilities() Capabilities
	// InheritsParentContext 说的是孩子看不看得到父那段已完成回合的前缀。
	//
	// 它是**描述性**的，不是服务会去验的开工能力：面向模型的那件工具拿它
	// 推出一句如实的措辞。它对工具注册、注入的服务、权限继承一个字都没说。
	InheritsParentContext() bool
	// Start 立起一个**一次性**孩子，发布之后交回它的句柄。
	//
	// 服务已经验过每一样被要求的开工期能力都支持、也解算好了 request.Descriptor，
	// 所以一个有会话的实现在孩子的第一个回合里把那份描述符追加进去。
	// 兑现之前，装配和「清掉一切还没发布的半成品资源」都归提供方，清干净了再报错。
	// 兑现的那一刻所有权转移；此后的回合失败或者基础设施失败从交回的那个 Run 结清。
	// 不同的 Start 可以重叠；每一次的取消、失败、结果结清和处置都各自独立。
	Start(ctx context.Context, request ResolvedStartRequest) (Run, error)
}

// ContinuablePreparer 是**可选**的可续创建能力：交出这个提供方的可续孩子
// 区别于别人的那点脱离的创建输入——也就只有「这个孩子会话要不要用父历史做种」。
//
// 源: packages/subagent/subagent/src/types.ts:330
//
// 新增: DSH 把它写成 SubagentProvider 上的一个可选方法，「方法在不在」**就是**
// 那个能力。Go 的接口没有可选方法，所以它单独成一个接口，服务用一次类型断言问：
//
//	preparer, ok := provider.(ContinuablePreparer)
//
// 这是 Go 表达可选能力的成例（见 [github.com/snight1983/ds-harness-go/llm.ModelLister] 那一组），
// 语义和 DSH 完全一样：没实现的提供方被可续开工拒掉，而它照样服务得了
// 普通的一次性派发。
type ContinuablePreparer interface {
	// PrepareContinuable 是这个提供方对一个可续孩子的**唯一**参与。
	//
	// 身份占位、组装、agent 创建、提示词投递、冷恢复、所有权和处置全归续接管理器，
	// 所以提供方永远看不到孩子的 Agent、句柄、回合和拆解。
	// 不同的预备可以重叠；每一次跟着自己那个 ctx 走，交回的数据只属于
	// request.SessionID 那一个。
	PrepareContinuable(ctx context.Context, request ContinuableCreateRequest) (ContinuableCreateSpec, error)
}
