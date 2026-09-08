// 本文件的作用：共享任务板的用例——八个动作各自的授权与迁移、版本号、整图校验，
// 以及写入范围那几句提醒。

package agentteam

import (
	"errors"
	"strings"
	"testing"
)

// ptr 交出一个指向副本的指针，给 [UpdateTaskRequest] 那几个可选字段用。
func ptr[T any](value T) *T { return &value }

func TestCreateTask新建的那一条是待办且就绪(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)

	view := box.createTask(t, lead, CreateTaskRequest{
		Subject:     "写用例",
		Description: "把任务板那八个动作都盖上",
		WriteScopes: []string{"./feature/agentteam/"},
	})
	if view.ID != "task-1" {
		t.Fatalf("第一条该发号 task-1，得到 %q", view.ID)
	}
	if view.Rev != 1 || view.Status != TaskPending || !view.Ready {
		t.Fatalf("新建的该是第 1 版、待办、就绪，得到 %+v", view.Task)
	}
	if len(view.WriteScopes) != 1 || view.WriteScopes[0] != "feature/agentteam" {
		t.Fatalf("写入范围该归一化成 feature/agentteam，得到 %v", view.WriteScopes)
	}
}

func TestCreateTask身份按次序发号(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)

	first := box.createTask(t, lead, CreateTaskRequest{Subject: "第一条"})
	second := box.createTask(t, lead, CreateTaskRequest{Subject: "第二条"})
	if first.ID != "task-1" || second.ID != "task-2" {
		t.Fatalf("该按次序发号，得到 %q %q", first.ID, second.ID)
	}
}

func TestCreateTask有前置的那一条不就绪(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)

	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "先做这条"})
	blocked := box.createTask(t, lead, CreateTaskRequest{
		Subject:   "等前面那条",
		BlockedBy: []TaskID{blocker.ID},
	})
	if blocked.Ready {
		t.Fatal("前置没做完，这条不该就绪")
	}
}

func TestCreateTask入参不合式一律当场拒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)

	cases := map[string]struct {
		request CreateTaskRequest
		code    Code
	}{
		"标题空着":      {CreateTaskRequest{Subject: "  ", Description: "正文"}, CodeInvalidArgument},
		"正文空着":      {CreateTaskRequest{Subject: "标题", Description: "  "}, CodeInvalidArgument},
		"标题太长":      {CreateTaskRequest{Subject: strings.Repeat("字", MaxSubjectLength+1), Description: "正文"}, CodeInvalidArgument},
		"写入范围是绝对路径": {CreateTaskRequest{Subject: "标题", Description: "正文", WriteScopes: []string{"/etc"}}, CodeInvalidWriteScope},
		"写入范围往上跑":   {CreateTaskRequest{Subject: "标题", Description: "正文", WriteScopes: []string{"../别处"}}, CodeInvalidWriteScope},
		"前置不在板上":    {CreateTaskRequest{Subject: "标题", Description: "正文", BlockedBy: []TaskID{"task-99"}}, CodeTaskNotFound},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := box.service.CreateTask(t.Context(), lead, testCase.request); !errors.Is(err, testCase.code) {
				t.Fatalf("该报 %q，得到 %v", testCase.code, err)
			}
		})
	}
}

func TestCreateTask到上限就不许再建(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxTasks = 1 })
	lead := box.leader(t)
	box.createTask(t, lead, CreateTaskRequest{Subject: "唯一那条"})

	_, err := box.service.CreateTask(t.Context(), lead, CreateTaskRequest{
		Subject: "第二条", Description: "放不下了",
	})
	if !errors.Is(err, CodeTaskLimit) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskLimit, err)
	}
}

func TestCreateTask删掉的那条不占名额(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxTasks = 1 })
	lead := box.leader(t)
	first := box.createTask(t, lead, CreateTaskRequest{Subject: "先建的"})
	box.update(t, lead, UpdateTaskRequest{TaskID: first.ID, ExpectedRev: first.Rev, Action: ActionDelete})

	second := box.createTask(t, lead, CreateTaskRequest{Subject: "补上的"})
	if second.ID != "task-2" {
		t.Fatalf("发号不回收，该是 task-2，得到 %q", second.ID)
	}
}

