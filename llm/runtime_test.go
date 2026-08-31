// 本文件的作用：验运行时那三张表——适配器注册表、可配置提供方目录、模型发现，
// 以及挂在它们上面的观察与拦截。流式那一半在 runtimestream_test.go。

package llm

import (
	"context"
	"errors"
	"iter"
	"testing"

	"ds-harness-go/core/scope"
)

// codeOf 取出一条错误挂着的失败码；不是本包的 Error 时让用例失败。
func codeOf(t *testing.T, err error) string {
	t.Helper()
	var carrier *Error
	if !errors.As(err, &carrier) {
		t.Fatalf("该是一条本包的 Error，得到 %v", err)
	}
	return carrier.Failure.Code
}

// ---- 登记与拓扑 ----

// TestRegisterAdapterListsInRegistrationOrder 钉住 ListProviders 按登记次序。
// Go 的 map 不保任何顺序，所以那份次序数组是这条性质唯一的来源。
func TestRegisterAdapterListsInRegistrationOrder(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"乙", "甲"}, &fakeAdapter{}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"丙"}, &fakeAdapter{}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	providers := runtime.ListProviders()
	if len(providers) != 3 ||
		providers[0].ID != "乙" || providers[1].ID != "甲" || providers[2].ID != "丙" {
		t.Fatalf("次序不对：%+v", providers)
	}
}

// TestRegisterAdapterRejections 走一遍登记那几种拒绝。
func TestRegisterAdapterRejections(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterAdapter(t.Context(), nil, []string{"甲"}, &fakeAdapter{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域该被拒，得到 %v", err)
	}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"甲"}, nil); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("没有适配器该报 INVALID_ADAPTER，得到 %v", err)
	}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, nil, &fakeAdapter{}); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("一条路由都没有该报 INVALID_ADAPTER，得到 %v", err)
	}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{""}, &fakeAdapter{}); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("空路由名该报 INVALID_ADAPTER，得到 %v", err)
	}

	// 元数据不合格：ID 被改掉。
	liar := &fakeAdapter{providerInfo: func(string) ProviderInfo {
		return ProviderInfo{ID: "别的", Name: "别的"}
	}}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"甲"}, liar); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("路由 id 被改掉该报 INVALID_ADAPTER，得到 %v", err)
	}
	// 元数据不合格：名字是空的。
	nameless := &fakeAdapter{providerInfo: func(provider string) ProviderInfo {
		return ProviderInfo{ID: provider}
	}}
	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"甲"}, nameless); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("空名字该报 INVALID_ADAPTER，得到 %v", err)
	}
}

// TestRegisterAdapterIsAllOrNothing 钉住那句「全有或者全无」：一条路由撞车，
// 整次登记失败，同一批里前面那些也一条都不留下。
func TestRegisterAdapterIsAllOrNothing(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	_, err := runtime.RegisterAdapter(t.Context(), owner, []string{"乙", "甲"}, &fakeAdapter{})
	if codeOf(t, err) != DuplicateAdapterCode {
		t.Fatalf("撞车该报 DUPLICATE_ADAPTER，得到 %v", err)
	}
	if len(runtime.ListProviders()) != 1 {
		t.Fatalf("失败的那次登记该一条路由都不留下，得到 %+v", runtime.ListProviders())
	}

	// 同一批里自己和自己重复，判据一样。
	_, err = runtime.RegisterAdapter(t.Context(), owner, []string{"丙", "丙"}, &fakeAdapter{})
	if codeOf(t, err) != DuplicateAdapterCode {
		t.Fatalf("同批重复该报 DUPLICATE_ADAPTER，得到 %v", err)
	}
}

