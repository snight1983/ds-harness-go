// Package agentteamtool 把 [github.com/snight1983/ds-harness-go/feature/agentteam]
// 那套团队能力包成模型看得见的十件工具，外加一段说清怎么用它们的策略指引。
//
// 源: packages/experimental/tool-agent-team/src/index.ts
//
// 十件工具分三组：花名册那组（spawn_teammate、list_agents、interrupt_agent）、
// 收件箱那组（send_message、followup_task、wait_agent）、任务板那组
// （team_task_create、team_task_list、team_task_get、team_task_update）。
//
// # 一个控制器管一支团队
//
// DSH 那边工具装在「每一个属于某支团队的 agent 作用域」上，装的时候现问
// `ctx.agentTeams.membership(agent)` 是哪支团队。Go 这边 [Controller.Install] 是
// 按作用域叫的，而且没有那张全局的成员关系表，所以团队身份改由装配方在
// [Config.Team] 上写死——**一个控制器服务一支团队**，装到一个不属于这支团队的
// 作用域上，每一次调用都会在解算发起人那一步拿到
// [github.com/snight1983/ds-harness-go/feature/agentteam.CodeNotMember]。
//
// # 这一层自己拦的两件事
//
// 本包绝大多数动作是原样转手，但有两处判断只在这一层做：
//
//	等待时限的上下界   服务那边只拒非正数（见 agentteam.Service.Wait 的说明），
//	                  10 秒到 1 小时这副界是说给模型听的话，落在这里。
//	起队友要队长身份   agentteam.Service.Spawn 收的是「谁当队长」而不是「谁在发起」，
//	                  一个队友叫它会另起一支以自己为队长的团队。DSH 那边
//	                  spawnTeammate 自己查这件事，本包挪到工具层查。
//
// # 输出是给模型读的 JSON
//
// 每件工具的结果都排成一行紧凑 JSON 摆进模型上下文，键名和 DSH 逐字相同——
// 那些名字（revision、writeScopeWarnings）和
// [github.com/snight1983/ds-harness-go/feature/agentteam] 的落盘键名（rev、
// scopeWarnings）对不上，所以本包自带一套线上结构体，见 wire.go。
//
// # 不做什么
//
//   - **不拥有任何耐久状态。**花名册、收件箱、任务板都在
//     [github.com/snight1983/ds-harness-go/feature/agentteam] 那台服务底下的三张表里，
//     本包一份都不留，连缓存都不做——两个副本上的同一支团队看见的必须是同一份。
//   - **不判修订号对不对。**expected_revision 原样转手，对不上归
//     [github.com/snight1983/ds-harness-go/feature/agentteam.CodeTaskStaleRevision] 管。
//     本包只拦「这个数根本不是个安全整数」。
//   - **不查授权。**除了起队友那道队长闸（挪上来的理由见上一节），改派、打断、
//     写别人的任务这些该不该做全由那台服务判——它还要顺带核一次本副本上的实况。
//   - **不起回合也不停回合。**followup_task 只是把送法标成
//     [github.com/snight1983/ds-harness-go/feature/agentteam.DeliveryWakeup]，
//     真正把队友叫起来的是那台服务和它背后的驱动。
//   - **不管队友怎么跑。**上下文怎么分叉、子会话怎么开，是
//     [Config.FreshProvider] 和 [Config.ForkProvider] 那两个名字指向的提供方的事，
//     本包只按 context 参数在两个名字之间挑一个。
package agentteamtool
