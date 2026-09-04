// 本文件的作用：把这个包落到线上的那几份字节钉死——快照和改动排出去长什么样、
// 那两段人写的自由文本为什么一个转义都不许多，以及那份消息来源怎么骑在
// [github.com/snight1983/ds-harness-go/llm.UnknownSource] 上来回走。
//
// # 这些测试防的是什么错
//
//   - **给模型看的那句话被 HTML 转义改了字节**。Go 的 [encoding/json.Marshal]
//     默认把 `<` 转成 `<`，DSH 的 JSON.stringify 不转。objective 和
//     blockedReason.message 都是人写的自由文本，两边说出来必须是同一句话。
//   - **排出一份自己都读不回来的改动**。[Change] 是导出的，调用方硬填一个别的
//     动词做得到；那份字节会落进日志，然后在下一次回放时才炸——那时候已经改不掉了。
//     所以拒收必须发生在排字节这一刻。
//   - **阶段和阻塞原因对不上就落了盘**。「恰好 blocked 才带 blockedReason」这条
//     不变量在读的那一侧有 [decodeSnapshot] 守着，写的那一侧就是这里。
//   - **字段名和线上的键悄悄对不齐**。[Snapshot] 的字段一个标签都没有（Ref 还是
//     内嵌的），少了那对自定义编解码就只能靠大小写不敏感的名字匹配去碰。

package goal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestEventTypesNamesJustThisPackagesEvent(t *testing.T) {
	types := EventTypes()
	if len(types) != 1 || types[0] != EventChange {
		t.Fatalf("本包往词汇表里加的是 %v，本该只有 %q", types, EventChange)
	}
}

// TestChangeMarshalsWithoutHTMLEscaping 走的是**生产上那条路**：[Service.commit]
// 直接叫 [Change.MarshalJSON]，而不是把它交给 [encoding/json.Marshal]。
//
// 这个区别是承重的。[encoding/json.Marshal] 会把一个 Marshaler 交回来的字节再压
// 一遍，而且照样转义；只有从最外面那层 Encoder 就把 SetEscapeHTML(false) 打开，
// 里面每一层自定义 MarshalJSON 排出来的字节才留得住。所以本包写日志时一次都不许
// 绕道 json.Marshal——这条用例就是那句话的守卫。
func TestChangeMarshalsWithoutHTMLEscaping(t *testing.T) {
	objective := "把 <a href> & </script> 这几个字原样留住"
	change := Change{
		Version:   ChangeVersion,
		Operation: OpCreate,
		Goal: Snapshot{
			Ref:           Ref{ID: "goal-1", Revision: 1},
			Objective:     objective,
			Phase:         PhaseActive,
			MaxGoalRounds: 3,
		},
		CreatedAt: 10, UpdatedAt: 10,
	}
	encoded, err := change.MarshalJSON()
	if err != nil {
		t.Fatalf("排改动失败：%v", err)
	}
	// 转义之后那三个字符会各自变成一段 6 字符的 Unicode 写法；这里按它们不带
	// 反斜杠的那一截找，免得断言本身还要为一层反斜杠转义费神。
	for _, escaped := range []string{"u003c", "u003e", "u0026"} {
		if strings.Contains(string(encoded), escaped) {
			t.Fatalf("这份字节被 HTML 转义改过了（出现了 %s）：%s", escaped, encoded)
		}
	}
	if !strings.Contains(string(encoded), objective) {
		t.Fatalf("那句话没原样留住：%s", encoded)
	}
}

func TestSnapshotRoundTripsThroughItsWireShape(t *testing.T) {
	original := Snapshot{
		Ref:           Ref{ID: "goal-1", Revision: 2},
		Objective:     "写完这一段",
		Phase:         PhaseBlocked,
		BlockedReason: &BlockReason{Code: "provider-quota", Message: "额度用完了"},
		MaxGoalRounds: 3,
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排快照失败：%v", err)
	}
	var back Snapshot
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("读快照失败：%v", err)
	}
	if back.Ref != original.Ref || back.Objective != original.Objective ||
		back.Phase != original.Phase || back.MaxGoalRounds != original.MaxGoalRounds {
		t.Fatalf("读回来的是 %+v，本该是 %+v", back, original)
	}
	if back.BlockedReason == nil || *back.BlockedReason != *original.BlockedReason {
		t.Fatalf("读回来的阻塞原因是 %+v", back.BlockedReason)
	}
}

func TestSnapshotOmitsBlockedReasonWhenItIsNotBlocked(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{
		Ref: Ref{ID: "goal-1", Revision: 1}, Objective: "写完", Phase: PhaseActive, MaxGoalRounds: 3,
	})
	if err != nil {
		t.Fatalf("排快照失败：%v", err)
	}
	if strings.Contains(string(encoded), "blockedReason") {
		t.Fatalf("不是 blocked 的快照本该整个键都不出现：%s", encoded)
	}
}

