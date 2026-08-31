// 本文件的作用：把那台状态机钉在它每一道边界上——什么时候敢排下一轮、什么时候
// 认输、以及认输之后收成 pause 还是 blocked 还是只是打回未活化。
//
// # 这些测试防的是什么错
//
//   - **在一道边界上少问一句**。本包立身的那句话是「我预定的那一轮此刻还成立吗」，
//     而每一条 readyToDrive / validReservation 上的合取项都对应一次真实的抢跑。
//     少验一项，模型就会在某个局面下收到一条它已经不该收到的指令，而那种局面
//     一年也许只出现一次。
//   - **把进程内的意外写成耐久结论**。读不回目标、落盘失败、下游抛了——这些只该
//     打回未活化（目标留在 active，人 resume 一下就接着走）。写成 blocked 的话，
//     一次偶发故障就变成了日志里一句「它撞墙了」。反过来，撞轮数上限和被明确拒掉
//     **必须**写成 blocked。所以这里每一条认输的路都断言它收成了哪一种。
//   - **让 queueRound 先发消息后立预定**。收件箱那条 inserted 是同步回调的，次序
//     反过来会让本包把自己发的消息当成别人抢跑。那条用例专门接上观察者去看。
//   - **拒一个步骤的时候顺手吞掉别人的消息**。一个被拒的步骤里夹着别人的引导和
//     上下文，它们已经从收件箱里被取走了；不放回去就等于本包替别人丢了活儿。

package goalrounddriver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/goal/goal"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// errDownstream 是那些「下游抛了」的用例摆出来的失败。
var errDownstream = errors.New("测试：下游抛了")

// corruptGoalLog 往这条会话日志里塞一条读不回来的 goal/change。
//
// 之后每一次 [goal.Service.Get] 都会失败——本包那几条「读不回目标」的路只有这样
// 才走得到，而它们恰恰是最该验的：这类失败一律只准打回未活化。
func corruptGoalLog(t *testing.T, live *testAgent) {
	t.Helper()
	live.appendEvent(t, session.Event{Type: goal.EventChange, Data: json.RawMessage(`{"version":1}`)})
}

// admitRound 把某一轮的续推消息直接写进日志，于是 RoundsStarted 涨一格。
func admitRound(t *testing.T, live *testAgent, view *goal.View, round int) {
	t.Helper()
	live.appendEvent(t, userMessageEvent(t, roundMessage(t, view, round)))
}

// queued 是此刻排在 next-turn 上的那些消息。
func queued(live *testAgent) []llm.Message { return live.queuedTurn() }

// onlyQueuedText 断言 next-turn 上正好排着一条消息，交回它那段正文。
func onlyQueuedText(t *testing.T, live *testAgent) string {
	t.Helper()
	pending := queued(live)
	if len(pending) != 1 {
		t.Fatalf("next-turn 上排着 %d 条消息，本该正好一条", len(pending))
	}
	return textOf(t, pending[0].Content)
}

// ---- 排下一轮 ----

func TestDriveOnceQueuesTheNextRound(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driver()

	drive.driveOnce(context.Background())

	if got, want := onlyQueuedText(t, live.owner), textOf(t, RenderRoundPrompt(view, 1)); got != want {
		t.Fatalf("排出去的不是第一轮那条提示词：\n拿到：%q\n本该：%q", got, want)
	}
	if drive.attempt == nil || drive.attempt.round != 1 || drive.attempt.phase != phaseQueued {
		t.Fatalf("预定不对：%#v", drive.attempt)
	}
}

func TestDriveOnceReservesBeforeSendingTheMessage(t *testing.T) {
	// 预定必须在 Followup 之前立起来：inserted 是同步回调的，先发后立会让本包把
	// 自己那条消息当成别人抢跑，于是它立刻给自己让路。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	if _, err := live.agents.OnInboxInserted(t.Context(), live.scope,
		func(_ agent.Agent, message llm.Message) { drive.onInboxInserted(message) }); err != nil {
		t.Fatalf("装收件箱观察者失败：%v", err)
	}

	drive.driveOnce(context.Background())

	if drive.competingQueued {
		t.Fatal("本包把自己发出去的那条消息当成了别人抢跑")
	}
	if drive.attempt == nil || drive.attempt.stale {
		t.Fatalf("自己发的消息把自己的预定判成了过时：%#v", drive.attempt)
	}
}

