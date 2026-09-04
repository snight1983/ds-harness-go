// 本文件的作用：「这条调用链是哪个 agent 发起的」这件事怎么顺着调用往下传、
// 怎么读回来、以及怎么故意断掉。
//
// 源: packages/core/agent/src/index.ts:300-358、620-706
//
// 新增: DSH 把这四个方法挂在 AgentRegistry 上，因为承载它们的那两个
// AsyncLocalStorage 是那个服务的实例状态，卸载时还要排干。Go 里承载它的是
// [context.Context]，和任何一张注册表都无关，所以这四个是包级函数——一段代码
// 想知道「谁发起的」，手上有 ctx 就够了，不必先找到那张表。

package agent

import "context"

// initiatorKey 是发起者在 ctx 上那个键。
//
// 空结构体加私有类型是 Go 里放 ctx 值的规矩：类型本身就是命名空间，别的包
// 造不出一个相等的键，所以谁都盖不掉、也读不走这个值。
type initiatorKey struct{}

// initiatorValue 是那个键上存的东西。
//
// 存的是一层包装而不是 [Agent] 本身，为的是让「明确清掉」和「从来没设过」
// 在读的一侧分得开：[WithoutInitiator] 存一个 Agent 为 nil 的包装，
// [context.Context.Value] 因此返回一个非 nil 的 *initiatorValue，
// 从而挡住继续往父 ctx 上找。直接存一个 nil 的 [Agent] 做不到这件事——
// 那个键上根本区分不出「存了个 nil」和「没存」。
type initiatorValue struct {
	agent Agent
}

// WithInitiator 派生一个把 agent 认作发起者的 ctx。
//
// 源: packages/core/agent/src/index.ts:329-343
//
// 自定义驱动和测试脚手架把自己那整段前台活儿裹进这个 ctx 里。一条从队列或者
// 线上收进来的活儿**先验身份、解出那个活着的 agent**，然后才配用这个函数；
// 这里两件事都不做。
//
// agent 出现在这里既不证明它还活着，也不代表任何授权——它只是一条归属线索。
//
// 新增: DSH 是 `withInitiator(agent, operation)`，把操作包起来跑，因为 ALS 的
// 边界只能这么划。Go 里边界就是 ctx 本身的传递范围，所以这里只派生不调用——
// 调用方拿到新的 ctx 往下传就是了。这也顺手去掉了 DSH 那条「操作返回的是不是
// Promise」的分支，以及它为一个恶意 @@species 准备的兜底。
//
// agent 为 nil 时等价于 [WithoutInitiator]：那是「没有发起者」唯一的表达，
// 另报一条错误只会让调用点多一段无事可做的分支。
func WithInitiator(parent context.Context, agent Agent) context.Context {
	return context.WithValue(parent, initiatorKey{}, &initiatorValue{agent: agent})
}

// WithoutInitiator 派生一个**藏起**任何继承来的发起者的 ctx。
//
// 源: packages/core/agent/src/index.ts:346-358
//
// 造惰性的共享定时器、队列泵、连接池维护、监视器、导出器时用它，免得它们认下
// 「碰巧第一个初始化了它们的那个 agent」。它清掉的只有发起者这条归属线索，
// 清不掉任何显式字段，也不接管、不排干任何游离的资源。
func WithoutInitiator(parent context.Context) context.Context {
	return context.WithValue(parent, initiatorKey{}, &initiatorValue{})
}

// CurrentInitiator 读出这条链继承下来的发起者。
//
// 源: packages/core/agent/src/index.ts:300-312
//
// 日志、追踪、度量、以及那些「有 agent 就记上、没有也照跑」的宿主归属用这一个。
// 一个父 agent 造子 agent 时，这里报的是因果上的父，而子 agent 是谁由
// [Agent.ID] 说。
//
// 第二个返回值为假表示这条链在任何发起者边界之外，或者落在一次
// [WithoutInitiator] 里面。
//
// 新增: 这个返回值就是 DSH 那个 ctx.accessor('agent') 想要的效果——读一个没有
// 发起者的 ctx 得到「没有」，而不是一次失败。
func CurrentInitiator(ctx context.Context) (Agent, bool) {
	value, present := ctx.Value(initiatorKey{}).(*initiatorValue)
	if !present || value.agent == nil {
		return nil, false
	}
	return value.agent, true
}

// RequireInitiator 读出发起者，没有就报 [ErrNoInitiator]。
//
// 源: packages/core/agent/src/index.ts:314-326
//
// 契约上就跑在某个驱动之下的私有辅助函数用它，或者一次「部署方规定不许匿名发出」
// 的对外请求用它。一条通用的、或者允许被直接调的路，该用 [CurrentInitiator]
// 或者干脆在自己的参数里显式收一个 agent。
func RequireInitiator(ctx context.Context) (Agent, error) {
	agent, present := CurrentInitiator(ctx)
	if !present {
		return nil, ErrNoInitiator
	}
	return agent, nil
}
