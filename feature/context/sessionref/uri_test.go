// 本文件的作用：验规范 URI 的编解码、提及的渲染，以及从一段主机文本里抽提及。

package sessionref

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestEncodeURI往返回原来那个会话id(t *testing.T) {
	for _, id := range []sessionlog.SessionID{
		"简单",
		"带 空格 和 中文",
		"a/b:c?d#e",
		`带"引号"和\反斜杠`,
		"",
	} {
		uri := EncodeURI(id)
		if !strings.HasPrefix(uri, Scheme) {
			t.Fatalf("URI %q 没有以 %q 开头", uri, Scheme)
		}
		decoded, err := DecodeURI(uri)
		if err != nil {
			t.Fatalf("解 %q 失败：%v", uri, err)
		}
		if decoded != id {
			t.Fatalf("往返之后是 %q，原来是 %q", decoded, id)
		}
	}
}

func TestEncodeURI的空id仍然编得出可解的载荷(t *testing.T) {
	// 空会话 id 编出来是 `""` 的 base64url，不是空载荷——空载荷过不了
	// [base64URLPayload] 那道非空检查。
	uri := EncodeURI("")
	if uri == Scheme {
		t.Fatal("空 id 编出了一条空载荷的 URI")
	}
}

func TestDecodeURI拒绝一切不规范的写法(t *testing.T) {
	// 同一段字节的非规范 base64 写法（带补位）解出来一样，但不是规范写法。
	padded := Scheme + base64.StdEncoding.EncodeToString([]byte(`"ab"`))
	// 载荷是合法 base64url，解出来却不是一个 JSON 字符串。
	notString := Scheme + base64.RawURLEncoding.EncodeToString([]byte(`{"a":1}`))
	// 载荷是合法 base64url，解出来根本不是 JSON。
	notJSON := Scheme + base64.RawURLEncoding.EncodeToString([]byte(`{{{`))

	for name, uri := range map[string]string{
		"方案不对":     "other:AAAA",
		"没有载荷":     Scheme,
		"载荷里有非法字符": Scheme + "AA*A",
		// 字符全合法，长度却不是一个 base64 能有的长度：过得了那道正则，
		// 过不了解码。
		"载荷长度不成立":    Scheme + "A",
		"带补位的非规范写法":  padded,
		"解出来不是字符串":   notString,
		"解出来不是 JSON": notJSON,
	} {
		if _, err := DecodeURI(uri); !errors.Is(err, CodeInvalidReference) {
			t.Fatalf("%s：%q 应当被拒，得到 %v", name, uri, err)
		}
	}
}

func TestDecodeURI的载荷解得开但重编不一致时被拒(t *testing.T) {
	// `"a"` 前面补一串空白，JSON 解出来还是 "a"，但重新编一遍不是这条 URI。
	loose := Scheme + base64.RawURLEncoding.EncodeToString([]byte(` "a" `))
	if _, err := DecodeURI(loose); !errors.Is(err, CodeInvalidReference) {
		t.Fatalf("宽松写法应当被拒，得到 %v", err)
	}
}

func TestFormatMention没给标签时退回会话id(t *testing.T) {
	mention := FormatMention(Input{SessionID: "abc"})
	if want := "@[abc](" + EncodeURI("abc") + ")"; mention != want {
		t.Fatalf("提及是 %q，要的是 %q", mention, want)
	}
}

func TestFormatMention转义标签里的反斜杠和右方括号(t *testing.T) {
	mention := FormatMention(Input{SessionID: "abc", Label: `一] 二\ 三`})
	if !strings.Contains(mention, `一\] 二\\ 三`) {
		t.Fatalf("标签没被转义：%q", mention)
	}
	// 转义过的提及要能被自己解回来，标签一个字不差。
	parsed, err := ParseText(mention)
	if err != nil {
		t.Fatalf("解自己渲染的提及失败：%v", err)
	}
	if len(parsed.References) != 1 || parsed.References[0].Label != `一] 二\ 三` {
		t.Fatalf("标签没还原回来：%+v", parsed.References)
	}
}

