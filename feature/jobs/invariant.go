// 本文件的作用：这个包自己拥有的那条持久不变量——一份作业快照里那些跨字段关系
// 要成立到什么程度，才算注册表没有骗人。
//
// 源: packages/jobs/jobs/src/invariant.ts

package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/scope"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/jobs/jobs/src/invariant.ts:8
const PackageName = "@deepseek-ai/dsh-jobs"

// ValidateSnapshot 验一份作业快照里那些跨字段关系；owner 是这份快照被交出来时
// 那个属主，无主作业交 nil。
//
// 源: packages/jobs/jobs/src/invariant.ts:17-43
//
// 查的都是**注册表自己**该守住的东西：id 是它发的，时刻是它盖的，属主会话是它围的。
// 生产方那些事实（[Snapshot.Detail]、输出内容）不在这里查——那些是生产方的账。
//
// 新增: DSH 那边这个函数收一个 `fail` 回调，每撞见一条就调一次（而 `fail` 会抛，
// 所以实际上也是第一条为准）。Go 这边返回**第一条**违例，和
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.ValidateEvent] 一致：它因此可以脱离不变量注册表
// 单独用（比如一台实现在自己的测试里自查），而 [RegisterInvariants] 只是把这个
// 错误接到 [invariants.Fail] 上。
func ValidateSnapshot(snapshot Snapshot, owner agent.Agent) error {
	id := string(snapshot.ID)
	prefix := string(snapshot.Kind) + "-"
	if err := validateID(id, snapshot.Kind, prefix); err != nil {
		return err
	}
	if snapshot.Label == "" {
		return fmt.Errorf("job %q label must be non-empty", id)
	}
	// 新增: DSH 查的是「startedAt 是一个非负安全整数」——它那边这个字段是 epoch
	// 毫秒数，一个 NaN 或者负数就是没盖过表。Go 里它是 [time.Time]，同一件事就是
	// 「不是零值」：注册表登记一件作业时必然盖过一次表。
	if snapshot.StartedAt.IsZero() {
		return fmt.Errorf("job %q startedAt must be set", id)
	}

	// 新增: DSH 没有这一条——它那台注册表只可能跑在一个进程里，「在谁那儿」不是
	// 一个问题。本仓库里它是（见 [RunnerID]）：一条记录没有 runner，任何一个副本
	// 都判断不了自己能不能 [Registry.Read] 或者 [Registry.Kill] 它，只能猜。
	if snapshot.Runner == "" {
		return fmt.Errorf("job %q runner must be set", id)
	}

	finished := !snapshot.FinishedAt.IsZero()
	if snapshot.Status.IsTerminal() != finished {
		return fmt.Errorf("job %q finishedAt must be present exactly for a terminal status", id)
	}
	if finished && snapshot.FinishedAt.Before(snapshot.StartedAt) {
		return fmt.Errorf("job %q finishedAt must be no earlier than startedAt", id)
	}

	// 属主为 nil 时期望的就是空串：无主作业的 [Snapshot.OwnerSession] 不填。
	var expected = ""
	if owner != nil {
		expected = string(owner.ID())
	}
	if string(snapshot.OwnerSession) != expected {
		return fmt.Errorf("job %q ownerSession does not match its completion owner", id)
	}
	return nil
}

// validateID 查那个 id 是不是「<种类>-<序号>」，且种类非空、序号是一个正整数。
//
// 源: packages/jobs/jobs/src/invariant.ts:19-24
//
// 新增: DSH 拿 `Number(id.slice(prefix.length))` 加 `Number.isSafeInteger` 判序号。
// Go 这边是 [strconv.Atoi]：它天生就只认整数（"1.5"、"" 都过不去），而 int 的
// 范围本身就是 Go 这边的「安全整数」，所以那道 isSafeInteger 没有对应物。
func validateID(id string, kind JobKind, prefix string) error {
	if kind == "" || !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("job snapshot id %q must be %q followed by a positive ordinal", id, prefix)
	}
	ordinal, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil || ordinal < 1 {
		return fmt.Errorf("job snapshot id %q must be %q followed by a positive ordinal", id, prefix)
	}
	return nil
}

// RegisterInvariants 装上作业快照那条形状检查，返回注销函数。
//
// 源: packages/jobs/jobs/src/invariant.ts:46-57
//
// 两条胳膊，和 DSH 一样：装的时候把**当下那些无主作业**走一遍（一台带着坏记录
// 起来的注册表，必须在装载这一刻就响），然后订阅后来每一次结算。装到一半失败的话
// 由 [invariants.Registry.Register] 自己按 scope 拆回去。
//
// owner 圈定这条检查看得见谁的结算，语义同 [Registry.OnJobDone]：交一个
// [scope.NewRoot] 造的无身份作用域就罩得住每一个属主——这正是 DSH 把这个伴生
// 插件装在根上时的样子。
//
// 新增: DSH 的 install 靠 `inject: ['jobs']` 从 cordis 上取那台注册表，订阅交回来
// 的退订函数也由 cordis 自动收。Go 这边注册表由装配方交进来，退订函数登记进这次
// 注册的 [invariants.Scope]：注销之后，一条不该再查的检查绝不许继续在别人的
// 结算路径上抛。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	jobs Registry,
	owner *scope.Scope,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("jobs: 注册不变量需要一个不变量注册表")
	}
	if jobs == nil {
		return nil, fmt.Errorf("jobs: 注册不变量需要一台作业注册表")
	}
	if owner == nil {
		return nil, fmt.Errorf("jobs: 注册不变量需要一个作用域（要罩住所有属主就交 scope.NewRoot()）")
	}

	install := func(installCtx context.Context, installScope *invariants.Scope, fail invariants.Fail) error {
		// 新增: 这次列举现在会失败（理由见 [Registry]）。列不出来就**装不上**这条
		// 检查：一条装到一半、只订阅了后续结算而没走过当下那批记录的检查，会让
		// 「带着坏记录起来」这件事悄悄溜过去，而那正是这条胳膊存在的理由。
		snapshots, err := jobs.List(installCtx, nil)
		if err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if err := ValidateSnapshot(snapshot, nil); err != nil {
				fail(err.Error())
			}
		}
		dispose, err := jobs.OnJobDone(installCtx, owner, func(snapshot Snapshot, jobOwner agent.Agent) {
			if err := ValidateSnapshot(snapshot, jobOwner); err != nil {
				fail(err.Error())
			}
		})
		if err != nil {
			return err
		}
		// 摘的时候不带装载时那个 ctx 的取消：它已经废了也得把监听器收回来。
		installScope.Defer(func() { _ = dispose(context.WithoutCancel(installCtx)) })
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
