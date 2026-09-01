// 本文件验错误翻译层。
//
// 这一组**不发请求**，直接拿构造好的 [minio.ErrorResponse] 喂进去。理由是
// 翻译层的价值在于「一张表覆盖全」：让每个调用点各翻各的，早晚会翻得不一样。
// 要证明这张表是全的，就得逐个码断言，而其中好几个码（签名不对、桶被禁用）
// 靠一台假服务端造出来只是在绕远路。

package objectstore

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/snight1983/ds-harness-go/fs"
)

// s3Failure 造一个 minio 会认出来的协议错误。
func s3Failure(code string, status int) error {
	return minio.ErrorResponse{Code: code, Message: code, StatusCode: status}
}

// TestTranslateMapsTheProtocolVocabulary 验协议错误码到接缝错误码的整张表。
func TestTranslateMapsTheProtocolVocabulary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code   string
		status int
		want   fs.ErrorCode
	}{
		{"NoSuchKey", http.StatusNotFound, fs.CodeNotFound},
		{"NoSuchBucket", http.StatusNotFound, fs.CodeNotFound},
		{"NotFound", http.StatusNotFound, fs.CodeNotFound},
		{"NoSuchVersion", http.StatusNotFound, fs.CodeNotFound},
		{"AccessDenied", http.StatusForbidden, fs.CodePermissionDenied},
		{"SignatureDoesNotMatch", http.StatusForbidden, fs.CodePermissionDenied},
		{"InvalidAccessKeyId", http.StatusForbidden, fs.CodePermissionDenied},
		{"AllAccessDisabled", http.StatusForbidden, fs.CodePermissionDenied},
		// 认不出的码但状态是 403：照样是「这个后端进不去」。
		{"NoSuchThingWeKnowOf", http.StatusForbidden, fs.CodePermissionDenied},
		// 认不出的码而且状态也不说明什么：落回兜底。
		{"InternalError", http.StatusInternalServerError, fs.CodeIOError},
	}

	for _, item := range cases {
		t.Run(item.code, func(t *testing.T) {
			t.Parallel()

			err := translate(s3Failure(item.code, item.status), fs.CodeIOError, "出事了")
			requireCode(t, err, item.want)

			var failure *fs.Error
			if !errors.As(err, &failure) || failure.Err == nil {
				t.Fatal("底层错误该被挂在 Err 上，供日志和排查用")
			}
		})
	}
}

// TestTranslateKeepsTheFallbackForNonProtocolFailures 验不是协议错误时用兜底码。
//
// 连不上、TLS 握手失败、读到一半断了——这些在 [minio.ToErrorResponse] 眼里
// 都是空 Code，不能当成任何一个具体的失败去分派。
func TestTranslateKeepsTheFallbackForNonProtocolFailures(t *testing.T) {
	t.Parallel()

	err := translate(errors.New("连接被重置"), fs.CodeIOError, "出事了")
	requireCode(t, err, fs.CodeIOError)
}

// TestTranslateChecksCancellationFirst 验取消**先**判。
//
// 一次被取消的请求在传输层看上去和一次网络故障一样。翻成 IO_ERROR 的话，
// 上层的重试层会去重试一个用户已经放弃的操作——而且是每一次都重试。
func TestTranslateChecksCancellationFirst(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		// 故意把取消包在一个看上去像「服务端 500」的壳里，证明顺序真的分先后。
		wrapped := errors.Join(s3Failure("InternalError", http.StatusInternalServerError), cause)
		requireCode(t, translate(wrapped, fs.CodeIOError, "出事了"), fs.CodeAborted)
	}
}

// TestTranslatePassesNilThrough 验 nil 进 nil 出。
func TestTranslatePassesNilThrough(t *testing.T) {
	t.Parallel()

	if err := translate(nil, fs.CodeIOError, "出事了"); err != nil {
		t.Fatalf("nil 该原样出去，实际 %v", err)
	}
}

// TestIsNotFoundRecognizesAbsence 验「那个键不在」的判定。
//
// 它是单独一个判定而不是翻完再看码，因为「不在」在好几条路上**不是失败**：
// Stat 要把它变成第二个返回值 false，写入要把它变成一次创建。
func TestIsNotFoundRecognizesAbsence(t *testing.T) {
	t.Parallel()

	if isNotFound(nil) {
		t.Fatal("没有错误就不是「不在」")
	}
	for _, code := range []string{"NoSuchKey", "NoSuchBucket", "NotFound"} {
		if !isNotFound(s3Failure(code, http.StatusNotFound)) {
			t.Fatalf("%s 该被认成「不在」", code)
		}
	}
	// 码认不出来但状态是 404：照样算不在。
	if !isNotFound(s3Failure("SomethingElse", http.StatusNotFound)) {
		t.Fatal("404 该被认成「不在」")
	}
	if isNotFound(s3Failure("AccessDenied", http.StatusForbidden)) {
		t.Fatal("403 不是「不在」——那是一条能靠改权限打开的路")
	}
}

// TestIsPreconditionFailedRecognizesARejectedGuard 验条件写被拒的判定。
//
// 它故意只给一个 bool 而不翻成某个码：同一个 412 在两条路上是两件不同的事。
// `If-None-Match: *` 上它表示「已经存在」，`If-Match: <etag>` 上它表示
// 「被人改过了」，只有发起那次写的代码知道自己带的是哪个头。
func TestIsPreconditionFailedRecognizesARejectedGuard(t *testing.T) {
	t.Parallel()

	if isPreconditionFailed(nil) {
		t.Fatal("没有错误就不是被守卫拒了")
	}
	for _, item := range []struct {
		code   string
		status int
	}{
		{"PreconditionFailed", http.StatusPreconditionFailed},
		// AWS 在条件写撞车时给这个码，语义上和 412 一样是「这次守卫没过」。
		{"ConditionalRequestConflict", http.StatusConflict},
		{"SomethingElse", http.StatusPreconditionFailed},
		{"SomethingElse", http.StatusConflict},
	} {
		if !isPreconditionFailed(s3Failure(item.code, item.status)) {
			t.Fatalf("%s/%d 该被认成守卫被拒", item.code, item.status)
		}
	}
	if isPreconditionFailed(s3Failure("NoSuchKey", http.StatusNotFound)) {
		t.Fatal("404 不是守卫被拒")
	}
}

// TestNewRejectsAMalformedEndpoint 验端点必须是裸的 host[:port]。
//
// minio-go 自己会拒，这里只是把那次拒绝接住并包上本包的话——一条
// 「建客户端失败」比一条来自 SDK 内部的错误更容易定位到那行配置。
//
// 只验这两种形状，是因为 minio-go 的主机名校验**是故意宽松的**
// （`s3utils.IsValidDomain` 的原话：「We let it valid and fail later」）：
// 它只拒一小撮字符，别的一律放过留给真正的连接去失败。所以 [New] 不是
// 一个主机名拼写检查器，也不该被当成一个来测。
func TestNewRejectsAMalformedEndpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"example.invalid/some/path", "example!invalid"} {
		if _, err := New(Config{Endpoint: endpoint, Bucket: "world"}); err == nil {
			t.Fatalf("%q 该被拒", endpoint)
		}
	}
}
