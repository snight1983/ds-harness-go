// 本文件的作用：不依赖检索后端的那些纯谓词，以及防注入的字面文本匹配器。
//
// 源: packages/session-query/session-query/src/filters.ts

package sessionquery

import (
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

// bound 取一个 int64 的地址，给区间过滤器用。
func bound(value int64) *int64 { return &value }

// 用于会话过滤的四条固定记录。
func filterRecords() []Record {
	return []Record{
		{Header: session.SessionHeader{ID: "a", CreatedAt: 100, Cwd: "/x"}, Live: true},
		{Header: session.SessionHeader{ID: "b", CreatedAt: 200, Cwd: "/y", ParentSession: "a"}, Persisted: true},
		{Header: session.SessionHeader{ID: "c", CreatedAt: 300}, Live: true, Persisted: true},
	}
}

func TestFilterSessionsAndsEveryClauseAndKeepsInputOrder(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		filters []SessionFilter
		want    []session.SessionID
	}{
		"没有过滤器就全留下": {want: []session.SessionID{"a", "b", "c"}},
		"按 id 取":    {filters: []SessionFilter{IDFilter{Values: []session.SessionID{"c", "a"}}}, want: []session.SessionID{"a", "c"}},
		"按工作目录取": {
			filters: []SessionFilter{CwdFilter{Values: []string{"/y"}}},
			want:    []session.SessionID{"b"},
		},
		"空串就是没有工作目录": {
			filters: []SessionFilter{CwdFilter{Values: []string{""}}},
			want:    []session.SessionID{"c"},
		},
		"按建会话时间取": {
			filters: []SessionFilter{CreatedAtFilter{Range: Range{From: bound(150), To: bound(250)}}},
			want:    []session.SessionID{"b"},
		},
		"下界不封顶": {
			filters: []SessionFilter{CreatedAtFilter{Range: Range{From: bound(200)}}},
			want:    []session.SessionID{"b", "c"},
		},
		"上界不封底": {
			filters: []SessionFilter{CreatedAtFilter{Range: Range{To: bound(100)}}},
			want:    []session.SessionID{"a"},
		},
		"按父会话取": {
			filters: []SessionFilter{ParentFilter{Values: []session.SessionID{"a"}}},
			want:    []session.SessionID{"b"},
		},
		"空串就是没有父会话": {
			filters: []SessionFilter{ParentFilter{Values: []session.SessionID{""}}},
			want:    []session.SessionID{"a", "c"},
		},
		"只要活着的": {
			filters: []SessionFilter{AvailabilityFilter{Values: []Availability{AvailabilityLive}}},
			want:    []session.SessionID{"a", "c"},
		},
		"只要落地的": {
			filters: []SessionFilter{AvailabilityFilter{Values: []Availability{AvailabilityPersisted}}},
			want:    []session.SessionID{"b", "c"},
		},
		"两个来源之间是或": {
			filters: []SessionFilter{AvailabilityFilter{Values: []Availability{AvailabilityLive, AvailabilityPersisted}}},
			want:    []session.SessionID{"a", "b", "c"},
		},
		"两条过滤器之间是与": {
			filters: []SessionFilter{
				AvailabilityFilter{Values: []Availability{AvailabilityLive}},
				CreatedAtFilter{Range: Range{From: bound(250)}},
			},
			want: []session.SessionID{"c"},
		},
		"一个都留不下": {
			filters: []SessionFilter{IDFilter{Values: []session.SessionID{"没有这个"}}},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kept, err := FilterSessions(filterRecords(), testCase.filters)
			if err != nil {
				t.Fatalf("过滤不了：%v", err)
			}
			ids := make([]session.SessionID, 0, len(kept))
			for _, record := range kept {
				ids = append(ids, record.Header.ID)
			}
			if len(ids) != len(testCase.want) {
				t.Fatalf("留下来的会话不对：想要 %v，实际 %v", testCase.want, ids)
			}
			for index, id := range ids {
				if id != testCase.want[index] {
					t.Fatalf("留下来的会话不对：想要 %v，实际 %v", testCase.want, ids)
				}
			}
		})
	}
}

func TestFilterSessionsAlsoTakesTypesThatEmbedARecord(t *testing.T) {
	t.Parallel()

	hits := []SearchHit{
		{Record: Record{Header: session.SessionHeader{ID: "a"}, Live: true}},
		{Record: Record{Header: session.SessionHeader{ID: "b"}}},
	}

	kept, err := FilterSessions(hits, []SessionFilter{AvailabilityFilter{Values: []Availability{AvailabilityLive}}})
	if err != nil {
		t.Fatalf("过滤不了：%v", err)
	}
	if len(kept) != 1 || kept[0].Header.ID != "a" {
		t.Fatalf("嵌了记录的类型没走通同一套谓词：%+v", kept)
	}
}

