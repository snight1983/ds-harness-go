// 本文件的作用：命令这一层的词汇——命令标识、发现用的描述符、处理器交出来的结果、
// 一次已结算的执行，以及那两条 command/* 生命周期事件的类型与负载。
//
// 源: packages/interaction/commands/src/types.ts
// 源: packages/interaction/commands/src/brand.ts

package commands

import (
	"github.com/snight1983/ds-harness-go/session"
)

// ID 把一次执行的 command/run 和它那条 command/done 配成一对。
//
// 源: packages/interaction/commands/src/brand.ts:20
//
// 它同时是那次准入应答上的相关号，所以一个界面能把 RPC 那一头的回执和日志里
// 长出来的那两条记录对上。由执行器现发，每个服务实例内单调。
//
// 新增: DSH 用 Branded<'CommandId'> 造名义类型，再用一个同名函数标牌子。Go 的定义
// 类型天生就是名义类型，ID(s) 这个转换本身就是那个函数。
type ID string

// InputDescriptor 是一条命令那点可选的自由输入的元数据。
//
// 源: packages/interaction/commands/src/types.ts:12-24（CommandInputDescriptor）
type InputDescriptor struct {
	// Hint 是用户还没输入时显示的占位提示。
	Hint string `json:"hint"`
	// Images 说明这条命令收不收编辑器带上来的图片附件。
	//
	// 假（或者没声明）表示执行器会把带图的调用当场判成错误结果，有能力的编辑器
	// 在派发之前就该拒掉这次提交。声明了的那条命令，它的处理器拿到已经准入的
	// 持久图块，之后的语法判断（包括拒掉用不了图的子命令）全归它自己。
	Images bool `json:"images,omitempty"`
}

// ResultKind 是一次结果的两种走向。
//
// 源: packages/interaction/commands/src/types.ts:27-34
type ResultKind string

const (
	// ResultSuccess 表示这条命令做成了。
	ResultSuccess ResultKind = "success"
	// ResultError 表示这条命令给出了一条预期之内的失败说明。
	//
	// 它和「处理器炸了」不是一回事：后者也落成 command/done 的 error，但会把
	// 那个错误继续抛给调用方。
	ResultError ResultKind = "error"
)

// Result 是派发这条命令的界面直接渲染的那个结局。
//
// 源: packages/interaction/commands/src/types.ts:26-34（CommandResult）
//
// 新增: DSH 是一个按 kind 分叉的联合类型，两支的字段约束不同（error 那支的 text
// 必填且非空）。Go 这边合成一个结构体，那条约束由 [normalizeResult] 在注册表边界上
// 判——因为它是**跨包**的约束（处理器是第三方代码），类型系统表达不了。
type Result struct {
	// Kind 是这次结果的走向。
	Kind ResultKind `json:"kind"`

	// Text 是给人看的那句话。
	//
	// [ResultError] 时必填且不能全是空白；[ResultSuccess] 时可以为空。
	//
	// 新增: DSH 的 success text 是 `text?: string`，一个显式的空串会原样落进
	// command/done。Go 这边空串和「没给」在介质上是同一件事（omitempty），因为
	// 一条 text 为空串的成功结果和一条没有 text 的成功结果对界面是同一个意思，
	// 而为了区分它们把这个字段做成 *string 会让每一个处理器都变难写。
	Text string `json:"text,omitempty"`

	// SourceEventSeq 指向更早那条自己拥有更丰富呈现的权威领域事件；nil 表示没有。
	//
	// 只有 [ResultSuccess] 能带它。指过去的那条事件必须真的在这条日志里、
	// 排在这条 command/done 前面、而且自己不是一条 command/* 生命周期事件——
	// 这三条由 [Trace] 盯着。
	SourceEventSeq *int `json:"sourceEventSeq,omitempty"`
}

// Execution 是一次已经结算的执行：处理器那个归一化过的结果，加上为它这一对
// command/run、command/done 现发的那个配对号。
//
// 源: packages/interaction/commands/src/types.ts:42-47
type Execution struct {
	// ID 是这次执行那两条生命周期事件带着的配对号。
	ID ID
	// Result 是处理器那个归一化过的结局。
	Result Result
}

