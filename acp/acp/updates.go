// 本文件的作用：把已提交的会话事件翻成 ACP 线上那几种标准更新——推理、助手消息、
// 上下文占用，以及一次工具调用的开始和收尾。
//
// 源: packages/acp/acp/src/updates.ts:1-111

package acp

import (
	"context"
	"encoding/json"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/attachment"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// TokenMeasurer 是这座桥用得到的那一块计量：只问「这条会话当下占了多少 token」。
//
// 源: packages/acp/acp/src/updates.ts:95（ctx.get('tokenMeter')）
//
// 新增: DSH 是 `ctx.get('tokenMeter')` 拿到整个服务，然后 `meter.measure(session)`。
// Go 这边 [github.com/snight1983/ds-harness-go/llm/tokenmeter.TokenMeter.Measure] 收的是一份
// 投影视图和一份请求头，不是会话本身——那两样要靠投影注册表才取得到，而这座桥不
// 拥有它。所以这里收窄成「给我一条会话、还我一个数」，那一步适配由装配方做：它
// 才是同时握着计量器和投影注册表的那一位。
//
// 它可以为 nil，对应 DSH 那个 `meter === undefined`：这条线上没挂计量，那么占用
// 更新就一条都不发。
type TokenMeasurer interface {
	// MeasureSession 交出这条会话当下占用的 token 数。
	MeasureSession(ctx context.Context, sess *coresession.Session) (int, error)
}

// AssistantUpdates 把一条已提交的助手消息按块序翻成那几条标准更新。
//
// 源: packages/acp/acp/src/updates.ts:9-45（assistantUpdates）
//
// 次序就是块序：推理块出 agent_thought_chunk，别的块出 agent_message_chunk，
// 最后可选地跟一条 usage_update。空的推理块不发——它没有任何可显示的东西。
func AssistantUpdates(
	ctx context.Context,
	attachments attachment.Store,
	meter TokenMeasurer,
	sess *coresession.Session,
	data sessionlog.AssistantMessageData,
) ([]wire.SessionUpdate, error) {
	messageID := string(data.Message.ID)
	var updates []wire.SessionUpdate
	for _, block := range data.Message.Content {
		if reasoning, isReasoning := block.(llm.ReasoningBlock); isReasoning {
			if reasoning.Text == "" {
				continue
			}
			updates = append(updates, wire.SessionUpdate{
				AgentThoughtChunk: &wire.SessionUpdateAgentThoughtChunk{
					MessageId: &messageID,
					Content:   wire.TextBlock(reasoning.Text),
				},
			})
			continue
		}
		converted, onWire, err := AssistantBlockToACP(ctx, attachments, block)
		if err != nil {
			return nil, err
		}
		if !onWire {
			continue
		}
		updates = append(updates, wire.SessionUpdate{
			AgentMessageChunk: &wire.SessionUpdateAgentMessageChunk{
				MessageId: &messageID,
				Content:   converted,
			},
		})
	}
	if usage, ok := usageUpdate(ctx, meter, sess, data); ok {
		updates = append(updates, usage)
	}
	return updates, nil
}

// usageUpdate 只在**用量和容量两件事实都有**的时候才报上下文占用。
//
// 源: packages/acp/acp/src/updates.ts:86-102
//
// 三个缺一不可：这个步骤的适配器报了记账（否则这一步根本没花掉什么可报的）、
// 这条路由公布了上下文上限（否则那个百分比没有分母）、以及这条线上挂了计量器。
// 少一样就不发——发一条分母是 0 的占用，比不发更糟。
//
// 新增: 计量失败在这里被咽掉，只当作「这一条不发」。DSH 那边 measure() 不会失败；
// Go 这边它会（投影重放读不动一条事件）。一次报不出来的占用不该把这条已经提交的
// 助手消息也一起拦下——那条消息是真的，占用只是它的注脚。
func usageUpdate(
	ctx context.Context,
	meter TokenMeasurer,
	sess *coresession.Session,
	data sessionlog.AssistantMessageData,
) (wire.SessionUpdate, bool) {
	if data.Usage == nil || meter == nil || sess == nil {
		return wire.SessionUpdate{}, false
	}
	requestContext, ok, err := sess.RequestContext()
	if err != nil || !ok || requestContext.ContextWindow == 0 {
		return wire.SessionUpdate{}, false
	}
	used, err := meter.MeasureSession(ctx, sess)
	if err != nil {
		return wire.SessionUpdate{}, false
	}
	return wire.SessionUpdate{UsageUpdate: &wire.SessionUsageUpdate{
		Used: used,
		Size: requestContext.ContextWindow,
	}}, true
}

// ToolCallUpdate 从那条耐久的调用事实上，起一次通用的 ACP 工具生命周期。
//
// 源: packages/acp/acp/src/updates.ts:47-61（toolCallUpdate）
//
// kind 一律是 other：本仓库的工具不往 ACP 那套图标分类上映射，那是给界面猜图标用的，
// 猜错了比不猜更误导人。
func ToolCallUpdate(data sessionlog.ToolCallData) wire.SessionUpdate {
	return wire.SessionUpdate{ToolCall: &wire.SessionUpdateToolCall{
		ToolCallId: wire.ToolCallId(data.CallID),
		Title:      data.Name,
		Kind:       wire.ToolKindOther,
		Status:     wire.ToolCallStatusInProgress,
		RawInput:   parseToolArguments(data.Arguments),
	}}
}

// ToolResultUpdate 用那份已提交的、面向模型的结果收掉这次工具生命周期。
//
// 源: packages/acp/acp/src/updates.ts:63-85（toolResultUpdate）
//
// 第二个返回值为假表示这条结果**不上线**：一条结果消息的第一个块不是工具结果块，
// 那它就指不出自己收的是哪一次调用。
//
// 新增: DSH 直接 `event.data.message.content[0]` 取那个块，靠 TS 的类型撑着。Go 里
// `content` 是一串接口值，空切片和别的块型在类型上都可能，所以这里显式判一次——
// 一次静默的下标越界会让这条线崩在一个通知处理器里。
func ToolResultUpdate(
	ctx context.Context,
	attachments attachment.Store,
	data sessionlog.ToolResultData,
) (wire.SessionUpdate, bool, error) {
	if len(data.Message.Content) == 0 {
		return wire.SessionUpdate{}, false, nil
	}
	result, isResult := data.Message.Content[0].(llm.ToolResultBlock)
	if !isResult {
		return wire.SessionUpdate{}, false, nil
	}
	content := make([]wire.ToolCallContent, 0, len(result.Content))
	for _, block := range result.Content {
		converted, onWire, err := AssistantBlockToACP(ctx, attachments, block)
		if err != nil {
			return wire.SessionUpdate{}, false, err
		}
		if !onWire {
			continue
		}
		content = append(content, wire.ToolCallContent{
			Content: &wire.ToolCallContentContent{Type: "content", Content: converted},
		})
	}
	status := wire.ToolCallStatusCompleted
	if result.IsError {
		status = wire.ToolCallStatusFailed
	}
	return wire.SessionUpdate{ToolCallUpdate: &wire.SessionToolCallUpdate{
		ToolCallId: wire.ToolCallId(result.ToolCallID),
		Status:     &status,
		Content:    content,
	}}, true, nil
}

// parseToolArguments 把模型产出的参数原样保住，解不开就当一个不透明的字符串。
//
// 源: packages/acp/acp/src/updates.ts:104-111
//
// 模型是会产出坏 JSON 的。那时把原串当作输入送出去，比整条调用更新都不发要好——
// 界面上至少还看得见它究竟说了什么。
func parseToolArguments(value string) any {
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return value
	}
	return parsed
}
