// 本文件的作用：这条入口的配置、它拒绝的那几种理由，以及从字节到一次已认投递的那条路。
//
// 源: packages/webhook/webhook-github/src/handler.ts

package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/credentials"
	"github.com/snight1983/ds-harness-go/feature/webhook"
)

// Kind 是本适配器认领的提供方族，[webhook.Rule.Kind] 拿它配对。
//
// 源: packages/webhook/webhook-github/src/handler.ts:103
const Kind = "github"

// 三个身份头。名字一律小写，比对之前入参的键也折成小写。
//
// 源: packages/webhook/webhook-github/src/handler.ts:98-100
const (
	// HeaderSignature 带着 GitHub 用共享密钥算出的 `sha256=<hex>`。
	HeaderSignature = "x-hub-signature-256"
	// HeaderDelivery 带着这次投递的身份。
	HeaderDelivery = "x-github-delivery"
	// HeaderEvent 带着事件名，比如 pull_request。
	HeaderEvent = "x-github-event"
)

// signaturePrefix 是签名头唯一接受的算法前缀。
//
// 只认这一个是有意的：GitHub 同时还发一个 SHA-1 的 `x-hub-signature`，
// 而 SHA-1 那一支已经不该再用来认身份了。本包连读都不读它。
const signaturePrefix = "sha256="

// 这条入口自己会返回的几种拒绝。做成哨兵值是为了让装配方用 errors.Is 挑状态码，
// 而不是去匹配错误文案。
//
// 新增: DSH 把状态码写进它自己的 WebhookHttpError。本包不绑 HTTP，
// 对照表因此挪到装配方手上，见包文档。
var (
	// ErrInvalidConfig 表示交给 [New] 的那份配置缺了东西。
	ErrInvalidConfig = errors.New("webhook/github: 配置不完整")

	// ErrMissingHeader 表示三个身份头里少了一个，或者它是空的。
	//
	// 源: packages/webhook/webhook-github/src/handler.ts:25-32
	ErrMissingHeader = errors.New("webhook/github: 缺一个必需的请求头")

	// ErrAmbiguousHeader 表示某个身份头出现了不止一次。
	//
	// 源: packages/webhook/webhook-github/src/handler.ts:28
	//
	// 取第一个是一条真实的绕过路径，见包文档。
	ErrAmbiguousHeader = errors.New("webhook/github: 这个请求头出现了不止一次")

	// ErrSecretUnavailable 表示共享密钥此刻解析不出来。
	//
	// 源: packages/webhook/webhook-github/src/handler.ts:102-104
	//
	// 它和 [ErrBadSignature] 必须分得开：一个是本端没配好，一个是对端没拿对密钥。
	// 混成一个，「密钥忘了填」在日志里会长得像一场攻击。
	ErrSecretUnavailable = errors.New("webhook/github: 共享密钥现在拿不到")

	// ErrBadSignature 表示签名头的形状不对，或者它和请求体对不上。
	//
	// 源: packages/webhook/webhook-github/src/handler.ts:112
	//
	// 形状不对和对不上是同一个错误，因为两者对发送方是同一件事：它没有拿对密钥。
	// 分开报会把「本端只认 sha256」这件事告诉一个还没通过认证的人。
	ErrBadSignature = errors.New("webhook/github: 签名对不上")

	// ErrMalformedPayload 表示请求体不是一段 UTF-8 的 JSON 对象。
	//
	// 源: packages/webhook/webhook-github/src/handler.ts:56-70
	ErrMalformedPayload = errors.New("webhook/github: 请求体不是一个 JSON 对象")
)

