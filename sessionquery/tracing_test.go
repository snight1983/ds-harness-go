// 本文件的作用：血统与事件关系追溯，包括语料被截断、成环这些边角。
//
// 源: packages/session-query/session-query/src/tracing.ts

package sessionquery

import (
	"testing"

	"ds-harness-go/session"
)

// lineageRecord 排一条只关心 id、父会话、建会话时间的记录。
func lineageRecord(id, parent session.SessionID, createdAt int64) Record {
	header := testHeader(id, createdAt)
	header.ParentSession = parent
	return Record{Header: header, Persisted: true}
}

// chainedReplacementLog 是「盖一次之后又被盖一次」的合法日志。
func chainedReplacementLog(t *testing.T) []session.Event {
	t.Helper()

	return append(replacementLog(t), replacingUserEvent(t, 3, "再盖一次", 2, 2, 2))
}

func TestEventRecordsClassifiesEverySurfacePosition(t *testing.T) {
	t.Parallel()

	events := append(replacementLog(t), plainEvent(t, session.EventTurnStart, 3, session.TurnStartData{Turn: 1}))

	records, err := EventRecords("s1", events)
	if err != nil {
		t.Fatalf("分类不出来：%v", err)
	}
	want := []EventSurface{SurfaceShadowed, SurfaceShadowed, SurfaceCurrent, SurfaceLogOnly}
	if len(records) != len(want) {
		t.Fatalf("记录数不对：想要 %d，实际 %d", len(want), len(records))
	}
	for index, record := range records {
		if record.SessionID != "s1" || record.Seq != index || record.Surface != want[index] {
			t.Fatalf("第 %d 条记录不对：%+v", index, record)
		}
	}
}

func TestEventRecordsRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := EventRecords("s1", []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)})
	requireCode(t, err, CodeInvalidSurface)
}

func TestCurrentSurfaceEventsHandsBackDetachedCopies(t *testing.T) {
	t.Parallel()

	events := replacementLog(t)

	surface, err := CurrentSurfaceEvents("s1", events)
	if err != nil {
		t.Fatalf("取不出表面：%v", err)
	}
	if len(surface) != 1 || surface[0].Seq != 2 {
		t.Fatalf("当前表面不对：%+v", surface)
	}

	surface[0].Data[0] = ' '
	if events[2].Data[0] == ' ' {
		t.Fatal("交出去的事件没有脱离，调用方改到了语料里那一份")
	}
}

func TestCurrentSurfaceEventsRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := CurrentSurfaceEvents("s1", []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)})
	requireCode(t, err, CodeInvalidSurface)
}

func TestTraceEventFollowsTheWholeReplacementChain(t *testing.T) {
	t.Parallel()

	events := chainedReplacementLog(t)

	trace, err := TraceEvent("s1", events, 0)
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if trace.Target.Seq != 0 || trace.Target.Surface != SurfaceShadowed {
		t.Fatalf("目标记录不对：%+v", trace.Target)
	}
	if !trace.Shadowed || trace.ReplacedBy != 2 {
		t.Fatalf("直接替换者不对：%+v", trace)
	}
	if len(trace.ReplacementChain) != 2 || trace.ReplacementChain[0] != 2 || trace.ReplacementChain[1] != 3 {
		t.Fatalf("替换链没走到底：%v", trace.ReplacementChain)
	}
	if len(trace.DerivedEventSeqs) != 1 || trace.DerivedEventSeqs[0] != 2 {
		t.Fatalf("派生事件不对：%v", trace.DerivedEventSeqs)
	}
	if len(trace.SourceEventSeqs) != 0 || len(trace.ReplacedEventSeqs) != 0 {
		t.Fatalf("第一条事件不该有来源、也不该盖过谁：%+v", trace)
	}
}

func TestTraceEventOnTheEventThatDidTheReplacing(t *testing.T) {
	t.Parallel()

	events := replacementLog(t)

	trace, err := TraceEvent("s1", events, 2)
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if trace.Shadowed {
		t.Fatalf("最后那条没被谁盖住：%+v", trace)
	}
	if len(trace.ReplacementChain) != 0 {
		t.Fatalf("没被盖住就不该有替换链：%v", trace.ReplacementChain)
	}
	if len(trace.ReplacedEventSeqs) != 2 || trace.ReplacedEventSeqs[0] != 0 || trace.ReplacedEventSeqs[1] != 1 {
		t.Fatalf("它移走的表面节点不对：%v", trace.ReplacedEventSeqs)
	}
	if len(trace.SourceEventSeqs) != 2 {
		t.Fatalf("来源清单不对：%v", trace.SourceEventSeqs)
	}
	if len(trace.DerivedEventSeqs) != 0 {
		t.Fatalf("后面没有事件引用它：%v", trace.DerivedEventSeqs)
	}
}

