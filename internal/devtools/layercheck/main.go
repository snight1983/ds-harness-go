// 本文件的作用：把门禁接到命令行上——找到仓库根、跑一遍、按结果定退出码。

package main

import (
	"fmt"
	"os"

	"github.com/snight1983/ds-harness-go/internal/devtools/toolpath"
)

func main() {
	root, err := toolpath.RepoRoot()
	if err != nil {
		fail(err)
	}
	report, err := checkTree(root)
	if err != nil {
		fail(err)
	}
	if len(report.Findings) > 0 {
		fmt.Fprintf(os.Stderr, "分层门禁不通过，%d 处：\n", len(report.Findings))
		for _, finding := range report.Findings {
			fmt.Fprintln(os.Stderr, "  "+finding.String())
		}
		fmt.Fprintln(os.Stderr,
			"\n分层靠 import 方向表达，目录名只是给人读的索引。要么把这条依赖倒过来，"+
				"要么承认这个包的档位变了、改 docs/layers.tsv。"+
				"理由见 internal/devtools/layercheck/doc.go。")
		os.Exit(1)
	}
	fmt.Printf("分层门禁通过：%d 个包分了档，查了 %d 个 Go 文件\n", report.Packages, report.Files)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
