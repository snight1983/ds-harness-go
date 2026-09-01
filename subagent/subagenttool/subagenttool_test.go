// 本文件的作用：把这件派发工具钉在它那几条真会出错的边上——装配面缺什么就不许开工、
// 提供方来去时那件工具跟不跟得上、三条执行路各自的落点，以及一次非正常终止怎么在
// 报错的同时还把那半截答案送到父手上。
//
// # 这些测试防的是什么错
//
//   - **残缺输出被当成跑完了**。一个非 [subagent.StopCompleted] 的终态必须走 error，
//     否则父 agent 会拿着半截答案继续往下走。
//   - **处置失败盖掉结果失败**。两件各自要人去看的故障，只报后一个等于把前一个藏了。
//   - **一次被取消的开工被说成干干净净的 killed**。提供方会把开工失败和回滚失败聚在
//     一起；那种情况下报 killed 会把「清理没做完」这件事藏掉。
//   - **措辞对不上提供方**。对着一个 fork 说「它看不到这段对话」是假话，模型会白白
//     把它已经知道的东西重述一遍——那正是 fork 这条路要省掉的开销。
//   - **后台参数关掉了却还能硬走后台**。校验器放行没声明的键，光把它从 schema 里
//     拿掉挡不住一次硬写 true 的调用。
//   - **一次合法的空 output 被 omitempty 省掉**。那一支的 `output` 是必填的，
//     省掉它当场验不过。
//   - **能力对不上却等到第一次派发才报**。一个管不住 MaxDepth 的提供方是一次配置
//     错误，它必须在装的那一刻就大声失败。
//   - **半装上去**。观察者挂上了、工具没装上，或者反过来。

package subagenttool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [github.com/snight1983/ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
type stubAgent struct {
	id  session.SessionID
	own *scope.Scope
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubRun 是一次假的子 agent 运行。
type stubRun struct {
	id         session.SessionID
	result     subagent.Result
	resultErr  error
	disposeErr error
	disposals  int
	// release 不为 nil 时 Result 一直等到它关掉或者 ctx 断掉。
	release chan struct{}
}

func (r *stubRun) ID() session.SessionID   { return r.id }
func (r *stubRun) LocalAgent() agent.Agent { return nil }

func (r *stubRun) Result(ctx context.Context) (subagent.Result, error) {
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return subagent.Result{}, ctx.Err()
		}
	}
	return r.result, r.resultErr
}

func (r *stubRun) Dispose(context.Context) error {
	r.disposals++
	return r.disposeErr
}

// stubProvider 是一个假提供方。continuable 为真时它多实现一个
// [subagent.ContinuablePreparer]，靠 [continuableProvider] 那个包装类型。
type stubProvider struct {
	name     string
	caps     subagent.Capabilities
	inherits bool
}

func (p *stubProvider) Name() string                        { return p.name }
func (p *stubProvider) Capabilities() subagent.Capabilities { return p.caps }
func (p *stubProvider) InheritsParentContext() bool         { return p.inherits }

func (p *stubProvider) Start(context.Context, subagent.ResolvedStartRequest) (subagent.Run, error) {
	return nil, errors.New("这个假提供方不直接开工")
}

// continuableProvider 是一个**带**可续创建能力的假提供方。
type continuableProvider struct{ stubProvider }

func (p *continuableProvider) PrepareContinuable(
	context.Context,
	subagent.ContinuableCreateRequest,
) (subagent.ContinuableCreateSpec, error) {
	return subagent.ContinuableCreateSpec{}, nil
}

// startCall 是那条接缝收到的一次一次性开工。
type startCall struct {
	provider string
	request  subagent.StartRequest
}

// stubSubagents 是那条记账的假子 agent 接缝。
type stubSubagents struct {
	provider subagent.Provider

	run          subagent.Run
	startErr     error
	continuable  subagent.ContinuableStart
	continuedErr error

	addedErr   error
	removedErr error

	onAdded   subagent.ProviderAddedObserver
	onRemoved subagent.ProviderRemovedObserver

	starts    []startCall
	continues []subagent.ContinuableStartSpec
	// startGate 不为 nil 时 Start 一直等到它关掉，用来观察后台那条路的取消。
	startGate chan struct{}
}

func (s *stubSubagents) GetProvider(name string) (subagent.Provider, bool) {
	if s.provider == nil || s.provider.Name() != name {
		return nil, false
	}
	return s.provider, true
}

