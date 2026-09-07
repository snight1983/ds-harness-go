// 本文件的作用：这一包测试共用的替身——五个协作方各一个可编排的假实现、一个
// 只记账的假 agent，以及一份把每一步都按次序记下来的流水。

package webhook

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/feature/interaction/permissionpresets"
	"github.com/snight1983/ds-harness-go/feature/preset/agentpresets"
	"github.com/snight1983/ds-harness-go/feature/workspace"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// journal 按次序记下这一趟都做了什么，用来断言那条事务的顺序。
type journal struct {
	mutex sync.Mutex
	steps []string
}

func (j *journal) add(step string) {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	j.steps = append(j.steps, step)
}

func (j *journal) snapshot() []string {
	j.mutex.Lock()
	defer j.mutex.Unlock()
	return append([]string(nil), j.steps...)
}

// fakeWorkspace 是一个只认得身份、挂上、摘掉三件事的工作区。
//
// 内嵌那个接口是为了不把 workspace.Workspace 上另外八个方法各写一遍空实现——
// 这一包一个都走不到，真走到了就是 nil 解引用当场炸，比一个静悄悄的零值好。
type fakeWorkspace struct {
	workspace.Workspace

	id       workspace.WorkspaceID
	journal  *journal
	onAttach func()

	mutex        sync.Mutex
	attachErr    error
	detachErr    error
	detachCtxErr error
}

func (w *fakeWorkspace) ID() workspace.WorkspaceID { return w.id }

func (w *fakeWorkspace) AttachSession(context.Context, sessionlog.SessionID) error {
	w.journal.add("attach")
	if w.onAttach != nil {
		w.onAttach()
	}
	return w.attachErr
}

func (w *fakeWorkspace) DetachSession(ctx context.Context, _ sessionlog.SessionID) error {
	w.journal.add("detach")
	w.mutex.Lock()
	w.detachCtxErr = ctx.Err()
	w.mutex.Unlock()
	return w.detachErr
}

func (w *fakeWorkspace) detachContextError() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.detachCtxErr
}

// fakeWorkspaces 是那张工作区登记表，只答 Create 一句。
type fakeWorkspaces struct {
	journal   *journal
	workspace *fakeWorkspace
	err       error

	path  string
	title string
}

func (r *fakeWorkspaces) Create(_ context.Context, path, title string) (workspace.Workspace, error) {
	r.journal.add("workspace")
	r.path, r.title = path, title
	if r.err != nil {
		return nil, r.err
	}
	return r.workspace, nil
}

// fakeAgent 是这一包用得上的那几样：身份、作用域、会话，外加把放行进来的提示词记下来。
type fakeAgent struct {
	id      sessionlog.SessionID
	scope   *scope.Scope
	journal *journal

	mutex     sync.Mutex
	followups []llm.Message
}

var _ agent.Agent = (*fakeAgent)(nil)

func (a *fakeAgent) ID() sessionlog.SessionID { return a.id }
func (a *fakeAgent) Scope() *scope.Scope      { return a.scope }

func (a *fakeAgent) Followup(message llm.Message) {
	a.journal.add("followup")
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.followups = append(a.followups, message)
}

func (a *fakeAgent) admitted() []llm.Message {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]llm.Message(nil), a.followups...)
}

func (a *fakeAgent) Options() agent.Options         { return agent.Options{} }
func (a *fakeAgent) Session() *session.Session      { return nil }
func (a *fakeAgent) Inbox() *agent.Inbox            { return nil }
func (a *fakeAgent) Status() agent.Status           { return agent.StatusIdle }
func (a *fakeAgent) WhenIdle(context.Context) error { return nil }

func (a *fakeAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *fakeAgent) RunMaintenance(context.Context, func(context.Context) error) error {
	return nil
}
func (a *fakeAgent) Send(llm.Message, agent.InboxTarget, bool) {}
func (a *fakeAgent) Steer(llm.Message)                         {}
func (a *fakeAgent) Inject(llm.Message)                        {}
func (a *fakeAgent) Prepend(llm.Message, agent.InboxTarget)    {}
func (a *fakeAgent) Remove(llm.MessageID)                      {}
func (a *fakeAgent) Replace(llm.MessageID, llm.Message)        {}

