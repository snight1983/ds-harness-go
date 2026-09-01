// 本文件的作用：把装配那一条路和它挂上去的四条观察者压一遍——配置和协作者
// 在哪一步验、一条工具触碰怎么走到收件箱、一个提议中的步骤上那条上下文折在哪儿，
// 以及整层摘下来之后还剩不剩活口。

package instructions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 一个协作者没给却照样装上去了，于是这一层在运行期才空指针——而它是异步的，
//     那一声 panic 落在一个跟装配现场毫无关系的协程上。
//   - 四条观察者装到一半失败，已经挂上的那几条留在瀑布上：整层没装成，
//     却仍然在改别人的收件箱。
//   - 一份基线被重复发进对话：模型会把同一批指令读成「这件事又发生了一次」。
//   - 一次工具触碰在步骤**开着**的时候就投影出去，于是模型看见一次可能被回滚掉的写。
//   - 一次嵌套调用刚收摊就投影，而外层还在跑、随时可能失败。
//   - 收件箱里排着的那条和这一次算出来的说的是同一件事，却被换了一个新身份——
//     已经认过它的地方会再认一次。
//   - 一个空的第一步被塞进上下文，于是一个本来不发请求的回合变成一次模型调用。
//   - 摘下来之后还有在飞的投影往一个已经不归本包管的收件箱里写。

// ---- 路径 ----
//
// 会话头要求 cwd 在**本机**上绝对（见 core/session/validate.go 那条），
// 而假文件系统按 POSIX 形状的键取。所以先取一条本机绝对路径，
// 再用本包自己的 [absPath] 折成假件认得的那一条——写死哪一边的字面量
// 都会让另一个平台上的用例变成假通过。
var (
	// workspaceCwd 是这个 agent 报出来的工作目录，本机绝对。
	workspaceCwd = filepath.Join(os.TempDir(), "ds-harness-go-instructions", "repo", "app")
	// fakeCwd 是同一条路径在假文件系统里的键。
	fakeCwd = absPath(workspaceCwd)
	// fakeRoot 是项目根，`.git` 就放在它下面。
	fakeRoot = path.Dir(fakeCwd)
)

// ---- 假注册表 ----

// captureAgents 是一台只把观察者接下来的假 agent 注册表。
//
// 用假的而不是真 [agent.Registry]：这些用例问的是「这一层在各种输入下做了什么」，
// 真注册表还要求把 agent 挂进去并公布，那一整套跟本包要验的事无关。
type captureAgents struct {
	preStep  agent.PreStepObserver
	disposed agent.DisposedObserver

	// preStepFail 与 disposedFail 非 nil 时那一条登记当场失败。
	preStepFail  error
	disposedFail error

	mutex   sync.Mutex
	removed []string
}

func (a *captureAgents) OnPreStep(
	_ context.Context,
	_ *scope.Scope,
	observer agent.PreStepObserver,
) (func(context.Context) error, error) {
	if a.preStepFail != nil {
		return nil, a.preStepFail
	}
	a.preStep = observer
	return a.record("前置步骤"), nil
}

func (a *captureAgents) OnDisposed(
	_ context.Context,
	_ *scope.Scope,
	observer agent.DisposedObserver,
) (func(context.Context) error, error) {
	if a.disposedFail != nil {
		return nil, a.disposedFail
	}
	a.disposed = observer
	return a.record("处置"), nil
}

// record 造一个「被摘的时候记下自己名字」的撤销函数。
func (a *captureAgents) record(label string) func(context.Context) error {
	return func(context.Context) error {
		a.mutex.Lock()
		defer a.mutex.Unlock()
		a.removed = append(a.removed, label)
		return nil
	}
}

// captureSessions 是一台只把会话事件观察者接下来的假广播。
type captureSessions struct {
	observer coresession.EventObserver
	fail     error
	agents   *captureAgents
}

func (s *captureSessions) OnEvent(
	_ context.Context,
	_ *scope.Scope,
	observer coresession.EventObserver,
) (func(context.Context) error, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	s.observer = observer
	return s.agents.record("会话事件"), nil
}

// ---- 假 agent ----

// stubAgent 是一个只有会话和收件箱的 agent。
//
// 收件箱是**真的** [agent.Inbox]：本层要验的正是它怎么被增删改，
// 一个记调用次数的桩会把「排着的那条留没留住自己的身份」整个藏起来。
//
// 三条改动方法各自走一遍这把锁，和 [github.com/snight1983/ds-harness-go/core/agentloop.ReactLoopAgent]
// 同一个理由：收件箱不加锁，而本层的投影跑在各自的协程上。
type stubAgent struct {
	id    session.SessionID
	sess  *coresession.Session
	key   *scope.Key
	scope *scope.Scope

	mutex sync.Mutex
	inbox *agent.Inbox
	// failures 收下改动收件箱时冒出来的错，用例末尾查它。
	failures []error
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return a.inbox }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.scope }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *stubAgent) Prepend(message llm.Message, target agent.InboxTarget) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, err := a.inbox.Splice(target, 0, 0, []llm.Message{message}); err != nil {
		a.failures = append(a.failures, err)
	}
}

func (a *stubAgent) Remove(messageID llm.MessageID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, err := a.inbox.Remove(messageID); err != nil {
		a.failures = append(a.failures, err)
	}
}

func (a *stubAgent) Replace(messageID llm.MessageID, newMessage llm.Message) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, err := a.inbox.Replace(messageID, newMessage); err != nil {
		a.failures = append(a.failures, err)
	}
}

// pending 交出收件箱里排着等下一步的那些消息。
func (a *stubAgent) pending() []llm.Message {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.inbox.NextStep()
}

// ---- 夹具 ----

