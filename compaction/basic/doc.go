// Package basic 是默认那个压缩后端里**不需要跑起来**的那一半：配置怎么补默认值、
// 怎么按模型合并、怎么折算成 token 预算，表面上该压哪一段，
// 日志尾巴上还有没有一次压缩开着，以及一份摘要怎么裹成落在表面上的检查点消息。
//
// 源: packages/compaction/compaction-basic/src/index.ts
//
// # 这一层管什么
//
// compaction 那一层写下了「一次压缩在日志上必须长成什么样」。本包回答的是紧接着的
// 三个问题——**什么时候压**、**压哪一段**、**摘要长什么样**：
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
//     形状，[FrameSummary] 把摘要裹上前言和一对标签。
//
// 不管的：**真的把它跑起来**。发总结请求、往日志里追加那四条事件、在每个步骤边界上
// 重算压力、超窗之后补救——那些都要一个活的会话和一个 Agent，见下面第 10 条。
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
//  9. **`COMPACTION_INSTRUCTION` 还没搬。**那段给模型的总结指令是私有常量，唯一的
//     消费方是 `summarizeWithLlm`，而它归第 6 块（见下一条）。搬到同一个 Go 包里之后
//     它仍然是私有的，所以留到那时候一起搬，不在这里放一个没人用的字符串。
//     [FrameSummary] 用得到的那三样（一对标签和前言）搬了，英文原样——它们是给模型
//     读的，也会被下一次总结认出来。
//
//  10. **引擎、事务、总结调用三样不在本包。**`BasicCompactionEngine`（连同
//     `src/index.ts` 的默认导出）、`compactSurfaceRegion` 和 `summarizeWithLlm` 都要
//     一个能追加事件的活会话、一个 Agent 和 LLM 接缝才拼得起来，那三样归第 6 块，
//     裁决仍然是 PENDING——和 compaction 包里 `CompactionEngine` 的处理一样。
//     不变量那一侧 DSH 装的是一个**空**安装器（它自己写明「本包没有独立的事件序列或
//     可变数据关系」），照本仓库的成例整条 SKIP。
package basic
