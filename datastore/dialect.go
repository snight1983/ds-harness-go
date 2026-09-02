// 本文件的作用：语句里数据库之间会分歧的那几处，以及一个名字能不能拼进 SQL 标识符。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package datastore

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
)

// namePattern 是单元名、表名、流集名、命名空间共用的合法形状。
//
// 新增: 这些名字会被**拼进 SQL 文本**当标识符，不是绑定参数。所以它们必须先被卡成
// 一个不需要转义的形状。取的是几种数据库都当普通标识符看的那个交集。
//
// 记录键、流名、条目负载都不在这里——它们永远是绑定参数，可以是任意字符串。
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidName 判断一个名字能不能当标识符拼进语句。
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Dialect 收着一种关系库和别的关系库不一样的那几处。
//
// 新增: 加一种数据库是加一个实现，不是加一个包——这正是本包存在的理由。
// 接口刻意窄：只有那些「同一件事在两种库上写法不同」的地方进得来，
// 而「同一件事」的定义在本包别处，不在这里。
type Dialect interface {
	// Name 是这个方言的名字，只用来说话（错误信息、诊断）。
	Name() string

	// DefaultNamespace 是 [Config.Namespace] 留空时用的那个。
	DefaultNamespace() string

	// MaxIdentifierBytes 是标识符的字节上限，0 表示没有上限。
	//
	// 有上限的库（Postgres 是 63）超了**不报错，直接截断**，于是两个长名字不同的
	// 表会塌成同一张物理表——数据互相覆盖，没有任何征兆。所以这个数要问得出来。
	MaxIdentifierBytes() int

	// Rebind 把写着 `?` 的语句改成这个库认的占位符。
	//
	// 本包所有语句一律用 `?` 写，因为它是最不显眼的那一种；语句文本里没有字符串
	// 字面量，所以这次替换不会误伤——真要有的话，值应该是绑定参数才对。
	Rebind(query string) string

	// Qualify 把命名空间和表名拼成一个限定标识符。
	//
	// 每一处都限定到命名空间，不靠连接上的会话状态（Postgres 的 search_path）：
	// 连接池里每条连接各自带着自己的状态，靠它就等于让「这条语句落在哪儿」
	// 取决于这次抓到了哪条连接。
	Qualify(namespace, table string) string

	// EnsureNamespace 在建表之前把命名空间准备好；库里没有这个概念时什么也不做。
	EnsureNamespace(ctx context.Context, tx *sql.Tx, namespace string) error

	// LockLayout 在一次建表事务里把并发的第一次打开串起来。
	//
	// 多机同时第一次打开同一份介质时，两条 CREATE TABLE IF NOT EXISTS 撞在一起，
	// 有的库会报重复建表而不是安静地各建各的。锁随事务结束自动放掉，所以中途返回
	// 也不会漏放。单写者的库里这一步是空操作。
	LockLayout(ctx context.Context, tx *sql.Tx, key int64) error

	// ReadTxOptions 是读那一路要的事务选项。
	//
	// 它必须让「一次事务里几句读看到的是同一个快照」成立：本包交出去的 [Revision]
	// 承诺它标识的恰好是同一次读到的那些值，而读头、读条目、读令牌是三句语句。
	// Postgres 的缺省是读已提交，会让交出去的令牌配着另一份日志，所以那里必须明说。
	//
	// 交回 nil 表示**这个库的缺省事务本身就是那个快照**（SQLite 就是），不是
	// 「随便怎样都行」——退回缺省之前得先确认缺省够用。
	ReadTxOptions() *sql.TxOptions

	// Greatest 拼出「这两个里取大的那个」的表达式。
	//
	// Postgres 叫 GREATEST，SQLite 的标量版叫 MAX——同名的聚合函数在两边都存在
	// 且意思不同，所以这一处非分不可。
	Greatest(left, right string) string
}

// layoutLockKey 是建表那一段用的锁键。
//
// 新增: 取值本身没有含义，只要求「本包用同一个、别人不会撞上」。咨询锁的键空间是
// 整个数据库共享的一个 int64，所以这里用一个不像序号的常数，而不是 1——1 是所有人
// 写第一个咨询锁时都会写的那个数。
const layoutLockKey int64 = 0x64736800_64617461 // "dsh\0data"

// Postgres 是 Postgres 方言。
//
// 新增: 驱动不在本包里。用 lib/pq 还是 pgx 的 stdlib 包是部署期的选择，
// 由装配方 sql.Open 出来经 [Config.DB] 传进来。本包只认 database/sql。
func Postgres() Dialect { return postgresDialect{} }

type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgres" }

func (postgresDialect) DefaultNamespace() string { return "public" }

// MaxIdentifierBytes 是 Postgres 的 63 字节上限，超了静默截断。
func (postgresDialect) MaxIdentifierBytes() int { return 63 }

// Rebind 把第 n 个 `?` 换成 `$n`。
func (postgresDialect) Rebind(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)
	index := 0
	for _, char := range query {
		if char == '?' {
			index++
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(index))
			continue
		}
		out.WriteRune(char)
	}
	return out.String()
}

func (postgresDialect) Qualify(namespace, table string) string {
	return `"` + namespace + `"."` + table + `"`
}

func (postgresDialect) EnsureNamespace(ctx context.Context, tx *sql.Tx, namespace string) error {
	_, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+namespace+`"`)
	return err
}

func (postgresDialect) LockLayout(ctx context.Context, tx *sql.Tx, key int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key)
	return err
}

func (postgresDialect) ReadTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
}

