// Package planmode 把「计划模式」做成逐 agent 的、记在日志里的协作状态。
//
// 源: packages/plan/plan-mode/src/index.ts:1-24
//
// 计划模式开着的时候，每一次模型请求里都多一段部署方拥有的指引；
// [ExitToolName] 这件工具把写好的计划摆到用户面前评审，同意了就离开计划模式；
// `/plan off` 让用户自己直接走。沙箱模式和审批策略各自独立地施加限制，
// 它们**不读也不写**计划状态。
//
// # 状态在日志里，没有第二份
//
// 当下生效的那个状态是从会话日志折出来的（[EventMode]，最后一条算数），
// 所以恢复和分叉不需要任何活着的镜像就能还原它——见 [FoldMode]。
//
// # 用户选的东西为什么要等
//
// 用户的选择先挂起来，等到**下一个被接受的、回合之内的**步骤前置才落进日志。
// 这条延迟不是保守，是必须的：一次请求的装配（系统提示词、工具表）发生在步骤
// 开头，而计划指引正是装配的一部分。如果在一个回合的中途直接翻状态，同一个回合里
// 先后两次请求会带着互相矛盾的指引，而模型看到的抄本里没有任何东西解释这件事。
//
// 回合之间选的那一下则**立刻**落盘：那时候不会再有回合之内的步骤前置来接它了。
// 判断依据是日志里有没有一个开着的回合（[hasOpenTurn]），不是 agent 的状态——
// 一个 agent 在回合结束后的检查点期间状态仍然是 running，而那时已经不会再有
// 回合之内的步骤前置。
//
// # 退出工具一直挂在那儿
//
// 不管计划模式开着还是关着，[ExitToolName] 都保持注册。进出计划模式只改那一段
// 提示词，不改请求里的工具表——工具表一变，提示词缓存就整段作废。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 那个 PlanModeController 是一个 cordis 服务，构造函数里就把五条胳膊
// （步骤前置、提示词段落、投影单元、命令、工具）全装上了，装不装取决于容器里有
// 没有那个服务。Go 里没有那个容器，所以拆成两步：[New] 只验配置造出这个值，
// [Controller.Install] 收一份 [Deps] 把胳膊装上去并交出一个摘除函数。
// Deps 里必填的三样对应 DSH 的 `static inject = ['tools','systemPrompt']` 加上那条
// 无条件的 agent/pre-step 订阅；可以为 nil 的三样对应 DSH 的 ctx.inject 子节点和
// ctx.get——不给就是这个装配里没有那条能力，别的胳膊照装。
//
// 新增: DSH 用 `WeakMap<Session, ...>` 记挂起的选择，会话被回收时那一条跟着没了。
// Go 没有弱引用，所以换成一张按 [github.com/snight1983/ds-harness-go/session.SessionID] 索引的表，
// 由装配方在会话散掉时调 [Controller.OnSessionDisposed] 清理（成例见
// [github.com/snight1983/ds-harness-go/session/sessiontitle.Service.OnSessionDisposed]）。不清理的后果
// 只是一条永远不会被读到的挂起记录，不会影响任何别的会话。
//
// 新增: DSH 的 exit 工具、`/plan` 命令、提示词段落都从一个结构类型的 agent 对象上
// 直接摸到 `agent.session`。Go 这几处拿到的是一把不透明的
// [github.com/snight1983/ds-harness-go/core/scope.Key]，所以由装配方经 [Config.AgentOf] 交进来一条
// 「从钥匙找 agent」的路（成例见
// [github.com/snight1983/ds-harness-go/interaction/commands.Options.LogOf]、
// [github.com/snight1983/ds-harness-go/todo.Config.Append]）。
//
// 新增: DSH 的 `session.append` 抛异常，`set` 不接，让它一路抛给调用方。Go 里
// [Controller.Set] 多一个返回的 error，理由和别处一样：一次没能落盘的状态翻转
// 绝不许被报成成功，否则用户以为自己已经进了计划模式，而下一次请求里那段指引
// 根本不在。
package planmode
