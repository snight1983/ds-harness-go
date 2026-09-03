// 本文件的作用：这一包测试共用的装配——一条会记账的假对面、一台可编排的假附件仓库、
// 一个真的会停在 WhenIdle 上的 agent、一份把整条创建路走完的假造法，以及把它们和
// 一台真运行时连起来的夹具。

package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// ---- 工作区 ----
//
// 新增: 这里原先是一条路径和它折进执行世界之后的样子，两者由本包的一个纯函数相互换算，
// 于是整套用例只跑在同一个宇宙里。会话头现在记的是一个不透明的工作区标识（见
// [sessionlog.SessionHeader.WorkspaceID]），线上那条 cwd 则是**客户端那台机器**上的
// 写法，两者之间隔着一个装配方给的 [WorkspaceResolver]。所以下面这几样刻意不共用
// 任何字面量：共用的话，「桥那次换算接错了」这类故障在夹具里永远现不出形。
var (
	// clientCwd 是客户端在线上报的那条工作目录。
	clientCwd = "/客户端那台机器/项目"
	// otherCwd 是另一台客户端报的另一条，用来摆「不是同一个工作区」。
	otherCwd = "C:\\另一台机器\\别的项目"
	// testWorkspaceID 是 clientCwd 换出来的那个工作区标识：一个标识，不是路径。
	testWorkspaceID = sessionlog.WorkspaceID("ws-acp")
	// otherWorkspaceID 是 otherCwd 换出来的那个。
	otherWorkspaceID = sessionlog.WorkspaceID("ws-acp-别的")
	// workspaceDisplay 是 testWorkspaceID 拿给客户端看的那条路径。它和 clientCwd
	// 不是同一个串：`session/list` 报出去的 cwd 走的是反向换算，两者相等的话那一步
	// 做没做就分不出来了。
	workspaceDisplay = "/登记册里/项目"
	// otherDisplay 是 otherWorkspaceID 的展示路径。
	otherDisplay = "/登记册里/别的项目"
)

// fakeWorkspaces 是一小块假的工作区登记册。
//
// 两张表故意分开填：一条 cwd 换得出标识，不等于那个标识换得回展示路径——
// 「登记册里已经没有这个工作区了」正是这么摆出来的。
type fakeWorkspaces struct {
	byCwd   map[string]sessionlog.WorkspaceID
	display map[sessionlog.WorkspaceID]string
	// resolveFail 非 nil 时 WorkspaceOf 报这个错。
	resolveFail error
	// displayFail 非 nil 时 WorkspaceDisplay 报这个错。
	displayFail error
}

// newFakeWorkspaces 造一册装着上面那两个工作区的登记册。
func newFakeWorkspaces() *fakeWorkspaces {
	return &fakeWorkspaces{
		byCwd: map[string]sessionlog.WorkspaceID{
			clientCwd: testWorkspaceID,
			otherCwd:  otherWorkspaceID,
		},
		display: map[sessionlog.WorkspaceID]string{
			testWorkspaceID:  workspaceDisplay,
			otherWorkspaceID: otherDisplay,
		},
	}
}

func (w *fakeWorkspaces) WorkspaceOf(_ context.Context, cwd string) (sessionlog.WorkspaceID, bool, error) {
	if w.resolveFail != nil {
		return "", false, w.resolveFail
	}
	found, ok := w.byCwd[cwd]
	return found, ok, nil
}

func (w *fakeWorkspaces) WorkspaceDisplay(_ context.Context, id sessionlog.WorkspaceID) (string, bool, error) {
	if w.displayFail != nil {
		return "", false, w.displayFail
	}
	found, ok := w.display[id]
	return found, ok, nil
}

// quietLogger 是一个什么都不记的 logger：这一包好几条路**本来就**要记警告，
// 让它们打到测试输出里只会淹掉真正的失败。
func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// tickingClock 是一个走得可预测的时钟：每读一次加一毫秒。
func tickingClock() func() int64 {
	tick := int64(1000)
	return func() int64 { return atomic.AddInt64(&tick, 1) }
}

