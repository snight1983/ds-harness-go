// 本文件的作用：门禁之外的那一层——**审查报告**。
//
// 门禁（[runCheck]）管的是「有没有漏看」和「填的名字存不存在」，它一直是绿的，
// 而绿的门禁和「移植对了」是两回事。这个模式回答后者的前半程：把 1433 条 PORTED
// 里**机器看得出可疑**的那些挑出来，压成一份可以逐条走的工作队列，让人的时间
// 花在真正要读两边源码的地方。
//
// 新增: DSH 没有对应物——它不是被移植过来的，是这次移植自己需要的账本工具。
//
// 四份报告，按「机器有多确定」从强到弱排：
//
//  1. **kind 交叉检查**：上游一个 `function` 落在 Go 的一个 `type` 上，几乎一定是
//     填错了格子。这一份是四份里唯一接近「一定有问题」的。
//
//  2. **非导出 go_ref**：上游公开的能力落在一个 Go 侧调不到的符号上。可能合法
//     （上游那个导出本来就只给它自己的测试用），但每一条都得有人说出那句话。
//
//  3. **go_ref 塌缩**：多条上游符号并进同一个 Go 符号。本仓库拿一个带判别字段的
//     结构体接住一整族 TS 判别联合是既定做法，所以塌缩本身不是错——**没写明白
//     并了哪些、判别字段是什么**才是。
//
//  4. **溯源密度**：一个包的 `// 源:` 注释密度远低于全仓中位数，说明这段代码多半
//     是照着记忆写的而不是照着源码写的。它指不出具体哪一行错，只指出该去哪儿细看。
//
// 外加一份和上面四份不同类的：
//
//  5. **包文档**：一个包没有包文档、有两份、或者把文件说明当成了包文档。它跟移植准不准
//     没关系，跟下一个读这份代码的人有关系。放进来是因为它**只能靠工具发现**——
//     第二种毛病编译得过、`go doc` 也有输出，写的人得不到任何提示。
//
// 这五份**都不阻断门禁**。它们的输出是给人排队用的，不是判决；一条报出来的行
// 可能完全正当，而把正当的东西判红会训练人去忽略红色。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go/ast"
	"go/parser"
	"go/token"

	"github.com/snight1983/ds-harness-go/internal/devtools/rulingtable"
)

// symbolKind 是一个 Go 顶层声明的类别，[collectGoSymbols] 记的就是它。
//
// 只分到这个粒度：再细（struct/interface/alias）对上游那一列 kind 没有对应物，
// 分了也无从对质。
type symbolKind string

const (
	kindType   symbolKind = "type"
	kindConst  symbolKind = "const"
	kindVar    symbolKind = "var"
	kindFunc   symbolKind = "func"
	kindMethod symbolKind = "method"
)

// expectedGoKinds 是上游一个 kind 在 Go 侧**可以**落到的类别。
//
// 不在这张表上的上游 kind（reexport、reexport-local、default、star）一律不检查：
// 一条 re-export 本身没有形状，它的形状是它转出去的那个东西的，而清单不记那个。
// 宁可漏报也不误报——这份报告的价值全在「报出来的都值得看一眼」。
var expectedGoKinds = map[string][]symbolKind{
	// TS 的三种类型声明都只能落成 Go 的一个 type。
	"interface":           {kindType},
	"type":                {kindType},
	"class":               {kindType},
	"reexport-type":       {kindType},
	"reexport-local-type": {kindType},

	// 函数落成函数或方法都正当：本仓库常把一族自由函数收编成某个类型上的方法。
	"function": {kindFunc, kindMethod},

	// const 这一支最松。TS 的 `const` 大量是对象字面量，Go 的 const 存不下，
	// 落成 var 是常态；而一份「不可变的默认值」在 Go 里做成**交回新值的函数**
	// 是本仓库明确的做法（见 subagent.NoStartCapabilities 那段注释），
	// 所以 func/method 也放行。落成 type 才是可疑的。
	"const": {kindConst, kindVar, kindFunc, kindMethod},
}

// auditFinding 是四份报告共用的一条：定位 + 一句话说清为什么被挑出来。
type auditFinding struct {
	pkg    string
	name   string
	goRef  string
	detail string
}

