// 本文件的作用：一次活化从物化到结清的那整条路——建出或者恢复出孩子 agent、
// 把所有权立起来、把消息准入进收件箱、盯着它静下来，然后孩子优先地处置掉，
// 并把结清交回给父。
//
// 源: packages/subagent/subagent/src/continuation.ts:945-1541

package subagent

import (
	"context"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/subagent/internal/childseed"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// materializeCreate 是「这一次是全新创建」那一支才有的东西；冷恢复这一支为 nil。
//
// 源: packages/subagent/subagent/src/continuation.ts:254-262（MaterializeInputs.create）
type materializeCreate struct {
	// seed 是这个孩子的创建种子：继承来的父历史前缀，后面跟那条描述符事件。
	seed []sessionlog.Event
	// baseSeq 是 seed 第一条应有的 seq；默认 0。
	//
	// 新增: 理由见 [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions.BaseSeq]。
	// 一段从被弹过头的父日志上切下来的前缀不从 0 起。
	baseSeq int
	// meta 是 [ChildSessionMeta] 拍好的那份血统元数据。SessionID、Seed、BaseSeq、
	// AgentOptions、Setup 由 [ContinuationManager.materializeTracked] 补上。
	meta agent.CreateOptions
	// delegatedPolicies 是在派发那一刻拍下来、要种进孩子自己日志的那份策略。
	delegatedPolicies DelegatedPolicyOverrides
}

// materializeInputs 是物化一次活化所需的全部输入。
//
// 源: packages/subagent/subagent/src/continuation.ts:254-262
//
// 新增: DSH 这里还有一个 signal 字段。Go 的取消是 ctx，走参数不走结构体。
type materializeInputs struct {
	// childID 是那个耐久的孩子身份。
	childID sessionlog.SessionID
	// provider 是记在耐久描述符里的那个提供方名字。
	provider string
	// parent 是发起这次物化的那个确切的活父。
	parent agent.Agent
	// create 非 nil 表示全新创建；nil 表示从持久化冷恢复。
	create *materializeCreate
	// agentOptions 是解算好的提供方路由与模型。
	agentOptions agent.Options
	// composition 是只挂在这个孩子作用域上的人设与工具范围。
	composition ChildComposition
}

// requirePersistence 解出可续孩子必需的那份会话持久化，没有就大声报错。
//
// 源: packages/subagent/subagent/src/continuation.ts:1532-1541
func (m *ContinuationManager) requirePersistence() (persistence.Store, error) {
	if m.deps.Persistence == nil {
		return nil, NewError(
			"continuable subagents require session persistence (load a dsh-session-persistence backend)",
			CodePersistenceUnavailable, nil,
		)
	}
	return m.deps.Persistence, nil
}

// coldResume 在一个没有驻留活化的耐久孩子上重新开一段轮次：读它自己的日志、
// 折出那份描述符、经 agent 注册表恢复出活化，然后把等着的那条消息投进去。
// 这条路**从不**经过任何子 agent 提供方——持久化的会话已经带着初始前缀，
// 而那份描述符就是重建所需的全部输入。
//
// 源: packages/subagent/subagent/src/continuation.ts:945-994
func (m *ContinuationManager) coldResume(
	ctx context.Context,
	parent agent.Agent,
	childID sessionlog.SessionID,
	content llm.Content,
	options FollowupOptions,
) (llm.MessageID, error) {
	store, err := m.requirePersistence()
	if err != nil {
		return "", err
	}
	loaded, err := store.Inspect(ctx, childID)
	if err != nil {
		if cancelled := continuationCancelled(ctx); cancelled != nil {
			return "", cancelled
		}
		return "", NewError(`子 agent "`+string(childID)+`" 用不了`, CodeNotResumable, err)
	}
	if err := continuationCancelled(ctx); err != nil {
		return "", err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return "", err
	}
	// 折之前先认那份落盘的头：只有这个耐久孩子那个确切的活直系父续得了它。
	if err := m.authorizeLineage(parent, childID, loaded.Meta.ParentSession); err != nil {
		return "", err
	}
	// 只折这个孩子自己那截后缀：一份分叉种子会回放父的日志，而父自己要是个可续孩子，
	// 那段回放里带的是一个**祖先**的描述符。
	descriptor, found, err := FoldDescriptor(childOwnEvents(loaded))
	if err != nil {
		return "", err
	}
	if !found || descriptor.Mode != ModeContinuable {
		return "", NewError(
			`子 agent "`+string(childID)+`" 没有能续下去的状态，恢复不了；不要拿这个 id 重试 send_message`,
			CodeNotResumable, nil,
		)
	}
	composition := ChildComposition{Persona: descriptor.Persona}
	if descriptor.ToolFilter != nil {
		composition.ToolFilter = *descriptor.ToolFilter
	}
	resumed, err := m.materialize(ctx, materializeInputs{
		childID:      childID,
		provider:     descriptor.Provider,
		parent:       parent,
		agentOptions: agent.Options{Provider: descriptor.AgentProvider, Model: descriptor.AgentModel},
		composition:  composition,
	})
	if err != nil {
		if cancelled := continuationCancelled(ctx); cancelled != nil {
			return "", cancelled
		}
		// 本包自己那些带码的失败原样往上交：调用方分得清的只有那个码，
		// 一律裹成 NOT_RESUMABLE 会把「谁都不许投递」说成「这个孩子没了」。
		var coded *llm.Error
		if errors.As(err, &coded) {
			return "", err
		}
		return "", NewError(`子 agent "`+string(childID)+`" 用不了`, CodeNotResumable, err)
	}
	return m.submitMaterialized(ctx, resumed, content, options.Source, parent)
}

// childOwnEvents 切出一份落盘检视里属于这个孩子自己的那截事件后缀。
//
// 新增: DSH 是 `loaded.events.slice(loaded.meta.seedLength ?? 0)`，JS 的 slice
// 越界给空数组。本仓库这道换算还多一层：SeedLength 是个条数，日志被弹过头之后
// 它和下标差着一个起点，直接拿它当下标切会把一批属于这个孩子自己的事件当成
// 继承来的丢掉。换算和夹取都收在 [sessionlog.SeedSuffix] 一处。
func childOwnEvents(loaded persistence.Inspection) []sessionlog.Event {
	return sessionlog.SeedSuffix(loaded.Events, loaded.Meta)
}

// submitMaterialized 要么把消息投进一个刚物化出来的活化，要么把它整个回滚掉。
//
// 源: packages/subagent/subagent/src/continuation.ts:1005-1020
func (m *ContinuationManager) submitMaterialized(
	ctx context.Context,
	target *activation,
	content llm.Content,
	source llm.MessageSource,
	parent agent.Agent,
) (llm.MessageID, error) {
	messageID, err := m.submitAdmitted(ctx, target, content, source, parent)
	if err == nil {
		return messageID, nil
	}
	// **有意**丢掉回滚自己的失败：它盖不过那次「接受之前就没过闸」的原因。
	// 等的这一下用 context.Background()——调用方的取消已经是这次失败的来由，
	// 不能再拿它去掐这次管理器自己欠下的拆解。
	_ = m.dispose(target).wait(context.Background())
	return "", err
}

// materialize 经那把私有的活化所有者作用域建出或者恢复出孩子 agent，把句柄装进一次
// 新的活化，并在一个受管理的父上立起所有权。失败之后不留活化、不留句柄、
// 也不留所有权。
//
// 源: packages/subagent/subagent/src/continuation.ts:1028-1041
func (m *ContinuationManager) materialize(
	ctx context.Context,
	inputs materializeInputs,
) (*activation, error) {
	if err := m.assertAdmitting(inputs.parent); err != nil {
		return nil, err
	}
	lineage := m.liveLineage(inputs.parent)
	tracked := &materialization{lineage: lineage, settled: make(chan struct{})}
	m.mutex.Lock()
	m.materializations[tracked] = struct{}{}
	m.mutex.Unlock()

	result, err := m.materializeTracked(ctx, inputs, lineage)

	m.mutex.Lock()
	delete(m.materializations, tracked)
	m.mutex.Unlock()
	close(tracked.settled)
	return result, err
}

// materializeTracked 做一次被跟着的物化。调用方那道排干栅栏一直挂到它要么交回一个
// 驻留下来的活化、要么把回滚做完为止。
//
// 源: packages/subagent/subagent/src/continuation.ts:1048-1138
func (m *ContinuationManager) materializeTracked(
	ctx context.Context,
	inputs materializeInputs,
	parentLineage []agent.Agent,
) (*activation, error) {
	childID := inputs.childID
	parent := inputs.parent
	// 这里**不**再查一次 id：孩子锁已经把每个耐久孩子串成一条线，两个调用方都是在
	// 确认过没有活化之后才到这儿的，而一个别的主人占着的 id，权威的碰撞边界在
	// agent 注册表那次登记上——撞了会在那儿连同回滚一起报出来。
	if err := continuationCancelled(ctx); err != nil {
		return nil, err
	}
	setup := func(setupCtx context.Context, childScope *scope.Scope) (func() error, error) {
		if err := ApplyChildComposition(setupCtx, childScope, parent, inputs.composition, m.deps.Composition); err != nil {
			return nil, err
		}
		return m.deps.Setups.Apply(setupCtx, childScope)
	}
	observer := m.host.observeActivation(inputs.provider, childID, parent)

	// 建 agent 这一步自己负责句柄交接之前的回滚。失败之后没有驻留的活化，
	// 因此一条生命周期边都不会发出去。
	var (
		handle agent.Handle
		err    error
	)
	if inputs.create == nil {
		handle, err = m.deps.Agents.Resume(ctx, m.ownerScope, agent.ResumeOptions{
			ResumeSessionID: childID,
			AgentOptions:    inputs.agentOptions,
			Setup:           setup,
		})
	} else {
		var seed []sessionlog.Event
		seed, err = childseed.Seed(childID, inputs.create.seed, inputs.create.baseSeq, inputs.create.delegatedPolicies.ApprovalPolicy)
		if err != nil {
			return nil, err
		}
		options := inputs.create.meta
		options.SessionID = childID
		options.Seed = seed
		options.BaseSeq = inputs.create.baseSeq
		options.AgentOptions = inputs.agentOptions
		options.Setup = setup
		handle, err = m.deps.Agents.Create(ctx, m.ownerScope, options)
	}
	if err != nil {
		return nil, err
	}

	// 记的是那条耐久血统，不只是这次的调用方：创建时盖进孩子头里的就是这同一个
	// agent，而冷恢复在物化之前已经拿落盘的头认过它了。
	ancestry := map[agent.Agent]struct{}{handle.Agent: {}}
	for _, member := range parentLineage {
		ancestry[member] = struct{}{}
	}
	live := &activation{
		childID:       childID,
		parentSession: parent.ID(),
		provider:      inputs.provider,
		handle:        handle,
		observer:      observer,
		ancestry:      ancestry,
		ownedChildren: map[sessionlog.SessionID]struct{}{},
		accepted:      map[llm.MessageID]struct{}{},
		poke:          make(chan struct{}),
	}
	// 交接之后，任何失败都必须把建出来的句柄处置掉、把活化摘掉、把父那边的所有权
	// 回滚，然后才报错。
	m.mutex.Lock()
	m.activations[childID] = live
	m.mutex.Unlock()

	if err := m.publishActivation(ctx, live, parent); err != nil {
		// **有意**丢掉回滚自己的失败：它盖不过那次挡住这次操作交出消息 id 的准入失败。
		_ = m.rollbackUnpublished(live)
		return nil, err
	}
	m.watchSettlement(live)
	return live, nil
}

// publishActivation 走完句柄交接之后那道准入边界：重验取消与准入、在父那边立起
// 所有权、装上两个收件箱观察者，然后发出这一轮的开始边。
//
// 源: packages/subagent/subagent/src/continuation.ts:1105-1127
func (m *ContinuationManager) publishActivation(
	ctx context.Context,
	live *activation,
	parent agent.Agent,
) error {
	if err := continuationCancelled(ctx); err != nil {
		return err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return err
	}
	if err := m.acquireOwnership(parent, live.childID); err != nil {
		return err
	}
	// 每一个被接受的 id 恰好离开收件箱一次，要么被认领、要么被丢弃。在那儿把它清掉，
	// 正是 stateOfLocked 分得清「真的静下来了」和「被接受的回合还没被准入」的凭据。
	//
	// 这两个 disposer **有意**丢掉：owner 就是孩子自己那个作用域，句柄一处置这两笔
	// 登记就跟着没了。
	childScope := live.handle.Agent.Scope()
	if _, err := m.deps.Agents.OnInboxClaimed(ctx, childScope,
		func(observed agent.Agent, message llm.Message, _ int) {
			if observed == live.handle.Agent {
				m.retire(live, message.ID)
			}
		}); err != nil {
		return err
	}
	if _, err := m.deps.Agents.OnInboxDiscarded(ctx, childScope,
		func(observed agent.Agent, message llm.Message) {
			if observed == live.handle.Agent {
				m.retire(live, message.ID)
			}
		}); err != nil {
		// 走不到：这两笔登记挂的是同一把作用域，唯一的失败缘由是它已经处置了，
		// 而那种情形上面第一笔就已经报出来了。
		return err
	}
	// 建 agent 那一步已经在它的公布边界上提交过 setup；从这儿起的撤销都是对活着的
	// 东西即时撤销。开始边要在任何回合跑得起来之前发出去，于是观察方在这一轮的
	// 第一次请求之前就看得到它。
	live.observer.start(live.handle.Agent)
	return nil
}

// retire 把一条已经离开收件箱的被接受消息销掉，并让结清守望重新看一次静止。
//
// 源: packages/subagent/subagent/src/continuation.ts:1116-1122
func (m *ContinuationManager) retire(target *activation, messageID llm.MessageID) {
	m.mutex.Lock()
	_, admitted := target.accepted[messageID]
	delete(target.accepted, messageID)
	m.mutex.Unlock()
	if admitted {
		m.wake(target)
	}
}

// rollbackUnpublished 放掉一个开始边还没发出去的活化。那份记下来的事务一直留在活表
// 里，直到句柄处置有了结局，于是一次并发的排干或者投递观察到的是同一道关闸边界。
//
// 源: packages/subagent/subagent/src/continuation.ts:1145-1154
//
// 新增: 拆解本身走 context.Background()。调用方的取消**正是**走到这条回滚路上的
// 原因之一，拿它去掐这次管理器自己欠下的句柄处置，会留下一个谁也拆不掉的孩子。
func (m *ContinuationManager) rollbackUnpublished(live *activation) error {
	m.mutex.Lock()
	transaction, opened := m.beginDisposalLocked(live)
	m.mutex.Unlock()
	if !opened {
		// 测不到：要走到这儿，得有一次并发的排干**恰好**在同一个孩子还没公布的
		// 那几微秒里开出处置。这是真竞态，不是死代码——两条路都摘同一份活化，
		// 谁先开谁负责结清，后来的那条只等结局。
		return transaction.wait(context.Background())
	}
	err := live.handle.Dispose(context.Background())
	m.mutex.Lock()
	delete(m.activations, live.childID)
	m.mutex.Unlock()
	m.releaseOwnership(live.childID)
	transaction.settle(err)
	return err
}

// acquireOwnership 在孩子跑得起来之前，把它记进一个受管理的父那组名下孩子里，
// 于是那个父在这个孩子还活着的时候结不了清。一个顶层的、或者别的什么 agent 没有
// 活化，它待在这张等待图之外。
//
// 源: packages/subagent/subagent/src/continuation.ts:1162-1172
func (m *ContinuationManager) acquireOwnership(parent agent.Agent, childID sessionlog.SessionID) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	parentActivation, resident := m.activations[parent.ID()]
	if !resident {
		return nil
	}
	if parentActivation.disposal != nil {
		return NewError(
			`子 agent 的父 "`+string(parent.ID())+`" 正在被处置，这个孩子没有立起来`,
			CodeActivationClosing, nil,
		)
	}
	parentActivation.ownedChildren[childID] = struct{}{}
	return nil
}

