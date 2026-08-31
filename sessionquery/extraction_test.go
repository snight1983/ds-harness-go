// 本文件的作用：哪些事件产出可检索的语义文字、哪些一个字都不产出。
//
// 源: packages/session-query/session-query/src/extraction.ts

package sessionquery

import (
	"encoding/json"
	"testing"

	"ds-harness-go/attachment"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestExtractEventTextTakesTheFirstPartySemanticText(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		event session.Event
		want  string
	}{
		"用户消息取正文": {
			event: userEvent(t, 0, "帮我看看这段"),
			want:  "帮我看看这段",
		},
		"助手消息取正文，推理块不进检索": {
			event: assistantEvent(t, 0, 1, 1, llm.Content{
				llm.ReasoningBlock{Text: "先想一想"},
				llm.TextBlock{Text: "看完了"},
			}),
			want: "看完了",
		},
		"助手消息里的工具调用块取名字和参数": {
			event: assistantEvent(t, 0, 1, 1, llm.Content{
				llm.ToolCallBlock{ID: "c1", Name: "read", Arguments: `{"path":"a"}`},
			}),
			want: "read\n{\"path\":\"a\"}",
		},
		"助手消息里的图片块不产出文字": {
			event: assistantEvent(t, 0, 1, 1, llm.Content{
				llm.ImageBlock{Attachment: attachment.ImageRef{}},
				llm.TextBlock{Text: "这是图"},
			}),
			want: "这是图",
		},
		"助手消息里认不得的块不产出文字": {
			event: assistantEvent(t, 0, 1, 1, llm.Content{
				llm.UnknownBlock{Kind: "future", Raw: json.RawMessage(`{"text":"别捡这个"}`)},
				llm.TextBlock{Text: "这句才算"},
			}),
			want: "这句才算",
		},
		"工具调用取名字和参数": {
			event: plainEvent(t, session.EventToolCall, 0, session.ToolCallData{
				Turn: 1, Step: 1, CallID: "c1", Name: "grep", Arguments: `{"q":"x"}`,
			}),
			want: "grep\n{\"q\":\"x\"}",
		},
		"工具结果取内容，带上失败的名字和码": {
			event: plainEvent(t, session.EventToolResult, 0, session.ToolResultData{
				Turn: 1, Step: 1,
				Message: llm.Message{
					ID: "r1", Role: llm.RoleUser,
					Content: llm.Content{llm.ToolResultBlock{
						ToolCallID: "c1",
						Content:    llm.Content{llm.TextBlock{Text: "文件不在"}},
						IsError:    true,
					}},
					Source: llm.ToolSource{CallID: "c1"},
				},
				Error: &session.ToolError{Name: "NotFound", Code: "ENOENT"},
			}),
			want: "文件不在\nNotFound\nENOENT",
		},
		"待办清单取状态和标题": {
			event: plainEvent(t, session.EventTodoWrite, 0, session.TodoWriteData{
				Todos: []session.TodoItem{
					{Content: "写测试", Status: session.TodoPending},
					{Content: "跑门禁", Status: session.TodoInProgress},
				},
			}),
			want: "pending\n写测试\nin_progress\n跑门禁",
		},
		"回合正常收尾不产出文字": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.CompletedTurnEnd{},
			}),
			want: "",
		},
		"回合出错取错误消息": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.ErrorTurnEnd{Error: llm.Failure{Message: "上游 503", Code: "UPSTREAM"}},
			}),
			want: "error\n上游 503",
		},
		"回合被取消": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.AbortedTurnEnd{Reason: session.UserCancel{}},
			}),
			want: "aborted",
		},
		"回合撞上 token 上限": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.MaxTokensTurnEnd{},
			}),
			want: "max-tokens",
		},
		"回合被打断": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.InterruptedTurnEnd{},
			}),
			want: "interrupted",
		},
		"回合被拦下不产出文字": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn: 1, Reason: session.BlockedTurnEnd{},
			}),
			want: "",
		},
		"回合结束理由认不得时不产出文字": {
			event: plainEvent(t, session.EventTurnEnd, 0, session.TurnEndData{
				Turn:   1,
				Reason: session.UnknownTurnEnd{Kind: "future", Raw: json.RawMessage(`{"kind":"future"}`)},
			}),
			want: "",
		},
		"回合开始是结构边界": {
			event: plainEvent(t, session.EventTurnStart, 0, session.TurnStartData{Turn: 1}),
			want:  "",
		},
		"步骤开始是结构边界": {
			event: plainEvent(t, session.EventStepStart, 0, session.StepStartData{Turn: 1, Step: 1}),
			want:  "",
		},
		"步骤结束是结构边界": {
			event: plainEvent(t, session.EventStepEnd, 0, session.StepEndData{Turn: 1, Step: 1}),
			want:  "",
		},
		"流式分块的内容在装配好的助手消息里": {
			event: plainEvent(t, session.EventAssistantChunk, 0, session.AssistantChunkData{Turn: 1, Step: 1, Chunk: llm.TextDeltaChunk{Index: 0, Text: "别捡这个"}}),
			want:  "",
		},
		"请求信封不是对话内容": {
			event: plainEvent(t, session.EventRequestHeader, 0, session.RequestHeaderData{}),
			want:  "",
		},
		"路由元数据不是对话内容": {
			event: plainEvent(t, session.EventRequestContext, 0, session.RequestContextData{}),
			want:  "",
		},
		"seed 边界的全部含义就是它的位置": {
			event: session.Event{Type: session.EventSessionEndSeed, Seq: 0},
			want:  "",
		},
		"本构建不认识的事件类型保持不可检索": {
			event: session.Event{
				Type: "compaction/summary",
				Seq:  0,
				Data: json.RawMessage(`{"text":"别捡这个"}`),
			},
			want: "",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := ExtractEventText(testCase.event)
			if err != nil {
				t.Fatalf("提不出文字：%v", err)
			}
			if got != testCase.want {
				t.Fatalf("提出来的文字不对：想要 %q，实际 %q", testCase.want, got)
			}
		})
	}
}

func TestExtractEventTextReportsAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	event := session.Event{
		Type: session.EventUserMessage,
		Seq:  3,
		Data: json.RawMessage(`{"message":`),
	}

	_, err := ExtractEventText(event)
	requireCode(t, err, CodeCorruptSession)
}

func TestExtractEventTextWalksIntoToolResultBlocks(t *testing.T) {
	t.Parallel()

	event := plainEvent(t, session.EventToolResult, 0, session.ToolResultData{
		Turn: 1, Step: 1,
		Message: llm.Message{
			ID: "r1", Role: llm.RoleUser,
			Content: llm.Content{llm.ToolResultBlock{
				ToolCallID: "c1",
				Content: llm.Content{
					llm.TextBlock{Text: "外层"},
					llm.ToolResultBlock{
						ToolCallID: "c2",
						Content:    llm.Content{llm.TextBlock{Text: "内层"}},
					},
				},
			}},
			Source: llm.ToolSource{CallID: "c1"},
		},
	})

	got, err := ExtractEventText(event)
	if err != nil {
		t.Fatalf("提不出文字：%v", err)
	}
	if got != "外层\n内层" {
		t.Fatalf("嵌套的工具结果块没有全部提出来：%q", got)
	}
}
