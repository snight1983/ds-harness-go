// 本文件的作用：对外那几个记录类型——主机选中的来源、候选列表的条目、
// 准备好的那条消息，以及落进会话日志的那份持久来源。
//
// 源: packages/context/session-reference/src/types.ts:1-78

package sessionref

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// Name 是这一层的产出方名字，落在消息来源里。
//
// 源: packages/context/session-reference/src/types.ts:14
const Name = "session-reference"

// sourceForm 是这份来源自称的信息形态。
//
// 源: packages/context/session-reference/src/types.ts:16
//
// recall 的意思是「从另一次会话的日志里捞出来的材料，可能在进来的路上被缩过」
// （见 [llm.FormRecall]），这正是本包干的事，所以它是个常数。
const sourceForm = "recall"

// sourceVersion 是这份来源的形状版本。
//
// 源: packages/context/session-reference/src/types.ts:17
const sourceVersion = 1

// Reference 是一次引用在持久来源里留下的那条账。
//
// 源: packages/context/session-reference/src/types.ts:18-29
//
// 它记的是**当时**的事实，不是现在去重算的：被引用的会话之后还会继续长，
// 而这条账要回答的是「模型那时候看见的是什么」。
type Reference struct {
	// SessionID 是被引用的那个来源会话。
	SessionID string `json:"sessionId"`
	// Label 是渲染进快照的那个显示名。
	Label string `json:"label"`
	// CapturedThroughSeq 是这次观察吃进的最大原始日志 seq。
	//
	// 新增: DSH 是 `number | null`，空日志给 null。Go 这边配一个 CapturedAny
	// 布尔，理由和 [sessionquery.SurfaceSnapshot] 那处逐字相同——seq 0 是一条
	// 真事件的合法序号，拿 0 当「没有」会撞车。
	CapturedThroughSeq int `json:"-"`
	// CapturedAny 为假表示那次观察到的是一份空日志，CapturedThroughSeq 无意义。
	CapturedAny bool `json:"-"`
	// Compacted 表示那份日志里出现过压缩检查点。
	Compacted bool `json:"compacted"`
	// OriginalMessages 是投影出来、还没裁之前有多少条消息。
	OriginalMessages int `json:"originalMessages"`
	// RetainedMessages 是裁完之后还剩多少条。
	RetainedMessages int `json:"retainedMessages"`
	// OmittedMessages 是被整条丢掉了多少条。
	OmittedMessages int `json:"omittedMessages"`
	// OmittedBytes 是丢掉的和截断掉的加起来有多少 UTF-8 字节。
	OmittedBytes int `json:"omittedBytes"`
	// Truncated 表示这次引用不是原样进去的。
	Truncated bool `json:"truncated"`
	// InputIndex 是它在这条消息的引用列表里排第几，从零开始。
	InputIndex int `json:"inputIndex"`
}

// referenceJSON 是 [Reference] 落到线上的形状，字段名和 DSH 逐字相同。
//
// 单独一个类型是为了把 CapturedThroughSeq 那对字段折回 DSH 的
// `number | null`：Go 的结构体标签表达不了「两个字段编成一个可空的值」。
type referenceJSON struct {
	SessionID          string `json:"sessionId"`
	Label              string `json:"label"`
	CapturedThroughSeq *int   `json:"capturedThroughSeq"`
	Compacted          bool   `json:"compacted"`
	OriginalMessages   int    `json:"originalMessages"`
	RetainedMessages   int    `json:"retainedMessages"`
	OmittedMessages    int    `json:"omittedMessages"`
	OmittedBytes       int    `json:"omittedBytes"`
	Truncated          bool   `json:"truncated"`
	InputIndex         int    `json:"inputIndex"`
}

// MarshalJSON 把这条账编成 DSH 那份形状。
func (r Reference) MarshalJSON() ([]byte, error) {
	wire := referenceJSON{
		SessionID:        r.SessionID,
		Label:            r.Label,
		Compacted:        r.Compacted,
		OriginalMessages: r.OriginalMessages,
		RetainedMessages: r.RetainedMessages,
		OmittedMessages:  r.OmittedMessages,
		OmittedBytes:     r.OmittedBytes,
		Truncated:        r.Truncated,
		InputIndex:       r.InputIndex,
	}
	if r.CapturedAny {
		seq := r.CapturedThroughSeq
		wire.CapturedThroughSeq = &seq
	}
	return json.Marshal(wire)
}

// UnmarshalJSON 把一条账读回来。
func (r *Reference) UnmarshalJSON(data []byte) error {
	var wire referenceJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("会话引用：这条引用账读不回来：%w", err)
	}
	*r = Reference{
		SessionID:        wire.SessionID,
		Label:            wire.Label,
		Compacted:        wire.Compacted,
		OriginalMessages: wire.OriginalMessages,
		RetainedMessages: wire.RetainedMessages,
		OmittedMessages:  wire.OmittedMessages,
		OmittedBytes:     wire.OmittedBytes,
		Truncated:        wire.Truncated,
		InputIndex:       wire.InputIndex,
	}
	if wire.CapturedThroughSeq != nil {
		r.CapturedThroughSeq, r.CapturedAny = *wire.CapturedThroughSeq, true
	}
	return nil
}

// Source 是一条注入上下文的持久事实：它引用了哪些会话、各自被缩成了什么样。
//
// 源: packages/context/session-reference/src/types.ts:12-30（SessionReferenceSource）
//
// 新增: 和 [instructions.Source] 同一条路子——DSH 用 TypeScript 的声明合并把
// `'session-reference'` 挂进 `MessageSourceMap`，Go 的 [llm.MessageSource]
// 是封闭接口（理由见 llm 的包文档），插件挂不进去。这里给出一个普通结构体
// 加它的 JSON 编解码，再靠 [llm.UnknownSource] 这个封闭联合**留出来的那个
// 口子**原样携带它。
type Source struct {
	// References 是这条消息携带的那些引用，按引用顺序。
	References []Reference
}

