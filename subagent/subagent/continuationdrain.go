// 本文件的作用：那三种拆解——整台管理器排干、按确切的宿主根排干它的后代、
// 以及点名放掉一个父的几个直系孩子；连同血统解算、准入闸和驻留状态这几样判定。
//
// 源: packages/subagent/subagent/src/continuation.ts:720-940

package subagent

import (
	"context"
	"errors"
	"fmt"

	"ds-harness-go/core/agent"
	"ds-harness-go/session"
)

// Drain 关掉准入、等每一次已经被准入的物化走到公布或者回滚，然后**孩子优先**地
// 处置那片已经稳定下来的活活化森林。
//
// 源: packages/subagent/subagent/src/continuation.ts:720-743
//
// 兄弟分支各排各的：一个失败会被记下来，但绝不妨碍剩下那些句柄被试到，
// 而那份汇总的失败要等每一条分支都有结局之后才报。
//
// 新增: 它的签名**恰好**是本仓库 disposer 的形状（收 ctx、交 error），所以可以
// 直接当 [ds-harness-go/core/scope.Scope.Defer] 的清理交出去，见
// [NewContinuationManager]。
func (m *ContinuationManager) Drain(ctx context.Context) error {
	// 在第一个可中断点**之前**同步关掉准入。已经过了那道闸的物化仍旧跟着，
	// 直到它的句柄装上、或者回滚做完，于是后面那次快照拍到的是一片稳定的森林。
	m.mutex.Lock()
	m.draining = true
	pending := m.pendingMaterializationsLocked()
	m.mutex.Unlock()

	if err := waitMaterializations(ctx, pending); err != nil {
		return err
	}

	// 关掉准入之后再拍根：一个根就是「没有任何活活化拥有它」的活化，所以处置根
	// 会孩子优先地递归进整片森林。
	m.mutex.Lock()
	owned := map[session.SessionID]struct{}{}
	for _, candidate := range m.activations {
		for child := range candidate.ownedChildren {
			owned[child] = struct{}{}
		}
	}
	var roots []*activation
	for _, candidate := range m.activations {
		if _, isOwned := owned[candidate.childID]; !isOwned {
			roots = append(roots, candidate)
		}
	}
	m.mutex.Unlock()

	return m.disposeRoots(ctx, roots, "活化")
}

// DrainDescendants 只停掉那几个确切的活宿主父底下的可续后代。
//
// 源: packages/subagent/subagent/src/continuation.ts:745-806
//
// 那几棵父树的准入一直关到「那个确切的父离开 agent 注册表」为止；不相干的树、
// 以及整台管理器的准入，都照常开着。
func (m *ContinuationManager) DrainDescendants(ctx context.Context, parents []agent.Agent) error {
	roots := map[agent.Agent]struct{}{}
	for _, parent := range parents {
		if parent == nil {
			continue
		}
		if live, found := m.deps.Agents.Get(parent.ID()); found && live == parent {
			roots[parent] = struct{}{}
		}
	}
	if len(roots) == 0 {
		return nil
	}

	m.mutex.Lock()
	// 在第一个可中断点之前把这道带范围的准入闸公布出去。和同一个确切根上更早的
	// 那次调用**合并**，于是两次汇合的排干不会漏掉那些释放已经在路上的后代。
	for root := range roots {
		m.closingMembersLocked(root)[root] = struct{}{}
	}
	var targets []*activation
	for _, candidate := range m.activations {
		lineage := m.liveLineage(candidate.handle.Agent)
		// 只要**严格**后代：一个可续 agent 自己也可能是一个宿主拥有的根，
		// 而那个根的句柄仍旧归它的宿主负责。
		var owners []agent.Agent
		for root := range roots {
			if candidate.handle.Agent == root {
				continue
			}
			if _, inLineage := candidate.ancestry[root]; inLineage {
				owners = append(owners, root)
			}
		}
		if len(owners) == 0 {
			continue
		}
		targets = append(targets, candidate)
		for _, owner := range owners {
			members := m.closingMembersLocked(owner)
			members[candidate.handle.Agent] = struct{}{}
			for _, member := range lineage {
				members[member] = struct{}{}
			}
		}
	}
	var pending []*materialization
	for candidate := range m.materializations {
		var owned bool
		for root := range roots {
			if !containsAgent(candidate.lineage, root) {
				continue
			}
			owned = true
			members := m.closingMembersLocked(root)
			for _, member := range candidate.lineage {
				members[member] = struct{}{}
			}
		}
		if owned {
			pending = append(pending, candidate)
		}
	}
	ownedTargets := map[session.SessionID]struct{}{}
	for _, candidate := range targets {
		for child := range candidate.ownedChildren {
			ownedTargets[child] = struct{}{}
		}
	}
	var targetRoots []*activation
	for _, candidate := range targets {
		if _, isOwned := ownedTargets[candidate.childID]; !isOwned {
			targetRoots = append(targetRoots, candidate)
		}
	}
	m.mutex.Unlock()

	// 在物化那道栅栏之前就把每一次选中的事务开出来。处置在同一段同步跨度里
	// 自上而下地传播取消；句柄的释放仍旧是孩子优先。
	for _, candidate := range targets {
		m.dispose(candidate)
	}

	if err := waitMaterializations(ctx, pending); err != nil {
		return err
	}
	return m.disposeRoots(ctx, targetRoots, "带范围的活化")
}

