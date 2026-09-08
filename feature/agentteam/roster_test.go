// 本文件的作用：花名册那三个动作的用例——起队友的两步落盘、列团队、打断。

package agentteam

import (
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestSpawn起成了的队友在岗且落了盘(t *testing.T) {
	box := boot(t)

	view := box.spawn(t, "reviewer")
	if view.Name != "reviewer" || view.Role != RoleTeammate {
		t.Fatalf("交回来的该是队友 reviewer，得到 %+v", view)
	}
	if view.Status != StatusIdle {
		t.Fatalf("起成之后孩子活在本副本上，该是 %q，得到 %q", StatusIdle, view.Status)
	}

	roster, err := box.service.roster(t.Context(), box.team())
	if err != nil {
		t.Fatalf("读花名册不该失败：%v", err)
	}
	member, found := roster.Member("reviewer")
	if !found {
		t.Fatal("花名册上该有 reviewer")
	}
	if member.Phase != PhaseActive {
		t.Fatalf("落盘的那一行该是 %q，得到 %q", PhaseActive, member.Phase)
	}
	if member.ID != view.ID {
		t.Fatalf("交回来的身份和落盘的对不上：%q vs %q", view.ID, member.ID)
	}
}

func TestSpawn预留的身份就是起会话时用的那个(t *testing.T) {
	box := boot(t)

	view := box.spawn(t, "reviewer")
	started, _, _ := box.subagents.calls()
	if len(started) != 1 {
		t.Fatalf("该起了一次会话，得到 %d 次", len(started))
	}
	if started[0].ChildID != view.ID {
		t.Fatalf("起会话用的该是先占下的身份 %q，得到 %q", view.ID, started[0].ChildID)
	}
	if started[0].Request.Parent == nil || started[0].Request.Parent.ID() != leadID {
		t.Fatal("起队友要把队长那个活 agent 交出去")
	}
}

func TestSpawn起会话失败时那一行留在花名册上并记下原因(t *testing.T) {
	box := boot(t)
	box.subagents.startErr = errors.New("提供方不认得")

	_, err := box.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name:        "reviewer",
		Description: "看代码",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	})
	if err == nil {
		t.Fatal("起会话失败时这一步该失败")
	}

	roster, rosterErr := box.service.roster(t.Context(), box.team())
	if rosterErr != nil {
		t.Fatalf("读花名册不该失败：%v", rosterErr)
	}
	member, found := roster.Member("reviewer")
	if !found {
		t.Fatal("起没起成也该在花名册上留下那一行")
	}
	if member.Phase != PhaseFailed {
		t.Fatalf("那一行该是 %q，得到 %q", PhaseFailed, member.Phase)
	}
	if !strings.Contains(member.Failure, "提供方不认得") {
		t.Fatalf("失败原因该记下来，得到 %q", member.Failure)
	}
}

func TestSpawn一支团队里不许重名(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")

	_, err := box.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name:        "reviewer",
		Description: "又一个",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	})
	if !errors.Is(err, CodeMemberNameTaken) {
		t.Fatalf("该报 %q，得到 %v", CodeMemberNameTaken, err)
	}
}

func TestSpawn人数到上限就不许再起(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxMembers = 1 })
	box.spawn(t, "first")

	_, err := box.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name:        "second",
		Description: "第二个",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	})
	if !errors.Is(err, CodeMemberLimit) {
		t.Fatalf("该报 %q，得到 %v", CodeMemberLimit, err)
	}
}

func TestSpawn队长不在本副本上就起不了队友(t *testing.T) {
	box := boot(t)
	box.agents.drop(leadID)

	_, err := box.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name:        "reviewer",
		Description: "看代码",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	})
	if !errors.Is(err, CodeInvalidTarget) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidTarget, err)
	}
	started, _, _ := box.subagents.calls()
	if len(started) != 0 {
		t.Fatal("队长不在就不该去起会话")
	}
}

func TestSpawn入参不合式一律当场拒(t *testing.T) {
	box := boot(t)
	valid := SpawnRequest{
		Name:        "reviewer",
		Description: "看代码",
		Provider:    "in-process",
		Context:     ContextFresh,
		Prompt:      llm.Content{llm.TextBlock{Text: "开工"}},
	}

	cases := map[string]struct {
		mutate func(*SpawnRequest)
		code   Code
	}{
		"名字带空格":   {func(r *SpawnRequest) { r.Name = "code reviewer" }, CodeInvalidMemberName},
		"职责空着":    {func(r *SpawnRequest) { r.Description = "  " }, CodeInvalidArgument},
		"提供方空着":   {func(r *SpawnRequest) { r.Provider = "" }, CodeInvalidArgument},
		"上文来源不认得": {func(r *SpawnRequest) { r.Context = "borrowed" }, CodeInvalidArgument},
		"没有第一句话":  {func(r *SpawnRequest) { r.Prompt = nil }, CodeInvalidArgument},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			request := valid
			testCase.mutate(&request)
			if _, err := box.service.Spawn(t.Context(), leadID, request); !errors.Is(err, testCase.code) {
				t.Fatalf("该报 %q，得到 %v", testCase.code, err)
			}
		})
	}
}

func TestMembers队长排在第一行(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")
	box.spawn(t, "writer")

	members, err := box.service.Members(t.Context(), box.team())
	if err != nil {
		t.Fatalf("列团队不该失败：%v", err)
	}
	if len(members) != 3 {
		t.Fatalf("该有队长加两个队友，得到 %d 行", len(members))
	}
	if members[0].Name != LeadName || members[0].Role != RoleLead {
		t.Fatalf("第一行该是队长，得到 %+v", members[0])
	}
	if members[1].Name != "reviewer" || members[2].Name != "writer" {
		t.Fatalf("队友该按加入次序排，得到 %q %q", members[1].Name, members[2].Name)
	}
}

