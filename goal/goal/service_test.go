// 本文件的作用：把那台对外服务钉在它真会出错的边上——那道「必须是注册表里活着
// 的那一个」的闸门、每一个动词准不准、活化怎么跨过一次追加又怎么被别人写的改动
// 打回去，以及那条 `goal/changed` 通告落在哪几层观察者身上。
//
// # 这些测试防的是什么错
//
//   - **活化从日志里被恢复出来**。[Activation] 一次都不落盘，可它偏偏是「这个进程
//     会不会自动接着推」的开关。一份新缓存、一次会话起跑、一条别人写的
//     `goal/change`，三处都必须把它打回 [Disarmed]；漏掉任何一处，一次续会话或者
//     一次分叉就会让两个进程同时推同一个目标。
//   - **本包自己刚写下的那条改动把自己掐掉**。上一条规矩的反面：认不出自己那一条，
//     每一次 create/resume 都会在它自己那次同步里立刻回到 disarmed，于是这个包
//     一次都推不起来。
//   - **CAS 被绕过去**。改动一律要交出调用方以为的那个修订号；少了这道比对，两个
//     各拿着一份旧状态的持有方会互相抹掉对方的写入，而日志上看不出任何异常。
//   - **一次时钟回拨写下一条自己都读不回来的改动**。严格回放要求 updatedAt 不往回
//     走，照抄墙上时钟会当场破掉本包自己的不变量。
//   - **交出去的视图和缓存共享内存**。[Snapshot.BlockedReason] 是导出的指针，调用方
//     穿过它写一个字就改掉了这个进程里的目标状态，而日志里一点痕迹都没有。
//   - **一个观察者炸掉把已经落盘的改动带下水**。通告发在改动之后，观察者说什么都
//     改不了它，也不该饿着排在它后面的同侪。
//
// # 有三条路这里够不着
//
// [Service.commit] 里「追加失败」和「追加成功但同步失败」那两支，从本包外面构造
// 不出来：待写的那条改动刚刚在同一把锁下折过一遍，而一台游离会话（[coresession.NewSession]）
// 没有事件观察者，也就没有任何人能在这两步中间插一条事件进去。留着它们是因为
// 装配方把会话换成一台**接了存储的**实例时这两支就活了，而那时候吞掉它们等于让一次
// 写失败表现成一次成功。
//
// 同一个 commit 里「排字节失败」那一支也够不着：[Snapshot.MarshalJSON] 只在阶段和
// 阻塞原因自相矛盾时才拒，而这条路上的那份快照是 [Service.snapshotChange] 刚从一份
// 折过的状态里拼出来的。它防的是本包以后自己写坏了那一步——那种时候必须当场报错，
// 而不是往日志里落一条谁都读不回来的改动。

package goal

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/session"
)

// ptr 把一个值取址，专给那几个「不给」和「给了个非法值」必须分开的可选入参。
func ptr[T any](value T) *T { return &value }

// newQuietService 造一台把诊断日志丢掉的服务。
//
// 那几条走 [Service.contain] 和 [Service.Install] 警告分支的用例会真的记一条
// warn，默认落到 [slog.Default] 上会把测试输出搅乱。
func newQuietService(t *testing.T, agents Agents, clock *stubClock) *Service {
	t.Helper()
	service, err := New(Config{
		Agents: agents,
		Now:    clock.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	return service
}

// appendChange 绕开本包，直接往这条日志里写一条改动——「别人写的改动」那几条用它。
func appendChange(t *testing.T, owner *stubAgent, change Change) {
	t.Helper()
	data, err := change.MarshalJSON()
	if err != nil {
		t.Fatalf("排改动失败：%v", err)
	}
	owner.append(t, session.Event{Type: EventChange, Data: data})
}

// expectCode 断言这次失败落在码表里的哪一格。
func expectCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if code := errorCode(t, err); code != want {
		t.Fatalf("报的是 %q，本该是 %q（%v）", code, want, err)
	}
}

// ---- 构造 ----

func TestNewRequiresAnAgentRegistry(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("没有 agent 注册表本该造不出这台服务")
	}
}

func TestNewRejectsAnUnusableDefaultRoundCap(t *testing.T) {
	// 部署方给的默认值和调用方显式给的走同一道校验：留一个非法默认值下来，
	// 每一次不带上限的 create 都会在写之前才炸，而那时候责任已经指不回配置了。
	if _, err := New(Config{Agents: newStubAgents(), DefaultMaxGoalRounds: -1}); err == nil {
		t.Fatal("一个负的默认轮数上限本该在造服务这一刻就被拒")
	}
}

