// 本文件的作用：物理布局那一层——布局版本号、库打开时那段建表与盖版本的序列、
// 以及单元表的物理表名怎么拼。单元自己那几张记录表由 backend.go 按描述符逐个建。
//
// 源: packages/storage/storage-sqlite/src/schema.ts:1-7

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ds-harness-go/storage"
)

// SchemaVersion 是**物理布局**的版本号，落在 storage_meta 那一行里。
//
// 源: packages/storage/storage-sqlite/src/schema.ts:14-20
//
// 它和每个单元自己的版本号（盖在 units 那一行里）是两回事：这一个说的是
// 「这几张表长什么样」，那一个说的是「这个单元里的记录是什么格式」。
// 只有表结构发生不兼容改动时才动它；盖着别的号一律**拒绝**——
// 这套格式还没发布过，没有迁移这一说。
const SchemaVersion = 1

// maxIdentifierLength 是 Postgres 的标识符字节上限。
//
// 新增: 超过它 Postgres **不报错，直接截断**。于是 u_a_很长的名字1 和
// u_a_很长的名字2 会塌成同一张物理表，两个单元的数据互相覆盖而没有任何征兆。
// SQLite 没有这个上限，所以抄来的那份形状里也没有这一查。
const maxIdentifierLength = 63

// layoutLockKey 是建表那一段用的咨询锁键。
//
// 新增: 取值本身没有含义，只要求「本包用同一个、别人不会撞上」。
// 咨询锁的键空间是整个数据库共享的一个 int64，所以这里用一个不像序号的常数，
// 而不是 1——1 是所有人写第一个咨询锁时都会写的那个数。
const layoutLockKey int64 = 0x64736800_73746f72 // "dsh\0stor"

// recordTableName 拼出一个单元表的物理表名。
//
// 源: packages/storage/storage-sqlite/src/schema.ts:110-120
//
// 两段名字都已经被 [storage.KVUnitDescriptor.Validate] 卡成
// `^[a-z][a-z0-9_]*$`，所以结果不用转义就能进 DDL 和语句文本。
//
// 新增: 长度超了就报错而不是让 Postgres 静默截断，理由见 [maxIdentifierLength]。
func recordTableName(unit, table string) (string, error) {
	name := "u_" + unit + "_" + table
	if len(name) > maxIdentifierLength {
		return "", &storage.Error{
			Code: storage.CodeMalformedMedium,
			Message: fmt.Sprintf(
				"单元 %q 的表 %q 拼出来的物理表名有 %d 字节，超过 Postgres 的 %d 字节上限；"+
					"再长下去两张不同的表会被截断成同一张",
				unit, table, len(name), maxIdentifierLength),
		}
	}
	return name, nil
}

// qualify 把一个 schema 和一个表名拼成带引号的限定标识符。
//
// 新增: 每一处都限定到 schema，不靠 search_path。连接池里每条连接各自带着
// 自己的会话状态，靠 search_path 就等于让「这条语句落在哪个 schema」
// 取决于这次抓到了哪条连接。
func qualify(schema, table string) string {
	return `"` + schema + `"."` + table + `"`
}

// ensureLayout 打开这份介质：建元数据表、查布局版本、建其余的表、给全新的介质盖上号。
//
// 源: packages/storage/storage-sqlite/src/schema.ts:52-108
//
// 整段包在一次事务里，进门先拿咨询锁：多机同时第一次打开同一份介质时，
// 两条 CREATE TABLE IF NOT EXISTS 撞在一起 Postgres 会报 duplicate_table，
// 而不是各建各的。锁随事务结束自动放掉，所以中途返回也不会漏放。
//
// 盖号放在最后一步，和抄来的那份形状同理：这一号是「布局齐了」的断言，
// 所以前面任何一步失败都必须让介质**保持没盖号的状态**，
// 下一次打开才会从头再来一遍。
func ensureLayout(ctx context.Context, db *sql.DB, schema string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres 存储：开事务失败：%w", err)
	}
	// 提交成功之后这次回滚是空操作；失败或提前返回时它才真正生效。
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, layoutLockKey); err != nil {
		return fmt.Errorf("postgres 存储：拿布局咨询锁失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+`"`+schema+`"`); err != nil {
		return fmt.Errorf("postgres 存储：建 schema %q 失败：%w", schema, err)
	}

	// 元数据表得先在场才读得到版本号——SQLite 那边 PRAGMA user_version 是天生就有的，
	// 这里要自己建一张。建一张空表不算动数据，所以「被拒绝的打开不碰介质」仍然成立。
	//
	// only_row 那个 CHECK 是把「单例」这件事写进表结构：没有它的话，
	// 多插一行就成了两个互相矛盾的版本号，而读的时候只会看见其中一个。
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+qualify(schema, "storage_meta")+` (
			only_row       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (only_row),
			schema_version INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("postgres 存储：建 storage_meta 失败：%w", err)
	}

	var onDisk int
	found := true
	row := tx.QueryRowContext(ctx, `SELECT schema_version FROM `+qualify(schema, "storage_meta"))
	if err := row.Scan(&onDisk); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("postgres 存储：读布局版本号失败：%w", err)
		}
		found = false
	}
	if found && onDisk != SchemaVersion {
		return &storage.Error{
			Code: storage.CodeVersionMismatch,
			Message: fmt.Sprintf(
				"schema %q 上的存储布局是版本 %d，和这个构建的 %d 不兼容",
				schema, onDisk, SchemaVersion),
		}
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+qualify(schema, "units")+` (
			name    TEXT PRIMARY KEY,
			version INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("postgres 存储：建 units 失败：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+qualify(schema, "unit_globals")+` (
			unit  TEXT PRIMARY KEY REFERENCES `+qualify(schema, "units")+`(name),
			value TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("postgres 存储：建 unit_globals 失败：%w", err)
	}

	if !found {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+qualify(schema, "storage_meta")+` (schema_version) VALUES ($1)`,
			SchemaVersion); err != nil {
			return fmt.Errorf("postgres 存储：盖布局版本号失败：%w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres 存储：提交布局事务失败：%w", err)
	}
	return nil
}
