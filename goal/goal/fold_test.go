// 本文件的作用：把那条**严格**回放钉在它真会出错的边上——每一道解码闸门、那七个
// 动词的跃迁规矩、id 为什么跨过墓碑也不许重用，以及轮数只由哪一种事件往上推。
//
// # 这些测试防的是什么错
//
//   - **一份宽松的解码放进一条坏改动**。这些字节是**跨实现**的：DSH 那一侧要能
//     原样读回去。少一道键的精确匹配，一条多带了字段的改动就会被默默接受，然后在
//     对面炸掉；少一道引号的拦截，一条 `"revision":"2"` 在对面会被当场拒收。
//   - **一个 id 被建第二次**。清掉一个目标之后拿同一个 id 再建一遍，会让一段日志
//     里同一个身份对应两段互不相干的历史，而所有指向它的来源都再也说不清指的是谁。
//   - **修订号不是恰好加一**。CAS 那条命脉靠的就是这一步：跳号意味着中间有一次
//     改动没被看见，而后写的那一次会把它整个抹掉。
//   - **阶段跃迁被放宽**。edit 碰了阶段、pause 顺手改了描述、resume 在预算用完
//     之后还能推起来——每一条都会让「日志是唯一权威」这句话失效。
//   - **普通的人类回合被算进轮数**。轮数是一道数得出来、赖不掉的预算；把不带
//     [Source] 的消息也算进去，预算就再也拦不住任何东西。

package goal

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ---- 字节模板 ----

// goalJSON 拼一份合规快照的字节。
func goalJSON(revision int, phase string) string {
	return fmt.Sprintf(
		`{"id":"goal-1","revision":%d,"objective":"写完这一段","phase":%q,"maxGoalRounds":3}`,
		revision, phase,
	)
}

// blockedGoalJSON 拼一份带阻塞原因的快照的字节。
func blockedGoalJSON(revision int) string {
	return fmt.Sprintf(
		`{"id":"goal-1","revision":%d,"objective":"写完这一段","phase":"blocked",`+
			`"maxGoalRounds":3,"blockedReason":{"code":"provider-quota","message":"额度用完了"}}`,
		revision,
	)
}

// changeJSON 拼一份非 clear 改动的字节。
func changeJSON(operation, goal string, rounds int, createdAt, updatedAt int64) string {
	return fmt.Sprintf(
		`{"kind":"goal/change","version":1,"operation":%q,"goal":%s,`+
			`"roundsStarted":%d,"createdAt":%d,"updatedAt":%d}`,
		operation, goal, rounds, createdAt, updatedAt,
	)
}

// clearJSON 拼一份墓碑改动的字节。
func clearJSON(revision int, clearedAt int64) string {
	return fmt.Sprintf(
		`{"kind":"goal/change","version":1,"operation":"clear",`+
			`"cleared":{"id":"goal-1","revision":%d},"clearedAt":%d}`,
		revision, clearedAt,
	)
}

// createEvent 是那一大半用例的开场白：一条建目标的改动。
func createEvent() session.Event {
	return changeEvent(changeJSON("create", goalJSON(1, "active"), 0, 10, 10))
}

// mustFold 是那些「这一串一定折得动」的地方用的短路。
func mustFold(t *testing.T, events ...session.Event) Folded {
	t.Helper()
	folded, err := Fold(events)
	if err != nil {
		t.Fatalf("Fold 本该成功，却报了 %v", err)
	}
	return folded
}

// ---- 一整条链 ----

func TestFoldWalksTheWholeLifecycle(t *testing.T) {
	folded := mustFold(t,
		createEvent(),
		userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
		changeEvent(changeJSON("edit", goalJSON(2, "active"), 1, 10, 20)),
		changeEvent(changeJSON("pause", goalJSON(3, "paused"), 1, 10, 30)),
		changeEvent(changeJSON("resume", goalJSON(4, "active"), 1, 10, 40)),
		changeEvent(changeJSON("block", blockedGoalJSON(5), 1, 10, 50)),
		changeEvent(changeJSON("complete", goalJSON(6, "complete"), 1, 10, 60)),
	)
	if folded.Goal == nil {
		t.Fatal("折完之后本该有一个当前目标")
	}
	if folded.Goal.Revision != 6 || folded.Goal.Phase != PhaseComplete {
		t.Fatalf("折出来的是修订 %d 阶段 %q，本该是修订 6 阶段 complete", folded.Goal.Revision, folded.Goal.Phase)
	}
	if folded.Goal.BlockedReason != nil {
		t.Fatal("complete 之后本该没有阻塞原因了")
	}
	if folded.RoundsStarted != 1 {
		t.Fatalf("轮数是 %d，本该是 1", folded.RoundsStarted)
	}
	if folded.CreatedAt != 10 || folded.UpdatedAt != 60 {
		t.Fatalf("时刻是 (%d, %d)，本该是 (10, 60)", folded.CreatedAt, folded.UpdatedAt)
	}
	if folded.LastRef == nil || folded.LastRef.Revision != 6 {
		t.Fatalf("最近一次改动的身份是 %+v，本该是修订 6", folded.LastRef)
	}
}

