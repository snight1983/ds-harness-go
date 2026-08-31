// 本文件的作用：验运行时那条从「问元数据」到「把请求交给适配器」的路——
// 目录与确切模型的校验、调用配置解算、准备好的调用，以及流式派发本身。
// 登记与拓扑那一半在 runtime_test.go。

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
)

// ---- 参考目录 ----

// TestListModelsReturnsACleanCatalog 走一遍正常那条：交出来的是复制品，
// 改它不该回流进适配器手上那份。
func TestListModelsReturnsACleanCatalog(t *testing.T) {
	runtime := newTestRuntime(t)
	catalog := []ModelInfo{
		{Provider: "甲", ID: "m-1", Name: "一号", InputModalities: []ModelModality{ModalityText}},
		{Provider: "甲", ID: "m-2", Name: "二号"},
	}
	registerFake(t, runtime, "甲", &fakeAdapter{
		listModels: func(context.Context, string) ([]ModelInfo, error) { return catalog, nil },
	})

	models, err := runtime.ListModels(t.Context(), "甲")
	if err != nil {
		t.Fatalf("列模型失败：%v", err)
	}
	if len(models) != 2 || models[0].ID != "m-1" || models[1].ID != "m-2" {
		t.Fatalf("目录不对：%+v", models)
	}
	models[0].InputModalities[0] = ModalityImage
	if catalog[0].InputModalities[0] != ModalityText {
		t.Fatal("交出去的该是复制品，改它不该回流进适配器那份")
	}
}

// TestListModelsRejectsBadCatalogEntries 走一遍目录那四种不合格。这四条判据合起来
// 说的是同一件事：界面拿这份清单让人点，一条对不上路由、没有身份、没有名字、
// 或者和别人同名的条目，点下去之后没有确定的含义。
func TestListModelsRejectsBadCatalogEntries(t *testing.T) {
	cases := map[string][]ModelInfo{
		"路由对不上": {{Provider: "乙", ID: "m-1", Name: "一号"}},
		"没有身份":  {{Provider: "甲", ID: "", Name: "一号"}},
		"没有名字":  {{Provider: "甲", ID: "m-1", Name: ""}},
		"身份重复":  {{Provider: "甲", ID: "m-1", Name: "一号"}, {Provider: "甲", ID: "m-1", Name: "又一个"}},
	}
	for name, catalog := range cases {
		t.Run(name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			registerFake(t, runtime, "甲", &fakeAdapter{
				listModels: func(context.Context, string) ([]ModelInfo, error) { return catalog, nil },
			})
			if _, err := runtime.ListModels(t.Context(), "甲"); codeOf(t, err) != InvalidCatalogCode {
				t.Fatalf("该报 INVALID_CATALOG，得到 %v", err)
			}
		})
	}
}

// TestListModelsPropagatesFailures 钉住没有适配器和适配器自己报错这两条都原样带出来。
func TestListModelsPropagatesFailures(t *testing.T) {
	runtime := newTestRuntime(t)
	if _, err := runtime.ListModels(t.Context(), "没登记过"); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("该报 NO_ADAPTER，得到 %v", err)
	}

	boom := errors.New("目录读不出来")
	registerFake(t, runtime, "甲", &fakeAdapter{
		listModels: func(context.Context, string) ([]ModelInfo, error) { return nil, boom },
	})
	if _, err := runtime.ListModels(t.Context(), "甲"); !errors.Is(err, boom) {
		t.Fatalf("适配器的失败该原样带出来，得到 %v", err)
	}
}

// ---- 确切模型元数据 ----

// resolvingRuntime 造一个只在 ResolveModel 上做文章的运行时。
func resolvingRuntime(
	t *testing.T,
	resolve func(ctx context.Context, provider, model string) (ResolvedModelInfo, error),
) *Runtime {
	t.Helper()
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{resolveModel: resolve})
	return runtime
}

// fixedModel 造一个不管问什么都交出同一份结果的解算函数。
func fixedModel(info ResolvedModelInfo) func(context.Context, string, string) (ResolvedModelInfo, error) {
	return func(context.Context, string, string) (ResolvedModelInfo, error) { return info, nil }
}

