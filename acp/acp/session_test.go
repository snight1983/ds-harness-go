// 本文件的作用：会话那四个方法（新建之外的续、列、关、改配置）和跟着它们走的那几条
// 推送——工具与推理更新的转发、LLM 拓扑变动时那一次配置项重推，以及握手摆出来的
// 那几样会话能力。

package acp

import (
	"context"
	"errors"
	"strings"
	"testing"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/mcp"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// archivedFixture 装一座挂了存档的桥：目录和续跑读的是同一份存档。
func archivedFixture(t *testing.T, catalog *fakeCatalog, mutate func(*Config)) *fixture {
	t.Helper()
	f := newFixture(t, func(config *Config) {
		config.Persistence = catalog
		if mutate != nil {
			mutate(config)
		}
	})
	f.factory.mutex.Lock()
	f.factory.archive = catalog
	f.factory.mutex.Unlock()
	return f
}

// ---- 握手摆出来的会话能力 ----

// idleHost 是一台不会被真的连上去的 MCP 宿主：这一组用例只看握手说了什么。
type idleHost struct{}

func (idleHost) Connect(context.Context, *scope.Scope, mcp.Config) (*mcp.Connection, error) {
	return nil, errors.New("这台宿主不接活")
}

func TestCapabilitiesFollowTheCollaboratorsThatAreMounted(t *testing.T) {
	t.Parallel()
	// 握手和方法表必须说同一件事：一个守规矩的客户端只读握手，它照着摆出来的能力去调，
	// 结果撞上 -32601，问题就出在这两处对不上，而不是它调错了。
	bare := newFixture(t, func(config *Config) { config.Persistence, config.MCPServers = nil, nil })
	got := bare.handshake(t).AgentCapabilities
	if got.McpCapabilities.Http {
		t.Fatal("没挂 MCP 宿主就不该声明 mcp.http")
	}
	if got.SessionCapabilities.List != nil || got.SessionCapabilities.Resume != nil {
		t.Fatal("没挂持久化就不该声明 session/list 与 session/resume")
	}
	if got.SessionCapabilities.Close == nil {
		t.Fatal("session/close 不挑协作者，任何时候都该声明")
	}

	full := archivedFixture(t, newCatalog(), func(config *Config) { config.MCPServers = idleHost{} })
	got = full.handshake(t).AgentCapabilities
	if !got.McpCapabilities.Http {
		t.Fatal("挂了 MCP 宿主就该声明 mcp.http")
	}
	if got.McpCapabilities.Sse || got.McpCapabilities.Acp {
		t.Fatal("这一端只搬 http 那一种传输")
	}
	if got.SessionCapabilities.List == nil || got.SessionCapabilities.Resume == nil {
		t.Fatal("挂了持久化就该声明 session/list 与 session/resume")
	}
	if got.LoadSession {
		t.Fatal("session/load 没搬，声明它等于骗对面")
	}
}

// ---- session/list ----

func TestListSessionsPutsTheNewestFirstAndBreaksTiesByIdentity(t *testing.T) {
	t.Parallel()
	// 这条序必须是全序：两条分不出先后的记录会让翻页漏掉一条或者交出两次。
	catalog := newCatalog()
	catalog.put(archived("b", 200, absolutePath))
	catalog.put(archived("a", 200, absolutePath))
	catalog.put(archived("c", 300, absolutePath))
	f := archivedFixture(t, catalog, nil)

	response, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("翻存档不该失败：%v", err)
	}
	got := listedIDs(response)
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("次序该是 c,a,b，实际 %v", got)
	}
	if response.NextCursor != nil {
		t.Fatal("一页装得下就不该给续页游标")
	}
}

func TestListSessionsPagesWithAnOpaqueCursor(t *testing.T) {
	t.Parallel()
	catalog := newCatalog()
	catalog.put(archived("a", 300, absolutePath))
	catalog.put(archived("b", 200, absolutePath))
	catalog.put(archived("c", 100, absolutePath))
	f := archivedFixture(t, catalog, func(config *Config) { config.SessionListPageSize = 2 })

	first, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("翻第一页不该失败：%v", err)
	}
	if got := listedIDs(first); strings.Join(got, ",") != "a,b" {
		t.Fatalf("第一页该是 a,b，实际 %v", got)
	}
	if first.NextCursor == nil {
		t.Fatal("还有第三条时该给续页游标")
	}

	second, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("翻第二页不该失败：%v", err)
	}
	if got := listedIDs(second); strings.Join(got, ",") != "c" {
		t.Fatalf("第二页该是 c，实际 %v", got)
	}
	if second.NextCursor != nil {
		t.Fatal("翻完了就不该再给游标")
	}
}

