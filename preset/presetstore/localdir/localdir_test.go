// 本文件的作用：钉住这份适配层上那几件**换一份实现就会悄悄变掉**的事——
// Stat 顺链而 List 不顺、不存在不是错、戳跟着内容走、覆盖写也收权限。

package localdir_test

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/snight1983/ds-harness-go/preset/presetstore/localdir"
)

// root 造一个空目录，交出这道缝上那种**斜杠分隔**的路径。
//
// [testing.T.TempDir] 在 Windows 上给的是反斜杠，而 [agentpresets.Store] 那条约定
// 是斜杠，所以进包之前先翻一次——这正好也把「本包自己会翻回去」这件事一并试到。
func root(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(t.TempDir())
}

// 不存在报的是「没找到」，不是错。发现要靠这个分得开「这个根还没建」和「这套部署配错了」。
func TestStatAndListTreatAMissingPathAsAbsenceRatherThanFailure(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	missing := path.Join(root(t), "没这个东西")

	entry, found, err := store.Stat(ctx, missing)
	if err != nil {
		t.Fatalf("看一条不存在的路径不该报错：%v", err)
	}
	if found {
		t.Fatalf("不存在却报成找到了：%+v", entry)
	}

	children, listed, err := store.List(ctx, missing)
	if err != nil {
		t.Fatalf("列一个不存在的目录不该报错：%v", err)
	}
	if listed || children != nil {
		t.Fatalf("不存在的目录该报没列到，实际 listed=%v children=%v", listed, children)
	}
}

// 戳跟着内容走：同一份文件不动就不变，改了就变。装载换代整个押在这上面。
func TestStampChangesOnlyWhenTheFileChanges(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	file := path.Join(root(t), "组合.yml")

	if err := store.WriteFile(ctx, file, []byte("- name: alpha\n"), false); err != nil {
		t.Fatalf("写不了：%v", err)
	}
	first, found, err := store.Stat(ctx, file)
	if err != nil || !found {
		t.Fatalf("看不了刚写下的文件：found=%v err=%v", found, err)
	}
	if first.Stamp == "" {
		t.Fatal("一份常规文件在本地介质上必须答得出身份")
	}
	again, _, err := store.Stat(ctx, file)
	if err != nil {
		t.Fatalf("再看一次失败：%v", err)
	}
	if again.Stamp != first.Stamp {
		t.Fatalf("没动过的文件戳却变了：%q -> %q", first.Stamp, again.Stamp)
	}

	// 长度变一变，好让戳在修改时间粒度粗的文件系统上照样分得开。
	if err := store.WriteFile(ctx, file, []byte("- name: alpha\n- name: beta\n"), false); err != nil {
		t.Fatalf("改不了：%v", err)
	}
	edited, _, err := store.Stat(ctx, file)
	if err != nil {
		t.Fatalf("看不了改过的文件：%v", err)
	}
	if edited.Stamp == first.Stamp {
		t.Fatalf("内容变了戳却没变：%q", edited.Stamp)
	}
}

// 目录不带戳：一份预设的身份只由它那份组合文件说了算，目录的修改时间会被一次
// 无关的写顺手动掉。
func TestDirectoriesCarryNoStamp(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	dir := path.Join(root(t), "预设")

	if err := store.MakeDir(ctx, dir); err != nil {
		t.Fatalf("建不了目录：%v", err)
	}
	entry, found, err := store.Stat(ctx, dir)
	if err != nil || !found {
		t.Fatalf("看不了刚建的目录：found=%v err=%v", found, err)
	}
	if !entry.Dir || entry.Regular {
		t.Fatalf("目录该报成目录：%+v", entry)
	}
	if entry.Stamp != "" {
		t.Fatalf("目录不该带戳，实际 %q", entry.Stamp)
	}
}

// 两种符号链接语义，**故意不一样**：Stat 顺链（于是一个指向目录的链接按目录整个
// 复制过去），List 不顺（于是它不算一个预设槽位）。把两者对齐会悄悄改掉其中一头。
func TestStatFollowsLinksWhileListDoesNot(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	base := root(t)
	target := path.Join(base, "实体")
	if err := store.MakeDir(ctx, target); err != nil {
		t.Fatalf("建不了目标目录：%v", err)
	}
	link := path.Join(base, "链接")
	if err := os.Symlink(filepath.FromSlash(target), filepath.FromSlash(link)); err != nil {
		// Windows 上建符号链接要特权，没有就跳过——这条断言守的是语义，不是权限。
		t.Skipf("这台机器建不了符号链接：%v", err)
	}

	entry, found, err := store.Stat(ctx, link)
	if err != nil || !found {
		t.Fatalf("看不了链接：found=%v err=%v", found, err)
	}
	if !entry.Dir {
		t.Fatal("Stat 该顺链，一个指向目录的链接要报成目录")
	}

	children, listed, err := store.List(ctx, base)
	if err != nil || !listed {
		t.Fatalf("列不了根：listed=%v err=%v", listed, err)
	}
	for _, child := range children {
		if child.Name != "链接" {
			continue
		}
		if child.Dir {
			t.Fatal("List 不该顺链，链接这一行说的是它自己")
		}
		return
	}
	t.Fatal("列出来的东西里没有那条链接")
}