// runAudit 出五份审查报告，写成一份 Markdown，并在终端上打一份摘要。
//
// 它**不**返回「有发现」这种错误：报告有内容是预期状态，退出码非零只留给
// 真正的 IO 和解析故障。
func runAudit(rulingPath, goRoot, outPath string) error {
	rows, err := rulingtable.ReadRuling(rulingPath)
	if err != nil {
		return err
	}
	symbols, err := collectGoSymbols(goRoot)
	if err != nil {
		return err
	}
	density, err := collectProvenanceDensity(goRoot)
	if err != nil {
		return err
	}

	ported := make([]rulingtable.Row, 0, len(rows))
	for _, row := range rows {
		if row.Decision == rulingtable.Ported && strings.TrimSpace(row.GoRef) != "" {
			ported = append(ported, row)
		}
	}

	mismatches := auditKindMismatches(ported, symbols)
	unexported := auditUnexportedRefs(ported)
	collapses := auditCollapses(ported)
	thin := auditThinProvenance(density)
	docs := auditPackageDocs(density)

	var out strings.Builder
	writeAuditHeader(&out, len(rows), len(ported))
	writeKindMismatches(&out, mismatches)
	writeUnexported(&out, unexported)
	writeCollapses(&out, collapses)
	writeThinProvenance(&out, thin, density)
	writePackageDocs(&out, docs, len(density))

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, []byte(out.String()), 0o644); err != nil {
		return err
	}

	fmt.Printf("审查报告已写出：%s\n", outPath)
	fmt.Printf("  PORTED 有 go_ref 的       %d 条\n", len(ported))
	fmt.Printf("  一、kind 对不上           %d 条\n", len(mismatches))
	fmt.Printf("  二、指向非导出符号        %d 条\n", len(unexported))
	fmt.Printf("  三、塌缩到同一个 go_ref   %d 组 / %d 条\n", len(collapses), countCollapsedRows(collapses))
	fmt.Printf("  四、溯源密度偏低的包      %d 个\n", len(thin))
	fmt.Printf("  五、包文档有毛病的包      %d 个\n", len(docs))
	return nil
}

// ---- 一、kind 交叉检查 ----

// auditKindMismatches 挑出上游 kind 和 Go 声明类别对不上的那些行。
//
// go_ref 指向一个收不到的符号，这里**不报**：那是门禁第四项的事，在这里再报一遍
// 只会让两份清单互相稀释。
func auditKindMismatches(ported []rulingtable.Row, symbols map[string]symbolKind) []auditFinding {
	var findings []auditFinding
	for _, row := range ported {
		want, checkable := expectedGoKinds[row.Kind]
		if !checkable {
			continue
		}
		ref := strings.TrimSpace(row.GoRef)
		got, known := symbols[ref]
		if !known {
			continue
		}
		if slicesContains(want, got) {
			continue
		}
		detail := fmt.Sprintf("上游是 %s，Go 侧是 %s（该是 %s）", row.Kind, got, joinKinds(want))
		findings = append(findings, auditFinding{
			pkg:    row.Package,
			name:   row.Name,
			goRef:  ref,
			detail: detail + describeNote(row),
		})
	}
	sortFindings(findings)
	return findings
}

// ---- 二、非导出 go_ref ----

// auditUnexportedRefs 挑出 go_ref 里含小写开头路径段的行。
//
// 认的是**包名之后的每一段**：`pkg.Type.method` 和 `pkg.helper` 一样是下游调不到的。
// 上游把它公开出来了、Go 侧却没有，这中间的差额必须有人说清楚是有意的。
func auditUnexportedRefs(ported []rulingtable.Row) []auditFinding {
	var findings []auditFinding
	for _, row := range ported {
		ref := strings.TrimSpace(row.GoRef)
		segments := strings.Split(ref, ".")
		if len(segments) < 2 {
			continue // 连包名都没有的 go_ref 不在这份报告的管辖里
		}
		var hidden []string
		for _, segment := range segments[1:] {
			if segment != "" && isLowerStart(segment) {
				hidden = append(hidden, segment)
			}
		}
		if len(hidden) == 0 {
			continue
		}
		detail := fmt.Sprintf("非导出的一段：%s", strings.Join(hidden, "、")) + describeNote(row)
		findings = append(findings, auditFinding{
			pkg: row.Package, name: row.Name, goRef: ref, detail: detail,
		})
	}
	sortFindings(findings)
	return findings
}

// ---- 三、go_ref 塌缩 ----

