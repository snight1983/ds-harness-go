// 本文件的作用：schedule_create / schedule_list / schedule_delete 这三件工具——
// 它们那份封闭的输出契约、每一次改动前后各一道的落盘屏障、那条「一个 agent 一次
// 一件」的串行，以及为什么每一件的第一行都要先问「这次调用真是我这个属主发的吗」。
//
// 源: packages/schedule/schedule/src/tools.ts

package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
)

// 这三个是三件工具在模型那边的名字。
//
// 源: packages/schedule/schedule/src/tools.ts:318,400,420
const (
	// CreateToolName 建一条提醒。
	CreateToolName = "schedule_create"
	// ListToolName 列出此刻活着的那些提醒。
	ListToolName = "schedule_list"
	// DeleteToolName 撤掉一条还活着的提醒。
	DeleteToolName = "schedule_delete"
)

// 这三段是给模型看的工具说明，所以是英文。
//
// 源: packages/schedule/schedule/src/tools.ts:147-162
var (
	createDescription = "Create one reminder in the current session. Supply a non-empty prompt and " +
		"exactly one selector: a positive safe-integer after_seconds delay, at as a strict offset " +
		"date-time or local date/time object, or safe-integer every_seconds of at least " +
		strconv.Itoa(MinEveryIntervalSeconds) + ". " +
		"Fixed-rate reminders stay creation-aligned, skip missed occurrences, and batch one latest " +
		"occurrence per overdue rule. " +
		"Delivery is session-local: the reminder runs on time only while this session " +
		"is live and otherwise becomes overdue until the session is resumed."

	listDescription = "List every active reminder in the current session in creation order, " +
		"including its exact id, UTC target, scheduled or overdue state, and session-local delivery mode."

	deleteDescription = "Delete one active reminder in the current session by the exact id returned by " +
		"schedule_create or schedule_list. Unknown or already-finished ids return deleted false."
)

// 这四段是三件工具的参数说明。
//
// 源: packages/schedule/schedule/src/tools.ts:322-348,423
var (
	promptDescription       = "Reminder content to present when the target becomes due."
	afterSecondsDescription = "Positive safe-integer delay in seconds."
	everySecondsDescription = "Fixed-rate safe-integer interval in seconds, at least " +
		strconv.Itoa(MinEveryIntervalSeconds) + "."
	atDescription = "Absolute target as strict offset RFC 3339 or local date/time with an explicit IANA zone."
	idDescription = "Exact session-local schedule id."
)

// rawConst 把一个字面量排成 schema 上那个钉死的取值。
//
// 这几个值全是本文件里写死的字符串和布尔，排不失败，所以错误吞掉——留一条走不到
// 的错误分支既测不着，也让读的人以为它会发生。
func rawConst(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

// closed 交回一个指向 false 的指针，给 schema 上那个 additionalProperties 用。
//
// 每次新造一个：[tools.Node] 上那个字段是指针，几份 schema 共用同一个地址会让
// 任何一次误改牵连到全部。
func closed() *bool {
	value := false
	return &value
}

// sharedViewProperties 是三支视图都有的那五个键。
//
// 源: packages/schedule/schedule/src/tools.ts:38-44
//
// 每次新造一份切片：[tools.Node] 里装的是值，但 Properties 是切片，共用同一个
// 底层数组之后，任何一支往后 append 都可能踩到另一支。
func sharedViewProperties() []tools.Property {
	return []tools.Property{
		{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
		{Name: "prompt", Schema: tools.Node{Type: tools.TypeString}},
		{Name: "scheduledAt", Schema: tools.Node{Type: tools.TypeString}},
		{Name: "state", Schema: tools.Node{
			Type: tools.TypeString,
			Enum: []json.RawMessage{rawConst(string(StateScheduled)), rawConst(string(StateOverdue))},
		}},
		{Name: "deliveryMode", Schema: tools.Node{
			Type:  tools.TypeString,
			Const: rawConst(string(DeliverySessionLocal)),
		}},
	}
}

// viewBranch 造一支视图 schema：五个共有键，加上钉死的 kind，再加上这一支自己那个
// 跟着判别走的整数键（[KindAt] 那一支没有）。
//
// 源: packages/schedule/schedule/src/tools.ts:46-73
func viewBranch(kind Kind, numericKey string) tools.Node {
	properties := append(sharedViewProperties(), tools.Property{
		Name:   "kind",
		Schema: tools.Node{Type: tools.TypeString, Const: rawConst(string(kind))},
	})
	if numericKey != "" {
		properties = append(properties, tools.Property{
			Name: numericKey, Schema: tools.Node{Type: tools.TypeInteger},
		})
	}
	required := make([]string, 0, len(properties))
	for _, property := range properties {
		required = append(required, property.Name)
	}
	return tools.Node{
		Type:                 tools.TypeObject,
		Properties:           properties,
		Required:             required,
		AdditionalProperties: closed(),
	}
}

// viewSchema 是那三支封闭视图的联合。
//
// 源: packages/schedule/schedule/src/tools.ts:75
//
// 三支靠 kind 上那个钉死的取值互斥，所以「恰好命中一支」这条判定永远有确切答案。
func viewSchema() tools.Node {
	return tools.Node{OneOf: []tools.Node{
		viewBranch(KindAfter, "afterSeconds"),
		viewBranch(KindAt, ""),
		viewBranch(KindEvery, "everySeconds"),
	}}
}

// basicErrorSchema 造一支只有 code 和 message 的封闭错误 schema，code 钉死。
//
// 源: packages/schedule/schedule/src/tools.ts:77-87
func basicErrorSchema(code ErrorCode) tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "code", Schema: tools.Node{Type: tools.TypeString, Const: rawConst(string(code))}},
			{Name: "message", Schema: tools.Node{Type: tools.TypeString}},
		},
		Required:             []string{"code", "message"},
		AdditionalProperties: closed(),
	}
}

