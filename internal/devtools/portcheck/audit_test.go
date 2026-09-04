// 本文件的作用：钉住四份审查报告各自的判据。
//
// 这四份报告的全部价值在于「报出来的都值得看一眼」。一份误报多的报告比没有报告更糟——
// 它会训练人去忽略红色。所以这里逐条钉的是**边界**：哪些该报、哪些明确不该报。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/internal/devtools/rulingtable"
)

// portedRow 造一条 PORTED 裁决，省掉每个用例里那一串无关字段。
func portedRow(kind, name, goRef, note string) rulingtable.Row {
	return rulingtable.Row{
		Package:  "core/demo",
		File:     "packages/core/demo/src/index.ts",
		Line:     1,
		Kind:     kind,
		Name:     name,
		Decision: rulingtable.Ported,
		GoRef:    goRef,
		Note:     note,
	}
}

// TestAuditKindMismatchesFlagsAShapeChange 是这份报告存在的理由：
// 上游一个函数落在 Go 的一个类型上，名字这一层看不出任何异样，形状这一层一眼就是错的。
func TestAuditKindMismatchesFlagsAShapeChange(t *testing.T) {
	t.Parallel()

	symbols := map[string]symbolKind{
		"demo.Thing": kindType,
		"demo.Make":  kindFunc,
	}
	findings := auditKindMismatches([]rulingtable.Row{
		portedRow("function", "makeThing", "demo.Thing", ""), // 函数 → type，该报
		portedRow("interface", "Thing", "demo.Thing", ""),    // 接口 → type，不该报
		portedRow("function", "makeThing", "demo.Make", ""),  // 函数 → func，不该报
	}, symbols)

	if len(findings) != 1 {
		t.Fatalf("只该报那条函数落在 type 上的，实际 %d 条：%#v", len(findings), findings)
	}
	if findings[0].goRef != "demo.Thing" || findings[0].name != "makeThing" {
		t.Fatalf("报错了对象：%#v", findings[0])
	}
	// 这一份和第二份一样要能一眼看出「有没有人已经说过话」，否则它排不了队。
	if !strings.Contains(findings[0].detail, "**裁决表没写理由**") {
		t.Errorf("没写 note 的那条要显式标出来，实际 %q", findings[0].detail)
	}
}

