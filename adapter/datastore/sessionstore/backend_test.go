// 本文件的作用：压后端自己那一层——读写一个来回、起点怎么跟着弹出走、
// 变更令牌的来源限定，以及那几条「这不是我该收下的东西」的拒绝。
//
// 覆盖率为什么低于 DESIGN.md 第九节那条 ≥99%，写在 helper_test.go 的开头。

package sessionstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 不动库的那一小块 ----

func Test后端报得出自己的名字(t *testing.T) {
	if got := (&Backend{}).Name(); got != BackendName {
		t.Errorf("后端名是 %q，要的是 %q", got, BackendName)
	}
}

func Test没有连接池就建不出后端(t *testing.T) {
	if _, err := NewBackend(t.Context(), Config{}); err == nil {
		t.Fatal("没有连接池就没有介质，该拒")
	}
}

// 命名空间会被拼进语句文本，所以它必须在碰库之前就先被卡住。传一个不为 nil 的
// 空池就够了：这一查发生得比第一次用池还早。
func Test不合法的命名空间名在动库之前就被拒(t *testing.T) {
	db := &sql.DB{}

	for _, namespace := range []string{"Public", "1st", "with-dash", "带中文"} {
		_, err := NewBackend(t.Context(), Config{
			Medium: datastore.Config{DB: db, Namespace: namespace},
		})
		if !errors.Is(err, datastore.ErrMalformedName) {
			t.Errorf("命名空间 %q：该报 ErrMalformedName，实际 %v", namespace, err)
		}
	}
}

// 一次写就是一个事务，这个后端造不出断尾，所以它从不发断尾凭据。收到一张就说明
// 编排器把别的后端的凭据递错了地方——照着一个由别人算出来的值去截断是最坏的选择。
func Test收到一张不是自己发的断尾凭据会被拒(t *testing.T) {
	err := (&Backend{}).CommitRepair(t.Context(), testMeta("torn"), "别的后端的凭据", nil)
	if err == nil {
		t.Fatal("该拒掉一张不是自己发的断尾凭据")
	}
}

// 没有断尾、也没有要补的收尾，那就没有活儿：这一下连介质都不该碰，所以拿一个
// 什么都没有的后端就能跑到底。
func Test无事可做的修复根本不碰介质(t *testing.T) {
	if err := (&Backend{}).CommitRepair(t.Context(), testMeta("quiet"), nil, nil); err != nil {
		t.Fatalf("无事可做的修复不该失败：%v", err)
	}
}

// 三道可选的缝，实现哪几道是这个后端的形状决定的，不是随手挑的：所有会话装在
// 同一份介质里，所以没有「这个会话那份存档」可指；按 seq 寻址在这里是走主键的
// 一句读；介质归后端所有，所以必须有人来收。
func Test这个后端填满的正好是它该填的那几道缝(t *testing.T) {
	backend := &Backend{}

	if _, ok := persistence.Seekable(backend); !ok {
		t.Error("按 seq 寻址是这个后端存在的理由之一，该满足 SeekableBackend")
	}
	if _, ok := persistence.Closable(backend); !ok {
		t.Error("介质要收，该满足 ClosableBackend")
	}
	if _, ok := persistence.Trimming(backend); !ok {
		t.Error("按 seq 弹掉最老那一段是下面那一层的一句话，该满足 TrimmingBackend")
	}
	if _, ok := persistence.Locating(backend); ok {
		t.Error("所有会话装在同一份介质里，不该满足 LocatingBackend")
	}
}

// 负的水位在碰介质之前就该被拦住，所以拿一个什么都没有的后端就能跑到底。
func Test负的弹出水位是坏seq(t *testing.T) {
	err := (&Backend{}).TrimBefore(t.Context(), "anyone", -1)
	if !errors.Is(err, persistence.ErrMalformedSeq) {
		t.Fatalf("该报 ErrMalformedSeq，实际 %v", err)
	}
}

// ---- 要一个真的数据库的那一大块 ----

