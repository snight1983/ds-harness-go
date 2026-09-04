// 本文件验本包那条运行期不变量：一份循环装出来的请求，必须和它那个会话在派发
// 这一刻的耐久日志推导得出的东西完全一致。
//
// 源: packages/core/agent-loop/src/invariant.ts:1-63
//
// 整组用例一律**从 [llm.Runtime.Stream] 走**，而不是直接调 [checkLoopRequest]：
// 这条检查是装在 llm/stream 瀑布上的一层规则，验的是真的被派发出去的那份请求，
// 绕过瀑布去调它就验不到「它确实装上了」这件事。
//
// 派发不需要任何适配器：这条规则在 next 之前就把请求查完了，而没有适配器时
// 那条流只是在**被读**的时候吐一个失败分块——用例一块都不读。

package agentloop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// invariantWorld 是这一组用例的舞台：一个装好检查的 llm 运行时，加上一个会话存储。
type invariantWorld struct {
	runtime *llm.Runtime
	store   *session.Store
	owner   *scope.Scope
}

// newInvariantWorld 造一个舞台，并把本包的检查装到那个运行时上，用例结束时注销。
func newInvariantWorld(t *testing.T) *invariantWorld {
	t.Helper()
	world := &invariantWorld{
		runtime: llm.NewRuntime(llm.RuntimeOptions{}),
		store:   newStore(t),
		owner:   rootScope(t),
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)

	dispose, err := RegisterInvariants(
		context.Background(), registry, world.owner, world.runtime, world.store)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}
	t.Cleanup(dispose)
	return world
}

// dispatch 派发一次请求，把这条检查抛出来的违例接下来；没有违例时返回 nil。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func (w *invariantWorld) dispatch(t *testing.T, options llm.GenerateOptions) *invariants.Error {
	t.Helper()

	var thrown any
	func() {
		defer func() { thrown = recover() }()
		if _, err := w.runtime.Stream(context.Background(), options); err != nil {
			t.Fatalf("开流不该失败：%v", err)
		}
	}()

	if thrown == nil {
		return nil
	}
	failure, ok := thrown.(*invariants.Error)
	if !ok {
		t.Fatalf("该抛出 *invariants.Error，实际 %#v", thrown)
	}
	if failure.PackageName != PackageName {
		t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
	}
	return failure
}

// requireViolation 断言这次派发被告了一状，而且那句话点到了 want。
func (w *invariantWorld) requireViolation(t *testing.T, want string, options llm.GenerateOptions) {
	t.Helper()
	failure := w.dispatch(t, options)
	if failure == nil {
		t.Fatalf("该抓住一条违例，说的是 %q", want)
	}
	if !strings.Contains(failure.Message, want) {
		t.Fatalf("违例该点到 %q，实际 %q", want, failure.Message)
	}
}

// stepStartEvent 造一条步骤开始事件。
func stepStartEvent(t *testing.T, turn, step int) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventStepStart,
		Data: mustData(t, sessionlog.StepStartData{Turn: turn, Step: step}),
	}
}

// headerEvent 造一条请求头快照事件。
func headerEvent(t *testing.T, header sessionlog.EpochHeader) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessionlog.EventRequestHeader,
		Data: mustData(t, sessionlog.RequestHeaderData{
			Header: header,
			Reason: sessionlog.HeaderInitial,
		}),
	}
}

// loopSession 建一个「循环刚要发请求」的会话：一条用户消息、一份请求头、一次
// 步骤开始，并交出那份头和推导出来的消息。
func (w *invariantWorld) loopSession(
	t *testing.T,
	id sessionlog.SessionID,
	header sessionlog.EpochHeader,
) (*session.Session, []llm.Message) {
	t.Helper()
	live := liveSession(t, w.store, w.owner, id, session.CreateOptions{Seed: seedOf(
		foreignUserEvent(t, "你好"),
		headerEvent(t, header),
		stepStartEvent(t, 1, 1),
	)})
	messages, err := live.DeriveMessages()
	if err != nil {
		t.Fatalf("推导消息失败：%v", err)
	}
	return live, messages
}