func TestFoldClearLeavesATombstoneAndZeroesTheCounters(t *testing.T) {
	folded := mustFold(t,
		createEvent(),
		changeEvent(clearJSON(2, 20)),
	)
	if folded.Goal != nil {
		t.Fatalf("清掉之后本该没有当前目标了，却还有 %+v", folded.Goal)
	}
	if folded.RoundsStarted != 0 || folded.CreatedAt != 0 || folded.UpdatedAt != 0 {
		t.Fatalf("清掉之后那几个派生量本该归零，却是 (%d, %d, %d)",
			folded.RoundsStarted, folded.CreatedAt, folded.UpdatedAt)
	}
	if folded.LastRef == nil || folded.LastRef.Revision != 2 {
		t.Fatalf("墓碑是 %+v，本该是修订 2", folded.LastRef)
	}
}

func TestFoldIgnoresUnrelatedEvents(t *testing.T) {
	folded := mustFold(t,
		session.Event{Type: session.EventTurnStart, Data: json.RawMessage(`{}`)},
		createEvent(),
		userEvent(t, nil),
	)
	if folded.Goal == nil || folded.RoundsStarted != 0 {
		t.Fatalf("不相干的事件和普通人类回合都不该动这份状态，折出来的是 %+v", folded)
	}
}

// ---- 解码闸门 ----