// world 是一次装好的工作区指令层，外加它那个 agent。
type world struct {
	t        *testing.T
	fsys     *fakeFS
	agents   *captureAgents
	sessions *captureSessions
	runtime  *tools.Runtime
	live     *stubAgent
	remove   func(context.Context) error
	// install 是这一层自己，那几条只有从内部才看得见的账（会话状态表、
	// 投影那条命）由它交出来。
	install *installer
	// logs 收这一层写出来的诊断，用来钉「丢掉一次触碰时确实说了一声」。
	logs *syncBuffer
}

// newWorld 摆一个最小可用的工作区，把这一层装上去。
//
// 工作区里有一份项目根指令，因为绝大多数用例要看的是「基线怎么发出去、
// 之后怎么不再重发」，而一个空工作区连第一条上下文都产不出来。
//
// 这里走的是 [newInstaller] 加 [installer.observe] 而不是 [Install]：
// 两个用例要查这一层内部那张会话状态表，而 [Install] 只交回一个撤销函数。
// [Install] 自己那几行由 Test装配这一层能整个装上去 压着。
func newWorld(t *testing.T, config Config) *world {
	t.Helper()

	fsys := newFakeFS()
	fsys.addDir(fakeRoot).addDir(fakeCwd).addFile(fakeRoot+"/.git", "gitdir")
	fsys.addFile(fakeRoot+"/AGENTS.md", "根上的规矩")

	agents := &captureAgents{}
	sessions := &captureSessions{agents: agents}
	runtime := mustRuntime(t)
	logs := &syncBuffer{}

	made := &world{
		t: t, fsys: fsys, agents: agents, sessions: sessions,
		runtime: runtime, logs: logs,
	}
	made.live = newStubAgent(t, "s")

	owner := scope.NewRoot()
	for _, name := range []string{"read", "write", "edit", "grep", "compound"} {
		if _, err := runtime.Register(t.Context(), owner, touchTool(name, runtime, made.live.key)); err != nil {
			t.Fatalf("注册 %q 失败：%v", name, err)
		}
	}

	deps := Deps{
		Agents:   agents,
		Sessions: sessions,
		Tools:    runtime,
		FS:       fsys,
		Logger:   slog.New(slog.NewTextHandler(logs, nil)),
		AgentOf: func(key *scope.Key) (agent.Agent, error) {
			if key == made.live.key {
				return made.live, nil
			}
			return nil, errUnknownAgent
		},
	}
	install, err := newInstaller(t.Context(), config, deps)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	undo, err := install.observe(t.Context(), owner, deps)
	if err != nil {
		t.Fatalf("挂不上观察者：%v", err)
	}
	made.install = install
	made.remove = func(ctx context.Context) error {
		install.shutdown()
		return undo(ctx)
	}
	t.Cleanup(func() { _ = made.remove(context.Background()) })
	return made
}

// syncBuffer 是一个加了锁的字节缓冲。
//
// 诊断是从投影那些协程里写出来的，而用例在自己这条协程上读它。
type syncBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.String()
}

// newStubAgent 造一个带真收件箱的假 agent。
func newStubAgent(t *testing.T, id session.SessionID) *stubAgent {
	t.Helper()

	live, err := coresession.NewSession(id, coresession.Options{
		Header: &session.SessionHeader{ID: id, Cwd: workspaceCwd},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	inbox, err := agent.NewInbox(live, agent.InboxNotifications{})
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}
	return &stubAgent{
		id:    id,
		sess:  live,
		key:   scope.NewKey(string(id)),
		scope: scope.NewRoot(),
		inbox: inbox,
	}
}

// errUnknownAgent 是「这把钥匙查不回一个 agent」那条哨兵。
var errUnknownAgent = errors.New("这把钥匙不认识")

// touchTool 造一个「碰了一个文件」的工具。
//
// 名字叫 compound 的那个会派发一次嵌套的 read，用来压「嵌套调用的触碰要沿着
// Parent 往上并到根上才放出去」那条路——而一个非零的 [tools.ExecutionToken]
// 只有真的走一遍运行时才拿得到，它的字段是未导出的。
func touchTool(name string, runtime *tools.Runtime, key *scope.Key) *tools.Definition {
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
		Execute: func(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
			if name == "compound" {
				nested := runtime.Execute(ctx, tools.ExecutionInput{
					CallID:    "nested",
					Name:      "read",
					Arguments: args,
					Agent:     key,
					Parent:    exec.Token,
				})
				if nested.IsError {
					return nil, errors.New("嵌套调用失败了")
				}
			}
			var parsed struct {
				Fail bool `json:"fail"`
			}
			_ = json.Unmarshal(args, &parsed)
			if parsed.Fail {
				return nil, errors.New("这次调用故意失败")
			}
			return json.Marshal("ok")
		},
	}
}

// call 代表这个 agent 调一次工具，交出它的结果。
func (w *world) call(name string, arguments string) tools.Result {
	w.t.Helper()
	return w.runtime.Execute(w.t.Context(), tools.ExecutionInput{
		CallID:    llm.CallID("c"),
		Name:      name,
		Arguments: json.RawMessage(arguments),
		Agent:     w.live.key,
	})
}

// touchArgs 是一次碰到项目根那份指令的调用参数。
func touchArgs() string {
	encoded, _ := json.Marshal(struct {
		FilePath string `json:"file_path"`
	}{FilePath: fakeRoot + "/AGENTS.md"})
	return string(encoded)
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
	// 会话事件观察者在真运行时里是被广播喂的，这台装配里由用例自己转发。
	if w.sessions.observer != nil {
		w.sessions.observer(w.live.sess, written)
	}
	return written
}

// openStep 开一个步骤。
func (w *world) openStep(turn, step int) {
	w.t.Helper()
	w.append(session.EventStepStart, session.StepStartData{Turn: turn, Step: step}, nil)
}

