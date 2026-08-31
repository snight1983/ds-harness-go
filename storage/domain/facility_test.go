// 本文件的作用：设施那一头的用例——建设施、打开、按名字找回、关掉、订阅变更。
//
// 这里压的是 facility.go 那两条结构性的保证：
// 一是**打开要么整份成功要么什么都不留**（名字让得出来、单元不泄漏），
// 二是**一个订阅者炸掉不影响其余的**，而不变量违例是那里面唯一的例外。

package domain

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/invariants"
	"ds-harness-go/storage"
	"ds-harness-go/storage/storagetest"
)

// TestNewRequiresAHubAndABackendName 钉住建设施时那两个没有缺省值的必填项。
//
// 源: packages/storage/storage-domain/src/index.ts:194-220
//
// 默认后端尤其不能猜：它决定断电之后还剩下什么（见 [Config.Backend]）。
func TestNewRequiresAHubAndABackendName(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Backend: "main"}); err == nil {
		t.Fatal("没有存储中枢该建不出设施")
	}
	if _, err := New(Config{Storage: storage.New()}); err == nil {
		t.Fatal("没有默认后端名该建不出设施")
	}
	if _, err := New(Config{Storage: storage.New(), Backend: "main"}); err != nil {
		t.Fatalf("两样齐了不该失败：%v", err)
	}
}

// TestTheRouteTableIsCopiedAtConstruction 钉住路由表在建设施时被拷了一份。
//
// 源: packages/storage/storage-domain/src/index.ts:53-61
//
// 装配方递进来的那个 map 之后被改掉的话，「这个域存在哪儿」会在运行途中变，
// 而已经打开的域还连着旧后端——这是一类只在改配置那一刻现形的故障。
func TestTheRouteTableIsCopiedAtConstruction(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	hub := storage.New()
	if _, err := hub.Backend.Register("main", storagetest.NewMemoryBackend(medium)); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}

	routes := map[string]string{"notes": "main"}
	facility, err := New(Config{Storage: hub, Backend: "main", Routes: routes, Logger: quiet()})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}
	// 建完之后把路由改到一个根本不存在的后端上。
	routes["notes"] = "不存在的后端"

	domain := open(t, facility, notesSpec())
	if domain.Name() != "notes" {
		t.Fatalf("域名该是 notes，实际 %q", domain.Name())
	}
}

// TestRoutesOverrideTheDefaultBackend 钉住逐域覆盖真的把域开到了另一份介质上。
//
// 源: packages/storage/storage-domain/src/index.ts:91-98
func TestRoutesOverrideTheDefaultBackend(t *testing.T) {
	t.Parallel()

	mainMedium := storagetest.NewMemoryMedium()
	sideMedium := storagetest.NewMemoryMedium()
	hub := storage.New()
	for name, medium := range map[string]*storagetest.MemoryMedium{
		"main": mainMedium, "side": sideMedium,
	} {
		if _, err := hub.Backend.Register(name, storagetest.NewMemoryBackend(medium)); err != nil {
			t.Fatalf("注册后端 %q 不该失败：%v", name, err)
		}
	}

	facility, err := New(Config{
		Storage: hub, Backend: "main",
		Routes: map[string]string{"notes": "side"},
		Logger: quiet(),
	})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}

	domain := open(t, facility, notesSpec())
	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "落在 side 上"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	if len(sideMedium.Table("notes", "entries")) != 1 {
		t.Fatal("被覆盖的域该落在 side 这份介质上")
	}
	if mainMedium.Table("notes", "entries") != nil {
		t.Fatal("默认后端那份介质上不该有这个域")
	}
}

// TestOpeningTheSameNameTwiceIsRefused 钉住一个域名同时只能开一次。
//
// 源: packages/storage/storage-domain/src/index.ts:85-90
//
// 开两次意味着两份内存态压在同一份介质上，谁后写谁赢，而两边都不知道对方存在。
func TestOpeningTheSameNameTwiceIsRefused(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	open(t, facility, notesSpec())

	_, err := facility.Open(t.Context(), notesSpec())
	expectCode(t, err, CodeAlreadyOpen)
}

