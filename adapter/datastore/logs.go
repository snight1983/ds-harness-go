// 本文件的作用：两种通用形态里的第二种——日志集：若干条流，每条流是一份头
// 加一串按 seq 升序、可以从头弹出的条目。
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

// insertChunkRows 是一条多值 INSERT 最多带几行条目。
//
// 新增: Postgres 一条语句最多 65535 个绑定参数，每行条目占三个，所以物理上界是
// 两万多行。这里取一个远小于它的数：分块的代价是多几个来回，不分块的代价是一次
// 攒批稍微大一点就整条语句被拒——两边不对称，所以留足余量。
const insertChunkRows = 500

// Revision 是一条流的变更令牌。
//
// 新增: 它是**来源限定**的：同一份没变过的流观察多少次都是同一个值，而两份各自
// 独立的介质发出的令牌永远比不出相等。各自从 0 数起的计数器不拌来源就一定会撞，
// 于是一个「从 A 读、拿 B 的令牌去核对」的调用方会以为自己看的是同一份东西。
//
// 值的内部结构不是契约，只能整个比。
type Revision string

// LogSpec 是一个日志集的静态形状。
type LogSpec struct {
	// Name 是单元名，必须满足 [ValidName]。它会被拼进物理表名。
	Name string
	// Version 是这个单元里的值的格式版本，第一次打开时盖到介质上。
	Version int
}

func (s LogSpec) validate() error {
	if err := checkUnitName(s.Name); err != nil {
		return err
	}
	if s.Version < 0 {
		return failf(ErrMalformedName, "单元 %q 的版本号是 %d，不能是负数", s.Name, s.Version)
	}
	return nil
}

// Entry 是流里的一条。
type Entry struct {
	// Seq 是这条在流里的序号，同一条流里严格递增，但**不保证从 0 起、也不保证连续**：
	// 最老的一段可以被 [LogUnit.TrimBefore] 弹掉。
	Seq int64
	// Payload 是一段不透明的 JSON。本包不解释它。
	Payload json.RawMessage
}

// Stream 是一条流的头部信息，不含条目。
type Stream struct {
	Name     string
	Head     json.RawMessage
	NextSeq  int64
	Revision Revision
}

// Segment 是从一条流上读到的一段。
type Segment struct {
	// Head 是这条流的头。
	Head json.RawMessage
	// Entries 是这一段的条目，按 seq 升序。
	Entries []Entry
	// BaseSeq 是**整条流**现存最早那条的 seq，不是这一段的起点。
	//
	// 读的一方要靠它分清「要的那一段早就被弹掉了」和「那一段压根没写过」。
	// 一条都没有时它是 [Segment.NextSeq]——一份空流推不出任何东西，而恰恰是那时候
	// 调用方要靠它决定下一条写在哪儿。
	BaseSeq int64
	// NextSeq 是下一条要写的 seq。
	NextSeq int64
	// Revision 标识**恰好这一次读到的这些值**。
	Revision Revision
}

// AppendRequest 是一次追加。
type AppendRequest struct {
	// Stream 是流名。它永远是绑定参数，可以是任意字符串。
	Stream string
	// Head 是这条流的头。EnsureStream 为真时它会被写下去或核对；为假时不看。
	Head json.RawMessage
	// EnsureStream 为真表示这条流可能还不在介质上，要连着这一批一起建出来。
	//
	// 建流和这一批条目在**同一个事务**里提交：崩在两者中间不会留下一条
	// 「建出来了但一条都没有」的流。
	EnsureStream bool
	// Entries 是这一批条目，按 seq 升序。可以是空的（只建流）。
	Entries []Entry
}

// LogUnit 是一个已打开的日志集。
//
// 头和条目负载对本包来说都是**不透明的 JSON**。
//
// 新增: 本包**不验**条目负载是不是合法 JSON，而记录集那边验（见
// [RecordUnit.Snapshot]）。两边不一样是想清楚的：记录集把值原样交出去，坏文本要到
// 很远的地方才炸；日志集的使用方拿到就解，坏在哪一条它自己当场说得清，而这里每读
// 一条多扫一遍负载的代价是按整份日志算的。
type LogUnit struct {
	medium *Medium
	spec   LogSpec
	// 两张物理表名，开的时候算好——每句 SQL 现拼一遍只是把同一个字符串反复算出来。
	streams string
	entries string

	mutex  sync.Mutex
	closed bool
}

