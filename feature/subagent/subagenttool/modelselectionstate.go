// 本文件的作用：那张授权表在一条会话上耐久的那一份——一条只进日志的事件、一个把它
// 折回来的宿主投影，以及「读一次」和「记一次」这两条路。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts

package subagenttool

import (
	"encoding/json"
	"errors"

	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// EventModelSelectionPolicy 记下这条会话的派发工具向模型开放了孩子的提供方、模型
// 和推理档位选择。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:17-20
//
// 在第一次模型请求之前追加；一条都没有就是那份路由写死的定义。它**只进日志**：
// 不带 SurfaceOp，也永远不进模型历史。
const EventModelSelectionPolicy sessionlog.EventType = "subagent/model-selection-policy"

// ModelSelectionEventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: 理由与 [github.com/snight1983/ds-harness-go/feature/subagent.EventTypes] 逐字相同——
// Go 没有声明合并，[sessionlog.Vocabulary] 是个闭合的值，所以装配方要自己拼：
//
//	vocabulary := sessionlog.CoreVocabulary().
//		With(subagent.EventTypes()...).
//		With(subagenttool.ModelSelectionEventTypes()...)
//
// 不拼的话，一段开过模型选择的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
//
// 名字里带 ModelSelection 而不是光叫 EventTypes：本包是个工具包，只有这一件事往
// 日志里写，叫全了读的人才不用回来查它到底管几种。
func ModelSelectionEventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventModelSelectionPolicy}
}

// ModelSelectionPolicyData 是那条事件的负载。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:18-19
type ModelSelectionPolicyData struct {
	// AllowedModels 是这条会话可以为一个孩子显式挑选的那些确切路由。
	AllowedModels []AllowedRoute `json:"allowedModels"`
}

// ModelSelectionProjectionKey 是那份授权表占的投影键。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:38
const ModelSelectionProjectionKey = "subagentModelSelectionPolicy"

// modelSelectionStateVersion 是那份状态的作废版本号。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:39
const modelSelectionStateVersion = 1

// modelSelectionState 是那份授权表的折叠状态。
//
// 新增: DSH 的状态直接就是 `AllowedModelRoute[] | null`。Go 这边落盘的检查点行按
// 一个 JSON 对象存，所以裹一层：Routes 为 nil 表示还没记过，非 nil 表示记过了，
// 而这两件事正是那个 apply 判「写一次就不再改」的依据。
type modelSelectionState struct {
	// Routes 是记下来的那张表；nil 表示这条日志上还没有过那条事件。
	Routes []AllowedRoute `json:"routes,omitempty"`
}

// RegisterModelSelectionProjection 把那份授权表登进投影注册表，返回注销它的函数。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:37-51
//
// 它是**只给宿主看的**：View 是 nil，所以它进检查点、但不进推送给界面的那份快照
// （见 [github.com/snight1983/ds-harness-go/sessionlog/projection.Definition.View]）。
// 模型能挑哪几条路由是宿主的授权决定，界面不需要，模型更不需要。
func RegisterModelSelectionProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("subagenttool: 需要一个投影注册表")
	}
	return projection.Register(registry, projection.Definition[modelSelectionState]{
		Key:          ModelSelectionProjectionKey,
		StateVersion: modelSelectionStateVersion,
		Init:         func() modelSelectionState { return modelSelectionState{} },
		Apply:        applyModelSelection,
		DecodeState:  projection.StrictDecoder[modelSelectionState](),
	})
}

// applyModelSelection 按「写一次就定了」折出那张耐久的授权表。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:42-50
//
// 新增: DSH 那个 apply 会在负载畸形、或者表是空的时候**抛**。Go 的一次折叠不许
// 报错（[github.com/snight1983/ds-harness-go/sessionlog/projection.Definition.Apply] 没有错误
// 返回），所以那两种坏负载在这里折成「还没记过」，做法与
// [github.com/snight1983/ds-harness-go/feature/subagent] 那个 applyIdentity 逐字相同。
// 折成「还没记过」而不是「记了个空表」，为的是这两件事在下游必须是同一件：
// 没有授权表就不许显式挑路由，而不是「有一张谁都不许」——后者会把一条本该走继承的
// 派发也一并拒掉。
func applyModelSelection(state modelSelectionState, event sessionlog.Event) (modelSelectionState, bool) {
	if state.Routes != nil || event.Type != EventModelSelectionPolicy {
		return state, false
	}
	var data ModelSelectionPolicyData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return state, false
	}
	if len(data.AllowedModels) == 0 || AssertAllowedRoutes(data.AllowedModels) != nil {
		return state, false
	}
	return modelSelectionState{Routes: data.AllowedModels}, true
}

// ModelSelectionPolicyOf 读出一条会话上记着的那张授权表。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:59-64
//
// 交回 nil 表示这条会话跑的是那份路由写死的定义。交出去的是一份脱离的拷贝——
// [github.com/snight1983/ds-harness-go/sessionlog/projection.Registry.StateOf] 交回的是活的引用。
func ModelSelectionPolicyOf(registry *projection.Registry, live *session.Session) *ModelSelectionPolicy {
	if registry == nil || live == nil {
		return nil
	}
	state, found := registry.StateOf(liveView{live}, ModelSelectionProjectionKey)
	if !found {
		return nil
	}
	typed, ok := state.(modelSelectionState)
	if !ok || typed.Routes == nil {
		return nil
	}
	return &ModelSelectionPolicy{Routes: append([]AllowedRoute(nil), typed.Routes...)}
}

// liveView 把一个活会话贴成投影注册表读得懂的视图。
//
// 新增: 活会话交出来的是 Seq（下一条事件的序号），而
// [github.com/snight1983/ds-harness-go/sessionlog/projection.SessionView] 要的名字是 NextSeq——
// 同一个数，两个名字。做法与 [github.com/snight1983/ds-harness-go/feature/subagent] 那个同名类型
// 逐字相同；那个是包内私有的，跨包用不上，所以在这里再贴一次，而不是为这道面
// 往活会话上加一个别名方法。
type liveView struct{ live *session.Session }

// ID 是这个会话的标识。
func (v liveView) ID() sessionlog.SessionID { return v.live.ID() }

// Events 是这份日志当下的快照。
func (v liveView) Events() []sessionlog.Event { return v.live.Events() }

// NextSeq 是下一条事件将要用的 seq。
func (v liveView) NextSeq() int { return v.live.Seq() }

// recordModelSelection 把那张授权表记一次，且只记一次。
//
// 源: packages/subagent/tool-subagent/src/model-selection-state.ts:72-81
//
// 记的时机是这份定义能走到一次模型请求之前——也就是它装上这个作用域的那一刻。
func recordModelSelection(
	registry *projection.Registry,
	live *session.Session,
	allowedModels []AllowedRoute,
) error {
	if ModelSelectionPolicyOf(registry, live) != nil {
		return nil
	}
	payload, err := json.Marshal(ModelSelectionPolicyData{
		AllowedModels: append([]AllowedRoute(nil), allowedModels...),
	})
	if err != nil {
		return err
	}
	_, err = live.Append(sessionlog.Event{Type: EventModelSelectionPolicy, Data: payload})
	return err
}