// releaseOwnership 把一个孩子从它那个活着的主人名下摘掉，并让那个主人重新查一次结清。
//
// 源: packages/subagent/subagent/src/continuation.ts:1175-1179
func (m *ContinuationManager) releaseOwnership(childID sessionlog.SessionID) {
	m.mutex.Lock()
	var woken []*activation
	for _, candidate := range m.activations {
		if _, owned := candidate.ownedChildren[childID]; !owned {
			continue
		}
		delete(candidate.ownedChildren, childID)
		woken = append(woken, candidate)
	}
	m.mutex.Unlock()
	for _, candidate := range woken {
		m.wake(candidate)
	}
}

// wake 让一个结清守望在所有权或者收件箱变过之后，重新观察一次静止。
//
// 源: packages/subagent/subagent/src/continuation.ts:1182-1185
func (m *ContinuationManager) wake(target *activation) {
	m.mutex.Lock()
	poked := target.poke
	target.poke = make(chan struct{})
	m.mutex.Unlock()
	close(poked)
}

// submit 把一条消息作为这个孩子的下一个 FIFO 回合投进去，交回那条被接受的收件箱
// 消息 id。**接受**就是这次操作的成功边界；此后这次活化由管理器自己拥有。
//
// 源: packages/subagent/subagent/src/continuation.ts:1192-1209
func (m *ContinuationManager) submit(
	target *activation,
	content llm.Content,
	source llm.MessageSource,
	parent agent.Agent,
) (llm.MessageID, error) {
	// 父发起的投递靠所有权把父留活，所以要在消息进得了孩子收件箱之前先立起来。
	if err := m.acquireOwnership(parent, target.childID); err != nil {
		// 测不到：这一步在这条路上只可能因为父那把作用域正在处置而失败，而调用方
		// 手上攥着的正是这个父。这是真竞态，不是死代码。
		return "", err
	}
	message := llm.NewUserMessage(content, source)
	m.admitWaking(target, message.ID, func() { target.handle.Agent.Followup(message) })
	// 过了这一步，调用方就攥着这个孩子的一个 id 了，于是它日后的结清是父应得的一份交代。
	m.mutex.Lock()
	target.announced = true
	m.mutex.Unlock()
	return message.ID, nil
}

