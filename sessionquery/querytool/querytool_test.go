// 本文件的作用：把这五件工具身上那条工作区边界、那道失败清洗门、以及交出去的
// 那几行文本钉住。
//
// 逐条对着 DSH 的 tests/tool-session-query.spec.ts 走。
//
// # 这些测试防的是什么错
//
//   - **边界漏了**。一个别的工作目录的会话出现在结果里，哪怕只露出一个 id，
//     也等于把另一个项目的存在告诉了模型。
//   - **越界和「不存在」被说成两句不同的话**。那两句话的差别本身就是情报：
//     模型试几次就能问出某个 id 到底存不存在。
//   - **引擎的内情漏进结果**。后端的错误链里带着路径、SQL、部署细节。
//   - **看不见的那截血统被画成完整的**。一条被截断的父链如果长得和「这就是根」
//     一样，模型会据此下结论说「这个会话没有上文」。
//   - **会话内检索没把当前这一步排掉**。那让模型读到自己刚写下的字，
//     还会把真正在找的旧事件挤出结果。
package querytool_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/sessiontitle"
	"ds-harness-go/sessionquery"
	"ds-harness-go/sessionquery/querytool"
)

// callerCwd 是这些用例里调用方那个工作目录。
//
// 会话头要求工作目录是绝对路径，而「绝对」在 Windows 上要带盘符，所以这两个
// 值现算，不写字面量——写死一个 /work/project 只在类 Unix 上过得去。
var callerCwd = absDir("workspace-project")

// otherCwd 是边界另一侧那个工作目录。
var otherCwd = absDir("workspace-elsewhere")

// absDir 把一个名字变成当前平台上的绝对路径。这两个目录不需要真的存在：
// 本包只拿工作目录做字符串比较。
func absDir(name string) string {
	path, err := filepath.Abs(name)
	if err != nil {
		panic(err)
	}
	return path
}

// stubAgent 是一个只把会话摆在那儿的假 agent。
//
// 这些用例只用得到 Session()，别的方法在这里全是哑的：本包除了「调用方是谁、
// 它的会话头长什么样、它的日志里有没有 step/start」之外不碰 agent 的任何东西。
type stubAgent struct {
	id   session.SessionID
	own  *scope.Scope
	sess *coresession.Session
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return a.sess }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubService 是一台按用例摆好的假读引擎。
//
// 每个字段是一次调用的答案；nil 表示这个用例不该走到那条路上，走到了就当场失败。
// 这比返回零值好：一次本不该发生的引擎调用是本包最危险的一类 bug（它意味着
// 授权没拦住），零值会让它悄悄过去。
type stubService struct {
	t *testing.T

	sessions map[session.SessionID]sessionquery.Record
	titles   map[session.SessionID]string
	pages    []sessionquery.SearchPage[sessionquery.SearchHit]
	events   []sessionquery.EventSearchPage
	lineage  *sessionquery.LineageTrace
	trace    *sessionquery.EventTraceObservation
	window   *sessionquery.EventWindow

	// searchErr 非 nil 时跨会话检索当场失败。
	searchErr error
	// titleErr 是某个会话读标题时的失败。
	titleErr map[session.SessionID]error

	// filterCalls 记下每一次 FilterSessions 收到的过滤器，用来验授权确实问了。
	filterCalls [][]sessionquery.SessionFilter
	// searchCalls 记下每一次跨会话检索的请求。
	searchCalls []sessionquery.SearchRequest
	// eventCalls 记下每一次会话内检索的请求。
	eventCalls []sessionquery.EventSearchRequest
}

func (s *stubService) FilterSessions(
	_ context.Context,
	filters []sessionquery.SessionFilter,
) ([]sessionquery.Record, error) {
	s.filterCalls = append(s.filterCalls, filters)
	var ids []session.SessionID
	var cwds []string
	for _, filter := range filters {
		switch typed := filter.(type) {
		case sessionquery.IDFilter:
			ids = typed.Values
		case sessionquery.CwdFilter:
			cwds = typed.Values
		}
	}
	var matched []sessionquery.Record
	for _, id := range ids {
		record, ok := s.sessions[id]
		if !ok {
			continue
		}
		for _, cwd := range cwds {
			if record.Header.Cwd == cwd {
				matched = append(matched, record)
				break
			}
		}
	}
	return matched, nil
}

