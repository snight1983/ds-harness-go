// 本文件的作用：这台循环本身——它攥着什么、怎么装到一个作用域上、
// 怎么按作用域找回当时那个 Agent。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/settings"
)

// AgentLoop 是那份具体的 agent 造法，也是驱动这一层的服务。
//
// 源: packages/core/agent-loop/src/index.ts:351-774（AgentLoop）
//
// 新增: DSH 那个类继承 cordis 的 Service、带一个 static inject 依赖清单和一份
// schemastery 的运行期 Config schema。Go 里前两样由 [New] 的显式参数顶掉，
// 第三样由 [Config] 这个结构体加上 [New] 里那几道校验顶掉——一份 Go 结构体本身
// 就是那个 schema，剩下的只有 schema 表达不了的那些跨字段规则。
type AgentLoop struct {
	deps   Deps
	owner  *scope.Scope
	logger *slog.Logger

	// config 是已经盖过启动器身份、也校验过的那一份。
	config Config
	// settingsScope 非 nil 时，并行上限每次都从它读透。
	settingsScope *settings.Scope[Settings]
	// staticCap 是设置不在位时锁住的那个上限。
	staticCap int

	ownership *factoryOwnership

	// byScope 把一个作用域键映射回它那个活 agent。
	//
	// 新增: DSH 靠 cordis 从上下文上直接取 `ctx.agent`——那三个系统提示词变量
	// （provider、model）读的是 `context.agent?...`，而 [Registry.Enter]
	// 要的那个 owner 读的是 `ownerCtx.agent`。Go 里作用域上挂不了值，所以工厂
	// 自己维护这张表：公布时填，拆除时清。查不到就是 DSH 那个 `?.` 的短路。
	agentsMutex sync.Mutex
	byScope     map[*scope.Key]*ReactLoopAgent

	startFailed *scope.AnonymousEntries[ConfigStartFailedObserver]
}

// AgentLoop 就是那份造法本身，编译期钉住这件事。
var _ agent.Factory = (*AgentLoop)(nil)

