// 本文件的作用：宿主那份「要不要向模型开放孩子的模型选择、开放哪几条路由」的用户
// 设置——它的命名空间、存下来的形状、那道校验，以及读它的那台服务。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts

package subagenttool

import (
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/settings"
)

// ModelSelectionSettingsNamespace 是这份用户设置那个小节的命名空间。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:20
var ModelSelectionSettingsNamespace = mustNamespace("subagent-model-selection")

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: 理由与 [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel] 那处逐字相同——
// 入参是包级字面量，它不合法说明本包写错了，不是运行期可以恢复的情况。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("subagenttool: 命名空间字面量不合法：%v", err))
	}
	return namespace
}

// ModelSelectionSettings 是存下来的那份用户偏好；发出去的那套组装把它默认关着。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:23-28
type ModelSelectionSettings struct {
	// Enabled 表示新组装出来的顶层会话要不要拿到模型选择。
	Enabled bool `json:"enabled"`
	// AllowedModels 是开放给新组装出来的顶层会话的那些确切孩子 LLM 路由。
	AllowedModels []AllowedRoute `json:"allowedModels"`
}

// validateModelSelectionSettings 拒掉一份开着、却一条路由都没给的偏好。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:92-97
//
// 开着而表是空的，落到派发那一步就是「每一次显式挑选都被拒」——那和关着的区别只在
// 报错的时机和说法上，而模型那边还会多出三个永远用不了的参数。
func validateModelSelectionSettings(value ModelSelectionSettings) error {
	if err := AssertAllowedRoutes(value.AllowedModels); err != nil {
		return err
	}
	if value.Enabled && len(value.AllowedModels) == 0 {
		return errors.New("enabled subagent model selection requires at least one allowed model")
	}
	return nil
}

// ModelSelectionConfig 是这台服务的装配入口。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:37-42（Config）
type ModelSelectionConfig struct {
	// Enabled 是用户文档没盖掉时继承的那个初始开关。
	Enabled bool
	// AllowedModels 是用户文档没盖掉时继承的那张初始路由表。
	AllowedModels []AllowedRoute

	// Settings 是可选的设置服务。
	//
	// 新增: 理由与 [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.Config.Settings]
	// 逐字相同——DSH 那边是 `ctx.inject(['settings'], ...)`，Go 里「挂没挂」就是这个
	// 字段是不是 nil。
	Settings *settings.Provider
}

// ModelSelectionService 拥有那份偏好，由派发工具在装到一个作用域上时读一次。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:45-98
//
// 新增: DSH 那个类叫 SubagentModelSelectionConfig，在 Go 里连上包名会和本包的
// [ModelSelectionConfig]（装配入口）撞名，所以按 Go 的习惯改叫 Service 那一档。
type ModelSelectionService struct {
	// entry 是装配给的那一份，永远不变。没有设置服务、或者设置服务撤走之后，
	// 它就是答案。
	entry ModelSelectionSettings

	// mutex 只护 scope 一个字段，理由同
	// [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.Service]。
	mutex sync.RWMutex
	scope *settings.Scope[ModelSelectionSettings]
}

// NewModelSelectionService 造一台拥有那份偏好的服务，返回它和撤销函数。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:53-78
//
// DSH 在那个 installSection 上把 onChange 留成空函数，并写明理由：消费方是在一个
// agent 发布的时候采样的，所以一次设置改动**绝不**重建一个已经跑起来的 agent 手上
// 那几件工具的定义。Go 这边采样点是 [Controller.Install]，那条约定原样成立——所以
// 这里没有任何变更回调，一个字都不用写。
func NewModelSelectionService(config ModelSelectionConfig) (*ModelSelectionService, func(), error) {
	entry := ModelSelectionSettings{
		Enabled:       config.Enabled,
		AllowedModels: append([]AllowedRoute(nil), config.AllowedModels...),
	}
	if err := validateModelSelectionSettings(entry); err != nil {
		return nil, nil, err
	}
	service := &ModelSelectionService{entry: entry}
	if config.Settings == nil {
		return service, func() {}, nil
	}

	base := map[string]any{"enabled": entry.Enabled}
	if len(entry.AllowedModels) > 0 {
		routes := make([]any, 0, len(entry.AllowedModels))
		for _, route := range entry.AllowedModels {
			routes = append(routes, map[string]any{"provider": route.Provider, "model": route.Model})
		}
		base["allowedModels"] = routes
	}
	scope, undo, err := settings.Register(config.Settings, ModelSelectionSettingsNamespace,
		ModelSelectionSettings{}, &settings.Options[ModelSelectionSettings]{
			Base:     base,
			Applies:  settings.AppliesLive,
			Validate: validateModelSelectionSettings,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("subagenttool: 登记模型选择设置小节失败：%w", err)
	}
	service.scope = scope

	var once sync.Once
	return service, func() {
		once.Do(func() {
			service.mutex.Lock()
			service.scope = nil
			service.mutex.Unlock()
			undo()
		})
	}, nil
}

// Current 读出下一次装配要用的那份脱离的偏好。
//
// 源: packages/subagent/tool-subagent/src/model-selection-settings.ts:84-90
func (s *ModelSelectionService) Current() ModelSelectionSettings {
	stored := s.entry
	s.mutex.RLock()
	scope := s.scope
	s.mutex.RUnlock()
	if scope != nil {
		stored = scope.Get()
	}
	return ModelSelectionSettings{
		Enabled:       stored.Enabled,
		AllowedModels: append([]AllowedRoute(nil), stored.AllowedModels...),
	}
}
