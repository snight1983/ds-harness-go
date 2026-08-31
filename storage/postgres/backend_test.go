// 本文件的作用：把这个后端放进共用一致性测试里跑一遍，再补上那几条
// 共用套件按设计不管、只能由各后端自己压的用例。
//
// # 为什么覆盖率低于 DESIGN.md 第九节那条 ≥99%
//
// 这个包里除了几个纯字符串函数，其余每一行都要一个**真的 Postgres** 才执行得到。
// 没有 DSN 时下面那些用例整批跳过，于是覆盖率只剩下不动库的那一小块。
// 这不是「测试没写」，是「测试跑不起来」——设 DSH_POSTGRES_DSN 之后同一批用例
// 会全部执行。用 sqlmock 之类的东西把行数刷上去是反效果：那验的是
// 「我拼出了我以为我会拼的那句 SQL」，而这个包里真正会出事的地方
// （ON CONFLICT 的语义、咨询锁下的并发建表、标识符截断）恰恰是假库看不见的。

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	// 驱动只在测试里挂上来。库本身只用 database/sql，理由见包文档。
	_ "github.com/lib/pq"

	"ds-harness-go/storage"
	"ds-harness-go/storage/storagetest"
)

// dsnEnv 是指向一个可用 Postgres 的连接串所在的环境变量。
//
// 没设就跳过，而不是失败：一个跑不起来的依赖不该让整个仓库的 go test 变红，
// 否则真正的失败会淹没在一片「连不上库」里。
const dsnEnv = "DSH_POSTGRES_DSN"

// schemaCounter 保证同一次 go test 里每份介质拿到不同的 schema 名。
var schemaCounter atomic.Int64

// hasCode 判断一条错误的分类是不是这一个。
//
// storagetest 里有一个同名的私有辅助函数，但它不导出，这里只能再写一遍。
// 分派看的是 [storage.Error.Code]，不是 Message 里的字。
func hasCode(err error, code storage.ErrorCode) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == code
}

// requireDSN 取出连接串，没有就跳过这条用例。
func requireDSN(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("没有设 %s，跳过：这个后端的每一行都要一个真的 Postgres 才执行得到", dsnEnv)
	}
	return dsn
}

// freshSchema 造一个本次测试专用的 schema 名，并登记好清理。
//
// 一个 schema 就是一份介质（见 [Config.Schema]），所以「每条用例一份全新介质」
// 在这里就是「每条用例一个新 schema」。共用介质的话，一条用例写下的数据
// 会变成下一条用例的隐含前提。
func freshSchema(t *testing.T, dsn string) string {
	t.Helper()

	schema := fmt.Sprintf("t_%d_%d", os.Getpid(), schemaCounter.Add(1))
	t.Cleanup(func() {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Errorf("清理 schema %q 时连库失败：%v", schema, err)
			return
		}
		defer func() { _ = db.Close() }()

		// schema 名是本文件自己拼的，只含小写字母、数字和下划线。
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); err != nil {
			t.Errorf("清理 schema %q 失败：%v", schema, err)
		}
	})
	return schema
}

// openOn 在指定 schema 上开一个后端，连接池是新的一条。
//
// 每次都新开一个池，是因为 [Config.DB] 归后端所有：上一个后端 Close 时
// 已经把它那条池关掉了，复用就会拿到一个关掉的池。
func openOn(ctx context.Context, dsn, schema string) (*Backend, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	backend, err := Open(ctx, Config{DB: db, Schema: schema})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return backend, nil
}

func TestPostgres后端满足键值契约(t *testing.T) {
	dsn := requireDSN(t)

	storagetest.RunKVBackendContract(t, "postgres 后端", func(t *testing.T) storagetest.Harness {
		ctx := context.Background()
		schema := freshSchema(t, dsn)

		backend, err := openOn(ctx, dsn, schema)
		if err != nil {
			t.Fatalf("打开后端失败：%v", err)
		}
		return storagetest.Harness{
			Backend: backend,
			Reopen: func() (storage.Backend, error) {
				return openOn(ctx, dsn, schema)
			},
		}
	})
}

