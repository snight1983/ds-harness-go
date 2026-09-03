// 本文件的作用：本包测试共用的那几样——一个假活会话、一个只会 ReadFrom 的假存储、
// 一个能被断言的日志接收器，以及把「中枢 + 后端 + 设施 + 域 + 缓存」装起来的那套装配。

package projectioncache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
	"github.com/snight1983/ds-harness-go/session/projection"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// fakeLive 是一个只把头和日志摆在那儿的活会话。
type fakeLive struct {
	header session.SessionHeader
	events []session.Event
}

func (l *fakeLive) ID() session.SessionID { return l.header.ID }

func (l *fakeLive) Events() []session.Event { return l.events }

func (l *fakeLive) NextSeq() int {
	if len(l.events) == 0 {
		return 0
	}
	return l.events[len(l.events)-1].Seq + 1
}

func (l *fakeLive) Header() session.SessionHeader { return l.header }

// newLive 造一个带着这些事件的假活会话，建会话时刻固定，好让身份可预期。
func newLive(id session.SessionID, createdAt int64, events ...session.Event) *fakeLive {
	return &fakeLive{
		header: session.SessionHeader{Version: 1, ID: id, CreatedAt: createdAt},
		events: events,
	}
}

// userEvent 造一条会被计数单元数进去的事件。
func userEvent(seq int) session.Event {
	return session.Event{Type: session.EventUserMessage, Seq: seq, Data: json.RawMessage(`{}`)}
}

// otherEvent 造一条计数单元不关心的事件。
func otherEvent(seq int) session.Event {
	return session.Event{Type: session.EventTodoWrite, Seq: seq, Data: json.RawMessage(`{}`)}
}

// turnEndEvent 造一条回合结束——[Cache.Observe] 的必写点。
func turnEndEvent(seq int) session.Event {
	return session.Event{Type: session.EventTurnEnd, Seq: seq, Data: json.RawMessage(`{}`)}
}

// countState 是计数单元的状态。
type countState struct {
	Count int `json:"count"`
}

// countUnit 是一个数 user/message 的客户端可见单元。
func countUnit(key string, version int) projection.Definition[countState] {
	return projection.Definition[countState]{
		Key:          key,
		StateVersion: version,
		Init:         func() countState { return countState{} },
		Apply: func(state countState, event session.Event) (countState, bool) {
			if event.Type != session.EventUserMessage {
				return state, false
			}
			state.Count++
			return state, true
		},
		DecodeState: projection.StrictDecoder[countState](),
		View:        func(state countState) any { return state.Count },
	}
}

// hostOnlyUnit 是同一份折叠，但没有客户端视图。
func hostOnlyUnit(key string, version int) projection.Definition[countState] {
	definition := countUnit(key, version)
	definition.View = nil
	return definition
}

// badState 是一个排不出去的状态：它带一个函数字段，[json.Marshal] 一定砸。
type badState struct {
	Bad func() `json:"bad"`
}

// unmarshalableUnit 是一个状态排不出去的单元，用来驱动 [Cache.Write] 的取切面失败。
func unmarshalableUnit(key string) projection.Definition[badState] {
	return projection.Definition[badState]{
		Key:          key,
		StateVersion: 0,
		Init:         func() badState { return badState{Bad: func() {}} },
		Apply:        func(state badState, _ session.Event) (badState, bool) { return state, false },
		DecodeState:  func(json.RawMessage) (badState, error) { return badState{}, nil },
		View:         func(badState) any { return nil },
	}
}

// countRow 造一条计数单元的检查点行。
func countRow(t *testing.T, version, seq, count int) projection.CheckpointRow {
	t.Helper()

	encoded, err := json.Marshal(countState{Count: count})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	return projection.CheckpointRow{Ver: version, Seq: seq, Val: encoded}
}

// errNotImplemented 是假存储那些用不到的方法统一给出的说法：本包一个都不该调到它们。
var errNotImplemented = errors.New("假存储没实现这个方法")

