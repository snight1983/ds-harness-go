// 本文件的作用：钉住重锚的三段判断——起点含 JSDoc、终点靠括号配平、锚点从裁决表反查——
// 以及 -fix 的幂等。
//
// 为什么这几条要用测试锁住：重锚的输出是一份「哪些行号漂了」的清单，而清单错了的表现
// 不是报错，是**一批看着具体的数字**。如果起点少算一段 JSDoc，全仓 6000 多条会一起
// 被判成 DRIFT；如果括号配平被字符串里的括号带偏，终点会安静地给出假答案。
// 两种都会让这份报告变成噪声，而噪声报告的下一步一定是被忽略。
//
// 夹具全部自带，不碰真实的上游快照：这些测试验的是判断逻辑，不是某个文件此刻多少行。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/tools/internal/rulingtable"
)

// fixtureTS 是一段带行号意图的假上游源码，每个声明的真实跨度都写在下面的注释里。
//
// 用切片拼而不是写成一整个反引号字符串，是因为它里头**必须**有反引号（模板串那一行是
// 「字符串里的括号不算数」这条的关键证据），而且这么写行号一眼能数出来。
var fixtureTS = []string{
	/*  1 */ "import { Foo } from './foo'",
	/*  2 */ "",
	/*  3 */ "/**",
	/*  4 */ " * Alpha 的文档，起点必须算在这一行。",
	/*  5 */ " */",
	/*  6 */ "export interface Alpha {",
	/*  7 */ "  id: string",
	/*  8 */ "}",
	/*  9 */ "",
	/* 10 */ "export function beta(input: string): string {",
	/* 11 */ "  const opener = '('",
	/* 12 */ "  const closer = '}'",
	/* 13 */ "  return `${input}${opener}${closer}`",
	/* 14 */ "}",
	/* 15 */ "",
	/* 16 */ "export type Gamma =",
	/* 17 */ "  | { kind: 'a' }",
	/* 18 */ "  | { kind: 'b' }",
	/* 19 */ "",
	/* 20 */ "export const delta = 42",
	/* 21 */ "",
	/* 22 */ "export class Epsilon {",
	/* 23 */ "  private zeta(count: number): void {",
	/* 24 */ "    // }}} 注释里的括号不算数",
	/* 25 */ "  }",
	/* 26 */ "}",
}

// fixtureUpstreamPath 是夹具在上游里的相对路径，溯源注释按这个口径引它。
const fixtureUpstreamPath = "packages/core/fixture/src/index.ts"

// reanchorFixture 造一对假的 goRoot / dshRoot。
func reanchorFixture(t *testing.T, goSource string) (goRoot, dshRoot string) {
	t.Helper()

	goRoot, dshRoot = t.TempDir(), t.TempDir()

	target := filepath.Join(dshRoot, filepath.FromSlash(filepath.Dir(fixtureUpstreamPath)))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dshRoot, filepath.FromSlash(fixtureUpstreamPath)),
		[]byte(strings.Join(fixtureTS, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写上游夹具失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "ported.go"), []byte(goSource), 0o644); err != nil {
		t.Fatalf("写 Go 文件失败：%v", err)
	}
	return goRoot, dshRoot
}

// TestTSSpanIncludesAdjacentJSDoc 钉住起点惯例：紧邻上方的 JSDoc 块算在跨度里。
//
// 这是本仓库 6000 多条溯源注释共用的口径（`SessionEvent` 记 378-423，其中 378-390
// 是 doc）。少算这一段，全仓会一起被判成漂移，而那时候报告里没有一条是可信的。
func TestTSSpanIncludesAdjacentJSDoc(t *testing.T) {
	t.Parallel()

	span, hits, _ := locateTSSymbol(fixtureTS, "Alpha")
	if hits != 1 {
		t.Fatalf("Alpha 应当唯一命中，实际命中 %d 处", hits)
	}
	if span.start != 3 {
		t.Errorf("起点该含 JSDoc 的 /** 那一行（3），实际 %d", span.start)
	}
	if span.end != 8 {
		t.Errorf("终点该是接口闭合的那一行（8），实际 %d", span.end)
	}
}

