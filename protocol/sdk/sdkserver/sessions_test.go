// 本文件的作用：把会话那十个方法各自在一台真运行时上走一趟，并单独压住那两个
// 从 seq 换算到下标的纯函数。
//
// # 这些测试防的是什么错
//
//   - **握手之前就办得了事**。这十条路里有四条是纯读的，放它们过去等于让这条线的
//     边界取决于方法名。
//   - **分叉切在回合中间**。一个回合收尾之后、下一个回合开张之前的那些事件属于前一个
//     回合，甩掉它们会让分出来的会话缺一截自己继承的历史。
//   - **拿 seq 当下标使**。DSH 那边日志从 0 起、一条不删，两者恒等；本仓库的日志会
//     从最老的一头被弹掉一截，那时差着一个起点——分叉会切错位置，翻页会翻空。
//   - **翻页把一条消息劈成两半**。切点落在那条消息所属那一组的开头，不是消息本身。
//   - **分出来的会话被挪进别的工作区**。它和它的来源谈的是同一件事，分家会让两者在
//     任何一张按工作区筛的表上再也对不上。
//   - **一份没解算过的模型选择被静静装上**。路由不开的话下一个回合才炸，而那时错误
//     已经落在会话历史里了。
//   - **换模型顺手改掉整份部署的默认**。一条线上的一次选择不该让别的每一条线跟着变。
//   - **接着跑一条、分出一条悄悄变成「你已经有一个了」**。撞名必须当场拒。
//   - **叫停、改队、改名把一条落地的会话隐式拉活**。那会为一件对它无事可做的操作起
//     一整套 agent。
//   - **一条标题折不出来的会话把整份列表带塌**。它该只是没有标题。
//   - **一个认不出的排队标签被折成某一支**，于是一次拼错的调用悄悄改掉队里的东西。

package sdkserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/feature/sessiontitle"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/protocol/sdk/sdkprotocol"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 假语料 ----

// fakeCorpus 是一小份「活着的」会话语料，喂给 [sessionquery.Engine]。
//
// 用它而不是那台真的会话存储：这十条路里读侧那四条要的是一份**形状可控**的日志
// （分叉点在哪、头部被弹过没有、标题折不折得出来），而那些形状在真存储上得先把
// 整个 agent 循环跑一遍才凑得出来。
type fakeCorpus struct {
	order   []sessionlog.SessionID
	sources map[sessionlog.SessionID]sessionquery.LogicalSource
}

func newFakeCorpus() *fakeCorpus {
	return &fakeCorpus{sources: map[sessionlog.SessionID]sessionquery.LogicalSource{}}
}

// put 放一条会话进语料。
func (c *fakeCorpus) put(header sessionlog.SessionHeader, events []sessionlog.Event) {
	if _, seen := c.sources[header.ID]; !seen {
		c.order = append(c.order, header.ID)
	}
	c.sources[header.ID] = sessionquery.LogicalSource{Header: header, Events: events}
}

func (c *fakeCorpus) Get(id sessionlog.SessionID) (sessionquery.LogicalSource, bool) {
	source, ok := c.sources[id]
	return source, ok
}

func (c *fakeCorpus) List() []sessionquery.LogicalSource {
	list := make([]sessionquery.LogicalSource, 0, len(c.order))
	for _, id := range c.order {
		list = append(list, c.sources[id])
	}
	return list
}

// fakeSearcher 是一个只按调用记账的检索后端：它把用例事先摆好的那一页原样交回。
//
// 检索本身怎么排、游标怎么走是
// [github.com/snight1983/ds-harness-go/adapter/datastore/searchstore] 那边压的事；
// 这条线上要压的只有「入参有没有原样传下去、那一页有没有原样折成线上的形状」。
type fakeSearcher struct {
	pages    []sessionquery.SearchPage[sessionquery.SearchHit]
	requests []sessionquery.SearchRequest
	fail     error
}

func (s *fakeSearcher) SearchSessions(
	_ context.Context, request sessionquery.SearchRequest,
) (sessionquery.SearchPage[sessionquery.SearchHit], error) {
	s.requests = append(s.requests, request)
	if s.fail != nil {
		return sessionquery.SearchPage[sessionquery.SearchHit]{}, s.fail
	}
	if len(s.pages) == 0 {
		return sessionquery.SearchPage[sessionquery.SearchHit]{}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func (s *fakeSearcher) SearchEvents(
	context.Context, sessionquery.EventSearchRequest,
) (sessionquery.EventSearchPage, error) {
	return sessionquery.EventSearchPage{}, errors.New("这台装配不做会话内检索")
}

// engineOver 在一份语料上开一台查询引擎。
func engineOver(t *testing.T, corpus *fakeCorpus, searcher sessionquery.Searcher) *sessionquery.Engine {
	t.Helper()
	engine, err := sessionquery.New(sessionquery.Options{Live: corpus, Searcher: searcher})
	if err != nil {
		t.Fatalf("造查询引擎失败：%v", err)
	}
	return engine
}

// ---- 日志夹具 ----

// headerOf 排一个会话头。
func headerOf(id sessionlog.SessionID, workspace sessionlog.WorkspaceID) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{Version: 1, ID: id, CreatedAt: 1700, WorkspaceID: workspace}
}

// dataOf 把一份负载排成字节。
func dataOf(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return encoded
}

// turnOf 排一个完整的回合：开张、人说一句、一个步骤、模型答一句、收尾。
//
// 交回那几条事件；第一条的 seq 是 base，第 turn 个回合的编号是 turn。
func turnOf(t *testing.T, base, turn int, ask, answer string) []sessionlog.Event {
	t.Helper()
	at := func(offset int, kind sessionlog.EventType, payload any) sessionlog.Event {
		return sessionlog.Event{
			Type: kind, Seq: base + offset, Time: int64(base+offset) + 1,
			Data: dataOf(t, payload),
		}
	}
	user := at(1, sessionlog.EventUserMessage, sessionlog.UserMessageData{Message: llm.Message{
		ID:      llm.MessageID(fmt.Sprintf("u-%d", turn)),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: ask}},
		Source:  llm.UserSource{},
	}})
	user.SurfaceOp = sessionlog.AppendOp{}
	assistant := at(3, sessionlog.EventAssistantMessage, sessionlog.AssistantMessageData{
		Turn: turn, Step: 1,
		Message: llm.Message{
			ID:      llm.MessageID(fmt.Sprintf("a-%d", turn)),
			Role:    llm.RoleAssistant,
			Content: llm.Content{llm.TextBlock{Text: answer}},
			Source:  llm.ModelSource{Provenance: llm.Provenance{Provider: "known", Model: "m"}},
		},
	})
	assistant.SurfaceOp = sessionlog.AppendOp{}
	return []sessionlog.Event{
		at(0, sessionlog.EventTurnStart, sessionlog.TurnStartData{Turn: turn}),
		user,
		at(2, sessionlog.EventStepStart, sessionlog.StepStartData{Turn: turn, Step: 1}),
		assistant,
		at(4, sessionlog.EventStepEnd, sessionlog.StepEndData{Turn: turn, Step: 1}),
		at(5, sessionlog.EventTurnEnd, sessionlog.TurnEndData{
			Turn: turn, Reason: sessionlog.CompletedTurnEnd{},
		}),
	}
}

