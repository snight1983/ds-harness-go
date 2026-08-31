// 本文件的作用：不依赖任何检索后端的纯谓词——逻辑会话过滤、事件过滤、
// 以及那个防注入的字面文本匹配器。
//
// 源: packages/session-query/session-query/src/filters.ts

package sessionquery

import (
	"regexp"
	"slices"
	"strings"
)

// logicalRecord 让嵌了 [Record] 的类型（比如 [SearchHit]）也能走会话过滤。
//
// 新增: DSH 用的是 `<T extends SessionRecord>` 这种结构子类型约束。Go 的类型
// 参数约束认的是方法集，所以这里挂一个未导出的取值方法：[Record] 自己实现它，
// 嵌了 Record 的类型自动提升。未导出是有意的——本包外面只能靠嵌入参与，
// 不能自己编一个「像会话记录」的东西混进来。
func (r Record) logicalRecord() Record { return r }

// searchDocument 让嵌了 [EventSearchDocument] 的类型也能走事件过滤，理由同上。
func (d EventSearchDocument) searchDocument() EventSearchDocument { return d }

// LogicalRecord 是「能当逻辑会话记录用」的类型集合。
type LogicalRecord interface{ logicalRecord() Record }

// SearchDocument 是「能当语义文档用」的类型集合。
type SearchDocument interface{ searchDocument() EventSearchDocument }

// FilterSessions 用一组**与**关系的逻辑会话谓词过滤，保持输入顺序。
//
// 源: packages/session-query/session-query/src/filters.ts:18-25
func FilterSessions[T LogicalRecord](records []T, filters []SessionFilter) ([]T, error) {
	predicates := make([]func(Record) bool, 0, len(filters))
	for _, filter := range filters {
		predicate, err := sessionPredicate(filter)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, predicate)
	}
	var kept []T
	for _, record := range records {
		if matchesAll(predicates, record.logicalRecord()) {
			kept = append(kept, record)
		}
	}
	return kept, nil
}

// FilterEventDocuments 用一组**与**关系的事件谓词过滤语义文档，保持输入顺序。
//
// 源: packages/session-query/session-query/src/filters.ts:27-38
func FilterEventDocuments[T SearchDocument](documents []T, filters []EventFilter) ([]T, error) {
	predicates := make([]func(EventSearchDocument) bool, 0, len(filters))
	for _, filter := range filters {
		predicate, err := eventPredicate(filter)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, predicate)
	}
	var kept []T
	for _, document := range documents {
		if matchesAll(predicates, document.searchDocument()) {
			kept = append(kept, document)
		}
	}
	return kept, nil
}

// matchesAll 是「每一条谓词都得过」这句话。
func matchesAll[T any](predicates []func(T) bool, value T) bool {
	for _, predicate := range predicates {
		if !predicate(value) {
			return false
		}
	}
	return true
}

// MaterializeSessionFilters 在跨过异步边界之前，复制并验一遍逻辑会话过滤器。
//
// 源: packages/session-query/session-query/src/filters.ts:40-70
//
// 复制是必须的：调用方交出来的切片归调用方所有，它随时可以在我们 await
// 持久化列举的空档里改掉自己那份，那样验过的东西和用上的东西就不是一回事了。
//
// 新增: DSH 那个函数里大半篇幅是 `Array.isArray` / `typeof value !== 'string'`
// 这类运行期类型检查，防的是 TypeScript 的类型被外部调用方绕过。Go 的封闭接口
// 让那些状态根本表达不出来，所以这里只剩三件真事：复制一份切片、验区间上下界、
// 验封闭词汇。
func MaterializeSessionFilters(filters []SessionFilter) ([]SessionFilter, error) {
	owned := make([]SessionFilter, 0, len(filters))
	for _, filter := range filters {
		switch typed := filter.(type) {
		case IDFilter:
			owned = append(owned, IDFilter{Values: slices.Clone(typed.Values)})
		case CwdFilter:
			owned = append(owned, CwdFilter{Values: slices.Clone(typed.Values)})
		case CreatedAtFilter:
			copied, err := copyRange("created-at", typed.Range)
			if err != nil {
				return nil, err
			}
			owned = append(owned, CreatedAtFilter{Range: copied})
		case ParentFilter:
			owned = append(owned, ParentFilter{Values: slices.Clone(typed.Values)})
		case AvailabilityFilter:
			values := slices.Clone(typed.Values)
			if err := assertAllowed("availability", values, []Availability{AvailabilityLive, AvailabilityPersisted}); err != nil {
				return nil, err
			}
			owned = append(owned, AvailabilityFilter{Values: values})
		default:
			return nil, unknownFilter(filter)
		}
	}
	return owned, nil
}