func TestUpdateTask版本号对不上就不许改(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: task.Rev + 1, Action: ActionClaim,
	})
	if !errors.Is(err, CodeTaskStaleRevision) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskStaleRevision, err)
	}
}

func TestUpdateTask每改一次版本号加一(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	claimed := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim})
	if claimed.Rev != 2 {
		t.Fatalf("改一次该是第 2 版，得到 %d", claimed.Rev)
	}
	released := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 2, Action: ActionRelease})
	if released.Rev != 3 {
		t.Fatalf("再改一次该是第 3 版，得到 %d", released.Rev)
	}
}

func TestUpdateTask板上没有这一条时说清楚(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: "task-99", ExpectedRev: 1, Action: ActionClaim,
	})
	if !errors.Is(err, CodeTaskNotFound) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskNotFound, err)
	}
}

func TestUpdateTask删掉的那条一律不许再动(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "要删的"})
	deleted := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionDelete})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: deleted.Rev, Action: ActionClaim,
	})
	if !errors.Is(err, CodeTaskDeleted) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskDeleted, err)
	}
}

func TestUpdateTask不认得的动作当场拒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: "publish",
	})
	if !errors.Is(err, CodeInvalidArgument) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidArgument, err)
	}
}

func TestClaim认领之后归认领人且在做(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	view := box.spawn(t, "reviewer")
	teammate := box.caller(t, view.ID)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	claimed := box.update(t, teammate, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim})
	if claimed.Status != TaskInProgress || claimed.Owner != view.ID {
		t.Fatalf("该归 reviewer 且在做，得到 %+v", claimed.Task)
	}
	if claimed.OwnerName != "reviewer" {
		t.Fatalf("认领人的名字该折出来，得到 %q", claimed.OwnerName)
	}
	if claimed.Ready {
		t.Fatal("在做的任务说不上就绪")
	}
}

func TestClaim别人认领过的就抢不走(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	view := box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, box.caller(t, view.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim,
	})

	second := box.spawn(t, "writer")
	_, err := box.service.UpdateTask(t.Context(), box.caller(t, second.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionClaim,
	})
	if !errors.Is(err, CodeTaskAlreadyClaimed) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskAlreadyClaimed, err)
	}
}

func TestClaim前置没做完就认领不了(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "前置"})
	blocked := box.createTask(t, lead, CreateTaskRequest{Subject: "后面那条", BlockedBy: []TaskID{blocker.ID}})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: blocked.ID, ExpectedRev: blocked.Rev, Action: ActionClaim,
	})
	if !errors.Is(err, CodeTaskBlocked) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskBlocked, err)
	}
}

func TestClaim前置做完之后就认领得了(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "前置"})
	blocked := box.createTask(t, lead, CreateTaskRequest{Subject: "后面那条", BlockedBy: []TaskID{blocker.ID}})

	claimed := box.update(t, lead, UpdateTaskRequest{TaskID: blocker.ID, ExpectedRev: 1, Action: ActionClaim})
	box.update(t, lead, UpdateTaskRequest{TaskID: blocker.ID, ExpectedRev: claimed.Rev, Action: ActionComplete})

	after := box.update(t, lead, UpdateTaskRequest{
		TaskID: blocked.ID, ExpectedRev: blocked.Rev, Action: ActionClaim,
	})
	if after.Status != TaskInProgress {
		t.Fatalf("前置做完了该认领得了，得到 %q", after.Status)
	}
}

func TestRelease放回去之后没人认领(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim})

	released := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionRelease,
	})
	if released.Status != TaskPending || released.Owner != "" {
		t.Fatalf("该变回待办且没人认领，得到 %+v", released.Task)
	}
}

func TestRelease没在做的放不回去(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionRelease,
	})
	if !errors.Is(err, CodeTaskInvalidTransition) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskInvalidTransition, err)
	}
}