// seededBackend 开一个后端，并在里面落一个写完了一个回合、从 base 起的会话。
func seededBackend(t *testing.T, id sessionlog.SessionID, base int) (*Backend, sessionlog.SessionHeader) {
	t.Helper()

	backend := newBackend(t)
	meta := testMeta(id)
	if err := backend.AppendBatch(t.Context(), meta, oneTurnLog(t, base), false); err != nil {
		t.Fatalf("写第一批失败：%v", err)
	}
	return backend, meta
}

func Test第一批把会话落地并且整批读得回(t *testing.T) {
	backend, meta := seededBackend(t, "whole", 0)

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if stored.Meta != meta {
		t.Errorf("读回来的头是 %+v，要的是 %+v", stored.Meta, meta)
	}
	if got, want := seqsOf(stored.Events), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
	if stored.BaseSeq != 0 {
		t.Errorf("起点是 %d，要的是 0", stored.BaseSeq)
	}
	// 要么整批提交要么一条都没有，所以这个后端永远没有坏尾巴要截。
	if stored.TornMarker != nil {
		t.Errorf("断尾凭据该恒为 nil，实际 %v", stored.TornMarker)
	}
	if stored.Revision == "" {
		t.Error("一个存在的会话拿到了空令牌")
	}
}

func Test会话不在时每条读路都报会话不存在(t *testing.T) {
	backend := newBackend(t)

	if _, err := backend.LoadStored(t.Context(), "nobody"); !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Errorf("整读该报 ErrSessionNotFound，实际 %v", err)
	}
	if _, err := backend.LoadStoredFrom(t.Context(), "nobody", 0); !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Errorf("寻址读该报 ErrSessionNotFound，实际 %v", err)
	}
	if _, err := backend.ReadStoredRevision(t.Context(), "nobody"); !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Errorf("读令牌该报 ErrSessionNotFound，实际 %v", err)
	}
}

// 起点是个变量：这份日志会从最老的一头弹出事件，所以「从哪个 seq 起」问的是
// 现存最早那条，不是 0。
func Test起点跟着现存最早那条事件走(t *testing.T) {
	backend, meta := seededBackend(t, "evicted", 0)
	evictHead(t, backend, meta.ID, 3)

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
	if stored.BaseSeq != 3 {
		t.Errorf("起点是 %d，要的是 3", stored.BaseSeq)
	}
}

// 一份全被弹空的存档推不出任何东西，而恰恰是那时候调用方要靠起点决定下一条
// 写在哪儿——所以空的那一种由「下一条要写的 seq」回答，不是回落成 0。
func Test弹空之后起点由下一条要写的seq回答(t *testing.T) {
	backend, meta := seededBackend(t, "emptied", 0)
	evictHead(t, backend, meta.ID, 6)

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if len(stored.Events) != 0 {
		t.Fatalf("全弹空了还读出 %d 条", len(stored.Events))
	}
	if stored.BaseSeq != 6 {
		t.Errorf("起点是 %d，要的是 6——那是下一条要写的 seq", stored.BaseSeq)
	}
}

// 一份存档的第一条 seq 本来就可以不是 0：弹出留下的就是这种样子，而读的一侧
// 现在就得答得出。
func Test存档从非零起点也读得回(t *testing.T) {
	backend, meta := seededBackend(t, "offset", 40)

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{40, 41, 42, 43, 44, 45}; !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
	if stored.BaseSeq != 40 {
		t.Errorf("起点是 %d，要的是 40", stored.BaseSeq)
	}
}

