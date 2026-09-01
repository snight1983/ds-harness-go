// 本文件的作用：这个包自己拥有的那条持久不变量——为什么它验的是**整条流**而不是
// 单独一条事件，以及 DSH 那三条胳膊在 Go 里为什么合成了一条。
//
// 源: packages/goal/goal/src/invariant.ts

package goal

import (
	"context"
	"errors"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/session"
)

// ValidateStream 验一整条会话日志；守住了就交回 nil。
//
// 源: packages/goal/goal/src/invariant.ts:29-37 (applyChecked)
//
// 新增: DSH 那个 applyChecked 收一个 fail 回调、就地报掉。这里交回错误，和
// [github.com/snight1983/ds-harness-go/schedule.ValidateStream] 的理由一样：它因此可以脱离不变量注册表
// 单独用——离线校验一份日志，或者在写之前自己先验一遍——而 [RegisterInvariants]
// 只是把这个错接到 [invariants.Fail] 上。
//
// 交回的一定是 [FoldError]：[Fold] 把每一种拒收都归一成了它。
func ValidateStream(events []session.Event) error {
	_, err := Fold(events)
	return err
}

// RegisterInvariants 装上这条整流不变量，返回注销函数。
//
// 源: packages/goal/goal/src/invariant.ts:40-79
//
// 两条胳膊：装的时候把**已经装载进来的**每一条流走一遍（一份历史里就带着坏改动的
// 会话，必须在装载这一刻就响，而不是等下一次追加），然后订阅后续。
//
// 新增: DSH 那边是**三个**监听器加两张 WeakMap——session/created 播一条新流，
// internal/dispatch 在事件落盘前拿一份克隆过的状态试一遍并暂存，session/event
// 在发布时把暂存那份扶正。那一整套是为了在 TS 里做增量验证：每条事件只折一次。
// Go 这边合成一条：装配方交进来的永远是一条完整的日志，本包整条重折。
//
// 换掉它是因为那套增量机器守的东西在这里都不成立——它依赖 cordis 的
// internal/dispatch 钩子（Go 里没有）、依赖对象身份做 WeakMap 键（Go 的
// [session.Event] 是值类型），而它换来的那点性能，代价是本包要认一遍它管不着的
// 事件总线形状。重折一条日志是线性的，而校验只在写路径上走一次。
//
// 装配方因此要负责两件事：一条新开出来的会话按原样交进来；一次 goal/change 或者
// 一条带目标来源的 user/message 落盘之前，把待写的那条事件**接在末尾**再交进来。
// 少了后一件，一条破规矩的改动会先落进日志，然后在下一次装载时才炸——那时候它
// 已经改不掉了。
//
// subscribe 交回来的退订函数会登记进这次注册的 scope：注销之后，一条不该再查的
// 检查绝不许继续在别人的写路径上抛。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	loaded func() [][]session.Event,
	subscribe func(observer func([]session.Event)) func(),
) (func(), error) {
	switch {
	case registry == nil:
		return nil, errors.New("goal: 注册不变量需要一个不变量注册表")
	case loaded == nil:
		return nil, errors.New("goal: 注册不变量需要一条读出已装载日志的路")
	case subscribe == nil:
		return nil, errors.New("goal: 注册不变量需要一条订阅后续流的路")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		check := func(events []session.Event) {
			if err := ValidateStream(events); err != nil {
				fail(err.Error())
			}
		}
		for _, events := range loaded() {
			check(events)
		}
		scope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
