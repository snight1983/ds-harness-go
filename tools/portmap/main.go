// 本文件的作用：把 DeepSeek Harness 的 TypeScript 源码里**所有**导出符号机器提取出来，
// 生成一份可复现的清单。这份清单是「抄全了没有」的唯一基准。
//
// 为什么需要它：这个移植工作最大的风险不是写错，是**悄悄漏掉**——我判断某个东西
// 「用不上」于是不抄，而这个判断从来没有被人看见过。所以基准不能由人来写，只能由
// 脚本从原始源码里抽。人只被允许往清单上填「Go 里的对应物」或者「不抄 + 理由」，
// 一行都不许删。
//
// 三条刻意的设计：
//
//   - **宁可多抽，不可少抽。** 不区分「包对外导出」和「包内文件之间的导出」，
//     所有 export 一律进清单。多抽只是让清单变长、逼人多做几个显式判断；
//     少抽会让某个东西永远没人问起，而这正是要防的事。
//
//   - **解析不了的行也要落进清单**，标成 UNPARSED。静默跳过等于自己给自己开后门：
//     一个没人看见的解析失败，和一次故意的省略，在结果上完全一样。
//
//   - **输出必须逐字节可复现。** 排序固定、格式固定，重跑一次得到同一个文件。
//     这样清单被人改过就是可检测的——重跑一次比一下就知道。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Export 是清单里的一行：某个文件的某一行导出了某个名字。
type Export struct {
	Package string // 包路径，形如 core/session
	File    string // 相对包目录的文件路径，形如 src/index.ts
	Line    int    // 行号，从 1 开始
	Kind    string // function / class / const / type / interface / enum / namespace / reexport / star / default / UNPARSED
	Name    string // 导出的名字。star 和 UNPARSED 时是来源模块或原始行内容
	From    string // 转发导出的来源模块，非转发时为空
}