// ---- 事件脚手架 ----

// rawData 把一份负载排成字节，排不出去当场失败。
func rawData(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// logEvent 造一条会话事件。
func logEvent(t *testing.T, kind sessionlog.EventType, payload any) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{Type: kind, Data: rawData(t, payload)}
}

// assistantEvent 造一条带内容的 assistant/message 事件。
//
// 带上 AppendOp 是必须的：这种事件够格上表面，[sessionlog.SurfaceOpOf] 要求它一定
// 带着自己的表面操作。
func assistantEvent(t *testing.T, turn int, content llm.Content) sessionlog.Event {
	t.Helper()
	built := logEvent(t, sessionlog.EventAssistantMessage, sessionlog.AssistantMessageData{
		Turn:    turn,
		Message: llm.NewAssistantMessage(content, llm.Provenance{Provider: "p", Model: "m"}),
	})
	built.SurfaceOp = sessionlog.AppendOp{}
	return built
}

// textContent 造一段只有一块文本的内容。
func textContent(body string) llm.Content { return llm.Content{llm.TextBlock{Text: body}} }

// ---- 假对面 ----

// recordingPeer 把每一条发出去的更新记下来，并且可以被要求发不出去或者按剧本答复
// 一次许可请求。
type recordingPeer struct {
	mutex   sync.Mutex
	updates []wire.SessionNotification
	// updateFail 非 nil 时每一条更新都发不出去。
	updateFail error
	// asked 是历次许可请求。
	asked []wire.RequestPermissionRequest
	// permission 是要回的那份答复，permissionFail 非 nil 时改为报错。
	permission     wire.RequestPermissionResponse
	permissionFail error
}

func (p *recordingPeer) SessionUpdate(_ context.Context, params wire.SessionNotification) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.updateFail != nil {
		return p.updateFail
	}
	p.updates = append(p.updates, params)
	return nil
}

func (p *recordingPeer) RequestPermission(
	_ context.Context,
	params wire.RequestPermissionRequest,
) (wire.RequestPermissionResponse, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.asked = append(p.asked, params)
	if p.permissionFail != nil {
		return wire.RequestPermissionResponse{}, p.permissionFail
	}
	return p.permission, nil
}

// chunks 交出到目前为止发出去的每一条助手文本。
func (p *recordingPeer) chunks() []string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	var texts []string
	for _, update := range p.updates {
		chunk := update.Update.AgentMessageChunk
		if chunk == nil || chunk.Content.Text == nil {
			continue
		}
		texts = append(texts, chunk.Content.Text.Text)
	}
	return texts
}

// requests 交出历次许可请求。
func (p *recordingPeer) requests() []wire.RequestPermissionRequest {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]wire.RequestPermissionRequest(nil), p.asked...)
}

// ---- 假附件仓库 ----

// fakeStore 是一台可编排的附件仓库：它不真的解码栅格，只按剧本收或者拒。
type fakeStore struct {
	mutex sync.Mutex
	// mediaTypes 是这个部署收的媒体类型，nil 表示用那四种栅格的全集。
	mediaTypes []attachment.MediaType
	// saveFail 非 nil 时提交那一步以它失败。
	saveFail error
	// readFail 非 nil 时读回那一步以它失败。
	readFail error
	// saveHook 在每一次提交之前跑，用来摆出"写这一批的半路上世界变了"。
	saveHook func()
	// saved 记下真的写进去的每一张图。
	saved []attachment.ImageInput
}

func (s *fakeStore) ImageLimits() attachment.ImageLimits {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	types := s.mediaTypes
	if types == nil {
		types = imageMediaTypes
	}
	return attachment.ImageLimits{
		MaxImageBytes:        1 << 20,
		MaxImagesPerMessage:  8,
		MaxMessageImageBytes: 1 << 20,
		MaxImagePixels:       1 << 20,
		MaxImageDimension:    4096,
		MediaTypes:           types,
	}
}

