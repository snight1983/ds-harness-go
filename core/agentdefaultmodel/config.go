// 本文件的作用：拥有「一个没有自带模型选择的 agent 该用哪个模型」这一份默认答案，
// 并让它在挂了设置服务时可以被用户改、在没挂时退回装配那一份。
//
// 源: packages/core/agent-default-model/src/index.ts:1-107
//
// 这个包刻意不认识任何宿主、任何传输层：它只拥有一份选择，谁要用谁来读。
// 各个 agent 入口读的都是 [Service.CurrentSelection]，所以设置文档一变，
// 下一次读就看得见——不需要任何人重建什么登记级的事实。
package agentdefaultmodel

import (
	"context"
	"fmt"
	"sync"

	"ds-harness-go/core/agent"
	"ds-harness-go/llm"
	"ds-harness-go/settings"
)

// SettingsNamespace 是本包那个设置小节的命名空间。
//
// 源: packages/core/agent-default-model/src/index.ts:20-21
var SettingsNamespace = mustNamespace("agent-default-model")

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: DSH 的 settingsNamespace() 在 TS 里就是一次品牌化转换，编译期定死。
// Go 里 [settings.NewNamespace] 会验一遍并返回错误，而这里的入参是一个包级字面量——
// 它不合法说明本包写错了，不是运行期可以恢复的情况。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("core/agentdefaultmodel: 命名空间字面量不合法：%v", err))
	}
	return namespace
}

