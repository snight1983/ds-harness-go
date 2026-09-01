// 本文件的作用：这一行的测试——遮蔽、同层重名、完整提示词、压制运行期上下文，
// 以及撤销把这三件事都还原。

package persona

import (
	"context"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
)

// fixture 是一个造好的注册表加上它的根作用域。
type fixture struct {
	registry *systemprompt.Registry
	root     *scope.Scope
}

func newFixture(t *testing.T, options systemprompt.Options) *fixture {
	t.Helper()
	root := scope.NewRoot()
	registry, err := systemprompt.NewRegistry(context.Background(), root, options)
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	t.Cleanup(func() { _ = root.Dispose(context.Background()) })
	return &fixture{registry: registry, root: root}
}

// agentScope 造一个有身份的作用域，用来装这一行。
func agentScope(t *testing.T, label string) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func render(t *testing.T, f *fixture, key *scope.Key) string {
	t.Helper()
	assembly, err := f.registry.Assemble(context.Background(), systemprompt.AssembleContext{Scope: key})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	rendered, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	return rendered
}

func assemble(t *testing.T, f *fixture, key *scope.Key) systemprompt.PromptAssembly {
	t.Helper()
	assembly, err := f.registry.Assemble(context.Background(), systemprompt.AssembleContext{Scope: key})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	return assembly
}

func TestAScopedPersonaShadowsTheDeploymentOne(t *testing.T) {
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")

	if _, err := Install(context.Background(), f.registry, owner, Config{Text: "这个 agent 的人设。"}); err != nil {
		t.Fatal(err)
	}

	scoped := render(t, f, owner.Key())
	if !strings.Contains(scoped, "这个 agent 的人设。") || strings.Contains(scoped, "部署方的人设。") {
		t.Fatalf("这个作用域里该只看得见自己那份：%q", scoped)
	}
	// 别的 agent 看到的还是部署方那份。
	if global := render(t, f, nil); !strings.Contains(global, "部署方的人设。") {
		t.Fatalf("别处不该受影响：%q", global)
	}
}

func TestTheShadowingPersonaKeepsThePersonaSlotsPosition(t *testing.T) {
	// 遮蔽换的是这个槽位的内容，不是它的位置——人设仍然排在宿主身份声明后面。
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")
	if _, err := Install(context.Background(), f.registry, owner, Config{Text: "自己的人设。"}); err != nil {
		t.Fatal(err)
	}

	assembly := assemble(t, f, owner.Key())
	if len(assembly.Sections) != 2 ||
		assembly.Sections[0].Name != "harness:identity" ||
		assembly.Sections[1].Name != systemprompt.PersonaSection {
		t.Fatalf("段落次序是 %#v", assembly.Sections)
	}
}

func TestMountingOnTheRegistrysOwnLayerCollidesLoudly(t *testing.T) {
	// 这一行是**只在作用域里成立**的：装在注册表持有的那一层上，撞的是注册表自己
	// 那份无条件登记。它该当场报错，而不是悄悄并存出两份人设。
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})

	_, err := Install(context.Background(), f.registry, f.root, Config{Text: "撞车的人设。"})
	if err == nil {
		t.Fatal("装在全局层上该失败")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("诊断是 %v", err)
	}
	// 撞车之后原来那份仍然完好。
	if rendered := render(t, f, nil); !strings.Contains(rendered, "部署方的人设。") {
		t.Fatalf("失败的那次登记动了原来那份：%q", rendered)
	}
}

func TestAnEmptyPersonaDropsTheSection(t *testing.T) {
	// 空正文是合法的：段落照样登记，只是渲染时被丢掉——和注册表对一份空人设的
	// 处理一样。这也是「遮蔽掉部署方人设、什么都不放回去」的写法。
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")
	if _, err := Install(context.Background(), f.registry, owner, Config{Text: ""}); err != nil {
		t.Fatal(err)
	}

	if rendered := render(t, f, owner.Key()); strings.Contains(rendered, "部署方的人设。") {
		t.Fatalf("被遮蔽掉的那份不该回来：%q", rendered)
	}
	// 段落本身还在装配里，只是正文空。
	assembly := assemble(t, f, owner.Key())
	if len(assembly.Sections) != 2 || assembly.Sections[1].Text != "" {
		t.Fatalf("装配是 %#v", assembly.Sections)
	}
}

