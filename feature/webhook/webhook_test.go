// 本文件验这个包对外的三件事：那张规则表怎么收登记、一次投递怎么散出去，
// 以及一条规则要来的会话是按什么次序建起来、失败了又怎么退回去。
//
// 派发是发后不管的，所以每一条用例都得自己找一个收敛点：规则回调里关一个 channel
// 只说明规则跑完了，建会话那一段还在后面——排干那条登记才是「它真的收完了」。
// 这件事收在 helpers_test.go 的 settle 里。

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
)

// ---- 造运行时 ----

func TestNewRejectsAnIncompleteConfig(t *testing.T) {
	full := func() Config {
		return Config{
			Workspaces:   &fakeWorkspaces{},
			Agents:       &fakeAgents{},
			AgentPresets: &fakePresets{},
			DefaultModel: fakeDefaultModel{},
			Permissions:  &fakePermissions{},
			Rename:       func(agent.Agent, string) error { return nil },
			Owner:        scopeRoot(t),
			Report:       func(Failure) {},
		}
	}
	if _, err := New(full()); err != nil {
		t.Fatalf("配齐了该造得出来：%v", err)
	}

	holes := map[string]func(*Config){
		"Workspaces":   func(c *Config) { c.Workspaces = nil },
		"Agents":       func(c *Config) { c.Agents = nil },
		"AgentPresets": func(c *Config) { c.AgentPresets = nil },
		"DefaultModel": func(c *Config) { c.DefaultModel = nil },
		"Permissions":  func(c *Config) { c.Permissions = nil },
		"Rename":       func(c *Config) { c.Rename = nil },
		"Owner":        func(c *Config) { c.Owner = nil },
		"Report":       func(c *Config) { c.Report = nil },
	}
	for name, punch := range holes {
		config := full()
		punch(&config)
		if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("缺 %s 该拒，实际 %v", name, err)
		}
	}
}

// ---- 登记 ----

func TestRegisterRejectsAMalformedRule(t *testing.T) {
	h := newHarness(t)
	run := func(context.Context, VerifiedDelivery) (*SessionRequest, error) { return nil, nil }

	bad := []Rule{
		{Kind: "github", Run: run},
		{ID: "r", Run: run},
		{ID: "r", Kind: "github"},
	}
	for _, rule := range bad {
		if _, err := h.runtime.Register(t.Context(), rule); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("规则 %+v 该被拒，实际 %v", rule, err)
		}
	}
}

func TestRegisterRejectsASecondRuleWithTheSameID(t *testing.T) {
	h := newHarness(t)
	rule := Rule{ID: "r", Kind: "github", Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
		return nil, nil
	}}
	registerRule(t, h.runtime, rule)
	if _, err := h.runtime.Register(t.Context(), rule); !errors.Is(err, ErrDuplicateRule) {
		t.Fatalf("同一个 id 该被拒，实际 %v", err)
	}
}

// 撤掉之后那个 id 该重新腾出来，否则一条规则换实现就得换名字。
func TestRegisterAcceptsTheSameIDAfterItIsReleased(t *testing.T) {
	h := newHarness(t)
	rule := Rule{ID: "r", Kind: "github", Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
		return nil, nil
	}}
	dispose := registerRule(t, h.runtime, rule)
	dispose()
	registerRule(t, h.runtime, rule)
}

func TestCancelingRegistrationContextReleasesTheRule(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	ran := make(chan struct{}, 1)
	rule := Rule{ID: "temporary", Kind: "github", Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
		ran <- struct{}{}
		return nil, nil
	}}
	if _, err := h.runtime.Register(ctx, rule); err != nil {
		t.Fatalf("登记规则失败：%v", err)
	}
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		h.runtime.mutex.Lock()
		_, exists := h.runtime.rules[rule.ID]
		h.runtime.mutex.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("登记上下文取消后规则仍未释放")
		}
		time.Sleep(time.Millisecond)
	}
	if err := h.runtime.Dispatch(delivery("github")); err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	h.runtime.Close()
	select {
	case <-ran:
		t.Fatal("登记上下文取消后规则仍收到了投递")
	default:
	}
}

