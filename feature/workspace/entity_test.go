// 本文件的作用：一个工作区实体自己的那些契约——标题、会话的挂载与摘除、
// 账目里的排序、归属投影怎么筛，以及目录还在不在。
//
// 这里的用例大半只在一次打开之内跑：实体的写路径全部落在域的写链上，
// 观察点是「这一次写落下去之后，介质上那条记录变成了什么样」。
//
// 新增: 这句话原来是「快照和落盘的记录各是什么样」——那时实体自己攥着一份记录，
// 于是「快照换没换」和「介质换没换」是两件可以分开看的事。权威搬回介质之后
// （见 storage/domain 的 domain.go 开头）实体手里只剩一个 id，读什么都是现取，
// 那两件事塌成了同一件。

package workspace

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// mustWorkspace 建一个工作区并把它当实体用，实体这一层的用例都从这里起步。
func mustWorkspace(t *testing.T, ctx context.Context, registry *Registry, path string) Workspace {
	t.Helper()
	return mustCreate(t, ctx, registry, path, "")
}

// -- 标题 ------------------------------------------------------------------

func TestSetTitle改标题并落盘(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")

	if err := created.SetTitle(ctx, "新名字"); err != nil {
		t.Fatalf("改标题不该失败：%v", err)
	}
	if mustTitle(t, ctx, created) != "新名字" {
		t.Fatalf("标题该是「新名字」，拿到 %q", mustTitle(t, ctx, created))
	}

	registry = h.reopen(ctx)
	if got := mustTitle(t, ctx, mustList(t, ctx, registry)[0]); got != "新名字" {
		t.Fatalf("标题该落盘，重开之后拿到 %q", got)
	}
}

func TestSetTitle改成同一个名字是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	before := mustUpdatedAt(t, ctx, created)

	if err := created.SetTitle(ctx, mustTitle(t, ctx, created)); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	// 一次真正的空操作既不重写介质，也不推进 UpdatedAt。
	if !mustUpdatedAt(t, ctx, created).Equal(before) {
		t.Fatalf("空操作不该推进 UpdatedAt，从 %v 变成了 %v", before, mustUpdatedAt(t, ctx, created))
	}
}

func TestSetTitle落盘失败时报存储失败且不改介质(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })

	if err := created.SetTitle(ctx, "新名字"); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if mustTitle(t, ctx, created) == "新名字" {
		t.Fatal("落盘没成功，介质上那条记录就不该换")
	}
}

func TestSetTitle登记册关掉之后报没启动(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	if err := registry.Close(ctx); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}

	if err := created.SetTitle(ctx, "新名字"); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
	}
}

// -- 挂载 ------------------------------------------------------------------

func TestAttachSession挂上来的排在账目最前面(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/b")
	h.persistence.set(
		header("s3", created.ID(), 300),
		header("s4", created.ID(), 400),
	)

	if err := created.AttachSession(ctx, "s3"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if err := created.AttachSession(ctx, "s4"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s4", "s3"}) {
		t.Fatalf("后挂上来的该在前面，拿到 %v", got)
	}

	// 已经在账目里的再挂一次是空操作，也不重排。
	before := mustUpdatedAt(t, ctx, created)
	if err := created.AttachSession(ctx, "s3"); err != nil {
		t.Fatalf("重复挂载不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s4", "s3"}) {
		t.Fatalf("重复挂载不该重排，拿到 %v", got)
	}
	if !mustUpdatedAt(t, ctx, created).Equal(before) {
		t.Fatal("重复挂载不该推进 UpdatedAt")
	}
}

func TestAttachSession认活会话表里的会话头(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	// 一个刚建出来还没落盘的会话只在活会话表里看得见。
	h.live.add(header("刚建的", created.ID(), 100))

	if err := created.AttachSession(ctx, "刚建的"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"刚建的"}) {
		t.Fatalf("拿到 %v", got)
	}
}