// errorSchemas 是那十支错误，九支基本形状加上落盘不确定那一支。
//
// 源: packages/schedule/schedule/src/tools.ts:89-115
//
// 落盘那一支多两个键：operation 必填，id 可有可无——一次还没分配到身份的创建
// 报不出 id 来。
func errorSchemas() []tools.Node {
	basic := []ErrorCode{
		CodeInvalidPrompt,
		CodeInvalidSelector,
		CodeInvalidRule,
		CodeInvalidTimeZone,
		CodeNotFuture,
		CodeTimeOutOfRange,
		CodeFrequencyTooHigh,
		CodeCorruptLog,
		CodeInternal,
	}
	schemas := make([]tools.Node, 0, len(basic)+1)
	for _, code := range basic {
		schemas = append(schemas, basicErrorSchema(code))
	}
	return append(schemas, tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "code", Schema: tools.Node{
				Type: tools.TypeString, Const: rawConst(string(CodePersistenceUncertain)),
			}},
			{Name: "message", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "operation", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: []json.RawMessage{
					rawConst(string(OperationCreate)),
					rawConst(string(OperationList)),
					rawConst(string(OperationDelete)),
				},
			}},
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
		},
		Required:             []string{"code", "message", "operation"},
		AdditionalProperties: closed(),
	})
}

// createOutputSchema 是 schedule_create 的输出契约：一份视图，或者一支错误。
//
// 源: packages/schedule/schedule/src/tools.ts:117
func createOutputSchema() tools.Node {
	return tools.Node{OneOf: append([]tools.Node{viewSchema()}, errorSchemas()...)}
}

// listOutputSchema 是 schedule_list 的输出契约：一串视图，或者一支错误。
//
// 源: packages/schedule/schedule/src/tools.ts:118-123
func listOutputSchema() tools.Node {
	items := viewSchema()
	return tools.Node{OneOf: append(
		[]tools.Node{{Type: tools.TypeArray, Items: &items}},
		errorSchemas()...,
	)}
}

// deleteOutputSchema 是 schedule_delete 的输出契约：删掉了、没找到、或者一支错误。
//
// 源: packages/schedule/schedule/src/tools.ts:124-145
//
// 前两支靠 deleted 上那个钉死的布尔互斥，而且「没找到」那一支多一个必填的 code
// 键——两支都是封闭对象，所以一份结果绝不会同时命中两支。
func deleteOutputSchema() tools.Node {
	deleted := tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "deleted", Schema: tools.Node{Type: tools.TypeBoolean, Const: rawConst(true)}},
		},
		Required:             []string{"id", "deleted"},
		AdditionalProperties: closed(),
	}
	missing := tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "deleted", Schema: tools.Node{Type: tools.TypeBoolean, Const: rawConst(false)}},
			{Name: "code", Schema: tools.Node{
				Type: tools.TypeString, Const: rawConst(CodeScheduleNotFound),
			}},
		},
		Required:             []string{"id", "deleted", "code"},
		AdditionalProperties: closed(),
	}
	return tools.Node{OneOf: append([]tools.Node{deleted, missing}, errorSchemas()...)}
}