// 各种导出写法的识别式。顺序有讲究，见 classify 里的说明。
var (
	// `export type * from '...'` 也是转发导出，只是只转发类型。漏掉 type 修饰
	// 会让 29 条真实导出全部落进 UNPARSED——这正是「宁可多抽」要防的那种漏。
	reStar      = regexp.MustCompile(`^export\s+(type\s+)?\*\s+(?:as\s+([A-Za-z_$][\w$]*)\s+)?from\s+['"]([^'"]+)['"]`)
	reBraceOpen = regexp.MustCompile(`^export\s+(?:type\s+)?\{`)
	reEnum      = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)`)
	reFunc      = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`)
	reClass     = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	reVar       = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	reType      = regexp.MustCompile(`^export\s+(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)`)
	reInterface = regexp.MustCompile(`^export\s+(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`)
	reNamespace = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:namespace|module)\s+([A-Za-z_$][\w$]*)`)
	reDefault   = regexp.MustCompile(`^export\s+default\s+(.+?)\s*;?\s*$`)
	reFromTail  = regexp.MustCompile(`from\s+['"]([^'"]+)['"]`)
)

func main() {
	root := flag.String("root", `C:\codestudy\deepseek-harness-master`, "DeepSeek Harness 源码根目录")
	out := flag.String("out", `C:\code\ds-harness-go\docs\portmap\dsh-exports.tsv`, "清单输出路径")
	flag.Parse()

	packagesDir := filepath.Join(*root, "packages")
	if _, err := os.Stat(packagesDir); err != nil {
		fmt.Fprintf(os.Stderr, "找不到 packages 目录：%v\n", err)
		os.Exit(1)
	}

	exports, files, err := scanAll(packagesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描失败：%v\n", err)
		os.Exit(1)
	}

	// 排序固定，保证重跑逐字节一致。
	sort.Slice(exports, func(i, j int) bool {
		a, b := exports[i], exports[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Name < b.Name
	})

	if err := writeTSV(*out, exports); err != nil {
		fmt.Fprintf(os.Stderr, "写清单失败：%v\n", err)
		os.Exit(1)
	}

	report(exports, files, *out)
}

// scanAll 遍历 packages 下所有非测试的 TypeScript 文件。
//
// 排除测试文件是因为它们不是被移植的对象——测试用例另有一份对照清单，
// 由单独的提取器负责，两件事不混在一起数。
func scanAll(packagesDir string) ([]Export, int, error) {
	var exports []Export
	fileCount := 0

	err := filepath.WalkDir(packagesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !isSourceFile(path) {
			return nil
		}
		pkg, rel, ok := splitPackagePath(packagesDir, path)
		if !ok {
			return nil
		}
		found, err := scanFile(path, pkg, rel)
		if err != nil {
			return err
		}
		fileCount++
		exports = append(exports, found...)
		return nil
	})
	return exports, fileCount, err
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", "lib", "dist", "tests", "test", "__tests__", "fixtures", "__fixtures__", "coverage":
		return true
	}
	return false
}

func isSourceFile(path string) bool {
	base := filepath.Base(path)
	if !strings.HasSuffix(base, ".ts") && !strings.HasSuffix(base, ".tsx") {
		return false
	}
	if strings.HasSuffix(base, ".d.ts") {
		return false
	}
	for _, marker := range []string{".spec.", ".test.", "_test.", "-test."} {
		if strings.Contains(base, marker) {
			return false
		}
	}
	// 形如 token_tests.rs 的命名在 TS 侧对应 xxx_tests.ts，一并排除。
	if strings.HasSuffix(base, "_tests.ts") || strings.HasSuffix(base, "-tests.ts") {
		return false
	}
	return true
}

// splitPackagePath 把绝对路径拆成「包路径」和「包内相对路径」。
//
// DSH 的目录形状是 packages/<组>/<包>/...，所以包路径取前两段。
func splitPackagePath(packagesDir, path string) (pkg, rel string, ok bool) {
	relative, err := filepath.Rel(packagesDir, path)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[0] + "/" + parts[1], strings.Join(parts[2:], "/"), true
}

// scanFile 逐行找出一个文件里的全部导出。
//
// 花括号块（export { A, B } from '...'）可能跨行，所以这里带一个极小的状态：
// 遇到未闭合的 `export {` 就把后面的行接上，直到看见 `}`。
func scanFile(path, pkg, rel string) ([]Export, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	var (
		found       []Export
		buffer      strings.Builder
		bufferStart int
		inBrace     bool
	)

	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())

		if inBrace {
			buffer.WriteString(" ")
			buffer.WriteString(line)
			if strings.Contains(line, "}") {
				inBrace = false
				found = append(found, parseBrace(buffer.String(), pkg, rel, bufferStart)...)
				buffer.Reset()
			}
			continue
		}

		if !strings.HasPrefix(line, "export") {
			continue
		}
		// `exports` `exported` 这类以 export 开头的标识符不算导出语句。
		if len(line) > 6 && !isBoundary(line[6]) {
			continue
		}
		if reBraceOpen.MatchString(line) {
			if strings.Contains(line, "}") {
				found = append(found, parseBrace(line, pkg, rel, lineNumber)...)
			} else {
				inBrace = true
				bufferStart = lineNumber
				buffer.Reset()
				buffer.WriteString(line)
			}
			continue
		}
		found = append(found, classify(line, pkg, rel, lineNumber))
	}
	if inBrace {
		// 文件读完了花括号还没闭合：这是解析失败，必须留痕而不是丢掉。
		found = append(found, Export{
			Package: pkg, File: rel, Line: bufferStart,
			Kind: "UNPARSED", Name: compact(buffer.String()),
		})
	}
	return found, scanner.Err()
}

func isBoundary(char byte) bool {
	return char == ' ' || char == '\t' || char == '{' || char == '*'
}

// classify 判定单行导出属于哪一种。
//
// 顺序不能随便调：`export const enum X` 既能被 reVar 又能被 reEnum 匹配，
// 枚举必须先判；`export default function f()` 同理要在 reDefault 之前判。
func classify(line, pkg, rel string, lineNumber int) Export {
	base := Export{Package: pkg, File: rel, Line: lineNumber}

	if match := reStar.FindStringSubmatch(line); match != nil {
		base.Kind, base.From = "star", match[3]
		if match[1] != "" {
			base.Kind = "star-type"
		}
		base.Name = match[2]
		if base.Name == "" {
			base.Name = "*"
		}
		return base
	}
	for _, rule := range []struct {
		kind string
		re   *regexp.Regexp
	}{
		{"enum", reEnum},
		{"function", reFunc},
		{"class", reClass},
		{"interface", reInterface},
		{"type", reType},
		{"namespace", reNamespace},
		{"const", reVar},
	} {
		if match := rule.re.FindStringSubmatch(line); match != nil {
			base.Kind, base.Name = rule.kind, match[1]
			if strings.Contains(line, "export default") {
				base.Kind = rule.kind + "+default"
			}
			return base
		}
	}
	if match := reDefault.FindStringSubmatch(line); match != nil {
		base.Kind, base.Name = "default", compact(match[1])
		return base
	}

	base.Kind, base.Name = "UNPARSED", compact(line)
	return base
}

// parseBrace 拆开 `export { A, B as C } from '...'` 这种块。
//
// 取的是**对外可见的那个名字**：`A as B` 对外是 B，`default as X` 对外是 X。
func parseBrace(text, pkg, rel string, lineNumber int) []Export {
	from := ""
	if match := reFromTail.FindStringSubmatch(text); match != nil {
		from = match[1]
	}
	open := strings.Index(text, "{")
	closing := strings.LastIndex(text, "}")
	if open < 0 || closing < 0 || closing < open {
		return []Export{{
			Package: pkg, File: rel, Line: lineNumber,
			Kind: "UNPARSED", Name: compact(text),
		}}
	}

	kind := "reexport"
	if from == "" {
		kind = "reexport-local"
	}
	if strings.HasPrefix(text, "export type") {
		kind += "-type"
	}

	var found []Export
	for _, piece := range strings.Split(text[open+1:closing], ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		name := piece
		if index := strings.LastIndex(piece, " as "); index >= 0 {
			name = strings.TrimSpace(piece[index+4:])
		}
		name = strings.TrimPrefix(strings.TrimSpace(name), "type ")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found = append(found, Export{
			Package: pkg, File: rel, Line: lineNumber,
			Kind: kind, Name: name, From: from,
		})
	}
	if len(found) == 0 {
		// 花括号是空的。两种写法都合法，都不导出具名符号：
		//   `export {}`                       —— 只为把文件标成 ES module；
		//   `export type {} from '<模块>'`     —— 只为把该模块的类型增强拉进来。
		//
		// 这两种我是看懂了的，所以不能标 UNPARSED。UNPARSED 必须只留给
		// 「我没看懂」，否则那个数字就不再是解析器可信度的指标了。
		// 但也不能直接扔掉：第二种代表一条对别的模块的类型依赖，
		// 移植时需要有人显式判断它要不要跟着走。
		found = append(found, Export{
			Package: pkg, File: rel, Line: lineNumber,
			Kind: kind + "-empty", Name: "{}", From: from,
		})
	}
	return found
}

// compact 把一行压成单行且不含制表符，免得破坏 TSV 的列。
func compact(text string) string {
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

func writeTSV(path string, exports []Export) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var builder strings.Builder
	builder.WriteString("package\tfile\tline\tkind\tname\tfrom\n")
	for _, item := range exports {
		fmt.Fprintf(&builder, "%s\t%s\t%d\t%s\t%s\t%s\n",
			item.Package, item.File, item.Line, item.Kind, item.Name, item.From)
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

// report 打印一份摘要。UNPARSED 的条数是这份清单可信度的直接指标：
// 它越多，说明解析器漏看的写法越多，清单就越不能当基准用。
func report(exports []Export, files int, out string) {
	byKind := map[string]int{}
	byPackage := map[string]int{}
	unparsed := 0
	for _, item := range exports {
		byKind[item.Kind]++
		byPackage[item.Package]++
		if item.Kind == "UNPARSED" {
			unparsed++
		}
	}

	sum := sha256.Sum256(mustRead(out))
	fmt.Printf("清单已写入：%s\n", out)
	fmt.Printf("sha256：%s\n", hex.EncodeToString(sum[:]))
	fmt.Printf("扫过文件：%d 个，包：%d 个，导出符号：%d 条\n", files, len(byPackage), len(exports))

	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return byKind[kinds[i]] > byKind[kinds[j]] })
	fmt.Println("按种类：")
	for _, kind := range kinds {
		fmt.Printf("  %-22s %d\n", kind, byKind[kind])
	}
	if unparsed > 0 {
		fmt.Printf("\n注意：有 %d 条没解析出来，已标成 UNPARSED 留在清单里。\n", unparsed)
	}
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
