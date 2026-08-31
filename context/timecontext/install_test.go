// 本文件的作用：把装配那一条路压一遍——配置在哪一步验、观察者挂上去之后
// 在什么条件下补那条读数，以及补出来的东西能不能被本包自己的不变量认回来。

package timecontext

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 一份时区写错的配置装上去了，于是每一条读数都带着一个模型读不懂的时区名，
//     而且不变量再也重现不出它们。
//   - 读数排在里层那批消息**前面**：模型先读到注脚再读到正文。
//   - 里层交出来的那张切片被就地追加，而它可能还被别的观察者拿着。
//   - 一步被否掉、或者请求已经取消了，却照样补一条说着「正在准备第 N 步」的读数。
//   - 一条读不回来的历史事件把整个步骤准入带崩——这一层补不上上下文是降级，
//     不是故障。
//   - 更坏的那一半：读不回来时退而求其次，注入一条基线可疑的读数。那条
//     "Elapsed since…" 会被模型当成事实。
//   - 节流配了等于没配：两次注入之间没走满间隔也照注。

// absolutePath 是一条在本机上确实绝对的路径；会话头要求 cwd 是绝对的，而写死
// 哪一边的字面量都会让另一个平台上的用例变成假失败。
var absolutePath = filepath.Join(os.TempDir(), "ds-harness-go-timecontext")

// ---- 假注册表 ----

// captureAgents 是一台只把观察者接下来的假注册表。
//
// 用假的而不是真 [agent.Registry]：这些用例问的是「这条规则在各种输入下交出
// 什么」，真注册表还要求把 agent 挂进去并公布，那一整套跟本包要验的事无关。
type captureAgents struct {
	// observer 是装上去的那条规则。
	observer agent.PreStepObserver
	// attachFail 非 nil 时登记当场失败。
	attachFail error
	// removeFail 非 nil 时摘下来那一步报这个错。
	removeFail error
	// removed 记下摘了几次。
	removed int
}

func (a *captureAgents) OnPreStep(
	_ context.Context,
	_ *scope.Scope,
	observer agent.PreStepObserver,
) (func(context.Context) error, error) {
	if a.attachFail != nil {
		return nil, a.attachFail
	}
	a.observer = observer
	return func(context.Context) error {
		a.removed++
		return a.removeFail
	}, nil
}

// ---- 假 agent ----

// stubAgent 只把一段会话摆在那儿，别的什么都不做。
type stubAgent struct {
	id   session.SessionID
	sess *coresession.Session
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                    { return nil }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// ---- 夹具 ----

// world 是一次装配加它那个 agent。
//
// 时钟是一个可以往前拨的字段：这一层产出的每一个字节都挂在「此刻几点」上，
// 拨不动的话「读数里写的就是采样那一刻」这条断言不了。
type world struct {
	t      *testing.T
	agents *captureAgents
	live   *stubAgent
	now    time.Time
	remove func(context.Context) error
}

// newWorld 装一次，交出装好的那一套。
func newWorld(t *testing.T, config Config) *world {
	t.Helper()

	made := &world{
		t:      t,
		agents: &captureAgents{},
		now:    time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
	}
	// 会话的时钟跟着同一个字段走：读数上的采样时刻和它落库的时刻必须能比先后，
	// 而不变量正是查这一条。
	live, err := coresession.NewSession("s", coresession.Options{
		Header: &session.SessionHeader{ID: "s", Cwd: absolutePath},
		Now:    func() int64 { return made.now.UnixMilli() },
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	made.live = &stubAgent{id: "s", sess: live}

	remove, err := Install(t.Context(), scope.NewRoot(), config, Deps{
		Agents: made.agents,
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return made.now },
	})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	made.remove = remove
	return made
}

// append 往这段会话里写一条事件。
func (w *world) append(kind session.EventType, payload any, surface session.SurfaceOp) session.Event {
	w.t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		w.t.Fatalf("负载排不出去：%v", err)
	}
	written, err := w.live.sess.Append(session.Event{Type: kind, Data: data, SurfaceOp: surface})
	if err != nil {
		w.t.Fatalf("追加 %s 失败：%v", kind, err)
	}
	return written
}

// openTurn 开一个回合。
func (w *world) openTurn(turn int) {
	w.t.Helper()
	w.append(session.EventTurnStart, session.TurnStartData{Turn: turn}, nil)
}

// openStep 开一个步骤。
func (w *world) openStep(turn, step int) {
	w.t.Helper()
	w.append(session.EventStepStart, session.StepStartData{Turn: turn, Step: step}, nil)
}

// say 写一条用户自己说的话。
func (w *world) say(text string) {
	w.t.Helper()
	w.append(session.EventUserMessage, session.UserMessageData{Message: llm.Message{
		ID: "u", Role: llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}}, session.AppendOp{})
}

// corrupt 写一条类型对、负载形状不对的用户消息。
//
// 会话本身只验 JSON 是否成立，一个合法的数组照收；形状对不对要到本包自己去解
// 的时候才发现，而这里测的正是那一步。
func (w *world) corrupt() {
	w.t.Helper()
	if _, err := w.live.sess.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      json.RawMessage("[1,2,3]"),
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		w.t.Fatalf("追加坏事件失败：%v", err)
	}
}

