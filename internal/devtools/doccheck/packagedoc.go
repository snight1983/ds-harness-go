package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// boundaryHeading 是每个包注释末尾那一节的标题。写死成一个常量而不是让各包自选措辞，
// 是因为这道检查唯一能机械判定的就是「这一节在不在」——措辞一散，判据就没了。
const boundaryHeading = "# 不做什么"

// checkPackageDocs 查每个包的包注释本身，两条：
//
//  1. 首句以 `Package x` 或 `Command x` 起头，x 就是这个包真正的名字。
//     godoc 拿首句当摘要，一个写着别的包名的摘要会一路错到 pkg.go.dev 上。
//     这条不是洁癖：仓库改过一轮包名和目录，靠人读检不出来的正是这种残留。
//  2. 末尾有一节「不做什么」。判据只到「这一节在不在」为止——里面写得对不对、
//     有没有漏掉真正的禁区，机器判不了，那是评审的事。但「一个包从来没人写过
//     它的禁区」是机器判得了的，而这恰恰是最常见的那一种：不是写错了，是压根没想过。
//
// 取包注释走 go/parser 而不是自己扫注释行：包注释的界定规则（紧挨 package 子句、
// 中间不许隔空行）由编译器定义，自己实现一遍迟早会和它分叉。
func checkPackageDocs(root string, packages []listedPackage) []string {
	var problems []string
	for _, item := range packages {
		text, err := packageDoc(item.Directory)
		if err != nil {
			problems = append(problems, fmt.Sprintf("读取包注释失败：%s（%v）", displayPath(root, item.Directory), err))
			continue
		}
		if want := docPrefix(item); !strings.HasPrefix(text, want) {
			problems = append(problems, fmt.Sprintf("包注释首句应以 %q 起头：%s", want, displayPath(root, item.Directory)))
		}
		if !hasBoundarySection(text) {
			problems = append(problems, fmt.Sprintf("包注释缺少「%s」一节：%s", boundaryHeading, displayPath(root, item.Directory)))
		}
	}
	sort.Strings(problems)
	return problems
}

// docPrefix 给出这个包的包注释该有的开头。可执行文件的包名一律是 main，摘要里写
// 「Package main」等于十几个命令共用一个名字，所以按 Go 的惯例改用「Command 目录名」。
func docPrefix(item listedPackage) string {
	if item.Name == "main" {
		return "Command " + path.Base(item.ImportPath) + " "
	}
	return "Package " + item.Name + " "
}

// packageDoc 把一个包目录下所有非测试文件的包注释连起来。正常情况下只有一份，
// 但 Go 并不禁止多份，连起来判断比挑一份出来判断少一条「挑错了」的失败模式。
func packageDoc(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	fileSet := token.NewFileSet()
	var docs []string
	for _, name := range names {
		filePath := filepath.Join(directory, name)
		file, parseErr := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments|parser.SkipObjectResolution)
		if parseErr != nil {
			return "", parseErr
		}
		if file.Doc != nil {
			docs = append(docs, file.Doc.Text())
		}
	}
	return strings.Join(docs, "\n"), nil
}

// hasBoundarySection 要求标题独占一行。允许标题后面跟内容的写法会让
// 「// # 不做什么的理由见别处」这种句子也算数，那不是一节，是一句话。
func hasBoundarySection(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == boundaryHeading {
			return true
		}
	}
	return false
}
