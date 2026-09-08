// 本文件的作用：这条缝对外那一套稳定的失败分类，以及带上它的错误值。
//
// 源: packages/credentials/authorization/src/index.ts:61-84（AuthorizationError、AuthorizationDeclinedError）

package authorization

import "fmt"

// Code 是一次授权失败的稳定分类，供机器路由。
//
// 源: packages/credentials/authorization/src/index.ts:62-67
//
// 新增: DSH 那边是 `AuthorizationError extends HarnessError`，码是构造时传进去的
// 一个字符串。Go 这边分派靠 errors.Is，所以 Code 自己就实现 error：
// `errors.Is(err, authorization.CodeNoFlow)` 直接能用，不必先 errors.As 出 [Error]
// 再比字段。写法与
// [github.com/snight1983/ds-harness-go/feature/sessionquery.Code] 逐字相同。
type Code string

// Error 让 [Code] 自己就是一个可以被 errors.Is 认出来的哨兵。
func (c Code) Error() string { return string(c) }

// 这一套码是封闭的：本包只会报这十一个里的一个。
//
// 前六个的字符串是 DSH 那六个码原样照录（index.ts:200,279,284,288,425,82）：
// 它们描述的是同一件事，换个写法只会让两边的日志对不上。后五个是本包自有的，
// 因为它们说的事 DSH 那个单进程实现里不存在。
const (
	// CodeDuplicateFlow 是这个凭据键上已经登记过一条流程了。
	//
	// 源: packages/credentials/authorization/src/index.ts:206
	CodeDuplicateFlow Code = "DUPLICATE_FLOW"
	// CodeNoFlow 是没有任何一条流程认领这个凭据键。
	//
	// 源: packages/credentials/authorization/src/index.ts:279
	CodeNoFlow Code = "NO_FLOW"
	// CodeUnknownMethod 是点名的方式不在这条流程提供的那几种里。
	//
	// 源: packages/credentials/authorization/src/index.ts:284
	CodeUnknownMethod Code = "UNKNOWN_METHOD"
	// CodeAlreadyInFlight 是这个凭据键上有一次尝试正攥在某个副本手里。
	//
	// 源: packages/credentials/authorization/src/index.ts:288
	CodeAlreadyInFlight Code = "ALREADY_IN_FLIGHT"
	// CodeNotCommitted 是流程跑完了，但这次尝试里没观察到凭据记录被写下去。
	//
	// 源: packages/credentials/authorization/src/index.ts:425,431
	CodeNotCommitted Code = "NOT_COMMITTED"
	// CodeDeclined 是人拒绝了那个问题——不是界面坏了。
	//
	// 源: packages/credentials/authorization/src/index.ts:82
	//
	// 它由 [Interaction.Prompt] 的实现方报上来，本包不产生它，只认它：
	// 认出来之后这次尝试结算成 [StatusCancelled]，而不是一次失败。
	CodeDeclined Code = "DECLINED"

	// CodeStalled 是这个凭据键上停着一次没结算的尝试，而没有副本攥着它。
	//
	// 新增: 这是本包独有的第三种状态，DSH 没有——它的尝试活在一个 Promise 里，
	// 进程没了尝试就跟着没了，只剩「在跑」和「不在」两种。落到介质上之后就多出
	// 「记录还在、跑它的那个进程没了」这一种，而它既不是忙也不是闲：
	// 起一次新的会把上一次攒下的进度扔掉，所以 [Service.Begin] 拒掉它，
	// 让调用方明确选 [Service.Resume] 还是 [Service.Cancel]。
	CodeStalled Code = "STALLED"
	// CodeNoAttempt 是要接手的那次尝试不在介质上，或者身份对不上。
	//
	// 新增: 身份对不上单独算一种失败而不是当成「不在」：一次已经被别人接手并且
	// 重新起过的尝试，和一个根本没存在过的 id，处置是不一样的。
	CodeNoAttempt Code = "NO_ATTEMPT"
	// CodeTakenOver 是跑到一半发现这次尝试已经不归本副本了。
	//
	// 新增: 租约过期之后别的副本可以接手（见 [Attempt.HeldUntil]）。本副本这时候
	// 必须停手，而且**不许结算**——那条记录已经是别人的了。
	CodeTakenOver Code = "TAKEN_OVER"
	// CodeInvalidConfig 是装配这条缝或者这次请求时给的东西不合法。
	CodeInvalidConfig Code = "INVALID_CONFIG"
	// CodeStoreFailed 是尝试记录读不出来或者写不下去。
	//
	// 新增: 介质失败在 DSH 那边不存在——它的单飞槽位是一个内存 Map。
	CodeStoreFailed Code = "STORE_FAILED"
)

// Error 是一次带分类的授权失败。
//
// 源: packages/credentials/authorization/src/index.ts:62-67
//
// errors.Is 认得它的 [Code]，errors.Unwrap 拿得到底层那条。两条链是分开的：
// Code 说的是「调用方该怎么处置」，Cause 说的是「底下究竟出了什么事」。
type Error struct {
	// Code 是这次失败的分类。
	Code Code
	// Message 是给人读的那句话。
	Message string
	// Cause 是底下那个错；没有就是 nil。
	Cause error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("authorization: %s（%s）：%v", e.Message, e.Code, e.Cause)
	}
	return fmt.Sprintf("authorization: %s（%s）", e.Message, e.Code)
}

// Is 让 errors.Is(err, CodeNoFlow) 这种写法成立。
func (e *Error) Is(target error) bool {
	code, ok := target.(Code)
	return ok && code == e.Code
}

// Unwrap 交出底下那条，让 errors.Is 能一路穿到后端自己的码上去。
func (e *Error) Unwrap() error { return e.Cause }

// newError 造一条不带底层原因的失败。
func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrapError 造一条带底层原因的失败。
func wrapError(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// Declined 是 [Interaction.Prompt] 用来说「人拒绝了」的那个错。
//
// 源: packages/credentials/authorization/src/index.ts:79-84（AuthorizationDeclinedError）
//
// **只有人说不才许用它**：一个被流程自己撤掉的问题（比如一次竞速里输掉的那一问）
// 必须报别的错，否则之后一次真的故障会被读成一次拒绝。
func Declined(message string) error {
	if message == "" {
		message = "这个授权问题被拒绝了"
	}
	return newError(CodeDeclined, "%s", message)
}
