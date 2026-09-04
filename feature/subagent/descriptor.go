// 本文件的作用：那份耐久的子 agent 描述符——带版本、不给模型看的 `subagent/descriptor`
// 会话事件。每一个有会话的子 agent 都由它认出来，它同时记下这个孩子是一次性的
// 还是可续的；可续的那种额外把冷恢复要用的那份声明出来的组装保存下来。
// 提供方在孩子的第一个回合里、回合之内追加它。
//
// 源: packages/subagent/subagent/src/descriptor.ts
//
// 这份描述符**有意**逐个字段地拍快照，而不是把可合并扩展的那个 AgentOptions 整个存下来：
// 一个不相干的扩展值不该仅仅因为自己排不成 JSON 就把续接搞挂，而多认一个组装输入
// 必须是一次有意的 [DescriptorVersion] 变更。它不收 subagentDepth——冷恢复认持久
// 会话头里那个 DelegationDepth 当单调下界；也不收 OutputSchema，那是**一次活化**的
// 结果契约，不是这个孩子耐久的组装。MaxTokens 这类按次活化的旋钮出于同样的理由不收：
// 它们预算的是一次活化。冷恢复要拿确切的活父 agent 做授权，但重建孩子的选项时只读
// 这份耐久的描述符，所以它既不恢复上次那份预算、也不继承父此刻那份；生效的是被恢复
// 那条路线自己的默认值。

package subagent

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// EventDescriptor 是那条耐久的子 agent 身份事件。
//
// 源: packages/subagent/subagent/src/descriptor.ts:29-41
//
// 由立起这个孩子的那个提供方在它第一个回合之内、第一次请求之前追加**恰好一条**。
// 它**只进日志**：不带 SurfaceOp、不进模型历史、也熬得过压缩。
const EventDescriptor sessionlog.EventType = "subagent/descriptor"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: DSH 靠 `declare module` 把它合并进 SessionEventMap。Go 没有声明合并，
// [sessionlog.Vocabulary] 是个闭合的值，所以由本包交出这张单子、装配方自己拼
// （成例见 [github.com/snight1983/ds-harness-go/feature/plan/planmode.EventTypes]）：
//
//	vocabulary := sessionlog.CoreVocabulary().With(subagent.EventTypes()...)
//
// 不拼的话，一段子 agent 的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventDescriptor}
}

// DescriptorVersion 是当下这份描述符的格式版本，盖进每一条追加出去的
// [EventDescriptor]，且被 [FoldDescriptor] 一字不差地要求。
//
// 源: packages/subagent/subagent/src/descriptor.ts:42-48（SUBAGENT_DESCRIPTOR_VERSION）
//
// 多认一个组装输入是一次有意的版本变更，绝不是悄悄多一个字段。
const DescriptorVersion = 2

// DescriptorMode 说的是这个孩子是一次性的运行，还是一场可恢复的对话。
//
// 源: packages/subagent/subagent/src/descriptor.ts:56
type DescriptorMode string

const (
	// ModeOneShot 是一个跑完就接不上的、有会话的子 agent。
	ModeOneShot DescriptorMode = "one-shot"
	// ModeContinuable 是一个声明出来的组装支持冷恢复的子 agent。
	ModeContinuable DescriptorMode = "continuable"
)

