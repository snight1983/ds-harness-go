// 本文件的作用：证明溯源验真不是摆设。
//
// checkProvenance 存在的唯一理由，是让「凭空编一个出处」和「如实引用」在结果上
// 不一样。如果它抓不住编造的出处，那它就只是一段让人安心的装饰——比没有更糟，
// 因为它会让人以为这件事已经被管住了。
//
// 所以这里逐条钉住这几种情况：真引用放过、文件不存在抓住、行号越界抓住、
// 不带行号的那种照样要数要验、文档注释里的示例不算数、
// 字符串字面量里长得像注释的内容不算数。
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

	count, added, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	// 两种注释分开计。混成一个数之后那个数没有含义：它曾经被打印成
	// 「溯源注释：6284 条」，而其中 1668 条是新增注释，于是拿它去和别的工具对账
	// 会得出「少了一千多条」这种假结论——我自己就先被这个数误导过一轮。
	if count != 1 {
		t.Errorf("应当数出 1 条源注释，实际 %d 条", count)
	}
	if added != 1 {
		t.Errorf("应当数出 1 条新增注释，实际 %d 条", added)
	}
	if len(problems) != 0 {
		t.Errorf("如实的引用不该被挑出问题，实际 %v", problems)
	}
}

// TestProvenanceCountsAndChecksCitationsWithoutLineNumbers 钉住第三个真实发生过的
// bug，也是这三个里危害最大的一个。
//
// 正则曾经写成 `(\S+):(\d+)`，路径后面**必须**跟行号才算数。于是仓库里两百多条
// 只写路径的引用整条匹配不上：既不计数，也不验出处。发现它是因为往一个新拆出来的
// 文件头上写了一条凭空编的出处，DSH 里根本没有那个文件，门禁照样全绿。
//
// 一道能被这样绕过去的门禁，它给的「出处全部存在」是假话——而假话比没有更糟，
// 因为人会拿它当已经核对过的证据。
func TestProvenanceCountsAndChecksCitationsWithoutLineNumbers(t *testing.T) {
	t.Parallel()

	t.Run("出处存在就放过", func(t *testing.T) {
		t.Parallel()

		goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src/index.ts
func Ported() {}
`)

		count, _, problems, err := checkProvenance(goRoot, dshRoot)
		if err != nil {
			t.Fatalf("checkProvenance() error = %v", err)
		}
		if count != 1 {
			t.Errorf("不带行号的引用也该被数进来，实际数出 %d 条", count)
		}
		if len(problems) != 0 {
			t.Errorf("出处存在就不该报问题，实际 %v", problems)
		}
	})

	t.Run("出处不存在照样抓住", func(t *testing.T) {
		t.Parallel()

		goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src/does-not-exist.ts
func Ported() {}
`)

		count, _, problems, err := checkProvenance(goRoot, dshRoot)
		if err != nil {
			t.Fatalf("checkProvenance() error = %v", err)
		}
		if count != 1 {
			t.Errorf("不带行号的引用也该被数进来，实际数出 %d 条", count)
		}
		if len(problems) != 1 {
			t.Fatalf("不带行号的编造出处本该被抓住，实际问题列表为 %v", problems)
		}
		if !strings.Contains(problems[0], "出处不存在") {
			t.Errorf("报错该说清是出处不存在，实际 %q", problems[0])
		}
	})
}

// TestProvenanceChecksEveryCitationOnTheLine 钉住「一条注释引好几处」。
//
// 仓库里有不少 `// 源: a.ts、b.ts` 这样一行引两处的写法，后面还常跟一段中文说明
// 在指哪一段。只认第一处的话，第二处等于没验；把说明里的中文一起吞进路径的话，
// 第一处也验不成。所以边界靠 `packages/` 这个前缀找，不靠取到行尾。
func TestProvenanceChecksEveryCitationOnTheLine(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src/index.ts:1-5、packages/core/session/src/gone.ts（那段折叠）
func Ported() {}
`)

	count, _, problems, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		t.Fatalf("checkProvenance() error = %v", err)
	}
	// 一条注释算一条，哪怕它引了两处。
	if count != 1 {
		t.Errorf("应当数出 1 条源注释，实际 %d 条", count)
	}
	if len(problems) != 1 {
		t.Fatalf("第二处出处不存在，本该被抓住，实际问题列表为 %v", problems)
	}
	if !strings.Contains(problems[0], "gone.ts") {
		t.Errorf("报错该指向出问题的那一处，实际 %q", problems[0])
	}
	// 中文说明不能被吞进路径，否则第一处会被误报成「出处不存在」。
	if strings.Contains(problems[0], "那段") {
		t.Errorf("路径不该把后面的中文说明吞进去，实际 %q", problems[0])
	}
}

// TestProvenanceHandlesDirectoryCitations 钉住「引的是一整个上游包」。
//
// `// 源: packages/skill/tool-skill` 这种指的是整个目录，没有行号可言，要放过；
// 反过来给一个目录标行号是自相矛盾的，要抓住。
func TestProvenanceHandlesDirectoryCitations(t *testing.T) {
	t.Parallel()

	t.Run("只写目录就放过", func(t *testing.T) {
		t.Parallel()

		goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src
func Ported() {}
`)

		_, _, problems, err := checkProvenance(goRoot, dshRoot)
		if err != nil {
			t.Fatalf("checkProvenance() error = %v", err)
		}
		if len(problems) != 0 {
			t.Errorf("引一整个目录不该报问题，实际 %v", problems)
		}
	})

	t.Run("给目录标行号就抓住", func(t *testing.T) {
		t.Parallel()

		goRoot, dshRoot := provenanceFixture(t, `package ported

// 源: packages/core/session/src:1-5
func Ported() {}
`)

		_, _, problems, err := checkProvenance(goRoot, dshRoot)
		if err != nil {
			t.Fatalf("checkProvenance() error = %v", err)
		}
		if len(problems) != 1 {
			t.Fatalf("给目录标行号本该被抓住，实际问题列表为 %v", problems)
		}
		if !strings.Contains(problems[0], "是目录") {
			t.Errorf("报错该说清它是目录，实际 %q", problems[0])
		}
	})
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

	_, _, problems, err := checkProvenance(goRoot, dshRoot)
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

			_, _, problems, err := checkProvenance(goRoot, dshRoot)
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

	count, _, problems, err := checkProvenance(goRoot, dshRoot)
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

	count, _, problems, err := checkProvenance(goRoot, dshRoot)
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

	_, _, _, err := checkProvenance(goRoot, dshRoot)
	if err == nil {
		t.Fatal("解析不了的 Go 文件本该让检查失败")
	}
	if !strings.Contains(err.Error(), "解析失败") {
		t.Errorf("报错该说清是解析失败，实际 %q", err.Error())
	}
}
