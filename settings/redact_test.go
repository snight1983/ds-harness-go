// 本文件验密钥摘除：摘了哪些、记了哪些位置、没摘到的照原样留着。
//
// 源: packages/settings/settings/tests/redact.spec.ts:22-104

package settings

import (
	"slices"
	"strings"
	"testing"
)

// redactProvider 是夹具类型里的一个字典条目 / 数组元素。
type redactProvider struct {
	APIKey    string `json:"apiKey" settings:"secret"`
	APIKeyEnv string `json:"apiKeyEnv"`
	BaseURL   string `json:"baseURL"`
}

// redactNested 用来验一个没有值的结构体字段照样会被列成密钥槽位。
type redactNested struct {
	Token string `json:"token" settings:"secret"`
}

// redactAdapter 是脱敏夹具类型，三种容器各占一个字段。
//
// 源: packages/settings/settings/tests/redact.spec.ts:9-18
type redactAdapter struct {
	APIKey    string                    `json:"apiKey" settings:"secret"`
	Providers map[string]redactProvider `json:"providers"`
	Fallbacks []redactProvider          `json:"fallbacks"`
	Nested    redactNested              `json:"nested"`
}

// TestRedactStripsSecretsFromEveryContainer 钉住三种容器都走得到，且每个位置都记了下来。
//
// 源: packages/settings/settings/tests/redact.spec.ts:23-48
func TestRedactStripsSecretsFromEveryContainer(t *testing.T) {
	t.Parallel()

	result := Redact[redactAdapter](map[string]any{
		"apiKey": "top-secret",
		"providers": map[string]any{
			"openai":    map[string]any{"apiKey": "sk-live", "apiKeyEnv": "OPENAI_API_KEY", "baseURL": "https://x"},
			"anthropic": map[string]any{"apiKeyEnv": "ANTHROPIC_API_KEY"},
		},
		"fallbacks": []any{map[string]any{"apiKey": "fb", "baseURL": "https://y"}},
		"nested":    map[string]any{},
	})

	want := map[string]any{
		"providers": map[string]any{
			"openai":    map[string]any{"apiKeyEnv": "OPENAI_API_KEY", "baseURL": "https://x"},
			"anthropic": map[string]any{"apiKeyEnv": "ANTHROPIC_API_KEY"},
		},
		"fallbacks": []any{map[string]any{"baseURL": "https://y"}},
		"nested":    map[string]any{},
	}
	if !DeepEqualJSON(result.Value, want) {
		t.Fatalf("摘完的值 %#v，该是 %#v", result.Value, want)
	}

	assertSecrets(t, result.Secrets, []Secret{
		{Path: []string{"apiKey"}, Set: true},
		{Path: []string{"providers", "openai", "apiKey"}, Set: true},
		{Path: []string{"providers", "anthropic", "apiKey"}, Set: false},
		{Path: []string{"fallbacks", "0", "apiKey"}, Set: true},
		{Path: []string{"nested", "token"}, Set: false},
	})
}

// TestRedactEnumeratesUnsetStructSlotsWithoutInventingContainers 钉住两条相反的规则同时成立。
//
// 源: packages/settings/settings/tests/redact.spec.ts:50-57
//
// **结构体字段一律列出来**，哪怕整个值都缺席——配置界面要靠它知道这个格子存在，
// 才画得出那个只写输入框。而**字典和数组不凭空造条目**：它们的键和下标由值决定，
// 值不在就没有位置可言，造出来的槽位会让界面渲染出一个不存在的条目。
func TestRedactEnumeratesUnsetStructSlotsWithoutInventingContainers(t *testing.T) {
	t.Parallel()

	result := Redact[redactAdapter](nil)

	if result.Value != nil {
		t.Fatalf("缺席的值摘完还该是缺席，实际 %#v", result.Value)
	}
	assertSecrets(t, result.Secrets, []Secret{
		{Path: []string{"apiKey"}, Set: false},
		{Path: []string{"nested", "token"}, Set: false},
	})
}

