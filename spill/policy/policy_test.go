// 本文件的作用：把这一层的全部可观察行为钉住——什么时候外置、换上去的那段文字长什么样、
// 以及那几条「宁可不换」的退路各自怎么触发。
//
// 逐条对着 DSH 的 tests/spill-policy.spec.ts 走，除了三组在 Go 这边**不存在**的：
// 那边给 Code Mode 的 tools/code-dispatch-log 瀑布写了七条（Code Mode 整个不移植）、
// 一条验 cordis 的 unwrapExports 导出形状、一条验 HMR 卸载之后不再变换结果
// （Go 这边就是 Install 交回来的那个撤销函数，本文件末尾有一条）。
//
// # 这些测试防的是什么错
//
//   - **换上去的东西比上限还大**。这一层唯一的价值就是省上下文；替换文字超出
//     maxInlineBytes，对一份刚刚超标的结果甚至可能比原文更长，那就是纯亏。
//   - **一次外置失败变成一次工具失败**。存不下去只是省不了上下文，不是这次调用出了错。
//     把它变成 IsError，模型就会去重试一件本来已经成功的事。
//   - **把别人的裁决吃掉**。下游换的值、下游挂的上下文、下游的拦截，一个都不能丢。
//   - **给一份没有归属的产物找个地方存**。存了也没人认领，日后既清不掉也查不到。
package policy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/spill"
	"github.com/snight1983/ds-harness-go/spill/policy"
)

// ownerSession 是测试里那个 agent 所属的会话。
const ownerSession = session.SessionID("s1")

// retrievalHint 是假后端给出的取回说明，短到不会喧宾夺主。
const retrievalHint = "Use the stub retrieval path."

// stubStore 是一个把每次请求都记下来的假后端。
type stubStore struct {
	saves []spill.SaveText
	fail  bool
}

// SaveText 记下这次请求，交回一个由建议名派生的句柄。
func (s *stubStore) SaveText(_ context.Context, input spill.SaveText) (spill.Ref, error) {
	if s.fail {
		return spill.Ref{}, errors.New("盘满了")
	}
	s.saves = append(s.saves, input)
	return spill.Ref{
		Locator:       spill.Locator("/spill/" + input.SuggestedName),
		Bytes:         len(input.Content),
		RetrievalHint: retrievalHint,
	}, nil
}

// harness 是一次测试要的全套家当。
type harness struct {
	runtime *tools.Runtime
	store   *stubStore
	agent   *scope.Key
	// logs 收下这一层记的那几条退让日志，测试靠它证明「不换」是走了退路而不是没触发。
	logs *strings.Builder
}

// newHarness 造一个装好这一层的运行时。owned 为 false 表示这个 agent 没有会话归属。
func newHarness(t *testing.T, maxInlineBytes int, owned bool) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	logs := &strings.Builder{}
	store := &stubStore{}
	installed, err := policy.New(policy.Config{
		MaxInlineBytes: maxInlineBytes,
		Store:          store,
		OwnerOf: func(*scope.Key) (session.SessionID, bool) {
			return ownerSession, owned
		},
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	// 先装的在外层：本层必须先调 next 让下游把结果定下来，再约束它定下来的那份。
	if _, err := installed.Install(context.Background(), runtime, scope.NewRoot()); err != nil {
		t.Fatalf("装这一层失败：%v", err)
	}
	return &harness{runtime: runtime, store: store, agent: scope.NewKey("agent"), logs: logs}
}

// textTool 造一件交出固定文本的工具。
//
// Render 照着**值**渲染而不是照着闭包里那个 body：换值的裁决会让注册表重新渲染一遍，
// 忽略值的渲染函数会让那条路径看起来什么都没发生。
func textTool(name, body string) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{Text: text}}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.Marshal(body)
		},
	}
}

// register 注册一份定义。
func (h *harness) register(t *testing.T, definition *tools.Definition) {
	t.Helper()
	if _, err := h.runtime.Register(context.Background(), scope.NewRoot(), definition); err != nil {
		t.Fatalf("注册 %q 失败：%v", definition.Name, err)
	}
}

// run 跑一次由 agent 发起的调用。
func (h *harness) run(name string) tools.Result {
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      name,
		Arguments: json.RawMessage(`{}`),
		Agent:     h.agent,
	})
}

