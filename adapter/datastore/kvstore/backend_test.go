// 本文件的作用：把这个后端放进共用一致性测试里跑一遍，再补上那几条共用套件
// 按设计不管、只能由这一侧自己压的用例——主要是词汇翻译。
//
// 这一轮跑在哪种库上由 [dbtest] 定：缺省 SQLite，设了 DSH_POSTGRES_DSN 就整批改跑
// Postgres。所以本包这批用例任何时候都跑得起来，不再有「没有 DSN 就整批跳过」
// 那个洞——理由见那个包的包文档。

package kvstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/adapter/datastore/internal/dbtest"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

// hasCode 判断一条错误的分类是不是这一个。
//
// storagetest 里有一个同名的私有辅助函数，但它不导出，这里只能再写一遍。
// 分派看的是 [storage.Error.Code]，不是 Message 里的字。
func hasCode(err error, code storage.ErrorCode) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == code
}

// freshNamespace 造一个本次测试专用的命名空间名，并登记好清理。
func freshNamespace(t *testing.T, dsn string) string {
	t.Helper()

	return dbtest.Namespace(t, "kv_test", dsn)
}

// openOn 在指定命名空间上开一个后端，连接池是新的一条。
//
// 每次都新开一个池，是因为连接池归后端所有：上一个后端 Close 时已经把它那条池
// 关掉了，复用就会拿到一个关掉的池。
//
// 收 t 是为了让开池那一步失败时当场停：连不上库不是这批用例要压的东西。
func openOn(t *testing.T, ctx context.Context, dsn, namespace string) (*Backend, error) {
	t.Helper()

	config, db := dbtest.Config(t, dsn, namespace)
	backend, err := Open(ctx, config)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return backend, nil
}

func Test这个后端满足键值契约(t *testing.T) {
	dsn := dbtest.DSN()

	storagetest.RunKVBackendContract(t, "datastore 键值后端", func(t *testing.T) storagetest.Harness {
		ctx := context.Background()
		namespace := freshNamespace(t, dsn)

		backend, err := openOn(t, ctx, dsn, namespace)
		if err != nil {
			t.Fatalf("打开后端失败：%v", err)
		}
		return storagetest.Harness{
			Backend: backend,
			Reopen: func() (storage.Backend, error) {
				return openOn(t, ctx, dsn, namespace)
			},
		}
	})
}

func Test没有连接池就建不出后端(t *testing.T) {
	if _, err := Open(context.Background(), datastore.Config{}); err == nil {
		t.Fatal("没有连接池就没有介质，该拒")
	}
}

// 命名空间会被拼进语句文本，所以它必须在碰库之前就先被卡住。传一个不为 nil 的
// 空池就够了：这一查发生得比第一次用池还早。
func Test不合法的命名空间名在动库之前就被拒(t *testing.T) {
	db := &sql.DB{}
	for _, namespace := range []string{"Public", "1st", "with-dash", "带中文"} {
		_, err := Open(context.Background(), datastore.Config{DB: db, Namespace: namespace})
		if !hasCode(err, storage.CodeMalformedMedium) {
			t.Errorf("命名空间 %q：想要 %v，拿到 %v", namespace, storage.CodeMalformedMedium, err)
		}
	}
}

// 这一层只翻两样东西，其中一样是词汇。翻错了的表现是调用方按错误的分类去做处置。
func Test词汇按分类翻过来(t *testing.T) {
	cases := []struct {
		err  error
		code storage.ErrorCode
	}{
		{datastore.ErrVersionMismatch, storage.CodeVersionMismatch},
		{datastore.ErrClosed, storage.CodeClosed},
		{datastore.ErrStaleRevision, storage.CodeStaleRevision},
		{datastore.ErrMalformedName, storage.CodeMalformedMedium},
		{datastore.ErrMalformedMedium, storage.CodeMalformedMedium},
		{datastore.ErrAlreadyOpen, storage.CodeMalformedMedium},
	}
	for _, item := range cases {
		translated := translate(fmt.Errorf("外面裹一层：%w", item.err))
		if !hasCode(translated, item.code) {
			t.Errorf("%v：想要 %v，拿到 %v", item.err, item.code, translated)
		}
		// 裹完还得认得出原来那条：调用方要靠 errors.Is 分派更细的情况。
		if !errors.Is(translated, item.err) {
			t.Errorf("%v：翻完之后原来那条认不出来了", item.err)
		}
	}

	if translate(nil) != nil {
		t.Error("nil 被翻成了一条错误")
	}
}

// 翻不出来的原样往上冒。连不上库、事务被中止、死锁——这些在 storage.ErrorCode 里
// 本来就没有位置，硬塞进 CodeMalformedMedium 会让调用方以为介质坏了，
// 然后去做一次它根本不该做的修复。
func Test翻不出来的错误原样往上冒(t *testing.T) {
	original := errors.New("连不上库")

	translated := translate(original)
	if translated != original {
		t.Fatalf("原样往上冒的那条被改成了 %v", translated)
	}
	var typed *storage.Error
	if errors.As(translated, &typed) {
		t.Errorf("被硬塞进了分类 %v", typed.Code)
	}
}

