// 本文件的作用：本包对外那一套稳定的失败分类，以及带上它的错误值。
//
// 源: packages/workspace/workspace/src/index.ts:45-64
// 源: packages/workspace/workspace/src/entity.ts:19-27

package workspace

import "fmt"

// Code 是一次工作区操作失败的稳定分类，供机器路由。
//
// 新增: DSH 那边只有三个具名错误类（WorkspaceUnknownSessionError、
// WorkspaceOrderInvalidError、WorkspaceMoveInvalidError），其余十几处失败
// 全是裸的 `throw new Error(...)`，调用方只能靠 instanceof 分出那三个，
// 剩下的分不出来。
//
// 本包给每一处失败都配一个码，原因是 Go 里没有那个「分不出来但至少还有个基类」
// 的中间态：一个裸 error 在 Go 里只剩下 Error() 那串文字，而按文字分派是本仓库
// 明令禁止的（见 [Error]）。给码的代价是多写十行常量，换掉的是「调用方要么什么都
// 分不出来、要么去匹配一句中文」。
//
// 这套码是本包新定的，不是 DSH 的线上契约，所以取值用本仓库的写法而不是抄谁。
type Code string

// Error 让 [Code] 自己就是一个可以被 errors.Is 认出来的哨兵。
func (c Code) Error() string { return string(c) }

// 这一套码是封闭的：本包只会报这九个里的一个。
const (
	// CodeInvalidConfig 是 [Config] 里少了必需的依赖，或者填了不合法的值。
	CodeInvalidConfig Code = "WORKSPACE_INVALID_CONFIG"
	// CodeNotStarted 是登记册还没 [Open]（或者已经 [Registry.Close] 了）就被使用。
	//
	// 源: packages/workspace/workspace/src/index.ts:634,639
	CodeNotStarted Code = "WORKSPACE_NOT_STARTED"
	// CodeInvalidPath 是建工作区的那条路径解析不出来、不存在、或者不是一个目录。
	//
	// 源: packages/workspace/workspace/src/index.ts:160-162
	CodeInvalidPath Code = "WORKSPACE_INVALID_PATH"
	// CodeAttachRejected 是要挂上来的会话过不了工作目录验证。
	//
	// 源: packages/workspace/workspace/src/entity.ts:116-143
	CodeAttachRejected Code = "WORKSPACE_ATTACH_REJECTED"
	// CodeMoveInvalid 是会话挪位时点名了一个不在账目里的会话或锚点。
	//
	// 源: packages/workspace/workspace/src/entity.ts:19-27（WorkspaceMoveInvalidError）
	CodeMoveInvalid Code = "WORKSPACE_MOVE_INVALID"
	// CodeOrderInvalid 是工作区挪位时点名了一个不在落盘次序里的工作区。
	//
	// 源: packages/workspace/workspace/src/index.ts:56-64（WorkspaceOrderInvalidError）
	CodeOrderInvalid Code = "WORKSPACE_ORDER_INVALID"
	// CodeUnknownSession 是归档时点名了一个既不活着、也不在持久化里的会话。
	//
	// 源: packages/workspace/workspace/src/index.ts:45-53（WorkspaceUnknownSessionError）
	//
	// 它**只表示一次确凿的没有**：持久化后端自己出故障时报的是那条原始错误，
	// 绝不塌成这个码——否则一次磁盘掉线会被上层当成「这个会话不存在」。
	CodeUnknownSession Code = "WORKSPACE_UNKNOWN_SESSION"
	// CodeInconsistentState 是介质上的次序和表对不上，且没有任何待恢复标记解释得了。
	//
	// 源: packages/workspace/workspace/src/index.ts:413-416,510-551
	//
	// 这条一律**报错而不修**：能被解释的分叉由 [PendingMutation] 明确点名，
	// 剩下的分叉说明有人绕过了本包去写这个域，而猜一次「大概本来该是什么样」
	// 会把一次可查的事故变成一份看上去正常的坏数据。
	CodeInconsistentState Code = "WORKSPACE_INCONSISTENT_STATE"
	// CodeStorageFailed 是域、文件系统、或者持久化后端自己失败了。
	//
	// 底层那条错误留在 [Error.Cause] 上，排查看它。
	CodeStorageFailed Code = "WORKSPACE_STORAGE_FAILED"
)

// Error 是一次带分类的工作区操作失败。
//
// 源: packages/workspace/workspace/src/index.ts:45-64
//
// errors.Is 认得它的 [Code]，errors.Unwrap 拿得到底层那条。两条链是分开的：
// Code 说的是「调用方该怎么处置」，Cause 说的是「底下究竟出了什么事」。
// **不要**去匹配 Message 里的字。
type Error struct {
	// Code 是这次失败的分类。
	Code Code
	// Message 是给人读的那句描述。
	Message string
	// Cause 是引发这次失败的底层错误；nil 表示这条失败是本包自己判出来的。
	//
	// 新增: 一次回滚也失败的场合，这里挂的是 errors.Join 出来的那一条，
	// 顶替 DSH 的 AggregateError（index.ts:321-325 等六处）。两者是同一件事：
	// 把「原本的失败」和「善后也失败了」一起交出去，谁都不许被另一个盖掉。
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
