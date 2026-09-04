// 本文件的作用：那份耐久描述符的测试——两种模式各自的完整声明、快照的脱钩，
// 以及从一段持久日志折回来时的版本与越界字段两道闸。

package subagent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// continuableInput 是一份最小的、成立的可续描述符输入。
func continuableInput() DescriptorData {
	return DescriptorData{Version: DescriptorVersion, Mode: ModeContinuable, Provider: "spawn", Label: "查一下"}
}

func TestEventTypesListsTheDescriptorEvent(t *testing.T) {
	types := EventTypes()
	if len(types) != 1 || types[0] != EventDescriptor {
		t.Fatalf("本包只往日志里写描述符这一种事件，实际 %#v", types)
	}
}

func TestSnapshotDescriptorFillsTheCurrentVersion(t *testing.T) {
	snapshot, err := SnapshotDescriptor(DescriptorData{Mode: ModeOneShot, Provider: "spawn"})
	if err != nil {
		t.Fatalf("拍一次性描述符失败：%v", err)
	}
	if snapshot.Version != DescriptorVersion {
		t.Fatalf("留空的版本该被补成 %d，实际 %d", DescriptorVersion, snapshot.Version)
	}
}

// 快照必须**脱钩**：调用方后来改自己那份输入，改不动已经拍下来的东西。
func TestSnapshotDescriptorDetachesNestedValues(t *testing.T) {
	filter := tools.Restriction{Allow: []string{"read"}}
	input := continuableInput()
	input.ToolFilter = &filter

	snapshot, err := SnapshotDescriptor(input)
	if err != nil {
		t.Fatalf("拍可续描述符失败：%v", err)
	}
	filter.Allow[0] = "write"

	if snapshot.ToolFilter == nil || snapshot.ToolFilter.Allow[0] != "read" {
		t.Fatalf("拍下来的工具范围该和调用方那份脱钩，实际 %#v", snapshot.ToolFilter)
	}
}

func TestSnapshotDescriptorRejectsIncompleteDeclarations(t *testing.T) {
	oneShotWithContinuableFields := DescriptorData{Mode: ModeOneShot, Provider: "spawn", Persona: "海盗"}
	continuableWithoutLabel := DescriptorData{Mode: ModeContinuable, Provider: "spawn"}
	unknownMode := DescriptorData{Mode: "无此模式", Provider: "spawn"}
	withoutProvider := DescriptorData{Mode: ModeOneShot}
	emptyFilter := continuableInput()
	emptyFilter.ToolFilter = &tools.Restriction{}

	for name, input := range map[string]DescriptorData{
		"一次性带了续接组装":  oneShotWithContinuableFields,
		"可续缺了 label": continuableWithoutLabel,
		"模式不认识":      unknownMode,
		"缺提供方名字":     withoutProvider,
		"工具范围两张单子都空": emptyFilter,
	} {
		if _, err := SnapshotDescriptor(input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s 该被拒，实际 %v", name, err)
		}
	}
}

func TestSnapshotDescriptorRejectsAWrongVersion(t *testing.T) {
	input := continuableInput()
	input.Version = DescriptorVersion + 1
	if _, err := SnapshotDescriptor(input); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("别的版本该被拒，实际 %v", err)
	}
}

// descriptorEvent 把一份负载包成一条描述符事件。
func descriptorEvent(t *testing.T, payload any) sessionlog.Event {
	t.Helper()
	return event(t, EventDescriptor, payload)
}

func TestFoldDescriptorReadsTheFirstRecord(t *testing.T) {
	first := continuableInput()
	second := continuableInput()
	second.Label = "后来那条"

	events := []sessionlog.Event{
		event(t, sessionlog.EventTurnStart, sessionlog.TurnStartData{Turn: 1}),
		descriptorEvent(t, first),
		descriptorEvent(t, second),
	}
	folded, found, err := FoldDescriptor(events)
	if err != nil || !found {
		t.Fatalf("该折出一份描述符，实际 found=%v err=%v", found, err)
	}
	if folded.Label != first.Label {
		t.Fatalf("第一条说了算，实际 %q", folded.Label)
	}
}

func TestFoldDescriptorFindsNothingWithoutARecord(t *testing.T) {
	_, found, err := FoldDescriptor(steppedTurn(t, 1, sessionlog.CompletedTurnEnd{}))
	if err != nil || found {
		t.Fatalf("一条都没有时该是「分类不了」，实际 found=%v err=%v", found, err)
	}
}

// 别的版本的记录带着这一版没有的字段是正常的，该被判成「分类不了」而不是坏记录。
func TestFoldDescriptorTreatsOtherVersionsAsUnclassifiable(t *testing.T) {
	payload := map[string]any{"version": DescriptorVersion + 1, "mode": "future", "provider": "spawn", "extra": 1}
	_, found, err := FoldDescriptor([]sessionlog.Event{descriptorEvent(t, payload)})
	if err != nil || found {
		t.Fatalf("别的版本该被判成分类不了，实际 found=%v err=%v", found, err)
	}
}

func TestFoldDescriptorRejectsMalformedRecords(t *testing.T) {
	unreadable := sessionlog.Event{Type: EventDescriptor, Data: json.RawMessage(`{"version":`)}
	if _, _, err := FoldDescriptor([]sessionlog.Event{unreadable}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("读不出来的记录该报错，实际 %v", err)
	}

	noVersion := descriptorEvent(t, map[string]any{"mode": ModeOneShot, "provider": "spawn"})
	if _, _, err := FoldDescriptor([]sessionlog.Event{noVersion}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("不带数字版本的记录该报错，实际 %v", err)
	}
}

// 越界字段由解码器上的 DisallowUnknownFields 挡住，嵌套的工具范围一并管住——
// 那正是 DSH 另外用一张键表守的东西。
func TestFoldDescriptorRejectsUnknownFields(t *testing.T) {
	topLevel := descriptorEvent(t, map[string]any{
		"version": DescriptorVersion, "mode": ModeOneShot, "provider": "spawn", "surprise": true,
	})
	if _, _, err := FoldDescriptor([]sessionlog.Event{topLevel}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("顶层越界字段该被拒，实际 %v", err)
	}

	nested := descriptorEvent(t, map[string]any{
		"version": DescriptorVersion, "mode": ModeContinuable, "provider": "spawn", "label": "查一下",
		"toolFilter": map[string]any{"allow": []string{"read"}, "surprise": true},
	})
	if _, _, err := FoldDescriptor([]sessionlog.Event{nested}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("嵌套越界字段该被拒，实际 %v", err)
	}
}

// 一份**当前版本**的记录和它自称的模式对不上，是坏记录而不是「分类不了」。
func TestFoldDescriptorRejectsIncompleteCurrentVersionRecords(t *testing.T) {
	incomplete := descriptorEvent(t, map[string]any{
		"version": DescriptorVersion, "mode": ModeContinuable, "provider": "spawn",
	})
	if _, _, err := FoldDescriptor([]sessionlog.Event{incomplete}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺 label 的可续记录该报错，实际 %v", err)
	}
}
