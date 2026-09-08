// 本文件的作用：本包用例共用的那套装配——一台内存介质、一个域设施、一份开好的服务，
// 以及子 Agent 能力和活 agent 查询面这两个假件。

package agentteam

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// leadID 是每个用例里那个队长会话的身份。
const leadID sessionlog.SessionID = "session-lead"

// quiet 造一台什么都不往外写的日志器：好几条路径故意去触发警告。
func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- 假件 ----

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent，外加一份收件记录。
type stubAgent struct {
	id     sessionlog.SessionID
	status agent.Status

	mutex    sync.Mutex
	received []sentMessage
}

// sentMessage 是 [stubAgent.Send] 收到的那一条。
type sentMessage struct {
	message llm.Message
	target  agent.InboxTarget
	wakeup  bool
}

func (a *stubAgent) ID() sessionlog.SessionID      { return a.id }
func (a *stubAgent) Options() agent.Options        { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session { return nil }
func (a *stubAgent) Inbox() *agent.Inbox           { return nil }
func (a *stubAgent) Scope() *scope.Scope           { return nil }

func (a *stubAgent) Status() agent.Status {
	if a.status == "" {
		return agent.StatusIdle
	}
	return a.status
}

func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Followup(llm.Message)                                      {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}
func (a *stubAgent) Remove(llm.MessageID)                                      {}
func (a *stubAgent) Replace(llm.MessageID, llm.Message)                        {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *stubAgent) Send(message llm.Message, target agent.InboxTarget, wakeup bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.received = append(a.received, sentMessage{message: message, target: target, wakeup: wakeup})
}

// inbox 交出这个假 agent 到此刻收到的那几条。
func (a *stubAgent) inbox() []sentMessage {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]sentMessage(nil), a.received...)
}

// stubAgents 是那份只回答「本副本上此刻登记着谁」的假查询面。
type stubAgents struct {
	mutex sync.Mutex
	live  map[sessionlog.SessionID]*stubAgent
}

func newStubAgents(live ...*stubAgent) *stubAgents {
	registry := &stubAgents{live: make(map[sessionlog.SessionID]*stubAgent, len(live))}
	for _, one := range live {
		registry.live[one.id] = one
	}
	return registry
}

func (r *stubAgents) Agent(id sessionlog.SessionID) (agent.Agent, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	live, ok := r.live[id]
	if !ok {
		return nil, false
	}
	return live, true
}

// add 让一个会话在本副本上活起来。
func (r *stubAgents) add(one *stubAgent) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.live[one.id] = one
}

// drop 让一个会话从本副本上消失，用来演「它在另一台机器上」。
func (r *stubAgents) drop(id sessionlog.SessionID) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	delete(r.live, id)
}

// followupCall 是 [stubSubagents.Followup] 收到的那一次。
type followupCall struct {
	child   sessionlog.SessionID
	content llm.Content
	source  llm.MessageSource
}

// stubSubagents 是那套假的子 Agent 能力：记下每次调用，并让用例指定它成不成。
type stubSubagents struct {
	// registry 是起成功之后要把孩子塞进去的那份查询面；为 nil 就不塞。
	registry *stubAgents
	// startErr 不为 nil 时起队友一律失败。
	startErr error
	// followupErr 不为 nil 时排回合一律失败。
	followupErr error

	mutex      sync.Mutex
	started    []subagent.ContinuableStartSpec
	followups  []followupCall
	interrupts []sessionlog.SessionID
}

func (s *stubSubagents) StartContinuable(
	_ context.Context,
	spec subagent.ContinuableStartSpec,
) (subagent.ContinuableStart, error) {
	s.mutex.Lock()
	s.started = append(s.started, spec)
	s.mutex.Unlock()
	if s.startErr != nil {
		return subagent.ContinuableStart{}, s.startErr
	}
	if s.registry != nil {
		s.registry.add(&stubAgent{id: spec.ChildID})
	}
	return subagent.ContinuableStart{ChildID: spec.ChildID, MessageID: "message-1"}, nil
}

func (s *stubSubagents) Followup(
	_ context.Context,
	_ agent.Agent,
	childID sessionlog.SessionID,
	content llm.Content,
	options subagent.FollowupOptions,
) (llm.MessageID, error) {
	s.mutex.Lock()
	s.followups = append(s.followups, followupCall{child: childID, content: content, source: options.Source})
	s.mutex.Unlock()
	if s.followupErr != nil {
		return "", s.followupErr
	}
	return "message-followup", nil
}

func (s *stubSubagents) Interrupt(target sessionlog.SessionID, _ subagent.InterruptAuthority) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.interrupts = append(s.interrupts, target)
	return nil
}

