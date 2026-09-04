// 本文件的作用：进这个包的东西在边界上要过的那几道检查——会话头、构造 seed 里
// 的每一条事件、以及那几种「带着一条消息」的事件的形状。
//
// 源: packages/core/session/src/index.ts:95-372

package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ErrInvalidHeader 是一份会话头本身不成立。
//
// 新增: DSH 侧全是 `throw new Error(字符串)`。分类的理由和
// [github.com/snight1983/ds-harness-go/sessionlog] 那几个哨兵逐字相同：Go 的错误是要被 errors.Is 分派的。
var ErrInvalidHeader = errors.New("harness/session: 会话头不合法")

// ErrInvalidSeed 是构造 seed 里有一条事件过不了检查。
//
// 该做的事：**别建这个会话**。一份坏 seed 造出来的活日志没有任何持久化后端
// 存得下，而它「看起来能跑」，所以不会有别的东西报警。
var ErrInvalidSeed = errors.New("harness/session: 构造 seed 不合法")

// ErrInvalidAppend 是这一次追加不合法，日志**没有**变。
var ErrInvalidAppend = errors.New("harness/session: 这次追加不合法")

// ErrCorruptLog 是活日志和从它折出来的表面对不上了。
//
// 新增: 上游没有这一条，因为它那边「seq 恒等于下标」，对不上是不可能的。本仓库的
// 日志会从最老的一头弹出事件（见 docs/session-log-limit.md），于是 seq 和下标差着
// 一个起点，凡是按 seq 取事件的地方都得减完再校验。校验不过说明这两份东西已经
// 分了岔，报错而不是 panic——一个在服务端长期跑着的组件不该因为一份坏存档
// 把整个进程带走。
var ErrCorruptLog = errors.New("harness/session: 活日志和表面对不上")

// ErrSessionExists 是这个标识上已经有一个活着的会话了。
var ErrSessionExists = errors.New("harness/session: 同名会话已存在")

// ErrNotLive 是这个会话对象不是本存储里活着的那一个。
var ErrNotLive = errors.New("harness/session: 会话不在这个存储里活着")

// ErrAlreadyAttached 是这个会话对象已经登记在某个存储里了。
//
// 它和 [ErrSessionExists] 分开：后者说的是「这个**名字**被占了」，这一条说的是
// 「这个**对象**已经有主了」。两者可以各自单独发生——一个已登记的会话拿去别的
// 存储登记，名字那边是空的。
var ErrAlreadyAttached = errors.New("harness/session: 这个会话已经登记在某个存储里了")

// ErrAlreadyAnnounced 是这个会话的创建公布已经开始过了。
//
// 一个观察者在创建回调里又去公布同一个会话，撞的也是这一条。
var ErrAlreadyAnnounced = errors.New("harness/session: 这个会话已经公布过了")

// legacyHeaderDelta 是 DSH 早年那种增量请求头事件的类型名。
//
// 源: packages/core/session/src/index.ts:215-217、363-366
//
// 它不在 [github.com/snight1983/ds-harness-go/sessionlog] 的词汇表里，本包认得它**只是为了报一句说得清
// 的话**：一份用它写下的旧日志读进来时，「不认识的类型」这句诊断帮不上忙，
// 「这是已经删掉的旧格式」才帮得上。
const legacyHeaderDelta sessionlog.EventType = "request/header-delta"

// legacyFallbackReason 是 DSH 早年那个跟着增量编解码一起删掉的请求头原因。
//
// 源: packages/core/session/src/index.ts:367-371
const legacyFallbackReason = "fallback"

