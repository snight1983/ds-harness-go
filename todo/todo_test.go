// 本文件的作用：把这个包三条胳膊的全部可观察行为钉住——一次写进去的是什么、
// 哪些写会被拒、折叠怎么走、以及那条持久不变量各拦什么。
//
// 逐条对着 DSH 的 tests/tool-todo.spec.ts、tests/projection.spec.ts、
// tests/invariant.spec.ts 走。
//
// # 这些测试防的是什么错
//
//   - **一次被拒的写留下了痕迹**。校验必须全部发生在追加**之前**：一次被拒的调用
//     往日志里写了半份清单，模型看到的和日志里的就分叉了，而整表替换的全部价值
//     正在于这两者永远一致。
//   - **策略漏进了持久规则**。[todo.ValidateEvent] 如果开始查「有几条在做」，
//     一份在允许并行时写下的日志会在部署收紧之后突然装不进来。
//   - **折叠把一条坏事件放大成一次清空**。那看起来和一次合法的清空一模一样，
//     界面上就是「计划凭空没了」。
//   - **一个没有归属会话的调用把清单存到了某个地方**。这份清单是逐 agent 的状态，
//     存了也没人认领。
package todo_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
	"github.com/snight1983/ds-harness-go/todo"
)

// appended 是一次落进日志的写。
type appended struct {
	agent *scope.Key
	todos []session.TodoItem
}

// harness 是一次工具测试要的全套家当。
type harness struct {
	runtime *tools.Runtime
	agent   *scope.Key
	writes  []appended
	// failWith 非 nil 时，每一次追加都以它失败。
	failWith error
}

// newHarness 造一个装好 todo_write 的运行时。
func newHarness(t *testing.T, allowParallel bool) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	h := &harness{runtime: runtime, agent: scope.NewKey("agent")}
	tool, err := todo.New(todo.Config{
		AllowParallelInProgress: allowParallel,
		Append: func(agent *scope.Key, todos []session.TodoItem) error {
			if h.failWith != nil {
				return h.failWith
			}
			h.writes = append(h.writes, appended{agent: agent, todos: todos})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	if _, err := tool.Install(context.Background(), runtime, scope.NewRoot()); err != nil {
		t.Fatalf("装这件工具失败：%v", err)
	}
	return h
}

// call 跑一次由 agent 发起的 todo_write。
func (h *harness) call(args string) tools.Result {
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      todo.ToolName,
		Arguments: json.RawMessage(args),
		Agent:     h.agent,
	})
}

// definition 取出装进去的那份定义，用来直接调那几条走不到运行时的分支。
func (h *harness) definition(t *testing.T) *tools.Definition {
	t.Helper()
	found, ok := h.runtime.Get(todo.ToolName, h.agent)
	if !ok {
		t.Fatalf("装上之后该查得到 %q", todo.ToolName)
	}
	return found
}

// textOf 把一份内容拼成可断言的纯文本。
func textOf(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "")
}

// failureOf 取出一次失败的那句话。
func failureOf(t *testing.T, result tools.Result) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("这次调用该失败：%s", textOf(result.Content))
	}
	if result.Error == nil {
		t.Fatal("失败的结果必须带上细节")
	}
	return result.Error.Message
}

func TestWritesTheWholeListAndReportsTheCounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	result := h.call(`{"todos":[
		{"content":"读代码","status":"completed"},
		{"content":"写代码","status":"in_progress"},
		{"content":"跑测试","status":"pending"}
	]}`)
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}

	if len(h.writes) != 1 {
		t.Fatalf("该恰好落一次日志，拿到 %d 次", len(h.writes))
	}
	write := h.writes[0]
	if write.agent != h.agent {
		t.Fatal("落日志时该带上发起这次调用的那个 agent")
	}
	want := []session.TodoItem{
		{Content: "读代码", Status: session.TodoCompleted},
		{Content: "写代码", Status: session.TodoInProgress},
		{Content: "跑测试", Status: session.TodoPending},
	}
	if len(write.todos) != len(want) {
		t.Fatalf("落进日志的必须是**整份**清单，拿到 %d 条", len(write.todos))
	}
	for index, item := range want {
		if write.todos[index] != item {
			t.Fatalf("第 %d 条对不上：拿到 %+v，要 %+v", index, write.todos[index], item)
		}
	}

	// 那份权威的值要能原样读回来：它是下游（呈现、外置）唯一认的东西。
	var value struct {
		Todos  []session.TodoItem `json:"todos"`
		Counts struct {
			Pending    int `json:"pending"`
			InProgress int `json:"inProgress"`
			Completed  int `json:"completed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("那份值该按输出 schema 读得回来：%v", err)
	}
	if value.Counts.Pending != 1 || value.Counts.InProgress != 1 || value.Counts.Completed != 1 {
		t.Fatalf("计数不对：%+v", value.Counts)
	}
	if got := textOf(result.Content); got != "Updated todo list: 1 pending, 1 in progress, 1 completed." {
		t.Fatalf("给模型看的那句话不对：%q", got)
	}
}

func TestTrimsContentBeforeWriting(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	if result := h.call(`{"todos":[{"content":"  写代码  ","status":"pending"}]}`); result.IsError {
		t.Fatalf("首尾空白该被抹掉而不是被拒：%+v", result.Error)
	}
	if h.writes[0].todos[0].Content != "写代码" {
		t.Fatalf("落进日志的该是规范化之后的内容：%q", h.writes[0].todos[0].Content)
	}
}

func TestTheSecondWriteReplacesTheFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	h.call(`{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"pending"}]}`)
	h.call(`{"todos":[{"content":"c","status":"completed"}]}`)

	if len(h.writes) != 2 {
		t.Fatalf("该落两次日志，拿到 %d 次", len(h.writes))
	}
	// 没有局部更新这回事：第二次写带的就是当下的整份清单。
	if len(h.writes[1].todos) != 1 || h.writes[1].todos[0].Content != "c" {
		t.Fatalf("第二次写该是一份完整的新清单：%+v", h.writes[1].todos)
	}
}

func TestAcceptsAnEmptyList(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	result := h.call(`{"todos":[]}`)
	if result.IsError {
		t.Fatalf("清空是一次合法的写：%+v", result.Error)
	}
	if len(h.writes) != 1 || len(h.writes[0].todos) != 0 {
		t.Fatalf("该落下一份空清单：%+v", h.writes)
	}
	if got := textOf(result.Content); got != "Updated todo list: 0 pending, 0 in progress, 0 completed." {
		t.Fatalf("空清单那句话不对：%q", got)
	}
}

func TestRejectsBlankContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	message := failureOf(t, h.call(`{"todos":[{"content":"   ","status":"pending"}]}`))
	if !strings.Contains(message, "`content` must be a non-empty string") {
		t.Fatalf("该报内容为空：%q", message)
	}
	if len(h.writes) != 0 {
		t.Fatal("被拒的写不许留下任何痕迹")
	}
}

func TestRejectsDuplicateContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	// 去空白之后才比：两条只差首尾空白的待办在界面上分不开。
	message := failureOf(t, h.call(`{"todos":[{"content":"a","status":"pending"},{"content":" a ","status":"pending"}]}`))
	if !strings.Contains(message, `duplicate content "a"`) {
		t.Fatalf("该报重复并把那条内容原样引出来：%q", message)
	}
	if len(h.writes) != 0 {
		t.Fatal("被拒的写不许留下任何痕迹")
	}
}

func TestRejectsSeveralInProgressUnderSingleActiveDiscipline(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	message := failureOf(t, h.call(`{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`))
	if !strings.Contains(message, "at most one task may be in_progress (got 2)") {
		t.Fatalf("该报在做的条数：%q", message)
	}
	if len(h.writes) != 0 {
		t.Fatal("被拒的写不许留下任何痕迹")
	}
}

func TestAcceptsSeveralInProgressWhenParallelIsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, true)

	if result := h.call(`{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`); result.IsError {
		t.Fatalf("允许并行时这次该通过：%+v", result.Error)
	}
	if len(h.writes) != 1 || len(h.writes[0].todos) != 2 {
		t.Fatalf("两条在做的都该落进日志：%+v", h.writes)
	}
}

func TestTheDescriptionFollowsTheDeployedDiscipline(t *testing.T) {
	t.Parallel()
	single := newHarness(t, false).definition(t).Description
	parallel := newHarness(t, true).definition(t).Description

	if !strings.Contains(single, "Keep AT MOST ONE todo `in_progress`") {
		t.Fatalf("单活那份说明该要求至多一条：%q", single)
	}
	if !strings.Contains(parallel, "several at once when work genuinely runs in parallel") {
		t.Fatalf("并行那份说明该允许好几条：%q", parallel)
	}
	// 只有中间那段随策略变——这条策略改变的只是这一条指令，别的都不该动。
	const head = "Send the ENTIRE list every call"
	const tail = "Skip the list for trivial single-step tasks."
	for _, description := range []string{single, parallel} {
		if !strings.Contains(description, head) || !strings.Contains(description, tail) {
			t.Fatalf("两份说明的首尾该一样：%q", description)
		}
	}
	if strings.Contains(single, "several at once") || strings.Contains(parallel, "AT MOST ONE") {
		t.Fatal("两份说明的中段该互斥")
	}
}

func TestTheSchemaPinsTheShapeOfATodo(t *testing.T) {
	t.Parallel()
	definition := newHarness(t, false).definition(t)

	parameters := definition.Parameters
	if len(parameters.Properties) != 1 || parameters.Properties[0].Name != "todos" {
		t.Fatalf("参数该只有 todos 一项：%+v", parameters.Properties)
	}
	list := parameters.Properties[0].Schema
	if list.Type != tools.TypeArray || list.Items == nil {
		t.Fatalf("todos 该是一个数组：%+v", list)
	}
	item := *list.Items
	if item.AdditionalProperties == nil || *item.AdditionalProperties {
		// 落进日志的那份快照必须和模型以为自己写下的那份一模一样。
		t.Fatal("条目该显式禁掉多余的键")
	}
	var names []string
	for _, property := range item.Properties {
		names = append(names, property.Name)
	}
	if strings.Join(names, ",") != "content,status" {
		t.Fatalf("条目的字段顺序就是语义（它逐字进提示词缓存的键）：%v", names)
	}
	var enum []string
	for _, raw := range item.Properties[1].Schema.Enum {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("枚举项该是字符串：%v", err)
		}
		enum = append(enum, value)
	}
	if strings.Join(enum, ",") != "pending,in_progress,completed" {
		t.Fatalf("状态枚举的取值和顺序都该钉住：%v", enum)
	}
	if item.Properties[0].Schema.Description == "" || item.Properties[1].Schema.Description == "" {
		t.Fatal("参数里的字段该带上给模型看的说明")
	}

	// 输出复用同一份条目形状，但不带那两句只对模型输入有意义的说明。
	output := definition.Output.Schema
	if len(output.Properties) != 2 || output.Properties[0].Name != "todos" || output.Properties[1].Name != "counts" {
		t.Fatalf("输出该是 todos + counts：%+v", output.Properties)
	}
	outputItem := *output.Properties[0].Schema.Items
	if outputItem.Properties[0].Schema.Description != "" {
		t.Fatal("输出里的字段不该带参数那两句说明")
	}
	if strings.Join(output.Required, ",") != "todos,counts" {
		t.Fatalf("输出的必填项该钉住：%v", output.Required)
	}
}

func TestTheRegistryRejectsWhatTheSchemaCanExpress(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	cases := map[string]struct{ args, want string }{
		"状态不在枚举里":    {`{"todos":[{"content":"a","status":"done"}]}`, `"todos[0].status" must be one of`},
		"todos 不是数组": {`{"todos":"a"}`, "todos"},
		"条目里多了一个键":   {`{"todos":[{"content":"a","status":"pending","id":1}]}`, "is not a declared property"},
		"缺 status":   {`{"todos":[{"content":"a"}]}`, "status"},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			message := failureOf(t, h.call(testCase.args))
			if !strings.Contains(message, testCase.want) {
				t.Fatalf("该在 schema 那道门上响并提到 %q：%q", testCase.want, message)
			}
		})
	}
	if len(h.writes) != 0 {
		t.Fatal("一条都不该落进日志")
	}
}

func TestRejectsACallWithoutAnOwningAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)

	result := h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      todo.ToolName,
		Arguments: json.RawMessage(`{"todos":[{"content":"a","status":"pending"}]}`),
	})
	if message := failureOf(t, result); !strings.Contains(message, "requires an owning agent session") {
		t.Fatalf("该报没有归属会话：%q", message)
	}
	if len(h.writes) != 0 {
		t.Fatal("一份没人认领的清单不该被存下来")
	}
}

func TestAFailedAppendFailsTheCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, false)
	h.failWith = errors.New("盘满了")

	// 一次没能落进日志的写不许被报成成功，否则模型会以为自己的清单已经在那儿了。
	if message := failureOf(t, h.call(`{"todos":[{"content":"a","status":"pending"}]}`)); !strings.Contains(message, "盘满了") {
		t.Fatalf("该把落盘的错误交出去：%q", message)
	}
}

func TestPresentsTheCallWithTheRawList(t *testing.T) {
	t.Parallel()
	definition := newHarness(t, false).definition(t)

	view, ok := definition.PresentCall(json.RawMessage(`{"todos":[{"content":"a","status":"pending"}]}`)).(tools.GenericCallView)
	if !ok {
		t.Fatal("该是一张通用卡片")
	}
	if view.Title != "Update todo list" || view.Kind != tools.CallOther {
		t.Fatalf("卡片的标题或类别不对：%+v", view)
	}
	if !strings.Contains(string(view.RawInput), `"content":"a"`) {
		t.Fatalf("卡片该带上那份原始清单：%s", view.RawInput)
	}
}

func TestPresentsATitleOnlyCardWhenTheArgumentsAreUnreadable(t *testing.T) {
	t.Parallel()
	definition := newHarness(t, false).definition(t)

	// 它是纯函数、且实时流式时参数可能还没拼完，所以拿不出清单只能少画一点，
	// 绝不能去碰别的地方。
	view, ok := definition.PresentCall(json.RawMessage(`{"todos":`)).(tools.GenericCallView)
	if !ok {
		t.Fatal("该是一张通用卡片")
	}
	if view.Title != "Update todo list" || view.RawInput != nil {
		t.Fatalf("该只剩标题：%+v", view)
	}
}

func TestRenderRefusesAValueItCannotRead(t *testing.T) {
	t.Parallel()
	definition := newHarness(t, false).definition(t)

	if _, err := definition.Output.Render(nil, json.RawMessage(`{`)); err == nil {
		t.Fatal("读不回来的值该报错，而不是渲染出一句假的计数")
	}
}

func TestExecuteRefusesArgumentsItCannotRead(t *testing.T) {
	t.Parallel()
	definition := newHarness(t, false).definition(t)

	// 走运行时的话 schema 那道门会先拦下来，所以这条分支只能直接调。
	run := &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: scope.NewKey("agent")},
	}}
	if _, err := definition.Execute(context.Background(), json.RawMessage(`{`), run); err == nil {
		t.Fatal("读不回来的参数该报错")
	}
}

func TestUndoRemovesTheTool(t *testing.T) {
	t.Parallel()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	tool, err := todo.New(todo.Config{Append: func(*scope.Key, []session.TodoItem) error { return nil }})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	undo, err := tool.Install(context.Background(), runtime, scope.NewRoot())
	if err != nil {
		t.Fatalf("装这件工具失败：%v", err)
	}
	if _, ok := runtime.Get(todo.ToolName, nil); !ok {
		t.Fatal("装上之后该查得到")
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("注销失败：%v", err)
	}
	if _, ok := runtime.Get(todo.ToolName, nil); ok {
		t.Fatal("注销之后就不该再查得到了")
	}
}

func TestNewRejectsAConfigWithoutAnAppendPath(t *testing.T) {
	t.Parallel()
	// 没有这条路，一次成功的调用就只会写进空气里。
	if _, err := todo.New(todo.Config{}); !errors.Is(err, todo.ErrInvalidConfig) {
		t.Fatalf("该被拒绝并认得出哨兵：%v", err)
	}
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	tool, err := todo.New(todo.Config{Append: func(*scope.Key, []session.TodoItem) error { return nil }})
	if err != nil {
		t.Fatalf("造这件工具失败：%v", err)
	}
	if _, err := tool.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有注册表就装不上，该报错")
	}
}

// fakeSession 是一个只把日志摆在那儿的会话。
type fakeSession struct {
	id     session.SessionID
	events []session.Event
}

func (s *fakeSession) ID() session.SessionID { return s.id }

func (s *fakeSession) Events() []session.Event { return s.events }

func (s *fakeSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}

// todoEvent 造一条 todo/write 事件。
func todoEvent(seq int, payload string) session.Event {
	return session.Event{Type: session.EventTodoWrite, Seq: seq, Data: json.RawMessage(payload)}
}

// turnEvent 造一条回合边界事件。
func turnEvent(seq int, kind session.EventType) session.Event {
	return session.Event{Type: kind, Seq: seq, Data: json.RawMessage(`{}`)}
}

// projectionOf 把这些事件折一遍，交出 todos 这个键的值。
func projectionOf(t *testing.T, events ...session.Event) (any, bool) {
	t.Helper()
	registry := projection.NewRegistry()
	dispose, err := todo.RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)
	return registry.StateOf(&fakeSession{id: "s1", events: events}, todo.ProjectionKey)
}

// itemsOf 把一份投影值断言成待办清单。
func itemsOf(t *testing.T, value any) []session.TodoItem {
	t.Helper()
	items, ok := value.([]session.TodoItem)
	if !ok {
		t.Fatalf("这个键该读出一份待办清单，拿到 %T", value)
	}
	return items
}

func TestTheProjectionIsNullBeforeTheFirstWrite(t *testing.T) {
	t.Parallel()
	value, ok := projectionOf(t)
	if !ok {
		t.Fatal("登记之后这个键就该在")
	}
	if items := itemsOf(t, value); items != nil {
		t.Fatalf("第一次写之前该是空的（排出去就是 JSON null），拿到：%+v", items)
	}
}

func TestTheProjectionKeepsTheLastWrite(t *testing.T) {
	t.Parallel()
	value, _ := projectionOf(t,
		todoEvent(0, `{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"pending"}]}`),
		todoEvent(1, `{"todos":[{"content":"c","status":"completed"}]}`),
	)
	items := itemsOf(t, value)
	if len(items) != 1 || items[0].Content != "c" || items[0].Status != session.TodoCompleted {
		t.Fatalf("每一条事件都带着整表快照，最后写的那份生效：%+v", items)
	}
}

func TestANewTurnClearsThePlanButTheEndOfATurnDoesNot(t *testing.T) {
	t.Parallel()

	// 上一个回合的计划不该挂在这一个回合头上。
	cleared, _ := projectionOf(t,
		todoEvent(0, `{"todos":[{"content":"a","status":"completed"}]}`),
		turnEvent(1, session.EventTurnStart),
	)
	if items := itemsOf(t, cleared); items != nil {
		t.Fatalf("新回合开起来该清空，拿到：%+v", items)
	}

	// 一个回合刚结束时，那份做完的清单正是最该被看见的东西。
	kept, _ := projectionOf(t,
		todoEvent(0, `{"todos":[{"content":"a","status":"completed"}]}`),
		turnEvent(1, session.EventTurnEnd),
	)
	if items := itemsOf(t, kept); len(items) != 1 {
		t.Fatalf("回合结束不该清空，拿到：%+v", items)
	}
}

func TestTheProjectionIgnoresUnrelatedEvents(t *testing.T) {
	t.Parallel()
	value, _ := projectionOf(t,
		todoEvent(0, `{"todos":[{"content":"a","status":"pending"}]}`),
		session.Event{Type: session.EventUserMessage, Seq: 1, Data: json.RawMessage(`{}`)},
	)
	if items := itemsOf(t, value); len(items) != 1 {
		t.Fatalf("别的事件不该动这份清单：%+v", items)
	}
}

func TestTheProjectionReportsNoChangeForARedundantClear(t *testing.T) {
	t.Parallel()
	registry := projection.NewRegistry()
	dispose, err := todo.RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	defer dispose()

	changes := 0
	defer registry.OnChanged(func(projection.SessionView, string, any, int) { changes++ })()

	view := &fakeSession{id: "s1"}
	// 已经是空的时候再开一个回合：报一次假的变化会让每一个新回合都往变更流上推一条 null。
	registry.Drive(view, turnEvent(0, session.EventTurnStart))
	if changes != 0 {
		t.Fatalf("空清单上的清空不算变化，拿到 %d 次通知", changes)
	}
	view.events = append(view.events, turnEvent(0, session.EventTurnStart))
	registry.Drive(view, todoEvent(1, `{"todos":[{"content":"a","status":"pending"}]}`))
	if changes != 1 {
		t.Fatalf("一次真的写该推一条，拿到 %d 次", changes)
	}
}

func TestTheProjectionKeepsThePlanWhenAWriteCannotBeRead(t *testing.T) {
	t.Parallel()
	value, _ := projectionOf(t,
		todoEvent(0, `{"todos":[{"content":"a","status":"pending"}]}`),
		todoEvent(1, `"不是一个对象"`),
	)
	// 换成空的会把「坏了一条」放大成「整份计划没了」，而那看起来和一次合法的清空
	// 一模一样。真正拦这种东西的是 ValidateEvent。
	if items := itemsOf(t, value); len(items) != 1 || items[0].Content != "a" {
		t.Fatalf("读不回来的负载该保持原状：%+v", items)
	}
}

func TestARestoredStateThatDoesNotFitIsRejected(t *testing.T) {
	t.Parallel()
	registry := projection.NewRegistry()
	dispose, err := todo.RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	defer dispose()

	events := []session.Event{todoEvent(0, `{"todos":[{"content":"a","status":"pending"}]}`)}

	// 版本对得上、形状也对得上：这一行能直接用，不必重折。
	good, err := registry.Restore(projection.Checkpoint{
		todo.ProjectionKey: {Ver: 2, Seq: 0, Val: json.RawMessage(`[{"content":"存过的","status":"completed"}]`)},
	}, events, 1)
	if err != nil {
		t.Fatalf("一行能用的检查点不该报错：%v", err)
	}
	if items := itemsOf(t, good.Snapshot.Values[todo.ProjectionKey]); len(items) != 1 || items[0].Content != "存过的" {
		t.Fatalf("该直接用那一行：%+v", items)
	}

	// 版本对得上却解不开：一份形状对不上的旧状态如果被宽容地读成「字段都在、
	// 值全是零」，它会被继续往前折成垃圾，而且一路上不报任何错。
	if _, err := registry.Restore(projection.Checkpoint{
		todo.ProjectionKey: {Ver: 2, Seq: 0, Val: json.RawMessage(`[{"content":"a","status":"pending","extra":1}]`)},
	}, events, 1); err == nil {
		t.Fatal("多一个字段就该报错，而不是默默地读成零值")
	}
}

func TestRegisterProjectionNeedsARegistry(t *testing.T) {
	t.Parallel()
	if _, err := todo.RegisterProjection(nil); err == nil {
		t.Fatal("没有注册表就登记不了，该报错")
	}
}

func TestValidateEventIgnoresEverythingElse(t *testing.T) {
	t.Parallel()
	// 本包只拥有 todo/write 里的那几个字段，别的事件一个字都不许说。
	if err := todo.ValidateEvent(session.Event{Type: session.EventUserMessage, Data: json.RawMessage(`{"todos":42}`)}); err != nil {
		t.Fatalf("不是 todo/write 就该什么都不做：%v", err)
	}
}

func TestValidateEventAcceptsAWellFormedSnapshot(t *testing.T) {
	t.Parallel()
	payload := `{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"in_progress"},{"content":"c","status":"completed"}]}`
	if err := todo.ValidateEvent(todoEvent(0, payload)); err != nil {
		t.Fatalf("这份快照该通过：%v", err)
	}
	if err := todo.ValidateEvent(todoEvent(0, `{"todos":[]}`)); err != nil {
		t.Fatalf("空清单该通过：%v", err)
	}
}

func TestValidateEventStaysSilentAboutHowManyAreInProgress(t *testing.T) {
	t.Parallel()
	// 有几条在做是这件工具**当下**的部署策略，不是一条持久的形状规则：一份在允许
	// 并行的时候写下的日志，在部署收紧策略之后必须仍然能回放。
	payload := `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`
	if err := todo.ValidateEvent(todoEvent(0, payload)); err != nil {
		t.Fatalf("持久规则不许绑在当下的配置上：%v", err)
	}
}

func TestValidateEventRejectsABrokenSnapshot(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ payload, want string }{
		"负载整个读不回来":   {`不是 JSON`, "todos must be an array"},
		"todos 不是数组": {`{"todos":42}`, "todos must be an array"},
		"缺 todos":    {`{}`, "todos must be an array"},
		"条目是数字":      {`{"todos":[42]}`, "entries must be objects"},
		"条目是 null":   {`{"todos":[null]}`, "entries must be objects"},
		"条目是数组":      {`{"todos":[[]]}`, "entries must be objects"},
		"content 不是字符串": {
			`{"todos":[{"content":42,"status":"pending"}]}`,
			"content must be non-empty and already trimmed",
		},
		"content 是空串": {
			`{"todos":[{"content":"","status":"pending"}]}`,
			"content must be non-empty and already trimmed",
		},
		"content 带首尾空白": {
			`{"todos":[{"content":" a ","status":"pending"}]}`,
			"content must be non-empty and already trimmed",
		},
		"缺 content": {
			`{"todos":[{"status":"pending"}]}`,
			"content must be non-empty and already trimmed",
		},
		"内容重复": {
			`{"todos":[{"content":"a","status":"pending"},{"content":"a","status":"pending"}]}`,
			`repeats content "a"`,
		},
		"状态不认识": {
			`{"todos":[{"content":"a","status":"done"}]}`,
			`unknown status "done"`,
		},
		"状态不是字符串": {
			`{"todos":[{"content":"a","status":42}]}`,
			"unknown status 42",
		},
		"缺 status": {
			`{"todos":[{"content":"a"}]}`,
			"unknown status null",
		},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			err := todo.ValidateEvent(todoEvent(0, testCase.payload))
			if err == nil {
				t.Fatal("这份快照该被拒")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("该报 %q，拿到：%v", testCase.want, err)
			}
		})
	}
}

// invariantHarness 是一次不变量测试要的家当。
type invariantHarness struct {
	registry  *invariants.Registry
	loaded    []session.Event
	observers []func(session.Event)
	// unsubscribed 记下退订被调了几次。
	unsubscribed int
}

// register 把本包的检查装进去。
func (h *invariantHarness) register(t *testing.T) func() {
	t.Helper()
	undo, err := todo.RegisterInvariants(
		context.Background(),
		h.registry,
		func() []session.Event { return h.loaded },
		func(observer func(session.Event)) func() {
			h.observers = append(h.observers, observer)
			return func() { h.unsubscribed++ }
		},
	)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	return undo
}

// emit 把一条事件推给所有还在的观察者。
func (h *invariantHarness) emit(event session.Event) {
	for _, observer := range h.observers {
		observer(event)
	}
}

// newInvariantHarness 造一个开着的注册表。
func newInvariantHarness(t *testing.T, loaded ...session.Event) *invariantHarness {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	return &invariantHarness{registry: registry, loaded: loaded}
}

// violation 跑一段会违例的代码，交出那条违例。
func violation(t *testing.T, run func()) *invariants.Error {
	t.Helper()
	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		run()
	}()
	if caught == nil {
		t.Fatal("该抛出一条违例")
	}
	return caught
}

func TestInvariantsCatchABrokenSnapshotAlreadyInTheLog(t *testing.T) {
	t.Parallel()
	// 一份历史里就带着坏快照的会话，必须在装载这一刻就响，而不是等下一次追加。
	h := newInvariantHarness(t, todoEvent(0, `{"todos":[{"content":"a","status":"done"}]}`))

	failure := violation(t, func() { h.register(t) })
	if failure.PackageName != todo.PackageName {
		t.Fatalf("该报在本包名下：%q", failure.PackageName)
	}
	if !strings.Contains(failure.Message, `unknown status "done"`) {
		t.Fatalf("该带上那条违例本身：%q", failure.Message)
	}
}

func TestInvariantsCatchABrokenSnapshotAppendedLater(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	h.register(t)

	// 一条合法的追加不该有任何动静。
	h.emit(todoEvent(0, `{"todos":[{"content":"a","status":"pending"}]}`))

	failure := violation(t, func() { h.emit(todoEvent(1, `{"todos":[42]}`)) })
	if !strings.Contains(failure.Message, "entries must be objects") {
		t.Fatalf("该带上那条违例本身：%q", failure.Message)
	}
}

func TestUnregisteringInvariantsStopsTheCheck(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	undo()

	// 一条不该再查的检查绝不许继续在别人的写路径上抛。
	if h.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", h.unsubscribed)
	}
}

func TestRegisterInvariantsNeedsAllThreeSeams(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	loaded := func() []session.Event { return nil }
	subscribe := func(func(session.Event)) func() { return func() {} }

	cases := map[string]func() error{
		"没给注册表": func() error {
			_, err := todo.RegisterInvariants(context.Background(), nil, loaded, subscribe)
			return err
		},
		"没给已装载日志": func() error {
			_, err := todo.RegisterInvariants(context.Background(), registry, nil, subscribe)
			return err
		},
		"没给订阅": func() error {
			_, err := todo.RegisterInvariants(context.Background(), registry, loaded, nil)
			return err
		},
	}
	for label, run := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if err := run(); !errors.Is(err, todo.ErrInvalidConfig) {
				t.Fatalf("该被拒绝并认得出哨兵：%v", err)
			}
		})
	}
}