// encodeValue 把一个工具结果排成那份权威的 JSON 值。
//
// 新增: 走 [encoding/json.Encoder] 加 SetEscapeHTML(false)，理由和 [jsonString]
// 逐字相同：默认那份转义会把提醒正文里的 < > & 写成 < 这种样子，而模型看到的
// 正是这段字节（见 [renderValue]）。DSH 那边 JSON.stringify 不转义，两边给模型的
// 必须是同一句话。
func encodeValue(value any) (json.RawMessage, error) {
	encoded, err := marshalNoEscape(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

// renderValue 是三件工具共用的那份确定性呈现：把那个权威值原样交给模型。
//
// 源: packages/schedule/schedule/src/tools.ts:164-169
//
// 值在到这里之前已经被工具运行时按输出契约验过了，所以这里不再解一遍——解一遍
// 只会多出一条永远走不到的错误分支。
func renderValue(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
	return llm.Content{llm.TextBlock{Text: string(value)}}, nil
}

// presentCall 是这三件工具进行中那张通用卡片。
//
// 源: packages/schedule/schedule/src/tools.ts:171-174
//
// 新增: DSH 的 rawInput 是 unknown，一个裸字符串直接就能放；Go 侧它是
// [encoding/json.RawMessage]，所以先排一遍。排不出去就留空——呈现是纯函数，
// 不许失败。
func presentCall(title string, kind tools.CallKind, rawInput string) tools.CallView {
	view := tools.GenericCallView{Title: title, Kind: kind}
	if rawInput != "" {
		if encoded, err := json.Marshal(rawInput); err == nil {
			view.RawInput = encoded
		}
	}
	return view
}

// internalError 是那些不适合外露的失败共用的那份稳定结果。
//
// 源: packages/schedule/schedule/src/tools.ts:176-179
func internalError() ToolError {
	return ToolError{Code: CodeInternal, Message: "The schedule operation failed."}
}

// corruptLogError 是「这个会话的提醒流坏了」那份稳定结果。
//
// 源: packages/schedule/schedule/src/tools.ts:198-201
//
// 它**不带**折日志时报出来的那句话：那句是给运维的诊断，里面有日志内部的形状；
// 模型这一侧只需要知道这条流不可信。
func corruptLogError() ToolError {
	return ToolError{Code: CodeCorruptLog, Message: "The session schedule log is corrupt."}
}

// persistenceError 是落盘不确定那份稳定结果，带上这次操作的身份。
//
// 源: packages/schedule/schedule/src/tools.ts:203-214
//
// 那句话告诉模型该怎么办（重跑一次 schedule_list 再说），而不是告诉它落盘为什么
// 没成——后者是运维的事，见 [PersistenceError]。
func persistenceError(operation PersistenceOperation, id ID) ToolError {
	return ToolError{
		Code: CodePersistenceUncertain,
		Message: "Schedule persistence is uncertain; retry with schedule_list " +
			"before relying on this result.",
		Operation: operation,
		ID:        id,
	}
}

// toolErrorFrom 把一个包内错误折成那份封闭的工具结果。
//
// 源: packages/schedule/schedule/src/tools.ts:216-228
//
// 三支：模型自己写错了的（原样透出它那个码和那句话）、日志坏了的（换成那句固定的）、
// 剩下的一律兜成 internal——一个没预料到的失败不该把它的文本带给模型。
func toolErrorFrom(err error) ToolError {
	var input *InputError
	if errors.As(err, &input) {
		return ToolError{Code: input.Code, Message: input.Message}
	}
	var corrupt *LogError
	if errors.As(err, &corrupt) {
		return corruptLogError()
	}
	return internalError()
}

// safeInteger 把模型给的那个 JSON number 折成一个安全整数。
//
// 源: packages/schedule/schedule/src/tools.ts:276,279 (Number.isSafeInteger)
//
// 新增: 走 float64 而不是 [encoding/json.Number]，是为了和 DSH 收下**同样**的一组
// 入参：JS 那边 60.0 和 60 是同一个数，`Number.isSafeInteger(60.0)` 为真；换成
// json.Number 的话 "60.0" 解不出整数，于是同一个模型在两边表现不一样。
//
// 不查 NaN 和无穷：JSON 里根本写不出这两个字面量，而 1e400 这种在解的那一步就报错了。
func safeInteger(raw json.RawMessage) (int64, bool) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	if value != math.Trunc(value) || math.Abs(value) > float64(maxSafeInteger) {
		return 0, false
	}
	return int64(value), true
}

// createArgs 是 schedule_create 那份已经拆开的入参。
//
// 三个选择器都是 [encoding/json.RawMessage]：**没写**和写了一个解不动的值在这里是
// 两回事，前者是那条「恰好一个」的计数，后者是一次规则错误。
type createArgs struct {
	prompt       string
	afterSeconds json.RawMessage
	at           json.RawMessage
	everySeconds json.RawMessage
	selectors    int
}

// createArgKeys 是 schedule_create 认得的全部键。
//
// 源: packages/schedule/schedule/src/tools.ts:259-263
var createArgKeys = []string{"prompt", "after_seconds", "at", "every_seconds"}

// selectorError 是那条「恰好一个选择器」没守住时的结果。
//
// 源: packages/schedule/schedule/src/tools.ts:267-271
func selectorError() ToolError {
	return ToolError{
		Code:    CodeInvalidSelector,
		Message: "schedule_create accepts exactly one of after_seconds, at, or every_seconds.",
	}
}

// parseCreateArgs 验那几条 schema 表达不出来的约束，并把入参拆开。
//
// 源: packages/schedule/schedule/src/tools.ts:252-289
//
// schema 那一层管得了「prompt 是个字符串」「at 是这两种形状之一」，管不了
// 「三个选择器恰好来一个」，也管不了「不许出现别的键」——参数根在 DSH 那边是开的。
// 所以这一段先照着键名把入参当一张表读一遍。
//
// 新增: DSH 靠 `Object.keys(args)` 数键。Go 这边先解成
// map[string]json.RawMessage，效果一样，而且顺带把「同一个键写了两遍」也归一了。
func parseCreateArgs(raw json.RawMessage) (createArgs, *ToolError) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		failure := selectorError()
		return createArgs{}, &failure
	}
	for key := range object {
		if !containsString(createArgKeys, key) {
			failure := selectorError()
			return createArgs{}, &failure
		}
	}

	args := createArgs{
		afterSeconds: object["after_seconds"],
		at:           object["at"],
		everySeconds: object["every_seconds"],
	}
	for _, selector := range []json.RawMessage{args.afterSeconds, args.at, args.everySeconds} {
		if selector != nil {
			args.selectors++
		}
	}
	if args.selectors != 1 {
		failure := selectorError()
		return createArgs{}, &failure
	}

	// prompt 由参数 schema 保证是个字符串；解不动只会是调用方绕过了运行时，
	// 那和「正文是空的」一样处理。
	_ = json.Unmarshal(object["prompt"], &args.prompt)
	// 这里只验，**不**把去过空白的那份写回去：真正落进记录的那次归一由
	// CreateAfterRecord 那三个构造器各自再做一遍，两处都做才能保证一条绕过本函数
	// 直接调构造器的路径也拿到同样的正文。
	if _, err := normalizePrompt(args.prompt); err != nil {
		failure := toolErrorFrom(err)
		return createArgs{}, &failure
	}

	if args.afterSeconds != nil {
		seconds, ok := safeInteger(args.afterSeconds)
		if !ok || seconds <= 0 {
			failure := ToolError{Code: CodeInvalidRule, Message: "after_seconds must be a positive safe integer."}
			return createArgs{}, &failure
		}
	}
	if args.everySeconds != nil {
		seconds, ok := safeInteger(args.everySeconds)
		switch {
		case !ok:
			failure := ToolError{Code: CodeInvalidRule, Message: "every_seconds must be a safe integer."}
			return createArgs{}, &failure
		case seconds < MinEveryIntervalSeconds:
			failure := ToolError{
				Code:    CodeFrequencyTooHigh,
				Message: fmt.Sprintf("every_seconds must be at least %d.", MinEveryIntervalSeconds),
			}
			return createArgs{}, &failure
		}
	}
	return args, nil
}

