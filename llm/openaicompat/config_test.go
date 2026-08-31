// 本文件验配置那一层：一份随手写的路由表怎么被落定、哪些写法会被拒，以及
// 落定出来的那份路由表为什么必须是「按身份可比」而且遍历次序稳定的。
//
// 用例都不起网络。这一层的全部职责就是「读一份配置、给出一个答案或者一条诊断」，
// 掺进 HTTP 只会把要考的东西换掉。

package openaicompat

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ds-harness-go/llm"
)

// minimalProfile 造一条刚好服务得了的路由：端点加一条只写了 id 的模型。
//
// 每条用例都从它出发再改一处，好让「被拒的是我改的那一处」这件事不需要靠读诊断
// 文案来确认。
func minimalProfile() ProviderProfile {
	return ProviderProfile{
		BaseURL: "https://gateway.example/v1",
		Models:  []ModelProfile{{ID: "m"}},
	}
}

// resolveOne 落定一条路由，失败就让用例当场停下。
func resolveOne(t *testing.T, profile ProviderProfile) ResolvedProviderProfile {
	t.Helper()
	profiles, err := ResolveProfiles(map[string]ProviderProfile{"acme": profile})
	if err != nil {
		t.Fatalf("这条路由本该服务得了：%v", err)
	}
	resolved, owned := profiles.Get("acme")
	if !owned {
		t.Fatal("落定完之后 acme 这条路由不见了")
	}
	return resolved
}

// rejects 断言一份配置被拒，并且拒它的是配置层那个哨兵。
func rejects(t *testing.T, providers map[string]ProviderProfile, wants string) {
	t.Helper()
	_, err := ResolveProfiles(providers)
	if err == nil {
		t.Fatal("这份配置本该被拒")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("拒绝没有挂在 ErrInvalidConfig 下面：%v", err)
	}
	if !strings.Contains(err.Error(), wants) {
		t.Fatalf("诊断没有点出 %q：%v", wants, err)
	}
}

// TestResolveProfilesFillsDefaults 验一条只写了必填项的路由把该兜的都兜上了。
func TestResolveProfilesFillsDefaults(t *testing.T) {
	resolved := resolveOne(t, minimalProfile())

	// displayName 不写就是路由键：配置界面上一直显示的就是它。
	if resolved.DisplayName != "acme" {
		t.Errorf("displayName 该兜到路由键上，得到 %q", resolved.DisplayName)
	}
	if resolved.APIKeyRef != "" {
		t.Errorf("没写 apiKeyEnv 的路由该是不认证的，得到 %q", resolved.APIKeyRef)
	}
	if resolved.StreamIdleTimeout != DefaultStreamIdleTimeout {
		t.Errorf("streamIdleTimeout 该兜到默认值，得到 %v", resolved.StreamIdleTimeout)
	}
	// timeoutMs 是唯一一个「零表示不设」的时限，不该被兜成某个默认值。
	if resolved.Timeout != 0 {
		t.Errorf("没写 timeoutMs 的路由该不设总时限，得到 %v", resolved.Timeout)
	}
	if resolved.MaxRequestImageBytes != DefaultMaxRequestImageBytes {
		t.Errorf("maxRequestImageBytes 该兜到默认值，得到 %d", resolved.MaxRequestImageBytes)
	}
	if resolved.RequestImagePixelBudget != DefaultRequestImagePixelBudget {
		t.Errorf("requestImagePixelBudget 该兜到默认值，得到 %d", resolved.RequestImagePixelBudget)
	}
	if resolved.RequestImageMaxBytes != DefaultRequestImageMaxBytes {
		t.Errorf("requestImageMaxBytes 该兜到默认值，得到 %d", resolved.RequestImageMaxBytes)
	}

	model := resolved.Models[0]
	if model.Name != "m" {
		t.Errorf("模型显示名该兜到 id 上，得到 %q", model.Name)
	}
	if model.ContextWindow != DefaultContextWindow || model.MaxTokens != DefaultMaxTokens {
		t.Errorf("模型容量该兜到路由默认值，得到 %d/%d", model.ContextWindow, model.MaxTokens)
	}
	if !slices.Equal(model.Input, DefaultInput()) {
		t.Errorf("模型模态该兜到 defaultInput，得到 %v", model.Input)
	}
	if model.ReasoningEfforts != nil {
		t.Errorf("没写 reasoningEfforts 的模型该是不推理的，得到 %v", model.ReasoningEfforts)
	}
	// 没写策略时整套默认由 llm 那一层给，这里只验它确实被解算过（模式非空）。
	if resolved.RetryPolicy.Mode == "" {
		t.Error("没写 retryPolicy 的路由该拿到那套默认策略")
	}
}

