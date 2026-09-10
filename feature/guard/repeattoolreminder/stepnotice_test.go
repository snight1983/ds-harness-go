// 本文件的作用：钉住「断链那一下真的被 agent 循环叫到了」。
//
// # 这些测试防的是什么错
//
//   - **接线接了个寂寞**。[Reminder.NoticeStep] 的判定本来就有测试，但那些测试
//     是直接叫它的。没人验过它挂上去之后会不会真的被派发——而这一层此前正是
//     卡在这里：判定写好了，线一直没接。
//   - **观察者顺手改了步骤**。它只该重置状态，永远调 next。哪天它开始表态，
//     一次「用户插了话」就会连带把这个步骤否掉。

package repeattoolreminder_test

import (
	"context"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/guard/repeattoolreminder"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// stepAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent。
type stepAgent struct {
	id    sessionlog.SessionID
	live  *session.Session
	owner *scope.Scope
}

func (a *stepAgent) ID() sessionlog.SessionID  { return a.id }
func (a *stepAgent) Options() agent.Options    { return agent.Options{} }
func (a *stepAgent) Session() *session.Session { return a.live }
func (a *stepAgent) Inbox() *agent.Inbox       { return nil }
func (a *stepAgent) Status() agent.Status      { return agent.Status("") }
func (a *stepAgent) Scope() *scope.Scope       { return a.owner }

func (a *stepAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions)         {}
func (a *stepAgent) WhenIdle(context.Context) error                                    { return nil }
func (a *stepAgent) RunMaintenance(context.Context, func(context.Context) error) error { return nil }
func (a *stepAgent) Send(llm.Message, agent.InboxTarget, bool)                         {}
func (a *stepAgent) Followup(llm.Message)                                              {}
func (a *stepAgent) Steer(llm.Message)                                                 {}
func (a *stepAgent) Inject(llm.Message)                                                {}
func (a *stepAgent) Prepend(llm.Message, agent.InboxTarget)                            {}
func (a *stepAgent) Remove(llm.MessageID)                                              {}
func (a *stepAgent) Replace(llm.MessageID, llm.Message)                                {}

// stepStage 是一次接线测试要的家当：装好这一层的工具注册表，加上一张登记了
// 那个假 agent 的 agent 注册表。
type stepStage struct {
	harness *harness
	agents  *agent.Registry
	actor   *stepAgent
}

// newStepStage 把两条线都接上：执行后那条计数，每步之前那条断链。
func newStepStage(t *testing.T, config repeattoolreminder.Config, toolName string) *stepStage {
	t.Helper()
	h := newHarness(t, config, toolName)

	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })

	store, err := session.NewStore(session.StoreOptions{})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	live, err := store.Create(context.Background(), owner, "s1", session.CreateOptions{})
	if err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	if _, err := h.reminder.InstallStepNotice(context.Background(), agents, owner); err != nil {
		t.Fatalf("接断链线失败：%v", err)
	}

	// 链的键是 agent 那把作用域钥匙，所以工具调用和步骤观察者必须报同一把——
	// 循环那边两处都取 a.scope，测试这里也让它们同源。
	actor := &stepAgent{id: live.ID(), live: live, owner: h.agentScope}
	detach, err := agents.Register(context.Background(), actor, nil)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	return &stepStage{harness: h, agents: agents, actor: actor}
}

// resolve 走一遍每步之前那条瀑布，交出最里面那个提议有没有原样出来。
func (s *stepStage) resolve(t *testing.T, messages []llm.Message) {
	t.Helper()
	entered := false
	decision, err := s.agents.ResolvePreStep(
		context.Background(),
		agent.PreStep{Agent: s.actor, Messages: messages, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			entered = true
			return agent.EnterStep(messages), nil
		},
	)
	if err != nil {
		t.Fatalf("走每步之前那条瀑布失败：%v", err)
	}
	if !entered {
		t.Fatal("这条观察者只该重置状态，不许不调 next")
	}
	if !decision.Enter {
		t.Fatal("这条观察者不许把步骤否掉")
	}
}

func TestStepNoticeBreaksTheChainThroughTheRegistry(t *testing.T) {
	t.Parallel()
	s := newStepStage(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	h := s.harness

	silent(t, h.run(t, "read", `{}`))
	s.resolve(t, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "换个思路"}}, llm.UserSource{}),
	})
	silent(t, h.run(t, "read", `{}`))
	if got := only(t, h.run(t, "read", `{}`)); got == "" {
		t.Fatal("插话之后该重新数")
	}
}

func TestStepNoticeLeavesTheChainAloneWithoutAUserMessage(t *testing.T) {
	t.Parallel()
	s := newStepStage(t, repeattoolreminder.Config{Thresholds: []int{2}}, "read")
	h := s.harness

	silent(t, h.run(t, "read", `{}`))
	// 循环每一步都会走这条瀑布，绝大多数步骤里一条用户消息都没有。那些步骤
	// 必须对这条链毫无影响，否则这一层永远数不到 2。
	s.resolve(t, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "某个插件说的"}}, llm.PluginSource{Plugin: "other"}),
	})
	s.resolve(t, nil)
	if got := only(t, h.run(t, "read", `{}`)); got == "" {
		t.Fatal("没有用户插话就不该断链")
	}
}

func TestInstallStepNoticeRejectsNilRegistry(t *testing.T) {
	t.Parallel()
	reminder, err := repeattoolreminder.New(repeattoolreminder.Config{})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := reminder.InstallStepNotice(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有 agent 注册表就装不上，该报错")
	}
}
