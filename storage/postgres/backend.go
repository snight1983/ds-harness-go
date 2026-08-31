// 本文件的作用：后端本体——它拥有的那份介质、已打开单元那张表，
// 以及把一个描述符落成介质上真实存在的几张表的那段。
//
// 源: packages/storage/storage-sqlite/src/index.ts:50-150

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"ds-harness-go/storage"
)

// Config 是建一个后端要的东西。
//
// 源: packages/storage/storage-sqlite/src/index.ts:23-48
type Config struct {
	// DB 是已经建好的连接池，**归后端所有**：[Backend.Close] 会把它关掉。
	//
	// 新增: DSH 那边后端自己 new 一个 DatabaseSync，因为 SQLite 的驱动就在
	// node 标准库里。Postgres 没有这回事，驱动是部署期的选择（lib/pq、
	// pgx 的 stdlib 包，都行），所以由装配方 sql.Open 出来再传进来。
	// 「归后端所有」是从 [storage.Backend] 那条契约来的：一个后端拥有一份介质，
	// 关后端就得把介质释放掉，否则连接池会随着后端一个个泄漏。
	DB *sql.DB

	// Schema 是这份介质落在哪个 Postgres schema 里，留空则是 "public"。
	//
	// 它必须满足 [storage.ValidUnitName]——本包所有语句都把标识符限定到它，
	// 而限定串是拼进 SQL 文本的，不是绑定参数。
	//
	// 新增: 同一个库里的两个 schema 就是两份互不相干的介质。
	// 抄来的那份形状里对应的是「两个不同的数据库文件」。
	Schema string
}

// defaultSchema 是 [Config.Schema] 留空时用的那个。
const defaultSchema = "public"

// Backend 是一个开在某份介质上的 Postgres 键值后端。
//
// 源: packages/storage/storage-sqlite/src/index.ts:50-55
//
// 它同时也是自己的键值操作组（见 [Backend.KV]），和内存参考后端一个做法：
// 这一层没有第二种数据形态要分开，多一个类型只是多一次转发。
type Backend struct {
	db     *sql.DB
	schema string

	mutex sync.Mutex
	// opened 是当前开着的单元，按名字索引；在场与否就是那道「不许重复打开」的闸。
	opened map[string]*kvUnit
	closed bool
}

// 编译期确认这个后端确实提供键值形态。写反了的话调用方会在 [storage.KV]
// 那里拿到 false，而那看起来像是「这个后端根本没注册上」。
var _ storage.KVProvider = (*Backend)(nil)

// Open 在一份介质上打开后端：建好三张元数据表，验一遍布局版本号。
//
// 源: packages/storage/storage-sqlite/src/index.ts:64-73
//
// 新增: 抄来的那份形状把打开做成一个存起来的 Promise，每次操作再 await 一遍，
// 于是「库打不开」这件事要等到第一次读写才浮出来。Go 里没有理由这么绕：
// 建后端本来就可以返回 error，打不开就是打不开，当场说。
func Open(ctx context.Context, config Config) (*Backend, error) {
	if config.DB == nil {
		return nil, &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: "postgres 存储：Config.DB 是必需的，没有连接池就没有介质",
		}
	}
	schema := config.Schema
	if schema == "" {
		schema = defaultSchema
	}
	if !storage.ValidUnitName(schema) {
		return nil, &storage.Error{
			Code: storage.CodeMalformedMedium,
			Message: fmt.Sprintf(
				"postgres 存储：schema 名 %q 不合法：必须是小写字母开头，之后只能是小写字母、数字或下划线",
				schema),
		}
	}
	if err := ensureLayout(ctx, config.DB, schema); err != nil {
		return nil, err
	}
	return &Backend{db: config.DB, schema: schema, opened: map[string]*kvUnit{}}, nil
}

// KV 让这个后端满足 [storage.KVProvider]。操作组就是后端自己。
//
// 源: packages/storage/storage-sqlite/src/index.ts:56-57
func (b *Backend) KV() storage.KVFacet { return b }

// Close 关掉所有还开着的单元，然后释放连接池。**幂等**。
//
// 源: packages/storage/storage-sqlite/src/index.ts:125-149
func (b *Backend) Close(context.Context) error {
	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return nil
	}
	b.closed = true
	units := make([]*kvUnit, 0, len(b.opened))
	for _, unit := range b.opened {
		units = append(units, unit)
	}
	b.opened = map[string]*kvUnit{}
	b.mutex.Unlock()

	for _, unit := range units {
		unit.markClosed()
	}
	if err := b.db.Close(); err != nil {
		return fmt.Errorf("postgres 存储：关连接池失败：%w", err)
	}
	return nil
}

