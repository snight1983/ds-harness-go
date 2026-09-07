// Package turnoutline 是整会话的回合大纲：每一个已开始的回合、它的
// `turn/start` seq、以及两段收住长度的预览。一个按窗口翻历史的客户端靠它
// 把没加载的回合也列出来，并且知道该往哪个 seq 上翻才能把某个回合的事件带进来。
//
// 对应 DSH 的 @deepseek-ai/dsh-session-turn-outline（packages/session/session-turn-outline）。
//
// 源: packages/session/session-turn-outline/src/index.ts:1-12
//
// # 为什么锚在 turn/start 上，不锚在那条提示词上
//
// 每一条大纲记的是 [github.com/snight1983/ds-harness-go/sessionlog.EventTurnStart]
// 的 seq，而不是那条用户消息的 seq。理由是这个 seq 的用途是**跳转落点**：
// 循环先写回合边界、再写这个回合的提示词和各个步骤，所以一个往回翻到这个 seq
// 的窗口装得下整个回合；翻到提示词那一条则会把边界本身漏在窗口外面。
//
// # 一个回合最多推三次
//
// 大纲是给会话侧栏那种列表用的，它的变更流不该在一个回合里响几十次。折叠因此
// 分成两半：[Entry] 里那份**已经定稿**的事实，和一份只在内存里的草稿。
//
//   - 回合边界落下 → 追加一条空记录（推一次）；
//   - 这个回合第一条用户消息 → 填上提示词预览（推一次）；
//   - 每一条带文字的助手消息 → 只更新草稿，**不推**；
//   - 回合结束 → 把草稿定稿成回复预览（推一次）。
//
// 中间那些助手消息照样把状态改了（检查点落的是最新的草稿），只是不惊动听众。
// 这件事在 Go 这边是怎么表达的，见 [RegisterProjection] 的注释。
//
// # 预览按字算，不按字节算
//
// 两个预算——提示词一行、回复三行——量的是列表卡片上放得下几行字。所以
// [PromptPreviewMaxChars] 和 [ResponsePreviewMaxChars] 数的是字符（rune），
// 不是字节：一行中文按字节收会在第十六个字上就砍断，那个数和「一行放得下」
// 没有关系。同一个取舍在 [github.com/snight1983/ds-harness-go/llm.BoundContextSummary]
// 上也做过一次。
//
// # 不做什么
//
//   - **不存事件，也不替客户端翻历史。**大纲只交出 seq，拿着它去取那一段
//     窗口的是会话查询那一侧（feature/sessionquery）。
//   - **不负责送达。**登记、快照、变更流、检查点全是
//     [github.com/snight1983/ds-harness-go/sessionlog/projection.Registry] 的活；
//     本包只拥有那段折叠。
//   - **不给回合起名字。**预览是原文的前几十个字，不是摘要；会话标题那件事
//     在 feature/sessiontitle，它要过一次模型。
//   - **不判断一个回合成功还是失败。**结束理由在
//     [github.com/snight1983/ds-harness-go/sessionlog.TurnEndData] 里，本包不读它——
//     一个被打断的回合照样有大纲，它的回复预览就是它中断前说出来的那些话。
//   - **不保证每个回合都有预览。**一个没说过话就结束的回合，两段预览都是空串；
//     本包不去别的地方找字来填。
package turnoutline
