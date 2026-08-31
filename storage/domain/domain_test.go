// 本文件的作用：域运行期的用例——读、写、删、更新、关闭，以及类型化句柄的取出。
//
// 用例集中压的是 domain.go 顶部那三条不变量，尤其是第 2 条的**次序**：
// 先落盘、再改内存、再发事件。次序对不对，只有在「落盘失败」和「关闭正卡在半路」
// 这两种情况下才看得出来——所以这两种情况各有自己的用例，而不是只跑正常路径。

package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/storage"
	"ds-harness-go/storage/storagetest"
)

// note 是用例里那张表的记录类型。
type note struct {
	Title string `json:"title"`
	Count int    `json:"count"`
}

// preference 是用例里全局槽的类型。
type preference struct {
	Theme string `json:"theme"`
}

// errRejected 是校验函数拒绝一个值时给的那句话，用例靠它认出「是校验拦下的」。
var errRejected = errors.New("标题不能为空")

// notesSpec 是用例共用的那份声明：两张表加一个全局槽。
//
// 两张表而不是一张：只有一张时，「表之间互不串」这件事没有被观察的机会。
func notesSpec() Spec {
	return Spec{
		Name:    "notes",
		Version: 1,
		Global:  DefineGlobal(preference{Theme: "light"}, nil),
		Tables: []TableSpec{
			DefineTable("entries", func(n note) error {
				if n.Title == "" {
					return errRejected
				}
				return nil
			}),
			DefineTable("drafts", func(note) error { return nil }),
		},
	}
}

// quiet 是一个不往任何地方写的 logger：用例会故意让订阅者炸掉，
// 而那条警告日志本身不是被测对象，让它污染测试输出没有意义。
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// boot 建一份介质、一个中枢、一个设施，并保证测试结束时都收干净。
func boot(t *testing.T) (*storagetest.MemoryMedium, *Facility) {
	t.Helper()

	medium := storagetest.NewMemoryMedium()
	facility := attach(t, medium, storagetest.NewMemoryBackend(medium))
	return medium, facility
}

// attach 把一个指定的后端挂成默认后端 "main"，建出设施。
func attach(t *testing.T, _ *storagetest.MemoryMedium, backend storage.Backend) *Facility {
	t.Helper()

	hub := storage.New()
	if _, err := hub.Backend.Register("main", backend); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}
	facility, err := New(Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}
	t.Cleanup(func() {
		_ = facility.CloseAll(context.Background())
		_ = backend.Close(context.Background())
	})
	return facility
}

// open 打开一份声明，并把域登记进 cleanup。
func open(t *testing.T, facility *Facility, spec Spec) *Domain {
	t.Helper()

	domain, err := facility.Open(t.Context(), spec)
	if err != nil {
		t.Fatalf("打开域不该失败：%v", err)
	}
	t.Cleanup(func() { _ = domain.Close(context.Background()) })
	return domain
}

// entries 取出用例那张主表的类型化句柄。
func entries(t *testing.T, domain *Domain) *Table[note] {
	t.Helper()

	table, err := TableOf[note](domain, "entries")
	if err != nil {
		t.Fatalf("取表句柄不该失败：%v", err)
	}
	return table
}

// prefs 取出全局槽的类型化句柄。
func prefs(t *testing.T, domain *Domain) *Global[preference] {
	t.Helper()

	global, err := GlobalOf[preference](domain)
	if err != nil {
		t.Fatalf("取全局句柄不该失败：%v", err)
	}
	return global
}

// collect 订阅设施上的变更，返回一个「把至今收到的都取出来」的函数。
func collect(t *testing.T, facility *Facility) func() []Changed {
	t.Helper()

	var mutex sync.Mutex
	var seen []Changed
	t.Cleanup(facility.Subscribe(func(change Changed) {
		mutex.Lock()
		defer mutex.Unlock()
		seen = append(seen, change)
	}))
	return func() []Changed {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]Changed(nil), seen...)
	}
}

