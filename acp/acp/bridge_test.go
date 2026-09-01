// 本文件的作用：把这座桥的装配、五个协议方法、运行时那三条边、审批那条线、以及
// 收摊那条次序敏感的路，各自用一台真的运行时压一遍。

package acp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// # 这些测试防的是什么错
//
//   - 装配面缺一样却拖到第一个请求进来才空指针。
//   - 装了两次：第二次会把第一次那条通道盖掉，从此第一条连接再也收不到东西。
//   - 一句"我支持内联图"在说不清的时候被声明了出去。
//   - 同一个会话上并发的两次 `session/prompt` 都被收下，于是两轮输入交错着排进去。
//   - 助手输出乱序送出去：每一块图都要重新读一遍存储，那几次异步读会把次序打乱。
//   - 一次提示词在它自己那个回合还没关掉的时候就报了 `end_turn`。
//   - 一个说不出结局的回合被报成正常结束——对面会以为这一轮拿到了答案。
//   - 相关回合**之内**的失败被 agent/error 那条边重复认领一次。
//   - 一次取消在提示词进耐久收件箱之前就打向 agent，把这个 agent 上别的生产者一起停掉。
//   - 一次取消挤在投递前后：Go 这边真的会交错，那条迟到的消息会没人清。
//   - 认不出的方法名回 -32603，让客户端把「你没有这个方法」当成「你炸了」去重试。
//   - 收摊拆到一半就返回，留下一个还在跑模型的 agent；或者收摊跑了两遍。
//   - 一条发不出去的通知把发出那条边的那次追加带崩。

// ---- 装配 ----

func TestNewRefusesToBuildWithoutItsRequiredCollaborators(t *testing.T) {
	t.Parallel()
	agents, err := agent.NewRegistry(agent.RegistryOptions{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: tickingClock()})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	for name, config := range map[string]Config{
		"缺 agent 注册表": {Sessions: sessions},
		"缺会话存储":       {Agents: agents},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(config); err == nil {
				t.Fatal("装配面缺一样就该当场拒，别拖到第一个请求进来")
			}
		})
	}
	if _, err := New(Config{Agents: agents, Sessions: sessions}); err != nil {
		t.Fatalf("两样必填都齐了就该造得出来：%v", err)
	}
}

func TestInstallRefusesIncompleteOrRepeatedWiring(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	// 第二次装会把第一条通道盖掉，从此第一条连接再也收不到东西。
	if _, err := f.bridge.Install(t.Context(), f.owner, &recordingPeer{}); err == nil {
		t.Fatal("装第二次该拒")
	}

	fresh, err := New(Config{Agents: f.agents, Sessions: f.sessions})
	if err != nil {
		t.Fatalf("造桥失败：%v", err)
	}
	if _, err := fresh.Install(t.Context(), nil, &recordingPeer{}); err == nil {
		t.Fatal("没有作用域该拒")
	}
	if _, err := fresh.Install(t.Context(), f.owner, nil); err == nil {
		t.Fatal("没有通道该拒")
	}
}

func TestInstallHangsTheApprovalAnswererOnlyWhenAServiceIsMounted(t *testing.T) {
	t.Parallel()
	approvals, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return nil, errors.New("这一条用例不走审计") },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造审批服务失败：%v", err)
	}
	f := newFixture(t, func(config *Config) { config.Approvals = approvals })
	// 五条订阅：会话事件、收件箱认领、agent 错误、LLM 拓扑变动、审批答复者。
	f.bridge.mutex.Lock()
	installed := len(f.bridge.disposers)
	f.bridge.mutex.Unlock()
	if installed != 5 {
		t.Fatalf("挂了审批服务时该有五条订阅，实际 %d 条", installed)
	}

	// 两样可选协作者各带一条：都不挂就只剩那三条必有的。
	bare := newFixture(t, func(config *Config) { config.Models, config.Prompts = nil, nil })
	bare.bridge.mutex.Lock()
	minimal := len(bare.bridge.disposers)
	bare.bridge.mutex.Unlock()
	if minimal != 3 {
		t.Fatalf("什么可选协作者都不挂时该只有三条订阅，实际 %d 条", minimal)
	}
}

func TestInstallUnwindsEverythingWhenOneSubscriptionFails(t *testing.T) {
	t.Parallel()
	// 装到一半就失败：已经挂上的那几条必须当场摘掉，别留下一条会往一座永远不会开工的
	// 桥上转发的边。
	f := newFixture(t, nil)
	bridge, err := New(Config{
		Agents:    f.agents,
		Sessions:  f.sessions,
		Approvals: failingRegistrar{},
		Logger:    quietLogger(),
	})
	if err != nil {
		t.Fatalf("造桥失败：%v", err)
	}
	if _, err := bridge.Install(t.Context(), scope.NewRoot(), &recordingPeer{}); err == nil {
		t.Fatal("挂订阅失败该往上报")
	}
	bridge.mutex.Lock()
	defer bridge.mutex.Unlock()
	if len(bridge.disposers) != 0 || bridge.peer != nil || bridge.owner != nil {
		t.Fatalf("失败之后不该留下任何装配痕迹：%d 条订阅", len(bridge.disposers))
	}
}

// failingRegistrar 是一条挂不上去的审批订阅。
type failingRegistrar struct{}

func (failingRegistrar) RegisterAnswerer(
	context.Context, *scope.Scope, userapproval.Answerer,
) (func(context.Context) error, error) {
	return nil, errors.New("挂不上")
}

// stuckRegistrar 挂得上，但摘不下来。
type stuckRegistrar struct{}

