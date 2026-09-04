// 本文件的作用：一次工具调用从「模型点了个名」走到「一份可以落库的结果」之间，
// 中间那条**派发管线**——四条可扩展的瀑布、一道审批接缝、以及把这一切串起来的
// 分段调度器。
//
// 源: packages/core/tools/src/index.ts:1229-1935
//
// 注册表在 runtime.go；这里只管「一次调用怎么跑」。
//
// # 一次调用的完整路线
//
//	createExecution   造执行对象、快照收尾函数、验参数是不是合法 JSON
//	  ↓
//	prepare           查调用方是否已取消 → 执行前瀑布 → ask 交给审批 → 守卫
//	  ↓  三种去向：dispatch / post-result（还要过执行后瀑布）/ final-result（直接收尾）
//	dispatch          绕派发瀑布包着执行体 → 规范化 → 挂上推迟的上下文 → 取消覆盖
//	  ↓
//	finalize          执行后瀑布 → 取消覆盖 → finish
//	  ↓
//	finish            物化 → 工具自己的内容收尾 → 再物化 → 通知观察者
//
// 分成四段而不是一个函数，是因为 agent 循环要让**多次调用的派发重叠**，同时让
// 执行前和执行后这两段策略保持有序：并行组里的每一次调用各自 [Runtime.Prepare]
// 完，才一起 [Runtime.Dispatch]，回来再逐个 [Runtime.Finalize]。策略的顺序性和
// 派发的并发性是两件事，切开才同时拿得到。
//
// # 一切失败都是结果，不是错误
//
// [Runtime.Execute] 不返回 error。工具抛的、策略抛的、参数不合法、找不到工具、
// 被取消——全都变成 `IsError` 为真的一份结果。理由是这条管线的出口是**模型**：
// 模型只认得工具结果，认不得 Go 的 error；一次失败的调用和一次成功的调用在
// 会话日志里必须是同一种东西，否则重放的时候就少了一条消息。
//
// # panic 是被兜住的
//
// 执行体、渲染投影、并发判定、结果观察者这四处都在 recover 里跑。Go 的惯例是
// 不要跨越任意代码 recover，但这里的代码是**第三方注册进来的工具**，而这个进程
// 同时服务多个用户的多个会话：一个工具写错了下标，代价不该是所有人的会话一起死。
// 兜住之后它变成那一次调用的失败结果，其他调用照常。

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// PreDecisionKind 是一次派发前裁决的种类。
//
// 源: packages/core/tools/src/index.ts:588-591
type PreDecisionKind string

const (
	// PreAllow 放这次调用过去。
	PreAllow PreDecisionKind = "allow"
	// PreDeny 直接把这次调用变成一份错误结果。
	PreDeny PreDecisionKind = "deny"
	// PreAsk 把裁决交给审批接缝，只有拿到「这一次允许」才继续。
	PreAsk PreDecisionKind = "ask"
)

// PreDecision 是派发前的裁决。
//
// 源: packages/core/tools/src/index.ts:575-584（PreToolDecision）
//
// 这里**没有**「改写入参」这一档：参数在到这里之前就已经落进会话日志、也已经
// 呈现给用户看了，此时再改，日志里记的和真正跑的就是两回事。
type PreDecision struct {
	// Kind 是裁决种类，零值 "" 视同 [PreAllow]。
	Kind PreDecisionKind
	// Reason 是 [PreDeny] 时给模型看的理由；[PreAsk] 时是给用户看的请求说明，可以为空。
	Reason string
}

// PostDecisionKind 是一次派发后裁决的种类。
//
// 源: packages/core/tools/src/index.ts:597-600
type PostDecisionKind string

const (
	// PostAccept 认下这份结果，可以顺带替换内容或者值。
	PostAccept PostDecisionKind = "accept"
	// PostBlock 把这次调用改判成失败，用一段纠正性的反馈当内容。
	PostBlock PostDecisionKind = "block"
)

// PostDecision 是派发后的裁决。
//
// 源: packages/core/tools/src/index.ts:586-593（PostToolDecision）
//
// 新增: DSH 用两个 accept 变体在类型层面挡住「同时换值和换内容」（`value?: never`）。
// Go 的结构体表达不了这种互斥，所以它变成一条运行时规矩：两个都给就是一次编程错误，
// 本包把它变成这次调用的失败结果。换值和换内容不能并存的理由是**换值会重新渲染**——
// 两个都给，就有两份内容在争同一个位置。
type PostDecision struct {
	// Kind 是裁决种类，零值 "" 视同 [PostAccept]。
	Kind PostDecisionKind
	// Content 是 [PostAccept] 时替换掉的模型可见内容，为 nil 表示不换。
	Content llm.Content
	// Value 是 [PostAccept] 时替换掉的权威值，为 nil 表示不换。
	//
	// 换了值就会按这个工具的输出契约重新验一遍、重新渲染一遍，所以它不能和
	// Content 同时给。失败的结果也换不了值——失败本来就没有值。
	Value json.RawMessage
	// Feedback 是 [PostBlock] 时那段纠正性内容，它同时是失败信息的来源。
	Feedback llm.Content
	// AdditionalContexts 是这次裁决要挂给下一次请求的上下文。
	//
	// accept 时它接在工具自己推迟的上下文后面；block 时工具推迟的那些会被丢掉，
	// 只剩这里显式给的——一次被拦下的调用不该把它自己想说的话捎出去。
	AdditionalContexts []llm.Message
}

