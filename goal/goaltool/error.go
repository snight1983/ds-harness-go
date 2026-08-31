// 本文件的作用：本包自己那套错误码和错误类型。
//
// 源: packages/goal/tool-goal/src/authority.ts:25-27
// 源: packages/goal/tool-goal/src/index.ts:148-151
//
// 形状照抄 [ds-harness-go/sessionquery/querytool] 的 error.go：分类码本身就是一个
// error，所以 errors.Is(err, goaltool.CodeDriverRequired) 直接成立，不必先
// errors.As 出一个结构体再比字段。
//
// 新增: DSH 这些错误是 HarnessError 带一个 code 字符串。本仓库没有 HarnessError
// （见 llm/doc.go 的说明），每个包自带一套码，这里也一样。

package goaltool

import "fmt"

// Code 是本包报出去的分类码。
//
// 它自己实现 error，好让调用方用 errors.Is 比对。
type Code string

const (
	// CodeAuthorityRequired 是那句通用的「你没这个资格」。
	//
	// 源: packages/goal/tool-goal/src/authority.ts:25
	//
	// 两处用它：一次没有直接人类回合的 create/edit/pause/resume，以及一次
	// 既没有人也不在当前准入轮次里的 complete/blocked。
	CodeAuthorityRequired Code = "GOAL_TOOL_AUTHORITY_REQUIRED"
	// CodeAgentRequired 是这次调用根本没落在任何一个 agent 上。
	//
	// 源: packages/goal/tool-goal/src/authority.ts:53
	CodeAgentRequired Code = "GOAL_TOOL_AGENT_REQUIRED"
	// CodeDriverRequired 是调用方不是注册表里那个确切的活 agent，或者它不在
	// 自己那个还开着的驱动回合里。
	//
	// 源: packages/goal/tool-goal/src/authority.ts:35,41,59
	CodeDriverRequired Code = "GOAL_TOOL_DRIVER_REQUIRED"
	// CodeInvalidUpdate 是这次 update_goal 的参数自相矛盾：ref 不合规，或者
	// 某个字段配不上这个动作。
	//
	// 源: packages/goal/tool-goal/src/index.ts:150,267,277,289,293,297
	CodeInvalidUpdate Code = "GOAL_TOOL_INVALID_UPDATE"
	// CodeBlockThreshold 是一次自动轮次里的 blocked 还没熬够部署方规定的轮数。
	//
	// 源: packages/goal/tool-goal/src/index.ts:304
	CodeBlockThreshold Code = "GOAL_TOOL_BLOCK_THRESHOLD"
)

// Error 让 [Code] 本身就能当错误用。
func (c Code) Error() string { return string(c) }

// Error 是本包报出去的错误，带一句给模型看的话。
//
// 那句话是**英文**的：它会原样进到模型的上下文里，和 DSH 逐字相同。本包写给
// 运维看的话（装配失败那几条）另走 [fmt.Errorf]，那些是中文。
type Error struct {
	// Code 是分类码。
	Code Code
	// Message 是那句给模型看的话。
	Message string
}

// Error 交出那句话。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Is 让 errors.Is(err, CodeDriverRequired) 这种写法成立。
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
