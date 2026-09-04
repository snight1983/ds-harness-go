// 本文件的作用：把 docs/embedding.md 那份装配顺序落成一个真的函数，让「一个宿主能把
// 这套组件拼起来」这件事有一处编译期证据。
//
// 新增: DSH 没有对应物，理由见包文档。

package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/agentloop"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
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
	// [github.com/snight1983/ds-harness-go/adapter/openaicompat]。
	Adapter llm.Adapter

	// Persona 是部署方那份写进系统提示词的身份与行为约束，可以是空串。
	Persona string

	// ExtraEventTypes 是这次部署额外接的那些模块各自的事件类型。
	//
	// 循环自己那一份（[agent.EventTypes]）由 [New] 并进去，不必在这里重复。
	ExtraEventTypes []sessionlog.EventType

	// Logger 为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Harness 是拼好之后，宿主手上握着的那几样东西。
//
// 它们全是构造函数交出来的值，没有一样藏在包级变量里——这正是这套组件能被同一个
// 进程装两份（比如两个租户各一份）的前提。
type Harness struct {
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
	// Loop 是循环工厂，已经登记成 [Harness.Agents] 的那份造法。
	Loop *agentloop.AgentLoop

	// Vocabulary 是拼好的会话词汇，接持久化时交给它。
	//
	// 本包自己不检查它：一个写日志的模块漏登记自己的事件类型，装配期看不出任何异样，
	// 代价落在恢复时——那条事件会被判成未知事件。所以它摆在交回来的这份结构上，
	// 好让接持久化的那一步有一个现成的、已经并过循环那一份的词汇可以拿。
	Vocabulary sessionlog.Vocabulary
}

// New 按 docs/embedding.md 的顺序拼出一份最小闭环，并交出拆除函数。
//
// 拆除按那份文档的关闭顺序**从外向内**：先拆循环工厂（它自己会摘掉造法、再等在跑
// 的回合收尾），最后释放根作用域。反过来的话，一个还在跑的回合会在它依赖的注册表
// 已经没了之后才去写它那条 turn/end。
//
// 交出来的拆除函数不重复执行；两次调用里第二次是空操作。
//
// 这份装配**不接**存储后端、持久化和协议入口：那三样各自需要一个真的介质，
// 由宿主自己决定接哪一个。于是 [agentloop.AgentLoop.Resume] 在这份装配上会报错，
// 配置里要续跑的项也起不来——恢复需要一份存下来的日志。
func New(ctx context.Context, options Options) (*Harness, func(context.Context) error, error) {
	if options.Provider == "" || options.Model == "" {
		return nil, nil, errors.New("harness: 装一份最小闭环要有提供方和模型")
	}
	if options.Adapter == nil {
		return nil, nil, errors.New("harness: 装一份最小闭环要有一个模型适配器")
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// 1. 宿主根作用域。
	//
	// 这里用没有身份的根：这份装配不分租户。真要分的话，每个租户一个
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
		return nil, nil, fmt.Errorf("harness: 造会话存储失败：%w", err)
	}

	// 3. 模型运行时，并把宿主那个适配器登记到它那条路由上。
	//
	// 登记挂在 root 上，所以根作用域释放时它跟着摘掉，不必单独记一个句柄。
	models := llm.NewRuntime(llm.RuntimeOptions{Logger: logger})
	if _, err := models.RegisterAdapter(ctx, root, []string{options.Provider}, options.Adapter); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("harness: 登记模型适配器失败：%w", err)
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
		return nil, nil, fmt.Errorf("harness: 造系统提示词注册表失败：%w", err)
	}

	// 5. 工具运行时。装配完是空的，业务工具由宿主自己按作用域装上去。
	toolRuntime, err := tools.NewRuntime(tools.Options{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("harness: 造工具运行时失败：%w", err)
	}

	// 6. agent 注册表。它只定义「一个活 agent 长什么样」，不造也不驱动任何东西。
	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("harness: 造 agent 注册表失败：%w", err)
	}

	// 7. 事件词汇。理由见 [Harness.Vocabulary]。
	vocabulary := sessionlog.CoreVocabulary().
		With(agent.EventTypes()...).
		With(options.ExtraEventTypes...)

	// 8. 循环工厂。
	//
	// 它自己会把自己登记成 [agent.Registry] 那份唯一的造法，撤销也折进了交回来的
	// unwindLoop——所以这里**不要**再调一次 [agent.Registry.SetFactory]，那会撞上
	// 「已经登记过一个 agent 造法」。这正是这段装配第一次跑起来就抓到的东西：
	// docs/embedding.md 第 10 步原先写的是「创建 Agent Loop，并把 Factory 注册到
	// Agent Registry」，照着字面装的宿主会当场装不起来。
	loop, unwindLoop, err := agentloop.New(ctx, agentloop.Deps{
		Agents:       agents,
		Sessions:     sessions,
		LLM:          models,
		Tools:        toolRuntime,
		SystemPrompt: prompts,
	}, root, agentloop.Config{Logger: logger})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("harness: 造循环工厂失败：%w", err)
	}

	assembled := &Harness{
		Scope:        root,
		Agents:       agents,
		Sessions:     sessions,
		Models:       models,
		Tools:        toolRuntime,
		SystemPrompt: prompts,
		Loop:         loop,
		Vocabulary:   vocabulary,
	}

	// 幂等走 [sync.Once] 而不是一个裸布尔：拆宿主的两条路（正常收尾和某处出错时的
	// 兜底）常常在不同 goroutine 上，一个裸布尔在那里是数据竞争，而且两边会同时读到
	// false、把 unwindLoop 和 root.Dispose 各跑一遍。
	var once sync.Once
	var unwindErr error
	return assembled, func(ctx context.Context) error {
		once.Do(func() {
			// 两步各自跑到底再汇总：前一步失败就提前返回的话，根作用域永远拆不掉。
			unwindErr = errors.Join(unwindLoop(ctx), root.Dispose(ctx))
		})
		return unwindErr
	}, nil
}