func TestDecodeChangeRejectsMalformedPayloads(t *testing.T) {
	cases := map[string]string{
		"负载不是对象":                 `[]`,
		"负载是 null":               `null`,
		"kind 不对":                `{"kind":"goal/other","version":1,"operation":"create"}`,
		"版本号认不得":                 `{"kind":"goal/change","version":2,"operation":"create"}`,
		"版本号带了引号":                `{"kind":"goal/change","version":"1","operation":"create"}`,
		"动词认不得":                  changeJSON("nope", goalJSON(1, "active"), 0, 10, 10),
		"快照改动多带了一个键":             changeJSON("create", goalJSON(1, "active"), 0, 10, 10)[:len(changeJSON("create", goalJSON(1, "active"), 0, 10, 10))-1] + `,"extra":1}`,
		"墓碑改动多带了一个键":             clearJSON(2, 20)[:len(clearJSON(2, 20))-1] + `,"extra":1}`,
		"createdAt 是负的":          changeJSON("create", goalJSON(1, "active"), 0, -1, 10),
		"updatedAt 早于 createdAt": changeJSON("create", goalJSON(1, "active"), 0, 20, 10),
		"roundsStarted 是小数":      `{"kind":"goal/change","version":1,"operation":"create","goal":` + goalJSON(1, "active") + `,"roundsStarted":1.5,"createdAt":10,"updatedAt":10}`,
		"goal 不是对象":              changeJSON("create", `[]`, 0, 10, 10),
		"goal.id 是空的":            changeJSON("create", `{"id":"","revision":1,"objective":"写完","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.objective 是空的":     changeJSON("create", `{"id":"goal-1","revision":1,"objective":"","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.objective 带着首尾空白":  changeJSON("create", `{"id":"goal-1","revision":1,"objective":" 写完 ","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.phase 认不得":         changeJSON("create", `{"id":"goal-1","revision":1,"objective":"写完","phase":"done","maxGoalRounds":3}`, 0, 10, 10),
		"goal.revision 带了引号":     changeJSON("create", `{"id":"goal-1","revision":"1","objective":"写完","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.revision 根本不是数字":   changeJSON("create", `{"id":"goal-1","revision":true,"objective":"写完","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.objective 不是字符串":   changeJSON("create", `{"id":"goal-1","revision":1,"objective":123,"phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"updatedAt 是负的":          changeJSON("create", goalJSON(1, "active"), 0, 0, -1),
		"goal.revision 超出安全整数":   changeJSON("create", `{"id":"goal-1","revision":9007199254740992,"objective":"写完","phase":"active","maxGoalRounds":3}`, 0, 10, 10),
		"goal.maxGoalRounds 是 0": changeJSON("create", `{"id":"goal-1","revision":1,"objective":"写完","phase":"active","maxGoalRounds":0}`, 0, 10, 10),
		"blocked 却没带阻塞原因":        changeJSON("block", goalJSON(2, "blocked"), 0, 10, 20),
		"不是 blocked 却带了阻塞原因":     changeJSON("create", `{"id":"goal-1","revision":1,"objective":"写完","phase":"active","maxGoalRounds":3,"blockedReason":{"code":"x","message":"y"}}`, 0, 10, 10),
		"阻塞原因的键不对":               changeJSON("block", `{"id":"goal-1","revision":2,"objective":"写完","phase":"blocked","maxGoalRounds":3,"blockedReason":{"code":"x"}}`, 0, 10, 20),
		"阻塞码不是 lower-kebab-case": changeJSON("block", `{"id":"goal-1","revision":2,"objective":"写完","phase":"blocked","maxGoalRounds":3,"blockedReason":{"code":"Bad_Code","message":"y"}}`, 0, 10, 20),
		"阻塞原因那句话带着首尾空白":          changeJSON("block", `{"id":"goal-1","revision":2,"objective":"写完","phase":"blocked","maxGoalRounds":3,"blockedReason":{"code":"x","message":" y "}}`, 0, 10, 20),
		"墓碑的键不对":                 `{"kind":"goal/change","version":1,"operation":"clear","cleared":{"id":"goal-1"},"clearedAt":20}`,
		// 键的**个数**对得上、名字却换了一个：只数个数的检查会把它放过去，然后
		// revision 会被当成「没填」读成 0。
		"墓碑的键名换了一个":     `{"kind":"goal/change","version":1,"operation":"clear","cleared":{"id":"goal-1","rev":2},"clearedAt":20}`,
		"墓碑 id 是空的":     `{"kind":"goal/change","version":1,"operation":"clear","cleared":{"id":"","revision":2},"clearedAt":20}`,
		"墓碑修订号是 0":      clearJSON(0, 20),
		"clearedAt 是负的": clearJSON(2, -1),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeChange(json.RawMessage(payload))
			expectFoldError(t, err, name)
		})
	}
}

func TestDecodeChangeAcceptsBothShapes(t *testing.T) {
	change, err := DecodeChange(json.RawMessage(changeJSON("block", blockedGoalJSON(2), 1, 10, 20)))
	if err != nil {
		t.Fatalf("一份合规的阻塞改动本该读得回来：%v", err)
	}
	if change.Goal.BlockedReason == nil || change.Goal.BlockedReason.Code != "provider-quota" {
		t.Fatalf("读回来的阻塞原因是 %+v", change.Goal.BlockedReason)
	}
	if ChangeRef(change) != (Ref{ID: "goal-1", Revision: 2}) {
		t.Fatalf("这次改动的身份是 %+v", ChangeRef(change))
	}

	cleared, err := DecodeChange(json.RawMessage(clearJSON(3, 30)))
	if err != nil {
		t.Fatalf("一份合规的墓碑改动本该读得回来：%v", err)
	}
	if ChangeRef(cleared) != (Ref{ID: "goal-1", Revision: 3}) {
		t.Fatalf("墓碑的身份是 %+v", ChangeRef(cleared))
	}
}

// ---- 跃迁规矩 ----

func TestFoldRejectsBrokenTransitions(t *testing.T) {
	cases := map[string][]session.Event{
		"create 的修订号不是 1": {
			changeEvent(changeJSON("create", goalJSON(2, "active"), 0, 10, 10)),
		},
		"create 的阶段不是 active": {
			changeEvent(changeJSON("create", goalJSON(1, "paused"), 0, 10, 10)),
		},
		"create 自带了轮数": {
			changeEvent(changeJSON("create", goalJSON(1, "active"), 1, 10, 10)),
		},
		"当前目标还没完成就再建一个": {
			createEvent(),
			changeEvent(`{"kind":"goal/change","version":1,"operation":"create","goal":{"id":"goal-2","revision":1,"objective":"另一件","phase":"active","maxGoalRounds":3},"roundsStarted":0,"createdAt":20,"updatedAt":20}`),
		},
		"清掉之后拿同一个 id 再建一遍": {
			createEvent(),
			changeEvent(clearJSON(2, 20)),
			changeEvent(changeJSON("create", goalJSON(1, "active"), 0, 30, 30)),
		},
		"没有当前目标就 pause": {
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)),
		},
		"没有当前目标就 clear": {
			changeEvent(clearJSON(2, 20)),
		},
		"clear 的修订号跳了号": {
			createEvent(),
			changeEvent(clearJSON(3, 20)),
		},
		"clear 的时刻早于当前那次改动": {
			createEvent(),
			changeEvent(clearJSON(2, 5)),
		},
		"修订号跳了号": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(3, "paused"), 0, 10, 20)),
		},
		"换了一个 id": {
			createEvent(),
			changeEvent(`{"kind":"goal/change","version":1,"operation":"pause","goal":{"id":"goal-2","revision":2,"objective":"写完这一段","phase":"paused","maxGoalRounds":3},"roundsStarted":0,"createdAt":10,"updatedAt":20}`),
		},
		"没保住 createdAt": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 11, 20)),
		},
		"updatedAt 往回走了": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 5)),
		},
		"没保住轮数": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 1, 10, 20)),
		},
		"edit 碰了阶段": {
			createEvent(),
			changeEvent(changeJSON("edit", goalJSON(2, "paused"), 0, 10, 20)),
		},
		"edit 碰了阻塞原因": {
			createEvent(),
			changeEvent(changeJSON("block", blockedGoalJSON(2), 0, 10, 20)),
			changeEvent(changeJSON("edit", goalJSON(3, "blocked"), 0, 10, 30)),
		},
		"pause 顺手改了描述": {
			createEvent(),
			changeEvent(changeJSON("pause", `{"id":"goal-1","revision":2,"objective":"换了一句","phase":"paused","maxGoalRounds":3}`, 0, 10, 20)),
		},
		"pause 的阶段跃迁不成立": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)),
			changeEvent(changeJSON("pause", goalJSON(3, "paused"), 0, 10, 30)),
		},
		// 除了 edit，剩下那四个改阶段的动词一律不许碰 objective / maxGoalRounds：
		// 那两样是目标的**定义**，混在一次阶段跃迁里改掉，日志里就再也分不清这次
		// 到底是「停一下」还是「换了一件事」。
		"resume 顺手改了描述": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)),
			changeEvent(changeJSON("resume", `{"id":"goal-1","revision":3,"objective":"换了一句","phase":"active","maxGoalRounds":3}`, 0, 10, 30)),
		},
		"complete 顺手改了轮数上限": {
			createEvent(),
			changeEvent(changeJSON("complete", `{"id":"goal-1","revision":2,"objective":"写完这一段","phase":"complete","maxGoalRounds":9}`, 0, 10, 20)),
		},
		"block 顺手改了描述": {
			createEvent(),
			changeEvent(changeJSON("block", `{"id":"goal-1","revision":2,"objective":"换了一句","phase":"blocked","maxGoalRounds":3,"blockedReason":{"code":"provider-quota","message":"额度用完了"}}`, 0, 10, 20)),
		},
		"resume 的目标阶段不是 active": {
			createEvent(),
			changeEvent(changeJSON("resume", goalJSON(2, "paused"), 0, 10, 20)),
		},
		"resume 时预算已经用完": {
			createEvent(),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 2}),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 3}),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 3, 10, 20)),
			changeEvent(changeJSON("resume", goalJSON(3, "active"), 3, 10, 30)),
		},
		"complete 之后再 complete": {
			createEvent(),
			changeEvent(changeJSON("complete", goalJSON(2, "complete"), 0, 10, 20)),
			changeEvent(changeJSON("complete", goalJSON(3, "complete"), 0, 10, 30)),
		},
		"block 的来源阶段不是 active": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)),
			changeEvent(changeJSON("block", blockedGoalJSON(3), 0, 10, 30)),
		},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Fold(events)
			expectFoldError(t, err, name)
		})
	}
}