func (s *fakeStore) ValidateImage(context.Context, attachment.ImageInput) error { return nil }

func (s *fakeStore) SaveImage(_ context.Context, input attachment.ImageInput) (attachment.ImageRef, error) {
	s.mutex.Lock()
	hook := s.saveHook
	s.mutex.Unlock()
	if hook != nil {
		hook()
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.saveFail != nil {
		return attachment.ImageRef{}, s.saveFail
	}
	s.saved = append(s.saved, input)
	return attachment.ImageRef{
		ID:        attachment.ID(string(rune('a' + len(s.saved) - 1))),
		MediaType: input.MediaType,
		Bytes:     len(input.Data),
		Width:     1,
		Height:    1,
	}, nil
}

func (s *fakeStore) ReadImage(_ context.Context, ref attachment.ImageRef) (attachment.StoredImage, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.readFail != nil {
		return attachment.StoredImage{}, s.readFail
	}
	return attachment.StoredImage{Ref: ref, Data: []byte{7, 7}}, nil
}

// ---- 假模型名册 ----

// fakeModels 按剧本回一份模型目录。
//
// 它满足整个 [ModelCatalog]：这一包好几条路要的是「翻得出提供方和模型」，而不只是
// 「问得出模态」。
type fakeModels struct {
	modalities []llm.ModelModality
	reasoning  *llm.ModelReasoningInfo
	providers  []llm.ProviderInfo
	models     map[string][]llm.ModelInfo
	fail       error
	listFail   error

	// observed 记下这台名册当下挂着的那些拓扑观察者，好让用例自己敲一次更新。
	observed *observerBox
}

// observerBox 是那几条观察者的共享落点：fakeModels 按值传，钩子得挂在同一处。
type observerBox struct {
	mutex     sync.Mutex
	observers []llm.AdaptersUpdatedObserver
}

// notify 敲一遍当下挂着的每一条观察者。
func (b *observerBox) notify() {
	b.mutex.Lock()
	observers := append([]llm.AdaptersUpdatedObserver(nil), b.observers...)
	b.mutex.Unlock()
	for _, observer := range observers {
		observer()
	}
}

func (m fakeModels) ResolveModelInfo(_ context.Context, provider, model string) (llm.ResolvedModelInfo, error) {
	if m.fail != nil {
		return llm.ResolvedModelInfo{}, m.fail
	}
	return llm.ResolvedModelInfo{
		ModelInfo: llm.ModelInfo{
			Provider:        provider,
			ID:              model,
			InputModalities: m.modalities,
		},
		Reasoning: m.reasoning,
	}, nil
}

func (m fakeModels) ListProviders() []llm.ProviderInfo { return m.providers }

func (m fakeModels) ListModels(_ context.Context, provider string) ([]llm.ModelInfo, error) {
	if m.listFail != nil {
		return nil, m.listFail
	}
	return m.models[provider], nil
}

func (m fakeModels) ResolveCallConfig(_ context.Context, config llm.CallConfig) (llm.CallConfig, error) {
	if m.fail != nil {
		return llm.CallConfig{}, m.fail
	}
	return config, nil
}

func (m fakeModels) OnAdaptersUpdated(
	_ context.Context,
	_ *scope.Scope,
	observer llm.AdaptersUpdatedObserver,
) (func(context.Context) error, error) {
	if m.observed == nil {
		return func(context.Context) error { return nil }, nil
	}
	m.observed.mutex.Lock()
	m.observed.observers = append(m.observed.observers, observer)
	m.observed.mutex.Unlock()
	return func(context.Context) error { return nil }, nil
}

// imageModels 是一台声明收图的名册。
func imageModels() fakeModels {
	return fakeModels{modalities: []llm.ModelModality{llm.ModalityImage}}
}

// catalogModels 是一台真翻得出东西的名册：一条提供方、两个模型。
func catalogModels() fakeModels {
	return fakeModels{
		providers: []llm.ProviderInfo{{ID: "acme", Name: "Acme"}},
		models: map[string][]llm.ModelInfo{"acme": {
			{Provider: "acme", ID: "fast", Name: "Fast", Description: "快的那个"},
			{Provider: "acme", ID: "slow", Name: "Slow"},
		}},
	}
}

// ---- 假 agent ----

// scriptedAgent 是一个**真的会停在 WhenIdle 上**的 agent：静不静下来由用例说了算。
//
// 当场返回的 WhenIdle 会让结算那条协程在提示词刚投进去的那一刻就去读结果，把「等这
// 一轮跑完」整条边测没。
type scriptedAgent struct {
	id      sessionlog.SessionID
	scope   *scope.Scope
	live    *coresession.Session
	options agent.Options

	mutex sync.Mutex
	// idle 关掉之后 WhenIdle 才返回。
	idle chan struct{}
	// quiet 是 idle 已经关过的一次性标记。
	quiet bool
	// idleErr 非 nil 时 WhenIdle 交回这个错。
	idleErr error

	followups []llm.Message
	cancels   []sessionlog.TurnEndCancelCause

	// onFollowup 在提示词投进来的那一刻跑，用来摆出这一轮跑完之后的日志。
	onFollowup func(*scriptedAgent, llm.Message)
}

func (a *scriptedAgent) ID() sessionlog.SessionID                  { return a.id }
func (a *scriptedAgent) Options() agent.Options                    { return a.options }
func (a *scriptedAgent) Session() *coresession.Session             { return a.live }
func (a *scriptedAgent) Inbox() *agent.Inbox                       { return nil }
func (a *scriptedAgent) Scope() *scope.Scope                       { return a.scope }
func (a *scriptedAgent) Status() agent.Status                      { return agent.StatusIdle }
func (a *scriptedAgent) Send(llm.Message, agent.InboxTarget, bool) {}
func (a *scriptedAgent) Steer(llm.Message)                         {}
func (a *scriptedAgent) Inject(llm.Message)                        {}
func (a *scriptedAgent) Prepend(llm.Message, agent.InboxTarget)    {}

func (a *scriptedAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *scriptedAgent) WhenIdle(ctx context.Context) error {
	select {
	case <-a.idle:
		a.mutex.Lock()
		defer a.mutex.Unlock()
		return a.idleErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// settle 让这个 agent 静下来，幂等。
func (a *scriptedAgent) settle() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.quiet {
		return
	}
	a.quiet = true
	close(a.idle)
}

func (a *scriptedAgent) Cancel(cause sessionlog.TurnEndCancelCause, _ agent.CancelOptions) {
	a.mutex.Lock()
	a.cancels = append(a.cancels, cause)
	a.mutex.Unlock()
	// 一次取消把这个 agent 停到静止——结算那一路正等在 WhenIdle 上。
	a.settle()
}

func (a *scriptedAgent) Followup(message llm.Message) {
	a.mutex.Lock()
	a.followups = append(a.followups, message)
	hook := a.onFollowup
	a.mutex.Unlock()
	if hook != nil {
		hook(a, message)
	}
}

// delivered 交出这个 agent 收到的那些跟进消息。
func (a *scriptedAgent) delivered() []llm.Message {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]llm.Message(nil), a.followups...)
}

