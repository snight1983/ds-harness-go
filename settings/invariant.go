// 本文件的作用：这个包自己拥有的那条运行期不变量。
//
// 源: packages/settings/settings/src/invariant.ts:1-4

package settings

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/settings/settings/src/invariant.ts:10
//
// 沿用 DSH 侧的包名字面量，理由同本仓库 credentials.PackageName：
// 注册表是按名字预留的，而这条约定的拥有者在两边是同一个模块。
const PackageName = "@deepseek-ai/dsh-settings"

// RegisterInvariants 装上「提交事件的约定」这条检查，返回注销函数。
//
// 源: packages/settings/settings/src/invariant.ts:17-48
//
// # 这条检查在查什么
//
// 一次已提交变更的通知必须同时满足四件事，缺一它说的话就不成立：
//
//  1. **服务还活着。** 通知陈述的是一次已经提交的变更，服务都拆了就没有「提交进哪里」可言。
//  2. **命名空间当前是登记着的。** 一个已经注销的命名空间不该再有人替它宣布变更。
//  3. **通知里的值就是服务此刻的权威解析值。** 对不上意味着通知和状态分了叉，
//     而收到通知的那一方会照着通知里的值走——两边从此各说各话，且谁都不会发现。
//  4. **解析值确实变了。** 一次「其实没变」的通知会让每一个观察者白跑一遍，
//     更糟的是它会把「变更」这个词的含义稀释掉。
//
// 全部用这条接缝**自己**的等值判定 [DeepEqualJSON]，不另写一份。
// 两份深比较会在有出入的地方误报或漏报，而那正是最难看出来的一类。
//
// # 为什么第三条要在这里查
//
// 通知是在 [Provider.commit] 里换完值之后发的，看上去不可能对不上。
// 但发通知这一步不持 mutex（持着它调用户代码会死锁），所以「换值」和「读到值」
// 之间隔着一段窗口；这条检查钉的就是这段窗口里不许有第二次提交插进来——
// 而那正是 commitMutex 存在的理由。检查和那把锁互为对方的证据。
//
// # 三个参数分别顶替了什么
//
// 新增: DSH 那边这是一个 cordis 插件（name / inject / apply 三件套），装配由容器负责。
// Go 里没有容器，装配就是调用方写的那一行，所以这三件事得显式递进来：
//
//   - registry 顶替 ctx.invariants；
//   - provider 顶替 ctx.on('settings/updated', ...) 和 ctx.get('settings')；
//   - live 顶替 `settings === undefined` 那一问。
//
// live 由装配方给而不是本包自己判断，理由同 credentials：
// 「这个服务还挂着吗」只有那个 New 出它、也负责关掉它的人知道。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	provider *Provider,
	live func() bool,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("settings: 注册不变量需要一个不变量注册表")
	}
	if provider == nil {
		return nil, fmt.Errorf("settings: 注册不变量需要一个设置服务")
	}
	if live == nil {
		return nil, fmt.Errorf("settings: 注册不变量需要一个「服务还活着吗」的判据")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		// 退订登记进 scope：注销这次注册时监听器必须跟着摘掉，
		// 否则一条不该再查的检查会继续在别人的提交路径上抛。
		scope.Defer(provider.SubscribeUpdated(func(ns Namespace, next, prev map[string]any, _ Source) {
			if !live() {
				fail(fmt.Sprintf("命名空间 %q 的提交变更事件发出时，设置服务已经不在了", string(ns)))
			}
			current, registered := provider.Get(ns)
			if !registered {
				fail(fmt.Sprintf("命名空间 %q 的提交变更事件发出时，它已经不是登记着的了", string(ns)))
			}
			if !DeepEqualJSON(toAny(current), toAny(next)) {
				fail(fmt.Sprintf("命名空间 %q 的提交变更事件带的值和服务此刻的权威解析值对不上", string(ns)))
			}
			if DeepEqualJSON(toAny(next), toAny(prev)) {
				fail(fmt.Sprintf("命名空间 %q 的提交变更事件发出时，解析值其实没变", string(ns)))
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