// TestResolveProfilesReadsExplicitValues 验写下来的值原样落进落定结果。
func TestResolveProfilesReadsExplicitValues(t *testing.T) {
	profile := minimalProfile()
	profile.DisplayName = "Acme Gateway"
	profile.APIKeyEnv = "ACME_KEY"
	profile.TimeoutMs = 1500
	profile.StreamIdleTimeoutMs = 2500
	profile.Headers = map[string]string{"X-Tenant": "acme"}
	profile.Models[0].ReasoningEfforts = map[llm.ReasoningEffortID]string{ThinkingHigh: "high"}
	profile.Reasoning = ThinkingHigh

	resolved := resolveOne(t, profile)
	if resolved.DisplayName != "Acme Gateway" {
		t.Errorf("displayName 没落下来：%q", resolved.DisplayName)
	}
	if string(resolved.APIKeyRef) != "ACME_KEY" {
		t.Errorf("apiKeyEnv 没变成凭据引用：%q", resolved.APIKeyRef)
	}
	if resolved.Timeout != 1500*time.Millisecond {
		t.Errorf("timeoutMs 该按毫秒读，得到 %v", resolved.Timeout)
	}
	if resolved.StreamIdleTimeout != 2500*time.Millisecond {
		t.Errorf("streamIdleTimeoutMs 该按毫秒读，得到 %v", resolved.StreamIdleTimeout)
	}
	if resolved.Headers["X-Tenant"] != "acme" {
		t.Errorf("headers 没落下来：%v", resolved.Headers)
	}
	if resolved.Reasoning != ThinkingHigh {
		t.Errorf("reasoning 没落下来：%q", resolved.Reasoning)
	}
}

// TestResolveProfilesClonesHeaders 验落定出来的请求头是本包自己的一份。
//
// 不复制的话，一份从设置文档里解出来的 map 会被这条路由一直握着，而写下它的那一层
// 之后改它，就等于绕过整个校验层改了一条已经在服务的路由。
func TestResolveProfilesClonesHeaders(t *testing.T) {
	headers := map[string]string{"X-Tenant": "acme"}
	profile := minimalProfile()
	profile.Headers = headers

	resolved := resolveOne(t, profile)
	headers["X-Tenant"] = "someone-else"
	if resolved.Headers["X-Tenant"] != "acme" {
		t.Error("改配置里那份 map 动到了已经落定的路由")
	}
}

// TestResolveProfilesRejections 逐条验哪些写法服务不了。
//
// 每条用例都点出诊断里必须出现的那个键名：一条说不清是哪个字段出事的诊断，
// 对着一份几十行的 providers 表是没有用的。
func TestResolveProfilesRejections(t *testing.T) {
	cases := []struct {
		name string
		// route 为空表示用默认的 acme；空路由键那条用例靠 emptyRoute 点出来。
		emptyRoute bool
		mutate     func(*ProviderProfile)
		wants      string
	}{
		{name: "空路由键", emptyRoute: true, mutate: func(*ProviderProfile) {}, wants: "提供方名字不能是空的"},
		{name: "没有端点", mutate: func(p *ProviderProfile) { p.BaseURL = "" }, wants: "baseURL"},
		{name: "空闲上限为负", mutate: func(p *ProviderProfile) { p.StreamIdleTimeoutMs = -1 }, wants: "streamIdleTimeout"},
		{name: "图片上限为负", mutate: func(p *ProviderProfile) { p.MaxRequestImageBytes = -1 }, wants: "maxRequestImageBytes"},
		{name: "像素预算为负", mutate: func(p *ProviderProfile) { p.RequestImagePixelBudget = -1 }, wants: "requestImagePixelBudget"},
		{name: "单图字节为负", mutate: func(p *ProviderProfile) { p.RequestImageMaxBytes = -1 }, wants: "requestImageMaxBytes"},
		{name: "上下文容量为负", mutate: func(p *ProviderProfile) { p.DefaultContextWindow = -1 }, wants: "defaultContextWindow"},
		{name: "输出上限为负", mutate: func(p *ProviderProfile) { p.DefaultMaxTokens = -1 }, wants: "defaultMaxTokens"},
		{
			name:   "模态清单空着",
			mutate: func(p *ProviderProfile) { p.DefaultInput = []llm.ModelModality{} },
			wants:  "defaultInput",
		},
		{
			name:   "不认得的推理档位",
			mutate: func(p *ProviderProfile) { p.Reasoning = "higth" },
			wants:  "reasoning",
		},
		{
			name:   "凭据引用不合法",
			mutate: func(p *ProviderProfile) { p.APIKeyEnv = "not a ref" },
			wants:  "apiKeyEnv",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			profile := minimalProfile()
			testCase.mutate(&profile)
			route := "acme"
			if testCase.emptyRoute {
				route = ""
			}
			rejects(t, map[string]ProviderProfile{route: profile}, testCase.wants)
		})
	}
}