func (postgresDialect) Greatest(left, right string) string {
	return "GREATEST(" + left + ", " + right + ")"
}

// SQLite 是 SQLite 方言。
//
// 新增: 驱动同样不在本包里（理由见 [Postgres]）。装配方 sql.Open 一个 SQLite 驱动
// 出来经 [Config.DB] 传进来，本包只认 database/sql。
//
// # DSN 上有两件事本包管不了
//
// 一是 **busy_timeout**。SQLite 只有一个写者，两条写事务撞在一起时后到的那条拿到
// SQLITE_BUSY。设了 busy_timeout，它会等着重试；没设，它当场失败。本包的建表那一段
// 正好是多机第一次打开时会撞的地方（见 [sqliteDialect.LockLayout]），所以这个值
// 该由装配方在 DSN 里设上，比如 `_pragma=busy_timeout(5000)`。
//
// 二是 **foreign_keys**。SQLite 默认不认外键，而本包的条目表靠一条外键挡住
// 「写进一条不存在的流」。挡不住也不会写错——那一步本包自己先查过流在不在——
// 但那道安全网没了，所以同样建议在 DSN 里 `_pragma=foreign_keys(1)`。
//
// 两件事都是**连接**上的状态，不是语句里的东西，所以它们进不了 [Dialect]：
// 一个连接池里每条连接各自带着自己的 pragma，本包没有「对每条新连接执行一句」的
// 抓手，硬做只会做出一个漏掉一部分连接的假保证。
func SQLite() Dialect { return sqliteDialect{} }

type sqliteDialect struct{}

func (sqliteDialect) Name() string { return "sqlite" }

// DefaultNamespace 借 SQLite 自己那个词：主库叫 main。
func (sqliteDialect) DefaultNamespace() string { return "main" }

// MaxIdentifierBytes 是 0：SQLite 不截断标识符，也没有长度上限。
func (sqliteDialect) MaxIdentifierBytes() int { return 0 }

// Rebind 原样交回：`?` 就是 SQLite 认的占位符。
func (sqliteDialect) Rebind(query string) string { return query }

// Qualify 把命名空间**前缀**进表名里，拼成一个带点的单一标识符。
//
// 新增: 这一处和 Postgres 分得最开。SQLite 没有 CREATE SCHEMA；它那个
// `库名.表名` 里的库名只能是 ATTACH 上来的名字，而 ATTACH 是**连接**上的状态——
// 连接池里每条连接各自带着自己的一份，靠它就等于让「这条语句落在哪儿」取决于
// 这次抓到了哪条连接。那正是 [Dialect.Qualify] 那句话拒绝的东西，所以这里不用它。
//
// 于是命名空间落成名字的一段：`"ns.表名"` 是一张**真的叫 `ns.表名`** 的表
// （引号里的点不是限定符，是名字里的一个字符）。同一个库文件里两个命名空间因此
// 各建各的表，[Config.Namespace] 那条「两份互不相干的介质」照旧成立。
//
// 分隔符取点而不是下划线，是因为命名空间和表名都合 [ValidName]（下划线在里面），
// 于是 `a` + `_` + `x_y` 和 `a_x` + `_` + `y` 会拼成同一个名字——两张本该互不相干的
// 表塌成同一张，数据互相覆盖且没有任何征兆，正是 [Medium.physical] 那道检查在防的事。
// 点进不了 [ValidName]，所以这一拼是可逆的。
func (sqliteDialect) Qualify(namespace, table string) string {
	return `"` + namespace + `.` + table + `"`
}

// EnsureNamespace 什么也不做：命名空间在 SQLite 上是表名的一段（见
// [sqliteDialect.Qualify]），没有可以事先建出来的东西。
func (sqliteDialect) EnsureNamespace(context.Context, *sql.Tx, string) error { return nil }

// LockLayout 什么也不做：SQLite 只有一个写者，那把写锁就是布局锁。
//
// 新增: 建表那一段的第一句就是 CREATE TABLE，所以两条并发的第一次打开在那儿
// 就分出了先后——一条拿到写锁，另一条拿到 SQLITE_BUSY。设了 busy_timeout 的话
// 后者会等前者提交再重试，那时表已经在了、元数据行也已经在了，于是它读到而不是
// 再插一遍。没设的话它当场失败，也仍旧不会写坏什么。见 [SQLite] 那段 DSN 说明。
func (sqliteDialect) LockLayout(context.Context, *sql.Tx, int64) error { return nil }

// ReadTxOptions 交回 nil：SQLite 缺省那把事务本身就是快照。
//
// 新增: 一次 SQLite 读事务从它第一句读起就钉住一个版本，直到结束——不管是回滚日志
// 模式下的 SHARED 锁还是 WAL 模式下的快照，中途别人提交的东西这次事务都看不见。
// 那比 [Dialect.ReadTxOptions] 要的可重复读还强，所以这里不必再点名一个隔离级别。
//
// 也**不**声明 ReadOnly。纯 Go 那个驱动收下这个位之后并不拦写，声明了等于挂一块
// 它不兑现的牌子；本包的读那一路本来就一句写都没有，牌子省了也没少什么。
func (sqliteDialect) ReadTxOptions() *sql.TxOptions { return nil }

// Greatest 用 SQLite 的标量 MAX——同名的聚合 MAX 只收一个参数，两个参数的这一支
// 是标量函数，正是要的那个。
func (sqliteDialect) Greatest(left, right string) string {
	return "MAX(" + left + ", " + right + ")"
}