func TestNewFillsInTheOmittedCollaborators(t *testing.T) {
	// Now、Logger 都不给，DefaultMaxGoalRounds 留零值：三样都该落到默认上，
	// 而且这台服务立刻就能用。
	owner := newStubAgent(t, "session-1", nil, nil)
	service, err := New(Config{Agents: newStubAgents(owner)})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	view := mustCreate(t, service, owner, "写完这一段")
	if view.MaxGoalRounds != DefaultMaxGoalRounds {
		t.Fatalf("默认轮数上限是 %d，本该是 %d", view.MaxGoalRounds, DefaultMaxGoalRounds)
	}
	if view.CreatedAt == 0 {
		t.Fatal("没给时钟时本该落到墙上时钟，得到的时刻是 0")
	}
}

// ---- 活 agent 闸门 ----

func TestEveryEntryPointRequiresALiveAgent(t *testing.T) {
	service, owner, agents, _ := newSingleAgentService(t)
	view := mustCreate(t, service, owner, "写完这一段")

	// 比的是对象不是标识：同一个标识被重新开起来是另一段生命周期，往它身上写
	// 目标等于往一段已经不归它管的会话里写东西。
	impostor := newStubAgent(t, "session-1", nil, nil)
	agents.put(impostor)

	calls := map[string]func(who agent.Agent) error{
		"Get":    func(who agent.Agent) error { _, err := service.Get(who); return err },
		"Disarm": func(who agent.Agent) error { _, err := service.Disarm(who); return err },
		"Create": func(who agent.Agent) error {
			_, err := service.Create(who, CreateRequest{Objective: "写完"})
			return err
		},
		"Edit": func(who agent.Agent) error {
			_, err := service.Edit(who, view.Ref, EditRequest{Objective: ptr("换")})
			return err
		},
		"Pause":    func(who agent.Agent) error { _, err := service.Pause(who, view.Ref); return err },
		"Resume":   func(who agent.Agent) error { _, err := service.Resume(who, view.Ref); return err },
		"Complete": func(who agent.Agent) error { _, err := service.Complete(who, view.Ref); return err },
		"Block": func(who agent.Agent) error {
			_, err := service.Block(who, view.Ref, BlockReason{Code: "x", Message: "y"})
			return err
		},
		"Clear": func(who agent.Agent) error { _, err := service.Clear(who, view.Ref); return err },
	}
	for name, call := range calls {
		t.Run(name+"／顶掉的实例", func(t *testing.T) {
			expectCode(t, call(owner), CodeAgentNotLive)
		})
		t.Run(name+"／根本没给 agent", func(t *testing.T) {
			expectCode(t, call(nil), CodeAgentNotLive)
		})
	}

	// 从注册表里摘掉之后同样进不来。
	agents.drop(impostor.ID())
	expectCode(t, func() error { _, err := service.Get(impostor); return err }(), CodeAgentNotLive)
}

// ---- 读 ----

func TestGetReturnsNilWhenThereIsNoGoal(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	view, err := service.Get(owner)
	if err != nil {
		t.Fatalf("读一个还没有目标的会话本该成功：%v", err)
	}
	if view != nil {
		t.Fatalf("本该是 nil，得到的是 %+v", view)
	}
}

func TestGetProjectsTheWholeLog(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")

	// 一条带目标来源的用户消息由别人写下：它推轮数，但它**不是** goal/change，
	// 所以一点都不该动活化。
	owner.append(t, userEvent(t, &Source{GoalID: created.ID, Revision: created.Revision, Round: 1}))
	clock.advance(50)

	view, err := service.Get(owner)
	if err != nil {
		t.Fatalf("读目标失败：%v", err)
	}
	if view.RoundsStarted != 1 {
		t.Fatalf("轮数是 %d，本该是 1", view.RoundsStarted)
	}
	if view.Activation != Armed {
		t.Fatalf("活化是 %q，一条用户消息本该一点都不动它", view.Activation)
	}
	if view.CreatedAt != created.CreatedAt || view.UpdatedAt != created.UpdatedAt {
		t.Fatalf("时刻被一条用户消息改了：%+v", view)
	}
}

func TestGetHandsOutADetachedView(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	blocked, err := service.Block(owner, created.Ref, BlockReason{Code: "provider-quota", Message: "额度用完了"})
	if err != nil {
		t.Fatalf("拦下目标失败：%v", err)
	}

	// 穿过那个导出的指针写一个字：缓存里的那份必须纹丝不动。
	blocked.BlockedReason.Message = "被改掉了"
	blocked.Objective = "也被改掉了"

	again, err := service.Get(owner)
	if err != nil {
		t.Fatalf("再读一次失败：%v", err)
	}
	if again.BlockedReason.Message != "额度用完了" || again.Objective != "写完这一段" {
		t.Fatalf("交出去的视图和缓存共享了内存：%+v", again)
	}
}

