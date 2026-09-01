// 本文件的作用：这个包的那几种值——落进日志的记录和改动、给模型看的视图、
// 以及那一份封闭的错误码表。
//
// 源: packages/schedule/schedule/src/types.ts

package schedule

import (
	"bytes"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/session"
)

// marshalNoEscape 把一个值排成 JSON，**不**做 HTML 转义。
//
// 新增: [encoding/json.Marshal] 默认把 < > & 转成 < 这类写法，DSH 用的
// JSON.stringify 不转。本包排出去的字节有两个去处，两个都不许被这层转义改掉：
// 落进会话日志的那份要和 DSH 互读，交给模型的那份**就是模型看到的原文**
// （见 [renderValue]）。
//
// 这件事必须落在最里面那一层。外面那圈 Encoder 上的 SetEscapeHTML(false) 管不着
// 自定义 MarshalJSON 已经排好的字节——那些字节到外层只会被原样搬运，转义早就
// 发生过了。所以下面每一处 MarshalJSON 都得走这里，一处漏掉就漏掉一整支。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/schedule/schedule/src/invariant.ts:11
const PackageName = "@deepseek-ai/dsh-schedule"

// PluginName 是这个包露面时用的名字，也是它发出去的那条提醒消息的来源标记。
//
// 源: packages/schedule/schedule/src/index.ts:35, runtime.ts:273
const PluginName = "schedule"

// EventChange 是本包拥有的那一种会话事件：一次带版本号的提醒改动。
//
// 源: packages/schedule/schedule/src/types.ts:216-219
//
// 拥有它的意思是：只有本包写它，也只有本包解释它，而且**整条流**的合法性由
// 本包在不变量那一侧一次性验完（见 [RegisterInvariants]）。
const EventChange session.EventType = "schedule/change"

// EventTypes 是本包往会话词汇表里加的那些类型。
//
//	vocabulary := session.CoreVocabulary().With(schedule.EventTypes()...)
func EventTypes() []session.EventType { return []session.EventType{EventChange} }

// ChangeVersion 是本包实现的那一版耐久协议。
//
// 源: packages/schedule/schedule/src/domain.ts:20-21（SCHEDULE_CHANGE_VERSION）
const ChangeVersion = 1

// MinEveryIntervalSeconds 是固定频率提醒的下限，v1 钉死在五分钟。
//
// 源: packages/schedule/schedule/src/domain.ts:23-24（MIN_EVERY_INTERVAL_SECONDS）
const MinEveryIntervalSeconds = 300

// ID 是一个提醒在它那个会话里的身份：唯一，而且**一次都不重用**。
//
// 源: packages/schedule/schedule/src/types.ts:9-10（ScheduleId）
//
// 新增: DSH 是一个 branded string。Go 里就是一个具名字符串类型——它挡住的东西
// 是一样的（一个裸 string 传不进来），而且不用那套品牌机制。
type ID string

// Kind 是一条记录的规则判别。
//
// 源: packages/schedule/schedule/src/types.ts:69
type Kind string

const (
	// KindAfter 是「从现在起过多少秒」定出来的一次性提醒。
	KindAfter Kind = "after"
	// KindAt 是一个绝对时刻定出来的一次性提醒。
	KindAt Kind = "at"
	// KindEvery 是固定频率提醒，锚点是它被创建的那一刻。
	KindEvery Kind = "every"
)

// State 是一条记录此刻的投递时序。
//
// 源: packages/schedule/schedule/src/types.ts:107-108（ScheduleState）
type State string

const (
	// StateScheduled 表示目标时刻还没到。
	StateScheduled State = "scheduled"
	// StateOverdue 表示目标时刻已经过了，但这一次还没投出去——会话没开着时就是这样。
	StateOverdue State = "overdue"
)

// DeliveryMode 是 v1 那条钉死的投递边界。
//
// 源: packages/schedule/schedule/src/types.ts:110-111（ScheduleDeliveryMode）
type DeliveryMode string

// DeliverySessionLocal 表示提醒**只在**它自己那个会话活着的时候投出去。
//
// 这是一句给模型的承诺，不是一个可选项：v1 里没有第二种取值。
const DeliverySessionLocal DeliveryMode = "session-local"

