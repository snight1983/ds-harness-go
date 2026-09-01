// Command capmap 从 DSH 的 227 个包里机器抽出一份「能力清单」。
//
// 为什么不是导出符号清单：符号清单管的是「这个函数抄了没有」，那是字符级搬运。
// 我们要抄的是**能力**——「进程崩在工具调用中间，重开后能说出结果未知」这种事。
// 用什么函数、几个函数、叫什么名字，Go 里自有 Go 的写法。
//
// 为什么能力清单也必须机器抽：如果由我来写「DSH 有哪些能力」，那等于我自己
// 定义了要抄什么——我判断某块「用不上」于是不写进清单，这个判断就永远没人看见。
// 所以清单的每一行都必须能指回原始文件的某一行。
//
// 抽三样东西，都是包作者自己写下的、不经我转述的：
//
//   - package.json 的 name / description —— 包作者对这个包干什么的一句话定义；
//   - 该包导出的顶层 class 和 interface —— 能力的边界长什么样；
//   - README 的第一段 —— 有则抽，没有就空着，不替它编。
//
// 还抽第四样：package.json 里指向别的 DSH 包的依赖，据此算出拓扑分层。
//
// 为什么需要这个：core/agent 的 Agent 接口引用了 Session、Inbox、LlmCallConfig 等
// 六个别处定义的类型。从它开工，就得先给这六个东西编存根——那是在没读过原文的
// 情况下替它们定形状。分层告诉我哪些包**不依赖任何 DSH 包**，那才是能在不猜的
// 前提下动笔的地方。
//
// 输出同样逐字节可复现，理由和导出清单一样：清单被人改过就是可检测的。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Capability 是能力清单的一行：一个包，连同它自己声明的身份和边界。
type Capability struct {
	Group       string   // packages 下的第一段，形如 core
	Package     string   // 第二段，形如 session
	NPMName     string   // package.json 里的 name
	Description string   // package.json 里的 description
	Lines       int      // 该包非测试 TS 源码的总行数，用来看分量
	Files       int      // 源文件数
	Classes     []string // 导出的顶层 class
	Interfaces  []string // 导出的顶层 interface
	Readme      string   // README 第一段正文，没有就是空
	DependsOn   []string // package.json 里指向别的 DSH 包的依赖，存 npm 名
	Layer       int      // 拓扑层号，0 表示不依赖任何 DSH 包
}

// slug 是这个包在清单里的短名，形如 core/agent。
func (c Capability) slug() string { return c.Group + "/" + c.Package }