func TestGetSurfacesALogItCannotFold(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	// 第一次触碰就折不动：这条路走的是 cacheFor 里那趟整段回放。
	owner.append(t, changeEvent(`{"kind":"goal/change","version":9}`))
	_, err := service.Get(owner)
	expectFoldError(t, err, "一份第一次触碰就折不动的日志")
}

func TestGetSurfacesALogThatBrokeAfterTheCacheWasBuilt(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	mustCreate(t, service, owner, "写完这一段")
	// 缓存已经建好了，这条路走的是 sync 里那趟增量。
	owner.append(t, changeEvent(`{"kind":"goal/change","version":9}`))
	_, err := service.Get(owner)
	expectFoldError(t, err, "一份后来才坏掉的日志")
}

// ---- create ----

func TestCreateStartsArmedAndUsesTheDeploymentDefault(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	view := mustCreate(t, service, owner, "  写完这一段  ")
	if view.Objective != "写完这一段" {
		t.Fatalf("目标描述是 %q，首尾空白本该被削掉", view.Objective)
	}
	if view.Revision != 1 || view.Phase != PhaseActive || view.Activation != Armed {
		t.Fatalf("建出来的是 %+v", view)
	}
	if view.MaxGoalRounds != DefaultMaxGoalRounds || view.RoundsStarted != 0 {
		t.Fatalf("轮数那一对是 (%d, %d)", view.RoundsStarted, view.MaxGoalRounds)
	}
	if view.CreatedAt != clock.Now().UnixMilli() || view.UpdatedAt != view.CreatedAt {
		t.Fatalf("时刻是 (%d, %d)", view.CreatedAt, view.UpdatedAt)
	}
	if len(owner.changes()) != 1 {
		t.Fatalf("日志里落了 %d 条改动，本该恰好 1 条", len(owner.changes()))
	}
}

func TestCreateHonorsAnExplicitRoundCap(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	view, err := service.Create(owner, CreateRequest{Objective: "写完这一段", MaxGoalRounds: ptr(3)})
	if err != nil {
		t.Fatalf("建目标失败：%v", err)
	}
	if view.MaxGoalRounds != 3 {
		t.Fatalf("轮数上限是 %d，本该是 3", view.MaxGoalRounds)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		request CreateRequest
		code    ErrorCode
	}{
		"目标描述是空的":    {CreateRequest{Objective: "   "}, CodeInvalidObjective},
		"显式给了 0 做上限": {CreateRequest{Objective: "写完", MaxGoalRounds: ptr(0)}, CodeInvalidMaxRounds},
		"上限是负的":      {CreateRequest{Objective: "写完", MaxGoalRounds: ptr(-1)}, CodeInvalidMaxRounds},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			service, owner, _, _ := newSingleAgentService(t)
			_, err := service.Create(owner, each.request)
			expectCode(t, err, each.code)
			if len(owner.changes()) != 0 {
				t.Fatal("被拒的那次 create 不该往日志里留下任何东西")
			}
		})
	}
}

func TestCreateRefusesASecondUnfinishedGoal(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	first := mustCreate(t, service, owner, "写完这一段")
	_, err := service.Create(owner, CreateRequest{Objective: "另一件"})
	expectCode(t, err, CodeAlreadyExists)

	// 停住的那个也拦得住：要么先 Clear，要么先 Complete。
	if _, err := service.Pause(owner, first.Ref); err != nil {
		t.Fatalf("停下目标失败：%v", err)
	}
	_, err = service.Create(owner, CreateRequest{Objective: "另一件"})
	expectCode(t, err, CodeAlreadyExists)
}

func TestCreateIsAllowedAfterCompletion(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	first := mustCreate(t, service, owner, "写完这一段")
	if _, err := service.Complete(owner, first.Ref); err != nil {
		t.Fatalf("完成目标失败：%v", err)
	}
	clock.advance(100)
	second := mustCreate(t, service, owner, "下一件")
	if second.ID == first.ID {
		t.Fatal("下一个目标本该有一个全新的 id——同一个 id 跨过历史再用一次，来源就说不清指的是谁了")
	}
	if second.Revision != 1 || second.CreatedAt != clock.Now().UnixMilli() {
		t.Fatalf("建出来的是 %+v", second)
	}
}

// ---- edit ----

