package llm

import "testing"

// TestIsContextWindowExceededError 逐条走那五种上下文溢出措辞，并确认一条只说
// 「请求太密」的报错不会被误判成上下文溢出——后者要压缩历史，前者只要等一会儿。
func TestIsContextWindowExceededError(t *testing.T) {
	matching := map[string]string{
		"结构化的码":      "context_length_exceeded",
		"点名最大上下文长度":  "This model's maximum context length is 8192 tokens",
		"请求对上下文来说太大": "The request is too large for this model's context window",
		"提示对模型来说太长":  "prompt is too long for this model",
		"输入超过了模型上下文": "input tokens exceed the model context length",
	}
	for name, detail := range matching {
		t.Run(name, func(t *testing.T) {
			if !IsContextWindowExceededError(detail) {
				t.Fatalf("这段应该被认成上下文溢出：%q", detail)
			}
		})
	}

	notMatching := map[string]string{
		"限流":     "rate limit exceeded, please retry after 20s",
		"额度":     "insufficient_quota",
		"只提到上下文": "the context was assembled successfully",
		"空串":     "",
	}
	for name, detail := range notMatching {
		t.Run("不该匹配/"+name, func(t *testing.T) {
			if IsContextWindowExceededError(detail) {
				t.Fatalf("这段不该被认成上下文溢出：%q", detail)
			}
		})
	}
}

// TestIsQuotaExceededError 逐条走那五种额度耗尽措辞。
//
// 「限流」那条负例是这个判据存在的全部理由：两者的英文都带 exceeded，但一个是
// 终局（要人去充值），另一个是瞬时（重试就能过）。
func TestIsQuotaExceededError(t *testing.T) {
	matching := map[string]string{
		"额度不足":   "insufficient_quota",
		"额度超了":   "quota exceeded for this organization",
		"超出当前额度": "You exceeded your current quota, please check your plan",
		"信用耗尽":   "credits exhausted",
		"预算用光":   "out of credits",
	}
	for name, detail := range matching {
		t.Run(name, func(t *testing.T) {
			if !IsQuotaExceededError(detail) {
				t.Fatalf("这段应该被认成额度耗尽：%q", detail)
			}
		})
	}

	notMatching := map[string]string{
		"限流":  "rate limit exceeded, please retry after 20s",
		"上下文": "context_length_exceeded",
		"空串":  "",
	}
	for name, detail := range notMatching {
		t.Run("不该匹配/"+name, func(t *testing.T) {
			if IsQuotaExceededError(detail) {
				t.Fatalf("这段不该被认成额度耗尽：%q", detail)
			}
		})
	}
}

// TestFailureCodesAreDistinct 守住那四个码互不相同。它们是路由用的判别值，
// 撞车会让两类完全不同的失败走同一条恢复路径。
func TestFailureCodesAreDistinct(t *testing.T) {
	codes := []string{
		ContextWindowExceededCode,
		QuotaExceededCode,
		EmptyResponseCode,
		InvalidCredentialCode,
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Fatal("失败码不能是空串")
		}
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("失败码 %q 重复了", code)
		}
		seen[code] = struct{}{}
	}
}
