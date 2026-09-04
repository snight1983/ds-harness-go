// 本文件的作用：把一个 agent 那份「提供方 / 模型 / 推理档位」的选择，投影成 ACP
// 线上那两个标准配置项，并且把对面改过来的值收回去。
//
// 源: packages/acp/acp/src/model-control.ts:1-237

package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// 这两个是这条线上摆出来的配置项标识。
//
// 源: packages/acp/acp/src/model-control.ts:8-9
const (
	modelConfigID     wire.SessionConfigId = "model"
	reasoningConfigID wire.SessionConfigId = "reasoning_effort"
)

// providerDefaultReasoningValue 是「不点档位，交给提供方自己的默认」那个选项值。
//
// 源: packages/acp/acp/src/model-control.ts:10-11
//
// 本仓库的推理档位标识一律非空（见 [llm.ReasoningEffortID] 那条「空串即缺失」的
// 约定），所以线上这个空串和任何一个真档位都不撞车。
const providerDefaultReasoningValue wire.SessionConfigValueId = ""

// ModelCatalog 是本包用得到的那一块 LLM 服务：除了问模态，还要翻目录和解路由。
//
// 源: packages/acp/acp/src/model-control.ts:41（LlmRuntime）
//
// 新增: DSH 是 `ctx.get('llm')` 把整个服务注进来。这里沿用本包 [ModelResolver] 那条
// 窄口子的做法，只是这一块要的方法多三个，所以把那个更窄的接口嵌进来而不是另开
// 一个字段——一座桥只有一个 LLM 协作者。交进来的 [llm.Runtime] 自然满足它。
type ModelCatalog interface {
	ModelResolver
	// ListProviders 交出当下登记着的那些提供方路由。
	ListProviders() []llm.ProviderInfo
	// ListModels 交出一条提供方路由上那个适配器知道的模型。
	ListModels(ctx context.Context, provider string) ([]llm.ModelInfo, error)
	// ResolveCallConfig 把一份调用配置按适配器的默认值解算完整。
	ResolveCallConfig(ctx context.Context, config llm.CallConfig) (llm.CallConfig, error)
	// OnAdaptersUpdated 登记一个拓扑观察者，交回撤销这次登记的函数。
	//
	// 源: packages/acp/acp/src/index.ts:148-150（ctx.on('llm/adapters-updated')）
	OnAdaptersUpdated(
		ctx context.Context,
		owner *scope.Scope,
		observer llm.AdaptersUpdatedObserver,
	) (func(context.Context) error, error)
}

// ModelConfigError 是一次「对面给错了、改一下就能对」的配置失败。
//
// 源: packages/acp/acp/src/model-control.ts:23-29（AcpModelConfigError）
//
// Message 面向协议对面，原样保留英文，理由和 [ContentError] 上那条一样：它会被塞进
// 线上的错误消息里送出去，而对面是一个程序。
type ModelConfigError struct {
	// Message 是那句能安全送出去的说明。
	Message string
}

// Error 实现 error。
func (e *ModelConfigError) Error() string { return e.Message }

// modelConfigErrorf 造一个配置失败。
func modelConfigErrorf(format string, args ...any) *ModelConfigError {
	return &ModelConfigError{Message: fmt.Sprintf(format, args...)}
}

// modelValue 把一条完整路由压成线上那个不透明的选项值。
//
// 源: packages/acp/acp/src/model-control.ts:234-237
//
// 新增: DSH 用 JSON.stringify。这里关掉 Go 默认的 HTML 转义，理由和 [jsonQuote]
// 上那条逐字相同：提供方或者模型标识里出现 `&` 时，转义会让两侧记下来的不是同一个值。
func modelValue(provider, model string) wire.SessionConfigValueId {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	// 编码一个字符串切片不会失败。
	_ = encoder.Encode([]string{provider, model})
	return wire.SessionConfigValueId(strings.TrimSuffix(buffer.String(), "\n"))
}

// configState 是一次算出来的那份完整配置项状态，外加反查用的那张选项表。
//
// 源: packages/acp/acp/src/model-control.ts:18-21（ConfigState）
type configState struct {
	choices map[wire.SessionConfigValueId]agent.ModelSelection
	options []wire.SessionConfigOption
}

