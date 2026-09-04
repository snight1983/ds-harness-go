// 本文件的作用：从一段会话日志里读出「某个还开着的步骤当时选中的是哪个提供方」，
// 以及支撑这件事的那点增量状态。
//
// 源: packages/llm/llm-retry/src/history.ts

package llmretry

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// routeKind 是一条事件对「回合/步骤/提供方」这点状态的影响种类。
type routeKind int

const (
	// routeNone 表示这条事件不影响这点状态。
	routeNone routeKind = iota
	routeTurnStart
	routeTurnEnd
	routeStepStart
	routeStepEnd
	routeHeader
)

// routeDelta 是一条事件解出来的那点改动，还没作数。
//
// 新增: 解码和改状态分成两步，是本仓库那条不变量惯例的要求（见 [Trace.Validate]）：
// 验的那一步必须是纯的，因为它会在事件真的提交之前先跑一遍。
type routeDelta struct {
	kind     routeKind
	turn     int
	step     int
	provider string
}

// stepRef 指一个步骤：回合号加步骤号。
type stepRef struct {
	turn int
	step int
}

// routeTransition 把一条事件解成它对这点状态的改动。
//
// 负载读不回来时报错而不是当成 routeNone：这几种事件的负载是核心词汇表里的，
// 读不回来说明日志本身坏了，咽下去的话后面每一条「提供方对不上」的诊断都会指错地方。
func routeTransition(event sessionlog.Event) (routeDelta, error) {
	switch event.Type {
	case sessionlog.EventTurnStart:
		var data sessionlog.TurnStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return routeDelta{}, fmt.Errorf("%s 的负载读不回来：%w", sessionlog.EventTurnStart, err)
		}
		return routeDelta{kind: routeTurnStart, turn: data.Turn}, nil
	case sessionlog.EventTurnEnd:
		return routeDelta{kind: routeTurnEnd}, nil
	case sessionlog.EventStepStart:
		var data sessionlog.StepStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return routeDelta{}, fmt.Errorf("%s 的负载读不回来：%w", sessionlog.EventStepStart, err)
		}
		return routeDelta{kind: routeStepStart, turn: data.Turn, step: data.Step}, nil
	case sessionlog.EventStepEnd:
		return routeDelta{kind: routeStepEnd}, nil
	case sessionlog.EventRequestHeader:
		var data sessionlog.RequestHeaderData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return routeDelta{}, fmt.Errorf("%s 的负载读不回来：%w", sessionlog.EventRequestHeader, err)
		}
		return routeDelta{kind: routeHeader, provider: data.Header.Config.Provider}, nil
	default:
		return routeDelta{}, nil
	}
}

// applyRoute 把一条已经解好的改动算进这条轨迹。
//
// 每一条事件都要走一遍这里，哪怕它的 kind 是 routeNone——position 记的是**事件在
// 日志里的位置**，漏掉一条，后面「这个步骤的 step/start 在最后一次收尾之后吗」
// 就会比错。
func (t *Trace) applyRoute(delta routeDelta) {
	t.position++
	switch delta.kind {
	case routeTurnStart:
		t.turnOpen, t.turn = true, delta.turn
	case routeTurnEnd:
		t.turnOpen = false
		// 源: packages/llm/llm-retry/src/history.ts:16-19
		//
		// turn/end 也算一次收尾：DSH 那句 slice(...).some(e => step/end 或者 turn/end)
		// 把两者并列。一个回合结束了，它里面那个还没收尾的步骤也不再是「开着的」——
		// 不然一条写在回合外的重试会因为翻到了上个回合的表头而被放行。
		t.lastStepClose = t.position
	case routeStepStart:
		t.stepBoundaryOpen = true
		t.stepTurn, t.step = delta.turn, delta.step
		if t.stepStarts == nil {
			t.stepStarts = map[stepRef]int{}
		}
		t.stepStarts[stepRef{turn: delta.turn, step: delta.step}] = t.position
	case routeStepEnd:
		t.stepBoundaryOpen = false
		t.lastStepClose = t.position
	case routeHeader:
		t.provider, t.hasProvider = delta.provider, true
	}
}

// routedProvider 交出「turn/step 这个步骤此刻还开着的话，它选中的是哪个提供方」。
//
// 源: packages/llm/llm-retry/src/history.ts:14-33
//
// 新增: DSH 那边每次都从头扫一遍日志：先 findLastIndex 找那个 step/start，再看它
// 后面有没有 step/end 或者 turn/end，最后从**日志末尾**倒着找最近的一条
// request/header。Go 这边这三件事分别记在 stepStarts、lastStepClose 和 provider 上，
// 一条事件一次更新，答案一样但不必重扫——不变量在每一条事件上都要问一次这个问题。
//
// 注意最后那一步在 DSH 里是从日志末尾倒着扫的，不是从那个 step/start 倒着扫，
// 所以 [Trace.provider] 记的也是**整段日志里最后一条**表头的提供方，不是这个步骤
// 里的那条。两者在正常日志上是同一条（表头写在步骤内），这里逐字跟着 DSH 走。
func (t *Trace) routedProvider(turn, step int) (string, bool) {
	started, present := t.stepStarts[stepRef{turn: turn, step: step}]
	if !present || started <= t.lastStepClose {
		return "", false
	}
	if !t.hasProvider {
		return "", false
	}
	return t.provider, true
}

// ProviderForOpenStep 从一段日志里读出「turn/step 这个步骤还开着的话，它选中的是
// 哪个提供方」，读不出来时第二个返回值为假。
//
// 源: packages/llm/llm-retry/src/history.ts:14
//
// 新增: DSH 那个函数只有一个返回值，`undefined` 同时表达「步骤没开着」「没有表头」
// 两件事，而负载读不回来在 TS 里根本不可能发生（类型已经收窄过了）。Go 这边负载是
// 一段字节，读不回来是真会出现的第三种结果，所以多带一个 error——把它和「没找到」
// 混成同一个返回值的话，一份坏掉的日志会被静静地当成「这个步骤没开着」。
func ProviderForOpenStep(events []sessionlog.Event, turn, step int) (string, bool, error) {
	trace := NewTrace()
	for _, event := range events {
		delta, err := routeTransition(event)
		if err != nil {
			return "", false, fmt.Errorf("%w：seq %d：%w", ErrMalformedEvent, event.Seq, err)
		}
		trace.applyRoute(delta)
	}
	provider, present := trace.routedProvider(turn, step)
	return provider, present, nil
}