// admitWaking 把一次唤醒投递记进一个驻留活化那扇结清窗口。
//
// 源: packages/subagent/subagent/src/continuation.ts:1218-1236
//
// 新增: DSH 那里 send 外面包了一层 try/catch，抛了就把这个 id 从 accepted 里撤回来。
// Go 这边送不出错——[github.com/snight1983/ds-harness-go/harness/agent.Agent] 的 Follower／Steer／Inject
// 签名上没有错误通道，入队失败由循环那一层报给它自己的错误出口（见
// [github.com/snight1983/ds-harness-go/harness/agentloop.ReactLoopAgent.Send]）——所以没有那条撤回路。
func (m *ContinuationManager) admitWaking(target *activation, messageID llm.MessageID, send func()) {
	// 唤醒式的投递会同步地发出收件箱事件，所以在调用开始之前，观察方就得看见这次
	// 活化是忙的。
	m.mutex.Lock()
	target.accepted[messageID] = struct{}{}
	m.mutex.Unlock()
	send()
	// 被接受的唤醒活儿把这次活化留活，直到 WhenIdle 看完整段唤醒后缀。
	m.wake(target)
}

// submitAdmitted 跨过最后那道准入闸并且不让出去地投递。取消、管理器排干、或者活化
// 处置只要在这段跨度之前赢下来，这次投递就不带收件箱接受地报错。
//
// 源: packages/subagent/subagent/src/continuation.ts:1243-1266
func (m *ContinuationManager) submitAdmitted(
	ctx context.Context,
	target *activation,
	content llm.Content,
	source llm.MessageSource,
	parent agent.Agent,
) (llm.MessageID, error) {
	if err := continuationCancelled(ctx); err != nil {
		return "", err
	}
	if err := m.assertAdmitting(parent); err != nil {
		return "", err
	}
	m.mutex.Lock()
	disposal := target.disposal
	m.mutex.Unlock()
	if disposal != nil {
		return "", NewError(
			`子 agent "`+string(target.childID)+`" 的活化正在被处置，这条消息没有被接受`,
			CodeActivationClosing, nil,
		)
	}
	if err := m.authorizeLineage(
		parent,
		target.childID,
		target.handle.Agent.Session().Header().ParentSession,
	); err != nil {
		return "", err
	}
	return m.submit(target, content, source, parent)
}

