// 本文件验这个包自己那条运行期不变量：四条检查各自会在什么时候响。
//
// 源: packages/storage/storage-domain/src/invariant.ts:12-67
//
// 四条查的都是「这条事件自身说得通吗」——它指的域开着吗、指的表声明过吗、
// 它带的东西和它宣称的动作配吗。正常代码路径永远不会让它们说不通，那正是这条
// 不变量存在的意义（次序被换成「先发事件再落盘」时，第 3 条当场抓到）。
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

// TestAPutEventWithoutAReceiptIsAViolation 钉住第 3 条，也是这条不变量最要紧的一支。
//
// 源: packages/storage/storage-domain/src/invariant.ts:47-53
//
// 修订标识只可能来自后端确认落盘之后的那个返回值（见 [Table.store]），凑不出来。
// 一条没带它的写入事件，只能是发在落盘之前——而那意味着订阅者会看见一次
// 可能根本没成功的写。
//
// 新增: DSH 那一条查的是「事件里的值等于此刻内存里那一份」。内存权威态删掉之后
// 那个比法没了对象，换成读介质又会误报（理由见 [RegisterInvariants]），
// 于是判据换成了这张收据。
func TestAPutEventWithoutAReceiptIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "发在后端确认落盘之前", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationPut,
			Value:     []byte(`{"title":"没收据","count":0}`),
		})
	})
}

// TestAPutEventWithoutAValueIsAViolation 钉住第 3 条的另一半。
//
// 源: packages/storage/storage-domain/src/invariant.ts:47-53
//
// 一条不带值的写入事件，订阅者没法据它做任何事——它连「写进去的是什么」都说不出来。
func TestAPutEventWithoutAValueIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "没带值", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationPut, Revision: "7",
		})
	})
}

// TestADeleteEventCarryingAValueIsAViolation 钉住第 4 条的前半：墓碑不带值。
//
// 源: packages/storage/storage-domain/src/invariant.ts:39-45
func TestADeleteEventCarryingAValueIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "墓碑不带值", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationDeleted,
			Value:     []byte(`{"title":"删了还带值","count":0}`),
		})
	})
}

// TestADeleteEventCarryingAReceiptIsAViolation 钉住第 4 条的后半：删除不产生新的一版。
//
// 一条删除事件带着修订标识，说明发它的那条路把删当成了一次写——而删掉的记录
// 没有「这一版」可言，那个号只能是从别处抄来的。
func TestADeleteEventCarryingAReceiptIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "删除不产生新的一版", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "entries", Key: "a",
			Operation: OperationDeleted, Revision: "7",
		})
	})
}

// TestAGlobalDeleteEventIsAViolation 钉住全局槽那一路。
//
// 源: packages/storage/storage-domain/src/invariant.ts:30-35
//
// 全局槽用空表名表示，和 [Changed] 与 [RecordSlot] 是同一套约定。
// [Global] 上根本没有删除这个操作，所以这样一条事件只可能是程序写错了。
func TestAGlobalDeleteEventIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "全局槽根本删不掉", func() {
		facility.emit(Changed{Domain: "notes", Operation: OperationDeleted})
	})
}

// TestARecordEventOnAnUndeclaredTableIsAViolation 钉住第 2 条。
//
// 源: packages/storage/storage-domain/src/invariant.ts:37-37
//
// 指到一张这个域没声明过的表，按表名分派的订阅者会静静地丢掉它。
func TestARecordEventOnAnUndeclaredTableIsAViolation(t *testing.T) {
	t.Parallel()

	_, facility := boot(t)
	arm(t, facility)
	open(t, facility, notesSpec())

	expectViolation(t, "没有声明过它", func() {
		facility.emit(Changed{
			Domain: "notes", Table: "没声明过的表", Key: "a",
			Operation: OperationPut, Revision: "7", Value: []byte(`{}`),
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