func TestFoldAllowsCreateAfterComplete(t *testing.T) {
	folded := mustFold(t,
		createEvent(),
		changeEvent(changeJSON("complete", goalJSON(2, "complete"), 0, 10, 20)),
		changeEvent(`{"kind":"goal/change","version":1,"operation":"create","goal":{"id":"goal-2","revision":1,"objective":"下一件","phase":"active","maxGoalRounds":3},"roundsStarted":0,"createdAt":30,"updatedAt":30}`),
	)
	if folded.Goal == nil || folded.Goal.ID != "goal-2" {
		t.Fatalf("上一个完成了就该建得出下一个，折出来的是 %+v", folded.Goal)
	}
	if folded.CreatedAt != 30 || folded.UpdatedAt != 30 {
		t.Fatalf("新目标的时刻是 (%d, %d)，本该是 (30, 30)", folded.CreatedAt, folded.UpdatedAt)
	}
}

// TestFoldAllowsEditingABlockedGoalThatKeepsItsReason 钉的是「edit 不许碰阻塞原因」
// 那条规矩的**另一半**：原封不动地把它带上就行。
//
// 一个被卡住的目标照样改得动描述——挡的是「借着一次 edit 把阻塞原因换掉或者抹掉」，
// 不是「被卡住的时候什么都不许改」。
func TestFoldAllowsEditingABlockedGoalThatKeepsItsReason(t *testing.T) {
	folded := mustFold(t,
		createEvent(),
		changeEvent(changeJSON("block", blockedGoalJSON(2), 0, 10, 20)),
		changeEvent(changeJSON("edit", `{"id":"goal-1","revision":3,"objective":"换了一句","phase":"blocked","maxGoalRounds":3,"blockedReason":{"code":"provider-quota","message":"额度用完了"}}`, 0, 10, 30)),
	)
	if folded.Goal == nil || folded.Goal.Objective != "换了一句" {
		t.Fatalf("被卡住的目标本该也改得动描述，折出来的是 %+v", folded.Goal)
	}
	if folded.Goal.Phase != PhaseBlocked || folded.Goal.BlockedReason == nil ||
		folded.Goal.BlockedReason.Code != "provider-quota" {
		t.Fatalf("那份阻塞原因本该原样留着，折出来的是 %+v", folded.Goal)
	}
}

