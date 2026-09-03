// Package storagetest 提供键值后端的共用一致性测试。
//
// 源: packages/storage/storage/tests/contract.ts:1-7
//
// 每个后端在自己的测试文件里调一次 [RunKVBackendContract]，传一个绑到它自己那份介质上的
// 工厂。这套用例把 storage 包里写在注释中的每一条契约逐条压住——契约只写在注释里而没有
// 测试的话，两个后端的行为会在没人察觉的情况下分叉，而分叉的症状会出现在换后端之后的
// 某个远处，看起来完全不像是存储层的问题。
//
// # 为什么它不是 _test.go
//
// 这套用例要被**别的包**（每个后端各一个）导入，而 Go 的 _test.go 只属于自己那个包，
// 导不出去。所以它是一个普通包，像标准库的 net/http/httptest 一样导入 testing。
//
// # 哪几条契约不在这里
//
// [storage.CodeMalformedMedium] 要求先把介质弄坏，而「弄坏」在每个后端上是不同的动作
// （删掉一个 JSON 文件里的括号 / 往 SQLite 文件中间写垃圾）。这条只能由各后端在自己的
// 测试里压，放在共用套件里会逼所有后端接受同一种破坏方式。
//
// 「没声明 HasGlobal 却调 SetGlobal」同理：契约只说了它不合法，没有规定用哪个码拒绝。
package storagetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/snight1983/ds-harness-go/storage"
)

// Harness 是一次一致性测试要用的东西：一个开在空介质上的新后端，
// 外加一个「在**同一份介质**上重新开一个后端」的办法。
//
// 源: packages/storage/storage/tests/contract.ts:12-18
//
// Reopen 是持久性那几条的关键：它模拟的是进程重启。没有它的话，「写完就能读到」
// 只证明了写进了内存。
type Harness struct {
	// Backend 是被测后端，开在一份空介质上。
	Backend storage.Backend

	// Reopen 在同一份介质上另开一个后端实例，等价于进程重启之后重新打开。
	Reopen func() (storage.Backend, error)
}

// contractDescriptor 是这套用例用的单元形状。
//
// 源: packages/storage/storage/tests/contract.ts:20-25
//
// 两张表而不是一张：只有一张表时，「表之间互不串」这件事没有被观察的机会。
var contractDescriptor = storage.KVUnitDescriptor{
	Name:      "contract_unit",
	Version:   3,
	Tables:    []string{"alpha", "beta"},
	HasGlobal: true,
}

// RunKVBackendContract 对一个后端实现跑一遍共用一致性测试。
//
// 源: packages/storage/storage/tests/contract.ts:27-102
//
// create 每条用例调一次，给出一份全新的介质——用例之间共享介质的话，
// 一条用例写下的数据会变成下一条用例的隐含前提，而那种耦合在失败时极难看出来。
func RunKVBackendContract(t *testing.T, label string, create func(t *testing.T) Harness) {
	t.Helper()

	t.Run(label, func(t *testing.T) {
		t.Run("打开不存在的单元得到空形状，且快照立刻可读", func(t *testing.T) {
			runOpensMissingUnitAsEmpty(t, create)
		})
		t.Run("记录和全局槽跨重开仍然在", func(t *testing.T) {
			runRoundTripsAcrossReopen(t, create)
		})
		t.Run("写是覆盖，删是幂等", func(t *testing.T) {
			runPutOverwritesAndDeleteIsIdempotent(t, create)
		})
		t.Run("重开时版本不符被拒，且数据一个字没动", func(t *testing.T) {
			runRejectsVersionMismatch(t, create)
		})
		t.Run("关掉之后所有调用都报 closed，且关闭幂等", func(t *testing.T) {
			runRejectsAfterClose(t, create)
		})
		t.Run("同一个单元没关就开第二次被拒", func(t *testing.T) {
			runRejectsDoubleOpen(t, create)
		})
		t.Run("单条读给出值和修订标识", func(t *testing.T) {
			runReadsSingleRecordWithRevision(t, create)
		})
		t.Run("只许建不许覆盖", func(t *testing.T) {
			runCreateIfAbsentRejectsExisting(t, create)
		})
		t.Run("拿过期修订标识写会被拒", func(t *testing.T) {
			runReplaceIfRevisionRejectsStale(t, create)
		})
	})
}

