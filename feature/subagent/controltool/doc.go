// Package controltool 是那三件**全局命名**的控制工具：send_message、interrupt_agent
// 和 list_agents。它们都只是那台子 agent 运行时面向模型的一层薄适配。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-subagent-control
// （packages/subagent/tool-subagent-control）。
//
// 源: packages/subagent/tool-subagent-control/src/index.ts:1-10
//
// 这一层自己不做任何生命周期路由：驻留、冷恢复、打断准入全归它调的那台服务。
// 它们也刻意和那些**按提供方绑定**的委派工具分开住，这样多件委派工具共用同一套
// 控制 API。
//
// # 两个装配面，分开装
//
// DSH 把 list_agents 单独摆成一个插件（src/list-agents.ts），理由写在那份模块
// 注释里：一套部署可以只登记「续接投递」而不外露「发现」。Go 这边照搬这条界线——
// 同一个包里两个互不相干的控制器：[Controller] 装 send_message 和 interrupt_agent，
// [ListController] 装 list_agents，各有各的 Config 和 Install。
//
// # 新增: 服务走参数，钥匙要查回去
//
// DSH 从 cordis 上下文上直接取 `ctx.tools`、`ctx.subagents`、`ctx.agents`，执行体
// 里的 `exec.agent` 也直接就是那个 Agent 对象。Go 这两样都没有：服务经 Config
// 显式交进来，而 [github.com/snight1983/ds-harness-go/tools.ExecutionInput.Agent] 是一把不透明的
// 作用域钥匙，所以由装配方经 AgentOf 交一条查回去的路（做法和
// github.com/snight1983/ds-harness-go/feature/sessionquery/querytool、
// github.com/snight1983/ds-harness-go/feature/subagent/reporttool 逐字相同）。
//
// # 面向模型的文字保持英文
//
// 工具名、说明、参数描述和渲染出来的那几行都是英文，和本仓库其余面向模型的载荷
// 同一条界线。中文只在注释里。
package controltool