// TestOpeningRefusesABackendWithoutTheKeyValueFacet 钉住形态不支持那条路。
//
// 源: packages/storage/storage-domain/src/index.ts:99-106
func TestOpeningRefusesABackendWithoutTheKeyValueFacet(t *testing.T) {
	t.Parallel()

	facility := attach(t, nil, bareBackend{})

	_, err := facility.Open(t.Context(), notesSpec())
	expectCode(t, err, CodeFacetUnsupported)

	// 名字要让得出来：改用一个提供键值形态的后端之后还能开。
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("开失败之后不该占着名字，实际 %v", names)
	}
}

// TestOpeningFailsWhenTheRoutedBackendIsNotRegistered 钉住后端失败**原样穿过去**。
//
// 源: packages/storage/storage-domain/src/error.ts:6-12
//
// 这一条要的是「不重新包一遍」：包成域自己的码，会把「介质出了什么事」压成一句转述。
func TestOpeningFailsWhenTheRoutedBackendIsNotRegistered(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	facility, err := New(Config{Storage: hub, Backend: "没登记过", Logger: quiet()})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}

	_, err = facility.Open(t.Context(), notesSpec())
	var backendErr *storage.Error
	if !errors.As(err, &backendErr) {
		t.Fatalf("该原样给出 *storage.Error，实际 %v", err)
	}
	var domainErr *Error
	if errors.As(err, &domainErr) {
		t.Fatalf("后端的失败不该被包成域的失败：%v", err)
	}
}

// TestABadSpecNeverTouchesTheMedium 钉住声明先验、介质后碰的次序。
//
// 源: packages/storage/storage-domain/src/index.ts:169-172
func TestABadSpecNeverTouchesTheMedium(t *testing.T) {
	t.Parallel()

	medium, facility := boot(t)

	spec := notesSpec()
	spec.Name = "Notes" // 大写开头，不合法
	if _, err := facility.Open(t.Context(), spec); err == nil {
		t.Fatal("不合法的域名该被拒")
	}

	// 介质上不该留下任何痕迹——连一张空表都不该有。
	if medium.Table("Notes", "entries") != nil || medium.Table("notes", "entries") != nil {
		t.Fatal("被拒的声明不该在介质上留下单元")
	}
}

