// 本文件的作用：压这个后端接上编排器之后那道对外的服务面——建、追加、装载、
// 按水位读、准备一个还没发布的会话，以及那两样这份介质根本没有的东西。
//
// 覆盖率为什么低于 DESIGN.md 第九节那条 ≥99%，写在 helper_test.go 的开头。

package sessionstore

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 不动库的那一小块 ----

func Test没有活会话表就装不出存储(t *testing.T) {
	// 这一查排在建后端之前，所以一个不为 nil 的空池就够了：轮不到用它。
	if _, err := New(t.Context(), Deps{}, Config{
		Medium: datastore.Config{DB: &sql.DB{}},
	}); err == nil {
		t.Fatal("没有活会话表就装不出存储，该拒")
	}
}

// 一份行式存储里没有「那个会话的原始字节」，也没有「这个会话那份存档」可指。
// 两样都是恒定的答案，所以不必先有一份介质才问得出来。
func Test这个存储既没有原始字节也没有按会话的位置(t *testing.T) {
	var store Store

	if store.SupportsRawArtifacts() {
		t.Error("SupportsRawArtifacts 该恒假")
	}
	if _, err := store.ReadRaw(t.Context(), "anyone"); !errors.Is(err, persistence.ErrRawArtifactsUnsupported) {
		t.Errorf("该报 ErrRawArtifactsUnsupported，实际 %v", err)
	}
	if _, ok := store.Locate(testMeta("anyone")); ok {
		t.Error("Locate 该恒假")
	}
}

// ---- 要一个真的数据库的那一大块 ----

// seededStore 装一整套存储，并在里面落一个写完了一个回合的会话。
func seededStore(t *testing.T, id sessionlog.SessionID) (*Store, sessionlog.SessionHeader) {
	t.Helper()

	store := newStore(t)
	meta := testMeta(id)
	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t, 0))
	return store, meta
}

func Test整个会话写读一个来回(t *testing.T) {
	store, meta := seededStore(t, "round-trip")

	loaded, err := store.Load(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	if loaded.Meta.ID != meta.ID {
		t.Errorf("装载出来的头是 %q，要的是 %q", string(loaded.Meta.ID), string(meta.ID))
	}
	if got, want := seqsOf(loaded.Events), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("装载出来的 seq 是 %v，要的是 %v", got, want)
	}
}

// 落地是懒的：一个建了但从没追加过的会话在介质上什么都不留，所以它不出现在列举里。
func TestList只看得见真的写下去过的那些会话(t *testing.T) {
	store, meta := seededStore(t, "written")
	mustCreate(t, store, testMeta("ghost"))

	headers, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if got := idsOf(headers); !slices.Equal(got, []string{string(meta.ID)}) {
		t.Fatalf("列举出来的是 %v，要的是 [%s]", got, string(meta.ID))
	}

	snapshots, err := store.ListSnapshots(t.Context())
	if err != nil {
		t.Fatalf("列举快照失败：%v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("快照有 %d 份，要的是 1 份", len(snapshots))
	}
	if snapshots[0].Revision == "" {
		t.Error("快照带的变更令牌是空的")
	}
}

func TestReadFrom只给要的那一截后缀(t *testing.T) {
	store, meta := seededStore(t, "suffix")

	suffix, err := store.ReadFrom(t.Context(), meta.ID, 3)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if got, want := seqsOf(suffix.Events), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("后缀的 seq 是 %v，要的是 %v", got, want)
	}

	// 水位越过末尾：空后缀是正常答案，不是错。
	beyond, err := store.ReadFrom(t.Context(), meta.ID, 99)
	if err != nil {
		t.Fatalf("水位越过末尾该给空后缀，实际报错：%v", err)
	}
	if len(beyond.Events) != 0 {
		t.Errorf("越过末尾还读出了 %d 条", len(beyond.Events))
	}
}

// 那一段早就被弹掉了，不是「日志坏了」：读的一方拿整份存档的起点就能分清这两种，
// 所以这里给的是一截空后缀，不是一条错误。
func TestReadFrom落在被弹掉那一段里不是错(t *testing.T) {
	store, meta := seededStore(t, "evicted-suffix")
	evictHead(t, store.Backend(), meta.ID, 4)

	suffix, err := store.ReadFrom(t.Context(), meta.ID, 1)
	if err != nil {
		t.Fatalf("水位落在被弹掉的那一段里不该报错：%v", err)
	}
	if got, want := seqsOf(suffix.Events), []int{4, 5}; !slices.Equal(got, want) {
		t.Fatalf("后缀的 seq 是 %v，要的是 %v", got, want)
	}
	if suffix.BaseSeq != 4 {
		t.Errorf("起点是 %d，要的是 4——调用方要靠它才知道 1..3 是被弹掉了", suffix.BaseSeq)
	}
}

func TestInspect只读不发布(t *testing.T) {
	store, meta := seededStore(t, "inspected")

	inspected, err := store.Inspect(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	if got, want := seqsOf(inspected.Events), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("查看到的 seq 是 %v，要的是 %v", got, want)
	}
	// 说了不落盘恢复，那令牌就一下都不许动。
	before, err := store.Backend().ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if _, err := store.Inspect(t.Context(), meta.ID); err != nil {
		t.Fatalf("再查看一次失败：%v", err)
	}
	after, err := store.Backend().ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if after != before {
		t.Errorf("Inspect 动了存档：令牌从 %q 变成了 %q", string(before), string(after))
	}
}

func TestPrepare造出一个还没发布的活会话(t *testing.T) {
	store, meta := seededStore(t, "prepared")

	prepared, err := store.Prepare(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	if prepared == nil {
		t.Fatal("准备出来的会话是 nil")
	}
}

func TestInstall交回一个摘得下来的函数(t *testing.T) {
	store := newStore(t)

	owner, err := scope.New(scope.NewKey("sessionstore-install-test"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	detach, err := store.Install(t.Context(), owner)
	if err != nil {
		t.Fatalf("挂载失败：%v", err)
	}
	if detach == nil {
		t.Fatal("挂载该交回一个摘下来的函数")
	}
	if err := detach(t.Context()); err != nil {
		t.Fatalf("摘下来失败：%v", err)
	}
}