// TestRedactNeverMutatesTheInputAndPreservesUndeclaredKeys 钉住两件事。
//
// 源: packages/settings/settings/tests/redact.spec.ts:59-68
//
// 保留未声明的键那一条是有具体后果的：存下来的文档可能带着一个已经删掉的旧字段，
// 或者一个更新版本才认识的新字段。顺手删掉它们会让一次降级运行把用户的配置吃掉。
func TestRedactNeverMutatesTheInputAndPreservesUndeclaredKeys(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"apiKey": "live",
		"extra":  map[string]any{"keep": true},
	}
	result := Redact[redactAdapter](input)

	if input["apiKey"] != "live" {
		t.Fatalf("输入被改了：%#v", input)
	}
	value, _ := result.Value.(map[string]any)
	if _, leaked := value["apiKey"]; leaked {
		t.Fatalf("密钥没摘干净：%#v", value)
	}
	if !DeepEqualJSON(value["extra"], map[string]any{"keep": true}) {
		t.Fatalf("未声明的键该原样留着，实际 %#v", value["extra"])
	}
}

// TestRedactPassesMalformedContainersThrough 钉住形状不对的容器原样穿过去。
//
// 源: packages/settings/settings/tests/redact.spec.ts:70-80
//
// 走不进去就不改：摘除这一趟的职责是摘密钥，不是替类型做裁剪。
// 把一个形状不对的值改写成别的样子，只会让「存的到底是什么」再也查不清。
func TestRedactPassesMalformedContainersThrough(t *testing.T) {
	t.Parallel()

	result := Redact[redactAdapter](map[string]any{
		"providers": "not-a-dict",
		"fallbacks": "not-an-array",
	})

	want := map[string]any{"providers": "not-a-dict", "fallbacks": "not-an-array"}
	if !DeepEqualJSON(result.Value, want) {
		t.Fatalf("形状不对的容器该原样留着，实际 %#v", result.Value)
	}
	assertSecrets(t, result.Secrets, []Secret{
		{Path: []string{"apiKey"}, Set: false},
		{Path: []string{"nested", "token"}, Set: false},
	})
}

// TestRedactDropsADictEntryWhoseWholeValueIsTheSecret 钉住整条都是密钥的字典条目会被整条摘掉。
//
// 源: packages/settings/settings/tests/redact.spec.ts:89-97
func TestRedactDropsADictEntryWhoseWholeValueIsTheSecret(t *testing.T) {
	t.Parallel()

	type tokens struct {
		Tokens map[string]string `json:"tokens" settings:"secret"`
	}
	result := Redact[tokens](map[string]any{"tokens": map[string]any{"a": "x", "b": "y"}})

	if !DeepEqualJSON(result.Value, map[string]any{}) {
		t.Fatalf("整个字段都是密钥时该整个摘掉，实际 %#v", result.Value)
	}
	assertSecrets(t, result.Secrets, []Secret{{Path: []string{"tokens"}, Set: true}})
}

// TestRedactCannotReachSecretsHiddenUnderAny 钉住那个 fail-open 的口子是**已知**的。
//
// 包文档说了：藏在一个 any 字段底下的密钥走不到，也就摘不掉，所以不要那样建模。
// 一句写在注释里的警告，只有配一条会失败的用例才算数——这条用例正是那句话的证据，
// 它一旦变绿（也就是有一天真能走进去了），说明那句警告该删了。
func TestRedactCannotReachSecretsHiddenUnderAny(t *testing.T) {
	t.Parallel()

	type opaque struct {
		Anything any `json:"anything"`
	}
	result := Redact[opaque](map[string]any{"anything": map[string]any{"apiKey": "leaked"}})

	if len(result.Secrets) != 0 {
		t.Fatalf("any 底下走不到，不该记出密钥位置，实际 %#v", result.Secrets)
	}
	if !DeepEqualJSON(result.Value, map[string]any{"anything": map[string]any{"apiKey": "leaked"}}) {
		t.Fatalf("原样穿过，实际 %#v", result.Value)
	}
}

