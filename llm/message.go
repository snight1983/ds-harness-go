// 本文件的作用：消息这个值、它的来源联合、以及造一条消息的那几个构造函数。
//
// 源: packages/llm/llm/src/message.ts:1-241

package llm

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Role 是与提供方无关的会话角色。
//
// 源: packages/llm/llm/src/message.ts:133
type Role string

const (
	// RoleSystem 是系统提示。
	RoleSystem Role = "system"
	// RoleUser 是用户侧的话，工具结果也走这个角色。
	RoleUser Role = "user"
	// RoleAssistant 是模型产出的回答。
	RoleAssistant Role = "assistant"
)

// Provenance 是一条助手消息的提供方／模型身份，加上适配器私有的重放数据。
//
// 源: packages/llm/llm/src/message.ts:7-19
type Provenance struct {
	// Provider 是产出这条消息的提供方路由。
	Provider string
	// Model 是产出这条消息的提供方模型标识。
	Model string
	// ReplayState 是重放这次提供方响应所需的、无损的适配器私有状态。
	//
	// 新增: DSH 那边是 unknown。这里用 json.RawMessage，理由和本仓库
	// credentials.GrantRecord.Payload 那条一样：「不透明」在这里可以做到字面
	// 意义上的不透明——不必解码就能存取，往返逐字节精确。解成 map[string]any
	// 再排回去的话，大整数会被 float64 磨掉精度、键的顺序会变，而这是别人用来
	// 重放一次响应的状态，磨掉一位数字之后它就不再是那次响应了。
	ReplayState json.RawMessage
}

// SourceKind 是消息来源的判别标签。
//
// 源: packages/llm/llm/src/message.ts:100-105
//
// 它不是封闭的：本包认识下面四个，读到别的会落进 [UnknownSource]。
type SourceKind string

const (
	// SourceUser 是用户自己说的话。
	SourceUser SourceKind = "user"
	// SourcePlugin 是插件注入的上下文。
	SourcePlugin SourceKind = "plugin"
	// SourceModel 是被路由到的模型产出的。
	SourceModel SourceKind = "model"
	// SourceTool 是一次工具调用的结果。
	SourceTool SourceKind = "tool"
)

// MessageSource 是一条消息（或者一段注入的内容）从哪来的。
//
// 源: packages/llm/llm/src/message.ts:125-126
//
// 新增: 封闭接口加 Unknown 变体，理由和 [ContentBlock] 逐字相同，见包文档。
type MessageSource interface {
	// SourceKind 是这个来源的判别标签。
	SourceKind() SourceKind

	// sealedMessageSource 把实现方封在本包内。
	sealedMessageSource()
}

// UserSource 表示这条消息是用户自己说的。
//
// 源: packages/llm/llm/src/message.ts:101
type UserSource struct{}

// SourceKind 实现 [MessageSource]。
func (UserSource) SourceKind() SourceKind { return SourceUser }

func (UserSource) sealedMessageSource() {}

// PluginSource 表示这段内容是某个插件注入的。
//
// 源: packages/llm/llm/src/message.ts:102
type PluginSource struct {
	// Plugin 是注入方的名字。
	Plugin string
	// Context 是注入方自己声明的「这是什么种类的东西」，可以为 nil。
	//
	// nil 是合法的，而且是有文档的默认：一段没声明形态的上下文按不透明内容呈现。
	Context Context

	// Extra 是注入方挂在自己这条来源上的额外持久字段，一个 JSON 对象；
	// 没有就是 nil。本包不解释里面的任何一个键，只保证它原样进、原样出。
	//
	// 新增: DSH 用交叉类型让插件在 `{kind:'plugin', plugin}` 上再加自己的字段，
	// 比如压缩检查点的 `compactionId`。Go 的结构体加不上字段，而**丢掉**它们
	// 不是一个选项：这些字节来自一份持久日志，一次读出来再写回去就会把别的层
	// 赖以工作的事实抹掉，且没有任何地方会报错。理由和 [UnknownBlock]、
	// [UnknownSource]、session.RawData 逐字相同——认不出来的东西原样保管。
	//
	// 键名不许和 kind、plugin、form、sections、summary 撞车，撞了当场报错：
	// 那种情况下排出去的对象会有两个同名键，而它的读回来结果取决于解码方。
	Extra json.RawMessage
}

