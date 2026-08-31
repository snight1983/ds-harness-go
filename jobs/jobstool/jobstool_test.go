// 本文件的作用：把这三件工具钉在它们那几条真会出错的边上——装配面缺什么就不许开工、
// 那份公开投影摘掉了什么、四级让位每一级的落点、一条结算通知怎么找到属主、以及
// 那份按执行令牌记的字节预算走的是记账还是现查。
//
// # 这些测试防的是什么错
//
//   - **让到最后把那句动作让没了**。一条模型看不懂该去读什么的通知，等于一次它
//     永远不会知道的完成，所以 [noticeAction] 在四级让位里一级都不许丢。
//   - **属主和通知记账漏给模型**。[PublicSnapshot] 少一个字段是格式问题，多一个
//     字段是泄漏。
//   - **未落定的作业被说成已落定**。零值时刻折成毫秒是一个很大的负数，不是 0；
//     拿 0 当「没有」会让 1970 年之外的任何一件作业都长出一个 finishedAt。
//   - **唤醒预算失灵**。一个被唤醒的回合起了一件作业、那件作业又把它唤醒——这条
//     自激链只有靠预算收得住，而任何一条人写的输入必须把它补回来。
//   - **旁路记账泄漏**。按执行令牌索引的那张表如果不在收尾时删，它会一直长。
//   - **空清单排成 null**。模型看见 null 读不出「一个都没有」，那份数组契约也验不过。
//   - **半装上去**。模型手上有一件读得了作业、却收不到任何完成通知的工具。

package jobstool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/invariants"
	"ds-harness-go/jobs/jobs"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包只读它的 ID、Status 和 Scope，投递则落在 followups / injects 上。
type stubAgent struct {
	id     session.SessionID
	status agent.Status
	own    *scope.Scope

	followups []llm.Message
	injects   []llm.Message
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return a.status }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Steer(llm.Message)                                      {}

func (a *stubAgent) Followup(message llm.Message) { a.followups = append(a.followups, message) }
func (a *stubAgent) Inject(message llm.Message)   { a.injects = append(a.injects, message) }
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// killCall 是服务收到的那一次取消请求。
type killCall struct {
	id     jobs.JobID
	caller agent.Agent
	reason string
}

// waitCall 是服务收到的那一次等待。
type waitCall struct {
	id      jobs.JobID
	timeout time.Duration
	caller  agent.Agent
}

// stubService 是那台记账的假作业注册表。
type stubService struct {
	// snapshots 是 List 交出去的那几行，Get 也从这里找。
	snapshots []jobs.Snapshot
	// read 是 Read 交出去的那一段。
	read jobs.Read

	// 这几个不为 nil 时对应那一次调用失败。
	readErr    error
	getErr     error
	killErr    error
	waitErr    error
	doneErr    error
	attachErr  error
	killResult jobs.KillResult

	// listener 是那个装上去的完成监听器。
	listener jobs.DoneListener
	// attached 记下控制器挂上来时用的那个名字。
	attached string

	kills []killCall
	waits []waitCall
}

func (s *stubService) List(agent.Agent) []jobs.Snapshot { return s.snapshots }

func (s *stubService) Get(id jobs.JobID, _ agent.Agent) (jobs.Snapshot, error) {
	if s.getErr != nil {
		return jobs.Snapshot{}, s.getErr
	}
	for _, snapshot := range s.snapshots {
		if snapshot.ID == id {
			return snapshot, nil
		}
	}
	return jobs.Snapshot{}, errors.New("no such job")
}

func (s *stubService) Read(jobs.JobID, agent.Agent) (jobs.Read, error) {
	if s.readErr != nil {
		return jobs.Read{}, s.readErr
	}
	return s.read, nil
}

func (s *stubService) Kill(id jobs.JobID, caller agent.Agent, reason string) (jobs.KillResult, error) {
	s.kills = append(s.kills, killCall{id: id, caller: caller, reason: reason})
	if s.killErr != nil {
		return "", s.killErr
	}
	return s.killResult, nil
}

func (s *stubService) Wait(
	_ context.Context,
	id jobs.JobID,
	timeout time.Duration,
	caller agent.Agent,
) (jobs.Snapshot, error) {
	s.waits = append(s.waits, waitCall{id: id, timeout: timeout, caller: caller})
	if s.waitErr != nil {
		return jobs.Snapshot{}, s.waitErr
	}
	return jobs.Snapshot{ID: id}, nil
}

func (s *stubService) OnJobDone(
	_ context.Context,
	_ *scope.Scope,
	listener jobs.DoneListener,
) (func(context.Context) error, error) {
	if s.doneErr != nil {
		return nil, s.doneErr
	}
	s.listener = listener
	return func(context.Context) error { s.listener = nil; return nil }, nil
}

func (s *stubService) AttachController(
	_ context.Context,
	_ *scope.Scope,
	name string,
) (func(context.Context) error, error) {
	if s.attachErr != nil {
		return nil, s.attachErr
	}
	s.attached = name
	return func(context.Context) error { s.attached = ""; return nil }, nil
}

// stubAgents 是那条唤醒预算补给线的假件。
type stubAgents struct {
	err      error
	observer agent.InboxClaimedObserver
}

func (a *stubAgents) OnInboxClaimed(
	_ context.Context,
	_ *scope.Scope,
	observer agent.InboxClaimedObserver,
) (func(context.Context) error, error) {
	if a.err != nil {
		return nil, a.err
	}
	a.observer = observer
	return func(context.Context) error { a.observer = nil; return nil }, nil
}

// ---- 装配 ----

// world 是一次用例要的全部家当。
type world struct {
	t          *testing.T
	root       *scope.Scope
	agentScope *scope.Scope
	service    *stubService
	agents     *stubAgents
	caller     *stubAgent
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
		service:    &stubService{killResult: jobs.KillRequested},
		agents:     &stubAgents{},
		caller:     &stubAgent{id: "caller", status: agent.StatusIdle, own: agentScope},
		tools:      runtime,
		prompts:    prompts,
	}
}

// agentOf 是那条从钥匙查回 agent 的路：只认这个世界那把 agent 钥匙。
func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key != w.agentScope.Key() {
		return nil, errors.New("这把钥匙不属于任何一个 agent")
	}
	return w.caller, nil
}

