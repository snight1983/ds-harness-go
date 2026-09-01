// 本文件的作用：子 agent 那两个投影的测试——计时那条以描述符为原点的折叠、
// 身份那条「最后一条算数、坏的清成没有」的折叠，以及两个单元一起登记的那道面。

package subagent

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

// timedEvent 造一条带时间的事件——计时那个单元全靠 Time 说话，所以本文件不走
// 那个不填时间的 event 帮手。
func timedEvent(t *testing.T, kind session.EventType, at int64, payload any) session.Event {
	t.Helper()
	built := event(t, kind, payload)
	built.Time = at
	return built
}

// turnStartAt、turnEndAt 是这两条边界的简写。
func turnStartAt(t *testing.T, at int64, turn int) session.Event {
	t.Helper()
	return timedEvent(t, session.EventTurnStart, at, session.TurnStartData{Turn: turn})
}

func turnEndAt(t *testing.T, at int64, turn int) session.Event {
	t.Helper()
	return timedEvent(t, session.EventTurnEnd, at, session.TurnEndData{
		Turn: turn, Reason: session.CompletedTurnEnd{},
	})
}

// descriptorAt 造一条带时间的描述符事件。
func descriptorAt(t *testing.T, at int64) session.Event {
	t.Helper()
	return timedEvent(t, EventDescriptor, at, continuableInput())
}

// foldTiming 把这些事件顺次折过计时那个单元，交出上线的那个值。
func foldTiming(t *testing.T, events ...session.Event) TimingProjection {
	t.Helper()
	state := timingState{}
	for _, each := range events {
		state, _ = applyTiming(state, each)
	}
	projected, ok := viewTiming(state).(TimingProjection)
	if !ok {
		t.Fatalf("计时那个单元该投出 TimingProjection，实际 %#v", viewTiming(state))
	}
	return projected
}

// ---- 计时 ----

// 描述符**之前**那些已完成的回合不算数：一份分叉种子里可能带着祖先的整段历史。
func TestTimingIgnoresTurnsSettledBeforeTheDescriptor(t *testing.T) {
	projected := foldTiming(t,
		turnStartAt(t, 10, 1),
		turnEndAt(t, 20, 1),
		descriptorAt(t, 30),
	)
	if projected.SettledMs != 0 || projected.Active != nil {
		t.Fatalf("描述符之前那一回合不该算进来，实际 %#v", projected)
	}
}

// 描述符落下来时还开着的那个回合被认领：它的起点就是计时原点。
func TestTimingAdoptsTheTurnThatIsStillOpenWhenTheDescriptorLands(t *testing.T) {
	projected := foldTiming(t,
		turnStartAt(t, 10, 1),
		descriptorAt(t, 30),
	)
	if projected.Active == nil || projected.Active.Since != 10 || projected.Active.Through != 30 {
		t.Fatalf("该认领那个开着的回合，实际 %#v", projected.Active)
	}

	settled := foldTiming(t,
		turnStartAt(t, 10, 1),
		descriptorAt(t, 30),
		turnEndAt(t, 50, 1),
	)
	// 算的是整个回合（50-10），不是描述符之后那一截。
	if settled.SettledMs != 40 || settled.Active != nil {
		t.Fatalf("该把整个回合结清成 40ms，实际 %#v", settled)
	}
}

func TestTimingAccumulatesEverySettledTurnAfterTheDescriptor(t *testing.T) {
	projected := foldTiming(t,
		descriptorAt(t, 10),
		turnStartAt(t, 20, 1),
		turnEndAt(t, 30, 1),
		turnStartAt(t, 40, 2),
		turnEndAt(t, 100, 2),
	)
	if projected.SettledMs != 70 || projected.Active != nil {
		t.Fatalf("两个回合该累成 70ms，实际 %#v", projected)
	}
}

// 回合开着的时候，每一条事件都把那一刀切面的另一端往前推。
func TestTimingAdvancesTheOpenIntervalWithEveryEvent(t *testing.T) {
	projected := foldTiming(t,
		descriptorAt(t, 10),
		turnStartAt(t, 20, 1),
		timedEvent(t, session.EventStepStart, 35, session.StepStartData{Turn: 1, Step: 0}),
	)
	if projected.Active == nil || projected.Active.Since != 20 || projected.Active.Through != 35 {
		t.Fatalf("开着的那一刀该推到 35，实际 %#v", projected.Active)
	}
}

// 每一条描述符都把累计清零：健康的目录只认自己那段后缀里那一条，于是最后一次
// 清零就是这个孩子权威的原点。
func TestTimingResetsAtEveryDescriptor(t *testing.T) {
	projected := foldTiming(t,
		descriptorAt(t, 10),
		turnStartAt(t, 20, 1),
		turnEndAt(t, 30, 1),
		descriptorAt(t, 40),
	)
	if projected.SettledMs != 0 || projected.Active != nil {
		t.Fatalf("后一条描述符该把前面累的清掉，实际 %#v", projected)
	}
}