// containsString 是那张认得的键名表上的成员判断。
func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// toolSet 是这三件工具共用的那几样：属主、落盘屏障、串行闸，以及那条
// 「有东西落盘了」的通知。
//
// 源: packages/schedule/schedule/src/tools.ts:299-304
type toolSet struct {
	owner        agent.Agent
	sessions     Sessions
	transactions *transactions
	now          func() time.Time
	// onDurableChange 在每一次成功的屏障之后被叫一下，让那份定时器投影重算。
	onDurableChange func()
}

// owns 问「这次调用真是我这个属主发的吗」。
//
// 源: packages/schedule/schedule/src/tools.ts:352,405,431
//
// 这三件工具登记在属主自己那把作用域钥匙上，所以正常路径下永远成立。它守的是
// 一次装配错误：同一份定义被登记到了别人的作用域上，那时候这三件工具会往一段
// 不归它管的会话里写东西。
//
// 新增: DSH 比的是 `exec.agent !== agent`，那里 exec.agent 就是 agent 对象。
// Go 这边执行落在的是一把作用域钥匙，所以比的是属主那把钥匙。
func (s *toolSet) owns(exec *tools.RunContext) bool {
	if exec == nil {
		return false
	}
	return exec.Agent == s.owner.Scope().Key()
}

