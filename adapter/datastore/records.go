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
	"strconv"
	"strings"
	"sync"
)

// RecordGuard 是一次写的**前置条件**，封闭的两种。
//
// 新增: 记录集一开始只有无条件覆盖，因为它上面那一层把权威状态放在进程内存里，
// 读-改-写整段发生在一个进程的一把锁底下。这个服务是多副本的，那一段中间会被
// 别的副本插进来，所以写这一侧必须能说清「我改的是哪一版」。
//
// 传 nil 表示无条件，**它不是第三个成员**：没有前置条件这件事的表达方式是这个值
// 不存在，而不是一个叫「无条件」的成员——多一个成员就多一处要分派的分支，
// 而那个分支和 nil 分支永远做同一件事。
type RecordGuard interface {
	sealedRecordGuard()
}

// MustBeAbsent 要求这条记录（或这个单元的单例槽）此刻**不存在**。
//
// 已经在了就是 [ErrStaleRevision]。它落成一句 ON CONFLICT DO NOTHING，
// **不是**先查一次再写一次——那两步之间正是别的副本插进来的地方。
type MustBeAbsent struct{}

func (MustBeAbsent) sealedRecordGuard() {}

// MustMatch 要求此刻的令牌正好是 Revision。
//
// 对不上（包括这条记录已经被删掉、或者令牌根本不是这张表发的）就是 [ErrStaleRevision]。
type MustMatch struct {
	Revision Revision
}

func (MustMatch) sealedRecordGuard() {}

// singletonSlot 是单例槽在令牌里占的那一段。
//
// 新增: 它是故意不合 [ValidName] 的，所以永远不会和某张真表的那一段撞上。
const singletonSlot = "@singleton"

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
					key      TEXT   PRIMARY KEY,
					value    TEXT   NOT NULL,
					revision BIGINT NOT NULL
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

// SnapshotTable 只读出其中一张表的全部记录。
//
// 没声明过的表交出一张空 map 而不是错误，理由和 [RecordUnit.Read] 那里一样：
// 它在介质上根本没有对应的物理表，而调用方问的是「这张表里有什么」。
//
// 新增: 整个方法都是本仓库自有的。只要一张表的调用方以前只能走
// [RecordUnit.Snapshot]，于是白读了同一个单元里其余所有的表。一张表落在一条
// SELECT 里本身就是原子的，所以这条路不需要那圈只读事务。
func (u *RecordUnit) SnapshotTable(ctx context.Context, table string) (map[string]json.RawMessage, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return nil, u.errClosed()
	}

	records := map[string]json.RawMessage{}
	if _, declared := u.physical[table]; !declared {
		return records, nil
	}
	if err := u.scanTable(ctx, u.medium.db, table, records); err != nil {
		return nil, err
	}
	return records, nil
}

