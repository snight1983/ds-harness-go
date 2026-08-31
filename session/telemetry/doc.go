// Package telemetry 是会话事件上报的**采集侧**。
//
// 对应 DSH 的 @deepseek-ai/dsh-session-telemetry（packages/session/session-telemetry）。
//
// 源: packages/session/session-telemetry/src/index.ts:1-14
//
// # 这一层管什么
//
// 它只管四件事：**有哪些记录**（那个固定的分块投影）、**记录里带什么**
// （那份逻辑记录）、**什么时候采**（认领、逐条事件、生命周期几个边界），
// 以及**交出去之前谁能改它**（脱敏规则那条瀑布）。
//
// [Sink.Emit] 下游的一切——攒批、重试、排队、丢弃策略——都是上报 SDK 那一侧的
// 事，本包一个字都不建模。这条线划在这里是有理由的：采集跑在 agent 循环的
// 热路径上（每追加一条事件同步走一趟），而攒批和重试天生是要等的。两件事
// 混在一个组件里，那个「要等的」迟早会拖住那个「不能等的」。
//
// # 两条通道
//
// ledger 通道和会话日志一一对应：一条事件一条记录，带着 event.seq，
// 接收方靠 (session.id, event.seq) 去重。ops 通道是**日志里没有家**的那两个
// 信号（agent-error 与 shutdown），它们**故意不带** event.seq 那类身份，
// 免得被当成 ledger 的行。
//
// # 固定的分块投影
//
// 每个 (turn, step) 只有**第一条** [session.EventAssistantChunk] 会被交出去，
// 当作「这个步骤开始出字了」这一个信号。内容不会丢——一个步骤装配好的
// [session.EventAssistantMessage] 里是逐字节完整的。于是介质上 seq 有洞是常态，
// **永远不是**丢数据的信号。
//
// # 交接游标
//
// 每个会话记一个「已经交出去的最高 seq」。它标的是**交出去**，不是**送到**：
// [Sink.Emit] 之后就往前走，因为再往下的投递是 SDK 的事，采集侧看不见。
// 没有游标时退回 [SessionView.FirstLiveSeq] 减一——不是 0：构造种子里的内容
// 早就以另一个身份离开过进程（同一个 id 在上一个进程里恢复，或者父会话的流），
// 重新交一遍等于重复上报。
//
// # 这里没有照抄的部分
//
// index.ts 那个 cordis Service（SessionTelemetryBackend 抽象类、
// `Context.sessionTelemetry` 声明合并、以及 `session-telemetry/record` 那个
// waterfall 事件的声明）在 Go 里散成三样：[Sink] 是那份契约，
// [SharingStatus] 是那套词汇，[Rule] 是那条瀑布。装配方自己拿着实现去构造
// [Coordinator]，没有服务注册这一层。
//
// invariant.ts 是一个空的安装器（它自己写明了为什么没有运行期不变量），
// 没有可移植的东西。
//
// # 这里和 DSH 不一样的地方
//
//  1. **没有总线，采集点是方法。** DSH 在 cordis 上订阅 session/created、
//     session/disposed、session/event、session/flush、agent/error 五个事件，
//     外加一次 `ctx.sessions.list()` 扫已经活着的会话。Go 这边它们是
//     [Coordinator.Adopt]、[Coordinator.Retire]、[Coordinator.Observe]、
//     [Coordinator.HintFlush]、[Coordinator.RelayError]，由装配方在同样那几个
//     位置调。同样的做法见 session/projectioncache。
//
//  2. **live 与 on-demand 不是一个开关，是「调不调 Adopt」。** DSH 的
//     SessionTelemetryCapture 决定注册哪几个监听器。Go 里没有注册这回事：
//     一个装配方要 on-demand，就只调 [Coordinator.CaptureSession]，
//     永远不调 [Coordinator.Adopt]。ops 记录只为**已认领**的会话产生，
//     所以「on-demand 不产生 ops 记录」这条行为自动成立，不需要一个字段去说。
//
//  3. **交接游标是这个协调器自己的一张表，不是模块级的 WeakMap。**
//     DSH 那份 WeakMap 挂在模块作用域上，它自己在注释里承认这是对
//     「注册即副作用」纪律的一次破例，理由是 cordis 没有 HMR 的状态交接接口。
//     Go 没有 HMR，那个理由整个不存在，所以游标就是 [Coordinator] 的字段，
//     [Coordinator.Retire] 时删掉。
//
//  4. **脱敏瀑布是一串显式的 [Rule]，不是总线上的监听器。** 语义逐条对齐
//     cordis 的 waterfall：每条规则拿到的都是那份原始记录加一个 next，
//     调 next 就往下走并可以再加工它的返回值，**不调 next 就把底下全部替换掉**。
//     这正好是 Go 里 http 中间件那条链的形状。
//
//  5. **一条规则可以返回错误，那条记录就被扣下。** DSH 靠抛异常加 contain
//     达到同一效果（它管这叫 fail-closed）。Go 有错误返回，就用错误返回；
//     panic 仍然被 [Coordinator] 兜住，理由见下一条。
//
//  6. **兜住 panic 的理由换了。** DSH 兜的是 cordis `emit` 遇错即停、会饿死
//     后面的订阅者。Go 里没有那条总线。这里兜的是另一件事：采集同步跑在
//     agent 循环的事件路径上，而规则和接收器都是**部署方挂上来的**代码。
//     它们炸了不该把循环一起炸掉——上报是尽力而为的观察，不是业务。
//
//  7. **记录在交出去之前验一遍。** DSH 靠 TypeScript 保证 attributes 的值
//     只能是 string 或 number。Go 的 map[string]any 保证不了，所以
//     [Record.Validate] 在这里把那条约束补回来：通道和级别落在封闭词汇里、
//     每个属性值是字符串或数字、body 是一段合法 JSON。验不过的记录被扣下并
//     记一条日志，不交给接收器——接收器多半要把它排成 OTLP，在那儿炸没人查得出来。
//
//  8. **body 是 [encoding/json.RawMessage]，深拷贝就是 bytes.Clone。**
//     DSH 要 structuredClone 一份 event.data，因为那是一个可变对象而接收器
//     稍后才序列化。Go 这边负载本来就是字节，复制一份即可，ops 记录也排成
//     同一种形状，两条通道的 body 类型统一。
//
//  9. **ops 记录的时刻可以注入。** DSH 直接调 Date.now()，于是那两条记录的
//     time 字段在测试里断言不了。这里是 [Options.Now]，留空才用真时钟。
package telemetry