// 清零也认领当下那个开着的回合，而不是把它一起丢掉。
func TestTimingCarriesTheOpenIntervalAcrossAReset(t *testing.T) {
	projected := foldTiming(t,
		descriptorAt(t, 10),
		turnStartAt(t, 20, 1),
		descriptorAt(t, 40),
	)
	if projected.Active == nil || projected.Active.Since != 20 || projected.Active.Through != 40 {
		t.Fatalf("清零该保住那个开着的回合，实际 %#v", projected.Active)
	}
}

// 一条走回头的时间不许把累计减下去。
func TestTimingNeverSubtractsFromTheSettledTotal(t *testing.T) {
	projected := foldTiming(t,
		descriptorAt(t, 10),
		turnStartAt(t, 20, 1),
		turnEndAt(t, 15, 1),
	)
	if projected.SettledMs != 0 || projected.Active != nil {
		t.Fatalf("倒着走的时间该被当成 0，实际 %#v", projected)
	}
}

// 一条什么都不动的事件必须报「没变」，否则框架会为它白算一次视图、白通知一圈。
func TestTimingReportsNoChangeWhenNothingIsOpen(t *testing.T) {
	quiet := []session.Event{
		timedEvent(t, session.EventStepStart, 10, session.StepStartData{Turn: 1, Step: 0}),
		turnEndAt(t, 20, 1),
	}
	for _, each := range quiet {
		if _, changed := applyTiming(timingState{}, each); changed {
			t.Fatalf("没有开着的回合时 %q 不该报变了", each.Type)
		}
	}
	// 描述符之后、回合又没开着的那条 turn/end 同样什么都不动。
	if _, changed := applyTiming(timingState{DescriptorSeen: true}, turnEndAt(t, 20, 1)); changed {
		t.Fatal("没有开着的回合时 turn/end 不该报变了")
	}
}

// ---- 身份 ----

// foldIdentity 把这些事件顺次折过身份那个单元，交出上线的那个值（nil 表示没有身份）。
func foldIdentity(t *testing.T, events ...session.Event) any {
	t.Helper()
	state := identityState{}
	for _, each := range events {
		state, _ = applyIdentity(state, each)
	}
	return viewIdentity(state)
}

func TestIdentityIgnoresEventsThatAreNotDescriptors(t *testing.T) {
	if _, changed := applyIdentity(identityState{}, turnStartAt(t, 10, 1)); changed {
		t.Fatal("不是描述符的事件不该报变了")
	}
	if folded := foldIdentity(t, turnStartAt(t, 10, 1)); folded != nil {
		t.Fatalf("没有描述符就该是没有身份，实际 %#v", folded)
	}
}

// 最后一条算数：一份分叉种子里回放的祖先描述符必须被孩子自己那条盖掉。
func TestIdentityKeepsTheLastDescriptor(t *testing.T) {
	ancestor := continuableInput()
	ancestor.Label = "祖先"
	mine := continuableInput()
	mine.Label = "我自己"

	folded := foldIdentity(t,
		event(t, EventDescriptor, ancestor),
		session.Event{Seq: 7, Type: EventDescriptor, Data: data(t, mine)},
	)
	identity, ok := folded.(IdentityProjection)
	if !ok {
		t.Fatalf("该投出一份身份，实际 %#v", folded)
	}
	if identity.Label != "我自己" || identity.Mode != ModeContinuable {
		t.Fatalf("该是最后那条描述符，实际 %#v", identity)
	}
	// seq 记的是那条记录本身的位置——「它在不在种子里」全靠这个数判。
	if identity.Seq != 7 {
		t.Fatalf("seq 该是那条记录的 seq 7，实际 %d", identity.Seq)
	}
}

// 一次性那支的 label 可以是空的，身份照样立得起来。
func TestIdentityAcceptsAOneShotWithoutALabel(t *testing.T) {
	folded := foldIdentity(t, event(t, EventDescriptor, DescriptorData{
		Version: DescriptorVersion, Mode: ModeOneShot, Provider: "spawn",
	}))
	identity, ok := folded.(IdentityProjection)
	if !ok {
		t.Fatalf("该投出一份身份，实际 %#v", folded)
	}
	if identity.Mode != ModeOneShot || identity.Label != "" {
		t.Fatalf("该是一份没有 label 的一次性身份，实际 %#v", identity)
	}
}