func TestRegisterAndDispatchAreRefusedAfterClose(t *testing.T) {
	h := newHarness(t)
	h.runtime.Close()

	rule := Rule{ID: "r", Kind: "github", Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
		return nil, nil
	}}
	if _, err := h.runtime.Register(t.Context(), rule); !errors.Is(err, ErrClosing) {
		t.Errorf("关掉之后不该再收登记，实际 %v", err)
	}
	if err := h.runtime.Dispatch(delivery("github")); !errors.Is(err, ErrClosing) {
		t.Errorf("关掉之后不该再派投递，实际 %v", err)
	}
}

// ---- 派发 ----

func TestDispatchRejectsAMalformedDelivery(t *testing.T) {
	h := newHarness(t)

	holes := map[string]func(*VerifiedDelivery){
		"kind":       func(d *VerifiedDelivery) { d.Kind = "" },
		"source":     func(d *VerifiedDelivery) { d.Source = "" },
		"id":         func(d *VerifiedDelivery) { d.DeliveryID = "" },
		"receivedAt": func(d *VerifiedDelivery) { d.ReceivedAt = time.Time{} },
		"event":      func(d *VerifiedDelivery) { d.Event = json.RawMessage(`{"action":`) },
	}
	for name, punch := range holes {
		bad := delivery("github")
		punch(&bad)
		if err := h.runtime.Dispatch(bad); !errors.Is(err, ErrInvalidDelivery) {
			t.Errorf("%s 不合法时该被拒，实际 %v", name, err)
		}
	}
}

func TestDispatchOnlyReachesRulesThatClaimThatKind(t *testing.T) {
	h := newHarness(t)
	reached := &journal{}

	registerRule(t, h.runtime, Rule{ID: "gh", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			reached.add("gh")
			return nil, nil
		}})
	registerRule(t, h.runtime, Rule{ID: "gl", Kind: "gitlab",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			reached.add("gl")
			return nil, nil
		}})

	settle(t, h, delivery("github"), func() bool { return len(reached.snapshot()) >= 1 })

	if got := reached.snapshot(); !reflect.DeepEqual(got, []string{"gh"}) {
		t.Fatalf("只有认领 github 的那条该跑到，实际 %v", got)
	}
}

// Dispatch 是发后不管的：它不能等规则跑完。
func TestDispatchReturnsBeforeTheRuleFinishes(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})

	registerRule(t, h.runtime, Rule{ID: "slow", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			close(entered)
			<-release
			return nil, nil
		}})

	if err := h.runtime.Dispatch(delivery("github")); err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("规则没被叫起来")
	}
	// 规则还堵在 release 上，而 Dispatch 早就回来了——这就是要验的那一条。
	close(release)
	h.runtime.Close()
}

func TestARuleThatWantsNothingCreatesNoSession(t *testing.T) {
	h := newHarness(t)
	ran := &journal{}
	registerRule(t, h.runtime, Rule{ID: "quiet", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			ran.add("quiet")
			return nil, nil
		}})

	settle(t, h, delivery("github"), func() bool { return len(ran.snapshot()) >= 1 })

	if steps := h.journal.snapshot(); len(steps) != 0 {
		t.Fatalf("不该动任何协作方，实际 %v", steps)
	}
	if failures := h.reported(); len(failures) != 0 {
		t.Fatalf("这是一条正常的路，不该上报，实际 %v", failures)
	}
}