func (s *stubSubagents) Start(
	ctx context.Context,
	name string,
	request subagent.StartRequest,
) (subagent.Run, error) {
	s.starts = append(s.starts, startCall{provider: name, request: request})
	if s.startGate != nil {
		select {
		case <-s.startGate:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if s.startErr != nil {
		return nil, s.startErr
	}
	return s.run, nil
}

func (s *stubSubagents) StartContinuable(
	_ context.Context,
	spec subagent.ContinuableStartSpec,
) (subagent.ContinuableStart, error) {
	s.continues = append(s.continues, spec)
	if s.continuedErr != nil {
		return subagent.ContinuableStart{}, s.continuedErr
	}
	return s.continuable, nil
}

func (s *stubSubagents) OnProviderAdded(
	_ context.Context,
	_ *scope.Scope,
	observer subagent.ProviderAddedObserver,
) (func(context.Context) error, error) {
	if s.addedErr != nil {
		return nil, s.addedErr
	}
	s.onAdded = observer
	return func(context.Context) error { s.onAdded = nil; return nil }, nil
}

func (s *stubSubagents) OnProviderRemoved(
	_ context.Context,
	_ *scope.Scope,
	observer subagent.ProviderRemovedObserver,
) (func(context.Context) error, error) {
	if s.removedErr != nil {
		return nil, s.removedErr
	}
	s.onRemoved = observer
	return func(context.Context) error { s.onRemoved = nil; return nil }, nil
}

// stubJobs 是那台假作业注册表：它当场把生产方那份钩子取出来攥着。
type stubJobs struct {
	id      jobs.JobID
	err     error
	spec    jobs.Start
	hooks   jobs.Hooks
	hookErr error
}

func (j *stubJobs) Start(spec jobs.Start) (jobs.JobID, error) {
	j.spec = spec
	if j.err != nil {
		return "", j.err
	}
	hooks, err := spec.Run()
	if err != nil {
		j.hookErr = err
		return "", err
	}
	j.hooks = hooks
	return j.id, nil
}

// ---- 台面 ----

type world struct {
	t          *testing.T
	root       *scope.Scope
	agentScope *scope.Scope
	caller     *stubAgent
	subagents  *stubSubagents
	jobs       *stubJobs
	tools      *tools.Runtime
	prompts    *systemprompt.Registry
}

// scopeOf 造一个有身份的作用域，用完自动释放。
func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	owner, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %s 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := scopeOf(t, "root", nil)
	agentScope := scopeOf(t, "agent", root)
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompts, err := systemprompt.NewRegistry(t.Context(), root, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	return &world{
		t:          t,
		root:       root,
		agentScope: agentScope,
		caller:     &stubAgent{id: "caller", own: agentScope},
		subagents: &stubSubagents{
			provider:    &stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
			run:         &stubRun{id: "child"},
			continuable: subagent.ContinuableStart{ChildID: "durable-child"},
		},
		jobs:    &stubJobs{id: "subagent-1"},
		tools:   runtime,
		prompts: prompts,
	}
}

func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key != w.agentScope.Key() {
		return nil, errors.New("这把钥匙不属于任何一个 agent")
	}
	return w.caller, nil
}

func (w *world) config() Config {
	return Config{Provider: "spawn", AgentOf: w.agentOf, ProviderManagedDepth: true}
}

func (w *world) deps() Deps {
	return Deps{Tools: w.tools, Subagents: w.subagents, Prompts: w.prompts, Jobs: w.jobs}
}

func (w *world) controller(shape func(*Config)) *Controller {
	w.t.Helper()
	config := w.config()
	if shape != nil {
		shape(&config)
	}
	controller, err := New(config)
	if err != nil {
		w.t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

func (w *world) install(shape func(*Config)) *Controller {
	w.t.Helper()
	controller := w.controller(shape)
	undo, err := controller.Install(w.t.Context(), w.root, w.deps())
	if err != nil {
		w.t.Fatalf("装控制器失败：%v", err)
	}
	w.t.Cleanup(func() { _ = undo(context.Background()) })
	return controller
}

func (w *world) execOn() *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: w.agentScope.Key()},
	}}
}

func textOf(content llm.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "")
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("排 JSON 失败：%v", err)
	}
	return encoded
}

// ---- 装配面 ----

func TestNewRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()

	depth := -1
	zero := 0
	cases := []struct {
		name  string
		shape func(*Config)
	}{
		{"没点名提供方", func(c *Config) { c.Provider = "" }},
		{"没有从钥匙查回 agent 的路", func(c *Config) { c.AgentOf = nil }},
		{"认不得的后台走法", func(c *Config) { c.BackgroundMode = "later" }},
		{"上限归谁管有两种说法", func(c *Config) { c.MaxDepth = &zero }},
		{"负的深度上限", func(c *Config) { c.ProviderManagedDepth = false; c.MaxDepth = &depth }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			config := Config{Provider: "spawn", ProviderManagedDepth: true, AgentOf: func(*scope.Key) (agent.Agent, error) {
				return nil, nil
			}}
			item.shape(&config)
			if _, err := New(config); err == nil {
				t.Fatal("这份配置装上了")
			}
		})
	}
}

func TestNewFillsInTheDefaults(t *testing.T) {
	t.Parallel()

	controller, err := New(Config{Provider: "spawn", AgentOf: func(*scope.Key) (agent.Agent, error) {
		return nil, nil
	}})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	if controller.toolName != DefaultToolName {
		t.Fatalf("工具名默认成了 %q", controller.toolName)
	}
	if controller.continuable {
		t.Fatal("后台走法默认成了可续")
	}
	if !controller.backgroundEnabled {
		t.Fatal("后台那条路默认关着")
	}
	if controller.maxDepth == nil || *controller.maxDepth != DefaultMaxDepth {
		t.Fatalf("深度上限默认成了 %v", controller.maxDepth)
	}
	if controller.logger == nil {
		t.Fatal("日志没补上默认值")
	}
}

