// 本文件的作用：本包对外那一套稳定的失败分类、带上它的错误值，以及三个预算
// 参数和它们的补默认值规则。
//
// 源: packages/context/session-reference/src/config.ts:1-41

package sessionref

import "fmt"

// MaxReferences 是一条消息最多能引用几个来源会话，硬上限。
//
// 源: packages/context/session-reference/src/config.ts:3-4
//
// 它是硬的：配置可以往下调，调不上去。理由是每个被引用的会话都会整段快照
// 进提示词，而这些内容是**不可信**的——放开个数等于让一条用户消息把上下文
// 撑满，正常对话反而挤不进去。
const MaxReferences = 3

// DefaultCandidateLimit 是候选列表默认返回多少条。
//
// 源: packages/context/session-reference/src/config.ts:5-6
const DefaultCandidateLimit = 50

// DefaultMaxReferenceBytes 是一个来源会话渲染成 JSON 之后默认的 UTF-8 字节上限。
//
// 源: packages/context/session-reference/src/config.ts:7-8
const DefaultMaxReferenceBytes = 65536

// ErrorCode 是一次会话引用失败的稳定分类，供机器路由。
//
// 源: packages/context/session-reference/src/config.ts:20-28
//
// 新增: 和 [sessionquery.Code] 同一个写法——DSH 那边是字符串字面量联合加一个
// `SessionReferenceError extends Error`，Go 这边让 ErrorCode 自己实现 error，
// 于是 `errors.Is(err, sessionref.CodeTooMany)` 直接能用，不必先 errors.As
// 出 [Error] 再比字段。
//
// 取值保持 DSH 的原字符串一个字不改：这些码会出现在对外协议里。
type ErrorCode string

// Error 让 [ErrorCode] 自己就是一个可以被 errors.Is 认出来的哨兵。
func (c ErrorCode) Error() string { return string(c) }

// 这一套码是封闭的：本包只会报这七个里的一个。
//
// 源: packages/context/session-reference/src/config.ts:21-28
const (
	// CodeInvalidConfig 是 [Config] 里的数字不合法。
	CodeInvalidConfig ErrorCode = "SESSION_REFERENCE_INVALID_CONFIG"
	// CodeInvalidReference 是引用本身不成立：URI 不规范、字段类型不对、上限不合法。
	CodeInvalidReference ErrorCode = "SESSION_REFERENCE_INVALID_REFERENCE"
	// CodeSelfReference 是一个会话引用了它自己。
	CodeSelfReference ErrorCode = "SESSION_REFERENCE_SELF_REFERENCE"
	// CodeTooMany 是一条消息引用的来源会话超过了配置的上限。
	CodeTooMany ErrorCode = "SESSION_REFERENCE_TOO_MANY"
	// CodeReadFailed 是读某个来源会话的当前表面时失败了。
	CodeReadFailed ErrorCode = "SESSION_REFERENCE_READ_FAILED"
	// CodeBudgetExceeded 是一份快照怎么裁都塞不进配置的字节预算。
	CodeBudgetExceeded ErrorCode = "SESSION_REFERENCE_BUDGET_EXCEEDED"
	// CodeCancelled 是调用方传进来的 ctx 在准备过程中被取消了。
	CodeCancelled ErrorCode = "SESSION_REFERENCE_CANCELLED"
)

// Error 是一次带分类的会话引用失败。
//
// 源: packages/context/session-reference/src/config.ts:30-41
//
// 两条链是分开的：Code 说的是「调用方该怎么处置」，Cause 说的是
// 「底下究竟出了什么事」。
type Error struct {
	// Code 是这次失败的分类。
	Code ErrorCode
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
	code, ok := target.(ErrorCode)
	return ok && code == e.Code
}

// Unwrap 交出底层那条错误。
func (e *Error) Unwrap() error { return e.Cause }

// fail 造一条没有底层原因的失败。
func fail(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrap 把一条底层错误裹进本包的分类里。
func wrap(code ErrorCode, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// Config 是会话引用这一层的三个预算参数。
//
// 源: packages/context/session-reference/src/config.ts:10-18
//
// 新增: 三个字段都是 int，零表示「没给」，由 [Config.Resolve] 补默认值。
// DSH 那边是 `number | undefined` 加 schemastery 的 `.default()`，
// 校验（`.min(1)`、`.step(1)`）在运行期做；Go 这边整数性由类型系统保证，
// 剩下的正数与上限两条留在 Resolve 里。
type Config struct {
	// MaxReferences 是一条消息最多引用几个来源会话；零取 [MaxReferences]。
	//
	// 给了也不能超过 [MaxReferences]，超了 Resolve 报 [CodeInvalidConfig]。
	MaxReferences int
	// CandidateLimit 是候选列表默认返回多少条；零取 [DefaultCandidateLimit]。
	CandidateLimit int
	// MaxReferenceBytes 是一份来源快照渲染后的字节上限；零取 [DefaultMaxReferenceBytes]。
	MaxReferenceBytes int
}

// ResolvedConfig 是补完默认值、验过之后的那份配置，本包内部只读它。
//
// 新增: DSH 用 `Required<Config>` 表达「补完之后的样子」，Go 里用一个独立的
// 结构体——好处是构造它的唯一入口是 [Config.Resolve]，一份没验过的配置
// 在类型上就传不进来。
type ResolvedConfig struct {
	// MaxReferences 落在 1 到 [MaxReferences] 之间。
	MaxReferences int
	// CandidateLimit 是正数。
	CandidateLimit int
	// MaxReferenceBytes 是正数。
	MaxReferenceBytes int
}

// Resolve 补上默认值并校验，不合法时报 [CodeInvalidConfig]。
//
// 源: packages/context/session-reference/src/index.ts:77-105
func (c Config) Resolve() (ResolvedConfig, error) {
	resolved := ResolvedConfig{
		MaxReferences:     defaulted(c.MaxReferences, MaxReferences),
		CandidateLimit:    defaulted(c.CandidateLimit, DefaultCandidateLimit),
		MaxReferenceBytes: defaulted(c.MaxReferenceBytes, DefaultMaxReferenceBytes),
	}
	for _, item := range []struct {
		name  string
		value int
	}{
		{"maxReferences", resolved.MaxReferences},
		{"candidateLimit", resolved.CandidateLimit},
		{"maxReferenceBytes", resolved.MaxReferenceBytes},
	} {
		if item.value <= 0 {
			return ResolvedConfig{}, fail(CodeInvalidConfig, "会话引用：%s 必须是正整数", item.name)
		}
	}
	if resolved.MaxReferences > MaxReferences {
		return ResolvedConfig{}, fail(CodeInvalidConfig, "会话引用：maxReferences 不得超过 %d", MaxReferences)
	}
	return resolved, nil
}

// defaulted 把零当成「没给」补上默认值，负数原样往下传给校验去报错。
func defaulted(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
