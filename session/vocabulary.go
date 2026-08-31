// 本文件的作用：这个构建认识哪些事件类型，以及读到一个不认识的类型时该怎么办。
//
// 源: packages/core/session/src/known-event-types.ts

package session

import (
	"fmt"
	"maps"
	"slices"
)

// Vocabulary 是一个构建认识的事件类型集合。
//
// 源: packages/core/session/src/known-event-types.ts:1-68
//
// 新增: DSH 那边是一个模块级的常量 Set，48 个类型写死在一份文件里。
// 本包做成一个**不可变的值**：[CoreVocabulary] 给出核心的那些，
// [Vocabulary.With] 派生出加了别的包的那些。
//
// 不做成一张带锁的全局注册表，理由是那样会引入 init 顺序上的先后依赖——
// 「谁先注册」决定「读日志时认不认识」，而这是一个纯粹的读取期判据，
// 不该取决于哪几个包碰巧被导入了。一个值可以在调用点当场组装、可以在测试里
// 各要各的、可以跨块累加，没有那些问题。
type Vocabulary struct {
	// types 是这个词汇表认识的类型集合。
	//
	// 未导出并且只由 [Vocabulary.With] 复制着长，所以 Vocabulary 的值传出去之后
	// 谁都改不动。
	types map[EventType]struct{}
}

// coreTypes 是本包实现了的那十三个事件类型。
//
// 新增: DSH 的 KNOWN_SESSION_EVENT_TYPES 里有 48 个，其中 35 个属于本仓库
// 还没移植到的包。本包**只登记这 13 个**——把另外 35 个也写进来等于宣称
// 「这些我认识」，而这个构建里没有任何代码能处理它们，那正是这套机制
// 要挡住的那种静默跳过。等那些包落地时，它们各自用 [Vocabulary.With] 把
// 自己的类型加进来。
var coreTypes = []EventType{
	EventTurnStart,
	EventTurnEnd,
	EventStepStart,
	EventStepEnd,
	EventUserMessage,
	EventAssistantChunk,
	EventAssistantMessage,
	EventToolCall,
	EventToolResult,
	EventTodoWrite,
	EventRequestHeader,
	EventRequestContext,
	EventSessionEndSeed,
}

// CoreVocabulary 是本包自己实现了的那些事件类型。
func CoreVocabulary() Vocabulary {
	types := make(map[EventType]struct{}, len(coreTypes))
	for _, kind := range coreTypes {
		types[kind] = struct{}{}
	}
	return Vocabulary{types: types}
}

// With 派生出一个又认识 extra 里那些类型的词汇表，原来那个不变。
func (v Vocabulary) With(extra ...EventType) Vocabulary {
	types := make(map[EventType]struct{}, len(v.types)+len(extra))
	maps.Copy(types, v.types)
	for _, kind := range extra {
		types[kind] = struct{}{}
	}
	return Vocabulary{types: types}
}

// Knows 判断这个词汇表认不认识某个类型。
func (v Vocabulary) Knows(kind EventType) bool {
	_, ok := v.types[kind]
	return ok
}

// Types 按字典序给出这个词汇表认识的全部类型。
//
// 排序是为了让它的产出可复现——map 的遍历顺序在 Go 里是随机的，
// 一个顺序不定的清单进了错误消息或者测试断言就会随机地不一样。
func (v Vocabulary) Types() []EventType {
	return slices.Sorted(maps.Keys(v.types))
}

// CheckVocabulary 检查一段日志里有没有本构建读不懂又不能跳过的事件。
//
// 源: packages/core/session/src/known-event-types.ts:1-30
//
// 判据是 [Event.Ignorable]：一个不认识的类型带着这个标记就跳过，
// 没带就返回 [ErrUnknownEventType] ——**拒绝重建这个会话**。
// 一条不认识的必需事件可能改变后面整段日志的解释方式，静默跳过等于重建出
// 一个错的会话，而它「能解析」，所以不会有任何别的东西报警。
func CheckVocabulary(events []Event, vocabulary Vocabulary) error {
	for _, event := range events {
		if vocabulary.Knows(event.Type) || event.Ignorable {
			continue
		}
		return fmt.Errorf("%w：seq %d 的 %q", ErrUnknownEventType, event.Seq, event.Type)
	}
	return nil
}