// TestTSSpanStopsAtBlankLineAboveDeclaration 钉住「紧邻」这个限定。
//
// 隔了空行的注释块讲的是上一段代码。把它算进来，起点会无端往上跑，而跑上去之后
// 那条注释仍然「看着很具体」。
func TestTSSpanStopsAtBlankLineAboveDeclaration(t *testing.T) {
	t.Parallel()

	lines := []string{
		"/**",
		" * 这段文档讲的是别的东西。",
		" */",
		"",
		"export const orphan = 1",
	}
	span, hits, _ := locateTSSymbol(lines, "orphan")
	if hits != 1 {
		t.Fatalf("orphan 应当唯一命中，实际命中 %d 处", hits)
	}
	if span.start != 5 || span.end != 5 {
		t.Errorf("隔了空行的注释块不该算进跨度，期望 5-5，实际 %d-%d", span.start, span.end)
	}
}

// TestTSSpanBalancesBrackets 钉住终点：靠 {}、()、[] 的深度回到 0 来收尾。
func TestTSSpanBalancesBrackets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		symbol     string
		start, end int
	}{
		{"函数体靠花括号收尾", "beta", 10, 14},
		{"没有花括号的类型联合靠续行判断收尾", "Gamma", 16, 18},
		{"单行常量就是一行", "delta", 20, 20},
		{"类整体", "Epsilon", 22, 26},
		{"类成员只到自己的闭合括号", "zeta", 23, 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			span, hits, _ := locateTSSymbol(fixtureTS, test.symbol)
			if hits != 1 {
				t.Fatalf("%s 应当唯一命中，实际命中 %d 处", test.symbol, hits)
			}
			if span.start != test.start || span.end != test.end {
				t.Errorf("%s 的跨度期望 %d-%d，实际 %d-%d",
					test.symbol, test.start, test.end, span.start, span.end)
			}
		})
	}
}

// TestTSSpanIgnoresBracketsInsideStrings 钉住那个会安静给出假答案的坑。
//
// 一句 `return '(((' + "}}}"` 里有六个不配平的括号。如果它们算进深度，终点要么提前
// 收在半句话上，要么一路跑到文件末尾——**两种都不会报错**，只会让报告里多出一批
// 数字很具体的假漂移。模板串的 `${}` 单独一档：它的花括号是真的代码括号。
func TestTSSpanIgnoresBracketsInsideStrings(t *testing.T) {
	t.Parallel()

	lines := []string{
		"export function tricky(): string {",
		"  const a = '((('",
		`  const b = "}}}"`,
		"  const c = `${ ['['].length }`",
		"  return a + b + c",
		"}",
		"",
		"export const after = 1",
	}
	span, hits, _ := locateTSSymbol(lines, "tricky")
	if hits != 1 {
		t.Fatalf("tricky 应当唯一命中，实际命中 %d 处", hits)
	}
	if span.start != 1 || span.end != 6 {
		t.Errorf("字符串里的括号不该影响配平，期望 1-6，实际 %d-%d", span.start, span.end)
	}

	// 顺带证明扫描没有被前一句带偏：后面那个常量还得能正确定位。
	span, hits, _ = locateTSSymbol(lines, "after")
	if hits != 1 || span.start != 8 || span.end != 8 {
		t.Errorf("after 期望唯一命中 8-8，实际命中 %d 处、跨度 %d-%d", hits, span.start, span.end)
	}
}

// TestAnchorFromRulingTable 钉住第二条锚点来路：注释没带 `（名字）` 时，
// 从它所文档化的 Go 声明经裁决表反查上游符号名。
//
// 这条是全仓能用上的关键——6284 条注释里只有 141 条自带锚点，其余全靠这条路。
func TestAnchorFromRulingTable(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":10-14\nfunc Beta() {}\n")

	table := newAnchorTable([]rulingtable.Row{{
		Package:  "core/fixture",
		File:     "src/index.ts",
		Line:     10,
		Kind:     "function",
		Name:     "beta",
		Decision: rulingtable.Ported,
		GoRef:    "fixture.Beta",
	}})

	findings, err := collectReanchorFindings(goRoot, dshRoot, table)
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.anchor != "beta" {
		t.Errorf("锚点该经裁决表反查成上游的 beta，实际 %q", finding.anchor)
	}
	if finding.status != statusOK {
		t.Errorf("引的 10-14 正是算出来的跨度，该判 OK，实际 %s（%s）", finding.status, finding.detail)
	}
}

