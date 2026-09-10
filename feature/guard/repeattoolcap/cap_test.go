// 本文件的作用：把这一层的判定、拒绝、和清零全部钉住——什么时候拦、拦下来
// 工具还跑不跑、什么会把计数清零、以及它和别的执行前规则怎么相处。
//
// # 这些测试防的是什么错
//
//   - **拦了但工具照样跑**。这一层的全部价值就是「不派发」。判定对了而执行体
//     还是跑了一遍，等于没拦。
//   - **被后面的规则改回放行**。到顶那一下必须短路，不许调 next——否则任何一条
//     登记在后面的放行规则都能把这道刹车松开。
//   - **没配上限的工具被误伤**。默认什么都不限是这一层的承诺，破了它每个接入方
//     都会踩一次坑。
//   - **撞了上限之后反而被放行**。计数在判断之前推进，所以撞得越多次数越大，
//     不该有任何一次因为「数得太多」而漏过去。

package repeattoolcap_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/guard/repeattoolcap"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// harness 是一次测试要的全套家当：注册表、装好的这一层、有身份的作用域，
// 以及每个工具各自被真正执行了多少次。
type harness struct {
	runtime *tools.Runtime
	cap     *repeattoolcap.Cap
	agent   *scope.Key
	// agentScope 是 agent 那把钥匙所在的作用域。计数的键是钥匙本身，而
	// [repeattoolcap.Cap.InstallStepNotice] 只拿得到作用域，所以两者必须同源。
	agentScope *scope.Scope
	runs       map[string]*atomic.Int64
}

// newHarness 造一个注册表，装上这一层，注册一批会自己记账的工具。
func newHarness(t *testing.T, config repeattoolcap.Config, toolNames ...string) *harness {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	capped, err := repeattoolcap.New(config)
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := capped.Install(context.Background(), runtime, scope.NewRoot()); err != nil {
		t.Fatalf("装这一层失败：%v", err)
	}
	runs := map[string]*atomic.Int64{}
	for _, name := range toolNames {
		counter := &atomic.Int64{}
		runs[name] = counter
		if _, err := runtime.Register(context.Background(), scope.NewRoot(), countingTool(name, counter)); err != nil {
			t.Fatalf("注册 %q 失败：%v", name, err)
		}
	}
	agentScope, err := scope.New(scope.NewKey("agent"), scope.Options{})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = agentScope.Dispose(context.Background()) })
	return &harness{
		runtime:    runtime,
		cap:        capped,
		agent:      agentScope.Key(),
		agentScope: agentScope,
		runs:       runs,
	}
}

// countingTool 造一个收任意参数、每跑一次就记一笔的工具。
//
// 记账是这些测试的关键：这一层的承诺是「不派发」，只验裁决验不出执行体有没有跑。
func countingTool(name string, counter *atomic.Int64) *tools.Definition {
	return &tools.Definition{
		Name:        name,
		Description: name,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeString},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: "ok"}}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			counter.Add(1)
			return json.Marshal("ok")
		},
	}
}

// run 跑一次调用，交出它最终那份结果。
func (h *harness) run(t *testing.T, name string) tools.Result {
	t.Helper()
	return h.runtime.Execute(context.Background(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      name,
		Arguments: json.RawMessage(`{}`),
		Agent:     h.agent,
	})
}

// pass 断言一次调用没被拦。
func (h *harness) pass(t *testing.T, name string) {
	t.Helper()
	if result := h.run(t, name); result.IsError {
		t.Fatalf("这次调用不该被拦：%+v", result.Error)
	}
}

// blocked 断言一次调用被拦了，并交出模型看到的那句话。
func (h *harness) blocked(t *testing.T, name string) string {
	t.Helper()
	result := h.run(t, name)
	if !result.IsError {
		t.Fatal("这次调用该被拦下来")
	}
	var parts []string
	for _, block := range result.Content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ran 交出一个工具的执行体到目前为止跑了多少次。
func (h *harness) ran(name string) int64 { return h.runs[name].Load() }

func TestBlocksTheCallAfterTheLimitAndNeverDispatchesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")

	h.pass(t, "read")
	h.pass(t, "read")
	reason := h.blocked(t, "read")

	if h.ran("read") != 2 {
		t.Fatalf("被拦下的那次不该派发，执行体跑了 %d 次", h.ran("read"))
	}
	// 三件事一句都不能少，理由见 denyReason 的注释。
	for _, want := range []string{"read", "3 times in a row", "limit of 2", "Blocked:", "different"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("那句话里少了 %q：\n%s", want, reason)
		}
	}
}