// TestResolveModelInfoRejections 走一遍 normalizeModelInfo 的每一条判据。
// 这些都不是「提供方返回了坏数据」——它们是**适配器自己**交回来的元数据，
// 下游的预检、记账和界面全都据此行事，错一个字后面全错。
func TestResolveModelInfoRejections(t *testing.T) {
	good := ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"}
	cases := []struct {
		name string
		info ResolvedModelInfo
		code string
	}{
		{"路由被改掉", ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "乙", ID: "m-1", Name: "一号"}},
			InvalidModelInfoCode},
		{"身份被改掉", ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "甲", ID: "别的", Name: "一号"}},
			InvalidModelInfoCode},
		{"没有名字", ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "甲", ID: "m-1"}},
			InvalidModelInfoCode},
		{"上下文容量不是正数", ResolvedModelInfo{ModelInfo: good, Context: &ModelContext{}},
			InvalidModelContextCode},
		{"默认输出上限是负数", ResolvedModelInfo{ModelInfo: good, DefaultMaxTokens: -1},
			InvalidModelMaxTokensCode},
		{"档位表是空的", ResolvedModelInfo{ModelInfo: good, Reasoning: &ModelReasoningInfo{}},
			InvalidModelReasoningCode},
		{"档位没有身份", ResolvedModelInfo{ModelInfo: good, Reasoning: &ModelReasoningInfo{
			Efforts: []ReasoningEffortInfo{{Name: "低"}}}}, InvalidModelReasoningCode},
		{"档位没有名字", ResolvedModelInfo{ModelInfo: good, Reasoning: &ModelReasoningInfo{
			Efforts: []ReasoningEffortInfo{{ID: "low"}}}}, InvalidModelReasoningCode},
		{"档位重名", ResolvedModelInfo{ModelInfo: good, Reasoning: &ModelReasoningInfo{
			Efforts: []ReasoningEffortInfo{{ID: "low", Name: "低"}, {ID: "low", Name: "又一个低"}}}},
			InvalidModelReasoningCode},
		{"默认档位不在表里", ResolvedModelInfo{ModelInfo: good, Reasoning: &ModelReasoningInfo{
			Efforts: []ReasoningEffortInfo{{ID: "low", Name: "低"}}, DefaultEffort: "high"}},
			InvalidModelReasoningCode},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := resolvingRuntime(t, fixedModel(testCase.info))
			_, err := runtime.ResolveModelInfo(t.Context(), "甲", "m-1")
			if codeOf(t, err) != testCase.code {
				t.Fatalf("该报 %s，得到 %v", testCase.code, err)
			}
		})
	}
}

// TestResolveModelInfoAcceptsAFullyDescribedModel 钉住一份说全了的元数据过得去，
// 而且交出来的是复制品；顺带钉住「默认输出上限为 0」是合法的——Go 这边 0 就是
// 「没配」，不是一个坏值。
func TestResolveModelInfoAcceptsAFullyDescribedModel(t *testing.T) {
	source := ResolvedModelInfo{
		ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号",
			InputModalities: []ModelModality{ModalityText, ModalityImage}},
		Context:   &ModelContext{ContextWindow: 8192},
		Reasoning: &ModelReasoningInfo{Efforts: []ReasoningEffortInfo{{ID: "low", Name: "低"}}},
	}
	runtime := resolvingRuntime(t, fixedModel(source))

	resolved, err := runtime.ResolveModelInfo(t.Context(), "甲", "m-1")
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	resolved.InputModalities[0] = ModalityImage
	if source.InputModalities[0] != ModalityText {
		t.Fatal("交出去的该是复制品")
	}

	if _, err := runtime.ResolveModelInfo(t.Context(), "没登记过", "m-1"); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("该报 NO_ADAPTER，得到 %v", err)
	}
}

// TestResolveModelInfoPropagatesAdapterFailure 钉住适配器自己报的错不被改写。
func TestResolveModelInfoPropagatesAdapterFailure(t *testing.T) {
	boom := errors.New("问不到这个模型")
	runtime := resolvingRuntime(t, func(context.Context, string, string) (ResolvedModelInfo, error) {
		return ResolvedModelInfo{}, boom
	})
	if _, err := runtime.ResolveModelInfo(t.Context(), "甲", "m-1"); !errors.Is(err, boom) {
		t.Fatalf("该原样带出来，得到 %v", err)
	}
}

// ---- 调用配置解算 ----

