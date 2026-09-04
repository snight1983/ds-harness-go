// Package skill 提供「技能」这条能力缝的注册表一侧：谁能贡献技能、同名的怎么排、
// 一个 agent 看得见其中哪些、以及一份技能正文怎么渲染给模型。
//
// 对应 DSH 的 @deepseek-ai/dsh-skill（packages/skill/skill）。
//
// 源: packages/skill/skill/src/index.ts:1-11
//
// # 技能是什么
//
// 一份技能就是一段针对某类任务写好的、可复用的指令正文，外加一句「什么时候该用它」
// 的路由说明。它不是工具：工具是模型能调的函数，技能是模型读完之后照着做的话。
//
// 技能从哪来由**提供方**决定——本地目录、打包进来的一批、远端注册中心都行。本包
// 只做三件事：把各个提供方报出来的清单合起来、给同名的技能定一个赢家、把赢家的
// 摘要和正文交给消费方。
//
// # 层与优先级
//
// 注册落在**调用方作用域**那一层（[github.com/snight1983/ds-harness-go/scope.Layers] 的规矩）：
// 宿主和仓库级插件落全局层，挂在某个 agent 预设常驻组合里的插件落那个预设自己那层。
// 读的时候把全局层和视角作用域这条链合起来，**近的那层整个盖住远的**——和工具
// 注册表同一条遮蔽规则。rank 只在**一层之内**决定同名谁赢。
//
// 一层之内的排序是 rank → 提供方注册顺序 → 提供方自己报出来的顺序，全都稳定。
// 运行期注册（[Registry.Register]）的 rank 固定是 250，打包提供方约定用
// [BundledRank]（600），于是「项目里的 > 运行期的 > 用户全局的」这条次序成立。
//
// # 缓存与 revision
//
// 一次 collect 会问遍每个提供方，那是真 I/O。所以结果按「工作目录 + 作用域链 + revision」
// 缓存起来，任何一次注册变动都把 revision 加一并清空缓存。**不完整**的发现结果
// （某个提供方报错了、或者它自己说 Incomplete）永远不进缓存：消费方拿到
// [CatalogSnapshot].Complete 为假时应当留着上一份好的，下一个请求边界再试。
//
// # 这里没有照抄的部分
//
// 新增: cordis 的 Service / ctx.skills / 插件名 / inject 声明全部不移。本包就是一个
// 普通类型，装配方自己造一个 [Registry] 拿着。事件 `skills/change` 换成
// [Options].OnChange 这个回调——和 tools 那边一样。
//
// 新增: AbortSignal → [context.Context]。DSH 的 SkillLookupOptions 上带一个 signal，
// Go 里 ctx 是第一个参数，所以 [LookupOptions] 和 [ViewOptions] 里只剩工作目录和作用域两项。
//
// 新增: DSH 的 provider.list() 可以返回「一个数组」或者「一份 {candidates, complete}
// 观察」，normalizeProviderObservation 在运行期分辨这两种形状。Go 有类型，只留
// [Observation] 一种形状，那个归一函数整个不需要。代价是它的默认值要自己对齐：
// DSH 的数组简写等价于 complete: true，所以 Go 这边字段反过来叫 [Observation].Incomplete，
// 零值就是「完整」。
//
// 新增: DSH 那一大摞 validateCandidate / validateDefinition 里，绝大多数是
// `typeof x !== 'string'` 这类类型检查——Go 的编译期就管了。留下来的只有类型系统
// 说不了的那几条：名字的文法、描述不能是空串、提供方报的 provider 字段要和它自己
// 的名字对得上。
//
// 新增: waitWithAbort。DSH 把提供方交回的 promise 和 abort 赛跑，理由写在它自己的
// 注释里：「一个不合作的提供方不该把调用方吊死」。那是**行为**不是手段，所以留着，
// 换成 Go 的写法——提供方的调用跑在一个 goroutine 里，取消时立刻交回，那个 goroutine
// 自己会随提供方返回而结束。
//
// 新增: DSH 用 `WeakMap<ScopeKey, number>` 给作用域发缓存键用的稳定编号，靠 GC 回收。
// Go 没有弱表，所以那张表就是一张普通 map；它会把作用域键留住，但缓存本身是有上限的、
// 而且每次注册变动都整个清空，所以留住的量跟着一起有界。
package skill