func TestThePersonaTextIsAStrictlyInterpolatedTemplate(t *testing.T) {
	f := newFixture(t, systemprompt.Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	value := "/srv"
	if _, err := f.registry.Variable(ctx, f.root, "cwd",
		func(context.Context, systemprompt.AssembleContext) (*string, error) { return &value, nil },
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ctx, f.registry, owner, Config{Text: "你在 {{cwd}} 里干活。"}); err != nil {
		t.Fatal(err)
	}
	if rendered := render(t, f, owner.Key()); rendered != "You are an AI agent powered by DeepSeek Harness.\n\n你在 /srv 里干活。" {
		t.Fatalf("渲染结果是 %q", rendered)
	}

	// 写错了名字是报错，不是悄悄留成原文。
	other := agentScope(t, "other")
	if _, err := Install(ctx, f.registry, other, Config{Text: "{{nope}}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := systemprompt.RenderPrompt(assemble(t, f, other.Key())); err == nil ||
		!strings.Contains(err.Error(), "unknown prompt variable") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestACompletePersonaBecomesTheWholePrompt(t *testing.T) {
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	// 一段本来会跟在后面的工具指引，用来证明它真的被压掉了。
	if _, err := f.registry.Section(ctx, f.root, systemprompt.PromptSection{
		Name: "tools:guidance", Order: 100, Text: systemprompt.StaticText("工具怎么用。"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ctx, f.registry, owner, Config{Text: "我就是整份提示词。", Complete: true}); err != nil {
		t.Fatal(err)
	}

	if rendered := render(t, f, owner.Key()); rendered != "我就是整份提示词。" {
		t.Fatalf("渲染结果是 %q", rendered)
	}
	// 别的 agent 那边三段都还在。
	if rendered := render(t, f, nil); !strings.Contains(rendered, "工具怎么用。") {
		t.Fatalf("别处不该受影响：%q", rendered)
	}
}

func TestSuppressRuntimeContextOnlyReachesThisPersonasScope(t *testing.T) {
	f := newFixture(t, systemprompt.Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := f.registry.Context(ctx, f.root, systemprompt.PromptContext{
		Name: "workspace", Text: systemprompt.StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ctx, f.registry, owner, Config{
		Text: "人设。", SuppressRuntimeContext: true,
	}); err != nil {
		t.Fatal(err)
	}

	if contexts := assemble(t, f, owner.Key()).Contexts; len(contexts) != 0 {
		t.Fatalf("这个作用域里该被压掉：%#v", contexts)
	}
	if contexts := assemble(t, f, nil).Contexts; len(contexts) != 1 {
		t.Fatalf("别处不该受影响：%#v", contexts)
	}
}

func TestTheDefaultConfigKeepsTheRuntimeContext(t *testing.T) {
	// 零值的 Config 说的就是 DSH 那个 includeRuntimeContext 默认 true。
	f := newFixture(t, systemprompt.Options{})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := f.registry.Context(ctx, f.root, systemprompt.PromptContext{
		Name: "workspace", Text: systemprompt.StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ctx, f.registry, owner, Config{Text: "人设。"}); err != nil {
		t.Fatal(err)
	}
	if contexts := assemble(t, f, owner.Key()).Contexts; len(contexts) != 1 {
		t.Fatalf("默认不该压掉运行期上下文：%#v", contexts)
	}
}

func TestUninstallRestoresEverything(t *testing.T) {
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	if _, err := f.registry.Context(ctx, f.root, systemprompt.PromptContext{
		Name: "workspace", Text: systemprompt.StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}
	uninstall, err := Install(ctx, f.registry, owner, Config{
		Text: "自己的人设。", SuppressRuntimeContext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := uninstall(ctx); err != nil {
		t.Fatal(err)
	}

	// 遮蔽撤了，部署方那份回来了；压制也放开了。
	if rendered := render(t, f, owner.Key()); !strings.Contains(rendered, "部署方的人设。") {
		t.Fatalf("撤销之后该回到部署方那份：%q", rendered)
	}
	if contexts := assemble(t, f, owner.Key()).Contexts; len(contexts) != 1 {
		t.Fatalf("撤销之后运行期上下文该回来：%#v", contexts)
	}
	// 撤完之后同一个作用域可以再装一次。
	if _, err := Install(ctx, f.registry, owner, Config{Text: "第二次。"}); err != nil {
		t.Fatalf("撤销没撤干净：%v", err)
	}
}

func TestUninstallWithoutSuppressionOnlyRemovesTheSection(t *testing.T) {
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	owner := agentScope(t, "agent")
	ctx := context.Background()

	uninstall, err := Install(ctx, f.registry, owner, Config{Text: "自己的人设。"})
	if err != nil {
		t.Fatal(err)
	}
	if err := uninstall(ctx); err != nil {
		t.Fatal(err)
	}
	if rendered := render(t, f, owner.Key()); !strings.Contains(rendered, "部署方的人设。") {
		t.Fatalf("撤销之后该回到部署方那份：%q", rendered)
	}
}

func TestInstallNeedsARegistry(t *testing.T) {
	if _, err := Install(context.Background(), nil, scope.NewRoot(), Config{Text: "x"}); err == nil ||
		!strings.Contains(err.Error(), "需要一个提示词注册表") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestInstallNeedsAnOwner(t *testing.T) {
	f := newFixture(t, systemprompt.Options{})
	// 两条路都得挡住：压制那一次是第一次登记，人设那一次在它后面。
	if _, err := Install(context.Background(), f.registry, nil, Config{
		Text: "x", SuppressRuntimeContext: true,
	}); err == nil {
		t.Fatal("没有 owner 该失败")
	}
	if _, err := Install(context.Background(), f.registry, nil, Config{Text: "x"}); err == nil {
		t.Fatal("没有 owner 该失败")
	}
}

func TestAFailedInstallLeavesNoSuppressionBehind(t *testing.T) {
	// 装在注册表自己那一层上，人设那一次登记必砸。压制那一次已经生效了，所以这
	// 一步必须把它放开——否则调用方拿到的是一个错误，外加一份再也撤不掉的压制。
	f := newFixture(t, systemprompt.Options{Persona: "部署方的人设。"})
	ctx := context.Background()

	if _, err := f.registry.Context(ctx, f.root, systemprompt.PromptContext{
		Name: "workspace", Text: systemprompt.StaticText("在这"),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(ctx, f.registry, f.root, Config{
		Text: "撞车的人设。", SuppressRuntimeContext: true,
	}); err == nil {
		t.Fatal("装在全局层上该失败")
	}

	if contexts := assemble(t, f, nil).Contexts; len(contexts) != 1 {
		t.Fatalf("失败之后不该留下压制：%#v", contexts)
	}
}
