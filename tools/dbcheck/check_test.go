// 本文件的作用：压这道门禁自己——分区分对没有、那三条规则拦得住什么、
// 以及哪些长得像 SQL 的东西不该被误伤。

package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// findingsIn 把一段源码按指定路径过一遍门禁，交出判红。
func findingsIn(t *testing.T, path, source string) []Finding {
	t.Helper()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return checkFile(fileSet, file, path, zoneOf(path))
}

// rulesOf 把一批判红里的规则名抽出来，好和期望的那串比。
func rulesOf(findings []Finding) []string {
	rules := make([]string, 0, len(findings))
	for _, finding := range findings {
		rules = append(rules, finding.Rule)
	}
	return rules
}

func Test分区按路径分而不是按包名(t *testing.T) {
	cases := map[string]zone{
		"datastore/medium.go":               zoneDatastore,
		"datastore/sessionstore/backend.go": zoneDatastore,
		"cmd/llmmockserver/main.go":         zoneCmd,
		"tools/doccheck/check.go":           zoneTools,
		"tools/dbcheck/check.go":            zoneExempt,
		"session/persistence/backend.go":    zoneBusiness,
		"storage/backend.go":                zoneBusiness,
		// 名字里带 datastore 不等于在 datastore 底下。
		"session/datastorehelper/x.go": zoneBusiness,
	}
	for path, want := range cases {
		if got := zoneOf(path); got != want {
			t.Errorf("%s 分到了 %d，要的是 %d", path, got, want)
		}
	}
}

// 这三条正是被收口掉的那两个包干过的事，门禁的意义就是让它们再也回不来。
func Test业务包碰数据库的三种样子都拦得住(t *testing.T) {
	source := `package persistence

import (
	"database/sql"

	_ "github.com/lib/pq"

	"github.com/snight1983/ds-harness-go/datastore"
)

var pool *sql.DB
var _ = datastore.Config{}

const create = "CREATE TABLE sessions (id TEXT PRIMARY KEY)"
`
	findings := findingsIn(t, "session/persistence/backend.go", source)
	want := []string{"import-database/sql", "import-driver", "import-datastore", "sql-text"}
	if got := rulesOf(findings); !equalStrings(got, want) {
		t.Fatalf("判出来的是 %v，要的是 %v", got, want)
	}
}

// 装配点可以开池、挂驱动——谁来部署、连哪个库是它的事；但它照样不许自己拼 SQL。
func Test装配点开得了池但拼不得SQL(t *testing.T) {
	source := `package main

import (
	"database/sql"

	_ "github.com/lib/pq"

	"github.com/snight1983/ds-harness-go/datastore"
)

var pool *sql.DB
var _ = datastore.Config{}

const query = "SELECT id FROM sessions"
`
	findings := findingsIn(t, "cmd/dsh/main.go", source)
	if got := rulesOf(findings); !equalStrings(got, []string{"sql-text"}) {
		t.Fatalf("判出来的是 %v，要的是 [sql-text]", got)
	}
}

// datastore 底下三条全放：那儿就是干这个的。
func Test抽象层底下什么都不拦(t *testing.T) {
	source := `package datastore

import (
	"database/sql"

	_ "github.com/lib/pq"
)

var pool *sql.DB

const create = "CREATE TABLE units (name TEXT PRIMARY KEY)"
`
	if findings := findingsIn(t, "datastore/medium.go", source); len(findings) != 0 {
		t.Fatalf("datastore 底下不该判红，实际 %v", findings)
	}
}

// 门禁工具要引 datastore 才诊断得了它，但它一样不许自己开池。
func Test门禁工具引得了datastore但开不了池(t *testing.T) {
	source := `package main

import (
	"database/sql"

	"github.com/snight1983/ds-harness-go/datastore"
)

var pool *sql.DB
var _ = datastore.Config{}
`
	findings := findingsIn(t, "tools/portcheck/main.go", source)
	if got := rulesOf(findings); !equalStrings(got, []string{"import-database/sql"}) {
		t.Fatalf("判出来的是 %v，要的是 [import-database/sql]", got)
	}
}

// 换一个没写进名单的驱动，兜底那条按路径命名的规则要接得住。
func Test没写进名单的驱动被兜底规则接住(t *testing.T) {
	for _, path := range []string{
		"github.com/some/postgres-driver",
		"gitee.com/x/mysql",
		"example.com/foo/sqlite3",
		"example.com/duckdb",
	} {
		if !isDatabaseDriver(path) {
			t.Errorf("%q 该被兜底规则接住", path)
		}
	}
	// 标准库和本仓库自己的包不走这条规则，否则 database/sql 那条和 datastore
	// 那条会各报一遍同一件事。
	for _, path := range []string{
		"database/sql", "encoding/json", "github.com/google/uuid",
		modulePath + "/storage", datastorePath,
	} {
		if isDatabaseDriver(path) {
			t.Errorf("%q 被兜底规则误伤了", path)
		}
	}
}

// 认的是语句骨架，不是单个关键词：错误信息里出现「更新」「表」很正常，
// 误伤会逼着人把话说得不像人话。
func Test认的是语句骨架不是关键词(t *testing.T) {
	for _, text := range []string{
		"SELECT name FROM units",
		"insert into entries (seq) values (0)",
		"UPDATE streams SET next_seq = 6",
		"DELETE FROM entries WHERE seq < 3",
		"create table if not exists meta (x int)",
		"DROP SCHEMA IF EXISTS ns CASCADE",
		"ALTER TABLE units ADD COLUMN kind TEXT",
		"ON CONFLICT (name) DO NOTHING",
	} {
		if !sqlStatement.MatchString(text) {
			t.Errorf("没认出这是一句 SQL：%q", text)
		}
	}
	for _, text := range []string{
		"更新失败：这张表还没建出来",
		"select 这个词单独出现不算",
		"from 这个词单独出现也不算",
		"这条记录要 insert 到哪张表里",
		"表格式对不上，拒绝打开",
	} {
		if sqlStatement.MatchString(text) {
			t.Errorf("误伤了一句人话：%q", text)
		}
	}
}

// 判红要点得进去，所以它排出来的必须是编译器那种 文件:行 的样子。
func Test判红排成编译器那种行(t *testing.T) {
	finding := Finding{File: "session/x.go", Line: 12, Rule: "sql-text", Detail: "有一句 SQL"}
	if got := finding.String(); !strings.HasPrefix(got, "session/x.go:12：[sql-text] ") {
		t.Fatalf("排出来是 %q", got)
	}
}

// 跳过的那几个目录里没有本仓库参与构建的 Go 源码。
func Test该跳过的目录跳过了(t *testing.T) {
	for _, name := range []string{".git", ".github", ".claude", "node_modules", "testdata"} {
		if !skipDir(name) {
			t.Errorf("%q 该跳过", name)
		}
	}
	for _, name := range []string{"datastore", "session", "tools"} {
		if skipDir(name) {
			t.Errorf("%q 不该跳过", name)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
