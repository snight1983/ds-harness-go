// 本文件的作用：孩子跑在哪条 LLM 路由上——那张授权表本身、模型说出来的那三个字段
// 怎么并进装配写死的那份、并完之后怎么对着授权表验，以及在真起孩子之前拿活着的
// 适配器把这条路由解一遍。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts

package subagenttool

import (
	"context"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
)

// AllowedRoute 是用户设置授权的、一条确切的孩子 LLM 路由。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:9-14（AllowedModelRoute）
type AllowedRoute struct {
	// Provider 是登记过的 LLM 提供方 id。
	Provider string `json:"provider"`
	// Model 是那个提供方自己拥有的确切模型 id。
	Model string `json:"model"`
}

// ModelSelectionPolicy 是一次派发定义手上那份路由选择权。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:23-26
//
// 一个 nil 的指针表示这次会话**没有**这张表——也就是路由写死，模型说什么都不算数；
// 一份非 nil、Routes 为空的表在这里造不出来，[AssertAllowedRoutes] 不收空表。
type ModelSelectionPolicy struct {
	// Routes 是准许显式挑选的那些确切提供方／模型对。
	Routes []AllowedRoute
}

// routeKey 是一条提供方／模型对的稳定身份，只拿来判相等。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:33-35（modelRouteKey）
func routeKey(route AllowedRoute) string {
	return route.Provider + "\x00" + route.Model
}