// TestResolveCallConfigFillsAdapterDefaults 钉住那两处「适配器说了算」的落实：
// 没给输出上限时用模型的默认，没给推理档位时用模型的默认档位。
func TestResolveCallConfigFillsAdapterDefaults(t *testing.T) {
	runtime := resolvingRuntime(t, fixedModel(ResolvedModelInfo{
		ModelInfo:        ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"},
		DefaultMaxTokens: 4096,
		Reasoning: &ModelReasoningInfo{
			Efforts:       []ReasoningEffortInfo{{ID: "low", Name: "低"}, {ID: "high", Name: "高"}},
			DefaultEffort: "low",
		},
	}))

	resolved, err := runtime.ResolveCallConfig(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if resolved.MaxTokens != 4096 {
		t.Fatalf("该用模型的默认输出上限，得到 %d", resolved.MaxTokens)
	}
	if resolved.ReasoningEffort != "low" {
		t.Fatalf("该用模型的默认档位，得到 %q", resolved.ReasoningEffort)
	}

	// 调用方自己给了的一律不覆盖。
	resolved, err = runtime.ResolveCallConfig(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1", MaxTokens: 128, ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if resolved.MaxTokens != 128 || resolved.ReasoningEffort != "high" {
		t.Fatalf("调用方给的值被盖掉了：%+v", resolved)
	}
}

// TestResolveCallConfigRejectsUnsupportedReasoning 钉住不支持的档位在任何提供方
// I/O 之前就被拒——这条判据分两支：模型压根不做推理，和模型做推理但没这一档。
func TestResolveCallConfigRejectsUnsupportedReasoning(t *testing.T) {
	plain := resolvingRuntime(t, fixedModel(ResolvedModelInfo{
		ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"}}))
	_, err := plain.ResolveCallConfig(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1", ReasoningEffort: "high"})
	if codeOf(t, err) != UnsupportedReasoningEffortCode {
		t.Fatalf("不做推理的模型该报 UNSUPPORTED_REASONING_EFFORT，得到 %v", err)
	}

	reasoning := resolvingRuntime(t, fixedModel(ResolvedModelInfo{
		ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"},
		Reasoning: &ModelReasoningInfo{Efforts: []ReasoningEffortInfo{{ID: "low", Name: "低"}}},
	}))
	_, err = reasoning.ResolveCallConfig(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1", ReasoningEffort: "high"})
	if codeOf(t, err) != UnsupportedReasoningEffortCode {
		t.Fatalf("表里没有的档位该被拒，得到 %v", err)
	}
}

// TestResolveCallConfigLeavesEffortUnsetWhenNobodyChose 钉住「模型做推理、但既没
// 默认档位、调用方也没选」时不替任何人做主：这里不做夹取也不做别名替换。
func TestResolveCallConfigLeavesEffortUnsetWhenNobodyChose(t *testing.T) {
	runtime := resolvingRuntime(t, fixedModel(ResolvedModelInfo{
		ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"},
		Reasoning: &ModelReasoningInfo{Efforts: []ReasoningEffortInfo{{ID: "low", Name: "低"}}},
	}))

	resolved, err := runtime.ResolveCallConfig(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if resolved.ReasoningEffort != "" {
		t.Fatalf("没人选过的档位不该被填上，得到 %q", resolved.ReasoningEffort)
	}
}

// TestResolveCallConfigPropagatesLookupFailures 钉住路由和模型两处的失败都带出来。
func TestResolveCallConfigPropagatesLookupFailures(t *testing.T) {
	runtime := newTestRuntime(t)
	if _, err := runtime.ResolveCallConfig(t.Context(),
		CallConfig{Provider: "没登记过", Model: "m-1"}); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("该报 NO_ADAPTER，得到 %v", err)
	}

	bad := resolvingRuntime(t, fixedModel(ResolvedModelInfo{
		ModelInfo: ModelInfo{Provider: "甲", ID: "m-1"}}))
	if _, err := bad.ResolveCallConfig(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1"}); codeOf(t, err) != InvalidModelInfoCode {
		t.Fatalf("该报 INVALID_MODEL_INFO，得到 %v", err)
	}
}

// ---- 准备好的调用 ----

// TestPrepareCallCapturesOneAdapterGeneration 走一遍那五个读取面，并钉住它们交出来的
// 都是复制品——[PreparedCall] 的字段全不导出，访问器就是 Object.freeze 在 Go 里的
// 等价物：拿到它的人读得到每一样，但改不动任何一样。
func TestPrepareCallCapturesOneAdapterGeneration(t *testing.T) {
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{Mode: RetryNormal, MaxRetries: ptr(3)}, "测试")
	if err != nil {
		t.Fatalf("解算重试策略失败：%v", err)
	}
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{
		retryPolicy: func(string) (ResolvedRetryPolicy, bool) { return policy, true },
		resolveModel: fixedModel(ResolvedModelInfo{
			ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号",
				InputModalities: []ModelModality{ModalityText}},
			Context:          &ModelContext{ContextWindow: 8192},
			DefaultMaxTokens: 4096,
			Reasoning: &ModelReasoningInfo{
				Efforts:       []ReasoningEffortInfo{{ID: "low", Name: "低"}},
				DefaultEffort: "low",
			},
		}),
	})

	prepared, err := runtime.PrepareCall(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}

	config := prepared.Config()
	if config.MaxTokens != 4096 || config.ReasoningEffort != "low" {
		t.Fatalf("配置不对：%+v", config)
	}
	if prepared.RetryPolicy().MaxRetries != 3 {
		t.Fatalf("重试策略该随登记一起捕获，得到 %+v", prepared.RetryPolicy())
	}
	modelContext, known := prepared.ModelContext()
	if !known || modelContext.ContextWindow != 8192 {
		t.Fatalf("上下文容量不对：%+v known=%v", modelContext, known)
	}
	defaults := prepared.AdapterDefaults()
	if !defaults.MaxTokens || !defaults.ReasoningEffort {
		t.Fatalf("这两样都是适配器落实的，得到 %+v", defaults)
	}

	modalities := prepared.InputModalities()
	modalities[0] = ModalityImage
	if prepared.InputModalities()[0] != ModalityText {
		t.Fatal("模态清单该是复制品")
	}
}

// TestPrepareCallLeavesUnknownsUnknown 钉住适配器没说的那两样不被编出来：
// 上下文容量的第二个返回值是假，模态清单是 nil；而调用方自己给全了的时候，
// AdapterDefaults 一格都不该亮。
func TestPrepareCallLeavesUnknownsUnknown(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	prepared, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1", MaxTokens: 64})
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	if _, known := prepared.ModelContext(); known {
		t.Fatal("适配器没说上下文容量，第二个返回值该是假")
	}
	if prepared.InputModalities() != nil {
		t.Fatal("适配器没说模态，该是 nil")
	}
	if defaults := prepared.AdapterDefaults(); defaults.MaxTokens || defaults.ReasoningEffort {
		t.Fatalf("这次没有一样是适配器落实的，得到 %+v", defaults)
	}
}

// TestPrepareCallRejections 走一遍准备那几种拒绝。
func TestPrepareCallRejections(t *testing.T) {
	runtime := newTestRuntime(t)
	if _, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "没登记过", Model: "m-1"}); codeOf(t, err) != NoAdapterCode {
		t.Fatalf("该报 NO_ADAPTER，得到 %v", err)
	}

	// 适配器交回来一份没有派发入口的准备：它是一份用不了的准备，必须当场拒掉，
	// 不能留到派发那一刻去解一个 nil。
	registerFake(t, runtime, "甲", &fakeAdapter{
		prepareCall: func(_ context.Context, provider, model string) (PreparedAdapterCall, error) {
			return PreparedAdapterCall{Model: ResolvedModelInfo{
				ModelInfo: ModelInfo{Provider: provider, ID: model, Name: model}}}, nil
		},
	})
	if _, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "甲", Model: "m-1"}); codeOf(t, err) != InvalidAdapterCode {
		t.Fatalf("该报 INVALID_ADAPTER，得到 %v", err)
	}

	boom := errors.New("准备不出来")
	registerFake(t, runtime, "乙", &fakeAdapter{
		prepareCall: func(context.Context, string, string) (PreparedAdapterCall, error) {
			return PreparedAdapterCall{}, boom
		},
	})
	if _, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "乙", Model: "m-1"}); !errors.Is(err, boom) {
		t.Fatalf("适配器的失败该原样带出来，得到 %v", err)
	}

	registerFake(t, runtime, "丙", &fakeAdapter{
		resolveModel: fixedModel(ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "丙", ID: "m-1"}}),
	})
	if _, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "丙", Model: "m-1"}); codeOf(t, err) != InvalidModelInfoCode {
		t.Fatalf("该报 INVALID_MODEL_INFO，得到 %v", err)
	}

	registerFake(t, runtime, "丁", &fakeAdapter{})
	if _, err := runtime.PrepareCall(t.Context(),
		CallConfig{Provider: "丁", Model: "m-1", ReasoningEffort: "high"}); codeOf(t, err) != UnsupportedReasoningEffortCode {
		t.Fatalf("该报 UNSUPPORTED_REASONING_EFFORT，得到 %v", err)
	}
}

