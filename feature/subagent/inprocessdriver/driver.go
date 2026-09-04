// 本文件的作用：那条「建孩子 → 投提示词 → 等静止 → 读结果 → 处置」的共用路，
// 以及它交出去的那个一次性运行句柄。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:14-233

package inprocessdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/feature/subagent/internal/childseed"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Services 是这台驱动要用到的那几样部署服务。
//
// 新增: DSH 全从 `parent.ctx`（cordis 上下文）上取：`parent.ctx.agents`、
// `childCtx.systemPrompt`、`childCtx.tools`、`childCtx.userApproval`。Go 没有那个
// 容器，「在不在场」就是装配方手上有没有这个值，所以做成一个显式的结构体
// （成例见 [github.com/snight1983/ds-harness-go/feature/subagent.ChildCompositionServices]）。
type Services struct {
	// Agents 是 agent 注册表：孩子从它建出来，那笔描述符前置步骤观察者也挂在它上面。必填。
	Agents *agent.Registry
	// Owner 是这次创建的主人作用域——句柄的生命周期归它，处置它就把还没处置的孩子带走。必填。
	Owner *scope.Scope
	// Composition 是孩子那份组装要用到的服务（系统提示词、工具、预设名册）。
	// 其中的 SystemPrompt 必填；要结构化输出时 Tools 也必填。
	Composition subagent.ChildCompositionServices
	// Approval 是用户审批服务；nil 表示这套部署没组装审批能力，于是不种派发策略。
	Approval *userapproval.Service
}

// RunOptions 是 spawn 和 fork 两个提供方额外给这台驱动的东西。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:68-72（InProcessRunOptions）
type RunOptions struct {
	// Seed 是 fork 那份「父日志上一段回合完整的前缀」；nil 表示一次全新的 spawn。
	//
	// **nil 和空切片不是一回事**（见 [github.com/snight1983/ds-harness-go/harness/session.Options.Seed]）：
	// 前者是全新的孩子，后者是一段长度为零的继承前缀。
	Seed []sessionlog.Event

	// SeedBaseSeq 是 Seed 第一条应有的 seq；默认 0。
	//
	// 新增: 理由见 [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions.BaseSeq]。
	// 父会话的日志被弹过头时这段前缀不从 0 起。
	SeedBaseSeq int
}

