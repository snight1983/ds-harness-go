// 本文件的作用：一个工具长什么样（名字、参数、输出契约、执行体、几个可选的呈现钩子），
// 一次调用长什么样，以及一次调用settle之后的那份结果长什么样。
//
// 源: packages/core/tools/src/index.ts:212-300, 469-600

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
)

// 这几个是错误的机器可读代号。它们会跟着失败结果一起走到调用方和会话日志里，
// 让重试、沙箱、重放各自认得出「这次失败是哪一类」。
//
// 源: packages/core/tools/src/index.ts:469-473, 494-524
// 源: packages/core/tools/src/schema.ts:461-468
const (
	// CodeUnknownTool 是「点名了一个它看不见的工具」。
	CodeUnknownTool = "UNKNOWN_TOOL"
	// CodeInvalidToolOutput 是「执行体交出来的值不符合它自己声明的输出契约」。
	CodeInvalidToolOutput = "INVALID_TOOL_OUTPUT"
	// CodeInvalidArgs 是「模型给的参数不符合这个工具声明的参数 schema」。
	CodeInvalidArgs = "INVALID_ARGS"
	// CodeAborted 是「执行体已经跑起来了，中途被取消」。
	CodeAborted = "ABORTED"
	// CodeAbortedBeforeDispatch 是「执行体还没跑就被取消了」。
	CodeAbortedBeforeDispatch = "ABORTED_BEFORE_DISPATCH"
)

// Coded 是一个带机器可读身份的错误。
//
// 新增: DSH 那边是 `HarnessError` 这个基类，靠 `instanceof` 认。Go 里没有基类，
// 分派靠接口——本包遇到的错误只要实现了这个接口，它的名字和代号就会被抄进
// [Failure.Info]，让下游不必解析错误文本就能分类。
type Coded interface {
	error
	// ErrorName 对应 DSH 的 Error.name。
	ErrorName() string
	// ErrorCode 是机器可读代号。
	ErrorCode() string
}

// ErrorName 交出 SchemaError 的名字。
func (err *SchemaError) ErrorName() string { return "JsonSchemaError" }

// ErrorCode 交出 SchemaError 的代号。
func (err *SchemaError) ErrorCode() string { return SchemaCode }

// ErrToolNotFound 表示点名的工具在这个作用域里不可见。
var ErrToolNotFound = errors.New("tools: 找不到这个工具")

// NotFoundError 是一次点名了看不见的工具的调用。
//
// 源: packages/core/tools/src/index.ts:481-503（ToolNotFoundError）
type NotFoundError struct {
	// ToolName 是调用方点的名字。
	ToolName string
	// ReachableFrom 是「这个名字其实是可见的，只是不能这么直接调」时，
	// 该走的那条路。名字在哪都没注册过时留空。
	ReachableFrom string
}

// Error 交出给模型看的那句话。
func (err *NotFoundError) Error() string {
	if err.ReachableFrom == "" {
		return fmt.Sprintf("unknown tool %q", err.ToolName)
	}
	return fmt.Sprintf("unknown tool %q: %s", err.ToolName, err.ReachableFrom)
}

// Unwrap 让 errors.Is 认出 [ErrToolNotFound]。
func (err *NotFoundError) Unwrap() error { return ErrToolNotFound }

// ErrorName 交出这个错误的名字。
func (err *NotFoundError) ErrorName() string { return "ToolNotFoundError" }

// ErrorCode 交出这个错误的代号。
func (err *NotFoundError) ErrorCode() string { return CodeUnknownTool }

// ErrInvalidToolOutput 表示执行体交出来的值不符合它声明的输出契约。
var ErrInvalidToolOutput = errors.New("tools: 工具交出来的值不符合它声明的输出")

// OutputError 是一次不合法的工具输出。
//
// 源: packages/core/tools/src/index.ts:505-515（ToolOutputError）
type OutputError struct {
	// ToolName 是这个工具的名字。
	ToolName string
	// Violations 是按校验顺序排的违规说明。
	Violations []string
}

