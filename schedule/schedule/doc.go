// Package schedule 是挂在会话日志上的耐久提醒：一次性的（`after` / `at`）和
// 固定频率的（`every`）。
//
// 源: packages/schedule/schedule/src/index.ts
//
// # 这个包在守什么
//
// 提醒的**唯一事实**是会话日志里那一串 `schedule/change` 事件，不是内存里的定时器。
// 定时器只是那串事件的一个进程局部投影：它随时可以被丢掉重算，丢掉不损失任何东西。
// 这条分工决定了本包几乎所有的形状——
//
//   - 每一次改动都先落一条事件再说，落不下去就当没发生（见 [Controller] 里那三件工具）。
//   - 每一次决定之前都重新折一遍日志，绝不信上一次折出来的结果（见 [runtime.driveOnce]）。
//   - 投递**只在原会话活着的时候**发生（[DeliverySessionLocal]）：一个已经过期
//     但会话没开着的提醒，会一直是 [StateOverdue]，等会话恢复了再响。
//
// # 一次性和固定频率为什么不一样
//
// 一次性提醒响过就没了，所以它的 dispatch 事件只带一个 id。固定频率的那条要
// **跳过错过的那些**：一台睡了三天的机器醒来时，一条每小时一次的提醒不该连着
// 补七十二条。所以它的 dispatch 带一个 acceptedAt——「在这个时刻做的决定」——
// 回放时按它算出「那时候最新的那一次是哪一次」，中间错过的直接跨过去
// （见 [ResolveEveryOccurrence]）。
//
// # 时间为什么这么小心
//
// 落到日志里的时刻只有一种写法：四位年份、毫秒三位、UTC 的 RFC 3339
// （见 [FormatInstant]）。模型那一侧可以写带偏移量的字符串，也可以写
// 「某个 IANA 时区的某年某月某日某时某分」，但两种写法都在**创建的那一刻**
// 折成上面那一种，日志里再也看不到时区。
//
// 本地时刻那条路上有两个真实存在的坑，[resolveLocalInstant] 逐个挡住了：
// 夏令时回拨那一小时里同一个墙上时刻对应两个瞬间（取**早**的那个），
// 夏令时前拨那一小时里的墙上时刻一个瞬间都不对应（当场拒绝，不四舍五入）。
//
// # 装配
//
// 这个包的事件类型要加进会话词汇表，否则读日志的一方会认为它读到了一条
// 不认识的必需事件：
//
//	vocabulary := session.CoreVocabulary().With(schedule.EventTypes()...)
//
// # 覆盖率为什么停在 97.5%
//
// 本包不是纯逻辑包：它握着会话日志、工具运行时和一条驱动协程。剩下没被覆盖的那
// 十几个块全是**按构造走不到**的错误分支，为它们造用例只能靠往包内部塞替身，那样
// 验的是替身而不是这个包：
//
//   - 折日志之后再解析时刻失败（[runtime.decide]、dispatchedRecord 里那两支
//     ParseInstant）。能走到那里的 scheduledAt 都已经过了 FoldEvents 那一关，
//     而那一关只收 [ParseInstant] 认得的字节。
//   - 把刚刚造出来的值排成字节失败（appendChange、appendDispatch 里的
//     MarshalJSON，以及三件工具里的 encodeValue / NewView）。上游给的永远是本包
//     自己拼的纯字符串结构。
//   - 往日志里追加失败。[github.com/snight1983/ds-harness-go/core/session.Session.Append] 只会在序号或
//     时间戳非零、数据不是合法 JSON、或者违反表层计划时报错；一条平铺的
//     `schedule/change` 事件这四条一条都碰不到。
package schedule