// StartInProcessRun 立起并驱动一个进程内的一次性孩子；交回的那一刻这个孩子已经
// 在注册表里公布了，此后它的回合、取消和处置全从交回的那个运行走。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:100-142
//
// 报错表示 agent 工厂那次还没公布的创建事务已经静止、且一个孩子都没公布——
// 调用方没有任何东西要处置。每一次开工都在孩子的第一个回合之内追加那份解算好的
// 描述符。
//
// 新增: DSH 从 `request.signal` 拿取消。Go 的取消是 ctx，所以这里的 ctx 一身兼两职，
// 和 DSH 那个 signal 逐字对应：公布之前它是创建窗口的取消口，公布之后它是那条
// 「父反悔了，把孩子停掉」的通道。
func StartInProcessRun(
	ctx context.Context,
	services Services,
	request subagent.ResolvedStartRequest,
	options RunOptions,
) (subagent.Run, error) {
	if services.Agents == nil {
		return nil, errInvalidRequestf("进程内子 agent 驱动需要 agent 注册表")
	}
	if services.Owner == nil {
		return nil, errInvalidRequestf("进程内子 agent 驱动需要一个主人作用域")
	}
	if request.Parent == nil {
		return nil, errInvalidRequestf("进程内子 agent 驱动需要一个确切的活父 agent")
	}
	if err := subagent.AssertMaxDepth(request.MaxDepth); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, prePublicationAbort(err)
	}
	parent := request.Parent
	childDepth, err := subagent.ResolveChildDepth(parent, request.MaxDepth)
	if err != nil {
		return nil, err
	}

	childID := sessionlog.SessionID(uuid.NewString())
	activationBoundary := len(options.Seed)

	// 在这次孩子开工的第一个可中断点**之前**拍下来：父后来那次切换属于父的未来。
	inherited := subagent.CaptureDelegatedPolicyOverrides(services.Approval)

	// structured 只由 setup 那一路写、由驱动那一路读，而 agent 工厂在交回句柄之前
	// 已经跑完并提交了 setup，所以这次交接由「创建返回」这件事本身定序，不需要锁。
	var structured *StructuredAttachment
	setup := func(setupCtx context.Context, childScope *scope.Scope) (func() error, error) {
		if err := subagent.ApplyChildComposition(setupCtx, childScope, parent, subagent.ChildComposition{
			Persona:    request.Persona,
			ToolFilter: request.ToolFilter,
		}, services.Composition); err != nil {
			return nil, err
		}
		if request.OutputSchema != nil {
			attachment, err := AttachStructuredRuntime(setupCtx, childScope, StructuredServices{
				Tools:        services.Composition.Tools,
				SystemPrompt: services.Composition.SystemPrompt,
			}, *request.OutputSchema)
			if err != nil {
				return nil, err
			}
			structured = attachment
		}
		if err := attachDescriptorAppend(setupCtx, services.Agents, childScope, request.Descriptor); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// 新增: DSH 在 setup 里拿 `childCtx.agent.session` 现场追加那份派发策略。Go 的
	// [github.com/snight1983/ds-harness-go/harness/agent.Setup] 只收作用域，那一刻会话还没登记进
	// [github.com/snight1983/ds-harness-go/harness/session.Store]，所以改成在种子上排演一次——和
	// [github.com/snight1983/ds-harness-go/feature/subagent.SeedDescriptorTurn] 完全同一条路子：那几条
	// 事件照样落在 SeedLength 边界**之后**，因此仍旧是这个孩子自己的历史，也照样在
	// 公布之前就定死了。续行激活那条路走的是同一个函数。
	seed, err := childseed.Seed(childID, options.Seed, options.SeedBaseSeq, inherited.ApprovalPolicy)
	if err != nil {
		return nil, err
	}

	create := subagent.ChildSessionMeta(parent, childDepth, activationBoundary, services.Composition.Presets)
	create.SessionID = childID
	create.Seed = seed
	create.BaseSeq = options.SeedBaseSeq
	create.AgentOptions = subagent.ResolveChildAgentOptions(parent, request.AgentOptions)
	create.Setup = setup

	handle, err := services.Agents.Create(ctx, services.Owner, create)
	if err != nil {
		return nil, err
	}
	return drivePublishedRun(ctx, handle, request.Prompt, childID, activationBoundary, structured), nil
}

// attachDescriptorAppend 在孩子的初始回合之内、它第一次请求之前，追加**恰好一条**
// 一次性描述符。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:78-88
//
// 挂在前置步骤那条瀑布的**外层**：先调 next 拿到里面那一层的决定，只有决定是
// 「进」才追加。一次被拒的步骤什么都不写，于是一个从没跑过的孩子日志上也就没有
// 那条身份事件。
//
// 新增: DSH 那个 `appended` 是闭包里的一个布尔，单线程下够用；Go 这条瀑布跑在
// agent 自己那条驱动上，用 [sync.Once] 表达「恰好一条」。追加对象取的是这一步
// 那个 agent（和 DSH 一样），而不是另外捉一个句柄——观察者登记在孩子**自己**那个
// 作用域上，注册表按 agent 的作用域链收观察者，所以够得到它的只有这个孩子和它
// 将来的后代，而孩子的第一个步骤必定排在任何后代出现之前。
func attachDescriptorAppend(
	ctx context.Context,
	agents *agent.Registry,
	childScope *scope.Scope,
	descriptor subagent.DescriptorData,
) error {
	data, err := json.Marshal(descriptor)
	if err != nil {
		return errInvalidRequestf("描述符排不成无损 JSON：%v", err)
	}
	var once sync.Once
	// 这笔登记的撤销函数**有意**丢掉：owner 就是孩子自己那个作用域，作用域一处置
	// 它就跟着没了（成例见 [github.com/snight1983/ds-harness-go/feature/subagent.ApplyChildComposition]）。
	_, err = agents.OnPreStep(ctx, childScope, func(
		ctx context.Context,
		step agent.PreStep,
		next func(context.Context) (agent.PreStepDecision, error),
	) (agent.PreStepDecision, error) {
		decision, err := next(ctx)
		if err != nil || !decision.Enter {
			return decision, err
		}
		var appendErr error
		once.Do(func() {
			_, appendErr = step.Agent.Session().Append(sessionlog.Event{
				Type: subagent.EventDescriptor,
				Data: data,
			})
		})
		if appendErr != nil {
			return agent.PreStepDecision{}, fmt.Errorf("追加子 agent 描述符失败：%w", appendErr)
		}
		return decision, nil
	})
	return err
}