// expectCode 要求一个错误是本包的 *[Error] 且码正确。
func expectCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()

	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("该是 *domain.Error，实际 %v", err)
	}
	if typed.Code != code {
		t.Fatalf("码该是 %q，实际 %q（%s）", code, typed.Code, typed.Message)
	}
}

// TestAWriteIsDurableBeforeItIsVisible 钉住那条写链的次序：落盘 → 内存 → 事件。
//
// 源: packages/storage/storage-domain/src/domain.ts:307-313
//
// 三件事一起验是有理由的：只验内存的话，一个「先改内存后落盘」的实现照样通过；
// 只验介质的话，一个忘了发事件的实现照样通过。
func TestAWriteIsDurableBeforeItIsVisible(t *testing.T) {
	t.Parallel()

	medium, facility := boot(t)
	events := collect(t, facility)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	if err := table.Put(t.Context(), "a", note{Title: "第一条", Count: 1}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	value, exists, err := table.Get("a")
	if err != nil || !exists {
		t.Fatalf("写完就该读得到，实际 exists=%v err=%v", exists, err)
	}
	if value.Title != "第一条" || value.Count != 1 {
		t.Fatalf("读回来的值不对：%+v", value)
	}

	// 介质那一面：关掉域再重新打开（等价于进程重启），值还在。
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}
	reopened := open(t, facility, notesSpec())
	again, exists, err := entries(t, reopened).Get("a")
	if err != nil || !exists || again.Title != "第一条" {
		t.Fatalf("重新打开该读到已落盘的值，实际 %+v exists=%v err=%v", again, exists, err)
	}
	if len(medium.Table("notes", "entries")) != 1 {
		t.Fatalf("介质上该有一条记录，实际 %d 条", len(medium.Table("notes", "entries")))
	}

	// 事件那一面。
	seen := events()
	if len(seen) != 1 {
		t.Fatalf("该发一条事件，实际 %d 条", len(seen))
	}
	if seen[0].Domain != "notes" || seen[0].Table != "entries" || seen[0].Key != "a" {
		t.Fatalf("事件的位置不对：%+v", seen[0])
	}
	if seen[0].Operation != OperationPut {
		t.Fatalf("动作该是 put，实际 %q", seen[0].Operation)
	}
	var carried note
	if err := json.Unmarshal(seen[0].Value, &carried); err != nil {
		t.Fatalf("事件带的该是这条记录的 JSON 投影，实际解不开：%v", err)
	}
	if carried != (note{Title: "第一条", Count: 1}) {
		t.Fatalf("事件带的值不对：%+v", carried)
	}
}

// TestARejectedWriteChangesNothing 钉住第 3 条不变量：落盘失败就什么都不动。
//
// 源: packages/storage/storage-domain/src/domain.ts:307-313
//
// 这是先落盘后改内存那个次序**唯一**能被观察到的地方。次序换过来的话，
// 这次失败会在内存里留下一个介质上根本不存在的值，而重启之后它凭空消失。
func TestARejectedWriteChangesNothing(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	events := collect(t, facility)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	if err := table.Put(t.Context(), "a", note{Title: "好的"}); err != nil {
		t.Fatalf("第一次写入不该失败：%v", err)
	}

	boom := errors.New("介质满了")
	backend.set(func(b *flakyBackend) { b.putErr = boom })

	if err := table.Put(t.Context(), "a", note{Title: "坏的"}); !errors.Is(err, boom) {
		t.Fatalf("落盘失败该原样上抛，实际 %v", err)
	}

	backend.set(func(b *flakyBackend) { b.putErr = nil })

	value, exists, err := table.Get("a")
	if err != nil || !exists || value.Title != "好的" {
		t.Fatalf("内存该保持写失败之前的样子，实际 %+v exists=%v err=%v", value, exists, err)
	}
	if len(events()) != 1 {
		t.Fatalf("失败的写不该发事件，实际一共 %d 条", len(events()))
	}
}

