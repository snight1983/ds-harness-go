// 本文件的作用：读数里那个时间戳长什么样——生产和不变量复核共用同一份写法。
//
// 源: packages/context/time-context/src/timestamp.ts

package timecontext

import "time"

// timestampLayout 是读数里时间戳的排法。
//
// 源: packages/context/time-context/src/timestamp.ts:11-21
//
// 逐项对上 DSH 那份 Intl 选项：`year:'numeric'` + 三个 `'2-digit'` 是
// `2006-01-02`，`hourCycle:'h23'` 是 `15`，`timeZoneName:'longOffset'` 切掉
// 前缀 `GMT` 之后剩下的就是 `-07:00`。
//
// 新增: 偏移量用 `-07:00` 而不是 Go 更常见的 `Z07:00`。后者在 UTC 上排出来
// 是单个字母 `Z`，而 DSH 那边 `GMT` 被显式补成 `GMT+00:00`、切出来是 `+00:00`。
// 一个字母的差别会让同一时刻在两边渲染成不同的字节，而不变量正是靠重排一遍
// 再逐字比对来认这条读数的。
const timestampLayout = "2006-01-02T15:04:05-07:00"

// FormatTimestamp 把一个时刻排成读数里那个带偏移量和 IANA 时区名的时间戳。
//
// 源: packages/context/time-context/src/timestamp.ts:31-37
//
// 方括号里的名字取 location 自己的 String()，而不是再收一个字符串参数。
// DSH 必须把两者分开传，因为它的兜底 formatter 是拿 `undefined` 造的、
// 自己不知道最后落到哪个时区；Go 这边 [Config.Resolve] 交出来的
// [time.Location] 一定是由一个 IANA 名字加载出来的，`String()` 就是那个名字。
// 少一个参数就少一处「偏移量按 A 时区算、括号里却写着 B」的机会。
//
// 传 [time.Local] 会排出 `[Local]`——那不是时区名。[Config.Resolve] 不会交出
// 它，理由写在 [DefaultTimeZone] 上。
func FormatTimestamp(now time.Time, location *time.Location) string {
	return now.In(location).Format(timestampLayout) + "[" + location.String() + "]"
}