func (w *world) config() Config {
	return Config{Service: w.service, AgentOf: w.agentOf}
}

func (w *world) deps() Deps {
	return Deps{Tools: w.tools, Prompts: w.prompts, Agents: w.agents}
}

// controller 造一个控制器。
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

// install 造一个控制器并把整套装上根作用域。
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

// execOn 造一份落在这个世界那把 agent 钥匙上的执行上下文。
func (w *world) execOn() *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: w.agentScope.Key()},
	}}
}

// textOf 把一份内容里那些文本块拼起来。
func textOf(content llm.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// mustJSON 把一个值排成字节，排不出去当场失败。
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

	cases := map[string]func(*Config){
		"没有作业注册表":          func(config *Config) { config.Service = nil },
		"没有从钥匙找回 agent 的路": func(config *Config) { config.AgentOf = nil },
		"认不得的投递方式":         func(config *Config) { config.CompletionDelivery = "shout" },
		"等待默认值太小":          func(config *Config) { config.WaitTimeout = time.Microsecond },
		"等待上限太小":           func(config *Config) { config.MaxWaitTimeout = time.Microsecond },
		"默认值超过上限":          func(config *Config) { config.WaitTimeout = time.Hour },
		"唤醒预算不足一个回合":       func(config *Config) { config.MaxConsecutiveWakes = -1 },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.config()
			damage(&config)
			if _, err := New(config); err == nil {
				t.Fatal("缺件或者矛盾的装配面还造得出控制器")
			}
		})
	}
}

// TestNewFillsInTheDocumentedDefaults 钉住那几个零值的落点：文档上写的默认值。
func TestNewFillsInTheDocumentedDefaults(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	if controller.waitDefault != DefaultWaitTimeout {
		t.Fatalf("等待默认值是 %v，要的是 %v", controller.waitDefault, DefaultWaitTimeout)
	}
	if controller.waitCap != DefaultMaxWaitTimeout {
		t.Fatalf("等待上限是 %v，要的是 %v", controller.waitCap, DefaultMaxWaitTimeout)
	}
	if controller.delivery != DeliveryWakeup {
		t.Fatalf("投递方式是 %q，要的是 %q", controller.delivery, DeliveryWakeup)
	}
	if controller.wakeBudget != DefaultMaxConsecutiveWakes {
		t.Fatalf("唤醒预算是 %d，要的是 %d", controller.wakeBudget, DefaultMaxConsecutiveWakes)
	}
}

func TestRegisterInvariantsNeedsARegistry(t *testing.T) {
	t.Parallel()

	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表也登记得上不变量")
	}
}

// TestTheInvariantIsDeliberatelyEmpty 钉住那条空检查：本包只是一层适配，它没有
// 自己的生命周期流可验，但它得占住那个包名。
func TestTheInvariantIsDeliberatelyEmpty(t *testing.T) {
	t.Parallel()

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

// ---- 公开投影 ----

// TestThePublicProjectionDropsOwnershipAndBookkeeping 钉住那份投影：属主和
// 「已汇报」一个字都不给模型。
func TestThePublicProjectionDropsOwnershipAndBookkeeping(t *testing.T) {
	t.Parallel()

	started := time.UnixMilli(1_700_000_000_000)
	finished := started.Add(time.Second)
	encoded := mustJSON(t, publicJob(jobs.Snapshot{
		ID:               "j1",
		Kind:             jobs.KindBash,
		Label:            "npm test",
		OwnerSession:     "owner-session",
		Status:           jobs.StatusCompleted,
		Detail:           "exit 0",
		StartedAt:        started,
		FinishedAt:       finished,
		Reported:         true,
		OutputLimitBytes: 4096,
	}))
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("解投影失败：%v", err)
	}
	for _, leaked := range []string{"ownerSession", "reported", "outputLimitBytes"} {
		if _, ok := fields[leaked]; ok {
			t.Fatalf("投影里漏了 %s：%s", leaked, encoded)
		}
	}
	if fields["startedAt"] != float64(started.UnixMilli()) {
		t.Fatalf("startedAt 不是纪元毫秒：%s", encoded)
	}
	if fields["finishedAt"] != float64(finished.UnixMilli()) {
		t.Fatalf("finishedAt 不是纪元毫秒：%s", encoded)
	}
}

// TestAnUnsettledJobHasNoFinishedAt 钉住那条零值陷阱：一件还活着的作业不许长出
// 一个 finishedAt，哪怕零值时刻折成毫秒不是 0。
func TestAnUnsettledJobHasNoFinishedAt(t *testing.T) {
	t.Parallel()

	public := publicJob(jobs.Snapshot{ID: "j1", Status: jobs.StatusRunning})
	if public.FinishedAt != nil {
		t.Fatalf("没落定的作业却有 finishedAt：%v", *public.FinishedAt)
	}
	encoded := mustJSON(t, public)
	if strings.Contains(string(encoded), "finishedAt") {
		t.Fatalf("没落定的作业排出了 finishedAt：%s", encoded)
	}
	if strings.Contains(string(encoded), "detail") {
		t.Fatalf("没有细节却排出了 detail：%s", encoded)
	}
}

// TestTheSchemaEnumeratesEveryStatusTheRegistryKnows 钉住那张白名单：它和注册表
// 认得的那套状态是同一套，抄一遍就意味着以后加一种会悄悄落下。
func TestTheSchemaEnumeratesEveryStatusTheRegistryKnows(t *testing.T) {
	t.Parallel()

	schema := publicJobSchema()
	var status tools.Node
	for _, property := range schema.Properties {
		if property.Name == "status" {
			status = property.Schema
		}
	}
	if len(status.Enum) != len(statusNames) {
		t.Fatalf("状态白名单有 %d 项，注册表认得 %d 种", len(status.Enum), len(statusNames))
	}
	for index, name := range statusNames {
		if string(status.Enum[index]) != `"`+string(name)+`"` {
			t.Fatalf("第 %d 项是 %s，要的是 %q", index, status.Enum[index], name)
		}
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("那份投影的 schema 没有关掉额外字段")
	}
}

func TestStatusLineCarriesTheDetailOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()

	if got := StatusLine(jobs.StatusRunning, ""); got != "[status: running]" {
		t.Fatalf("没有细节时那行是 %q", got)
	}
	if got := StatusLine(jobs.StatusFailed, "exit 1"); got != "[status: failed, exit 1]" {
		t.Fatalf("有细节时那行是 %q", got)
	}
}

// ---- 让位 ----

func TestFitWithSuffixKeepsTheSuffixWhatever(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		content  string
		suffix   string
		maxBytes int
		omitted  string
		want     string
	}{
		{"不设上限", "abc", "!", 0, "\n[x]", "abc!"},
		{"放得下", "abc", "!", 10, "\n[x]", "abc!"},
		{"正文让位", "abcdefgh", "!", 6, "\n[x]", "h\n[x]!"},
		{"正文自己已经带着那句提示", "abc\n[x]", "!", 6, "\n[x]", "c\n[x]!"},
		{"连提示加后缀都放不下", "abcdefgh", "!!!", 3, "\n[x]", "!!!"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			got := fitWithSuffix(item.content, item.suffix, item.maxBytes, item.omitted)
			if got != item.want {
				t.Fatalf("让出来的是 %q，要的是 %q", got, item.want)
			}
		})
	}
}

// TestTheCompletionNoticeNeverLosesTheAction 钉住那四级让位：每一级都保住
// [noticeAction]，一条模型看不懂该去读什么的通知等于一次它永远不会知道的完成。
func TestTheCompletionNoticeNeverLosesTheAction(t *testing.T) {
	t.Parallel()

	base := jobs.Snapshot{
		ID:     "j1",
		Kind:   jobs.KindBash,
		Label:  "a fairly long label so the detail has something to give up",
		Status: jobs.StatusCompleted,
		Detail: "exit 0",
	}
	prefix := "background job " + string(base.ID)
	fixed := prefix + noticeOmitted + noticeAction
	compact := prefix + noticeAction

	cases := []struct {
		name     string
		maxBytes int
		want     string
	}{
		{"细节让到刚好", len(fixed), fixed},
		{"细节让掉一部分", len(fixed) + 3, prefix + " (b" + noticeOmitted + noticeAction},
		{"细节整个让掉", len(compact), compact},
		{"连 id 也让掉", len(noticeAction) + 2, "ba" + noticeAction},
		{"只剩那句动作", len(noticeAction), noticeAction},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			snapshot := base
			snapshot.OutputLimitBytes = item.maxBytes
			got := fitCompletionNotice(snapshot)
			if got != item.want {
				t.Fatalf("让出来的是 %q，要的是 %q", got, item.want)
			}
			if len(got) > item.maxBytes {
				t.Fatalf("让完还是 %d 字节，预算只有 %d", len(got), item.maxBytes)
			}
			if !strings.HasSuffix(got, noticeAction) {
				t.Fatalf("这一级把那句动作让没了：%q", got)
			}
		})
	}
}

// TestTheCompletionNoticeIsWholeWhenItFits 钉住不让位的那条路：预算够就是整句，
// 预算是 0 表示不设上限。
func TestTheCompletionNoticeIsWholeWhenItFits(t *testing.T) {
	t.Parallel()

	snapshot := jobs.Snapshot{ID: "j1", Kind: jobs.KindBash, Label: "npm test", Status: jobs.StatusCompleted}
	want := "background job j1 (bash: npm test) finished [status: completed]. Read its output with job_output."
	if got := fitCompletionNotice(snapshot); got != want {
		t.Fatalf("不设上限时那句是 %q", got)
	}
	snapshot.OutputLimitBytes = len(want)
	if got := fitCompletionNotice(snapshot); got != want {
		t.Fatalf("刚好放得下时那句是 %q", got)
	}
}

// TestTheNoticeSummaryIsBounded 钉住那条折叠行：它走的是 llm 那份统一的上限。
func TestTheNoticeSummaryIsBounded(t *testing.T) {
	t.Parallel()

	summary := completionSummary(jobs.Snapshot{
		Kind:   jobs.KindBash,
		Label:  strings.Repeat("x", llm.ContextSummaryMaxChars*2),
		Status: jobs.StatusFailed,
		Detail: "exit 1",
	})
	if len([]rune(summary)) > llm.ContextSummaryMaxChars+1 {
		t.Fatalf("折叠行没收住：%d 个字", len([]rune(summary)))
	}
}

// TestOnlyAsingleTextBlockCanBeBounded 钉住那条形状判断：不是「恰好一块文本」的
// 内容一律不动——猜着改会把别的层放进去的东西弄丢。
func TestOnlyAsingleTextBlockCanBeBounded(t *testing.T) {
	t.Parallel()

	if boundSingleText(llm.Content{llm.TextBlock{Text: "a"}, llm.TextBlock{Text: "b"}}, 4) != nil {
		t.Fatal("两块内容也被动了")
	}
	if boundSingleText(llm.Content{llm.ImageBlock{}}, 4) != nil {
		t.Fatal("不是文本的那一块也被动了")
	}
	// 预算连那句提示都装不下时留的是它的**尾巴**，不是头：这条规矩是给「必须留住的
	// 后缀」定的，此处后缀为空，于是提示自己占了那个位置。
	bounded := boundSingleText(llm.Content{llm.TextBlock{Text: "abcdefgh"}}, 5)
	if textOf(bounded) != "ated]" {
		t.Fatalf("收出来的是 %q", textOf(bounded))
	}
}

// ---- 字节预算的旁路记账 ----

