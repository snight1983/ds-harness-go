// 本文件的作用：把这张注册表的全部可观察行为钉住——那一行的解析边界、全局与作用域
// 两层的登记与遮蔽、一次执行的准入与结算、图片那一段的三种拒绝，以及那条持久不变量。
//
// 逐条对着 DSH 的 tests/commands.spec.ts 与 tests/invariant.spec.ts 走。那边靠 cordis
// 把整套东西装起来，这里换成一条假日志、一个假附件仓库和几个手搭的作用域。
//
// # 这些测试防的是什么错
//
//   - **生命周期那一对被拆了**。一次执行只落了 command/run 没落 command/done，
//     回放时那条命令永远停在「正在跑」，而这正是 [commands.Trace] 要拦的东西。
//   - **准入没过却写了日志**。语法不成立、名字不认得，两种都必须一个字节都不写——
//     否则一条打错的斜杠会在审计里留下一次从没发生过的执行。
//   - **图片绕过了准入**。把图发给一条没声明收图的命令、仓库没装、或者超了限额，
//     三种都必须在处理器跑起来**之前**结算掉；漏掉任何一种，一批没验过的字节
//     就进了第三方插件的手里。
//   - **取消之后处理器照样跑**。用户已经撤回的命令不许在几秒后动到状态，
//     因为重试的调用方接着又会动一遍。
package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/interaction/commands"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/session"
)

// errAppend 是那条假日志被要求失败时报的错。
var errAppend = errors.New("append failed before log growth")

// appended 是一次被记下来的追加。
type appended struct {
	kind session.EventType
	raw  json.RawMessage
}

// fakeLog 是一条把每次追加都记下来的假会话日志。
type fakeLog struct {
	mutex    sync.Mutex
	appended []appended
	// failOn 非空时，这种事件的追加失败。
	failOn session.EventType
}

// Append 记下这次追加。
func (l *fakeLog) Append(kind session.EventType, data any) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.failOn == kind {
		return errAppend
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	l.appended = append(l.appended, appended{kind: kind, raw: raw})
	return nil
}

// kinds 列出这条日志上被追加过的事件类型。
func (l *fakeLog) kinds() []string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	list := make([]string, 0, len(l.appended))
	for _, event := range l.appended {
		list = append(list, string(event.kind))
	}
	return list
}

// len 是这条日志上追加过几次。
func (l *fakeLog) len() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return len(l.appended)
}

// payload 把第 index 次追加的负载读成一张表。
func (l *fakeLog) payload(t *testing.T, index int) map[string]json.RawMessage {
	t.Helper()
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if index < 0 {
		index += len(l.appended)
	}
	if index < 0 || index >= len(l.appended) {
		t.Fatalf("第 %d 次追加不存在，一共 %d 次", index, len(l.appended))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(l.appended[index].raw, &fields); err != nil {
		t.Fatalf("第 %d 次追加的负载读不回来：%v", index, err)
	}
	return fields
}

// field 从第 index 次追加的负载里取一个字段的原始 JSON。
func (l *fakeLog) field(t *testing.T, index int, name string) string {
	t.Helper()
	return string(l.payload(t, index)[name])
}

// text 从第 index 次追加的负载里取一个字符串字段。
func (l *fakeLog) text(t *testing.T, index int, name string) string {
	t.Helper()
	raw, present := l.payload(t, index)[name]
	if !present {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("第 %d 次追加的 %s 不是字符串：%v", index, name, err)
	}
	return value
}

// memoryHandler 把 slog 的每一条记录收进内存，好让测试看得见被兜住的那几件事。
type memoryHandler struct {
	mutex    sync.Mutex
	messages []string
}

func (h *memoryHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *memoryHandler) Handle(_ context.Context, record slog.Record) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.messages = append(h.messages, record.Message)
	return nil
}

func (h *memoryHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *memoryHandler) WithGroup(string) slog.Handler { return h }

