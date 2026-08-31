// 本文件验这条接缝的失败词汇：码是稳定的、原因串得起来、分派拿得到。
//
// 源: packages/fs/fs/tests/service.spec.ts:170-184

package fs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestErrorCarriesAStableCode 钉住码就是码。
//
// 源: packages/fs/fs/tests/service.spec.ts:171-176
//
// DSH 那条还验了 `error.name === 'FsError'` 和 `instanceof Error`。
// 前者在 Go 里没有对应物（错误没有 name 这个字段），后者由编译器保证——
// *Error 不满足 error 接口的话这个包根本编译不过。
func TestErrorCarriesAStableCode(t *testing.T) {
	t.Parallel()

	failure := &Error{Code: CodeNotFound, Message: "nope"}

	if failure.Code != CodeNotFound {
		t.Errorf("码该是 %s，实际 %s", CodeNotFound, failure.Code)
	}
	if got := failure.Error(); !strings.Contains(got, "nope") || !strings.Contains(got, "FS_NOT_FOUND") {
		t.Errorf("那句话该同时带上诊断和码，实际 %q", got)
	}
}

// TestErrorChainsItsUnderlyingCause 钉住底层原因串得起来。
//
// 源: packages/fs/fs/tests/service.spec.ts:178-183
//
// 对应 DSH 的 ErrorOptions.cause。它只供日志和排查用，分派一律看 Code。
func TestErrorChainsItsUnderlyingCause(t *testing.T) {
	t.Parallel()

	root := errors.New("EACCES")
	failure := &Error{Code: CodeAborted, Message: "cannot read", Err: root}

	if !errors.Is(failure, root) {
		t.Error("底层原因该被 errors.Is 找到")
	}
	if got := failure.Error(); !strings.Contains(got, "EACCES") {
		t.Errorf("那句话该带上底层原因，实际 %q", got)
	}
}

// TestErrorIsRoutableThroughAWrappedChain 钉住 errors.As 是分派的正路。
//
// 源: packages/fs/fs/src/types.ts:190-203
//
// 新增: DSH 靠 `instanceof FsError` 认，一层 wrap 就不认了。Go 这边
// 中间层可以随便用 fmt.Errorf("%w") 包一层，分派方照样拿得到 Code——
// 这是接口文档里「调用方用 errors.As 拿到它」那句话的实际内容。
func TestErrorIsRoutableThroughAWrappedChain(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("读技能目录失败：%w", &Error{Code: CodeTooLarge, Message: "too large"})

	var failure *Error
	if !errors.As(wrapped, &failure) {
		t.Fatal("包了一层之后该还认得出来")
	}
	if failure.Code != CodeTooLarge {
		t.Errorf("码该是 %s，实际 %s", CodeTooLarge, failure.Code)
	}
}

// TestErrorSatisfiesTheStructuralCoder 钉住那个只有一个方法的接口。
//
// 源: packages/fs/fs/src/types.ts:190-203
//
// [ErrorCoder] 的全部意义是：别的包想按「带稳定路由码」这件事分派时，
// **不需要 import 本包**。这条用例用一个本地声明的、和 [ErrorCoder]
// 同名同签名却互不相识的接口来证明这件事——它能接住 *[Error]，
// 正说明结构化满足是真的，而不是靠某一次显式的类型声明。
func TestErrorSatisfiesTheStructuralCoder(t *testing.T) {
	t.Parallel()

	// 假装这是另一个包里的接口：它没有 import fs，也不知道 fs 的存在。
	type someoneElsesCoder interface {
		error
		ErrorCode() string
	}

	var coder someoneElsesCoder = &Error{Code: CodePermissionDenied, Message: "denied"}
	if coder.ErrorCode() != "FS_PERMISSION_DENIED" {
		t.Errorf("码该是 FS_PERMISSION_DENIED，实际 %s", coder.ErrorCode())
	}

	var local ErrorCoder = &Error{Code: CodeIOError, Message: "io"}
	if local.ErrorCode() != string(CodeIOError) {
		t.Errorf("码该是 %s，实际 %s", CodeIOError, local.ErrorCode())
	}
}

// TestTheErrorVocabularyIsExactlyTheThirteenDshCodes 钉住这个封闭集合不多不少。
//
// 源: packages/fs/fs/src/types.ts:170-188
//
// 这一条不是形式主义。词汇是**线上可见**的载荷（工具注册表把 code 原样交给
// 模型和上层），而上层的重试、权限提示、界面分派都是照着它逐个列全的。
// 少一个会让某种失败落进 default 分支，多一个会让上层收到一个它没准备过的码。
// 拼写同样钉死：改一个字母就是改一次线上协议。
func TestTheErrorVocabularyIsExactlyTheThirteenDshCodes(t *testing.T) {
	t.Parallel()

	want := map[ErrorCode]string{
		CodeNotFound:         "FS_NOT_FOUND",
		CodeNotDirectory:     "FS_NOT_DIRECTORY",
		CodeNotText:          "FS_NOT_TEXT",
		CodeNotRegularFile:   "FS_NOT_REGULAR_FILE",
		CodeTooLarge:         "FS_TOO_LARGE",
		CodePermissionDenied: "FS_PERMISSION_DENIED",
		CodeSandboxDenied:    "FS_SANDBOX_DENIED",
		CodeIOError:          "FS_IO_ERROR",
		CodeStaleVersion:     "FS_STALE_VERSION",
		CodeNotObserved:      "FS_NOT_OBSERVED",
		CodeAmbiguousEdit:    "FS_AMBIGUOUS_EDIT",
		CodeEditNotFound:     "FS_EDIT_NOT_FOUND",
		CodeAborted:          "FS_ABORTED",
	}

	if len(want) != 13 {
		t.Fatalf("DSH 那边是 13 个码，这张表列了 %d 个", len(want))
	}
	for code, literal := range want {
		if string(code) != literal {
			t.Errorf("字面量该是 %q，实际 %q", literal, string(code))
		}
	}
}
