// 本文件的作用：`session/list` 那一页里两件不靠外部世界的判断——两个工作目录算不算
// 同一个，以及「最新的排前面」那条定死的次序。
//
// 源: packages/acp/acp/src/index.ts:316-320, 526-535

package acp

import (
	"os"
	"path/filepath"
	"testing"

	sessionlog "github.com/snight1983/ds-harness-go/session"
)

func TestSameDirectoryAnswersTheSameWhetherOrNotThePathExists(t *testing.T) {
	t.Parallel()

	// 这条是这个函数存在的全部理由。它一度先走 [path/filepath.EvalSymlinks]，
	// 于是同一对字符串在「目录还在」和「目录早删了」两种情况下能给出不同答案——
	// 一份落档半年的会话续不续得上，取决于运维有没有把那个目录留着。会话存档搬进
	// 数据库之后这是错的：这个字段是会话头里的一个标签，不是一次文件系统查询。
	existing := t.TempDir()
	missing := filepath.Join(existing, "早就不在了")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("这个路径本来就不该存在：%v", err)
	}

	for _, path := range []string{existing, missing} {
		if !sameDirectory(path, path) {
			t.Fatalf("一个路径和它自己必须相等，路径存不存在都一样：%s", path)
		}
	}
	if sameDirectory(existing, missing) {
		t.Fatalf("两个不同的路径不该相等")
	}
}

func TestSameDirectoryNormalizesBeforeComparing(t *testing.T) {
	t.Parallel()

	// 规范化那一步用 [path/filepath.Clean]，它是纯字符串函数。DSH 那边这一支用的是
	// path.resolve，两者在「消掉 `.`、并掉重复分隔符、去掉末尾分隔符」上一致。
	sep := string(filepath.Separator)
	base := filepath.Join(sep, "工作区", "项目")

	cases := map[string]string{
		"末尾多一个分隔符": base + sep,
		"中间有个点":    filepath.Join(sep, "工作区", ".", "项目"),
		"重复的分隔符":   sep + "工作区" + sep + sep + "项目",
		"退回上一级再进来": filepath.Join(sep, "工作区", "别的", "..", "项目"),
	}

	for name, variant := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !sameDirectory(base, variant) {
				t.Fatalf("规范化之后该算同一个：%q 对 %q", base, variant)
			}
		})
	}
}

func TestSameDirectoryNeverMatchesAnEmptyLeftSide(t *testing.T) {
	t.Parallel()

	// 空 cwd 谁也匹配不上，**包括另一个空 cwd**：让它们相等，等于所有没有工作目录的
	// 会话互相可见，边界就整个化掉了。
	for name, right := range map[string]string{
		"右边也是空":  "",
		"右边是个路径": filepath.Join(string(filepath.Separator), "工作区"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if sameDirectory("", right) {
				t.Fatalf("左边是空就一律不相等")
			}
		})
	}
}

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