// toStopReason 把一个回合的结局映成这条子 agent 接缝的终止词汇。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:47-64
//
// 新增: DSH 收的是 `TurnEndReason | undefined`，「没有交代回合」落进 default。
// Go 这边收的是 [github.com/snight1983/ds-harness-go/harness/agent.FoldConsumedWork] 的产物，
// HasEnd 为假就是那个 undefined。
func toStopReason(work agent.ConsumedWork) subagent.StopReason {
	if !work.HasEnd {
		return subagent.StopError
	}
	var data sessionlog.TurnEndData
	if err := json.Unmarshal(work.End.Data, &data); err != nil {
		return subagent.StopError
	}
	switch data.Reason.TurnEndReasonKind() {
	case sessionlog.ReasonCompleted:
		return subagent.StopCompleted
	case sessionlog.ReasonMaxTokens:
		return subagent.StopMaxTokens
	case sessionlog.ReasonAborted:
		return subagent.StopAborted
	case sessionlog.ReasonBlocked:
		// 一次前置步骤的拒绝把认领下来的提示词丢掉了：这件活儿是被回绝了，
		// 调用方不许把这次运行读成做完了。
		return subagent.StopRefusal
	default:
		// ReasonError、ReasonInterrupted，以及后端加出来的新变体。
		return subagent.StopError
	}
}

// inProcessRun 是一个已公布孩子外面那层唯一的运行生命周期：信号交接、一个回合、
// 结果结清，以及静止的处置。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:148-190
type inProcessRun struct {
	// id 是这个孩子的会话 id，对本地运行它就是运行 id。
	id sessionlog.SessionID
	// handle 是那份公布出来的生命周期所有权。
	handle agent.Handle
	// child 是 handle.Agent，读起来顺一点。
	child agent.Agent
	// boundary 是活化边界：这之后的事件才是这个孩子自己的历史。
	boundary int
	// structured 是这次运行那份结构化捕获；nil 表示没要结构化输出。
	structured *StructuredAttachment

	// done 在结果那一路结清时关掉。
	done chan struct{}
	// result 和 resultErr 只由那条驱动 goroutine 在关掉 done **之前**写。
	result    subagent.Result
	resultErr error

	// mutex 看着 cancelled——守望 goroutine、处置方、驱动 goroutine 三边都碰它。
	mutex sync.Mutex
	// cancelled 记的是「在这次尝试的结局被观察到之前，本地取消有没有先结清」。
	cancelled bool

	// stopWatch 摘掉那个取消守望，可以重复调。
	stopWatch func()
	// disposeOnce 让处置只跑一遍，重复调等着第一遍的结论。
	disposeOnce sync.Once
	// disposeErr 是第一遍处置的结论。
	disposeErr error
}

// drivePublishedRun 把一个已公布的孩子裹进那唯一的运行生命周期。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:148-190
func drivePublishedRun(
	ctx context.Context,
	handle agent.Handle,
	prompt llm.Content,
	childID sessionlog.SessionID,
	boundary int,
	structured *StructuredAttachment,
) subagent.Run {
	run := &inProcessRun{
		id:         childID,
		handle:     handle,
		child:      handle.Agent,
		boundary:   boundary,
		structured: structured,
		done:       make(chan struct{}),
	}

	// 新增: DSH 是 `signal.addEventListener('abort', onAbort, {once:true})` 加两处
	// removeEventListener。Go 没有可摘的监听器，等价物是一条守望 goroutine 加一个
	// 关闭信道——摘监听器就是关掉那个信道，让它退出，于是它同样不会活过这次运行。
	stopped := make(chan struct{})
	var stopOnce sync.Once
	run.stopWatch = func() { stopOnce.Do(func() { close(stopped) }) }
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				run.abort()
			case <-stopped:
			}
		}()
	}
	// 建 agent 那一步在交回之前已经摘掉了它那个只管创建的监听器。这次登记之后的
	// 重查把那道交接补上，同时又不会把一个已经公布的孩子当成一次失败的开工。
	if ctx.Err() != nil {
		run.abort()
	}

	// 新增: 驱动那一路拿的是一个**去掉取消**的 ctx。DSH 那个 async IIFE 压根不看
	// signal——它靠 onAbort 去 cancel 孩子，然后照样等孩子静下来、照样读结果。
	// 用会被取消的 ctx，会让 WhenIdle 在孩子还在写日志的时候就返回，读结果那一步
	// 于是和循环的收尾赛跑。值照样带过去。
	driveCtx := context.WithoutCancel(ctx)
	go func() {
		defer close(run.done)
		defer run.stopWatch()
		if !run.isCancelled() {
			run.child.Followup(llm.NewUserMessage(prompt, llm.UserSource{}))
			if err := run.child.WhenIdle(driveCtx); err != nil {
				// 本地取消已经结清的话，静止等待被打断是预期之内的收摊；
				// 否则它是一次这条接缝表达不成停止原因的基础设施故障。
				if !run.isCancelled() {
					run.resultErr = err
					return
				}
			}
		}
		run.result = run.readResult()
	}()
	return run
}