func TestEdit改标题正文和写入范围(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "旧标题", Description: "旧正文"})

	edited := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionEdit,
		Subject:     ptr("新标题"),
		Description: ptr("新正文"),
		WriteScopes: ptr([]string{"docs/", "docs"}),
	})
	if edited.Subject != "新标题" || edited.Description != "新正文" {
		t.Fatalf("标题正文该改了，得到 %+v", edited.Task)
	}
	if len(edited.WriteScopes) != 1 || edited.WriteScopes[0] != "docs" {
		t.Fatalf("写入范围该归一化并去重，得到 %v", edited.WriteScopes)
	}
}

func TestEdit一样都不给就拒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionEdit,
	})
	if !errors.Is(err, CodeInvalidArgument) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidArgument, err)
	}
}

func TestEdit给了空标题和没给不是一回事(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionEdit, Subject: ptr("   "),
	})
	if !errors.Is(err, CodeInvalidArgument) {
		t.Fatalf("给了个空标题该报 %q，得到 %v", CodeInvalidArgument, err)
	}
}

func TestEdit不是认领人也不是队长就改不了(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	owner := box.spawn(t, "reviewer")
	other := box.spawn(t, "writer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, box.caller(t, owner.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim,
	})

	_, err := box.service.UpdateTask(t.Context(), box.caller(t, other.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionEdit, Subject: ptr("插一脚"),
	})
	if !errors.Is(err, CodeTaskUnauthorized) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskUnauthorized, err)
	}
}

func TestEdit队长改得动别人认领的(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	owner := box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, box.caller(t, owner.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim,
	})

	edited := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionEdit, Subject: ptr("队长改的"),
	})
	if edited.Subject != "队长改的" {
		t.Fatalf("队长该改得动，得到 %q", edited.Subject)
	}
}

func TestSetDependencies整份替换前置(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	first := box.createTask(t, lead, CreateTaskRequest{Subject: "甲"})
	second := box.createTask(t, lead, CreateTaskRequest{Subject: "乙"})
	target := box.createTask(t, lead, CreateTaskRequest{Subject: "丙", BlockedBy: []TaskID{first.ID}})

	updated := box.update(t, lead, UpdateTaskRequest{
		TaskID: target.ID, ExpectedRev: target.Rev, Action: ActionSetDependencies,
		BlockedBy: ptr([]TaskID{second.ID}),
	})
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != second.ID {
		t.Fatalf("该整份换成 %q，得到 %v", second.ID, updated.BlockedBy)
	}

	cleared := box.update(t, lead, UpdateTaskRequest{
		TaskID: target.ID, ExpectedRev: updated.Rev, Action: ActionSetDependencies,
		BlockedBy: ptr([]TaskID{}),
	})
	if len(cleared.BlockedBy) != 0 {
		t.Fatalf("空名单该把前置清空，得到 %v", cleared.BlockedBy)
	}
}

func TestSetDependencies不给名单就拒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionSetDependencies,
	})
	if !errors.Is(err, CodeInvalidArgument) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidArgument, err)
	}
}

func TestSetDependencies前置写坏了当场拒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	other := box.createTask(t, lead, CreateTaskRequest{Subject: "别的"})
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	cases := map[string]struct {
		blockers []TaskID
		code     Code
	}{
		"挡自己":  {[]TaskID{task.ID}, CodeTaskDependencyCycle},
		"写了两遍": {[]TaskID{other.ID, other.ID}, CodeInvalidArgument},
		"不在板上": {[]TaskID{"task-99"}, CodeTaskNotFound},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
				TaskID: task.ID, ExpectedRev: task.Rev, Action: ActionSetDependencies,
				BlockedBy: ptr(testCase.blockers),
			})
			if !errors.Is(err, testCase.code) {
				t.Fatalf("该报 %q，得到 %v", testCase.code, err)
			}
		})
	}
}

func TestSetDependencies绕成圈的从远处也拦得住(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	first := box.createTask(t, lead, CreateTaskRequest{Subject: "甲"})
	second := box.createTask(t, lead, CreateTaskRequest{Subject: "乙", BlockedBy: []TaskID{first.ID}})
	third := box.createTask(t, lead, CreateTaskRequest{Subject: "丙", BlockedBy: []TaskID{second.ID}})

	// 把甲的前置指向丙，就成了 甲→丙→乙→甲；这次动的是甲，坏的是整张图。
	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: first.ID, ExpectedRev: first.Rev, Action: ActionSetDependencies,
		BlockedBy: ptr([]TaskID{third.ID}),
	})
	if !errors.Is(err, CodeTaskDependencyCycle) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskDependencyCycle, err)
	}
}

