// 本文件的作用：三种通用形态里的第三种——文档集：若干个分组，每组若干条带文本的
// 文档，能按一段文字在整个集合上找出命中的那些条并数出命中次数。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。前两种形状装不下按文本找这件事：
// 记录集只按键取值，日志集只按 seq 取一段，两者都答不出「哪些条里有这段话」。

package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// DocSpec 是一个文档集的静态形状。
type DocSpec struct {
	// Name 是单元名，必须满足 [ValidName]。它会被拼进物理表名。
	Name string
	// Version 是这个单元里的值的格式版本，第一次打开时盖到介质上。
	Version int
}

func (s DocSpec) validate() error {
	if err := checkUnitName(s.Name); err != nil {
		return err
	}
	if s.Version < 0 {
		return failf(ErrMalformedName, "单元 %q 的版本号是 %d，不能是负数", s.Name, s.Version)
	}
	return nil
}

// Doc 是一条文档。
//
// 除了 [Doc.Body]，别的字段本包一律不解释，只当成能等值比较、能排序的标签往回筛。
type Doc struct {
	// Seq 是这条在它那一组里的序号，同组内不许重复。
	Seq int64
	// At 是这条的时刻，排序时用。本包不规定它的单位。
	At int64
	// Kind 是一个由调用方定义的分类标签。
	Kind string
	// Facet 是第二个分类标签，和 [Doc.Kind] 正交。
	Facet string
	// Body 是要被找的那段文本，原样存、原样交回。
	Body string
}

// MatchRequest 是一次查找。
//
// 所有筛选条件之间是**与**的关系；每个切片内部是**或**。
type MatchRequest struct {
	// Groups 限定只在这几组里找。空切片表示**不限**，不是「一组都不找」——
	// 调用方那边算出一个空集合时该直接不发这次查找，而不是发一次空切片。
	Groups []string
	// ExcludeGroups 把这几组排除掉。它在 [MatchRequest.Groups] 之后生效。
	ExcludeGroups []string
	// Needle 是要找的那段文字。空的会被拒。
	//
	// 匹配是**字面的**：不分词、不做词干、不认通配符。大小写和空白的处置见
	// [DocUnit.Match]。
	Needle string
	// Kinds 限定 [Doc.Kind] 落在这几个值里。空切片表示不限。
	Kinds []string
	// Facets 限定 [Doc.Facet] 落在这几个值里。空切片表示不限。
	Facets []string
	// SeqFrom、SeqTo 是 [Doc.Seq] 的闭区间边界，nil 表示那一头不设限。
	SeqFrom, SeqTo *int64
	// AtFrom、AtTo 是 [Doc.At] 的闭区间边界，nil 表示那一头不设限。
	AtFrom, AtTo *int64
	// BestPerGroup 为真时每一组只交回命中最好的那一条。
	//
	// 这件事必须在库里做，不能取回来再在内存里挑：翻页是在库里切的，先切页再挑
	// 每组最好的那条，会让一页里出现同一组的好几条，而下一页里那一组一条都没有。
	BestPerGroup bool
	// Limit 是这一页最多几条，必须为正。
	Limit int
	// Offset 是跳过前几条，不能是负数。
	Offset int
}

func (r MatchRequest) validate() error {
	if r.Limit <= 0 {
		return failf(ErrMalformedName, "查找的 Limit 是 %d，必须为正", r.Limit)
	}
	if r.Offset < 0 {
		return failf(ErrMalformedName, "查找的 Offset 是 %d，不能是负数", r.Offset)
	}
	return nil
}

// MatchRow 是一条命中。
type MatchRow struct {
	// Group 是这条落在哪一组。
	Group string
	// Doc 是这条文档本身。
	Doc
	// Hits 是这段文字在 [Doc.Body] 里出现了几次（不计重叠）。
	Hits int64
}