// TestPreparedCallDispatchesExactlyOnce 钉住那句「只能派发一次」。一份准备攥着的是
// 某一代适配器的解算结果，用第二次就等于拿旧结果去打新请求。
func TestPreparedCallDispatchesExactlyOnce(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	prepared, err := runtime.PrepareCall(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	options := GenerateOptions{Provider: "甲", Model: "m-1"}

	stream, err := prepared.Stream(t.Context(), options)
	if err != nil {
		t.Fatalf("第一次派发不该失败：%v", err)
	}
	if _, err := drain(t, stream); err != nil {
		t.Fatalf("读流不该出错：%v", err)
	}
	if _, err := prepared.Stream(t.Context(), options); codeOf(t, err) != InvalidPreparedCallCode {
		t.Fatalf("第二次派发该报 INVALID_PREPARED_CALL，得到 %v", err)
	}
}

// TestPreparedCallRejectsDriftedConfig 钉住请求里那几个调用配置字段必须和准备时
// 那一份一致：对不上就说明请求头在准备之后被改过，而这份准备是照旧那份算出来的。
func TestPreparedCallRejectsDriftedConfig(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	prepared, err := runtime.PrepareCall(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	_, err = prepared.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1", MaxTokens: 999})
	if codeOf(t, err) != InvalidPreparedCallCode {
		t.Fatalf("该报 INVALID_PREPARED_CALL，得到 %v", err)
	}
	// 被拒的那一次不算用掉：正确的那次请求还派发得出去。
	if _, err := prepared.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"}); err != nil {
		t.Fatalf("被拒之后该还能正确派发一次：%v", err)
	}
}

// TestPreparedCallKeepsItsOwnAdapterAcrossAHotSwap 钉住这份准备存在的**全部理由**：
// 它从头到尾攥着同一份登记，所以热更新没法把一个适配器的能力结果和另一个适配器
// 凑到一起。
func TestPreparedCallKeepsItsOwnAdapterAcrossAHotSwap(t *testing.T) {
	runtime := newTestRuntime(t)
	original := &fakeAdapter{}
	handle := registerFake(t, runtime, "甲", original)

	prepared, err := runtime.PrepareCall(t.Context(), CallConfig{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}

	if err := handle.Release(t.Context()); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	replacement := &fakeAdapter{}
	registerFake(t, runtime, "甲", replacement)

	stream, err := prepared.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(t, stream); err != nil {
		t.Fatalf("读流不该出错：%v", err)
	}
	if original.seen.Model != "m-1" {
		t.Fatal("该派发到准备时捕获的那个适配器")
	}
	if replacement.seen.Model != "" {
		t.Fatal("换上来的那个适配器不该收到这次请求")
	}
}

// ---- 流式派发 ----

// streamOf 开一条流并读干，用例里最常用的那一下。
func streamOf(t *testing.T, runtime *Runtime, options GenerateOptions) []StreamChunk {
	t.Helper()
	stream, err := runtime.Stream(t.Context(), options)
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	collected, err := drain(t, stream)
	if err != nil {
		t.Fatalf("读流不该出错：%v", err)
	}
	return collected
}

// TestStreamHandsBackAdapterChunks 走一遍最普通的那条流。
func TestStreamHandsBackAdapterChunks(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{
		stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
			return chunks(TextDeltaChunk{Index: 0, Text: "你好"}, FinishChunk{Reason: StopFinish{}}), nil
		},
	})

	collected := streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
	if len(collected) != 2 {
		t.Fatalf("该收到两块，得到 %d 块", len(collected))
	}
	if finishOf(t, collected).FinishKind() != FinishStop {
		t.Fatalf("结局不对：%q", finishOf(t, collected).FinishKind())
	}
}