// cancelled 交出历次取消的理由。
func (a *scriptedAgent) cancelled() []sessionlog.TurnEndCancelCause {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]sessionlog.TurnEndCancelCause(nil), a.cancels...)
}

// appendAll 把这些事件挨个追加到它自己的日志上。
func (a *scriptedAgent) appendAll(t *testing.T, events ...sessionlog.Event) {
	t.Helper()
	for _, each := range events {
		if _, err := a.live.Append(each); err != nil {
			t.Fatalf("追加事件失败：%v", err)
		}
	}
}

// runTurn 摆出一个「开、进过模型步骤、按 reason 收尾」的完整回合，中间那几条助手消息
// 按序追加，并在收尾之前把这一轮的回合号报给注册表。
func (a *scriptedAgent) runTurn(
	t *testing.T,
	agents *agent.Registry,
	message llm.Message,
	turn int,
	reason sessionlog.TurnEndReason,
	says ...string,
) {
	t.Helper()
	a.appendAll(t,
		logEvent(t, sessionlog.EventTurnStart, sessionlog.TurnStartData{Turn: turn}),
		logEvent(t, sessionlog.EventStepStart, sessionlog.StepStartData{Turn: turn, Step: 0}),
	)
	if err := agents.ReportInboxClaimed(a, message, turn); err != nil {
		t.Fatalf("报认领失败：%v", err)
	}
	for _, said := range says {
		a.appendAll(t, assistantEvent(t, turn, textContent(said)))
	}
	a.appendAll(t, logEvent(t, sessionlog.EventTurnEnd, sessionlog.TurnEndData{Turn: turn, Reason: reason}))
}

