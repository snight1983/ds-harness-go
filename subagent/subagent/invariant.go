// 本文件的作用：这个包自己拥有的那两条运行期不变量——提供方注册表的收支平衡，
// 和 start／end 那对生命周期边的配对。
//
// 源: packages/subagent/subagent/src/invariant.ts

package subagent

import (
	"context"
	"fmt"
	"sync"

	"ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/subagent/subagent/src/invariant.ts:7
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径，理由同 fs、credentials、llm：
// 注册表是按名字预留的，而这条约定的拥有者在两边是同一个模块。
const PackageName = "@deepseek-ai/dsh-subagent"

// lifecycleInvariant 是这条接缝那份检查的状态：注册表里当下有哪些提供方，
// 以及哪些运行发过 start 却还没发 end。
//
// 源: packages/subagent/subagent/src/invariant.ts:23-29
//
// 新增: DSH 分两拍——先在 cordis 的 `internal/dispatch` 钩子里验、把对象记进一个
// WeakSet 暂存，再在真正的监听器里认那个暂存并提交状态。那两拍是它那套事件系统
// 逼出来的（验必须跑在所有监听器之前，而提交必须跑在这条边真的发出去之后）。
// Go 这边这份检查有一个**专属**的调用点，就在发射器兜底循环的前面（见
// [lifecycleEmitter.check]），验和提交在同一次调用里一前一后，所以那个 WeakSet
// 暂存没有对应物。语义一样：验不过就 panic，那一行提交压根走不到。
type lifecycleInvariant struct {
	fail invariants.Fail

	// mutex 守住下面两张表。fail 一律在锁**外面**叫——它是 panic，在临界区里抛
	// 会把这把锁永远留在锁着的状态（和 [ds-harness-go/llm.Runtime] 那处同一条规矩）。
	mutex     sync.Mutex
	providers map[string]struct{}
	runs      map[RunID]RunInfo
}

// providerAdded 验一条「提供方来了」，验过之后把它记进注册表镜像。
//
// 源: packages/subagent/subagent/src/invariant.ts:32-39
func (i *lifecycleInvariant) providerAdded(provider Provider) {
	name := provider.Name()
	i.mutex.Lock()
	var violation string
	switch {
	case name == "":
		violation = "subagent provider names must be non-empty"
	default:
		if _, present := i.providers[name]; present {
			violation = fmt.Sprintf("subagent/provider-added repeated %q", name)
		}
	}
	if violation == "" {
		i.providers[name] = struct{}{}
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// providerRemoved 验一条「提供方走了」，验过之后把它从注册表镜像里摘掉。
//
// 源: packages/subagent/subagent/src/invariant.ts:40-45
func (i *lifecycleInvariant) providerRemoved(name string) {
	i.mutex.Lock()
	_, present := i.providers[name]
	if present {
		delete(i.providers, name)
	}
	i.mutex.Unlock()
	if !present {
		i.fail(fmt.Sprintf("subagent/provider-removed names unknown provider %q", name))
	}
}

// runStarted 验一条 `subagent/start`，验过之后把这次运行的身份记下来。
//
// 源: packages/subagent/subagent/src/invariant.ts:46-57
//
// **不**查「这个提供方还在不在注册表里」：那是准入那一刻的关系。一次已发布的
// 一次性运行可以活得比它的提供方长，而一次冷恢复出来的活化会照着描述符里记的
// 那个提供方名字发边，根本不经过它派发。
func (i *lifecycleInvariant) runStarted(info RunInfo) {
	i.mutex.Lock()
	var violation string
	switch {
	case info.Provider == "" || info.RunID == "" || info.ID == "":
		violation = "subagent/start provider, runId, and child id must be non-empty"
	default:
		if _, repeated := i.runs[info.RunID]; repeated {
			violation = fmt.Sprintf("subagent/start repeated run id %q", string(info.RunID))
		}
	}
	if violation == "" {
		i.runs[info.RunID] = info
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// runEnded 验一条 `subagent/end`：它必须配得上一条发过的 start，而且身份一致。
//
// 源: packages/subagent/subagent/src/invariant.ts:14-20、58-64
func (i *lifecycleInvariant) runEnded(info RunEndInfo) {
	i.mutex.Lock()
	start, paired := i.runs[info.RunID]
	var violation string
	switch {
	case !paired:
		violation = fmt.Sprintf("subagent/end has no matching subagent/start for run %q", string(info.RunID))
	case start.Provider != info.Provider || start.ID != info.ID || start.Local != info.Local:
		violation = fmt.Sprintf("subagent/end identity diverges from subagent/start for run %q", string(info.RunID))
	default:
		delete(i.runs, info.RunID)
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// RegisterInvariants 装上本包那两条检查，返回注销函数。
//
// 源: packages/subagent/subagent/src/invariant.ts:88-92
//
// # 这两条检查在查什么
//
// 第一条是**提供方注册表的收支平衡**：名字非空、不重复登记、也不会摘掉一个从来
// 没登记过的名字。它防的是「注册表镜像和真表分了岔」——一条摘除边报出一个陌生的
// 名字，说明某次登记的回滚跑了两遍，或者跑在了一次它并不拥有的登记上。
//
// 第二条是 start／end **配对**：每一条 start 都有非空的提供方、运行 id 和孩子 id，
// 一个运行 id 不重开，每一条 end 都配得上一条发过的 start，而且两边说的提供方、
// 孩子和「是不是同进程」必须一致。一次性运行和可续活化用的是同一套词汇，所以这条
// 检查同时看着两种形态。
//
// # 装在哪一层
//
// 这份检查有一个专属的调用点，跑在生命周期发射器那圈兜底观察者**之前**（见
// [lifecycleEmitter.check]）。它必须在外面：一条被 [lifecycleEmitter.contain]
// 吞成一行警告日志的不变量检查等于没有检查。
//
// # 一台 Runtime 只该被装一次
//
// 注册表按包名预留，同一个注册表上装第二次会直接失败。用两个注册表装同一台
// [Runtime] 的话，后装的会盖掉先装的——那是一次装配错误，本包不为它兜底。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	runtime *Runtime,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("subagent: 注册不变量需要一个不变量注册表")
	}
	if runtime == nil {
		return nil, fmt.Errorf("subagent: 注册不变量需要一台运行时")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		// 拿当下那份注册表当种子：这份检查可以装在若干次登记之后，那些提供方
		// 是既成事实，不是违例。
		seeded := map[string]struct{}{}
		for _, name := range runtime.List() {
			seeded[name] = struct{}{}
		}
		runtime.emitLifecycle.check.Store(&lifecycleInvariant{
			fail:      fail,
			providers: seeded,
			runs:      map[RunID]RunInfo{},
		})
		// 摘掉这一步登记进 scope：注销之后，一条不该再查的检查必须停下来，
		// 否则它会继续在别人的登记和派发路径上抛。
		scope.Defer(func() { runtime.emitLifecycle.check.Store(nil) })
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
