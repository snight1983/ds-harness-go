// 本文件的作用：把「循环跑起来之前必须先有的那五样」凑齐，交给用例。

package harnesstest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// Options 是这副脚手架上用例真正可能想调的那几处。
//
// 源: packages/test-support/agent-loop-testkit/src/index.ts:17-23（AgentLoopTestDependenciesOptions）
//
// 新增: DSH 那份只透两项（systemPrompt、tools 两个插件的配置），因为其余三个插件
// 在它那边不吃配置。Go 这边多出 Owner 与 Logger 两项：作用域在 Go 里是显式的值而
// 不是 cordis 的环境上下文，日志则默认要压掉——脚手架不该在用例的输出里刷屏。
type Options struct {
	// Owner 是这几样东西挂靠的作用域，为 nil 时本包自己开一个没有身份的根。
	//
	// 想验「作用域一散、登记跟着掉」的用例把自己那个交进来，然后自己释放它。
	Owner *scope.Scope

	// SystemPrompt 是系统提示词注册表的配置。
	//
	// 零值就是 DSH 的默认行为。留意 OmitHarnessIdentity：不打开的话每一次装配都会
	// 多出一段宿主身份声明，验提示词文本的用例多半要打开它。
	SystemPrompt systemprompt.Options

	// Tools 是工具运行时的配置，零值即可。
	Tools tools.Options

	// Logger 为 nil 时用一个把所有输出丢掉的 logger，不是 [slog.Default]。
	//
	// 新增: 生产装配那边为 nil 走 slog.Default，这里反过来。理由是一次
	// go test ./... 里这些包会被装起来成百上千次，默认那个 logger 会把
	// 用例真正的失败信息淹掉。要看日志的用例自己交一个进来。
	Logger *slog.Logger

	// Now 是会话存储用的时钟，为 nil 时用一个每读一次加一毫秒的假时钟。
	//
	// 新增: 真时钟会让同一毫秒内落的两条事件拿到相同的时间戳，快照比对因此不稳。
	Now func() int64
}

// Dependencies 是凑齐之后交回来的那几样，外加一条拆除链。
//
// 源: packages/test-support/agent-loop-testkit/src/index.ts:36-45
//
// 新增: DSH 那边这些东西挂在 cordis 上下文上，函数本身返回 void——拿它们靠
// ctx.llm / ctx.session 这些字段。Go 没有那个服务表，所以按本仓库一贯的做法
// 显式交回来。字段名和 [github.com/snight1983/ds-harness-go/harness.Harness]
// 上对应的那几个一致，好让两边的用例读起来是一回事。
type Dependencies struct {
	// Scope 是这几样东西挂靠的作用域。用例没指定 Owner 时它是本包开的那个根。
	Scope *scope.Scope
	// Models 是模型运行时，上面**没有**登记任何适配器。
	Models *llm.Runtime
	// Sessions 是活会话存储。
	Sessions *session.Store
	// SystemPrompt 是系统提示词注册表。
	SystemPrompt *systemprompt.Registry
	// Tools 是工具运行时，上面**没有**装任何工具。
	Tools *tools.Runtime
	// Agents 是 agent 注册表，上面**没有**登记造法。
	Agents *agent.Registry

	// ownsScope 记着 Scope 是不是本包开的——不是的话拆除时不许碰它。
	ownsScope bool
	// once 让 Dispose 幂等：[Mount] 挂的那次 t.Cleanup 和用例自己那次
	// 常常都会跑到。
	once sync.Once
	// disposeErr 是第一次拆除的结果，之后每次原样交回。
	disposeErr error
}

// Mount 凑齐那五样，并把释放挂到用例收尾上。
//
// 源: packages/test-support/agent-loop-testkit/src/index.ts:36-45（mountAgentLoopTestDependencies）
//
// 造不出来当场 t.Fatalf：一个连先决依赖都立不起来的用例，后面每一句断言都没有意义。
// 要自己接住那个错误的（比如就是想验某一样造不出来时会怎样），走 [New]。
func Mount(t *testing.T, options Options) *Dependencies {
	t.Helper()
	deps, err := New(context.Background(), options)
	if err != nil {
		t.Fatalf("凑循环的先决依赖失败：%v", err)
	}
	t.Cleanup(func() {
		if err := deps.Dispose(context.Background()); err != nil {
			t.Errorf("拆先决依赖失败：%v", err)
		}
	})
	return deps
}

// New 凑齐那五样，不碰 testing。
//
// 造到一半失败时已经建起来的那半会被拆掉，调用方拿到错误的同时不会漏着一个作用域。
// 这一条和 [github.com/snight1983/ds-harness-go/harness.New] 的理由一样。
func New(ctx context.Context, options Options) (*Dependencies, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := options.Now
	if now == nil {
		now = tickingClock()
	}

	deps := &Dependencies{Scope: options.Owner}
	if deps.Scope == nil {
		deps.Scope = scope.NewRoot()
		deps.ownsScope = true
	}
	rollback := func() {
		if deps.ownsScope {
			_ = deps.Scope.Dispose(ctx)
		}
	}

	deps.Models = llm.NewRuntime(llm.RuntimeOptions{Logger: logger})

	store, err := session.NewStore(session.StoreOptions{Logger: logger, Now: now})
	if err != nil {
		rollback()
		return nil, fmt.Errorf("harnesstest: 造会话存储失败：%w", err)
	}
	deps.Sessions = store

	promptOptions := options.SystemPrompt
	prompts, err := systemprompt.NewRegistry(ctx, deps.Scope, promptOptions)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("harnesstest: 造系统提示词注册表失败：%w", err)
	}
	deps.SystemPrompt = prompts

	toolOptions := options.Tools
	if toolOptions.Logger == nil {
		toolOptions.Logger = logger
	}
	toolRuntime, err := tools.NewRuntime(toolOptions)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("harnesstest: 造工具运行时失败：%w", err)
	}
	deps.Tools = toolRuntime

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: logger})
	if err != nil {
		rollback()
		return nil, fmt.Errorf("harnesstest: 造 agent 注册表失败：%w", err)
	}
	deps.Agents = agents

	return deps, nil
}

// Dispose 释放本包开的那个作用域。用例自己交进来的作用域**不碰**。
//
// 重复调用是空操作，第二次原样交回第一次那个结果。
func (d *Dependencies) Dispose(ctx context.Context) error {
	d.once.Do(func() {
		if d.ownsScope {
			d.disposeErr = d.Scope.Dispose(ctx)
		}
	})
	return d.disposeErr
}

// tickingClock 是每读一次加一毫秒的假时钟。
//
// 新增: 用原子加而不是裸变量——存储在锁外面读它，而同一份依赖可能被好几条
// goroutine 同时碰。这份写法和 harness/session、harness/agentloop 各自测试里
// 那个同名工具一致。
func tickingClock() func() int64 {
	tick := int64(1000)
	return func() int64 { return atomic.AddInt64(&tick, 1) }
}