// TestOneBadRecordKeepsTheWholeDomainClosed 钉住「要么完整可信，要么根本不给出来」。
//
// 源: packages/storage/storage-domain/src/index.ts:107-140
//
// 跳过坏记录意味着交出一个「大部分对」的域，而调用方没有任何办法知道自己少看了什么；
// 随后一次针对那个键的写还会把坏数据覆盖掉，于是连现场都没了。
func TestOneBadRecordKeepsTheWholeDomainClosed(t *testing.T) {
	t.Parallel()

	medium, facility := boot(t)

	// 先正常写一条，再关掉，然后从介质那一侧塞一条过不了校验的进去。
	domain := open(t, facility, notesSpec())
	if err := entries(t, domain).Put(t.Context(), "good", note{Title: "好的"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	corruptRecord(t, medium, "notes", "entries", "bad", `{"title":""}`)

	_, err := facility.Open(t.Context(), notesSpec())
	expectCode(t, err, CodeInvalidRecord)

	var typed *Error
	if !errors.As(err, &typed) || typed.Slot == nil {
		t.Fatalf("该带上出问题那条记录的位置：%v", err)
	}
	if typed.Slot.Table != "entries" || typed.Slot.Key != "bad" {
		t.Fatalf("位置该指到 entries/bad，实际 %+v", *typed.Slot)
	}
	if !errors.Is(err, errRejected) {
		t.Fatalf("底层原因该是校验函数给的那句：%v", err)
	}
}

// TestABadGlobalValueOnTheMediumKeepsTheDomainClosed 钉住全局槽那一路的加载校验。
//
// 源: packages/storage/storage-domain/src/index.ts:141-155
//
// 和记录那一路是同一条规矩，但位置标注不同：全局槽用两个空串表示（见 [RecordSlot]）。
func TestABadGlobalValueOnTheMediumKeepsTheDomainClosed(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	facility := attach(t, medium, storagetest.NewMemoryBackend(medium))

	spec := notesSpec()
	spec.Global = DefineGlobal(preference{Theme: "light"}, func(p preference) error {
		if p.Theme == "" {
			return errRejected
		}
		return nil
	})

	domain := open(t, facility, spec)
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	corruptGlobal(t, medium, "notes", `{"theme":""}`)

	_, err := facility.Open(t.Context(), spec)
	expectCode(t, err, CodeInvalidRecord)

	var typed *Error
	if !errors.As(err, &typed) || typed.Slot == nil {
		t.Fatalf("该带上位置：%v", err)
	}
	if typed.Slot.Table != "" || typed.Slot.Key != "" {
		t.Fatalf("全局槽的位置该是两个空串，实际 %+v", *typed.Slot)
	}
	if !strings.Contains(typed.Message, "全局值") {
		t.Fatalf("那句话里该点明是全局值：%q", typed.Message)
	}
	if !errors.Is(err, errRejected) {
		t.Fatalf("底层原因该是校验函数给的那句：%v", err)
	}
}

// TestOpeningFailsWhenTheMediumIsStampedWithAnotherVersion 钉住单元打开失败原样穿过去。
//
// 源: packages/storage/storage-domain/src/index.ts:99-106
func TestOpeningFailsWhenTheMediumIsStampedWithAnotherVersion(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	facility := attach(t, medium, storagetest.NewMemoryBackend(medium))

	domain := open(t, facility, notesSpec())
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}

	next := notesSpec()
	next.Version = 2
	_, err := facility.Open(t.Context(), next)

	var backendErr *storage.Error
	if !errors.As(err, &backendErr) || backendErr.Code != storage.CodeVersionMismatch {
		t.Fatalf("该原样给出版本不符：%v", err)
	}
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("开砸之后不该占着名字，实际 %v", names)
	}
}

// TestOpeningFailsWhenTheSnapshotCannotBeRead 钉住整份读回来那一步砸掉的路。
//
// 源: packages/storage/storage-domain/src/index.ts:107-112
//
// 读不回来就没有内存态，也就没有「域」可言——不存在「读到一半也能用」的中间态。
func TestOpeningFailsWhenTheSnapshotCannotBeRead(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)

	loadFailure := errors.New("介质读不动了")
	backend.set(func(b *flakyBackend) { b.loadErr = loadFailure })

	_, err := facility.Open(t.Context(), notesSpec())
	if !errors.Is(err, loadFailure) {
		t.Fatalf("该把后端的失败带出来：%v", err)
	}
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("开砸之后不该占着名字，实际 %v", names)
	}
}

// TestAFailedOpenReleasesTheUnitAndTheName 钉住开砸之后既不泄漏单元也不占着名字。
//
// 源: packages/storage/storage-domain/src/index.ts:141-145
//
// 单元不释放的话，改完数据重试会在后端那一层撞上「没关就开第二次」；
// 名字不让出来的话，这个域这辈子都开不起来了。
func TestAFailedOpenReleasesTheUnitAndTheName(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	facility := attach(t, medium, storagetest.NewMemoryBackend(medium))

	domain := open(t, facility, notesSpec())
	if err := entries(t, domain).Put(t.Context(), "good", note{Title: "好的"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	corruptRecord(t, medium, "notes", "entries", "bad", `{"title":""}`)

	if _, err := facility.Open(t.Context(), notesSpec()); err == nil {
		t.Fatal("坏记录该让打开失败")
	}
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("开砸之后不该占着名字，实际 %v", names)
	}

	// 把坏记录清掉，同一个名字能重新开起来——这同时证明后端那个单元已经释放了。
	repairRecord(t, medium, "notes", "entries", "bad")
	again := open(t, facility, notesSpec())
	if got, ok, err := again.RawRecord("entries", "good"); err != nil || !ok || len(got) == 0 {
		t.Fatalf("重开之后好的那条该还在：%v %v", ok, err)
	}
}

// TestAFailedOpenSurfacesTheRecordErrorNotTheCleanupError 钉住清理失败不覆盖原因。
//
// 源: packages/storage/storage-domain/src/index.ts:130-139
//
// 调用方要看的是记录为什么不合法，而不是关闭单元时的次生错误。
func TestAFailedOpenSurfacesTheRecordErrorNotTheCleanupError(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)

	domain := open(t, facility, notesSpec())
	if err := domain.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	corruptRecord(t, medium, "notes", "entries", "bad", `{"title":""}`)

	closeFailure := errors.New("释放单元也砸了")
	backend.set(func(b *flakyBackend) { b.closeErr = closeFailure })

	_, err := facility.Open(t.Context(), notesSpec())
	expectCode(t, err, CodeInvalidRecord)
	if errors.Is(err, closeFailure) {
		t.Fatalf("次生错误不该盖住原因：%v", err)
	}
}

