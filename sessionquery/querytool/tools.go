// 本文件的作用：五件工具摆到工具表上的样子——名字、说明、参数 schema、那段
// 共用的指引，以及把它们一次装齐、一次摘干净的那两个函数。
//
// 源: packages/session-query/tool-session-query/src/index.ts:47-123
//
// 本文件里所有面向模型的文字（工具名、说明、参数描述、那段指引）都保持英文，
// 和本仓库其余面向模型的载荷同一条界线。中文只在注释里。

package querytool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionquery"
)

// SectionName 是这五件工具那段共用指引在提示词注册表里的名字。
//
// 源: packages/session-query/tool-session-query/src/index.ts:61
const SectionName = "tool:session-query"

// SectionOrder 是那段指引的排序位。
//
// 源: packages/session-query/tool-session-query/src/index.ts:62
//
// 100–199 是本仓库留给工具指引的那一段，113 是 DSH 给这个包定的位置。
const SectionOrder = 113

// 五件工具的名字。
//
// 源: packages/session-query/tool-session-query/src/index.ts:67,77,87,97,110
const (
	// SearchToolName 是跨会话检索。
	SearchToolName = "session_search"
	// EventSearchToolName 是会话内检索。
	EventSearchToolName = "session_event_search"
	// TraceToolName 是血统追溯。
	TraceToolName = "session_trace"
	// EventTraceToolName 是事件关系追溯。
	EventTraceToolName = "session_event_trace"
	// EventReadToolName 是事件精读。
	EventReadToolName = "session_event_read"
)

// promptText 是那段告诉模型这五件工具怎么配着用的指引。
//
// 源: packages/session-query/tool-session-query/src/index.ts:52-55
//
// 它把五件工具分成两拨：两件检索用来找，三件精读用来跟进。没有这一句的话，
// 模型倾向于只用检索、拿摘录当全部事实——而摘录本来就是截过的。
const promptText = "Use session_search to find relevant work from prior sessions, or session_event_search to search earlier " +
	"events in one session. Search results are cursor-free and workspace-scoped. Follow a useful hit with " +
	"session_trace, session_event_trace, or session_event_read when you need lineage, relationships, or exact data."

// textOutput 是五件工具共用的那份输出声明：一段纯文本。
//
// 源: packages/session-query/tool-session-query/src/index.ts:47-50
//
// 五件工具交出去的都是排好版的一整段文字（见 presentation.go），不是结构化对象。
// 这是刻意的：那些行的形状本身就是给模型看的契约。
var textOutput = tools.OutputDefinition{
	Schema: tools.Node{Type: tools.TypeString},
	Render: renderText,
}

// renderText 把那份字符串值折成给模型看的内容。
//
// 源: packages/session-query/tool-session-query/src/index.ts:49
func renderText(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return nil, err
	}
	return llm.Content{llm.TextBlock{Text: text}}, nil
}

// concurrencySafe 说这次调用可以和别的调用并行跑。
//
// 源: packages/session-query/tool-session-query/src/index.ts:91,104,119
//
// 三件只读工具都是安全的：它们一个字节都不写。两件检索工具**没有**这个标记，
// 和 DSH 一致——它们各自握着一个截止时间，让一堆全文检索同时压上去没有好处。
func concurrencySafe(json.RawMessage) bool { return true }

// sessionIDParameter 是那个「指哪个会话」的参数，四件工具共用。
//
// 源: packages/session-query/tool-session-query/src/input.ts:84-86
func sessionIDParameter() tools.Property {
	return tools.Property{
		Name: "session_id",
		Schema: tools.Node{
			Type:        tools.TypeString,
			Description: "Target session id. Omit for the current session.",
		},
	}
}

// seqParameter 是那个「指哪条事件」的参数，两件精读工具共用。
//
// 源: packages/session-query/tool-session-query/src/index.ts:101,114
func seqParameter() tools.Property {
	return tools.Property{
		Name: "seq",
		Schema: tools.Node{
			Type:        tools.TypeInteger,
			Description: "Target event sequence number.",
		},
	}
}

// availabilityNames 是 availability 那个参数的取值白名单。
//
// 源: packages/session-query/tool-session-query/src/input.ts:54
//
// 从 [sessionquery] 的常量取，不写字面量：这张白名单和引擎认得的那套值必须
// 是同一套，抄一遍就意味着以后引擎加一种来源时这里会悄悄落下。
var availabilityNames = []string{
	string(sessionquery.AvailabilityLive),
	string(sessionquery.AvailabilityPersisted),
}

