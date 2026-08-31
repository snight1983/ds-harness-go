// 本文件的作用：介质上那条记录本身——身份怎么投影出来、校验拦住哪几种越界行、
// 以及这份声明真的能在存储上打开并往返一条记录。
//
// 源: packages/session/session-projection-cache/src/spec.ts

package projectioncache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ds-harness-go/session"
	"ds-harness-go/session/projection"
	"ds-harness-go/storage/domain"
)

func TestIdentityOfTakesOnlyTheTwoFieldsThatBindALifetime(t *testing.T) {
	t.Parallel()

	// 会话 id 故意**不在**身份里：身份要回答的正是「同一个 id 是不是同一段生命」，
	// 把 id 放进去这个问题就自问自答了。
	header := session.SessionHeader{
		Version:   1,
		ID:        "s1",
		CreatedAt: 1700000000000,
		Cwd:       "/work",
		Origin:    session.OriginSubagent,
	}
	if got := IdentityOf(header); got != (Identity{CreatedAt: 1700000000000, Cwd: "/work"}) {
		t.Fatalf("身份该只取建会话时刻和工作目录：%#v", got)
	}
}

func TestIdentityIsComparableSoMatchingIsOneEquals(t *testing.T) {
	t.Parallel()

	base := session.SessionHeader{ID: "s1", CreatedAt: 7, Cwd: "/work"}

	cases := map[string]struct {
		other session.SessionHeader
		same  bool
	}{
		"同一段生命":      {other: session.SessionHeader{ID: "s1", CreatedAt: 7, Cwd: "/work"}, same: true},
		"换了个 id 不影响": {other: session.SessionHeader{ID: "s2", CreatedAt: 7, Cwd: "/work"}, same: true},
		"重建过（时刻变了）":  {other: session.SessionHeader{ID: "s1", CreatedAt: 8, Cwd: "/work"}, same: false},
		"换了工作目录":     {other: session.SessionHeader{ID: "s1", CreatedAt: 7, Cwd: "/other"}, same: false},
		"没给工作目录":     {other: session.SessionHeader{ID: "s1", CreatedAt: 7}, same: false},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if same := IdentityOf(base) == IdentityOf(item.other); same != item.same {
				t.Fatalf("身份是否相同该是 %v，实际 %v", item.same, same)
			}
		})
	}
}

func TestIdentityGoesThroughJSONVerbatim(t *testing.T) {
	t.Parallel()

	// 字段名就是介质上的字段名，改了它等于把旧库读废。
	encoded, err := json.Marshal(Identity{CreatedAt: 7, Cwd: "/work"})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"createdAt":7,"cwd":"/work"}` {
		t.Fatalf("介质上的样子不对：%s", encoded)
	}

	// 没有工作目录时那个键整个缺席，和 DSH 的 `cwd?: string` 在介质上一模一样。
	encoded, err = json.Marshal(Identity{CreatedAt: 7})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"createdAt":7}` {
		t.Fatalf("空工作目录该整个缺席：%s", encoded)
	}
}

func TestValidateRecordAcceptsTheRowsThatAreInRange(t *testing.T) {
	t.Parallel()

	cases := map[string]Record{
		"一条正常的行": {
			Identity: Identity{CreatedAt: 7, Cwd: "/work"},
			Rows:     projection.Checkpoint{"count": countRow(t, 0, 3, 4)},
		},
		"水位负一（一条都没折过）": {
			Rows: projection.Checkpoint{"count": countRow(t, 0, -1, 0)},
		},
		"一行都没有": {
			Identity: Identity{CreatedAt: 7},
		},
		"建会话时刻为零": {},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateRecord(item); err != nil {
				t.Fatalf("该收下：%v", err)
			}
		})
	}
}