// TestProviderManagedDepthSendsNoCap 钉住那条边：一个字的上限都不发，
// 递归预算归提供方。
func TestProviderManagedDepthSendsNoCap(t *testing.T) {
	t.Parallel()

	controller, err := New(Config{
		Provider:             "spawn",
		ProviderManagedDepth: true,
		AgentOf:              func(*scope.Key) (agent.Agent, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	if controller.maxDepth != nil {
		t.Fatalf("交给提供方管的时候还发了上限 %d", *controller.maxDepth)
	}
}

func TestRegisterInvariantsReservesThePackageName(t *testing.T) {
	t.Parallel()

	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表也登记上了")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	remove, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记不变量失败：%v", err)
	}
	if names := registry.Registered(); len(names) != 1 || names[0] != PackageName {
		t.Fatalf("这个包名没占住：%v", names)
	}
	remove()
	if names := registry.Registered(); len(names) != 0 {
		t.Fatalf("注销之后位置该空出来，还剩：%v", names)
	}
}

// ---- 措辞 ----

// TestWordingFollowsTheProvider 钉住那条最容易说假话的边。
func TestWordingFollowsTheProvider(t *testing.T) {
	t.Parallel()

	fresh := providerWording(false)
	if !strings.Contains(fresh.description, "it does not see this conversation") {
		t.Fatalf("一个全新的孩子没被说成看不到这段对话：%s", fresh.description)
	}
	forked := providerWording(true)
	if strings.Contains(forked.description, "does not see this conversation") {
		t.Fatalf("对着一个 fork 说了假话：%s", forked.description)
	}
	if !strings.Contains(forked.promptDescription, "already sees") {
		t.Fatalf("fork 那句参数说明没提它已经看得见：%s", forked.promptDescription)
	}
}

func TestToolDescriptionPicksOneTail(t *testing.T) {
	t.Parallel()

	if got := toolDescription("base", false, true); got != "base"+foregroundOnlySuffix {
		t.Fatalf("后台关着的时候接的是 %q", got)
	}
	if got := toolDescription("base", true, true); got != "base"+continuableSuffix {
		t.Fatalf("可续那条路接的是 %q", got)
	}
	if got := toolDescription("base", true, false); got != "base"+oneShotSuffix {
		t.Fatalf("一次性那条路接的是 %q", got)
	}
}

func TestBackgroundDescriptionFollowsTheMode(t *testing.T) {
	t.Parallel()

	if backgroundDescription(true) != continuableBackgroundDescription {
		t.Fatal("可续那条路的参数说明不对")
	}
	if backgroundDescription(false) != oneShotBackgroundDescription {
		t.Fatal("一次性那条路的参数说明不对")
	}
}

func TestSectionTextNamesTheConfiguredTool(t *testing.T) {
	t.Parallel()

	if !strings.Contains(sectionText("delegate"), "Use delegate in the background by default") {
		t.Fatalf("那段指引没提这次装配的工具名：%s", sectionText("delegate"))
	}
}

// ---- 结清 ----

func TestStopReasonErrorTreatsEveryAbnormalEndAsAFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		reason subagent.StopReason
		want   string
	}{
		{subagent.StopCompleted, ""},
		{subagent.StopAborted, errAborted},
		{subagent.StopError, errFailed},
		{subagent.StopMaxTokens, errMaxTokens},
		{subagent.StopRefusal, errRefusal},
		{subagent.StopReason("quota-exhausted"), "subagent run ended abnormally (quota-exhausted)"},
	}
	for _, item := range cases {
		if got := stopReasonError(subagent.Result{StopReason: item.reason}); got != item.want {
			t.Fatalf("%q 排成了 %q，要的是 %q", item.reason, got, item.want)
		}
	}
}

// TestPartialOutputTravelsWithTheError 钉住那条边：残缺输出不是成功，但那半截答案
// 照样跟着那次报错送到父手上——它是孩子唯一留下来的东西。
func TestPartialOutputTravelsWithTheError(t *testing.T) {
	t.Parallel()

	message := withDiagnosticAndPartialText(errMaxTokens, subagent.Result{
		Diagnostic: "5 万个 token",
		Output: llm.Content{
			llm.TextBlock{Text: "答案是 "},
			llm.ImageBlock{},
			llm.TextBlock{Text: "42"},
		},
	})
	for _, want := range []string{errMaxTokens, "Diagnostic: 5 万个 token", "答案是 42"} {
		if !strings.Contains(message, want) {
			t.Fatalf("这段话里没有 %q：%s", want, message)
		}
	}
	bare := withDiagnosticAndPartialText(errRefusal, subagent.Result{})
	if bare != errRefusal {
		t.Fatalf("既没细节也没残缺输出的时候排成了 %q", bare)
	}
}

func TestSettleForegroundRunAlwaysDisposes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  *stubRun
		want []string
	}{
		{
			"跑完了",
			&stubRun{result: subagent.Result{
				StopReason: subagent.StopCompleted,
				Output:     llm.Content{llm.TextBlock{Text: "好了"}},
			}},
			nil,
		},
		{
			"非正常终止折成 error",
			&stubRun{result: subagent.Result{StopReason: subagent.StopRefusal}},
			[]string{errRefusal},
		},
		{
			"等结果本身就砸了",
			&stubRun{resultErr: errors.New("传输塌了")},
			[]string{"传输塌了"},
		},
		{
			"只有处置砸了",
			&stubRun{
				result:     subagent.Result{StopReason: subagent.StopCompleted},
				disposeErr: errors.New("端口没关"),
			},
			[]string{"端口没关"},
		},
		{
			"两样都砸了，两条原因都留住",
			&stubRun{
				resultErr:  errors.New("传输塌了"),
				disposeErr: errors.New("端口没关"),
			},
			[]string{"传输塌了", "端口没关"},
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			output, err := settleForegroundRun(t.Context(), item.run)
			if item.want == nil {
				if err != nil {
					t.Fatalf("跑完了却报错：%v", err)
				}
				if textOf(output) != "好了" {
					t.Fatalf("交出来的是 %q", textOf(output))
				}
			} else {
				if err == nil {
					t.Fatal("这次结清没报错")
				}
				for _, want := range item.want {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("这条错里没有 %q：%v", want, err)
					}
				}
			}
			if item.run.disposals != 1 {
				t.Fatalf("这次运行被处置了 %d 回", item.run.disposals)
			}
		})
	}
}

// TestACancelledForegroundWaitStillReleasesTheChild 钉住那条最要紧的边：调用方的
// 取消只掐等待，不许把「把孩子收干净」一起掐掉。
func TestACancelledForegroundWaitStillReleasesTheChild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	run := &stubRun{release: make(chan struct{})}
	cancel()

	if _, err := settleForegroundRun(ctx, run); err == nil {
		t.Fatal("取消之后这次结清还成了")
	}
	if run.disposals != 1 {
		t.Fatalf("取消之后没收拾孩子：处置了 %d 回", run.disposals)
	}
}

