// 本文件的作用：一条日志条目的信封在介质上的往返，以及信封那道严格的键检查。
//
// 源: packages/core/session/src/types.ts:378-423

package sessionlog

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestEventMarshalsItsEnvelope(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		event Event
		want  string
	}{
		"最小的一条：空负载排成一个空对象": {
			event: Event{Type: EventSessionEndSeed, Seq: 4, Time: 17},
			want:  `{"type":"session/end-seed","seq":4,"time":17,"data":{}}`,
		},
		"可跳过的标记只在为真时出现": {
			event: Event{Type: "x/y", Seq: 0, Time: 1, Data: json.RawMessage(`{"a":1}`), Ignorable: true},
			want:  `{"type":"x/y","seq":0,"time":1,"data":{"a":1},"ignorable":true}`,
		},
		"追加的表面事件带一个裸字符串": {
			event: Event{
				Type: EventUserMessage, Seq: 2, Time: 3,
				Data: json.RawMessage(`{}`), SurfaceOp: AppendOp{},
			},
			want: `{"type":"user/message","seq":2,"time":3,"data":{},"surfaceOp":"append"}`,
		},
		"替换的表面事件带区间和来源": {
			event: Event{
				Type: EventToolResult, Seq: 9, Time: 3,
				Data:            json.RawMessage(`{}`),
				SurfaceOp:       ReplaceOp{Start: 4, End: 5},
				SourceEventSeqs: []int{4, 5},
			},
			want: `{"type":"tool/result","seq":9,"time":3,"data":{},` +
				`"surfaceOp":{"op":"replace","start":4,"end":5},"sourceEventSeqs":[4,5]}`,
		},
		"明确给了一个空的来源清单": {
			event: Event{
				Type: EventAssistantMessage, Seq: 1, Time: 2,
				Data: json.RawMessage(`{}`), SurfaceOp: AppendOp{},
				SourceEventSeqs: []int{},
			},
			want: `{"type":"assistant/message","seq":1,"time":2,"data":{},` +
				`"surfaceOp":"append","sourceEventSeqs":[]}`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.event)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：\n想要 %s\n实际 %s", testCase.want, got)
			}
		})
	}
}

func TestEventTellsAnEmptySourceListFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		line string
		want []int
	}{
		"没给这个字段":   {line: `{"type":"a","seq":0,"time":0,"data":{}}`, want: nil},
		"给了个空清单":   {line: `{"type":"a","seq":0,"time":0,"data":{},"sourceEventSeqs":[]}`, want: []int{}},
		"给了个 null": {line: `{"type":"a","seq":0,"time":0,"data":{},"sourceEventSeqs":null}`, want: []int{}},
		"给了两个 seq": {line: `{"type":"a","seq":0,"time":0,"data":{},"sourceEventSeqs":[1,2]}`, want: []int{1, 2}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var event Event
			if err := json.Unmarshal([]byte(testCase.line), &event); err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if (event.SourceEventSeqs == nil) != (testCase.want == nil) {
				t.Fatalf("「没给」和「空清单」被弄混了：想要 %#v，实际 %#v",
					testCase.want, event.SourceEventSeqs)
			}
			if !reflect.DeepEqual(event.SourceEventSeqs, testCase.want) {
				t.Fatalf("来源清单不对：想要 %#v，实际 %#v", testCase.want, event.SourceEventSeqs)
			}
		})
	}
}

func TestEventRejectsAnUnknownEnvelopeKey(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"信封上多了一个键":  `{"type":"a","seq":0,"time":0,"data":{},"epoch":3}`,
		"压根不是一个对象":  `[1,2,3]`,
		"seq 的类型不对": `{"type":"a","seq":"0","time":0,"data":{}}`,
		"表面操作读不回来":  `{"type":"a","seq":0,"time":0,"data":{},"surfaceOp":"squash"}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var event Event
			if err := json.Unmarshal([]byte(line), &event); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

func TestEventRoundTripsThroughTheWire(t *testing.T) {
	t.Parallel()

	original := Event{
		Type: EventToolResult, Seq: 12, Time: 1700000000123,
		Data:            json.RawMessage(`{"turn":1}`),
		Ignorable:       true,
		SurfaceOp:       ReplaceOp{Start: 8, End: 9},
		SourceEventSeqs: []int{8, 9},
	}

	line, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back Event
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if !reflect.DeepEqual(original, back) {
		t.Fatalf("往返之后不一样了：\n想要 %#v\n实际 %#v", original, back)
	}
}

func TestEventCloneDetachesTheSlices(t *testing.T) {
	t.Parallel()

	original := Event{
		Type: EventToolCall, Seq: 1, Time: 2,
		Data:            json.RawMessage(`{"a":1}`),
		SourceEventSeqs: []int{7},
	}
	clone := original.Clone()
	clone.Data[0] = 'X'
	clone.SourceEventSeqs[0] = 99

	if string(original.Data) != `{"a":1}` {
		t.Fatalf("复制品改了原件的负载：%s", original.Data)
	}
	if original.SourceEventSeqs[0] != 7 {
		t.Fatalf("复制品改了原件的来源清单：%v", original.SourceEventSeqs)
	}

	if bare := (Event{Type: "a"}).Clone(); bare.SourceEventSeqs != nil {
		t.Fatalf("没给的来源清单复制之后应该还是没给，实际 %#v", bare.SourceEventSeqs)
	}
}

// unmarshalableOp 是一个排不出去的表面操作，用来走到 [Event.MarshalJSON] 的错误分支。
type unmarshalableOp struct{}

func (unmarshalableOp) SurfaceOpKind() SurfaceOpKind { return OpAppend }

func (unmarshalableOp) sealedSurfaceOp() {}

func (unmarshalableOp) MarshalJSON() ([]byte, error) { return nil, errors.New("排不出去") }

func TestEventReportsASurfaceOpThatCannotBeMarshaled(t *testing.T) {
	t.Parallel()

	event := Event{Type: EventUserMessage, Seq: 0, Time: 0, SurfaceOp: unmarshalableOp{}}
	if _, err := json.Marshal(event); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}
