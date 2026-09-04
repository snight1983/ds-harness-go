// 本文件的作用：把那一层时刻折算钉在它真会出错的边上——落盘写法的严格读写、
// 带偏移量的串怎么折、本地日历那一支在夏令时两个坑上的落点，以及四位年份窗口的两端。
//
// # 这些测试防的是什么错
//
//   - **一个写错的日期被悄悄挪成合法的**。2 月 30 日在 [time.Date] 那里会变成
//     3 月 2 日，于是模型定在一个不存在的日子上的提醒会在别的一天响。挡它的是
//     [calendarFields.utcMillis] 那一次逐字段回比，一旦被人「顺手简化」掉就再也
//     没人发现。
//   - **夏令时回拨那一小时取错了那一个**。那一小时里有两个瞬间显示成同一个墙上
//     时刻，取晚的那个会让提醒晚响一小时。这是给模型的承诺，不是实现细节。
//   - **夏令时前拨那一小时被当成合法**。那一小时里一个瞬间都没有，硬折出一个数
//     等于替模型选了一个它没说过的时刻。
//   - **掉进这台机器的本地时区**。"Local"、"EST" 这类非 IANA 名字如果放进来，
//     同一条规则会在不同机器上算出不同的时刻。
//   - **-00:00 被当成 UTC**。RFC 3339 里它的意思是「偏移量未知」，正是本包不收的
//     那一种。
//   - **四位年份窗口漏判**。窗口外的时刻排出来会多排或少排数字，那份字节此后
//     谁都读不回来。

package schedule

import (
	"encoding/json"
	"testing"
	"time"

	// 本包自己不带时区库（理由见 CanonicalizeTimeZone 上的注释），但这几个用例
	// 必须拿到真的 zoneinfo 才验得动夏令时那两条规则。
	_ "time/tzdata"
)

// mustParseInstant 是那些「这个串一定合法」的地方用的短路。
func mustParseInstant(t *testing.T, value string) time.Time {
	t.Helper()
	instant, err := ParseInstant(value)
	if err != nil {
		t.Fatalf("ParseInstant(%q) 本该成功，却报了 %v", value, err)
	}
	return instant
}

func TestFormatInstantRoundTrips(t *testing.T) {
	instant := time.Date(2026, 8, 30, 12, 34, 56, int(789*time.Millisecond), time.UTC)
	formatted := FormatInstant(instant)
	if formatted != "2026-08-30T12:34:56.789Z" {
		t.Fatalf("排出来的是 %q", formatted)
	}
	if got := mustParseInstant(t, formatted); !got.Equal(instant) {
		t.Fatalf("读回来的是 %v，本该是 %v", got, instant)
	}
}

func TestFormatInstantKeepsTrailingZeroMillis(t *testing.T) {
	// 这一条守的是 Record.ScheduledAt 上那段注释：毫秒必须占满三位。省掉尾零
	// 排出来的就不再是本包认得的写法，下一次回放会把整条日志判成坏的。
	formatted := FormatInstant(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if formatted != "2026-01-01T00:00:00.000Z" {
		t.Fatalf("零毫秒排成了 %q", formatted)
	}
}

func TestFormatInstantNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("plus-two", 2*60*60)
	formatted := FormatInstant(time.Date(2026, 8, 30, 14, 0, 0, 0, zone))
	if formatted != "2026-08-30T12:00:00.000Z" {
		t.Fatalf("带偏移量的时刻排成了 %q", formatted)
	}
}

func TestParseInstantRejectsMalformed(t *testing.T) {
	for _, value := range []string{
		"",
		"2026-08-30T12:34:56Z",          // 少了毫秒
		"2026-08-30T12:34:56.7Z",        // 毫秒不足三位
		"2026-08-30T12:34:56.789+00:00", // 只认字面量 Z
		"2026-08-30T12:34:56.789",       // 没有时区
		"0000-01-01T00:00:00.000Z",      // 零年份
		"2026-13-01T00:00:00.000Z",      // 月份越界
		"2026-08-30T24:00:00.000Z",      // 小时越界
		" 2026-08-30T12:34:56.789Z",     // 首尾空白
	} {
		if _, err := ParseInstant(value); err == nil {
			t.Fatalf("ParseInstant(%q) 本该拒收", value)
		}
	}
}

func TestParseInstantRejectsImpossibleCalendarDay(t *testing.T) {
	// 形状完全合法，但 2 月 30 日不存在。这一条走的是 time.Parse 自己那道
	// day-out-of-range，而不是上面那条正则。
	_, err := ParseInstant("2026-02-30T00:00:00.000Z")
	if err == nil {
		t.Fatal("2 月 30 日本该被拒")
	}
	var logErr *LogError
	if !asError(err, &logErr) {
		t.Fatalf("交回的是 %T，本该是 *LogError", err)
	}
}

