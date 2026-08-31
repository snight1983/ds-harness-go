// Package compaction 是压缩这一层和后端无关的那一半：四条 compaction/* 事件的
// 词汇、那条替换用的检查点标记、下刀处的工具配对平衡，以及守住「一次压缩改的是
// 哪一段」说得清的那几条不变量。
//
// 源: packages/compaction/compaction/src/index.ts
//
// # 这一层管什么
//
// 一次压缩是一段**事务**，日志上长成这个样子：
//
//	compaction/start        ← 开锁，记下这次事务的身份和归属
//	compaction/summary      ← 摘要本身，以及它换掉了哪些节点、值多少
//	user/message (replace)  ← 真正把表面换掉的那一条，盖着本包的检查点标记
//	compaction/end          ← 放锁
//
// 前三样里只有那条 user/message 上表面。另外三条只进日志，记的是这次改动的锁、
// 输入和计价。不过模型的裁剪走 [EventCompactionPrune]，它顶替 summary 的位置。
//
// 本包提供四件事：
//
//  1. **词汇。**[EventTypes] 交出这四种事件类型，装配方拼进 [session.Vocabulary]。
//     [StartData]、[SummaryData]、[EndData]、[PruneData] 是它们的负载。
//  2. **检查点标记。**[NewCheckpointSource] 给那条替换消息盖章，
//     [IsCheckpointSource] 和 [CheckpointSourceOf] 认回来。
//  3. **下刀处。**[BalanceIndex] 增量地算出表面上每一刀有没有把一次工具调用和它的
//     结果劈开。压缩只能从配平的地方下刀。
//  4. **不变量。**[Trace] 逐条验事件，[ValidateLog] 把一整段日志走一遍。
//
// 不管的：**怎么压**。谁去调模型、按什么策略挑被遮的那一段、什么时候触发，
// 那是各个后端（compaction/basic、compaction/toolresultpruner）和 Agent 装配层的事。
// 本包只写下「不管哪个后端，写进日志的东西必须长成什么样」。
//
// # 为什么要有这一层
//
// 上下文窗口是有限的，长对话迟早装不下。压缩就是把一段旧历史换成一份摘要。
// 麻烦在于这件事**改的是已经写下去的东西**，而日志本身是只增的——于是「模型现在
// 看到的历史」和「日志里记着的历史」从此不是同一件事，两者靠这四条事件对上账。
//
// 三件必须说得清的事，正是本包那几条不变量守的：
//
//   - **括号必须配对。**一次没有 compaction/end 的 start 会把锁永远占着；
//     一次成功却没有 summary 的 end，意味着表面被换掉了、而换上去的是什么查不到。
//   - **归属必须唯一。**压缩期间开一个新回合，那次压缩换掉的范围就横跨两个回合，
//     而它的 end 只报得出一个归属。所以回合边界不许跨过一个还开着的压缩括号。
//   - **价格必须对得上。**替换事件自己不带价格，靠**紧挨在它前面**的那条计价事件
//     （summary 或 prune）给出。消费方按这个相邻关系配对，所以那几个 shadowed*
//     字段必须自洽：[SummaryData.ShadowedSeqs] 的头尾要对上
//     [SummaryData.ShadowedRange]，估价不能是负数。
//
// # 这里和 DSH 不一样的地方
//
//  1. **词汇要装配方自己拼。**DSH 靠 `declare module` 把四个类型合并进
//     `SessionEventMap`，全局登记表因此自动认得它们。Go 没有声明合并，
//     [session.Vocabulary] 也是个闭合的值，所以改成由本包交出 [EventTypes]：
//
//     vocabulary := session.CoreVocabulary().With(compaction.EventTypes()...)
//
//     不拼的话，一段带压缩的日志会被 [session.CheckVocabulary] 整个拒掉。
//
//  2. **负载自己解，不走 [session.DecodeData]。**[session.EventData] 是个封闭接口
//     （带一个不可导出的方法），本包的四种负载进不去那个联合，
//     [session.DecodeData] 只会把它们交成 [session.RawData]。所以
//     [DecodeStart] 那四个函数直接读 [session.Event.Data]，自己先查一遍类型。
//
//  3. **`CommandId` 换成普通的 string。**DSH 用 `dsh-commands/brand` 的具名类型。
//     那个包归 docs/DESIGN.md 第八节第 4 块，从第 3 块去 import 它会把移植顺序
//     倒过来。不变量对它的全部要求就是「一个非空的不透明字符串，一次事务里前后
//     一致」，string 够用；具名类型进来之后改个类型即可，介质上的字节不动。
//     代价是「有这个字段但它是空串」这个状态在 Go 这边表达不出来，
//     所以 DSH 那条单独的空串检查折进了 [Trace.requireOpen] 的相等比较里。
//
//  4. **`turn: number | null` 拆成一个值加一个布尔。**[StartData.Standalone]，
//     理由和 timecontext.Reading.HasPrevious 逐字相同：回合号从 1 起，拿 0 当
//     「没有」看着能用，但那是在给自己埋一个「哪个零值算数」的坑。介质上仍然是
//     `null`，[encodeOwner] 和 [decodeOwner] 管两侧的换算。**这个键缺了就是错**，
//     不当成独立事务——一条归属被静默补成默认值的压缩括号，正是本包这条不变量
//     存在的原因。
//
//  5. **WeakMap 换成调用方自己拿着的值。**DSH 把工具配对的增量状态和压缩这份账
//     都挂在以 `Session` 为键的 WeakMap 上，因为它要给一个活对象挂旁路缓存、
//     还得跟着对象一起被回收。[BalanceIndex] 和 [Trace] 都是普通的值，
//     零值可用，一个会话配一份、随会话一起消失，不需要弱引用。
//
//  6. **`Session` 收窄成 [SurfaceView]。**DSH 的工具配对收一整个 `Session` 活对象，
//     实际只用到表面节点、改写代数和事件三样。那个类型归第 6 块，
//     而这里现拼一个值就够——这也是本仓库对「消费方自己声明它需要的那一小片」的
//     一贯做法。多出来的 [SurfaceView.BaseSeq] 是因为 DSH 那边 seq 就是数组下标
//     （它总是从头持有全部事件），而本包的调用方可能只拿着一段后缀。
//
//  7. **[Transition] 装的是转移之后的完整状态。**DSH 的 `CompactionTransition` 是
//     一个带 kind 的四支联合，而 kind 那个字段除了给 `applyCompactionTransition`
//     分派之外没有别的用处。这里 [Trace.Apply] 只管赋值——和 session.Transition
//     是同一个做法，两个包里同一件事写成同一个样子。
//
//  8. **`Number.isSafeInteger` 那几条只剩一半。**JS 的 number 是浮点，一个 0.5 或者
//     2^53 之外的整数都进得来。Go 的 int 已经把这两件事挡掉了，
//     所以 [Trace.validateSummary] 里只剩「不能是负数」。同理 [requireID] 那条
//     `typeof value !== 'string'` 整个消失。
//
//  9. **检查点的两个字段搬进 [llm.PluginSource.Extra]。**DSH 那边是
//     `{kind:'plugin', plugin:'compact'} & {compactionId, sourceCommandId?}`
//     一个交叉类型，两半摊在同一个对象上。Go 的结构体加不上字段，所以拆成两层：
//     [CheckpointSource] 记本包自己的两个字段，[NewCheckpointSource] 把它们排进
//     Extra，介质上的字节一模一样。连带的后果是「不是检查点」和「是检查点但那两个
//     字段读不回来」变成两件必须分开的事，于是 [CheckpointSourceOf] 有三个返回值。
//
//  10. **成功的 compaction/end 必须有配对的 summary。**DSH 没有这一条。加它是因为
//     一次没有摘要的成功结束意味着表面被换掉了、而换上去的是什么、按什么价格算的，
//     日志里查不到——那正好是本包剩下那几条不变量在守的东西。
//
//  11. **`CompactionEngine` 那一族不在本包。**`CompactionAgentContext`、
//     `ManualCompactAgentContext`、`CompactionEngine` 和 `src/index.ts` 的默认导出
//     都要 Agent 才拼得起来，归第 6 块，裁决仍然是 PENDING。不变量那一侧的 `apply`
//     同样只是往注册表上挂一个安装器，核心的 [ValidateLog] 在这里写完了——
//     成例是 core/session 的 session.Trace。
//
// # 覆盖率为什么到不了 99%
//
// docs/DESIGN.md 第九节给纯逻辑包定的线是 99%，低于这个数要在源码里写明为什么。
// 本包的用例覆盖到 98.3%，没覆盖到的是五条构造不出来的语句：
//
//   - [decodeTurnStart]、[decodeUserMessage] 和 [eventDelta] 里那句「负载解出来
//     不是这个类型」。[session.DecodeData] 按事件类型分发，一条 turn/start 只会
//     得到 [session.TurnStartData]，所以这一支走不到。
//   - [NewCheckpointSource] 里那句「两个 string 排不出去」。json.Marshal 对两个
//     string 字段不会失败。
//   - [Trace.validateEnd] 里那次归属复核的失败分支。理由写在那一行上面。
//
// 前三条留着而不是断言掉，是因为一次分发错位的后果在本包特别贵：看漏一道回合边界，
// 等于放过一个跨过了回合的压缩括号；看漏一条替换消息，等于放过一个不属于当前压缩的
// 检查点；把一整条助手消息里的调用全数漏掉，等于把一刀本该不配平的地方算成配平，
// 压缩照着下刀，模型收到一条没有调用的工具结果。三种都是**静默**的——
// 日志读得回来，没有任何地方会报警。
package compaction
