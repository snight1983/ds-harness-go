// 本文件的作用：把纯回放这一侧钉住——整份日志折出来的模式、plan 这个投影单元
// 怎么从三条事件里还原出挂起状态、以及那条持久不变量各拦什么。
//
// 逐条对着 DSH 的 tests/plan-mode.spec.ts 里那几组纯函数用例走。
//
// # 这些测试防的是什么错
//
//   - **挂起状态偷偷藏在了内存里**。它必须是一个纯回放量：宿主重启、另一个标签页、
//     一次冷读都要能只从日志把它还原出来。任何一条只在活控制器上成立的断言都会让
//     第二个界面看见一个和第一个不一样的 plan 徽标。
//   - **一条坏事件被折成了一次真的翻转**。那看起来和一次合法的关闭一模一样，
//     界面上就是「计划模式自己关了」。
//   - **裸的 `/plan` 折不出挂起**。commands.RunData.Args 带 omitempty，一次空输入
//     排出去就没有 args 键；照抄 DSH 那道 `args === undefined` 检查会让最常见的
//     那一次调用整个失效。
//   - **不变量开始查形状之外的东西**。[planmode.ValidateEvent] 只该管负载长得对不对；
//     它一旦开始管次序或者配对，一份合法的老日志就会在装载这一刻突然装不进来。
package planmode_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/feature/plan/planmode"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// modeEvent 造一条负载由调用方原样给的 plan/mode。
func modeEvent(seq int, data string) sessionlog.Event {
	return sessionlog.Event{Seq: seq, Type: planmode.EventMode, Data: json.RawMessage(data)}
}

// mode 造一条负载合法的 plan/mode。
func mode(seq int, active bool) sessionlog.Event {
	if active {
		return modeEvent(seq, `{"active":true}`)
	}
	return modeEvent(seq, `{"active":false}`)
}

// plain 造一条只有类型的会话事件。
func plain(seq int, kind sessionlog.EventType) sessionlog.Event {
	return sessionlog.Event{Seq: seq, Type: kind}
}

// runEvent 造一条 `/plan` 的 command/run。
func runEvent(t *testing.T, seq int, id commands.ID, args string) sessionlog.Event {
	t.Helper()
	return commandEvent(t, seq, commands.EventRun, commands.RunData{
		ID:     id,
		Name:   planmode.CommandName,
		Args:   args,
		Source: commands.Source{Kind: commands.SourceUser},
	})
}

// doneEvent 造一条配对的 command/done。
func doneEvent(t *testing.T, seq int, id commands.ID, kind commands.ResultKind) sessionlog.Event {
	t.Helper()
	return commandEvent(t, seq, commands.EventDone, commands.DoneData{ID: id, Kind: kind})
}

// commandEvent 把一份命令负载排成事件。
func commandEvent(t *testing.T, seq int, kind sessionlog.EventType, payload any) sessionlog.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return sessionlog.Event{Seq: seq, Type: kind, Data: encoded}
}

// fakeSession 是一个只把日志摆在那儿的会话视图。
type fakeSession struct {
	id     sessionlog.SessionID
	events []sessionlog.Event
}

func (s *fakeSession) ID() sessionlog.SessionID   { return s.id }
func (s *fakeSession) Events() []sessionlog.Event { return s.events }

func (s *fakeSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}

// projectionOf 把这些事件折一遍，交出 plan 这个键上线的那个值。
func projectionOf(t *testing.T, events ...sessionlog.Event) planmode.Projection {
	t.Helper()
	registry := projection.NewRegistry()
	dispose, err := planmode.RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)
	snapshot := registry.Snapshot(&fakeSession{id: "s1", events: events})
	value, ok := snapshot.Values[planmode.ProjectionKey]
	if !ok {
		t.Fatalf("plan 这个键该在：%+v", snapshot.Values)
	}
	typed, ok := value.(planmode.Projection)
	if !ok {
		t.Fatalf("plan 这个键该是一份 Projection，拿到 %T", value)
	}
	return typed
}

func TestAnEmptyLogFoldsToInactive(t *testing.T) {
	t.Parallel()
	if planmode.FoldMode(nil) {
		t.Fatal("一条 plan/mode 都没有的日志该折成关着")
	}
}

func TestTheLastModeEventWins(t *testing.T) {
	t.Parallel()
	events := []sessionlog.Event{mode(0, true), mode(1, false), mode(2, true)}
	if !planmode.FoldMode(events) {
		t.Fatal("最后一条算数，该折成开着")
	}
}

func TestFoldingStopsAtTheGivenEnd(t *testing.T) {
	t.Parallel()
	events := []sessionlog.Event{mode(0, true), mode(1, false)}
	if !planmode.FoldModeUntil(events, 1) {
		t.Fatal("只折前一条时该是开着")
	}
	if planmode.FoldModeUntil(events, 2) {
		t.Fatal("两条都折时该是关着")
	}
	// 超出长度的 end 收敛到整份日志，而不是越界。
	if planmode.FoldModeUntil(events, 99) {
		t.Fatal("end 超长时该等价于折整份")
	}
	// 空前缀里一条都没有。
	if planmode.FoldModeUntil(events, 0) {
		t.Fatal("空前缀该折成关着")
	}
}

