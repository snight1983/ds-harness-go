// 本文件的作用：两种通用形态里的第一种——记录集：若干张「键 → 一段不透明 JSON」
// 的表，外加一个可选的单例槽。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// RecordSpec 是一个记录集的静态形状。
type RecordSpec struct {
	// Name 是单元名，必须满足 [ValidName]。它会被拼进物理表名。
	Name string
	// Version 是这个单元里的值的格式版本，第一次打开时盖到介质上。
	//
	// 本包不解释它，只负责「盖上去的和这次要开的一样」。
	Version int
	// Tables 是这个单元有哪几张表，每一个都必须满足 [ValidName]。
	Tables []string
	// Singleton 表示这个单元带一个单例槽。
	Singleton bool
}

func (s RecordSpec) validate() error {
	if err := checkUnitName(s.Name); err != nil {
		return err
	}
	if s.Version < 0 {
		return failf(ErrMalformedName, "单元 %q 的版本号是 %d，不能是负数", s.Name, s.Version)
	}
	seen := make(map[string]struct{}, len(s.Tables))
	for _, table := range s.Tables {
		if !ValidName(table) {
			return failf(ErrMalformedName,
				"单元 %q 的表名 %q 必须是小写字母开头，之后只能是小写字母、数字或下划线",
				s.Name, table)
		}
		if _, duplicate := seen[table]; duplicate {
			// 重名的表在快照里会塌成一张，而声明它的人以为有两张。
			return failf(ErrMalformedName, "单元 %q 里的表名 %q 重复了", s.Name, table)
		}
		seen[table] = struct{}{}
	}
	return nil
}

// RecordSnapshot 是一个记录集当前的完整内容。
type RecordSnapshot struct {
	// Tables 是每张表的记录，按表名索引。声明过但一条记录都没有的表，这里是一个
	// **空 map 而不是缺席**——缺席和空在调用方那里会走不同的分支。
	Tables map[string]map[string]json.RawMessage
	// Singleton 是单例槽。没声明过、或者声明了但从没写过，都是 nil。
	Singleton json.RawMessage
}

// RecordUnit 是一个已打开的记录集。
//
// 值对本包来说是**不透明的 JSON**：没有 schema，没有任何领域含义。
//
// 本类型不负责把并发的写串起来——写的顺序是调用方的事。它只保证每一次单独的调用
// 在介质上是原子的，以及调用返回之后那次写是持久的。
type RecordUnit struct {
	medium *Medium
	spec   RecordSpec
	// physical 把逻辑表名映射到物理表名。「这张表声明过没有」一律查它，
	// 不遍历 spec.Tables——两者同源，不会分叉，而查 map 是 O(1)。
	physical map[string]string

	mutex  sync.Mutex
	closed bool
}

// recordTableName 拼一张记录表的物理表名。
//
// 新增: 前缀分开两种形态（记录集 r_、日志集 l_），这样一个单元名底下的两种形态
// 不会撞表名——虽然登记处已经拦住了同名换形态，但物理层不该依赖那一层的正确性。
func recordTableName(unit, table string) string { return "r_" + unit + "_" + table }

