// 本文件的作用：登记表怎么收单元、怎么把事件推过去、以及三种读面各自看到什么。
//
// 源: packages/session/session-projection/src/index.ts:163-495

package projection

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

func TestRegisterRefusesADefinitionThatCannotWork(t *testing.T) {
	t.Parallel()

	// DSH 只在运行时拦了版本号那一项，别的几项靠 TypeScript 的类型。Go 的
	// 结构体字面量允许留空，一个 nil 的函数字段要到第一次推进时才 panic，
	// 那时离登记点已经很远了——所以每一项都在这道边界上拦。
	cases := map[string]func(Definition[countState]) Definition[countState]{
		"空键": func(d Definition[countState]) Definition[countState] {
			d.Key = ""
			return d
		},
		"负版本号": func(d Definition[countState]) Definition[countState] {
			d.StateVersion = -1
			return d
		},
		"没给 Init": func(d Definition[countState]) Definition[countState] {
			d.Init = nil
			return d
		},
		"没给 Apply": func(d Definition[countState]) Definition[countState] {
			d.Apply = nil
			return d
		},
		"没给 DecodeState": func(d Definition[countState]) Definition[countState] {
			d.DecodeState = nil
			return d
		},
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			dispose, err := Register(registry, break_(countUnit("count", 0)))
			if !errors.Is(err, ErrInvalidDefinition) {
				t.Fatalf("该拒掉这份定义：%v", err)
			}
			if dispose != nil {
				t.Fatalf("拒掉了就不该给出注销函数")
			}
			if len(registry.registrations) != 0 {
				t.Fatalf("拒掉了就不该留下登记")
			}
		})
	}
}

func TestRegisterCountsRegistrantsSharingOneKey(t *testing.T) {
	t.Parallel()

	// 一份单元定义服务所有会话，而登记方是逐会话的：同一个包挂在两个会话上
	// 就登记两次。不数一下的话，第一个走的那个会把这个投影从另一个会话上
	// 一起抹掉。
	registry := NewRegistry()
	first := mustRegister(t, registry, countUnit("count", 0))
	second := mustRegister(t, registry, countUnit("count", 0))

	view := newSession("s1", userEvent(0))

	first()
	if _, ok := registry.StateOf(view, "count"); !ok {
		t.Fatalf("还有一个登记方在，键不该消失")
	}

	second()
	if _, ok := registry.StateOf(view, "count"); ok {
		t.Fatalf("最后一个走了，键该消失")
	}
}

func TestARepeatedDisposeDoesNotStealSomeoneElsesKey(t *testing.T) {
	t.Parallel()

	// DSH 那边注销函数只会被成功登记跑一次（它挂在 fiber 上）。Go 这边一个
	// defer 加一次显式调用就破了那个前提，破了之后引用计数会变成负数、
	// 把还活着的登记方的键删掉。
	registry := NewRegistry()
	first := mustRegister(t, registry, countUnit("count", 0))
	defer mustRegister(t, registry, countUnit("count", 0))()

	first()
	first()
	first()

	if _, ok := registry.StateOf(newSession("s1"), "count"); !ok {
		t.Fatalf("多调几次注销不该动到别人的登记")
	}
}

func TestRegisterRefusesToShareAKeyAcrossStateVersions(t *testing.T) {
	t.Parallel()

	// 版本号不同就是折叠语义不同。共用它等于让一个登记方读到另一个算出来的
	// 状态，而且检查点里那个 ver 也不再说明任何事。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 1))()

	_, err := Register(registry, countUnit("count", 2))
	if !errors.Is(err, ErrStateVersionConflict) {
		t.Fatalf("该拒掉：%v", err)
	}
	if !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("两个版本号都该出现，否则不知道该改哪一边：%q", err.Error())
	}

	// 拒掉之后原来那份登记必须原封不动。
	if got := registry.registrations["count"]; got == nil || got.refs != 1 || got.def.stateVersion != 1 {
		t.Fatalf("被拒的那次登记不许动到已经在的那份：%#v", got)
	}
}

func TestStateOfAnswersWhetherTheKeyIsRegistered(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	view := newSession("s1", userEvent(0), otherEvent(1), userEvent(2))

	if _, ok := registry.StateOf(view, "nobody"); ok {
		t.Fatalf("没登记的键该说没有")
	}

	// 单元格是懒建的：这个会话比这张表老，第一次被碰到时在完整日志上折一遍。
	state, ok := registry.StateOf(view, "count")
	if !ok {
		t.Fatalf("登记过的键该给得出状态")
	}
	if state.(countState).Count != 2 {
		t.Fatalf("该折出两条：%#v", state)
	}
}

