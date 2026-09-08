// 本文件的作用：这个包自己拥有的那条关系不变量——一次尝试结算之后，
// 它那个凭据键上的槽位必须已经放开。
//
// 源: packages/credentials/authorization/src/invariant.ts

package authorization

import (
	"context"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/credentials/authorization/src/invariant.ts:8
const PackageName = "@deepseek-ai/dsh-credentials-authorization"

// ValidateRelease 验一次结算：结算完了，这个键上就不许再有人攥着。
//
// 源: packages/credentials/authorization/src/invariant.ts:18-40
//
// 这是**唯一**值得一条不变量的那件事：一个卡住的槽位从外面看不出来，而它会让之后
// 每一次 [Service.Begin] 都撞 [CodeAlreadyInFlight]，直到重启。
//
// 新增: DSH 只查 inFlight。本包多查一条 [Entry.Attempt]——一次结算完的尝试如果还
// 留着自己的身份，说明 [Service.release] 那一步没删掉记录，而那会让下一次
// [Service.Begin] 撞 [CodeStalled]。两者的病因不同，但都是「结算没收干净」。
//
// 新增: DSH 那边这个函数收一个 fail 回调、违例时直接调它。Go 这边返回第一条违例，
// 理由和 [github.com/snight1983/ds-harness-go/feature/webhook.ValidateAdmission]
// 逐字相同：它因此可以脱离不变量注册表单独用。
func ValidateRelease(settled Settled, entry Entry, found bool) error {
	if !found {
		// 这条流程的登记在结算之后被撤了。槽位跟着记录走，记录已经不该在了，
		// 但这里读不到它——没有可判的事实，就不判。
		return nil
	}
	if entry.InFlight {
		return fmt.Errorf("authorization slot for %s still in flight after settlement %s",
			strconv.Quote(string(settled.Key)), strconv.Quote(string(settled.Settlement)))
	}
	if entry.Attempt == settled.Attempt {
		return fmt.Errorf("authorization attempt %s for %s survived its own settlement",
			strconv.Quote(string(settled.Attempt)), strconv.Quote(string(settled.Key)))
	}
	return nil
}

// RegisterInvariants 装上那条放开检查，返回注销函数。
//
// 源: packages/credentials/authorization/src/invariant.ts:12-44
//
// 它订的是 [Service.OnSettled]，也只能订它：槽位放开发生在结算**之后**，
// 而结算这条旁观路是唯一一处「这次尝试已经彻底完了」说得准的地方。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	service *Service,
) (func(), error) {
	switch {
	case registry == nil:
		return nil, newError(CodeInvalidConfig, "注册不变量需要一个不变量注册表")
	case service == nil:
		return nil, newError(CodeInvalidConfig, "注册不变量需要一个授权服务")
	}

	install := func(installCtx context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		// 装载那个 ctx 只活到装完为止，而这条检查是**之后**每次结算都要跑一遍的。
		// 拿一个已经结束的 ctx 去读介质，读到的永远是取消。
		readCtx := context.WithoutCancel(installCtx)
		scope.Defer(service.OnSettled(func(settled Settled) {
			entry, found, err := service.Describe(readCtx, settled.Key)
			if err != nil {
				fail(fmt.Sprintf("authorization: 结算之后读不到凭据 %q 的槽位：%v", settled.Key, err))
				return
			}
			if err := ValidateRelease(settled, entry, found); err != nil {
				fail(err.Error())
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