// Record 是一条落进日志的耐久提醒。
//
// 源: packages/schedule/schedule/src/types.ts:12-24（AfterScheduleRecord）
//
// 新增: DSH 是 AfterScheduleRecord | AtScheduleRecord | EveryScheduleRecord 三支
// 判别联合，每一支在介质上都是 `additionalProperties: false` 的封闭对象。Go 这边
// 落成**一个**带 Kind 判别的结构体，理由和 [github.com/snight1983/ds-harness-go/core/tools.Result] 上
// 那一段逐字相同：折叠、排序、查找、投影全都要在一个同质的集合上做，三个各自
// 封闭的结构体会逼出一个只为了装它们而存在的接口。
//
// 那三份封闭 schema 由 [Record.MarshalJSON] 和 [decodeRecord] 守住：排出去时
// 按 Kind 只写那一支该有的键，读回来时多一个键少一个键都当场拒绝。两个跟着
// Kind 走的字段各自只在自己那一支上有意义：
//
//   - AfterSeconds 只在 [KindAfter] 上，正数。
//   - EverySeconds 只在 [KindEvery] 上，不小于 [MinEveryIntervalSeconds]。
type Record struct {
	// ID 是会话内那个稳定的身份。
	ID ID
	// Kind 是这条规则的判别。
	Kind Kind
	// Prompt 是创建时给的提醒正文，已经去过首尾空白且非空。
	Prompt string
	// AfterSeconds 是 [KindAfter] 那条规则的延迟秒数。
	AfterSeconds int64
	// EverySeconds 是 [KindEvery] 那条规则的固定间隔秒数。
	EverySeconds int64
	// ScheduledAt 是下一个还没投出去的目标时刻，写法见 [FormatInstant]。
	//
	// 新增: DSH 也是字符串。这里没换成 [time.Time]，因为它同时是**介质上的字节**
	// 和**给模型看的那个值**，而 [time.Time] 排出去会把末尾的零毫秒省掉
	// （RFC 3339 Nano），那就不再是本包认得的那一种写法了。要算的时候
	// 用 [ParseInstant]。
	ScheduledAt string
}

// afterWire、atWire、everyWire 是三支各自封闭的介质形状。
//
// 源: packages/schedule/schedule/src/types.ts:13-50
type afterWire struct {
	ID           ID     `json:"id"`
	Kind         Kind   `json:"kind"`
	Prompt       string `json:"prompt"`
	AfterSeconds int64  `json:"afterSeconds"`
	ScheduledAt  string `json:"scheduledAt"`
}

type atWire struct {
	ID          ID     `json:"id"`
	Kind        Kind   `json:"kind"`
	Prompt      string `json:"prompt"`
	ScheduledAt string `json:"scheduledAt"`
}

type everyWire struct {
	ID           ID     `json:"id"`
	Kind         Kind   `json:"kind"`
	Prompt       string `json:"prompt"`
	EverySeconds int64  `json:"everySeconds"`
	ScheduledAt  string `json:"scheduledAt"`
}

// wire 把一条记录摊成它那一支该有的形状。
//
// 认不出的 Kind 交回 nil：本包自己造不出这种值，唯一的来路是调用方硬填了一个
// 别的字符串，所以让它排不出去，而不是排出一份形状对不上的字节。
func (r Record) wire() any {
	switch r.Kind {
	case KindAfter:
		return afterWire{ID: r.ID, Kind: r.Kind, Prompt: r.Prompt, AfterSeconds: r.AfterSeconds, ScheduledAt: r.ScheduledAt}
	case KindAt:
		return atWire{ID: r.ID, Kind: r.Kind, Prompt: r.Prompt, ScheduledAt: r.ScheduledAt}
	case KindEvery:
		return everyWire{ID: r.ID, Kind: r.Kind, Prompt: r.Prompt, EverySeconds: r.EverySeconds, ScheduledAt: r.ScheduledAt}
	default:
		return nil
	}
}

// MarshalJSON 按这条记录那一支的封闭形状排出去。
func (r Record) MarshalJSON() ([]byte, error) {
	shape := r.wire()
	if shape == nil {
		return nil, &LogError{Reason: "认不得的提醒判别 " + string(r.Kind)}
	}
	return marshalNoEscape(shape)
}

// View 是一条活着的提醒面向模型的完整样子。
//
// 源: packages/schedule/schedule/src/types.ts:113-119（ScheduleView）
//
// 它是记录本身加上两个**推出来的**字段：一个跟着墙上时钟走，一个是钉死的承诺。
// 两个都不落盘——落盘的东西必须能从日志重算出来，而这两个都不能。
type View struct {
	Record
	// State 是按某一次墙上时钟采样算出来的时序。
	State State
	// DeliveryMode 永远是 [DeliverySessionLocal]。
	DeliveryMode DeliveryMode
}

// viewExtra 是视图比记录多出来的那两个键。
//
// 单独抽出来是为了让下面三支复用同一份定义：介质上它们是平铺的，
// 靠 Go 的匿名嵌入字段摊开。
type viewExtra struct {
	State        State        `json:"state"`
	DeliveryMode DeliveryMode `json:"deliveryMode"`
}

