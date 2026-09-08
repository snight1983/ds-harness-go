// 本文件的作用：把孩子那条 LLM 路由钉在它那几条真会出错的边上——那张授权表怎么校验、
// 模型说出来的三个字段怎么并、并完之后归谁管、那份耐久的表怎么记怎么读，以及那件
// 只读发现工具一层一层往下问的时候，哪几样绝不许说出去。
//
// # 这些测试防的是什么错
//
//   - **provider 和 model 被拆开给**。只换一半是一条谁都不认识的路由，它必须在
//     工具这道边界上就被拒，而不是在一个已经落了盘的孩子身上失败。
//   - **换了路由却留着上一条路由的推理档位**。那个档位是跟着模型走的，留着它等于
//     拿甲模型的档位去发乙模型的请求。
//   - **一份写坏了的授权表被折成「一张谁都不许的表」**。那会把一次本该走继承的
//     派发也一并拒掉，而它本来就不受这张表管。
//   - **授权表被后来的事件改掉**。它写一次就定了：一条恢复回来的会话必须跑它当初
//     被授的那张表，而不是此刻的用户设置。
//   - **发现工具把一个没被授权的提供方名说出去**。那句「你还可以选这些」是模型
//     唯一的信息来源，漏一个名字就是漏一条越权路径。
//   - **开着模型选择却一条路由都没给**。那和关着的区别只在报错的时机上，而模型
//     那边还会多出三个永远用不了的参数。
//   - **交出去的那份表是活引用**。调用方改一下就等于改了这条会话被授的权。

package subagenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// ---- 假件 ----

// catalogAdapter 是一个只答目录问题的假适配器：它列得出模型、解得出一条精确路由，
// 而那次流调用永远走不到——本文件里一次派发都不起。
type catalogAdapter struct {
	models   []llm.ModelInfo
	resolved map[string]llm.ResolvedModelInfo
}

func (a *catalogAdapter) Stream(
	context.Context,
	llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	return func(func(llm.StreamChunk, error) bool) {}, nil
}

func (a *catalogAdapter) ListModels(_ context.Context, provider string) ([]llm.ModelInfo, error) {
	var owned []llm.ModelInfo
	for _, model := range a.models {
		if model.Provider == provider {
			owned = append(owned, model)
		}
	}
	return owned, nil
}

func (a *catalogAdapter) ResolveModel(
	_ context.Context,
	provider, model string,
) (llm.ResolvedModelInfo, error) {
	info, found := a.resolved[provider+"/"+model]
	if !found {
		return llm.ResolvedModelInfo{}, fmt.Errorf("这个假适配器不认得 %s/%s", provider, model)
	}
	return info, nil
}

// effortsOf 排一份只有这几档、且第一档是默认的推理档位表。
func effortsOf(ids ...llm.ReasoningEffortID) *llm.ModelReasoningInfo {
	if len(ids) == 0 {
		return nil
	}
	efforts := make([]llm.ReasoningEffortInfo, 0, len(ids))
	for _, id := range ids {
		efforts = append(efforts, llm.ReasoningEffortInfo{ID: id, Name: string(id)})
	}
	return &llm.ModelReasoningInfo{Efforts: efforts, DefaultEffort: ids[0]}
}

// catalogRuntime 造一台登记了「甲」和「乙」两条提供方路由的 llm 运行时。
//
// 甲有 m1 和 m2 两个模型，只有 m1 认 high 这一档；乙只有 n1，一档都不认。
// 这个形状刚好让「换路由要不要把继承来的档位清掉」变成一次真的成败判定。
func catalogRuntime(t *testing.T) (*llm.Runtime, *scope.Scope) {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })

	adapter := &catalogAdapter{
		models: []llm.ModelInfo{
			{Provider: "甲", ID: "m1", Name: "甲一号", Description: "带档位的那个"},
			{Provider: "甲", ID: "m2", Name: "甲二号"},
			{Provider: "乙", ID: "n1", Name: "乙一号"},
		},
		resolved: map[string]llm.ResolvedModelInfo{
			"甲/m1": {
				ModelInfo: llm.ModelInfo{Provider: "甲", ID: "m1", Name: "甲一号", Description: "带档位的那个"},
				Reasoning: effortsOf("high", "low"),
			},
			"甲/m2": {ModelInfo: llm.ModelInfo{Provider: "甲", ID: "m2", Name: "甲二号"}},
			"乙/n1": {ModelInfo: llm.ModelInfo{Provider: "乙", ID: "n1", Name: "乙一号"}},
		},
	}
	runtime := llm.NewRuntime(llm.RuntimeOptions{})
	if _, err := runtime.RegisterAdapter(
		t.Context(), owner, []string{"甲", "乙"}, adapter); err != nil {
		t.Fatalf("注册适配器失败：%v", err)
	}
	return runtime, owner
}