// turnsOf 把 count 个回合接成一份日志，第一条的 seq 是 base。
func turnsOf(t *testing.T, base, count int) []sessionlog.Event {
	t.Helper()
	events := make([]sessionlog.Event, 0, count*6)
	for turn := 1; turn <= count; turn++ {
		events = append(events, turnOf(t, base+(turn-1)*6, turn,
			fmt.Sprintf("问第 %d 遍", turn), fmt.Sprintf("答第 %d 遍", turn))...)
	}
	return events
}

// titleEventAt 排一条标题事件。
func titleEventAt(t *testing.T, seq int, title string) sessionlog.Event {
	t.Helper()
	return sessionlog.Event{
		Type: sessiontitle.EventSessionTitle, Seq: seq, Time: int64(seq) + 1,
		Data: dataOf(t, sessiontitle.EventData{Title: title, MessageSeqs: []int{}}),
	}
}

// seqsOf 把一串事件的 seq 抽出来。
func seqsOf(events []sessionlog.Event) []int {
	seqs := make([]int, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}
	return seqs
}

// ---- 装配的几块可选件 ----

// withQueries 把一台查询引擎挂上去。
func withQueries(engine *sessionquery.Engine) func(*Config) {
	return func(config *Config) { config.Queries = engine }
}

// promptsOn 在一个作用域上开一张提示词注册表。
func promptsOn(t *testing.T, owner *scope.Scope) *systemprompt.Registry {
	t.Helper()
	prompts, err := systemprompt.NewRegistry(t.Context(), owner, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	return prompts
}

// ---- 握手之前 ----

// sessionCalls 是这十个方法各自的一次最小调用，用来整批压同一件事。
func sessionCalls() []struct {
	method string
	params any
} {
	return []struct {
		method string
		params any
	}{
		{sdkprotocol.MethodSessionCreate, sdkprotocol.SessionCreateParams{}},
		{sdkprotocol.MethodSessionResume, sdkprotocol.SessionResumeParams{SessionID: "s"}},
		{sdkprotocol.MethodSessionFork, sdkprotocol.SessionForkParams{SessionID: "s"}},
		{sdkprotocol.MethodSessionRename, sdkprotocol.SessionRenameParams{SessionID: "s", Title: "t"}},
		{sdkprotocol.MethodSessionList, sdkprotocol.SessionListParams{}},
		{sdkprotocol.MethodSessionSearch, sdkprotocol.SessionSearchParams{Query: "q"}},
		{sdkprotocol.MethodSessionCancel, sdkprotocol.SessionCancelParams{SessionID: "s"}},
		{sdkprotocol.MethodSessionSelectModel, sdkprotocol.SessionSelectModelParams{
			SessionID: "s", Provider: "known", Model: "m",
		}},
		{sdkprotocol.MethodSessionUpdateQueue, sdkprotocol.SessionUpdateQueueParams{
			SessionID: "s", Op: sdkprotocol.QueueRemove, MessageID: "m-1",
		}},
		{sdkprotocol.MethodSessionHistory, sdkprotocol.SessionHistoryParams{SessionID: "s"}},
	}
}

// TestSessionMethodsRefuseBeforeTheHandshake 钉住握手之前这十条路一条都走不通。
//
// 连纯读的那四条也挡：一个还没握手的客户端根本还不在这条线上，让它读得到东西等于让
// 这条线的边界取决于方法名。
func TestSessionMethodsRefuseBeforeTheHandshake(t *testing.T) {
	t.Parallel()
	server := newLive(t, withQueries(engineOver(t, newFakeCorpus(), nil)))
	for _, call := range sessionCalls() {
		params := dataOf(t, call.params)
		if _, err := server.server.HandleRequest(t.Context(), call.method, params); err == nil {
			t.Errorf("%s 在握手之前该被拒", call.method)
		}
	}
}

// TestHandleRequestRoutesTheSessionTable 钉住这十个方法名派得到、而别的照旧回
// [sdkprotocol.ErrMethodNotFound]。
func TestHandleRequestRoutesTheSessionTable(t *testing.T) {
	t.Parallel()
	server := newLive(t, withQueries(engineOver(t, newFakeCorpus(), nil)))
	server.handshake(t)
	for _, call := range sessionCalls() {
		params := dataOf(t, call.params)
		_, err := server.server.HandleRequest(t.Context(), call.method, params)
		if errors.Is(err, sdkprotocol.ErrMethodNotFound) {
			t.Errorf("%s 该派得到，实际报了「没这个方法」", call.method)
		}
	}
	if _, err := server.server.HandleRequest(
		t.Context(), "session/根本没有这个", nil,
	); !errors.Is(err, sdkprotocol.ErrMethodNotFound) {
		t.Fatalf("认不出的方法名该报「没这个方法」，实际 %v", err)
	}
}

// ---- 开一条 ----

func TestCreateSessionMintsAnIDWhenAbsent(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	result, err := server.server.CreateSession(t.Context(), sdkprotocol.SessionCreateParams{})
	if err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if result.SessionID == "" {
		t.Fatal("没给标识时该起一个")
	}
	created := server.factory.created()
	if len(created) != 1 || string(created[0].SessionID) != result.SessionID {
		t.Fatalf("建出来的会话和交回去的标识对不上：%+v", created)
	}
}

func TestCreateSessionHonoursAGivenID(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	result, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "我自己起的"})
	if err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if result.SessionID != "我自己起的" {
		t.Fatalf("客户端给的标识该被用上，实际 %s", result.SessionID)
	}
}

