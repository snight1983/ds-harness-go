// 本文件的作用：goal 这个投影单元——它折的只有本包那一条事件，而且和严格回放
// 是**两套标准**，为什么可以这样，以及为什么状态版本号是 4。
//
// 源: packages/goal/goal/src/index.ts:65-113、201-213

package goal

import (
	"errors"

	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// ProjectionKey 是这个单元占的投影键。
//
// 源: packages/goal/goal/src/index.ts:206
const ProjectionKey = "goal"

// projectionStateVersion 是这份状态的作废版本号。
//
// 源: packages/goal/goal/src/index.ts:211
//
// 跟着 DSH 从 4 开始，不从 1 开始：这个键在 DSH 侧已经改过三次语义，而落盘的
// 检查点行是按 (键, 版本) 认的。改回 1 会让一批本该被丢掉的旧行重新变得
// 「看起来还能用」。
const projectionStateVersion = 4

// RegisterProjection 把 goal 这个单元登进投影注册表，返回注销它的函数。
//
// 源: packages/goal/goal/src/index.ts:160-168（goalProjectionDefinition）
//
// 新增: DSH 那边这是构造函数里的一个 ctx.inject(['sessionProjections'], ...)
// 子节点——投影服务在场它就装，不在场整个装配不受影响。Go 里没有那个容器，
// 「在不在场」就是装配方手上有没有这个注册表，所以它是一个显式的函数（成例见
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.RegisterProjection]）。
func RegisterProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("goal: 需要一个投影注册表")
	}
	return projection.Register(registry, projection.Definition[*Projection]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() *Projection { return nil },
		Apply:        ApplyProjection,
		DecodeState:  decodeProjectionState,
		View:         func(state *Projection) any { return state },
	})
}

// ApplyProjection 是这个单元那个「最后写的赢」的纯转移。
//
// 源: packages/goal/goal/src/index.ts:136-158（applyGoalProjection）
//
// # 它为什么可以比 [ApplyEvent] 松这么多
//
// 严格回放（[ApplyEvent]）验跃迁、遇到读不动的改动当场炸；这一条不验，读不动
// 就原样返回。两者的职责本来就不同：
//
//   - 写的那一侧已经验过了。一条 goal/change 落进日志之前，[Service] 先把它
//     折过一遍；本包那条不变量（[ValidateStream]）在装配方装上它的地方还会
//     再拒一次破规矩的流。
//   - 这份状态要落盘做检查点，所以它必须是纯 JSON，也就不能带严格回放那个
//     seenGoalIDs 集合。
//   - 一个读不动的历史事件如果在这里炸掉，整份投影就没法重建了——而它本该只是
//     少一格。
//
// 交回的第二个值是「这一条动没动这份状态」：不是本包的事件、或者读不动的，一律
// 交回 false，注册表据此跳过一次没必要的重排。
func ApplyProjection(state *Projection, event sessionlog.Event) (*Projection, bool) {
	if event.Type != EventChange {
		return state, false
	}
	change, err := DecodeChange(event.Data)
	if err != nil {
		return state, false
	}
	if change.Operation == OpClear {
		return nil, true
	}
	return &Projection{
		Goal:          change.Goal,
		RoundsStarted: change.RoundsStarted,
		CreatedAt:     change.CreatedAt,
		UpdatedAt:     change.UpdatedAt,
	}, true
}

// decodeProjectionState 把一份落过盘的状态读回来。
//
// 用严格解码（多一个字段就报错）：一份形状对不上的旧状态如果被宽容地读成
// 「字段都在、值全是零」，它会被继续往前折成垃圾，而且一路上不报任何错。
var decodeProjectionState = projection.StrictDecoder[*Projection]()