// liveSession 建一条空的活会话，好让那份耐久的授权表有地方落。
func liveSession(t *testing.T, id sessionlog.SessionID, options coresession.CreateOptions) *coresession.Session {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	live, err := store.Create(t.Context(), owner, id, options)
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	return live
}

// selectionRegistry 造一个只登着那份授权表的投影注册表。
func selectionRegistry(t *testing.T) *projection.Registry {
	t.Helper()
	registry := projection.NewRegistry()
	undo, err := RegisterModelSelectionProjection(registry)
	if err != nil {
		t.Fatalf("登记授权表投影失败：%v", err)
	}
	t.Cleanup(undo)
	return registry
}

// pointer 把一个字面量变成指针，好写那几个可缺的请求字段。
func pointer[T any](value T) *T { return &value }

// ---- 那张表本身 ----

func TestAssertAllowedRoutesRefusesMalformedEntries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		routes []AllowedRoute
	}{
		{"提供方空着", []AllowedRoute{{Provider: "", Model: "m1"}}},
		{"模型空着", []AllowedRoute{{Provider: "甲", Model: ""}}},
		{"同一条写了两遍", []AllowedRoute{{Provider: "甲", Model: "m1"}, {Provider: "甲", Model: "m1"}}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			if err := AssertAllowedRoutes(item.routes); err == nil {
				t.Fatal("这份表验过去了")
			}
		})
	}
}

func TestAssertAllowedRoutesAcceptsDistinctRoutes(t *testing.T) {
	t.Parallel()

	routes := []AllowedRoute{{Provider: "甲", Model: "m1"}, {Provider: "甲", Model: "m2"}, {Provider: "乙", Model: "m1"}}
	if err := AssertAllowedRoutes(routes); err != nil {
		t.Fatalf("这份表被拒了：%v", err)
	}
}

// ---- 并那三个字段 ----

func TestRequestedAgentOptionsRefusesABrokenRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		enabled bool
		request modelRequest
	}{
		{"没开却挑了", false, modelRequest{Provider: pointer("甲"), Model: pointer("m1")}},
		{"只给了提供方", true, modelRequest{Provider: pointer("甲")}},
		{"只给了模型", true, modelRequest{Model: pointer("m1")}},
		{"提供方是空串", true, modelRequest{Provider: pointer(""), Model: pointer("m1")}},
		{"模型是空串", true, modelRequest{Provider: pointer("甲"), Model: pointer("")}},
		{"档位是空串", true, modelRequest{ReasoningEffort: pointer("")}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			_, err := requestedAgentOptions(agent.Options{}, agent.Options{}, item.request, item.enabled)
			if err == nil {
				t.Fatal("这次请求被放行了")
			}
		})
	}
}

// TestRequestedAgentOptionsPassesThroughWhenNothingWasAsked 钉住那条边：模型一个字
// 都没说的时候，交回来的就是装配写死的那一份，一个字节都不改。
func TestRequestedAgentOptionsPassesThroughWhenNothingWasAsked(t *testing.T) {
	t.Parallel()

	configured := agent.Options{Provider: "甲", Model: "m1", ReasoningEffort: "high"}
	// 这里连 enabled 都是假的：没挑就不受那道开关管。
	resolved, err := requestedAgentOptions(agent.Options{}, configured, modelRequest{}, false)
	if err != nil {
		t.Fatalf("一次纯继承被拒了：%v", err)
	}
	if resolved != configured {
		t.Fatalf("装配写死的那份被改成了 %+v", resolved)
	}
}

