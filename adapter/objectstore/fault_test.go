// 本文件验服务端出事时每条路各自报什么码。
//
// 这几条用例存在的理由只有一个：错误翻译层的价值在于**每条路都翻得一样**。
// 单元测 translate 只能证明那张表是对的，证明不了每个调用点都走了它——
// 少接一个 err、或者在某处自己造一条 fs.Error，单元测都发现不了。
// 所以这里从 [Store] 的公开方法打进去，让失败真的从 minio-go 传回来。

package objectstore

import (
	"net/http"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/fs"
)

// denyAll 让这台服务端对每个请求都答 403。
func denyAll(fake *fakeS3) {
	fake.intercept = func(string, string) (int, string, bool) {
		return http.StatusForbidden, "AccessDenied", true
	}
}

// denyMethod 只让某一个 HTTP 方法失败，别的照常。
//
// 写入那几条路要先读一次基准、再 PUT，只有把两者分开才验得到 PUT 上那一支。
func denyMethod(fake *fakeS3, method string) {
	fake.intercept = func(gotMethod string, _ string) (int, string, bool) {
		if gotMethod != method {
			return 0, "", false
		}
		return http.StatusForbidden, "AccessDenied", true
	}
}

// TestEveryReadPathReportsPermissionDenied 验读那几条路都把 403 翻成同一个码。
func TestEveryReadPathReportsPermissionDenied(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")
	dir := target(t, store, "/")
	denyAll(fake)

	_, _, err := store.Stat(t.Context(), where)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.ReadText(t.Context(), where)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.ReadBytes(t.Context(), where, 1024)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.ListDir(t.Context(), dir)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.StreamText(t.Context(), where)
	requireCode(t, err, fs.CodePermissionDenied)

	_, _, err = store.Lstat(t.Context(), "/a.txt", "")
	requireCode(t, err, fs.CodePermissionDenied)
}

// TestListingChildrenReportsPermissionDenied 验推断目录时那次列举也翻对。
//
// 这一支单独验，是因为它藏在 [Store.Stat] 的第二步里：对象查不到之后
// 才会去列子项。让 HEAD 报不在、让列举报 403，才走得到那里。
//
// 顺带验 [Store.ListDir] 会把这次失败原样交出去。它对**根**做不到这件事——
// 根不问服务端就知道自己是目录，那条路上的 Stat 永远不会失败。
func TestListingChildrenReportsPermissionDenied(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	fake.intercept = func(method string, _ string) (int, string, bool) {
		if method != http.MethodGet {
			return 0, "", false
		}
		return http.StatusForbidden, "AccessDenied", true
	}

	_, _, err := store.Stat(t.Context(), target(t, store, "/dir"))
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.ListDir(t.Context(), target(t, store, "/dir"))
	requireCode(t, err, fs.CodePermissionDenied)
}

// TestEveryWritePathReportsPermissionDeniedFromThePut 验三条写路上的 PUT 失败。
//
// 只拦 PUT：读基准那一步照常走完，于是失败一定来自 PUT 本身，
// 而不是被前面某一步提前挡掉了。
func TestEveryWritePathReportsPermissionDeniedFromThePut(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello world")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")

	info, _, err := store.Stat(t.Context(), where)
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}
	denyMethod(fake, http.MethodPut)

	_, err = store.WriteText(t.Context(), where, "x", nil)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.WriteText(t.Context(), target(t, store, "/new.txt"), "x", fs.CreateIfAbsent{})
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.WriteText(t.Context(), where, "x", fs.ReplaceIfVersion{Version: info.Version})
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "world", NewString: "there"}, nil)
	requireCode(t, err, fs.CodePermissionDenied)
}

// TestWritePathsReportAFailedBaselineRead 验读基准那一步失败时整次写就失败。
//
// 基准读不出来和「读基准这件事本身出错了」是两回事：前者（二进制、超限）
// 只是没有 diff 基准，后者说明这个后端此刻有问题，不该接着往里写。
func TestWritePathsReportAFailedBaselineRead(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello")
	store := fake.store(t, "w1")
	where := target(t, store, "/a.txt")
	denyMethod(fake, http.MethodGet)

	_, err := store.WriteText(t.Context(), where, "x", nil)
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.WriteText(t.Context(), where, "x", fs.ReplaceIfVersion{Version: "任何版本"})
	requireCode(t, err, fs.CodePermissionDenied)

	_, err = store.EditText(t.Context(), where,
		fs.EditRequest{OldString: "hello", NewString: "x"}, nil)
	requireCode(t, err, fs.CodePermissionDenied)
}

