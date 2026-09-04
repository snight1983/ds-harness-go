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

	first, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"x":1}`), nil)
	if err != nil {
		t.Fatalf("写失败：%v", err)
	}
	second, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"x":2}`), nil)
	if err != nil {
		t.Fatalf("覆盖失败：%v", err)
	}
	// 每一次成功的写都得换一个令牌，否则别的副本的前置条件会把「改过了」看成「没人动过」。
	if first == second {
		t.Fatalf("覆盖之后令牌没变，还是 %q", first)
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

	if _, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`), nil); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	// 第一次删得掉，第二次是空操作——「删之前在不在」由这个返回值回答。
	existed, err := unit.Delete(t.Context(), "a", "k", nil)
	if err != nil || !existed {
		t.Fatalf("第一次删该交回 true：existed=%v err=%v", existed, err)
	}
	existed, err = unit.Delete(t.Context(), "a", "k", nil)
	if err != nil || existed {
		t.Fatalf("第二次删该交回 false：existed=%v err=%v", existed, err)
	}
	if _, err := unit.Delete(t.Context(), "没声明过", "k", nil); err != nil {
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

	if _, err := unit.Put(t.Context(), "b", "k", json.RawMessage(`1`), nil); !errors.Is(err, ErrMalformedName) {
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

	first, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":1}`), nil)
	if err != nil {
		t.Fatalf("盖单例槽失败：%v", err)
	}
	second, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":2}`), nil)
	if err != nil {
		t.Fatalf("重盖单例槽失败：%v", err)
	}
	if first == second {
		t.Fatalf("重盖之后令牌没变，还是 %q", first)
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

	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{}`), nil); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("该报 ErrMalformedName，实际 %v", err)
	}
}

// 值那一列是 TEXT，库不替我们验 JSON。不验的话一段坏文本会原样变成
// json.RawMessage 交出去，然后在某个离这里很远的 Unmarshal 处炸掉。
func Test介质上的值不是合法JSON时报坏介质(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "rotten", Version: 1, Tables: []string{"a"}, Singleton: true})

	if _, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatalf("写失败：%v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"ok":true}`), nil); err != nil {
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

	if _, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{不是 JSON`), nil); !errors.Is(err, ErrMalformedName) {
		t.Errorf("该报 ErrMalformedName，实际 %v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{不是 JSON`), nil); !errors.Is(err, ErrMalformedName) {
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
	if _, err := unit.Put(t.Context(), "a", "k", encoded, nil); err != nil {
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
	if _, _, _, err := unit.Read(t.Context(), "a", "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("单条读该报 ErrClosed，实际 %v", err)
	}
	if _, _, err := unit.ReadSingleton(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("读单例槽该报 ErrClosed，实际 %v", err)
	}
	if _, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("写该报 ErrClosed，实际 %v", err)
	}
	if _, err := unit.Delete(t.Context(), "a", "k", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("删该报 ErrClosed，实际 %v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`1`), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("盖单例槽该报 ErrClosed，实际 %v", err)
	}
}

// 单条读是多副本下唯一读得到「别人刚写的那一版」的路：它必须穿到介质，
// 并且把令牌一起交出来。
func Test单条读给出值和令牌(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "reading", Version: 1, Tables: []string{"a"}})

	// 不存在不是错误——调用方问的就是「在不在」。
	value, revision, found, err := unit.Read(t.Context(), "a", "k")
	if err != nil || found || value != nil || revision != "" {
		t.Fatalf("没写过的键该是不存在：value=%s revision=%q found=%v err=%v",
			value, revision, found, err)
	}
	// 没声明过的表同样是「不在」，不是错误。
	if _, _, found, err = unit.Read(t.Context(), "没声明过", "k"); err != nil || found {
		t.Fatalf("没声明过的表该是不存在：found=%v err=%v", found, err)
	}

	written, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`{"x":1}`), nil)
	if err != nil {
		t.Fatalf("写失败：%v", err)
	}
	value, revision, found, err = unit.Read(t.Context(), "a", "k")
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if !found || string(value) != `{"x":1}` {
		t.Fatalf("读回来的是 %s（found=%v）", value, found)
	}
	// 写那一路交回的令牌和读那一路交回的必须是同一个，否则拿写的结果去守卫下一次写
	// 会当场判成「有人动过」。
	if revision != written {
		t.Fatalf("写给的是 %q，读给的是 %q", written, revision)
	}
}

func Test单例槽的单条读给出值和令牌(t *testing.T) {
	unit := newRecords(t, RecordSpec{
		Name: "reading_single", Version: 1, Tables: []string{"a"}, Singleton: true,
	})

	value, revision, err := unit.ReadSingleton(t.Context())
	if err != nil || value != nil || revision != "" {
		t.Fatalf("没写过的单例槽该是空的：value=%s revision=%q err=%v", value, revision, err)
	}

	written, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":1}`), nil)
	if err != nil {
		t.Fatalf("盖单例槽失败：%v", err)
	}
	value, revision, err = unit.ReadSingleton(t.Context())
	if err != nil {
		t.Fatalf("读单例槽失败：%v", err)
	}
	if string(value) != `{"v":1}` || revision != written {
		t.Fatalf("读回来的是 %s / %q，写给的是 %q", value, revision, written)
	}

	// 没声明槽的单元不该读得出一个「空槽」——那和「声明了但没写过」是两件事。
	other := newRecords(t, RecordSpec{Name: "no_single_read", Version: 1, Tables: []string{"a"}})
	if _, _, err := other.ReadSingleton(t.Context()); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("该报 ErrMalformedName，实际 %v", err)
	}
}