// notifyDurableChange 把「有东西落盘了」告诉那份投影。
//
// 源: packages/schedule/schedule/src/tools.ts:307-314
//
// 新增: DSH 拿 try/catch 罩住这个回调，因为那里它是外面交进来的任意闭包。Go 这边
// 不罩：一个 panic 是缺陷，吞掉它只会让那份投影在往后每一次改动上静悄悄地不更新。
func (s *toolSet) notifyDurableChange() {
	if s.onDurableChange != nil {
		s.onDurableChange()
	}
}

// preflight 要求走成一次落盘检查点，走不成就换成那份不泄底的结果。
//
// 源: packages/schedule/schedule/src/tools.ts:237-250
func (s *toolSet) preflight(
	ctx context.Context,
	operation PersistenceOperation,
	id ID,
) *ToolError {
	if err := flushPersistence(ctx, s.sessions, s.owner.Session()); err != nil {
		failure := persistenceError(operation, id)
		return &failure
	}
	return nil
}

// foldForTool 折一遍此刻这份日志，把一条坏掉的流折成那份稳定结果。
//
// 源: packages/schedule/schedule/src/tools.ts:221-228
func (s *toolSet) foldForTool() (Folded, *ToolError) {
	session := s.owner.Session()
	folded, err := FoldEvents(session.Events(), session.Header().SeedLength)
	if err != nil {
		failure := toolErrorFrom(err)
		return Folded{}, &failure
	}
	return folded, nil
}

// serialize 把一件操作排进这个 agent 那条队，并把它的产出捞出来。
//
// 源: packages/schedule/schedule/src/tools.ts:186-196
//
// 新增: DSH 在轮到自己之后查一次 `signal.aborted`，中止了就交回那份 internal
// 占位结果，等注册表把它换成规范的 ABORTED。Go 这边那次检查在 [transactions.run]
// 的**等待**里就做了，而且交回的是 ctx 那个原因——工具运行时认得它，会自己产出
// 规范的中止结果，用不着占位。
func (s *toolSet) serialize(
	ctx context.Context,
	body func(context.Context) (json.RawMessage, error),
) (json.RawMessage, error) {
	var value json.RawMessage
	err := s.transactions.run(ctx, s.owner, func(runCtx context.Context) error {
		var bodyErr error
		value, bodyErr = body(runCtx)
		return bodyErr
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

// newCreateTool 造那件 schedule_create 工具。
//
// 源: packages/schedule/schedule/src/tools.ts:317-397
func (s *toolSet) newCreateTool() *tools.Definition {
	return &tools.Definition{
		Name:        CreateToolName,
		Description: createDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "prompt", Schema: tools.Node{Type: tools.TypeString, Description: promptDescription}},
				{Name: "after_seconds", Schema: tools.Node{Type: tools.TypeNumber, Description: afterSecondsDescription}},
				{Name: "every_seconds", Schema: tools.Node{Type: tools.TypeNumber, Description: everySecondsDescription}},
				{Name: "at", Schema: tools.Node{
					Description: atDescription,
					OneOf: []tools.Node{
						{Type: tools.TypeString},
						{
							Type: tools.TypeObject,
							Properties: []tools.Property{
								{Name: "date", Schema: tools.Node{Type: tools.TypeString}},
								{Name: "time", Schema: tools.Node{Type: tools.TypeString}},
								{Name: "time_zone", Schema: tools.Node{Type: tools.TypeString}},
							},
							Required:             []string{"date", "time", "time_zone"},
							AdditionalProperties: closed(),
						},
					},
				}},
			},
			Required: []string{"prompt"},
		},
		Output:  tools.OutputDefinition{Schema: createOutputSchema(), Render: renderValue},
		Execute: s.create,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input struct {
				Prompt string `json:"prompt"`
			}
			_ = json.Unmarshal(args, &input)
			return presentCall("Create reminder", tools.CallOther, input.Prompt)
		},
	}
}

