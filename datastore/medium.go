// 本文件的作用：一份介质本身——连接池、命名空间、物理布局、实例标识，
// 以及所有单元共用的那几件事（拼表名、开事务、认领单元名）。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// LayoutVersion 是**物理布局**的版本号，盖在 datastore_meta 那一行里。
//
// 新增: 它说的是「本包建的那几张表长什么样」。每个单元自己还有一个版本号
// （盖在 datastore_units 那一行里），说的是「这个单元里的值是什么格式」——
// 那一个由使用方定，本包只负责存下来、对不上就拒。
//
// 盖着别的号一律拒绝，没有迁移这一说：这套布局还没发布过。
const LayoutVersion = 1

// 本包建的三张公共表。单元自己的表由 [Medium.OpenRecords] / [Medium.OpenLog] 按需建。
const (
	// metaTable 是这份介质自己的那一行：布局版本号和实例标识。
	metaTable = "datastore_meta"
	// unitsTable 是单元登记处：一个单元一行，记着它是哪种形态、第几版。
	unitsTable = "datastore_units"
	// singletonsTable 是记录集那个可选的单例槽。
	singletonsTable = "datastore_singletons"
)

// 两种形态在 datastore_units.kind 那一列里的取值。
const (
	kindRecords = "records"
	kindLog     = "log"
)

// PoolConfig 是连接池的那几个数。
//
// 新增: 它们必须问得出来。database/sql 的缺省是「连接数不设上限、空闲只留 2 条、
// 永不过期」——高峰期开出去几百条连接、回落之后立刻关到只剩 2 条，然后下一波
// 请求再从头建。中间还夹着一个更难查的：数据库那侧或者中间的连接跟踪设备
// 单方面掐掉一条闲连接时，本侧不知道，直到某次拿到它的调用报一个莫名其妙的
// 连接错误。ConnMaxLifetime 就是为这个存在的。
//
// 留零表示照 database/sql 的缺省来，本包不替装配方猜。
type PoolConfig struct {
	// MaxOpenConns 是同时开着的连接数上限，0 表示不限。
	MaxOpenConns int
	// MaxIdleConns 是留在池子里的空闲连接数，0 表示照缺省（2 条）。
	MaxIdleConns int
	// ConnMaxLifetime 是一条连接从建立起能活多久，0 表示不限。
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime 是一条连接闲置多久之后被关掉，0 表示不限。
	ConnMaxIdleTime time.Duration
}

// Config 是打开一份介质要的东西。
type Config struct {
	// DB 是已经建好的连接池。
	//
	// 新增: 驱动的选择、DSN 的来源、凭据怎么拿，都是部署期的决定，不是本包的。
	// 本包只认 database/sql。
	//
	// 一份介质**独占**一个连接池：[Medium.Close] 会把它关掉，[Pool] 那几个数
	// 也是设在它身上的。想在同一个库里开两份介质（两个命名空间），给两个池子。
	DB *sql.DB

	// Dialect 是后面挂的那种数据库。留空按 [Postgres] 算。
	Dialect Dialect

	// Namespace 是这份介质落在哪个命名空间里。留空按方言的缺省算。
	//
	// 同一个库里的两个命名空间就是两份互不相干的介质：各自一份布局、
	// 各自一个实例标识、各自一套单元。
	Namespace string

	// Pool 是连接池的那几个数，见 [PoolConfig]。
	Pool PoolConfig
}

// Medium 是一份介质。
//
// 除 [Medium.Close] 之外的方法可以被多个 goroutine 同时调用。
type Medium struct {
	db        *sql.DB
	dialect   Dialect
	namespace string

	// instance 是这份介质的实例标识，第一次打开时盖上，此后不变。
	// 它拌进每一个 [Revision] 里，见 doc.go。
	instance string

	// mu 只护着下面两个字段——它们是「这个进程里的账」，不是介质上的状态。
	mu sync.Mutex
	// opened 是这个进程里正开着的单元名。
	opened map[string]struct{}
	closed bool
}