// MustBeAbsent 是「只许建，不许覆盖」。它必须落成一句原子语句：先查一次再写一次的话，
// 两个副本会同时查到「不在」，然后后写的那个静默覆盖先写的。
func Test只许建不许覆盖(t *testing.T) {
	unit := newRecords(t, RecordSpec{Name: "absent", Version: 1, Tables: []string{"a"}})

	if _, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`), MustBeAbsent{}); err != nil {
		t.Fatalf("第一次建失败：%v", err)
	}
	_, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`2`), MustBeAbsent{})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("第二次建该报 ErrStaleRevision，实际 %v", err)
	}
	// 被拒的那一次一个字都不许改。
	value, _, _, err := unit.Read(t.Context(), "a", "k")
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if string(value) != `1` {
		t.Fatalf("被拒的写改了介质：现在是 %s", value)
	}
}

// 这条压的是丢更新：拿一个过期的令牌去写，必须写不进去。
func Test拿过期令牌写会被拒(t *testing.T) {
	unit := newRecords(t, RecordSpec{
		Name: "guarded", Version: 1, Tables: []string{"a"}, Singleton: true,
	})

	stale, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`1`), nil)
	if err != nil {
		t.Fatalf("写失败：%v", err)
	}
	// 「别人」在这中间改了一次。
	fresh, err := unit.Put(t.Context(), "a", "k", json.RawMessage(`2`), MustMatch{Revision: stale})
	if err != nil {
		t.Fatalf("拿最新令牌写该成功：%v", err)
	}
	_, err = unit.Put(t.Context(), "a", "k", json.RawMessage(`3`), MustMatch{Revision: stale})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("拿过期令牌写该报 ErrStaleRevision，实际 %v", err)
	}
	value, _, _, err := unit.Read(t.Context(), "a", "k")
	if err != nil {
		t.Fatalf("读失败：%v", err)
	}
	if string(value) != `2` {
		t.Fatalf("被拒的写改了介质：现在是 %s", value)
	}

	// 别处发的令牌当作对不上处理，不当作一个格式错误——调用方真正的问题是
	// 「我以为我读过这条记录」。
	_, err = unit.Put(t.Context(), "a", "k", json.RawMessage(`4`), MustMatch{Revision: "别处发的"})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("认不出的令牌该报 ErrStaleRevision，实际 %v", err)
	}

	// 删也守得住：拿过期令牌删不掉，拿最新的删得掉。
	if _, err := unit.Delete(t.Context(), "a", "k", &MustMatch{Revision: stale}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("拿过期令牌删该报 ErrStaleRevision，实际 %v", err)
	}
	existed, err := unit.Delete(t.Context(), "a", "k", &MustMatch{Revision: fresh})
	if err != nil || !existed {
		t.Fatalf("拿最新令牌删该成功：existed=%v err=%v", existed, err)
	}
	// 记录已经不在了，那一版自然也守不住。
	if _, err := unit.Delete(t.Context(), "a", "k", &MustMatch{Revision: fresh}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("删一个已经不在的记录该报 ErrStaleRevision，实际 %v", err)
	}

	// 单例槽走的是同一条路。
	single, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":1}`), MustBeAbsent{})
	if err != nil {
		t.Fatalf("建单例槽失败：%v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":2}`), MustBeAbsent{}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("重建单例槽该报 ErrStaleRevision，实际 %v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":3}`), MustMatch{Revision: single}); err != nil {
		t.Fatalf("拿最新令牌盖单例槽该成功：%v", err)
	}
	if _, err := unit.SetSingleton(t.Context(), json.RawMessage(`{"v":4}`), MustMatch{Revision: single}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("拿过期令牌盖单例槽该报 ErrStaleRevision，实际 %v", err)
	}
}

// 记录的令牌和流的令牌一样拌了实例标识：两份各自独立的介质发出来的永远比不出相等。
// 不拌的话，一个「从 A 读、拿 B 的令牌去核对」的调用方会以为自己看的是同一份东西。
func Test两份介质发的令牌不撞(t *testing.T) {
	left := newRecords(t, RecordSpec{Name: "same", Version: 1, Tables: []string{"a"}})
	right := newRecords(t, RecordSpec{Name: "same", Version: 1, Tables: []string{"a"}})

	one, err := left.Put(t.Context(), "a", "k", json.RawMessage(`1`), nil)
	if err != nil {
		t.Fatalf("写失败：%v", err)
	}
	two, err := right.Put(t.Context(), "a", "k", json.RawMessage(`1`), nil)
	if err != nil {
		t.Fatalf("写失败：%v", err)
	}
	if one == two {
		t.Fatalf("两份介质发出了同一个令牌 %q", one)
	}
	_, err = right.Put(t.Context(), "a", "k", json.RawMessage(`2`), MustMatch{Revision: one})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("拿另一份介质的令牌写该报 ErrStaleRevision，实际 %v", err)
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
