// 本文件的作用：从一条事件里提出「可检索的语义文字」。
//
// 源: packages/session-query/session-query/src/extraction.ts

package sessionquery

import (
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ExtractEventText 提取一条第一方事件里可检索的语义文字。
//
// 源: packages/session-query/session-query/src/extraction.ts:13-40
//
// 结构性边界、流式分块、请求信封、以及本构建不认识的事件，一律不产出文字——
// 它们要么没有语义内容，要么内容已经在别的事件里完整存在（分块之于装配好的
// 助手消息就是后者），重复索引一遍只会让同一句话命中两次。
//
// 新增: DSH 的 extractSessionEventText 拿到的是已经带好类型的负载，读不坏，
// 所以它不返回错误。Go 这边 [sessionlog.Event.Data] 是原始字节，解不回来说明
// 这份日志坏了——那是一件必须说出来的事，不是「这条没有可检索的文字」。
// 把它压成空串会让一份坏日志安静地检索不出东西，那比报错难查得多。
func ExtractEventText(event sessionlog.Event) (string, error) {
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		return "", wrap(CodeCorruptSession, err, "会话 seq %d 的 %s 负载读不回来", event.Seq, event.Type)
	}
	switch payload := data.(type) {
	case sessionlog.UserMessageData:
		return contentText(payload.Content), nil
	case sessionlog.AssistantMessageData:
		return contentText(payload.Message.Content), nil
	case sessionlog.ToolCallData:
		return joinText([]string{payload.Name, payload.Arguments}), nil
	case sessionlog.ToolResultData:
		parts := []string{contentText(payload.Message.Content)}
		if payload.Error != nil {
			parts = append(parts, payload.Error.Name, payload.Error.Code)
		}
		return joinText(parts), nil
	case sessionlog.TodoWriteData:
		parts := make([]string, 0, len(payload.Todos)*2)
		for _, todo := range payload.Todos {
			parts = append(parts, string(todo.Status), todo.Content)
		}
		return joinText(parts), nil
	case sessionlog.TurnEndData:
		return turnEndText(payload.Reason), nil
	case sessionlog.TurnStartData, sessionlog.StepStartData, sessionlog.StepEndData,
		sessionlog.AssistantChunkData, sessionlog.RequestHeaderData:
		// 源: packages/session-query/session-query/src/extraction.ts:32-37
		// 结构性边界、原始分块、请求信封：没有第一方语义文字。
		return "", nil
	case sessionlog.RequestContextData, sessionlog.EndSeedData:
		// 新增: 这两个类型 DSH 的 switch 没有列举，落在它的 default 上。
		// 显式列出来给的是同样的空结果，差别只在于下一个读代码的人能看出
		// 这是想清楚了的：路由元数据是投递决策不是对话内容，
		// seed 边界的全部含义就是它的位置。
		return "", nil
	default:
		// 源: packages/session-query/session-query/src/extraction.ts:38-40
		// 事件类型是开放的。一个本构建不认识的类型在有人定义它的语义之前
		// 保持不可检索——负载里恰好有字符串，不构成把它当语义文字的理由。
		return "", nil
	}
}

// turnEndText 提取一个回合结束理由里的语义文字。
//
// 源: packages/session-query/session-query/src/extraction.ts:42-58
func turnEndText(reason sessionlog.TurnEndReason) string {
	switch typed := reason.(type) {
	case sessionlog.ErrorTurnEnd:
		return joinText([]string{"error", typed.Error.Message})
	case sessionlog.AbortedTurnEnd:
		return "aborted"
	case sessionlog.MaxTokensTurnEnd, sessionlog.InterruptedTurnEnd:
		return string(reason.TurnEndReasonKind())
	case sessionlog.CompletedTurnEnd:
		// 源: packages/session-query/session-query/src/extraction.ts:50
		// 正常收尾没有任何可说的：它是常态，索引它等于索引每一个回合。
		return ""
	case sessionlog.BlockedTurnEnd:
		// 新增: DSH 的 switch 没有列举 blocked，落在它的 default 上。
		// 显式列出来给同样的空结果：被拦下的回合为什么被拦，理由在拦它的那一处
		// 的事件里，这条只记了「没进步骤就结束了」这个结构事实。
		return ""
	default:
		// 源: packages/session-query/session-query/src/extraction.ts:56-58
		// 理由这个联合是开放的。哪一部分算语义、哪一部分算结构，
		// 由新增它的那一方定，在那之前不猜。
		return ""
	}
}

// contentText 把一串内容块里的语义文字拼起来。
//
// 源: packages/session-query/session-query/src/extraction.ts:62-64
func contentText(content llm.Content) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		parts = append(parts, blockText(block)...)
	}
	return joinText(parts)
}

// blockText 给出一个内容块贡献的那些文字片段。
//
// 源: packages/session-query/session-query/src/extraction.ts:66-81
func blockText(block llm.ContentBlock) []string {
	switch typed := block.(type) {
	case llm.TextBlock:
		return []string{typed.Text}
	case llm.ReasoningBlock:
		// 源: packages/session-query/session-query/src/extraction.ts:70-71
		// 推理内容不是给最终用户看的那句话，不进检索。
		return nil
	case llm.ToolCallBlock:
		return []string{typed.Name, typed.Arguments}
	case llm.ToolResultBlock:
		var parts []string
		for _, inner := range typed.Content {
			parts = append(parts, blockText(inner)...)
		}
		return parts
	case llm.ImageBlock:
		// 新增: DSH 的 switch 没有列举 image，落在它的 default 上。
		// 显式列出来给同样的空结果：一张图的可检索文字得由别处（描述、
		// 转写）提供，附件引用本身不是文字。
		return nil
	default:
		// 源: packages/session-query/session-query/src/extraction.ts:78-81
		// 内容块联合是开放的。一个不认识的块不会因为负载里恰好有字符串
		// 就变成可检索的。
		return nil
	}
}

// joinText 去掉每段两头的空白、丢掉空段，再用换行拼起来。
//
// 源: packages/session-query/session-query/src/extraction.ts:83-85
func joinText(parts []string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n")
}