// DocUnit 是一个已打开的文档集。
//
// 新增: 它**不是倒排索引**。一次查找是在这个单元的全部文档上扫一遍子串，代价随
// 集合大小线性增长。选它而不是选 Postgres 的 tsvector，是因为 to_tsvector 不切
// 中文——一整句中文会被当成一个词元，于是「找一个词」永远不命中，而那种失败是
// 静默的：查询跑绿了，只是一条都没有。子串扫描慢，但它答得对，而且两种方言上
// 答的是同一件事。
type DocUnit struct {
	medium *Medium
	spec   DocSpec
	// 两张物理表名，开的时候算好。
	docs   string
	groups string

	mutex  sync.Mutex
	closed bool
}

// 两张表的物理表名怎么拼。
func docsTableName(unit string) string      { return "d_" + unit + "_docs" }
func docGroupsTableName(unit string) string { return "d_" + unit + "_groups" }

// OpenDocs 打开一个文档集，介质上还没有它的痕迹时就建出来。
//
// 同一个单元名没关就开第二次返回 [ErrAlreadyOpen]；介质上盖着的版本号或形态对不上
// 返回 [ErrVersionMismatch]，且**一个字都不改**。
func (m *Medium) OpenDocs(ctx context.Context, spec DocSpec) (*DocUnit, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	docs, err := m.physical(docsTableName(spec.Name))
	if err != nil {
		return nil, err
	}
	groups, err := m.physical(docGroupsTableName(spec.Name))
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
		if err := m.registerUnit(ctx, tx, spec.Name, kindDocs, spec.Version); err != nil {
			return err
		}
		// tag 是这一组内容的一个印记，由调用方定义。它在这里的唯一用处是让调用方
		// 自己答得出「这一组要不要重建」——本包不解释它，也不拿它做任何判断。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(groups)+` (
				name TEXT PRIMARY KEY,
				tag  TEXT NOT NULL
			)`); err != nil {
			return fmt.Errorf("datastore: 建单元 %q 的分组表失败：%w", spec.Name, err)
		}
		// folded 是 body 折过之后的那一份，查找只看它。两份都存是拿空间换两件事：
		// 交回去的是原文（调用方要拿它做高亮），而比对走的是折过的（大小写和
		// 换行不该影响命中）。折这一下在 Go 里做，不在 SQL 里做，理由见 [fold]。
		//
		// 主键 (grp, seq) 同时是「同一组里 seq 不许重复」那道闸和按组删除那条路。
		if _, err := m.exec(ctx, tx, `
			CREATE TABLE IF NOT EXISTS `+m.qualify(docs)+` (
				grp     TEXT   NOT NULL REFERENCES `+m.qualify(groups)+`(name) ON DELETE CASCADE,
				seq     BIGINT NOT NULL,
				made_at BIGINT NOT NULL,
				kind    TEXT   NOT NULL,
				facet   TEXT   NOT NULL,
				body    TEXT   NOT NULL,
				folded  TEXT   NOT NULL,
				PRIMARY KEY (grp, seq)
			)`); err != nil {
			return fmt.Errorf("datastore: 建单元 %q 的文档表失败：%w", spec.Name, err)
		}
		return nil
	})
	if err != nil {
		m.releaseUnit(spec.Name)
		return nil, err
	}

	return &DocUnit{
		medium: m,
		spec:   spec,
		docs:   m.qualify(docs),
		groups: m.qualify(groups),
	}, nil
}

// Name 是这个单元的名字。
func (u *DocUnit) Name() string { return u.spec.Name }

// check 是每个方法进门那一下。
func (u *DocUnit) check() error {
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if u.closed {
		return failf(ErrClosed, "文档集 %q 已经关闭", u.spec.Name)
	}
	return nil
}

// Fold 把一段文本折成 [DocUnit.Match] 拿来比对的那一份。
//
// 新增: 它是导出的，因为有的调用方手里有一批还没落进介质的文档（比如正开着的那些
// 会话），要在库外给它们算出和库里同一个分数，再把两边的结果并起来排。那种时候
// **必须**用这个函数，不能在外面再实现一遍——两份折法只要差一点，同一句话就会在
// 库里命中、在库外不命中，而那种分岔没有任何一侧的测试看得见。
func Fold(text string) string { return fold(stripNul(text)) }

// fold 把一段文本折成用来比对的那一份：小写，且所有连续空白折成一个空格。
//
// 新增: 折这一下必须在 Go 里做，不能落成 SQL 里的 lower()。SQLite 不带 ICU 时
// lower() 只处理 ASCII，Postgres 的 lower() 认整个 Unicode——同一句话在两种方言
// 上会折成两样，于是同一批用例在两种库上压的不是同一件事。写和读都从这一个函数
// 出去，两边就不可能分叉。
//
// 折空白是为了让「一段文字」在 SQL 里能用子串比对表达：查 "hello world" 要能命中
// 正文里换了行的 "hello\nworld"，而 SQL 的 LIKE 表达不了「一段或多段空白」。
func fold(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// stripNul 把 U+0000 剔掉。
//
// 新增: 这个码位在两种库的文本列上都待不住，而且待不住的方式还不一样：Postgres
// 当场拒收整条，SQLite 收下但它的 length() 数到那儿就停——于是同一条正文在两边
// 算出两个长度，排序跟着分岔。剔掉是唯一在两边都说得通的处置。
func stripNul(text string) string {
	if !strings.ContainsRune(text, 0) {
		return text
	}
	return strings.ReplaceAll(text, "\x00", "")
}

// likeEscape 把一段文字里 LIKE 认得的那几个字符转义掉，好让它只当字面量用。
func likeEscape(text string) string {
	var escaped strings.Builder
	escaped.Grow(len(text))
	for _, r := range text {
		if r == '\\' || r == '%' || r == '_' {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

// ---- 写 ----

// Replace 把一整组文档换成给定的这一批，并盖上这一组的印记。
//
// 整件事在**同一个事务**里：崩在中间不会留下删了一半的一组。这也是本包对这种
// 形状唯一的写入口——没有「往一组里再加一条」，因为重建一组的调用方每次都算得出
// 完整的那一批，而增量写要它自己记住上次写到哪儿，那份记账迟早和介质对不上。
func (u *DocUnit) Replace(ctx context.Context, group, tag string, docs []Doc) error {
	if err := u.check(); err != nil {
		return err
	}
	if group == "" {
		return failf(ErrMalformedName, "分组名不能是空的")
	}
	return u.medium.inTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := u.medium.exec(ctx, tx,
			`INSERT INTO `+u.groups+` (name, tag) VALUES (?, ?)
			 ON CONFLICT (name) DO UPDATE SET tag = ?`, group, tag, tag); err != nil {
			return fmt.Errorf("datastore: 在单元 %q 里登记分组 %q 失败：%w", u.spec.Name, group, err)
		}
		// 先整组删干净再插。外键那道 CASCADE 在 SQLite 上要装配方在 DSN 上开
		// foreign_keys 才生效，所以这里不指望它，自己删。
		if _, err := u.medium.exec(ctx, tx,
			`DELETE FROM `+u.docs+` WHERE grp = ?`, group); err != nil {
			return fmt.Errorf("datastore: 清空单元 %q 的分组 %q 失败：%w", u.spec.Name, group, err)
		}
		return u.insertDocs(ctx, tx, group, docs)
	})
}

// insertDocs 把一批文档插进表，按 [insertChunkRows] 分块。
func (u *DocUnit) insertDocs(ctx context.Context, tx *sql.Tx, group string, docs []Doc) error {
	// 每行七个绑定参数，比日志集那边多，所以分块的行数按参数上限再收一道。
	const chunkRows = insertChunkRows / 2

	for start := 0; start < len(docs); start += chunkRows {
		end := min(start+chunkRows, len(docs))
		chunk := docs[start:end]

		var statement strings.Builder
		statement.WriteString(`INSERT INTO ` + u.docs +
			` (grp, seq, made_at, kind, facet, body, folded) VALUES `)
		arguments := make([]any, 0, len(chunk)*7)
		for index, doc := range chunk {
			if index > 0 {
				statement.WriteByte(',')
			}
			statement.WriteString("(?,?,?,?,?,?,?)")
			body := stripNul(doc.Body)
			arguments = append(arguments,
				group, doc.Seq, doc.At, doc.Kind, doc.Facet, body, fold(body))
		}
		if _, err := u.medium.exec(ctx, tx, statement.String(), arguments...); err != nil {
			return fmt.Errorf("datastore: 单元 %q 的分组 %q 写 seq %d..%d 那批失败：%w",
				u.spec.Name, group, chunk[0].Seq, chunk[len(chunk)-1].Seq, err)
		}
	}
	return nil
}

// DropGroup 整组丢掉，交回这一组原先在不在。
func (u *DocUnit) DropGroup(ctx context.Context, group string) (bool, error) {
	if err := u.check(); err != nil {
		return false, err
	}
	var existed bool
	err := u.medium.inTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := u.medium.exec(ctx, tx,
			`DELETE FROM `+u.docs+` WHERE grp = ?`, group); err != nil {
			return fmt.Errorf("datastore: 清空单元 %q 的分组 %q 失败：%w", u.spec.Name, group, err)
		}
		result, err := u.medium.exec(ctx, tx,
			`DELETE FROM `+u.groups+` WHERE name = ?`, group)
		if err != nil {
			return fmt.Errorf("datastore: 删单元 %q 的分组 %q 失败：%w", u.spec.Name, group, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("datastore: 删单元 %q 的分组 %q 失败：%w", u.spec.Name, group, err)
		}
		existed = affected > 0
		return nil
	})
	return existed, err
}

// ---- 读 ----

// Groups 列出这个单元里所有分组和它们各自的印记。
func (u *DocUnit) Groups(ctx context.Context) (map[string]string, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	rows, err := u.medium.query(ctx, u.medium.db, `SELECT name, tag FROM `+u.groups)
	if err != nil {
		return nil, fmt.Errorf("datastore: 列举单元 %q 的分组失败：%w", u.spec.Name, err)
	}
	defer func() { _ = rows.Close() }()

	tags := make(map[string]string)
	for rows.Next() {
		var name, tag string
		if err := rows.Scan(&name, &tag); err != nil {
			return nil, fmt.Errorf("datastore: 列举单元 %q 的分组失败：%w", u.spec.Name, err)
		}
		tags[name] = tag
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datastore: 列举单元 %q 的分组失败：%w", u.spec.Name, err)
	}
	return tags, nil
}

// Match 在这个单元里找出正文含有 [MatchRequest.Needle] 的那些文档。
//
// 比对前正文和要找的那段文字都会被折一遍（见 [fold]）：大小写不算数，连续空白
// 当一个空格算。除此之外是**逐字**比，不分词也不认通配符。
//
// 排序是「命中次数多的在前，同次数时正文短的在前，再同则新的在前」，末了拿组名和
// seq 兜底，好让同一次查询翻页翻出来的是同一个序。
func (u *DocUnit) Match(ctx context.Context, request MatchRequest) ([]MatchRow, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	if err := request.validate(); err != nil {
		return nil, err
	}

	needle := fold(stripNul(request.Needle))
	if needle == "" {
		return nil, failf(ErrMalformedName, "查找的文字不能是空的")
	}
	arguments := []any{needle, utf8.RuneCountInString(needle), "%" + likeEscape(needle) + "%"}

	// 命中次数用「删掉之后短了多少」除以「要找的那段有多长」算出来，数的是不重叠的
	// 那些次。两种方言的 length() 对文本都数字符，replace() 也都按字符替，所以这一
	// 句在两边算出同一个数。
	//
	// LIKE 那一句只是先把明显不可能的行筛掉，真正的判据是 hits > 0——SQLite 的 LIKE
	// 对 ASCII 是不分大小写的，而两边都折过了，所以它至多多放几行进来。
	var scored strings.Builder
	scored.WriteString(`SELECT grp, seq, made_at, kind, facet, body,
			length(folded) AS len,
			(length(folded) - length(replace(folded, ?, ''))) / ? AS hits
		FROM ` + u.docs + `
		WHERE folded LIKE ? ESCAPE '\'`)

	appendIn := func(column string, values []string, negate bool) {
		if len(values) == 0 {
			return
		}
		scored.WriteString(" AND " + column)
		if negate {
			scored.WriteString(" NOT")
		}
		scored.WriteString(" IN (")
		for index, value := range values {
			if index > 0 {
				scored.WriteByte(',')
			}
			scored.WriteByte('?')
			arguments = append(arguments, value)
		}
		scored.WriteByte(')')
	}
	appendIn("grp", request.Groups, false)
	appendIn("grp", request.ExcludeGroups, true)
	appendIn("kind", request.Kinds, false)
	appendIn("facet", request.Facets, false)

	appendBound := func(column, operator string, bound *int64) {
		if bound == nil {
			return
		}
		scored.WriteString(" AND " + column + " " + operator + " ?")
		arguments = append(arguments, *bound)
	}
	appendBound("seq", ">=", request.SeqFrom)
	appendBound("seq", "<=", request.SeqTo)
	appendBound("made_at", ">=", request.AtFrom)
	appendBound("made_at", "<=", request.AtTo)

	const tail = ` ORDER BY hits DESC, len ASC, made_at DESC, grp ASC, seq DESC LIMIT ? OFFSET ?`

	var statement string
	if request.BestPerGroup {
		// 每组只留最好的那一条这件事落在窗口函数上，不落在取回来之后的内存里：
		// LIMIT 是在库里切的，先切页再去重会让一页里挤满同一组，而那一组在下一页
		// 一条都不剩。
		statement = `SELECT grp, seq, made_at, kind, facet, body, len, hits FROM (
				SELECT grp, seq, made_at, kind, facet, body, len, hits,
					ROW_NUMBER() OVER (
						PARTITION BY grp
						ORDER BY hits DESC, len ASC, made_at DESC, seq DESC
					) AS rnk
				FROM (` + scored.String() + `) AS scored
				WHERE hits > 0
			) AS ranked WHERE rnk = 1` + tail
	} else {
		statement = `SELECT grp, seq, made_at, kind, facet, body, len, hits
			FROM (` + scored.String() + `) AS scored WHERE hits > 0` + tail
	}
	arguments = append(arguments, request.Limit, request.Offset)

	rows, err := u.medium.query(ctx, u.medium.db, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("datastore: 在单元 %q 里查找失败：%w", u.spec.Name, err)
	}
	defer func() { _ = rows.Close() }()

	var matched []MatchRow
	for rows.Next() {
		var row MatchRow
		var length int64
		if err := rows.Scan(&row.Group, &row.Seq, &row.At,
			&row.Kind, &row.Facet, &row.Body, &length, &row.Hits); err != nil {
			return nil, fmt.Errorf("datastore: 读单元 %q 的一条命中失败：%w", u.spec.Name, err)
		}
		matched = append(matched, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("datastore: 遍历单元 %q 的命中失败：%w", u.spec.Name, err)
	}
	return matched, nil
}

// Close 释放这个单元，并把单元名放回去，之后同名单元才重新开得起来。**幂等**。
//
// 这里不关连接池：连接池是整份介质的，见 [Config.DB]。
func (u *DocUnit) Close(context.Context) error {
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