// TestAnchorFallsBackToCaseInsensitiveDeclName 钉住第三条来路。
//
// 裁决表并没有覆盖全部声明，而 Go 的 PascalCase 对上游的 camelCase 是这份代码库里
// 最常见的一组对应。退化匹配救回来的量不小，所以不能因为「不够严谨」就不做。
func TestAnchorFallsBackToCaseInsensitiveDeclName(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":11-13\nfunc Beta() {}\n")

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.anchor != "Beta" {
		t.Errorf("裁决表查不到时该拿 Go 声明名当候选，实际 %q", finding.anchor)
	}
	if finding.foundStart != 10 || finding.foundEnd != 14 {
		t.Errorf("算出的跨度期望 10-14，实际 %d-%d", finding.foundStart, finding.foundEnd)
	}
	if finding.status != statusDrift {
		t.Errorf("引的 11-13 落在算出的 10-14 里但不等也不包含，该判 DRIFT，实际 %s", finding.status)
	}
}

// TestReanchorClassifiesContainsAndNoAnchor 钉住两个「不算错」的状态。
//
// 报告的价值全在「报出来的都值得看一眼」。文件头部那种整体溯源本来就该包住单个符号，
// 把它判成漂移会让人学会忽略红色；而判不出锚点必须明确记成判不出，不能算进 OK
// 假装通过——那才是这份报告最初要修的毛病。
func TestReanchorClassifiesContainsAndNoAnchor(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":1-26（Alpha）\nfunc Wide() {}\n\n"+
		"// 源: "+fixtureUpstreamPath+":1-26\nfunc Loose() {}\n")

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("应当收出 2 条发现，实际 %d 条", len(findings))
	}
	if findings[0].status != statusContains {
		t.Errorf("带锚点的大范围溯源该判 CONTAINS，实际 %s", findings[0].status)
	}
	// Loose 在上游夹具里没有同名声明，裁决表也是空的，于是三条路都走不通。
	if findings[1].status != statusNotFound {
		t.Errorf("锚点在上游找不到时该判 NOT_FOUND，实际 %s", findings[1].status)
	}
}

// TestReanchorFixIsIdempotent 钉住 -fix 的两条底线：只改行号那几个字节，以及跑两遍
// 和跑一遍结果一样。
//
// 幂等不是洁癖。这批改动的唯一复核办法是读 diff，而一个不幂等的修复器会在第二次
// 运行时产生新的 diff，于是没人分得清哪一行是修复、哪一行是工具自己的抖动。
func TestReanchorFixIsIdempotent(t *testing.T) {
	t.Parallel()

	// 第一条自带锚点；第二条没有，靠裁决表反查，且那一行记的上游文件和注释引的一致
	// ——够 [tierRulingPath]，所以 -fix 敢动它。改的时候顺手把 `（名字）` 补进注释，
	// 因为反查依赖一张会变的表，把结论固化下来才是一次性的。
	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":1-2（Alpha）\n" +
		"// 说明：这句话不该被动到。\n" +
		"type Alpha struct{}\n\n" +
		"// 源: " + fixtureUpstreamPath + ":99\n" +
		"func Beta() {}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)
	goFile := filepath.Join(goRoot, "ported.go")

	table := newAnchorTable([]rulingtable.Row{{
		Package:  "core/fixture",
		File:     "src/index.ts",
		Line:     10,
		Kind:     "function",
		Name:     "beta",
		Decision: rulingtable.Ported,
		GoRef:    "fixture.Beta",
	}})

	runFix := func(round int) string {
		t.Helper()
		findings, err := collectReanchorFindings(goRoot, dshRoot, table)
		if err != nil {
			t.Fatalf("第 %d 轮 collectReanchorFindings() error = %v", round, err)
		}
		if _, _, err := applyReanchorFixes(findings); err != nil {
			t.Fatalf("第 %d 轮 applyReanchorFixes() error = %v", round, err)
		}
		data, err := os.ReadFile(goFile)
		if err != nil {
			t.Fatalf("第 %d 轮读回 Go 文件失败：%v", round, err)
		}
		return string(data)
	}

	first := runFix(1)
	want := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":3-8（Alpha）\n" +
		"// 说明：这句话不该被动到。\n" +
		"type Alpha struct{}\n\n" +
		"// 源: " + fixtureUpstreamPath + ":10-14（beta）\n" +
		"func Beta() {}\n"
	if first != want {
		t.Fatalf("修复结果不对。\n期望：\n%s\n实际：\n%s", want, first)
	}

	if second := runFix(2); second != first {
		t.Errorf("-fix 不幂等，第二轮又改了。\n第一轮：\n%s\n第二轮：\n%s", first, second)
	}
}