func TestApplyChangeRejectsAHandBuiltVerb(t *testing.T) {
	state := EmptyFoldState()
	if err := ApplyEvent(state, createEvent()); err != nil {
		t.Fatalf("建目标本该成功：%v", err)
	}
	// 这条路解码解不出来：调用方自己拼一个 [Change] 才走得到，而 [Change] 是导出的。
	err := ApplyChange(state, Change{
		Version:   ChangeVersion,
		Operation: Operation("nope"),
		Goal: Snapshot{
			Ref: Ref{ID: "goal-1", Revision: 2}, Objective: "写完这一段",
			Phase: PhaseActive, MaxGoalRounds: 3,
		},
		CreatedAt: 10, UpdatedAt: 20,
	})
	expectFoldError(t, err, "一个认不得的快照动词")
}

// ---- 轮数 ----

func TestFoldCountsOnlyGoalDrivenRounds(t *testing.T) {
	folded := mustFold(t,
		createEvent(),
		userEvent(t, nil),
		userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
		userEvent(t, nil),
		userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 2}),
	)
	if folded.RoundsStarted != 2 {
		t.Fatalf("轮数是 %d，本该是 2——只有带目标来源的那两条算数", folded.RoundsStarted)
	}
}

func TestFoldRejectsIllegitimateRounds(t *testing.T) {
	cases := map[string][]session.Event{
		"没有当前目标": {
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
		},
		"目标不是 active": {
			createEvent(),
			changeEvent(changeJSON("pause", goalJSON(2, "paused"), 0, 10, 20)),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 2, Round: 1}),
		},
		"来源指着别的目标": {
			createEvent(),
			userEvent(t, &Source{GoalID: "goal-2", Revision: 1, Round: 1}),
		},
		"来源带的是一个旧修订": {
			createEvent(),
			changeEvent(changeJSON("edit", goalJSON(2, "active"), 0, 10, 20)),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
		},
		"轮号跳了号": {
			createEvent(),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 2}),
		},
		"轮号超出了预算": {
			createEvent(),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 1}),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 2}),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 3}),
			userEvent(t, &Source{GoalID: "goal-1", Revision: 1, Round: 4}),
		},
		"用户消息读不回来": {
			{Type: session.EventUserMessage, Data: json.RawMessage(`[]`)},
		},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Fold(events)
			expectFoldError(t, err, name)
		})
	}
}