// endStep 关一个步骤。
func (w *world) endStep(turn, step int) {
	w.t.Helper()
	w.append(session.EventStepEnd, session.StepEndData{Turn: turn, Step: step}, nil)
}

// commit 把一批消息当成「这一步真的发出去了」写进会话表面。
func (w *world) commit(messages []llm.Message) {
	w.t.Helper()
	for _, message := range messages {
		w.append(session.EventUserMessage, session.UserMessageData{Message: message}, session.AppendOp{})
	}
}

// run 跑一遍前置步骤那条规则，里层交出一个带着 given 的准入决定。
func (w *world) run(step int, given ...llm.Message) agent.PreStepDecision {
	w.t.Helper()
	decision, err := w.agents.preStep(w.t.Context(), agent.PreStep{
		Agent: w.live, Messages: given, Turn: 1, Step: step,
	}, func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep(given), nil
	})
	if err != nil {
		w.t.Fatalf("这条规则不该报错：%v", err)
	}
	return decision
}

// waitForInbox 等收件箱里等下一步的消息数量变成 want，等不到就当场失败。
//
// 投影是异步的，而 [installer.wait] 只有从前置步骤那条路进得去。
func (w *world) waitForInbox(want int) []llm.Message {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending := w.live.pending()
		if len(pending) == want {
			return pending
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("等收件箱变成 %d 条超时，实际 %d 条", want, len(pending))
		}
		time.Sleep(time.Millisecond)
	}
}

// waitForTail 等这段会话的投影队尾摆上，等不到就当场失败。
//
// 「投影还在飞」这个时刻没有别的观察点：队尾是在 [installer.queue] 里同步摆上的，
// 而那次投影自己跑在另一条协程上。
func (w *world) waitForTail() {
	w.t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if state, present := w.install.lookupState(w.live.id); present {
			state.mutex.Lock()
			tail := state.tail
			state.mutex.Unlock()
			if tail != nil {
				return
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatal("等投影排上队超时")
		}
		time.Sleep(time.Millisecond)
	}
}

// ours 从一批消息里挑出本层署名的那些。
func ours(messages []llm.Message) []llm.Message { return workspaceContexts(messages) }

// textOf 把一条消息里的文本块拼起来。
func textOf(message llm.Message) string {
	var builder strings.Builder
	for _, block := range message.Content {
		if typed, ok := block.(llm.TextBlock); ok {
			builder.WriteString(typed.Text)
		}
	}
	return builder.String()
}

// appendQuiet 只往日志上写，不把这条事件转给会话观察者。
//
// 用来摆出「这一层是在一段会话已经跑了一阵之后才第一次看见它」那个局面：
// 那时状态表里还没有这段会话，步骤开着没有只能靠重放整段日志算出来。
func (w *world) appendQuiet(kind session.EventType, payload any) {
	w.t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		w.t.Fatalf("负载排不出去：%v", err)
	}
	if _, err := w.live.sess.Append(session.Event{Type: kind, Data: data}); err != nil {
		w.t.Fatalf("追加 %s 失败：%v", kind, err)
	}
}

// plantContext 往收件箱最前面塞一条本层署名的陈货。
//
// [installer.syncInbox] 自己一次最多留下一条，所以「收件箱里排着好几条」这个局面
// 只能由外面摆出来。它在真运行时里来自上一次装配留下的残留：收件箱跟着会话走，
// 而这一层是装上去、摘下来、再装上去的。
func (w *world) plantContext(id llm.MessageID, text string) llm.Message {
	w.t.Helper()

	message := newContextMessage(w.t, id, text)
	w.live.Prepend(message, agent.NextStep)
	return message
}

// newContextMessage 手工造一条本层署名的上下文。
//
// 身份自己给，因为几条用例要按身份认出「留下的是哪一条」，而
// [llm.NewUserMessage] 自己发号。
func newContextMessage(t *testing.T, id llm.MessageID, text string) llm.Message {
	t.Helper()

	source, err := Source{}.MessageSource()
	if err != nil {
		t.Fatalf("造来源失败：%v", err)
	}
	return llm.Message{
		ID:      id,
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  source,
	}
}

// userSaid 造一条用户消息，用来占住里层那批消息里的位置。
func userSaid(id llm.MessageID, text string) llm.Message {
	return llm.Message{
		ID: id, Role: llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}
}

// ---- 装配那一步 ----

func Test装配缺了任何一个协作者都装不上(t *testing.T) {
	t.Parallel()
	full := func() Deps {
		return Deps{
			Agents:   &captureAgents{},
			Sessions: &captureSessions{agents: &captureAgents{}},
			Tools:    mustRuntime(t),
			FS:       newFakeFS(),
			AgentOf:  func(*scope.Key) (agent.Agent, error) { return nil, nil },
		}
	}
	cases := map[string]func(*Deps){
		"没有 agent 注册表":  func(d *Deps) { d.Agents = nil },
		"没有会话广播":        func(d *Deps) { d.Sessions = nil },
		"没有工具广播":        func(d *Deps) { d.Tools = nil },
		"没有文件系统":        func(d *Deps) { d.FS = nil },
		"没有查回 agent 的路": func(d *Deps) { d.AgentOf = nil },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps := full()
			breakIt(&deps)
			if _, err := Install(t.Context(), scope.NewRoot(), Config{}, deps); err == nil {
				t.Fatalf("%s 该装不上", name)
			}
		})
	}
	if _, err := Install(t.Context(), nil, Config{}, full()); err == nil {
		t.Fatal("没给作用域该装不上")
	}
}