func TestListSessionsRefusesACursorItDidNotIssue(t *testing.T) {
	t.Parallel()
	// 游标是这一端自己发出去的东西。收一个改过的游标，等于让对面自己挑从哪条开始翻。
	catalog := newCatalog()
	catalog.put(archived("a", 100, absolutePath))
	f := archivedFixture(t, catalog, nil)

	for name, cursor := range map[string]string{
		"不是 base64": "!!!!",
		"解出来不是两元素":  "WzFd",
		"重新编一遍对不上":  "WzEwMC4wLCJhIl0",
		"能解开但结构不对":  "eyJhIjoxfQ",
		"创建时刻是负的":   "Wy0xLCJhIl0",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{Cursor: &cursor})
			assertRequestError(t, err, -32602)
		})
	}
}

func TestListSessionsHidesEverythingThatCannotBeResumed(t *testing.T) {
	t.Parallel()
	catalog := newCatalog()
	catalog.put(archived("ok", 500, absolutePath))
	catalog.put(archived("无工作目录", 400, ""))
	catalog.put(archived("相对工作目录", 300, "relative/path"))
	subagent := archived("子 agent", 200, absolutePath)
	subagent.Origin = sessionlog.OriginSubagent
	catalog.put(subagent)
	parented := archived("有父会话", 100, absolutePath)
	parented.ParentSession = "别人"
	catalog.put(parented)
	f := archivedFixture(t, catalog, nil)

	response, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("翻存档不该失败：%v", err)
	}
	if got := listedIDs(response); strings.Join(got, ",") != "ok" {
		t.Fatalf("只有那一条该露面，实际 %v", got)
	}
}

func TestListSessionsHidesTheOnesThisBridgeIsAlreadyHolding(t *testing.T) {
	t.Parallel()
	// 一条已经在这条连接上开着的会话出现在「可以续跑」的名单里，等于邀请对面在同一段
	// 日志上再开一个 agent。
	catalog := newCatalog()
	f := archivedFixture(t, catalog, nil)
	id := f.newSession(t)
	catalog.put(archived(sessionlog.SessionID(id), 100, absolutePath))

	response, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("翻存档不该失败：%v", err)
	}
	if len(response.Sessions) != 0 {
		t.Fatalf("开着的那一条不该露面，实际 %v", listedIDs(response))
	}
}

func TestListSessionsFiltersByWorkingDirectory(t *testing.T) {
	t.Parallel()
	other := absolutePath + "-别处"
	catalog := newCatalog()
	catalog.put(archived("这里", 200, absolutePath))
	catalog.put(archived("别处", 100, other))
	f := archivedFixture(t, catalog, nil)

	cwd := other
	response, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{Cwd: &cwd})
	if err != nil {
		t.Fatalf("翻存档不该失败：%v", err)
	}
	if got := listedIDs(response); strings.Join(got, ",") != "别处" {
		t.Fatalf("只有那一条该露面，实际 %v", got)
	}

	relative := "relative/path"
	_, err = f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{Cwd: &relative})
	assertRequestError(t, err, -32602)
}

func TestListSessionsReportsAnArchiveItCannotRead(t *testing.T) {
	t.Parallel()
	catalog := newCatalog()
	catalog.listFail = errors.New("存档读不动")
	f := archivedFixture(t, catalog, nil)

	_, err := f.bridge.ListSessions(t.Context(), wire.ListSessionsRequest{})
	assertRequestError(t, err, -32603)
}

// listedIDs 掏出一页里那些会话标识。
func listedIDs(response wire.ListSessionsResponse) []string {
	ids := make([]string, 0, len(response.Sessions))
	for _, info := range response.Sessions {
		ids = append(ids, string(info.SessionId))
	}
	return ids
}