func TestSettleStartSeparatesCancellationFromFailure(t *testing.T) {
	t.Parallel()

	t.Run("开工成了就走普通结清", func(t *testing.T) {
		t.Parallel()
		run := &stubRun{result: subagent.Result{
			StopReason: subagent.StopCompleted,
			Output:     llm.Content{llm.TextBlock{Text: "好了"}},
		}}
		got := settleStart(t.Context(), func(context.Context) (subagent.Run, error) { return run, nil })
		if got.Status != jobs.StatusCompleted || got.Output != "好了" {
			t.Fatalf("结清出来的是 %+v", got)
		}
	})

	t.Run("没被取消的开工失败算 failed", func(t *testing.T) {
		t.Parallel()
		got := settleStart(t.Context(), func(context.Context) (subagent.Run, error) {
			return nil, errors.New("装不起来")
		})
		if got.Status != jobs.StatusFailed || !strings.Contains(got.Detail, "装不起来") {
			t.Fatalf("结清出来的是 %+v", got)
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	t.Run("干干净净的取消算 killed", func(t *testing.T) {
		t.Parallel()
		got := settleStart(ctx, func(context.Context) (subagent.Run, error) {
			return nil, context.Canceled
		})
		if got.Status != jobs.StatusKilled || got.Detail != "" {
			t.Fatalf("结清出来的是 %+v", got)
		}
	})

	// 这一条是本文件最要紧的一句：提供方把开工失败和回滚失败聚在一起时，
	// 报一个干干净净的 killed 会把「清理没做完」这件事藏掉。
	t.Run("取消夹带着清理失败算 failed", func(t *testing.T) {
		t.Parallel()
		got := settleStart(ctx, func(context.Context) (subagent.Run, error) {
			return nil, errors.Join(context.Canceled, errors.New("端口没关"))
		})
		if got.Status != jobs.StatusFailed || !strings.Contains(got.Detail, "端口没关") {
			t.Fatalf("结清出来的是 %+v", got)
		}
	})

	t.Run("嵌套的取消照样只算取消", func(t *testing.T) {
		t.Parallel()
		got := settleStart(ctx, func(context.Context) (subagent.Run, error) {
			return nil, errors.Join(context.Canceled, errors.Join(context.DeadlineExceeded))
		})
		if got.Status != jobs.StatusKilled {
			t.Fatalf("结清出来的是 %+v", got)
		}
	})
}

// ---- 提供方生命周期 ----

// TestTheToolFollowsTheProvider 钉住那条边：提供方晚来、又走掉，那件工具跟着装上
// 又摘掉。
func TestTheToolFollowsTheProvider(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	provider := w.subagents.provider
	w.subagents.provider = nil
	controller := w.install(nil)

	if _, visible := w.tools.Get(DefaultToolName, w.root.Key()); visible {
		t.Fatal("提供方还没来就把工具装上了")
	}
	if w.subagents.onAdded == nil || w.subagents.onRemoved == nil {
		t.Fatal("两个观察者没挂全")
	}

	if err := w.subagents.onAdded(provider); err != nil {
		t.Fatalf("提供方到场时装工具失败：%v", err)
	}
	if _, visible := w.tools.Get(DefaultToolName, w.root.Key()); !visible {
		t.Fatal("提供方来了工具没装上")
	}

	// 同一个提供方名再来一次不许装第二件同名工具。
	if err := w.subagents.onAdded(provider); err != nil {
		t.Fatalf("重复到场报错了：%v", err)
	}
	// 别人的提供方不关这件工具的事。
	if err := w.subagents.onAdded(&stubProvider{name: "acp"}); err != nil {
		t.Fatalf("别的提供方到场报错了：%v", err)
	}

	w.subagents.onRemoved("acp")
	if !controller.mounted() {
		t.Fatal("别人走了把这件工具摘了")
	}
	w.subagents.onRemoved("spawn")
	if _, visible := w.tools.Get(DefaultToolName, w.root.Key()); visible {
		t.Fatal("提供方走了工具还在")
	}
	// 摘第二遍是空操作。
	w.subagents.onRemoved("spawn")
}

// TestMountRejectsAProviderThatCannotEnforceTheCap 钉住那条边：一个管不住 MaxDepth
// 的提供方是一次配置错误，它必须在装的那一刻大声失败，不是等到第一次派发。
func TestMountRejectsAProviderThatCannotEnforceTheCap(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.subagents.provider = &stubProvider{name: "spawn"}
	depth := 2
	controller := w.controller(func(c *Config) {
		c.ProviderManagedDepth = false
		c.MaxDepth = &depth
	})
	_, err := controller.Install(t.Context(), w.root, w.deps())
	if err == nil || !strings.Contains(err.Error(), "cannot enforce maxDepth") {
		t.Fatalf("装出来的是 %v", err)
	}
	if w.subagents.onAdded != nil || w.subagents.onRemoved != nil {
		t.Fatal("装失败了观察者还留着")
	}
}

// TestContinuableNeedsAPreparer 钉住那条边：可续那条路要提供方实现
// [subagent.ContinuablePreparer]，装不上就大声失败。
func TestContinuableNeedsAPreparer(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(func(c *Config) { c.BackgroundMode = ModeContinuable })
	_, err := controller.Install(t.Context(), w.root, w.deps())
	if err == nil || !strings.Contains(err.Error(), "does not support backgroundMode: continuable") {
		t.Fatalf("装出来的是 %v", err)
	}

	fresh := newWorld(t)
	fresh.subagents.provider = &continuableProvider{
		stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
	}
	fresh.install(func(c *Config) { c.BackgroundMode = ModeContinuable })
	if _, visible := fresh.tools.Get(DefaultToolName, fresh.root.Key()); !visible {
		t.Fatal("带可续能力的提供方没把工具装上")
	}
}

func TestInstallRefusesAnIncompleteWiring(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	cases := []struct {
		name  string
		shape func(*Deps) *scope.Scope
	}{
		{"没有工具运行时", func(d *Deps) *scope.Scope { d.Tools = nil; return w.root }},
		{"没有子 agent 接缝", func(d *Deps) *scope.Scope { d.Subagents = nil; return w.root }},
		{"没有提示词注册表", func(d *Deps) *scope.Scope { d.Prompts = nil; return w.root }},
		{"没有作用域", func(d *Deps) *scope.Scope { return nil }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			deps := w.deps()
			owner := item.shape(&deps)
			if _, err := w.controller(nil).Install(t.Context(), owner, deps); err == nil {
				t.Fatal("这份接线装上了")
			}
		})
	}
}

func TestInstallingTwiceIsRefused(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(nil)
	if _, err := controller.Install(t.Context(), w.root, w.deps()); err == nil {
		t.Fatal("同一个控制器装了两遍")
	}
}

// TestInstallUnwindsWhenAStepFails 钉住那条边：半装上去意味着模型手上有一件派得了
// 活、却跟不上提供方来去的工具。
func TestInstallUnwindsWhenAStepFails(t *testing.T) {
	t.Parallel()

	t.Run("到场观察者挂不上", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.subagents.addedErr = errors.New("挂不上")
		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("挂不上还装成了")
		}
	})

	t.Run("离场观察者挂不上", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.subagents.removedErr = errors.New("挂不上")
		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("挂不上还装成了")
		}
		if w.subagents.onAdded != nil {
			t.Fatal("装失败了到场观察者还留着")
		}
	})

	t.Run("工具名被占了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		closed := false
		release, err := w.tools.Register(t.Context(), w.root, &tools.Definition{
			Name:        DefaultToolName,
			Description: "someone else got here first",
			Parameters:  tools.Node{Type: tools.TypeObject, AdditionalProperties: &closed},
			Output: tools.OutputDefinition{
				Schema: tools.Node{Type: tools.TypeObject},
				Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
					return llm.Content{llm.TextBlock{Text: "occupied"}}, nil
				},
			},
			Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
				return json.RawMessage(`{}`), nil
			},
		})
		if err != nil {
			t.Fatalf("占工具名失败：%v", err)
		}
		t.Cleanup(func() { _ = release(context.Background()) })
		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("工具名被占了还装成了")
		}
		if w.subagents.onAdded != nil || w.subagents.onRemoved != nil {
			t.Fatal("装失败了观察者还留着")
		}
	})

	t.Run("段落名被占了", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.subagents.provider = &continuableProvider{
			stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
		}
		release, err := w.prompts.Section(t.Context(), w.root, systemprompt.PromptSection{
			Name: "tool:" + DefaultToolName,
			Text: systemprompt.StaticText("someone else got here first"),
		})
		if err != nil {
			t.Fatalf("占段落名失败：%v", err)
		}
		t.Cleanup(func() { _ = release(context.Background()) })
		if _, err := w.controller(nil).Install(t.Context(), w.root, w.deps()); err != nil {
			t.Fatalf("一次性那条路根本不该登记这一段：%v", err)
		}

		fresh := newWorld(t)
		fresh.subagents.provider = w.subagents.provider
		release2, err := fresh.prompts.Section(t.Context(), fresh.root, systemprompt.PromptSection{
			Name: "tool:" + DefaultToolName,
			Text: systemprompt.StaticText("someone else got here first"),
		})
		if err != nil {
			t.Fatalf("占段落名失败：%v", err)
		}
		t.Cleanup(func() { _ = release2(context.Background()) })
		controller := fresh.controller(func(c *Config) { c.BackgroundMode = ModeContinuable })
		if _, err := controller.Install(t.Context(), fresh.root, fresh.deps()); err == nil {
			t.Fatal("段落名被占了还装成了")
		}
		if _, visible := fresh.tools.Get(DefaultToolName, fresh.root.Key()); visible {
			t.Fatal("装失败了那件工具还留着")
		}
	})
}