func TestAttachSession工作区对得上的会话挂得上来(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.persistence.set(header("对得上的", created.ID(), 100))

	// 这条用例是整件事的靶心。会话头原来记的是一条**宿主机**路径，而这里验它用的
	// [fs.FileSystem] 唯一的生产后端是一个对象键空间：两个宇宙，于是这一格在生产上
	// 无论如何都走不通，永远是 [CodeAttachRejected]。归属改由工作区标识判之后，
	// 「它说它属于这里」和「这里认它」是同一件事，这一格才第一次成立。
	if err := created.AttachSession(ctx, "对得上的"); err != nil {
		t.Fatalf("工作区对得上就该挂得上来，拿到 %v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"对得上的"}) {
		t.Fatalf("拿到 %v", got)
	}
}

func TestAttachSession归属对不上时分类到位(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	other := mustWorkspace(t, ctx, registry, "/b")
	h.persistence.set(
		header("没点名工作区", "", 10),
		header("点名了别人", other.ID(), 20),
	)

	cases := []struct {
		sessionID sessionlog.SessionID
		code      Code
	}{
		{"没点名工作区", CodeAttachRejected},
		{"点名了别人", CodeAttachRejected},
		{"根本没这个会话", CodeUnknownSession},
	}
	for _, one := range cases {
		t.Run(string(one.sessionID), func(t *testing.T) {
			if err := created.AttachSession(ctx, one.sessionID); !errors.Is(err, one.code) {
				t.Fatalf("要的是 %s，拿到 %v", one.code, err)
			}
		})
	}
	if got := mustSessionIDs(t, ctx, created); len(got) != 0 {
		t.Fatalf("一个都不该挂上来，拿到 %v", got)
	}
}

func TestAttachSession列举失败时报存储失败而不是未知会话(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.persistence.fail(errBackend)

	err := created.AttachSession(ctx, "谁知道呢")
	if !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if errors.Is(err, CodeUnknownSession) {
		t.Fatal("后端故障不许塌成 CodeUnknownSession")
	}
}

// -- 账目里的排序 ----------------------------------------------------------

func TestInsertSessionBefore按锚点挪位(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(
		header("s1", ws, 100),
		header("s2", ws, 200),
		header("s3", ws, 300),
	)
	registry := h.open(ctx)
	// bootstrap 收编出来的账目是 s3、s2、s1。
	created := mustGet(t, ctx, registry, ws)

	if err := created.InsertSessionBefore(ctx, "s1", "s3"); err != nil {
		t.Fatalf("挪位不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s1", "s3", "s2"}) {
		t.Fatalf("拿到 %v", got)
	}

	// 锚点为空串是「挪到末尾」。
	if err := created.InsertSessionBefore(ctx, "s1", ""); err != nil {
		t.Fatalf("挪到末尾不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s3", "s2", "s1"}) {
		t.Fatalf("拿到 %v", got)
	}
}

func TestInsertSessionBefore挪到原位是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100), header("s2", ws, 200))
	registry := h.open(ctx)
	created := mustGet(t, ctx, registry, ws)
	before := mustUpdatedAt(t, ctx, created)

	// 锚点就是自己。
	if err := created.InsertSessionBefore(ctx, "s2", "s2"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	// 挪到本来就在的位置。
	if err := created.InsertSessionBefore(ctx, "s2", "s1"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s2", "s1"}) {
		t.Fatalf("拿到 %v", got)
	}
	if !mustUpdatedAt(t, ctx, created).Equal(before) {
		t.Fatal("空操作不该推进 UpdatedAt")
	}
}

func TestInsertSessionBefore点名账目外的会话时不写(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100))
	registry := h.open(ctx)
	created := mustGet(t, ctx, registry, ws)

	if err := created.InsertSessionBefore(ctx, "不在账目里", ""); !errors.Is(err, CodeMoveInvalid) {
		t.Fatalf("要的是 CodeMoveInvalid，拿到 %v", err)
	}
	if err := created.InsertSessionBefore(ctx, "s1", "不在账目里"); !errors.Is(err, CodeMoveInvalid) {
		t.Fatalf("锚点不在账目里也该报 CodeMoveInvalid，拿到 %v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s1"}) {
		t.Fatalf("失败的挪位不该改账目，拿到 %v", got)
	}
}

