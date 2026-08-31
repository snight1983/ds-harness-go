// 本文件的作用：从一份日志里读出计划这件事的四个纯事实——当下开不开、
// 到某个位置为止开不开、有没有一个开着的回合、最后一次告诉模型的时候开不开。
//
// 源: packages/plan/plan-mode/src/index.ts:90-97, 121-138, 175-195

package planmode

import (
	"encoding/json"
	"regexp"

	"ds-harness-go/session"
)

// headingPattern 匹配一行 markdown 标题（一到六级），捕获去掉首尾空白之后的标题正文。
//
// 源: packages/plan/plan-mode/src/index.ts:93
var headingPattern = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// FoldMode 说明整份日志折下来计划模式开不开。
//
// 源: packages/plan/plan-mode/src/index.ts:129-138
func FoldMode(events []session.Event) bool {
	return FoldModeUntil(events, len(events))
}

// FoldModeUntil 说明 events[0, end) 这一段折下来计划模式开不开。
//
// 源: packages/plan/plan-mode/src/index.ts:129-138
//
// 最后一条 [EventMode] 算数；一段一条都没有的前缀折出来是关着。
//
// 新增: DSH 是一个带默认参数的 foldPlanMode(events, end = events.length)。Go 没有
// 默认参数，所以拆成两个具名函数——比让每一个调用方都写一遍 len(events) 好读，
// 也不会有人把「整份」写成「到 len-1 为止」。
func FoldModeUntil(events []session.Event, end int) bool {
	if end > len(events) {
		end = len(events)
	}
	active := false
	for index := 0; index < end; index++ {
		if events[index].Type != EventMode {
			continue
		}
		if value, ok := decodeMode(events[index]); ok {
			active = value
		}
	}
	return active
}

// decodeMode 读一条 [EventMode] 的负载；读不回来时第二个返回值是假。
//
// 新增: DSH 那边负载已经是解好的对象，这个函数在那里不存在。Go 这边是原始字节，
// 所以要多问一句「读得回来吗」。
//
// 用 *bool 而不是 bool 接：一条负载是 `{}` 的事件解成 bool 是 false 且不报错，
// 而那和一条明确写着「关掉」的事件在日志里是**两件事**——前者是坏数据。真正拦
// 它的是 [ValidateEvent]，折叠这一侧只要求「认不出来就当它不存在」。
//
// 认不出来时保持前一个状态而不是让整次折叠失败：折叠在系统提示词装配这类热路径上
// 被反复调用，没有地方接得住这个错误；而两种降级里，「状态没变」比「状态莫名变了」
// 更接近日志真正说的话。理由和 [ds-harness-go/todo] 那个投影折叠逐字相同。
func decodeMode(event session.Event) (bool, bool) {
	var payload struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil || payload.Active == nil {
		return false, false
	}
	return *payload.Active, true
}

// hasOpenTurn 说明日志里有没有一个开起来了、还没等到它那条 turn/end 的回合。
//
// 源: packages/plan/plan-mode/src/index.ts:176-183
//
// 这是 [Controller.Set] 用来判「现在算不算回合之间」的那个信号。不看 agent 状态：
// 一个 agent 在回合结束后的检查点期间状态仍然是 running，而那时已经不会再有
// 回合之内的步骤前置来接一次挂起的选择了。
func hasOpenTurn(events []session.Event) bool {
	open := false
	for _, event := range events {
		switch event.Type {
		case session.EventTurnStart:
			open = true
		case session.EventTurnEnd:
			open = false
		}
	}
	return open
}

// modeAtLastHeader 说明最后一条记进日志的请求头是在哪个状态下发出去的；
// 第一条请求头之前，第二个返回值是假。
//
// 源: packages/plan/plan-mode/src/index.ts:186-195
//
// 它回答的是「模型上一次被告知的是哪一种模式」，而那正是要不要补一句旁白的依据。
func modeAtLastHeader(events []session.Event) (bool, bool) {
	lastHeader := -1
	for index, event := range events {
		if event.Type == session.EventRequestHeader {
			lastHeader = index
		}
	}
	if lastHeader < 0 {
		return false, false
	}
	return FoldModeUntil(events, lastHeader+1), true
}

// firstHeading 交出这份计划里第一条 markdown 标题的正文，一条都没有时是空串。
//
// 源: packages/plan/plan-mode/src/index.ts:91-97
func firstHeading(plan string) string {
	start := 0
	for index := 0; index <= len(plan); index++ {
		if index != len(plan) && plan[index] != '\n' {
			continue
		}
		line := plan[start:index]
		start = index + 1
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			return match[1]
		}
	}
	return ""
}