// collapseGroup 是一组并到同一个 Go 符号上的上游符号。
type collapseGroup struct {
	goRef   string
	members []rulingtable.Row
}

// auditCollapses 找出被两条以上非 re-export 的 PORTED 共用的 go_ref。
//
// **排除 re-export 是这份报告能用的前提。** 一个 TS 包同时声明并转出同一个类型，
// 清单里就是两行指向同一个 Go 符号——那是清单的形状，不是移植的问题。
// 不排除的话这份报告有 664 条，排除之后只有 125 条，而后者才全是真要看的。
func auditCollapses(ported []rulingtable.Row) []collapseGroup {
	byRef := map[string][]rulingtable.Row{}
	for _, row := range ported {
		if strings.HasPrefix(row.Kind, "reexport") {
			continue
		}
		ref := strings.TrimSpace(row.GoRef)
		byRef[ref] = append(byRef[ref], row)
	}
	var groups []collapseGroup
	for ref, members := range byRef {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
		groups = append(groups, collapseGroup{goRef: ref, members: members})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].members) != len(groups[j].members) {
			return len(groups[i].members) > len(groups[j].members)
		}
		return groups[i].goRef < groups[j].goRef
	})
	return groups
}

func countCollapsedRows(groups []collapseGroup) int {
	total := 0
	for _, group := range groups {
		total += len(group.members)
	}
	return total
}

// ---- 四、溯源密度 ----

// packageProvenance 是一个 Go 包目录的溯源统计。
type packageProvenance struct {
	pkg     string
	lines   int
	sources int // `// 源:` 条数
	added   int // `// 新增:` 条数

	// declaredNew 记的是「这个包的**包文档**里有一条 `新增:`」。
	//
	// 它单独记一位而不是复用 added，因为位置就是语义：一条写在某个函数头上的
	// `新增:` 说的是那个函数偏离了上游，而一条写在 package 子句正上方的
	// `新增:` 说的是**整个包**没有上游对应物。后者配上「一条 `源:` 都没有」，
	// 就是一份已经交代过的零溯源，不该再被当成缺口报一遍。
	declaredNew bool

	// docFiles 是这个包里带包文档的非测试文件名，按遍历序。
	//
	// 记的是**文件名的列表**而不是一个布尔，因为这份报告要分辨的三种毛病里有两种
	// 只有列表才看得出来：一个都没有（go doc 出来是空的），和有两个以上
	// （go doc 只会取排在前面那一个，另一份等于白写，而写的人不会知道）。
	docFiles []string

	// headerDocFiles 是 docFiles 里那些「其实是文件说明」的。
	//
	// 见 [docHeaderPrefix]。
	headerDocFiles []string
}

// density 是每千行非测试代码的 `// 源:` 条数。
func (p packageProvenance) density() float64 {
	if p.lines == 0 {
		return 0
	}
	return float64(p.sources) * 1000 / float64(p.lines)
}

// thinProvenanceThreshold 是「密度偏低」的门槛，单位是条/千行。
//
// 全仓是 37 条/千行上下。25 这个数不是算出来的，是看着分布挑的：它把尾部那二十来个包
// 分出来。门槛的意义只是排队顺序，不是判决，所以不必精确。
const thinProvenanceThreshold = 25.0

// collectProvenanceDensity 按目录统计非测试代码的行数和溯源注释条数。
//
// 用 go/parser 数注释而不是逐行正则，理由和 [checkProvenance] 一模一样：
// 字符串字面量里长得像注释的东西不是注释。行数则直接数源文件的行——它只是个分母。
func collectProvenanceDensity(goRoot string) ([]packageProvenance, error) {
	byDir := map[string]*packageProvenance{}

	err := filepath.WalkDir(goRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "tmp", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s 解析失败，无法统计其中的溯源注释：%w", path, err)
		}

		relative, err := filepath.Rel(goRoot, filepath.Dir(path))
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		stats, seen := byDir[key]
		if !seen {
			stats = &packageProvenance{pkg: key}
			byDir[key] = stats
		}

		stats.lines += countLines(path)
		if declaresNoUpstream(parsed.Doc) {
			stats.declaredNew = true
		}
		if parsed.Doc != nil {
			name := filepath.Base(path)
			stats.docFiles = append(stats.docFiles, name)
			if isFileHeader(parsed.Doc) {
				stats.headerDocFiles = append(stats.headerDocFiles, name)
			}
		}
		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				switch {
				case reAdded.MatchString(comment.Text):
					stats.added++
				case reSourceLine.MatchString(comment.Text):
					stats.sources++
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]packageProvenance, 0, len(byDir))
	for _, stats := range byDir {
		result = append(result, *stats)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].density() != result[j].density() {
			return result[i].density() < result[j].density()
		}
		return result[i].pkg < result[j].pkg
	})
	return result, nil
}