// faithfulOptions 按一份头和一段推导出来的历史，装出这条检查该放行的那份请求。
//
// 字段的对应关系和 [ReactLoopAgent] 装请求时用的是同一份：模型、系统提示、
// 温度、上限、停止串、工具表都来自那份头。
func faithfulOptions(
	id sessionlog.SessionID,
	header sessionlog.EpochHeader,
	messages []llm.Message,
) llm.GenerateOptions {
	return llm.GenerateOptions{
		AgentLoop:   true,
		SessionID:   llm.SessionID(id),
		Provider:    header.Config.Provider,
		Model:       header.Config.Model,
		System:      header.System,
		Temperature: header.Config.Temperature,
		MaxTokens:   header.Config.MaxTokens,
		Stop:        header.Config.Stop,
		Tools:       header.Tools,
		Messages:    messages,
	}
}

// plainHeader 是这一组用例的基准请求头。
func plainHeader() sessionlog.EpochHeader {
	return sessionlog.EpochHeader{
		Config: llm.CallConfig{Provider: "甲", Model: "m-1"},
		System: "你是一个助手",
	}
}

// TestPackageNameIsTheDSHNameVerbatim 钉住这个包在注册表里占的名字一个字符都没改。
//
// 源: packages/core/agent-loop/src/invariant.ts:11
//
// 注册表按名字预留名额，而这条约定的拥有者在两边是同一个模块。换个名字，两边的
// 诊断日志就对不上了——一条 Go 侧抛出来的违例，在 DSH 的记录里查无此包。
func TestPackageNameIsTheDSHNameVerbatim(t *testing.T) {
	t.Parallel()

	if PackageName != "@deepseek-ai/dsh-agent-loop" {
		t.Errorf("包名变了：%q", PackageName)
	}
}

// TestRegisterInvariantsNeedsEveryCollaborator 钉住四件必需品缺一不可。
//
// 少了任何一件，这条检查要么装不上、要么装上了也查不了——而「装上了但从不报」
// 比不装更坏：门禁是绿的，可它什么都没守。
func TestRegisterInvariantsNeedsEveryCollaborator(t *testing.T) {
	t.Parallel()

	owner := rootScope(t)
	runtime := llm.NewRuntime(llm.RuntimeOptions{})
	store := newStore(t)
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)

	ctx := context.Background()
	if _, err := RegisterInvariants(ctx, nil, owner, runtime, store); err == nil {
		t.Error("没有注册表该被拒")
	}
	if _, err := RegisterInvariants(ctx, registry, nil, runtime, store); err == nil {
		t.Error("没有作用域该被拒")
	}
	if _, err := RegisterInvariants(ctx, registry, owner, nil, store); err == nil {
		t.Error("没有 llm 运行时该被拒")
	}
	if _, err := RegisterInvariants(ctx, registry, owner, runtime, nil); err == nil {
		t.Error("没有会话存储该被拒")
	}
}

// TestRegisterInvariantsRefusesASecondInstallOnTheSameRegistry 钉住同一条注册表上
// 装第二次会失败——名额是按包名预留的。
func TestRegisterInvariantsRefusesASecondInstallOnTheSameRegistry(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)

	dispose, err := RegisterInvariants(
		context.Background(), registry, world.owner, world.runtime, world.store)
	if err != nil {
		t.Fatalf("第一次该装得上：%v", err)
	}
	t.Cleanup(dispose)
	if _, err := RegisterInvariants(
		context.Background(), registry, world.owner, world.runtime, world.store); err == nil {
		t.Error("同一条注册表上装第二次该失败")
	}
}

