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

// Key 是一行在**某一份**清单里的精确身份：包 + 文件 + 行号 + 名字。
//
// 只在同一次清单快照内部比对时用它（门禁那两项就是——同步之后裁决表的行号是从
// 清单刷过来的，两边必然一致）。跨上游版本认同一个符号**不能**用它，见 [Row.MatchKey]。
func (r Row) Key() string {
	return r.Package + "\x00" + r.File + "\x00" + strconv.Itoa(r.Line) + "\x00" + r.Name
}

// MatchKey 是一行跨上游版本的身份：包 + 文件 + 种类 + 名字，**不含行号**。
//
// 为什么必须把行号排除掉：行号是派生数据，上游每发一版都会整体漂移，而人填的裁决
// （decision/go_ref/note）是这套流程里最贵的东西。拿 [Row.Key] 去跨版本配对的话，
// 一个只是往下挪了两行的符号会同时变成「一条新的 PENDING」和「一条 STALE」，
// 那份裁决就凭空丢了。实测从旧快照换到 v0.1.2-alpha.3：6224 条 PENDING 里有 2947 条
// 是这么来的——同包同文件同种类同名，裁决就躺在旁边那条 STALE 上。
//
// 种类算进键里而名字之外不再加别的，是因为名字加种类已经把 9771 条清单分成 9683 个
// 不同的键；剩下那 18 个重复键（106 条，绝大多数是名字为 `*` 的 star 转发导出）
// 靠 [MatchIndex] 按行序配序号来分。
func (r Row) MatchKey() string {
	return r.Package + "\x00" + r.File + "\x00" + r.Kind + "\x00" + r.Name
}

// MatchIndex 给一批行算出各自「同键第几个」的下标，让 [Row.MatchKey] 重复时也能稳定配对。
//
// 同一个键下按行号排序后取序号：一个文件里有 9 条 star 转发、上游仍旧有 9 条时，
// 它们按出现次序一一对上。这比「重复就整批不配」强——后者会把那 106 条的裁决全丢掉；
// 也比「随便挑一条」强——那个结果取决于 map 遍历次序，每次跑都不一样。
//
// 交回的切片和 rows 一一对应。
func MatchIndex(rows []Row) []int {
	order := make([]int, len(rows))
	for index := range rows {
		order[index] = index
	}
	// 按键、再按行号排，于是同键的行在 order 里连成一段、且段内是行序。
	sort.SliceStable(order, func(i, j int) bool {
		left, right := rows[order[i]], rows[order[j]]
		if leftKey, rightKey := left.MatchKey(), right.MatchKey(); leftKey != rightKey {
			return leftKey < rightKey
		}
		return left.Line < right.Line
	})

	indexes := make([]int, len(rows))
	seen := map[string]int{}
	for _, position := range order {
		key := rows[position].MatchKey()
		indexes[position] = seen[key]
		seen[key]++
	}
	return indexes
}

// QualifiedMatchKey 是 [Row.MatchKey] 加上 [MatchIndex] 算出的序号，可以直接当 map 键。
func QualifiedMatchKey(row Row, index int) string {
	return row.MatchKey() + "\x00#" + strconv.Itoa(index)
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