// MaterializeEventFilters 在跨过异步边界之前，复制并验一遍事件过滤器。理由同上。
//
// 源: packages/session-query/session-query/src/filters.ts:72-100
func MaterializeEventFilters(filters []EventFilter) ([]EventFilter, error) {
	owned := make([]EventFilter, 0, len(filters))
	for _, filter := range filters {
		switch typed := filter.(type) {
		case SeqFilter:
			copied, err := copyRange("seq", typed.Range)
			if err != nil {
				return nil, err
			}
			owned = append(owned, SeqFilter{Range: copied})
		case TimeFilter:
			copied, err := copyRange("time", typed.Range)
			if err != nil {
				return nil, err
			}
			owned = append(owned, TimeFilter{Range: copied})
		case TypeFilter:
			owned = append(owned, TypeFilter{Values: slices.Clone(typed.Values)})
		case SurfaceFilter:
			values := slices.Clone(typed.Values)
			if err := assertAllowed("surface", values, []EventSurface{SurfaceCurrent, SurfaceShadowed, SurfaceLogOnly}); err != nil {
				return nil, err
			}
			owned = append(owned, SurfaceFilter{Values: values})
		case TextFilter:
			owned = append(owned, TextFilter{Text: typed.Text})
		default:
			return nil, unknownFilter(filter)
		}
	}
	return owned, nil
}

// CompileTextFilter 编出一个字面的、忽略大小写、空白宽松的语义文字匹配器。
//
// 源: packages/session-query/session-query/src/filters.ts:102-121
//
// 调用方给的每一段都被 [regexp.QuoteMeta] 转义，所以查询串里的正则元字符
// 一律当普通字符看——这是防注入的那一步，不是可选的美化。段与段之间换成
// `\s+`，于是查询里的空白怎么打都能对上文本里的换行、多空格。
//
// 新增: DSH 拼完之后 new RegExp(pattern, 'iu')；Go 这边前缀 `(?i)` 就是那个 i，
// 而 u 是白送的——Go 的正则本来就按 UTF-8 走，`(?i)` 的大小写折叠也是 Unicode 的。
func CompileTextFilter(text string) (*regexp.Regexp, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fail(CodeInvalidFilter, "会话文本过滤器必须含有非空白文字")
	}
	parts := strings.Fields(trimmed)
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, regexp.QuoteMeta(part))
	}
	// 每一段都转义过，拼出来的模式一定编得过，所以这里不会失败。
	return regexp.Compile("(?i)" + strings.Join(quoted, `\s+`))
}

// sessionPredicate 把一条逻辑会话过滤器编成一个判定函数。
//
// 源: packages/session-query/session-query/src/filters.ts:123-141
func sessionPredicate(filter SessionFilter) (func(Record) bool, error) {
	switch typed := filter.(type) {
	case IDFilter:
		return func(record Record) bool { return slices.Contains(typed.Values, record.Header.ID) }, nil
	case CwdFilter:
		return func(record Record) bool { return slices.Contains(typed.Values, record.Header.Cwd) }, nil
	case CreatedAtFilter:
		if err := validateRange("created-at", typed.Range); err != nil {
			return nil, err
		}
		return func(record Record) bool { return matchesRange(record.Header.CreatedAt, typed.Range) }, nil
	case ParentFilter:
		return func(record Record) bool {
			return slices.Contains(typed.Values, record.Header.ParentSession)
		}, nil
	case AvailabilityFilter:
		if err := assertAllowed("availability", typed.Values, []Availability{AvailabilityLive, AvailabilityPersisted}); err != nil {
			return nil, err
		}
		return func(record Record) bool {
			for _, value := range typed.Values {
				if value == AvailabilityLive && record.Live {
					return true
				}
				if value == AvailabilityPersisted && record.Persisted {
					return true
				}
			}
			return false
		}, nil
	default:
		return nil, unknownFilter(filter)
	}
}