func (stuckRegistrar) RegisterAnswerer(
	context.Context, *scope.Scope, userapproval.Answerer,
) (func(context.Context) error, error) {
	return func(context.Context) error { return errors.New("摘不掉") }, nil
}

func TestQuiesceReportsASubscriptionThatCannotBeReleased(t *testing.T) {
	t.Parallel()
	// 一条摘不掉的订阅意味着这座已经收摊的桥还挂在运行时上。收摊本身照旧走完，
	// 但这件事必须报上去，不能咽掉。
	f := newFixture(t, func(config *Config) { config.Approvals = stuckRegistrar{} })
	f.newSession(t)
	err := f.bridge.Quiesce(t.Context())
	if err == nil || !strings.Contains(err.Error(), "摘订阅失败") {
		t.Fatalf("摘不掉的订阅该报上去，实际 %v", err)
	}
	if live := f.agents.List(); len(live) != 0 {
		t.Fatalf("拆 agent 那一步照旧要走完，实际还有 %d 个", len(live))
	}
}

func TestRegisterInvariantsClaimsThePackageName(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表该拒")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	release, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	release()
}

// ---- 握手 ----

func TestInitializeAdvertisesImagesOnlyWhenItCanBeHonest(t *testing.T) {
	t.Parallel()
	// 一句声明出去之后客户端会照它发图，所以说不清的一律说没有。
	honest := newFixture(t, nil)
	if got := honest.handshake(t); !got.AgentCapabilities.PromptCapabilities.Image {
		t.Fatal("样样都齐时该如实声明支持内联图")
	}

	blind := newFixture(t, func(config *Config) { config.Models = fakeModels{} })
	response := blind.handshake(t)
	if response.AgentCapabilities.PromptCapabilities.Image {
		t.Fatal("模型没声明收图时不该声明支持")
	}
	if response.AgentCapabilities.PromptCapabilities.Audio ||
		response.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Fatal("这条线从不搬音频和内嵌资源")
	}
	if response.ProtocolVersion != wire.ProtocolVersionNumber {
		t.Fatalf("协议版本该是 %d，实际 %d", wire.ProtocolVersionNumber, response.ProtocolVersion)
	}
	if response.AgentInfo == nil || response.AgentInfo.Name != AgentName {
		t.Fatalf("报出来的身份不对：%#v", response.AgentInfo)
	}
}

func TestAuthenticateDoesNothing(t *testing.T) {
	t.Parallel()
	// 这条线上的客户端是受信任的：认证由承载它的那条通道负责。
	f := newFixture(t, nil)
	if _, err := f.bridge.Authenticate(t.Context(), wire.AuthenticateRequest{}); err != nil {
		t.Fatalf("认证不该失败：%v", err)
	}
}

// ---- 开会话 ----

func TestNewSessionRejectsFeaturesOutsideTheAutomationContract(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	cases := map[string]wire.NewSessionRequest{
		"相对路径":   {Cwd: "relative/path"},
		"额外目录":   {Cwd: absolutePath, AdditionalDirectories: []string{absolutePath}},
		"mcp 服务": {Cwd: absolutePath, McpServers: []wire.McpServer{{}}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.bridge.NewSession(t.Context(), params)
			assertRequestError(t, err, -32602)
		})
	}
}

func TestNewSessionCreatesAnAgentOnTheSharedRoute(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	created := f.factory.created()
	if len(created) != 1 {
		t.Fatalf("该恰好建一个 agent，实际 %d 个", len(created))
	}
	if created[0].SessionID != sessionlog.SessionID(id) {
		t.Fatalf("交回的会话标识和建出来那个对不上：%s vs %s", id, created[0].SessionID)
	}
	if created[0].AgentOptions.Provider != "p" || created[0].AgentOptions.Model != "m" {
		t.Fatalf("该用这条线共用的那份路由：%#v", created[0].AgentOptions)
	}
}

func TestNewSessionReportsAFailedCreation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.factory.mutex.Lock()
	f.factory.createFail = errors.New("建不出来")
	f.factory.mutex.Unlock()
	_, err := f.bridge.NewSession(t.Context(), wire.NewSessionRequest{Cwd: absolutePath})
	assertRequestError(t, err, -32603)
}

func TestNewSessionRefusesOnceTheBridgeIsDisposed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	_, err := f.bridge.NewSession(t.Context(), wire.NewSessionRequest{Cwd: absolutePath})
	assertRequestError(t, err, -32603)
}

func TestNewSessionRefusesBeforeItIsInstalled(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	bridge, err := New(Config{Agents: f.agents, Sessions: f.sessions})
	if err != nil {
		t.Fatalf("造桥失败：%v", err)
	}
	_, err = bridge.NewSession(t.Context(), wire.NewSessionRequest{Cwd: absolutePath})
	assertRequestError(t, err, -32603)
}