// auditThinProvenance 挑出密度低于门槛的包。
//
// 两类排除，都是为了不让这份报告的前几名被「本来就该是零」的包占满：
//
//   - 本仓自造的门禁工具（internal/devtools/…）没有上游对应物，一条溯源都不该有。
//     判据认的是这个路径，不是「叫 tools 的目录」：顶层 `tools/` 是工具**契约**包，
//     照着上游译过来的，一条溯源都不能少。
//   - 包文档里写了 `新增:` 且全包一条 `源:` 都没有的包，等于已经在最显眼的地方
//     交代过「这个包整份是新写的」。adapter/objectstore 就是这样一个包：DSH 那边挂在
//     fs 接缝上的实现是 fs-local 和 fs-sandbox，两个都碰机器资源、都在范围外，
//     服务端要的对象存储后端只能新写。再把它报成缺口是在罚一份已经写好的交代。
//
// **只在零溯源时才认这条豁免。** 一个包既有 `源:` 又在包文档里写 `新增:` 是常态
// （多数包都这样：整体照着上游译，某处刻意偏离），那种包的密度偏低仍然是真信号。
func auditThinProvenance(all []packageProvenance) []packageProvenance {
	var thin []packageProvenance
	for _, stats := range all {
		if strings.HasPrefix(stats.pkg, "internal/devtools/") {
			continue
		}
		if stats.sources == 0 && stats.declaredNew {
			continue
		}
		if stats.density() < thinProvenanceThreshold {
			thin = append(thin, stats)
		}
	}
	return thin
}

// ---- 五、包文档 ----

// docHeaderPrefix 是本仓库文件说明的开头。
//
// 本仓库的写法是：每份文件顶上一条 `本文件的作用：…` 讲这一份文件，和 package 子句
// 之间**空一行**隔开，于是它不是包文档；包文档另写，多数落在 doc.go 里（全仓 58 个包
// 是这么做的）。少掉那个空行，Go 就把这条文件说明当成了整个包的文档——
// 编译器不会说话，`go doc` 也照样有输出，只是内容答非所问。
const docHeaderPrefix = "本文件的作用"

// docFinding 是一个包的包文档毛病。
type docFinding struct {
	pkg    string
	detail string
}

// auditPackageDocs 挑出包文档有毛病的包。
//
// 三种毛病，按「读的人受多大影响」排：
//
//  1. **一个都没有**：`go doc` 这个包出来是空的，pkg.go.dev 上也是空的。
//  2. **文件说明被当成了包文档**：少了 package 子句前那个空行。这一种最阴，
//     因为它有输出、看着正常，只是讲的是某一份文件而不是这个包。
//  3. **两个以上文件都带包文档**：`go doc` 只取排在前面那一个，另一份白写，
//     而写的人得不到任何提示。
//
// 三者不互斥（一个包可以同时中第二和第三条），所以一个包只出一条发现，把中了的
// 都写进 detail 里——否则同一个包会在报告里出现两次，看的人以为是两件事。
func auditPackageDocs(all []packageProvenance) []docFinding {
	var findings []docFinding
	for _, stats := range all {
		var troubles []string
		switch {
		case len(stats.docFiles) == 0:
			troubles = append(troubles, "**没有包文档**（`go doc` 这个包出来是空的）")
		case len(stats.docFiles) > 1:
			troubles = append(troubles, fmt.Sprintf(
				"**%d 个文件都带包文档**（%s），`go doc` 只取排在最前的那一个，其余白写",
				len(stats.docFiles), strings.Join(stats.docFiles, "、")))
		}
		if len(stats.headerDocFiles) > 0 {
			troubles = append(troubles, fmt.Sprintf(
				"**文件说明被当成了包文档**（%s 少了 package 前那个空行）",
				strings.Join(stats.headerDocFiles, "、")))
		}
		if len(troubles) == 0 {
			continue
		}
		findings = append(findings, docFinding{pkg: stats.pkg, detail: strings.Join(troubles, "；")})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].pkg < findings[j].pkg })
	return findings
}