// saw 说明有没有哪一条记录里含着这段话。
func (h *memoryHandler) saw(fragment string) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for _, message := range h.messages {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

// pngLimits 是 DSH 那个测试替身的限额：一批最多两张。
func pngLimits() attachment.ImageLimits {
	return attachment.ImageLimits{
		MaxImageBytes:        1024,
		MaxImagesPerMessage:  2,
		MaxMessageImageBytes: 1024,
		MaxImagePixels:       1_000_000,
		MaxImageDimension:    2000,
		MediaTypes:           []attachment.MediaType{attachment.MediaTypePNG},
	}
}

// fakeStore 是一个只够跑准入这一段的假附件仓库。
type fakeStore struct {
	limits attachment.ImageLimits
	saved  int
	// failNext 非 nil 时，下一次提交报这个错然后清空。
	failNext error
	// onSave 在每一次提交**之前**跑一下，用来在准入中途把请求取消掉。
	onSave func()
}

func (s *fakeStore) ImageLimits() attachment.ImageLimits { return s.limits }

func (s *fakeStore) ValidateImage(context.Context, attachment.ImageInput) error { return nil }

func (s *fakeStore) SaveImage(
	_ context.Context, input attachment.ImageInput,
) (attachment.ImageRef, error) {
	if s.onSave != nil {
		s.onSave()
	}
	if s.failNext != nil {
		failure := s.failNext
		s.failNext = nil
		return attachment.ImageRef{}, failure
	}
	s.saved++
	return attachment.ImageRef{
		ID:        attachment.ID(fmt.Sprintf("att-%d", s.saved)),
		MediaType: input.MediaType,
		Bytes:     3,
		Width:     1,
		Height:    1,
		Name:      input.Name,
	}, nil
}

func (s *fakeStore) ReadImage(
	context.Context, attachment.ImageRef,
) (attachment.StoredImage, error) {
	return attachment.StoredImage{}, errors.New("这个测试替身不读字节")
}

// png 是一张三字节的假 PNG 上传。
func png(name string) attachment.EncodedImage {
	return attachment.EncodedImage{MediaType: attachment.MediaTypePNG, Data: "AAAA", Name: name}
}

// harness 是一次注册表测试要的全套家当。
type harness struct {
	runtime *commands.Runtime
	log     *fakeLog
	agent   *scope.Key
	root    *scope.Scope
	logs    *memoryHandler
	// changes 是可见命令集变化的次数。
	changes int
}

// newHarness 造一张接好假日志的注册表。options 里的 LogOf/Logger/OnChange/NewToken 会被接管。
func newHarness(t *testing.T, options commands.Options) *harness {
	t.Helper()
	h := &harness{log: &fakeLog{}, agent: scope.NewKey("agent"), root: scope.NewRoot(), logs: &memoryHandler{}}
	if options.LogOf == nil {
		options.LogOf = func(*scope.Key) (commands.Log, error) { return h.log, nil }
	}
	if options.Logger == nil {
		options.Logger = slog.New(h.logs)
	}
	if options.OnChange == nil {
		options.OnChange = func() { h.changes++ }
	}
	if options.NewToken == nil {
		options.NewToken = func() string { return "tkn" }
	}
	runtime, err := commands.NewRuntime(options)
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	h.runtime = runtime
	return h
}

// register 登记一条命令，并把撤销登记到测试收尾上。
func (h *harness) register(
	t *testing.T, owner *scope.Scope, definition commands.Definition,
) func(context.Context) error {
	t.Helper()
	undo, err := h.runtime.Register(context.Background(), owner, definition)
	if err != nil {
		t.Fatalf("登记 %q 失败：%v", definition.Name, err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	return undo
}

// succeeding 造一条只交回一句成功文本的命令。
func succeeding(name, text string) commands.Definition {
	return commands.Definition{
		Name:        name,
		Description: "command " + name,
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Kind: commands.ResultSuccess, Text: text}, nil
		},
	}
}

// vision 造一条声明了收图的命令。
func vision(handler commands.Handler) commands.Definition {
	return commands.Definition{
		Name:        "vision",
		Description: "accepts images",
		Input:       &commands.InputDescriptor{Hint: "<objective>", Images: true},
		Handler:     handler,
	}
}

// execute 在这个 agent 上跑一行命令。
func (h *harness) execute(
	ctx context.Context, line string, images ...attachment.EncodedImage,
) (*commands.Execution, error) {
	return h.runtime.Execute(ctx, h.agent, line, images)
}

// names 把一份描述符清单摊成名字。
func names(list []commands.Descriptor) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.Name)
	}
	return out
}

// ---- 解析那一行 ----

func TestParseTakesTheExactLineWithoutNormalizingTrailingInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line  string
		name  string
		input string
	}{
		{"/goal", "goal", ""},
		{"/goal create the thing", "goal", " create the thing"},
		{"/goal\ncreate the thing", "goal", "\ncreate the thing"},
		{"/goal_name-2\t x ", "goal_name-2", "\t x "},
		{"/goal\r", "goal", "\r"},
	}
	for _, item := range cases {
		t.Run(fmt.Sprintf("%q", item.line), func(t *testing.T) {
			t.Parallel()
			parsed, ok := commands.Parse(item.line)
			if !ok {
				t.Fatalf("这一行该解析得出来：%q", item.line)
			}
			if parsed.Name != item.name || parsed.RawInput != item.input {
				t.Fatalf("解析成了 %q + %q，要的是 %q + %q",
					parsed.Name, parsed.RawInput, item.name, item.input)
			}
		})
	}
}

func TestParseRejectsEverythingThatIsNotACommandBoundary(t *testing.T) {
	t.Parallel()
	// 尾巴上紧跟着别的字符就不是一条命令：`/goal/path` 是一条路径，`/Goal` 不是小写名。
	for _, line := range []string{"goal", " /goal", "/", "/Goal", "/goal/path", "/goal🔥", "", "//goal"} {
		t.Run(fmt.Sprintf("%q", line), func(t *testing.T) {
			t.Parallel()
			if parsed, ok := commands.Parse(line); ok {
				t.Fatalf("这一行不该解析得出来：%q，却得到 %+v", line, parsed)
			}
		})
	}
}

// ---- 登记与发现 ----

func TestListsDescriptorsWithInputMetadataAndFindsThemByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	hint := commands.InputDescriptor{Hint: "<target>"}
	h.register(t, h.root, commands.Definition{
		Name:        "inspect",
		Description: "Inspect state",
		Input:       &hint,
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	listed := h.runtime.List(h.agent)
	if len(listed) != 1 || listed[0].Name != "inspect" || listed[0].Description != "Inspect state" {
		t.Fatalf("清单不对：%+v", listed)
	}
	if listed[0].Input == nil || listed[0].Input.Hint != "<target>" || listed[0].Input.Images {
		t.Fatalf("输入元数据不对：%+v", listed[0].Input)
	}
	// 登记之后调用方再改自己那个结构体，不该把已经发布出去的描述符也改了。
	hint.Hint = "mutated"
	if again := h.runtime.List(h.agent); again[0].Input.Hint != "<target>" {
		t.Fatalf("描述符跟着调用方一起变了：%q", again[0].Input.Hint)
	}
	if found, ok := h.runtime.Find(h.agent, "inspect"); !ok || found.Name != "inspect" {
		t.Fatalf("该找得到 inspect：%+v %v", found, ok)
	}
	if _, ok := h.runtime.Find(h.agent, "missing"); ok {
		t.Fatal("不存在的名字不该找得到")
	}
}

func TestListSortsTheEffectiveNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("zeta", "z"))
	h.register(t, h.root, succeeding("alpha", "a"))
	h.register(t, h.root, succeeding("middle", "m"))

	got := names(h.runtime.List(h.agent))
	if strings.Join(got, ",") != "alpha,middle,zeta" {
		t.Fatalf("该按名字排：%v", got)
	}
}

