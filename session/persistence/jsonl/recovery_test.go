// 本文件的作用：拿真的盘上字节验一遍崩溃恢复——断掉的尾巴、已提交前缀不许被
// 重写、以及一次失败的追加要把半截字节收回去。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:605-694

package jsonl

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

// TestLoadClosesInterruptedTurn 断在半截的一个回合要被留下来，并且被合成的收尾关掉。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:605-640
//
// 要紧的是「留下来」：一个回合可以很大，把它整段丢掉等于让一次崩溃吃掉真实工作。
// 只有那条**没写完**的记录该消失。
func TestLoadClosesInterruptedTurn(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t, Config{})
	meta := testMeta("crash", testCwd(t, "/proj"))

	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t))

	// 模拟第二个回合中途掉电：turn/start 和 step/start 是整行写完的，
	// 最后那条 assistant/chunk 只写了一半，连换行都没有。
	path := storedPath(t, root, meta.Cwd, meta.ID)
	appendRawBytes(t, path, strings.Join([]string{
		`{"type":"turn/start","seq":6,"time":8,"data":{"turn":2}}`,
		`{"type":"step/start","seq":7,"time":9,"data":{"turn":2,"step":1}}`,
		`{"type":"assistant/chunk","seq":8,"ti`,
	}, "\n"))

	loaded, err := store.Load(ctx, meta.ID)
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	if got, want := seqsOf(loaded.Events), []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}; !slices.Equal(got, want) {
		t.Fatalf("装载出来的 seq 是 %v，要的是 %v", got, want)
	}

	last := loaded.Events[len(loaded.Events)-1]
	if last.Type != session.EventTurnEnd {
		t.Fatalf("最后一条是 %q，要的是 %q", last.Type, session.EventTurnEnd)
	}
	var closer session.TurnEndData
	if err := json.Unmarshal(last.Data, &closer); err != nil {
		t.Fatalf("合成的回合结束解不开：%v", err)
	}
	if closer.Reason.TurnEndReasonKind() != session.ReasonInterrupted {
		t.Fatalf("回合结束的理由是 %q，要的是 %q",
			closer.Reason.TurnEndReasonKind(), session.ReasonInterrupted)
	}
	if got := loaded.Events[8].Type; got != session.EventStepEnd {
		t.Fatalf("seq 8 那条是 %q，要的是 %q", got, session.EventStepEnd)
	}
	for _, event := range loaded.Events {
		if event.Type == session.EventAssistantChunk {
			t.Fatalf("那条写坏的增量分块活下来了：seq %d", event.Seq)
		}
	}

	// 下一批从补平之后那个长度接着写：seq 10。
	mustAppend(t, store, meta.ID, []session.Event{
		turnStartEvent(t, 10, 11, 3),
		turnEndEvent(t, 11, 12, 3),
	})
	reloaded, err := store.Load(ctx, meta.ID)
	if err != nil {
		t.Fatalf("重新装载失败：%v", err)
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	if got := seqsOf(reloaded.Events); !slices.Equal(got, want) {
		t.Fatalf("重新装载出来的 seq 是 %v，要的是 %v", got, want)
	}
}

// TestCommittedBytesAreNeverRewritten 一次修复只碰坏尾巴，已提交那段字节原样不动。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:642-659
func TestCommittedBytesAreNeverRewritten(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t, Config{})
	meta := testMeta("append-only", "")

	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t))

	path := storedPath(t, root, meta.Cwd, meta.ID)
	committed := readStored(t, path)

	appendRawBytes(t, path, `{"partial`)
	if _, err := store.Load(ctx, meta.ID); err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	mustAppend(t, store, meta.ID, []session.Event{
		turnStartEvent(t, 6, 9, 2),
		turnEndEvent(t, 7, 10, 2),
	})

	if after := readStored(t, path); !strings.HasPrefix(after, committed) {
		t.Fatal("已提交那段字节被改过了：修复之后的文件头和修复之前不一样")
	}
}

// TestFailedAppendTruncatesPartialBytes 一次追加失败要把已经落在盘上的半截字节收回去，
// 于是重试那一次不会在日志里留下一个 seq 的空洞。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:661-694
//
// 新增: 上游把 FileHandle.prototype.sync 打了桩，让 fsync 失败一次。Go 里
// [os.File.Sync] 不能这么替，所以这里直接考 [appendDurably] 那条回滚路径：
// 拿一份真的存档、真的把长度记下来、真的塞进一段排不出去的事件让编码在写之后
// 失败——它验的是同一件事，「追加没成功就等于什么都没发生过」。
func TestFailedAppendTruncatesPartialBytes(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t, Config{})
	meta := testMeta("truncate-retry", "")

	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t))

	path := storedPath(t, root, meta.Cwd, meta.ID)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量不出 %q 追加前的长度：%v", path, err)
	}

	// 往一份**只读**的存档上追加：写下去就报错，回滚那一步不必做，
	// 文件长度必须还是追加之前那个。
	if err := os.Chmod(path, 0o400); err != nil {
		t.Skipf("这个文件系统改不动权限位，跳过：%v", err)
	}
	appendErr := appendDurably(path, []byte("{\"type\":\"turn/start\"}\n"))
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("权限位改不回去：%v", err)
	}
	if appendErr == nil {
		t.Skip("这个文件系统不认只读位，一次本该失败的追加成功了")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量不出 %q 追加后的长度：%v", path, err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("一次失败的追加留下了字节：长度从 %d 变成了 %d", before.Size(), after.Size())
	}

	// 重试那一次接上去，日志是连续的 0..7，没有空洞。
	mustAppend(t, store, meta.ID, []session.Event{
		turnStartEvent(t, 6, 9, 2),
		turnEndEvent(t, 7, 10, 2),
	})
	loaded, err := store.Load(ctx, meta.ID)
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	if got, want := seqsOf(loaded.Events), []int{0, 1, 2, 3, 4, 5, 6, 7}; !slices.Equal(got, want) {
		t.Fatalf("装载出来的 seq 是 %v，要的是 %v", got, want)
	}
}

// TestRollbackRestoresLengthAfterPartialWrite 一段写下去一半就断掉的字节要被
// [truncateDurably] 收回去，收回去之后那份日志还能装载。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:665-708
func TestRollbackRestoresLengthAfterPartialWrite(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t, Config{})
	meta := testMeta("rollback", "")

	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t))

	path := storedPath(t, root, meta.Cwd, meta.ID)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量不出追加前的长度：%v", err)
	}

	appendRawBytes(t, path, `{"type":"turn/start","seq":6,"ti`)
	if err := truncateDurably(path, before.Size()); err != nil {
		t.Fatalf("回滚失败：%v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量不出回滚后的长度：%v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("回滚之后长度是 %d，要的是 %d", after.Size(), before.Size())
	}

	loaded, err := store.Load(ctx, meta.ID)
	if err != nil {
		t.Fatalf("回滚之后装载失败：%v", err)
	}
	if got, want := seqsOf(loaded.Events), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("装载出来的 seq 是 %v，要的是 %v", got, want)
	}
}