// DescriptorData 是那份支持得了的耐久子 agent 身份与可选的续接组装。
//
// 源: packages/subagent/subagent/src/descriptor.ts:60-107
//
// 新增: DSH 是 OneShotSubagentDescriptorData | ContinuableSubagentDescriptorData
// 两支按 mode 判别的联合，两支的差别只在「label 可不可选」加上四个只有可续那支才有
// 的字段。Go 没有判别联合，做成两个结构体会让每一个读者都得先类型断言一次才能读
// version 和 provider。所以这里是**一个**结构体加一个 Mode 字段，两支的差别由
// [DescriptorData.Validate] 守着——它就是 DSH 那两处 assertKnownKeys 在 Go 里的样子。
type DescriptorData struct {
	// Version 是描述符的格式版本（[DescriptorVersion]）。
	Version int `json:"version"`
	// Mode 说的是这个孩子是一次性的运行还是一场可恢复的对话。
	Mode DescriptorMode `json:"mode"`
	// Provider 是立起这个孩子的那个提供方名字。
	Provider string `json:"provider"`
	// Label 是最初那次派发的短 description，留作这个孩子耐久的创建名，好让列举
	// 认得出这场对话，而不必回放父的工具结果、也不必把孩子的提示词露出来。
	// ModeOneShot 时它可以是空串（不给）；ModeContinuable 时它必须非空。
	Label string `json:"label,omitempty"`
	// AgentProvider 是解算出来的孩子 AgentOptions.Provider；空串表示没声明。
	// 只有 ModeContinuable 能带它。
	AgentProvider string `json:"agentProvider,omitempty"`
	// AgentModel 是解算出来的孩子 AgentOptions.Model；空串表示没声明。
	// 只有 ModeContinuable 能带它。
	AgentModel string `json:"agentModel,omitempty"`
	// Persona 是恢复时盖掉部署人设的那份单孩子人设；空串表示不换。
	// 只有 ModeContinuable 能带它。
	Persona string `json:"persona,omitempty"`
	// ToolFilter 是恢复时重新施加的孩子工具范围；nil 表示不过滤。
	// 只有 ModeContinuable 能带它。
	ToolFilter *tools.Restriction `json:"toolFilter,omitempty"`
}

// Validate 验一份描述符符不符合它那个 Mode 的完整声明。
//
// 源: packages/subagent/subagent/src/descriptor.ts:136-142, 190-233
//
// 新增: DSH 那边这件事分成两处——快照那一侧靠 TypeScript 的联合类型在编译期挡住
// 越界字段，读回来那一侧靠 assertKnownKeys 在运行期挡。Go 这边两侧共用一个结构体，
// 所以两侧共用这一个方法：一次性那支带了可续才有的字段，和可续那支缺了 Label，
// 都是同一类「这份记录和它自称的模式对不上」。
func (d DescriptorData) Validate() error {
	if d.Version != DescriptorVersion {
		return fmt.Errorf("%w：描述符版本必须是 %d，实际 %d", ErrInvalidRequest, DescriptorVersion, d.Version)
	}
	if d.Provider == "" {
		return fmt.Errorf("%w：描述符必须带提供方名字", ErrInvalidRequest)
	}
	switch d.Mode {
	case ModeOneShot:
		if d.AgentProvider != "" || d.AgentModel != "" || d.Persona != "" || d.ToolFilter != nil {
			return fmt.Errorf("%w：一次性描述符不许带续接组装（agentProvider／agentModel／persona／toolFilter）", ErrInvalidRequest)
		}
	case ModeContinuable:
		if d.Label == "" {
			return fmt.Errorf("%w：可续描述符必须带一个非空的 label", ErrInvalidRequest)
		}
		if d.ToolFilter != nil && len(d.ToolFilter.Allow) == 0 && len(d.ToolFilter.Deny) == 0 {
			return fmt.Errorf("%w：描述符的 toolFilter 至少要声明 allow 或者 deny 之一", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w：描述符的 mode 只能是 %q 或者 %q，实际 %q",
			ErrInvalidRequest, ModeOneShot, ModeContinuable, d.Mode)
	}
	return nil
}

// SnapshotDescriptor 验一份描述符输入、并把它脱钩成那份耐久负载，在任何提供方的活
// 开工之前。
//
// 源: packages/subagent/subagent/src/descriptor.ts:244-283
//
// 它落的是会话日志自己那道「脱钩的无损 JSON」边界，只是提早落——好让一次同步的校验
// 失败当场把这次工具调用拒掉，而不是先建出一个孩子来再失败。
//
// 新增: DSH 的 snapshotSubagentDescriptor 有两个重载（一次性一个、可续一个），
// 因为它要靠重载把「一次性不许给 agentProvider」写进类型。Go 这边是一个函数加
// [DescriptorData.Validate]；调用方自己填 Version（或者留 0 让这里补上）。
// 脱钩那一步在 Go 里就是 [encoding/json.Marshal] 再解回来——成例见
// [github.com/snight1983/ds-harness-go/sessionlog] 的 doc.go 里那条关于 snapshotJsonValue 的说明。
func SnapshotDescriptor(input DescriptorData) (DescriptorData, error) {
	if input.Version == 0 {
		input.Version = DescriptorVersion
	}
	if err := input.Validate(); err != nil {
		return DescriptorData{}, err
	}
	// 走不到（下面两支）：这份结构只有整数、字符串和一个 *tools.Restriction，
	// 排得出来也读得回去。留着是因为「脱钩」这条约束靠的正是这一来一回，把它
	// 断言成不会失败，日后往结构里添一个排不出去的字段时就没有人报警了。
	encoded, err := json.Marshal(input)
	if err != nil {
		return DescriptorData{}, fmt.Errorf("%w：描述符排不成无损 JSON：%w", ErrInvalidRequest, err)
	}
	var snapshot DescriptorData
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return DescriptorData{}, fmt.Errorf("%w：描述符读不回来：%w", ErrInvalidRequest, err)
	}
	return snapshot, nil
}