// TestResolveProfilesRejectsFirstRouteBySortOrder 验一份错了好几处的配置报的是
// 排序在前的那一条。
//
// 这个次序是这个重写自己定的（Go 的 map 留不住书写次序），而它必须**稳定**——
// 同一份配置每次解算报出来的都得是同一条，否则一次「改一处、再跑一次」的排错
// 会变成打地鼠。
func TestResolveProfilesRejectsFirstRouteBySortOrder(t *testing.T) {
	broken := ProviderProfile{Models: []ModelProfile{{ID: "m"}}}
	providers := map[string]ProviderProfile{"zeta": broken, "alpha": broken}
	for range 20 {
		_, err := ResolveProfiles(providers)
		if err == nil {
			t.Fatal("这份配置本该被拒")
		}
		if !strings.Contains(err.Error(), `"alpha"`) {
			t.Fatalf("报的不是排序在前的那条路由：%v", err)
		}
	}
}

// TestProfilesIdentity 验落定结果的身份语义。
//
// [Adapter.current] 靠 == 决定要不要重建快照，而快照里每条路由都握着一个连接池。
// 「同一份解算结果原样再交一次」必须认得出来，「两次解算」必须认成两份。
func TestProfilesIdentity(t *testing.T) {
	providers := map[string]ProviderProfile{"acme": minimalProfile()}
	first, err := ResolveProfiles(providers)
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	again := first
	if again != first {
		t.Error("复制一份 Profiles 之后它不再等于原本那一份")
	}
	second, err := ResolveProfiles(providers)
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if second == first {
		t.Error("两次独立的解算被认成了同一份路由表")
	}
}

// TestProfilesZeroValue 验零值 Profiles 的每个读法都受得住。
//
// 一次待命的裸装配在设置文档递来路由之前握着的就是它。
func TestProfilesZeroValue(t *testing.T) {
	var profiles Profiles
	if profiles.Len() != 0 {
		t.Errorf("零值该是空的，得到 %d", profiles.Len())
	}
	if routes := profiles.Routes(); len(routes) != 0 {
		t.Errorf("零值该没有路由，得到 %v", routes)
	}
	if _, owned := profiles.Get("acme"); owned {
		t.Error("零值不该拥有任何路由")
	}
	profiles.All(func(string, ResolvedProviderProfile) bool {
		t.Error("零值不该遍历出任何东西")
		return true
	})
}

// TestProfilesRoutesAreSorted 验遍历次序是稳定的字典序。
//
// 登记事实、目录条目、诊断里的枚举全指望它：不稳的话，同一份配置解算两次会给出
// 两串不同的登记事实，于是每一次设置变更都会把整套路由白白重新登记一遍。
func TestProfilesRoutesAreSorted(t *testing.T) {
	providers := map[string]ProviderProfile{
		"zeta": minimalProfile(), "alpha": minimalProfile(), "mid": minimalProfile(),
	}
	profiles, err := ResolveProfiles(providers)
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if routes := profiles.Routes(); !slices.Equal(routes, want) {
		t.Errorf("路由次序不对：%v", routes)
	}
	var visited []string
	profiles.All(func(provider string, profile ResolvedProviderProfile) bool {
		if profile.Provider != provider {
			t.Errorf("路由 %q 落定结果里的 Provider 是 %q", provider, profile.Provider)
		}
		visited = append(visited, provider)
		return true
	})
	if !slices.Equal(visited, want) {
		t.Errorf("遍历次序和 Routes 不一致：%v", visited)
	}
}