// ---- 假存档 ----

// storedSession 是存档里的一条：那份耐久的头，加上它整条事件日志。
type storedSession struct {
	header sessionlog.SessionHeader
	events []sessionlog.Event
}

// fakeCatalog 是一份摆在内存里的会话存档，同时当 [SessionCatalog] 和续跑的数据源。
type fakeCatalog struct {
	mutex sync.Mutex
	// order 定死 List 交出去的次序：一份真实现的次序是不保证的，用例不该依赖它。
	order []sessionlog.SessionID
	// stored 是那些落了档的会话。
	stored map[sessionlog.SessionID]storedSession
	// listFail 非 nil 时翻存档当场失败。
	listFail error
}

func newCatalog() *fakeCatalog {
	return &fakeCatalog{stored: map[sessionlog.SessionID]storedSession{}}
}

// put 往存档里放一条会话。
func (c *fakeCatalog) put(header sessionlog.SessionHeader, events ...sessionlog.Event) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, seen := c.stored[header.ID]; !seen {
		c.order = append(c.order, header.ID)
	}
	c.stored[header.ID] = storedSession{header: header, events: events}
}

// take 取出一条落了档的会话。
func (c *fakeCatalog) take(id sessionlog.SessionID) (storedSession, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	found, ok := c.stored[id]
	return found, ok
}

func (c *fakeCatalog) List(context.Context) ([]sessionlog.SessionHeader, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.listFail != nil {
		return nil, c.listFail
	}
	headers := make([]sessionlog.SessionHeader, 0, len(c.order))
	for _, id := range c.order {
		headers = append(headers, c.stored[id].header)
	}
	return headers, nil
}

// archived 造一条落了档的会话头。
func archived(id sessionlog.SessionID, createdAt int64, workspace sessionlog.WorkspaceID) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{
		Version:     sessionlog.FormatVersion,
		ID:          id,
		CreatedAt:   createdAt,
		WorkspaceID: workspace,
	}
}

// ---- 假造法 ----

// scriptedFactory 把整条真创建路走完：铸自己的作用域、在活会话表里建会话、进注册表、
// 公布。少任何一步，那几条认人的边就测不到。
type scriptedFactory struct {
	agents   *agent.Registry
	sessions *coresession.Store

	mutex sync.Mutex
	// creates 是历次造法请求，按调用顺序。
	creates []agent.CreateOptions
	// made 是造出来的那些 agent，按会话 id。
	made map[sessionlog.SessionID]*scriptedAgent
	// createFail 非 nil 时创建当场失败。
	createFail error
	// resumeFail 非 nil 时续跑当场失败。
	resumeFail error
	// archive 是续跑读的那份存档，为 nil 表示这台造法根本续不动。
	archive *fakeCatalog
	// disposeFail 非 nil 时每一个句柄的处置都报这个错；真的摘除照旧走完。
	disposeFail error
	// gate 非 nil 时，每一次创建都先等它开。
	gate chan struct{}
	// enter 非 nil 时，每一次创建在卡住之前先往它上面报一声。
	enter chan struct{}
	// onAgent 在一个 agent 已经公布、句柄还没交出去的那一刻跑。
	onAgent func(*scriptedAgent)
}