// Config 是一条 GitHub 入口要的四样东西。
//
// 源: packages/webhook/webhook-github/src/index.ts:18-27
//
// 新增: 上游那份配置里的 path 和 maxBodyBytes 都不在这里。path 是一条 HTTP 路由，
// 本包不注册路由；maxBodyBytes 要边读边数才有意义，见包文档的「不做什么」。
type Config struct {
	// Source 是这个适配器实例的名字，原样带给规则。
	//
	// 一份部署可以挂不止一条 GitHub 入口（两个组织、两把密钥），
	// 规则靠这一位分辨眼前这次投递是从哪一条进来的。
	Source webhook.SourceID

	// SecretRef 指向那把共享密钥。
	SecretRef credentials.Ref

	// Credentials 是解析 [Config.SecretRef] 的那条接缝。
	Credentials credentials.Provider

	// Now 取当前时刻，用来盖 [webhook.VerifiedDelivery.ReceivedAt]。
	//
	// 新增: DSH 直接调 Date.now()。这里做成可注入的口子，理由和本仓库别处一样：
	// 不给定一个可控的时钟，「收到的时刻是不是这一刻」就没法测。
	// 留空回落到 [time.Now]。
	Now func() time.Time
}

func (c Config) validate() error {
	var missing []string
	if c.Source == "" {
		missing = append(missing, "Source")
	}
	if c.SecretRef == "" {
		missing = append(missing, "SecretRef")
	}
	if c.Credentials == nil {
		missing = append(missing, "Credentials")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w：还缺 %s", ErrInvalidConfig, strings.Join(missing, "、"))
	}
	return nil
}

// Ingress 是一条配好的 GitHub 入口。
//
// 它没有可变状态，可以被多个 goroutine 同时用。
type Ingress struct {
	config Config
}

// New 校验一份配置，造一条入口。
//
// 源: packages/webhook/webhook-github/src/index.ts:46-61（apply）
func New(config Config) (*Ingress, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Ingress{config: config}, nil
}

// Accept 认一次投递：验签、取三个身份、拼出事件体。
//
// 源: packages/webhook/webhook-github/src/handler.ts:78-129
//
// headers 的键大小写随便，本包自己折成小写；每个身份头必须**恰好**出现一次。
// body 是**原始字节**，签名就是按这一段算的——调用方在这之前对它做过任何
// 规整（重新格式化 JSON、换行尾、去空白），签名就一定对不上。
//
// 交回的投递已经满足 [webhook.Runtime.Dispatch] 的全部前置条件；
// 派不派、派给谁，是运行时的事。
//
// 新增: DSH 那个处理函数验完就自己 dispatch，然后回 202。这里只交回投递，
// 由装配方决定派给哪个运行时——同一次投递要同时进两个运行时（比如一个跑规则、
// 一个只做审计）时，那件事在这里是自然的，在上游要改这个包。
func (i *Ingress) Accept(ctx context.Context, headers map[string][]string, body []byte) (webhook.VerifiedDelivery, error) {
	lower := lowered(headers)
	signature, err := exactlyOne(lower, HeaderSignature)
	if err != nil {
		return webhook.VerifiedDelivery{}, err
	}
	deliveryID, err := exactlyOne(lower, HeaderDelivery)
	if err != nil {
		return webhook.VerifiedDelivery{}, err
	}
	eventName, err := exactlyOne(lower, HeaderEvent)
	if err != nil {
		return webhook.VerifiedDelivery{}, err
	}

	secret, err := i.secret(ctx)
	if err != nil {
		return webhook.VerifiedDelivery{}, err
	}
	if !verify(secret, signature, body) {
		return webhook.VerifiedDelivery{}, fmt.Errorf("%w：投递 %q", ErrBadSignature, deliveryID)
	}

	// 验签在**解析之前**：一段还没认过身份的字节不该让解析器动一下。
	event, err := composeEvent(eventName, body)
	if err != nil {
		return webhook.VerifiedDelivery{}, err
	}

	return webhook.VerifiedDelivery{
		Kind:       Kind,
		Source:     i.config.Source,
		DeliveryID: webhook.DeliveryID(deliveryID),
		Event:      event,
		ReceivedAt: i.config.Now(),
	}, nil
}

