// 本文件的作用：域这一层自己的失败词汇。
//
// 源: packages/storage/storage-domain/src/error.ts:1-4

package domain

import "fmt"

// ErrorCode 是**封闭**的失败词汇，调用方照着它分派。
//
// 源: packages/storage/storage-domain/src/error.ts:6-12
//
// 后端的失败（backend-not-found、version-mismatch、malformed-medium……）
// **原样穿过去**，仍然是 *storage.Error，域这一层不重新包一遍。
// 重包会把「介质出了什么事」这个信息压成一句转述，而排查那类问题靠的正是原始那句。
type ErrorCode string

const (
	// CodeAlreadyOpen 表示这个域名已经开着了。
	CodeAlreadyOpen ErrorCode = "already-open"
	// CodeFacetUnsupported 表示路由到的后端不提供键值形态。
	CodeFacetUnsupported ErrorCode = "facet-unsupported"
	// CodeInvalidRecord 表示介质上存着的某条记录过不了这个域声明的校验。
	CodeInvalidRecord ErrorCode = "invalid-record"
	// CodeMissingKey 表示要改的那条记录不存在。
	CodeMissingKey ErrorCode = "missing-key"
	// CodeClosed 表示这个域已经关了。
	CodeClosed ErrorCode = "closed"
)

// RecordSlot 指出是哪一条记录没过校验。
//
// 源: packages/storage/storage-domain/src/error.ts:14-20
//
// 全局单例槽用两个空串表示，和变更事件里的约定是同一套（见 [Changed]）。
type RecordSlot struct {
	// Table 是出问题那条记录所在的表；全局槽为空串。
	Table string
	// Key 是出问题那条记录的键；全局槽为空串。
	Key string
}

// Error 是域这一层抛出的、带类型的失败。
//
// 源: packages/storage/storage-domain/src/error.ts:28-53
//
// 调用方用 errors.As 拿到它，**照着 Code 分派**，不要去匹配 Message 里的字。
type Error struct {
	// Code 是稳定的失败类别，也是唯一该被分派的东西。
	Code ErrorCode
	// Message 是给人看的诊断细节。
	Message string
	// Slot 只在 Code 是 [CodeInvalidRecord] 时非 nil。
	Slot *RecordSlot
	// Err 是底层原因，可以为 nil。
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("storage/domain: %s（%s）", e.Message, e.Code)
}

// Unwrap 让 errors.Is / errors.As 能问到底层原因。
func (e *Error) Unwrap() error { return e.Err }

// newError 是包内建错误的快捷方式。
func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// invalidRecord 把一次校验失败包成带位置的 [CodeInvalidRecord]。
//
// 源: packages/storage/storage-domain/src/index.ts:180-192
//
// 位置既进结构化字段也进那句话：结构化字段给程序，那句话给日志里那一行。
func invalidRecord(domainName, table, key string, cause error) *Error {
	slot := fmt.Sprintf("表 %q 里的记录 %q", table, key)
	if table == "" {
		slot = "全局值"
	}
	return &Error{
		Code:    CodeInvalidRecord,
		Message: fmt.Sprintf("域 %q：介质上存着的%s过不了它声明的校验", domainName, slot),
		Slot:    &RecordSlot{Table: table, Key: key},
		Err:     cause,
	}
}
