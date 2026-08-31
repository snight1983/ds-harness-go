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
// spawn 与 fork 两个提供方（ds-harness-go/subagent/spawninprocess 与
// ds-harness-go/subagent/forkinprocess）各自实现
// ds-harness-go/subagent/subagent.Provider，把种子这一样差别填进来，剩下那条
// 「建孩子 → 投提示词 → 等静止 → 读结果 → 处置」的路全在这里。
//
// # 新增: 服务走参数，不走容器
//
// DSH 从父那个 cordis 上下文上直接取 `parent.ctx.agents`，孩子那些登记则挂在
// setup 回调拿到的 `childCtx` 上。Go 没有那个容器，「在不在场」就是装配方手上有
// 没有这个值，所以做成一个显式的 [Services]（成例见
// ds-harness-go/subagent/subagent.ChildCompositionServices）。
package inprocessdriver