func TestFilterSessionsRejectsAnInvalidClause(t *testing.T) {
	t.Parallel()

	cases := map[string][]SessionFilter{
		"区间上下界反了": {CreatedAtFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"来源词汇不认识": {AvailabilityFilter{Values: []Availability{"存档里"}}},
	}

	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := FilterSessions(filterRecords(), filters)
			requireCode(t, err, CodeInvalidFilter)
		})
	}
}

// 用于事件过滤的三篇固定文档。
func filterDocuments() []EventSearchDocument {
	return []EventSearchDocument{
		{EventRecord: EventRecord{Seq: 0, Type: session.EventUserMessage, Time: 100, Surface: SurfaceShadowed}, Text: "改一下  首页 标题"},
		{EventRecord: EventRecord{Seq: 1, Type: session.EventAssistantMessage, Time: 200, Surface: SurfaceCurrent}, Text: "改好了"},
		{EventRecord: EventRecord{Seq: 2, Type: session.EventTurnStart, Time: 300, Surface: SurfaceLogOnly}, Text: ""},
	}
}

func TestFilterEventDocumentsAndsEveryClause(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		filters []EventFilter
		want    []int
	}{
		"没有过滤器就全留下": {want: []int{0, 1, 2}},
		"按序号区间取":    {filters: []EventFilter{SeqFilter{Range: Range{From: bound(1)}}}, want: []int{1, 2}},
		"按时间区间取":    {filters: []EventFilter{TimeFilter{Range: Range{To: bound(200)}}}, want: []int{0, 1}},
		"按类型取": {
			filters: []EventFilter{TypeFilter{Values: []session.EventType{session.EventUserMessage}}},
			want:    []int{0},
		},
		"按表面位置取": {
			filters: []EventFilter{SurfaceFilter{Values: []EventSurface{SurfaceCurrent, SurfaceLogOnly}}},
			want:    []int{1, 2},
		},
		"文本匹配忽略大小写与空白": {
			filters: []EventFilter{TextFilter{Text: "  首页\n标题 "}},
			want:    []int{0},
		},
		"文本匹配是字面的，正则元字符不当语法": {
			filters: []EventFilter{TextFilter{Text: "改.一下"}},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kept, err := FilterEventDocuments(filterDocuments(), testCase.filters)
			if err != nil {
				t.Fatalf("过滤不了：%v", err)
			}
			if len(kept) != len(testCase.want) {
				t.Fatalf("留下来的文档不对：想要 %v，实际 %+v", testCase.want, kept)
			}
			for index, document := range kept {
				if document.Seq != testCase.want[index] {
					t.Fatalf("留下来的文档不对：想要 %v，实际 %+v", testCase.want, kept)
				}
			}
		})
	}
}

func TestFilterEventDocumentsRejectsAnInvalidClause(t *testing.T) {
	t.Parallel()

	cases := map[string][]EventFilter{
		"序号区间反了":    {SeqFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"时间区间反了":    {TimeFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"表面词汇不认识":   {SurfaceFilter{Values: []EventSurface{"半透明"}}},
		"文本过滤器全是空白": {TextFilter{Text: "  \n "}},
	}

	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := FilterEventDocuments(filterDocuments(), filters)
			requireCode(t, err, CodeInvalidFilter)
		})
	}
}

func TestMaterializeSessionFiltersCopiesEveryClause(t *testing.T) {
	t.Parallel()

	ids := []session.SessionID{"a"}
	cwds := []string{"/x"}
	parents := []session.SessionID{"p"}
	availability := []Availability{AvailabilityLive}
	filters := []SessionFilter{
		IDFilter{Values: ids},
		CwdFilter{Values: cwds},
		CreatedAtFilter{Range: Range{From: bound(1), To: bound(2)}},
		ParentFilter{Values: parents},
		AvailabilityFilter{Values: availability},
	}

	owned, err := MaterializeSessionFilters(filters)
	if err != nil {
		t.Fatalf("复制不了：%v", err)
	}
	ids[0], cwds[0], parents[0], availability[0] = "改掉了", "改掉了", "改掉了", "改掉了"

	if owned[0].(IDFilter).Values[0] != "a" {
		t.Fatal("id 那一列没有复制，调用方改自己的切片改到了验过的那一份")
	}
	if owned[1].(CwdFilter).Values[0] != "/x" {
		t.Fatal("工作目录那一列没有复制")
	}
	if owned[3].(ParentFilter).Values[0] != "p" {
		t.Fatal("父会话那一列没有复制")
	}
	if owned[4].(AvailabilityFilter).Values[0] != AvailabilityLive {
		t.Fatal("来源那一列没有复制")
	}
}