func TestNewSessionDisposesAnAgentThatRacedTheConnectionClosing(t *testing.T) {
	t.Parallel()
	// 一次真的连接关闭挤在了创建的半路上：这一个再记进表里就没人拆得掉它。
	f := newFixture(t, nil)
	gate, enter := make(chan struct{}), make(chan struct{})
	f.factory.mutex.Lock()
	f.factory.gate, f.factory.enter = gate, enter
	// 连这一次补拆都失败：那就只剩记一行了，但交给对面的仍然是"连接关掉了"这条
	// 结论——拆不掉是这一端的事，不该改写对面看到的原因。
	f.factory.disposeFail = errors.New("拆不掉")
	f.factory.mutex.Unlock()

	failed := make(chan error, 1)
	go func() {
		_, err := f.bridge.NewSession(context.Background(), wire.NewSessionRequest{Cwd: absolutePath})
		failed <- err
	}()
	<-enter
	if err := f.bridge.Quiesce(context.Background()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	close(gate)

	assertRequestError(t, <-failed, -32603)
	f.bridge.mutex.Lock()
	leaked := len(f.bridge.sessions)
	f.bridge.mutex.Unlock()
	if leaked != 0 {
		t.Fatalf("那个抢在半路建出来的会话该被拆掉，实际留下 %d 个", leaked)
	}
	if live := f.agents.List(); len(live) != 0 {
		t.Fatalf("那个 agent 该从注册表里摘掉，实际还有 %d 个", len(live))
	}
}

// ---- 提示词 ----

func TestPromptDeliversAssistantOutputInOrderAndEndsTheTurn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.handshake(t)
	id := f.newSession(t)
	made := f.factory.only(t)
	f.scriptTurn(made, t, sessionlog.CompletedTurnEnd{}, "一", "二", "三")

	response, err := f.promptText(t, id, "喂")
	if err != nil {
		t.Fatalf("这轮输入不该失败：%v", err)
	}
	if response.StopReason != wire.StopReasonEndTurn {
		t.Fatalf("该报 end_turn，实际 %s", response.StopReason)
	}
	if got := strings.Join(f.peer.chunks(), ""); got != "一二三" {
		t.Fatalf("助手输出该按序送出去，实际 %q", got)
	}
	if len(made.delivered()) != 1 {
		t.Fatalf("该恰好投递一条跟进消息，实际 %d 条", len(made.delivered()))
	}
}

func TestPromptFoldsMaxTokensIntoAnOrdinaryEndTurn(t *testing.T) {
	t.Parallel()
	// 撞上输出上限在回合那一层是 max_tokens，在提示词这一层只是一次寻常的静默。
	f := newFixture(t, nil)
	id := f.newSession(t)
	f.scriptTurn(f.factory.only(t), t, sessionlog.MaxTokensTurnEnd{})

	response, err := f.promptText(t, id, "喂")
	if err != nil {
		t.Fatalf("这轮输入不该失败：%v", err)
	}
	if response.StopReason != wire.StopReasonEndTurn {
		t.Fatalf("该报 end_turn，实际 %s", response.StopReason)
	}
}

func TestPromptTurnsAFailedTurnIntoAWireError(t *testing.T) {
	t.Parallel()
	// 报 error 的那种回合根本没有结局可言，所以它在这一层变成一个线上错误。
	f := newFixture(t, nil)
	id := f.newSession(t)
	f.scriptTurn(f.factory.only(t), t,
		sessionlog.ErrorTurnEnd{Error: llm.Failure{Message: "上游拒了"}})

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
}

func TestPromptReportsCancelledWhenTheTurnNeverClosed(t *testing.T) {
	t.Parallel()
	// 这一端说不出这一轮是怎么结束的，绝不能报成正常结束。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, _ llm.Message) { self.settle() }
	made.mutex.Unlock()

	response, err := f.promptText(t, id, "喂")
	if err != nil {
		t.Fatalf("这轮输入不该失败：%v", err)
	}
	if response.StopReason != wire.StopReasonCancelled {
		t.Fatalf("说不出结局时该报 cancelled，实际 %s", response.StopReason)
	}
}

func TestPromptFailsWhenCommittedOutputCannotBeConverted(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.store.mutex.Lock()
	f.store.readFail = errors.New("字节和引用对不上")
	f.store.mutex.Unlock()

	id := f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, message llm.Message) {
		self.appendAll(t,
			logEvent(t, sessionlog.EventTurnStart, sessionlog.TurnStartData{Turn: 1}),
			logEvent(t, sessionlog.EventStepStart, sessionlog.StepStartData{Turn: 1, Step: 0}),
		)
		if err := f.agents.ReportInboxClaimed(self, message, 1); err != nil {
			t.Errorf("报认领失败：%v", err)
		}
		self.appendAll(t, assistantEvent(t, 1, llm.Content{llm.ImageBlock{}}))
		self.appendAll(t, logEvent(t, sessionlog.EventTurnEnd,
			sessionlog.TurnEndData{Turn: 1, Reason: sessionlog.CompletedTurnEnd{}}))
		self.settle()
	}
	made.mutex.Unlock()

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
}

func TestPromptClaimsAnErrorRaisedOutsideItsOwnTurn(t *testing.T) {
	t.Parallel()
	// 相关回合**之内**的失败会在 turn/end 上留下一个 error 理由，不走 agent/error 那条边。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, _ llm.Message) {
		if err := f.agents.ReportError(agent.TurnError{
			Agent: self, Turn: 9, Err: errors.New("循环炸了"),
		}); err != nil {
			t.Errorf("报错误失败：%v", err)
		}
		self.settle()
	}
	made.mutex.Unlock()

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
}

func TestPromptIgnoresAnErrorInsideItsOwnTurn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, message llm.Message) {
		if err := f.agents.ReportInboxClaimed(self, message, 1); err != nil {
			t.Errorf("报认领失败：%v", err)
		}
		if err := f.agents.ReportError(agent.TurnError{
			Agent: self, Turn: 1, Err: errors.New("这一轮自己的失败"),
		}); err != nil {
			t.Errorf("报错误失败：%v", err)
		}
		self.appendAll(t, logEvent(t, sessionlog.EventTurnStart, sessionlog.TurnStartData{Turn: 1}))
		self.appendAll(t, logEvent(t, sessionlog.EventTurnEnd,
			sessionlog.TurnEndData{Turn: 1, Reason: sessionlog.CompletedTurnEnd{}}))
		self.settle()
	}
	made.mutex.Unlock()

	response, err := f.promptText(t, id, "喂")
	if err != nil {
		t.Fatalf("回合之内的失败该由 turn/end 说了算：%v", err)
	}
	if response.StopReason != wire.StopReasonEndTurn {
		t.Fatalf("该报 end_turn，实际 %s", response.StopReason)
	}
}