// runOpensMissingUnitAsEmpty 钉住「还没建出来」不该漏给调用方。
//
// 源: packages/storage/storage/tests/contract.ts:34-41
//
// 允许后端把真正的落盘推迟到第一次写，但空形状必须**立刻**给得出来。给不出来的话，
// 调用方会看到一个缺席的表，而缺席和空在它那里会走不同的分支。
func runOpensMissingUnitAsEmpty(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	unit := openUnit(t, harness.Backend, contractDescriptor)
	snapshot, err := unit.LoadAll(ctx)
	if err != nil {
		t.Fatalf("刚打开就该能读出快照：%v", err)
	}

	// 声明过的表必须**在场且为空**，而不是缺席。
	for _, table := range contractDescriptor.Tables {
		records, present := snapshot.Tables[table]
		if !present {
			t.Fatalf("声明过的表 %q 该在场（哪怕一条记录都没有），实际缺席：%v", table, snapshot.Tables)
		}
		if len(records) != 0 {
			t.Errorf("新单元里的表 %q 该是空的，实际 %v", table, records)
		}
	}
	if len(snapshot.Tables) != len(contractDescriptor.Tables) {
		t.Errorf("快照里该正好是声明过的那几张表，实际 %v", snapshot.Tables)
	}
	if snapshot.Global != nil {
		t.Errorf("从没写过的全局槽该是 nil，实际 %s", snapshot.Global)
	}
}

// runRoundTripsAcrossReopen 钉住持久性：写调用返回之后，进程重启也读得回来。
//
// 源: packages/storage/storage/tests/contract.ts:43-59
//
// 那个带斜杠和冒号的记录键是有意的：契约说记录键**永远不会**出现在文件路径里。
// 后端要是把键当成了路径的一段，这个键会让它写到另一个目录，或者干脆失败。
func runRoundTripsAcrossReopen(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	unit := openUnit(t, harness.Backend, contractDescriptor)

	const weirdKey = "weird key / with:stuff"
	putRecord(t, unit, "alpha", "k1", `{"n":1}`)
	putRecord(t, unit, "alpha", "k2", `{"n":2}`)
	putRecord(t, unit, "beta", weirdKey, `{"ok":true}`)
	if _, err := unit.SetGlobal(ctx, json.RawMessage(`{"counter":7}`), nil); err != nil {
		t.Fatalf("SetGlobal 意外失败：%v", err)
	}
	if err := harness.Backend.Close(ctx); err != nil {
		t.Fatalf("关闭后端意外失败：%v", err)
	}

	reopened, err := harness.Reopen()
	if err != nil {
		t.Fatalf("在同一份介质上重开后端失败：%v", err)
	}
	defer closeBackend(t, reopened)

	snapshot, err := openUnit(t, reopened, contractDescriptor).LoadAll(ctx)
	if err != nil {
		t.Fatalf("重开之后读快照失败：%v", err)
	}
	assertTable(t, snapshot, "alpha", map[string]string{"k1": `{"n":1}`, "k2": `{"n":2}`})
	assertTable(t, snapshot, "beta", map[string]string{weirdKey: `{"ok":true}`})
	assertJSONEqual(t, "全局槽", snapshot.Global, `{"counter":7}`)
}