// authorizeLineage 拿那条耐久的直系父血统去认一次操作。别的 agent、祖先、团队、
// 工作流和宿主一律拒着，直到一份显式的权属协议真有生产消费方为止。
//
// 源: packages/subagent/subagent/src/continuation.ts:1273-1287
func (m *ContinuationManager) authorizeLineage(
	parent agent.Agent,
	childID sessionlog.SessionID,
	parentSession sessionlog.SessionID,
) error {
	if live, found := m.deps.Agents.Get(parent.ID()); !found || live != parent {
		return NewError(
			`给子 agent "`+string(childID)+`" 投递需要那个确切的活父 agent`,
			CodeUnauthorized, nil,
		)
	}
	if parentSession != parent.ID() {
		return NewError(
			`子 agent "`+string(childID)+`" 属于另一个父会话`,
			CodeUnauthorized, nil,
		)
	}
	return nil
}

// watchSettlement 跟着一次活化走到结清：先等 agent 静下来，再等名下每个孩子处置完，
// 两者都成立才处置这个句柄。一条在 waiting 期间投进来的 next-step 会唤醒同一个
// agent、把它带回 running，所以这里是重新观察，而不是提前结清。
//
// 源: packages/subagent/subagent/src/continuation.ts:1295-1330
//
// 新增: DSH 是 `Promise.race([whenIdle(), poked])`。Go 的
// [github.com/snight1983/ds-harness-go/harness/agent.Agent.WhenIdle] 是阻塞调用，所以这里把它放进一条自己的
// 线，用一个派生 ctx 掐掉输的那一边，并且在下一轮之前等它退干净——不等就是每一圈
// 漏一条 goroutine。
func (m *ContinuationManager) watchSettlement(target *activation) {
	go func() {
		ctx := context.Background()
		for {
			m.mutex.Lock()
			disposal := target.disposal
			poked := target.poke
			m.mutex.Unlock()
			if disposal != nil {
				return
			}

			idleCtx, stopIdle := context.WithCancel(ctx)
			idle := make(chan struct{})
			go func() {
				defer close(idle)
				_ = target.handle.Agent.WhenIdle(idleCtx)
			}()
			select {
			case <-idle:
			case <-poked:
			}
			stopIdle()
			<-idle

			m.mutex.Lock()
			if target.disposal != nil {
				m.mutex.Unlock()
				return
			}
			m.mutex.Unlock()

			// 在孩子锁**里面**重查一次结清，并且在同一段临界区里开出那次处置，于是
			// 一次并发的投递要么在事务开出来之前赢下准入、要么等放开之后冷恢复。
			// 在锁外做这个判断，会让一次投递看见一个这个守望已经准备拆掉的句柄。
			release, err := m.locks.acquire(ctx, target.childID)
			if err != nil {
				// 走不到：ctx 是 Background，占不上只有取消一种来由。
				return
			}
			m.mutex.Lock()
			var transaction *disposalTx
			settling := target.disposal == nil && m.stateOfLocked(target) == stateSettled
			if settling {
				// 事务是同步赋上的，所以这段临界区放开之前准入就已经关掉了。
				transaction, _ = m.beginDisposalLocked(target)
			}
			m.mutex.Unlock()
			release()

			if !settling {
				// 还在跑，或者还等着后代：等下一条被接受的消息、或者下一次所有权
				// 释放之后再看。
				if target.handle.Agent.Status() != agent.StatusRunning {
					<-poked
				}
				continue
			}
			m.startTeardown(target, transaction)
			if err := transaction.wait(ctx); err != nil {
				m.warn("子 agent 活化拆解失败了", "孩子", string(target.childID), "错误", err)
			}
			return
		}
	}()
}