func TestFoldRejectsMalformedGoalSources(t *testing.T) {
	cases := map[string]string{
		"goalId 不是字符串": `{"kind":"goal","goalId":123,"revision":1,"round":1}`,
		"goalId 是空的":   `{"kind":"goal","goalId":"","revision":1,"round":1}`,
		"修订号是 0":       `{"kind":"goal","goalId":"goal-1","revision":0,"round":1}`,
		"轮号是 0":        `{"kind":"goal","goalId":"goal-1","revision":1,"round":0}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			data := session.UserMessageData{}
			data.Source = llm.UnknownSource{Kind: llm.SourceKind(SourceKind), Raw: json.RawMessage(raw)}
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatalf("排用户消息失败：%v", err)
			}
			_, err = Fold([]session.Event{
				createEvent(),
				{Type: session.EventUserMessage, Data: encoded},
			})
			expectFoldError(t, err, name)
		})
	}
}

func TestParseSourceIsLenientAboutBrokenSources(t *testing.T) {
	if _, ok := ParseSource(llm.UserSource{}); ok {
		t.Fatal("一个别人的来源本该被当成「不是目标推的」")
	}
	broken := llm.UnknownSource{
		Kind: llm.SourceKind(SourceKind),
		Raw:  json.RawMessage(`{"kind":"goal","goalId":"goal-1","revision":0,"round":1}`),
	}
	if _, ok := ParseSource(broken); ok {
		t.Fatal("一份坏掉的目标来源在这条宽松的路上本该也交回 false")
	}
	good := Source{GoalID: "goal-1", Revision: 2, Round: 3}
	carried, err := good.MessageSource()
	if err != nil {
		t.Fatalf("包来源失败：%v", err)
	}
	parsed, ok := ParseSource(carried)
	if !ok || parsed != good {
		t.Fatalf("读回来的是 %+v（ok=%v），本该是 %+v", parsed, ok, good)
	}
}

// ---- 累加器本身 ----

func TestFoldStateCloneDoesNotShareAnything(t *testing.T) {
	state := EmptyFoldState()
	for _, event := range []session.Event{
		createEvent(),
		changeEvent(changeJSON("block", blockedGoalJSON(2), 0, 10, 20)),
	} {
		if err := ApplyEvent(state, event); err != nil {
			t.Fatalf("折这一段本该成功：%v", err)
		}
	}
	clone := state.Clone()
	clone.Goal.Objective = "被改掉了"
	clone.Goal.BlockedReason.Message = "也被改掉了"
	clone.LastRef.Revision = 99
	clone.seenGoalIDs["goal-9"] = true
	if state.Goal.Objective != "写完这一段" || state.Goal.BlockedReason.Message != "额度用完了" {
		t.Fatalf("原件被克隆件改掉了：%+v", state.Goal)
	}
	if state.LastRef.Revision != 2 {
		t.Fatalf("原件的 LastRef 被改掉了：%+v", state.LastRef)
	}
	if state.seenGoalIDs["goal-9"] {
		t.Fatal("原件那份 id 集合被克隆件改掉了")
	}

	// 脱手出去的那一份同样不许和累加器共享内存。
	folded := state.Folded()
	folded.Goal.BlockedReason.Message = "第三次被改掉"
	if state.Goal.BlockedReason.Message != "额度用完了" {
		t.Fatalf("Folded 交出去的那份和累加器共享了阻塞原因：%+v", state.Goal.BlockedReason)
	}
}

func TestValidateStreamAgreesWithFold(t *testing.T) {
	good := []session.Event{createEvent()}
	if err := ValidateStream(good); err != nil {
		t.Fatalf("一条合规的流本该过：%v", err)
	}
	bad := []session.Event{changeEvent(changeJSON("create", goalJSON(2, "active"), 0, 10, 10))}
	expectFoldError(t, ValidateStream(bad), "一条破规矩的流")
}