// TestReanchorFixRefusesWeakAnchors 钉住 -fix 不碰弱锚点推出来的漂移。
//
// 这是这份工具最危险的一条路。第一版把四种来路的锚点混成一串平铺的候选名，于是
// 「拿 Go 声明名去上游做大小写不敏感匹配碰上的同名东西」和「注释自己写明的符号名」
// 在结论里长得一模一样。前者根本不是证据——Go 侧的构造器、哨兵错误、测试函数名上游
// 一个都没有，一旦凑巧撞上某个无关的同名符号，-fix 就会把一条**正确**的行号改成错的。
// 那不是修复，是编出处，比放着不管更坏。
//
// 用 `Delta` 撞上游的 `delta`：裁决表留空，锚点只能退到 Go 声明名，且必须放宽大小写
// 才命中——两道闸各自都该拦住它，所以这一条同时验了两道。
func TestReanchorFixRefusesWeakAnchors(t *testing.T) {
	t.Parallel()

	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":1-2\nfunc Delta() {}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.status != statusDrift {
		t.Fatalf("引的 1-2 对不上 delta 的 20-20，该判 DRIFT，实际 %s", finding.status)
	}
	if finding.tier != tierDeclName {
		t.Errorf("锚点该记成退化的 Go 声明名一级，实际 %s", finding.tier)
	}
	if !finding.loose {
		t.Error("Delta 命中上游的 delta 靠的是放宽大小写，该记下来")
	}
	if finding.fixable() {
		t.Error("弱锚点推出来的漂移不该交给 -fix：改了就是编出处")
	}

	changed, _, err := applyReanchorFixes(findings)
	if err != nil {
		t.Fatalf("applyReanchorFixes() error = %v", err)
	}
	if changed != 0 {
		t.Errorf("不该改任何一条，实际改了 %d 条", changed)
	}

	data, err := os.ReadFile(filepath.Join(goRoot, "ported.go"))
	if err != nil {
		t.Fatalf("读回 Go 文件失败：%v", err)
	}
	if string(data) != goSource {
		t.Errorf("文件该原样不动，实际：\n%s", data)
	}
}

// TestReanchorFixLeavesInnerCitationsAlone 钉住「引的范围落在算出的范围里面」不许被改。
//
// 上游一个三百行的 `apply` 在 Go 侧被拆成十几个方法，每个只引其中几行；而锚点只能从
// 外层声明名反查出来，于是必然对不上。那种注释引得比锚点窄是**对的**，把它改成整个
// 函数的跨度等于把它引丢了——本来精确指到几行，改完只剩「在这个大函数里某处」。
func TestReanchorFixLeavesInnerCitationsAlone(t *testing.T) {
	t.Parallel()

	// 引 beta（10-14）内部的 11-12。
	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":11-12（beta）\nfunc Beta() {}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.tier != tierExplicit {
		t.Fatalf("锚点是注释自己写的，该记成最强一级，实际 %s", finding.tier)
	}
	if finding.fixable() {
		t.Error("引的是锚点内部的一小段，不该被改宽")
	}

	changed, _, err := applyReanchorFixes(findings)
	if err != nil {
		t.Fatalf("applyReanchorFixes() error = %v", err)
	}
	if changed != 0 {
		t.Errorf("不该改任何一条，实际改了 %d 条", changed)
	}
}