// validateSessionHeader 验一份会话头，并确认它说的就是 id 这个会话。
//
// 源: packages/core/session/src/index.ts:95-134
//
// 新增: DSH 那边前四分之三是「这是不是一个普通对象」「这个字段是不是 number」
// 之类的类型探测，因为它拿到的是 unknown。Go 这边
// [github.com/snight1983/ds-harness-go/sessionlog.SessionHeader] 已经把形状钉死了，剩下的只有取值范围。
// Number.isSafeInteger 同理消失：那几个字段在 Go 里是 int / int64，逐位精确。
//
// 新增: DSH 的 validateRestoredSessionHeader 在这道检查前面多一道 JS 原型检查
// （挡住 class 实例和 Object.create(null)）。Go 里没有原型，恢复路径和新建路径
// 验的是同一件事，所以只有这一个函数。
func validateSessionHeader(id sessionlog.SessionID, header sessionlog.SessionHeader) error {
	if header.Version != sessionlog.FormatVersion {
		return fmt.Errorf(
			"%w: session header version must be %d, got %d",
			ErrInvalidHeader, sessionlog.FormatVersion, header.Version,
		)
	}
	if header.ID != id {
		return fmt.Errorf(
			"%w: session header id %q does not match session id %q",
			ErrInvalidHeader, string(header.ID), string(id),
		)
	}
	if header.CreatedAt < 0 {
		return fmt.Errorf("%w: session header createdAt must be a non-negative safe integer", ErrInvalidHeader)
	}
	// 新增: DSH 在这里校验 `cwd` 是不是一条绝对路径（node 的 path.isAbsolute）。
	// 本仓库这一项是 [sessionlog.SessionHeader.WorkspaceID]，一个不透明标识，
	// 没有「绝对性」这种可校验的形状——它合不合法只有一个意思：工作区登记册里认不认得
	// 这一行。那件事在挂载的那一刻由 workspace 包判（一次相等），不在这里判，
	// 也判不了：本包不认识工作区登记册。
	if header.SeedLength < 0 {
		return fmt.Errorf("%w: session header seedLength must be a non-negative safe integer", ErrInvalidHeader)
	}
	if header.Origin != "" && header.Origin != sessionlog.OriginSubagent {
		return fmt.Errorf("%w: session header origin must be %q", ErrInvalidHeader, sessionlog.OriginSubagent)
	}
	if header.DelegationDepth < 0 {
		return fmt.Errorf("%w: session header delegationDepth must be a non-negative safe integer", ErrInvalidHeader)
	}
	return nil
}

// snapshotSessionHeader 给出这个会话对外公布的那份创建元数据。
//
// 源: packages/core/session/src/index.ts:148-155
//
// source 为 nil 表示没人给头，这里合成一份最小的（版本号、标识、当下）。
// 给了就用它，验过之后交出去。
//
// 新增: DSH 在这里先 snapshotJsonValue 再验，排不成 JSON 就报
// 「is not losslessly JSON-serializable」。Go 里
// [github.com/snight1983/ds-harness-go/sessionlog.SessionHeader] 每个字段都是标量，那句诊断没有对应物。
func snapshotSessionHeader(
	id sessionlog.SessionID,
	source *sessionlog.SessionHeader,
	now func() int64,
) (sessionlog.SessionHeader, error) {
	header := sessionlog.SessionHeader{
		Version:   sessionlog.FormatVersion,
		ID:        id,
		CreatedAt: now(),
	}
	if source != nil {
		header = *source
	}
	if err := validateSessionHeader(id, header); err != nil {
		return sessionlog.SessionHeader{}, err
	}
	return header, nil
}

// validateSeedEvent 验构造 seed 里第 index 条事件。
//
// 源: packages/core/session/src/index.ts:212-250
//
// 新增: DSH 那边这道检查同时在做三件事：判「这是不是一个事件信封」、判
// 「里面有没有多余的键」、判「取值合不合法」。前两件在 Go 里由
// [github.com/snight1983/ds-harness-go/sessionlog.Event] 兑现——它的 UnmarshalJSON 本来就拒收信封上
// 不认识的键，而一个在 Go 代码里直接构造出来的 Event 根本没有「多余的键」这
// 种状态。所以这里只剩第三件。
//
// 同样消失的还有：`data !== undefined`（Go 的 Data 为空就是空负载，
// [github.com/snight1983/ds-harness-go/sessionlog.Event.MarshalJSON] 会把它排成 `{}`，没有第三种状态）、
// `ignorable !== true`（bool 只有两个值）、以及 seq / time 那两道
// Number.isSafeInteger（int 与 int64 逐位精确，只剩下 seq 的非负）。
func validateSeedEvent(event sessionlog.Event, index int) error {
	location := fmt.Sprintf("seed event at index %d", index)
	if event.Type == legacyHeaderDelta {
		return fmt.Errorf("%w: %s uses unsupported legacy request/header-delta format", ErrInvalidSeed, location)
	}
	if event.Type == "" {
		return fmt.Errorf("%w: %s has an invalid event envelope", ErrInvalidSeed, location)
	}
	if event.Seq < 0 {
		return fmt.Errorf("%w: %s has an invalid event envelope", ErrInvalidSeed, location)
	}
	if len(event.Data) > 0 && !json.Valid(event.Data) {
		// 这是 DSH 那句 snapshotJsonValue 回传 undefined 在 Go 里的样子：
		// seed 是一道持久化／回放边界，一段排不回去的负载现在不报，
		// 就要等到某个后端刷盘时才报，那时候已经建出一个存不下的会话了。
		return fmt.Errorf("%w: %s is not losslessly JSON-serializable", ErrInvalidSeed, location)
	}
	return validateCurrentLLMShape(event, index)
}