func TestDriveOnceHoldsBackAtEveryGate(t *testing.T) {
	cases := map[string]func(t *testing.T, live *harness, drive *driver){
		"agent 正在跑": func(_ *testing.T, live *harness, _ *driver) {
			live.owner.setStatus(agent.StatusRunning)
		},
		"agent 已经离开注册表": func(_ *testing.T, live *harness, _ *driver) {
			live.agents.drop(live.owner.ID())
		},
		"别人的提示词排在前面": func(_ *testing.T, _ *harness, drive *driver) {
			drive.competingQueued = true
		},
		"这台驱动已经在收摊": func(_ *testing.T, _ *harness, drive *driver) {
			drive.stopping = true
		},
		"这条链已经被取消": func(_ *testing.T, _ *harness, drive *driver) {
			drive.cancel()
		},
		"目标已经停住": func(t *testing.T, live *harness, _ *driver) {
			if _, err := live.goals.Pause(live.owner, live.currentGoal().Ref); err != nil {
				t.Fatalf("停住目标失败：%v", err)
			}
		},
		"目标已经被打回未活化": func(t *testing.T, live *harness, _ *driver) {
			if _, err := live.goals.Disarm(live.owner); err != nil {
				t.Fatalf("打回未活化失败：%v", err)
			}
		},
		"读不回目标": func(t *testing.T, live *harness, _ *driver) {
			corruptGoalLog(t, live.owner)
		},
	}
	for what, setup := range cases {
		t.Run(what, func(t *testing.T) {
			live := newHarness(t)
			live.createGoal("ship the release", 3)
			drive := live.driver()
			setup(t, live, drive)

			drive.driveOnce(context.Background())

			if pending := queued(live.owner); len(pending) != 0 {
				t.Fatalf("本该按兵不动，却排了 %d 条消息", len(pending))
			}
		})
	}
}

func TestDriveOnceDoesNothingWithoutAGoal(t *testing.T) {
	live := newHarness(t)
	live.driver().driveOnce(context.Background())
	if pending := queued(live.owner); len(pending) != 0 {
		t.Fatalf("一个目标都没有，却排了 %d 条消息", len(pending))
	}
}

func TestDriveOnceBlocksWhenRoundsAreExhausted(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 1)
	admitRound(t, live.owner, view, 1)

	live.driver().driveOnce(context.Background())

	after := live.currentGoal()
	if after.Phase != goal.PhaseBlocked {
		t.Fatalf("轮数用完之后阶段是 %q，本该是 %q", after.Phase, goal.PhaseBlocked)
	}
	if after.BlockedReason == nil || after.BlockedReason.Code != "round-limit" {
		t.Fatalf("阻塞理由不对：%#v", after.BlockedReason)
	}
	if pending := queued(live.owner); len(pending) != 0 {
		t.Fatalf("轮数用完还排了 %d 条消息", len(pending))
	}
}

func TestBlockRoundsSurvivesARefusedBlock(t *testing.T) {
	// 目标已经不在 active 上了，那次 Block 会被目标服务拒掉。本包只该记一条日志，
	// 不该把这次失败升级成别的动作——它已经没有话语权了。
	live := newHarness(t)
	view := live.createGoal("ship the release", 1)
	if _, err := live.goals.Complete(live.owner, view.Ref); err != nil {
		t.Fatalf("完成目标失败：%v", err)
	}
	live.driver().blockRounds(view)
	if live.currentGoal().Phase != goal.PhaseComplete {
		t.Fatalf("一次被拒的 Block 改动了目标：%q", live.currentGoal().Phase)
	}
}

// ---- 落盘检查点 ----

func TestDriveOnceFlushesBeforeQueueing(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.onGoalChanged()

	drive.driveOnce(context.Background())

	if live.sessions.flushed() != 1 {
		t.Fatalf("落盘了 %d 次，本该正好一次", live.sessions.flushed())
	}
	if len(queued(live.owner)) != 1 {
		t.Fatal("落盘之后本该照常排下一轮")
	}
	if drive.needsCheckpoint {
		t.Fatal("那面检查点旗子本该被取走")
	}
}

