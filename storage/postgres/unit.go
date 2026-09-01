// 本文件的作用：一个已打开的单元——读回整份快照、写一条、删一条、盖全局槽，
// 以及关掉之后所有调用都拒绝这件事。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:27-156

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/storage"
)

// kvUnit 是一个开在某份介质上的单元。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:27-42
//
// 它不自己拿连接池和 schema，而是从 backend 上取：这两样是介质的属性，
// 单元只是介质上的一块地方。physical 是这个单元每张表的物理表名，
// 在 [Backend.Open] 里就算好了——算它要查长度，而那一查必须发生在动库之前。
type kvUnit struct {
	backend    *Backend
	descriptor storage.KVUnitDescriptor
	// physical 把描述符里的逻辑表名映射到介质上的物理表名。
	// 「这张表声明过没有」这个判断一律看它，不看 descriptor.Tables 那个切片——
	// 查一个 map 是 O(1)，而且两者同源，不会分叉。
	physical map[string]string

	mutex  sync.Mutex
	closed bool
}

// 编译期确认这个类型确实是一个单元。
var _ storage.KVUnit = (*kvUnit)(nil)

// markClosed 由后端在自己关闭时调用：只把这个单元标记成关闭，不去动 backend.opened。
//
// 后端那边正在整张表一起清掉，这里再逐个摘一遍既多余、又要反向去拿后端的锁。
func (u *kvUnit) markClosed() {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	u.closed = true
}

// errClosed 是关掉之后所有调用共用的那个拒绝。
func (u *kvUnit) errClosed() error {
	return &storage.Error{
		Code:    storage.CodeClosed,
		Message: fmt.Sprintf("postgres 存储：单元 %q 已经关闭", u.descriptor.Name),
	}
}

// LoadAll 把这个单元在介质上的全部内容读回来。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:65-97
func (u *kvUnit) LoadAll(ctx context.Context) (storage.Snapshot, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return storage.Snapshot{}, u.errClosed()
	}

	tables := make(map[string]map[string]json.RawMessage, len(u.descriptor.Tables))
	for _, table := range u.descriptor.Tables {
		// 先建成空 map 再去读：声明过而一条记录都没有的表，契约要求它
		// **在场且为空**，而不是缺席。缺席和「空」在调用方那里是两件事——
		// 前者会让「这张表还没建出来」和「这张表是空的」长得一模一样。
		records := map[string]json.RawMessage{}
		tables[table] = records

		if err := u.scanTable(ctx, table, records); err != nil {
			return storage.Snapshot{}, err
		}
	}

	global, err := u.loadGlobal(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	return storage.Snapshot{Tables: tables, Global: global}, nil
}

// scanTable 把一张记录表整个读进 records。
func (u *kvUnit) scanTable(ctx context.Context, table string, records map[string]json.RawMessage) error {
	rows, err := u.backend.db.QueryContext(ctx,
		`SELECT key, value FROM `+qualify(u.backend.schema, u.physical[table]))
	if err != nil {
		return fmt.Errorf("postgres 存储：读单元 %q 的表 %q 失败：%w", u.descriptor.Name, table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("postgres 存储：读单元 %q 的表 %q 的一行失败：%w",
				u.descriptor.Name, table, err)
		}
		// 值那一列是 TEXT，库不替我们验 JSON（理由见包文档里 jsonb 那一段），
		// 所以这一验必须在这里做。不验的话一段坏文本会原样变成 json.RawMessage
		// 交出去，然后在某个离这里很远的 Unmarshal 处炸掉。
		if !json.Valid([]byte(value)) {
			return &storage.Error{
				Code: storage.CodeMalformedMedium,
				Message: fmt.Sprintf("单元 %q 的表 %q 里键 %q 的值不是合法 JSON",
					u.descriptor.Name, table, key),
			}
		}
		records[key] = json.RawMessage(value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres 存储：遍历单元 %q 的表 %q 失败：%w",
			u.descriptor.Name, table, err)
	}
	return nil
}

