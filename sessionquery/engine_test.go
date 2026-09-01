// 本文件的作用：读侧门面把语料、过滤、追溯编排起来之后对外是什么行为，
// 以及没挂检索后端时那两个方法怎么收场。
//
// 源: packages/session-query/session-query/src/index.ts

package sessionquery

import (
	"context"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

// fakeSearcher 是一个把请求原样记下来的检索后端替身。
type fakeSearcher struct {
	sessionRequest EventSearchRequest
	err            error
}

func (f *fakeSearcher) SearchSessions(context.Context, SearchRequest) (SearchPage[SearchHit], error) {
	if f.err != nil {
		return SearchPage[SearchHit]{}, f.err
	}
	return SearchPage[SearchHit]{Items: []SearchHit{{Record: Record{Header: testHeader("s1", 100)}}}}, nil
}

func (f *fakeSearcher) SearchEvents(_ context.Context, request EventSearchRequest) (EventSearchPage, error) {
	f.sessionRequest = request
	if f.err != nil {
		return EventSearchPage{}, f.err
	}
	return EventSearchPage{Session: testHeader(request.SessionID, 100)}, nil
}

// newEngine 排一个只挂了一份活会话的门面。
func newEngine(t *testing.T, events []session.Event) *Engine {
	t.Helper()

	live := newFakeLive()
	live.put(testHeader("s1", 100), events)
	engine, err := New(Options{Live: live})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}
	return engine
}

func TestNewRefusesAnUnusableConfig(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{Live: newFakeLive(), ReadWindowMax: -1}); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("负的读窗口上限本该拒：%v", err)
	}
	if _, err := New(Options{}); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("没有活会话表本该拒：%v", err)
	}

	engine, err := New(Options{Live: newFakeLive()})
	if err != nil {
		t.Fatalf("只挂活会话表也该建得出来：%v", err)
	}
	if engine.readWindowMax != DefaultReadWindowMax {
		t.Fatalf("读窗口上限传 0 该落到默认值，实际 %d", engine.readWindowMax)
	}
	if engine.Corpus() == nil {
		t.Fatal("语料该拿得到，[ProjectMany] 要用")
	}
}

func TestEngineListSessionsPassesThroughToTheCorpus(t *testing.T) {
	t.Parallel()

	records, err := newEngine(t, nil).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("列举不了：%v", err)
	}
	if len(records) != 1 || records[0].Header.ID != "s1" || !records[0].Live {
		t.Fatalf("列举结果不对：%+v", records)
	}
}

func TestReadSessionReplaysTheWholeLogBeforeHandingItOver(t *testing.T) {
	t.Parallel()

	snapshot, err := newEngine(t, replacementLog(t)).ReadSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	if snapshot.Session.ID != "s1" || len(snapshot.Events) != 3 {
		t.Fatalf("读出来的日志不对：%+v", snapshot)
	}
}

func TestReadSessionRefusesALogThatDoesNotReplay(t *testing.T) {
	t.Parallel()

	// 表面折得出来（seq 0 是一条普通追加），但关系约束过不去：
	// 一条步骤结束却没有对应的步骤开始。
	broken := []session.Event{
		userEvent(t, 0, "开个头"),
		plainEvent(t, session.EventStepEnd, 1, session.StepEndData{Turn: 1, Step: 1}),
	}

	_, err := newEngine(t, broken).ReadSession(context.Background(), "s1")
	requireCode(t, err, CodeCorruptSession)
}

func TestReadSessionRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := newEngine(t, []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}).
		ReadSession(context.Background(), "s1")
	requireCode(t, err, CodeInvalidSurface)
}

func TestEngineFilterSessionsValidatesFiltersBeforeTouchingTheCorpus(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, nil)

	kept, err := engine.FilterSessions(context.Background(),
		[]SessionFilter{AvailabilityFilter{Values: []Availability{AvailabilityLive}}})
	if err != nil {
		t.Fatalf("过滤不了：%v", err)
	}
	if len(kept) != 1 || kept[0].Header.ID != "s1" {
		t.Fatalf("过滤结果不对：%+v", kept)
	}

	_, err = engine.FilterSessions(context.Background(),
		[]SessionFilter{AvailabilityFilter{Values: []Availability{"存档里"}}})
	requireCode(t, err, CodeInvalidFilter)
}

