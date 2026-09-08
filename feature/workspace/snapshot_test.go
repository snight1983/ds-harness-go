// 本文件验 [Workspace.Snapshot]：它交出来的六个字段和逐字段读的一致，
// 而且是同一个时刻的——别的写手插在中间时，读到的仍然是完整的一份，不是两半拼的。

package workspace

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestSnapshot交出来的和逐字段读的一样(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.persistence.set(header("s1", created.ID(), 100), header("s2", created.ID(), 200))
	if err := created.AttachSession(ctx, "s1"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if err := created.AttachSession(ctx, "s2"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if err := created.SetTitle(ctx, "改过的标题"); err != nil {
		t.Fatalf("改标题不该失败：%v", err)
	}

	snapshot, err := created.Snapshot(ctx)
	if err != nil {
		t.Fatalf("读快照不该失败：%v", err)
	}
	if snapshot.ID != created.ID() {
		t.Errorf("id 对不上：%q", snapshot.ID)
	}
	if snapshot.TargetKey != mustTargetKey(t, ctx, created) {
		t.Errorf("目标身份对不上：%q", snapshot.TargetKey)
	}
	if snapshot.Path != mustPath(t, ctx, created) {
		t.Errorf("展示路径对不上：%q", snapshot.Path)
	}
	if snapshot.Title != mustTitle(t, ctx, created) {
		t.Errorf("标题对不上：%q", snapshot.Title)
	}
	if !slices.Equal(snapshot.SessionIDs, mustSessionIDs(t, ctx, created)) {
		t.Errorf("账目对不上：%v", snapshot.SessionIDs)
	}
	if !snapshot.CreatedAt.Equal(mustCreatedAt(t, ctx, created)) {
		t.Errorf("创建时刻对不上：%v", snapshot.CreatedAt)
	}
	if !snapshot.UpdatedAt.Equal(mustUpdatedAt(t, ctx, created)) {
		t.Errorf("更新时刻对不上：%v", snapshot.UpdatedAt)
	}
}

// TestSnapshot的账目是一份新切片 验改它碰不到下一次读。
func TestSnapshot的账目是一份新切片(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.persistence.set(header("s1", created.ID(), 100))
	if err := created.AttachSession(ctx, "s1"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}

	snapshot, err := created.Snapshot(ctx)
	if err != nil {
		t.Fatalf("读快照不该失败：%v", err)
	}
	snapshot.SessionIDs[0] = "改坏的"

	again, err := created.Snapshot(ctx)
	if err != nil {
		t.Fatalf("读快照不该失败：%v", err)
	}
	if !slices.Equal(again.SessionIDs, []sessionlog.SessionID{"s1"}) {
		t.Errorf("上一份被改动波及到了下一份：%v", again.SessionIDs)
	}
}

// TestSnapshot读不到记录时把错交出来 验记录被删掉之后快照不会静悄悄交一份零值。
func TestSnapshot读不到记录时把错交出来(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	if _, err := registry.Delete(ctx, created.ID()); err != nil {
		t.Fatalf("删工作区不该失败：%v", err)
	}

	if _, err := created.Snapshot(ctx); !errors.Is(err, CodeWorkspaceGone) {
		t.Fatalf("要的是 CodeWorkspaceGone，拿到 %v", err)
	}
}

// TestSnapshot在并发写下不出撕裂 是这个方法存在的全部理由。
//
// 写手按顺序走两步：先换标题，再挂一个会话。于是介质上依次出现三种记录，
// 每一种都是自洽的：
//
//	{一、无会话} → {二、无会话} → {二、有一个会话}
//
// 「一」配上「有一个会话」在介质上从没出现过。逐字段读能读出它——先读标题
// （拿到「一」），两步写在这中间跑完，再读账目（拿到那个会话）。
// [Workspace.Snapshot] 只发一次读，所以永远读不出这一种。
func TestSnapshot在并发写下不出撕裂(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	if err := created.SetTitle(ctx, "一"); err != nil {
		t.Fatalf("改标题不该失败：%v", err)
	}
	h.persistence.set(header("s1", created.ID(), 100))

	const rounds = 200
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for round := 0; round < rounds; round++ {
			if err := created.SetTitle(ctx, "二"); err != nil {
				t.Errorf("改标题不该失败：%v", err)
				return
			}
			if err := created.AttachSession(ctx, "s1"); err != nil {
				t.Errorf("挂载不该失败：%v", err)
				return
			}
			// 退回起点，好让下一轮重新走一遍那两步。
			if err := created.DetachSession(ctx, "s1"); err != nil {
				t.Errorf("摘除不该失败：%v", err)
				return
			}
			if err := created.SetTitle(ctx, "一"); err != nil {
				t.Errorf("改标题不该失败：%v", err)
				return
			}
		}
	}()

	group.Add(1)
	go func() {
		defer group.Done()
		for round := 0; round < rounds*4; round++ {
			snapshot, err := created.Snapshot(ctx)
			if err != nil {
				t.Errorf("读快照不该失败：%v", err)
				return
			}
			if snapshot.Title == "一" && len(snapshot.SessionIDs) != 0 {
				t.Errorf("读到了一份介质上没有过的记录：标题 %q 配着账目 %v",
					snapshot.Title, snapshot.SessionIDs)
				return
			}
		}
	}()
	group.Wait()
}