// AssertAllowedRoutes 在耐久边界和配置边界上拒掉畸形的、或者重复的授权条目。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:42-62（assertAllowedModelRoutes）
//
// 新增: DSH 那道 `Array.isArray` 检查在这里没有对应物——Go 侧这两个边界都要先把
// JSON 解进 []AllowedRoute，解不成本身就是那句「不是一个数组」。留在这里的是解完
// 之后仍然看不出来的两件事：id 空着，以及同一条路由写了两遍。
func AssertAllowedRoutes(routes []AllowedRoute) error {
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.Provider == "" || route.Model == "" {
			return errors.New("subagent model selection requires non-empty provider and model ids")
		}
		key := routeKey(route)
		if _, repeated := seen[key]; repeated {
			return fmt.Errorf("subagent model selection repeats route %q", route.Provider+"/"+route.Model)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// modelRequest 是模型自己在一次工具调用里说的那三个路由字段。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:65-69（DelegationModelRequest）
//
// 新增: 三个字段都是指针，因为「没写」和「写了个空串」在这里是两件不同的事：前者
// 表示这一项随继承，后者是模型给错了东西，要被 [assertNonEmpty] 当场拒掉。
type modelRequest struct {
	Provider        *string `json:"provider,omitempty"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
}

// present 说这次调用有没有显式挑任何一个孩子 LLM 值。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:76-80（hasDelegationModelRequest）
func (r modelRequest) present() bool {
	return r.Provider != nil || r.Model != nil || r.ReasoningEffort != nil
}

// assertNonEmpty 在工具 JSON 这道边界上拒掉一个空着的路由值。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:83-87
func assertNonEmpty(value *string, field string) error {
	if value != nil && *value == "" {
		return fmt.Errorf("child LLM `%s` must be non-empty", field)
	}
	return nil
}

// requestedAgentOptions 把模型说的那几个字段并到装配写死的那份孩子默认值上。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:99-128
//
// 提供方和模型合起来才是一条路由，必须一起给。换掉那条路由又没点名档位时，写死的
// 那份里跟着路由走的档位被清掉——理由和
// [github.com/snight1983/ds-harness-go/feature/subagent.ResolveChildAgentOptions] 那处相同。
func requestedAgentOptions(
	parentOptions agent.Options,
	configured agent.Options,
	request modelRequest,
	enabled bool,
) (agent.Options, error) {
	if !request.present() {
		return configured, nil
	}
	if !enabled {
		return agent.Options{}, errors.New("child model selection is disabled for this tool instance")
	}
	for _, each := range []struct {
		value *string
		field string
	}{
		{request.Provider, "provider"},
		{request.Model, "model"},
		{request.ReasoningEffort, "reasoning_effort"},
	} {
		if err := assertNonEmpty(each.value, each.field); err != nil {
			return agent.Options{}, err
		}
	}
	if (request.Provider == nil) != (request.Model == nil) {
		return agent.Options{}, errors.New("child LLM `provider` and `model` must be supplied together")
	}

	baselineProvider := configured.Provider
	if baselineProvider == "" {
		baselineProvider = parentOptions.Provider
	}
	baselineModel := configured.Model
	if baselineModel == "" {
		baselineModel = parentOptions.Model
	}
	resolved := configured
	routeChanged := request.Provider != nil &&
		(*request.Provider != baselineProvider || *request.Model != baselineModel)
	if routeChanged && request.ReasoningEffort == nil {
		resolved.ReasoningEffort = ""
	}
	if request.Provider != nil {
		resolved.Provider, resolved.Model = *request.Provider, *request.Model
	}
	if request.ReasoningEffort != nil {
		resolved.ReasoningEffort = llm.ReasoningEffortID(*request.ReasoningEffort)
	}
	return resolved, nil
}

// assertAllowedModelSelection 在真起孩子的那一步守住设置拥有的那张路由表。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:139-153
//
// 纯继承不受这张表管——那时候模型一个字都没说；只要显式写了任何一个路由或档位
// 字段，并出来的那条路由就必须落在表里。
func assertAllowedModelSelection(
	policy *ModelSelectionPolicy,
	parentOptions agent.Options,
	requested agent.Options,
	request modelRequest,
) error {
	if policy == nil || !request.present() {
		return nil
	}
	provider, model := effectiveRoute(parentOptions, requested)
	if provider == "" || model == "" {
		return errors.New("cannot select child LLM values without an effective provider and model")
	}
	for _, route := range policy.Routes {
		if route.Provider == provider && route.Model == model {
			return nil
		}
	}
	return fmt.Errorf("child LLM route %q is not allowed for this Session", provider+"/"+model)
}

// hasConfiguredLLMSelection 说装配写死的那份孩子选项要不要在派发之前先验一遍路由。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:160-164（hasConfiguredLlmSelection）
func hasConfiguredLLMSelection(options agent.Options) bool {
	return options.Provider != "" || options.Model != "" || options.ReasoningEffort != ""
}

// effectiveRoute 是并完之后孩子实际要走的那条路由：请求那份说了就用它，没说的
// 落回父那份。
//
// 新增: DSH 那两个 `?? parentOptions.x` 在 model-selection.ts 里写了两遍（授权检查
// 一遍、预检一遍），两处必须一模一样，所以在这里收成一个函数。
func effectiveRoute(parentOptions agent.Options, requested agent.Options) (string, string) {
	provider, model := requested.Provider, requested.Model
	if provider == "" {
		provider = parentOptions.Provider
	}
	if model == "" {
		model = parentOptions.Model
	}
	return provider, model
}

// preflightChildRoute 在孩子被造出来之前，拿活着的适配器把这条路由解一遍。
//
// 源: packages/subagent/tool-subagent/src/model-selection.ts:176-196（preflightChildLlmRoute）
//
// 提供方查找、确切模型的元数据、档位是否存在、以及适配器那几个默认值，全归
// [github.com/snight1983/ds-harness-go/llm.Runtime] 管——这一步只是**提前**把它要说的话问出来，
// 好让一条走不通的路由在这次工具调用上失败，而不是在一个已经落了盘的孩子身上失败。
//
// 新增: DSH 拿到的是 resolveCallConfig 的结果然后丢掉。Go 这边
// [github.com/snight1983/ds-harness-go/llm.Runtime.PrepareCall] 交回的是一个**一次性**句柄，
// 这里同样只要它的成败，不要它的值。
func preflightChildRoute(
	ctx context.Context,
	runtime *llm.Runtime,
	parentOptions agent.Options,
	requested agent.Options,
	inheritParentReasoningEffort bool,
) error {
	provider, model := effectiveRoute(parentOptions, requested)
	if provider == "" || model == "" {
		return errors.New("cannot select child LLM values without an effective provider and model")
	}
	routeChanged := provider != parentOptions.Provider || model != parentOptions.Model
	effort := requested.ReasoningEffort
	if effort == "" && inheritParentReasoningEffort && !routeChanged {
		effort = parentOptions.ReasoningEffort
	}
	_, err := runtime.PrepareCall(ctx, llm.CallConfig{
		Provider:        provider,
		Model:           model,
		ReasoningEffort: effort,
	})
	return err
}