func TestEditChangesWhatItIsToldAndNothingElse(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	clock.advance(100)

	edited, err := service.Edit(owner, created.Ref, EditRequest{
		Objective: ptr("  换一句  "), MaxGoalRounds: ptr(4),
	})
	if err != nil {
		t.Fatalf("改目标失败：%v", err)
	}
	if edited.Objective != "换一句" || edited.MaxGoalRounds != 4 {
		t.Fatalf("改出来的是 %+v", edited)
	}
	if edited.Revision != 2 || edited.Phase != PhaseActive {
		t.Fatalf("edit 本该只加一个修订号、一点都不碰阶段，得到的是 %+v", edited)
	}
	// 活化原样留下：改一句描述不该把一个正在推进的目标停下。
	if edited.Activation != Armed {
		t.Fatalf("活化是 %q，本该原样留着", edited.Activation)
	}
	if edited.CreatedAt != created.CreatedAt || edited.UpdatedAt != clock.Now().UnixMilli() {
		t.Fatalf("时刻是 (%d, %d)", edited.CreatedAt, edited.UpdatedAt)
	}

	// 只给一个字段时另一个原样留着。
	onlyRounds, err := service.Edit(owner, edited.Ref, EditRequest{MaxGoalRounds: ptr(9)})
	if err != nil {
		t.Fatalf("只改轮数失败：%v", err)
	}
	if onlyRounds.Objective != "换一句" || onlyRounds.MaxGoalRounds != 9 {
		t.Fatalf("只改轮数却动了别的：%+v", onlyRounds)
	}
}

func TestEditRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		ref     func(view *View) Ref
		request EditRequest
		code    ErrorCode
	}{
		"ref 的修订号过时了": {
			func(view *View) Ref { return Ref{ID: view.ID, Revision: view.Revision + 1} },
			EditRequest{Objective: ptr("换一句")}, CodeStaleRevision,
		},
		"ref 指着别的目标": {
			func(view *View) Ref { return Ref{ID: "goal-别人", Revision: view.Revision} },
			EditRequest{Objective: ptr("换一句")}, CodeStaleRevision,
		},
		"一个字段都没给": {
			func(view *View) Ref { return view.Ref }, EditRequest{}, CodeInvalidEdit,
		},
		"新描述是空的": {
			func(view *View) Ref { return view.Ref },
			EditRequest{Objective: ptr("   ")}, CodeInvalidObjective,
		},
		"新的轮数上限是 0": {
			func(view *View) Ref { return view.Ref },
			EditRequest{MaxGoalRounds: ptr(0)}, CodeInvalidMaxRounds,
		},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			service, owner, _, _ := newSingleAgentService(t)
			view := mustCreate(t, service, owner, "写完这一段")
			_, err := service.Edit(owner, each.ref(view), each.request)
			expectCode(t, err, each.code)
			if len(owner.changes()) != 1 {
				t.Fatal("被拒的那次 edit 不该往日志里留下任何东西")
			}
		})
	}
}

func TestMutationsWithoutACurrentGoalReportNotFound(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	ref := Ref{ID: "goal-1", Revision: 1}
	calls := map[string]func() error{
		"Edit":     func() error { _, err := service.Edit(owner, ref, EditRequest{Objective: ptr("换")}); return err },
		"Pause":    func() error { _, err := service.Pause(owner, ref); return err },
		"Resume":   func() error { _, err := service.Resume(owner, ref); return err },
		"Complete": func() error { _, err := service.Complete(owner, ref); return err },
		"Block": func() error {
			_, err := service.Block(owner, ref, BlockReason{Code: "x", Message: "y"})
			return err
		},
		"Clear": func() error { _, err := service.Clear(owner, ref); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) { expectCode(t, call(), CodeNotFound) })
	}
}

// ---- 阶段 ----

func TestPauseAndResumeWalkTheActivationBackAndForth(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	clock.advance(10)

	paused, err := service.Pause(owner, created.Ref)
	if err != nil {
		t.Fatalf("停下目标失败：%v", err)
	}
	if paused.Phase != PhasePaused || paused.Revision != 2 || paused.Activation != Disarmed {
		t.Fatalf("停下之后是 %+v", paused)
	}

	// 停住的目标再停一次不成立：那说明调用方以为自己停下了什么，其实什么都没发生。
	_, err = service.Pause(owner, paused.Ref)
	expectCode(t, err, CodeInvalidTransition)

	clock.advance(10)
	resumed, err := service.Resume(owner, paused.Ref)
	if err != nil {
		t.Fatalf("推起来失败：%v", err)
	}
	if resumed.Phase != PhaseActive || resumed.Revision != 3 || resumed.Activation != Armed {
		t.Fatalf("推起来之后是 %+v", resumed)
	}
	if resumed.CreatedAt != created.CreatedAt {
		t.Fatalf("建立时刻被跃迁改了：%d", resumed.CreatedAt)
	}
}

