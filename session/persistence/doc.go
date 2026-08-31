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
// # 这里只有词汇和纯函数，编排不在这里
//
// DSH 的 dsh-session-persistence 包里装着三样东西：一套**接口加纯函数**、
// 一个 [WriteBehind] 那样的自足控制器、和一个 1362 行的 PersistenceCoordinator
// ——后者是活的：它按会话串行化每一次操作、监听 cordis 的
// `session/created` `session/event` `session/flush` `session/disposed`
// 四个事件、把一个活的 Session 认领进来、在 HMR 之后重新播种。
//
// 本包只是前两样。理由和 llm、session 两个包那两次一模一样：编排那一层需要
// 一个活的 Session 和一个活的 SessionStore，而这两样按 DESIGN.md 第八节的
// 顺序落在第 6 块（循环）。把编排放进本包，任何一个只想实现一个后端的人
// 都得先把整套循环立起来。
//
// 留在第 6 块的：PersistenceCoordinator 整个类、SessionPreparations
// （它的每一个阶段都绑在一个活的 Session 上）、以及 Store.Prepare
// ——DSH 的 SessionPersistence.prepare 返回的是一个未发布的活会话，
// 所以本包的 [Store] 里没有这个方法。
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
package persistence
