// 本文件的作用：一个回合是怎么结束的——结束理由，以及「被取消」时的具体来路。
//
// 源: packages/core/session/src/types.ts:236-337

package session

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
)

// TurnEndReasonKind 是回合结束理由的判别标签。
type TurnEndReasonKind string

const (
	// ReasonCompleted 是模型自己收的尾，没有再要工具。
	ReasonCompleted TurnEndReasonKind = "completed"
	// ReasonAborted 是取消，具体来路在 [AbortedTurnEnd.Reason] 上。
	ReasonAborted TurnEndReasonKind = "aborted"
	// ReasonBlocked 是这个回合一个步骤都没进就被拦下了。
	ReasonBlocked TurnEndReasonKind = "blocked"
	// ReasonError 是提供方或适配器报了错。
	ReasonError TurnEndReasonKind = "error"
	// ReasonMaxTokens 是输出撞上了长度上限。
	ReasonMaxTokens TurnEndReasonKind = "max-tokens"
	// ReasonInterrupted 是进程在这个回合中途死了，由收尾补出来的。
	//
	// 它只出现在 [InterruptedTurnClosers] 的产物里，活着的循环不会写它。
	ReasonInterrupted TurnEndReasonKind = "interrupted"
)

// TurnEndReason 说明一个回合为什么结束。
//
// 源: packages/core/session/src/types.ts:171-172（TurnEndReason）
//
// 这个联合是**开放**的：DSH 那边 TurnEndReasonMap 是一个可被插件合并扩展的
// 映射，本包只登记核心的六个，读到别的落进 [UnknownTurnEnd] 原样保管。
type TurnEndReason interface {
	// TurnEndReasonKind 是这个理由的判别标签。
	TurnEndReasonKind() TurnEndReasonKind

	// sealedTurnEndReason 把实现方封在本包内。
	sealedTurnEndReason()
}

// CompletedTurnEnd 是模型自己收的尾。
type CompletedTurnEnd struct{}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (CompletedTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonCompleted }

func (CompletedTurnEnd) sealedTurnEndReason() {}

// MarshalJSON 排出这个理由。
func (CompletedTurnEnd) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(ReasonCompleted)})
}

// AbortedTurnEnd 是这个回合被取消了。
type AbortedTurnEnd struct {
	// Reason 是取消的具体来路。
	Reason TurnEndCancelCause
}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (AbortedTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonAborted }

func (AbortedTurnEnd) sealedTurnEndReason() {}

// abortedWire 是取消理由在介质上的样子。
type abortedWire struct {
	Kind   TurnEndReasonKind `json:"kind"`
	Reason json.RawMessage   `json:"reason"`
}

// MarshalJSON 排出这个理由。
func (r AbortedTurnEnd) MarshalJSON() ([]byte, error) {
	if r.Reason == nil {
		return nil, fmt.Errorf("%w：取消的回合必须带一个取消原因", ErrMalformedValue)
	}
	cause, err := json.Marshal(r.Reason)
	if err != nil {
		return nil, fmt.Errorf("%w：取消原因排不出去：%w", ErrMalformedValue, err)
	}
	return json.Marshal(abortedWire{Kind: ReasonAborted, Reason: cause})
}

// BlockedTurnEnd 是这个回合一个步骤都没进就被拦下了。
type BlockedTurnEnd struct{}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (BlockedTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonBlocked }

func (BlockedTurnEnd) sealedTurnEndReason() {}

// MarshalJSON 排出这个理由。
func (BlockedTurnEnd) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(ReasonBlocked)})
}

// ErrorTurnEnd 是提供方或适配器报了错。
type ErrorTurnEnd struct {
	// Error 是那次失败。
	Error llm.Failure
}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (ErrorTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonError }

func (ErrorTurnEnd) sealedTurnEndReason() {}

// errorWire 是失败理由在介质上的样子。
type errorWire struct {
	Kind  TurnEndReasonKind `json:"kind"`
	Error llm.Failure       `json:"error"`
}

// MarshalJSON 排出这个理由。
func (r ErrorTurnEnd) MarshalJSON() ([]byte, error) {
	return json.Marshal(errorWire{Kind: ReasonError, Error: r.Error})
}

