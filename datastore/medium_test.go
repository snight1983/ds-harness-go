// 本文件的作用：压一份介质本身——打开时那几条拒绝、实例标识的稳定、单元登记处
// 认不认得出「同名换形态」，以及关掉之后每条路都得响。

package datastore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// ---- 不动库的那一小块 ----

func Test没有连接池就打不开介质(t *testing.T) {
	if _, err := Open(t.Context(), Config{}); err == nil {
		t.Fatal("没有连接池就没有介质，该拒")
	}
}

// 命名空间会被拼进语句文本，所以它必须在碰库之前就先被卡住。传一个不为 nil 的
// 空池就够了：这一查发生得比第一次用池还早。
func Test不合法的命名空间名在动库之前就被拒(t *testing.T) {
	db := &sql.DB{}

	for _, namespace := range []string{"Public", "1st", "with-dash", "带中文", `a"b`, "a;b"} {
		if _, err := Open(t.Context(), Config{DB: db, Namespace: namespace}); !errors.Is(err, ErrMalformedName) {
			t.Errorf("命名空间 %q：该报 ErrMalformedName，实际 %v", namespace, err)
		}
	}
	// 超过 63 字节 Postgres 静默截断，两份本该互不相干的介质会写进同一批表。
	tooLong := strings.Repeat("a", Postgres().MaxIdentifierBytes()+1)
	if _, err := Open(t.Context(), Config{DB: db, Namespace: tooLong}); !errors.Is(err, ErrMalformedName) {
		t.Errorf("超长的命名空间该报 ErrMalformedName，实际 %v", err)
	}
}

func Test单元名和记录集形状在动库之前就被查完(t *testing.T) {
	medium := &Medium{dialect: Postgres()}

	if err := checkUnitName("Bad"); !errors.Is(err, ErrMalformedName) {
		t.Errorf("该报 ErrMalformedName，实际 %v", err)
	}
	for _, spec := range []RecordSpec{
		{Name: "ok", Version: -1, Tables: []string{"a"}},
		{Name: "ok", Version: 1, Tables: []string{"Bad"}},
		{Name: "ok", Version: 1, Tables: []string{"a", "a"}},
	} {
		if err := spec.validate(); !errors.Is(err, ErrMalformedName) {
			t.Errorf("%+v：该报 ErrMalformedName，实际 %v", spec, err)
		}
	}
	if err := (LogSpec{Name: "ok", Version: -1}).validate(); !errors.Is(err, ErrMalformedName) {
		t.Errorf("负版本号该报 ErrMalformedName，实际 %v", err)
	}
	// 长度那一查排在建表之前：一旦开始建表，中途因为第七张表名太长而失败会留下
	// 六张建好的表。
	if _, err := medium.physical(recordTableName(strings.Repeat("a", 40), strings.Repeat("b", 40))); err == nil {
		t.Error("拼出来超过上限的物理表名该被拒")
	}
}

func Test形态在错误信息里说的是人话(t *testing.T) {
	if got := shapeWord(kindRecords); got != "记录集" {
		t.Errorf("records 说成了 %q", got)
	}
	if got := shapeWord(kindLog); got != "日志集" {
		t.Errorf("log 说成了 %q", got)
	}
	// 认不出来的原样交出去，总比说成一个错的形态好。
	if got := shapeWord("未来的某种"); got != "未来的某种" {
		t.Errorf("认不出的形态被改写成了 %q", got)
	}
}

// ---- 要一个真的数据库的那一大块 ----

// 实例标识和布局同生共死：它一旦被别人读到过就不许再变，否则那些手里攥着旧令牌的
// 调用方会以为日志变过了。
func Test重开同一份介质拿回同一个实例标识(t *testing.T) {
	dsn, namespace := freshMedium(t)

	first, err := openMedium(t, dsn, namespace)
	if err != nil {
		t.Fatalf("打开介质失败：%v", err)
	}
	before := first.InstanceID()
	if before == "" {
		t.Fatal("实例标识是空的")
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("关介质失败：%v", err)
	}

	second, err := openMedium(t, dsn, namespace)
	if err != nil {
		t.Fatalf("重开介质失败：%v", err)
	}
	if after := second.InstanceID(); after != before {
		t.Fatalf("重开之后实例标识从 %q 变成了 %q", before, after)
	}
}

// 两份各自独立的介质各自盖各自的实例标识。撞上的话，令牌就不再是来源限定的了。
func Test两份介质的实例标识不一样(t *testing.T) {
	if newMedium(t).InstanceID() == newMedium(t).InstanceID() {
		t.Fatal("两份介质盖出了同一个实例标识")
	}
}

// 盖着别的布局号一律拒绝：这套布局还没发布过，没有迁移这一说，照着今天的形状
// 去读一份别的形状的表只会读出一堆看起来对的东西。
func Test盖着别的布局号的介质开不起来(t *testing.T) {
	dsn, namespace := freshMedium(t)

	medium, err := openMedium(t, dsn, namespace)
	if err != nil {
		t.Fatalf("打开介质失败：%v", err)
	}
	if _, err := medium.exec(t.Context(), medium.db,
		`UPDATE `+medium.qualify(metaTable)+` SET layout_version = ?`, LayoutVersion+1); err != nil {
		t.Fatalf("改布局号失败：%v", err)
	}

	if _, err := openMedium(t, dsn, namespace); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("布局号对不上该报 ErrVersionMismatch，实际 %v", err)
	}
}

