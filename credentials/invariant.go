// 本文件的作用：这个包自己拥有的那条运行期不变量。
//
// 源: packages/credentials/credentials/src/invariant.ts:1-4

package credentials

import (
	"context"
	"fmt"

	"ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/credentials/credentials/src/invariant.ts:9
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径：不变量注册表是按名字预留的，
// 而这条约定的拥有者在两边是同一个模块。换个名字，两边的诊断日志就对不上了。
const PackageName = "@deepseek-ai/dsh-credentials"

// RegisterInvariants 装上「提交事件的生命周期约定」这条检查，返回注销函数。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// # 这条检查在查什么
//
// `credentials/reference-updated` 陈述的是一次**已经提交**的提供方来源变更，
// 所以它只可能在一个活着的凭据服务上发生。服务已经拆掉之后还发出来，
// 说明某个提供方把工作漏到了自己的收尾静默期之后——也就是那次「提交」
// 到底提交进了哪里，已经没有人答得上来。
//
// 值本身的关系（Describe 说的和 Resolve 给的对不对得上）不在这里查：
// 那是异步的提供方 I/O，由每个提供方自己的用例钉。
//
// # Go 侧的三个参数分别顶替了什么
//
// 新增: DSH 那边这是一个 cordis 插件（name / inject / apply 三件套），
// 装配由容器负责。Go 里没有容器，装配就是调用方自己写的这一行，所以
// 「注册表在哪」「订阅哪张表」「服务还活着吗」这三件事都得显式递进来：
//
//   - registry 顶替 ctx.invariants；
//   - observer 顶替 ctx.on(...)，也就是订阅那两个事件的地方；
//   - live 顶替 ctx.get('credentials') !== undefined。
//
// live 由装配方提供而不是由本包自己判断，是因为「这个提供方还挂着吗」这件事
// 本来就只有装配方知道——它是那个 New 出提供方、也负责关掉它的人。
// 本包越权去猜的话，就等于凭空发明了一套它并不拥有的生命周期。
//
// # 通知器要比提供方活得长
//
// 装配的次序是：先造 [Notifier]，把它交给提供方内嵌，再用同一个通知器注册这条检查。
// 反过来（让检查订阅在提供方身上）的话，提供方一拆，这条检查连同它一起没了——
// 而它要查的恰恰就是**提供方拆掉之后**发生的事。
//
// 新增: DSH 只在引用那一半装了这条检查，记录那一半没有。这里照此实现。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	observer Observer,
	live func() bool,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("credentials: 注册不变量需要一个不变量注册表")
	}
	if observer == nil {
		return nil, fmt.Errorf("credentials: 注册不变量需要一个可订阅的提供方")
	}
	if live == nil {
		return nil, fmt.Errorf("credentials: 注册不变量需要一个「服务还活着吗」的判据")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		// 退订登记进 scope：注销这次注册时，这个监听器必须跟着一起摘掉，
		// 否则一个已经不该再查的检查会继续在别人的提交路径上抛。
		scope.Defer(observer.SubscribeReference(func(ref Ref) {
			if !live() {
				fail(fmt.Sprintf("引用 %q 的提交变更事件发出时，凭据服务已经不在了", string(ref)))
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