// TestARejectedDeleteChangesNothing 是删除那一路的同一条断言。
//
// 源: packages/storage/storage-domain/src/domain.ts:315-330
func TestARejectedDeleteChangesNothing(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	events := collect(t, facility)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: "好的"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	boom := errors.New("介质只读")
	backend.set(func(b *flakyBackend) { b.deleteErr = boom })

	deleted, err := table.Delete(t.Context(), "a")
	if !errors.Is(err, boom) {
		t.Fatalf("删除失败该原样上抛，实际 %v", err)
	}
	if deleted {
		t.Fatal("失败的删除不该报告删掉了")
	}
	if _, exists, _ := table.Get("a"); !exists {
		t.Fatal("删除失败之后记录该还在内存里")
	}
	if len(events()) != 1 {
		t.Fatalf("失败的删除不该发事件，实际一共 %d 条", len(events()))
	}
}

// TestDeletingAnAbsentKeyIsNotAChange 钉住「空操作不算变更」。
//
// 源: packages/storage/storage-domain/src/domain.ts:315-330
//
// 把一次空操作说成一次变更，会让订阅者以为有东西没了，并据此丢掉自己那份缓存。
func TestDeletingAnAbsentKeyIsNotAChange(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	events := collect(t, facility)
	table := entries(t, open(t, facility, notesSpec()))

	deleted, err := table.Delete(t.Context(), "没有这条")
	if err != nil {
		t.Fatalf("删一个不存在的键不该失败：%v", err)
	}
	if deleted {
		t.Fatal("该报告没删到东西")
	}
	if len(events()) != 0 {
		t.Fatalf("不该发事件，实际 %d 条", len(events()))
	}
}

// TestDeleteRemovesTheRecordAndAnnouncesIt 是删除的正常路径。
func TestDeleteRemovesTheRecordAndAnnouncesIt(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	events := collect(t, facility)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: "要删的"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}
	deleted, err := table.Delete(t.Context(), "a")
	if err != nil || !deleted {
		t.Fatalf("该删掉，实际 deleted=%v err=%v", deleted, err)
	}
	if _, exists, _ := table.Get("a"); exists {
		t.Fatal("删完不该还读得到")
	}

	seen := events()
	if len(seen) != 2 {
		t.Fatalf("该有写入和删除两条事件，实际 %d 条", len(seen))
	}
	if seen[1].Operation != OperationDeleted {
		t.Fatalf("第二条该是 deleted，实际 %q", seen[1].Operation)
	}
	if seen[1].Value != nil {
		t.Fatalf("墓碑不该带值，实际 %s", seen[1].Value)
	}
}

// TestUpdateIsAtomicAcrossConcurrentCallers 钉住 Update 比「自己 Get 再 Put」强在哪。
//
// 源: packages/storage/storage-domain/src/domain.ts:332-346
//
// 五十个并发的自增，只有当每个 fn 都看到**轮到它那一刻**的值时，结果才是五十。
// 换成 Get 再 Put 的话，中间那个窗口会让大部分自增互相覆盖。
func TestUpdateIsAtomicAcrossConcurrentCallers(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: "计数"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	const callers = 50
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = table.Update(context.Background(), "a", func(current note) (note, error) {
				current.Count++
				return current, nil
			})
		}()
	}
	wg.Wait()

	value, _, err := table.Get("a")
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if value.Count != callers {
		t.Fatalf("每次自增都该看到上一次的结果，该是 %d，实际 %d", callers, value.Count)
	}
}

// TestUpdateOnAnAbsentKeyIsRefused 钉住 Update 不会顺手把记录建出来。
//
// 源: packages/storage/storage-domain/src/domain.ts:332-346
func TestUpdateOnAnAbsentKeyIsRefused(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	table := entries(t, open(t, facility, notesSpec()))

	_, err := table.Update(t.Context(), "没有这条", func(n note) (note, error) { return n, nil })
	expectCode(t, err, CodeMissingKey)
}

