// 本文件的作用：会话快照那条规范 URI 怎么编、怎么解，以及一段主机文本里的
// 提及怎么抽出来、原地换成人能读的样子。
//
// 源: packages/context/session-reference/src/uri.ts:1-102

package sessionref

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"

	"ds-harness-go/session"
)

// Scheme 是留给会话快照的 URI 方案，冒号包含在内。
//
// 源: packages/context/session-reference/src/uri.ts:7-8
const Scheme = "dsh-session:"

// EncodeURI 把一个会话 id 编成规范 URI。
//
// 源: packages/context/session-reference/src/uri.ts:10-18
//
// 先 JSON 再 base64url 而不是直接 base64url 那个字符串：会话 id 是不透明的，
// 里面可以有任何字节；JSON 那一层保证解回来的一定是一个字符串，
// 而不是某段字节碰巧拼出来的别的东西。
func EncodeURI(sessionID session.SessionID) string {
	payload, err := json.Marshal(string(sessionID))
	if err != nil {
		// 一个 Go 字符串永远编得成 JSON——非法 UTF-8 会被换成替换字符，
		// 不会报错。留着这一支只是因为 json.Marshal 的签名带 error。
		payload = []byte(`""`)
	}
	return Scheme + base64.RawURLEncoding.EncodeToString(payload)
}

// base64URLPayload 是 base64url 载荷允许出现的那套字符。
//
// 源: packages/context/session-reference/src/uri.ts:24
var base64URLPayload = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// DecodeURI 解一条规范 URI，并顺带确认它**就是**规范写法。
//
// 源: packages/context/session-reference/src/uri.ts:20-40
//
// 最后那一步「重新编一遍看是不是同一条」不是多余的：base64 有一批
// 非规范写法（补位、字母表混用）解出来是同一段字节。不钉死规范写法的话，
// 同一个会话就有多条 URI 指得到它，而去重、自引用检查全都按字符串比。
func DecodeURI(uri string) (session.SessionID, error) {
	if !strings.HasPrefix(uri, Scheme) {
		return "", invalidURI(uri, nil)
	}
	payload := uri[len(Scheme):]
	if !base64URLPayload.MatchString(payload) {
		return "", invalidURI(uri, nil)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", invalidURI(uri, err)
	}
	var parsed any
	if err := json.Unmarshal(decoded, &parsed); err != nil {
		return "", invalidURI(uri, err)
	}
	text, ok := parsed.(string)
	if !ok {
		return "", invalidURI(uri, nil)
	}
	sessionID := session.SessionID(text)
	if EncodeURI(sessionID) != uri {
		return "", invalidURI(uri, nil)
	}
	return sessionID, nil
}

// FormatMention 渲染一段与主机无关的 Markdown 提及，里面带着规范 URI。
//
// 源: packages/context/session-reference/src/uri.ts:42-50
func FormatMention(reference Input) string {
	label := reference.Label
	if label == "" {
		label = string(reference.SessionID)
	}
	return "@[" + escapeLabel(label) + "](" + EncodeURI(reference.SessionID) + ")"
}

// ParsedText 是从一段纯文本里抽提及的结果。
//
// 源: packages/context/session-reference/src/uri.ts:52-58
type ParsedText struct {
	// Text 是把那些不透明记号换成可读的 `@label` 之后的文本。
	Text string
	// References 是按出现顺序排好的引用，本包的去重还没做。
	References []Input
}

// mentionPattern 认两种写法：显式的 Markdown 提及，和裸的规范 URI。
//
// 源: packages/context/session-reference/src/uri.ts:70
//
// 新增: DSH 那条正则里的标签部分是 `(?:\\.|[^\\\]])*`——反斜杠转义或者
// 任意一个不是反斜杠也不是右方括号的字符。Go 的 regexp 是 RE2，不支持
// 回溯，但这个子表达式本来就不需要回溯，逐字搬过来就是对的。
// `\\.` 在 RE2 里的 `.` 默认不跨行，而 DSH 那边也没开 `s` 标志，两边一致。
var mentionPattern = regexp.MustCompile(
	`@\[((?:\\.|[^\\\]])*)\]\((dsh-session:[^\s)]*)\)|(dsh-session:[A-Za-z0-9_-]+)`)

