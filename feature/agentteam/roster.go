// 本文件的作用：花名册上那三个动作——起一个队友、列出这支团队、打断一个队友。
//
// 源: packages/experimental/agent-team/src/roster.ts

package agentteam

import (
	"context"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage"
)

// SpawnRequest 是起一个队友要给的东西。
//
// 源: packages/experimental/agent-team/src/types.ts（SpawnTeammateRequest）
type SpawnRequest struct {
	// Name 是这个队友的名字，团队之内唯一，定下来不许改。
	Name string
	// Description 是他负责什么，给别的队友看。
	Description string
	// Provider 是起这个会话用的子 Agent 提供方。
	Provider string
	// Context 是全新开一个会话还是从队长此刻的上文分一支。
	Context MemberContext
	// Prompt 是交给他的第一句话。
	Prompt llm.Content
}

// Spawn 起一个队友，交回花名册上那一行。
//
// 源: packages/experimental/agent-team/src/roster.ts:245-336（spawnAdmitted）
//
// 分两步落盘，中间才去起会话：
//
//	第一步  占名字。查重名、查人数上限、写一行 [PhaseProvisioning]，一次条件写做完。
//	第二步  起会话。成了转 [PhaseActive]，没成转 [PhaseFailed] 并记下原因。
//
// 这两步之间进程没了，花名册上会留下一行停在 provisioning——那正是要的：
// 反过来先起会话再落盘的话，那个半开的子会话不会在任何地方留下痕迹。
//
// **必须在队长活着的那个副本上发起。**起一个可续子 Agent 要交出队长那个活 agent
// 对象（[subagent.StartRequest.Parent]），一个只有身份没有对象的队长起不出孩子来。
func (s *Service) Spawn(ctx context.Context, lead sessionlog.SessionID, request SpawnRequest) (MemberView, error) {
	name, err := ValidateMemberName(request.Name)
	if err != nil {
		return MemberView{}, err
	}
	description, err := RequiredText(request.Description, "队友职责", MaxMemberDescriptionLength)
	if err != nil {
		return MemberView{}, err
	}
	provider, err := RequiredText(request.Provider, "提供方", MaxProviderLength)
	if err != nil {
		return MemberView{}, err
	}
	switch request.Context {
	case ContextFresh, ContextFork:
	default:
		return MemberView{}, newError(CodeInvalidArgument, "上文来源 %q 不认得", request.Context)
	}
	if len(request.Prompt) == 0 {
		return MemberView{}, newError(CodeInvalidArgument, "起一个队友要给他第一句话")
	}
	parent, live := s.agents.Agent(lead)
	if !live {
		return MemberView{}, newError(CodeInvalidTarget, "队长 %q 不在本副本上，起不了队友", lead)
	}

	team := TeamID(lead)
	member := Member{
		ID:          sessionlog.SessionID(s.newID()),
		Name:        name,
		Description: description,
		Provider:    provider,
		Context:     request.Context,
		Phase:       PhaseProvisioning,
	}
	if err := s.reserveMember(ctx, team, lead, member); err != nil {
		return MemberView{}, err
	}

	// DSH 在这里还要等孩子那条初始提示词落盘才敢把这一行写成 active
	// （packages/experimental/agent-team/src/roster.ts:339-386，checkpointInitialPrompt）。
	// 本包不需要那一步：StartContinuable 交回来的时候提示词已经进了孩子的收件箱，
	// 而会话本身就在介质上，没有「已接受但还没落盘」这个中间态，所以交回来的
	// [subagent.ContinuableStart] 这里没有用处——身份是本方预留的，消息 id 没人记。
	_, startErr := s.subagents.StartContinuable(ctx, subagent.ContinuableStartSpec{
		Provider: provider,
		Label:    description,
		ChildID:  member.ID,
		Request: subagent.StartRequest{
			Prompt: request.Prompt,
			Parent: parent,
		},
	})
	if startErr != nil {
		failed := member
		failed.Phase = PhaseFailed
		failed.Failure = startErr.Error()
		if _, settleErr := s.settleMember(ctx, team, failed); settleErr != nil {
			// 起没起成是已经发生的事，把它记下来才是这一步的目的；记不下来要连着原因一起说，
			// 只报后者会让调用方以为队友起成了。
			return MemberView{}, wrapError(CodeStoreFailed, startErr, "队友 %q 没起来，失败原因也没记下去：%v", name, settleErr)
		}
		return MemberView{}, startErr
	}

	active := member
	active.Phase = PhaseActive
	settled, err := s.settleMember(ctx, team, active)
	if err != nil {
		return MemberView{}, err
	}
	if settled.Phase != PhaseActive {
		// 会话已经起来了，花名册上却写着别的——这一行从此说的不是发生过的事。
		// 不替它选一个答案，交给调用方。
		return MemberView{}, newError(CodeProvisioningConflict,
			"队友 %q 起来了，但花名册上那一行已经被写成 %s", name, settled.Phase)
	}
	return s.memberView(active), nil
}

// Members 列出这支团队，队长排在第一行。
//
// 源: packages/experimental/agent-team/src/roster.ts:128-159（list）
func (s *Service) Members(ctx context.Context, team TeamID) ([]MemberView, error) {
	roster, err := s.roster(ctx, team)
	if err != nil {
		return nil, err
	}
	lead := sessionlog.SessionID(roster.Lead)
	views := make([]MemberView, 0, len(roster.Members)+1)
	views = append(views, MemberView{
		ID:     lead,
		Name:   LeadName,
		Role:   RoleLead,
		Status: s.liveStatus(lead),
	})
	for _, member := range roster.Members {
		views = append(views, s.memberView(member))
	}
	return views, nil
}