// calls 交出到此刻记下的三份调用记录。
func (s *stubSubagents) calls() ([]subagent.ContinuableStartSpec, []followupCall, []sessionlog.SessionID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]subagent.ContinuableStartSpec(nil), s.started...),
		append([]followupCall(nil), s.followups...),
		append([]sessionlog.SessionID(nil), s.interrupts...)
}

// ---- 装配 ----

// fixture 是一个用例手上那一整套东西。
type fixture struct {
	service   *Service
	agents    *stubAgents
	subagents *stubSubagents
	lead      *stubAgent
	// now 是这套装配里的时钟，用例可以往前拨。
	now time.Time
}

// facility 建一份内存介质并在它上面开一个域设施，用完自己收干净。
func facility(t *testing.T) *domain.Facility {
	t.Helper()

	medium := storagetest.NewMemoryMedium()
	backend := storagetest.NewMemoryBackend(medium)
	hub := storage.New()
	if _, err := hub.Backend.Register("main", backend); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}
	built, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}
	t.Cleanup(func() {
		_ = built.CloseAll(context.Background())
		_ = backend.Close(context.Background())
	})
	return built
}

// boot 建一份内存介质、一个域设施、一份开好的服务，队长默认活在本副本上。
func boot(t *testing.T, tune ...func(*Config)) *fixture {
	t.Helper()

	facility := facility(t)

	lead := &stubAgent{id: leadID}
	agents := newStubAgents(lead)
	subagents := &stubSubagents{registry: agents}
	box := &fixture{agents: agents, subagents: subagents, lead: lead, now: time.Unix(1700000000, 0)}

	// 身份按调用次序发号，好让用例里的断言写死一个值就够。
	counter := 0
	config := Config{
		Domain:    facility,
		Subagents: subagents,
		Agents:    agents,
		Replica:   "replica-a",
		Logger:    quiet(),
		Now:       func() time.Time { return box.now },
		NewMessageID: func() string {
			counter++
			return strconv.Itoa(counter)
		},
	}
	for _, adjust := range tune {
		adjust(&config)
	}
	service, err := Open(t.Context(), config)
	if err != nil {
		t.Fatalf("打开团队服务不该失败：%v", err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	box.service = service
	return box
}

// team 是这套装配里那支团队的身份。
func (f *fixture) team() TeamID { return TeamID(leadID) }

// spawn 起一个队友，起不成直接让用例失败。
func (f *fixture) spawn(t *testing.T, name string) MemberView {
	t.Helper()

	view, err := f.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name:        name,
		Description: name + " 干的活",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	})
	if err != nil {
		t.Fatalf("起队友 %q 不该失败：%v", name, err)
	}
	return view
}

// leader 交出队长那一位。一支团队要起出第一个队友才成立，所以还没人的时候
// 先垫一个队友上去——只关心任务板的那些用例不必自己操心这件事。
func (f *fixture) leader(t *testing.T) Caller {
	t.Helper()

	if _, err := f.service.roster(t.Context(), f.team()); err != nil {
		f.spawn(t, "worker")
	}
	return f.caller(t, leadID)
}

// caller 解算一位发起人，解不出来直接让用例失败。
func (f *fixture) caller(t *testing.T, id sessionlog.SessionID) Caller {
	t.Helper()

	who, err := f.service.ResolveCaller(t.Context(), f.team(), id)
	if err != nil {
		t.Fatalf("解算发起人 %q 不该失败：%v", id, err)
	}
	return who
}

// createTask 建一条任务，建不成直接让用例失败。
func (f *fixture) createTask(t *testing.T, who Caller, request CreateTaskRequest) TaskView {
	t.Helper()

	if request.Subject == "" {
		request.Subject = "一条任务"
	}
	if request.Description == "" {
		request.Description = "把它做完"
	}
	view, err := f.service.CreateTask(t.Context(), who, request)
	if err != nil {
		t.Fatalf("建任务不该失败：%v", err)
	}
	return view
}

// update 改一条任务，改不成直接让用例失败。
func (f *fixture) update(t *testing.T, who Caller, request UpdateTaskRequest) TaskView {
	t.Helper()

	view, err := f.service.UpdateTask(t.Context(), who, request)
	if err != nil {
		t.Fatalf("改任务不该失败：%v", err)
	}
	return view
}

// message 从介质上读回一条消息。
func (f *fixture) message(t *testing.T, id MessageID) Message {
	t.Helper()

	stored, found, err := f.service.messages.Get(t.Context(), string(id))
	if err != nil {
		t.Fatalf("读消息不该失败：%v", err)
	}
	if !found {
		t.Fatalf("消息 %q 该在介质上", id)
	}
	return stored
}

// text 把一份内容里的文本块拼起来，供断言用。
func text(content llm.Content) string {
	joined := ""
	for _, block := range content {
		if textBlock, ok := block.(llm.TextBlock); ok {
			joined += textBlock.Text
		}
	}
	return joined
}
