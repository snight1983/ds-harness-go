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

func TestCheckRepositoryRejectsIncompleteModuleDoc(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [A](modules/a.md)\n", "")
	writeTestFile(t, filepath.Join(root, "docs", "modules", "a.md"), "# A\n")
	assertCheckError(t, root, []string{"example/a"}, "模块文档缺少定位章节")
}

func TestCheckRepositoryRejectsMissingCapabilitySection(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [A](modules/a.md)\n", "")
	body := strings.Replace(moduleSkeleton, "## 对应的 DSH 能力\n\n", "", 1)
	writeTestFile(t, filepath.Join(root, "docs", "modules", "a.md"), body)
	assertCheckError(t, root, []string{"example/a"}, "模块文档缺少对应的 DSH 能力章节")
}

// 骨架检查按 docs/modules 目录取，不按侧栏分组取：一篇没进侧栏、也没被任何包映射
// 的模块文档同样要查，否则把它从侧栏摘掉就能绕过检查。
func TestCheckRepositoryChecksModuleDocOutsideSidebar(t *testing.T) {
	root := fixture(t, "| `example/a` | [A](modules/a.md) |\n", "- [A](modules/a.md)\n", "")
	writeTestFile(t, filepath.Join(root, "docs", "modules", "b.md"), "# B\n")
	assertCheckError(t, root, []string{"example/a"}, "模块文档缺少定位章节：docs/modules/b.md")
}

const moduleSkeleton = "# A\n\n## 定位\n\n## 架构\n\n## 生命周期与并发\n\n" +
	"## 失败语义\n\n## 能力边界\n\n## 对应的 DSH 能力\n\n## 相关源码\n\n"

func fixture(t *testing.T, rows, sidebar, document string) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "README.md"), "# fixture\n")
	writeTestFile(t, filepath.Join(root, "docs", "README.md"), "# docs\n")
	writeTestFile(t, filepath.Join(root, mappingPath), "# mapping\n\n| Go 包 | 主文档 |\n|---|---|\n"+rows)
	writeTestFile(t, filepath.Join(root, sidebarPath), sidebar)
	writeTestFile(t, filepath.Join(root, "docs", "modules", "a.md"), moduleSkeleton+document)
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