var (
	reClass     = regexp.MustCompile(`^export\s+(?:declare\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	reInterface = regexp.MustCompile(`^export\s+(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`)
)

func main() {
	root := flag.String("root", `C:\codestudy\deepseek-harness-dsh-v0.1.2-alpha.3`, "DSH 源码根目录")
	out := flag.String("out", `C:\code\ds-harness-go\docs\portmap\dsh-capabilities.md`, "能力清单输出路径")
	flag.Parse()

	packagesDir := filepath.Join(*root, "packages")
	capabilities, err := scanPackages(packagesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "扫描失败：%v\n", err)
		os.Exit(1)
	}

	sort.Slice(capabilities, func(i, j int) bool {
		a, b := capabilities[i], capabilities[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Package < b.Package
	})

	cycles := assignLayers(capabilities)

	if err := writeMarkdown(*out, capabilities, cycles); err != nil {
		fmt.Fprintf(os.Stderr, "写清单失败：%v\n", err)
		os.Exit(1)
	}

	missing := 0
	total := 0
	for _, capability := range capabilities {
		total += capability.Lines
		if strings.TrimSpace(capability.Description) == "" {
			missing++
		}
	}
	fmt.Printf("能力清单已写入：%s\n", *out)
	fmt.Printf("包：%d 个，源码合计 %d 行\n", len(capabilities), total)

	// 第 0 层是唯一能在不给任何东西编存根的前提下动笔的地方，所以直接打出来。
	fmt.Println("\n第 0 层（不依赖任何 DSH 包，可以直接开写）：")
	for _, capability := range capabilities {
		if capability.Layer == 0 {
			fmt.Printf("  %-40s %6d 行  %s\n", capability.slug(), capability.Lines, capability.NPMName)
		}
	}
	byLayer := map[int]int{}
	for _, capability := range capabilities {
		byLayer[capability.Layer]++
	}
	depth := 0
	for layer := range byLayer {
		if layer > depth {
			depth = layer
		}
	}
	fmt.Printf("\n共 %d 层：", depth+1)
	for layer := 0; layer <= depth; layer++ {
		fmt.Printf(" 第%d层 %d 个", layer, byLayer[layer])
	}
	fmt.Println()
	if len(cycles) > 0 {
		fmt.Printf("注意：有 %d 个包卷在依赖环里，定不了层：%s\n", len(cycles), strings.Join(cycles, ", "))
	}
	if missing > 0 {
		// 没有 description 的包，我无法在不猜测的前提下说出它干什么，必须单独标出来。
		fmt.Printf("注意：有 %d 个包没写 description，需要靠读代码判断，已在清单里标成【无描述】。\n", missing)
	}
}

// scanPackages 走遍 packages/<组>/<包>，每个包抽一条。
func scanPackages(packagesDir string) ([]Capability, error) {
	entries, err := os.ReadDir(packagesDir)
	if err != nil {
		return nil, err
	}

	var capabilities []Capability
	for _, group := range entries {
		if !group.IsDir() {
			continue
		}
		groupDir := filepath.Join(packagesDir, group.Name())
		packages, err := os.ReadDir(groupDir)
		if err != nil {
			return nil, err
		}
		for _, pkg := range packages {
			if !pkg.IsDir() {
				continue
			}
			packageDir := filepath.Join(groupDir, pkg.Name())
			if _, err := os.Stat(filepath.Join(packageDir, "package.json")); err != nil {
				continue // 不是一个包，跳过
			}
			capability, err := scanPackage(packageDir, group.Name(), pkg.Name())
			if err != nil {
				return nil, err
			}
			capabilities = append(capabilities, capability)
		}
	}
	return capabilities, nil
}

func scanPackage(packageDir, group, name string) (Capability, error) {
	capability := Capability{Group: group, Package: name}

	manifest, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
	if err != nil {
		return capability, err
	}
	var parsed struct {
		Name             string            `json:"name"`
		Description      string            `json:"description"`
		Dependencies     map[string]string `json:"dependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	// 解析失败不静默吞掉：一个读不出来的 package.json 是需要人看的事。
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return capability, fmt.Errorf("%s 的 package.json 解析失败：%w", packageDir, err)
	}
	capability.NPMName = parsed.Name
	capability.Description = parsed.Description

	// 只取 dependencies 和 peerDependencies，**不取 devDependencies**。
	// 这个仓库里 devDependencies 是 peerDependencies 的镜像再加上构建期工具
	// （典型的是 typert-registry，一个代码生成器）。把构建工具算成能力依赖，
	// 会让本来在第 0 层的包被推到后面去，分层就失去了指导意义。
	seen := map[string]bool{}
	for _, group := range []map[string]string{parsed.Dependencies, parsed.PeerDependencies} {
		for name := range group {
			if strings.HasPrefix(name, "@deepseek-ai/") && !seen[name] {
				seen[name] = true
				capability.DependsOn = append(capability.DependsOn, name)
			}
		}
	}
	sort.Strings(capability.DependsOn)
	capability.Readme = readFirstParagraph(filepath.Join(packageDir, "README.md"))

	err = filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, err error) error {
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
		lines, classes, interfaces, err := scanSource(path)
		if err != nil {
			return err
		}
		capability.Files++
		capability.Lines += lines
		capability.Classes = append(capability.Classes, classes...)
		capability.Interfaces = append(capability.Interfaces, interfaces...)
		return nil
	})
	sort.Strings(capability.Classes)
	sort.Strings(capability.Interfaces)
	return capability, err
}

