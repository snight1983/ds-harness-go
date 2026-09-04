// 本文件的作用：「活的优先」这条规则在列举、精确读取、批量投影三处怎么落地，
// 以及后端失败、取消、观察对不上这些边角怎么收场。
//
// 源: packages/session-query/session-query/src/corpus.ts

package sessionquery

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// newCorpus 排一份挂了两处观察的语料，并发度用默认值。
func newCorpus(t *testing.T, live LiveSessions, store Persistence) *Corpus {
	t.Helper()

	corpus, err := NewCorpus(live, store, 0)
	if err != nil {
		t.Fatalf("语料建不出来：%v", err)
	}
	return corpus
}

// idsOf 取一串记录的 id。
func idsOf(records []Record) []sessionlog.SessionID {
	ids := make([]sessionlog.SessionID, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Header.ID)
	}
	return ids
}

// headerProjection 是一个只取会话头 id 的投影，用来观察批量投影的编排。
func headerProjection(source LogicalSource) (sessionlog.SessionID, error) {
	return source.Header.ID, nil
}

// canceledContext 造一个已经取消了的 ctx。
func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNewCorpusRefusesAnUnusableAssembly(t *testing.T) {
	t.Parallel()

	if _, err := NewCorpus(nil, newFakeStore(), 1); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("没有活会话表就没法「活的优先」，本该拒：%v", err)
	}
	if _, err := NewCorpus(newFakeLive(), newFakeStore(), -1); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("负的并发度本该拒：%v", err)
	}

	corpus, err := NewCorpus(newFakeLive(), nil, 0)
	if err != nil {
		t.Fatalf("不挂持久化后端也该建得出来：%v", err)
	}
	if corpus.persistedInspectConcurrency != DefaultPersistedInspectConcurrency {
		t.Fatalf("并发度传 0 该落到默认值，实际 %d", corpus.persistedInspectConcurrency)
	}
}

func TestListSessionsOverlaysLiveOnPersistedAndOrdersDeterministically(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("both", 200), singleUserLog(t, "活的那份"))
	live.put(testHeader("liveOnly", 300), nil)

	store := newFakeStore()
	store.put(testHeader("both", 200), singleUserLog(t, "落地那份"))
	store.put(testHeader("persistedOnly", 300), nil)

	records, err := newCorpus(t, live, store).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("列举不了：%v", err)
	}

	// 建得晚的在前；300 那一刻有两个，按 id 排。
	want := []sessionlog.SessionID{"liveOnly", "persistedOnly", "both"}
	got := idsOf(records)
	if len(got) != len(want) {
		t.Fatalf("列举结果不对：想要 %v，实际 %v", want, got)
	}
	for index, id := range got {
		if id != want[index] {
			t.Fatalf("列举顺序不对：想要 %v，实际 %v", want, got)
		}
	}

	byID := map[sessionlog.SessionID]Record{}
	for _, record := range records {
		byID[record.Header.ID] = record
	}
	if !byID["both"].Live || !byID["both"].Persisted {
		t.Fatalf("两处都有的那份该同时标上两个来源：%+v", byID["both"])
	}
	if !byID["liveOnly"].Live || byID["liveOnly"].Persisted {
		t.Fatalf("只活着的那份来源标错了：%+v", byID["liveOnly"])
	}
	if byID["persistedOnly"].Live || !byID["persistedOnly"].Persisted {
		t.Fatalf("只落地的那份来源标错了：%+v", byID["persistedOnly"])
	}
}

func TestListSessionsWithoutPersistenceSeesOnlyLiveSessions(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("a", 100), nil)

	records, err := newCorpus(t, live, nil).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("列举不了：%v", err)
	}
	if len(records) != 1 || records[0].Header.ID != "a" || records[0].Persisted {
		t.Fatalf("没挂后端时的列举结果不对：%+v", records)
	}
}

func TestListSessionsRefusesTwoObservationsThatDisagree(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("s1", 100), nil)

	store := newFakeStore()
	store.put(testHeader("s1", 999), nil)

	_, err := newCorpus(t, live, store).ListSessions(context.Background())
	requireCode(t, err, CodeSourceConflict)
}

func TestListSessionsReportsABackendFailure(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.listErr = errors.New("磁盘掉了")

	_, err := newCorpus(t, newFakeLive(), store).ListSessions(context.Background())
	requireCode(t, err, CodePersistenceFailed)
}

func TestListSessionsStopsOnACanceledContext(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.put(testHeader("a", 100), nil)

	_, err := newCorpus(t, newFakeLive(), store).ListSessions(canceledContext())
	requireCode(t, err, CodeAborted)
	if store.listCalls != 0 {
		t.Fatalf("已经取消了就不该再去问后端，实际问了 %d 次", store.listCalls)
	}
}

