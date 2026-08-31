// 本文件的作用：「一个孩子最后说了什么」的那条唯一选法。
//
// 源: packages/subagent/subagent/src/assistant-output.ts
//
// 后端的运行结果和 `subagent/end` 上的 LastAssistantMessage 走同一条规矩：
// 选**最后一条非空**的助手消息。一条内容为空的消息只在「循环在一个撞了 token
// 天花板、又没有可执行块的步骤之后补记用量」时出现，所以它不该顶掉更早那份真的
// 输出。一条非空消息都没有时，选攒下来的助手文本。这条选法**不看**终止原因。

package subagent

import (
	"strings"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// AssistantOutputFold 是那条选法的增量版，给那些边流边看孩子输出的后端用：
// 走会话事件的后端把每一条事件 [AssistantOutputFold.Push] 进来，
// 没有会话事件的传输（比如 ACP 的内容分块）把生文本 [AssistantOutputFold.PushText]
// 进同一个流式兜底。
//
// 源: packages/subagent/subagent/src/assistant-output.ts:22-58
//
// 零值可用。
type AssistantOutputFold struct {
	// message 是当下那条候选终稿；nil 表示还没见过非空的助手消息。
	message llm.Content
	// partial 是攒下来的流式文本片段。
	partial []string
}

// Push 折进一条会话事件：一条非空的助手消息成为候选终稿，一块 text-delta 续上
// 流式兜底，别的事件一律不贡献。
//
// 源: packages/subagent/subagent/src/assistant-output.ts:33-40
//
// 新增: DSH 直接读 `event.data.message.content`，因为它那个负载是活的对象。Go 的
// [ds-harness-go/session.Event.Data] 是原样保管的 JSON，所以先解一次；解不出来
// （日志坏了）在这里当**没贡献**处理而不是报错——这条折叠只负责选出一份输出，
// 判日志成不成立是 [ds-harness-go/session] 那道边界的事，在这里报错只会让一次
// 本来能收场的运行多一种收不了场的方式。
func (f *AssistantOutputFold) Push(event session.Event) {
	switch event.Type {
	case session.EventAssistantMessage:
		data, err := session.DecodeData(event)
		if err != nil {
			return
		}
		message, ok := data.(session.AssistantMessageData)
		if !ok {
			return
		}
		if len(message.Message.Content) > 0 {
			f.message = message.Message.Content
		}
	case session.EventAssistantChunk:
		data, err := session.DecodeData(event)
		if err != nil {
			return
		}
		chunk, ok := data.(session.AssistantChunkData)
		if !ok {
			return
		}
		if delta, isDelta := chunk.Chunk.(llm.TextDeltaChunk); isDelta {
			f.PushText(delta.Text)
		}
	}
}

// PushText 用一段在会话事件之外看到的文本续上流式兜底。空片段什么都不做。
//
// 源: packages/subagent/subagent/src/assistant-output.ts:46-48
func (f *AssistantOutputFold) PushText(text string) {
	if text != "" {
		f.partial = append(f.partial, text)
	}
}

// Collect 选出到此为止折出来的那份终稿：最后一条非空助手消息，否则是攒下来的
// 流式文本，两样都没有时是 nil。
//
// 源: packages/subagent/subagent/src/assistant-output.ts:55-59
func (f *AssistantOutputFold) Collect() llm.Content {
	if f.message != nil {
		return f.message
	}
	text := strings.Join(f.partial, "")
	if text == "" {
		return nil
	}
	return llm.Content{llm.TextBlock{Text: text}}
}

// FinalAssistantOutput 把那条选法应用在一整段孩子自己的事件后缀上。
//
// 源: packages/subagent/subagent/src/assistant-output.ts:66-72
//
// events 是孩子**自己**那些事件（种子或者轮次边界**之后**的）。
// 一份都选不出来时交回 nil。
func FinalAssistantOutput(events []session.Event) llm.Content {
	var fold AssistantOutputFold
	for _, event := range events {
		fold.Push(event)
	}
	return fold.Collect()
}
