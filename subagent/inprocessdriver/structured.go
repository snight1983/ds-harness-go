// 本文件的作用：给一个进程内的一次性孩子挂上那件带作用域的「结构化输出」工具、
// 那句提示词指令、一道终局守卫，以及权威的结果捕获。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:1-11
//
// 每个孩子把它自己那份真 schema 注册在**它自己**的作用域上，所以并发的多次运行
// 互不干扰，处置之后也不会在全局留下残渣。那句提示词贡献就是普通的、重建得出来的
// 请求状态。
//
// 捕获在那条权威的工具结果成功**之后**才提交；嵌套派发的捕获还要多等外层那次调用
// 的结果。终局的结果标记加上那道单调的工具守卫，挡住后来的调用把一次已经跑完的
// 结构化运行重新打开。

package inprocessdriver

import (
	"context"
	"encoding/json"
	"sync"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// StructuredOutputTool 是一个要结构化输出的孩子必须调来收尾的那个工具名。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:19
const StructuredOutputTool = "structured_output"

// StructuredOutputInstruction 是登记成孩子那段收尾（order 190，也就是工具指引
// 那一带的末尾）带作用域提示词段落的正文：这个要求跟着工具一起走，是恰好一个
// agent 的普通提示词状态。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:26-29
const StructuredOutputInstruction = "When you have your final answer, you MUST report it by calling the `" +
	StructuredOutputTool + "` tool with arguments matching its parameter schema exactly. " +
	"Do not finish with a plain text answer: only the tool call counts as your result."

// structuredOutputDescription 是这件工具给模型看的说明。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:66-68
const structuredOutputDescription = "Report your final structured result. " +
	"Call this exactly once, when your answer is complete; " +
	"the arguments must match this tool's parameter schema exactly."

// structuredOutputRecorded 是这件工具那份收下之后给模型看的内容。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:83
const structuredOutputRecorded = "Structured output recorded."

// structuredPromptSectionOrder 是那句指令排的位置：工具指引带（100–199）的末尾。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:101
const structuredPromptSectionOrder = 190

// StructuredServices 是挂一次结构化捕获要用到的那两样服务。
//
// 新增: DSH 从孩子那个 cordis 上下文上直接取 `childCtx.tools` 和
// `childCtx.systemPrompt`。Go 没有那个容器，所以做成一个显式的结构体（成例见
// ds-harness-go/subagent/subagent.ChildCompositionServices）。
type StructuredServices struct {
	// Tools 是工具运行时：捕获工具、那道守卫、结果观察者都登记在它上面。必填。
	Tools *tools.Runtime
	// SystemPrompt 是系统提示词注册表，那句指令必须挂得上去。必填。
	SystemPrompt *systemprompt.Registry
}

// stagedCapture 是一次已经验过、还没提交的捕获。
type stagedCapture struct {
	// parent 是外层那次传输执行的 token；零值表示这是一次模型直调。
	parent tools.ExecutionToken
	// value 是那次调用的参数，也就是要提交的那个值。
	value json.RawMessage
}

// StructuredAttachment 是一次结构化运行的活句柄：孩子结清之后从这里读那份捕获。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:32-39
//
// 新增: DSH 那三样状态（staged / pending / captured）是 attach 函数闭包里的三个
// 局部变量，句柄只是一个读 captured 的对象。Go 里守卫和结果观察者都是要被别的
// goroutine 碰到的回调，所以状态收进这个结构体，由它自己那把锁看着。
type StructuredAttachment struct {
	mutex sync.Mutex
	// staged 是捕获工具体已经收下、正等**它自己那条**权威工具结果的那些值。
	//
	// 键是本包发的执行 token：适配器给的 call id 会在多个步骤之间重复，而另一次
	// 执行永远够不到这里的条目。那条最终通知无论成败都会把自己那条 staged 删掉。
	//
	// 新增: DSH 是一张以执行对象为键的 WeakMap——JS 里给一个对象挂私有旁路状态的
	// 办法。Go 的 ds-harness-go/core/tools.ExecutionToken 本身就是可比较、可当 map
	// 键的相关性标识，所以这里是一张普通的 map，删除由那条最终通知负责。
	staged map[tools.ExecutionToken]json.RawMessage
	// pending 是一次成功的嵌套捕获，正等它外层那次传输提交。
	pending *stagedCapture
	// captured 是已经提交的那个值。
	captured json.RawMessage
	// hasCaptured 表示确实提交过一个值。
	//
	// 新增: DSH 用 `{ value } | undefined` 那层包装区分「提交了一个值」和「什么都
	// 没提交」。Go 里 json.RawMessage 的 nil 不是一个分得开的哨兵，所以另拿一位说
	// 这件事（理由和 ds-harness-go/core/agent.ConsumedWork.HasEnd 逐字相同）。
	hasCaptured bool
}

// Captured 交出那个已经提交的值：孩子调过那件工具、参数合法，而且它那条权威的
// 最终工具结果接受了这次调用。第二个返回值为假表示一个都没接受。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:33-38
func (a *StructuredAttachment) Captured() (json.RawMessage, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.captured, a.hasCaptured
}

// closed 说的是这次结构化运行是不是已经收口了——提交过、或者有一份等着外层提交的
// 嵌套捕获。守卫问的就是这一件事。
func (a *StructuredAttachment) closed() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.hasCaptured || a.pending != nil
}