// DrainChildren 点名放掉一个确切的活父那几个驻留着的直系孩子，而**不**关掉这个父
// 其余可续孩子的准入。名下的后代经同一条生命周期递归释放。
//
// 源: packages/subagent/subagent/src/continuation.ts:808-842
//
// 一个驻留着的目标不是这个父的直系可续孩子、或者父的身份已经陈旧时，
// 报 [CodeUnauthorized]。
func (m *ContinuationManager) DrainChildren(
	ctx context.Context,
	parent agent.Agent,
	childIDs []session.SessionID,
) error {
	if parent == nil {
		return errInvalidRequestf("点名放掉孩子需要那个确切的活父 agent")
	}
	if live, found := m.deps.Agents.Get(parent.ID()); !found || live != parent {
		return NewError("点名拆掉孩子需要那个确切的活父 agent", CodeUnauthorized, nil)
	}
	m.mutex.Lock()
	var targets []*activation
	seen := map[session.SessionID]struct{}{}
	for _, childID := range childIDs {
		if _, duplicate := seen[childID]; duplicate {
			continue
		}
		seen[childID] = struct{}{}
		candidate, resident := m.activations[childID]
		if !resident {
			continue
		}
		_, inLineage := candidate.ancestry[parent]
		if candidate.parentSession != parent.ID() || !inLineage {
			m.mutex.Unlock()
			return NewError(
				`子 agent "`+string(childID)+`" 不是 agent "`+string(parent.ID())+`" 的直系孩子`,
				CodeUnauthorized, nil,
			)
		}
		targets = append(targets, candidate)
	}
	m.mutex.Unlock()

	// 在第一个可中断点之前把每一次事务都开出来，于是取消在一段同步跨度里
	// 传遍这几个选中的根。
	for _, candidate := range targets {
		m.dispose(candidate)
	}
	return m.disposeRoots(ctx, targets, "点名的活化")
}

