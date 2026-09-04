// 本文件的作用：本包测试共用的那几样——这一轮跑在哪种库上、每条用例一份全新命名空间，
// 以及绕过本包直接改介质的那几下。
//
// # 这一轮跑在哪种库上
//
// 缺省是 SQLite，落在一个临时目录里的库文件上：本包每一行代码都要一个**真的**数据库
// 才执行得到，而一个要靠外部服务才跑得起来的测试等于没有测试——它在开发机上永远跳过，
// 在 CI 上永远只由一个环境变量决定跑不跑。SQLite 让这批用例在 `go test ./...` 里就整批
// 执行，不必先起一台库。
//
// 设了 DSH_POSTGRES_DSN 就整批改跑 Postgres。两种都要跑得过，因为这批用例压的正是
// 两边会分歧的那些地方（ON CONFLICT 的语义、只读事务里的快照、外键与主键冲突、
// 并发建表），而一种方言自己跟自己是没有分歧的。
//
// # 为什么不拿一个假库把覆盖率刷上去
//
// sqlmock 之类的东西验的是「我拼出了我以为我会拼的那句 SQL」。这个包里真正会出事的
// 地方恰恰是假库看不见的那些——所以这里宁可挂一个真的引擎。

package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	// 驱动只在测试里挂上来。库本身只用 database/sql，理由见包文档。
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
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
	// driver 是 sql.Open 的第一个参数。
	driver string
	// dsn 是连接串。
	dsn string
	// dialect 是配这种库的方言。
	dialect Dialect
	// drop 把一个命名空间连同它底下的表整个删掉。
	//
	// 这一步没法由方言给：本包不提供「删掉一个命名空间」，因为那不是任何一条
	// 业务路径要的动作——只有测试要它。
	drop func(t *testing.T, db *sql.DB, namespace string)
}

// active 是这一轮选中的那个后端，由 [TestMain] 定下来。
var active backend

// namespaceCounter 保证同一次 go test 里每份介质拿到不同的命名空间名。
var namespaceCounter atomic.Int64

func TestMain(m *testing.M) {
	if dsn := os.Getenv(dsnEnv); dsn != "" {
		active = postgresBackend(dsn)
		os.Exit(m.Run())
	}
	if os.Getenv(requireEnv) != "" {
		fmt.Fprintf(os.Stderr,
			"设了 %s 却没有 %s：这一轮声明了要跑 Postgres，但连接串是空的"+
				"——多半是 service container 没起来或端口没映上。这里必须失败，"+
				"不能退回 SQLite，否则一整批本该压两种方言的用例只压了一种。\n",
			requireEnv, dsnEnv)
		os.Exit(1)
	}

	dir, err := os.MkdirTemp("", "dsh-datastore")
	if err != nil {
		fmt.Fprintf(os.Stderr, "建不出 SQLite 的临时目录：%v\n", err)
		os.Exit(1)
	}
	active = sqliteBackend(dir)
	code := m.Run()
	// 收在 m.Run 之后而不是 defer 里：os.Exit 不跑 defer。
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func postgresBackend(dsn string) backend {
	return backend{
		driver:  "postgres",
		dsn:     dsn,
		dialect: Postgres(),
		drop: func(t *testing.T, db *sql.DB, namespace string) {
			t.Helper()
			// 这个名字是本文件自己拼的，只含小写字母、数字和下划线。
			if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + namespace + `" CASCADE`); err != nil {
				t.Errorf("删不掉命名空间 %q：%v", namespace, err)
			}
		},
	}
}

// sqliteBackend 把整轮用例落在**同一个**库文件上。
//
// 一个文件而不是一条用例一个：那样每条用例各自一个库，两个命名空间在同一个库里
// 互不相干这件事就一次也没被压到——而那正是 SQLite 这一支最新、最容易错的地方
// （见 [sqliteDialect.Qualify]）。
//
// DSN 上那三个 pragma 是本包管不了、但装配方该设的那些（见 [SQLite]）：
// busy_timeout 让并发的第一次打开等一等而不是当场失败，foreign_keys 把条目表那道
// 外键真的打开——不打开的话 Postgres 那一轮拦得住的东西这一轮拦不住，两轮就不再
// 压同一件事，WAL 让读不挡写。
func sqliteBackend(dir string) backend {
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "datastore.db")) +
		"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	return backend{
		driver:  "sqlite",
		dsn:     dsn,
		dialect: SQLite(),
		drop: func(t *testing.T, db *sql.DB, namespace string) {
			t.Helper()
			ctx := context.Background()

			// 整个清理钉在**同一条**连接上，因为下一句 pragma 是连接上的状态：
			// 换一条连接它就没了（这正是 [SQLite] 那段说的事）。
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Errorf("清理命名空间 %q 时取不到连接：%v", namespace, err)
				return
			}
			defer func() { _ = conn.Close() }()

			// 清理期间把外键关掉：条目表引着流表，按 sqlite_master 给的次序删就会
			// 撞上「先删了被引的那张」。清理不需要那道保护，而按依赖排序等于在测试里
			// 重写一遍库的结构，那份复制品迟早和真的对不上。
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
		},
	}
}