func TestLoadStoredFrom读的是后缀但交的是整份存档的起点(t *testing.T) {
	backend, meta := seededBackend(t, "seek", 0)
	evictHead(t, backend, meta.ID, 2)

	suffix, err := backend.LoadStoredFrom(t.Context(), meta.ID, 4)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if got, want := seqsOf(suffix.Events), []int{4, 5}; !slices.Equal(got, want) {
		t.Fatalf("后缀的 seq 是 %v，要的是 %v", got, want)
	}
	if suffix.Meta.ID != meta.ID {
		t.Errorf("后缀带的头是 %q，要的是 %q", string(suffix.Meta.ID), string(meta.ID))
	}
	// 交的是**整份存档**的起点，不是这一截后缀的起点：读的一方要靠它分清
	// 「请求的水位早就被弹掉了」和「那一段压根没写过」。
	if suffix.BaseSeq != 2 {
		t.Errorf("起点是 %d，要的是 2", suffix.BaseSeq)
	}

	// 水位越过末尾：空后缀是正常答案，不是错。
	beyond, err := backend.LoadStoredFrom(t.Context(), meta.ID, 99)
	if err != nil {
		t.Fatalf("水位越过末尾该给空后缀，实际报错：%v", err)
	}
	if len(beyond.Events) != 0 {
		t.Errorf("越过末尾还读出了 %d 条", len(beyond.Events))
	}
}

func Test负的水位是坏seq不是空后缀(t *testing.T) {
	backend, meta := seededBackend(t, "negative", 0)

	if _, err := backend.LoadStoredFrom(t.Context(), meta.ID, -1); !errors.Is(err, persistence.ErrMalformedSeq) {
		t.Fatalf("该报 ErrMalformedSeq，实际 %v", err)
	}
}

// 令牌不动是「变没变」那个回合的全部依据：不动就该纹丝不动，一动就必须真的动。
func Test令牌写的时候动读的时候不动(t *testing.T) {
	backend, meta := seededBackend(t, "moving", 0)

	before, err := backend.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	again, err := backend.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("再读令牌失败：%v", err)
	}
	if again != before {
		t.Fatalf("什么都没写，令牌却从 %q 变成了 %q", string(before), string(again))
	}

	if err := backend.AppendBatch(t.Context(), meta, oneTurnLog(t, 6), true); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	after, err := backend.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if after == before {
		t.Fatalf("追加之后令牌还是 %q，没动", string(before))
	}
	// 两条读路交出来的必须是同一套表示，否则「读一遍、再核对一遍」永远说变过了。
	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if stored.Revision != after {
		t.Errorf("整读给的是 %q，单读令牌给的是 %q", string(stored.Revision), string(after))
	}
}

// 两份各自独立的介质各自从头数起。不拌进实例标识，同一个会话号在两边就会
// 比出相等的令牌。
func Test两份介质对同一个会话给不出同一个令牌(t *testing.T) {
	first, meta := seededBackend(t, "twins", 0)
	second := newBackend(t)
	if err := second.AppendBatch(t.Context(), meta, oneTurnLog(t, 0), false); err != nil {
		t.Fatalf("往第二份介质写失败：%v", err)
	}

	left, err := first.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读第一份介质的令牌失败：%v", err)
	}
	right, err := second.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读第二份介质的令牌失败：%v", err)
	}
	if left == right {
		t.Fatalf("两份介质上同一个会话的令牌比出了相等：%q", string(left))
	}
}

// 实例标识和布局同生共死：它一旦被别人读到过就不许再变，否则那些手里攥着旧令牌的
// 调用方会以为日志变过了。
func Test重开同一份介质令牌不变(t *testing.T) {
	dsn, namespace := freshMedium(t)

	first, err := openBackend(t, dsn, namespace)
	if err != nil {
		t.Fatalf("开后端失败：%v", err)
	}
	meta := testMeta("reopened")
	if err := first.AppendBatch(t.Context(), meta, oneTurnLog(t, 0), false); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	before, err := first.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("关后端失败：%v", err)
	}

	second, err := openBackend(t, dsn, namespace)
	if err != nil {
		t.Fatalf("重开后端失败：%v", err)
	}
	after, err := second.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("重开之后读令牌失败：%v", err)
	}
	if after != before {
		t.Fatalf("重开之后同一份没变过的日志给出了另一个令牌：%q → %q", string(before), string(after))
	}
}

