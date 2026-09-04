// 本文件的作用：续接管理器那几件公开操作——起一个可续后台孩子、对它做后续投递、
// 打断它，以及把它对父的汇报投出去；连同准入、授权和投递这几样私有辅助。
//
// 源: packages/subagent/subagent/src/continuation.ts:394-719

package subagent

import (
	"context"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// StartContinuable 起一个可续后台孩子：占下它耐久的身份、解算提供方那份脱离的创建
// 贡献、经那把私有的活化所有者作用域建出孩子 agent、把可续父那边的所有权立起来，
// 然后投出初始提示词。收件箱一接受、拿到消息 id 就返回——**不**等回合开跑，
// 也**不**等那条消息落进会话日志。
//
// 源: packages/subagent/subagent/src/continuation.ts:394-476
//
// 那次接受之前的每一个失败都两个 id 都不给地报错，并把已经建出来的句柄处置掉、
// 把活化和父那边的所有权回滚。调用方的 ctx 只拥有查找、物化和准入这一段，
// 接受之后活化归管理器自己。
func (m *ContinuationManager) StartContinuable(
	ctx context.Context,
	spec ContinuableStartSpec,
) (ContinuableStart, error) {
	request := spec.Request
	parent := request.Parent
	if parent == nil {
		return ContinuableStart{}, errInvalidRequestf("起一个可续子 agent 需要一个发起派发的父 agent")
	}
	if err := m.assertAdmitting(parent); err != nil {
		return ContinuableStart{}, err
	}
	store, err := m.requirePersistence()
	if err != nil {
		return ContinuableStart{}, err
	}
	if err := AssertMaxDepth(request.MaxDepth); err != nil {
		return ContinuableStart{}, err
	}
	childID := spec.ChildID
	if childID == "" {
		childID = sessionlog.SessionID(uuid.NewString())
	}
	if err := m.assertChildIDAvailable(childID); err != nil {
		return ContinuableStart{}, err
	}
	childDepth, err := ResolveChildDepth(parent, request.MaxDepth)
	if err != nil {
		return ContinuableStart{}, err
	}
	// 描述符里记的是**解算之后**的路由，不是请求里那半份：一次冷恢复只有这份记录
	// 可读，从父身上重新取会让一个换过模型的父把孩子也一起换掉。
	parentOptions := parent.Options()
	agentProvider := request.AgentOptions.Provider
	if agentProvider == "" {
		agentProvider = parentOptions.Provider
	}
	agentModel := request.AgentOptions.Model
	if agentModel == "" {
		agentModel = parentOptions.Model
	}
	descriptorInput := DescriptorData{
		Mode:          ModeContinuable,
		Provider:      spec.Provider,
		Label:         spec.Label,
		AgentProvider: agentProvider,
		AgentModel:    agentModel,
		Persona:       request.Persona,
	}
	if request.ToolFilter.Allow != nil || request.ToolFilter.Deny != nil {
		filter := request.ToolFilter
		descriptorInput.ToolFilter = &filter
	}
	descriptor, err := SnapshotDescriptor(descriptorInput)
	if err != nil {
		return ContinuableStart{}, err
	}
	// 在这次开工的第一个可中断点**之前**同步拍下：父后来那次切换属于父的未来。
	delegatedPolicies := CaptureDelegatedPolicyOverrides(m.deps.Approval)

	prepared, err := m.host.prepareContinuable(ctx, spec.Provider, ContinuableCreateRequest{
		SessionID: childID,
		Parent:    parent,
	})
	if err != nil {
		return ContinuableStart{}, err
	}
	if err := continuationCancelled(ctx); err != nil {
		return ContinuableStart{}, err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return ContinuableStart{}, err
	}
	lineageSeedLength := len(prepared.Seed)
	seed, err := SeedDescriptorTurn(childID, prepared.Seed, descriptor)
	if err != nil {
		return ContinuableStart{}, err
	}

	release, err := m.locks.acquire(ctx, childID)
	if err != nil {
		return ContinuableStart{}, err
	}
	defer release()

	if err := continuationCancelled(ctx); err != nil {
		return ContinuableStart{}, err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return ContinuableStart{}, err
	}
	if err := m.assertChildIDAvailable(childID); err != nil {
		return ContinuableStart{}, err
	}
	// 只有调用方自己给了 id 才查持久化：管理器分配的 UUID 撞不上任何东西，而这次
	// 列举是一次真的介质读。
	if spec.ChildID != "" {
		if err := m.assertPersistedIDAvailable(ctx, store, parent, childID); err != nil {
			return ContinuableStart{}, err
		}
	}
	activation, err := m.materialize(ctx, materializeInputs{
		childID:  childID,
		provider: spec.Provider,
		parent:   parent,
		create: &materializeCreate{
			seed:              seed,
			meta:              ChildSessionMeta(parent, childDepth, lineageSeedLength, m.deps.Composition.Presets),
			delegatedPolicies: delegatedPolicies,
		},
		agentOptions: ResolveChildAgentOptions(parent, request.AgentOptions),
		composition:  ChildComposition{Persona: request.Persona, ToolFilter: request.ToolFilter},
	})
	if err != nil {
		return ContinuableStart{}, err
	}
	messageID, err := m.submitMaterialized(ctx, activation, request.Prompt, llm.UserSource{}, parent)
	if err != nil {
		return ContinuableStart{}, err
	}
	return ContinuableStart{ChildID: childID, MessageID: messageID}, nil
}

// assertChildIDAvailable 拒掉一个已经被某个活 agent 或者活会话占着的孩子 id。
//
// 源: packages/subagent/subagent/src/continuation.ts:479-489
func (m *ContinuationManager) assertChildIDAvailable(childID sessionlog.SessionID) error {
	if _, live := m.deps.Agents.Get(childID); live {
		return NewError(`子 agent "`+string(childID)+`" 已经存在`, CodeDuplicateChild, nil)
	}
	if _, live := m.deps.Sessions.Get(childID); live {
		return NewError(`子 agent "`+string(childID)+`" 已经存在`, CodeDuplicateChild, nil)
	}
	return nil
}

// assertPersistedIDAvailable 在调用方自带 id 那条路上，确认持久化里也没有这个身份。
//
// 源: packages/subagent/subagent/src/continuation.ts:449-459
//
// 这次列举之后要把三道闸全部重验一遍：它是一次真的介质读，中间什么都可能变。
func (m *ContinuationManager) assertPersistedIDAvailable(
	ctx context.Context,
	store persistence.Store,
	parent agent.Agent,
	childID sessionlog.SessionID,
) error {
	headers, err := store.List(ctx)
	if err != nil {
		return err
	}
	if err := continuationCancelled(ctx); err != nil {
		return err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return err
	}
	if err := m.assertChildIDAvailable(childID); err != nil {
		return err
	}
	for _, header := range headers {
		if header.ID == childID {
			return NewError(`子 agent "`+string(childID)+`" 已经存在`, CodeDuplicateChild, nil)
		}
	}
	return nil
}

// Followup 把一条消息投给一个可续孩子的下一个 FIFO 回合：活化在就直接送，
// 不在就先冷恢复。收件箱一接受就返回那条消息的 id。
//
// 源: packages/subagent/subagent/src/continuation.ts:491-531
//
// 一个正在被拆的活化不接投递：这一路会等那次拆解走完，再对同一个耐久孩子冷恢复
// 出一次新的活化。
func (m *ContinuationManager) Followup(
	ctx context.Context,
	parent agent.Agent,
	childID sessionlog.SessionID,
	content llm.Content,
	options FollowupOptions,
) (llm.MessageID, error) {
	if parent == nil {
		return "", errInvalidRequestf("投递给可续子 agent 需要一个活的直系父 agent")
	}
	if err := m.assertAdmitting(parent); err != nil {
		return "", err
	}
	for {
		messageID, retry, err := m.followupOnce(ctx, parent, childID, content, options)
		if err != nil {
			return "", err
		}
		if !retry {
			return messageID, nil
		}
		// 上一次活化正在拆：等它走完之后重来一轮，这一轮会走冷恢复。
		if err := m.assertAdmitting(parent); err != nil {
			return "", err
		}
		if err := continuationCancelled(ctx); err != nil {
			return "", err
		}
	}
}

// followupOnce 是 [ContinuationManager.Followup] 的一轮。retry 为真表示这次撞上了
// 一个正在拆的活化，已经等它拆完，调用方该再来一轮。
//
// 源: packages/subagent/subagent/src/continuation.ts:507-528
func (m *ContinuationManager) followupOnce(
	ctx context.Context,
	parent agent.Agent,
	childID sessionlog.SessionID,
	content llm.Content,
	options FollowupOptions,
) (llm.MessageID, bool, error) {
	release, err := m.locks.acquire(ctx, childID)
	if err != nil {
		return "", false, err
	}
	// 冷恢复和投递都在这把锁里跑完，所以这里不能提前放；用一个具名的收尾。
	defer release()

	m.mutex.Lock()
	existing, resident := m.activations[childID]
	var disposal *disposalTx
	if resident {
		disposal = existing.disposal
	}
	m.mutex.Unlock()

	if !resident {
		messageID, err := m.coldResume(ctx, parent, childID, content, options)
		return messageID, false, err
	}
	if disposal != nil {
		// **有意**丢掉那次拆解自己的结局：它属于开这次事务的那一方，不属于这次投递。
		// 这里只是等它退出场，好让下一轮冷恢复一个新的活化。
		if err := disposal.wait(ctx); err != nil {
			return "", false, err
		}
		return "", true, nil
	}
	messageID, err := m.submitAdmitted(ctx, existing, content, options.Source, parent)
	return messageID, false, err
}

// Interrupt 打断一个可续孩子当下那段活动，它排着的活儿留着。
//
// 源: packages/subagent/subagent/src/continuation.ts:533-594
//
// 目标没有活化时这是一次**被接受的空操作**：那个孩子本来就没在跑。
func (m *ContinuationManager) Interrupt(
	targetSessionID sessionlog.SessionID,
	authority InterruptAuthority,
) error {
	if authority.Kind == AuthorityAncestor {
		caller := authority.Agent
		if caller == nil {
			return errInvalidRequestf("以祖先身份打断需要那个确切的活祖先 agent")
		}
		if live, found := m.deps.Agents.Get(caller.ID()); !found || live != caller {
			return NewError(
				`打断 "`+string(targetSessionID)+`" 需要那个确切的活祖先 agent`,
				CodeUnauthorized, nil,
			)
		}
		if caller.ID() == targetSessionID {
			return NewError(
				`agent "`+string(caller.ID())+`" 不能打断它自己`,
				CodeUnauthorized, nil,
			)
		}
	}
	m.mutex.Lock()
	target, resident := m.activations[targetSessionID]
	var disposal *disposalTx
	if resident {
		disposal = target.disposal
	}
	m.mutex.Unlock()
	if !resident {
		// 被接受的空操作：这个孩子没有驻留轮次，没有什么可打断的。
		return nil
	}
	switch authority.Kind {
	case AuthorityUser:
		if target.handle.Agent.Session().Header().ParentSession != authority.ParentSessionID {
			return NewError(
				`子 agent "`+string(targetSessionID)+`" 属于另一个父会话`,
				CodeUnauthorized, nil,
			)
		}
	case AuthorityAncestor:
		if _, inLineage := target.ancestry[authority.Agent]; !inLineage {
			return NewError(
				`子 agent "`+string(targetSessionID)+`" 不是 agent "`+string(authority.Agent.ID())+`" 的活后代`,
				CodeUnauthorized, nil,
			)
		}
	default:
		return errInvalidRequestf("打断请求的权属类型不认识：%q", authority.Kind)
	}
	if disposal != nil {
		// 已经在拆了：取消这件事已经发生过，再发一次没有意义。
		return nil
	}
	var cause sessionlog.TurnEndCancelCause = sessionlog.ParentCancel{}
	if authority.Kind == AuthorityUser {
		cause = sessionlog.UserCancel{}
	}
	// KeepInbox：打断停的是当下这段活动，不是这个孩子排着的活儿。
	target.handle.Agent.Cancel(cause, agent.CancelOptions{KeepInbox: true})
	return nil
}

// ReportFrom 把一个可续孩子对它直系父的显式汇报投出去，交回那条被接受的消息 id。
//
// 源: packages/subagent/subagent/src/continuation.ts:596-619
func (m *ContinuationManager) ReportFrom(
	ctx context.Context,
	child agent.Agent,
	content llm.Content,
	options ReportOptions,
) (llm.MessageID, error) {
	if child == nil {
		return "", errInvalidRequestf("汇报需要那个汇报的孩子 agent")
	}
	if err := continuationCancelled(ctx); err != nil {
		return "", err
	}
	if err := m.assertAdmitting(child); err != nil {
		return "", err
	}
	activation, err := m.authorizeReporter(child)
	if err != nil {
		return "", err
	}
	parent, err := m.resolveReportParent(child)
	if err != nil {
		return "", err
	}
	return m.deliverReport(activation, parent, content, options.Delivery)
}

// authorizeReporter 认这个汇报方确实是一个活着的可续孩子，并交回它那次活化。
//
// 源: packages/subagent/subagent/src/continuation.ts:622-639
func (m *ContinuationManager) authorizeReporter(child agent.Agent) (*activation, error) {
	m.mutex.Lock()
	found, resident := m.activations[child.ID()]
	var disposal *disposalTx
	if resident {
		disposal = found.disposal
	}
	m.mutex.Unlock()
	if !resident || found.handle.Agent != child {
		return nil, NewError(
			`agent "`+string(child.ID())+`" 不是一个活着的可续子 agent，汇报不了`,
			CodeUnauthorized, nil,
		)
	}
	if disposal != nil {
		return nil, NewError(
			`子 agent "`+string(child.ID())+`" 的活化正在被处置，这次汇报没有投出去`,
			CodeActivationClosing, nil,
		)
	}
	return found, nil
}

// resolveReportParent 解出这次汇报要投给的那个活着的直系父。
//
// 源: packages/subagent/subagent/src/continuation.ts:642-653
func (m *ContinuationManager) resolveReportParent(child agent.Agent) (agent.Agent, error) {
	parentSession := child.Session().Header().ParentSession
	parent, live := m.deps.Agents.Get(parentSession)
	if !live {
		return nil, NewError("直系父不在了，这次汇报没有投出去", CodeParentUnavailable, nil)
	}
	return parent, nil
}

// deliverReport 把汇报按部署的排期策略送进父的收件箱。
//
// 源: packages/subagent/subagent/src/continuation.ts:656-679
//
// 打头那一行是运行时加的，好让父知道这段话是谁说的——归属那一半在来源上，
// 但模型看不到来源。
func (m *ContinuationManager) deliverReport(
	reporter *activation,
	parent agent.Agent,
	content llm.Content,
	delivery ReportDelivery,
) (llm.MessageID, error) {
	source, err := NewReportSource(reporter.childID)
	if err != nil {
		// 走不到：这个 id 非空（一份活化必然有），而剩下那一步是 marshalSenderExtra
		// 里那次转不失败的编码。
		return "", err
	}
	// 给模型看的载荷，所以保持英文。
	body := make(llm.Content, 0, len(content)+1)
	body = append(body, llm.TextBlock{
		Text: "Background subagent " + string(reporter.childID) + " reported:",
	})
	body = append(body, content...)
	message := llm.NewUserMessage(body, source)
	if delivery == DeliveryNextStep {
		m.sendWaking(parent, message.ID, func() { m.sendReport(parent, message, delivery) })
		return message.ID, nil
	}
	m.sendReport(parent, message, delivery)
	return message.ID, nil
}

// sendWaking 在父自己也是一个可续孩子时，把这次唤醒记进它那扇结清窗口，
// 免得父在「消息已接受、但还没被认领」那个空档里被判成静止。
//
// 源: packages/subagent/subagent/src/continuation.ts:682-701
func (m *ContinuationManager) sendWaking(parent agent.Agent, messageID llm.MessageID, send func()) {
	m.mutex.Lock()
	parentActivation, resident := m.activations[parent.ID()]
	if !resident || parentActivation.handle.Agent != parent {
		m.mutex.Unlock()
		// 顶层的、或者别的什么 agent：它不在这张等待图里，直接送。
		send()
		return
	}
	m.mutex.Unlock()
	m.admitWaking(parentActivation, messageID, send)
}

// sendReport 按排期策略把那条消息真的送出去。
//
// 源: packages/subagent/subagent/src/continuation.ts:704-719
//
// 新增: DSH 这里包了一层 try/catch，把 steer／inject 抛出来的东西翻译成
// [CodeParentUnavailable]。Go 这边 [github.com/snight1983/ds-harness-go/harness/agent.Agent] 的
// Steer／Inject 签名上没有错误通道——循环那一层把入队失败报给它自己的错误出口，
// 绝不抛给送信方（见 [github.com/snight1983/ds-harness-go/harness/agentloop.ReactLoopAgent.Send]）。
// 所以这一路没有可翻译的失败，[CodeParentUnavailable] 只剩
// [ContinuationManager.resolveReportParent] 一个来源。
func (m *ContinuationManager) sendReport(parent agent.Agent, message llm.Message, delivery ReportDelivery) {
	if delivery == DeliveryNextStep {
		parent.Steer(message)
		return
	}
	parent.Inject(message)
}
