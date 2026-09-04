// 本文件的作用：压日志集——写读一个来回、起点跟着弹出走、令牌什么时候动什么时候
// 不动、同一个身份底下换一份头，以及一批长过一块时怎么分。

package datastore

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// seeded 开一个日志集，并在里面落一条从 base 起、count 条的流。
func seeded(t *testing.T, unitName, stream string, base, count int64) *LogUnit {
	t.Helper()

	unit := newLog(t, unitName)
	if err := unit.Append(t.Context(), AppendRequest{
		Stream: stream, Head: []byte(`{"v":1}`), EnsureStream: true, Entries: entriesFrom(base, count),
	}); err != nil {
		t.Fatalf("写第一批失败：%v", err)
	}
	return unit
}

func Test第一批把流建出来并且整批读得回(t *testing.T) {
	unit := seeded(t, "whole", "s", 0, 6)

	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if got := string(segment.Head); got != `{"v":1}` {
		t.Errorf("读回来的头是 %s", got)
	}
	if got, want := seqsOf(segment.Entries), []int64{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
	if segment.BaseSeq != 0 {
		t.Errorf("起点是 %d，要的是 0", segment.BaseSeq)
	}
	if segment.NextSeq != 6 {
		t.Errorf("下一条要写的是 %d，要的是 6", segment.NextSeq)
	}
	if segment.Revision == "" {
		t.Error("一条存在的流拿到了空令牌")
	}
}

func Test流不在时每条读路都报流不存在(t *testing.T) {
	unit := newLog(t, "missing")

	if _, err := unit.Load(t.Context(), "nobody", 0); !errors.Is(err, ErrStreamNotFound) {
		t.Errorf("读该报 ErrStreamNotFound，实际 %v", err)
	}
	if _, err := unit.ReadRevision(t.Context(), "nobody"); !errors.Is(err, ErrStreamNotFound) {
		t.Errorf("读令牌该报 ErrStreamNotFound，实际 %v", err)
	}
	// 一个不存在的流上那句 DELETE 影响零行，和「那一段早就弹掉了」长得一模一样，
	// 所以身份必须单独查一遍。
	if err := unit.TrimBefore(t.Context(), "nobody", 1); !errors.Is(err, ErrStreamNotFound) {
		t.Errorf("弹出该报 ErrStreamNotFound，实际 %v", err)
	}
}

// 起点是个变量：这份日志会从最老的一头弹出，所以「从哪个 seq 起」问的是现存最早
// 那条，不是 0。
func Test起点跟着现存最早那条走(t *testing.T) {
	unit := seeded(t, "trimmed", "s", 0, 6)

	if err := unit.TrimBefore(t.Context(), "s", 3); err != nil {
		t.Fatalf("弹出失败：%v", err)
	}
	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if got, want := seqsOf(segment.Entries), []int64{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("剩下的 seq 是 %v，要的是 %v", got, want)
	}
	if segment.BaseSeq != 3 {
		t.Errorf("起点是 %d，要的是 3", segment.BaseSeq)
	}

	// 那一段已经不在了就什么也不做——一次写在回执丢掉之后重来是常态。
	if err := unit.TrimBefore(t.Context(), "s", 3); err != nil {
		t.Fatalf("重复弹出该是幂等的：%v", err)
	}
}

// 一条被弹空的流推不出任何东西，而恰恰是那时候调用方要靠起点决定下一条写在哪儿。
func Test弹空之后起点由下一条要写的seq回答(t *testing.T) {
	unit := seeded(t, "emptied", "s", 0, 6)

	if err := unit.TrimBefore(t.Context(), "s", 99); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if len(segment.Entries) != 0 {
		t.Fatalf("清空之后还剩 %d 条", len(segment.Entries))
	}
	if segment.BaseSeq != 6 {
		t.Errorf("起点是 %d，要的是 6——那是下一条要写的 seq", segment.BaseSeq)
	}

	// 弹空之后接着写：下一条接在 6 上，不是回到 0。
	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{"v":1}`), EnsureStream: true, Entries: entriesFrom(6, 2),
	}); err != nil {
		t.Fatalf("清空之后再写失败：%v", err)
	}
	segment, err = unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if got, want := seqsOf(segment.Entries), []int64{6, 7}; !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v", got, want)
	}
}

// 一次空批算出来的下一条是 0，直接盖会把已经推进过的起点抹回去。
func Test空批不会把下一条要写的seq抹回去(t *testing.T) {
	unit := seeded(t, "rewind", "s", 0, 6)

	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{"v":1}`), EnsureStream: true,
	}); err != nil {
		t.Fatalf("空批不该失败：%v", err)
	}
	if err := unit.TrimBefore(t.Context(), "s", 6); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if segment.BaseSeq != 6 {
		t.Fatalf("起点被抹回 %d 了，要的是 6", segment.BaseSeq)
	}
}