func TestComplete做完再重新打开(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim})

	done := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionComplete,
	})
	if done.Status != TaskCompleted || done.Owner != leadID {
		t.Fatalf("做完了该留着认领人，得到 %+v", done.Task)
	}

	reopened := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: done.Rev, Action: ActionReopen,
	})
	if reopened.Status != TaskPending || reopened.Owner != "" {
		t.Fatalf("重新打开该变回待办且没人认领，得到 %+v", reopened.Task)
	}
}

func TestComplete没在做的做不完(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionComplete,
	})
	if !errors.Is(err, CodeTaskInvalidTransition) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskInvalidTransition, err)
	}
}

func TestReopen没做完的打不开(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionReopen,
	})
	if !errors.Is(err, CodeTaskInvalidTransition) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskInvalidTransition, err)
	}
}

func TestReassign队长按名字把任务指给队友(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	view := box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	assigned := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionReassign, Owner: "reviewer",
	})
	if assigned.Owner != view.ID || assigned.Status != TaskInProgress {
		t.Fatalf("该指给 reviewer 且在做，得到 %+v", assigned.Task)
	}
	if assigned.OwnerName != "reviewer" {
		t.Fatalf("认领人的名字该折出来，得到 %q", assigned.OwnerName)
	}
}

func TestReassign名字空着就是收回来(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	assigned := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionReassign, Owner: "reviewer",
	})

	taken := box.update(t, lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: assigned.Rev, Action: ActionReassign,
	})
	if taken.Owner != "" || taken.Status != TaskPending {
		t.Fatalf("该收回来变回待办，得到 %+v", taken.Task)
	}
}

func TestReassign只有队长能改派(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	view := box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), box.caller(t, view.ID), UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionReassign, Owner: "reviewer",
	})
	if !errors.Is(err, CodeLeadRequired) {
		t.Fatalf("该报 %q，得到 %v", CodeLeadRequired, err)
	}
}

func TestReassign指给一个不在花名册上的名字(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: 1, Action: ActionReassign, Owner: "查无此人",
	})
	if !errors.Is(err, CodeMemberNotFound) {
		t.Fatalf("该报 %q，得到 %v", CodeMemberNotFound, err)
	}
}

func TestReassign前置没做完就指不出去(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	box.spawn(t, "reviewer")
	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "前置"})
	blocked := box.createTask(t, lead, CreateTaskRequest{Subject: "后面那条", BlockedBy: []TaskID{blocker.ID}})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: blocked.ID, ExpectedRev: blocked.Rev, Action: ActionReassign, Owner: "reviewer",
	})
	if !errors.Is(err, CodeTaskBlocked) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskBlocked, err)
	}
}

func TestReassign做完的任务改不了派给谁(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	box.spawn(t, "reviewer")
	task := box.createTask(t, lead, CreateTaskRequest{Subject: "一条任务"})
	claimed := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: 1, Action: ActionClaim})
	done := box.update(t, lead, UpdateTaskRequest{TaskID: task.ID, ExpectedRev: claimed.Rev, Action: ActionComplete})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: task.ID, ExpectedRev: done.Rev, Action: ActionReassign, Owner: "reviewer",
	})
	if !errors.Is(err, CodeTaskInvalidTransition) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskInvalidTransition, err)
	}
}

func TestDelete还挡着别人的删不得(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "前置"})
	box.createTask(t, lead, CreateTaskRequest{Subject: "后面那条", BlockedBy: []TaskID{blocker.ID}})

	_, err := box.service.UpdateTask(t.Context(), lead, UpdateTaskRequest{
		TaskID: blocker.ID, ExpectedRev: blocker.Rev, Action: ActionDelete,
	})
	if !errors.Is(err, CodeTaskHasDependents) {
		t.Fatalf("该报 %q，得到 %v", CodeTaskHasDependents, err)
	}
}

