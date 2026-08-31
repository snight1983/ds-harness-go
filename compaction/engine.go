// 本文件的作用：压缩这道接缝上、和具体后端无关的那几个词——为什么要压、
// 人工压缩会怎么失败。
//
// 源: packages/compaction/compaction/src/index.ts:24-57

package compaction

import "fmt"

// Trigger 是自动策略为什么在问后端「要不要压一次」。
//
// 源: packages/compaction/compaction/src/index.ts:25
type Trigger string

const (
	// TriggerPressure 是历史长到该压了，但还没有真的撑爆。
	TriggerPressure Trigger = "pressure"
	// TriggerContextOverflow 是上下文已经装不下了。
	TriggerContextOverflow Trigger = "context-overflow"
)

// ManualErrorCode 是一次显式的人工压缩请求可以预期的那几类失败。
//
// 源: packages/compaction/compaction/src/index.ts:28-34
type ManualErrorCode string

const (
	// ManualErrorBusy 是已经有一次压缩或者一个回合占着。
	//
	// 它是唯一一个自动压缩路径上也会报出来的：那道持久锁两条路共用。
	ManualErrorBusy ManualErrorCode = "busy"
	// ManualErrorCancelled 是这次请求在做完之前被取消了。
	ManualErrorCancelled ManualErrorCode = "cancelled"
	// ManualErrorChanged 是会话在做的过程中变了，这次的结果已经不对得上了。
	ManualErrorChanged ManualErrorCode = "changed"
	// ManualErrorSummary 是总结那一步本身失败了。
	ManualErrorSummary ManualErrorCode = "summary"
	// ManualErrorCommit 是把替换落到表面上那一步失败了。
	ManualErrorCommit ManualErrorCode = "commit"
	// ManualErrorPersistence 是持久化那一步失败了。
	ManualErrorPersistence ManualErrorCode = "persistence"
)

// ManualError 是一次人工压缩里可以预期的、能直接当作人工命令结果回给用户的失败。
//
// 源: packages/compaction/compaction/src/index.ts:41-57
//
// 新增: DSH 是一个 `extends Error` 的类，靠 `instanceof` 认。Go 这边是一个
// 普通的错误类型，调用方用 errors.As 取出 [ManualError.Code] 再分派。
// 分类留在 Code 上而不是拆成六个哨兵值，是因为这六个取值会**原样进人工命令的
// 结果**——它们是给上层照着写提示语用的一张封闭的单子，而不是六种给
// errors.Is 分派的独立失败。
type ManualError struct {
	// Code 是这次失败的分类。
	Code ManualErrorCode
	// Message 是后端给出的那句诊断，原样保留。
	Message string
	// Cause 是原本那条失败；没有就是 nil。
	Cause error
}

// Error 实现 error。
func (e *ManualError) Error() string {
	return fmt.Sprintf("compaction: 人工压缩失败（%s）：%s", e.Code, e.Message)
}

// Unwrap 交出原本那条失败，让 errors.Is 和 errors.As 能一路查下去。
func (e *ManualError) Unwrap() error { return e.Cause }

// NewManualError 造一条分了类的人工压缩失败。
func NewManualError(code ManualErrorCode, message string, cause error) *ManualError {
	return &ManualError{Code: code, Message: message, Cause: cause}
}