// runPutOverwritesAndDeleteIsIdempotent 钉住写的覆盖语义和删的幂等。
//
// 源: packages/storage/storage/tests/contract.ts:61-72
//
// 删不幂等的话，收尾路径（同一个清理跑了两遍、重试路径上多走了一遍）会失败，
// 而那次失败发生在「本来就已经是想要的状态」上，是一次纯粹多余的报错。
func runPutOverwritesAndDeleteIsIdempotent(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	unit := openUnit(t, harness.Backend, contractDescriptor)
	putRecord(t, unit, "alpha", "k", `{"v":"old"}`)
	putRecord(t, unit, "alpha", "k", `{"v":"new"}`)

	snapshot, err := unit.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll 意外失败：%v", err)
	}
	// 先确认覆盖是覆盖而不是追加——两条并存的话下面那次删除会「看起来成功」。
	assertTable(t, snapshot, "alpha", map[string]string{"k": `{"v":"new"}`})

	// 第一次删得掉，之后都是空操作——「删之前在不在」由后端的返回值回答，
	// 而读穿到介质之后只有真正执行删除的那一步知道答案。
	for _, expectation := range []struct {
		key     string
		existed bool
	}{{"k", true}, {"k", false}, {"never-existed", false}} {
		existed, err := unit.DeleteRecord(ctx, "alpha", expectation.key, nil)
		if err != nil {
			t.Fatalf("删 %q 该是幂等的，实际失败：%v", expectation.key, err)
		}
		if existed != expectation.existed {
			t.Errorf("删 %q 该交回 existed=%v，实际 %v",
				expectation.key, expectation.existed, existed)
		}
	}

	snapshot, err = unit.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll 意外失败：%v", err)
	}
	assertTable(t, snapshot, "alpha", map[string]string{})
}

// runRejectsVersionMismatch 钉住版本不符是**拒绝**，不是迁移、也不是清空。
//
// 源: packages/storage/storage/tests/contract.ts:74-89
//
// 后半段（原版本照样打得开、数据一条不少）比前半段更要紧：一次被拒绝的打开要是顺手
// 动了介质，那么「升级到新版本失败了」就会连带把旧版本的数据毁掉，而调用方拿到的
// 只是一个版本不符的错误，根本不会想到去查数据还在不在。
func runRejectsVersionMismatch(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	unit := openUnit(t, harness.Backend, contractDescriptor)
	putRecord(t, unit, "alpha", "k", `{"v":1}`)
	if err := harness.Backend.Close(ctx); err != nil {
		t.Fatalf("关闭后端意外失败：%v", err)
	}

	reopened, err := harness.Reopen()
	if err != nil {
		t.Fatalf("在同一份介质上重开后端失败：%v", err)
	}
	defer closeBackend(t, reopened)

	facet := kvFacet(t, reopened)
	bumped := contractDescriptor
	bumped.Version = contractDescriptor.Version + 1
	if _, err := facet.Open(ctx, bumped); !hasCode(err, storage.CodeVersionMismatch) {
		t.Fatalf("版本对不上时该报 %s，实际 %v", storage.CodeVersionMismatch, err)
	}

	snapshot, err := openUnit(t, reopened, contractDescriptor).LoadAll(ctx)
	if err != nil {
		t.Fatalf("原版本该照样打得开：%v", err)
	}
	assertTable(t, snapshot, "alpha", map[string]string{"k": `{"v":1}`})
}