// 条目上的 seq 和负载里那一份对不上，说明有人绕过本包直接改过介质——那时候按哪
// 一份都是猜，只能拒。
func Test条目上的seq和负载里那份对不上是坏存档(t *testing.T) {
	backend := newBackend(t)
	meta := testMeta("disagree")

	head, err := encodeHeader(meta)
	if err != nil {
		t.Fatalf("排头失败：%v", err)
	}
	writeRaw(t, backend, meta.ID, head, []datastore.Entry{
		{Seq: 99, Payload: marshalData(t, "事件", userMessageEvent(t, 5, "hi"))},
	})

	var corruption *persistence.CorruptionError
	if _, err := backend.LoadStored(t.Context(), meta.ID); !errors.As(err, &corruption) {
		t.Fatalf("该报 CorruptionError，实际 %v", err)
	}
}

func Test负载不是一条事件是坏存档(t *testing.T) {
	backend := newBackend(t)
	meta := testMeta("rotten")

	head, err := encodeHeader(meta)
	if err != nil {
		t.Fatalf("排头失败：%v", err)
	}
	writeRaw(t, backend, meta.ID, head, []datastore.Entry{
		{Seq: 0, Payload: json.RawMessage(`{这不是 JSON`)},
	})

	var corruption *persistence.CorruptionError
	if _, err := backend.LoadStored(t.Context(), meta.ID); !errors.As(err, &corruption) {
		t.Fatalf("该报 CorruptionError，实际 %v", err)
	}
}

func Test头解不回来是坏存档(t *testing.T) {
	backend := newBackend(t)

	writeRaw(t, backend, "bad-head", json.RawMessage(`{这不是 JSON`), nil)

	var corruption *persistence.CorruptionError
	if _, err := backend.LoadStored(t.Context(), "bad-head"); !errors.As(err, &corruption) {
		t.Fatalf("该报 CorruptionError，实际 %v", err)
	}
}

// 身份在流名上和头里各存了一份，两边都有的时候必须一致：不一致说明有人绕过本包
// 直接改过介质。
func Test头里的身份和流名对不上是坏存档(t *testing.T) {
	backend := newBackend(t)

	head, err := encodeHeader(testMeta("另一个人"))
	if err != nil {
		t.Fatalf("排头失败：%v", err)
	}
	writeRaw(t, backend, "mismatch", head, nil)

	var corruption *persistence.CorruptionError
	if _, err := backend.LoadStored(t.Context(), "mismatch"); !errors.As(err, &corruption) {
		t.Fatalf("该报 CorruptionError，实际 %v", err)
	}
	// 列举也解头，所以它也得认得出来：一条解不回来的头不许混进列举结果里。
	if _, err := backend.ListSnapshots(t.Context()); !errors.As(err, &corruption) {
		t.Fatalf("列举该报 CorruptionError，实际 %v", err)
	}
}

