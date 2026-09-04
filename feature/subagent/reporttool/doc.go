// Package reporttool 是那件**只给孩子看**的 report 工具和它那段用法指引：
// 装进每一个可续进程内孩子还没公布的创建窗口里。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-subagent-report
// （packages/subagent/tool-subagent-report）。
//
// 源: packages/subagent/tool-subagent-report/src/index.ts:1-7
//
// 根 agent、一次性孩子、跨进程提供方，以及不带 agent 的那些执行，谁都看不到这笔登记。
//
// # 两笔登记，一条命
//
// 那件工具和那段指引都登记在**孩子自己那个作用域**上，所以对它的父和兄弟都不可见。
// 这个包交回的释放函数把两笔一起摘掉：装的时候后一笔失败要把前一笔滚回去，
// 摘的时候两笔都要试过再报失败——一笔摘不掉不该让另一笔留在那儿。
//
// # 新增: 服务走参数，钥匙要查回去
//
// DSH 从 cordis 上下文上直接取 `childCtx.tools`、`childCtx.systemPrompt` 和
// `ctx.subagents`，执行体里的 `exec.agent` 也直接就是那个 Agent 对象。Go 这两样
// 都没有：服务经 [Config] 显式交进来，而
// [github.com/snight1983/ds-harness-go/tools.ExecutionInput.Agent] 是一把不透明的作用域钥匙，
// 所以由装配方经 [Config.AgentOf] 交一条查回去的路（做法和
// github.com/snight1983/ds-harness-go/feature/sessionquery/querytool、
// github.com/snight1983/ds-harness-go/feature/plan/planmode 逐字相同）。
//
// # 面向模型的文字保持英文
//
// 工具名、说明、参数描述和那段提示词指引都是英文，和本仓库其余面向模型的载荷
// 同一条界线。中文只在注释里。
package reporttool