// TestStreamTurnsAdapterFailuresIntoTerminalChunks 钉住那句「适配器那边的失败一律
// 不走返回值第二格——它们是分块」。选适配器、派发、迭代中途三处各走一遍。
//
// 这条性质是循环那一侧敢于只读流、不去接第二个返回值的全部依据。
func TestStreamTurnsAdapterFailuresIntoTerminalChunks(t *testing.T) {
	t.Run("选不出适配器", func(t *testing.T) {
		runtime := newTestRuntime(t)
		collected := streamOf(t, runtime, GenerateOptions{Provider: "没登记过", Model: "m-1"})
		if failureOf(t, finishOf(t, collected)).Code != NoAdapterCode {
			t.Fatalf("该是一条 NO_ADAPTER 的终止分块，得到 %+v", collected)
		}
	})

	t.Run("派发时就失败", func(t *testing.T) {
		runtime := newTestRuntime(t)
		registerFake(t, runtime, "甲", &fakeAdapter{
			stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
				return nil, NewError("端点不答话", "PROVIDER_DOWN", nil)
			},
		})
		collected := streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
		if failureOf(t, finishOf(t, collected)).Code != "PROVIDER_DOWN" {
			t.Fatalf("该是一条 PROVIDER_DOWN 的终止分块，得到 %+v", collected)
		}
	})

	t.Run("迭代中途失败", func(t *testing.T) {
		runtime := newTestRuntime(t)
		registerFake(t, runtime, "甲", &fakeAdapter{
			stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
				return failingStream(NewError("断在半路", "PROVIDER_DOWN", nil),
					TextDeltaChunk{Index: 0, Text: "说了一半"}), nil
			},
		})
		collected := streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
		if len(collected) != 2 {
			t.Fatalf("说了一半那块该留着，得到 %d 块", len(collected))
		}
		if failureOf(t, finishOf(t, collected)).Code != "PROVIDER_DOWN" {
			t.Fatalf("结局不对：%+v", collected)
		}
	})
}

