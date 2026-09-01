// 本文件的作用：后台作业注册表那条服务缝本身——实现方要提供哪几个方法，
// 每一个方法的契约是什么。
//
// 源: packages/jobs/jobs/src/index.ts:62-179

package jobs

import (
	"context"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
)

// KillResult 是一次 [Registry.Kill] 的结果。
//
// 源: packages/jobs/jobs/src/index.ts:120
type KillResult string

const (
	// KillRequested 表示活儿还在跑，取消已经请求出去了。
	KillRequested KillResult = "requested"
	// KillAlreadyFinished 表示这件作业早就落定了，这次是一次被接受的空操作。
	KillAlreadyFinished KillResult = "already-finished"
)

// Registry 是后台作业注册表这条缝。进程内那台实现在 github.com/snight1983/ds-harness-go/jobs/localjobs。
//
// 源: packages/jobs/jobs/src/index.ts:62-177
//
// 新增: DSH 是一个抽象类，子类当插件装上去就注册成 ctx.jobs。Go 这边它是一个
// 接口，装配方把实现直接交给需要它的那几个包（成例见
// [github.com/snight1983/ds-harness-go/subagent/reporttool.Service]）。DSH 那道
// `new.target === JobRegistry` 的守卫因此没有对应物：接口实例化不了。
type Registry interface {
	// Start 先把访问、校验、属主清理和实现方自己那道准入过一遍，然后起活儿并
	// 原子地登记它。预检拒掉的话不会留下任何作业 id 或者执行资源；[Start.Run]
	// 出错的话什么都不会被登记，而它一旦返回，登记就不可能再失败。结算会记下
	// 结局、通知监听器、放开等待者。
	//
	// 源: packages/jobs/jobs/src/index.ts:82
	Start(spec Start) (JobID, error)

	// List 按登记顺序列出调用方自己的和无主的那些作业，不外露别的会话那些标签。
	// caller 为 nil 的调用方只看得见无主作业。
	//
	// 源: packages/jobs/jobs/src/index.ts:90
	List(caller agent.Agent) []Snapshot

	// Get 交回一份不消费的快照，既不动它的读游标也不动它的通知状态。
	// 认不得的 id 和别人的作业都报错。
	//
	// 源: packages/jobs/jobs/src/index.ts:99
	Get(id JobID, caller agent.Agent) (Snapshot, error)

	// Read 读下一段流式增量，或者结算之后那份幂等的最终输出。一次终态读会把这件
	// 作业标成已汇报。认不得的 id 和别人的作业都报错。
	//
	// 源: packages/jobs/jobs/src/index.ts:109
	Read(id JobID, caller agent.Agent) (Read, error)

	// Kill 请求取消，然后把作业标成 stopping 和已汇报。生产方出错要原样传上去，
	// 且不改作业状态。认不得的 id 和别人的作业都报错。reason 空串表示没给理由。
	//
	// 源: packages/jobs/jobs/src/index.ts:120
	Kill(id JobID, caller agent.Agent, reason string) (KillResult, error)

	// Wait 等到结算或者超时，不取消这件作业。调用方主动取消只在作业还活着的
	// 时候生效；落定之后终态快照赢，这样一份为这个等待者压掉的通知仍旧发得出去。
	// 参数不合法、认不得的 id 和别人的作业都报错。
	//
	// 源: packages/jobs/jobs/src/index.ts:133
	//
	// 新增: DSH 是 `wait(id, timeoutMs, caller?, signal?)`。Go 里那个 signal
	// 就是 ctx，而 timeout 是一个 [time.Duration]，必须是正的有限值。
	Wait(ctx context.Context, id JobID, timeout time.Duration, caller agent.Agent) (Snapshot, error)

	// OnJobDone 登记一个按作用域圈定的完成监听器。它收到的是**它登记时那个作用域
	// 罩得住的那些属主**的结算；每个监听器都被包住，服务释放之后一个都不再跑。
	//
	// 源: packages/jobs/jobs/src/index.ts:143
	//
	// 新增: DSH 的可见范围由 cordis 上下文的作用域隐式决定，交回一个 `() => void`。
	// Go 这边那个作用域是显式的 owner 参数，交回的也是本仓库统一的那种带 ctx 的
	// 释放函数（见 [github.com/snight1983/ds-harness-go/core/agent.Registry.OnCreated]）。owner 必填，
	// 想罩住每一个属主就交一个 [github.com/snight1983/ds-harness-go/core/scope.NewRoot] 造的无身份作用域
	// ——它落在全局层，和 DSH 把插件装在无作用域上下文上是同一件事。
	OnJobDone(
		ctx context.Context,
		owner *scope.Scope,
		listener DoneListener,
	) (func(context.Context) error, error)

	// OnJobsChanged 登记一个按作用域圈定的「可见集合变了」观察者。每一次让
	// [Registry.List] 对那个属主的结果发生变化的提交之后它都会响——登记、每一次
	// 转到 stopping（含拆除在等一个慢生产方之前做的那一次）、结算、属主释放带来的
	// 移除，以及服务释放提交的那次清空——所以观察者该重读，而不是攒增量。
	//
	// 源: packages/jobs/jobs/src/index.ts:167
	//
	// 它**不是** [Registry.OnJobDone] 的超集：那一个在先到先得的语义下投递终态
	// 记录，作业控制器把通知投递和它绑在一起；这一个不带任何投递含义，也不把
	// 任何东西标成已汇报。owner 的语义同 [Registry.OnJobDone]。
	OnJobsChanged(
		ctx context.Context,
		owner *scope.Scope,
		listener ChangedListener,
	) (func(context.Context) error, error)

	// AttachController 挂上一个按作用域圈定的、读得了也停得了作业的控制器。
	// 它服务它登记时那个作用域罩得住的那些属主，而 [Registry.Start] 会拒掉一个
	// 没有任何已挂控制器服务的属主。name 只是诊断标签，重名的仍旧互相独立。
	//
	// 源: packages/jobs/jobs/src/index.ts:176
	AttachController(
		ctx context.Context,
		owner *scope.Scope,
		name string,
	) (func(context.Context) error, error)
}