// PreRule 是执行前瀑布上的一环：可以直接裁决，也可以调 next 让后面的接着说。
//
// 源: packages/core/tools/src/index.ts:152（`tools/pre-execute`）
//
// 新增: DSH 这四条是挂在 cordis 事件总线上、靠 `ctx.waterfall` 分派的监听器。
// Go 里就是一串显式的「拿到 next、可以不调」的函数，参照 feature/telemetry.Rule
// 立下的先例。不调 next 就是**短路**：后面登记的规则一条都不会跑。
//
// 按作用域登记的规则只看得见那个 agent 的调用；登记在全局层的看得见全部。
// 顺序是先全局、再从最远的祖先到 agent 自己，先登记的在外层。
type PreRule func(exec Execution, next func() (PreDecision, error)) (PreDecision, error)

// DispatchRule 是绕派发瀑布上的一环：超时、重试、埋点都挂在这里。
//
// 源: packages/core/tools/src/index.ts:161（`tools/execute`）
//
// next 交回来的是一份**已经规范化**的结果。想给这次派发换一个更短的期限，就派生
// 一个 ctx 传给 next——但必须**从拿到的这个 ctx 派生**：context 的父子关系就是
// DSH 那边 `fuseToolSignals` 干的事，传一个不相干的 ctx 进去等于把调用方的取消
// 摘掉了，那是这条规矩唯一挡不住、也唯一不许犯的错。
type DispatchRule func(ctx context.Context, exec Execution, next func(context.Context) (Result, error)) (Result, error)

// PostRule 是执行后瀑布上的一环：认下、替换、追加上下文，或者拦下来。
//
// 源: packages/core/tools/src/index.ts:175（`tools/post-execute`）
//
// 工具自己抛出来的失败也会走到这条瀑布上——一条规则看得见失败，才有机会把
// 「这个工具又超时了」这类事实变成给模型的纠正性反馈。
type PostRule func(exec Execution, result Result, next func() (PostDecision, error)) (PostDecision, error)

// ResultObserver 观察一次调用的最终结果，改不了它。
//
// 源: packages/core/tools/src/index.ts:197（`tools/result`）
//
// 每个观察者拿到的都是自己那一份副本，所以谁也改不到别人看见的东西。
// 观察者自己 panic 会被兜住并记一条日志——它是**旁路**，不该让一次成功的调用失败。
type ResultObserver func(exec Execution, result Result)

// PreExecute 登记一条执行前规则，返回撤销它的函数。
//
// 源: packages/core/tools/src/index.ts:152
//
// 和守卫一样不发变更通知：它不改变**看得见什么**，只改变能不能跑。
func (r *Runtime) PreExecute(ctx context.Context, owner *scope.Scope, rule PreRule) (func(context.Context) error, error) {
	if rule == nil {
		return nil, errors.New("tools: 执行前规则不能是 nil")
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.preRules.Append(rule), nil
	}, scope.EffectOptions{Label: "tools.PreExecute()", Silent: true})
}

// AroundDispatch 登记一条绕派发规则，返回撤销它的函数。
//
// 源: packages/core/tools/src/index.ts:161
func (r *Runtime) AroundDispatch(ctx context.Context, owner *scope.Scope, rule DispatchRule) (func(context.Context) error, error) {
	if rule == nil {
		return nil, errors.New("tools: 绕派发规则不能是 nil")
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.dispatchRules.Append(rule), nil
	}, scope.EffectOptions{Label: "tools.AroundDispatch()", Silent: true})
}

// PostExecute 登记一条执行后规则，返回撤销它的函数。
//
// 源: packages/core/tools/src/index.ts:175
func (r *Runtime) PostExecute(ctx context.Context, owner *scope.Scope, rule PostRule) (func(context.Context) error, error) {
	if rule == nil {
		return nil, errors.New("tools: 执行后规则不能是 nil")
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.postRules.Append(rule), nil
	}, scope.EffectOptions{Label: "tools.PostExecute()", Silent: true})
}

// ObserveResult 登记一个结果观察者，返回撤销它的函数。
//
// 源: packages/core/tools/src/index.ts:197
func (r *Runtime) ObserveResult(ctx context.Context, owner *scope.Scope, observer ResultObserver) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("tools: 结果观察者不能是 nil")
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.observers.Append(observer), nil
	}, scope.EffectOptions{Label: "tools.ObserveResult()", Silent: true})
}

// ApprovalOutcome 是审批接缝给出的答复。
//
// 源: packages/interaction/user-approval/src/types.ts:28-32（ApprovalOutcome）
type ApprovalOutcome string

