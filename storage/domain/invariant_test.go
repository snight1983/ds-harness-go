// 本文件验这个包自己那条运行期不变量：三条检查各自会在什么时候响。
//
// 源: packages/storage/storage-domain/src/invariant.ts:12-67
//
// 三条查的都是「事件说的话和此刻的状态对不对得上」，而正常代码路径永远不会让它们
// 对不上——那正是这条不变量存在的意义（次序被换成「先发事件再改内存」时它当场抓到）。
// 所以用例是直接调 [Facility.emit] 发一条**捏造的**通知，而不是绕一次真的写。
// 这是白盒手法，同包测试拿得到，也只有这里拿得到。

package domain

import (
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
)

// newRegistry 造一条全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// arm 装上这条检查，并返回一个「设施还活着吗」的开关。
func arm(t *testing.T, facility *Facility) *bool {
	t.Helper()

	live := true
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), facility, func() bool { return live })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
	return &live
}

// expectViolation 发一条通知，要求它以一条不变量违例 panic，且文案里带某个片段。
//
// 违例是**重新抛出**的（见 [Facility.emit] 的第 3 条规则），所以它到得了发起方手里——
// 这条断言同时也是「重抛」那条规则的证据。
func expectViolation(t *testing.T, fragment string, notify func()) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("该抛出不变量违例，实际什么都没抛")
		}
		failure, isInvariant := recovered.(*invariants.Error)
		if !isInvariant {
			t.Fatalf("抛出来的该是 *invariants.Error，实际 %#v", recovered)
		}
		if failure.PackageName != PackageName {
			t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
		}
		if !strings.Contains(failure.Message, fragment) {
			t.Errorf("违例文案里该有 %q，实际 %q", fragment, failure.Message)
		}
	}()

	notify()
}

// TestRegisterInvariantsRequiresAllThreePieces 钉住装配方必须把三样都递进来。
//
// 源: packages/storage/storage-domain/src/invariant.ts:61-67
//
// DSH 那边这三样都由 cordis 从上下文里取（invariant.ts:21 的 inject 和 apply 的 ctx），
// 装配方什么都不用写。Go 里没有容器，三样都是显式入参，缺一个就装不上。
// 「这个设施还挂着吗」只有那个 New 出它、也负责关掉它的人知道，所以 live 也是必填。
func TestRegisterInvariantsRequiresAllThreePieces(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	registry := newRegistry(t)
	alive := func() bool { return true }

	if _, err := RegisterInvariants(t.Context(), nil, facility, alive); err == nil {
		t.Fatal("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), registry, nil, alive); err == nil {
		t.Fatal("没有设施该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), registry, facility, nil); err == nil {
		t.Fatal("没有存活判据该装不上")
	}
}

// TestARealWriteSatisfiesTheInvariant 钉住正常路径一条都不响。
//
// 这条用例是其余几条的地基：如果一条永远会响的检查，那它抓到什么都不说明问题。
func TestARealWriteSatisfiesTheInvariant(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)

	domain := open(t, facility, notesSpec())
	table := entries(t, domain)

	if err := table.Put(t.Context(), "a", note{Title: "写"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if err := table.Put(t.Context(), "a", note{Title: "改"}); err != nil {
		t.Fatalf("覆盖不该失败：%v", err)
	}
	if removed, err := table.Delete(t.Context(), "a"); err != nil || !removed {
		t.Fatalf("删不该失败：%v %v", removed, err)
	}
	if err := prefs(t, domain).Set(t.Context(), preference{Theme: "dark"}); err != nil {
		t.Fatalf("写全局不该失败：%v", err)
	}
}

// TestANotificationAfterTheFacilityIsGoneIsAViolation 钉住第 1 条的前半。
//
// 源: packages/storage/storage-domain/src/invariant.ts:26-29
//
// 通知陈述的是一次已经落盘的变更，设施都没了就没有「变更进哪里」可言。
//
// 新增: DSH 只问「这个域开着吗」——设施本身由容器持有，不存在「设施没了但还在发事件」
// 这个状态。Go 里设施是调用方自己拿着的一个指针，所以这一问拆成了两半，这是前一半。
func TestANotificationAfterTheFacilityIsGoneIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	live := arm(t, facility)
	open(t, facility, notesSpec())

	*live = false
	expectViolation(t, "已经不在了", func() {
		facility.emit(Changed{Domain: "notes", Table: "entries", Key: "a", Operation: OperationPut})
	})
}

// TestANotificationForAClosedDomainIsAViolation 钉住第 1 条的后半。
//
// 源: packages/storage/storage-domain/src/invariant.ts:26-29
func TestANotificationForAClosedDomainIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)

	expectViolation(t, "并没有开着", func() {
		facility.emit(Changed{Domain: "从来没开过", Table: "entries", Key: "a", Operation: OperationPut})
	})
}

