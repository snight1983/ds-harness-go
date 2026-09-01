// 本文件的作用：十三个负载各自的介质形状，以及按事件类型把它们解出来那件事。
//
// 源: packages/core/session/src/types.ts:236-337

package session

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestDecodeDataCoversEveryRegisteredType(t *testing.T) {
	t.Parallel()

	toolName := "read"

	cases := map[string]struct {
		kind    EventType
		payload string
		want    EventData
	}{
		"回合开始": {
			kind: EventTurnStart, payload: `{"turn":2}`,
			want: TurnStartData{Turn: 2},
		},
		"回合结束": {
			kind: EventTurnEnd, payload: `{"turn":2,"reason":{"kind":"completed"}}`,
			want: TurnEndData{Turn: 2, Reason: CompletedTurnEnd{}},
		},
		"步骤开始": {
			kind: EventStepStart, payload: `{"turn":2,"step":1}`,
			want: StepStartData{Turn: 2, Step: 1},
		},
		"步骤结束": {
			kind: EventStepEnd, payload: `{"turn":2,"step":1}`,
			want: StepEndData{Turn: 2, Step: 1},
		},
		"用户消息": {
			kind:    EventUserMessage,
			payload: `{"id":"m1","role":"user","content":[{"type":"text","text":"hi"}],"source":{"kind":"user"}}`,
			want: UserMessageData{Message: llm.Message{
				ID: "m1", Role: llm.RoleUser,
				Content: llm.Content{llm.TextBlock{Text: "hi"}},
				Source:  llm.UserSource{},
			}},
		},
		"助手分块": {
			kind:    EventAssistantChunk,
			payload: `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"a"}}`,
			want: AssistantChunkData{
				Turn: 1, Step: 1, Chunk: llm.TextDeltaChunk{Index: 0, Text: "a"},
			},
		},
		"待办快照": {
			kind: EventTodoWrite, payload: `{"todos":[{"content":"x","status":"pending"}]}`,
			want: TodoWriteData{Todos: []TodoItem{{Content: "x", Status: TodoPending}}},
		},
		"路由元数据": {
			kind: EventRequestContext, payload: `{"provider":"p","model":"m","contextWindow":128}`,
			want: RequestContextData{RequestContext: RequestContext{
				Provider: "p", Model: "m", ContextWindow: 128,
			}},
		},
		"seed 的结尾": {
			kind: EventSessionEndSeed, payload: `{}`,
			want: EndSeedData{},
		},
		"工具调用": {
			kind:    EventToolCall,
			payload: `{"turn":1,"step":1,"callId":"c1","name":"read","arguments":"{\"path\":\"a\"}"}`,
			want: ToolCallData{
				Turn: 1, Step: 1, CallID: "c1", Name: toolName, Arguments: `{"path":"a"}`,
			},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			event := Event{Type: testCase.kind, Data: json.RawMessage(testCase.payload)}
			got, err := DecodeData(event)
			if err != nil {
				t.Fatalf("解不出来：%v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("解出来的负载不对：\n想要 %#v\n实际 %#v", testCase.want, got)
			}
			if got.EventType() != testCase.kind {
				t.Fatalf("负载自报的类型不对：想要 %q，实际 %q", testCase.kind, got.EventType())
			}
		})
	}
}

func TestDecodeDataReadsTheStructuredPayloads(t *testing.T) {
	t.Parallel()

	t.Run("助手消息带记账与中断标记", func(t *testing.T) {
		t.Parallel()

		payload := `{"turn":1,"step":2,"message":{"id":"m","role":"assistant",` +
			`"content":[{"type":"text","text":"hi"}],"source":{"kind":"model","provider":"p","model":"m"}},` +
			`"usage":{"inputTokens":3,"outputTokens":4},"interrupted":true}`
		got, err := DecodeData(Event{Type: EventAssistantMessage, Data: json.RawMessage(payload)})
		if err != nil {
			t.Fatalf("解不出来：%v", err)
		}
		data, ok := got.(AssistantMessageData)
		if !ok {
			t.Fatalf("想要 AssistantMessageData，实际 %T", got)
		}
		if data.Turn != 1 || data.Step != 2 || !data.Interrupted {
			t.Fatalf("信封字段不对：%#v", data)
		}
		if data.Usage == nil || data.Usage.OutputTokens != 4 {
			t.Fatalf("记账不对：%#v", data.Usage)
		}
		if data.EventType() != EventAssistantMessage {
			t.Fatalf("负载自报的类型不对：%q", data.EventType())
		}
	})

	t.Run("没报记账时那个指针是 nil", func(t *testing.T) {
		t.Parallel()

		payload := `{"turn":1,"step":2,"message":{"id":"m","role":"assistant",` +
			`"content":[],"source":{"kind":"model","provider":"p","model":"m"}}}`
		got, err := DecodeData(Event{Type: EventAssistantMessage, Data: json.RawMessage(payload)})
		if err != nil {
			t.Fatalf("解不出来：%v", err)
		}
		if got.(AssistantMessageData).Usage != nil {
			t.Fatalf("没报记账时不该有记账")
		}
	})

	t.Run("工具结果带错误身份与工具私有负载", func(t *testing.T) {
		t.Parallel()

		payload := `{"turn":1,"step":1,"message":{"id":"m","role":"user",` +
			`"content":[{"type":"tool-result","toolCallId":"c1","content":[],"isError":true}],` +
			`"source":{"kind":"tool","callId":"c1"}},` +
			`"error":{"name":"E","code":"X"},"meta":{"lines":3}}`
		got, err := DecodeData(Event{Type: EventToolResult, Data: json.RawMessage(payload)})
		if err != nil {
			t.Fatalf("解不出来：%v", err)
		}
		data := got.(ToolResultData)
		if data.Error == nil || *data.Error != (ToolError{Name: "E", Code: "X"}) {
			t.Fatalf("错误身份不对：%#v", data.Error)
		}
		if string(data.Meta) != `{"lines":3}` {
			t.Fatalf("工具私有负载没原样保管：%s", data.Meta)
		}
		if data.EventType() != EventToolResult {
			t.Fatalf("负载自报的类型不对：%q", data.EventType())
		}
	})

	t.Run("请求头快照", func(t *testing.T) {
		t.Parallel()

		payload := `{"header":{"config":{"provider":"p","model":"m"}},"reason":"initial"}`
		got, err := DecodeData(Event{Type: EventRequestHeader, Data: json.RawMessage(payload)})
		if err != nil {
			t.Fatalf("解不出来：%v", err)
		}
		data := got.(RequestHeaderData)
		if data.Reason != HeaderInitial || data.Header.Config.Model != "m" {
			t.Fatalf("请求头负载不对：%#v", data)
		}
		if data.EventType() != EventRequestHeader {
			t.Fatalf("负载自报的类型不对：%q", data.EventType())
		}
	})
}