func TestDriveOnceDisarmsWhenTheCheckpointFails(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	live.sessions.flushErr = errDownstream
	drive := live.driver()
	drive.onGoalChanged()

	drive.driveOnce(context.Background())

	after := live.currentGoal()
	if after.Activation != goal.Disarmed {
		t.Fatalf("落盘失败之后活化是 %q，本该是 %q", after.Activation, goal.Disarmed)
	}
	if after.Phase != goal.PhaseActive {
		t.Fatalf("落盘失败把阶段改成了 %q——一次进程内的意外不该写成耐久结论", after.Phase)
	}
	if len(queued(live.owner)) != 0 {
		t.Fatal("落盘失败之后不该再排下一轮")
	}
}

func TestDriveOnceYieldsToWhateverArrivedDuringTheCheckpoint(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.onGoalChanged()
	// 落盘 settle 的这段时间里又来了一次目标改动：那次改动该有它自己的检查点，
	// 不该被这一次顺手带过去。
	live.sessions.beforeFlush = func() { drive.onGoalChanged() }

	drive.driveOnce(context.Background())

	if len(queued(live.owner)) != 0 {
		t.Fatal("落盘途中又来了一次改动，本该让路")
	}
	if !drive.needsCheckpoint {
		t.Fatal("那次插进来的改动本该留下它自己的检查点旗子")
	}
}

func TestDriveOnceReleasesAnAdmittedAttemptFirst(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	if drive.attempt == nil {
		t.Fatal("第一次驱动本该立起一份预定")
	}

	// 第二次驱动走到的是那条「先把账清干净」的闸：它只交出预定，不立刻想下一轮。
	drive.driveOnce(context.Background())
	if drive.attempt != nil {
		t.Fatalf("那份预定本该被交出去：%#v", drive.attempt)
	}
	if !drive.needsCheckpoint {
		t.Fatal("交出预定之后本该记下「该落一次盘了」")
	}
	if len(queued(live.owner)) != 1 {
		t.Fatal("同一次驱动里不该既清账又排新的一轮")
	}
}

// ---- 排队失败那条收尾 ----

func TestFailToQueueBlocksTheGoal(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driver()

	drive.failToQueue(view, 1, errDownstream)

	after := live.currentGoal()
	if after.Phase != goal.PhaseBlocked {
		t.Fatalf("排不出去之后阶段是 %q，本该是 %q", after.Phase, goal.PhaseBlocked)
	}
	if after.BlockedReason == nil || after.BlockedReason.Code != "queue-failed" {
		t.Fatalf("阻塞理由不对：%#v", after.BlockedReason)
	}
}

func TestFailToQueueLeavesADifferentGoalAlone(t *testing.T) {
	// 这中间目标已经被人停住了。再往上盖一条 queue-failed，盖掉的是一条比它更
	// 权威的结论。
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	if _, err := live.goals.Pause(live.owner, view.Ref); err != nil {
		t.Fatalf("停住目标失败：%v", err)
	}

	live.driver().failToQueue(view, 1, errDownstream)

	if after := live.currentGoal(); after.Phase != goal.PhasePaused {
		t.Fatalf("一条迟到的 queue-failed 盖掉了别人的结论：%q", after.Phase)
	}
}

func TestFailToQueueStaysQuietWhenTheGoalCannotBeRead(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	corruptGoalLog(t, live.owner)
	// 读不回来就什么都不做：连它要盖的那个目标是不是还在都不知道。
	live.driver().failToQueue(view, 1, errDownstream)
}

// ---- 状态跃迁 ----

func TestOnStatusPausesAnUnfinishedRound(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())

	drive.onStatus(agent.StatusIdle)

	after := live.currentGoal()
	if after.Phase != goal.PhasePaused {
		t.Fatalf("一轮没走完的续推之后阶段是 %q，本该是 %q", after.Phase, goal.PhasePaused)
	}
	if drive.attempt != nil {
		t.Fatal("那份没走完的预定本该被撤掉")
	}
}

func TestOnStatusLeavesAdmittedRoundsAlone(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	drive.attempt.phase = phaseAdmitted

	drive.onStatus(agent.StatusIdle)

	if after := live.currentGoal(); after.Phase != goal.PhaseActive {
		t.Fatalf("一轮已经准入的续推被当成没走完：%q", after.Phase)
	}
}

func TestOnStatusIgnoresEverythingButIdle(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.competingQueued = true

	drive.onStatus(agent.StatusRunning)

	if !drive.competingQueued {
		t.Fatal("只有转回空闲才该清掉那面让路的旗子")
	}
}

func TestOnStatusClearsTheYieldFlag(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.competingQueued = true

	drive.onStatus(agent.StatusIdle)

	if drive.competingQueued {
		t.Fatal("转回空闲之后那面让路的旗子本该清掉")
	}
}

