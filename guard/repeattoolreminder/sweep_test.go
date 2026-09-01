// 本文件的作用：验一件从包外看不见的事——一个用完就没人再引用的 agent，
// 不会因为这张链表而留在内存里。
//
// 它必须写在包**内**：这件事唯一的观察点是 chains 这张表的规模，而那是个
// 未导出字段。为它开一个导出的访问器等于为了测试改公开面，代价比多一个
// 内部测试文件大得多。

package repeattoolreminder

import (
	"runtime"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
)

func TestAdvanceSweepsChainsOfDeadAgents(t *testing.T) {
	// 不并行：这条要真的触发 GC。
	reminder, err := New(Config{})
	if err != nil {
		t.Fatalf("造这一层失败：%v", err)
	}

	// 在一个闭包里造这个 agent 并用掉它，好让它一返回就没人引用了。
	func() {
		gone := scope.NewKey("gone")
		reminder.advance(gone, "k")
	}()
	if len(reminder.chains) != 1 {
		t.Fatalf("该先记下一条，拿到 %d 条", len(reminder.chains))
	}

	// 扫是搭在写入上的，所以得一边推活着的那条链一边给 GC 机会。
	// 循环而不是只 GC 一次：弱指针什么时候被清掉由运行时决定，
	// 断言的是「终究会被扫掉」，不是「第一次 GC 就扫掉」。
	live := scope.NewKey("live")
	for i := 0; i < 100; i++ {
		runtime.GC()
		reminder.advance(live, "k")
		if len(reminder.chains) == 1 {
			return
		}
	}
	t.Fatalf("死掉的那条一直没被扫掉，表里还有 %d 条", len(reminder.chains))
}