func TestPromptRejectsWhatItCannotServe(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)

	_, err := f.bridge.Prompt(t.Context(), wire.PromptRequest{
		SessionId: "不存在", Prompt: []wire.ContentBlock{wire.TextBlock("喂")}})
	assertRequestError(t, err, -32602)

	// 一条没通过准入的提示词：那个位子必须还回去，不然这条会话从此排不进任何东西。
	_, err = f.bridge.Prompt(t.Context(), wire.PromptRequest{
		SessionId: id, Prompt: []wire.ContentBlock{{Audio: &wire.ContentBlockAudio{}}}})
	assertRequestError(t, err, -32602)

	f.scriptTurn(f.factory.only(t), t, sessionlog.CompletedTurnEnd{})
	if _, err := f.promptText(t, id, "喂"); err != nil {
		t.Fatalf("上一次失败之后这条会话该还能用：%v", err)
	}
}

func TestPromptRefusesASecondPromptOnTheSameSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	reached, second := make(chan struct{}), make(chan error, 1)
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, _ llm.Message) {
		close(reached)
		_, err := f.bridge.Prompt(context.Background(), wire.PromptRequest{
			SessionId: id, Prompt: []wire.ContentBlock{wire.TextBlock("插队")}})
		second <- err
		self.settle()
	}
	made.mutex.Unlock()

	if _, err := f.promptText(t, id, "喂"); err != nil {
		t.Fatalf("第一轮不该失败：%v", err)
	}
	<-reached
	assertRequestError(t, <-second, -32602)
}

func TestPromptRefusesOnceTheBridgeIsDisposed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
}

func TestPromptRefusesAnAgentThatDiedOutsideTheBridge(t *testing.T) {
	t.Parallel()
	// agent 循环的一次重载会把 agent 换掉，会话记录却还在——投进去就没了声音。
	// 这里直接把记录上那个 agent 换成一个不在注册表里的：这正是"退休"在运行时里的
	// 样子，而这条路走不到公开接口上，只能这么摆。
	f := newFixture(t, nil)
	id := f.newSession(t)
	retired := freeAgent(t, "p", "m")
	f.bridge.mutex.Lock()
	f.bridge.sessions[sessionlog.SessionID(id)].agent = retired
	f.bridge.mutex.Unlock()

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
	if len(retired.delivered()) != 0 {
		t.Fatal("不该往一个已经退休的目的地投递")
	}
}

func TestPromptReportsAnAgentThatNeverSettles(t *testing.T) {
	t.Parallel()
	// 等这一轮跑完这一步自己失败了：说不出这一轮的结局，就不能报一个结局出去。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.idleErr = errors.New("等不到")
	made.onFollowup = func(self *scriptedAgent, _ llm.Message) { self.settle() }
	made.mutex.Unlock()

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32603)
	f.bridge.mutex.Lock()
	defer f.bridge.mutex.Unlock()
	if f.bridge.sessions[made.id].inflight != nil {
		t.Fatal("失败之后那个位子该腾出来，否则这条会话再也接不了第二条提示词")
	}
}

func TestAdmitStopsWhenTheRequestIsCancelledDuringAdmission(t *testing.T) {
	t.Parallel()
	// 准入自己不看上下文（纯文本那条路上没有任何会等的东西），所以撤单要在准入
	// 之后、投递之前再问一次。少了这一问，一条已经没人等的输入照样会排进耐久收件箱。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := f.bridge.admit(ctx, f.record(t, id), made, []wire.ContentBlock{wire.TextBlock("喂")}, false, &inflightPrompt{
		done: make(chan struct{}), admissionDone: make(chan struct{}), abortAdmission: func(error) {},
	}, agent.ModelSelection{}, false)
	assertRequestError(t, err, -32603)
	if len(made.delivered()) != 0 {
		t.Fatal("撤了之后不该再投递")
	}
}

func TestAdmitRefusesAnAgentThatRetiredWhileTheImagesWereBeingWritten(t *testing.T) {
	t.Parallel()
	// 写一批图是真的要花时间的，agent 循环的一次重载正好挤在那段时间里。投进一个
	// 已经退休的目的地就等于把这条输入丢了——没有人会再读那个收件箱。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	f.store.mutex.Lock()
	f.store.saveHook = func() {
		f.bridge.mutex.Lock()
		dispose := f.bridge.sessions[sessionlog.SessionID(id)].dispose
		f.bridge.mutex.Unlock()
		if err := dispose(context.Background()); err != nil {
			t.Errorf("拆会话失败：%v", err)
		}
	}
	f.store.mutex.Unlock()

	err := f.bridge.admit(
		t.Context(), f.record(t, id), made,
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true,
		&inflightPrompt{
			done: make(chan struct{}), admissionDone: make(chan struct{}), abortAdmission: func(error) {},
		}, agent.ModelSelection{}, false)
	assertRequestError(t, err, -32603)
	if len(made.delivered()) != 0 {
		t.Fatal("不该往一个已经退休的目的地投递")
	}
}