func TestAgentScopedRegistrationShadowsTheGlobalOneAndLeavesWithItsScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	mine, err := scope.New(h.agent, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	h.register(t, h.root, succeeding("shared", "global"))
	h.register(t, mine, succeeding("shared", "scoped"))

	if got := names(h.runtime.List(h.agent)); strings.Join(got, ",") != "shared" {
		t.Fatalf("遮蔽不该让名字出现两遍：%v", got)
	}
	// 别人的 agent 看不见这一层，读到的仍然是全局那一条。
	if got := names(h.runtime.List(scope.NewKey("other"))); strings.Join(got, ",") != "shared" {
		t.Fatalf("别的 agent 该看见全局那一条：%v", got)
	}
	execution, err := h.execute(context.Background(), "/shared")
	if err != nil || execution == nil || execution.Result.Text != "scoped" {
		t.Fatalf("该跑作用域那一条：%+v %v", execution, err)
	}

	if err := mine.Dispose(context.Background()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}

	execution, err = h.execute(context.Background(), "/shared")
	if err != nil || execution == nil || execution.Result.Text != "global" {
		t.Fatalf("作用域走了就该回落到全局那一条：%+v %v", execution, err)
	}
}

func TestRejectsADuplicateWithinOneLayerWhilePermittingAScopedShadow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	mine, err := scope.New(h.agent, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	h.register(t, h.root, succeeding("same", "global"))

	_, err = h.runtime.Register(context.Background(), h.root, succeeding("same", "again"))
	if !errors.Is(err, commands.ErrInvalidDefinition) ||
		!strings.Contains(err.Error(), "that agent's own scope") {
		t.Fatalf("全局重名该指出「去那个 agent 自己的作用域上登记」：%v", err)
	}

	h.register(t, mine, succeeding("same", "scoped"))
	_, err = h.runtime.Register(context.Background(), mine, succeeding("same", "scoped again"))
	if !errors.Is(err, commands.ErrInvalidDefinition) ||
		!strings.Contains(err.Error(), "already registered in this scope") {
		t.Fatalf("同一层里重名该被拒：%v", err)
	}
}

func TestNotifiesOnRegistrationAndOnRemovalAndContainsABrokenObserver(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})

	undo, err := h.runtime.Register(context.Background(), h.root, succeeding("live", "x"))
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	// 第二次撤销是幂等的：它不该再报一次变化。
	if err := undo(context.Background()); err != nil {
		t.Fatalf("第二次撤销失败：%v", err)
	}
	if h.changes != 2 {
		t.Fatalf("登记一次加撤销一次该通知两次，通知了 %d 次", h.changes)
	}

	// 一个炸了的观察者不该让那次登记回滚——界面刷新不是登记路径上的承重结构。
	broken := newHarness(t, commands.Options{OnChange: func() { panic("observer threw") }})
	broken.register(t, broken.root, succeeding("contained", "x"))
	if _, ok := broken.runtime.Find(broken.agent, "contained"); !ok {
		t.Fatal("观察者炸了不该把登记一起带走")
	}
	if !broken.logs.saw("commands/change 观察者 panic 了") {
		t.Fatalf("该记一条警告：%+v", broken.logs.messages)
	}

	// 没接观察者也要走得通。
	quiet := newHarness(t, commands.Options{OnChange: func() {}})
	quiet.register(t, quiet.root, succeeding("silent", "x"))
}

func TestRejectsAnInvalidDefinitionAtTheBoundary(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		definition commands.Definition
		fragment   string
	}{
		"名字不是小写形状": {
			definition: succeeding("Bad", "x"),
			fragment:   "command name",
		},
		"名字是空的": {
			definition: succeeding("", "x"),
			fragment:   "command name",
		},
		"描述全是空白": {
			definition: commands.Definition{
				Name: "blank-description", Description: "  ",
				Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
					return commands.Result{Kind: commands.ResultSuccess}, nil
				},
			},
			fragment: "description must not be empty",
		},
		"输入提示全是空白": {
			definition: commands.Definition{
				Name: "blank-hint", Description: "d",
				Input: &commands.InputDescriptor{Hint: " "},
				Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
					return commands.Result{Kind: commands.ResultSuccess}, nil
				},
			},
			fragment: "input hint must not be empty",
		},
		"没给处理器": {
			definition: commands.Definition{Name: "no-handler", Description: "d"},
			fragment:   "handler must not be nil",
		},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, commands.Options{})
			_, err := h.runtime.Register(context.Background(), h.root, item.definition)
			if !errors.Is(err, commands.ErrInvalidDefinition) ||
				!strings.Contains(err.Error(), item.fragment) {
				t.Fatalf("该被拒并说清是哪一条：%v", err)
			}
		})
	}
}

// ---- 一次执行 ----

func TestHandsTheHandlerTheExactInvocationAndIgnoresWhatItCannotResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	var seen commands.Invocation
	h.register(t, h.root, commands.Definition{
		Name: "run", Description: "Run it",
		Handler: func(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
			seen = invocation
			return commands.Result{Kind: commands.ResultSuccess, Text: "ok"}, nil
		},
	})

	execution, err := h.execute(context.Background(), "/run  untouched ")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if execution.Result.Kind != commands.ResultSuccess || execution.Result.Text != "ok" {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
	if execution.ID != "cmd-tkn-1" {
		t.Fatalf("配对号该带实例令牌：%q", execution.ID)
	}
	if seen.ID != execution.ID || seen.Agent != h.agent || seen.RawInput != "  untouched " {
		t.Fatalf("交给处理器的调用不对：%+v", seen)
	}
	if len(seen.Attachments) != 0 {
		t.Fatalf("没声明收图就不该有附件：%+v", seen.Attachments)
	}

	// 语法不成立、名字不认得，两种都交回 nil 而不是一个错误。
	for _, line := range []string{"run", "/missing"} {
		execution, err := h.execute(context.Background(), line)
		if execution != nil || err != nil {
			t.Fatalf("%q 该什么都不交回：%+v %v", line, execution, err)
		}
	}
}