// TestStreamRejectsABadPreparationAsATerminalChunk 把派发前那四道关各走一遍，
// 钉住它们**也**是终止分块，而不是从 [Runtime.Stream] 的第二格出来。
//
// 这四道关拦的都是适配器自己交出来的东西：准备时就报错、交出一次没有派发入口的
// 调用、交出对不上号的确切模型元数据、以及模型压根不支持调用方点名的那档推理力度。
// 上面那组已经钉住了「适配器那边的失败一律是分块」，但它走的是最里面那一跳；
// 这四条走的是**派发之前**。这条边界一旦被挪到第二格，循环那一侧只读流的写法就会
// 静默地漏掉整整四类失败——流是空的，什么都没发生，也没有人报错。
func TestStreamRejectsABadPreparationAsATerminalChunk(t *testing.T) {
	valid := ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"}}
	emptyStream := func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
		return chunks(), nil
	}

	cases := []struct {
		name        string
		prepareCall func(ctx context.Context, provider, model string) (PreparedAdapterCall, error)
		options     GenerateOptions
		code        string
	}{
		{
			name: "准备时就报错",
			prepareCall: func(context.Context, string, string) (PreparedAdapterCall, error) {
				return PreparedAdapterCall{}, NewError("准备不出来", "PREPARE_FAILED", nil)
			},
			code: "PREPARE_FAILED",
		},
		{
			name: "交出一次没有派发入口的调用",
			prepareCall: func(context.Context, string, string) (PreparedAdapterCall, error) {
				return PreparedAdapterCall{Model: valid}, nil
			},
			code: InvalidAdapterCode,
		},
		{
			name: "确切模型对不上号",
			prepareCall: func(context.Context, string, string) (PreparedAdapterCall, error) {
				return PreparedAdapterCall{
					Model:  ResolvedModelInfo{ModelInfo: ModelInfo{Provider: "甲", ID: "换了一个", Name: "一号"}},
					Stream: emptyStream,
				}, nil
			},
			code: InvalidModelInfoCode,
		},
		{
			name: "模型不支持点名的那档推理力度",
			prepareCall: func(context.Context, string, string) (PreparedAdapterCall, error) {
				return PreparedAdapterCall{Model: valid, Stream: emptyStream}, nil
			},
			options: GenerateOptions{ReasoningEffort: "high"},
			code:    UnsupportedReasoningEffortCode,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			registerFake(t, runtime, "甲", &fakeAdapter{prepareCall: testCase.prepareCall})

			options := testCase.options
			options.Provider, options.Model = "甲", "m-1"
			collected := streamOf(t, runtime, options)
			if got := failureOf(t, finishOf(t, collected)).Code; got != testCase.code {
				t.Fatalf("该是一条 %s 的终止分块，得到 %q（%+v）", testCase.code, got, collected)
			}
		})
	}
}

// TestStreamReportsAbortedRatherThanError 钉住取消和失败分成两种结局。循环要靠
// 这个区分决定「这是一次用户按了停」还是「这次调用挂了、该不该重试」。
func TestStreamReportsAbortedRatherThanError(t *testing.T) {
	t.Run("上下文已取消", func(t *testing.T) {
		runtime := newTestRuntime(t)
		registerFake(t, runtime, "甲", &fakeAdapter{
			stream: func(ctx context.Context, _ GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
				return nil, ctx.Err()
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		stream, err := runtime.Stream(ctx, GenerateOptions{Provider: "甲", Model: "m-1"})
		if err != nil {
			t.Fatalf("开流不该失败：%v", err)
		}
		collected, _ := drain(t, stream)
		if finishOf(t, collected).FinishKind() != FinishAborted {
			t.Fatalf("该是 aborted，得到 %q", finishOf(t, collected).FinishKind())
		}
	})

	t.Run("适配器自己挂了 ABORTED 码", func(t *testing.T) {
		runtime := newTestRuntime(t)
		registerFake(t, runtime, "甲", &fakeAdapter{
			stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
				return nil, NewError("提供方那边取消了", AbortedCode, nil)
			},
		})
		collected := streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
		if finishOf(t, collected).FinishKind() != FinishAborted {
			t.Fatalf("该是 aborted，得到 %q", finishOf(t, collected).FinishKind())
		}
	})
}

// TestStreamDoesNothingUntilItIsRanged 钉住那句「一层中间件拿到这个序列而不去 range
// 它的话，一次提供方往返都不会发生」——这是 DSH 那边 async generator 在第一次
// next() 之前一行都不跑的等价物。
func TestStreamDoesNothingUntilItIsRanged(t *testing.T) {
	runtime := newTestRuntime(t)
	adapter := &fakeAdapter{}
	registerFake(t, runtime, "甲", adapter)

	stream, err := runtime.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	if adapter.seen.Model != "" {
		t.Fatal("还没 range 就已经派发出去了")
	}
	if _, err := drain(t, stream); err != nil {
		t.Fatalf("读流不该出错：%v", err)
	}
	if adapter.seen.Model != "m-1" {
		t.Fatal("range 之后该派发出去")
	}
}

// TestStreamIsSpentAfterOneRange 钉住一条流用掉之后再 range 一次什么都不出，
// 和一个已经跑完的 async generator 再被 for-await 一次的行为一样。
func TestStreamIsSpentAfterOneRange(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	stream, err := runtime.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	if collected, _ := drain(t, stream); len(collected) == 0 {
		t.Fatal("第一遍该读出东西")
	}
	if collected, _ := drain(t, stream); len(collected) != 0 {
		t.Fatalf("用掉之后该什么都不出，得到 %+v", collected)
	}
}

// TestStreamStopsWhenTheConsumerBreaks 钉住消费方提前收手时上游跟着停下来。
func TestStreamStopsWhenTheConsumerBreaks(t *testing.T) {
	runtime := newTestRuntime(t)
	delivered := 0
	registerFake(t, runtime, "甲", &fakeAdapter{
		stream: func(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
			return func(yield func(StreamChunk, error) bool) {
				for index := range 5 {
					delivered++
					if !yield(TextDeltaChunk{Index: 0, Text: string(rune('a' + index))}, nil) {
						return
					}
				}
			}, nil
		},
	})

	stream, err := runtime.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	for range stream {
		break
	}
	if delivered != 1 {
		t.Fatalf("消费方收手之后上游该停下来，实际吐了 %d 块", delivered)
	}
}

// ---- llm/stream 瀑布 ----

// TestStreamRulesRunOutermostFirst 钉住那条次序：先登记的在**外面**，最里面是
// 适配器边界。整个仓库每一条瀑布都是这个次序，读的人不该在这里遇到例外。
func TestStreamRulesRunOutermostFirst(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)
	adapter := &fakeAdapter{}
	registerFake(t, runtime, "甲", adapter)

	var order []string
	rule := func(label string) StreamRule {
		return func(
			ctx context.Context,
			_ GenerateOptions,
			next func(context.Context) (iter.Seq2[StreamChunk, error], error),
		) (iter.Seq2[StreamChunk, error], error) {
			order = append(order, "进"+label)
			stream, err := next(ctx)
			order = append(order, "出"+label)
			return stream, err
		}
	}
	for _, label := range []string{"外", "内"} {
		if _, err := runtime.OnStream(t.Context(), owner, rule(label)); err != nil {
			t.Fatalf("挂中间件失败：%v", err)
		}
	}

	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
	want := []string{"进外", "进内", "出内", "出外"}
	if len(order) != 4 {
		t.Fatalf("次序不对：%v", order)
	}
	for index, step := range want {
		if order[index] != step {
			t.Fatalf("次序该是 %v，得到 %v", want, order)
		}
	}
	if adapter.seen.Model != "m-1" {
		t.Fatal("最里面该是适配器边界")
	}
}

// TestStreamRuleConstructionFailureIsReturned 钉住返回值第二格的用途：它只装
// 瀑布上某一层**构造时**的失败。适配器那边的失败不走这里，见上面那组终止分块。
func TestStreamRuleConstructionFailureIsReturned(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	boom := errors.New("这层装不起来")
	rule := func(
		context.Context,
		GenerateOptions,
		func(context.Context) (iter.Seq2[StreamChunk, error], error),
	) (iter.Seq2[StreamChunk, error], error) {
		return nil, boom
	}
	if _, err := runtime.OnStream(t.Context(), testScope(t), rule); err != nil {
		t.Fatalf("挂中间件失败：%v", err)
	}

	if _, err := runtime.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"}); !errors.Is(err, boom) {
		t.Fatalf("构造失败该从第二格出来，得到 %v", err)
	}
}