func (u *RecordUnit) scanTable(
	ctx context.Context, tx querier, table string, records map[string]json.RawMessage,
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

// revisionOf 把一条记录的计数折成对外的令牌。
//
// 新增: 和 [LogUnit] 那个令牌同源同形——拌进实例标识（两份介质之间不撞）、单元名和
// 表名（同一份介质里不同表底下同名的键是两条毫无关系的记录，各自从 1 数起）。
func (u *RecordUnit) revisionOf(slot string, counter int64) Revision {
	return Revision(u.medium.instance + ":" + u.spec.Name + ":" + slot + ":" +
		strconv.FormatInt(counter, 10))
}

// counterOf 把一个令牌折回计数。第二个返回值为 false 表示它不是这个槽发出来的。
//
// 新增: 别处发的令牌**当作对不上处理，不当作错误**：一个拿着 A 介质的令牌去 B 介质
// 写的调用方，它真正的问题是「我以为我读过这条记录」，而那正是前置条件要拦的事。
func (u *RecordUnit) counterOf(slot string, revision Revision) (int64, bool) {
	rest, ok := strings.CutPrefix(string(revision),
		u.medium.instance+":"+u.spec.Name+":"+slot+":")
	if !ok {
		return 0, false
	}
	counter, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return counter, true
}

func (u *RecordUnit) staleErr(slot, key string) error {
	if key == "" {
		return failf(ErrStaleRevision, "单元 %q 的 %s 上的前置条件不成立", u.spec.Name, slot)
	}
	return failf(ErrStaleRevision, "单元 %q 的表 %q 的键 %q 上的前置条件不成立",
		u.spec.Name, slot, key)
}

// Read 读出单独一条记录，连同它此刻的令牌。
//
// 第三个返回值为 false 表示这条记录不存在（表压根没声明过也算不存在），此时值为 nil、
// 令牌为空串。**不存在不是错误**：调用方问的就是「在不在」。
//
// 新增: 整个方法都是本仓库自有的。多副本部署下进程内存不再是权威，每次读都得穿到
// 介质；而穿到介质的读一旦要改回去，就必须同时拿到「我读的是哪一版」。
func (u *RecordUnit) Read(
	ctx context.Context, table, key string,
) (json.RawMessage, Revision, bool, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return nil, "", false, u.errClosed()
	}

	// 没声明过的表在介质上根本没有对应的物理表，去 SELECT 会报「表不存在」。
	// 而这个方法问的是「在不在」，答案显然是不在。
	physical, declared := u.physical[table]
	if !declared {
		return nil, "", false, nil
	}

	var value string
	var counter int64
	row := u.medium.queryRow(ctx, u.medium.db,
		`SELECT value, revision FROM `+u.medium.qualify(physical)+` WHERE key = ?`, key)
	switch err := row.Scan(&value, &counter); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, "", false, nil
	case err != nil:
		return nil, "", false, fmt.Errorf("datastore: 读单元 %q 的表 %q 的键 %q 失败：%w",
			u.spec.Name, table, key, err)
	}
	if !json.Valid([]byte(value)) {
		return nil, "", false, failf(ErrMalformedMedium,
			"单元 %q 的表 %q 里键 %q 的值不是合法 JSON", u.spec.Name, table, key)
	}
	return json.RawMessage(value), u.revisionOf(table, counter), true, nil
}

// ReadSingleton 读出这个单元的单例槽，连同它此刻的令牌。
//
// 只有 [RecordSpec.Singleton] 为真时才合法。从没写过时值为 nil、令牌为空串。
func (u *RecordUnit) ReadSingleton(ctx context.Context) (json.RawMessage, Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return nil, "", u.errClosed()
	}
	if !u.spec.Singleton {
		return nil, "", failf(ErrMalformedName, "单元 %q 没有声明单例槽", u.spec.Name)
	}

	var value string
	var counter int64
	row := u.medium.queryRow(ctx, u.medium.db,
		`SELECT value, revision FROM `+u.medium.qualify(singletonsTable)+` WHERE unit = ?`,
		u.spec.Name)
	switch err := row.Scan(&value, &counter); {
	case errors.Is(err, sql.ErrNoRows):
		// 声明了槽但一次都没写过——全新单元的正常状态，不是介质坏了。
		return nil, "", nil
	case err != nil:
		return nil, "", fmt.Errorf("datastore: 读单元 %q 的单例槽失败：%w", u.spec.Name, err)
	}
	if !json.Valid([]byte(value)) {
		return nil, "", failf(ErrMalformedMedium, "单元 %q 的单例槽里的值不是合法 JSON", u.spec.Name)
	}
	return json.RawMessage(value), u.revisionOf(singletonSlot, counter), nil
}

