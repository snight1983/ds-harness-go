// 本文件验写和编辑，重点全在**服务端条件头**上。
//
// 这几条用例的价值不在「内容写对了没有」——那件事任何一个假实现都能满足。
// 它们要证明的是：`If-None-Match: *` 和 `If-Match: <etag>` 真的发出去了，
// 服务端的 412 真的被认出来并且翻成了两个**不同**的码。所以它们跑在
// [fakeS3] 上，那台服务端会照着这两个头拒绝请求。

package objectstore

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/fs"
)

// unknownIntent 是一个本包造不出来、但嵌入能造出来的第三种写意图。
//
// [fs.WriteIntent] 靠一个未导出方法封印，本包**实现**不了它；但把接口嵌进来
// 就能拿到那个方法的提升版本，于是可以造出一个 fs 包没有定义过的成员。
// 这正好模拟了「fs 包以后加了第三支，而这个后端还没跟上」那一刻。
type unknownIntent struct{ fs.WriteIntent }

// shownBaseline 把一个可能缺席的基准写成能读的诊断。
//
// 直接 %v 一个 *string 打出来的是指针地址，那条诊断等于没写。
func shownBaseline(baseline *string) string {
	if baseline == nil {
		return "nil"
	}
	return strconv.Quote(*baseline)
}

// TestWriteWithoutAGuardCreatesThenReplaces 验不带守卫的那条路。
func TestWriteWithoutAGuardCreatesThenReplaces(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	created, err := store.WriteText(t.Context(), where, "one", nil)
	if err != nil {
		t.Fatalf("创建不该失败：%v", err)
	}
	if created.Operation != fs.OperationCreate {
		t.Fatalf("该报创建，实际 %v", created.Operation)
	}
	if created.Before != nil {
		t.Fatalf("此前不存在，基准该是 nil，实际 %q", *created.Before)
	}
	if created.After != "one" {
		t.Fatalf("After 该是写进去的内容，实际 %q", created.After)
	}

	replaced, err := store.WriteText(t.Context(), where, "two", nil)
	if err != nil {
		t.Fatalf("替换不该失败：%v", err)
	}
	if replaced.Operation != fs.OperationUpdate {
		t.Fatalf("该报更新，实际 %v", replaced.Operation)
	}
	if replaced.Before == nil || *replaced.Before != "one" {
		t.Fatalf("基准该是旧内容，实际 %s", shownBaseline(replaced.Before))
	}
	if replaced.Version == created.Version {
		t.Fatal("改了内容，版本就该变")
	}
}

// TestWriteNormalizesLineEndingsOnTheWayIn 验写进去的就是 LF。
//
// 进出两侧都规范化，才能让「写进去什么、读出来什么」是个往返。
// 这一条验的是进那一侧：库里存的字节本身就已经没有 CR 了。
func TestWriteNormalizesLineEndingsOnTheWayIn(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	outcome, err := store.WriteText(t.Context(), where, "a\r\nb\r\n", nil)
	if err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if outcome.After != "a\nb\n" {
		t.Fatalf("After 该是规范化之后的文本，实际 %q", outcome.After)
	}

	raw, err := store.ReadBytes(t.Context(), where, 1024)
	if err != nil {
		t.Fatalf("读字节不该失败：%v", err)
	}
	if string(raw) != "a\nb\n" {
		t.Fatalf("库里存的就该是 LF，实际 %q", raw)
	}
}

// TestCreateIfAbsentIsEnforcedByTheServer 验创建守卫走的是条件头。
//
// 第二次写被拒，而且拒它的是服务端：先探测再写的话，两个都以为自己在创建的写
// 会有一个覆盖掉另一个，而两次都报成功。
func TestCreateIfAbsentIsEnforcedByTheServer(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	outcome, err := store.WriteText(t.Context(), where, "one", fs.CreateIfAbsent{})
	if err != nil {
		t.Fatalf("第一次创建不该失败：%v", err)
	}
	if outcome.Operation != fs.OperationCreate {
		t.Fatalf("该报创建，实际 %v", outcome.Operation)
	}
	// 创建没有基准可言：要么创建了一个此前不存在的文件，要么根本没写成。
	if outcome.Before != nil {
		t.Fatalf("创建的基准该是 nil，实际 %q", *outcome.Before)
	}

	_, err = store.WriteText(t.Context(), where, "two", fs.CreateIfAbsent{})
	requireCode(t, err, fs.CodeNotObserved)

	text, err := store.ReadText(t.Context(), where)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "one" {
		t.Fatalf("被拒的那次写不该改到内容，实际 %q", text)
	}
}

