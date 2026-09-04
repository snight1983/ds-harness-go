// Package userapproval 是审批这条能力的**提供方**：它把一次「要不要放这个工具过去」
// 的问题，先过会话自己的策略闸，再交给接上来的那些答复者，最后把问和答这一对
// 原样写进会话日志。
//
// 对应 DSH 的 @deepseek-ai/dsh-user-approval（packages/interaction/user-approval）。
//
// 源: packages/interaction/user-approval/src/index.ts:1-5
//
// # 谁在消费它
//
// [Service.Request] 的签名就是 [github.com/snight1983/ds-harness-go/tools.Approval]，所以一个装好的
// 服务可以直接交给工具运行时当审批接缝，中间不需要任何胶水。答复的词汇表
// （[github.com/snight1983/ds-harness-go/tools.ApprovalOutcome]）也只有那一份：DSH 把它放在本包一个
// 不依赖 cordis 的 types.ts 里，好让浏览器侧的类型链吃得下；Go 里没有那个模块图问题，
// 而消费方 tools 是更底下的一层，所以词汇表留在那里，本包引它。
//
// # 三件事按这个顺序发生
//
//  1. **回合围栏**。没有打开的回合就当场拒，一个字节都不写。审计那一对必须被回合
//     圈住，因为回合才是持久日志的提交/回放边界——两个回合之间裸写的一条事件，
//     重新装载时和一段崩溃残尾长得一模一样，会被静静丢掉。
//  2. **策略闸**。会话策略是 `never` 时**在派发之前**就判成拒绝。这一步故意不做成
//     一条答复者：任何一条后登记的答复者都可能排在闸前面，那样「never 必然拒绝」
//     这句承诺就取决于登记顺序了。
//  3. **答复者链**。没人应答、应答的报错、应答的还回来一个词汇表外的值——三种都
//     收敛成 [github.com/snight1983/ds-harness-go/tools.ApprovalUnavailable]。失败一律向「不放行」
//     那一侧倒。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 靠 cordis 的 ctx.approval 和 `agent.session` 直接拿到活会话。Go 里活会话
// 是循环那一块的东西（见 docs/DESIGN.md 第八节），所以本包收一条 [Config.LogOf]：
// 从一个作用域键找到它那条日志。同理，DSH 的 `agent.inject(...)` 变成 [Config.Notify]。
//
// 新增: DSH 把切换后的那句策略陈述通过 `ctx.systemPrompt.context` 挂进系统提示。
// 系统提示服务在第 6 块，所以本包只交出那两句话本身（[PolicyStatement]），由装配方
// 在系统提示装起来之后自己挂。这样做的另一个好处是这两句话在本包是纯函数，测得动。
//
// 新增: DSH 用 AbortSignal 和 answer promise 赛跑，好让一次中途取消立刻结算成
// cancelled、迟到的答复被丢掉。Go 里取消是 ctx，答复者链是同步调用，所以那场赛跑
// 写成「答复者跑在一个 goroutine 里，结果送进一个容量 1 的 channel」——容量 1 保证
// 取消赢了之后那个 goroutine 仍然送得出去、收得掉，迟到的答复由构造本身丢弃。
package userapproval
