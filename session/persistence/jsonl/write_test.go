// 本文件的作用：压写那一侧那几道守门——后端怎么配才算配得起来、一个根能不能用、
// 落地时撞上一份已经存在的存档怎么办、以及身份判据的另外那两条分支。
//
// 为什么和 read_test.go 分开：那边验的是「盘上已经有东西，读得对不对」，这边验的
// 是「往盘上放东西之前拦不拦得住」。两组的搭建方向相反——那边先播种再改盘，
// 这边先摆障碍再写。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:150-166
// 源: packages/session/session-persistence-jsonl/src/index.ts:529-542
// 源: packages/session/session-persistence-jsonl/src/index.ts:816-847

package jsonl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

func TestNewBackendRefusesAConfigItCannotHonour(t *testing.T) {
	t.Run("没给根目录", func(t *testing.T) {
		// 没有缺省值是有意的：拿本进程工作目录当缺省，会话文件就会跟着它到处散。
		if _, err := NewBackend(Config{}); err == nil {
			t.Fatal("没有根目录该报错，不该悄悄挑一个")
		}
	})

	t.Run("要的编码本包还没搬过来", func(t *testing.T) {
		_, err := NewBackend(Config{Root: t.TempDir(), Compression: CompressionZstd})
		if err == nil || !strings.Contains(err.Error(), "还没有搬过来") {
			t.Fatalf("该说清这一档还没搬过来，实际 %v", err)
		}
	})

	t.Run("根被一个普通文件占着", func(t *testing.T) {
		// 已经存在的根必须读得动。是个文件的话，第一次落地才炸就太晚了——
		// 那时候使用者已经以为这个存储装配好了。
		//
		// 这一条在两个平台上分得开：POSIX 上 ReadDir 报 ENOTDIR，Windows 上报的
		// 是 ERROR_PATH_NOT_FOUND，映到 fs.ErrNotExist，会被「不存在是正常的」
		// 那条放过去。所以守门那里是 Stat 判 IsDir，不是只看 ReadDir 报没报错。
		occupied := filepath.Join(t.TempDir(), "root")
		if err := os.WriteFile(occupied, []byte("我不是目录"), 0o600); err != nil {
			t.Fatalf("摆一个占位文件失败：%v", err)
		}
		if _, err := NewBackend(Config{Root: occupied}); err == nil {
			t.Fatal("根是个文件，造后端就该失败")
		}
	})

	t.Run("根还不存在", func(t *testing.T) {
		// 不存在是正常的：它在第一次落地时建出来，造后端这一刻不该碰盘。
		missing := filepath.Join(t.TempDir(), "还没有", "这一级")
		backend, err := NewBackend(Config{Root: missing})
		if err != nil {
			t.Fatalf("根还不存在该照样造得出后端：%v", err)
		}
		if _, err := os.Stat(backend.Root()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("造后端不该把根建出来：%q", backend.Root())
		}
	})
}

func TestNewStoreRequiresALiveSessionTable(t *testing.T) {
	if _, err := New(Deps{}, Config{Root: t.TempDir()}); err == nil {
		t.Fatal("没有活会话表该报错——编排层没它对不了账")
	}
	// 配置那一层的错误要原样透上来，不该被存储这一层吞掉换一句含糊的。
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	if _, err := New(Deps{Sessions: sessions}, Config{}); err == nil {
		t.Fatal("根目录没给，造存储该失败")
	}
}

// 落地是这个后端认定为新的那个会话的第一次写。那个位置上已经有文件，就说明盘上
// 另有一个会话和它撞了号——挑一边写下去会安静地毁掉其中一份。
func TestMaterializeRefusesToPublishOverAnExistingArtifact(t *testing.T) {
	store, root, meta := seededStore(t, Config{})
	backend := store.Backend()

	err := backend.materialize(meta, oneTurnLog(t))
	if err == nil || !strings.Contains(err.Error(), "不落地") {
		t.Fatalf("那个位置上已经有一份日志，落地该拒，实际 %v", err)
	}
	// 拒了就一个字节都不许动：被撞上的那一份是别人的历史。
	if got := readStored(t, storedPath(t, root, meta.Cwd, meta.ID)); !strings.Contains(got, `"id":"read-me"`) {
		t.Errorf("原来那份存档被动过了：%q", got)
	}
}

