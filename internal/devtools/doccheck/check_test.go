package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRepositoryPasses(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [A](modules/a.md)\n", "")
	result, err := checkRepository(root, []string{"example/a"})
	if err != nil {
		t.Fatalf("checkRepository() error = %v", err)
	}
	if result.Packages != 1 || result.Documents != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCheckRepositoryRejectsMissingPackageMapping(t *testing.T) {
	root := fixture(t, "", "- [A](modules/a.md)\n", "")
	assertCheckError(t, root, []string{"example/a"}, "Go 包缺少文档映射：example/a")
}

func TestCheckRepositoryRejectsDuplicatePackageMapping(t *testing.T) {
	rows := "| `example/a` | [A](modules/a.md) |\n| `example/a` | [A](modules/a.md) |\n"
	root := fixture(t, rows, "- [A](modules/a.md)\n", "")
	assertCheckError(t, root, []string{"example/a"}, "Go 包重复映射：example/a")
}

func TestCheckRepositoryRejectsStalePackageMapping(t *testing.T) {
	rows := "| `example/a` | [A](modules/a.md) |\n| `example/old` | [A](modules/a.md) |\n"
	root := fixture(t, rows, "- [A](modules/a.md)\n", "")
	assertCheckError(t, root, []string{"example/a"}, "映射包含已经不存在的 Go 包：example/old")
}

func TestCheckRepositoryRejectsMissingDocument(t *testing.T) {
	root := fixture(t, "| `example/a` | [Missing](modules/missing.md) |\n", "- [Missing](modules/missing.md)\n", "")
	assertCheckError(t, root, []string{"example/a"}, "映射的文档不存在")
}

func TestCheckRepositoryRejectsBrokenLocalLink(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [A](modules/a.md)\n", "[坏链接](missing.md)\n")
	assertCheckError(t, root, []string{"example/a"}, "本地链接不存在：missing.md")
}

func TestCheckRepositoryRejectsSidebarOmission(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [首页](README.md)\n", "")
	assertCheckError(t, root, []string{"example/a"}, "映射文档未进入侧栏：docs/modules/a.md")
}

func TestCheckRepositoryRejectsIncompleteDetailedModule(t *testing.T) {
	sidebar := "- 详细模块\n  - [A](modules/a.md)\n"
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", sidebar, "")
	assertCheckError(t, root, []string{"example/a"}, "详细模块文档缺少定位章节")
}

func fixture(t *testing.T, rows, sidebar, document string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "README.md"), "# fixture\n")
	writeTestFile(t, filepath.Join(root, "docs", "README.md"), "# docs\n")
	writeTestFile(t, filepath.Join(root, mappingPath), "# mapping\n\n| Go 包 | 主文档 |\n|---|---|\n"+rows)
	writeTestFile(t, filepath.Join(root, sidebarPath), sidebar)
	writeTestFile(t, filepath.Join(root, "docs", "modules", "a.md"), "# A\n\n"+document)
	return root
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertCheckError(t *testing.T, root string, packages []string, want string) {
	t.Helper()
	_, err := checkRepository(root, packages)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("checkRepository() error = %v, want substring %q", err, want)
	}
}
