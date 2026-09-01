// 本文件的作用：模型写得出来的那些参数——它们的 schema、它们怎么被洗干净、
// 以及怎么变成引擎认得的过滤器。
//
// 源: packages/session-query/tool-session-query/src/input.ts
//
// 本文件里的错误**不经过**那道清洗门（见 boundary.go）：它们说的全是模型自己
// 刚写下的那几个参数哪里不对，是模型下一步唯一的依据，藏起来等于让它瞎改。
// 引擎内情的错误才需要清洗，这些不需要。

package querytool

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/sessionquery"
)

// sessionSearchArgs 是 session_search 的参数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:20-34
//
// 可选的数和布尔用指针，可选的表用切片：一个没给的 event_seq_from 和一个写着 0 的
// 是两件事，一张没给的 session_ids 和一张空表也是两件事（后者要报错，见
// [assertNonEmpty]）。json 解码天然分得开这两组——缺席留 nil，`[]` 解出一个非 nil
// 的空切片。
type sessionSearchArgs struct {
	Query               string                      `json:"query"`
	SessionIDs          []string                    `json:"session_ids"`
	CreatedAtFrom       string                      `json:"created_at_from"`
	CreatedAtTo         string                      `json:"created_at_to"`
	ParentSessionIDs    []string                    `json:"parent_session_ids"`
	IncludeRootSessions bool                        `json:"include_root_sessions"`
	Availability        []sessionquery.Availability `json:"availability"`
	EventSeqFrom        *int                        `json:"event_seq_from"`
	EventSeqTo          *int                        `json:"event_seq_to"`
	EventTimeFrom       string                      `json:"event_time_from"`
	EventTimeTo         string                      `json:"event_time_to"`
	EventTypes          []string                    `json:"event_types"`
	EventSurfaces       []sessionquery.EventSurface `json:"event_surfaces"`
}

// eventSearchArgs 是 session_event_search 的参数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:36-43,68-82
type eventSearchArgs struct {
	SessionID  string                      `json:"session_id"`
	Query      string                      `json:"query"`
	SeqFrom    *int                        `json:"seq_from"`
	SeqTo      *int                        `json:"seq_to"`
	TimeFrom   string                      `json:"time_from"`
	TimeTo     string                      `json:"time_to"`
	EventTypes []string                    `json:"event_types"`
	Surfaces   []sessionquery.EventSurface `json:"surfaces"`
}

// sessionTargetArgs 是只指一个会话的那件工具的参数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:84-86
type sessionTargetArgs struct {
	SessionID string `json:"session_id"`
}

// eventTargetArgs 是指一个会话里一条事件的参数。
//
// 源: packages/session-query/tool-session-query/src/index.ts:98-101
type eventTargetArgs struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
}

// eventReadArgs 是 session_event_read 的参数。
//
// 源: packages/session-query/tool-session-query/src/index.ts:111-115
type eventReadArgs struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
	Before    *int   `json:"before"`
	After     *int   `json:"after"`
}

// eventFilterInput 是两件检索工具共用的那批事件过滤参数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:36-43
type eventFilterInput struct {
	seqFrom    *int
	seqTo      *int
	timeFrom   string
	timeTo     string
	eventTypes []string
	surfaces   []sessionquery.EventSurface
}

// buildSessionFilters 把会话那一侧的参数搭成过滤器。
//
// 源: packages/session-query/tool-session-query/src/input.ts:88-101
//
// parent 那一条不在这里：它要先过一遍授权，见 [Controller.executeSessionSearch]。
func buildSessionFilters(args sessionSearchArgs) ([]sessionquery.SessionFilter, error) {
	var filters []sessionquery.SessionFilter
	if args.SessionIDs != nil {
		if err := assertNonEmpty("session_ids", len(args.SessionIDs)); err != nil {
			return nil, err
		}
		ids := make([]session.SessionID, 0, len(args.SessionIDs))
		for _, id := range args.SessionIDs {
			ids = append(ids, session.SessionID(id))
		}
		filters = append(filters, sessionquery.IDFilter{Values: ids})
	}
	created, ok, err := timestampRange("created_at", args.CreatedAtFrom, args.CreatedAtTo)
	if err != nil {
		return nil, err
	}
	if ok {
		filters = append(filters, sessionquery.CreatedAtFilter{Range: created})
	}
	if args.Availability != nil {
		if err := assertNonEmpty("availability", len(args.Availability)); err != nil {
			return nil, err
		}
		filters = append(filters, sessionquery.AvailabilityFilter{Values: args.Availability})
	}
	return filters, nil
}