func TestRequestedAgentOptionsClearsTheEffortWhenTheRouteChanges(t *testing.T) {
	t.Parallel()

	configured := agent.Options{Provider: "甲", Model: "m1", ReasoningEffort: "high"}
	resolved, err := requestedAgentOptions(
		agent.Options{}, configured,
		modelRequest{Provider: pointer("乙"), Model: pointer("n1")}, true)
	if err != nil {
		t.Fatalf("换路由被拒了：%v", err)
	}
	if resolved.ReasoningEffort != "" {
		t.Fatalf("上一条路由的档位被留下了：%q", resolved.ReasoningEffort)
	}
	if resolved.Provider != "乙" || resolved.Model != "n1" {
		t.Fatalf("新路由没落进去：%+v", resolved)
	}
}

// TestRequestedAgentOptionsKeepsANamedEffortAcrossARouteChange 钉住反面：档位是模型
// 自己点的名，那就不许因为路由变了而被清掉。
func TestRequestedAgentOptionsKeepsANamedEffortAcrossARouteChange(t *testing.T) {
	t.Parallel()

	configured := agent.Options{Provider: "甲", Model: "m1", ReasoningEffort: "high"}
	resolved, err := requestedAgentOptions(
		agent.Options{}, configured,
		modelRequest{Provider: pointer("乙"), Model: pointer("n1"), ReasoningEffort: pointer("low")}, true)
	if err != nil {
		t.Fatalf("换路由被拒了：%v", err)
	}
	if resolved.ReasoningEffort != "low" {
		t.Fatalf("模型点名的档位没落进去：%q", resolved.ReasoningEffort)
	}
}

// TestRequestedAgentOptionsTakesTheBaselineFromTheParent 钉住那条边：装配一个路由值
// 都没写死时，「路由变没变」是拿**父**那条路由比的——否则每一次显式挑选都会被算成
// 一次换路由，把继承来的档位白白清掉。
func TestRequestedAgentOptionsTakesTheBaselineFromTheParent(t *testing.T) {
	t.Parallel()

	parent := agent.Options{Provider: "甲", Model: "m1"}
	configured := agent.Options{ReasoningEffort: "high"}
	resolved, err := requestedAgentOptions(
		parent, configured,
		modelRequest{Provider: pointer("甲"), Model: pointer("m1")}, true)
	if err != nil {
		t.Fatalf("同一条路由被拒了：%v", err)
	}
	if resolved.ReasoningEffort != "high" {
		t.Fatalf("路由没变却把档位清掉了：%q", resolved.ReasoningEffort)
	}
}

// ---- 那张表说了算 ----

func TestAssertAllowedModelSelectionLetsPureInheritanceThrough(t *testing.T) {
	t.Parallel()

	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	parent := agent.Options{Provider: "乙", Model: "n1"}
	// 父跑在一条不在表里的路由上，而这次派发一个字都没挑：它不受这张表管。
	if err := assertAllowedModelSelection(policy, parent, parent, modelRequest{}); err != nil {
		t.Fatalf("一次纯继承被这张表拒了：%v", err)
	}
}

func TestAssertAllowedModelSelectionRefusesAnUnlistedRoute(t *testing.T) {
	t.Parallel()

	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	requested := agent.Options{Provider: "乙", Model: "n1"}
	err := assertAllowedModelSelection(policy, agent.Options{}, requested,
		modelRequest{Provider: pointer("乙"), Model: pointer("n1")})
	if err == nil {
		t.Fatal("一条没被授权的路由过去了")
	}
	if !strings.Contains(err.Error(), "乙/n1") {
		t.Fatalf("那句话没点出是哪条路由：%v", err)
	}
}

// TestAssertAllowedModelSelectionChecksTheInheritedHalf 钉住那条边：只点了档位的时候，
// 生效的那条路由是从父那份补齐的，检查的也得是补齐之后的那一条。
func TestAssertAllowedModelSelectionChecksTheInheritedHalf(t *testing.T) {
	t.Parallel()

	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	parent := agent.Options{Provider: "乙", Model: "n1"}
	err := assertAllowedModelSelection(policy, parent, agent.Options{ReasoningEffort: "low"},
		modelRequest{ReasoningEffort: pointer("low")})
	if err == nil {
		t.Fatal("父那条没被授权的路由被一次点档位带过去了")
	}
}

