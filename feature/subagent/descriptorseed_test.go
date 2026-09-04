// 本文件的作用：可续孩子创建种子的测试——描述符那条记录接在继承来的父前缀后面，
// 序号由会话自己盖上，并且折得回来。

package subagent

import (
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestSeedDescriptorTurnAppendsTheRecordToAnInheritedPrefix(t *testing.T) {
	inherited := []sessionlog.Event{
		{Seq: 0, Type: sessionlog.EventTurnStart, Data: data(t, sessionlog.TurnStartData{Turn: 1})},
		{Seq: 1, Type: sessionlog.EventTurnEnd, Data: data(t, sessionlog.TurnEndData{
			Turn: 1, Reason: sessionlog.CompletedTurnEnd{},
		})},
	}
	descriptor := continuableInput()

	seed, err := SeedDescriptorTurn("child", inherited, descriptor)
	if err != nil {
		t.Fatalf("做种失败：%v", err)
	}
	// 继承来的两条、会话自己盖的那条种子边界，再加描述符。
	if len(seed) != len(inherited)+2 {
		t.Fatalf("该是父前缀 + 种子边界 + 描述符，实际 %d 条", len(seed))
	}
	if boundary := seed[len(inherited)]; boundary.Type != sessionlog.EventSessionEndSeed {
		t.Fatalf("父前缀之后该是种子边界，实际 %q", boundary.Type)
	}
	if last := seed[len(seed)-1]; last.Type != EventDescriptor {
		t.Fatalf("描述符该排在最后，实际 %q", last.Type)
	}

	folded, found, err := FoldDescriptor(seed)
	if err != nil || !found {
		t.Fatalf("这份种子该折得出描述符，实际 found=%v err=%v", found, err)
	}
	if folded != descriptor {
		t.Fatalf("折出来的该和写进去的一致，实际 %#v", folded)
	}
}

// 序号由会话自己盖上，从 0 起连续——一次冷恢复读的就是这份日志。
func TestSeedDescriptorTurnStampsContiguousSequenceNumbers(t *testing.T) {
	seed, err := SeedDescriptorTurn("child", nil, continuableInput())
	if err != nil {
		t.Fatalf("做种失败：%v", err)
	}
	for index, event := range seed {
		if event.Seq != index {
			t.Fatalf("第 %d 条的序号该是 %d，实际 %d", index, index, event.Seq)
		}
	}
}

// 描述符那条记录不给模型看：它上不了表面，所以带不上表面操作。
func TestSeedDescriptorTurnKeepsTheRecordOffTheSurface(t *testing.T) {
	seed, err := SeedDescriptorTurn("child", nil, continuableInput())
	if err != nil {
		t.Fatalf("做种失败：%v", err)
	}
	if last := seed[len(seed)-1]; last.SurfaceOp != nil {
		t.Fatalf("描述符不该上表面，实际 %#v", last.SurfaceOp)
	}
}

// 一段立不起来的父前缀在这里就该失败，而不是造出一个日志不成立的孩子。
func TestSeedDescriptorTurnRejectsAMalformedPrefix(t *testing.T) {
	broken := []sessionlog.Event{
		{Seq: 7, Type: sessionlog.EventTurnStart, Data: data(t, sessionlog.TurnStartData{Turn: 1})},
	}
	if _, err := SeedDescriptorTurn("child", broken, continuableInput()); err == nil {
		t.Fatal("序号不连续的前缀该被拒")
	}
}