// TestCreateSessionSharesOneCreationWithPrompt 钉住先建后发和直接发第一轮输入走的是
// 同一条创建路。
//
// 两条路分叉的话，「先建一条再喂」这种用法会拿到一个和别人不一样的会话，而那种差别
// 要到很久以后才现形。
func TestCreateSessionSharesOneCreationWithPrompt(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "同一条"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	server.prompt(t, "同一条")
	if got := len(server.factory.created()); got != 1 {
		t.Fatalf("该只建一次，实际建了 %d 次", got)
	}
}

// ---- 接着跑一条 ----

func TestResumeSessionRunsThroughTheRegistry(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	result, err := server.server.ResumeSession(
		t.Context(), sdkprotocol.SessionResumeParams{SessionID: "落地的那一条"})
	if err != nil {
		t.Fatalf("接着跑不该失败：%v", err)
	}
	if result.SessionID != "落地的那一条" {
		t.Fatalf("活起来的该是同一条会话，实际 %s", result.SessionID)
	}
	resumed := server.factory.resumed()
	if len(resumed) != 1 || string(resumed[0].ResumeSessionID) != "落地的那一条" {
		t.Fatalf("续跑的选项不对：%+v", resumed)
	}
	// 握手那条路由要走到续跑出来的这个 agent 上。
	if resumed[0].AgentOptions.Provider != "known" || resumed[0].AgentOptions.Model != "m" {
		t.Fatalf("这条线的路由没走到续跑上：%+v", resumed[0].AgentOptions)
	}
}

// TestResumeSessionRefusesAnIDAlreadyLive 钉住撞名当场拒，而不是把已有的那一个交出去。
//
// 悄悄交出已有的那一个，等于让「接着跑」在某些时候什么都不做，而调用方以为它重读了
// 一遍日志。
func TestResumeSessionRefusesAnIDAlreadyLive(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "占着了"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if _, err := server.server.ResumeSession(
		t.Context(), sdkprotocol.SessionResumeParams{SessionID: "占着了"}); err == nil {
		t.Fatal("撞名该被拒")
	}
}

func TestResumeSessionNeedsASessionID(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.ResumeSession(
		t.Context(), sdkprotocol.SessionResumeParams{}); err == nil {
		t.Fatal("不给会话标识该被拒")
	}
}

// ---- 分出来 ----

// TestForkCut 把那条切点算法单独压一遍，连同「日志被弹过头」那一支。
func TestForkCut(t *testing.T) {
	t.Parallel()
	// 两个完整回合，seq 从 0 到 11。
	fromZero := turnsOf(t, 0, 2)
	// 同样两个回合，但头部被弹过：seq 从 100 起。
	popped := turnsOf(t, 100, 2)

	cases := []struct {
		name   string
		events []sessionlog.Event
		atSeq  *int
		want   int
	}{
		{"没给分叉点就取最后一个收了尾的回合", fromZero, nil, 12},
		{"给了第一个回合里的一条就切在那个回合收尾之后", fromZero, ptr(1), 6},
		{"给了第一个回合的收尾那一条本身也切在它之后", fromZero, ptr(5), 6},
		{"给了越过末尾的一条就退回最后一个回合", fromZero, ptr(999), 12},
		{"头部被弹过时按 seq 找、按下标切", popped, ptr(101), 6},
		{"头部被弹过时没给分叉点也切得对", popped, nil, 12},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cut, err := forkCut("s", test.events, test.atSeq)
			if err != nil {
				t.Fatalf("算切点不该失败：%v", err)
			}
			if cut != test.want {
				t.Fatalf("切点不对：想要 %d，实际 %d", test.want, cut)
			}
		})
	}
}

// TestForkCutPushesPastTrailingEvents 钉住切点会推过回合收尾之后、下一个回合开张
// 之前的那些事件。
//
// 那些事件（落地下来的工具产物、标题、压缩标记之类）属于**前**一个回合，甩掉它们会
// 让分出来的会话缺一截自己继承的历史。
func TestForkCutPushesPastTrailingEvents(t *testing.T) {
	t.Parallel()
	events := append(turnsOf(t, 0, 1), titleEventAt(t, 6, "回合之间写下的标题"))
	events = append(events, turnOf(t, 7, 2, "再问一遍", "再答一遍")...)
	cut, err := forkCut("s", events, ptr(1))
	if err != nil {
		t.Fatalf("算切点不该失败：%v", err)
	}
	// 6 条回合事件 + 那条标题 = 7，下一条才是第二个回合的开张。
	if cut != 7 {
		t.Fatalf("切点该推到下一个回合开张之前，想要 7，实际 %d", cut)
	}
}

// TestForkCutRefusesAnUnclosedTurn 钉住包住那条事件的回合还没收尾时当场拒。
func TestForkCutRefusesAnUnclosedTurn(t *testing.T) {
	t.Parallel()
	open := turnsOf(t, 0, 1)[:3]
	if _, err := forkCut("s", open, ptr(1)); err == nil {
		t.Fatal("回合还没收尾该被拒")
	}
	if _, err := forkCut("s", nil, nil); err == nil {
		t.Fatal("一个收了尾的回合都没有该被拒")
	}
}

