// 本文件的作用：值那一侧的测试——插值的每一条规矩、空段落的丢弃、快照怎么连起来、
// 以及工具次序那套配置。注册表的测试在 registry_test.go。

package systemprompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ds-harness-go/llm"
)

func text(value string) *string { return &value }

func schema(name string) llm.ToolSchema {
	return llm.ToolSchema{Name: name, Parameters: json.RawMessage(`{"type":"object"}`)}
}

func toolNames(tools []llm.ToolSchema) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestStaticTextIgnoresItsArguments(t *testing.T) {
	got, err := StaticText("固定")(context.Background(), AssembleContext{})
	if err != nil || got != "固定" {
		t.Fatalf("StaticText 交回 (%q, %v)", got, err)
	}
}

func TestRenderPromptDropsEmptySectionsAndJoinsWithBlankLines(t *testing.T) {
	rendered, err := RenderPrompt(PromptAssembly{Sections: []AssembledSection{
		{Name: "a", Text: "第一段"},
		{Name: "empty", Text: ""},
		{Name: "b", Text: "第二段"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "第一段\n\n第二段" {
		t.Fatalf("渲染结果是 %q", rendered)
	}
}

func TestRenderPromptWithNothingAtAllIsEmpty(t *testing.T) {
	rendered, err := RenderPrompt(PromptAssembly{})
	if err != nil || rendered != "" {
		t.Fatalf("空装配渲染出 (%q, %v)", rendered, err)
	}
}

func TestRenderPromptInterpolates(t *testing.T) {
	rendered, err := RenderPrompt(PromptAssembly{
		Sections:  []AssembledSection{{Name: "persona", Text: "你在 {{cwd}} 里干活。"}},
		Variables: map[string]*string{"cwd": text("/srv")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "你在 /srv 里干活。" {
		t.Fatalf("渲染结果是 %q", rendered)
	}
}

func TestInterpolationNeverRescansASubstitutedValue(t *testing.T) {
	// 替换进去的值里那个 {{sneaky}} 必须原样留着，否则一个变量的取值就能注入别的变量。
	rendered, err := RenderPrompt(PromptAssembly{
		Sections:  []AssembledSection{{Name: "s", Text: "{{outer}}"}},
		Variables: map[string]*string{"outer": text("{{sneaky}}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "{{sneaky}}" {
		t.Fatalf("替换进去的值被又扫了一遍：%q", rendered)
	}
}

func TestALoneOpenBraceIsProseOnlyWhenNoCloseFollows(t *testing.T) {
	prose, err := RenderPrompt(PromptAssembly{
		Sections: []AssembledSection{{Name: "s", Text: "写 {{ 就这样结束"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prose != "写 {{ 就这样结束" {
		t.Fatalf("孤零零的 {{ 该原样留着，得到 %q", prose)
	}

	_, err = RenderPrompt(PromptAssembly{
		Sections: []AssembledSection{{Name: "s", Text: "写 {{ 然后后面有个 }} 在别处"}},
	})
	if err == nil || !strings.Contains(err.Error(), "malformed prompt variable reference") {
		t.Fatalf("后面还有 }} 就该判成写坏了，得到 %v", err)
	}
}

func TestMalformedReferences(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"中间有空格", "{{a b}}", "malformed prompt variable reference"},
		{"空名字", "{{}}", "malformed prompt variable reference"},
		{"大写", "{{Name}}", "malformed prompt variable reference"},
		{"数字开头", "{{1a}}", "malformed prompt variable reference"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := RenderPrompt(PromptAssembly{
				Sections: []AssembledSection{{Name: "s", Text: testCase.text}},
			})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("得到 %v", err)
			}
		})
	}
}

func TestMalformedReferenceExcerptDoesNotSplitAMultiByteCharacter(t *testing.T) {
	// 里面那个 `{` 让这一组凑不成引用，后面又还有 `}}`，于是走带摘录的那条诊断。
	// 摘录按字符截，不按字节——否则一段中文正文会在诊断里被劈成半个字。
	_, err := RenderPrompt(PromptAssembly{
		Sections: []AssembledSection{{Name: "s", Text: "{{一二三四五六七八九十一二三四五六七{ 后面 }}"}},
	})
	if err == nil {
		t.Fatal("这该是一次写坏了的引用")
	}
	if !strings.Contains(err.Error(), "{{一二三四五六七八九十一二三四") {
		t.Fatalf("摘录被劈开了：%v", err)
	}
	if strings.ContainsRune(err.Error(), '�') {
		t.Fatalf("诊断里出现了替换字符：%v", err)
	}
}

func TestUnknownVariableListsWhatExists(t *testing.T) {
	_, err := RenderPrompt(PromptAssembly{
		Sections:  []AssembledSection{{Name: "s", Text: "{{missing}}"}},
		Variables: map[string]*string{"beta": text("b"), "alpha": text("a")},
	})
	if err == nil {
		t.Fatal("引用了没注册的变量该报错")
	}
	// 名单排过序，好让同一条诊断每次跑出来都一样。
	if !strings.Contains(err.Error(), "registered variables: alpha, beta") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestUnknownVariableSaysNoneWhenNothingIsRegistered(t *testing.T) {
	_, err := RenderPrompt(PromptAssembly{
		Sections: []AssembledSection{{Name: "s", Text: "{{missing}}"}},
	})
	if err == nil || !strings.Contains(err.Error(), "registered variables: (none)") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestARegisteredVariableWithoutAValueIsItsOwnFailure(t *testing.T) {
	// 「没注册」和「注册了但这次没值」报的不是同一件事，所以 Variables 的值是指针。
	_, err := RenderPrompt(PromptAssembly{
		Sections:  []AssembledSection{{Name: "s", Text: "{{here}}"}},
		Variables: map[string]*string{"here": nil},
	})
	if err == nil || !strings.Contains(err.Error(), "has no value for this assembly") {
		t.Fatalf("诊断是 %v", err)
	}
	if strings.Contains(err.Error(), "unknown prompt variable") {
		t.Fatalf("不该报成不认识的变量：%v", err)
	}
}

func TestContextInterpolationFailuresAreAttributedToTheContext(t *testing.T) {
	_, err := RenderContextSnapshot(PromptAssembly{
		Contexts: []AssembledContext{{Name: "workspace", Text: "{{nope}}"}},
	})
	if err == nil || !strings.Contains(err.Error(), `in context "workspace"`) {
		t.Fatalf("诊断该归到贡献它的那份上下文头上：%v", err)
	}
}

func TestRenderContextSectionsDropsEmptyOnes(t *testing.T) {
	sections, err := RenderContextSections(PromptAssembly{
		Contexts: []AssembledContext{
			{Name: "a", Text: "有内容"},
			{Name: "b", Text: ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].Name != "a" {
		t.Fatalf("结果是 %#v", sections)
	}
}

func TestSnapshotIsEmptyWithoutAnyActiveContext(t *testing.T) {
	snapshot, err := RenderContextSnapshot(PromptAssembly{
		Contexts: []AssembledContext{{Name: "a", Text: ""}},
	})
	if err != nil || snapshot != "" {
		t.Fatalf("空快照渲染出 (%q, %v)", snapshot, err)
	}
	if JoinContextSections(nil) != "" {
		t.Fatal("没有贡献时连出来的该是空串")
	}
}

func TestSnapshotCarriesTheSupersedeSentence(t *testing.T) {
	snapshot, err := RenderContextSnapshot(PromptAssembly{
		Contexts: []AssembledContext{{Name: "a", Text: "甲"}, {Name: "b", Text: "乙"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Current runtime context. This snapshot supersedes earlier runtime-context snapshots.\n\n甲\n\n乙"
	if snapshot != want {
		t.Fatalf("快照是 %q", snapshot)
	}
}

func TestToolOrderRestIsTheDocumentedMarker(t *testing.T) {
	if ToolOrderRest != "<unlisted-tools>" {
		t.Fatalf("rest 标记是 %q", ToolOrderRest)
	}
}

func TestValidateToolOrder(t *testing.T) {
	if err := validateToolOrder(nil); err != nil {
		t.Fatalf("不指定次序该是合法的：%v", err)
	}
	if err := validateToolOrder([]string{"a", ToolOrderRest, "a"}); err == nil ||
		!strings.Contains(err.Error(), "more than once") {
		t.Fatalf("重名该报错，得到 %v", err)
	}
	if err := validateToolOrder([]string{"a"}); err == nil ||
		!strings.Contains(err.Error(), "rest entry") {
		t.Fatalf("漏了 rest 标记该报错，得到 %v", err)
	}
	// 一份明确写出来的空次序不等于 nil：它漏了 rest 标记。
	if err := validateToolOrder([]string{}); err == nil {
		t.Fatal("显式的空次序该被判成配错了")
	}
}

func TestOrderToolsWithoutAConfiguredOrderIsLexicographic(t *testing.T) {
	ordered, err := orderTools(
		[]llm.ToolSchema{schema("write"), schema("bash"), schema("read")}, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(ordered); !equalStrings(got, []string{"bash", "read", "write"}) {
		t.Fatalf("次序是 %v", got)
	}
}

func TestOrderToolsPlacesUnlistedToolsAtTheRestEntry(t *testing.T) {
	known := map[string]struct{}{"write": {}, "bash": {}, "read": {}, "glob": {}}
	ordered, err := orderTools(
		[]llm.ToolSchema{schema("write"), schema("glob"), schema("bash"), schema("read")},
		[]string{"write", ToolOrderRest, "bash"},
		known,
	)
	if err != nil {
		t.Fatal(err)
	}
	// 列出来的按配置就位，没列的按字典序塞进 rest 那个位置。
	if got := toolNames(ordered); !equalStrings(got, []string{"write", "glob", "read", "bash"}) {
		t.Fatalf("次序是 %v", got)
	}
}

func TestOrderToolsKeepsCollectionOrderBetweenNameTwins(t *testing.T) {
	first := schema("dup")
	first.Description = "先来的"
	second := schema("dup")
	second.Description = "后到的"
	ordered, err := orderTools(
		[]llm.ToolSchema{first, second},
		[]string{"dup", ToolOrderRest},
		map[string]struct{}{"dup": {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 || ordered[0].Description != "先来的" {
		t.Fatalf("同名的两份该按收集顺序留着：%#v", ordered)
	}
}

func TestOrderToolsRejectsAnUnregisteredName(t *testing.T) {
	_, err := orderTools(nil, []string{"nope", ToolOrderRest}, map[string]struct{}{"bash": {}})
	if err == nil || !strings.Contains(err.Error(), `unregistered tool "nope"`) {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "known tools: bash") {
		t.Fatalf("诊断该列出认得的工具：%v", err)
	}
}

func TestOrderToolsPluralizesAndSaysNoneWithNothingRegistered(t *testing.T) {
	_, err := orderTools(nil, []string{"a", "b", ToolOrderRest}, nil)
	if err == nil || !strings.Contains(err.Error(), `unregistered tools "a", "b"`) {
		t.Fatalf("多于一个时该用复数：%v", err)
	}
	if !strings.Contains(err.Error(), "known tools: (none)") {
		t.Fatalf("一个都没注册时该说 (none)：%v", err)
	}
}

func TestOrderToolsRejectsTheReservedName(t *testing.T) {
	_, err := orderTools([]llm.ToolSchema{schema(ToolOrderRest)}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "reserved tool name") {
		t.Fatalf("诊断是 %v", err)
	}
	// 没配置次序的那条路也得挡住它。
	_, err = orderTools([]llm.ToolSchema{schema(ToolOrderRest)}, []string{ToolOrderRest}, nil)
	if err == nil {
		t.Fatal("配了次序也一样不许报出保留名")
	}
}

func TestTruncateRunesKeepsShortTextIntact(t *testing.T) {
	if got := truncateRunes("abc", 16); got != "abc" {
		t.Fatalf("短文本被动过了：%q", got)
	}
}
