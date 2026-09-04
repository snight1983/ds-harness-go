// 本文件的作用：注册表的测试——分层与遮蔽、一层之内的排序、缓存与 revision、
// 提供方失败的收容，以及取正文那条路上的每一次校验。

package skill

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// quietLogger 是一个不往测试输出里灌警告的日志器；本包的警告是**行为**，
// 由计数和结果来断言，不靠肉眼看。
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// harness 是一张注册表加它的变更计数。
type harness struct {
	registry *Registry
	changes  atomic.Int64
}

func newHarness(t *testing.T, maxEntries int) *harness {
	t.Helper()
	h := &harness{}
	registry, err := NewRegistry(Options{
		CollectCacheMaxEntries: maxEntries,
		Logger:                 quietLogger(),
		OnChange:               func() { h.changes.Add(1) },
	})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	h.registry = registry
	return h
}

// fakeProvider 是一个由测试完全控制的提供方。
type fakeProvider struct {
	name  string
	list  func(ctx context.Context, options LookupOptions) (Observation, error)
	get   func(ctx context.Context, candidate Candidate, options LookupOptions) (*Definition, error)
	calls atomic.Int64
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) List(ctx context.Context, options LookupOptions) (Observation, error) {
	p.calls.Add(1)
	if p.list == nil {
		return Observation{}, nil
	}
	return p.list(ctx, options)
}

func (p *fakeProvider) Get(ctx context.Context, candidate Candidate, options LookupOptions) (*Definition, error) {
	if p.get == nil {
		return nil, nil
	}
	return p.get(ctx, candidate, options)
}

// staticProvider 造一个只报一批固定候选的提供方。
func staticProvider(name string, candidates ...Candidate) *fakeProvider {
	return &fakeProvider{
		name: name,
		list: func(context.Context, LookupOptions) (Observation, error) {
			return Observation{Candidates: candidates}, nil
		},
		get: func(_ context.Context, candidate Candidate, _ LookupOptions) (*Definition, error) {
			return &Definition{Summary: candidate.Summary, Content: "正文：" + candidate.Name}, nil
		},
	}
}

// candidateOf 造一条合法候选。
func candidateOf(provider, name string, rank int) Candidate {
	return Candidate{
		Summary: Summary{
			Name:        name,
			Description: name + " 的说明",
			Invocation:  InvocationPolicy{ModelInvocable: true, UserInvocable: true},
			Source:      SourceCustom,
			Provider:    provider,
		},
		Rank:    rank,
		Locator: name,
	}
}

func mustRegisterProvider(t *testing.T, registry *Registry, owner *scope.Scope, provider Provider) func(context.Context) error {
	t.Helper()
	dispose, err := registry.RegisterProvider(context.Background(), owner,
		func(ProviderControl) (Provider, error) { return provider, nil })
	if err != nil {
		t.Fatalf("注册提供方 %q 失败：%v", provider.Name(), err)
	}
	return dispose
}

