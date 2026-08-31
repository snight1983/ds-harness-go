// Package projection 是「把会话日志折成状态」这套词汇：一个域怎么交出一份
// 纯折叠、框架怎么替所有已登记的单元把每一条已提交的事件推过去、以及那份
// 折叠结果怎么被切成一致的读面、存成检查点、再从检查点接着往下折。
//
// 源: packages/session/session-projection/src/index.ts
// 源: packages/session/session-projection/src/types.ts
// 源: packages/session/session-projection/src/invariant.ts
//
// # 这一层在解决什么
//
// 日志是权威事实，但没人能直接用一串事件干活：要显示待办清单、要算 token
// 用量、要知道当前开着哪个回合，每一样都得从头把日志扫一遍。投影就是把
// 「从头扫一遍」这件事收进框架里做一次：域只写一个纯函数
// （[Definition.Apply]：上一个状态 + 一条事件 → 下一个状态），框架负责订阅、
// 逐会话记水位、以及值变了的时候通知出去。域不持有任何订阅，也不知道谁在读；
// 读的一方不知道值是怎么算出来的。
//
// **整值事件规则**（承重）：一条带状态的日志事件必须携带**改完之后的完整状态**，
// 绝不能只带一个增量。这条规则让每个单元的转移都便宜到可以无脑跑，
// 也让每一个被服务出去的值自己就说明了自己。
//
// # 这里没有照抄的部分
//
//   - cordis 的 Service 与 `ctx.sessionProjections`。本仓库没有 cordis，
//     [Registry] 就是一个普通对象，谁装配谁拿着。DSH 靠 `ctx.on('session/event')`
//     自己订阅，本包改成由会话那一侧在提交之后显式调 [Registry.Drive]
//     ——推进的次序本来就是会话自己的事，让它显式地说出来比藏在事件总线里清楚。
//
//   - `SessionProjectionMap` / `SessionProjectionStateMap` 两张可合并扩展的
//     类型表（types.ts 整份）。它们是 TypeScript 的 declaration merging：域包
//     往一个全局 interface 上加一个键，于是 `snapshot().values` 的类型跟着长。
//     Go 没有这个机制，也不需要——本包的键就是字符串，值的类型由登记它的那个
//     [Definition] 的类型参数带着，读的一方按自己登记的那个类型断言回去。
//
//   - `stateSchema` / `viewSchema` 两个 zod 校验器。视图那个直接消失：
//     DSH 要它是因为 `view` 的返回值在运行时没有类型，Go 这边 [Definition.View]
//     的返回类型就是它的 schema。状态那个**留着但换了形**：它校验的是从盘上读回来
//     的字节，那件事在 Go 里同样必须做，所以变成必填的 [Definition.DecodeState]。
//
//   - `structuredClone`。DSH 在 [Registry.Checkpoint] 里克隆一份状态，
//     免得调用方拿到活引用之后把缓存改坏。本包直接 [encoding/json.Marshal]：
//     脱离引用和「调用方最终想要的那份可落盘的行」是同一件事，顺手还把
//     「状态必须是纯 JSON」这条前提从注释变成了会报错的检查。
//
//   - invariant.ts 整份。DSH 那份文件自己就是空实现，注释里写明了理由：
//     这个包的约束全都在服务内部同步执行、由它自己的测试立住，没有任何一条
//     可以在进程内被持续观察的关系。没有东西可移。
//
// # 这里和 DSH 不一样的地方
//
//   - **变没变由单元自己说**。DSH 的契约是「不关心这条事件的单元必须返回**同一个
//     引用**」，框架拿 `Object.is` 判有没有变。JS 里引用比较是白送的，Go 里不是：
//     状态多半是带切片或映射的结构体，`==` 会 panic，[reflect.DeepEqual] 又贵。
//     所以 [Definition.Apply] 直接返回第二个值说自己变没变——移的是「单元便宜地
//     告诉框架有没有变，没变就零下游开销」这个能力，不是引用比较这个手段。
//
//   - **单元格按会话身份存，不按对象身份**。DSH 用 `WeakMap<Session, UnitCell>`，
//     会话对象被回收时单元格跟着走。Go 这边改成 map[[session.SessionID]]，
//     由 [Registry.Forget] 显式清。理由有两条：活会话在 DSH 里本来就是一个 id
//     一个对象，按身份存和按对象存等价；而显式的清理点是可以写测试的，
//     靠 GC 的那条路不行。
//
//   - **中途才建单元格时按 seq 取前缀，不按下标切**。DSH 写的是
//     `session.events.slice(0, event.seq)`，注释说「seq 就是数组下标」。
//     本仓库的 [session.Trace] 只保证 seq **严格递增**，不保证从 0 起密排，
//     所以这里按「seq 小于这条事件」取前缀。
//
//   - **推进时挡一次重复应用**。一个读的一方正好在「事件已经追加进日志」和
//     「[Registry.Drive] 被调到」之间懒建了单元格，那份单元格已经把这条事件
//     折进去了。DSH 是单线程，这个窗口不存在；本仓库要扛并发读，所以
//     [Registry.Drive] 先比一次水位。
package projection