// beginDisposalLocked 开出这次活化那唯一一份处置事务，第二个返回值说的是「这一次
// 是不是由我开出来的」。调用方持锁。
//
// 源: packages/subagent/subagent/src/continuation.ts:1343-1352
//
// 「在不在」就是准入闸，所以它必须在那条真的拆解跑起来之前、在锁里同步赋上。
func (m *ContinuationManager) beginDisposalLocked(target *activation) (*disposalTx, bool) {
	if target.disposal != nil {
		return target.disposal, false
	}
	target.disposal = newDisposalTx()
	return target.disposal, true
}

// dispose 立刻停掉一次活化，然后孩子优先地把它放掉。那份记下来的事务在取消和递归
// 回调之前就装上了，于是准入和重入的拆解汇合到同一个主人身上。
//
// 源: packages/subagent/subagent/src/continuation.ts:1343-1352
//
// 最后那次会话刷盘是尽力而为的，它绝不挡着句柄处置或者所有权释放——留住一个孩子
// 会把它的祖先永久钉在 waiting 上。
func (m *ContinuationManager) dispose(target *activation) *disposalTx {
	m.mutex.Lock()
	transaction, opened := m.beginDisposalLocked(target)
	m.mutex.Unlock()
	if opened {
		m.startTeardown(target, transaction)
	}
	return transaction
}

