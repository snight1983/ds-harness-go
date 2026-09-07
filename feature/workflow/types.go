// 本文件的作用：这条接缝的词汇——一次运行的身份、它的结局，以及六条生命周期边
// 各自的负载。
//
// 源: packages/workflow/workflow/src/types.ts

package workflow

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// RunID 认一次工作流运行。
//
// 源: packages/workflow/workflow/src/types.ts:13
//
// 新增: DSH 那边它是一个 Branded<'WorkflowRunId'>，还配了一个把裸字符串打上标记的
// 工厂函数。Go 的具名类型自带这层区分，转换写 workflow.RunID(s) 就行，不需要那个
// 工厂。
type RunID string

// Phase 是脚本在 meta.phases 里声明的一个阶段。
//
// 源: packages/workflow/workflow/src/types.ts:28-37
//
// 它**只是进度词汇**：阶段在观察方和界面上把子 agent 分组，对执行结构一个字都
// 没说。脚本里那句 phase(title) 按 Title 精确匹配。
type Phase struct {
	// Title 是阶段标题，phase() 调用按它逐字匹配。
	Title string `json:"title"`
	// Detail 是这个阶段干什么的一句话，可以空着。
	Detail string `json:"detail,omitempty"`
	// Provider 是这个阶段**预期**会用的提供方，说明性质，可以空着。
	Provider string `json:"provider,omitempty"`
	// Model 是这个阶段**预期**会用的模型，说明性质，可以空着。
	Model string `json:"model,omitempty"`
}

// Meta 是脚本那份身份说明，和脚本正文分开，以纯 JSON 数据的形式一起交进来，
// 由引擎在正文跑起来之前验形状。
//
// 源: packages/workflow/workflow/src/types.ts:46-55
//
// Name 和 Description 是必填的，其余是可选注解。
type Meta struct {
	// Name 是短横线分隔的工作流名字，同时用于显示和持久化的键。
	Name string `json:"name"`
	// Description 是这个工作流干什么的一句话。
	Description string `json:"description"`
	// WhenToUse 是「什么时候该用它」的说明，列表里会显示，可以空着。
	WhenToUse string `json:"whenToUse,omitempty"`
	// Phases 是那些被 phase() 调用匹配的阶段声明，可以为 nil。
	Phases []Phase `json:"phases,omitempty"`
}

// StopReason 是一次运行为什么结清。
//
// 源: packages/workflow/workflow/src/types.ts:63
//
// **封闭**联合：这三个值归引擎所有，消费方可以穷尽它们。
type StopReason string

const (
	// StopCompleted 是「脚本跑到了它最后那句 return」。
	StopCompleted StopReason = "completed"
	// StopCancelled 是「这次运行被取消了」——调用方叫了 Cancel，或者 ctx 断了。
	StopCancelled StopReason = "cancelled"
	// StopError 是「脚本抛了、一条致命的工作流错误穿了出来、或者结果物化失败」。
	StopError StopReason = "error"
)

// Result 是一次活着的运行结清出来的结局。
//
// 源: packages/workflow/workflow/src/types.ts:72-87
type Result struct {
	// Value 是脚本那个已经物化好的返回值；脚本没有 return 时是 JSON 的 null。
	// 只有 StopReason 为 [StopCompleted] 时它才有意义。
	//
	// 新增: DSH 那边这个字段的类型是 unknown，附带一句「plain host-realm JSON
	// data」的约定——那是一句注释，编译器不管。Go 这边定成
	// [encoding/json.RawMessage]：值本来就是跨引擎边界物化过来的字节，用这个类型
	// 那句约定就由类型本身兑现，而不是靠消费方自觉。
	Value json.RawMessage
	// StopReason 是这次运行为什么结清。
	StopReason StopReason
	// Error 是那句失败描述，**当且仅当** StopReason 不是 [StopCompleted] 时非空。
	//
	// 一次非完成的结清由消费方映成一个 isError 的工具结果，而不是报告半截输出。
	Error string
	// AgentsStarted 是这次运行一辈子接受过多少次 agent() 调用。
	//
	// 优雅结清时它是脚本侧那个计数（还在排队等并发槽的也算在内）；走到终止那条路上
	// （宽限期用尽强行结清、worker 死掉）它降级成宿主观察到的计数——一个已经被终止的
	// 脚本里排着队的调用，那时是无从得知的。
	AgentsStarted int
}

// RunInfo 是一次运行的身份，六条边每一条都带着它。
//
// 源: packages/workflow/workflow/src/types.ts:90-95
//
// 它是**借出去的不可变数据**，绝不是那个活的运行句柄——观察方看得见一次运行是谁，
// 但取消不了它。
type RunInfo struct {
	// ID 是这次运行的 id。
	ID RunID
	// Meta 是这次运行那份已经验过的身份说明。
	Meta Meta
}

// AgentInfo 是一次 agent() 调用在一次运行里的身份，也就是 `workflow/agent-start`
// 那条边的负载。
//
// 源: packages/workflow/workflow/src/types.ts:98-107
type AgentInfo struct {
	// Seq 是这次 agent() 调用在本次运行里的序号，从 1 起。
	Seq int
	// Label 是显示用的标签（label 选项，没给就是提示词的一小段）。
	Label string
	// Phase 是这个子 agent 属于哪个阶段（phase 选项，没给就是当下 phase() 的标题），
	// 可以空着。
	Phase string
	// ChildID 是这个孩子在子 agent 接缝上的 id。
	ChildID sessionlog.SessionID
}

// AgentOutcome 是一次 agent() 调用怎么结清的。
//
// 源: packages/workflow/workflow/src/types.ts:110
type AgentOutcome string

const (
	// AgentCompleted 是「孩子干净地交回了结果」。
	AgentCompleted AgentOutcome = "completed"
	// AgentFailed 是「孩子失败了」——脚本那一侧看到的是 null，整次运行不因此结束。
	AgentFailed AgentOutcome = "failed"
	// AgentCancelled 是「整次运行被取消，这个调用跟着散了」。
	AgentCancelled AgentOutcome = "cancelled"
)

// AgentEndInfo 是一次 agent() 调用的结清，也就是 `workflow/agent-end` 那条边的负载。
//
// 源: packages/workflow/workflow/src/types.ts:113-116
type AgentEndInfo struct {
	// AgentInfo 是这次调用的身份，逐字等于配对那条 `workflow/agent-start` 上的那份。
	AgentInfo
	// Outcome 是这次调用怎么结清的。
	Outcome AgentOutcome
}

// ResultInfo 是一次已结清运行的结局**作为事件数据**的样子，也就是 `workflow/end`
// 那条边的负载。
//
// 源: packages/workflow/workflow/src/types.ts:124-131
//
// 它是 [Result] 去掉 Value：一个只观察结局的监听器不该拿到调用方那个结果值的别名。
// 要值的消费方自己攥着运行句柄去等 [Run.Result]。
type ResultInfo struct {
	// StopReason 是这次运行为什么结清。
	StopReason StopReason
	// Error 是那句失败描述，当且仅当 StopReason 不是 [StopCompleted] 时非空。
	Error string
	// AgentsStarted 是这次运行接受过多少次 agent() 调用，含义同
	// [Result.AgentsStarted]。
	AgentsStarted int
}