// OpenRecords 打开一个记录集，介质上还没有它的痕迹时就建出来。
//
// 同一个单元名没关就开第二次返回 [ErrAlreadyOpen]；介质上盖着的版本号或形态对不上
// 返回 [ErrVersionMismatch]，且**一个字都不改**。
func (m *Medium) OpenRecords(ctx context.Context, spec RecordSpec) (*RecordUnit, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}

	// 物理表名的长度必须在动库之前查完：查它要的全部信息这里都有，而一旦开始建表，
	// 中途因为第七张表名太长而失败会留下六张建好的表。
	physical := make(map[string]string, len(spec.Tables))
	for _, table := range spec.Tables {
		name, err := m.physical(recordTableName(spec.Name, table))
		if err != nil {
			return nil, err
		}
		physical[table] = name
	}

	if err := m.claimUnit(spec.Name); err != nil {
		return nil, err
	}
	err := m.inTx(ctx, nil, func(tx *sql.Tx) error {
		if err := m.dialect.LockLayout(ctx, tx, layoutLockKey); err != nil {
			return fmt.Errorf("datastore: 拿布局锁失败：%w", err)
		}
		if err := m.registerUnit(ctx, tx, spec.Name, kindRecords, spec.Version); err != nil {
			return err
		}
		for _, table := range spec.Tables {
			// 键那一列是 TEXT 主键，值那一列是 TEXT 不是 jsonb：jsonb 拒收 U+0000
			// 那个码位，而一段合法的 JSON 字符串里完全可以有它。代价是库不替我们
			// 验 JSON，所以读的时候本包自己验（见 [RecordUnit.Snapshot]）。
			if _, err := m.exec(ctx, tx, `
				CREATE TABLE IF NOT EXISTS `+m.qualify(physical[table])+` (
					key   TEXT PRIMARY KEY,
					value TEXT NOT NULL
				)`); err != nil {
				return fmt.Errorf("datastore: 建单元 %q 的表 %q 失败：%w", spec.Name, table, err)
			}
		}
		return nil
	})
	if err != nil {
		m.releaseUnit(spec.Name)
		return nil, err
	}

	return &RecordUnit{medium: m, spec: spec, physical: physical}, nil
}

// Name 是这个单元的名字。
func (u *RecordUnit) Name() string { return u.spec.Name }

func (u *RecordUnit) errClosed() error {
	return failf(ErrClosed, "记录集 %q 已经关闭", u.spec.Name)
}

// Snapshot 读出这个单元当前的完整内容。
//
// 新增: 整个快照落在一次只读事务里。分开读的话，几张表之间可以夹进别人的写，
// 于是交出去的「快照」是一个从来没有在介质上同时存在过的状态。
func (u *RecordUnit) Snapshot(ctx context.Context) (RecordSnapshot, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return RecordSnapshot{}, u.errClosed()
	}

	snapshot := RecordSnapshot{Tables: make(map[string]map[string]json.RawMessage, len(u.spec.Tables))}
	err := u.medium.inReadTx(ctx, func(tx *sql.Tx) error {
		for _, table := range u.spec.Tables {
			// 先建成空 map 再去读：声明过而一条记录都没有的表，契约要求它
			// **在场且为空**。缺席会让「这张表还没建出来」和「这张表是空的」
			// 长得一模一样。
			records := map[string]json.RawMessage{}
			snapshot.Tables[table] = records
			if err := u.scanTable(ctx, tx, table, records); err != nil {
				return err
			}
		}
		single, err := u.loadSingleton(ctx, tx)
		if err != nil {
			return err
		}
		snapshot.Singleton = single
		return nil
	})
	if err != nil {
		return RecordSnapshot{}, err
	}
	return snapshot, nil
}