func TestLogsAPairedRunAndDoneAroundASuccessfulHandler(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("deploy", "deployed"))

	execution, err := h.execute(context.Background(), "/deploy now")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	// 这一对是**直接的、只进日志的**追加：没有回合把它们包起来。
	if got := strings.Join(h.log.kinds(), ","); got != "command/run,command/done" {
		t.Fatalf("该恰好落一对生命周期事件：%v", got)
	}
	if got := h.log.field(t, 0, "name"); got != `"deploy"` {
		t.Fatalf("command/run 的 name 不对：%v", got)
	}
	if got := h.log.field(t, 0, "args"); got != `" now"` {
		t.Fatalf("命令名后面那段该一字不改地记下来：%v", got)
	}
	if got := h.log.field(t, 0, "source"); got != `{"kind":"user"}` {
		t.Fatalf("来源不对：%v", got)
	}
	if got := h.log.field(t, 1, "kind"); got != `"success"` {
		t.Fatalf("command/done 的 kind 不对：%v", got)
	}
	if got := h.log.text(t, 1, "text"); got != "deployed" {
		t.Fatalf("command/done 的 text 不对：%q", got)
	}
	// 落进日志的配对号就是交回给调用方的那个——界面靠它把 RPC 回执和日志对上。
	if h.log.text(t, 0, "commandId") != string(execution.ID) ||
		h.log.text(t, 1, "commandId") != string(execution.ID) {
		t.Fatalf("两条记录该带同一个配对号：%+v", h.log.appended)
	}
}

func TestPreservesAnEarlierAuthoritativeEventReferenceOnSuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	source := 3
	h.register(t, h.root, commands.Definition{
		Name: "linked", Description: "Link outcome",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{
				Kind: commands.ResultSuccess, Text: "linked", SourceEventSeq: &source,
			}, nil
		},
	})

	execution, err := h.execute(context.Background(), "/linked")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if execution.Result.SourceEventSeq == nil || *execution.Result.SourceEventSeq != 3 {
		t.Fatalf("那个引用该原样交回：%+v", execution.Result)
	}
	if got := h.log.field(t, 1, "sourceEventSeq"); got != "3" {
		t.Fatalf("那个引用该落进 command/done：%v", got)
	}
}

func TestSkipInputRecordOmitsArgsWhileTheHandlerStillSeesThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	var seen string
	h.register(t, h.root, commands.Definition{
		Name: "private", Description: "Record privately", SkipInputRecord: true,
		Handler: func(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
			seen = invocation.RawInput
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	if _, err := h.execute(context.Background(), "/private keep this once"); err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if seen != " keep this once" {
		t.Fatalf("处理器还是该看得见输入：%q", seen)
	}
	// 有一条自己的权威事件拥有这份负载的命令，不该在日志里把它再抄一遍。
	if _, present := h.log.payload(t, 0)["args"]; present {
		t.Fatalf("command/run 不该带 args：%s", h.log.appended[0].raw)
	}
}

func TestMintsDistinctMonotonicCommandIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("first", "1"))
	h.register(t, h.root, succeeding("second", "2"))

	first, err := h.execute(context.Background(), "/first")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	second, err := h.execute(context.Background(), "/second")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if first.ID != "cmd-tkn-1" || second.ID != "cmd-tkn-2" {
		t.Fatalf("配对号该是实例内单调的：%q %q", first.ID, second.ID)
	}
}

func TestTheDefaultTokenIsAFreshUUIDPrefix(t *testing.T) {
	t.Parallel()
	log := &fakeLog{}
	build := func() *commands.Runtime {
		runtime, err := commands.NewRuntime(commands.Options{
			LogOf: func(*scope.Key) (commands.Log, error) { return log, nil },
		})
		if err != nil {
			t.Fatalf("造注册表失败：%v", err)
		}
		return runtime
	}
	root := scope.NewRoot()
	agent := scope.NewKey("agent")
	mint := func(runtime *commands.Runtime) commands.ID {
		if _, err := runtime.Register(context.Background(), root, succeeding("go", "x")); err != nil {
			t.Fatalf("登记失败：%v", err)
		}
		execution, err := runtime.Execute(context.Background(), agent, "/go", nil)
		if err != nil {
			t.Fatalf("执行失败：%v", err)
		}
		return execution.ID
	}

	first := mint(build())
	// 前一个作用域上的登记还在，所以第二张表得换一个根。
	root = scope.NewRoot()
	second := mint(build())
	if first == second {
		t.Fatalf("两个实例的令牌该不一样：%q %q", first, second)
	}
	for _, id := range []commands.ID{first, second} {
		if !strings.HasPrefix(string(id), "cmd-") || len(id) != len("cmd-")+8+len("-1") {
			t.Fatalf("配对号的形状不对：%q", id)
		}
	}
}

func TestSettlesAnExpectedErrorResultWithoutRaising(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, commands.Definition{
		Name: "denied", Description: "Denied",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Kind: commands.ResultError, Text: "not now"}, nil
		},
	})

	execution, err := h.execute(context.Background(), "/denied")
	if err != nil {
		t.Fatalf("一次预期之内的失败不该抛给调用方：%v", err)
	}
	if execution.Result.Kind != commands.ResultError || execution.Result.Text != "not now" {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
	if h.log.text(t, 1, "kind") != "error" || h.log.text(t, 1, "text") != "not now" {
		t.Fatalf("该落成 command/done 的 error：%s", h.log.appended[1].raw)
	}
}

func TestASuccessResultMayCarryNoText(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("silent", ""))

	execution, err := h.execute(context.Background(), "/silent")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if execution.Result.Kind != commands.ResultSuccess || execution.Result.Text != "" {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
	if _, present := h.log.payload(t, 1)["text"]; present {
		t.Fatalf("空的 text 不该落进日志：%s", h.log.appended[1].raw)
	}
}