const (
	// ApprovalAllowedOnce 是「这一次允许」，不构成对以后的授权。
	ApprovalAllowedOnce ApprovalOutcome = "allowed-once"
	// ApprovalRejected 是「用户说不行」。
	ApprovalRejected ApprovalOutcome = "rejected"
	// ApprovalCancelled 是「这次询问在得到答复之前被取消了」。
	ApprovalCancelled ApprovalOutcome = "cancelled"
	// ApprovalUnavailable 是「压根没有能问的人」。
	ApprovalUnavailable ApprovalOutcome = "unavailable"
)

// ApprovalRequest 是一次要人拍板的请求。
//
// 源: packages/interaction/user-approval/src/index.ts:114-139（ApprovalRequest）
type ApprovalRequest struct {
	// Agent 是代表哪个 agent 在问，界面靠它把问题送到对的会话上。
	Agent *scope.Key
	// ToolName 是要跑的工具名。
	ToolName string
	// CallID 是这次调用的标识。
	CallID llm.CallID
	// Reason 是给用户看的说明，可以为空。
	Reason string
}

// Approval 是把一次 [PreAsk] 送到人面前的接缝。
//
// 源: packages/core/tools/src/index.ts:1691-1728
//
// 它是**用的时候才取**的可选后端：没装的部署里 ask 一律降级成拒绝。
// 实现方要观察 ctx——一次没人管的询问不该把整个 agent 循环挂死。
type Approval interface {
	// Request 问一次，返回答复。返回 error 视同 [ApprovalUnavailable]。
	Request(ctx context.Context, request ApprovalRequest) (ApprovalOutcome, error)
}

// StageKind 是分段调度器对「下一步走哪」的答复。
//
// 源: packages/core/tools/src/index.ts:424-441
type StageKind string

const (
	// StageDispatch 表示这次调用过了策略闸，可以派发。
	StageDispatch StageKind = "dispatch"
	// StagePostResult 表示已经有结果了，但它还要过执行后瀑布。
	StagePostResult StageKind = "post-result"
	// StageFinalResult 表示已经有结果了，并且它绕过执行后瀑布直接收尾。
	StageFinalResult StageKind = "final-result"
)

// Preparation 是 [Runtime.Prepare] 的产物：执行对象加上下一段该走哪。
//
// 源: packages/core/tools/src/index.ts:419-427（ScheduledToolPreparation）
type Preparation struct {
	// Kind 是下一段。
	Kind StageKind
	// Exec 是本包造出来的执行对象，后面每一段都要把它原样传回来。
	Exec *RunContext
	// Result 在 Kind 不是 [StageDispatch] 时有意义。
	Result Result
}

// Dispatched 是 [Runtime.Dispatch] 的产物。
//
// 源: packages/core/tools/src/index.ts:429-436（ScheduledToolDispatch）
type Dispatched struct {
	// Kind 只会是 [StagePostResult] 或者 [StageFinalResult]。
	Kind StageKind
	// Result 是这一段的结果。
	Result Result
}

// Execute 跑完一次工具调用的全程，交出那份最终结果。
//
// 源: packages/core/tools/src/index.ts:1343-1345
//
// 不返回 error：见本文件开头「一切失败都是结果，不是错误」。进入本方法之后、
// 最终结果物化之前到达的取消，会把一个还没起步的执行体换成
// [CodeAbortedBeforeDispatch]，把一次已经起步的**成功**换成 [CodeAborted]；
// 已经跑起来的活儿仍然会被等到收敛，本包不会丢下它不管。
func (r *Runtime) Execute(ctx context.Context, input ExecutionInput) Result {
	prepared := r.Prepare(ctx, input)
	switch prepared.Kind {
	case StageDispatch:
		dispatched := r.Dispatch(ctx, prepared.Exec)
		if dispatched.Kind == StagePostResult {
			return r.Finalize(ctx, prepared.Exec, dispatched.Result)
		}
		return r.Finish(ctx, prepared.Exec, dispatched.Result)
	case StagePostResult:
		return r.Finalize(ctx, prepared.Exec, prepared.Result)
	default:
		return r.Finish(ctx, prepared.Exec, prepared.Result)
	}
}