// TestJSONFieldsFollowsEncodingJSONNaming 钉住键名和 encoding/json 解释出的是同一套。
//
// 分叉的后果很具体：脱敏走一套键名、编解码走另一套，密钥会从那道缝里漏出去。
func TestJSONFieldsFollowsEncodingJSONNaming(t *testing.T) {
	t.Parallel()

	type embedded struct {
		Inherited string `json:"inherited" settings:"secret"`
	}
	type tagged struct {
		embedded
		Renamed string `json:"renamed,omitempty" settings:"secret"`
		NoTag   string
		Skipped string `json:"-" settings:"secret"`
		//lint:ignore U1000 这个字段永远不会被读——「不导出的字段不参与摘除」正是本用例要验的
		unexported string
	}

	result := Redact[tagged](map[string]any{
		"renamed":   "a",
		"NoTag":     "b",
		"-":         "c",
		"inherited": "d",
	})

	value, _ := result.Value.(map[string]any)
	if _, leaked := value["renamed"]; leaked {
		t.Fatalf("带 omitempty 的标签名该按逗号前那段认，实际 %#v", value)
	}
	if _, leaked := value["inherited"]; leaked {
		t.Fatalf("匿名内嵌该摊平到同一层，实际 %#v", value)
	}
	if value["NoTag"] != "b" {
		t.Fatalf("没有标签的字段该按字段名，实际 %#v", value)
	}
	// `json:"-"` 的字段整个不参与，所以值里那个字面叫 "-" 的键不是它，原样留着。
	if value["-"] != "c" {
		t.Fatalf("json:\"-\" 的字段该整个跳过，实际 %#v", value)
	}
	assertSecrets(t, result.Secrets, []Secret{
		{Path: []string{"inherited"}, Set: true},
		{Path: []string{"renamed"}, Set: true},
	})
}

// TestRedactWalksThroughPointers 钉住指针会被穿透。
//
// 一个 *Config 字段和一个 Config 字段在 JSON 里长得一样，密钥性质也一样。
// 不穿透的话，把字段改成指针就等于悄悄关掉了它下面所有的脱敏。
func TestRedactWalksThroughPointers(t *testing.T) {
	t.Parallel()

	type outer struct {
		Inner *redactNested `json:"inner"`
	}
	result := Redact[outer](map[string]any{"inner": map[string]any{"token": "x"}})

	if !DeepEqualJSON(result.Value, map[string]any{"inner": map[string]any{}}) {
		t.Fatalf("指针底下的密钥该摘掉，实际 %#v", result.Value)
	}
	assertSecrets(t, result.Secrets, []Secret{{Path: []string{"inner", "token"}, Set: true}})
}

// TestJSONFieldsFlattensAPointerEmbedAndKeepsTheOuterNameOnAClash 钉住两条内嵌规则。
//
// 两条都直接对着 encoding/json 的行为：
//
//  1. **匿名内嵌指针也要摊平。** `*embedded` 和 `embedded` 在 JSON 里长得一模一样，
//     不摊平的话，把一个内嵌字段改成指针就等于悄悄关掉了它下面所有的脱敏。
//  2. **重名时外层先到先得。** 外层字段和内嵌字段撞了同一个 JSON 名字时，
//     编解码认的是外层那个；脱敏要是认内嵌那个，两边就会对着不同的字段各走各的，
//     而密钥标记恰恰只写在其中一个上。
func TestJSONFieldsFlattensAPointerEmbedAndKeepsTheOuterNameOnAClash(t *testing.T) {
	t.Parallel()

	type inner struct {
		Token  string `json:"token" settings:"secret"`
		Shared string `json:"shared" settings:"secret"`
	}
	type outer struct {
		Shared string `json:"shared"` // 和内嵌的 shared 撞名，且**不是**密钥。
		*inner
	}

	result := Redact[outer](map[string]any{"token": "摘掉我", "shared": "留着我"})

	value, _ := result.Value.(map[string]any)
	if _, leaked := value["token"]; leaked {
		t.Fatalf("内嵌指针底下的密钥该摘掉，实际 %#v", value)
	}
	if value["shared"] != "留着我" {
		t.Fatalf("重名时该认外层那个非密钥字段，实际 %#v", value)
	}
	assertSecrets(t, result.Secrets, []Secret{{Path: []string{"token"}, Set: true}})
}

// assertSecrets 按路径排序之后比对，因为结构体字段的遍历顺序是声明顺序、
// 而字典条目的顺序是 Go map 的随机顺序——不排的话用例会随机失败。
func assertSecrets(t *testing.T, got, want []Secret) {
	t.Helper()

	sortSecrets(got)
	sortSecrets(want)

	if len(got) != len(want) {
		t.Fatalf("密钥位置有 %d 条，该是 %d 条：\n实际 %v\n期望 %v", len(got), len(want), got, want)
	}
	for index := range got {
		if !slices.Equal(got[index].Path, want[index].Path) || got[index].Set != want[index].Set {
			t.Fatalf("第 %d 条是 %v，该是 %v", index, got[index], want[index])
		}
	}
}

func sortSecrets(secrets []Secret) {
	slices.SortFunc(secrets, func(a, b Secret) int {
		return strings.Compare(strings.Join(a.Path, "\x00"), strings.Join(b.Path, "\x00"))
	})
}