// SourceKind 实现 [MessageSource]。
func (PluginSource) SourceKind() SourceKind { return SourcePlugin }

func (PluginSource) sealedMessageSource() {}

// ModelSource 是一条由被路由到的模型产出的助手消息必须带的来源。
//
// 源: packages/llm/llm/src/message.ts:21-24
type ModelSource struct {
	Provenance
}

// SourceKind 实现 [MessageSource]。
func (ModelSource) SourceKind() SourceKind { return SourceModel }

func (ModelSource) sealedMessageSource() {}

// ToolSource 是一条装着工具结果的用户角色消息必须带的来源。
//
// 源: packages/llm/llm/src/message.ts:26-30
type ToolSource struct {
	// CallID 指回发起这次调用的那个 [ToolCallBlock.ID]。
	CallID CallID
}

// SourceKind 实现 [MessageSource]。
func (ToolSource) SourceKind() SourceKind { return SourceTool }

func (ToolSource) sealedMessageSource() {}

// UnknownSource 是一个本构建不认识的消息来源，原样保管。
//
// 新增: 理由和 [UnknownBlock] 逐字相同。
type UnknownSource struct {
	// Kind 是这个来源自称的类别。
	Kind SourceKind
	// Raw 是这个来源完整的原始 JSON。
	Raw json.RawMessage
}

// SourceKind 实现 [MessageSource]，给出它自称的类别。
func (s UnknownSource) SourceKind() SourceKind { return s.Kind }

func (UnknownSource) sealedMessageSource() {}

// ContextForm 是注入方在自己的来源旁边声明的「这是什么种类的信息」。
//
// 源: packages/llm/llm/src/message.ts:32-60
//
// [MessageSource] 的类别回答的是**谁产出的**，形态回答的是**这是什么东西**，
// 两条轴故意互相独立——好几个产出方共用一种形态，一个产出方也可能在一次会话里
// 发出不止一种形态。
//
// 这套词汇是**语义的，绝不是视觉的**：一个取值陈述「这段内容是某个文件里的指令」
// 或者「这是本次会话可用条目的清单」，至于它长什么样由消费方决定。
// 颜色、图标、排序、默认折不折叠都是消费方的事，不许进这个联合。
type ContextForm string

const (
	// FormInstructions 是从工作区文件里读出来的、期望模型遵守的指令。
	FormInstructions ContextForm = "instructions"
	// FormCatalog 是本次会话里可用条目的清单，会随变化重新发布。
	FormCatalog ContextForm = "catalog"
	// FormSnapshot 是当前状态：同一个产出方后发的快照顶掉先发的。
	FormSnapshot ContextForm = "snapshot"
	// FormNotice 是对刚发生的某件事的一次性陈述，它不顶掉任何东西。
	FormNotice ContextForm = "notice"
	// FormRelay 是另一个 agent 说给这个 agent 听的话。
	FormRelay ContextForm = "relay"
	// FormRecall 是从另一次会话的日志里捞出来的材料，可能在进来的路上被缩过。
	FormRecall ContextForm = "recall"
)

// Context 是注入方声明的形态，连同那个形态**要求**的字段。
//
// 源: packages/llm/llm/src/message.ts:70-94
//
// 它是个联合而不是一个「带 Form 字段、各形态的载荷都摆上去」的结构体，
// 因为要守住的正是那句话：**选了一个形态就必须给出它需要的字段**。
// notice 必须记下它那一行陈述，snapshot 必须记下它的分节。
// 摊平成一个结构体的话，一个 form 是 notice 而 summary 是空串的值就写得出来，
// 而那是 DSH 那边的类型根本表达不出来的状态。
type Context interface {
	// ContextForm 是这个形态的判别标签。
	ContextForm() ContextForm

	// sealedContext 把实现方封在本包内。
	sealedContext()
}

// InstructionsContext 是 [FormInstructions] 形态，没有额外字段。
type InstructionsContext struct{}

// ContextForm 实现 [Context]。
func (InstructionsContext) ContextForm() ContextForm { return FormInstructions }

func (InstructionsContext) sealedContext() {}

// CatalogContext 是 [FormCatalog] 形态，没有额外字段。
type CatalogContext struct{}