// fakeStore 是一个只有 [persistence.Store.ReadFrom] 有意义的假存储。
//
// 它记下每一次读的水位，好让「冷读到底从哪儿起读」这件事能被直接断言——
// 那正是这一层存在的全部理由。
type fakeStore struct {
	mu sync.Mutex
	// logs 是每个会话在存储里那份日志；键不在表里就是「没有这个会话」。
	logs map[session.SessionID][]session.Event
	// metas 是每个会话在存档里那份头。
	metas map[session.SessionID]session.SessionHeader
	// froms 是历次 ReadFrom 的水位，按调用顺序。
	froms []int
	// err 是安排好的失败；从第 failFrom 次读起生效。
	err error
	// failFrom 是失败从第几次读开始（从 0 数）。
	failFrom int
}

func newStore() *fakeStore {
	return &fakeStore{
		logs:     map[session.SessionID][]session.Event{},
		metas:    map[session.SessionID]session.SessionHeader{},
		failFrom: math.MaxInt,
	}
}

// put 把一份存档摆进假存储。
func (s *fakeStore) put(meta session.SessionHeader, events ...session.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metas[meta.ID] = meta
	s.logs[meta.ID] = events
}

// failWith 让第 from 次（从 0 数）及其之后的每次 ReadFrom 都失败。
//
// 「从第几次起」这个旋钮是为冷读那条回退路准备的：整读是**第二**次读，
// 只有让第一次成功、第二次失败，才走得到「回退之后又失败」那一段。
func (s *fakeStore) failWith(from int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failFrom, s.err = from, err
}

// reads 给出历次 ReadFrom 的水位。
func (s *fakeStore) reads() []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]int(nil), s.froms...)
}

func (s *fakeStore) ReadFrom(_ context.Context, id session.SessionID, fromSeq int) (persistence.StoredSuffix, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	call := len(s.froms)
	s.froms = append(s.froms, fromSeq)
	if s.err != nil && call >= s.failFrom {
		return persistence.StoredSuffix{}, s.err
	}
	if fromSeq < 0 {
		return persistence.StoredSuffix{}, persistence.ErrMalformedSeq
	}
	events, ok := s.logs[id]
	if !ok {
		return persistence.StoredSuffix{}, persistence.ErrSessionNotFound
	}
	var tail []session.Event
	for _, event := range events {
		if event.Seq >= fromSeq {
			tail = append(tail, event)
		}
	}
	// BaseSeq 是**整份存档**现存最早一条事件的 seq，不是这一截后缀的起点，
	// 所以用的是 events 不是 tail。见 [persistence.StoredSuffix]。
	return persistence.StoredSuffix{
		Meta:    s.metas[id],
		Events:  tail,
		BaseSeq: session.LogBaseSeq(events),
	}, nil
}

// 下面这些方法本包一条路都走不到，一律拒绝——真被调到时测试会当场看见，
// 而不是拿一个零值继续往下跑。

func (s *fakeStore) Locate(session.SessionHeader) (persistence.Location, bool) {
	return persistence.Location{}, false
}

func (s *fakeStore) SupportsRawArtifacts() bool { return false }

func (s *fakeStore) ReadRaw(context.Context, session.SessionID) (persistence.RawArtifact, error) {
	return persistence.RawArtifact{}, errNotImplemented
}

func (s *fakeStore) Create(context.Context, session.SessionHeader) error { return errNotImplemented }

func (s *fakeStore) Append(context.Context, session.SessionID, []session.Event) error {
	return errNotImplemented
}

func (s *fakeStore) Load(context.Context, session.SessionID) (persistence.Inspection, error) {
	return persistence.Inspection{}, errNotImplemented
}

func (s *fakeStore) Inspect(context.Context, session.SessionID) (persistence.Inspection, error) {
	return persistence.Inspection{}, errNotImplemented
}

func (s *fakeStore) List(context.Context) ([]session.SessionHeader, error) {
	return nil, errNotImplemented
}

func (s *fakeStore) ListSnapshots(context.Context) ([]persistence.Snapshot, error) {
	return nil, errNotImplemented
}

// logSink 收下缓存打出来的每一条日志，供断言。
//
// 那几条 Warn 是本包 fail-soft 路径唯一留下的痕迹（见包文档第 4 条），
// 「回退了但没留下痕迹」和「没回退」在别的地方分不出来，只能在这里分。
type logSink struct {
	mu      sync.Mutex
	entries []slog.Record
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, record slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, record.Clone())
	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *logSink) WithGroup(string) slog.Handler { return s }