func TestForkSessionSeedsTheNewSessionAndInheritsTheWorkspace(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("来源", "ws-来源的那一个"), turnsOf(t, 0, 2))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	result, err := server.server.ForkSession(t.Context(), sdkprotocol.SessionForkParams{
		SessionID: "来源", AtSeq: ptr(1),
	})
	if err != nil {
		t.Fatalf("分出来不该失败：%v", err)
	}
	if result.SessionID == "" {
		t.Fatal("没给标识时该起一个")
	}
	created := server.factory.created()
	if len(created) != 1 {
		t.Fatalf("该建一条会话，实际建了 %d 条", len(created))
	}
	got := created[0]
	if got.SeedLength != 6 || len(got.Seed) != 6 {
		t.Fatalf("继承的那一段该是第一个回合那 6 条，实际 %d 条", got.SeedLength)
	}
	if got.ParentSession != "来源" {
		t.Fatalf("血统该指向来源，实际 %s", got.ParentSession)
	}
	// 工作区继承来源那一条，不是握手时那一条——两者在这台夹具里刻意不同。
	if got.WorkspaceID != "ws-来源的那一个" {
		t.Fatalf("工作区该继承来源，实际 %s", got.WorkspaceID)
	}
	if got.WorkspaceID == testWorkspaceID {
		t.Fatal("工作区不该取握手时那一条")
	}
}

// TestForkSessionCarriesTheSourceBaseSeq 钉住来源头部被弹过一截时，分出来的会话
// 从来源那个起点数起。
//
// 不带这个起点的话，继承来的那些事件的 seq 会和新会话自己的编号对不上，重放当场就断。
func TestForkSessionCarriesTheSourceBaseSeq(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("弹过头的", "ws-1"), turnsOf(t, 100, 2))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	if _, err := server.server.ForkSession(t.Context(), sdkprotocol.SessionForkParams{
		SessionID: "弹过头的", ForkSessionID: "分出来的",
	}); err != nil {
		t.Fatalf("分出来不该失败：%v", err)
	}
	created := server.factory.created()
	if len(created) != 1 || created[0].BaseSeq != 100 {
		t.Fatalf("起点该是来源那一个，实际 %+v", created)
	}
}

func TestForkSessionNeedsTheQueryEngine(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.ForkSession(
		t.Context(), sdkprotocol.SessionForkParams{SessionID: "s"}); err == nil {
		t.Fatal("没挂查询引擎该被拒")
	}
}

func TestForkSessionRefusesABadRequest(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("来源", "ws-1"), turnsOf(t, 0, 1))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	if _, err := server.server.ForkSession(
		t.Context(), sdkprotocol.SessionForkParams{}); err == nil {
		t.Fatal("不给来源标识该被拒")
	}
	if _, err := server.server.ForkSession(t.Context(), sdkprotocol.SessionForkParams{
		SessionID: "来源", AtSeq: ptr(-1),
	}); err == nil {
		t.Fatal("负数的分叉点该被拒")
	}
	if _, err := server.server.ForkSession(t.Context(), sdkprotocol.SessionForkParams{
		SessionID: "根本没有这一条",
	}); err == nil {
		t.Fatal("读不回来的来源该被拒")
	}
}

// ---- 改名 ----

func TestRenameSessionReportsTheNormalizedTitle(t *testing.T) {
	t.Parallel()
	var renamed []string
	server := newLive(t, func(config *Config) {
		config.Rename = func(
			_ context.Context, target agent.Agent, title string,
		) (sessiontitle.Snapshot, error) {
			renamed = append(renamed, string(target.ID())+"→"+title)
			return sessiontitle.Snapshot{
				EventData: sessiontitle.EventData{Title: "归一化过的"},
				EventSeq:  12,
				UpdatedAt: 1700,
			}, nil
		}
	})
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "要改名的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}

	result, err := server.server.RenameSession(t.Context(), sdkprotocol.SessionRenameParams{
		SessionID: "要改名的", Title: "  人给的名字  ",
	})
	if err != nil {
		t.Fatalf("改名不该失败：%v", err)
	}
	// 交回去的是归一化之后真正落进日志的那一个，不是入参原样。
	if result.Title != "归一化过的" || result.EventSeq != 12 || result.UpdatedAt != 1700 {
		t.Fatalf("改名的回执不对：%+v", result)
	}
	if len(renamed) != 1 || renamed[0] != "要改名的→  人给的名字  " {
		t.Fatalf("标题服务收到的东西不对：%v", renamed)
	}
}

func TestRenameSessionNeedsATitleService(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.RenameSession(t.Context(), sdkprotocol.SessionRenameParams{
		SessionID: "随便哪条", Title: "t",
	}); err == nil {
		t.Fatal("没挂标题服务该被拒")
	}
}

// TestRenameSessionDoesNotResumeImplicitly 钉住改名不会把一条落地的会话拉活。
//
// 隐式的续跑会起一整套 agent，只为了执行一件对它无事可做的操作。
func TestRenameSessionDoesNotResumeImplicitly(t *testing.T) {
	t.Parallel()
	server := newLive(t, func(config *Config) {
		config.Rename = func(context.Context, agent.Agent, string) (sessiontitle.Snapshot, error) {
			return sessiontitle.Snapshot{}, nil
		}
	})
	server.handshake(t)
	if _, err := server.server.RenameSession(t.Context(), sdkprotocol.SessionRenameParams{
		SessionID: "这条线上没有的", Title: "t",
	}); err == nil {
		t.Fatal("这条线上没有的会话该被拒")
	}
	if got := len(server.factory.resumed()); got != 0 {
		t.Fatalf("不该有任何一次续跑，实际 %d 次", got)
	}
}

// ---- 列出来 ----

func TestListSessionsReportsTitlesAndPresence(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	withTitle := append(turnsOf(t, 0, 1), titleEventAt(t, 6, "有名字的那条"))
	corpus.put(headerOf("有标题的", "ws-1"), withTitle)
	corpus.put(headerOf("没标题的", "ws-1"), turnsOf(t, 0, 1))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	result, err := server.server.ListSessions(t.Context(), sdkprotocol.SessionListParams{})
	if err != nil {
		t.Fatalf("列会话不该失败：%v", err)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("该列出两条，实际 %d 条", len(result.Sessions))
	}
	byID := map[string]sdkprotocol.SessionSummary{}
	for _, summary := range result.Sessions {
		byID[summary.SessionID] = summary
	}
	if got := byID["有标题的"]; !got.Titled || got.Title != "有名字的那条" {
		t.Fatalf("有标题的那条不对：%+v", got)
	}
	// 没有标题和空标题是两件事：前者的布尔为假，文本没有意义。
	if got := byID["没标题的"]; got.Titled || got.Title != "" {
		t.Fatalf("没标题的那条不对：%+v", got)
	}
	// 这份语料只有活着的那一侧，所以每一条都该是活的、都没落地。
	for _, summary := range result.Sessions {
		if !summary.Live || summary.Persisted {
			t.Fatalf("在场情况不对：%+v", summary)
		}
	}
}

