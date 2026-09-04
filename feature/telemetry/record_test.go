// 本文件的作用：那份逻辑记录本身——三套封闭词汇、交出去之前那道校验、
// 复制一份是不是真的复制，以及脱敏那条链的语义。
//
// 源: packages/session/session-telemetry/src/index.ts

package telemetry

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTheThreeVocabulariesAreClosed(t *testing.T) {
	t.Parallel()

	// 这三套词汇是采集侧对外说的全部话。多收一个值等于单方面把契约扩宽了，
	// 而接收方那边多半要把它映成自己的枚举，在那儿才炸。
	cases := map[string]struct {
		valid bool
		got   bool
	}{
		"ledger 是通道":      {true, ChannelLedger.Valid()},
		"ops 是通道":         {true, ChannelOps.Valid()},
		"别的不是通道":          {false, Channel("trace").Valid()},
		"空串不是通道":          {false, Channel("").Valid()},
		"info 是级别":        {true, SeverityInfo.Valid()},
		"warn 是级别":        {true, SeverityWarn.Valid()},
		"error 是级别":       {true, SeverityError.Valid()},
		"别的不是级别":          {false, Severity("fatal").Valid()},
		"full 是政策":        {true, SharingFull.Valid()},
		"feedback-only 是": {true, SharingFeedbackOnly.Valid()},
		"disabled 是政策":    {true, SharingDisabled.Valid()},
		"别的不是政策":          {false, SharingStatus("partial").Valid()},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if item.got != item.valid {
				t.Fatalf("该是 %v，实际 %v", item.valid, item.got)
			}
		})
	}
}

func TestTheVocabulariesGoOutAsTheirWireStrings(t *testing.T) {
	t.Parallel()

	// 这几个字面量就是介质上的字节，改了它等于让接收方读不懂。
	if string(ChannelLedger) != "ledger" || string(ChannelOps) != "ops" {
		t.Fatalf("通道的字面量不对：%q %q", ChannelLedger, ChannelOps)
	}
	if string(SeverityInfo) != "info" || string(SeverityWarn) != "warn" ||
		string(SeverityError) != "error" {
		t.Fatalf("级别的字面量不对")
	}
	if string(SharingFull) != "full" || string(SharingFeedbackOnly) != "feedback-only" ||
		string(SharingDisabled) != "disabled" {
		t.Fatalf("共享政策的字面量不对")
	}
}

func TestValidateAcceptsTheRecordsTheCoordinatorProduces(t *testing.T) {
	t.Parallel()

	cases := map[string]Record{
		"一条 ledger 记录": {
			Channel: ChannelLedger, Time: 7, Severity: SeverityInfo,
			Attributes: map[string]any{"session.id": "s1", "event.seq": 3},
			Body:       json.RawMessage(`{"turn":0}`),
		},
		"一条 ops 记录": {
			Channel: ChannelOps, Time: 7, Severity: SeverityError,
			Attributes: map[string]any{"telemetry.op": "agent-error"},
			Body:       json.RawMessage(`{"op":"shutdown"}`),
		},
		"warn 也收": {
			Channel: ChannelOps, Time: 0, Severity: SeverityWarn,
			Body: json.RawMessage(`null`),
		},
		"一个属性都没有": {
			Channel: ChannelLedger, Time: 0, Severity: SeverityInfo,
			Body: json.RawMessage(`{}`),
		},
		"各种数字都收": {
			Channel: ChannelLedger, Time: 0, Severity: SeverityInfo,
			Attributes: map[string]any{
				"a": "s", "b": 1, "c": int8(1), "d": int16(1), "e": int32(1), "f": int64(1),
				"g": uint(1), "h": uint8(1), "i": uint16(1), "j": uint32(1), "k": uint64(1),
				"l": float32(1), "m": float64(1),
			},
			Body: json.RawMessage(`{}`),
		},
	}

	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := record.Validate(); err != nil {
				t.Fatalf("该收下：%v", err)
			}
		})
	}
}

func TestValidateWithholdsTheRecordsAReceiverCouldNotSerialize(t *testing.T) {
	t.Parallel()

	// 这四条守的正是 TypeScript 那边由类型系统白送、Go 这边要自己补回来的
	// 那部分约束。一条属性值类型不对的记录会在接收器排 OTLP 的时候炸，
	// 那时候它已经离开采集侧了，没人查得出来是谁写坏的。
	cases := map[string]struct {
		record Record
		want   string
	}{
		"通道不在词汇里": {
			record: Record{Channel: "trace", Severity: SeverityInfo, Body: json.RawMessage(`{}`)},
			want:   "通道",
		},
		"级别不在词汇里": {
			record: Record{Channel: ChannelOps, Severity: "fatal", Body: json.RawMessage(`{}`)},
			want:   "级别",
		},
		"属性值既不是字符串也不是数字": {
			record: Record{
				Channel: ChannelOps, Severity: SeverityInfo,
				Attributes: map[string]any{"session.live": true},
				Body:       json.RawMessage(`{}`),
			},
			want: "session.live",
		},
		"body 不是合法 JSON": {
			record: Record{Channel: ChannelOps, Severity: SeverityInfo, Body: json.RawMessage(`{`)},
			want:   "body",
		},
		"body 是空的": {
			record: Record{Channel: ChannelOps, Severity: SeverityInfo},
			want:   "body",
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := item.record.Validate()
			if err == nil {
				t.Fatalf("该拒掉")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("错误里该说清是哪一条不合规，实际是 %q", err.Error())
			}
		})
	}
}