// loadGlobal 读全局槽；没声明过全局槽、或者声明了但从没写过，都返回 nil。
func (u *kvUnit) loadGlobal(ctx context.Context) (json.RawMessage, error) {
	if !u.descriptor.HasGlobal {
		return nil, nil
	}

	var value string
	row := u.backend.db.QueryRowContext(ctx,
		`SELECT value FROM `+qualify(u.backend.schema, "unit_globals")+` WHERE unit = $1`,
		u.descriptor.Name)
	switch err := row.Scan(&value); {
	case errors.Is(err, sql.ErrNoRows):
		// 声明了槽但一次都没写过——这是全新单元的正常状态，不是介质坏了。
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres 存储：读单元 %q 的全局槽失败：%w", u.descriptor.Name, err)
	}
	if !json.Valid([]byte(value)) {
		return nil, &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 的全局槽里的值不是合法 JSON", u.descriptor.Name),
		}
	}
	return json.RawMessage(value), nil
}

// PutRecord 写一条记录，同键覆盖。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:99-103
func (u *kvUnit) PutRecord(ctx context.Context, table, key string, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}

	physical, declared := u.physical[table]
	if !declared {
		return &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明过表 %q", u.descriptor.Name, table),
		}
	}

	// 记录键走绑定参数，永远不进语句文本——这是 [storage.KVUnit] 明写的后端义务。
	if _, err := u.backend.db.ExecContext(ctx,
		`INSERT INTO `+qualify(u.backend.schema, physical)+` (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, string(value)); err != nil {
		return fmt.Errorf("postgres 存储：写单元 %q 的表 %q 的键 %q 失败：%w",
			u.descriptor.Name, table, key, err)
	}
	return nil
}

// DeleteRecord 删一条记录。**幂等**。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:105-109
//
// 新增: **表没声明过时不报错**，这一条和抄来的那份形状相反——DSH 的 deleteRecord
// 经由 statementsFor（src/unit.ts:149-155）对没声明的表直接 throw。
// 这里跟的是本仓库的内存参考后端（storage/storagetest/memory.go）：删是幂等的，
// 而「删一个不存在的东西」就是什么也不做。两个后端在同一条调用上一个报错一个不报，
// 换后端时会在离存储层很远的地方冒出来，所以这里只能有一种行为，而参考后端是那一种。
func (u *kvUnit) DeleteRecord(ctx context.Context, table, key string) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}

	// 没声明过的表在介质上根本没有对应的物理表，去 DELETE 会报 undefined_table。
	physical, declared := u.physical[table]
	if !declared {
		return nil
	}

	if _, err := u.backend.db.ExecContext(ctx,
		`DELETE FROM `+qualify(u.backend.schema, physical)+` WHERE key = $1`, key); err != nil {
		return fmt.Errorf("postgres 存储：删单元 %q 的表 %q 的键 %q 失败：%w",
			u.descriptor.Name, table, key, err)
	}
	return nil
}

// SetGlobal 盖上这个单元的全局槽。
//
// 源: packages/storage/storage-sqlite/src/unit.ts:111-118
func (u *kvUnit) SetGlobal(ctx context.Context, value json.RawMessage) error {
	u.mutex.Lock()
	defer u.mutex.Unlock()

	if u.closed {
		return u.errClosed()
	}
	if !u.descriptor.HasGlobal {
		return &storage.Error{
			Code:    storage.CodeMalformedMedium,
			Message: fmt.Sprintf("单元 %q 没有声明全局槽", u.descriptor.Name),
		}
	}

	if _, err := u.backend.db.ExecContext(ctx,
		`INSERT INTO `+qualify(u.backend.schema, "unit_globals")+` (unit, value) VALUES ($1, $2)
		 ON CONFLICT (unit) DO UPDATE SET value = EXCLUDED.value`,
		u.descriptor.Name, string(value)); err != nil {
		return fmt.Errorf("postgres 存储：盖单元 %q 的全局槽失败：%w", u.descriptor.Name, err)
	}
	return nil
}

// Close 释放这个单元。**幂等**，且把它从后端那张「已打开」表里摘掉，
// 摘掉之后同名单元才重新开得起来。
//
// 这里不关连接池：连接池是整份介质的，归后端所有（见 [Config.DB]）。
func (u *kvUnit) Close(context.Context) error {
	u.mutex.Lock()
	if u.closed {
		u.mutex.Unlock()
		return nil
	}
	u.closed = true
	// 先放掉自己的锁再去拿后端的锁：这两把锁只允许「先后端、后单元」一个方向，
	// [Backend.Close] 也是先放掉后端的锁才去 markClosed 的。这里要是握着
	// 单元锁去拿后端锁，就出现了反向的一段，两边撞上就是死锁。
	u.mutex.Unlock()

	u.backend.release(u)
	return nil
}