// Error 交出给模型看的那句话。
func (err *OutputError) Error() string {
	return fmt.Sprintf("tool %q returned invalid output: %s", err.ToolName, strings.Join(err.Violations, "; "))
}

// Unwrap 让 errors.Is 认出 [ErrInvalidToolOutput]。
func (err *OutputError) Unwrap() error { return ErrInvalidToolOutput }

// ErrorName 交出这个错误的名字。
func (err *OutputError) ErrorName() string { return "ToolOutputError" }

// ErrorCode 交出这个错误的代号。
func (err *OutputError) ErrorCode() string { return CodeInvalidToolOutput }

// ErrInvalidArgs 表示模型给的参数不符合这个工具声明的参数 schema。
var ErrInvalidArgs = errors.New("tools: 参数不合法")

// ArgsError 是一次不合法的参数。
//
// 源: packages/core/tools/src/schema.ts:460-470（ToolArgsError）
type ArgsError struct {
	// Violations 是按校验顺序排的违规说明。
	Violations []string
}

// Error 交出给模型看的那句话。
func (err *ArgsError) Error() string {
	return "invalid arguments: " + strings.Join(err.Violations, "; ")
}

// Unwrap 让 errors.Is 认出 [ErrInvalidArgs]。
func (err *ArgsError) Unwrap() error { return ErrInvalidArgs }

// ErrorName 交出这个错误的名字。
func (err *ArgsError) ErrorName() string { return "ToolArgsError" }

// ErrorCode 交出这个错误的代号。
func (err *ArgsError) ErrorCode() string { return CodeInvalidArgs }

// ErrorInfo 是一次失败的结构化身份，和给模型看的那句话并排放着。
//
// 源: packages/core/tools/src/index.ts:467-471（ToolErrorInfo）
type ErrorInfo struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

// Failure 是一次失败的全部细节。
//
// 源: packages/core/tools/src/index.ts:473-479（ToolFailure）
type Failure struct {
	// Message 是给模型看的那句话。
	Message string `json:"message"`
	// Info 是结构化身份，抛出来的错误没实现 [Coded] 时是 nil。
	Info *ErrorInfo `json:"info,omitempty"`
}

// ExecutionToken 是一次调用在本进程内的相关性标识。
//
// 源: packages/core/tools/src/index.ts:299-300（ToolExecutionToken）
//
// 新增: DSH 用一个 `Symbol` 当这个标识，靠**对象身份**唯一。Go 里没有那种身份，
// 所以这是一个包级计数器发出来的值，可比较、可当 map 键、复制之后仍然是它自己。
// 零值表示「没有」——这样 [ExecutionInput.Parent] 不必再用一层指针。
type ExecutionToken struct{ id uint64 }

// IsZero 说明这是不是「没有 token」。
func (t ExecutionToken) IsZero() bool { return t.id == 0 }

// String 给出一个便于诊断的写法。
func (t ExecutionToken) String() string { return fmt.Sprintf("tool-exec-%d", t.id) }

// tokenCounter 从 1 开始发 token，0 留给「没有」。
var tokenCounter atomic.Uint64

// newExecutionToken 发一个新的 token。
//
// 源: packages/core/tools/src/index.ts:1866-1868
func newExecutionToken() ExecutionToken { return ExecutionToken{id: tokenCounter.Add(1)} }