// assignLayers 用 Kahn 算法给每个包定层：第 0 层是不依赖任何 DSH 包的，
// 第 N 层的每个包至少有一个依赖落在第 N-1 层。
//
// 只认**在本次扫描里出现过**的包名。`@deepseek-ai/cordis` 这类不在 packages/
// 下的依赖不构成边——它们是外部依赖，由 Go 侧另行决定用什么代替。
//
// 走不动之后还剩下的包，说明它们卷在依赖环里。这种情况**不静默处理**：
// 层号记 -1 并由调用方报出来。TypeScript 允许类型层面的循环引用，Go 不允许，
// 所以环是一个必须由人看见并拆开的事实，不是一个可以糊过去的细节。
func assignLayers(capabilities []Capability) (cycles []string) {
	index := make(map[string]int, len(capabilities)) // npm 名 -> 下标
	for i, capability := range capabilities {
		index[capability.NPMName] = i
	}

	settled := make([]bool, len(capabilities))
	for i := range capabilities {
		capabilities[i].Layer = -1
	}

	for {
		progressed := false
		for i, capability := range capabilities {
			if settled[i] {
				continue
			}
			layer, ready := 0, true
			for _, dependency := range capability.DependsOn {
				j, inTree := index[dependency]
				if !inTree {
					continue // 外部依赖，不构成边
				}
				if !settled[j] {
					ready = false
					break
				}
				if capabilities[j].Layer+1 > layer {
					layer = capabilities[j].Layer + 1
				}
			}
			if ready {
				capabilities[i].Layer = layer
				settled[i] = true
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	for i, capability := range capabilities {
		if !settled[i] {
			cycles = append(cycles, capability.slug())
		}
	}
	sort.Strings(cycles)
	return cycles
}

func scanSource(path string) (lines int, classes, interfaces []string, err error) {
	handle, err := os.Open(path)
	if err != nil {
		return 0, nil, nil, err
	}
	defer handle.Close()

	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		lines++
		text := strings.TrimSpace(scanner.Text())
		if match := reClass.FindStringSubmatch(text); match != nil {
			classes = append(classes, match[1])
			continue
		}
		if match := reInterface.FindStringSubmatch(text); match != nil {
			interfaces = append(interfaces, match[1])
		}
	}
	return lines, classes, interfaces, scanner.Err()
}

// readFirstParagraph 取 README 的第一段正文。
//
// 跳过标题、徽章、空行；遇到第一段连续的正文就返回。读不到就返回空字符串——
// 没有 README 是一个事实，不是一个需要被填补的空缺。
func readFirstParagraph(path string) string {
	handle, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer handle.Close()

	var paragraph []string
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[!") || strings.HasPrefix(line, "![") {
			continue
		}
		paragraph = append(paragraph, line)
		if len(paragraph) >= 4 {
			break
		}
	}
	return compact(strings.Join(paragraph, " "))
}

// writeLayers 把拓扑分层放在清单最前面，因为它回答的是「先写哪个」这个问题，
// 而那是打开这份文件时的第一个问题。
func writeLayers(builder *strings.Builder, capabilities []Capability, cycles []string) {
	depth := 0
	for _, capability := range capabilities {
		if capability.Layer > depth {
			depth = capability.Layer
		}
	}

	builder.WriteString("## 移植顺序（依赖拓扑分层）\n\n")
	builder.WriteString("第 0 层不依赖任何 DSH 包，是唯一能在不给别人的类型编存根的前提下动笔的地方。\n")
	builder.WriteString("边只取 `dependencies` 与 `peerDependencies`，不取 `devDependencies`（那里混着构建期工具）。\n\n")

	for layer := 0; layer <= depth; layer++ {
		var members []string
		lines := 0
		for _, capability := range capabilities {
			if capability.Layer == layer {
				members = append(members, capability.slug())
				lines += capability.Lines
			}
		}
		if len(members) == 0 {
			continue
		}
		fmt.Fprintf(builder, "**第 %d 层** — %d 个包 / %d 行：%s\n\n",
			layer, len(members), lines, strings.Join(members, "、"))
	}

	if len(cycles) > 0 {
		fmt.Fprintf(builder, "**定不了层（卷在依赖环里，需要人拆）** — %d 个：%s\n\n",
			len(cycles), strings.Join(cycles, "、"))
	}
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
	return !strings.HasSuffix(base, "_tests.ts") && !strings.HasSuffix(base, "-tests.ts")
}

func compact(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|") // 别把 markdown 表格的列冲掉
	return strings.Join(strings.Fields(text), " ")
}

// writeMarkdown 输出成 markdown 而不是 TSV，因为这份东西是给人读着做判断的，
// 不是拿去和别的表对键的。对键那件事由 dsh-exports.tsv 负责。
func writeMarkdown(path string, capabilities []Capability, cycles []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var builder strings.Builder
	builder.WriteString("# DSH 能力清单（机器抽取，勿手改）\n\n")
	builder.WriteString("每一行都来自包作者自己写下的内容，不经转述：\n")
	builder.WriteString("`description` 取自各包 `package.json`，`类/接口` 取自源码里的 `export class` / `export interface`。\n")
	builder.WriteString("由 `tools/capmap` 生成，重跑覆盖。\n\n")

	writeLayers(&builder, capabilities, cycles)

	currentGroup := ""
	for _, capability := range capabilities {
		if capability.Group != currentGroup {
			currentGroup = capability.Group
			fmt.Fprintf(&builder, "\n## %s\n\n", currentGroup)
		}
		description := capability.Description
		if strings.TrimSpace(description) == "" {
			description = "**【无描述】**"
		}
		fmt.Fprintf(&builder, "### %s/%s — %d 行 / %d 文件\n\n",
			capability.Group, capability.Package, capability.Lines, capability.Files)
		fmt.Fprintf(&builder, "- npm: `%s`\n", capability.NPMName)
		fmt.Fprintf(&builder, "- 层: %d\n", capability.Layer)
		fmt.Fprintf(&builder, "- 自述: %s\n", compact(description))
		if len(capability.DependsOn) > 0 {
			fmt.Fprintf(&builder, "- 依赖: %s\n", strings.Join(capability.DependsOn, ", "))
		}
		if capability.Readme != "" {
			fmt.Fprintf(&builder, "- README: %s\n", capability.Readme)
		}
		if len(capability.Classes) > 0 {
			fmt.Fprintf(&builder, "- 类: %s\n", strings.Join(capability.Classes, ", "))
		}
		if len(capability.Interfaces) > 0 {
			fmt.Fprintf(&builder, "- 接口: %s\n", strings.Join(capability.Interfaces, ", "))
		}
		builder.WriteString("\n")
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}