func (s *stubService) SearchSessions(
	_ context.Context,
	request sessionquery.SearchRequest,
) (sessionquery.SearchPage[sessionquery.SearchHit], error) {
	s.searchCalls = append(s.searchCalls, request)
	if s.searchErr != nil {
		return sessionquery.SearchPage[sessionquery.SearchHit]{}, s.searchErr
	}
	index := 0
	if request.Cursor != "" {
		for position, page := range s.pages {
			if page.NextCursor == request.Cursor {
				index = position + 1
				break
			}
		}
	}
	if index >= len(s.pages) {
		return sessionquery.SearchPage[sessionquery.SearchHit]{}, nil
	}
	return s.pages[index], nil
}

func (s *stubService) SearchEvents(
	_ context.Context,
	request sessionquery.EventSearchRequest,
) (sessionquery.EventSearchPage, error) {
	s.eventCalls = append(s.eventCalls, request)
	index := 0
	if request.Cursor != "" {
		for position, page := range s.events {
			if page.NextCursor == request.Cursor {
				index = position + 1
				break
			}
		}
	}
	if index >= len(s.events) {
		// 一页都没摆的用例照样要拿到目标会话那份头：真引擎哪怕一条都没命中
		// 也会把它交回来，而本包会拿它复核一遍授权。回一份空头等于假装
		// 这个会话不在任何工作区里。
		return sessionquery.EventSearchPage{Session: s.sessions[request.SessionID].Header}, nil
	}
	return s.events[index], nil
}

func (s *stubService) TraceSession(_ context.Context, id session.SessionID) (sessionquery.LineageTrace, error) {
	if s.lineage == nil {
		s.t.Fatalf("这个用例不该追溯血统，却追了 %s", id)
	}
	return *s.lineage, nil
}

func (s *stubService) TraceEvent(
	_ context.Context,
	request sessionquery.EventTraceRequest,
) (sessionquery.EventTraceObservation, error) {
	if s.trace == nil {
		s.t.Fatalf("这个用例不该追溯事件，却追了 %s#%d", request.SessionID, request.Seq)
	}
	return *s.trace, nil
}

func (s *stubService) ReadEvent(
	_ context.Context,
	request sessionquery.EventReadRequest,
) (sessionquery.EventWindow, error) {
	if s.window == nil {
		s.t.Fatalf("这个用例不该精读事件，却读了 %s#%d", request.SessionID, request.Seq)
	}
	return *s.window, nil
}

func (s *stubService) ReadTitleSnapshots(
	_ context.Context,
	ids []session.SessionID,
) ([]sessionquery.ProjectionResult[sessionquery.TitleObservation], error) {
	results := make([]sessionquery.ProjectionResult[sessionquery.TitleObservation], 0, len(ids))
	seen := map[session.SessionID]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err, failed := s.titleErr[id]; failed {
			results = append(results, sessionquery.ProjectionResult[sessionquery.TitleObservation]{
				SessionID: id,
				Err:       err,
			})
			continue
		}
		record, ok := s.sessions[id]
		if !ok {
			results = append(results, sessionquery.ProjectionResult[sessionquery.TitleObservation]{
				SessionID: id,
				Err:       &sessionquery.Error{Code: sessionquery.CodeSessionNotFound, Message: string(id)},
			})
			continue
		}
		title, titled := s.titles[id]
		results = append(results, sessionquery.ProjectionResult[sessionquery.TitleObservation]{
			SessionID: id,
			Value: sessionquery.TitleObservation{
				Session: record.Header,
				Title:   sessiontitle.Snapshot{EventData: sessiontitle.EventData{Title: title}},
				Titled:  titled,
			},
		})
	}
	return results, nil
}

// world 是一次用例要的全部家当。
type world struct {
	t          *testing.T
	root       *scope.Scope
	service    *stubService
	controller *querytool.Controller
	agent      *stubAgent
	tools      *tools.Runtime
	prompts    *systemprompt.Registry
}