// TestOnlyTheTwoJobTargetedToolsHaveABudget 钉住那条边：job_list 交出去的清单
// 不属于任何一件作业，也就没有哪件作业的上限管得着它。
func TestOnlyTheTwoJobTargetedToolsHaveABudget(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.snapshots = []jobs.Snapshot{{ID: "j1", OutputLimitBytes: 64}}
	controller := w.controller(nil)

	cases := []struct {
		name    string
		exec    tools.Execution
		want    int
		wantAny bool
	}{
		{"清单没有预算", execFor(ListToolName, `{"job_id":"j1"}`, w.agentScope.Key()), 0, false},
		{"参数解不动", execFor(OutputToolName, `"nope"`, w.agentScope.Key()), 0, false},
		{"没给 id", execFor(OutputToolName, `{}`, w.agentScope.Key()), 0, false},
		{"这个调用方看不见那件作业", execFor(OutputToolName, `{"job_id":"j2"}`, w.agentScope.Key()), 0, false},
		{"读取拿得到预算", execFor(OutputToolName, `{"job_id":"j1"}`, w.agentScope.Key()), 64, true},
		{"取消拿得到预算", execFor(KillToolName, `{"job_id":"j1"}`, w.agentScope.Key()), 64, true},
		{"无身份的调用方", execFor(OutputToolName, `{"job_id":"j1"}`, nil), 64, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got, ok := controller.visibleOutputLimit(item.exec)
			if ok != item.wantAny || got != item.want {
				t.Fatalf("查出来的是 (%d, %v)，要的是 (%d, %v)", got, ok, item.want, item.wantAny)
			}
		})
	}
}

// execFor 造一份指名工具名和参数的执行。
func execFor(name, args string, key *scope.Key) tools.Execution {
	return tools.Execution{ExecutionInput: tools.ExecutionInput{
		Name:      name,
		Arguments: json.RawMessage(args),
		Agent:     key,
	}}
}

// TestTheRecordedBudgetIsRemovedOnFinalize 钉住那张按令牌索引的表：收尾就是它的
// 摘除点，不删它会一直长。
func TestTheRecordedBudgetIsRemovedOnFinalize(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.snapshots = []jobs.Snapshot{{ID: "j1", OutputLimitBytes: 64}}
	controller := w.controller(nil)
	exec := execFor(OutputToolName, `{"job_id":"j1"}`, w.agentScope.Key())

	called := false
	decision, err := controller.captureOutputLimit(exec, func() (tools.PreDecision, error) {
		called = true
		return tools.PreDecision{Kind: tools.PreAllow}, nil
	})
	if err != nil || !called || decision.Kind != tools.PreAllow {
		t.Fatalf("那条规则没把裁决原样递下去：%v %v %v", decision, err, called)
	}
	if len(controller.outputLimits) != 1 {
		t.Fatalf("派发前没记上账：%v", controller.outputLimits)
	}
	if maxBytes, ok := controller.takeOutputLimit(exec); !ok || maxBytes != 64 {
		t.Fatalf("取出来的是 (%d, %v)", maxBytes, ok)
	}
	if len(controller.outputLimits) != 0 {
		t.Fatalf("收尾之后那张表还有东西：%v", controller.outputLimits)
	}
	// 没记上账的那次调用回头现查一遍，结论一样。
	if maxBytes, ok := controller.takeOutputLimit(exec); !ok || maxBytes != 64 {
		t.Fatalf("现查出来的是 (%d, %v)", maxBytes, ok)
	}
}

// TestFinalizeSplitsTheDefaultRenderingAndFallsBackOtherwise 钉住那条拆法的前提：
// 只有内容确实就是照那个权威值渲染出来的，正文和状态行才拆得开。
func TestFinalizeSplitsTheDefaultRenderingAndFallsBackOtherwise(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	// 预算得容得下「一点正文 + 那句提示 + 那行状态」，否则提示自己就先被挤掉了，
	// 那验的就不是拆法而是最后一级的兜底。
	const limit = 45
	w.service.snapshots = []jobs.Snapshot{{ID: "j1", OutputLimitBytes: limit}}
	controller := w.controller(nil)
	exec := execFor(OutputToolName, `{"job_id":"j1"}`, w.agentScope.Key())

	value := mustJSON(t, outputResult{
		Text: strings.Repeat("x", 40),
		Job:  PublicSnapshot{ID: "j1", Status: string(jobs.StatusRunning)},
	})
	rendered := strings.Repeat("x", 40) + "\n[status: running]"

	t.Run("默认渲染没被动过", func(t *testing.T) {
		content := controller.finalizeContent(exec, tools.Result{
			Value:   value,
			Content: llm.Content{llm.TextBlock{Text: rendered}},
		})
		text := textOf(content)
		if len(text) > limit {
			t.Fatalf("收完还是 %d 字节：%q", len(text), text)
		}
		if !strings.HasSuffix(text, "\n[status: running]") {
			t.Fatalf("那行状态被让掉了：%q", text)
		}
		if !strings.Contains(text, outputOmitted) {
			t.Fatalf("正文被截了却没说：%q", text)
		}
	})

	t.Run("内容被别的规则改过", func(t *testing.T) {
		content := controller.finalizeContent(exec, tools.Result{
			Value:   value,
			Content: llm.Content{llm.TextBlock{Text: "policy rewrote this entirely and made it much longer"}},
		})
		text := textOf(content)
		if len(text) > limit {
			t.Fatalf("按老办法整段让之后还是 %d 字节：%q", len(text), text)
		}
		if !strings.Contains(text, resultOmitted) {
			t.Fatalf("走的不是整段让那条路：%q", text)
		}
	})

	t.Run("失败的结果按老办法让", func(t *testing.T) {
		content := controller.finalizeContent(exec, tools.Result{
			IsError: true,
			Content: llm.Content{llm.TextBlock{Text: strings.Repeat("e", 80)}},
		})
		if len(textOf(content)) > limit {
			t.Fatalf("失败结果没收住：%q", textOf(content))
		}
	})

	t.Run("值排不回来", func(t *testing.T) {
		content := controller.finalizeContent(exec, tools.Result{
			Value:   json.RawMessage(`"not an object"`),
			Content: llm.Content{llm.TextBlock{Text: strings.Repeat("y", 80)}},
		})
		if !strings.Contains(textOf(content), resultOmitted) {
			t.Fatalf("值排不回来时没退回整段让：%q", textOf(content))
		}
	})

	t.Run("没有预算就不动它", func(t *testing.T) {
		free := execFor(ListToolName, `{}`, w.agentScope.Key())
		if content := controller.finalizeContent(free, tools.Result{}); content != nil {
			t.Fatalf("没有预算却动了内容：%v", content)
		}
	})
}