func TestPauseUnfinishedDisarmsWhenTheGoalCannotBeRead(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	corruptGoalLog(t, live.owner)

	// 读不回来时不该硬去 pause，只该记一条日志然后交出自动权。
	drive.onStatus(agent.StatusIdle)
}

func TestPauseUnfinishedDisarmsWhenThePauseIsRefused(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	// 阶段已经不在 active 上了，那次 Pause 会被拒。这时候本包必须退到打回未活化，
	// 而不是把这次失败放着不管——放着不管的话下一次驱动会原地再推一遍同一轮。
	if _, err := live.goals.Complete(live.owner, view.Ref); err != nil {
		t.Fatalf("完成目标失败：%v", err)
	}

	drive.onStatus(agent.StatusIdle)

	if after := live.currentGoal(); after.Phase != goal.PhaseComplete {
		t.Fatalf("一次被拒的 Pause 改动了目标：%q", after.Phase)
	}
}

// ---- 会话起跑与目标改动 ----

func TestOnSessionStartClearsEveryLocalTally(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	drive.competingQueued = true
	drive.needsCheckpoint = true

	drive.onSessionStart()

	if drive.attempt != nil || drive.competingQueued || drive.needsCheckpoint {
		t.Fatalf("会话起跑之后本该一笔账都不剩：%#v %v %v",
			drive.attempt, drive.competingQueued, drive.needsCheckpoint)
	}
}

func TestOnGoalChangedAsksForACheckpoint(t *testing.T) {
	live := newHarness(t)
	drive := live.driver()

	drive.onGoalChanged()

	if !drive.needsCheckpoint {
		t.Fatal("一次目标改动本该留下一面检查点旗子")
	}
	select {
	case <-drive.requests:
	default:
		t.Fatal("一次目标改动本该排一次驱动")
	}
}

// ---- 收件箱那三条边 ----

func TestOnInboxInsertedYieldsToAnotherPrompt(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())

	intruder := plainMessage("actually, do this instead")
	if err := live.owner.appendInbox(agent.NextTurn, intruder); err != nil {
		t.Fatalf("往收件箱里追消息失败：%v", err)
	}
	drive.onInboxInserted(intruder)

	if !drive.competingQueued {
		t.Fatal("人自己发的话本该让本包让路")
	}
	if !drive.attempt.stale {
		t.Fatal("还排着队的那份预定本该被判成过时")
	}
}

func TestOnInboxInsertedIgnoresMessagesOutsideNextTurn(t *testing.T) {
	live := newHarness(t)
	drive := live.driver()
	// next-step 上的东西是引导和上下文，它们不跟本包抢回合。
	steering := plainMessage("keep going")
	if err := live.owner.appendInbox(agent.NextStep, steering); err != nil {
		t.Fatalf("往收件箱里追消息失败：%v", err)
	}

	drive.onInboxInserted(steering)

	if drive.competingQueued {
		t.Fatal("一条 next-step 上的引导本不该让本包让路")
	}
}

func TestOnInboxClaimedAndDiscardedMarkTheAttempt(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)

	t.Run("认领", func(t *testing.T) {
		drive := live.driver()
		drive.driveOnce(context.Background())
		mine := queued(live.owner)[0]
		drive.onInboxClaimed(mine)
		if drive.attempt.phase != phaseClaimed {
			t.Fatalf("认领之后阶段是 %q，本该是 %q", drive.attempt.phase, phaseClaimed)
		}
		drive.onInboxDiscarded(mine)
		if !drive.attempt.cancelled {
			t.Fatal("丢弃之后本该记上取消")
		}
	})

	t.Run("别人的消息", func(t *testing.T) {
		drive := live.driver()
		drive.driveOnce(context.Background())
		other := plainMessage("not mine")
		drive.onInboxClaimed(other)
		drive.onInboxDiscarded(other)
		if drive.attempt.phase != phaseQueued || drive.attempt.cancelled {
			t.Fatalf("别人的消息动了本包的预定：%#v", drive.attempt)
		}
	})

	t.Run("手里没有预定", func(t *testing.T) {
		drive := live.driver()
		drive.onInboxClaimed(plainMessage("nobody home"))
		drive.onInboxDiscarded(plainMessage("nobody home"))
	})
}

// ---- 日志广播 ----

