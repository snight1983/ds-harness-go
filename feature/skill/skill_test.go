// 本文件的作用：值那一侧的测试——名字的文法、调用许可的两个投影，以及一份技能
// 渲染成模型看的那个块之后长什么样。注册表的测试在 registry_test.go。

package skill

import (
	"strings"
	"testing"
)

func TestIsName(t *testing.T) {
	valid := []string{"a", "a1", "code-review", "a-b-c", "9", "x9-y0"}
	for _, name := range valid {
		if !IsName(name) {
			t.Errorf("%q 应该是合法技能名", name)
		}
	}
	invalid := []string{"", "-a", "a-", "a--b", "A", "a_b", "a b", "a.b", "中文"}
	for _, name := range invalid {
		if IsName(name) {
			t.Errorf("%q 不该是合法技能名", name)
		}
	}
}

func TestInvocationProjections(t *testing.T) {
	both := Summary{Invocation: InvocationPolicy{ModelInvocable: true, UserInvocable: true}}
	if !IsModelInvocable(both) || !IsUserInvocable(both) {
		t.Fatal("两条都放行时两个投影都该为真")
	}
	// 零值是两条都关：这正是 [Registration].Invocation 必须是指针的理由。
	var none Summary
	if IsModelInvocable(none) || IsUserInvocable(none) {
		t.Fatal("零值应该对谁都不放行")
	}
}

func TestResourceBaseKinds(t *testing.T) {
	cases := []struct {
		base ResourceBase
		want ResourceBaseKind
	}{
		{DirectoryBase{Path: "/tmp"}, ResourceBaseDirectory},
		{URLBase{URL: "https://example.test/"}, ResourceBaseURL},
		{OpaqueBase{Description: "在那台注册中心里"}, ResourceBaseOpaque},
	}
	for _, testCase := range cases {
		if got := testCase.base.ResourceBaseKind(); got != testCase.want {
			t.Errorf("%T 的判别标签是 %q，应该是 %q", testCase.base, got, testCase.want)
		}
		testCase.base.sealedResourceBase()
	}
}

func TestRenderContentWithoutAResourceBase(t *testing.T) {
	rendered := RenderContent(Definition{
		Summary: Summary{Name: "code-review", Provider: "remote"},
		Content: "照这么做。",
	})
	want := strings.Join([]string{
		`<skill_content name="code-review">`,
		"<skill_resources>",
		`Resources for this skill are managed by provider "remote".`,
		"Load referenced resources only as needed.",
		"</skill_resources>",
		"",
		"<skill_instructions>",
		"照这么做。",
		"</skill_instructions>",
		"</skill_content>",
	}, "\n")
	if rendered != want {
		t.Fatalf("渲染结果不对：\n%s\n应该是：\n%s", rendered, want)
	}
}

func TestRenderContentResourceHints(t *testing.T) {
	cases := []struct {
		name string
		base ResourceBase
		want string
	}{
		{"目录", DirectoryBase{Path: "/skills/x"}, "Base directory for this skill: /skills/x"},
		{"URL", URLBase{URL: "https://example.test/x"}, "Base URL for this skill: https://example.test/x"},
		{"一句话", OpaqueBase{Description: "在 <那台> 注册中心里"}, "Resources for this skill: 在 &lt;那台&gt; 注册中心里"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := RenderContent(Definition{
				Summary: Summary{Name: "x", Provider: "p", ResourceBase: testCase.base},
				Content: "正文",
			})
			if !strings.Contains(rendered, testCase.want) {
				t.Fatalf("渲染结果里没有 %q：\n%s", testCase.want, rendered)
			}
			if !strings.Contains(rendered, "Load referenced resources only as needed.") {
				t.Fatal("每一种基址后面都该跟着那句按需加载")
			}
		})
	}
}

// unknownBase 是一个本包之外造不出来、但 type switch 仍然得兜住的基址。
type unknownBase struct{}

func (unknownBase) ResourceBaseKind() ResourceBaseKind { return "unknown" }
func (unknownBase) sealedResourceBase()                {}

func TestRenderContentFallsBackForAnUnknownBase(t *testing.T) {
	// [ResourceBase] 是封闭的，所以这一支在包外走不到；但 Go 的 type switch 没有
	// 穷尽性检查，兜底那一支仍然得是一份**可用**的提示，而不是崩掉。
	rendered := RenderContent(Definition{
		Summary: Summary{Name: "x", Provider: "p", ResourceBase: unknownBase{}},
		Content: "正文",
	})
	if !strings.Contains(rendered, `Resources for this skill are managed by provider "p".`) {
		t.Fatalf("认不出基址时该退回最保守的那份提示：\n%s", rendered)
	}
}

func TestRenderContentEscapesTheName(t *testing.T) {
	// 一份技能的名字和描述不许开得了或者闭得了框架标签。
	rendered := RenderContent(Definition{
		Summary: Summary{Name: `a"<&b`, Provider: "p"},
		Content: "正文",
	})
	if !strings.Contains(rendered, `<skill_content name="a&quot;&lt;&amp;b">`) {
		t.Fatalf("名字没转义：\n%s", rendered)
	}
}

func TestEscapeText(t *testing.T) {
	if got := EscapeText("</available_skills> & <x>"); got != "&lt;/available_skills&gt; &amp; &lt;x&gt;" {
		t.Fatalf("转义结果是 %q", got)
	}
}

func TestInvocationSourceCarriesTheName(t *testing.T) {
	// 这个类型只是一份约定的形状：注入方把它排成 llm.PluginSource.Extra 那份不透明 JSON。
	if (InvocationSource{Name: "code-review"}).Name != "code-review" {
		t.Fatal("调起来源该带着技能名")
	}
}