// TestAdapterRegistrationReplace 走一遍换名单：换成功之后旧的没了新的在，
// 空名单合法，撞车时原样留着当下那份。
func TestAdapterRegistrationReplace(t *testing.T) {
	runtime := newTestRuntime(t)
	handle := registerFake(t, runtime, "甲", &fakeAdapter{})
	registerFake(t, runtime, "占着的", &fakeAdapter{})

	if err := handle.Replace([]string{"乙", "丙"}); err != nil {
		t.Fatalf("换名单失败：%v", err)
	}
	if _, err := runtime.ProviderRetryPolicy("甲"); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("旧路由该没了，得到 %v", err)
	}
	if _, err := runtime.ProviderRetryPolicy("丙"); err != nil {
		t.Fatalf("新路由该在：%v", err)
	}

	if err := handle.Replace([]string{"丁", "占着的"}); codeOf(t, err) != DuplicateAdapterCode {
		t.Fatalf("撞车该报 DUPLICATE_ADAPTER，得到 %v", err)
	}
	if _, err := runtime.ProviderRetryPolicy("丙"); err != nil {
		t.Fatalf("被拒之后该原样留着当下那份名单：%v", err)
	}
	if _, err := runtime.ProviderRetryPolicy("丁"); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("被拒的那批一条都不该进去，得到 %v", err)
	}

	// 空名单合法：一次活着但一条路由都没有的登记。
	if err := handle.Replace(nil); err != nil {
		t.Fatalf("空名单该合法：%v", err)
	}
	if len(runtime.ListProviders()) != 1 {
		t.Fatalf("该只剩那条占着的，得到 %+v", runtime.ListProviders())
	}
}

// TestAdapterRegistrationReleaseThenReplace 钉住已经释放的登记不许再往回放东西：
// 它的释放已经跑过，此刻放进去的东西不会有人负责摘出来。
func TestAdapterRegistrationReleaseThenReplace(t *testing.T) {
	runtime := newTestRuntime(t)
	handle := registerFake(t, runtime, "甲", &fakeAdapter{})

	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	if len(runtime.ListProviders()) != 0 {
		t.Fatalf("释放之后该一条都不剩，得到 %+v", runtime.ListProviders())
	}
	// 重复释放没有额外效果。
	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("重复释放不该报错：%v", err)
	}
	if err := handle.Replace([]string{"乙"}); codeOf(t, err) != RegistrationDisposedCode {
		t.Fatalf("该报 REGISTRATION_DISPOSED，得到 %v", err)
	}
}