func TestParseInstantReportsLogError(t *testing.T) {
	// 时刻只在耐久边界上被读，所以读不动一律是「日志坏了」，不是「模型写错了」。
	var logErr *LogError
	if _, err := ParseInstant("nope"); !asError(err, &logErr) {
		t.Fatalf("形状错交回的不是 *LogError")
	}
}

func TestFutureInstantEnforcesWindowAndFuture(t *testing.T) {
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC).UnixMilli()

	if _, err := futureInstant(now, now); errorCode(t, err) != CodeNotFuture {
		t.Fatalf("与此刻相等本该报 not_future，报的是 %v", err)
	}
	if _, err := futureInstant(now-1, now); errorCode(t, err) != CodeNotFuture {
		t.Fatalf("过去的时刻本该报 not_future，报的是 %v", err)
	}
	if _, err := futureInstant(maxFourDigitYearMillis+1, now); errorCode(t, err) != CodeTimeOutOfRange {
		t.Fatalf("超出窗口上端本该报 time_out_of_range，报的是 %v", err)
	}
	if _, err := futureInstant(minFourDigitYearMillis-1, now); errorCode(t, err) != CodeTimeOutOfRange {
		t.Fatalf("超出窗口下端本该报 time_out_of_range，报的是 %v", err)
	}

	value, err := futureInstant(now+1000, now)
	if err != nil {
		t.Fatalf("窗口内的未来时刻本该成功：%v", err)
	}
	if value != "2026-08-30T00:00:01.000Z" {
		t.Fatalf("排出来的是 %q", value)
	}
}

func TestFutureInstantAcceptsWindowEdges(t *testing.T) {
	// 两端都是闭区间。少了这一条，把 <= 写成 < 的改动不会被任何用例发现。
	if _, err := futureInstant(maxFourDigitYearMillis, maxFourDigitYearMillis-1); err != nil {
		t.Fatalf("窗口上端本该收下：%v", err)
	}
	if _, err := futureInstant(minFourDigitYearMillis, minFourDigitYearMillis-1); err != nil {
		t.Fatalf("窗口下端本该收下：%v", err)
	}
}

func TestParseOffsetInstantAcceptsShapes(t *testing.T) {
	utc := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, each := range []struct {
		value string
		want  int64
	}{
		{"2026-08-30T12:00:00Z", utc},
		{"2026-08-30T12:00:00.5Z", utc + 500},
		{"2026-08-30T12:00:00.05Z", utc + 50},
		{"2026-08-30T12:00:00.005Z", utc + 5},
		{"2026-08-30T14:00:00+02:00", utc},
		{"2026-08-30T09:30:00-02:30", utc},
		{"2026-08-30T12:00:00+00:00", utc},
	} {
		got, err := parseOffsetInstant(each.value)
		if err != nil {
			t.Fatalf("parseOffsetInstant(%q) 报了 %v", each.value, err)
		}
		if got != each.want {
			t.Fatalf("parseOffsetInstant(%q) 得到 %d，本该是 %d", each.value, got, each.want)
		}
	}
}

func TestParseOffsetInstantRejects(t *testing.T) {
	for _, each := range []struct {
		value string
		code  ErrorCode
	}{
		{"2026-08-30", CodeInvalidRule},                // 只有日期
		{"2026-08-30T12:00:00", CodeInvalidRule},       // 没有时区
		{"2026-08-30T12:00:00.1234Z", CodeInvalidRule}, // 小数超过三位
		{"2026-08-30T12:00:00 Z", CodeInvalidRule},     // 多一个空格
		{"0000-08-30T12:00:00Z", CodeInvalidRule},      // 零年份
		{"2026-08-30T25:00:00Z", CodeInvalidRule},      // 小时越界
		{"2026-08-30T12:60:00Z", CodeInvalidRule},      // 分钟越界
		{"2026-08-30T12:00:60Z", CodeInvalidRule},      // 秒越界
		{"2026-02-30T12:00:00Z", CodeInvalidRule},      // 日历上不存在
		{"2026-08-30T12:00:00-00:00", CodeInvalidRule}, // 偏移量未知
		{"2026-08-30T12:00:00+24:00", CodeInvalidRule}, // 偏移小时越界
		{"2026-08-30T12:00:00+01:60", CodeInvalidRule}, // 偏移分钟越界
	} {
		_, err := parseOffsetInstant(each.value)
		if got := errorCode(t, err); got != each.code {
			t.Fatalf("parseOffsetInstant(%q) 报的是 %v，本该是 %v", each.value, got, each.code)
		}
	}
}