// isFileHeader 判断一段包文档其实是一条文件说明。
//
// 只看第一行：`本文件的作用：` 是本仓库文件说明的固定开头，而一份真的包文档
// 第一行必然是 `Package X …`（Go 的惯例，`go vet` 之外的工具也认这个）。
func isFileHeader(doc *ast.CommentGroup) bool {
	if doc == nil || len(doc.List) == 0 {
		return false
	}
	first := strings.TrimSpace(strings.TrimPrefix(doc.List[0].Text, "//"))
	return strings.HasPrefix(first, docHeaderPrefix)
}

// ---- 报告渲染 ----

func writeAuditHeader(out *strings.Builder, totalRows, portedRows int) {
	out.WriteString("# 移植审查发现\n\n")
	out.WriteString("由 `go run ./internal/devtools/portcheck -mode audit` 生成，**不要手工编辑**——它每次会被整份覆盖。\n\n")
	out.WriteString("这五份都不是判决，是**工作队列**：每一条要么改代码、要么在裁决表的 `note` 列写明为什么它是对的。\n\n")
	fmt.Fprintf(out, "裁决表 %d 行，其中 PORTED 且填了 `go_ref` 的 %d 条。\n\n", totalRows, portedRows)
}

func writeKindMismatches(out *strings.Builder, findings []auditFinding) {
	out.WriteString("## 一、kind 对不上（最强信号）\n\n")
	out.WriteString("上游的声明类别和 `go_ref` 指向的 Go 声明类别不一致。四份里唯一接近「一定填错了」的一份。\n\n")
	if len(findings) == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	out.WriteString("| 上游包 | 上游符号 | go_ref | 问题 |\n|---|---|---|---|\n")
	for _, finding := range findings {
		fmt.Fprintf(out, "| %s | `%s` | `%s` | %s |\n", finding.pkg, finding.name, finding.goRef, finding.detail)
	}
	out.WriteString("\n")
}

func writeUnexported(out *strings.Builder, findings []auditFinding) {
	out.WriteString("## 二、go_ref 指向非导出符号\n\n")
	out.WriteString("上游公开的能力，Go 侧下游调不到。合法的情形是「上游那个导出本来就只给它自己用」——")
	out.WriteString("但那句话必须写在裁决表的 `note` 列里，写不出来的就是真缺口。\n\n")
	if len(findings) == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	out.WriteString("| 上游包 | 上游符号 | go_ref | 说明 |\n|---|---|---|---|\n")
	for _, finding := range findings {
		fmt.Fprintf(out, "| %s | `%s` | `%s` | %s |\n", finding.pkg, finding.name, finding.goRef, finding.detail)
	}
	out.WriteString("\n")
}

func writeCollapses(out *strings.Builder, groups []collapseGroup) {
	out.WriteString("## 三、多条上游符号塌缩到同一个 go_ref\n\n")
	out.WriteString("已排除 re-export（一个包同时声明并转出同一个类型是清单的形状，不是移植的问题）。\n\n")
	out.WriteString("拿一个带判别字段的结构体接住一整族 TS 判别联合是本仓库的既定做法，所以**塌缩本身不是错**。")
	out.WriteString("要查的是：那个 Go 类型的注释里有没有写明并进来了哪几个上游形态、判别字段是什么、")
	out.WriteString("以及上游靠类型窄化保证的那些约束在 Go 里由谁来保证。\n\n")
	if len(groups) == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	fmt.Fprintf(out, "共 %d 组 / %d 条。\n\n", len(groups), countCollapsedRows(groups))
	out.WriteString("| go_ref | 条数 | 并进来的上游符号 |\n|---|---|---|\n")
	for _, group := range groups {
		var members []string
		for _, row := range group.members {
			members = append(members, fmt.Sprintf("`%s`(%s)", row.Name, row.Kind))
		}
		fmt.Fprintf(out, "| `%s` | %d | %s |\n", group.goRef, len(group.members), strings.Join(members, "、"))
	}
	out.WriteString("\n")
}

