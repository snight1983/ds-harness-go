// 本文件把对象存储的失败翻成 [fs.ErrorCode] 那 13 个码。
//
// 翻译层单独放一份，是因为分派方**只**看码：一个 S3 的 AccessDenied 和一次
// 签名不对，对上层是同一件事（这个后端进不去），而一次 NoSuchKey 和一次
// 网络断了完全不是同一件事。让每个调用点各翻各的，早晚会翻得不一样。

package objectstore

import (
	"context"
	"errors"
	"net/http"

	"github.com/minio/minio-go/v7"

	"ds-harness-go/fs"
)

// translate 把底层错误翻成一个 *[fs.Error]。
//
// message 是给人看的诊断，fallback 是认不出来时用的码。
// 原始错误一律挂在 Err 上——它只供日志和排查用，分派一律看 Code。
func translate(err error, fallback fs.ErrorCode, message string) error {
	if err == nil {
		return nil
	}

	// 取消要**先**判。一次被取消的请求在传输层看上去和一次网络故障一样，
	// 翻成 IO_ERROR 的话上层的重试层会去重试一个用户已经放弃的操作。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &fs.Error{Code: fs.CodeAborted, Message: message, Err: err}
	}

	code := fallback
	switch response := minio.ToErrorResponse(err); response.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound", "NoSuchVersion":
		code = fs.CodeNotFound
	case "AccessDenied", "SignatureDoesNotMatch", "InvalidAccessKeyId", "AllAccessDisabled":
		code = fs.CodePermissionDenied
	case "":
		// 不是一个 S3 协议错误（连不上、TLS 握手失败、读到一半断了）。
		// 保持 fallback，通常是 IO_ERROR。
	default:
		if response.StatusCode == http.StatusForbidden {
			code = fs.CodePermissionDenied
		}
	}

	return &fs.Error{Code: code, Message: message, Err: err}
}

// isNotFound 判断这次失败是不是「那个键不在」。
//
// 单独一个判定而不是翻完再看码，是因为「不在」在好几条路上**不是失败**：
// Stat 要把它变成第二个返回值 false，写入要把它变成一次创建。
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	switch response := minio.ToErrorResponse(err); response.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return true
	}
	return minio.ToErrorResponse(err).StatusCode == http.StatusNotFound
}

// isPreconditionFailed 判断这次 PUT 是不是被服务端的条件写守卫拒掉了。
//
// 它**故意不**在 translate 里翻成某个码：同一个 412 在两条路上是两件不同的事——
// `If-None-Match: *` 上它表示「已经存在」（[fs.CodeNotObserved]），
// `If-Match: <etag>` 上它表示「被人改过了」（[fs.CodeStaleVersion]）。
// 只有发起那次写的代码知道自己带的是哪个头。
//
// 409 一并认：AWS 在条件写撞车时会给 ConditionalRequestConflict，
// 语义上和 412 一样是「这次守卫没过」，只是提示可以重试。
func isPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "PreconditionFailed", "ConditionalRequestConflict":
		return true
	}
	return response.StatusCode == http.StatusPreconditionFailed ||
		response.StatusCode == http.StatusConflict
}