func TestSnapshotRefusesToMarshalAContradiction(t *testing.T) {
	cases := map[string]Snapshot{
		"blocked 却没带阻塞原因": {
			Ref: Ref{ID: "goal-1", Revision: 1}, Objective: "写完",
			Phase: PhaseBlocked, MaxGoalRounds: 3,
		},
		"不是 blocked 却带了阻塞原因": {
			Ref: Ref{ID: "goal-1", Revision: 1}, Objective: "写完",
			Phase: PhaseActive, MaxGoalRounds: 3,
			BlockedReason: &BlockReason{Code: "x", Message: "y"},
		},
	}
	for name, snapshot := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(snapshot); err == nil {
				t.Fatalf("%s 本该在排字节这一刻就被拒", name)
			}
		})
	}
}

func TestSnapshotUnmarshalRejectsANonObject(t *testing.T) {
	var snapshot Snapshot
	if err := snapshot.UnmarshalJSON([]byte(`[]`)); err == nil {
		t.Fatal("一份不是对象的快照本该读不回来")
	}
}

func TestChangeMarshalsBothShapesAndRefusesAnythingElse(t *testing.T) {
	snapshotChange := Change{
		Version:   ChangeVersion,
		Operation: OpCreate,
		Goal: Snapshot{
			Ref: Ref{ID: "goal-1", Revision: 1}, Objective: "写完",
			Phase: PhaseActive, MaxGoalRounds: 3,
		},
		CreatedAt: 10, UpdatedAt: 10,
	}
	encoded, err := json.Marshal(snapshotChange)
	if err != nil {
		t.Fatalf("排快照改动失败：%v", err)
	}
	decoded, err := DecodeChange(encoded)
	if err != nil {
		t.Fatalf("本包排出去的字节本包自己得读得回来：%v（原文 %s）", err, encoded)
	}
	if decoded.Operation != OpCreate || decoded.Goal.ID != "goal-1" {
		t.Fatalf("读回来的是 %+v", decoded)
	}

	clearChange := Change{
		Version:   ChangeVersion,
		Operation: OpClear,
		Cleared:   Ref{ID: "goal-1", Revision: 2},
		ClearedAt: 20,
	}
	encoded, err = json.Marshal(clearChange)
	if err != nil {
		t.Fatalf("排墓碑改动失败：%v", err)
	}
	decoded, err = DecodeChange(encoded)
	if err != nil {
		t.Fatalf("墓碑改动本包自己得读得回来：%v（原文 %s）", err, encoded)
	}
	if decoded.Operation != OpClear || decoded.Cleared.Revision != 2 || decoded.ClearedAt != 20 {
		t.Fatalf("读回来的是 %+v", decoded)
	}

	if _, err := json.Marshal(Change{Version: ChangeVersion, Operation: Operation("nope")}); err == nil {
		t.Fatal("一个认不得的动词本该在排字节这一刻就被拒")
	}
	if _, err := json.Marshal(Change{
		Version: ChangeVersion, Operation: OpCreate,
		Goal: Snapshot{Ref: Ref{ID: "goal-1", Revision: 1}, Objective: "写完", Phase: PhaseBlocked},
	}); err == nil {
		t.Fatal("一份自相矛盾的快照本该把整次排字节带下水")
	}
}

func TestSourceRidesOnAnUnknownMessageSource(t *testing.T) {
	source := Source{GoalID: "goal-1", Revision: 2, Round: 3}
	carried, err := source.MessageSource()
	if err != nil {
		t.Fatalf("包来源失败：%v", err)
	}
	if carried.SourceKind() != llm.SourceKind(SourceKind) {
		t.Fatalf("包出来的类别是 %q，本该是 %q", carried.SourceKind(), SourceKind)
	}
	unknown, ok := carried.(llm.UnknownSource)
	if !ok {
		t.Fatalf("包出来的是 %T，本该是 llm.UnknownSource", carried)
	}
	var back Source
	if err := json.Unmarshal(unknown.Raw, &back); err != nil {
		t.Fatalf("读来源失败：%v", err)
	}
	if back != source {
		t.Fatalf("读回来的是 %+v，本该是 %+v", back, source)
	}
}

func TestSourceUnmarshalRejectsForeignBytes(t *testing.T) {
	cases := map[string]string{
		"不是一个对象":  `[]`,
		"kind 不对": `{"kind":"schedule","goalId":"goal-1","revision":1,"round":1}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var source Source
			if err := source.UnmarshalJSON([]byte(raw)); err == nil {
				t.Fatalf("%s 本该读不回来", name)
			}
		})
	}
}

func TestErrorsCarryTheirCodeAndSentence(t *testing.T) {
	failure := newError(CodeNotFound, "no current goal")
	if failure.Code != CodeNotFound || failure.Error() != "no current goal" {
		t.Fatalf("造出来的是 %+v", failure)
	}
	broken := foldErrorf("日志坏在 %s 上", "第三条")
	if broken.Error() != "日志坏在 第三条 上" {
		t.Fatalf("造出来的是 %q", broken.Error())
	}
}
