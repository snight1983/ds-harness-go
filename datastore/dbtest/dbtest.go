// 本文件的作用：把「这一轮测试跑在哪种库上」这件事定下来，交给 datastore 底下
// 那几个测试包共用。
//
// 新增: 整个包都是本仓库自有的。
//
// # 为什么缺省是 SQLite
//
// 一个要靠外部服务才跑得起来的测试等于没有测试：它在开发机上永远跳过，在 CI 上
// 永远只由一个环境变量决定跑不跑，于是「跑绿了」和「一行没跑」长得一模一样。
// 缺省落在一个临时目录里的 SQLite 库文件上，这批用例就在 `go test ./...` 里整批
// 执行，不必先起一台库。
//
// 设了 DSH_POSTGRES_DSN 就整批改跑 Postgres。两种都要跑得过，因为这批用例压的正是
// 两边会分歧的那些地方（ON CONFLICT 的语义、只读事务里的快照、外键与主键冲突、
// 并发建表），而一种方言自己跟自己是没有分歧的。
//
// # 为什么 datastore 自己没用这个包
//
// [datastore] 那个包的测试写在包内（要够得着未导出的东西），而本包引着
// [datastore]——包内测试再引本包就成了环。所以那边留着一份自己的，两处都不长。
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	// 驱动只挂在这里。被测的那几个包只用 database/sql，理由见 datastore 的包文档。
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"github.com/snight1983/ds-harness-go/datastore"
)

// dsnEnv 是指向一个可用 Postgres 的连接串所在的环境变量。设了就整批改跑 Postgres。
const dsnEnv = "DSH_POSTGRES_DSN"

// requireEnv 是「这一轮必须跑 Postgres」的开关。
//
// 缺省的 SQLite 让这批用例任何时候都跑得起来，于是「跳过」这个洞没了；剩下的洞是
// 声明了要跑 Postgres 的那一轮**悄悄退回了 SQLite**——连接串没传进来、service
// container 没起来、端口没映上，都长这个样子。设了这个变量就把那种退回变成失败。
const requireEnv = "DSH_REQUIRE_POSTGRES"

// backend 是这一轮用例跑在哪种库上。
type backend struct {
	driver  string
	dsn     string
	dialect datastore.Dialect
	// drop 把一个命名空间连同它底下的表整个删掉。
	//
	// 这一步没法由方言给：datastore 不提供「删掉一个命名空间」，因为那不是任何
	// 一条业务路径要的动作——只有测试要它。
	drop func(t *testing.T, db *sql.DB, namespace string)
}

// active 是这一轮选中的那个后端，由 [Run] 定下来。
var active backend

// namespaceCounter 保证同一次 go test 里每份介质拿到不同的命名空间名。
var namespaceCounter atomic.Int64

// Run 选好这一轮的库，跑完整批用例，交回退出码。
//
// 交回退出码而不是自己 os.Exit，是为了让调用方的 TestMain 保持成一行看得懂的样子：
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }
func Run(m *testing.M) int {
	if dsn := os.Getenv(dsnEnv); dsn != "" {
		active = postgresBackend(dsn)
		return m.Run()
	}
	if os.Getenv(requireEnv) != "" {
		fmt.Fprintf(os.Stderr,
			"设了 %s 却没有 %s：这一轮声明了要跑 Postgres，但连接串是空的"+
				"——多半是 service container 没起来或端口没映上。这里必须失败，"+
				"不能退回 SQLite，否则一整批本该压两种方言的用例只压了一种。\n",
			requireEnv, dsnEnv)
		return 1
	}

	dir, err := os.MkdirTemp("", "dsh-dbtest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "建不出 SQLite 的临时目录：%v\n", err)
		return 1
	}
	active = sqliteBackend(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	return code
}

func postgresBackend(dsn string) backend {
	return backend{
		driver:  "postgres",
		dsn:     dsn,
		dialect: datastore.Postgres(),
		drop: func(t *testing.T, db *sql.DB, namespace string) {
			t.Helper()
			// 这个名字是 [Namespace] 自己拼的，只含小写字母、数字和下划线。
			if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + namespace + `" CASCADE`); err != nil {
				t.Errorf("删不掉命名空间 %q：%v", namespace, err)
			}
		},
	}
}