// validateCurrentLLMShape 挡住过时的请求头和形状坏掉的消息。
//
// 源: packages/core/session/src/index.ts:252-277
func validateCurrentLLMShape(event sessionlog.Event, index int) error {
	switch event.Type {
	case sessionlog.EventRequestHeader:
		return validateSeedRequestHeader(event, index)
	case sessionlog.EventUserMessage, sessionlog.EventAssistantMessage, sessionlog.EventToolResult:
		return validateMessageEventShape(event, fmt.Sprintf("seed %s at index %d", event.Type, index))
	default:
		return nil
	}
}

// validateSeedRequestHeader 验一条 seed 里的请求头事件。
//
// 源: packages/core/session/src/index.ts:256-268
func validateSeedRequestHeader(event sessionlog.Event, index int) error {
	var data sessionlog.RequestHeaderData
	if err := json.Unmarshal(nonEmptyData(event.Data), &data); err != nil {
		return fmt.Errorf("%w: seed request/header at index %d lacks provider/model", ErrInvalidSeed, index)
	}
	if data.Header.Config.Provider == "" || data.Header.Config.Model == "" {
		return fmt.Errorf("%w: seed request/header at index %d lacks provider/model", ErrInvalidSeed, index)
	}
	// 新增: DSH 那条「reasoningEffort 在的话必须是非空字符串」在 Go 里落空了：
	// ReasoningEffortID 是具名字符串，空串**就是**「没给」，两者同一个值。
	return validateAdapterDefaults(data.Header, index)
}

// validateAdapterDefaults 验一份耐久请求头上的适配器默认值标记。
//
// 源: packages/core/session/src/index.ts:280-298
//
// 新增: DSH 验四件事——是不是普通对象、键在不在白名单里、值是不是恒为 true、
// 以及每个立着的标记在 config 里有没有对应的字段。前三件在
// [github.com/snight1983/ds-harness-go/llm.CallConfigAdapterDefaults] 上不可能违反：它是一个恰好两个
// bool 字段的结构体。所以这里只剩第四件——而它恰恰是真正有内容的那一条：
// 一个「这一项是适配器解析出来的」标记，指向一个根本不存在的字段，说明写下
// 这份头的那一方和读它的这一方对不上。
func validateAdapterDefaults(header sessionlog.EpochHeader, index int) error {
	invalid := header.AdapterDefaults.ReasoningEffort && header.Config.ReasoningEffort == "" ||
		header.AdapterDefaults.MaxTokens && header.Config.MaxTokens == 0
	if invalid {
		return fmt.Errorf("%w: seed request/header at index %d has invalid adapterDefaults", ErrInvalidSeed, index)
	}
	return nil
}

// validateMessageEventShape 只验「安全重放这条消息」所需的那几条约束。
//
// 源: packages/core/session/src/index.ts:300-360
//
// subject 是诊断里指认这条事件的那半句话，由调用方按自己的位置组装
// （seed 里是「seed user/message at index 3」，活着的日志里是
// 「session event at seq 12」）。
//
// 新增: DSH 那条「content 必须是数组」在 Go 里落空了——[github.com/snight1983/ds-harness-go/llm.Content]
// 是切片，nil 和空清单是同一个值，没有「content 是 null」这种状态。
//
// 新增: 这一整套在本仓库是新写的，不是转发。[github.com/snight1983/ds-harness-go/sessionlog.Trace] 只管
// 回合与步骤的开关，[github.com/snight1983/ds-harness-go/llm.Message] 的 UnmarshalJSON 只认来源的判别
// 标签，两者都不查「这条消息有没有身份」「角色对不对得上事件类型」。
func validateMessageEventShape(event sessionlog.Event, subject string) error {
	message, ok, err := messageOf(event)
	if err != nil {
		return fmt.Errorf("%w: %s lacks an identified message", ErrInvalidSeed, subject)
	}
	if !ok {
		return nil
	}
	if message.ID == "" {
		return fmt.Errorf("%w: %s lacks an identified message", ErrInvalidSeed, subject)
	}
	expectedRole := llm.RoleUser
	if event.Type == sessionlog.EventAssistantMessage {
		expectedRole = llm.RoleAssistant
	}
	if message.Role != expectedRole {
		return fmt.Errorf("%w: %s message must have role %q", ErrInvalidSeed, subject, expectedRole)
	}
	if message.Source == nil || message.Source.SourceKind() == "" {
		return fmt.Errorf("%w: %s message has invalid source", ErrInvalidSeed, subject)
	}
	switch event.Type {
	case sessionlog.EventAssistantMessage:
		model, isModel := message.ModelSource()
		if !isModel || model.Provider == "" || model.Model == "" {
			return fmt.Errorf("%w: %s message must have model source", ErrInvalidSeed, subject)
		}
		return nil
	case sessionlog.EventToolResult:
		return validateToolResultShape(message, subject)
	default:
		return nil
	}
}