// Open 打开一份介质：把连接池调好、建齐布局、取回实例标识。
//
// 新增: 建布局整段包在一次事务里，进门先拿咨询锁——多机同时第一次打开同一份介质时，
// 两条 CREATE TABLE IF NOT EXISTS 撞在一起，有的库会报重复建表而不是各建各的。
//
// 盖号和实例标识是同一行、同一次插入：那一行的存在就是「布局齐了」这条断言，
// 所以前面任何一步失败都必须让介质**保持没有那一行的状态**，下一次打开才会从头
// 再来一遍。实例标识也因此和布局同生共死——它一旦被别人读到过就不许再变，否则那些
// 手里攥着旧令牌的调用方会以为日志变过了。
//
// 版本对不上时**一个字都不改**：一次被拒的打开要是顺手动了介质，「升级失败」
// 就会连带把旧版本的数据毁掉。
func Open(ctx context.Context, config Config) (*Medium, error) {
	if config.DB == nil {
		return nil, errors.New("datastore: Config.DB 是空的")
	}
	dialect := config.Dialect
	if dialect == nil {
		dialect = Postgres()
	}
	namespace := config.Namespace
	if namespace == "" {
		namespace = dialect.DefaultNamespace()
	}
	if !ValidName(namespace) {
		return nil, failf(ErrMalformedName,
			"命名空间 %q 必须是小写字母开头，之后只能是小写字母、数字或下划线", namespace)
	}
	if limit := dialect.MaxIdentifierBytes(); limit > 0 && len(namespace) > limit {
		return nil, failf(ErrMalformedName,
			"命名空间 %q 有 %d 字节，超过 %s 的 %d 字节上限；再长下去两个不同的命名空间会被截断成同一个",
			namespace, len(namespace), dialect.Name(), limit)
	}

	applyPool(config.DB, config.Pool)

	medium := &Medium{
		db:        config.DB,
		dialect:   dialect,
		namespace: namespace,
		opened:    make(map[string]struct{}),
	}
	instance, err := medium.ensureLayout(ctx)
	if err != nil {
		return nil, err
	}
	medium.instance = instance
	return medium, nil
}

func applyPool(db *sql.DB, pool PoolConfig) {
	if pool.MaxOpenConns > 0 {
		db.SetMaxOpenConns(pool.MaxOpenConns)
	}
	if pool.MaxIdleConns > 0 {
		db.SetMaxIdleConns(pool.MaxIdleConns)
	}
	if pool.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(pool.ConnMaxLifetime)
	}
	if pool.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(pool.ConnMaxIdleTime)
	}
}

// InstanceID 是这份介质的实例标识。
func (m *Medium) InstanceID() string { return m.instance }

// Namespace 是这份介质所在的命名空间。
func (m *Medium) Namespace() string { return m.namespace }

// DialectName 是后面挂的那种数据库的名字，只用来说话。
func (m *Medium) DialectName() string { return m.dialect.Name() }

// Close 关掉这份介质和它的连接池。重复调用是空操作。
//
// 新增: 它不去问单元关没关。还开着的单元此后每一次调用都会撞上 [ErrClosed]，
// 这比在这里拦住关闭要好——关不掉的介质意味着连接池泄漏，而那是没法在别处补救的。
func (m *Medium) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.opened = nil
	m.mu.Unlock()

	if err := m.db.Close(); err != nil {
		return fmt.Errorf("datastore: 关连接池失败：%w", err)
	}
	return nil
}

// claimUnit 认领一个单元名；已经开着就报 [ErrAlreadyOpen]。
//
// 新增: 这一条在本包里，不在适配层。两个句柄各自持有一份状态，后写的把先写的
// 覆盖掉，而两次写都「成功」了——这件事和落在上面的是哪种业务无关。
func (m *Medium) claimUnit(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return failf(ErrClosed, "介质已经关掉了")
	}
	if _, taken := m.opened[name]; taken {
		return failf(ErrAlreadyOpen, "单元 %q", name)
	}
	m.opened[name] = struct{}{}
	return nil
}