// 规则自己炸了没人接得住——Dispatch 早就返回了，Report 是唯一能看见它的地方。
func TestARuleThatFailsIsReported(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("规则自己炸了")
	registerRule(t, h.runtime, Rule{ID: "boom", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return nil, boom
		}})

	settle(t, h, delivery("github"), h.afterFailures(1))

	failures := h.reported()
	if len(failures) != 1 {
		t.Fatalf("该上报一条，实际 %v", failures)
	}
	got := failures[0]
	if !errors.Is(got.Err, boom) {
		t.Errorf("上报的该是原错，实际 %v", got.Err)
	}
	if got.RuleID != "boom" || got.Kind != "github" || got.Source != "primary-github" || got.DeliveryID != "d-1" {
		t.Errorf("出处字段不对：%+v", got)
	}
	if got.Stopped {
		t.Error("这次是自己出错的，不是被停掉的")
	}
}

// 撤销函数要等在跑的那些退出，而且它们的失败要标成「被停掉的」而不是「失败的」。
func TestReleasingARegistrationWaitsAndMarksStopped(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{})
	returned := make(chan struct{})

	dispose := registerRule(t, h.runtime, Rule{ID: "hang", Kind: "github",
		Run: func(ctx context.Context, _ VerifiedDelivery) (*SessionRequest, error) {
			close(entered)
			<-ctx.Done()
			close(returned)
			return nil, context.Cause(ctx)
		}})

	if err := h.runtime.Dispatch(delivery("github")); err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("规则没被叫起来")
	}

	dispose()

	select {
	case <-returned:
	default:
		t.Fatal("撤销该等到在跑的那次退出")
	}
	failures := h.reported()
	if len(failures) != 1 {
		t.Fatalf("该上报一条，实际 %v", failures)
	}
	if !failures[0].Stopped {
		t.Errorf("这次是被停掉的，该标上：%+v", failures[0])
	}
}

// ---- 建会话 ----

// 这条用例钉的是次序本身：三件只读的事先做完，再动介质。
func TestCreatingASessionFollowsTheOrderedTransaction(t *testing.T) {
	h := newHarness(t)
	registerRule(t, h.runtime, Rule{ID: "open", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return request(), nil
		}})

	want := []string{
		"permission.resolve", "preset.resolve", "preset.standing",
		"workspace", "agent", "preset.mount", "onrequest",
		"attach", "permission.set", "rename", "followup",
	}
	settle(t, h, delivery("github"), h.afterSteps(len(want)))

	if failures := h.reported(); len(failures) != 0 {
		t.Fatalf("这一趟该一路顺，实际 %v", failures)
	}
	if steps := h.journal.snapshot(); !reflect.DeepEqual(steps, want) {
		t.Fatalf("次序不对：\n want %v\n  got %v", want, steps)
	}

	options := h.agents.createOptions()
	if options.SessionID != "session-1" {
		t.Errorf("会话身份该是 session-1，实际 %q", options.SessionID)
	}
	// 工作区要在 agent 建出来之前定下来：挂上那一步会比会话头上记的工作区。
	if options.WorkspaceID != "ws-1" {
		t.Errorf("工作区该是 ws-1，实际 %q", options.WorkspaceID)
	}
	if options.AgentPreset != "coder" {
		t.Errorf("预设该是 coder，实际 %q", options.AgentPreset)
	}
	if options.AgentOptions.Provider != "deepseek" || options.AgentOptions.Model != "chat" {
		t.Errorf("没显式挑路由时该用部署默认，实际 %+v", options.AgentOptions)
	}
	if h.permissions.pinned() != "guarded" {
		t.Errorf("档位该钉成 guarded，实际 %q", h.permissions.pinned())
	}
	if h.title() != "修一个 issue" {
		t.Errorf("标题不对：%q", h.title())
	}
	if h.workspaces.path != "/repos/demo" {
		t.Errorf("工作区路径不对：%q", h.workspaces.path)
	}
	// 标题给空串，让工作区自己回落到路径的最后一段。
	if h.workspaces.title != "" {
		t.Errorf("工作区标题该留空，实际 %q", h.workspaces.title)
	}

	admitted := h.agent.admitted()
	if len(admitted) != 1 {
		t.Fatalf("该放行一条提示词，实际 %d 条", len(admitted))
	}
	provenance, ok, err := ProvenanceOf(admitted[0].Source)
	if err != nil || !ok {
		t.Fatalf("放行的那条该带本包的来源：ok=%v err=%v", ok, err)
	}
	want4 := Provenance{Provider: "github", Source: "primary-github", DeliveryID: "d-1", RuleID: "open"}
	if provenance != want4 {
		t.Errorf("来源不对：%+v", provenance)
	}
}

