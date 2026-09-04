// 本文件的作用：门禁的判断本身——读档位表、在一棵目录树上收 import、把「低档不许
// 引高档」跑一遍。

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// modulePath 是本仓库的 module 路径，用来认出哪些 import 是仓库内部的。
const modulePath = "github.com/snight1983/ds-harness-go"

// layersFile 是档位表在仓库里的位置。
const layersFile = "docs/layers.tsv"

// tiers 是档位从低到高的顺序。低档不许 import 高档。
var tiers = []string{"contract", "runtime", "feature", "adapter", "protocol", "assembly", "cmd"}

// rankOf 把档位名换成序号；认不出的档位名返回 -1。
func rankOf(tier string) int {
	for index, name := range tiers {
		if name == tier {
			return index
		}
	}
	return -1
}

// Finding 是一处判红。
type Finding struct {
	// File 是出事的文件，仓库内相对路径、斜杠形式。
	File string
	// Line 是那条 import 所在的行；表本身的问题没有行号，记 0。
	Line int
	// Rule 是被违反的那条规则的名字。
	Rule string
	// Detail 是给人读的那一句。
	Detail string
}

func (f Finding) String() string {
	if f.Line == 0 {
		return fmt.Sprintf("%s：%s（%s）", f.File, f.Detail, f.Rule)
	}
	return fmt.Sprintf("%s:%d：%s（%s）", f.File, f.Line, f.Detail, f.Rule)
}

// Report 是跑完一整棵树的结果。
type Report struct {
	// Packages 是档位表里登记的包数。
	Packages int
	// Files 是实际读过的 .go 文件数。
	Files int
	// Findings 是所有判红。
	Findings []Finding
}

// readLayers 读档位表，返回「包路径（仓库内相对，斜杠形式）→ 档位」。
func readLayers(root string) (map[string]string, error) {
	path := filepath.Join(root, filepath.FromSlash(layersFile))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败：%w", layersFile, err)
	}
	layers := map[string]string{}
	for number, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d 该是两列，实际 %d 列", layersFile, number+1, len(fields))
		}
		pkg, tier := fields[0], fields[1]
		if rankOf(tier) < 0 {
			return nil, fmt.Errorf("%s:%d 档位 %q 不认识，可选：%s", layersFile, number+1, tier, strings.Join(tiers, " "))
		}
		if _, duplicated := layers[pkg]; duplicated {
			return nil, fmt.Errorf("%s:%d 包 %q 登记了两次", layersFile, number+1, pkg)
		}
		layers[pkg] = tier
	}
	return layers, nil
}

// checkTree 在 root 底下把那条规则跑一遍。
//
// 走的是文件系统而不是 go list：带构建标签、当前平台编译不到的文件也得算数，
// 一道分层门禁不该因为换了个 GOOS 就漏掉半棵树。测试文件一起查，理由见 doc.go。
func checkTree(root string) (Report, error) {
	layers, err := readLayers(root)
	if err != nil {
		return Report{}, err
	}
	report := Report{Packages: len(layers)}
	fileSet := token.NewFileSet()
	seen := map[string]bool{}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		pkg := filepath.ToSlash(filepath.Dir(rel))
		tier, registered := layers[pkg]
		if !registered {
			if !seen[pkg] {
				seen[pkg] = true
				report.Findings = append(report.Findings, Finding{
					File: pkg, Rule: "unregistered",
					Detail: fmt.Sprintf("有 Go 源码但没在 %s 里登记档位", layersFile),
				})
			}
			return nil
		}
		seen[pkg] = true
		report.Files++

		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("解析 %s 失败：%w", slashed, parseErr)
		}
		for _, spec := range file.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil || !strings.HasPrefix(imported, modulePath) {
				continue
			}
			target := strings.TrimPrefix(strings.TrimPrefix(imported, modulePath), "/")
			if target == "" {
				target = "."
			}
			targetTier, known := layers[target]
			if !known {
				continue
			}
			if rankOf(tier) >= rankOf(targetTier) {
				continue
			}
			report.Findings = append(report.Findings, Finding{
				File: slashed, Line: fileSet.Position(spec.Pos()).Line, Rule: "layer",
				Detail: fmt.Sprintf("%s（%s）引了 %s（%s）：低档不许引高档", pkg, tier, target, targetTier),
			})
		}
		return nil
	})
	if walkErr != nil {
		return Report{}, walkErr
	}

	// 表里有、树上没有的行也要判红，否则删掉一个包之后这张表会慢慢烂成谎话。
	var stale []string
	for pkg := range layers {
		if !seen[pkg] {
			stale = append(stale, pkg)
		}
	}
	sort.Strings(stale)
	for _, pkg := range stale {
		report.Findings = append(report.Findings, Finding{
			File: layersFile, Rule: "stale",
			Detail: fmt.Sprintf("登记了 %s，但那里没有 Go 源码", pkg),
		})
	}

	sort.Slice(report.Findings, func(left, right int) bool {
		if report.Findings[left].File != report.Findings[right].File {
			return report.Findings[left].File < report.Findings[right].File
		}
		return report.Findings[left].Line < report.Findings[right].Line
	})
	return report, nil
}

// skipDir 说的是哪些目录整个不看，判据和别的几道门禁一致。
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata"
}
