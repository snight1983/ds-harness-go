// Command rule 往裁决表上填一条结论，且只填一条。
//
// 为什么要有它：裁决表是九列 TSV，制表符和空列在编辑器里看不见。手工改一行，
// 改错列、吃掉一个制表符、把 CRLF 带进来——这些都不会当场报错，只会在门禁那边
// 变成一条莫名其妙的失败，或者更糟：变成一条看起来填好了、其实填到隔壁列的裁决。
// 要填的行有七千多，靠手稳是不行的。
//
// 但这个工具**刻意做得难用**：
//
//   - 一次只填一行，靠「包 + 符号名」定位，匹配不到或匹配到多行都报错退出。
//     没有批量模式、没有通配符——因为「一次把一整个包标成 SKIP」正是这张表要防的事。
//
//   - 已经裁决过的行默认拒绝覆盖。改主意是允许的，但必须显式加 -force，
//     这样它会出现在 git diff 里，也出现在命令历史里。
//
//   - GO_NATIVE / SKIP 没写 -note 直接拒绝，PORTED 没写 -go-ref 直接拒绝。
//     门禁那边也查这一条，这里再查一遍是为了让错误在**填的时候**就出现，
//     而不是攒到最后跑门禁时才一起冒出来。
//
// 它不做的事：不判断裁决对不对。那是人的活，也是事后 git diff 要看的东西。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/snight1983/ds-harness-go/tools/internal/rulingtable"
)

func main() {
	rulingPath := flag.String("ruling", `C:\code\ds-harness-go\docs\portmap\portmap.tsv`, "裁决表路径")
	packageName := flag.String("package", "", "包名，形如 util/brand")
	symbol := flag.String("name", "", "符号名，形如 Branded")
	file := flag.String("file", "", "可选：源文件，同名符号出现在多个文件时用来消歧")
	// 光有 -file 不够：`export class X` 和它下面那句 `export default X` 同名、同文件，
	// 只有行号不同。这个形状在 DSH 里到处都是（几乎每个服务包都有一句默认导出），
	// 所以必须能按行定位，否则那些行永远填不进去。
	line := flag.Int("line", 0, "可选：源文件里的行号，同名符号出现在同一文件时用来消歧")
	decision := flag.String("decision", "", "PORTED / GO_NATIVE / SKIP / OUT_OF_SCOPE")
	goRef := flag.String("go-ref", "", "PORTED 时指向的 Go 符号，形如 invariants.Registry")
	note := flag.String("note", "", "GO_NATIVE / SKIP 时的理由")
	force := flag.Bool("force", false, "允许覆盖已经裁决过的行")
	flag.Parse()

	if err := run(*rulingPath, *packageName, *file, *line, *symbol, *decision, *goRef, *note, *force); err != nil {
		fmt.Fprintf(os.Stderr, "填写失败：%v\n", err)
		os.Exit(1)
	}
}

func run(rulingPath, packageName, file string, line int, symbol, decision, goRef, note string, force bool) error {
	switch {
	case packageName == "" || symbol == "":
		return fmt.Errorf("-package 和 -name 都是必填的")
	case !rulingtable.ValidDecisions[decision]:
		return fmt.Errorf("-decision 必须是 PORTED / GO_NATIVE / SKIP / OUT_OF_SCOPE 之一，收到 %q", decision)
	case decision == rulingtable.Pending:
		return fmt.Errorf("PENDING 是初始值，不该由这个工具填回去")
	case decision == rulingtable.Ported && strings.TrimSpace(goRef) == "":
		return fmt.Errorf("PORTED 必须用 -go-ref 指出对应的 Go 符号")
	case (decision == rulingtable.GoNative || decision == rulingtable.Skip) && strings.TrimSpace(note) == "":
		return fmt.Errorf("%s 必须用 -note 写清理由", decision)
	}
	// 制表符会把一列劈成两列，换行会把一行劈成两行。这两种输入进去之后，
	// 表就不再是九列了，而且不会有任何地方报错。
	for label, value := range map[string]string{"-go-ref": goRef, "-note": note} {
		if strings.ContainsAny(value, "\t\r\n") {
			return fmt.Errorf("%s 里不能有制表符或换行", label)
		}
	}

	rows, err := rulingtable.ReadRuling(rulingPath)
	if err != nil {
		return err
	}

	var hits []int
	for index, row := range rows {
		if row.Package == packageName && row.Name == symbol &&
			(file == "" || row.File == file) && (line == 0 || row.Line == line) {
			hits = append(hits, index)
		}
	}
	switch {
	case len(hits) == 0:
		return fmt.Errorf("裁决表里找不到 %s 的 %s——先跑 portcheck -mode sync，或者确认名字没写错",
			packageName, symbol)
	case len(hits) > 1:
		var where []string
		for _, index := range hits {
			where = append(where, fmt.Sprintf("%s:%d", rows[index].File, rows[index].Line))
		}
		return fmt.Errorf("%s 的 %s 匹配到 %d 行（%s），用 -file 或 -line 指明是哪一条",
			packageName, symbol, len(hits), strings.Join(where, "、"))
	}

	target := &rows[hits[0]]
	if target.Decision != rulingtable.Pending && !force {
		return fmt.Errorf("%s 的 %s 已经裁决为 %s 了，改主意请加 -force",
			packageName, symbol, target.Decision)
	}
	previous := target.Decision
	target.Decision, target.GoRef, target.Note = decision, goRef, note

	// 不重排：writeRuling 写的是传进去的顺序，而 rows 是原样读进来的，
	// 所以这次改动在 git diff 里就是干净的一行。
	if err := rulingtable.WriteRuling(rulingPath, rows); err != nil {
		return err
	}

	fmt.Printf("%s %s（%s:%d）：%s → %s\n",
		packageName, symbol, target.File, target.Line, previous, decision)
	if goRef != "" {
		fmt.Printf("  go_ref: %s\n", goRef)
	}
	if note != "" {
		fmt.Printf("  理由: %s\n", note)
	}
	return nil
}