// ModelControl 把一个 agent 的模型选择投影成 ACP 配置项，也把对面改过来的值按回去。
//
// 源: packages/acp/acp/src/model-control.ts:31-232（AcpModelControl）
//
// 它有两份选择：selected 是这个会话**配置成**什么，turnSelection 是某一个已经准入的
// 回合被**钉死**在什么上。钉住的那一份只管路由，摆给对面看的和下一轮提示词抓的都是
// selected——于是回合中途改模型不会把正在跑的那一步换掉。
type ModelControl struct {
	catalog   ModelCatalog
	selection *agent.ModelSelectionRef

	// queue 串行化 [ModelControl.Options] 和 [ModelControl.Set]。
	//
	// 新增: DSH 是一条 promise 尾巴，注释写着「按收到的次序」。Go 这边收不回那个
	// 次序——[github.com/coder/acp-go-sdk] 的读循环对每一个进来的请求各起一个
	// goroutine（connection.go:412），谁先摸到锁在那一刻就已经不定了。所以这里保住的
	// 是这个状态机真正需要的那一半：互斥。DSH 那条尾巴要靠
	// `.then(() => undefined, () => undefined)` 收住一次拒绝，否则整条尾巴就废了；
	// Go 这边一个交回错误的调用不会毒化互斥锁，那一半是白得的。
	queue sync.Mutex

	// state 护住下面那几个字段。它和 queue 是两把锁：[ModelControl.PinTurn]、
	// [ModelControl.ReleaseTurn] 和 [ModelControl.Snapshot] 走在提示词那条路上，
	// 不能被一次正在翻目录的 Set 卡住——DSH 那三个方法同样不进那条尾巴。
	state            sync.Mutex
	selected         agent.ModelSelection
	hasSelected      bool
	turn             int
	turnSelection    agent.ModelSelection
	hasTurn          bool
	hasResolvedState bool
}

// NewModelControl 造一份模型控制。initial 为零值加 false 表示这个会话没有模型选择，
// 那时它一个配置项都不摆。
//
// 源: packages/acp/acp/src/model-control.ts:40-52
func NewModelControl(catalog ModelCatalog, initial agent.ModelSelection, hasInitial bool) *ModelControl {
	control := &ModelControl{
		catalog:     catalog,
		selection:   agent.NewModelSelectionRef(),
		selected:    initial,
		hasSelected: hasInitial,
	}
	control.publish()
	return control
}

// publish 把「当下有效的那一份」推进引用里：钉住的优先，没钉住就是 selected。
//
// 新增: DSH 那个 ref 是一个带 getter 的字面量对象，每次读都现算
// `turnSelection?.selection ?? selected`。Go 的 [agent.ModelSelectionRef] 是一个具体
// 类型，接不上自定义 getter，所以这里改成每次变更都显式推一次。两者可观察到的
// 行为一样，因为这里是唯一改这两个字段的地方。
//
// 调用方必须已经持有 state。
func (c *ModelControl) publish() {
	switch {
	case c.hasTurn:
		c.selection.Select(c.turnSelection)
	case c.hasSelected:
		c.selection.Select(c.selected)
	default:
		c.selection.Clear()
	}
}

// Install 把这份选择接到 owner 这个作用域的提示词装配和请求路由上。
//
// 源: packages/acp/acp/src/model-control.ts:54-60
//
// owner 应当是那个**还没公布**的 agent 作用域，也就是 [agent.Setup] 收到的那一个。
func (c *ModelControl) Install(
	ctx context.Context,
	owner *scope.Scope,
	agents *agent.Registry,
	prompts *systemprompt.Registry,
) (func(context.Context) error, error) {
	return agent.InstallModelSelection(ctx, owner, agents, prompts, c.selection)
}

// Snapshot 抓下要挂到下一条准入的提示词上的那一份选择。
//
// 源: packages/acp/acp/src/model-control.ts:62-68
//
// 抓的是 selected 而不是钉住的那一份：钉住的是上一个回合的事。
func (c *ModelControl) Snapshot() (agent.ModelSelection, bool) {
	c.state.Lock()
	defer c.state.Unlock()
	return c.selected, c.hasSelected
}

// PinTurn 把一条已准入消息的选择钉在它那个回合的每一步上。
//
// 源: packages/acp/acp/src/model-control.ts:70-77
func (c *ModelControl) PinTurn(turn int, selection agent.ModelSelection) {
	c.state.Lock()
	defer c.state.Unlock()
	c.turn, c.turnSelection, c.hasTurn = turn, selection, true
	c.publish()
}

// ReleaseTurn 只放掉**确切是**这个回合的那次钉住。
//
// 源: packages/acp/acp/src/model-control.ts:79-85
func (c *ModelControl) ReleaseTurn(turn int) {
	c.state.Lock()
	defer c.state.Unlock()
	if !c.hasTurn || c.turn != turn {
		return
	}
	c.turnSelection, c.hasTurn = agent.ModelSelection{}, false
	c.publish()
}