// TestAuditKindMismatchesCarriesTheLedgerReason 钉住写了 note 的那条要把理由带进报告。
//
// 一条形变可能完全正当（上游的 interface 是声明合并接入点，Go 里只能落成泛型函数）。
// 报告不把那句话带上的话，复核的人得自己回裁决表翻一遍，这份队列就白排了。
func TestAuditKindMismatchesCarriesTheLedgerReason(t *testing.T) {
	t.Parallel()

	findings := auditKindMismatches(
		[]rulingtable.Row{portedRow("interface", "Forms", "demo.FormAs", "声明合并接入点，Go 里只能是泛型函数")},
		map[string]symbolKind{"demo.FormAs": kindFunc},
	)
	if len(findings) != 1 {
		t.Fatalf("该报一条，实际 %d 条：%#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].detail, "声明合并接入点") {
		t.Errorf("理由没带进报告：%q", findings[0].detail)
	}
}

// TestAuditKindMismatchesAcceptsAConstBecomingAFactory 钉住一条**有意**放行的形变。
//
// TS 的 `const` 常常是一份不可变的默认值对象；Go 的 const 存不下结构体，
// 而包级 var 是可以被外面改掉的，所以本仓库把这种默认值做成「每次交回一份新值」的
// 函数（成例见 subagent.NoStartCapabilities）。这是既定做法，报它就是误报。
func TestAuditKindMismatchesAcceptsAConstBecomingAFactory(t *testing.T) {
	t.Parallel()

	findings := auditKindMismatches(
		[]rulingtable.Row{portedRow("const", "DEFAULTS", "demo.NewDefaults", "")},
		map[string]symbolKind{"demo.NewDefaults": kindFunc},
	)
	if len(findings) != 0 {
		t.Fatalf("const 做成工厂函数是既定做法，不该报：%#v", findings)
	}
}

// TestAuditKindMismatchesSkipsShapelessAndUnknownRefs 钉住两条不检查的边界。
//
// 一条裸 re-export 没有自己的形状（形状在它转出去的那个东西身上，而清单不记那个）；
// 一个收不到的 go_ref 归门禁第四项管，在这儿再报一遍只会让两份清单互相稀释。
func TestAuditKindMismatchesSkipsShapelessAndUnknownRefs(t *testing.T) {
	t.Parallel()

	findings := auditKindMismatches([]rulingtable.Row{
		portedRow("reexport", "Thing", "demo.Thing", ""),
		portedRow("function", "gone", "demo.NotThere", ""),
	}, map[string]symbolKind{"demo.Thing": kindType})

	if len(findings) != 0 {
		t.Fatalf("这两条都不该报：%#v", findings)
	}
}

// TestAuditUnexportedRefsCatchesAnUnexportedMethod 钉住「包名之后**每一段**都要看」。
//
// 一个导出类型上的非导出方法，下游一样调不到。只看第一段的话它会漏过去。
func TestAuditUnexportedRefsCatchesAnUnexportedMethod(t *testing.T) {
	t.Parallel()

	findings := auditUnexportedRefs([]rulingtable.Row{
		portedRow("function", "publicOne", "demo.Supervisor.transport", ""),
		portedRow("function", "hidden", "demo.helper", "上游也只给自己的测试用"),
		portedRow("function", "fine", "demo.Supervisor.Start", ""),
	})

	if len(findings) != 2 {
		t.Fatalf("该报两条，实际 %d 条：%#v", len(findings), findings)
	}
	byName := map[string]auditFinding{}
	for _, finding := range findings {
		byName[finding.name] = finding
	}
	if !strings.Contains(byName["publicOne"].detail, "**裁决表没写理由**") {
		t.Errorf("没写 note 的那条要显式标出来，实际 %q", byName["publicOne"].detail)
	}
	if !strings.Contains(byName["hidden"].detail, "上游也只给自己的测试用") {
		t.Errorf("写了 note 的那条要把理由带上，实际 %q", byName["hidden"].detail)
	}
}

// TestAuditCollapsesExcludesReexports 钉住这份报告能用的前提。
//
// 一个 TS 包同时声明并转出同一个类型，清单里就是两行指向同一个 Go 符号。
// 不排除 re-export 的话这份报告会从 125 条膨胀到 664 条，而多出来的那 539 条
// 全是清单的形状，跟移植对不对没有关系。
func TestAuditCollapsesExcludesReexports(t *testing.T) {
	t.Parallel()

	groups := auditCollapses([]rulingtable.Row{
		portedRow("interface", "OneShotData", "demo.Data", ""),
		portedRow("interface", "ContinuableData", "demo.Data", ""),
		portedRow("reexport-type", "Data", "demo.Data", ""),
		portedRow("type", "Solo", "demo.Solo", ""),
		portedRow("reexport-type", "Solo", "demo.Solo", ""),
	})

	if len(groups) != 1 {
		t.Fatalf("只有 demo.Data 该成组，实际 %d 组：%#v", len(groups), groups)
	}
	if groups[0].goRef != "demo.Data" || len(groups[0].members) != 2 {
		t.Fatalf("re-export 不该被算进组里：%#v", groups[0])
	}
}

// TestProvenanceDensityCountsOnlyRealCommentsInNonTestFiles 钉住分子和分母各自的口径。
//
// 分子只认真正的注释节点：字符串字面量里长得像溯源注释的东西不是注释，
// 这个 bug 在 checkProvenance 身上真的发生过。分母只认非测试代码。
func TestProvenanceDensityCountsOnlyRealCommentsInNonTestFiles(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "demo", "thing.go"), "package demo\n"+
		"\n"+
		"// 源: packages/core/demo/src/index.ts:1-10\n"+
		"type Thing struct{}\n"+
		"\n"+
		"// 新增: Go 侧需要的并发保护\n"+
		"var fixture = `\n"+
		"// 源: packages/nowhere/src/fake.ts:1\n"+
		"`\n")
	// 测试代码进不了分子也进不了分母。
	writeFile(t, filepath.Join(goRoot, "demo", "thing_test.go"), "package demo\n"+
		"\n"+
		"// 源: packages/core/demo/src/index.ts:1-10\n"+
		"func helper() {}\n")

	stats, err := collectProvenanceDensity(goRoot)
	if err != nil {
		t.Fatalf("collectProvenanceDensity() error = %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("只该有 demo 一个包，实际 %#v", stats)
	}
	if stats[0].sources != 1 {
		t.Errorf("字符串字面量里那条不算注释，该数出 1 条源，实际 %d", stats[0].sources)
	}
	if stats[0].added != 1 {
		t.Errorf("该数出 1 条新增，实际 %d", stats[0].added)
	}
	if stats[0].lines != 9 {
		t.Errorf("分母只算非测试代码的 9 行，实际 %d", stats[0].lines)
	}
}

