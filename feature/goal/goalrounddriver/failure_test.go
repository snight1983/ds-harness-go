// 本文件的作用：把那些「协作者自己失败了」的路走一遍——目标服务的三种改动各自
// 报错时本包怎么收尾，以及那几条只有 agent 已经离场才走得到的分支。
//
// # 这些测试防的是什么错
//
//   - **把一次失败的改动当成成功**。[driver.pauseUnfinished] 停不住就必须退回去
//     打回未活化；少了那一步，一个停不下来的目标会带着自动权一直挂着。
//   - **在收尾的路上再抛一次**。这几条路本身就是在处理别的失败，它们只许记日志，
//     一条都不许把调用方带走。
//   - **忘了 agent 已经不在了这件事**。[driver.currentGoal] 在那种时候必须交回一份
//     空目标而不是去问服务——服务只会白报一条 GOAL_AGENT_NOT_LIVE。

package goalrounddriver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// errGoalService 是那台被套了一层的目标服务摆出来的失败。
var errGoalService = errors.New("测试：目标服务报错了")

func TestCurrentGoalIsEmptyOnceTheAgentIsGone(t *testing.T) {
	// agent 已经从注册表里摘掉了，本包必须当场交回一份空目标：这时候去问服务，
	// 换回来的只会是一条 GOAL_AGENT_NOT_LIVE，而那不是「出岔子」，是正常退场。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	live.agents.drop(live.owner.ID())

	view, err := drive.currentGoal()
	if err != nil {
		t.Fatalf("离场的 agent 上读目标本该安静地交回空：%v", err)
	}
	if view != nil {
		t.Fatalf("本该是空的，拿到 %#v", view)
	}
}

func TestDisarmLogsWhenTheServiceRefuses(t *testing.T) {
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driverWithGoals(failingGoals{Goals: live.goals, disarmErr: errGoalService})

	drive.disarm()

	// 打回未活化失败只记一条日志：这条路本来就是在收拾别的岔子，再抛一次没人接。
	if live.currentGoal().Activation != goal.Armed {
		t.Fatal("那次失败的调用不该真的改到活化")
	}
}

func TestPauseUnfinishedDisarmsWhenPauseFails(t *testing.T) {
	// 停不住就退回去交出自动权：一个既停不下来又还举着自动权的目标会一直被推。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driverWithGoals(failingGoals{Goals: live.goals, pauseErr: errGoalService})

	drive.pauseUnfinished()

	if got := live.currentGoal().Activation; got != goal.Disarmed {
		t.Fatalf("停失败之后活化是 %q，本该已经被打回 %q", got, goal.Disarmed)
	}
}

func TestFailToQueueLogsWhenBlockFails(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driverWithGoals(failingGoals{Goals: live.goals, blockErr: errGoalService})

	drive.failToQueue(view, 1, errDownstream)

	if got := live.currentGoal().Phase; got != goal.PhaseActive {
		t.Fatalf("那次失败的调用不该真的改到阶段，拿到 %q", got)
	}
}

func TestBlockRejectedLogsWhenBlockFails(t *testing.T) {
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driverWithGoals(failingGoals{Goals: live.goals, blockErr: errGoalService})

	decision := drive.blockRejected(
		goal.Source{GoalID: view.ID, Revision: view.Revision, Round: 1})

	// 停不住也照样把这个步骤挡下来：放它进去等于让一条已经被人拒过的提示词跑起来。
	if decision.Enter {
		t.Fatal("停不住不代表这个步骤就该放行")
	}
}

func TestQueueRoundDropsTheRoundOnceStopping(t *testing.T) {
	// 收摊途中排出去的一轮永远没人认领，也没人替它收尾。所以这一步必须整个作废。
	live := newHarness(t)
	view := live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.stopping = true

	drive.queueRound(view)

	if len(live.owner.queuedTurn()) != 0 {
		t.Fatal("收摊途中还往收件箱里排了一轮")
	}
	if drive.attempt != nil {
		t.Fatal("收摊途中还立了一份预定")
	}
}

func TestGoalAsideRejectsWhatItCannotRead(t *testing.T) {
	// kind 对得上但载荷读不回来的来源不算「目标那一层发的非轮次消息」。判成是的话，
	// 一条别人的消息会在步骤被拒时被悄悄丢掉，而不是放回收件箱。
	cases := map[string]llm.MessageSource{
		"载荷不是对象":    llm.UnknownSource{Kind: llm.SourceKind(goal.SourceKind), Raw: json.RawMessage(`[]`)},
		"kind 不是目标": llm.UnknownSource{Kind: "somebody-else", Raw: json.RawMessage(`{"kind":"goal","round":0}`)},
		"压根没有来源":    nil,
	}
	for what, source := range cases {
		t.Run(what, func(t *testing.T) {
			if goalAside(source) {
				t.Fatal("本该判成「不是」")
			}
		})
	}
}

func TestRestoreOtherClaimedSurvivesADuplicateIdentity(t *testing.T) {
	// 同一个身份放两遍会被真收件箱当场拒掉。这条路必须照样走完：它本来就是在收拾
	// 一次被拒的步骤，为了第二条放不进去就撂挑子，前面那些消息就再也回不了队。
	live := newHarness(t)
	drive := live.driver()
	duplicate := plainMessage("same identity twice")

	drive.restoreOtherClaimed([]llm.Message{duplicate, duplicate}, "nobody")

	if len(live.owner.queuedStep()) != 1 {
		t.Fatalf("队里有 %d 条，本该只放进去一条", len(live.owner.queuedStep()))
	}
}

func TestTurnEndEventsThatCannotBeReadAreIgnored(t *testing.T) {
	// 一条读不回来的 turn/end 必须安静地滑过去。当成「被取消了」处理的话，
	// 一条坏事件就能把一个好好的目标打回未活化。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()

	// 「没带收场理由」也走这一条：[sessionlog.TurnEndData] 那份 UnmarshalJSON 把缺了
	// reason 的负载直接判成读不回来，所以它到不了本包那道 data.Reason == nil 的门口。
	cases := map[string]json.RawMessage{
		"读不回来":   json.RawMessage(`not json`),
		"没带收场理由": json.RawMessage(`{"turn":1}`),
	}
	for what, data := range cases {
		t.Run(what, func(t *testing.T) {
			drive.onSessionEvent(sessionlog.Event{Type: sessionlog.EventTurnEnd, Data: data})
			if got := live.currentGoal().Activation; got != goal.Armed {
				t.Fatalf("一条读不出收场的回合结束把活化改成了 %q", got)
			}
		})
	}
}

func TestDriveOnceSkipsTheCheckpointWhenItIsNoLongerReady(t *testing.T) {
	// 落盘那段等待里 agent 又忙起来了：这一次驱动必须就地收手，而不是接着往下排。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	drive := live.driver()
	drive.needsCheckpoint = true
	live.sessions.beforeFlush = func() { live.owner.setStatus(agent.StatusRunning) }

	drive.driveOnce(context.Background())

	if live.sessions.flushed() != 1 {
		t.Fatalf("落了 %d 次盘，本该正好一次", live.sessions.flushed())
	}
	if len(live.owner.queuedTurn()) != 0 {
		t.Fatal("落盘之后条件已经不成立了，却还是排了一轮")
	}
}
