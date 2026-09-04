// 本文件的作用：一个回合和它里面每一步怎么走——什么时候接着走、什么时候停，
// 以及一次被打断的流怎么收尾。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515

package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"runtime/debug"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// turnExit 是一个回合正文是怎么收场的。
//
// 新增: DSH 用 `return false` 和 `break` 两种控制流表达这件事——前者跳过回合尾巴、
// 驱动收工，后者走尾巴、可能再开一个回合。Go 里正文被拆成了单独一个函数
// （因为 turn/end 那次追加要在它之后无条件发生），控制流跨不出去，所以变成一个返回值。
type turnExit int

const (
	// turnExitStop 表示这个回合之后驱动就收工。
	turnExitStop turnExit = iota
	// turnExitTail 表示走回合尾巴，队列里还有活儿就再开一个回合。
	turnExitTail
)

// turn 开一个回合，跑完它，然后交出「还要不要再开一个」。
//
// 源: packages/core/agent-loop/src/agent.ts:245-330
func (a *ReactLoopAgent) turn() (bool, error) {
	a.mutex.Lock()
	if a.phase.kind != phaseRunning {
		a.mutex.Unlock()
		err := fmt.Errorf("harness/agentloop: agent %q：没有驱动预约就开回合", string(a.id))
		a.reportError(err)
		return false, err
	}
	// 每个回合各读一次：回合尾巴会换掉这一相里的 ctx，下一个回合等的是新的那个。
	ctx := a.phase.ctx
	turn := a.phase.turn + 1
	a.mutex.Unlock()

	if err := abortedErr(ctx); err != nil {
		return false, err
	}
	if _, err := a.appendEvent(sessionlog.TurnStartData{Turn: turn}, nil, nil); err != nil {
		a.reportError(err)
		return false, err
	}
	a.mutex.Lock()
	a.phase.turn = turn
	a.mutex.Unlock()

	reason, exit, bodyErr := a.runTurnBody(ctx, turn)

	// turn/end 无条件写：一个开了却没关的回合，读日志的人看不出它是怎么收场的。
	//
	// 新增: DSH 这一句在 finally 里，追加失败时那条新错误会**顶掉**正文那条
	//（JS 的 finally 抛出会丢掉在飞的异常）。这里两条都保住：追加失败照样报出去，
	// 但只有在正文没出错时才成为交出去的那一条。根因比收尾的次生故障重要。
	if _, err := a.appendEvent(sessionlog.TurnEndData{Turn: turn, Reason: reason}, nil, nil); err != nil {
		a.reportError(err)
		if bodyErr == nil {
			bodyErr = err
		}
	}
	if bodyErr != nil || exit == turnExitStop {
		return false, bodyErr
	}

	a.mutex.Lock()
	hasPending := a.inbox.HasPending()
	a.mutex.Unlock()
	if !hasPending {
		return false, nil
	}

	// 换一个全新的取消根：上一个上过的膛就此作废——活着的驱动自己去认领队列。
	nextCtx, cancel := a.newActivityContext()
	a.mutex.Lock()
	a.phase.ctx = nextCtx
	a.phase.cancel = cancel
	a.phase.wakeRequested = false
	a.phase.step = 0
	a.mutex.Unlock()
	return true, nil
}