// Put 写一条记录，交回写完之后的令牌。
//
// guard 为 nil 时同键覆盖；给了前置条件而条件不成立时**介质上一个字都不改**，
// 返回 [ErrStaleRevision]。见 [RecordGuard]。
//
// key 可以是任意字符串：它永远走绑定参数，不进语句文本。
func (u *RecordUnit) Put(
	ctx context.Context, table, key string, value json.RawMessage, guard RecordGuard,
) (Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return "", u.errClosed()
	}

	physical, declared := u.physical[table]
	if !declared {
		return "", failf(ErrMalformedName, "单元 %q 没有声明过表 %q", u.spec.Name, table)
	}
	if !json.Valid(value) {
		return "", failf(ErrMalformedName, "单元 %q 的表 %q 里键 %q 的值不是合法 JSON",
			u.spec.Name, table, key)
	}

	counter, err := u.bump(ctx, bumpTarget{
		qualified: u.medium.qualify(physical),
		keyColumn: "key",
		slot:      table,
		bindKey:   key,
		humanKey:  key,
	}, value, guard)
	if err != nil {
		return "", err
	}
	return u.revisionOf(table, counter), nil
}

// SetSingleton 盖上这个单元的单例槽，交回写完之后的令牌。
//
// 只有 [RecordSpec.Singleton] 为真时才合法。guard 的语义同 [RecordUnit.Put]。
func (u *RecordUnit) SetSingleton(
	ctx context.Context, value json.RawMessage, guard RecordGuard,
) (Revision, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return "", u.errClosed()
	}
	if !u.spec.Singleton {
		return "", failf(ErrMalformedName, "单元 %q 没有声明单例槽", u.spec.Name)
	}
	if !json.Valid(value) {
		return "", failf(ErrMalformedName, "单元 %q 的单例槽里的值不是合法 JSON", u.spec.Name)
	}

	counter, err := u.bump(ctx, bumpTarget{
		qualified: u.medium.qualify(singletonsTable),
		keyColumn: "unit",
		slot:      singletonSlot,
		bindKey:   u.spec.Name,
		// 单例槽没有「键」这个概念——它在物理表里的那一列存的是单元名，
		// 而单元名已经在错误信息的前半句里了。
		humanKey: "",
	}, value, guard)
	if err != nil {
		return "", err
	}
	return u.revisionOf(singletonSlot, counter), nil
}

// bumpTarget 说清这一次写落在哪一行上。
type bumpTarget struct {
	// qualified 是限定过的物理表名，由本包拼出，不来自调用方。
	qualified string
	// keyColumn 是主键那一列叫什么，本包写死的两个字面量之一（key / unit）。
	keyColumn string
	// slot 是这一行在令牌里占的那一段：表名，或者 [singletonSlot]。
	slot string
	// bindKey 是主键那一列的值，永远走绑定参数。
	bindKey string
	// humanKey 是错误信息里那个键，单例槽留空。
	humanKey string
}