// TestAuditThinProvenanceExcludesOwnTooling 钉住本仓自造工具的豁免。
//
// internal/devtools/ 底下的东西没有上游对应物，一条溯源都不该有。不豁免的话这份报告的
// 前几名永远是它们，真正该看的包会被挤到看不见的地方。
//
// 同时钉住顶层 `tools/` **不**在豁免里：那是工具契约包，照着上游译过来的。判据要是
// 写成「名字叫 tools 的目录」，它会连同门禁工具一起被放过，而它恰恰是最该有溯源的包之一。
func TestAuditThinProvenanceExcludesOwnTooling(t *testing.T) {
	t.Parallel()

	thin := auditThinProvenance([]packageProvenance{
		{pkg: "internal/devtools/portcheck", lines: 500, sources: 0},
		{pkg: "tools", lines: 1000, sources: 0},
		{pkg: "adapter/objectstore", lines: 1000, sources: 0},
		{pkg: "llm", lines: 1000, sources: 100},
	})

	if len(thin) != 2 || thin[0].pkg != "tools" || thin[1].pkg != "adapter/objectstore" {
		t.Fatalf("该留下 tools 和 adapter/objectstore，实际 %#v", thin)
	}
}

// TestAuditThinProvenanceHonoursAPackageThatDeclaredItselfNew 钉住那条豁免的两个条件。
//
// 一个包在包文档里写了 `新增:` 且一条 `源:` 都没有，就是已经交代过「整份是新写的」；
// 再报它一遍是在罚一份已经写好的交代。但**两个条件缺一不可**：既有 `源:` 又在包文档里
// 写 `新增:` 是多数包的常态（整体照译、某处刻意偏离），那种包的密度偏低仍是真信号。
func TestAuditThinProvenanceHonoursAPackageThatDeclaredItselfNew(t *testing.T) {
	t.Parallel()

	thin := auditThinProvenance([]packageProvenance{
		{pkg: "adapter/objectstore", lines: 1000, sources: 0, declaredNew: true},
		{pkg: "half/ported", lines: 1000, sources: 5, declaredNew: true},
		{pkg: "silent/gap", lines: 1000, sources: 0},
	})

	if len(thin) != 2 {
		t.Fatalf("只该豁免 adapter/objectstore 那一个，实际留下 %#v", thin)
	}
	for _, stats := range thin {
		if stats.pkg == "adapter/objectstore" {
			t.Fatalf("声明过自己没有上游的包不该再被报：%#v", stats)
		}
	}
}

// TestProvenanceDensityOnlyTrustsThePackageDocForTheNewDeclaration 钉住位置就是语义。
//
// 一条 `新增:` 写在函数头上说的是那个函数偏离了上游，写在 package 子句正上方
// 说的才是整个包没有上游对应物。认错位置的话，任何一个带函数级 `新增:` 的包
// 都会白拿到豁免——而那样的包全仓到处都是。
func TestProvenanceDensityOnlyTrustsThePackageDocForTheNewDeclaration(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "declared", "thing.go"),
		"// Package declared 没有上游对应物。\n"+
			"//\n"+
			"// 新增: DSH 没有这个包。\n"+
			"package declared\n")
	writeFile(t, filepath.Join(goRoot, "inline", "thing.go"),
		"package inline\n"+
			"\n"+
			"// 新增: Go 侧需要的并发保护\n"+
			"func Guard() {}\n")

	stats, err := collectProvenanceDensity(goRoot)
	if err != nil {
		t.Fatalf("collectProvenanceDensity() error = %v", err)
	}
	byPkg := map[string]packageProvenance{}
	for _, one := range stats {
		byPkg[one.pkg] = one
	}
	if !byPkg["declared"].declaredNew {
		t.Error("写在 package 子句上方的 `新增:` 该被认出来")
	}
	if byPkg["inline"].declaredNew {
		t.Error("写在函数头上的 `新增:` 不该被当成整包声明")
	}
}