// ---- session/resume ----

func TestResumeSessionBringsAnArchivedSessionBack(t *testing.T) {
	t.Parallel()
	catalog := newCatalog()
	catalog.put(archived("存着的", 100, absolutePath))
	f := archivedFixture(t, catalog, nil)

	response, err := f.bridge.ResumeSession(t.Context(), wire.ResumeSessionRequest{
		SessionId: "存着的", Cwd: absolutePath,
	})
	if err != nil {
		t.Fatalf("续跑不该失败：%v", err)
	}
	if len(response.ConfigOptions) == 0 {
		t.Fatal("挂了 LLM 目录就该把配置项一起交回去")
	}
	if _, live := f.sessions.Get("存着的"); !live {
		t.Fatal("续跑之后那条会话该活着")
	}
	f.bridge.mutex.Lock()
	_, held := f.bridge.sessions["存着的"]
	f.bridge.mutex.Unlock()
	if !held {
		t.Fatal("续跑出来的会话该记进这座桥的表里")
	}
}

func TestResumeSessionAdoptsTheRouteRecordedInTheLog(t *testing.T) {
	t.Parallel()
	// 一条会话上次用的是哪条路由记在它自己的日志里。续跑之后摆给对面看的当前值必须是
	// 那一份，而不是这条线的部署默认——否则对面看到的是一个它从来没点过的模型。
	catalog := newCatalog()
	catalog.put(archived("存着的", 100, absolutePath), requestHeaderEvent(t, llm.CallConfig{
		Provider: "acme", Model: "slow", ReasoningEffort: "high",
	}, llm.CallConfigAdapterDefaults{}))
	f := archivedFixture(t, catalog, func(config *Config) { config.Models = catalogModels() })

	if _, err := f.bridge.ResumeSession(t.Context(), wire.ResumeSessionRequest{
		SessionId: "存着的", Cwd: absolutePath,
	}); err != nil {
		t.Fatalf("续跑不该失败：%v", err)
	}
	got, ok := f.record(t, "存着的").control.Snapshot()
	if !ok {
		t.Fatal("续跑出来的会话该有一份模型选择")
	}
	want := agent.ModelSelection{Provider: "acme", Model: "slow", ReasoningEffort: "high"}
	if got != want {
		t.Fatalf("该按回日志里那份路由 %+v，实际 %+v", want, got)
	}
}

func TestResumeSessionDropsAnAdapterSuppliedReasoningEffort(t *testing.T) {
	t.Parallel()
	// 一个适配器补出来的档位不是这条会话选的。把它按成一次显式选择，对面就再也回不到
	// 「交给提供方」那一档了。
	catalog := newCatalog()
	catalog.put(archived("存着的", 100, absolutePath), requestHeaderEvent(t, llm.CallConfig{
		Provider: "acme", Model: "slow", ReasoningEffort: "high",
	}, llm.CallConfigAdapterDefaults{ReasoningEffort: true}))
	f := archivedFixture(t, catalog, func(config *Config) { config.Models = catalogModels() })

	if _, err := f.bridge.ResumeSession(t.Context(), wire.ResumeSessionRequest{
		SessionId: "存着的", Cwd: absolutePath,
	}); err != nil {
		t.Fatalf("续跑不该失败：%v", err)
	}
	got, _ := f.record(t, "存着的").control.Snapshot()
	if got.ReasoningEffort != "" {
		t.Fatalf("补出来的档位不该留下，实际 %q", got.ReasoningEffort)
	}
}

func TestResumeSessionRefusesWhatItMustNotReopen(t *testing.T) {
	t.Parallel()
	catalog := newCatalog()
	catalog.put(archived("存着的", 100, absolutePath))
	subagent := archived("子 agent", 100, absolutePath)
	subagent.Origin = sessionlog.OriginSubagent
	catalog.put(subagent)
	f := archivedFixture(t, catalog, nil)
	open := f.newSession(t)

	cases := map[string]wire.ResumeSessionRequest{
		"存档里没有":   {SessionId: "没见过", Cwd: absolutePath},
		"子 agent": {SessionId: "子 agent", Cwd: absolutePath},
		"工作目录对不上": {SessionId: "存着的", Cwd: absolutePath + "-别处"},
		"相对工作目录":  {SessionId: "存着的", Cwd: "relative/path"},
		"额外目录": {
			SessionId: "存着的", Cwd: absolutePath, AdditionalDirectories: []string{absolutePath},
		},
		"已经开着": {SessionId: open, Cwd: absolutePath},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.bridge.ResumeSession(t.Context(), params)
			assertRequestError(t, err, -32602)
		})
	}
}