// Prepare 造执行对象，跑完执行前瀑布和守卫，给出下一段该走哪。
//
// 源: packages/core/tools/src/index.ts:1456-1504
//
// 它和 [Runtime.Dispatch]、[Runtime.Finalize]、[Runtime.Finish] 一起构成分段调度器，
// 是给 agent 循环里那个并行调度用的。只想跑一次调用的调用方用 [Runtime.Execute]。
func (r *Runtime) Prepare(ctx context.Context, input ExecutionInput) Preparation {
	exec, immediate := r.createExecution(input)
	if immediate != nil {
		return Preparation{Kind: StageFinalResult, Exec: exec, Result: *immediate}
	}
	if cancelled(ctx) {
		return Preparation{Kind: StageFinalResult, Exec: exec, Result: abortedBeforeDispatchResult(nil)}
	}

	decision, approvalCancelled, err := r.gate(ctx, exec)
	if err != nil {
		return Preparation{Kind: StageFinalResult, Exec: exec, Result: failureResult(err)}
	}
	// 审批那一路是等得起的：调用方在等答复的时候撤了，这次调用还没跑过任何东西，
	// 但它已经进过策略管线，所以按 post-result 收尾——让执行后瀑布也看见这件事。
	if approvalCancelled && cancelled(ctx) {
		return Preparation{Kind: StagePostResult, Exec: exec, Result: abortedBeforeDispatchResult(nil)}
	}

	denial := decision.Reason
	if decision.Kind == PreAllow || decision.Kind == "" {
		denial = r.guardReason(exec.Execution)
	}
	if denial != "" {
		return Preparation{Kind: StagePostResult, Exec: exec, Result: denialResult(denial)}
	}
	if cancelled(ctx) {
		return Preparation{Kind: StagePostResult, Exec: exec, Result: abortedBeforeDispatchResult(nil)}
	}
	return Preparation{Kind: StageDispatch, Exec: exec}
}

// gate 跑执行前瀑布，再把一次 ask 交给审批接缝解出 allow 或者 deny。
//
// 源: packages/core/tools/src/index.ts:1471-1481
func (r *Runtime) gate(ctx context.Context, exec *RunContext) (PreDecision, bool, error) {
	rules := collectRules(r, exec.Agent, func(layer *toolLayer) *scope.AnonymousEntries[PreRule] {
		return layer.preRules
	})
	next := func() (PreDecision, error) { return PreDecision{Kind: PreAllow}, nil }
	for index := len(rules) - 1; index >= 0; index-- {
		rule, inner := rules[index], next
		next = func() (PreDecision, error) { return rule(exec.Execution, inner) }
	}
	decision, err := next()
	if err != nil {
		return PreDecision{}, false, err
	}
	if decision.Kind != PreAsk {
		return decision, false, nil
	}
	return r.serviceAsk(ctx, exec, decision)
}

// serviceAsk 把一次 [PreAsk] 送过审批接缝，解成 allow 或者 deny。
//
// 源: packages/core/tools/src/index.ts:1691-1728
//
// 三种「没批准」给出三句不同的话，是为了让模型分得清「人说了不行」和「这里根本
// 没有能问的人」——前者该换个做法，后者该报告部署缺了东西。
func (r *Runtime) serviceAsk(ctx context.Context, exec *RunContext, ask PreDecision) (PreDecision, bool, error) {
	if r.approval == nil {
		reason := ask.Reason
		if reason == "" {
			reason = fmt.Sprintf("tool %q requires approval (not yet supported)", exec.Name)
		}
		return PreDecision{Kind: PreDeny, Reason: reason}, false, nil
	}
	if exec.Agent == nil {
		return PreDecision{
			Kind:   PreDeny,
			Reason: fmt.Sprintf("tool %q requires approval, but the call has no agent to route it through", exec.Name),
		}, false, nil
	}
	outcome, err := r.approval.Request(ctx, ApprovalRequest{
		Agent:    exec.Agent,
		ToolName: exec.Name,
		CallID:   exec.CallID,
		Reason:   ask.Reason,
	})
	if err != nil {
		outcome = ApprovalUnavailable
	}
	switch outcome {
	case ApprovalAllowedOnce:
		return PreDecision{Kind: PreAllow}, false, nil
	case ApprovalRejected:
		return PreDecision{
			Kind:   PreDeny,
			Reason: fmt.Sprintf("the user rejected tool %q", exec.Name),
		}, false, nil
	case ApprovalCancelled:
		return PreDecision{
			Kind:   PreDeny,
			Reason: fmt.Sprintf("approval for tool %q was cancelled", exec.Name),
		}, true, nil
	default:
		return PreDecision{
			Kind:   PreDeny,
			Reason: fmt.Sprintf("tool %q requires approval, but no approval channel is available", exec.Name),
		}, false, nil
	}
}

// Dispatch 跑绕派发瀑布和执行体，交出一份还没过执行后瀑布的结果。
//
// 源: packages/core/tools/src/index.ts:1569-1599
func (r *Runtime) Dispatch(ctx context.Context, exec *RunContext) Dispatched {
	rules := collectRules(r, exec.Agent, func(layer *toolLayer) *scope.AnonymousEntries[DispatchRule] {
		return layer.dispatchRules
	})
	next := func(inner context.Context) (Result, error) { return r.dispatchToolBody(inner, exec), nil }
	for index := len(rules) - 1; index >= 0; index-- {
		rule, inner := rules[index], next
		next = func(current context.Context) (Result, error) { return rule(current, exec.Execution, inner) }
	}

	result, err := next(ctx)
	if err != nil {
		return Dispatched{Kind: StageFinalResult, Result: failureResult(err)}
	}
	normalized, err := r.normalizeDispatchResult(exec, result)
	if err != nil {
		return Dispatched{Kind: StageFinalResult, Result: failureResult(err)}
	}
	if len(exec.deferred) > 0 {
		normalized.AdditionalContexts = append(append([]llm.Message{}, exec.deferred...), normalized.AdditionalContexts...)
	}
	if cancelled(ctx) && !normalized.IsError {
		return Dispatched{Kind: StagePostResult, Result: exec.cancellationResult(&normalized)}
	}
	return Dispatched{Kind: StagePostResult, Result: normalized}
}