// bump 是三种前置条件落到 SQL 的那一处，记录表和单例槽共用。
//
// 新增: 两张表只差主键那一列叫什么，语句骨架完全一样。分开写两份的话，将来改了一处
// 忘了另一处，症状是「单例槽的令牌不换」——一个只在多副本下才看得见的丢更新。
//
// 令牌从 1 起数：0 留给「这一行还不存在」，于是「令牌对不上」和「记录不在了」
// 在 SQL 里是同一句 WHERE，不必分两次问。
//
// ON CONFLICT DO UPDATE 那一句里的 revision 用表名限定过。两种库都规定不带前缀的
// 列名指的是**已经在的那一行**（要提本次待插入的那份得写 EXCLUDED.），所以不限定
// 也对；限定是因为这一处一旦解错方向，写出来的是「revision 恒等于 1」——每次写都
// 交回同一个令牌，而那正是前置条件用来判断「没人动过」的东西。
func (u *RecordUnit) bump(
	ctx context.Context, target bumpTarget, value json.RawMessage, guard RecordGuard,
) (int64, error) {
	insert := `INSERT INTO ` + target.qualified +
		` (` + target.keyColumn + `, value, revision) VALUES (?, ?, 1) ON CONFLICT (` +
		target.keyColumn + `) DO `

	var (
		statement string
		arguments []any
	)
	switch typed := guard.(type) {
	case nil:
		statement = insert + `UPDATE SET value = EXCLUDED.value, revision = ` +
			target.qualified + `.revision + 1 RETURNING revision`
		arguments = []any{target.bindKey, string(value)}
	case MustBeAbsent:
		statement = insert + `NOTHING RETURNING revision`
		arguments = []any{target.bindKey, string(value)}
	case MustMatch:
		expected, ok := u.counterOf(target.slot, typed.Revision)
		if !ok {
			return 0, u.staleErr(target.slot, target.humanKey)
		}
		statement = `UPDATE ` + target.qualified + ` SET value = ?, revision = revision + 1
		             WHERE ` + target.keyColumn + ` = ? AND revision = ? RETURNING revision`
		arguments = []any{string(value), target.bindKey, expected}
	default:
		// RecordGuard 是封闭的：sealedRecordGuard 那个未导出方法让包外造不出第三个
		// 成员。这一支只可能是本包自己将来加了成员却忘了在这里分派。
		return 0, failf(ErrMalformedName, "认不出的写前置条件 %T", guard)
	}

	var counter int64
	row := u.medium.queryRow(ctx, u.medium.db, statement, arguments...)
	switch err := row.Scan(&counter); {
	case errors.Is(err, sql.ErrNoRows):
		// 两种带条件的意图共用这一支：DO NOTHING 撞上已存在、条件 UPDATE 没匹配上，
		// 交回的都是零行。无条件那一支到不了这里——它要么写成，要么报别的错。
		return 0, u.staleErr(target.slot, target.humanKey)
	case err != nil:
		return 0, fmt.Errorf("datastore: 写单元 %q 的 %s 失败：%w", u.spec.Name, target.slot, err)
	}
	return counter, nil
}

// Delete 删一条记录，交回它删之前在不在。
//
// guard 为 nil 时**幂等**：键不在、甚至这张表压根没声明过，都是空操作，交回 false。
// 给了令牌而对不上（包括记录已经不在了）时返回 [ErrStaleRevision]，且一个字都不改。
//
// 新增: 参数是 *[MustMatch] 而不是 [RecordGuard]。删这一侧只有「必须还是那一版」
// 讲得通——[MustBeAbsent] 落到删上是「删一个必须不存在的东西」，那句话没有意义，
// 而收下一个没有意义的输入就得在运行期把它拒掉。用更窄的类型，它在编译期就写不出来。
//
// 没声明过的表不报错——删是幂等的，而「删一个不存在的东西」就是什么也不做。
// 报错的话，同一条调用在不同介质上一个响一个不响，换介质时会在离本包很远的地方冒出来。
func (u *RecordUnit) Delete(
	ctx context.Context, table, key string, guard *MustMatch,
) (bool, error) {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return false, u.errClosed()
	}

	// 没声明过的表在介质上根本没有对应的物理表，去 DELETE 会报「表不存在」。
	physical, declared := u.physical[table]
	if !declared {
		if guard != nil {
			// 表都没有，那一版更不可能还在。
			return false, u.staleErr(table, key)
		}
		return false, nil
	}

	statement := `DELETE FROM ` + u.medium.qualify(physical) + ` WHERE key = ?`
	arguments := []any{key}
	if guard != nil {
		expected, ok := u.counterOf(table, guard.Revision)
		if !ok {
			return false, u.staleErr(table, key)
		}
		statement += ` AND revision = ?`
		arguments = append(arguments, expected)
	}

	result, err := u.medium.exec(ctx, u.medium.db, statement, arguments...)
	if err != nil {
		return false, fmt.Errorf("datastore: 删单元 %q 的表 %q 的键 %q 失败：%w",
			u.spec.Name, table, key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("datastore: 数单元 %q 的表 %q 删掉了几行失败：%w",
			u.spec.Name, table, err)
	}
	if affected == 0 && guard != nil {
		return false, u.staleErr(table, key)
	}
	return affected > 0, nil
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