// create 是 schedule_create 的体。
//
// 源: packages/schedule/schedule/src/tools.ts:351-395
//
// 两道屏障中间夹着这次追加：前一道保证「我据以分配 id 的那段日志已经存住了」，
// 后一道保证「我刚写下的这条 create 已经存住了」。少了后一道，模型会拿到一条
// 它下次开会话时找不着的提醒。
func (s *toolSet) create(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	if !s.owns(exec) {
		return encodeValue(internalError())
	}
	input, invalid := parseCreateArgs(args)
	if invalid != nil {
		return encodeValue(*invalid)
	}
	return s.serialize(ctx, func(runCtx context.Context) (json.RawMessage, error) {
		if uncertain := s.preflight(runCtx, OperationCreate, ""); uncertain != nil {
			return encodeValue(*uncertain)
		}
		s.notifyDurableChange()
		folded, corrupt := s.foldForTool()
		if corrupt != nil {
			return encodeValue(*corrupt)
		}
		id := AllocateID(folded)
		record, err := s.buildRecord(id, input)
		if err != nil {
			return encodeValue(toolErrorFrom(err))
		}
		if runCtx.Err() != nil {
			return nil, context.Cause(runCtx)
		}
		if err := appendChange(s.owner, Change{
			Version: ChangeVersion, Operation: OpCreate, Schedule: &record,
		}); err != nil {
			return encodeValue(internalError())
		}
		if barrier := s.preflight(runCtx, OperationCreate, id); barrier != nil {
			return encodeValue(*barrier)
		}
		s.notifyDurableChange()
		view, err := NewView(record, s.now())
		if err != nil {
			return encodeValue(internalError())
		}
		return encodeValue(view)
	})
}

// buildRecord 按那个唯一的选择器造出记录。
//
// 源: packages/schedule/schedule/src/tools.ts:363-375
//
// 次序照 DSH：at 先于 after_seconds 先于 every_seconds。这里的次序其实不重要
// ——[parseCreateArgs] 已经保证了三个里恰好有一个——但照抄能让两边的分支一一对上。
func (s *toolSet) buildRecord(id ID, input createArgs) (Record, error) {
	now := s.now()
	switch {
	case input.at != nil:
		return CreateAtRecord(id, input.prompt, input.at, now)
	case input.afterSeconds != nil:
		seconds, _ := safeInteger(input.afterSeconds)
		return CreateAfterRecord(id, input.prompt, seconds, now)
	default:
		seconds, _ := safeInteger(input.everySeconds)
		return CreateEveryRecord(id, input.prompt, seconds, now)
	}
}

// newListTool 造那件 schedule_list 工具。
//
// 源: packages/schedule/schedule/src/tools.ts:399-417
func (s *toolSet) newListTool() *tools.Definition {
	return &tools.Definition{
		Name:        ListToolName,
		Description: listDescription,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output:      tools.OutputDefinition{Schema: listOutputSchema(), Render: renderValue},
		Execute:     s.list,
		PresentCall: func(json.RawMessage) tools.CallView {
			return presentCall("List reminders", tools.CallRead, "")
		},
	}
}

