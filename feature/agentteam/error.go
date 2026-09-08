// 本文件的作用：这个包对外那一套稳定的失败分类，以及带上它的错误值。
//
// 源: packages/experimental/agent-team/src/error.ts（TeamError、errorMessage）

package agentteam

import (
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// PackageName 是本包在不变量注册表上的名字。
const PackageName = "feature/agentteam"

// Code 是一次团队操作失败的稳定分类。
//
// 源: packages/experimental/agent-team/src/error.ts:6-11（TeamError）
//
// 新增: DSH 那边是 `TeamError extends HarnessError`，码是构造时传进去的一个字符串。
// Go 这边分派靠 errors.Is，所以 Code 自己就实现 error：
// `errors.Is(err, agentteam.CodeTaskBlocked)` 直接能用，不必先 errors.As 出 [Error]
// 再比字段。写法与
// [github.com/snight1983/ds-harness-go/feature/authorization.Code] 逐字相同。
type Code string

// Error 让 [Code] 自己就是一个可以被 errors.Is 认出来的哨兵。
func (c Code) Error() string { return string(c) }

// 这一套码里，前二十四个的字符串是 DSH 那二十四个码原样照录：它们要经模型侧的工具
// 结果回到模型眼前，换个写法只会让两边的排障记录对不上。后三个是本包自有的，
// 因为它们说的事在 DSH 那个单进程实现里不存在。
const (
	// CodeInvalidConfig 是装配这个包时给的东西不合法。
	//
	// 源: packages/experimental/agent-team/src/index.ts:53
	CodeInvalidConfig Code = "TEAM_INVALID_CONFIG"
	// CodeInvalidArgument 是一次请求里的某个字段不合法：空的、超长的、或者这个动作
	// 要求的字段没给。
	//
	// 源: packages/experimental/agent-team/src/validation.ts:14,16
	CodeInvalidArgument Code = "TEAM_INVALID_ARGUMENT"
	// CodeInvalidMemberName 是队友的名字不合式：不是小写连字符那一套，或者叫了 "lead"。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:455
	CodeInvalidMemberName Code = "TEAM_INVALID_MEMBER_NAME"
	// CodeInvalidWriteScope 是任务上挂的写入范围不是一条干净的相对路径。
	//
	// 源: packages/experimental/agent-team/src/validation.ts:31
	CodeInvalidWriteScope Code = "TEAM_INVALID_WRITE_SCOPE"
	// CodeInvalidTarget 是消息的收件人指向了一个说不通的对象。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:208
	CodeInvalidTarget Code = "TEAM_INVALID_TARGET"
	// CodeInvalidTimeout 是等待时长不在允许的区间里。
	//
	// 源: packages/experimental/agent-team/src/activity.ts:24
	CodeInvalidTimeout Code = "TEAM_INVALID_TIMEOUT"

	// CodeMemberNotFound 是点名的队友不在花名册上，或者还没转成在岗。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:51
	CodeMemberNotFound Code = "TEAM_MEMBER_NOT_FOUND"
	// CodeMemberNameTaken 是这个名字已经有队友占着了。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:271
	CodeMemberNameTaken Code = "TEAM_MEMBER_NAME_TAKEN"
	// CodeMemberLimit 是这支团队的人数到顶了。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:274
	CodeMemberLimit Code = "TEAM_MEMBER_LIMIT"
	// CodeNotMember 是发起这次操作的身份不属于这支团队。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:81
	CodeNotMember Code = "TEAM_NOT_MEMBER"
	// CodeLeadRequired 是这个动作只有队长做得了。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:205,251、task-board.ts:177
	CodeLeadRequired Code = "TEAM_LEAD_REQUIRED"
	// CodeProvisioningConflict 是同一个队友的开工结果被两条路各写了一次，而且写的
	// 不是同一件事。
	//
	// 源: packages/experimental/agent-team/src/roster.ts:304,325,353,470
	CodeProvisioningConflict Code = "TEAM_PROVISIONING_CONFLICT"

	// CodeTaskNotFound 是点名的任务不在板上。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:85,118,230
	CodeTaskNotFound Code = "TEAM_TASK_NOT_FOUND"
	// CodeTaskStaleRevision 是请求里带的那个版本号和板上那条对不上。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:122
	//
	// **它和 [github.com/snight1983/ds-harness-go/storage.CodeStaleRevision] 是两回事**，
	// 名字必须分开：这一条说的是「你手里那份任务已经被别人改过了，重读再来」，是给
	// 模型看的；那一条说的是介质上的记录版本对不上，只在 [Board] 那次条件写内部使用，
	// 从不出现在工具结果里。两层版本号的分工见 [Task.Rev]。
	CodeTaskStaleRevision Code = "TEAM_TASK_STALE_REVISION"
	// CodeTaskDeleted 是这条任务已经删了，不再接受改动。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:125
	CodeTaskDeleted Code = "TEAM_TASK_DELETED"
	// CodeTaskAlreadyClaimed 是这条任务已经被别人认领了。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:135
	CodeTaskAlreadyClaimed Code = "TEAM_TASK_ALREADY_CLAIMED"
	// CodeTaskBlocked 是这条任务还有没做完的前置。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:138,188
	CodeTaskBlocked Code = "TEAM_TASK_BLOCKED"
	// CodeTaskInvalidTransition 是这条任务的当前状态走不到请求要的那个状态。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:144,168,173,181
	CodeTaskInvalidTransition Code = "TEAM_TASK_INVALID_TRANSITION"
	// CodeTaskHasDependents 是还有别的任务挡在这条后面，删不得。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:198
	CodeTaskHasDependents Code = "TEAM_TASK_HAS_DEPENDENTS"
	// CodeTaskDependencyCycle 是这次改动会让任务的前置关系绕成一个圈。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:28,226
	CodeTaskDependencyCycle Code = "TEAM_TASK_DEPENDENCY_CYCLE"
	// CodeTaskUnauthorized 是改这条任务要它的认领人或者队长的身份。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:129
	CodeTaskUnauthorized Code = "TEAM_TASK_UNAUTHORIZED"
	// CodeTaskLimit 是这块板上的任务数到顶了。
	//
	// 源: packages/experimental/agent-team/src/task-board.ts:54,58
	CodeTaskLimit Code = "TEAM_TASK_LIMIT"

	// CodeSelfMessage 是有人给自己发消息。
	//
	// 源: packages/experimental/agent-team/src/mailbox.ts:121
	CodeSelfMessage Code = "TEAM_SELF_MESSAGE"
	// CodeMailboxFull 是这个收件人手里压着的消息到顶了。
	//
	// 源: packages/experimental/agent-team/src/mailbox.ts:127
	CodeMailboxFull Code = "TEAM_MAILBOX_FULL"
	// CodeMessageTooLarge 是这条消息的正文超出了单条上限。
	//
	// 源: packages/experimental/agent-team/src/mailbox.ts:139
	CodeMessageTooLarge Code = "TEAM_MESSAGE_TOO_LARGE"

	// CodeWaitAborted 是那次等待被调用方撤掉了。
	//
	// 源: packages/experimental/agent-team/src/activity.ts:50
	CodeWaitAborted Code = "TEAM_WAIT_ABORTED"
	// CodeDisposed 是这支团队的运行期已经收摊了。
	//
	// 源: packages/experimental/agent-team/src/lifecycle.ts:36,46
	CodeDisposed Code = "TEAM_DISPOSED"
	// CodeDisposalTimeout 是收摊的时候有东西没在限期内停下来。
	//
	// 源: packages/experimental/agent-team/src/lifecycle.ts:77
	CodeDisposalTimeout Code = "TEAM_DISPOSAL_TIMEOUT"

	// CodeBoardContended 是这块板改的人太多，条件写在重试上限之内没抢赢。
	//
	// 新增: DSH 没有这一条——它的板活在一个进程的内存里，串行化靠一条 Promise 链。
	// 本包把整块板做成介质上的一条记录，于是「两个副本同时改板」变成一次真的条件写
	// 竞争，而 [github.com/snight1983/ds-harness-go/storage/domain.Table.Update]
	// 的重试次数是有上限的。撞穿之后必须报这一条而**不是** [CodeTaskStaleRevision]：
	// 后者是「你的版本号过期了，重读」，而这一条是「板太挤，等一下再来」，
	// 两者的处置完全不同。
	CodeBoardContended Code = "TEAM_BOARD_CONTENDED"
	// CodeStoreFailed 是团队状态读不出来或者写不下去。
	//
	// 新增: 介质失败在 DSH 那边不存在——它的团队状态是一份内存投影。
	CodeStoreFailed Code = "TEAM_STORE_FAILED"
	// CodeTeamNotFound 是这个团队身份在介质上没有对应的记录。
	//
	// 新增: DSH 的团队是「Lead Session 加一份从它日志折出来的投影」，Session 在团队
	// 就在。本包的团队是介质上一条独立记录，于是「记录不在」成了一种要单独说的失败。
	CodeTeamNotFound Code = "TEAM_NOT_FOUND"
)

// Error 是一次带分类的团队失败。
//
// 源: packages/experimental/agent-team/src/error.ts:6-11
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
		return fmt.Sprintf("agentteam: %s（%s）：%v", e.Message, e.Code, e.Cause)
	}
	return fmt.Sprintf("agentteam: %s（%s）", e.Message, e.Code)
}

