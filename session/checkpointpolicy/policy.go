// 本文件的作用：这一层的全部内容——把三条检查点规则装上去，以及它们各自在
// 刷不下去时怎么关门。
//
// 源: packages/session/session-checkpoint-policy/src/index.ts:63-83

package checkpointpolicy

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// Install 把三条检查点规则一次装齐，返回把它们一起摘下来的函数。
//
// 源: packages/session/session-checkpoint-policy/src/index.ts:63-83
//
// owner 决定这三条管哪些 agent，规矩和本仓库别处一样：[scope.NewRoot] 造出来的
// 作用域没有身份，落全局层管所有人；有身份的只管那条链下面的。
//
// 中途装不上就把已经装上的按反序摘干净再报错。半装上去比装不上更坏：那意味着
// 有的边界有检查点、有的没有，而调用方拿到的是一个错误，多半不会去猜哪几条还留着。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	sessions *session.Store,
	runtime *llm.Runtime,
	toolRuntime *tools.Runtime,
	agents *agent.Registry,
) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, errors.New("checkpointpolicy: 需要一个持有这三条登记的作用域")
	case sessions == nil:
		return nil, errors.New("checkpointpolicy: 需要一个会话存储")
	case runtime == nil:
		return nil, errors.New("checkpointpolicy: 需要一个 llm 运行时")
	case toolRuntime == nil:
		return nil, errors.New("checkpointpolicy: 需要一个工具运行时")
	case agents == nil:
		return nil, errors.New("checkpointpolicy: 需要一个 agent 注册表")
	}

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		var failures []error
		// 反序摘：装的时候后来的在里层，摘的时候先摘里层。
		for index := len(installed) - 1; index >= 0; index-- {
			if err := installed[index](undoCtx); err != nil {
				failures = append(failures, err)
			}
		}
		installed = nil
		return errors.Join(failures...)
	}

	for _, step := range []struct {
		what    string
		install func() (func(context.Context) error, error)
	}{
		{"模型请求", func() (func(context.Context) error, error) {
			return runtime.OnStream(ctx, owner, streamRule(sessions))
		}},
		{"顶层工具调用", func() (func(context.Context) error, error) {
			return toolRuntime.AroundDispatch(ctx, owner, dispatchRule(sessions))
		}},
		{"步骤边界", func() (func(context.Context) error, error) {
			return agents.OnPreStep(ctx, owner, preStepRule(sessions))
		}},
	} {
		dispose, err := step.install()
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("checkpointpolicy: 装「%s」这道检查点失败：%w", step.what, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}

// streamRule 是模型请求派发前那一道。
//
// 源: packages/session/session-checkpoint-policy/src/index.ts:64-68
//
// 认不出会话就原样放过，这有两种情形：一次不属于任何会话的辅助调用（起标题、
// 压缩摘要都是），和一个已经不在存储里活着的 id。两种都没有日志可刷。
func streamRule(sessions *session.Store) llm.StreamRule {
	return func(
		streamCtx context.Context,
		options llm.GenerateOptions,
		next func(context.Context) (iter.Seq2[llm.StreamChunk, error], error),
	) (iter.Seq2[llm.StreamChunk, error], error) {
		if options.SessionID == "" {
			return next(streamCtx)
		}
		sess, ok := sessions.Get(sessionlog.SessionID(options.SessionID))
		if !ok {
			return next(streamCtx)
		}
		if _, err := sessions.Flush(streamCtx, sess); err != nil {
			return nil, fmt.Errorf("checkpointpolicy: 派发前的检查点没过，这次模型请求不发：%w", err)
		}
		return next(streamCtx)
	}
}

// dispatchRule 是顶层工具调用派发前那一道。
//
// 源: packages/session/session-checkpoint-policy/src/index.ts:70-75
func dispatchRule(sessions *session.Store) tools.DispatchRule {
	return func(
		dispatchCtx context.Context,
		exec tools.Execution,
		next func(context.Context) (tools.Result, error),
	) (tools.Result, error) {
		if !exec.Parent.IsZero() {
			return next(dispatchCtx)
		}
		initiator, ok := agent.CurrentInitiator(dispatchCtx)
		if !ok {
			return next(dispatchCtx)
		}
		if _, err := sessions.Flush(dispatchCtx, initiator.Session()); err != nil {
			return tools.Result{}, fmt.Errorf(
				"checkpointpolicy: 派发前的检查点没过，这次工具调用不跑：%w", err)
		}
		// 刷盘要等真正落盘，一次取消完全可能正好落在这段等待里。这里明确再问一次
		// 而不是把它交给 next：里层可能还登记着别的绕派发规则，它们各自都有副作用，
		// 而这一层承诺的是「取消之后工具体一次都不起步」。
		if dispatchCtx.Err() != nil {
			return tools.AbortedBeforeDispatchResult(), nil
		}
		return next(dispatchCtx)
	}
}

// preStepRule 是每个步骤开始前那一道，刷的是上一步提交的那一批。
//
// 源: packages/session/session-checkpoint-policy/src/index.ts:77-82
func preStepRule(sessions *session.Store) agent.PreStepObserver {
	return func(
		stepCtx context.Context,
		step agent.PreStep,
		next func(context.Context) (agent.PreStepDecision, error),
	) (agent.PreStepDecision, error) {
		if _, err := sessions.Flush(stepCtx, step.Agent.Session()); err != nil {
			// 零值就是拒绝，见 [agent.PreStepDecision]。刷不下去而让这一步照进，
			// 等于在一份已经不可信的日志上再往下写。
			return agent.PreStepDecision{}, fmt.Errorf(
				"checkpointpolicy: 步骤边界的检查点没过，这一步不进：%w", err)
		}
		return next(stepCtx)
	}
}
