// 本文件的作用：请求头的规范形式、相等判断，以及从日志里把它折回来。
//
// 源: packages/core/session/src/request-header.ts

package sessionlog

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestSessionHeaderDropsItsAbsentOptionalFields(t *testing.T) {
	t.Parallel()

	minimal := SessionHeader{Version: FormatVersion, ID: "s1", CreatedAt: 7}
	got, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	want := `{"version":0,"id":"s1","createdAt":7}`
	if string(got) != want {
		t.Fatalf("排出来的字节不对：\n想要 %s\n实际 %s", want, got)
	}

	full := SessionHeader{
		Version: FormatVersion, ID: "s2", CreatedAt: 8,
		WorkspaceID: "ws-1", ParentSession: "s1", SeedLength: 3,
		Origin: OriginSubagent, DelegationDepth: 1, AgentPreset: "coder",
	}
	line, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back SessionHeader
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back != full {
		t.Fatalf("往返之后不一样了：\n想要 %#v\n实际 %#v", full, back)
	}
}

func TestEpochHeaderMarshalsCanonically(t *testing.T) {
	t.Parallel()

	config := llm.CallConfig{Provider: "p", Model: "m"}

	cases := map[string]struct {
		header EpochHeader
		want   string
	}{
		"空的系统提示与空的工具表整个消失": {
			header: EpochHeader{Config: config},
			want:   `{"config":{"provider":"p","model":"m"}}`,
		},
		"两个适配器标记都是假时那个对象也消失": {
			header: EpochHeader{Config: config, Tools: []llm.ToolSchema{}},
			want:   `{"config":{"provider":"p","model":"m"}}`,
		},
		"任一标记为真那个对象就在": {
			header: EpochHeader{
				Config:          config,
				AdapterDefaults: llm.CallConfigAdapterDefaults{MaxTokens: true},
			},
			want: `{"config":{"provider":"p","model":"m"},"adapterDefaults":{"maxTokens":true}}`,
		},
		"系统提示与工具表非空时原样带上": {
			header: EpochHeader{
				Config: config, System: "be brief",
				Tools: []llm.ToolSchema{{
					Name: "read", Description: "read a file",
					Parameters: json.RawMessage(`{"type":"object"}`),
				}},
			},
			want: `{"config":{"provider":"p","model":"m"},"system":"be brief",` +
				`"tools":[{"name":"read","description":"read a file","parameters":{"type":"object"}}]}`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.header)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：\n想要 %s\n实际 %s", testCase.want, got)
			}

			var back EpochHeader
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if !HeaderEquals(CanonicalHeader(testCase.header), back) {
				t.Fatalf("往返之后不相等了：\n想要 %#v\n实际 %#v", testCase.header, back)
			}
		})
	}
}

func TestEpochHeaderRejectsBrokenBytes(t *testing.T) {
	t.Parallel()

	var header EpochHeader
	if err := json.Unmarshal([]byte(`7`), &header); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestCanonicalHeaderNormalizesTheEmptyForms(t *testing.T) {
	t.Parallel()

	got := CanonicalHeader(EpochHeader{
		Config: llm.CallConfig{Provider: "p", Model: "m"},
		Tools:  []llm.ToolSchema{},
	})
	if got.Tools != nil {
		t.Fatalf("空的工具表应该收成 nil，实际 %#v", got.Tools)
	}
	if (got.AdapterDefaults != llm.CallConfigAdapterDefaults{}) {
		t.Fatalf("两个标记都是假时适配器默认应该是零值，实际 %#v", got.AdapterDefaults)
	}

	kept := CanonicalHeader(EpochHeader{
		Config:          llm.CallConfig{Provider: "p", Model: "m"},
		AdapterDefaults: llm.CallConfigAdapterDefaults{ReasoningEffort: true},
	})
	if !kept.AdapterDefaults.ReasoningEffort {
		t.Fatalf("为真的标记被抹掉了")
	}
}

func TestHeaderEqualsComparesEveryFieldThatShapesARequest(t *testing.T) {
	t.Parallel()

	base := EpochHeader{
		Config:          llm.CallConfig{Provider: "p", Model: "m", MaxTokens: 100},
		AdapterDefaults: llm.CallConfigAdapterDefaults{MaxTokens: true},
		System:          "sys",
		Tools: []llm.ToolSchema{
			{Name: "a", Description: "da", Parameters: json.RawMessage(`{"x":1}`)},
			{Name: "b", Description: "db", Parameters: json.RawMessage(`{}`)},
		},
	}

	same := base
	same.Tools = append([]llm.ToolSchema(nil), base.Tools...)
	if !HeaderEquals(base, same) {
		t.Fatalf("两份一模一样的头被判成不等")
	}

	cases := map[string]func(h *EpochHeader){
		"配置变了":     func(h *EpochHeader) { h.Config.Model = "m2" },
		"适配器标记变了":  func(h *EpochHeader) { h.AdapterDefaults.ReasoningEffort = true },
		"系统提示变了":   func(h *EpochHeader) { h.System = "sys2" },
		"工具少了一个":   func(h *EpochHeader) { h.Tools = h.Tools[:1] },
		"工具名变了":    func(h *EpochHeader) { h.Tools[0].Name = "z" },
		"工具说明变了":   func(h *EpochHeader) { h.Tools[0].Description = "z" },
		"工具参数字节变了": func(h *EpochHeader) { h.Tools[0].Parameters = json.RawMessage(`{"x":2}`) },
		"工具顺序变了": func(h *EpochHeader) {
			h.Tools[0], h.Tools[1] = h.Tools[1], h.Tools[0]
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			other := base
			other.Tools = append([]llm.ToolSchema(nil), base.Tools...)
			mutate(&other)
			if HeaderEquals(base, other) {
				t.Fatalf("改动之后仍被判成相等：%#v", other)
			}
		})
	}
}