func TestAssertAllowedModelSelectionNeedsAnEffectiveRoute(t *testing.T) {
	t.Parallel()

	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	err := assertAllowedModelSelection(policy, agent.Options{}, agent.Options{ReasoningEffort: "low"},
		modelRequest{ReasoningEffort: pointer("low")})
	if err == nil {
		t.Fatal("父子两边都没有路由，这次挑选还是过去了")
	}
}

// ---- 预检 ----

func TestPreflightChildRouteAcceptsAnAdvertisedEffort(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	err := preflightChildRoute(t.Context(), runtime,
		agent.Options{}, agent.Options{Provider: "甲", Model: "m1", ReasoningEffort: "high"}, true)
	if err != nil {
		t.Fatalf("一条公告过的路由被拒了：%v", err)
	}
}

func TestPreflightChildRouteRefusesAnUnsupportedEffort(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	err := preflightChildRoute(t.Context(), runtime,
		agent.Options{}, agent.Options{Provider: "乙", Model: "n1", ReasoningEffort: "high"}, true)
	if err == nil {
		t.Fatal("一档这个模型不认的档位过去了")
	}
}

// TestPreflightChildRouteDropsTheInheritedEffortAcrossARouteChange 是那条边的成败版本：
// 父跑在 high 上，孩子换到一个不认 high 的模型。继承那一档的话这次预检必然失败——
// 它没失败，说明换路由确实把继承那一步关掉了。
func TestPreflightChildRouteDropsTheInheritedEffortAcrossARouteChange(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	parent := agent.Options{Provider: "甲", Model: "m1", ReasoningEffort: "high"}
	if err := preflightChildRoute(t.Context(), runtime,
		parent, agent.Options{Provider: "乙", Model: "n1"}, true); err != nil {
		t.Fatalf("换路由之后还在继承上一条路由的档位：%v", err)
	}
	// 反面：路由没变的时候那一档要继承过来，而 m2 不认 high。
	if err := preflightChildRoute(t.Context(), runtime,
		agent.Options{Provider: "甲", Model: "m2", ReasoningEffort: "high"},
		agent.Options{}, true); err == nil {
		t.Fatal("路由没变却没继承父那一档")
	}
}

func TestPreflightChildRouteNeedsAnEffectiveRoute(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	if err := preflightChildRoute(t.Context(), runtime,
		agent.Options{}, agent.Options{ReasoningEffort: "high"}, true); err == nil {
		t.Fatal("没有生效路由也预检过去了")
	}
}

// ---- 那份耐久的表 ----

func TestModelSelectionPolicyIsWriteOnce(t *testing.T) {
	t.Parallel()

	registry := selectionRegistry(t)
	live := liveSession(t, "s1", coresession.CreateOptions{})

	first := []AllowedRoute{{Provider: "甲", Model: "m1"}}
	if err := recordModelSelection(registry, live, first); err != nil {
		t.Fatalf("记授权表失败：%v", err)
	}
	// 第二条事件是后来者，折叠必须把它丢掉。
	state := modelSelectionState{Routes: first}
	payload, err := json.Marshal(ModelSelectionPolicyData{
		AllowedModels: []AllowedRoute{{Provider: "乙", Model: "n1"}},
	})
	if err != nil {
		t.Fatalf("排 JSON 失败：%v", err)
	}
	next, changed := applyModelSelection(state, sessionlog.Event{Type: EventModelSelectionPolicy, Data: payload})
	if changed {
		t.Fatal("第二条事件把已经定下来的表改掉了")
	}
	if len(next.Routes) != 1 || next.Routes[0].Provider != "甲" {
		t.Fatalf("表被改成了 %+v", next.Routes)
	}
}

func TestApplyModelSelectionFoldsABadPayloadToNothingRecorded(t *testing.T) {
	t.Parallel()

	empty, err := json.Marshal(ModelSelectionPolicyData{})
	if err != nil {
		t.Fatalf("排 JSON 失败：%v", err)
	}
	repeated, err := json.Marshal(ModelSelectionPolicyData{AllowedModels: []AllowedRoute{
		{Provider: "甲", Model: "m1"}, {Provider: "甲", Model: "m1"},
	}})
	if err != nil {
		t.Fatalf("排 JSON 失败：%v", err)
	}
	cases := []struct {
		name string
		data json.RawMessage
	}{
		{"解不开的负载", json.RawMessage(`"不是一个对象"`)},
		{"空表", empty},
		{"重复条目", repeated},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			state, changed := applyModelSelection(modelSelectionState{},
				sessionlog.Event{Type: EventModelSelectionPolicy, Data: item.data})
			if changed {
				t.Fatal("一份坏负载被折进去了")
			}
			if state.Routes != nil {
				t.Fatalf("折成了一张表 %+v，而不是「还没记过」", state.Routes)
			}
		})
	}
}