func TestKeepsBlockingAndKeepsCountingPastTheLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 1}}, "read")

	h.pass(t, "read")
	if got := h.blocked(t, "read"); !strings.Contains(got, "2 times in a row") {
		t.Fatalf("第二次该报 2：%s", got)
	}
	// 一个反复撞同一道拒绝的模型，不该因为撞得多了反而被放行。
	if got := h.blocked(t, "read"); !strings.Contains(got, "3 times in a row") {
		t.Fatalf("被拦下的那次也该计数，第三次该报 3：%s", got)
	}
	if h.ran("read") != 1 {
		t.Fatalf("只有第一次该真的跑，执行体跑了 %d 次", h.ran("read"))
	}
}

func TestADifferentCappedToolResetsTheCount(t *testing.T) {
	t.Parallel()
	// 两个都配了上限：只有配了上限的工具才断链，没配的那些是透明的，见下一条。
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2, "write": 2}}, "read", "write")

	h.pass(t, "read")
	h.pass(t, "read")
	h.pass(t, "write")
	h.pass(t, "read")
	h.pass(t, "read")
	h.blocked(t, "read")
}

func TestUntrackedToolsAreNeverCapped(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 1}}, "read", "write")

	for i := 0; i < 20; i++ {
		h.pass(t, "write")
	}
	if h.ran("write") != 20 {
		t.Fatalf("没配上限的工具一次都不该被拦，执行体跑了 %d 次", h.ran("write"))
	}
	// 没被跟踪的调用也不参与计数，所以它不该把 read 那条链断掉——换句话说，
	// 这一层对它是彻底透明的。
	h.pass(t, "read")
	h.pass(t, "write")
	h.blocked(t, "read")
}

func TestAnEmptyConfigCapsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{}, "read")

	for i := 0; i < 20; i++ {
		h.pass(t, "read")
	}
}

func TestCountsAreKeyedPerAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")
	other := scope.NewKey("other")

	h.pass(t, "read")
	h.pass(t, "read")
	// 另一个 agent 调同一件事：它自己的计数才刚开始。
	for i := 0; i < 3; i++ {
		elsewhere := h.runtime.Execute(context.Background(), tools.ExecutionInput{
			CallID: llm.CallID("c"), Name: "read", Arguments: json.RawMessage(`{}`), Agent: other,
		})
		if i < 2 && elsewhere.IsError {
			t.Fatalf("别的 agent 头两次不该被拦：%+v", elsewhere.Error)
		}
	}
	// 这条链还停在 2 上，下一次才该撞上限。
	h.blocked(t, "read")
}

func TestDirectExecutesWithoutAnAgentAreIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 1}}, "read")

	for i := 0; i < 5; i++ {
		result := h.runtime.Execute(context.Background(), tools.ExecutionInput{
			CallID: llm.CallID("c"), Name: "read", Arguments: json.RawMessage(`{}`),
		})
		if result.IsError {
			t.Fatalf("没有身份的调用不该被拦：%+v", result.Error)
		}
	}
}

func TestCappedCallShortCircuitsLaterRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 1}}, "read")
	entered := &atomic.Int64{}
	if _, err := h.runtime.PreExecute(context.Background(), scope.NewRoot(),
		func(tools.Execution, func() (tools.PreDecision, error)) (tools.PreDecision, error) {
			entered.Add(1)
			return tools.PreDecision{Kind: tools.PreAllow}, nil
		}); err != nil {
		t.Fatalf("装执行前规则失败：%v", err)
	}

	h.pass(t, "read")
	before := entered.Load()
	h.blocked(t, "read")
	// 到顶这件事没有商量余地：后面登记的规则一条都不许跑，否则任何一条放行
	// 规则都能把这道刹车松开。
	if entered.Load() != before {
		t.Fatal("到顶那一次不该再往下走")
	}
}

func TestObserveReportsTheCountAndLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")
	observe := func(name string) (int, int, bool) {
		return h.cap.Observe(tools.Execution{ExecutionInput: tools.ExecutionInput{
			CallID: llm.CallID("c"), Name: name, Arguments: json.RawMessage(`{}`), Agent: h.agent,
		}})
	}

	if count, limit, capped := observe("read"); count != 1 || limit != 2 || capped {
		t.Fatalf("第一次该是 1/2 且不拦，拿到 %d/%d/%v", count, limit, capped)
	}
	if count, limit, capped := observe("read"); count != 2 || limit != 2 || capped {
		t.Fatalf("第二次该是 2/2 且不拦，拿到 %d/%d/%v", count, limit, capped)
	}
	if count, limit, capped := observe("read"); count != 3 || limit != 2 || !capped {
		t.Fatalf("第三次该是 3/2 且要拦，拿到 %d/%d/%v", count, limit, capped)
	}
	if count, limit, capped := observe("write"); count != 0 || limit != 0 || capped {
		t.Fatalf("没配上限的工具该是 0/0/false，拿到 %d/%d/%v", count, limit, capped)
	}
}

func TestUserInterjectionResetsTheCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")

	h.pass(t, "read")
	h.pass(t, "read")
	h.cap.NoticeStep(h.agent, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "再查一次"}}, llm.UserSource{}),
	})
	h.pass(t, "read")
	h.pass(t, "read")
	h.blocked(t, "read")
}

func TestNonUserMessagesDoNotResetTheCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")

	h.pass(t, "read")
	h.pass(t, "read")
	// 插件自己注入的上下文不是用户插话，计数不该清零。
	h.cap.NoticeStep(h.agent, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "某个插件说的"}}, llm.PluginSource{Plugin: "other"}),
	})
	h.cap.NoticeStep(nil, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "没有 agent"}}, llm.UserSource{}),
	})
	h.blocked(t, "read")
}

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

// newStepStage 把两条线都接上：执行前那条闸门，每步之前那条清零。
func newStepStage(t *testing.T, config repeattoolcap.Config, toolName string) (*harness, *agent.Registry, *stepAgent) {
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
	if _, err := h.cap.InstallStepNotice(context.Background(), agents, owner); err != nil {
		t.Fatalf("接清零线失败：%v", err)
	}

	// 计数的键是 agent 那把作用域钥匙，所以工具调用和步骤观察者必须报同一把。
	actor := &stepAgent{id: live.ID(), live: live, owner: h.agentScope}
	detach, err := agents.Register(context.Background(), actor, nil)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })

	return h, agents, actor
}

// resolveStep 走一遍每步之前那条瀑布，顺便钉住这条观察者不表态。
func resolveStep(t *testing.T, agents *agent.Registry, actor *stepAgent, messages []llm.Message) {
	t.Helper()
	entered := false
	decision, err := agents.ResolvePreStep(
		context.Background(),
		agent.PreStep{Agent: actor, Messages: messages, Turn: 1, Step: 1},
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

func TestStepNoticeResetsTheCountThroughTheRegistry(t *testing.T) {
	t.Parallel()
	h, agents, actor := newStepStage(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")

	h.pass(t, "read")
	h.pass(t, "read")
	resolveStep(t, agents, actor, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "再查一次"}}, llm.UserSource{}),
	})
	h.pass(t, "read")
	h.pass(t, "read")
	h.blocked(t, "read")
}

func TestStepNoticeLeavesTheCountAloneWithoutAUserMessage(t *testing.T) {
	t.Parallel()
	h, agents, actor := newStepStage(t, repeattoolcap.Config{PerTool: map[string]int{"read": 2}}, "read")

	h.pass(t, "read")
	h.pass(t, "read")
	// 循环每一步都会走这条瀑布，绝大多数步骤里一条用户消息都没有。那些步骤
	// 必须对计数毫无影响，否则这道闸门永远不会落下。
	resolveStep(t, agents, actor, []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "某个插件说的"}}, llm.PluginSource{Plugin: "other"}),
	})
	resolveStep(t, agents, actor, nil)
	h.blocked(t, "read")
}

func TestInstallRejectsNilRuntime(t *testing.T) {
	t.Parallel()
	capped, err := repeattoolcap.New(repeattoolcap.Config{})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}
	if _, err := capped.Install(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有工具注册表就装不上，该报错")
	}
	if _, err := capped.InstallStepNotice(context.Background(), nil, scope.NewRoot()); err == nil {
		t.Fatal("没有 agent 注册表就装不上，该报错")
	}
}

func TestConfigValidationFailsLoud(t *testing.T) {
	t.Parallel()
	cases := map[string]repeattoolcap.Config{
		"工具名是空的": {PerTool: map[string]int{"": 3}},
		"上限是 0":  {PerTool: map[string]int{"read": 0}},
		"上限是负数":  {PerTool: map[string]int{"read": -1}},
	}
	for label, config := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			_, err := repeattoolcap.New(config)
			if err == nil {
				t.Fatal("这份配置该被拒绝")
			}
			if !errors.Is(err, repeattoolcap.ErrInvalidConfig) {
				t.Fatalf("该认得出哨兵：%v", err)
			}
		})
	}
}