// releaseUnit 放掉一个认领过的单元名。
func (m *Medium) releaseUnit(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.opened != nil {
		delete(m.opened, name)
	}
}

// qualify 把一个表名限定到这份介质的命名空间。
func (m *Medium) qualify(table string) string {
	return m.dialect.Qualify(m.namespace, table)
}

// physical 校验一个拼出来的物理表名进不进得了 DDL。
//
// 新增: 长度超了就报错，不让库静默截断——两个长名字不同的表塌成同一张物理表之后，
// 数据互相覆盖且没有任何征兆。
func (m *Medium) physical(name string) (string, error) {
	limit := m.dialect.MaxIdentifierBytes()
	if limit > 0 && len(name) > limit {
		return "", failf(ErrMalformedName,
			"物理表名 %q 有 %d 字节，超过 %s 的 %d 字节上限；再长下去两张不同的表会被截断成同一张",
			name, len(name), m.dialect.Name(), limit)
	}
	return name, nil
}

// querier 是 [sql.DB] 和 [sql.Tx] 都满足的那一部分。
//
// 新增: 本包每一条语句都写着 `?`，出门前统一过一遍 [Dialect.Rebind]，所以这三个
// 方法一律走下面三个包装，不直接调 database/sql——漏掉一处的表现是「占位符没换」，
// 而那要到运行到那一句时才看得见。
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (m *Medium) exec(ctx context.Context, q querier, query string, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, m.dialect.Rebind(query), args...)
}

func (m *Medium) query(ctx context.Context, q querier, query string, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, m.dialect.Rebind(query), args...)
}

func (m *Medium) queryRow(ctx context.Context, q querier, query string, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, m.dialect.Rebind(query), args...)
}

// begin 开一次事务。options 为 nil 时按库的缺省来。
func (m *Medium) begin(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, failf(ErrClosed, "介质已经关掉了")
	}
	tx, err := m.db.BeginTx(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("datastore: 开事务失败：%w", err)
	}
	return tx, nil
}