// newWorld 造一个调用方在 [callerCwd] 里的世界。
func newWorld(t *testing.T, service *stubService) *world {
	t.Helper()
	service.t = t
	ctx := t.Context()

	root, err := scope.New(scope.NewKey("root"), scope.Options{})
	if err != nil {
		t.Fatalf("造根作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = root.Dispose(context.Background()) })

	agentScope, err := scope.New(scope.NewKey("agent"), scope.Options{Parent: root.Key()})
	if err != nil {
		t.Fatalf("造 agent 作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = agentScope.Dispose(context.Background()) })

	const id session.SessionID = "caller"
	sess, err := coresession.NewSession(id, coresession.Options{
		Header: &session.SessionHeader{ID: id, Cwd: callerCwd},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	stub := &stubAgent{id: id, own: agentScope, sess: sess}
	if service.sessions == nil {
		service.sessions = map[session.SessionID]sessionquery.Record{}
	}
	service.sessions[id] = sessionquery.Record{Header: sess.Header(), Live: true}

	controller, err := querytool.New(querytool.Config{
		Service: service,
		AgentOf: func(key *scope.Key) (agent.Agent, error) {
			if key == agentScope.Key() {
				return stub, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}

	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具注册表失败：%v", err)
	}
	prompts, err := systemprompt.NewRegistry(ctx, root, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	return &world{
		t: t, root: root, service: service, controller: controller,
		agent: stub, tools: runtime, prompts: prompts,
	}
}

// install 把五件工具和那段指引装上去。
func (w *world) install() {
	w.t.Helper()
	undo, err := w.controller.Install(w.t.Context(), w.root, querytool.Deps{
		Tools: w.tools, Prompts: w.prompts,
	})
	if err != nil {
		w.t.Fatalf("装控制器失败：%v", err)
	}
	w.t.Cleanup(func() { _ = undo(context.Background()) })
}

// call 调一件工具，交回它那段文本。
func (w *world) call(name string, args any) (string, error) {
	w.t.Helper()
	w.install()
	definition, ok := w.tools.Get(name, w.root.Key())
	if !ok {
		w.t.Fatalf("工具表里没有 %s", name)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		w.t.Fatalf("排参数失败：%v", err)
	}
	value, err := definition.Execute(w.t.Context(), encoded, &tools.RunContext{
		Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{Agent: w.agent.own.Key()}},
	})
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		w.t.Fatalf("解结果失败：%v", err)
	}
	return text, nil
}

// appendStepStart 往调用方日志里写一条 turn/start 加一条 step/start。
//
// 回合那条不能省。seq 恒等于日志长度，光写 step/start 会让它落在 seq 0 上，
// 于是上界被钉成 -1、被参数校验挡下来——而真实日志里步骤永远开在回合之内，
// 这个形状测不到任何东西。
func (w *world) appendStepStart(turn, step int) {
	w.t.Helper()
	w.appendEvent(session.EventTurnStart, map[string]int{"turn": turn})
	w.appendEvent(session.EventStepStart, map[string]int{"turn": turn, "step": step})
}

// appendEvent 往调用方日志里追加一条事件。
func (w *world) appendEvent(kind session.EventType, payload any) {
	w.t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		w.t.Fatalf("排 %s 负载失败：%v", kind, err)
	}
	if _, err := w.agent.sess.Append(session.Event{Type: kind, Data: data}); err != nil {
		w.t.Fatalf("追加 %s 失败：%v", kind, err)
	}
}

// record 造一条会话记录。
func record(id session.SessionID, cwd string, parent session.SessionID) sessionquery.Record {
	return sessionquery.Record{
		Header: session.SessionHeader{ID: id, Cwd: cwd, ParentSession: parent},
		Live:   true,
	}
}

// hit 造一条跨会话命中。
func hit(record sessionquery.Record, seq int, snippet string) sessionquery.SearchHit {
	return sessionquery.SearchHit{
		Record: record,
		BestMatch: sessionquery.EventSearchHit{
			EventRecord: sessionquery.EventRecord{
				SessionID: record.Header.ID,
				Seq:       seq,
				Type:      session.EventStepStart,
				Surface:   sessionquery.SurfaceCurrent,
			},
			Snippet: snippet,
		},
	}
}

// TestCrossSessionSearchKeepsTheWorkspaceBoundary 钉住那条边界：别的工作目录的
// 会话即使被引擎交上来，也一个字都不许出现在结果里。
func TestCrossSessionSearchKeepsTheWorkspaceBoundary(t *testing.T) {
	inside := record("inside", callerCwd, "")
	outside := record("outside", otherCwd, "")
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{"inside": inside, "outside": outside},
		titles:   map[session.SessionID]string{"inside": "Inside work"},
		pages: []sessionquery.SearchPage[sessionquery.SearchHit]{{
			Items: []sessionquery.SearchHit{hit(inside, 3, "alpha"), hit(outside, 4, "beta")},
		}},
	}
	w := newWorld(t, service)
	text, err := w.call("session_search", map[string]any{"query": "alpha"})
	if err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if !strings.Contains(text, "inside") {
		t.Fatalf("结果里没有工作区内那个会话：\n%s", text)
	}
	if strings.Contains(text, "outside") || strings.Contains(text, "beta") {
		t.Fatalf("越界的会话漏进了结果：\n%s", text)
	}
	if !strings.Contains(text, "Session search results (1):") {
		t.Fatalf("结果条数不对：\n%s", text)
	}
}

// TestCrossSessionSearchNeedsACallerWorkspace 钉住那条前提：没有工作目录的
// 调用方根本用不了这件工具，而不是退化成一次无边界的全库检索。
func TestCrossSessionSearchNeedsACallerWorkspace(t *testing.T) {
	service := &stubService{}
	w := newWorld(t, service)
	// 换一个没有工作目录的会话头。
	sess, err := coresession.NewSession("caller", coresession.Options{
		Header: &session.SessionHeader{ID: "caller"},
	})
	if err != nil {
		t.Fatalf("造无 cwd 会话失败：%v", err)
	}
	w.agent.sess = sess

	_, err = w.call("session_search", map[string]any{"query": "alpha"})
	if err == nil {
		t.Fatal("一个没有工作目录的调用方竟然搜成了")
	}
	if !errors.Is(err, querytool.CodeUnauthorized) {
		t.Fatalf("报的码不对：%v", err)
	}
	if len(service.searchCalls) != 0 {
		t.Fatalf("越界的调用竟然打到了引擎：%d 次", len(service.searchCalls))
	}
}

// TestCrossSessionSearchExcludesTheCallerSession 钉住「当前会话不进结果」。
func TestCrossSessionSearchExcludesTheCallerSession(t *testing.T) {
	caller := record("caller", callerCwd, "")
	service := &stubService{
		pages: []sessionquery.SearchPage[sessionquery.SearchHit]{{
			Items: []sessionquery.SearchHit{hit(caller, 1, "self")},
		}},
	}
	w := newWorld(t, service)
	text, err := w.call("session_search", map[string]any{"query": "self"})
	if err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if text != "No prior session matches found." {
		t.Fatalf("当前会话没被排掉：\n%s", text)
	}
}

// TestCrossSessionSearchHidesOutOfWorkspaceParents 钉住那条「父亲在界外」：
// 它必须说出来，而不是画成「这是一个根会话」。
func TestCrossSessionSearchHidesOutOfWorkspaceParents(t *testing.T) {
	child := record("child", callerCwd, "hidden")
	hidden := record("hidden", otherCwd, "")
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{"child": child, "hidden": hidden},
		titles:   map[session.SessionID]string{"child": "Child"},
		pages: []sessionquery.SearchPage[sessionquery.SearchHit]{{
			Items: []sessionquery.SearchHit{hit(child, 2, "gamma")},
		}},
	}
	w := newWorld(t, service)
	text, err := w.call("session_search", map[string]any{"query": "gamma"})
	if err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if !strings.Contains(text, "Parent: [outside workspace]") {
		t.Fatalf("界外的父亲没说出来：\n%s", text)
	}
	if strings.Contains(text, "hidden") {
		t.Fatalf("界外父亲的 id 漏了出去：\n%s", text)
	}
}

// TestCrossSessionSearchStopsAtTheResultCap 钉住上限，以及撞上限时那句话。
func TestCrossSessionSearchStopsAtTheResultCap(t *testing.T) {
	first := record("one", callerCwd, "")
	second := record("two", callerCwd, "")
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{"one": first, "two": second},
		pages: []sessionquery.SearchPage[sessionquery.SearchHit]{{
			Items: []sessionquery.SearchHit{hit(first, 1, "a"), hit(second, 2, "b")},
		}},
	}
	w := newWorld(t, service)
	controller, err := querytool.New(querytool.Config{
		Service:          service,
		AgentOf:          func(*scope.Key) (agent.Agent, error) { return w.agent, nil },
		MaxSearchResults: 1,
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	w.controller = controller
	text, err := w.call("session_search", map[string]any{"query": "a"})
	if err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if !strings.Contains(text, "Session search results (1):") {
		t.Fatalf("上限没生效：\n%s", text)
	}
	if !strings.Contains(text, "Result cap reached.") {
		t.Fatalf("撞上限那句话没说：\n%s", text)
	}
}

// TestEngineFailuresAreSanitized 钉住那道清洗门：引擎错误的原文一个字都不许
// 出现在交给模型的那句话里。
func TestEngineFailuresAreSanitized(t *testing.T) {
	service := &stubService{
		searchErr: &sessionquery.Error{
			Code:    sessionquery.CodeIndexFailed,
			Message: "/var/lib/deploy/secret.db is locked",
		},
	}
	w := newWorld(t, service)
	_, err := w.call("session_search", map[string]any{"query": "alpha"})
	if err == nil {
		t.Fatal("引擎砸了，工具竟然成功了")
	}
	if strings.Contains(err.Error(), "secret.db") {
		t.Fatalf("引擎内情漏进了结果：%v", err)
	}
	if !strings.Contains(err.Error(), "session search index is unavailable") {
		t.Fatalf("没换成那句安全的话：%v", err)
	}
}

// TestUnknownEngineFailuresCollapse 钉住那两条塌成通用失败的码：说给模型听
// 既没用、又暴露部署内情。
func TestUnknownEngineFailuresCollapse(t *testing.T) {
	service := &stubService{
		searchErr: &sessionquery.Error{
			Code:    sessionquery.CodeInvalidConfig,
			Message: "searchProvider was not configured",
		},
	}
	w := newWorld(t, service)
	_, err := w.call("session_search", map[string]any{"query": "alpha"})
	if err == nil {
		t.Fatal("引擎砸了，工具竟然成功了")
	}
	if !errors.Is(err, querytool.CodeFailed) {
		t.Fatalf("没塌成通用失败：%v", err)
	}
	if strings.Contains(err.Error(), "searchProvider") {
		t.Fatalf("装配内情漏了出去：%v", err)
	}
}

// TestEventSearchExcludesTheCurrentStep 钉住那道上界：检索自己的会话时，
// 上界被钉在当前这一步开始之前。
func TestEventSearchExcludesTheCurrentStep(t *testing.T) {
	service := &stubService{titles: map[session.SessionID]string{"caller": "Caller"}}
	w := newWorld(t, service)
	w.appendStepStart(1, 1)
	events := w.agent.sess.Events()
	stepSeq := events[len(events)-1].Seq

	if _, err := w.call("session_event_search", map[string]any{"query": "alpha"}); err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if len(service.eventCalls) != 1 {
		t.Fatalf("引擎被调了 %d 次", len(service.eventCalls))
	}
	var bound *int64
	for _, filter := range service.eventCalls[0].Filters {
		if seq, ok := filter.(sessionquery.SeqFilter); ok {
			bound = seq.To
		}
	}
	if bound == nil {
		t.Fatal("没有钉上界")
	}
	if *bound != int64(stepSeq-1) {
		t.Fatalf("上界钉错了：要 %d，拿到 %d", stepSeq-1, *bound)
	}
}

// TestEventSearchNeedsAStepBoundary 钉住那条前提：一个还没开过步骤的会话
// 不许检索自己——退化成「全都能搜」正好是上面那条要防的事。
func TestEventSearchNeedsAStepBoundary(t *testing.T) {
	service := &stubService{}
	w := newWorld(t, service)
	_, err := w.call("session_event_search", map[string]any{"query": "alpha"})
	if err == nil {
		t.Fatal("没有步骤边界竟然搜成了")
	}
	if !errors.Is(err, querytool.CodeNoCurrentStep) {
		t.Fatalf("报的码不对：%v", err)
	}
}

// TestTargetingAnotherWorkspaceIsRefusedWithoutDisclosure 钉住那句统一的越界话：
// 它既不说那个会话存不存在，也不说它属于谁。
func TestTargetingAnotherWorkspaceIsRefusedWithoutDisclosure(t *testing.T) {
	for _, name := range []string{"session_trace", "session_event_trace", "session_event_read"} {
		t.Run(name, func(t *testing.T) {
			service := &stubService{
				sessions: map[session.SessionID]sessionquery.Record{
					"outside": record("outside", otherCwd, ""),
				},
			}
			w := newWorld(t, service)
			args := map[string]any{"session_id": "outside", "seq": 1}
			_, err := w.call(name, args)
			if err == nil {
				t.Fatal("越界的目标竟然读成了")
			}
			if !errors.Is(err, querytool.CodeUnauthorized) {
				t.Fatalf("报的码不对：%v", err)
			}
			if strings.Contains(err.Error(), otherCwd) {
				t.Fatalf("别人的工作目录漏了出去：%v", err)
			}
		})
	}
}

// TestAMissingSessionLooksExactlyLikeAnOutOfWorkspaceOne 钉住那件最要紧的事：
// 「不存在」和「在界外」必须是同一句话。
func TestAMissingSessionLooksExactlyLikeAnOutOfWorkspaceOne(t *testing.T) {
	outside := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{
			"target": record("target", otherCwd, ""),
		},
	}
	missing := &stubService{}

	_, outsideErr := newWorld(t, outside).call("session_trace", map[string]any{"session_id": "target"})
	_, missingErr := newWorld(t, missing).call("session_trace", map[string]any{"session_id": "target"})
	if outsideErr == nil || missingErr == nil {
		t.Fatal("两种情形里有一种竟然成功了")
	}
	if outsideErr.Error() != missingErr.Error() {
		t.Fatalf("两句话不一样，模型据此就能问出会话存不存在：\n%q\n%q", outsideErr, missingErr)
	}
}

// TestSessionTraceMarksTheAncestorBoundary 钉住那条「往上还有，但你看不见」：
// 它和「这就是根」必须分开。
func TestSessionTraceMarksTheAncestorBoundary(t *testing.T) {
	target := record("target", callerCwd, "parent")
	visible := record("parent", callerCwd, "hidden")
	hiddenAncestor := record("hidden", otherCwd, "")
	trace := sessionquery.LineageTrace{
		Target:    target,
		Ancestors: []sessionquery.Record{visible, hiddenAncestor},
		Complete:  true,
	}
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{
			"target": target, "parent": visible, "hidden": hiddenAncestor,
		},
		titles:  map[session.SessionID]string{"target": "Target", "parent": "Parent"},
		lineage: &trace,
	}
	w := newWorld(t, service)
	text, err := w.call("session_trace", map[string]any{"session_id": "target"})
	if err != nil {
		t.Fatalf("追溯失败：%v", err)
	}
	if !strings.Contains(text, "- [outside workspace boundary]") {
		t.Fatalf("边界没画出来：\n%s", text)
	}
	if strings.Contains(text, "hidden") {
		t.Fatalf("界外祖先的 id 漏了出去：\n%s", text)
	}
	if strings.Contains(text, "- none (target is a root session)") {
		t.Fatalf("一条被截断的血统被画成了根：\n%s", text)
	}
}

// TestSessionTraceMarksAnIncompleteLineage 钉住第二种「往上还有」：引擎自己
// 就没追到根。它同样不许被画成「这就是根」。
func TestSessionTraceMarksAnIncompleteLineage(t *testing.T) {
	target := record("target", callerCwd, "gone")
	trace := sessionquery.LineageTrace{
		Target:             target,
		Complete:           false,
		UnresolvedParentID: "gone",
	}
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{"target": target},
		titles:   map[session.SessionID]string{"target": "Target"},
		lineage:  &trace,
	}
	w := newWorld(t, service)
	text, err := w.call("session_trace", map[string]any{"session_id": "target"})
	if err != nil {
		t.Fatalf("追溯失败：%v", err)
	}
	if !strings.Contains(text, "- [outside workspace boundary]") {
		t.Fatalf("追不到根这件事没说出来：\n%s", text)
	}
	if strings.Contains(text, "- none (target is a root session)") {
		t.Fatalf("一条没追到根的血统被画成了根：\n%s", text)
	}
}

// TestSessionTracePrunesOutOfWorkspaceSubtrees 钉住那个 nil 占位：越界的一支
// 要留一句「这里有东西，你看不到」，不能删掉。
func TestSessionTracePrunesOutOfWorkspaceSubtrees(t *testing.T) {
	target := record("target", callerCwd, "")
	child := record("child", callerCwd, "target")
	stranger := record("stranger", otherCwd, "target")
	trace := sessionquery.LineageTrace{
		Target:   target,
		Complete: true,
		Root:     target,
		Descendants: []sessionquery.LineageNode{
			{Session: child},
			{Session: stranger, Descendants: []sessionquery.LineageNode{{Session: record("grand", callerCwd, "stranger")}}},
		},
	}
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{
			"target": target, "child": child, "stranger": stranger,
		},
		titles:  map[session.SessionID]string{"target": "Target", "child": "Child"},
		lineage: &trace,
	}
	w := newWorld(t, service)
	text, err := w.call("session_trace", map[string]any{"session_id": "target"})
	if err != nil {
		t.Fatalf("追溯失败：%v", err)
	}
	if !strings.Contains(text, "- [outside workspace subtree]") {
		t.Fatalf("被剪掉的那一支没留占位：\n%s", text)
	}
	if strings.Contains(text, "stranger") || strings.Contains(text, "grand") {
		t.Fatalf("界外子树的 id 漏了出去：\n%s", text)
	}
	if !strings.Contains(text, "child") {
		t.Fatalf("界内的后代不见了：\n%s", text)
	}
}

// TestEventReadEmitsTheTargetVerbatim 钉住这件工具存在的全部理由：目标那条是
// 原样的 JSON，任何摘要都会把模型想找的那个字段摘掉。
func TestEventReadEmitsTheTargetVerbatim(t *testing.T) {
	target := record("caller", callerCwd, "")
	window := sessionquery.EventWindow{
		Session: target.Header,
		Target: session.Event{
			Seq: 7, Type: session.EventStepStart, Time: 1700000000000,
			Data: json.RawMessage(`{"turn":2,"step":1}`),
		},
		StartSeq: 7, EndSeq: 7,
	}
	service := &stubService{
		titles: map[session.SessionID]string{"caller": "Caller"},
		window: &window,
	}
	w := newWorld(t, service)
	text, err := w.call("session_event_read", map[string]any{"seq": 7})
	if err != nil {
		t.Fatalf("精读失败：%v", err)
	}
	if !strings.Contains(text, `"turn": 2`) {
		t.Fatalf("目标事件没有原样交出来：\n%s", text)
	}
	if !strings.Contains(text, "Target event seq 7:") {
		t.Fatalf("目标那一行不对：\n%s", text)
	}
}

// TestNegativeSequenceIsRejectedBeforeAnyEngineCall 钉住那个次序：写错的参数
// 当场退回，不占引擎那一趟。
func TestNegativeSequenceIsRejectedBeforeAnyEngineCall(t *testing.T) {
	service := &stubService{}
	w := newWorld(t, service)
	_, err := w.call("session_event_read", map[string]any{"seq": -1})
	if err == nil {
		t.Fatal("一个负数 seq 竟然被收了")
	}
	if !strings.Contains(err.Error(), "non-negative safe integer") {
		t.Fatalf("没说清楚哪个参数不对：%v", err)
	}
	if len(service.filterCalls) != 0 {
		t.Fatalf("参数还没验就去问引擎了：%d 次", len(service.filterCalls))
	}
}

// TestAnEmptyQueryIsRejected 钉住那道检索词清洗。
func TestAnEmptyQueryIsRejected(t *testing.T) {
	w := newWorld(t, &stubService{})
	if _, err := w.call("session_search", map[string]any{"query": "   \n  "}); err == nil {
		t.Fatal("一句全是空白的检索词竟然被收了")
	} else if !strings.Contains(err.Error(), "non-whitespace text") {
		t.Fatalf("没说清楚检索词哪里不对：%v", err)
	}
}

// TestAnEmptyFilterListIsRejected 钉住「一张空表和不给这个过滤器不是一回事」。
func TestAnEmptyFilterListIsRejected(t *testing.T) {
	w := newWorld(t, &stubService{})
	_, err := w.call("session_search", map[string]any{
		"query": "alpha", "session_ids": []string{},
	})
	if err == nil {
		t.Fatal("一张空的 session_ids 竟然被收了")
	}
	if !strings.Contains(err.Error(), "at least one value when supplied") {
		t.Fatalf("没说清楚那张表哪里不对：%v", err)
	}
}

// TestUnauthorizedParentsCollapseToAnEmptyResult 钉住那条捷径：模型问的父会话
// 全在界外时，一趟引擎都不该跑。
func TestUnauthorizedParentsCollapseToAnEmptyResult(t *testing.T) {
	service := &stubService{
		sessions: map[session.SessionID]sessionquery.Record{
			"stranger": record("stranger", otherCwd, ""),
		},
	}
	w := newWorld(t, service)
	text, err := w.call("session_search", map[string]any{
		"query": "alpha", "parent_session_ids": []string{"stranger"},
	})
	if err != nil {
		t.Fatalf("检索失败：%v", err)
	}
	if text != "No prior session matches found." {
		t.Fatalf("交出来的不是空结果：\n%s", text)
	}
	if len(service.searchCalls) != 0 {
		t.Fatalf("明知一条都搜不到还去问了引擎：%d 次", len(service.searchCalls))
	}
}

// TestTheFiveToolsAndTheGuidanceInstallTogether 钉住那次装配：五件工具和那段
// 指引一起上，摘掉之后一件都不剩。
func TestTheFiveToolsAndTheGuidanceInstallTogether(t *testing.T) {
	w := newWorld(t, &stubService{})
	undo, err := w.controller.Install(t.Context(), w.root, querytool.Deps{
		Tools: w.tools, Prompts: w.prompts,
	})
	if err != nil {
		t.Fatalf("装控制器失败：%v", err)
	}
	names := []string{
		querytool.SearchToolName, querytool.EventSearchToolName, querytool.TraceToolName,
		querytool.EventTraceToolName, querytool.EventReadToolName,
	}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); !ok {
			t.Fatalf("%s 没装上", name)
		}
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("摘控制器失败：%v", err)
	}
	for _, name := range names {
		if _, ok := w.tools.Get(name, w.root.Key()); ok {
			t.Fatalf("%s 摘掉之后还在", name)
		}
	}
}

