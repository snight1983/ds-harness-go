package llm

import (
	"context"
	"errors"
	"iter"
	"testing"
)

// TestAdapterFallbacksWhenNothingOptionalIsImplemented 走一遍那五个兜底。
// 这条测试守的是本文件开头那句话的下半截：DSH 的抽象基类带着五份默认实现，
// Go 这边把它们搬到了运行时侧，一个只实现 Stream 的适配器必须还是能用。
func TestAdapterFallbacksWhenNothingOptionalIsImplemented(t *testing.T) {
	adapter := &bareAdapter{}

	if info := AdapterProviderInfo(adapter, "acme"); info.ID != "acme" || info.Name != "acme" {
		t.Fatalf("路由元数据该兜底成 id 和名字都是路由名，得到 %+v", info)
	}
	if _, owns := AdapterRetryPolicy(adapter, "acme"); owns {
		t.Fatal("不实现重试策略时该说「用普通默认」")
	}
	models, err := AdapterListModels(t.Context(), adapter, "acme")
	if err != nil || models != nil {
		t.Fatalf("不实现模型清单时该是空清单，得到 %v %v", models, err)
	}
	resolved, err := AdapterResolveModel(t.Context(), adapter, "acme", "m-1")
	if err != nil {
		t.Fatalf("兜底解算不该出错：%v", err)
	}
	if resolved.Provider != "acme" || resolved.ID != "m-1" || resolved.Name != "m-1" {
		t.Fatalf("确切模型该兜底成身份三件套，得到 %+v", resolved.ModelInfo)
	}
	call, err := AdapterPrepareCall(t.Context(), adapter, "acme", "m-1")
	if err != nil {
		t.Fatalf("兜底准备不该出错：%v", err)
	}
	if call.Stream == nil {
		t.Fatal("兜底准备该把派发接到 Adapter.Stream 上")
	}
	if call.Model.ID != "m-1" {
		t.Fatalf("兜底准备该带上兜底解算的那份模型，得到 %+v", call.Model.ModelInfo)
	}
}

// TestAdapterPrepareCallGoesThroughResolveOverride 钉住本文件存在的**全部理由**：
// 兜底的 PrepareCall 必须走**被覆盖之后**的 ResolveModel。照抄成一个可内嵌的
// 基类的话，这条测试会失败——Go 的方法集没有虚派发，基类里那句调用只会调到
// 基类自己那一份。
func TestAdapterPrepareCallGoesThroughResolveOverride(t *testing.T) {
	adapter := &fakeAdapter{
		resolveModel: func(_ context.Context, provider, model string) (ResolvedModelInfo, error) {
			return ResolvedModelInfo{
				ModelInfo:        ModelInfo{Provider: provider, ID: model, Name: "覆盖过的"},
				DefaultMaxTokens: 512,
			}, nil
		},
		// prepareCall 留空，走的就是那份兜底。
	}

	call, err := AdapterPrepareCall(t.Context(), adapter, "acme", "m-1")
	if err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if call.Model.Name != "覆盖过的" || call.Model.DefaultMaxTokens != 512 {
		t.Fatalf("兜底准备该走覆盖后的 ResolveModel，得到 %+v", call.Model)
	}
}

// TestAdapterPrepareCallPropagatesResolveFailure 钉住兜底那一支不吞掉解算的失败。
func TestAdapterPrepareCallPropagatesResolveFailure(t *testing.T) {
	boom := errors.New("解算不出来")
	adapter := &fakeAdapter{
		resolveModel: func(context.Context, string, string) (ResolvedModelInfo, error) {
			return ResolvedModelInfo{}, boom
		},
	}

	if _, err := AdapterPrepareCall(t.Context(), adapter, "acme", "m-1"); !errors.Is(err, boom) {
		t.Fatalf("该把解算的失败带出来，得到 %v", err)
	}
}

// TestAdapterPrepareCallFallbackPropagatesResolveFailure 钉住兜底那一支自己的
// 错误出口：适配器**没有**自备 PrepareCall、而它的 ResolveModel 报了错时，
// 那条错误原样带出来，不会变成一次「模型叫 m-1」的兜底解算蒙混过去。
//
// [TestAdapterPrepareCallPropagatesResolveFailure] 验的是另一条路——那个假适配器
// 的方法集里 PrepareCall 在，走的是它自己那份 prepareCall 里的解算。这里换成一个
// 真的不实现 PrepareCall 的适配器，走的才是 [AdapterPrepareCall] 里的兜底。
func TestAdapterPrepareCallFallbackPropagatesResolveFailure(t *testing.T) {
	boom := errors.New("这个模型不认识")
	adapter := &resolveOnlyAdapter{
		resolveModel: func(context.Context, string, string) (ResolvedModelInfo, error) {
			return ResolvedModelInfo{}, boom
		},
	}

	if _, err := AdapterPrepareCall(t.Context(), adapter, "acme", "m-1"); !errors.Is(err, boom) {
		t.Fatalf("兜底那一支该把解算的失败带出来，得到 %v", err)
	}
}

// TestAdapterOptionalHooksAreUsedWhenPresent 走一遍五个可选实现被采纳的那一路。
func TestAdapterOptionalHooksAreUsedWhenPresent(t *testing.T) {
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{Mode: RetryNormal, MaxRetries: ptr(7)}, "测试")
	if err != nil {
		t.Fatalf("解算重试策略失败：%v", err)
	}
	adapter := &fakeAdapter{
		providerInfo: func(provider string) ProviderInfo {
			return ProviderInfo{ID: provider, Name: "Acme 公司"}
		},
		retryPolicy: func(string) (ResolvedRetryPolicy, bool) { return policy, true },
		listModels: func(context.Context, string) ([]ModelInfo, error) {
			return []ModelInfo{{Provider: "acme", ID: "m-1", Name: "M 一号"}}, nil
		},
		prepareCall: func(_ context.Context, provider, model string) (PreparedAdapterCall, error) {
			return PreparedAdapterCall{
				Model: ResolvedModelInfo{ModelInfo: ModelInfo{Provider: provider, ID: model, Name: "自备的"}},
				Stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
					return chunks(), nil
				},
			}, nil
		},
	}

	if info := AdapterProviderInfo(adapter, "acme"); info.Name != "Acme 公司" {
		t.Fatalf("该用适配器自己那份路由元数据，得到 %+v", info)
	}
	resolved, owns := AdapterRetryPolicy(adapter, "acme")
	if !owns || resolved.MaxRetries != 7 {
		t.Fatalf("该用适配器自己那份重试策略，得到 %+v owns=%v", resolved, owns)
	}
	models, err := AdapterListModels(t.Context(), adapter, "acme")
	if err != nil || len(models) != 1 || models[0].Name != "M 一号" {
		t.Fatalf("该用适配器自己那份模型清单，得到 %v %v", models, err)
	}
	call, err := AdapterPrepareCall(t.Context(), adapter, "acme", "m-1")
	if err != nil || call.Model.Name != "自备的" {
		t.Fatalf("该用适配器自己那份准备，得到 %+v %v", call.Model.ModelInfo, err)
	}
}