func TestDecodeDataKeepsAnUnregisteredPayloadByteForByte(t *testing.T) {
	t.Parallel()

	raw := `{"summary":"…","droppedSeqs":[1,2,3]}`
	got, err := DecodeData(Event{Type: "compaction/summary", Data: json.RawMessage(raw)})
	if err != nil {
		t.Fatalf("解不出来：%v", err)
	}
	data, ok := got.(RawData)
	if !ok {
		t.Fatalf("想要 RawData，实际 %T", got)
	}
	if data.EventType() != "compaction/summary" {
		t.Fatalf("类型不对：%q", data.EventType())
	}
	back, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(back) != raw {
		t.Fatalf("原样保管失败：\n想要 %s\n实际 %s", raw, back)
	}
}

func TestDecodeDataTreatsAnEmptyPayloadAsAnEmptyObject(t *testing.T) {
	t.Parallel()

	got, err := DecodeData(Event{Type: EventTurnStart})
	if err != nil {
		t.Fatalf("解不出来：%v", err)
	}
	if got != (TurnStartData{}) {
		t.Fatalf("空负载应该解成零值：%#v", got)
	}

	empty, err := DecodeData(Event{Type: "x/y"})
	if err != nil {
		t.Fatalf("解不出来：%v", err)
	}
	line, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(line) != `{}` {
		t.Fatalf("没有原始字节的未知负载应该排成空对象，实际 %s", line)
	}
}

func TestDecodeDataReportsABrokenPayload(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		kind    EventType
		payload string
	}{
		"回合开始的 turn 不是个数": {kind: EventTurnStart, payload: `{"turn":"2"}`},
		"回合结束没带理由":        {kind: EventTurnEnd, payload: `{"turn":2}`},
		"回合结束的理由认不出":      {kind: EventTurnEnd, payload: `{"turn":2,"reason":{"kind":"aborted"}}`},
		"回合结束不是个对象":       {kind: EventTurnEnd, payload: `7`},
		"助手分块没带分块":        {kind: EventAssistantChunk, payload: `{"turn":1,"step":1}`},
		"助手分块不是个对象":       {kind: EventAssistantChunk, payload: `7`},
		"助手分块的类型认不出":      {kind: EventAssistantChunk, payload: `{"turn":1,"step":1,"chunk":{"type":"martian"}}`},
		"用户消息不是一条消息":      {kind: EventUserMessage, payload: `7`},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			event := Event{Type: testCase.kind, Data: json.RawMessage(testCase.payload)}
			if _, err := DecodeData(event); err == nil {
				t.Fatalf("想要报错，实际解出来了")
			}
		})
	}
}

func TestPayloadsMarshalToTheExpectedWire(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		data EventData
		want string
	}{
		"回合结束":        {data: TurnEndData{Turn: 3, Reason: MaxTokensTurnEnd{}}, want: `{"turn":3,"reason":{"kind":"max-tokens"}}`},
		"助手分块":        {data: AssistantChunkData{Turn: 1, Step: 1, Chunk: llm.ReasoningDeltaChunk{Index: 0, Text: "t"}}, want: `{"turn":1,"step":1,"chunk":{"type":"reasoning-delta","index":0,"text":"t"}}`},
		"seed 的结尾是空的": {data: EndSeedData{}, want: `{}`},
		"步骤开始":        {data: StepStartData{Turn: 1, Step: 2}, want: `{"turn":1,"step":2}`},
		"步骤结束":        {data: StepEndData{Turn: 1, Step: 2}, want: `{"turn":1,"step":2}`},
		"工具调用":        {data: ToolCallData{Turn: 1, Step: 1, CallID: "c", Name: "n", Arguments: "{}"}, want: `{"turn":1,"step":1,"callId":"c","name":"n","arguments":"{}"}`},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.data)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：\n想要 %s\n实际 %s", testCase.want, got)
			}
		})
	}
}

func TestPayloadsRefuseToBeMarshaledWithoutTheirRequiredMember(t *testing.T) {
	t.Parallel()

	if _, err := json.Marshal(TurnEndData{Turn: 1}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("回合结束缺理由时想要 ErrMalformedValue，实际 %v", err)
	}
	if _, err := json.Marshal(AssistantChunkData{Turn: 1, Step: 1}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("助手分块缺分块时想要 ErrMalformedValue，实际 %v", err)
	}
	_, err := json.Marshal(TurnEndData{Turn: 1, Reason: UnknownTurnEnd{Kind: "x"}})
	if !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("理由排不出去时想要 ErrMalformedValue，实际 %v", err)
	}
}
