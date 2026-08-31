// 本文件的作用：一串连续的增量分块怎么压成一行、又怎么原样展开回来，以及哪些形状不压。
//
// 源: packages/core/session/src/chunk-rows.ts

package session

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"ds-harness-go/llm"
)

func TestPackChunkRunsCompressesARunOfTextDeltas(t *testing.T) {
	t.Parallel()

	events := []Event{
		textChunkEvent(t, 0, 10, 1, 1, 0, "a"),
		textChunkEvent(t, 1, 12, 1, 1, 0, "b"),
		textChunkEvent(t, 2, 11, 1, 1, 0, "c"),
	}

	rows, err := PackChunkRuns(events)
	if err != nil {
		t.Fatalf("压不出来：%v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("三条该压成一行，实际 %d 行", len(rows))
	}
	// dt 记的是间隔而不是绝对时刻，而且间隔可以是负的——墙上时钟会倒退。
	want := `{"type":"text-chunks","seq0":0,"time0":10,` +
		`"data":{"turn":1,"step":1,"index":0,"dt":[2,-1],"texts":["a","b","c"]}}`
	if string(rows[0]) != want {
		t.Fatalf("压出来的字节不对：\n想要 %s\n实际 %s", want, rows[0])
	}

	assertRoundTrip(t, events)
}

func TestPackChunkRunsKeepsAShortRunAsPlainEvents(t *testing.T) {
	t.Parallel()

	// 低于 minRun，一行记录的信封和它替掉的那几行事件差不多大，压了没收益。
	for count := 1; count < minRun; count++ {
		events := make([]Event, 0, count)
		for seq := range count {
			events = append(events, textChunkEvent(t, seq, int64(seq), 1, 1, 0, "x"))
		}

		rows, err := PackChunkRuns(events)
		if err != nil {
			t.Fatalf("压不出来：%v", err)
		}
		if len(rows) != count {
			t.Fatalf("%d 条不该被压：实际 %d 行", count, len(rows))
		}
		assertRoundTrip(t, events)
	}
}

func TestPackChunkRunsSeparatesTheThreeRowKinds(t *testing.T) {
	t.Parallel()

	name := "read"

	cases := map[string]struct {
		events []Event
		want   RowType
	}{
		"文本增量": {
			events: []Event{
				textChunkEvent(t, 0, 0, 1, 1, 0, "a"),
				textChunkEvent(t, 1, 1, 1, 1, 0, "b"),
				textChunkEvent(t, 2, 2, 1, 1, 0, "c"),
			},
			want: RowTextChunks,
		},
		"推理增量": {
			events: []Event{
				reasoningChunkEvent(t, 0, 0, 1, 1, 0, "a"),
				reasoningChunkEvent(t, 1, 1, 1, 1, 0, "b"),
				reasoningChunkEvent(t, 2, 2, 1, 1, 0, "c"),
			},
			want: RowReasoningChunks,
		},
		"工具调用增量": {
			events: []Event{
				toolCallChunkEvent(t, 0, 0, 1, 1, 0, "c1", &name, `{"p`),
				toolCallChunkEvent(t, 1, 1, 1, 1, 0, "c1", &name, `ath"`),
				toolCallChunkEvent(t, 2, 2, 1, 1, 0, "c1", &name, `:"a"}`),
			},
			want: RowToolCallChunks,
		},
		"工具调用增量不带工具名": {
			events: []Event{
				toolCallChunkEvent(t, 0, 0, 1, 1, 0, "c1", nil, "a"),
				toolCallChunkEvent(t, 1, 1, 1, 1, 0, "c1", nil, "b"),
				toolCallChunkEvent(t, 2, 2, 1, 1, 0, "c1", nil, "c"),
			},
			want: RowToolCallChunks,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows, err := PackChunkRuns(testCase.events)
			if err != nil {
				t.Fatalf("压不出来：%v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("该压成一行，实际 %d 行", len(rows))
			}
			var probe struct {
				Type RowType `json:"type"`
			}
			if err := json.Unmarshal(rows[0], &probe); err != nil {
				t.Fatalf("行标签读不回来：%v", err)
			}
			if probe.Type != testCase.want {
				t.Fatalf("行标签不对：想要 %q，实际 %q", testCase.want, probe.Type)
			}
			assertRoundTrip(t, testCase.events)
		})
	}
}

func TestPackChunkRunsBreaksTheRunOnEveryDiscontinuity(t *testing.T) {
	t.Parallel()

	first := "read"
	second := "write"

	// 每一例的头三条能压成一行，第四条因为写着的那个理由接不上去。
	cases := map[string]Event{
		"seq 断了":    textChunkEvent(t, 9, 3, 1, 1, 0, "d"),
		"回合变了":      textChunkEvent(t, 3, 3, 2, 1, 0, "d"),
		"步骤变了":      textChunkEvent(t, 3, 3, 1, 2, 0, "d"),
		"块序号变了":     textChunkEvent(t, 3, 3, 1, 1, 1, "d"),
		"换成了推理增量":   reasoningChunkEvent(t, 3, 3, 1, 1, 0, "d"),
		"换成了工具调用增量": toolCallChunkEvent(t, 3, 3, 1, 1, 0, "c1", &first, "d"),
		"时间戳相减会溢出":  textChunkEvent(t, 3, math.MinInt64, 1, 1, 0, "d"),
	}

	for name, tail := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			events := []Event{
				textChunkEvent(t, 0, 0, 1, 1, 0, "a"),
				textChunkEvent(t, 1, 1, 1, 1, 0, "b"),
				textChunkEvent(t, 2, 2, 1, 1, 0, "c"),
				tail,
			}
			rows, err := PackChunkRuns(events)
			if err != nil {
				t.Fatalf("压不出来：%v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("该是一行压好的加一条原样的，实际 %d 行", len(rows))
			}
			assertRoundTrip(t, events)
		})
	}

	t.Run("工具名的有无变了", func(t *testing.T) {
		t.Parallel()

		events := []Event{
			toolCallChunkEvent(t, 0, 0, 1, 1, 0, "c1", &first, "a"),
			toolCallChunkEvent(t, 1, 1, 1, 1, 0, "c1", &first, "b"),
			toolCallChunkEvent(t, 2, 2, 1, 1, 0, "c1", &first, "c"),
			toolCallChunkEvent(t, 3, 3, 1, 1, 0, "c1", nil, "d"),
		}
		rows, err := PackChunkRuns(events)
		if err != nil {
			t.Fatalf("压不出来：%v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("该断开，实际 %d 行", len(rows))
		}
		assertRoundTrip(t, events)
	})

	t.Run("工具名的取值变了", func(t *testing.T) {
		t.Parallel()

		events := []Event{
			toolCallChunkEvent(t, 0, 0, 1, 1, 0, "c1", &first, "a"),
			toolCallChunkEvent(t, 1, 1, 1, 1, 0, "c1", &first, "b"),
			toolCallChunkEvent(t, 2, 2, 1, 1, 0, "c1", &first, "c"),
			toolCallChunkEvent(t, 3, 3, 1, 1, 0, "c1", &second, "d"),
		}
		rows, err := PackChunkRuns(events)
		if err != nil {
			t.Fatalf("压不出来：%v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("该断开，实际 %d 行", len(rows))
		}
		assertRoundTrip(t, events)
	})

	t.Run("调用 id 变了", func(t *testing.T) {
		t.Parallel()

		events := []Event{
			toolCallChunkEvent(t, 0, 0, 1, 1, 0, "c1", nil, "a"),
			toolCallChunkEvent(t, 1, 1, 1, 1, 0, "c1", nil, "b"),
			toolCallChunkEvent(t, 2, 2, 1, 1, 0, "c1", nil, "c"),
			toolCallChunkEvent(t, 3, 3, 1, 1, 0, "c2", nil, "d"),
		}
		rows, err := PackChunkRuns(events)
		if err != nil {
			t.Fatalf("压不出来：%v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("该断开，实际 %d 行", len(rows))
		}
		assertRoundTrip(t, events)
	})
}

func TestPackChunkRunsPassesTheShapesItDoesNotRecognize(t *testing.T) {
	t.Parallel()

	// 认不全的一律原样存：丢的是压缩率，不是数据。
	unrecognized := func(mutate func(*Event)) []Event {
		events := []Event{
			textChunkEvent(t, 0, 0, 1, 1, 0, "a"),
			textChunkEvent(t, 1, 1, 1, 1, 0, "b"),
			textChunkEvent(t, 2, 2, 1, 1, 0, "c"),
		}
		for index := range events {
			mutate(&events[index])
		}
		return events
	}

	cases := map[string][]Event{
		"带了可跳过标记": unrecognized(func(e *Event) { e.Ignorable = true }),
		"带了表面标记":  unrecognized(func(e *Event) { e.SurfaceOp = AppendOp{} }),
		"带了来源清单":  unrecognized(func(e *Event) { e.SourceEventSeqs = []int{} }),
		"seq 是负的": unrecognized(func(e *Event) { e.Seq -= 10 }),
		"不是助手分块": {
			{Type: EventTurnStart, Seq: 0, Data: json.RawMessage(`{"turn":1}`)},
			{Type: EventTurnStart, Seq: 1, Data: json.RawMessage(`{"turn":1}`)},
			{Type: EventTurnStart, Seq: 2, Data: json.RawMessage(`{"turn":1}`)},
		},
		"负载多一个键": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"index":0,"chunk":{"type":"text-delta","index":0,"text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"index":0,"chunk":{"type":"text-delta","index":0,"text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"index":0,"chunk":{"type":"text-delta","index":0,"text":"c"}}`),
		},
		"负载少一个键": {
			rawChunkEvent(0, 0, `{"turn":1,"chunk":{"type":"text-delta","index":0,"text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"chunk":{"type":"text-delta","index":0,"text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"chunk":{"type":"text-delta","index":0,"text":"c"}}`),
		},
		// 键数对得上但名字不对：数一数不够，三个键得是那三个键。
		"负载的键数对但名字不对": {
			rawChunkEvent(0, 0, `{"turn":1,"stage":1,"chunk":{"type":"text-delta","index":0,"text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"stage":1,"chunk":{"type":"text-delta","index":0,"text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"stage":1,"chunk":{"type":"text-delta","index":0,"text":"c"}}`),
		},
		// 三个键齐了、名字也对，但值读不进那个分块结构体。
		"文本分块的值读不回来": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":"0","text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":"0","text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":"0","text":"c"}}`),
		},
		"工具调用分块的值读不回来": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":"0","id":"c1","argumentsDelta":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":"0","id":"c1","argumentsDelta":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":"0","id":"c1","argumentsDelta":"c"}}`),
		},
		"负载不是个对象": {
			rawChunkEvent(0, 0, `7`),
			rawChunkEvent(1, 1, `7`),
			rawChunkEvent(2, 2, `7`),
		},
		"turn 不是个数": {
			rawChunkEvent(0, 0, `{"turn":"1","step":1,"chunk":{"type":"text-delta","index":0,"text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":"1","step":1,"chunk":{"type":"text-delta","index":0,"text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":"1","step":1,"chunk":{"type":"text-delta","index":0,"text":"c"}}`),
		},
		"分块不是个对象": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":7}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":7}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":7}`),
		},
		"分块没带类型": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"index":0,"text":"a"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"index":0,"text":"b"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"index":0,"text":"c"}}`),
		},
		"分块的类型不在白名单里": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		},
		"文本分块多一个键": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"a","x":1}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"b","x":1}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"c","x":1}}`),
		},
		"工具调用分块少一个键": {
			rawChunkEvent(0, 0, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":0,"id":"c1"}}`),
			rawChunkEvent(1, 1, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":0,"id":"c1"}}`),
			rawChunkEvent(2, 2, `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":0,"id":"c1"}}`),
		},
	}

	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows, err := PackChunkRuns(events)
			if err != nil {
				t.Fatalf("压不出来：%v", err)
			}
			if len(rows) != len(events) {
				t.Fatalf("认不出来的形状该一条一行，实际 %d 行", len(rows))
			}
		})
	}
}

