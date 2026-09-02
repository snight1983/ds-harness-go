// 本文件的作用：压方言那几处——名字的形状、占位符的改写、限定标识符怎么拼。
// 这一整个文件不碰库。

package datastore

import (
	"database/sql"
	"strings"
	"testing"
)

// 这些名字会被拼进 SQL 文本当标识符，不是绑定参数，所以形状必须先卡死。
func Test名字的形状卡在标识符能直接用的那一档(t *testing.T) {
	for _, name := range []string{"a", "sessions", "kv_unit", "a1", "x_9_y"} {
		if !ValidName(name) {
			t.Errorf("%q 该是合法名字", name)
		}
	}
	for _, name := range []string{
		"", "Public", "1st", "with-dash", "带中文", `a"b`, "a;b", "_lead", "trail ",
	} {
		if ValidName(name) {
			t.Errorf("%q 不该是合法名字", name)
		}
	}
}

// 占位符按出现次序编号。编错了的表现是「参数错位」，而那要到运行到那一句才看得见。
func TestRebind按次序把问号换成编号(t *testing.T) {
	got := Postgres().Rebind(`INSERT INTO t (a, b, c) VALUES (?, ?, ?) ON CONFLICT (a) DO UPDATE SET b = ?`)
	want := `INSERT INTO t (a, b, c) VALUES ($1, $2, $3) ON CONFLICT (a) DO UPDATE SET b = $4`
	if got != want {
		t.Fatalf("改写成了 %q，要的是 %q", got, want)
	}
	if got := Postgres().Rebind(`SELECT 1`); got != `SELECT 1` {
		t.Errorf("没有占位符的语句被改动了：%q", got)
	}
}

// 每一处表名都限定到命名空间，不靠 search_path：靠它就等于让「这条语句落在哪个
// 命名空间」取决于这次从池里抓到了哪条连接。
func TestQualify永远点名命名空间(t *testing.T) {
	if got, want := Postgres().Qualify("dsh", "sessions"), `"dsh"."sessions"`; got != want {
		t.Fatalf("拼出来是 %q，要的是 %q", got, want)
	}
}

// 读那一路必须一次事务里看同一个快照，否则交出去的令牌会配着另一份日志。
func Test读事务是只读且看得到同一个快照(t *testing.T) {
	options := Postgres().ReadTxOptions()
	if options == nil {
		t.Fatal("读事务的选项是 nil，那就退回了库的缺省（读已提交）")
	}
	if !options.ReadOnly {
		t.Error("读那一路该声明只读")
	}
	if options.Isolation < sql.LevelRepeatableRead {
		t.Errorf("隔离级别是 %v，比可重复读还松", options.Isolation)
	}
}

// GREATEST 和聚合的 MAX 同名不同义，所以这一处非分方言不可。
func TestGreatest拼的是标量取大(t *testing.T) {
	if got, want := Postgres().Greatest("next_seq", "?"), "GREATEST(next_seq, ?)"; got != want {
		t.Fatalf("拼出来是 %q，要的是 %q", got, want)
	}
}

// 超过 63 字节 Postgres 不报错，直接截断：两张本该互不相干的表会被截成同一张。
func Test物理表名超过上限被拒而不是被截断(t *testing.T) {
	medium := &Medium{dialect: Postgres()}
	limit := Postgres().MaxIdentifierBytes()

	if _, err := medium.physical(strings.Repeat("a", limit+1)); err == nil {
		t.Fatalf("%d 字节的表名该被拒", limit+1)
	}
	// 边界上那一个必须过：卡在 63 而不是 62，否则合法的名字会被误杀。
	if _, err := medium.physical(strings.Repeat("a", limit)); err != nil {
		t.Fatalf("%d 字节的表名不该被拒：%v", limit, err)
	}
}

// 上限 0 表示没有上限，不能被当成「所有名字都超了」。
func TestSQLite没有标识符长度上限(t *testing.T) {
	if got := SQLite().MaxIdentifierBytes(); got != 0 {
		t.Fatalf("上限是 %d，SQLite 不截断标识符，该是 0", got)
	}
	medium := &Medium{dialect: SQLite()}
	if _, err := medium.physical(strings.Repeat("a", 200)); err != nil {
		t.Fatalf("200 字节的表名在 SQLite 上不该被拒：%v", err)
	}
}

// `?` 就是 SQLite 认的占位符，所以这一支必须原样交回——顺手改一遍会把语句改坏。
func TestSQLite的Rebind原样交回(t *testing.T) {
	query := `INSERT INTO t (a, b) VALUES (?, ?) ON CONFLICT (a) DO UPDATE SET b = ?`
	if got := SQLite().Rebind(query); got != query {
		t.Fatalf("语句被改成了 %q", got)
	}
}

// SQLite 没有 CREATE SCHEMA，命名空间落成表名的一段：`"ns.表名"` 是一张**真的叫
// `ns.表名`** 的表，引号里的点不是限定符。
func TestSQLite的Qualify把命名空间折进表名(t *testing.T) {
	if got, want := SQLite().Qualify("dsh", "sessions"), `"dsh.sessions"`; got != want {
		t.Fatalf("拼出来是 %q，要的是 %q", got, want)
	}
}

// 分隔符必须是 [ValidName] 拼不出来的那个字符，否则两组不同的（命名空间, 表名）
// 会拼成同一个名字——两张本该互不相干的表塌成同一张，数据互相覆盖且没有任何征兆。
// 下划线正是拼得出来的那个，所以这里钉住点。
func TestSQLite的Qualify分得开会撞的那两组名字(t *testing.T) {
	if !ValidName("a") || !ValidName("x_y") || !ValidName("a_x") || !ValidName("y") {
		t.Fatal("这条用例的前提是这四个名字都合法")
	}
	left := SQLite().Qualify("a", "x_y")
	right := SQLite().Qualify("a_x", "y")
	if left == right {
		t.Fatalf("两组不同的名字拼成了同一个：%q", left)
	}
}

// 交回 nil 是「这个库的缺省事务本身就是那个快照」，不是「随便怎样都行」。
//
// 一次 SQLite 读事务从它第一句读起就钉住一个版本直到结束，比可重复读还强；
// 而 ReadOnly 那个位纯 Go 那个驱动收下之后并不拦写，声明了等于挂一块它不兑现的牌子。
func TestSQLite的读事务用库的缺省(t *testing.T) {
	if options := SQLite().ReadTxOptions(); options != nil {
		t.Fatalf("SQLite 该退回缺省事务，实际点名了 %+v", *options)
	}
}

// 两个参数的 MAX 是标量函数；同名的聚合 MAX 只收一个参数。
func TestSQLite的Greatest拼的是标量MAX(t *testing.T) {
	if got, want := SQLite().Greatest("next_seq", "?"), "MAX(next_seq, ?)"; got != want {
		t.Fatalf("拼出来是 %q，要的是 %q", got, want)
	}
}
