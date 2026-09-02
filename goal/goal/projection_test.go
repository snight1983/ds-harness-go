// 本文件的作用：把那条**宽松**的投影转移和它的落盘状态钉住——它比严格回放松在
// 哪里、松到哪儿为止，以及为什么它读一份旧检查点时反而比谁都严。
//
// # 这些测试防的是什么错
//
//   - **一个读不动的历史事件让整份投影再也建不起来**。这份状态是拿来做检查点的：
//     一条坏改动该只让它少一格，不该把重建整个堵死。
//   - **一次没动状态的事件被当成动了**。第二个返回值是注册表据以跳过重排的依据；
//     恒真的话，每一条不相干的事件都会白白重排一次投影。
//   - **一份形状对不上的旧检查点被宽容地读成「字段都在、值全是零」**。那种状态会
//     被继续往前折成垃圾，而且一路上不报任何错。
//   - **状态版本号被改回 1**。落盘的检查点行是按 (键, 版本) 认的；这个键在 DSH
//     侧已经改过三次语义，改回 1 会让一批本该被丢掉的旧行重新看起来还能用。

package goal

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

func TestApplyProjectionTakesTheLastWrite(t *testing.T) {
	var state *Projection
	state, changed := ApplyProjection(state, createEvent())
	if !changed || state == nil {
		t.Fatalf("一条建目标的改动本该动这份状态，得到的是 (%+v, %v)", state, changed)
	}
	if state.Goal.Revision != 1 || state.RoundsStarted != 0 || state.CreatedAt != 10 {
		t.Fatalf("投影出来的是 %+v", state)
	}

	state, changed = ApplyProjection(state, changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)))
	if !changed || state == nil || state.Goal.Phase != PhasePaused || state.UpdatedAt != 20 {
		t.Fatalf("停下之后的投影是 %+v（changed=%v）", state, changed)
	}

	state, changed = ApplyProjection(state, changeEvent(clearJSON(3, 30)))
	if !changed || state != nil {
		t.Fatalf("清掉之后本该是一份空状态，得到的是 %+v（changed=%v）", state, changed)
	}
}

// TestApplyProjectionSkipsWhatItCannotUse 是这条转移和 [ApplyEvent] 分道扬镳的地方：
// 同样一条读不动的改动，严格回放当场炸，这里原样返回。
func TestApplyProjectionSkipsWhatItCannotUse(t *testing.T) {
	seeded, _ := ApplyProjection(nil, createEvent())
	cases := map[string]session.Event{
		"不是本包的事件":  {Type: session.EventTurnStart, Data: json.RawMessage(`{}`)},
		"读不动的改动":   changeEvent(`{"kind":"goal/change","version":9}`),
		"负载根本不是对象": changeEvent(`[]`),
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			next, changed := ApplyProjection(seeded, event)
			if changed {
				t.Fatalf("%s 本该被当成「没动过」", name)
			}
			if next != seeded {
				t.Fatalf("%s 本该把原来那份状态原样交回来", name)
			}
		})
	}
}

// TestApplyProjectionDoesNotValidateTransitions 钉的是这条路**松到哪儿**：一条
// 接不上的跃迁在这里照单全收。
//
// 这不是漏了一道检查，是分工：这条路一次跃迁都不验，它信的是写的那一侧——
// [Service] 每次改动之前先严格折过一遍，[ValidateStream] 在装配处还会再拒一次。
// 同一条改动交给严格回放会当场炸，下半段就是那份对照。
func TestApplyProjectionDoesNotValidateTransitions(t *testing.T) {
	disconnected := changeEvent(changeJSON("pause", goalJSON(9, "paused"), 0, 10, 20))

	seeded, _ := ApplyProjection(nil, createEvent())
	next, changed := ApplyProjection(seeded, disconnected)
	if !changed || next == nil {
		t.Fatalf("这条路本该照单全收，得到的是 (%+v, %v)", next, changed)
	}
	if next.Goal.Revision != 9 || next.Goal.Phase != PhasePaused {
		t.Fatalf("折出来的是 %+v，本该是最后写的那一份", next.Goal)
	}

	expectFoldError(
		t,
		ApplyEvent(EmptyFoldState(), disconnected),
		"同一条接不上的跃迁走严格回放",
	)
}

func TestRegisterProjectionNeedsARegistry(t *testing.T) {
	if _, err := RegisterProjection(nil); err == nil {
		t.Fatal("没有注册表本该造不出这个单元")
	}
}

// fakeSession 是一份只满足 [projection.SessionView] 的假会话。
//
// 这里用不着一台真会话：这一条验的是「这个单元在注册表里接得上」，而不是本包
// 排出去的字节过不过得了会话的信封校验——后者是 service_test.go 的活儿。
type fakeSession struct {
	events []session.Event
}

// newFakeSession 造一份假会话，seq 按下标排。
func newFakeSession(events ...session.Event) *fakeSession {
	for index := range events {
		events[index].Seq = index
	}
	return &fakeSession{events: events}
}

func (s *fakeSession) ID() session.SessionID   { return "session-1" }
func (s *fakeSession) Events() []session.Event { return s.events }
func (s *fakeSession) NextSeq() int            { return len(s.events) }

func TestRegisterProjectionPlugsIntoTheRegistry(t *testing.T) {
	registry := projection.NewRegistry()
	stop, err := RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}

	view := newFakeSession(createEvent())
	value, present := registry.StateOf(view, ProjectionKey)
	if !present {
		t.Fatalf("登记完之后 %q 本该在读切里", ProjectionKey)
	}
	state, ok := value.(*Projection)
	if !ok || state == nil || state.Goal.Revision != 1 {
		t.Fatalf("读切里的值是 %#v", value)
	}

	checkpoint, err := registry.Checkpoint(view)
	if err != nil {
		t.Fatalf("排检查点失败：%v", err)
	}
	row, present := checkpoint[ProjectionKey]
	if !present || row.Ver != projectionStateVersion {
		t.Fatalf("检查点行是 %+v，本该带着版本号 %d", row, projectionStateVersion)
	}

	// 一份自己刚排出去的行读得回来。
	values := registry.ViewCheckpoint(checkpoint)
	restored, ok := values[ProjectionKey].(*Projection)
	if !ok || restored == nil || restored.Goal.ID != "goal-1" {
		t.Fatalf("从检查点折出来的是 %#v", values[ProjectionKey])
	}

	// 严格解码：版本对得上、却多带了一个字段的行，是**这个构建自己写坏了**，
	// 所以冷读会原样上抛而不是悄悄重折。一份被宽容地读成「值全是零」的状态
	// 会被继续往前折成垃圾，而且一路上不报任何错。
	broken := projection.Checkpoint{ProjectionKey: projection.CheckpointRow{
		Ver: projectionStateVersion,
		Seq: 0,
		Val: json.RawMessage(
			`{"goal":{"id":"goal-1","revision":1,"objective":"写完","phase":"active",` +
				`"maxGoalRounds":3},"roundsStarted":0,"createdAt":10,"updatedAt":10,"extra":1}`,
		),
	}}
	if _, err := registry.Restore(broken, view.Events(), 0, 0); err == nil {
		t.Fatal("一行多带了字段的检查点本该把这次冷读整个拒掉")
	}

	stop()
	stop() // 幂等：再调一次不该把别人的键删掉。
	if _, present := registry.StateOf(view, ProjectionKey); present {
		t.Fatalf("注销之后 %q 本该读成「这个能力不在」", ProjectionKey)
	}
}