// TestAnEmptyReadStillEndsWithAStatusLine 钉住那句空读：模型必须能把「这次没有
// 新东西」和「这件作业出问题了」分开。
func TestAnEmptyReadStillEndsWithAStatusLine(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.snapshots = []jobs.Snapshot{{ID: "j1", OutputLimitBytes: 200}}
	controller := w.controller(nil)
	exec := execFor(OutputToolName, `{"job_id":"j1"}`, w.agentScope.Key())
	value := mustJSON(t, outputResult{Job: PublicSnapshot{ID: "j1", Status: string(jobs.StatusRunning)}})
	rendered := noNewOutput + "\n[status: running]"

	fromRender, err := controller.newOutputTool().Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(fromRender) != rendered {
		t.Fatalf("空读渲染成了 %q", textOf(fromRender))
	}

	content := controller.finalizeContent(exec, tools.Result{
		Value:   value,
		Content: llm.Content{llm.TextBlock{Text: rendered}},
	})
	if textOf(content) != rendered {
		t.Fatalf("放得下却被改了：%q", textOf(content))
	}
}

// TestARenderedBodyKeepsExactlyOneTrailingNewline 钉住那条边界：正文自己带换行时
// 不再补一个，拆回去时也只削掉那一个。
func TestARenderedBodyKeepsExactlyOneTrailingNewline(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	render := controller.newOutputTool().Output.Render

	value := mustJSON(t, outputResult{
		Text: "line\n",
		Job:  PublicSnapshot{ID: "j1", Status: string(jobs.StatusRunning), Detail: "pid 7"},
	})
	content, err := render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "line\n[status: running, pid 7]" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
	if _, err := render(nil, json.RawMessage(`"nope"`)); err == nil {
		t.Fatal("值排不回来却渲染成功了")
	}
}

// ---- 三件工具 ----

func TestJobOutputValidatesTheIdAndHonoursTheWaitCap(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.read = jobs.Read{Text: "hello", Snapshot: jobs.Snapshot{ID: "j1", Status: jobs.StatusRunning}}
	controller := w.controller(func(config *Config) {
		config.WaitTimeout = time.Second
		config.MaxWaitTimeout = 2 * time.Second
	})

	if _, err := controller.readOutput(t.Context(), json.RawMessage(`{"job_id":""}`), w.execOn()); err == nil {
		t.Fatal("空 job_id 也读得动")
	}
	if _, err := controller.readOutput(t.Context(), json.RawMessage(`"nope"`), w.execOn()); err == nil {
		t.Fatal("参数解不动也读得动")
	}

	// 没写 timeout_ms 取默认。
	if _, err := controller.readOutput(t.Context(), json.RawMessage(`{"job_id":"j1","wait":true}`), w.execOn()); err != nil {
		t.Fatalf("读失败：%v", err)
	}
	// 写了就用它，但夹到硬上限。
	args := json.RawMessage(`{"job_id":"j1","wait":true,"timeout_ms":9000}`)
	if _, err := controller.readOutput(t.Context(), args, w.execOn()); err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if len(w.service.waits) != 2 {
		t.Fatalf("等了 %d 次", len(w.service.waits))
	}
	if w.service.waits[0].timeout != time.Second {
		t.Fatalf("没写 timeout_ms 时等了 %v", w.service.waits[0].timeout)
	}
	if w.service.waits[1].timeout != 2*time.Second {
		t.Fatalf("超过上限的 timeout_ms 没被夹住：%v", w.service.waits[1].timeout)
	}
	if w.service.waits[0].caller != w.caller {
		t.Fatal("等待没带上那个确切的调用方")
	}

	// 不等的时候直接读。
	value, err := controller.readOutput(t.Context(), json.RawMessage(`{"job_id":"j1"}`), w.execOn())
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	var decoded outputResult
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("解结果失败：%v", err)
	}
	if decoded.Text != "hello" || decoded.Job.ID != "j1" {
		t.Fatalf("读出来的是 %+v", decoded)
	}
}

func TestJobOutputSurfacesServiceFailures(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)

	w.service.waitErr = errors.New("等崩了")
	if _, err := controller.readOutput(t.Context(), json.RawMessage(`{"job_id":"j1","wait":true}`), w.execOn()); err == nil {
		t.Fatal("等待失败被吞了")
	}
	w.service.waitErr = nil
	w.service.readErr = errors.New("读崩了")
	if _, err := controller.readOutput(t.Context(), json.RawMessage(`{"job_id":"j1"}`), w.execOn()); err == nil {
		t.Fatal("读取失败被吞了")
	}
}

// TestJobListNeverRendersNull 钉住那条空清单：模型看见 null 读不出「一个都没有」，
// 那份数组契约也验不过。
func TestJobListNeverRendersNull(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	definition := controller.newListTool()

	value, err := controller.listJobs(t.Context(), nil, w.execOn())
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if string(value) != "[]" {
		t.Fatalf("空清单排成了 %s", value)
	}
	content, err := definition.Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != noJobs {
		t.Fatalf("空清单渲染成了 %q", textOf(content))
	}

	w.service.snapshots = []jobs.Snapshot{
		{ID: "j1", Kind: jobs.KindBash, Label: "npm test", Status: jobs.StatusRunning},
		{ID: "j2", Kind: jobs.KindSubagent, Label: "review", Status: jobs.StatusCompleted},
	}
	value, err = controller.listJobs(t.Context(), nil, w.execOn())
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	content, err = definition.Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	want := "j1 [bash] running — npm test\nj2 [subagent] completed — review"
	if textOf(content) != want {
		t.Fatalf("列出来的是 %q", textOf(content))
	}
	if _, err := definition.Output.Render(nil, json.RawMessage(`"nope"`)); err == nil {
		t.Fatal("值排不回来却渲染成功了")
	}
}

