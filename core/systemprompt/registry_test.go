// 本文件的作用：注册表那一侧的测试——分层与遮蔽、几条登记路径的校验、装配瀑布、
// 完整段落的恢复、以及工具次序在真装配里的表现。值那一侧的测试在 prompt_test.go。

package systemprompt

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
)

// harness 是一个造好的注册表加上它的根作用域和变更计数。
type harness struct {
	registry *Registry
	root     *scope.Scope
	changes  atomic.Int64
}

func newHarness(t *testing.T, options Options) *harness {
	t.Helper()
	h := &harness{root: scope.NewRoot()}
	options.OnChange = func() { h.changes.Add(1) }
	registry, err := NewRegistry(context.Background(), h.root, options)
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	h.registry = registry
	return h
}

// agentScope 造一个挂在 root 下面的有身份的作用域，用来测遮蔽。
func agentScope(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func sectionNames(assembly PromptAssembly) []string {
	names := make([]string, 0, len(assembly.Sections))
	for _, section := range assembly.Sections {
		names = append(names, section.Name)
	}
	return names
}

func sectionText(t *testing.T, assembly PromptAssembly, name string) string {
	t.Helper()
	for _, section := range assembly.Sections {
		if section.Name == name {
			return section.Text
		}
	}
	t.Fatalf("装配里没有段落 %q，只有 %v", name, sectionNames(assembly))
	return ""
}

func mustAssemble(t *testing.T, registry *Registry, assemble AssembleContext) PromptAssembly {
	t.Helper()
	assembly, err := registry.Assemble(context.Background(), assemble)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	return assembly
}

func TestNewRegistryRegistersTheHarnessIdentityAndTheConfiguredPersona(t *testing.T) {
	h := newHarness(t, Options{Persona: "你是审稿人。"})
	assembly := mustAssemble(t, h.registry, AssembleContext{})

	// 宿主身份排在人设前面，这是模型读到的次序。
	if got := sectionNames(assembly); !equalStrings(got, []string{harnessIdentitySection, PersonaSection}) {
		t.Fatalf("段落是 %v", got)
	}
	if sectionText(t, assembly, PersonaSection) != "你是审稿人。" {
		t.Fatal("人设正文不对")
	}

	rendered, err := RenderPrompt(assembly)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != harnessIdentityText+"\n\n你是审稿人。" {
		t.Fatalf("渲染结果是 %q", rendered)
	}
}

func TestAPersonaLessDeploymentRendersNoPersonaSection(t *testing.T) {
	// 人设那一段照样登记着（预设要能替换它），只是空正文在渲染时被丢掉。
	h := newHarness(t, Options{})
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if len(assembly.Sections) != 2 {
		t.Fatalf("段落该有两段：%v", sectionNames(assembly))
	}
	rendered, err := RenderPrompt(assembly)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != harnessIdentityText {
		t.Fatalf("渲染结果是 %q", rendered)
	}
}

func TestOmitHarnessIdentity(t *testing.T) {
	h := newHarness(t, Options{OmitHarnessIdentity: true, Persona: "只有我。"})
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if got := sectionNames(assembly); !equalStrings(got, []string{PersonaSection}) {
		t.Fatalf("段落是 %v", got)
	}
}

func TestNewRegistryRejectsBadInput(t *testing.T) {
	if _, err := NewRegistry(context.Background(), nil, Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有 owner 该被拒：%v", err)
	}
	_, err := NewRegistry(context.Background(), scope.NewRoot(), Options{ToolOrder: []string{"a"}})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("漏了 rest 标记的次序该被拒：%v", err)
	}
}

func TestNewRegistryPropagatesAFailedBuiltInRegistration(t *testing.T) {
	// owner 已经释放了，两段固定登记都放不进去——构造就该失败，而不是交出一个
	// 少了人设槽位的注册表。
	owner := scope.NewRoot()
	if err := owner.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(context.Background(), owner, Options{}); err == nil {
		t.Fatal("owner 已释放时构造该失败")
	}
	// 关掉宿主身份之后，失败的那一次就落在人设那一段上。
	if _, err := NewRegistry(context.Background(), owner, Options{OmitHarnessIdentity: true}); err == nil {
		t.Fatal("人设那一段放不进去时构造也该失败")
	}
	// 压制运行期上下文那一步同样得把失败报出来。
	if _, err := NewRegistry(context.Background(), owner, Options{SuppressRuntimeContext: true}); err == nil {
		t.Fatal("压制那一步放不进去时构造也该失败")
	}
}

