// 本文件的作用：标题那层薄壳的三个方法——头和标题是不是同一次观察出来的、
// 没有标题怎么说、一条坏标题事件的失败隔不隔得住。
//
// # 这些测试防的是什么错
//
//   - **「没有标题」被折成了一个空标题。**界面上那是两件事：前者显示会话 id 或者
//     一句占位，后者显示一个名字叫空串的会话。用零值表达前者会让第二种情形
//     再也做不出来。
//   - **一条坏标题事件把整批读垮掉。**一个损坏的会话不该让同一批里其余会话的
//     标题读不出来；那会让一份会话列表因为其中一行坏了而整个空掉。
//   - **头和标题来自两次读。**一个会话在两次读之间被改名，调用方就会拿到一份
//     对不上的观察。

package sessionquery

import (
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/sessiontitle"
)

// titleEvent 排一条合法的 session/title。
func titleEvent(t *testing.T, seq int, title string) session.Event {
	t.Helper()

	return plainEvent(t, sessiontitle.EventSessionTitle, seq, sessiontitle.EventData{
		Title:       title,
		MessageSeqs: []int{0},
		Source:      sessiontitle.Source{Kind: sessiontitle.SourceFallback},
	})
}

// titleEngine 排一个活会话表挂了若干份日志的门面。
func titleEngine(t *testing.T, logs map[session.SessionID][]session.Event) *Engine {
	t.Helper()

	live := newFakeLive()
	for id, events := range logs {
		live.put(testHeader(id, 100), events)
	}
	engine, err := New(Options{Live: live})
	if err != nil {
		t.Fatalf("门面建不出来：%v", err)
	}
	return engine
}

func TestReadingATitleFoldsTheLatestOneOffTheLog(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {userEvent(t, 0, "开头"), titleEvent(t, 1, "旧名字"), titleEvent(t, 2, "新名字")},
	})

	title, titled, err := engine.ReadTitle(t.Context(), "s1")
	if err != nil {
		t.Fatalf("读标题失败：%v", err)
	}
	if !titled {
		t.Fatal("这个会话有标题，第二个返回值该为真")
	}
	// last-wins：最新那条才算数。
	if title.Title != "新名字" {
		t.Fatalf("该折出最新那条标题，拿到 %q", title.Title)
	}
	if title.EventSeq != 2 {
		t.Fatalf("信封事实该跟着最新那条走，拿到 seq %d", title.EventSeq)
	}
}

func TestAnUntitledSessionSaysSoInsteadOfReturningAnEmptyTitle(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {userEvent(t, 0, "开头")},
	})

	title, titled, err := engine.ReadTitle(t.Context(), "s1")
	if err != nil {
		t.Fatalf("一个没有标题的会话不该报错：%v", err)
	}
	if titled {
		t.Fatal("这个会话没有过标题，第二个返回值该为假")
	}
	if title.Title != "" || title.EventSeq != 0 || title.Source.Kind != "" {
		t.Fatalf("没有标题时那份快照该是零值，拿到 %+v", title)
	}
}

func TestATitleSnapshotCarriesTheHeaderItWasFoldedWith(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {userEvent(t, 0, "开头"), titleEvent(t, 1, "名字")},
	})

	observation, err := engine.ReadTitleSnapshot(t.Context(), "s1")
	if err != nil {
		t.Fatalf("读观察失败：%v", err)
	}
	// 头和标题必须是同一次观察出来的：分两次读会让调用方拿到一份对不上的组合。
	if observation.Session.ID != "s1" || observation.Session.CreatedAt != 100 {
		t.Fatalf("该带上折它用的那份头，拿到 %+v", observation.Session)
	}
	if !observation.Titled || observation.Title.Title != "名字" {
		t.Fatalf("该带上折出来的标题，拿到 %+v", observation)
	}
}

func TestABatchOfTitlesKeepsFirstOccurrenceOrderAndIsolatesFailures(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {titleEvent(t, 0, "一号")},
		// 一条读不回来的标题事件：它必须只让**这一个**会话的投影失败。
		"s2": {session.Event{Type: sessiontitle.EventSessionTitle, Seq: 0, Data: []byte(`not json`)}},
		"s3": {titleEvent(t, 0, "三号")},
	})

	results, err := engine.ReadTitleSnapshots(t.Context(), []session.SessionID{"s1", "s2", "s3", "s1"})
	if err != nil {
		t.Fatalf("批量读不该整体失败：%v", err)
	}
	// 去重后的首次出现顺序，重复的那个 id 不再出现。
	if len(results) != 3 {
		t.Fatalf("该按去重后的 id 出三条结果，拿到 %d 条", len(results))
	}
	if results[0].SessionID != "s1" || results[1].SessionID != "s2" || results[2].SessionID != "s3" {
		t.Fatalf("顺序该按首次出现排，拿到 %v %v %v",
			results[0].SessionID, results[1].SessionID, results[2].SessionID)
	}
	if results[0].Err != nil || results[0].Value.Title.Title != "一号" {
		t.Fatalf("好的那条该正常出结果，拿到 %+v", results[0])
	}
	if results[1].Err == nil {
		t.Fatal("坏的那条该带着自己的错误")
	}
	if results[2].Err != nil || results[2].Value.Title.Title != "三号" {
		t.Fatalf("坏的那条不该拖垮后面的，拿到 %+v", results[2])
	}
}

func TestReadingOneTitleSurfacesTheIsolatedFailure(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {session.Event{Type: sessiontitle.EventSessionTitle, Seq: 0, Data: []byte(`{"title":42}`)}},
	})

	// 单条读法上没有别的结果可以隔离，那条被隔离的失败就是这次调用的失败。
	if _, err := engine.ReadTitleSnapshot(t.Context(), "s1"); err == nil {
		t.Fatal("一条坏标题事件该让单条读法失败")
	}
	if _, _, err := engine.ReadTitle(t.Context(), "s1"); err == nil {
		t.Fatal("ReadTitle 该把那条失败原样交出来")
	}
}

func TestReadingATitleFromAnUnknownSessionReportsNotFound(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, nil)

	_, _, err := engine.ReadTitle(t.Context(), "nope")
	requireCode(t, err, CodeSessionNotFound)
}

func TestCancellingATitleReadFailsTheWholeCall(t *testing.T) {
	t.Parallel()

	engine := titleEngine(t, map[session.SessionID][]session.Event{
		"s1": {titleEvent(t, 0, "一号")},
	})

	// 取消是「这次观察不作数了」，不是「这一个会话读不了」，所以它让整个调用失败。
	_, err := engine.ReadTitleSnapshots(canceledContext(), []session.SessionID{"s1"})
	requireCode(t, err, CodeAborted)
}
