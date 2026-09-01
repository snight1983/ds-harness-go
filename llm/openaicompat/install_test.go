// 本文件验装配那一层：一次装配在注册表上留下什么、一次设置提交怎么把那几处登记
// 对齐过去，以及一次拆除有没有把它们都收回去。
//
// 这里起的是真的 [llm.Runtime] 和真的 [settings.Provider]（配一个内存后端），
// 因为要考的恰恰是「这几次登记在注册表眼里长什么样」——换成打桩，验的就成了
// 我自己写的那个桩。

package openaicompat

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/credentials"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/settings"
)

// memorySettings 是一个把整份文档记在内存里的设置后端。
//
// settings 包自己那个只在它的用例里存在（不导出），而这一层要考的是「提交之后
// 观察者有没有被叫醒」，所以只需要一个收得下写的后端，不需要真的落盘。
type memorySettings struct {
	document map[string]any
}

func (b *memorySettings) Writable() bool { return true }

func (b *memorySettings) Load(context.Context) (map[string]any, error) {
	// 交出去的必须是脱钩的一份，见 [settings.Backend].Load。
	return maps.Clone(b.document), nil
}

func (b *memorySettings) Persist(_ context.Context, ns settings.Namespace, section map[string]any) error {
	if b.document == nil {
		b.document = map[string]any{}
	}
	b.document[string(ns)] = section
	return nil
}

// quietLogger 是一个什么都不记的 logger。
//
// 装配那几条「一次更新被拒」的诊断本来就是用例故意造出来的，让它们打到测试输出上
// 只会把真正的失败埋掉。
func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// harness 是一次装配连同它周围那几样东西。
type harness struct {
	runtime  *llm.Runtime
	owner    *scope.Scope
	settings *settings.Provider
	teardown func(context.Context) error
}

// install 装一次，用例结束时拆掉。
//
// withSettings 为假时这次装配没有设置面：装配那一份配置就是最终答案。
func install(t *testing.T, config Config, withSettings bool, mutate ...func(*Options)) *harness {
	t.Helper()
	kit := &harness{runtime: llm.NewRuntime(llm.RuntimeOptions{Logger: quietLogger()}), owner: scope.NewRoot()}
	options := Options{
		Runtime: kit.runtime,
		Owner:   kit.owner,
		Config:  config,
		Logger:  quietLogger(),
	}
	if withSettings {
		provider, err := settings.New(t.Context(), &memorySettings{}, quietLogger())
		if err != nil {
			t.Fatalf("起不了设置服务：%v", err)
		}
		kit.settings = provider
		options.Settings = provider
	}
	for _, apply := range mutate {
		apply(&options)
	}
	teardown, err := Install(t.Context(), options)
	if err != nil {
		t.Fatalf("这次装配本该成功：%v", err)
	}
	kit.teardown = teardown
	t.Cleanup(func() { _ = teardown(context.Background()) })
	return kit
}

// commit 把一整段用户设置写下去。
//
// 用 Replace 而不是 Update：这一层的用例要的是「此刻这个命名空间就是这些路由」，
// 而一次补丁式的合并留不住「把某条路由删掉」这种意思。
func (h *harness) commit(t *testing.T, section map[string]any) {
	t.Helper()
	if err := h.settings.Replace(t.Context(), SettingsNamespace, section, nil); err != nil {
		t.Fatalf("提交设置失败：%v", err)
	}
}

// routes 交出注册表此刻认得的那些路由键。
func (h *harness) routes() []string {
	var ids []string
	for _, info := range h.runtime.ListProviders() {
		ids = append(ids, info.ID)
	}
	return ids
}

// section 造一段只有一条路由的用户设置。
func section(provider string, profile map[string]any) map[string]any {
	return map[string]any{"providers": map[string]any{provider: profile}}
}

// routeJSON 造一条最小的、写成原始形状的路由。
func routeJSON() map[string]any {
	return map[string]any{
		"baseURL": "https://gateway.example/v1",
		"models":  []any{map[string]any{"id": "m"}},
	}
}

