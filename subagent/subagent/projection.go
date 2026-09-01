// 本文件的作用：子 agent 身份（模式／名字）和活跃回合时长这两个纯投影。
//
// 源: packages/subagent/subagent/src/projection.ts

package subagent

import (
	"errors"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

const (
	// TimingProjectionKey 是活跃回合时长那个单元占的投影键。
	//
	// 源: packages/subagent/subagent/src/projection.ts:60
	TimingProjectionKey = "subagentTiming"
	// IdentityProjectionKey 是身份那个单元占的投影键。
	//
	// 源: packages/subagent/subagent/src/projection.ts:162
	IdentityProjectionKey = "subagent"
)

// projectionStateVersion 是这两份状态的作废版本号。
//
// 源: packages/subagent/subagent/src/projection.ts:110, 176
//
// 两个单元在 DSH 侧都已经有过一次语义变更（身份那个是加了 seq 字段），
// 而落盘的检查点行是按 (键, 版本) 认的，所以跟着它从 2 开始，不从 1 开始。
const projectionStateVersion = 2

// timingState 是计时那个单元的折叠状态。
//
// 源: packages/subagent/subagent/src/projection.ts:15-25（TimingState）
type timingState struct {
	// SettledMs 是描述符之后那些已完成回合累起来的毫秒数。
	SettledMs int64 `json:"settledMs"`
	// Active 是折叠里成对保管的那个当下区间；nil 表示没有开着的回合。
	Active *TimingActive `json:"active,omitempty"`
	// PendingTurnStart 是描述符**之前**最近那次回合开始的时间；孩子自己那条
	// 描述符到达时它被提升成 Active。nil 表示没有。
	PendingTurnStart *int64 `json:"pendingTurnStart,omitempty"`
	// DescriptorSeen 表示这条逻辑日志上已经跨过一条描述符了。
	DescriptorSeen bool `json:"descriptorSeen"`
}

// identityState 是身份那个单元的折叠状态。
//
// 源: packages/subagent/subagent/src/projection.ts:113-116
type identityState struct {
	// Identity 是最后一条**合法**描述符折出来的身份；nil 表示还没见过合法的、
	// 或者最近那条是坏的。
	Identity *IdentityProjection `json:"identity,omitempty"`
}

// RegisterProjections 把子 agent 那两个单元登进投影注册表，返回注销它们的函数。
//
// 源: packages/subagent/subagent/src/projection.ts:157-181（subagentIdentityProjectionDefinition）, 161-180
//
// 新增: DSH 那边这是 apply 里的一个 ctx.inject(['sessionProjections'], ...) 子节点。
// Go 里没有那个容器，「在不在场」就是装配方手上有没有这个注册表，所以它是一个显式
// 的函数（成例见 [github.com/snight1983/ds-harness-go/plan/planmode.RegisterProjection]）。
//
// 两个单元一起登记：它们折的是同一条 [EventDescriptor]，分开登记只会让装配方多一次
// 忘掉其中一个的机会，而只有身份没有计时的界面读起来是坏的。
func RegisterProjections(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("subagent: 需要一个投影注册表")
	}
	unregisterTiming, err := projection.Register(registry, projection.Definition[timingState]{
		Key:          TimingProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() timingState { return timingState{} },
		Apply:        applyTiming,
		DecodeState:  projection.StrictDecoder[timingState](),
		View:         viewTiming,
	})
	if err != nil {
		return nil, err
	}
	unregisterIdentity, err := projection.Register(registry, projection.Definition[identityState]{
		Key:          IdentityProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() identityState { return identityState{} },
		Apply:        applyIdentity,
		DecodeState:  projection.StrictDecoder[identityState](),
		View:         viewIdentity,
	})
	if err != nil {
		unregisterTiming()
		return nil, err
	}
	return func() {
		unregisterIdentity()
		unregisterTiming()
	}, nil
}

