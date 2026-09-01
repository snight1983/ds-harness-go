// 本文件的作用：这个包自己拥有的那条运行期不变量——文件系统事件带的身份得是能用的。
//
// 源: packages/fs/fs/src/invariant.ts:1-7

package fs

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/fs/fs/src/invariant.ts:7
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径，理由同 credentials 包：
// 注册表是按名字预留的，而这条约定的拥有者在两边是同一个模块。
const PackageName = "@deepseek-ai/dsh-fs"

// RegisterInvariants 装上「文件系统事件带的身份得是能用的」这条检查，返回注销函数。
//
// 源: packages/fs/fs/src/invariant.ts:20-48
//
// # 这条检查在查什么
//
// 三个通道（[Policy.DecideWriteIntent]、[Policy.DecideEditIntent]、
// [Policy.NotifyObserved]）上流过的身份必须是能用的：
//
//   - [Target.TargetKey] 非空——空 key 会让每一个目标看上去都是同一个，
//     于是一次陈旧守卫会拿别人的版本来比。
//   - [Target.DisplayPath] 非空——它是唯一能展示给人看的字段，空了之后
//     一条「拒绝写入 ""」的诊断谁也读不懂。
//   - 在场观察的 [Present.Version] 非空——空版本存进登记之后，
//     后面那次 [ReplaceIfVersion] 拿它去比，比的是一个从来不会相等的东西。
//
// 三条都不是「用户输入错了」，而是**某个后端或策略把字段漏填了**，
// 也就是不变量该管的那类事：正常代码路径永远不会违反它。
//
// # Go 侧的两个参数分别顶替了什么
//
// 新增: DSH 那边这是一个 cordis 插件（name / inject / apply 三件套），
// 检查挂在容器的 `internal/dispatch` 全局钩子上，按事件名过滤出那三个。
// Go 没有事件总线，也就没有一个能挂全局钩子的地方，于是：
//
//   - registry 顶替 ctx.invariants；
//   - policy 顶替那个全局钩子——检查直接落在 [Policy] 的分发路径上，
//     由「这个包的检查装没装」决定跑不跑。
//
// 这个搬法比 DSH 那边少一层间接：DSH 要按事件名过滤是因为全局钩子看得见
// **所有**事件（invariant.ts:23-25 那三行 if 就是干这个的），
// Go 这边分发方法本来就是分开的，过滤不存在。
//
// # 一个 Policy 只该被装一次
//
// 注册表按包名预留，同一个注册表上装第二次会直接失败。用两个注册表装同一个
// [Policy] 的话，后装的会盖掉先装的——那是一次装配错误，本包不为它兜底。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	policy *Policy,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("fs: 注册不变量需要一个不变量注册表")
	}
	if policy == nil {
		return nil, fmt.Errorf("fs: 注册不变量需要一个策略通道")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		policy.mutex.Lock()
		policy.fail = fail
		policy.mutex.Unlock()

		// 摘掉这一步登记进 scope：注销之后，一条不该再查的检查必须停下来，
		// 否则它会继续在别人的分发路径上抛。
		scope.Defer(func() {
			policy.mutex.Lock()
			policy.fail = nil
			policy.mutex.Unlock()
		})
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