// 读的一方要靠整条流的起点分清「要的那一段早就被弹掉了」和「那一段压根没写过」。
func TestLoad带水位读的是后缀但交的是整条流的起点(t *testing.T) {
	unit := seeded(t, "suffix", "s", 0, 6)

	if err := unit.TrimBefore(t.Context(), "s", 2); err != nil {
		t.Fatalf("弹出失败：%v", err)
	}
	segment, err := unit.Load(t.Context(), "s", 4)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if got, want := seqsOf(segment.Entries), []int64{4, 5}; !slices.Equal(got, want) {
		t.Fatalf("后缀的 seq 是 %v，要的是 %v", got, want)
	}
	if segment.BaseSeq != 2 {
		t.Errorf("起点是 %d，要的是 2", segment.BaseSeq)
	}

	// 水位越过末尾：空后缀是正常答案，不是错。
	beyond, err := unit.Load(t.Context(), "s", 99)
	if err != nil {
		t.Fatalf("水位越过末尾该给空后缀，实际报错：%v", err)
	}
	if len(beyond.Entries) != 0 {
		t.Errorf("越过末尾还读出了 %d 条", len(beyond.Entries))
	}

	// 负的水位在碰库之前就该被拦住。
	if _, err := unit.Load(t.Context(), "s", -1); !errors.Is(err, ErrMalformedName) {
		t.Errorf("负水位该报 ErrMalformedName，实际 %v", err)
	}
	if err := unit.TrimBefore(t.Context(), "s", -1); !errors.Is(err, ErrMalformedName) {
		t.Errorf("负水位该报 ErrMalformedName，实际 %v", err)
	}
}

