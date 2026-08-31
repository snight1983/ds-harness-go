// 本文件的作用：那个严格解码器为什么必须严格，以及类型抹除之后每条闭包还是
// 认得自己那个类型。
//
// 源: packages/session/session-projection/src/index.ts:34-136

package projection

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/session"
)

func TestStrictDecoderRefusesFieldsItDoesNotKnow(t *testing.T) {
	t.Parallel()

	decode := StrictDecoder[countState]()

	t.Run("认得的形状读得回来", func(t *testing.T) {
		t.Parallel()

		state, err := decode(json.RawMessage(`{"count":3}`))
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if state.Count != 3 {
			t.Fatalf("值该原样读回来：%d", state.Count)
		}
	})

	t.Run("多出来的字段就是读不动", func(t *testing.T) {
		t.Parallel()

		// 这正是这个帮手存在的全部理由：encoding/json 默认会把 total 默默扔掉，
		// 读出一个 Count 为零的状态，然后被往前折成垃圾。一份形状对不上的旧状态
		// 必须报错，而不是变成一个「字段都在、值全是零」的状态。
		if _, err := decode(json.RawMessage(`{"count":3,"total":9}`)); err == nil {
			t.Fatalf("多一个字段该读不动")
		}
	})

	t.Run("坏字节读不动", func(t *testing.T) {
		t.Parallel()

		if _, err := decode(json.RawMessage(`{`)); err == nil {
			t.Fatalf("坏字节该读不动")
		}
	})
}

func TestEraseKeepsEachClosureOnItsOwnType(t *testing.T) {
	t.Parallel()

	erased := erase(countUnit("count", 2))

	if erased.key != "count" || erased.stateVersion != 2 {
		t.Fatalf("声明该原样带过来：%q v%d", erased.key, erased.stateVersion)
	}

	state := erased.init()
	if state.(countState).Count != 0 {
		t.Fatalf("初始状态该是零：%#v", state)
	}

	next, changed := erased.apply(state, userEvent(0))
	if !changed || next.(countState).Count != 1 {
		t.Fatalf("该数上一条：%#v %v", next, changed)
	}

	same, changed := erased.apply(next, otherEvent(1))
	if changed || same.(countState).Count != 1 {
		t.Fatalf("不关心的事件该原样返回并报没变：%#v %v", same, changed)
	}

	if erased.view == nil {
		t.Fatalf("这个单元有视图")
	}
	if erased.view(next) != 1 {
		t.Fatalf("视图该给出计数：%#v", erased.view(next))
	}

	decoded, err := erased.decodeState(json.RawMessage(`{"count":7}`))
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if decoded.(countState).Count != 7 {
		t.Fatalf("该读回来：%#v", decoded)
	}
}

func TestEraseSurfacesADecodeFailureAsIs(t *testing.T) {
	t.Parallel()

	erased := erase(undecodableUnit("count"))

	state, err := erased.decodeState(json.RawMessage(`{}`))
	if !errors.Is(err, errDecode) {
		t.Fatalf("该原样上抛那次失败：%v", err)
	}
	if state != nil {
		t.Fatalf("读不动的时候不该顺手给一个状态出来：%#v", state)
	}
}

func TestEraseLeavesAHostOnlyUnitWithoutAView(t *testing.T) {
	t.Parallel()

	// view 为 nil 就是「只给宿主看」这件事在类型抹除之后的全部表示，
	// 读切那一侧就是靠它跳过这个单元的。
	if erase(hostOnlyUnit("count", 0)).view != nil {
		t.Fatalf("没给视图就不该凭空造一个")
	}
}

func TestBuildCellFoldsFromInit(t *testing.T) {
	t.Parallel()

	def := erase(countUnit("count", 0))

	t.Run("空日志的水位是负一", func(t *testing.T) {
		t.Parallel()

		// -1 是「一条都没折过」的表示，[Registry.Restore] 判一行可不可用时
		// 拿的就是这个值当地板。
		cell := buildCell(def, nil)
		if cell.observedSeq != -1 {
			t.Fatalf("空日志的水位该是 -1：%d", cell.observedSeq)
		}
		if cell.state.(countState).Count != 0 {
			t.Fatalf("空日志该给出初始状态：%#v", cell.state)
		}
	})

	t.Run("水位停在最后一条上，不管它变没变", func(t *testing.T) {
		t.Parallel()

		// 最后那条是单元不关心的类型：水位照样要推到它身上，否则下一次推进会
		// 把它当成还没折过的事件再数一遍。
		cell := buildCell(def, []session.Event{userEvent(0), userEvent(1), otherEvent(2)})
		if cell.observedSeq != 2 {
			t.Fatalf("水位该停在最后一条上：%d", cell.observedSeq)
		}
		if cell.state.(countState).Count != 2 {
			t.Fatalf("该数出两条：%#v", cell.state)
		}
	})
}

func TestEventsBeforeCutsBySeqNotByIndex(t *testing.T) {
	t.Parallel()

	// seq 不从 0 起、也不密排——本仓库只保证严格递增。按下标切会切错，
	// 这正是这个函数不写成 events[:boundary] 的理由。
	events := []session.Event{userEvent(5), userEvent(7), userEvent(9)}

	cases := map[string]struct {
		boundary int
		want     int
	}{
		"比第一条还小":    {boundary: 5, want: 0},
		"落在两条中间":    {boundary: 8, want: 2},
		"正好等于某一条":   {boundary: 9, want: 2},
		"比最后一条还大":   {boundary: 100, want: 3},
		"负的（空日志推进）": {boundary: -1, want: 0},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := eventsBefore(events, item.boundary); len(got) != item.want {
				t.Fatalf("该切出 %d 条，实际 %d 条", item.want, len(got))
			}
		})
	}
}

func TestStrictDecoderErrorMentionsTheOffendingField(t *testing.T) {
	t.Parallel()

	// 运维看到的是这句话，里面没有那个字段名就只能自己去猜是哪儿对不上。
	_, err := StrictDecoder[countState]()(json.RawMessage(`{"count":1,"total":2}`))
	if err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("错误里该点出是哪个字段：%v", err)
	}
}