func Test没有连接池就建不出后端(t *testing.T) {
	_, err := Open(context.Background(), Config{})
	if !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}
}

func Test不合法的schema名在动库之前就被拒(t *testing.T) {
	// 故意不给 DSN 也不给池：这一查必须发生在碰库之前，所以它连
	// Config.DB 是不是能用都轮不到判断。传一个不为 nil 的空池即可。
	db := &sql.DB{}
	for _, schema := range []string{"Public", "1st", "with-dash", "带中文"} {
		_, err := Open(context.Background(), Config{DB: db, Schema: schema})
		if !hasCode(err, storage.CodeMalformedMedium) {
			t.Errorf("schema 名 %q：想要 %v，拿到 %v", schema, storage.CodeMalformedMedium, err)
		}
	}
}

func Test物理表名超过63字节被拒而不是被截断(t *testing.T) {
	unit := strings.Repeat("a", 40)
	table := strings.Repeat("b", 40)

	_, err := recordTableName(unit, table)
	if !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}

	// 边界上那一个必须过：卡在 63 上而不是 62，否则合法的名字会被误杀。
	name, err := recordTableName(strings.Repeat("a", 30), strings.Repeat("b", 30))
	if err != nil {
		t.Fatalf("63 字节的名字不该被拒：%v", err)
	}
	if len(name) != maxIdentifierLength {
		t.Fatalf("这个用例想测的是边界，但名字只有 %d 字节", len(name))
	}
}

func Test没声明全局槽就写全局槽会被拒(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	schema := freshSchema(t, dsn)

	backend, err := openOn(ctx, dsn, schema)
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
	if err := unit.SetGlobal(ctx, json.RawMessage(`{"x":1}`)); !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}
}

func Test介质上的值不是合法JSON时报坏介质(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	schema := freshSchema(t, dsn)

	backend, err := openOn(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("打开后端失败：%v", err)
	}
	defer func() { _ = backend.Close(ctx) }()

	descriptor := storage.KVUnitDescriptor{Name: "rotten", Version: 1, Tables: []string{"records"}}
	unit, err := backend.KV().Open(ctx, descriptor)
	if err != nil {
		t.Fatalf("打开单元失败：%v", err)
	}
	if err := unit.PutRecord(ctx, "records", "k", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("写记录失败：%v", err)
	}

	// 绕过本包直接把介质弄坏。这一条只能各后端自己压：「弄坏」在每个后端上
	// 是不同的动作，共用套件里没法表达。
	table, err := recordTableName(descriptor.Name, "records")
	if err != nil {
		t.Fatalf("拼表名失败：%v", err)
	}
	if _, err := backend.db.ExecContext(ctx,
		`UPDATE `+qualify(schema, table)+` SET value = $1 WHERE key = $2`,
		`{not json`, "k"); err != nil {
		t.Fatalf("弄坏介质失败：%v", err)
	}

	if _, err := unit.LoadAll(ctx); !hasCode(err, storage.CodeMalformedMedium) {
		t.Fatalf("想要 %v，拿到 %v", storage.CodeMalformedMedium, err)
	}
}

func Test带NUL的值存得下也读得回(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	schema := freshSchema(t, dsn)

	backend, err := openOn(ctx, dsn, schema)
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

	// 这一条压的是「值那一列为什么是 TEXT 不是 jsonb」：encoding/json 把 NUL
	// 编码成 U+0000 那个转义，而 jsonb 会当场拒掉它。模型输出里出现一个 NUL 就够了。
	encoded, err := json.Marshal(map[string]string{"text": "前\x00后"})
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if err := unit.PutRecord(ctx, "records", "k", encoded); err != nil {
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