// inTx 把一段活儿放进一次事务里跑；返回错误就回滚。
//
// 新增: 提交成功之后那次 Rollback 是空操作，所以中途 return 也不会漏放——
// 这一处收在这里，是因为「忘了回滚」在每一条写路径上都是同一个写法、同一个后果。
func (m *Medium) inTx(ctx context.Context, options *sql.TxOptions, body func(tx *sql.Tx) error) error {
	tx, err := m.begin(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := body(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datastore: 提交事务失败：%w", err)
	}
	return nil
}

// inReadTx 和 [Medium.inTx] 一样，但用的是方言给的只读隔离级别。
//
// 新增: 读那一路必须一次事务看同一个快照——本包交出去的 [Revision] 承诺它标识的
// 恰好是同一次读到的那些值，而读头、读条目、读令牌是三句语句。
func (m *Medium) inReadTx(ctx context.Context, body func(tx *sql.Tx) error) error {
	return m.inTx(ctx, m.dialect.ReadTxOptions(), body)
}

// ensureLayout 建齐公共布局，交出这份介质的实例标识。
func (m *Medium) ensureLayout(ctx context.Context) (string, error) {
	var instance string
	err := m.inTx(ctx, nil, func(tx *sql.Tx) error {
		if err := m.dialect.LockLayout(ctx, tx, layoutLockKey); err != nil {
			return fmt.Errorf("datastore: 拿布局锁失败：%w", err)
		}
		if err := m.dialect.EnsureNamespace(ctx, tx, m.namespace); err != nil {
			return fmt.Errorf("datastore: 准备命名空间 %q 失败：%w", m.namespace, err)
		}

		// 元数据表得先在场才读得到版本号。建一张空表不算动数据，
		// 所以「被拒绝的打开不碰介质」仍然成立。
		//
		// only_row 那个 CHECK 是把「单例」这件事写进表结构：没有它的话，多插一行
		// 就成了两份互相矛盾的元数据，而读的时候只会看见其中一份。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(metaTable)+` (
				only_row       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (only_row),
				layout_version INTEGER NOT NULL,
				instance_id    TEXT    NOT NULL
			)`); err != nil {
			return fmt.Errorf("datastore: 建 %s 失败：%w", metaTable, err)
		}

		var onDisk int
		found := true
		row := m.queryRow(ctx, tx,
			`SELECT layout_version, instance_id FROM `+m.qualify(metaTable))
		if err := row.Scan(&onDisk, &instance); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("datastore: 读布局版本号失败：%w", err)
			}
			found = false
		}
		if found && onDisk != LayoutVersion {
			return failf(ErrVersionMismatch,
				"命名空间 %q 上的布局是版本 %d，这个构建认的是 %d",
				m.namespace, onDisk, LayoutVersion)
		}

		// 单元登记处：kind 那一列让「同一个名字先当记录集开、后当日志集开」
		// 当场撞出来，而不是撞在一张形状对不上的物理表上。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(unitsTable)+` (
				name    TEXT PRIMARY KEY,
				kind    TEXT    NOT NULL,
				version INTEGER NOT NULL
			)`); err != nil {
			return fmt.Errorf("datastore: 建 %s 失败：%w", unitsTable, err)
		}
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(singletonsTable)+` (
				unit  TEXT PRIMARY KEY REFERENCES `+m.qualify(unitsTable)+`(name),
				value TEXT NOT NULL
			)`); err != nil {
			return fmt.Errorf("datastore: 建 %s 失败：%w", singletonsTable, err)
		}

		if !found {
			instance = uuid.NewString()
			if _, err := m.exec(ctx, tx,
				`INSERT INTO `+m.qualify(metaTable)+` (layout_version, instance_id) VALUES (?, ?)`,
				LayoutVersion, instance); err != nil {
				return fmt.Errorf("datastore: 盖布局版本号失败：%w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return instance, nil
}

// registerUnit 在登记处认一个单元：没有就记上，有就核形态和版本。
//
// 新增: 版本对不上只拒绝、不改任何东西，理由同 [Open]。
func (m *Medium) registerUnit(ctx context.Context, tx *sql.Tx, name, kind string, version int) error {
	var onDiskKind string
	var onDiskVersion int
	row := m.queryRow(ctx, tx,
		`SELECT kind, version FROM `+m.qualify(unitsTable)+` WHERE name = ?`, name)
	switch err := row.Scan(&onDiskKind, &onDiskVersion); {
	case err == nil:
		if onDiskKind != kind {
			return failf(ErrVersionMismatch,
				"单元 %q 在这份介质里是%s，这次要按%s打开", name, shapeWord(onDiskKind), shapeWord(kind))
		}
		if onDiskVersion != version {
			return failf(ErrVersionMismatch,
				"单元 %q 在这份介质里是版本 %d，这次要开的是版本 %d", name, onDiskVersion, version)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := m.exec(ctx, tx,
			`INSERT INTO `+m.qualify(unitsTable)+` (name, kind, version) VALUES (?, ?, ?)`,
			name, kind, version); err != nil {
			return fmt.Errorf("datastore: 登记单元 %q 失败：%w", name, err)
		}
		return nil
	default:
		return fmt.Errorf("datastore: 读单元 %q 的登记失败：%w", name, err)
	}
}

// shapeWord 把 kind 那一列的取值说成人话。
func shapeWord(kind string) string {
	switch kind {
	case kindRecords:
		return "记录集"
	case kindLog:
		return "日志集"
	default:
		return kind
	}
}

// checkUnitName 校验一个单元名。
func checkUnitName(name string) error {
	if !ValidName(name) {
		return failf(ErrMalformedName,
			"单元名 %q 必须是小写字母开头，之后只能是小写字母、数字或下划线", name)
	}
	return nil
}
