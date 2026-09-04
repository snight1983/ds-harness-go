// 本文件的作用：一次模型请求怎么攒出来——并发上限、工具那份上下文、请求头，
// 以及请求头和模型上下文各自怎么折进当前状态。
//
// 源: packages/core/agent-loop/src/agent.ts:1-515

package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// maxParallelToolCalls 读出当下的并行池上限。
func (a *ReactLoopAgent) maxParallelToolCalls() int {
	if a.deps.MaxParallelToolCalls == nil {
		return DefaultMaxParallelToolCalls
	}
	return a.deps.MaxParallelToolCalls()
}

// acceptToolContext 收下一份已提交的工具结果捎回来的上下文，排到下一个步骤末尾。
//
// 源: packages/core/agent-loop/src/agent.ts:416
func (a *ReactLoopAgent) acceptToolContext(message llm.Message) {
	a.mutex.Lock()
	_, err := a.inbox.Splice(agent.NextStep, len(a.inbox.NextStep()), 0, []llm.Message{message})
	pending := a.takeNotifyLocked()
	a.mutex.Unlock()
	runNotifications(pending)
	if err != nil {
		a.reportError(fmt.Errorf("harness/agentloop: 收下工具捎回来的上下文失败：%w", err))
	}
}

// requestProposal 把适配器解析出来的那几个值摘掉，再让插件提议下一次请求的配置。
//
// 源: packages/core/agent-loop/src/agent.ts:54-61
//
// 不摘的话，一个适配器按确切模型填的默认值会在下一次请求里冒充成调用方的选择，
// 于是换了模型也换不掉它。
//
// 新增: DSH 开头那句 `if (header.adapterDefaults === undefined) return header.config`
// 在这里不需要——[sessionlog.EpochHeader.AdapterDefaults] 是值不是指针，
// 「没有」就是两个标记都为假，下面两个 if 自然都不成立。
func requestProposal(header sessionlog.EpochHeader) llm.CallConfig {
	proposal := header.Config.Clone()
	if header.AdapterDefaults.ReasoningEffort {
		proposal.ReasoningEffort = ""
	}
	if header.AdapterDefaults.MaxTokens {
		proposal.MaxTokens = 0
	}
	return proposal
}

// buildRequest 装配出一次完整的模型请求，并把它绑在解算出它那份默认值的那次适配器登记上。
//
// 源: packages/core/agent-loop/src/agent.ts:422-514
//
// 第二个返回值为 nil 表示这条路由当下没有登记着的适配器——中间件仍然可能服务它。
func (a *ReactLoopAgent) buildRequest(
	ctx context.Context,
	turn, step int,
	toolSchemas []llm.ToolSchema,
	system string,
	boundaryMessages []llm.Message,
) (llm.GenerateOptions, *llm.PreparedCall, error) {
	var empty llm.GenerateOptions

	persisted, hasPersisted, err := a.session.RequestHeader()
	if err != nil {
		return empty, nil, fmt.Errorf("harness/agentloop: 读请求头失败：%w", err)
	}
	a.mutex.Lock()
	headerLogged := a.requestHeaderLogged
	a.mutex.Unlock()

	// 一个循环实例从它自己声明的那条路由起步，只把「确实属于这个确切模型、
	// 而且是人选的」那个推理档位恢复回来。后面的步骤重新解算被标记过的默认值。
	seed := llm.CallConfig{
		Provider:        a.options.Provider,
		Model:           a.options.Model,
		ReasoningEffort: a.options.ReasoningEffort,
		MaxTokens:       a.options.MaxTokens,
	}
	if headerLogged {
		seed = requestProposal(persisted)
	} else if seed.ReasoningEffort == "" &&
		hasPersisted &&
		persisted.Config.Provider == a.options.Provider &&
		persisted.Config.Model == a.options.Model &&
		!persisted.AdapterDefaults.ReasoningEffort {
		// 声明出来的档位压过持久化下来的那个，对应 DSH 那句
		// `this.options.reasoningEffort ?? persistedReasoningEffort`（agent.ts:466）：
		// 换了档位重挂的那个实例，要跑的是新档位，不是上次记下来的。
		seed.ReasoningEffort = persisted.Config.ReasoningEffort
	}

	proposed, err := a.deps.Agents.ResolveRequest(
		ctx,
		agent.Request{Agent: a, Turn: turn, Step: step},
		func(context.Context) (llm.CallConfig, error) { return seed.Clone(), nil },
	)
	if err != nil {
		return empty, nil, err
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}
	if proposed.Provider == "" || proposed.Model == "" {
		return empty, nil, fmt.Errorf(
			"harness/agentloop: agent %q 没有 provider/model：给 agent.Options 填上这两项，或者在 agent/request 那条瀑布上一起给出",
			string(a.id))
	}

	config := proposed
	prepared, err := a.deps.LLM.PrepareCall(ctx, proposed)
	if err != nil {
		// 中间件可以服务一条没登记的路由，别的失败照抛。
		var carrier *llm.Error
		if !errors.As(err, &carrier) || carrier.Failure.Code != llm.NoAdapterCode {
			return empty, nil, err
		}
		prepared = nil
	} else {
		config = prepared.Config()
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}

	header := sessionlog.EpochHeader{Config: config, System: system, Tools: toolSchemas}
	if prepared != nil {
		header.AdapterDefaults = prepared.AdapterDefaults()
	}
	header = sessionlog.CanonicalHeader(header)

	if err := a.foldRequestHeader(header, headerLogged); err != nil {
		return empty, nil, err
	}
	if err := a.foldRequestContext(config, prepared); err != nil {
		return empty, nil, err
	}
	if err := abortedErr(ctx); err != nil {
		return empty, nil, err
	}

	return llm.GenerateOptions{
		Provider:        header.Config.Provider,
		Model:           header.Config.Model,
		ReasoningEffort: header.Config.ReasoningEffort,
		Messages:        boundaryMessages,
		System:          header.System,
		Tools:           header.Tools,
		Temperature:     header.Config.Temperature,
		MaxTokens:       header.Config.MaxTokens,
		Stop:            header.Config.Stop,
		SessionID:       llm.SessionID(a.session.ID()),
		AgentLoop:       true,
	}, prepared, nil
}