func TestDelete挡着的那条自己删了之后就删得动(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	blocker := box.createTask(t, lead, CreateTaskRequest{Subject: "前置"})
	dependent := box.createTask(t, lead, CreateTaskRequest{Subject: "后面那条", BlockedBy: []TaskID{blocker.ID}})
	box.update(t, lead, UpdateTaskRequest{TaskID: dependent.ID, ExpectedRev: dependent.Rev, Action: ActionDelete})

	deleted := box.update(t, lead, UpdateTaskRequest{
		TaskID: blocker.ID, ExpectedRev: blocker.Rev, Action: ActionDelete,
	})
	if deleted.Status != TaskDeleted {
		t.Fatalf("该删掉了，得到 %q", deleted.Status)
	}
}

func TestTaskView写入范围撞上正在做的那条就提醒(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	view := box.spawn(t, "reviewer")
	running := box.createTask(t, lead, CreateTaskRequest{
		Subject: "正在做的", WriteScopes: []string{"feature/agentteam"},
	})
	box.update(t, box.caller(t, view.ID), UpdateTaskRequest{
		TaskID: running.ID, ExpectedRev: 1, Action: ActionClaim,
	})

	overlapping := box.createTask(t, lead, CreateTaskRequest{
		Subject: "压上去的", WriteScopes: []string{"feature/agentteam/mailbox.go"},
	})
	if len(overlapping.ScopeWarnings) != 1 {
		t.Fatalf("该有一句提醒，得到 %v", overlapping.ScopeWarnings)
	}
	if !strings.Contains(overlapping.ScopeWarnings[0], string(running.ID)) {
		t.Fatalf("提醒里该点出撞的是 %q，得到 %q", running.ID, overlapping.ScopeWarnings[0])
	}
}

func TestTaskView没人在做的范围不算撞(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	box.createTask(t, lead, CreateTaskRequest{Subject: "还没人做", WriteScopes: []string{"docs"}})

	another := box.createTask(t, lead, CreateTaskRequest{Subject: "另一条", WriteScopes: []string{"docs"}})
	if len(another.ScopeWarnings) != 0 {
		t.Fatalf("待办的任务说不上跟谁抢，得到 %v", another.ScopeWarnings)
	}
}

func TestTaskView兄弟目录不算压在一起(t *testing.T) {
	box := boot(t)
	lead := box.leader(t)
	running := box.createTask(t, lead, CreateTaskRequest{Subject: "正在做的", WriteScopes: []string{"src/api"}})
	box.update(t, lead, UpdateTaskRequest{TaskID: running.ID, ExpectedRev: 1, Action: ActionClaim})

	sibling := box.createTask(t, lead, CreateTaskRequest{Subject: "隔壁", WriteScopes: []string{"src/app"}})
	if len(sibling.ScopeWarnings) != 0 {
		t.Fatalf("src/app 和 src/api 是兄弟，不该提醒，得到 %v", sibling.ScopeWarnings)
	}
}

func TestValidateGraph只校验现有这批时不塞候选(t *testing.T) {
	tasks := []Task{
		{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-2"}},
		{ID: "task-2", Status: TaskPending},
	}
	if err := ValidateGraph(tasks, Task{}); err != nil {
		t.Fatalf("这张图是好的，不该拒：%v", err)
	}
}

func TestValidateGraph删掉的任务不参与(t *testing.T) {
	tasks := []Task{
		{ID: "task-1", Status: TaskDeleted, BlockedBy: []TaskID{"task-99"}},
	}
	if err := ValidateGraph(tasks, Task{}); err != nil {
		t.Fatalf("删掉的那条不该被校验：%v", err)
	}
}

func TestBlockersCleared只看前置不看自己(t *testing.T) {
	byID := map[TaskID]Task{"task-1": {ID: "task-1", Status: TaskCompleted}}
	task := Task{ID: "task-2", Status: TaskDeleted, BlockedBy: []TaskID{"task-1"}}
	if !BlockersCleared(task, byID) {
		t.Fatal("前置做完了就算清了，这条自己什么状态不归它管")
	}
}