// MaxTokensTurnEnd 是输出撞上了长度上限。
type MaxTokensTurnEnd struct{}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (MaxTokensTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonMaxTokens }

func (MaxTokensTurnEnd) sealedTurnEndReason() {}

// MarshalJSON 排出这个理由。
func (MaxTokensTurnEnd) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(ReasonMaxTokens)})
}

// InterruptedTurnEnd 是进程在这个回合中途死了。
type InterruptedTurnEnd struct{}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (InterruptedTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return ReasonInterrupted }

func (InterruptedTurnEnd) sealedTurnEndReason() {}

// MarshalJSON 排出这个理由。
func (InterruptedTurnEnd) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(ReasonInterrupted)})
}

// UnknownTurnEnd 收着一个本构建没登记的结束理由，**原样**保管它的字节。
//
// 新增: DSH 那边 TurnEndReasonMap 是可被插件合并扩展的，一个只认识核心六种的
// 读者照样要把别人写的理由完整地排回去——把它解成「未知」再排回去时丢字段，
// 等于替写的一方决定它那部分意思不重要。
type UnknownTurnEnd struct {
	// Kind 是那个本构建不认识的判别标签。
	Kind TurnEndReasonKind
	// Raw 是这个理由完整的原始字节。
	Raw json.RawMessage
}

// TurnEndReasonKind 实现 [TurnEndReason]。
func (r UnknownTurnEnd) TurnEndReasonKind() TurnEndReasonKind { return r.Kind }

func (UnknownTurnEnd) sealedTurnEndReason() {}

// MarshalJSON 把原始字节原样送回去。
func (r UnknownTurnEnd) MarshalJSON() ([]byte, error) {
	if len(r.Raw) == 0 {
		return nil, fmt.Errorf("%w：未知的结束理由没有原始字节可排", ErrMalformedValue)
	}
	return append(json.RawMessage(nil), r.Raw...), nil
}

// UnmarshalTurnEndReason 把一段字节读回一个结束理由。
//
// 源: packages/core/session/src/types.ts:296-311
func UnmarshalTurnEndReason(data []byte) (TurnEndReason, error) {
	var probe struct {
		Kind TurnEndReasonKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("%w：结束理由不是一个带 kind 的对象：%w", ErrMalformedValue, err)
	}

	switch probe.Kind {
	case ReasonCompleted:
		return CompletedTurnEnd{}, nil
	case ReasonBlocked:
		return BlockedTurnEnd{}, nil
	case ReasonMaxTokens:
		return MaxTokensTurnEnd{}, nil
	case ReasonInterrupted:
		return InterruptedTurnEnd{}, nil
	case ReasonAborted:
		var wire abortedWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：取消的结束理由读不回来：%w", ErrMalformedValue, err)
		}
		if wire.Reason == nil {
			return nil, fmt.Errorf("%w：取消的回合必须带一个取消原因", ErrMalformedValue)
		}
		cause, err := UnmarshalTurnEndCancelCause(wire.Reason)
		if err != nil {
			return nil, err
		}
		return AbortedTurnEnd{Reason: cause}, nil
	case ReasonError:
		var wire errorWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：失败的结束理由读不回来：%w", ErrMalformedValue, err)
		}
		return ErrorTurnEnd{Error: wire.Error}, nil
	default:
		if probe.Kind == "" {
			return nil, fmt.Errorf("%w：结束理由没有 kind", ErrMalformedValue)
		}
		return UnknownTurnEnd{
			Kind: probe.Kind,
			Raw:  append(json.RawMessage(nil), data...),
		}, nil
	}
}

// CancelCauseKind 是取消原因的判别标签。
type CancelCauseKind string

const (
	// CancelUser 是使用者自己按的停。
	CancelUser CancelCauseKind = "user"
	// CancelParent 是上级 agent 停了它派下来的这一支。
	CancelParent CancelCauseKind = "parent"
	// CancelHook 是某个钩子拦下的，原因在 [HookCancel.Reason] 上。
	CancelHook CancelCauseKind = "hook"
	// CancelDisposed 是承载这个回合的东西被销毁了。
	CancelDisposed CancelCauseKind = "disposed"
	// CancelLegacy 是一条旧日志里没记来路的取消。
	//
	// 源: packages/core/session/src/types.ts:293
	//
	// 只会从日志里读出来，产出方不该再写它。
	CancelLegacy CancelCauseKind = "legacy"
)

