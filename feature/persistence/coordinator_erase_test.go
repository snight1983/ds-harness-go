// 本文件的作用：压删除那条路——删不动的后端上怎么当场说清楚、还有主的存档怎么
// 拦住、一个只登记过还没落地的身份删掉之后册子上还剩不剩，以及删完同一个 id
// 能不能重新建起来。
//
// # 这些测试防的是什么错
//
//   - 一个删不动的后端上「删掉了」被回报成功，使用方刷新列表之后那一条又冒出来。
//   - 存档被删掉了而册子上那条状态还留着，于是同一个 id 再也建不起来。
//   - 一份还绑在活会话上的存档被删掉，那个会话下一批事件照着旧游标落地出一份
//     只有尾巴的存档。
//   - 拿「弹到末尾」去顶删除，留下一份在 [Backend.LoadStored] 眼里合法的空存档。
//
// 新增: 整个文件都是本仓库自有的，上游的会话日志建了就永远在。理由见
// [ErasingBackend]。

package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// erasingBackend 把内存后端裹成一个删得动的后端。
type erasingBackend struct {
	*memoryBackend

	// eraseErr 非 nil 时每一次 Erase 都以它失败。
	eraseErr error
	// erases 记下 Erase 收到过的那几个身份。
	erases []sessionlog.SessionID
}

var _ ErasingBackend = (*erasingBackend)(nil)

func (b *erasingBackend) Erase(_ context.Context, id sessionlog.SessionID) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.eraseErr != nil {
		return b.eraseErr
	}
	if _, ok := b.logs[id]; !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, string(id))
	}
	b.erases = append(b.erases, id)
	delete(b.logs, id)
	return nil
}

// eraseCalls 交出 Erase 到此为止收到过的那几个身份。
func (b *erasingBackend) eraseCalls() []sessionlog.SessionID {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return append([]sessionlog.SessionID(nil), b.erases...)
}

// newErasingHarness 搭一套挂着删得动后端的编排器。
func newErasingHarness(t *testing.T) (*harness, *erasingBackend) {
	t.Helper()

	var erasing *erasingBackend
	h := newHarnessOn(t, func(memory *memoryBackend) Backend {
		erasing = &erasingBackend{memoryBackend: memory}
		return erasing
	})
	return h, erasing
}

func TestErasingTakesTheWholeArchiveAway(t *testing.T) {
	h, backend := newErasingHarness(t)
	meta := testHeader(t, "erase-me")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	if err := h.Append(t.Context(), meta.ID, []sessionlog.Event{
		userEvent(t, 0, "一"), userEvent(t, 1, "二"),
	}); err != nil {
		t.Fatalf("写事件失败：%v", err)
	}

	if err := h.Erase(t.Context(), meta.ID); err != nil {
		t.Fatalf("删存档失败：%v", err)
	}

	// 删掉之后这个身份就不存在了，不是「一份空存档」——那两件事在
	// [Backend.LoadStored] 眼里差得很远。
	if _, err := h.backend.LoadStored(t.Context(), meta.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("删完再读该说没有，实际 %v", err)
	}
	if calls := backend.eraseCalls(); len(calls) != 1 || calls[0] != meta.ID {
		t.Fatalf("后端收到的删除是 %v，要的是 [%q] 一次", calls, string(meta.ID))
	}
}

// 删掉之后这个身份必须是全新的：册子上那条状态没划走的话，同一个 id 会被
// [Coordinator.createCore] 的第一道拦当成「已经存在」。
func TestTheSameIdentityCanBeCreatedAgainAfterErasing(t *testing.T) {
	h, _ := newErasingHarness(t)
	meta := testHeader(t, "reborn")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	if err := h.Append(t.Context(), meta.ID, []sessionlog.Event{userEvent(t, 0, "旧的")}); err != nil {
		t.Fatalf("写事件失败：%v", err)
	}
	if err := h.Erase(t.Context(), meta.ID); err != nil {
		t.Fatalf("删存档失败：%v", err)
	}

	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("删完之后同一个 id 该重新建得起来，实际 %v", err)
	}
	// 新的那份从头写起，接不上旧存档的游标。
	if err := h.Append(t.Context(), meta.ID, []sessionlog.Event{userEvent(t, 0, "新的")}); err != nil {
		t.Fatalf("重建之后写不进去：%v", err)
	}
	if got := len(h.backend.storedEvents(meta.ID)); got != 1 {
		t.Fatalf("重建之后存档里有 %d 条，要的是 1 条", got)
	}
}

