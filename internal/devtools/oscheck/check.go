// 本文件的作用：门禁的判断本身——分区、两条规则，以及在一棵目录树上把它们跑一遍。

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// zone 是一个文件所在的分区。规则按分区放行，不按包名。
type zone int

const (
	// zoneBusiness 是业务包：两条规则全都管得到。
	zoneBusiness zone = iota
	// zoneLocal 是 internal/devtools/ 和 cmd/：它们跑在本机上，两条全放。
	zoneLocal
	// zoneExempt 是整个跳过的那几处，名单见 doc.go。
	zoneExempt
)

// exemptPrefixes 是豁免名单。只有两项，不许再加，理由见 doc.go。
//
// internal/devtools/ 和 cmd/ 不在这里——它们由 [zoneLocal] 整个放行，那是分区不是豁免。
var exemptPrefixes = []string{
	// 数据库测试夹具：起一个临时 SQLite 文件再删掉。它必须供别的包的测试
	// import，所以不能写成 _test.go，于是躲不进「测试不查」那一条。
	"adapter/datastore/dbtest/",
	// 快照测试用的假模型：从磁盘读回放脚本。那些脚本是仓库里的测试数据，
	// 不是这个服务的运行时状态，自陈见 feature/replay/doc.go。
	"feature/replay/",
}

// zoneOf 按仓库内的相对路径判断分区。path 是斜杠形式。
func zoneOf(path string) zone {
	for _, prefix := range exemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			return zoneExempt
		}
	}
	switch {
	case strings.HasPrefix(path, "internal/devtools/"), strings.HasPrefix(path, "cmd/"):
		return zoneLocal
	default:
		return zoneBusiness
	}
}

// bannedOSFuncs 是 os 包里那些碰文件系统的函数。
//
// 名单是「点名」而不是「整个 os 包禁掉」：os.Getenv、os.Exit、os.Stdout、
// os.Signal 这些和磁盘没有关系，禁了它们只会逼人写更绕的代码。
var bannedOSFuncs = map[string]bool{
	// 打开与创建
	"Open":       true,
	"OpenFile":   true,
	"Create":     true,
	"CreateTemp": true,
	"NewFile":    true,
	// 整份读写
	"ReadFile":  true,
	"WriteFile": true,
	// 元数据
	"Stat":     true,
	"Lstat":    true,
	"Readlink": true,
	"Chmod":    true,
	"Chown":    true,
	"Chtimes":  true,
	"Truncate": true,
	// 命名空间
	"Mkdir":     true,
	"MkdirAll":  true,
	"MkdirTemp": true,
	"ReadDir":   true,
	"Remove":    true,
	"RemoveAll": true,
	"Rename":    true,
	"Link":      true,
	"Symlink":   true,
	// 工作目录与临时目录：两者都是「有那么一台机器」这个假设本身
	"Getwd":   true,
	"Chdir":   true,
	"TempDir": true,
	// 目录树
	"DirFS": true,
}

// bannedImports 是那两个整个不许引的包。
//
// path/filepath 是宿主机路径的语法（盘符、反斜杠、符号链接），本仓库的路径
// 一律斜杠分隔、由后端解释，要拼路径用 path；io/ioutil 整个是文件 I/O。
var bannedImports = map[string]string{
	"path/filepath": "它认的是宿主机路径的语法（盘符、反斜杠）。本仓库的路径一律斜杠分隔、" +
		"由 fs.FileSystem 的后端解释，拼路径用 path。",
	"io/ioutil": "它整个是文件 I/O，而且早已废弃。内容读写走 fs.FileSystem。",
}

// Finding 是一条被判红的事实。
type Finding struct {
	// File 是仓库内的相对路径，斜杠形式。
	File string
	// Line 是行号。
	Line int
	// Rule 是被违反的那条规则的名字。
	Rule string
	// Detail 说的是「违反在哪儿」，不是「该怎么改」。
	Detail string
}

// String 把一条判红排成一行，格式和编译器的错误行一致，好让编辑器点得进去。
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d：[%s] %s", f.File, f.Line, f.Rule, f.Detail)
}

// Report 是跑完一整棵树的结果。
type Report struct {
	// Files 是实际读过的 .go 文件数。
	Files int
	// Findings 是所有判红，按文件、行号排好序。
	Findings []Finding
}

// checkTree 在 root 底下把两条规则跑一遍。
//
// 走的是文件系统而不是 go list：带构建标签、当前平台编译不到的文件也得算数，
// 一道「绝对禁止」的门禁不该因为换了个 GOOS 就漏掉半棵树。
func checkTree(root string) (Report, error) {
	var report Report
	fileSet := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slashed := filepath.ToSlash(rel)

		if entry.IsDir() {
			if slashed != "." && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		// 测试要造夹具、要临时目录，那是测试进程自己的事，和这个服务部署成
		// 什么样无关。
		if strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		if zoneOf(slashed) != zoneBusiness {
			return nil
		}
		report.Files++

		file, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("解析 %s 失败：%w", slashed, parseErr)
		}
		report.Findings = append(report.Findings, checkFile(fileSet, file, slashed)...)
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

// skipDir 说的是哪些目录整个不看。
//
// 点开头的目录（.git、.github、.claude）和 node_modules 里没有本仓库的 Go 源码；
// testdata 里的 .go 文件按约定不参与构建，它们常常是故意写坏的。
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata"
}

// checkFile 在一个已经解析好的业务文件上跑那两条规则。
func checkFile(fileSet *token.FileSet, file *ast.File, path string) []Finding {
	var findings []Finding
	line := func(pos token.Pos) int { return fileSet.Position(pos).Line }

	// osNames 是本文件里 os 包被叫成了什么。默认是 "os"，但一条重命名的 import
	// 能让 os.ReadFile 写成别的样子，所以按文件收一遍别名。
	osNames := map[string]bool{}
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if reason, banned := bannedImports[imported]; banned {
			findings = append(findings, Finding{
				File: path, Line: line(spec.Pos()), Rule: "import-filesystem",
				Detail: fmt.Sprintf("引了 %q。%s", imported, reason),
			})
			continue
		}
		if imported != "os" {
			continue
		}
		switch {
		case spec.Name == nil:
			osNames["os"] = true
		case spec.Name.Name == "." || spec.Name.Name == "_":
			// 点导入把 os 的全部名字摊进本文件，那时 ReadFile 前面没有限定符，
			// 下面那趟按 SelectorExpr 的检查就整个失效了。直接判红。
			if spec.Name.Name == "." {
				findings = append(findings, Finding{
					File: path, Line: line(spec.Pos()), Rule: "import-os-dot",
					Detail: `以点导入引了 "os"。那样 os.ReadFile 会写成一个没有限定符的 ` +
						`ReadFile，这道门禁就看不见了。`,
				})
			}
		default:
			osNames[spec.Name.Name] = true
		}
	}
	if len(osNames) == 0 {
		return findings
	}

	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		// Obj 非空说明这个名字在本文件里被局部声明了（一个叫 os 的变量），
		// 那它就不是包限定符。
		if !ok || ident.Obj != nil || !osNames[ident.Name] {
			return true
		}
		if !bannedOSFuncs[selector.Sel.Name] {
			return true
		}
		findings = append(findings, Finding{
			File: path, Line: line(selector.Pos()), Rule: "os-filesystem",
			Detail: fmt.Sprintf("调了 %s.%s。这个服务跑的地方没有可用硬盘，"+
				"内容读写整个收在 fs.FileSystem 一条缝上。",
				ident.Name, selector.Sel.Name),
		})
		return true
	})
	return findings
}
