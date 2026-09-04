// 本文件的作用：这一层的词汇——那条日志事件与它的负载、给界面看的投影值、
// 一次选择的四种结局，以及配置和它那道门。
//
// 源: packages/plan/plan-mode/src/types.ts
// 源: packages/plan/plan-mode/src/index.ts:46-119

package planmode

import (
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ExitToolName 是那件面向模型的退出工具的名字。
//
// 源: packages/plan/plan-mode/src/index.ts:56-60（EXIT_PLAN_MODE）
//
// 它在计划模式关着的时候也保持注册，好让请求里的工具表在进出计划模式时纹丝不动。
const ExitToolName = "exit_plan_mode"

// CommandName 是那条面向用户的斜杠命令的名字，不带斜杠。
//
// 源: packages/plan/plan-mode/src/index.ts:297
const CommandName = "plan"

// EventMode 记下从这条事件往后计划模式开不开。
//
// 源: packages/plan/plan-mode/src/index.ts:46-55
//
// 它是**只进日志**的：不上模型可见表面，也不进派生历史。整值替换，最后一条算数；
// 一条一次都没出现过它的日志折出来就是「关着」，见 [FoldMode]。
const EventMode sessionlog.EventType = "plan/mode"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: DSH 靠 `declare module` 把它合并进 SessionEventMap。Go 没有声明合并，
// [sessionlog.Vocabulary] 是个闭合的值，所以改成由本包交出这张单子、装配方自己拼
// （成例见 [github.com/snight1983/ds-harness-go/feature/sessiontitle.EventTypes]）：
//
//	vocabulary := sessionlog.CoreVocabulary().With(planmode.EventTypes()...)
//
// 不拼的话，一段进过计划模式的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventMode}
}

// ModeData 是 [EventMode] 的负载。
//
// 源: packages/plan/plan-mode/src/index.ts:53
type ModeData struct {
	// Active 表示从这条事件往后计划模式开着。
	Active bool `json:"active"`
}

// Projection 是 plan 这个投影单元交给界面的那个值。
//
// 源: packages/plan/plan-mode/src/types.ts:19-22
//
// Active 是日志上当下生效的那个状态（第一条 [EventMode] 之前是关着）；
// Pending 在「一次记进日志的 `/plan` 选择指向另一个状态、它那条配对的
// command/done 没判它失败、而且之后还没有任何一条 [EventMode] 记下过那个状态」
// 期间为真。
//
// 「这个装配里根本没有计划模式」由这个键**整个不在**来表达，绝不用某个取值表达。
type Projection struct {
	// Active 是日志上当下生效的状态。
	Active bool `json:"active"`
	// Pending 表示有一次选择还在等下一个被接受的、回合之内的步骤前置。
	Pending bool `json:"pending"`
}

// Outcome 是一次 [Controller.Set] 的结局。
//
// 源: packages/plan/plan-mode/src/index.ts:462
type Outcome string

const (
	// OutcomeCommitted 表示这次翻转已经落进日志了。
	OutcomeCommitted Outcome = "committed"
	// OutcomeQueued 表示它挂着，等下一个被接受的、回合之内的步骤前置。
	OutcomeQueued Outcome = "queued"
	// OutcomeCancelled 表示一次方向相反的挂起选择被撤掉了，日志上的状态本来就对。
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeNoop 表示已经是那个状态了，什么都没做。
	OutcomeNoop Outcome = "noop"
)

// State 是 [Controller.Get] 交出来的那份读数。
//
// 源: packages/plan/plan-mode/src/index.ts:440
type State struct {
	// Active 是日志上当下生效的状态。
	Active bool
	// Pending 是那次挂起选择指向的状态；nil 表示没有挂起的选择。
	//
	// 新增: DSH 是 `pending?: boolean`，「没有挂起」和「挂起指向 false」靠字段
	// 在不在区分。Go 的 bool 没有「不在」，所以用指针——把它做成 bool 会让
	// 「挂着一次退出」和「什么都没挂」读起来一模一样，而这两件事的界面完全不同。
	Pending *bool
}

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("planmode: 配置不成立")

// resolveSection 验部署方拥有的那段计划指引。
//
// 源: packages/plan/plan-mode/src/index.ts:92-112（resolveConfig）
//
// 缺席或者全是空白都在构造这一刻就拒掉，而不是被忽略：一段空指引意味着计划模式
// 开着却什么都没告诉模型，那和没开的唯一区别是用户以为它开着。
//
// 新增: DSH 那个 resolveConfig 还查「有没有 section 之外的键」——那是 JS 收一份
// 任意对象才有的问题。Go 的结构体字面量写不出未知字段，编译器就是那道检查。
func resolveSection(section string) (string, error) {
	if strings.TrimSpace(section) == "" {
		return "", fmt.Errorf("%w: 需要一段非空的计划指引 Section", ErrInvalidConfig)
	}
	return section, nil
}