// validateToolResultShape 验一条工具结果消息：来源、唯一那一块、以及两处调用
// 标识对得上。
//
// 源: packages/core/session/src/index.ts:339-359
func validateToolResultShape(message llm.Message, subject string) error {
	source, isTool := message.Source.(llm.ToolSource)
	if !isTool || source.CallID == "" {
		return fmt.Errorf("%w: %s message must have tool source", ErrInvalidSeed, subject)
	}
	if len(message.Content) != 1 {
		return fmt.Errorf("%w: %s message must contain one tool-result block", ErrInvalidSeed, subject)
	}
	block, isResult := message.Content[0].(llm.ToolResultBlock)
	if !isResult {
		return fmt.Errorf("%w: %s message must contain one tool-result block", ErrInvalidSeed, subject)
	}
	if block.ToolCallID != source.CallID {
		return fmt.Errorf("%w: %s message has mismatched tool call ids", ErrInvalidSeed, subject)
	}
	return nil
}

// messageOf 取出一条事件携带的那条消息。第二个返回值为假表示这个类型不带消息。
//
// 源: packages/core/session/src/index.ts:301-311
//
// 三种类型把消息放在负载的不同位置：user/message 的负载**就是**那条消息，
// 另外两种把它放在 message 字段下。
func messageOf(event sessionlog.Event) (llm.Message, bool, error) {
	payload := nonEmptyData(event.Data)
	switch event.Type {
	case sessionlog.EventUserMessage:
		var data sessionlog.UserMessageData
		if err := json.Unmarshal(payload, &data); err != nil {
			return llm.Message{}, true, err
		}
		return data.Message, true, nil
	case sessionlog.EventAssistantMessage:
		var data sessionlog.AssistantMessageData
		if err := json.Unmarshal(payload, &data); err != nil {
			return llm.Message{}, true, err
		}
		return data.Message, true, nil
	case sessionlog.EventToolResult:
		var data sessionlog.ToolResultData
		if err := json.Unmarshal(payload, &data); err != nil {
			return llm.Message{}, true, err
		}
		return data.Message, true, nil
	default:
		return llm.Message{}, false, nil
	}
}

// validateSupportedRequestHeader 挡住跟着旧增量编解码一起删掉的那两样请求头词汇。
//
// 源: packages/core/session/src/index.ts:363-372
//
// location 是诊断里指认这条事件的那半句话。sentinel 是要裹的哨兵：同一条规矩
// 在 seed 边界上和在追加路径上都要过，而两边该让调用方做的事不一样。
func validateSupportedRequestHeader(
	kind sessionlog.EventType,
	data json.RawMessage,
	location string,
	sentinel error,
) error {
	if kind == legacyHeaderDelta {
		return fmt.Errorf("%w: %s uses unsupported legacy request/header-delta format", sentinel, location)
	}
	if kind != sessionlog.EventRequestHeader {
		return nil
	}
	// DSH 这里只在 data 恰好是个普通对象时才看 reason；解不开就当没这回事，
	// 因为负载本身的形状由别处负责。Go 里同样：解不开就交给别的检查去报。
	var probe struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(nonEmptyData(data), &probe); err != nil {
		return nil
	}
	if probe.Reason == legacyFallbackReason {
		return fmt.Errorf(
			"%w: %s uses unsupported legacy request/header reason %q",
			sentinel, location, legacyFallbackReason,
		)
	}
	return nil
}

// nonEmptyData 把一段空负载补成 `{}`，好让它能被解进一个结构体。
//
// 新增: [github.com/snight1983/ds-harness-go/sessionlog.Event.MarshalJSON] 在排出去时做的是同一件事，
// 这里是它读回来那一侧的对应物——一个 Data 为 nil 的事件在 Go 里表示
// 「负载是空的」，而 encoding/json 解不动一段零长度的字节。
func nonEmptyData(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return json.RawMessage(`{}`)
	}
	return data
}