func TestModelSelectionPolicyOfHandsBackADetachedCopy(t *testing.T) {
	t.Parallel()

	registry := selectionRegistry(t)
	live := liveSession(t, "s1", coresession.CreateOptions{})
	if err := recordModelSelection(registry, live, []AllowedRoute{{Provider: "甲", Model: "m1"}}); err != nil {
		t.Fatalf("记授权表失败：%v", err)
	}
	// 折叠是懒的，而这条日志是记完之后才读的：读之前先把这条事件驱进去。
	registry.Drive(liveView{live}, live.Events()[len(live.Events())-1])

	policy := ModelSelectionPolicyOf(registry, live)
	if policy == nil || len(policy.Routes) != 1 {
		t.Fatalf("读回来的表是 %+v", policy)
	}
	policy.Routes[0].Model = "被改过了"
	again := ModelSelectionPolicyOf(registry, live)
	if again.Routes[0].Model != "m1" {
		t.Fatalf("交出去的是活引用，被改成了 %q", again.Routes[0].Model)
	}
}

func TestRecordModelSelectionWritesOnlyOnce(t *testing.T) {
	t.Parallel()

	registry := selectionRegistry(t)
	live := liveSession(t, "s1", coresession.CreateOptions{})
	routes := []AllowedRoute{{Provider: "甲", Model: "m1"}}
	if err := recordModelSelection(registry, live, routes); err != nil {
		t.Fatalf("记授权表失败：%v", err)
	}
	registry.Drive(liveView{live}, live.Events()[len(live.Events())-1])
	if err := recordModelSelection(registry, live, routes); err != nil {
		t.Fatalf("第二次记失败：%v", err)
	}
	count := 0
	for _, event := range live.Events() {
		if event.Type == EventModelSelectionPolicy {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("这条日志上有 %d 条授权表事件", count)
	}
}

func TestModelSelectionEventTypesNamesTheOnlyEventThisPackageWrites(t *testing.T) {
	t.Parallel()

	types := ModelSelectionEventTypes()
	if len(types) != 1 || types[0] != EventModelSelectionPolicy {
		t.Fatalf("那份词汇表是 %v", types)
	}
}

// ---- 用户设置 ----

func TestModelSelectionSettingsRefusesAnEnabledEmptyTable(t *testing.T) {
	t.Parallel()

	if _, _, err := NewModelSelectionService(ModelSelectionConfig{Enabled: true}); err == nil {
		t.Fatal("开着却一条路由都没给，这份设置装上了")
	}
}

func TestModelSelectionServiceHandsBackADetachedCopy(t *testing.T) {
	t.Parallel()

	service, undo, err := NewModelSelectionService(ModelSelectionConfig{
		Enabled:       true,
		AllowedModels: []AllowedRoute{{Provider: "甲", Model: "m1"}},
	})
	if err != nil {
		t.Fatalf("造模型选择设置服务失败：%v", err)
	}
	t.Cleanup(undo)

	current := service.Current()
	current.AllowedModels[0].Model = "被改过了"
	if service.Current().AllowedModels[0].Model != "m1" {
		t.Fatal("交出去的是活引用")
	}
}

// TestModelSelectionServiceStaysOffByDefault 钉住那条边：发出去的那套组装默认不开。
func TestModelSelectionServiceStaysOffByDefault(t *testing.T) {
	t.Parallel()

	service, undo, err := NewModelSelectionService(ModelSelectionConfig{})
	if err != nil {
		t.Fatalf("造模型选择设置服务失败：%v", err)
	}
	t.Cleanup(undo)
	if service.Current().Enabled {
		t.Fatal("默认就开着")
	}
}

// ---- 那件发现工具 ----

func TestDiscoverRoutesListsOnlyAllowedProviders(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	text, err := discoverRoutes(t.Context(), runtime, policy, listModelsArgs{})
	if err != nil {
		t.Fatalf("列提供方失败：%v", err)
	}
	if !strings.Contains(text, "甲") {
		t.Fatalf("被授权的提供方没列出来：%q", text)
	}
	if strings.Contains(text, "乙") {
		t.Fatalf("一个没被授权的提供方被说出去了：%q", text)
	}
}

func TestDiscoverRoutesListsOnlyAllowedModels(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	text, err := discoverRoutes(t.Context(), runtime, policy, listModelsArgs{Provider: pointer("甲")})
	if err != nil {
		t.Fatalf("列模型失败：%v", err)
	}
	if !strings.Contains(text, "甲/m1") {
		t.Fatalf("被授权的模型没列出来：%q", text)
	}
	if strings.Contains(text, "m2") {
		t.Fatalf("一个没被授权的模型被说出去了：%q", text)
	}
}

func TestDiscoverRoutesInspectsAnExactModel(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	text, err := discoverRoutes(t.Context(), runtime, policy,
		listModelsArgs{Provider: pointer("甲"), Model: pointer("m1")})
	if err != nil {
		t.Fatalf("看一条精确路由失败：%v", err)
	}
	for _, want := range []string{effortsSectionHeading, "high (default)", "low"} {
		if !strings.Contains(text, want) {
			t.Fatalf("答案里没有 %q：%q", want, text)
		}
	}
}

func TestDiscoverRoutesRefusesWhatThePolicyDoesNotCover(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	cases := []struct {
		name string
		args listModelsArgs
	}{
		{"没被授权的提供方", listModelsArgs{Provider: pointer("乙")}},
		{"没被授权的模型", listModelsArgs{Provider: pointer("甲"), Model: pointer("m2")}},
		{"只给了模型", listModelsArgs{Model: pointer("m1")}},
		{"提供方是空串", listModelsArgs{Provider: pointer("")}},
		{"模型是空串", listModelsArgs{Provider: pointer("甲"), Model: pointer("")}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			if _, err := discoverRoutes(t.Context(), runtime, policy, item.args); err == nil {
				t.Fatal("这次问询被放行了")
			}
		})
	}
}