// ContextForm 实现 [Context]。
func (CatalogContext) ContextForm() ContextForm { return FormCatalog }

func (CatalogContext) sealedContext() {}

// ContextSnapshotSection 是一份 snapshot 形态上下文里的一个具名贡献，按装配顺序排列。
//
// 源: packages/llm/llm/src/message.ts:62-68
type ContextSnapshotSection struct {
	// Name 是做出这份贡献的子系统的名字。
	Name string `json:"name"`
	// Text 是那份贡献面向模型的文本，和装配出来的一模一样。
	Text string `json:"text"`
}

// SnapshotContext 是 [FormSnapshot] 形态。
type SnapshotContext struct {
	// Sections 是这份快照装配起来的那些具名贡献，按顺序。
	Sections []ContextSnapshotSection
}

// ContextForm 实现 [Context]。
func (SnapshotContext) ContextForm() ContextForm { return FormSnapshot }

func (SnapshotContext) sealedContext() {}

// NoticeContext 是 [FormNotice] 形态。
type NoticeContext struct {
	// Summary 是「发生了什么」的一行陈述，不展开那一行也看得到。
	Summary string
}

// ContextForm 实现 [Context]。
func (NoticeContext) ContextForm() ContextForm { return FormNotice }

func (NoticeContext) sealedContext() {}

// RelayContext 是 [FormRelay] 形态，没有额外字段。
type RelayContext struct{}

// ContextForm 实现 [Context]。
func (RelayContext) ContextForm() ContextForm { return FormRelay }

func (RelayContext) sealedContext() {}

// RecallContext 是 [FormRecall] 形态，没有额外字段。
type RecallContext struct{}

// ContextForm 实现 [Context]。
func (RecallContext) ContextForm() ContextForm { return FormRecall }

func (RecallContext) sealedContext() {}

// UnknownContext 是一个本构建不认识的形态，只记它自称的名字。
//
// 新增: 这个类型自己**不**保留载荷，和 [UnknownBlock]、[UnknownSource] 两处
// 不一样。理由是 DSH 对未知形态定的行为就是「按有文档的默认处理，当作不透明
// 内容呈现」（message.ts:44-46）——消费方 switch 到 default 那一支，
// 本来就不读它的载荷；留着名字是为了诊断和转发时说得清这是什么。
//
// 载荷本身并没有被丢掉：形态的字段和注入方自己的字段在介质上摊在**同一个**
// 对象里，解码时分不出哪个键属于哪一边，所以两者一起落进
// [PluginSource.Extra]，原样进原样出。这两件事是分开的——认不认得出这个形态，
// 和这些字节保不保得住，没有关系。
type UnknownContext struct {
	// Form 是这段上下文自称的形态。
	Form ContextForm
}

// ContextForm 实现 [Context]，给出它自称的形态。
func (c UnknownContext) ContextForm() ContextForm { return c.Form }

func (UnknownContext) sealedContext() {}

// ContextSummaryMaxChars 是一行 notice 陈述的上限。
//
// 源: packages/llm/llm/src/message.ts:107-112
//
// 这行陈述跟着一条折叠的对话行走，而且会进持久日志；它的输入——任务标签、
// 目标描述、工具参数——都是调用方的文本，本身没有长度约束，所以得在这里收住。
const ContextSummaryMaxChars = 120

// BoundContextSummary 把一行 notice 陈述收进 [ContextSummaryMaxChars]。
//
// 源: packages/llm/llm/src/message.ts:114-123
//
// 新增: DSH 按 summary.length 算，那是 UTF-16 码元数。这里按**字符**（rune）算。
// 不是照抄字节数：一行中文陈述在 Go 里一个字三个字节，按字节收会在第四十个字
// 上砍断，而这个上限守的是「一行折叠的对话行放得下」，那件事按字算才成立。
func BoundContextSummary(summary string) string {
	runes := []rune(summary)
	if len(runes) <= ContextSummaryMaxChars {
		return summary
	}
	return string(runes[:ContextSummaryMaxChars-1]) + "…"
}