// ParseText 从一段文本里抽出 Markdown 提及和裸的规范 URI。
//
// 源: packages/context/session-reference/src/uri.ts:60-88
//
// 显式的 Markdown 提及碰上任何一条不规范的 URI 都直接失败：那是主机自己
// 拼出来的东西，拼错了要让它知道。裸文本要先长得像一个非空的 base64url 载荷
// 才当成引用，但一旦当成了，不规范同样失败——一段随口写的
// `dsh-session:` 不会被当成引用，而一段看着像引用的东西不许悄悄放过去。
func ParseText(text string) (ParsedText, error) {
	var references []Input
	var failure error
	rendered := replaceAllSubmatchFunc(mentionPattern, text, func(groups []string) string {
		if failure != nil {
			return groups[0]
		}
		uri := groups[2]
		if uri == "" {
			uri = groups[3]
		}
		sessionID, err := DecodeURI(uri)
		if err != nil {
			failure = err
			return groups[0]
		}
		label := string(sessionID)
		if groups[2] != "" {
			// 只有 Markdown 那一支才有标签；裸 URI 用会话 id 当标签。
			label = unescapeLabel(groups[1])
		}
		references = append(references, Input{SessionID: sessionID, Label: label})
		return "@" + label
	})
	if failure != nil {
		return ParsedText{}, failure
	}
	return ParsedText{Text: rendered, References: references}, nil
}

// replaceAllSubmatchFunc 是 Regexp.ReplaceAllStringFunc 的带分组版本。
//
// 新增: 标准库那个只把整段匹配交给回调，而这里要按「匹配的是哪一支」
// 分开处理，非拿到分组不可。JS 的 String.replace 天生把分组传给回调，
// Go 没有对应物，所以在这里补一个。
//
// groups[0] 是整段匹配，之后依次是各个分组；没参与匹配的分组是空串。
func replaceAllSubmatchFunc(pattern *regexp.Regexp, text string, replace func(groups []string) string) string {
	matches := pattern.FindAllStringSubmatchIndex(text, -1)
	if matches == nil {
		return text
	}
	var builder strings.Builder
	last := 0
	for _, match := range matches {
		groups := make([]string, len(match)/2)
		for index := range groups {
			start, end := match[2*index], match[2*index+1]
			if start >= 0 {
				groups[index] = text[start:end]
			}
		}
		builder.WriteString(text[last:match[0]])
		builder.WriteString(replace(groups))
		last = match[1]
	}
	builder.WriteString(text[last:])
	return builder.String()
}

// labelEscapes 是标签里要转义的那两个字符：反斜杠自己，和会提前收尾的右方括号。
//
// 源: packages/context/session-reference/src/uri.ts:90-92
var labelEscapes = strings.NewReplacer(`\`, `\\`, `]`, `\]`)

// escapeLabel 把标签里的反斜杠和右方括号转义掉。
//
// 源: packages/context/session-reference/src/uri.ts:90-92
func escapeLabel(label string) string { return labelEscapes.Replace(label) }

// unescapeLabel 把 `\x` 还原成 `x`。
//
// 源: packages/context/session-reference/src/uri.ts:94-96
//
// 新增: DSH 是 `label.replace(/\\(.)/gu, '$1')`。这里手写一趟扫描而不是
// 用正则替换：RE2 的 `.` 按 UTF-8 走，遇到非法字节会跳过而不是匹配，
// 于是一段坏字节里的转义就还原不回来了。逐字节扫描没有这个问题，
// 而标签是主机给的文本，坏字节是有可能的。
func unescapeLabel(label string) string {
	if !strings.Contains(label, `\`) {
		return label
	}
	var builder strings.Builder
	builder.Grow(len(label))
	for index := 0; index < len(label); index++ {
		if label[index] == '\\' && index+1 < len(label) {
			index++
		}
		builder.WriteByte(label[index])
	}
	return builder.String()
}

// invalidURI 是「这条 URI 不成立」那句话，出现的地方太多，收成一处。
//
// 源: packages/context/session-reference/src/uri.ts:98-102
func invalidURI(uri string, cause error) *Error {
	if cause == nil {
		return fail(CodeInvalidReference, "会话引用 URI 不合法：%q", uri)
	}
	return wrap(CodeInvalidReference, cause, "会话引用 URI 不合法：%q", uri)
}