// TestProfilesAllStopsEarly 验遍历回调交出 false 时就地停下。
func TestProfilesAllStopsEarly(t *testing.T) {
	providers := map[string]ProviderProfile{"alpha": minimalProfile(), "zeta": minimalProfile()}
	profiles, err := ResolveProfiles(providers)
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	count := 0
	profiles.All(func(string, ResolvedProviderProfile) bool {
		count++
		return false
	})
	if count != 1 {
		t.Errorf("回调交出 false 之后还在继续遍历：走了 %d 次", count)
	}
}

// TestModelInfoReportsRouteDefaultEffortOnlyWhenOffered 验路由的默认推理档位只落到
// **确实提供这一档**的模型上。
//
// 同一条路由上放一个推理模型和一个不推理的模型是完全正常的；把一个模型不提供的
// 档位报成它的默认值，等于让每一次不点档位的请求都撞 UNSUPPORTED_REASONING_EFFORT。
func TestModelInfoReportsRouteDefaultEffortOnlyWhenOffered(t *testing.T) {
	profile := minimalProfile()
	profile.Reasoning = ThinkingHigh
	profile.Models = []ModelProfile{
		{ID: "thinker", ReasoningEfforts: map[llm.ReasoningEffortID]string{ThinkingHigh: "high"}},
		{ID: "narrow", ReasoningEfforts: map[llm.ReasoningEffortID]string{ThinkingLow: "low"}},
		{ID: "plain"},
	}
	resolved := resolveOne(t, profile)

	thinker, _ := resolved.Model("thinker")
	if info := resolved.ModelInfo(thinker); info.Reasoning == nil || info.Reasoning.DefaultEffort != ThinkingHigh {
		t.Errorf("提供这一档的模型该报出路由默认档位：%+v", info.Reasoning)
	}
	narrow, _ := resolved.Model("narrow")
	if info := resolved.ModelInfo(narrow); info.Reasoning == nil || info.Reasoning.DefaultEffort != "" {
		t.Errorf("不提供这一档的模型不该报出默认档位：%+v", info.Reasoning)
	}
	plain, _ := resolved.Model("plain")
	if info := resolved.ModelInfo(plain); info.Reasoning != nil {
		t.Errorf("不推理的模型不该有推理元数据：%+v", info.Reasoning)
	}
}

// TestModelInfoCarriesConfiguredMaxTokensOnly 验只有配置**点名**写下的输出上限才
// 变成请求默认值。
//
// 从路由兜出来的那个数只是能力；把它当成请求默认值落下去，等于开始拿一个谁也没
// 挑过的数字去封每一次请求。
func TestModelInfoCarriesConfiguredMaxTokensOnly(t *testing.T) {
	profile := minimalProfile()
	profile.DefaultMaxTokens = 8192
	profile.Models = []ModelProfile{{ID: "picked", MaxTokens: 4096}, {ID: "inherited"}}
	resolved := resolveOne(t, profile)

	picked, _ := resolved.Model("picked")
	if info := resolved.ModelInfo(picked); info.DefaultMaxTokens != 4096 {
		t.Errorf("显式写下的 maxTokens 该成为请求默认值，得到 %d", info.DefaultMaxTokens)
	}
	inherited, _ := resolved.Model("inherited")
	info := resolved.ModelInfo(inherited)
	if info.DefaultMaxTokens != 0 {
		t.Errorf("兜出来的能力不该成为请求默认值，得到 %d", info.DefaultMaxTokens)
	}
	if inherited.MaxTokens != 8192 {
		t.Errorf("兜底的能力本身该落下来，得到 %d", inherited.MaxTokens)
	}
}

// TestAssertServiceableMatchesResolve 验设置层那个校验器和解算是同一套判据。
//
// 它登记成命名空间的校验器，好让一份服务不了的配置在被写下的地方就被拒。两者
// 一旦分家，就会出现「存下来了但一条路由都没在服务」的配置。
func TestAssertServiceableMatchesResolve(t *testing.T) {
	good := Config{Providers: map[string]ProviderProfile{"acme": minimalProfile()}}
	if err := AssertServiceable(good); err != nil {
		t.Errorf("这份配置本该过：%v", err)
	}
	bad := Config{Providers: map[string]ProviderProfile{"acme": {Models: []ModelProfile{{ID: "m"}}}}}
	if err := AssertServiceable(bad); err == nil {
		t.Error("没有端点的路由本该被校验器拒掉")
	}
	// 空配置就是待命姿态，不是一份错的配置。
	if err := AssertServiceable(Config{}); err != nil {
		t.Errorf("一份空配置该被当成待命而不是出错：%v", err)
	}
}