// Message 是投递、持久历史、模型请求三处共用的那一个不可变消息表示。
//
// 源: packages/llm/llm/src/message.ts:128-156
//
// 新增: DSH 还有 UserMessage / AssistantMessage / ToolResultMessage 三个**子类型**，
// 它们不是另外三种消息，是同一种消息把 role、source、content 收窄之后的样子。
// Go 没有这种结构化的子类型，硬造三个结构体的代价是 []Message 装不下它们——
// 而「一个共用表示」正是这个类型存在的理由。
//
// 所以这里只有一个结构体，收窄由两侧分头保证：
//
//   - 写的一侧：[NewUserMessage]、[NewAssistantMessage]、[NewToolResultMessage]
//     三个构造函数把该钉死的字段钉死，调用方给不出一条角色和来源对不上的消息。
//   - 读的一侧：[Message.ModelSource] 和 [Message.ToolResult] 把那两个收窄
//     重新取出来，取不到就是 false。
type Message struct {
	// ID 是跨越每一道表示边界都不变的身份。
	ID MessageID
	// Role 是与提供方无关的会话角色。
	Role Role
	// Content 是面向模型的那些块，一字不差。
	Content Content
	// Source 是产出方必须给出的来源字段。
	Source MessageSource
}

// Clone 深复制这条消息。
//
// 源: packages/llm/llm/src/message.ts:164-171（freezeMessage）
//
// 新增: DSH 那边是 structuredClone 加 deepFreeze，因为 JS 的对象按引用共享，
// 一条发布出去的消息会被收到它的人改掉。Go 的结构体赋值就是复制，
// 但里面的切片不是，所以这一趟负责的就是那些切片。
func (m Message) Clone() Message {
	m.Content = m.Content.Clone()
	if unknown, ok := m.Source.(UnknownSource); ok {
		unknown.Raw = append(json.RawMessage(nil), unknown.Raw...)
		m.Source = unknown
	}
	if model, ok := m.Source.(ModelSource); ok {
		model.ReplayState = append(json.RawMessage(nil), model.ReplayState...)
		m.Source = model
	}
	if plugin, ok := m.Source.(PluginSource); ok {
		if snapshot, isSnapshot := plugin.Context.(SnapshotContext); isSnapshot {
			snapshot.Sections = append([]ContextSnapshotSection(nil), snapshot.Sections...)
			plugin.Context = snapshot
		}
		m.Source = plugin
	}
	return m
}

// ModelSource 取出这条消息的模型来源；不是一条模型产出的助手消息时第二个返回值是 false。
//
// 这是 DSH 的 AssistantMessage 在 Go 里的读取面，见 [Message] 的类型注释。
func (m Message) ModelSource() (ModelSource, bool) {
	if m.Role != RoleAssistant {
		return ModelSource{}, false
	}
	source, ok := m.Source.(ModelSource)
	return source, ok
}

// ToolResult 取出这条消息唯一的那个工具结果块；不是一条工具结果消息时第二个返回值是 false。
//
// 这是 DSH 的 ToolResultMessage 在 Go 里的读取面：那边的类型把 content 收窄成
// 恰好一个 [ToolResultBlock]，这里把那个收窄重新验一遍再交出去。
func (m Message) ToolResult() (ToolResultBlock, bool) {
	if m.Role != RoleUser {
		return ToolResultBlock{}, false
	}
	if _, ok := m.Source.(ToolSource); !ok {
		return ToolResultBlock{}, false
	}
	if len(m.Content) != 1 {
		return ToolResultBlock{}, false
	}
	block, ok := m.Content[0].(ToolResultBlock)
	return block, ok
}

// NewMessage 造一条带全新身份的消息。
//
// 源: packages/llm/llm/src/message.ts:173-185
//
// 内容会被复制一份：调用方留着的那个切片之后怎么改，都改不动这条消息。
func NewMessage(role Role, content Content, source MessageSource) Message {
	return Message{
		ID:      MessageID(uuid.NewString()),
		Role:    role,
		Content: content.Clone(),
		Source:  source,
	}
}

// NewUserMessage 造一条带全新身份的用户角色消息。
//
// 源: packages/llm/llm/src/message.ts:187-199
func NewUserMessage(content Content, source MessageSource) Message {
	return NewMessage(RoleUser, content, source)
}

// NewAssistantMessage 造一条带全新身份的、模型产出的助手消息。
//
// 源: packages/llm/llm/src/message.ts:201-217
func NewAssistantMessage(content Content, provenance Provenance) Message {
	return NewMessage(RoleAssistant, content, ModelSource{Provenance: provenance})
}

