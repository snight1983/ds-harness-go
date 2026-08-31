// Package projectioncache 是「把投影检查点落到耐久介质上」这套词汇。
//
// 对应 DSH 的 @deepseek-ai/dsh-session-projection-cache
// （packages/session/session-projection-cache）。
//
// 源: packages/session/session-projection-cache/src/index.ts:1-13
//
// # 这一层在解决什么
//
// session/projection 那一层能从一份日志把状态折出来，但折的代价随日志长度线性
// 增长。一个跑了两天、几万条事件的会话，每次冷启都从 seq 0 折一遍是不可接受的。
// 这一层存的就是那个折叠的中间结果：每个会话一条记录，记着它上一次被折到哪个
// seq、以及折到那里时每个单元的状态。
//
// **它是折叠的捷径，永远不是权威。** 一条记录可能是旧的（它自己的 seq 说明旧到
// 哪儿），但绝不会是错的。这一句话决定了这一层其余的全部设计：
//
//   - 每一条写路径都是 fail-soft。写丢了只意味着下一次冷读要多折一段尾巴，
//     所以一次写失败不该让触发它的那次读或者那条事件跟着失败。
//   - 状态版本对不上就**丢掉那一行**，不迁移。迁移意味着这一层要理解每个单元
//     的状态语义，而它一个都不认识；丢掉只是退回去重折一遍。
//   - 记录绑着日志身份（见 [Identity]）。会话 id 是一个**槽位**不是一段生命：
//     删掉再重建同一个 id、或者在缓存还活着的时候把持久化根目录换掉，都会让一条
//     旧记录通过所有水位检查，然后把一段和它毫无关系的日志折出来的状态端出去。
//
// # 三级读法
//
// 和 session/projection 那三级（[projection.Registry.Snapshot] 走活会话、
// [projection.Registry.ViewCheckpoint] 零 I/O、[projection.Registry.Restore]
// 冷读）接上，这一层给出的是带介质的那两级：
//
//   - [Cache.CachedSnapshot]：零 I/O。直接看已经读进内存的那条记录，不碰日志。
//     列表页用它，代价是它可能停在上一次耐久检查点那一刻。
//   - [Cache.ColdSnapshot]：不读整份日志的冷读。缓存行 + 从恢复地板起的一截
//     尾巴，折完再写回去，于是下一次冷读起点更近。缓存行被一份缩短了的日志
//     （崩溃修复截过尾）作废时退化成一次从 seq 0 的整读——阶梯最慢的那一级，
//     但仍然不会崩。
//
// # 这里没有照抄的部分
//
// DSH 的 SessionProjectionCache 是一个 cordis 的 Service，靠
// ctx.sessionProjectionCache 供出去，靠 static inject 声明它要的四个服务，
// 靠 ctx.on('session/event') 和 ctx.on('session/disposed') 订阅两条全局事件流。
// 本仓库不用 cordis：依赖由装配方在 [Options] 里显式递进来，两条订阅改成两个
// 显式方法（[Cache.Observe] 和 [Cache.Detach]）。
//
// spec.ts 那三份 zod schema（checkpointRow / checkpointIdentity /
// checkpointRecord）在 Go 里是 [Identity]、[Record] 两个结构体加一个
// [ValidateRecord]，理由和 storage/domain 包顶部那段一样：Go 的类型就是类型，
// 校验是 encoding/json 加一个校验函数，不需要在类型系统旁边再立一套说法。
//
// snapshotJsonValue 那一步整个消失了。DSH 要它是因为 checkpoint 交出来的
// `val` 是活对象，写下去之前必须脱开、而且要在那时候才发现「这个状态排不成
// JSON」。本仓库的 [projection.Registry.Checkpoint] 交出来的每个 Val 本来就
// 已经是排好的字节，脱离和那条契约检查都在更早的地方做完了。
//
// invariant.ts 是一个空的安装器（它自己写明了为什么没有运行期不变量），
// 没有可移植的东西。
//
// # 这里和 DSH 不一样的地方
//
//  1. **域由装配方打开，不由缓存自己打开。** DSH 在 Service.init 里
//     ctx.storageDomain.open(spec)，于是 this.table 是可空的、每次用之前要过一道
//     requireTable（那道检查在 DSH 自己的覆盖率里都是被忽略掉的死分支）。
//     Go 这边 [New] 收一个**已经打开**的域，句柄在构造时就取好——不存在
//     「还没初始化」这个状态，也就不需要那道检查。这也对上 storage/domain 那句
//     「域的生命周期归拿到句柄的那一方」：打开它的人负责关它。
//
//  2. **两条事件订阅变成两个方法。** DSH 挂在 cordis 的全局总线上，Go 里由
//     持有活会话的那一层（DESIGN.md 第八节第 6 块）在提交事件之后调
//     [Cache.Observe]、在会话脱离时调 [Cache.Detach]。
//
//  3. **[Cache.Detach] 是同步的，而且把错误交回去。** DSH 那边是
//     `void this.flushSoft(session, 'detach')`——一次脱离的异步调用。脱离是
//     活会话变冷的**最后一次机会**，这之后所有读都走缓存；把这一次写做成
//     即发即忘，等于让最重要的那次检查点去和进程退出赛跑。同步写掉、把失败
//     报给调用方，由调用方决定要不要忍——这才是 fail-soft 的正确落点。
//
//  4. **回退到整读时会留下一条 Warn。** DSH 的 catch 是空的：一条解不开的
//     缓存行和一条过期的缓存行走同一条静默回退路径。两者的成因完全不同——
//     过期是设计内的，解不开说明**这个构建自己**写坏了那一行，而它的症状只是
//     「每次冷读都慢一点」，没有任何人会去查。缓存这一层仍然照 DSH 回退（一个
//     缓存不该因为自己坏了就让读失败），但那次回退必须留下痕迹。
//
//  5. **落盘屏障是必填的。** 见 [Options.Flush]。
package projectioncache