// 一个建了但从没追加过的身份在介质上什么都没有。册子上那条要划走，而这次删除
// 不该被回报成「没找到」——调用方要删的东西确实存在过。
func TestErasingAnIdentityThatNeverMaterialized(t *testing.T) {
	h, backend := newErasingHarness(t)
	meta := testHeader(t, "never-written")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}

	if err := h.Erase(t.Context(), meta.ID); err != nil {
		t.Fatalf("删一个还没落地的身份该成功，实际 %v", err)
	}
	if calls := backend.eraseCalls(); len(calls) != 0 {
		t.Fatalf("介质上本来就没有它，不该真删掉什么：%v", calls)
	}
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("册子上那条没划走：%v", err)
	}
}

// 介质上没有、册子上也没有——这才是真的「没找到」，原样交上去。
func TestErasingAnUnknownIdentityReportsNotFound(t *testing.T) {
	h, _ := newErasingHarness(t)

	err := h.Erase(t.Context(), sessionlog.SessionID("从来没有过"))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("该说没有，实际 %v", err)
	}
}

// 删不动的后端上不许假装删过：回报一句「删成功了」而其实什么都没删，会让使用方
// 的列表刷新之后那一条又冒出来。
func TestABackendThatCannotEraseRefusesUpFront(t *testing.T) {
	h := newHarness(t)
	meta := testHeader(t, "append-only")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	if err := h.Append(t.Context(), meta.ID, []sessionlog.Event{userEvent(t, 0, "留着")}); err != nil {
		t.Fatalf("写事件失败：%v", err)
	}

	if err := h.Erase(t.Context(), meta.ID); !errors.Is(err, ErrEraseUnsupported) {
		t.Fatalf("该报删不动，实际 %v", err)
	}
	// 拒绝之后一个字都不许动：那份存档还得在。
	if got := len(h.backend.storedEvents(meta.ID)); got != 1 {
		t.Fatalf("拒绝之后存档里有 %d 条，要的是 1 条", got)
	}
}

// 删一份还有主的存档是危险的：那个会话手上还攥着自己的游标，删完它下一批事件
// 会照着旧游标重新落地出一份只有尾巴的存档。次序不能反——先退场，再删。
func TestErasingRefusesWhileALiveSessionOwnsTheIdentity(t *testing.T) {
	h, backend := newErasingHarness(t)
	id := sessionlog.SessionID("still-live")
	live := h.createLive(t, id, coresession.CreateOptions{
		Seed: []sessionlog.Event{userEvent(t, 0, "活着")}, SeedLength: 1,
	})
	h.settle(t, live)

	err := h.Erase(t.Context(), id)
	if !errors.Is(err, ErrSessionLive) {
		t.Fatalf("该说这个会话还活着，实际 %v", err)
	}
	if calls := backend.eraseCalls(); len(calls) != 0 {
		t.Fatalf("拒绝之后不该真删掉什么：%v", calls)
	}

	// 退场之后同一次删除就该过。[Coordinator.Erase] 自己会等那趟退场收手——
	// 退场跑在它自己的 goroutine 上，赶在它前面删掉存档，最后那次刷盘会把事件
	// 写回一个刚被删掉的身份。
	h.retire(live)
	if err := h.Erase(t.Context(), id); err != nil {
		t.Fatalf("退场之后该删得掉，实际 %v", err)
	}
	if calls := backend.eraseCalls(); len(calls) != 1 {
		t.Fatalf("后端收到的删除是 %v，要的是一次", calls)
	}
}

// 后端把删除报成失败时，册子上那条不许划走：划走了就等于对外说删掉了，而介质上
// 那份还在。
func TestAFailedEraseLeavesTheRegistryAlone(t *testing.T) {
	h, backend := newErasingHarness(t)
	meta := testHeader(t, "stubborn-erase")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话失败：%v", err)
	}
	if err := h.Append(t.Context(), meta.ID, []sessionlog.Event{userEvent(t, 0, "删不掉")}); err != nil {
		t.Fatalf("写事件失败：%v", err)
	}

	backend.mutex.Lock()
	backend.eraseErr = errors.New("删不动")
	backend.mutex.Unlock()

	if err := h.Erase(t.Context(), meta.ID); err == nil {
		t.Fatal("后端删不动，这次删除该失败")
	}
	// 册子上还认得它，所以同一个 id 建不起来——这正是「没删掉」该有的样子。
	if err := h.Create(t.Context(), meta); err == nil {
		t.Fatal("删除失败了，册子上那条不该被划走")
	}
}

// 一个不实现 [ErasingBackend] 的后端不许被断言成删得动，反之亦然。
func TestErasableAsksTheBackendItself(t *testing.T) {
	plain := newMemoryBackend()
	if _, ok := Erasable(plain); ok {
		t.Error("内存后端没有 Erase，不该被认成删得动")
	}
	if _, ok := Erasable(&erasingBackend{memoryBackend: plain}); !ok {
		t.Error("裹过的后端有 Erase，该被认成删得动")
	}
}