// dispatchToolBody 解析出这次调用真正该跑的那份定义，验参数，然后跑它。
//
// 源: packages/core/tools/src/index.ts:1524-1560
//
// 新增: DSH 的参数校验藏在 `defineTool` 生成的那层包装里，每个工具各带一份。
// Go 这边没有那层包装（它靠的是 TS 的泛型推导，Go 表达不了），所以校验挪到这里
// 统一做一次。失败的形状不变：还是一个 [ArgsError]，还是走同一条错误结果的路。
func (r *Runtime) dispatchToolBody(ctx context.Context, exec *RunContext) Result {
	if cancelled(ctx) {
		return abortedBeforeDispatchResult(nil)
	}
	definition, ok := r.Get(exec.Name, exec.Agent)
	if !ok {
		return failureResult(&NotFoundError{ToolName: exec.Name})
	}
	exec.definition = definition
	if violations := ValidateValue(definition.Parameters, exec.Arguments, ""); len(violations) > 0 {
		return failureResult(&ArgsError{Violations: violations})
	}

	exec.bodyInvoked = true
	value, err := invokeBody(ctx, definition, exec)
	if err != nil {
		return failureResult(err)
	}
	result, err := r.createSuccessResult(exec, definition, value)
	if err != nil {
		return failureResult(err)
	}
	// 执行体已经收敛了才发现调用方撤了：活儿白干，但结果不能当成功交出去。
	if cancelled(ctx) {
		return abortedResult(&result)
	}
	return result
}

// invokeBody 跑一次执行体，把它的 panic 兜成一个普通的 error。
//
// 源: packages/core/tools/src/index.ts:1546-1554（`try { await tool.execute(...) }`）
func invokeBody(ctx context.Context, definition *Definition, exec *RunContext) (value json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value, err = nil, fmt.Errorf("tool %q panicked: %v", definition.Name, recovered)
		}
	}()
	return definition.Execute(ctx, exec.Arguments, exec)
}

// Finalize 跑执行后瀑布，再交给 [Runtime.Finish] 收尾。
//
// 源: packages/core/tools/src/index.ts:1609-1621
func (r *Runtime) Finalize(ctx context.Context, exec *RunContext, result Result) Result {
	decided, err := r.postExecute(exec, result)
	if err != nil {
		return r.Finish(ctx, exec, failureResult(err))
	}
	if cancelled(ctx) && !decided.IsError {
		return r.Finish(ctx, exec, exec.cancellationResult(&decided))
	}
	return r.Finish(ctx, exec, decided)
}

// postExecute 跑执行后瀑布，并把它的裁决落到结果上。
//
// 源: packages/core/tools/src/index.ts:1740-1781
func (r *Runtime) postExecute(exec *RunContext, result Result) (Result, error) {
	rules := collectRules(r, exec.Agent, func(layer *toolLayer) *scope.AnonymousEntries[PostRule] {
		return layer.postRules
	})
	next := func() (PostDecision, error) { return PostDecision{Kind: PostAccept}, nil }
	for index := len(rules) - 1; index >= 0; index-- {
		rule, inner := rules[index], next
		snapshot := result.clone()
		next = func() (PostDecision, error) { return rule(exec.Execution, snapshot, inner) }
	}
	decision, err := next()
	if err != nil {
		return Result{}, err
	}

	if decision.Kind == PostBlock {
		blocked := blockedResult(decision.Feedback)
		blocked.AdditionalContexts = decision.AdditionalContexts
		return blocked, nil
	}
	if decision.Content != nil && decision.Value != nil {
		return Result{}, errors.New("tools: 一条执行后规则不能同时替换值和内容")
	}

	contexts := append(append([]llm.Message{}, result.AdditionalContexts...), decision.AdditionalContexts...)
	if decision.Value != nil {
		if result.IsError {
			return Result{}, errors.New("tools: 一条执行后规则不能给失败的结果换上一个值")
		}
		definition, ok := r.Get(exec.Name, exec.Agent)
		if !ok {
			return Result{}, &NotFoundError{ToolName: exec.Name}
		}
		replaced, err := r.createSuccessResult(exec, definition, decision.Value)
		if err != nil {
			return Result{}, err
		}
		replaced.AdditionalContexts = contexts
		return replaced, nil
	}
	if decision.Content != nil {
		result.Content = decision.Content
	}
	result.AdditionalContexts = contexts
	return result, nil
}

// Finish 物化结果、让工具做最后一次内容收尾、再物化一次，然后通知观察者。
//
// 源: packages/core/tools/src/index.ts:1631-1645
//
// 物化做两遍不是冗余：收尾函数拿到的必须是一份**已经定形**的结果（它据此决定改不改
// 内容），而它改完之后那份才是真正要落库的，得再定形一次。
func (r *Runtime) Finish(ctx context.Context, exec *RunContext, result Result) Result {
	materialized := materialize(result)
	final := materialize(r.applyFinalContent(exec, materialized))
	r.notifyResult(exec, final)
	return final
}