func TestResumeSessionLetsGoOfTheSlotWhenItFails(t *testing.T) {
	t.Parallel()
	// 占位那张表是给「同时来两次续跑」用的。一次失败的续跑不放手，这条会话就永远
	// 续不动了。
	catalog := newCatalog()
	catalog.put(archived("存着的", 100, absolutePath))
	f := archivedFixture(t, catalog, nil)
	f.factory.mutex.Lock()
	f.factory.resumeFail = errors.New("续不起来")
	f.factory.mutex.Unlock()

	_, err := f.bridge.ResumeSession(t.Context(), wire.ResumeSessionRequest{
		SessionId: "存着的", Cwd: absolutePath,
	})
	assertRequestError(t, err, -32603)
	f.bridge.mutex.Lock()
	stuck := len(f.bridge.activating)
	f.bridge.mutex.Unlock()
	if stuck != 0 {
		t.Fatalf("失败之后占位该让出来，实际还占着 %d 个", stuck)
	}
}

// requestHeaderEvent 造一条记着某份路由的请求头事件。
func requestHeaderEvent(
	t *testing.T,
	config llm.CallConfig,
	defaults llm.CallConfigAdapterDefaults,
) sessionlog.Event {
	t.Helper()
	return logEvent(t, sessionlog.EventRequestHeader, sessionlog.RequestHeaderData{
		Header: sessionlog.EpochHeader{Config: config, AdapterDefaults: defaults},
		Reason: sessionlog.HeaderInitial,
	})
}

// ---- session/close ----

func TestCloseSessionTearsDownTheAgentAndForgetsIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	made.settle()

	if _, err := f.bridge.CloseSession(t.Context(), wire.CloseSessionRequest{SessionId: id}); err != nil {
		t.Fatalf("关会话不该失败：%v", err)
	}
	f.bridge.mutex.Lock()
	_, held := f.bridge.sessions[sessionlog.SessionID(id)]
	f.bridge.mutex.Unlock()
	if held {
		t.Fatal("关掉之后这条记录该从表里摘掉")
	}
	if live := f.agents.List(); len(live) != 0 {
		t.Fatalf("这个 agent 该拆掉，实际还有 %d 个", len(live))
	}
	// 关一条已经关掉的会话，回的是「没有这条会话」而不是又走一遍收尾。
	_, err := f.bridge.CloseSession(t.Context(), wire.CloseSessionRequest{SessionId: id})
	assertRequestError(t, err, -32602)
}

func TestCloseSessionCancelsTheTurnItInterrupts(t *testing.T) {
	t.Parallel()
	// 关会话对正在跑的那个回合就是一次取消：对面拿到的必须是 cancelled，不是一句
	// 「这条会话没了」。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)

	started := make(chan struct{})
	made.mutex.Lock()
	made.onFollowup = func(self *scriptedAgent, _ llm.Message) { close(started) }
	made.mutex.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := f.promptText(t, id, "喂")
		done <- err
	}()
	<-started

	if _, err := f.bridge.CloseSession(t.Context(), wire.CloseSessionRequest{SessionId: id}); err != nil {
		t.Fatalf("关会话不该失败：%v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("那条被打断的提示词该正常收尾：%v", err)
	}
	if len(made.cancelled()) == 0 {
		t.Fatal("关会话该把这个 agent 上的活儿停掉")
	}
}

func TestCloseSessionRefusesAPromptThatArrivesTooLate(t *testing.T) {
	t.Parallel()
	// 收尾要等这个 agent 静下来。那段时间里进来的提示词必须当场被拒，不然它会排进一条
	// 马上就要被拆掉的收件箱。
	f := newFixture(t, nil)
	id := f.newSession(t)
	record := f.record(t, id)

	f.bridge.mutex.Lock()
	record.closing = true
	f.bridge.mutex.Unlock()

	_, err := f.promptText(t, id, "喂")
	assertRequestError(t, err, -32602)
}

