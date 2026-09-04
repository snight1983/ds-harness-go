// 本文件的作用：把一次调用要用的东西解出来——重试策略、模型清单、模型信息，
// 以及一份 CallConfig 补成最终形态。
//
// 源: packages/llm/llm/src/index.ts:262-1026

package llm

import (
	"context"
	"fmt"
	"slices"
)

// ProviderRetryPolicy 交出登记某条路由时捕获下来的那份重试策略。
//
// 源: packages/llm/llm/src/index.ts:588-595
func (r *Runtime) ProviderRetryPolicy(provider string) (ResolvedRetryPolicy, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return ResolvedRetryPolicy{}, err
	}
	return registration.retryPolicy, nil
}

// ListModels 列出某条已登记路由公告的模型。目录成员资格是参考性的，它既不改
// 路由也不改请求校验。
//
// 源: packages/llm/llm/src/index.ts:602-635
func (r *Runtime) ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return nil, err
	}
	models, err := AdapterListModels(ctx, registration.adapter, provider)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	catalog := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		_, duplicate := seen[model.ID]
		if model.Provider != provider || model.ID == "" || model.Name == "" || duplicate {
			return nil, NewError(
				fmt.Sprintf("adapter returned invalid or duplicate model metadata for provider %q", provider),
				InvalidCatalogCode, nil)
		}
		seen[model.ID] = struct{}{}
		catalog = append(catalog, model.Clone())
	}
	return catalog, nil
}

// ResolveModelInfo 解算并校验拥有某条精确路由的那个适配器交出来的全部元数据。
//
// 源: packages/llm/llm/src/index.ts:637-652
func (r *Runtime) ResolveModelInfo(ctx context.Context, provider, model string) (ResolvedModelInfo, error) {
	registration, err := r.registration(provider)
	if err != nil {
		return ResolvedModelInfo{}, err
	}
	return r.resolveModelInfoFor(ctx, registration, model)
}

// 源: packages/llm/llm/src/index.ts:654-661
func (r *Runtime) resolveModelInfoFor(
	ctx context.Context,
	registration *adapterRegistration,
	model string,
) (ResolvedModelInfo, error) {
	resolved, err := AdapterResolveModel(ctx, registration.adapter, registration.provider.ID, model)
	if err != nil {
		return ResolvedModelInfo{}, err
	}
	return normalizeModelInfo(registration.provider.ID, model, resolved)
}

// normalizeModelInfo 校验并摘下适配器交回来的那份确切模型结果。
//
// 源: packages/llm/llm/src/index.ts:663-754
//
// 新增: DSH 那一版里过半的判断是 `typeof x !== 'string'` 之类的类型检查，在 Go 里
// 由编译器管。剩下的是**取值**判断，一条不少：身份必须原样保住、名字非空、
// 上下文窗口是正数、默认输出上限是正数、推理档位非空且互不重名、默认档位在清单里。
//
// 新增: DSH 判的是 `defaultMaxTokens <= 0`，因为它用 undefined 表示「没配」。
// Go 这边 0 就是「没配」（见 [ResolvedModelInfo]），所以这条判据是 `< 0`。
func normalizeModelInfo(provider, model string, resolved ResolvedModelInfo) (ResolvedModelInfo, error) {
	if resolved.Provider != provider || resolved.ID != model || resolved.Name == "" {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid exact model metadata for provider %q model %q", provider, model),
			InvalidModelInfoCode, nil)
	}
	if resolved.Context != nil && resolved.Context.ContextWindow <= 0 {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid context metadata for provider %q model %q", provider, model),
			InvalidModelContextCode, nil)
	}
	if resolved.DefaultMaxTokens < 0 {
		return ResolvedModelInfo{}, NewError(
			fmt.Sprintf("adapter returned invalid default maxTokens for provider %q model %q", provider, model),
			InvalidModelMaxTokensCode, nil)
	}
	if resolved.Reasoning != nil {
		if len(resolved.Reasoning.Efforts) == 0 {
			return ResolvedModelInfo{}, NewError(
				fmt.Sprintf("adapter returned invalid reasoning metadata for provider %q model %q", provider, model),
				InvalidModelReasoningCode, nil)
		}
		seen := map[ReasoningEffortID]struct{}{}
		for _, effort := range resolved.Reasoning.Efforts {
			_, duplicate := seen[effort.ID]
			if effort.ID == "" || effort.Name == "" || duplicate {
				return ResolvedModelInfo{}, NewError(
					fmt.Sprintf("adapter returned invalid or duplicate reasoning effort metadata for provider %q model %q",
						provider, model),
					InvalidModelReasoningCode, nil)
			}
			seen[effort.ID] = struct{}{}
		}
		if resolved.Reasoning.DefaultEffort != "" {
			if _, known := seen[resolved.Reasoning.DefaultEffort]; !known {
				return ResolvedModelInfo{}, NewError(
					fmt.Sprintf("adapter returned an unknown default reasoning effort for provider %q model %q",
						provider, model),
					InvalidModelReasoningCode, nil)
			}
		}
	}
	// 能力元数据原样带过去：一次**显式**省掉某个模态是下游预检要据以行动的
	// 否定能力（图片准入那条路）。
	return resolved.Clone(), nil
}

// ---- 调用配置解算 ----

// ResolveCallConfig 拿一份会话调用配置去对确切模型的能力，并把适配器配好的默认
// 落实进去。不支持的显式档位在任何提供方 I/O 之前就被拒；这里不做任何夹取或者
// 别名替换。
//
// 源: packages/llm/llm/src/index.ts:756-768
//
// 这次独立问询**不绑定**之后那次派发；日志和流式必须共用同一份适配器登记时，
// 用 [Runtime.PrepareCall]。
func (r *Runtime) ResolveCallConfig(ctx context.Context, config CallConfig) (CallConfig, error) {
	registration, err := r.registration(config.Provider)
	if err != nil {
		return CallConfig{}, err
	}
	info, err := r.resolveModelInfoFor(ctx, registration, config.Model)
	if err != nil {
		return CallConfig{}, err
	}
	return resolveCallWithInfo(config, info)
}

// resolveCallWithInfo 拿请求控制项去对一份已经绑好的确切模型结果。
//
// 源: packages/llm/llm/src/index.ts:779-814
func resolveCallWithInfo(config CallConfig, info ResolvedModelInfo) (CallConfig, error) {
	resolved := config
	if resolved.MaxTokens == 0 && info.DefaultMaxTokens != 0 {
		resolved.MaxTokens = info.DefaultMaxTokens
	}
	requested := resolved.ReasoningEffort
	if info.Reasoning == nil {
		if requested != "" {
			return CallConfig{}, NewError(
				fmt.Sprintf("provider %q model %q does not support reasoning effort %q",
					config.Provider, config.Model, requested),
				UnsupportedReasoningEffortCode, nil)
		}
		return resolved, nil
	}
	effective := requested
	if effective == "" {
		effective = info.Reasoning.DefaultEffort
	}
	if effective == "" {
		return resolved, nil
	}
	supported := slices.ContainsFunc(info.Reasoning.Efforts, func(effort ReasoningEffortInfo) bool {
		return effort.ID == effective
	})
	if !supported {
		return CallConfig{}, NewError(
			fmt.Sprintf("provider %q model %q does not support reasoning effort %q",
				config.Provider, config.Model, effective),
			UnsupportedReasoningEffortCode, nil)
	}
	resolved.ReasoningEffort = effective
	return resolved, nil
}
