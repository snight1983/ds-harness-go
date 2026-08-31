// Package stats 是「整份日志的会话数字」这个投影单元。
//
// 对应 DSH 的 @deepseek-ai/dsh-session-stats（packages/session/session-stats）。
//
// 源: packages/session/session-stats/src/index.ts:1-10
//
// # 这一层在算什么
//
// 一个客户端想显示「这次会话跑了几个回合、几个步骤、模型总共花了多久」时，
// 它手上通常只有翻进来的那一段历史。翻页翻得多少、压缩掉了多少，都会改变
// 那段历史的长度，于是同一个会话在两个客户端上显示出两组不同的数字。
//
// 这个单元把那些数字从**整份日志**折出来，交给 session/projection 那条缝送出去。
// 翻页和压缩都改不了它：它数的是日志里的事件，不是表面上的节点。
//
// # 为什么数 step/end 而不数 assistant/message
//
// step/end 是步骤生命周期的权威：循环每进一个步骤就在 finally 里追一条，所以
// 跑完的、失败的、被取消的、撞上 token 上限的，一律各留一条。改数装配好的助手
// 消息会两头出错——撞上 token 上限那条只承载用量、内容是空的、根本不上表面
// （会多数），而被取消的步骤在消息装配出来之前就断了（会少数）。
//
// # 每个字段的折法
//
// 各字段的具体折法写在 [Figures] 每个字段上。共同的规则只有一条：
// **每个字段在它第一条有贡献的事件落地之前都是零。** 一个组装好的登记表永远
// 端得出这个键，所以客户端读的是值，不是「键在不在」。
//
// # 这里没有照抄的部分
//
// index.ts 那个 cordis 插件（name / inject / apply 三样）在 Go 里就是
// [Definition] 交出来的那份值加一次 [projection.Register]，由装配方自己调。
//
// types.ts 与 client.ts 是 TypeScript 的声明合并：前者往
// SessionProjectionMap 上挂一个 `sessionStats` 键，后者原样再导出一遍给客户端
// 命名空间。Go 没有声明合并，投影键就是 [Key] 那个字符串常量，视图类型就是
// [Figures]，一处定义，宿主和客户端读同一个。
//
// projection.ts 里那两份 zod schema 是同一个东西的两半：viewSchema 管出去的
// 字节，stateSchema 管从盘上回来的字节。Go 这边出去的字节由 [Figures] 自己的
// 类型保证，回来的字节由 [Definition] 的 DecodeState 过一遍 [State.Validate]。
//
// invariant.ts 是一个空的安装器（它自己写明了为什么没有运行期不变量），
// 没有可移植的东西。
//
// # 这里和 DSH 不一样的地方
//
//  1. **墙上时间是 int64 毫秒，不是 float64。** DSH 的 event.time 是 number，
//     所有累加都在浮点上做，于是 zod 那边要写 z.number().nonnegative() 而不是
//     整数约束。Go 的 [session.Event.Time] 是 int64，累加逐位精确，
//     「毫秒数会不会飘」这个问题不存在。
//
//  2. **工具结果那道「自有键检查」消失了。** DSH 在 pendingCalls 上要写
//     Object.hasOwn，因为 callId 是模型/工具那一侧铸出来的字符串，一个叫
//     "constructor" 的 callId 会从原型链上读到一个函数，把 toolMs 污染成 NaN。
//     Go 的 map 没有原型链，`value, ok := m[key]` 本来就只看自有键。
//
//  3. **「这次转移改了什么没有」是显式返回的。** DSH 靠「返回同一个引用」加
//     Object.is 表达没变，所以那份 apply 里每一条不相干的事件都要写
//     `return state`。Go 这边 [projection.Definition.Apply] 的第二个返回值就是
//     那件事，理由见 session/projection 的包文档。
package stats