// TestUpdateWritesNothingWhenTheFunctionFails 钉住 fn 失败时那次写整个不发生。
func TestUpdateWritesNothingWhenTheFunctionFails(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	events := collect(t, facility)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: "原样"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	boom := errors.New("调用方自己不干了")
	_, err := table.Update(t.Context(), "a", func(note) (note, error) { return note{}, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("fn 的错误该原样上抛，实际 %v", err)
	}
	if value, _, _ := table.Get("a"); value.Title != "原样" {
		t.Fatalf("值该没被动过，实际 %+v", value)
	}
	if len(events()) != 1 {
		t.Fatalf("不该多发事件，实际一共 %d 条", len(events()))
	}
}

// TestUpdateChangesNothingWhenTheMediumRefuses 钉住更新走的是和写入同一条落盘路。
//
// 源: packages/storage/storage-domain/src/domain.ts:307-313
//
// 单独钉一条，是因为 [Table.Update] 和 [Table.Put] 走的是同一个 store 但入口不同：
// 一个把「落盘失败就什么都不动」实现在 Put 里、而 Update 自己另写一遍的版本，
// 只验 Put 的话照样通过。
func TestUpdateChangesNothingWhenTheMediumRefuses(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	events := collect(t, facility)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: "原样"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	refused := errors.New("介质拒了这次写")
	backend.set(func(b *flakyBackend) { b.putErr = refused })

	_, err := table.Update(t.Context(), "a", func(current note) (note, error) {
		current.Title = "改过的"
		return current, nil
	})
	if !errors.Is(err, refused) {
		t.Fatalf("该把后端的失败带出来，实际 %v", err)
	}
	if value, _, _ := table.Get("a"); value.Title != "原样" {
		t.Fatalf("落盘失败时内存该没被动过，实际 %+v", value)
	}
	if len(events()) != 1 {
		t.Fatalf("失败的更新不该发事件，实际一共 %d 条", len(events()))
	}
}

// TestTheErrorTextCarriesBothTheReasonAndTheCode 钉住错误那句话的形状。
//
// 源: packages/storage/storage-domain/src/error.ts:28-53
//
// 码要出现在文本里：日志里那一行是排查时最先看到的东西，而调用方分派靠的是
// [Error.Code] 字段，两边都得有。
func TestTheErrorTextCarriesBothTheReasonAndTheCode(t *testing.T) {
	t.Parallel()

	err := newError(CodeClosed, "域 %q 已经关了", "notes")
	text := err.Error()
	if !strings.Contains(text, "notes") || !strings.Contains(text, string(CodeClosed)) {
		t.Fatalf("那句话该同时带上原因和码，实际 %q", text)
	}

	// Unwrap 让 errors.Is 问得到底层原因。
	cause := errors.New("底下那句")
	wrapped := &Error{Code: CodeInvalidRecord, Message: "包了一层", Err: cause}
	if !errors.Is(wrapped, cause) {
		t.Fatal("该能问到底层原因")
	}
	if errors.Unwrap(&Error{Code: CodeClosed}) != nil {
		t.Fatal("没有底层原因时该给 nil")
	}
}

// TestValidationRunsOnWriteNotJustOnLoad 钉住那条和 DSH 的差异。
//
// DSH 只在「从介质读回来」这个边界上跑校验，写是不查的——于是一条过不了校验的记录
// 能安静地写下去，直到下一次进程重启、整个域因为它打不开，而那时候现场早没了。
func TestValidationRunsOnWriteNotJustOnLoad(t *testing.T) {
	t.Parallel()

	medium, facility := boot(t)
	table := entries(t, open(t, facility, notesSpec()))

	if err := table.Put(t.Context(), "a", note{Title: ""}); !errors.Is(err, errRejected) {
		t.Fatalf("写入时就该被校验拦下，实际 %v", err)
	}
	if len(medium.Table("notes", "entries")) != 0 {
		t.Fatal("被校验拦下的写不该碰介质")
	}

	if err := table.Put(t.Context(), "a", note{Title: "好的"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}
	_, err := table.Update(t.Context(), "a", func(note) (note, error) { return note{Title: ""}, nil })
	if !errors.Is(err, errRejected) {
		t.Fatalf("更新时也该被校验拦下，实际 %v", err)
	}
}

// TestKeysAndEntriesAreSortedSnapshots 钉住两件事：排过序，而且是快照。
//
// 源: packages/storage/storage-domain/src/domain.ts:50-61
//
// 排序那一半是 Go 这边独有的：map 遍历顺序是故意随机的，原样交出去的话
// 同一份数据两次调用给出的顺序都不一样，翻页、诊断输出和测试断言都没法用。
func TestKeysAndEntriesAreSortedSnapshots(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	table := entries(t, open(t, facility, notesSpec()))

	for _, key := range []string{"c", "a", "b"} {
		if err := table.Put(t.Context(), key, note{Title: key}); err != nil {
			t.Fatalf("写入不该失败：%v", err)
		}
	}

	keys, err := table.Keys()
	if err != nil {
		t.Fatalf("读键不该失败：%v", err)
	}
	if fmt.Sprint(keys) != "[a b c]" {
		t.Fatalf("键该按字典序，实际 %v", keys)
	}

	rows, err := table.Entries()
	if err != nil {
		t.Fatalf("读条目不该失败：%v", err)
	}
	if len(rows) != 3 || rows[0].Key != "a" || rows[0].Value.Title != "a" {
		t.Fatalf("条目不对：%+v", rows)
	}
	if size, _ := table.Size(); size != 3 {
		t.Fatalf("条数该是 3，实际 %d", size)
	}

	// 快照那一半：拿到之后再写一条，手上这份不变形。
	if err := table.Put(t.Context(), "d", note{Title: "d"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}
	if len(keys) != 3 || len(rows) != 3 {
		t.Fatal("已经拿到手的那份快照不该跟着变")
	}
}

// TestTablesDoNotBleedIntoEachOther 钉住同名键在两张表里互不相干。
func TestTablesDoNotBleedIntoEachOther(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())

	drafts, err := TableOf[note](domain, "drafts")
	if err != nil {
		t.Fatalf("取表句柄不该失败：%v", err)
	}
	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "正式"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}
	if _, exists, _ := drafts.Get("a"); exists {
		t.Fatal("另一张表不该看得到这条")
	}
	if size, _ := drafts.Size(); size != 0 {
		t.Fatalf("另一张表该是空的，实际 %d 条", size)
	}
}

// TestTableOfRefusesUnknownNamesAndWrongTypes 钉住两类调用方 bug 都在取句柄时就报。
//
// 源: packages/storage/storage-domain/src/domain.ts:211-223
//
// 不核对的话，一个写错的 V 会在读到第一条记录时才现形，而那时候离声明处已经很远了。
func TestTableOfRefusesUnknownNamesAndWrongTypes(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())

	if _, err := TableOf[note](domain, "没这张表"); err == nil {
		t.Fatal("没声明过的表名该失败")
	}
	if _, err := TableOf[preference](domain, "entries"); err == nil {
		t.Fatal("记录类型对不上该失败")
	}
	if _, err := GlobalOf[note](domain); err == nil {
		t.Fatal("全局值类型对不上该失败")
	}
}

