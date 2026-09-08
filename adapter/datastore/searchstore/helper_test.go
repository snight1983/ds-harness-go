// 本文件的作用：本包测试共用的那几样——这一轮跑在哪种库上、活会话表和持久化后端
// 的替身，以及排一份会话日志出来的那几下。
//
// 这一轮跑在哪种库上由 [dbtest] 定：缺省 SQLite，设了 DSH_POSTGRES_DSN 就整批改跑
// Postgres。两种都要跑得过——这个包的排序和翻页恰恰压在两种库会分歧的那些地方。

package searchstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/adapter/datastore/internal/dbtest"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

// fakeLive 是一张可以在用例中途被改动的活会话表。
type fakeLive struct {
	mutex   sync.Mutex
	sources map[sessionlog.SessionID]sessionquery.LogicalSource
	order   []sessionlog.SessionID
}

func newFakeLive() *fakeLive {
	return &fakeLive{sources: map[sessionlog.SessionID]sessionquery.LogicalSource{}}
}

func (f *fakeLive) put(header sessionlog.SessionHeader, events []sessionlog.Event) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if _, ok := f.sources[header.ID]; !ok {
		f.order = append(f.order, header.ID)
	}
	f.sources[header.ID] = sessionquery.LogicalSource{Header: header, Events: events}
}

func (f *fakeLive) drop(id sessionlog.SessionID) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	delete(f.sources, id)
	for at, kept := range f.order {
		if kept == id {
			f.order = append(f.order[:at], f.order[at+1:]...)
			break
		}
	}
}

func (f *fakeLive) Get(id sessionlog.SessionID) (sessionquery.LogicalSource, bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	source, ok := f.sources[id]
	return source, ok
}

func (f *fakeLive) List() []sessionquery.LogicalSource {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	list := make([]sessionquery.LogicalSource, 0, len(f.order))
	for _, id := range f.order {
		list = append(list, f.sources[id])
	}
	return list
}

// fakeStore 是一个持久化后端替身：每份会话带一个可以被改动的变更令牌。
type fakeStore struct {
	mutex     sync.Mutex
	order     []sessionlog.SessionID
	headers   map[sessionlog.SessionID]sessionlog.SessionHeader
	events    map[sessionlog.SessionID][]sessionlog.Event
	revisions map[sessionlog.SessionID]persistence.Revision

	listErr      error
	inspectErr   map[sessionlog.SessionID]error
	inspectCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		headers:    map[sessionlog.SessionID]sessionlog.SessionHeader{},
		events:     map[sessionlog.SessionID][]sessionlog.Event{},
		revisions:  map[sessionlog.SessionID]persistence.Revision{},
		inspectErr: map[sessionlog.SessionID]error{},
	}
}

// put 放一份落地会话；重复放同一个 id 会换掉内容并换一个新令牌。
func (f *fakeStore) put(
	header sessionlog.SessionHeader,
	revision persistence.Revision,
	events []sessionlog.Event,
) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if _, ok := f.headers[header.ID]; !ok {
		f.order = append(f.order, header.ID)
	}
	f.headers[header.ID] = header
	f.events[header.ID] = events
	f.revisions[header.ID] = revision
}

func (f *fakeStore) drop(id sessionlog.SessionID) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	delete(f.headers, id)
	delete(f.events, id)
	delete(f.revisions, id)
	for at, kept := range f.order {
		if kept == id {
			f.order = append(f.order[:at], f.order[at+1:]...)
			break
		}
	}
}

func (f *fakeStore) ListSnapshots(context.Context) ([]persistence.Snapshot, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if f.listErr != nil {
		return nil, f.listErr
	}
	snapshots := make([]persistence.Snapshot, 0, len(f.order))
	for _, id := range f.order {
		snapshots = append(snapshots, persistence.Snapshot{
			Header: f.headers[id], Revision: f.revisions[id],
		})
	}
	return snapshots, nil
}

// List 是 [sessionquery.Persistence] 那一半，只在把这个后端挂进引擎的那条用例里用得着。
func (f *fakeStore) List(context.Context) ([]sessionlog.SessionHeader, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	headers := make([]sessionlog.SessionHeader, 0, len(f.order))
	for _, id := range f.order {
		headers = append(headers, f.headers[id])
	}
	return headers, nil
}

func (f *fakeStore) Inspect(
	_ context.Context,
	id sessionlog.SessionID,
) (persistence.Inspection, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.inspectCalls++
	if err := f.inspectErr[id]; err != nil {
		return persistence.Inspection{}, err
	}
	header, ok := f.headers[id]
	if !ok {
		return persistence.Inspection{}, errors.New("找不到会话 " + string(id))
	}
	return persistence.Inspection{Meta: header, Events: f.events[id]}, nil
}

// reads 交出到目前为止读过几次整份日志，用来验对账那一步真的跳过了没变的那些。
func (f *fakeStore) reads() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	return f.inspectCalls
}

// newStore 在一份全新介质上开一个检索后端，用例结束时收掉。
func newStore(t *testing.T, live sessionquery.LiveSessions, store Persistence) *Store {
	t.Helper()

	return newStoreWith(t, Config{Live: live, Persistence: store})
}

// newStoreWith 按一份给定配置开一个检索后端，介质那几项由本函数补齐。
func newStoreWith(t *testing.T, config Config) *Store {
	t.Helper()

	dsn := dbtest.DSN()
	medium, db := dbtest.Config(t, dsn, dbtest.Namespace(t, "search_test", dsn))
	config.Medium = medium

	search, err := New(t.Context(), config)
	if err != nil {
		_ = db.Close()
		t.Fatalf("打开检索后端不该失败：%v", err)
	}
	t.Cleanup(func() {
		if closeErr := search.Close(context.Background()); closeErr != nil {
			t.Errorf("关检索后端不该失败：%v", closeErr)
		}
	})
	return search
}

// testHeader 排一个会话头出来。
func testHeader(id sessionlog.SessionID, createdAt int64) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{Version: 1, ID: id, CreatedAt: createdAt, WorkspaceID: "ws-1"}
}

// userEvent 排一条追加进表面的用户消息。
func userEvent(t *testing.T, seq int, text string) sessionlog.Event {
	t.Helper()

	data, err := json.Marshal(sessionlog.UserMessageData{Message: llm.Message{
		ID:      llm.MessageID("u" + text),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}})
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Seq:       seq,
		Time:      int64(seq) * 1000,
		Data:      data,
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// log 把若干段文字排成一份合法日志，seq 从 0 起。
func logOf(t *testing.T, texts ...string) []sessionlog.Event {
	t.Helper()

	events := make([]sessionlog.Event, 0, len(texts))
	for seq, text := range texts {
		events = append(events, userEvent(t, seq, text))
	}
	return events
}

// textsOf 把一页事件命中的摘录抽出来。
func textsOf(hits []sessionquery.EventSearchHit) []string {
	texts := make([]string, 0, len(hits))
	for _, hit := range hits {
		texts = append(texts, hit.Snippet)
	}
	return texts
}

// idsOf 把一页会话命中的 id 抽出来。
func idsOf(hits []sessionquery.SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, string(hit.Record.Header.ID))
	}
	return ids
}

// requireCode 断言一条错误带着预期的分类码。
func requireCode(t *testing.T, err error, want sessionquery.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("想要 %s，实际没报错", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("错误码不对：想要 %s，实际 %v", want, err)
	}
}