func TestTraceEventDetachesTheSeqSlices(t *testing.T) {
	t.Parallel()

	events := replacementLog(t)

	trace, err := TraceEvent("s1", events, 2)
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	trace.SourceEventSeqs[0] = 99
	if events[2].SourceEventSeqs[0] != 0 {
		t.Fatal("来源清单没有复制，调用方改到了日志里那一份")
	}
}

func TestTraceEventRefusesASeqThatIsNotThere(t *testing.T) {
	t.Parallel()

	events := singleUserLog(t, "只有一条")
	broken := []session.Event{userEvent(t, 0, "只有一条")}
	broken[0].Seq = 5

	cases := map[string]struct {
		events []session.Event
		seq    int
	}{
		"序号为负":      {events: events, seq: -1},
		"序号超出日志":    {events: events, seq: 7},
		"下标上的事件不是它": {events: broken, seq: 0},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := TraceEvent("s1", testCase.events, testCase.seq)
			requireCode(t, err, CodeEventNotFound)
		})
	}
}

func TestTraceEventRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	_, err := TraceEvent("s1", []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}, 0)
	requireCode(t, err, CodeInvalidSurface)
}

func TestTraceSessionWalksBothDirections(t *testing.T) {
	t.Parallel()

	records := []Record{
		lineageRecord("root", "", 100),
		lineageRecord("mid", "root", 200),
		lineageRecord("leaf", "mid", 300),
		lineageRecord("兄弟乙", "mid", 300),
		lineageRecord("兄弟甲", "mid", 250),
		lineageRecord("孙", "leaf", 400),
	}

	trace, err := TraceSession(records, "mid")
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if !trace.Complete || trace.Root.Header.ID != "root" {
		t.Fatalf("这条血统本该是完整的、根是 root：%+v", trace)
	}
	if len(trace.Ancestors) != 1 || trace.Ancestors[0].Header.ID != "root" {
		t.Fatalf("祖先链不对：%+v", trace.Ancestors)
	}
	wantChildren := []session.SessionID{"兄弟甲", "leaf", "兄弟乙"}
	if len(trace.Descendants) != len(wantChildren) {
		t.Fatalf("孩子数不对：想要 %v，实际 %+v", wantChildren, trace.Descendants)
	}
	for index, node := range trace.Descendants {
		if node.Session.Header.ID != wantChildren[index] {
			t.Fatalf("兄弟顺序不对：想要 %v，实际第 %d 个是 %q", wantChildren, index, node.Session.Header.ID)
		}
	}
	grandchildren := trace.Descendants[1].Descendants
	if len(grandchildren) != 1 || grandchildren[0].Session.Header.ID != "孙" {
		t.Fatalf("后代树没有往下展开：%+v", grandchildren)
	}
	if len(grandchildren[0].Descendants) != 0 {
		t.Fatalf("叶子不该有后代：%+v", grandchildren[0])
	}
}

func TestTraceSessionSaysWhichParentItLostSightOf(t *testing.T) {
	t.Parallel()

	records := []Record{lineageRecord("only", "在语料外面", 100)}

	trace, err := TraceSession(records, "only")
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if trace.Complete {
		t.Fatal("父亲不在语料里，这条血统不完整")
	}
	if trace.UnresolvedParentID != "在语料外面" {
		t.Fatalf("没说清是在哪儿断的：%q", trace.UnresolvedParentID)
	}
	if trace.Root.Header.ID != "" {
		t.Fatalf("不完整的血统不该给出根：%+v", trace.Root)
	}
}

func TestTraceSessionOnASessionWithNoParentIsItsOwnRoot(t *testing.T) {
	t.Parallel()

	records := []Record{lineageRecord("only", "", 100)}

	trace, err := TraceSession(records, "only")
	if err != nil {
		t.Fatalf("追溯不了：%v", err)
	}
	if !trace.Complete || trace.Root.Header.ID != "only" {
		t.Fatalf("没有父亲的会话，根就是它自己：%+v", trace)
	}
}

func TestTraceSessionRefusesACycleInTheParentChain(t *testing.T) {
	t.Parallel()

	records := []Record{
		lineageRecord("a", "b", 100),
		lineageRecord("b", "a", 200),
	}

	_, err := TraceSession(records, "a")
	requireCode(t, err, CodeInvalidLineage)
}

func TestTraceSessionRefusesAnIDThatIsNotInTheCorpus(t *testing.T) {
	t.Parallel()

	_, err := TraceSession([]Record{lineageRecord("a", "", 100)}, "b")
	requireCode(t, err, CodeSessionNotFound)
}