func TestEngineListEventsClassifiesEverySurfacePosition(t *testing.T) {
	t.Parallel()

	records, err := newEngine(t, replacementLog(t)).ListEvents(context.Background(), "s1")
	if err != nil {
		t.Fatalf("列举不了：%v", err)
	}
	want := []EventSurface{SurfaceShadowed, SurfaceShadowed, SurfaceCurrent}
	if len(records) != len(want) {
		t.Fatalf("记录数不对：%+v", records)
	}
	for index, record := range records {
		if record.Surface != want[index] {
			t.Fatalf("第 %d 条的表面位置不对：%+v", index, record)
		}
	}
}

func TestEngineFilterEventsScansTheSemanticDocuments(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, replacementLog(t))

	documents, err := engine.FilterEvents(context.Background(), "s1",
		[]EventFilter{TextFilter{Text: "第二条"}})
	if err != nil {
		t.Fatalf("过滤不了：%v", err)
	}
	if len(documents) != 1 || documents[0].Seq != 1 {
		t.Fatalf("过滤结果不对：%+v", documents)
	}

	// 过滤器先验后用，两道关都得报同一个码：区间反了是复制那一步判出来的，
	// 空白文本是扫描那一步判出来的。
	_, err = engine.FilterEvents(context.Background(), "s1",
		[]EventFilter{SeqFilter{Range: Range{From: bound(2), To: bound(1)}}})
	requireCode(t, err, CodeInvalidFilter)

	_, err = engine.FilterEvents(context.Background(), "s1", []EventFilter{TextFilter{Text: "  "}})
	requireCode(t, err, CodeInvalidFilter)
}

func TestEngineFilterEventsRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := newEngine(t, []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}).
		FilterEvents(context.Background(), "s1", nil)
	requireCode(t, err, CodeInvalidSurface)
}

func TestReadSurfaceMarksHowFarTheObservationGot(t *testing.T) {
	t.Parallel()

	snapshot, err := newEngine(t, replacementLog(t)).ReadSurface(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Seq != 2 {
		t.Fatalf("当前表面不对：%+v", snapshot.Events)
	}
	if !snapshot.CapturedAny || snapshot.CapturedThroughSeq != 2 {
		t.Fatalf("观察水位不对：%+v", snapshot)
	}
}

func TestReadSurfaceOnAnEmptyLogSaysItCapturedNothing(t *testing.T) {
	t.Parallel()

	snapshot, err := newEngine(t, nil).ReadSurface(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	if snapshot.CapturedAny {
		// seq 0 是一条真事件的合法序号，空日志必须另外说一句「什么都没吃到」。
		t.Fatalf("空日志不该报出一个水位：%+v", snapshot)
	}
	if len(snapshot.Events) != 0 {
		t.Fatalf("空日志不该有表面事件：%+v", snapshot.Events)
	}
}

func TestReadSurfaceRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := newEngine(t, []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}).
		ReadSurface(context.Background(), "s1")
	requireCode(t, err, CodeInvalidSurface)
}

func TestEngineTraceSessionUsesOneCorpusObservation(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	parent := testHeader("parent", 100)
	child := testHeader("child", 200)
	child.ParentSession = "parent"
	live.put(parent, nil)
	live.put(child, nil)
	engine, err := New(Options{Live: live})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}

	trace, err := engine.TraceSession(context.Background(), "child")
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if !trace.Complete || trace.Root.Header.ID != "parent" {
		t.Fatalf("血统不对：%+v", trace)
	}
}

func TestEngineTraceEventBindsTheTraceToTheSameObservation(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, replacementLog(t))

	observation, err := engine.TraceEvent(context.Background(), EventTraceRequest{SessionID: "s1", Seq: 0})
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if observation.Session.ID != "s1" {
		t.Fatalf("没绑上同一次会话头观察：%+v", observation.Session)
	}
	if !observation.Shadowed || observation.ReplacedBy != 2 {
		t.Fatalf("追溯结果不对：%+v", observation.EventTrace)
	}

	_, err = engine.TraceEvent(context.Background(), EventTraceRequest{SessionID: "s1", Seq: 9})
	requireCode(t, err, CodeEventNotFound)
}

