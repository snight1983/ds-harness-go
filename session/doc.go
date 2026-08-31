// Package session 是会话事件日志这套词汇：一条日志条目长什么样、有哪几种事件、
// 哪些事件产出模型消息、一份崩在半路的日志怎么补齐成一份提供方肯收的历史。
//
// 源: packages/core/session/src/types.ts
// 源: packages/core/session/src/known-event-types.ts
// 源: packages/core/session/src/surface.ts
// 源: packages/core/session/src/repair.ts
// 源: packages/core/session/src/request-header.ts
// 源: packages/core/session/src/chunk-rows.ts
// 源: packages/core/session/src/invariant.ts
// 源: packages/core/session/src/json.ts
//
// # 这里只有词汇，服务不在这里
//
// DSH 的 dsh-session 包里装着两样东西：一套**值加纯函数**，和一个活的
// Session／SessionStore（事件总线、发布时机、seed 生命周期、Disposable 的
// 构造中会话）。本包只是前者。
//
// 分开的理由和 llm 包那次一模一样：持久化那一层要把每一条事件原样落盘、
// 要在进程重启后把一份半截日志读回来补齐，它需要的是 [Event]、[ValidateLog]、
// [InterruptedTurnClosers]、[FoldSurface] 这些**值和纯函数**；而事件总线、
// 发布顺序、seed 归属那一套一个字节都不写进日志。把两者放一个包里，
// 持久化就得连着一整套它用不到的东西一起立起来。
//
// 活的那一半（Session、SessionStore、PrepareSessionOptions 那一族构造选项、
// 构造中会话的 Disposable 交接）留在移植顺序的第 6 块，和循环一起落地。
//
// # 事件数据为什么是 json.RawMessage
//
// [Event.Data] 是一段原始字节，不是一个已经解好的联合类型——这和 llm 包里
// [llm.ContentBlock] 那几个联合的做法**正好相反**，是有意的。
//
// 内容块是一个五到七个变体的封闭词汇，每一个消费方都要 switch 它，
// 所以解开的收益大于代价。事件类型不是：DSH 自己那份登记表里有 48 个类型，
// 其中 35 个属于本仓库还没移植到的包，而日志是**持久**的——一个只认识 13 个
// 类型的构建读到一条 compaction/summary 时，正确的行为是原样保管那段字节，
// 而不是把它解成一个「未知」再排回去时丢掉字段。
//
// 需要具名字段的时候用 [DecodeData]，它按 [Event.Type] 解成 [EventData] 那 13 个
// 变体之一，认不出来的落进 [RawData]。解码是**按需**的：读一份日志不会因为
// 里面有一条本构建读不懂的事件而整个失败，那件事由 [CheckVocabulary] 单独判，
// 判据是 [Event.Ignorable] 而不是「能不能解开」。
//
// # 这里没有照抄的部分
//
// 下面每一条都是 DSH 用 TypeScript 或 JS 运行时的机制解决的问题，
// 而 Go 有它自己的答案。
//
//   - json.ts 整份（JsonValue、isJsonValue、snapshotJsonValue）→ encoding/json。
//     那份文件从头到尾在防 JS 对象图的危险：伪造的原型、取值器、稀疏数组、
//     环、-0、非有限数。这些在 Go 里要么根本不存在（没有原型、没有取值器、
//     切片不稀疏），要么 encoding/json 自己就会拒（环报错、NaN 与 Inf 报错）。
//     snapshotJsonValue 那个「验一遍顺手脱钩」的动作就是 json.Marshal：
//     排得出去就说明是合法 JSON，排出来的字节和源对象再无关系。
//     所以本包不包一层同名函数——按仓库既定的「用 Go 现成的办法」，
//     多包一层只会多一个要维护的名字。
//
//   - Branded<'SessionId'> → 具名 string 类型，理由同 llm 包。
//
//   - 映射类型条件字段（`K extends SurfaceEventType ? {...} : object`）→
//     一个结构体加一条运行期检查。DSH 用类型系统保证「只有三种事件能带
//     surfaceOp」，编译期就拦住了写错的调用点。Go 的结构体做不到按字段值
//     裁剪另一批字段，所以 [Event] 上三个字段都在，那条约束由 [SurfaceOpOf]
//     在读和写两侧各验一次——DSH 在运行期也验（surfaceOpOf），本包只是没有
//     它那道编译期的第二层。
//
//   - Number.isSafeInteger 那一整套 → int64 的精确算术。
//     DSH 里 chunk-rows.ts 和 surface.ts 反复检查安全整数，因为 JS 的数字是
//     float64，两个大整数相减会四舍五入到一个**别的**整数，于是一条时间戳
//     解回来会变。Go 的 int64 减法在范围内逐位精确，这类检查整片消失；
//     只有真正的溢出还需要一句判断，见 [PackChunkRuns]。
//
//   - cordis 的不变量伴生插件（invariant.ts 的 install/apply）→ 不在本包。
//     本包只留那份纯粹的检查器 [Trace]，它不认识任何容器。
//     把它挂上本仓库的 invariants 注册表是第 6 块的事。
//
//   - assertNever → 不设。Go 的 switch 走到 default 就是走到 default，
//     该报错的地方直接返回一条错误，不需要一个靠「永远不该被调用」立身的函数。
//
//   - WeakMap／WeakSet 记对象身份（invariant.ts 的 traces、stagedTransitions）→
//     不需要。那两张表是为了给一个活的 Session 对象挂旁路状态；本包的
//     [Trace] 就是一个普通的值，谁拥有它谁自己拿着。
//
// # 覆盖率封顶在 98.1%
//
// 本包是纯逻辑包，按仓库的规矩该到 99%，但它到不了，差的那 15 个语句块
// 全部**结构性不可达**，逐个查过：
//
//   - 对着本地拼出来的具体结构体调 json.Marshal 的那些 `if err != nil`
//     （chunkrow.go 的 245／270／279／291／305、repair.go 的 167／188／196）。
//     被排的值里没有接口、没有 map、没有环、没有非有限浮点，encoding/json
//     没有失败的余地；但错误照样得接住，不接住是更糟的代码。
//
//   - 守在 llm 那几个**封在 llm 包里**的联合背后的分支
//     （surface.go 的 278／329／333、eventdata.go 的 156）。要走到它们得有一个
//     排不出去的 [llm.MessageSource] 或 [llm.StreamChunk]，而那两个接口都带着
//     llm 自己的非导出方法，本包的测试造不出第三个变体。本包内部的两个联合
//     （[SurfaceOp]、[TurnEndCancelCause]）就没有这个问题，它们的防御分支都
//     由测试里的本地变体走到了。
//
//   - 先用一个结构体探针解成功、再解一遍才可能失败的那两处
//     （chunkrow.go:343 的 map 二次解，turnend.go:198 的 abortedWire）。
//     探针能解成功就说明那一段是个对象，第二遍解不会再失败。
//
// 这几处要么删掉错误处理（更糟），要么放宽封装只为让测试走到（更糟）。
// 记在这里，是为了让下一个看见 98.1% 的人不必再查一遍。
package session