// runTurnBody 跑回合正文，把正文里冒出来的 panic 收成一条普通的失败。
//
// 新增: 上游是 TypeScript，那边一个抛出去的异常会被 agent 边界的 try/catch 接住，
// 于是「正文炸了」和「正文返回了一条错误」本来就是同一条路。Go 里 panic 沿栈往上冲，
// 而驱动跑在自己那个 goroutine 上（见 [ReactLoopAgent.wakeDriver]）——没人接就是
// **整个进程**没，不是这一个会话没。一个嵌在长期运行的服务里的组件不能这样：
// 一个客户端手上的坏存档不该掀掉所有其他用户的会话。
//
// 兜在这一层而不是 goroutine 根上，是为了让 turn/end 照样写得下去。从这里交出去的
// 错误走的是和别的正文失败完全相同的那条路：写 turn/end、报一次 agent/error、
// 驱动收工、相回到 idle。兜在根上的话这个回合会在日志里永远开着，而读日志的人
// 看不出它是怎么收场的。
//
// 收场原因取 [github.com/snight1983/ds-harness-go/sessionlog.ErrorTurnEnd]，和
// turnBody 自己那个 fail 一模一样——对读日志的人来说「正文报了错」和「正文炸了」
// 是同一件事，两者都是这个回合没走完。
//
// 报错不走 [ReactLoopAgent.reportError]，因为它要拿 [ReactLoopAgent.mutex]，
// 而 panic 有可能正是在持锁的那一小段里发生的，那时候再去要一次就是死锁。
// 直接找注册表报，turn 是参数上现成的，step 交 0——精确到哪一步不值得拿一次
// 可能永远等下去的加锁去换。
//
// 兜不住的只剩一种：panic 恰好发生在本 agent 持着自己那把锁的时候。那几段都是
// 纯字段读写加一次收件箱认领，炸的可能性极低；真炸了这个 agent 从此卡住，但
// **进程还活着**，别人的会话照跑。
func (a *ReactLoopAgent) runTurnBody(
	ctx context.Context,
	turn int,
) (reason sessionlog.TurnEndReason, exit turnExit, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf(
			"harness/agentloop: agent %q 的第 %d 个回合正文 panic 了：%v\n%s",
			string(a.id), turn, recovered, debug.Stack(),
		)
		reason = sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}
		exit = turnExitStop
		_ = a.deps.Agents.ReportError(agent.TurnError{Agent: a, Turn: turn, Err: err})
	}()
	return a.turnBody(ctx, turn)
}

// turnBody 跑一个已经开了的回合里那一串步骤，交出这个回合的收场原因。
//
// 源: packages/core/agent-loop/src/agent.ts:262-315
func (a *ReactLoopAgent) turnBody(
	ctx context.Context,
	turn int,
) (sessionlog.TurnEndReason, turnExit, error) {
	var turnEnds sessionlog.TurnEndReason
	target := agent.NextTurn

	// fail 把一条冒出来的错误折成这个回合的收场原因。取消**不报** agent/error：
	// 它不是故障，而且它已经作为 aborted 写进 turn/end 了。
	fail := func(err error) (sessionlog.TurnEndReason, turnExit, error) {
		if ctx.Err() != nil {
			if cause, ok := cancelCauseOf(ctx); ok {
				return sessionlog.AbortedTurnEnd{Reason: cause}, turnExitStop, err
			}
			// 走不到：驱动那个 ctx 的父节点不可取消，唯一关得掉它的是
			// [ReactLoopAgent.Cancel]，而它一定把原因包成 *CancelError 带上。
			// 真落到这儿也不能伪造一个原因——sessionlog.LegacyCancel 是给读旧日志
			// 用的，循环一个字都不许往日志里写它。
			return sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}, turnExitStop, err
		}
		a.reportError(err)
		// 每一次失败都是结构化的：一条 *llm.Error 留着它自己那份事实，别的都摊成
		// UNKNOWN 码加一句文本。
		return sessionlog.ErrorTurnEnd{Error: llm.NormalizeFailure(err)}, turnExitStop, err
	}

	for {
		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}

		a.mutex.Lock()
		priorStep := a.phase.step
		a.mutex.Unlock()
		step := priorStep + 1

		decision, assembly, err := a.preStep(ctx, target, turn, step)
		if err != nil {
			return fail(err)
		}
		if !decision.Enter {
			return sessionlog.BlockedTurnEnd{}, turnExitStop, nil
		}
		if turnEnds != nil && len(decision.Messages) == 0 {
			return turnEnds, turnExitTail, nil
		}
		// 一条被撤走的唤醒消息、或者一个被改写成空的准入决定，照样拥有这个回合的
		// 开场边界，只是它不花一次模型调用。
		if priorStep == 0 && len(decision.Messages) == 0 {
			return sessionlog.CompletedTurnEnd{}, turnExitStop, nil
		}

		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}
		if _, err := a.appendEvent(sessionlog.StepStartData{Turn: turn, Step: step}, nil, nil); err != nil {
			return fail(err)
		}
		a.mutex.Lock()
		a.phase.step = step
		a.mutex.Unlock()

		stepEnd, err := a.runStep(ctx, turn, step, decision.Messages, assembly)
		if err != nil {
			return fail(err)
		}
		// max-tokens 是粘的：一旦有哪个步骤撞了上限，后面正常收场的步骤不许把这个
		// 回合的结论降回去。
		if turnEnds == nil || turnEnds.TurnEndReasonKind() != sessionlog.ReasonMaxTokens {
			turnEnds = stepEnd
		}

		if err := abortedErr(ctx); err != nil {
			return fail(err)
		}
		if turnEnds != nil && a.nextStepEmpty() {
			if err := a.deps.Agents.TurnStopping(ctx, a, turn); err != nil {
				return fail(err)
			}
			if err := abortedErr(ctx); err != nil {
				return fail(err)
			}
		}
		// 再问一遍：收尾观察者可能刚刚往下一个步骤里放了东西。
		if turnEnds != nil && a.nextStepEmpty() {
			return turnEnds, turnExitTail, nil
		}
		target = agent.NextStep
	}
}