type afterViewWire struct {
	afterWire
	viewExtra
}

type atViewWire struct {
	atWire
	viewExtra
}

type everyViewWire struct {
	everyWire
	viewExtra
}

// MarshalJSON 按这条视图那一支的封闭形状排出去。
//
// 必须显式写在 [View] 上：不写的话嵌进来的 [Record.MarshalJSON] 会被提升上来，
// 于是 state 和 deliveryMode 两个键会**静悄悄地消失**。
func (v View) MarshalJSON() ([]byte, error) {
	extra := viewExtra{State: v.State, DeliveryMode: v.DeliveryMode}
	switch shape := v.Record.wire().(type) {
	case afterWire:
		return marshalNoEscape(afterViewWire{afterWire: shape, viewExtra: extra})
	case atWire:
		return marshalNoEscape(atViewWire{atWire: shape, viewExtra: extra})
	case everyWire:
		return marshalNoEscape(everyViewWire{everyWire: shape, viewExtra: extra})
	default:
		return nil, &LogError{Reason: "认不得的提醒判别 " + string(v.Kind)}
	}
}

// Operation 是一次耐久改动做的是哪件事。
//
// 源: packages/schedule/schedule/src/types.ts:105
type Operation string

const (
	// OpCreate 造一条记录。
	OpCreate Operation = "create"
	// OpDelete 撤掉一条还活着的记录。
	OpDelete Operation = "delete"
	// OpDispatch 记下「这一次投出去了」。
	OpDispatch Operation = "dispatch"
)

// Change 是一次带版本号的耐久提醒改动，也就是 [EventChange] 的负载。
//
// 源: packages/schedule/schedule/src/types.ts:72-105
//
// 新增: 同 [Record]，DSH 那边是四支判别联合，这里是一个带 Operation 判别的
// 结构体。跟着判别走的三个字段各自只在自己那一支上有意义：
//
//   - Schedule 只在 [OpCreate] 上，非 nil。
//   - ID 只在 [OpDelete] 和 [OpDispatch] 上。
//   - AcceptedAt 只在**固定频率**那一次 [OpDispatch] 上；一次性那一次必须留空。
//
// 最后那一条不是可有可无的整洁：一次性提醒本来就没有「跳过错过的那些」这回事，
// 一条带着 acceptedAt 的一次性 dispatch 说明写它的那一方把两种规则搞混了，
// 而**那条日志后面的每一次回放都会跟着错**。所以它在解码那一步就被拒。
type Change struct {
	// Version 必须是 [ChangeVersion]。
	Version int
	// Operation 是这次改动做的事。
	Operation Operation
	// Schedule 是 [OpCreate] 造出来的那条记录。
	Schedule *Record
	// ID 是 [OpDelete] 和 [OpDispatch] 指的那条记录。
	ID ID
	// AcceptedAt 是固定频率那一次投递做决定的墙上时刻，写法见 [FormatInstant]。
	// 空串表示没有这个键。
	AcceptedAt string
}

type createChangeWire struct {
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
	Schedule  *Record   `json:"schedule"`
}

type deleteChangeWire struct {
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
	ID        ID        `json:"id"`
}

type dispatchChangeWire struct {
	Version    int       `json:"version"`
	Operation  Operation `json:"operation"`
	ID         ID        `json:"id"`
	AcceptedAt string    `json:"acceptedAt,omitempty"`
}

// MarshalJSON 按这次改动那一支的封闭形状排出去。
//
// dispatch 那一支上的 omitempty 是对的、而且是必须的：acceptedAt 在一次性那一支上
// **必须不出现**，而它合法时永远是一个非空的时刻串，所以「空」和「不该出现」
// 在这里是同一件事。
func (c Change) MarshalJSON() ([]byte, error) {
	switch c.Operation {
	case OpCreate:
		if c.Schedule == nil {
			return nil, &LogError{Reason: "create 改动必须带上那条记录"}
		}
		return marshalNoEscape(createChangeWire{Version: c.Version, Operation: c.Operation, Schedule: c.Schedule})
	case OpDelete:
		return marshalNoEscape(deleteChangeWire{Version: c.Version, Operation: c.Operation, ID: c.ID})
	case OpDispatch:
		return marshalNoEscape(dispatchChangeWire{
			Version: c.Version, Operation: c.Operation, ID: c.ID, AcceptedAt: c.AcceptedAt,
		})
	default:
		return nil, &LogError{Reason: "认不得的改动操作 " + string(c.Operation)}
	}
}

