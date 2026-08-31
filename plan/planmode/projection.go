// 本文件的作用：plan 这个投影单元——折的是哪三条事件、为什么 pending 能从日志
// 单独还原出来、以及为什么状态版本号是 2。
//
// 源: packages/plan/plan-mode/src/index.ts:140-292

package planmode

import (
	"encoding/json"
	"errors"
	"strings"

	"ds-harness-go/interaction/commands"
	"ds-harness-go/session"
	"ds-harness-go/session/projection"
)

// ProjectionKey 是这个单元占的投影键。
//
// 源: packages/plan/plan-mode/src/index.ts:263
const ProjectionKey = "plan"

// projectionStateVersion 是这份状态的作废版本号。
//
// 源: packages/plan/plan-mode/src/index.ts:290
//
// 跟着 DSH 从 2 开始，不从 1 开始：这个键在 DSH 侧已经有过一次语义变更，
// 而落盘的检查点行是按 (键, 版本) 认的。改回 1 会让一批本该被丢掉的旧行
// 重新变得「看起来还能用」。
const projectionStateVersion = 2

// runningCommand 是一次已经派出去、还没等到它那条 command/done 的 `/plan`。
//
// 源: packages/plan/plan-mode/src/index.ts:151
type runningCommand struct {
	// CommandID 是那条 command/run 的配对号。
	CommandID commands.ID `json:"commandId"`
	// Wanted 是这次调用想要的那个状态。
	Wanted bool `json:"wanted"`
}

// unitState 是这个单元的内部状态：日志上的模式、最近一次还没被 [EventMode] 结清的
// 成功选择、以及一次还没结算的执行。
//
// 源: packages/plan/plan-mode/src/index.ts:146-152
//
// 它必须是纯 JSON（落盘检查点的前提），所以三个字段都排得出去；Wanted 和 Running
// 用指针表达 DSH 那两个 nullable，而且**不带** omitempty——排出去的是显式的 null，
// 和 DSH 那份 `.nullable()` 的形状逐字一致。
type unitState struct {
	// Active 是日志上当下生效的模式。
	Active bool `json:"active"`
	// Wanted 是那次选择指向的模式；nil 表示没有还没结清的选择。
	Wanted *bool `json:"wanted"`
	// Running 是最近那条还没结算的 `/plan`；nil 表示没有。
	Running *runningCommand `json:"running"`
}

// RegisterProjection 把 plan 这个单元登进投影注册表，返回注销它的函数。
//
// 源: packages/plan/plan-mode/src/index.ts:261-292
//
// 新增: DSH 那边这是 apply 里的一个 ctx.inject(['sessionProjections'], ...)
// 子节点——投影服务在场它就装，不在场整个装配不受影响。Go 里没有那个容器，
// 「在不在场」就是装配方手上有没有这个注册表，所以它是一个显式的函数（成例见
// [ds-harness-go/todo.RegisterProjection]）。[Controller.Install] 在
// [Deps.Projections] 非 nil 时替装配方叫它。
//
// # pending 为什么是一个纯回放量
//
// 挂起状态并不存在于任何活着的镜像里，它是这三条事件折出来的：
//
//   - command/run 记下用户那次 `/plan` 选择（`off` 是关，其余一律是开）；
//   - 它那条配对的 command/done 只留下**成功**的那些选择；
//   - [EventMode] 记下那个选择已经生效了，并把它清掉。
//
// 于是宿主重启、另一个标签页、一次冷读，都能只从日志把它还原出来。
func RegisterProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("planmode: 需要一个投影注册表")
	}
	return projection.Register(registry, projection.Definition[unitState]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() unitState { return unitState{} },
		Apply:        applyProjection,
		DecodeState:  decodeProjectionState,
		View:         viewProjection,
	})
}

// applyProjection 是那个纯转移。
//
// 源: packages/plan/plan-mode/src/index.ts:266-282
func applyProjection(state unitState, event session.Event) (unitState, bool) {
	switch event.Type {
	case commands.EventRun:
		var data commands.RunData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Name != CommandName {
			return state, false
		}
		// 新增: DSH 先查 `event.data.args === undefined` 再决定要不要理这一条——
		// 那是给「定义里关掉了输入记录」的命令留的口子。Go 这边 RunData.Args 带
		// omitempty，一次 `/plan`（空输入）排出去就**没有** args 键，和「没记输入」
		// 在介质上分不开。照抄那道检查会让裸的 `/plan` 永远折不出 pending。
		// 这一条不需要那道检查：`/plan` 这条定义由本包自己登记，输入一直是记的
		// （见 [Controller.commandDefinition]），所以键不在就等于空串。
		wanted := strings.TrimSpace(data.Args) != "off"
		state.Running = &runningCommand{CommandID: data.ID, Wanted: wanted}
		return state, true
	case commands.EventDone:
		var data commands.DoneData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return state, false
		}
		if state.Running == nil || data.ID != state.Running.CommandID {
			return state, false
		}
		var wanted *bool
		if data.Kind == commands.ResultSuccess && state.Running.Wanted != state.Active {
			value := state.Running.Wanted
			wanted = &value
		}
		state.Wanted = wanted
		state.Running = nil
		return state, true
	case EventMode:
		active, ok := decodeMode(event)
		if !ok {
			return state, false
		}
		state.Active = active
		state.Wanted = nil
		return state, true
	default:
		return state, false
	}
}

// viewProjection 把内部状态投成上线的那个值。
//
// 源: packages/plan/plan-mode/src/index.ts:283-289
//
// 一条还没结算的执行盖过上一次已经结清的选择：用户刚敲下的那一下就是最新的意图，
// 哪怕它还没有走完自己的生命周期。
func viewProjection(state unitState) any {
	wanted := state.Wanted
	if state.Running != nil {
		wanted = &state.Running.Wanted
	}
	return Projection{
		Active:  state.Active,
		Pending: wanted != nil && *wanted != state.Active,
	}
}

// decodeProjectionState 把一份落过盘的状态读回来。
//
// 用严格解码（多一个字段就报错）：一份形状对不上的旧状态如果被宽容地读成
// 「字段都在、值全是零」，它会被继续往前折成垃圾，而且一路上不报任何错。
var decodeProjectionState = projection.StrictDecoder[unitState]()