// TestListSessionsSurvivesAnUnfoldableTitle 钉住一条标题折不出来的会话只是没有标题，
// 而不是把整份列表带塌。
func TestListSessionsSurvivesAnUnfoldableTitle(t *testing.T) {
	t.Parallel()
	broken := turnsOf(t, 0, 1)
	broken = append(broken, sessionlog.Event{
		Type: sessiontitle.EventSessionTitle, Seq: 6, Time: 7,
		Data: json.RawMessage(`{"title":`),
	})
	corpus := newFakeCorpus()
	corpus.put(headerOf("坏标题的", "ws-1"), broken)
	corpus.put(headerOf("好好的", "ws-1"), turnsOf(t, 0, 1))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	result, err := server.server.ListSessions(t.Context(), sdkprotocol.SessionListParams{})
	if err != nil {
		t.Fatalf("一条坏标题不该把整份列表带塌：%v", err)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("两条都该在列表上，实际 %d 条", len(result.Sessions))
	}
	for _, summary := range result.Sessions {
		if summary.Titled {
			t.Fatalf("折不出来的标题该表现成「没有标题」：%+v", summary)
		}
	}
}

func TestListSessionsNeedsTheQueryEngine(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.ListSessions(
		t.Context(), sdkprotocol.SessionListParams{}); err == nil {
		t.Fatal("没挂查询引擎该被拒")
	}
}

// ---- 找 ----

// TestSearchSessionsPagesThroughTheCursor 走完两页，钉住续页令牌原样往返。
func TestSearchSessionsPagesThroughTheCursor(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("第一条", "ws-1"), turnsOf(t, 0, 1))
	corpus.put(headerOf("第二条", "ws-1"), turnsOf(t, 0, 1))
	hitOf := func(id sessionlog.SessionID, seq int, snippet string) sessionquery.SearchHit {
		return sessionquery.SearchHit{
			Record: sessionquery.Record{Header: headerOf(id, "ws-1"), Live: true},
			BestMatch: sessionquery.EventSearchHit{
				EventRecord: sessionquery.EventRecord{SessionID: id, Seq: seq},
				Snippet:     snippet,
			},
		}
	}
	searcher := &fakeSearcher{pages: []sessionquery.SearchPage[sessionquery.SearchHit]{
		{Items: []sessionquery.SearchHit{hitOf("第一条", 1, "命中一")}, NextCursor: "翻下一页"},
		{Items: []sessionquery.SearchHit{hitOf("第二条", 3, "命中二")}},
	}}
	server := newLive(t, withQueries(engineOver(t, corpus, searcher)))
	server.handshake(t)

	first, err := server.server.SearchSessions(t.Context(), sdkprotocol.SessionSearchParams{
		Query: "找这段", Limit: 1,
	})
	if err != nil {
		t.Fatalf("找不该失败：%v", err)
	}
	if len(first.Hits) != 1 || first.Hits[0].Session.SessionID != "第一条" {
		t.Fatalf("第一页不对：%+v", first)
	}
	if first.Hits[0].Seq != 1 || first.Hits[0].Snippet != "命中一" {
		t.Fatalf("命中那一条没折对：%+v", first.Hits[0])
	}
	if first.NextCursor != "翻下一页" {
		t.Fatalf("续页令牌该交回来，实际 %q", first.NextCursor)
	}

	second, err := server.server.SearchSessions(t.Context(), sdkprotocol.SessionSearchParams{
		Query: "找这段", Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("翻第二页不该失败：%v", err)
	}
	if len(second.Hits) != 1 || second.Hits[0].Session.SessionID != "第二条" {
		t.Fatalf("第二页不对：%+v", second)
	}
	// 最后一页不带续页令牌。
	if second.NextCursor != "" {
		t.Fatalf("最后一页不该带续页令牌，实际 %q", second.NextCursor)
	}

	if len(searcher.requests) != 2 {
		t.Fatalf("该问了后端两次，实际 %d 次", len(searcher.requests))
	}
	// 入参原样传下去：那段文字一律当数据看，条数和游标不重写。
	if searcher.requests[0].Query != "找这段" || searcher.requests[0].Limit != 1 {
		t.Fatalf("第一次的入参不对：%+v", searcher.requests[0])
	}
	if string(searcher.requests[1].Cursor) != "翻下一页" {
		t.Fatalf("游标没原样传下去：%q", searcher.requests[1].Cursor)
	}
}

// TestSearchSessionsRefusesWithoutASearchBackend 钉住没挂检索后端时这条路拒，
// 而不是静静地交回空的一页。
func TestSearchSessionsRefusesWithoutASearchBackend(t *testing.T) {
	t.Parallel()
	server := newLive(t, withQueries(engineOver(t, newFakeCorpus(), nil)))
	server.handshake(t)
	_, err := server.server.SearchSessions(
		t.Context(), sdkprotocol.SessionSearchParams{Query: "找这段"})
	if !errors.Is(err, sessionquery.CodeSearchDisabled) {
		t.Fatalf("该报「检索没开」，实际 %v", err)
	}
}

func TestSearchSessionsNeedsTheQueryEngine(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.SearchSessions(
		t.Context(), sdkprotocol.SessionSearchParams{Query: "q"}); err == nil {
		t.Fatal("没挂查询引擎该被拒")
	}
}

// ---- 叫停 ----