// Team 把花名册和任务板折成一份 [TeamView]。
//
// 源: packages/experimental/agent-team/src/types.ts:94-98（TeamView）
func (s *Service) Team(ctx context.Context, team TeamID) (TeamView, error) {
	members, err := s.Members(ctx, team)
	if err != nil {
		return TeamView{}, err
	}
	tasks, err := s.Tasks(ctx, team)
	if err != nil {
		return TeamView{}, err
	}
	return TeamView{Members: members, Tasks: tasks}, nil
}

// Interrupt 打断一个队友此刻那段活动，不动他收件箱里压着的东西。
//
// 源: packages/experimental/agent-team/src/roster.ts:203-214（interrupt）
//
// 凭的是队长那份祖先权（[subagent.AuthorityAncestor]）——队友本来就都是队长起出来的。
// 目标不在本副本上就交回 [StatusInactive]：打断是一件进程内的事，
// 别的副本上那个队友该由那台机器上的谁去打断。
func (s *Service) Interrupt(ctx context.Context, who Caller, targetName string) (MemberStatus, error) {
	if !who.Lead {
		return "", newError(CodeLeadRequired, "只有队长能打断队友")
	}
	roster, err := s.roster(ctx, who.Team)
	if err != nil {
		return "", err
	}
	target, err := resolveActiveMember(roster, targetName)
	if err != nil {
		return "", err
	}
	if target == who.ID {
		return "", newError(CodeInvalidTarget, "队长打断不了自己")
	}
	caller, live := s.agents.Agent(who.ID)
	if !live {
		return "", newError(CodeInvalidTarget, "队长 %q 不在本副本上，打断不了谁", who.ID)
	}
	previous := s.liveStatus(target)
	if previous == StatusInactive {
		return StatusInactive, nil
	}
	if err := s.subagents.Interrupt(target, subagent.InterruptAuthority{
		Kind:  subagent.AuthorityAncestor,
		Agent: caller,
	}); err != nil {
		return "", wrapError(CodeInvalidTarget, err, "打断队友 %q 没成", targetName)
	}
	return previous, nil
}

// reserveMember 把一行 [PhaseProvisioning] 占进花名册。
//
// 源: packages/experimental/agent-team/src/roster.ts:268-277
//
// 花名册还不存在就先建一份：团队不是单独建出来的，一个会话有了第一个队友才成为队长，
// 见 [TeamID]。建和改分两步不会丢东西——建撞上别的副本说明花名册已经有了，接着改就行。
func (s *Service) reserveMember(ctx context.Context, team TeamID, lead sessionlog.SessionID, member Member) error {
	blank := Roster{Team: team, Lead: string(lead)}
	if err := s.rosters.Create(ctx, string(team), blank); err != nil && !isStorageCode(err, storage.CodeStaleRevision) {
		return storeError(err, "建团队 %q 的花名册没成", team)
	}
	_, err := s.updateRoster(ctx, team, func(roster Roster) (Roster, error) {
		if _, taken := roster.Member(member.Name); taken {
			return roster, newError(CodeMemberNameTaken, "这支团队里已经有人叫 %q 了", member.Name)
		}
		if len(roster.Members) >= s.config.MaxMembers {
			return roster, newError(CodeMemberLimit, "这支团队的人数已经到 %d 了", s.config.MaxMembers)
		}
		roster.Members = append(roster.Members, member)
		return roster, nil
	})
	return err
}

// settleMember 把一行从 [PhaseProvisioning] 改成终态，交回改完之后那一行。
//
// 源: packages/experimental/agent-team/src/roster.ts:462-480（settleProvisioning）
//
// 已经不在 provisioning 上就原样交回去，不覆盖：那说明有别的路先settle过它，
// 而 provisioning 到终态只走一次。调用方拿两者一比就知道自己这次算不算数。
func (s *Service) settleMember(ctx context.Context, team TeamID, terminal Member) (Member, error) {
	var settled Member
	_, err := s.updateRoster(ctx, team, func(roster Roster) (Roster, error) {
		current, found := roster.MemberByID(string(terminal.ID))
		if !found {
			return roster, newError(CodeProvisioningConflict, "刚占下的队友 %q 从花名册上没了", terminal.Name)
		}
		if current.Phase != PhaseProvisioning {
			settled = current
			return roster, nil
		}
		for index := range roster.Members {
			if roster.Members[index].ID == terminal.ID {
				roster.Members[index] = terminal
				break
			}
		}
		settled = terminal
		return roster, nil
	})
	if err != nil {
		return Member{}, err
	}
	return settled, nil
}

// memberView 把花名册上一行折成给出去的样子。
//
// 源: packages/experimental/agent-team/src/roster.ts:435-448（memberView）
func (s *Service) memberView(member Member) MemberView {
	view := MemberView{
		ID:          member.ID,
		Name:        member.Name,
		Role:        RoleTeammate,
		Description: member.Description,
		Provider:    member.Provider,
		Context:     member.Context,
	}
	switch member.Phase {
	case PhaseFailed:
		view.Status = StatusFailed
		if member.Failure != "" {
			view.Diagnostics = []string{member.Failure}
		}
	case PhaseProvisioning:
		view.Status = StatusProvisioning
	default:
		view.Status = s.liveStatus(member.ID)
	}
	return view
}

// liveStatus 问「这个会话此刻在本副本上是什么状态」。
//
// 新增: DSH 问的是一个进程内的 agent 注册表，问它等于问全世界。本包多副本，
// 所以问不到只能说成 [StatusInactive]——「本副本看不见它」，不是「它死了」。
func (s *Service) liveStatus(id sessionlog.SessionID) MemberStatus {
	live, found := s.agents.Agent(id)
	if !found {
		return StatusInactive
	}
	if live.Status() == agent.StatusRunning {
		return StatusRunning
	}
	return StatusIdle
}