func writeThinProvenance(out *strings.Builder, thin, all []packageProvenance) {
	out.WriteString("## 四、溯源密度偏低的包\n\n")
	totalLines, totalSources := 0, 0
	for _, stats := range all {
		totalLines += stats.lines
		totalSources += stats.sources
	}
	overall := 0.0
	if totalLines > 0 {
		overall = float64(totalSources) * 1000 / float64(totalLines)
	}
	fmt.Fprintf(out, "全仓 %d 条 `// 源:` / %d 行非测试代码 = **%.1f 条/千行**。低于 %.0f 条/千行的列在下面。\n\n",
		totalSources, totalLines, overall, thinProvenanceThreshold)
	out.WriteString("密度低不等于写错了，它只说明这段代码多半是照着记忆写的而不是照着源码写的——")
	out.WriteString("**这一份指的是该去哪儿细读，不是哪一行有 bug**。两类包已排除：本仓自造的 `internal/devtools/`，")
	out.WriteString("以及包文档里写了 `新增:` 且全包零条 `源:` 的包——后者已经在最显眼的地方交代过自己整份是新写的。\n\n")
	if len(thin) == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	out.WriteString("| 包 | 非测试行数 | `// 源:` | `// 新增:` | 条/千行 |\n|---|---:|---:|---:|---:|\n")
	for _, stats := range thin {
		fmt.Fprintf(out, "| %s | %d | %d | %d | %.1f |\n",
			stats.pkg, stats.lines, stats.sources, stats.added, stats.density())
	}
	out.WriteString("\n")
}

func writePackageDocs(out *strings.Builder, findings []docFinding, total int) {
	out.WriteString("## 五、包文档有毛病的包\n\n")
	out.WriteString("这一份和前四份不一样：它跟移植准不准没关系，跟**下一个读这份代码的人**有关系。\n\n")
	out.WriteString("本仓库的写法是每份文件顶上一条 `本文件的作用：…`，和 `package` 子句之间空一行隔开；")
	out.WriteString("包文档另写，多数落在 `doc.go` 里。少掉那个空行，Go 就把文件说明当成了整个包的文档——")
	out.WriteString("**编译器不会说话，`go doc` 也照样有输出，只是讲的是某一份文件而不是这个包**。")
	out.WriteString("这一种自己是看不出来的，只能靠这份报告。\n\n")
	fmt.Fprintf(out, "全仓 %d 个包，有毛病的 %d 个。\n\n", total, len(findings))
	if len(findings) == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	out.WriteString("| 包 | 毛病 |\n|---|---|\n")
	for _, finding := range findings {
		fmt.Fprintf(out, "| %s | %s |\n", finding.pkg, finding.detail)
	}
	out.WriteString("\n")
}

// ---- 小工具 ----

// declaresNoUpstream 判断一段**包文档**里有没有 `新增:` 这条声明。
//
// 收 *ast.CommentGroup 而不是整份注释表，是这个判断的全部要害：位置就是语义。
// 一条 `新增:` 写在某个函数头上说的是那个函数偏离了上游，写在 package 子句正上方
// 说的才是整个包没有上游对应物。
//
// doc 为 nil（这个文件没有包文档）时答 false，调用方按包里任一文件给出 true 就算数。
func declaresNoUpstream(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, comment := range doc.List {
		if reAdded.MatchString(comment.Text) {
			return true
		}
	}
	return false
}

// describeNote 把裁决表的 note 列接在一条发现后面，没填的显式标红。
//
// 报告的用法是「逐条要么改代码、要么写清为什么它是对的」，所以「有没有人已经说过话」
// 本身就是排队的第一依据——一条已经写了理由的发现只要复核那句话，一条没写的才要从头查。
func describeNote(row rulingtable.Row) string {
	if note := strings.TrimSpace(row.Note); note != "" {
		return "（裁决表已有理由：" + note + "）"
	}
	return "（**裁决表没写理由**）"
}

func sortFindings(findings []auditFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].pkg != findings[j].pkg {
			return findings[i].pkg < findings[j].pkg
		}
		return findings[i].name < findings[j].name
	})
}

func slicesContains(kinds []symbolKind, want symbolKind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func joinKinds(kinds []symbolKind) string {
	parts := make([]string, len(kinds))
	for index, kind := range kinds {
		parts[index] = string(kind)
	}
	return strings.Join(parts, "/")
}

// isLowerStart 判断一段标识符是不是小写开头，也就是 Go 里的「非导出」。
//
// 只看第一个字节：Go 的导出规则认的是首字符的大小写，而本仓库的标识符全是 ASCII。
func isLowerStart(segment string) bool {
	first := segment[0]
	return first >= 'a' && first <= 'z'
}