// TestADomainWithoutAGlobalRefusesGlobalAccess 钉住没声明全局槽时的三条路都拒。
func TestADomainWithoutAGlobalRefusesGlobalAccess(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, Spec{
		Name:   "plain",
		Tables: []TableSpec{DefineTable[note]("entries", nil)},
	})

	if _, err := GlobalOf[preference](domain); err == nil {
		t.Fatal("没声明全局槽该失败")
	}
	if _, err := domain.RawGlobal(); err == nil {
		t.Fatal("没声明全局槽时读原始全局值该失败")
	}
}

// TestTheGlobalStartsAtItsDeclaredInitialAndPersistsAfterSet 钉住全局槽的两段人生。
//
// 源: packages/storage/storage-domain/src/domain.ts:20-34
//
// 第一次 Set 才是把全局槽真正落到介质上的那一刻——在那之前介质上是空的，
// 读到的是声明里的初值。这也是 [DefineGlobal] 必须挡住能编码成 null 的值的原因。
func TestTheGlobalStartsAtItsDeclaredInitialAndPersistsAfterSet(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	events := collect(t, facility)
	domain := open(t, facility, notesSpec())
	global := prefs(t, domain)

	current, err := global.Get()
	if err != nil || current.Theme != "light" {
		t.Fatalf("第一次 Set 之前该读到初值，实际 %+v err=%v", current, err)
	}

	if err := global.Set(t.Context(), preference{Theme: "dark"}); err != nil {
		t.Fatalf("写全局值不该失败：%v", err)
	}
	if current, _ := global.Get(); current.Theme != "dark" {
		t.Fatalf("该读到新值，实际 %+v", current)
	}

	seen := events()
	if len(seen) != 1 {
		t.Fatalf("该发一条事件，实际 %d 条", len(seen))
	}
	// 全局槽的写用两个空串表示位置，和 [RecordSlot] 是同一套约定。
	if seen[0].Table != "" || seen[0].Key != "" {
		t.Fatalf("全局槽的事件该用空表名空键，实际 %+v", seen[0])
	}

	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}
	reopened := open(t, facility, notesSpec())
	if current, _ := prefs(t, reopened).Get(); current.Theme != "dark" {
		t.Fatalf("重新打开该读到已落盘的全局值，实际 %+v", current)
	}
}