// NewToolResultMessage 造一条带全新身份的工具结果消息。
//
// 源: packages/llm/llm/src/message.ts:219-241
//
// 角色是 user 而不是另立一个 tool 角色：工具结果是回送给模型的输入，
// 提供方那一侧看到的就是一条用户消息。调用相关性靠 [ToolSource.CallID] 和
// 那个块里的 [ToolResultBlock.ToolCallID] 两处一起保住。
func NewToolResultMessage(callID CallID, content Content, isError bool) Message {
	return NewUserMessage(
		Content{ToolResultBlock{ToolCallID: callID, Content: content, IsError: isError}},
		ToolSource{CallID: callID},
	)
}

// messageWire 是一条消息在介质上的样子。
type messageWire struct {
	ID      MessageID       `json:"id"`
	Role    Role            `json:"role"`
	Content Content         `json:"content"`
	Source  json.RawMessage `json:"source"`
}

// UnmarshalJSON 把一段字节读回一条消息。
//
// 新增: 只有读的一侧需要自己写。排出去的时候 [Message] 的字段本身就带 json 标签，
// 里面那两个接口各自的 MarshalJSON 会被 encoding/json 自动叫到。
func (m *Message) UnmarshalJSON(data []byte) error {
	var wire messageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}
	source, err := UnmarshalMessageSource(wire.Source)
	if err != nil {
		return err
	}
	m.ID = wire.ID
	m.Role = wire.Role
	m.Content = wire.Content
	m.Source = source
	return nil
}

// MarshalJSON 把这条消息排出去。
func (m Message) MarshalJSON() ([]byte, error) {
	source, err := json.Marshal(m.Source)
	if err != nil {
		return nil, err
	}
	return json.Marshal(messageWire{
		ID:      m.ID,
		Role:    m.Role,
		Content: m.Content,
		Source:  source,
	})
}

// 下面是四种来源在介质上的样子。插件来源把它的形态**摊平**在同一个对象里，
// 和 DSH 的交叉类型（`{kind, plugin} & ContextFormed`）排出来的形状一致。
type userSourceWire struct {
	Kind SourceKind `json:"kind"`
}

type pluginSourceWire struct {
	Kind     SourceKind               `json:"kind"`
	Plugin   string                   `json:"plugin"`
	Form     ContextForm              `json:"form,omitempty"`
	Sections []ContextSnapshotSection `json:"sections,omitempty"`
	Summary  string                   `json:"summary,omitempty"`
}

type modelSourceWire struct {
	Kind        SourceKind      `json:"kind"`
	Provider    string          `json:"provider"`
	Model       string          `json:"model"`
	ReplayState json.RawMessage `json:"replayState,omitempty"`
}

type toolSourceWire struct {
	Kind   SourceKind `json:"kind"`
	CallID CallID     `json:"callId"`
}

// MarshalJSON 把这个来源连同判别标签一起排出去。
func (UserSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(userSourceWire{Kind: SourceUser})
}

// MarshalJSON 把这个来源连同判别标签、形态和注入方自己的额外字段一起排出去。
func (s PluginSource) MarshalJSON() ([]byte, error) {
	wire := pluginSourceWire{Kind: SourcePlugin, Plugin: s.Plugin}
	if s.Context != nil {
		wire.Form = s.Context.ContextForm()
		switch typed := s.Context.(type) {
		case SnapshotContext:
			wire.Sections = typed.Sections
		case NoticeContext:
			wire.Summary = typed.Summary
		}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		// 走不到：wire 里只有字符串和字符串结构体的切片，排不出错。
		return nil, err
	}
	return mergePluginExtra(encoded, s.Extra)
}

// pluginSourceKeys 是本包在插件来源上认识的键，[PluginSource.Extra] 不许用它们。
var pluginSourceKeys = map[string]struct{}{
	"kind": {}, "plugin": {}, "form": {}, "sections": {}, "summary": {},
}

