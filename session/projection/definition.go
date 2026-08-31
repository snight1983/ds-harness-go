// 本文件的作用：一个域要交出来的那份计算单元长什么样，以及框架从一个活会话
// 身上要看的那三样东西。
//
// 源: packages/session/session-projection/src/index.ts:34-136

package projection

import (
	"bytes"
	"encoding/json"

	"ds-harness-go/session"
)

// SessionView 是投影从一个活会话身上要看的全部东西。
//
// 新增: DSH 那边直接收一个 `Session` 对象——那是循环那一块（DESIGN.md 第八节
// 第 6 块）的类型，本包在第 2 块。Go 的办法是收接口不收具体类型：这里只列出
// 投影真正用到的三样，等活会话被移过来时它天然满足这个接口，本包不需要改。
type SessionView interface {
	// ID 是这个会话的身份，单元格按它归档。
	ID() session.SessionID
	// Events 是这个会话在内存里的完整日志，按 seq 升序。
	//
	// 返回的切片只会被读，不会被本包改动或留存。
	Events() []session.Event
	// NextSeq 是下一条事件将要用的 seq；空日志时为 0。
	//
	// [Snapshot.AsOfSeq] 就是它减一。
	NextSeq() int
}

// Definition 是一个域交出来的状态驱动计算单元：一份纯粹的同步折叠，加上
// 声明和一个可选的客户端视图——绝不是一个不透明的 getter。框架替它把每一条
// 已提交的事件推过 [Definition.Apply]；域自己不持有任何订阅，只拥有这段计算。
//
// 源: packages/session/session-projection/src/index.ts:34-82
//
// 所有函数都必须是同步的（一个异步单元会把读的一方那道一致性切口撕开），
// 而且 S 必须能原样进出 JSON（这是检查点能落盘的前提）。
type Definition[S any] struct {
	// Key 是这个单元占的那个投影键，在一个 [Registry] 里唯一。
	Key string

	// StateVersion 是持久检查点的作废版本号：序列化出来的状态字段变了、
	// 或者折叠的语义变了，就往上加一，好让旧单元写下的行被丢掉，
	// 而不是被往前折成一堆垃圾。必须是非负整数。
	StateVersion int

	// Init 给出空日志对应的状态。
	Init func() S

	// Apply 是那个纯转移：上一个状态 + 一条已提交的事件 → 下一个状态。
	//
	// 第二个返回值说这次转移有没有真的改变什么。不关心这条事件的单元返回
	// (state, false)，框架就一点下游开销都不产生——不算视图，也不通知任何人。
	//
	// 新增: DSH 那边靠「返回同一个引用」加 `Object.is` 表达「没变」。
	// 见本包文档「这里和 DSH 不一样的地方」。
	Apply func(state S, event session.Event) (S, bool)

	// DecodeState 把一份落过盘的状态读回来，读不成就报错。
	//
	// 它是必填的，而且是本包唯一一处**必须**由域自己写的校验。理由是这里的
	// 字节来自盘上：可能是另一个构建写的、可能被人手改过、可能根本就不是这个
	// 键的状态。[StateVersion] 只挡得住「版本对不上」，挡不住「版本对得上但
	// 内容不对」。想要一份严格的实现直接用 [StrictDecoder]。
	DecodeState func(data json.RawMessage) (S, error)

	// View 把状态折成给客户端看的整值；为 nil 表示这是一个只给宿主看的单元。
	//
	// 只给宿主看的单元不进 [Registry.Snapshot] 的 Values，但照样进
	// [Registry.Checkpoint]。
	View func(state S) any
}

// StrictDecoder 给出一个「多一个字段就报错」的 [Definition.DecodeState]。
//
// 新增: DSH 用 zod 校验落盘状态，zod 默认就会拒掉认不出来的键。Go 的
// [encoding/json.Unmarshal] 默认相反——它把不认识的字段默默扔掉，于是一份
// 形状对不上的旧状态会被读成一个「字段都在、值全是零」的状态，然后被往前
// 折成垃圾。这个帮手把 Go 的默认掰回 DSH 的默认，让「填对」和「填成 fail-open」
// 一样便宜。
func StrictDecoder[S any]() func(data json.RawMessage) (S, error) {
	return func(data json.RawMessage) (S, error) {
		var state S
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil {
			var zero S
			return zero, err
		}
		return state, nil
	}
}

// erasedDefinition 是抹掉类型参数之后的单元，驱动机器拿着的就是它。
//
// 源: packages/session/session-projection/src/index.ts:128-136
//
// 登记那一刻已经用泛型把类型证明过了，所以这里的 any 是安全的：每一个闭包
// 都只会拿到自己那次登记塞进去的那个类型的值。
type erasedDefinition struct {
	key          string
	stateVersion int
	init         func() any
	apply        func(state any, event session.Event) (any, bool)
	decodeState  func(data json.RawMessage) (any, error)
	// view 为 nil 表示这是一个只给宿主看的单元。
	view func(state any) any
}

// erase 把一份具体类型的单元抹成驱动机器认的形状。
func erase[S any](definition Definition[S]) erasedDefinition {
	erased := erasedDefinition{
		key:          definition.Key,
		stateVersion: definition.StateVersion,
		init:         func() any { return definition.Init() },
		apply: func(state any, event session.Event) (any, bool) {
			next, changed := definition.Apply(state.(S), event)
			return next, changed
		},
		decodeState: func(data json.RawMessage) (any, error) {
			state, err := definition.DecodeState(data)
			if err != nil {
				return nil, err
			}
			return state, nil
		},
	}
	if definition.View != nil {
		erased.view = func(state any) any { return definition.View(state.(S)) }
	}
	return erased
}

// unitCell 是一个单元在一个会话上的水位缓存。
//
// 源: packages/session/session-projection/src/index.ts:138-143
type unitCell struct {
	// state 是折到 observedSeq 为止的状态。
	state any
	// observedSeq 是最后一条被推过 [Definition.Apply] 的事件的 seq
	// （不管那次转移有没有改变什么）；空日志时为 -1。
	observedSeq int
}

// registration 是一次活着的登记：单元本身，加上它逐会话的单元格。
//
// 源: packages/session/session-projection/src/index.ts:145-161
type registration struct {
	def   erasedDefinition
	cells map[session.SessionID]*unitCell
	// refs 是共用这个单元的登记方数量，最后一个走的那个把键删掉。
	//
	// 它存在是因为一份单元定义本来就服务所有会话，而登记方是逐会话的：
	// 同一个工具包挂在 N 个会话上就会把同一个键登记 N 次。不数一下的话，
	// 第一个登记方会独占那个注销函数，它的会话一结束就把这个投影从**所有**
	// 还活着的会话上抹掉。
	refs int
}