// TestDiscoverRoutesKeepsUnallowedNamesOutOfTheDiagnostic 钉住那条边：一条被授权、
// 但此刻没登记的提供方，报错时那句「你还可以选这些」只许说被授权的那些。
func TestDiscoverRoutesKeepsUnallowedNamesOutOfTheDiagnostic(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{
		{Provider: "甲", Model: "m1"},
		{Provider: "丙", Model: "x1"},
	}}
	_, err := discoverRoutes(t.Context(), runtime, policy, listModelsArgs{Provider: pointer("丙")})
	if err == nil {
		t.Fatal("一条没登记的提供方问出了答案")
	}
	if strings.Contains(err.Error(), "乙") {
		t.Fatalf("那句诊断把没被授权的提供方说出去了：%v", err)
	}
}

func TestDiscoverRoutesSaysSoWithoutAnLLMService(t *testing.T) {
	t.Parallel()

	_, err := discoverRoutes(t.Context(), nil, &ModelSelectionPolicy{}, listModelsArgs{})
	if err == nil || !strings.Contains(err.Error(), "`llm` service is unavailable") {
		t.Fatalf("缺 llm 服务时报的是：%v", err)
	}
}

// TestListModelsToolRendersItsText 钉住那件工具那一整圈：参数解出来、答案排成一段
// 文本、再被渲染回一个文本块。
func TestListModelsToolRendersItsText(t *testing.T) {
	t.Parallel()

	runtime, _ := catalogRuntime(t)
	policy := &ModelSelectionPolicy{Routes: []AllowedRoute{{Provider: "甲", Model: "m1"}}}
	definition := newListModelsTool(runtime, policy)
	if definition.Name != ListModelsToolName {
		t.Fatalf("那件工具叫 %q", definition.Name)
	}
	value, err := definition.Execute(t.Context(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("跑那件工具失败：%v", err)
	}
	content, err := definition.Output.Render(json.RawMessage(`{}`), value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if !strings.Contains(textOf(content), "甲") {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
}

// ---- 装到一个 agent 上 ----

// routedAgent 是一个手上有活会话的假 agent：那份耐久的授权表要落在它那条日志上。
type routedAgent struct {
	stubAgent
	live *coresession.Session
}

func (a *routedAgent) Session() *coresession.Session { return a.live }

// selectionWorld 是这一批用例的台面：一个开着模型选择的世界。
type selectionWorld struct {
	*world
	live        *coresession.Session
	projections *projection.Registry
	settings    *ModelSelectionService
	llm         *llm.Runtime
}

// newSelectionWorld 把那四样协作者都备好，并让那个假提供方管得住孩子的 agent 选项。
func newSelectionWorld(t *testing.T, allowed []AllowedRoute) *selectionWorld {
	t.Helper()
	base := newWorld(t)
	base.subagents.provider = &stubProvider{
		name: "spawn",
		caps: subagent.Capabilities{DepthLimit: true, AgentOptions: true},
	}
	settings, undo, err := NewModelSelectionService(ModelSelectionConfig{
		Enabled:       len(allowed) > 0,
		AllowedModels: allowed,
	})
	if err != nil {
		t.Fatalf("造模型选择设置服务失败：%v", err)
	}
	t.Cleanup(undo)
	runtime, _ := catalogRuntime(t)
	return &selectionWorld{
		world:       base,
		live:        liveSession(t, "caller", coresession.CreateOptions{}),
		projections: selectionRegistry(t),
		settings:    settings,
		llm:         runtime,
	}
}

// deps 是那份开着模型选择的依赖。
func (w *selectionWorld) deps() Deps {
	deps := w.world.deps()
	deps.Agent = &routedAgent{stubAgent: stubAgent{id: "caller", own: w.agentScope}, live: w.live}
	deps.Projections = w.projections
	deps.ModelSelection = w.settings
	deps.LLM = w.llm
	return deps
}

// install 装一个开着模型选择的控制器。
func (w *selectionWorld) install(t *testing.T) *Controller {
	t.Helper()
	controller := w.controller(func(c *Config) { c.ModelSelectionSettings = true })
	undo, err := controller.Install(t.Context(), w.root, w.deps())
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	return controller
}

func TestInstallRefusesModelSelectionWithoutItsCollaborators(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		shape func(*Deps)
	}{
		{"没有投影注册表", func(d *Deps) { d.Projections = nil }},
		{"没有设置服务", func(d *Deps) { d.ModelSelection = nil }},
		{"没点名这次装配属于哪个 agent", func(d *Deps) { d.Agent = nil }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			w := newSelectionWorld(t, []AllowedRoute{{Provider: "甲", Model: "m1"}})
			deps := w.deps()
			item.shape(&deps)
			controller := w.controller(func(c *Config) { c.ModelSelectionSettings = true })
			if _, err := controller.Install(t.Context(), w.root, deps); err == nil {
				t.Fatal("缺着协作者也装上了")
			}
		})
	}
}

// TestInstallRefusesAProviderThatCannotCarryAgentOptions 钉住那条边：一个管不住孩子
// agent 选项的提供方会把模型挑的那条路由静默丢掉，所以它得在装的那一刻就大声失败。
func TestInstallRefusesAProviderThatCannotCarryAgentOptions(t *testing.T) {
	t.Parallel()

	w := newSelectionWorld(t, []AllowedRoute{{Provider: "甲", Model: "m1"}})
	w.subagents.provider = &stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}}
	controller := w.controller(func(c *Config) { c.ModelSelectionSettings = true })
	if _, err := controller.Install(t.Context(), w.root, w.deps()); err == nil {
		t.Fatal("一个管不住孩子 agent 选项的提供方装上了")
	}
}