// foldRequestHeader 只在需要时往日志上添一份请求头快照。
//
// 源: packages/core/agent-loop/src/agent.ts:483-489
//
// 每个循环实例的第一次请求一定写一份锚点（initial 或者 resume），之后只有内容
// 真的变了才写（change）。不写锚点的话，一段恢复出来的日志读不出「这一程是从
// 哪份配置开始的」。
func (a *ReactLoopAgent) foldRequestHeader(header sessionlog.EpochHeader, headerLogged bool) error {
	baseline, hasBaseline, err := a.session.RequestHeader()
	if err != nil {
		return fmt.Errorf("harness/agentloop: 读请求头失败：%w", err)
	}
	if headerLogged {
		if hasBaseline && sessionlog.HeaderEquals(baseline, header) {
			return nil
		}
		_, err := a.appendEvent(
			sessionlog.RequestHeaderData{Header: header, Reason: sessionlog.HeaderChange}, nil, nil)
		return err
	}

	reason := sessionlog.HeaderInitial
	if hasBaseline {
		reason = sessionlog.HeaderResume
	}
	if _, err := a.appendEvent(sessionlog.RequestHeaderData{Header: header, Reason: reason}, nil, nil); err != nil {
		return err
	}
	a.mutex.Lock()
	a.requestHeaderLogged = true
	a.mutex.Unlock()
	return nil
}

// foldRequestContext 只在这条已解析路由的注册期元数据变了时记一条。
//
// 源: packages/core/agent-loop/src/agent.ts:491-502
//
// 新增: DSH 那边是 provider、model、contextWindow 三个字段一路 !== 比过来。
// [sessionlog.RequestContext] 在 Go 里是个可比较的结构体，所以直接比整体——
// 少一处「以后加了字段却忘了加进比较」的地方。
func (a *ReactLoopAgent) foldRequestContext(config llm.CallConfig, prepared *llm.PreparedCall) error {
	requestContext := sessionlog.RequestContext{Provider: config.Provider, Model: config.Model}
	if prepared != nil {
		if modelContext, ok := prepared.ModelContext(); ok {
			requestContext.ContextWindow = modelContext.ContextWindow
		}
	}
	previous, hasPrevious, err := a.session.RequestContext()
	if err != nil {
		return fmt.Errorf("harness/agentloop: 读请求元数据失败：%w", err)
	}
	if hasPrevious && previous == requestContext {
		return nil
	}
	_, err = a.appendEvent(sessionlog.RequestContextData{RequestContext: requestContext}, nil, nil)
	return err
}

// appendEvent 把一份负载排成字节、追加进日志，交出落定的那条事件。
//
// 新增: 本包每一处追加都长一个样（排负载、包错误、追加、再包错误），DSH 那边
// 由 session.append(type, data, options) 这一个方法承担。这里把它收成一个函数，
// 好让每一处调用点只剩下「写的是什么」。
func (a *ReactLoopAgent) appendEvent(
	data sessionlog.EventData,
	surfaceOp sessionlog.SurfaceOp,
	sourceEventSeqs []int,
) (sessionlog.Event, error) {
	eventType := data.EventType()
	payload, err := json.Marshal(data)
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("harness/agentloop: 排 %s 负载失败：%w", eventType, err)
	}
	event, err := a.session.Append(sessionlog.Event{
		Type:            eventType,
		Data:            payload,
		SurfaceOp:       surfaceOp,
		SourceEventSeqs: sourceEventSeqs,
	})
	if err != nil {
		return sessionlog.Event{}, fmt.Errorf("harness/agentloop: 追加 %s 失败：%w", eventType, err)
	}
	return event, nil
}
