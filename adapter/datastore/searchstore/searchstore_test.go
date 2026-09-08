// 本文件验这个检索后端答得对：对账只重建变过的那些、活的盖住落地的、两侧并起来
// 排出同一个序、翻页翻得完整不重复，以及语料变过之后旧游标当场作废。

package searchstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func Test检索落地的会话(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "请把配置改成异步的"))
	store.put(testHeader("s2", 200), "r1", logOf(t, "这里和配置无关"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 2 {
		t.Fatalf("两份会话都该命中：%v", got)
	}
	if page.NextCursor != "" {
		t.Errorf("一页装得下就不该发游标：%q", page.NextCursor)
	}
}

func Test没命中的会话不出现(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "只谈天气"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("不该有命中：%v", idsOf(page.Items))
	}
}

func Test活的盖住落地的(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "落地那份写的是旧词"))

	live := newFakeLive()
	live.put(testHeader("s1", 100), logOf(t, "落地那份写的是旧词", "活着那份补了新词"))

	search := newStore(t, live, store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "新词"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("活着那份的新话该搜得到：%v", got)
	}
	if !page.Items[0].Record.Live {
		t.Errorf("命中该标成活着的：%+v", page.Items[0].Record)
	}

	// 同一句话在两边都有：不能因为落地的那一份还在索引里就出两条。
	page, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "旧词"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 {
		t.Fatalf("一个会话只该出一条：%v", got)
	}
}

func Test一个会话只出最强的那一条(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置", "配置 配置 配置"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 {
		t.Fatalf("一个会话只该出一条：%v", got)
	}
	if best := page.Items[0].BestMatch.Snippet; !strings.Contains(best, "配置 配置 配置") {
		t.Errorf("出的不是最强的那一条：%q", best)
	}
}

func Test两侧并起来排出同一个序(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("p1", 100), "r1", logOf(t, "配置 配置"))
	store.put(testHeader("p2", 100), "r1", logOf(t, "配置"))

	live := newFakeLive()
	live.put(testHeader("l1", 100), logOf(t, "配置 配置 配置"))

	search := newStore(t, live, store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	want := []string{"l1", "p1", "p2"}
	got := idsOf(page.Items)
	if len(got) != len(want) {
		t.Fatalf("三份都该命中：%v", got)
	}
	for at := range want {
		if got[at] != want[at] {
			t.Fatalf("命中多的该排在前：想要 %v，实际 %v", want, got)
		}
	}
}

func Test翻页翻得完整不重复(t *testing.T) {
	store := newFakeStore()
	// 命中次数依次递减，于是次序是定死的，翻页翻错位当场看得出来。
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置 配置 配置 配置"))
	store.put(testHeader("s2", 100), "r1", logOf(t, "配置 配置 配置"))
	store.put(testHeader("s3", 100), "r1", logOf(t, "配置 配置"))
	store.put(testHeader("s4", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	var walked []string
	cursor := sessionquery.SearchCursor("")
	for range 10 {
		page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{
			Query: "配置", Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("检索不该失败：%v", err)
		}
		walked = append(walked, idsOf(page.Items)...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	want := []string{"s1", "s2", "s3", "s4"}
	if len(walked) != len(want) {
		t.Fatalf("翻页翻出来的不是四条：%v", walked)
	}
	for at := range want {
		if walked[at] != want[at] {
			t.Fatalf("翻页错位：想要 %v，实际 %v", want, walked)
		}
	}
}

func Test语料变过之后旧游标作废(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置 配置"))
	store.put(testHeader("s2", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置", Limit: 1})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if page.NextCursor == "" {
		t.Fatalf("还有第二页，该发游标")
	}

	store.put(testHeader("s3", 100), "r1", logOf(t, "配置 配置 配置"))
	_, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Limit: 1, Cursor: page.NextCursor,
	})
	requireCode(t, err, sessionquery.CodeStaleCursor)
}

func Test活会话多说一句就让游标过期(t *testing.T) {
	live := newFakeLive()
	live.put(testHeader("s1", 100), logOf(t, "配置 配置"))
	live.put(testHeader("s2", 100), logOf(t, "配置"))
	search := newStore(t, live, newFakeStore())

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置", Limit: 1})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}

	live.put(testHeader("s1", 100), logOf(t, "配置 配置", "又说了一句"))
	_, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Limit: 1, Cursor: page.NextCursor,
	})
	requireCode(t, err, sessionquery.CodeStaleCursor)
}

func Test游标不属于这次请求(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置 配置"))
	store.put(testHeader("s2", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置", Limit: 1})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}

	// 换一个查询词，指纹就变了；语料没动，所以这不该被当成过期。
	_, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "另一个词", Limit: 1, Cursor: page.NextCursor,
	})
	requireCode(t, err, sessionquery.CodeInvalidCursor)

	// 会话内检索的游标换不到跨会话检索上。
	_, err = search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "s1", Query: "配置", Limit: 1, Cursor: page.NextCursor,
	})
	requireCode(t, err, sessionquery.CodeInvalidCursor)
}

