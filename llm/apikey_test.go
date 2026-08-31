package llm

import (
	"errors"
	"strings"
	"testing"
)

// TestNormalizeAPIKey 走一遍那三种裁定：去空白之后能用、去空白之后什么都不剩、
// 以及里面有 HTTP 头带不了的字符。
func TestNormalizeAPIKey(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		ok     bool
		value  string
		reason APIKeyRejection
	}{
		{name: "干净的密钥", raw: "sk-abc123", ok: true, value: "sk-abc123"},
		{name: "首尾空白不声张地去掉", raw: "  sk-abc123\n\t", ok: true, value: "sk-abc123"},
		{name: "全是空白", raw: " \t\r\n ", reason: APIKeyEmpty},
		{name: "空串", raw: "", reason: APIKeyEmpty},
		{name: "中间有空格", raw: "sk abc", reason: APIKeyIllegalCharacters},
		{name: "有换行", raw: "sk\nabc", reason: APIKeyIllegalCharacters},
		// Latin-1 是故意排除的：头理论上带得动，但没有哪家提供方签发这种密钥。
		{name: "Latin-1", raw: "sk-café", reason: APIKeyIllegalCharacters},
		{name: "控制字符", raw: "sk-\x01abc", reason: APIKeyIllegalCharacters},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			checked := NormalizeAPIKey(testCase.raw)
			if checked.OK != testCase.ok {
				t.Fatalf("OK 该是 %v，得到 %v", testCase.ok, checked.OK)
			}
			if checked.Value != testCase.value {
				t.Fatalf("Value 该是 %q，得到 %q", testCase.value, checked.Value)
			}
			if checked.Reason != testCase.reason {
				t.Fatalf("Reason 该是 %q，得到 %q", testCase.reason, checked.Reason)
			}
		})
	}
}

// TestLegalAPIKeyRuneBoundaries 钉住 0x21–0x7E 这个闭区间的四个边界字符，
// 因为把正则换成逐字符判据之后，差一位的错误在别处看不出来。
func TestLegalAPIKeyRuneBoundaries(t *testing.T) {
	if legalAPIKeyRune(0x20) {
		t.Fatal("空格不该合法")
	}
	if !legalAPIKeyRune(0x21) {
		t.Fatal("0x21 该合法")
	}
	if !legalAPIKeyRune(0x7E) {
		t.Fatal("0x7E 该合法")
	}
	if legalAPIKeyRune(0x7F) {
		t.Fatal("0x7F 不该合法")
	}
}

// TestAssertUsableAPIKeyAccepts 确认能用的密钥原样（去过空白）交回来，不报错。
func TestAssertUsableAPIKeyAccepts(t *testing.T) {
	value, err := AssertUsableAPIKey("  sk-abc  ", "llm-test", "providers.acme.apiKey")
	if err != nil {
		t.Fatalf("干净的密钥不该被拒：%v", err)
	}
	if value != "sk-abc" {
		t.Fatalf("该交回去过空白的那份，得到 %q", value)
	}
}

// TestAssertUsableAPIKeyNeverEchoesTheSecret 是本包最要紧的一条：诊断里一个
// 密钥字符都不许出现。它点的是「去哪儿改」，不是「你写的是什么」。
func TestAssertUsableAPIKeyNeverEchoesTheSecret(t *testing.T) {
	secret := "sk-super-secret-value"
	for _, raw := range []string{"   ", secret + " with space"} {
		_, err := AssertUsableAPIKey(raw, "llm-test", "providers.acme.apiKey")
		if err == nil {
			t.Fatalf("%q 该被拒", raw)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("诊断里回显了密钥：%s", err)
		}
		if !strings.Contains(err.Error(), "providers.acme.apiKey") {
			t.Fatalf("诊断该点出去哪儿改，得到：%s", err)
		}
		if !strings.Contains(err.Error(), "llm-test") {
			t.Fatalf("诊断该缀上发出拒绝的包名，得到：%s", err)
		}
	}
}

// TestAssertUsableAPIKeyCarriesTheCode 确认两种拒绝都挂 INVALID_CREDENTIAL，
// 而且诊断分得开——空和字符不合法要采取的行动不一样。
func TestAssertUsableAPIKeyCarriesTheCode(t *testing.T) {
	_, blank := AssertUsableAPIKey("", "llm-test", "ref")
	_, illegal := AssertUsableAPIKey("sk abc", "llm-test", "ref")

	for _, err := range []error{blank, illegal} {
		var carrier *Error
		if !errors.As(err, &carrier) {
			t.Fatalf("该是一条本包的 Error：%v", err)
		}
		if carrier.Failure.Code != InvalidCredentialCode {
			t.Fatalf("码该是 %q，得到 %q", InvalidCredentialCode, carrier.Failure.Code)
		}
	}
	if blank.Error() == illegal.Error() {
		t.Fatal("两种拒绝的诊断该分得开")
	}
	if !strings.Contains(blank.Error(), "blank") {
		t.Fatalf("空密钥的诊断该说它是空的：%s", blank)
	}
	if !strings.Contains(illegal.Error(), "no HTTP header can carry") {
		t.Fatalf("非法字符的诊断该说头带不了：%s", illegal)
	}
}
