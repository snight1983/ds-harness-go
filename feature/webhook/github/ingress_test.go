// 本文件验这条入口的全部理由：签名对不上的字节走不出去，
// 而认过之后交出来的事件体一个字节都没被动过。

package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/credentials"
)

const (
	secretValue = "十分要紧的共享密钥"
	secretRef   = credentials.Ref("GITHUB_WEBHOOK_SECRET")
)

// fakeCredentials 是一条只回答一个引用的凭据接缝。
type fakeCredentials struct {
	credentials.Provider

	value string
	err   error
}

func (f fakeCredentials) Resolve(context.Context, credentials.Ref) (credentials.Resolved, bool, error) {
	if f.err != nil {
		return credentials.Resolved{}, false, f.err
	}
	if f.value == "" {
		return credentials.Resolved{}, false, nil
	}
	return credentials.Resolved{Value: f.value, Source: "test"}, true, nil
}

var fixedNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

func newIngress(t *testing.T, provider credentials.Provider) *Ingress {
	t.Helper()
	ingress, err := New(Config{
		Source:      "primary-github",
		SecretRef:   secretRef,
		Credentials: provider,
		Now:         func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("造入口不该失败：%v", err)
	}
	return ingress
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func goodHeaders(body []byte) map[string][]string {
	return map[string][]string{
		HeaderSignature: {sign(secretValue, body)},
		HeaderDelivery:  {"d-1"},
		HeaderEvent:     {"pull_request"},
	}
}

func TestAccept认下一次签名对的投递(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	delivery, err := ingress.Accept(context.Background(), goodHeaders(body), body)
	if err != nil {
		t.Fatalf("签名对的投递不该被拒：%v", err)
	}
	if delivery.Kind != Kind {
		t.Errorf("kind 对不上：%q", delivery.Kind)
	}
	if delivery.Source != "primary-github" {
		t.Errorf("source 对不上：%q", delivery.Source)
	}
	if delivery.DeliveryID != "d-1" {
		t.Errorf("投递 id 对不上：%q", delivery.DeliveryID)
	}
	if !delivery.ReceivedAt.Equal(fixedNow) {
		t.Errorf("收到的时刻对不上：%v", delivery.ReceivedAt)
	}
	if !json.Valid(delivery.Event) {
		t.Fatalf("事件体不是合法 JSON：%s", delivery.Event)
	}
}

// TestAccept把事件体原样穿过去 验大整数的精度和键序都没被动。
//
// 规则完全可能拿 payload 那一段再算一次签名或者按字节比对，
// 解一遍再排回去就会把这两样都磨掉。
func TestAccept把事件体原样穿过去(t *testing.T) {
	// 这个整数超出 float64 能精确表示的范围，解成 any 再排回去必然变样；
	// 键序也是故意反字典序的。
	body := []byte(`{"zeta":1,"id":123456789012345678901,"alpha":2}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	delivery, err := ingress.Accept(context.Background(), goodHeaders(body), body)
	if err != nil {
		t.Fatalf("不该被拒：%v", err)
	}

	want := `{"name":"pull_request","payload":` + string(body) + `}`
	if string(delivery.Event) != want {
		t.Errorf("事件体被动过了：\n拿到 %s\n要的 %s", delivery.Event, want)
	}
}

func TestAccept拒掉签名不对的投递(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	headers := goodHeaders(body)
	headers[HeaderSignature] = []string{sign("另一把密钥", body)}

	if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("要的是 ErrBadSignature，拿到 %v", err)
	}
}

// TestAccept改一个字节就拒 验签名认的是这一段字节本身。
func TestAccept改一个字节就拒(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})
	headers := goodHeaders(body)

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] = 'D'

	if _, err := ingress.Accept(context.Background(), headers, tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("要的是 ErrBadSignature，拿到 %v", err)
	}
}

// TestAccept只认sha256那一支 验 GitHub 同时发的 SHA-1 签名连读都不读。
func TestAccept只认sha256那一支(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})
	headers := goodHeaders(body)
	headers[HeaderSignature] = []string{strings.Replace(sign(secretValue, body), signaturePrefix, "sha1=", 1)}

	if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("要的是 ErrBadSignature，拿到 %v", err)
	}
}

func TestAccept缺一个身份头就拒(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	for _, name := range []string{HeaderSignature, HeaderDelivery, HeaderEvent} {
		headers := goodHeaders(body)
		delete(headers, name)
		if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrMissingHeader) {
			t.Errorf("缺 %s 时要的是 ErrMissingHeader，拿到 %v", name, err)
		}
	}
}

// TestAccept一个头出现两次就拒 验不会退化成取第一个。
//
// 取第一个是一条真实的绕过路径：中间每一跳挑的那一个不一定是同一个，
// 于是「验的那一份」和「用的那一份」可以不是同一份。
func TestAccept一个头出现两次就拒(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	headers := goodHeaders(body)
	headers[HeaderSignature] = []string{sign(secretValue, body), sign("另一把密钥", body)}

	if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrAmbiguousHeader) {
		t.Fatalf("要的是 ErrAmbiguousHeader，拿到 %v", err)
	}
}

// TestAccept同一个头的两种拼法也算歧义 验大小写折叠之后那道关照样成立。
func TestAccept同一个头的两种拼法也算歧义(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	headers := goodHeaders(body)
	headers["X-Hub-Signature-256"] = []string{sign("另一把密钥", body)}

	if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrAmbiguousHeader) {
		t.Fatalf("要的是 ErrAmbiguousHeader，拿到 %v", err)
	}
}

// TestAccept请求头大小写随便 验单独一份的时候折叠是管用的。
func TestAccept请求头大小写随便(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	headers := map[string][]string{
		"X-Hub-Signature-256": {sign(secretValue, body)},
		"X-GitHub-Delivery":   {"d-1"},
		"X-GitHub-Event":      {"push"},
	}
	if _, err := ingress.Accept(context.Background(), headers, body); err != nil {
		t.Fatalf("大写拼法不该被拒：%v", err)
	}
}

// TestAccept密钥拿不到和签名不对分得开 验「忘了填密钥」不会在日志里长得像一场攻击。
func TestAccept密钥拿不到和签名不对分得开(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	headers := goodHeaders(body)

	cases := map[string]credentials.Provider{
		"没配置":  fakeCredentials{},
		"后端坏了": fakeCredentials{err: errors.New("凭据后端坏了")},
	}
	for name, provider := range cases {
		ingress := newIngress(t, provider)
		_, err := ingress.Accept(context.Background(), headers, body)
		if !errors.Is(err, ErrSecretUnavailable) {
			t.Errorf("%s：要的是 ErrSecretUnavailable，拿到 %v", name, err)
		}
		if errors.Is(err, ErrBadSignature) {
			t.Errorf("%s：不该同时是 ErrBadSignature", name)
		}
	}
}

// TestAccept签名过了但不是JSON对象也拒 验验签在解析之前，而解析仍旧要做。
func TestAccept签名过了但不是JSON对象也拒(t *testing.T) {
	ingress := newIngress(t, fakeCredentials{value: secretValue})

	for name, body := range map[string][]byte{
		"顶层是数组":    []byte(`[1,2,3]`),
		"顶层是字符串":   []byte(`"不是对象"`),
		"不是合法JSON": []byte(`{"action":`),
		"空的":       []byte(``),
	} {
		headers := goodHeaders(body)
		headers[HeaderSignature] = []string{sign(secretValue, body)}
		if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrMalformedPayload) {
			t.Errorf("%s：要的是 ErrMalformedPayload，拿到 %v", name, err)
		}
	}
}

func TestAccept非UTF8的请求体也拒(t *testing.T) {
	ingress := newIngress(t, fakeCredentials{value: secretValue})
	body := []byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'}
	headers := goodHeaders(body)
	headers[HeaderSignature] = []string{sign(secretValue, body)}

	if _, err := ingress.Accept(context.Background(), headers, body); !errors.Is(err, ErrMalformedPayload) {
		t.Fatalf("要的是 ErrMalformedPayload，拿到 %v", err)
	}
}

func TestNew缺配置就拒(t *testing.T) {
	for name, config := range map[string]Config{
		"没有 Source":      {SecretRef: secretRef, Credentials: fakeCredentials{}},
		"没有 SecretRef":   {Source: "s", Credentials: fakeCredentials{}},
		"没有 Credentials": {Source: "s", SecretRef: secretRef},
	} {
		if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s：要的是 ErrInvalidConfig，拿到 %v", name, err)
		}
	}
}
