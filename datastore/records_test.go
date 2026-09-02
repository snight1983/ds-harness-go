// 本文件的作用：压记录集——写读一个来回、声明过而空的表在不在场、删的幂等、
// 单例槽，以及介质上的值坏掉时报的是「坏介质」。

package datastore

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPut之后读得回来(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "round", Version: 1, Tables: []string{"a", "b"}})

	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"x":2}`)); err != nil {
		t.Fatalf("覆盖失败：%v", err)
	}

	snapshot, err := unit.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	if got := string(snapshot.Tables["a"]["k"]); got != `{"x":2}` {
		t.Fatalf("读回来的是 %s，要的是 {\"x\":2}", got)
	}
	// 声明过而一条记录都没有的表必须**在场且为空**：缺席会让「这张表还没建出来」
	// 和「这张表是空的」长得一模一样。
	empty, present := snapshot.Tables["b"]
	if !present {
		t.Fatal("声明过的空表在快照里缺席了")
	}
	if len(empty) != 0 {
		t.Fatalf("空表里有 %d 条", len(empty))
	}
	// 没声明单例槽时它是 nil，不是一段空 JSON。
	if snapshot.Singleton != nil {
		t.Errorf("没声明的单例槽读出了 %s", snapshot.Singleton)
	}
}

// 删是幂等的：键不在、甚至这张表压根没声明过，都是空操作。报错的话，同一条调用
// 在不同介质上一个响一个不响。
func TestDelete是幂等的(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "deleting", Version: 1, Tables: []string{"a"}})

	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`)); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	for range 2 {
		if err := unit.Delete(t.Context(), "a", "k"); err != nil {
			t.Fatalf("删失败：%v", err)
		}
	}
	if err := unit.Delete(t.Context(), "没声明过", "k"); err != nil {
		t.Fatalf("删一张没声明过的表该是空操作：%v", err)
	}

	snapshot, err := unit.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	if len(snapshot.Tables["a"]) != 0 {
		t.Fatalf("删完还剩 %d 条", len(snapshot.Tables["a"]))
	}
}

// 写一张没声明过的表是调用方拼错了名字，不是一次空操作——写丢了没人看得见。
func Test往没声明过的表里写会被拒(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "undeclared", Version: 1, Tables: []string{"a"}})

	if err := unit.Put(t.Context(), "b", "k", json.RawMessage(`1`)); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("该报 ErrMalformedName，实际 %v", err)
	}
}

func Test单例槽写读一个来回(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "single", Version: 1, Tables: []string{"a"}, Singleton: true})

	// 声明了槽但一次都没写过——全新单元的正常状态，不是介质坏了。
	snapshot, err := unit.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	if snapshot.Singleton != nil {
		t.Fatalf("没写过的单例槽读出了 %s", snapshot.Singleton)
	}

	if err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatalf("盖单例槽失败：%v", err)
	}
	if err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":2}`)); err != nil {
		t.Fatalf("重盖单例槽失败：%v", err)
	}
	snapshot, err = unit.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	if got := string(snapshot.Singleton); got != `{"v":2}` {
		t.Fatalf("单例槽读回来是 %s，要的是 {\"v\":2}", got)
	}
}

func Test没声明单例槽就写单例槽会被拒(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "no_single", Version: 1, Tables: []string{"a"}})

	if err := unit.SetSingleton(t.Context(), json.RawMessage(`{}`)); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("该报 ErrMalformedName，实际 %v", err)
	}
}