func TestOnSessionEventAdmitsTheRound(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	mine := queued(live.owner)[0]

	drive.onSessionEvent(userMessageEvent(t, mine))

	if drive.attempt.phase != phaseAdmitted {
		t.Fatalf("落进日志之后阶段是 %q，本该是 %q", drive.attempt.phase, phaseAdmitted)
	}
}

func TestOnSessionEventIgnoresWhatIsNotItsOwn(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())

	events := []session.Event{
		userMessageEvent(t, plainMessage("somebody else")),
		{Type: session.EventUserMessage, Data: json.RawMessage(`not json`)},
		{Type: session.EventAssistantChunk, Data: json.RawMessage(`{}`)},
		{Type: session.EventTurnEnd, Data: json.RawMessage(`not json`)},
		turnEndEvent(t, session.CompletedTurnEnd{}),
	}
	for _, event := range events {
		drive.onSessionEvent(event)
	}

	if drive.attempt.phase != phaseQueued || drive.attempt.cancelled {
		t.Fatalf("不相干的事件动了本包的预定：%#v", drive.attempt)
	}
	if live.currentGoal().Activation != goal.Armed {
		t.Fatal("不相干的事件把目标打回了未活化")
	}
}

func TestOnTurnEndDisarmsOnMaxTokens(t *testing.T) {
	// 模型自己没说完，再推一轮多半还是撞同一堵墙，而每一轮都要真花钱。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())

	drive.onSessionEvent(turnEndEvent(t, session.MaxTokensTurnEnd{}))

	after := live.currentGoal()
	if after.Activation != goal.Disarmed {
		t.Fatalf("撞上限之后活化是 %q，本该是 %q", after.Activation, goal.Disarmed)
	}
	if after.Phase != goal.PhaseActive {
		t.Fatalf("撞上限把阶段改成了 %q——那是人下次来该看见还能接着走的", after.Phase)
	}
}

func TestOnTurnEndMarksAnInflightRoundCancelled(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	drive.attempt.phase = phaseAdmitted

	drive.onSessionEvent(turnEndEvent(t, session.AbortedTurnEnd{Reason: session.ParentCancel{}}))

	if !drive.attempt.cancelled {
		t.Fatal("本包那一轮被取消了，本该记上号")
	}
	if live.currentGoal().Activation != goal.Armed {
		t.Fatal("取消的收尾该等到转空闲时做，不该当场打回未活化")
	}
}

func TestOnTurnEndDisarmsWhenTheCancelWasNotItsOwn(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()

	drive.onSessionEvent(turnEndEvent(t, session.AbortedTurnEnd{Reason: session.ParentCancel{}}))

	if live.currentGoal().Activation != goal.Disarmed {
		t.Fatal("外面有人按了停，本包本该交出自动权")
	}
}

// ---- 前置步骤那道闸 ----

// enterNext 是那个「保留机器本来的提议」的下游。
func enterNext(messages []llm.Message) func(context.Context) (agent.PreStepDecision, error) {
	return func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep(messages), nil
	}
}

// claimedRound 排一轮出去、把它认领走，交回那台驱动和那条消息。
func claimedRound(t *testing.T, live *harness) (*driver, llm.Message) {
	t.Helper()
	drive := live.driver()
	drive.driveOnce(context.Background())
	claimed, err := live.owner.claimInbox(agent.NextTurn, 1)
	if err != nil {
		t.Fatalf("认领收件箱失败：%v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("认领到 %d 条消息，本该正好一条", len(claimed))
	}
	drive.onInboxClaimed(claimed[0])
	return drive, claimed[0]
}

func TestOnPreStepLetsUnrelatedStepsThrough(t *testing.T) {
	live := newHarness(t)
	drive := live.driver()
	messages := []llm.Message{plainMessage("just a human")}

	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: messages}, enterNext(messages))

	if err != nil || !decision.Enter {
		t.Fatalf("一批没有本包东西的消息本该原样放行：%v %#v", err, decision)
	}
}

func TestOnPreStepAdmitsAValidReservation(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)
	messages := []llm.Message{mine}

	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: messages}, enterNext(messages))

	if err != nil || !decision.Enter {
		t.Fatalf("一份成立的预定本该放行：%v %#v", err, decision)
	}
	if drive.attempt == nil {
		t.Fatal("放行之后那份预定本该还在——它要等落进日志才算准入")
	}
}

