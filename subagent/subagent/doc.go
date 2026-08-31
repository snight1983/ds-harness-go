// Package subagent 是「子 agent」这条能力接缝的**服务定义**：一张按名字的提供方
// 注册表、一条验能力的一次性派发，外加耐久可续孩子那一整套编排。
//
// 源: packages/subagent/subagent/src/index.ts:1-31
//
// 和 bash 那条接缝（一个上下文一个执行器，第二次装载直接报错）不一样，这里**多个**
// 提供方共存：各自按唯一的名字登记，调用方点名取用。形状对着的是
// [ds-harness-go/llm.Runtime.RegisterAdapter] 那张适配器注册表。
//
// 本包只扮演服务定义那一角。提供方（spawn／fork／acp）和面向模型的消费方
// （tool-subagent）各自成包。
//
// # 两种子 agent 形态
//
// **一次性运行**（[Runtime.Start]）：提供方在交回 [Run] 之前就把孩子立起来，所以
// 「兑现」就是那唯一一道公布与所有权转移的边界。一次失败既不留下要调用方处置的
// 运行，也不发任何生命周期边。发布之后的失败从交回的那个运行结清。
//
// **可续孩子**（[Runtime.StartContinuable]）：一份耐久会话，加上至多一个进程内的
// 驻留轮次（活化）。它**绝不**变成一个 [Run]——续接管理器直接攥着孩子的 agent 句柄，
// 每一个回合都经孩子自己的收件箱排队，所以提供方只贡献那份脱离的创建输入，
// 看不到句柄、回合和拆解。活化不是请求、不是结果、不是取消，也不是任务边界。
//
// 孩子与后代的发现（[Runtime.ListChildren]、[Runtime.ListDescendants]）只读活会话表
// 和可选的会话持久化，既不装载也不恢复任何 agent，更不需要那台续接运行时。
//
// # 同进程的提供方是可信的
//
// 请求、提供方描述符、结果和生命周期负载都是借来的不可变值。序列化和敌意输入的
// 校验属于真正的进程、worker、持久化和模型边界，不属于这里。
//
// # 生命周期
//
// 四条边（[StartObserver]、[EndObserver]、[ProviderAddedObserver]、
// [ProviderRemovedObserver]）经一个兜住异常的发射器发出去。运行那两条边带着发起
// 派发的那个父，按作用域分层派发；提供方那两条边没有父这个载体，只发给全局层。
// 「提供方来了」是唯一一条**不**被兜住的边——它发在一次登记的中途，它的失败就是
// 那次登记的失败。
//
// 一次性运行和可续活化用的是**同一套**开始／结束词汇，所以观察方看得见一个孩子的
// 开始与结清，而管理器究竟是物化了它、唤醒了它、还是冷恢复了它，不外露。
//
// [RegisterInvariants] 装上本包那两条运行期检查：提供方注册表的收支平衡，以及
// start／end 那对边的配对。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 那个服务从 cordis 上下文上 `ctx.inject(['agents'])`、
// `ctx.inject(['sessionProjections'])` 现取依赖，装没装、什么时候装都由容器决定。
// Go 没有那个容器，「在不在场」就是装配方手上有没有这个值，所以依赖一次性经
// [RuntimeOptions] 交清。[RuntimeOptions.Continuation] 里的 Agents 为 nil，就是那个
// `inject(['agents'])` 没兑现的情形：这套部署起不了可续孩子。
//
// 新增: 会话投影的登记在 Go 里是一个显式函数 [RegisterProjections]，由**装配方**
// 在它手上那张注册表上调用，[NewRuntime] 不代劳（成例见
// [ds-harness-go/plan/planmode]）。不调的后果是 [Runtime.ListChildren] 分不出孩子的
// 耐久身份。
//
// 新增: DSH 靠 cordis 的作用域派发过滤监听器，本包统一换成
// [ds-harness-go/core/scope.Layers]——全局层加各作用域的覆盖层，派发时按载体作用域的
// 父链取并集（成例见 [ds-harness-go/core/agent] 那十二个事件）。
//
// 新增: DSH 是单线程的，本包那几张表（提供方注册表、活化表、物化表）都另加了锁。
// 规矩全仓一致：**用户给的函数和一切会阻塞的调用一律在锁外**，锁里只做原子的簿记。
//
// 新增: DSH 的 AbortSignal 一律换成 [context.Context]；`Promise` 换成阻塞方法加
// 一次性关闭的通道。
package subagent