// Open 打开一个单元：验描述符、盖/查这个单元自己的版本号、建它的记录表。
//
// 源: packages/storage/storage-sqlite/src/index.ts:75-123
func (b *Backend) Open(ctx context.Context, descriptor storage.KVUnitDescriptor) (storage.KVUnit, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	physical, err := physicalNames(descriptor)
	if err != nil {
		return nil, err
	}

	// 先把名字占住再去动库：占位和落库要是分两步做，两个并发的同名打开
	// 会双双越过这道闸，各自拿到一个句柄，然后互相覆盖对方的写。
	b.mutex.Lock()
	if b.closed {
		b.mutex.Unlock()
		return nil, &storage.Error{Code: storage.CodeClosed, Message: "postgres 存储：后端已经关闭"}
	}
	if _, already := b.opened[descriptor.Name]; already {
		b.mutex.Unlock()
		return nil, &storage.Error{
			Code: storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 已经打开了，没关就再开一次是调用方的 bug",
				descriptor.Name),
		}
	}
	unit := &kvUnit{backend: b, descriptor: descriptor, physical: physical}
	b.opened[descriptor.Name] = unit
	b.mutex.Unlock()

	if err := b.materialize(ctx, descriptor, physical); err != nil {
		// 落库失败就把名字让出来，否则这个单元名在本进程里永远开不起来了。
		b.mutex.Lock()
		if b.opened[descriptor.Name] == unit {
			delete(b.opened, descriptor.Name)
		}
		b.mutex.Unlock()
		return nil, err
	}
	return unit, nil
}

// physicalNames 把描述符里每张表的物理表名先算出来。
//
// 单独算一遍是为了让长度那一查发生在**动库之前**：算到一半才发现第三张表名
// 太长的话，前两张已经建出来了。
func physicalNames(descriptor storage.KVUnitDescriptor) (map[string]string, error) {
	physical := make(map[string]string, len(descriptor.Tables))
	for _, table := range descriptor.Tables {
		name, err := recordTableName(descriptor.Name, table)
		if err != nil {
			return nil, err
		}
		physical[table] = name
	}
	return physical, nil
}

// materialize 在介质上把这个单元真正建出来：版本号那一行，加上每张记录表。
//
// 源: packages/storage/storage-sqlite/src/index.ts:98-123
//
// 和 [ensureLayout] 一样包在一次带咨询锁的事务里，理由也一样：多机同时第一次
// 打开同一个单元时，两条 INSERT 会撞主键、两条 CREATE TABLE 会撞 duplicate_table。
func (b *Backend) materialize(ctx context.Context, descriptor storage.KVUnitDescriptor, physical map[string]string) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres 存储：为单元 %q 开事务失败：%w", descriptor.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return fmt.Errorf("postgres 存储：为单元 %q 拿咨询锁失败：%w", descriptor.Name, err)
	}

	var stamped int
	row := tx.QueryRowContext(ctx,
		`SELECT version FROM `+qualify(b.schema, "units")+` WHERE name = $1`, descriptor.Name)
	switch err := row.Scan(&stamped); {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+qualify(b.schema, "units")+` (name, version) VALUES ($1, $2)`,
			descriptor.Name, descriptor.Version); err != nil {
			return fmt.Errorf("postgres 存储：给单元 %q 盖版本号失败：%w", descriptor.Name, err)
		}
	case err != nil:
		return fmt.Errorf("postgres 存储：读单元 %q 的版本号失败：%w", descriptor.Name, err)
	case stamped != descriptor.Version:
		// 这里**只拒绝**，一个字都不改：一次被拒的打开要是顺手动了介质，
		// 「升级失败」就会连带把旧版本的数据毁掉，而调用方拿到的只是一个版本不符。
		return &storage.Error{
			Code: storage.CodeVersionMismatch,
			Message: fmt.Sprintf(
				"单元 %q 介质上盖的是版本 %d，这次要开的是 %d",
				descriptor.Name, stamped, descriptor.Version),
		}
	}

	for _, table := range descriptor.Tables {
		// 两段名字都过了 [storage.ValidUnitName]，长度也查过了，所以这个标识符
		// 直接拼进 DDL 是安全的。记录键不在这里，它永远是绑定参数。
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS `+qualify(b.schema, physical[table])+` (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`); err != nil {
			return fmt.Errorf("postgres 存储：建单元 %q 的表 %q 失败：%w",
				descriptor.Name, table, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres 存储：提交单元 %q 的建表事务失败：%w", descriptor.Name, err)
	}
	return nil
}

// release 把一个单元从「已打开」表里摘掉，摘掉之后同名单元才重新开得起来。
func (b *Backend) release(unit *kvUnit) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.opened[unit.descriptor.Name] == unit {
		delete(b.opened, unit.descriptor.Name)
	}
}
