// 本文件的作用：todos 这个投影单元——折叠怎么写、为什么是这个状态版本号。
//
// 源: packages/todo/tool-todo/src/index.ts:135-148

package todo

import (
	"encoding/json"
	"errors"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

// ProjectionKey 是这个单元占的投影键。
//
// 源: packages/todo/tool-todo/src/index.ts:137
const ProjectionKey = "todos"

// projectionStateVersion 是这份状态的作废版本号。
//
// 源: packages/todo/tool-todo/src/index.ts:146
//
// 跟着 DSH 从 2 开始，不从 1 开始：这个键在 DSH 侧已经有过一次语义变更，
// 而落盘的检查点行是按 (键, 版本) 认的。改回 1 会让一批本该被丢掉的旧行
// 重新变得「看起来还能用」。
const projectionStateVersion = 2

// RegisterProjection 把 todos 这个单元登进投影注册表，返回注销它的函数。
//
// 源: packages/todo/tool-todo/src/index.ts:135-148
//
// 新增: DSH 那边这是 apply 里的一个 ctx.inject(['sessionProjections'], ...)
// 子节点——投影服务在场它就装，不在场整个装配不受影响。Go 里没有那个容器，
// 「在不在场」就是装配方手上有没有这个注册表，所以它是一个显式的函数：
// 不叫它，界面读到的就是「这个能力不在」。
//
// # 这份折叠是什么
//
// 待办清单是一份**当下的计划**，不是一条流水账：
//
//   - 每一条 todo/write 带着整表快照，所以折叠是 last-wins，一句赋值。
//   - 一个新回合开起来就清空——上一个回合的计划不该挂在这一个回合头上。
//   - turn/end **不**清空：一个回合刚结束时，那份做完的清单正是最该被看见的东西。
//   - 第一次写之前、以及一个新回合刚开起来之后，值都是 nil，排出去就是 JSON null。
func RegisterProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("todo: 需要一个投影注册表")
	}
	return projection.Register(registry, projection.Definition[[]session.TodoItem]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() []session.TodoItem { return nil },
		Apply:        applyProjection,
		DecodeState:  decodeProjectionState,
		View:         func(state []session.TodoItem) any { return state },
	})
}

// applyProjection 是那个纯转移。
//
// 源: packages/todo/tool-todo/src/index.ts:140-144
func applyProjection(state []session.TodoItem, event session.Event) ([]session.TodoItem, bool) {
	switch event.Type {
	case session.EventTodoWrite:
		var data session.TodoWriteData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			// 读不回来的负载只可能来自一份已经坏掉的日志。折叠不能报错，
			// 所以这里保持原状：把清单换成空的会把「坏了一条」放大成
			// 「整份计划没了」，而那看起来和一次合法的清空一模一样。
			// 真正拦这种东西的是 [ValidateEvent]。
			return state, false
		}
		return data.Todos, true
	case session.EventTurnStart:
		// 已经是空的就不算变化——报一次假的变化会让每一个新回合都往变更流上
		// 推一条 null。
		return nil, state != nil
	default:
		return state, false
	}
}

// decodeProjectionState 把一份落过盘的状态读回来。
//
// 用严格解码（多一个字段就报错）：一份形状对不上的旧状态如果被宽容地读成
// 「字段都在、值全是零」，它会被继续往前折成垃圾，而且一路上不报任何错。
var decodeProjectionState = projection.StrictDecoder[[]session.TodoItem]()
