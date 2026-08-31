// 本文件的作用：本包会报的那几种错误。
//
// 新增: DSH 侧全部是 `throw new Error(字符串)`，调用方分不出种类，只能看消息。
// Go 里错误是要被 errors.Is 分派的，所以这里按「读到之后该做什么」分类，
// 而不是按「哪个函数报的」分类。

package session

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedValue 表示这段字节排不成、或者读不回本包的某个值。
	//
	// 该做的事：这份日志坏了或者根本不是会话日志，没救。
	ErrMalformedValue = errors.New("session: 值的编码格式不对")

	// ErrUnknownEventType 表示日志里有一条本构建不认识、又没标 [Event.Ignorable] 的事件。
	//
	// 该做的事：**拒绝重建这个会话**，提示升级。这是 DSH 在
	// known-event-types.ts 上写死的那条规则：一条不认识的必需事件可能改变
	// 后面整段日志的解释方式，静默跳过等于重建出一个错的会话——而它「能解析」，
	// 所以不会有任何别的东西报警。
	ErrUnknownEventType = errors.New("session: 事件类型不在本构建的词汇表里")

	// ErrUnknownCancelCause 表示一个 [TurnEndCancelCause] 的判别标签不是登记过的五个之一。
	//
	// 单独立着而不是并进 [ErrMalformedValue]，理由和 llm.ErrUnknownChunkType 一样：
	// 字节是好的，只是这个词本构建没学过，那是升级提示不是损坏。
	//
	// 取消原因这个联合是**封闭**的，而它外面那层 [TurnEndReason] 是开放的
	// （不认识的落进 [UnknownTurnEnd]）。这个不对称是 DSH 自己的：
	// TurnEndReasonMap 是一个可被插件合并扩展的映射，AgentCancelCause 是一个
	// 普通的联合类型，插件加不进去。照抄是对的——真要新增一个取消原因，
	// 按 [FormatVersion] 自己的规矩那是一次「核心事件语义」改动，得升版本号，
	// 于是版本检查会先一步拦住，这里这条错误是第二道。
	ErrUnknownCancelCause = errors.New("session: 回合取消原因的类型不认识")

	// ErrSurfaceViolation 表示日志违反了表面层的规则（该带的标记没带、
	// 替换范围找不到、来源事件引用不合法）。
	//
	// 该做的事：这是**写日志那一方**的缺陷，不是版本差异。
	ErrSurfaceViolation = errors.New("session: 事件违反表面层规则")

	// ErrTraceViolation 表示日志违反了回合／步骤的关系约束（seq 不递增、
	// 回合没关就开下一个、工具结果没有在先的调用）。
	//
	// 该做的事：同上，写的一方有缺陷。和 [ErrSurfaceViolation] 分开是因为
	// 两者指向的产出方通常不是同一个：表面层的标记由追加事件的那一处填，
	// 回合关系由循环的控制流决定。
	ErrTraceViolation = errors.New("session: 事件违反回合与步骤的关系约束")
)

// wrapMalformed 把一个底层的编解码错误裹进 [ErrMalformedValue]，前面加一句中文说明。
//
// 新增: 本包里「读不回来」这件事出现几十次，每次都写一遍同样的 fmt.Errorf 只会
// 让格式串各写各的。裹两层是有意的：errors.Is 认得出 ErrMalformedValue，
// errors.Unwrap 一路下去还能拿到 encoding/json 原本那条。
func wrapMalformed(what string, err error) error {
	return fmt.Errorf("%w：%s：%w", ErrMalformedValue, what, err)
}

// malformed 报一条没有底层错误可裹的编码格式问题。
func malformed(format string, args ...any) error {
	return fmt.Errorf("%w：%s", ErrMalformedValue, fmt.Sprintf(format, args...))
}
