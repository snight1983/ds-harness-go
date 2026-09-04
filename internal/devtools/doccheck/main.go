package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type listedPackage struct {
	ImportPath string
	Directory  string
	Synopsis   string
}

func main() {
	root, err := findRepositoryRoot()
	if err != nil {
		fail(err)
	}
	packages, ignored, err := listPackages(root)
	if err != nil {
		fail(err)
	}
	result, err := checkRepository(root, packages)
	if err != nil {
		fail(err)
	}
	fmt.Printf("文档检查通过：%d 个 Go 包，%d 篇主文档，%d 个 Markdown 文件",
		result.Packages, result.Documents, result.MarkdownFiles)
	if ignored > 0 {
		fmt.Printf("；忽略 %d 个 Git 忽略目录中的临时包", ignored)
	}
	fmt.Println()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func findRepositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil && !info.IsDir() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("从当前目录向上找不到 go.mod")
		}
		directory = parent
	}
}

func listPackages(root string) ([]string, int, error) {
	// Doc 是包注释的首句。取它而不是自己去读文件，是因为 go list 已经按编译器
	// 的规则认过「哪一段是包注释」——一段隔了空行、因而其实不是包注释的注释，在
	// 这里是空的。
	command := exec.Command("go", "list", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{.Doc}}", "./...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, 0, fmt.Errorf("go list ./... 失败：%w", err)
	}

	var listed []listedPackage
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) < 2 {
			return nil, 0, fmt.Errorf("无法解析 go list 输出：%q", line)
		}
		item := listedPackage{ImportPath: fields[0], Directory: fields[1]}
		if len(fields) == 3 {
			item.Synopsis = strings.TrimSpace(fields[2])
		}
		listed = append(listed, item)
	}
	ignored, err := gitIgnoredPackageDirs(root, listed)
	if err != nil {
		return nil, 0, err
	}

	packages := make([]string, 0, len(listed)-len(ignored))
	var missing []string
	for _, item := range listed {
		rel, relErr := filepath.Rel(root, item.Directory)
		if relErr != nil {
			return nil, 0, relErr
		}
		if _, skip := ignored[filepath.ToSlash(rel)]; skip {
			continue
		}
		packages = append(packages, item.ImportPath)
		if item.Synopsis == "" {
			missing = append(missing, item.ImportPath)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, 0, fmt.Errorf("下面这些 Go 包没有包注释：\n- %s", strings.Join(missing, "\n- "))
	}
	return packages, len(ignored), nil
}

func gitIgnoredPackageDirs(root string, packages []listedPackage) (map[string]struct{}, error) {
	var input strings.Builder
	for _, item := range packages {
		rel, err := filepath.Rel(root, item.Directory)
		if err != nil {
			return nil, err
		}
		input.WriteString(filepath.ToSlash(rel))
		input.WriteByte('\n')
	}

	command := exec.Command("git", "check-ignore", "--stdin")
	command.Dir = root
	command.Stdin = strings.NewReader(input.String())
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("git check-ignore 失败：%w", err)
	}

	ignored := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if path := filepath.ToSlash(strings.TrimSpace(line)); path != "" {
			ignored[path] = struct{}{}
		}
	}
	return ignored, nil
}
