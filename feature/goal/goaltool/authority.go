// 本文件的作用：那几道执行期的资格闸——认出调用方和它那个还开着的回合，
// 判断这一轮里有没有一条**人自己写的**输入，以及一次终局更新到底靠哪一份授权
// 站得住。
//
// 源: packages/goal/tool-goal/src/authority.ts

package goaltool

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// 这几句是拒收时给模型看的话，一个字都不许改译。
//
// 源: packages/goal/tool-goal/src/authority.ts:35,41,53,58,92,107
const (
	missingAgent    = "goal tools require a calling agent"
	missingTurn     = "goal tools require an open model turn"
	missingDriver   = "goal tools require the exact live calling agent inside its active driver"
	missingHuman    = "this goal operation requires a direct human turn on a top-level agent"
	missingWrapup   = "complete and blocked require a direct human turn or the current goal round"
	blockThresholds = "blocked requires at least %d consecutive goal rounds; current round is %d"
)

// Execution 是一次经过认证的目标工具调用：调用方、它所在的那个还开着的回合，
// 以及那个回合边界**之后**被接受的那些事件。
//
// 源: packages/goal/tool-goal/src/authority.ts:11-16（GoalToolExecution）
type Execution struct {
	// Agent 是那个确切的活调用方。
	Agent agent.Agent
	// Start 是围着这次调用的那条 turn/start。
	//
	// 新增: 本包自己一次都没读它——DSH 也没有。留着是因为它是那份「这一轮是谁开的」
	// 证据里不可分割的一半：Events 是「这条边界之后发生了什么」，没有边界本身，
	// 一份脱手出去的 Execution 就说不清那些事件是相对**哪一条**边界切出来的。
	Start sessionlog.Event
	// Events 是那条边界之后被接受的事件，按日志顺序。
	Events []sessionlog.Event
}

// AuthorityKind 是一次改状态的调用拿到的那份硬授权的判别。
//
// 源: packages/goal/tool-goal/src/authority.ts:20-22
type AuthorityKind string

const (
	// AuthorityDirectHuman 表示这一轮里有一条人自己写的输入。
	AuthorityDirectHuman AuthorityKind = "direct-human"
	// AuthorityGoalRound 表示这一轮就是当前目标那个准入轮次。
	AuthorityGoalRound AuthorityKind = "goal-round"
)

// Authority 是一次改状态的调用拿到的那份硬授权。
//
// 源: packages/goal/tool-goal/src/authority.ts:18-21（GoalToolAuthority）
//
// 新增: DSH 那边这是两支联合。Go 里合成一个带 Kind 判别的结构体（成例见
// [github.com/snight1983/ds-harness-go/feature/goal.Change]）：Goal 恰好在 Kind 是 [AuthorityGoalRound]
// 时非 nil。
type Authority struct {
	// Kind 是这份授权的判别。
	Kind AuthorityKind
	// Goal 是那个准入轮次所属的目标，只有 [AuthorityGoalRound] 带。
	Goal *goal.View
}

// openTurn 从日志尾巴往前走，找出围着这次调用的那个还开着的回合。
//
// 源: packages/goal/tool-goal/src/authority.ts:30-42
//
// 先撞上 turn/end 说明最近那个回合已经关了——那这次工具调用就不在任何驱动之下，
// 走的是别人硬凑出来的一条路。走到头一条边界都没有也是同一件事。
func openTurn(caller agent.Agent) (sessionlog.Event, []sessionlog.Event, error) {
	events := caller.Session().Events()
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Type {
		case sessionlog.EventTurnEnd:
			return sessionlog.Event{}, nil, fail(CodeDriverRequired, missingTurn)
		case sessionlog.EventTurnStart:
			return events[index], events[index+1:], nil
		}
	}
	return sessionlog.Event{}, nil, fail(CodeDriverRequired, missingTurn)
}

// execution 认出这次调用的 agent，并且把它那个还开着的回合切出来。
//
// 源: packages/goal/tool-goal/src/authority.ts:41-60（goalToolExecution）
//
// 三件事都得成立才算数：手里这个 agent 就是注册表里那个确切的活实例、它此刻正
// 在跑、而且这条调用链上继承下来的发起者就是它本人。第三条挡的是「一个 agent
// 借着另一个 agent 的钥匙去改目标」——目标是按 agent 记的预算，谁花得动它必须
// 由那条因果链说了算。
//
// 新增: DSH 的 `ctx.agents.currentInitiator()` 是 cordis 上的全局态。Go 里同一件
// 事是 [github.com/snight1983/ds-harness-go/harness/agent.CurrentInitiator]——它挂在 ctx 上，而工具执行体
// 拿到的 ctx 正是从那个驱动的活动 ctx 派生下来的。
func (c *Controller) execution(ctx context.Context, exec *tools.RunContext) (Execution, error) {
	if exec == nil || exec.Agent == nil {
		return Execution{}, fail(CodeAgentRequired, missingAgent)
	}
	// 查不回来和「查回来的不是它」在这里是同一件事：两种情况下手里都没有一个够
	// 格的调用方。分开报只会告诉模型注册表里还有没有别的东西。
	caller, err := c.agentOf(exec.Agent)
	if err != nil || caller == nil {
		return Execution{}, fail(CodeDriverRequired, missingDriver)
	}
	live, present := c.agents.Get(caller.ID())
	initiator, driving := agent.CurrentInitiator(ctx)
	if !present || live != caller || caller.Status() != agent.StatusRunning ||
		!driving || initiator != caller {
		return Execution{}, fail(CodeDriverRequired, missingDriver)
	}
	start, events, err := openTurn(caller)
	if err != nil {
		return Execution{}, err
	}
	return Execution{Agent: caller, Start: start, Events: events}, nil
}