// ExecutionInput 是调用方交上来的那份「要调哪个工具、参数是什么」。
//
// 源: packages/core/tools/src/index.ts:302-331（ToolExecutionInput）
//
// 新增: DSH 这里还有一个 `signal: AbortSignal`。Go 的取消是 context.Context，
// 而按 Go 的惯例它是**参数**不是字段，所以它跟着 [Runtime.Execute] 那一路传下去，
// 不在这个结构体里。DSH 那个 `fuseToolSignals`（把调用方的信号和外层包装换上的信号
// 合成一个）在 Go 这边整个消失：包装函数派生出来的 ctx 天然被两边任意一边取消，
// context 的父子关系本身就是那个「合成」。
type ExecutionInput struct {
	// CallID 是这次调用的标识，工具结果靠它和调用对上。
	CallID llm.CallID
	// RootCallID 是这棵调用树最外层那次模型请求的调用标识。
	// 根调用留空，本包会补成 CallID；嵌套派发的一方把外层的值原样传下来。
	RootCallID llm.CallID
	// Name 是要调的工具名。
	Name string
	// Arguments 是模型写出来的参数，已经解析过、是一段合法 JSON。
	Arguments json.RawMessage
	// Agent 是代表哪个 agent 在调，决定可见性、限制和守卫走哪条作用域链。
	Agent *scope.Key
	// Parent 是外层传输执行的 token，有值表示这是一次嵌套的子派发而不是模型直调。
	Parent ExecutionToken
}

// Execution 是加上了本包发的 token 之后的那次调用。
//
// 源: packages/core/tools/src/index.ts:365-377（ToolExecution）
type Execution struct {
	ExecutionInput
	// Token 是本包发的相关性标识，调用方选不了它。
	Token ExecutionToken
}

// RunContext 是执行体拿到的那个活的执行对象。
//
// 源: packages/core/tools/src/index.ts:389-414（ToolRunContext）
//
// 新增: DSH 把「这次执行推迟了哪些上下文」「它宣布回合结束了没有」「调用方的信号」
// 「快照下来的 finalizeContent」这四样分别放在四张 WeakMap 里，键是这个执行对象。
// 那是 JS 里给一个对象挂私有旁路状态的办法。Go 里它们就是这个结构体上的
// 未导出字段——同样外面看不见，但少四张表、也少了「表里查不到」这种走不到的分支。
type RunContext struct {
	Execution

	// deferred 是执行体推迟到最终结果时才交出去的上下文，按调用顺序。
	deferred []llm.Message
	// concludes 表示执行体宣布这个回合到此为止。
	concludes bool
	// finalizer 是调用刚开始时快照下来的那个内容收尾函数。
	finalizer func(exec Execution, result Result) llm.Content
	// bodyInvoked 表示执行体已经被调起来了——它决定取消时报哪一种中止。
	bodyInvoked bool
	// definition 是这次调用解析到的工具，取消或者找不到时是 nil。
	definition *Definition
	// canonicalValue 是本包最近一次为这次调用验过、并据以渲染出内容的那个值。
	//
	// 新增: DSH 拿一张 WeakMap 记「这份结果对象是不是我自己造的」，靠 JS 的对象身份。
	// Go 的结构体是值，一个绕派发的包装函数把结果复制一份再改个字段，复制出来的东西
	// 和原件在语言层面无法区分，那张表在这里根本立不住。所以本包改成比**值**：
	// 包装函数交回来的成功结果只要 Value 变了，就按输出契约重新验一遍、重新渲染一遍。
	// 见 [Runtime.normalizeDispatchResult]。
	canonicalValue json.RawMessage
}

// DeferContext 把一段上下文推迟到这次调用的最终结果送达 agent 循环时再交出去。
//
// 源: packages/core/tools/src/index.ts:410
//
// 典型的两种用法：一个复合工具把嵌套派发带回来的上下文捎给外层结果；
// 一个叶子工具现造一条插件来源的指令。每一条都保留自己的来源和元数据，
// 按调用顺序发出去。
func (c *RunContext) DeferContext(message llm.Message) {
	c.deferred = append(c.deferred, message)
}

