// 本文件的作用：两种进程内孩子共用那套组装的测试——深度预算、agent 选项的路由、
// 耐久的会话元数据、孩子创建窗口里那份带作用域的组合，以及种进派发这条边的那份策略。

package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/fs/fstest"
	"github.com/snight1983/ds-harness-go/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/preset/agentpresets"
	"github.com/snight1983/ds-harness-go/session"
)

// ---- 深度 ----

// 持久的头是那条单调的地板：一个恢复回来的父不会像顶层一样重新数起。
func TestResolveChildDepthCountsFromTheParentHeader(t *testing.T) {
	for name, depth := range map[string]int{"顶层": 0, "已经派发过两层": 2} {
		t.Run(name, func(t *testing.T) {
			childDepth, err := ResolveChildDepth(agentAtDepth(t, "parent", depth), nil)
			if err != nil {
				t.Fatalf("解算深度失败：%v", err)
			}
			if childDepth != depth+1 {
				t.Fatalf("孩子该在 %d 层，实际 %d", depth+1, childDepth)
			}
		})
	}
}

// 正好顶到上限是允许的，再往下一层才越界。
func TestResolveChildDepthStopsAtTheRequestedCeiling(t *testing.T) {
	parent := agentAtDepth(t, "parent", 2)
	ceiling := 3
	if _, err := ResolveChildDepth(parent, &ceiling); err != nil {
		t.Fatalf("正好顶到上限该放行，实际 %v", err)
	}

	tooLow := 2
	_, err := ResolveChildDepth(parent, &tooLow)
	var depthErr *DepthError
	if !errors.As(err, &depthErr) {
		t.Fatalf("越界该报 DepthError，实际 %v", err)
	}
	if depthErr.AttemptedDepth != 3 || depthErr.MaxDepth != 2 {
		t.Fatalf("那两个数该说清越在哪儿，实际 %#v", depthErr)
	}
	// 上限本身是合法的，越界是这次派发在运行期的结局，不是调用方给错了东西。
	if errors.Is(err, ErrInvalidRequest) {
		t.Fatal("越界不该被判成请求本身不合法")
	}
	if !strings.Contains(depthErr.Error(), "maxDepth") {
		t.Fatalf("那句话该说到 maxDepth，实际 %q", depthErr.Error())
	}
}

// ---- agent 选项 ----

// 没说的字段走父那条路由；零值就是「没说」。
func TestResolveChildAgentOptionsRoutesThroughTheParent(t *testing.T) {
	parent := agentAtDepth(t, "parent", 0)
	parent.options = agent.Options{Provider: "p", Model: "m", MaxTokens: 100}

	inherited := ResolveChildAgentOptions(parent, agent.Options{})
	if inherited != parent.options {
		t.Fatalf("一样都没说该整份继承，实际 %#v", inherited)
	}

	overridden := ResolveChildAgentOptions(parent, agent.Options{Provider: "别的", MaxTokens: 7})
	if overridden.Provider != "别的" || overridden.MaxTokens != 7 {
		t.Fatalf("说了的该盖掉父的，实际 %#v", overridden)
	}
	if overridden.Model != "m" {
		t.Fatalf("没说的那一项该留着父的，实际 %q", overridden.Model)
	}

	// 换模型这一项也一样：三个字段各判各的零值。
	remodelled := ResolveChildAgentOptions(parent, agent.Options{Model: "另一个"})
	if remodelled.Model != "另一个" {
		t.Fatalf("说了的模型该盖掉父的，实际 %q", remodelled.Model)
	}
	if remodelled.Provider != "p" || remodelled.MaxTokens != 100 {
		t.Fatalf("其余两项该留着父的，实际 %#v", remodelled)
	}
}

// ---- 会话元数据 ----