func TestResumeRearmsAnActiveButDisarmedGoal(t *testing.T) {
	// 这是「一次会话起跑边沿之后重新点亮」那条路：耐久阶段还是 active，只是这个
	// 进程的续推资格被收走了，Resume 是重新授权的那一次。
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	disarmed, err := service.Disarm(owner)
	if err != nil {
		t.Fatalf("收回续推资格失败：%v", err)
	}
	if disarmed.Phase != PhaseActive || disarmed.Revision != 1 || disarmed.Activation != Disarmed {
		t.Fatalf("Disarm 本该只动活化，得到的是 %+v", disarmed)
	}
	if len(owner.changes()) != 1 {
		t.Fatal("Disarm 一条耐久改动都不该写")
	}

	resumed, err := service.Resume(owner, created.Ref)
	if err != nil {
		t.Fatalf("重新点亮失败：%v", err)
	}
	if resumed.Phase != PhaseActive || resumed.Revision != 2 || resumed.Activation != Armed {
		t.Fatalf("重新点亮之后是 %+v", resumed)
	}
}

func TestResumeRefusesAnAlreadyArmedActiveGoal(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	_, err := service.Resume(owner, created.Ref)
	expectCode(t, err, CodeInvalidTransition)
	if len(owner.changes()) != 1 {
		t.Fatal("被拒的那次 resume 本该连一个修订号都不吃掉")
	}
}

func TestResumeRefusesAnExhaustedBudget(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	created, err := service.Create(owner, CreateRequest{Objective: "写完这一段", MaxGoalRounds: ptr(1)})
	if err != nil {
		t.Fatalf("建目标失败：%v", err)
	}
	owner.append(t, userEvent(t, &Source{GoalID: created.ID, Revision: created.Revision, Round: 1}))

	paused, err := service.Pause(owner, created.Ref)
	if err != nil {
		t.Fatalf("停下目标失败：%v", err)
	}
	// 预算是一道数得出来、赖不掉的东西：推起来之前必须先把上限提上去。
	_, err = service.Resume(owner, paused.Ref)
	expectCode(t, err, CodeInvalidTransition)

	raised, err := service.Edit(owner, paused.Ref, EditRequest{MaxGoalRounds: ptr(2)})
	if err != nil {
		t.Fatalf("提高上限失败：%v", err)
	}
	if _, err := service.Resume(owner, raised.Ref); err != nil {
		t.Fatalf("提高上限之后本该推得起来：%v", err)
	}
}

func TestResumeRefusesACompletedGoal(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	completed, err := service.Complete(owner, created.Ref)
	if err != nil {
		t.Fatalf("完成目标失败：%v", err)
	}
	if completed.Phase != PhaseComplete || completed.Revision != 2 || completed.Activation != Disarmed {
		t.Fatalf("完成之后是 %+v", completed)
	}
	_, err = service.Resume(owner, completed.Ref)
	expectCode(t, err, CodeInvalidTransition)
	_, err = service.Complete(owner, completed.Ref)
	expectCode(t, err, CodeInvalidTransition)
}

func TestBlockCarriesItsReasonAndDisarms(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")

	blocked, err := service.Block(owner, created.Ref, BlockReason{
		Code: "provider-quota", Message: "  额度用完了  ",
	})
	if err != nil {
		t.Fatalf("拦下目标失败：%v", err)
	}
	if blocked.Phase != PhaseBlocked || blocked.Revision != 2 || blocked.Activation != Disarmed {
		t.Fatalf("拦下之后是 %+v", blocked)
	}
	if blocked.BlockedReason == nil || blocked.BlockedReason.Message != "额度用完了" {
		t.Fatalf("阻塞原因是 %+v，那句话的首尾空白本该被削掉", blocked.BlockedReason)
	}

	// 别的动词一律把阻塞原因抹掉——「不是 blocked 就不许带 blockedReason」那条
	// 不变量在写这一侧的兑现处。
	resumed, err := service.Resume(owner, blocked.Ref)
	if err != nil {
		t.Fatalf("从阻塞里推起来失败：%v", err)
	}
	if resumed.Phase != PhaseActive || resumed.BlockedReason != nil {
		t.Fatalf("推起来之后是 %+v", resumed)
	}
}

func TestBlockRejectsWhatItCannotBlock(t *testing.T) {
	t.Run("阶段不是 active", func(t *testing.T) {
		service, owner, _, _ := newSingleAgentService(t)
		created := mustCreate(t, service, owner, "写完这一段")
		paused, err := service.Pause(owner, created.Ref)
		if err != nil {
			t.Fatalf("停下目标失败：%v", err)
		}
		_, err = service.Block(owner, paused.Ref, BlockReason{Code: "x", Message: "y"})
		expectCode(t, err, CodeInvalidTransition)
	})

	reasons := map[string]BlockReason{
		"阻塞码不是 lower-kebab-case": {Code: "Bad_Code", Message: "y"},
		"阻塞码是空的":                 {Code: "", Message: "y"},
		"那句话是空的":                 {Code: "provider-quota", Message: "   "},
	}
	for name, reason := range reasons {
		t.Run(name, func(t *testing.T) {
			service, owner, _, _ := newSingleAgentService(t)
			created := mustCreate(t, service, owner, "写完这一段")
			_, err := service.Block(owner, created.Ref, reason)
			expectCode(t, err, CodeInvalidBlockReason)
		})
	}
}

