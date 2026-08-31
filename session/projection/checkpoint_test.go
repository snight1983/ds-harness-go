// 本文件的作用：不拿活会话的那三级读法——地板怎么算、零 I/O 那级看到什么、
// 以及冷读在什么情况下必须拒。
//
// 源: packages/session/session-projection/src/index.ts:342-454

package projection

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/session"
)

func TestRestoreFloorSaysNothingWhenNoUnitIsRegistered(t *testing.T) {
	t.Parallel()

	// 一个单元都没登记就根本不用去读日志——[Registry.Restore] 无论如何都只会
	// 给出空值。
	if _, ok := NewRegistry().RestoreFloor(Checkpoint{}); ok {
		t.Fatalf("没有单元就不该要求读任何东西")
	}
}

func TestRestoreFloorTakesTheLowestUsableWatermarkMinusOne(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		checkpoint func(*testing.T) Checkpoint
		want       int
	}{
		"两个键都有可用的行，取低的那个再减一": {
			checkpoint: func(t *testing.T) Checkpoint {
				return Checkpoint{"a": countRow(t, 0, 9, 1), "b": countRow(t, 0, 4, 1)}
			},
			want: 4,
		},
		"缺一行的键把地板拉到零": {
			checkpoint: func(t *testing.T) Checkpoint {
				return Checkpoint{"a": countRow(t, 0, 9, 1)}
			},
			want: 0,
		},
		"版本对不上的行等于没有": {
			checkpoint: func(t *testing.T) Checkpoint {
				return Checkpoint{"a": countRow(t, 0, 9, 1), "b": countRow(t, 7, 9, 1)}
			},
			want: 0,
		},
		"空日志的行（水位负一）也是零": {
			checkpoint: func(t *testing.T) Checkpoint {
				return Checkpoint{"a": countRow(t, 0, -1, 0), "b": countRow(t, 0, -1, 0)}
			},
			want: 0,
		},
		"两个键都从头折过一条": {
			checkpoint: func(t *testing.T) Checkpoint {
				return Checkpoint{"a": countRow(t, 0, 0, 1), "b": countRow(t, 0, 0, 1)}
			},
			want: 0,
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			defer mustRegister(t, registry, countUnit("a", 0))()
			defer mustRegister(t, registry, countUnit("b", 0))()

			floor, ok := registry.RestoreFloor(item.checkpoint(t))
			if !ok {
				t.Fatalf("有单元登记就该给出地板")
			}
			if floor != item.want {
				t.Fatalf("地板该是 %d，实际 %d", item.want, floor)
			}
		})
	}
}

func TestRestoreFloorAnchorsOneEventBelowSoAShrunkLogShowsUp(t *testing.T) {
	t.Parallel()

	// 「往下让一条」是承重的：读回来的尾巴要能证明存储里的日志还延伸到哪儿。
	// 一份被崩溃收尾截短到某一行水位之下的日志，从这个锚点读回来是空的，
	// 于是 Restore 拒掉、调用方重读整份——而不是把那行过期数据当成当前值端出去。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	checkpoint := Checkpoint{"count": countRow(t, 0, 9, 5)}
	floor, ok := registry.RestoreFloor(checkpoint)
	if !ok || floor != 9 {
		t.Fatalf("地板该正好落在那行水位上（10 减一）：%d %v", floor, ok)
	}

	// 日志缩到 seq 9 以下：从 9 读起什么都没有。
	_, err := registry.Restore(checkpoint, nil, floor)
	if !errors.Is(err, ErrCheckpointUnusable) {
		t.Fatalf("空尾巴该把这行判成越界：%v", err)
	}
}

func TestViewCheckpointServesOnlyTheRowsItCanTrust(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("good", 0))()
	defer mustRegister(t, registry, countUnit("stale", 0))()
	defer mustRegister(t, registry, countUnit("absent", 0))()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 0))()
	defer mustRegister(t, registry, undecodableUnit("broken"))()

	values := registry.ViewCheckpoint(Checkpoint{
		"good":   countRow(t, 0, 3, 4),
		"stale":  countRow(t, 9, 3, 4),
		"hidden": countRow(t, 0, 3, 4),
		"broken": countRow(t, 0, 3, 4),
	})

	if len(values) != 1 || values["good"] != 4 {
		t.Fatalf("只有版本对得上又解得开的客户端可见键能出来：%#v", values)
	}
}

func TestViewCheckpointWithNothingUsableIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	values := registry.ViewCheckpoint(Checkpoint{})
	if values == nil {
		t.Fatalf("该是空映射，不是 nil")
	}
	if len(values) != 0 {
		t.Fatalf("一行都不可用：%#v", values)
	}
}

func TestRestoreFoldsTheTailOnTopOfAUsableRow(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	// 行折到 seq 3、已经数了 4 条；尾巴从 seq 3 起给到 seq 5。
	// seq 3 那条已经在行里了，不许再数一遍。
	events := []session.Event{userEvent(3), userEvent(4), userEvent(5)}
	restored, err := registry.Restore(Checkpoint{"count": countRow(t, 0, 3, 4)}, events, 3)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}

	if restored.Snapshot.AsOfSeq != 5 {
		t.Fatalf("水位该切在给进来那截日志的末尾：%d", restored.Snapshot.AsOfSeq)
	}
	if restored.Snapshot.Values["count"] != 6 {
		t.Fatalf("行里已经数过的那条不许重复数：%#v", restored.Snapshot.Values)
	}

	row := restored.Checkpoint["count"]
	if row.Ver != 0 || row.Seq != 5 || string(row.Val) != `{"count":6}` {
		t.Fatalf("刷新出来的行该直接能落盘：%#v %s", row, row.Val)
	}
}

func TestRestoreRefoldsFromInitWhenTheWholeLogIsSupplied(t *testing.T) {
	t.Parallel()

	// baseSeq 为 0 就是「整份日志都在这儿」，这时候一行用不了直接从 Init 重折
	// 是**成立**的——这也是唯一成立的场合。
	events := []session.Event{userEvent(0), otherEvent(1), userEvent(2)}

	cases := map[string]Checkpoint{
		"压根没有行":  {},
		"版本对不上":  {"count": countRow(t, 9, 1, 100)},
		"行超出了末尾": {"count": countRow(t, 0, 99, 100)},
	}

	for name, checkpoint := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			defer mustRegister(t, registry, countUnit("count", 0))()

			restored, err := registry.Restore(checkpoint, events, 0)
			if err != nil {
				t.Fatalf("整份日志在手就该重折，不该报错：%v", err)
			}
			if restored.Snapshot.Values["count"] != 2 {
				t.Fatalf("重折出来该是 2：%#v", restored.Snapshot.Values)
			}
		})
	}
}

func TestRestoreRefusesToRefoldOverAPartialLog(t *testing.T) {
	t.Parallel()

	// 重折只在完整日志上成立。给的是一截尾巴、行又用不了，就必须让调用方
	// 从 seq 0 重读——在半截日志上从 Init 折出来的值是错的，不是旧的。
	events := []session.Event{userEvent(4), userEvent(5)}

	cases := map[string]Checkpoint{
		"压根没有行":  {},
		"版本对不上":  {"count": countRow(t, 9, 3, 100)},
		"行比地板还旧": {"count": countRow(t, 0, 1, 100)},
		"行超出了末尾": {"count": countRow(t, 0, 99, 100)},
	}

	for name, checkpoint := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			defer mustRegister(t, registry, countUnit("count", 0))()

			_, err := registry.Restore(checkpoint, events, 4)
			if !errors.Is(err, ErrCheckpointUnusable) {
				t.Fatalf("该拒掉：%v", err)
			}
			if !strings.Contains(err.Error(), "count") {
				t.Fatalf("错误里该点出是哪个键：%q", err.Error())
			}
		})
	}
}

func TestRestoreOnAnEmptyTailSitsOneBelowTheBase(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	restored, err := registry.Restore(Checkpoint{}, nil, 0)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if restored.Snapshot.AsOfSeq != -1 {
		t.Fatalf("空日志的水位该是 -1：%d", restored.Snapshot.AsOfSeq)
	}
	if restored.Checkpoint["count"].Seq != -1 {
		t.Fatalf("刷新出来的行也该停在 -1：%#v", restored.Checkpoint["count"])
	}
}