// Options 交出当下那份完整的配置项状态，排在之前那些改动后面。
//
// 源: packages/acp/acp/src/model-control.ts:87-94
func (c *ModelControl) Options(ctx context.Context) ([]wire.SessionConfigOption, error) {
	c.queue.Lock()
	defer c.queue.Unlock()
	state, err := c.buildState(ctx)
	if err != nil {
		return nil, err
	}
	return state.options, nil
}

// Set 改一个摆出来的配置项，交回改完之后那份完整状态。
//
// 源: packages/acp/acp/src/model-control.ts:96-134
func (c *ModelControl) Set(
	ctx context.Context,
	configID wire.SessionConfigId,
	value wire.SessionConfigValueId,
) ([]wire.SessionConfigOption, error) {
	c.queue.Lock()
	defer c.queue.Unlock()

	current, hasCurrent := c.Snapshot()
	if !hasCurrent {
		return nil, modelConfigErrorf("this session has no model selection")
	}

	switch configID {
	case modelConfigID:
		state, err := c.buildState(ctx)
		if err != nil {
			return nil, err
		}
		chosen, ok := state.choices[value]
		if !ok {
			return nil, modelConfigErrorf("unknown model option: %s", value)
		}
		// 这一次解算只用来验路由通不通，结果不要：DSH 存的是**没解算过**的那一份
		// （model-control.ts:112-113 先 await，再 `this.selected = selected`）。原样
		// 照搬——换模型时推理档位跟着清掉，下一次 buildState 再按新模型的默认值补回去。
		if _, err := c.resolveSelection(ctx, chosen); err != nil {
			return nil, err
		}
		c.commit(chosen)
	case reasoningConfigID:
		info, err := c.catalog.ResolveModelInfo(ctx, current.Provider, current.Model)
		if err != nil {
			return nil, err
		}
		providerDefault := value == providerDefaultReasoningValue &&
			info.Reasoning != nil && info.Reasoning.DefaultEffort == ""
		if info.Reasoning == nil || (!providerDefault && !hasEffort(info.Reasoning.Efforts, value)) {
			return nil, modelConfigErrorf("unknown reasoning effort for %s/%s: %s",
				current.Provider, current.Model, value)
		}
		next := agent.ModelSelection{Provider: current.Provider, Model: current.Model}
		if !providerDefault {
			next.ReasoningEffort = llm.ReasoningEffortID(value)
		}
		resolved, err := c.resolveSelection(ctx, next)
		if err != nil {
			return nil, err
		}
		c.commit(resolved)
	default:
		return nil, modelConfigErrorf("unknown session config option: %s", configID)
	}

	state, err := c.buildState(ctx)
	if err != nil {
		return nil, err
	}
	return state.options, nil
}

// commit 换掉这个会话配置成的那一份。
func (c *ModelControl) commit(selection agent.ModelSelection) {
	c.state.Lock()
	defer c.state.Unlock()
	c.selected, c.hasSelected = selection, true
	c.publish()
}

// hasEffort 判一个线上值是不是这条路由摆出来的某一档。
func hasEffort(efforts []llm.ReasoningEffortInfo, value wire.SessionConfigValueId) bool {
	for _, effort := range efforts {
		if wire.SessionConfigValueId(effort.ID) == value {
			return true
		}
	}
	return false
}

// resolveSelection 验一条精确路由，只留下 agent 自己拥有的那几个字段。
//
// 源: packages/acp/acp/src/model-control.ts:223-231
func (c *ModelControl) resolveSelection(
	ctx context.Context,
	selection agent.ModelSelection,
) (agent.ModelSelection, error) {
	resolved, err := c.catalog.ResolveCallConfig(ctx, llm.CallConfig{
		Provider:        selection.Provider,
		Model:           selection.Model,
		ReasoningEffort: selection.ReasoningEffort,
	})
	if err != nil {
		return agent.ModelSelection{}, err
	}
	return agent.ModelSelection{
		Provider:        resolved.Provider,
		Model:           resolved.Model,
		ReasoningEffort: resolved.ReasoningEffort,
	}, nil
}