// abort 结清本地取消，并把孩子那个回合停掉。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:157-160
func (r *inProcessRun) abort() {
	r.mutex.Lock()
	r.cancelled = true
	r.mutex.Unlock()
	r.child.Cancel(sessionlog.ParentCancel{}, agent.CancelOptions{})
}

// isCancelled 问本地取消结清了没有。
func (r *inProcessRun) isCancelled() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.cancelled
}

// ID 实现 [github.com/snight1983/ds-harness-go/feature/subagent.Run]。
func (r *inProcessRun) ID() sessionlog.SessionID { return r.id }

// LocalAgent 实现 [github.com/snight1983/ds-harness-go/feature/subagent.Run]。
func (r *inProcessRun) LocalAgent() agent.Agent { return r.child }

// Result 实现 [github.com/snight1983/ds-harness-go/feature/subagent.Run]：等这次运行结清，反复调交出
// 同一份结果。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:186（`result` 字段）
func (r *inProcessRun) Result(ctx context.Context) (subagent.Result, error) {
	select {
	case <-r.done:
		return r.result, r.resultErr
	case <-ctx.Done():
		return subagent.Result{}, ctx.Err()
	}
}

// Dispose 实现 [github.com/snight1983/ds-harness-go/feature/subagent.Run]：摘掉取消守望、结清本地取消、
// 放掉那份公布出来的句柄，然后等结果那一路也静下来。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:187-194
//
// 运行故障归结果那条通道，所以这里只报「放不掉那份已公布的句柄」，而且是在两件事
// 都结清之后才报。
func (r *inProcessRun) Dispose(ctx context.Context) error {
	r.disposeOnce.Do(func() {
		r.stopWatch()
		r.mutex.Lock()
		r.cancelled = true
		r.mutex.Unlock()
		r.disposeErr = r.handle.Dispose(ctx)
		<-r.done
	})
	return r.disposeErr
}

// readResult 从活化边界之后那些事件里读出一个已结清孩子的结果。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:197-232
//
// [github.com/snight1983/ds-harness-go/harness/agent.ConsumedWork.DroppedUnrun] **有意**不读：一条一次性
// 提示词几乎立刻就被它那个被等着的第一个回合认领掉了，而主人自己那次拆解走的是
// 下面那个 cancelled。一次连交代回合都没有的取消，经 toStopReason 落在 StopError
// 上——它永远不会把结果说得比实情好。
func (r *inProcessRun) readResult() subagent.Result {
	own := r.child.Session().Events()
	if r.boundary < len(own) {
		own = own[r.boundary:]
	} else {
		own = nil
	}
	work := agent.FoldConsumedWork(own)
	// 这条接缝那条权威的选法：一份残缺的回答也熬得过取消和截断。
	output := subagent.FinalAssistantOutput(own)
	recorded := toStopReason(work)
	cancelled := r.isCancelled()
	// 处置可能赶在循环记下它那条普通的 aborted 收尾之前就把主人拆了。
	stopReason := recorded
	if cancelled && recorded != subagent.StopCompleted {
		stopReason = subagent.StopAborted
	}
	if r.structured != nil {
		if value, ok := r.structured.Captured(); ok {
			return subagent.Result{Output: output, Structured: value, StopReason: stopReason}
		}
		if stopReason == subagent.StopCompleted {
			// 跑完了却没留下一份合法的捕获：这次运行没有兑现它被要求的那份契约。
			if cancelled {
				return subagent.Result{Output: output, StopReason: subagent.StopAborted}
			}
			return subagent.Result{Output: output, StopReason: subagent.StopError}
		}
	}
	return subagent.Result{Output: output, StopReason: stopReason}
}
