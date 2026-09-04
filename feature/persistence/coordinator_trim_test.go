// 本文件的作用：压条数上限那条路——谁来算「弹到哪儿」、起点跟不跟得上、
// 弹不动的时候那次写还算不算成功，以及一个弹不动的后端上什么都不该发生。
//
// # 这些测试防的是什么错
//
//   - 存档无上限地长下去，而这正是这次改造要治的病。
//   - 弹出失败被当成写失败往上报，于是写入方回头重发一批已经在盘上的事件。
//   - 起点在事件真被弹掉之前就推上去，于是一段还在盘上的事件被读成不存在。
//   - 一个弹不动的后端被当成弹得动，编排器把起点推到一个介质并不认的位置。
//
// 新增: 整个文件都是本仓库自有的，上游的日志只追加、永不删除。
// 规则见 docs/session-log-limit.md 的决定第 1、3、4、13、15 条。

package persistence

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// trimmingBackend 把内存后端裹成一个弹得动的后端。
type trimmingBackend struct {
	*memoryBackend

	// trimErr 非 nil 时每一次 TrimBefore 都以它失败。
	trimErr error
	// trims 记下 TrimBefore 收到过的那几个 beforeSeq。
	trims []int
}

var _ TrimmingBackend = (*trimmingBackend)(nil)

func (b *trimmingBackend) TrimBefore(_ context.Context, id sessionlog.SessionID, beforeSeq int) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.trimErr != nil {
		return b.trimErr
	}
	log, ok := b.logs[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, string(id))
	}
	b.trims = append(b.trims, beforeSeq)

	kept := make([]sessionlog.Event, 0, len(log.events))
	for _, event := range log.events {
		if event.Seq >= beforeSeq {
			kept = append(kept, event)
		}
	}
	log.events = kept
	return nil
}

// trimCalls 交出 TrimBefore 到此为止收到过的那几个 beforeSeq。
func (b *trimmingBackend) trimCalls() []int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return append([]int(nil), b.trims...)
}

// newTrimmingHarness 搭一套挂着弹得动后端、上限为 limit 的编排器。
func newTrimmingHarness(t *testing.T, limit int) (*harness, *trimmingBackend) {
	t.Helper()

	var trimming *trimmingBackend
	h := newHarnessWith(t, func(memory *memoryBackend) Backend {
		trimming = &trimmingBackend{memoryBackend: memory}
		return trimming
	}, CoordinatorOptions{MaxStoredEvents: limit})
	return h, trimming
}

