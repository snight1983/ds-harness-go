// 本文件的作用：本包对外那一套稳定的失败分类，以及带上它的错误值。
//
// 源: packages/session-query/session-query/src/config.ts:20-48

package sessionquery

import (
	"context"
	"errors"
	"fmt"
)

// Code 是一次会话查询失败的稳定分类，供机器路由。
//
// 源: packages/session-query/session-query/src/config.ts:19-37（SessionQueryErrorCode）
//
// 新增: DSH 那边这是一个字符串字面量联合，配一个 `SessionQueryError extends
// HarnessError` 把它收窄。Go 这边分派靠 errors.Is，所以 Code 自己就实现 error：
// `errors.Is(err, sessionquery.CodeAborted)` 直接能用，不必先 errors.As 出
// [Error] 再比字段。这是 syscall.Errno 用了几十年的写法。
//
// 取值保持 DSH 的原字符串，一个字都没改：这些码会出现在对外协议里，
// 换个写法就等于换一份契约。
type Code string

// Error 让 [Code] 自己就是一个可以被 errors.Is 认出来的哨兵。
func (c Code) Error() string { return string(c) }

// 这一套码是封闭的：本包只会报这十七个里的一个。
//
// 源: packages/session-query/session-query/src/config.ts:20-37
const (
	// CodeAborted 是调用方传进来的 ctx 已经取消了。
	CodeAborted Code = "SESSION_QUERY_ABORTED"
	// CodeCorruptSession 是落地的那份日志坏了，持久化层已经认定它没救。
	CodeCorruptSession Code = "SESSION_QUERY_CORRUPT_SESSION"
	// CodeEventNotFound 是这个会话里没有那个 seq 的事件。
	CodeEventNotFound Code = "SESSION_QUERY_EVENT_NOT_FOUND"
	// CodeIndexFailed 是检索后端在建索引或对账时失败了。
	//
	// 本包自己不报它——没有索引就没有建索引这件事。它留在这套码里是因为
	// 挂上来的 [Searcher] 需要一个属于本契约的码来报这类失败，
	// 而那些失败要和 [CodeSearchDisabled]、[CodeStaleCursor] 分得开。
	CodeIndexFailed Code = "SESSION_QUERY_INDEX_FAILED"
	// CodeInvalidConfig 是 [Options] 里的数字不合法。
	CodeInvalidConfig Code = "SESSION_QUERY_INVALID_CONFIG"
	// CodeInvalidCursor 是游标不是本后端签发的、或者读不回来。由 [Searcher] 报。
	CodeInvalidCursor Code = "SESSION_QUERY_INVALID_CURSOR"
	// CodeInvalidFilter 是过滤器的取值不在封闭词汇里、或者区间上下界反了。
	CodeInvalidFilter Code = "SESSION_QUERY_INVALID_FILTER"
	// CodeInvalidLimit 是分页大小不合法。由 [Searcher] 报。
	CodeInvalidLimit Code = "SESSION_QUERY_INVALID_LIMIT"
	// CodeInvalidQuery 是检索文本不合法。由 [Searcher] 报。
	CodeInvalidQuery Code = "SESSION_QUERY_INVALID_QUERY"
	// CodeInvalidLineage 是血统里有环。
	CodeInvalidLineage Code = "SESSION_QUERY_INVALID_LINEAGE"
	// CodeInvalidSurface 是这份日志的表面层折不出来。
	CodeInvalidSurface Code = "SESSION_QUERY_INVALID_SURFACE"
	// CodeInvalidWindow 是 before/after 超出了 [Options.ReadWindowMax]。
	CodeInvalidWindow Code = "SESSION_QUERY_INVALID_WINDOW"
	// CodePersistenceFailed 是持久化后端列举或装载时失败了。
	CodePersistenceFailed Code = "SESSION_QUERY_PERSISTENCE_FAILED"
	// CodeSearchDisabled 是没挂 [Searcher]，两个检索方法无从谈起。
	CodeSearchDisabled Code = "SESSION_QUERY_SEARCH_DISABLED"
	// CodeSessionNotFound 是这个 id 在活会话表和持久化后端里都没有。
	CodeSessionNotFound Code = "SESSION_QUERY_SESSION_NOT_FOUND"
	// CodeStaleCursor 是游标指向的索引世代已经过期。由 [Searcher] 报。
	CodeStaleCursor Code = "SESSION_QUERY_STALE_CURSOR"
	// CodeSourceConflict 是同一个会话 id 底下的两份观察对不上，见
	// [AssertHeadersCompatible]。
	CodeSourceConflict Code = "SESSION_QUERY_SOURCE_CONFLICT"
)

// Error 是一次带分类的会话查询失败。
//
// 源: packages/session-query/session-query/src/config.ts:39-48（SessionQueryError）
//
// errors.Is 认得它的 [Code]，errors.Unwrap 拿得到底层那条（后端报上来的
// 原始错误、或者 [session] 包的哨兵）。两条链是分开的：Code 说的是
// 「调用方该怎么处置」，Cause 说的是「底下究竟出了什么事」。
type Error struct {
	// Code 是这次失败的分类。
	Code Code
	// Message 是给人读的那句描述。
	Message string
	// Cause 是引发这次失败的底层错误；nil 表示这条失败是本包自己判出来的。
	Cause error
}

// Error 实现 error。
func (e *Error) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s：%s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s：%s：%v", e.Code, e.Message, e.Cause)
}

// Is 让 errors.Is(err, CodeXxx) 认出这次失败的分类。
func (e *Error) Is(target error) bool {
	code, ok := target.(Code)
	return ok && code == e.Code
}

// Unwrap 交出底层那条错误。
func (e *Error) Unwrap() error { return e.Cause }

// fail 造一条没有底层原因的失败。
func fail(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrap 把一条底层错误裹进本包的分类里。
func wrap(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// checkAborted 把 ctx 的取消翻成本包的 [CodeAborted]。
//
// 源: packages/session-query/session-query/src/corpus.ts 里散布的 signal?.throwIfAborted()
//
// DSH 在每个可能让出执行权的位置调一次 throwIfAborted；本包在同样那些位置
// 调这一个。翻成本包的码是有意的：调用方只需要认一套码，不必同时认
// context.Canceled 和 SESSION_QUERY_ABORTED 两种表达。原来那条留在 Cause 上。
func checkAborted(err error) error {
	if err == nil {
		return nil
	}
	return wrap(CodeAborted, err, "会话查询被取消")
}

// notFound 是「这个 id 哪儿都找不到」这句话，出现的地方太多，收成一处。
func notFound(id string) *Error {
	return fail(CodeSessionNotFound, "找不到会话 %q", id)
}

// isAborted 判断一条错误是不是取消引起的。
//
// 后端可能直接把 context.Canceled 报上来，也可能已经裹过一层，两种都算。
func isAborted(err error) bool {
	return errors.Is(err, CodeAborted) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
