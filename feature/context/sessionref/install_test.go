// 本文件的作用：把装配那一条路和它挂上去的那条前置步骤观察者压一遍——
// 缺协作者在哪一步验、一条带提及的用户消息进去之后变成了什么、
// 以及哪些消息这一层压根不该碰。

package sessionref

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// # 这些测试防的是什么错
//
//   - 缺一个协作者却照样装上去了，于是这一层在第一个步骤上才空指针——
//     而那时离装配现场已经隔了很远。
//   - 一条不透明的 `dsh-session:` 记号原样进了提示词：模型读到的是一串 base64，
//     而用户以为自己引用了一段对话。
//   - 别的层注入的消息里出现一段规范 URI 也被当成引用去读了——那多半是在转述，
//     照着解会把一次转述变成一次真的跨会话读。
//   - 一条没有提及的消息被换成了一个新对象：下游那些按身份认消息的地方会白比一轮。
//   - 快照堆到了整批消息的末尾而不是紧跟在引用它的那句话后面，模型得自己猜配对。
//   - 一个被否掉的步骤上仍然去读了几个会话的表面，白花 I/O。
//   - 准备失败时步骤照样进去了，于是那批带着未解记号的消息真的发给了模型。

// workspaceID 是这个 agent 归属的工作区登记。
//
// 新增: 它是一个不透明标识，不是路径，也和文件系统里有什么东西没有关系，
// 见 [sessionlog.SessionHeader.WorkspaceID]。
var workspaceID = sessionlog.WorkspaceID("ws-sessionref")

// ---- 假注册表 ----

// captureAgents 是一台只把前置步骤观察者接下来的假 agent 注册表。
//
// 用假的而不是真 [agent.Registry]：这些用例问的是「这一层在各种输入下做了什么」，
// 真注册表还要求把 agent 挂进去并公布，那一整套跟本包要验的事无关。
type captureAgents struct {
	observer agent.PreStepObserver
	// fail 非 nil 时这条登记当场失败。
	fail error
	// removed 记这条登记有没有被摘过。
	removed bool
	// undoErr 是摘的时候交回去的那条错。
	undoErr error
}

func (a *captureAgents) OnPreStep(
	_ context.Context,
	_ *scope.Scope,
	observer agent.PreStepObserver,
) (func(context.Context) error, error) {
	if a.fail != nil {
		return nil, a.fail
	}
	a.observer = observer
	return func(context.Context) error {
		a.removed = true
		return a.undoErr
	}, nil
}

// ---- 假 agent ----