// fillTo 一条一条地写到 seq 为 count-1，每条各一次 Append。
//
// 一条一批而不是一次一大批，是因为要压的正是「每写一批之后回头看一眼」那条路：
// 一次写完的话，上限只会被跨过一次。
func fillTo(t *testing.T, h *harness, id sessionlog.SessionID, count int) {
	t.Helper()

	for seq := range count {
		if err := h.Append(t.Context(), id, []sessionlog.Event{
			userEvent(t, seq, fmt.Sprintf("第%d条", seq)),
		}); err != nil {
			t.Fatalf("写 seq %d 失败：%v", seq, err)
		}
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

func TestTheArchiveStopsGrowingAtTheLimit(t *testing.T) {
	h, backend := newTrimmingHarness(t, 4)
	meta := testHeader(t, "capped")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	fillTo(t, h, meta.ID, 10)

	stored := backend.storedEvents(meta.ID)
	if len(stored) != 4 {
		t.Fatalf("存档里剩 %d 条，上限是 4 条", len(stored))
	}
	// 留下的必须是**最新**的那几条：先进先出丢的是最老那头。
	if got, want := seqsOf(stored), []int{6, 7, 8, 9}; !slices.Equal(got, want) {
		t.Fatalf("留下来的 seq 是 %v，要的是 %v", got, want)
	}
}

// 上限之内一下都不该动介质：一次白跑的 DELETE 不算错，但它说明这里在拿一个
// 编排器自己就能算出来的判断去问介质。
func TestNothingIsEvictedWhileTheArchiveFitsTheLimit(t *testing.T) {
	h, backend := newTrimmingHarness(t, 4)
	meta := testHeader(t, "fits")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	fillTo(t, h, meta.ID, 4)

	if calls := backend.trimCalls(); len(calls) != 0 {
		t.Fatalf("没超上限却弹了 %d 次：%v", len(calls), calls)
	}
}

// 起点是读那一侧分辨「被弹掉了」和「日志真坏了」的依据，所以它必须跟着弹出走。
func TestTheBaseSeqFollowsWhatWasEvicted(t *testing.T) {
	h, _ := newTrimmingHarness(t, 4)
	meta := testHeader(t, "moved-base")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	fillTo(t, h, meta.ID, 10)

	suffix, err := h.ReadFrom(t.Context(), meta.ID, 0)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if suffix.BaseSeq != 6 {
		t.Errorf("起点是 %d，要的是 6", suffix.BaseSeq)
	}
}

// 那一批已经耐久写下去了，弹不动只是没腾出地方。把它回报成写失败，写入方会
// 回头重发一批已经在盘上的事件——那才是真的坏事。
func TestAFailedEvictionDoesNotFailTheWriteThatTriggeredIt(t *testing.T) {
	h, backend := newTrimmingHarness(t, 4)
	meta := testHeader(t, "stubborn")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	backend.mutex.Lock()
	backend.trimErr = errors.New("弹不动")
	backend.mutex.Unlock()

	fillTo(t, h, meta.ID, 10)

	// 一条都没弹掉，存档比上限长——这是接受的后果，不是错。
	if got := len(backend.storedEvents(meta.ID)); got != 10 {
		t.Fatalf("存档里剩 %d 条，弹不动的话该是 10 条", got)
	}
	// 起点不许在事件还在盘上的时候就推走。
	suffix, err := h.ReadFrom(t.Context(), meta.ID, 0)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if suffix.BaseSeq != 0 {
		t.Errorf("起点推到了 %d，可一条都没弹掉", suffix.BaseSeq)
	}
}

// 弹不动的后端上这条路整条不走：它没有 TrimBefore 可调，而编排器也不许因此
// 把起点推到一个介质并不认的位置。
func TestABackendThatCannotEvictIsLeftAlone(t *testing.T) {
	h := newHarnessWith(t, nil, CoordinatorOptions{MaxStoredEvents: 4})
	meta := testHeader(t, "append-only")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	fillTo(t, h, meta.ID, 10)

	if got := len(h.backend.storedEvents(meta.ID)); got != 10 {
		t.Fatalf("存档里剩 %d 条，弹不动的后端上该是 10 条", got)
	}
	suffix, err := h.ReadFrom(t.Context(), meta.ID, 0)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if suffix.BaseSeq != 0 {
		t.Errorf("起点是 %d，弹不动的后端上该是 0", suffix.BaseSeq)
	}
}

// 一个不实现 [TrimmingBackend] 的后端不许被断言成弹得动，反之亦然。
func TestTrimmingAsksTheBackendItself(t *testing.T) {
	plain := newMemoryBackend()
	if _, ok := Trimming(plain); ok {
		t.Error("内存后端没有 TrimBefore，不该被认成弹得动")
	}
	if _, ok := Trimming(&trimmingBackend{memoryBackend: plain}); !ok {
		t.Error("裹过的后端有 TrimBefore，该被认成弹得动")
	}
}

func TestTheLimitDefaultsAndRefusesANegativeValue(t *testing.T) {
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}

	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: newMemoryBackend(), Sessions: sessions},
		CoordinatorOptions{},
	)
	if err != nil {
		t.Fatalf("造编排器失败：%v", err)
	}
	if coordinator.maxStoredEvents != DefaultMaxStoredEvents {
		t.Errorf("上限是 %d，零值该落到 %d", coordinator.maxStoredEvents, DefaultMaxStoredEvents)
	}

	if _, err := NewCoordinator(
		CoordinatorDeps{Backend: newMemoryBackend(), Sessions: sessions},
		CoordinatorOptions{MaxStoredEvents: -1},
	); err == nil {
		t.Fatal("负数上限该被拒")
	}
}