// nextStepEmpty 判「下一个步骤那条队列是不是空的」。
func (a *ReactLoopAgent) nextStepEmpty() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return len(a.inbox.NextStep()) == 0
}

// preStep 认领一批消息、装配一次提示词，然后过一遍步骤准入那条瀑布。
//
// 源: packages/core/agent-loop/src/agent.ts:225-243
func (a *ReactLoopAgent) preStep(
	ctx context.Context,
	target agent.InboxTarget,
	turn, step int,
) (agent.PreStepDecision, systemprompt.PromptAssembly, error) {
	var empty systemprompt.PromptAssembly

	a.mutex.Lock()
	if a.phase.kind != phaseRunning {
		a.mutex.Unlock()
		// 走不到：本类型里叫它的地方都先立好了 running 这一相。
		return agent.PreStepDecision{}, empty,
			fmt.Errorf("harness/agentloop: agent %q：在 running 之外提议步骤", string(a.id))
	}
	claimed, err := a.inbox.Claim(target, turn)
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 认领收件箱失败：%w", err)
	}

	assembly, err := a.deps.SystemPrompt.Assemble(ctx, systemprompt.AssembleContext{Scope: a.scope.Key()})
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 装配系统提示词失败：%w", err)
	}
	if err := abortedErr(ctx); err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	sections, err := systemprompt.RenderContextSections(assembly)
	if err != nil {
		return agent.PreStepDecision{}, empty, fmt.Errorf("harness/agentloop: 渲染运行期上下文失败：%w", err)
	}
	snapshot, hasSnapshot := a.runtimeContext.Project(systemprompt.JoinContextSections(sections), sections)

	decision, err := a.deps.Agents.ResolvePreStep(
		ctx,
		agent.PreStep{Agent: a, Messages: claimed, Turn: turn, Step: step},
		func(context.Context) (agent.PreStepDecision, error) {
			// 另起一份切片：claimed 的底层数组可能还有富余容量，直接 append 会把
			// 那块内存写花，而收件箱那一侧还留着同一个数组的别名。
			messages := make([]llm.Message, 0, len(claimed)+1)
			messages = append(messages, claimed...)
			if hasSnapshot {
				messages = append(messages, snapshot)
			}
			return agent.EnterStep(messages), nil
		},
	)
	if err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	if err := abortedErr(ctx); err != nil {
		return agent.PreStepDecision{}, empty, err
	}
	return decision, assembly, nil
}

// runStep 把这一步认领到的消息落进日志，跑完这一步，然后无条件写下 step/end。
//
// 源: packages/core/agent-loop/src/agent.ts:281-293
func (a *ReactLoopAgent) runStep(
	ctx context.Context,
	turn, step int,
	messages []llm.Message,
	assembly systemprompt.PromptAssembly,
) (reason sessionlog.TurnEndReason, err error) {
	// 新增: 和 turn/end 那一处同样的取舍——追加失败照样报出去，但只有正文没出错时
	// 才成为交出去的那一条。DSH 在 finally 里抛，会顶掉根因。
	defer func() {
		if _, appendErr := a.appendEvent(sessionlog.StepEndData{Turn: turn, Step: step}, nil, nil); appendErr != nil {
			a.reportError(appendErr)
			if err == nil {
				err = appendErr
			}
		}
	}()

	for _, message := range messages {
		if _, appendErr := a.appendEvent(sessionlog.UserMessageData{Message: message}, sessionlog.AppendOp{}, nil); appendErr != nil {
			return nil, appendErr
		}
	}
	return a.step(ctx, turn, step, assembly)
}