// run 跑一遍装上去的那条规则，里层交出一个带着 given 的准入决定。
func (w *world) run(ctx context.Context, turn, step int, given ...llm.Message) agent.PreStepDecision {
	w.t.Helper()

	decision, err := w.agents.observer(ctx, agent.PreStep{
		Agent: w.live, Turn: turn, Step: step,
	}, func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep(given), nil
	})
	if err != nil {
		w.t.Fatalf("这条规则不该报错：%v", err)
	}
	return decision
}

// readingIn 取出决定末尾那条读数的正文，末尾那条不是读数就当场失败。
func (w *world) readingIn(decision agent.PreStepDecision) string {
	w.t.Helper()

	if len(decision.Messages) == 0 {
		w.t.Fatal("该补上一条读数，一条消息都没有")
	}
	last := decision.Messages[len(decision.Messages)-1]
	source, ok := last.Source.(llm.PluginSource)
	if !ok || source.Plugin != PluginName {
		w.t.Fatalf("末尾那条不是本包署名的：%#v", last.Source)
	}
	text, ok := last.Content[0].(llm.TextBlock)
	if !ok {
		w.t.Fatalf("末尾那条里不是文本：%#v", last.Content[0])
	}
	return text.Text
}

// userSaid 造一条用户消息，用来占住里层那批消息里的位置。
func userSaid(text string) llm.Message {
	return llm.Message{
		ID: "u", Role: llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}
}

// ---- 装配那一步 ----

func TestInstallNeedsAScopeAndARegistry(t *testing.T) {
	t.Parallel()
	if _, err := Install(t.Context(), nil, Config{}, Deps{Agents: &captureAgents{}}); err == nil {
		t.Fatal("没给作用域该装不上")
	}
	if _, err := Install(t.Context(), scope.NewRoot(), Config{}, Deps{}); err == nil {
		t.Fatal("没给 agent 注册表该装不上")
	}
}

func TestInstallValidatesTheConfigBeforeAttaching(t *testing.T) {
	t.Parallel()
	// 一份验不过的配置绝不能变成一个「时区错着照样往提示词里写」的运行期。
	agents := &captureAgents{}
	_, err := Install(t.Context(), scope.NewRoot(), Config{TimeZone: "Local"}, Deps{Agents: agents})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("该报配置不合法，实际 %v", err)
	}
	if agents.observer != nil {
		t.Fatal("配置没过就不该往瀑布上挂东西")
	}
}

func TestInstallReportsAFailedRegistration(t *testing.T) {
	t.Parallel()
	planted := errors.New("挂不上")
	_, err := Install(t.Context(), scope.NewRoot(), Config{},
		Deps{Agents: &captureAgents{attachFail: planted}})
	if !errors.Is(err, planted) {
		t.Fatalf("底层原因该留在链上：%v", err)
	}
}

func TestInstallHandsBackTheRegistrationsOwnDisposer(t *testing.T) {
	t.Parallel()
	// 交回来的必须就是注册表那一个：自己包一层而漏掉它的错，摘不下来这件事
	// 就没人知道了。
	planted := errors.New("摘不掉")
	agents := &captureAgents{removeFail: planted}
	remove, err := Install(t.Context(), scope.NewRoot(), Config{}, Deps{Agents: agents})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if err := remove(t.Context()); !errors.Is(err, planted) {
		t.Fatalf("摘不下来该原样报出去，实际 %v", err)
	}
	if agents.removed != 1 {
		t.Fatalf("该摘一次，实际 %d 次", agents.removed)
	}
}