// TestAdapterRegistrationFollowsOwnerScope 钉住 owner 释放时登记跟着走——
// 这是本仓库每一处登记的共同条款，也是「谁登记谁负责摘」在 Go 里的落实处。
func TestAdapterRegistrationFollowsOwnerScope(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := scope.NewRoot()

	if _, err := runtime.RegisterAdapter(t.Context(), owner, []string{"甲"}, &fakeAdapter{}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := owner.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if len(runtime.ListProviders()) != 0 {
		t.Fatalf("作用域释放之后该一条都不剩，得到 %+v", runtime.ListProviders())
	}
}

// TestOnAdaptersUpdatedFires 钉住每一次已提交的拓扑改动都会通知到，
// 而且撤销登记之后不再通知。
func TestOnAdaptersUpdatedFires(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	fired := 0
	undo, err := runtime.OnAdaptersUpdated(t.Context(), owner, func() { fired++ })
	if err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	handle := registerFake(t, runtime, "甲", &fakeAdapter{})
	if fired != 1 {
		t.Fatalf("登记该通知一次，得到 %d", fired)
	}
	if err := handle.Replace([]string{"乙"}); err != nil {
		t.Fatalf("换名单失败：%v", err)
	}
	if fired != 2 {
		t.Fatalf("换名单该通知一次，得到 %d", fired)
	}
	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	if fired != 3 {
		t.Fatalf("释放该通知一次，得到 %d", fired)
	}

	if err := undo(t.Context()); err != nil {
		t.Fatalf("撤销观察者失败：%v", err)
	}
	registerFake(t, runtime, "丙", &fakeAdapter{})
	if fired != 3 {
		t.Fatalf("撤销之后不该再通知，得到 %d", fired)
	}
}

// TestOnAdaptersUpdatedSurvivesAPanickingObserver 钉住那句「没有否决权」：
// 一个观察者炸了，改动照样已经提交，别的观察者照样跑到。
func TestOnAdaptersUpdatedSurvivesAPanickingObserver(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	reached := false
	if _, err := runtime.OnAdaptersUpdated(t.Context(), owner, func() {
		panic("这个观察者坏了")
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := runtime.OnAdaptersUpdated(t.Context(), owner, func() { reached = true }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	registerFake(t, runtime, "甲", &fakeAdapter{})
	if !reached {
		t.Fatal("前一个观察者炸了不该拦住后一个")
	}
	if len(runtime.ListProviders()) != 1 {
		t.Fatal("改动已经提交，一个坏观察者否决不了它")
	}
}

// TestRegistrationRequiresNonNilCallbacks 钉住两个登记入口都不收 nil，以及
// 都要一个持有它的作用域。
func TestRegistrationRequiresNonNilCallbacks(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.OnAdaptersUpdated(t.Context(), owner, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil 观察者该被拒，得到 %v", err)
	}
	if _, err := runtime.OnAdaptersUpdated(t.Context(), nil, func() {}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域该被拒，得到 %v", err)
	}
	if _, err := runtime.OnStream(t.Context(), owner, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil 规则该被拒，得到 %v", err)
	}
	if _, err := runtime.OnStream(t.Context(), nil, passthroughRule); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域该被拒，得到 %v", err)
	}
}

// TestAttachOnDisposedScopeUndoesItsRegistration 钉住 attach 那条「挂不上去就把刚
// 登记的撤掉」：静默接受的话这份登记永远没人负责撤销，也就是泄漏了。
func TestAttachOnDisposedScopeUndoesItsRegistration(t *testing.T) {
	runtime := newTestRuntime(t)
	dead := scope.NewRoot()
	if err := dead.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}

	if _, err := runtime.OnAdaptersUpdated(t.Context(), dead, func() {}); err == nil {
		t.Fatal("往一个已释放的作用域上挂该失败")
	}
	// 观察者没留下：再发一次通知不该炸也不该有人接到。
	registerFake(t, runtime, "甲", &fakeAdapter{})

	if _, err := runtime.RegisterAdapter(t.Context(), dead, []string{"乙"}, &fakeAdapter{}); err == nil {
		t.Fatal("往一个已释放的作用域上登记适配器该失败")
	}
	for _, info := range runtime.ListProviders() {
		if info.ID == "乙" {
			t.Fatal("挂不上去的那次登记该把路由撤回去")
		}
	}
}

// passthroughRule 是一条什么都不做、直接往里走的瀑布规则。
func passthroughRule(
	ctx context.Context,
	_ GenerateOptions,
	next func(context.Context) (iter.Seq2[StreamChunk, error], error),
) (iter.Seq2[StreamChunk, error], error) {
	return next(ctx)
}

// ---- 可配置提供方目录 ----

// sampleEntry 造一条最小的合法目录条目。
func sampleEntry(provider string) ConfigurableProvider {
	return ConfigurableProvider{
		Provider:     provider,
		DisplayName:  provider,
		SettingsNs:   "llm",
		SettingsPath: []string{"providers", provider},
	}
}

// TestConfigurableProvidersListInDeclarationOrder 钉住目录按声明次序，以及
// 交出来的是复制品——改到手上那份不许回流进目录。
func TestConfigurableProvidersListInDeclarationOrder(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("乙"), sampleEntry("甲")}); err != nil {
		t.Fatalf("声明失败：%v", err)
	}

	entries := runtime.ListConfigurableProviders()
	if len(entries) != 2 || entries[0].Provider != "乙" || entries[1].Provider != "甲" {
		t.Fatalf("次序不对：%+v", entries)
	}
	entries[0].SettingsPath[0] = "改过的"
	if runtime.ListConfigurableProviders()[0].SettingsPath[0] != "providers" {
		t.Fatal("交出去的该是复制品，改它不该回流进目录")
	}
}

// TestConfigurableProvidersRejections 走一遍目录那几种拒绝。
func TestConfigurableProvidersRejections(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterConfigurableProviders(t.Context(), nil,
		[]ConfigurableProvider{sampleEntry("甲")}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域该被拒，得到 %v", err)
	}
	if _, err := runtime.RegisterConfigurableProviders(t.Context(), owner, nil); codeOf(t, err) != InvalidDirectoryCode {
		t.Fatalf("空清单该报 INVALID_DIRECTORY，得到 %v", err)
	}

	cases := []struct {
		name  string
		entry ConfigurableProvider
	}{
		{name: "没有路由名", entry: ConfigurableProvider{DisplayName: "甲", SettingsNs: "llm"}},
		{name: "没有显示名", entry: ConfigurableProvider{Provider: "甲", SettingsNs: "llm"}},
		{name: "没有命名空间", entry: ConfigurableProvider{Provider: "甲", DisplayName: "甲"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
				[]ConfigurableProvider{testCase.entry})
			if codeOf(t, err) != InvalidDirectoryCode {
				t.Fatalf("该报 INVALID_DIRECTORY，得到 %v", err)
			}
		})
	}

	blank := sampleEntry("甲")
	blank.SettingsPath = []string{"providers", ""}
	if _, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{blank}); codeOf(t, err) != InvalidDirectoryCode {
		t.Fatalf("空的路径段该报 INVALID_DIRECTORY，得到 %v", err)
	}
}

