// 本文件的作用：续页令牌——它里面装什么、怎么签、怎么验，以及一次请求的指纹
// 是怎么算出来的。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:186-201（CursorPayload）
// 源: packages/session-query/session-query-sqlite/src/query.ts:244-267、444-455（requestFingerprint、canonicalFilters）

package searchstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// cursorVersion 是令牌自身格式的版本号。
//
// 认不出的版本一律当「不是本后端签发的」处置，不做兼容：一个游标的寿命是几秒到
// 几分钟，跨版本续页这件事没有人需要。
const cursorVersion = 1

// 两种检索各自的作用域名，拌进指纹里，好让两边的令牌换不过来。
const (
	scopeSessions = "sessions"
	scopeEvents   = "events"
)

// cursorPayload 是一个续页令牌解开之后的样子。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:186-201
//
// 新增: DSH 那份里还有一个 instance 字段，装的是签发这个令牌的那个进程的标识，
// 用来认出「这不是我签的」。本仓库的检索要在多个副本上跑，那个字段会让每一次
// 换副本都变成一次「游标无效」。这里把它去掉，靠指纹和世代认——两者都是从内容
// 算出来的，任何副本都算得出同一个数。
type cursorPayload struct {
	// Version 是令牌格式的版本，见 [cursorVersion]。
	Version int `json:"v"`
	// Scope 是这个令牌属于哪一种检索。
	Scope string `json:"scope"`
	// Fingerprint 是签发它的那次请求的指纹。
	Fingerprint string `json:"fp"`
	// Generation 是签发它时那份语料的世代。
	Generation string `json:"gen"`
	// Offset 是从第几条接着往下取。
	Offset int `json:"offset"`
}

// encodeCursor 把一个续页位置签成令牌。
func encodeCursor(payload cursorPayload) sessionquery.SearchCursor {
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 五个字段全是 int 和 string，编不出来是不可能的。
		panic("searchstore: 续页令牌编不出来：" + err.Error())
	}
	return sessionquery.SearchCursor(base64.RawURLEncoding.EncodeToString(encoded))
}

// decodeCursor 验一个续页令牌，交回它指向的位置。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:959-989（decodeCursor）
//
// 三种拒绝分得很清，因为调用方要拿它们做三件不同的事：
//
//	读不回来、版本不认、作用域不对、指纹不同 → SESSION_QUERY_INVALID_CURSOR
//	                                          这不是同一次查询的令牌，重发请求
//	世代不同                                 → SESSION_QUERY_STALE_CURSOR
//	                                          语料在两页之间变过了，从第一页重来
//
// 混成一个码的后果是调用方只能一律从头再来，包括那些其实只是把令牌传串了的场合。
func decodeCursor(
	cursor sessionquery.SearchCursor,
	scope, fingerprint, generation string,
) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(cursor))
	if err != nil {
		return 0, wrap(sessionquery.CodeInvalidCursor, err, "续页令牌读不回来")
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, wrap(sessionquery.CodeInvalidCursor, err, "续页令牌读不回来")
	}
	if payload.Version != cursorVersion {
		return 0, fail(sessionquery.CodeInvalidCursor,
			"续页令牌是第 %d 版的，这个后端认的是第 %d 版", payload.Version, cursorVersion)
	}
	if payload.Scope != scope {
		return 0, fail(sessionquery.CodeInvalidCursor,
			"续页令牌是 %q 那一种检索的，这次是 %q", payload.Scope, scope)
	}
	if payload.Fingerprint != fingerprint {
		return 0, fail(sessionquery.CodeInvalidCursor, "续页令牌不属于这次请求")
	}
	if payload.Offset < 0 {
		return 0, fail(sessionquery.CodeInvalidCursor, "续页令牌里的位置是负数")
	}
	if payload.Generation != generation {
		return 0, fail(sessionquery.CodeStaleCursor, "语料在两页之间变过了，请从第一页重来")
	}
	return payload.Offset, nil
}