// TestReplaceIfVersionAcceptsTheVersionItWasGiven 验版本对得上时能落。
func TestReplaceIfVersionAcceptsTheVersionItWasGiven(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "one")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	info, _, err := store.Stat(t.Context(), where)
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}

	outcome, err := store.WriteText(t.Context(), where, "two", fs.ReplaceIfVersion{Version: info.Version})
	if err != nil {
		t.Fatalf("版本对得上时不该失败：%v", err)
	}
	if outcome.Operation != fs.OperationUpdate {
		t.Fatalf("该报更新，实际 %v", outcome.Operation)
	}
	if outcome.Before == nil || *outcome.Before != "one" {
		t.Fatalf("基准该是旧内容，实际 %s", shownBaseline(outcome.Before))
	}
}

// TestReplaceIfVersionRefusesAStaleOrAbsentTarget 验守卫的两种失败。
//
// 目标不在也报陈旧而不是不存在：在这条路上「不在」就是「你看到的那一份已经没了」，
// 和版本对不上是同一件事。
func TestReplaceIfVersionRefusesAStaleOrAbsentTarget(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "one")
	store := fake.store(t, "w1")

	_, err := store.WriteText(t.Context(), target(t, store, "/a.txt"), "two",
		fs.ReplaceIfVersion{Version: "别的版本"})
	requireCode(t, err, fs.CodeStaleVersion)

	_, err = store.WriteText(t.Context(), target(t, store, "/nope.txt"), "two",
		fs.ReplaceIfVersion{Version: "任何版本"})
	requireCode(t, err, fs.CodeStaleVersion)
}

// TestReplaceIfVersionCatchesAWriteSlippedInBeforeThePut 验守卫真的落在服务端。
//
// 读和写之间被别人插了一手：客户端这边的版本比对**已经过了**，
// 拦住这次写的只可能是 PUT 上那个 `If-Match`。这一条是整个乐观并发方案
// 唯一能被证明的地方——去掉那个头，前面每一条用例照样过。
func TestReplaceIfVersionCatchesAWriteSlippedInBeforeThePut(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "one")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	info, _, err := store.Stat(t.Context(), where)
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}

	var once sync.Once
	fake.onPut = func(string) {
		once.Do(func() { fake.seed("w1/a.txt", "别人写的") })
	}

	_, err = store.WriteText(t.Context(), where, "two", fs.ReplaceIfVersion{Version: info.Version})
	requireCode(t, err, fs.CodeStaleVersion)

	text, err := store.ReadText(t.Context(), where)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "别人写的" {
		t.Fatalf("插进来的那次写不该被盖掉，实际 %q", text)
	}
}

// TestWriteTreatsAnEmptyFileAsARealBaseline 验空文件是一份真基准，不是「不在」。
//
// 分不开的话，一次「把内容清空再写回去」会被报成创建。
func TestWriteTreatsAnEmptyFileAsARealBaseline(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "")
	store := fake.store(t, "w1")

	outcome, err := store.WriteText(t.Context(), target(t, store, "/a.txt"), "one", nil)
	if err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if outcome.Operation != fs.OperationUpdate {
		t.Fatalf("空文件也是在的，该报更新，实际 %v", outcome.Operation)
	}
	if outcome.Before == nil || *outcome.Before != "" {
		t.Fatalf("基准该是空串而不是 nil，实际 %s", shownBaseline(outcome.Before))
	}
}

// TestWriteOverAnUnreadableBaselineStillSucceeds 验基准拿不到时写本身照样成。
//
// 旧内容是二进制、或者超过了文本上限时，这次写没有任何问题，只是结果里
// 没有 diff 基准。把它报成错误的话，一次完全正常的覆盖会因为**旧**内容
// 是二进制而失败。
func TestWriteOverAnUnreadableBaselineStillSucceeds(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seedBytes("w1/binary", []byte{0xff, 0xfe})
	fake.seed("w1/big.txt", strings.Repeat("x", 64))
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16})

	binary, err := store.WriteText(t.Context(), target(t, store, "/binary"), "text", nil)
	if err != nil {
		t.Fatalf("覆盖二进制不该失败：%v", err)
	}
	if binary.Operation != fs.OperationUpdate || binary.Before != nil {
		t.Fatalf("该是一次没有基准的更新，实际 %v / %s", binary.Operation, shownBaseline(binary.Before))
	}

	big, err := store.WriteText(t.Context(), target(t, store, "/big.txt"), "text", nil)
	if err != nil {
		t.Fatalf("覆盖超限对象不该失败：%v", err)
	}
	if big.Operation != fs.OperationUpdate || big.Before != nil {
		t.Fatalf("该是一次没有基准的更新，实际 %v / %s", big.Operation, shownBaseline(big.Before))
	}
}