func TestClearLeavesATombstoneRef(t *testing.T) {
	service, owner, _, clock := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	clock.advance(20)

	tombstone, err := service.Clear(owner, created.Ref)
	if err != nil {
		t.Fatalf("清掉目标失败：%v", err)
	}
	if tombstone.ID != created.ID || tombstone.Revision != created.Revision+1 {
		t.Fatalf("墓碑是 %+v，本该是 %q 修订 %d", tombstone, created.ID, created.Revision+1)
	}

	view, err := service.Get(owner)
	if err != nil {
		t.Fatalf("清掉之后读目标失败：%v", err)
	}
	if view != nil {
		t.Fatalf("清掉之后本该没有当前目标了，得到的是 %+v", view)
	}

	// 清掉之后建得出下一个，但绝不许是同一个 id。
	next := mustCreate(t, service, owner, "下一件")
	if next.ID == created.ID {
		t.Fatal("同一个 id 跨过墓碑又被建了一次")
	}
}

// ---- 时钟 ----

func TestMutationTimeNeverWalksBackwards(t *testing.T) {
	// 严格回放要求 updatedAt 不往回走。一次时钟回拨如果直接照抄，写下的那条改动
	// 会当场破掉本包自己的不变量——下一次装载就再也折不出来了。
	service, owner, _, clock := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	clock.set(created.UpdatedAt - 500)

	edited, err := service.Edit(owner, created.Ref, EditRequest{Objective: ptr("换一句")})
	if err != nil {
		t.Fatalf("改目标失败：%v", err)
	}
	if edited.UpdatedAt != created.UpdatedAt {
		t.Fatalf("回拨之后写下的时刻是 %d，本该被夹在 %d 上", edited.UpdatedAt, created.UpdatedAt)
	}
	// 夹住之后那条改动本身也必须还折得回来。
	if err := ValidateStream(owner.events()); err != nil {
		t.Fatalf("回拨之后写下的日志折不动了：%v", err)
	}
}

// ---- 活化的同步规矩 ----

func TestAChangeWrittenByAnyoneElseDisarms(t *testing.T) {
	// 一次分叉、一件外部工具、一段被别的进程续上的日志，都从这条路进来：不是本
	// 进程写的那条 goal/change，一律把续推资格收回去。
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")

	appendChange(t, owner, Change{
		Version:   ChangeVersion,
		Operation: OpEdit,
		Goal: Snapshot{
			Ref:           Ref{ID: created.ID, Revision: created.Revision + 1},
			Objective:     "别人换的一句",
			Phase:         PhaseActive,
			MaxGoalRounds: created.MaxGoalRounds,
		},
		RoundsStarted: created.RoundsStarted,
		CreatedAt:     created.CreatedAt,
		UpdatedAt:     created.UpdatedAt + 1,
	})

	view, err := service.Get(owner)
	if err != nil {
		t.Fatalf("读目标失败：%v", err)
	}
	if view.Revision != 2 || view.Objective != "别人换的一句" {
		t.Fatalf("别人写的那条改动没被吃进来：%+v", view)
	}
	if view.Activation != Disarmed {
		t.Fatalf("活化是 %q，别人写的改动本该把它收回去", view.Activation)
	}
}

func TestTheServiceKeepsTheActivationItJustWroteDownItself(t *testing.T) {
	// 上一条规矩的另一半：认不出自己刚写下的那一条，每一次 create/resume 都会在
	// 它自己那次同步里立刻把刚点亮的活化掐掉，于是这个包一次都推不起来。
	service, owner, _, _ := newSingleAgentService(t)
	created := mustCreate(t, service, owner, "写完这一段")
	if created.Activation != Armed {
		t.Fatalf("刚建出来的目标是 %q，本该是 armed", created.Activation)
	}
	// 再读一次也得还在：pending 那份认领是一次性的，读路径不该把它弄丢。
	again, err := service.Get(owner)
	if err != nil {
		t.Fatalf("读目标失败：%v", err)
	}
	if again.Activation != Armed {
		t.Fatalf("再读一次变成了 %q", again.Activation)
	}
}

