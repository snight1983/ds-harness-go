package llm

import (
	"strings"
	"testing"
)

// TestDefaultAppIdentityIsFresh 钉住把 DSH 那个导出常量改成函数的理由：
// 每次交出来的都是一份新的，谁都改不动别人手里那份产品身份。
func TestDefaultAppIdentityIsFresh(t *testing.T) {
	first := DefaultAppIdentity()
	first.Product = "tampered"

	if again := DefaultAppIdentity(); again.Product != "deepseek-harness" {
		t.Fatalf("改到手上那份不该影响下一位调用方，得到 %q", again.Product)
	}
}

// TestDefaultAppIdentityCarriesNoSecret 钉住这份身份的边界：三样公开事实，
// 而且版本跟着那个常量走。文件开头明写「按用户或者按请求变化的东西一样都不许进来」，
// 这条测试守的就是那句话里可机器判定的那一半。
func TestDefaultAppIdentityCarriesNoSecret(t *testing.T) {
	identity := DefaultAppIdentity()
	if identity.Product == "" || identity.URL == "" {
		t.Fatalf("产品名和主页都不能空：%+v", identity)
	}
	if identity.Version != AppIdentityVersion {
		t.Fatalf("版本该是常量 %q，得到 %q", AppIdentityVersion, identity.Version)
	}
	if !strings.HasPrefix(identity.URL, "https://") {
		t.Fatalf("主页该是一个 https 地址，得到 %q", identity.URL)
	}
}

// TestUserAgentShape 钉住 RFC 9110 §10.1.5 那个 product/version (comment) 形状。
func TestUserAgentShape(t *testing.T) {
	agent := UserAgent(AppIdentity{Product: "acme", Version: "9.9", URL: "https://example.test"})
	if agent != "acme/9.9 (+https://example.test)" {
		t.Fatalf("User-Agent 形状不对：%q", agent)
	}
}

// TestAttributionHeadersLowercase 钉住头名字是小写的，以及当下只有一个头。
// 适配器照着这张表往请求上加，多一个少一个都得先改这条测试。
func TestAttributionHeadersLowercase(t *testing.T) {
	headers := AttributionHeaders(DefaultAppIdentity())
	if len(headers) != 1 {
		t.Fatalf("当下只该有一个归属头，得到 %v", headers)
	}
	value, present := headers["user-agent"]
	if !present {
		t.Fatalf("头名字必须是小写的 user-agent，得到 %v", headers)
	}
	if value != UserAgent(DefaultAppIdentity()) {
		t.Fatalf("头的值该和 UserAgent 一致，得到 %q", value)
	}
}
