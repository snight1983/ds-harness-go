// 本文件的作用：门禁的判断本身——分区、三条规则，以及在一棵目录树上把它们跑一遍。

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// modulePath 是本仓库的 module path。
const modulePath = "github.com/snight1983/ds-harness-go"

// datastorePath 是那个唯一准知道下面是数据库的包。
const datastorePath = modulePath + "/adapter/datastore"

// zone 是一个文件所在的分区。规则按分区放行，不按包名。
type zone int

const (
	// zoneBusiness 是业务包：三条规则全都管得到。
	zoneBusiness zone = iota
	// zoneDatastore 是 adapter/datastore/ 底下：数据库就是它的活儿，三条全放。
	zoneDatastore
	// zoneCmd 是装配点：可以开连接池、挂驱动，但不许自己拼 SQL。
	zoneCmd
	// zoneTools 是门禁工具自己：可以引 datastore，但不许开池、不许拼 SQL。
	zoneTools
	// zoneExempt 是整个跳过的那一处，见 doc.go。
	zoneExempt
)

// zoneOf 按仓库内的相对路径判断分区。path 是斜杠形式。
func zoneOf(path string) zone {
	switch {
	case strings.HasPrefix(path, "internal/devtools/dbcheck/"):
		return zoneExempt
	case path == "adapter/datastore" || strings.HasPrefix(path, "adapter/datastore/"):
		return zoneDatastore
	case strings.HasPrefix(path, "cmd/"):
		return zoneCmd
	case strings.HasPrefix(path, "internal/devtools/"):
		return zoneTools
	default:
		return zoneBusiness
	}
}

// sqlStatement 认的是语句骨架，不是单个关键词。
//
// 只认骨架是为了不误伤：一句错误信息里出现「表」「更新」都很正常，而
// "INSERT INTO" 这种两词搭配在自然语言里几乎不会出现。
var sqlStatement = regexp.MustCompile(`(?is)` + strings.Join([]string{
	`\bselect\b.{0,200}?\bfrom\b`,
	`\binsert\s+into\b`,
	`\bupdate\b.{0,200}?\bset\b`,
	`\bdelete\s+from\b`,
	`\bcreate\s+(table|schema|index|unique\s+index|or\s+replace)\b`,
	`\bdrop\s+(table|schema|index|database)\b`,
	`\balter\s+table\b`,
	`\btruncate\s+table\b`,
	`\bon\s+conflict\b`,
	`\bpragma\s+\w`,
}, "|"))

// sqlPackages 是 database/sql 那一家。
var sqlPackages = map[string]bool{
	"database/sql":        true,
	"database/sql/driver": true,
}

// driverPrefixes 是写死的那份驱动与 ORM 名单。
//
// 它不可能列全，所以还有 driverHint 兜底；两者是「宁可多报」的关系。
var driverPrefixes = []string{
	"github.com/lib/pq",
	"github.com/jackc/pgx",
	"github.com/go-sql-driver/mysql",
	"github.com/mattn/go-sqlite3",
	"modernc.org/sqlite",
	"github.com/microsoft/go-mssqldb",
	"github.com/denisenkom/go-mssqldb",
	"github.com/sijms/go-ora",
	"github.com/ClickHouse/clickhouse-go",
	"github.com/gocql/gocql",
	"go.mongodb.org/mongo-driver",
	"github.com/redis/go-redis",
	"github.com/jmoiron/sqlx",
	"gorm.io",
	"entgo.io/ent",
}

// driverHint 是按路径命名的兜底：换一个没写进名单的驱动，也别想从底下溜过去。
//
// 它只对第三方路径生效。误伤的代价是往 driverPrefixes 旁边加一行豁免，
// 漏判的代价是这道墙悄悄没了——所以宁可误伤。
var driverHint = regexp.MustCompile(
	`(?i)(^|/|-|_)(sql|sqlx|pq|pgx|postgres|postgresql|mysql|mariadb|sqlite\d*|mssql|` +
		`oracle|mongo|redis|clickhouse|cassandra|gocql|badger|bbolt|leveldb|rocksdb|duckdb)($|/|-|_)`)

// isStdlib 判断一条 import 路径是不是标准库：第一段里没有点就是。
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// isDatabaseDriver 判断一条 import 路径是不是某家数据库的驱动。
func isDatabaseDriver(path string) bool {
	if isStdlib(path) || path == modulePath || strings.HasPrefix(path, modulePath+"/") {
		return false
	}
	for _, prefix := range driverPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return driverHint.MatchString(path)
}

// isDatastore 判断一条 import 路径指的是不是那个抽象层本身。
func isDatastore(path string) bool {
	return path == datastorePath || strings.HasPrefix(path, datastorePath+"/")
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

// checkTree 在 root 底下把三条规则跑一遍。
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
		region := zoneOf(slashed)
		if region == zoneExempt {
			return nil
		}
		report.Files++

		file, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("解析 %s 失败：%w", slashed, parseErr)
		}
		report.Findings = append(report.Findings, checkFile(fileSet, file, slashed, region)...)
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

// checkFile 在一个已经解析好的文件上跑那三条规则。
func checkFile(fileSet *token.FileSet, file *ast.File, path string, region zone) []Finding {
	var findings []Finding
	line := func(pos token.Pos) int { return fileSet.Position(pos).Line }

	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		switch {
		case sqlPackages[imported] && region != zoneDatastore && region != zoneCmd:
			findings = append(findings, Finding{
				File: path, Line: line(spec.Pos()), Rule: "import-database/sql",
				Detail: fmt.Sprintf("引了 %q。下面是不是个数据库，只有 datastore 该知道；"+
					"这里要的是一道业务接口。", imported),
			})
		case isDatabaseDriver(imported) && region != zoneDatastore && region != zoneCmd:
			findings = append(findings, Finding{
				File: path, Line: line(spec.Pos()), Rule: "import-driver",
				Detail: fmt.Sprintf("引了数据库驱动 %q。驱动只挂在 datastore/ 和装配点上。", imported),
			})
		case isDatastore(imported) && region == zoneBusiness:
			findings = append(findings, Finding{
				File: path, Line: line(spec.Pos()), Rule: "import-datastore",
				Detail: fmt.Sprintf("引了具体实现 %q。业务包引的该是业务接口，"+
					"否则「换一份介质」又变成了一次跨包改动。", imported),
			})
		}
	}

	if region == zoneDatastore {
		return findings
	}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		if match := sqlStatement.FindString(text); match != "" {
			findings = append(findings, Finding{
				File: path, Line: line(literal.Pos()), Rule: "sql-text",
				Detail: fmt.Sprintf("字符串里有一句 SQL（%q）。SQL 只写在 datastore/ 底下。",
					strings.Join(strings.Fields(match), " ")),
			})
		}
		return true
	})
	return findings
}
