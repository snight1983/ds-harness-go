// 本文件的作用：一台**装齐了**的服务的测试——续接管理器真的立起来了，那几件可续
// 操作确实转交给它，而只读列举确实从这条服务上答得出来。runtime_test.go 那一份走的
// 是另一半：没组装 agent 服务时这几条各自的答复。

package subagent

import (
	"context"
	"log/slog"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// wiredRuntime 是一台装齐了续接能力和列举服务的服务，外加它周围那几样东西。
type wiredRuntime struct {
	runtime  *Runtime
	agents   *agent.Registry
	sessions *coresession.Store
	factory  *fakeFactory
	owner    *scope.Scope
}

// newWiredRuntime 装一台带续接能力的服务，并登好那个能预备可续孩子的提供方。
//
// 和 newContinuation 那台装配的差别只在宿主：那边挂的是 fakeHost，这边挂的是这条
// 服务自己，于是「服务把活儿转交给管理器」这一段才落在被测范围里。
func newWiredRuntime(t *testing.T) *wiredRuntime {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	projections := projection.NewRegistry()
	dispose, err := RegisterProjections(projections)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)

	owner := keyedScope(t, "subagents", nil)
	store := newPersistence()
	factory := newFactory(t, agents, sessions, store)
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}

	runtime, err := NewRuntime(RuntimeOptions{
		Continuation: ContinuationDeps{
			Owner:       owner,
			Agents:      agents,
			Sessions:    sessions,
			Persistence: store,
			Composition: ChildCompositionServices{SystemPrompt: promptRegistry(t, "")},
		},
		Listing: ListingServices{
			Projections: projections,
			Sessions:    sessions,
			Persistence: store,
		},
		Logger: quiet,
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	register(t, runtime, owner, &preparingProvider{fakeProvider: fakeProvider{name: "spawn"}})

	return &wiredRuntime{
		runtime: runtime, agents: agents, sessions: sessions,
		factory: factory, owner: owner,
	}
}

// parent 造一个活着的父 agent：会话进活会话表，agent 进注册表。
//
// 管理器每一道血统检查都要求父是注册表里那个确切的对象，所以父不能是一个游离的假 agent。
func (w *wiredRuntime) parent(t *testing.T, id sessionlog.SessionID) *childAgent {
	t.Helper()
	agentScope := keyedScope(t, string(id), w.owner.Key())
	live, err := w.sessions.Create(t.Context(), agentScope, id, coresession.CreateOptions{
		WorkspaceID: testWorkspaceID,
	})
	if err != nil {
		t.Fatalf("建父会话失败：%v", err)
	}
	built := newChildAgent(id, agentScope, live)
	detach, err := w.agents.Register(t.Context(), built, nil)
	if err != nil {
		t.Fatalf("登记父 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })
	return built
}

// start 起一个可续孩子，走的是**服务**这条口。
func (w *wiredRuntime) start(t *testing.T, parent agent.Agent, childID sessionlog.SessionID) ContinuableStart {
	t.Helper()
	started, err := w.runtime.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  childID,
		Request:  StartRequest{Prompt: textContent("干活"), Parent: parent},
	})
	if err != nil {
		t.Fatalf("起可续孩子失败：%v", err)
	}
	return started
}

// ---- 构造 ----

// agent 注册表在场，那台管理器就该真的立起来。
func TestNewRuntimeStandsUpTheManagerWhenAgentsArePresent(t *testing.T) {
	manager, err := newWiredRuntime(t).runtime.requireContinuations()
	if err != nil || manager == nil {
		t.Fatalf("该立起一台管理器，实际 manager=%v err=%v", manager, err)
	}
}

// 管理器立不起来就是这台服务立不起来：绝不无声地退回「没组装续接能力」那一档，
// 那会把一次装配错误伪装成一套合法部署。
func TestNewRuntimeRefusesAnUnusableContinuationAssembly(t *testing.T) {
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	// 有 agent 注册表、没有拥有它的作用域：管理器那道构造闸会拒。
	if _, err := NewRuntime(RuntimeOptions{
		Continuation: ContinuationDeps{Agents: agents},
	}); err == nil {
		t.Fatal("装不成的续接组装该把整台服务的构造带崩")
	}
}

// ---- 转交 ----

// 起、投、汇报、打断、放孩子，五件都该落到那台管理器上。
func TestRuntimeHandsContinuableWorkToTheManager(t *testing.T) {
	wired := newWiredRuntime(t)
	parent := wired.parent(t, "parent")
	wired.start(t, parent, "child")
	child := wired.factory.child("child")

	if _, err := wired.runtime.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	); err != nil {
		t.Fatalf("后续投递失败：%v", err)
	}
	// 初始提示词一条、后续一条，都从孩子自己的收件箱过。
	if followups, _, _ := child.delivered(); len(followups) != 2 {
		t.Fatalf("孩子该收到两条，实际 %d", len(followups))
	}

	if _, err := wired.runtime.ReportFrom(
		t.Context(), child, textContent("干完一半了"), ReportOptions{Delivery: DeliveryQuiet},
	); err != nil {
		t.Fatalf("汇报失败：%v", err)
	}
	if _, _, injects := parent.delivered(); len(injects) != 1 {
		t.Fatalf("父该收到那份汇报，实际 %d 条注入", len(injects))
	}

	// 打断留住收件箱，所以这个孩子照旧驻留着。
	if err := wired.runtime.Interrupt("child", InterruptAuthority{
		Kind:            AuthorityUser,
		ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("打断失败：%v", err)
	}
	if len(child.cancelled()) != 1 {
		t.Fatalf("打断该发出一次取消，实际 %d 次", len(child.cancelled()))
	}

	if err := wired.runtime.DrainContinuableChildren(
		t.Context(), parent, []sessionlog.SessionID{"child"},
	); err != nil {
		t.Fatalf("放孩子失败：%v", err)
	}
	if _, still := wired.agents.Get("child"); still {
		t.Fatal("放掉之后那个孩子不该还在注册表里")
	}
}

// 按范围排干同样只是转交：那个根底下的后代放掉，根自己留着。
func TestRuntimeHandsScopedDrainingToTheManager(t *testing.T) {
	wired := newWiredRuntime(t)
	parent := wired.parent(t, "parent")
	wired.start(t, parent, "child")

	if err := wired.runtime.DrainContinuableDescendants(
		t.Context(), []agent.Agent{parent},
	); err != nil {
		t.Fatalf("按范围排干失败：%v", err)
	}
	if _, still := wired.agents.Get("child"); still {
		t.Fatal("排干之后那个后代不该还在注册表里")
	}
	if _, gone := wired.agents.Get("parent"); !gone {
		t.Fatal("那个根自己该留着")
	}
}

// ---- 只读发现 ----

// 列举走的是列举服务那条路，一个 agent 都不装载——所以它和续接管理器毫无关系，
// 但仍旧要从这条服务上答得出来。
func TestRuntimeListsChildrenAndDescendants(t *testing.T) {
	wired := newWiredRuntime(t)
	parent := wired.parent(t, "parent")
	wired.start(t, parent, "child")

	children, err := wired.runtime.ListChildren(t.Context(), "parent")
	if err != nil {
		t.Fatalf("列举孩子失败：%v", err)
	}
	if len(children) != 1 || children[0].ID != "child" || children[0].Mode != ModeContinuable {
		t.Fatalf("该列出那一个可续孩子，实际 %#v", children)
	}

	descendants, err := wired.runtime.ListDescendants(t.Context(), "parent")
	if err != nil {
		t.Fatalf("列举后代失败：%v", err)
	}
	if len(descendants) != 1 || descendants[0].ID != "child" || descendants[0].Depth != 1 {
		t.Fatalf("该列出那一个直接后代，实际 %#v", descendants)
	}
}