func TestInstallFallsBackToTheProcessDefaults(t *testing.T) {
	t.Parallel()
	// 不给 Logger 和 Now 是最常见的用法，它必须能跑出一条读数来。
	agents := &captureAgents{}
	if _, err := Install(t.Context(), scope.NewRoot(), Config{}, Deps{Agents: agents}); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	live, err := coresession.NewSession("s", coresession.Options{
		Header: &session.SessionHeader{ID: "s", Cwd: absolutePath}})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	decision, err := agents.observer(t.Context(),
		agent.PreStep{Agent: &stubAgent{id: "s", sess: live}, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if err != nil || len(decision.Messages) != 1 {
		t.Fatalf("默认装配该照样补出一条读数：%d 条，err=%v", len(decision.Messages), err)
	}
}

// ---- 这条规则本身 ----

func TestPreStepPassesDownstreamErrorsThrough(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{})
	planted := errors.New("里层炸了")
	_, err := w.agents.observer(t.Context(), agent.PreStep{Agent: w.live, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.PreStepDecision{}, planted
		})
	if !errors.Is(err, planted) {
		t.Fatalf("里层的错该原样交出去，实际 %v", err)
	}
}

func TestPreStepLeavesARejectedStepAlone(t *testing.T) {
	t.Parallel()
	// 拒了就没有「这一步」可言，往里补一条说着「正在准备第 1 步」的读数是句假话。
	w := newWorld(t, Config{})
	decision, err := w.agents.observer(t.Context(), agent.PreStep{Agent: w.live, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.RejectStep(), nil })
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if decision.Enter || len(decision.Messages) != 0 {
		t.Fatalf("被拒的决定该原样交回去：%#v", decision)
	}
}

func TestPreStepLeavesACancelledStepAlone(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	decision := w.run(ctx, 1, 1)
	if len(decision.Messages) != 0 {
		t.Fatalf("取消之后不该再补读数：%#v", decision.Messages)
	}
}

func TestPreStepLeavesAStepWithNoAgentAlone(t *testing.T) {
	t.Parallel()
	// 运行时里出不来这种提议，但这条规则挂在一个公开的瀑布上，别人喂什么进来
	// 不归本包管——不能因此空指针。
	w := newWorld(t, Config{})
	decision, err := w.agents.observer(t.Context(), agent.PreStep{Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if err != nil || len(decision.Messages) != 0 {
		t.Fatalf("没有 agent 的提议该原样放过：%#v，err=%v", decision, err)
	}
}

func TestPreStepAppendsTheReadingAfterTheInnerMessages(t *testing.T) {
	t.Parallel()
	// 里层那批里有用户刚说的话和运行期上下文，读数是给它们做注脚的；
	// 排在前面等于让模型先读到注脚。
	w := newWorld(t, Config{})
	w.openTurn(1)
	decision := w.run(t.Context(), 1, 1, userSaid("前"))
	if len(decision.Messages) != 2 {
		t.Fatalf("该是原来那条加一条读数，实际 %d 条", len(decision.Messages))
	}
	first, ok := decision.Messages[0].Content[0].(llm.TextBlock)
	if !ok || first.Text != "前" {
		t.Fatalf("头一条该原样留着：%#v", decision.Messages[0])
	}
	if !strings.HasPrefix(w.readingIn(decision), "Time sampled while preparing turn 1, step 1: ") {
		t.Fatalf("补出来的不是一条读数：%q", w.readingIn(decision))
	}
}

func TestPreStepDoesNotWriteIntoTheInnerSlice(t *testing.T) {
	t.Parallel()
	// 里层交出来的那张切片可能还被别人拿着，就地 append 会把别人手上那份改掉。
	w := newWorld(t, Config{})
	w.openTurn(1)
	given := make([]llm.Message, 1, 4)
	given[0] = userSaid("前")
	decision := w.run(t.Context(), 1, 1, given...)
	if &decision.Messages[0] == &given[0] {
		t.Fatal("该复制一份再追加，实际共用同一段底层数组")
	}
	if len(given) != 1 {
		t.Fatalf("里层那张切片该纹丝不动，实际 %d 条", len(given))
	}
}

func TestPreStepUsesTheConfiguredTimeZone(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{TimeZone: "Asia/Shanghai"})
	w.openTurn(1)
	text := w.readingIn(w.run(t.Context(), 1, 1))
	if !strings.Contains(text, "[Asia/Shanghai]") || !strings.Contains(text, "+08:00") {
		t.Fatalf("读数该按配的时区排：%q", text)
	}
}

func TestPreStepMeasuresFromTheLastModelVisibleMessageOnTheFirstStep(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{})
	w.openTurn(1)
	w.say("喂")
	w.now = w.now.Add(90 * time.Second)
	text := w.readingIn(w.run(t.Context(), 1, 1))
	if !strings.Contains(text, "Elapsed since the preceding model-visible message: 1m 30s.") {
		t.Fatalf("第一步该按上一条模型看得见的消息算：%q", text)
	}
}

func TestPreStepMeasuresFromTheLastReadingOnLaterSteps(t *testing.T) {
	t.Parallel()
	// 第二步的基线是本回合里上一条读数，不是那条用户消息——否则一个回合里的
	// 每一步都会报出「离用户说话过了多久」，而模型问的是「离上一步过了多久」。
	w := newWorld(t, Config{})
	w.openTurn(1)
	w.say("喂")
	w.now = w.now.Add(time.Minute)
	w.openStep(1, 1)
	w.appendReading(w.run(t.Context(), 1, 1))
	w.now = w.now.Add(5 * time.Second)
	text := w.readingIn(w.run(t.Context(), 1, 2))
	if !strings.Contains(text, "Elapsed since the preceding step context: 5s.") {
		t.Fatalf("后续步骤该按本回合上一条读数算：%q", text)
	}
}

func TestPreStepSaysUnavailableWhenThereIsNoBaseline(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{})
	w.openTurn(1)
	text := w.readingIn(w.run(t.Context(), 1, 1))
	if !strings.Contains(text, "Elapsed since the preceding model-visible message: unavailable.") {
		t.Fatalf("找不到基线该写 unavailable：%q", text)
	}
}