func TestJobKillReportsTheOutcomeWithoutConsumingOutput(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.snapshots = []jobs.Snapshot{
		{ID: "j1", Kind: jobs.KindBash, Label: "npm test", Status: jobs.StatusStopping},
	}
	controller := w.controller(nil)
	definition := controller.newKillTool()

	value, err := controller.killJob(t.Context(), json.RawMessage(`{"job_id":"j1","reason":"changed my mind"}`), w.execOn())
	if err != nil {
		t.Fatalf("取消失败：%v", err)
	}
	if len(w.service.kills) != 1 || w.service.kills[0].reason != "changed my mind" {
		t.Fatalf("递过去的取消是 %+v", w.service.kills)
	}
	if w.service.kills[0].caller != w.caller {
		t.Fatal("取消没带上那个确切的调用方")
	}
	content, err := definition.Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "requested cancellation of job j1" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}

	w.service.killResult = jobs.KillAlreadyFinished
	w.service.snapshots = []jobs.Snapshot{
		{ID: "j1", Kind: jobs.KindBash, Label: "npm test", Status: jobs.StatusCompleted, Detail: "exit 0"},
	}
	value, err = controller.killJob(t.Context(), json.RawMessage(`{"job_id":"j1"}`), w.execOn())
	if err != nil {
		t.Fatalf("取消失败：%v", err)
	}
	content, err = definition.Output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	if textOf(content) != "job j1 had already finished [status: completed, exit 0]" {
		t.Fatalf("渲染出来的是 %q", textOf(content))
	}
	if _, err := definition.Output.Render(nil, json.RawMessage(`"nope"`)); err == nil {
		t.Fatal("值排不回来却渲染成功了")
	}
}

func TestJobKillSurfacesFailures(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)

	if _, err := controller.killJob(t.Context(), json.RawMessage(`"nope"`), w.execOn()); err == nil {
		t.Fatal("参数解不动也取消得动")
	}
	if _, err := controller.killJob(t.Context(), json.RawMessage(`{"job_id":""}`), w.execOn()); err == nil {
		t.Fatal("空 job_id 也取消得动")
	}
	w.service.killErr = errors.New("取消崩了")
	if _, err := controller.killJob(t.Context(), json.RawMessage(`{"job_id":"j1"}`), w.execOn()); err == nil {
		t.Fatal("取消失败被吞了")
	}
	w.service.killErr = nil
	w.service.getErr = errors.New("取快照崩了")
	if _, err := controller.killJob(t.Context(), json.RawMessage(`{"job_id":"j1"}`), w.execOn()); err == nil {
		t.Fatal("取快照失败被吞了")
	}
}

// TestAnAnonymousCallerSeesOnlyUnownedJobs 钉住那条可见范围：查不回 agent 不是错，
// 它拿到的是最紧的一档，不是最松的。
func TestAnAnonymousCallerSeesOnlyUnownedJobs(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	if controller.callerOf(nil) != nil {
		t.Fatal("没落在 agent 上却查出了一个调用方")
	}
	if controller.callerOf(w.root.Key()) != nil {
		t.Fatal("查不回来却给了一个调用方")
	}
	if controller.callerOf(w.agentScope.Key()) != agent.Agent(w.caller) {
		t.Fatal("查得回来却没给那个确切的调用方")
	}
	if execAgent(nil) != nil {
		t.Fatal("空执行上下文却给出了一把钥匙")
	}
}

func TestTheCardsCarryTheJobIdWhenThereIsOne(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)

	view := controller.newOutputTool().PresentCall(json.RawMessage(`{"job_id":"j1"}`))
	generic, ok := view.(tools.GenericCallView)
	if !ok || generic.Title != "Read output from background job j1" || generic.Kind != tools.CallRead {
		t.Fatalf("读取那张卡片是 %+v", view)
	}
	if string(generic.RawInput) != `"j1"` {
		t.Fatalf("卡片上的入参是 %s", generic.RawInput)
	}
	kill := controller.newKillTool().PresentCall(json.RawMessage(`{"job_id":"j1"}`)).(tools.GenericCallView)
	if kill.Kind != tools.CallExecute {
		t.Fatalf("取消那张卡片的种类是 %q", kill.Kind)
	}
	list := controller.newListTool().PresentCall(nil).(tools.GenericCallView)
	if list.RawInput != nil {
		t.Fatalf("清单那张卡片带了入参：%s", list.RawInput)
	}
	empty := controller.newOutputTool().PresentCall(json.RawMessage(`{}`)).(tools.GenericCallView)
	if empty.RawInput != nil {
		t.Fatalf("空 job_id 进了卡片：%s", empty.RawInput)
	}
}

// ---- 投递 ----

// TestAnIdleOwnerIsWokenUntilTheBudgetRunsOut 钉住那条自激链的闸：预算花完之后
// 通知降级成注入。
func TestAnIdleOwnerIsWokenUntilTheBudgetRunsOut(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(func(config *Config) { config.MaxConsecutiveWakes = 2 })
	snapshot := jobs.Snapshot{ID: "j1", Kind: jobs.KindBash, Label: "npm test", Status: jobs.StatusCompleted}

	for range 3 {
		w.service.listener(snapshot, w.caller)
	}
	if len(w.caller.followups) != 2 {
		t.Fatalf("唤醒了 %d 次，预算是 2", len(w.caller.followups))
	}
	if len(w.caller.injects) != 1 {
		t.Fatalf("超出预算之后注入了 %d 次", len(w.caller.injects))
	}

	message := w.caller.followups[0]
	source, ok := message.Source.(llm.PluginSource)
	if !ok || source.Plugin != PluginName {
		t.Fatalf("那条通知的来源是 %+v", message.Source)
	}
	notice, ok := source.Context.(llm.NoticeContext)
	if !ok || notice.Summary == "" {
		t.Fatalf("那条通知没带一行折叠陈述：%+v", source.Context)
	}
	if !strings.Contains(textOf(message.Content), "job_output") {
		t.Fatalf("那条通知没说该去读什么：%q", textOf(message.Content))
	}

	// 一条人写的输入被认领，预算清零。
	w.agents.observer(w.caller, llm.NewUserMessage(nil, llm.UserSource{}), 1)
	w.service.listener(snapshot, w.caller)
	if len(w.caller.followups) != 3 {
		t.Fatalf("人写的输入没把预算补回来：唤醒了 %d 次", len(w.caller.followups))
	}

	// 本插件自己排进去的那条通知不许把预算补回来。
	w.agents.observer(w.caller, message, 2)
	if len(controller.spentWakes) == 0 {
		t.Fatal("插件自己那条通知把预算补回来了")
	}
}