func TestListSessionsChecksCancellationAgainAfterListing(t *testing.T) {
	t.Parallel()

	// 列举成功返回，但调用方在这期间撤了。
	ctx, cancel := context.WithCancel(context.Background())
	store := newFakeStore()
	store.put(testHeader("a", 100), nil)
	store.afterList = cancel

	_, err := newCorpus(t, newFakeLive(), store).ListSessions(ctx)
	requireCode(t, err, CodeAborted)
}

func TestLoadPrefersTheLiveObservationAndNeverAsksPersistence(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("s1", 100), singleUserLog(t, "活的那份"))

	store := newFakeStore()
	store.listErr = errors.New("后端此刻是坏的")

	loaded, err := newCorpus(t, live, store).Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("活着的会话不该受后端故障影响：%v", err)
	}
	if loaded.Header.ID != "s1" || len(loaded.Events) != 1 {
		t.Fatalf("读出来的不是活的那份：%+v", loaded)
	}
	if store.listCalls != 0 {
		t.Fatal("认出目标是活的之后还去问了后端")
	}
}

func TestLoadHandsBackADetachedSnapshot(t *testing.T) {
	t.Parallel()

	events := singleUserLog(t, "原文")
	live := newFakeLive()
	live.put(testHeader("s1", 100), events)

	loaded, err := newCorpus(t, live, nil).Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	loaded.Events[0].Data[0] = ' '
	if events[0].Data[0] == ' ' {
		t.Fatal("读出来的日志没有脱离，调用方改到了活会话里那一份")
	}
}

func TestLoadFallsBackToPersistence(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.put(testHeader("s1", 100), singleUserLog(t, "落地那份"))

	loaded, err := newCorpus(t, newFakeLive(), store).Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	if loaded.Header.ID != "s1" || len(loaded.Events) != 1 {
		t.Fatalf("读出来的不是落地那份：%+v", loaded)
	}
}

func TestLoadRechecksWhetherTheSessionCameAliveWhileItRead(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	store := newFakeStore()
	store.put(testHeader("s1", 100), singleUserLog(t, "落地那份"))
	store.afterInspect = func(id sessionlog.SessionID) {
		// 读完落地那份之后，这个会话被挂了起来：落地那份已经旧了。
		live.put(testHeader("s1", 100), []sessionlog.Event{
			userEvent(t, 0, "落地那份"),
			userEvent(t, 1, "刚刚又说了一句"),
		})
	}

	loaded, err := newCorpus(t, live, store).Load(context.Background(), "s1")
	if err != nil {
		t.Fatalf("读不了：%v", err)
	}
	if len(loaded.Events) != 2 {
		t.Fatalf("没有改用刚刚变活的那份：%+v", loaded.Events)
	}
}

func TestLoadRefusesTwoObservationsThatDisagree(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.put(testHeader("s1", 100), nil)
	// 列举给出的头和 Inspect 给出的头对不上。
	store.inspectMeta = map[sessionlog.SessionID]sessionlog.SessionHeader{"s1": testHeader("s1", 999)}

	_, err := newCorpus(t, newFakeLive(), store).Load(context.Background(), "s1")
	requireCode(t, err, CodeSourceConflict)
}

func TestLoadReportsEveryWayItCanFail(t *testing.T) {
	t.Parallel()

	corrupt := newFakeStore()
	corrupt.put(testHeader("s1", 100), nil)
	corrupt.inspectErr["s1"] = &persistence.CorruptionError{ID: "s1", Cause: errors.New("重放没通过")}

	broken := newFakeStore()
	broken.put(testHeader("s1", 100), nil)
	broken.inspectErr["s1"] = errors.New("磁盘掉了")

	listBroken := newFakeStore()
	listBroken.listErr = errors.New("磁盘掉了")

	cases := map[string]struct {
		store Persistence
		id    sessionlog.SessionID
		want  Code
	}{
		"没挂后端又不活着": {store: nil, id: "s1", want: CodeSessionNotFound},
		"后端里也没有":   {store: newFakeStore(), id: "s1", want: CodeSessionNotFound},
		"列举失败":     {store: listBroken, id: "s1", want: CodePersistenceFailed},
		"存档坏了":     {store: corrupt, id: "s1", want: CodeCorruptSession},
		"这一次没读成":   {store: broken, id: "s1", want: CodePersistenceFailed},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := newCorpus(t, newFakeLive(), testCase.store).Load(context.Background(), testCase.id)
			requireCode(t, err, testCase.want)
		})
	}
}

func TestLoadStopsOnACanceledContext(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("s1", 100), nil)

	_, err := newCorpus(t, live, newFakeStore()).Load(canceledContext(), "s1")
	requireCode(t, err, CodeAborted)
}