func TestOnPreStepRejectsReservationsThatNoLongerHold(t *testing.T) {
	cases := map[string]func(t *testing.T, live *harness, drive *driver){
		"预定被判成过时": func(_ *testing.T, _ *harness, drive *driver) {
			drive.attempt.stale = true
		},
		"根本没有预定": func(_ *testing.T, _ *harness, drive *driver) {
			drive.attempt = nil
		},
		"预定还停在排队上": func(_ *testing.T, _ *harness, drive *driver) {
			drive.attempt.phase = phaseQueued
		},
		"预定说的是另一轮": func(_ *testing.T, _ *harness, drive *driver) {
			drive.attempt.round = 9
		},
		"目标已经被停住": func(t *testing.T, live *harness, _ *driver) {
			if _, err := live.goals.Pause(live.owner, live.currentGoal().Ref); err != nil {
				t.Fatalf("停住目标失败：%v", err)
			}
		},
		"目标已经换了一个修订": func(t *testing.T, live *harness, _ *driver) {
			objective := "something else entirely"
			if _, err := live.goals.Edit(live.owner, live.currentGoal().Ref,
				goal.EditRequest{Objective: &objective}); err != nil {
				t.Fatalf("改目标失败：%v", err)
			}
		},
		"读不回目标": func(t *testing.T, live *harness, _ *driver) {
			corruptGoalLog(t, live.owner)
		},
		"这台驱动已经在收摊": func(_ *testing.T, _ *harness, drive *driver) {
			drive.stopping = true
		},
	}
	for what, setup := range cases {
		t.Run(what, func(t *testing.T) {
			live := newHarness(t)
			live.createGoal("ship the release", 3)
			drive, mine := claimedRound(t, live)
			setup(t, live, drive)
			messages := []llm.Message{mine}

			decision, err := drive.onPreStep(t.Context(),
				agent.PreStep{Agent: live.owner, Messages: messages}, enterNext(messages))

			if err != nil {
				t.Fatalf("这道闸只该关门，不该报错：%v", err)
			}
			if decision.Enter {
				t.Fatal("一份不成立的预定本该被挡在门外")
			}
		})
	}
}

func TestOnPreStepRestoresTheOtherClaimedMessages(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())
	steering := plainMessage("look at the logs first")
	if err := live.owner.appendInbox(agent.NextStep, steering); err != nil {
		t.Fatalf("往收件箱里追引导失败：%v", err)
	}
	claimed, err := live.owner.claimInbox(agent.NextTurn, 1)
	if err != nil {
		t.Fatalf("认领收件箱失败：%v", err)
	}
	drive.attempt.stale = true

	zeroRound, err := goal.Source{GoalID: view.ID, Revision: view.Revision, Round: 0}.MessageSource()
	if err != nil {
		t.Fatalf("包目标来源失败：%v", err)
	}
	wrapUp := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "wrap it up"}}, zeroRound)
	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: append(claimed, wrapUp)},
		enterNext(claimed))

	if err != nil || decision.Enter {
		t.Fatalf("过时的预定本该把整个步骤挡下来：%v %#v", err, decision)
	}
	restored := live.owner.queuedStep()
	if len(restored) != 1 || restored[0].ID != steering.ID {
		t.Fatalf("别人那条引导没被放回去：%#v", restored)
	}
}

func TestRestoreOtherClaimedSkipsWhatIsStillQueued(t *testing.T) {
	// 已经在队里的消息不能再放一次：同一个身份在收件箱里出现两次会被认领两次，
	// 而 [agent.Inbox] 本身也会当场拒掉那次改动。
	live := newHarness(t)
	drive := live.driver()
	still := plainMessage("still waiting")
	if err := live.owner.appendInbox(agent.NextTurn, still); err != nil {
		t.Fatalf("往收件箱里追消息失败：%v", err)
	}

	drive.restoreOtherClaimed([]llm.Message{still}, "somebody-else")

	if len(live.owner.queuedStep()) != 0 {
		t.Fatal("一条还排在队里的消息被又放了一遍")
	}
}

func TestOnPreStepBlocksWhenDownstreamRejects(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)

	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: []llm.Message{mine}},
		func(context.Context) (agent.PreStepDecision, error) { return agent.RejectStep(), nil })

	if err != nil || decision.Enter {
		t.Fatalf("下游拒了，本包也该拒：%v %#v", err, decision)
	}
	after := live.currentGoal()
	if after.Phase != goal.PhaseBlocked {
		t.Fatalf("被明确拒掉之后阶段是 %q，本该是 %q", after.Phase, goal.PhaseBlocked)
	}
	if after.BlockedReason == nil || after.BlockedReason.Code != "prompt-rejected" {
		t.Fatalf("阻塞理由不对：%#v", after.BlockedReason)
	}
}