func TestParseOffsetInstantAcceptsPlusZeroOffset(t *testing.T) {
	// +00:00 和 -00:00 只差一个符号，语义完全不同。这一条和上面那条负零的用例
	// 成对存在，防的是有人把两者一起放行或者一起拒掉。
	if _, err := parseOffsetInstant("2026-08-30T12:00:00+00:00"); err != nil {
		t.Fatalf("+00:00 本该收下：%v", err)
	}
}

func TestCanonicalizeTimeZone(t *testing.T) {
	for _, name := range []string{"UTC", "America/New_York", "Asia/Shanghai"} {
		if _, err := CanonicalizeTimeZone(name); err != nil {
			t.Fatalf("CanonicalizeTimeZone(%q) 报了 %v", name, err)
		}
	}
	for _, name := range []string{
		"",
		" UTC",
		"UTC ",
		"Local",              // 会掉进这台机器的时区
		"EST",                // 不带斜杠的缩写
		"America/Nowhere",    // 形状对，机器上没有
		"America//New_York",  // 空的一段
		"1America/New_York",  // 首字符不是字母
		"America/New York",   // 有空格
		"America/New_York/X", // 这一条其实合法形状，但机器上没有
	} {
		_, err := CanonicalizeTimeZone(name)
		if got := errorCode(t, err); got != CodeInvalidTimeZone {
			t.Fatalf("CanonicalizeTimeZone(%q) 报的是 %v，本该是 invalid_time_zone", name, got)
		}
	}
}

func TestParseLocalAtRejects(t *testing.T) {
	for _, each := range []struct {
		date  string
		clock string
	}{
		{"2026-8-30", "12:00:00"},       // 月份不足两位
		{"2026-08-30", "12:00"},         // 少了秒
		{"2026-08-30", "12:00:00.1234"}, // 小数超过三位
		{"0000-08-30", "12:00:00"},      // 零年份
		{"2026-08-30", "24:00:00"},      // 小时越界
		{"2026-02-30", "12:00:00"},      // 日历上不存在
	} {
		_, err := parseLocalAt(each.date, each.clock)
		if got := errorCode(t, err); got != CodeInvalidRule {
			t.Fatalf("parseLocalAt(%q, %q) 报的是 %v", each.date, each.clock, got)
		}
	}
}

func TestResolveLocalInstantPicksEarliestAcrossFallBack(t *testing.T) {
	// 2026-11-01 01:30 在纽约出现两次：一次在 EDT（UTC-4），一次在 EST（UTC-5）。
	// 规则是取早的那个，也就是 EDT 那一次。
	location, err := CanonicalizeTimeZone("America/New_York")
	if err != nil {
		t.Fatalf("加载时区失败：%v", err)
	}
	fields, err := parseLocalAt("2026-11-01", "01:30:00")
	if err != nil {
		t.Fatalf("解本地字段失败：%v", err)
	}
	got, err := resolveLocalInstant(fields, location)
	if err != nil {
		t.Fatalf("回拨那一小时本该有解：%v", err)
	}
	want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC).UnixMilli()
	if got != want {
		t.Fatalf("取到的是 %s，本该是 EDT 那一次 %s",
			FormatInstant(time.UnixMilli(got)), FormatInstant(time.UnixMilli(want)))
	}
}

func TestResolveLocalInstantRejectsSpringForwardGap(t *testing.T) {
	// 2026-03-08 02:30 在纽约根本不存在：时钟从 02:00 直接跳到 03:00。
	location, err := CanonicalizeTimeZone("America/New_York")
	if err != nil {
		t.Fatalf("加载时区失败：%v", err)
	}
	fields, err := parseLocalAt("2026-03-08", "02:30:00")
	if err != nil {
		t.Fatalf("解本地字段失败：%v", err)
	}
	_, err = resolveLocalInstant(fields, location)
	if got := errorCode(t, err); got != CodeInvalidRule {
		t.Fatalf("跳过的那一小时报的是 %v，本该是 invalid_rule", got)
	}
}

func TestResolveLocalInstantHandlesOrdinaryZone(t *testing.T) {
	// 一个不做夏令时的时区上，五个采样点得到同一个偏移量，只留下一个候选。
	location, err := CanonicalizeTimeZone("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区失败：%v", err)
	}
	fields, err := parseLocalAt("2026-08-30", "20:00:00.250")
	if err != nil {
		t.Fatalf("解本地字段失败：%v", err)
	}
	got, err := resolveLocalInstant(fields, location)
	if err != nil {
		t.Fatalf("本该有解：%v", err)
	}
	want := time.Date(2026, 8, 30, 12, 0, 0, int(250*time.Millisecond), time.UTC).UnixMilli()
	if got != want {
		t.Fatalf("折出来的是 %d，本该是 %d", got, want)
	}
}