func (u *RecordUnit) scanTable(
	ctx context.Context, tx *sql.Tx, table string, records map[string]json.RawMessage,
) error {
	rows, err := u.medium.query(ctx, tx, `SELECT key, value FROM `+u.medium.qualify(u.physical[table]))
	if err != nil {
		return fmt.Errorf("datastore: 读单元 %q 的表 %q 失败：%w", u.spec.Name, table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("datastore: 读单元 %q 的表 %q 的一行失败：%w", u.spec.Name, table, err)
		}
		// 值那一列是 TEXT，库不替我们验 JSON。不验的话一段坏文本会原样变成
		// json.RawMessage 交出去，然后在某个离这里很远的 Unmarshal 处炸掉。
		if !json.Valid([]byte(value)) {
			return failf(ErrMalformedMedium,
				"单元 %q 的表 %q 里键 %q 的值不是合法 JSON", u.spec.Name, table, key)
		}
		records[key] = json.RawMessage(value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("datastore: 遍历单元 %q 的表 %q 失败：%w", u.spec.Name, table, err)
	}
	return nil
}

func (u *RecordUnit) loadSingleton(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
	if !u.spec.Singleton {
		return nil, nil
	}
	var value string
	row := u.medium.queryRow(ctx, tx,
		`SELECT value FROM `+u.medium.qualify(singletonsTable)+` WHERE unit = ?`, u.spec.Name)
	switch err := row.Scan(&value); {
	case errors.Is(err, sql.ErrNoRows):
		// 声明了槽但一次都没写过——全新单元的正常状态，不是介质坏了。
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("datastore: 读单元 %q 的单例槽失败：%w", u.spec.Name, err)
	}
	if !json.Valid([]byte(value)) {
		return nil, failf(ErrMalformedMedium, "单元 %q 的单例槽里的值不是合法 JSON", u.spec.Name)
	}
	return json.RawMessage(value), nil
}

// Put 写一条记录，同键覆盖。
//
// key 可以是任意字符串：它永远走绑定参数，不进语句文本。
func (u *RecordUnit) Put(ctx context.Context, table, key string, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return u.errClosed()
	}

	physical, declared := u.physical[table]
	if !declared {
		return failf(ErrMalformedName, "单元 %q 没有声明过表 %q", u.spec.Name, table)
	}
	if !json.Valid(value) {
		return failf(ErrMalformedName, "单元 %q 的表 %q 里键 %q 的值不是合法 JSON",
			u.spec.Name, table, key)
	}

	if _, err := u.medium.exec(ctx, u.medium.db,
		`INSERT INTO `+u.medium.qualify(physical)+` (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, string(value)); err != nil {
		return fmt.Errorf("datastore: 写单元 %q 的表 %q 的键 %q 失败：%w",
			u.spec.Name, table, key, err)
	}
	return nil
}

// Delete 删一条记录。**幂等**：键不在、甚至这张表压根没声明过，都是空操作。
//
// 新增: 没声明过的表不报错——删是幂等的，而「删一个不存在的东西」就是什么也不做。
// 报错的话，同一条调用在不同介质上一个响一个不响，换介质时会在离本包很远的地方冒出来。
func (u *RecordUnit) Delete(ctx context.Context, table, key string) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return u.errClosed()
	}

	// 没声明过的表在介质上根本没有对应的物理表，去 DELETE 会报「表不存在」。
	physical, declared := u.physical[table]
	if !declared {
		return nil
	}

	if _, err := u.medium.exec(ctx, u.medium.db,
		`DELETE FROM `+u.medium.qualify(physical)+` WHERE key = ?`, key); err != nil {
		return fmt.Errorf("datastore: 删单元 %q 的表 %q 的键 %q 失败：%w",
			u.spec.Name, table, key, err)
	}
	return nil
}

// SetSingleton 盖上这个单元的单例槽。只有 [RecordSpec.Singleton] 为真时才合法。
func (u *RecordUnit) SetSingleton(ctx context.Context, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return u.errClosed()
	}
	if !u.spec.Singleton {
		return failf(ErrMalformedName, "单元 %q 没有声明单例槽", u.spec.Name)
	}
	if !json.Valid(value) {
		return failf(ErrMalformedName, "单元 %q 的单例槽里的值不是合法 JSON", u.spec.Name)
	}

	if _, err := u.medium.exec(ctx, u.medium.db,
		`INSERT INTO `+u.medium.qualify(singletonsTable)+` (unit, value) VALUES (?, ?)
		 ON CONFLICT (unit) DO UPDATE SET value = EXCLUDED.value`,
		u.spec.Name, string(value)); err != nil {
		return fmt.Errorf("datastore: 盖单元 %q 的单例槽失败：%w", u.spec.Name, err)
	}
	return nil
}

// Close 释放这个单元，并把单元名放回去，之后同名单元才重新开得起来。**幂等**。
//
// 这里不关连接池：连接池是整份介质的，见 [Config.DB]。
func (u *RecordUnit) Close(context.Context) error {
	u.mutex.Lock()
	if u.closed {
		u.mutex.Unlock()
		return nil
	}
	u.closed = true
	u.mutex.Unlock()

	u.medium.releaseUnit(u.spec.Name)
	return nil
}