// ConcludeTurn 把一次成功的最终结果标成「这个 agent 回合到此为止」。
//
// 源: packages/core/tools/src/index.ts:420
//
// 这个标记只挂在**成功**的结果上（见 [Result.ConcludesTurn]）。一个派发了嵌套调用的
// 复合工具要把它从嵌套结果转发上来，和 AdditionalContexts 的做法一样——所以只有一次
// 有权威的嵌套成功才结束得了外层这一轮。
func (c *RunContext) ConcludeTurn() { c.concludes = true }

// Result 是一次工具调用settle之后的那份结果。
//
// 源: packages/core/tools/src/index.ts:556-581
//
// 新增: DSH 是 ToolExecutionSuccess | ToolExecutionFailure 两个类型的可判别联合，
// 用 `isError: false | true` 加 `value?: never` / `error?: never` 互相排他。
// Go 这边是一个结构体加一个判别字段，理由和 llm.ToolResultBlock.IsError 一致：
// 联合在 Go 里要么是接口（那 Content 这类共有字段就得每个变体各写一遍），
// 要么就是这个形状。两条规矩由本包自己守住：
//
//   - IsError 为真时 Error 非 nil、Value 为 nil、ConcludesTurn 为假；
//   - IsError 为假时 Error 为 nil。
type Result struct {
	// IsError 表示这次调用失败了。
	IsError bool
	// Value 是成功时那份**权威的**值，已经按输出 schema 验过。
	//
	// 它是执行期局部的：会话日志里落的是 Content，不是它。
	Value json.RawMessage
	// Error 是失败时的细节。
	Error *Failure
	// Content 是给模型看的那份内容；失败时是渲染好的错误文本。
	Content llm.Content
	// Meta 是工具自己的呈现载荷，只在顶层调用上算。
	Meta json.RawMessage
	// AdditionalContexts 是要挂给下一次请求的上下文。
	AdditionalContexts []llm.Message
	// ConcludesTurn 表示 agent 循环提交完这一批成功结果之后就停。
	ConcludesTurn bool
}

// clone 复制这份结果里那几段可写的东西。
//
// 源: packages/core/tools/src/index.ts:1837-1853（materializeFinalResult）
//
// 新增: DSH 那边这一步是 snapshotJsonValue 加 deepFreeze——JS 的对象按引用共享，
// 一份发布出去的结果能被收到它的人改掉。Go 的结构体赋值就是复制，但里面的切片不是，
// 所以这里复制内容树和上下文列表。json.RawMessage 也是切片，同样复制。
func (r Result) clone() Result {
	cloned := r
	cloned.Content = r.Content.Clone()
	cloned.Value = cloneRaw(r.Value)
	cloned.Meta = cloneRaw(r.Meta)
	if r.Error != nil {
		failure := *r.Error
		if r.Error.Info != nil {
			info := *r.Error.Info
			failure.Info = &info
		}
		cloned.Error = &failure
	}
	if r.AdditionalContexts != nil {
		contexts := make([]llm.Message, len(r.AdditionalContexts))
		for index, message := range r.AdditionalContexts {
			contexts[index] = message.Clone()
		}
		cloned.AdditionalContexts = contexts
	}
	return cloned
}

// cloneRaw 复制一段原始 JSON；nil 仍然是 nil。
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	copied := make(json.RawMessage, len(raw))
	copy(copied, raw)
	return copied
}

// PresentedResult 是交给 [Definition.PresentResult] 的那份已完成结果。
//
// 源: packages/core/tools/src/index.ts:282-295（ToolResult）
//
// 新增: DSH 那边这个类型就叫 `ToolResult`，和 `ToolExecutionResult` 只差一个词。
// 这里改名叫 PresentedResult，因为它是**专门给呈现用的那一份投影**：只有能重放的
// 三样东西，没有值、没有上下文、没有回合标记。
type PresentedResult struct {
	// Content 是最终给模型看的内容（失败时是渲染好的错误文本）。
	Content llm.Content
	// IsError 表示这次调用失败了。
	IsError bool
	// Meta 是工具自己的呈现载荷；工具没声明投影、或者这次调用嵌在复合工具下面时为 nil。
	Meta json.RawMessage
}

