// Package persistence 是会话日志落盘这套词汇：一份存档读回来长什么样、
// 一个后端要提供哪几样原始能力、读到一份不是本构建写的日志时怎么拒、
// 以及一个活会话的事件怎么攒成批写下去。
//
// 源: packages/session/session-persistence/src/index.ts
// 源: packages/session/session-persistence/src/revision.ts
// 源: packages/session/session-persistence/src/coordinator.ts
// 源: packages/session/session-persistence/src/write-behind.ts
// 源: packages/session/session-persistence/src/preparations.ts
// 源: packages/session/session-persistence/src/invariant.ts
//
// # 这里有什么
//
// 三层，从下往上：
//
//  1. **词汇和纯函数。**[Backend] 是给具体介质用的最小原始能力集，
//     [Store] 是给使用方用的服务面；[CheckStored]、[BalanceStored]、
//     [SeedCoversPrefix] 这些是读回一份存档之后要过的判据。
//
//  2. **[WriteBehind]。**一个自足的攒批控制器，不认识会话，只认识
//     「攒一批、到点写下去、写失败了把缓冲留着」。
//
//  3. **[Coordinator]。**活的那一层：它按会话身份串行化每一次操作，
//     挂在会话存储的四条观察者（创建／事件／刷盘／退场）上，把一个活会话
//     认领进来，并维护那个准备池（[preparations]）。
//
// 第三层比前两层晚落地：它要一个活的 [github.com/snight1983/ds-harness-go/core/session.Session]
// 和一个活的会话存储，而那两样按 DESIGN.md 第八节的顺序在第 6 块才有。
// 分层的好处留着了——只想实现一个后端的人只需要看第一层。
//
// # 这里没有照抄的部分
//
//   - invariant.ts 整份。DSH 那份文件本身就是一个**空实现**，它自己的注释
//     写明了理由：持久化的正确性要靠后端往返和崩溃尾巴的测试来立，
//     这个包没有任何一条可以在进程内被持续观察的关系。没有东西可移。
//
//   - 四个 legacy 形状的升级器（migrateLegacySteeringEvent、
//     migrateLegacyTurnStartEvent、migrateLegacyTurnEndEvent、
//     migrateLegacyMessageEvent）和它们的三个帮手（legacyMessageId、
//     replacementStart、needsLegacyPrefix）。[session.FormatVersion] 的文档
//     已经裁过这件事：未发布期间钉在 0，不承诺任何兼容性，不匹配的日志直接
//     拒收，不提供迁移。既然不迁移，就没有要升级的旧形状。
//
//   - assertSupportedEvents。它拒的是 DSH 自己 v0 时期写出过的三种形状
//     （request/header-delta、mode/set、reason 为 fallback 的 request/header）。
//     本仓库从来没有写出过这三种字节，所以没有要拒的东西；真出现了，
//     [session.CheckVocabulary] 会按「不认识又没标可跳过」把它拦下。
//
//   - asRecord、hasOnlyKeys。那是 DSH 用来在 unknown 上做形状探测的两个
//     帮手，只被 legacy 升级器用。encoding/json 直接解进具名结构体就够了。
//
//   - AbortSignal → context.Context。本包每一个可能阻塞的方法第一个参数
//     都是 ctx，取消由它带；DSH 那套 `signal?.throwIfAborted()` 随之消失。
//
//   - MAX_WRITE_BATCH_DELAY_MS。DSH 这个常量等于 Node 定时器能接受的最大
//     延迟（约 24.8 天），存在的意义是「超过这个数 setTimeout 会立刻触发」。
//     Go 的 [time.Timer] 收 [time.Duration]，上限是 int64 纳秒（约 292 年），
//     一个批处理窗口够不着它，所以这个上限不存在。
//
// # 谁来实现
//
// [Backend] 是给具体介质用的最小原始能力集；[Store] 是给使用方用的服务面。
// 第一方的两个后端（JSONL、SQLite）在裁决表里是 OUT_OF_SCOPE
// ——本仓库是一个空运行时，落盘介质由装配它的人挑。本包给出的是那道缝。
//
// # 覆盖率为什么到不了 99%
//
// docs/DESIGN.md 第九节给纯逻辑包定的线是 99%，低于这个数要在源码里写明为什么。
// 本包的用例覆盖到 98.0%，没覆盖到的都在 [Coordinator] 那一层，分三类：
//
//  1. **只有在一段串行化区间**里**ctx 断掉才走得到的那几句。**
//     [Coordinator.adopt] 每一轮开头、[Coordinator.readFromCore] 和
//     [Coordinator.readStoredPrefix] 里那三处 `ctx.Err()`，还有
//     [Coordinator.inspectOnce] 收尾里那一句。调用方进这些函数之前 serialize
//     已经查过一次 ctx，所以要走到它们，ctx 必须**恰好**死在排队之后、
//     这一句之前。这几句本身就是为那个窗口写的（一条已经取消的 ctx 会让
//     adopt 那圈重试空转到天荒地老），构造不出来不等于可以删。
//
//  2. **一个身份在一次读操作跑到半路时才活过来。**[Coordinator.Load] 里
//     独占消掉之后那次 `sessions.Get`，以及 [Coordinator.inspectOnce] 里
//     那三处。它们守的是同一件事：这条读路正在跟磁盘打交道的时候，别人把
//     这个身份发布成了活会话，那么这一份从磁盘读出来的视图立刻就旧了，
//     该改读那个活会话。要在用例里钉住这个交错，得给内存后端加一套「读到
//     一半停住」的闸，而那套闸本身比它验的这四句还长。
//
//  3. **三句标着「走不到」的兜底。**[Coordinator.loadLiveSnapshot] 里
//     「刷盘成功了状态却没立起来」、[Coordinator.reconcileTracked] 里
//     「同一个会话对象重入」、[Coordinator.seedMatchesPersisted] 里
//     「游标大于零却读不到这份存档」。三句的注释各自写明了为什么走不到，
//     以及为什么留着它而不是断言掉。
//
// 剩下四句零星的：[Coordinator.attachPrepared] 里那次 `attach` 失败
// （[preparations.reservationFor] 会先一步把同一种局面拒掉，所以到这里
// 已经不可能过期）、[Coordinator.onCreated] 情形 4 里那次 `createCore` 失败
// （要求存档在同一次调用的两次读之间凭空出现）、[Coordinator.adoptLivePrefix]
// 里那次 [SeedCoversPrefix] 失败和 [Coordinator.loadLiveSnapshot] 里那次
// [github.com/snight1983/ds-harness-go/session.InterruptedTurnClosers] 失败（两者都要求一条负载
// 排不出去／解不回来的事件，而它们进不了一个活会话）。
package persistence