func TestFoldRequestHeaderKeepsTheLastSnapshot(t *testing.T) {
	t.Parallel()

	first := EpochHeader{Config: llm.CallConfig{Provider: "p", Model: "m1"}}
	second := EpochHeader{Config: llm.CallConfig{Provider: "p", Model: "m2"}}

	events := []Event{
		headerEvent(t, 0, first, HeaderInitial),
		{Type: EventTurnStart, Seq: 1, Data: json.RawMessage(`{"turn":1}`)},
		headerEvent(t, 2, second, HeaderChange),
	}

	got, ok, err := FoldRequestHeader(events, EpochHeader{}, false)
	if err != nil {
		t.Fatalf("折不出来：%v", err)
	}
	if !ok {
		t.Fatalf("这段日志里有头事件，第二个返回值不该是假")
	}
	if !HeaderEquals(got, second) {
		t.Fatalf("折出来的不是最后一份快照：%#v", got)
	}
}

func TestFoldRequestHeaderOnALogWithoutHeaders(t *testing.T) {
	t.Parallel()

	events := []Event{{Type: EventTurnStart, Seq: 0, Data: json.RawMessage(`{"turn":1}`)}}

	if _, ok, err := FoldRequestHeader(events, EpochHeader{}, false); err != nil || ok {
		t.Fatalf("一条头事件都没有时应该返回假：ok=%v err=%v", ok, err)
	}

	carried := EpochHeader{Config: llm.CallConfig{Provider: "p", Model: "m"}}
	got, ok, err := FoldRequestHeader(events, carried, true)
	if err != nil || !ok || !HeaderEquals(got, carried) {
		t.Fatalf("上一次折出来的状态应该原样带过来：got=%#v ok=%v err=%v", got, ok, err)
	}
}

func TestFoldRequestHeaderReportsABrokenPayload(t *testing.T) {
	t.Parallel()

	events := []Event{{Type: EventRequestHeader, Seq: 0, Data: json.RawMessage(`7`)}}
	if _, _, err := FoldRequestHeader(events, EpochHeader{}, false); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestRequestContextDropsAnUnpublishedWindow(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(RequestContext{Provider: "p", Model: "m"})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	want := `{"provider":"p","model":"m"}`
	if string(got) != want {
		t.Fatalf("排出来的字节不对：想要 %s，实际 %s", want, got)
	}
}

func TestTodoItemRoundTrips(t *testing.T) {
	t.Parallel()

	todos := []TodoItem{
		{Content: "读日志", Status: TodoCompleted},
		{Content: "折表面", Status: TodoInProgress},
		{Content: "补收尾", Status: TodoPending},
	}
	line, err := json.Marshal(todos)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back []TodoItem
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if !reflect.DeepEqual(todos, back) {
		t.Fatalf("往返之后不一样了：\n想要 %#v\n实际 %#v", todos, back)
	}
}

// headerEvent 排一条请求头事件出来，测试里用。
func headerEvent(t *testing.T, seq int, header EpochHeader, reason RequestHeaderReason) Event {
	t.Helper()

	payload, err := json.Marshal(RequestHeaderData{Header: header, Reason: reason})
	if err != nil {
		t.Fatalf("请求头负载排不出去：%v", err)
	}
	return Event{Type: EventRequestHeader, Seq: seq, Data: payload}
}