// stage 把一次刚跑完的捕获调用收进暂存区，等它自己那条权威结果来提交。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:93
func (a *StructuredAttachment) stage(exec tools.Execution, args json.RawMessage) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.staged[exec.Token] = args
}

// commit 是那条权威结果上的提交判定。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:116-139
//
// 它观察的是走完整条管线、经过外层错误规范化之后那份不可变的权威结果。这条通知
// 改不了结局，所以提交判定外面不需要再裹一层。
func (a *StructuredAttachment) commit(exec tools.Execution, result tools.Result) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if exec.Name == StructuredOutputTool {
		staged, ok := a.staged[exec.Token]
		if !ok {
			return
		}
		delete(a.staged, exec.Token)
		if result.IsError {
			return
		}
		if exec.Parent.IsZero() {
			if !a.hasCaptured {
				a.captured, a.hasCaptured = staged, true
			}
			return
		}
		// 一次嵌套派发的捕获还不算数：外层那次传输仍旧可能把这次成功变成失败。
		if !a.hasCaptured && a.pending == nil {
			a.pending = &stagedCapture{parent: exec.Parent, value: staged}
		}
		return
	}
	// 不是捕获工具，那它只可能是那份嵌套捕获在等的外层传输。
	if a.pending == nil || a.pending.parent != exec.Token {
		return
	}
	waiting := a.pending
	a.pending = nil
	if result.IsError {
		return
	}
	if !a.hasCaptured {
		a.captured, a.hasCaptured = waiting.value, true
	}
}

// recordedOutputSchema 是这件工具那份钉死的输出契约：恰好一个 `recorded: true`。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:77-82
func recordedOutputSchema() tools.Node {
	closed := false
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{{
			Name:   "recorded",
			Schema: tools.Node{Type: tools.TypeBoolean, Const: json.RawMessage(`true`)},
		}},
		Required:             []string{"recorded"},
		AdditionalProperties: &closed,
	}
}

// AttachStructuredRuntime 在一个孩子的创建窗口里给它挂上那件带作用域的捕获工具、
// 那句指令和那道守卫。孩子一处置，这几笔登记跟着散掉。
//
// 源: packages/subagent/subagent-in-process-driver/src/structured.ts:49-142
//
// schema 是调用方已经断言过的那个受支持子集（见
// ds-harness-go/core/tools.AssertObjectSchema）。
//
// 新增: DSH 那四笔登记都是 void，撤销靠 cordis 处置孩子那个上下文。Go 这边它们
// 各自交回一个撤销函数，而这里**有意**把它们丢掉：owner 就是孩子自己那个作用域，
// 作用域一处置这四笔就跟着没了（成例见
// ds-harness-go/subagent/subagent.ApplyChildComposition）。
func AttachStructuredRuntime(
	ctx context.Context,
	childScope *scope.Scope,
	services StructuredServices,
	schema tools.Node,
) (*StructuredAttachment, error) {
	if services.Tools == nil {
		return nil, errInvalidRequestf("结构化输出需要工具运行时")
	}
	if services.SystemPrompt == nil {
		return nil, errInvalidRequestf("结构化输出需要系统提示词注册表")
	}
	attachment := &StructuredAttachment{staged: map[tools.ExecutionToken]json.RawMessage{}}

	// 新增: DSH 这个执行体开头还自己按 schema 验一遍参数、不合法就抛 ToolArgsError。
	// 那是因为它是用 `ctx.tools.register` 裸注册的，绕开了 `defineTool` 那层生成的
	// 校验包装。Go 的 ds-harness-go/core/tools.Runtime 在进执行体之前统一验过
	// （见它的 dispatchToolBody），失败的形状也一样是 ArgsError、同样走错误结果那条
	// 路，所以这里再验一遍是重复的。
	if _, err := services.Tools.Register(ctx, childScope, &tools.Definition{
		Name:        StructuredOutputTool,
		Description: structuredOutputDescription,
		Parameters:  schema,
		Output: tools.OutputDefinition{
			Schema: recordedOutputSchema(),
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: structuredOutputRecorded}}, nil
			},
		},
		Execute: func(_ context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
			// 两段提交，键是**这一次**执行：后面那些可改写的瀑布仍旧可能把这次成功
			// 变成失败。运行时在真正的输入边界上已经把发给模型的参数定死了。
			attachment.stage(exec.Execution, args)
			exec.ConcludeTurn()
			return json.RawMessage(`{"recorded":true}`), nil
		},
	}); err != nil {
		return nil, err
	}

	if _, err := services.SystemPrompt.Section(ctx, childScope, systemprompt.PromptSection{
		Name:  "tool:" + StructuredOutputTool,
		Order: structuredPromptSectionOrder,
		Text:  systemprompt.StaticText(StructuredOutputInstruction),
	}); err != nil {
		return nil, err
	}

	// 这一步之内的终局。守卫排在整条执行前瀑布之后，而且是单调复合的（只拒或者
	// 不表态，永远不放行），所以一个后来插到前面的监听者救不回这次派发。同一份
	// 响应里排在捕获**之前**的那些调用不受影响。
	if _, err := services.Tools.Guard(ctx, childScope, func(exec tools.Execution) string {
		if !attachment.closed() {
			return ""
		}
		return "structured output already recorded: the run is complete, so `" +
			exec.Name + "` is not executed"
	}); err != nil {
		return nil, err
	}

	if _, err := services.Tools.ObserveResult(ctx, childScope, attachment.commit); err != nil {
		return nil, err
	}
	return attachment, nil
}