func Test装到一半失败要把已经挂上的撤干净(t *testing.T) {
	t.Parallel()
	// 装不成却留着半套观察者，等于一层没人认领的代码在改别人的收件箱。
	planted := errors.New("挂不上")
	agents := &captureAgents{preStepFail: planted}
	sessions := &captureSessions{agents: agents}
	_, err := Install(t.Context(), scope.NewRoot(), Config{}, Deps{
		Agents: agents, Sessions: sessions, Tools: mustRuntime(t), FS: newFakeFS(),
		AgentOf: func(*scope.Key) (agent.Agent, error) { return nil, nil },
	})
	if !errors.Is(err, planted) {
		t.Fatalf("底层原因该留在链上：%v", err)
	}
	if !strings.Contains(err.Error(), "前置步骤") {
		t.Fatalf("该说清是哪一条挂不上：%v", err)
	}
	if len(agents.removed) != 1 || agents.removed[0] != "会话事件" {
		t.Fatalf("该把先挂上的那条撤掉，实际撤了 %v", agents.removed)
	}
}

func Test摘下来按反序撤(t *testing.T) {
	t.Parallel()
	// 反序是有意的：先掐掉最外层的入口，再往里撤。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	if err := w.remove(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	// 工具结果那一条挂在**真**的工具运行时上，它交回的是自己那个撤销函数，
	// 记不到这本账里；它夹在「处置」和「前置步骤」之间。
	want := []string{"处置", "前置步骤", "会话事件"}
	if len(w.agents.removed) != len(want) {
		t.Fatalf("该撤 %d 条，实际 %v", len(want), w.agents.removed)
	}
	for i := range want {
		if w.agents.removed[i] != want[i] {
			t.Fatalf("撤的次序不对：%v", w.agents.removed)
		}
	}
}

// mustRuntime 造一台工具运行时，造不出来当场失败。
func mustRuntime(t *testing.T) *tools.Runtime {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	return runtime
}

// quietLogger 是一个什么都不写的日志器。
func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// ---- 前置步骤那条规则 ----

func Test前置步骤把里层的错原样交出去(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	planted := errors.New("里层炸了")
	_, err := w.agents.preStep(t.Context(), agent.PreStep{Agent: w.live, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.PreStepDecision{}, planted
		})
	if !errors.Is(err, planted) {
		t.Fatalf("里层的错该原样交出去，实际 %v", err)
	}
}

func Test没有agent的提议原样放过(t *testing.T) {
	t.Parallel()
	// 运行时里出不来这种提议，但这条规则挂在一个公开的瀑布上，别人喂什么进来
	// 不归本包管——不能因此空指针。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	decision, err := w.agents.preStep(t.Context(), agent.PreStep{Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if err != nil || len(decision.Messages) != 0 {
		t.Fatalf("没有 agent 的提议该原样放过：%#v，err=%v", decision, err)
	}
}

func Test预算关掉时一个字都不发(t *testing.T) {
	t.Parallel()
	// MaxBytes 小于等于零就是「这套部署关掉了这一层」，不是「预算很小」。
	w := newWorld(t, Config{MaxBytes: 0})
	decision := w.run(2, userSaid("u", "喂"))
	if len(decision.Messages) != 1 {
		t.Fatalf("关掉之后不该补东西，实际 %d 条", len(decision.Messages))
	}
	if len(w.live.pending()) != 0 {
		t.Fatal("关掉之后收件箱里也不该有东西")
	}
}

func Test第一步空着时上下文排进收件箱而不是塞进这一步(t *testing.T) {
	t.Parallel()
	// 一个空的第一步占的是一个不发请求的回合，塞进去会让它变成一次独立的模型调用。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	decision := w.run(1)
	if len(decision.Messages) != 0 {
		t.Fatalf("空的第一步不该被塞东西，实际 %d 条", len(decision.Messages))
	}
	pending := ours(w.live.pending())
	if len(pending) != 1 {
		t.Fatalf("该排进收件箱，实际 %d 条", len(pending))
	}
	requireContains(t, textOf(pending[0]), "根上的规矩")
}

func Test上下文折在认领走的那批之后(t *testing.T) {
	t.Parallel()
	// 直接的提示排在它前面，驱动补的运行期上下文排在它后面。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	claimed := userSaid("u", "用户说的话")
	runtimeContext := userSaid("r", "驱动补的")

	decision, err := w.agents.preStep(t.Context(), agent.PreStep{
		Agent: w.live, Messages: []llm.Message{claimed}, Turn: 1, Step: 1,
	}, func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep([]llm.Message{claimed, runtimeContext}), nil
	})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(decision.Messages) != 3 {
		t.Fatalf("该是原来两条加一条上下文，实际 %d 条", len(decision.Messages))
	}
	if decision.Messages[0].ID != "u" || decision.Messages[2].ID != "r" {
		t.Fatalf("上下文该折在认领走的那条之后：%v %v %v",
			decision.Messages[0].ID, decision.Messages[1].ID, decision.Messages[2].ID)
	}
	if _, mine := ParseSource(decision.Messages[1].Source); !mine {
		t.Fatalf("中间那条该是本层署名的：%#v", decision.Messages[1].Source)
	}
}

func Test被拒的步骤不塞东西但收件箱照样对齐(t *testing.T) {
	t.Parallel()
	// 拒了就没有「这一步」可言，可这一层算出来的话仍然要有个去处——
	// 丢掉它等于让下一步凭空少一次指令变更。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	decision, err := w.agents.preStep(t.Context(), agent.PreStep{Agent: w.live, Turn: 1, Step: 2},
		func(context.Context) (agent.PreStepDecision, error) { return agent.RejectStep(), nil })
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if decision.Enter || len(decision.Messages) != 0 {
		t.Fatalf("被拒的决定该原样交回去：%#v", decision)
	}
	if len(ours(w.live.pending())) != 1 {
		t.Fatalf("该排进收件箱，实际 %d 条", len(ours(w.live.pending())))
	}
}