// -- 摘除 ------------------------------------------------------------------

func TestDetachSession摘掉并落盘且不在账目里的是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100), header("s2", ws, 200))
	registry := h.open(ctx)
	created := mustGet(t, ctx, registry, ws)

	if err := created.DetachSession(ctx, "s1"); err != nil {
		t.Fatalf("摘除不该失败：%v", err)
	}
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s2"}) {
		t.Fatalf("拿到 %v", got)
	}

	before := mustUpdatedAt(t, ctx, created)
	if err := created.DetachSession(ctx, "s1"); err != nil {
		t.Fatalf("摘一个不在账目里的该是空操作，拿到 %v", err)
	}
	if !mustUpdatedAt(t, ctx, created).Equal(before) {
		t.Fatal("空操作不该推进 UpdatedAt")
	}

	registry = h.reopen(ctx)
	if got := mustSessionIDs(t, ctx, mustGet(t, ctx, registry, ws)); !slices.Equal(got, []sessionlog.SessionID{"s2"}) {
		t.Fatalf("摘除该落盘，重开之后拿到 %v", got)
	}
}

// -- 归属投影 --------------------------------------------------------------

func TestSessionIDs筛掉归属对不上的候选并在下一次真实写入时裁掉(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/另一处")
	ids := h.seedWorkspaces(ctx, "/a", "/另一处")
	here, elsewhere := ids[0], ids[1]
	h.persistence.set(header("s1", here, 100), header("s2", here, 200))
	registry := h.open(ctx)
	if got := mustSessionIDs(t, ctx, mustGet(t, ctx, registry, here)); !slices.Equal(got, []sessionlog.SessionID{"s2", "s1"}) {
		t.Fatalf("前置条件不成立，拿到 %v", got)
	}

	// s2 改挂到了别的工作区名下：账目还记着它，会话头已经不认这里了。
	h.persistence.set(header("s1", here, 100), header("s2", elsewhere, 200))
	registry = h.reopen(ctx)
	created := mustGet(t, ctx, registry, here)

	// 读投影当场就把它筛掉了……
	if got := mustSessionIDs(t, ctx, created); !slices.Equal(got, []sessionlog.SessionID{"s1"}) {
		t.Fatalf("拿到 %v", got)
	}
	// ……但落盘的账目还留着，这是「不抹数据」那条。
	if got := mustRecord(t, ctx, registry, created.ID()).SessionIDs; !slices.Equal(got, []sessionlog.SessionID{"s2", "s1"}) {
		t.Fatalf("读操作不该抹数据，拿到 %v", got)
	}

	// 下一次真实写入才把它裁掉——读和写用的是同一条判据。
	if err := created.SetTitle(ctx, "换个名字"); err != nil {
		t.Fatalf("改标题不该失败：%v", err)
	}
	if got := mustRecord(t, ctx, registry, created.ID()).SessionIDs; !slices.Equal(got, []sessionlog.SessionID{"s1"}) {
		t.Fatalf("一次真实写入该把筛掉的候选裁掉，拿到 %v", got)
	}
}

func TestSessionIDs候选全被裁掉时那一次写照样落下去(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100))
	registry := h.open(ctx)
	// 拿一遍是为了让这个工作区先在账目上落下来；下面重开之后再取一次新的句柄。
	mustGet(t, ctx, registry, ws)

	// 会话头整个不见了：候选筛不出来，但账目本身没被任何 fn 改过。
	h.persistence.set()
	registry = h.reopen(ctx)
	created := mustGet(t, ctx, registry, ws)
	before := mustUpdatedAt(t, ctx, created)

	// fn 说「我什么都没改」，可裁剪有所斩获，所以这一格不是空操作。
	if err := created.DetachSession(ctx, "不在账目里"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got := mustRecord(t, ctx, registry, created.ID()).SessionIDs; len(got) != 0 {
		t.Fatalf("裁剪该落盘，拿到 %v", got)
	}
	if !mustUpdatedAt(t, ctx, created).After(before) {
		t.Fatal("裁剪是一次真实写入，该推进 UpdatedAt")
	}
}