// TestReplacingAnOversizedTargetReportsAFailedStat 验超限那条退路上的失败。
//
// 基准超限时会退回去单独 Stat 一次拿版本；那次 Stat 也可能失败，
// 而它失败的时候这次写不该继续。
//
// 拦的是**第二次** HEAD，不是所有 HEAD：读基准那一步自己也发一次 HEAD
// （minio-go 的 Object.Stat 走的就是 StatObject），一律拦掉的话读基准就先失败了，
// 这条用例会在一个**看上去正确**的错误码上通过，而那条退路一次也没走到。
func TestReplacingAnOversizedTargetReportsAFailedStat(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/big.txt", strings.Repeat("x", 64))
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16})
	where := target(t, store, "/big.txt")

	heads := 0
	fake.intercept = func(method string, _ string) (int, string, bool) {
		if method != http.MethodHead {
			return 0, "", false
		}
		heads++
		if heads < 2 {
			return 0, "", false
		}
		return http.StatusForbidden, "AccessDenied", true
	}

	_, err := store.WriteText(t.Context(), where, "x", nil)
	requireCode(t, err, fs.CodePermissionDenied)
	if heads < 2 {
		t.Fatalf("超限之后该再 Stat 一次拿版本，实际只发了 %d 次 HEAD", heads)
	}
}

// TestStreamTextReportsAFailureRaisedAtFirstIteration 验流在第一次迭代时才发的 GET。
//
// 「在不在」是提前用 HEAD 问掉的，真正的 GET 推迟到第一次迭代——所以这次失败
// 只能从流里出来，不可能是 [Store.StreamText] 的返回值。
func TestStreamTextReportsAFailureRaisedAtFirstIteration(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello")
	store := fake.store(t, "w1")

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	denyMethod(fake, http.MethodGet)

	requireCode(t, drain(chunks), fs.CodePermissionDenied)
}

// TestStreamTextReportsATransferCutInTheMiddle 验传到一半断了报 IO 错误。
//
// 这不是 EOF：EOF 是「读完了」，而这里是「还没读完就没了」。两个混在一起的话，
// 一份**被截断**的内容会被当成一份完整的内容交出去——流式读取里最坏的一种失败，
// 因为它一声不响。
func TestStreamTextReportsATransferCutInTheMiddle(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", strings.Repeat("x", 4096))
	fake.cutBodyAfter = 16
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 8})

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	requireCode(t, drain(chunks), fs.CodeIOError)
}

// TestReadTextReportsATransferCutInTheMiddle 验整份读也认这次中断。
//
// 和上面那条是同一件事在另一条路上：这里断在 io.ReadAll 里。
func TestReadTextReportsATransferCutInTheMiddle(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", strings.Repeat("x", 4096))
	fake.cutBodyAfter = 16
	store := fake.store(t, "w1")

	_, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	requireCode(t, err, fs.CodeIOError)
}

// TestStreamTextReportsATruncatedFinalRune 验流末尾压着半个 rune 时报二进制。
//
// 这条走的是 flush 那一支：前面每一块都是合法的，只有最后收尾时才发现
// 这份内容的最后一个字符是残缺的。
func TestStreamTextReportsATruncatedFinalRune(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seedBytes("w1/a.txt", append([]byte("ab"), []byte("中")[0]))
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 2})

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	requireCode(t, drain(chunks), fs.CodeNotText)
}

// TestVerifyCreateIfAbsentReportsAnUnwritableBucket 验自测第一次写就失败时的诊断。
//
// 这时候结论不是「服务端不认那个头」，而是「这个桶可能压根不可写」。
// 两件事混成一条错误的话，运维会去升级一台其实没问题的服务端。
func TestVerifyCreateIfAbsentReportsAnUnwritableBucket(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	denyMethod(fake, http.MethodPut)

	err := store.VerifyCreateIfAbsent(t.Context())
	requireCode(t, err, fs.CodeIOError)
	if !strings.Contains(err.Error(), "可能不可写") {
		t.Fatalf("诊断该说的是桶不可写，实际 %v", err)
	}
	if strings.Contains(err.Error(), "RELEASE.2024-09-13") {
		t.Fatalf("这不是服务端版本的问题，不该这么说：%v", err)
	}
}

// TestVerifyCreateIfAbsentReportsAFailedCleanup 验清场失败时自测直接放弃。
//
// 清不掉上一轮留下的保留键，第一次写就一定会被自己的守卫拒掉，
// 于是自测会得出「服务端认头」这个**碰巧正确但没有依据**的结论。
// 宁可报错。
func TestVerifyCreateIfAbsentReportsAFailedCleanup(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")
	denyMethod(fake, http.MethodDelete)

	requireCode(t, store.VerifyCreateIfAbsent(t.Context()), fs.CodePermissionDenied)
}

// TestVerifyCreateIfAbsentPassesThroughAnUnexpectedFailure 验第二次写因别的原因失败。
//
// 第二次写被拒了，但拒它的不是条件守卫（比如凭据在这中间失效了）——
// 那就不能说「服务端认这个头」，得把那个错误原样交出去。
func TestVerifyCreateIfAbsentPassesThroughAnUnexpectedFailure(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")

	puts := 0
	fake.intercept = func(method string, _ string) (int, string, bool) {
		if method != http.MethodPut {
			return 0, "", false
		}
		puts++
		if puts < 2 {
			return 0, "", false
		}
		return http.StatusForbidden, "AccessDenied", true
	}

	requireCode(t, store.VerifyCreateIfAbsent(t.Context()), fs.CodePermissionDenied)
}