func TestParseText把提及换成可读的at标签(t *testing.T) {
	text := "看看 " + FormatMention(Input{SessionID: "s1", Label: "上一次调研"}) + " 里说的"
	parsed, err := ParseText(text)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if want := "看看 @上一次调研 里说的"; parsed.Text != want {
		t.Fatalf("渲染出来是 %q，要的是 %q", parsed.Text, want)
	}
	if len(parsed.References) != 1 {
		t.Fatalf("抽出来 %d 条引用，要的是 1 条", len(parsed.References))
	}
	if parsed.References[0].SessionID != "s1" || parsed.References[0].Label != "上一次调研" {
		t.Fatalf("引用不对：%+v", parsed.References[0])
	}
}

func TestParseText认裸的规范URI并拿会话id当标签(t *testing.T) {
	uri := EncodeURI("s2")
	parsed, err := ParseText("对比一下 " + uri + " 那边")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if want := "对比一下 @s2 那边"; parsed.Text != want {
		t.Fatalf("渲染出来是 %q，要的是 %q", parsed.Text, want)
	}
	if len(parsed.References) != 1 || parsed.References[0].Label != "s2" {
		t.Fatalf("裸 URI 的标签不对：%+v", parsed.References)
	}
}

func TestParseText按出现顺序抽出多条并保留重复(t *testing.T) {
	text := FormatMention(Input{SessionID: "a"}) + " 和 " +
		FormatMention(Input{SessionID: "b"}) + " 还有 " +
		FormatMention(Input{SessionID: "a"})
	parsed, err := ParseText(text)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 去重是 [normalizeReferences] 的事，这一层原样交出来。
	ids := []sessionlog.SessionID{}
	for _, reference := range parsed.References {
		ids = append(ids, reference.SessionID)
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "a" {
		t.Fatalf("顺序或重复没保住：%v", ids)
	}
}

func TestParseText没有提及时原样返回(t *testing.T) {
	parsed, err := ParseText("一段没有任何提及的话")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.Text != "一段没有任何提及的话" || parsed.References != nil {
		t.Fatalf("原样返回没做到：%+v", parsed)
	}
}

func TestParseText不把随口写的方案名当成引用(t *testing.T) {
	// 裸文本要先长得像一个非空的 base64url 载荷才当成引用。
	parsed, err := ParseText("这条 URI 的方案是 dsh-session: 开头的")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(parsed.References) != 0 {
		t.Fatalf("不该抽出引用：%+v", parsed.References)
	}
}

func TestParseText碰上不规范的URI就失败(t *testing.T) {
	for name, text := range map[string]string{
		"Markdown 提及里的 URI 坏了": "@[标签](dsh-session:@@@)",
		"裸 URI 长得像但不规范":        "dsh-session:AAAA",
	} {
		if _, err := ParseText(text); !errors.Is(err, CodeInvalidReference) {
			t.Fatalf("%s：应当失败，得到 %v", name, err)
		}
	}
}

func TestParseText第一条失败之后后面的原样留着(t *testing.T) {
	// 失败之后的回调直接把原文送回去，不再解析——这里只验它确实报了错，
	// 且没有把先抽到的那条引用交出来。
	text := FormatMention(Input{SessionID: "good"}) + " @[坏的](dsh-session:@@@) " +
		FormatMention(Input{SessionID: "later"})
	parsed, err := ParseText(text)
	if !errors.Is(err, CodeInvalidReference) {
		t.Fatalf("应当失败，得到 %v", err)
	}
	if parsed.References != nil || parsed.Text != "" {
		t.Fatalf("失败时不该交出半份结果：%+v", parsed)
	}
}

func TestUnescapeLabel在坏字节上也能还原转义(t *testing.T) {
	// 逐字节扫描而不是用正则：一段非法 UTF-8 里的转义也要还原得回来。
	got := unescapeLabel("\xff\\]尾")
	if want := "\xff]尾"; got != want {
		t.Fatalf("还原出来是 %q，要的是 %q", got, want)
	}
}

func TestUnescapeLabel末尾孤零零一个反斜杠原样留着(t *testing.T) {
	if got := unescapeLabel(`abc\`); got != `abc\` {
		t.Fatalf("还原出来是 %q，要的是 %q", got, `abc\`)
	}
}

func TestUnescapeLabel没有反斜杠时走快路(t *testing.T) {
	if got := unescapeLabel("干净的标签"); got != "干净的标签" {
		t.Fatalf("还原出来是 %q", got)
	}
}