// surfaceNames 是 event_surfaces / surfaces 那两个参数的取值白名单。
//
// 源: packages/session-query/tool-session-query/src/input.ts:64,78
var surfaceNames = []string{
	string(sessionquery.SurfaceCurrent),
	string(sessionquery.SurfaceShadowed),
	string(sessionquery.SurfaceLogOnly),
}

// stringArray 造一个「一串字符串」的参数。
func stringArray(description string) tools.Node {
	return tools.Node{
		Type:        tools.TypeArray,
		Items:       &tools.Node{Type: tools.TypeString},
		Description: description,
	}
}

// enumArray 造一个「一串取值受限的字符串」的参数。
func enumArray(description string, values ...string) tools.Node {
	enum := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			// 这些取值全是本文件里的字面量，排不出去是不可能的。
			continue
		}
		enum = append(enum, encoded)
	}
	return tools.Node{
		Type:        tools.TypeArray,
		Items:       &tools.Node{Type: tools.TypeString, Enum: enum},
		Description: description,
	}
}

// sessionSearchParameters 是 session_search 的参数 schema。
//
// 源: packages/session-query/tool-session-query/src/input.ts:296-307（toolInput）
//
// 属性顺序照抄 DSH：[tools.Property] 是有序切片，因为这个顺序会进提示词缓存的
// 键（见 [tools.Node] 的注释）。换个顺序不改语义，但会把整份缓存作废。
func sessionSearchParameters() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "query", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Literal full-text query over prior session history.",
			}},
			{Name: "session_ids", Schema: stringArray("Optional session ids to include.")},
			{Name: "created_at_from", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 creation-time lower bound.",
			}},
			{Name: "created_at_to", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 creation-time upper bound.",
			}},
			{Name: "parent_session_ids", Schema: stringArray("Optional direct parent session ids.")},
			{Name: "include_root_sessions", Schema: tools.Node{
				Type:        tools.TypeBoolean,
				Description: "Include sessions with no parent in the parent filter.",
			}},
			{Name: "availability", Schema: enumArray(
				"Require at least one selected source availability.", availabilityNames...)},
			{Name: "event_seq_from", Schema: tools.Node{
				Type:        tools.TypeInteger,
				Description: "Inclusive event sequence lower bound.",
			}},
			{Name: "event_seq_to", Schema: tools.Node{
				Type:        tools.TypeInteger,
				Description: "Inclusive event sequence upper bound.",
			}},
			{Name: "event_time_from", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 event-time lower bound.",
			}},
			{Name: "event_time_to", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 event-time upper bound.",
			}},
			{Name: "event_types", Schema: stringArray("Event types to include.")},
			{Name: "event_surfaces", Schema: enumArray("Event surfaces to include.", surfaceNames...)},
		},
		Required: []string{"query"},
	}
}

// eventSearchParameters 是 session_event_search 的参数 schema。
//
// 源: packages/session-query/tool-session-query/src/input.ts:69-82
func eventSearchParameters() tools.Node {
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			sessionIDParameter(),
			{Name: "query", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Literal full-text query over the target session.",
			}},
			{Name: "seq_from", Schema: tools.Node{
				Type:        tools.TypeInteger,
				Description: "Inclusive event sequence lower bound.",
			}},
			{Name: "seq_to", Schema: tools.Node{
				Type:        tools.TypeInteger,
				Description: "Inclusive event sequence upper bound.",
			}},
			{Name: "time_from", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 event-time lower bound.",
			}},
			{Name: "time_to", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Inclusive timezone-qualified ISO 8601 event-time upper bound.",
			}},
			{Name: "event_types", Schema: stringArray("Event types to include.")},
			{Name: "surfaces", Schema: enumArray("Event surfaces to include.", surfaceNames...)},
		},
		Required: []string{"query"},
	}
}