// 只读那一段任何一件不成立，介质就一步都不该动。
func TestAFailedReadOnlyCheckTouchesNoMedium(t *testing.T) {
	cases := map[string]struct {
		arm  func(*harness)
		want []string
	}{
		"档位名不认识": {
			arm:  func(h *harness) { h.permissions.resolveErr = errors.New("没这个档位") },
			want: []string{"permission.resolve"},
		},
		"预设不在": {
			arm:  func(h *harness) { h.presets.resolveErr = errors.New("没这份预设") },
			want: []string{"permission.resolve", "preset.resolve"},
		},
		"常驻组合装不起来": {
			arm:  func(h *harness) { h.presets.standingErr = errors.New("装不起来") },
			want: []string{"permission.resolve", "preset.resolve", "preset.standing"},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			testCase.arm(h)
			registerRule(t, h.runtime, Rule{ID: "open", Kind: "github",
				Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
					return request(), nil
				}})

			settle(t, h, delivery("github"), h.afterFailures(1))

			if steps := h.journal.snapshot(); !reflect.DeepEqual(steps, testCase.want) {
				t.Fatalf("次序不对：\n want %v\n  got %v", testCase.want, steps)
			}
		})
	}
}

// 动过介质之后失败，已经做过的要按逆序退回去。
func TestAFailureAfterAdmissionRollsBackInReverseOrder(t *testing.T) {
	h := newHarness(t)
	h.renameErr = errors.New("起名失败")
	registerRule(t, h.runtime, Rule{ID: "open", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return request(), nil
		}})

	settle(t, h, delivery("github"), h.afterFailures(1))

	want := []string{
		"permission.resolve", "preset.resolve", "preset.standing",
		"workspace", "agent", "preset.mount", "onrequest",
		"attach", "permission.set", "rename", "detach", "dispose",
	}
	if steps := h.journal.snapshot(); !reflect.DeepEqual(steps, want) {
		t.Fatalf("次序不对：\n want %v\n  got %v", want, steps)
	}
	if !h.agents.wasDisposed() {
		t.Error("那个 agent 该被处置掉")
	}
	if admitted := h.agent.admitted(); len(admitted) != 0 {
		t.Errorf("提示词还没放行，不该有：%v", admitted)
	}
	failures := h.reported()
	if len(failures) != 1 || !errors.Is(failures[0].Err, h.renameErr) {
		t.Fatalf("该只上报那个死因，实际 %v", failures)
	}
}

// 还没挂上就失败，就没有可摘的——只处置 agent。
func TestAFailureBeforeAttachOnlyDisposesTheAgent(t *testing.T) {
	h := newHarness(t)
	h.workspace.attachErr = errors.New("挂不上")
	registerRule(t, h.runtime, Rule{ID: "open", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return request(), nil
		}})

	settle(t, h, delivery("github"), h.afterFailures(1))

	steps := h.journal.snapshot()
	for _, step := range steps {
		if step == "detach" {
			t.Fatalf("没挂上就不该摘：%v", steps)
		}
	}
	if !h.agents.wasDisposed() {
		t.Errorf("那个 agent 该被处置掉：%v", steps)
	}
}

