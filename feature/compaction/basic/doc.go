// Package basic 是默认那个压缩后端：配置怎么补默认值、怎么按模型合并、怎么折算成
// token 预算，表面上该压哪一段，日志尾巴上还有没有一次压缩开着，一份摘要怎么裹成
// 落在表面上的检查点消息，那次复用缓存的总结调用怎么发，真的会改日志的那一段
// 压缩事务怎么开括号、怎么合括号，以及把这些拼起来的那个引擎什么时候动手。
//
// 源: packages/compaction/compaction-basic/src/index.ts
//
// # 这一层管什么
//
// compaction 那一层写下了「一次压缩在日志上必须长成什么样」。本包回答的是紧接着的
// 五个问题——**什么时候压**、**压哪一段**、**摘要长什么样**、**这一刀怎么落下去**、
// **谁来叫**：
//
//  1. **什么时候压。**[Config.Resolve] 把一份写下来的配置补全、验一遍；
//     [ResolvedConfig.ForTarget] 按当前路由合并按模型的覆盖；
//     [TargetPolicy.Spec] 再按这个模型的窗口大小折算出压力线和保留预算。
//     三步分开，是因为前两步和模型窗口无关，而窗口来自适配器那一侧。
//  2. **压哪一段。**[SelectCompactableRange] 从表面头上起，一直挑到「再往后就动到
//     要保留的尾巴了」为止，并且那一刀不许把一次工具调用和它的结果劈开。
//     [InspectEntryState] 和 [CheckNoActiveCompaction] 读日志尾巴，拦掉一次
//     「上一个压缩括号还开着」的开工。
//  3. **摘要长什么样。**[SummarizationInput] 和 [SummaryResult] 是一次总结调用两头的
//     形状，[FrameSummary] 把摘要裹上前言和一对标签，[SummarizeWithLLM] 真的把那次
//     调用发出去——重放对话前缀、末尾接上那条总结指令，所以它恰好是上一次路由请求的
//     一个前缀，提供方那边的 KV 缓存是复用而不是作废。
//  4. **这一刀怎么落下去。**[CompactSurfaceRegion] 是那段会改日志的事务：
//     start / summary / 替换消息 / end 一组括号，中途失败也只做一次合括号的尝试、
//     并且**不回滚**——一次做砸了的压缩本身就是要在日志里看得见的。
//  5. **谁来叫。**[Engine] 把上面四样拼成一个 [compaction.Engine]：
//     [Engine.CompactIfNeeded] 分出压力和超窗两条自动路，[Engine.CompactNow]
//     是人明着要的那一次、要先占住一段空闲期。[Install] 再把这些挂到一个跑着的
//     运行时上——步骤边界上按压力压一次，提供方确认超窗之后补救一次并要求重试。
//
// # 为什么要有这一层
//
// 压缩的策略参数彼此是有关系的，而配错了的后果是**安静**的：保留的尾巴比压力线还长，
// 那么压完一次仍然在线上，下一步又会去压，于是每一步都做一次总结调用、却永远降不到
// 线下；保留比例和压力线各自都合法、合起来不合法，这种冲突和模型窗口无关，却只在
// 某个具体模型上才会暴露出来。所以本包把「验」和「用」拆开：验不过就没有一份可用的
// 配置（[Config.Resolve] 是构造 [ResolvedConfig] 的唯一入口），而不是留一个照样每步
// 都去压一次的运行期。
//
// 挑区间那一步同理。表面上任意一刀都切得下去，但只有配平的那些刀切完之后模型收到的
// 历史仍然是自洽的——一条没有对应调用的工具结果会让下一次请求当场被提供方拒掉。
//
// # 这里和 DSH 不一样的地方
//
//  1. **`Pick<LlmCallConfig, 'provider'|'model'>` 换成 [Target]。**Go 没有类型运算，
//     而 [llm.CallConfig] 整个搬进来只用两个字段是白搭。[Target] 同时当 map 的键用，
//     替掉 DSH 那个 `${provider} ${model}` 拼串——拼串的写法在 provider 里带空格时
//     会撞车，而 Go 的结构体键不会。
//
//  2. **零有意义的那几个字段用指针。**[PolicyConfig.RetainTokens]、
//     [PolicyConfig.CompactionRetries]、[PolicyConfig.MaxOverflowRetries] 和
//     [Config.Auto] 是 *int / *bool，其余用零值当「没给」。理由是这四个的**零是明确的
//     意思**——一点尾巴都不留、压一次就不再试、整个关掉超窗补救、关掉自动压缩——
//     而它们的默认值恰好都不是零。拿零当「没给」会把这四种意思静默改写成默认值。
//     比例和 MaxTokens 不需要指针：合法取值分别是 (0,1] 和 ≥1，零本来就不在里面。
//
//  3. **两个可选字符串合成一个 [PolicyConfig.Summarization] 指针。**DSH 是
//     summarizationProvider / summarizationModel 两个可选字段，外加一条「要么都不给、
//     要么一起给空串、要么一起给非空」的成对校验，其中**一起给空串**是有用的：
//     一条按模型的覆盖靠它清掉全局配的摘要路由，回落到对话自己的模型。Go 里空串和
//     「没给」分不开，所以折成一个指针：nil 是继承，指向零值 [Target] 是显式清掉，
//     指向非零值是替换。成对这件事顺带由类型担着，校验只剩「非 nil 时两个字段要么都
//     空要么都不空」。
//
//  4. **`ResolvedRetention` 那个排他联合换成 [Retention] 加 [Retention.ByRatio]。**
//     DSH 是「两个字段互相 never」的联合。这里不需要额外的标志位：Ratio 的合法取值是
//     (0,1]，所以 Ratio 为零就**只能**意味着这份保留是按绝对 token 数算的。
//
//  5. **`ResolvedPolicyFields` 跟着导出成 [Policy]。**DSH 那个是模块私有的 interface，
//     只用来给 [ResolvedConfig] 和 [TargetPolicy] 做交叉。Go 里匿名嵌入要求它是个
//     具名类型，而嵌入一个不可导出的类型会让外部读不到那几个提升上来的字段。
//
//  6. **`validateKeys` 和那一堆 typeof 检查整个消失。**DSH 那边配置解出来是 unknown，
//     每个字段都要先查类型再查取值，还要单独拒掉拼错的键。Go 这一侧类型由
//     [PolicyConfig] 钉死了，拼错的字段编译期就过不去，只剩取值范围这一半。
//     但 [validateRatio] 里那条 `Number.isFinite` 仍然留着——float64 一样有 NaN 和
//     ±Inf，而 NaN 和任何数比较都是假，光靠上下界拦不住它。
//     `Number.isInteger` 那一半可以走了，Go 的 int 已经把它挡掉了。
//
//  7. **`TokenMeasurement` 收窄成 []PricedNode。**DSH 收一整份计量结果，
//     而 [SelectCompactableRange] 只用到里面每个节点的 seq 和估价两个字段。
//     计量器归 docs/DESIGN.md 第八节第 6 块，从这里去 import 它会把移植顺序倒过来。
//     `Session` 同样收窄成 [compaction.SurfaceView] 加 [compaction.BalanceIndex]——
//     都是本仓库「消费方自己声明它需要的那一小片」的一贯做法。
//
//  8. **[EntryState] 只记 seq 和一个布尔。**DSH 的 `unmatchedCompactionStart` 装的是
//     整条事件，而它只被读了 `.seq`。这里留 seq 加一个「有没有」的布尔，理由和
//     [compaction.StartData.Standalone] 那一处相同：拿 0 当「没有」在一段从头开始的
//     日志里恰好会撞车，seq 0 是合法的。
//
//  9. **那些跑起来才要的东西一律收窄成单方法接缝。**`ctx.llm` 整个服务收窄成
//     [Streamer]（只有 Stream，签名和 [llm.Runtime.Stream] 逐字相同，一个真的运行时
//     结构上就满足它）；计量器收窄成 [Meter]；那次总结调用收窄成 [Summarize]。
//     写窄的后果不只是少写几行：本包在**类型上**就发不出别的模型请求，
//     一次总结**只可能**是那一次调用。`AbortSignal` 一律换成头一个参数的
//     [context.Context]，理由和 compaction 包那一处相同。
//
//  10. **引擎和「挂上去」拆成了两件事。**DSH 的 `BasicCompactionEngine` 在构造函数里
//     顺手把自己注册进 cordis、并且在 `auto` 开着时当场挂上那几个观察者。这里
//     [Engine] 只是一个满足 [compaction.Engine] 的值、自己不带任何可变状态，
//     挂观察者由 [Install] 单独做。拆开的理由是那几条观察者要一张活的 agent 注册表
//     和一段 [scope.Scope] 生命期，而这两样和「压一次」本身无关——一份只想手动调
//     [Engine.CompactNow] 的装配不该被迫先备一个注册表。DSH 挂在 WeakMap 上的那两份
//     重试计数和「已经警告过的路由」因此都归 [Install]。
//     不变量那一侧 DSH 装的是一个**空**安装器（它自己写明「本包没有独立的事件序列或
//     可变数据关系」），照本仓库的成例整条 SKIP。
//
// # 覆盖率为什么到不了 99%
//
// docs/DESIGN.md 第九节给纯逻辑包定的线是 99%，低于这个数要在源码里写明为什么。
// 本包的用例覆盖到 98.4%，没覆盖到的是十一条构造不出来的语句。
//
// 先是 [Engine] 那三条：
//
//   - **[Engine.CompactIfNeeded] 压力重试循环里那句 break。**上一轮换上去的那条检查点
//     消息本身就是一个可压的节点，所以压过一次之后一定还挑得出区间。留着而不是断言掉,
//     是因为**摘要那个钩子是可换的**，一个换掉的钩子理论上能产出一条挑不中的替换消息。
//     DSH 那边也标了同一条不可达。
//   - **摘要那一步读路由失败的那两条**（[Engine] 私有的 summarize 和 conversationPolicy
//     各一处）。它们只在 [CompactSurfaceRegion] 里被调到，而那条链路更早的
//     [buildSummarizationInput] 已经先读过同一段会话的请求头了——头读不回来的话，
//     先失败的是那里。留着这两条是因为「读不出路由」和「没有路由」的处理完全不同：
//     前者要停，后者要回落到默认策略，把错吞掉会拿一份不是给这条路由的策略去发请求。
//
// 剩下八条全在 [CompactSurfaceRegion] 那条链路上：
//
//   - **合括号那两处的失败分支**（transaction.go 里正常收尾和补救收尾各一处）。
//     一条 compaction/end 没有 SurfaceOp、负载又是本包自己排出来的合法 JSON，
//     所以它写不进去只剩「追加期间重入」一个成因——而那时候更前面的 compaction/start
//     已经先失败了，走不到这里。开括号那一处的重入用例是有的
//     （见 transaction_test.go 里那个在事件通告里开工的用例）。
//   - **[compaction.BalanceIndex.BalancedAfter] 那条错误。**它紧跟在
//     BalancedBefore 后面，那一次调用已经把索引同步到当前这一代表面了，
//     两次之间没有任何东西会动表面。
//   - **[compaction.NewCheckpointSource] 排不出去。**两个 string 字段，
//     json.Marshal 不会失败——和 compaction 包里同一条的理由逐字相同。
//   - **那条检查点消息排不出去。**同一份 [llm.Content] 在上一条语句就已经跟着
//     [compaction.SummaryData] 排过一次了，真排不出去的话在那里就先失败了。
//   - **那条替换消息写不进日志。**要让它失败得有第二个写入方在同一段会话上并发追加，
//     而一次压缩事务从开括号那一刻起就把这段会话独占了——这正是那对括号存在的理由。
//   - **[buildSummarizationInput] 里那条基准偏移的护栏。**一个真的
//     [coresession.Session] 恒有「log 索引等于 seq」，对不上意味着表面和日志已经不是
//     同一段历史了。留着它而不是断言掉，是因为真出这种事的时候，下面那一步会拿错一条
//     事件去当被遮的历史送给模型——**静默**，日志读得回来，没有任何地方会报警。
//   - **[decodeTurnStart] 里那句「负载解出来不是这个类型」。**理由写在那一行上面。
package basic
