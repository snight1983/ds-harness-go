// 本文件的作用：把「装上去」和「摘下来」这两件事钉住——装的那一刻先把在场的每一个
// agent 打回未活化、十一条观察者的接线、其中一条装不上时的反序退绕，以及摘的时候
// 驱动必须先于观察者收摊。
//
// # 这些测试防的是什么错
//
//   - **从上一个生产方那里继承一份看不见的自动授权**。装的时候不扫在场的 agent，
//     换一次驱动实现就等于凭空替人批准了一批还点着的目标接着自动跑。
//   - **半装上去**。第七条观察者装不上就交回错误，可前六条还挂在别人的注册表上——
//     那六条会一直对着一台根本没建起来的安装说话。
//   - **为一条会话广播凭空建一台驱动**。仓库里躺着的会话不一定都有 agent 在驱动，
//     比如一段只是被读出来看看的历史。
//   - **收摊之后还给新来的 agent 建活驱动**。摘下来的那一刻起本包就不该再管任何人。

package goalrounddriver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/goal/goal"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// installWith 用一份改过的配置装一次，交回那个摘下来的函数。
func installWith(t *testing.T, live *harness, config Config) func(context.Context) error {
	t.Helper()
	stop, err := Install(context.Background(), live.scope, config)
	if err != nil {
		t.Fatalf("装续推驱动失败：%v", err)
	}
	t.Cleanup(func() { _ = stop(context.Background()) })
	return stop
}