// TestInstallRejectsMissingWiring 验少了必填接线时当场拒，而不是装出一个半截的装配。
func TestInstallRejectsMissingWiring(t *testing.T) {
	if _, err := Install(t.Context(), Options{Owner: scope.NewRoot()}); err == nil {
		t.Error("没有 Runtime 的装配本该被拒")
	} else if !strings.Contains(err.Error(), "Runtime") {
		t.Errorf("诊断没点名缺的是什么：%v", err)
	}
	// 宿主作用域在这里拒，而不是等 scope 那一层报它自己的规矩：那句话说的是
	// scope 包内部的事，看到它的人还得先弄明白这次登记的宿主是从哪儿来的。
	if _, err := Install(t.Context(), Options{Runtime: llm.NewRuntime(llm.RuntimeOptions{})}); err == nil {
		t.Error("没有 Owner 的装配本该被拒")
	} else if !strings.Contains(err.Error(), "Owner") {
		t.Errorf("诊断没点名缺的是什么：%v", err)
	}
}

// TestInstallRejectsUnserviceableAssemblyConfig 验装配那一份配置在这里就先解算一次。
//
// 留到设置登记那一步才报的话，诊断会指向「存下来的段解不开」，而毛病其实在
// 装配参数里——那是两拨完全不同的人去改的东西。
func TestInstallRejectsUnserviceableAssemblyConfig(t *testing.T) {
	runtime := llm.NewRuntime(llm.RuntimeOptions{Logger: quietLogger()})
	_, err := Install(t.Context(), Options{
		Runtime: runtime,
		Owner:   scope.NewRoot(),
		Logger:  quietLogger(),
		Config:  Config{Providers: map[string]ProviderProfile{"acme": {Models: []ModelProfile{{ID: "m"}}}}},
	})
	if err == nil {
		t.Fatal("一份服务不了的装配配置本该被拒")
	}
	if len(runtime.ListProviders()) != 0 || len(runtime.ListConfigurableProviders()) != 0 {
		t.Error("一次失败的装配在注册表上留下了东西")
	}
}

