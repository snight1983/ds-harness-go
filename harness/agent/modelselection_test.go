// 本文件的作用：那份可变的模型选择——它自己的读写面，以及接到提示词装配和请求
// 路由两处之后，两边为什么永远说的是同一个模型。

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// selectionFixture 是这一组测试共用的那一整套接线。
type selectionFixture struct {
	owner     *scope.Scope
	agents    *Registry
	prompts   *systemprompt.Registry
	selection *ModelSelectionRef
	agent     *fakeAgent
	detach    func(context.Context) error
}

// newSelectionFixture 把两张表、一个活 agent 和一份接好线的选择一起备好。
func newSelectionFixture(t *testing.T) *selectionFixture {
	t.Helper()
	ctx := context.Background()
	owner := rootScope(t)
	prompts, err := systemprompt.NewRegistry(ctx, owner, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	agents := newRegistry(t)
	agent := newFakeAgent(t, "selector", nil)
	live(t, agents, agent, nil)

	selection := NewModelSelectionRef()
	detach, err := InstallModelSelection(ctx, owner, agents, prompts, selection)
	if err != nil {
		t.Fatalf("接线失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	return &selectionFixture{
		owner: owner, agents: agents, prompts: prompts,
		selection: selection, agent: agent, detach: detach,
	}
}

// assemble 跑一次装配，跑不出来当场失败。
func (f *selectionFixture) assemble(t *testing.T) systemprompt.PromptAssembly {
	t.Helper()
	assembly, err := f.prompts.Assemble(context.Background(), systemprompt.AssembleContext{})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	return assembly
}

// resolve 跑一次请求路由，base 交出机器本来那一份。
func (f *selectionFixture) resolve(t *testing.T, base llm.CallConfig) llm.CallConfig {
	t.Helper()
	resolved, err := f.agents.ResolveRequest(
		context.Background(),
		Request{Agent: f.agent, Turn: 1, Step: 1},
		func(context.Context) (llm.CallConfig, error) { return base, nil },
	)
	if err != nil {
		t.Fatalf("请求路由失败：%v", err)
	}
	return resolved
}

// variableOf 读一个装配变量；没登记过或者没值时第二个返回值为假。
func variableOf(assembly systemprompt.PromptAssembly, name string) (string, bool) {
	value, present := assembly.Variables[name]
	if !present || value == nil {
		return "", false
	}
	return *value, true
}

// TestModelSelectionRefStartsEmpty 还没选中任何模型时两个面都说「没有」。
func TestModelSelectionRefStartsEmpty(t *testing.T) {
	ref := NewModelSelectionRef()
	if _, present := ref.Current(); present {
		t.Fatal("刚造出来不该有当下的选择")
	}
	if _, present := ref.Assembled(); present {
		t.Fatal("还没有步骤进过装配，不该有抓拍")
	}
}

// TestModelSelectionRefSelectAndClear 选中读得回来，撤掉之后又回到「没有」。
func TestModelSelectionRefSelectAndClear(t *testing.T) {
	ref := NewModelSelectionRef()
	want := ModelSelection{Provider: "p", Model: "m", ReasoningEffort: "high"}
	ref.Select(want)

	got, present := ref.Current()
	if !present || got != want {
		t.Fatalf("读回来的选择不对：%+v %v", got, present)
	}

	ref.Clear()
	if got, present := ref.Current(); present || got != (ModelSelection{}) {
		t.Fatalf("撤掉之后该回到没有：%+v %v", got, present)
	}
}

// TestInstallModelSelectionNeedsAllThreePieces 三样缺一样都接不上线。
func TestInstallModelSelectionNeedsAllThreePieces(t *testing.T) {
	ctx := context.Background()
	owner := rootScope(t)
	prompts, err := systemprompt.NewRegistry(ctx, owner, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	agents := newRegistry(t)

	cases := map[string]struct {
		agents    *Registry
		prompts   *systemprompt.Registry
		selection *ModelSelectionRef
	}{
		"没有 agent 注册表": {nil, prompts, NewModelSelectionRef()},
		"没有提示词注册表":     {agents, nil, NewModelSelectionRef()},
		"没有选择":         {agents, prompts, nil},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := InstallModelSelection(ctx, owner, testCase.agents, testCase.prompts, testCase.selection)
			if !errors.Is(err, ErrInvalidRegistration) {
				t.Fatalf("该报 ErrInvalidRegistration，得到 %v", err)
			}
		})
	}
}

// TestInstallModelSelectionIsTransparentWhenNothingIsSelected 没选中模型时两边
// 都原样放行。
func TestInstallModelSelectionIsTransparentWhenNothingIsSelected(t *testing.T) {
	fixture := newSelectionFixture(t)

	assembly := fixture.assemble(t)
	if _, present := variableOf(assembly, "provider"); present {
		t.Fatalf("没选模型时不该改写变量：%v", assembly.Variables)
	}

	base := llm.CallConfig{Provider: "本来的", Model: "本来的模型", ReasoningEffort: "low"}
	if got := fixture.resolve(t, base); !llm.CallConfigEquals(got, base) {
		t.Fatalf("没选模型时该原样放行：%+v", got)
	}
}

// TestInstallModelSelectionRewritesBothFaces 选中之后提示词变量和调用配置说的是
// 同一个模型——这套东西存在的全部理由。
func TestInstallModelSelectionRewritesBothFaces(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1", ReasoningEffort: "high"})

	assembly := fixture.assemble(t)
	provider, hasProvider := variableOf(assembly, "provider")
	model, hasModel := variableOf(assembly, "model")
	if !hasProvider || !hasModel || provider != "月球" || model != "m1" {
		t.Fatalf("提示词变量没改写：%q %q", provider, model)
	}

	got := fixture.resolve(t, llm.CallConfig{Provider: "本来的", Model: "本来的模型"})
	if got.Provider != "月球" || got.Model != "m1" || got.ReasoningEffort != "high" {
		t.Fatalf("调用配置没改写：%+v", got)
	}
}

// TestInstallModelSelectionClearsAnInheritedReasoningEffort 一份不带档位的选择
// 要把继承来的档位清掉，不然提示词说的是新模型、请求带的是旧模型的档位。
func TestInstallModelSelectionClearsAnInheritedReasoningEffort(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})
	fixture.assemble(t)

	got := fixture.resolve(t, llm.CallConfig{Provider: "本来的", ReasoningEffort: "high"})
	if got.ReasoningEffort != "" {
		t.Fatalf("继承来的档位该被清掉：%q", got.ReasoningEffort)
	}
}