// mergePluginExtra 把注入方自己的字段并进已经排好的来源对象里。
//
// 走「解成 map 再排一次」而不是拼字节，是因为拼字节要自己处理空对象、
// 逗号和转义，那是一条稳定出错的路；而这里排的是一个只有几个键的小对象。
func mergePluginExtra(encoded []byte, extra json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return encoded, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(extra, &fields); err != nil {
		return nil, fmt.Errorf("%w：插件来源的额外字段不是一个 JSON 对象：%w", ErrMalformedValue, err)
	}
	if len(fields) == 0 {
		return encoded, nil
	}

	var merged map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &merged); err != nil {
		return nil, err
	}
	for name, value := range fields {
		if _, taken := pluginSourceKeys[name]; taken {
			return nil, fmt.Errorf("%w：插件来源的额外字段不许叫 %q，那是本包自己的键",
				ErrMalformedValue, name)
		}
		merged[name] = value
	}
	return json.Marshal(merged)
}

// pluginExtraOf 把一个插件来源上本包不认识的键收起来，没有就交出 nil。
func pluginExtraOf(data []byte) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}
	for name := range fields {
		if _, known := pluginSourceKeys[name]; known {
			delete(fields, name)
		}
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return json.Marshal(fields)
}

// MarshalJSON 把这个来源连同判别标签一起排出去。
//
// 重放状态不合法时当场报错，不排成 `null`。理由和本仓库 credentials 里那条一样：
// 一段声称自己是 JSON 的字节声称错了，必须当场失败，而不是往日志里写一条
// 下次读回来重放状态是 null 的消息——那条消息在适配器看来就是一次静默的重放丢失。
func (s ModelSource) MarshalJSON() ([]byte, error) {
	if len(s.ReplayState) > 0 && !json.Valid(s.ReplayState) {
		return nil, fmt.Errorf("%w：模型来源的重放状态不是合法 JSON", ErrMalformedValue)
	}
	return json.Marshal(modelSourceWire{
		Kind:        SourceModel,
		Provider:    s.Provider,
		Model:       s.Model,
		ReplayState: s.ReplayState,
	})
}

// MarshalJSON 把这个来源连同判别标签一起排出去。
func (s ToolSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(toolSourceWire{Kind: SourceTool, CallID: s.CallID})
}

// MarshalJSON 把这个来源原样吐回去，理由同 [UnknownBlock.MarshalJSON]。
func (s UnknownSource) MarshalJSON() ([]byte, error) {
	if !json.Valid(s.Raw) {
		return nil, fmt.Errorf("%w：不认识的消息来源没有原始字节", ErrMalformedValue)
	}
	return s.Raw, nil
}

// UnmarshalMessageSource 把一段字节读回一个 [MessageSource]。
//
// 不认识的标签收进 [UnknownSource]，不报错。
func UnmarshalMessageSource(data []byte) (MessageSource, error) {
	var tagged struct {
		Kind SourceKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}

	switch tagged.Kind {
	case SourceUser:
		return UserSource{}, nil

	case SourcePlugin:
		var wire pluginSourceWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		extra, err := pluginExtraOf(data)
		if err != nil {
			// 走不到：上面那一句已经把 data 解进过一个结构体，它必然是个对象。
			return nil, err
		}
		return PluginSource{Plugin: wire.Plugin, Context: contextOf(wire), Extra: extra}, nil

	case SourceModel:
		var wire modelSourceWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ModelSource{Provenance: Provenance{
			Provider:    wire.Provider,
			Model:       wire.Model,
			ReplayState: wire.ReplayState,
		}}, nil

	case SourceTool:
		var wire toolSourceWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ToolSource{CallID: wire.CallID}, nil

	default:
		return UnknownSource{
			Kind: tagged.Kind,
			Raw:  append(json.RawMessage(nil), data...),
		}, nil
	}
}

// contextOf 把摊平的那几个字段收回成一个 [Context]。
//
// 没有 form 字段时给 nil——那是有文档的默认，不是一条错误。
func contextOf(wire pluginSourceWire) Context {
	switch wire.Form {
	case "":
		return nil
	case FormInstructions:
		return InstructionsContext{}
	case FormCatalog:
		return CatalogContext{}
	case FormSnapshot:
		return SnapshotContext{Sections: wire.Sections}
	case FormNotice:
		return NoticeContext{Summary: wire.Summary}
	case FormRelay:
		return RelayContext{}
	case FormRecall:
		return RecallContext{}
	default:
		return UnknownContext{Form: wire.Form}
	}
}