// -- 目录还在不在 ----------------------------------------------------------

func TestStatus按目录还在不在报状态(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.filesystem.addDir("/c")
	registry := h.open(ctx)
	alive := mustWorkspace(t, ctx, registry, "/a")
	moved := mustWorkspace(t, ctx, registry, "/b")
	broken := mustWorkspace(t, ctx, registry, "/c")

	if got := mustStatus(t, ctx, alive); got != StatusOK {
		t.Fatalf("目录还在，该是 %q，拿到 %q", StatusOK, got)
	}

	h.filesystem.removeDir("/b")
	if got := mustStatus(t, ctx, moved); got != StatusMissingDir {
		t.Fatalf("目录不在了，该是 %q，拿到 %q", StatusMissingDir, got)
	}

	// 后端出故障时报「目录不在」而不是崩掉：这是一个诊断投影，不是一次操作。
	h.filesystem.failStat("/c", errBackend)
	if got := mustStatus(t, ctx, broken); got != StatusMissingDir {
		t.Fatalf("拿到 %q", got)
	}

	// 目录变成一个文件也算不在。
	h.filesystem.removeDir("/a")
	h.filesystem.addFile("/a")
	if got := mustStatus(t, ctx, alive); got != StatusMissingDir {
		t.Fatalf("拿到 %q", got)
	}
}

func TestTargetKey交出记录里那个身份(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")

	target, err := h.filesystem.Resolve(ctx, "/a", "")
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if mustTargetKey(t, ctx, created) != target.TargetKey {
		t.Fatalf("身份该是 %q，拿到 %q", target.TargetKey, mustTargetKey(t, ctx, created))
	}
}

// -- 两个纯函数 ------------------------------------------------------------

func TestDefaultTitle取展示路径最后一段(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/项目", "项目"},
		{"/home/项目/", "项目"},
		{`C:\code\项目`, "项目"},
		{`C:\code\项目\`, "项目"},
		{"单独一段", "单独一段"},
		// 全是分隔符：切不出东西，回落到整条展示路径——空标题读的人什么也看不出来。
		{"/", "/"},
		{`\\`, `\\`},
		{"", ""},
	}
	for _, one := range cases {
		if got := defaultTitle(one.path); got != one.want {
			t.Fatalf("defaultTitle(%q) 该是 %q，拿到 %q", one.path, one.want, got)
		}
	}
}

func TestInsertBefore两条插入函数的语义一致(t *testing.T) {
	sessions := []sessionlog.SessionID{"a", "b", "c"}
	if got := insertBeforeSession(sessions, "c", "a"); !slices.Equal(got, []sessionlog.SessionID{"c", "a", "b"}) {
		t.Fatalf("拿到 %v", got)
	}
	if got := insertBeforeSession(sessions, "a", ""); !slices.Equal(got, []sessionlog.SessionID{"b", "c", "a"}) {
		t.Fatalf("锚点为空串该插到末尾，拿到 %v", got)
	}

	workspaces := []WorkspaceID{"a", "b", "c"}
	if got := insertBeforeWorkspace(workspaces, "c", "a"); !slices.Equal(got, []WorkspaceID{"c", "a", "b"}) {
		t.Fatalf("拿到 %v", got)
	}
	if got := insertBeforeWorkspace(workspaces, "a", ""); !slices.Equal(got, []WorkspaceID{"b", "c", "a"}) {
		t.Fatalf("锚点为空串该插到末尾，拿到 %v", got)
	}
}
