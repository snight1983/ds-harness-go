// 本文件的作用：会话日志里的一条条目——信封字段、它在介质上的样子，
// 以及「一个不认识的类型能不能跳过」那条规则。
//
// 源: packages/core/session/src/types.ts:378-423

package session

import (
	"encoding/json"
	"fmt"
)

// EventType 是一条事件的判别标签。
//
// 源: packages/core/session/src/types.ts:322-323（SessionEventType）
//
// 它是**开放**的：DSH 那边 SessionEventMap 是一个可被插件合并扩展的映射，
// 本包只登记了核心的那 13 个（见 [CoreVocabulary]），别的包可以往上加。
type EventType string

// 核心的 13 个事件类型。
//
// 源: packages/core/session/src/types.ts:236-337
const (
	// EventTurnStart 打开一个回合，在循环认领排队输入、跑前置步骤之前。
	//
	// 被拒、输入为空、被取消、或者失败，都可能让这个回合一个步骤都没进就关掉；
	// 否则紧跟着的那条（或那批）user/message 记的就是进入这个步骤的消息。
	EventTurnStart EventType = "turn/start"
	// EventTurnEnd 用一个 [TurnEndReason] 关掉这个回合。
	//
	// 一个没进过步骤的回合不会有 step/start 和 step/end。
	EventTurnEnd EventType = "turn/end"
	// EventStepStart 打开一个步骤——一次模型调用，加上它请求的那些工具执行。
	EventStepStart EventType = "step/start"
	// EventStepEnd 关掉一个步骤。
	EventStepEnd EventType = "step/end"
	// EventUserMessage 是模型可见表面上的一条用户角色消息。
	//
	// 三种来路都走这一条：真人直接输入的提示、注入进来的合成上下文
	// （文件变更通知、技能内容、定时通知……）、以及进入的目标续跑轮次。
	// 三种都把自己的 content 原样投影出去，靠 source 分辨彼此。
	EventUserMessage EventType = "user/message"
	// EventAssistantChunk 是一个原始流式分块——留着做 token 级的回放保真。
	EventAssistantChunk EventType = "assistant/chunk"
	// EventAssistantMessage 是一个步骤装配好的助手消息，派生历史用的就是它。
	//
	// 适配器报了 token 记账时，这个步骤的用量跟着它一起走——模型输出和它的
	// 记账是同一条事件，没有单独的用量记录。
	EventAssistantMessage EventType = "assistant/message"
	// EventToolCall 是模型请求的一次工具调用。
	EventToolCall EventType = "tool/call"
	// EventToolResult 是一次已完成的工具调用面向模型的结果。
	EventToolResult EventType = "tool/result"
	// EventTodoWrite 是待办清单的整份快照，回放时最后一次写的那份生效。
	//
	// 只进日志、不进派生历史。
	EventTodoWrite EventType = "todo/write"
	// EventRequestHeader 是下一次请求的完整请求头，在它所属的步骤里、发出之前追加。
	//
	// 只进日志：最新的那份快照就是重建出来的请求头。
	EventRequestHeader EventType = "request/header"
	// EventRequestContext 是下一次请求的路由元数据，只在路由或容量变了时记。
	//
	// 它不参与请求重建，也不参与请求头的相等判断。
	EventRequestContext EventType = "request/context"
	// EventSessionEndSeed 标记一份构造 seed 的结尾。
	//
	// 排在它前面的事件 seq 更小、来自 seed（恢复、分叉或回放），这一次生命周期
	// 一条都没产出过。它的负载是空的——位置和时间戳就是它的全部意思。
	EventSessionEndSeed EventType = "session/end-seed"
)

// Event 是会话日志里一条不可变的条目。
//
// 源: packages/core/session/src/types.ts:378-423
//
// 新增: DSH 那边是一个在 type 上判别的映射类型，data 的类型跟着 type 收窄，
// 而且 sourceEventSeqs 与 surfaceOp 两个字段**只在**三种表面事件的变体上存在
// （`K extends SurfaceEventType ? {...} : object`）。Go 的结构体做不到按一个
// 字段的值裁剪另一批字段，所以这里三个字段都在，那条约束改由 [SurfaceOpOf]
// 在运行期验——DSH 在运行期也验（surfaceOpOf），本包只是少了它那道编译期的
// 第二层。
//
// Data 是一段原始字节而不是解好的联合类型，理由见包文档。
type Event struct {
	// Type 是这条事件的类型。
	Type EventType
	// Seq 是会话内单调递增的序号。
	Seq int
	// Time 是 Unix 纪元毫秒。
	//
	// 新增: DSH 是 number，也就是 float64。这里是 int64：毫秒时间戳在 Go 里
	// 逐位精确，DSH 那一整套 Number.isSafeInteger 检查随之消失。
	Time int64
	// Data 是这条事件的负载，原样保管。
	//
	// 要具名字段时用 [DecodeData]。
	Data json.RawMessage
	// Ignorable 标记一个读者在**不认识** Type 时可以安全跳过这条事件。
	//
	// 源: packages/core/session/src/types.ts:416-426
	//
	// 缺省是「必需」：读者遇到一个不认识、又没带这个标记的类型时，
	// 必须**拒绝重建**这个会话，而不是把它默默丢掉——一条不认识的必需事件
	// 可能改变后面整段日志的解释方式。写的一方只在纯信息性的、丢掉也不影响
	// 重建的记录上把它设成真。默认必需意味着忘了标只会过度拒绝（麻烦一下），
	// 而不是默默地恢复出一个被掏空的会话。
	Ignorable bool
	// SurfaceOp 是这条事件进表面的方式；nil 表示它不上表面。
	//
	// 只有 [IsSurfaceEligibleType] 认的那三种类型能带它，而那三种类型**必须**带它。
	SurfaceOp SurfaceOp
	// SourceEventSeqs 是这条事件引用的、更早那些事件的 seq。
	//
	// 比如装配出一条 assistant/message 的那些 assistant/chunk 的 seq，
	// 或者一个压缩替换节点盖掉的那些表面节点。
	//
	// nil 表示**没给**这个字段；长度为零的切片表示**明确给了一个空清单**
	// （只有 assistant/message 可以这么用，表示一次已知为空的提供方流）。
	// 两者必须分得开，所以介质那一侧用的是指针，见 [Event.MarshalJSON]。
	SourceEventSeqs []int
}