// messages 给出收到的每一条日志的正文。
func (s *logSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry.Message)
	}
	return out
}

// attr 给出第 index 条日志上某个属性的字符串形式，没有那个属性时给空串。
func (s *logSink) attr(index int, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if index >= len(s.entries) {
		return ""
	}
	found := ""
	s.entries[index].Attrs(func(candidate slog.Attr) bool {
		if candidate.Key == key {
			found = candidate.Value.String()
			return false
		}
		return true
	})
	return found
}

// quiet 是一个什么都不记的 logger，给那些不看日志的用例用。
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// openDomain 在一份全新的内存介质上按 spec 打开一个域，测试结束时关掉。
//
// 每个用例一份介质：本包的用例大量并行，共用介质会让「介质上现在是什么」
// 这件事取决于别的用例跑到哪儿了。
func openDomain(t *testing.T, spec domain.Spec) *domain.Domain {
	t.Helper()

	return openDomainOn(t, spec, storagetest.NewMemoryBackend(storagetest.NewMemoryMedium()))
}

// openDomainOn 是 [openDomain] 的底子，介质那一层由调用方给。
func openDomainOn(t *testing.T, spec domain.Spec, backend storage.Backend) *domain.Domain {
	t.Helper()

	hub := storage.New()
	if _, err := hub.Backend.Register("main", backend); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}
	facility, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}
	opened, err := facility.Open(context.Background(), spec)
	if err != nil {
		t.Fatalf("打开域不该失败：%v", err)
	}
	t.Cleanup(func() {
		if closeErr := opened.Close(context.Background()); closeErr != nil {
			t.Errorf("关域不该失败：%v", closeErr)
		}
	})
	return opened
}

// ctxAwareBackend 是套在内存后端外面的一层薄包装，它让 context 的取消
// 真的能拦住一次写。
//
// storagetest 那个内存后端整个忽略 ctx（见 storagetest/memory.go 里 PutRecord
// 的第一个参数就叫 `_`），所以「后台写的 context 就是这个缓存自己的寿命」
// 这条约定在它上面根本观察不到——真正的后端会认，测试用的这个不认。
type ctxAwareBackend struct{ storage.KVProvider }

func (b ctxAwareBackend) KV() storage.KVFacet { return ctxAwareFacet{b.KVProvider.KV()} }

type ctxAwareFacet struct{ storage.KVFacet }

func (f ctxAwareFacet) Open(ctx context.Context, descriptor storage.KVUnitDescriptor) (storage.KVUnit, error) {
	unit, err := f.KVFacet.Open(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	return ctxAwareUnit{unit}, nil
}

type ctxAwareUnit struct{ storage.KVUnit }

func (u ctxAwareUnit) PutRecord(
	ctx context.Context,
	table, key string,
	value json.RawMessage,
	expected storage.WriteIntent,
) (storage.Revision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return u.KVUnit.PutRecord(ctx, table, key, value, expected)
}

// fixture 是一次完整装配：缓存本身，加上能从外面观察到的那几个侧面。
type fixture struct {
	cache    *Cache
	registry *projection.Registry
	store    *fakeStore
	sink     *logSink
	// opened 是这套装配用的那个域；用例要驱动介质失败时把它关掉。
	opened *domain.Domain
	table  *domain.Table[Record]

	mu       sync.Mutex
	flushed  []session.SessionID
	flushErr error
	// flushHook 在记账之前跑，非 nil 时它的返回值就是这次屏障的结果。
	//
	// 它是**顺序探针**：屏障跑到的那一刻，检查点已经取完、记录还没写下去，
	// 所以在这里回头看介质，就能证明 put 确实排在 flush 后面。
	flushHook func(live LiveSession) error
}

// newFixture 按给定的两个节流阈值装一套缓存出来，登记表是空的——
// 要哪个单元由用例自己登记。
func newFixture(t *testing.T, every int, interval time.Duration) *fixture {
	t.Helper()

	opened := openDomain(t, Spec())
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		t.Fatalf("取表不该失败：%v", err)
	}

	f := &fixture{
		registry: projection.NewRegistry(),
		store:    newStore(),
		sink:     &logSink{},
		opened:   opened,
		table:    table,
	}
	cache, err := New(opened, Options{
		Registry:         f.registry,
		Store:            f.store,
		Flush:            f.flush,
		WriteEveryEvents: every,
		WriteInterval:    interval,
		Logger:           slog.New(f.sink),
	})
	if err != nil {
		t.Fatalf("建缓存不该失败：%v", err)
	}
	f.cache = cache
	t.Cleanup(cache.Close)
	return f
}