// 一次写在提交回执丢掉之后由编排层重试，是数据库上的常态——重试那一下手里的
// materialized 位还是假的，所以重复落地必须是幂等的。
func Test重复落地是幂等的(t *testing.T) {
	backend, meta := seededBackend(t, "retried", 0)

	if err := backend.AppendBatch(t.Context(), meta, oneTurnLog(t, 6), false); err != nil {
		t.Fatalf("重复落地该是幂等的，实际 %v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	if got := seqsOf(stored.Events); !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
}

// 幂等只管「同一份头写两遍」。同一个 id 底下换一份头是撞号，不是重试。
func Test同一个身份底下换一份头会被拒(t *testing.T) {
	backend, meta := seededBackend(t, "collide", 0)

	other := meta
	other.CreatedAt = meta.CreatedAt + 1
	err := backend.AppendBatch(t.Context(), other, oneTurnLog(t, 6), false)
	if err == nil || !strings.Contains(err.Error(), "撞号") {
		t.Fatalf("该拒掉撞号，实际 %v", err)
	}
}

// 同一个会话里同一个 seq 写两遍，说明有两个写者在同一份日志上各写各的——
// 那不是这里能悄悄合上的事。
func Test同一个seq写两遍会响(t *testing.T) {
	backend, meta := seededBackend(t, "double", 0)

	if err := backend.AppendBatch(t.Context(), meta, oneTurnLog(t, 0), true); err == nil {
		t.Fatal("同一个 seq 写两遍该响")
	}
}

// 编排层以为这个会话已经落地而其实没有——报出来，不要让一批事件悄悄写进一个
// 不存在的会话。空批正好走得到这一条：它没有行可插，外键拦不住。
func Test往一个没落地的会话上追加报会话不存在(t *testing.T) {
	backend := newBackend(t)

	err := backend.AppendBatch(t.Context(), testMeta("ghost"), nil, true)
	if !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Fatalf("该报 ErrSessionNotFound，实际 %v", err)
	}
}

// 一次空批算出来的下一条是 0，直接盖会把已经推进过的起点抹回去——于是一份
// 全被弹空的存档会说自己从 0 起，而下一条其实该写在 6。
func Test空批不会把下一条要写的seq抹回去(t *testing.T) {
	backend, meta := seededBackend(t, "rewind", 0)

	if err := backend.AppendBatch(t.Context(), meta, nil, true); err != nil {
		t.Fatalf("空批不该失败：%v", err)
	}
	evictHead(t, backend, meta.ID, 6)

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if stored.BaseSeq != 6 {
		t.Fatalf("起点被抹回 %d 了，要的是 6", stored.BaseSeq)
	}
}

// 负载那一列是 TEXT 不是 jsonb：一个 NUL 码位在 JSON 里编成一个转义序列，而 jsonb
// 当场拒收它——模型输出里出现一个就够了。
func Test带NUL的事件存得下也读得回(t *testing.T) {
	backend := newBackend(t)
	meta := testMeta("nul")
	event := userMessageEvent(t, 0, "前\x00后")

	if err := backend.AppendBatch(t.Context(), meta, []sessionlog.Event{event}, false); err != nil {
		t.Fatalf("写带 NUL 的事件失败：%v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if len(stored.Events) != 1 {
		t.Fatalf("读回来 %d 条，写进去的是 1 条", len(stored.Events))
	}
	want := marshalData(t, "写进去那条", event)
	got := marshalData(t, "读回来那条", stored.Events[0])
	if !bytes.Equal(got, want) {
		t.Fatalf("读回来的事件变了\n读到：%s\n写的：%s", got, want)
	}
	// 这条用例压的就是那个码位，它没进到负载里的话上面那一比就什么也没验。
	if !strings.Contains(string(want), `0000`) {
		t.Fatalf("负载里没有那个转义序列，这条用例白跑了：%s", want)
	}
}

// 装载时补收尾走的是这条路：不截断（这个后端没有断尾可截），只把缺的那几条补上。
func TestCommitRepair把缺的收尾补上(t *testing.T) {
	backend := newBackend(t)
	meta := testMeta("repaired")

	// 一个开着没关的回合：turn/start 之后就断了。
	if err := backend.AppendBatch(t.Context(), meta, []sessionlog.Event{
		turnStartEvent(t, 0, 1),
		userMessageEvent(t, 1, "hi"),
	}, false); err != nil {
		t.Fatalf("写失败：%v", err)
	}

	if err := backend.CommitRepair(t.Context(), meta, nil, []sessionlog.Event{
		turnEndEvent(t, 2, 1),
	}); err != nil {
		t.Fatalf("补收尾失败：%v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("补完之后的 seq 是 %v，要的是 %v", got, want)
	}
}

// 弹出丢的是连续的一头，起点跟着走。
func Test弹出丢掉最老那一段(t *testing.T) {
	backend, meta := seededBackend(t, "trimmed", 0)

	if err := backend.TrimBefore(t.Context(), meta.ID, 3); err != nil {
		t.Fatalf("弹出失败：%v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("剩下的 seq 是 %v，要的是 %v", got, want)
	}
	if stored.BaseSeq != 3 {
		t.Errorf("起点是 %d，要的是 3", stored.BaseSeq)
	}
}

// 那一段已经不在了就什么也不做——一次写在回执丢掉之后重来是常态，重复弹出
// 不许因此变成一条错。
func Test重复弹出是幂等的(t *testing.T) {
	backend, meta := seededBackend(t, "trim-twice", 0)

	for range 2 {
		if err := backend.TrimBefore(t.Context(), meta.ID, 3); err != nil {
			t.Fatalf("弹出失败：%v", err)
		}
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("剩下的 seq 是 %v，要的是 %v", got, want)
	}
}

// 水位越过末尾把存档整个清空，这不是错：起点那时候由「下一条要写的 seq」回答。
func Test弹出可以把存档清空(t *testing.T) {
	backend, meta := seededBackend(t, "trim-all", 0)

	if err := backend.TrimBefore(t.Context(), meta.ID, 99); err != nil {
		t.Fatalf("清空失败：%v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if len(stored.Events) != 0 {
		t.Fatalf("清空之后还剩 %d 条", len(stored.Events))
	}
	if stored.BaseSeq != 6 {
		t.Errorf("起点是 %d，要的是 6——那是下一条要写的 seq", stored.BaseSeq)
	}
}

// 一个不存在的会话上那一句弹出影响零行，和「那一段早就弹掉了」长得一模一样，
// 所以身份必须单独查一遍，否则编排层永远等不到这条。
func Test弹一个不存在的会话报会话不存在(t *testing.T) {
	backend := newBackend(t)

	if err := backend.TrimBefore(t.Context(), "nobody", 1); !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Fatalf("该报 ErrSessionNotFound，实际 %v", err)
	}
}

// 令牌标识的是「日志里有哪些事件」，而弹出丢掉的那一段是读的一侧靠起点自己
// 认得出来的。让它动，所有攥着令牌的观察者会在一次纯粹的收缩之后集体重读。
func Test弹出不动令牌(t *testing.T) {
	backend, meta := seededBackend(t, "trim-revision", 0)

	before, err := backend.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if err := backend.TrimBefore(t.Context(), meta.ID, 3); err != nil {
		t.Fatalf("弹出失败：%v", err)
	}
	after, err := backend.ReadStoredRevision(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if after != before {
		t.Errorf("弹出动了令牌：%q → %q", string(before), string(after))
	}
}

// 弹空之后接着写：下一条要接在 6 上，不是回到 0。
func Test清空之后接着写(t *testing.T) {
	backend, meta := seededBackend(t, "trim-then-write", 0)

	if err := backend.TrimBefore(t.Context(), meta.ID, 99); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	if err := backend.AppendBatch(t.Context(), meta, oneTurnLog(t, 6), true); err != nil {
		t.Fatalf("清空之后再写失败：%v", err)
	}

	stored, err := backend.LoadStored(t.Context(), meta.ID)
	if err != nil {
		t.Fatalf("读存档失败：%v", err)
	}
	if got, want := seqsOf(stored.Events), []int{6, 7, 8, 9, 10, 11}; !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
	if stored.BaseSeq != 6 {
		t.Errorf("起点是 %d，要的是 6", stored.BaseSeq)
	}
}

// 排序按建立时间，不是按流名——那是本包这一侧加的，下面那一层不认识头里有什么。
func TestList和ListSnapshots看得见每一个已落地的会话(t *testing.T) {
	backend := newBackend(t)

	for _, id := range []sessionlog.SessionID{"alpha", "beta"} {
		if err := backend.AppendBatch(t.Context(), testMeta(id), oneTurnLog(t, 0), false); err != nil {
			t.Fatalf("落地 %q 失败：%v", string(id), err)
		}
	}

	headers, err := backend.List(t.Context())
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if got := idsOf(headers); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("列举出来的是 %v，要的是 [alpha beta]", got)
	}

	snapshots, err := backend.ListSnapshots(t.Context())
	if err != nil {
		t.Fatalf("列举快照失败：%v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("快照有 %d 份，要的是 2 份", len(snapshots))
	}
	for _, snapshot := range snapshots {
		// 令牌是「变没变」那个回合的全部依据，空的话那个回合走不通。
		if snapshot.Revision == "" {
			t.Errorf("%q 的变更令牌是空的", string(snapshot.Header.ID))
		}
	}
}