// definitions 造这五件工具。
//
// 源: packages/session-query/tool-session-query/src/index.ts:66-122
func (c *Controller) definitions() []*tools.Definition {
	return []*tools.Definition{
		{
			Name:        SearchToolName,
			Description: "Search prior sessions in the caller workspace and return the strongest matching event from each session.",
			Parameters:  sessionSearchParameters(),
			Output:      textOutput,
			// 截止时间交给工具运行时执行，见 [Config.SearchTimeout] 的注释。
			Timeout:     c.searchTimeout,
			Execute:     execute(c.executeSessionSearch),
			PresentCall: presentSessionSearchCall,
		},
		{
			Name:        EventSearchToolName,
			Description: "Search prior events in one authorized session; the current session excludes the step performing this call.",
			Parameters:  eventSearchParameters(),
			Output:      textOutput,
			Timeout:     c.searchTimeout,
			Execute:     execute(c.executeEventSearch),
			PresentCall: presentEventSearchCall,
		},
		{
			Name:        TraceToolName,
			Description: "Read the authorized session lineage around one session, including complete visible ancestor and descendant relationships.",
			Parameters: tools.Node{
				Type:       tools.TypeObject,
				Properties: []tools.Property{sessionIDParameter()},
			},
			Output:            textOutput,
			IsConcurrencySafe: concurrencySafe,
			Execute:           execute(c.executeSessionTrace),
			PresentCall:       presentSessionTraceCall,
		},
		{
			Name:        EventTraceToolName,
			Description: "Read every direct replacement and relationship to a cited source event for one event in an authorized session.",
			Parameters: tools.Node{
				Type:       tools.TypeObject,
				Properties: []tools.Property{sessionIDParameter(), seqParameter()},
				Required:   []string{"seq"},
			},
			Output:            textOutput,
			IsConcurrencySafe: concurrencySafe,
			Execute:           execute(c.executeEventTrace),
			PresentCall: func(args json.RawMessage) tools.CallView {
				return presentEventTargetCall("Trace event", args)
			},
		},
		{
			Name:        EventReadToolName,
			Description: "Read one full unabridged event and optional neighboring raw-event summaries from an authorized session.",
			Parameters: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					sessionIDParameter(),
					seqParameter(),
					{Name: "before", Schema: tools.Node{
						Type:        tools.TypeInteger,
						Description: "Number of preceding raw events to summarize. Omit for none.",
					}},
					{Name: "after", Schema: tools.Node{
						Type:        tools.TypeInteger,
						Description: "Number of following raw events to summarize. Omit for none.",
					}},
				},
				Required: []string{"seq"},
			},
			Output:            textOutput,
			IsConcurrencySafe: concurrencySafe,
			Execute:           execute(c.executeEventRead),
			PresentCall: func(args json.RawMessage) tools.CallView {
				return presentEventTargetCall("Read event", args)
			},
		},
	}
}

// execute 把一个「收结构化参数、交一段文本」的操作包成工具运行时要的那种执行体。
//
// 新增: DSH 那边 schemastery 已经把 args 解成了类型化的对象，execute 直接收。
// Go 侧 [tools.Definition.Execute] 收的是 json.RawMessage，所以解码这一步要自己
// 做；五件工具的解码和编码完全一样，抽成一个泛型包装比抄五遍好。
func execute[A any](
	operation func(ctx context.Context, args A, exec *tools.RunContext) (string, error),
) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		var args A
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		text, err := operation(ctx, args, exec)
		if err != nil {
			return nil, err
		}
		return json.Marshal(text)
	}
}

// Deps 是装这五件工具要用到的协作者。
//
// 新增: DSH 那边它们从 cordis 容器里按 inject 取。Go 里没有那个容器，
// 所以摊成一个结构体，做法和 [github.com/snight1983/ds-harness-go/plan/planmode.Deps] 相同。
type Deps struct {
	// Tools 是工具注册表，必填。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，必填。
	Prompts *systemprompt.Registry
}

// Install 把五件工具和那段指引一次装齐，返回把它们一起摘下来的函数。
//
// 源: packages/session-query/tool-session-query/src/index.ts:56-122（apply）
//
// 中途装不上就把已经装上的按反序摘干净再报错：半装上去意味着模型手上有一件
// 检索工具、却没有那段告诉它「找到之后该跟进」的指引，它会拿摘录当全部事实。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("querytool: 需要一个工具注册表")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("querytool: 需要一个系统提示词注册表")
	}

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		var failures []error
		for index := len(installed) - 1; index >= 0; index-- {
			if err := installed[index](undoCtx); err != nil {
				failures = append(failures, err)
			}
		}
		installed = nil
		return errors.Join(failures...)
	}
	// 指引先装：它装不上的话一件工具都还没露出去，模型不会看见一组没有说明的工具。
	remove, err := deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText(promptText),
	})
	if err != nil {
		return nil, fmt.Errorf("querytool: 装会话检索指引失败：%w", err)
	}
	installed = append(installed, remove)

	for _, definition := range c.definitions() {
		dispose, err := deps.Tools.Register(ctx, owner, definition)
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("querytool: 装 %s 失败：%w", definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
