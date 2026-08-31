// Package llm 是与提供方无关的消息、内容、流式词汇，加上把请求路由到提供方
// 适配器上的那个运行时。
//
// 源: packages/llm/llm/src/types.ts
// 源: packages/llm/llm/src/message.ts
// 源: packages/llm/llm/src/brand.ts
// 源: packages/llm/llm/src/content.ts
// 源: packages/llm/llm/src/call-config.ts
// 源: packages/llm/llm/src/retry-policy.ts
// 源: packages/llm/llm/src/assembler.ts
// 源: packages/llm/llm/src/adapter-failure.ts
// 源: packages/llm/llm/src/api-key.ts
// 源: packages/llm/llm/src/attribution.ts
// 源: packages/llm/llm/src/index.ts
//
// # 两半，以及为什么它们分得开
//
// DSH 的 dsh-llm 包里装着两样东西：一套**值**（[Message]、[ContentBlock]、
// [StreamChunk]、[CallConfig]），和一个把请求路由到提供方适配器上的**服务**
// （[Runtime]：注册表、模型目录、模型发现、重试策略、流的装配）。
//
// 两半在同一个包里，但依赖是单向的：值这一半一个字都不认识 [Runtime]。
// 会话日志要把每一条消息、每一个流式分块原样落盘，所以它只用得着值这一半；
// 而路由和适配器一个字节都不写进日志。这条单向性靠 [Adapter] 那道接缝守着——
// 本包不带任何一个具体提供方的实现，一个空的 [Runtime] 一条路由都没有。
//
// # 这里没有照抄的部分
//
// 下面每一条都是 DSH 用 TypeScript 的机制解决的问题，而 Go 有它自己的答案。
//
//   - Branded<'MessageId'> 这类品牌类型 → 具名 string 类型。
//     Go 的具名类型天生是标称类型：[MessageID] 和 [CallID] 编译期不可互换、
//     运行期零成本，正是那个 TS 技巧在模拟的东西。恒等构造函数也一并不要，
//     语言自带的类型转换就是它。
//
//   - HarnessError 基类 → 不设。理由和本仓库 fs、storage、attachment 三个包
//     给出的是同一个：Go 的错误分派靠 errors.As 认**具体类型**，不靠原型链认基类。
//     本包里跟错误有关的只有 [Failure]——它不是一个 Go error，它是一份
//     **可序列化的事实**，会跟着 [FinishReason] 一起写进会话日志。
//
//   - deepFreeze 与 structuredClone → Go 的值语义加 Clone。
//     DSH 冻结是因为 JS 的对象按引用共享，一个发布出去的消息可以被收到它的人改掉。
//     Go 的结构体赋值就是复制，但里面的切片不是——所以构造函数复制传进来的内容，
//     [Message.Clone] 复制整棵内容树。能力一样：拿到一条消息的人改不动别人手里那份。
//
//   - 声明合并（declaration merging）→ 封闭接口加一个 Unknown 变体。
//     DSH 的 ContentBlockMap / MessageSourceMap / FinishReasonMap 都是可被插件
//     合并扩展的映射接口，配的话术是「switch 上认识的，不认识的落到 default」。
//     Go 没有声明合并，本包用「接口 + 未导出的封印方法」把变体封在包内，
//     再对那三个可扩展的联合各留一个 Unknown 变体：它**原样保管**没读懂的那段
//     JSON，于是一个旧版本读进一份新版本写的日志、再写回去，不会把它读不懂的
//     东西抹掉。[StreamChunk] 不留这个口子——它是适配器和运行时之间的
//     协议，DSH 那边也是封闭联合，不认识的分块只能是错误。
//
//   - markAgentLoopRequest / isAgentLoopRequest（用 WeakSet 记住请求**对象的身份**）
//     → 不照抄。DSH 用一张 WeakSet 记住某个 GenerateOptions **对象**是循环发的，
//     靠的是「同一个对象」这个可被外部观察的身份；Go 里 [GenerateOptions] 是值，
//     传一次就复制一次，那个身份根本不存在。这件事在 Go 侧是请求上的一个字段，
//     由拥有那条循环的包自己带，不是一张旁路的表。
//
//   - LlmAdapter 抽象基类 → 一个最小接口加五个可选接口。
//     内嵌不是继承：Go 的方法集没有虚派发，一个可内嵌的基类里的默认实现永远
//     调不到外层覆盖的那一份。派发交给运行时之后覆盖才落得到实处，
//     详见 [Adapter] 那个文件开头。
package llm
