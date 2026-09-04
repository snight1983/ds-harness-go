// 本文件的作用：`session/list` 那一页里那条定死的次序——最新的排前面。
//
// 源: packages/acp/acp/src/index.ts:526-535
//
// 新增: 这里原先还有三条验 `sameDirectory` 的用例。那个函数把两条路径规范化之后比串，
// 而服务端已经没有工作目录这个概念了：`session/list` 和 `session/resume` 现在比的是
// 两个不透明的工作区标识（见 [sessionlog.SessionHeader.WorkspaceID]），一次相等判定
// 没有需要单独钉住的行为，那三条用例连同函数一起去掉。

package acp

import (
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestSortSessionListPutsTheNewestFirstAndBreaksTiesByID(t *testing.T) {
	t.Parallel()

	// 这条序会进对外协议：一份随枚举顺序漂移的列表，会让同一份数据的两次查询给出
	// 不同的字节，而翻页靠的正是「排在游标后面」这个判据。
	entries := []sessionListEntry{
		{sessionID: sessionlog.SessionID("b"), createdAt: 100},
		{sessionID: sessionlog.SessionID("a"), createdAt: 100},
		{sessionID: sessionlog.SessionID("c"), createdAt: 200},
	}
	sortSessionList(entries)

	want := []sessionlog.SessionID{"c", "a", "b"}
	for index, id := range want {
		if entries[index].sessionID != id {
			t.Fatalf("第 %d 条该是 %s，实际 %s", index, id, entries[index].sessionID)
		}
	}
}