// applyFinalContent 用调用刚开始时快照下来的那个收尾函数改一次内容。
//
// 源: packages/core/tools/src/index.ts:1647-1653
//
// 快照发生在调用**开始**时，而不是这里现取：一次调用跑到一半，别人可能已经把这个
// 工具注销、换成另一份定义了；用现取的那份，等于让这次调用的结尾归另一个工具管。
func (r *Runtime) applyFinalContent(exec *RunContext, result Result) Result {
	if exec.finalizer == nil {
		return result
	}
	content := callFinalizer(r, exec, result)
	if content == nil {
		return result
	}
	result.Content = content
	return result
}

// callFinalizer 跑一次内容收尾，把它的 panic 兜成「不改」。
func callFinalizer(r *Runtime, exec *RunContext, result Result) (content llm.Content) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("tools: 内容收尾函数 panic 了，保留原内容",
				"tool", exec.Name, "call_id", string(exec.CallID), "panic", fmt.Sprint(recovered))
			content = nil
		}
	}()
	return exec.finalizer(exec.Execution, result)
}

// notifyResult 把最终结果发给每一个观察者，各发一份副本。
//
// 源: packages/core/tools/src/index.ts:1655-1675
func (r *Runtime) notifyResult(exec *RunContext, result Result) {
	observers := collectRules(r, exec.Agent, func(layer *toolLayer) *scope.AnonymousEntries[ResultObserver] {
		return layer.observers
	})
	for _, observer := range observers {
		r.callObserver(observer, exec, result.clone())
	}
}

// callObserver 跑一个观察者，把它的 panic 兜成一条日志。
func (r *Runtime) callObserver(observer ResultObserver, exec *RunContext, result Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("tools: 结果观察者 panic 了",
				"tool", exec.Name, "call_id", string(exec.CallID), "panic", fmt.Sprint(recovered))
		}
	}()
	observer(exec.Execution, result)
}

// createExecution 造一次调用的执行对象，并快照它的内容收尾函数。
//
// 源: packages/core/tools/src/index.ts:1364-1451
//
// 返回的第二个值非 nil 表示这次调用在进策略管线之前就已经定死了结果。
func (r *Runtime) createExecution(input ExecutionInput) (*RunContext, *Result) {
	if input.RootCallID == "" {
		input.RootCallID = input.CallID
	}
	exec := &RunContext{Execution: Execution{ExecutionInput: input, Token: newExecutionToken()}}
	// 收尾函数在这里就快照下来，理由见 [Runtime.applyFinalContent]。
	// 工具此刻不可见也照样往下走：UNKNOWN_TOOL 的路留在派发那一段，好让每一条
	// 执行前规则都看得见模型点过的每一个名字。
	if definition, ok := r.Get(input.Name, input.Agent); ok {
		exec.finalizer = definition.FinalizeContent
	}
	// 尺寸先于形状：一份几十兆的载荷不该先被 [json.Valid] 整个走一遍才被拒。
	if r.maxArgumentBytes > 0 && len(input.Arguments) > r.maxArgumentBytes {
		failure := failureResult(&ArgsTooLargeError{
			ToolName: input.Name,
			Bytes:    len(input.Arguments),
			Limit:    r.maxArgumentBytes,
		})
		return exec, &failure
	}
	if len(input.Arguments) == 0 || !json.Valid(input.Arguments) {
		failure := failureResult(&ArgsError{Violations: []string{"arguments must be valid JSON"}})
		return exec, &failure
	}
	return exec, nil
}

// createSuccessResult 验一个值、渲染它、可能再投影一份呈现载荷。
//
// 源: packages/core/tools/src/index.ts:1793-1822
func (r *Runtime) createSuccessResult(exec *RunContext, definition *Definition, value json.RawMessage) (Result, error) {
	if value == nil {
		value = json.RawMessage("null")
	}
	if !json.Valid(value) {
		return Result{}, &OutputError{ToolName: definition.Name, Violations: []string{"value is not valid JSON"}}
	}
	if violations := ValidateValue(definition.Output.Schema, value, "value"); len(violations) > 0 {
		return Result{}, &OutputError{ToolName: definition.Name, Violations: violations}
	}
	content, err := renderContent(definition, exec.Arguments, value)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		IsError:       false,
		Value:         value,
		Content:       content,
		ConcludesTurn: exec.concludes,
	}
	// 呈现载荷只在**顶层**调用上算：一次嵌在复合工具下面的调用没有自己的卡片，
	// 算出来的东西没有任何地方能显示它。
	if exec.Parent.IsZero() && definition.Output.PresentationMeta != nil {
		meta, err := renderMeta(definition, exec.Arguments, value)
		if err != nil {
			return Result{}, err
		}
		result.Meta = meta
	}
	exec.canonicalValue = value
	return result, nil
}