func TestInstallRequiresEveryCollaborator(t *testing.T) {
	live := newHarness(t)
	full := live.config()
	cases := map[string]struct {
		owner  *scope.Scope
		config Config
	}{
		"少了作用域":        {nil, full},
		"少了 agent 注册表": {live.scope, Config{Goals: full.Goals, Sessions: full.Sessions}},
		"少了目标服务":       {live.scope, Config{Agents: full.Agents, Sessions: full.Sessions}},
		"少了会话仓库":       {live.scope, Config{Agents: full.Agents, Goals: full.Goals}},
	}
	for what, each := range cases {
		t.Run(what, func(t *testing.T) {
			stop, err := Install(context.Background(), each.owner, each.config)
			if err == nil {
				t.Fatal("本该报错，却装上去了")
			}
			if stop != nil {
				t.Fatal("报错的那次安装不该交回一个摘除函数")
			}
			if !strings.Contains(err.Error(), "goalrounddriver:") {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestInstallDisarmsEveryAgentAlreadyOnStage(t *testing.T) {
	// 这是本包的立身之本：一台生命周期驱动绝不从上一个生产方那里继承自动授权。
	live := newHarness(t)
	live.createGoal("ship the release", 3)
	if live.currentGoal().Activation != goal.Armed {
		t.Fatal("刚建出来的目标本该是活化的")
	}

	live.install()

	if got := live.currentGoal().Activation; got != goal.Disarmed {
		t.Fatalf("装上去之后活化是 %q，本该已经被打回 %q", got, goal.Disarmed)
	}
}

func TestInstallUsesTheDefaultLoggerWhenNoneIsGiven(t *testing.T) {
	live := newHarness(t)
	config := live.config()
	config.Logger = nil
	installWith(t, live, config)
}

func TestInstallWiresEveryObserver(t *testing.T) {
	live := newHarness(t)
	live.install()

	// 十一条里有十条挂在 agent 注册表上，剩下那条会话事件挂在会话面上。
	counts := map[string]int{
		"created":         len(live.agents.created),
		"disposed":        len(live.agents.disposed),
		"status":          len(live.agents.status),
		"session-start":   len(live.agents.sessionStart),
		"error":           len(live.agents.errors),
		"inbox-inserted":  len(live.agents.inboxInserted),
		"inbox-claimed":   len(live.agents.inboxClaimed),
		"inbox-discarded": len(live.agents.inboxDiscarded),
		"pre-step":        len(live.agents.preStep),
		"session-event":   len(live.sessions.observers),
	}
	for what, count := range counts {
		if count != 1 {
			t.Fatalf("%s 观察者装了 %d 条，本该正好一条", what, count)
		}
	}
	if len(live.agents.errors) != 1 {
		t.Fatal("出错观察者没装上")
	}
}

func TestInstallUnwindsWhenOneObserverCannotBeAttached(t *testing.T) {
	labels := []string{
		"error", "created", "disposed", "session-start", "status",
		"inbox-inserted", "inbox-claimed", "inbox-discarded", "pre-step",
	}
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			live := newHarness(t)
			live.agents.failOn = label
			stop, err := Install(context.Background(), live.scope, live.config())
			if err == nil {
				t.Fatal("本该报错，却装上去了")
			}
			if stop != nil {
				t.Fatal("报错的那次安装不该交回一个摘除函数")
			}
			if !errors.Is(err, errTestRegister) {
				t.Fatalf("报的是 %v，本该裹着那次登记失败", err)
			}
			// 已经装上的那几条必须全部撤掉：留一条在别人的注册表上，
			// 它会一直对着一台根本没建起来的安装说话。
			attached := len(live.agents.created) + len(live.agents.disposed) +
				len(live.agents.status) + len(live.agents.sessionStart) +
				len(live.agents.errors) + len(live.agents.inboxInserted) +
				len(live.agents.inboxClaimed) + len(live.agents.inboxDiscarded) +
				len(live.agents.preStep) + len(live.sessions.observers)
			if live.agents.unsubscribed()+live.sessions.stopped != attached {
				t.Fatalf("装上了 %d 条，只撤掉 %d 条",
					attached, live.agents.unsubscribed()+live.sessions.stopped)
			}
		})
	}
}

func TestInstallUnwindsWhenTheSessionObserverCannotBeAttached(t *testing.T) {
	live := newHarness(t)
	live.sessions.failOnEvent = true
	stop, err := Install(context.Background(), live.scope, live.config())
	if err == nil {
		t.Fatal("本该报错，却装上去了")
	}
	if stop != nil {
		t.Fatal("报错的那次安装不该交回一个摘除函数")
	}
	if !strings.Contains(err.Error(), "会话事件") {
		t.Fatalf("报的是 %v，本该说清是哪一条装不上", err)
	}
	// 会话事件排在第十位，它前面有九条，其中八条挂在 agent 注册表上（第九条是目标
	// 改动，挂在目标服务上，这张假注册表数不到）。这八条必须已经按反序撤干净。
	if live.agents.unsubscribed() != 8 {
		t.Fatalf("撤掉了 %d 条 agent 观察者，本该是八条", live.agents.unsubscribed())
	}
}

func TestInstallReportsWhatEachStopFunctionSaid(t *testing.T) {
	live := newHarness(t)
	stop := live.install()
	live.agents.stopErr = errTestRegister

	if err := stop(context.Background()); !errors.Is(err, errTestRegister) {
		t.Fatalf("摘下来时那几条退订报的错本该原样带出来，拿到 %v", err)
	}
}

// ---- 那几条边上的派发 ----

func TestObserversBuildADriverForEveryAgent(t *testing.T) {
	live := newHarness(t)
	live.install()

	// 创建那条边只建驱动，不动目标：新 agent 身上还没有目标可打回。
	fresh := newTestAgent(t, "session-2", live.scope, live.agents)
	live.agents.emitCreated(t, fresh)

	// 出错那条边把这个 agent 打回未活化。
	live.createGoal("ship the release", 3)
	live.agents.emitError(agent.TurnError{Agent: live.owner, Err: errTestRegister})
	if got := live.currentGoal().Activation; got != goal.Disarmed {
		t.Fatalf("一次回合失败之后活化是 %q，本该已经被打回", got)
	}
}

func TestDisposedStopsAndForgetsTheDriver(t *testing.T) {
	live := newHarness(t)
	live.install()
	live.agents.emitStatus(live.owner, agent.StatusIdle)

	live.agents.drop(live.owner.ID())
	live.agents.emitDisposed(live.owner)

	// 摘掉之后那条会话广播不该再落到任何驱动身上。
	live.sessions.emitEvent(live.owner.log, turnEndEvent(t, session.AbortedTurnEnd{Reason: session.ParentCancel{}}))
}

func TestSessionEventsOnlyReachTheOwningDriver(t *testing.T) {
	live := newHarness(t)
	live.install()

	// 一段没有 agent 在驱动的会话：注册表里查不到，这条广播必须原地消失。
	orphan := newTestAgent(t, "session-orphan", live.scope, newTestAgents())
	live.sessions.emitEvent(orphan.log, turnEndEvent(t, session.AbortedTurnEnd{Reason: session.ParentCancel{}}))

	// 一个在注册表里但本包还没为它建过驱动的 agent：也不该为一条广播凭空建一台。
	stranger := newTestAgent(t, "session-stranger", live.scope, live.agents)
	live.sessions.emitEvent(stranger.log, turnEndEvent(t, session.AbortedTurnEnd{Reason: session.ParentCancel{}}))

	// 自己那一段上的广播才落到驱动身上：一次 max-tokens 把这个目标打回未活化。
	live.createGoal("ship the release", 3)
	live.sessions.emitEvent(live.owner.log, turnEndEvent(t, session.MaxTokensTurnEnd{}))
	if got := live.currentGoal().Activation; got != goal.Disarmed {
		t.Fatalf("自己那一段上的 max-tokens 之后活化是 %q，本该已经被打回", got)
	}
}

func TestSessionEventsAreIgnoredForASupersededSession(t *testing.T) {
	// 同一个 agent 换了一段会话之后，旧那一段上的广播不归这台驱动管：那台驱动记着的
	// 是它开工时那一段。照单收下的话，一条来自上一段历史的 max-tokens 会把当前这个
	// 还好好的目标打回未活化。
	live := newHarness(t)
	live.install()
	live.createGoal("ship the release", 3)
	// 装的时候那次扫台已经建过驱动了，所以这条广播查得到驱动，只是会话对不上。
	superseded := sessionWithID(t, live.owner.ID())

	live.sessions.emitEvent(superseded, turnEndEvent(t, session.MaxTokensTurnEnd{}))

	if got := live.currentGoal().Activation; got != goal.Armed {
		t.Fatalf("上一段会话里的回合结束把活化改成了 %q", got)
	}
}

func TestPreStepIsDelegatedToTheDriver(t *testing.T) {
	live := newHarness(t)
	live.install()

	// 一批不含本包续推的消息一路穿过去：这道闸只管自己那条提示词。
	messages := []llm.Message{plainMessage("do the thing")}
	decision, err := live.agents.emitPreStep(t.Context(),
		agent.PreStep{Agent: live.owner, Messages: messages}, enterNext(messages))
	if err != nil {
		t.Fatalf("这道闸本该原样放行：%v", err)
	}
	if !decision.Enter {
		t.Fatal("一批跟本包无关的消息被挡下来了")
	}
}

func TestInboxEdgesReachTheDriver(t *testing.T) {
	live := newHarness(t)
	live.install()

	// 别人的提示词进队：驱动记下「有人排在前面」，于是这一次驱动让路。
	steering := plainMessage("look at the logs first")
	if err := live.owner.appendInbox(agent.NextTurn, steering); err != nil {
		t.Fatalf("往收件箱里追消息失败：%v", err)
	}
	claimed, err := live.owner.claimInbox(agent.NextTurn, 1)
	if err != nil {
		t.Fatalf("认领收件箱失败：%v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("认领到 %d 条，本该正好一条", len(claimed))
	}
	live.agents.emitInboxDiscarded(live.owner, steering)
}

func TestSessionStartReachesTheDriver(t *testing.T) {
	live := newHarness(t)
	live.install()
	live.agents.emitSessionStart(live.owner, agent.StartResume)
}

// ---- 收摊 ----

func TestDisposeHandsOutInertDriversAfterwards(t *testing.T) {
	// 摘下来之后新来的 agent 一律拿到一台立着 stopping 的空壳：调用点全是观察者，
	// 它们没有一条能处理 nil 的路。
	live := newHarness(t)
	stop := live.install()
	if err := stop(context.Background()); err != nil {
		t.Fatalf("摘下来失败：%v", err)
	}

	fresh := newTestAgent(t, "session-late", live.scope, live.agents)
	live.agents.emitCreated(t, fresh)
	live.agents.emitStatus(fresh, agent.StatusIdle)

	// 那台空壳什么都不做：这个 agent 的收件箱里不该冒出任何东西。
	if len(fresh.queuedTurn()) != 0 {
		t.Fatal("收摊之后还给新来的 agent 排了一轮")
	}
}

func TestDisposeIsIdempotent(t *testing.T) {
	live := newHarness(t)
	stop := live.install()
	if err := stop(context.Background()); err != nil {
		t.Fatalf("第一次摘下来失败：%v", err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatalf("第二次摘下来失败：%v", err)
	}
}