func TestChildSessionMetaStampsTheLineage(t *testing.T) {
	parent := agentAtDepth(t, "parent", 1)
	meta := ChildSessionMeta(parent, 2, 5, nil)

	if meta.WorkspaceID != testWorkspaceID || meta.ParentSession != "parent" {
		t.Fatalf("该盖上父的工作目录和血统，实际 %#v", meta)
	}
	if meta.Origin != session.OriginSubagent {
		t.Fatalf("来源该是 %q，实际 %q", session.OriginSubagent, meta.Origin)
	}
	// 耐久：那份递归预算必须活过持久化和恢复。
	if meta.DelegationDepth != 2 || meta.SeedLength != 5 {
		t.Fatalf("深度和种子边界该原样记下，实际 %#v", meta)
	}
	// 一套不组装名册的部署不记预设：那些行本来就在宿主组合里。
	if meta.AgentPreset != "" {
		t.Fatalf("没有名册就不该记预设，实际 %q", meta.AgentPreset)
	}
}

// emptyRoster 造一份没有任何常驻装载的预设名册：认亲这一步走得完，但答出来是
// 「一份都没认」。要的正是这个——presets 在不在场那条分支和名册里有什么无关。
func emptyRoster(t *testing.T) *agentpresets.Roster {
	t.Helper()
	roster, err := agentpresets.New(
		agentpresets.Config{FileSystem: fstest.New(), Default: "base"}, nil)
	if err != nil {
		t.Fatalf("造预设名册失败：%v", err)
	}
	return roster
}

// 一套组装了名册的部署要把父跑在哪份预设上记进孩子的血统。父一份都没认——包括一个
// 连作用域都没有的父——记的是空串，而不是失败：没有预设这件事本身是合法的。
func TestChildSessionMetaRecordsTheComposedPresetWhenThereIsARoster(t *testing.T) {
	roster := emptyRoster(t)

	if meta := ChildSessionMeta(agentAtDepth(t, "parent", 0), 1, 0, roster); meta.AgentPreset != "" {
		t.Fatalf("父一份预设都没认就该记空串，实际 %q", meta.AgentPreset)
	}
	scopeless := agentAtDepth(t, "scopeless", 0)
	scopeless.scope = nil
	if meta := ChildSessionMeta(scopeless, 1, 0, roster); meta.AgentPreset != "" {
		t.Fatalf("没有作用域的父同样该记空串，实际 %q", meta.AgentPreset)
	}
}

// ---- 孩子那份组合 ----

// promptRegistry 造一个带部署人设的系统提示词注册表，登记落在全局层。
func promptRegistry(t *testing.T, persona string) *systemprompt.Registry {
	t.Helper()
	registry, err := systemprompt.NewRegistry(t.Context(), rootScope(t), systemprompt.Options{
		Persona:             persona,
		OmitHarnessIdentity: true,
	})
	if err != nil {
		t.Fatalf("造系统提示词注册表失败：%v", err)
	}
	return registry
}

// assembledFor 把某个作用域看到的那份装配摊成「段落名→正文」和「上下文名→正文」。
func assembledFor(
	t *testing.T,
	registry *systemprompt.Registry,
	key *scope.Key,
) (sections, contexts map[string]string) {
	t.Helper()
	assembly, err := registry.Assemble(t.Context(), systemprompt.AssembleContext{Scope: key})
	if err != nil {
		t.Fatalf("装配提示词失败：%v", err)
	}
	sections, contexts = map[string]string{}, map[string]string{}
	for _, section := range assembly.Sections {
		sections[section.Name] = section.Text
	}
	for _, each := range assembly.Contexts {
		contexts[each.Name] = each.Text
	}
	return sections, contexts
}

func TestApplyChildCompositionNeedsTheSystemPromptRegistry(t *testing.T) {
	err := ApplyChildComposition(
		t.Context(), keyedScope(t, "child", nil), agentAtDepth(t, "parent", 0),
		ChildComposition{}, ChildCompositionServices{},
	)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有系统提示词注册表该被拒，实际 %v", err)
	}
}

// 那句派发范围陈述只有孩子看得见——它归孩子这个作用域所有。
func TestApplyChildCompositionScopesTheDelegationStatementToTheChild(t *testing.T) {
	registry := promptRegistry(t, "部署人设")
	parent := agentAtDepth(t, "parent", 0)
	child := keyedScope(t, "child", nil)

	if err := ApplyChildComposition(t.Context(), child, parent, ChildComposition{},
		ChildCompositionServices{SystemPrompt: registry}); err != nil {
		t.Fatalf("组装孩子失败：%v", err)
	}

	_, childContexts := assembledFor(t, registry, child.Key())
	if childContexts[delegationContextName] != DelegationContext {
		t.Fatalf("孩子该看得见那句派发范围陈述，实际 %q", childContexts[delegationContextName])
	}
	_, parentContexts := assembledFor(t, registry, parent.Scope().Key())
	if _, leaked := parentContexts[delegationContextName]; leaked {
		t.Fatal("父不该看见那句只给孩子的陈述")
	}
}