func TestLoadCallsAFailedInspectCancellationWhenTheCallerAlreadyWalkedAway(t *testing.T) {
	t.Parallel()

	// 列举成功返回、调用方在那之后撤了，然后读落地日志失败。这一刻后端报的
	// 是什么已经不重要了：调用方要的是「这次没结果」，不是「磁盘出了什么事」。
	ctx, cancel := context.WithCancel(context.Background())
	store := newFakeStore()
	store.put(testHeader("s1", 100), nil)
	store.afterList = cancel
	store.inspectErr["s1"] = errors.New("磁盘掉了")

	_, err := newCorpus(t, newFakeLive(), store).Load(ctx, "s1")
	requireCode(t, err, CodeAborted)
}

func TestProjectManyDedupesAndKeepsFirstAppearanceOrder(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("a", 100), nil)
	live.put(testHeader("b", 200), nil)

	results, err := ProjectMany(context.Background(), newCorpus(t, live, nil),
		[]sessionlog.SessionID{"b", "a", "b"}, headerProjection)
	if err != nil {
		t.Fatalf("投影不了：%v", err)
	}
	if len(results) != 2 || results[0].SessionID != "b" || results[1].SessionID != "a" {
		t.Fatalf("去重或顺序不对：%+v", results)
	}
	for _, result := range results {
		if result.Err != nil || result.Value != result.SessionID {
			t.Fatalf("投影结果不对：%+v", result)
		}
	}
}

func TestProjectManyIsolatesASingleSourceFailure(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("好的", 100), nil)

	store := newFakeStore()
	store.put(testHeader("坏的", 200), nil)
	store.inspectErr["坏的"] = errors.New("磁盘掉了")

	results, err := ProjectMany(context.Background(), newCorpus(t, live, store),
		[]sessionlog.SessionID{"好的", "坏的", "不存在"}, headerProjection)
	if err != nil {
		t.Fatalf("单个源的失败不该让整个调用失败：%v", err)
	}
	if results[0].Err != nil || results[0].Value != "好的" {
		t.Fatalf("好的那份被连累了：%+v", results[0])
	}
	requireCode(t, results[1].Err, CodePersistenceFailed)
	requireCode(t, results[2].Err, CodeSessionNotFound)
}

func TestProjectManyReportsAFailingProjector(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("a", 100), nil)

	boom := errors.New("投影函数自己炸了")
	results, err := ProjectMany(context.Background(), newCorpus(t, live, nil),
		[]sessionlog.SessionID{"a"}, func(LogicalSource) (int, error) { return 0, boom })
	if err != nil {
		t.Fatalf("投影函数炸了不该让整个调用失败：%v", err)
	}
	if !errors.Is(results[0].Err, boom) {
		t.Fatalf("投影函数的失败没被收进结果：%+v", results[0])
	}
}

func TestProjectManyWithoutPersistenceMarksEveryUnresolvedID(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("a", 100), nil)

	results, err := ProjectMany(context.Background(), newCorpus(t, live, nil),
		[]sessionlog.SessionID{"a", "b"}, headerProjection)
	if err != nil {
		t.Fatalf("投影不了：%v", err)
	}
	if results[0].Err != nil {
		t.Fatalf("活着的那份不该受影响：%+v", results[0])
	}
	requireCode(t, results[1].Err, CodeSessionNotFound)
}

func TestProjectManySpreadsAListingFailureOverThePendingIDs(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	live.put(testHeader("活的", 100), nil)

	store := newFakeStore()
	store.listErr = errors.New("磁盘掉了")

	results, err := ProjectMany(context.Background(), newCorpus(t, live, store),
		[]sessionlog.SessionID{"活的", "要去后端找的"}, headerProjection)
	if err != nil {
		t.Fatalf("列举失败不该扔掉已经投影好的那些：%v", err)
	}
	if results[0].Err != nil || results[0].Value != "活的" {
		t.Fatalf("已经投影好的那份被扔了：%+v", results[0])
	}
	requireCode(t, results[1].Err, CodePersistenceFailed)
}

func TestProjectManyFailsWholeOperationOnCancellation(t *testing.T) {
	t.Parallel()

	t.Run("进门就已经取消", func(t *testing.T) {
		t.Parallel()

		_, err := ProjectMany(canceledContext(), newCorpus(t, newFakeLive(), newFakeStore()),
			[]sessionlog.SessionID{"a"}, headerProjection)
		requireCode(t, err, CodeAborted)
	})

	t.Run("列举时被取消", func(t *testing.T) {
		t.Parallel()

		store := newFakeStore()
		store.listErr = context.Canceled

		_, err := ProjectMany(context.Background(), newCorpus(t, newFakeLive(), store),
			[]sessionlog.SessionID{"a"}, headerProjection)
		requireCode(t, err, CodeAborted)
	})

	t.Run("读落地日志时被取消", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		store := newFakeStore()
		store.put(testHeader("a", 100), nil)
		store.afterInspect = func(sessionlog.SessionID) { cancel() }

		_, err := ProjectMany(ctx, newCorpus(t, newFakeLive(), store),
			[]sessionlog.SessionID{"a"}, headerProjection)
		requireCode(t, err, CodeAborted)
	})
}