func TestSnapshotIsOneConsistentCutOverTheClientVisibleUnits(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("shown", 0))()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 0))()

	snapshot := registry.Snapshot(newSession("s1", userEvent(0), userEvent(1)))

	if snapshot.AsOfSeq != 1 {
		t.Fatalf("水位该是最后一条事件的 seq：%d", snapshot.AsOfSeq)
	}
	if len(snapshot.Values) != 1 || snapshot.Values["shown"] != 2 {
		t.Fatalf("只给宿主看的单元不许进读切：%#v", snapshot.Values)
	}
}

func TestSnapshotOnAnEmptyLogSitsAtMinusOne(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	snapshot := registry.Snapshot(newSession("s1"))
	if snapshot.AsOfSeq != -1 {
		t.Fatalf("空日志的水位该是 -1：%d", snapshot.AsOfSeq)
	}
	if snapshot.Values["count"] != 0 {
		t.Fatalf("空日志该给出初始视图：%#v", snapshot.Values)
	}
}

func TestSnapshotWithNoClientVisibleUnitIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	// 空映射和 nil 在这一层是两个意思：前者是「问过了，一个都没有」，
	// 后者会让使用方在往里写的时候炸掉。
	registry := NewRegistry()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 0))()

	values := registry.Snapshot(newSession("s1")).Values
	if values == nil {
		t.Fatalf("该是空映射，不是 nil")
	}
	if len(values) != 0 {
		t.Fatalf("一个客户端可见的单元都没有：%#v", values)
	}
}

func TestCheckpointTakesEveryUnitAndDetachesTheState(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("shown", 3))()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 4))()

	view := newSession("s1", userEvent(0), userEvent(1))
	rows, err := registry.Checkpoint(view)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}

	// 只给宿主看的单元不进读切，但**照样进检查点**——不然它每次冷启都要重折。
	if len(rows) != 2 {
		t.Fatalf("每个已登记的键都该有一行：%#v", rows)
	}
	if rows["shown"].Ver != 3 || rows["hidden"].Ver != 4 {
		t.Fatalf("版本号该跟着各自的单元走：%#v", rows)
	}
	for key, row := range rows {
		if row.Seq != 1 {
			t.Fatalf("%q 的水位该是最后一条事件的 seq：%d", key, row.Seq)
		}
		if string(row.Val) != `{"count":2}` {
			t.Fatalf("%q 的状态该是排出去的字节：%s", key, row.Val)
		}
	}
}

func TestCheckpointRefusesAStateThatCannotBeWritten(t *testing.T) {
	t.Parallel()

	// 「状态必须是纯 JSON」是单元契约，DSH 靠 structuredClone 在这里炸。
	// 本包直接排字节，于是这条契约在**取检查点**的时候就被验了，
	// 而不是等到落盘那一刻才在存储层炸出来。
	registry := NewRegistry()
	defer mustRegister(t, registry, unmarshalableUnit("bad"))()

	if _, err := registry.Checkpoint(newSession("s1")); err == nil {
		t.Fatalf("排不出去的状态该在这里就报出来")
	}
}

func TestForgetDropsTheCellsAndTheyRefoldOnNextTouch(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	view := newSession("s1", userEvent(0))
	registry.Snapshot(view)
	if len(registry.registrations["count"].cells) != 1 {
		t.Fatalf("读过一次该留下一个单元格")
	}

	registry.Forget("s1")
	if len(registry.registrations["count"].cells) != 0 {
		t.Fatalf("忘掉之后不该还留着单元格")
	}

	// 忘掉只是丢缓存，不是丢事实：再碰一次就在当时的日志上重折出来。
	if registry.Snapshot(view).Values["count"] != 1 {
		t.Fatalf("重折出来的值该还是对的")
	}
}

func TestDriveFoldsAndNotifiesOnlyWhenSomethingChanged(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	type change struct {
		key   string
		value any
		seq   int
	}
	var changes []change
	defer registry.OnChanged(func(_ SessionView, key string, value any, seq int) {
		changes = append(changes, change{key: key, value: value, seq: seq})
	})()

	view := newSession("s1")

	view.events = append(view.events, userEvent(0))
	registry.Drive(view, userEvent(0))
	view.events = append(view.events, otherEvent(1))
	registry.Drive(view, otherEvent(1))
	view.events = append(view.events, userEvent(2))
	registry.Drive(view, userEvent(2))

	if len(changes) != 2 {
		t.Fatalf("报了没变的那条不该通知任何人：%#v", changes)
	}
	if changes[0] != (change{key: "count", value: 1, seq: 0}) {
		t.Fatalf("第一声不对：%#v", changes[0])
	}
	if changes[1] != (change{key: "count", value: 2, seq: 2}) {
		t.Fatalf("第二声的 seq 该是引起这次变化的那条事件的：%#v", changes[1])
	}
}