func TestAnUnreadableModeEventKeepsThePreviousState(t *testing.T) {
	t.Parallel()
	// 四种坏法：不是 JSON、缺字段、显式 null、类型不对。每一种被宽容地读成 false
	// 都等于让界面看见一次凭空发生的关闭。
	for _, bad := range []string{`not json`, `{}`, `{"active":null}`, `{"active":42}`} {
		events := []sessionlog.Event{mode(0, true), modeEvent(1, bad)}
		if !planmode.FoldMode(events) {
			t.Fatalf("负载 %s 该被跳过、状态保持开着", bad)
		}
	}
}

func TestTheProjectionFoldsPendingOutOfTheLogAlone(t *testing.T) {
	t.Parallel()

	// 一次还没结算的 `/plan`：用户刚敲下的那一下就是最新的意图。
	running := projectionOf(t, runEvent(t, 0, "c1", ""))
	if running.Active || !running.Pending {
		t.Fatalf("一次还没结算的进入该是 active=false pending=true，拿到 %+v", running)
	}

	// 成功结算之后仍然挂着，等那条 plan/mode。
	settled := projectionOf(t, runEvent(t, 0, "c1", ""), doneEvent(t, 1, "c1", commands.ResultSuccess))
	if settled.Active || !settled.Pending {
		t.Fatalf("成功结算之后该继续挂着，拿到 %+v", settled)
	}

	// plan/mode 落下来就把它清掉。
	applied := projectionOf(t,
		runEvent(t, 0, "c1", ""),
		doneEvent(t, 1, "c1", commands.ResultSuccess),
		mode(2, true),
	)
	if !applied.Active || applied.Pending {
		t.Fatalf("落盘之后该是 active=true pending=false，拿到 %+v", applied)
	}
}

func TestABareSlashPlanStillFoldsToPending(t *testing.T) {
	t.Parallel()
	// commands.RunData.Args 带 omitempty，所以一次空输入排出去**没有** args 键。
	// 照抄 DSH 那道 `args === undefined` 检查会让这条日志折不出任何挂起。
	raw := runEvent(t, 0, "c1", "")
	if strings.Contains(string(raw.Data), `"args"`) {
		t.Fatalf("这条用例的前提是空输入不排 args 键，实际排出了 %s", raw.Data)
	}
	if got := projectionOf(t, raw); !got.Pending {
		t.Fatalf("裸的 /plan 该折出 pending，拿到 %+v", got)
	}
}

func TestAFailedCommandLeavesNoPendingSelection(t *testing.T) {
	t.Parallel()
	got := projectionOf(t, runEvent(t, 0, "c1", ""), doneEvent(t, 1, "c1", commands.ResultError))
	if got.Pending {
		t.Fatalf("失败的选择不该留下挂起，拿到 %+v", got)
	}
}

func TestASelectionThatMatchesTheLoggedStateIsNotPending(t *testing.T) {
	t.Parallel()
	// 已经开着的会话上再 `/plan`：那一次成功没有改变任何东西，徽标不该亮。
	got := projectionOf(t,
		mode(0, true),
		runEvent(t, 1, "c1", ""),
		doneEvent(t, 2, "c1", commands.ResultSuccess),
	)
	if !got.Active || got.Pending {
		t.Fatalf("同向的选择该是 active=true pending=false，拿到 %+v", got)
	}
}

func TestOffIsTheOnlyArgumentThatMeansLeave(t *testing.T) {
	t.Parallel()
	leaving := projectionOf(t, mode(0, true), runEvent(t, 1, "c1", " off "))
	if !leaving.Pending {
		t.Fatalf("`/plan off` 该折出一次挂起的退出，拿到 %+v", leaving)
	}
	// 其余一律是「进入」，包括跟着一段话的那一次。
	staying := projectionOf(t, mode(0, true), runEvent(t, 1, "c1", "offline 的时候怎么办"))
	if staying.Pending {
		t.Fatalf("`off` 之外的输入都是进入，已经开着就不该挂起，拿到 %+v", staying)
	}
}

func TestADoneEventFromAnotherCommandIsIgnored(t *testing.T) {
	t.Parallel()
	got := projectionOf(t, runEvent(t, 0, "c1", ""), doneEvent(t, 1, "c2", commands.ResultError))
	if !got.Pending {
		t.Fatalf("配对号对不上的结算不该动这次执行，拿到 %+v", got)
	}
}

func TestTheInvariantOnlyChecksTheModePayloadShape(t *testing.T) {
	t.Parallel()

	if err := planmode.ValidateEvent(mode(0, true)); err != nil {
		t.Fatalf("一条合法的 plan/mode 不该被拦：%v", err)
	}
	// 别的类型一概不管：一条 command/run 的形状不归本包的不变量管。
	if err := planmode.ValidateEvent(runEvent(t, 0, "c1", "")); err != nil {
		t.Fatalf("别的事件类型不该被拦：%v", err)
	}

	for _, bad := range []struct {
		payload string
		quoted  string
	}{
		{`{}`, "null"},
		{`{"active":null}`, "null"},
		{`{"active":42}`, "42"},
		{`{"active":"true"}`, `"true"`},
	} {
		err := planmode.ValidateEvent(modeEvent(0, bad.payload))
		if err == nil {
			t.Fatalf("负载 %s 该被拦下", bad.payload)
		}
		want := "plan/mode carries invalid active state " + bad.quoted + "; expected a boolean"
		if err.Error() != want {
			t.Fatalf("报的是日志里那个原样的值：想要 %q，拿到 %q", want, err.Error())
		}
	}
}

