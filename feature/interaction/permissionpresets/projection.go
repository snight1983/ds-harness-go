// 本文件的作用：permissions 这个投影单元——折的是哪三条事件、界面读到的是什么，
// 以及为什么状态版本号是 2。
//
// 源: packages/interaction/permission-presets/src/index.ts:237-244

package permissionpresets

import (
	"errors"

	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// ProjectionKey 是这个单元占的投影键。
//
// 源: packages/interaction/permission-presets/src/index.ts:238
const ProjectionKey = "permissions"

// projectionStateVersion 是这份状态的作废版本号。
//
// 源: packages/interaction/permission-presets/src/index.ts:239
//
// 跟着 DSH 从 2 开始，理由和
// [github.com/snight1983/ds-harness-go/feature/plan/planmode] 那个同名常量逐字相同：
// 落盘的检查点行按 (键, 版本) 认，改回 1 会让一批本该作废的旧行重新看起来能用。
const projectionStateVersion = 2

// RegisterProjection 把 permissions 这个单元登进投影注册表，返回注销它的函数。
//
// 源: packages/interaction/permission-presets/src/index.ts:237-244
//
// 新增: DSH 那边这几行在构造函数里直接跑——投影服务是它的 static inject 之一，
// 不在场整个插件装不起来。Go 这边它是一个显式的函数（成例见
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.RegisterProjection]），
// 而且**可以不叫**：本包的服务自己折旋钮（[FoldKnobs]），不靠这个注册表求值。
func RegisterProjection(registry *projection.Registry, service *Service) (func(), error) {
	if registry == nil {
		return nil, errors.New("permissionpresets: 需要一个投影注册表")
	}
	if service == nil {
		return nil, errors.New("permissionpresets: 需要一个预设服务来把状态摆成选项单")
	}
	return projection.Register(registry, projection.Definition[KnobState]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() KnobState { return KnobState{} },
		Apply:        applyKnob,
		DecodeState:  projection.StrictDecoder[KnobState](),
		View:         func(state KnobState) any { return service.SelectFor(state) },
	})
}