// sqliteBackend 把整轮用例落在**同一个**库文件上。
//
// 一个文件而不是一条用例一个：那样每条用例各自一个库，两个命名空间在同一个库里
// 互不相干这件事就一次也没被压到——而那正是 SQLite 这一支最新、最容易错的地方。
//
// DSN 上那三个 pragma 是 datastore 管不了、但装配方该设的那些（见
// [datastore.SQLite]）：busy_timeout 让并发的第一次打开等一等而不是当场失败，
// foreign_keys 把条目表那道外键真的打开——不打开的话 Postgres 那一轮拦得住的东西
// 这一轮拦不住，两轮就不再压同一件事，WAL 让读不挡写。
func sqliteBackend(dir string) backend {
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "dbtest.db")) +
		"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	return backend{
		driver:  "sqlite",
		dsn:     dsn,
		dialect: datastore.SQLite(),
		drop:    dropSQLiteNamespace,
	}
}

func dropSQLiteNamespace(t *testing.T, db *sql.DB, namespace string) {
	t.Helper()
	ctx := context.Background()

	// 整个清理钉在**同一条**连接上，因为下一句 pragma 是连接上的状态：
	// 换一条连接它就没了（这正是 [datastore.SQLite] 那段说的事）。
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Errorf("清理命名空间 %q 时取不到连接：%v", namespace, err)
		return
	}
	defer func() { _ = conn.Close() }()

	// 清理期间把外键关掉：条目表引着流表，按 sqlite_master 给的次序删就会撞上
	// 「先删了被引的那张」。清理不需要那道保护，而按依赖排序等于在测试里重写
	// 一遍库的结构，那份复制品迟早和真的对不上。
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Errorf("关不掉外键：%v", err)
		return
	}

	prefix := namespace + "."
	// 比前缀而不是 LIKE：命名空间里可以有下划线，而下划线是 LIKE 的通配符。
	rows, err := conn.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND substr(name, 1, ?) = ?`,
		len(prefix), prefix)
	if err != nil {
		t.Errorf("列不出命名空间 %q 的表：%v", namespace, err)
		return
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Errorf("读不出表名：%v", err)
			break
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	for _, name := range tables {
		// 表名来自 sqlite_master，引号是它里面唯一需要转义的字符。
		if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS "`+name+`"`); err != nil {
			t.Errorf("删不掉表 %q：%v", name, err)
		}
	}
}

// DSN 是这一轮的连接串。
func DSN() string { return active.dsn }

// Dialect 是这一轮的方言，直接填进 [datastore.Config]。
func Dialect() datastore.Dialect { return active.dialect }

// Pool 按这一轮选中的驱动开一条连接池。
//
// 每次都新开一条，是因为 [datastore.Config.DB] 归介质所有：上一份 Close 时已经把
// 它那条池关掉了，复用就会拿到一个关掉的池。
func Pool(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open(active.driver, dsn)
	if err != nil {
		t.Fatalf("连不上库：%v", err)
	}
	return db
}

// Config 拼出一份指向这一轮那种库、那个命名空间的介质配置，连同它自己那条池。
func Config(t *testing.T, dsn, namespace string) (datastore.Config, *sql.DB) {
	t.Helper()

	db := Pool(t, dsn)
	return datastore.Config{DB: db, Dialect: active.dialect, Namespace: namespace}, db
}

// Namespace 造一个本次用例专用的命名空间名，并登记好清理。
//
// 一个命名空间就是一份介质，所以「每条用例一份全新介质」就是「每条用例一个新
// 名字」。共用一份的话，一条用例写下的东西会变成下一条用例的隐含前提。
//
// prefix 由调用方给，好让不同测试包在同一个库文件上也各占各的名字段。
func Namespace(t *testing.T, prefix, dsn string) string {
	t.Helper()

	name := fmt.Sprintf("%s_%d_%d", prefix, os.Getpid(), namespaceCounter.Add(1))
	t.Cleanup(func() {
		db, err := sql.Open(active.driver, dsn)
		if err != nil {
			t.Errorf("清理命名空间 %q 时连不上库：%v", name, err)
			return
		}
		defer func() { _ = db.Close() }()

		active.drop(t, db, name)
	})
	return name
}
