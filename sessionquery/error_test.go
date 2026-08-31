// 本文件的作用：本包那套失败分类怎么被认出来、怎么串成链。
//
// 源: packages/session-query/session-query/src/config.ts:20-48

package sessionquery

import (
	"context"
	"errors"
	"testing"
)

func TestCodeIsItselfASentinelError(t *testing.T) {
	t.Parallel()

	if CodeAborted.Error() != "SESSION_QUERY_ABORTED" {
		t.Fatalf("码的文字变了，对外协议就变了：%q", CodeAborted.Error())
	}
	if !errors.Is(fail(CodeAborted, "随便说点什么"), CodeAborted) {
		t.Fatal("errors.Is 认不出这条失败的分类")
	}
	if errors.Is(fail(CodeAborted, "随便说点什么"), CodeInvalidFilter) {
		t.Fatal("errors.Is 把两个不同的码当成了一个")
	}
}

func TestErrorMessageShowsTheCauseOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()

	bare := fail(CodeInvalidFilter, "区间反了")
	if bare.Error() != "SESSION_QUERY_INVALID_FILTER：区间反了" {
		t.Fatalf("没有底层原因时的文字不对：%q", bare.Error())
	}

	cause := errors.New("底下那条")
	wrapped := wrap(CodeInvalidFilter, cause, "区间反了")
	if wrapped.Error() != "SESSION_QUERY_INVALID_FILTER：区间反了：底下那条" {
		t.Fatalf("有底层原因时的文字不对：%q", wrapped.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("errors.Is 顺不到底层那条")
	}
	if errors.Unwrap(wrapped) != cause {
		t.Fatal("errors.Unwrap 交出来的不是底层那条")
	}
}

func TestErrorIsRefusesATargetThatIsNotACode(t *testing.T) {
	t.Parallel()

	if fail(CodeAborted, "随便说点什么").Is(errors.New("不是码")) {
		t.Fatal("拿一条普通错误当分类去比，不该比中")
	}
}

func TestCheckAbortedTranslatesCancellationAndPassesNilThrough(t *testing.T) {
	t.Parallel()

	if err := checkAborted(nil); err != nil {
		t.Fatalf("没被取消就不该造错误：%v", err)
	}

	err := checkAborted(context.Canceled)
	requireCode(t, err, CodeAborted)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("原来那条取消没有留在链上")
	}
}

func TestIsAbortedRecognizesEveryShapeOfCancellation(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want bool
	}{
		"本包的取消码":       {err: fail(CodeAborted, "取消了"), want: true},
		"裸的 ctx 取消":    {err: context.Canceled, want: true},
		"裸的 ctx 超时":    {err: context.DeadlineExceeded, want: true},
		"裹过一层的 ctx 取消": {err: wrap(CodePersistenceFailed, context.Canceled, "后端挂了"), want: true},
		"跟取消无关的失败":     {err: fail(CodeInvalidFilter, "区间反了")},
		"没出错":          {},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if isAborted(testCase.err) != testCase.want {
				t.Fatalf("判断不对：想要 %v，实际 %v（%v）", testCase.want, !testCase.want, testCase.err)
			}
		})
	}
}

func TestNotFoundNamesTheSessionItLookedFor(t *testing.T) {
	t.Parallel()

	err := notFound("s1")
	requireCode(t, err, CodeSessionNotFound)
	if err.Message != `找不到会话 "s1"` {
		t.Fatalf("没说清找的是哪个会话：%q", err.Message)
	}
}