func TestCachesDoNotPinDeadSessions(t *testing.T) {
	// 缓存的键是弱引用：一个用完就不再有人引用的会话不该因为这张表而留在内存里。
	// 清扫只在建新缓存那条路上做，所以下面必须再建一份缓存才看得到效果。
	agents := newStubAgents()
	service := newTestService(t, agents, newStubClock())
	func() {
		gone := newStubAgent(t, "session-gone", nil, nil)
		agents.put(gone)
		mustCreate(t, service, gone, "先建一个")
		agents.drop(gone.ID())
	}()

	collected := false
	for attempt := 0; attempt < 10 && !collected; attempt++ {
		runtime.GC()
		service.mutex.Lock()
		collected = true
		for key := range service.caches {
			if key.Value() != nil {
				collected = false
			}
		}
		service.mutex.Unlock()
	}
	if !collected {
		t.Skip("这台机器上那份会话还没被回收，清扫这一步试不出来")
	}

	live := newStubAgent(t, "session-live", nil, nil)
	agents.put(live)
	mustCreate(t, service, live, "再建一个")

	service.mutex.Lock()
	remaining := len(service.caches)
	service.mutex.Unlock()
	if remaining != 1 {
		t.Fatalf("表里还剩 %d 份缓存，那份死掉的会话本该在建新缓存时被扫掉", remaining)
	}
}

// ---- Install ----

func TestInstallSurfacesARegistrationFailure(t *testing.T) {
	agents := newStubAgents()
	agents.sessionStartErr = errors.New("挂不上")
	service := newTestService(t, agents, newStubClock())
	if _, err := service.Install(t.Context(), scope.NewRoot()); err == nil {
		t.Fatal("注册表拒了这次登记，Install 本该照实报出来")
	}
}

func TestInstallDisarmsOnEverySessionStart(t *testing.T) {
	// 这条边是「活化从不落盘」真正的兑现处：续会话、分叉、换驱动都走到它，于是
	// 一个回放出来的 active 目标绝不会自己动起来。
	service, owner, agents, _ := newSingleAgentService(t)
	stop, err := service.Install(t.Context(), scope.NewRoot())
	if err != nil {
		t.Fatalf("挂会话起跑边失败：%v", err)
	}
	created := mustCreate(t, service, owner, "写完这一段")

	agents.emitSessionStart(owner, agent.StartResume)
	view, err := service.Get(owner)
	if err != nil {
		t.Fatalf("读目标失败：%v", err)
	}
	if view.Activation != Disarmed {
		t.Fatalf("会话起跑之后活化是 %q，本该被打回 disarmed", view.Activation)
	}
	// 耐久那一侧一点都不该动：阶段、修订号、已用轮数全都留着。
	if view.Revision != created.Revision || view.Phase != PhaseActive {
		t.Fatalf("会话起跑动了耐久状态：%+v", view)
	}
	if len(owner.changes()) != 1 {
		t.Fatal("会话起跑一条改动都不该写")
	}

	if err := stop(t.Context()); err != nil {
		t.Fatalf("撤销那条边失败：%v", err)
	}
	if agents.stopped != 1 {
		t.Fatalf("退订被叫了 %d 次", agents.stopped)
	}
}

func TestInstallSurvivesASessionItCannotFold(t *testing.T) {
	// 这条边发在会话生命周期起跑时，那一刻没有人接得住错误。折不动就记一条警告
	// 走开——把这里变成 panic 会让一份坏日志直接掀翻整个装配。
	agents := newStubAgents()
	service := newQuietService(t, agents, newStubClock())
	if _, err := service.Install(t.Context(), scope.NewRoot()); err != nil {
		t.Fatalf("挂会话起跑边失败：%v", err)
	}
	broken := newStubAgent(t, "session-broken", nil, nil)
	agents.put(broken)
	broken.append(t, changeEvent(`{"kind":"goal/change","version":9}`))

	agents.emitSessionStart(broken, agent.StartStartup)

	service.mutex.Lock()
	cached := len(service.caches)
	service.mutex.Unlock()
	if cached != 0 {
		t.Fatalf("折不动的那条日志本该一份缓存都不留下，表里有 %d 份", cached)
	}
}

// ---- goal/changed ----

func TestOnChangedNeedsAnObserver(t *testing.T) {
	service, _, _, _ := newSingleAgentService(t)
	if _, err := service.OnChanged(t.Context(), scope.NewRoot(), nil); err == nil {
		t.Fatal("没有观察者本该登记不上")
	}
}