// TestReanchorFixClosesJSDocBoundary 钉住那一种该改的「包含」：只漏了 JSDoc 抬头。
//
// Alpha 的真实跨度是 3-8（3-5 是 doc，6-8 是声明本体）。一条只引 6-8 的注释落在
// 真实跨度里面，但它和「引了大函数内部一小段」是两件事：终点一模一样，差的只是抬头。
// 全仓有 114 条属于这一类，不认它们就等于把这批已经定位准了的漂移永远留在报告里。
func TestReanchorFixClosesJSDocBoundary(t *testing.T) {
	t.Parallel()

	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":6-8（Alpha）\ntype Alpha struct{}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.status != statusDrift {
		t.Fatalf("引的范围和算出的不等，该判 DRIFT，实际 %s", finding.status)
	}
	if !finding.jsdocBoundary() {
		t.Fatalf("终点相同、起点早 3 行，该认成 JSDoc 抬头缺失")
	}
	if !finding.fixable() {
		t.Fatal("这一类该交给 -fix")
	}

	changed, _, err := applyReanchorFixes(findings)
	if err != nil {
		t.Fatalf("applyReanchorFixes() error = %v", err)
	}
	if changed != 1 {
		t.Fatalf("应当改 1 条，实际 %d 条", changed)
	}

	data, err := os.ReadFile(filepath.Join(goRoot, "ported.go"))
	if err != nil {
		t.Fatalf("读回 Go 文件失败：%v", err)
	}
	if !strings.Contains(string(data), fixtureUpstreamPath+":3-8（Alpha）") {
		t.Errorf("该把跨度补成 3-8，实际内容：\n%s", string(data))
	}
}

// TestReanchorFixKeepsWideInnerCitation 钉住 JSDoc 那条例外的边界。
//
// 同样落在 Alpha 的 3-8 里面，但起点差了 4 行——超出 [jsdocBoundarySlack]，
// 说明它更可能真的是在引声明内部，不能替它拓宽。
func TestReanchorFixKeepsWideInnerCitation(t *testing.T) {
	t.Parallel()

	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":7-8（Alpha）\ntype Alpha struct{}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	if findings[0].jsdocBoundary() {
		t.Error("起点差 4 行已经超出宽限，不该认成 JSDoc 抬头缺失")
	}
	if findings[0].fixable() {
		t.Error("超出宽限的包含不该被自动改宽")
	}
}

// fixtureMovedPath 是「上游把符号搬去了别的文件」那一档用的第二个上游文件。
const fixtureMovedPath = "packages/core/fixture/src/moved.ts"

// fixtureMovedTS 里的 omega 真实跨度是 3-6（3-5 是 doc）。
var fixtureMovedTS = []string{
	/* 1 */ "import { Foo } from './foo'",
	/* 2 */ "",
	/* 3 */ "/**",
	/* 4 */ " * omega 的文档。",
	/* 5 */ " */",
	/* 6 */ "export const omega = 1",
}

// writeUpstreamFixture 往假上游里再放一个文件。
func writeUpstreamFixture(t *testing.T, dshRoot, relPath string, lines []string) {
	t.Helper()

	full := filepath.Join(dshRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写上游夹具失败：%v", err)
	}
}