func Test已经发过的基线不再发第二遍(t *testing.T) {
	t.Parallel()
	// 一条内容和来路都一样的上下文重复进对话，模型会把它读成
	// 「这件事又发生了一次」。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	first := w.run(2, userSaid("u", "喂"))
	if len(first.Messages) != 2 {
		t.Fatalf("头一步该补出基线，实际 %d 条", len(first.Messages))
	}
	w.commit(first.Messages[1:])

	second := w.run(3, userSaid("u2", "再说一句"))
	if len(second.Messages) != 1 {
		t.Fatalf("基线已经在对话里了就不该再发，实际补了 %d 条", len(second.Messages)-1)
	}
	if len(w.live.pending()) != 0 {
		t.Fatal("也不该悄悄排进收件箱")
	}
}

func Test收件箱里排着同一件事时留住它自己的身份(t *testing.T) {
	t.Parallel()
	// 换一个身份等于让已经认过它的地方再认一次。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.run(1)
	queued := ours(w.live.pending())
	if len(queued) != 1 {
		t.Fatalf("该先排上一条，实际 %d 条", len(queued))
	}

	w.run(1)
	again := ours(w.live.pending())
	if len(again) != 1 {
		t.Fatalf("重算一次不该变成两条，实际 %d 条", len(again))
	}
	if again[0].ID != queued[0].ID {
		t.Fatalf("该留住原来那个身份：%v → %v", queued[0].ID, again[0].ID)
	}
}

// Test把收件箱对齐到算出来那条上 直接调 [installer.syncInbox]。
//
// 这几种局面在一次正常的 [installer.compose] 里凑不齐：没有触碰而收件箱里已经排着
// 东西时，compose 会直接复用排在头一条的那条（见它那句「这一次没有任何新事实」），
// 于是「排着好几条、而算出来的话和它们都不是一回事」永远走不到。可这段代码防的是
// 上一次装配留下的残留——收件箱跟着会话走，而这一层是装上去、摘下来、再装上去的，
// 所以那个局面在运行时里是真会出现的。
func Test把收件箱对齐到算出来那条上(t *testing.T) {
	t.Parallel()

	t.Run("排着的全不对就原地换掉头一条", func(t *testing.T) {
		t.Parallel()
		// 原地换而不是「删了再插」：位置决定模型读到它的先后。
		w := newWorld(t, Config{MaxBytes: 1 << 20})
		w.plantContext("陈货一", "上一轮留下的话")
		w.plantContext("陈货二", "更早留下的话")
		desired := newContextMessage(t, "新的", "这一次算出来的话")

		w.install.syncInbox(w.live, nil, desired, true)

		left := ours(w.live.pending())
		if len(left) != 1 || left[0].ID != "新的" {
			t.Fatalf("该只剩新算出来那一条：%v", left)
		}
	})

	t.Run("排着的里有说这件事的那条就留住它的身份", func(t *testing.T) {
		t.Parallel()
		// 换一个身份等于让已经认过它的地方再认一次。
		w := newWorld(t, Config{MaxBytes: 1 << 20})
		w.plantContext("陈货", "上一轮留下的话")
		w.plantContext("说的就是这件事", "这一次算出来的话")
		desired := newContextMessage(t, "新的", "这一次算出来的话")

		w.install.syncInbox(w.live, nil, desired, true)

		left := ours(w.live.pending())
		if len(left) != 1 || left[0].ID != "说的就是这件事" {
			t.Fatalf("该留住排着那条自己的身份：%v", left)
		}
	})

	t.Run("认领走的那批里已经有了就一条都不插", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, Config{MaxBytes: 1 << 20})
		w.plantContext("陈货", "上一轮留下的话")
		desired := newContextMessage(t, "新的", "这一次算出来的话")
		claimed := newContextMessage(t, "这一步认领走的", "这一次算出来的话")

		w.install.syncInbox(w.live, []llm.Message{claimed}, desired, true)

		if left := ours(w.live.pending()); len(left) != 0 {
			t.Fatalf("已经发出去过了就该把排着的清掉：%v", left)
		}
	})

	t.Run("会话表面上已经有了就一条都不插", func(t *testing.T) {
		t.Parallel()
		// 一条内容和来路都一样的上下文重复进对话，模型会把它读成「这件事又发生了一次」。
		w := newWorld(t, Config{MaxBytes: 1 << 20})
		w.commit([]llm.Message{newContextMessage(t, "早就发过了", "这一次算出来的话")})
		w.plantContext("陈货", "上一轮留下的话")
		desired := newContextMessage(t, "新的", "这一次算出来的话")

		w.install.syncInbox(w.live, nil, desired, true)

		if left := ours(w.live.pending()); len(left) != 0 {
			t.Fatalf("表面上已经有了就该把排着的清掉：%v", left)
		}
	})

	t.Run("收件箱空着就插到最前面", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, Config{MaxBytes: 1 << 20})
		desired := newContextMessage(t, "新的", "这一次算出来的话")

		w.install.syncInbox(w.live, nil, desired, true)

		left := ours(w.live.pending())
		if len(left) != 1 || left[0].ID != "新的" {
			t.Fatalf("该插进去：%v", left)
		}
	})
}

func Test算不出话来时收件箱里排着的都清掉(t *testing.T) {
	t.Parallel()
	// 这一层没话要说时，排着的那些也不再算数：它们说的是一个已经过期的口径。
	w := newWorld(t, Config{MaxBytes: 0})
	w.plantContext("陈货一", "上一轮留下的话")
	w.plantContext("陈货二", "更早留下的话")

	w.run(1)
	if left := ours(w.live.pending()); len(left) != 0 {
		t.Fatalf("该全清掉，实际还剩 %d 条", len(left))
	}
}