func TestBlockRejectedStaysQuietWhenTheGoalMovedOn(t *testing.T) {
	cases := map[string]func(t *testing.T, live *harness){
		"目标已经完成": func(t *testing.T, live *harness) {
			if _, err := live.goals.Complete(live.owner, live.currentGoal().Ref); err != nil {
				t.Fatalf("完成目标失败：%v", err)
			}
		},
		"读不回目标": func(t *testing.T, live *harness) { corruptGoalLog(t, live.owner) },
	}
	for what, setup := range cases {
		t.Run(what, func(t *testing.T) {
			live := newHarness(t)
			view := live.createGoal("ship the release", 3)
			drive := live.driver()
			setup(t, live)

			decision := drive.blockRejected(goal.Source{
				GoalID: view.ID, Revision: view.Revision, Round: 1,
			})

			if decision.Enter {
				t.Fatal("被拒的步骤永远进不去")
			}
		})
	}
}

func TestOnPreStepPropagatesDownstreamErrors(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)

	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: []llm.Message{mine}},
		func(context.Context) (agent.PreStepDecision, error) { return agent.RejectStep(), errDownstream })

	if !errors.Is(err, errDownstream) {
		t.Fatalf("下游那个错本该原样带上去：%v", err)
	}
	if decision.Enter {
		t.Fatal("下游抛了，这个步骤进不去")
	}
	if drive.attempt != nil {
		t.Fatal("整个步骤提议作废了，那份预定本该撤掉好让下一次驱动重排这一轮")
	}
	if live.currentGoal().Phase != goal.PhaseActive {
		t.Fatal("下游抛了一次，不该被写成一条耐久结论")
	}
}

func TestOnPreStepKeepsTheReservationWhenTheTurnWasCancelled(t *testing.T) {
	// 这个回合是被取消的，不是下游不让跑。撤预定的收尾归 turn/end 和转空闲那两条边，
	// 在这里抢着撤会跟它们打架。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)
	ctx, cancel := context.WithCancel(context.Background())

	decision, err := drive.onPreStep(ctx,
		agent.PreStep{Agent: live.owner, Messages: []llm.Message{mine}},
		func(context.Context) (agent.PreStepDecision, error) {
			cancel()
			return agent.RejectStep(), errDownstream
		})

	if !errors.Is(err, errDownstream) || decision.Enter {
		t.Fatalf("被取消的步骤本该带着那个错拒掉：%v %#v", err, decision)
	}
	if drive.attempt == nil {
		t.Fatal("一次取消不该把预定撤掉")
	}
}

func TestOnPreStepReturnsTheDecisionWhenTheTurnWasCancelledAfterNext(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)
	steering := plainMessage("look at the logs first")
	ctx, cancel := context.WithCancel(context.Background())

	messages := []llm.Message{mine, steering}
	decision, err := drive.onPreStep(ctx,
		agent.PreStep{Agent: live.owner, Messages: messages},
		func(context.Context) (agent.PreStepDecision, error) {
			cancel()
			return agent.EnterStep(messages), nil
		})

	if err != nil || !decision.Enter {
		t.Fatalf("下游那个决定本该原样交回去：%v %#v", err, decision)
	}
	restored := live.owner.queuedStep()
	if len(restored) != 1 || restored[0].ID != steering.ID {
		t.Fatalf("这个回合已经没了，别人那条引导本该被放回去：%#v", restored)
	}
}

func TestOnPreStepRevalidatesAfterDownstream(t *testing.T) {
	// 跑 next 的这段时间里，下游任何一个人都可能改了目标。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive, mine := claimedRound(t, live)
	messages := []llm.Message{mine}

	decision, err := drive.onPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: messages},
		func(context.Context) (agent.PreStepDecision, error) {
			if _, failure := live.goals.Pause(live.owner, live.currentGoal().Ref); failure != nil {
				t.Fatalf("停住目标失败：%v", failure)
			}
			return agent.EnterStep(messages), nil
		})

	if err != nil {
		t.Fatalf("这道闸只该关门，不该报错：%v", err)
	}
	if decision.Enter {
		t.Fatal("目标在下游那段时间里被停住了，这一轮不该再进去")
	}
	if drive.attempt != nil {
		t.Fatal("那份已经不成立的预定本该撤掉")
	}
}