func TestDriveDoesNotNotifyForAHostOnlyUnit(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 0))()

	notified := 0
	defer registry.OnChanged(func(SessionView, string, any, int) { notified++ })()

	view := newSession("s1", userEvent(0))
	registry.Drive(view, userEvent(0))

	if notified != 0 {
		t.Fatalf("没有客户端视图就没有可通知的值：%d", notified)
	}
	if state, _ := registry.StateOf(view, "hidden"); state.(countState).Count != 1 {
		t.Fatalf("但状态照样要往前走：%#v", state)
	}
}

func TestDriveWithNoListenerSkipsTheView(t *testing.T) {
	t.Parallel()

	// 没人听的时候连视图都不算——这是「零下游开销」那句话里省下的另一半。
	views := 0
	definition := countUnit("count", 0)
	definition.View = func(state countState) any {
		views++
		return state.Count
	}

	registry := NewRegistry()
	defer mustRegister(t, registry, definition)()

	view := newSession("s1", userEvent(0))
	registry.Drive(view, userEvent(0))

	if views != 0 {
		t.Fatalf("没人听就不该去算视图：%d", views)
	}
}

func TestDriveBuildsACellMidStreamFromTheHistoryBeforeTheEvent(t *testing.T) {
	t.Parallel()

	// 一个在事件已经流过之后才登记的单元：第一次被推进时要先把这条事件**之前**
	// 的历史折进去，再走正常的门——不然它会漏掉整段历史。
	registry := NewRegistry()
	view := newSession("s1", userEvent(0), userEvent(1), userEvent(2))

	defer mustRegister(t, registry, countUnit("count", 0))()
	registry.Drive(view, userEvent(2))

	state, _ := registry.StateOf(view, "count")
	if state.(countState).Count != 3 {
		t.Fatalf("前面两条也该被折进去：%#v", state)
	}
}

func TestDriveRefusesToCountTheSameEventTwice(t *testing.T) {
	t.Parallel()

	// 有人在「事件追加进日志」和「Drive 被调到」之间读了一次，那次懒建折的是
	// 包含这条事件的完整日志。DSH 是单线程，这个窗口不存在；本仓库要扛并发读。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	view := newSession("s1", userEvent(0))
	registry.Snapshot(view) // 懒建，已经把 seq 0 折进去了
	registry.Drive(view, userEvent(0))

	state, _ := registry.StateOf(view, "count")
	if state.(countState).Count != 1 {
		t.Fatalf("同一条事实不许数两遍：%#v", state)
	}
}

func TestDriveKeepsSessionsApart(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	first := newSession("s1", userEvent(0))
	second := newSession("s2")

	registry.Drive(first, userEvent(0))

	if state, _ := registry.StateOf(second, "count"); state.(countState).Count != 0 {
		t.Fatalf("单元格按会话身份分开存：%#v", state)
	}
}

func TestOnChangedUnsubscribesExactlyOnce(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	kept := 0
	defer registry.OnChanged(func(SessionView, string, any, int) { kept++ })()

	dropped := 0
	stop := registry.OnChanged(func(SessionView, string, any, int) { dropped++ })

	view := newSession("s1", userEvent(0))
	registry.Drive(view, userEvent(0))

	stop()
	stop()

	view.events = append(view.events, userEvent(1))
	registry.Drive(view, userEvent(1))

	if kept != 2 {
		t.Fatalf("没退订的那个该收到两声：%d", kept)
	}
	if dropped != 1 {
		t.Fatalf("退订之后不该再收到，而且多调几次退订不许动到别人：%d", dropped)
	}
}

func TestAListenerMayCallBackIntoTheRegistry(t *testing.T) {
	t.Parallel()

	// 通知是在锁外发的，所以听众回头读这张表不会死锁。这是一条会以**挂住**
	// 的形式失败的性质，值得单独钉一枪。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	var seen any
	defer registry.OnChanged(func(view SessionView, _ string, _ any, _ int) {
		seen, _ = registry.StateOf(view, "count")
	})()

	view := newSession("s1", userEvent(0))
	registry.Drive(view, userEvent(0))

	if seen.(countState).Count != 1 {
		t.Fatalf("听众该读得到刚刚那次变化：%#v", seen)
	}
}

func TestDriveOnAnEmptyRegistryIsQuiet(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.Drive(newSession("s1"), userEvent(0))
}

func TestConcurrentReadsOnDistinctSessionsAreSafe(t *testing.T) {
	t.Parallel()

	// 多用户并发是这个仓库的前提之一，读切必须能被同时调。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	var wait sync.WaitGroup
	for index := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()

			view := newSession(session.SessionID(string(rune('a'+index))), userEvent(0))
			registry.Snapshot(view)
			registry.Checkpoint(view) //nolint:errcheck // 这里只压并发，结果另有测试
			registry.StateOf(view, "count")
			registry.Forget(view.ID())
		}()
	}
	wait.Wait()
}