// TestConfigurableProvidersAreAllOrNothing 钉住目录那一侧同样是全有或者全无，
// 而且同一批里自己和自己重复也算撞车。
func TestConfigurableProvidersAreAllOrNothing(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("甲")}); err != nil {
		t.Fatalf("声明失败：%v", err)
	}
	_, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("乙"), sampleEntry("甲")})
	if codeOf(t, err) != DuplicateDirectoryCode {
		t.Fatalf("撞车该报 DUPLICATE_DIRECTORY，得到 %v", err)
	}
	if len(runtime.ListConfigurableProviders()) != 1 {
		t.Fatalf("失败的那次声明该一条都不留下，得到 %+v", runtime.ListConfigurableProviders())
	}

	_, err = runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("丙"), sampleEntry("丙")})
	if codeOf(t, err) != DuplicateDirectoryCode {
		t.Fatalf("同批重复该报 DUPLICATE_DIRECTORY，得到 %v", err)
	}
}

// TestDirectoryRegistrationReplaceAndRelease 走一遍目录那一侧的换与撤：
// 换掉自己攥着的那些不算撞车，撤销之后再换报 REGISTRATION_DISPOSED。
func TestDirectoryRegistrationReplaceAndRelease(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	handle, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("甲"), sampleEntry("乙")})
	if err != nil {
		t.Fatalf("声明失败：%v", err)
	}

	// 自己攥着的那条留在新名单里，不该被判成撞车。
	if err := handle.Replace([]ConfigurableProvider{sampleEntry("甲"), sampleEntry("丙")}); err != nil {
		t.Fatalf("换名单失败：%v", err)
	}
	entries := runtime.ListConfigurableProviders()
	if len(entries) != 2 || entries[0].Provider != "甲" || entries[1].Provider != "丙" {
		t.Fatalf("换完之后不对：%+v", entries)
	}

	if err := handle.Replace(nil); err != nil {
		t.Fatalf("空名单该合法：%v", err)
	}
	if len(runtime.ListConfigurableProviders()) != 0 {
		t.Fatal("换成空名单之后目录该空了")
	}

	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("重复撤销不该报错：%v", err)
	}
	if err := handle.Replace([]ConfigurableProvider{sampleEntry("丁")}); codeOf(t, err) != RegistrationDisposedCode {
		t.Fatalf("该报 REGISTRATION_DISPOSED，得到 %v", err)
	}
}

// TestDirectoryRegistrationFollowsOwnerScope 钉住目录声明也跟着 owner 走。
func TestDirectoryRegistrationFollowsOwnerScope(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := scope.NewRoot()

	if _, err := runtime.RegisterConfigurableProviders(t.Context(), owner,
		[]ConfigurableProvider{sampleEntry("甲")}); err != nil {
		t.Fatalf("声明失败：%v", err)
	}
	if err := owner.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if len(runtime.ListConfigurableProviders()) != 0 {
		t.Fatal("作用域释放之后目录该空了")
	}

	dead := scope.NewRoot()
	if err := dead.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if _, err := runtime.RegisterConfigurableProviders(t.Context(), dead,
		[]ConfigurableProvider{sampleEntry("乙")}); err == nil {
		t.Fatal("往一个已释放的作用域上声明该失败")
	}
	if len(runtime.ListConfigurableProviders()) != 0 {
		t.Fatal("挂不上去的那次声明该把条目撤回去")
	}
}

// ---- 模型发现 ----