// PersistenceOperation 是那三件可能报「落盘不确定」的管理操作。
//
// 源: packages/schedule/schedule/src/types.ts:121-122（SchedulePersistenceOperation）
type PersistenceOperation string

const (
	// OperationCreate 是 schedule_create。
	OperationCreate PersistenceOperation = "create"
	// OperationList 是 schedule_list。
	OperationList PersistenceOperation = "list"
	// OperationDelete 是 schedule_delete。
	OperationDelete PersistenceOperation = "delete"
)

// ErrorCode 是这个包那一份**封闭**的、面向模型的错误码表。
//
// 源: packages/schedule/schedule/src/types.ts:187-197
//
// 封闭的意思是：模型可以照着这十个码写判断，本包不会在 v1 里往里加第十一个。
// 每一条工具结果要么是一个成功值，要么带着这里面的一个码。
type ErrorCode string

const (
	// CodeInvalidPrompt 是提醒正文去掉首尾空白之后是空的。
	CodeInvalidPrompt ErrorCode = "invalid_prompt"
	// CodeInvalidSelector 是三个规则选择器没给、给多了、或者混进了别的键。
	CodeInvalidSelector ErrorCode = "invalid_selector"
	// CodeInvalidRule 是规则本身或者管理参数不合法。
	CodeInvalidRule ErrorCode = "invalid_rule"
	// CodeInvalidTimeZone 是时区名不合法或者这台机器上没有。
	CodeInvalidTimeZone ErrorCode = "invalid_time_zone"
	// CodeNotFuture 是算出来的目标时刻不严格在未来。
	CodeNotFuture ErrorCode = "not_future"
	// CodeTimeOutOfRange 是算出来的时刻写不成四位年份的 UTC 时刻。
	CodeTimeOutOfRange ErrorCode = "time_out_of_range"
	// CodeFrequencyTooHigh 是固定频率比 [MinEveryIntervalSeconds] 还密。
	CodeFrequencyTooHigh ErrorCode = "frequency_too_high"
	// CodeCorruptLog 是这个会话的提醒流坏了。
	CodeCorruptLog ErrorCode = "corrupt_schedule_log"
	// CodePersistenceUncertain 是那道落盘检查点没走完，这次结果作不了准。
	CodePersistenceUncertain ErrorCode = "persistence_uncertain"
	// CodeInternal 是一个不适合外露的失败的兜底。
	CodeInternal ErrorCode = "internal_error"
)

// ToolError 是那三件工具交回去的错误值。
//
// 源: packages/schedule/schedule/src/types.ts:125-197
//
// 新增: DSH 是十个各自封闭的接口。Go 这边是一个结构体，两个只属于
// [CodePersistenceUncertain] 的字段靠 omitempty 退场——它们在别的码上永远是零值，
// 而在那个码上 Operation 永远非空，所以这一次 omitempty 表达的正是
// 「这个键在这一支上不存在」，不是把一个合法的空值弄丢了。
type ToolError struct {
	// Code 是那十个码之一。
	Code ErrorCode `json:"code"`
	// Message 是给模型看的那句话，所以是英文。
	Message string `json:"message"`
	// Operation 只在 [CodePersistenceUncertain] 上。
	Operation PersistenceOperation `json:"operation,omitempty"`
	// ID 只在 [CodePersistenceUncertain] 上，而且只在那次操作已经有身份时才有。
	ID ID `json:"id,omitempty"`
}

// CodeScheduleNotFound 是删除一个不存在的 id 时那句**不算错误**的说明。
//
// 源: packages/schedule/schedule/src/types.ts:208
//
// 它不在 [ErrorCode] 里，这是有意的：删一个已经响完了的提醒是一次正常的、
// 幂等的操作，报成错误会逼模型去处理一件根本不用处理的事。
const CodeScheduleNotFound = "schedule_not_found"

// DeleteResult 是 schedule_delete 成功时的那个值，包括「没找到」那一支。
//
// 源: packages/schedule/schedule/src/types.ts:205-208（ScheduleDeleteResult）
type DeleteResult struct {
	// ID 是被点名的那条。
	ID ID `json:"id"`
	// Deleted 表示这次真的删掉了一条。
	//
	// 这里**不能**加 omitempty：false 是这个字段最有信息量的取值，
	// 省掉它就等于把「没找到」那一支排成了「删掉了」那一支缺一个键。
	Deleted bool `json:"deleted"`
	// Code 在 Deleted 为假时是 [CodeScheduleNotFound]，为真时不出现。
	Code string `json:"code,omitempty"`
}