// buildState 算出那些脱钩的模型选项，以及跟着当下路由走的那个推理档位选项。
//
// 源: packages/acp/acp/src/model-control.ts:143-221
func (c *ModelControl) buildState(ctx context.Context) (configState, error) {
	selected, hasSelected := c.Snapshot()
	if !hasSelected {
		return configState{choices: map[wire.SessionConfigValueId]agent.ModelSelection{}}, nil
	}

	// 一条曾经解算成功过的路由后来解不动了（适配器被摘掉、配置被改坏），这里退回
	// 那份没解算的选择接着把选项摆出来——不然对面连"换回一个能用的模型"这条路都
	// 没有了。第一次就解不动则照实报错。
	resolved, err := c.resolveSelection(ctx, selected)
	routeAvailable := true
	if err != nil {
		c.state.Lock()
		everResolved := c.hasResolvedState
		c.state.Unlock()
		if !everResolved {
			return configState{}, err
		}
		resolved, routeAvailable = selected, false
	} else {
		c.state.Lock()
		c.hasResolvedState = true
		c.state.Unlock()
	}

	choices := map[wire.SessionConfigValueId]agent.ModelSelection{}
	var groups []wire.SessionConfigSelectGroup
	for _, provider := range c.catalog.ListProviders() {
		group := wire.SessionConfigSelectGroup{
			Group: wire.SessionConfigGroupId(provider.ID),
			Name:  provider.Name,
		}
		// 一条提供方翻不出目录不该让整份状态发不出去：别的提供方还是选得动的。
		models, listErr := c.catalog.ListModels(ctx, provider.ID)
		if listErr == nil {
			for _, model := range models {
				value := modelValue(provider.ID, model.ID)
				choices[value] = agent.ModelSelection{Provider: provider.ID, Model: model.ID}
				option := wire.SessionConfigSelectOption{Name: model.Name, Value: value}
				if model.Description != "" {
					description := model.Description
					option.Description = &description
				}
				group.Options = append(group.Options, option)
			}
		}
		groups = append(groups, group)
	}

	currentValue := modelValue(resolved.Provider, resolved.Model)
	if _, ok := choices[currentValue]; !ok {
		// 当下这条路由不在任何一份目录里（模型是手配的，或者提供方下架了它）。
		// 它必须摆得出来，否则那个选择器会显示一个它自己都不认的当前值。
		choices[currentValue] = agent.ModelSelection{Provider: resolved.Provider, Model: resolved.Model}
		index := -1
		for position := range groups {
			if string(groups[position].Group) == resolved.Provider {
				index = position
				break
			}
		}
		if index < 0 {
			groups = append(groups, wire.SessionConfigSelectGroup{
				Group: wire.SessionConfigGroupId(resolved.Provider),
				Name:  resolved.Provider,
			})
			index = len(groups) - 1
		}
		groups[index].Options = append(
			[]wire.SessionConfigSelectOption{{Name: resolved.Model, Value: currentValue}},
			groups[index].Options...,
		)
	}

	populated := make(wire.SessionConfigSelectOptionsGrouped, 0, len(groups))
	for _, group := range groups {
		if len(group.Options) > 0 {
			populated = append(populated, group)
		}
	}
	modelCategory := wire.SessionConfigOptionCategoryModel
	options := []wire.SessionConfigOption{{Select: &wire.SessionConfigOptionSelect{
		Id:           modelConfigID,
		Name:         "Model",
		Category:     &modelCategory,
		Type:         "select",
		CurrentValue: currentValue,
		Options:      wire.SessionConfigSelectOptions{Grouped: &populated},
	}}}

	if !routeAvailable {
		return configState{choices: choices, options: options}, nil
	}
	info, err := c.catalog.ResolveModelInfo(ctx, resolved.Provider, resolved.Model)
	if err != nil {
		return configState{}, err
	}
	if info.Reasoning == nil {
		return configState{choices: choices, options: options}, nil
	}

	var effortOptions []wire.SessionConfigSelectOption
	if info.Reasoning.DefaultEffort == "" {
		// 适配器没配默认档位时，「交给提供方」才是一个真的、选得出来的第三态。
		effortOptions = append(effortOptions, wire.SessionConfigSelectOption{
			Name:  "Provider default",
			Value: providerDefaultReasoningValue,
		})
	}
	for _, effort := range info.Reasoning.Efforts {
		option := wire.SessionConfigSelectOption{
			Name:  effort.Name,
			Value: wire.SessionConfigValueId(effort.ID),
		}
		if effort.Description != "" {
			description := effort.Description
			option.Description = &description
		}
		effortOptions = append(effortOptions, option)
	}
	ungrouped := wire.SessionConfigSelectOptionsUngrouped(effortOptions)
	thoughtCategory := wire.SessionConfigOptionCategoryThoughtLevel
	options = append(options, wire.SessionConfigOption{Select: &wire.SessionConfigOptionSelect{
		Id:           reasoningConfigID,
		Name:         "Reasoning effort",
		Category:     &thoughtCategory,
		Type:         "select",
		CurrentValue: wire.SessionConfigValueId(resolved.ReasoningEffort),
		Options:      wire.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	}})
	return configState{choices: choices, options: options}, nil
}
