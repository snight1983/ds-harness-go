// 本文件的作用：证明 PORTED 这一栏不是自述。
//
// 「已移植」是裁决表上唯一一个宣称「东西已经做出来了」的结论。如果门禁只查 go_ref
// 非空，那么填一个不存在的符号名和真的写了那段代码，在结果上完全一样——
// 而这正是这套裁决表想挡的东西。所以这里逐条钉住：符号收得对、测试代码不算数、
// 指向空气的 PORTED 会让门禁红。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/internal/devtools/rulingtable"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s 失败：%v", path, err)
	}
}

func TestCollectGoSymbolsCoversEachKindOfTopLevelName(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "session", "store.go"), `package session

const Code = "SESSION"

var ErrMissing = 1

type Store struct{}

type Appender interface{ Append() }

func New() *Store { return nil }

func (s *Store) Append() {}

func (s Store) Len() int { return 0 }
`)

	// 测试代码里的符号不算数：把「已移植」指到测试上是真会犯的错。
	writeFile(t, filepath.Join(goRoot, "session", "store_test.go"), `package session

type OnlyInTests struct{}

func HelperInTests() {}
`)

	symbols, err := collectGoSymbols(goRoot)
	if err != nil {
		t.Fatalf("collectGoSymbols() error = %v", err)
	}

	for _, want := range []string{
		"session.Code",
		"session.ErrMissing",
		"session.Store",
		"session.Appender",
		"session.New",
		"session.Store.Append", // 指针接收者
		"session.Store.Len",    // 值接收者，剥出来的类型名该一样
	} {
		if _, ok := symbols[want]; !ok {
			t.Errorf("该收进 %q，实际没有", want)
		}
	}
	for _, unwanted := range []string{"session.OnlyInTests", "session.HelperInTests"} {
		if _, ok := symbols[unwanted]; ok {
			t.Errorf("测试代码里的 %q 不该被当成已移植的符号", unwanted)
		}
	}
}

// TestCheckRejectsPortedPointingAtNothing 是这条检查存在的核心理由。
//
// 两行裁决唯一的差别就是 go_ref 指向的符号存不存在。门禁必须只放过那一条真的。
func TestCheckRejectsPortedPointingAtNothing(t *testing.T) {
	t.Parallel()

	goRoot := t.TempDir()
	dshRoot := t.TempDir()
	writeFile(t, filepath.Join(goRoot, "session", "store.go"), "package session\n\ntype Store struct{}\n")

	exportsPath := filepath.Join(t.TempDir(), "dsh-exports.tsv")
	writeFile(t, exportsPath, strings.Join([]string{
		"package\tfile\tline\tkind\tname\tfrom",
		"harness/session\tpackages/core/session/src/index.ts\t1\tclass\tStore\tsrc/index.ts",
		"harness/session\tpackages/core/session/src/index.ts\t2\tclass\tGhost\tsrc/index.ts",
	}, "\n")+"\n")

	rulingPath := filepath.Join(t.TempDir(), "portmap.tsv")
	writeFile(t, rulingPath, strings.Join([]string{
		rulingtable.Header,
		"harness/session\tpackages/core/session/src/index.ts\t1\tclass\tStore\tsrc/index.ts\tPORTED\tsession.Store\t",
		"harness/session\tpackages/core/session/src/index.ts\t2\tclass\tGhost\tsrc/index.ts\tPORTED\tsession.Ghost\t",
	}, "\n")+"\n")

	err := runCheck(exportsPath, rulingPath, goRoot, dshRoot, false)
	if err == nil {
		t.Fatal("指向不存在符号的 PORTED 本该让门禁不通过")
	}
	if !strings.Contains(err.Error(), "共 1 项") {
		t.Errorf("只该有 Ghost 这一项不通过，实际 %q", err.Error())
	}
}
