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

// RegisterInvariants 装上「workspaces 表只由登记册来写」这条检查，返回注销函数。
//
// 源: packages/workspace/workspace/src/invariant.ts:27-57
//
// # 这条检查在查什么
//
// 本包对 workspaces 表的每一次写，都被 [Registry.beginWrite] 记了一笔，而域的
// 变更通知是**在写链的槽位里同步发的**（见 [domain.ChangedListener]），所以一条
// 事件到达时那笔记账必然还举着。反过来说：一条针对这张表、而记账上查无此人的事件，
// 只可能来自一次绕过 [Registry] 的写。
//
// 绕过之后落盘的记录会和登记册次序、和归属账目各说各话，而且没有任何一步会报错
// ——这正是它值得一条不变量的原因。
//
// # 为什么不去读一遍介质核对
//
// 新增: DSH 那条检查是拿实体缓存和落盘表比镜像。缓存删掉之后（见 [Registry]）
// 那个比法没了对象，而换成「事件到了就回头读一次表核对」是**错的**两次：
//
//   - 它会误报。别的副本完全可以合法地插在这次提交和这次核对之间，把同一条记录
//     再改一遍甚至删掉；那时读回来的东西和事件对不上，但没有任何人做错事。
//     一条会误报的检查比没有检查更糟——它教会所有人忽略它。
//   - 它会把一次数据库往返串到**每一次写**上，因为通知就发在写链里。
//
// 记账这条判据两样都不占：它不碰介质，也只认本副本自己的写。代价是它**只抓得住
// 本进程内**的绕过——别的副本绕过登记册写这张表，这里一条事件都收不到。
// 漏报可以接受，误报不行。
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
			if workspaces.writingRecord(WorkspaceID(change.Key)) {
				return
			}
			failCheck(fmt.Sprintf(
				"工作区记录 %q 被 %s 了，但这次写不是登记册发起的——有写入路径绕过了登记册",
				change.Key, change.Operation))
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