// TurnEndCancelCause 是一次取消的具体来路。
//
// 源: packages/core/session/src/types.ts:137-142（AgentCancelCause）
//
// 这个联合是**封闭**的：不认识的标签返回 [ErrUnknownCancelCause]，不留 Unknown
// 变体。理由写在 [ErrUnknownCancelCause] 上——DSH 那边 AgentCancelCause 是一个
// 普通联合类型，插件加不进去，真要新增一个取消原因按 [FormatVersion] 的规矩
// 得进位，版本检查会先一步拦住。
type TurnEndCancelCause interface {
	// CancelCauseKind 是这个原因的判别标签。
	CancelCauseKind() CancelCauseKind

	// sealedCancelCause 把实现方封在本包内。
	sealedCancelCause()
}

// UserCancel 是使用者自己按的停。
type UserCancel struct{}

// CancelCauseKind 实现 [TurnEndCancelCause]。
func (UserCancel) CancelCauseKind() CancelCauseKind { return CancelUser }

func (UserCancel) sealedCancelCause() {}

// MarshalJSON 排出这个原因。
func (UserCancel) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(CancelUser)})
}

// ParentCancel 是上级 agent 停了它派下来的这一支。
type ParentCancel struct{}

// CancelCauseKind 实现 [TurnEndCancelCause]。
func (ParentCancel) CancelCauseKind() CancelCauseKind { return CancelParent }

func (ParentCancel) sealedCancelCause() {}

// MarshalJSON 排出这个原因。
func (ParentCancel) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(CancelParent)})
}

// HookCancel 是某个钩子拦下的。
type HookCancel struct {
	// Reason 是那个钩子给的说明，面向模型，原样保留英文。
	Reason string
}

// CancelCauseKind 实现 [TurnEndCancelCause]。
func (HookCancel) CancelCauseKind() CancelCauseKind { return CancelHook }

func (HookCancel) sealedCancelCause() {}

// hookCancelWire 是钩子取消在介质上的样子。
type hookCancelWire struct {
	Kind   CancelCauseKind `json:"kind"`
	Reason string          `json:"reason"`
}

// MarshalJSON 排出这个原因。
func (c HookCancel) MarshalJSON() ([]byte, error) {
	return json.Marshal(hookCancelWire{Kind: CancelHook, Reason: c.Reason})
}

// DisposedCancel 是承载这个回合的东西被销毁了。
type DisposedCancel struct{}

// CancelCauseKind 实现 [TurnEndCancelCause]。
func (DisposedCancel) CancelCauseKind() CancelCauseKind { return CancelDisposed }

func (DisposedCancel) sealedCancelCause() {}

// MarshalJSON 排出这个原因。
func (DisposedCancel) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(CancelDisposed)})
}

// LegacyCancel 是一条旧日志里没记来路的取消。
type LegacyCancel struct{}

// CancelCauseKind 实现 [TurnEndCancelCause]。
func (LegacyCancel) CancelCauseKind() CancelCauseKind { return CancelLegacy }

func (LegacyCancel) sealedCancelCause() {}

// MarshalJSON 排出这个原因。
func (LegacyCancel) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"kind": string(CancelLegacy)})
}

// UnmarshalTurnEndCancelCause 把一段字节读回一个取消原因。
//
// 源: packages/core/session/src/types.ts:277-294
func UnmarshalTurnEndCancelCause(data []byte) (TurnEndCancelCause, error) {
	var probe struct {
		Kind CancelCauseKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("%w：取消原因不是一个带 kind 的对象：%w", ErrMalformedValue, err)
	}

	switch probe.Kind {
	case CancelUser:
		return UserCancel{}, nil
	case CancelParent:
		return ParentCancel{}, nil
	case CancelDisposed:
		return DisposedCancel{}, nil
	case CancelLegacy:
		return LegacyCancel{}, nil
	case CancelHook:
		var wire hookCancelWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：钩子取消原因读不回来：%w", ErrMalformedValue, err)
		}
		return HookCancel{Reason: wire.Reason}, nil
	default:
		return nil, fmt.Errorf("%w：%q", ErrUnknownCancelCause, probe.Kind)
	}
}
