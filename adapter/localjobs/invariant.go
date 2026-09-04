// 本文件的作用：这个包在不变量注册表里占的那个位置——一条检查都没有的位置。
//
// 源: packages/jobs/jobs-local/src/invariant.ts

package localjobs

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/jobs/jobs-local/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-jobs-local"

// RegisterInvariants 把这个包登记进不变量注册表，返回注销函数。
//
// 源: packages/jobs/jobs-local/src/invariant.ts:26-32（apply）
//
// 装进去的检查是**空的**，这是刻意的，不是没写完，DSH 那边把理由写全了：
//
//   - 每份快照的身份、状态、时刻、属主，归 [github.com/snight1983/ds-harness-go/feature/jobs.ValidateSnapshot]
//     所有，这里再查一遍是重复。
//   - 这台实现自己那条独有的规矩是**准入**——「没有控制器服务的属主一律不许开工」。
//     它要在生产方跑起来**之前**就拒掉，而它依据的是这台注册表的私有装配
//     （并发上限、已挂的控制器）。等到快照公布之后再去核一个聚合量，既要把那份
//     私有装配单单为这条检查外露出去，又根本验不到「开工前就失败」这条保证本身。
//     [Registry.Start] 是当场同步守住它的，那才是它该待的地方。
//
// 那为什么还要登记？理由和 [github.com/snight1983/ds-harness-go/feature/subagent/controltool.RegisterInvariants]
// 逐字相同：占住这个包名，并且让「检查过了、结论是无需检查」和「这个包被漏掉了」
// 区分得开。
func RegisterInvariants(ctx context.Context, registry *invariants.Registry) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("localjobs: 注册不变量需要一个不变量注册表")
	}
	return registry.Register(ctx, PackageName, func(context.Context, *invariants.Scope, invariants.Fail) error {
		return nil
	})
}
