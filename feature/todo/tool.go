// 本文件的作用：todo_write 这件工具本身——配置怎么验、给模型看的那段说明怎么随
// 策略变、schema 表达不了的那几条约束在哪查、以及一次成功的调用往日志里写什么。
//
// 源: packages/todo/tool-todo/src/index.ts:22-223

package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// ToolName 是这件工具在注册表里的名字。
//
// 源: packages/todo/tool-todo/src/index.ts:150
const ToolName = "todo_write"

// statuses 是 [sessionlog.TodoStatus] 的三个合法取值，按给模型看的顺序排。
//
// 源: packages/todo/tool-todo/src/index.ts:26
//
// 顺序就是语义：它会原样进 schema 的 enum，而那份 schema 逐字进提示词缓存的键。
var statuses = []sessionlog.TodoStatus{sessionlog.TodoPending, sessionlog.TodoInProgress, sessionlog.TodoCompleted}

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("todo: 配置不成立")

// 说明分四段拼，只有中间那段随策略变——因为那是这条策略唯一改变的指令。
//
// 源: packages/todo/tool-todo/src/index.ts:45-78
//
// 这几段是给模型看的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线。
const (
	descriptionHead = "Record and update a structured task list for the current work. Send the ENTIRE " +
		"list every call — it REPLACES the previous list (there are no partial updates, " +
		"no per-item edits). Use it to plan multi-step work and show progress: add one " +
		"todo per concrete step before you start. "

	descriptionParallel = "Mark every todo being actively worked " +
		"on `in_progress` — several at once when work genuinely runs in parallel (e.g. " +
		"concurrent subagents or background commands), one for sequential work; while " +
		"work remains, at least one task should be `in_progress`. "

	descriptionSingle = "Keep AT MOST ONE todo `in_progress` at a " +
		"time; while work remains, exactly one active task should be `in_progress`. "

	descriptionTail = "Mark a todo " +
		"`completed` the moment it is done (do not batch completions), and allow no " +
		"`in_progress` item only once all work is complete. Skip the list for trivial " +
		"single-step tasks. Statuses: `pending` (not started), `in_progress` (being " +
		"worked on now), `completed` (finished)."
)

// Config 是这件工具的部署配置。
//
// 源: packages/todo/tool-todo/src/index.ts:29-43
type Config struct {
	// AllowParallelInProgress 决定能不能同时有好几条 in_progress。
	//
	// 真适合那些并行干活的 agent——子 agent、后台命令、工作流扇出——这时给模型的
	// 说明会要求它把每一件**正在做**的事都标上。假恢复单活纪律：说明要求恰好一条，
	// 而一次标了好几条的调用会被拒。
	//
	// 新增: DSH 那边这一项标了 required，好让部署必须明确选一个。Go 的 bool
	// 没有「没填」这个状态，零值 false 就是更紧的那条纪律——漏填只会更严。
	AllowParallelInProgress bool

	// Append 把一次整表快照写进这个 agent 所属会话的日志。
	//
	// 新增: DSH 的执行体直接写 exec.agent.session.append(...)，靠结构类型从那个
	// agent 对象上摸到一个活会话。Go 这边 [tools.Execution.Agent] 是一个不透明的
	// 作用域键，从它到「往哪个会话追加」的映射只有装配方知道。
	//
	// 它返回的错误会变成这次调用的失败结果：一次没能落进日志的写不许被报成成功，
	// 否则模型会以为自己的清单已经在那儿了。
	Append func(agent *scope.Key, todos []sessionlog.TodoItem) error
}

// Tool 是验好的配置。
type Tool struct {
	allowParallel bool
	append        func(*scope.Key, []sessionlog.TodoItem) error
}

// New 验一份配置，造出这件工具。
func New(config Config) (*Tool, error) {
	if config.Append == nil {
		return nil, fmt.Errorf("%w: 需要一条往会话日志追加待办快照的路", ErrInvalidConfig)
	}
	return &Tool{allowParallel: config.AllowParallelInProgress, append: config.Append}, nil
}