// TestGetAndNamesOnlySeeFullyOpenedDomains 钉住诊断面只交出建完的域。
//
// 源: packages/storage/storage-domain/src/index.ts:158-167
func TestGetAndNamesOnlySeeFullyOpenedDomains(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)

	if _, ok := facility.Get("notes"); ok {
		t.Fatal("还没打开就不该找得到")
	}
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("一个都没开时该是空的，实际 %v", names)
	}

	first := open(t, facility, notesSpec())
	second := notesSpec()
	second.Name = "archive"
	open(t, facility, second)

	found, ok := facility.Get("notes")
	if !ok || found != first {
		t.Fatal("按名字该找回同一个域")
	}
	names := facility.Names()
	// 排好序：archive 在 notes 前面。
	if len(names) != 2 || names[0] != "archive" || names[1] != "notes" {
		t.Fatalf("该按字典序给出两个域，实际 %v", names)
	}

	if err := first.Close(t.Context()); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	if _, ok := facility.Get("notes"); ok {
		t.Fatal("关掉之后不该再找得到")
	}
}

// TestCloseAllClosesEveryDomainEvenIfOneFails 钉住兜底关闭不被单个失败打断。
//
// 源: packages/storage/storage-domain/src/index.ts:169-177
func TestCloseAllClosesEveryDomainEvenIfOneFails(t *testing.T) {
	t.Parallel()

	medium := storagetest.NewMemoryMedium()
	backend := newFlakyBackend(medium)
	facility := attach(t, medium, backend)

	first := notesSpec()
	second := notesSpec()
	second.Name = "archive"
	domainOne, err := facility.Open(t.Context(), first)
	if err != nil {
		t.Fatalf("打开 notes 不该失败：%v", err)
	}
	domainTwo, err := facility.Open(t.Context(), second)
	if err != nil {
		t.Fatalf("打开 archive 不该失败：%v", err)
	}

	closeFailure := errors.New("释放单元砸了")
	backend.set(func(b *flakyBackend) { b.closeErr = closeFailure })

	err = facility.CloseAll(t.Context())
	if !errors.Is(err, closeFailure) {
		t.Fatalf("该把失败带出来：%v", err)
	}
	// 两个都失败了，两条都要在——短路的话只会有一条。
	if strings.Count(err.Error(), closeFailure.Error()) != 2 {
		t.Fatalf("两个域都该被试着关过一次：%v", err)
	}
	// 关失败也照样让出名字（见 Domain.runClose 里那段说明）。
	if names := facility.Names(); len(names) != 0 {
		t.Fatalf("关完不该还占着名字，实际 %v", names)
	}
	if _, ok := facility.Get(domainOne.Name()); ok {
		t.Fatal("notes 该已经摘掉了")
	}
	if _, ok := facility.Get(domainTwo.Name()); ok {
		t.Fatal("archive 该已经摘掉了")
	}
}

