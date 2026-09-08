// 本文件的作用：那件只读的发现工具——模型自己去问「孩子能跑在哪几条 LLM 路由上」，
// 一层一层从提供方问到确切模型再问到推理档位，问到的每一样都被那张授权表过一遍。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts

package subagenttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// ListModelsToolName 是那件发现工具的名字。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:88
//
// 它是**写死的**：派发工具那个名字可以由装配改（[Config.ToolName]），这一个不行，
// 因为那几段面向模型的说明里逐字提到它。
const ListModelsToolName = "list_subagent_models"

// listModelsArgs 是这件工具的参数：两个都可选，一层一层往下问。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:9-12
//
// 新增: 两个字段都是指针，理由与 [modelRequest] 逐字相同——「没写」是往上退一层，
// 「写了个空串」是模型给错了东西，要被当场拒掉。
type listModelsArgs struct {
	Provider *string `json:"provider,omitempty"`
	Model    *string `json:"model,omitempty"`
}

// 这几句是给模型看的，所以是英文。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:89-104
const (
	listModelsDescription = "Discover LLM routes for subagents without changing the current Agent. Call with " +
		"no arguments to list registered providers, with `provider` to list its advertised models, or with " +
		"`provider` and `model` to inspect that exact model and its reasoning efforts. Catalog membership is " +
		"advisory: an adapter may accept an unlisted model id. Use the returned ids with a delegation tool's " +
		"`provider`, `model`, and `reasoning_effort` fields."
	listModelsProviderDescription = "Registered LLM provider id. Omit to list providers."
	listModelsModelDescription    = "Exact model id to inspect. Requires provider; omit to list that " +
		"provider's advertised models."
)

// 这四句是那三层各自的空答案，以及一句缺服务的诊断。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:44, 53, 66, 77
const (
	noLLMServiceMessage   = "cannot discover child LLM routes because the `llm` service is unavailable"
	noProvidersMessage    = "(no LLM providers)"
	noEffortsMessage      = "(no advertised reasoning efforts)"
	noAvailableProviders  = "(none)"
	effortsSectionHeading = "Reasoning efforts:"
)

// allowedProviderIDs 是那张授权表点到过的提供方里，此刻真的登记着的那些。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:23-26, 50-51
//
// 这一层求交在两处用得着：列提供方那一层的答案，以及找不到提供方时那句诊断里
// 「你还可以选这些」——两处都绝不许把一个没被授权的提供方名说出去。
func allowedProviderIDs(runtime *llm.Runtime, policy *ModelSelectionPolicy) []llm.ProviderInfo {
	var allowed []llm.ProviderInfo
	for _, candidate := range runtime.ListProviders() {
		for _, route := range policy.Routes {
			if route.Provider == candidate.ID {
				allowed = append(allowed, candidate)
				break
			}
		}
	}
	return allowed
}

// registeredProvider 把一个提供方 id 解成它那条登记着的路由，解不出来时那句诊断
// 带上模型改得动的东西。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:15-28
func registeredProvider(
	runtime *llm.Runtime,
	policy *ModelSelectionPolicy,
	providerID string,
) (llm.ProviderInfo, error) {
	for _, candidate := range runtime.ListProviders() {
		if candidate.ID == providerID {
			return candidate, nil
		}
	}
	names := make([]string, 0, len(policy.Routes))
	for _, each := range allowedProviderIDs(runtime, policy) {
		names = append(names, each.ID)
	}
	available := noAvailableProviders
	if len(names) > 0 {
		available = strings.Join(names, ", ")
	}
	return llm.ProviderInfo{}, fmt.Errorf(
		"LLM provider %q is not registered; available providers: %s", providerID, available)
}

// modelLine 排一行模型。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:31-33
func modelLine(provider string, model llm.ModelInfo) string {
	line := provider + "/" + model.ID + " — " + model.Name
	if model.Description != "" {
		line += ": " + model.Description
	}
	return line
}