func TestPackChunkRunsFlushesTheRunBeforeAForeignEvent(t *testing.T) {
	t.Parallel()

	events := []Event{
		textChunkEvent(t, 0, 0, 1, 1, 0, "a"),
		textChunkEvent(t, 1, 1, 1, 1, 0, "b"),
		textChunkEvent(t, 2, 2, 1, 1, 0, "c"),
		{Type: EventStepEnd, Seq: 3, Time: 3, Data: json.RawMessage(`{"turn":1,"step":1}`)},
		textChunkEvent(t, 4, 4, 1, 2, 0, "d"),
		textChunkEvent(t, 5, 5, 1, 2, 0, "e"),
		textChunkEvent(t, 6, 6, 1, 2, 0, "f"),
	}

	rows, err := PackChunkRuns(events)
	if err != nil {
		t.Fatalf("压不出来：%v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("该是「一行 + 一条 + 一行」，实际 %d 行", len(rows))
	}
	assertRoundTrip(t, events)
}

func TestPackChunkRunsIsSafeOnAnyCut(t *testing.T) {
	t.Parallel()

	// 纯函数、无状态：一串被落盘边界切开的批次各压各的，合起来还是原样。
	var events []Event
	for seq := range 8 {
		events = append(events, textChunkEvent(t, seq, int64(seq), 1, 1, 0, "x"))
	}

	for cut := range len(events) + 1 {
		var restored []Event
		for _, part := range [][]Event{events[:cut], events[cut:]} {
			rows, err := PackChunkRuns(part)
			if err != nil {
				t.Fatalf("切在 %d 压不出来：%v", cut, err)
			}
			for _, row := range rows {
				back, err := DecodeStorageRecord(row)
				if err != nil {
					t.Fatalf("切在 %d 解不出来：%v", cut, err)
				}
				restored = append(restored, back...)
			}
		}
		assertSameEvents(t, events, restored)
	}
}

func TestPackChunkRunsOnAnEmptyBatch(t *testing.T) {
	t.Parallel()

	rows, err := PackChunkRuns(nil)
	if err != nil {
		t.Fatalf("压不出来：%v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("空批次该压出空的，实际 %d 行", len(rows))
	}
}

func TestPackChunkRunsReportsAnEventThatCannotBeMarshaled(t *testing.T) {
	t.Parallel()

	broken := Event{Type: EventUserMessage, Seq: 0, SurfaceOp: unmarshalableOp{}}
	if _, err := PackChunkRuns([]Event{broken}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestDecodeStorageRecordReadsAPlainEventBack(t *testing.T) {
	t.Parallel()

	line, err := json.Marshal(userMessageEvent(t, 3, "hi"))
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	got, err := DecodeStorageRecord(line)
	if err != nil {
		t.Fatalf("解不出来：%v", err)
	}
	if len(got) != 1 || got[0].Seq != 3 || got[0].Type != EventUserMessage {
		t.Fatalf("解出来的事件不对：%#v", got)
	}
}

func TestDecodeStorageRecordRejectsBrokenValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"根本不是一个对象":     `7`,
		"看着像事件但信封上有生键": `{"type":"user/message","seq":0,"time":0,"data":{},"x":1}`,
		"行的信封多一个键":     `{"type":"text-chunks","seq0":0,"time0":0,"data":{},"x":1}`,
		"行的信封少一个键":     `{"type":"text-chunks","seq0":0,"data":{}}`,
		"seq0 是负的":     `{"type":"text-chunks","seq0":-1,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"texts":["a"]}}`,
		"seq0 不是个数":    `{"type":"text-chunks","seq0":"0","time0":0,"data":{}}`,
		"负载不是个对象":      `{"type":"text-chunks","seq0":0,"time0":0,"data":7}`,
		"文本负载少一个键":     `{"type":"text-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[]}}`,
		"文本负载多一个键":     `{"type":"text-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"texts":["a"],"x":1}}`,
		"文本负载的值读不回来":   `{"type":"text-chunks","seq0":0,"time0":0,"data":{"turn":"1","step":1,"index":0,"dt":[],"texts":["a"]}}`,
		"成员清单是空的":      `{"type":"text-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"texts":[]}}`,
		"dt 的长度对不上成员数": `{"type":"text-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[1],"texts":["a"]}}`,
		"工具调用负载少一个键":   `{"type":"tool-call-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"id":"c1"}}`,
		"工具调用负载多一个键":   `{"type":"tool-call-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"id":"c1","args":["a"],"x":1}}`,
		"工具调用负载的值读不回来": `{"type":"tool-call-chunks","seq0":0,"time0":0,"data":{"turn":1,"step":1,"index":0,"dt":[],"id":7,"args":["a"]}}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := DecodeStorageRecord(json.RawMessage(line)); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

func TestDecodeStorageRecordRefusesToOverflowWhileRebuilding(t *testing.T) {
	t.Parallel()

	// 编码方只压那些 seq 与时间都在范围内的串，所以一个跑出范围的中间值
	// 不在任何编码方的像里——继续算下去只会得到一个和原值不同的数。
	cases := map[string]string{
		"成员的 seq 会溢出": `{"type":"text-chunks","seq0":9223372036854775807,"time0":0,` +
			`"data":{"turn":1,"step":1,"index":0,"dt":[0],"texts":["a","b"]}}`,
		"成员的时间戳会溢出": `{"type":"text-chunks","seq0":0,"time0":9223372036854775807,` +
			`"data":{"turn":1,"step":1,"index":0,"dt":[1],"texts":["a","b"]}}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := DecodeStorageRecord(json.RawMessage(line)); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

func TestOverflowChecksAgreeWithBigIntArithmetic(t *testing.T) {
	t.Parallel()

	edges := []int64{math.MinInt64, math.MinInt64 + 1, -2, -1, 0, 1, 2, math.MaxInt64 - 1, math.MaxInt64}

	for _, a := range edges {
		for _, b := range edges {
			// 判溢出的正解：结果的符号必须和无限精度下的一致。
			wantSub := (a >= 0) != (b >= 0) && ((a-b >= 0) != (a >= 0))
			if got := subOverflows(a, b); got != wantSub {
				t.Fatalf("%d - %d 的溢出判断不对：%v", a, b, got)
			}
			wantAdd := (a >= 0) == (b >= 0) && ((a+b >= 0) != (a >= 0))
			if got := addOverflows(a, b); got != wantAdd {
				t.Fatalf("%d + %d 的溢出判断不对：%v", a, b, got)
			}
		}
	}

	if !addOverflows(math.MaxInt64, 1) {
		t.Fatalf("加到头了该判溢出")
	}
	if !subOverflows(math.MaxInt64, -1) {
		t.Fatalf("减到头了该判溢出")
	}
	if addOverflows(1, -1) || subOverflows(1, 1) {
		t.Fatalf("不该溢出的判成了溢出")
	}
}

// assertRoundTrip 压一遍再解一遍，断言解出来的和原来一模一样。
func assertRoundTrip(t *testing.T, events []Event) {
	t.Helper()

	rows, err := PackChunkRuns(events)
	if err != nil {
		t.Fatalf("压不出来：%v", err)
	}
	var restored []Event
	for _, row := range rows {
		back, err := DecodeStorageRecord(row)
		if err != nil {
			t.Fatalf("解不出来：%v", err)
		}
		restored = append(restored, back...)
	}
	assertSameEvents(t, events, restored)
}

// assertSameEvents 按介质字节逐条比两串事件。
//
// 比字节而不是 [reflect.DeepEqual]：Data 是 json.RawMessage，同一份负载排出去的
// 字节一样，但中间过一趟 map 之后底层切片不一定还是同一段。
func assertSameEvents(t *testing.T, want, got []Event) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("条数对不上：想要 %d，实际 %d", len(want), len(got))
	}
	for index := range want {
		wantLine, err := json.Marshal(want[index])
		if err != nil {
			t.Fatalf("排不出去：%v", err)
		}
		gotLine, err := json.Marshal(got[index])
		if err != nil {
			t.Fatalf("排不出去：%v", err)
		}
		if !reflect.DeepEqual(wantLine, gotLine) {
			t.Fatalf("第 %d 条对不上：\n想要 %s\n实际 %s", index, wantLine, gotLine)
		}
	}
}

// textChunkEvent 排一条可见文本增量事件出来。
func textChunkEvent(t *testing.T, seq int, time int64, turn, step, index int, text string) Event {
	t.Helper()

	return chunkEventOf(t, seq, time, turn, step, llm.TextDeltaChunk{Index: index, Text: text})
}

// reasoningChunkEvent 排一条推理内容增量事件出来。
func reasoningChunkEvent(t *testing.T, seq int, time int64, turn, step, index int, text string) Event {
	t.Helper()

	return chunkEventOf(t, seq, time, turn, step, llm.ReasoningDeltaChunk{Index: index, Text: text})
}

// toolCallChunkEvent 排一条工具调用参数增量事件出来。
func toolCallChunkEvent(
	t *testing.T, seq int, time int64, turn, step, index int,
	callID llm.CallID, name *string, args string,
) Event {
	t.Helper()

	return chunkEventOf(t, seq, time, turn, step, llm.ToolCallDeltaChunk{
		Index: index, ID: callID, Name: name, ArgumentsDelta: args,
	})
}

// chunkEventOf 把一个分块包成一条助手分块事件。
func chunkEventOf(t *testing.T, seq int, time int64, turn, step int, chunk llm.StreamChunk) Event {
	t.Helper()

	payload, err := json.Marshal(AssistantChunkData{Turn: turn, Step: step, Chunk: chunk})
	if err != nil {
		t.Fatalf("分块负载排不出去：%v", err)
	}
	return Event{Type: EventAssistantChunk, Seq: seq, Time: time, Data: payload}
}

// rawChunkEvent 排一条负载由调用方指定的助手分块事件出来，用来试那些认不出的形状。
func rawChunkEvent(seq int, time int64, payload string) Event {
	return Event{
		Type: EventAssistantChunk, Seq: seq, Time: time, Data: json.RawMessage(payload),
	}
}
