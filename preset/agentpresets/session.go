// 本文件的作用：一个会话**实际跑在**哪一份预设上这件事——那条只进日志的选择事件，
// 以及为什么重建必须读日志而不是只读创建头。
//
// 源: packages/preset/agent-presets/src/session.ts

package agentpresets

import (
	"encoding/json"

	"ds-harness-go/session"
)

// EventPresetSelected 记下这个会话在**建出来之后**改点了哪一份预设。
//
// 源: packages/preset/agent-presets/src/session.ts:26
//
// 创建头记的是「建的时候点的是哪个」，那是一件创建事实、不可改。但一个还空着的
// 会话可以换预设，而这次更改的效果**活得比那扇空窗久**：第一轮以及之后的每一轮
// 都跑在新装的那份组合上。记下它才让日志诚实，而且这是本仓「模型看得见 ⟺ 记进
// 日志」那条规矩直接要求的——预设决定模型看得见的工具 schema 和提示词段落。
//
// 它是**只进日志**的：不上模型可见表面，也不进派生历史。
const EventPresetSelected session.EventType = "agent-preset/selected"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: DSH 靠 `declare module` 把它合并进 SessionEventMap。Go 没有声明合并，
// [session.Vocabulary] 是个闭合的值，所以改成由本包交出这张单子、装配方自己拼
// （成例见 [ds-harness-go/plan/planmode.EventTypes]）：
//
//	vocabulary := session.CoreVocabulary().With(agentpresets.EventTypes()...)
//
// 不拼的话，一段换过预设的日志会被 [session.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []session.EventType {
	return []session.EventType{EventPresetSelected}
}

// SelectedData 是 [EventPresetSelected] 的负载。
//
// 源: packages/preset/agent-presets/src/session.ts:26
type SelectedData struct {
	// AgentPreset 是从这条事件往后这个会话跑的那份预设 id。
	AgentPreset string `json:"agentPreset"`
}

// ResolveSessionPreset 交出一个会话**实际跑在**的那份预设，最新的一次选择算数。
//
// 源: packages/preset/agent-presets/src/session.ts:48-54
//
// 头提供创建时那个值，之后每一次更改都是一条记进日志的事件，所以最后一条就是答案。
// 只读头会把一个换过预设的会话按它**建出来时**那份组合重建，而不是它那段历史真正
// 产生于其下的那一份。
//
// 交回空串表示这套部署一份预设都不组装。
//
// 新增: DSH 收一个 `{ header, events }` 对象（PresetBearingSession）。Go 这边两个
// 参数直接给——那个接口在 TS 里的作用是给一个结构类型起名字，Go 有具名参数，
// 再包一层只是让调用方多建一个临时值。
func ResolveSessionPreset(header session.SessionHeader, events []session.Event) string {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != EventPresetSelected {
			continue
		}
		var data SelectedData
		// 新增: DSH 那边负载已经是解好的对象，这一步在那里不存在；Go 这边是原始
		// 字节，所以要多问一句「读得回来吗」（成例见 [ds-harness-go/plan/planmode] 的
		// decodeMode）。读不回来时**往前接着找**：一条更早的选择、或者创建头，
		// 都比拿一个空串当「这套部署一份预设都不装」要诚实。
		if err := json.Unmarshal(events[index].Data, &data); err != nil || data.AgentPreset == "" {
			continue
		}
		return data.AgentPreset
	}
	return header.AgentPreset
}