func TestCancelSessionCarriesKeepQueue(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "要叫停的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	live, ok := server.agents.Get("要叫停的")
	if !ok {
		t.Fatal("该有一个活着的 agent")
	}

	for _, keep := range []bool{false, true} {
		if _, err := server.server.CancelSession(t.Context(), sdkprotocol.SessionCancelParams{
			SessionID: "要叫停的", KeepQueue: keep,
		}); err != nil {
			t.Fatalf("叫停不该失败：%v", err)
		}
	}
	cancels := live.(*stubAgent).cancelled()
	if len(cancels) != 2 {
		t.Fatalf("该叫停两次，实际 %d 次", len(cancels))
	}
	if cancels[0].options.KeepInbox || !cancels[1].options.KeepInbox {
		t.Fatalf("留着排队那一支没传下去：%+v", cancels)
	}
	// 原因是「人叫停的」，不是别的什么——它会原样落进会话日志。
	if _, ok := cancels[0].cause.(sessionlog.UserCancel); !ok {
		t.Fatalf("叫停的原因该是人叫停，实际 %T", cancels[0].cause)
	}
}

func TestCancelSessionNeedsALiveSession(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.CancelSession(
		t.Context(), sdkprotocol.SessionCancelParams{SessionID: "这条线上没有的"}); err == nil {
		t.Fatal("这条线上没有的会话该被拒")
	}
}

// ---- 换模型 ----

func TestSelectModelInstallsTheResolvedSelection(t *testing.T) {
	t.Parallel()
	var prompts *systemprompt.Registry
	server := newLive(t, func(config *Config) {
		config.LLM = &stubLLM{
			entries: []llm.ProviderInfo{{ID: "known", Name: "known"}},
			// 适配器把自己的默认落实进去：交回去的该是这一份，不是入参原样。
			resolve: func(config llm.CallConfig) (llm.CallConfig, error) {
				config.ReasoningEffort = "适配器补上的档位"
				return config, nil
			},
		}
	})
	// 提示词注册表挂在这台服务器自己那个作用域上。
	prompts = promptsOn(t, server.owner)
	server.server.config.Prompts = prompts
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "换模型的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}

	result, err := server.server.SelectModel(t.Context(), sdkprotocol.SessionSelectModelParams{
		SessionID: "换模型的", Provider: "known", Model: "换成这个",
	})
	if err != nil {
		t.Fatalf("换模型不该失败：%v", err)
	}
	if result.Provider != "known" || result.Model != "换成这个" {
		t.Fatalf("生效的那份选择不对：%+v", result)
	}
	if result.ReasoningEffort != "适配器补上的档位" {
		t.Fatalf("交回去的该是解算过的那一份，实际 %q", result.ReasoningEffort)
	}
	// 装上去的也是解算过的那一份。
	selection, ok := server.server.selectionFor("换模型的")
	if !ok {
		t.Fatal("该有一份模型选择")
	}
	current, present := selection.Current()
	if !present || current.Model != "换成这个" || current.ReasoningEffort != "适配器补上的档位" {
		t.Fatalf("装上去的那份选择不对：%+v", current)
	}
}

// TestSelectModelIsPerSession 钉住一条会话上的一次选择不影响别的会话。
//
// 换成整份部署的默认的话，这条线上一个客户端换了模型，别的每一条线、每一条会话
// 下次都跟着变。
func TestSelectModelIsPerSession(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.server.config.Prompts = promptsOn(t, server.owner)
	server.handshake(t)
	for _, id := range []string{"甲", "乙"} {
		if _, err := server.server.CreateSession(
			t.Context(), sdkprotocol.SessionCreateParams{SessionID: id}); err != nil {
			t.Fatalf("开一条会话不该失败：%v", err)
		}
	}
	if _, err := server.server.SelectModel(t.Context(), sdkprotocol.SessionSelectModelParams{
		SessionID: "甲", Provider: "known", Model: "只给甲的",
	}); err != nil {
		t.Fatalf("换模型不该失败：%v", err)
	}
	other, ok := server.server.selectionFor("乙")
	if !ok {
		t.Fatal("乙也该有一份模型选择")
	}
	if _, present := other.Current(); present {
		t.Fatal("乙那份不该跟着变")
	}
}

// TestSelectModelRefusesAnUnresolvableRoute 钉住解不开的那份选择不会被装上。
//
// 静静装上的话，下一个回合才炸——而那时错误已经落在会话历史里了。
func TestSelectModelRefusesAnUnresolvableRoute(t *testing.T) {
	t.Parallel()
	boom := errors.New("这条路由开不了")
	server := newLive(t, func(config *Config) {
		config.LLM = &stubLLM{
			entries: []llm.ProviderInfo{{ID: "known", Name: "known"}},
			resolve: func(call llm.CallConfig) (llm.CallConfig, error) {
				if call.Model == "开不了的" {
					return llm.CallConfig{}, boom
				}
				return call, nil
			},
		}
	})
	server.server.config.Prompts = promptsOn(t, server.owner)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "换模型的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if _, err := server.server.SelectModel(t.Context(), sdkprotocol.SessionSelectModelParams{
		SessionID: "换模型的", Provider: "known", Model: "开不了的",
	}); !errors.Is(err, boom) {
		t.Fatalf("该原样带出解算那条错，实际 %v", err)
	}
	selection, ok := server.server.selectionFor("换模型的")
	if !ok {
		t.Fatal("该有一份模型选择")
	}
	if _, present := selection.Current(); present {
		t.Fatal("解不开的那份不该被装上")
	}
}

// TestSelectModelNeedsThePromptRegistry 钉住没挂提示词注册表时整条拒，不做半套。
//
// 只改路由会让提示词说 A、请求发给 B。
func TestSelectModelNeedsThePromptRegistry(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "换模型的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if _, err := server.server.SelectModel(t.Context(), sdkprotocol.SessionSelectModelParams{
		SessionID: "换模型的", Provider: "known", Model: "m",
	}); err == nil {
		t.Fatal("没挂提示词注册表该被拒")
	}
}

func TestSelectModelNeedsAnLLMService(t *testing.T) {
	t.Parallel()
	server := newLive(t, func(config *Config) { config.LLM = nil })
	// 没有 LLM 服务时握不了手，所以这里直接把那个开关拨过去，只压这一条路。
	server.server.mutex.Lock()
	server.server.initialized = true
	server.server.mutex.Unlock()
	if _, err := server.server.SelectModel(t.Context(), sdkprotocol.SessionSelectModelParams{
		SessionID: "随便哪条", Provider: "known", Model: "m",
	}); err == nil {
		t.Fatal("没挂 LLM 服务该被拒")
	}
}