func newFactory(agents *agent.Registry, sessions *coresession.Store) *scriptedFactory {
	return &scriptedFactory{
		agents:   agents,
		sessions: sessions,
		made:     map[sessionlog.SessionID]*scriptedAgent{},
	}
}

// only 交出唯一那个造出来的 agent，不恰好一个就当场失败。
func (f *scriptedFactory) only(t *testing.T) *scriptedAgent {
	t.Helper()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if len(f.made) != 1 {
		t.Fatalf("该恰好造出一个 agent，实际 %d 个", len(f.made))
	}
	for _, made := range f.made {
		return made
	}
	return nil
}

// created 交出历次创建请求。
func (f *scriptedFactory) created() []agent.CreateOptions {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]agent.CreateOptions(nil), f.creates...)
}

func (f *scriptedFactory) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	f.mutex.Lock()
	f.creates = append(f.creates, options)
	failure, gate, enter, planted, hook := f.createFail, f.gate, f.enter, f.disposeFail, f.onAgent
	f.mutex.Unlock()

	if enter != nil {
		enter <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	if failure != nil {
		return agent.Handle{}, failure
	}

	agentScope, err := scope.New(scope.NewKey(string(options.SessionID)), scope.Options{Parent: owner.Key()})
	if err != nil {
		return agent.Handle{}, err
	}
	live, err := f.sessions.Create(ctx, agentScope, options.SessionID, coresession.CreateOptions{
		WorkspaceID:   options.WorkspaceID,
		ParentSession: options.ParentSession,
		SeedLength:    options.SeedLength,
	})
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	return f.publish(ctx, agentScope, live, options.SessionID, options.AgentOptions, options.Setup, planted, hook)
}

// Resume 走的是同一条装配路，差别只有那个活会话是从存下来的头和事件上重建的。
//
// 那份存档由 [fakeCatalog] 提供：造法和目录读的是同一张表，不然一次续跑会重建出一条
// 和存档对不上的会话，而这一包好几条判据（cwd 是否相符、日志里记着哪条路由）恰恰要
// 看那两样。
func (f *scriptedFactory) Resume(
	ctx context.Context,
	owner *scope.Scope,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	f.mutex.Lock()
	failure, planted, hook := f.resumeFail, f.disposeFail, f.onAgent
	archive := f.archive
	f.mutex.Unlock()
	if failure != nil {
		return agent.Handle{}, failure
	}
	if archive == nil {
		return agent.Handle{}, errors.New("这台造法没挂存档")
	}

	stored, ok := archive.take(options.ResumeSessionID)
	if !ok {
		return agent.Handle{}, fmt.Errorf("存档里没有 %s", options.ResumeSessionID)
	}
	agentScope, err := scope.New(
		scope.NewKey(string(options.ResumeSessionID)), scope.Options{Parent: owner.Key()})
	if err != nil {
		return agent.Handle{}, err
	}
	live, err := f.sessions.PrepareRestored(options.ResumeSessionID, coresession.RestoreOptions{
		Seed: stored.events, Header: stored.header,
	})
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	detachSession, err := f.sessions.Enter(agentScope, live)
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if _, err := agentScope.Defer("scriptedFactory.Resume()", detachSession); err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if err := f.sessions.Announce(ctx, live); err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	return f.publish(
		ctx, agentScope, live, options.ResumeSessionID, options.AgentOptions, options.Setup, planted, hook)
}

