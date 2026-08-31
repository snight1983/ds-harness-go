// Package sessiontitle 给一个会话起名，并且把这个名字**当成日志上的一条事件**来管：
// 谁起的、从哪几句话起的、什么时候起的，全都写进去，一条都不放在旁边的可变元数据里。
//
// 对应 DSH 的 @deepseek-ai/dsh-session-title（packages/session/session-title）。
//
// 源: packages/session/session-title/src/index.ts:1-4
//
// # 为什么标题要进日志
//
// 标题看起来像是一个可以随手存在会话头上的字符串。它不是：它有三种来路（确定性
// 兜底、模型生成、用户改名），其中两种会互相盖，而「这个名字是谁起的、从哪句话来的」
// 这个问题在换了模型、改了提示词之后必须还答得上来。一个可变字段答不了它，一条
// 事件答得了。
//
// 于是本包的立场是：**日志是唯一的真相**。[Service] 不缓存任何标题，[Service.Get]
// 每次都从日志现折。服务自己那份可变状态只有并发账（版本号、排着的活、跑着的活），
// 那些东西一条都不进日志，进程重启之后从零开始，而标题本身一个字都不会丢。
// [FoldSnapshot] 和 [CollectMessages] 因此不需要服务也不需要活会话——一份回放出来的
// 日志照样答得上话。
//
// # 三种来路，一条钉住规则
//
//   - [SourceFallback]：截第一条人类消息的前几个词。确定性、不花钱、不上模型，
//     所以它在每一条够格的用户消息上都会先落地，好让列表行立刻有个名字。
//   - [SourceProvider]：登记进来的那个唯一 [Provider] 产出的。它是异步的、会被取代的。
//   - [SourceUser]：用户自己改的。它**钉住**这个标题——在途的自动生成当场作废，
//     后面再来的用户消息也不再排期。解开只有一条路：一次显式的 [Service.Refresh]。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 的构造函数顺手做了两件事——把标题单元登进投影服务、订阅三个事件。
// Go 没有那个容器，所以两件事都成了显式入口：投影是 [RegisterProjection]，
// 三条订阅是 [Service.OnEvent]、[Service.OnMainRequest]、[Service.OnSessionDisposed]。
// 装配方接哪几条自己定，一份只做离线回放的装配一条都不接，[Service.Get] 照样管用。
//
// 新增: DSH 拿 `ctx.sessions.get(id) !== session` 判会话活性——按引用比。Go 的接口值
// 在动态类型不可比较时用 == 会当场 panic，所以这件事翻成 [Config.IsLive] 交给装配方，
// 它手上有具体类型。
//
// 新增: DSH 那套 `AbortSignal.any([...])` 合并信号在 Go 里没有对应物（context 只有
// 一个父）。这里是「挂在服务寿命下面 + [context.AfterFunc] 把调用方那条转发进来」，
// 见 [Service.Refresh]。
//
// 新增: 那个兜底标题的推导和追加在 Go 这边是同一把锁里一口气做完的，不像 DSH 隔着
// 一个 microtask。这删掉了 DSH 的在途 promise 去重、以及三次进去之后的重查。代价
// 是一条约定：[Session.Append] 会在服务持有自己那把锁的时候被调用，实现方不许
// 反过来同步地调回本包的任何一个钩子。
package sessiontitle