func TestSettlesAndRaisesWhenTheHandlerFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("handler exploded")
	cases := map[string]commands.Handler{
		"处理器返回错误": func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{}, boom
		},
		"处理器 panic 了": func(context.Context, commands.Invocation) (commands.Result, error) {
			panic("handler exploded")
		},
	}
	for label, handler := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, commands.Options{})
			h.register(t, h.root, commands.Definition{
				Name: "boom", Description: "Throw", Handler: handler,
			})

			execution, err := h.execute(context.Background(), "/boom")
			if execution != nil || err == nil || !strings.Contains(err.Error(), "handler exploded") {
				t.Fatalf("该把这个错误抛给调用方：%+v %v", execution, err)
			}
			// 炸了也要留下审计：一次进过处理器的执行必须有结局。
			if got := strings.Join(h.log.kinds(), ","); got != "command/run,command/done" {
				t.Fatalf("生命周期那一对该是完整的：%v", got)
			}
			if h.log.text(t, 1, "kind") != "error" ||
				!strings.Contains(h.log.text(t, 1, "text"), "handler exploded") {
				t.Fatalf("该落成 command/done 的 error：%s", h.log.appended[1].raw)
			}
		})
	}
}

func TestRejectsAMalformedHandlerResult(t *testing.T) {
	t.Parallel()
	negative := -1
	cases := map[string]struct {
		result   commands.Result
		fragment string
	}{
		"没给 kind": {
			result: commands.Result{}, fragment: "unknown result kind",
		},
		"词汇表外的 kind": {
			result:   commands.Result{Kind: commands.ResultKind("future"), Text: "x"},
			fragment: "unknown result kind",
		},
		"错误结果的 text 是空的": {
			result:   commands.Result{Kind: commands.ResultError, Text: " "},
			fragment: "error text must be a non-empty string",
		},
		"成功结果指了一个负的序号": {
			result:   commands.Result{Kind: commands.ResultSuccess, SourceEventSeq: &negative},
			fragment: "sourceEventSeq must be non-negative",
		},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, commands.Options{})
			h.register(t, h.root, commands.Definition{
				Name: "broken", Description: "Broken",
				Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
					return item.result, nil
				},
			})

			_, err := h.execute(context.Background(), "/broken")
			if !errors.Is(err, commands.ErrInvalidResult) ||
				!strings.Contains(err.Error(), item.fragment) {
				t.Fatalf("该被拒并说清是哪一条：%v", err)
			}
			// 一份不合法的结果走的是「处理器炸了」那条路：审计照样是完整的。
			if got := strings.Join(h.log.kinds(), ","); got != "command/run,command/done" {
				t.Fatalf("生命周期那一对该是完整的：%v", got)
			}
		})
	}
}

func TestAnErrorResultDropsAnyAuthoritativeReference(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	source := 0
	h.register(t, h.root, commands.Definition{
		Name: "denied", Description: "Denied",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{
				Kind: commands.ResultError, Text: "no", SourceEventSeq: &source,
			}, nil
		},
	})

	execution, err := h.execute(context.Background(), "/denied")
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	// 那个槽的意思是「有一条更权威的成功呈现」，一条失败里带着它是自相矛盾的。
	if execution.Result.SourceEventSeq != nil {
		t.Fatalf("错误结果不该带那个引用：%+v", execution.Result)
	}
	if _, present := h.log.payload(t, 1)["sourceEventSeq"]; present {
		t.Fatalf("那个引用不该落进日志：%s", h.log.appended[1].raw)
	}
}

// ---- 取消 ----

func TestStopsWaitingOnAHangingHandlerAndSettlesTheCancellation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	h.register(t, h.root, commands.Definition{
		Name: "hang", Description: "Hang",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			close(entered)
			<-release
			return commands.Result{Kind: commands.ResultSuccess, Text: "late"}, nil
		},
	})

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go func() {
		<-entered
		cancel(errors.New("operator cancelled command"))
	}()

	_, err := h.execute(ctx, "/hang")
	if err == nil || err.Error() != "operator cancelled command" {
		t.Fatalf("该抛出操作者那句话：%v", err)
	}
	// command/run 必须在取消之前就落了，那一对才配得上。
	if got := strings.Join(h.log.kinds(), ","); got != "command/run,command/done" {
		t.Fatalf("生命周期那一对该是完整的：%v", got)
	}
	if h.log.text(t, 1, "text") != "operator cancelled command" {
		t.Fatalf("那句话该原样落进 command/done：%s", h.log.appended[1].raw)
	}
}

func TestAnAlreadyCancelledRequestWritesNothingAtAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("wait", "x"))
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("already gone"))

	execution, err := h.execute(ctx, "/wait")
	if execution != nil || err == nil || err.Error() != "already gone" {
		t.Fatalf("该在动笔之前就认掉：%+v %v", execution, err)
	}
	// 这次执行从没开始过，日志上就不该有它的痕迹。
	if h.log.len() != 0 {
		t.Fatalf("一个字节都不该写：%v", h.log.kinds())
	}
}

func TestACancellationRaisedInsideTheHandlerWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	h.register(t, h.root, commands.Definition{
		Name: "self-abort", Description: "Cancel before returning",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			cancel(errors.New("cancelled in handler"))
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	// 一个自己把请求撤了、然后照常返回成功的处理器必须算取消，而不是让 select 掷骰子。
	_, err := h.execute(ctx, "/self-abort")
	if err == nil || err.Error() != "cancelled in handler" {
		t.Fatalf("该认取消：%v", err)
	}
	if h.log.text(t, 1, "text") != "cancelled in handler" {
		t.Fatalf("那句话该落进 command/done：%s", h.log.appended[1].raw)
	}
}

// ---- 日志这道接缝失败的时候 ----

func TestWritesNothingForAnAdmissionMiss(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, succeeding("real", "x"))

	if _, err := h.execute(context.Background(), "not a command"); err != nil {
		t.Fatalf("语法不成立不是一个错误：%v", err)
	}
	if _, err := h.execute(context.Background(), "/missing"); err != nil {
		t.Fatalf("名字不认得不是一个错误：%v", err)
	}
	if h.log.len() != 0 {
		t.Fatalf("准入没过就不该写日志：%v", h.log.kinds())
	}
}