// step 发一次模型请求，把它的输出落进日志，并派发它要求的那些工具调用。
//
// 源: packages/core/agent-loop/src/agent.ts:332-420
//
// 交出这一步给回合的收场原因；为 nil 表示这个回合还没完（工具跑过了，接着下一步）。
func (a *ReactLoopAgent) step(
	ctx context.Context,
	turn, step int,
	assembly systemprompt.PromptAssembly,
) (sessionlog.TurnEndReason, error) {
	if err := abortedErr(ctx); err != nil {
		return nil, err
	}
	system, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		return nil, fmt.Errorf("harness/agentloop: 渲染系统提示词失败：%w", err)
	}

	for {
		// 每次重试都重新推导一遍：上一次尝试可能已经往日志上留下了痕迹。
		boundaryMessages, err := a.session.DeriveMessages()
		if err != nil {
			return nil, fmt.Errorf("harness/agentloop: 推导边界消息失败：%w", err)
		}
		request, prepared, err := a.buildRequest(ctx, turn, step, assembly.Tools, system, boundaryMessages)
		if err != nil {
			return nil, err
		}

		assembler := llm.NewBlockAssembler()
		var chunkSeqs []int
		streamErr := a.consumeStream(ctx, request, prepared, turn, step, assembler, &chunkSeqs)
		if streamErr != nil {
			// 被打断时把已经吐出来的那个安全前缀定稿，重放才读得通。
			if ctx.Err() != nil {
				if err := a.appendInterrupted(turn, step, request, assembler, chunkSeqs); err != nil {
					return nil, errors.Join(streamErr, err)
				}
			}
			return nil, streamErr
		}

		var failure llm.Failure
		terminal := false
		switch finished := assembler.Finish().(type) {
		case llm.ErrorFinish:
			failure, terminal = finished.Failure, true
		case llm.AbortedFinish:
			failure, terminal = finished.Failure, true
		}
		if terminal {
			retryPolicy, hasRetryPolicy := llm.ResolvedRetryPolicy{}, false
			if prepared != nil {
				retryPolicy, hasRetryPolicy = prepared.RetryPolicy(), true
			}
			action, err := a.deps.Agents.ResolveRequestError(
				ctx,
				agent.RequestFailure{
					Agent: a, Turn: turn, Step: step,
					Provider:       request.Provider,
					Failure:        failure,
					RetryPolicy:    retryPolicy,
					HasRetryPolicy: hasRetryPolicy,
				},
				func(context.Context) (agent.RequestErrorAction, error) {
					return agent.RequestErrorAction{}, nil
				},
			)
			if err != nil {
				return nil, err
			}
			if err := abortedErr(ctx); err != nil {
				return nil, err
			}
			if !action.Retry {
				// 直接造结构体而不是走 [llm.NewError]：那个构造器只收得下 message
				// 和 code，而这份事实里的 status、providerRetryAfterMs、requestId
				// 是上层路由要用的。DSH 那边 new LlmError(msg, code, finish.failure)
				// 的第三个参数就是把整份事实原样带上。
				return nil, &llm.Error{Failure: failure}
			}
			continue
		}

		message, err := assembledMessage(request, assembler)
		if err != nil {
			return nil, err
		}
		data := sessionlog.AssistantMessageData{Turn: turn, Step: step, Message: message}
		if usage, ok := assembler.Usage(); ok {
			data.Usage = &usage
		}
		if _, err := a.appendEvent(data, sessionlog.AppendOp{}, chunkSeqs); err != nil {
			return nil, err
		}
		if _, hitCeiling := assembler.Finish().(llm.MaxTokensFinish); hitCeiling {
			return sessionlog.MaxTokensTurnEnd{}, nil
		}

		var toolCalls []llm.ToolCallBlock
		for _, block := range message.Content {
			if call, ok := block.(llm.ToolCallBlock); ok {
				toolCalls = append(toolCalls, call)
			}
		}
		if len(toolCalls) == 0 {
			return sessionlog.CompletedTurnEnd{}, nil
		}
		concluded, err := ExecuteToolCalls(
			ctx, a.deps.Tools, a.maxParallelToolCalls(), turn, step, toolCalls, a.acceptToolContext,
		)
		if err != nil {
			return nil, err
		}
		if concluded {
			return sessionlog.CompletedTurnEnd{}, nil
		}
		return nil, nil
	}
}