func TestRestoreSurfacesABrokenRowInsteadOfQuietlyRefolding(t *testing.T) {
	t.Parallel()

	// 版本对得上却解不开，说明是**这个构建自己**写坏了这行，不是过期。
	// 悄悄退回重折会把一个真实的缺陷盖住，而且盖住之后每次冷读都白折整份日志。
	// 零 I/O 那一级（ViewCheckpoint）跳过它是另一回事：那一级本来就只承诺
	// 「看得见多少算多少」。
	registry := NewRegistry()
	defer mustRegister(t, registry, undecodableUnit("count"))()

	_, err := registry.Restore(Checkpoint{"count": countRow(t, 0, -1, 0)}, nil, 0)
	if !errors.Is(err, errDecode) {
		t.Fatalf("该原样上抛那次解码失败：%v", err)
	}
	if errors.Is(err, ErrCheckpointUnusable) {
		t.Fatalf("这不是「行用不了」，重读一遍治不好它：%v", err)
	}
}

func TestRestoreRefusesAStateThatCannotBeWritten(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, unmarshalableUnit("bad"))()

	if _, err := registry.Restore(Checkpoint{}, nil, 0); err == nil {
		t.Fatalf("排不出去的状态该报出来")
	}
}

func TestRestoreOmitsHostOnlyUnitsFromTheSnapshotButNotFromTheCheckpoint(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	defer mustRegister(t, registry, hostOnlyUnit("hidden", 0))()

	restored, err := registry.Restore(Checkpoint{}, []session.Event{userEvent(0)}, 0)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(restored.Snapshot.Values) != 0 {
		t.Fatalf("只给宿主看的单元不进读切：%#v", restored.Snapshot.Values)
	}
	if string(restored.Checkpoint["hidden"].Val) != `{"count":1}` {
		t.Fatalf("但它照样要进检查点：%s", restored.Checkpoint["hidden"].Val)
	}
}

func TestRestoreRoundTripsWithRestoreFloor(t *testing.T) {
	t.Parallel()

	// 这三个函数是一条链，单独看每一个都对不出问题来：取地板 → 按地板读尾巴 →
	// 用同一个地板做 baseSeq 恢复 → 把刷新出来的行当成下一轮的检查点。
	registry := NewRegistry()
	defer mustRegister(t, registry, countUnit("count", 0))()

	stored := []session.Event{userEvent(0), otherEvent(1), userEvent(2), userEvent(3)}
	readFrom := func(from int) []session.Event {
		var tail []session.Event
		for _, event := range stored {
			if event.Seq >= from {
				tail = append(tail, event)
			}
		}
		return tail
	}

	// 第一轮：手上什么都没有，地板落在 0，读整份。
	checkpoint := Checkpoint{}
	floor, ok := registry.RestoreFloor(checkpoint)
	if !ok || floor != 0 {
		t.Fatalf("没有行时地板该是 0：%d %v", floor, ok)
	}
	first, err := registry.Restore(checkpoint, readFrom(floor), floor)
	if err != nil {
		t.Fatalf("第一轮不该报错：%v", err)
	}
	if first.Snapshot.Values["count"] != 3 {
		t.Fatalf("第一轮该数出三条：%#v", first.Snapshot.Values)
	}

	// 第二轮：拿上一轮刷新出来的行，日志又长了两条。
	stored = append(stored, userEvent(4), otherEvent(5))
	floor, ok = registry.RestoreFloor(first.Checkpoint)
	if !ok || floor != 3 {
		t.Fatalf("地板该落在上一轮水位上：%d %v", floor, ok)
	}
	second, err := registry.Restore(first.Checkpoint, readFrom(floor), floor)
	if err != nil {
		t.Fatalf("第二轮不该报错：%v", err)
	}
	if second.Snapshot.AsOfSeq != 5 || second.Snapshot.Values["count"] != 4 {
		t.Fatalf("第二轮只该把新的那条数进去：%d %#v", second.Snapshot.AsOfSeq, second.Snapshot.Values)
	}
}

func TestCheckpointRowGoesThroughJSONVerbatim(t *testing.T) {
	t.Parallel()

	// 这一行是要落盘的，字段名就是介质上的字段名，改了它等于把旧库读废。
	row := CheckpointRow{Ver: 2, Seq: 7, Val: json.RawMessage(`{"count":3}`)}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"ver":2,"seq":7,"val":{"count":3}}` {
		t.Fatalf("介质上的样子不对：%s", encoded)
	}

	var back CheckpointRow
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back.Ver != 2 || back.Seq != 7 || string(back.Val) != `{"count":3}` {
		t.Fatalf("往返不一致：%#v", back)
	}
}
