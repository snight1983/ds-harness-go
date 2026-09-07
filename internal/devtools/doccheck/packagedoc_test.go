package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPackageDocsAcceptsCompletePackage(t *testing.T) {
	root := t.TempDir()
	item := writeGoPackage(t, root, "example", "example", boundaryDoc)
	if problems := checkPackageDocs(root, []listedPackage{item}); len(problems) > 0 {
		t.Fatalf("checkPackageDocs() = %v, want none", problems)
	}
}

func TestCheckPackageDocsRejectsMissingBoundarySection(t *testing.T) {
	root := t.TempDir()
	item := writeGoPackage(t, root, "example", "example", "// Package example 做一件事。\npackage example\n")
	assertPackageDocProblem(t, root, item, "包注释缺少「# 不做什么」一节：example")
}

// 标题必须独占一行：把它写成句子的一部分不算一节，否则一句「本节说明不做什么」
// 就能把检查糊弄过去。
func TestCheckPackageDocsRejectsHeadingInsideSentence(t *testing.T) {
	root := t.TempDir()
	body := "// Package example 做一件事。\n//\n// # 不做什么由别处说明\npackage example\n"
	item := writeGoPackage(t, root, "example", "example", body)
	assertPackageDocProblem(t, root, item, "包注释缺少「# 不做什么」一节：example")
}

// 隔了空行的注释不是包注释，编译器不认，这里也不该认。
func TestCheckPackageDocsRejectsDetachedComment(t *testing.T) {
	root := t.TempDir()
	body := "// Package example 做一件事。\n//\n// # 不做什么\n//\n//   - **不做那件事。**理由。\n\npackage example\n"
	item := writeGoPackage(t, root, "example", "example", body)
	assertPackageDocProblem(t, root, item, "包注释缺少「# 不做什么」一节：example")
}

// 测试文件里的包注释不算数：它随测试一起被排除在发布出去的文档之外。
func TestCheckPackageDocsIgnoresTestFiles(t *testing.T) {
	root := t.TempDir()
	item := writeGoPackage(t, root, "example", "example", "// Package example 做一件事。\npackage example\n")
	writeTestFile(t, filepath.Join(item.Directory, "helper_test.go"), boundaryDoc)
	assertPackageDocProblem(t, root, item, "包注释缺少「# 不做什么」一节：example")
}

// 改过一轮包名之后最容易留下的残留：注释里还写着旧名字。
func TestCheckPackageDocsRejectsStalePackageName(t *testing.T) {
	root := t.TempDir()
	body := strings.Replace(boundaryDoc, "// Package example ", "// Package old ", 1)
	item := writeGoPackage(t, root, "example", "example", body)
	assertPackageDocProblem(t, root, item, `包注释首句应以 "Package example " 起头：example`)
}

// 可执行文件的包名一律是 main，摘要要按目录名写成 Command。
func TestCheckPackageDocsRequiresCommandPrefixForMain(t *testing.T) {
	root := t.TempDir()
	body := strings.Replace(boundaryDoc, "// Package example 做一件事。\npackage example\n",
		"// Package main 做一件事。\npackage main\n", 1)
	body = strings.Replace(body, "package example", "package main", 1)
	item := writeGoPackage(t, root, "example", "main", body)
	assertPackageDocProblem(t, root, item, `包注释首句应以 "Command example " 起头：example`)
}

func TestCheckPackageDocsAcceptsCommandPrefix(t *testing.T) {
	root := t.TempDir()
	body := strings.Replace(boundaryDoc, "// Package example ", "// Command example ", 1)
	body = strings.Replace(body, "package example", "package main", 1)
	item := writeGoPackage(t, root, "example", "main", body)
	if problems := checkPackageDocs(root, []listedPackage{item}); len(problems) > 0 {
		t.Fatalf("checkPackageDocs() = %v, want none", problems)
	}
}

const boundaryDoc = "// Package example 做一件事。\n//\n// # 不做什么\n//\n" +
	"//   - **不做那件事。**归 other 管。\n//   - **也不做另一件。**理由。\npackage example\n"

func writeGoPackage(t *testing.T, root, directory, name, body string) listedPackage {
	t.Helper()
	full := filepath.Join(root, directory)
	writeTestFile(t, filepath.Join(full, "doc.go"), body)
	return listedPackage{ImportPath: "example.test/" + directory, Directory: full, Name: name}
}

func assertPackageDocProblem(t *testing.T, root string, item listedPackage, want string) {
	t.Helper()
	problems := checkPackageDocs(root, []listedPackage{item})
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return
		}
	}
	t.Fatalf("checkPackageDocs() = %v, want substring %q", problems, want)
}