func TestProjectManyPicksUpASessionThatCameAliveMidFlight(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	store := newFakeStore()
	store.put(testHeader("落地的", 100), nil)
	store.afterInspect = func(id sessionlog.SessionID) {
		live.put(testHeader(id, 100), singleUserLog(t, "刚刚活过来"))
	}

	results, err := ProjectMany(context.Background(), newCorpus(t, live, store),
		[]sessionlog.SessionID{"落地的"}, func(source LogicalSource) (int, error) {
			return len(source.Events), nil
		})
	if err != nil {
		t.Fatalf("投影不了：%v", err)
	}
	if results[0].Err != nil || results[0].Value != 1 {
		t.Fatalf("没有改用刚刚变活的那份：%+v", results[0])
	}
}

func TestProjectManyPicksUpAnIDThatWasNotInTheListing(t *testing.T) {
	t.Parallel()

	live := newFakeLive()
	store := newFakeStore()
	store.put(testHeader("别人", 100), nil)
	// 列举之后、解析之前，目标才被挂起来。
	store.afterList = func() { live.put(testHeader("迟到的", 100), nil) }

	results, err := ProjectMany(context.Background(), newCorpus(t, live, store),
		[]sessionlog.SessionID{"迟到的"}, headerProjection)
	if err != nil {
		t.Fatalf("投影不了：%v", err)
	}
	if results[0].Err != nil || results[0].Value != "迟到的" {
		t.Fatalf("列举之后才变活的那份没被认出来：%+v", results[0])
	}
}

func TestProjectManyRefusesTwoObservationsThatDisagree(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.put(testHeader("s1", 100), nil)
	store.inspectMeta = map[sessionlog.SessionID]sessionlog.SessionHeader{"s1": testHeader("s1", 999)}

	results, err := ProjectMany(context.Background(), newCorpus(t, newFakeLive(), store),
		[]sessionlog.SessionID{"s1"}, headerProjection)
	if err != nil {
		t.Fatalf("观察对不上是单个源的事：%v", err)
	}
	requireCode(t, results[0].Err, CodeSourceConflict)
}

func TestProjectManyHonorsTheConcurrencyCeilingAndRunsProjectionsSerially(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	var ids []sessionlog.SessionID
	for _, id := range []sessionlog.SessionID{"a", "b", "c", "d", "e", "f", "g", "h"} {
		store.put(testHeader(id, 100), nil)
		ids = append(ids, id)
	}
	// 一道两人的栅栏：头两个 Inspect 必须同时在里面，第三个之后直接放行。
	// 并发上限真的没生效的话这里会死等，比事后看一个采样来的计数可靠。
	gate := make(chan struct{})
	var mutex sync.Mutex
	arrivals := 0
	store.afterInspect = func(sessionlog.SessionID) {
		mutex.Lock()
		arrivals++
		last := arrivals == 2
		mutex.Unlock()
		if last {
			close(gate)
			return
		}
		<-gate
	}

	corpus, err := NewCorpus(newFakeLive(), store, 2)
	if err != nil {
		t.Fatalf("语料建不出来：%v", err)
	}

	// 投影只在调用方那条 goroutine 上跑，所以这个不加锁的计数器是安全的——
	// 它同时也是这条契约的断言：真并发起来 -race 会当场把它抓出来。
	projections := 0
	results, err := ProjectMany(context.Background(), corpus, ids, func(LogicalSource) (int, error) {
		projections++
		return projections, nil
	})
	if err != nil {
		t.Fatalf("投影不了：%v", err)
	}
	if len(results) != len(ids) || projections != len(ids) {
		t.Fatalf("投影次数不对：想要 %d，实际 %d", len(ids), projections)
	}
	if store.livePeak != 2 {
		t.Fatalf("同时读的落地日志数不是并发上限：峰值 %d", store.livePeak)
	}
}

func TestProjectManyOnAnEmptyIDListDoesNothing(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	results, err := ProjectMany(context.Background(), newCorpus(t, newFakeLive(), store),
		nil, headerProjection)
	if err != nil {
		t.Fatalf("空清单不该出错：%v", err)
	}
	if len(results) != 0 {
		t.Fatalf("空清单不该有结果：%+v", results)
	}
	if store.listCalls != 0 {
		t.Fatal("空清单不该去问后端")
	}
}
