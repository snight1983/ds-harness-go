// Package session 是会话活着的那一半：一个可追加的 [Session]、装着它们的
// [Store]、以及分叉。
//
// 对应 DSH 的 @deepseek-ai/dsh-session 里 index.ts 与 preparation.ts 那两份
// 运行期实现（packages/core/session）。
//
// 源: packages/core/session/src/index.ts:1-36
//
// # 为什么和 github.com/snight1983/ds-harness-go/session 分成两个包
//
// DSH 那个包里既有词汇（事件信封、负载、表面折叠、不变量），也有活的对象
// （Session、SessionStore）。本仓库把它劈成两半：
//
//   - [github.com/snight1983/ds-harness-go/session] 是**词汇**——纯值和纯函数，持久化后端、查询、
//     回放、统计都只需要它，谁都不必为了读一条事件而把一个内存注册表拖进来。
//   - 本包是**活的那一半**——可变状态、发布钩子、作用域派发。它依赖词汇，
//     词汇不依赖它。
//
// 这条边界不是审美：本仓库里 session/persistence、session/projection、
// session/stats 这些包全部只 import 词汇那一半。合成一个包的话，一个只想
// 排一条事件的调用方会连带拿到 [Store] 和它整套 [github.com/snight1983/ds-harness-go/core/scope]
// 依赖。
//
// 两个包的 Go 包名都是 session，本包在源码里把词汇那一半 import 成
// sessionlog。名字取自它保管的东西：会话日志的那些值。
//
// # 事件在这里是显式登记的观察者
//
// 源: packages/core/session/src/index.ts:37-93
//
// DSH 那四个 cordis 事件在这里是 [Store] 上的四组登记：
//
//   - session/created → [Store.OnCreated]。它是**有否决权**的：一个观察者返回
//     错误（或者 panic）会让 [Store.Announce] 失败，调用方交出去的 detach
//     随即把这次 attach 回滚掉，并配对地发出一次 disposed。
//   - session/disposed → [Store.OnDisposed]。只观察，失败被记日志兜住。
//   - session/event → [Store.OnEvent]。提交之后的广播，同上只观察。
//   - session/flush → [Store.OnFlush]。要等的耐久检查点，并行跑、无否决。
//
// 派发按作用域过滤，规矩和本仓库其他几处一样：登记在全局层的看得见全部，
// 登记在某个作用域上的只看得见那个作用域（及其子孙）里进来的会话。
//
// 新增: cordis 的 Service / ctx.sessions / typert lookup 登记全部不移，理由和
// 本仓库其他几个注册表逐字相同——装配方自己造一个 [Store] 拿着。
//
// # 并发
//
// 新增: DSH 是单线程 JS，一个 Session 的 log 不可能被两处同时改。Go 里
// [Session] 会被循环、持久化、遥测多个 goroutine 同时碰到，所以它有自己的
// 互斥锁，[Store] 也有。两把锁**从不同时持有**：追加路径在锁外调用观察者，
// 存储路径在锁外调用观察者，于是一个观察者回头读同一个会话不会自锁。
//
// DSH 那个 `appending` 重入标记在这里叫 publishing，含义扩了一点：它既挡住
// DSH 挡的那件事（一个 session/event 观察者在回调里又追加），也挡住 Go 才有
// 的那件事（另一个 goroutine 同时追加）。一个会话的日志**只该有一个写者**
// ——它自己那个循环；撞上这条错误说明有第二个写者，那是缺陷不是竞争。
//
// # 联合类型拆成两个入口
//
// DSH 用 TS 的联合类型表达「两种调用形态」，Go 里一律拆成两个函数：
//
//   - PrepareSessionOptions（`seedSource?: undefined` 与
//     `seedSource: 'persistence'` 两支）→ [Store.Prepare] 与
//     [Store.PrepareRestored]。后者接手调用方交出所有权的那份日志，不复制。
//   - SessionForkSource（`Session | SessionId`）→ [Store.Fork] 与
//     [Store.ForkByID]。拆开之后 [ForkNotLive] 只可能从前者产生，
//     这一点在 TS 那边要靠读实现才看得出来。
//
// # 这里没有照抄的部分
//
// 新增: deepFreeze / freezeRestoredObject / structuredClone / snapshotJsonValue
// 全部不移。Go 的结构体是值，切片那一层由 [github.com/snight1983/ds-harness-go/session.Event.Clone]
// 复制；「冻结」在 Go 里由「交出去的是复制品」兑现。DSH 靠 snapshotJsonValue
// 回传 undefined 判断「排不成 JSON」，Go 这边负载本来就是 json.RawMessage，
// 那件事变成 json.Valid。
//
// 新增: attachments 那张 WeakMap 不移。它在 DSH 存在的理由是「让 Session 在
// 公开面上和 Store 无关，同时让 append 找得到发布钩子」。Go 里 [Session] 和
// [Store] 同包，一个不导出的字段做的是同一件事，而且不必回收。
//
// 新增: assertSessionEventEnvelope 里那圈信封键白名单不移——它在 Go 里是
// [github.com/snight1983/ds-harness-go/session.Event] 的 UnmarshalJSON 干的活，而且干得更早。
// 同理 `data !== undefined`、`ignorable !== true`、Number.isSafeInteger 那几条
// 在 Go 的类型上不可能违反，见 [validateSeedEvent] 各处的注释。
package session