// Is 让 errors.Is(err, CodeTaskBlocked) 这种写法成立。
func (e *Error) Is(target error) bool {
	code, ok := target.(Code)
	return ok && code == e.Code
}

// Unwrap 交出底下那条，让 errors.Is 能一路穿到介质自己的码上去。
func (e *Error) Unwrap() error { return e.Cause }

// newError 造一条不带底层原因的失败。
func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrapError 造一条带底层原因的失败。
func wrapError(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// storeError 把一条介质错译成本包的分类。
//
// 新增: 分成三档而不是一律 [CodeStoreFailed]，是因为它们的处置不一样：键不在是
// 调用方给错了身份，抢不赢是让它退避重来，其余才是真的介质坏了。
func storeError(err error, format string, args ...any) error {
	switch {
	case err == nil:
		return nil
	case isDomainCode(err, domain.CodeMissingKey):
		return wrapError(CodeTeamNotFound, err, format, args...)
	case isStorageCode(err, storage.CodeStaleRevision):
		return wrapError(CodeBoardContended, err, format, args...)
	default:
		return wrapError(CodeStoreFailed, err, format, args...)
	}
}

// isStorageCode 说这条错是不是存储层的某个码。
//
// 新增: 存储层的码是普通字符串，不实现 error，所以认它要先 errors.As 出
// [github.com/snight1983/ds-harness-go/storage.Error] 再比字段——这一点和本包
// 自己那套 [Code] 不一样，容易在改动里写错，所以收成一个私有帮手。
func isStorageCode(err error, code storage.ErrorCode) bool {
	var storeErr *storage.Error
	return errors.As(err, &storeErr) && storeErr.Code == code
}

// isDomainCode 说这条错是不是域层的某个码。理由同 [isStorageCode]。
func isDomainCode(err error, code domain.ErrorCode) bool {
	var domainErr *domain.Error
	return errors.As(err, &domainErr) && domainErr.Code == code
}