// TestReplaceIfVersionStillGuardsAnOversizedTarget 验超限对象照样拿得到版本。
//
// 基准读不出来是一回事，守卫认不认版本是另一回事。混在一起的话，
// 带守卫的替换会因为一个和内容完全无关的理由失败。
func TestReplaceIfVersionStillGuardsAnOversizedTarget(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/big.txt", strings.Repeat("x", 64))
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16})
	where := target(t, store, "/big.txt")

	info, _, err := store.Stat(t.Context(), where)
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}

	outcome, err := store.WriteText(t.Context(), where, "small", fs.ReplaceIfVersion{Version: info.Version})
	if err != nil {
		t.Fatalf("版本对得上就该能写：%v", err)
	}
	if outcome.Before != nil {
		t.Fatalf("超限对象给不出基准，该是 nil，实际 %q", *outcome.Before)
	}

	_, err = store.WriteText(t.Context(), where, "small", fs.ReplaceIfVersion{Version: info.Version})
	requireCode(t, err, fs.CodeStaleVersion)
}

// TestEditReplacesExactlyOneMatch 验编辑最平常的那条路。
func TestEditReplacesExactlyOneMatch(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello world")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	outcome, err := store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "world", NewString: "there"}, nil)
	if err != nil {
		t.Fatalf("编辑不该失败：%v", err)
	}
	if outcome.Before != "hello world" || outcome.After != "hello there" {
		t.Fatalf("前后该分别是旧新内容，实际 %q / %q", outcome.Before, outcome.After)
	}

	text, err := store.ReadText(t.Context(), where)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "hello there" {
		t.Fatalf("库里该已经改了，实际 %q", text)
	}
}

// TestEditReplaceAllTouchesEveryMatch 验 ReplaceAll。
func TestEditReplaceAllTouchesEveryMatch(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "a a a")
	store := fake.store(t, "w1")

	outcome, err := store.EditText(t.Context(), target(t, store, "/a.txt"),
		fs.EditRequest{OldString: "a", NewString: "b", ReplaceAll: true}, nil)
	if err != nil {
		t.Fatalf("编辑不该失败：%v", err)
	}
	if outcome.After != "b b b" {
		t.Fatalf("该全改，实际 %q", outcome.After)
	}
}

// TestEditRefusesZeroOrAmbiguousMatches 验匹配数是硬判据。
//
// 「多处就改第一处」是不行的：调用方会以为改完了，而剩下的几处还是旧的。
func TestEditRefusesZeroOrAmbiguousMatches(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "a a")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	_, err := store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "zzz", NewString: "b"}, nil)
	requireCode(t, err, fs.CodeEditNotFound)

	_, err = store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "a", NewString: "b"}, nil)
	requireCode(t, err, fs.CodeAmbiguousEdit)

	// 空串在每一个位置都匹配，包括长度为零的那些位置。
	_, err = store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "", NewString: "b"}, nil)
	requireCode(t, err, fs.CodeAmbiguousEdit)

	text, err := store.ReadText(t.Context(), where)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "a a" {
		t.Fatalf("被拒的编辑不该改到内容，实际 %q", text)
	}
}

// TestEditChecksTheVersionBeforeMatching 验两个判据的先后顺序。
//
// 内容陈旧时报陈旧而不是「找不到那段字面」。反过来的话，调用方会以为是自己的
// 搜索串写错了，换个串再试一次——而它每一次都在改别人刚写下的内容。
func TestEditChecksTheVersionBeforeMatching(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello world")
	store := fake.store(t, "w1")

	_, err := store.EditText(t.Context(), target(t, store, "/a.txt"),
		fs.EditRequest{OldString: "找不到的串", NewString: "x"},
		&fs.EditIntent{Version: "早就不对的版本"})
	requireCode(t, err, fs.CodeStaleVersion)
}

// TestEditRefusesAnAbsentOrBinaryTarget 验编辑的另外两条拒绝。
func TestEditRefusesAnAbsentOrBinaryTarget(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seedBytes("w1/binary", []byte{0xff, 0xfe})
	store := fake.store(t, "w1")

	_, err := store.EditText(t.Context(), target(t, store, "/nope.txt"),
		fs.EditRequest{OldString: "a", NewString: "b"}, nil)
	requireCode(t, err, fs.CodeStaleVersion)

	_, err = store.EditText(t.Context(), target(t, store, "/binary"),
		fs.EditRequest{OldString: "a", NewString: "b"}, nil)
	requireCode(t, err, fs.CodeNotText)
}

