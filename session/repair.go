// 本文件的作用：一份崩在半路的会话日志怎么补齐成一份提供方肯收的历史。
//
// 源: packages/core/session/src/repair.ts

package session

import (
	"encoding/json"
	"fmt"

	"ds-harness-go/llm"
)

const (
	// ToolNotStarted 是「助手请求了这次调用，但它从没被记为开始过」的恢复码。
	ToolNotStarted = "TOOL_NOT_STARTED"
	// ToolOutcomeUnknown 是「这次调用记下了，但它完成后的结果没被持久记下」的恢复码。
	ToolOutcomeUnknown = "TOOL_OUTCOME_UNKNOWN"
)

// 补写出来的工具结果里给模型看的两句话，原样保留英文。
//
// 两句话的措辞差别**就是这整件事的全部意义**：前一句说的是「不知道有没有生效，
// 别乱重试」，后一句说的是「压根没开始，需要就重试」。把它们写成同一句，
// 这个模块就白做了——一次已经打过款的转账会被当成没发生过再打一次。
const (
	// outcomeUnknownText 给的是那些已经记为开始、但结果未知的调用。
	outcomeUnknownText = "The tool call was interrupted after it was recorded, but no result was durably recorded. Its outcome is unknown. Decide whether to retry from the tool semantics: retry only if the operation is read-only or idempotent; if it may have side effects, first verify external state or ask the user. Do not retry blindly."
	// notStartedText 给的是那些还没被记为开始就断掉的调用。
	notStartedText = "The tool call was interrupted before the Harness recorded it as started. Retry it if it is still needed."
)

// pendingCall 是一次已被助手声明、但还没等到结果的调用。
type pendingCall struct {
	// step 是声明它的那条助手消息所在的步骤号。
	step int
	// callSeq 是那条 tool/call 事件的 seq。
	callSeq int
	// started 表示这次调用已经被记为开始过（也就是 callSeq 有效）。
	started bool
}