func TestFailsLoudlyWhenTheRunRecordCannotBeWritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.log.failOn = commands.EventRun
	entered := false
	h.register(t, h.root, commands.Definition{
		Name: "deploy", Description: "Deploy",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			entered = true
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	// 这一条写不进去就必须大声失败：后面那条 command/done 会变成一条配不上对的孤儿记录。
	_, err := h.execute(context.Background(), "/deploy")
	if !errors.Is(err, errAppend) {
		t.Fatalf("该把追加的失败抛出来：%v", err)
	}
	if entered {
		t.Fatal("落不下 command/run 就不该进处理器")
	}
}

func TestFailsLoudlyWhenTheDoneRecordCannotBeWrittenOnASuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.log.failOn = commands.EventDone
	h.register(t, h.root, succeeding("deploy", "deployed"))

	// 走到这里说明处理器自己是好的，那条记录没写进去就是一次真正的失败。
	execution, err := h.execute(context.Background(), "/deploy")
	if execution != nil || !errors.Is(err, errAppend) {
		t.Fatalf("该把追加的失败抛出来：%+v %v", execution, err)
	}
}

func TestContainsAFailedDoneRecordOnTheThrowingPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.log.failOn = commands.EventDone
	h.register(t, h.root, commands.Definition{
		Name: "boom", Description: "Throw",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{}, errors.New("handler exploded")
		},
	})

	// 这里已经有一个要抛出去的错误了：让追加的失败盖掉它会把调用方引到错误的方向上。
	_, err := h.execute(context.Background(), "/boom")
	if err == nil || err.Error() != "handler exploded" {
		t.Fatalf("该抛处理器自己那条错误：%v", err)
	}
	if !h.logs.saw("command/done 没写进去") {
		t.Fatalf("该记一条警告：%+v", h.logs.messages)
	}
}

func TestRefusesToRunWhenTheAgentHasNoWritableLog(t *testing.T) {
	t.Parallel()
	broken := errors.New("no session for this agent")
	cases := map[string]struct {
		logOf func(*scope.Key) (commands.Log, error)
		check func(*testing.T, error)
	}{
		"取日志报错": {
			logOf: func(*scope.Key) (commands.Log, error) { return nil, broken },
			check: func(t *testing.T, err error) {
				if !errors.Is(err, broken) {
					t.Fatalf("该原样转出去：%v", err)
				}
			},
		},
		"取日志给了个空答复": {
			logOf: func(*scope.Key) (commands.Log, error) { return nil, nil },
			check: func(t *testing.T, err error) {
				// 一条 (nil, nil) 的答复是装配方的 bug，它往下走就是解引用 panic。
				if !errors.Is(err, commands.ErrInvalidConfig) {
					t.Fatalf("该当场认成配置问题：%v", err)
				}
			},
		},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, commands.Options{LogOf: item.logOf})
			h.register(t, h.root, succeeding("deploy", "x"))
			_, err := h.execute(context.Background(), "/deploy")
			item.check(t, err)
		})
	}
}

func TestPublishesItsEventVocabularyForTheAssembler(t *testing.T) {
	t.Parallel()
	// Go 没有声明合并，所以由本包交出单子、装配方自己拼进会话词汇表。
	got := commands.EventTypes()
	if len(got) != 2 || got[0] != commands.EventRun || got[1] != commands.EventDone {
		t.Fatalf("该交出这两条生命周期事件：%v", got)
	}
}

func TestNewRuntimeNeedsAPathToTheSessionLog(t *testing.T) {
	t.Parallel()
	_, err := commands.NewRuntime(commands.Options{})
	if !errors.Is(err, commands.ErrInvalidConfig) {
		t.Fatalf("没给 LogOf 就该被拒：%v", err)
	}
}

// ---- 图片准入 ----

func TestListsImageAcceptanceOnTheDescriptor(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, vision(func(context.Context, commands.Invocation) (commands.Result, error) {
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))
	h.register(t, h.root, commands.Definition{
		Name: "plain-input", Description: "no images",
		Input: &commands.InputDescriptor{Hint: "x", Images: false},
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	byName := map[string]commands.Descriptor{}
	for _, item := range h.runtime.List(h.agent) {
		byName[item.Name] = item
	}
	if !byName["vision"].Input.Images {
		t.Fatalf("声明了收图就该摆在描述符上：%+v", byName["vision"].Input)
	}
	if byName["plain-input"].Input.Images {
		t.Fatalf("没声明就该是假：%+v", byName["plain-input"].Input)
	}
}

func TestSettlesImagesSentToANonDeclaringCommandBeforeTheHandler(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{Attachments: &fakeStore{limits: pngLimits()}})
	entered := false
	h.register(t, h.root, commands.Definition{
		Name: "deploy", Description: "Deploy",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			entered = true
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})

	execution, err := h.execute(context.Background(), "/deploy now", png(""))
	if err != nil {
		t.Fatalf("这是一次预期之内的失败，不该抛：%v", err)
	}
	if execution.Result.Kind != commands.ResultError ||
		execution.Result.Text != "/deploy does not accept image attachments" {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
	if entered {
		t.Fatal("准入没过就不该进处理器")
	}
	if got := strings.Join(h.log.kinds(), ","); got != "command/run,command/done" {
		t.Fatalf("生命周期那一对该是完整的：%v", got)
	}
}

func TestSettlesADeclaringCommandWhenNoAttachmentStoreIsComposed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{})
	h.register(t, h.root, vision(func(context.Context, commands.Invocation) (commands.Result, error) {
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))

	// 拿不到仓库就干不了活，这件事必须让用户看见，而不是把图默默丢掉。
	execution, err := h.execute(context.Background(), "/vision x", png(""))
	if err != nil {
		t.Fatalf("这是一次预期之内的失败，不该抛：%v", err)
	}
	want := "/vision: image attachments are unavailable because no attachment store is composed"
	if execution.Result.Kind != commands.ResultError || execution.Result.Text != want {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
}

func TestAdmitsOrderedImageBlocksAndLeavesPlainInvocationsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{Attachments: &fakeStore{limits: pngLimits()}})
	var seen [][]string
	h.register(t, h.root, vision(func(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
		batch := make([]string, 0, len(invocation.Attachments))
		for _, block := range invocation.Attachments {
			batch = append(batch, string(block.Attachment.ID)+":"+block.Attachment.Name)
		}
		seen = append(seen, batch)
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))

	if _, err := h.execute(context.Background(), "/vision x", png("a.png"), png("b.png")); err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if _, err := h.execute(context.Background(), "/vision y"); err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("该进过两次处理器：%+v", seen)
	}
	// 顺序就是提交顺序，界面靠它把每一块对回自己那张图。
	if strings.Join(seen[0], ",") != "att-1:a.png,att-2:b.png" {
		t.Fatalf("图块的顺序不对：%v", seen[0])
	}
	if len(seen[1]) != 0 {
		t.Fatalf("不带图的调用该拿到空的一批：%v", seen[1])
	}
}