// TestStreamRulesStopAfterUndo 钉住撤销之后那一层真的不在瀑布上了。
func TestStreamRulesStopAfterUndo(t *testing.T) {
	runtime := newTestRuntime(t)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	entered := 0
	rule := func(
		ctx context.Context,
		_ GenerateOptions,
		next func(context.Context) (iter.Seq2[StreamChunk, error], error),
	) (iter.Seq2[StreamChunk, error], error) {
		entered++
		return next(ctx)
	}
	undo, err := runtime.OnStream(t.Context(), testScope(t), rule)
	if err != nil {
		t.Fatalf("挂中间件失败：%v", err)
	}

	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
	if entered != 1 {
		t.Fatalf("该走到一次，得到 %d", entered)
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1"})
	if entered != 1 {
		t.Fatalf("撤销之后不该再走到，得到 %d", entered)
	}
}

// ---- 交到适配器手上的那份请求 ----

// TestStreamWritesTheResolvedConfigIntoTheRequest 钉住解算出来的调用配置会盖回
// 请求上再交出去：适配器落实的默认必须在它自己收到的那份请求里看得见，否则
// 日志记的和实际发出去的就是两件事。
func TestStreamWritesTheResolvedConfigIntoTheRequest(t *testing.T) {
	runtime := newTestRuntime(t)
	adapter := &fakeAdapter{
		resolveModel: fixedModel(ResolvedModelInfo{
			ModelInfo:        ModelInfo{Provider: "甲", ID: "m-1", Name: "一号"},
			DefaultMaxTokens: 4096,
			Reasoning: &ModelReasoningInfo{
				Efforts:       []ReasoningEffortInfo{{ID: "low", Name: "低"}},
				DefaultEffort: "low",
			},
		}),
	}
	registerFake(t, runtime, "甲", adapter)

	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1", System: "系统提示"})
	if adapter.seen.MaxTokens != 4096 || adapter.seen.ReasoningEffort != "low" {
		t.Fatalf("解算好的配置没盖回请求：%+v", adapter.seen.CallConfig())
	}
	if adapter.seen.System != "系统提示" {
		t.Fatal("配置以外的字段该原样留着")
	}
}

// TestStreamProjectsImagesOnlyForDeclaredTextOnlyModels 钉住图片投影那条判据：
// 模态清单为 nil 是「适配器没说」，一张图都不投；一份**显式**列出来、却没有图片的
// 清单才是一条否定能力，这时候才把历史里的图片投影成文本。
func TestStreamProjectsImagesOnlyForDeclaredTextOnlyModels(t *testing.T) {
	withImage := []Message{NewUserMessage(
		Content{TextBlock{Text: "看这张"}, ImageBlock{Attachment: sampleRef()}}, UserSource{})}

	cases := []struct {
		name       string
		modalities []ModelModality
		projected  bool
	}{
		{"适配器没说", nil, false},
		{"说了收图", []ModelModality{ModalityText, ModalityImage}, false},
		{"说了只收文本", []ModelModality{ModalityText}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			adapter := &fakeAdapter{
				resolveModel: fixedModel(ResolvedModelInfo{ModelInfo: ModelInfo{
					Provider: "甲", ID: "m-1", Name: "一号", InputModalities: testCase.modalities}}),
			}
			registerFake(t, runtime, "甲", adapter)

			streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1", Messages: withImage})
			if messagesHaveImage(adapter.seen.Messages) == testCase.projected {
				t.Fatalf("投影与否不对：projected=%v", testCase.projected)
			}
			if !messagesHaveImage(withImage) {
				t.Fatal("调用方手上那份历史不该被改动")
			}
		})
	}
}

