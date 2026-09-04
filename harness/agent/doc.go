// Package agent 是一个 agent 活着的那一层：公开的 [Agent] 面、它那份收件箱投影
// [Inbox]、装着活 agent 的 [Registry]、以及循环挂在上面的那些扩展点。
//
// 对应 DSH 的 @deepseek-ai/dsh-agent（packages/core/agent）。
//
// 源: packages/core/agent/src/index.ts:1-6
//
// # 这个包不造 agent
//
// 源: packages/core/agent/src/index.ts:177-214
//
// 造一个 agent、驱动它跑回合，是循环那一层的事（DSH 的 @deepseek-ai/dsh-agent-loop，
// 本仓库的 github.com/snight1983/ds-harness-go/harness/agentloop）。本包只定义：
//
//   - [Agent]：一个活 agent 对外的样子。
//   - [Factory]：循环实现的那个造法，由 [Registry.SetFactory] 登记进来。
//   - [Registry]：活 agent 的那张表，加上「登记 → 公布 → 摘除」三步。
//   - [ModelSelectionRef] 与 [InstallModelSelection]：把「这个 agent 下一步用哪个
//     模型」这一份可变的选择，同时接到提示词装配和请求路由上。
//
// 这条切分是 DSH 自己的，理由也照抄：消费方（ACP 桥、子 agent、作业）编程时
// 只对着 [Registry] 和 [Agent]，不必依赖具体那个循环包。
//
// # 事件在这里是显式登记的观察者
//
// 源: packages/core/agent/src/runtime-types.ts:146-292
//
// DSH 那十二个 cordis 事件在这里是 [Registry] 上的十二组登记，按派发语义分三类：
//
//   - **只通知**（DSH 的 `@mode emit`）：created、disposed、status、
//     inbox/inserted、inbox/claimed、inbox/discarded、session-start、error。
//     观察者 panic 被逐个兜住记日志，拦不下任何东西。唯一的例外是
//     created——见下。
//   - **串行**（`@mode serial`）：turn-stopping。按登记顺序一个个跑完。
//   - **瀑布**（`@mode waterfall`）：pre-step、request、request-error。先登记的
//     裹在后登记的外面，每一层拿到一个 next 决定要不要往里走；最里面那个 next
//     交出机器本来的答案。
//
// created 是唯一有否决权的：一个观察者返回错误（或者 panic）会让
// [Registry.Announce] 失败，调用方交出去的那个摘除函数随即把这次登记回滚掉，
// 并配对地发出一次 disposed。这条和 [github.com/snight1983/ds-harness-go/harness/session.Store] 上的
// session/created 逐字相同，实现也是同一套。
//
// 派发按作用域过滤，规矩和本仓库其他几处一样：登记在全局层的看得见全部，
// 登记在某个作用域上的只看得见挂在那个作用域（及其子孙）上的 agent。
// DSH 的 scopeTarget(agent, agent) 载体在这里是 [Agent.Scope] 交出的那把钥匙。
//
// 新增: dispatch.ts 那个「融合派发器」整套不移。它在 DSH 存在的理由是把
// cordis 的 `Events` 映射按「负载里带 agent 且 this 是 Scoped<Agent>」筛出一个
// 子集（AgentSubjectEvent），再靠泛型把 agent 字段注进去，好让主体和作用域键
// 不可能对不上。Go 里每一组观察者是一个具名函数类型、每一次派发是一个具名方法，
// 主体和载体都写在方法体里，调用方连传都传不进来——那个类型体操防的事在这里
// 不可能发生。
//
// # 进程内的发起者靠 context 传
//
// 源: packages/core/agent/src/index.ts:256-282、520-560、648-706
//
// 新增: DSH 用两个 node:async_hooks 的 AsyncLocalStorage 把「这条异步调用链是
// 谁发起的」顺着 await 传下去，于是它需要一整套 closing/draining/disposed 状态
// 机（initiatorState、activeInitiatorRuns、initiatorDrain、
// releaseReentrantInitiatorRuns、hasLifecycleAncestor），只为了在服务卸载时
// 等那些还挂在 ALS 上的 continuation 排干、再 disable() 掉存储。
//
// Go 里这件事就是 [context.Context] 的值：[WithInitiator] 派生一个带发起者的
// ctx，[CurrentInitiator] 从 ctx 上读回来。值随 ctx 走、随 ctx 死，没有「retain
// 了一份引用要排干」这回事，所以那套状态机整个消失，连同 DISPOSED_INITIATOR_MESSAGE
// 那条只为它存在的诊断——「发起者作用域已经处置了」在 Go 里根本不是一种失败，
// 一个过期的 ctx 只是不再被谁传下去而已。[WithoutInitiator] 还在——它表达的是
// 「这段活儿故意不认爹」，那个意图和实现手段无关。
//
// # 这里没有照抄的部分
//
// 新增: typert 的 lookups/contexts 登记（index.ts:281-296）不移。typert 是 DSH
// 自己造的一套运行期类型系统，用来把线上的 agentId 解回宿主对象；Go 里
// [Registry.Get] 就是那个解析函数，类型由编译器管。
//
// 新增: ctx.accessor('agent')（index.ts:297-306）不移。它在 DSH 存在的理由是
// 「让一个普通 cordis 上下文读 ctx.agent 得到 undefined 而不是抛未知属性」，
// 那是 cordis 代理的实现细节。Go 里同一件事是 [CurrentInitiator] 的第二个返回值。
//
// 新增: getTraceable / Reflect.apply / symbols.original（index.ts:406-429）不移。
// 它们在 DSH 存在的理由是「一个 cordis Service 被当成工厂传进来时，避免叠两层
// 追踪代理，同时让工厂的 effect 挂到调用方的 fiber 上」。Go 里 [Factory] 就是
// 一个接口值，调用方的归属由传进去的 ctx 和作用域显式表达。
//
// 新增: invariant.ts 那个 cordis 伴生插件（name/inject/apply）不移成插件，
// 只留它守的那条规则本身，见 [Registry.OnStatus] 上的注释。
//
// # 并发
//
// 新增: DSH 是单线程 JS。Go 里 [Registry] 会被循环、ACP 桥、子 agent 多个
// goroutine 同时碰到，所以它有自己的互斥锁，规矩和
// [github.com/snight1983/ds-harness-go/harness/session.Store] 逐字相同：**观察者一律在锁外调用**，
// 于是一个观察者回头读同一张表不会自锁。
//
// [Inbox] 不加锁。它是一个 agent 自己那份投影，只该被那个 agent 的循环碰——
// 和会话日志「只该有一个写者」是同一条规则，理由也一样。
package agent