// ---- 改排队 ----

// queued 把一条消息排进这个 agent 的收件箱，交回它的身份。
func queued(t *testing.T, live agent.Agent, text string) llm.MessageID {
	t.Helper()
	message := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{})
	if err := live.Inbox().Append(agent.NextTurn, message); err != nil {
		t.Fatalf("排一条消息不该失败：%v", err)
	}
	return message.ID
}

// liveAgentOn 开一条会话并交出它那个 agent。
func liveAgentOn(t *testing.T, server *live, sessionID string) *stubAgent {
	t.Helper()
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: sessionID}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	found, ok := server.agents.Get(sessionlog.SessionID(sessionID))
	if !ok {
		t.Fatalf("该有一个活着的 agent：%s", sessionID)
	}
	return found.(*stubAgent)
}

func TestUpdateQueueRemoves(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	live := liveAgentOn(t, server, "改队的")
	first := queued(t, live, "第一条")
	second := queued(t, live, "第二条")

	result, err := server.server.UpdateQueue(t.Context(), sdkprotocol.SessionUpdateQueueParams{
		SessionID: "改队的", Op: sdkprotocol.QueueRemove, MessageID: first,
	})
	if err != nil {
		t.Fatalf("改队不该失败：%v", err)
	}
	if !slices.Equal(result.NextTurn, []llm.MessageID{second}) {
		t.Fatalf("改完之后那条队不对：%v", result.NextTurn)
	}
	if len(result.NextStep) != 0 {
		t.Fatalf("等步骤那条队该是空的，实际 %v", result.NextStep)
	}
}

func TestUpdateQueueReplacesInPlace(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	live := liveAgentOn(t, server, "改队的")
	first := queued(t, live, "第一条")
	second := queued(t, live, "第二条")

	result, err := server.server.UpdateQueue(t.Context(), sdkprotocol.SessionUpdateQueueParams{
		SessionID:     "改队的",
		Op:            sdkprotocol.QueueReplace,
		MessageID:     first,
		ContentBlocks: sdkprotocol.PromptContent{{Durable: llm.TextBlock{Text: "换成这句"}}},
	})
	if err != nil {
		t.Fatalf("改队不该失败：%v", err)
	}
	// 位置不变是这一支比「拿掉再插一条」多守的那件事：位置决定模型读到它的先后。
	if len(result.NextTurn) != 2 || result.NextTurn[1] != second {
		t.Fatalf("换完之后那条队的次序不对：%v", result.NextTurn)
	}
	if result.NextTurn[0] == first {
		t.Fatal("换上去的该是一条新消息")
	}
}

func TestUpdateQueuePrependsToTheHead(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	live := liveAgentOn(t, server, "改队的")
	first := queued(t, live, "第一条")

	result, err := server.server.UpdateQueue(t.Context(), sdkprotocol.SessionUpdateQueueParams{
		SessionID:     "改队的",
		Op:            sdkprotocol.QueuePrepend,
		ContentBlocks: sdkprotocol.PromptContent{{Durable: llm.TextBlock{Text: "插到队头"}}},
	})
	if err != nil {
		t.Fatalf("改队不该失败：%v", err)
	}
	if len(result.NextTurn) != 2 || result.NextTurn[1] != first {
		t.Fatalf("插到队头之后那条队不对：%v", result.NextTurn)
	}
}

func TestUpdateQueueRefusesABadRequest(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	liveAgentOn(t, server, "改队的")

	cases := []struct {
		name   string
		params sdkprotocol.SessionUpdateQueueParams
	}{
		{"认不出的标签", sdkprotocol.SessionUpdateQueueParams{
			SessionID: "改队的", Op: sdkprotocol.QueueOp("删掉"),
		}},
		{"拿掉不给消息标识", sdkprotocol.SessionUpdateQueueParams{
			SessionID: "改队的", Op: sdkprotocol.QueueRemove,
		}},
		{"原地换不给消息标识", sdkprotocol.SessionUpdateQueueParams{
			SessionID:     "改队的",
			Op:            sdkprotocol.QueueReplace,
			ContentBlocks: sdkprotocol.PromptContent{{Durable: llm.TextBlock{Text: "x"}}},
		}},
		{"插到队头不给内容", sdkprotocol.SessionUpdateQueueParams{
			SessionID: "改队的", Op: sdkprotocol.QueuePrepend,
		}},
		{"这条线上没有的会话", sdkprotocol.SessionUpdateQueueParams{
			SessionID: "没有的", Op: sdkprotocol.QueueRemove, MessageID: "m-1",
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := server.server.UpdateQueue(t.Context(), test.params); err == nil {
				t.Fatal("该被拒")
			}
		})
	}
}

// ---- 翻历史 ----

// TestPaginateHistory 把那条切页算法单独压一遍。
func TestPaginateHistory(t *testing.T) {
	t.Parallel()
	// 三个回合，每个回合一条人的消息加一条模型的消息，seq 从 0 到 17。
	events := turnsOf(t, 0, 3)

	cases := []struct {
		name        string
		beforeSeq   *int
		maxMessages int
		wantFirst   int
		wantLast    int
		wantMore    bool
	}{
		{"数得下整份就从头给", nil, 50, 0, 17, false},
		// 六条消息坐在 1、3、7、9、13、15；从最新那头数满两条停在 13，那一页从 13 起。
		{"数满两条就停在那条消息那一组的开头", nil, 2, 13, 17, true},
		{"翻第 6 条之前的那一页", ptr(6), 50, 0, 5, false},
		// 起点先把 12 之后的整段切掉，剩下 0..11 里数满两条停在 7，前面还剩得下。
		{"翻页起点配上条数", ptr(12), 2, 7, 11, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			page, more := paginateHistory(events, test.beforeSeq, test.maxMessages)
			if len(page) == 0 {
				t.Fatal("这一页不该是空的")
			}
			if page[0].Seq != test.wantFirst || page[len(page)-1].Seq != test.wantLast {
				t.Fatalf("这一页的两头不对：想要 %d..%d，实际 %v",
					test.wantFirst, test.wantLast, seqsOf(page))
			}
			if more != test.wantMore {
				t.Fatalf("「前面还有」不对：想要 %v，实际 %v", test.wantMore, more)
			}
		})
	}
}