// publish 把「跑 setup、进注册表、commit、公布」这四步走完，交出那个句柄。
//
// 次序照 [agent.Setup] 的约定：铸出作用域之后、公布之前跑组装，报错就回滚。少了它，
// 这台假造法会让「装配失败必须挡住这次创建」那一整类用例假通过。
func (f *scriptedFactory) publish(
	ctx context.Context,
	agentScope *scope.Scope,
	live *coresession.Session,
	sessionID sessionlog.SessionID,
	options agent.Options,
	setup agent.Setup,
	planted error,
	hook func(*scriptedAgent),
) (agent.Handle, error) {
	made := &scriptedAgent{
		id:      sessionID,
		scope:   agentScope,
		live:    live,
		options: options,
		idle:    make(chan struct{}),
	}

	var commit func() error
	if setup != nil {
		var err error
		commit, err = setup(ctx, agentScope)
		if err != nil {
			_ = agentScope.Dispose(context.Background())
			return agent.Handle{}, err
		}
	}

	detach, err := f.agents.Enter(made, nil)
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if commit != nil {
		if err := commit(); err != nil {
			_ = detach(context.Background())
			_ = agentScope.Dispose(context.Background())
			return agent.Handle{}, err
		}
	}
	if err := f.agents.Announce(ctx, made); err != nil {
		_ = detach(context.Background())
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}

	f.mutex.Lock()
	f.made[sessionID] = made
	f.mutex.Unlock()
	if hook != nil {
		hook(made)
	}

	var once sync.Once
	return agent.Handle{Agent: made, Dispose: func(ctx context.Context) error {
		var failure error
		once.Do(func() {
			// 处置必须把它停到静止，否则结算那一路会一直挂在 WhenIdle 上。
			made.settle()
			failure = errors.Join(planted, detach(ctx), agentScope.Dispose(ctx))
		})
		return failure
	}}, nil
}

// ---- 夹具 ----

// fixture 是一座装好的桥和它周边那几样协作者。
type fixture struct {
	bridge   *Bridge
	peer     *recordingPeer
	agents   *agent.Registry
	sessions *coresession.Store
	factory  *scriptedFactory
	store    *fakeStore
	// workspaces 是 [newFixture] 默认装上去的那册登记；换掉了 Config.Workspaces 的
	// 用例不该再看它。
	workspaces *fakeWorkspaces
	owner      *scope.Scope
	config     Config
}

// newFixture 装一座桥：真的 agent 注册表、真的会话存储，假的只有那条通道、那台附件
// 仓库和 agent 本身。
//
// 订阅落在 [scope.NewRoot] 造的那个作用域上——全局层，看得见运行时里的每一个会话，
// 这正是这座桥该有的视野。
func newFixture(t *testing.T, mutate func(*Config)) *fixture {
	t.Helper()
	quiet := quietLogger()

	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quiet})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: tickingClock()})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	factory := newFactory(agents, sessions)
	if _, err := agents.SetFactory(factory); err != nil {
		t.Fatalf("登记 agent 造法失败：%v", err)
	}

	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	prompts, err := systemprompt.NewRegistry(t.Context(), owner, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}

	store := &fakeStore{}
	workspaces := newFakeWorkspaces()
	config := Config{
		Agents:      agents,
		Sessions:    sessions,
		Attachments: store,
		Models:      imageModels(),
		Prompts:     prompts,
		Provider:    "p",
		Model:       "m",
		Workspaces:  workspaces,
		Logger:      quiet,
	}
	if mutate != nil {
		mutate(&config)
	}
	bridge, err := New(config)
	if err != nil {
		t.Fatalf("造桥失败：%v", err)
	}

	peer := &recordingPeer{}
	quiesce, err := bridge.Install(t.Context(), owner, peer)
	if err != nil {
		t.Fatalf("装桥失败：%v", err)
	}
	t.Cleanup(func() { _ = quiesce(context.Background()) })

	return &fixture{
		bridge:     bridge,
		peer:       peer,
		agents:     agents,
		sessions:   sessions,
		factory:    factory,
		store:      store,
		workspaces: workspaces,
		owner:      owner,
		config:     config,
	}
}

