// 本文件的作用：调度一个助手步骤请求的那些工具调用。独占调用形成次序屏障；
// 并行调用走一个有上限的滚动池，并且在起步之前**重新**判一次并发方式。
// 派发可以重叠，但策略、结果、以及结果捎回来的上下文一律按模型给的次序落地。
//
// 取消会替那些没轮到的调用补上合成的错误结果，好让重放仍然读得通。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:1-289

package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// plannedCall 是一次解析完参数、可以排期的工具调用。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:19-23
type plannedCall struct {
	// block 是模型产出的那一块，落日志用的是它。
	block llm.ToolCallBlock
	// exec 是交给工具运行时的那份输入。
	exec tools.ExecutionInput
}

// toolSlot 是一次已经 settle、等着按模型次序定稿的派发。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:25-30
type toolSlot struct {
	// exec 是本次调用的执行对象，后面每一段都要把它原样传回去。
	exec *tools.RunContext
	// result 是这次派发交出来的、还没定稿的结果。
	result tools.Result
	// needsPost 表示它还要过一遍执行后瀑布。
	needsPost bool
}

// groupOutcome 是一组调度的结果，包括被取消之后排空的那种。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:32-38
type groupOutcome struct {
	// consumed 是这一组吃掉了排期表上多少次调用。
	consumed int
	// aborted 表示这一组是被取消收场的。
	aborted bool
	// concluded 表示有一份已提交的结果宣布了「这个回合到此为止」。
	concluded bool
}

// toolBatch 是一个步骤那一批工具调用共用的东西。
//
// 新增: DSH 这些值来自 cordis 的 ctx（ctx.tools、ctx.agentLoop.config、
// ctx.agents.requireInitiator().session）。Go 里没有那个万能容器，所以它们是
// 显式的字段，由 [ExecuteToolCalls] 在入口处一次性解出来。
type toolBatch struct {
	// tools 是派发这些调用的工具运行时。
	tools *tools.Runtime
	// session 是这些调用和结果落进去的那份日志。
	session *session.Session
	// maxParallel 是并行池的上限。
	maxParallel int
	// turn、step 是这一批所属的位置。
	turn, step int
	// acceptContext 收下已提交结果捎回来的上下文，交给下一个步骤边界。
	acceptContext func(context llm.Message)
}

// ExecuteToolCalls 按每次调用**当下**的并发方式，调度一个助手步骤请求的那些工具调用。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:59-101
//
// 正常收场和被取消收场都会按模型次序提交那些已经起步的调用。取消还会把它们的
// 上下文交给 acceptContext（循环把它挪进下一步的收件箱，在步骤边界上生效），
// 替没起步的调用补上合成结果，然后带着「ctx 仍然是被取消的」返回。
//
// 第一个返回值为真表示有一份结果宣布了回合结束。
//
// 新增: DSH 那边还有一整套 schedulerFailure 机制——派发的 promise 会 reject，
// 于是要留住第一条失败、停止续杯、把已经起步的排空、最后把它抛出去。Go 这边
// [github.com/snight1983/ds-harness-go/core/tools.Runtime] 的 Prepare／Dispatch／Finalize／Finish
// **一个都不返回 error**（那个包的立场是「一切失败都是结果，不是错误」），
// 所以那套机制在这里唯一还剩的触发源是**往日志上追加失败**。它仍然按同样的
// 办法处理：停止续杯、排空在飞的、把错误交出去、不伪造任何结果。
func ExecuteToolCalls(
	ctx context.Context,
	runtime *tools.Runtime,
	maxParallel int,
	turn, step int,
	toolCalls []llm.ToolCallBlock,
	acceptContext func(context llm.Message),
) (bool, error) {
	if runtime == nil || acceptContext == nil {
		return false, errors.New("core/agentloop: 调度工具调用要有工具运行时和一个上下文接收方")
	}
	initiator, err := agent.RequireInitiator(ctx)
	if err != nil {
		return false, fmt.Errorf("core/agentloop: 调度工具调用时读不出发起者：%w", err)
	}
	batch := &toolBatch{
		tools:         runtime,
		session:       initiator.Session(),
		maxParallel:   max(maxParallel, 1),
		turn:          turn,
		step:          step,
		acceptContext: acceptContext,
	}

	// 每次调用各自一份输入：绕派发的包装函数可能把 exec 换掉，共用一份会串味。
	planned := make([]plannedCall, 0, len(toolCalls))
	for _, block := range toolCalls {
		planned = append(planned, plannedCall{
			block: block,
			exec: tools.ExecutionInput{
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: parseToolArguments(block.Arguments),
				Agent:     initiator.Scope().Key(),
			},
		})
	}

	next := 0
	concluded := false
	for next < len(planned) {
		// 先提交、再重新判一次方式：这中间注册表可能变了，而那要影响的正是
		// 还没起步的那些调用。
		first := planned[next]
		mode := runtime.ExecutionMode(first.exec)
		group := []plannedCall{first}
		if mode == tools.ModeParallel {
			group = planned[next:]
		}

		outcome, err := batch.runGroup(ctx, group, mode)
		if err != nil {
			return concluded, err
		}
		next += outcome.consumed
		concluded = concluded || outcome.concluded
		if outcome.aborted {
			for _, call := range planned[next:] {
				if err := batch.appendSkippedToolCall(call.block); err != nil {
					return concluded, err
				}
			}
			return concluded, nil
		}
	}
	return concluded, nil
}