// runRejectsAfterClose 钉住关掉之后每一个调用都报 [storage.CodeClosed]，且关闭幂等。
//
// 源: packages/storage/storage/tests/contract.ts:91-100
//
// 关掉之后的读要是**静默地**返回空快照，调用方会以为数据没了；关掉之后的写要是静默成功，
// 那次写就丢了而没人知道。所以这里逐个方法都压一遍，不是只压一个代表。
//
// 关闭幂等是必需的，因为关闭常常同时来自正常收尾和错误清理两条路，谁先到是不确定的。
func runRejectsAfterClose(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	unit := openUnit(t, harness.Backend, contractDescriptor)

	if err := unit.Close(ctx); err != nil {
		t.Fatalf("关单元意外失败：%v", err)
	}
	if err := unit.Close(ctx); err != nil {
		t.Fatalf("单元的 Close 该是幂等的，第二次实际失败：%v", err)
	}

	closedCalls := map[string]func() error{
		"LoadAll":   func() error { _, err := unit.LoadAll(ctx); return err },
		"LoadTable": func() error { _, err := unit.LoadTable(ctx, "alpha"); return err },
		"ReadRecord": func() error {
			_, _, _, err := unit.ReadRecord(ctx, "alpha", "k")
			return err
		},
		"ReadGlobal": func() error { _, _, err := unit.ReadGlobal(ctx); return err },
		"PutRecord": func() error {
			_, err := unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{}`), nil)
			return err
		},
		"DeleteRecord": func() error {
			_, err := unit.DeleteRecord(ctx, "alpha", "k", nil)
			return err
		},
		"SetGlobal": func() error {
			_, err := unit.SetGlobal(ctx, json.RawMessage(`{}`), nil)
			return err
		},
	}
	for name, call := range closedCalls {
		if err := call(); !hasCode(err, storage.CodeClosed) {
			t.Errorf("单元关掉之后 %s 该报 %s，实际 %v", name, storage.CodeClosed, err)
		}
	}

	if err := harness.Backend.Close(ctx); err != nil {
		t.Fatalf("关后端意外失败：%v", err)
	}
	if err := harness.Backend.Close(ctx); err != nil {
		t.Fatalf("后端的 Close 该是幂等的，第二次实际失败：%v", err)
	}
}

// runRejectsDoubleOpen 钉住同一个单元名没关就开第二次必须报错。
//
// 源: packages/storage/storage/src/backend.ts:36-38
//
// 新增: DSH 把这条规则写在 backend.ts 的契约里，还在文件头声称「共用一致性测试逐条检查
// 每一条规则」，但 tests/contract.ts 里没有它。这里补上。
//
// 放过的话，两个句柄会各自持有一份状态，后写的那个把先写的整个覆盖掉，
// 而两次写都返回了成功——数据是在没有任何错误的情况下丢的。
func runRejectsDoubleOpen(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	facet := kvFacet(t, harness.Backend)
	if _, err := facet.Open(ctx, contractDescriptor); err != nil {
		t.Fatalf("第一次打开意外失败：%v", err)
	}
	if _, err := facet.Open(ctx, contractDescriptor); err == nil {
		t.Fatal("同一个单元没关就开第二次本该被拒")
	}
}

// runReadsSingleRecordWithRevision 钉住单条读：值、修订标识、以及「不存在不是错误」。
//
// 新增: 整条用例是本仓库加的，DSH 那套契约里没有单条读——那边上面那一层把权威状态
// 放在进程内存里，读根本不到这一层来。多副本部署下进程内存不再是权威，单条读是
// 唯一读得到「别的副本刚写的那一版」的路。
//
// 同时压住修订标识必须**每次写都换**：不换的话，一个「读到某一版、写了个相同的值」
// 的序列会让另一个副本的守卫把「改过了」误判成「没人动过」。
func runReadsSingleRecordWithRevision(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	unit := openUnit(t, harness.Backend, contractDescriptor)

	// 不存在不是错误——调用方问的就是「在不在」。
	value, revision, found, err := unit.ReadRecord(ctx, "alpha", "k")
	if err != nil || found || value != nil || revision != "" {
		t.Fatalf("没写过的键该是不存在：value=%s revision=%q found=%v err=%v",
			value, revision, found, err)
	}
	// 从没写过的全局槽同样是空的，不是介质坏了。
	global, globalRevision, err := unit.ReadGlobal(ctx)
	if err != nil || global != nil || globalRevision != "" {
		t.Fatalf("没写过的全局槽该是空的：value=%s revision=%q err=%v",
			global, globalRevision, err)
	}

	written := putRecord(t, unit, "alpha", "k", `{"n":1}`)
	value, revision, found, err = unit.ReadRecord(ctx, "alpha", "k")
	if err != nil {
		t.Fatalf("单条读意外失败：%v", err)
	}
	if !found {
		t.Fatal("刚写过的键该读得到")
	}
	assertJSONEqual(t, "alpha/k", value, `{"n":1}`)
	// 写那一路交回的和读那一路交回的必须是同一个，否则拿写的结果去守卫下一次写
	// 会当场判成「有人动过」。
	if revision != written {
		t.Errorf("写给的修订标识是 %q，读给的是 %q", written, revision)
	}

	// 覆盖之后必须换一个新的，**即使写进去的值一模一样**。
	same := putRecord(t, unit, "alpha", "k", `{"n":1}`)
	if same == written {
		t.Errorf("覆盖之后修订标识没变，还是 %q", written)
	}

	// 全局槽走的是同一套。
	firstGlobal, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":1}`), nil)
	if err != nil {
		t.Fatalf("SetGlobal 意外失败：%v", err)
	}
	secondGlobal, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":1}`), nil)
	if err != nil {
		t.Fatalf("重盖全局槽意外失败：%v", err)
	}
	if firstGlobal == "" || firstGlobal == secondGlobal {
		t.Errorf("重盖全局槽之后修订标识该变：先是 %q，后是 %q", firstGlobal, secondGlobal)
	}
	global, globalRevision, err = unit.ReadGlobal(ctx)
	if err != nil {
		t.Fatalf("读全局槽意外失败：%v", err)
	}
	assertJSONEqual(t, "全局槽", global, `{"c":1}`)
	if globalRevision != secondGlobal {
		t.Errorf("写给的全局槽修订标识是 %q，读给的是 %q", secondGlobal, globalRevision)
	}
}