// materializeParentSessionIDs 把父会话那张表去重定下来；没给时第二个返回值为假。
//
// 源: packages/session-query/tool-session-query/src/input.ts:103-107
//
// 去重是必要的：下面那条 parent 过滤器的取值表要拿去和授权过的集合对齐，
// 重复的 id 会让同一个父会话被数两遍。
func materializeParentSessionIDs(values []string) ([]session.SessionID, bool, error) {
	if values == nil {
		return nil, false, nil
	}
	if err := assertNonEmpty("parent_session_ids", len(values)); err != nil {
		return nil, false, err
	}
	seen := make(map[session.SessionID]struct{}, len(values))
	unique := make([]session.SessionID, 0, len(values))
	for _, value := range values {
		id := session.SessionID(value)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique, true, nil
}

// buildEventFilters 把事件那一侧的参数搭成过滤器。
//
// 源: packages/session-query/tool-session-query/src/input.ts:109-125
func buildEventFilters(input eventFilterInput) ([]sessionquery.EventMetadataFilter, error) {
	var filters []sessionquery.EventMetadataFilter
	seq, err := sequenceRange(input.seqFrom, input.seqTo)
	if err != nil {
		return nil, err
	}
	if seq.From != nil || seq.To != nil {
		filters = append(filters, sessionquery.SeqFilter{Range: seq})
	}
	moment, ok, err := timestampRange("time", input.timeFrom, input.timeTo)
	if err != nil {
		return nil, err
	}
	if ok {
		filters = append(filters, sessionquery.TimeFilter{Range: moment})
	}
	if input.eventTypes != nil {
		if err := assertNonEmpty("event_types", len(input.eventTypes)); err != nil {
			return nil, err
		}
		types := make([]session.EventType, 0, len(input.eventTypes))
		for _, kind := range input.eventTypes {
			types = append(types, session.EventType(kind))
		}
		filters = append(filters, sessionquery.TypeFilter{Values: types})
	}
	if input.surfaces != nil {
		if err := assertNonEmpty("surfaces", len(input.surfaces)); err != nil {
			return nil, err
		}
		filters = append(filters, sessionquery.SurfaceFilter{Values: input.surfaces})
	}
	return filters, nil
}

// normalizeQuery 把模型写的那句检索词洗成一行。
//
// 源: packages/session-query/tool-session-query/src/input.ts:127-143
//
// 空白折成单个空格是为了让「同一句话、换行方式不同」两次调用命中同一份缓存、
// 也命中同一批结果。NUL 单独拦掉：它在若干检索后端里是字符串终止符，混进去会让
// 查询在半路被截断，而截断之后那句话仍然是**合法**的，没人会发现。
func normalizeQuery(value string) (string, error) {
	query := strings.Join(strings.Fields(value), " ")
	if query == "" {
		return "", invalidQuery("session-search query must contain non-whitespace text")
	}
	if strings.ContainsRune(query, 0) {
		return "", invalidQuery("session-search query must not contain NUL")
	}
	return query, nil
}

// sequenceRange 验一对 seq 边界并搭成一个范围。
//
// 源: packages/session-query/tool-session-query/src/input.ts:145-160
func sequenceRange(from, to *int) (sessionquery.Range, error) {
	var out sessionquery.Range
	if from != nil {
		if err := assertNonNegative("sequence lower bound", *from); err != nil {
			return out, err
		}
		bound := int64(*from)
		out.From = &bound
	}
	if to != nil {
		if err := assertNonNegative("sequence upper bound", *to); err != nil {
			return out, err
		}
		bound := int64(*to)
		out.To = &bound
	}
	if out.From != nil && out.To != nil && *out.From > *out.To {
		return sessionquery.Range{}, invalidRange("sequence", "from must be less than or equal to to")
	}
	return out, nil
}

// timestampRange 验一对 ISO 时间戳并搭成一个毫秒范围；两头都没给时第二个返回值为假。
//
// 源: packages/session-query/tool-session-query/src/input.ts:162-184
func timestampRange(name, from, to string) (sessionquery.Range, bool, error) {
	if from == "" && to == "" {
		return sessionquery.Range{}, false, nil
	}
	var out sessionquery.Range
	var fromExact, toExact exactTimestamp
	if from != "" {
		parsed, err := parseISOTimestamp(name+"_from", from)
		if err != nil {
			return out, false, err
		}
		fromExact = parsed
	}
	if to != "" {
		parsed, err := parseISOTimestamp(name+"_to", to)
		if err != nil {
			return out, false, err
		}
		toExact = parsed
	}
	if from != "" && to != "" && compareTimestamps(fromExact, toExact) > 0 {
		return sessionquery.Range{}, false, invalidRange(name, "from must be less than or equal to to")
	}
	if from != "" {
		bound := timestampLowerBound(fromExact)
		out.From = &bound
	}
	if to != "" {
		bound := timestampUpperBound(toExact)
		out.To = &bound
	}
	return out, true, nil
}

// isoTimestamp 是那道时间戳的形状检查。
//
// 源: packages/session-query/tool-session-query/src/input.ts:186-187
//
// 秒可以省，小数可以省，时区**不可以**省：一个没有时区的时间戳要靠宿主的本地时区
// 才解得出来，那意味着同一句参数在两台机器上筛出不同的事件。
var isoTimestamp = regexp.MustCompile(
	`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(?:Z|([+-])(\d{2}):(\d{2}))$`)

// exactTimestamp 是一个解出来的时间戳：整毫秒部分，加上比一毫秒更细的那截小数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:189-193
type exactTimestamp struct {
	// millisecond 是 Unix 纪元毫秒。
	millisecond int64
	// remainder 是严格小于一毫秒的那几位十进制小数，尾零已经去掉。
	remainder string
}

// parseISOTimestamp 解一个带时区的 ISO 8601 时间戳。
//
// 源: packages/session-query/tool-session-query/src/input.ts:195-227
//
// 新增: 没走 time.Parse。那一套要求秒必须在（RFC3339），而 DSH 这道语法允许
// `2026-01-01T00:00Z`；换成 time.Parse 会让一批模型本来写得出来的参数突然被拒。
// 手写这一道也顺带把「二月三十号」这类日期挡在外面——time.Date 会把它规范成三月，
// 那意味着一个明显写错的参数被默默地当成了另一个日子。
func parseISOTimestamp(name, value string) (exactTimestamp, error) {
	match := isoTimestamp.FindStringSubmatch(value)
	if match == nil {
		return exactTimestamp{}, invalidRange(name, "must be an ISO 8601 timestamp with Z or a numeric offset")
	}
	year := atoi(match[1])
	month := atoi(match[2])
	day := atoi(match[3])
	hour := atoi(match[4])
	minute := atoi(match[5])
	second := atoi(match[6])
	offsetHour := atoi(match[9])
	offsetMinute := atoi(match[10])
	if month < 1 || month > 12 ||
		day < 1 || day > daysInMonth(year, month) ||
		hour > 23 || minute > 59 || second > 59 ||
		offsetHour > 23 || offsetMinute > 59 {
		return exactTimestamp{}, invalidRange(name, "must be a valid ISO 8601 timestamp")
	}
	offset := offsetHour*3600 + offsetMinute*60
	if match[8] == "-" {
		offset = -offset
	}
	// 小数只取前三位算进毫秒，剩下的原样留着当余数：那截小数决定这个边界是落在
	// 这一毫秒上还是下一毫秒上，见 [timestampLowerBound]。
	fraction := match[7]
	millisecondDigits := (fraction + "000")[:3]
	moment := time.Date(year, time.Month(month), day, hour, minute, second,
		atoi(millisecondDigits)*int(time.Millisecond), time.FixedZone("", offset))
	return exactTimestamp{
		millisecond: moment.UnixMilli(),
		remainder:   strings.TrimRight(fractionBeyondMillis(fraction), "0"),
	}, nil
}

// fractionBeyondMillis 取小数里比一毫秒更细的那几位。
func fractionBeyondMillis(fraction string) string {
	if len(fraction) <= 3 {
		return ""
	}
	return fraction[3:]
}

// compareTimestamps 比两个精确时间戳的先后。
//
// 源: packages/session-query/tool-session-query/src/input.ts:229-239
//
// 余数按位比，短的那个右边补零：`.5` 和 `.50` 是同一个时刻，`.5` 比 `.49` 晚。
func compareTimestamps(left, right exactTimestamp) int {
	if left.millisecond != right.millisecond {
		if left.millisecond < right.millisecond {
			return -1
		}
		return 1
	}
	length := max(len(left.remainder), len(right.remainder))
	for index := range length {
		leftDigit := digitAt(left.remainder, index)
		rightDigit := digitAt(right.remainder, index)
		if leftDigit != rightDigit {
			if leftDigit < rightDigit {
				return -1
			}
			return 1
		}
	}
	return 0
}

// digitAt 取余数的第 index 位，越界当零。
func digitAt(remainder string, index int) byte {
	if index >= len(remainder) {
		return '0'
	}
	return remainder[index]
}

// timestampLowerBound 把一个精确时间戳收成一个包含式的毫秒下界。
//
// 源: packages/session-query/tool-session-query/src/input.ts:241-245
//
// 新增: DSH 这里调 nextUpFinite——在 float64 的位表示上找下一个可表示的值，因为
// 那边范围端点是浮点数、能表示 `1000.0000000000001` 这种「刚好比一毫秒大一点」。
// Go 侧 [sessionquery.Range] 的端点是 int64 毫秒，根本表示不了亚毫秒，所以那套
// 位操作没有对应物，取而代之的是**向上取整**：一个带亚毫秒余数的下界意味着
// 这一整毫秒上的事件都在界外，下界因此是下一个整毫秒。
// 事件时间戳本身也是整毫秒（[session.Event.Time] 是 int64），所以筛出来的集合
// 和 DSH 逐个事件一致。上界同理，见 [timestampUpperBound]。
func timestampLowerBound(timestamp exactTimestamp) int64 {
	if timestamp.remainder == "" {
		return timestamp.millisecond
	}
	return timestamp.millisecond + 1
}

// timestampUpperBound 把一个精确时间戳收成一个包含式的毫秒上界。
//
// 源: packages/session-query/tool-session-query/src/input.ts:247-251
//
// 向下取整：一个带亚毫秒余数的上界意味着这一整毫秒上的事件仍然在界内
// （它们的时刻是这一毫秒的整点，比这个上界早），下一毫秒才在界外。
func timestampUpperBound(timestamp exactTimestamp) int64 {
	return timestamp.millisecond
}

// daysInMonth 交出某年某月有多少天。
//
// 源: packages/session-query/tool-session-query/src/input.ts:271-274
func daysInMonth(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

// atoi 解一段十进制数字；空串是零。
//
// 那些捕获组已经被正则钉成纯数字，解不出来是不可能的。
func atoi(text string) int {
	if text == "" {
		return 0
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0
	}
	return value
}

// invalidRange 造一条「这个范围不对」。
//
// 源: packages/session-query/tool-session-query/src/input.ts:276-281
func invalidRange(name, detail string) error {
	return engineError(sessionquery.CodeInvalidFilter, "session %s range %s", name, detail)
}

// invalidQuery 造一条「这句检索词不对」。
//
// 源: packages/session-query/tool-session-query/src/input.ts:129-141
func invalidQuery(message string) error {
	return engineError(sessionquery.CodeInvalidQuery, "%s", message)
}

// assertNonNegative 要求一个数是非负整数。
//
// 源: packages/session-query/tool-session-query/src/input.ts:283-290
//
// 新增: DSH 查的是 Number.isSafeInteger——那边所有数都是 float64，一个
// `1e21` 或者 `1.5` 都写得出来。Go 这边参数解出来就是 int，只剩「非负」要查。
func assertNonNegative(name string, value int) error {
	if value < 0 {
		return engineError(sessionquery.CodeInvalidFilter, "%s must be a non-negative safe integer", name)
	}
	return nil
}

// assertNonEmpty 要求一张给了的表里至少有一个值。
//
// 源: packages/session-query/tool-session-query/src/input.ts:292-299
//
// 一张空表和不给这个过滤器不是一回事：前者是模型想说「一个都不要」，那次调用
// 必然一条都返不回来，当场说清楚比让它对着一份空结果发愣好。
func assertNonEmpty(name string, length int) error {
	if length == 0 {
		return engineError(sessionquery.CodeInvalidFilter, "%s must contain at least one value when supplied", name)
	}
	return nil
}

// engineError 造一条带引擎分类码的错误。
//
// 新增: 这些参数错误在 DSH 那边是 SessionQueryError，报的是引擎的码而不是本包的。
// 照抄那个选择：这几句话说的全是模型自己写下的参数，本来就该原样给它看，而
// 引擎的码正好已经把「过滤器不对」和「检索词不对」分开了。
func engineError(code sessionquery.Code, format string, args ...any) error {
	return &sessionquery.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