// renderContent 跑一次内容投影，把 panic 和 error 都变成一个 [OutputError]。
//
// 源: packages/core/tools/src/index.ts:517-520（projectionError）
func renderContent(definition *Definition, args, value json.RawMessage) (content llm.Content, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			content, err = nil, projectionError(definition.Name, "render", fmt.Errorf("%v", recovered))
		}
	}()
	content, err = definition.Output.Render(args, value)
	if err != nil {
		return nil, projectionError(definition.Name, "render", err)
	}
	return content, nil
}

// renderMeta 跑一次呈现载荷投影，把 panic 和 error 都变成一个 [OutputError]。
func renderMeta(definition *Definition, args, value json.RawMessage) (meta json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			meta, err = nil, projectionError(definition.Name, "presentationMeta", fmt.Errorf("%v", recovered))
		}
	}()
	meta, err = definition.Output.PresentationMeta(args, value)
	if err != nil {
		return nil, projectionError(definition.Name, "presentationMeta", err)
	}
	if meta != nil && !json.Valid(meta) {
		return nil, &OutputError{
			ToolName:   definition.Name,
			Violations: []string{"output.presentationMeta returned non-lossless JSON"},
		}
	}
	return meta, nil
}

// projectionError 把一次投影失败包成这个工具的输出违规。
//
// 源: packages/core/tools/src/index.ts:525-527
func projectionError(toolName, projector string, err error) error {
	return &OutputError{
		ToolName:   toolName,
		Violations: []string{fmt.Sprintf("output.%s failed: %s", projector, err.Error())},
	}
}

// normalizeDispatchResult 把绕派发瀑布交回来的结果按这个工具的输出契约重新对一遍。
//
// 源: packages/core/tools/src/index.ts:1826-1843
//
// 新增: DSH 靠一张 WeakMap 认「这份结果是不是我自己造的原件」，认出来就整份跳过。
// Go 的结构体是值，一个包装函数复制一份再改个字段，复制件和原件在语言层面完全一样，
// 那张表在这里立不住。所以判据换成**值变没变**：
//
//   - Value 和本包最近一次验过的那个一样 → 这次派发没有改动权威事实，原样放行。
//     顺带地，包装函数对 Content 的改写会被保留——它是显式表达的意图，而值没被动过，
//     模型看到的内容和权威值之间的那条不变式仍然成立。
//   - Value 不一样 → 按输出 schema 重新验、重新渲染。包装函数自己写的内容在这里
//     被丢掉，因为内容必须是**那个值**渲染出来的，不能是另一个值渲染出来的。
func (r *Runtime) normalizeDispatchResult(exec *RunContext, result Result) (Result, error) {
	if result.IsError {
		result.Value = nil
		result.ConcludesTurn = false
		return result, nil
	}
	if exec.canonicalValue != nil && string(result.Value) == string(exec.canonicalValue) {
		return result, nil
	}
	definition, ok := r.Get(exec.Name, exec.Agent)
	if !ok {
		return Result{}, &NotFoundError{ToolName: exec.Name}
	}
	normalized, err := r.createSuccessResult(exec, definition, result.Value)
	if err != nil {
		return Result{}, err
	}
	normalized.AdditionalContexts = result.AdditionalContexts
	return normalized, nil
}

// materialize 把一份结果定形：复制掉里面每一段可写的东西，并守住那两条不变式。
//
// 源: packages/core/tools/src/index.ts:1846-1863
//
// 新增: DSH 这一步同时做 deepFreeze，让发布出去的结果在运行时改不动。Go 没有那个
// 手段，代价由复制承担——收到结果的人改的是自己那一份。
func materialize(result Result) Result {
	if result.IsError {
		result.Value = nil
		result.ConcludesTurn = false
		if result.Error == nil {
			result.Error = &Failure{Message: "tool call failed"}
		}
	} else {
		result.Error = nil
	}
	return result.clone()
}

// ExecutionMode 判一次待调用能不能和兄弟调用重叠。
//
// 源: packages/core/tools/src/index.ts:1275-1285
//
// 一律**失败即独占**：看不见的工具、没声明判定的工具、判定 panic 的工具，全都独占。
// 并行是一项需要工具明确认领的能力，不是默认待遇——错判成独占只是慢一点，
// 错判成并行会让两次调用同时改同一份状态。
func (r *Runtime) ExecutionMode(input ExecutionInput) ExecutionModeKind {
	definition, ok := r.Get(input.Name, input.Agent)
	if !ok || definition.IsConcurrencySafe == nil {
		return ModeExclusive
	}
	if concurrencySafe(definition, input.Arguments) {
		return ModeParallel
	}
	return ModeExclusive
}

// concurrencySafe 跑一次并发判定，把它的 panic 兜成 false。
func concurrencySafe(definition *Definition, args json.RawMessage) (safe bool) {
	defer func() {
		if recover() != nil {
			safe = false
		}
	}()
	return definition.IsConcurrencySafe(args)
}