// TestInstallModelSelectionRequestUsesTheAssembledSnapshot 请求认的是抓拍而不是
// 当下的选择：一次和步骤赛跑的切换要么整个落在这一步、要么整个落在下一步。
func TestInstallModelSelectionRequestUsesTheAssembledSnapshot(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})
	fixture.assemble(t)

	// 装配已经过去了，这一次切换该算下一步的。
	fixture.selection.Select(ModelSelection{Provider: "火星", Model: "m2"})

	if got := fixture.resolve(t, llm.CallConfig{}); got.Provider != "月球" || got.Model != "m1" {
		t.Fatalf("请求该认那份抓拍：%+v", got)
	}
}

// TestInstallModelSelectionSnapshotsBeforeDelegating 抓拍在委托下去**之前**取：
// 里层的规则可能自己跑得很久，那期间的一次切换算下一步的。
//
// 这里那条里层规则就是「跑得很久的那一层」——本仓库的瀑布次序是先登记的在外面，
// 所以后登记的这一条落在接线里面。
func TestInstallModelSelectionSnapshotsBeforeDelegating(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})

	detach, err := fixture.prompts.OnAssemble(context.Background(), fixture.owner, func(
		ctx context.Context,
		assembly systemprompt.PromptAssembly,
		assemble systemprompt.AssembleContext,
		next func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
	) (systemprompt.PromptAssembly, error) {
		fixture.selection.Select(ModelSelection{Provider: "火星", Model: "m2"})
		return next(assembly)
	})
	if err != nil {
		t.Fatalf("挂里层规则失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	assembly := fixture.assemble(t)
	if provider, _ := variableOf(assembly, "provider"); provider != "月球" {
		t.Fatalf("提示词该用进装配那一刻的那一份：%q", provider)
	}
	if got := fixture.resolve(t, llm.CallConfig{}); got.Provider != "月球" {
		t.Fatalf("请求该用同一份抓拍：%+v", got)
	}
}

// TestInstallModelSelectionKeepsNoSnapshotWhenAssemblyFails 装配失败时不记抓拍：
// 那一步根本没成形，请求路由也就轮不到它。
func TestInstallModelSelectionKeepsNoSnapshotWhenAssemblyFails(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})

	boom := errors.New("装不下去了")
	detach, err := fixture.prompts.OnAssemble(context.Background(), fixture.owner, func(
		context.Context,
		systemprompt.PromptAssembly,
		systemprompt.AssembleContext,
		func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
	) (systemprompt.PromptAssembly, error) {
		return systemprompt.PromptAssembly{}, boom
	})
	if err != nil {
		t.Fatalf("挂里层规则失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	if _, err := fixture.prompts.Assemble(context.Background(), systemprompt.AssembleContext{}); !errors.Is(err, boom) {
		t.Fatalf("该把里层那条错误交出来，得到 %v", err)
	}
	if _, present := fixture.selection.Assembled(); present {
		t.Fatal("装配失败不该留下抓拍")
	}
	if got := fixture.resolve(t, llm.CallConfig{Provider: "本来的"}); got.Provider != "本来的" {
		t.Fatalf("没有抓拍时请求该原样放行：%+v", got)
	}
}