// disposeRoots 处置几个互不相干的根，等它们全部有结局之后，才报每一条分支的失败。
//
// 源: packages/subagent/subagent/src/continuation.ts:844-865
//
// 新增: DSH 把每个失败过一遍 errorChain 再用 '; ' 拼成一句话。Go 里
// [errors.Join] 就是这件事，而且拼出来的东西仍旧 [errors.Is] 得出每一个原因。
func (m *ContinuationManager) disposeRoots(
	ctx context.Context,
	roots []*activation,
	failureSubject string,
) error {
	transactions := make([]*disposalTx, 0, len(roots))
	for _, root := range roots {
		transactions = append(transactions, m.dispose(root))
	}
	var failures []error
	for _, transaction := range transactions {
		if err := transaction.wait(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return NewError(
		fmt.Sprintf("可续子 agent 拆解在 %d 个%s上失败了", len(failures), failureSubject),
		CodeActivationTeardownFailed, errors.Join(failures...),
	)
}

// pendingMaterializationsLocked 拍下当下那些还没走完的物化。调用方持锁。
func (m *ContinuationManager) pendingMaterializationsLocked() []*materialization {
	pending := make([]*materialization, 0, len(m.materializations))
	for candidate := range m.materializations {
		pending = append(pending, candidate)
	}
	return pending
}

// waitMaterializations 等一批物化各自走到公布或者回滚。
//
// 新增: DSH 是 `await Promise.all(...)`。Go 这边逐个 select 等，顺带把调用方的
// 取消接进来——DSH 那几处 await 没有取消口，一次卡住的物化会把排干一起卡住。
func waitMaterializations(ctx context.Context, pending []*materialization) error {
	for _, candidate := range pending {
		select {
		case <-candidate.settled:
		case <-ctx.Done():
			return NewError("等子 agent 物化落定时被取消了", CodeCancelled, ctx.Err())
		}
	}
	return nil
}

// closingMembersLocked 取出（没有就建出）一个确切的带范围拆解根那份留住的成员集。
// 调用方持锁。
//
// 源: packages/subagent/subagent/src/continuation.ts:867-874
func (m *ContinuationManager) closingMembersLocked(root agent.Agent) map[agent.Agent]struct{} {
	members, found := m.closingScopes[root]
	if !found {
		members = map[agent.Agent]struct{}{}
		m.closingScopes[root] = members
	}
	return members
}

// liveLineage 从 from 往上，交出当下解得出来的那条确切血统。
//
// 源: packages/subagent/subagent/src/continuation.ts:876-893
//
// 第一项**始终**是传进来的那个身份，哪怕它已经陈旧；它之后的每一个祖先都必须是
// 注册表当下那一条确切条目。
func (m *ContinuationManager) liveLineage(from agent.Agent) []agent.Agent {
	lineage := []agent.Agent{from}
	seen := map[session.SessionID]struct{}{from.ID(): {}}
	parentSession := from.Session().Header().ParentSession
	for parentSession != "" {
		parent, live := m.deps.Agents.Get(parentSession)
		if !live {
			break
		}
		if _, repeated := seen[parent.ID()]; repeated {
			break
		}
		lineage = append(lineage, parent)
		seen[parent.ID()] = struct{}{}
		parentSession = parent.Session().Header().ParentSession
	}
	return lineage
}

// containsAgent 说的是一条血统里有没有这个确切身份。
func containsAgent(lineage []agent.Agent, wanted agent.Agent) bool {
	for _, member := range lineage {
		if member == wanted {
			return true
		}
	}
	return false
}

// closingTeardown 是把这个 agent 那条血统的可续准入关掉的那次拆解。
//
// 源: packages/subagent/subagent/src/continuation.ts:895-909
//
// 第二个返回值为真表示是整台管理器在排干；那种情况下第一个返回值是 nil。
// 两个都是零值表示准入还开着。
//
// 新增: DSH 交回的是 `Agent | 'manager' | undefined` 这种字面量联合。Go 没有它，
// 所以拆成「那个根」加一个「是不是整台管理器」的布尔。
func (m *ContinuationManager) closingTeardown(subject agent.Agent) (agent.Agent, bool) {
	// 血统要在锁外解：它要读 agent 注册表，而那是外面的服务。
	lineage := m.liveLineage(subject)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.draining {
		return nil, true
	}
	for root, members := range m.closingScopes {
		if _, member := members[subject]; member {
			return root, false
		}
		if containsAgent(lineage, root) {
			return root, false
		}
	}
	return nil, false
}

// assertAdmitting 在整台管理器、或者这棵确切的父树已经开始排干之后，拒掉新的准入。
//
// 源: packages/subagent/subagent/src/continuation.ts:911-921
func (m *ContinuationManager) assertAdmitting(subject agent.Agent) error {
	root, managerWide := m.closingTeardown(subject)
	if managerWide {
		return NewError("可续子 agent 正在排干，这次操作没有被准入", CodeDraining, nil)
	}
	if root == nil {
		return nil
	}
	return NewError(
		`父 "`+string(root.ID())+`" 底下的可续子 agent 正在排干，这次操作没有被准入`,
		CodeDraining, nil,
	)
}

// stateOfLocked 从 agent 的静止程度和它名下那组孩子推出驻留状态。调用方持锁。
//
// 源: packages/subagent/subagent/src/continuation.ts:923-936
//
// 光看 [ds-harness-go/core/agent.Agent.Status] 不够：在「一次唤醒投递被接受」和
// 「准入它的那一跳」之间它一直是 idle，于是一个同步的收件箱观察者会在回合已经排上
// 的时候看到 settled。accepted 装的正是这个管理器已经准入、却还没看着它离场的那些 id。
func (m *ContinuationManager) stateOfLocked(target *activation) activationState {
	if target.handle.Agent.Status() == agent.StatusRunning || len(target.accepted) > 0 {
		return stateRunning
	}
	if len(target.ownedChildren) > 0 {
		return stateWaiting
	}
	return stateSettled
}