// TestAGlobalThatEncodesToNullIsRefused 钉住那个哨兵冲突在写的时候也拦得住。
//
// 介质拿 null 当「从来没写过」，所以一个真能编码成 null 的全局值存不住：
// 重新打开时它会被当成没写过，安静地退回初值，中间没有任何一步报错。
func TestAGlobalThatEncodesToNullIsRefused(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, Spec{
		Name:   "nullable",
		Global: DefineGlobal[*preference](&preference{Theme: "light"}, nil),
	})
	global, err := GlobalOf[*preference](domain)
	if err != nil {
		t.Fatalf("取全局句柄不该失败：%v", err)
	}

	if err := global.Set(t.Context(), nil); err == nil {
		t.Fatal("能编码成 null 的全局值该被拒")
	}
	if current, _ := global.Get(); current == nil || current.Theme != "light" {
		t.Fatalf("被拒之后该保持原值，实际 %+v", current)
	}
}

// TestARejectedGlobalWriteChangesNothing 是全局槽那一路的「落盘失败就什么都不动」。
func TestARejectedGlobalWriteChangesNothing(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	events := collect(t, facility)
	global := prefs(t, open(t, facility, notesSpec()))

	boom := errors.New("介质满了")
	backend.set(func(b *flakyBackend) { b.globalErr = boom })

	if err := global.Set(t.Context(), preference{Theme: "dark"}); !errors.Is(err, boom) {
		t.Fatalf("落盘失败该原样上抛，实际 %v", err)
	}
	if current, _ := global.Get(); current.Theme != "light" {
		t.Fatalf("内存该保持原样，实际 %+v", current)
	}
	if len(events()) != 0 {
		t.Fatalf("失败的写不该发事件，实际 %d 条", len(events()))
	}
}