// 令牌不动是「变没变」那个回合的全部依据：不动就该纹丝不动，一动就必须真的动。
func Test令牌写的时候动读的时候不动(t *testing.T) {
	unit := seeded(t, "moving", "s", 0, 6)

	before, err := unit.ReadRevision(t.Context(), "s")
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	again, err := unit.ReadRevision(t.Context(), "s")
	if err != nil {
		t.Fatalf("再读令牌失败：%v", err)
	}
	if again != before {
		t.Fatalf("什么都没写，令牌却从 %q 变成了 %q", before, again)
	}

	if err := unit.Append(t.Context(), AppendRequest{Stream: "s", Entries: entriesFrom(6, 2)}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	after, err := unit.ReadRevision(t.Context(), "s")
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if after == before {
		t.Fatalf("追加之后令牌还是 %q，没动", before)
	}
	// 两条读路交出来的必须是同一套表示，否则「读一遍、再核对一遍」永远说变过了。
	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if segment.Revision != after {
		t.Errorf("整读给的是 %q，单读令牌给的是 %q", segment.Revision, after)
	}
}

// 弹出是一次纯粹的收缩，读的一侧靠起点自己认得出来。让它动，所有攥着令牌的
// 观察者会在一次它们本来不必关心的收缩之后集体重读。
func Test弹出不动令牌(t *testing.T) {
	unit := seeded(t, "trim_revision", "s", 0, 6)

	before, err := unit.ReadRevision(t.Context(), "s")
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if err := unit.TrimBefore(t.Context(), "s", 3); err != nil {
		t.Fatalf("弹出失败：%v", err)
	}
	after, err := unit.ReadRevision(t.Context(), "s")
	if err != nil {
		t.Fatalf("读令牌失败：%v", err)
	}
	if after != before {
		t.Errorf("弹出动了令牌：%q → %q", before, after)
	}
}

// 令牌拌进实例标识和单元名两段：前者管两份介质之间不撞，后者管同一份介质里两个
// 单元底下同名的流不撞——它们是两条毫无关系的流，各自从 0 数起。
func Test令牌是来源限定的(t *testing.T) {
	medium := newMedium(t)

	left, err := medium.OpenLog(t.Context(), LogSpec{Name: "left", Version: 1})
	if err != nil {
		t.Fatalf("开左边失败：%v", err)
	}
	right, err := medium.OpenLog(t.Context(), LogSpec{Name: "right", Version: 1})
	if err != nil {
		t.Fatalf("开右边失败：%v", err)
	}

	if left.revisionOf(1) == right.revisionOf(1) {
		t.Error("同一份介质里两个单元的第一个令牌比出了相等")
	}
	if left.revisionOf(1) == left.revisionOf(2) {
		t.Error("同一个单元上计数不同，令牌就该不同")
	}
	if left.revisionOf(7) != left.revisionOf(7) {
		t.Error("同一条没变过的流观察多少次都该是同一个令牌")
	}
	// 空串在上一层的含义是「这条流不存在」，一条存在的流不许拿到它。
	if left.revisionOf(0) == "" {
		t.Error("计数为零的流拿到了空令牌")
	}

	other := newMedium(t)
	otherLeft, err := other.OpenLog(t.Context(), LogSpec{Name: "left", Version: 1})
	if err != nil {
		t.Fatalf("在第二份介质上开失败：%v", err)
	}
	if left.revisionOf(1) == otherLeft.revisionOf(1) {
		t.Error("两份介质上同名单元的令牌比出了相等")
	}
}

// 冲突时不报错而是回头核对，因为这一步会**重来**：一次写在提交回执丢掉之后由
// 调用方重试是数据库上的常态，而它手里那个「建过了没有」的位还是假的。
func Test重复建流是幂等的但换一份头会被拒(t *testing.T) {
	unit := seeded(t, "heads", "s", 0, 6)

	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{"v":1}`), EnsureStream: true, Entries: entriesFrom(6, 2),
	}); err != nil {
		t.Fatalf("同一份头重复建流该是幂等的：%v", err)
	}
	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{"v":2}`), EnsureStream: true, Entries: entriesFrom(8, 2),
	}); !errors.Is(err, ErrHeadConflict) {
		t.Fatalf("换一份头该报 ErrHeadConflict，实际 %v", err)
	}

	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if got, want := seqsOf(segment.Entries), []int64{0, 1, 2, 3, 4, 5, 6, 7}; !slices.Equal(got, want) {
		t.Fatalf("读回来的 seq 是 %v，要的是 %v——被拒的那一批不该留下痕迹", got, want)
	}
}

// 同一条流上同一个 seq 写两遍，说明有两个写者在同一份日志上各写各的——那不是
// 这里能悄悄合上的事。
func Test同一个seq写两遍会响(t *testing.T) {
	unit := seeded(t, "double", "s", 0, 6)

	if err := unit.Append(t.Context(), AppendRequest{Stream: "s", Entries: entriesFrom(0, 6)}); err == nil {
		t.Fatal("同一个 seq 写两遍该响")
	}
}

// 调用方以为这条流已经建出来了而其实没有——报出来，不要让一批条目悄悄写进一条
// 不存在的流。空批正好走得到这一条：它没有行可插，外键拦不住。
func Test往一条没建出来的流上写报流不存在(t *testing.T) {
	unit := newLog(t, "ghost")

	if err := unit.Append(t.Context(), AppendRequest{Stream: "s"}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("该报 ErrStreamNotFound，实际 %v", err)
	}
}