// cancellationResult 按「执行体起没起步」选一份中止结果。
//
// 源: packages/core/tools/src/index.ts:1515-1522
func (c *RunContext) cancellationResult(prior *Result) Result {
	if c.bodyInvoked {
		return abortedResult(prior)
	}
	return abortedBeforeDispatchResult(prior)
}

// cancelled 说明这个 ctx 已经被取消了。
func cancelled(ctx context.Context) bool { return ctx.Err() != nil }

// collectRules 按「先全局、再从最远的祖先到自己」的顺序收齐一条链上的规则。
//
// 源: packages/core/tools/src/index.ts:1477（`ctx.waterfall` 的作用域分派）
func collectRules[T any](r *Runtime, key *scope.Key, pick func(*toolLayer) *scope.AnonymousEntries[T]) []T {
	var rules []T
	for rule := range pick(r.layers.Global()).Values() {
		rules = append(rules, rule)
	}
	if key == nil {
		return rules
	}
	for _, layer := range r.layers.ChainLayers(key) {
		for rule := range pick(layer).Values() {
			rules = append(rules, rule)
		}
	}
	return rules
}

// failureResult 把一个 error 变成一份给模型看的失败结果。
//
// 源: packages/core/tools/src/index.ts:1869-1877
func failureResult(err error) Result {
	message := err.Error()
	failure := &Failure{Message: message}
	var coded Coded
	if errors.As(err, &coded) {
		failure.Info = &ErrorInfo{Name: coded.ErrorName(), Code: coded.ErrorCode()}
	}
	return Result{
		IsError: true,
		Error:   failure,
		Content: llm.Content{llm.TextBlock{Text: "Error: " + message}},
	}
}

// denialResult 是一次被执行前策略或者守卫拦下的调用。
//
// 源: packages/core/tools/src/index.ts:1489-1497
//
// 它不带 Info：拒绝的理由是策略现写的一句话，不是一个有身份的错误类。
func denialResult(reason string) Result {
	return Result{
		IsError: true,
		Error:   &Failure{Message: reason},
		Content: llm.Content{llm.TextBlock{Text: "Error: " + reason}},
	}
}

// blockedResult 是一次被执行后策略改判成失败的调用。
//
// 源: packages/core/tools/src/index.ts:1746-1754
//
// 失败信息从那段反馈里读出来，而不是另写一句：模型看到的和日志里记的是同一件事。
func blockedResult(feedback llm.Content) Result {
	return Result{
		IsError: true,
		Error:   &Failure{Message: failureMessageFromContent(feedback)},
		Content: feedback,
	}
}

// failureMessageFromContent 从一段反馈里读出一句失败信息，不改动它渲染出来的块。
//
// 源: packages/core/tools/src/index.ts:625-631
func failureMessageFromContent(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s content]", block.BlockType()))
	}
	if len(parts) == 0 {
		return "tool result blocked by post-execute policy"
	}
	joined := ""
	for index, part := range parts {
		if index > 0 {
			joined += "\n"
		}
		joined += part
	}
	if joined == "" {
		return "tool result blocked by post-execute policy"
	}
	return joined
}

// abortedResult 是取消盖掉了一次**已经起步**的执行。
//
// 源: packages/core/tools/src/index.ts:1919-1931
func abortedResult(prior *Result) Result {
	return canonicalAbort("tool call aborted", CodeAborted, prior)
}

// abortedBeforeDispatchResult 是取消让执行体压根没起步。
//
// 源: packages/core/tools/src/index.ts:1923-1935
func abortedBeforeDispatchResult(prior *Result) Result {
	return canonicalAbort("tool call aborted before dispatch", CodeAbortedBeforeDispatch, prior)
}

// AbortedBeforeDispatchResult 造一份「取消赶在执行体起步之前到达」的规范结果，
// 给循环那一层替**根本没轮到**的那些模型调用补结果用。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:249-259
//
// 新增: DSH 在 agent-loop 那边把这份结果的形状又手写了一遍，只从 dsh-tools 借了
// 那个错误码。两处一旦对不上，日志里就会出现两种措辞不同的「派发前中止」，
// 而认它的下游是按 [Failure.Message] 和 [ErrorInfo] 认的。这里改成由本包交出去，
// 循环那一层直接用，两处不可能再分叉。
func AbortedBeforeDispatchResult() Result { return abortedBeforeDispatchResult(nil) }

// canonicalAbort 造一份中止结果，并把前一份结果攒下的上下文带上。
//
// 上下文要带走，是因为它们是**已经发生过的事实**：一个复合工具已经派发出去的
// 子调用捎回来的话，不会因为外层被取消就变得不曾说过。
func canonicalAbort(message, code string, prior *Result) Result {
	result := Result{
		IsError: true,
		Error:   &Failure{Message: message, Info: &ErrorInfo{Name: "AbortError", Code: code}},
		Content: llm.Content{llm.TextBlock{Text: "Error: " + message}},
	}
	if prior != nil && len(prior.AdditionalContexts) > 0 {
		result.AdditionalContexts = prior.AdditionalContexts
	}
	return result
}
