// 本文件的作用：这个包自己拥有的那条持久不变量——一条 permission/preset 记着的
// 名字必须还在这份部署的表里。
//
// 源: packages/interaction/permission-presets/src/invariant.ts

package permissionpresets

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/interaction/permission-presets/src/invariant.ts:6
const PackageName = "@deepseek-ai/dsh-permission-presets"

// ValidateEvent 验一条事件里本包拥有的那些字段；不是 [EventPreset] 就什么都不做。
//
// 源: packages/interaction/permission-presets/src/invariant.ts:14-19
//
// 查的是「这个名字还认不认得出来」，不是负载形状：一条读不回来的负载在
// [applyKnob] 那边已经被当作没有这条覆盖，而一个**读得回来、却指向表外**的名字
// 才是真正的坏账——它会让 [Service.Current] 把用户那次选择静默降级成
// [CustomPreset]，而且没有任何地方说得清为什么。
//
// 这条检查跟着表走：同一份日志换一份 [Config.Presets] 就可能从合法变成违例。
// 那正是它要抓的事——一次去掉某条预设的部署改动，历史里引着它的会话会在装载
// 这一刻就响。
//
// 新增: DSH 那边这个函数收一个 `fail` 回调、违例时直接抛。Go 这边返回第一条违例，
// 理由和 [github.com/snight1983/ds-harness-go/feature/plan/planmode.ValidateEvent]
// 逐字相同：它因此可以脱离不变量注册表单独用。
func (s *Service) ValidateEvent(event sessionlog.Event) error {
	if event.Type != EventPreset {
		return nil
	}
	var data PresetData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil
	}
	if _, known := s.byName[data.Preset]; !known {
		return fmt.Errorf("permission/preset names unknown preset %s", strconv.Quote(data.Preset))
	}
	return nil
}

// RegisterInvariants 装上那条「记着的名字必须还认得出来」的检查，返回注销函数。
//
// 源: packages/interaction/permission-presets/src/invariant.ts:22-38
//
// 两条胳膊，和 DSH 一样：装的时候把已经装进来的日志走一遍，然后订阅后续的追加。
// 装配那两条胳膊由调用方以函数交进来，理由和
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.RegisterInvariants]
// 逐字相同。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	service *Service,
	loaded func() []sessionlog.Event,
	subscribe func(observer func(sessionlog.Event)) func(),
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一个不变量注册表", ErrInvalidConfig)
	}
	if service == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一个预设服务来认名字", ErrInvalidConfig)
	}
	if loaded == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一条读出已装载日志的路", ErrInvalidConfig)
	}
	if subscribe == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一条订阅后续事件的路", ErrInvalidConfig)
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		for _, event := range loaded() {
			if err := service.ValidateEvent(event); err != nil {
				fail(err.Error())
			}
		}
		scope.Defer(subscribe(func(event sessionlog.Event) {
			if err := service.ValidateEvent(event); err != nil {
				fail(err.Error())
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