func TestReadEventCutsABoundedWindowAroundTheTarget(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		userEvent(t, 0, "零"),
		userEvent(t, 1, "一"),
		userEvent(t, 2, "二"),
		userEvent(t, 3, "三"),
		userEvent(t, 4, "四"),
	}
	engine := newEngine(t, events)

	cases := map[string]struct {
		request  EventReadRequest
		wantFrom int
		wantTo   int
	}{
		"两边都取得到":   {request: EventReadRequest{SessionID: "s1", Seq: 2, Before: 1, After: 1}, wantFrom: 1, wantTo: 3},
		"前面不够就贴边":  {request: EventReadRequest{SessionID: "s1", Seq: 0, Before: 3, After: 1}, wantFrom: 0, wantTo: 1},
		"后面不够就贴边":  {request: EventReadRequest{SessionID: "s1", Seq: 4, Before: 1, After: 3}, wantFrom: 3, wantTo: 4},
		"一条上下文都不带": {request: EventReadRequest{SessionID: "s1", Seq: 2}, wantFrom: 2, wantTo: 2},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			window, err := engine.ReadEvent(context.Background(), testCase.request)
			if err != nil {
				t.Fatalf("读不了：%v", err)
			}
			if window.Target.Seq != testCase.request.Seq {
				t.Fatalf("目标事件不对：%+v", window.Target)
			}
			if window.StartSeq != testCase.wantFrom || window.EndSeq != testCase.wantTo {
				t.Fatalf("窗口边界不对：想要 %d 到 %d，实际 %d 到 %d",
					testCase.wantFrom, testCase.wantTo, window.StartSeq, window.EndSeq)
			}
			if len(window.Events) != testCase.wantTo-testCase.wantFrom+1 {
				t.Fatalf("窗口里的事件数对不上边界：%+v", window.Events)
			}
			if window.Events[0].Seq != testCase.wantFrom {
				t.Fatalf("窗口第一条不是 StartSeq：%+v", window.Events[0])
			}
		})
	}
}

func TestReadEventRefusesAWindowOutsideTheConfiguredBound(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("s1", 100), singleUserLog(t, "只有一条"))
	engine, err := New(Options{Live: live, ReadWindowMax: 2})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}

	cases := map[string]EventReadRequest{
		"前面要多了": {SessionID: "s1", Seq: 0, Before: 3},
		"后面要多了": {SessionID: "s1", Seq: 0, After: 3},
		"前面是负数": {SessionID: "s1", Seq: 0, Before: -1},
		"后面是负数": {SessionID: "s1", Seq: 0, After: -1},
	}

	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := engine.ReadEvent(context.Background(), request)
			requireCode(t, err, CodeInvalidWindow)
		})
	}
}

func TestReadEventRefusesASeqThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := newEngine(t, singleUserLog(t, "只有一条")).
		ReadEvent(context.Background(), EventReadRequest{SessionID: "s1", Seq: 9})
	requireCode(t, err, CodeEventNotFound)
}

func TestEveryReadingMethodReportsAMissingSession(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, nil)
	ctx := context.Background()

	cases := map[string]func() error{
		"读整份日志": func() error { _, err := engine.ReadSession(ctx, "没有这个"); return err },
		"列举事件":  func() error { _, err := engine.ListEvents(ctx, "没有这个"); return err },
		"过滤事件":  func() error { _, err := engine.FilterEvents(ctx, "没有这个", nil); return err },
		"读表面":   func() error { _, err := engine.ReadSurface(ctx, "没有这个"); return err },
		"追溯血统":  func() error { _, err := engine.TraceSession(ctx, "没有这个"); return err },
		"追溯事件": func() error {
			_, err := engine.TraceEvent(ctx, EventTraceRequest{SessionID: "没有这个"})
			return err
		},
		"读一条事件": func() error {
			_, err := engine.ReadEvent(ctx, EventReadRequest{SessionID: "没有这个"})
			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireCode(t, call(), CodeSessionNotFound)
		})
	}
}