// flush 是递给 [Options.Flush] 的那道落盘屏障，记下刷过谁。
func (f *fixture) flush(live LiveSession) error {
	f.mu.Lock()
	hook, err := f.flushHook, f.flushErr
	f.mu.Unlock()

	if hook != nil {
		if hookErr := hook(live); hookErr != nil {
			return hookErr
		}
	}
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushed = append(f.flushed, live.ID())
	return nil
}

// onFlush 装一个顺序探针，见 [fixture.flushHook]。
func (f *fixture) onFlush(hook func(live LiveSession) error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.flushHook = hook
}

// failFlush 让后续每次落盘屏障都失败。
func (f *fixture) failFlush(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.flushErr = err
}

// flushes 给出落盘屏障被调过的那些会话。
func (f *fixture) flushes() []session.SessionID {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]session.SessionID(nil), f.flushed...)
}

// mustRegister 登记一个单元，登记失败就判测试失败；注销挂在测试结束时。
func mustRegister[S any](t *testing.T, registry *projection.Registry, definition projection.Definition[S]) {
	t.Helper()

	dispose, err := projection.Register(registry, definition)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	t.Cleanup(dispose)
}

// record 读出介质上那条记录，读不到就判测试失败。
func (f *fixture) record(t *testing.T, id session.SessionID) Record {
	t.Helper()

	stored, ok, err := f.table.Get(t.Context(), string(id))
	if err != nil {
		t.Fatalf("读记录不该失败：%v", err)
	}
	if !ok {
		t.Fatalf("会话 %s 该有一条记录", id)
	}
	return stored
}

// stored 说明介质上现在有没有这个会话的记录。
//
// 它是给 [waitFor] 用的：落盘屏障记账排在 [Cache.put] **前面**，所以「屏障调过了」
// 这个位比记录真的落下去早一步，等它等不到记录。读失败在这里当成「还没有」，
// 用例随后那次 [fixture.record] 会把它报出来。
func (f *fixture) stored(ctx context.Context, id session.SessionID) bool {
	_, ok, _ := f.table.Get(ctx, string(id))
	return ok
}

// waitFor 等一个条件成立，等不到就判测试失败。
//
// 节流触发的写跑在别的 goroutine 上（见 [Cache.Observe]），没有同步点可等，
// 只能轮询。轮询间隔取得很短，正常情况下第一两轮就成立。
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等不到：%s", what)
}

// otherSpec 是一份**别的**域声明，用来驱动「域不是按 [Spec] 打开的」那条路。
func otherSpec() domain.Spec {
	return domain.Spec{
		Name:    "projcache_other",
		Version: 1,
		Tables:  []domain.TableSpec{domain.DefineTable("other_table", func(int) error { return nil })},
	}
}

// wrongTypeSpec 表名对得上、记录类型对不上，驱动 [domain.TableOf] 的另一条拒绝路径。
func wrongTypeSpec() domain.Spec {
	return domain.Spec{
		Name:    "projcache_wrong",
		Version: 1,
		Tables:  []domain.TableSpec{domain.DefineTable(TableName, func(int) error { return nil })},
	}
}

// validOptions 是一份能建出缓存的最小配置，用例在它上面改一个字段来驱动各条拒绝。
func validOptions(f *fixture) Options {
	return Options{
		Registry:         projection.NewRegistry(),
		Store:            f.store,
		Flush:            func(LiveSession) error { return nil },
		WriteEveryEvents: 1,
		WriteInterval:    time.Second,
	}
}

// describe 是错误信息里那一小段，把 nil 和非 nil 说清楚。
func describe(err error) string {
	if err == nil {
		return "无错误"
	}
	return fmt.Sprintf("%v", err)
}