// FoldDescriptor 把一份持久的孩子日志折成它那份支持得了的描述符。
//
// 源: packages/subagent/subagent/src/descriptor.ts:305-323（foldSubagentDescriptor）
//
// **第一条** [EventDescriptor] 说了算——立起孩子的那个提供方只追加一条，所以后来
// 一条同类型的事件改不动那份声明出来的组装。
//
// 日志上一条都没有、或者它的版本不是 [DescriptorVersion] 时交回 found 为假
// （这个运行时分类不了这个孩子）。一份当前版本的持久负载和它那份完整的声明对不上时报错。
func FoldDescriptor(events []sessionlog.Event) (data DescriptorData, found bool, err error) {
	for _, event := range events {
		if event.Type != EventDescriptor {
			continue
		}
		// 版本先看，且**宽松**地看：一份别的版本的记录带着这一版没有的字段是正常的，
		// 那种记录该被判成「分类不了」，而不是判成坏记录。次序和 DSH 一样。
		var versioned struct {
			Version *int `json:"version"`
		}
		if unmarshalErr := json.Unmarshal(event.Data, &versioned); unmarshalErr != nil {
			return DescriptorData{}, false, fmt.Errorf("%w：持久的子 agent 描述符读不出来：%w", ErrInvalidRequest, unmarshalErr)
		}
		if versioned.Version == nil {
			return DescriptorData{}, false, fmt.Errorf("%w：持久的子 agent 描述符必须带一个数字 version", ErrInvalidRequest)
		}
		if *versioned.Version != DescriptorVersion {
			return DescriptorData{}, false, nil
		}
		// 新增: DSH 用 assertKnownKeys 逐个键地挡越界字段。Go 这边就是解码器上的
		// DisallowUnknownFields，它连 ToolFilter 那层嵌套一并管住——那正是 DSH 另外
		// 用一张 TOOL_FILTER_KEYS 表守的东西。
		decoder := json.NewDecoder(bytes.NewReader(event.Data))
		decoder.DisallowUnknownFields()
		var candidate DescriptorData
		if decodeErr := decoder.Decode(&candidate); decodeErr != nil {
			return DescriptorData{}, false, fmt.Errorf("%w：持久的子 agent 描述符读不出来：%w", ErrInvalidRequest, decodeErr)
		}
		if validateErr := candidate.Validate(); validateErr != nil {
			return DescriptorData{}, false, validateErr
		}
		return candidate, true, nil
	}
	return DescriptorData{}, false, nil
}