func TestCloseSessionReportsAnAgentItCannotTearDown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	id := f.newSession(t)
	f.factory.only(t).settle()
	// 处置那个错是在句柄造出来的那一刻抓拍进去的，所以往造法上装已经晚了；直接改这条
	// 记录攥着的那一份。
	f.bridge.mutex.Lock()
	f.bridge.sessions[sessionlog.SessionID(id)].dispose = func(context.Context) error {
		return errors.New("拆不掉")
	}
	f.bridge.mutex.Unlock()

	_, err := f.bridge.CloseSession(t.Context(), wire.CloseSessionRequest{SessionId: id})
	assertRequestError(t, err, -32603)
	f.bridge.mutex.Lock()
	_, held := f.bridge.sessions[sessionlog.SessionID(id)]
	f.bridge.mutex.Unlock()
	if held {
		t.Fatal("收不干净也要摘掉：留着的话对面既用不了也关不掉")
	}
}

// ---- session/set_config_option ----

func TestSetConfigOptionSwitchesTheModel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(config *Config) { config.Models = catalogModels() })
	id := f.newSession(t)

	response, err := f.bridge.SetSessionConfigOption(t.Context(), wire.SetSessionConfigOptionRequest{
		ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: id, ConfigId: modelConfigID, Value: modelValue("acme", "slow"),
		},
	})
	if err != nil {
		t.Fatalf("改模型不该失败：%v", err)
	}
	if len(response.ConfigOptions) == 0 {
		t.Fatal("改完该把整份状态交回去")
	}
	got, _ := f.record(t, id).control.Snapshot()
	if got.Provider != "acme" || got.Model != "slow" {
		t.Fatalf("该换成 acme/slow，实际 %+v", got)
	}
}

func TestSetConfigOptionRefusesWhatIsNotOnTheMenu(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(config *Config) { config.Models = catalogModels() })
	id := f.newSession(t)

	cases := map[string]wire.SetSessionConfigOptionRequest{
		"没摆出来的模型": {ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: id, ConfigId: modelConfigID, Value: modelValue("acme", "没这个"),
		}},
		"认不出的配置项": {ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: id, ConfigId: "随便", Value: "随便",
		}},
		"不认识的会话": {ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: "没见过", ConfigId: modelConfigID, Value: modelValue("acme", "slow"),
		}},
		// 这条线只摆 select 型的两项，一个布尔值请求指不出其中任何一个。
		"布尔值请求": {Boolean: &wire.SetSessionConfigOptionBoolean{SessionId: id, ConfigId: modelConfigID}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := f.bridge.SetSessionConfigOption(t.Context(), params)
			assertRequestError(t, err, -32602)
		})
	}
}

func TestSetConfigOptionRefusesAClosingSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(config *Config) { config.Models = catalogModels() })
	id := f.newSession(t)
	f.bridge.mutex.Lock()
	f.bridge.sessions[sessionlog.SessionID(id)].closing = true
	f.bridge.mutex.Unlock()

	_, err := f.bridge.SetSessionConfigOption(t.Context(), wire.SetSessionConfigOptionRequest{
		ValueId: &wire.SetSessionConfigOptionValueId{
			SessionId: id, ConfigId: modelConfigID, Value: modelValue("acme", "slow"),
		},
	})
	assertRequestError(t, err, -32602)
}

// ---- 拓扑变动时那一次重推 ----

func TestAdaptersUpdatedPushesTheNewConfigOptions(t *testing.T) {
	t.Parallel()
	// 一台适配器被挂上或者摘掉，摆给对面看的那份选项就过期了。不推，对面就会一直
	// 照一份不存在的目录去选。
	box := &observerBox{}
	models := catalogModels()
	models.observed = box
	f := newFixture(t, func(config *Config) { config.Models = models })
	id := f.newSession(t)

	box.notify()
	isConfigPush := func(update wire.SessionNotification) bool {
		return update.Update.ConfigOptionUpdate != nil && update.SessionId == id
	}
	f.waitUpdates(t, 1, isConfigPush)
	f.waitQuiet(f.record(t, id))

	var pushed int
	f.peer.mutex.Lock()
	for _, update := range f.peer.updates {
		if isConfigPush(update) {
			pushed++
		}
	}
	f.peer.mutex.Unlock()
	if pushed != 1 {
		t.Fatalf("该推恰好一条配置项更新，实际 %d 条", pushed)
	}
}