// invariantHarness 是一次不变量测试要的家当。
//
// 两条胳膊都由它扮：loaded 是「装载这一刻日志里已经有的」，observers 是「后来追加的」。
type invariantHarness struct {
	registry  *invariants.Registry
	loaded    []sessionlog.Event
	observers []func(sessionlog.Event)
	// unsubscribed 记下退订被调了几次。
	unsubscribed int
}

// newInvariantHarness 造一个全开的注册表，带上一份已经装载的日志。
func newInvariantHarness(t *testing.T, loaded ...sessionlog.Event) *invariantHarness {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return &invariantHarness{registry: registry, loaded: loaded}
}

// register 把本包的检查装进去。
func (h *invariantHarness) register(t *testing.T) func() {
	t.Helper()
	undo, err := planmode.RegisterInvariants(
		t.Context(),
		h.registry,
		func() []sessionlog.Event { return h.loaded },
		func(observer func(sessionlog.Event)) func() {
			h.observers = append(h.observers, observer)
			return func() { h.unsubscribed++ }
		},
	)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	return undo
}

// emit 把一条事件推给所有还在的观察者。
func (h *invariantHarness) emit(event sessionlog.Event) {
	for _, observer := range h.observers {
		observer(event)
	}
}

// violation 跑一段会违例的代码，交出那条违例。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func violation(t *testing.T, run func()) *invariants.Error {
	t.Helper()
	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		run()
	}()
	if caught == nil {
		t.Fatal("该抛出一条违例")
	}
	return caught
}

func TestTheInvariantCatchesABadRecordAlreadyInTheLog(t *testing.T) {
	t.Parallel()
	// 一份历史里就带着坏记录的会话必须在装载这一刻就响，而不是等下一次追加。
	h := newInvariantHarness(t, modeEvent(0, `{"active":42}`))

	failure := violation(t, func() { h.register(t) })
	if failure.PackageName != planmode.PackageName {
		t.Fatalf("该报在本包名下，拿到 %q", failure.PackageName)
	}
	if !strings.Contains(failure.Message, "invalid active state 42") {
		t.Fatalf("该带上那条违例本身，拿到 %q", failure.Message)
	}
}

func TestTheInvariantCatchesABadRecordAppendedLater(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	h.register(t)

	// 一条合法的追加不该有任何动静。
	h.emit(mode(0, true))

	failure := violation(t, func() { h.emit(modeEvent(1, `{"active":"true"}`)) })
	if !strings.Contains(failure.Message, `invalid active state "true"`) {
		t.Fatalf("该带上那条违例本身，拿到 %q", failure.Message)
	}
}

func TestUnregisteringTheInvariantStopsTheCheck(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	undo()

	// 一条不该再查的检查绝不许继续在别人的写路径上抛。
	if h.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", h.unsubscribed)
	}
}

func TestRegisteringInvariantsNeedsAllThreeArms(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	loaded := func() []sessionlog.Event { return nil }
	subscribe := func(func(sessionlog.Event)) func() { return func() {} }

	if _, err := planmode.RegisterInvariants(t.Context(), nil, loaded, subscribe); !errors.Is(err, planmode.ErrInvalidConfig) {
		t.Fatalf("没有注册表该报配置不成立，拿到 %v", err)
	}
	if _, err := planmode.RegisterInvariants(t.Context(), registry, nil, subscribe); !errors.Is(err, planmode.ErrInvalidConfig) {
		t.Fatalf("没有读出已装载日志的路该报配置不成立，拿到 %v", err)
	}
	if _, err := planmode.RegisterInvariants(t.Context(), registry, loaded, nil); !errors.Is(err, planmode.ErrInvalidConfig) {
		t.Fatalf("没有订阅后续事件的路该报配置不成立，拿到 %v", err)
	}
}

func TestTheProjectionIsAbsentWhenTheUnitIsNotRegistered(t *testing.T) {
	t.Parallel()
	// 「这个装配里根本没有计划模式」由这个键整个不在来表达，绝不用某个取值表达。
	registry := projection.NewRegistry()
	if _, ok := registry.StateOf(&fakeSession{id: "s1"}, planmode.ProjectionKey); ok {
		t.Fatal("没登记过这个单元的时候 plan 这个键不该在")
	}
}

func TestRegisteringTheProjectionNeedsARegistry(t *testing.T) {
	t.Parallel()
	if _, err := planmode.RegisterProjection(nil); err == nil {
		t.Fatal("没有投影注册表该报错")
	}
}

// 这一行只为让 plain 在本文件里有用处：turn/start 那条在控制器那一侧的用例里用。
var _ = plain
