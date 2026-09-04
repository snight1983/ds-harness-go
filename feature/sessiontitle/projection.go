// 本文件的作用：标题那个投影单元——客户端列表行读的就是它。
//
// 源: packages/session/session-title/src/index.ts:304-318

package sessiontitle

import (
	"encoding/json"
	"errors"

	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// ProjectionKey 是标题那个单元占的投影键。
//
// 源: packages/session/session-title/src/index.ts:311
const ProjectionKey = "title"

// projectionStateVersion 是标题状态的作废版本号。
//
// 源: packages/session/session-title/src/index.ts:316
const projectionStateVersion = 1

// titleState 是标题单元的状态：就是当前那个标题，空串表示还没有过标题。
//
// 新增: DSH 那边状态的类型是 `string | null`，init 给 null。Go 这边是一个
// 具名的字符串包装：
//
//   - 用一层结构体而不是裸 string，是因为 [projection.StrictDecoder] 要
//     Unmarshal 进一个值，而一个裸的 JSON 字符串没有「多出来的字段」这个概念，
//     严格解码在它身上等于没有；包一层之后 {"title":"x"} 这个形状才有得可验。
//   - 空串就是 DSH 的 null。这两件事在这里同义：[Service] 只会追加**非空**的
//     标题（[NormalizeSessionTitle] 洗完是空串的一律拒掉），所以「标题是空串」
//     这个状态在一份合法的日志上走不到，空串只可能是「还没有过标题」。
type titleState struct {
	// Title 是当前的标题；空串表示还没有过。
	Title string `json:"title"`
}

// TitleView 是这个单元交给客户端的东西：一个普通的标题字符串。
//
// 源: packages/session/session-title/src/index.ts:315
//
// 新增: DSH 那边视图的类型直接就是 `string | null`——一个裸标量。Go 这边包成
// 一个单字段结构体，理由是投影的视图最终要排成 JSON 走到客户端去，而一个裸的
// JSON 字符串在客户端那侧没法在不破坏兼容的前提下再长出字段来。这一层壳是
// 现在花一个键，换掉以后加字段时的一次破坏性变更。
type TitleView struct {
	// Title 是当前的标题；空串表示这个会话还没有标题。
	Title string `json:"title"`
}

// projectionDefinition 是标题那个单元。
//
// 源: packages/session/session-title/src/index.ts:261-274（titleProjectionDefinition）
//
// 它是一次纯粹的 last-wins 折叠，消化的正是 [FoldSnapshot] 消化的那批事件。
// 两者有意并存：折叠函数给宿主一份**带信封事实**的完整快照（谁定的、引了哪几句、
// 什么时候定的），这个单元只给客户端那一个字符串。列表行不需要知道标题的来历，
// 而把来历一起推给每一个客户端会让每次改名都多传一份没人读的东西。
func projectionDefinition() projection.Definition[titleState] {
	return projection.Definition[titleState]{
		Key:          ProjectionKey,
		StateVersion: projectionStateVersion,
		Init:         func() titleState { return titleState{} },
		Apply:        applyTitle,
		DecodeState:  projection.StrictDecoder[titleState](),
		// 这里逐字段挑，不用 TitleView(state) 那个转换。两个类型今天字段一样，
		// 但它们**要往两个方向长**：状态那边以后会加上标题的来历，视图这边有意
		// 不给（见上面）。写成转换就是把这条分岔钉成一次编译错误。
		//
		//lint:ignore S1016 见上
		View: func(state titleState) any { return TitleView{Title: state.Title} },
	}
}

// applyTitle 是标题那个纯转移。
//
// 源: packages/session/session-title/src/index.ts:314
func applyTitle(state titleState, event sessionlog.Event) (titleState, bool) {
	if event.Type != EventSessionTitle {
		return state, false
	}
	var data EventData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		// 新增: [projection.Definition.Apply] 按构造是不会失败的（它没有错误
		// 返回），所以一条读不回来的标题事件在这里只能保持原值。这和
		// [FoldSnapshot] 那边**有意分家**：那边报错，因为它的调用方接得住；
		// 这边只能在「保持旧标题」和「清成空」之间选，而清空会让列表行上的
		// 名字突然消失，比停在一个旧名字上难解释得多。
		return state, false
	}
	if data.Title == state.Title {
		return state, false
	}
	return titleState{Title: data.Title}, true
}

// RegisterProjection 把标题单元登进注册表，返回把它注销的函数。
//
// 源: packages/session/session-title/src/index.ts:308-318
//
// 新增: DSH 那边这件事在服务的构造函数里，用 ctx.inject(['sessionProjections'])
// 包着——投影服务在场就登、不在场就跳过。Go 没有那个容器，「投影服务在不在场」
// 就是装配方手上有没有那张注册表，所以它成了一次显式的调用（成例见
// todo.RegisterProjection 与 tokenmeter.RegisterProjections）。
//
// 它和 [New] 是分开的：一份无头装配（比如只跑日志回放的那种）根本不建注册表，
// 那时候标题服务照样该能工作。
//
// 返回的注销函数是幂等的。
func RegisterProjection(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("sessiontitle: 需要一个投影注册表")
	}
	return projection.Register(registry, projectionDefinition())
}