// Install 把 todo_write 注册进一个工具注册表，返回注销它的函数。
//
// 源: packages/todo/tool-todo/src/index.ts:146-222
func (t *Tool) Install(ctx context.Context, runtime *tools.Runtime, owner *scope.Scope) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("todo: 需要一个工具注册表")
	}
	return runtime.Register(ctx, owner, t.definition())
}

// describe 拼出这一次装载给模型看的说明。
//
// 源: packages/todo/tool-todo/src/index.ts:74-78
func describe(allowParallel bool) string {
	middle := descriptionSingle
	if allowParallel {
		middle = descriptionParallel
	}
	return descriptionHead + middle + descriptionTail
}

// writeArgs 是模型写出来的参数。
//
// 单独收一个 Status 字符串而不是直接收 [sessionlog.TodoStatus]，是因为这一层只做
// 「schema 表达不了的那几条」的校验；枚举本身已经在注册表那道门上验过了。
type writeArgs struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

// counts 是一次写入之后三种状态各有几条。
type counts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"inProgress"`
	Completed  int `json:"completed"`
}

// writeValue 是这件工具那份权威的返回值。
type writeValue struct {
	Todos  []sessionlog.TodoItem `json:"todos"`
	Counts counts                `json:"counts"`
}

// toTodoList 查 schema 表达不了的那几条值约束，并拼出规范化之后的清单：
// 去掉首尾空白之后非空、互不重复，以及——部署不允许并行时——至多一条在做。
//
// 源: packages/todo/tool-todo/src/index.ts:91-111
//
// 注册表已经把枚举验过、也已经拒掉了条目里认不出来的键
// （additionalProperties: false——落进日志的那份快照必须和模型以为自己写下的
// 那份一模一样，所以一个嵌套或者加料的条目形状要在 schema 那道门上响，
// 而不是被默默拍平）。
func toTodoList(raw writeArgs, allowParallel bool) ([]sessionlog.TodoItem, error) {
	todos := make([]sessionlog.TodoItem, 0, len(raw.Todos))
	seen := make(map[string]struct{}, len(raw.Todos))
	active := 0
	for _, item := range raw.Todos {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, errors.New("invalid todo: `content` must be a non-empty string")
		}
		if _, duplicated := seen[content]; duplicated {
			quoted, _ := json.Marshal(content)
			return nil, fmt.Errorf("invalid todos: duplicate content %s", quoted)
		}
		seen[content] = struct{}{}
		if item.Status == string(sessionlog.TodoInProgress) {
			active++
		}
		todos = append(todos, sessionlog.TodoItem{Content: content, Status: sessionlog.TodoStatus(item.Status)})
	}
	if !allowParallel && active > 1 {
		return nil, fmt.Errorf("invalid todos: at most one task may be in_progress (got %d)", active)
	}
	return todos, nil
}

// falseValue 是 [tools.Node.AdditionalProperties] 要的那个「显式的 false」。
var falseValue = false

// statusEnum 把三个合法状态排成 schema 的 enum。
func statusEnum() []json.RawMessage {
	enum := make([]json.RawMessage, 0, len(statuses))
	for _, status := range statuses {
		quoted, _ := json.Marshal(string(status))
		enum = append(enum, quoted)
	}
	return enum
}

// itemNode 是一条待办的 schema，参数和输出共用同一份。
//
// 源: packages/todo/tool-todo/src/index.ts:157-169, 180-187
func itemNode(describeFields bool) tools.Node {
	content := tools.Node{Type: tools.TypeString}
	status := tools.Node{Type: tools.TypeString, Enum: statusEnum()}
	if describeFields {
		content.Description = "What the task is — a short imperative line."
		status.Description = "pending (not started) | in_progress (now) | completed (done)."
	}
	return tools.Node{
		Type:                 tools.TypeObject,
		AdditionalProperties: &falseValue,
		Properties: []tools.Property{
			{Name: "content", Schema: content},
			{Name: "status", Schema: status},
		},
		Required: []string{"content", "status"},
	}
}

// definition 造这件工具的定义。
//
// 源: packages/todo/tool-todo/src/index.ts:147-221
func (t *Tool) definition() *tools.Definition {
	parameterItem := itemNode(true)
	outputItem := itemNode(false)
	return &tools.Definition{
		Name:        ToolName,
		Description: describe(t.allowParallel),
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name: "todos",
				Schema: tools.Node{
					Type:        tools.TypeArray,
					Description: "The COMPLETE task list, replacing any previous list.",
					Items:       &parameterItem,
				},
			}},
			Required: []string{"todos"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type:                 tools.TypeObject,
				AdditionalProperties: &falseValue,
				Properties: []tools.Property{
					{Name: "todos", Schema: tools.Node{Type: tools.TypeArray, Items: &outputItem}},
					{Name: "counts", Schema: tools.Node{
						Type:                 tools.TypeObject,
						AdditionalProperties: &falseValue,
						Properties: []tools.Property{
							{Name: "pending", Schema: tools.Node{Type: tools.TypeInteger}},
							{Name: "inProgress", Schema: tools.Node{Type: tools.TypeInteger}},
							{Name: "completed", Schema: tools.Node{Type: tools.TypeInteger}},
						},
						Required: []string{"pending", "inProgress", "completed"},
					}},
				},
				Required: []string{"todos", "counts"},
			},
			Render: render,
		},
		Execute:     t.execute,
		PresentCall: presentCall,
	}
}

// render 把那份值折成给模型看的一句话。
//
// 源: packages/todo/tool-todo/src/index.ts:201-204
func render(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
	var decoded writeValue
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Updated todo list: %d pending, %d in progress, %d completed.",
		decoded.Counts.Pending, decoded.Counts.InProgress, decoded.Counts.Completed)
	return llm.Content{llm.TextBlock{Text: text}}, nil
}

// presentCall 是这次调用进行中在界面上的样子。
//
// 源: packages/todo/tool-todo/src/index.ts:221
//
// 它必须是纯函数（实时流式和会话重放都会调它），所以只看 args：拿不出那份清单
// 就交出一张只有标题的卡片，绝不去碰别的地方。
func presentCall(args json.RawMessage) tools.CallView {
	view := tools.GenericCallView{Title: "Update todo list", Kind: tools.CallOther}
	var raw struct {
		Todos json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(args, &raw); err == nil {
		view.RawInput = raw.Todos
	}
	return view
}

// execute 跑一次已经放行的调用。
//
// 源: packages/todo/tool-todo/src/index.ts:206-223
func (t *Tool) execute(_ context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
	var decoded writeArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return nil, err
	}
	todos, err := toTodoList(decoded, t.allowParallel)
	if err != nil {
		return nil, err
	}
	if exec.Agent == nil {
		// 这份清单是逐 agent 会话的状态；一个没有归属会话的调用方无处安放它。
		// 拒掉，而不是默默地什么都不做。
		return nil, errors.New("todo_write requires an owning agent session")
	}
	if err := t.append(exec.Agent, todos); err != nil {
		return nil, err
	}
	return json.Marshal(writeValue{Todos: todos, Counts: countByStatus(todos)})
}

// countByStatus 数出三种状态各有几条。
//
// 源: packages/todo/tool-todo/src/index.ts:214-221
func countByStatus(todos []sessionlog.TodoItem) counts {
	var tally counts
	for _, todo := range todos {
		switch todo.Status {
		case sessionlog.TodoPending:
			tally.Pending++
		case sessionlog.TodoInProgress:
			tally.InProgress++
		case sessionlog.TodoCompleted:
			tally.Completed++
		}
	}
	return tally
}