// 最后那条立不起来时清成**没有身份**，而且这次清零要报「变了」——否则一个攥着
// 更早那份身份的消费方会把它留成陈的。
func TestIdentityClearsWhenTheLastDescriptorDoesNotStand(t *testing.T) {
	incomplete := map[string]any{"version": DescriptorVersion, "mode": ModeContinuable, "provider": "spawn"}
	for name, broken := range map[string]session.Event{
		"读不出来":  {Type: EventDescriptor, Data: json.RawMessage(`{"version":`)},
		"版本不认识": event(t, EventDescriptor, map[string]any{"version": DescriptorVersion + 1, "mode": "future"}),
		"声明不完整": event(t, EventDescriptor, incomplete),
	} {
		t.Run(name, func(t *testing.T) {
			state, _ := applyIdentity(identityState{}, event(t, EventDescriptor, continuableInput()))
			if state.Identity == nil {
				t.Fatal("先该有一份身份")
			}
			next, changed := applyIdentity(state, broken)
			if !changed {
				t.Fatal("清零该报变了")
			}
			if next.Identity != nil {
				t.Fatalf("该清成没有身份，实际 %#v", next.Identity)
			}
		})
	}
}

// ---- 两个单元一起登记 ----

func TestRegisterProjectionsNeedsARegistry(t *testing.T) {
	if _, err := RegisterProjections(nil); err == nil {
		t.Fatal("没有注册表该登不上")
	}
}

// occupy 在一张注册表上先把某个投影键占住，好让紧接着那次 [RegisterProjections]
// 在这个键上撞名。这份定义什么都不折、也不上线视图——它只为了占住这个名字。
func occupy(t *testing.T, registry *projection.Registry, key string) {
	t.Helper()
	if _, err := projection.Register(registry, projection.Definition[struct{}]{
		Key:          key,
		StateVersion: 1,
		Init:         func() struct{} { return struct{}{} },
		Apply:        func(state struct{}, _ session.Event) (struct{}, bool) { return state, false },
		DecodeState:  projection.StrictDecoder[struct{}](),
	}); err != nil {
		t.Fatalf("占住 %q 失败：%v", key, err)
	}
}

// 两个单元里哪一个撞了名，这次登记就整个不成——而且**先上线的那个要跟着卷回去**，
// 否则注册表上会留下一个只有计时没有身份的半拉界面。
func TestRegisterProjectionsUnwindsWhenEitherKeyIsTaken(t *testing.T) {
	for name, taken := range map[string]string{
		"计时那个键被占了": TimingProjectionKey,
		"身份那个键被占了": IdentityProjectionKey,
	} {
		t.Run(name, func(t *testing.T) {
			registry := projection.NewRegistry()
			occupy(t, registry, taken)

			if _, err := RegisterProjections(registry); err == nil {
				t.Fatal("撞名该登不上")
			}
			// 占位那份定义只给宿主看，所以这两个键一个值都不该上线：先占的那个
			// 本来就不投视图，另一个要么没登上、要么已经卷回去了。
			live := newFreeSession(t, "child", "parent", []session.Event{descriptorAt(t, 30)})
			values := registry.Snapshot(liveView{live: live}).Values
			for _, key := range []string{TimingProjectionKey, IdentityProjectionKey} {
				if _, published := values[key]; published {
					t.Fatalf("卷回去之后 %q 不该有值", key)
				}
			}
		})
	}
}

// 两个单元一起上线、一起下线：只有身份没有计时的界面读起来是坏的。
func TestRegisterProjectionsPublishesAndWithdrawsBothUnits(t *testing.T) {
	registry := projection.NewRegistry()
	dispose, err := RegisterProjections(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}

	live := newFreeSession(t, "child", "parent", []session.Event{
		turnStartAt(t, 10, 1),
		descriptorAt(t, 30),
		turnEndAt(t, 50, 1),
	})
	values := registry.Snapshot(liveView{live: live}).Values

	timing, ok := values[TimingProjectionKey].(TimingProjection)
	if !ok {
		t.Fatalf("计时那个键该在，实际 %#v", values)
	}
	if timing.SettledMs != 40 || timing.Active != nil {
		t.Fatalf("那一回合该结清成 40ms，实际 %#v", timing)
	}
	identity, ok := values[IdentityProjectionKey].(IdentityProjection)
	if !ok {
		t.Fatalf("身份那个键该在，实际 %#v", values)
	}
	if identity.Label != "查一下" || identity.Seq != 1 {
		t.Fatalf("该折出种子里那条描述符，实际 %#v", identity)
	}

	dispose()
	withdrawn := registry.Snapshot(liveView{live: live}).Values
	if _, still := withdrawn[TimingProjectionKey]; still {
		t.Fatal("注销之后计时那个键不该还在")
	}
	if _, still := withdrawn[IdentityProjectionKey]; still {
		t.Fatal("注销之后身份那个键不该还在")
	}
}
