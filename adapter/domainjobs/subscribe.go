// 本文件的作用：挂在作用域上的那三样东西——作业控制器、完成监听器、变化观察者——
// 以及按属主把通知投给该收的那几个。
//
// 新增: 这一套和 [github.com/snight1983/ds-harness-go/adapter/localjobs] 里那一套是同一份逻辑，
// 这里重写了一遍而不是抽一个公共件：那边整个包是
// packages/jobs/jobs-local/src/index.ts 的逐行移植，由 internal/devtools/portcheck 钉着，
// 把它的一半搬进一个共享包会让那份对照关系断在半路，而换来的只是这一个调用方少写
// 一百行。这一百行本身没有分歧——两台注册表的**分层订阅语义完全相同**，
// 分歧全在账本那一侧。
//
// 这三样东西都是**副本本地**的：它们挂在这个进程的作用域上，也只收得到这个进程
// 发出的通知。跨副本的变更广播要一套发布订阅，本轮不做，见 doc.go。

package domainjobs

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
)

// jobLayer 是一个作用域在这台注册表上的全部贡献。三张表都是匿名的——一次贡献由
// 它自己那个撤销函数认定，从来不靠一个后来者能顶掉的名字。
type jobLayer struct {
	// controllers 是挂在这一层的那些作业控制器，值是它们的诊断标签。
	controllers *scope.AnonymousEntries[string]
	// listeners 是登记在这一层的完成监听器。
	listeners *scope.AnonymousEntries[jobs.DoneListener]
	// changed 是登记在这一层的「可见集合变了」观察者。
	changed *scope.AnonymousEntries[jobs.ChangedListener]
}

// newJobLayer 造一层。
func newJobLayer() *jobLayer {
	return &jobLayer{
		controllers: scope.NewAnonymousEntries[string](),
		listeners:   scope.NewAnonymousEntries[jobs.DoneListener](),
		changed:     scope.NewAnonymousEntries[jobs.ChangedListener](),
	}
}

// IsEmpty 表示这一层的每一张表都空了，[scope.Layers] 靠它回收空层。
func (l *jobLayer) IsEmpty() bool {
	return l.controllers.IsEmpty() && l.listeners.IsEmpty() && l.changed.IsEmpty()
}

// OnJobDone 登记一个按作用域圈定的完成监听器。
//
// 只收得到**本副本**结算的那些作业，理由见本文件开头。
func (r *Registry) OnJobDone(
	ctx context.Context,
	owner *scope.Scope,
	listener jobs.DoneListener,
) (func(context.Context) error, error) {
	if listener == nil {
		return nil, fmt.Errorf("domainjobs: OnJobDone 需要一个监听器")
	}
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.listeners.Append(listener), nil
	}, scope.EffectOptions{Label: "jobs.OnJobDone()"})
}

// OnJobsChanged 登记一个按作用域圈定的「可见集合变了」观察者。
//
// 同样只收得到本副本引起的变化：别的副本开了一件属于这个属主的活儿，这里一声不响。
func (r *Registry) OnJobsChanged(
	ctx context.Context,
	owner *scope.Scope,
	listener jobs.ChangedListener,
) (func(context.Context) error, error) {
	if listener == nil {
		return nil, fmt.Errorf("domainjobs: OnJobsChanged 需要一个观察者")
	}
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.changed.Append(listener), nil
	}, scope.EffectOptions{Label: "jobs.OnJobsChanged()"})
}

// AttachController 挂上一个按作用域圈定的作业控制器。
func (r *Registry) AttachController(
	ctx context.Context,
	owner *scope.Scope,
	name string,
) (func(context.Context) error, error) {
	return r.layers.Effect(ctx, owner, func(layer *jobLayer) (func(), error) {
		return layer.controllers.Append(name), nil
	}, scope.EffectOptions{Label: "jobs.AttachController()"})
}

// servesOwner 说有没有一个够得着的控制器，收得走也停得下这个属主的活儿。
//
// 源: packages/jobs/jobs-local/src/index.ts:315-319
//
// 全局层里是所有从无身份作用域挂上来的控制器——宿主组合自己那套控制——所以它
// 服务每一个属主；一个圈了作用域的控制器只服务组合在它底下的那些 agent。
//
// 这道闸只管**开工**，而开工只发生在本副本，所以它是副本本地的这件事在这里不是
// 一个折中：起活儿的资源本来就在这儿。
func (r *Registry) servesOwner(owner agent.Agent) bool {
	if !r.layers.Global().controllers.IsEmpty() {
		return true
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		if !layer.controllers.IsEmpty() {
			return true
		}
	}
	return false
}

// scopeKeyOf 取一个属主的作用域钥匙，无主作业交回 nil。
func scopeKeyOf(owner agent.Agent) *scope.Key {
	if owner == nil {
		return nil
	}
	return owner.Scope().Key()
}

// listenersFor 给出该收这次结算的那些完成监听器：先全局层，再属主链上各层。
//
// 源: packages/jobs/jobs-local/src/index.ts:338-342
//
// 链外的监听器属于另一份组合，不许投递——否则这个属主每装一份预设就多读一条通知。
func (r *Registry) listenersFor(owner agent.Agent) []jobs.DoneListener {
	var chosen []jobs.DoneListener
	for listener := range r.layers.Global().listeners.Values() {
		chosen = append(chosen, listener)
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		for listener := range layer.listeners.Values() {
			chosen = append(chosen, listener)
		}
	}
	return chosen
}

// changedFor 给出该收这次变化的那些观察者，取法同 [Registry.listenersFor]。
//
// 源: packages/jobs/jobs-local/src/index.ts:388-392
func (r *Registry) changedFor(owner agent.Agent) []jobs.ChangedListener {
	var chosen []jobs.ChangedListener
	for listener := range r.layers.Global().changed.Values() {
		chosen = append(chosen, listener)
	}
	for _, layer := range r.layers.ChainLayers(scopeKeyOf(owner)) {
		for listener := range layer.changed.Values() {
			chosen = append(chosen, listener)
		}
	}
	return chosen
}

// notifyChanged 宣布某一个属主的可见集合变了。每个观察者都被包住：一个观察者
// 不该掀翻一次已经发生了的账本提交。
//
// 必须在**锁外**调：观察者回头调 [Registry.List] 是完全正常的事，而那会去读账本。
func (r *Registry) notifyChanged(owner agent.Agent) {
	for _, listener := range r.changedFor(owner) {
		r.callChanged(listener, owner)
	}
}

// callChanged 跑一个观察者，把它抛出来的东西关在里面。
func (r *Registry) callChanged(listener jobs.ChangedListener, owner agent.Agent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("domainjobs: onJobsChanged 观察者抛了", "error", recovered)
		}
	}()
	listener(owner)
}

// callDone 跑一个完成监听器，把它抛出来的东西关在里面。
func (r *Registry) callDone(listener jobs.DoneListener, snapshot jobs.Snapshot, owner agent.Agent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("domainjobs: onJobDone 监听器抛了", "job", snapshot.ID, "error", recovered)
		}
	}()
	listener(snapshot, owner)
}