// TestInvariantIgnoresRequestsTheLoopDidNotBuild 钉住非循环请求原样放行。
//
// 源: packages/core/agent-loop/src/invariant.ts:20
//
// 手搓的一次性调用、标题生成、压缩都不是从某段日志推导出来的，拿这条检查去量
// 它们，等于禁掉本仓库里除了循环之外的每一次模型调用。
func TestInvariantIgnoresRequestsTheLoopDidNotBuild(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	// 这份请求既没有会话身份，消息也是凭空写的——循环装的话每一条都是违例。
	failure := world.dispatch(t, llm.GenerateOptions{
		Provider: "甲",
		Model:    "m-1",
		Messages: []llm.Message{llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "手搓的"}}, llm.UserSource{})},
	})
	if failure != nil {
		t.Fatalf("非循环请求不该被查：%v", failure)
	}
}

// TestLoopRequestMustCarryASessionID 钉住循环请求必须带会话身份。
//
// 源: packages/core/agent-loop/src/invariant.ts:26-28
//
// 没有身份就没有任何一段日志能说明这次请求为什么长成这样，这条检查往下每一步
// 都无从谈起。
func TestLoopRequestMustCarryASessionID(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	world.requireViolation(t, "must carry a session id",
		llm.GenerateOptions{AgentLoop: true, Provider: "甲", Model: "m-1"})
}

// TestLoopRequestMustCarryALiveSessionID 钉住那个身份必须指向一个活着的会话。
//
// 源: packages/core/agent-loop/src/invariant.ts:29-30
//
// 指向一个不在存储里的会话，意味着这次请求的历史无从核对——而那正是本条检查
// 唯一要做的事。放过它等于把检查关掉。
func TestLoopRequestMustCarryALiveSessionID(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	world.requireViolation(t, "must carry a live session id",
		llm.GenerateOptions{AgentLoop: true, SessionID: "查无此会话", Provider: "甲", Model: "m-1"})
}

// TestLoopRequestNeedsAStepStartInItsLog 钉住日志里必须有过一条 step/start。
//
// 源: packages/core/agent-loop/src/invariant.ts:31-33
//
// 没有它，这次请求不属于任何一个步骤——也就没有任何一段日志说得出它为什么被
// 发出去。重放的一方会读到一次凭空出现的模型往返。
func TestLoopRequestNeedsAStepStartInItsLog(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	live := liveSession(t, world.store, world.owner, "无步骤", session.CreateOptions{Seed: seedOf(
		foreignUserEvent(t, "你好"),
		headerEvent(t, header),
	)})
	messages, err := live.DeriveMessages()
	if err != nil {
		t.Fatalf("推导消息失败：%v", err)
	}
	world.requireViolation(t, "no step/start", faithfulOptions(live.ID(), header, messages))
}

// TestLoopRequestNeedsARequestHeaderInItsLog 钉住日志里必须折得出一份请求头。
//
// 源: packages/core/agent-loop/src/invariant.ts:35-38
//
// 那份头是模型、系统提示、工具表的耐久记录。它不在的话，这次请求用的是哪个
// 模型、看的是哪份工具表，日志里一个字都没有——重放拼不出同一次调用。
func TestLoopRequestNeedsARequestHeaderInItsLog(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	live := liveSession(t, world.store, world.owner, "无头", session.CreateOptions{Seed: seedOf(
		foreignUserEvent(t, "你好"),
		stepStartEvent(t, 1, 1),
	)})
	messages, err := live.DeriveMessages()
	if err != nil {
		t.Fatalf("推导消息失败：%v", err)
	}
	world.requireViolation(t, "no request/header event",
		faithfulOptions(live.ID(), plainHeader(), messages))
}

// TestLoopRequestMustMatchTheDurableDerivation 钉住请求带的消息和日志推导出来的
// 那一份逐字节一致。
//
// 源: packages/core/agent-loop/src/invariant.ts:40-44
//
// 这是本条不变量的正题：会话日志是唯一的真相，请求不过是它的一次投影。两者
// 一旦分叉，存下来的历史和模型真正看到的历史就是两回事——而且事后从任何一侧
// 都查不出来，因为两边各自都是自洽的。
func TestLoopRequestMustMatchTheDurableDerivation(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	live, messages := world.loopSession(t, "分叉", header)

	options := faithfulOptions(live.ID(), header, messages)
	options.Messages = append(append([]llm.Message(nil), messages...),
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "日志里没有这一句"}}, llm.UserSource{}))

	world.requireViolation(t, "log-reconstruction desync", options)
}

