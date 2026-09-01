// 本文件的作用：这个包自己拥有的那条运行期不变量。
//
// 源: packages/workspace/workspace/src/invariant.ts

package workspace

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/workspace/workspace/src/invariant.ts:11
const PackageName = "@deepseek-ai/dsh-workspace"

// RegisterInvariants 装上「实体缓存就是落盘表的镜像」这条检查，返回注销函数。
//
// 源: packages/workspace/workspace/src/invariant.ts:27-57
//
// # 这条检查在查什么
//
// 登记册的实体缓存和 workspaces 表是互为镜像的，而这个镜像由**写入次序**维持：
//
//   - 建的时候实体先进缓存、再落盘（见 [Registry.createCanonical]），所以一次
//     put 事件发出时缓存里必须已经有它。没有，说明有人绕过登记册往这张表里写了。
//   - 删的时候实体先离开缓存、再删记录（见 [Registry.deleteKnown]），所以一次
//     deleted 事件发出时缓存里必须已经没有它。还在，说明同上。
//
// 两条都是「有人绕过了 [Registry]」的证据。绕过之后内存和介质会各说各话，
// 而且没有任何一步会报错——这正是它值得一条不变量的原因。
//
// # 必须在 [Open] 成功之后再注册
//
// 新增: DSH 那边这条检查的 installer 上挂着 `inject: ['workspaceRegistry']`
// （invariant.ts:48），cordis 会等那个服务变活之后才装；而历史 bootstrap 跑在
// `[Service.init]` 里，也就是服务变活**之前**——所以 bootstrap 那一批 put
// 天然不在这条检查的视野里。
//
// Go 里没有那个容器，这件事靠签名表达：这个函数收的是一个**已经打开的**
// *[Registry]，拿不到它就注册不了，而拿到它就意味着 [Open] 已经返回。
// bootstrap 期间的写因此同样落在装上这条检查之前。
//
// live 由装配方给而不是本包自己判断，理由同 [domain.RegisterInvariants]：
// 「这个登记册还挂着吗」只有那个 [Open] 出它、也负责 [Registry.Close] 的人知道。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	facility *domain.Facility,
	workspaces *Registry,
	live func() bool,
) (func(), error) {
	if registry == nil {
		return nil, fail(CodeInvalidConfig, "注册不变量需要一个不变量注册表")
	}
	if facility == nil {
		return nil, fail(CodeInvalidConfig, "注册不变量需要一个域设施")
	}
	if workspaces == nil {
		return nil, fail(CodeInvalidConfig, "注册不变量需要一个已经打开的工作区登记册")
	}
	if live == nil {
		return nil, fail(CodeInvalidConfig, "注册不变量需要一个「登记册还活着吗」的判据")
	}

	install := func(_ context.Context, scope *invariants.Scope, failCheck invariants.Fail) error {
		// 退订登记进 scope：注销这次注册时订阅必须跟着摘掉，
		// 否则一条不该再查的检查会继续在别人的写路径上抛。
		scope.Defer(facility.Subscribe(func(change domain.Changed) {
			if change.Domain != DomainName || change.Table != TableName {
				return
			}
			if !live() {
				return
			}
			_, cached := workspaces.Get(WorkspaceID(change.Key))
			if change.Operation == domain.OperationDeleted {
				if cached {
					failCheck(fmt.Sprintf(
						"工作区记录 %q 被删了，但登记册缓存还在发布它——有写入路径绕过了登记册",
						change.Key))
				}
				return
			}
			if !cached {
				failCheck(fmt.Sprintf(
					"工作区记录 %q 已经落盘，但登记册缓存里没有对应的实体——缓存和域表分叉了",
					change.Key))
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
