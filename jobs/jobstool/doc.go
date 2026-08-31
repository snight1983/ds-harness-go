// Package jobstool 是后台作业那条缝**面向模型**的那一层：job_output、job_list、
// job_kill 三件工具，加上「一件作业结算了要怎么让它的属主知道」这条投递规矩。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-jobs（packages/jobs/tool-jobs）。
//
// 源: packages/jobs/tool-jobs/src/index.ts:1-8
//
// 装上它同时也把生产方开工必需的那个**控制器**挂了上去（见
// [ds-harness-go/jobs/jobs.Registry.AttachController]）：没有任何控制器服务的属主
// 一律开不了工，所以「谁读得了作业」和「谁起得了作业」在装配上是同一件事。
//
// # 一次结算怎么找到它的属主
//
// 忙着的属主用**注入**：那条通知在它下一步的收件箱里等着，而当下这个回合关不掉
// 那份清单，于是同时落定的一堆作业只花掉一步。闲着的属主在默认的 wakeup 之下被
// **唤醒**——一条没人认领的通知等于一次模型永远不会知道的完成。两条路都一样：
// 属主在认领之前就被释放的话，那条通知跟着它一起没了；而拆除带来的那些结算本身
// 就带着「已汇报」，不会再被投递一次。
//
// 投给谁不归本包管：注册表按结算属主的作用域链决定哪些监听器够得着，本包只决定
// 够得着之后**怎么**投。
//
// # 唤醒预算
//
// 一个被唤醒的回合可能又起一件作业，那件作业结算时又把它唤醒——这条自激链由
// [Config.MaxConsecutiveWakes] 收住：超出之后通知降级成注入，而任何一条**用户
// 自己写的**输入被认领时预算清零。
//
// # 新增: 三处 WeakMap 在 Go 里各自换了办法
//
// DSH 拿三张 WeakMap 给对象挂旁路状态，键分别是 Agent 和 ToolExecution。Go 没有
// 弱引用表，所以：
//
//   - **唤醒预算**是一张 `map[agent.Agent]int`，第一次记账时在那个属主自己的作用域
//     上挂一项清理，做法和 [ds-harness-go/jobs/localjobs] 那张属主表逐字相同。
//   - **每次调用的输出上限**是一张按 [ds-harness-go/core/tools.ExecutionToken] 索引
//     的表。token 可比较、可当键，而 [ds-harness-go/core/tools.Definition.FinalizeContent]
//     对每一份规范化过的结果恰好被调一次，所以那一次就是它的摘除点。
//   - 两张表都被同一把 [sync.Mutex] 罩着：结算来自生产方那条协程，工具调用来自
//     模型那条。
package jobstool