// TestConfigJSONRoundTripPreservesNilVersusEmpty 验那三个「nil 和空不是一回事」的
// 字段过一遍 JSON 之后仍然分得开。
//
// 这不是一条洁癖：[settings.Register] 就是拿 encoding/json 把这个类型来回过一遍的，
// 所以一个漏掉的 omitempty 会把「该被拒的空清单」在存盘那一刻悄悄变成「没写」。
func TestConfigJSONRoundTripPreservesNilVersusEmpty(t *testing.T) {
	empty := Config{Providers: map[string]ProviderProfile{"acme": {
		BaseURL:      "https://gateway.example/v1",
		DefaultInput: []llm.ModelModality{},
		Models:       []ModelProfile{{ID: "m", ReasoningEfforts: map[llm.ReasoningEffortID]string{}}},
		RetryPolicy:  &RetryPolicy{Mode: llm.RetryNormal, RetryableCodes: []string{}},
	}}}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	var decoded Config
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	route := decoded.Providers["acme"]
	if route.DefaultInput == nil {
		t.Error("空的 defaultInput 过了一遍 JSON 之后变成了没写")
	}
	if route.Models[0].ReasoningEfforts == nil {
		t.Error("空的 reasoningEfforts 过了一遍 JSON 之后变成了没写")
	}
	if route.RetryPolicy == nil || route.RetryPolicy.RetryableCodes == nil {
		t.Error("空的 retryableCodes 过了一遍 JSON 之后变成了没写")
	}

	// 反过来：真正没写的那几个字段解回来仍然是 nil。
	var bare Config
	if err := json.Unmarshal([]byte(
		`{"providers":{"acme":{"baseURL":"https://gateway.example/v1","models":[{"id":"m"}]}}}`), &bare); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	route = bare.Providers["acme"]
	if route.DefaultInput != nil || route.Models[0].ReasoningEfforts != nil || route.RetryPolicy != nil {
		t.Error("没写的字段解回来不该是非 nil")
	}
}

// TestRetryPolicyConfigTranslatesMilliseconds 验设置形状那份策略翻过去时按毫秒读。
//
// 这个类型存在的唯一理由就是 [llm.RetryPolicyConfig] 的时限是 [time.Duration]
// （在 JSON 里是纳秒），所以这次翻译错了的话，一份写着 500 的退避会变成 500 纳秒。
func TestRetryPolicyConfigTranslatesMilliseconds(t *testing.T) {
	jitter := 0.0
	policy := &RetryPolicy{
		Mode:           llm.RetryNormal,
		RetryableCodes: []string{"SERVER"},
		Backoff:        Backoff{InitialDelayMs: 500, MaxDelayMs: 10_000, JitterRatio: &jitter},
	}
	translated := policy.config()
	if translated.Backoff.InitialDelay != 500*time.Millisecond {
		t.Errorf("initialDelayMs 没按毫秒读：%v", translated.Backoff.InitialDelay)
	}
	if translated.Backoff.MaxDelay != 10*time.Second {
		t.Errorf("maxDelayMs 没按毫秒读：%v", translated.Backoff.MaxDelay)
	}
	// 抖动是指针，因为 0 是一个有意义的取值（完全不抖动）。
	if translated.Backoff.JitterRatio == nil || *translated.Backoff.JitterRatio != 0 {
		t.Errorf("显式写下的零抖动没落下来：%v", translated.Backoff.JitterRatio)
	}
	// 翻出来的可重试码得是自己的一份，不然改它会动到配置。
	translated.RetryableCodes[0] = "OTHER"
	if policy.RetryableCodes[0] != "SERVER" {
		t.Error("翻译结果和配置共享了同一个切片")
	}
	if (*RetryPolicy)(nil).config() != nil {
		t.Error("没写策略时翻译结果该仍然是 nil")
	}
}

// TestResolveProfilesRejectsBadRetryPolicy 验策略的毛病由 llm 那一层报，但仍然
// 挂在本包的配置哨兵下面。
func TestResolveProfilesRejectsBadRetryPolicy(t *testing.T) {
	profile := minimalProfile()
	profile.RetryPolicy = &RetryPolicy{Mode: "sometimes"}
	rejects(t, map[string]ProviderProfile{"acme": profile}, "retryPolicy")
}
