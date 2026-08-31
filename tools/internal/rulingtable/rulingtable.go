// Package rulingtable 是裁决表的读写和取值定义。
//
// 它被抽出来共用，是因为读它的（portcheck 门禁）和写它的（rule 填表）是两个程序。
// 如果两边各存一份 TSV 解析，它们迟早会在某个边角上分道扬镳——补齐空列的规则、
// 行序、CRLF 的处理——而分歧的表现不是报错，是一张看起来正常、其实列错位的表。
// 表是这次移植的唯一账本，账本的读写口径必须只有一份。
package rulingtable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 裁决表里允许出现的结论。
//
// 取值刻意做得少：多一个模糊选项，就多一个可以把「我没想清楚」藏进去的地方。
const (
	// Pending 是每一行的初始值，也是唯一会让门禁变红的值。
	Pending = "PENDING"
	// Ported 表示已经在 Go 里做出来了，GoRef 必须指向真实存在的符号。
	Ported = "PORTED"
	// GoNative 表示 Go 标准库或成熟三方库已有等价能力，用它代替。
	// Note 必须写清用什么代替——「Go 有」不是理由，「用 encoding/json 的 struct tag 代替」才是。
	GoNative = "GO_NATIVE"
	// Skip 表示不抄。Note 必须写理由，且这个理由要经得起人看。
	Skip = "SKIP"
	// OutOfScope 表示属于产品外壳（Web UI、宿主进程、打包、示例等），
	// 不在「agent 框架」这个移植范围内。
	OutOfScope = "OUT_OF_SCOPE"
)

// ValidDecisions 是允许写进裁决表的全部值。写别的一律报错，
// 免得有人用一个自造的状态把 PENDING 绕过去。
var ValidDecisions = map[string]bool{
	Pending:    true,
	Ported:     true,
	GoNative:   true,
	Skip:       true,
	OutOfScope: true,
}

// Row 是裁决表的一行：清单的六列，加上人填的三列。
type Row struct {
	Package  string
	File     string
	Line     int
	Kind     string
	Name     string
	From     string
	Decision string // 上面五个常量之一
	GoRef    string // Ported 时指向 Go 符号，形如 session.Store.Append
	Note     string // GoNative / Skip 时的理由
}

// Key 是一行的身份。用「包 + 文件 + 行号 + 名字」而不是自增序号，
// 这样清单重跑、行序变化都不会让已有的裁决对不上号。
func (r Row) Key() string {
	return r.Package + "\x00" + r.File + "\x00" + strconv.Itoa(r.Line) + "\x00" + r.Name
}

// Header 是裁决表的表头，也是列顺序的唯一定义。
const Header = "package\tfile\tline\tkind\tname\tfrom\tdecision\tgo_ref\tnote"

// ReadExports 读机器清单（六列，没有裁决那三列）。
func ReadExports(path string) ([]Row, error) {
	records, err := readTSV(path, 6)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(records))
	for _, fields := range records {
		line, _ := strconv.Atoi(fields[2])
		rows = append(rows, Row{
			Package: fields[0], File: fields[1], Line: line,
			Kind: fields[3], Name: fields[4], From: fields[5],
		})
	}
	return rows, nil
}

// ReadRuling 读裁决表。文件不存在当空表处理——第一次跑 sync 时它本来就还没有。
func ReadRuling(path string) ([]Row, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	records, err := readTSV(path, 9)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(records))
	for _, fields := range records {
		line, _ := strconv.Atoi(fields[2])
		rows = append(rows, Row{
			Package: fields[0], File: fields[1], Line: line,
			Kind: fields[3], Name: fields[4], From: fields[5],
			Decision: fields[6], GoRef: fields[7], Note: fields[8],
		})
	}
	return rows, nil
}

// readTSV 读一份带表头的 TSV，并把每行补齐到 want 列。
//
// 补齐而不是报错，是因为末尾的空列在很多编辑器里会被剪掉——那是保存文件的副作用，
// 不该被当成数据损坏。列数**多**于 want 才是真的对不上，那时候报错。
func readTSV(path string, want int) ([][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var records [][]string
	for index, line := range lines {
		if index == 0 || strings.TrimSpace(line) == "" {
			continue // 跳过表头和空行
		}
		fields := strings.Split(line, "\t")
		if len(fields) > want {
			return nil, fmt.Errorf("%s 第 %d 行有 %d 列，期望 %d 列", path, index+1, len(fields), want)
		}
		for len(fields) < want {
			fields = append(fields, "")
		}
		records = append(records, fields)
	}
	return records, nil
}

// WriteRuling 把整张表写回去，行序就是传进来的行序。
//
// 调用方负责决定顺序：sync 会先 Sort，填表则原样写回，这样一次填写在 git diff 里
// 就只是一行的改动。
func WriteRuling(path string, rows []Row) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var builder strings.Builder
	builder.WriteString(Header + "\n")
	for _, row := range rows {
		fmt.Fprintf(&builder, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Package, row.File, row.Line, row.Kind, row.Name, row.From,
			row.Decision, row.GoRef, row.Note)
	}
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

// Sort 的排序必须和提取器一致，这样裁决表重写之后 diff 才只包含真正的改动。
func Sort(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Name < b.Name
	})
}