// 两个句柄各自持有一份状态，后写的把先写的覆盖掉，而两次写都「成功」了。
func Test同名单元没关就开第二次会被拒(t *testing.T) {
	medium := newMedium(t)
	spec := RecordSpec{Name: "twice", Version: 1, Tables: []string{"a"}}

	unit, err := medium.OpenRecords(t.Context(), spec)
	if err != nil {
		t.Fatalf("第一次打开失败：%v", err)
	}
	if _, err := medium.OpenRecords(t.Context(), spec); !errors.Is(err, ErrAlreadyOpen) {
		t.Fatalf("该报 ErrAlreadyOpen，实际 %v", err)
	}

	// 关掉之后名字放回去，同名单元才重新开得起来。
	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("关单元失败：%v", err)
	}
	again, err := medium.OpenRecords(t.Context(), spec)
	if err != nil {
		t.Fatalf("关掉之后该重新开得起来：%v", err)
	}
	if err := again.Close(t.Context()); err != nil {
		t.Fatalf("再关一次失败：%v", err)
	}
}

// 同一个名字先当记录集开、后当日志集开，得在登记处当场撞出来，而不是撞在一张
// 形状对不上的物理表上。
func Test同一个单元名换一种形态会被拒(t *testing.T) {
	medium := newMedium(t)

	unit, err := medium.OpenRecords(t.Context(), RecordSpec{Name: "shape", Version: 1, Tables: []string{"a"}})
	if err != nil {
		t.Fatalf("开记录集失败：%v", err)
	}
	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("关记录集失败：%v", err)
	}

	if _, err := medium.OpenLog(t.Context(), LogSpec{Name: "shape", Version: 1}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("该报 ErrVersionMismatch，实际 %v", err)
	}
}

// 版本对不上只拒绝、不改任何东西：一次被拒的打开要是顺手动了介质，
// 「升级失败」就会连带把旧版本的数据毁掉。
func Test单元的版本对不上会被拒(t *testing.T) {
	medium := newMedium(t)

	unit, err := medium.OpenLog(t.Context(), LogSpec{Name: "versioned", Version: 1})
	if err != nil {
		t.Fatalf("开日志集失败：%v", err)
	}
	if err := unit.Append(t.Context(), AppendRequest{
		Stream: "s", Head: []byte(`{}`), EnsureStream: true, Entries: entriesFrom(0, 2),
	}); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("关日志集失败：%v", err)
	}

	if _, err := medium.OpenLog(t.Context(), LogSpec{Name: "versioned", Version: 2}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("该报 ErrVersionMismatch，实际 %v", err)
	}

	// 被拒的那一次不许动过任何东西。
	again, err := medium.OpenLog(t.Context(), LogSpec{Name: "versioned", Version: 1})
	if err != nil {
		t.Fatalf("按原版本重开失败：%v", err)
	}
	segment, err := again.Load(t.Context(), "s", 0)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if len(segment.Entries) != 2 {
		t.Fatalf("被拒的那次打开动过数据：现在只剩 %d 条", len(segment.Entries))
	}
}

// 关不掉的介质意味着连接池泄漏，所以 Close 不去问单元关没关；还开着的单元此后
// 每一次调用都撞上 ErrClosed。
func Test关掉介质之后单元每一条路都响(t *testing.T) {
	medium := newMedium(t)

	records, err := medium.OpenRecords(t.Context(), RecordSpec{
		Name: "orphan", Version: 1, Tables: []string{"a"}, Singleton: true,
	})
	if err != nil {
		t.Fatalf("开记录集失败：%v", err)
	}
	log, err := medium.OpenLog(t.Context(), LogSpec{Name: "orphan_log", Version: 1})
	if err != nil {
		t.Fatalf("开日志集失败：%v", err)
	}

	if err := medium.Close(t.Context()); err != nil {
		t.Fatalf("关介质失败：%v", err)
	}
	// 幂等：重复关是空操作，不是错。
	if err := medium.Close(t.Context()); err != nil {
		t.Fatalf("重复关介质该是空操作：%v", err)
	}
	// 上面已经关了，helper 登记的那次收尾不该再报错——它自己也是幂等的。

	if _, err := medium.OpenRecords(t.Context(), RecordSpec{Name: "after", Version: 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("关掉之后再开单元该报 ErrClosed，实际 %v", err)
	}
	if _, err := records.Snapshot(t.Context()); err == nil {
		t.Error("关掉之后读记录集该响")
	}
	if _, err := log.Load(t.Context(), "anyone", 0); err == nil {
		t.Error("关掉之后读日志集该响")
	}
}

// 池子那几个数留零表示照 database/sql 的缺省来，本包不替装配方猜。
func Test池子的配置设得上去(t *testing.T) {
	dsn := testDSN(t)

	db := openPool(t, dsn)
	medium, err := Open(t.Context(), Config{
		DB:        db,
		Dialect:   active.dialect,
		Namespace: freshNamespace(t, dsn),
		Pool:      PoolConfig{MaxOpenConns: 3, MaxIdleConns: 2},
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("打开介质失败：%v", err)
	}
	defer func() { _ = medium.Close(context.Background()) }()

	if got := db.Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("连接数上限是 %d，要的是 3", got)
	}
}