func TestAdmitCancelsAgentActivityWhenACancelLandsAroundDelivery(t *testing.T) {
	t.Parallel()
	// DSH 靠 JS 的单线程把"最后一次中止检查"和"投递"做成原子的。Go 里做不到：一次
	// 并发的 session/cancel 真的会挤在这两步之间，而它看见 messageQueued 还是假就不会
	// 去动这个 agent，于是这条迟到的消息没人清。投递之后补的那一次取消治的就是这个。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	inflight := &inflightPrompt{
		done:            make(chan struct{}),
		admissionDone:   make(chan struct{}),
		abortAdmission:  func(error) {},
		cancelRequested: true,
	}
	if err := f.bridge.admit(
		t.Context(), f.record(t, id), made, []wire.ContentBlock{wire.TextBlock("喂")}, false, inflight,
		agent.ModelSelection{}, false); err != nil {
		t.Fatalf("准入本身不该失败：%v", err)
	}
	if len(made.cancelled()) != 1 {
		t.Fatalf("投递之后该自己补一次取消，实际 %d 次", len(made.cancelled()))
	}
}

func TestMapAdmissionErrorKeepsWireErrorsIntact(t *testing.T) {
	t.Parallel()
	planted := invalidParams("已经是一个线上错误了")
	if got := mapAdmissionError(planted); got != error(planted) {
		t.Fatalf("已经成型的线上错误该原样传出去，实际 %#v", got)
	}
	assertRequestError(t, mapAdmissionError(errors.New("说不清的失败")), -32603)
	assertRequestError(t, mapAdmissionError(internalContent("这一端没办成", nil)), -32603)
}

// ---- 取消 ----

func TestCancelLeavesTheAgentAloneWhileAdmissionIsStillRunning(t *testing.T) {
	t.Parallel()
	// 准入不是 agent 的活儿：这条提示词进耐久收件箱之前，这个 agent 上别的生产者照常活着。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	f.bridge.mutex.Lock()
	record := f.bridge.sessions[sessionlog.SessionID(id)]
	installed := &inflightPrompt{
		done:           make(chan struct{}),
		admissionDone:  make(chan struct{}),
		abortAdmission: func(error) {},
	}
	record.inflight = installed
	f.bridge.mutex.Unlock()
	// 摆进去的这条准入永远跑不完，结算那条协程会一直等着它——收摊那一步也在等同一个
	// 东西，不放它走这个用例就卡死在拆夹具上。
	t.Cleanup(installed.finishAdmission)

	if err := f.bridge.Cancel(t.Context(), wire.CancelNotification{SessionId: id}); err != nil {
		t.Fatalf("取消不该失败：%v", err)
	}
	if len(made.cancelled()) != 0 {
		t.Fatal("还没进耐久收件箱的提示词不该把这个 agent 停掉")
	}
}

func TestCancelStopsAutonomousActivityWhenNoPromptIsInFlight(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	if err := f.bridge.Cancel(t.Context(), wire.CancelNotification{SessionId: id}); err != nil {
		t.Fatalf("取消不该失败：%v", err)
	}
	if len(made.cancelled()) != 1 {
		t.Fatalf("没有提示词时取消该照旧打向这个 agent，实际 %d 次", len(made.cancelled()))
	}
}

func TestCancelIgnoresAnUnknownSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	// 取消是一条单向通知，认不出的会话上它什么都不做，更不该报错。
	if err := f.bridge.Cancel(t.Context(), wire.CancelNotification{SessionId: "不存在"}); err != nil {
		t.Fatalf("认不出的会话上取消该是空操作：%v", err)
	}
}

func TestCancelSettlesAnInFlightPromptAsCancelled(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	made.mutex.Lock()
	made.onFollowup = func(_ *scriptedAgent, _ llm.Message) {
		if err := f.bridge.Cancel(context.Background(), wire.CancelNotification{SessionId: id}); err != nil {
			t.Errorf("取消不该失败：%v", err)
		}
	}
	made.mutex.Unlock()

	response, err := f.promptText(t, id, "喂")
	if err != nil {
		t.Fatalf("被取消的一轮该报停止原因，不该报错：%v", err)
	}
	if response.StopReason != wire.StopReasonCancelled {
		t.Fatalf("该报 cancelled，实际 %s", response.StopReason)
	}
}

// ---- 运行时那几条边 ----

func TestSessionEventsFromOtherSessionsAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)

	// 一个这座桥根本没建过的会话：它归别人管，一个字都不该往这条线上发。
	stranger, err := f.sessions.Create(t.Context(), f.owner, "外人", coresession.CreateOptions{Cwd: absolutePath})
	if err != nil {
		t.Fatalf("建外人会话失败：%v", err)
	}
	if _, err := stranger.Append(assistantEvent(t, 1, textContent("别人的话"))); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	// 自己名下的会话上，一条解不开的负载只该记一行，不该把追加带崩。
	// 用一个合法的 JSON 数组：会话本身只验 JSON 是否成立，形状对不对要到这座桥
	// 自己去解的时候才发现，而这里测的正是那一步。
	made.appendAll(t, sessionlog.Event{
		Type: sessionlog.EventTurnEnd, Data: []byte("[1,2,3]")})

	if got := f.peer.chunks(); len(got) != 0 {
		t.Fatalf("一条都不该发出去，实际 %v", got)
	}
}