func Test认领走的那批里已经带着基线就不再发一份(t *testing.T) {
	t.Parallel()
	// 基线可能还没落进会话表面，就已经在这一步认领走的那批消息里了——
	// 只看表面的话会当场再发一份。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	first := w.run(2, userSaid("u", "喂"))
	if len(first.Messages) != 2 {
		t.Fatalf("头一步该补出基线，实际 %d 条", len(first.Messages))
	}

	second := w.run(3, userSaid("u2", "再说一句"), first.Messages[1])
	if len(second.Messages) != 2 {
		t.Fatalf("认领走的那批里已经有基线了就不该再发，实际 %d 条", len(second.Messages))
	}
}

func Test基线口径变了要明说旧的那些没了(t *testing.T) {
	t.Parallel()
	// 换掉一份口径不同的旧基线时，旧基线上那些新基线不再覆盖的作用域要明确告诉
	// 模型「没了」——不说的话它会一直以为那些指令还算数。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	// 工作目录自己变成了项目根：祖先链缩到只剩它自己，上一份基线里那条根上的
	// 规矩不再被覆盖，而作用域是相对根算的，所以它连名字都换了。
	w.fsys.addFile(fakeCwd+"/.git", "gitdir")

	decision := w.run(3, userSaid("u2", "再说一句"))
	if len(decision.Messages) != 2 {
		t.Fatalf("口径变了该重发一份基线，实际补了 %d 条", len(decision.Messages)-1)
	}
	source, mine := ParseSource(decision.Messages[1].Source)
	if !mine || !source.Baseline {
		t.Fatalf("该是一份新基线：%#v", decision.Messages[1].Source)
	}
	removed := make([]string, 0, len(source.Changes))
	for _, change := range source.Changes {
		if change.Action == ActionRemove {
			removed = append(removed, change.Scope)
		}
	}
	if len(removed) == 0 {
		t.Fatalf("旧基线上不再覆盖的作用域该明说没了：%#v", source.Changes)
	}

	// 再换回去一次。这一次被换掉的那份基线自己就带着上面那条 remove，而一条
	// remove 说的是「这个作用域已经没了」——把它再翻译成一条 remove 是句废话。
	w.commit(decision.Messages[1:])
	w.fsys.remove(fakeCwd + "/.git")

	third := w.run(4, userSaid("u3", "第三句"))
	if len(third.Messages) != 2 {
		t.Fatalf("口径又变了该再发一份基线，实际补了 %d 条", len(third.Messages)-1)
	}
	back, mine := ParseSource(third.Messages[1].Source)
	if !mine || !back.Baseline {
		t.Fatalf("该是一份新基线：%#v", third.Messages[1].Source)
	}
	for _, change := range back.Changes {
		if change.Action == ActionRemove && slices.Contains(removed, change.Scope) {
			t.Fatalf("已经说过没了的作用域不该再说一遍：%q", change.Scope)
		}
	}
}

func Test表面上认不出来的那几条被跳过(t *testing.T) {
	t.Parallel()
	// 会话表面上不是只有用户消息，助手消息也在上面；而且日志是会跨版本读回来的，
	// 一条解不开的负载不该让整一层停摆——它只意味着这一条上面没有本层的署名。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.append(session.EventAssistantMessage, session.AssistantMessageData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID: "a", Role: llm.RoleAssistant,
			Content: llm.Content{llm.TextBlock{Text: "模型说的话"}},
		},
	}, session.AppendOp{})
	w.append(session.EventUserMessage, map[string]any{"message": "这不是一条消息"}, session.AppendOp{})

	decision := w.run(2, userSaid("u", "喂"))
	if len(decision.Messages) != 2 {
		t.Fatalf("认不出来的该被跳过、基线照发，实际 %d 条", len(decision.Messages))
	}
}

func Test这一步真跑了就把排着的那批了结掉(t *testing.T) {
	t.Parallel()
	// 要么它作为这条上下文跟着进去，要么它说的话已经被这一批盖住了。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.run(1)
	if len(ours(w.live.pending())) != 1 {
		t.Fatal("该先排上一条")
	}
	decision := w.run(2, userSaid("u", "喂"))
	if len(decision.Messages) != 2 {
		t.Fatalf("该带着上下文进去，实际 %d 条", len(decision.Messages))
	}
	if len(w.live.pending()) != 0 {
		t.Fatalf("排着的那批该了结了，实际还剩 %d 条", len(w.live.pending()))
	}
}

func Test里层已经带着这条上下文就不重复插(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	first := w.run(2, userSaid("u", "喂"))
	injected := first.Messages[1]

	decision, err := w.agents.preStep(t.Context(), agent.PreStep{
		Agent: w.live, Messages: nil, Turn: 1, Step: 3,
	}, func(context.Context) (agent.PreStepDecision, error) {
		return agent.EnterStep([]llm.Message{injected}), nil
	})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(decision.Messages) != 1 {
		t.Fatalf("里层已经带着了就不该再插，实际 %d 条", len(decision.Messages))
	}
}

func Test取消之后这一步被否掉(t *testing.T) {
	t.Parallel()
	// 补不上上下文时宁可否掉这一步：一条半成品的指令上下文比没有更坏。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	decision, err := w.agents.preStep(ctx, agent.PreStep{Agent: w.live, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if err == nil {
		t.Fatal("取消之后该报出取消")
	}
	if decision.Enter {
		t.Fatal("取消之后不该放这一步进去")
	}
}