// TestReanchorRelocatesOnExactGoRef 钉住 MOVED：锚点在引的文件里找不到，但在裁决表
// 记的那个文件里唯一命中，说明上游搬了文件。
//
// 这一档是必需的，因为 alpha.3 把 `packages/acp/acp/src/index.ts` 拆成了八个文件。
// 那批注释引的**文件名**已经过期，只改行号改不对——改完仍然指着一个不存在的符号，
// 而报告会显示 OK。
func TestReanchorRelocatesOnExactGoRef(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":99\nfunc Omega() {}\n")
	writeUpstreamFixture(t, dshRoot, fixtureMovedPath, fixtureMovedTS)

	table := newAnchorTable([]rulingtable.Row{{
		Package:  "core/fixture",
		File:     "src/moved.ts",
		Line:     6,
		Kind:     "variable",
		Name:     "omega",
		Decision: rulingtable.Ported,
		GoRef:    "fixture.Omega",
	}})

	findings, err := collectReanchorFindings(goRoot, dshRoot, table)
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.status != statusMoved {
		t.Fatalf("锚点在 moved.ts 里唯一命中，该判 MOVED，实际 %s（%s）", finding.status, finding.detail)
	}
	if finding.movedPath != fixtureMovedPath {
		t.Errorf("搬去的路径期望 %q，实际 %q", fixtureMovedPath, finding.movedPath)
	}
	if finding.foundStart != 3 || finding.foundEnd != 6 {
		t.Errorf("跨度期望 3-6，实际 %d-%d", finding.foundStart, finding.foundEnd)
	}
	if !finding.fixable() {
		t.Fatal("裁决表带路径的唯一命中该交给 -fix")
	}

	changed, _, err := applyReanchorFixes(findings)
	if err != nil {
		t.Fatalf("applyReanchorFixes() error = %v", err)
	}
	if changed != 1 {
		t.Fatalf("应当改 1 条，实际 %d 条", changed)
	}

	data, err := os.ReadFile(filepath.Join(goRoot, "ported.go"))
	if err != nil {
		t.Fatalf("读回 Go 文件失败：%v", err)
	}
	// 路径和行号要一起换掉，锚点也要固化进去。
	if !strings.Contains(string(data), "// 源: "+fixtureMovedPath+":3-6（omega）") {
		t.Errorf("该把路径和行号一起改掉，实际内容：\n%s", string(data))
	}
	if strings.Contains(string(data), fixtureUpstreamPath+":99") {
		t.Errorf("旧的路径和行号该被替换掉，实际内容：\n%s", string(data))
	}
}

// TestReanchorRefusesRelocateOnLastSegmentOnly 钉住搬家的唯一凭据：go_ref 全等。
//
// 第一版把「末段相等」的候选也带上了路径，于是 `Config`、`Beta` 这种末段在全仓命中
// 上百行的名字会让工具**给注释编一个完全无关的出处**——实测编出过
// `cmd/llmmockserver/main.go` 指向 `schedule/src/transaction.ts`、
// `compaction/invariant.go` 指向 `storage-domain/spec.ts` 这种。
// 这类错误从 diff 上看是路径和行号一起变了，比不改更难发现，所以宁可留在 NOT_FOUND 里。
func TestReanchorRefusesRelocateOnLastSegmentOnly(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":99\nfunc Omega() {}\n")
	writeUpstreamFixture(t, dshRoot, fixtureMovedPath, fixtureMovedTS)

	// go_ref 是 other.Omega：末段和 Go 声明名 Omega 相等，但全名对不上。
	table := newAnchorTable([]rulingtable.Row{{
		Package:  "core/fixture",
		File:     "src/moved.ts",
		Line:     6,
		Kind:     "variable",
		Name:     "omega",
		Decision: rulingtable.Ported,
		GoRef:    "other.Omega",
	}})

	findings, err := collectReanchorFindings(goRoot, dshRoot, table)
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	finding := findings[0]
	if finding.status != statusNotFound {
		t.Fatalf("末段相等不足以搬文件，该留在 NOT_FOUND，实际 %s（搬去 %q）",
			finding.status, finding.movedPath)
	}
	if finding.movedPath != "" {
		t.Errorf("不该记下任何搬家路径，实际 %q", finding.movedPath)
	}
	if finding.fixable() {
		t.Error("这一条不该交给 -fix")
	}
}