// TestLoopRequestMustMatchTheFoldedHeader 钉住请求那几个头字段和折出来的那份头
// 对得上，六项各走一遍。
//
// 源: packages/core/agent-loop/src/invariant.ts:45-52
//
// 消息一致但头不一致的请求，是最难查的那一种：日志读起来完全正常，可模型其实
// 是在另一个模型、另一份系统提示、或者另一张工具表下作答的。
func TestLoopRequestMustMatchTheFoldedHeader(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	other := 0.7
	header.Config.Temperature = &other
	header.Config.MaxTokens = 1024
	header.Config.Stop = []string{"停"}
	header.Tools = []llm.ToolSchema{{
		Name:        "读文件",
		Description: "读一个文件",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}
	live, messages := world.loopSession(t, "头对不上", header)

	changed := map[string]func(*llm.GenerateOptions){
		"模型":   func(o *llm.GenerateOptions) { o.Model = "m-2" },
		"系统提示": func(o *llm.GenerateOptions) { o.System = "换了一份" },
		"上限":   func(o *llm.GenerateOptions) { o.MaxTokens = 2048 },
		"温度":   func(o *llm.GenerateOptions) { o.Temperature = nil },
		"停止串":  func(o *llm.GenerateOptions) { o.Stop = []string{"另一个"} },
		"工具名":  func(o *llm.GenerateOptions) { o.Tools[0].Name = "写文件" },
		"工具表长度": func(o *llm.GenerateOptions) {
			o.Tools = append(o.Tools, llm.ToolSchema{Name: "多的"})
		},
		"工具 schema": func(o *llm.GenerateOptions) {
			o.Tools[0].Parameters = json.RawMessage(`{"type":"string"}`)
		},
	}
	for label, mutate := range changed {
		t.Run(label, func(t *testing.T) {
			options := faithfulOptions(live.ID(), header, messages)
			// 工具表是共享的底层数组，改之前先复制一份，免得一个子用例污染下一个。
			options.Tools = append([]llm.ToolSchema(nil), header.Tools...)
			mutate(&options)
			world.requireViolation(t, "diverges from the folded request header", options)
		})
	}
}

// TestInvariantAcceptsAFaithfulLoopRequest 钉住一份忠实的循环请求原样放行。
//
// 反面用例只证明这条检查抓得住偏离；这一条证明它不会把正常的请求也拦下来。
// 少了它，一条「永远报违例」的检查同样能让上面每一条用例通过。
func TestInvariantAcceptsAFaithfulLoopRequest(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	temperature := 0.2
	header.Config.Temperature = &temperature
	header.Config.MaxTokens = 512
	header.Config.Stop = []string{"停"}
	header.Tools = []llm.ToolSchema{{
		Name:        "读文件",
		Description: "读一个文件",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}
	live, messages := world.loopSession(t, "忠实", header)

	if failure := world.dispatch(t, faithfulOptions(live.ID(), header, messages)); failure != nil {
		t.Fatalf("忠实的请求不该被告：%v", failure)
	}
}

// TestInvariantToleratesAnEmptyStopListAgainstAnAbsentOne 钉住 [sameStop] 那道
// 刻意的放宽：请求手里的空清单和从日志折回来的 nil 算相等。
//
// 那份头是从会话事件的 JSON 字节里折回来的，而 [llm.CallConfig] 的 `stop,omitempty`
// 把「明确给了一个空清单」和「没给」排成同一段字节。在这里严格地比，等于对每一个
// 解析出空停止清单的适配器报一条**不是违例的违例**。
func TestInvariantToleratesAnEmptyStopListAgainstAnAbsentOne(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	live, messages := world.loopSession(t, "空停止串", header)

	options := faithfulOptions(live.ID(), header, messages)
	options.Stop = []string{}
	if failure := world.dispatch(t, options); failure != nil {
		t.Fatalf("空清单对上 nil 不该算违例：%v", failure)
	}
}

// TestInvariantTreatsNilAndEmptyToolListsAsTheSame 钉住 nil 工具表和空工具表相等。
//
// 源: packages/core/agent-loop/src/invariant.ts:51
//
// DSH 那一句自己就写着 `?? []`，两边都把「没给」摊平成空表。分开的话，一次
// 没有工具的请求会因为它带的是 `[]` 而头里是 nil 被告一状。
func TestInvariantTreatsNilAndEmptyToolListsAsTheSame(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	live, messages := world.loopSession(t, "空工具表", header)

	options := faithfulOptions(live.ID(), header, messages)
	options.Tools = []llm.ToolSchema{}
	if failure := world.dispatch(t, options); failure != nil {
		t.Fatalf("空工具表对上 nil 不该算违例：%v", failure)
	}
}

// TestInvariantTreatsNilAndEmptyMessageListsAsTheSame 钉住 [normalizeMessages]
// 那道摊平：nil 和空表排成 JSON 之后不能一个是 `null` 一个是 `[]`。
//
// 一段刚开张、什么都还没有的日志推导出来的正是 nil，而循环装请求时那个字段
// 可能是 `[]`。不摊平的话，这一步会当场报一条不存在的偏离。
func TestInvariantTreatsNilAndEmptyMessageListsAsTheSame(t *testing.T) {
	t.Parallel()

	world := newInvariantWorld(t)
	header := plainHeader()
	live := liveSession(t, world.store, world.owner, "空历史", session.CreateOptions{Seed: seedOf(
		headerEvent(t, header),
		stepStartEvent(t, 1, 1),
	)})
	messages, err := live.DeriveMessages()
	if err != nil {
		t.Fatalf("推导消息失败：%v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("这条用例要求推导出来的历史是空的，实际 %d 条", len(messages))
	}

	options := faithfulOptions(live.ID(), header, messages)
	options.Messages = []llm.Message{}
	if failure := world.dispatch(t, options); failure != nil {
		t.Fatalf("空消息表对上 nil 不该算违例：%v", failure)
	}
}

// TestInvariantStopsAfterDispose 钉住注销之后这条检查真的停下来了。
//
// 一条不该再查的检查继续在别人的流上抛，比不查更坏：它会在一条完全正常的调用
// 路径上炸掉，而现场看起来像是那条路径出了问题。
func TestInvariantStopsAfterDispose(t *testing.T) {
	t.Parallel()

	world := &invariantWorld{
		runtime: llm.NewRuntime(llm.RuntimeOptions{}),
		store:   newStore(t),
		owner:   rootScope(t),
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	dispose, err := RegisterInvariants(
		context.Background(), registry, world.owner, world.runtime, world.store)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}

	bad := llm.GenerateOptions{AgentLoop: true, Provider: "甲", Model: "m-1"}
	world.requireViolation(t, "must carry a session id", bad)

	dispose()
	if failure := world.dispatch(t, bad); failure != nil {
		t.Fatalf("注销之后不该再查：%v", failure)
	}
}

// TestSameTemperatureComparesValuesNotAddresses 钉住两个可选温度按**值**比。
//
// 新增: DSH 那一句是 `===`，两个 undefined 相等、两个 0.7 也相等。Go 里这是两个
// *float64，直接比会变成比地址——两个各自指向 0.7 的指针判成不等，于是每一次
// 带温度的请求都被告一状。这条用例钉的就是那个陷阱没有被踩回去。
func TestSameTemperatureComparesValuesNotAddresses(t *testing.T) {
	t.Parallel()

	left, right := 0.7, 0.7
	other := 0.2
	if !sameTemperature(nil, nil) {
		t.Error("都没给该算相等")
	}
	if sameTemperature(&left, nil) || sameTemperature(nil, &left) {
		t.Error("一边给了一边没给该算不等")
	}
	if !sameTemperature(&left, &right) {
		t.Error("两个各自指向同一个值的指针该算相等")
	}
	if sameTemperature(&left, &other) {
		t.Error("值不同该算不等")
	}
}

// TestSameStopComparesElementsInOrder 钉住停止串按元素、按次序比。
//
// 停止串的次序对提供方是有意义的，而且这条比对是逐元素的：只比长度的话，
// 两份完全不同的停止串会被判成相同。
func TestSameStopComparesElementsInOrder(t *testing.T) {
	t.Parallel()

	if !sameStop(nil, []string{}) || !sameStop([]string{}, nil) {
		t.Error("nil 和空清单在这里是刻意相等的")
	}
	if !sameStop([]string{"甲", "乙"}, []string{"甲", "乙"}) {
		t.Error("同样的两串该算相等")
	}
	if sameStop([]string{"甲", "乙"}, []string{"乙", "甲"}) {
		t.Error("次序不同该算不等")
	}
	if sameStop([]string{"甲"}, []string{"甲", "乙"}) {
		t.Error("长度不同该算不等")
	}
}

// TestSameToolsComparesSchemaBytes 钉住工具的参数 schema 是**逐字节**比的。
//
// 源: packages/core/agent-loop/src/invariant.ts:51
//
// 键的顺序是这份 schema 的一部分（见 [llm.ToolSchema] 的字段注释）：解出来比
// 会把两份键序不同的 schema 判成相同，而模型看到的正是那串字节本身。
func TestSameToolsComparesSchemaBytes(t *testing.T) {
	t.Parallel()

	base := []llm.ToolSchema{{
		Name:        "读文件",
		Description: "读一个文件",
		Parameters:  json.RawMessage(`{"type":"object","required":["path"]}`),
	}}
	same, err := sameTools(base, append([]llm.ToolSchema(nil), base...))
	if err != nil || !same {
		t.Errorf("同一份工具表该算相等：same=%v err=%v", same, err)
	}

	// 语义上等价、但键序不同的一份 schema：这里刻意判成不等。
	reordered := []llm.ToolSchema{{
		Name:        "读文件",
		Description: "读一个文件",
		Parameters:  json.RawMessage(`{"required":["path"],"type":"object"}`),
	}}
	if same, err := sameTools(base, reordered); err != nil || same {
		t.Errorf("键序不同该算不等：same=%v err=%v", same, err)
	}

	descriptionChanged := []llm.ToolSchema{{
		Name: "读文件", Description: "换了说明", Parameters: base[0].Parameters,
	}}
	if same, err := sameTools(base, descriptionChanged); err != nil || same {
		t.Errorf("说明不同该算不等：same=%v err=%v", same, err)
	}
	if same, err := sameTools(nil, []llm.ToolSchema{}); err != nil || !same {
		t.Errorf("nil 和空表该算相等：same=%v err=%v", same, err)
	}
}

// TestHasStepStartScansTheWholeLog 钉住 step/start 是在整段日志里找的，不是只看
// 最后一条。
//
// 一次已经收尾的步骤后面还会跟着 tool/result 之类的事件，只看末尾的话，一个
// 完全正常的会话会被判成「没有步骤」。
func TestHasStepStartScansTheWholeLog(t *testing.T) {
	t.Parallel()

	if hasStepStart(nil) {
		t.Error("空日志里没有 step/start")
	}
	if hasStepStart([]sessionlog.Event{{Type: sessionlog.EventUserMessage}}) {
		t.Error("只有用户消息时没有 step/start")
	}
	if !hasStepStart([]sessionlog.Event{
		{Type: sessionlog.EventStepStart},
		{Type: sessionlog.EventUserMessage},
	}) {
		t.Error("日志里有 step/start 就该找得到，哪怕它不在末尾")
	}
}