// consumeStream 把一次请求的流吃完：每个分块先落日志再喂装配器。
//
// 源: packages/core/agent-loop/src/agent.ts:345-353
//
// 先落日志后喂装配器是有意的：日志是权威的，一条没记下来的分块等于没发生过，
// 而一个吃了它的装配器会装出一条日志重放不出来的消息。
func (a *ReactLoopAgent) consumeStream(
	ctx context.Context,
	request llm.GenerateOptions,
	prepared *llm.PreparedCall,
	turn, step int,
	assembler *llm.BlockAssembler,
	chunkSeqs *[]int,
) error {
	var chunks iter.Seq2[llm.StreamChunk, error]
	var err error
	if prepared != nil {
		chunks, err = prepared.Stream(ctx, request)
	} else {
		// 中间件可以服务一条没登记的路由，但终端派发照样要一个适配器。
		chunks, err = a.deps.LLM.Stream(ctx, request)
	}
	if err != nil {
		return err
	}
	if err := abortedErr(ctx); err != nil {
		return err
	}
	for chunk, err := range chunks {
		if err != nil {
			return err
		}
		if err := abortedErr(ctx); err != nil {
			return err
		}
		event, appendErr := a.appendEvent(
			sessionlog.AssistantChunkData{Turn: turn, Step: step, Chunk: chunk}, nil, nil)
		if appendErr != nil {
			return appendErr
		}
		*chunkSeqs = append(*chunkSeqs, event.Seq)
		assembler.Push(chunk)
	}
	return abortedErr(ctx)
}

// appendInterrupted 把一条被打断的流那个可以安全定稿的前缀写进日志。
//
// 源: packages/core/agent-loop/src/agent.ts:355-369
//
// 一个字都没吐出来时什么都不写：一条空的助手消息在重放里是凭空多出来的一轮。
func (a *ReactLoopAgent) appendInterrupted(
	turn, step int,
	request llm.GenerateOptions,
	assembler *llm.BlockAssembler,
	chunkSeqs []int,
) error {
	content := assembler.InterruptedBlocks()
	if len(content) == 0 {
		return nil
	}
	data := sessionlog.AssistantMessageData{
		Turn: turn,
		Step: step,
		Message: llm.NewAssistantMessage(
			content, llm.Provenance{Provider: request.Provider, Model: request.Model}),
		Interrupted: true,
	}
	if usage, ok := assembler.Usage(); ok {
		data.Usage = &usage
	}
	_, err := a.appendEvent(data, sessionlog.AppendOp{}, chunkSeqs)
	return err
}

// assembledMessage 把装配器攒出来的那些块定成一条署了来路的助手消息。
//
// 源: packages/core/agent-loop/src/agent.ts:392-399
func assembledMessage(request llm.GenerateOptions, assembler *llm.BlockAssembler) (llm.Message, error) {
	blocks, err := assembler.Blocks()
	if err != nil {
		return llm.Message{}, fmt.Errorf("harness/agentloop: 装配助手消息失败：%w", err)
	}
	provenance := llm.Provenance{Provider: request.Provider, Model: request.Model}
	envelope, hasReplay, err := assembler.ReplayState()
	if err != nil {
		return llm.Message{}, fmt.Errorf("harness/agentloop: 取重放状态失败：%w", err)
	}
	if hasReplay {
		// 新增: DSH 那边 replayState 是一个原样透传的 JS 值。Go 里
		// [llm.Provenance.ReplayState] 是一段 JSON 字节，所以这里排一次。
		raw, err := json.Marshal(envelope)
		if err != nil {
			return llm.Message{}, fmt.Errorf("harness/agentloop: 排重放状态失败：%w", err)
		}
		provenance.ReplayState = raw
	}
	return llm.NewAssistantMessage(blocks, provenance), nil
}