// eventWire 是一条事件在介质上的样子。
type eventWire struct {
	Type            EventType       `json:"type"`
	Seq             int             `json:"seq"`
	Time            int64           `json:"time"`
	Data            json.RawMessage `json:"data"`
	Ignorable       bool            `json:"ignorable,omitempty"`
	SurfaceOp       json.RawMessage `json:"surfaceOp,omitempty"`
	SourceEventSeqs *[]int          `json:"sourceEventSeqs,omitempty"`
}

// MarshalJSON 把这条事件排出去。
//
// 新增: SourceEventSeqs 在介质上是 *[]int 而不是 []int。omitempty 会把
// 长度为零的切片和 nil 排成同一段字节（都是没有这个键），而这两者在
// DSH 那边是两件事：`sourceEventSeqs: []` 说的是「已知这条消息没有来源事件」，
// 缺失说的是「这条事件不记录自己从哪来」。多一层指针换来的正是这个区分。
func (e Event) MarshalJSON() ([]byte, error) {
	wire := eventWire{
		Type:      e.Type,
		Seq:       e.Seq,
		Time:      e.Time,
		Data:      e.Data,
		Ignorable: e.Ignorable,
	}
	if wire.Data == nil {
		// 每条事件都有负载，空负载是 `{}`（session/end-seed 就是这样）。
		// 排成 `null` 会让下次读回来的 Data 是四个字节的 "null"，
		// 而那不是任何一个负载结构体的合法形状。
		wire.Data = json.RawMessage(`{}`)
	}
	if e.SurfaceOp != nil {
		operation, err := json.Marshal(e.SurfaceOp)
		if err != nil {
			return nil, fmt.Errorf("%w：表面操作排不出去：%w", ErrMalformedValue, err)
		}
		wire.SurfaceOp = operation
	}
	if e.SourceEventSeqs != nil {
		seqs := e.SourceEventSeqs
		wire.SourceEventSeqs = &seqs
	}
	return json.Marshal(wire)
}

// UnmarshalJSON 把一段字节读回一条事件。
//
// 新增: 信封上出现一个本包不认识的键时**当场报错**，不是默默忽略。
// 理由写在 [FormatVersion] 上：信封形状的改动是一次必须进位的结构性改动，
// 所以一份多带了信封字段的日志一定来自一个更新的写方，而那份日志本构建
// 读不全。这和 [Event.Ignorable] 那条「不认识就拒绝重建、不许静默丢弃」
// 是同一个态度，只是作用在信封而不是词汇上。
//
// 词汇的增长不受这条影响——新的事件类型体现在 Type 和 Data 上，信封形状不变。
func (e *Event) UnmarshalJSON(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("%w：事件不是一个对象：%w", ErrMalformedValue, err)
	}
	for key := range probe {
		switch key {
		case "type", "seq", "time", "data", "ignorable", "surfaceOp", "sourceEventSeqs":
		default:
			return fmt.Errorf("%w：事件信封上有本构建不认识的键 %q", ErrMalformedValue, key)
		}
	}

	var wire eventWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：事件读不回来：%w", ErrMalformedValue, err)
	}

	var operation SurfaceOp
	if wire.SurfaceOp != nil {
		parsed, err := UnmarshalSurfaceOp(wire.SurfaceOp)
		if err != nil {
			return err
		}
		operation = parsed
	}

	e.Type = wire.Type
	e.Seq = wire.Seq
	e.Time = wire.Time
	e.Data = wire.Data
	e.Ignorable = wire.Ignorable
	e.SurfaceOp = operation
	e.SourceEventSeqs = nil
	// 「这个键在不在」只能从 probe 上看：stdlib 把 `null` 解进 *[]int 时留下的
	// 也是一个 nil 指针，和「这个键根本没出现」在 wire 上分不开。
	if _, present := probe["sourceEventSeqs"]; present {
		// `"sourceEventSeqs": null` 和长度为零的清单是两回事，
		// 但 null 也不是「没给」——把它收成一个空清单，
		// 剩下的合法性由 [assertProvenance] 判。
		e.SourceEventSeqs = []int{}
		if wire.SourceEventSeqs != nil {
			e.SourceEventSeqs = *wire.SourceEventSeqs
		}
	}
	return nil
}

// Clone 深复制这条事件。
//
// 事件是**不可变**的日志条目，但 Go 的结构体里那两个切片不是。
// 一条被别人从背后改掉的日志条目，改动发生在离现场很远的地方。
func (e Event) Clone() Event {
	e.Data = append(json.RawMessage(nil), e.Data...)
	if e.SourceEventSeqs != nil {
		e.SourceEventSeqs = append([]int{}, e.SourceEventSeqs...)
	}
	return e
}