func TestEveryReadingMethodStopsOnACanceledContext(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, replacementLog(t))
	ctx := canceledContext()

	cases := map[string]func() error{
		"列举会话":  func() error { _, err := engine.ListSessions(ctx); return err },
		"读整份日志": func() error { _, err := engine.ReadSession(ctx, "s1"); return err },
		"过滤会话":  func() error { _, err := engine.FilterSessions(ctx, nil); return err },
		"列举事件":  func() error { _, err := engine.ListEvents(ctx, "s1"); return err },
		"过滤事件":  func() error { _, err := engine.FilterEvents(ctx, "s1", nil); return err },
		"读表面":   func() error { _, err := engine.ReadSurface(ctx, "s1"); return err },
		"追溯血统":  func() error { _, err := engine.TraceSession(ctx, "s1"); return err },
		"追溯事件": func() error {
			_, err := engine.TraceEvent(ctx, EventTraceRequest{SessionID: "s1"})
			return err
		},
		"读一条事件": func() error {
			_, err := engine.ReadEvent(ctx, EventReadRequest{SessionID: "s1"})
			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireCode(t, call(), CodeAborted)
		})
	}
}

func TestTraceMethodsCheckCancellationAfterTheCorpusRead(t *testing.T) {
	t.Parallel()

	// 语料读成功，但调用方在读的这段时间里撤了。
	cases := map[string]func(*Engine, context.Context) error{
		"追溯血统": func(engine *Engine, ctx context.Context) error {
			_, err := engine.TraceSession(ctx, "s1")
			return err
		},
		"追溯事件": func(engine *Engine, ctx context.Context) error {
			_, err := engine.TraceEvent(ctx, EventTraceRequest{SessionID: "s1"})
			return err
		},
		"读一条事件": func(engine *Engine, ctx context.Context) error {
			_, err := engine.ReadEvent(ctx, EventReadRequest{SessionID: "s1"})
			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			store := newFakeStore()
			store.put(testHeader("s1", 100), singleUserLog(t, "只有一条"))
			store.afterList = cancel
			store.afterInspect = func(session.SessionID) { cancel() }
			engine, err := New(Options{Live: newFakeLive(), Persistence: store})
			if err != nil {
				t.Fatalf("门面建不出来：%v", err)
			}

			requireCode(t, call(engine, ctx), CodeAborted)
		})
	}
}

func TestSearchMethodsSayWhenNoBackendIsMounted(t *testing.T) {
	t.Parallel()

	engine := newEngine(t, nil)

	_, err := engine.SearchSessions(context.Background(), SearchRequest{Query: "找我"})
	requireCode(t, err, CodeSearchDisabled)

	_, err = engine.SearchEvents(context.Background(), EventSearchRequest{SessionID: "s1", Query: "找我"})
	requireCode(t, err, CodeSearchDisabled)
}

func TestSearchMethodsHandOffToTheMountedBackend(t *testing.T) {
	t.Parallel()

	searcher := &fakeSearcher{}
	engine, err := New(Options{Live: newFakeLive(), Searcher: searcher})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}

	page, err := engine.SearchSessions(context.Background(), SearchRequest{Query: "找我"})
	if err != nil {
		t.Fatalf("检索不了：%v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("后端交回来的那一页没原样传出去：%+v", page)
	}

	eventPage, err := engine.SearchEvents(context.Background(),
		EventSearchRequest{SessionID: "s1", Query: "找我"})
	if err != nil {
		t.Fatalf("检索不了：%v", err)
	}
	if eventPage.Session.ID != "s1" || searcher.sessionRequest.Query != "找我" {
		t.Fatalf("请求或结果没原样过手：%+v", eventPage)
	}
}

func TestSearchMethodsPassTheBackendFailureThrough(t *testing.T) {
	t.Parallel()

	searcher := &fakeSearcher{err: fail(CodeStaleCursor, "索引世代过期了")}
	engine, err := New(Options{Live: newFakeLive(), Searcher: searcher})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}

	_, sessionErr := engine.SearchSessions(context.Background(), SearchRequest{})
	requireCode(t, sessionErr, CodeStaleCursor)

	_, eventErr := engine.SearchEvents(context.Background(), EventSearchRequest{})
	requireCode(t, eventErr, CodeStaleCursor)
}