func TestDropReservationLeavesANewerRoundAlone(t *testing.T) {
	// 一次迟到的拒绝不该把别人刚立起来的那份新预定顺手抹掉。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.driveOnce(context.Background())

	drive.dropReservation(goal.Source{GoalID: drive.attempt.goalID, Revision: 1, Round: 9})

	if drive.attempt == nil {
		t.Fatal("一次说的是别的轮次的撤销把当前预定抹掉了")
	}
}

// ---- 收摊与协程 ----

func TestStopDisarmsAndCancelsAnInflightRound(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.start()
	drive.driveOnce(context.Background())
	live.owner.setStatus(agent.StatusRunning)

	drive.stop(context.Background())

	if live.currentGoal().Activation != goal.Disarmed {
		t.Fatal("收摊之后本该交出自动权")
	}
	if live.owner.canceled() != 1 {
		t.Fatalf("掐了 %d 次，本该正好一次——没人再来给那个回合收尾了", live.owner.canceled())
	}
	if !drive.stopping {
		t.Fatal("收摊之后那面旗子本该立着")
	}
}

func TestStopIsIdempotentAndSurvivesAFailedWait(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	live.owner.idleErr = errDownstream
	drive := live.driver()
	drive.driveOnce(context.Background())
	live.owner.setStatus(agent.StatusRunning)

	drive.stop(context.Background())
	drive.stop(context.Background())

	if live.owner.canceled() != 1 {
		t.Fatalf("收了两次摊却掐了 %d 次", live.owner.canceled())
	}
}

func TestStopLeavesARunningAgentAloneWithoutAReservation(t *testing.T) {
	// 手里没有预定就没什么好掐的：那个回合是别人的。
	live := newHarness(t)
	live.owner.setStatus(agent.StatusRunning)
	drive := live.driver()

	drive.stop(context.Background())

	if live.owner.canceled() != 0 {
		t.Fatal("手里没有预定却去掐了别人的回合")
	}
}

func TestDriverLoopQueuesWhatWasRequested(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.start()
	t.Cleanup(func() { drive.stop(context.Background()) })

	drive.requestDrive()

	waitFor(t, "第一轮排进收件箱", func() bool { return len(queued(live.owner)) == 1 })
}

func TestStartAndRequestDriveAreNoopsAfterStop(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.stop(context.Background())

	drive.start()
	drive.requestDrive()

	select {
	case <-drive.requests:
		t.Fatal("收摊之后还排了一次驱动")
	default:
	}
	if drive.started {
		t.Fatal("收摊之后还把协程开起来了")
	}
}

func TestStartIsIdempotent(t *testing.T) {
	live := newHarness(t)
	drive := live.driver()
	t.Cleanup(func() { drive.stop(context.Background()) })
	drive.start()
	drive.start()
}

func TestOwnsOnlyItsOwnSession(t *testing.T) {
	live := newHarness(t)
	stranger := newTestAgent(t, "session-2", live.scope, live.agents)
	drive := live.driver()

	if !drive.owns(live.owner.Session()) {
		t.Fatal("本包认不出自己那段会话")
	}
	if drive.owns(stranger.Session()) {
		t.Fatal("本包把别人那段会话认成了自己的")
	}
	live.agents.drop(live.owner.ID())
	if drive.owns(live.owner.Session()) {
		t.Fatal("agent 已经离开注册表，本包还认着那段会话")
	}
}

func TestDriverIgnoresASupersededInstance(t *testing.T) {
	// 同一段会话被重新开起来时标识还在，实例换了。这台驱动必须闭嘴——它继续说话
	// 只会往一段不归它管的会话里写东西。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	live.agents.drop(live.owner.ID())
	newTestAgent(t, "session-1", live.scope, live.agents)

	drive.driveOnce(context.Background())

	if len(queued(live.owner)) != 0 {
		t.Fatal("一个已经被顶掉的实例还在往会话里写东西")
	}
}

// waitFor 等一个条件成立，超时就当场 Fatal。
//
// 只有那几条真的要跑驱动协程的用例用得着它。轮询而不是等一个 channel：本包没有
// 「这一轮排完了」的对外信号，加一个只为测试的信号会让生产代码多出一条没人用的路。
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等不到：%s", what)
}
