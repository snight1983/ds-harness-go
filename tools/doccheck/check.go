package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	mappingPath = "docs/packages.md"
	sidebarPath = "docs/_sidebar.md"
)

var (
	mappingRow   = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*\\[[^]]+\\]\\(([^)]+)\\)\\s*\\|\\s*$")
	markdownLink = regexp.MustCompile(`!?\[[^]]*\]\(([^)]+)\)`)
)

type mapping struct {
	Package string
	Target  string
	Line    int
}

type report struct {
	Packages      int
	Documents     int
	MarkdownFiles int
}

func checkRepository(root string, packages []string) (report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}

	mappings, err := readMappings(filepath.Join(root, filepath.FromSlash(mappingPath)))
	if err != nil {
		return report{}, err
	}

	actual := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		actual[pkg] = struct{}{}
	}

	var problems []string
	byPackage := make(map[string][]mapping, len(mappings))
	documents := make(map[string]struct{})
	for _, item := range mappings {
		byPackage[item.Package] = append(byPackage[item.Package], item)
		doc, targetErr := resolveLocalLink(root, filepath.Join(root, filepath.FromSlash(mappingPath)), item.Target)
		if targetErr != nil {
			problems = append(problems, fmt.Sprintf("%s:%d 的文档链接无效：%v", mappingPath, item.Line, targetErr))
			continue
		}
		documents[doc] = struct{}{}
		if info, statErr := os.Stat(doc); statErr != nil || info.IsDir() {
			problems = append(problems, fmt.Sprintf("%s 映射的文档不存在：%s", item.Package, displayPath(root, doc)))
		}
	}

	for pkg, rows := range byPackage {
		if len(rows) > 1 {
			problems = append(problems, fmt.Sprintf("Go 包重复映射：%s（%d 次）", pkg, len(rows)))
		}
		if _, ok := actual[pkg]; !ok {
			problems = append(problems, fmt.Sprintf("映射包含已经不存在的 Go 包：%s", pkg))
		}
	}
	for pkg := range actual {
		if len(byPackage[pkg]) == 0 {
			problems = append(problems, fmt.Sprintf("Go 包缺少文档映射：%s", pkg))
		}
	}

	sidebarLinks, sidebarErr := localLinks(root, filepath.Join(root, filepath.FromSlash(sidebarPath)))
	if sidebarErr != nil {
		problems = append(problems, sidebarErr.Error())
	} else {
		for doc := range documents {
			if _, ok := sidebarLinks[doc]; !ok {
				problems = append(problems, fmt.Sprintf("映射文档未进入侧栏：%s", displayPath(root, doc)))
			}
		}
	}
	detailed, detailedErr := detailedModuleLinks(root)
	if detailedErr != nil {
		problems = append(problems, detailedErr.Error())
	} else {
		for _, doc := range detailed {
			problems = append(problems, checkDetailedModule(root, doc)...)
		}
	}

	markdownFiles, linkProblems, walkErr := checkMarkdownLinks(root)
	if walkErr != nil {
		problems = append(problems, walkErr.Error())
	}
	problems = append(problems, linkProblems...)

	if len(problems) > 0 {
		sort.Strings(problems)
		return report{}, fmt.Errorf("文档检查失败：\n- %s", strings.Join(problems, "\n- "))
	}

	return report{
		Packages:      len(actual),
		Documents:     len(documents),
		MarkdownFiles: markdownFiles,
	}, nil
}

func detailedModuleLinks(root string) ([]string, error) {
	path := filepath.Join(root, filepath.FromSlash(sidebarPath))
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取侧栏失败：%w", err)
	}
	defer file.Close()

	var documents []string
	inSection := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "- 详细模块" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "- ") {
			break
		}
		if !inSection {
			continue
		}
		match := markdownLink.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		target, local, parseErr := markdownTarget(match[1])
		if parseErr != nil || !local {
			continue
		}
		resolved, resolveErr := resolveLocalLink(root, path, target)
		if resolveErr == nil {
			documents = append(documents, resolved)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取侧栏失败：%w", err)
	}
	return documents, nil
}