// startTeardown 同步地把停传下去，剩下那段孩子优先的释放交给一条后台线跑完。
//
// 源: packages/subagent/subagent/src/continuation.ts:1359-1369
//
// 新增: DSH 那个 async 函数在第一个 await 之前就把 wake 和 cancel 做完了，于是
// `dispose()` 的调用方拿到的是一次已经同步传播出去的取消。Go 的函数没有那种「跑到
// 第一个挂起点」的语义，所以这里显式拆成两半：这一半同步做完，另一半进 goroutine。
// [ContinuationManager.DrainDescendants] 那句「在同一段同步跨度里自上而下地传播
// 取消」靠的正是这个拆法。
func (m *ContinuationManager) startTeardown(target *activation, transaction *disposalTx) {
	m.wake(target)
	// 在第一个可中断点之前自上而下地停掉。后代清理得慢可以拖着释放，但拖不住这个
	// 祖先继续跑模型或者工具。
	target.handle.Agent.Cancel(sessionlog.ParentCancel{}, agent.CancelOptions{})
	go func() { transaction.settle(m.finishDisposal(target)) }()
}

// finishDisposal 走完那段孩子优先的释放。
//
// 源: packages/subagent/subagent/src/continuation.ts:1359-1442
//
// 新增: 整段走 context.Background()。这次拆解归管理器自己所有，一个调用方的取消
// 掐掉它，会留下一个已经从活表里摘不掉的孩子；调用方能取消的只有**等**这一下，
// 见 [disposalTx.wait]。
func (m *ContinuationManager) finishDisposal(target *activation) error {
	ctx := context.Background()
	childID := target.childID

	m.mutex.Lock()
	children := make([]*activation, 0, len(target.ownedChildren))
	for child := range target.ownedChildren {
		if resident, live := m.activations[child]; live {
			children = append(children, resident)
		}
	}
	m.mutex.Unlock()

	transactions := make([]*disposalTx, 0, len(children))
	for _, child := range children {
		transactions = append(transactions, m.dispose(child))
	}

	var failures []error
	// 取消是自上而下传的，释放仍旧孩子优先：名下每个孩子都走完，才摘这个句柄。
	var childFailures []error
	for _, transaction := range transactions {
		if err := transaction.wait(ctx); err != nil {
			childFailures = append(childFailures, err)
		}
	}
	if len(childFailures) > 0 {
		failures = append(failures, NewError(
			`子 agent "`+string(childID)+`" 的孩子拆解失败了`,
			CodeActivationTeardownFailed, errors.Join(childFailures...),
		))
	}
	// 刷盘之前先静下来：一个还在跑的回合会继续追加这次刷盘覆盖不到的事件。
	if err := target.handle.Agent.WhenIdle(ctx); err != nil {
		failures = append(failures, NewError(
			`子 agent "`+string(childID)+`" 的活化拆解失败了`,
			CodeActivationTeardownFailed, err,
		))
	} else {
		m.flushFinalState(ctx, target)
		// 趁孩子还登记着，把那些依赖孩子的边数据拍下来：句柄一处置就把它摘掉了，
		// 而消费方要读它的日志和作用域。
		target.observer.capture(target.handle.Agent)
	}
	if err := target.handle.Dispose(ctx); err != nil {
		failures = append(failures, NewError(
			`子 agent "`+string(childID)+`" 的活化句柄处置失败了`,
			CodeActivationTeardownFailed, err,
		))
	}

	var failure error
	switch len(failures) {
	case 0:
	case 1:
		failure = failures[0]
	default:
		failure = NewError(
			fmt.Sprintf(`子 agent "%s" 的活化拆解在 %d 处边界上失败了`, string(childID), len(failures)),
			CodeActivationTeardownFailed, errors.Join(failures...),
		)
	}
	// 到这一刻这次活化才算没了：条目留到处置落定，会让一次赛跑中的投递等着放开，
	// 而不是对着一个还登记着的 agent 冷恢复。
	m.mutex.Lock()
	delete(m.activations, childID)
	m.mutex.Unlock()
	// 在放掉所有权**之前**投，此时父还数着这个孩子、因此判不成结清。放开之后再投
	// 会和父那个守望赛跑：它紧接着醒来、发现自己无子又安静，于是处置掉一个 agent，
	// 而那次处置的 cancel 清掉的正是这条通知待着的收件箱。
	m.notifySettlement(target, target.observer.terminal(failure))
	// 失败也要放掉所有权：一个留着的失败孩子会把它整条祖先链永远钉在 waiting 上。
	m.releaseOwnership(childID)
	// 等处置的结局明朗之后再发，于是一次拒掉的带作用域清理不会被报成一轮成功的驻留。
	target.observer.settle(failure)
	return failure
}