// TestReanchorRefusesRelocateOnSharedGoRef 钉住搬家的第二道闸：一个 go_ref 上并了
// 好几个上游符号时，只认名字就是这个 Go 声明名的那个候选。
//
// 实测的坑：`acp.Bridge.onSessionEvent` 在裁决表上同时并着 `toolCallUpdate` 和
// `toolResultUpdate`，而 onSessionEvent 的注释白纸黑字写着「工具……一条都不发」。
// 没有这道闸，工具会把一条讲会话事件分发的注释搬到 updates.ts 的某个工具更新函数上。
func TestReanchorRefusesRelocateOnSharedGoRef(t *testing.T) {
	t.Parallel()

	goRoot, dshRoot := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":99\nfunc Dispatch() {}\n")
	writeUpstreamFixture(t, dshRoot, fixtureMovedPath, fixtureMovedTS)

	// 一个 go_ref 并了两个上游符号，两个都不叫 Dispatch。
	rows := []rulingtable.Row{{
		Package: "core/fixture", File: "src/moved.ts", Line: 6, Kind: "variable",
		Name: "omega", Decision: rulingtable.Ported, GoRef: "fixture.Dispatch",
	}, {
		Package: "core/fixture", File: "src/moved.ts", Line: 1, Kind: "variable",
		Name: "psi", Decision: rulingtable.Ported, GoRef: "fixture.Dispatch",
	}}

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(rows))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	if findings[0].status != statusNotFound {
		t.Fatalf("一个 go_ref 并了多个符号且都不是这个声明名，不该搬，实际 %s（搬去 %q）",
			findings[0].status, findings[0].movedPath)
	}

	// 反过来：同样并了两个，但其中一个名字就是 Go 声明名，那一个该搬。
	goRoot2, dshRoot2 := reanchorFixture(t, "package fixture\n\n"+
		"// 源: "+fixtureUpstreamPath+":99\nfunc Omega() {}\n")
	writeUpstreamFixture(t, dshRoot2, fixtureMovedPath, fixtureMovedTS)

	rows[0].GoRef, rows[1].GoRef = "fixture.Omega", "fixture.Omega"
	findings, err = collectReanchorFindings(goRoot2, dshRoot2, newAnchorTable(rows))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应当收出 1 条发现，实际 %d 条", len(findings))
	}
	if findings[0].status != statusMoved || findings[0].movedPath != fixtureMovedPath {
		t.Errorf("名字和 Go 声明名一致的那个候选该搬，实际 %s（搬去 %q）",
			findings[0].status, findings[0].movedPath)
	}
}

// TestReanchorFixLeavesNonDriftAlone 钉住 -fix 的管辖范围。
//
// OK / CONTAINS / NO_ANCHOR 这些都不该被碰。尤其是 CONTAINS——把一条正当的大范围
// 溯源「收窄」到单个符号上，是在悄悄丢掉信息。
func TestReanchorFixLeavesNonDriftAlone(t *testing.T) {
	t.Parallel()

	goSource := "package fixture\n\n" +
		"// 源: " + fixtureUpstreamPath + ":1-26（Alpha）\nfunc Wide() {}\n\n" +
		"// 源: " + fixtureUpstreamPath + ":10-14（beta）\nfunc Beta() {}\n"
	goRoot, dshRoot := reanchorFixture(t, goSource)

	findings, err := collectReanchorFindings(goRoot, dshRoot, newAnchorTable(nil))
	if err != nil {
		t.Fatalf("collectReanchorFindings() error = %v", err)
	}
	changed, _, err := applyReanchorFixes(findings)
	if err != nil {
		t.Fatalf("applyReanchorFixes() error = %v", err)
	}
	if changed != 0 {
		t.Errorf("CONTAINS 和 OK 都不该被改，实际改了 %d 条", changed)
	}

	data, err := os.ReadFile(filepath.Join(goRoot, "ported.go"))
	if err != nil {
		t.Fatalf("读回 Go 文件失败：%v", err)
	}
	if string(data) != goSource {
		t.Errorf("文件不该被改动。\n期望：\n%s\n实际：\n%s", goSource, string(data))
	}
}