// TestUnsubscribeStopsDeliveryAndIsIdempotent 钉住退订。
//
// 源: packages/storage/storage-domain/src/events.ts:36-48
func TestUnsubscribeStopsDeliveryAndIsIdempotent(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	var mutex sync.Mutex
	var count int
	unsubscribe := facility.Subscribe(func(Changed) {
		mutex.Lock()
		defer mutex.Unlock()
		count++
	})

	if err := table.Put(t.Context(), "a", note{Title: "第一条"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	unsubscribe()
	unsubscribe() // 第二次是空操作，不该崩也不该动到别人
	if err := table.Put(t.Context(), "b", note{Title: "第二条"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if count != 1 {
		t.Fatalf("退订之后不该再收到，实际收到 %d 条", count)
	}
}

// TestSubscribingNilIsANoOp 钉住递 nil 订阅者不会在分发时崩掉。
//
// 分发路径上一个 nil 回调会 panic 在**别人**那次写里，而登记它的人早就返回了。
func TestSubscribingNilIsANoOp(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	unsubscribe := facility.Subscribe(nil)
	unsubscribe()

	domain := open(t, facility, notesSpec())
	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "还能写"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
}

// TestAPanickingListenerDoesNotStopTheOthers 钉住分发的第 1、2 条规则。
//
// 源: packages/storage/storage-domain/src/domain.ts:246-261
//
// 变更已经提交了，没跑到的订阅者从此和介质不一致，而它们永远不会知道——
// 所以一个订阅者炸掉既不许掐断后面的，也不许把那次已经成功的写变成失败。
func TestAPanickingListenerDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	var mutex sync.Mutex
	var reached []string
	record := func(name string) {
		mutex.Lock()
		defer mutex.Unlock()
		reached = append(reached, name)
	}

	t.Cleanup(facility.Subscribe(func(Changed) { record("前"); panic("订阅者炸了") }))
	t.Cleanup(facility.Subscribe(func(Changed) { record("后") }))

	if err := table.Put(t.Context(), "a", note{Title: "写照样成功"}); err != nil {
		t.Fatalf("订阅者炸掉不该让写失败：%v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(reached) != 2 || reached[0] != "前" || reached[1] != "后" {
		t.Fatalf("两个订阅者都该跑到，实际 %v", reached)
	}
}

// TestAnInvariantFailureIsRethrownAfterEveryListenerRan 钉住分发的第 3 条规则。
//
// 源: packages/storage/storage-domain/src/domain.ts:246-261
//
// 不变量违例意味着程序写错了，它必须传到发起方手里；但传出去之前，
// 其余订阅者仍然要各自跑到——它们和那个 bug 没关系。
func TestAnInvariantFailureIsRethrownAfterEveryListenerRan(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)

	var mutex sync.Mutex
	var reached []string
	record := func(name string) {
		mutex.Lock()
		defer mutex.Unlock()
		reached = append(reached, name)
	}

	first := &invariants.Error{PackageName: PackageName, Message: "第一条违例"}
	second := &invariants.Error{PackageName: PackageName, Message: "第二条违例"}
	t.Cleanup(facility.Subscribe(func(Changed) { record("一"); panic(first) }))
	t.Cleanup(facility.Subscribe(func(Changed) { record("二"); panic(second) }))
	t.Cleanup(facility.Subscribe(func(Changed) { record("三") }))

	// 直接调 emit：这条规则的观察点是分发本身，不必绕一次真的写。
	// 用例在包内正是为了这个。
	thrown := func() (recovered any) {
		defer func() { recovered = recover() }()
		facility.emit(Changed{Domain: "notes", Table: "entries", Key: "a", Operation: OperationPut})
		return nil
	}()

	failure, ok := thrown.(*invariants.Error)
	if !ok {
		t.Fatalf("该重新抛出 *invariants.Error，实际 %#v", thrown)
	}
	if failure != first {
		// 只留第一条：后面的多半是同一个原因的连锁反应，抛最早的那个离现场最近。
		t.Fatalf("该抛第一条违例，实际 %q", failure.Message)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(reached) != 3 {
		t.Fatalf("三个订阅者都该跑到，实际 %v", reached)
	}
}

// TestListenerFailuresAreLoggedWithoutTheValue 钉住那条警告不带记录内容。
//
// 源: packages/storage/storage-domain/src/domain.ts:255-260
//
// 记录里完全可能有敏感数据，而一个 panic 的载荷里也常带着刚读到的那一段。
func TestListenerFailuresAreLoggedWithoutTheValue(t *testing.T) {
	t.Parallel()

	var buffer strings.Builder
	logged := slog.New(slog.NewTextHandler(&buffer, nil))

	medium := storagetest.NewMemoryMedium()
	hub := storage.New()
	if _, err := hub.Backend.Register("main", storagetest.NewMemoryBackend(medium)); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}
	facility, err := New(Config{Storage: hub, Backend: "main", Logger: logged})
	if err != nil {
		t.Fatalf("建设施不该失败：%v", err)
	}

	t.Cleanup(facility.Subscribe(func(Changed) { panic("订阅者炸了") }))
	domain := open(t, facility, notesSpec())
	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "机密标题"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	line := buffer.String()
	if !strings.Contains(line, "notes") || !strings.Contains(line, "entries") || !strings.Contains(line, "a") {
		t.Fatalf("该记下是哪一次变更：%q", line)
	}
	if strings.Contains(line, "机密标题") {
		t.Fatalf("不该把记录内容记进日志：%q", line)
	}
}

// TestDescribeNamesSaysSomethingWhenThereIsNothing 钉住空列表拼出来是一个词而不是空白。
//
// 空白让读的人分不清「一个都没有」和「这句话写漏了」。
func TestDescribeNamesSaysSomethingWhenThereIsNothing(t *testing.T) {
	t.Parallel()

	if got := describeNames(nil); got != "无" {
		t.Fatalf("空列表该给「无」，实际 %q", got)
	}
	if got := describeNames([]string{"a", "b"}); got != "a, b" {
		t.Fatalf("非空该逗号连起来，实际 %q", got)
	}
}

// corruptRecord 从介质那一侧塞一条原始 JSON 进去，绕开所有校验。
//
// 用例要它是因为「介质上存着的记录过不了校验」在正常路径上造不出来——
// 写路径本身就会校验（见 [DefineTable]）。这类记录的真实来源是上一版程序、
// 手改的文件、或者另一个进程，它们都在这一层之外。
func corruptRecord(t *testing.T, medium *storagetest.MemoryMedium, unit, table, key, raw string) {
	t.Helper()

	withUnit(t, medium, unit, func(opened storage.KVUnit) {
		if err := opened.PutRecord(t.Context(), table, key, json.RawMessage(raw)); err != nil {
			t.Fatalf("塞坏记录不该失败：%v", err)
		}
	})
}

// corruptGlobal 从介质那一侧塞一个原始全局值进去，理由同 [corruptRecord]。
func corruptGlobal(t *testing.T, medium *storagetest.MemoryMedium, unit, raw string) {
	t.Helper()

	withUnit(t, medium, unit, func(opened storage.KVUnit) {
		if err := opened.SetGlobal(t.Context(), json.RawMessage(raw)); err != nil {
			t.Fatalf("塞坏的全局值不该失败：%v", err)
		}
	})
}

// repairRecord 是 [corruptRecord] 的反面：把那条坏记录删掉。
func repairRecord(t *testing.T, medium *storagetest.MemoryMedium, unit, table, key string) {
	t.Helper()

	withUnit(t, medium, unit, func(opened storage.KVUnit) {
		if err := opened.DeleteRecord(t.Context(), table, key); err != nil {
			t.Fatalf("删坏记录不该失败：%v", err)
		}
	})
}

// withUnit 在介质上另开一个后端、开一次单元、跑完就收干净。
//
// 另开一个后端而不是复用设施那个：设施持有的单元此刻可能正开着，
// 同一个后端上同名单元开第二次是调用方的 bug（后端会拒），而介质本身是共用的。
func withUnit(t *testing.T, medium *storagetest.MemoryMedium, unit string, body func(storage.KVUnit)) {
	t.Helper()

	backend := storagetest.NewMemoryBackend(medium)
	defer func() { _ = backend.Close(context.Background()) }()

	descriptor := notesSpec().Descriptor()
	descriptor.Name = unit

	opened, err := backend.KV().Open(t.Context(), descriptor)
	if err != nil {
		t.Fatalf("打开单元 %q 不该失败：%v", unit, err)
	}
	defer func() { _ = opened.Close(context.Background()) }()

	body(opened)
}