// TestSearchToolsCarryTheDeadlineAndReadToolsAreConcurrencySafe 钉住那两组标记
// 的分工：检索带截止时间，精读可以并行。
func TestSearchToolsCarryTheDeadlineAndReadToolsAreConcurrencySafe(t *testing.T) {
	w := newWorld(t, &stubService{})
	w.install()
	for _, name := range []string{querytool.SearchToolName, querytool.EventSearchToolName} {
		definition, ok := w.tools.Get(name, w.root.Key())
		if !ok {
			t.Fatalf("%s 没装上", name)
		}
		if definition.Timeout != querytool.DefaultSearchTimeout {
			t.Fatalf("%s 的截止时间是 %s", name, definition.Timeout)
		}
		if definition.IsConcurrencySafe != nil {
			t.Fatalf("%s 不该标成可并行", name)
		}
	}
	for _, name := range []string{
		querytool.TraceToolName, querytool.EventTraceToolName, querytool.EventReadToolName,
	} {
		definition, ok := w.tools.Get(name, w.root.Key())
		if !ok {
			t.Fatalf("%s 没装上", name)
		}
		if definition.IsConcurrencySafe == nil || !definition.IsConcurrencySafe(nil) {
			t.Fatalf("%s 该标成可并行", name)
		}
	}
}