func names(summaries []Summary) []string {
	result := make([]string, len(summaries))
	for index, summary := range summaries {
		result[index] = summary.Name
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestNewRegistryValidatesTheCacheBound(t *testing.T) {
	if _, err := NewRegistry(Options{CollectCacheMaxEntries: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("负数上限该被拒掉，拿到 %v", err)
	}
	registry, err := NewRegistry(Options{})
	if err != nil {
		t.Fatalf("空选项该用默认值：%v", err)
	}
	if registry.collectCacheMaxEntries != defaultCollectCacheMaxEntries {
		t.Fatalf("默认上限是 %d", registry.collectCacheMaxEntries)
	}
	if registry.logger == nil {
		t.Fatal("没给日志器时该退回 slog.Default()")
	}
}

func TestEmptyRegistryIsCompleteAndEmpty(t *testing.T) {
	h := newHarness(t, 0)
	snapshot, err := h.registry.Snapshot(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Skills) != 0 || !snapshot.Complete {
		t.Fatalf("空注册表该是一份跑完了的空目录，拿到 %+v", snapshot)
	}
}

func TestRegisterProviderRejectsBadInput(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	if _, err := h.registry.RegisterProvider(ctx, owner, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没给构造函数该被拒，拿到 %v", err)
	}
	if _, err := h.registry.RegisterProvider(ctx, owner,
		func(ProviderControl) (Provider, error) { return nil, nil }); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("构造函数交回 nil 提供方该被拒，拿到 %v", err)
	}

	boom := errors.New("这个提供方造不出来")
	var lifecycle context.Context
	_, err := h.registry.RegisterProvider(ctx, owner, func(control ProviderControl) (Provider, error) {
		lifecycle = control.Context
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("构造失败该原样报出来，拿到 %v", err)
	}
	// 构造失败时那条生命周期必须当场收掉，否则它会永远挂着。
	if !errors.Is(context.Cause(lifecycle), boom) {
		t.Fatalf("生命周期该带着构造失败的原因，拿到 %v", context.Cause(lifecycle))
	}

	_, err = h.registry.RegisterProvider(ctx, owner, func(ProviderControl) (Provider, error) {
		return &fakeProvider{name: runtimeProvider}, nil
	})
	if err == nil {
		t.Fatal(`"runtime" 这个名字是留给运行期注册的，提供方不许占`)
	}
}

func TestRegisterProviderRejectsADuplicateNameInOneLayer(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	mustRegisterProvider(t, h.registry, owner, staticProvider("files"))

	var lifecycle context.Context
	_, err := h.registry.RegisterProvider(context.Background(), owner, func(control ProviderControl) (Provider, error) {
		lifecycle = control.Context
		return staticProvider("files"), nil
	})
	if err == nil {
		t.Fatal("同一层里重名该被拒")
	}
	if context.Cause(lifecycle) == nil {
		t.Fatal("登记失败时那条生命周期也该收掉")
	}
}

func TestProviderLifecycleAndInvalidate(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	var control ProviderControl
	dispose, err := h.registry.RegisterProvider(ctx, owner, func(received ProviderControl) (Provider, error) {
		control = received
		// 注册还没落地时调它是安全的，而且什么都不该发生。
		received.Invalidate()
		return staticProvider("files", candidateOf("files", "alpha", 100)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.Context.Err() != nil {
		t.Fatal("注册成功之后生命周期该还活着")
	}

	before := h.changes.Load()
	control.Invalidate()
	if h.changes.Load() != before+1 {
		t.Fatal("活着的注册调 Invalidate 该通知一次")
	}

	if err := dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(control.Context), context.Canceled) && context.Cause(control.Context) == nil {
		t.Fatal("撤销之后生命周期该带着原因取消")
	}
	after := h.changes.Load()
	control.Invalidate()
	if h.changes.Load() != after {
		t.Fatal("撤销之后再调 Invalidate 什么都不该发生")
	}
}

func TestRegisterRuntimeSkill(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	if _, err := h.registry.Register(ctx, owner, Registration{Name: "BAD", Description: "x"}); !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("非法名字该被拒，拿到 %v", err)
	}
	if _, err := h.registry.Register(ctx, owner, Registration{Name: "alpha"}); !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("空描述该被拒，拿到 %v", err)
	}
	if _, err := h.registry.Register(ctx, nil, Registration{Name: "alpha", Description: "甲"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没给作用域该被拒，拿到 %v", err)
	}

	if _, err := h.registry.Register(ctx, owner, Registration{
		Name: "alpha", Description: "甲", Content: "照这么做", Source: SourceCustom,
	}); err != nil {
		t.Fatal(err)
	}

	summaries, err := h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("该看得见一份技能，拿到 %v", names(summaries))
	}
	// 漏填的 Invocation 是两条都放行，漏填的 Provider 是 runtime。
	if !IsModelInvocable(summaries[0]) || !IsUserInvocable(summaries[0]) {
		t.Fatal("漏填的调用许可该是两条都放行")
	}
	if summaries[0].Provider != runtimeProvider {
		t.Fatalf("漏填的提供方该是 %q，拿到 %q", runtimeProvider, summaries[0].Provider)
	}

	definition, err := h.registry.Get(ctx, "alpha", ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if definition == nil || definition.Content != "照这么做" {
		t.Fatalf("取正文拿到 %+v", definition)
	}
}

func TestRegisterRuntimeSkillHonoursAnExplicitPolicy(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	policy := InvocationPolicy{ModelInvocable: false, UserInvocable: true}
	if _, err := h.registry.Register(context.Background(), owner, Registration{
		Name: "alpha", Description: "甲", Invocation: &policy, Provider: "host",
	}); err != nil {
		t.Fatal(err)
	}
	summaries, err := h.registry.List(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if IsModelInvocable(summaries[0]) || !IsUserInvocable(summaries[0]) {
		t.Fatalf("显式给的许可该原样留着，拿到 %+v", summaries[0].Invocation)
	}
	if summaries[0].Provider != "host" {
		t.Fatalf("显式给的提供方该原样留着，拿到 %q", summaries[0].Provider)
	}
}

func TestDuplicateRuntimeSkillIsFirstWins(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	if _, err := h.registry.Register(ctx, owner, Registration{Name: "alpha", Description: "先来的", Content: "甲"}); err != nil {
		t.Fatal(err)
	}
	dispose, err := h.registry.Register(ctx, owner, Registration{Name: "alpha", Description: "后到的", Content: "乙"})
	if err != nil {
		t.Fatal(err)
	}
	// 后到的拿到的是一个空操作的撤销函数，所以它撤不掉赢家。
	if err := dispose(ctx); err != nil {
		t.Fatal(err)
	}
	definition, err := h.registry.Get(ctx, "alpha", ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if definition == nil || definition.Content != "甲" {
		t.Fatalf("先到的那份该还在，拿到 %+v", definition)
	}
}

func TestScopedLayerShadowsTheGlobalLayer(t *testing.T) {
	h := newHarness(t, 0)
	ctx := context.Background()

	global := scope.NewRoot()
	mustRegisterProvider(t, h.registry, global,
		staticProvider("host", candidateOf("host", "alpha", 100), candidateOf("host", "beta", 100)))

	key := scope.NewKey("preset")
	preset, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mustRegisterProvider(t, h.registry, preset,
		staticProvider("preset", candidateOf("preset", "alpha", 600)))

	// 全局视角只看得见全局层。
	summaries, err := h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(names(summaries), []string{"alpha", "beta"}) {
		t.Fatalf("全局视角拿到 %v", names(summaries))
	}
	if summaries[0].Provider != "host" {
		t.Fatalf("全局视角的 alpha 该来自 host，拿到 %q", summaries[0].Provider)
	}

	// 预设视角里，近的那层整个盖住远的——哪怕它的 rank 更大。
	scoped, err := h.registry.List(ctx, ViewOptions{Scope: key})
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(names(scoped), []string{"alpha", "beta"}) {
		t.Fatalf("预设视角拿到 %v", names(scoped))
	}
	if scoped[0].Provider != "preset" {
		t.Fatalf("预设那层的 alpha 该盖住全局的，拿到 %q", scoped[0].Provider)
	}
}

func TestRankDecidesWithinOneLayer(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()

	// 先注册的 rank 大，后注册的 rank 小——小的赢。
	mustRegisterProvider(t, h.registry, owner, staticProvider("user", candidateOf("user", "alpha", BundledRank)))
	mustRegisterProvider(t, h.registry, owner, staticProvider("project", candidateOf("project", "alpha", 100)))

	summaries, err := h.registry.List(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Provider != "project" {
		t.Fatalf("rank 小的该赢，拿到 %+v", summaries)
	}
}

func TestRegistrationOrderBreaksARankTie(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	mustRegisterProvider(t, h.registry, owner, staticProvider("first", candidateOf("first", "alpha", 100)))
	mustRegisterProvider(t, h.registry, owner, staticProvider("second", candidateOf("second", "alpha", 100)))

	summaries, err := h.registry.List(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].Provider != "first" {
		t.Fatalf("rank 一样时先注册的赢，拿到 %q", summaries[0].Provider)
	}
}

func TestProviderLocalOrderBreaksTheLastTie(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	first := candidateOf("files", "alpha", 100)
	first.WhenToUse = "先报出来的"
	second := candidateOf("files", "alpha", 100)
	second.WhenToUse = "后报出来的"
	mustRegisterProvider(t, h.registry, owner, staticProvider("files", first, second))

	summaries, err := h.registry.List(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].WhenToUse != "先报出来的" {
		t.Fatalf("同一个提供方报重了名字时先报的赢，拿到 %+v", summaries)
	}
}

func TestRuntimeSkillsOutrankProvidersAtTheSameLayerByRank(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	// 打包提供方的 rank 是 600，运行期注册是 250：运行期的赢。
	mustRegisterProvider(t, h.registry, owner, staticProvider("bundled", candidateOf("bundled", "alpha", BundledRank)))
	if _, err := h.registry.Register(ctx, owner, Registration{Name: "alpha", Description: "运行期的", Content: "甲"}); err != nil {
		t.Fatal(err)
	}
	summaries, err := h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].Provider != runtimeProvider {
		t.Fatalf("运行期注册该压住打包提供方，拿到 %q", summaries[0].Provider)
	}

	// 项目那层约定的 rank 更小，它又该压住运行期的。
	mustRegisterProvider(t, h.registry, owner, staticProvider("project", candidateOf("project", "alpha", 100)))
	summaries, err = h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summaries[0].Provider != "project" {
		t.Fatalf("项目里的该压住运行期的，拿到 %q", summaries[0].Provider)
	}
}

func TestRuntimeSkillsAreSortedByName(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()
	for _, name := range []string{"gamma", "alpha", "beta"} {
		if _, err := h.registry.Register(ctx, owner, Registration{Name: name, Description: name}); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(names(summaries), []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("拿到 %v", names(summaries))
	}
}

func TestProviderFailureIsContainedAndUncacheable(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()

	broken := &fakeProvider{
		name: "broken",
		list: func(context.Context, LookupOptions) (Observation, error) {
			return Observation{}, errors.New("远端挂了")
		},
	}
	mustRegisterProvider(t, h.registry, owner, broken)
	mustRegisterProvider(t, h.registry, owner, staticProvider("good", candidateOf("good", "alpha", 100)))

	snapshot, err := h.registry.Snapshot(ctx, ViewOptions{})
	if err != nil {
		t.Fatalf("一个提供方挂了不该让整次读失败：%v", err)
	}
	if !equalStrings(names(snapshot.Skills), []string{"alpha"}) {
		t.Fatalf("别的提供方的技能该照样看得见，拿到 %v", names(snapshot.Skills))
	}
	if snapshot.Complete {
		t.Fatal("少了东西的目录该报不完整")
	}
	// 不完整的发现永远不进缓存，所以下一次读会重新问一遍。
	if _, err := h.registry.Snapshot(ctx, ViewOptions{}); err != nil {
		t.Fatal(err)
	}
	if broken.calls.Load() != 2 {
		t.Fatalf("不完整的目录不该进缓存，提供方被问了 %d 次", broken.calls.Load())
	}
}

func TestIncompleteObservationIsNotCached(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	provider := &fakeProvider{
		name: "partial",
		list: func(context.Context, LookupOptions) (Observation, error) {
			return Observation{Candidates: []Candidate{candidateOf("partial", "alpha", 100)}, Incomplete: true}, nil
		},
	}
	mustRegisterProvider(t, h.registry, owner, provider)

	snapshot, err := h.registry.Snapshot(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Complete {
		t.Fatal("提供方自报没跑完时，这份目录就是不完整的")
	}
	if len(snapshot.Skills) != 1 {
		t.Fatalf("不完整不等于没有，拿到 %v", names(snapshot.Skills))
	}
	if _, err := h.registry.Snapshot(context.Background(), ViewOptions{}); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("提供方被问了 %d 次", provider.calls.Load())
	}
}

func TestCatalogIsCachedUntilARegistrationChanges(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	mustRegisterProvider(t, h.registry, owner, provider)

	for range 3 {
		if _, err := h.registry.List(ctx, ViewOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("跑完的目录该被缓存，提供方被问了 %d 次", provider.calls.Load())
	}

	// 换一个 cwd 就是另一个键。
	if _, err := h.registry.List(ctx, ViewOptions{LookupOptions: LookupOptions{WorkspaceID: "ws-other"}}); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("换 cwd 该重新发现，提供方被问了 %d 次", provider.calls.Load())
	}

	// 任何一次注册变动都把缓存整个清掉。
	if _, err := h.registry.Register(ctx, owner, Registration{Name: "beta", Description: "乙"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.List(ctx, ViewOptions{}); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 {
		t.Fatalf("注册变动之后该重新发现，提供方被问了 %d 次", provider.calls.Load())
	}
}

func TestCacheEvictsTheOldestEntry(t *testing.T) {
	h := newHarness(t, 1)
	owner := scope.NewRoot()
	ctx := context.Background()
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	mustRegisterProvider(t, h.registry, owner, provider)

	for _, workspace := range []sessionlog.WorkspaceID{"ws-a", "ws-b", "ws-a"} {
		if _, err := h.registry.List(ctx, ViewOptions{LookupOptions: LookupOptions{WorkspaceID: workspace}}); err != nil {
			t.Fatal(err)
		}
	}
	// 上限是 1，所以 ws-b 把 ws-a 挤掉了，第三次读 ws-a 又得重新发现一遍。
	if provider.calls.Load() != 3 {
		t.Fatalf("提供方被问了 %d 次", provider.calls.Load())
	}
}

func TestCollectRetriesWhenTheRevisionMovesUnderIt(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	provider := &fakeProvider{name: "churn"}
	var control ProviderControl
	_, err := h.registry.RegisterProvider(context.Background(), owner, func(received ProviderControl) (Provider, error) {
		control = received
		return provider, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 每一次发现都把注册表改一遍，于是 revision 永远对不上。
	provider.list = func(context.Context, LookupOptions) (Observation, error) {
		control.Invalidate()
		return Observation{Candidates: []Candidate{candidateOf("churn", "alpha", 100)}}, nil
	}

	snapshot, err := h.registry.Snapshot(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Complete {
		t.Fatal("发现期间注册表一直在变，这份目录不该算跑完了")
	}
	if provider.calls.Load() != int64(maxCollectAttempts) {
		t.Fatalf("该重来 %d 次，实际问了 %d 次", maxCollectAttempts, provider.calls.Load())
	}
	if len(snapshot.Skills) != 1 {
		t.Fatalf("重来到头之后仍然该把这批交出去，拿到 %v", names(snapshot.Skills))
	}
}

func TestInvalidCandidateFailsTheWholeRead(t *testing.T) {
	cases := []struct {
		name      string
		candidate Candidate
	}{
		{"名字不合法", func() Candidate { c := candidateOf("files", "alpha", 100); c.Name = "BAD"; return c }()},
		{"没有描述", func() Candidate { c := candidateOf("files", "alpha", 100); c.Description = ""; return c }()},
		{"报的提供方对不上", func() Candidate { c := candidateOf("files", "alpha", 100); c.Provider = "别人"; return c }()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, 0)
			mustRegisterProvider(t, h.registry, scope.NewRoot(), staticProvider("files", testCase.candidate))
			if _, err := h.registry.List(context.Background(), ViewOptions{}); !errors.Is(err, ErrInvalidSkill) {
				t.Fatalf("拿到 %v", err)
			}
		})
	}
}

func TestGetRejectsAMalformedName(t *testing.T) {
	h := newHarness(t, 0)
	definition, err := h.registry.Get(context.Background(), "NOT A NAME", ViewOptions{})
	if err != nil || definition != nil {
		t.Fatalf("非法名字该当作没找到，拿到 %+v / %v", definition, err)
	}
}

func TestGetReturnsNothingForAnUnknownName(t *testing.T) {
	h := newHarness(t, 0)
	definition, err := h.registry.Get(context.Background(), "missing", ViewOptions{})
	if err != nil || definition != nil {
		t.Fatalf("拿到 %+v / %v", definition, err)
	}
}

func TestGetPassesTheLocatorBackToTheProvider(t *testing.T) {
	h := newHarness(t, 0)
	var seen any
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	provider.get = func(_ context.Context, candidate Candidate, _ LookupOptions) (*Definition, error) {
		seen = candidate.Locator
		return &Definition{Summary: candidate.Summary, Content: "正文"}, nil
	}
	mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

	if _, err := h.registry.Get(context.Background(), "alpha", ViewOptions{}); err != nil {
		t.Fatal(err)
	}
	if seen != "alpha" {
		t.Fatalf("那份不透明句柄该原样递回去，拿到 %v", seen)
	}
}

func TestGetTreatsAVanishedSkillAsMissing(t *testing.T) {
	h := newHarness(t, 0)
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	provider.get = func(context.Context, Candidate, LookupOptions) (*Definition, error) { return nil, nil }
	mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

	definition, err := h.registry.Get(context.Background(), "alpha", ViewOptions{})
	if err != nil || definition != nil {
		t.Fatalf("交回 nil 表示它没了，不是错误，拿到 %+v / %v", definition, err)
	}
}

func TestGetReportsAProviderFailure(t *testing.T) {
	h := newHarness(t, 0)
	boom := errors.New("读不出来")
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	provider.get = func(context.Context, Candidate, LookupOptions) (*Definition, error) { return nil, boom }
	mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

	if _, err := h.registry.Get(context.Background(), "alpha", ViewOptions{}); !errors.Is(err, boom) {
		t.Fatalf("拿到 %v", err)
	}
}

func TestGetValidatesTheLoadedDefinition(t *testing.T) {
	cases := []struct {
		name       string
		definition Definition
	}{
		{"名字不合法", Definition{Summary: Summary{Name: "BAD", Description: "甲"}}},
		{"没有描述", Definition{Summary: Summary{Name: "alpha"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, 0)
			provider := staticProvider("files", candidateOf("files", "alpha", 100))
			loaded := testCase.definition
			provider.get = func(context.Context, Candidate, LookupOptions) (*Definition, error) { return &loaded, nil }
			mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

			if _, err := h.registry.Get(context.Background(), "alpha", ViewOptions{}); !errors.Is(err, ErrInvalidSkill) {
				t.Fatalf("拿到 %v", err)
			}
		})
	}
}

func TestGetInvalidatesWhenTheLoadedNameDisagrees(t *testing.T) {
	h := newHarness(t, 0)
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	provider.get = func(_ context.Context, candidate Candidate, _ LookupOptions) (*Definition, error) {
		renamed := candidate.Summary
		renamed.Name = "beta"
		return &Definition{Summary: renamed, Content: "正文"}, nil
	}
	mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

	before := h.changes.Load()
	definition, err := h.registry.Get(context.Background(), "alpha", ViewOptions{})
	if err != nil || definition != nil {
		t.Fatalf("名字对不上说明目录过期了，该当作没找到，拿到 %+v / %v", definition, err)
	}
	if h.changes.Load() != before+1 {
		t.Fatal("目录过期该作废缓存并通知一次")
	}
}

func TestStaleEntryDoesNotInvalidateAfterItsRegistrationIsGone(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	provider := staticProvider("files", candidateOf("files", "alpha", 100))
	dispose := mustRegisterProvider(t, h.registry, owner, provider)

	// 一次读正文可以活得比它选中的那次注册还久。这里让读正文的过程中把注册撤掉，
	// 于是那条目录项对应的注册已经不在表里了——此时不该再多作废一次缓存，
	// 因为撤销本身早就把缓存清干净了。
	provider.get = func(_ context.Context, candidate Candidate, _ LookupOptions) (*Definition, error) {
		if err := dispose(context.Background()); err != nil {
			t.Errorf("撤销失败：%v", err)
		}
		renamed := candidate.Summary
		renamed.Name = "beta"
		return &Definition{Summary: renamed, Content: "正文"}, nil
	}

	before := h.changes.Load()
	definition, err := h.registry.Get(context.Background(), "alpha", ViewOptions{})
	if err != nil || definition != nil {
		t.Fatalf("拿到 %+v / %v", definition, err)
	}
	// 只该有撤销自己那一次通知。
	if h.changes.Load() != before+1 {
		t.Fatalf("通知了 %d 次，只该有撤销那一次", h.changes.Load()-before)
	}
}

func TestDisposingAProviderRemovesItsSkills(t *testing.T) {
	h := newHarness(t, 0)
	owner := scope.NewRoot()
	ctx := context.Background()
	dispose := mustRegisterProvider(t, h.registry, owner, staticProvider("files", candidateOf("files", "alpha", 100)))

	if summaries, err := h.registry.List(ctx, ViewOptions{}); err != nil || len(summaries) != 1 {
		t.Fatalf("拿到 %v / %v", summaries, err)
	}
	if err := dispose(ctx); err != nil {
		t.Fatal(err)
	}
	summaries, err := h.registry.List(ctx, ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("撤销之后该一个都看不见，拿到 %v", names(summaries))
	}
}

func TestCollectRefusesAnAlreadyCancelledContext(t *testing.T) {
	h := newHarness(t, 0)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.registry.List(cancelled, ViewOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("拿到 %v", err)
	}
	if _, err := h.registry.Get(cancelled, "alpha", ViewOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("拿到 %v", err)
	}
}

func TestCancellationDuringDiscoveryStopsTheNextLayer(t *testing.T) {
	h := newHarness(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 全局层那个提供方在自己跑完之后把这次读取消掉，于是轮到预设那层时，
	// listLayerCandidates 一进门就该交回取消。
	global := &fakeProvider{
		name: "global",
		list: func(context.Context, LookupOptions) (Observation, error) {
			cancel()
			return Observation{}, nil
		},
	}
	mustRegisterProvider(t, h.registry, scope.NewRoot(), global)

	key := scope.NewKey("preset")
	preset, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatal(err)
	}
	inner := staticProvider("preset", candidateOf("preset", "alpha", 100))
	mustRegisterProvider(t, h.registry, preset, inner)

	if _, err := h.registry.List(ctx, ViewOptions{Scope: key}); !errors.Is(err, context.Canceled) {
		t.Fatalf("拿到 %v", err)
	}
	if inner.calls.Load() != 0 {
		t.Fatal("取消之后不该再去问下一层的提供方")
	}
}

func TestCancellationBeatsAnUncooperativeProvider(t *testing.T) {
	h := newHarness(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	stuck := &fakeProvider{
		name: "stuck",
		list: func(context.Context, LookupOptions) (Observation, error) {
			close(entered)
			<-release
			return Observation{}, nil
		},
	}
	mustRegisterProvider(t, h.registry, scope.NewRoot(), stuck)

	go func() {
		<-entered
		cancel()
	}()
	if _, err := h.registry.List(ctx, ViewOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("一个不合作的提供方不该把调用方吊死，拿到 %v", err)
	}
}

// lateCancel 是一个「被问过 n 次之后才承认自己取消了」的上下文。
//
// 本包在几个步骤**之间**反复查取消，为的是让一次已经取消的读尽早停下来：collect
// 进门时查一次、每一层进门时查一次、每次问提供方之前查一次、跑完发现之后再查一次、
// [Registry.Get] 选完赢家之后还要查一次。这些检查各自守着一小段时间窗，用真的
// context 去命中其中某一段是一场竞态，所以这里把「第几次问」显式摆出来。
//
// 它嵌的是一个**没有**取消的父上下文，于是 Done() 永远不会就绪——
// [waitWithCancel] 里那个 select 因此确定地走提供方返回那一支，而不是和取消赛跑。
// [context.Cause] 对这种不是 cancelCtx 的上下文退回读 Err()，所以原因照样报得出来。
type lateCancel struct {
	context.Context
	skips *atomic.Int64
}

func (c lateCancel) Err() error {
	if c.skips.Add(-1) >= 0 {
		return nil
	}
	return context.Canceled
}

func cancelAfter(probes int64) lateCancel {
	skips := &atomic.Int64{}
	skips.Store(probes)
	return lateCancel{Context: context.Background(), skips: skips}
}

func TestGetRechecksCancellationAfterACacheHit(t *testing.T) {
	h := newHarness(t, 0)
	mustRegisterProvider(t, h.registry, scope.NewRoot(), staticProvider("files", candidateOf("files", "alpha", 100)))
	// 先把缓存捂热，这样接下来那次 Get 里的 collect 会直接命中，
	// 于是只剩「collect 进门」和「选完赢家之后」这两次查。
	if _, err := h.registry.List(context.Background(), ViewOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Get(cancelAfter(1), "alpha", ViewOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("拿到 %v", err)
	}
}

func TestCancellationIsRecheckedBetweenSteps(t *testing.T) {
	// 一层、一个提供方时，一次读依次查这几处取消：
	//   1 collect 进门 → 2 这一层进门 → 3 问提供方之前 → 4 跑完发现之后。
	cases := []struct {
		name   string
		probes int64
	}{
		{"这一层进门时", 1},
		{"问提供方之前", 2},
		{"跑完发现之后", 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, 0)
			provider := staticProvider("files", candidateOf("files", "alpha", 100))
			mustRegisterProvider(t, h.registry, scope.NewRoot(), provider)

			if _, err := h.registry.List(cancelAfter(testCase.probes), ViewOptions{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("拿到 %v", err)
			}
			// 取消之后不该留下缓存，否则下一次读会拿到一份半截的目录。
			if _, err := h.registry.List(context.Background(), ViewOptions{}); err != nil {
				t.Fatal(err)
			}
			if provider.calls.Load() == 0 && testCase.probes > 2 {
				t.Fatal("这一轮该已经问过提供方了")
			}
		})
	}
}

func TestRuntimeSkillOnAScopedOwner(t *testing.T) {
	h := newHarness(t, 0)
	ctx := context.Background()
	key := scope.NewKey("preset")
	preset, err := scope.New(key, scope.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.registry.Register(ctx, preset, Registration{Name: "alpha", Description: "甲", Content: "正文"}); err != nil {
		t.Fatal(err)
	}
	// 同名的第二份仍然是先到先得，走的是「这个作用域自己那一层」那条 Peek。
	if _, err := h.registry.Register(ctx, preset, Registration{Name: "alpha", Description: "乙", Content: "别的"}); err != nil {
		t.Fatal(err)
	}

	if summaries, err := h.registry.List(ctx, ViewOptions{}); err != nil || len(summaries) != 0 {
		t.Fatalf("全局视角不该看得见预设那层的技能，拿到 %v / %v", summaries, err)
	}
	// 连读两次同一个视角，缓存键里那个作用域编号该是同一个。
	for range 2 {
		summaries, err := h.registry.List(ctx, ViewOptions{Scope: key})
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(names(summaries), []string{"alpha"}) {
			t.Fatalf("拿到 %v", names(summaries))
		}
	}
	if len(h.registry.scopeIDs) != 1 {
		t.Fatalf("同一个作用域该只发一个编号，发了 %d 个", len(h.registry.scopeIDs))
	}
}

func TestInvalidateEntryIgnoresARuntimeEntry(t *testing.T) {
	// 运行期技能没有提供方注册可言，所以一条它的目录项永远作废不了缓存。
	// 这条支在 [Registry.Get] 那边走不到（运行期技能读出来的名字必然对得上），
	// 但它是 invalidateEntry 的前提，直接验。
	h := newHarness(t, 0)
	before := h.changes.Load()
	h.registry.invalidateEntry(indexedCandidate{})
	if h.changes.Load() != before {
		t.Fatal("没有提供方注册的目录项不该作废缓存")
	}
}

func TestRuntimeSkillProviderSurface(t *testing.T) {
	provider := runtimeSkillProvider{}
	if provider.Name() != runtimeProvider {
		t.Fatalf("拿到 %q", provider.Name())
	}
	// 运行期技能是注册表自己塞进候选里的，这个提供方只负责 Get。
	observation, err := provider.List(context.Background(), LookupOptions{})
	if err != nil || len(observation.Candidates) != 0 {
		t.Fatalf("拿到 %+v / %v", observation, err)
	}
	if _, err := provider.Get(context.Background(), Candidate{Locator: "不是定义"}, LookupOptions{}); !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("拿到 %v", err)
	}
}

func TestLayerDuplicateDiagnosticsNameTheScope(t *testing.T) {
	// 全局层和作用域层的重名措辞不一样，因为处理办法不一样：前者是撞了整个进程，
	// 后者只是撞了这一个预设。
	global := newLayer(nil)
	if _, err := global.providers.Insert("files", &registration{}); err != nil {
		t.Fatal(err)
	}
	_, err := global.providers.Insert("files", &registration{})
	if err == nil || strings.Contains(err.Error(), "in this scope") {
		t.Fatalf("全局层的措辞不该提作用域，拿到 %v", err)
	}

	scoped := newLayer(scope.NewKey("preset"))
	if _, err := scoped.providers.Insert("files", &registration{}); err != nil {
		t.Fatal(err)
	}
	_, err = scoped.providers.Insert("files", &registration{})
	if err == nil || !strings.Contains(err.Error(), "in this scope") {
		t.Fatalf("作用域层的措辞该提作用域，拿到 %v", err)
	}

	// 运行期技能重名的措辞不分层：那条路上重名根本走不到 Insert，
	// [Registry.Register] 先用 Peek 挡住了。
	if _, err := scoped.runtime.Insert("alpha", &Definition{}); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.runtime.Insert("alpha", &Definition{}); err == nil {
		t.Fatal("同一层里的运行期技能重名该被拒")
	}
}

func TestFreshLayerIsEmpty(t *testing.T) {
	if !newLayer(nil).IsEmpty() {
		t.Fatal("一层刚建出来就该是空的")
	}
}