// applyTiming 折的是孩子**自己**那条耐久描述符前后的回合边界。
//
// 源: packages/subagent/subagent/src/projection.ts:63-102
//
// 一份分叉种子里可能带着一条祖先的描述符和若干已完成回合。所以**每一条**描述符都
// 把累计状态清零；健康的目录只认「自己那段后缀里恰好一条描述符」的孩子，于是最后
// 那一次清零就是这个孩子权威的计时原点。
func applyTiming(state timingState, event session.Event) (timingState, bool) {
	switch event.Type {
	case session.EventTurnStart:
		if state.DescriptorSeen {
			state.Active = &TimingActive{Since: event.Time, Through: event.Time}
		} else {
			start := event.Time
			state.PendingTurnStart = &start
		}
		return state, true
	case EventDescriptor:
		var activeSince *int64
		switch {
		case state.Active != nil:
			since := state.Active.Since
			activeSince = &since
		case state.PendingTurnStart != nil:
			activeSince = state.PendingTurnStart
		}
		next := timingState{DescriptorSeen: true}
		if activeSince != nil {
			next.Active = &TimingActive{Since: *activeSince, Through: event.Time}
		}
		return next, true
	case session.EventTurnEnd:
		if !state.DescriptorSeen {
			if state.PendingTurnStart == nil {
				return state, false
			}
			state.PendingTurnStart = nil
			return state, true
		}
		if state.Active == nil {
			return state, false
		}
		if elapsed := event.Time - state.Active.Since; elapsed > 0 {
			state.SettledMs += elapsed
		}
		state.Active = nil
		return state, true
	default:
		if state.Active == nil {
			return state, false
		}
		active := *state.Active
		active.Through = event.Time
		state.Active = &active
		return state, true
	}
}

// viewTiming 把计时状态投成上线的那个值。
//
// 源: packages/subagent/subagent/src/projection.ts:103-109
func viewTiming(state timingState) any {
	return TimingProjection{SettledMs: state.SettledMs, Active: state.Active}
}

// applyIdentity 按「最后一条算数」折出那份耐久的模式／名字身份。
//
// 源: packages/subagent/subagent/src/projection.ts:163-172
//
// 一份分叉种子可能回放一条祖先的描述符，而孩子自己那条必须盖掉它——和
// [applyTiming] 是同一套清零纪律。一份坏掉的、或者版本不认识的负载**清成没有身份**
// 而不是报错，于是一次从健康祖先分出来的分叉绝不会继承一份它自己那条描述符没能立
// 起来的身份；这次清零也会走完每一个 JSON 推送帧，所以一个攥着更早那份身份的消费方
// 会把它换掉，而不是把它留成陈的。
//
// 「没有合法描述符」这一件事，成因（缺、坏、版本不认识）**有意**不加区分。
func applyIdentity(state identityState, event session.Event) (identityState, bool) {
	if event.Type != EventDescriptor {
		return state, false
	}
	// 一次折叠绝不许报错，所以坏掉的负载在这里折成「没有身份」。
	descriptor, found, err := FoldDescriptor([]session.Event{event})
	if err != nil || !found {
		return identityState{}, true
	}
	return identityState{Identity: &IdentityProjection{
		Mode:  descriptor.Mode,
		Label: descriptor.Label,
		Seq:   event.Seq,
	}}, true
}

// viewIdentity 把身份状态投成上线的那个值。
//
// 源: packages/subagent/subagent/src/projection.ts:178
//
// 新增: DSH 那个键的类型写成 `SubagentIdentityProjection | null`，并专门说明这个
// null 哨兵**有意**是可序列化的——一个 undefined 字段会被 JSON.stringify 丢掉，
// 于是收端那份陈旧的身份会活下来。Go 的 nil 指针排出去就是显式的 null，
// 那个隐患本来就不存在，所以这里不需要那句说明对应的任何代码。
func viewIdentity(state identityState) any {
	if state.Identity == nil {
		return nil
	}
	return *state.Identity
}