// 只给这个孩子的那份人设盖掉部署人设——最近的那一层赢下这个名字。
func TestApplyChildCompositionOverridesThePersonaForTheChildOnly(t *testing.T) {
	registry := promptRegistry(t, "部署人设")
	parent := agentAtDepth(t, "parent", 0)
	child := keyedScope(t, "child", nil)

	if err := ApplyChildComposition(t.Context(), child, parent,
		ChildComposition{Persona: "只给这个孩子"},
		ChildCompositionServices{SystemPrompt: registry}); err != nil {
		t.Fatalf("组装孩子失败：%v", err)
	}

	childSections, _ := assembledFor(t, registry, child.Key())
	if childSections[systemprompt.PersonaSection] != "只给这个孩子" {
		t.Fatalf("孩子该读到自己那份人设，实际 %q", childSections[systemprompt.PersonaSection])
	}
	parentSections, _ := assembledFor(t, registry, parent.Scope().Key())
	if parentSections[systemprompt.PersonaSection] != "部署人设" {
		t.Fatalf("父该照旧读到部署人设，实际 %q", parentSections[systemprompt.PersonaSection])
	}
}

// 不给人设就不动那个槽位，而不是把它盖成空的。
func TestApplyChildCompositionLeavesThePersonaAloneWhenNoneIsGiven(t *testing.T) {
	registry := promptRegistry(t, "部署人设")
	child := keyedScope(t, "child", nil)

	if err := ApplyChildComposition(t.Context(), child, agentAtDepth(t, "parent", 0),
		ChildComposition{}, ChildCompositionServices{SystemPrompt: registry}); err != nil {
		t.Fatalf("组装孩子失败：%v", err)
	}
	childSections, _ := assembledFor(t, registry, child.Key())
	if childSections[systemprompt.PersonaSection] != "部署人设" {
		t.Fatalf("不给人设该照旧是部署人设，实际 %q", childSections[systemprompt.PersonaSection])
	}
}

// 认亲和那几笔登记在同一次调用里：名册在场时那一步真的跑过，而一个一份预设都没认的
// 父不该把整次组装带崩。
func TestApplyChildCompositionJoinsTheParentPresetWhenThereIsARoster(t *testing.T) {
	registry := promptRegistry(t, "")
	child := keyedScope(t, "child", nil)

	if err := ApplyChildComposition(t.Context(), child, agentAtDepth(t, "parent", 0),
		ChildComposition{}, ChildCompositionServices{
			SystemPrompt: registry,
			Presets:      emptyRoster(t),
		}); err != nil {
		t.Fatalf("组装孩子失败：%v", err)
	}
	_, childContexts := assembledFor(t, registry, child.Key())
	if childContexts[delegationContextName] != DelegationContext {
		t.Fatal("认亲之后那句派发范围陈述照旧该挂上")
	}
}

