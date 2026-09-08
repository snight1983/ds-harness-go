// 本文件验摘录切得对：短的原样交出，长的围着命中处切一段，两头该补记号就补。

package searchstore

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
)

func Test摘录短的原样交出(t *testing.T) {
	if got := snippet("一句短话", "短话", 240); got != "一句短话" {
		t.Errorf("装得下就不该切：%q", got)
	}
}

func Test摘录把空白折起来(t *testing.T) {
	if got := snippet("上面\n\n下面   还有", "下面", 240); got != "上面 下面 还有" {
		t.Errorf("连续空白该折成一个空格：%q", got)
	}
}

func Test摘录围着命中处切(t *testing.T) {
	body := strings.Repeat("前", 500) + "命中" + strings.Repeat("后", 500)

	got := snippet(body, "命中", 20)
	if !strings.Contains(got, "命中") {
		t.Fatalf("切出来的一段里没有命中处：%q", got)
	}
	if !strings.HasPrefix(got, ellipsis) || !strings.HasSuffix(got, ellipsis) {
		t.Errorf("两头都被切掉了，该补上记号：%q", got)
	}
	if body := strings.Trim(got, ellipsis); utf8.RuneCountInString(body) != 20 {
		t.Errorf("切出来的一段不是 20 个字：%q", body)
	}
}

func Test摘录切在头上就不补前面那个记号(t *testing.T) {
	body := "命中" + strings.Repeat("后", 500)

	got := snippet(body, "命中", 20)
	if strings.HasPrefix(got, ellipsis) {
		t.Errorf("前面没被切掉，不该补记号：%q", got)
	}
	if !strings.HasSuffix(got, ellipsis) {
		t.Errorf("后面被切掉了，该补记号：%q", got)
	}
}

func Test摘录对不上就从头切(t *testing.T) {
	body := strings.Repeat("话", 500)

	got := snippet(body, "对不上的词", 20)
	if strings.HasPrefix(got, ellipsis) {
		t.Errorf("对不上就该从头切：%q", got)
	}
	if body := strings.Trim(got, ellipsis); utf8.RuneCountInString(body) != 20 {
		t.Errorf("切出来的一段不是 20 个字：%q", body)
	}
}

func Test摘录切出来仍是合法的文字(t *testing.T) {
	body := strings.Repeat("中", 500)

	got := snippet(body, "中", 20)
	if !utf8.ValidString(got) {
		t.Errorf("切出来的不是合法的 UTF-8：%q", got)
	}
}

func Test检索交出来的摘录被截住(t *testing.T) {
	store := newFakeStore()
	store.put(testHeader("s1", 100), "r1",
		logOf(t, strings.Repeat("前", 300)+"配置"+strings.Repeat("后", 300)))
	search := newStoreWith(t, Config{
		Live: newFakeLive(), Persistence: store, SnippetChars: 40,
	})

	page, err := search.SearchSessions(t.Context(), sessionquery.SearchRequest{Query: "配置"})
	if err != nil {
		t.Fatalf("检索不该失败：%v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("该有一条命中：%d 条", len(page.Items))
	}
	got := page.Items[0].BestMatch.Snippet
	if !strings.Contains(got, "配置") {
		t.Errorf("摘录里没有命中处：%q", got)
	}
	if utf8.RuneCountInString(got) > 40+2*utf8.RuneCountInString(ellipsis) {
		t.Errorf("摘录没被截住：%d 个字", utf8.RuneCountInString(got))
	}
}
