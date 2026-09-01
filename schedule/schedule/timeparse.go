// 本文件的作用：模型那一侧写绝对时刻的两种写法——带偏移量的 RFC 3339 串，和
// 「某个 IANA 时区的某年某月某日某时某分」——怎么各自折成一个瞬间，以及夏令时
// 那两个坑是怎么挡住的。
//
// 源: packages/schedule/schedule/src/domain.ts:209-382

package schedule

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 这四条是模型那一侧那几种写法的形状。
//
// 源: packages/schedule/schedule/src/domain.ts:29-37
//
// 时分秒都写成不受约束的 \d{2}：范围检查故意留到匹配之后做，因为「25:00」该报的是
// 「不是一个真实的日历时刻」，不是「形状不对」——两句话指的是模型该改的两件事。
var (
	offsetInstantPattern = regexp.MustCompile(
		`^(?P<year>\d{4})-(?P<month>\d{2})-(?P<day>\d{2})` +
			`T(?P<hour>\d{2}):(?P<minute>\d{2}):(?P<second>\d{2})` +
			`(?:\.(?P<fraction>\d{1,3}))?(?P<zone>Z|(?P<sign>[+-])` +
			`(?P<offsetHour>\d{2}):(?P<offsetMinute>\d{2}))$`)
	localDatePattern = regexp.MustCompile(`^(?P<year>\d{4})-(?P<month>\d{2})-(?P<day>\d{2})$`)
	localTimePattern = regexp.MustCompile(
		`^(?P<hour>\d{2}):(?P<minute>\d{2}):(?P<second>\d{2})(?:\.(?P<fraction>\d{1,3}))?$`)
	ianaZonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+.-]*(?:/[A-Za-z0-9_+.-]+)+$`)
)

// group 读一个具名组；这一组没匹配上时是空串。
func group(pattern *regexp.Regexp, match []string, name string) string {
	index := pattern.SubexpIndex(name)
	if index < 0 || index >= len(match) {
		return ""
	}
	return match[index]
}

// groupNumber 读一个由 \d{n} 保证过的数字组。
//
// 源: packages/schedule/schedule/src/domain.ts:156-161
//
// 新增: DSH 那边要挡「这一组没出现」，因为 TS 的具名组类型是可选的。Go 这边
// 匹配上就一定只有数字、而且位数固定在四位以内，所以 [strconv.Atoi] 的错吞掉是
// 安全的，也没法在测试里走到——留一个错误分支反而是一段验不了的代码。
func groupNumber(pattern *regexp.Regexp, match []string, name string) int {
	value, _ := strconv.Atoi(group(pattern, match, name))
	return value
}

// fractionMillis 把那个一到三位的小数秒补齐成毫秒。
//
// 源: packages/schedule/schedule/src/domain.ts:178-180
func fractionMillis(fraction string) int {
	if fraction == "" {
		return 0
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	value, _ := strconv.Atoi(fraction)
	return value
}

// realCalendarTime 是那几个正则管不了的范围检查。
//
// 源: packages/schedule/schedule/src/domain.ts:238-240, 300-302
func (f calendarFields) realCalendarTime() bool {
	return f.year != 0 && f.hour <= 23 && f.minute <= 59 && f.second <= 59
}

// parseOffsetInstant 解一个把偏移量写在自己身上的 RFC 3339 时刻，交回毫秒数。
//
// 源: packages/schedule/schedule/src/domain.ts:209-249
//
// 「把偏移量写在自己身上」是这条路的全部要求：不接受本地时刻，也不接受省略时区。
// -00:00 被拒是有意的——RFC 3339 里它的意思是「偏移量未知」，那正好是本包不收的
// 那一种。那几句话是给模型看的，所以是英文。
func parseOffsetInstant(value string) (int64, error) {
	match := offsetInstantPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, newInputError(CodeInvalidRule,
			"at must use YYYY-MM-DDTHH:mm:ss with optional 1-3 digit fractional seconds "+
				"and an explicit Z or numeric offset.")
	}
	fields := calendarFields{
		year:        groupNumber(offsetInstantPattern, match, "year"),
		month:       groupNumber(offsetInstantPattern, match, "month"),
		day:         groupNumber(offsetInstantPattern, match, "day"),
		hour:        groupNumber(offsetInstantPattern, match, "hour"),
		minute:      groupNumber(offsetInstantPattern, match, "minute"),
		second:      groupNumber(offsetInstantPattern, match, "second"),
		millisecond: fractionMillis(group(offsetInstantPattern, match, "fraction")),
	}
	if !fields.realCalendarTime() {
		return 0, newInputError(CodeInvalidRule, "The at value must be a real ISO calendar date and time.")
	}
	localMillis, err := fields.utcMillis()
	if err != nil {
		return 0, err
	}
	if group(offsetInstantPattern, match, "zone") == "Z" {
		return localMillis, nil
	}
	sign := group(offsetInstantPattern, match, "sign")
	offsetHour := groupNumber(offsetInstantPattern, match, "offsetHour")
	offsetMinute := groupNumber(offsetInstantPattern, match, "offsetMinute")
	if offsetHour > 23 || offsetMinute > 59 || (sign == "-" && offsetHour == 0 && offsetMinute == 0) {
		return 0, newInputError(CodeInvalidRule, "The at numeric offset is invalid.")
	}
	direction := int64(1)
	if sign == "-" {
		direction = -1
	}
	return localMillis - direction*int64(offsetHour*60+offsetMinute)*60_000, nil
}

// CanonicalizeTimeZone 验一个 IANA 时区名，交回这台机器上加载出来的那个时区。
//
// 源: packages/schedule/schedule/src/domain.ts:246-270（canonicalizeTimeZone）
//
// 新增: DSH 交回的是**规范化之后的名字**（`Intl` 会把 Asia/Calcutta 这类别名折成
// 主名）。Go 的 [time.LoadLocation] 只验、不折别名，所以这里交回的是加载好的
// [time.Location] 而不是一个名字。这一处差别不影响任何耐久数据：时区名从来不落盘，
// 落盘的只有折算出来的那个 UTC 时刻，两个别名折出来的是同一个瞬间。
//
// "Local" 这类非 IANA 的特殊名被那条正则挡在外面（它要求至少一个斜杠），所以本包
// 绝不会掉进这台机器的本地时区——那会让同一条规则在不同机器上算出不同的时刻。
//
// 本包**不** import time/tzdata，理由和
// [github.com/snight1983/ds-harness-go/context/timecontext] 那一条一样：那四百多 KB 该由做最终二进制的
// 人决定要不要带。部署里没有 zoneinfo 时，这里报的是 [CodeInvalidTimeZone]，
// 解法是在 main 包里 import _ "time/tzdata"。
func CanonicalizeTimeZone(value string) (*time.Location, error) {
	invalid := func(cause error) error {
		return wrapInputError(CodeInvalidTimeZone,
			"time_zone must be UTC or a valid IANA Area/Location name.", cause)
	}
	if value == "" || value != strings.TrimSpace(value) ||
		(value != "UTC" && !ianaZonePattern.MatchString(value)) {
		return nil, invalid(nil)
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, invalid(err)
	}
	return location, nil
}

// parseLocalAt 解那一组严格的本地日历字段，**一次都不碰**这台机器的时区。
//
// 源: packages/schedule/schedule/src/domain.ts:277-305
func parseLocalAt(date string, clock string) (calendarFields, error) {
	dateMatch := localDatePattern.FindStringSubmatch(date)
	timeMatch := localTimePattern.FindStringSubmatch(clock)
	if dateMatch == nil || timeMatch == nil {
		return calendarFields{}, newInputError(CodeInvalidRule,
			"Local at requires date YYYY-MM-DD and time HH:mm:ss with optional "+
				"one-to-three digit milliseconds.")
	}
	fields := calendarFields{
		year:        groupNumber(localDatePattern, dateMatch, "year"),
		month:       groupNumber(localDatePattern, dateMatch, "month"),
		day:         groupNumber(localDatePattern, dateMatch, "day"),
		hour:        groupNumber(localTimePattern, timeMatch, "hour"),
		minute:      groupNumber(localTimePattern, timeMatch, "minute"),
		second:      groupNumber(localTimePattern, timeMatch, "second"),
		millisecond: fractionMillis(group(localTimePattern, timeMatch, "fraction")),
	}
	if !fields.realCalendarTime() {
		return calendarFields{}, newInputError(CodeInvalidRule,
			"The local at value must be a real ISO calendar date and time.")
	}
	// 这一次只为了把 2 月 30 日那种日子挡住，算出来的数不要。
	if _, err := fields.utcMillis(); err != nil {
		return calendarFields{}, err
	}
	return fields, nil
}

// dstSampleDeltas 是采样偏移量的那五个点，单位毫秒。
//
// 源: packages/schedule/schedule/src/domain.ts:347
//
// 前后各两天足够罩住任何一次夏令时切换：目标那一刻可能落在切换的哪一侧还不知道，
// 所以两侧的偏移量都要拿到手。
var dstSampleDeltas = []int64{-172_800_000, -86_400_000, 0, 86_400_000, 172_800_000}

// resolveLocalInstant 把一组本地墙上时刻字段折成一个瞬间：夏令时回拨那一小时里
// 取**早**的那个，前拨那一小时里当场拒绝。
//
// 源: packages/schedule/schedule/src/domain.ts:332-381
//
// 新增: DSH 拿 Intl 的 longOffset 采样偏移量，Go 这边是
// [time.Time.Zone]。算法本身**一模一样**，而且必须一模一样：Go 的 [time.Date] 明文
// 写了它不保证在切换的哪一侧落脚，所以不能直接把字段交给它了事——那样回拨那一小时
// 里取到哪一个瞬间就成了实现细节，而这条规则是给模型的承诺。
//
// 判断一个候选算不算数的办法是**投影回去逐字段比**：把候选瞬间放回那个时区，
// 看它显示出来的年月日时分秒毫秒是不是原样。前拨跳掉的那一小时里没有任何瞬间
// 能显示成那个墙上时刻，于是一个候选都留不下来，那就是「这个时刻不存在」。
func resolveLocalInstant(fields calendarFields, location *time.Location) (int64, error) {
	localMillis, err := fields.utcMillis()
	if err != nil {
		return 0, err
	}
	offsets := make([]int64, 0, len(dstSampleDeltas))
	for _, delta := range dstSampleDeltas {
		sample := min(maxFourDigitYearMillis, max(minFourDigitYearMillis, localMillis+delta))
		_, seconds := time.UnixMilli(sample).In(location).Zone()
		offset := int64(seconds) * 1000
		if !containsInt64(offsets, offset) {
			offsets = append(offsets, offset)
		}
	}
	candidates := make([]int64, 0, len(offsets))
	outOfRange := false
	for _, offset := range offsets {
		candidate := localMillis - offset
		if candidate < minFourDigitYearMillis || candidate > maxFourDigitYearMillis {
			outOfRange = true
			continue
		}
		if fieldsOf(time.UnixMilli(candidate).In(location)) == fields {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		if outOfRange {
			return 0, newInputError(CodeTimeOutOfRange,
				"The scheduled time must be representable as a four-digit-year RFC 3339 UTC instant.")
		}
		return 0, newInputError(CodeInvalidRule,
			"The local at time does not exist in the selected time zone.")
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left] < candidates[right] })
	return candidates[0], nil
}

// containsInt64 是那个「偏移量去重」用的成员判断。
//
// 新增: DSH 用 Set。这里最多五个元素，线性扫比建一张表便宜，也不用为顺序另操心
// ——后面那一步要的是最小值，跟遍历顺序无关。
func containsInt64(values []int64, wanted int64) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// resolveAtInput 把模型给的那个 at 折成一个瞬间的毫秒数。
//
// 源: packages/schedule/schedule/src/domain.ts:690-712
//
// 新增: DSH 那边 at 的静态类型是 `string | LocalAtInput`，运行时还要再判一次形状。
// Go 这边它就是一段还没解的 JSON：判别落在解出来是什么类型上，和 DSH 那两个
// 运行时判断一一对应。
func resolveAtInput(at json.RawMessage) (int64, error) {
	shapeError := newInputError(CodeInvalidRule,
		"at must be an explicit-offset string or local calendar object.")
	var decoded any
	if err := json.Unmarshal(at, &decoded); err != nil {
		return 0, shapeError
	}
	switch value := decoded.(type) {
	case string:
		return parseOffsetInstant(value)
	case map[string]any:
		return resolveLocalAtObject(value)
	default:
		return 0, shapeError
	}
}

// resolveLocalAtObject 走本地日历那一支：键必须**恰好**是那三个，三个都得是字符串。
//
// 源: packages/schedule/schedule/src/domain.ts:693-709
func resolveLocalAtObject(value map[string]any) (int64, error) {
	if len(value) != 3 {
		return 0, newInputError(CodeInvalidRule, "Local at must contain exactly date, time, and time_zone.")
	}
	rawDate, hasDate := value["date"]
	rawTime, hasTime := value["time"]
	rawZone, hasZone := value["time_zone"]
	if !hasDate || !hasTime || !hasZone {
		return 0, newInputError(CodeInvalidRule, "Local at must contain exactly date, time, and time_zone.")
	}
	date, dateIsString := rawDate.(string)
	clock, timeIsString := rawTime.(string)
	if !dateIsString || !timeIsString {
		return 0, newInputError(CodeInvalidRule, "Local at date and time must be strings.")
	}
	zone, zoneIsString := rawZone.(string)
	if !zoneIsString {
		return 0, newInputError(CodeInvalidTimeZone, "time_zone must be a string.")
	}
	fields, err := parseLocalAt(date, clock)
	if err != nil {
		return 0, err
	}
	location, err := CanonicalizeTimeZone(zone)
	if err != nil {
		return 0, err
	}
	return resolveLocalInstant(fields, location)
}