// 要设工具范围就必须有工具运行时——绝不「先收下再忽略」。
// mountedRoster 造一份**真的装了一份预设**的名册，并把 parentKey 认进那份常驻装载。
func mountedRoster(t *testing.T, parentKey *scope.Key) *agentpresets.Roster {
	t.Helper()

	// 这道缝上的路径是斜杠分隔的（见 preset/agentpresets/content.go），
	// 而这份内存假件的键就是这样的路径。
	store := fstest.New()
	root := "/presets"
	dir := path.Join(root, "demo")
	store.Seed(fs.TargetKey(path.Join(dir, agentpresets.CompositionFile)), "- name: alpha\n")
	roster, err := agentpresets.New(agentpresets.Config{
		FileSystem: store,
		Default:    "demo",
		Roots:      []agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}},
		Composers: agentpresets.ComposerSet{
			"alpha": func(context.Context, *scope.Scope, json.RawMessage) (func(context.Context) error, error) {
				return nil, nil
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("造预设名册失败：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })
	if _, err := roster.Mount(context.Background(), parentKey, "demo"); err != nil {
		t.Fatalf("把父认进预设失败：%v", err)
	}
	return roster
}

// 认亲失败是这次组装的结论，不是可以咽下去的东西：一个没认亲就组装好的孩子会看到
// 一个空的工具注册表、以及它父亲那些提示词段落一段都没有。
func TestApplyChildCompositionSurfacesARefusedPresetJoin(t *testing.T) {
	parent := agentAtDepth(t, "parent", 0)
	roster := mountedRoster(t, parent.Scope().Key())
	// 这个孩子的钥匙已经认过一个父了，再认进那份常驻装载会撞上「一把钥匙只认一个父」。
	child := keyedScope(t, "child", parent.Scope().Key())

	err := ApplyChildComposition(t.Context(), child, parent, ChildComposition{},
		ChildCompositionServices{SystemPrompt: promptRegistry(t, ""), Presets: roster})
	if err == nil {
		t.Fatal("认亲失败该被抛上来")
	}
}

// 那句派发范围陈述登记不上去时同样要报出来：这个孩子会以为自己没有被派发的边界。
func TestApplyChildCompositionSurfacesARefusedDelegationStatement(t *testing.T) {
	registry := promptRegistry(t, "")
	child := keyedScope(t, "child", nil)
	// 先把那个名字占掉，于是下面那笔登记撞上重名。
	if _, err := registry.Context(t.Context(), child, systemprompt.PromptContext{
		Name:  delegationContextName,
		Order: delegationContextOrder,
		Text:  systemprompt.StaticText("先占着"),
	}); err != nil {
		t.Fatalf("占名失败：%v", err)
	}

	err := ApplyChildComposition(t.Context(), child, agentAtDepth(t, "parent", 0),
		ChildComposition{}, ChildCompositionServices{SystemPrompt: registry})
	if err == nil {
		t.Fatal("那句陈述登记不上去该被抛上来")
	}
}

// 只给这个孩子的那份人设登记不上去时也一样：孩子会跑在部署人设上，而不是派发方
// 指定的那一份。
func TestApplyChildCompositionSurfacesARefusedPersona(t *testing.T) {
	registry := promptRegistry(t, "部署人设")
	child := keyedScope(t, "child", nil)
	if _, err := registry.Section(t.Context(), child, systemprompt.PromptSection{
		Name:  systemprompt.PersonaSection,
		Order: systemprompt.PersonaOrder,
		Text:  systemprompt.StaticText("先占着"),
	}); err != nil {
		t.Fatalf("占名失败：%v", err)
	}

	err := ApplyChildComposition(t.Context(), child, agentAtDepth(t, "parent", 0),
		ChildComposition{Persona: "只给这个孩子"}, ChildCompositionServices{SystemPrompt: registry})
	if err == nil {
		t.Fatal("那份人设登记不上去该被抛上来")
	}
}

func TestApplyChildCompositionNeedsTheToolRuntimeForAFilter(t *testing.T) {
	err := ApplyChildComposition(
		t.Context(), keyedScope(t, "child", nil), agentAtDepth(t, "parent", 0),
		ChildComposition{ToolFilter: tools.Restriction{Allow: []string{"read"}}},
		ChildCompositionServices{SystemPrompt: promptRegistry(t, "")},
	)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有工具运行时该被拒，实际 %v", err)
	}
}

// toolNames 交出某个作用域**真的发给模型**的那些工具名。
//
// 这里不看 KnownNames：被限制摘掉的名字仍旧算「认识的名字」，那条面判不出限制。
func toolNames(runtime *tools.Runtime, key *scope.Key) []string {
	var names []string
	for _, schema := range runtime.Schemas(key) {
		names = append(names, schema.Name)
	}
	return names
}

func TestApplyChildCompositionRestrictsTheChildToolset(t *testing.T) {
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	owner := rootScope(t)
	for _, name := range []string{"read", "write"} {
		definition := &tools.Definition{
			Name:        name,
			Description: name,
			Parameters:  tools.Node{Type: tools.TypeObject},
			Output: tools.OutputDefinition{
				Schema: tools.Node{Type: tools.TypeString},
				Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
					return textContent(string(value)), nil
				},
			},
			Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
				return json.RawMessage(`""`), nil
			},
		}
		if _, err := runtime.Register(t.Context(), owner, definition); err != nil {
			t.Fatalf("注册工具失败：%v", err)
		}
	}

	child := keyedScope(t, "child", nil)
	if err := ApplyChildComposition(t.Context(), child, agentAtDepth(t, "parent", 0),
		ChildComposition{ToolFilter: tools.Restriction{Allow: []string{"read"}}},
		ChildCompositionServices{SystemPrompt: promptRegistry(t, ""), Tools: runtime}); err != nil {
		t.Fatalf("组装孩子失败：%v", err)
	}

	visible := toolNames(runtime, child.Key())
	if len(visible) != 1 || visible[0] != "read" {
		t.Fatalf("孩子该只看得见 read，实际 %#v", visible)
	}
	// 那份限制只归孩子所有，父的工具集一件都不少。
	if len(toolNames(runtime, nil)) != 2 {
		t.Fatalf("父那边该照旧看得见两件，实际 %#v", toolNames(runtime, nil))
	}
}