func TestSessionEventsWithAnUnreadablePayloadOnlyGetLogged(t *testing.T) {
	t.Parallel()
	// 一条解不开的负载改变不了运行时里已经发生的那件事：记一行，然后什么都不发。
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	made.appendAll(t, sessionlog.Event{
		Type: sessionlog.EventAssistantMessage, Data: []byte("[1,2,3]"), SurfaceOp: sessionlog.AppendOp{}})
	if got := f.peer.chunks(); len(got) != 0 {
		t.Fatalf("一条都不该发出去，实际 %v", got)
	}
}

func TestOnlyAutomatableAssistantBlocksReachTheWire(t *testing.T) {
	t.Parallel()
	// 推理不上线。整条消息只有推理时，这一步该是彻底的静默，而不是一条空块。
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	made.appendAll(t, assistantEvent(t, 1, llm.Content{
		llm.ReasoningBlock{Text: "想了想"},
		llm.TextBlock{Text: "说出口的"},
	}))
	f.bridge.mutex.Lock()
	tail := f.bridge.sessions[made.id].outputTail
	f.bridge.mutex.Unlock()
	<-tail
	if got := f.peer.chunks(); len(got) != 1 || got[0] != "说出口的" {
		t.Fatalf("只该发出说出口的那一块，实际 %v", got)
	}
}

func TestSettlementLetsGoOfASlotThatIsNoLongerItsOwn(t *testing.T) {
	t.Parallel()
	// 结算和它那条补救分支都要先认一下"这个位子还是我的"。位子已经被别人清掉时，
	// 再清一次会把后来那条提示词的位子一起抹掉——那一条就永远等不到结论了。
	bare := &Bridge{config: Config{Logger: quietLogger()}}
	record := &sessionRecord{outputTail: closedChan()}
	stale := &inflightPrompt{
		done: make(chan struct{}), admissionDone: closedChan(), abortAdmission: func(error) {},
	}

	bare.settle(record, stale)
	bare.clearAndFinish(record, stale, errors.New("来晚了"))
	select {
	case <-stale.done:
		t.Fatal("一条已经不属于自己的结算不该给出结论")
	default:
	}
}

func TestDeliverIsANoOpBeforeAnythingIsInstalled(t *testing.T) {
	t.Parallel()
	// 一条还没装上去的桥没有任何可以发的地方；这一步必须是空操作而不是空指针。
	bare := &Bridge{config: Config{Logger: quietLogger()}}
	bare.deliver("随便", func(context.Context) ([]wire.SessionUpdate, error) {
		return []wire.SessionUpdate{{AgentMessageChunk: &wire.SessionUpdateAgentMessageChunk{
			Content: wire.TextBlock("喂"),
		}}}, nil
	}, nil)
}

func TestAgentErrorsOutsideAPromptAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	// 这个 agent 归这座桥管，但它此刻没有半路上的提示词：这次失败是自主活动里的，
	// 认领它会凭空造出一条没人等的结论。
	if err := f.agents.ReportError(agent.TurnError{Agent: made, Turn: 1, Err: errors.New("炸")}); err != nil {
		t.Fatalf("报错不该失败：%v", err)
	}

	// 一个在同一台注册表里、却不归这座桥管的 agent：它的失败与这条线无关。
	outsider, err := f.agents.Create(t.Context(), f.owner, agent.CreateOptions{
		SessionID: "局外人", Cwd: absolutePath,
	})
	if err != nil {
		t.Fatalf("建局外人失败：%v", err)
	}
	t.Cleanup(func() { _ = outsider.Dispose(context.Background()) })
	if err := f.agents.ReportError(
		agent.TurnError{Agent: outsider.Agent, Turn: 1, Err: errors.New("炸")}); err != nil {
		t.Fatalf("报错不该失败：%v", err)
	}
}

func TestInboxClaimsFromOtherPromptsAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	// 没有半路上的提示词时，任何一次认领都不该被认成"我这一条被认领了"。
	if err := f.agents.ReportInboxClaimed(made, llm.NewUserMessage(textContent("别人的"), llm.UserSource{}), 4); err != nil {
		t.Fatalf("报认领失败：%v", err)
	}
	f.bridge.mutex.Lock()
	defer f.bridge.mutex.Unlock()
	if f.bridge.sessions[made.id].inflight != nil {
		t.Fatal("凭空多出来一条半路上的提示词")
	}
}

func TestAgentErrorsFromStrangersAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	stranger := freeAgent(t, "p", "m")
	// 这个 agent 不在这座桥名下：它的失败与这条线无关。
	if err := f.agents.ReportError(agent.TurnError{Agent: stranger, Err: errors.New("炸")}); err == nil {
		t.Fatal("一个没进注册表的 agent 报错该被注册表拒掉")
	}
}

func TestNotifyIsANoOpBeforeAnythingIsInstalled(t *testing.T) {
	t.Parallel()
	// 一条发不出去的更新改变不了运行时里已经发生的那件事，更不该把发出这条边的那次
	// 追加带崩。
	bare := &Bridge{config: Config{Logger: quietLogger()}}
	bare.notify(t.Context(), wire.SessionNotification{})

	f := newFixture(t, nil)
	f.peer.mutex.Lock()
	f.peer.updateFail = errors.New("连接断了")
	f.peer.mutex.Unlock()
	id := f.newSession(t)
	f.scriptTurn(f.factory.only(t), t, sessionlog.CompletedTurnEnd{}, "说点什么")
	if _, err := f.promptText(t, id, "喂"); err != nil {
		t.Fatalf("一条发不出去的更新不该把这一轮带崩：%v", err)
	}
}

// ---- 审批 ----

