// 本文件的作用：拿 dsh-exports.tsv 这份机器清单当基准，管住「抄全了没有」这件事。
//
// 它做两个动作：
//
//	-mode sync   把清单同步进裁决表。清单里新出现的符号一律以 PENDING 落进去，
//	             已经裁决过的行原样保留。**只增不删**——人可以往裁决表上填结论，
//	             但没有任何一条路径能让一个符号从表上消失。
//
//	-mode check  门禁。任何一条不满足就退出码非零。
//
// 为什么要有它：这次移植最大的风险不是写错，是**悄悄漏掉**——我判断某个东西
// 「用不上」于是不抄，而这个判断从来没有被人看见过。所以每一条「不抄」都必须
// 落在纸上，并且默认是红的（PENDING），只有人明确填了结论才会变绿。
// 门禁的默认答案是「不通过」，不是「通过」。
//
// 三条刻意的设计：
//
//   - **裁决表和清单必须严格一一对应。** 清单有而裁决表没有 → 有人跳过了 sync；
//     裁决表有而清单没有 → 有人手工加了一行不存在的符号。两种都是硬错误。
//
//   - **溯源注释要验真。** Go 代码里写 `// 源: packages/core/session/src/index.ts:425-760`
//     不算数，checker 会去打开那个文件、数那个行范围。引用一个不存在的文件或者
//     越界的行号，和凭空编一段出处，在结果上完全一样——所以必须机器验。
//
//   - **PORTED 必须指向真实存在的 Go 符号。** 否则「已移植」就只是一句自述。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ds-harness-go/tools/internal/rulingtable"
)