// ---- 那段后台指引 ----

// TestTheSectionDisappearsWithTheTool 钉住那条边：那一段跟着提供方在与不在自己开关，
// 不用另挂一条生命周期。
func TestTheSectionDisappearsWithTheTool(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	provider := &continuableProvider{
		stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
	}
	w.subagents.provider = nil
	controller := w.install(func(c *Config) { c.BackgroundMode = ModeContinuable })

	promptOf := func() string {
		t.Helper()
		assembly, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.root.Key()})
		if err != nil {
			t.Fatalf("装配提示词失败：%v", err)
		}
		prompt, err := systemprompt.RenderPrompt(assembly)
		if err != nil {
			t.Fatalf("渲染提示词失败：%v", err)
		}
		return prompt
	}

	if strings.Contains(promptOf(), "in the background by default") {
		t.Fatal("工具还没装上那段指引就出现了")
	}
	if err := w.subagents.onAdded(provider); err != nil {
		t.Fatalf("提供方到场时装工具失败：%v", err)
	}
	if !strings.Contains(promptOf(), "in the background by default") {
		t.Fatal("工具装上了那段指引没出现")
	}

	// 一个看不见这件工具的作用域上，这一段照样是空的。
	stranger := scopeOf(t, "stranger", nil)
	text, err := controller.sectionTextFor(t.Context(), systemprompt.AssembleContext{Scope: stranger.Key()})
	if err != nil {
		t.Fatalf("求那段指引失败：%v", err)
	}
	if text != "" {
		t.Fatalf("一个看不见这件工具的作用域上还给了正文：%q", text)
	}

	w.subagents.onRemoved("spawn")
	if strings.Contains(promptOf(), "in the background by default") {
		t.Fatal("工具摘了那段指引还在")
	}
}