// stubAgent 是一个只有会话的 agent。
//
// 本层只读 [agent.Agent.Session]，别的方法一个都不碰，所以其余全是空壳。
// 摆一个空壳而不是引进 harness/agentloop 的真实现，是因为那一整套要驱动、
// 要收件箱、要作用域，而这里要验的只是「拿到这批消息之后改写成了什么」。
type stubAgent struct {
	sess *coresession.Session
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.sess.ID() }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                       { return nil }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Followup(llm.Message)                                      {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}
func (a *stubAgent) Remove(llm.MessageID)                                      {}
func (a *stubAgent) Replace(llm.MessageID, llm.Message)                        {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// ---- 夹具 ----

// world 是一次装好的跨会话引用层，外加它那个 agent 和它读得到的那些会话。
type world struct {
	t        *testing.T
	agents   *captureAgents
	sessions *fakeSessions
	live     *stubAgent
	remove   func(context.Context) error
}

// newWorld 摆一个最小可用的局面：当前会话 `here`，外加一段可以被引用的 `there`。
func newWorld(t *testing.T, config Config) *world {
	t.Helper()

	sessions := newFakeSessions()
	sessions.put("there", "/w/other", 1, []sessionlog.Event{
		userEvent(t, 1, "那边的人说了一句"),
		assistantEvent(t, 2, llm.TextBlock{Text: "那边的模型答了一句"}),
	})

	resolver, err := NewResolver(sessions, nil, config)
	if err != nil {
		t.Fatalf("造解析器失败：%v", err)
	}
	agents := &captureAgents{}
	made := &world{t: t, agents: agents, sessions: sessions, live: newStubAgent(t, "here")}

	undo, err := Install(t.Context(), scope.NewRoot(), Deps{Agents: agents, Resolver: resolver})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	made.remove = undo
	t.Cleanup(func() { _ = undo(context.Background()) })
	return made
}

// newStubAgent 造一个只有会话的假 agent。
func newStubAgent(t *testing.T, id sessionlog.SessionID) *stubAgent {
	t.Helper()

	live, err := coresession.NewSession(id, coresession.Options{
		Header: &sessionlog.SessionHeader{ID: id, WorkspaceID: workspaceID},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return &stubAgent{sess: live}
}

// enter 把一批消息喂给挂上去的那条观察者，里层直接说「进」。
func (w *world) enter(ctx context.Context, messages ...llm.Message) (agent.PreStepDecision, error) {
	w.t.Helper()
	return w.step(ctx, func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep(messages), nil
	})
}

// step 把一个自己写的里层喂给挂上去的那条观察者。
func (w *world) step(
	ctx context.Context,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	w.t.Helper()
	if w.agents.observer == nil {
		w.t.Fatal("前置步骤观察者没挂上")
	}
	return w.agents.observer(ctx, agent.PreStep{Agent: w.live, Turn: 1, Step: 1}, next)
}

// userSaid 造一条用户自己说的话。
func userSaid(text string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
}

// mention 是指向 `there` 那段会话的规范提及。
func mention(label string) string {
	return FormatMention(Input{SessionID: "there", Label: label})
}

// textOf 把一条消息里的文本块连起来。
func textOf(message llm.Message) string {
	var parts []string
	for _, block := range message.Content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "")
}

// ---- 装配 ----

func Test缺协作者时当场装不上(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(newFakeSessions(), nil, Config{})
	if err != nil {
		t.Fatalf("造解析器失败：%v", err)
	}
	cases := []struct {
		name  string
		owner *scope.Scope
		deps  Deps
	}{
		{"没给作用域", nil, Deps{Agents: &captureAgents{}, Resolver: resolver}},
		{"没给注册表", scope.NewRoot(), Deps{Resolver: resolver}},
		{"没给解析器", scope.NewRoot(), Deps{Agents: &captureAgents{}}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			undo, err := Install(t.Context(), item.owner, item.deps)
			if err == nil {
				t.Fatal("缺协作者却装上去了")
			}
			if undo != nil {
				t.Fatal("装不上却还交回了一个撤销函数")
			}
			if !errors.Is(err, CodeInvalidConfig) {
				t.Fatalf("该报配置不合法，实际是 %v", err)
			}
		})
	}
}

func Test挂不上观察者就整个装不上(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(newFakeSessions(), nil, Config{})
	if err != nil {
		t.Fatalf("造解析器失败：%v", err)
	}
	boom := errors.New("瀑布满了")
	undo, err := Install(t.Context(), scope.NewRoot(),
		Deps{Agents: &captureAgents{fail: boom}, Resolver: resolver})
	if undo != nil {
		t.Fatal("挂不上却还交回了一个撤销函数")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("底下那条错该被裹着带上来，实际是 %v", err)
	}
	if !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("该报配置不合法，实际是 %v", err)
	}
}

