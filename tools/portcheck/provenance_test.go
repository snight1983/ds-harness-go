// 本文件的作用：证明溯源验真不是摆设。
//
// checkProvenance 存在的唯一理由，是让「凭空编一个出处」和「如实引用」在结果上
// 不一样。如果它抓不住编造的出处，那它就只是一段让人安心的装饰——比没有更糟，
// 因为它会让人以为这件事已经被管住了。
//
// 所以这里逐条钉住五种情况：真引用放过、文件不存在抓住、行号越界抓住、
// 文档注释里的示例不算数、字符串字面量里长得像注释的内容不算数。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// provenanceFixture 造一对假的 goRoot / dshRoot，不依赖真实的 DSH 源码。
//
// 不依赖真实源码是有意的：这条测试要能在任何机器上跑，而且它验的是
// checkProvenance 的判断逻辑，不是某个具体文件此刻有多少行。
func provenanceFixture(t *testing.T, goSource string) (goRoot, dshRoot string) {
	t.Helper()

	goRoot = t.TempDir()
	dshRoot = t.TempDir()

	// 造一个正好 10 行的来源文件。
	target := filepath.Join(dshRoot, "packages", "core", "session", "src")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(
		filepath.Join(target, "index.ts"),
		[]byte(strings.Repeat("line\n", 10)),
		0o644,
	); err != nil {
		t.Fatalf("写来源文件失败：%v", err)
	}

	if err := os.WriteFile(filepath.Join(goRoot, "ported.go"), []byte(goSource), 0o644); err != nil {
		t.Fatalf("写 Go 文件失败：%v", err)
	}
	return goRoot, dshRoot
}

func TestProvenanceAcceptsAnHonestCitation(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src/index.ts:3-8
func Ported() {}

// 新增: Go 侧需要的并发保护，DSH 是单进程 CLI 所以没有这一层
func Added() {}
`)

	count, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	if count != 2 {
		t.Errorf("应当数出 2 条溯源注释，实际 %d 条", count)
	}
	if len(problems) != 0 {
		t.Errorf("如实的引用不该被挑出问题，实际 %v", problems)
	}
}

// TestProvenanceCatchesAFabricatedFile 是这个检查器存在的核心理由。
//
// 引一个不存在的文件，和凭空编一段出处，在结果上完全一样。这一条抓不住，
// 后面所有「已核对」的说法就都失去了依据。
func TestProvenanceCatchesAFabricatedFile(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src/does-not-exist.ts:1-5
func Ported() {}
`)

	_, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("编造的出处本该被抓住，实际问题列表为 %v", problems)
	}
	if !strings.Contains(problems[0], "出处不存在") {
		t.Errorf("报错该说清是出处不存在，实际 %q", problems[0])
	}
}

// TestProvenanceCatchesAnOutOfRangeCitation 抓「文件对了但行号是编的」。
//
// 这种比整个文件都编造更隐蔽：路径看着眼熟，行号看着具体，人一眼扫过去不会起疑。
func TestProvenanceCatchesAnOutOfRangeCitation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		citation string
	}{
		{"结束行超出文件末尾", "packages/core/session/src/index.ts:5-99"},
		{"起始行就已经越界", "packages/core/session/src/index.ts:42"},
		{"区间反着写", "packages/core/session/src/index.ts:8-3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			goRoot, dshRoot := provenanceFixture(t, "package ported\n\n// 源: "+test.citation+"\nfunc Ported() {}\n")

			_, problems, err := checkProvenance(goRoot, dshRoot)
			if err != nil {
				t.Fatalf("checkProvenance() error = %v", err)
			}
			if len(problems) != 1 {
				t.Fatalf("越界的行范围本该被抓住，实际问题列表为 %v", problems)
			}
			if !strings.Contains(problems[0], "越界") {
				t.Errorf("报错该说清是行范围越界，实际 %q", problems[0])
			}
		})
	}
}

// TestProvenanceIgnoresExamplesInsideDocComments 钉住那个真实发生过的 bug。
//
// 第一版正则没锚行首，于是本工具自己文档注释里的三行示例被数成了 3 条真注释。
// 一个会把示例当数据的检查器，报出来的数字没有意义，所以这条不能靠人记着别写示例。
func TestProvenanceIgnoresExamplesInsideDocComments(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, `package ported

// 溯源注释的写法：
//
//	// 源: packages/core/session/src/index.ts:1-5
//	// 新增: 某个理由
//
// 以上都是示例，不是真的引用。
func Ported() {}
`)

	count, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	if count != 0 {
		t.Errorf("文档注释里的示例不该被数成溯源注释，实际数出 %d 条", count)
	}
	if len(problems) != 0 {
		t.Errorf("不该报出问题，实际 %v", problems)
	}
}

// TestProvenanceIgnoresCitationsInsideStringLiterals 钉住第二个真实发生过的 bug。
//
// 本文件上面那几条测试，夹具本身就是一段写在反引号字符串里的假 Go 源码，其中一条
// 故意引了不存在的文件。第一版 checkProvenance 逐行扫文本，于是把**测试夹具**
// 当成了真的溯源注释，报「出处不存在」——它抓住的是自己的测试数据。
//
// 字符串字面量里长得像注释的东西不是注释。这一点逐行正则分不出来，
// 所以修法不是再补一条正则，而是改用 go/parser 只看真正的注释节点。
func TestProvenanceIgnoresCitationsInsideStringLiterals(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, "package ported\n\n"+
		"const fixture = `package fake\n\n"+
		"// 源: packages/core/session/src/does-not-exist.ts:1-5\n"+
		"func Fake() {}\n`\n")

	count, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	if count != 0 {
		t.Errorf("字符串字面量里的引用不该被数成溯源注释，实际数出 %d 条", count)
	}
	if len(problems) != 0 {
		t.Errorf("不该报出问题，实际 %v", problems)
	}
}

// TestProvenanceRejectsUnparsableGo 钉住一条底线：解析不了的 Go 文件必须报错，
// 不能跳过。
//
// 跳过的话，那个文件里的溯源注释一条都没验过，而门禁照样会打印
// 「溯源注释：N 条，全部验过出处」——那句话就成了假话。
func TestProvenanceRejectsUnparsableGo(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, "package ported\n\nfunc Broken( {\n")

	_, _, err := checkProvenance(goRoot, dshRoot)
	if err == nil {
		t.Fatal("解析不了的 Go 文件本该让检查失败")
	}
	if !strings.Contains(err.Error(), "解析失败") {
		t.Errorf("报错该说清是解析失败，实际 %q", err.Error())
	}
}
