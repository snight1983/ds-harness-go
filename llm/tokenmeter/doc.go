// Package tokenmeter 回答一个问题：这个会话现在占了多少 token。
//
// 对应 DSH 的 @deepseek-ai/dsh-token-meter（packages/llm/token-meter）。
//
// 源: packages/llm/token-meter/src/index.ts
//
// # 这个包的根本立场：它不测量，它记账
//
// 这里没有分词器。[EstimateContent] 那一套是**固定密度**的估算——字符数除以一个常数，
// 每一块再加一份结构开销——它必然不准：CJK 会被低估，JSON schema 会被高估。
//
// 整个包的设计正是建立在「它不准」这个前提上的。真正的数字来自提供方在每条落定的助手
// 消息上报回来的用量；这套启发式只负责度量**两次用量采样之间**的那段增量。
// 所以 [Measurement].TotalTokens 不是一次测量，而是一个锚
// （[MeasurementBaseline]）加上那之后表面的**带符号**净位移。基准越新这个数越可信；
// 一份 [BaselineEstimated] 的基准意味着整份都是估的。
//
// 位移带符号是要紧的：一次压缩把一大段表面换成一小段摘要，那个位移是负的。
// 最终值钳在 0——负数会一路串进预算和压缩触发的算式里，那比丢掉一次记账严重得多。
//
// # 这个包管什么
//
// [New] 造出 [TokenMeter]，它按会话缓一份重放状态，把日志往前折，
// [TokenMeter.Measure] 交出 [Measurement]——包括表面上每个节点的估价 [SurfaceNode]。
// [RegisterProjections] 装上三个投影单元：
//
//   - [TokenUsageProjectionKey]：整份日志累计下来的、提供方报回来的账单（[TokenUsageView]）。
//   - [ContextPressureProjectionKey]：下一次请求大概会占多少（[ContextPressureView]）。
//   - [ContextBreakdownProjectionKey]：这些占用由什么组成（[ContextBreakdownView]）。
//
// 不管的：**拿这个数去做什么决定**。什么时候该压缩、预算怎么切、超了怎么办，
// 是 compaction 那一块和装配层的事。本包只交出数字。
//
// # 同一件事有两份实现，是故意的
//
// 表面折叠在这个包里写了两遍：
//
//   - surfacefold.go 那份是 **O(表面)**：每个节点的估价都留着。
//     [TokenMeter] 走这条，因为它交出去的结果里要带上整张节点表——
//     压缩那边挑下刀点就是照着它挑的，那张表必须和当前表面一一对上。
//   - surfaceprojection.go 那份是 **O(1)**：状态里只有一个可选的认领单。
//     三个投影单元走这条，因为投影状态要序列化进检查点落盘，
//     而一份随会话长度线性增长的状态会把检查点撑爆。
//
// 代价落在 O(1) 那一份上：一次替换要知道「被换掉那一段值多少」，而它手上没有那张表。
// 补偿它的是**影子价协议**——压缩在写下替换之前，先写一条 compaction/summary 或
// compaction/prune 事件，把自己即将盖掉的那段区间和估价一起记进日志（[ShadowPriceClaim]），
// 折叠只需要记住紧挨着的上一张认领单。
//
// 两份折叠在每个事件边界上必须给出同一个总价，这条由
// TestBothFoldsAgreeAtEveryEventBoundary 钉住。
//
// # 几处容易算错的地方
//
//  1. **推理 token 不另加一笔。**[github.com/snight1983/ds-harness-go/llm.TokenUsage].ReasoningTokens 说的是
//     「输出里有多少花在推理上」，它已经含在 OutputTokens 里。四个桶互不重叠。
//
//  2. **同一个步骤重复报用量是替换，不是叠加。**流中途那条 usage 分块和它后面那条落定
//     消息报的常常逐字相同，加两遍就把账翻倍了。
//
//  3. **占用只算提示词侧，不含输出。**所以一个回合正流着的时候压力不动——那个投影值
//     回答的是**下一次**请求。一次压缩能让它当场掉下来，而压力自己看不见压缩这件事，
//     因为压缩不产生用量。
//
//  4. **读不回来的请求头保持原值，不归零。**归零会让界面显示成「这次请求没有系统提示、
//     没带工具」，那比偏一点严重得多。
//
// # 这里和 DSH 不一样的地方
//
//   - [Config] 是空的。这套启发式的三个常数写死，没有一处能配。DSH 那边写成
//     `Record<string, never>`，意思一样：**存在一份配置**这件事本身是接口的一部分。
//   - [MeasurementBaseline] 没按本仓库处理 TS 可辨识联合的一贯做法（封闭接口加变体，
//     成例是 [github.com/snight1983/ds-harness-go/llm.ContentBlock]）写，而是收成一个带判别字段的结构体。
//     理由见那个类型上的注释：它不过任何序列化边界，三支都带着同一个 Tokens，
//     而每一处读它的地方都只想读那一个数。
//   - 那几份折叠状态在 Go 里不导出。它们是 [TokenMeter] 和投影单元内部的账本形状，
//     对外只出现在 [Measurement].Nodes 里。
package tokenmeter