// OutputDefinition 是一个工具对自己**返回值**的声明。
//
// 源: packages/core/tools/src/index.ts:203-211（ToolOutputDefinition）
//
// 这是必填的：执行体交出来的每一个值都按 Schema 验一遍，再由 Render 投影成
// 给模型看的内容。「工具直接返回内容块」这条路本包不提供——那样一来同一件事实
// 就有两种表示，重放和呈现各读一种。
type OutputDefinition struct {
	// Schema 是每个成功值都要满足的输出契约。
	Schema Node
	// Render 是从「验过的参数和值」到「给模型看的内容」的纯投影。
	Render func(args json.RawMessage, value json.RawMessage) (llm.Content, error)
	// PresentationMeta 是可重放的呈现投影，只在顶层调用上算，可以为 nil。
	PresentationMeta func(args json.RawMessage, value json.RawMessage) (json.RawMessage, error)
}

// Definition 是一个注册进来的工具。
//
// 源: packages/core/tools/src/index.ts:213-280（ToolDefinition）
type Definition struct {
	// Name 是工具名，模型按这个名字调它。
	Name string
	// Description 是给模型看的工具说明。
	Description string
	// Parameters 是参数的 schema，必须是对象根。
	Parameters Node
	// Output 是返回值的声明，必填。
	Output OutputDefinition

	// Execute 跑一次已经放行的调用，只交出它那份权威的 JSON 值。
	//
	// 异步的工作要观察或者转发 ctx：本包保证调用方的取消能传到这里，
	// 但它**杀不掉**同进程里的代码，只能等这次执行自己收敛。
	Execute func(ctx context.Context, args json.RawMessage, exec *RunContext) (json.RawMessage, error)

	// FinalizeContent 是给模型看的内容在落地之前最后一次改写的机会。
	//
	// 本包在调用**刚开始**时就把这个函数快照下来，然后对每一份规范化过的结果
	// 恰好调它一次——包括那些绕过了后置策略的管线失败。返回 nil 表示不改；
	// 结果里除了内容之外的每一个字段都仍然归本包所有。
	FinalizeContent func(exec Execution, result Result) llm.Content

	// Timeout 是这次调用的协作式超时预算，零表示不设。
	//
	// 它由 guard/timeoutpolicy 那一层（一个围绕派发的包装）执行，**永远不会**发给模型。
	// 声明它就是在断言这个工具会把 ctx 转发给一个取消时收得住的实现。
	Timeout time.Duration

	// IsConcurrencySafe 是「这次调用能不能和兄弟调用重叠」的纯同步判定。
	//
	// 只有明确返回 true 才算加入并行组；不声明、panic、返回 false 都是独占。
	// 这个信息永远不会被模型看到。
	IsConcurrencySafe func(args json.RawMessage) bool

	// PresentCall 是这次调用**进行中**在界面上的样子，返回 nil 表示用通用卡片。
	//
	// 必须是纯函数：界面会在实时流式和会话重放两种场合调它，所以它只能依赖 args。
	PresentCall func(args json.RawMessage) CallView

	// PresentResult 是这次调用**已完成**在界面上的样子，返回 nil 表示保持原样。
	//
	// 纯函数的理由和 PresentCall 一样。
	PresentResult func(args json.RawMessage, result PresentedResult) ResultView
}

// ExecutionModeKind 是一次待调用的调度方式。
//
// 源: packages/core/tools/src/index.ts:333-339（ToolExecutionMode）
type ExecutionModeKind string

const (
	// ModeParallel 表示这次调用可以和兄弟调用重叠。
	ModeParallel ExecutionModeKind = "parallel"
	// ModeExclusive 表示这次调用要独占，并且形成一道次序屏障。
	ModeExclusive ExecutionModeKind = "exclusive"
)