// runCreateIfAbsentRejectsExisting 钉住 [storage.CreateIfAbsent]：已经在了就得拒，
// 且介质上一个字都不许改。
//
// 新增: 整条用例是本仓库加的，理由同 runReadsSingleRecordWithRevision。
//
// 这条压的是「只许建一次」这类调用（登记一个新工作区、抢一次锁）：放过的话，
// 两个副本会各自以为自己是那个建出来的人。
func runCreateIfAbsentRejectsExisting(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	unit := openUnit(t, harness.Backend, contractDescriptor)

	if _, err := unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{"v":1}`),
		storage.CreateIfAbsent{}); err != nil {
		t.Fatalf("第一次建意外失败：%v", err)
	}
	_, err := unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{"v":2}`), storage.CreateIfAbsent{})
	if !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("第二次建该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}
	// 被拒的那一次一个字都不许改。
	value, _, _, err := unit.ReadRecord(ctx, "alpha", "k")
	if err != nil {
		t.Fatalf("单条读意外失败：%v", err)
	}
	assertJSONEqual(t, "alpha/k", value, `{"v":1}`)

	// 全局槽走的是同一套。
	if _, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":1}`), storage.CreateIfAbsent{}); err != nil {
		t.Fatalf("第一次建全局槽意外失败：%v", err)
	}
	_, err = unit.SetGlobal(ctx, json.RawMessage(`{"c":2}`), storage.CreateIfAbsent{})
	if !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("重建全局槽该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}
}