// TestCloseDrainsQueuedWritesAndRejectsLaterOnes 钉住关闭的次序。
//
// 源: packages/storage/storage-domain/src/domain.ts:226-244
//
// 「已经排上队的写跑完，之后来的当场拒」这件事只有在一次写**正卡在后端里**的时候
// 才看得出来——所以这里用钩子把一次写按在 PutRecord 里，在那个当口去调 Close。
func TestCloseDrainsQueuedWritesAndRejectsLaterOnes(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	events := collect(t, facility)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	inside := make(chan struct{})
	release := make(chan struct{})
	backend.set(func(b *flakyBackend) {
		b.hook = func(op string) {
			if op != "put" {
				return
			}
			// 只按住第一次写：钩子摘掉之后后面的调用直接过。
			b.set(func(inner *flakyBackend) { inner.hook = nil })
			close(inside)
			<-release
		}
	})

	written := make(chan error, 1)
	go func() { written <- table.Put(context.Background(), "a", note{Title: "排在前面"}) }()
	<-inside

	closed := make(chan error, 1)
	go func() { closed <- domain.Close(context.Background()) }()

	// 等「关闭已经开始」这件事真的成立再发下一次写。
	//
	// 这里直接读 domain.closing，而不是反复试 Put 到它被拒为止——那样写会死锁：
	// closing 立起来**之前**到的写不会被拒，它会去排写链，而写链正被上面那次
	// 按在钩子里的写占着，于是这个 goroutine 卡住，钩子也就永远没人放。
	// 「关闭开始」这个时刻在包外没有任何可观察的信号（读还没拒、写还没拒），
	// 本用例在包内，于是直接看那个标志位——这也是这个用例放在包内的原因。
	for {
		domain.gate.Lock()
		started := domain.closing
		domain.gate.Unlock()
		if started {
			break
		}
		runtime.Gosched()
	}

	// 关闭已经开始了，此时来的写当场被拒——不必等前面那次写做完。
	err := table.Put(context.Background(), "b", note{Title: "来晚了"})
	if err == nil {
		t.Fatal("关闭开始之后的写该被拒")
	}
	expectCode(t, err, CodeClosed)

	close(release)
	if err := <-written; err != nil {
		t.Fatalf("已经排上队的写该跑完，实际 %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}

	// 排干的那次写，事件照发。
	seen := events()
	if len(seen) != 1 || seen[0].Key != "a" {
		t.Fatalf("排干的那次写该发过事件，实际 %+v", seen)
	}
	if len(medium.Table("notes", "entries")) != 1 {
		t.Fatal("排干的那次写该已经落盘")
	}
}

// TestCloseIsIdempotentAndFreesTheName 钉住重复关闭共用同一次拆解，且名字让得出来。
//
// 源: packages/storage/storage-domain/src/domain.ts:110-118
func TestCloseIsIdempotentAndFreesTheName(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())

	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("第一次关闭不该失败：%v", err)
	}
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("重复关闭该返回同一个结果，实际 %v", err)
	}
	if _, open := facility.Get("notes"); open {
		t.Fatal("关掉之后该从设施上摘掉")
	}
	// 名字让出来了，同名的域重新开得起来。
	if _, err := facility.Open(t.Context(), notesSpec()); err != nil {
		t.Fatalf("关掉之后该能重新打开，实际 %v", err)
	}
}

// TestAFailingUnitCloseStillFreesTheDomain 钉住那条和 DSH 的差异。
//
// DSH 那边 unit.close() 失败会直接抛出去，closed 和 onClosed 都跑不到——于是这个域
// 既写不进去（disposing 已经立起来了）也重新打开不了（名字还占着），整个进程只能重启。
// 这里返回错误，但域照样标成关闭、名字照样让出来。
func TestAFailingUnitCloseStillFreesTheDomain(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)
	domain := open(t, facility, notesSpec())

	boom := errors.New("关不掉")
	backend.set(func(b *flakyBackend) { b.closeErr = boom })

	if err := domain.Close(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("该把失败原因上抛，实际 %v", err)
	}
	if _, stillOpen := facility.Get("notes"); stillOpen {
		t.Fatal("名字该让出来")
	}
	if _, _, err := entries(t, domain).Get("a"); err == nil {
		t.Fatal("域该已经标成关闭")
	}
}