func main() {
	mode := flag.String("mode", "check", "sync（同步裁决表）或 check（跑门禁）")
	exports := flag.String("exports", `C:\code\ds-harness-go\docs\portmap\dsh-exports.tsv`, "机器清单路径")
	ruling := flag.String("ruling", `C:\code\ds-harness-go\docs\portmap\portmap.tsv`, "裁决表路径")
	goRoot := flag.String("go-root", `C:\code\ds-harness-go`, "Go 代码根目录")
	dshRoot := flag.String("dsh-root", `C:\codestudy\deepseek-harness-master`, "DSH 源码根目录，用来验溯源注释")
	flag.Parse()

	switch *mode {
	case "sync":
		if err := runSync(*exports, *ruling); err != nil {
			fmt.Fprintf(os.Stderr, "同步失败：%v\n", err)
			os.Exit(1)
		}
	case "check":
		if err := runCheck(*exports, *ruling, *goRoot, *dshRoot); err != nil {
			fmt.Fprintf(os.Stderr, "\n门禁未通过：%v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "未知的 -mode：%s\n", *mode)
		os.Exit(2)
	}
}

// runSync 把清单合并进裁决表。
//
// 合并规则只有两条，都是为了让「漏掉」这件事无处可藏：
//   - 清单里有、裁决表里没有的 → 以 PENDING 加进去；
//   - 裁决表里有、清单里没有的 → **不删**，标成 STALE 留在表上等人处理。
//
// 第二条尤其重要：一个符号从清单里消失，只可能是 DSH 源码变了或者提取器改了，
// 两种都需要人看一眼。静默删掉等于把这个信号扔了。
func runSync(exportsPath, rulingPath string) error {
	fresh, err := rulingtable.ReadExports(exportsPath)
	if err != nil {
		return err
	}
	existing, err := rulingtable.ReadRuling(rulingPath)
	if err != nil {
		return err
	}

	byKey := make(map[string]rulingtable.Row, len(existing))
	for _, row := range existing {
		byKey[row.Key()] = row
	}

	merged := make([]rulingtable.Row, 0, len(fresh))
	seen := make(map[string]bool, len(fresh))
	added := 0
	for _, row := range fresh {
		seen[row.Key()] = true
		if previous, ok := byKey[row.Key()]; ok {
			// 保留人填过的三列，其余六列以清单为准（清单是唯一基准）。
			row.Decision, row.GoRef, row.Note = previous.Decision, previous.GoRef, previous.Note
		} else {
			row.Decision = rulingtable.Pending
			added++
		}
		merged = append(merged, row)
	}

	stale := 0
	for _, row := range existing {
		if !seen[row.Key()] {
			row.Kind = "STALE:" + row.Kind
			merged = append(merged, row)
			stale++
		}
	}

	rulingtable.Sort(merged)
	if err := rulingtable.WriteRuling(rulingPath, merged); err != nil {
		return err
	}

	fmt.Printf("裁决表已同步：%s\n", rulingPath)
	fmt.Printf("清单 %d 条，新增 %d 条 PENDING，保留已裁决 %d 条\n",
		len(fresh), added, len(fresh)-added)
	if stale > 0 {
		fmt.Printf("注意：有 %d 条在清单里已经不存在了，已标成 STALE 留在表上，需要人确认。\n", stale)
	}
	return nil
}

// runCheck 是门禁。默认答案是不通过，每一项都要主动证明自己是绿的。
func runCheck(exportsPath, rulingPath, goRoot, dshRoot string) error {
	fresh, err := rulingtable.ReadExports(exportsPath)
	if err != nil {
		return err
	}
	rows, err := rulingtable.ReadRuling(rulingPath)
	if err != nil {
		return err
	}

	var failures []string

	// 一、清单与裁决表必须严格一一对应。
	inRuling := make(map[string]bool, len(rows))
	for _, row := range rows {
		inRuling[row.Key()] = true
	}
	missing := 0
	for _, row := range fresh {
		if !inRuling[row.Key()] {
			missing++
		}
	}
	if missing > 0 {
		failures = append(failures,
			fmt.Sprintf("清单里有 %d 条符号不在裁决表上——先跑 -mode sync", missing))
	}

	inExports := make(map[string]bool, len(fresh))
	for _, row := range fresh {
		inExports[row.Key()] = true
	}

	// 二、逐行检查裁决本身是否成立。
	counts := map[string]int{}
	var pendingSamples []string
	ghosts, stales := 0, 0
	for _, row := range rows {
		if strings.HasPrefix(row.Kind, "STALE:") {
			stales++
			continue
		}
		if !inExports[row.Key()] {
			ghosts++
			continue
		}
		counts[row.Decision]++

		if !rulingtable.ValidDecisions[row.Decision] {
			failures = append(failures,
				fmt.Sprintf("%s:%d %s 的裁决 %q 不是合法取值", row.File, row.Line, row.Name, row.Decision))
			continue
		}
		switch row.Decision {
		case rulingtable.Pending:
			if len(pendingSamples) < 10 {
				pendingSamples = append(pendingSamples,
					fmt.Sprintf("  %s %s:%d %s", row.Package, row.File, row.Line, row.Name))
			}
		case rulingtable.Ported:
			if strings.TrimSpace(row.GoRef) == "" {
				failures = append(failures,
					fmt.Sprintf("%s %s 标了 PORTED 但 go_ref 是空的", row.Package, row.Name))
			}
		case rulingtable.GoNative, rulingtable.Skip:
			if strings.TrimSpace(row.Note) == "" {
				failures = append(failures,
					fmt.Sprintf("%s %s 标了 %s 但没写理由", row.Package, row.Name, row.Decision))
			}
		}
	}
	if ghosts > 0 {
		failures = append(failures,
			fmt.Sprintf("裁决表上有 %d 条符号在清单里不存在——手工加的行不算数", ghosts))
	}
	if pending := counts[rulingtable.Pending]; pending > 0 {
		failures = append(failures, fmt.Sprintf("还有 %d 条没有裁决", pending))
	}

	// 三、溯源注释验真。
	provenance, provErrs, err := checkProvenance(goRoot, dshRoot)
	if err != nil {
		return err
	}
	failures = append(failures, provErrs...)

	// 四、PORTED 的 go_ref 必须指向真实存在的 Go 符号。
	symbols, err := collectGoSymbols(goRoot)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Decision != rulingtable.Ported {
			continue
		}
		ref := strings.TrimSpace(row.GoRef)
		if ref == "" {
			continue // 上面已经报过「go_ref 是空的」
		}
		if _, exists := symbols[ref]; !exists {
			failures = append(failures,
				fmt.Sprintf("%s %s 标了 PORTED，但 go_ref %q 在 Go 代码里找不到", row.Package, row.Name, ref))
		}
	}

	report(rows, counts, stales, provenance, pendingSamples)

	if len(failures) > 0 {
		fmt.Println("\n不通过的原因：")
		for _, failure := range failures {
			fmt.Printf("  - %s\n", failure)
		}
		return fmt.Errorf("共 %d 项", len(failures))
	}
	fmt.Println("\n门禁通过。")
	return nil
}

func report(rows []rulingtable.Row, counts map[string]int, stales, provenance int, pendingSamples []string) {
	fmt.Printf("裁决表共 %d 行\n", len(rows))
	kinds := make([]string, 0, len(counts))
	for decision := range counts {
		kinds = append(kinds, decision)
	}
	sort.Slice(kinds, func(i, j int) bool { return counts[kinds[i]] > counts[kinds[j]] })
	for _, decision := range kinds {
		fmt.Printf("  %-14s %d\n", decision, counts[decision])
	}
	if stales > 0 {
		fmt.Printf("  %-14s %d\n", "STALE", stales)
	}
	fmt.Printf("溯源注释：%d 条，全部验过出处\n", provenance)
	if len(pendingSamples) > 0 {
		fmt.Println("待裁决的头几条：")
		for _, sample := range pendingSamples {
			fmt.Println(sample)
		}
	}
}

// 溯源注释的两种合法写法。
//
//	// 源: packages/core/session/src/index.ts:425-760
//	// 新增: Go 侧需要的并发保护，DSH 是单进程 CLI 所以没有
//
// 路径一律相对 DSH 根，用正斜杠。行号从 1 开始，闭区间。
// 两个正则都锚在行首（`^`），等于要求 `源:` 前面那个 `//` 必须是这条注释的开头。
//
// 这一条不是洁癖。不锚行首的话，文档注释里写的**示例**会被当成真的溯源注释数进去——
// 这个 bug 在本文件自己身上就发生过：头部说明里的三行示例被数成了 3 条真注释。
// 一个会把示例当数据的检查器，报出来的数字没有意义。
var (
	reSource = regexp.MustCompile(`^//\s*源:\s*(\S+):(\d+)(?:-(\d+))?`)
	reAdded  = regexp.MustCompile(`^//\s*新增:\s*(\S.*)`)
)

// checkProvenance 走遍 Go 代码，把每一条溯源注释拿去和 DSH 源码对质。
//
// 只验「出处是否真实存在」，不验「引的那段是不是真的对应这里的代码」——后者机器做不到。
// 但光是前者就能挡住凭空编出处这一类，而那正是幻觉最常见的形态。
//
// **用 go/parser 而不是按行扫文本**，因为只有真正的注释节点才算数。
// 第一版是逐行正则，于是本工具自己的测试夹具（一段写在反引号字符串里的假 Go 源码，
// 里面故意放了一条指向不存在文件的引用）被当成了真注释报错。
// 字符串字面量里长得像注释的内容不是注释——这一点靠正则分不出来，靠解析器是白送的。
func checkProvenance(goRoot, dshRoot string) (int, []string, error) {
	lineCache := map[string]int{}
	count := 0
	var problems []string

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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			// 语法错误不静默跳过：一个解析不了的 Go 文件里的溯源注释一条都没验过，
			// 而门禁如果照样说「全部验过出处」，那句话就是假的。
			return fmt.Errorf("%s 解析失败，无法验证其中的溯源注释：%w", path, err)
		}

		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				text := comment.Text
				if reAdded.MatchString(text) {
					count++
					continue
				}
				match := reSource.FindStringSubmatch(text)
				if match == nil {
					continue
				}
				count++
				where := fmt.Sprintf("%s:%d", path, fileSet.Position(comment.Pos()).Line)

				total, ok := lineCache[match[1]]
				if !ok {
					total = countLines(filepath.Join(dshRoot, filepath.FromSlash(match[1])))
					lineCache[match[1]] = total
				}
				if total < 0 {
					problems = append(problems,
						fmt.Sprintf("%s 引的出处不存在：%s", where, match[1]))
					continue
				}
				start, _ := strconv.Atoi(match[2])
				end := start
				if match[3] != "" {
					end, _ = strconv.Atoi(match[3])
				}
				if start < 1 || end < start || end > total {
					problems = append(problems,
						fmt.Sprintf("%s 引的行范围 %d-%d 越界，%s 只有 %d 行",
							where, start, end, match[1], total))
				}
			}
		}
		return nil
	})
	return count, problems, err
}