// TestInstallModelSelectionCopiesTheVariables 改写变量前先复制一份：交进来的那
// 份是里层算出来的，那一层可能还留着它的引用，就地改会把改动漏出去。
func TestInstallModelSelectionCopiesTheVariables(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})

	// 里层那份变量表**不是空的**：复制这件事只有在真有东西要搬的时候才看得出来，
	// 而且搬过去的那些必须一个不少。
	untouched := "里层放的"
	inner := map[string]*string{"persona": &untouched}
	detach, err := fixture.prompts.OnAssemble(context.Background(), fixture.owner, func(
		ctx context.Context,
		assembly systemprompt.PromptAssembly,
		assemble systemprompt.AssembleContext,
		next func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
	) (systemprompt.PromptAssembly, error) {
		assembled, err := next(assembly)
		if err != nil {
			return systemprompt.PromptAssembly{}, err
		}
		assembled.Variables = inner
		return assembled, nil
	})
	if err != nil {
		t.Fatalf("挂里层规则失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	assembly := fixture.assemble(t)
	if _, leaked := inner["provider"]; leaked {
		t.Fatalf("改写漏到里层那份变量表上了：%v", inner)
	}
	if carried, present := variableOf(assembly, "persona"); !present || carried != untouched {
		t.Fatalf("里层放的变量该原样搬过来：%q %v", carried, present)
	}
}

// TestInstallModelSelectionFailsOnADisposedOwner 宿主作用域已经释放时接线整个挂
// 不上：交出错误而不是一份半截接线。
func TestInstallModelSelectionFailsOnADisposedOwner(t *testing.T) {
	ctx := context.Background()
	owner := rootScope(t)
	prompts, err := systemprompt.NewRegistry(ctx, owner, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	agents := newRegistry(t)

	// 提示词注册表自己就挂在 owner 上，所以这里另开一把钥匙当宿主：要验的是
	// 「登记挂不上去」，不是「注册表本身没了」。
	dead := keyedScope(t, "dead", nil)
	if err := dead.Dispose(ctx); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}

	detach, err := InstallModelSelection(ctx, dead, agents, prompts, NewModelSelectionRef())
	if !errors.Is(err, scope.ErrScopeDisposed) {
		t.Fatalf("该报 ErrScopeDisposed，得到 %v", err)
	}
	if detach != nil {
		t.Fatal("挂不上线时不该交出摘除函数")
	}
}

// TestInstallModelSelectionPropagatesADownstreamRequestError 里层交出来的错误
// 原样往上走，配置是零值。
func TestInstallModelSelectionPropagatesADownstreamRequestError(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})
	fixture.assemble(t)

	boom := errors.New("路由不出去")
	got, err := fixture.agents.ResolveRequest(
		context.Background(),
		Request{Agent: fixture.agent},
		func(context.Context) (llm.CallConfig, error) { return llm.CallConfig{}, boom },
	)
	if !errors.Is(err, boom) {
		t.Fatalf("该把里层那条错误交出来，得到 %v", err)
	}
	if !llm.CallConfigEquals(got, llm.CallConfig{}) {
		t.Fatalf("失败时该交出零值：%+v", got)
	}
}

// TestInstallModelSelectionDetachRemovesBothSides 摘除把两处一起摘掉，不留半套
// 接线——那会让提示词说 A、请求还说调用方原本那一个。
func TestInstallModelSelectionDetachRemovesBothSides(t *testing.T) {
	fixture := newSelectionFixture(t)
	fixture.selection.Select(ModelSelection{Provider: "月球", Model: "m1"})
	fixture.assemble(t)

	if err := fixture.detach(context.Background()); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}

	assembly := fixture.assemble(t)
	if _, present := variableOf(assembly, "provider"); present {
		t.Fatalf("摘除之后不该还改写变量：%v", assembly.Variables)
	}
	base := llm.CallConfig{Provider: "本来的", ReasoningEffort: "low"}
	if got := fixture.resolve(t, base); !llm.CallConfigEquals(got, base) {
		t.Fatalf("摘除之后该原样放行：%+v", got)
	}
}