// eventPredicate 把一条事件过滤器编成一个判定函数。
//
// 源: packages/session-query/session-query/src/filters.ts:143-166
func eventPredicate(filter EventFilter) (func(EventSearchDocument) bool, error) {
	switch typed := filter.(type) {
	case SeqFilter:
		if err := validateRange("seq", typed.Range); err != nil {
			return nil, err
		}
		return func(document EventSearchDocument) bool {
			return matchesRange(int64(document.Seq), typed.Range)
		}, nil
	case TimeFilter:
		if err := validateRange("time", typed.Range); err != nil {
			return nil, err
		}
		return func(document EventSearchDocument) bool {
			return matchesRange(document.Time, typed.Range)
		}, nil
	case TypeFilter:
		return func(document EventSearchDocument) bool {
			return slices.Contains(typed.Values, document.Type)
		}, nil
	case SurfaceFilter:
		if err := assertAllowed("surface", typed.Values, []EventSurface{SurfaceCurrent, SurfaceShadowed, SurfaceLogOnly}); err != nil {
			return nil, err
		}
		return func(document EventSearchDocument) bool {
			return slices.Contains(typed.Values, document.Surface)
		}, nil
	case TextFilter:
		pattern, err := CompileTextFilter(typed.Text)
		if err != nil {
			return nil, err
		}
		return func(document EventSearchDocument) bool {
			return pattern.MatchString(document.Text)
		}, nil
	default:
		return nil, unknownFilter(filter)
	}
}

// copyRange 复制一个区间并验它。
//
// 源: packages/session-query/session-query/src/filters.ts:196-207
func copyRange(name string, source Range) (Range, error) {
	copied := Range{}
	if source.From != nil {
		from := *source.From
		copied.From = &from
	}
	if source.To != nil {
		to := *source.To
		copied.To = &to
	}
	return copied, validateRange(name, copied)
}

// validateRange 只剩下「下界不能大于上界」这一条。
//
// 源: packages/session-query/session-query/src/filters.ts:227-240
//
// 新增: DSH 那边还要验 Number.isFinite——它的 number 装得下 NaN 和 Infinity。
// int64 装不下，那两条检查在这里消失了。
func validateRange(name string, r Range) error {
	if r.From != nil && r.To != nil && *r.From > *r.To {
		return fail(CodeInvalidFilter, "会话 %s 过滤器的下界不能大于上界", name)
	}
	return nil
}

// matchesRange 判断一个值落不落在闭区间里。
//
// 源: packages/session-query/session-query/src/filters.ts:228-231
func matchesRange(value int64, r Range) bool {
	return (r.From == nil || value >= *r.From) && (r.To == nil || value <= *r.To)
}

// assertAllowed 验一组取值全在封闭词汇里。
//
// 源: packages/session-query/session-query/src/filters.ts:209-225
func assertAllowed[T ~string](name string, values, allowed []T) error {
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return fail(CodeInvalidFilter, "会话 %s 过滤器里有一个不认识的取值 %q", name, string(value))
		}
	}
	return nil
}

// unknownFilter 报一个本包不认识的过滤器变体。
//
// 源: packages/session-query/session-query/src/filters.ts:209-213
//
// 封印方法把外面挡在门外，所以走到这里只有一种可能：本包自己新加了一个变体，
// 却忘了在这两处 switch 里接住它。留着这一条，那种疏忽会在测试里当场炸出来，
// 而不是变成一条被静默忽略的过滤器——后者会让查询悄悄返回比预期多的结果。
func unknownFilter(filter any) error {
	return fail(CodeInvalidFilter, "会话过滤器的类型不认识：%T", filter)
}

// 编译期确认：本包自己的两个 EventRecord 承载体都能走过滤。
var (
	_ SearchDocument = EventSearchDocument{}
	_ LogicalRecord  = Record{}
	_ LogicalRecord  = SearchHit{}
)