// fakeAgents 是那张 agent 注册表。
type fakeAgents struct {
	journal *journal

	created    *fakeAgent
	createErr  error
	disposeErr error

	mutex         sync.Mutex
	options       agent.CreateOptions
	observers     []agent.RequestObserver
	disposed      bool
	disposeCtxErr error
}

func (r *fakeAgents) Create(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	r.journal.add("agent")
	r.mutex.Lock()
	r.options = options
	r.mutex.Unlock()
	if r.createErr != nil {
		return agent.Handle{}, r.createErr
	}
	if options.Setup != nil {
		commit, err := options.Setup(ctx, r.created.scope)
		if err != nil {
			return agent.Handle{}, err
		}
		if commit != nil {
			if err := commit(); err != nil {
				return agent.Handle{}, err
			}
		}
	}
	return agent.Handle{
		Agent: r.created,
		Dispose: func(ctx context.Context) error {
			r.journal.add("dispose")
			r.mutex.Lock()
			r.disposed = true
			r.disposeCtxErr = ctx.Err()
			r.mutex.Unlock()
			return r.disposeErr
		},
	}, nil
}

func (r *fakeAgents) OnRequest(
	_ context.Context,
	_ *scope.Scope,
	observer agent.RequestObserver,
) (func(context.Context) error, error) {
	r.journal.add("onrequest")
	r.mutex.Lock()
	r.observers = append(r.observers, observer)
	r.mutex.Unlock()
	return func(context.Context) error { return nil }, nil
}

func (r *fakeAgents) createOptions() agent.CreateOptions {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.options
}

func (r *fakeAgents) wasDisposed() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.disposed
}

func (r *fakeAgents) disposalContextError() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.disposeCtxErr
}

// fakePresets 是那份 agent 预设表。
type fakePresets struct {
	journal *journal

	preset      agentpresets.Preset
	resolveErr  error
	standingErr error
	mountErr    error
}

func (p *fakePresets) Resolve(context.Context, string) (agentpresets.Preset, error) {
	p.journal.add("preset.resolve")
	return p.preset, p.resolveErr
}

func (p *fakePresets) StandingKeyFor(context.Context, string) (*scope.Key, error) {
	p.journal.add("preset.standing")
	if p.standingErr != nil {
		return nil, p.standingErr
	}
	return scope.NewKey("standing"), nil
}

func (p *fakePresets) Mount(context.Context, *scope.Key, string) (agentpresets.Preset, error) {
	p.journal.add("preset.mount")
	return p.preset, p.mountErr
}

// fakeDefaultModel 是那份部署级默认选择。
type fakeDefaultModel struct{ selection agent.ModelSelection }

func (m fakeDefaultModel) CurrentSelection() agent.ModelSelection { return m.selection }

// fakePermissions 是那张权限档位表。
type fakePermissions struct {
	journal *journal

	resolveErr error
	setErr     error

	mutex sync.Mutex
	set   string
}

func (p *fakePermissions) Resolve(string) (permissionpresets.PresetSpec, error) {
	p.journal.add("permission.resolve")
	return permissionpresets.PresetSpec{}, p.resolveErr
}

func (p *fakePermissions) SetFor(_ *scope.Key, name string) error {
	p.journal.add("permission.set")
	if p.setErr != nil {
		return p.setErr
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.set = name
	return nil
}

func (p *fakePermissions) pinned() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.set
}

// harness 把一整套替身连同它们的运行时装在一起。
type harness struct {
	runtime *Runtime
	journal *journal

	workspaces  *fakeWorkspaces
	workspace   *fakeWorkspace
	agents      *fakeAgents
	agent       *fakeAgent
	presets     *fakePresets
	permissions *fakePermissions

	renameErr error

	mutex    sync.Mutex
	renamed  string
	failures []Failure
}

