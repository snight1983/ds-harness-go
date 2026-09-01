// 本文件的作用：把 docs/embedding.md 那份装配顺序落成一个真的函数，让「一个外部
// 宿主能把这套组件拼起来」这件事有一处编译期证据。
//
// 新增: DSH 没有对应物，理由见包文档。

package minimalhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/agentloop"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// Options 是宿主装这套最小闭环时必须自己决定的那几样。
//
// 这里每一项都没有默认值可给：提供方名、模型名和适配器是这次部署接的是哪家模型，
// 本仓库无从替它猜。
type Options struct {
	// Provider 是这条模型路由的名字，必填。
	Provider string
	// Model 是这条路由上要用的模型标识，必填。
	Model string
	// Adapter 是把上面那条路由接到线上协议的适配器，必填。
	//
	// 走 OpenAI Chat Completions 兼容服务的话，现成的一份在
	// [github.com/snight1983/ds-harness-go/llm/openaicompat]。
	Adapter llm.Adapter

	// Persona 是部署方那份写进系统提示词的身份与行为约束，可以是空串。
	Persona string

	// ExtraEventTypes 是这次部署额外接的那些模块各自的事件类型。
	//
	// 循环自己那一份（[agent.EventTypes]）由 [Assemble] 并进去，不必在这里重复。
	ExtraEventTypes []sessionlog.EventType

	// Logger 为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Host 是拼好之后，宿主手上握着的那几样东西。
//
// 它们全是构造函数交出来的值，没有一样藏在包级变量里——这正是这套组件能被同一个
// 进程装两份（比如两个租户各一份）的前提。
type Host struct {
	// Scope 是这次装配的根作用域，底下所有登记都挂在它下面。
	Scope *scope.Scope
	// Agents 是活 agent 注册表，宿主的请求入口从它 Create / Resume / Get。
	Agents *agent.Registry
	// Sessions 是活会话存储。
	Sessions *session.Store
	// Models 是模型运行时，[Options].Adapter 已经登记在上面了。
	Models *llm.Runtime
	// Tools 是工具运行时，装配完是空的——业务工具由宿主自己往上装。
	Tools *tools.Runtime
	// SystemPrompt 是系统提示词注册表。
	SystemPrompt *systemprompt.Registry
	// Loop 是循环工厂，已经登记成 [Agents] 的那份造法。
	Loop *agentloop.AgentLoop

	// Vocabulary 是拼好的会话词汇，接持久化时交给它。理由见包文档。
	Vocabulary sessionlog.Vocabulary
}

// Assemble 按 docs/embedding.md 的顺序拼出一份最小闭环，并交出拆除函数。
//
// 拆除按那份文档的关闭顺序**从外向内**：先拆循环工厂（它自己会摘掉造法、再等在跑
// 的回合收尾），最后释放根作用域。反过来的话，一个还在跑的回合会在它依赖的注册表
// 已经没了之后才去写它那条 turn/end。
//
// 交出来的拆除函数不重复执行；两次调用里第二次是空操作。
func Assemble(ctx context.Context, options Options) (*Host, func(context.Context) error, error) {
	if options.Provider == "" || options.Model == "" {
		return nil, nil, errors.New("example/minimalhost: 装一份最小闭环要有提供方和模型")
	}
	if options.Adapter == nil {
		return nil, nil, errors.New("example/minimalhost: 装一份最小闭环要有一个模型适配器")
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// 1. 宿主根作用域。
	//
	// 这里用没有身份的根：这份样例不分租户。真要分的话，每个租户一个
	// [scope.New]，工具、Skill、人设各自装在自己那一层，互相看不见。
	root := scope.NewRoot()
	// 装到一半失败时要把已经建起来的那半拆掉，否则调用方拿到一个错误的同时
	// 还漏着一个作用域——而它手上没有任何句柄能去释放它。
	rollback := func() { _ = root.Dispose(ctx) }

	// 2. 活会话存储。
	//
	// 这是**会话日志**的活那一份，和通用 KV 存储是两回事，见 docs/embedding.md。
	sessions, err := session.NewStore(session.StoreOptions{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 造会话存储失败：%w", err)
	}

	// 3. 模型运行时，并把宿主那个适配器登记到它那条路由上。
	//
	// 登记挂在 root 上，所以根作用域释放时它跟着摘掉，不必单独记一个句柄。
	models := llm.NewRuntime(llm.RuntimeOptions{Logger: logger})
	if _, err := models.RegisterAdapter(ctx, root, []string{options.Provider}, options.Adapter); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 登记模型适配器失败：%w", err)
	}

	// 4. 系统提示词注册表。
	//
	// OmitHarnessIdentity 打开：那句宿主身份声明说的是本装置自己，一个嵌进别人
	// 服务里的组件不该替宿主宣布它是谁。这个开关的存在正是为了这件事。
	prompts, err := systemprompt.NewRegistry(ctx, root, systemprompt.Options{
		OmitHarnessIdentity: true,
		Persona:             options.Persona,
	})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 造系统提示词注册表失败：%w", err)
	}

	// 5. 工具运行时。装配完是空的，业务工具由宿主自己按作用域装上去。
	toolRuntime, err := tools.NewRuntime(tools.Options{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 造工具运行时失败：%w", err)
	}

	// 6. agent 注册表。它只定义「一个活 agent 长什么样」，不造也不驱动任何东西。
	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 造 agent 注册表失败：%w", err)
	}

	// 7. 事件词汇。理由见包文档：这份最小装配自己不检查它，但漏了它的代价落在恢复时。
	vocabulary := sessionlog.CoreVocabulary().
		With(agent.EventTypes()...).
		With(options.ExtraEventTypes...)

	// 8. 循环工厂。
	//
	// 它自己会把自己登记成 [agent.Registry] 那份唯一的造法，撤销也折进了交回来的
	// unwindLoop——所以这里**不要**再调一次 [agent.Registry.SetFactory]，那会撞上
	// 「已经登记过一个 agent 造法」。这正是本包第一次跑起来就抓到的东西：
	// docs/embedding.md 第 10 步原先写的是「创建 Agent Loop，并把 Factory 注册到
	// Agent Registry」，照着字面装的宿主会当场装不起来。
	//
	// Config.Persistence 留空：这份装配没接持久化，于是 [agentloop.AgentLoop.Resume]
	// 会报错、配置里要续跑的项也起不来。这是有意的——恢复需要一个真的介质。
	loop, unwindLoop, err := agentloop.New(ctx, agentloop.Deps{
		Agents:       agents,
		Sessions:     sessions,
		LLM:          models,
		Tools:        toolRuntime,
		SystemPrompt: prompts,
	}, root, agentloop.Config{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("example/minimalhost: 造循环工厂失败：%w", err)
	}

	host := &Host{
		Scope:        root,
		Agents:       agents,
		Sessions:     sessions,
		Models:       models,
		Tools:        toolRuntime,
		SystemPrompt: prompts,
		Loop:         loop,
		Vocabulary:   vocabulary,
	}

	var unwound bool
	return host, func(ctx context.Context) error {
		if unwound {
			return nil
		}
		unwound = true
		// 两步各自跑到底再汇总：前一步失败就提前返回的话，根作用域永远拆不掉。
		return errors.Join(unwindLoop(ctx), root.Dispose(ctx))
	}, nil
}