// TestABusyOwnerIsAlwaysInjected 钉住忙着的属主那条路：注入，于是同时落定的一堆
// 作业只花掉一步。
func TestABusyOwnerIsAlwaysInjected(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.caller.status = agent.StatusRunning
	w.install(nil)
	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, w.caller)
	if len(w.caller.followups) != 0 {
		t.Fatal("忙着的属主被唤醒了")
	}
	if len(w.caller.injects) != 1 {
		t.Fatalf("忙着的属主收到了 %d 条注入", len(w.caller.injects))
	}
}

// TestQuietDeliveryNeverOpensATurn 钉住 quiet：它一个回合都不开，也就不需要那条
// 补给线。
func TestQuietDeliveryNeverOpensATurn(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(func(config *Config) { config.CompletionDelivery = DeliveryQuiet })
	deps := w.deps()
	deps.Agents = nil
	undo, err := controller.Install(t.Context(), w.root, deps)
	if err != nil {
		t.Fatalf("quiet 之下装不上：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, w.caller)
	if len(w.caller.followups) != 0 {
		t.Fatal("quiet 之下还是开了一个回合")
	}
	if len(w.caller.injects) != 1 {
		t.Fatalf("quiet 之下注入了 %d 次", len(w.caller.injects))
	}
}

// TestSettledJobsWithNoOneToTellAreDropped 钉住那两条不投的路：已汇报的结算和
// 没有属主的结算。
func TestSettledJobsWithNoOneToTellAreDropped(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.install(nil)
	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted, Reported: true}, w.caller)
	w.service.listener(jobs.Snapshot{ID: "j2", Status: jobs.StatusCompleted}, nil)
	if len(w.caller.followups)+len(w.caller.injects) != 0 {
		t.Fatal("不该投的结算被投出去了")
	}
}

// TestAnOwnerWithoutALiveScopeIsNeverWoken 钉住那条挂不上清理的路：那一行记账
// 会一直留在表里没人删，所以宁可退回注入。
func TestAnOwnerWithoutALiveScopeIsNeverWoken(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.install(nil)

	homeless := &stubAgent{id: "homeless", status: agent.StatusIdle}
	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, homeless)
	if len(homeless.followups) != 0 || len(homeless.injects) != 1 {
		t.Fatalf("没有作用域的属主被唤醒了：%d/%d", len(homeless.followups), len(homeless.injects))
	}

	dead := scopeOf(t, "dead", w.root)
	if err := dead.Dispose(context.Background()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	buried := &stubAgent{id: "buried", status: agent.StatusIdle, own: dead}
	w.service.listener(jobs.Snapshot{ID: "j2", Status: jobs.StatusCompleted}, buried)
	if len(buried.followups) != 0 || len(buried.injects) != 1 {
		t.Fatalf("正在散的属主被唤醒了：%d/%d", len(buried.followups), len(buried.injects))
	}
}

// TestDisposingTheOwnerClearsItsWakeBudget 钉住那项挂在属主身上的清理：属主散掉，
// 它那行记账跟着走。
func TestDisposingTheOwnerClearsItsWakeBudget(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(nil)
	own := scopeOf(t, "short-lived", w.root)
	owner := &stubAgent{id: "short-lived", status: agent.StatusIdle, own: own}

	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, owner)
	if len(controller.spentWakes) != 1 || len(controller.wakeCleanups) != 1 {
		t.Fatalf("记账没落上：%v %v", controller.spentWakes, controller.wakeCleanups)
	}
	if err := own.Dispose(context.Background()); err != nil {
		t.Fatalf("释放属主作用域失败：%v", err)
	}
	if len(controller.spentWakes) != 0 || len(controller.wakeCleanups) != 0 {
		t.Fatalf("属主散了记账还在：%v %v", controller.spentWakes, controller.wakeCleanups)
	}
}

// TestUninstallingReleasesEveryOwnerCleanup 钉住那处泄漏：那些闭包挂在属主自己的
// 作用域上，活得比本包一次装配长，不摘就一直在。
func TestUninstallingReleasesEveryOwnerCleanup(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	undo, err := controller.Install(t.Context(), w.root, w.deps())
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, w.caller)
	if len(controller.wakeCleanups) != 1 {
		t.Fatalf("清理没挂上：%v", controller.wakeCleanups)
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	if len(controller.wakeCleanups) != 0 || len(controller.spentWakes) != 0 {
		t.Fatalf("拆除之后记账还在：%v %v", controller.wakeCleanups, controller.spentWakes)
	}
}

// TestOnlyHumanInputRefillsTheBudget 钉住那条判别：只有 [ds-harness-go/llm.SourceUser]
// 算数。
func TestOnlyHumanInputRefillsTheBudget(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.install(nil)
	w.service.listener(jobs.Snapshot{ID: "j1", Status: jobs.StatusCompleted}, w.caller)
	if controller.spentWakes[w.caller] != 1 {
		t.Fatalf("预算没花掉：%v", controller.spentWakes)
	}
	controller.refillWakeBudget(w.caller, llm.Message{}, 1)
	if controller.spentWakes[w.caller] != 1 {
		t.Fatal("一条没有来源的消息把预算补回来了")
	}
	controller.refillWakeBudget(w.caller, llm.NewUserMessage(nil, llm.UserSource{}), 1)
	if _, ok := controller.spentWakes[w.caller]; ok {
		t.Fatal("人写的输入没把预算补回来")
	}
}

// ---- 装配 ----

// TestTheWholeSeamInstallsAndComesOffTogether 钉住那次装配：三件工具、指引、
// 控制器和完成监听器一起上，摘掉之后一样都不剩。
func TestTheWholeSeamInstallsAndComesOffTogether(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	controller := w.controller(nil)
	undo, err := controller.Install(t.Context(), w.root, w.deps())
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	names := []string{OutputToolName, ListToolName, KillToolName}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); !ok {
			t.Fatalf("%s 没装上", name)
		}
	}
	if w.service.attached != PluginName {
		t.Fatalf("控制器挂上来的名字是 %q", w.service.attached)
	}
	if w.service.listener == nil {
		t.Fatal("完成监听器没装上")
	}
	if w.agents.observer == nil {
		t.Fatal("唤醒预算的补给线没装上")
	}
	assembly, err := w.prompts.Assemble(t.Context(), systemprompt.AssembleContext{Scope: w.root.Key()})
	if err != nil {
		t.Fatalf("装配提示词失败：%v", err)
	}
	prompt, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		t.Fatalf("渲染提示词失败：%v", err)
	}
	if !strings.Contains(prompt, "job_output") {
		t.Fatalf("那段指引没进提示词：\n%s", prompt)
	}

	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); ok {
			t.Fatalf("%s 摘掉之后还在", name)
		}
	}
	if w.service.attached != "" || w.service.listener != nil || w.agents.observer != nil {
		t.Fatal("摘掉之后还有东西挂着")
	}
}