func TestMaterializeSessionFiltersCopiesRangeBounds(t *testing.T) {
	t.Parallel()

	from, to := int64(1), int64(2)
	owned, err := MaterializeSessionFilters([]SessionFilter{
		CreatedAtFilter{Range: Range{From: &from, To: &to}},
	})
	if err != nil {
		t.Fatalf("复制不了：%v", err)
	}
	from, to = 100, 200

	copied := owned[0].(CreatedAtFilter)
	if *copied.From != 1 || *copied.To != 2 {
		t.Fatalf("区间上下界没有复制：%d 到 %d", *copied.From, *copied.To)
	}
}

func TestMaterializeSessionFiltersValidatesAsItCopies(t *testing.T) {
	t.Parallel()

	cases := map[string][]SessionFilter{
		"区间上下界反了": {CreatedAtFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"来源词汇不认识": {AvailabilityFilter{Values: []Availability{"存档里"}}},
	}

	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := MaterializeSessionFilters(filters)
			requireCode(t, err, CodeInvalidFilter)
		})
	}
}

func TestMaterializeEventFiltersCopiesAndValidates(t *testing.T) {
	t.Parallel()

	types := []session.EventType{session.EventUserMessage}
	owned, err := MaterializeEventFilters([]EventFilter{
		SeqFilter{Range: Range{From: bound(0), To: bound(9)}},
		TimeFilter{Range: Range{From: bound(0)}},
		TypeFilter{Values: types},
		SurfaceFilter{Values: []EventSurface{SurfaceCurrent}},
		TextFilter{Text: "找我"},
	})
	if err != nil {
		t.Fatalf("复制不了：%v", err)
	}
	types[0] = "改掉了"

	if owned[2].(TypeFilter).Values[0] != session.EventUserMessage {
		t.Fatal("类型那一列没有复制")
	}
	if owned[4].(TextFilter).Text != "找我" {
		t.Fatal("文本没有原样带过来")
	}
}

func TestMaterializeEventFiltersRejectsAnInvalidClause(t *testing.T) {
	t.Parallel()

	cases := map[string][]EventFilter{
		"序号区间反了":  {SeqFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"时间区间反了":  {TimeFilter{Range: Range{From: bound(2), To: bound(1)}}},
		"表面词汇不认识": {SurfaceFilter{Values: []EventSurface{"半透明"}}},
	}

	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := MaterializeEventFilters(filters)
			requireCode(t, err, CodeInvalidFilter)
		})
	}
}

// foreignSessionFilter 是一个本包之外造不出来、但可以在包内伪造的变体，
// 用来钉住两处 switch 的兜底分支。
type foreignSessionFilter struct{}

func (foreignSessionFilter) sealedSessionFilter() {}

// foreignEventFilter 同上。
type foreignEventFilter struct{}

func (foreignEventFilter) sealedEventFilter() {}

func TestUnknownFilterVariantsAreRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	t.Run("逻辑会话过滤器", func(t *testing.T) {
		t.Parallel()

		_, err := MaterializeSessionFilters([]SessionFilter{foreignSessionFilter{}})
		requireCode(t, err, CodeInvalidFilter)
		_, err = FilterSessions(filterRecords(), []SessionFilter{foreignSessionFilter{}})
		requireCode(t, err, CodeInvalidFilter)
	})

	t.Run("事件过滤器", func(t *testing.T) {
		t.Parallel()

		_, err := MaterializeEventFilters([]EventFilter{foreignEventFilter{}})
		requireCode(t, err, CodeInvalidFilter)
		_, err = FilterEventDocuments(filterDocuments(), []EventFilter{foreignEventFilter{}})
		requireCode(t, err, CodeInvalidFilter)
	})
}

func TestCompileTextFilterQuotesEveryMetacharacter(t *testing.T) {
	t.Parallel()

	pattern, err := CompileTextFilter("  a.b   c*d  ")
	if err != nil {
		t.Fatalf("编不出来：%v", err)
	}
	if !pattern.MatchString("前 A.B\n\tC*D 后") {
		t.Fatal("字面匹配没能忽略大小写、也没能跨空白")
	}
	if pattern.MatchString("axb cyd") {
		t.Fatal("元字符被当成了正则语法")
	}
}

func TestCompileTextFilterRefusesBlankText(t *testing.T) {
	t.Parallel()

	_, err := CompileTextFilter(" \t\n ")
	requireCode(t, err, CodeInvalidFilter)
}