// TestEveryReadRefusesAfterClose 钉住关掉之后读也拒，而不是安静地读到一张空表。
//
// 源: packages/storage/storage-domain/src/domain.ts:287-305
//
// 这是 Go 这边比 DSH 多出来的一个返回值换来的东西：「关掉之后一律返回不存在」
// 会让一个拿着过期句柄的调用方安静地读到空，然后照着这个结论往下走，
// 而它永远不会知道自己读的是一个死域。
func TestEveryReadRefusesAfterClose(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)
	global := prefs(t, domain)

	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}

	if _, _, err := table.Get("a"); err == nil {
		t.Error("Get 该拒")
	} else {
		expectCode(t, err, CodeClosed)
	}
	if _, err := table.Keys(); err == nil {
		t.Error("Keys 该拒")
	}
	if _, err := table.Entries(); err == nil {
		t.Error("Entries 该拒")
	}
	if _, err := table.Size(); err == nil {
		t.Error("Size 该拒")
	}
	if _, err := global.Get(); err == nil {
		t.Error("全局值的 Get 该拒")
	}
	if _, _, err := domain.RawRecord("entries", "a"); err == nil {
		t.Error("RawRecord 该拒")
	}
	if _, err := domain.RawGlobal(); err == nil {
		t.Error("RawGlobal 该拒")
	}
	if err := global.Set(t.Context(), preference{Theme: "dark"}); err == nil {
		t.Error("全局值的 Set 该拒")
	}
	if _, err := table.Update(t.Context(), "a", func(n note) (note, error) { return n, nil }); err == nil {
		t.Error("Update 该拒")
	}
	if _, err := table.Delete(t.Context(), "a"); err == nil {
		t.Error("Delete 该拒")
	}
}

// TestRawReadsAreTheUntypedDiagnosticSurface 钉住那两个不带类型的读。
//
// 源: packages/storage/storage-domain/src/index.ts:158-167
//
// 它们是给不变量检查和诊断用的：那些代码按定义不知道记录是什么 Go 类型。
func TestRawReadsAreTheUntypedDiagnosticSurface(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())

	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "原始"}); err != nil {
		t.Fatalf("写入不该失败：%v", err)
	}

	raw, exists, err := domain.RawRecord("entries", "a")
	if err != nil || !exists {
		t.Fatalf("该读得到，实际 exists=%v err=%v", exists, err)
	}
	var decoded note
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Title != "原始" {
		t.Fatalf("原始投影该解得开且是那条记录，实际 %s err=%v", raw, err)
	}

	if _, exists, err := domain.RawRecord("entries", "没有"); err != nil || exists {
		t.Fatalf("不存在的键该报不存在，实际 exists=%v err=%v", exists, err)
	}
	if _, _, err := domain.RawRecord("没这张表", "a"); err == nil {
		t.Fatal("没声明过的表该失败")
	}

	global, err := domain.RawGlobal()
	if err != nil {
		t.Fatalf("读原始全局值不该失败：%v", err)
	}
	if string(global) != `{"theme":"light"}` {
		t.Fatalf("原始全局值该是初值的投影，实际 %s", global)
	}
}

// TestNameAndTableNamesDescribeTheDomain 钉住那两个诊断读。
func TestNameAndTableNamesDescribeTheDomain(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())

	if domain.Name() != "notes" {
		t.Fatalf("域名该是 notes，实际 %q", domain.Name())
	}
	// 排过序：声明里的顺序是 entries、drafts。
	if got := fmt.Sprint(domain.TableNames()); got != "[drafts entries]" {
		t.Fatalf("表名该按字典序，实际 %s", got)
	}
}

// TestRecordsSurviveAcrossManyKeys 是一次多键往返，顺带压住 Entries 的解码那一路。
func TestRecordsSurviveAcrossManyKeys(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	for i := range 20 {
		key := strconv.Itoa(i)
		if err := table.Put(t.Context(), key, note{Title: key, Count: i}); err != nil {
			t.Fatalf("写入不该失败：%v", err)
		}
	}
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}

	rows, err := entries(t, open(t, facility, notesSpec())).Entries()
	if err != nil {
		t.Fatalf("读条目不该失败：%v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("该读回 20 条，实际 %d 条", len(rows))
	}
	for _, row := range rows {
		if row.Value.Title != row.Key {
			t.Fatalf("键和值对不上：%+v", row)
		}
	}
}