// TestTheSectionIsOnlyForTheContinuableBackgroundPath 钉住那条边：那段指引说的是
// 「默认走后台」，一次性和关掉后台两条路上说这句话就是错的。
func TestTheSectionIsOnlyForTheContinuableBackgroundPath(t *testing.T) {
	t.Parallel()

	for _, item := range []struct {
		name  string
		shape func(*Config)
	}{
		{"一次性那条路", nil},
		{"后台整个关着", func(c *Config) { c.DisableRunInBackground = true }},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.install(item.shape)
			// 段落名还空着就说明这一段没登记。
			release, err := w.prompts.Section(t.Context(), w.root, systemprompt.PromptSection{
				Name: "tool:" + DefaultToolName,
				Text: systemprompt.StaticText("still free"),
			})
			if err != nil {
				t.Fatalf("这一段被登记走了：%v", err)
			}
			_ = release(context.Background())
		})
	}
}

// ---- 三条执行路 ----

func TestDelegationNeedsACallingAgent(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(nil)
	args := mustJSON(t, delegationArgs{Description: "查一下", Prompt: "去查"})

	for _, item := range []struct {
		name string
		exec *tools.RunContext
	}{
		{"根本没有执行上下文", nil},
		{"执行没落在 agent 上", &tools.RunContext{}},
		{"这把钥匙查不回 agent", &tools.RunContext{Execution: tools.Execution{
			ExecutionInput: tools.ExecutionInput{Agent: w.root.Key()},
		}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			_, err := controller.delegate(t.Context(), args, item.exec)
			if err == nil || !strings.Contains(err.Error(), "requires a calling agent") {
				t.Fatalf("派出来的是 %v", err)
			}
		})
	}
}

func TestDelegationRejectsMalformedArguments(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(nil)
	if _, err := controller.delegate(t.Context(), json.RawMessage("{"), w.execOn()); err == nil {
		t.Fatal("解不动的参数还派出去了")
	}
}

// TestTheForegroundPathCarriesTheChildsAnswer 钉住那条边：前台调用把孩子那份内容
// 原样带回来，而且**一定**处置了这次运行。
func TestTheForegroundPathCarriesTheChildsAnswer(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	run := &stubRun{id: "child-7", result: subagent.Result{
		StopReason: subagent.StopCompleted,
		Output:     llm.Content{llm.TextBlock{Text: "查完了"}},
	}}
	w.subagents.run = run
	controller := w.install(nil)

	value, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "查一下", Prompt: "去查",
	}), w.execOn())
	if err != nil {
		t.Fatalf("前台派发失败：%v", err)
	}
	var decoded foregroundResult
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("解权威值失败：%v", err)
	}
	if decoded.Kind != kindForeground || decoded.RunID != "child-7" {
		t.Fatalf("交出来的是 %+v", decoded)
	}
	if run.disposals != 1 {
		t.Fatalf("这次运行被处置了 %d 回", run.disposals)
	}
	if len(w.subagents.starts) != 1 {
		t.Fatalf("开工了 %d 回", len(w.subagents.starts))
	}
	request := w.subagents.starts[0].request
	if request.Label != "查一下" || textOf(request.Prompt) != "去查" || request.Parent != w.caller {
		t.Fatalf("派出去的请求是 %+v", request)
	}

	content, err := controller.newTool(false).Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "查完了" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
}

// TestAnEmptyForegroundOutputStaysAnArray 钉住那条边：一次合法的空 output 不许被
// 省掉——那一支的 `output` 是必填的。
func TestAnEmptyForegroundOutputStaysAnArray(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.subagents.run = &stubRun{id: "child", result: subagent.Result{StopReason: subagent.StopCompleted}}
	controller := w.install(nil)

	value, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "空的", Prompt: "什么都不做",
	}), w.execOn())
	if err != nil {
		t.Fatalf("前台派发失败：%v", err)
	}
	if !strings.Contains(string(value), `"output":[]`) {
		t.Fatalf("空 output 没排成数组：%s", value)
	}
}

func TestTheForegroundPathReportsFailures(t *testing.T) {
	t.Parallel()

	t.Run("开不了工", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.subagents.startErr = errors.New("开不了工")
		controller := w.install(nil)
		if _, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
			Description: "查一下", Prompt: "去查",
		}), w.execOn()); err == nil {
			t.Fatal("开不了工还成了")
		}
	})

	t.Run("孩子拒绝了这件事", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.subagents.run = &stubRun{result: subagent.Result{StopReason: subagent.StopRefusal}}
		controller := w.install(nil)
		_, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
			Description: "查一下", Prompt: "去查",
		}), w.execOn())
		if err == nil || !strings.Contains(err.Error(), errRefusal) {
			t.Fatalf("派出来的是 %v", err)
		}
	})
}

// TestTheOneShotBackgroundPathOwnsAJob 钉住那条边：一次性后台走一件普通的后台作业，
// 当场交回它的 id，取消口是作业自己的。
func TestTheOneShotBackgroundPathOwnsAJob(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	gate := make(chan struct{})
	w.subagents.startGate = gate
	controller := w.install(nil)

	wanted := true
	value, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "慢慢查", Prompt: "去查", RunInBackground: &wanted,
	}), w.execOn())
	if err != nil {
		t.Fatalf("后台派发失败：%v", err)
	}
	var decoded backgroundResult
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("解权威值失败：%v", err)
	}
	if decoded.Kind != kindBackground || decoded.JobID != "subagent-1" {
		t.Fatalf("交出来的是 %+v", decoded)
	}
	if w.jobs.spec.Kind != jobs.KindSubagent || w.jobs.spec.Owner != w.caller {
		t.Fatalf("那件作业是 %+v", w.jobs.spec)
	}
	if w.jobs.hooks.ReadOutput != nil {
		t.Fatal("这件作业不该有增量输出：中间过程归孩子那段会话")
	}

	if err := w.jobs.hooks.Cancel(""); err != nil {
		t.Fatalf("取消失败：%v", err)
	}
	outcome, ok := <-w.jobs.hooks.Done
	if !ok {
		t.Fatal("那条结局通道关掉了却没送值")
	}
	if outcome.Status != jobs.StatusKilled {
		t.Fatalf("取消之后结清成了 %+v", outcome)
	}
	close(gate)

	// 渲染那一支。
	content, err := controller.newTool(false).Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "started background subagent job subagent-1" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
}

