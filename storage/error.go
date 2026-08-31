// 本文件的作用：存储中枢和它下面所有后端共用的那套失败词汇。
//
// 源: packages/storage/storage/src/error.ts:1-4

package storage

import "fmt"

// ErrorCode 是**封闭**的失败词汇。
//
// 源: packages/storage/storage/src/error.ts:6-14
//
// 封闭是重点：调用方要照着它分派，而分派一个封闭集合可以逐个列全，
// 分派一段自由文本只能靠字符串匹配——那种匹配会在有人改了一句文案之后静默失配。
//
// 「后端不提供某种数据形态」不在这个词汇里，和 DSH 一致：那件事由
// [KV] 的第二个返回值回答，不是一个错误码。理由见 [KV] 的注释。
type ErrorCode string

const (
	// CodeBackendNotFound 表示这个名字底下没有注册过后端。
	CodeBackendNotFound ErrorCode = "backend-not-found"
	// CodeFormNotMounted 表示这个数据形态还没挂上来。
	CodeFormNotMounted ErrorCode = "form-not-mounted"
	// CodeDuplicateBackend 表示这个后端名字已经被占了。
	CodeDuplicateBackend ErrorCode = "duplicate-backend"
	// CodeDuplicateMount 表示这个数据形态已经挂过一次了。
	CodeDuplicateMount ErrorCode = "duplicate-mount"
	// CodeVersionMismatch 表示介质上盖着的版本号和这次要开的对不上。
	CodeVersionMismatch ErrorCode = "version-mismatch"
	// CodeMalformedMedium 表示介质解析不出这个单元该有的形状。
	CodeMalformedMedium ErrorCode = "malformed-medium"
	// CodeClosed 表示这个单元或后端已经关掉了。
	CodeClosed ErrorCode = "closed"
)

// Error 是中枢和后端实现共同抛出的、带类型的失败。
//
// 源: packages/storage/storage/src/error.ts:16-35
//
// 调用方用 errors.As 拿到它，**照着 Code 分派**，不要去匹配 Message 里的字。
type Error struct {
	// Code 是稳定的失败类别，也是唯一该被分派的东西。
	Code ErrorCode
	// Message 是给人看的诊断细节。
	Message string
	// Err 是底层原因，可以为 nil。
	//
	// 对应 DSH 的 ErrorOptions.cause。它只供日志和排查使用：分派一律看 Code，
	// 因为底层错误在不同后端上不是同一个东西（一个是文件系统的 errno，
	// 另一个是 SQLite 的错误码）。
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("storage: %s（%s）", e.Message, e.Code)
}

// Unwrap 让 errors.Is / errors.As 能问到底层原因。
func (e *Error) Unwrap() error { return e.Err }

// newError 是包内建错误的快捷方式，省掉每处都写一遍结构体字面量。
func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