// scopeRoot 造一把用完就散的根作用域。
func scopeRoot(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// newHarness 装好一套默认全都成功的替身。
func newHarness(t *testing.T) *harness {
	t.Helper()

	owner := scopeRoot(t)
	agentScope, err := scope.New(scope.NewKey("webhook-session"), scope.Options{Parent: owner.Key()})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = agentScope.Dispose(context.Background()) })

	record := &journal{}
	h := &harness{journal: record}
	h.workspace = &fakeWorkspace{id: "ws-1", journal: record}
	h.workspaces = &fakeWorkspaces{journal: record, workspace: h.workspace}
	h.agent = &fakeAgent{id: "session-1", scope: agentScope, journal: record}
	h.agents = &fakeAgents{journal: record, created: h.agent}
	h.presets = &fakePresets{journal: record, preset: agentpresets.Preset{ID: "coder"}}
	h.permissions = &fakePermissions{journal: record}

	runtime, err := New(Config{
		Workspaces:   h.workspaces,
		Agents:       h.agents,
		AgentPresets: h.presets,
		DefaultModel: fakeDefaultModel{selection: agent.ModelSelection{
			Provider: "deepseek", Model: "chat", ReasoningEffort: "high",
		}},
		Permissions: h.permissions,
		Rename: func(_ agent.Agent, title string) error {
			record.add("rename")
			if h.renameErr != nil {
				return h.renameErr
			}
			h.mutex.Lock()
			defer h.mutex.Unlock()
			h.renamed = title
			return nil
		},
		Owner: owner,
		Report: func(failure Failure) {
			h.mutex.Lock()
			defer h.mutex.Unlock()
			h.failures = append(h.failures, failure)
		},
		NewSessionID: func() sessionlog.SessionID { return "session-1" },
	})
	if err != nil {
		t.Fatalf("造运行时失败：%v", err)
	}
	h.runtime = runtime
	t.Cleanup(runtime.Close)
	return h
}

func (h *harness) reported() []Failure {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]Failure(nil), h.failures...)
}

func (h *harness) title() string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.renamed
}

// delivery 造一次身份齐全的投递。
func delivery(kind string) VerifiedDelivery {
	return VerifiedDelivery{
		Kind:       kind,
		Source:     "primary-github",
		DeliveryID: "d-1",
		Event:      json.RawMessage(`{"action":"opened"}`),
		ReceivedAt: time.Unix(1700000000, 0),
	}
}

// request 造一份必填项齐全的会话请求。
func request() *SessionRequest {
	return &SessionRequest{
		WorkspacePath:    "/repos/demo",
		Title:            "修一个 issue",
		Prompt:           "读一遍 issue 再动手",
		AgentPreset:      "coder",
		PermissionPreset: "guarded",
	}
}

// registerRule 登记一条规则，撤销挂到用例收尾上。
func registerRule(t *testing.T, runtime *Runtime, rule Rule) func() {
	t.Helper()
	dispose, err := runtime.Register(t.Context(), rule)
	if err != nil {
		t.Fatalf("登记规则失败：%v", err)
	}
	t.Cleanup(dispose)
	return dispose
}

// settle 派一次投递，等到 until 说这一趟的效果全落下了。
//
// 本包是发后不管的，用例得自己找一个收敛点。**不能拿排干这条登记当那个收敛点**：
// 排干会取消在跑的那次调用，而规则回调返回之后还有一次取消检查——于是「规则跑完了」
// 和「这一趟收完了」之间有一道缝，排干正好挤进那道缝里，把用例要验的事截掉，
// 上报成一条「被停掉的」。所以这里盯的是效果本身：流水又长了几步、或者上报了几条。
// 收尾交给 newHarness 挂上的那次 Close。
func settle(t *testing.T, h *harness, delivery VerifiedDelivery, until func() bool) {
	t.Helper()
	if err := h.runtime.Dispatch(delivery); err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !until() {
		if time.Now().After(deadline) {
			t.Fatalf("这一趟没在五秒内收完，走到的是 %v，上报了 %v", h.journal.snapshot(), h.reported())
		}
		time.Sleep(time.Millisecond)
	}
}

// afterSteps 说这趟流水至少走到了 n 步。
func (h *harness) afterSteps(n int) func() bool {
	return func() bool { return len(h.journal.snapshot()) >= n }
}

// afterFailures 说至少上报了 n 条失败。上报是一次调用的最后一步，所以它同时意味着
// 这一趟的流水已经定稿了。
func (h *harness) afterFailures(n int) func() bool {
	return func() bool { return len(h.reported()) >= n }
}