// parseToolArguments 解析模型写出来的参数：空的当成 `{}`，解不动的原样留成文本。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:103-110
//
// 新增: DSH 交出去的是一个 `unknown`，解不动时就是那个原始字符串本身。Go 这一路
// 的参数类型是 [encoding/json.RawMessage]，必须是**合法 JSON**，所以解不动的那串
// 被包成一个 JSON 字符串字面量。可观察的行为一样：它是一个非对象的值，工具的
// 参数 schema 会拒收它，而那句诊断里照样看得见模型原本写了什么。
func parseToolArguments(raw string) json.RawMessage {
	if raw == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	quoted, err := json.Marshal(raw)
	if err != nil {
		// 走不到：排一个 Go 字符串不会失败。
		return json.RawMessage(`{}`)
	}
	return quoted
}

// runGroup 跑一道独占屏障，或者一个并行池。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:121-246
//
// 排在后面的调用在起步之前重新判一次方式；一次判成独占的会让当前这个池排空，
// 并留给调用方的下一道屏障。结果和上下文按模型次序提交。取消会停止起步、
// 把已经起步的排空并提交、把它们的上下文交给这一批、替被跳过的补上结果，
// 然后交出一个 aborted 的结果。
func (b *toolBatch) runGroup(
	ctx context.Context,
	group []plannedCall,
	mode tools.ExecutionModeKind,
) (groupOutcome, error) {
	slots := make([]toolSlot, len(group))
	// ready 记着哪些槽位已经可以读了。它只由**本 goroutine** 读写：一格只有在它的
	// 下标从 settled 上**收到**之后才置位。派发那边写完 slots[index] 才发这个下标，
	// 所以「收到过」就是读那一格的许可。早一步去看 slots 本身是一次真实的竞态。
	ready := make([]bool, len(group))
	// 起步过的槽位记着自己那条 tool/call 的 seq，好让结果引用得到它。
	callSeqs := make([]int, len(group))
	nextToStart, committed, started := 0, 0, 0
	inFlight := 0
	aborted := ctx.Err() != nil
	concluded := false

	// settled 是在飞的派发报「我这一格填好了」的地方。缓冲开到整组那么大，
	// 所以排空阶段之前那些 goroutine 一个都不会挂在发送上。
	settled := make(chan int, len(group))
	// drain 等在飞的那些收敛。出错要走它，理由和 DSH 一样：本包派出去的活儿
	// 不能丢下不管——它们还在写 slots，而这个函数一返回那些槽位就没人看了。
	drain := func() {
		for ; inFlight > 0; inFlight-- {
			<-settled
		}
	}

	// commitReady 只沿着**连续**的模型次序往前推 committed。
	commitReady := func() error {
		for committed < len(group) {
			if !ready[committed] {
				break
			}
			slot := slots[committed]
			result := slot.result
			if slot.needsPost {
				result = b.tools.Finalize(ctx, slot.exec, result)
			} else {
				result = b.tools.Finish(ctx, slot.exec, result)
			}
			if err := b.appendToolResult(group[committed].block, result, callSeqs[committed]); err != nil {
				return err
			}
			for _, context := range result.AdditionalContexts {
				b.acceptContext(context)
			}
			concluded = concluded || result.ConcludesTurn
			committed++
		}
		return nil
	}

	startCall := func(index int) error {
		call := group[index]
		seq, err := b.appendToolCall(call.block)
		if err != nil {
			return err
		}
		callSeqs[index] = seq
		started++

		prepared := b.tools.Prepare(ctx, call.exec)
		switch prepared.Kind {
		case tools.StageDispatch:
			inFlight++
			go func() {
				dispatched := b.tools.Dispatch(ctx, prepared.Exec)
				// 每个 goroutine 只写自己那一格，然后**才**把下标发出去。读的一方
				// 一律等收到下标再看那一格，这次收发就是那道 happens-before。
				// 不收就看的话，这里的写和那边的读同时发生——那是一次真实的竞态。
				slots[index] = toolSlot{
					exec:      prepared.Exec,
					result:    dispatched.Result,
					needsPost: dispatched.Kind == tools.StagePostResult,
				}
				settled <- index
			}()
		case tools.StagePostResult:
			slots[index] = toolSlot{exec: prepared.Exec, result: prepared.Result, needsPost: true}
			ready[index] = true
		default:
			slots[index] = toolSlot{exec: prepared.Exec, result: prepared.Result, needsPost: false}
			ready[index] = true
		}
		return nil
	}

	fillPool := func() error {
		for !aborted && nextToStart < len(group) && inFlight < b.maxParallel {
			// 每次按序提交之后重新读一遍后面那些调用的方式，好让注册表的变化
			// 能当场立起一道屏障。
			nextCall := group[nextToStart]
			if nextToStart > 0 && mode == tools.ModeParallel &&
				b.tools.ExecutionMode(nextCall.exec) != tools.ModeParallel {
				break
			}
			if err := startCall(nextToStart); err != nil {
				return err
			}
			nextToStart++
			if err := commitReady(); err != nil {
				return err
			}
			// 取消可能是在执行前那一段等待里到的。
			if ctx.Err() != nil {
				aborted = true
			}
		}
		return nil
	}

	// 有序的执行前那一段是会等的，只有派发和执行体才重叠。
	if err := fillPool(); err != nil {
		drain()
		return groupOutcome{}, err
	}
	for inFlight > 0 {
		ready[<-settled] = true
		inFlight--
		if err := commitReady(); err != nil {
			drain()
			return groupOutcome{}, err
		}
		// 取消可能是在某个工具或者某次有序提交等待的时候到的。
		if ctx.Err() != nil {
			aborted = true
		}
		if err := fillPool(); err != nil {
			drain()
			return groupOutcome{}, err
		}
	}

	if aborted {
		// 已经起步的调用和它们捎回来的上下文先落定，剩下的每一次模型调用
		// 再按次序拿到一份合成结果，然后这个回合才中止。
		for _, call := range group[started:] {
			if err := b.appendSkippedToolCall(call.block); err != nil {
				return groupOutcome{}, err
			}
		}
		return groupOutcome{consumed: len(group), aborted: true, concluded: concluded}, nil
	}
	if committed != started {
		// 走不到：一组没被取消的调度会把每一次起步过的调用都提交掉。
		return groupOutcome{}, errors.New("core/agentloop: 工具调度器有 settle 了却没提交的调用")
	}
	return groupOutcome{consumed: started, concluded: concluded}, nil
}