// TestAPutEventCarryingTheWrongValueIsAViolation 钉住第 2 条。
//
// 源: packages/storage/storage-domain/src/invariant.ts:47-53
//
// 事件和状态分了叉，收到通知的那一方会照着事件里的值走——两边从此各说各话，
// 且谁都不会发现。这一条是「先落盘、再改内存、再发事件」在运行期的证据。
func TestAPutEventCarryingTheWrongValueIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	domain := open(t, facility, notesSpec())

	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "内存里的"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	expectViolation(t, "对不上", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationPut,
			Value:     []byte(`{"title":"事件里的","count":0}`),
		})
	})
}

// TestAPutEventForAnAbsentRecordIsAViolation 钉住第 2 条里「内存里根本没有它」那一支。
//
// 源: packages/storage/storage-domain/src/invariant.ts:47-53
func TestAPutEventForAnAbsentRecordIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "内存里没有它", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "从来没写过",
			Operation: OperationPut,
			Value:     []byte(`{"title":"凭空","count":0}`),
		})
	})
}

// TestADeleteEventForALivingRecordIsAViolation 钉住第 3 条。
//
// 源: packages/storage/storage-domain/src/invariant.ts:39-45
//
// 一条还在的记录被宣布删除，会让订阅者据此丢掉自己那份缓存，而下一次读又把它读回来。
func TestADeleteEventForALivingRecordIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	domain := open(t, facility, notesSpec())

	if err := entries(t, domain).Put(t.Context(), "a", note{Title: "还活着"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	expectViolation(t, "还在内存里", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationDeleted,
		})
	})
}

// TestAGlobalEventCarryingTheWrongValueIsAViolation 钉住全局槽那一路。
//
// 源: packages/storage/storage-domain/src/invariant.ts:30-35
//
// 全局槽用空表名表示，和 [Changed] 与 [RecordSlot] 是同一套约定。
func TestAGlobalEventCarryingTheWrongValueIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	domain := open(t, facility, notesSpec())

	if err := prefs(t, domain).Set(t.Context(), preference{Theme: "dark"}); err != nil {
		t.Fatalf("写全局不该失败：%v", err)
	}

	expectViolation(t, "对不上", func() {
		facility.emit(Changed{
			Domain: "notes", Operation: OperationPut,
			Value: []byte(`{"theme":"light"}`),
		})
	})
}

// TestAGlobalEventOnADomainWithoutAGlobalSlotIsAViolation 钉住读不到当前值那一支。
//
// 源: packages/storage/storage-domain/src/invariant.ts:30-35
//
// 一条声称写了全局值的通知，发自一个根本没有全局槽的域——这只可能是程序写错了。
//
// 新增: DSH 那边 `domain.global.get()` 在没有全局槽时是取一个 never 类型的句柄，
// 编译期就过不去；Go 的 [Domain.RawGlobal] 返回 error，于是这一支在运行期才现形。
func TestAGlobalEventOnADomainWithoutAGlobalSlotIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)

	spec := notesSpec()
	spec.Global = nil
	open(t, facility, spec)

	expectViolation(t, "读不到当前值", func() {
		facility.emit(Changed{Domain: "notes", Operation: OperationPut, Value: []byte(`{}`)})
	})
}

// TestARecordEventOnAnUndeclaredTableIsAViolation 钉住记录那一路读不到当前值的情况。
//
// 源: packages/storage/storage-domain/src/invariant.ts:37-37
func TestARecordEventOnAnUndeclaredTableIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "读不到当前值", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "没声明过的表", Key: "a",
			Operation: OperationPut, Value: []byte(`{}`),
		})
	})
}

// TestUnregisteringTheInvariantAlsoDropsTheSubscription 钉住注销把订阅一起摘掉。
//
// 源: packages/storage/storage-domain/src/invariant.ts:58-59
//
// 摘不掉的话，一条不该再查的检查会继续在**别人**的写路径上抛。
func TestUnregisteringTheInvariantAlsoDropsTheSubscription(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), facility, func() bool { return true })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	open(t, facility, notesSpec())

	dispose()

	// 注销之后，同一条本该违例的通知一声不响地过去。
	facility.emit(Changed{Domain: "从来没开过", Table: "entries", Key: "a", Operation: OperationPut})
}