func TestMembers不在本副本上的队友是inactive(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)

	members, err := box.service.Members(t.Context(), box.team())
	if err != nil {
		t.Fatalf("列团队不该失败：%v", err)
	}
	if members[1].Status != StatusInactive {
		t.Fatalf("本副本看不见它，该是 %q，得到 %q", StatusInactive, members[1].Status)
	}
}

func TestMembers正在跑的队友是running(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.add(&stubAgent{id: view.ID, status: agent.StatusRunning})

	members, err := box.service.Members(t.Context(), box.team())
	if err != nil {
		t.Fatalf("列团队不该失败：%v", err)
	}
	if members[1].Status != StatusRunning {
		t.Fatalf("该是 %q，得到 %q", StatusRunning, members[1].Status)
	}
}

func TestTeam把花名册和任务板折在一起(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")
	lead := box.caller(t, leadID)
	kept := box.createTask(t, lead, CreateTaskRequest{Subject: "留着的"})
	dropped := box.createTask(t, lead, CreateTaskRequest{Subject: "删掉的"})
	box.update(t, lead, UpdateTaskRequest{
		TaskID: dropped.ID, ExpectedRev: dropped.Rev, Action: ActionDelete,
	})

	view, err := box.service.Team(t.Context(), box.team())
	if err != nil {
		t.Fatalf("折团队不该失败：%v", err)
	}
	if len(view.Members) != 2 {
		t.Fatalf("该有队长加一个队友，得到 %d 行", len(view.Members))
	}
	if len(view.Tasks) != 1 || view.Tasks[0].ID != kept.ID {
		t.Fatalf("删掉的任务不该出现在团队视图里，得到 %+v", view.Tasks)
	}
}

func TestInterrupt凭队长那份祖先权打断队友(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")

	previous, err := box.service.Interrupt(t.Context(), box.caller(t, leadID), "reviewer")
	if err != nil {
		t.Fatalf("打断不该失败：%v", err)
	}
	if previous != StatusIdle {
		t.Fatalf("打断之前它是闲着的，该交回 %q，得到 %q", StatusIdle, previous)
	}
	_, _, interrupts := box.subagents.calls()
	if len(interrupts) != 1 || interrupts[0] != view.ID {
		t.Fatalf("该打断 %q，得到 %v", view.ID, interrupts)
	}
}

func TestInterrupt只有队长能打断(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.spawn(t, "writer")

	_, err := box.service.Interrupt(t.Context(), box.caller(t, view.ID), "writer")
	if !errors.Is(err, CodeLeadRequired) {
		t.Fatalf("该报 %q，得到 %v", CodeLeadRequired, err)
	}
}

func TestInterrupt队长打断不了自己(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")

	_, err := box.service.Interrupt(t.Context(), box.caller(t, leadID), LeadName)
	if !errors.Is(err, CodeInvalidTarget) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidTarget, err)
	}
}

func TestInterrupt目标不在本副本上就说inactive(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)

	previous, err := box.service.Interrupt(t.Context(), box.caller(t, leadID), "reviewer")
	if err != nil {
		t.Fatalf("目标不在本副本上不该报错：%v", err)
	}
	if previous != StatusInactive {
		t.Fatalf("该交回 %q，得到 %q", StatusInactive, previous)
	}
	_, _, interrupts := box.subagents.calls()
	if len(interrupts) != 0 {
		t.Fatal("看不见的目标不该发出打断")
	}
}

func TestResolveCaller认队长认队友拒外人(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")

	lead := box.caller(t, leadID)
	if !lead.Lead || lead.Name != LeadName {
		t.Fatalf("队长该解算成 %q 且带队长身份，得到 %+v", LeadName, lead)
	}
	teammate := box.caller(t, view.ID)
	if teammate.Lead || teammate.Name != "reviewer" {
		t.Fatalf("队友该解算成 reviewer 且不带队长身份，得到 %+v", teammate)
	}
	if _, err := box.service.ResolveCaller(t.Context(), box.team(), sessionlog.SessionID("外人")); !errors.Is(err, CodeNotMember) {
		t.Fatalf("该报 %q，得到 %v", CodeNotMember, err)
	}
}

func TestResolveCaller没有这支团队时说清楚(t *testing.T) {
	box := boot(t)

	_, err := box.service.ResolveCaller(t.Context(), TeamID("没起过的"), leadID)
	if !errors.Is(err, CodeTeamNotFound) {
		t.Fatalf("该报 %q，得到 %v", CodeTeamNotFound, err)
	}
}

func TestOpen三样依赖缺一不可(t *testing.T) {
	cases := map[string]func(*Config){
		"没有域":         func(config *Config) { config.Domain = nil },
		"没有子Agent能力":  func(config *Config) { config.Subagents = nil },
		"没有活agent查询面": func(config *Config) { config.Agents = nil },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			config := Config{
				Domain:    facility(t),
				Subagents: &stubSubagents{},
				Agents:    newStubAgents(),
				Logger:    quiet(),
			}
			breakIt(&config)
			if _, err := Open(t.Context(), config); !errors.Is(err, CodeInvalidConfig) {
				t.Fatalf("该报 %q，得到 %v", CodeInvalidConfig, err)
			}
		})
	}
}