func TestInstallOpensTheRouteParametersWhenThePolicyAllows(t *testing.T) {
	t.Parallel()

	w := newSelectionWorld(t, []AllowedRoute{{Provider: "甲", Model: "m1"}})
	w.install(t)

	definition, visible := w.tools.Get(DefaultToolName, w.root.Key())
	if !visible {
		t.Fatal("那件派发工具没装上")
	}
	named := map[string]bool{}
	for _, property := range definition.Parameters.Properties {
		named[property.Name] = true
	}
	for _, want := range []string{"provider", "model", "reasoning_effort"} {
		if !named[want] {
			t.Fatalf("那件工具没露出 %q", want)
		}
	}
	if !strings.Contains(definition.Description, "list_subagent_models") {
		t.Fatalf("工具说明里没提那件发现工具：%q", definition.Description)
	}
	if _, found := w.tools.Get(ListModelsToolName, w.root.Key()); !found {
		t.Fatal("那件发现工具没装上")
	}
}

// TestInstallKeepsTheRouteParametersHiddenWithoutAPolicy 钉住反面：设置关着的时候
// 那三个参数和那件发现工具都不该出现——一个永远用不了的参数只会诱着模型去填它。
func TestInstallKeepsTheRouteParametersHiddenWithoutAPolicy(t *testing.T) {
	t.Parallel()

	w := newSelectionWorld(t, nil)
	w.install(t)

	definition, visible := w.tools.Get(DefaultToolName, w.root.Key())
	if !visible {
		t.Fatal("那件派发工具没装上")
	}
	for _, property := range definition.Parameters.Properties {
		switch property.Name {
		case "provider", "model", "reasoning_effort":
			t.Fatalf("关着的时候还露出了 %q", property.Name)
		}
	}
	if _, found := w.tools.Get(ListModelsToolName, w.root.Key()); found {
		t.Fatal("关着的时候还装上了那件发现工具")
	}
}