// sourceJSON 是 [Source] 落到线上的形状，字段名和 DSH 逐字相同。
type sourceJSON struct {
	Kind       string      `json:"kind"`
	Form       string      `json:"form"`
	Version    int         `json:"version"`
	References []Reference `json:"references"`
}

// MarshalJSON 让 [Source] 编成 DSH 那份形状。
func (s Source) MarshalJSON() ([]byte, error) {
	references := s.References
	if references == nil {
		// references 在 DSH 那边是必填数组。编成 null 的话，读回来时
		// 「一个引用都没有」和「这个字段坏了」就长得一样了。
		references = []Reference{}
	}
	return json.Marshal(sourceJSON{
		Kind:       Name,
		Form:       sourceForm,
		Version:    sourceVersion,
		References: references,
	})
}

// UnmarshalJSON 把一份来源读回来。
//
// 和 [instructions.Source] 一样宽进：这些字节来自一份**已经写下的**会话日志，
// 可能是别的版本写的。读不懂的那条引用逐条丢掉，而不是整份拒绝——整份拒绝会让
// 一次本来能续上的会话丢掉全部引用记录。
func (s *Source) UnmarshalJSON(data []byte) error {
	var raw struct {
		Kind       string            `json:"kind"`
		References []json.RawMessage `json:"references"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("会话引用：来源不是一个 JSON 对象：%w", err)
	}
	if raw.Kind != Name {
		return fmt.Errorf("会话引用：来源的 kind 是 %q，不是 %q", raw.Kind, Name)
	}
	references := make([]Reference, 0, len(raw.References))
	for _, entry := range raw.References {
		var reference Reference
		if err := json.Unmarshal(entry, &reference); err != nil {
			continue
		}
		references = append(references, reference)
	}
	s.References = references
	return nil
}

// MessageSource 把这份来源包成一个 [llm.MessageSource]。
//
// 源: packages/context/session-reference/src/index.ts:269-280
func (s Source) MessageSource() (llm.MessageSource, error) {
	encoded, err := json.Marshal(s)
	if err != nil {
		// [Source] 里只有 string、bool、int 和结构体切片，编码不可能失败。
		// 留着这条的理由和 [instructions.Source.MessageSource] 逐字相同。
		return nil, fmt.Errorf("会话引用：编码来源失败：%w", err)
	}
	return llm.UnknownSource{Kind: llm.SourceKind(Name), Raw: encoded}, nil
}

// ParseSource 从一个消息来源里把这份状态读回来；不是这一层产出的就返回 false。
func ParseSource(source llm.MessageSource) (Source, bool) {
	unknown, ok := source.(llm.UnknownSource)
	if !ok || unknown.Kind != llm.SourceKind(Name) {
		return Source{}, false
	}
	var parsed Source
	if err := json.Unmarshal(unknown.Raw, &parsed); err != nil {
		return Source{}, false
	}
	return parsed, true
}

// Input 是主机选中的一个来源会话。
//
// 源: packages/context/session-reference/src/types.ts:38-44（SessionReferenceInput）
type Input struct {
	// SessionID 是那个来源会话的不透明身份。
	SessionID session.SessionID
	// Label 是给人看的显示名；空串表示没给，由本包补成会话 id。
	Label string
}

// Candidate 是候选列表里的一条，全部来自会话元数据。
//
// 源: packages/context/session-reference/src/types.ts:47-57
type Candidate struct {
	// SessionID 是那个来源会话的不透明身份。
	SessionID session.SessionID
	// Label 是日志里折出来的最新标题，没有标题时退回会话 id。
	Label string
	// Cwd 是那个会话的工作目录；空串表示日志里没记。
	Cwd string
	// CreatedAt 是那个会话的建立时间，Unix 纪元毫秒。
	CreatedAt int64
}

// MentionCandidate 是一条候选，外加主机要插进输入框的那段规范提及。
//
// 源: packages/context/session-reference/src/types.ts:64-68（SessionReferenceMentionCandidate）
type MentionCandidate struct {
	Candidate
	// Mention 是 `@[label](dsh-session:…)` 这段规范提及。
	Mention string
}

// PreparedMessage 是准备完之后的产物：正文，加上可选的那条引用上下文。
//
// 源: packages/context/session-reference/src/types.ts:70-76（PreparedReferencedMessage）
type PreparedMessage struct {
	// Content 是去掉主机提及记号之后、可读的那份正文。
	Content llm.Content
	// AdditionalContext 是把全部引用聚在一起的那条不可信快照消息。
	AdditionalContext llm.Message
	// HasContext 为假表示这条消息一个引用都没有，AdditionalContext 无意义。
	//
	// 新增: DSH 用可选字段表达「没有」。Go 的 [llm.Message] 零值是一条
	// 没有 id、没有角色的消息，看上去像一条真消息，所以配一个显式的布尔——
	// 少了它，调用方会把零值消息当成一条要投递的上下文发出去。
	HasContext bool
}

// ConversationItem 是投影出来的一条纯文本对话行。
//
// 源: packages/context/session-reference/src/types.ts:78-84（ReferencedConversationItem）
type ConversationItem struct {
	// Role 是原消息的角色，只有 user 和 assistant 两种。
	Role llm.Role `json:"role"`
	// Text 是从那条消息里留下来的可见文本。
	Text string `json:"text"`
}
