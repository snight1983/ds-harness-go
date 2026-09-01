// 本文件的作用：验自动压缩挂上去之后那五条观察者各自做什么——步骤边界上按压力
// 压一次、超窗之后补救一次并要求重试，以及那个重试计数在哪几处归零。

package basic

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 一次压不动的压缩把整个步骤拦掉。那是「这个回合当场死掉」，比「历史长了、
//     这次没缩下来」严重得多，而且下一个边界上还会再试一次。
//   - 一条配错了的压力参数按步骤数刷屏，把别的诊断顶掉。压力在每一个步骤边界上
//     都会重算，而配错的参数每次都以同样的方式失败。
//   - 表面**没换过**却要求重试。那是一个必然以同样方式失败的死循环。
//   - 一次已经落地的剪枝之后总结才失败，就放弃重试。那份缩减是耐久的，足以支撑
//     一次重试。
//   - 超窗重试计数不归零，于是一个工具用得多的长回合把额度慢慢耗光，真撞上超窗时
//     已经没有补救次数可用。
//   - 某一条观察者挂不上，前面已经挂上的那几条却留在运行时上。
//   - Auto 关着时还是挂了观察者。

// stubAgents 是一张可摆布的假 agent 注册表：记下挂上来的观察者和摘除的次序。
type stubAgents struct {
	mutex sync.Mutex

	preStep      agent.PreStepObserver
	status       agent.StatusObserver
	requestError agent.RequestErrorObserver
	disposed     agent.DisposedObserver

	// failAt 是第几次登记时失败，从 1 数起；0 表示一直不失败。这里的次序按
	// [installer.observe] 里那张表算：前置步骤、状态、会话事件、请求失败、处置。
	failAt int
	// calls 是这张表和那道广播加起来已经被登记过几次。
	calls int
	// undone 按摘除发生的先后记下每一条的名字。
	undone []string
}

// register 是四条 On* 共用的那一半：记次数、按脚本失败、交回一个记名的摘除函数。
func (a *stubAgents) register(label string, attach func()) (func(context.Context) error, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.calls++
	if a.failAt == a.calls {
		return nil, errors.New("这一条挂不上")
	}
	attach()
	return func(context.Context) error {
		a.mutex.Lock()
		defer a.mutex.Unlock()
		a.undone = append(a.undone, label)
		return nil
	}, nil
}

func (a *stubAgents) OnPreStep(_ context.Context, _ *scope.Scope, observer agent.PreStepObserver,
) (func(context.Context) error, error) {
	return a.register("前置步骤", func() { a.preStep = observer })
}

func (a *stubAgents) OnStatus(_ context.Context, _ *scope.Scope, observer agent.StatusObserver,
) (func(context.Context) error, error) {
	return a.register("状态", func() { a.status = observer })
}

func (a *stubAgents) OnRequestError(_ context.Context, _ *scope.Scope,
	observer agent.RequestErrorObserver,
) (func(context.Context) error, error) {
	return a.register("请求失败", func() { a.requestError = observer })
}

func (a *stubAgents) OnDisposed(_ context.Context, _ *scope.Scope, observer agent.DisposedObserver,
) (func(context.Context) error, error) {
	return a.register("处置", func() { a.disposed = observer })
}

// stubSessions 是那道假的会话日志广播，和 [stubAgents] 共用同一份登记计数。
type stubSessions struct {
	agents *stubAgents
	event  coresession.EventObserver
}