// post 在本层之后再挂一条执行后规则，用来演下游。
func (h *harness) post(t *testing.T, rule tools.PostRule) {
	t.Helper()
	if _, err := h.runtime.PostExecute(context.Background(), scope.NewRoot(), rule); err != nil {
		t.Fatalf("装下游规则失败：%v", err)
	}
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

// warned 说明这一层记过退让日志。
func (h *harness) warned() bool { return strings.Contains(h.logs.String(), "外置放弃") }

func TestSpillsTheFullTextAndReplacesWithinTheCap(t *testing.T) {
	t.Parallel()
	const cap = 200
	h := newHarness(t, cap, true)
	body := strings.Repeat("HEAD", 200) + strings.Repeat("TAIL", 200)
	h.register(t, textTool("big", body))

	result := h.run("big")
	if result.IsError {
		t.Fatalf("外置不该让这次调用失败：%+v", result.Error)
	}
	if len(h.store.saves) != 1 {
		t.Fatalf("该存下恰好一份，拿到 %d 份", len(h.store.saves))
	}
	saved := h.store.saves[0]
	if saved.Content != body {
		t.Fatal("存下去的必须是**全文**，一个字都不能少")
	}
	if saved.Owner.SessionID != ownerSession || saved.Source.ToolName != "big" || saved.SuggestedName != "big.txt" {
		t.Fatalf("产物的归属或来源不对：%+v", saved)
	}

	replaced := textOf(result.Content)
	if !strings.HasPrefix(replaced, "HEAD") {
		t.Fatalf("预览该从原文开头开始：%q", replaced)
	}
	for _, want := range []string{"Omitted", "Full formatted result stored at: /spill/big.txt", retrievalHint} {
		if !strings.Contains(replaced, want) {
			t.Fatalf("替换文字里少了 %q：\n%s", want, replaced)
		}
	}
	// 这一层唯一的价值就在这两行上：不超上限，且比原文短。
	if len(replaced) > cap {
		t.Fatalf("替换文字 %d 字节，超过了上限 %d", len(replaced), cap)
	}
	if len(replaced) >= len(body) {
		t.Fatalf("替换文字 %d 字节，没有比原文的 %d 字节短", len(replaced), len(body))
	}
}

func TestLeavesASmallResultAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1000, true)
	h.register(t, textTool("small", "tiny"))

	if got := textOf(h.run("small").Content); got != "tiny" {
		t.Fatalf("没超标的结果该原样交出去，拿到：%q", got)
	}
	if len(h.store.saves) != 0 {
		t.Fatal("没超标就不该存任何东西")
	}
}

func TestLeavesAResultWithANonTextBlockAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 5, true)
	h.register(t, &tools.Definition{
		Name:        "mixed",
		Description: "mixed",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{
					llm.TextBlock{Text: strings.Repeat("x", 100)},
					llm.ReasoningBlock{Text: "why"},
				}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.Marshal("mixed")
		},
	})

	result := h.run("mixed")
	if len(result.Content) != 2 {
		t.Fatalf("带非文本块的结果该原样留着，拿到 %d 块", len(result.Content))
	}
	if len(h.store.saves) != 0 {
		t.Fatal("拍不平的内容不该被外置")
	}
}

func TestNeverSpillsTheReadTool(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, true)
	body := strings.Repeat("x", 1000)
	h.register(t, textTool("read", body))

	if got := textOf(h.run("read").Content); got != body {
		t.Fatal("read 的结果不该被外置，否则 read → 外置 → 再 read 会绕成一个环")
	}
	if len(h.store.saves) != 0 {
		t.Fatal("read 不该存任何东西")
	}
}

func TestKeepsTheInlineResultWhenTheNoticeAloneExceedsTheCap(t *testing.T) {
	t.Parallel()
	// 上限比那句说明本身还小：没有任何一份在上限之内的替换，所以只能不换。
	h := newHarness(t, 8, true)
	body := strings.Repeat("x", 5000)
	h.register(t, textTool("big", body))

	if got := textOf(h.run("big").Content); got != body {
		t.Fatalf("塞不下说明就该原样保留内联内容，拿到：%q", got)
	}
	if !h.warned() {
		t.Fatal("这条退路该留下一条日志")
	}
}

func TestKeepsTheInlineResultWhenSavingFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, true)
	h.store.fail = true
	body := strings.Repeat("x", 1000)
	h.register(t, textTool("big", body))

	result := h.run("big")
	if result.IsError {
		t.Fatal("一次外置失败绝不许把一次成功的调用变成失败")
	}
	if got := textOf(result.Content); got != body {
		t.Fatalf("存不下去就该原样保留内联内容，拿到 %d 字节", len(got))
	}
	if !h.warned() {
		t.Fatal("这条退路该留下一条日志")
	}
}

func TestKeepsTheInlineResultWithoutASessionOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, false)
	body := strings.Repeat("x", 1000)
	h.register(t, textTool("big", body))

	if got := textOf(h.run("big").Content); got != body {
		t.Fatalf("没有会话归属就该原样保留内联内容，拿到 %d 字节", len(got))
	}
	if len(h.store.saves) != 0 {
		t.Fatal("一份没人认领的产物不该被存下来")
	}
	if !h.warned() {
		t.Fatal("这条退路该留下一条日志")
	}
}

func TestBoundsContentADownstreamRuleReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 200, true)
	// 下游把一份很小的结果换成一份很大的：本层是先调的 next，所以约束的是换上去的那份。
	replacement := strings.Repeat("z", 500)
	h.post(t, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return tools.PostDecision{Kind: tools.PostAccept, Content: llm.Content{llm.TextBlock{Text: replacement}}}, nil
	})
	h.register(t, textTool("small", "tiny"))

	result := h.run("small")
	if len(h.store.saves) != 1 || h.store.saves[0].Content != replacement {
		t.Fatalf("存下去的该是下游换上来的那份：%+v", h.store.saves)
	}
	if !strings.Contains(textOf(result.Content), "Full formatted result stored at") {
		t.Fatalf("换上来的那份该被封顶：%q", textOf(result.Content))
	}
}

func TestPreservesDownstreamContextsWhenSpilling(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 200, true)
	theirs := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "别人挂的"}}, llm.PluginSource{Plugin: "other"})
	h.post(t, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return tools.PostDecision{Kind: tools.PostAccept, AdditionalContexts: []llm.Message{theirs}}, nil
	})
	h.register(t, textTool("big", strings.Repeat("x", 1000)))

	result := h.run("big")
	if !strings.Contains(textOf(result.Content), "Full formatted result stored at") {
		t.Fatal("这次该外置")
	}
	if len(result.AdditionalContexts) != 1 || textOf(result.AdditionalContexts[0].Content) != "别人挂的" {
		t.Fatalf("下游挂的上下文该原样留着，拿到：%+v", result.AdditionalContexts)
	}
}

func TestPassesADownstreamValueReplacementThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, true)
	replacement := strings.Repeat("z", 500)
	encoded, err := json.Marshal(replacement)
	if err != nil {
		t.Fatalf("造替换值失败：%v", err)
	}
	h.post(t, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return tools.PostDecision{Kind: tools.PostAccept, Value: encoded}, nil
	})
	h.register(t, textTool("small", "tiny"))

	result := h.run("small")
	if result.IsError {
		t.Fatalf("这次调用不该失败：%+v", result.Error)
	}
	// 换值要回注册表重新验、重新渲染，所以内容是渲染出来的那 500 个 z，而不是外置的替换。
	if got := textOf(result.Content); got != replacement {
		t.Fatalf("换值的裁决该原样穿过去，拿到 %d 字节", len(got))
	}
	if len(h.store.saves) != 0 {
		t.Fatal("换值和换内容在同一次裁决里互斥，这次不该外置")
	}
}

func TestPassesADownstreamBlockThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, true)
	feedback := strings.Repeat("拦", 1000)
	h.post(t, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return tools.PostDecision{Kind: tools.PostBlock, Feedback: llm.Content{llm.TextBlock{Text: feedback}}}, nil
	})
	h.register(t, textTool("big", strings.Repeat("x", 1000)))

	result := h.run("big")
	if !result.IsError {
		t.Fatal("被拦下的调用该是失败的")
	}
	if got := textOf(result.Content); got != feedback {
		t.Fatal("纠正性反馈不是工具结果，不该被外置")
	}
	if len(h.store.saves) != 0 {
		t.Fatal("被拦下的调用不该存任何东西")
	}
}

func TestDownstreamErrorPassesThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 10, true)
	boom := errors.New("下游炸了")
	h.post(t, func(tools.Execution, tools.Result, func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		return tools.PostDecision{}, boom
	})
	h.register(t, textTool("big", strings.Repeat("x", 1000)))

	if !h.run("big").IsError {
		t.Fatal("下游抛错该让这次调用失败")
	}
	if len(h.store.saves) != 0 {
		t.Fatal("没有裁决可换的时候不该存任何东西")
	}
}

