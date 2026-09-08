// 本文件的作用：那段团队策略指引，以及把它和这十件工具一起装上一个作用域的那条路。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:30-37,158-171,388-395

package agentteamtool

import (
	"context"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/agentteam"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// SectionName 是那段团队策略指引在系统提示词里的段名。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:165
const SectionName = "team:policy"

// SectionOrder 决定这段指引排在系统提示词的哪个位置。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:166（SECTION_ORDERS.TEAM_POLICY = 600）
//
// 新增: DSH 那套次序是 100 起步、以 100 递增的一列大数，本包按
// [github.com/snight1983/ds-harness-go/harness/systemprompt.PromptSection.Order] 的
// 约定重排过一遍（PLAN_POLICY 的 500 在这边是 50）。团队策略跟着排成 60：
// 在部署方人设（0）之后、工具指引（100–199）之前，并且**紧跟在计划策略后面**——
// DSH 那两条也是相邻的两档，这层前后关系比绝对值要紧。
const SectionOrder = 60

// policy 是给队长和队友看的那段协作指引，一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:31-37
const policy = `Agent Teams is available in this session, but create teammates only when the user explicitly asks to use Agent Teams or teammates.

The Team Lead and all teammates share the same working directory and filesystem. Edits are immediately visible to every member. Split write work into disjoint scopes, record expected write scopes on shared tasks, and use task dependencies when work must be ordered. Write-scope overlap is advisory, not a lock.

Prefer read/edit/write for file changes. If a file operation returns FS_STALE_VERSION, read the current file, rebase your intended change onto the new content, and retry. Bash, formatters, code generators, and scripts are not fully protected by the filesystem version guard; coordinate them explicitly and have the Lead review the final diff and run tests.

Use send_message for quiet information that must not start an idle teammate. Use followup_task when the target should run another turn. A delivered peer item starts with its stable message id and sender name. A successful send is already durable even when its result says queued; do not resend it. Shared-task workflow is list, get, claim with the current revision, perform the work, then complete. Task readiness never starts an owner. Before wait_agent, use list_agents and make sure another required member is running or provisioning; use followup_task first when the required member is inactive. wait_agent observes only changes after that call starts, never wakes a member, and returns noProgress immediately when no other member can produce a change. Re-list after wakeup or timeout. The Lead must wait for required teammates before giving the final answer.`

// identityFormat 是缀在那段指引后面、说清「你是谁」的一句话，一个字都不许改译。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:169
const identityFormat = "\n\nYour Team role is %s; your Team name is %s; Team id is %s."

// guidance 是这段指引每次装配时求出来的正文。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:167-170
//
// 每次装配现解算一次发起人，而不是装的时候求一次存下来：一个队友的角色和名字定在
// 花名册上，而花名册在介质上随时可能被队长改（reassign、interrupt 都会动它）。
func (c *Controller) guidance(ctx context.Context, assemble systemprompt.AssembleContext) (string, error) {
	live, err := c.agentOf(assemble.Scope)
	if err != nil {
		return "", err
	}
	if live == nil {
		return "", fmt.Errorf("%s requires a calling Agent", SectionName)
	}
	who, err := c.service.ResolveCaller(ctx, c.team, live.ID())
	if err != nil {
		return "", err
	}
	role := roleOf(who)
	return policy + fmt.Sprintf(identityFormat, role, who.Name, who.Team), nil
}

// roleOf 说这一位在花名册上算哪一档。
//
// 新增: [github.com/snight1983/ds-harness-go/feature/agentteam.Caller] 上只有一个
// Lead 布尔位，没有 Role 那一列——它是解算发起人时顺手算出来的身份，不是花名册上
// 的一行。译回那两个字要在这里做。
func roleOf(who agentteam.Caller) agentteam.MemberRole {
	if who.Lead {
		return agentteam.RoleLead
	}
	return agentteam.RoleTeammate
}

// Install 把那段策略指引和这十件工具装上一个作用域，交回把它们一起摘下来的函数。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:158-195
//
// 次序照 DSH 的 install：先指引，再花名册那组、收件箱那组、任务板那组。中途失败就按
// 反序摘干净——半装上去意味着模型手上有一件起得了队友、却没有那段指引管着的工具，
// 而那段指引正是「什么时候才该起队友」的唯一说明。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("agentteamtool: 需要一个工具运行时")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("agentteamtool: 需要一个系统提示词注册表")
	}

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		return errors.Join(failures...)
	}
	// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
	abort := func(what string, err error) (func(context.Context) error, error) {
		_ = undo(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("agentteamtool: 装%s失败：%w", what, err)
	}

	remove, err := deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  c.guidance,
	})
	if err != nil {
		return abort("团队策略指引", err)
	}
	installed = append(installed, remove)

	for _, definition := range []*tools.Definition{
		c.newSpawnTool(),
		c.newMessageTool(SendMessageTool, agentteam.DeliveryQuiet),
		c.newMessageTool(FollowupTool, agentteam.DeliveryWakeup),
		c.newListAgentsTool(),
		c.newWaitTool(),
		c.newInterruptTool(),
		c.newTaskCreateTool(),
		c.newTaskListTool(),
		c.newTaskGetTool(),
		c.newTaskUpdateTool(),
	} {
		dispose, err := deps.Tools.Register(ctx, owner, definition)
		if err != nil {
			return abort(definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