// 回滚本身再失败也不顶掉原来那个错，只另报一条。
func TestCancellationDoesNotCancelRollback(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	h.workspace.onAttach = cancel
	if _, err := h.runtime.Register(ctx, Rule{ID: "open", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return request(), nil
		}}); err != nil {
		t.Fatalf("登记规则失败：%v", err)
	}

	settle(t, h, delivery("github"), h.afterFailures(1))

	if err := h.workspace.detachContextError(); err != nil {
		t.Errorf("工作区回滚继承了取消：%v", err)
	}
	if err := h.agents.disposalContextError(); err != nil {
		t.Errorf("agent 回滚继承了取消：%v", err)
	}
}

func TestARollbackFailureIsReportedSeparately(t *testing.T) {
	h := newHarness(t)
	h.renameErr = errors.New("起名失败")
	h.workspace.detachErr = errors.New("摘不掉")
	registerRule(t, h.runtime, Rule{ID: "open", Kind: "github",
		Run: func(context.Context, VerifiedDelivery) (*SessionRequest, error) {
			return request(), nil
		}})

	settle(t, h, delivery("github"), h.afterFailures(2))

	failures := h.reported()
	if len(failures) != 2 {
		t.Fatalf("该上报两条，实际 %v", failures)
	}
	if failures[0].Rollback == "" || !errors.Is(failures[0].Err, h.workspace.detachErr) {
		t.Errorf("头一条该是那次回滚失败：%+v", failures[0])
	}
	if failures[1].Rollback != "" || !errors.Is(failures[1].Err, h.renameErr) {
		t.Errorf("第二条该是那个死因：%+v", failures[1])
	}
}

// ---- 解析请求 ----

func TestResolveRequestRejectsAnIncompleteRequest(t *testing.T) {
	h := newHarness(t)

	holes := map[string]func(*SessionRequest){
		"workspacePath":    func(r *SessionRequest) { r.WorkspacePath = "  " },
		"title":            func(r *SessionRequest) { r.Title = "" },
		"prompt":           func(r *SessionRequest) { r.Prompt = "" },
		"agentPreset":      func(r *SessionRequest) { r.AgentPreset = "" },
		"permissionPreset": func(r *SessionRequest) { r.PermissionPreset = "" },
		"model.provider":   func(r *SessionRequest) { r.Model = &ModelRoute{Model: "chat"} },
		"model.model":      func(r *SessionRequest) { r.Model = &ModelRoute{Provider: "deepseek"} },
		"model.maxTokens":  func(r *SessionRequest) { r.Model = &ModelRoute{Provider: "p", Model: "m", MaxTokens: -1} },
	}
	for name, punch := range holes {
		req := request()
		punch(req)
		if _, err := h.runtime.resolveRequest(*req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s 不合法时该被拒，实际 %v", name, err)
		}
	}
}

// 缺席时连推理档位一起继承部署默认；显式挑了路由就不带档位。
func TestResolveRequestPicksTheModelRoute(t *testing.T) {
	h := newHarness(t)

	resolved, err := h.runtime.resolveRequest(*request())
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := agent.ModelSelection{Provider: "deepseek", Model: "chat", ReasoningEffort: "high"}
	if resolved.selection != want {
		t.Errorf("缺席时该整份继承部署默认，实际 %+v", resolved.selection)
	}
	if resolved.options.MaxTokens != 0 {
		t.Errorf("部署默认那一份不带输出上限，实际 %d", resolved.options.MaxTokens)
	}

	req := request()
	req.Model = &ModelRoute{Provider: "other", Model: "big", MaxTokens: 512}
	resolved, err = h.runtime.resolveRequest(*req)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if resolved.selection != (agent.ModelSelection{Provider: "other", Model: "big"}) {
		t.Errorf("显式挑了路由就不该带推理档位，实际 %+v", resolved.selection)
	}
	if resolved.options.MaxTokens != 512 {
		t.Errorf("输出上限该传下去，实际 %d", resolved.options.MaxTokens)
	}
}

// ---- 投递与来源 ----