func checkDetailedModule(root, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("读取详细模块文档失败：%s", displayPath(root, path))}
	}
	text := string(data)
	required := []struct {
		name    string
		pattern string
	}{
		{"一级标题", `(?m)^# .+$`},
		{"定位", `(?m)^## 定位$`},
		{"架构", `(?m)^## .*架构.*$`},
		{"生命周期与并发", `(?m)^## .*生命周期.*并发.*$`},
		{"失败语义", `(?m)^## 失败语义$`},
		{"能力边界", `(?m)^## 能力边界$`},
		{"相关源码", `(?m)^## 相关源码$`},
	}
	var problems []string
	for _, item := range required {
		if !regexp.MustCompile(item.pattern).MatchString(text) {
			problems = append(problems, fmt.Sprintf("详细模块文档缺少%s章节：%s", item.name, displayPath(root, path)))
		}
	}
	return problems
}

func readMappings(path string) ([]mapping, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取包文档映射失败：%w", err)
	}
	defer file.Close()

	var mappings []mapping
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		match := mappingRow.FindStringSubmatch(text)
		if match != nil {
			mappings = append(mappings, mapping{Package: match[1], Target: match[2], Line: line})
			continue
		}
		if strings.HasPrefix(text, "| `") {
			return nil, fmt.Errorf("%s:%d 不是合法的包映射行", filepath.ToSlash(path), line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取包文档映射失败：%w", err)
	}
	return mappings, nil
}

func checkMarkdownLinks(root string) (int, []string, error) {
	files := []string{filepath.Join(root, "README.md")}
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path == filepath.Join(docsRoot, "portmap") {
			// 这些是上游移植清单，链接按上游仓库解释，不属于本站本地链接。
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return 0, nil, fmt.Errorf("遍历 Markdown 失败：%w", err)
	}

	var problems []string
	for _, path := range files {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return 0, nil, fmt.Errorf("读取 %s 失败：%w", displayPath(root, path), readErr)
		}
		for _, match := range markdownLink.FindAllStringSubmatch(string(data), -1) {
			target, local, parseErr := markdownTarget(match[1])
			if parseErr != nil {
				problems = append(problems, fmt.Sprintf("%s 的链接无效：%v", displayPath(root, path), parseErr))
				continue
			}
			if !local {
				continue
			}
			resolved, resolveErr := resolveLocalLink(root, path, target)
			if resolveErr != nil {
				problems = append(problems, fmt.Sprintf("%s 的本地链接无效：%s（%v）", displayPath(root, path), target, resolveErr))
				continue
			}
			if _, statErr := os.Stat(resolved); statErr != nil {
				problems = append(problems, fmt.Sprintf("%s 的本地链接不存在：%s", displayPath(root, path), target))
			}
		}
	}
	return len(files), problems, nil
}

func localLinks(root, path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取侧栏失败：%w", err)
	}
	result := make(map[string]struct{})
	for _, match := range markdownLink.FindAllStringSubmatch(string(data), -1) {
		target, local, parseErr := markdownTarget(match[1])
		if parseErr != nil || !local {
			continue
		}
		resolved, resolveErr := resolveLocalLink(root, path, target)
		if resolveErr == nil {
			result[resolved] = struct{}{}
		}
	}
	return result, nil
}

func markdownTarget(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("目标为空")
	}
	if strings.HasPrefix(raw, "<") {
		end := strings.Index(raw, ">")
		if end < 0 {
			return "", false, fmt.Errorf("尖括号链接没有结束符")
		}
		raw = raw[1:end]
	} else {
		raw = strings.Fields(raw)[0]
	}
	if strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "//") {
		return raw, false, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if parsed.Scheme != "" {
		return raw, false, nil
	}
	target := raw
	if index := strings.IndexAny(target, "?#"); index >= 0 {
		target = target[:index]
	}
	if target == "" {
		return raw, false, nil
	}
	target, err = url.PathUnescape(target)
	if err != nil {
		return "", false, err
	}
	return target, true, nil
}

func resolveLocalLink(root, source, target string) (string, error) {
	parsedTarget, local, err := markdownTarget(target)
	if err != nil {
		return "", err
	}
	if !local {
		return "", fmt.Errorf("不是本地相对链接")
	}
	var resolved string
	if strings.HasPrefix(parsedTarget, "/") {
		resolved = filepath.Join(root, "docs", filepath.FromSlash(strings.TrimPrefix(parsedTarget, "/")))
	} else {
		resolved = filepath.Join(filepath.Dir(source), filepath.FromSlash(parsedTarget))
	}
	resolved = filepath.Clean(resolved)
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("链接越出仓库")
	}
	return resolved, nil
}

func displayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
