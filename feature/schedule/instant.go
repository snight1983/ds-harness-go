// 本文件的作用：那一种落进日志的时刻写法——怎么排出去、怎么严格读回来、
// 四位年份那个窗口是什么，以及一组精确日历字段怎么在拒绝规格化的前提下变成一个瞬间。
//
// 源: packages/schedule/schedule/src/domain.ts:26-28, 133-207

package schedule

import (
	"regexp"
	"strings"
	"time"
)

// instantLayout 是本包**唯一**认得的那一种时刻写法：四位年份、毫秒三位、UTC。
//
// 源: packages/schedule/schedule/src/domain.ts:28
//
// 末尾的 Z 是一个字面量：Go 的布局里只有 `Z07:00` 那种写法才被当成时区标记，
// 单独一个 Z 就是它自己。这正好是本包要的——时区永远是 UTC，不允许写偏移量。
const instantLayout = "2006-01-02T15:04:05.000Z"

// utcInstantPattern 是那种写法的形状检查。
//
// 源: packages/schedule/schedule/src/domain.ts:28
//
// 新增: DSH 用 `(?!0000)` 这个否定先行断言挡住零年份。Go 的 regexp 是 RE2，
// 没有先行断言，所以那一条挪进 [ParseInstant] 里做一次显式的前缀判断。
var utcInstantPattern = regexp.MustCompile(
	`^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d\.\d{3}Z$`)

// minFourDigitYearMillis、maxFourDigitYearMillis 是那个窗口的两端。
//
// 源: packages/schedule/schedule/src/domain.ts:26-27
//
// 新增: DSH 拿 `Date.parse` 从字符串算出来，这里直接用 [time.Date] 造。两条路
// 得到的是同一个数，但这一条不依赖解析器：常量的意思是「四位年份写得下的第一个和
// 最后一个毫秒」，用日历字段表达比用一个待解析的字符串表达更直白。
var (
	minFourDigitYearMillis = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	maxFourDigitYearMillis = time.Date(9999, 12, 31, 23, 59, 59, int(999*time.Millisecond), time.UTC).UnixMilli()
)

// FormatInstant 把一个瞬间排成本包认得的那一种写法。
//
// 源: packages/schedule/schedule/src/domain.ts:198 (`new Date(epoch).toISOString()`)
//
// 只有落在四位年份窗口里的时刻排出来才是合法的：窗口外的年份会多排或少排数字。
// 本包所有的排出口都在 [futureInstant] 之后，那一步已经把窗口验过了。
func FormatInstant(instant time.Time) string {
	return instant.UTC().Format(instantLayout)
}

// ParseInstant 严格读回一个本包写法的时刻。
//
// 源: packages/schedule/schedule/src/domain.ts:133-144
//
// 「严格」的意思是：形状不对、年份是 0000、或者不是一个真实存在的日历日，
// 三种都当场拒。交回的错是 [LogError]——本包只在耐久边界上读时刻，读不动就是
// 日志坏了。
func ParseInstant(value string) (time.Time, error) {
	if !utcInstantPattern.MatchString(value) || strings.HasPrefix(value, "0000") {
		return time.Time{}, &LogError{Reason: "scheduledAt 必须是四位年份、毫秒三位的 RFC 3339 UTC 时刻"}
	}
	// 新增: DSH 解完之后再排一遍比对，为的是挡住 2 月 30 日这种日历上不存在的日期。
	// Go 的 [time.Parse] 自己就查月内天数（"day out of range"），所以那一次回排在这里
	// 是一段永远走不到的分支，去掉。
	instant, err := time.Parse(instantLayout, value)
	if err != nil {
		return time.Time{}, &LogError{Reason: "scheduledAt 不是一个真实存在的 UTC 日历时刻"}
	}
	return instant, nil
}

// calendarFields 是一组**精确的**日历字段，还没有被安到任何时区上。
//
// 源: packages/schedule/schedule/src/domain.ts:146-154
type calendarFields struct {
	year        int
	month       int
	day         int
	hour        int
	minute      int
	second      int
	millisecond int
}

// fieldsOf 把一个瞬间在某个时区上的样子摊成日历字段。
//
// 源: packages/schedule/schedule/src/domain.ts:308-329 (localProjection 的字段部分)
func fieldsOf(instant time.Time) calendarFields {
	return calendarFields{
		year:        instant.Year(),
		month:       int(instant.Month()),
		day:         instant.Day(),
		hour:        instant.Hour(),
		minute:      instant.Minute(),
		second:      instant.Second(),
		millisecond: instant.Nanosecond() / int(time.Millisecond),
	}
}

// utcMillis 把这组字段当成 UTC 上的字段算出毫秒数，并且**拒绝规格化**。
//
// 源: packages/schedule/schedule/src/domain.ts:156-176 (calendarEpoch)
//
// 拒绝规格化是这个函数存在的理由：2 月 30 日在 [time.Date] 那里会被悄悄挪成
// 3 月 2 日，于是一个写错的日期会变成一个合法的提醒，在错的那一天响。所以算完
// 再把字段读回来逐个比一遍，动过就是不合法。
//
// 那句话是给模型看的，所以是英文；用词照抄 DSH——本地日历那条路上出的这个错
// 也用这一句，因为它是同一个检查。
func (f calendarFields) utcMillis() (int64, error) {
	instant := time.Date(f.year, time.Month(f.month), f.day, f.hour, f.minute, f.second,
		f.millisecond*int(time.Millisecond), time.UTC)
	if fieldsOf(instant) != f {
		return 0, newInputError(CodeInvalidRule, "The at value must be a real ISO calendar date and time.")
	}
	return instant.UnixMilli(), nil
}

// futureInstant 要求这个目标既写得下、又严格在未来，然后把它排成日志里的写法。
//
// 源: packages/schedule/schedule/src/domain.ts:184-207
//
// 新增: DSH 那两个 `Number.isSafeInteger` 检查在这里没有对应物——Go 这边两个值
// 都是 int64，不存在「整数已经不精确了」这回事，窗口检查本身就够。
// 排出去之后那一次形状回验也去掉了：窗口里的任何一个毫秒排出来必然是规范写法，
// 那是一段验不了的分支。
func futureInstant(target int64, now int64) (string, error) {
	if target < minFourDigitYearMillis || target > maxFourDigitYearMillis {
		return "", newInputError(CodeTimeOutOfRange,
			"The scheduled time must be representable as a four-digit-year RFC 3339 UTC instant.")
	}
	if target <= now {
		return "", newInputError(CodeNotFuture, "The scheduled time must be strictly in the future.")
	}
	return FormatInstant(time.UnixMilli(target)), nil
}
