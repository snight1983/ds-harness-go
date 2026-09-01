// 本文件的作用：本包自己那套错误码和错误类型。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:14-17,88-93,138-143
//
// 形状照抄 [github.com/snight1983/ds-harness-go/sessionquery] 的 error.go：分类码本身就是一个
// error，所以 errors.Is(err, querytool.CodeUnauthorized) 直接成立，不必先
// errors.As 出一个结构体再比字段。理由和那边一样，见
// [github.com/snight1983/ds-harness-go/sessionquery.Code]。
//
// 新增: DSH 这些错误是 HarnessError 的子类。本仓库没有 HarnessError（见
// llm/doc.go 的说明），每个包自带一套码，这里也一样。

package querytool

import (
	"errors"
	"fmt"
)

// Code 是本包报出去的分类码。
//
// 它自己实现 error，好让调用方用 errors.Is 比对。
type Code string

const (
	// CodeUnauthorized 是目标会话落在调用方工作区之外。
	//
	// 源: packages/session-query/tool-session-query/src/service-boundary.ts:90
	//
	// 「不在这个工作区」和「根本没有这个会话」共用这一个码，而且共用同一句话：
	// 分开报等于告诉模型别的工作区里有哪些会话存在。
	CodeUnauthorized Code = "SESSION_QUERY_TOOL_UNAUTHORIZED"
	// CodeMissingAgent 是这次调用没有绑定 agent，因而没有调用方会话可言。
	//
	// 源: packages/session-query/tool-session-query/src/workspace-access.ts
	CodeMissingAgent Code = "SESSION_QUERY_TOOL_MISSING_AGENT"
	// CodeNoCurrentStep 是当前会话上还没有过步骤边界。
	//
	// 源: packages/session-query/tool-session-query/src/operations.ts
	//
	// 在自己这个会话里检索时，可见范围要卡在当前这一步开始之前；日志上一条
	// step/start 都没有的时候那条线画不出来，只能拒。
	CodeNoCurrentStep Code = "SESSION_QUERY_TOOL_NO_CURRENT_STEP"
	// CodeFailed 是那句通用的失败。
	//
	// 源: packages/session-query/tool-session-query/src/service-boundary.ts:139-142
	//
	// 引擎那些说的是装配和后端内情的码一律塌到这里，见 [safeFailures]。
	CodeFailed Code = "SESSION_QUERY_TOOL_FAILED"
)

// Error 让 [Code] 本身就能当错误用。
func (c Code) Error() string { return string(c) }

// Error 是本包报出去的错误，带一句给模型看的话。
type Error struct {
	// Code 是分类码。
	Code Code
	// Message 是那句给模型看的话，绝不含内情。
	Message string
}

// Error 交出那句话。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Is 让 errors.Is(err, CodeUnauthorized) 这种写法成立。
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	code, ok := target.(Code)
	return ok && code == e.Code
}

// fail 造一条带码的错误。
func fail(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// codeOf 取一条错误上的本包分类码；不是本包的错误交出空串。
func codeOf(err error) Code {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