// sessionsFingerprint 给一次跨会话检索算指纹。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:244-267（requestFingerprint）
func sessionsFingerprint(
	query string,
	sessionFilters []sessionquery.SessionFilter,
	eventFilters []sessionquery.EventFilter,
	limit int,
) string {
	return digestParts(scopeSessions, query, fmt.Sprint(limit),
		strings.Join(canonicalSessionFilters(sessionFilters), "\x01"),
		strings.Join(canonicalEventFilters(eventFilters), "\x01"))
}

// eventsFingerprint 给一次会话内检索算指纹。
func eventsFingerprint(
	id sessionlog.SessionID,
	query string,
	filters []sessionquery.EventFilter,
	limit int,
) string {
	return digestParts(scopeEvents, string(id), query, fmt.Sprint(limit),
		strings.Join(canonicalEventFilters(filters), "\x01"))
}

// digestParts 把几段文字压成一个指纹。
//
// 段与段之间垫一个 NUL：不垫的话 ("ab","c") 和 ("a","bc") 会哈出同一个数，
// 于是两次不同的请求共用一个游标。
func digestParts(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		digest.Write([]byte(part))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// canonicalSessionFilters 把一组会话过滤器写成一份与书写顺序无关的文字。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:444-455（canonicalFilters）
//
// 一条过滤器里的取值之间是**或**，所以取值的先后不影响它筛出什么，排一遍；
// 一组过滤器之间是**与**，所以过滤器的先后也不影响，也排一遍。不排的话，同一份
// 谓词换个写法就成了另一次请求，续页令牌当场作废。
func canonicalSessionFilters(filters []sessionquery.SessionFilter) []string {
	parts := make([]string, 0, len(filters))
	for _, filter := range filters {
		switch typed := filter.(type) {
		case sessionquery.IDFilter:
			parts = append(parts, canonicalValues("id", typed.Values))
		case sessionquery.WorkspaceFilter:
			parts = append(parts, canonicalValues("workspace", typed.Values))
		case sessionquery.CreatedAtFilter:
			parts = append(parts, canonicalRange("created-at", typed.Range))
		case sessionquery.ParentFilter:
			parts = append(parts, canonicalValues("parent", typed.Values))
		case sessionquery.AvailabilityFilter:
			parts = append(parts, canonicalValues("availability", typed.Values))
		default:
			// 走不到：过滤器是封印的，而上面五个就是全部变体。留着它，本包漏接
			// 一个新变体时指纹会当场变形，而不是悄悄把两条不同的谓词哈成一个数。
			parts = append(parts, fmt.Sprintf("unknown(%T)", filter))
		}
	}
	slices.Sort(parts)
	return parts
}

// canonicalEventFilters 把一组事件过滤器写成一份与书写顺序无关的文字，理由同上。
func canonicalEventFilters(filters []sessionquery.EventFilter) []string {
	parts := make([]string, 0, len(filters))
	for _, filter := range filters {
		switch typed := filter.(type) {
		case sessionquery.SeqFilter:
			parts = append(parts, canonicalRange("seq", typed.Range))
		case sessionquery.TimeFilter:
			parts = append(parts, canonicalRange("time", typed.Range))
		case sessionquery.TypeFilter:
			parts = append(parts, canonicalValues("type", typed.Values))
		case sessionquery.SurfaceFilter:
			parts = append(parts, canonicalValues("surface", typed.Values))
		case sessionquery.TextFilter:
			parts = append(parts, "text="+typed.Text)
		default:
			parts = append(parts, fmt.Sprintf("unknown(%T)", filter))
		}
	}
	slices.Sort(parts)
	return parts
}

// canonicalValues 把一条按取值筛的过滤器写成 `名字=[排好序的取值]`。
func canonicalValues[T ~string](name string, values []T) string {
	sorted := make([]string, 0, len(values))
	for _, value := range values {
		sorted = append(sorted, string(value))
	}
	slices.Sort(sorted)
	return name + "=[" + strings.Join(sorted, ",") + "]"
}

// canonicalRange 把一条按区间筛的过滤器写成 `名字=[下界,上界]`，不设限写成空。
func canonicalRange(name string, r sessionquery.Range) string {
	return name + "=[" + bound(r.From) + "," + bound(r.To) + "]"
}

// bound 把一个可以不设限的边界写成文字。
func bound(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}