func TestNestedSubDispatchesAreLeftToTheirOuterCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 200, true)
	body := strings.Repeat("x", 1000)
	h.register(t, textTool("inner", body))
	// 外层工具把子结果原样交出去：子派发那次不该被外置，整段由外层这次统一外置。
	var innerText string
	h.register(t, &tools.Definition{
		Name:        "outer",
		Description: "outer",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{Text: text}}, nil
			},
		},
		Execute: func(ctx context.Context, _ json.RawMessage, run *tools.RunContext) (json.RawMessage, error) {
			inner := h.runtime.Execute(ctx, tools.ExecutionInput{
				CallID:    llm.CallID("sub"),
				Name:      "inner",
				Arguments: json.RawMessage(`{}`),
				Agent:     run.Agent,
				Parent:    run.Token,
			})
			innerText = textOf(inner.Content)
			return json.Marshal(innerText)
		},
	})

	result := h.run("outer")
	if innerText != body {
		t.Fatalf("子派发的结果该完完整整地交给外层，拿到 %d 字节", len(innerText))
	}
	if len(h.store.saves) != 1 || h.store.saves[0].Source.ToolName != "outer" {
		t.Fatalf("该只有外层那次存了一份：%+v", h.store.saves)
	}
	if !strings.Contains(textOf(result.Content), "Full formatted result stored at") {
		t.Fatal("外层那次该被外置")
	}
}

func TestUndoRemovesTheRule(t *testing.T) {
	t.Parallel()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	installed, err := policy.New(policy.Config{
		MaxInlineBytes: 200,
		Store:          &stubStore{},
		OwnerOf:        func(*scope.Key) (session.SessionID, bool) { return ownerSession, true },
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	undo, err := installed.Install(context.Background(), runtime, scope.NewRoot())
	if err != nil {
		t.Fatalf("装这一层失败：%v", err)
	}
	body := strings.Repeat("x", 1000)
	if _, err := runtime.Register(context.Background(), scope.NewRoot(), textTool("big", body)); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	call := tools.ExecutionInput{CallID: llm.CallID("c"), Name: "big", Arguments: json.RawMessage(`{}`), Agent: scope.NewKey("agent")}

	if got := textOf(runtime.Execute(context.Background(), call).Content); got == body {
		t.Fatal("装上之后该被外置")
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if got := textOf(runtime.Execute(context.Background(), call).Content); got != body {
		t.Fatal("撤销之后就不该再动结果了")
	}
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	installed, err := policy.New(policy.Config{
		MaxInlineBytes: 10,
		Store:          &stubStore{},
		OwnerOf:        func(*scope.Key) (session.SessionID, bool) { return ownerSession, true },
	})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := installed.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有注册表就装不上，该报错")
	}
}

func TestConfigValidationFailsLoud(t *testing.T) {
	t.Parallel()
	ok := func(*scope.Key) (session.SessionID, bool) { return ownerSession, true }
	cases := map[string]policy.Config{
		"上限是负数":  {MaxInlineBytes: -1, Store: &stubStore{}, OwnerOf: ok},
		"没给存储后端": {MaxInlineBytes: 10, OwnerOf: ok},
		"没给归属映射": {MaxInlineBytes: 10, Store: &stubStore{}},
	}
	for label, config := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.New(config); !errors.Is(err, policy.ErrInvalidConfig) {
				t.Fatalf("这份配置该被拒绝并认得出哨兵：%v", err)
			}
		})
	}
}

func TestZeroCapSpillsEverything(t *testing.T) {
	t.Parallel()
	// 0 是合法的上限，表示凡是有内容的结果都外置。它也顺带钉住一件事：
	// 上限 0 之下没有任何在上限之内的替换，所以最终仍然保留内联内容。
	h := newHarness(t, 0, true)
	h.register(t, textTool("big", "x"))

	if got := textOf(h.run("big").Content); got != "x" {
		t.Fatalf("上限 0 时没有可用的替换，该保留内联内容，拿到：%q", got)
	}
	if len(h.store.saves) != 1 {
		t.Fatalf("上限 0 意味着这份结果超标了，该走到存这一步：%+v", h.store.saves)
	}
}