// 断链在 Stat 这里如实报成不存在：它确实给不出任何内容。
func TestStatReportsADanglingLinkAsAbsent(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	base := root(t)
	link := path.Join(base, "断链")
	if err := os.Symlink(filepath.FromSlash(path.Join(base, "没这个东西")), filepath.FromSlash(link)); err != nil {
		t.Skipf("这台机器建不了符号链接：%v", err)
	}
	if _, found, err := store.Stat(ctx, link); err != nil || found {
		t.Fatalf("断链该报成不存在：found=%v err=%v", found, err)
	}
}

// 覆盖写也要收权限：os.WriteFile 只在新建时套用那个 mode，少了事后那一下 Chmod，
// 一份从所有人可读的安装复制过来的文件就会带着原来的权限留在可写根里。
func TestWriteFileTightensModeOnOverwrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 权限位")
	}
	store := localdir.New()
	ctx := context.Background()
	file := path.Join(root(t), "宽的")

	if err := os.WriteFile(file, []byte("旧的\n"), 0o644); err != nil {
		t.Fatalf("摆不了那份宽权限的文件：%v", err)
	}
	if err := store.WriteFile(ctx, file, []byte("新的\n"), false); err != nil {
		t.Fatalf("写不了：%v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("看不了：%v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("覆盖写该把权限收成 0600，实际 %o", info.Mode().Perm())
	}
}

// 执行位是这道缝上唯一过得来的权限概念：一份预设可以带可执行的辅助脚本，
// 写下去带着、再看回来还在。
func TestExecutableBitSurvivesTheRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有 Unix 权限位")
	}
	store := localdir.New()
	ctx := context.Background()
	base := root(t)

	script := path.Join(base, "脚本")
	if err := store.WriteFile(ctx, script, []byte("#!/bin/sh\n"), true); err != nil {
		t.Fatalf("写不了脚本：%v", err)
	}
	entry, _, err := store.Stat(ctx, script)
	if err != nil {
		t.Fatalf("看不了脚本：%v", err)
	}
	if !entry.Executable {
		t.Fatal("带执行位写下去的文件该报成可执行")
	}

	plain := path.Join(base, "文本")
	if err := store.WriteFile(ctx, plain, []byte("文本\n"), false); err != nil {
		t.Fatalf("写不了文本：%v", err)
	}
	if entry, _, err := store.Stat(ctx, plain); err != nil || entry.Executable {
		t.Fatalf("不带执行位的文件不该报成可执行：%+v err=%v", entry, err)
	}
}

// 两种删都是幂等的：一次失败的复制会在什么都还没落下的时候撤销，那时撤销必须
// 静静地成功，而不是拿一个「本来就不在」把真正那个错盖掉。
func TestRemoveAndRemoveTreeAreIdempotent(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	base := root(t)
	missing := path.Join(base, "没这个东西")

	if err := store.Remove(ctx, missing); err != nil {
		t.Fatalf("删一个不在的条目不该报错：%v", err)
	}
	if err := store.RemoveTree(ctx, missing); err != nil {
		t.Fatalf("删一棵不在的子树不该报错：%v", err)
	}

	dir := path.Join(base, "预设")
	if err := store.MakeDir(ctx, path.Join(dir, "技能")); err != nil {
		t.Fatalf("建不了嵌套目录：%v", err)
	}
	if err := store.WriteFile(ctx, path.Join(dir, "技能", "一.md"), []byte("x"), false); err != nil {
		t.Fatalf("写不了嵌套文件：%v", err)
	}
	if err := store.RemoveTree(ctx, dir); err != nil {
		t.Fatalf("删不掉那棵树：%v", err)
	}
	if _, found, err := store.Stat(ctx, dir); err != nil || found {
		t.Fatalf("那棵树该没了：found=%v err=%v", found, err)
	}
}

// MakeDir 连上级一起建，而且已经在了不算错——复制一棵树时每一层都会撞上这两件事。
func TestMakeDirCreatesParentsAndToleratesAnExistingDirectory(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	nested := path.Join(root(t), "预设", "技能", "深处")

	if err := store.MakeDir(ctx, nested); err != nil {
		t.Fatalf("建不了嵌套目录：%v", err)
	}
	if err := store.MakeDir(ctx, nested); err != nil {
		t.Fatalf("再建一次已经在的目录不该报错：%v", err)
	}
	entry, found, err := store.Stat(ctx, nested)
	if err != nil || !found || !entry.Dir {
		t.Fatalf("那个嵌套目录该在：%+v found=%v err=%v", entry, found, err)
	}
}

// 列出来的次序按名字升序，而且报的是名字、不是路径——发现照着这个次序拼名册。
func TestListReportsNamesInAscendingOrder(t *testing.T) {
	store := localdir.New()
	ctx := context.Background()
	base := root(t)
	for _, name := range []string{"c", "a", "b"} {
		if err := store.MakeDir(ctx, path.Join(base, name)); err != nil {
			t.Fatalf("建不了 %s：%v", name, err)
		}
	}
	children, listed, err := store.List(ctx, base)
	if err != nil || !listed {
		t.Fatalf("列不了：listed=%v err=%v", listed, err)
	}
	if len(children) != 3 {
		t.Fatalf("该列出 3 个，实际 %d", len(children))
	}
	for index, want := range []string{"a", "b", "c"} {
		if children[index].Name != want {
			t.Fatalf("第 %d 个该是 %q，实际 %q", index, want, children[index].Name)
		}
		if !children[index].Dir {
			t.Fatalf("%q 该报成目录", want)
		}
	}
}