// testDSN 是这一轮的连接串。
func testDSN(t *testing.T) string {
	t.Helper()
	return active.dsn
}

// openPool 按这一轮选中的驱动开一条连接池。
func openPool(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open(active.driver, dsn)
	if err != nil {
		t.Fatalf("连不上库：%v", err)
	}
	return db
}

// freshNamespace 造一个本次用例专用的命名空间名，并登记好清理。
//
// 一个命名空间就是一份介质（见 [Config.Namespace]），所以「每条用例一份全新介质」
// 就是「每条用例一个新名字」。共用一份的话，一条用例写下的东西会变成下一条用例
// 的隐含前提。
func freshNamespace(t *testing.T, dsn string) string {
	t.Helper()

	name := fmt.Sprintf("ds_test_%d_%d", os.Getpid(), namespaceCounter.Add(1))
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

// freshMedium 取出连接串，再要一个本次用例专用的命名空间名。
//
// 分成两步交出来，是因为「同一份介质重开一次」那几条用例得记住这两个值。
func freshMedium(t *testing.T) (dsn, namespace string) {
	t.Helper()

	dsn = testDSN(t)
	return dsn, freshNamespace(t, dsn)
}

// openMedium 在指定介质上打开一份，成功的话登记好收尾。
//
// 每次都新开一条连接池，是因为 [Config.DB] 归介质所有：上一份 Close 时已经把它
// 那条池关掉了，复用就会拿到一个关掉的池。
//
// 开不出来是**返回**而不是当场 t.Fatal：有用例压的正是「这份介质开不得」。
func openMedium(t *testing.T, dsn, namespace string) (*Medium, error) {
	t.Helper()

	db := openPool(t, dsn)
	medium, err := Open(t.Context(), Config{DB: db, Dialect: active.dialect, Namespace: namespace})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	t.Cleanup(func() {
		if closeErr := medium.Close(context.Background()); closeErr != nil {
			t.Errorf("关介质不该失败：%v", closeErr)
		}
	})
	return medium, nil
}

// newMedium 在一份全新介质上打开，测试结束时收掉。
func newMedium(t *testing.T) *Medium {
	t.Helper()

	dsn, namespace := freshMedium(t)
	medium, err := openMedium(t, dsn, namespace)
	if err != nil {
		t.Fatalf("打开介质失败：%v", err)
	}
	return medium
}

// newRecords 在一份全新介质上开一个记录集。
func newRecords(t *testing.T, spec RecordSpec) *RecordUnit {
	t.Helper()

	unit, err := newMedium(t).OpenRecords(t.Context(), spec)
	if err != nil {
		t.Fatalf("打开记录集 %q 失败：%v", spec.Name, err)
	}
	return unit
}

// newLog 在一份全新介质上开一个日志集。
func newLog(t *testing.T, name string) *LogUnit {
	t.Helper()

	unit, err := newMedium(t).OpenLog(t.Context(), LogSpec{Name: name, Version: 1})
	if err != nil {
		t.Fatalf("打开日志集 %q 失败：%v", name, err)
	}
	return unit
}

// seqsOf 把一段条目的 seq 抽出来，好和期望的那串比。
func seqsOf(entries []Entry) []int64 {
	seqs := make([]int64, 0, len(entries))
	for _, entry := range entries {
		seqs = append(seqs, entry.Seq)
	}
	return seqs
}

// entriesFrom 造一批从 base 起连续的条目。
func entriesFrom(base, count int64) []Entry {
	entries := make([]Entry, 0, count)
	for offset := range count {
		seq := base + offset
		entries = append(entries, Entry{
			Seq:     seq,
			Payload: []byte(fmt.Sprintf(`{"seq":%d}`, seq)),
		})
	}
	return entries
}