// list 是 schedule_list 的体。
//
// 源: packages/schedule/schedule/src/tools.ts:404-414
//
// 它也过屏障、也走那条串行队：一份「读」在这里同样必须建立在一段存住了的前缀上，
// 否则模型会照着一份随时可能消失的清单去做判断。
func (s *toolSet) list(
	ctx context.Context,
	_ json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	if !s.owns(exec) {
		return encodeValue(internalError())
	}
	return s.serialize(ctx, func(runCtx context.Context) (json.RawMessage, error) {
		if uncertain := s.preflight(runCtx, OperationList, ""); uncertain != nil {
			return encodeValue(*uncertain)
		}
		s.notifyDurableChange()
		folded, corrupt := s.foldForTool()
		if corrupt != nil {
			return encodeValue(*corrupt)
		}
		now := s.now()
		// 这份切片必须是 make 出来的，不能是 nil：一个 nil 切片排出去是 null，
		// 而输出契约说的是数组——一份空清单会当场验不过。
		views := make([]View, 0, len(folded.Active))
		for _, record := range folded.Active {
			view, err := NewView(record, now)
			if err != nil {
				return encodeValue(internalError())
			}
			views = append(views, view)
		}
		return encodeValue(views)
	})
}

// newDeleteTool 造那件 schedule_delete 工具。
//
// 源: packages/schedule/schedule/src/tools.ts:419-455
func (s *toolSet) newDeleteTool() *tools.Definition {
	return &tools.Definition{
		Name:        DeleteToolName,
		Description: deleteDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "id", Schema: tools.Node{Type: tools.TypeString, Description: idDescription}},
			},
			Required: []string{"id"},
		},
		Output:  tools.OutputDefinition{Schema: deleteOutputSchema(), Render: renderValue},
		Execute: s.delete,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var input struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(args, &input)
			return presentCall("Delete reminder", tools.CallOther, input.ID)
		},
	}
}

// deleteArgs 是 schedule_delete 的入参。
type deleteArgs struct {
	ID string `json:"id"`
}

// delete 是 schedule_delete 的体。
//
// 源: packages/schedule/schedule/src/tools.ts:426-452
//
// 删一个不活着的 id **不是**错误：它交回 deleted false 加上那句说明。理由写在
// [CodeScheduleNotFound] 上——那是一次正常的、幂等的操作。
func (s *toolSet) delete(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input deleteArgs
	_ = json.Unmarshal(args, &input)
	if input.ID == "" || strings.TrimSpace(input.ID) != input.ID {
		return encodeValue(ToolError{
			Code:    CodeInvalidRule,
			Message: "schedule_delete id must be non-empty without surrounding whitespace.",
		})
	}
	id := ID(input.ID)
	if !s.owns(exec) {
		return encodeValue(internalError())
	}
	return s.serialize(ctx, func(runCtx context.Context) (json.RawMessage, error) {
		if uncertain := s.preflight(runCtx, OperationDelete, id); uncertain != nil {
			return encodeValue(*uncertain)
		}
		s.notifyDurableChange()
		folded, corrupt := s.foldForTool()
		if corrupt != nil {
			return encodeValue(*corrupt)
		}
		if !hasRecord(folded.Active, id) {
			return encodeValue(DeleteResult{ID: id, Deleted: false, Code: CodeScheduleNotFound})
		}
		if runCtx.Err() != nil {
			return nil, context.Cause(runCtx)
		}
		if err := appendChange(s.owner, Change{
			Version: ChangeVersion, Operation: OpDelete, ID: id,
		}); err != nil {
			return encodeValue(internalError())
		}
		if barrier := s.preflight(runCtx, OperationDelete, id); barrier != nil {
			return encodeValue(*barrier)
		}
		s.notifyDurableChange()
		return encodeValue(DeleteResult{ID: id, Deleted: true})
	})
}

// hasRecord 问这个 id 此刻还活着吗。
func hasRecord(active []Record, id ID) bool {
	for _, record := range active {
		if record.ID == id {
			return true
		}
	}
	return false
}

// registerTools 把三件工具装上一个作用域，交回把它们一起摘下来的函数。
//
// 源: packages/schedule/schedule/src/tools.ts:299-467
//
// 中途失败就按反序摘干净：半装上去意味着模型手上有一件建得了提醒、却查不了清单的
// 工具，那比一件都没有更难解释。
func registerTools(
	ctx context.Context,
	runtime *tools.Runtime,
	owner *scope.Scope,
	set *toolSet,
) (func(context.Context) error, error) {
	installed := make([]func(context.Context) error, 0, 3)
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		return errors.Join(failures...)
	}
	for _, definition := range []*tools.Definition{
		set.newCreateTool(), set.newListTool(), set.newDeleteTool(),
	} {
		dispose, err := runtime.Register(ctx, owner, definition)
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("schedule: 装 %s 失败：%w", definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