// appendSkippedToolCall 替一次取消之后被跳过的模型调用，把那对耐久的调用／结果补齐。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:248-259
//
// 补的不是装样子：派生历史要求每一次 tool/call 都配得上一份 tool/result，
// 少一份，这段日志喂回给模型时就是一次悬空的调用。
func (b *toolBatch) appendSkippedToolCall(block llm.ToolCallBlock) error {
	callSeq, err := b.appendToolCall(block)
	if err != nil {
		return err
	}
	return b.appendToolResult(block, tools.AbortedBeforeDispatchResult(), callSeq)
}

// appendToolCall 记下一次起步的调用，交出它的结果必须引用的那个 seq。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:261-265
func (b *toolBatch) appendToolCall(block llm.ToolCallBlock) (int, error) {
	payload, err := json.Marshal(sessionlog.ToolCallData{
		Turn:      b.turn,
		Step:      b.step,
		CallID:    block.ID,
		Name:      block.Name,
		Arguments: block.Arguments,
	})
	if err != nil {
		// 走不到：这个负载里只有整数和字符串。
		return 0, fmt.Errorf("core/agentloop: 排 tool/call 负载失败：%w", err)
	}
	event, err := b.session.Append(sessionlog.Event{Type: sessionlog.EventToolCall, Data: payload})
	if err != nil {
		return 0, fmt.Errorf("core/agentloop: 追加 tool/call 失败：%w", err)
	}
	return event.Seq, nil
}

// appendToolResult 记下一份按模型次序排好的结果，并把它和自己那次调用连起来。
//
// 源: packages/core/agent-loop/src/tool-calls.ts:267-289
func (b *toolBatch) appendToolResult(
	block llm.ToolCallBlock,
	result tools.Result,
	callSeq int,
) error {
	data := sessionlog.ToolResultData{
		Turn:    b.turn,
		Step:    b.step,
		Message: llm.NewToolResultMessage(block.ID, result.Content, result.IsError),
		// 工具私有的展示负载（比如一份结果期才算得出的 diff），存下来是为了
		// 重放时 UI 那一侧还原得出同一张卡片。
		Meta: result.Meta,
	}
	if result.Error != nil && result.Error.Info != nil {
		data.Error = &sessionlog.ToolError{Name: result.Error.Info.Name, Code: result.Error.Info.Code}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("core/agentloop: 排 tool/result 负载失败：%w", err)
	}
	if _, err := b.session.Append(sessionlog.Event{
		Type:            sessionlog.EventToolResult,
		Data:            payload,
		SurfaceOp:       sessionlog.AppendOp{},
		SourceEventSeqs: []int{callSeq},
	}); err != nil {
		return fmt.Errorf("core/agentloop: 追加 tool/result 失败：%w", err)
	}
	return nil
}