// 一份点到不存在的工具的范围当场把组装拒掉，而不是悄悄少给孩子几件工具。
func TestApplyChildCompositionSurfacesARejectedToolFilter(t *testing.T) {
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}

	err = ApplyChildComposition(
		t.Context(), keyedScope(t, "child", nil), agentAtDepth(t, "parent", 0),
		ChildComposition{ToolFilter: tools.Restriction{Allow: []string{"根本没有这件"}}},
		ChildCompositionServices{SystemPrompt: promptRegistry(t, ""), Tools: runtime},
	)
	if !errors.Is(err, tools.ErrInvalidRestriction) {
		t.Fatalf("该原样交回工具运行时那次拒绝，实际 %v", err)
	}
}

// ---- 派发策略 ----

// 没组装审批能力就一条都不种。
func TestCaptureDelegatedPolicyOverridesSeedsNothingWithoutApproval(t *testing.T) {
	if captured := CaptureDelegatedPolicyOverrides(nil); captured.ApprovalPolicy != "" {
		t.Fatalf("没有审批能力时不该种策略，实际 %q", captured.ApprovalPolicy)
	}
}

// 审批在场时策略被钉成「谁都不问」，不看父自己是什么策略。
func TestCaptureDelegatedPolicyOverridesPinsNever(t *testing.T) {
	approval, err := userapproval.New(userapproval.Config{
		// 这个服务只被问「在不在场」，两条回调走不到，给成最小的形状。
		Policy: userapproval.PolicyAsk,
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return nil, nil },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造审批服务失败：%v", err)
	}
	if captured := CaptureDelegatedPolicyOverrides(approval); captured.ApprovalPolicy != userapproval.PolicyNever {
		t.Fatalf("该被钉成 %q，实际 %q", userapproval.PolicyNever, captured.ApprovalPolicy)
	}
}

func TestAppendDelegatedPolicyOverridesWritesNothingWhenNoneWereCaptured(t *testing.T) {
	child := newFreeSession(t, "child", "parent", nil)
	if err := AppendDelegatedPolicyOverrides(child, DelegatedPolicyOverrides{}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if len(child.Events()) != 0 {
		t.Fatalf("一条都不该写，实际 %#v", child.Events())
	}
}

// `delegation` 这个来源正是这一笔和一次运行期切换的唯一区别。
func TestAppendDelegatedPolicyOverridesMarksTheDelegationSource(t *testing.T) {
	child := newFreeSession(t, "child", "parent", nil)
	if err := AppendDelegatedPolicyOverrides(child, DelegatedPolicyOverrides{
		ApprovalPolicy: userapproval.PolicyNever,
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	events := child.Events()
	if len(events) != 1 || events[0].Type != userapproval.EventPolicy {
		t.Fatalf("该恰好写下一条策略事件，实际 %#v", events)
	}
	var written userapproval.PolicyData
	if err := json.Unmarshal(events[0].Data, &written); err != nil {
		t.Fatalf("读回策略负载失败：%v", err)
	}
	if written.Policy != userapproval.PolicyNever || written.Source != userapproval.PolicySourceDelegation {
		t.Fatalf("该是一条派发来源的 never，实际 %#v", written)
	}
}