// 值那一列是 TEXT，库不替我们验 JSON。不验的话一段坏文本会原样变成
// json.RawMessage 交出去，然后在某个离这里很远的 Unmarshal 处炸掉。
func Test介质上的值不是合法JSON时报坏介质(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "rotten", Version: 1, Tables: []string{"a"}, Singleton: true})

	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	if err := unit.SetSingleton(t.Context(), json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("盖单例槽失败：%v", err)
	}

	// 绕过本包直接把介质弄坏——写那一路自己验，所以只能从这里进去。
	medium := unit.medium
	if _, err := medium.exec(t.Context(), medium.db,
		`UPDATE `+medium.qualify(unit.physical["a"])+` SET value = ? WHERE key = ?`,
		`{不是 JSON`, "k"); err != nil {
		t.Fatalf("弄坏记录失败：%v", err)
	}
	if _, err := unit.Snapshot(t.Context()); !errors.Is(err, ErrMalformedMedium) {
		t.Fatalf("该报 ErrMalformedMedium，实际 %v", err)
	}

	if _, err := medium.exec(t.Context(), medium.db,
		`UPDATE `+medium.qualify(unit.physical["a"])+` SET value = ? WHERE key = ?`,
		`{"ok":true}`, "k"); err != nil {
		t.Fatalf("改回记录失败：%v", err)
	}
	if _, err := medium.exec(t.Context(), medium.db,
		`UPDATE `+medium.qualify(singletonsTable)+` SET value = ? WHERE unit = ?`,
		`{不是 JSON`, unit.Name()); err != nil {
		t.Fatalf("弄坏单例槽失败：%v", err)
	}
	if _, err := unit.Snapshot(t.Context()); !errors.Is(err, ErrMalformedMedium) {
		t.Fatalf("单例槽坏了该报 ErrMalformedMedium，实际 %v", err)
	}
}

// 写那一路自己先验一遍：一段坏文本落进介质之后，每一次读都会撞上它。
func Test写一段不是JSON的值会被拒(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "invalid", Version: 1, Tables: []string{"a"}, Singleton: true})

	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{不是 JSON`)); !errors.Is(err, ErrMalformedName) {
		t.Errorf("该报 ErrMalformedName，实际 %v", err)
	}
	if err := unit.SetSingleton(t.Context(), json.RawMessage(`{不是 JSON`)); !errors.Is(err, ErrMalformedName) {
		t.Errorf("该报 ErrMalformedName，实际 %v", err)
	}
}

// 值那一列是 TEXT 不是 jsonb：encoding/json 把 NUL 编成一个转义序列，而 jsonb
// 会当场拒掉它。模型输出里出现一个 NUL 就够了。
func Test带NUL的值存得下也读得回(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "nul", Version: 1, Tables: []string{"a"}})

	encoded, err := json.Marshal(map[string]string{"text": "前\x00后"})
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if err := unit.Put(t.Context(), "a", "k", encoded); err != nil {
		t.Fatalf("写带 NUL 的值失败：%v", err)
	}

	snapshot, err := unit.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(snapshot.Tables["a"]["k"], &decoded); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if decoded["text"] != "前\x00后" {
		t.Fatalf("读回来的值变了：%q", decoded["text"])
	}
}

func Test关掉的记录集每一条路都响(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "closing", Version: 1, Tables: []string{"a"}, Singleton: true})

	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("关单元失败：%v", err)
	}
	// 幂等：重复关是空操作。
	if err := unit.Close(t.Context()); err != nil {
		t.Fatalf("重复关该是空操作：%v", err)
	}

	if _, err := unit.Snapshot(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("读该报 ErrClosed，实际 %v", err)
	}
	if err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`)); !errors.Is(err, ErrClosed) {
		t.Errorf("写该报 ErrClosed，实际 %v", err)
	}
	if err := unit.Delete(t.Context(), "a", "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("删该报 ErrClosed，实际 %v", err)
	}
	if err := unit.SetSingleton(t.Context(), json.RawMessage(`1`)); !errors.Is(err, ErrClosed) {
		t.Errorf("盖单例槽该报 ErrClosed，实际 %v", err)
	}
}

// 一个单元名底下的两种形态不该撞表名——登记处已经拦住了同名换形态，但物理层
// 不该依赖那一层的正确性。
func Test两种形态的物理表名不撞(t *testing.T) {
	if recordTableName("u", "streams") == logStreamsTableName("u") {
		t.Error("记录集和日志集拼出了同一个物理表名")
	}
	if recordTableName("u", "entries") == logEntriesTableName("u") {
		t.Error("记录集和日志集拼出了同一个物理表名")
	}
}