// 一条多值 INSERT 最多带 insertChunkRows 行，所以更长的一批要分成几条语句发。
// 分块出错的样子是「中间少了一段」，那要到读的时候才看得见。
func Test一批长过一块时整批写下去(t *testing.T) {
	unit := newLog(t, "chunked")
	count := int64(insertChunkRows*2 + 1)

	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{}`), EnsureStream: true, Entries: entriesFrom(0, count),
	}); err != nil {
		t.Fatalf("写一批 %d 条失败：%v", count, err)
	}

	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if int64(len(segment.Entries)) != count {
		t.Fatalf("读回来 %d 条，写进去的是 %d 条", len(segment.Entries), count)
	}
	if last := segment.Entries[count-1].Seq; last != count-1 {
		t.Errorf("最后一条的 seq 是 %d，要的是 %d", last, count-1)
	}
}

// 负载那一列是 TEXT 不是 jsonb：一个 NUL 码位在 JSON 里编成一个转义序列，而 jsonb
// 当场拒收它——模型输出里出现一个就够了。
func Test带NUL的负载存得下也读得回(t *testing.T) {
	unit := newLog(t, "nul")

	payload, err := json.Marshal(map[string]string{"text": "前\x00后"})
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{}`), EnsureStream: true,
		Entries: []Entry{{Seq: 0, Payload: payload}},
	}); err != nil {
		t.Fatalf("写带 NUL 的负载失败：%v", err)
	}

	segment, err := unit.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if len(segment.Entries) != 1 {
		t.Fatalf("读回来 %d 条", len(segment.Entries))
	}
	var decoded map[string]string
	if err := json.Unmarshal(segment.Entries[0].Payload, &decoded); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if decoded["text"] != "前\x00后" {
		t.Fatalf("读回来的负载变了：%q", decoded["text"])
	}
}

// 排序按流名，因为本包不认识头里有什么，排不出别的顺序。
func TestList按流名升序列出所有流(t *testing.T) {
	unit := newLog(t, "listing")

	for _, name := range []string{"gamma", "alpha", "beta"} {
		if err := unit.Append(t.Context(), AppendRequest{
			Stream: name, Head: []byte(`{}`), EnsureStream: true, Entries: entriesFrom(0, 2),
		}); err != nil {
			t.Fatalf("建流 %q 失败：%v", name, err)
		}
	}

	streams, err := unit.List(t.Context())
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	names := make([]string, 0, len(streams))
	for _, stream := range streams {
		names = append(names, stream.Name)
		if stream.Revision == "" {
			t.Errorf("%q 的令牌是空的", stream.Name)
		}
		if stream.NextSeq != 2 {
			t.Errorf("%q 的下一条是 %d，要的是 2", stream.Name, stream.NextSeq)
		}
	}
	if !slices.Equal(names, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("列举出来的是 %v，要的是 [alpha beta gamma]", names)
	}
}

func Test关掉的日志集每一条路都响(t *testing.T) {
	unit := seeded(t, "closing", "s", 0, 2)

	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("关单元失败：%v", err)
	}
	// 幂等：重复关是空操作。
	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("重复关该是空操作：%v", err)
	}

	if _, err := unit.Load(t.Context(), "s", 0); !errors.Is(err, ErrClosed) {
		t.Errorf("读该报 ErrClosed，实际 %v", err)
	}
	if _, err := unit.List(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("列举该报 ErrClosed，实际 %v", err)
	}
	if _, err := unit.ReadRevision(t.Context(), "s"); !errors.Is(err, ErrClosed) {
		t.Errorf("读令牌该报 ErrClosed，实际 %v", err)
	}
	if err := unit.Append(t.Context(), AppendRequest{Stream: "s"}); !errors.Is(err, ErrClosed) {
		t.Errorf("写该报 ErrClosed，实际 %v", err)
	}
	if err := unit.TrimBefore(t.Context(), "s", 1); !errors.Is(err, ErrClosed) {
		t.Errorf("弹出该报 ErrClosed，实际 %v", err)
	}
}