func TestTheOneShotBackgroundPathNeedsAJobRegistry(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	deps := w.deps()
	deps.Jobs = nil
	undo, err := controller.Install(t.Context(), w.root, deps)
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	wanted := true
	_, err = controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "慢慢查", Prompt: "去查", RunInBackground: &wanted,
	}), w.execOn())
	if err == nil || !strings.Contains(err.Error(), "background jobs unavailable") {
		t.Fatalf("派出来的是 %v", err)
	}
}

func TestAFailedJobRegistrationSurfaces(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.jobs.err = errors.New("登记不上")
	controller := w.install(nil)
	wanted := true
	if _, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "慢慢查", Prompt: "去查", RunInBackground: &wanted,
	}), w.execOn()); err == nil {
		t.Fatal("登记不上还成了")
	}
}

// TestTheContinuablePathReturnsTheDurableChildID 钉住那条边：可续那条路孩子接下
// 提示词就返回，既不等结果也不收结果。
func TestTheContinuablePathReturnsTheDurableChildID(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.subagents.provider = &continuableProvider{
		stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
	}
	controller := w.install(func(c *Config) { c.BackgroundMode = ModeContinuable })

	value, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "接着聊", Prompt: "先看这个",
	}), w.execOn())
	if err != nil {
		t.Fatalf("可续派发失败：%v", err)
	}
	var decoded continuableResult
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("解权威值失败：%v", err)
	}
	if decoded.Kind != kindContinuable || decoded.SubagentID != "durable-child" {
		t.Fatalf("交出来的是 %+v", decoded)
	}
	if len(w.subagents.continues) != 1 || w.subagents.continues[0].Label != "接着聊" {
		t.Fatalf("派出去的是 %+v", w.subagents.continues)
	}
	if len(w.subagents.starts) != 0 {
		t.Fatal("可续那条路不该走一次性开工")
	}

	content, err := controller.newTool(false).Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "started subagent durable-child" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
}

func TestAFailedContinuableStartSurfaces(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.subagents.provider = &continuableProvider{
		stubProvider{name: "spawn", caps: subagent.Capabilities{DepthLimit: true}},
	}
	w.subagents.continuedErr = errors.New("起不来")
	controller := w.install(func(c *Config) { c.BackgroundMode = ModeContinuable })
	if _, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "接着聊", Prompt: "先看这个",
	}), w.execOn()); err == nil {
		t.Fatal("起不来还成了")
	}
}

// ---- 排期请求 ----

// TestResolveRunInBackgroundPicksOneRoute 钉住那条边：校验器放行没声明的键，所以
// 光把参数从 schema 里拿掉挡不住一次硬写 true 的调用。
func TestResolveRunInBackgroundPicksOneRoute(t *testing.T) {
	t.Parallel()

	yes, no := true, false
	cases := []struct {
		name        string
		enabled     bool
		continuable bool
		requested   *bool
		want        bool
		wantErr     bool
	}{
		{"关着而且没写", false, false, nil, false, false},
		{"关着但写了 false", false, false, &no, false, false},
		{"关着却硬写 true", false, false, &yes, false, true},
		{"一次性没写默认前台", true, false, nil, false, false},
		{"可续没写默认后台", true, true, nil, true, false},
		{"明说要前台", true, true, &no, false, false},
		{"明说要后台", true, false, &yes, true, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			controller := &Controller{backgroundEnabled: item.enabled, continuable: item.continuable}
			got, err := controller.resolveRunInBackground(item.requested)
			if item.wantErr {
				if err == nil || !strings.Contains(err.Error(), "run_in_background is disabled") {
					t.Fatalf("挡出来的是 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("解排期失败：%v", err)
			}
			if got != item.want {
				t.Fatalf("解出来的是 %v，要的是 %v", got, item.want)
			}
		})
	}
}

// ---- 工具定义 ----

// TestTheBackgroundParameterDisappearsWhenDisabled 钉住那条边：关掉后台的那份装配
// 里，模型根本看不到这个参数。
func TestTheBackgroundParameterDisappearsWhenDisabled(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	names := func(controller *Controller) []string {
		definition := controller.newTool(false)
		found := make([]string, 0, len(definition.Parameters.Properties))
		for _, property := range definition.Parameters.Properties {
			found = append(found, property.Name)
		}
		return found
	}
	on := names(w.controller(nil))
	if len(on) != 3 || on[2] != "run_in_background" {
		t.Fatalf("后台开着时的参数是 %v", on)
	}
	off := names(w.controller(func(c *Config) { c.DisableRunInBackground = true }))
	if len(off) != 2 {
		t.Fatalf("后台关着时的参数是 %v", off)
	}
}

// TestDelegationIsConcurrencySafe 钉住那句承诺：孩子从不改父那段会话。
func TestDelegationIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	definition := newWorld(t).controller(nil).newTool(false)
	if definition.IsConcurrencySafe == nil || !definition.IsConcurrencySafe(nil) {
		t.Fatal("这件工具没被声明成可并发的")
	}
}