// userMessage 把一条事件读成一条用户消息；不是用户消息或者读不回来就交回 false。
//
// 读不回来当成「不算数」而不是当成错误：这两道闸问的都是「这一轮里**有没有**一条
// 够格的输入」，一条坏掉的事件显然不是那一条。真要拿这段日志当权威去回放的是
// [github.com/snight1983/ds-harness-go/feature/goal.Fold]，那一侧一个字节都不放过。
func userMessage(event sessionlog.Event) (llm.Message, bool) {
	if event.Type != sessionlog.EventUserMessage {
		return llm.Message{}, false
	}
	var data sessionlog.UserMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return llm.Message{}, false
	}
	return data.Message, true
}

// hasDirectHumanInput 问「这一轮里有没有一条宿主认过的人类输入」。
//
// 源: packages/goal/tool-goal/src/authority.ts:70-74
//
// 两道条件缺一不可：调用方得是一个**顶层** agent，而且这条边界之后得有一条
// kind 是 user 的消息。子 agent 一律不算——它那条链上的输入来自它的父，不来自人。
//
// [github.com/snight1983/ds-harness-go/harness/agent.Agent.Followup] 和 Steer 省掉来源时落下来的就是
// user，所以任何一个**不是人**的生产方必须自己带上来源，否则它就白捡了这份资格。
func (c *Controller) hasDirectHumanInput(execution Execution) bool {
	if !slices.Contains(c.agents.Roots(), execution.Agent) {
		return false
	}
	return slices.ContainsFunc(execution.Events, func(event sessionlog.Event) bool {
		message, ok := userMessage(event)
		return ok && message.Source != nil && message.Source.SourceKind() == llm.SourceUser
	})
}

// isMatchingGoalRound 问「这一轮是不是当前目标那个确切的准入轮次」。
//
// 源: packages/goal/tool-goal/src/authority.ts:77-83
//
// 三样都得对上：目标身份、发这一轮时的修订号、以及轮号。对不上意味着这条边界
// 之后那条目标消息说的是另一个目标、或者另一次修订——那份授权不该转给这一次。
func isMatchingGoalRound(execution Execution, view *goal.View) bool {
	return slices.ContainsFunc(execution.Events, func(event sessionlog.Event) bool {
		message, ok := userMessage(event)
		if !ok {
			return false
		}
		source, ok := goal.ParseSource(message.Source)
		return ok && source.GoalID == view.ID && source.Revision == view.Revision &&
			source.Round == view.RoundsStarted
	})
}

// requireDirectHuman 要求这次调用坐在一条直接的人类回合上。
//
// 源: packages/goal/tool-goal/src/authority.ts:94-102（requireDirectHuman）
//
// create、edit、pause、resume 走这条：它们要么开出一份新预算，要么改动预算本身，
// 要么把一个停住的目标重新推起来——每一件都不该由模型自己批给自己。
func (c *Controller) requireDirectHuman(execution Execution) error {
	if c.hasDirectHumanInput(execution) {
		return nil
	}
	return fail(CodeAuthorityRequired, missingHuman)
}

// completionAuthority 给一次 complete 或者 blocked 定出它靠的是哪一份授权。
//
// 源: packages/goal/tool-goal/src/authority.ts:104-117（completionAuthority）
//
// 比 [Controller.requireDirectHuman] 松一档，而且必须松：一个自动往下推的目标
// 如果只有人才能宣布它结束，那它就永远结束不了，只能耗光轮数预算。
//
// 取目标失败**不当错报**，当成「没有目标」：这条路上真正要回答的是「这次调用够
// 不够格」，而读不回来目标的调用同样不够格；把那条内情原样抛给模型，只会让它
// 看见一句和资格无关的话。
func (c *Controller) completionAuthority(execution Execution) (Authority, error) {
	if c.hasDirectHumanInput(execution) {
		return Authority{Kind: AuthorityDirectHuman}, nil
	}
	view, err := c.service.Get(execution.Agent)
	if err == nil && view != nil && isMatchingGoalRound(execution, view) {
		return Authority{Kind: AuthorityGoalRound, Goal: view}, nil
	}
	return Authority{}, fail(CodeAuthorityRequired, missingWrapup)
}
