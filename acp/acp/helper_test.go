// 本文件的作用：这一包测试共用的装配——一条会记账的假对面、一台可编排的假附件仓库、
// 一个真的会停在 WhenIdle 上的 agent、一份把整条创建路走完的假造法，以及把它们和
// 一台真运行时连起来的夹具。

package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	wire "github.com/coder/acp-go-sdk"

	"ds-harness-go/attachment"
	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// absolutePath 是一条在本机上确实绝对的路径；写死哪一边的字面量都会让另一个平台上的
// 用例变成假通过。
var absolutePath = filepath.Join(os.TempDir(), "ds-harness-go-acp")

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

// fakeModels 按剧本回一份模型元数据。
type fakeModels struct {
	modalities []llm.ModelModality
	fail       error
}

func (m fakeModels) ResolveModelInfo(context.Context, string, string) (llm.ResolvedModelInfo, error) {
	if m.fail != nil {
		return llm.ResolvedModelInfo{}, m.fail
	}
	return llm.ResolvedModelInfo{
		ModelInfo: llm.ModelInfo{InputModalities: m.modalities},
	}, nil
}

// imageModels 是一台声明收图的名册。
func imageModels() fakeModels {
	return fakeModels{modalities: []llm.ModelModality{llm.ModalityImage}}
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
		Cwd:           options.Cwd,
		ParentSession: options.ParentSession,
		SeedLength:    options.SeedLength,
	})
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	made := &scriptedAgent{
		id:      options.SessionID,
		scope:   agentScope,
		live:    live,
		options: options.AgentOptions,
		idle:    make(chan struct{}),
	}

	detach, err := f.agents.Enter(made, nil)
	if err != nil {
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}
	if err := f.agents.Announce(ctx, made); err != nil {
		_ = detach(context.Background())
		_ = agentScope.Dispose(context.Background())
		return agent.Handle{}, err
	}

	f.mutex.Lock()
	f.made[options.SessionID] = made
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

func (f *scriptedFactory) Resume(context.Context, *scope.Scope, agent.ResumeOptions) (agent.Handle, error) {
	return agent.Handle{}, errors.New("这一包不走续跑")
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
	owner    *scope.Scope
	config   Config
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

	store := &fakeStore{}
	config := Config{
		Agents:      agents,
		Sessions:    sessions,
		Attachments: store,
		Models:      imageModels(),
		Provider:    "p",
		Model:       "m",
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
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	quiesce, err := bridge.Install(t.Context(), owner, peer)
	if err != nil {
		t.Fatalf("装桥失败：%v", err)
	}
	t.Cleanup(func() { _ = quiesce(context.Background()) })

	return &fixture{
		bridge:   bridge,
		peer:     peer,
		agents:   agents,
		sessions: sessions,
		factory:  factory,
		store:    store,
		owner:    owner,
		config:   config,
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
	response, err := f.bridge.NewSession(t.Context(), wire.NewSessionRequest{Cwd: absolutePath})
	if err != nil {
		t.Fatalf("开会话不该失败：%v", err)
	}
	return response.SessionId
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
	header := sessionlog.SessionHeader{ID: id, Cwd: absolutePath}
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