// 调用方在 Dispatch 返回之后改那段字节，不该改到正在跑的规则手里的事件体。
func TestDispatchSnapshotsTheEventBody(t *testing.T) {
	h := newHarness(t)
	seen := make(chan string, 1)
	entered := make(chan struct{})
	mutated := make(chan struct{})
	registerRule(t, h.runtime, Rule{ID: "peek", Kind: "github",
		Run: func(_ context.Context, d VerifiedDelivery) (*SessionRequest, error) {
			close(entered)
			<-mutated
			seen <- string(d.Event)
			return nil, nil
		}})

	incoming := delivery("github")
	if err := h.runtime.Dispatch(incoming); err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	// 得先等它真的进了 Run：撤登记会在叫规则**之前**就把这次调用截掉，
	// 那样这条用例要验的那件事根本没发生过。
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("规则没被叫起来")
	}
	for i := range incoming.Event {
		incoming.Event[i] = 'x'
	}
	close(mutated)
	h.runtime.Close()

	if got := <-seen; got != `{"action":"opened"}` {
		t.Fatalf("事件体被外面改到了：%q", got)
	}
}

func TestNewSourceRoundTripsItsProvenance(t *testing.T) {
	provenance := Provenance{Provider: "github", Source: "primary-github", DeliveryID: "d-1", RuleID: "open"}
	source, err := NewSource(provenance, "github webhook handled by open")
	if err != nil {
		t.Fatalf("造来源失败：%v", err)
	}
	if source.Plugin != Plugin {
		t.Errorf("Plugin 名不对：%q", source.Plugin)
	}

	// 走一趟介质：日志上存的是排出去的字节，读回来必须还是这四个字段。
	message := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "hi"}}, source)
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("排消息失败：%v", err)
	}
	var back llm.Message
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("读消息失败：%v", err)
	}
	got, ok, err := ProvenanceOf(back.Source)
	if err != nil || !ok {
		t.Fatalf("读回来该认得出是本包发的：ok=%v err=%v", ok, err)
	}
	if got != provenance {
		t.Errorf("四个字段没原样回来：%+v", got)
	}
}

func TestNewSourceNeedsAllFourProvenanceFields(t *testing.T) {
	full := Provenance{Provider: "github", Source: "s", DeliveryID: "d", RuleID: "r"}
	holes := map[string]func(*Provenance){
		"provider":   func(p *Provenance) { p.Provider = "" },
		"source":     func(p *Provenance) { p.Source = "" },
		"deliveryId": func(p *Provenance) { p.DeliveryID = "" },
		"ruleId":     func(p *Provenance) { p.RuleID = "" },
	}
	for name, punch := range holes {
		provenance := full
		punch(&provenance)
		if _, err := NewSource(provenance, "x"); !errors.Is(err, ErrInvalidDelivery) {
			t.Errorf("缺 %s 时该被拒，实际 %v", name, err)
		}
	}
}

// 别人发的来源不该被认成本包的。
func TestProvenanceOfIgnoresOtherSources(t *testing.T) {
	other := llm.PluginSource{Plugin: "someone-else", Extra: json.RawMessage(`{}`)}
	if _, ok, err := ProvenanceOf(other); ok || err != nil {
		t.Fatalf("别的插件的来源该不认：ok=%v err=%v", ok, err)
	}
}

func TestFailureRendersItsThreeOutcomes(t *testing.T) {
	base := Failure{Kind: "github", Source: "s", DeliveryID: "d", RuleID: "r", Err: errors.New("boom")}
	if got := base.String(); !strings.Contains(got, "failed") {
		t.Errorf("自己出错那一支：%q", got)
	}
	stopped := base
	stopped.Stopped = true
	if got := stopped.String(); !strings.Contains(got, "stopped after disposal") {
		t.Errorf("被停掉那一支：%q", got)
	}
	rollback := base
	rollback.Rollback = "工作区摘除"
	if got := rollback.String(); !strings.Contains(got, "回滚失败") {
		t.Errorf("回滚失败那一支：%q", got)
	}
}