func Test没声明全局槽就写全局槽会被拒(t *testing.T) {
	dsn := dbtest.DSN()
	ctx := context.Background()

	backend, err := openOn(t, ctx, dsn, freshNamespace(t, dsn))
	if err != nil {
		t.Fatalf("打开后端失败：%v", err)
	}
	defer func() { _ = backend.Close(ctx) }()

	unit, err := backend.KV().Open(ctx, storage.KVUnitDescriptor{
		Name: "no_global", Version: 1, Tables: []string{"only"},
	})
	if err != nil {
		t.Fatalf("打开单元失败：%v", err)
	}
	if _, err := unit.SetGlobal(ctx, json.RawMessage(`{"x":1}`), nil); !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}
}

// 后端关掉时会把还开着的单元一起收掉。不收的话，那些单元会挂在一份已经释放的
// 介质上，而后端那一遍已经走过去了。
func Test关后端把还开着的单元一起收掉(t *testing.T) {
	dsn := dbtest.DSN()
	ctx := context.Background()

	backend, err := openOn(t, ctx, dsn, freshNamespace(t, dsn))
	if err != nil {
		t.Fatalf("打开后端失败：%v", err)
	}
	unit, err := backend.KV().Open(ctx, storage.KVUnitDescriptor{
		Name: "orphan", Version: 1, Tables: []string{"a"},
	})
	if err != nil {
		t.Fatalf("打开单元失败：%v", err)
	}

	if err := backend.Close(ctx); err != nil {
		t.Fatalf("关后端失败：%v", err)
	}
	// 幂等：重复关是空操作。
	if err := backend.Close(ctx); err != nil {
		t.Fatalf("重复关后端该是空操作：%v", err)
	}

	if _, err := unit.LoadAll(ctx); !hasCode(err, storage.CodeClosed) {
		t.Errorf("单元该报 %v，拿到 %v", storage.CodeClosed, err)
	}
	if _, err := backend.KV().Open(ctx, storage.KVUnitDescriptor{
		Name: "after", Version: 1, Tables: []string{"a"},
	}); !hasCode(err, storage.CodeClosed) {
		t.Errorf("关掉之后再开单元该报 %v，拿到 %v", storage.CodeClosed, err)
	}
}

// 绕过本包直接把介质弄坏。这一条只能这一侧自己压：「弄坏」在每个后端上是不同的
// 动作，共用套件里没法表达。
func Test介质上的值不是合法JSON时报坏介质(t *testing.T) {
	dsn := dbtest.DSN()
	ctx := context.Background()
	namespace := freshNamespace(t, dsn)

	backend, err := openOn(t, ctx, dsn, namespace)
	if err != nil {
		t.Fatalf("打开后端失败：%v", err)
	}
	defer func() { _ = backend.Close(ctx) }()

	descriptor := storage.KVUnitDescriptor{Name: "rotten", Version: 1, Tables: []string{"records"}}
	unit, err := backend.KV().Open(ctx, descriptor)
	if err != nil {
		t.Fatalf("打开单元失败：%v", err)
	}
	if _, err := unit.PutRecord(ctx, "records", "k", json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatalf("写记录失败：%v", err)
	}

	db := dbtest.Pool(t, dsn)
	defer func() { _ = db.Close() }()
	// 物理表名的拼法在 datastore 那一层，这里照着它拼一遍——这条用例本来就是
	// 「绕过所有抽象直接动介质」。限定和占位符都借这一轮的方言，否则这条用例
	// 只在 Postgres 那一轮跑得起来，而它压的东西和方言无关。
	dialect := dbtest.Dialect()
	table := dialect.Qualify(namespace, "r_"+descriptor.Name+"_records")
	if _, err := db.ExecContext(ctx,
		dialect.Rebind(`UPDATE `+table+` SET value = ? WHERE key = ?`),
		`{不是 JSON`, "k"); err != nil {
		t.Fatalf("弄坏介质失败：%v", err)
	}

	if _, err := unit.LoadAll(ctx); !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}
}

// 值那一列是 TEXT 不是 jsonb：encoding/json 把 NUL 编成一个转义序列，而 jsonb
// 会当场拒掉它。模型输出里出现一个 NUL 就够了。
func Test带NUL的值存得下也读得回(t *testing.T) {
	dsn := dbtest.DSN()
	ctx := context.Background()

	backend, err := openOn(t, ctx, dsn, freshNamespace(t, dsn))
	if err != nil {
		t.Fatalf("打开后端失败：%v", err)
	}
	defer func() { _ = backend.Close(ctx) }()

	unit, err := backend.KV().Open(ctx, storage.KVUnitDescriptor{
		Name: "nul_values", Version: 1, Tables: []string{"records"},
	})
	if err != nil {
		t.Fatalf("打开单元失败：%v", err)
	}

	encoded, err := json.Marshal(map[string]string{"text": "前\x00后"})
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if _, err := unit.PutRecord(ctx, "records", "k", encoded, nil); err != nil {
		t.Fatalf("写带 NUL 的值失败：%v", err)
	}

	snapshot, err := unit.LoadAll(ctx)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(snapshot.Tables["records"]["k"], &decoded); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if decoded["text"] != "前\x00后" {
		t.Fatalf("读回来的值变了：%q", decoded["text"])
	}
}