// TestEditCatchesAWriteSlippedInBeforeThePut 验编辑的临界区等价物真的成立。
//
// 接缝要求「校验版本、匹配字面、重写」三步在同一个临界区里完成。这里没有临界区，
// 靠的是读到的那个 ETag 被原样带进 `If-Match`。这一条制造出「读完之后、写之前
// 有人改了」这个时刻，证明那次 PUT 会被拒——编辑绝不会落在一份已经不存在的内容上。
func TestEditCatchesAWriteSlippedInBeforeThePut(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello world")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	var once sync.Once
	fake.onPut = func(string) {
		once.Do(func() { fake.seed("w1/a.txt", "别人写的") })
	}

	_, err := store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "world", NewString: "there"}, nil)
	requireCode(t, err, fs.CodeStaleVersion)

	text, err := store.ReadText(t.Context(), where)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "别人写的" {
		t.Fatalf("插进来的那次写不该被盖掉，实际 %q", text)
	}
}

// TestCreateIfAbsentCatchesAWriteSlippedInBeforeThePut 验创建守卫也是服务端的。
func TestCreateIfAbsentCatchesAWriteSlippedInBeforeThePut(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")

	var once sync.Once
	fake.onPut = func(key string) {
		once.Do(func() { fake.seed(key, "别人先建的") })
	}

	_, err := store.WriteText(t.Context(), target(t, store, "/a.txt"), "我建的", fs.CreateIfAbsent{})
	requireCode(t, err, fs.CodeNotObserved)
}

// TestEditNormalizesLineEndingsBeforeMatching 验字面匹配发生在规范化之后。
//
// 接缝就是这么定的：[fs.EditRequest.OldString] 是「在行尾规范化之后」精确匹配。
// 一份带 CRLF 的对象里，调用方拿着一个带 LF 的搜索串该能匹配上。
func TestEditNormalizesLineEndingsBeforeMatching(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "one\r\ntwo\r\n")
	store := fake.store(t, "w1")

	outcome, err := store.EditText(t.Context(), target(t, store, "/a.txt"),
		fs.EditRequest{OldString: "one\ntwo", NewString: "three"}, nil)
	if err != nil {
		t.Fatalf("编辑不该失败：%v", err)
	}
	if outcome.After != "three\n" {
		t.Fatalf("该在规范化之后匹配，实际 %q", outcome.After)
	}
}

// TestVerifyCreateIfAbsentPassesAgainstAnHonestServer 验自测在认头的服务端上通过。
//
// 顺便验它把保留键清干净了：那个键不是用户数据，不该留在桶里。
func TestVerifyCreateIfAbsentPassesAgainstAnHonestServer(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "a")
	store := fake.store(t, "w1")

	if err := store.VerifyCreateIfAbsent(t.Context()); err != nil {
		t.Fatalf("认头的服务端该通过自测：%v", err)
	}
	for _, key := range fake.keys() {
		if strings.Contains(key, probeKeyName) {
			t.Fatalf("自测的保留键该被清掉，实际还留着 %q", key)
		}
	}
}

// TestVerifyCreateIfAbsentCatchesAServerThatIgnoresTheHeader 验它抓得到失败开放。
//
// 这是这个自测存在的**唯一**理由：早于 RELEASE.2024-09-13 的 MinIO 把
// `If-None-Match: *` 当不存在，静默覆盖并返回 200。客户端在响应里看不出来——
// 创建和覆盖返回的东西一模一样——于是 [fs.CreateIfAbsent] 静默退化成无条件覆盖。
func TestVerifyCreateIfAbsentCatchesAServerThatIgnoresTheHeader(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.IgnoreConditionals = true
	store := fake.store(t, "w1")

	err := store.VerifyCreateIfAbsent(t.Context())
	if err == nil {
		t.Fatal("不认头的服务端该被抓出来")
	}
	requireCode(t, err, fs.CodeIOError)
	if !strings.Contains(err.Error(), "RELEASE.2024-09-13") {
		t.Fatalf("诊断该说清楚要多新的服务端，实际 %v", err)
	}
}

// TestWriteRejectsAnUnknownIntent 验那条走不到的分支确实会响。
//
// [fs.WriteIntent] 是封印接口，本包外面造不出第三种实现，所以正常情况下
// 这一支到不了。留着它、并且用一个包内的假实现验一次，是为了 fs 包**以后**
// 加了第三支时，这里是一次编译期就该处理、运行期一定会响的地方，
// 而不是静默当成无条件写。
func TestWriteRejectsAnUnknownIntent(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")

	_, err := store.WriteText(t.Context(), target(t, store, "/a.txt"), "x", unknownIntent{})
	requireCode(t, err, fs.CodeIOError)

	if len(fake.keys()) != 0 {
		t.Fatalf("认不出的意图不该写下任何东西，实际 %v", fake.keys())
	}
}