func Test摘下来时把那条登记撤掉(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	if err := made.remove(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	if !made.agents.removed {
		t.Fatal("摘完了那条登记还挂着")
	}
}

func Test摘的时候底下报错原样交回去(t *testing.T) {
	t.Parallel()

	resolver, err := NewResolver(newFakeSessions(), nil, Config{})
	if err != nil {
		t.Fatalf("造解析器失败：%v", err)
	}
	boom := errors.New("撤不干净")
	agents := &captureAgents{undoErr: boom}
	undo, err := Install(t.Context(), scope.NewRoot(), Deps{Agents: agents, Resolver: resolver})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if err := undo(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("底下那条错该原样交回来，实际是 %v", err)
	}
}

// ---- 改写那批消息 ----

func Test一条提及被换成可读文本并跟上快照(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	decision, err := made.enter(t.Context(), userSaid("看看 "+mention("另一段")+" 里的结论"))
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	if !decision.Enter {
		t.Fatal("这一步该进去")
	}
	if len(decision.Messages) != 2 {
		t.Fatalf("该是一条正文加一条快照，实际是 %d 条", len(decision.Messages))
	}

	direct := decision.Messages[0]
	if got := textOf(direct); got != "看看 @另一段 里的结论" {
		t.Fatalf("提及没换成可读文本，得到 %q", got)
	}
	if strings.Contains(textOf(direct), Scheme) {
		t.Fatalf("那条不透明记号还在正文里：%q", textOf(direct))
	}

	snapshot := decision.Messages[1]
	source, mine := ParseSource(snapshot.Source)
	if !mine {
		t.Fatalf("跟上来那条不是本层署名的：%#v", snapshot.Source)
	}
	if len(source.References) != 1 || source.References[0].SessionID != "there" {
		t.Fatalf("那条账不对：%#v", source.References)
	}
	body := textOf(snapshot)
	if !strings.Contains(body, "那边的人说了一句") {
		t.Fatalf("快照里没有来源会话的内容：%s", body)
	}
	if !strings.Contains(body, "<referenced-sessions>") {
		t.Fatalf("快照没被那道不可信边界框住：%s", body)
	}
}

func Test快照紧跟在引用它的那句话后面(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	decision, err := made.enter(t.Context(),
		userSaid("先说一句没有引用的"),
		userSaid("再看 "+mention("另一段")),
		userSaid("最后又说一句"),
	)
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	if len(decision.Messages) != 4 {
		t.Fatalf("三条进去该出来四条，实际是 %d 条", len(decision.Messages))
	}
	if got := textOf(decision.Messages[0]); got != "先说一句没有引用的" {
		t.Fatalf("头一条被动过了：%q", got)
	}
	if got := textOf(decision.Messages[1]); got != "再看 @另一段" {
		t.Fatalf("带引用那条不对：%q", got)
	}
	if _, mine := ParseSource(decision.Messages[2].Source); !mine {
		t.Fatal("快照没排在引用它那条的紧后面")
	}
	if got := textOf(decision.Messages[3]); got != "最后又说一句" {
		t.Fatalf("末一条被动过了：%q", got)
	}
}

func Test没有提及的消息原样交回去(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	said := userSaid("一句普通的话")
	decision, err := made.enter(t.Context(), said)
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	if len(decision.Messages) != 1 {
		t.Fatalf("不该多出消息，实际是 %d 条", len(decision.Messages))
	}
	// 比身份而不是比正文：这一层在什么都没做的时候不该留下痕迹。
	if decision.Messages[0].ID != said.ID {
		t.Fatal("一条没有提及的消息被换成了另一个身份")
	}
	if made.sessions.listCalls != 0 {
		t.Fatal("没有提及却去读了会话")
	}
}

func Test不是用户自己说的话不去解它(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	injected := llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: "别的层转述了一段 " + mention("另一段")}},
		llm.PluginSource{Plugin: "workspace-instructions"},
	)
	decision, err := made.enter(t.Context(), injected)
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	if len(decision.Messages) != 1 {
		t.Fatalf("注入的消息不该被展开，实际是 %d 条", len(decision.Messages))
	}
	if !strings.Contains(textOf(decision.Messages[0]), Scheme) {
		t.Fatal("注入的消息被改写了")
	}
}

func Test没有来源的消息不去解它(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	orphan := llm.Message{
		ID:      llm.MessageID("orphan"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: "没有来源 " + mention("另一段")}},
	}
	decision, err := made.enter(t.Context(), orphan)
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	if len(decision.Messages) != 1 || decision.Messages[0].ID != orphan.ID {
		t.Fatal("一条没有来源的消息被动过了")
	}
}