func TestPreStepThrottlesOnTheRefreshInterval(t *testing.T) {
	t.Parallel()
	// 节流问的是「本包最近一次往这个会话里写字是什么时候」。走不满这段间隔
	// 还照注，等于配了间隔没生效。
	w := newWorld(t, Config{RefreshInterval: time.Minute})
	w.openTurn(1)
	w.openStep(1, 1)
	w.appendReading(w.run(t.Context(), 1, 1))

	w.now = w.now.Add(30 * time.Second)
	if messages := w.run(t.Context(), 1, 2).Messages; len(messages) != 0 {
		t.Fatalf("间隔没走满不该再注，实际补了 %d 条", len(messages))
	}
	w.now = w.now.Add(31 * time.Second)
	if messages := w.run(t.Context(), 1, 3).Messages; len(messages) != 1 {
		t.Fatalf("间隔走满了该接着注，实际补了 %d 条", len(messages))
	}
}

func TestPreStepSkipsTheReadingWhenTheThrottleCannotBeAnswered(t *testing.T) {
	t.Parallel()
	// 补不上上下文是降级，不是故障：让 agent 因为一条读不回来的历史事件停摆，
	// 代价和收益完全不成比例。
	w := newWorld(t, Config{RefreshInterval: time.Minute})
	w.openTurn(1)
	w.corrupt()
	if messages := w.run(t.Context(), 1, 1).Messages; len(messages) != 0 {
		t.Fatalf("算不出该不该注入时不该注，实际补了 %d 条", len(messages))
	}
}

func TestPreStepSkipsTheReadingWhenTheBaselineCannotBeAnswered(t *testing.T) {
	t.Parallel()
	// 这里更要紧的是**不能**退而求其次注一条基线可疑的读数：那句
	// "Elapsed since…" 会被模型当成事实。
	w := newWorld(t, Config{})
	w.openTurn(1)
	w.corrupt()
	if messages := w.run(t.Context(), 1, 2).Messages; len(messages) != 0 {
		t.Fatalf("算不出基线时不该注，实际补了 %d 条", len(messages))
	}
}

func TestTheInjectedReadingSatisfiesThePackagesOwnInvariant(t *testing.T) {
	t.Parallel()
	// 这一条把装配那一端和校验那一端接上：这条规则补出来的东西，本包自己那条
	// 不变量必须认得回来。两端各自都对、拼起来不对，是这一层最贵的那种错。
	location := mustLoad(t, "Asia/Shanghai")
	w := newWorld(t, Config{TimeZone: "Asia/Shanghai"})
	w.openTurn(1)
	w.say("喂")

	w.now = w.now.Add(time.Minute)
	w.openStep(1, 1)
	decision := w.run(t.Context(), 1, 1)
	// 采样在准入里发生，落库要等这一步真的开跑，所以时钟往前拨一下再写。
	w.now = w.now.Add(time.Second)
	w.appendReading(decision)

	w.openStep(1, 2)
	w.now = w.now.Add(2 * time.Second)
	second := w.run(t.Context(), 1, 2)
	w.now = w.now.Add(time.Second)
	w.appendReading(second)

	if err := ValidateSession(w.live.sess.Events(), location); err != nil {
		t.Fatalf("补出来的读数过不了本包自己的不变量：%v", err)
	}
}

// appendReading 把这次决定末尾那条读数落进日志，摆出这一步真的开跑了。
func (w *world) appendReading(decision agent.PreStepDecision) {
	w.t.Helper()

	if len(decision.Messages) == 0 {
		w.t.Fatal("这一步该补出一条读数")
	}
	w.append(session.EventUserMessage,
		session.UserMessageData{Message: decision.Messages[len(decision.Messages)-1]},
		session.AppendOp{})
}