func Test装基线的中途被取消时这一步被否掉(t *testing.T) {
	t.Parallel()
	// 取消可以落在一次流式读的**中途**，那一刻 [compose] 开头那次 ctx 检查早就过去了。
	// 这条路必须一直报到准入上：一份读了一半的基线看上去是完整的。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	ctx, cancel := context.WithCancel(t.Context())
	w.fsys.chunkSize = 4
	w.fsys.cancelAfterFirstChunk(fakeRoot+"/AGENTS.md", cancel)

	decision, err := w.agents.preStep(ctx, agent.PreStep{Agent: w.live, Turn: 1, Step: 1},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("中途的取消该一直报上来：%v", err)
	}
	if decision.Enter {
		t.Fatal("算不出上下文时不该放这一步进去")
	}
}

func Test文件系统读不动时降级而不是报错(t *testing.T) {
	t.Parallel()
	// 「这个文件读不了」和「这次调用被取消了」是两件事：前者是降级——
	// 让 agent 因为一份读不出来的 AGENTS.md 整个停摆，代价和收益完全不成比例。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.fsys.failStream(fakeRoot+"/AGENTS.md", errFake)

	decision := w.run(2, userSaid("u", "喂"))
	if !decision.Enter {
		t.Fatal("读不动一份指令不该把这一步否掉")
	}
	if len(decision.Messages) != 1 {
		t.Fatalf("没有内容可发就什么都不补，实际 %d 条", len(decision.Messages))
	}
}

// ---- 工具触碰那条路 ----

func Test一次读文件走完之后收件箱里就有指令变更(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	// 先把基线发出去并落进对话，这样这次触碰算出来的才是一条纯增量。
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())
	pending := ours(w.waitForInbox(1))
	requireContains(t, textOf(pending[0]), "这个目录自己的规矩")
}

func Test步骤开着的时候先攒着不投影(t *testing.T) {
	t.Parallel()
	// 一个复合工具的写随时可能被外层回滚掉，模型不该看见一次被撤销的写。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.openStep(1, 3)
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())

	// 攒着的东西没有一个「什么时候一定不出现」的时刻，只能给它一段时间。
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("步骤开着的时候不该投影")
	}
	w.endStep(1, 3)
	w.waitForInbox(1)
}

func Test一批触碰放出来时一个接一个投影(t *testing.T) {
	t.Parallel()
	// 后一次投影要等前一次落地：两次同时去动收件箱的话，后一次算的是一份
	// 还没更新完的排队情况，会把前一次刚放进去的那条当成陈货撤掉。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.openStep(1, 3)
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())
	w.call("read", touchArgs())
	w.endStep(1, 3)

	pending := ours(w.waitForInbox(1))
	requireContains(t, textOf(pending[0]), "这个目录自己的规矩")
}

func Test前置步骤等不到投影落地就把这一步否掉(t *testing.T) {
	t.Parallel()
	// 投影跑在自己的协程上。不等它落地就去算这一步，算出来的是一份还没更新完的
	// 收件箱，而它刚放进去的那条会被当成陈货撤掉。等不下去时宁可否掉这一步。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	gate := make(chan struct{})
	w.fsys.gateStream(fakeCwd+"/AGENTS.md", gate)
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())
	w.waitForTail()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	decision, err := w.agents.preStep(ctx, agent.PreStep{Agent: w.live, Turn: 1, Step: 3},
		func(context.Context) (agent.PreStepDecision, error) { return agent.EnterStep(nil), nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("等不到投影落地该报出取消：%v", err)
	}
	if decision.Enter {
		t.Fatal("等不到投影落地就不该放这一步进去")
	}

	close(gate)
	w.waitForInbox(1)
}

func Test摘下来时还排在后面的投影当场散掉(t *testing.T) {
	t.Parallel()
	// 排在后面那个还没开跑，整层就被摘了。它不该再去动一个已经不归本层管的收件箱。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	gate := make(chan struct{})
	w.fsys.gateStream(fakeCwd+"/AGENTS.md", gate)
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")

	// 步骤开着的时候攒两次触碰，关掉时它们一起放出来，于是后一个排在前一个后面。
	w.openStep(1, 3)
	w.call("read", touchArgs())
	w.call("read", `{"file_path":"`+fakeRoot+`/AGENTS.md"}`)
	w.endStep(1, 3)
	w.waitForTail()

	if err := w.remove(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	close(gate)

	time.Sleep(50 * time.Millisecond)
	if left := ours(w.live.pending()); len(left) != 0 {
		t.Fatalf("摘下来之后不该再往收件箱里放东西，实际 %d 条", len(left))
	}
}

func Test整层摘下来时在飞的投影不当故障报(t *testing.T) {
	t.Parallel()
	// 收摊时那条投影一定失败——它那条命就是被收摊掐掉的。把它记成警告的话，
	// 每一次正常的关停都会在日志里留下一行看着像故障的东西。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	// 让这次投影读到一半的时候整层被摘掉。
	w.fsys.chunkSize = 4
	w.fsys.cancelAfterFirstChunk(fakeCwd+"/AGENTS.md", w.install.stop)
	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())

	time.Sleep(50 * time.Millisecond)
	if strings.Contains(w.logs.String(), "刷新工作区指令失败") {
		t.Fatalf("收摊不是故障，不该记警告：%s", w.logs.String())
	}
}

func Test头一次看见一段跑到一半的会话时靠重放算出步骤开着(t *testing.T) {
	t.Parallel()
	// 一段续跑的会话在这一层装上去之前就已经有步骤边界了，那些事件观察者没见过。
	// 头一次要用它的时候只能把整段日志重放一遍。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])
	// 把这段会话的状态扔掉，下一次触碰就得从一张白纸开始。
	w.agents.disposed(w.live)

	w.appendQuiet(session.EventStepStart, session.StepStartData{Turn: 1, Step: 3})
	w.appendQuiet(session.EventStepEnd, session.StepEndData{Turn: 1, Step: 3})
	w.appendQuiet(session.EventStepStart, session.StepStartData{Turn: 1, Step: 4})

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", touchArgs())
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("重放该看出最后那个步骤还开着")
	}
	w.endStep(1, 4)
	w.waitForInbox(1)
}