// 两张表的物理表名怎么拼。
func logStreamsTableName(unit string) string { return "l_" + unit + "_streams" }
func logEntriesTableName(unit string) string { return "l_" + unit + "_entries" }

// OpenLog 打开一个日志集，介质上还没有它的痕迹时就建出来。
//
// 同一个单元名没关就开第二次返回 [ErrAlreadyOpen]；介质上盖着的版本号或形态对不上
// 返回 [ErrVersionMismatch]，且**一个字都不改**。
func (m *Medium) OpenLog(ctx context.Context, spec LogSpec) (*LogUnit, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	streams, err := m.physical(logStreamsTableName(spec.Name))
	if err != nil {
		return nil, err
	}
	entries, err := m.physical(logEntriesTableName(spec.Name))
	if err != nil {
		return nil, err
	}

	if err := m.claimUnit(spec.Name); err != nil {
		return nil, err
	}
	err = m.inTx(ctx, nil, func(tx *sql.Tx) error {
		if err := m.dialect.LockLayout(ctx, tx, layoutLockKey); err != nil {
			return fmt.Errorf("datastore: 拿布局锁失败：%w", err)
		}
		if err := m.registerUnit(ctx, tx, spec.Name, kindLog, spec.Version); err != nil {
			return err
		}
		// next_seq 单独存一列，不靠 MIN(seq)/MAX(seq) 现算：一条被弹空的流
		// 那两个聚合都是 NULL，答不出「下一条写在哪儿」，而恰恰是那时候调用方
		// 最需要它。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(streams)+` (
				name     TEXT PRIMARY KEY,
				head     TEXT   NOT NULL,
				next_seq BIGINT NOT NULL,
				revision BIGINT NOT NULL
			)`); err != nil {
			return fmt.Errorf("datastore: 建单元 %q 的流表失败：%w", spec.Name, err)
		}
		// payload 是 TEXT 不是 jsonb：jsonb 拒收 U+0000 那个码位，而一段合法的
		// JSON 字符串里完全可以有它。
		//
		// 主键 (stream, seq) 同时是三样东西：「同一条流里 seq 不许重复」那道闸、
		// 按 seq 寻址那条索引、以及从头弹出时那句 DELETE 走的路。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(entries)+` (
				stream  TEXT   NOT NULL REFERENCES `+m.qualify(streams)+`(name) ON DELETE CASCADE,
				seq     BIGINT NOT NULL,
				payload TEXT   NOT NULL,
				PRIMARY KEY (stream, seq)
			)`); err != nil {
			return fmt.Errorf("datastore: 建单元 %q 的条目表失败：%w", spec.Name, err)
		}
		return nil
	})
	if err != nil {
		m.releaseUnit(spec.Name)
		return nil, err
	}

	return &LogUnit{
		medium:  m,
		spec:    spec,
		streams: m.qualify(streams),
		entries: m.qualify(entries),
	}, nil
}

// Name 是这个单元的名字。
func (u *LogUnit) Name() string { return u.spec.Name }

func (u *LogUnit) errClosed() error {
	return failf(ErrClosed, "日志集 %q 已经关闭", u.spec.Name)
}

// check 是每个方法进门那一下。
func (u *LogUnit) check() error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return u.errClosed()
	}
	return nil
}

// revisionOf 把一条流的计数折成对外的令牌。
//
// 新增: 拌进实例标识和单元名两段。实例标识管的是两份介质之间不撞；单元名管的是
// 同一份介质里两个单元底下同名的流不撞——它们是两条毫无关系的流，各自从 0 数起。
func (u *LogUnit) revisionOf(counter int64) Revision {
	return Revision(u.medium.instance + ":" + u.spec.Name + ":" + strconv.FormatInt(counter, 10))
}

// ---- 读 ----

// List 列出这个单元里的所有流，按流名升序。
//
// 新增: 排序按流名，因为本包不认识头里有什么，排不出别的顺序。要按别的顺序排的
// 调用方自己排——它认识那份头。
func (u *LogUnit) List(ctx context.Context) ([]Stream, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	rows, err := u.medium.query(ctx, u.medium.db,
		`SELECT name, head, next_seq, revision FROM `+u.streams+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("datastore: 列举单元 %q 的流失败：%w", u.spec.Name, err)
	}
	defer func() { _ = rows.Close() }()

	var streams []Stream
	for rows.Next() {
		var stream Stream
		var head string
		var counter int64
		if err := rows.Scan(&stream.Name, &head, &stream.NextSeq, &counter); err != nil {
			return nil, fmt.Errorf("datastore: 列举单元 %q 的流失败：%w", u.spec.Name, err)
		}
		stream.Head = json.RawMessage(head)
		stream.Revision = u.revisionOf(counter)
		streams = append(streams, stream)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datastore: 列举单元 %q 的流失败：%w", u.spec.Name, err)
	}
	return streams, nil
}

// Load 读出一条流的头，加上 seq 不小于 fromSeq 的那些条目。
//
// 流不在这个单元里时返回 [ErrStreamNotFound]——那是正常控制流。
//
// 新增: 整段落在一次只读、可重复读的事务里。[Segment.Revision] 承诺它标识的恰好是
// 这一次读到的那些值，而读头、读条目、读起点是三句语句；读已提交给每句语句各自一个
// 快照，中间插进来的一次提交会让交出去的令牌配着另一份日志。
func (u *LogUnit) Load(ctx context.Context, name string, fromSeq int64) (Segment, error) {
	if err := u.check(); err != nil {
		return Segment{}, err
	}
	if fromSeq < 0 {
		return Segment{}, failf(ErrMalformedName, "Load 的 fromSeq 不能是负数（给的是 %d）", fromSeq)
	}

	var segment Segment
	err := u.medium.inReadTx(ctx, func(tx *sql.Tx) error {
		var head string
		var counter int64
		row := u.medium.queryRow(ctx, tx,
			`SELECT head, next_seq, revision FROM `+u.streams+` WHERE name = ?`, name)
		switch err := row.Scan(&head, &segment.NextSeq, &counter); {
		case errors.Is(err, sql.ErrNoRows):
			return failf(ErrStreamNotFound, "单元 %q 里没有流 %q", u.spec.Name, name)
		case err != nil:
			return fmt.Errorf("datastore: 读单元 %q 的流 %q 的头失败：%w", u.spec.Name, name, err)
		}
		segment.Head = json.RawMessage(head)
		segment.Revision = u.revisionOf(counter)

		entries, err := u.readEntries(ctx, tx, name, fromSeq)
		if err != nil {
			return err
		}
		segment.Entries = entries

		base, err := u.baseSeqOf(ctx, tx, name, segment.NextSeq)
		if err != nil {
			return err
		}
		segment.BaseSeq = base
		return nil
	})
	if err != nil {
		return Segment{}, err
	}
	return segment, nil
}

func (u *LogUnit) readEntries(
	ctx context.Context, tx *sql.Tx, name string, fromSeq int64,
) ([]Entry, error) {
	rows, err := u.medium.query(ctx, tx,
		`SELECT seq, payload FROM `+u.entries+` WHERE stream = ? AND seq >= ? ORDER BY seq`,
		name, fromSeq)
	if err != nil {
		return nil, fmt.Errorf("datastore: 读单元 %q 的流 %q 失败：%w", u.spec.Name, name, err)
	}
	defer func() { _ = rows.Close() }()

	var entries []Entry
	for rows.Next() {
		var entry Entry
		var payload string
		if err := rows.Scan(&entry.Seq, &payload); err != nil {
			return nil, fmt.Errorf("datastore: 读单元 %q 的流 %q 的一行失败：%w", u.spec.Name, name, err)
		}
		entry.Payload = json.RawMessage(payload)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datastore: 遍历单元 %q 的流 %q 失败：%w", u.spec.Name, name, err)
	}
	return entries, nil
}

// baseSeqOf 问一条流现存最早那条的 seq，一条都没有时给出下一条要写的 seq。
func (u *LogUnit) baseSeqOf(
	ctx context.Context, tx *sql.Tx, name string, nextSeq int64,
) (int64, error) {
	var earliest sql.NullInt64
	err := u.medium.queryRow(ctx, tx,
		`SELECT MIN(seq) FROM `+u.entries+` WHERE stream = ?`, name).Scan(&earliest)
	if err != nil {
		return 0, fmt.Errorf("datastore: 读单元 %q 的流 %q 的起点失败：%w", u.spec.Name, name, err)
	}
	if !earliest.Valid {
		return nextSeq, nil
	}
	return earliest.Int64, nil
}

// ReadRevision 只读出一条流当前的令牌，不读它的条目。
func (u *LogUnit) ReadRevision(ctx context.Context, name string) (Revision, error) {
	if err := u.check(); err != nil {
		return "", err
	}
	var counter int64
	err := u.medium.queryRow(ctx, u.medium.db,
		`SELECT revision FROM `+u.streams+` WHERE name = ?`, name).Scan(&counter)
	if errors.Is(err, sql.ErrNoRows) {
		return "", failf(ErrStreamNotFound, "单元 %q 里没有流 %q", u.spec.Name, name)
	}
	if err != nil {
		return "", fmt.Errorf("datastore: 读单元 %q 的流 %q 的令牌失败：%w", u.spec.Name, name, err)
	}
	return u.revisionOf(counter), nil
}

// ---- 写 ----

// Append 把一批条目追加到一条流上，[AppendRequest.EnsureStream] 为真时连带把流建出来。
//
// 同一条流上同一个 seq 写两遍会撞主键，**响的**：那说明有两个写者在同一份日志上
// 各写各的，不是这里能悄悄合上的事。
func (u *LogUnit) Append(ctx context.Context, request AppendRequest) error {
	if err := u.check(); err != nil {
		return err
	}
	return u.medium.inTx(ctx, nil, func(tx *sql.Tx) error {
		if request.EnsureStream {
			if err := u.ensureStream(ctx, tx, request.Stream, request.Head); err != nil {
				return err
			}
		}
		if err := u.insertEntries(ctx, tx, request.Stream, request.Entries); err != nil {
			return err
		}
		return u.advance(ctx, tx, request.Stream, request.Entries)
	})
}

// ensureStream 建流，已经在了就核对头没变过。
//
// 新增: 冲突时不报错而是回头核对，因为这一步会**重来**：一次写在提交回执丢掉之后
// 由调用方重试是数据库上的常态，而它手里那个「建过了没有」的位还是假的。
//
// 核对的是**字节**。头对本包是不透明的，比不了字段；调用方那边只要是同一份头就排出
// 同一串字节（Go 的 encoding/json 排结构体是定序的），所以字节比等价于逐字段比。
// 排得不定序的调用方在这里会看见撞号——那时候该改的是它的编码，不是这一比。
func (u *LogUnit) ensureStream(
	ctx context.Context, tx *sql.Tx, name string, head json.RawMessage,
) error {
	if _, err := u.medium.exec(ctx, tx,
		`INSERT INTO `+u.streams+` (name, head, next_seq, revision) VALUES (?, ?, 0, 0)
		 ON CONFLICT (name) DO NOTHING`, name, string(head)); err != nil {
		return fmt.Errorf("datastore: 在单元 %q 里建流 %q 失败：%w", u.spec.Name, name, err)
	}

	var stored string
	if err := u.medium.queryRow(ctx, tx,
		`SELECT head FROM `+u.streams+` WHERE name = ?`, name).Scan(&stored); err != nil {
		return fmt.Errorf("datastore: 读单元 %q 的流 %q 的头失败：%w", u.spec.Name, name, err)
	}
	if stored != string(head) {
		return failf(ErrHeadConflict, "单元 %q 里的流 %q", u.spec.Name, name)
	}
	return nil
}

// insertEntries 把一批条目插进表，按 [insertChunkRows] 分块。
func (u *LogUnit) insertEntries(
	ctx context.Context, tx *sql.Tx, name string, entries []Entry,
) error {
	for start := 0; start < len(entries); start += insertChunkRows {
		end := min(start+insertChunkRows, len(entries))
		chunk := entries[start:end]

		var statement strings.Builder
		statement.WriteString(`INSERT INTO ` + u.entries + ` (stream, seq, payload) VALUES `)
		arguments := make([]any, 0, len(chunk)*3)
		for index, entry := range chunk {
			if index > 0 {
				statement.WriteByte(',')
			}
			statement.WriteString("(?,?,?)")
			arguments = append(arguments, name, entry.Seq, string(entry.Payload))
		}
		if _, err := u.medium.exec(ctx, tx, statement.String(), arguments...); err != nil {
			return fmt.Errorf("datastore: 单元 %q 的流 %q 写 seq %d..%d 那批失败：%w",
				u.spec.Name, name, chunk[0].Seq, chunk[len(chunk)-1].Seq, err)
		}
	}
	return nil
}

// advance 推进一条流的 next_seq 和变更计数。
//
// 流不在表里时影响零行，那说明调用方以为它已经建出来了而其实没有——报出来，
// 不要让一批条目悄悄写进一条不存在的流（外键会先拦住，但那条报错说的是外键，
// 不是这件事）。
//
// 新增: next_seq 取「两个里大的那个」而不是直接盖，因为一次空批（只建流）算出来的
// 是 0，直接盖会把已经推进过的起点抹回去。
func (u *LogUnit) advance(ctx context.Context, tx *sql.Tx, name string, entries []Entry) error {
	var next int64
	if len(entries) > 0 {
		next = entries[len(entries)-1].Seq + 1
	}
	result, err := u.medium.exec(ctx, tx,
		`UPDATE `+u.streams+` SET next_seq = `+u.medium.dialect.Greatest("next_seq", "?")+`,
		        revision = revision + 1
		 WHERE name = ?`, next, name)
	if err != nil {
		return fmt.Errorf("datastore: 推进单元 %q 的流 %q 失败：%w", u.spec.Name, name, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("datastore: 推进单元 %q 的流 %q 失败：%w", u.spec.Name, name, err)
	}
	if affected == 0 {
		return failf(ErrStreamNotFound, "单元 %q 里没有流 %q，写不进去", u.spec.Name, name)
	}
	return nil
}

// TrimBefore 丢掉一条流里 seq 严格小于 beforeSeq 的那些条目。
//
// 新增: 令牌**不动**。它标识的是「这条流里有哪些条目」，而从头弹出是一次纯粹的收缩，
// 读的一侧靠 [Segment.BaseSeq] 自己认得出来。让弹出去动令牌，会让所有手里攥着令牌的
// 观察者在一次它们本来不必关心的收缩之后集体重读。
func (u *LogUnit) TrimBefore(ctx context.Context, name string, beforeSeq int64) error {
	if err := u.check(); err != nil {
		return err
	}
	if beforeSeq < 0 {
		return failf(ErrMalformedName, "TrimBefore 的 beforeSeq 不能是负数（给的是 %d）", beforeSeq)
	}

	// 流在不在先查一遍：不查的话，一条不存在的流上那句 DELETE 影响零行，和
	// 「那一段早就弹掉了」长得一模一样，于是调用方永远等不到 [ErrStreamNotFound]。
	var exists int
	err := u.medium.queryRow(ctx, u.medium.db,
		`SELECT 1 FROM `+u.streams+` WHERE name = ?`, name).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return failf(ErrStreamNotFound, "单元 %q 里没有流 %q", u.spec.Name, name)
	}
	if err != nil {
		return fmt.Errorf("datastore: 查单元 %q 的流 %q 在不在失败：%w", u.spec.Name, name, err)
	}

	if _, err := u.medium.exec(ctx, u.medium.db,
		`DELETE FROM `+u.entries+` WHERE stream = ? AND seq < ?`, name, beforeSeq); err != nil {
		return fmt.Errorf("datastore: 弹掉单元 %q 的流 %q 里 seq 小于 %d 的那些条目失败：%w",
			u.spec.Name, name, beforeSeq, err)
	}
	return nil
}

// Close 释放这个单元，并把单元名放回去，之后同名单元才重新开得起来。**幂等**。
//
// 这里不关连接池：连接池是整份介质的，见 [Config.DB]。
func (u *LogUnit) Close(context.Context) error {
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