// TestPaginateHistoryOnAPoppedLog 钉住头部被弹过一截时翻页仍然按 seq 找、按下标切。
//
// 拿 seq 直接当下标使的话，弹掉多少条就翻空多少条。
func TestPaginateHistoryOnAPoppedLog(t *testing.T) {
	t.Parallel()
	events := turnsOf(t, 100, 3)
	page, more := paginateHistory(events, ptr(106), 50)
	if !slices.Equal(seqsOf(page), []int{100, 101, 102, 103, 104, 105}) {
		t.Fatalf("这一页不对：%v", seqsOf(page))
	}
	if more {
		t.Fatal("已经翻到最老那一头了")
	}
}

func TestPaginateHistoryOnAnEmptyLog(t *testing.T) {
	t.Parallel()
	page, more := paginateHistory(nil, nil, 10)
	if len(page) != 0 || more {
		t.Fatalf("空日志该给空的一页：%v %v", page, more)
	}
}

// TestSessionHistoryPagesBackwards 一路往老里翻到头，钉住每两页都接得上、且不重不漏。
//
// 逐页比对固定的两头挡不住这条错：真正要防的是「翻着翻着回到原地」和「两页之间
// 掉了一段」，那只有把整趟拼回来才看得见。
func TestSessionHistoryPagesBackwards(t *testing.T) {
	t.Parallel()
	full := turnsOf(t, 0, 3)
	corpus := newFakeCorpus()
	corpus.put(headerOf("翻历史的", "ws-1"), full)
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	var walked []int
	pages := 0
	params := sdkprotocol.SessionHistoryParams{SessionID: "翻历史的", MaxMessages: 2}
	for {
		if pages > 10 {
			t.Fatal("翻不到头：这条路没在往老里走")
		}
		page, err := server.server.SessionHistory(t.Context(), params)
		if err != nil {
			t.Fatalf("翻历史不该失败：%v", err)
		}
		if len(page.Events) == 0 {
			t.Fatalf("第 %d 页不该是空的", pages)
		}
		if page.Session.ID != "翻历史的" {
			t.Fatalf("会话头该和这一页出自同一次观察：%+v", page.Session)
		}
		// 新翻出来的这一页更老，接在已经走过的那一段前面。
		walked = append(seqsOf(page.Events), walked...)
		pages++
		if !page.HasMore {
			break
		}
		params.BeforeSeq = ptr(page.Events[0].Seq)
	}
	if pages < 2 {
		t.Fatalf("一页就装完了，这趟没验到翻页：%d 页", pages)
	}
	if !slices.Equal(walked, seqsOf(full)) {
		t.Fatalf("翻完之后拼不回整份日志：%v", walked)
	}
}

// TestSessionHistoryDefaultsTheMaxMessages 钉住不给条数时走的是本包那个缺省。
func TestSessionHistoryDefaultsTheMaxMessages(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("翻历史的", "ws-1"), turnsOf(t, 0, 3))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	result, err := server.server.SessionHistory(
		t.Context(), sdkprotocol.SessionHistoryParams{SessionID: "翻历史的"})
	if err != nil {
		t.Fatalf("翻历史不该失败：%v", err)
	}
	// 三个回合一共六条消息，远在缺省之内，所以整份都在这一页上。
	if len(result.Events) != 18 || result.HasMore {
		t.Fatalf("该一页装下整份：%d 条，前面还有 %v", len(result.Events), result.HasMore)
	}
	if DefaultHistoryMaxMessages != 50 {
		t.Fatalf("缺省条数该是 50，实际 %d", DefaultHistoryMaxMessages)
	}
}

func TestSessionHistoryRefusesABadRequest(t *testing.T) {
	t.Parallel()
	corpus := newFakeCorpus()
	corpus.put(headerOf("翻历史的", "ws-1"), turnsOf(t, 0, 1))
	server := newLive(t, withQueries(engineOver(t, corpus, nil)))
	server.handshake(t)

	if _, err := server.server.SessionHistory(
		t.Context(), sdkprotocol.SessionHistoryParams{}); err == nil {
		t.Fatal("不给会话标识该被拒")
	}
	if _, err := server.server.SessionHistory(t.Context(), sdkprotocol.SessionHistoryParams{
		SessionID: "翻历史的", MaxMessages: -1,
	}); err == nil {
		t.Fatal("负数的条数该被拒")
	}
	if _, err := server.server.SessionHistory(t.Context(), sdkprotocol.SessionHistoryParams{
		SessionID: "根本没有这一条",
	}); err == nil {
		t.Fatal("读不回来的会话该被拒")
	}
}

func TestSessionHistoryNeedsTheQueryEngine(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.handshake(t)
	if _, err := server.server.SessionHistory(
		t.Context(), sdkprotocol.SessionHistoryParams{SessionID: "s"}); err == nil {
		t.Fatal("没挂查询引擎该被拒")
	}
}

// ---- 收摊 ----

// TestShutdownClearsTheModelSelections 钉住收摊把那张模型选择表也清了。
//
// 不清的话，一台服务器上开过的每一条会话都在那张表上留着一份，而它们指着的 agent
// 早就拆掉了。
func TestShutdownClearsTheModelSelections(t *testing.T) {
	t.Parallel()
	server := newLive(t, nil)
	server.server.config.Prompts = promptsOn(t, server.owner)
	server.handshake(t)
	if _, err := server.server.CreateSession(
		t.Context(), sdkprotocol.SessionCreateParams{SessionID: "开过的"}); err != nil {
		t.Fatalf("开一条会话不该失败：%v", err)
	}
	if _, ok := server.server.selectionFor("开过的"); !ok {
		t.Fatal("该有一份模型选择")
	}
	if err := server.server.Shutdown(t.Context()); err != nil {
		t.Fatalf("收摊不该失败：%v", err)
	}
	if _, ok := server.server.selectionFor("开过的"); ok {
		t.Fatal("收摊之后那张表该是空的")
	}
}

// ptr 交出一个指向 value 的指针，给那几个「缺席和零值是两件事」的字段用。
func ptr[V any](value V) *V { return &value }