func (s *stubSessions) OnEvent(_ context.Context, _ *scope.Scope, observer coresession.EventObserver,
) (func(context.Context) error, error) {
	return s.agents.register("会话事件", func() { s.event = observer })
}

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent。
//
// 本层用得着的只有 ID、Session 和 Options 三样；剩下的方法在这里都是空的，
// 被叫到说明本层越界了。
type stubAgent struct {
	id      session.SessionID
	log     *coresession.Session
	options agent.Options
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return a.options }
func (a *stubAgent) Session() *coresession.Session                          { return a.log }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                    { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}
func (a *stubAgent) Remove(llm.MessageID)                                   {}
func (a *stubAgent) Replace(llm.MessageID, llm.Message)                     {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// installed 是一次装好之后，用例手上要用的那几样。
type installed struct {
	agents   *stubAgents
	sessions *stubSessions
	undo     func(context.Context) error
	logs     *strings.Builder
}

// installOn 把自动压缩装到一对假注册表上。
func installOn(t *testing.T, config ResolvedConfig, deps EngineDeps, failAt int) installed {
	t.Helper()

	agents := &stubAgents{failAt: failAt}
	sessions := &stubSessions{agents: agents}
	buffer := &strings.Builder{}

	undo, err := Install(t.Context(), scope.NewRoot(), InstallDeps{
		Engine:   newTestEngine(t, config, deps),
		Agents:   agents,
		Sessions: sessions,
		Logger: slog.New(slog.NewTextHandler(buffer,
			&slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if failAt == 0 && err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if failAt != 0 {
		if err == nil {
			t.Fatal("这一趟该装不上")
		}
		return installed{agents: agents, sessions: sessions, logs: buffer}
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	return installed{agents: agents, sessions: sessions, undo: undo, logs: buffer}
}

// stubAgentOn 把一段真会话包成一个假 agent。
func stubAgentOn(live *coresession.Session, provider, model string) *stubAgent {
	return &stubAgent{
		id:      live.ID(),
		log:     live,
		options: agent.Options{Provider: provider, Model: model},
	}
}

// keepStep 是最里面那个「机器本来的提议」：带着零条消息进这个步骤。
func keepStep(context.Context) (agent.PreStepDecision, error) {
	return agent.EnterStep(nil), nil
}

// keepFailure 是最里面那个「不认领，这次失败是终局」。
func keepFailure(context.Context) (agent.RequestErrorAction, error) {
	return agent.RequestErrorAction{}, nil
}

// overflowAt 拼一条「提供方确认超窗」的请求失败。
func overflowAt(live *stubAgent) agent.RequestFailure {
	return agent.RequestFailure{
		Agent:   live,
		Turn:    1,
		Step:    1,
		Failure: llm.Failure{Code: llm.ContextWindowExceededCode, Message: "装不下了"},
	}
}

func TestInstall少了必填的那几样就拒(t *testing.T) {
	t.Parallel()

	config := engineConfig(t, nil)
	engine := newTestEngine(t, config, engineDeps())
	agents := &stubAgents{}
	sessions := &stubSessions{agents: agents}

	for name, deps := range map[string]InstallDeps{
		"没有压缩后端": {Agents: agents, Sessions: sessions},
		"没有注册表":  {Engine: engine, Sessions: sessions},
		"没有会话广播": {Engine: engine, Agents: agents},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Install(t.Context(), scope.NewRoot(), deps); err == nil {
				t.Fatal("装上去了")
			}
		})
	}
	if _, err := Install(t.Context(), nil, InstallDeps{
		Engine: engine, Agents: agents, Sessions: sessions,
	}); err == nil {
		t.Fatal("没有作用域也装上去了")
	}
}

func TestInstallAuto关着就一条观察者都不挂(t *testing.T) {
	t.Parallel()

	// 手工那条路不受它影响：Auto 管的是「要不要有人自动来叫」，不是「能不能压」。
	config := engineConfig(t, func(c *Config) { c.Auto = boolOf(false) })
	agents := &stubAgents{}
	sessions := &stubSessions{agents: agents}

	undo, err := Install(t.Context(), scope.NewRoot(), InstallDeps{
		Engine:   newTestEngine(t, config, engineDeps()),
		Agents:   agents,
		Sessions: sessions,
	})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if agents.calls != 0 {
		t.Fatalf("挂了 %d 条观察者", agents.calls)
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("空的摘除函数报了错：%v", err)
	}
}

func TestInstall不给日志器就回落到默认那一个(t *testing.T) {
	t.Parallel()

	// 装配方不给日志器是常态：那些诊断只在本进程里有意义，不给就该照常挂上去,
	// 而不是拿一个 nil 去记日志、在第一个步骤边界上把整个回合打爆。
	agents := &stubAgents{}
	sessions := &stubSessions{agents: agents}

	undo, err := Install(t.Context(), scope.NewRoot(), InstallDeps{
		Engine:   newTestEngine(t, engineConfig(t, nil), engineDeps(600, 100)),
		Agents:   agents,
		Sessions: sessions,
	})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	log := routedLog(t)
	decision, err := agents.preStep(t.Context(),
		agent.PreStep{Agent: stubAgentOn(log.live, "openai", "gpt-x"), Turn: 1, Step: 1}, keepStep)
	if err != nil || !decision.Enter {
		t.Fatalf("步骤被拦了：%+v / %v", decision, err)
	}
}

func TestInstall五条都挂上摘除按反序(t *testing.T) {
	t.Parallel()

	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)
	if setup.agents.calls != 5 {
		t.Fatalf("挂了 %d 条观察者", setup.agents.calls)
	}
	if err := setup.undo(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	want := []string{"处置", "请求失败", "会话事件", "状态", "前置步骤"}
	if strings.Join(setup.agents.undone, ",") != strings.Join(want, ",") {
		t.Fatalf("摘除次序是 %v", setup.agents.undone)
	}
}

func TestInstall中间一条挂不上就把前面的撤掉(t *testing.T) {
	t.Parallel()

	// 留着半装的观察者比整个装不上更坏：它们会在一个没人知道它存在的运行时上
	// 继续压缩。
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 3)
	want := []string{"状态", "前置步骤"}
	if strings.Join(setup.agents.undone, ",") != strings.Join(want, ",") {
		t.Fatalf("撤掉的是 %v", setup.agents.undone)
	}
}

func TestOnPreStep到线了就压一次并且照样让步骤走下去(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	setup := installOn(t, engineConfig(t, nil), engineDeps(600, 100), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	decision, err := setup.agents.preStep(t.Context(),
		agent.PreStep{Agent: live, Turn: 1, Step: 1}, keepStep)
	if err != nil || !decision.Enter {
		t.Fatalf("步骤被拦了：%+v / %v", decision, err)
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionEnd); count != 1 {
		t.Fatalf("合上的括号有 %d 对", count)
	}
	if !strings.Contains(setup.logs.String(), "压缩落地") {
		t.Fatalf("没记下这一次：%s", setup.logs.String())
	}
}

func TestOnPreStep压不动也照样让这个步骤走下去(t *testing.T) {
	t.Parallel()

	// 一次压缩失败是「历史长了、这次没缩下来」，把步骤拦掉是「这个回合当场死掉」。
	log := routedLog(t)
	deps := engineDeps(600, 600)
	deps.Summarize = failingSummary(errors.New("总结炸了"))
	setup := installOn(t, engineConfig(t, nil), deps, 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	decision, err := setup.agents.preStep(t.Context(),
		agent.PreStep{Agent: live, Turn: 1, Step: 1}, keepStep)
	if err != nil || !decision.Enter {
		t.Fatalf("步骤被拦了：%+v / %v", decision, err)
	}
	if !strings.Contains(setup.logs.String(), "步骤压缩失败") {
		t.Fatalf("没记下这次失败：%s", setup.logs.String())
	}
}

func TestOnPreStep同一条路由的配置错只吵一次(t *testing.T) {
	t.Parallel()

	// 压力在每一个步骤边界上都会重算，而一份配错了的压力参数每次都以同样的方式
	// 失败。不去重的话，一条配置错误会按步骤数刷屏，把别的诊断顶掉。
	log := routedLog(t)
	deps := engineDeps(600, 600, 600, 600)
	deps.Models = &fixedModels{}
	setup := installOn(t, engineConfig(t, nil), deps, 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	for range 3 {
		if _, err := setup.agents.preStep(t.Context(),
			agent.PreStep{Agent: live, Turn: 1, Step: 1}, keepStep); err != nil {
			t.Fatalf("步骤被拦了：%v", err)
		}
	}
	if warned := strings.Count(setup.logs.String(), "步骤压缩失败"); warned != 1 {
		t.Fatalf("吵了 %d 次", warned)
	}
}

func TestOnPreStep已经取消了就不去压(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	before := log.live.Seq()
	setup := installOn(t, engineConfig(t, nil), engineDeps(600, 100), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if _, err := setup.agents.preStep(ctx,
		agent.PreStep{Agent: live, Turn: 1, Step: 1}, keepStep); err != nil {
		t.Fatalf("步骤被拦了：%v", err)
	}
	if log.live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, log.live.Seq())
	}
}

func TestOnRequestError不是超窗就原样往下走(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	before := log.live.Seq()
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	failure := overflowAt(live)
	failure.Failure.Code = "别的什么错"
	action, err := setup.agents.requestError(t.Context(), failure, keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if log.live.Seq() != before {
		t.Fatalf("动手了：日志从 %d 长到了 %d", before, log.live.Seq())
	}
}

func TestOnRequestError没有路由就原样往下走(t *testing.T) {
	t.Parallel()

	// 一段还没发过带路由请求的会话查不出上限，也就没有补救可言。
	live := stubAgentOn(liveSession(t, "s-noroute-retry"), "openai", "gpt-x")
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
}

func TestOnRequestError路由读不出来就保留原本那条失败(t *testing.T) {
	t.Parallel()

	// 读不出路由和「还没发过带路由的请求」不是一回事：后者是一段刚开头的会话的
	// 正常样子，前者是这段日志本身坏了。两者都不补救，但坏了的那一种要吵一声,
	// 否则一段读不回来的日志会安静地把超窗补救整个关掉。
	live := stubAgentOn(brokenHeaderSession(t, "s-broken-overflow"), "openai", "gpt-x")
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if !strings.Contains(setup.logs.String(), "读不出这段会话的路由") {
		t.Fatalf("没吵这一声：%s", setup.logs.String())
	}
}

func TestOnRequestError超窗补救之后要求重试(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || !action.Retry {
		t.Fatalf("没要求重试：%+v / %v", action, err)
	}
	if !strings.Contains(setup.logs.String(), "超窗补救") {
		t.Fatalf("没记下这一次：%s", setup.logs.String())
	}
}

func TestOnRequestError次数用完就不再补救(t *testing.T) {
	t.Parallel()

	// 关掉超窗补救之后一次都不该动手。
	log := routedLog(t)
	before := log.live.Seq()
	config := engineConfig(t, func(c *Config) { c.MaxOverflowRetries = intOf(0) })
	setup := installOn(t, config, engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if log.live.Seq() != before {
		t.Fatalf("动手了：日志从 %d 长到了 %d", before, log.live.Seq())
	}
}

func TestOnRequestError一串补救用完额度就停(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	config := engineConfig(t, func(c *Config) { c.MaxOverflowRetries = intOf(1) })
	setup := installOn(t, config, engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	first, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || !first.Retry {
		t.Fatalf("第一次没要求重试：%+v / %v", first, err)
	}
	second, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || second.Retry {
		t.Fatalf("额度用完了还在补救：%+v / %v", second, err)
	}
}

func TestOnRequestError表面没换过就不要求重试(t *testing.T) {
	t.Parallel()

	// 没换过就原样重发，那是一个必然以同样方式失败的死循环。表面上只剩一个节点
	// 时挑不出区间，于是这一趟什么都没换。
	live := liveSession(t, "s-thin-overflow", llm.CallConfig{Provider: "openai", Model: "gpt-x"})
	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})
	appendTo(t, live, surfaceMessage(t, session.EventUserMessage, session.UserMessageData{
		Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "就一句"}}, llm.UserSource{}),
	}))
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)

	action, err := setup.agents.requestError(t.Context(),
		overflowAt(stubAgentOn(live, "openai", "gpt-x")), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
}

func TestOnRequestError已经落地的缩减之后总结才失败也重试(t *testing.T) {
	t.Parallel()

	// 判据是表面替换代数有没有往前走，不是「压缩这一趟返没返错」——一次不过模型
	// 的剪枝可能已经落地了，后面那半总结才失败，那份缩减是耐久的。
	log := routedLog(t)
	deps := engineDeps()
	deps.Summarize = failingSummary(errors.New("总结炸了"))
	deps.Prune = func(live *coresession.Session) error {
		// 拿一条替换件把表面真的换掉，模拟那一遍已经落地的剪枝。
		_, err := live.Append(session.Event{
			Type:            session.EventUserMessage,
			Data:            marshalPayload(t, session.UserMessageData{Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "砍过了"}}, llm.UserSource{})}),
			SurfaceOp:       session.ReplaceOp{Start: log.seqs[0], End: log.seqs[0]},
			SourceEventSeqs: []int{log.seqs[0]},
		})
		return err
	}
	setup := installOn(t, engineConfig(t, nil), deps, 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || !action.Retry {
		t.Fatalf("没要求重试：%+v / %v", action, err)
	}
	if !strings.Contains(setup.logs.String(), "已经落地的缩减") {
		t.Fatalf("没记下这一次：%s", setup.logs.String())
	}
}

func TestOnRequestError补救失败而表面没动就保留原本那条失败(t *testing.T) {
	t.Parallel()

	boom := errors.New("定价炸了")
	log := routedLog(t)
	deps := engineDeps()
	deps.Meter.(*pressureMeter).priceErr = boom
	setup := installOn(t, engineConfig(t, nil), deps, 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	action, err := setup.agents.requestError(t.Context(), overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if !strings.Contains(setup.logs.String(), "保留原本那条请求失败") {
		t.Fatalf("没记下这一次：%s", setup.logs.String())
	}
}

func TestOnRequestError已经取消就不补救(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	before := log.live.Seq()
	setup := installOn(t, engineConfig(t, nil), engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")
	ctx, stop := context.WithCancel(t.Context())
	stop()

	action, err := setup.agents.requestError(ctx, overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if log.live.Seq() != before {
		t.Fatalf("动手了：日志从 %d 长到了 %d", before, log.live.Seq())
	}
}

func TestOnRequestError补救当中被取消就不重试(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	ctx, stop := context.WithCancel(t.Context())
	deps := engineDeps()
	deps.Prune = func(*coresession.Session) error { stop(); return nil }
	deps.Summarize = failingSummary(errors.New("总结炸了"))
	setup := installOn(t, engineConfig(t, nil), deps, 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	action, err := setup.agents.requestError(ctx, overflowAt(live), keepFailure)
	if err != nil || action.Retry {
		t.Fatalf("认领了：%+v / %v", action, err)
	}
	if !strings.Contains(setup.logs.String(), "已经被取消") {
		t.Fatalf("没记下这一次：%s", setup.logs.String())
	}
}

func TestOnRequestError那串计数在三处归零(t *testing.T) {
	t.Parallel()

	// 回到空闲、一条助手消息落地、这个 agent 被处置掉——每一处都是一段新的开始。
	for name, reset := range map[string]func(setup installed, live *stubAgent){
		"回到空闲": func(setup installed, live *stubAgent) {
			setup.agents.status(live, agent.StatusIdle)
		},
		"助手消息落地": func(setup installed, live *stubAgent) {
			setup.sessions.event(live.Session(), session.Event{
				Type: session.EventAssistantMessage,
			})
		},
		"这个 agent 被处置": func(setup installed, live *stubAgent) {
			setup.agents.disposed(live)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := routedLog(t)
			config := engineConfig(t, func(c *Config) { c.MaxOverflowRetries = intOf(1) })
			setup := installOn(t, config, engineDeps(), 0)
			live := stubAgentOn(log.live, "openai", "gpt-x")

			if first, err := setup.agents.requestError(
				t.Context(), overflowAt(live), keepFailure); err != nil || !first.Retry {
				t.Fatalf("第一次没要求重试：%+v / %v", first, err)
			}
			reset(setup, live)
			if second, err := setup.agents.requestError(
				t.Context(), overflowAt(live), keepFailure); err != nil || !second.Retry {
				t.Fatalf("计数没归零：%+v / %v", second, err)
			}
		})
	}
}

func TestOnStatus和会话事件只认那一种(t *testing.T) {
	t.Parallel()

	// 不是空闲的状态跃迁、不是助手消息的日志事件，都不该动那串计数。
	log := routedLog(t)
	config := engineConfig(t, func(c *Config) { c.MaxOverflowRetries = intOf(1) })
	setup := installOn(t, config, engineDeps(), 0)
	live := stubAgentOn(log.live, "openai", "gpt-x")

	if first, err := setup.agents.requestError(
		t.Context(), overflowAt(live), keepFailure); err != nil || !first.Retry {
		t.Fatalf("第一次没要求重试：%+v / %v", first, err)
	}
	setup.agents.status(live, agent.StatusRunning)
	setup.sessions.event(live.Session(), session.Event{Type: session.EventUserMessage})

	if second, err := setup.agents.requestError(
		t.Context(), overflowAt(live), keepFailure); err != nil || second.Retry {
		t.Fatalf("计数被这两条清掉了：%+v / %v", second, err)
	}
}