// InterruptedTurnClosers 给出一批确定性的合成事件，用来关掉一个开着的尾部回合。
//
// 源: packages/core/session/src/repair.ts:27-133
//
// 没配上结果的调用先各得一条错误结果，然后是一条 step/end（如果有步骤开着），
// 最后是一条 [ReasonInterrupted] 的 turn/end。seq 接着日志往下排，
// 时间戳复用最后一条真事件的——这让补出来的事件是确定的，也从不发明一个
// 「未来」的时刻。一份平衡的或者空的日志返回空。
//
// 新增: DSH 靠 JS Map 的插入顺序保证补出来的结果和记录里的顺序一致。
// Go 的 map 遍历顺序是随机的，所以这里另外拿一个切片记顺序：
// 一份「补出来的历史每次不一样」的日志会让重放和缓存全部失效。
func InterruptedTurnClosers(events []Event) ([]Event, error) {
	var (
		openTurn, openStep     int
		turnIsOpen, stepIsOpen bool
		order                  []llm.CallID
		pending                = map[llm.CallID]*pendingCall{}
		clearPending           = func() { order, pending = nil, map[llm.CallID]*pendingCall{} }
	)

	for _, event := range events {
		switch event.Type {
		case EventTurnStart:
			var data TurnStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, wrapMalformed("回合开始事件的负载读不回来", err)
			}
			// 每个回合边界上都清一次，早先的调用漏不进尾部的修补。
			openTurn, turnIsOpen, stepIsOpen = data.Turn, true, false
			clearPending()
		case EventTurnEnd:
			turnIsOpen, stepIsOpen = false, false
			clearPending()
		case EventStepStart:
			var data StepStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, wrapMalformed("步骤开始事件的负载读不回来", err)
			}
			openStep, stepIsOpen = data.Step, true
		case EventStepEnd:
			stepIsOpen = false
			clearPending()
		case EventAssistantMessage:
			// 工具调用块挂在助手消息上；每一块都悬着，直到一条同 callId 的
			// tool/result 落进日志。
			var data AssistantMessageData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, wrapMalformed("助手消息事件的负载读不回来", err)
			}
			for _, block := range data.Message.Content {
				call, ok := block.(llm.ToolCallBlock)
				if !ok {
					continue
				}
				if _, seen := pending[call.ID]; !seen {
					order = append(order, call.ID)
				}
				pending[call.ID] = &pendingCall{step: data.Step}
			}
		case EventToolCall:
			// 补出来的结果要引用这条 tool/call 的 seq。
			var data ToolCallData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, wrapMalformed("工具调用事件的负载读不回来", err)
			}
			if entry, ok := pending[data.CallID]; ok {
				entry.callSeq, entry.started = event.Seq, true
			}
		case EventToolResult:
			var data ToolResultData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return nil, wrapMalformed("工具结果事件的负载读不回来", err)
			}
			source, ok := data.Message.Source.(llm.ToolSource)
			if !ok {
				return nil, fmt.Errorf("%w：seq %d 的工具结果消息的来路不是一次工具调用",
					ErrMalformedValue, event.Seq)
			}
			if _, tracked := pending[source.CallID]; tracked {
				delete(pending, source.CallID)
				order = removeCallID(order, source.CallID)
			}
		}
	}

	// 平衡的日志（没有崩在回合中途）没什么可关的。一个开着的回合意味着 events
	// 非空（它那条 turn/start 是记下来了的），所以最后一条一定在。
	if !turnIsOpen || len(events) == 0 {
		return nil, nil
	}

	// 最后一条真事件给出补写事件的 seq 起点和时间戳。
	last := events[len(events)-1]
	seq, time := last.Seq+1, last.Time
	var closers []Event

	// 先关调用再关它那个步骤：提供方会拒收悬空的助手调用，
	// 而 order 保住了它们在记录里的顺序。
	for _, callID := range order {
		entry := pending[callID]
		text, errorName, errorCode := notStartedText, "ToolNotStartedError", ToolNotStarted
		if entry.started {
			text, errorName, errorCode = outcomeUnknownText, "ToolOutcomeUnknownError", ToolOutcomeUnknown
		}
		// 这条消息是**手搭**的，不走 llm.NewToolResultMessage——那个构造函数会
		// 现分配一个 uuid 当 id，而这里的 id 必须是可复现的：同一份崩掉的日志
		// 补两次得到的字节要一样，否则重放和缓存都对不上。
		message := llm.Message{
			ID:     llm.MessageID(fmt.Sprintf("interrupted-tool-result-%s-%d", callID, seq)),
			Role:   llm.RoleUser,
			Source: llm.ToolSource{CallID: callID},
			Content: llm.Content{llm.ToolResultBlock{
				ToolCallID: callID,
				IsError:    true,
				Content:    llm.Content{llm.TextBlock{Text: text}},
			}},
		}
		payload, err := json.Marshal(ToolResultData{
			Turn:    openTurn,
			Step:    entry.step,
			Message: message,
			Error:   &ToolError{Name: errorName, Code: errorCode},
		})
		if err != nil {
			return nil, wrapMalformed("补写的工具结果排不出去", err)
		}
		closer := Event{
			Type:      EventToolResult,
			Seq:       seq,
			Time:      time,
			Data:      payload,
			SurfaceOp: AppendOp{},
		}
		if entry.started {
			closer.SourceEventSeqs = []int{entry.callSeq}
		}
		closers = append(closers, closer)
		seq++
	}

	// 接着关那个开着的步骤——步骤没关就发 turn/end 是一次不变量违反，
	// 所以步骤的边界必须排在回合的前面。
	if stepIsOpen {
		payload, err := json.Marshal(StepEndData{Turn: openTurn, Step: openStep})
		if err != nil {
			return nil, wrapMalformed("补写的步骤结束排不出去", err)
		}
		closers = append(closers, Event{Type: EventStepEnd, Seq: seq, Time: time, Data: payload})
		seq++
	}

	payload, err := json.Marshal(TurnEndData{Turn: openTurn, Reason: InterruptedTurnEnd{}})
	if err != nil {
		return nil, wrapMalformed("补写的回合结束排不出去", err)
	}
	closers = append(closers, Event{Type: EventTurnEnd, Seq: seq, Time: time, Data: payload})
	return closers, nil
}

// removeCallID 把一个调用 id 从顺序表里摘掉。
//
// 新增: 摘而不是留一个墓碑，是为了和 JS Map 的 delete 语义对齐——同一个 id
// 要是后来又被声明一次，它应该排在**末尾**，而不是回到原来那个位置。
func removeCallID(order []llm.CallID, target llm.CallID) []llm.CallID {
	kept := order[:0]
	for _, callID := range order {
		if callID != target {
			kept = append(kept, callID)
		}
	}
	return kept
}