func TestSuppressRuntimeContextFromOptions(t *testing.T) {
	evaluated := 0
	h := newHarness(t, Options{SuppressRuntimeContext: true})
	if _, err := h.registry.Context(context.Background(), h.root, PromptContext{
		Name: "workspace",
		Text: func(context.Context, AssembleContext) (string, error) {
			evaluated++
			return "不该出现", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if len(assembly.Contexts) != 0 {
		t.Fatalf("上下文该被整个压掉：%#v", assembly.Contexts)
	}
	if evaluated != 0 {
		t.Fatal("被压掉的上下文不该被求值")
	}
}

func TestSuppressRuntimeContextIsRestoredWhenDisposed(t *testing.T) {
	h := newHarness(t, Options{})
	if _, err := h.registry.Context(context.Background(), h.root, PromptContext{
		Name: "workspace", Text: StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}
	release, err := h.registry.SuppressRuntimeContext(context.Background(), h.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mustAssemble(t, h.registry, AssembleContext{}).Contexts) != 0 {
		t.Fatal("压制期间不该有上下文")
	}
	if err := release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mustAssemble(t, h.registry, AssembleContext{}).Contexts) != 1 {
		t.Fatal("撤销压制之后上下文该回来")
	}
}

func TestAScopedSuppressionOnlyReachesItsOwnScope(t *testing.T) {
	// 压制登记在哪一层，就只在那条链上生效——别的作用域照样看得见运行期上下文。
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := h.registry.Context(ctx, h.root, PromptContext{
		Name: "workspace", Text: StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.SuppressRuntimeContext(ctx, owner); err != nil {
		t.Fatal(err)
	}

	scoped := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if len(scoped.Contexts) != 0 {
		t.Fatalf("这个作用域里该被压掉：%#v", scoped.Contexts)
	}
	global := mustAssemble(t, h.registry, AssembleContext{})
	if len(global.Contexts) != 1 {
		t.Fatalf("别处不该受影响：%#v", global.Contexts)
	}
}

func TestAScopedVariableProviderFailureStopsTheAssembly(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	boom := errors.New("变量炸了")

	if _, err := h.registry.Variable(context.Background(), owner, "cwd",
		func(context.Context, AssembleContext) (*string, error) { return nil, boom },
	); err != nil {
		t.Fatal(err)
	}

	_, err := h.registry.Assemble(context.Background(), AssembleContext{Scope: owner.Key()})
	if !errors.Is(err, ErrInvalidAssembly) || !errors.Is(err, boom) {
		t.Fatalf("诊断是 %v", err)
	}
	// 同一个提供方在别的作用域里不参与，那次装配照样成。
	mustAssemble(t, h.registry, AssembleContext{})
}

func TestAScopedPersonaShadowsTheDeploymentPersona(t *testing.T) {
	h := newHarness(t, Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")

	release, err := h.registry.Section(context.Background(), owner, PromptSection{
		Name: PersonaSection, Order: PersonaOrder, Text: StaticText("这个 agent 的人设。"),
	})
	if err != nil {
		t.Fatal(err)
	}

	scoped := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if sectionText(t, scoped, PersonaSection) != "这个 agent 的人设。" {
		t.Fatal("作用域里的人设该盖住部署方那份")
	}
	// 遮蔽只改值不挪位置：人设仍然排在宿主身份后面。
	if got := sectionNames(scoped); !equalStrings(got, []string{harnessIdentitySection, PersonaSection}) {
		t.Fatalf("遮蔽把段落挪位置了：%v", got)
	}

	global := mustAssemble(t, h.registry, AssembleContext{})
	if sectionText(t, global, PersonaSection) != "部署方的人设。" {
		t.Fatal("别的作用域不该受影响")
	}

	if err := release(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if sectionText(t, restored, PersonaSection) != "部署方的人设。" {
		t.Fatal("撤销之后该退回部署方那份")
	}
}

func TestShadowingHappensBeforeEitherTextProviderRuns(t *testing.T) {
	// 被盖住的那一份连求值都不该发生：一个提供方可能有代价，也可能会失败。
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	globalRuns := 0

	if _, err := h.registry.Section(context.Background(), h.root, PromptSection{
		Name: "tools:hint", Order: 100,
		Text: func(context.Context, AssembleContext) (string, error) {
			globalRuns++
			return "全局的", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Section(context.Background(), owner, PromptSection{
		Name: "tools:hint", Order: 100, Text: StaticText("作用域的"),
	}); err != nil {
		t.Fatal(err)
	}

	assembly := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if sectionText(t, assembly, "tools:hint") != "作用域的" {
		t.Fatal("该用作用域那一份")
	}
	if globalRuns != 0 {
		t.Fatal("被盖住的提供方不该被求值")
	}
}

func TestDuplicateNamesFailPerLayerWithLayerSpecificDiagnostics(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")

	// 全局层的重名诊断要指路到「挂到那个 agent 的作用域上去」。
	_, err := h.registry.Section(context.Background(), h.root, PromptSection{
		Name: PersonaSection, Order: 0, Text: StaticText("又一份"),
	})
	if !errors.Is(err, ErrInvalidRegistration) || !strings.Contains(err.Error(), "per-agent override") {
		t.Fatalf("全局重名诊断是 %v", err)
	}

	if _, err := h.registry.Section(context.Background(), owner, PromptSection{
		Name: "x", Order: 1, Text: StaticText("一"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = h.registry.Section(context.Background(), owner, PromptSection{
		Name: "x", Order: 1, Text: StaticText("二"),
	})
	if !errors.Is(err, ErrInvalidRegistration) || !strings.Contains(err.Error(), "in this scope") {
		t.Fatalf("作用域重名诊断是 %v", err)
	}

	// 上下文和变量走同一套诊断。
	if _, err := h.registry.Context(context.Background(), h.root, PromptContext{
		Name: "c", Text: StaticText("一"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = h.registry.Context(context.Background(), h.root, PromptContext{
		Name: "c", Text: StaticText("二"),
	})
	if err == nil || !strings.Contains(err.Error(), `prompt context "c"`) {
		t.Fatalf("上下文重名诊断是 %v", err)
	}
	if _, err := h.registry.Variable(context.Background(), h.root, "v", StaticVariable("一")); err != nil {
		t.Fatal(err)
	}
	_, err = h.registry.Variable(context.Background(), h.root, "v", StaticVariable("二"))
	if err == nil || !strings.Contains(err.Error(), `prompt variable "v"`) {
		t.Fatalf("变量重名诊断是 %v", err)
	}
}

// StaticVariable 是测试里用来省事的固定值提供方。
func StaticVariable(value string) VariableProvider {
	return func(context.Context, AssembleContext) (*string, error) { return &value, nil }
}

func TestRegistrationRejectsMissingProviders(t *testing.T) {
	h := newHarness(t, Options{})
	ctx := context.Background()

	if _, err := h.registry.Section(ctx, h.root, PromptSection{Name: "s"}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("段落缺正文该被拒：%v", err)
	}
	if _, err := h.registry.Context(ctx, h.root, PromptContext{Name: "c"}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("上下文缺正文该被拒：%v", err)
	}
	if _, err := h.registry.Tools(ctx, h.root, nil); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("工具缺提供方该被拒：%v", err)
	}
	if _, err := h.registry.Variable(ctx, h.root, "v", nil); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("变量缺提供方该被拒：%v", err)
	}
	if _, err := h.registry.OnAssemble(ctx, h.root, nil); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("装配规则为 nil 该被拒：%v", err)
	}
}

func TestVariableNamesMustMatchTheReferenceGrammar(t *testing.T) {
	h := newHarness(t, Options{})
	for _, name := range []string{"", "Name", "1a", "a-b", "a b"} {
		_, err := h.registry.Variable(context.Background(), h.root, name, StaticVariable("x"))
		if !errors.Is(err, ErrInvalidRegistration) {
			t.Fatalf("%q 该被拒，得到 %v", name, err)
		}
	}
	if _, err := h.registry.Variable(context.Background(), h.root, "a_b9", StaticVariable("x")); err != nil {
		t.Fatalf("合法的名字被拒了：%v", err)
	}
}

func TestAScopedVariableShadowsItsGlobalNameTwin(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := h.registry.Variable(ctx, h.root, "cwd", StaticVariable("/global")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Variable(ctx, owner, "cwd", StaticVariable("/scoped")); err != nil {
		t.Fatal(err)
	}

	scoped := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if value := scoped.Variables["cwd"]; value == nil || *value != "/scoped" {
		t.Fatalf("作用域里的值该赢：%v", value)
	}
	global := mustAssemble(t, h.registry, AssembleContext{})
	if value := global.Variables["cwd"]; value == nil || *value != "/global" {
		t.Fatalf("全局那份不该受影响：%v", value)
	}
}

func TestAVariableProviderMayHaveNoValueForThisAssembly(t *testing.T) {
	h := newHarness(t, Options{})
	if _, err := h.registry.Variable(context.Background(), h.root, "maybe",
		func(context.Context, AssembleContext) (*string, error) { return nil, nil },
	); err != nil {
		t.Fatal(err)
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	value, registered := assembly.Variables["maybe"]
	if !registered {
		t.Fatal("这个变量注册过，名字该在表里")
	}
	if value != nil {
		t.Fatal("这次没值，该是 nil")
	}
}

func TestProvidersSeeTheAssembleContext(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := h.registry.Section(ctx, h.root, PromptSection{
		Name: "echo", Order: 50,
		Text: func(_ context.Context, assemble AssembleContext) (string, error) {
			if assemble.Scope == nil {
				return "无作用域", nil
			}
			return assemble.Scope.Label(), nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	if got := sectionText(t, mustAssemble(t, h.registry, AssembleContext{}), "echo"); got != "无作用域" {
		t.Fatalf("得到 %q", got)
	}
	scoped := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if got := sectionText(t, scoped, "echo"); got != "agent" {
		t.Fatalf("得到 %q", got)
	}
}

func TestProviderFailuresAreAttributedAndStopTheAssembly(t *testing.T) {
	boom := errors.New("炸了")
	cases := []struct {
		name string
		want string
		set  func(*harness) error
	}{
		{"段落", `段落 "bad"`, func(h *harness) error {
			_, err := h.registry.Section(context.Background(), h.root, PromptSection{
				Name: "bad", Order: 1,
				Text: func(context.Context, AssembleContext) (string, error) { return "", boom },
			})
			return err
		}},
		{"上下文", `上下文 "bad"`, func(h *harness) error {
			_, err := h.registry.Context(context.Background(), h.root, PromptContext{
				Name: "bad",
				Text: func(context.Context, AssembleContext) (string, error) { return "", boom },
			})
			return err
		}},
		{"变量", `变量 "bad"`, func(h *harness) error {
			_, err := h.registry.Variable(context.Background(), h.root, "bad",
				func(context.Context, AssembleContext) (*string, error) { return nil, boom },
			)
			return err
		}},
		{"工具", "工具提供方失败", func(h *harness) error {
			_, err := h.registry.Tools(context.Background(), h.root,
				func(context.Context, AssembleContext) (ToolProviderResult, error) {
					return ToolProviderResult{}, boom
				},
			)
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, Options{})
			if err := testCase.set(h); err != nil {
				t.Fatal(err)
			}
			_, err := h.registry.Assemble(context.Background(), AssembleContext{})
			if !errors.Is(err, ErrInvalidAssembly) || !errors.Is(err, boom) {
				t.Fatalf("错误该同时是装配失败和原因：%v", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("诊断该指出是谁失败的：%v", err)
			}
		})
	}
}

func TestSectionsAndContextsAreOrderedAscending(t *testing.T) {
	h := newHarness(t, Options{})
	ctx := context.Background()
	for _, entry := range []struct {
		name  string
		order int
	}{{"late", 200}, {"early", -500}, {"middle", 120}} {
		if _, err := h.registry.Section(ctx, h.root, PromptSection{
			Name: entry.name, Order: entry.order, Text: StaticText(entry.name),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.registry.Context(ctx, h.root, PromptContext{
			Name: entry.name, Order: entry.order, Text: StaticText(entry.name),
		}); err != nil {
			t.Fatal(err)
		}
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	want := []string{"early", harnessIdentitySection, PersonaSection, "middle", "late"}
	if got := sectionNames(assembly); !equalStrings(got, want) {
		t.Fatalf("段落次序是 %v", got)
	}
	var contexts []string
	for _, entry := range assembly.Contexts {
		contexts = append(contexts, entry.Name)
	}
	if !equalStrings(contexts, []string{"early", "middle", "late"}) {
		t.Fatalf("上下文次序是 %v", contexts)
	}
}

func TestScopedToolProvidersAreConsultedOnlyForTheirScope(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	other := agentScope(t, "other")
	ctx := context.Background()

	if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("bash")}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Tools(ctx, owner, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("agent_only")}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	mine := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if got := toolNames(mine.Tools); !equalStrings(got, []string{"agent_only", "bash"}) {
		t.Fatalf("这个作用域该看见两个：%v", got)
	}
	theirs := mustAssemble(t, h.registry, AssembleContext{Scope: other.Key()})
	if got := toolNames(theirs.Tools); !equalStrings(got, []string{"bash"}) {
		t.Fatalf("别的作用域只该看见全局那个：%v", got)
	}
}

func TestToolParametersAreCopiedSoARuleCannotReachTheProvidersOwnSchema(t *testing.T) {
	h := newHarness(t, Options{})
	original := schema("bash")
	if _, err := h.registry.Tools(context.Background(), h.root,
		func(context.Context, AssembleContext) (ToolProviderResult, error) {
			return ToolProviderResult{Schemas: []llm.ToolSchema{original}}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	assembly.Tools[0].Parameters[0] = 'X'
	if original.Parameters[0] == 'X' {
		t.Fatal("改到装配里的参数把提供方自己那份也改了")
	}
}

func TestKnownNamesLetARestrictedToolStayInTheConfiguredOrder(t *testing.T) {
	// 一个在这个作用域里被藏起来的工具，名字仍然是登记过的：配置里提到它不算写错。
	h := newHarness(t, Options{ToolOrder: []string{"hidden", ToolOrderRest}})
	if _, err := h.registry.Tools(context.Background(), h.root,
		func(context.Context, AssembleContext) (ToolProviderResult, error) {
			return ToolProviderResult{
				Schemas:    []llm.ToolSchema{schema("bash")},
				KnownNames: []string{"bash", "hidden"},
			}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if got := toolNames(assembly.Tools); !equalStrings(got, []string{"bash"}) {
		t.Fatalf("被藏起来的可以缺席：%v", got)
	}

	// 拼错的名字仍然要炸。
	typo := newHarness(t, Options{ToolOrder: []string{"hiddne", ToolOrderRest}})
	if _, err := typo.registry.Tools(context.Background(), typo.root,
		func(context.Context, AssembleContext) (ToolProviderResult, error) {
			return ToolProviderResult{
				Schemas:    []llm.ToolSchema{schema("bash")},
				KnownNames: []string{"bash", "hidden"},
			}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := typo.registry.Assemble(context.Background(), AssembleContext{}); !errors.Is(err, ErrInvalidAssembly) {
		t.Fatalf("写错的名字该炸：%v", err)
	}
}

func TestAssembleRulesComposeInOrderAndSeeTheContext(t *testing.T) {
	h := newHarness(t, Options{})
	var order []string
	ctx := context.Background()

	for _, name := range []string{"first", "second"} {
		if _, err := h.registry.OnAssemble(ctx, h.root, func(
			_ context.Context, assembly PromptAssembly, assemble AssembleContext,
			next func(PromptAssembly) (PromptAssembly, error),
		) (PromptAssembly, error) {
			order = append(order, name+":in")
			if assemble.Scope != nil {
				assembly.Variables[name] = &name
			}
			result, err := next(assembly)
			order = append(order, name+":out")
			return result, err
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := h.registry.Assemble(ctx, AssembleContext{}); err != nil {
		t.Fatal(err)
	}
	// 先登记的在外层。
	want := []string{"first:in", "second:in", "second:out", "first:out"}
	if !equalStrings(order, want) {
		t.Fatalf("次序是 %v", order)
	}
}

func TestAScopedAssembleRuleShapesOnlyItsOwnScope(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	other := agentScope(t, "other")

	if _, err := h.registry.OnAssemble(context.Background(), owner, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		assembly.Sections = append(assembly.Sections, AssembledSection{Name: "added", Text: "加的"})
		return next(assembly)
	}); err != nil {
		t.Fatal(err)
	}

	mine := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if !strings.Contains(strings.Join(sectionNames(mine), ","), "added") {
		t.Fatalf("自己的作用域该看见：%v", sectionNames(mine))
	}
	theirs := mustAssemble(t, h.registry, AssembleContext{Scope: other.Key()})
	if strings.Contains(strings.Join(sectionNames(theirs), ","), "added") {
		t.Fatalf("别的作用域不该看见：%v", sectionNames(theirs))
	}
}

func TestAnAssembleRuleCanShortCircuitByNotCallingNext(t *testing.T) {
	h := newHarness(t, Options{})
	inner := false
	ctx := context.Background()

	if _, err := h.registry.OnAssemble(ctx, h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		_ func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		assembly.Sections = []AssembledSection{{Name: "only", Text: "短路"}}
		return assembly, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.OnAssemble(ctx, h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		inner = true
		return next(assembly)
	}); err != nil {
		t.Fatal(err)
	}

	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if got := sectionNames(assembly); !equalStrings(got, []string{"only"}) {
		t.Fatalf("短路之后该只剩它自己给的：%v", got)
	}
	if inner {
		t.Fatal("不调 next 时后面的规则不该跑")
	}
}

func TestAnAssembleRuleFailureStopsTheAssembly(t *testing.T) {
	h := newHarness(t, Options{})
	boom := errors.New("规则炸了")
	if _, err := h.registry.OnAssemble(context.Background(), h.root, func(
		context.Context, PromptAssembly, AssembleContext,
		func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		return PromptAssembly{}, boom
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Assemble(context.Background(), AssembleContext{}); !errors.Is(err, boom) {
		t.Fatalf("规则的失败该原样报出来：%v", err)
	}
}

func TestACompleteSectionIsRestoredAfterTheWaterfall(t *testing.T) {
	h := newHarness(t, Options{Persona: "部署方的。"})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := h.registry.Section(ctx, owner, PromptSection{
		Name: PersonaSection, Order: PersonaOrder,
		Text: StaticText("我就是全部。"), Complete: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 一条规则想往里加东西——瀑布跑得到，但完整段落会被放回去。
	ruleSawTools := 0
	if _, err := h.registry.OnAssemble(ctx, h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		ruleSawTools = len(assembly.Tools)
		assembly.Sections = append(assembly.Sections, AssembledSection{Name: "sneak", Text: "偷加的"})
		return next(assembly)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("bash")}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	assembly := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if got := sectionNames(assembly); !equalStrings(got, []string{PersonaSection}) {
		t.Fatalf("完整段落该是唯一的一段：%v", got)
	}
	if assembly.Sections[0].Text != "我就是全部。" {
		t.Fatalf("正文是 %q", assembly.Sections[0].Text)
	}
	// 工具照样解出来了——协作瀑布没有被跳过。
	if ruleSawTools != 1 || len(assembly.Tools) != 1 {
		t.Fatalf("工具该照常解出来：规则看见 %d，装配里 %d", ruleSawTools, len(assembly.Tools))
	}
}

func TestMultipleEffectiveCompleteSectionsFailTheAssembly(t *testing.T) {
	h := newHarness(t, Options{})
	ctx := context.Background()
	for _, name := range []string{"one", "two"} {
		if _, err := h.registry.Section(ctx, h.root, PromptSection{
			Name: name, Order: 10, Text: StaticText(name), Complete: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := h.registry.Assemble(ctx, AssembleContext{})
	if !errors.Is(err, ErrInvalidAssembly) ||
		!strings.Contains(err.Error(), "multiple complete prompt sections") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestSuppressedContextsSurviveAWaterfallThatAddsThem(t *testing.T) {
	// 压制是最后说了算的：一条规则加回来的上下文照样被抹掉。
	h := newHarness(t, Options{SuppressRuntimeContext: true})
	if _, err := h.registry.OnAssemble(context.Background(), h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		assembly.Contexts = append(assembly.Contexts, AssembledContext{Name: "sneak", Text: "偷加的"})
		return next(assembly)
	}); err != nil {
		t.Fatal(err)
	}
	assembly := mustAssemble(t, h.registry, AssembleContext{})
	if len(assembly.Contexts) != 0 {
		t.Fatalf("压制该盖过规则加回来的：%#v", assembly.Contexts)
	}
}

func TestChangeNotificationsFireOnRegisterAndDispose(t *testing.T) {
	h := newHarness(t, Options{})
	before := h.changes.Load()

	release, err := h.registry.Tools(context.Background(), h.root,
		func(context.Context, AssembleContext) (ToolProviderResult, error) {
			return ToolProviderResult{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if h.changes.Load() != before+1 {
		t.Fatal("登记该发一次变更通知")
	}
	if err := release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.changes.Load() != before+2 {
		t.Fatal("撤销该再发一次")
	}
}

func TestAnAssembleRuleDoesNotNotify(t *testing.T) {
	// 一条规则不改变**登记了什么**，只改变装配出来的东西长什么样。
	h := newHarness(t, Options{})
	before := h.changes.Load()
	release, err := h.registry.OnAssemble(context.Background(), h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		return next(assembly)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.changes.Load() != before {
		t.Fatal("装配规则不该发变更通知")
	}
}

func TestARegistryWithoutAnOnChangeCallbackStillWorks(t *testing.T) {
	root := scope.NewRoot()
	registry, err := NewRegistry(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	release, err := registry.Section(context.Background(), root, PromptSection{
		Name: "x", Order: 1, Text: StaticText("一"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := release(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDisposingAScopeRemovesItsContributionsWithoutResidue(t *testing.T) {
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := h.registry.Section(ctx, owner, PromptSection{
		Name: "scoped", Order: 5, Text: StaticText("只在这里"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Tools(ctx, owner, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("scoped_tool")}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	before := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if len(before.Tools) != 1 {
		t.Fatal("释放之前该看得见")
	}
	if err := owner.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	after := mustAssemble(t, h.registry, AssembleContext{Scope: owner.Key()})
	if len(after.Tools) != 0 {
		t.Fatalf("释放之后不该有残留：%v", toolNames(after.Tools))
	}
	if strings.Contains(strings.Join(sectionNames(after), ","), "scoped") {
		t.Fatalf("段落也不该有残留：%v", sectionNames(after))
	}
	// 层被回收了：再问一次不会凭空冒出一层。
	if _, exists := h.registry.layers.Peek(owner.Key()); exists {
		t.Fatal("空掉的层该被回收")
	}
}

func TestAssembleRefusesAnAlreadyCancelledContext(t *testing.T) {
	h := newHarness(t, Options{})
	ctx, cancel := context.WithCancelCause(context.Background())
	reason := errors.New("这一轮不要了")
	cancel(reason)
	if _, err := h.registry.Assemble(ctx, AssembleContext{}); !errors.Is(err, reason) {
		t.Fatalf("该原样报出取消原因：%v", err)
	}
}

func TestToolOrderIsAppliedInARealAssemblyRegardlessOfProviderOrder(t *testing.T) {
	build := func(first, second string) []string {
		h := newHarness(t, Options{ToolOrder: []string{"write", ToolOrderRest, "bash"}})
		ctx := context.Background()
		for _, name := range []string{first, second} {
			if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
				return ToolProviderResult{Schemas: []llm.ToolSchema{schema(name)}}, nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
			return ToolProviderResult{Schemas: []llm.ToolSchema{schema("glob"), schema("read")}}, nil
		}); err != nil {
			t.Fatal(err)
		}
		return toolNames(mustAssemble(t, h.registry, AssembleContext{}).Tools)
	}
	want := []string{"write", "glob", "read", "bash"}
	if got := build("bash", "write"); !equalStrings(got, want) {
		t.Fatalf("次序是 %v", got)
	}
	if got := build("write", "bash"); !equalStrings(got, want) {
		t.Fatalf("登记顺序不该影响结果：%v", got)
	}
}

func TestCanonicalToolOrderIsAlreadyInPlaceWhenTheWaterfallRuns(t *testing.T) {
	h := newHarness(t, Options{})
	var seen []string
	ctx := context.Background()

	if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("write"), schema("bash")}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.OnAssemble(ctx, h.root, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		seen = toolNames(assembly.Tools)
		return next(assembly)
	}); err != nil {
		t.Fatal(err)
	}

	mustAssemble(t, h.registry, AssembleContext{})
	if !equalStrings(seen, []string{"bash", "write"}) {
		t.Fatalf("规则看见的该已经是最终次序：%v", seen)
	}
}

func TestToolProviderMembershipIsSnapshotBeforeEvaluation(t *testing.T) {
	// 一个提供方在被问的时候又登记了一个提供方，这一轮不该看见它——否则一次装配
	// 会不会终止就取决于提供方自己了。
	h := newHarness(t, Options{})
	ctx := context.Background()
	added := false

	if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		if !added {
			added = true
			if _, err := h.registry.Tools(ctx, h.root, func(context.Context, AssembleContext) (ToolProviderResult, error) {
				return ToolProviderResult{Schemas: []llm.ToolSchema{schema("late")}}, nil
			}); err != nil {
				return ToolProviderResult{}, err
			}
		}
		return ToolProviderResult{Schemas: []llm.ToolSchema{schema("early")}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	first := mustAssemble(t, h.registry, AssembleContext{})
	if got := toolNames(first.Tools); !equalStrings(got, []string{"early"}) {
		t.Fatalf("这一轮不该看见后加的：%v", got)
	}
	second := mustAssemble(t, h.registry, AssembleContext{})
	if got := toolNames(second.Tools); !equalStrings(got, []string{"early", "late"}) {
		t.Fatalf("下一轮该看见了：%v", got)
	}
}

func TestPromptLayerIsEmptyOnlyWhenEveryTableIs(t *testing.T) {
	// [scope.Layers] 靠这个判断回收空层，所以每张表都得算进去。
	h := newHarness(t, Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	releases := []func(context.Context) error{}
	add := func(release func(context.Context) error, err error) {
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	add(h.registry.Section(ctx, owner, PromptSection{Name: "s", Order: 1, Text: StaticText("s")}))
	add(h.registry.Context(ctx, owner, PromptContext{Name: "c", Text: StaticText("c")}))
	add(h.registry.SuppressRuntimeContext(ctx, owner))
	add(h.registry.Tools(ctx, owner, func(context.Context, AssembleContext) (ToolProviderResult, error) {
		return ToolProviderResult{}, nil
	}))
	add(h.registry.Variable(ctx, owner, "v", StaticVariable("v")))
	add(h.registry.OnAssemble(ctx, owner, func(
		_ context.Context, assembly PromptAssembly, _ AssembleContext,
		next func(PromptAssembly) (PromptAssembly, error),
	) (PromptAssembly, error) {
		return next(assembly)
	}))

	for index, release := range releases {
		layer, exists := h.registry.layers.Peek(owner.Key())
		if !exists {
			t.Fatalf("撤销第 %d 项之前这一层就没了", index)
		}
		if layer.IsEmpty() {
			t.Fatalf("撤销第 %d 项之前这一层不该算空", index)
		}
		if err := release(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, exists := h.registry.layers.Peek(owner.Key()); exists {
		t.Fatal("全撤完之后这一层该被回收")
	}
}