func TestValidateRecordRejectsRowsOutsideTheirRange(t *testing.T) {
	t.Parallel()

	// 这四条守的是 [projection.CheckpointRow] 自己的取值范围。读侧尤其要紧：
	// 介质上的字节可能来自任何一个历史构建，而一条越界的行会被恢复路径当成正常行用。
	cases := map[string]struct {
		record Record
		want   string
	}{
		"建会话时刻是负数": {
			record: Record{Identity: Identity{CreatedAt: -1}},
			want:   "不能是负数",
		},
		"状态版本号是负数": {
			record: Record{Rows: projection.Checkpoint{"count": countRow(t, -1, 0, 0)}},
			want:   "状态版本号",
		},
		"水位小于负一": {
			record: Record{Rows: projection.Checkpoint{"count": countRow(t, 0, -2, 0)}},
			want:   "水位",
		},
		"状态不是合法 JSON": {
			record: Record{Rows: projection.Checkpoint{
				"count": {Ver: 0, Seq: 0, Val: json.RawMessage(`{`)},
			}},
			want: "不是一段合法的 JSON",
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRecord(item.record)
			if err == nil {
				t.Fatalf("该拒掉")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("错误里该说清是哪一条越界了，实际是 %q", err.Error())
			}
		})
	}
}

func TestValidateRecordNamesTheOffendingKey(t *testing.T) {
	t.Parallel()

	// 一条记录里有好几个键，不点名的话拿到错误的人还得自己去猜是哪个单元写坏的。
	err := ValidateRecord(Record{Rows: projection.Checkpoint{"坏掉的那个": countRow(t, 0, -5, 0)}})
	if err == nil || !strings.Contains(err.Error(), "坏掉的那个") {
		t.Fatalf("错误里该点出投影键：%v", err)
	}
}

func TestSpecDeclaresTheOneTableAtTheCurrentVersion(t *testing.T) {
	t.Parallel()

	spec := Spec()
	if spec.Name != DomainName || spec.Version != DomainVersion {
		t.Fatalf("域名或版本不对：%q %d", spec.Name, spec.Version)
	}
	if len(spec.Tables) != 1 || spec.Tables[0].Name() != TableName {
		t.Fatalf("该只声明一张 %q 表：%#v", TableName, spec.Tables)
	}
	if spec.Global != nil {
		t.Fatalf("这个域没有全局槽")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("声明本身该立得住：%v", err)
	}
}

func TestSpecIsAFreshValueEveryCallSoNoOneCanEditEveryoneElses(t *testing.T) {
	t.Parallel()

	// 它是函数不是包级变量，正因为 [domain.Spec] 里带一个切片：一个包级变量
	// 会让任何一个拿到它的人改到所有人共用的那一份。
	first, second := Spec(), Spec()
	first.Tables = nil
	if len(second.Tables) != 1 {
		t.Fatalf("改到一份声明不该影响另一份：%#v", second.Tables)
	}
}

func TestRecordRoundTripsThroughTheDomain(t *testing.T) {
	t.Parallel()

	// 这一条把声明、校验和记录类型串起来跑一遍：单独看每一个都对不出问题来。
	opened := openDomain(t, Spec())
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		t.Fatalf("取表不该失败：%v", err)
	}

	want := Record{
		Identity: Identity{CreatedAt: 7, Cwd: "/work"},
		Rows:     projection.Checkpoint{"count": countRow(t, 0, 3, 4)},
	}
	if err := table.Put(context.Background(), "s1", want); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	got, ok, err := table.Get("s1")
	if err != nil || !ok {
		t.Fatalf("该读得回来：%v %v", ok, err)
	}
	if got.Identity != want.Identity {
		t.Fatalf("身份对不上：%#v", got.Identity)
	}
	row := got.Rows["count"]
	if row.Ver != 0 || row.Seq != 3 || string(row.Val) != `{"count":4}` {
		t.Fatalf("行对不上：%#v %s", row, row.Val)
	}
}

func TestDomainRefusesToStoreAnInvalidRecord(t *testing.T) {
	t.Parallel()

	// 校验挂在声明上，所以写路径上也跑得到——一条越界的行进不了介质，
	// 而不是等到下次重启、整个域因为它打不开。
	opened := openDomain(t, Spec())
	table, err := domain.TableOf[Record](opened, TableName)
	if err != nil {
		t.Fatalf("取表不该失败：%v", err)
	}

	bad := Record{Rows: projection.Checkpoint{"count": countRow(t, 0, -9, 0)}}
	if err := table.Put(context.Background(), "s1", bad); err == nil {
		t.Fatalf("越界的行不该写进去")
	}
}