func TestPresentCallShowsTheDescription(t *testing.T) {
	t.Parallel()

	definition := newWorld(t).controller(nil).newTool(false)
	view, ok := definition.PresentCall(mustJSON(t, delegationArgs{Description: "查一下"})).(tools.GenericCallView)
	if !ok || view.Title != "查一下" || view.Kind != tools.CallExecute {
		t.Fatalf("那张卡片是 %+v", view)
	}
	// 解不动的参数不许让呈现失败：呈现是纯函数。
	_ = definition.PresentCall(json.RawMessage("{"))
}

func TestRenderRejectsAValueItCannotRead(t *testing.T) {
	t.Parallel()

	definition := newWorld(t).controller(nil).newTool(false)
	if _, err := definition.Output.Render(nil, json.RawMessage("{")); err == nil {
		t.Fatal("解不动的值还渲染出来了")
	}
}

// TestOutputValueTextIgnoresBlocksItCannotRead 钉住那条边：那份数组是权威值的一部分，
// 但本包不信里面每一个值都认得出来。
func TestOutputValueTextIgnoresBlocksItCannotRead(t *testing.T) {
	t.Parallel()

	text := outputValueText([]json.RawMessage{
		mustJSON(t, map[string]string{"type": "text", "text": "答案是 "}),
		json.RawMessage(`{"type":"totally-unknown"}`),
		json.RawMessage(`nonsense`),
		mustJSON(t, map[string]string{"type": "text", "text": "42"}),
	})
	if text != "答案是 42" {
		t.Fatalf("摊平出来的是 %q", text)
	}
}

// TestTheOutputSchemaHasThreeClosedShapes 钉住那份契约：三支互斥，各自封闭。
func TestTheOutputSchemaHasThreeClosedShapes(t *testing.T) {
	t.Parallel()

	schema := outputSchema()
	if len(schema.OneOf) != 3 {
		t.Fatalf("那份契约有 %d 支", len(schema.OneOf))
	}
	for _, shape := range schema.OneOf {
		if shape.AdditionalProperties == nil || *shape.AdditionalProperties {
			t.Fatalf("这一支没封闭：%+v", shape)
		}
		if len(shape.Required) != len(shape.Properties) {
			t.Fatalf("这一支有可选字段：%+v", shape)
		}
	}
}

// TestDelegationRejectsAnExplicitBackgroundWhenDisabled 钉住那条边：把
// run_in_background 从 schema 里拿掉挡不住一次硬写的调用——校验器放行没声明的键，
// 所以执行期还得再挡一道，而且要在起孩子**之前**挡。
func TestDelegationRejectsAnExplicitBackgroundWhenDisabled(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(func(c *Config) { c.DisableRunInBackground = true })

	wanted := true
	if _, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "慢慢查", Prompt: "去查", RunInBackground: &wanted,
	}), w.execOn()); err == nil {
		t.Fatal("后台关着还接了一次硬写的 run_in_background")
	}
	if len(w.subagents.starts) != 0 {
		t.Fatalf("挡下来之前已经起了 %d 个孩子", len(w.subagents.starts))
	}
}

// TestAnUnserializableBlockSurfaces 钉住那条边：孩子那份内容排不出去的时候要报错，
// 不能把一份残缺的数组当成答案送上去。
//
// [github.com/snight1983/ds-harness-go/llm.UnknownBlock] 没有原始字节时自己会拒绝编组，这里借它造出
// 那一次失败——[github.com/snight1983/ds-harness-go/llm.ContentBlock] 是封闭的，本包外造不出别的坏块。
func TestAnUnserializableBlockSurfaces(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.subagents.run = &stubRun{result: subagent.Result{
		StopReason: subagent.StopCompleted,
		Output:     llm.Content{llm.UnknownBlock{Kind: "weird"}},
	}}
	controller := w.install(nil)

	if _, err := controller.delegate(t.Context(), mustJSON(t, delegationArgs{
		Description: "查一下", Prompt: "去查",
	}), w.execOn()); err == nil {
		t.Fatal("排不出去的块还交出了一份权威值")
	}
}

// TestMountRefusesATornDownController 钉住那条边：提供方晚到时那两个观察者跑在
// 别的协程上，而这次装配可能已经拆掉了。那一刻没有工具运行时也没有作用域，
// 得当场停住，不能拿零值去登记。
func TestMountRefusesATornDownController(t *testing.T) {
	t.Parallel()

	controller := &Controller{provider: "spawn", toolName: DefaultToolName}
	err := controller.mount(t.Context(), &stubProvider{name: "spawn"})
	if err == nil {
		t.Fatal("拆过的控制器还装上了")
	}
	if controller.mounted() {
		t.Fatal("装失败了还记着一个摘除函数")
	}
}

// TestProviderRemovedReportsAFailedUnmount 钉住那条边：提供方离场是个观察者，
// 没有人接它的返回值，所以摘不掉这件事只能落到日志上——不能就这么咽了。
func TestProviderRemovedReportsAFailedUnmount(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	w := newWorld(t)
	controller := w.controller(func(c *Config) {
		c.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})
	controller.disposeTool = func(context.Context) error { return errors.New("摘不掉") }

	controller.providerRemoved("someone-else")
	if !controller.mounted() {
		t.Fatal("别人家的提供方走了也把这件工具摘了")
	}

	controller.providerRemoved("spawn")
	if controller.mounted() {
		t.Fatal("摘过一次之后还记着那个摘除函数")
	}
	if !strings.Contains(logged.String(), "摘不掉") {
		t.Fatalf("摘失败没落到日志上：%q", logged.String())
	}
}

func (a *stubAgent) Remove(llm.MessageID) {}

func (a *stubAgent) Replace(llm.MessageID, llm.Message) {}