func TestSettlesAnAdmissionLimitFailureAsAnErrorResult(t *testing.T) {
	t.Parallel()
	h := newHarness(t, commands.Options{Attachments: &fakeStore{limits: pngLimits()}})
	entered := false
	h.register(t, h.root, vision(func(context.Context, commands.Invocation) (commands.Result, error) {
		entered = true
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))

	execution, err := h.execute(context.Background(), "/vision x", png(""), png(""), png(""))
	if err != nil {
		t.Fatalf("一条限额违规是预期之内的失败，不该抛：%v", err)
	}
	if execution.Result.Kind != commands.ResultError ||
		execution.Result.Text != "Image batch exceeds the configured image-count limit." {
		t.Fatalf("结果不对：%+v", execution.Result)
	}
	if entered {
		t.Fatal("准入没过就不该进处理器")
	}
	if h.log.text(t, -1, "kind") != "error" {
		t.Fatalf("该落成 command/done 的 error：%s", h.log.appended[1].raw)
	}
}

func TestHonorsACancellationThatLandsDuringAdmission(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	store := &fakeStore{limits: pngLimits()}
	store.onSave = func() { cancel(errors.New("operator cancelled during admission")) }
	h := newHarness(t, commands.Options{Attachments: store})
	entered := false
	h.register(t, h.root, vision(func(context.Context, commands.Invocation) (commands.Result, error) {
		entered = true
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))

	// 取消必须在处理器跑起来之前认掉：准入可能等一段慢存储，而一个在调用方已经撤回
	// 之后才进去的处理器，会动到那些状态，重试的调用方接着又动一遍。
	_, err := h.execute(ctx, "/vision x", png(""))
	if err == nil || err.Error() != "operator cancelled during admission" {
		t.Fatalf("该抛出那句取消理由：%v", err)
	}
	if entered {
		t.Fatal("已经取消了就不该进处理器")
	}
	if h.log.text(t, -1, "text") != "operator cancelled during admission" {
		t.Fatalf("那句话该落进 command/done：%s", h.log.appended[1].raw)
	}
}

func TestLogsAndRethrowsANonAttachmentAdmissionFailure(t *testing.T) {
	t.Parallel()
	store := &fakeStore{limits: pngLimits(), failNext: errors.New("disk gone")}
	h := newHarness(t, commands.Options{Attachments: store})
	h.register(t, h.root, vision(func(context.Context, commands.Invocation) (commands.Result, error) {
		return commands.Result{Kind: commands.ResultSuccess}, nil
	}))

	// 一次存储故障不是「说给用户听的失败」：它要抛回派发它的界面，同时留下审计。
	_, err := h.execute(context.Background(), "/vision x", png(""))
	if err == nil || err.Error() != "disk gone" {
		t.Fatalf("该原样抛出去：%v", err)
	}
	if h.log.text(t, -1, "text") != "disk gone" {
		t.Fatalf("该落成 command/done 的 error：%s", h.log.appended[1].raw)
	}
}

// ---- 那条持久不变量 ----
//
// 逐条对着 DSH 的 tests/invariant.spec.ts 走。

// eventAt 造一条带序号和负载的事件。
func eventAt(t *testing.T, seq int, kind session.EventType, data any) session.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("造事件失败：%v", err)
	}
	return session.Event{Type: kind, Seq: seq, Data: raw}
}

// runAt 造一条 command/run。
func runAt(t *testing.T, seq int, id commands.ID) session.Event {
	t.Helper()
	return eventAt(t, seq, commands.EventRun, commands.RunData{
		ID: id, Name: "linked", Source: commands.Source{Kind: commands.SourceUser},
	})
}

// doneAt 造一条 command/done。
func doneAt(t *testing.T, seq int, id commands.ID, kind commands.ResultKind, source *int) session.Event {
	t.Helper()
	return eventAt(t, seq, commands.EventDone, commands.DoneData{
		ID: id, Kind: kind, Text: "settled", SourceEventSeq: source,
	})
}

// plainAt 造一条不属于本包的领域事件。
func plainAt(t *testing.T, seq int) session.Event {
	t.Helper()
	return eventAt(t, seq, session.EventTurnStart, map[string]int{"turn": 1})
}

func TestTheTraceAcceptsAPairSettledAgainstAnEarlierDomainEvent(t *testing.T) {
	t.Parallel()
	source := 0
	trace, err := commands.ValidateLog([]session.Event{
		plainAt(t, 0),
		runAt(t, 1, "cmd-valid"),
		doneAt(t, 2, "cmd-valid", commands.ResultSuccess, &source),
	})
	if err != nil {
		t.Fatalf("这一段该走得通：%v", err)
	}
	if trace.Runs() != 1 {
		t.Fatalf("该记下一个配对号，记了 %d 个", trace.Runs())
	}
}