// TestStreamStripsReplayStateOwnedByAnotherAdapter 钉住重放状态那条归属判据：
// 只有同一个适配器实例同时拥有那条历史路由和这次的目标路由时，那份私有状态才留得下来。
//
// 一份适配器私有的重放状态交到另一个适配器手上，对方要么读不懂、要么读成别的意思，
// 两种都比没有更坏。
func TestStreamStripsReplayStateOwnedByAnotherAdapter(t *testing.T) {
	runtime := newTestRuntime(t)
	adapter := &fakeAdapter{}
	registerFake(t, runtime, "甲", adapter)
	registerFake(t, runtime, "乙", &fakeAdapter{})

	replay := json.RawMessage(`{"私有":1}`)
	messages := []Message{
		NewUserMessage(Content{TextBlock{Text: "问一句"}}, UserSource{}),
		NewAssistantMessage(Content{TextBlock{Text: "自家的"}},
			Provenance{Provider: "甲", Model: "m-1", ReplayState: replay}),
		NewAssistantMessage(Content{TextBlock{Text: "别家的"}},
			Provenance{Provider: "乙", Model: "m-9", ReplayState: replay}),
		NewAssistantMessage(Content{TextBlock{Text: "没登记过的"}},
			Provenance{Provider: "丙", Model: "m-9", ReplayState: replay}),
		NewAssistantMessage(Content{TextBlock{Text: "本来就没有"}},
			Provenance{Provider: "乙", Model: "m-9"}),
	}

	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1", Messages: messages})

	seen := adapter.seen.Messages
	if len(seen) != len(messages) {
		t.Fatalf("消息条数变了：%d", len(seen))
	}
	replayOf := func(index int) json.RawMessage {
		source, isModel := seen[index].ModelSource()
		if !isModel {
			t.Fatalf("第 %d 条该是模型产出的", index)
		}
		return source.ReplayState
	}
	if replayOf(1) == nil {
		t.Fatal("自家路由那条重放状态该留着")
	}
	if replayOf(2) != nil || replayOf(3) != nil {
		t.Fatal("别家和没登记过的路由，重放状态都该摘掉")
	}
	// 摘的时候身份不能跟着丢：下游还要靠它知道这条消息是谁产出的。
	if source, _ := seen[2].ModelSource(); source.Provider != "乙" || source.Model != "m-9" {
		t.Fatalf("摘重放状态时把身份也弄丢了：%+v", source)
	}
	if messages[2].Source.(ModelSource).ReplayState == nil {
		t.Fatal("调用方手上那份历史不该被改动")
	}
}

// TestStreamLeavesHistoryAloneWhenNothingNeedsStripping 钉住没有一条需要摘的时候
// 原样交出去——这条不是优化，它是「不该被改动的东西一个字节都没动」的直接表达。
func TestStreamLeavesHistoryAloneWhenNothingNeedsStripping(t *testing.T) {
	runtime := newTestRuntime(t)
	adapter := &fakeAdapter{}
	registerFake(t, runtime, "甲", adapter)

	messages := []Message{
		NewUserMessage(Content{TextBlock{Text: "问一句"}}, UserSource{}),
		NewAssistantMessage(Content{TextBlock{Text: "答一句"}}, Provenance{Provider: "甲", Model: "m-1"}),
	}
	streamOf(t, runtime, GenerateOptions{Provider: "甲", Model: "m-1", Messages: messages})

	if len(adapter.seen.Messages) != 2 || adapter.seen.Messages[1].ID != messages[1].ID {
		t.Fatalf("历史该原样交过去：%+v", adapter.seen.Messages)
	}
}