func TestBoolIsNotAnAttributeValueBecauseTheContractNeverHadOne(t *testing.T) {
	t.Parallel()

	// 收下 bool 不是「宽容一点」，是把契约单方面改宽了：接收方按
	// string | number 建的表里没有它的位置。
	if validAttribute(true) {
		t.Fatalf("bool 不该是一个合法的属性值")
	}
	if validAttribute(nil) || validAttribute([]string{"a"}) {
		t.Fatalf("nil 和切片都不该是合法的属性值")
	}
}

func TestCloneCopiesBothTheTableAndTheBytes(t *testing.T) {
	t.Parallel()

	original := Record{
		Channel: ChannelLedger, Time: 7, Severity: SeverityInfo,
		Attributes: map[string]any{"session.id": "s1"},
		Body:       json.RawMessage(`{"a":1}`),
	}
	cloned := original.Clone()
	cloned.Attributes["session.id"] = "s2"
	cloned.Body[2] = 'b'

	if original.Attributes["session.id"] != "s1" {
		t.Fatalf("改到副本不该动到原件的属性表：%v", original.Attributes)
	}
	if string(original.Body) != `{"a":1}` {
		t.Fatalf("改到副本不该动到原件的 body：%s", original.Body)
	}
}

func TestCloningAnEmptyRecordKeepsBothSlotsEmpty(t *testing.T) {
	t.Parallel()

	// 空表和缺席在 Go 的读上完全等价，复制的时候不该凭空造出一张空表来——
	// 那会让 [Record] 的 JSON 从 `"attributes":null` 变成 `{}`。
	cloned := Record{Channel: ChannelOps, Severity: SeverityInfo}.Clone()
	if cloned.Attributes != nil || cloned.Body != nil {
		t.Fatalf("两个槽都该还是空的：%#v", cloned)
	}
}

func TestTheRecordGoesOutWithTheFieldNamesAReceiverReads(t *testing.T) {
	t.Parallel()

	// 字段名就是介质上的字段名，改了它等于让已经在跑的接收方读不到。
	encoded, err := json.Marshal(Record{
		Channel: ChannelOps, Time: 7, Severity: SeverityInfo,
		Attributes: map[string]any{"telemetry.op": "shutdown"},
		Body:       json.RawMessage(`{"op":"shutdown"}`),
	})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	want := `{"channel":"ops","time":7,"severity":"info",` +
		`"attributes":{"telemetry.op":"shutdown"},"body":{"op":"shutdown"}}`
	if string(encoded) != want {
		t.Fatalf("介质上的样子不对：%s", encoded)
	}
}

func TestWithNoRuleMountedTheRecordReachesTheSinkAsCaptured(t *testing.T) {
	t.Parallel()

	// 导出去的数据有多干净，完全等于部署方挂了哪些规则——本包一条都不带。
	record := Record{Channel: ChannelLedger, Severity: SeverityInfo}
	got, err := runRules(nil, record)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("该原样出来：%#v %v", got, err)
	}
}

func TestRulesStackFromTheInsideOut(t *testing.T) {
	t.Parallel()

	// cordis 的 waterfall 语义：每条规则拿到的都是那份原始记录，next 跑的是
	// 排在它下面的全部规则。所以最外面那条**最后**加工，日志里读到的顺序
	// 和实际生效的顺序是反的——这一条不钉住，两边的规则链就会静默地不一样。
	trace := []string{}
	mark := func(name string) Rule {
		return func(record Record, next func() (Record, error)) (Record, error) {
			trace = append(trace, "进入 "+name)
			inner, err := next()
			if err != nil {
				return Record{}, err
			}
			trace = append(trace, "离开 "+name)
			inner.Attributes["order"] = inner.Attributes["order"].(string) + name
			return inner, nil
		}
	}

	got, err := runRules([]Rule{mark("外"), mark("内")}, Record{
		Channel: ChannelLedger, Severity: SeverityInfo,
		Attributes: map[string]any{"order": ""},
	})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got.Attributes["order"] != "内外" {
		t.Fatalf("加工顺序该是从里往外：%v", got.Attributes["order"])
	}
	want := []string{"进入 外", "进入 内", "离开 内", "离开 外"}
	if strings.Join(trace, ",") != strings.Join(want, ",") {
		t.Fatalf("调用顺序不对：%v", trace)
	}
}

func TestARuleThatSkipsNextReplacesEverythingBeneathIt(t *testing.T) {
	t.Parallel()

	// 这是 waterfall 明写的一条：不调 next 就把底下全部替换掉。一条规则要
	// 「这类记录一律换成这份脱敏后的形状」，靠的就是它。
	replaced := Record{Channel: ChannelOps, Severity: SeverityWarn}
	inner := func(Record, func() (Record, error)) (Record, error) {
		t.Fatalf("底下那条规则不该跑")
		return Record{}, nil
	}
	outer := func(Record, func() (Record, error)) (Record, error) { return replaced, nil }

	got, err := runRules([]Rule{outer, inner}, Record{Channel: ChannelLedger})
	if err != nil || !reflect.DeepEqual(got, replaced) {
		t.Fatalf("该被整条替换掉：%#v %v", got, err)
	}
}

func TestARuleThatFailsWithholdsTheRecord(t *testing.T) {
	t.Parallel()

	// 扣下是 fail-closed 那一边：一条脱敏规则拿不准，宁可不上报也不能
	// 把没脱干净的东西送出去。
	refused := errors.New("这条不许出去")
	got, err := runRules([]Rule{
		func(record Record, next func() (Record, error)) (Record, error) { return next() },
		func(Record, func() (Record, error)) (Record, error) { return Record{}, refused },
	}, Record{Channel: ChannelLedger})

	if !errors.Is(err, refused) {
		t.Fatalf("该把规则的错误交上来：%v", err)
	}
	if !reflect.DeepEqual(got, Record{}) {
		t.Fatalf("失败时不该给出半份记录：%#v", got)
	}
}