func TestOnChangedDeliversEveryOperationAndThenStops(t *testing.T) {
	service, owner, _, _ := newSingleAgentService(t)
	root := scope.NewRoot()
	var seen []Changed
	stop, err := service.OnChanged(t.Context(), root, func(who agent.Agent, change Changed) {
		if who != owner {
			t.Errorf("通告挂的是 %v，本该是发起改动的那个 agent", who)
		}
		seen = append(seen, change)
	})
	if err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	created := mustCreate(t, service, owner, "写完这一段")
	completed, err := service.Complete(owner, created.Ref)
	if err != nil {
		t.Fatalf("完成目标失败：%v", err)
	}
	tombstone, err := service.Clear(owner, completed.Ref)
	if err != nil {
		t.Fatalf("清掉目标失败：%v", err)
	}

	if len(seen) != 3 {
		t.Fatalf("收到 %d 条通告，本该是 3 条", len(seen))
	}
	if seen[0].Operation != OpCreate || seen[0].Ref != created.Ref || seen[0].Goal == nil {
		t.Fatalf("第一条通告是 %+v", seen[0])
	}
	if seen[1].Operation != OpComplete || seen[1].Goal == nil || seen[1].Goal.Phase != PhaseComplete {
		t.Fatalf("第二条通告是 %+v", seen[1])
	}
	// 墓碑那一条没有当前目标了：Goal 必须是 nil，而 Ref 是那块墓碑。
	if seen[2].Operation != OpClear || seen[2].Ref != tombstone || seen[2].Goal != nil {
		t.Fatalf("第三条通告是 %+v", seen[2])
	}

	if err := stop(t.Context()); err != nil {
		t.Fatalf("撤销登记失败：%v", err)
	}
	mustCreate(t, service, owner, "下一件")
	if len(seen) != 3 {
		t.Fatalf("撤销之后又收到了通告：%d 条", len(seen))
	}
}

func TestOnChangedIsScopedToTheOwnerChain(t *testing.T) {
	// 有身份的作用域只看得见它自己（或它子孙）那条链上的改动；兄弟之间互相看不见。
	parent := scopeOf(t, "parent", nil)
	stranger := scopeOf(t, "stranger", nil)
	owner := newStubAgent(t, "session-1", parent, nil)
	service := newTestService(t, newStubAgents(owner), newStubClock())

	var onChain, offChain, global int
	if _, err := service.OnChanged(t.Context(), parent, func(agent.Agent, Changed) { onChain++ }); err != nil {
		t.Fatalf("往父作用域上登记失败：%v", err)
	}
	if _, err := service.OnChanged(t.Context(), stranger, func(agent.Agent, Changed) { offChain++ }); err != nil {
		t.Fatalf("往旁人作用域上登记失败：%v", err)
	}
	if _, err := service.OnChanged(t.Context(), scope.NewRoot(), func(agent.Agent, Changed) { global++ }); err != nil {
		t.Fatalf("往全局层上登记失败：%v", err)
	}

	mustCreate(t, service, owner, "写完这一段")
	if onChain != 1 || global != 1 {
		t.Fatalf("父作用域收到 %d 条、全局层收到 %d 条，本该各 1 条", onChain, global)
	}
	if offChain != 0 {
		t.Fatalf("旁人那条作用域收到了 %d 条，本该一条都收不到", offChain)
	}
}

func TestOnChangedReachesOnlyGlobalWhenTheAgentHasNoScope(t *testing.T) {
	// 一个没有身份的载体：没有父链可走，只有全局层那些无标签的观察者收得到。
	base := newStubAgent(t, "session-1", nil, nil)
	owner := &stubAgent{id: base.id, log: base.log}
	service := newTestService(t, newStubAgents(owner), newStubClock())

	var global, scoped int
	if _, err := service.OnChanged(t.Context(), scope.NewRoot(), func(agent.Agent, Changed) { global++ }); err != nil {
		t.Fatalf("往全局层上登记失败：%v", err)
	}
	if _, err := service.OnChanged(t.Context(), scopeOf(t, "旁人", nil), func(agent.Agent, Changed) { scoped++ }); err != nil {
		t.Fatalf("往旁人作用域上登记失败：%v", err)
	}

	mustCreate(t, service, owner, "写完这一段")
	if global != 1 || scoped != 0 {
		t.Fatalf("全局层收到 %d 条、有身份那层收到 %d 条", global, scoped)
	}
}

func TestOnChangedContainsAPanickingObserver(t *testing.T) {
	// 改动已经落进日志了：一个观察者炸掉不该把它撤回去，也不该饿着排在它后面的同侪。
	owner := newStubAgent(t, "session-1", nil, nil)
	service := newQuietService(t, newStubAgents(owner), newStubClock())
	root := scope.NewRoot()

	if _, err := service.OnChanged(t.Context(), root, func(agent.Agent, Changed) {
		panic("炸了")
	}); err != nil {
		t.Fatalf("登记会炸的观察者失败：%v", err)
	}
	survived := 0
	if _, err := service.OnChanged(t.Context(), root, func(agent.Agent, Changed) {
		survived++
	}); err != nil {
		t.Fatalf("登记后一个观察者失败：%v", err)
	}

	view := mustCreate(t, service, owner, "写完这一段")
	if view.Revision != 1 {
		t.Fatalf("那次改动被观察者带下水了：%+v", view)
	}
	if survived != 1 {
		t.Fatalf("排在后面那个观察者被叫了 %d 次，本该是 1 次", survived)
	}
}
