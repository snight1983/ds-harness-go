package llm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestErrorCarriesCodeAndCause 钉住三件事：诊断前面缀了码、Unwrap 交出底下那条、
// 以及 errors.Is 能一路找到底。缀码是本包相对 DSH 多做的一件事，理由是一条错误
// 往上走一路通常只剩下这一行字。
func TestErrorCarriesCodeAndCause(t *testing.T) {
	cause := errors.New("底下那条")
	err := NewError("上面这句", "SOME_CODE", cause)

	if !strings.HasPrefix(err.Error(), "SOME_CODE: ") {
		t.Fatalf("诊断该以码开头，得到 %q", err.Error())
	}
	if !strings.Contains(err.Error(), "上面这句") {
		t.Fatalf("诊断该带上那句话，得到 %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is 该能顺着 Unwrap 找到底下那条")
	}
	if errors.Unwrap(NewError("没有底下那条", "CODE", nil)) != nil {
		t.Fatal("没给 cause 时 Unwrap 该是 nil")
	}
}

// TestFailureValid 走一遍那四条取值范围。它们故意不在 NewError 里查
// （见 [Error] 的类型注释），所以这条测试是它们唯一的把关处。
func TestFailureValid(t *testing.T) {
	cases := []struct {
		name    string
		failure Failure
		valid   bool
	}{
		{name: "最小的一份", failure: Failure{Message: "m", Code: "C"}, valid: true},
		{name: "没有描述", failure: Failure{Code: "C"}},
		{name: "没有码", failure: Failure{Message: "m"}},
		{name: "状态码 0 表示没有", failure: Failure{Message: "m", Code: "C", Status: 0}, valid: true},
		{name: "状态码下界", failure: Failure{Message: "m", Code: "C", Status: 100}, valid: true},
		{name: "状态码上界", failure: Failure{Message: "m", Code: "C", Status: 599}, valid: true},
		{name: "状态码偏小", failure: Failure{Message: "m", Code: "C", Status: 99}},
		{name: "状态码偏大", failure: Failure{Message: "m", Code: "C", Status: 600}},
		{name: "等待毫秒为负", failure: Failure{Message: "m", Code: "C", ProviderRetryAfterMs: -1}},
		{name: "等待毫秒为正", failure: Failure{Message: "m", Code: "C", ProviderRetryAfterMs: 1}, valid: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.failure.Valid(); got != testCase.valid {
				t.Fatalf("Valid 该是 %v，得到 %v", testCase.valid, got)
			}
		})
	}
}

// TestNormalizeFailureRecognizesOwnError 确认认得出本包的 Error 时，那份事实
// 原样交出来——码、状态、请求标识一个都不许丢。
func TestNormalizeFailureRecognizesOwnError(t *testing.T) {
	carried := &Error{Failure: Failure{
		Message:              "上游 429",
		Code:                 QuotaExceededCode,
		Status:               429,
		ProviderRetryAfterMs: 1500,
		RequestID:            ProviderRequestID("req-1"),
	}}

	failure := NormalizeFailure(fmt.Errorf("外面又包了一层：%w", carried))
	if failure != carried.Failure {
		t.Fatalf("该原样交出被包住的那份事实，得到 %+v", failure)
	}
}

// TestNormalizeFailureRejectsInvalidCarriedFailure 钉住 Valid 是这条路上的闸门：
// 一份自称本包 Error、但事实本身不合法的错误，不许把它那个码带进流里。
func TestNormalizeFailureRejectsInvalidCarriedFailure(t *testing.T) {
	broken := &Error{Failure: Failure{Message: "有描述没有码"}}

	failure := NormalizeFailure(broken)
	if failure.Code != "UNKNOWN" {
		t.Fatalf("码该退回 UNKNOWN，得到 %q", failure.Code)
	}
	if failure.Message != broken.Error() {
		t.Fatalf("该退回那条错误自己的文本，得到 %q", failure.Message)
	}
}

// TestNormalizeFailureForeignErrors 确认外来错误一律挂 UNKNOWN：第三方 SDK
// 自己那套码不是本装置的分类学，照抄进来会让上层按一个它其实不认识的码去路由。
func TestNormalizeFailureForeignErrors(t *testing.T) {
	failure := NormalizeFailure(errors.New("boom"))
	if failure.Code != "UNKNOWN" || failure.Message != "boom" {
		t.Fatalf("外来错误该是 UNKNOWN + 原文本，得到 %+v", failure)
	}

	nilFailure := NormalizeFailure(nil)
	if nilFailure.Code != "UNKNOWN" || nilFailure.Message != adapterFailureMessage {
		t.Fatalf("nil 该得到兜底描述，得到 %+v", nilFailure)
	}

	blank := NormalizeFailure(errors.New(""))
	if blank.Message != adapterFailureMessage {
		t.Fatalf("说不出自己是什么的错误该得到兜底描述，得到 %q", blank.Message)
	}
}