// runReplaceIfRevisionRejectsStale 钉住 [storage.ReplaceIfRevision]：这是读-改-写唯一
// 安全的收尾方式，而这条用例压的正是它拦不住时会发生的那件事——丢更新。
//
// 新增: 整条用例是本仓库加的，理由同 runReadsSingleRecordWithRevision。
func runReplaceIfRevisionRejectsStale(t *testing.T, create func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	harness := create(t)
	defer closeBackend(t, harness.Backend)

	unit := openUnit(t, harness.Backend, contractDescriptor)

	stale := putRecord(t, unit, "alpha", "k", `{"v":1}`)
	// 「别人」在这中间改了一次。
	fresh, err := unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{"v":2}`),
		storage.ReplaceIfRevision{Revision: stale})
	if err != nil {
		t.Fatalf("拿最新修订标识写该成功：%v", err)
	}
	_, err = unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{"v":3}`),
		storage.ReplaceIfRevision{Revision: stale})
	if !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("拿过期修订标识写该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}
	value, _, _, err := unit.ReadRecord(ctx, "alpha", "k")
	if err != nil {
		t.Fatalf("单条读意外失败：%v", err)
	}
	assertJSONEqual(t, "alpha/k", value, `{"v":2}`)

	// 别处发的修订标识当作**对不上**处理，不当作格式错误——调用方真正的问题是
	// 「我以为我读过这条记录」。
	_, err = unit.PutRecord(ctx, "alpha", "k", json.RawMessage(`{"v":4}`),
		storage.ReplaceIfRevision{Revision: "别处发的"})
	if !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("认不出的修订标识该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}

	// 删也守得住：拿过期的删不掉，拿最新的删得掉，记录不在了之后那一版自然也守不住。
	if _, err := unit.DeleteRecord(ctx, "alpha", "k",
		&storage.ReplaceIfRevision{Revision: stale}); !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("拿过期修订标识删该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}
	existed, err := unit.DeleteRecord(ctx, "alpha", "k", &storage.ReplaceIfRevision{Revision: fresh})
	if err != nil || !existed {
		t.Fatalf("拿最新修订标识删该成功：existed=%v err=%v", existed, err)
	}
	if _, err := unit.DeleteRecord(ctx, "alpha", "k",
		&storage.ReplaceIfRevision{Revision: fresh}); !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("删一个已经不在的记录该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}

	// 全局槽走的是同一套。
	single, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":1}`), nil)
	if err != nil {
		t.Fatalf("SetGlobal 意外失败：%v", err)
	}
	if _, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":2}`),
		storage.ReplaceIfRevision{Revision: single}); err != nil {
		t.Fatalf("拿最新修订标识盖全局槽该成功：%v", err)
	}
	if _, err := unit.SetGlobal(ctx, json.RawMessage(`{"c":3}`),
		storage.ReplaceIfRevision{Revision: single}); !hasCode(err, storage.CodeStaleRevision) {
		t.Fatalf("拿过期修订标识盖全局槽该报 %s，实际 %v", storage.CodeStaleRevision, err)
	}
}

// kvFacet 取出被测后端的键值操作组，取不到就让测试停下。
func kvFacet(t *testing.T, backend storage.Backend) storage.KVFacet {
	t.Helper()

	facet, ok := storage.KV(backend)
	if !ok {
		t.Fatal("这套用例只针对键值后端，而被测后端不提供键值形态")
	}
	return facet
}

// openUnit 打开契约用的那个单元，失败就让测试停下。
func openUnit(t *testing.T, backend storage.Backend, descriptor storage.KVUnitDescriptor) storage.KVUnit {
	t.Helper()

	unit, err := kvFacet(t, backend).Open(context.Background(), descriptor)
	if err != nil {
		t.Fatalf("打开单元 %q 意外失败：%v", descriptor.Name, err)
	}
	return unit
}

// putRecord 无条件写一条记录，交回写完之后的修订标识。失败就让测试停下。
func putRecord(t *testing.T, unit storage.KVUnit, table, key, value string) storage.Revision {
	t.Helper()

	revision, err := unit.PutRecord(context.Background(), table, key, json.RawMessage(value), nil)
	if err != nil {
		t.Fatalf("往 %s/%s 写记录意外失败：%v", table, key, err)
	}
	if revision == "" {
		// 空串在契约里的意思是「这条记录不存在」，而这次写刚刚成功了。
		t.Fatalf("往 %s/%s 写成功之后该交回一个非空的修订标识", table, key)
	}
	return revision
}

// closeBackend 收尾关后端，失败只报不停——此时用例本身的断言已经跑完了。
func closeBackend(t *testing.T, backend storage.Backend) {
	t.Helper()

	if err := backend.Close(context.Background()); err != nil {
		t.Errorf("收尾关闭后端失败：%v", err)
	}
}

// hasCode 判断一个错误是不是带某个 [storage.ErrorCode] 的 *[storage.Error]。
//
// 用 errors.As 而不是类型断言：后端完全可以把它包一层再返回，而包过之后断言就不成立了。
func hasCode(err error, code storage.ErrorCode) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == code
}

// assertTable 断言快照里某张表的内容和期望**完全一致**，多一条少一条都算不一致。
func assertTable(t *testing.T, snapshot storage.Snapshot, table string, want map[string]string) {
	t.Helper()

	got, present := snapshot.Tables[table]
	if !present {
		t.Fatalf("表 %q 该在快照里，实际缺席：%v", table, snapshot.Tables)
	}
	if len(got) != len(want) {
		t.Fatalf("表 %q 该有 %d 条记录，实际 %d 条：%v", table, len(want), len(got), got)
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Errorf("表 %q 里该有记录 %q，实际没有", table, key)
			continue
		}
		assertJSONEqual(t, table+"/"+key, gotValue, wantValue)
	}
}

// assertJSONEqual 按 JSON 的**语义**比较，而不是按字节。
//
// 按字节比会把后端的序列化排版（键序、空格）当成契约的一部分，而契约明说值对这一层
// 是不透明的 JSON——一个把值重新编码过的后端会因为多了一个空格而失败，那是假警报。
func assertJSONEqual(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()

	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Errorf("%s 读回来的不是合法 JSON：%v（原文 %s）", what, err, got)
		return
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("用例自己写的期望值 %q 不是合法 JSON：%v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("%s 该是 %s，实际 %s", what, want, got)
	}
}