func Test嵌套调用的触碰要等根上那次收摊(t *testing.T) {
	t.Parallel()
	// 嵌套的 read 先收摊，那时外层还在跑、随时可能失败。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	if result := w.call("compound", touchArgs()); result.IsError {
		t.Fatalf("这次复合调用不该失败：%+v", result.Error)
	}
	// 外层 compound 自己不在 fileTouchToolNames 里，所以这一条只可能来自
	// 被并上来的那次嵌套 read。
	pending := ours(w.waitForInbox(1))
	requireContains(t, textOf(pending[0]), "这个目录自己的规矩")
}

func Test失败的调用不算一次触碰(t *testing.T) {
	t.Parallel()
	// 一次失败的写什么都没改，把它当触碰会让模型收到一条凭空的指令变更。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	encoded, _ := json.Marshal(struct {
		FilePath string `json:"file_path"`
		Fail     bool   `json:"fail"`
	}{FilePath: fakeRoot + "/AGENTS.md", Fail: true})
	if result := w.call("write", string(encoded)); !result.IsError {
		t.Fatal("这次调用该失败")
	}
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("失败的调用不该投影")
	}
}

func Test不碰文件的工具不算一次触碰(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("grep", touchArgs())
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("grep 不在那三个名字里，不该投影")
	}
}

func Test没有file_path的调用不算一次触碰(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.commit(w.run(2, userSaid("u", "喂")).Messages[1:])

	w.fsys.addFile(fakeCwd+"/AGENTS.md", "这个目录自己的规矩")
	w.call("read", `{"file_path":"   "}`)
	w.call("read", `{"file_path":42}`)
	w.call("read", `{}`)
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("取不出路径的调用不该投影")
	}
}

func Test查不回agent时丢掉这次触碰并说一声(t *testing.T) {
	t.Parallel()
	// 一次读文件的副作用补不上，代价是模型少看见一次指令变更；
	// 让整条工具结果路径出错，代价大得多。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	stranger := scope.NewKey("别人")
	w.runtime.Execute(t.Context(), tools.ExecutionInput{
		CallID: "c", Name: "read", Arguments: json.RawMessage(touchArgs()), Agent: stranger,
	})
	time.Sleep(50 * time.Millisecond)
	if len(ours(w.live.pending())) != 0 {
		t.Fatal("查不回 agent 就不该动任何人的收件箱")
	}
	if !strings.Contains(w.logs.String(), "丢掉这次触碰") {
		t.Fatalf("该记一行警告，实际写出来的是：%s", w.logs.String())
	}
}

// ---- 处置与收摊 ----

func Test处置之后这段会话的状态就没了(t *testing.T) {
	t.Parallel()
	// Go 没有弱引用表，没人删的话一段跑完的会话会一直占着那一行。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.run(1)
	if _, present := w.install.lookupState("s"); !present {
		t.Fatal("跑过一次该留下状态")
	}
	w.agents.disposed(w.live)
	if _, present := w.install.lookupState("s"); present {
		t.Fatal("处置之后该把状态扔掉")
	}
}

func Test摘下来之后攒着的东西都清了(t *testing.T) {
	t.Parallel()
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	w.run(1)
	if err := w.remove(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	if _, present := w.install.lookupState("s"); present {
		t.Fatal("摘下来之后不该还留着会话状态")
	}
	if w.install.lifetime.Err() == nil {
		t.Fatal("摘下来之后投影那条命该被掐掉")
	}
}

func Test会话事件只认已经在场的那几段(t *testing.T) {
	t.Parallel()
	// 这条观察者是全局装的，它看得见这个进程里每一段会话的每一条事件。
	// 见一段就建一行的话，一段从来没碰过指令的会话也会永远留在表里。
	w := newWorld(t, Config{MaxBytes: 1 << 20})
	other := newStubAgent(t, "别人家的会话")
	w.sessions.observer(other.sess, session.Event{Type: session.EventStepStart})
	if _, present := w.install.lookupState("别人家的会话"); present {
		t.Fatal("不该为一段没碰过指令的会话建状态")
	}
}

func Test装配这一层能整个装上去(t *testing.T) {
	t.Parallel()
	// 这一条走的是公开的 [Install]，把四条观察者、撤销闭包和「摘下来先掐投影」
	// 那个次序一起压住——别的用例为了看内部账走的是里面那两步。
	agents := &captureAgents{}
	sessions := &captureSessions{agents: agents}
	live := newStubAgent(t, "s")
	fsys := newFakeFS()
	fsys.addDir(fakeRoot).addDir(fakeCwd).addFile(fakeRoot+"/.git", "gitdir")
	fsys.addFile(fakeRoot+"/AGENTS.md", "根上的规矩")

	remove, err := Install(t.Context(), scope.NewRoot(), Config{MaxBytes: 1 << 20}, Deps{
		Agents: agents, Sessions: sessions, Tools: mustRuntime(t), FS: fsys,
		AgentOf: func(*scope.Key) (agent.Agent, error) { return live, nil },
	})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if agents.preStep == nil || agents.disposed == nil || sessions.observer == nil {
		t.Fatal("四条观察者该都挂上了")
	}
	decision, err := agents.preStep(t.Context(),
		agent.PreStep{Agent: live, Messages: []llm.Message{userSaid("u", "喂")}, Turn: 1, Step: 2},
		func(context.Context) (agent.PreStepDecision, error) {
			return agent.EnterStep([]llm.Message{userSaid("u", "喂")}), nil
		})
	if err != nil || len(decision.Messages) != 2 {
		t.Fatalf("默认装配该照样补出一条上下文：%d 条，err=%v", len(decision.Messages), err)
	}
	if err := remove(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
}