func Test读不回来的游标(t *testing.T) {
	search := newStore(t, newFakeLive(), newFakeStore())

	_, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Cursor: "这不是一个令牌",
	})
	requireCode(t, err, sessionquery.CodeInvalidCursor)
}

func Test谓词的书写顺序不影响游标(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置 配置"))
	store.put(testHeader("s2", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	filters := []sessionquery.EventMetadataFilter{
		sessionquery.TypeFilter{Values: []sessionlog.EventType{sessionlog.EventUserMessage}},
		sessionquery.SeqFilter{Range: sessionquery.Range{}},
	}
	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Limit: 1, EventFilters: filters,
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}

	swapped := []sessionquery.EventMetadataFilter{filters[1], filters[0]}
	if _, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Limit: 1, EventFilters: swapped, Cursor: page.NextCursor,
	}); err != nil {
		t.Fatalf("换个书写顺序不该让游标作废：%v", err)
	}
}

func Test会话内检索(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置一", "无关的一句", "配置二"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "s1", Query: "配置",
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("两条都该命中：%v", textsOf(page.Items))
	}
	if page.Session.ID != "s1" {
		t.Errorf("交回来的会话头不对：%+v", page.Session)
	}
}

func Test会话内检索活的那份不问库(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "落地那份"))

	live := newFakeLive()
	live.put(testHeader("s1", 100), logOf(t, "落地那份", "活着那份"))

	search := newStore(t, live, store)

	page, err := search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "s1", Query: "那份",
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("活着那份的两条都该命中：%v", textsOf(page.Items))
	}
}

func Test会话内检索找不到会话(t *testing.T) {
	search := newStore(t, newFakeLive(), newFakeStore())

	_, err := search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "没有这个", Query: "配置",
	})
	requireCode(t, err, sessionquery.CodeSessionNotFound)
}

func Test事件谓词筛得住(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置一", "配置二", "配置三"))
	search := newStore(t, newFakeLive(), store)

	from := int64(1)
	page, err := search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "s1", Query: "配置",
		Filters: []sessionquery.EventMetadataFilter{
			sessionquery.SeqFilter{Range: sessionquery.Range{From: &from}},
		},
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("第 0 条该被筛掉：%v", textsOf(page.Items))
	}
	for _, hit := range page.Items {
		if hit.Seq < 1 {
			t.Errorf("筛剩下的里还有第 %d 条", hit.Seq)
		}
	}
}

func Test自相矛盾的谓词交出空页(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	// 两条类型过滤器交出来是空集：一条都不该命中，而不是「什么都不筛」。
	page, err := search.SearchEvents(t.Context(), sessionquery.EventSearchRequest{
		SessionID: "s1", Query: "配置",
		Filters: []sessionquery.EventMetadataFilter{
			sessionquery.TypeFilter{Values: []sessionlog.EventType{sessionlog.EventUserMessage}},
			sessionquery.TypeFilter{Values: []sessionlog.EventType{sessionlog.EventAssistantMessage}},
		},
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("交出来是空集就该一条都没有：%v", textsOf(page.Items))
	}
}

func Test会话谓词筛得住(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	store.put(testHeader("s2", 200), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置",
		SessionFilters: []sessionquery.SessionFilter{
			sessionquery.IDFilter{Values: []sessionlog.SessionID{"s2"}},
		},
	})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 || got[0] != "s2" {
		t.Errorf("只该剩下 s2：%v", got)
	}
}

func Test大小写和空白都不算数(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "Async\nConfig  已经打开"))
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "async config"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("换行和大小写都不该挡住命中：%v", idsOf(page.Items))
	}
}

