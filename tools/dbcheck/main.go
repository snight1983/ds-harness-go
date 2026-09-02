// 本文件的作用：把门禁接到命令行上——找到仓库根、跑一遍、按结果定退出码。

package main

import (
	"fmt"
	"os"

	"github.com/snight1983/ds-harness-go/tools/internal/toolpath"
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
		fmt.Fprintf(os.Stderr, "数据库门禁不通过，%d 处：\n", len(report.Findings))
		for _, finding := range report.Findings {
			fmt.Fprintln(os.Stderr, "  "+finding.String())
		}
		fmt.Fprintln(os.Stderr,
			"\n数据库那一摊整个收在 datastore 底下。业务包要的是一道业务接口，"+
				"不是一个连接池。理由见 tools/dbcheck/doc.go。")
		os.Exit(1)
	}
	fmt.Printf("数据库门禁通过：查了 %d 个 Go 文件\n", report.Files)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
