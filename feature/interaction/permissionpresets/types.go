// 本文件的作用：这一层的词汇——一条预设长什么样、那条只进日志的选择事件、
// 界面读到的那份选项单，以及从日志折出旋钮状态的那个纯函数。
//
// 源: packages/interaction/permission-presets/src/types.ts
// 源: packages/interaction/permission-presets/src/index.ts:45-134

package permissionpresets

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// CustomPreset 是旋钮值配不上任何一条表项时交出来的那个值。
//
// 源: packages/interaction/permission-presets/src/index.ts:73
//
// 界面可以把它显示成当前值，但它既不是切换目标，也永远不会被写进 [EventPreset]。
const CustomPreset = "custom"

// EventPreset 记下用户选了哪个预设，是一条耐久的、只进日志的意图。
//
// 源: packages/interaction/permission-presets/src/index.ts:45-55
//
// 旋钮事件跟在同一次切换里，真正控制执行的是它们；这一条**不进模型誊本**，
// 它的用处是在两个预设捆着同一份旋钮值时保住用户选的那个名字。
const EventPreset sessionlog.EventType = "permission/preset"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: 理由和 [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.EventTypes]
// 逐字相同——Go 没有声明合并，[sessionlog.Vocabulary] 是个闭合的值，所以由本包交出
// 单子、装配方自己拼：
//
//	vocabulary := sessionlog.CoreVocabulary().
//		With(userapproval.EventTypes()...).
//		With(permissionpresets.EventTypes()...)
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventPreset}
}

// PresetData 是 [EventPreset] 的负载。
//
// 源: packages/interaction/permission-presets/src/index.ts:52-53
type PresetData struct {
	// Preset 是被选中的那个表项名。
	Preset string `json:"preset"`
}

// PresetSpec 是一条预设捆着的旋钮值和它可选的呈现方式。
//
// 源: packages/interaction/permission-presets/src/index.ts:57-67
//
// 新增: DSH 这里还有一个 `sandbox: SandboxMode`。沙箱那一整支在本仓库范围外，
// 理由见包文档。
type PresetSpec struct {
	// Approval 是这条预设要写下去的那个审批策略。
	Approval userapproval.Policy
	// Label 是界面显示的名字；空串时回落到表项名本身。
	//
	// 新增: DSH 叫 `name`。Go 这边表项名已经是 [Preset.Name] 了，再来一个 Name
	// 会变成 preset.Name 和 preset.Spec.Name 两个都叫名字的东西。
	Label string
	// Description 是给用户看的那一句话，可以为空。
	Description string
}

// Preset 是表里的一条：名字加上它捆的东西。
//
// 新增: DSH 的表是 `Record<string, PresetSpec>`，键的插入顺序就是 `derive` 的匹配
// 顺序和界面选项的排列顺序。Go 的 map 没有顺序，所以表是一张有序切片。
type Preset struct {
	// Name 是这条预设的稳定键，也是 [EventPreset] 里写下去的那个值。
	Name string
	// PresetSpec 是它捆的东西。
	PresetSpec
}

// PresetOption 是界面为一条预设（或者派生出来的 [CustomPreset]）广告出去的那个选项。
//
// 源: packages/interaction/permission-presets/src/types.ts:13-20
type PresetOption struct {
	// Value 是稳定的选项值：表项名，或者 [CustomPreset]。
	Value string `json:"value"`
	// Name 是显示名。
	Name string `json:"name"`
	// Description 是给用户看的那一句话；没配就不出现在字节里。
	Description string `json:"description,omitempty"`
}

// PermissionSelect 是 [ProjectionKey] 这个投影的完整值。
//
// 源: packages/interaction/permission-presets/src/types.ts:27-32
type PermissionSelect struct {
	// Options 是按表的顺序排的那些可切换预设；[CustomPreset] 只在它正是当前值的
	// 时候才追加在末尾。
	Options []PresetOption `json:"options"`
	// CurrentValue 是生效的当前值：一个表项名，或者 [CustomPreset]。
	CurrentValue string `json:"currentValue"`
}

// KnobState 是这个投影单元折出来的旋钮状态：每个旋钮最后一次被覆盖成了什么。
//
// 源: packages/interaction/permission-presets/src/index.ts:82-95
//
// 新增: DSH 用 `string | null` 表达「还没被覆盖过」。Go 这边用**空串即缺失**
// （成例见 [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.Settings]）：
// 表项名和 [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.Policy]
// 都没有一个合法的空值，所以空串本身就是明确的。
// [github.com/snight1983/ds-harness-go/feature/plan/planmode] 那边走指针，是因为它那两个
// 字段是 bool，没有空出来的零值可用。
type KnobState struct {
	// Preset 是最后一条 [EventPreset] 的负载；空串表示没选过。
	Preset string `json:"preset"`
	// Approval 是最后一条 approval/policy 的负载；空串表示这条会话没有自己的覆盖，
	// 用部署默认值。
	Approval userapproval.Policy `json:"approval"`
	// Seeded 表示这段日志里有过一道种子边界。
	Seeded bool `json:"seeded"`
}

// applyKnob 是那个纯转移：一条事件把旋钮状态往前推一格。
//
// 源: packages/interaction/permission-presets/src/index.ts:118-134
//
// 第二个返回值说明这条事件改没改动状态，正是投影注册表那道变更闸要的答案。
//
// 一条读不回来的负载当作没有这条覆盖（状态不动）：这个函数在读路径上，它的职责是
// 「当前旋钮是什么」，不是「这条日志合不合法」。后者是 [Service.ValidateEvent] 的事。
func applyKnob(state KnobState, event sessionlog.Event) (KnobState, bool) {
	switch event.Type {
	case EventPreset:
		var data PresetData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Preset == "" {
			return state, false
		}
		state.Preset = data.Preset
		return state, true
	case userapproval.EventPolicy:
		var data userapproval.PolicyData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Policy == "" {
			return state, false
		}
		state.Approval = data.Policy
		return state, true
	case sessionlog.EventSessionEndSeed:
		if state.Seeded {
			return state, false
		}
		state.Seeded = true
		return state, true
	default:
		return state, false
	}
}

// FoldKnobs 把一段日志折成旋钮状态。
//
// 源: packages/interaction/permission-presets/src/index.ts:118-134
//
// 新增: DSH 这边只有投影单元这一条读法——它的服务从
// `ctx.sessionProjections.stateOf(session, 'permissions')` 取状态，取不到就抛。
// Go 这边服务自己折（成例见
// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.EffectivePolicy]、
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.FoldMode]），投影单元只是
// 同一个转移的另一个出口。这样一个没装投影注册表的装配照样切得动预设——DSH 那边
// 那种装配会在第一次 `current()` 上炸。
func FoldKnobs(events []sessionlog.Event) KnobState {
	var state KnobState
	for _, event := range events {
		state, _ = applyKnob(state, event)
	}
	return state
}