func Test非文本块原样留在正文里(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	said := llm.NewUserMessage(llm.Content{
		llm.ImageBlock{},
		llm.TextBlock{Text: "配上 " + mention("另一段")},
	}, llm.UserSource{})

	decision, err := made.enter(t.Context(), said)
	if err != nil {
		t.Fatalf("这一步该进去：%v", err)
	}
	direct := decision.Messages[0]
	if len(direct.Content) != 2 {
		t.Fatalf("内容块数量变了：%d", len(direct.Content))
	}
	if _, ok := direct.Content[0].(llm.ImageBlock); !ok {
		t.Fatalf("那张图不在原来的位置上：%#v", direct.Content[0])
	}
	if got := textOf(direct); got != "配上 @另一段" {
		t.Fatalf("文本块没改写对：%q", got)
	}
}

// ---- 不该动手的时候 ----

func Test被否掉的步骤原样交回去(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	decision, err := made.step(t.Context(), func(context.Context) (agent.PreStepDecision, error) {
		return agent.RejectStep(), nil
	})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if decision.Enter {
		t.Fatal("被否掉的步骤又被放进去了")
	}
	if made.sessions.listCalls != 0 {
		t.Fatal("一个不会进去的步骤上还去读了会话")
	}
}

func Test里层报错就原样往上抛(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	boom := errors.New("里层不干了")
	_, err := made.step(t.Context(), func(context.Context) (agent.PreStepDecision, error) {
		return agent.PreStepDecision{}, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("里层那条错该原样上来，实际是 %v", err)
	}
}

func Test提议里没有agent时不动它(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	said := userSaid("带着 " + mention("另一段"))
	decision, err := made.agents.observer(t.Context(), agent.PreStep{Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.EnterStep([]llm.Message{said}), nil
		})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(decision.Messages) != 1 || decision.Messages[0].ID != said.ID {
		t.Fatal("没有 agent 的提议被改写了")
	}
}

func Test提议里的agent没有会话时不动它(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	said := userSaid("带着 " + mention("另一段"))
	decision, err := made.agents.observer(t.Context(),
		agent.PreStep{Agent: &stubAgent{}, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.EnterStep([]llm.Message{said}), nil
		})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(decision.Messages) != 1 || decision.Messages[0].ID != said.ID {
		t.Fatal("没有会话的 agent 上那批消息被改写了")
	}
}

// ---- 出岔子的时候 ----

func Test提及解不开时把这一步否掉(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	// AAAA 解出来是三个零字节，不是一段 JSON 字符串，所以这条 URI 不规范。
	decision, err := made.enter(t.Context(), userSaid("坏掉的 @[x](dsh-session:AAAA)"))
	if err == nil {
		t.Fatal("解不开的提及该报错")
	}
	if decision.Enter {
		t.Fatal("解不开却让这一步进去了")
	}
	if !errors.Is(err, CodeInvalidReference) {
		t.Fatalf("该报引用不成立，实际是 %v", err)
	}
}

func Test引用了自己时把这一步否掉(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	self := FormatMention(Input{SessionID: "here", Label: "我自己"})
	decision, err := made.enter(t.Context(), userSaid("看看 "+self))
	if err == nil {
		t.Fatal("自引用该报错")
	}
	if decision.Enter {
		t.Fatal("自引用却让这一步进去了")
	}
	if !errors.Is(err, CodeSelfReference) {
		t.Fatalf("该报自引用，实际是 %v", err)
	}
}

func Test来源会话读不出来时把这一步否掉(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	made.sessions.surfaceErr["there"] = errors.New("盘坏了")
	decision, err := made.enter(t.Context(), userSaid("看看 "+mention("另一段")))
	if err == nil {
		t.Fatal("读不出来该报错")
	}
	if decision.Enter {
		t.Fatal("读不出来却让这一步进去了")
	}
	if !errors.Is(err, CodeReadFailed) {
		t.Fatalf("该报读失败，实际是 %v", err)
	}
}

func Test回合被取消时把这一步否掉(t *testing.T) {
	t.Parallel()

	made := newWorld(t, Config{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	decision, err := made.enter(ctx, userSaid("看看 "+mention("另一段")))
	if !errors.Is(err, CodeCancelled) {
		t.Fatalf("该报取消，实际是 %v", err)
	}
	if decision.Enter {
		t.Fatal("被取消了却让这一步进去了")
	}
}