// TestInstallDormantRegistersOnlyDiscovery 验一次没有路由的裸装配什么都不登记，
// 但仍然提供模型问询。
//
// 那正是「先问出这个端点有哪些模型、再据此写下第一条路由」的场景：问询按整个
// 命名空间登记，和路由集合无关。
func TestInstallDormantRegistersOnlyDiscovery(t *testing.T) {
	kit := install(t, Config{}, false)
	if routes := kit.routes(); len(routes) != 0 {
		t.Errorf("裸装配登记了路由：%v", routes)
	}
	if entries := kit.runtime.ListConfigurableProviders(); len(entries) != 0 {
		t.Errorf("裸装配公告了可配置提供方：%v", entries)
	}

	server := listingServer(t, http.StatusOK, `{"data":[{"id":"m"}]}`, nil)
	models, err := kit.runtime.DiscoverModels(t.Context(), string(SettingsNamespace),
		llm.ModelDiscoveryRequest{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("裸装配本该照样问得了模型：%v", err)
	}
	if len(models) != 1 || models[0].ID != "m" {
		t.Errorf("问回来的候选不对：%+v", models)
	}
}

// TestInstallRegistersConfiguredRoutes 验装配那一份配置里的路由两处都登记上了。
func TestInstallRegistersConfiguredRoutes(t *testing.T) {
	profile := minimalProfile()
	profile.DisplayName = "Acme Gateway"
	kit := install(t, Config{Providers: map[string]ProviderProfile{"acme": profile}}, false)

	providers := kit.runtime.ListProviders()
	if len(providers) != 1 || providers[0].ID != "acme" {
		t.Fatalf("路由没登记上：%+v", providers)
	}
	if providers[0].Name != "Acme Gateway" {
		t.Errorf("显示名没送到注册表：%q", providers[0].Name)
	}

	entries := kit.runtime.ListConfigurableProviders()
	if len(entries) != 1 {
		t.Fatalf("目录条数不对：%+v", entries)
	}
	entry := entries[0]
	if entry.Provider != "acme" || entry.DisplayName != "Acme Gateway" {
		t.Errorf("目录条目不对：%+v", entry)
	}
	// 设置地址必须指到**这个包**的命名空间上，不然配置界面编不了这条路由。
	if entry.SettingsNs != string(SettingsNamespace) {
		t.Errorf("目录条目的设置命名空间不对：%q", entry.SettingsNs)
	}
	if len(entry.SettingsPath) != 2 || entry.SettingsPath[0] != "providers" || entry.SettingsPath[1] != "acme" {
		t.Errorf("目录条目的设置路径不对：%v", entry.SettingsPath)
	}
	// 这个包不带内置目录，所以每一条路由都是声明来的。
	if entry.Declared == nil || !*entry.Declared {
		t.Errorf("目录条目该标成声明来的：%v", entry.Declared)
	}
}

// TestTeardownUnwindsEverything 验一次拆除把三处登记都收回去，而且重复调不出事。
//
// 拆到一半留下的路由会一直挂在选择器上，点下去打到一个已经没人管的适配器。
func TestTeardownUnwindsEverything(t *testing.T) {
	kit := install(t, Config{Providers: map[string]ProviderProfile{"acme": minimalProfile()}}, false)
	if len(kit.routes()) != 1 {
		t.Fatal("装配没把路由登记上")
	}
	if err := kit.teardown(t.Context()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	if routes := kit.routes(); len(routes) != 0 {
		t.Errorf("拆完之后路由还在：%v", routes)
	}
	if entries := kit.runtime.ListConfigurableProviders(); len(entries) != 0 {
		t.Errorf("拆完之后目录还在：%+v", entries)
	}
	_, err := kit.runtime.DiscoverModels(t.Context(), string(SettingsNamespace),
		llm.ModelDiscoveryRequest{BaseURL: "https://gateway.example/v1"})
	if code := failureCode(t, err); code != llm.NoDiscoveryCode {
		t.Errorf("拆完之后模型问询还挂着：%q", code)
	}
	// 幂等：拆除器被调两次是常事（用例的 Cleanup 就会再调一次）。
	if err := kit.teardown(t.Context()); err != nil {
		t.Errorf("第二次拆除本该什么都不做：%v", err)
	}
}

// TestSettingsCommitBringsRoutesToLife 验一次设置提交能让一个待命的装配开始服务。
func TestSettingsCommitBringsRoutesToLife(t *testing.T) {
	kit := install(t, Config{}, true)
	if len(kit.routes()) != 0 {
		t.Fatal("装的时候就不该有路由")
	}

	kit.commit(t, section("acme", routeJSON()))
	if routes := kit.routes(); len(routes) != 1 || routes[0] != "acme" {
		t.Fatalf("提交之后路由没起来：%v", routes)
	}
	if entries := kit.runtime.ListConfigurableProviders(); len(entries) != 1 {
		t.Errorf("提交之后目录没跟上：%+v", entries)
	}
}

// TestSettingsRenameReregisters 验一次纯粹的改名也会重新登记。
//
// 注册表是在登记那一刻捕获显示名的，并且通过 providerInfo() 把它交给每一个选择器：
// 不重新登记的话，旧标签会一直挂在界面上，直到某件不相干的事碰巧变了。
func TestSettingsRenameReregisters(t *testing.T) {
	kit := install(t, Config{}, true)
	route := routeJSON()
	route["displayName"] = "Acme"
	kit.commit(t, section("acme", route))
	if name := kit.runtime.ListProviders()[0].Name; name != "Acme" {
		t.Fatalf("显示名没落下来：%q", name)
	}

	renamed := routeJSON()
	renamed["displayName"] = "Acme Gateway"
	kit.commit(t, section("acme", renamed))
	if name := kit.runtime.ListProviders()[0].Name; name != "Acme Gateway" {
		t.Errorf("改名没让注册表重新登记：%q", name)
	}
}

// TestSettingsRetryPolicyChangeReregisters 验改重试策略也会重新登记。
//
// 那份策略同样是在登记那一刻捕获的，而它决定的是这条路由上每一次失败要不要重来。
func TestSettingsRetryPolicyChangeReregisters(t *testing.T) {
	kit := install(t, Config{}, true)
	kit.commit(t, section("acme", routeJSON()))
	policy, err := kit.runtime.ProviderRetryPolicy("acme")
	if err != nil {
		t.Fatalf("取不到重试策略：%v", err)
	}
	if policy.Mode != llm.RetryNormal {
		t.Fatalf("默认策略该是 normal，得到 %q", policy.Mode)
	}

	route := routeJSON()
	route["retryPolicy"] = map[string]any{"mode": string(llm.RetryAlways)}
	kit.commit(t, section("acme", route))
	policy, err = kit.runtime.ProviderRetryPolicy("acme")
	if err != nil {
		t.Fatalf("取不到重试策略：%v", err)
	}
	if policy.Mode != llm.RetryAlways {
		t.Errorf("改策略没让注册表重新登记：%q", policy.Mode)
	}
}

// TestSettingsRoutesCanGoEmptyAndComeBack 验路由被删光之后这次装配仍然活着。
//
// 已经登记过之后清单变空是「这个命名空间此刻一条路由都没有」，不是「这个插件没了」
// ——所以下一次提交必须还能把它们带回来。
func TestSettingsRoutesCanGoEmptyAndComeBack(t *testing.T) {
	kit := install(t, Config{}, true)
	kit.commit(t, section("acme", routeJSON()))
	if len(kit.routes()) != 1 {
		t.Fatal("第一次提交没把路由带起来")
	}

	kit.commit(t, map[string]any{})
	if routes := kit.routes(); len(routes) != 0 {
		t.Errorf("路由删光之后注册表里还有：%v", routes)
	}
	if entries := kit.runtime.ListConfigurableProviders(); len(entries) != 0 {
		t.Errorf("路由删光之后目录里还有：%+v", entries)
	}

	kit.commit(t, section("beta", routeJSON()))
	if routes := kit.routes(); len(routes) != 1 || routes[0] != "beta" {
		t.Errorf("路由没能重新起来：%v", routes)
	}
}

// TestSettingsRejectsUnserviceableSection 验一份服务不了的段在被写下的地方就被拒，
// 而且先前那些路由继续服务。
//
// 没有这一道的话，一份 schema 上过得去、适配器却服务不了的配置会被存下来，
// 然后悄悄把这个命名空间里的每条路由都停掉。
func TestSettingsRejectsUnserviceableSection(t *testing.T) {
	kit := install(t, Config{}, true)
	kit.commit(t, section("acme", routeJSON()))

	// 没有端点的路由服务不了。
	err := kit.settings.Replace(t.Context(), SettingsNamespace,
		section("acme", map[string]any{"models": []any{map[string]any{"id": "m"}}}), nil)
	if err == nil {
		t.Fatal("一份服务不了的段本该被拒")
	}
	if routes := kit.routes(); len(routes) != 1 || routes[0] != "acme" {
		t.Errorf("一次被拒的提交动到了正在服务的路由：%v", routes)
	}
}

// TestSettingsCommitCarriesRouteDetailIntoTheAdapter 验提交下来的路由细节真的被
// 适配器读到了，而不是只在注册表那份登记事实里。
func TestSettingsCommitCarriesRouteDetailIntoTheAdapter(t *testing.T) {
	kit := install(t, Config{}, true)
	route := routeJSON()
	route["models"] = []any{map[string]any{"id": "m", "contextWindow": float64(4096)}}
	kit.commit(t, section("acme", route))

	models, err := kit.runtime.ListModels(t.Context(), "acme")
	if err != nil {
		t.Fatalf("列不出模型：%v", err)
	}
	if len(models) != 1 || models[0].ID != "m" {
		t.Fatalf("提交下来的模型没到适配器：%+v", models)
	}
	// 上下文容量不在清单那一层——它是随一次模型解算一起交出来的，而那正是派发
	// 之前真的会去问的那条路。
	resolved, err := kit.runtime.ResolveModelInfo(t.Context(), "acme", "m")
	if err != nil {
		t.Fatalf("解不出这条模型：%v", err)
	}
	if resolved.Context == nil || resolved.Context.ContextWindow != 4096 {
		t.Errorf("提交下来的上下文容量没到适配器：%+v", resolved.Context)
	}
}

// fakeCredentials 只答 Resolve，别的方法一律不该被叫到。
//
// 内嵌接口而不是把它整个实现一遍：这一层只用得上 Resolve，而一个多出来的空实现
// 会让「本包偷偷用了别的方法」这件事悄无声息地过去——内嵌的 nil 会当场崩。
type fakeCredentials struct {
	credentials.Provider
	value string
	err   error
}

func (c fakeCredentials) Resolve(context.Context, credentials.Ref) (credentials.Resolved, bool, error) {
	if c.err != nil {
		return credentials.Resolved{}, false, c.err
	}
	if c.value == "" {
		return credentials.Resolved{}, false, nil
	}
	return credentials.Resolved{Value: c.value, Source: "test"}, true, nil
}

// TestResolveAPIKeyReadsTheCredentialFace 验挂了凭据面时从它取密钥。
func TestResolveAPIKeyReadsTheCredentialFace(t *testing.T) {
	profile := minimalProfile()
	profile.APIKeyEnv = "ACME_KEY"
	resolved := resolveOne(t, profile)

	install := &installation{credentials: fakeCredentials{value: " sk-live "}, logger: quietLogger()}
	key, err := install.resolveAPIKey(t.Context(), "acme", resolved)
	if err != nil || key != "sk-live" {
		t.Fatalf("该从凭据面取到密钥并去掉首尾空白：%q %v", key, err)
	}

	// 凭据面自己报错时原样往上抛：那条错误比这里能编出来的任何一句都准。
	install = &installation{credentials: fakeCredentials{err: os.ErrPermission}, logger: quietLogger()}
	if _, err := install.resolveAPIKey(t.Context(), "acme", resolved); err == nil {
		t.Error("凭据面报的错本该往上抛")
	}
}

// TestResolveAPIKeyFallsBackToTheEnvironment 验没挂凭据面时进程环境就是整个凭据面。
func TestResolveAPIKeyFallsBackToTheEnvironment(t *testing.T) {
	profile := minimalProfile()
	profile.APIKeyEnv = "ACME_KEY"
	resolved := resolveOne(t, profile)
	install := &installation{logger: quietLogger()}

	t.Setenv("ACME_KEY", "sk-from-env")
	key, err := install.resolveAPIKey(t.Context(), "acme", resolved)
	if err != nil || key != "sk-from-env" {
		t.Fatalf("该从环境里取到密钥：%q %v", key, err)
	}
}

// TestResolveAPIKeyDistinguishesUnauthenticatedFromMissing 验「没点名凭据」和
// 「点了名但取不到」是两件事。
//
// 前者是本地推理服务器的正常姿态；后者悄悄退回成不认证的话，请求会带着这次部署
// 根本没打算用的身份（或者干脆没有身份）发出去，而两者都不是配置说的那件事。
func TestResolveAPIKeyDistinguishesUnauthenticatedFromMissing(t *testing.T) {
	install := &installation{logger: quietLogger()}

	// 没写 apiKeyEnv：这条路由不认证。
	key, err := install.resolveAPIKey(t.Context(), "acme", resolveOne(t, minimalProfile()))
	if err != nil || key != "" {
		t.Fatalf("一条不认证的路由该安静地交出空串：%q %v", key, err)
	}

	profile := minimalProfile()
	profile.APIKeyEnv = "ACME_KEY"
	t.Setenv("ACME_KEY", "")
	_, err = install.resolveAPIKey(t.Context(), "acme", resolveOne(t, profile))
	if code := failureCode(t, err); code != "MISSING_CREDENTIAL" {
		t.Errorf("失败码不对：%q", code)
	}
	// 诊断得点出是哪条路由、缺的是哪个引用，不然一份几十行的配置查不下去。
	if !strings.Contains(err.Error(), `"acme"`) || !strings.Contains(err.Error(), "ACME_KEY") {
		t.Errorf("诊断没点名路由和引用：%v", err)
	}
}

// TestRegistrationFactsFollowSortedRoutes 验登记事实按路由键排着。
//
// 不稳的话，一份只是把键换了个书写顺序的设置文档会被当成一次路由变更，
// 于是每一次设置提交都会把整套路由白白重新登记一遍。
func TestRegistrationFactsFollowSortedRoutes(t *testing.T) {
	profiles, err := ResolveProfiles(map[string]ProviderProfile{
		"zeta": minimalProfile(), "alpha": minimalProfile(), "mid": minimalProfile()})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	facts := registrationFacts(profiles)
	want := []string{"alpha", "mid", "zeta"}
	if len(facts) != len(want) {
		t.Fatalf("事实条数不对：%d", len(facts))
	}
	for index, provider := range want {
		if facts[index].provider != provider {
			t.Errorf("第 %d 条该是 %q，得到 %q", index, provider, facts[index].provider)
		}
	}
	if !sameRouteFacts(facts, registrationFacts(profiles)) {
		t.Error("同一份路由表算两次给出了不一样的事实")
	}

	// 只改显示名也算变了：注册表在登记那一刻就把它捕获走了。
	renamed := minimalProfile()
	renamed.DisplayName = "Alpha!"
	other, err := ResolveProfiles(map[string]ProviderProfile{
		"zeta": minimalProfile(), "alpha": renamed, "mid": minimalProfile()})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if sameRouteFacts(facts, registrationFacts(other)) {
		t.Error("一次改名没被认成变了")
	}
}

// TestSameDirectoryEntriesComparesTheDeclaredValue 验目录比较看的是 declared
// 指向的值，不是那个指针本身。
//
// 两次算出来的目录各自指着同一个包级变量，但那是这个实现的巧合；比较依赖它的话，
// 哪天改成每条现分配一个 bool，每一次设置提交都会白白重登一遍目录。
func TestSameDirectoryEntriesComparesTheDeclaredValue(t *testing.T) {
	profiles, err := ResolveProfiles(map[string]ProviderProfile{"acme": minimalProfile()})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	entries := directoryEntries(profiles)
	if len(entries) != 1 || entries[0].Declared == nil || !*entries[0].Declared {
		t.Fatalf("目录条目不对：%+v", entries)
	}
	if !sameDirectoryEntries(entries, directoryEntries(profiles)) {
		t.Error("同一份路由表算两次给出了不一样的目录")
	}

	// 换一个指向同样值的指针：仍然该算一样。
	same := true
	clone := []llm.ConfigurableProvider{entries[0]}
	clone[0].Declared = &same
	if !sameDirectoryEntries(entries, clone) {
		t.Error("比较看的是指针而不是它指向的值")
	}
	// 换一个指向不同值的指针：该算不一样。
	other := false
	clone[0].Declared = &other
	if sameDirectoryEntries(entries, clone) {
		t.Error("declared 变了却被认成没变")
	}
}

// TestRawConfigOmitsAnEmptyProvidersKey 验一份没有路由的装配配置不在组装层留下
// 一个空的 providers 键。
//
// 那个键存在与否是配置界面用来标「这一层提没提过这个字段」的依据。
func TestRawConfigOmitsAnEmptyProvidersKey(t *testing.T) {
	raw, err := rawConfig(Config{})
	if err != nil || raw != nil {
		t.Fatalf("空配置该投影成 nil：%v %v", raw, err)
	}

	raw, err = rawConfig(Config{Providers: map[string]ProviderProfile{"acme": minimalProfile()}})
	if err != nil {
		t.Fatalf("投影失败：%v", err)
	}
	providers, present := raw["providers"].(map[string]any)
	if !present {
		t.Fatalf("投影里没有 providers：%v", raw)
	}
	route, present := providers["acme"].(map[string]any)
	if !present {
		t.Fatalf("投影里没有这条路由：%v", providers)
	}
	// 键名走的是 [Config] 自己的 json 标签，好让组装层和用户段说同一套话。
	if route["baseURL"] != "https://gateway.example/v1" {
		t.Errorf("投影出来的键名不对：%v", route)
	}
}

// TestSettingsNamespaceMatchesThePluginName 验设置命名空间就是插件名。
//
// 它同时是目录里每一条的设置地址；指到一个这份代码里根本不存在的插件名上，
// 配置界面就编不了这些路由了。
func TestSettingsNamespaceMatchesThePluginName(t *testing.T) {
	if string(SettingsNamespace) != PluginName {
		t.Errorf("命名空间和插件名对不上：%q vs %q", SettingsNamespace, PluginName)
	}
}