// TestInstallRefusesAnIncompleteAssembly 钉住那几条缺件：半装上去意味着模型手上
// 有一件读得了作业、却收不到任何完成通知的工具。
func TestInstallRefusesAnIncompleteAssembly(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*world, *Config, *Deps){
		"没有工具运行时":       func(_ *world, _ *Config, deps *Deps) { deps.Tools = nil },
		"没有提示词注册表":      func(_ *world, _ *Config, deps *Deps) { deps.Prompts = nil },
		"wakeup 却没有补给线": func(_ *world, _ *Config, deps *Deps) { deps.Agents = nil },
		"补给线装不上": func(w *world, _ *Config, _ *Deps) {
			w.agents.err = errors.New("补给线崩了")
		},
		"控制器挂不上": func(w *world, _ *Config, _ *Deps) {
			w.service.attachErr = errors.New("控制器崩了")
		},
		"完成监听器装不上": func(w *world, _ *Config, _ *Deps) {
			w.service.doneErr = errors.New("监听器崩了")
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			config := w.config()
			deps := w.deps()
			damage(w, &config, &deps)
			controller, err := New(config)
			if err != nil {
				t.Fatalf("造控制器失败：%v", err)
			}
			if _, err := controller.Install(t.Context(), w.root, deps); err == nil {
				t.Fatal("缺件还装得上")
			}
			if w.service.attached != "" || w.service.listener != nil {
				t.Fatal("装失败了还留着东西")
			}
			for _, toolName := range []string{OutputToolName, ListToolName, KillToolName} {
				if _, ok := w.tools.Get(toolName, w.root.Key()); ok {
					t.Fatalf("装失败了 %s 还在", toolName)
				}
			}
		})
	}
}

// TestInstallUnwindsWhenAToolNameIsTaken 钉住那条反序摘除：一件工具装不上，
// 前面装上去的全收回来。
func TestInstallUnwindsWhenAToolNameIsTaken(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	occupied := &tools.Definition{
		Name:       KillToolName,
		Parameters: tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: "occupied"}}, nil
			},
		},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}
	remove, err := w.tools.Register(t.Context(), w.root, occupied)
	if err != nil {
		t.Fatalf("占名失败：%v", err)
	}
	t.Cleanup(func() { _ = remove(context.Background()) })

	controller := w.controller(nil)
	if _, err := controller.Install(t.Context(), w.root, w.deps()); err == nil {
		t.Fatal("名字被占了还装得上")
	}
	for _, name := range []string{OutputToolName, ListToolName} {
		if _, ok := w.tools.Get(name, w.root.Key()); ok {
			t.Fatalf("装失败了 %s 还在", name)
		}
	}
	if w.service.attached != "" || w.service.listener != nil || w.agents.observer != nil {
		t.Fatal("装失败了还留着东西")
	}
}

// TestInstallUnwindsWhenTheScopeIsGone 钉住那两条「登记方自己已经散了」的路：
// 派发前记账和那段指引都装不上。
func TestInstallUnwindsWhenTheScopeIsGone(t *testing.T) {
	t.Parallel()

	t.Run("派发前记账装不上", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		controller := w.controller(nil)
		dead := scopeOf(t, "dead", w.root)
		if err := dead.Dispose(context.Background()); err != nil {
			t.Fatalf("释放作用域失败：%v", err)
		}
		if _, err := controller.Install(t.Context(), dead, w.deps()); err == nil {
			t.Fatal("作用域散了还装得上")
		}
	})

	t.Run("指引装不上", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		controller := w.controller(nil)
		// 先把那个段落名占掉：工具那一侧还活着，于是失败点恰好落在装指引这一步，
		// 而它前面那两步（派发前记账、挂控制器）已经装上了，正好验反序摘除。
		release, err := w.prompts.Section(t.Context(), w.root, systemprompt.PromptSection{
			Name: SectionName,
			Text: systemprompt.StaticText("someone else got here first"),
		})
		if err != nil {
			t.Fatalf("占段落名失败：%v", err)
		}
		t.Cleanup(func() { _ = release(context.Background()) })

		if _, err := controller.Install(t.Context(), w.root, w.deps()); err == nil {
			t.Fatal("段落名被占了还装得上")
		}
		if w.service.attached != "" {
			t.Fatal("装失败了控制器还挂着")
		}
	})
}

// TestTheToolsRunThroughTheRealRuntime 钉住那条端到端的路：真运行时派发一次
// job_output，那份预算记账和收尾一起生效。
func TestTheToolsRunThroughTheRealRuntime(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.service.snapshots = []jobs.Snapshot{{ID: "j1", OutputLimitBytes: 40, Status: jobs.StatusRunning}}
	w.service.read = jobs.Read{
		Text:     strings.Repeat("z", 200),
		Snapshot: jobs.Snapshot{ID: "j1", Status: jobs.StatusRunning},
	}
	controller := w.install(nil)

	result := w.tools.Execute(t.Context(), tools.ExecutionInput{
		CallID:    "call-1",
		Name:      OutputToolName,
		Arguments: json.RawMessage(`{"job_id":"j1"}`),
		Agent:     w.agentScope.Key(),
	})
	if result.IsError {
		t.Fatalf("派发失败：%+v", result.Error)
	}
	text := textOf(result.Content)
	if len(text) > 40 {
		t.Fatalf("收尾没把内容收进预算：%d 字节", len(text))
	}
	if !strings.HasSuffix(text, "\n[status: running]") {
		t.Fatalf("那行状态被让掉了：%q", text)
	}
	if len(controller.outputLimits) != 0 {
		t.Fatalf("派发完那张表还有东西：%v", controller.outputLimits)
	}
}
