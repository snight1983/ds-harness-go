// 本文件的作用：从会话日志里回答三个问题——上一个基线时刻是什么时候、
// 本包上一次注入是什么时候、这一步该不该再注入。
//
// 源: packages/context/time-context/src/index.ts:57-96,177-185

package timecontext

import (
	"fmt"
	"time"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// eventTime 把日志里的 Unix 纪元毫秒变成一个时刻。
//
// 统一走 UTC：这个值只用来做减法和跟另一个时刻比先后，带哪个时区不影响结果，
// 而固定成 UTC 能让测试里的失败信息不随跑测试的机器变。
func eventTime(event session.Event) time.Time {
	return time.UnixMilli(event.Time).UTC()
}

// PrecedingMessageTime 找出日志里最后一条模型看得见的事件的时刻。
//
// 源: packages/context/time-context/src/index.ts:57-71
//
// 只有用户消息、助手消息和工具结果算数，别的事件（回合与步骤的边界、请求头、
// 待办写入）模型根本看不见。第一步的「上一次」是相对这个时刻算的：模型关心的
// 是「我上次看见东西到现在过了多久」，不是「日志上次动是什么时候」。
//
// 注意本包自己注入的读数**也是**一条用户消息，所以它同样算数。DSH 在这里
// 依赖调用时机：这个函数在本次读数被追加**之前**跑，看不到自己这一条。
func PrecedingMessageTime(events []session.Event) (time.Time, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Type {
		case session.EventUserMessage, session.EventAssistantMessage, session.EventToolResult:
			return eventTime(events[index]), true
		}
	}
	return time.Time{}, false
}

// PrecedingStepContextTime 找出**本回合内**上一条时间读数的时刻。
//
// 源: packages/context/time-context/src/index.ts:73-84
//
// 往回扫到本回合的 turn/start 就停，交出「没有」。这道边界是有意的：
// 第二步及以后的基线是「上一条时间读数」，而上一个回合里的读数不是本回合的
// 基线——跨过回合去取会把两次对话之间用户离开的那段时间算进本回合的耗时。
func PrecedingStepContextTime(events []session.Event, turn int) (time.Time, bool, error) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type == session.EventTurnStart {
			start, err := decodeTurnStart(event)
			if err != nil {
				return time.Time{}, false, err
			}
			if start.Turn == turn {
				return time.Time{}, false, nil
			}
			continue
		}
		reading, err := IsReadingEvent(event)
		if err != nil {
			return time.Time{}, false, err
		}
		if reading {
			return eventTime(event), true, nil
		}
	}
	return time.Time{}, false, nil
}

// LatestInjectionTime 找出本包上一次落库的读数，不看回合边界。
//
// 源: packages/context/time-context/src/index.ts:86-96
//
// 和 [PrecedingStepContextTime] 的区别就是那道边界，而这个区别是必需的：
// 节流问的是「本包最近一次往这个会话里写字是什么时候」，那是**跨回合**的，
// 否则每开一个新回合节流就重新开始，配了间隔等于没配。
func LatestInjectionTime(events []session.Event) (time.Time, bool, error) {
	for index := len(events) - 1; index >= 0; index-- {
		reading, err := IsReadingEvent(events[index])
		if err != nil {
			return time.Time{}, false, err
		}
		if reading {
			return eventTime(events[index]), true, nil
		}
	}
	return time.Time{}, false, nil
}

// PreviousBaseline 按步骤号挑出这一步该用的基线时刻。
//
// 源: packages/context/time-context/src/index.ts:183-185
//
// 新增: DSH 把这个三元表达式写在 `apply` 的钩子里。挪出来是因为它和
// [RenderText] 里那个 baseline 字符串必须**同时**改：挑的是哪个基线、
// 正文里写的是哪个名字，两处对不上的话读数会说谎，而不变量正是查这一条。
// 放在一起，改一处就看得见另一处。
func PreviousBaseline(events []session.Event, turn int, step int) (time.Time, bool, error) {
	if step == 1 {
		found, ok := PrecedingMessageTime(events)
		return found, ok, nil
	}
	return PrecedingStepContextTime(events, turn)
}

// ShouldInject 判断这一步该不该再落一条读数。
//
// 源: packages/context/time-context/src/index.ts:177-182
//
// 间隔不为正就永远注入。否则：上一次注入之后还没走满这段间隔，就跳过。
//
// `!now.Before(last)` 那一半是 DSH 的 `now >= lastInjection` 直译，它管的是
// 时钟往回跳的情况——那时 `now - last` 是负数，比什么间隔都小，光看这一条会
// 把读数永远节流掉。所以时刻本身早于上一次注入时，判定是「注入」而不是「跳过」。
func ShouldInject(events []session.Event, now time.Time, refreshInterval time.Duration) (bool, error) {
	if refreshInterval <= 0 {
		return true, nil
	}
	last, ok, err := LatestInjectionTime(events)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if !now.Before(last) && now.Sub(last) < refreshInterval {
		return false, nil
	}
	return true, nil
}

// IsReadingEvent 判断一条事件是不是本包写下的时间读数。
//
// 源: packages/context/time-context/src/index.ts:89-91
//
// 判据只有两条：它是一条用户消息，而且它的来源是署了 [PluginName] 的插件来源。
// 别的插件注入的用户消息、用户自己说的话，都不算。
func IsReadingEvent(event session.Event) (bool, error) {
	if event.Type != session.EventUserMessage {
		return false, nil
	}
	message, err := decodeUserMessage(event)
	if err != nil {
		return false, err
	}
	plugin, ok := message.Source.(llm.PluginSource)
	return ok && plugin.Plugin == PluginName, nil
}

// decodeTurnStart 读回一条 turn/start 的负载。
func decodeTurnStart(event session.Event) (session.TurnStartData, error) {
	data, err := session.DecodeData(event)
	if err != nil {
		return session.TurnStartData{}, fmt.Errorf("%w：seq %d 的 turn/start：%w",
			ErrMalformedEvent, event.Seq, err)
	}
	start, ok := data.(session.TurnStartData)
	if !ok {
		// 不可达：[session.DecodeData] 按 Type 分发，turn/start 只会得到这一种负载。
		// 留着它是因为一次分发错位会让本包把某个回合的边界整个看漏，
		// 而看漏边界的后果是跨回合取基线——那是一段静默错掉的耗时。
		return session.TurnStartData{}, fmt.Errorf("%w：seq %d 声称是 turn/start，负载却是 %T",
			ErrMalformedEvent, event.Seq, data)
	}
	return start, nil
}

// decodeUserMessage 读回一条 user/message 的负载。
func decodeUserMessage(event session.Event) (llm.Message, error) {
	data, err := session.DecodeData(event)
	if err != nil {
		return llm.Message{}, fmt.Errorf("%w：seq %d 的 user/message：%w",
			ErrMalformedEvent, event.Seq, err)
	}
	message, ok := data.(session.UserMessageData)
	if !ok {
		// 不可达，理由同 [decodeTurnStart]。
		return llm.Message{}, fmt.Errorf("%w：seq %d 声称是 user/message，负载却是 %T",
			ErrMalformedEvent, event.Seq, data)
	}
	return message.Message, nil
}