func TestTheTraceIgnoresEventsThatAreNotItsOwn(t *testing.T) {
	t.Parallel()
	trace, err := commands.ValidateLog([]session.Event{plainAt(t, 0), plainAt(t, 1)})
	if err != nil {
		t.Fatalf("别人的事件该被放过：%v", err)
	}
	if trace.Runs() != 0 {
		t.Fatalf("不该记下任何配对号，记了 %d 个", trace.Runs())
	}
}

func TestTheTraceCatchesAnUnpairedOrRepeatedLifecycle(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		events   []session.Event
		fragment string
	}{
		"同一个配对号跑了两次": {
			events: []session.Event{
				runAt(t, 0, "cmd-twice"),
				runAt(t, 1, "cmd-twice"),
			},
			fragment: `command/run repeats commandId "cmd-twice"`,
		},
		"结算配不上任何一次执行": {
			events:   []session.Event{doneAt(t, 0, "cmd-orphan", commands.ResultSuccess, nil)},
			fragment: `command/done "cmd-orphan" pairs no prior command/run in this log`,
		},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			_, err := commands.ValidateLog(item.events)
			if err == nil || !strings.Contains(err.Error(), item.fragment) {
				t.Fatalf("该报这一条：%v", err)
			}
		})
	}
}

func TestTheTraceCatchesAnInvalidAuthoritativeReference(t *testing.T) {
	t.Parallel()
	negative := -1
	self := 2
	ahead := 5
	missing := 1
	lifecycle := 0
	cases := map[string]struct {
		kind   commands.ResultKind
		source *int
	}{
		"序号是负的":         {commands.ResultSuccess, &negative},
		"指着自己":          {commands.ResultSuccess, &self},
		"指着还没发生的事件":     {commands.ResultSuccess, &ahead},
		"指着这条日志里没有的序号":  {commands.ResultSuccess, &missing},
		"指着另一条命令记录":     {commands.ResultSuccess, &lifecycle},
		"一条失败带着只属于成功的槽": {commands.ResultError, &lifecycle},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			// 序号 0 是那条 command/run，1 空着，2 是这条结算。
			_, err := commands.ValidateLog([]session.Event{
				runAt(t, 0, "cmd-linked"),
				doneAt(t, 2, "cmd-linked", item.kind, item.source),
			})
			if err == nil || !strings.Contains(err.Error(), "invalid sourceEventSeq") {
				t.Fatalf("该报这一条：%v", err)
			}
		})
	}
}

func TestTheTraceCatchesAnUnreadablePayload(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		event    session.Event
		fragment string
	}{
		"command/run 的负载读不回来": {
			event:    session.Event{Type: commands.EventRun, Data: json.RawMessage(`"not an object"`)},
			fragment: "command/run carries an unreadable payload",
		},
		"command/done 的负载读不回来": {
			event:    session.Event{Type: commands.EventDone, Data: json.RawMessage(`42`)},
			fragment: "command/done carries an unreadable payload",
		},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			_, err := commands.ValidateLog([]session.Event{item.event})
			if err == nil || !strings.Contains(err.Error(), item.fragment) {
				t.Fatalf("该报这一条：%v", err)
			}
		})
	}
}

func TestTheTraceCarriesAnOpenCommandForwardAcrossInstallation(t *testing.T) {
	t.Parallel()
	// 装的那一刻先把已经装进来的日志走一遍，后来那条结算才配得上对。
	h := newInvariantHarness(t, plainAt(t, 0), runAt(t, 1, "cmd-open"))
	undo := h.register(t)
	t.Cleanup(undo)

	source := 0
	if failure := violationOf(func() {
		h.emit(doneAt(t, 2, "cmd-open", commands.ResultSuccess, &source))
	}); failure != nil {
		t.Fatalf("这一条该配得上：%v", failure)
	}
}

func TestTheTraceCatchesABrokenPairAlreadyInTheLog(t *testing.T) {
	t.Parallel()
	// 一份历史里就带着拆了对的生命周期的会话，必须在装载这一刻就响。
	h := newInvariantHarness(t, doneAt(t, 0, "cmd-late", commands.ResultSuccess, nil))
	failure := violationOf(func() { h.register(t) })
	if failure == nil {
		t.Fatal("装载那一刻就该响")
	}
	if failure.PackageName != commands.PackageName {
		t.Fatalf("该记在本包名下：%q", failure.PackageName)
	}
	if !strings.Contains(failure.Message, "pairs no prior command/run") {
		t.Fatalf("说法不对：%q", failure.Message)
	}
}

func TestTheTraceCatchesABrokenPairAppendedLater(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	t.Cleanup(undo)

	failure := violationOf(func() { h.emit(doneAt(t, 0, "cmd-late", commands.ResultSuccess, nil)) })
	if failure == nil || !strings.Contains(failure.Message, "pairs no prior command/run") {
		t.Fatalf("后续追加也该被盯着：%+v", failure)
	}
}

func TestUnregisteringTheCommandInvariantsStopsTheCheck(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	undo()

	if h.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", h.unsubscribed)
	}
}

func TestRegisterCommandInvariantsNeedsAllThreeSeams(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	loaded := func() []session.Event { return nil }
	subscribe := func(func(session.Event)) func() { return func() {} }

	cases := map[string]func() error{
		"没给注册表": func() error {
			_, err := commands.RegisterInvariants(context.Background(), nil, loaded, subscribe)
			return err
		},
		"没给已装载日志": func() error {
			_, err := commands.RegisterInvariants(context.Background(), registry, nil, subscribe)
			return err
		},
		"没给订阅": func() error {
			_, err := commands.RegisterInvariants(context.Background(), registry, loaded, nil)
			return err
		},
	}
	for label, run := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if err := run(); !errors.Is(err, commands.ErrInvalidConfig) {
				t.Fatalf("该被拒绝并认得出哨兵：%v", err)
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
	undo, err := commands.RegisterInvariants(
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

// violationOf 跑一段可能违例的代码，交出那条违例，没违例就交出 nil。
func violationOf(run func()) *invariants.Error {
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
	return caught
}