// New 装一个循环工厂：登记造法、装上那三个系统提示词变量，然后把配置里那些
// agent 起起来。返回的函数拆掉这一整套。
//
// 源: packages/core/agent-loop/src/index.ts:318-382（constructor）
//
// owner 是拥有这个工厂的作用域，工厂造出来的每一个 agent 的作用域都挂在它下面。
//
// 新增: deps.MaxParallelToolCalls 由这里**接管**——本包这一层正是「把设置接上去」
// 的那一层（见 [Deps].MaxParallelToolCalls 的字段说明），调用方填的任何值都会被
// 换成工厂自己那个读透函数。
func New(ctx context.Context, deps Deps, owner *scope.Scope, config Config) (*AgentLoop, func(context.Context) error, error) {
	if deps.Agents == nil || deps.Sessions == nil || deps.LLM == nil ||
		deps.Tools == nil || deps.SystemPrompt == nil {
		return nil, nil, errors.New("harness/agentloop: 装一个循环工厂要有注册表、会话存储、模型、工具和系统提示词五样")
	}
	if owner == nil {
		return nil, nil, errors.New("harness/agentloop: 装一个循环工厂要有一个持有它的作用域")
	}

	staticCap, err := resolveMaxParallelToolCalls(config.MaxParallelToolCalls)
	if err != nil {
		return nil, nil, err
	}
	config.Agents = applyLauncherIdentities(config.Agents, config.LauncherIdentities)
	if err := validateConfiguredAgents(config.Agents); err != nil {
		return nil, nil, err
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	loop := &AgentLoop{
		deps:        deps,
		owner:       owner,
		logger:      logger,
		config:      config,
		staticCap:   staticCap,
		ownership:   newFactoryOwnership(),
		byScope:     make(map[*scope.Key]*ReactLoopAgent),
		startFailed: scope.NewAnonymousEntries[ConfigStartFailedObserver](),
	}
	loop.deps.MaxParallelToolCalls = loop.maxParallelToolCalls

	// 拆除是后进先出的一条链，所以这一条登记在最前面、跑在最后面：造法先摘掉
	// （不再有人能从这里要新 agent），三个变量再摘掉，最后才把在飞的那些 agent
	// 排干。反过来的话，排干过程中的最后几个步骤会装不出系统提示词。
	var teardown []func(context.Context) error
	keep := func(undo func(context.Context) error, err error) error {
		if err != nil {
			return err
		}
		teardown = append(teardown, undo)
		return nil
	}
	unwind := func(ctx context.Context) error {
		var failures []error
		for index := len(teardown) - 1; index >= 0; index-- {
			if err := teardown[index](ctx); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	fail := func(err error) (*AgentLoop, func(context.Context) error, error) {
		return nil, nil, errors.Join(err, unwind(ctx))
	}

	if err := keep(owner.Defer("agentLoop.transactions()", loop.ownership.dispose)); err != nil {
		return fail(err)
	}

	if config.Settings != nil {
		settingsScope, undo, err := settings.Register(config.Settings, SettingsNamespace,
			Settings{MaxParallelToolCalls: staticCap}, &settings.Options[Settings]{
				Applies: settings.AppliesLive,
				// schema 那一层只管得住「是个正整数」，而整条规则归
				// resolveMaxParallelToolCalls 拥有；在这里拒掉一次坏改动，
				// 跑着的调度器就停在上一个好值上，而不是等到下一组工具调用才炸。
				Validate: func(value Settings) error {
					_, err := resolveMaxParallelToolCalls(value.MaxParallelToolCalls)
					return err
				},
			})
		if err != nil {
			return fail(fmt.Errorf("harness/agentloop: 登记设置小节失败：%w", err))
		}
		loop.settingsScope = settingsScope
		teardown = append(teardown, func(context.Context) error { undo(); return nil })
	}

	if err := keep(deps.Agents.SetFactory(loop)); err != nil {
		return fail(fmt.Errorf("harness/agentloop: 登记 agent 造法失败：%w", err))
	}

	// 新增: DSH 在这里还登记第三个变量 `cwd`，把宿主机工作目录摆给模型看。本仓库
	// 没有这一项：服务端没有工作目录（见 [sessionlog.SessionHeader.WorkspaceID]），
	// 告诉模型「你的工作目录是 /x」是一句谎话——它会照着去拼路径、去猜相对位置，
	// 而那些路径在这套部署里指不到任何东西。换成工作区标识也不行：那是一个不透明
	// 的 id，对模型只是噪音。
	for name, read := range map[string]func(*ReactLoopAgent) string{
		"provider": func(a *ReactLoopAgent) string { return a.Options().Provider },
		"model":    func(a *ReactLoopAgent) string { return a.Options().Model },
	} {
		if err := keep(deps.SystemPrompt.Variable(ctx, owner, name, loop.agentVariable(read))); err != nil {
			return fail(fmt.Errorf("harness/agentloop: 登记系统提示词变量 %q 失败：%w", name, err))
		}
	}

	loop.startConfiguredAgents(ctx)
	return loop, unwind, nil
}

// agentVariable 把一个「从 agent 上读一个字段」的函数包成系统提示词变量。
//
// 源: packages/core/agent-loop/src/index.ts:377-379
//
// 装配的作用域上没有 agent（比如一次不属于任何 agent 的装配）时交出 nil，
// 那正是 DSH 那三行里 `context.agent?.` 短路成 undefined 的意思。
func (l *AgentLoop) agentVariable(read func(*ReactLoopAgent) string) systemprompt.VariableProvider {
	return func(_ context.Context, assemble systemprompt.AssembleContext) (*string, error) {
		live := l.agentForScope(assemble.Scope)
		if live == nil {
			return nil, nil
		}
		value := read(live)
		return &value, nil
	}
}

// agentForScope 查一个作用域键上那个活 agent。
func (l *AgentLoop) agentForScope(key *scope.Key) *ReactLoopAgent {
	if key == nil {
		return nil
	}
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	return l.byScope[key]
}

// maxParallelToolCalls 读出当下的并行上限，设置在位时每次都读透。
//
// 源: packages/core/agent-loop/src/index.ts:330-334
//
// DSH 那段注释写的是「tool-calls.ts 在每一组开头解构它，所以一次提交过的改动
// 只影响下一组，不打扰在飞的那一组」——本包 [ReactLoopAgent.maxParallelToolCalls]
// 的调用位置就在同一个地方。
func (l *AgentLoop) maxParallelToolCalls() int {
	if l.settingsScope == nil {
		return l.staticCap
	}
	resolved, err := resolveMaxParallelToolCalls(l.settingsScope.Get().MaxParallelToolCalls)
	if err != nil {
		// 走不到：Validate 已经把坏值挡在提交之前了。真漏进来一个的话，
		// 停在那个静态上限上比让调度器拿一个非法的池宽度跑要好。
		return l.staticCap
	}
	return resolved
}

// OnConfigStartFailed 登记一个「配置驱动的启动失败了」的观察者。
//
// 源: packages/core/agent-loop/src/index.ts:160-179
func (l *AgentLoop) OnConfigStartFailed(observer ConfigStartFailedObserver) (func(), error) {
	if observer == nil {
		return nil, errors.New("harness/agentloop: OnConfigStartFailed 需要一个观察者")
	}
	return l.startFailed.Append(observer), nil
}

// rememberAgent 把一个刚公布的 agent 记进作用域索引。
func (l *AgentLoop) rememberAgent(live *ReactLoopAgent) {
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	l.byScope[live.Scope().Key()] = live
}

// forgetAgent 把一个拆掉的 agent 从作用域索引里摘掉。
//
// 不摘的话这张表会一直长，而且一个已经死掉的 agent 还能被系统提示词变量读到。
func (l *AgentLoop) forgetAgent(live *ReactLoopAgent) {
	l.agentsMutex.Lock()
	defer l.agentsMutex.Unlock()
	delete(l.byScope, live.Scope().Key())
}
