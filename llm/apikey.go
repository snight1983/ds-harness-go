// 本文件的作用：一个提供方 API 密钥「长得对不对」的唯一判据，凡是要把密钥塞进
// HTTP 头的适配器都用它。
//
// 源: packages/llm/llm/src/api-key.ts:1-41
// 源: packages/llm/llm/src/index.ts:138-161
//
// 新增: DSH 把 assertUsableApiKey 放在 index.ts 而不是 api-key.ts，明写的理由是
// 「让那个判据模块保持零依赖」——它要用 LlmError，而 LlmError 在 index.ts 里。
// Go 这边两样东西同在 llm 包内，那个理由整个不存在，所以判据和它唯一的诊断放在
// 一起。

package llm

import (
	"fmt"
	"strings"
)

// APIKeyRejection 说明一个交进来的密钥为什么用不了。
//
// 源: packages/llm/llm/src/api-key.ts:18
type APIKeyRejection string

const (
	// APIKeyEmpty 表示去掉首尾空白之后什么都不剩。
	APIKeyEmpty APIKeyRejection = "empty"
	// APIKeyIllegalCharacters 表示里面有 HTTP 头带不了的字符。
	APIKeyIllegalCharacters APIKeyRejection = "illegalCharacters"
)

// APIKeyCheck 是对一个交进来的密钥的裁定。
//
// 源: packages/llm/llm/src/api-key.ts:21-23
//
// 新增: DSH 是一个判别联合（ok:true 带 value，ok:false 带 reason）。Go 这边是
// 一个结构体加一位 OK：这个联合只有两支、两支的载荷各只有一个字段，用封闭接口
// 那一套（见本包 [ContentBlock]）换不到任何东西，反而让调用方多写一次类型断言。
// OK 为真时 Reason 是空串，为假时 Value 是空串。
type APIKeyCheck struct {
	// OK 为真表示这个密钥能用，Value 是去掉首尾空白之后的那一份。
	OK bool
	// Value 是可以直接塞进 HTTP 头的那份密钥；OK 为假时是空串。
	Value string
	// Reason 说明为什么用不了；OK 为真时是空串。
	Reason APIKeyRejection
}

// legalAPIKeyRune 判一个字符是不是 HTTP 头能原样带走、而且各家提供方确实在用的
// 那一类：可打印 ASCII，不含空格。
//
// 源: packages/llm/llm/src/api-key.ts:15
//
// 出了这个集合的密钥根本到不了任何提供方——fetch 那一层就拼不出这个头，所以这是
// 一条**传输层**的不变量，不是某一家提供方的政策。Latin-1 是**故意**排除的：
// 头理论上带得动，但没有哪家提供方签发这种密钥，放它进来只会把一次本地的、
// 说得清原因的拒绝，换成一个看不懂的 401。
//
// 新增: DSH 写的是正则 /^[\x21-\x7E]+$/。Go 这边是一个逐字符的判据：这个集合
// 全在 ASCII 里，一次 range 就是逐字节，比起一个正则少一次编译、也少一层间接。
func legalAPIKeyRune(character rune) bool {
	return character >= 0x21 && character <= 0x7E
}

// NormalizeAPIKey 裁定一个**交进来的** API 密钥，先去掉首尾空白。
//
// 源: packages/llm/llm/src/api-key.ts:36-41
//
// 去空白这件事是不声张的：一个前后带了空格的密钥只有一种读法。除此之外每一种
// 毛病都要报出来。「压根没给」是这个函数永远见不到的一种配置状态——一份没点名
// 任何凭据的档案走的是提供方自己那套环境发现或者 OAuth——所以「到底给没给」
// 由调用方在问之前自己判。
func NormalizeAPIKey(raw string) APIKeyCheck {
	value := strings.TrimSpace(raw)
	if value == "" {
		return APIKeyCheck{Reason: APIKeyEmpty}
	}
	for _, character := range value {
		if !legalAPIKeyRune(character) {
			return APIKeyCheck{Reason: APIKeyIllegalCharacters}
		}
	}
	return APIKeyCheck{OK: true, Value: value}
}

// AssertUsableAPIKey 收下一份凭据，或者说清它为什么用不了。
//
// 源: packages/llm/llm/src/index.ts:138-161
//
// 存下来的密钥从凭据接缝、一行 .env、或者一句 shell export 过来，这几条路都会
// 顺手带上首尾空白，所以去空白是不声张的。除此之外的毛病在这里就拦下，而不是留到
// HTTP 那一层——那边拒起来只会点出一个 UTF-16 码位，不会告诉人该去改哪个设置。
//
// 密钥本身**一个字符都不进这句诊断**：ref 说的是「去哪儿改」，而把一份秘密的任何
// 一段回显进日志或者界面，正是这句诊断要避开的那次失败。
//
// pkg 是发出拒绝的那个包名，缀在诊断最前面；ref 是这个值解算时走过的那个凭据引用。
//
// 新增: 诊断里点名网页 Models 页面，是因为它**通常**是写下这个值的那一方，不是
// 唯一一方——同一个值也可能来自手改的 .env 或者一句 export，而那种部署里可能
// 根本没挂凭据接缝，把人指到一个它不提供的页面上就是条死路。这句括号里的话
// 照抄 DSH，理由也照抄。
func AssertUsableAPIKey(raw, pkg, ref string) (string, error) {
	checked := NormalizeAPIKey(raw)
	if checked.OK {
		return checked.Value, nil
	}
	var message string
	if checked.Reason == APIKeyEmpty {
		message = fmt.Sprintf(
			"%s: the API key resolved from %s is blank; set %s to the raw key"+
				" (the web Models page writes it) or export it in the launching environment",
			pkg, ref, ref)
	} else {
		message = fmt.Sprintf(
			"%s: the API key resolved from %s contains characters no HTTP header can carry;"+
				" set %s to the raw key alone (the web Models page writes it)",
			pkg, ref, ref)
	}
	return "", NewError(message, InvalidCredentialCode, nil)
}