func TestApprovalOffersOnlyOneShotChoices(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)

	cases := map[string]struct {
		outcome wire.RequestPermissionOutcome
		want    tools.ApprovalOutcome
	}{
		"允许一次": {
			wire.RequestPermissionOutcome{
				Selected: &wire.RequestPermissionOutcomeSelected{OptionId: optionAllowOnce}},
			tools.ApprovalAllowedOnce,
		},
		"拒绝": {
			wire.RequestPermissionOutcome{
				Selected: &wire.RequestPermissionOutcomeSelected{OptionId: optionRejectOnce}},
			tools.ApprovalRejected,
		},
		// 一个认不出的回答绝不能被折成一份耐久的授权。
		"认不出的选项": {
			wire.RequestPermissionOutcome{
				Selected: &wire.RequestPermissionOutcomeSelected{OptionId: "allow-always"}},
			tools.ApprovalRejected,
		},
		"这一轮被取消了": {
			wire.RequestPermissionOutcome{Cancelled: &wire.RequestPermissionOutcomeCancelled{}},
			tools.ApprovalCancelled,
		},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			f.peer.mutex.Lock()
			f.peer.permission = wire.RequestPermissionResponse{Outcome: each.outcome}
			f.peer.mutex.Unlock()

			got, err := f.bridge.answerApproval(t.Context(), tools.ApprovalRequest{
				Agent: made.scope.Key(), ToolName: "bash", CallID: "c1",
			}, refusingNext(t))
			if err != nil {
				t.Fatalf("问一次不该失败：%v", err)
			}
			if got != each.want {
				t.Fatalf("该是 %s，实际 %s", each.want, got)
			}
		})
	}

	asked := f.peer.requests()
	if len(asked) == 0 {
		t.Fatal("该真的问过对面")
	}
	options := asked[0].Options
	if len(options) != 2 || options[0].OptionId != optionAllowOnce || options[1].OptionId != optionRejectOnce {
		t.Fatalf("只该给一次性的两档，实际 %#v", options)
	}
}

func TestApprovalFallsThroughWhenItHasNothingToAsk(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	stranger := freeAgent(t, "p", "m")

	cases := map[string]tools.ApprovalRequest{
		"不是这座桥名下的 agent": {Agent: stranger.scope.Key(), CallID: "c1"},
		"没有调用标识":         {Agent: made.scope.Key()},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := f.bridge.answerApproval(t.Context(), request, func() (tools.ApprovalOutcome, error) {
				return tools.ApprovalUnavailable, nil
			})
			if err != nil || got != tools.ApprovalUnavailable {
				t.Fatalf("该让给瀑布上的下一条规则，实际 %s / %v", got, err)
			}
		})
	}
}

func TestApprovalReportsAFailedAsk(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	f.peer.mutex.Lock()
	f.peer.permissionFail = errors.New("对面不吭声")
	f.peer.mutex.Unlock()

	_, err := f.bridge.answerApproval(t.Context(), tools.ApprovalRequest{
		Agent: f.factory.only(t).scope.Key(), CallID: "c1",
	}, refusingNext(t))
	if err == nil {
		t.Fatal("问不到人该往上报，不该悄悄当成拒绝")
	}
}

// refusingNext 是一条"不该被走到"的瀑布下一环。
func refusingNext(t *testing.T) func() (tools.ApprovalOutcome, error) {
	t.Helper()
	return func() (tools.ApprovalOutcome, error) {
		t.Error("不该落到瀑布上的下一条规则")
		return tools.ApprovalUnavailable, nil
	}
}

// ---- 不办的那几个 ----

func TestUnservedMethodsAnswerMethodNotFound(t *testing.T) {
	t.Parallel()
	// 回 -32603 会让客户端把「你没有这个方法」当成「你炸了」去重试。
	f := newFixture(t, nil)
	calls := map[string]func() error{
		"session/logout": func() error {
			_, err := f.bridge.Logout(t.Context(), wire.LogoutRequest{})
			return err
		},
		"session/set_mode": func() error {
			_, err := f.bridge.SetSessionMode(t.Context(), wire.SetSessionModeRequest{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertRequestError(t, call(), -32601)
		})
	}
}

func TestPersistenceBackedMethodsAnswerMethodNotFoundWithoutAnArchive(t *testing.T) {
	t.Parallel()
	// 这两项能力跟着持久化走：没挂存档，握手里就不声明，方法本身也必须说「没有」，
	// 而不是回一句「找不到这条会话」——后者会让客户端以为再换一个标识就能成。
	f := newFixture(t, nil)
	calls := map[string]func() error{
		"session/list": func() error {
			cwd := absolutePath
			_, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{Cwd: &cwd})
			return err
		},
		"session/resume": func() error {
			_, err := f.bridge.ResumeSession(t.Context(), wire.ResumeSessionRequest{
				SessionId: "随便", Cwd: absolutePath,
			})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertRequestError(t, call(), -32601)
		})
	}
}

func TestSetConfigOptionAnswersMethodNotFoundWithoutAModelCatalog(t *testing.T) {
	t.Parallel()
	// 没挂 LLM 目录就一个配置项都没摆过，所以「改一个配置项」这件事在这条线上不存在。
	f := newFixture(t, func(config *Config) { config.Models, config.Prompts = nil, nil })
	id := f.newSession(t)
	_, err := f.bridge.SetSessionConfigOption(t.Context(), wire.SetSessionConfigOptionRequest{
		ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: id, ConfigId: modelConfigID, Value: "随便",
		},
	})
	assertRequestError(t, err, -32601)
}

// ---- 收摊 ----