func TestResolveLocalInstantReportsOutOfRange(t *testing.T) {
	// 9999-12-31 23:59 在一个**西**边的时区上折回 UTC 要往后加五个小时，越过窗口
	// 上端。东八区反过来是往前减，落在窗口里，所以这一条必须挑西边的时区才走得到
	// outOfRange 那条路：一个候选都留不下来，但原因是越界而不是「这一刻不存在」。
	location, err := CanonicalizeTimeZone("America/New_York")
	if err != nil {
		t.Fatalf("加载时区失败：%v", err)
	}
	fields, err := parseLocalAt("9999-12-31", "23:59:59.999")
	if err != nil {
		t.Fatalf("解本地字段失败：%v", err)
	}
	_, err = resolveLocalInstant(fields, location)
	if got := errorCode(t, err); got != CodeTimeOutOfRange {
		t.Fatalf("越界报的是 %v，本该是 time_out_of_range", got)
	}
}

func TestResolveAtInputDiscriminatesShapes(t *testing.T) {
	utc := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC).UnixMilli()

	got, err := resolveAtInput(json.RawMessage(`"2026-08-30T12:00:00Z"`))
	if err != nil || got != utc {
		t.Fatalf("字符串那一支得到 (%d, %v)", got, err)
	}

	got, err = resolveAtInput(json.RawMessage(
		`{"date":"2026-08-30","time":"20:00:00","time_zone":"Asia/Shanghai"}`))
	if err != nil || got != utc {
		t.Fatalf("本地对象那一支得到 (%d, %v)", got, err)
	}
}

func TestResolveAtInputRejectsWrongShapes(t *testing.T) {
	for _, each := range []struct {
		raw  string
		code ErrorCode
	}{
		{`42`, CodeInvalidRule},
		{`true`, CodeInvalidRule},
		{`null`, CodeInvalidRule},
		{`["2026-08-30T12:00:00Z"]`, CodeInvalidRule},
		{`{`, CodeInvalidRule}, // 根本不是 JSON
		{`{"date":"2026-08-30","time":"20:00:00"}`, CodeInvalidRule},
		{`{"date":"2026-08-30","time":"20:00:00","zone":"UTC"}`, CodeInvalidRule},
		{`{"date":"2026-08-30","time":"20:00:00","time_zone":"UTC","extra":1}`, CodeInvalidRule},
		{`{"date":1,"time":"20:00:00","time_zone":"UTC"}`, CodeInvalidRule},
		{`{"date":"2026-08-30","time":2,"time_zone":"UTC"}`, CodeInvalidRule},
		{`{"date":"2026-08-30","time":"20:00:00","time_zone":3}`, CodeInvalidTimeZone},
		{`{"date":"2026-08-30","time":"20:00:00","time_zone":"Local"}`, CodeInvalidTimeZone},
	} {
		_, err := resolveAtInput(json.RawMessage(each.raw))
		if got := errorCode(t, err); got != each.code {
			t.Fatalf("resolveAtInput(%s) 报的是 %v，本该是 %v", each.raw, got, each.code)
		}
	}
}

func TestResolveLocalAtObjectRejectsThreeWrongKeys(t *testing.T) {
	// 键数对得上、但名字不对，走的是那条按名字取的分支而不是那条数个数的。
	_, err := resolveLocalAtObject(map[string]any{"a": "1", "b": "2", "c": "3"})
	if got := errorCode(t, err); got != CodeInvalidRule {
		t.Fatalf("报的是 %v，本该是 invalid_rule", got)
	}
}

func TestFractionMillisPadsRight(t *testing.T) {
	// 「.5」是五百毫秒，不是五毫秒。补零补错方向会让每一条带小数秒的规则都偏。
	for _, each := range []struct {
		fraction string
		want     int
	}{{"", 0}, {"5", 500}, {"05", 50}, {"005", 5}, {"123", 123}} {
		if got := fractionMillis(each.fraction); got != each.want {
			t.Fatalf("fractionMillis(%q) = %d，本该是 %d", each.fraction, got, each.want)
		}
	}
}

func TestGroupHandlesMissingName(t *testing.T) {
	// 这条守的是 group 那两道越界判断：拿一个不存在的组名去问，得到空串而不是崩。
	match := localDatePattern.FindStringSubmatch("2026-08-30")
	if got := group(localDatePattern, match, "nope"); got != "" {
		t.Fatalf("不存在的组名交回了 %q", got)
	}
	if got := group(localDatePattern, nil, "year"); got != "" {
		t.Fatalf("空 match 交回了 %q", got)
	}
}

func TestContainsInt64(t *testing.T) {
	if !containsInt64([]int64{1, 2, 3}, 2) {
		t.Fatal("在的元素没找到")
	}
	if containsInt64([]int64{1, 2, 3}, 4) {
		t.Fatal("不在的元素找到了")
	}
}