// effortLines 排那些推理档位；一档都没有时是那句固定的空答案。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:74-77
func effortLines(model llm.ResolvedModelInfo) string {
	if model.Reasoning == nil || len(model.Reasoning.Efforts) == 0 {
		return noEffortsMessage
	}
	lines := make([]string, 0, len(model.Reasoning.Efforts))
	for _, effort := range model.Reasoning.Efforts {
		line := string(effort.ID)
		if model.Reasoning.DefaultEffort == effort.ID {
			line += " (default)"
		}
		line += " — " + effort.Name
		if effort.Description != "" {
			line += ": " + effort.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// listProviders 是最上面那一层：这条会话被授权的那些提供方。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:49-55
func listProviders(runtime *llm.Runtime, policy *ModelSelectionPolicy) string {
	allowed := allowedProviderIDs(runtime, policy)
	if len(allowed) == 0 {
		return noProvidersMessage
	}
	lines := make([]string, 0, len(allowed))
	for _, each := range allowed {
		lines = append(lines, each.ID+" — "+each.Name)
	}
	return strings.Join(lines, "\n")
}

// listModels 是中间那一层：一个提供方下面被授权的那些确切模型。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:62-68
func listModels(
	ctx context.Context,
	runtime *llm.Runtime,
	provider llm.ProviderInfo,
	allowedRoutes []AllowedRoute,
) (string, error) {
	advertised, err := runtime.ListModels(ctx, provider.ID)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, model := range advertised {
		for _, route := range allowedRoutes {
			if route.Model == model.ID {
				lines = append(lines, modelLine(provider.ID, model))
				break
			}
		}
	}
	if len(lines) == 0 {
		return "(no advertised models for " + provider.ID + ")", nil
	}
	return strings.Join(lines, "\n"), nil
}

// inspectModel 是最里面那一层：一条确切路由的元数据加它的那些推理档位。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:69-78
func inspectModel(
	ctx context.Context,
	runtime *llm.Runtime,
	provider llm.ProviderInfo,
	allowedRoutes []AllowedRoute,
	modelID string,
) (string, error) {
	if modelID == "" {
		return "", errors.New("`model` must be non-empty")
	}
	permitted := false
	for _, route := range allowedRoutes {
		if route.Model == modelID {
			permitted = true
			break
		}
	}
	if !permitted {
		return "", fmt.Errorf(
			"child LLM route %q is not allowed for this Session", provider.ID+"/"+modelID)
	}
	resolved, err := runtime.ResolveModelInfo(ctx, provider.ID, modelID)
	if err != nil {
		return "", err
	}
	return modelLine(provider.ID, resolved.ModelInfo) + "\n" +
		effortsSectionHeading + "\n" + effortLines(resolved), nil
}

// discoverRoutes 是这件工具的体：按写了几个参数一层一层往下走。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:36-79
func discoverRoutes(
	ctx context.Context,
	runtime *llm.Runtime,
	policy *ModelSelectionPolicy,
	args listModelsArgs,
) (string, error) {
	if runtime == nil {
		return "", errors.New(noLLMServiceMessage)
	}
	if args.Model != nil && args.Provider == nil {
		return "", errors.New("`model` requires `provider`")
	}
	if args.Provider == nil {
		return listProviders(runtime, policy), nil
	}
	if *args.Provider == "" {
		return "", errors.New("`provider` must be non-empty")
	}
	var allowedRoutes []AllowedRoute
	for _, route := range policy.Routes {
		if route.Provider == *args.Provider {
			allowedRoutes = append(allowedRoutes, route)
		}
	}
	if len(allowedRoutes) == 0 {
		return "", fmt.Errorf(
			"LLM provider %q is not allowed for this Session", *args.Provider)
	}
	provider, err := registeredProvider(runtime, policy, *args.Provider)
	if err != nil {
		return "", err
	}
	if args.Model == nil {
		return listModels(ctx, runtime, provider, allowedRoutes)
	}
	return inspectModel(ctx, runtime, provider, allowedRoutes, *args.Model)
}

// newListModelsTool 造那件发现工具，它攥着这条会话那张授权表。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:86-113
func newListModelsTool(runtime *llm.Runtime, policy *ModelSelectionPolicy) *tools.Definition {
	return &tools.Definition{
		Name:        ListModelsToolName,
		Description: listModelsDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "provider", Schema: tools.Node{
					Type: tools.TypeString, Description: listModelsProviderDescription}},
				{Name: "model", Schema: tools.Node{
					Type: tools.TypeString, Description: listModelsModelDescription}},
			},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var text string
				if err := json.Unmarshal(value, &text); err != nil {
					return nil, err
				}
				return llm.Content{llm.TextBlock{Text: text}}, nil
			},
		},
		Execute: func(
			ctx context.Context,
			rawArgs json.RawMessage,
			_ *tools.RunContext,
		) (json.RawMessage, error) {
			var args listModelsArgs
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				return nil, err
			}
			text, err := discoverRoutes(ctx, runtime, policy, args)
			if err != nil {
				return nil, err
			}
			return json.Marshal(text)
		},
	}
}

// registerListModels 把那件发现工具装上一个作用域，交回摘它的函数。
//
// 源: packages/subagent/tool-subagent/src/list-models.ts:86-113
func registerListModels(
	ctx context.Context,
	runtime *tools.Runtime,
	owner *scope.Scope,
	llmRuntime *llm.Runtime,
	policy *ModelSelectionPolicy,
) (func(context.Context) error, error) {
	return runtime.Register(ctx, owner, newListModelsTool(llmRuntime, policy))
}