// Descriptor 是交给界面的那份不带处理器的命令视图。
//
// 源: packages/interaction/commands/src/types.ts:49-57（CommandDescriptor）
type Descriptor struct {
	// Name 是不带斜杠的小写命令名。
	Name string `json:"name"`
	// Description 是发现界面上那句人读的摘要。
	Description string `json:"description"`
	// Input 是那点可选自由输入的元数据；nil 表示这条命令不收输入。
	Input *InputDescriptor `json:"input,omitempty"`
}

// SourceKind 说明这一行命令是谁发的。
//
// 源: packages/interaction/commands/src/types.ts:65-70
//
// 今天只有一个值：每一个调用执行器的调用方都是一个人面前的界面，派发的是人敲进去
// 的一行字。DSH 那边它是一个可合并扩展的和类型（对着 MessageSourceMap 的形状），
// Go 里换成一个开放的字符串类型——外面的包写得出新值，读的一方按已知值分流。
type SourceKind string

// SourceUser 表示这一行是人敲进去的。
const SourceUser SourceKind = "user"

// Source 是一次命令调用的来源记录，也就是 command/run 那个 source 槽。
//
// 源: packages/interaction/commands/src/types.ts:65-70
type Source struct {
	// Kind 是这条来源的种类。
	Kind SourceKind `json:"kind"`
}

// 两条 command/* 生命周期事件的类型。
//
// 源: packages/interaction/commands/src/types.ts:84-110
//
// 两条**都不上表面**：它们只进日志，模型的抄本里一条都看不见。
const (
	// EventRun 记下一条解析得出来的斜杠命令进了它的处理器。
	//
	// 它和后面必然跟着的那条 [EventDone] 靠 commandId 配对，形状对着
	// tool/call 和 tool/result 那一对。
	EventRun session.EventType = "command/run"
	// EventDone 记下配对的那条命令结算了。
	EventDone session.EventType = "command/done"
)

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: 理由和 [github.com/snight1983/ds-harness-go/compaction.EventTypes] 逐字相同——Go 没有声明合并，
// [session.Vocabulary] 是个闭合的值，所以由本包交出单子、装配方自己拼：
//
//	vocabulary := session.CoreVocabulary().With(commands.EventTypes()...)
func EventTypes() []session.EventType {
	return []session.EventType{EventRun, EventDone}
}

// RunData 是 [EventRun] 的负载。
//
// 源: packages/interaction/commands/src/types.ts:96
//
// 负载是**拆开的**：Name 和 Args 就是 [Parse] 自己那一刀（命令名，加上一字不改的
// 剩余输入，分隔空白也在内），所以下游（自己折命令记录的投影单元、一张富命令卡片）
// 再也不用把这一行重新解析一遍。
type RunData struct {
	// ID 是这次执行的配对号。
	ID ID `json:"commandId"`
	// Name 是不带斜杠的命令名。
	Name string `json:"name"`
	// Args 是命令名后面那段一字不改的输入。
	//
	// 定义里把 RecordInput 设成假时这个字段整个不出现——那种命令有一条自己的
	// 权威领域事件拥有这份负载，不该在日志里再抄一遍。
	Args string `json:"args,omitempty"`
	// Source 是这一行的来源。
	Source Source `json:"source"`
}

// DoneData 是 [EventDone] 的负载。
//
// 源: packages/interaction/commands/src/types.ts:103-108
//
// Kind 和 Text 是处理器那个一字不改的结局；一个炸了的、或者被取消的处理器也在这里
// 落成 kind=error，Text 是那条失败渲染出来的话。
type DoneData struct {
	// ID 是它回引的那条 [EventRun]。
	ID ID `json:"commandId"`
	// Kind 是这次结算的走向。
	Kind ResultKind `json:"kind"`
	// Text 是给人看的那句话，可以为空。
	Text string `json:"text,omitempty"`
	// SourceEventSeq 指向那条拥有更丰富呈现的权威事件；nil 表示没有。
	SourceEventSeq *int `json:"sourceEventSeq,omitempty"`
}

// Parsed 是一行语法上成立、但还没经过注册表解析的斜杠命令。
//
// 源: packages/interaction/commands/src/index.ts:72-77
type Parsed struct {
	// Name 是不带斜杠的小写命令名。
	Name string
	// RawInput 是命令名后面那段一字不改的文字。
	RawInput string
}