func Test查询词不合法(t *testing.T) {
	search := newStore(t, newFakeLive(), newFakeStore())

	_, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "   "})
	requireCode(t, err, sessionquery.CodeInvalidQuery)

	_, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "带\x00的"})
	requireCode(t, err, sessionquery.CodeInvalidQuery)
}

func Test页大小不合法(t *testing.T) {
	search := newStore(t, newFakeLive(), newFakeStore())

	_, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置", Limit: -1})
	requireCode(t, err, sessionquery.CodeInvalidLimit)

	_, err = search.SearchSessions(t.Context(), sessionquery.SearchRequest{
		Query: "配置", Limit: DefaultMaxLimit + 1,
	})
	requireCode(t, err, sessionquery.CodeInvalidLimit)
}

func Test装配不合法(t *testing.T) {
	if _, err := New(t.Context(), Config{}); err == nil {
		t.Fatalf("不挂活会话表该开不起来")
	} else {
		requireCode(t, err, sessionquery.CodeInvalidConfig)
	}

	_, err := New(t.Context(), Config{Live: newFakeLive(), Limit: 10, MaxLimit: 5})
	requireCode(t, err, sessionquery.CodeInvalidConfig)

	_, err = New(t.Context(), Config{Live: newFakeLive(), SnippetChars: -1})
	requireCode(t, err, sessionquery.CodeInvalidConfig)
}

func Test没变过的会话不重读(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	if _, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"}); err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	first := store.reads()
	if first == 0 {
		t.Fatalf("第一次该把日志读一遍")
	}

	if _, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"}); err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if store.reads() != first {
		t.Errorf("令牌没变就不该重读：第一次 %d 次，第二次累计 %d 次", first, store.reads())
	}

	store.put(testHeader("s1", 100), "r2", logOf(t, "配置", "又说了一句"))
	if _, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"}); err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if store.reads() == first {
		t.Errorf("令牌变了就该重读")
	}
}

func Test落地的会话没了就整组丢掉(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	if _, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"}); err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}

	store.drop("s1")
	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("会话没了，它的文档也该没了：%v", idsOf(page.Items))
	}
	if groups, err := search.docs.Groups(t.Context()); err != nil {
		t.Fatalf("列分组不该失败：%v", err)
	} else if len(groups) != 0 {
		t.Errorf("索引里还留着这一组：%v", groups)
	}
}

func Test一份坏存档不该让别的搜不了(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("好的", 100), "r1", logOf(t, "配置"))
	store.put(testHeader("坏的", 100), "r1", logOf(t, "配置"))
	store.inspectErr["坏的"] = &persistence.CorruptionError{
		ID: "坏的", Cause: errors.New("校验没过"),
	}
	search := newStore(t, newFakeLive(), store)

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("一份坏的不该让整次检索失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 || got[0] != "好的" {
		t.Errorf("好的那份该照样搜得到：%v", got)
	}
}

func Test读存档失败照实报出来(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	store.inspectErr["s1"] = errors.New("库连不上")
	search := newStore(t, newFakeLive(), store)

	_, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	requireCode(t, err, sessionquery.CodePersistenceFailed)
}

func Test列举落地会话失败照实报出来(t *testing.T) {
	store := newFakeStore()
	store.listErr = errors.New("库连不上")
	search := newStore(t, newFakeLive(), store)

	_, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	requireCode(t, err, sessionquery.CodePersistenceFailed)
}

func Test只有活会话也能检索(t *testing.T) {
	live := newFakeLive()
	live.put(testHeader("s1", 100), logOf(t, "配置"))
	search := newStoreWith(t, Config{Live: live})

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 || got[0] != "s1" {
		t.Errorf("不挂持久化后端也该搜得到活的：%v", got)
	}
}

func Test取消当场停下(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := search.SearchSessions(ctx, sessionquery.SearchRequest{Query: "配置"})
	requireCode(t, err, sessionquery.CodeAborted)
}

func Test挂进引擎就生效(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1", logOf(t, "配置"))
	search := newStore(t, newFakeLive(), store)

	engine, err := sessionquery.New(sessionquery.Options{
		Live: newFakeLive(), Persistence: store, Searcher: search,
	})
	if err != nil {
		t.Fatalf("装引擎不该失败：%v", err)
	}
	page, err := engine.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if got := idsOf(page.Items); len(got) != 1 || got[0] != "s1" {
		t.Errorf("引擎该把检索转给这个后端：%v", got)
	}
}