// ---- 工具与推理的转发 ----

func TestAssistantOutputForwardsReasoningAndToolLifecycle(t *testing.T) {
	t.Parallel()
	// 这四样都从已提交的会话事件上来。少任何一样，对面看到的就是一段跳着走的记录。
	f := newFixture(t, nil)
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
		self.appendAll(t,
			assistantEvent(t, 1, llm.Content{
				llm.ReasoningBlock{Text: "想一下"},
				llm.ReasoningBlock{},
				llm.TextBlock{Text: "这就查"},
			}),
			logEvent(t, sessionlog.EventToolCall, sessionlog.ToolCallData{
				Turn: 1, Step: 0, CallID: "调用一号", Name: "grep", Arguments: `{"pattern":"喂"}`,
			}),
			toolResultEvent(t, 1, "调用一号", false),
			logEvent(t, sessionlog.EventTurnEnd, sessionlog.TurnEndData{
				Turn: 1, Reason: sessionlog.CompletedTurnEnd{},
			}),
		)
		self.settle()
	}
	made.mutex.Unlock()

	if _, err := f.promptText(t, id, "喂"); err != nil {
		t.Fatalf("这一轮不该失败：%v", err)
	}

	f.peer.mutex.Lock()
	defer f.peer.mutex.Unlock()
	var thoughts, messages int
	var call *wire.SessionUpdateToolCall
	var result *wire.SessionToolCallUpdate
	for _, update := range f.peer.updates {
		switch {
		case update.Update.AgentThoughtChunk != nil:
			thoughts++
		case update.Update.AgentMessageChunk != nil:
			messages++
		case update.Update.ToolCall != nil:
			call = update.Update.ToolCall
		case update.Update.ToolCallUpdate != nil:
			result = update.Update.ToolCallUpdate
		}
	}
	if thoughts != 1 {
		t.Fatalf("该发一条推理块（空的那一条不发），实际 %d 条", thoughts)
	}
	if messages != 1 {
		t.Fatalf("该发一条助手文本，实际 %d 条", messages)
	}
	if call == nil || call.ToolCallId != "调用一号" || call.Title != "grep" {
		t.Fatalf("工具调用没转出来：%#v", call)
	}
	if result == nil || result.Status == nil || *result.Status != wire.ToolCallStatusCompleted {
		t.Fatalf("工具结果没转出来：%#v", result)
	}
}

func TestFailedToolResultsAreForwardedAsFailed(t *testing.T) {
	t.Parallel()
	// 一次失败的工具调用被报成完成，对面就会以为那一步拿到了它要的东西。
	f := newFixture(t, nil)
	id := f.newSession(t)
	made := f.factory.only(t)
	record := f.record(t, id)

	made.appendAll(t, toolResultEvent(t, 1, "调用一号", true))
	f.waitQuiet(record)

	f.peer.mutex.Lock()
	defer f.peer.mutex.Unlock()
	for _, update := range f.peer.updates {
		if got := update.Update.ToolCallUpdate; got != nil {
			if got.Status == nil || *got.Status != wire.ToolCallStatusFailed {
				t.Fatalf("该报失败，实际 %#v", got.Status)
			}
			return
		}
	}
	t.Fatal("那条工具结果一条更新都没发出去")
}

// toolResultEvent 造一条工具结果事件。
func toolResultEvent(t *testing.T, turn int, callID llm.CallID, failed bool) sessionlog.Event {
	t.Helper()
	built := logEvent(t, sessionlog.EventToolResult, sessionlog.ToolResultData{
		Turn:    turn,
		Message: llm.NewToolResultMessage(callID, textContent("查到了"), failed),
	})
	built.SurfaceOp = sessionlog.AppendOp{}
	return built
}