// TestRegisterModelDiscoveryRejections 走一遍发现登记那几种拒绝。
func TestRegisterModelDiscoveryRejections(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)
	discover := func(context.Context, ModelDiscoveryRequest) ([]DiscoveredModel, error) { return nil, nil }

	if _, err := runtime.RegisterModelDiscovery(t.Context(), nil, "llm", discover); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没有作用域该被拒，得到 %v", err)
	}
	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "", discover); codeOf(t, err) != InvalidDiscoveryCode {
		t.Fatalf("空命名空间该报 INVALID_DISCOVERY，得到 %v", err)
	}
	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm", nil); codeOf(t, err) != InvalidDiscoveryCode {
		t.Fatalf("nil 函数该报 INVALID_DISCOVERY，得到 %v", err)
	}

	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm", discover); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm", discover); codeOf(t, err) != DuplicateDiscoveryCode {
		t.Fatalf("重复登记该报 DUPLICATE_DISCOVERY，得到 %v", err)
	}

	dead := scope.NewRoot()
	if err := dead.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if _, err := runtime.RegisterModelDiscovery(t.Context(), dead, "另一个", discover); err == nil {
		t.Fatal("往一个已释放的作用域上登记该失败")
	}
	// 挂不上去的那次该把登记撤回去，所以同一个命名空间还能再登记一次。
	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "另一个", discover); err != nil {
		t.Fatalf("撤回之后该能重新登记：%v", err)
	}
}

// TestDiscoverModelsDeduplicatesAndDropsBlankIDs 钉住答复那一遍过滤：没有 id 的
// 条目和重复的 id 都丢掉。界面要拿这份清单让人点，一个没有 id 的条目点不了，
// 两个同 id 的条目点哪个都一样。
func TestDiscoverModelsDeduplicatesAndDropsBlankIDs(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm",
		func(context.Context, ModelDiscoveryRequest) ([]DiscoveredModel, error) {
			return []DiscoveredModel{
				{ID: "m-1", Name: "第一份"},
				{ID: ""},
				{ID: "m-1", Name: "重复的"},
				{ID: "m-2"},
			}, nil
		}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	models, err := runtime.DiscoverModels(t.Context(), "llm", ModelDiscoveryRequest{Provider: "acme"})
	if err != nil {
		t.Fatalf("问询失败：%v", err)
	}
	if len(models) != 2 || models[0].ID != "m-1" || models[0].Name != "第一份" || models[1].ID != "m-2" {
		t.Fatalf("过滤结果不对：%+v", models)
	}
}

// TestDiscoverModelsRejections 走一遍问询那几种拒绝：没登记过、没有问询对象、
// 以及发现函数自己报的错原样带出来。
func TestDiscoverModelsRejections(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	if _, err := runtime.DiscoverModels(t.Context(), "llm", ModelDiscoveryRequest{Provider: "acme"}); codeOf(t, err) != NoDiscoveryCode {
		t.Fatalf("没登记过该报 NO_DISCOVERY，得到 %v", err)
	}

	boom := errors.New("端点不答话")
	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm",
		func(context.Context, ModelDiscoveryRequest) ([]DiscoveredModel, error) {
			return nil, boom
		}); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	if _, err := runtime.DiscoverModels(t.Context(), "llm", ModelDiscoveryRequest{}); codeOf(t, err) != InvalidDiscoveryCode {
		t.Fatalf("既没路由又没端点该报 INVALID_DISCOVERY，得到 %v", err)
	}
	if _, err := runtime.DiscoverModels(t.Context(), "llm",
		ModelDiscoveryRequest{BaseURL: "https://example.test"}); !errors.Is(err, boom) {
		t.Fatalf("发现函数的失败该原样带出来，得到 %v", err)
	}
}

// TestModelDiscoveryFollowsOwnerScope 钉住发现登记也跟着 owner 走。
func TestModelDiscoveryFollowsOwnerScope(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := scope.NewRoot()

	if _, err := runtime.RegisterModelDiscovery(t.Context(), owner, "llm",
		func(context.Context, ModelDiscoveryRequest) ([]DiscoveredModel, error) { return nil, nil }); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := owner.Dispose(t.Context()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if _, err := runtime.DiscoverModels(t.Context(), "llm",
		ModelDiscoveryRequest{Provider: "acme"}); codeOf(t, err) != NoDiscoveryCode {
		t.Fatalf("作用域释放之后该没有发现了，得到 %v", err)
	}
}