// 同一个身份在另一种物理编码下已经落过地，落地必须当场喊出来，而不是在旁边
// 再写一份——那样一个根下面就有了两份互相看不见的历史。
func TestMaterializeRefusesWhenTheOtherEncodingAlreadyHasIt(t *testing.T) {
	store, root := newTestStore(t, Config{})
	meta := testMeta("twin-encoding", testCwd(t, "/proj"))

	zstd, err := logPath(root, meta.Cwd, meta.ID, CompressionZstd)
	if err != nil {
		t.Fatalf("拼不出另一种编码的路径：%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(zstd), 0o750); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(zstd, []byte("not really zstd"), 0o600); err != nil {
		t.Fatalf("摆一份另一种编码的存档失败：%v", err)
	}

	err = store.Backend().materialize(meta, oneTurnLog(t))
	if err == nil || !strings.Contains(err.Error(), "换一个根") {
		t.Fatalf("另一种编码下已经有它了，落地该拒，实际 %v", err)
	}
}

// 一份编不出存储路径的头没有「它该在哪」这个答案，那时候落地无处可去。
func TestMaterializeRefusesAHeaderThatEncodesToNoPath(t *testing.T) {
	store, _ := newTestStore(t, Config{})

	if err := store.Backend().materialize(testMeta("", testCwd(t, "/proj")), nil); err == nil {
		t.Fatal("空会话标识编不出路径，落地该拒")
	}
}

// 一批排不出去的事件必须在**碰盘之前**炸掉。写下头、再发现事件排不出去，
// 留下的就是一份只有头的存档，而那份存档会被后面每一次列举当成一个真会话。
func TestMaterializeFailsBeforeTouchingDiskWhenEventsCannotBeEncoded(t *testing.T) {
	store, root := newTestStore(t, Config{})
	meta := testMeta("bad-batch", testCwd(t, "/proj"))

	broken := []session.Event{{
		Type: session.EventTurnStart, Seq: 0, Time: 1,
		Data: []byte(`{这不是 json`),
	}}
	if err := store.Backend().materialize(meta, broken); err == nil {
		t.Fatal("排不出去的事件该让落地失败")
	}
	if _, err := os.Stat(storedPath(t, root, meta.Cwd, meta.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Error("落地失败之后盘上不该留下半份存档")
	}
}

func TestAssertStoredIdentityRefusesAHeaderThatIsNotWhoWeAskedFor(t *testing.T) {
	store, root := newTestStore(t, Config{})
	backend := store.Backend()
	meta := testMeta("here", testCwd(t, "/proj"))
	path := storedPath(t, root, meta.Cwd, meta.ID)

	// 位置对、身份也对：这条该过。
	if err := backend.assertStoredIdentity(path, meta, meta.ID); err != nil {
		t.Fatalf("头和位置都对得上，不该拒：%v", err)
	}
	// 要的是另一个会话：这是「读到的存档不是我要的那一份」。
	err := backend.assertStoredIdentity(path, meta, "someone-else")
	if err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("该说清要的是谁，实际 %v", err)
	}
	// 头里的身份编不出路径：那是「这份头本身不成立」，和上面那条不是一回事。
	err = backend.assertStoredIdentity(path, testMeta("", meta.Cwd), "")
	if err == nil || !strings.Contains(err.Error(), "编不出") {
		t.Fatalf("编不出路径的头该单独说一句，实际 %v", err)
	}
}

func TestReadStoredRevisionReportsAMissingSessionAsNotFound(t *testing.T) {
	store, _ := newTestStore(t, Config{})

	if _, err := store.Backend().ReadStoredRevision(context.Background(), "nobody"); !errors.Is(
		err, persistence.ErrSessionNotFound) {
		t.Fatalf("该报 ErrSessionNotFound，实际 %v", err)
	}
}

// 落地之后那个令牌就该有值：它是「变没变」唯一的抓手，空的话那个回合走不通。
func TestReadStoredRevisionAnswersOnceTheSessionIsMaterialized(t *testing.T) {
	store, _, meta := seededStore(t, Config{})

	revision, err := store.Backend().ReadStoredRevision(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("读变更令牌失败：%v", err)
	}
	if revision == "" {
		t.Error("落地过的会话的变更令牌不该是空的")
	}
}

// packChunks 是指针，为的是把「没给」和「明确给了 false」分开——缺省是 true，
// 用零值当缺省的话就没法关掉它。
func TestPackChunksDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	backend, err := NewBackend(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("造后端失败：%v", err)
	}
	if !backend.packChunks {
		t.Error("没给的时候该是缺省的开着")
	}

	off := false
	backend, err = NewBackend(Config{Root: t.TempDir(), PackChunks: &off})
	if err != nil {
		t.Fatalf("造后端失败：%v", err)
	}
	if backend.packChunks {
		t.Error("明确给了 false 就该关掉")
	}
}

// 目标路径上蹲着一个普通文件时，「把这一级建出来」是不可能的。这里必须报错，
// 因为继续走下去就会往一个不是目录的东西里发布。
func TestEnsureDurableDirectoryRefusesAPathOccupiedByAFile(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "占位")
	if err := os.WriteFile(occupied, []byte("我不是目录"), 0o600); err != nil {
		t.Fatalf("摆一个占位文件失败：%v", err)
	}

	err := ensureDurableDirectory(occupied)
	if err == nil || !strings.Contains(err.Error(), "不是一个目录") {
		t.Fatalf("该说清那个位置不是目录，实际 %v", err)
	}
	// 建到底下去也不行：父级不是目录，整条路都走不通。
	if err := ensureDurableDirectory(filepath.Join(occupied, "再下一级")); err == nil {
		t.Fatal("父级是个文件，往下建该失败")
	}

	// 正常那条：一次建出好几级，而且重复建是幂等的。
	deep := filepath.Join(t.TempDir(), "a", "b", "c")
	for range 2 {
		if err := ensureDurableDirectory(deep); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
	}
	info, err := os.Stat(deep)
	if err != nil || !info.IsDir() {
		t.Fatalf("%q 没建成目录：%v", deep, err)
	}
}