func TestQuiesceTearsDownEverythingItBuiltExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	f.newSession(t)

	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	if live := f.agents.List(); len(live) != 0 {
		t.Fatalf("这座桥建出来的 agent 该全部拆掉，实际还有 %d 个", len(live))
	}
	f.bridge.mutex.Lock()
	remaining, disposers := len(f.bridge.sessions), len(f.bridge.disposers)
	f.bridge.mutex.Unlock()
	if remaining != 0 || disposers != 0 {
		t.Fatalf("会话表和订阅都该清空：%d 个会话、%d 条订阅", remaining, disposers)
	}
	// 第二次只该交回同一个结论，不该再拆一遍。
	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("第二次收摊该交回同一个结论：%v", err)
	}
}

func TestQuiesceSettlesAPromptThatWasStillRunning(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	reached := make(chan struct{})
	made.mutex.Lock()
	made.onFollowup = func(_ *scriptedAgent, _ llm.Message) { close(reached) }
	made.mutex.Unlock()

	settled := make(chan wire.PromptResponse, 1)
	go func() {
		response, err := f.bridge.Prompt(context.Background(), wire.PromptRequest{
			SessionId: id, Prompt: []wire.ContentBlock{wire.TextBlock("喂")}})
		if err != nil {
			t.Errorf("收摊里的一轮该报停止原因，不该报错：%v", err)
		}
		settled <- response
	}()
	<-reached

	if err := f.bridge.Quiesce(context.Background()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	select {
	case response := <-settled:
		if response.StopReason != wire.StopReasonCancelled {
			t.Fatalf("该报 cancelled，实际 %s", response.StopReason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("收摊之后那一轮还挂着")
	}
}

func TestQuiesceDrainsContinuableDescendantsBeforeDisposingParents(t *testing.T) {
	t.Parallel()
	// 可续的子 agent 活得比开出它们的那个回合久：拆掉顶层 agent 之前得先孩子优先地
	// 排干这几棵树，不然会留下一个攥着已经被主人放掉的运行时的后代。
	drain := &recordingDrain{}
	f := newFixture(t, func(config *Config) { config.Subagents = drain })
	f.newSession(t)
	made := f.factory.only(t)

	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	drain.mutex.Lock()
	defer drain.mutex.Unlock()
	if len(drain.calls) != 1 || len(drain.calls[0]) != 1 || drain.calls[0][0] != agent.Agent(made) {
		t.Fatalf("该拿这座桥名下那些顶层 agent 排一次，实际 %#v", drain.calls)
	}
}

func TestQuiesceKeepsGoingWhenTeardownPartlyFails(t *testing.T) {
	t.Parallel()
	// 一次拆解失败不能把后面几步吃掉：那会留下一个还在跑模型和工具的 agent。
	drain := &recordingDrain{fail: errors.New("排不干净")}
	f := newFixture(t, func(config *Config) { config.Subagents = drain })
	f.factory.mutex.Lock()
	f.factory.disposeFail = errors.New("拆不掉")
	f.factory.mutex.Unlock()
	f.newSession(t)

	err := f.bridge.Quiesce(t.Context())
	if err == nil || !strings.Contains(err.Error(), "拆不掉") {
		t.Fatalf("拆解失败该攒起来往上报，实际 %v", err)
	}
	// 后代排干失败只记一行：它改变不了顶层那几个必须被拆掉这件事。
	if live := f.agents.List(); len(live) != 0 {
		t.Fatalf("失败之后顶层 agent 照样该拆掉，实际还有 %d 个", len(live))
	}
}

func TestQuiesceReportsAnAgentThatNeverWentQuiet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.newSession(t)
	made := f.factory.only(t)
	made.mutex.Lock()
	made.idleErr = errors.New("静不下来")
	made.mutex.Unlock()

	err := f.bridge.Quiesce(t.Context())
	if err == nil || !strings.Contains(err.Error(), "静不下来") {
		t.Fatalf("静不下来该攒起来往上报，实际 %v", err)
	}
}

func TestQuiesceWorksWithARealSubagentRuntime(t *testing.T) {
	t.Parallel()
	// 那个收摊钩子的窄口子必须真的被 [subagent.Runtime] 满足。
	subs, err := subagent.NewRuntime(subagent.RuntimeOptions{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("造子 agent 运行时失败：%v", err)
	}
	f := newFixture(t, func(config *Config) { config.Subagents = subs })
	f.newSession(t)
	if err := f.bridge.Quiesce(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
}

// recordingDrain 记下历次后代排干，并且可以被要求失败。
type recordingDrain struct {
	mutex sync.Mutex
	calls [][]agent.Agent
	fail  error
}

func (d *recordingDrain) DrainContinuableDescendants(_ context.Context, parents []agent.Agent) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.calls = append(d.calls, append([]agent.Agent(nil), parents...))
	return d.fail
}

// ---- 装配面的诊断口 ----

func TestConfigFallsBackToTheDefaultLogger(t *testing.T) {
	t.Parallel()
	// 没配 logger 时那几条"报不出去的失败"仍然要有地方落。
	Config{}.warn("这一行落到默认 logger 上")
}

// assertRequestError 断言这是一个带确切 JSON-RPC 错误码的线上错误。
func assertRequestError(t *testing.T, err error, code int) {
	t.Helper()
	var failure *wire.RequestError
	if !errors.As(err, &failure) {
		t.Fatalf("该是一个线上错误，实际 %v", err)
	}
	if failure.Code != code {
		t.Fatalf("错误码该是 %d，实际 %d（%s）", code, failure.Code, failure.Error())
	}
}