// countLines 数一个文件有多少行。文件打不开返回 -1，让调用方把它报成「出处不存在」。
func countLines(path string) int {
	handle, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer handle.Close()

	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
	}
	if scanner.Err() != nil {
		return -1
	}
	return count
}

// collectGoSymbols 把 Go 代码里所有顶层符号收成一个集合，键的形式和裁决表的
// go_ref 一致：包名开头，形如 invariants.Registry 或 session.Store.Append。
//
// 为什么要有它：go_ref 非空只能证明「有人填了字」，证明不了「填的那个东西存在」。
// 而 PORTED 这一栏的全部意义就是「已经在 Go 里做出来了」——如果它指向一个不存在的
// 符号，这句话就是一句自述，和裁决表想挡的「悄悄漏掉」是同一类问题。
//
// 三个刻意的取舍：
//
//   - **跳过 _test.go。** 把「已移植」指到测试代码上是真会犯的错，值得被抓住。
//
//   - **不跳过 tools/。** 本来想把工具目录排除掉，但那是一条没有依据的特例——
//     真有一天某个能力落在工具里，特例会让它悄悄通过。宁可多收一些符号。
//
//   - **方法用「包.接收者类型.方法名」而不是「包.方法名」。** 不同类型上的同名方法
//     （Close、String）遍地都是，不带接收者的话它们会互相冒充。
func collectGoSymbols(goRoot string) (map[string]struct{}, error) {
	symbols := map[string]struct{}{}

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
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			// 和 checkProvenance 一样不静默跳过：一个解析不了的文件里的符号一个都没收进来，
			// 于是指向它的 PORTED 会被误报成「找不到」——那是把工具的失败算到人头上。
			return fmt.Errorf("%s 解析失败，无法收集其中的 Go 符号：%w", path, err)
		}
		packageName := parsed.Name.Name

		for _, declaration := range parsed.Decls {
			switch node := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range node.Specs {
					switch item := spec.(type) {
					case *ast.TypeSpec:
						symbols[packageName+"."+item.Name.Name] = struct{}{}
					case *ast.ValueSpec:
						for _, name := range item.Names {
							symbols[packageName+"."+name.Name] = struct{}{}
						}
					}
				}
			case *ast.FuncDecl:
				if node.Recv == nil || len(node.Recv.List) == 0 {
					symbols[packageName+"."+node.Name.Name] = struct{}{}
					continue
				}
				if receiver := receiverTypeName(node.Recv.List[0].Type); receiver != "" {
					symbols[packageName+"."+receiver+"."+node.Name.Name] = struct{}{}
				}
			}
		}
		return nil
	})
	return symbols, err
}

// receiverTypeName 从接收者的类型表达式里剥出类型名，*Registry 和 Registry 都得到 Registry。
// 值接收者和指针接收者是同一个类型上的方法，在裁决表里没有理由写成两种。
func receiverTypeName(expression ast.Expr) string {
	switch node := expression.(type) {
	case *ast.StarExpr:
		return receiverTypeName(node.X)
	case *ast.Ident:
		return node.Name
	case *ast.IndexExpr: // 泛型接收者 func (s Store[T]) ...
		return receiverTypeName(node.X)
	case *ast.IndexListExpr: // 多类型参数 func (s Store[K, V]) ...
		return receiverTypeName(node.X)
	}
	return ""
}