// Settings 是存下来、也是装配出来的那一份默认模型选择。
//
// 源: packages/core/agent-default-model/src/index.ts:23-31
//
// 新增: DSH 那份 schema 把 provider 和 model 标成 required、reasoningEffort 留可选。
// Go 里没有那套运行期 schema，所以「必填」由 [validateSettings] 表达，
// 而「可选」用**空串即缺失**表达——这条约定是 [llm.CallConfig].ReasoningEffort 定下的，
// [agent.ModelSelection] 已经照用，这里跟着用，好让三处不必来回翻译指针和零值。
type Settings struct {
	// Provider 是登记过的那个提供方路由键。
	Provider string `json:"provider"`
	// Model 是那个提供方自己拥有的模型标识。
	Model string `json:"model"`
	// ReasoningEffort 是适配器自己拥有的推理档位；空串表示没选，交回给提供方
	// 或者适配器自己的默认行为。
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// validateSettings 拒绝一份缺了提供方或者模型的选择。
//
// 源: packages/core/agent-default-model/src/index.ts:34-38
//
// 这两项是 DSH 那份 schema 上的 `.required()`。放过一个空的提供方，症状会出现在
// 很远的地方：某个 agent 起来之后第一次发请求时才在路由那一层报「查无此提供方」，
// 而那时已经没有任何线索指回这一段设置。
func validateSettings(value Settings) error {
	if value.Provider == "" {
		return fmt.Errorf("core/agentdefaultmodel: 默认模型选择缺了提供方")
	}
	if value.Model == "" {
		return fmt.Errorf("core/agentdefaultmodel: 默认模型选择缺了模型")
	}
	return nil
}

// Config 是这个包的装配入口。
//
// 源: packages/core/agent-default-model/src/index.ts:40-46
type Config struct {
	// Provider 是登记过的那个提供方路由键。
	Provider string
	// Model 是那个提供方自己拥有的模型标识。
	Model string

	// Settings 是可选的设置服务。
	//
	// 新增: DSH 那边这条接线是 `ctx.inject(['settings'], ...)`——没挂设置服务时
	// 整段登记根本不跑，装配那一份就是最终答案。Go 里没有那个万能上下文，
	// 「挂没挂」就是这个字段是不是 nil。
	Settings *settings.Provider
}

// Service 拥有那份默认模型选择，独立于任何宿主和传输层。
//
// 源: packages/core/agent-default-model/src/index.ts:59-105
//
// 新增: DSH 那个类叫 AgentDefaultModelConfig，在 Go 里连上包名会变成
// agentdefaultmodel.AgentDefaultModelConfig——又结巴、又和本包的 [Config]
// （装配入口，DSH 里是另一个类型）撞名。这里按 Go 的习惯改叫 Service，
// 对应 DSH 那个 `extends Service`。
type Service struct {
	// entry 是装配给的那一份，永远不变。没有设置服务、或者设置服务撤走之后，
	// 它就是答案。
	entry Settings

	// mutex 只护 scope 一个字段。
	//
	// 新增: DSH 是单线程 JS，那个 source 函数换来换去不需要同步。Go 里撤登记的那一方
	// 和读选择的那一方是两个 goroutine，这里换的是一个指针，不加锁就是一次真的数据竞争。
	mutex sync.RWMutex
	// scope 是设置服务上那个已登记的小节；nil 表示没挂设置服务、或者已经撤了。
	scope *settings.Scope[Settings]
}

// New 造一个拥有默认模型选择的服务，返回它和撤销函数。
//
// 源: packages/core/agent-default-model/src/index.ts:72-82
//
// 挂了设置服务时，装配那一份作为**组装层**压进登记里（[settings.Options].Base），
// 用户段叠在它之上；这样配置界面看得见「这次部署给的是什么」和「用户改成了什么」
// 两件事各自是什么。没挂设置服务时不登记任何东西，装配那一份就是冻住的答案。
//
// 撤销函数把这个服务退回到装配那一份，然后摘掉登记。退回是有意的：
// DSH 在 installSettingsSection 的拆除里写明了同一件事——设置服务撤走之后，
// 消费方要**照装配时的样子**继续工作，而不是冻在最后读到的那个用户值上。
func New(config Config) (*Service, func(), error) {
	entry := Settings{Provider: config.Provider, Model: config.Model}
	if err := validateSettings(entry); err != nil {
		return nil, nil, err
	}
	service := &Service{entry: entry}
	if config.Settings == nil {
		return service, func() {}, nil
	}

	// 默认值那一层留空：这个包没有「类型自带的默认模型」这种东西，
	// 一个模型选择只可能来自装配或者用户。
	scope, undo, err := settings.Register(config.Settings, SettingsNamespace, Settings{},
		&settings.Options[Settings]{
			Base: map[string]any{
				"provider": entry.Provider,
				"model":    entry.Model,
			},
			Applies:  settings.AppliesLive,
			Validate: validateSettings,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("core/agentdefaultmodel: 登记设置小节失败：%w", err)
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

// currentScope 读出当下那个设置小节句柄，没有就返回 nil。
func (s *Service) currentScope() *settings.Scope[Settings] {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.scope
}

// CurrentSelection 读出当下那份默认模型选择。
//
// 源: packages/core/agent-default-model/src/index.ts:84-90
//
// 每一次都重新读，不缓存：所有消费方都从这里读，所以设置文档提交了一次改动之后，
// 下一次读自然就看得见——没有任何登记级的事实需要跟着重建。
func (s *Service) CurrentSelection() agent.ModelSelection {
	stored := s.entry
	if scope := s.currentScope(); scope != nil {
		stored = scope.Get()
	}
	return agent.ModelSelection{
		Provider:        stored.Provider,
		Model:           stored.Model,
		ReasoningEffort: llm.ReasoningEffortID(stored.ReasoningEffort),
	}
}

// SaveSelection 整段存下一份完整的默认模型选择。
//
// 源: packages/core/agent-default-model/src/index.ts:92-104
//
// 没挂设置服务的部署**不报错**，它保留自己装配那一份：这条路上「存不下来」不是
// 故障，而是这次部署本来就没有可写的存储。整段替换而不是打补丁，是因为交进来的
// 是一份入口已经解析完的完整选择——打补丁会把上一次存的推理档位留在一份没有档位
// 的新选择上。
func (s *Service) SaveSelection(ctx context.Context, next agent.ModelSelection) error {
	scope := s.currentScope()
	if scope == nil {
		return nil
	}
	section := map[string]any{
		"provider": next.Provider,
		"model":    next.Model,
	}
	if next.ReasoningEffort != "" {
		section["reasoningEffort"] = string(next.ReasoningEffort)
	}
	return scope.Replace(ctx, section)
}
