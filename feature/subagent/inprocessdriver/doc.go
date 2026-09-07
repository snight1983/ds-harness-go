// Package inprocessdriver 是进程内**一次性**子 agent 提供方共用的那台驱动。
//
// 对应 DSH 的 @deepseek-ai/dsh-subagent-in-process-driver
// （packages/subagent/subagent-in-process-driver）。
//
// 源: packages/subagent/subagent-in-process-driver/src/index.ts:1-12
//
// agent 工厂那次创建事务拥有还没公布的装配与回滚；公布之后交回来的那个句柄就是
// 提供方的调用方手里唯一那份静止的生命周期所有权。
//
// 可续的孩子**从不**走这里：续接管理器自己组装、自己驱动它们，所以这台驱动恰好
// 拥有一个回合、一份结果。
//
// # 这个包不是提供方
//
// spawn 与 fork 两个提供方（github.com/snight1983/ds-harness-go/feature/subagent/spawninprocess 与
// github.com/snight1983/ds-harness-go/feature/subagent/forkinprocess）各自实现
// github.com/snight1983/ds-harness-go/feature/subagent.Provider，把种子这一样差别填进来，剩下那条
// 「建孩子 → 投提示词 → 等静止 → 读结果 → 处置」的路全在这里。
//
// # 新增: 服务走参数，不走容器
//
// DSH 从父那个 cordis 上下文上直接取 `parent.ctx.agents`，孩子那些登记则挂在
// setup 回调拿到的 `childCtx` 上。Go 没有那个容器，「在不在场」就是装配方手上有
// 没有这个值，所以做成一个显式的 [Services]（成例见
// github.com/snight1983/ds-harness-go/feature/subagent.ChildCompositionServices）。
//
// # 不做什么
//
//   - **不实现 agent 循环，也不自己判断孩子静没静。**造孩子交给
//     [github.com/snight1983/ds-harness-go/harness/agent] 的注册表和造法，本包只在它两端记账。
//   - **不验结构化输出的参数。**那份真 schema 注册在孩子自己的作用域上，验参数是
//     [github.com/snight1983/ds-harness-go/tools] 那台注册表的活；本包只认「那次权威的工具结果
//     成功了」这一个事实。
//   - **不重试，也不续跑。**一台驱动恰好对着一个回合、一份结果；再来一次是调用方
//     的事，可续那条路整个不经过这里。
//   - **不挑孩子被限成什么样。**深度上限、工具过滤、人设都从请求里来，本包只负责把
//     它们落进那个还没公布的创建窗口。
//   - **不跨进程。**这里的一切都在当前进程内；进程外运行归宿主自己的提供方。
package inprocessdriver