// secret 现解析一次共享密钥。
//
// 源: packages/webhook/webhook-github/src/handler.ts:102-104
//
// 每次都重解是 [credentials.Provider.Resolve] 的规矩：轮换过的密钥下一次投递就生效。
// 存下来的空值等于没配（那条规则贯穿整条凭据接缝），所以这里也把空值当没配。
func (i *Ingress) secret(ctx context.Context) (string, error) {
	resolved, configured, err := i.config.Credentials.Resolve(ctx, i.config.SecretRef)
	if err != nil {
		return "", fmt.Errorf("%w：解析 %q 失败：%w", ErrSecretUnavailable, i.config.SecretRef, err)
	}
	if !configured || resolved.Value == "" {
		return "", fmt.Errorf("%w：引用 %q 没有配置", ErrSecretUnavailable, i.config.SecretRef)
	}
	return resolved.Value, nil
}

// lowered 把请求头的键折成小写。
//
// 新增: DSH 走 node:http 的 headersDistinct，那一步已经替它折过了。本包不绑
// net/http，所以自己折——否则同一个头写成 X-Hub-Signature-256 还是
// x-hub-signature-256 会得到两种结果，而 HTTP 的头名本来就不区分大小写。
//
// 同一个头的两种拼法在这里会**并到一起**，于是它照样触发那道
// [ErrAmbiguousHeader]：两份不同大小写的签名头是同一种歧义。
func lowered(headers map[string][]string) map[string][]string {
	folded := make(map[string][]string, len(headers))
	for name, values := range headers {
		key := strings.ToLower(name)
		folded[key] = append(folded[key], values...)
	}
	return folded
}

// exactlyOne 取一个必须恰好出现一次、且不是空白的头。
//
// 源: packages/webhook/webhook-github/src/handler.ts:25-32（requiredHeader）
func exactlyOne(headers map[string][]string, name string) (string, error) {
	values := headers[name]
	if len(values) == 0 {
		return "", fmt.Errorf("%w：%s", ErrMissingHeader, name)
	}
	if len(values) > 1 {
		return "", fmt.Errorf("%w：%s 出现了 %d 次", ErrAmbiguousHeader, name, len(values))
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", fmt.Errorf("%w：%s 是空的", ErrMissingHeader, name)
	}
	return value, nil
}

// verify 用共享密钥把请求体的 HMAC-SHA256 算一遍，和签名头定时比较。
//
// 源: packages/webhook/webhook-github/src/handler.ts:105-112
//
// 新增: DSH 调 @octokit/webhooks 的 verify。Go 标准库自带 [crypto/hmac]，
// 为这一件事引一个依赖不合算——而且那个库里真正要紧的只有
// [hmac.Equal] 这一句：普通的字节比较会在第一个不同的字节上返回，
// 而那个提前返回的时间差本身就是一条可以一位一位试出签名的信道。
func verify(secret, signature string, body []byte) bool {
	encoded, found := strings.CutPrefix(signature, signaturePrefix)
	if !found {
		return false
	}
	given, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(given, mac.Sum(nil))
}

// composeEvent 把事件名和已认过的请求体拼成 `{"name":…,"payload":…}`。
//
// 源: packages/webhook/webhook-github/src/handler.ts:56-70, 103-108
//
// payload 那一段是**原字节**，一个都不动。解一遍再排回去会磨掉大整数的精度、
// 也会重排键序，而规则完全可能拿它再算一次签名或者按字节比对。
//
// 新增: DSH 那边 event 是一个 `{name, payload}` 对象，`payload` 经过
// snapshotJsonValue 做成冻住的深拷贝。本仓库的
// [webhook.VerifiedDelivery.Event] 是一段 JSON 字节（理由见那个字段），
// 所以同一个形状在这里是拼出来的，而不是构造出来的。
func composeEvent(name string, body []byte) (json.RawMessage, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("%w：不是合法的 UTF-8", ErrMalformedPayload)
	}
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w：顶层不是对象", ErrMalformedPayload)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("%w：不是合法的 JSON", ErrMalformedPayload)
	}
	quoted, err := json.Marshal(name)
	if err != nil {
		return nil, fmt.Errorf("%w：事件名编不出来：%w", ErrMalformedPayload, err)
	}

	var event bytes.Buffer
	event.WriteString(`{"name":`)
	event.Write(quoted)
	event.WriteString(`,"payload":`)
	event.Write(body)
	event.WriteString(`}`)
	return json.RawMessage(event.Bytes()), nil
}
