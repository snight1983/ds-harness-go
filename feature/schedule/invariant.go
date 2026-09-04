// 本文件的作用：这个包自己拥有的那条持久不变量——为什么它验的是**整条流**而不是
// 单独一条事件，以及那两条胳膊在 Go 里为什么合成了一条。
//
// 源: packages/schedule/schedule/src/invariant.ts

package schedule

import (
	"context"
	"errors"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Stream 是一段要整条验的会话日志。
//
// 源: packages/schedule/schedule/src/invariant.ts:19 (events, seedLength)
//
// 新增: 本仓库别处的不变量都是一条事件一验（见 [github.com/snight1983/ds-harness-go/feature/todo.ValidateEvent]），
// 这一条不行：schedule 的规则全是**跨事件**的——一个 id 不许被建两次、delete 和
// dispatch 只许指向此刻活着的记录、固定频率的 dispatch 必须带一个不早于当前
// scheduledAt 的 acceptedAt。这几条脱离前面那一串就无从判断，所以这里传的是整条流。
type Stream struct {
	// Events 是这条日志此刻的全部事件，按写下的先后。
	Events []sessionlog.Event
	// Header 是这个会话的头，种子边界从它算出来。
	//
	// 新增: DSH 传的是一个 seedLength 数字。本仓库的日志会被弹头，条数换不回下标，
	// 理由见 [FoldEvents]。
	Header sessionlog.SessionHeader
}

// ValidateStream 验一整条流；守住了就交回 nil。
//
// 源: packages/schedule/schedule/src/invariant.ts:19-27
//
// 新增: DSH 那个 validate 收一个 fail 回调、就地报掉。这里交回错误，和
// [github.com/snight1983/ds-harness-go/feature/todo.ValidateEvent] 的理由一样：它因此可以脱离不变量注册表单独用
// ——离线校验一份日志，或者在写之前自己先验一遍——而 [RegisterInvariants] 只是把
// 这个错接到 [invariants.Fail] 上。
//
// 交回的一定是 [LogError]：[FoldEvents] 把每一种拒收都归一成了它。所以 DSH 那句
// 「不是 ScheduleLogError 就原样抛出去」在这里没有对应物，它守的是 TS 那边 catch
// 收得住任何东西。
func ValidateStream(stream Stream) error {
	_, err := FoldEvents(stream.Events, stream.Header)
	return err
}

// RegisterInvariants 装上这条整流不变量，返回注销函数。
//
// 源: packages/schedule/schedule/src/invariant.ts:31-53
//
// 两条胳膊：装的时候把**已经装载进来的**每一条流走一遍（一份历史里就带着坏改动的
// 会话，必须在装载这一刻就响，而不是等下一次追加），然后订阅后续。
//
// 新增: DSH 那边订阅是**两个**监听器——session/created 验一条刚开出来的流，
// internal/dispatch 在 schedule/change 落盘前验 `[...events, event]`。Go 这边合成
// 一条：两者交给验的都是一条完整的 [Stream]，差别只在这条流是怎么凑出来的，而那是
// 装配方的事。写在这里只会让本包多认一遍它管不着的事件总线形状。
//
// 装配方因此要负责两件事：一条新开出来的会话按原样交进来；一次 schedule/change
// 落盘之前，把待写的那条事件**接在末尾**再交进来。少了后一件，一条破规矩的改动会
// 先落进日志，然后在下一次装载时才炸——那时候它已经改不掉了。
//
// subscribe 交回来的退订函数会登记进这次注册的 scope：注销之后，一条不该再查的
// 检查绝不许继续在别人的写路径上抛。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	loaded func() []Stream,
	subscribe func(observer func(Stream)) func(),
) (func(), error) {
	switch {
	case registry == nil:
		return nil, errors.New("schedule: 注册不变量需要一个不变量注册表")
	case loaded == nil:
		return nil, errors.New("schedule: 注册不变量需要一条读出已装载日志的路")
	case subscribe == nil:
		return nil, errors.New("schedule: 注册不变量需要一条订阅后续流的路")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		check := func(stream Stream) {
			if err := ValidateStream(stream); err != nil {
				fail(err.Error())
			}
		}
		for _, stream := range loaded() {
			check(stream)
		}
		scope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