// notifySettlement 告诉那个耐久的直系父：这个孩子该产出的都产出了。
//
// 源: packages/subagent/subagent/src/continuation.ts:1462-1511
//
// 对每一个调用方拿到过 id 的孩子都无条件投：它**不**看这个孩子有没有汇报过，
// 因为最需要这条通知的那几种情况——token 到顶、模型失败、被取消、拆解——恰恰是
// 孩子根本没机会选的。一次在首次接受之前就回滚掉的物化保持沉默，因为调用方被告知
// 的是那个孩子没有立起来。父不在了不算错；孩子自己那份会话无论如何都是耐久记录。
// 一个自己血统已经在关的父收到这条通知，但**不**被唤醒——拆解不是开回合的理由。
//
// 绝不挡着处置。投递失败记一条日志就丢掉：留住一个孩子去重试一条通知，会把它整条
// 血统永远钉在 waiting 上。
func (m *ContinuationManager) notifySettlement(target *activation, terminal activationTerminal) {
	if !target.announced {
		return
	}
	parent, live := m.deps.Agents.Get(target.parentSession)
	if !live {
		return
	}
	summary := settlementSummary(target.childID, terminal.StopReason)
	source, err := NewSettledSource(target.childID, summary)
	if err != nil {
		// 走不到：这个 id 非空（一份活化必然有），而剩下那一步是 marshalSenderExtra
		// 里那次转不失败的编码。fail-soft 照旧留着：绝不因为一条通知挡住处置。
		m.warn("子 agent 的结清通知没有投给它的父", "孩子", string(target.childID), "错误", err)
		return
	}
	// 给模型看的载荷，所以保持英文。
	body := llm.Content{llm.TextBlock{Text: summary}}
	if terminal.Output == nil {
		body = append(body, llm.TextBlock{Text: "It left no closing message."})
	} else {
		body = append(body, llm.TextBlock{Text: "Its closing message:"})
		body = append(body, terminal.Output...)
	}
	message := llm.NewUserMessage(body, source)
	// 一个自己的拆解已经开了的父不许被唤醒。唤醒不是一次入队操作：对一个静止的
	// agent 调 Followup 会开一个回合，而 Cancel 并不对之后的回合设防，于是一条在
	// 拆解期间到的通知会为一个宿主马上就要处置掉的 agent 花掉一次模型请求——而且
	// 每一层树各花一次，因为每一层自己那条通知又会唤醒上面那一层。Inject 投给的是
	// 一个还在读收件箱的父，两种做法都会把这份交代记进日志；它**不**熬得过那个父
	// 自己的处置——那次 cancel 不留收件箱，会把它没认领的东西耐久地清掉。
	if root, managerWide := m.closingTeardown(parent); managerWide || root != nil {
		parent.Inject(message)
		return
	}
	// 一个静止的父没有别的可看，所以它得到一个普通回合。一个忙着的父被 Steer 而不是
	// 被唤醒：收件箱一次认领会把整批 next-step 在同一道边界上取走，于是几个孩子一起
	// 结清只花一个步骤，而不是一人一个回合。用 Steer 而不是 Inject，堵的是「状态读完
	// 到真的送出去」之间驱动退场、把这条通知晾成没人认领」的那个窗口。
	m.sendWaking(parent, message.ID, func() {
		if parent.Status() == agent.StatusIdle {
			parent.Followup(message)
		} else {
			parent.Steer(message)
		}
	})
}

// flushFinalState 在孩子静下来之后请一次尽力而为的最后刷盘。监听方失败只记日志，
// 因为参与刷盘这件事认不出具体是哪一个持久化后端，而拆解仍旧必须把所有权放掉。
//
// 源: packages/subagent/subagent/src/continuation.ts:1519-1529
func (m *ContinuationManager) flushFinalState(ctx context.Context, target *activation) {
	child := target.handle.Agent
	if _, err := m.deps.Sessions.Flush(ctx, child.Session()); err != nil {
		m.warn(
			"子 agent 那次尽力而为的最后会话刷盘没成，恢复时那份落盘状态可能用不了或者陈旧",
			"孩子", string(target.childID), "错误", err,
		)
	}
}
