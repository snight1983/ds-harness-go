// 本文件的作用：这个包的两种失败——一种是日志坏了，一种是模型给的规则立不住，
// 以及它们为什么必须分开。
//
// 源: packages/schedule/schedule/src/domain.ts:41-86

package schedule

// LogError 是耐久数据本身坏了：字节解不动，或者这一串改动接不上。
//
// 源: packages/schedule/schedule/src/domain.ts:41-53
//
// 它的 Reason 是**给运维看的**，所以是中文：模型那一侧永远只会看到
// [CodeCorruptLog] 那一个码加上一句固定的英文（见 tools.go 里的 corruptLogError），
// 具体是哪一条不变量破了不外露——那是一句只有读日志的人才用得上的话。
type LogError struct {
	// Reason 是破掉的那条不变量。
	Reason string
}

// Code 是这种失败在那份封闭错误码表里的位置。
func (e *LogError) Code() ErrorCode { return CodeCorruptLog }

// Error 让它成为一个 error。
func (e *LogError) Error() string { return e.Reason }

// InputError 是模型给的规则立不住，成不了一条记录。
//
// 源: packages/schedule/schedule/src/domain.ts:55-86
//
// 和 [LogError] 相反，它的 Message 是**给模型看的**，所以是英文，而且逐字照抄
// DSH：这句话会原样变成工具结果里的 `message` 字段，是模型据以改写下一次调用的
// 唯一线索。Code 只可能是那六个和输入有关的码，不会是 [CodeCorruptLog] 这类。
type InputError struct {
	// Code 是那六个输入码之一。
	Code ErrorCode
	// Message 是给模型看的那句话。
	Message string
	// cause 是被包住的实现细节，比如时区加载失败。
	//
	// 不导出：它是**诊断**，不是这次失败面向模型的那一部分，露出来会诱使调用方
	// 把它拼进给模型的话里。要看它走 [InputError.Unwrap]。
	cause error
}

// Error 让它成为一个 error。
func (e *InputError) Error() string { return e.Message }

// Unwrap 交出被包住的那个原因，没有就是 nil。
func (e *InputError) Unwrap() error { return e.cause }

// newInputError 造一个不带原因的输入失败。
func newInputError(code ErrorCode, message string) *InputError {
	return &InputError{Code: code, Message: message}
}

// wrapInputError 造一个带原因的输入失败。
func wrapInputError(code ErrorCode, message string, cause error) *InputError {
	return &InputError{Code: code, Message: message, cause: cause}
}