// handshake 走一次握手，把那句内联图声明算出来。
func (f *fixture) handshake(t *testing.T) wire.InitializeResponse {
	t.Helper()
	response, err := f.bridge.Initialize(t.Context(), wire.InitializeRequest{
		ProtocolVersion: wire.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("握手不该失败：%v", err)
	}
	return response
}

// newSession 开一个会话并交出它的标识。
func (f *fixture) newSession(t *testing.T) wire.SessionId {
	t.Helper()
	response, err := f.bridge.NewSession(t.Context(), wire.NewSessionRequest{Cwd: clientCwd})
	if err != nil {
		t.Fatalf("开会话不该失败：%v", err)
	}
	return response.SessionId
}

// record 取出这条会话在桥上的那份记录，取不到当场失败。
func (f *fixture) record(t *testing.T, id wire.SessionId) *sessionRecord {
	t.Helper()
	f.bridge.mutex.Lock()
	defer f.bridge.mutex.Unlock()
	found, ok := f.bridge.sessions[sessionlog.SessionID(id)]
	if !ok {
		t.Fatalf("会话 %s 不在桥上", id)
	}
	return found
}

// waitQuiet 等这条会话当下这条投递链送完。
//
// 接在一次同步的事件追加后面才作数：那一步已经把这一节接上了尾巴，所以「当下这条尾巴」
// 关掉就等于那一节送到了。异步排上来的那种（拓扑变动那一路）得用 [fixture.waitUpdates]。
func (f *fixture) waitQuiet(record *sessionRecord) {
	f.bridge.mutex.Lock()
	tail := record.outputTail
	f.bridge.mutex.Unlock()
	<-tail
}

// waitUpdates 等对面收满 count 条满足 match 的更新，等不到就当场失败。
//
// 这是给那些从别的 goroutine 排上来的推送用的：那种情况下投递链的尾巴在这里看还没接上，
// 等它等的是上一节。
func (f *fixture) waitUpdates(t *testing.T, count int, match func(wire.SessionNotification) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		seen := 0
		f.peer.mutex.Lock()
		for _, update := range f.peer.updates {
			if match(update) {
				seen++
			}
		}
		f.peer.mutex.Unlock()
		if seen >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等了十秒也没收满 %d 条更新，只收到 %d 条", count, seen)
		}
		time.Sleep(time.Millisecond)
	}
}

// promptText 排一轮纯文本输入。
func (f *fixture) promptText(t *testing.T, id wire.SessionId, body string) (wire.PromptResponse, error) {
	t.Helper()
	return f.bridge.Prompt(t.Context(), wire.PromptRequest{
		SessionId: id,
		Prompt:    []wire.ContentBlock{wire.TextBlock(body)},
	})
}

// scriptTurn 让下一次投递进来的提示词摆出一个跑完的回合：几句助手话，然后按 reason 收尾。
func (f *fixture) scriptTurn(made *scriptedAgent, t *testing.T, reason sessionlog.TurnEndReason, says ...string) {
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, message llm.Message) {
		self.runTurn(t, f.agents, message, 1, reason, says...)
		self.settle()
	}
	made.mutex.Unlock()
}

// closedChan 造一条已经关掉的通道。
func closedChan() chan struct{} {
	made := make(chan struct{})
	close(made)
	return made
}

// base64Of 把这些字节编成规范 base64。
func base64Of(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// freeAgent 造一个游离 agent：它不在任何注册表里，只为内容那几条不碰运行时的用例
// 提供一条「当下路由」。
func freeAgent(t *testing.T, provider, model string) *scriptedAgent {
	t.Helper()
	id := sessionlog.SessionID("free")
	header := sessionlog.SessionHeader{ID: id, WorkspaceID: testWorkspaceID}
	live, err := coresession.NewSession(id, coresession.Options{Header: &header, Now: tickingClock()})
	if err != nil {
		t.Fatalf("造游离会话失败：%v", err)
	}
	return &scriptedAgent{
		id:      id,
		live:    live,
		options: agent.Options{Provider: provider, Model: model},
		idle:    make(chan struct{}),
	}
}

func (a *scriptedAgent) Remove(llm.MessageID) {}

func (a *scriptedAgent) Replace(llm.MessageID, llm.Message) {}