// TestInstallRecordsThePolicyOnTheSession 钉住那条边：挑出来的那张表当场就记进这条
// 会话的日志——一条恢复回来的会话必须跑它当初被授的那张表。
func TestInstallRecordsThePolicyOnTheSession(t *testing.T) {
	t.Parallel()

	w := newSelectionWorld(t, []AllowedRoute{{Provider: "甲", Model: "m1"}})
	w.install(t)

	var recorded []AllowedRoute
	for _, event := range w.live.Events() {
		if event.Type != EventModelSelectionPolicy {
			continue
		}
		var data ModelSelectionPolicyData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("解那条事件失败：%v", err)
		}
		recorded = data.AllowedModels
	}
	if len(recorded) != 1 || recorded[0] != (AllowedRoute{Provider: "甲", Model: "m1"}) {
		t.Fatalf("这条日志上记下来的是 %+v", recorded)
	}
}

// TestInstallInheritsThePolicyFromTheParentSession 钉住那条边：一个孩子跟它父那份表走，
// 而不是回头去读此刻的用户设置——那份设置随时会被改，改了不该松开一个跑着的孩子。
func TestInstallInheritsThePolicyFromTheParentSession(t *testing.T) {
	t.Parallel()

	w := newSelectionWorld(t, []AllowedRoute{{Provider: "乙", Model: "n1"}})
	parentLive := liveSession(t, "parent", coresession.CreateOptions{})
	if err := recordModelSelection(w.projections, parentLive,
		[]AllowedRoute{{Provider: "甲", Model: "m1"}}); err != nil {
		t.Fatalf("给父记授权表失败：%v", err)
	}
	w.projections.Drive(liveView{parentLive}, parentLive.Events()[len(parentLive.Events())-1])

	w.live = liveSession(t, "child", coresession.CreateOptions{
		Origin:        sessionlog.OriginSubagent,
		ParentSession: "parent",
	})
	deps := w.deps()
	deps.Agents = stubAgents{"parent": &routedAgent{
		stubAgent: stubAgent{id: "parent", own: w.agentScope}, live: parentLive}}

	controller := w.controller(func(c *Config) { c.ModelSelectionSettings = true })
	undo, err := controller.Install(t.Context(), w.root, deps)
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	if controller.policy == nil || len(controller.policy.Routes) != 1 {
		t.Fatalf("挑出来的表是 %+v", controller.policy)
	}
	if controller.policy.Routes[0].Provider != "甲" {
		t.Fatalf("孩子拿到的是用户设置那份，不是父那份：%+v", controller.policy.Routes)
	}
}

// stubAgents 是那张按会话 id 查回去的假活 agent 表。
type stubAgents map[sessionlog.SessionID]agent.Agent

func (a stubAgents) Get(id sessionlog.SessionID) (agent.Agent, bool) {
	found, present := a[id]
	return found, present
}
