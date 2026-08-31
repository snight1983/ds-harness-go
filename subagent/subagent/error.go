// 本文件的作用：这条接缝自己那种带码的失败，以及它实际用到的那些码。
//
// 源: packages/subagent/subagent/src/error.ts

package subagent

import (
	"errors"

	"ds-harness-go/llm"
)

// ErrInvalidRequest 是「调用方给的东西本身不成立」。
//
// 新增: DSH 这一类失败抛的是光的 TypeError（比如 depth.ts 里那两处），没有码——
// 它们是编程错误，不是上游要分情况处理的运行期结局，所以下面那些码一个都不占。
// Go 没有 TypeError，用一个哨兵错误顶上，好让调用方仍然 [errors.Is] 得出来。
var ErrInvalidRequest = errors.New("subagent: 请求不成立")

// NewError 造一条子 agent 接缝的带码失败。cause 可以为 nil。
//
// 源: packages/subagent/subagent/src/error.ts:10-15
//
// 新增: DSH 那边 SubagentError 是 HarnessError 的子类，只为把 `name` 改成
// 'SubagentError'——那个字段在 JS 里是用来认错误来源的。Go 认错误靠
// [errors.Is] / [errors.As] 和那个码，多派生一个类型只会让上游那句
// `errors.As(err, &target)`（target 是 *llm.Error）失效。所以这里不新造类型，
// 直接交 [ds-harness-go/llm.Error]，身份由码承担。
func NewError(message, code string, cause error) *llm.Error {
	return llm.NewError(message, code, cause)
}

// 本包抛出去的那些码，一字不差照搬 DSH。它们跟着错误一路走到工具层，
// 是上游唯一分得清的东西。
//
// 源: packages/subagent/subagent/src/index.ts、continuation.ts 各处 throw 点
const (
	// CodeNoProvider 是「点名的那个提供方没登记过」。
	CodeNoProvider = "NO_PROVIDER"
	// CodeDuplicateProvider 是「这个提供方名字已经有人占了」。
	CodeDuplicateProvider = "DUPLICATE_PROVIDER"
	// CodeUnsupportedCapability 是「请求要一样这个提供方没有的开工期能力」。
	CodeUnsupportedCapability = "UNSUPPORTED_CAPABILITY"
	// CodeCancelled 是「在发布之前被取消了」。
	CodeCancelled = "CANCELLED"
	// CodeDuplicateChild 是「调用方点名的那个 childId 已经被占了」。
	CodeDuplicateChild = "DUPLICATE_CHILD"
	// CodeNotResumable 是「这个孩子接不上——不是可续的，或者它的日志重建不出来」。
	CodeNotResumable = "NOT_RESUMABLE"
	// CodeUnauthorized 是「这个调用方没有资格对这个目标下手」。
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeParentUnavailable 是「那个确切的活父 agent 不在注册表里」。
	CodeParentUnavailable = "PARENT_UNAVAILABLE"
	// CodePersistenceUnavailable 是「这一步要会话持久化，而它没装」。
	CodePersistenceUnavailable = "PERSISTENCE_UNAVAILABLE"
	// CodeContinuationUnavailable 是「这一步要续接管理器，而它没装」。
	CodeContinuationUnavailable = "CONTINUATION_UNAVAILABLE"
	// CodeDraining 是「这一支已经关了准入，正在排干」。
	CodeDraining = "DRAINING"
	// CodeActivationClosing 是「这个孩子的活化正在收摊，不再收活」。
	CodeActivationClosing = "ACTIVATION_CLOSING"
	// CodeActivationSetupRevoked 是「一份可选装配在用的时候已经被撤了」。
	CodeActivationSetupRevoked = "ACTIVATION_SETUP_REVOKED"
	// CodeActivationSetupReleaseFailed 是「撤一份可选装配时它自己失败了」。
	CodeActivationSetupReleaseFailed = "ACTIVATION_SETUP_RELEASE_FAILED"
	// CodeActivationTeardownFailed 是「拆一次活化时它自己失败了」。
	CodeActivationTeardownFailed = "ACTIVATION_TEARDOWN_FAILED"
	// CodeControlProjectionsUnavailable 是「只读列举要投影注册表，而它没装」。
	//
	// 源: packages/subagent/subagent/src/list-children.ts:191-196
	CodeControlProjectionsUnavailable = "SUBAGENT_CONTROL_PROJECTIONS_UNAVAILABLE"
	// CodeControlSessionStoreUnavailable 是「只读列举要活会话表，而它没装」。
	//
	// 源: packages/subagent/subagent/src/list-children.ts:201-206
	CodeControlSessionStoreUnavailable = "SUBAGENT_CONTROL_SESSION_STORE_UNAVAILABLE"
)