// TestPackageDocsCatchAFileHeaderStandingInForThePackageDoc 是这份报告存在的理由。
//
// 少掉 package 子句前那个空行，Go 就把 `本文件的作用：…` 当成了整个包的文档。
// 编译得过、`go doc` 也有输出，只是讲的是某一份文件——**没有任何东西会提醒写的人**。
func TestPackageDocsCatchAFileHeaderStandingInForThePackageDoc(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	// 少了空行：这条文件说明成了包文档。
	writeFile(t, filepath.Join(goRoot, "hijacked", "thing.go"),
		"// 本文件的作用：造一个 Thing。\n"+
			"package hijacked\n")
	// 空行还在：文件说明就只是文件说明，但这个包于是一份包文档都没有。
	writeFile(t, filepath.Join(goRoot, "silent", "thing.go"),
		"// 本文件的作用：造一个 Thing。\n"+
			"\n"+
			"package silent\n")
	// 正经写法：doc.go 讲包，thing.go 讲文件。
	writeFile(t, filepath.Join(goRoot, "proper", "doc.go"), "// Package proper 讲的是这个包。\npackage proper\n")
	writeFile(t, filepath.Join(goRoot, "proper", "thing.go"),
		"// 本文件的作用：造一个 Thing。\n"+
			"\n"+
			"package proper\n")

	stats, err := collectProvenanceDensity(goRoot)
	if err != nil {
		t.Fatalf("collectProvenanceDensity() error = %v", err)
	}
	byPkg := map[string]docFinding{}
	for _, finding := range auditPackageDocs(stats) {
		byPkg[finding.pkg] = finding
	}

	if _, reported := byPkg["proper"]; reported {
		t.Errorf("doc.go 讲包、文件说明隔开的写法是正经写法，不该报：%#v", byPkg["proper"])
	}
	if !strings.Contains(byPkg["hijacked"].detail, "文件说明被当成了包文档") {
		t.Errorf("被顶替的那个包没报对：%q", byPkg["hijacked"].detail)
	}
	if !strings.Contains(byPkg["silent"].detail, "没有包文档") {
		t.Errorf("一份包文档都没有的那个包没报对：%q", byPkg["silent"].detail)
	}
}

// TestPackageDocsReportTwoDocsAsOneFinding 钉住「一个包只出一条发现」。
//
// 一个包可以同时中两条（两份包文档，而且其中一份还是文件说明）。分成两条发现的话，
// 同一个包会在报告里出现两次，看的人会以为是两件不相干的事。
func TestPackageDocsReportTwoDocsAsOneFinding(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "double", "a.go"), "// Package double 讲的是这个包。\npackage double\n")
	writeFile(t, filepath.Join(goRoot, "double", "b.go"), "// 本文件的作用：另一份。\npackage double\n")

	stats, err := collectProvenanceDensity(goRoot)
	if err != nil {
		t.Fatalf("collectProvenanceDensity() error = %v", err)
	}
	findings := auditPackageDocs(stats)
	if len(findings) != 1 {
		t.Fatalf("同一个包该只出一条发现，实际 %d 条：%#v", len(findings), findings)
	}
	if !strings.Contains(findings[0].detail, "2 个文件都带包文档") {
		t.Errorf("没报出「有两份」：%q", findings[0].detail)
	}
	if !strings.Contains(findings[0].detail, "文件说明被当成了包文档") {
		t.Errorf("两条毛病要写在同一条发现里，实际 %q", findings[0].detail)
	}
}

// TestRunAuditWritesEverySection 走一遍端到端，钉住报告的五节都在、且真的落了盘。
func TestRunAuditWritesEverySection(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "demo", "thing.go"), "package demo\n\ntype Thing struct{}\n")

	rulingPath := filepath.Join(t.TempDir(), "portmap.tsv")
	writeFile(t, rulingPath, strings.Join([]string{
		rulingtable.Header,
		"core/demo\tpackages/core/demo/src/index.ts\t1\tfunction\tmakeThing\tsrc/index.ts\tPORTED\tdemo.Thing\t",
	}, "\n")+"\n")

	outPath := filepath.Join(t.TempDir(), "nested", "audit-findings.md")
	if err := runAudit(rulingPath, goRoot, outPath); err != nil {
		t.Fatalf("runAudit() error = %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("报告没落盘：%v", err)
	}
	report := string(data)
	for _, section := range []string{
		"## 一、kind 对不上",
		"## 二、go_ref 指向非导出符号",
		"## 三、多条上游符号塌缩到同一个 go_ref",
		"## 四、溯源密度偏低的包",
		"## 五、包文档有毛病的包",
	} {
		if !strings.Contains(report, section) {
			t.Errorf("报告里缺了 %q", section)
		}
	}
	if !strings.Contains(report, "`demo.Thing`") {
		t.Error("那条 kind 对不上的发现没出现在报告里")
	}
}
