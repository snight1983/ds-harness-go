// 本文件的作用：一份目标怎么排成给人看的那几行字——状态、阻塞原因、已用轮数，
// 以及「此刻还能敲哪几条命令」那一行。
//
// 源: packages/goal/command-goal/src/index.ts:46-106

package goalcommand

import (
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/interaction/commands"
)

// renderGoal 把一份目标排成一段人读的文字，**不**把 CAS 修订号露出去。
//
// 源: packages/goal/command-goal/src/index.ts:75-95
//
// 修订号是这一层和那台目标服务之间的事：人拿它做不了任何决定，而露出来只会让
// 那段文字看着像调试输出。
//
// 新增: DSH 有一个 phaseLabel 函数，把四种阶段各自映射成一个人读的词——而那四个
// 映射全是恒等的。它在 TS 里的真正作用是靠 assertNever 逼着「加一种阶段就得回来
// 改这里」；Go 没有那个穷尽检查，留下它就只是一段永远返回入参的代码。所以直接
// 用阶段本身。
func renderGoal(title string, view *goal.View) (commands.Result, error) {
	lines := []string{title, "Status: " + string(view.Phase)}
	if view.Phase == goal.PhaseBlocked {
		// 新增: DSH 这里是一句 throw，标着 v8 ignore——耐久回放保证每一个 blocked
		// 的目标都带着它验过的原因。Go 这边同样走不到，但它是一次**解指针**，
		// 悄悄跳过会排出一段看不出问题、却少了最关键那行的文字。
		if view.BlockedReason == nil {
			return commands.Result{}, fmt.Errorf(
				"goalcommand: 阻塞的目标 %q 没带阻塞原因", view.ID)
		}
		lines = append(lines, "Blocker: "+view.BlockedReason.Code+": "+view.BlockedReason.Message)
	}
	lines = append(lines,
		"Objective: "+view.Objective,
		fmt.Sprintf("Rounds: %d/%d", view.RoundsStarted, view.MaxGoalRounds),
		"Activation: "+string(view.Activation),
		"",
		"Commands: "+commandHint(view),
	)
	return commands.Result{Kind: commands.ResultSuccess, Text: strings.Join(lines, "\n")}, nil
}

// commandHint 给出从这一个确切的活状态出发、此刻真敲得动的那几条命令。
//
// 源: packages/goal/command-goal/src/index.ts:59-73
//
// active 那一支还要再看一眼续推资格：一个耐久上 active、进程里却已经 disarmed 的
// 目标（续会话、分叉、换驱动之后都是这样）该提示 resume 而不是 pause——对着它敲
// pause 什么都不会发生，而人会以为自己按上了。
//
// 新增: 最后那个 default 是 DSH 没有的（那边是 assertNever）。
// [github.com/snight1983/ds-harness-go/goal/goal.Phase] 是具名字符串类型，挡不住一个认不出的阶段；而在
// 一条面向人的命令里，为了一个仅仅是标不出名字的阶段就把整条命令炸掉，比告诉他
// 那两条永远成立的命令更糟。
func commandHint(view *goal.View) string {
	switch view.Phase {
	case goal.PhaseActive:
		if view.Activation == goal.Armed {
			return "/goal edit <objective>, /goal pause, /goal clear"
		}
		return "/goal edit <objective>, /goal resume, /goal clear"
	case goal.PhasePaused, goal.PhaseBlocked:
		return "/goal edit <objective>, /goal resume, /goal clear"
	case goal.PhaseComplete:
		return "/goal <objective>, /goal clear"
	default:
		return "/goal, /goal clear"
	}
}

// missingGoal 是一次「这条命令得先有个目标」的回话。
//
// 源: packages/goal/command-goal/src/index.ts:103-106
func missingGoal(action string) commands.Result {
	return commands.Result{
		Kind: commands.ResultError,
		Text: "No goal is currently set; /goal " + action + " requires one. " + usage,
	}
}
