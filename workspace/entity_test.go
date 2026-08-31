// 本文件的作用：一个工作区实体自己的那些契约——标题、会话的挂载与摘除、
// 账目里的排序、归属投影怎么筛，以及目录还在不在。
//
// 这里的用例大半只在一次打开之内跑：实体的写路径全部落在域的写链上，
// 观察点是「这一次写落下去之后，快照和落盘的记录各是什么样」。

package workspace

import (
	"context"
	"errors"
	"slices"
	"testing"

	"ds-harness-go/session"
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
	if created.Title() != "新名字" {
		t.Fatalf("标题该是「新名字」，拿到 %q", created.Title())
	}

	registry = h.reopen(ctx)
	if got := mustList(t, registry)[0].Title(); got != "新名字" {
		t.Fatalf("标题该落盘，重开之后拿到 %q", got)
	}
}

func TestSetTitle改成同一个名字是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	before := created.UpdatedAt()

	if err := created.SetTitle(ctx, created.Title()); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	// 一次真正的空操作既不重写介质，也不推进 UpdatedAt。
	if !created.UpdatedAt().Equal(before) {
		t.Fatalf("空操作不该推进 UpdatedAt，从 %v 变成了 %v", before, created.UpdatedAt())
	}
}

func TestSetTitle落盘失败时报存储失败且不改快照(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")
	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })

	if err := created.SetTitle(ctx, "新名字"); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if created.Title() == "新名字" {
		t.Fatal("落盘没成功，内存里的快照就不该换")
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
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100), header("s2", "/a", 200))
	registry := h.open(ctx)
	// 这两条已经被 bootstrap 收编了，另建一个目录来看纯粹的挂载。
	h.filesystem.addDir("/b")
	h.persistence.set(
		header("s1", "/a", 100),
		header("s2", "/a", 200),
		header("s3", "/b", 300),
		header("s4", "/b", 400),
	)
	created := mustWorkspace(t, ctx, registry, "/b")

	if err := created.AttachSession(ctx, "s3"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if err := created.AttachSession(ctx, "s4"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s4", "s3"}) {
		t.Fatalf("后挂上来的该在前面，拿到 %v", got)
	}

	// 已经在账目里的再挂一次是空操作，也不重排。
	before := created.UpdatedAt()
	if err := created.AttachSession(ctx, "s3"); err != nil {
		t.Fatalf("重复挂载不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s4", "s3"}) {
		t.Fatalf("重复挂载不该重排，拿到 %v", got)
	}
	if !created.UpdatedAt().Equal(before) {
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
	h.live.add(header("刚建的", "/a", 100))

	if err := created.AttachSession(ctx, "刚建的"); err != nil {
		t.Fatalf("挂载不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"刚建的"}) {
		t.Fatalf("拿到 %v", got)
	}
}

func TestAttachSession过不了工作目录验证时分类到位(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.filesystem.addFile("/f")
	h.filesystem.failResolve("/bad", errBackend)
	h.filesystem.failStat("/statfail", errBackend)
	h.persistence.set(
		header("没有工作目录", "", 10),
		header("解析不出来", "/bad", 20),
		header("后端出故障", "/statfail", 30),
		header("那是个文件", "/f", 40),
		header("目录不存在", "/nothere", 50),
		header("在别的目录", "/b", 60),
	)
	registry := h.open(ctx)
	created := mustWorkspace(t, ctx, registry, "/a")

	cases := []struct {
		sessionID session.SessionID
		code      Code
	}{
		{"没有工作目录", CodeAttachRejected},
		{"解析不出来", CodeAttachRejected},
		// 后端自己出故障是可以重试的；「解析得出来但不是目录」重试多少次都一样。
		{"后端出故障", CodeStorageFailed},
		{"那是个文件", CodeAttachRejected},
		{"目录不存在", CodeAttachRejected},
		{"在别的目录", CodeAttachRejected},
		{"根本没这个会话", CodeUnknownSession},
	}
	for _, one := range cases {
		t.Run(string(one.sessionID), func(t *testing.T) {
			if err := created.AttachSession(ctx, one.sessionID); !errors.Is(err, one.code) {
				t.Fatalf("要的是 %s，拿到 %v", one.code, err)
			}
		})
	}
	if got := created.SessionIDs(); len(got) != 0 {
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
	h.persistence.set(
		header("s1", "/a", 100),
		header("s2", "/a", 200),
		header("s3", "/a", 300),
	)
	registry := h.open(ctx)
	// bootstrap 收编出来的账目是 s3、s2、s1。
	created := mustList(t, registry)[0]

	if err := created.InsertSessionBefore(ctx, "s1", "s3"); err != nil {
		t.Fatalf("挪位不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s1", "s3", "s2"}) {
		t.Fatalf("拿到 %v", got)
	}

	// 锚点为空串是「挪到末尾」。
	if err := created.InsertSessionBefore(ctx, "s1", ""); err != nil {
		t.Fatalf("挪到末尾不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s3", "s2", "s1"}) {
		t.Fatalf("拿到 %v", got)
	}
}

func TestInsertSessionBefore挪到原位是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100), header("s2", "/a", 200))
	registry := h.open(ctx)
	created := mustList(t, registry)[0]
	before := created.UpdatedAt()

	// 锚点就是自己。
	if err := created.InsertSessionBefore(ctx, "s2", "s2"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	// 挪到本来就在的位置。
	if err := created.InsertSessionBefore(ctx, "s2", "s1"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s2", "s1"}) {
		t.Fatalf("拿到 %v", got)
	}
	if !created.UpdatedAt().Equal(before) {
		t.Fatal("空操作不该推进 UpdatedAt")
	}
}

func TestInsertSessionBefore点名账目外的会话时不写(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100))
	registry := h.open(ctx)
	created := mustList(t, registry)[0]

	if err := created.InsertSessionBefore(ctx, "不在账目里", ""); !errors.Is(err, CodeMoveInvalid) {
		t.Fatalf("要的是 CodeMoveInvalid，拿到 %v", err)
	}
	if err := created.InsertSessionBefore(ctx, "s1", "不在账目里"); !errors.Is(err, CodeMoveInvalid) {
		t.Fatalf("锚点不在账目里也该报 CodeMoveInvalid，拿到 %v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s1"}) {
		t.Fatalf("失败的挪位不该改账目，拿到 %v", got)
	}
}

// -- 摘除 ------------------------------------------------------------------

func TestDetachSession摘掉并落盘且不在账目里的是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100), header("s2", "/a", 200))
	registry := h.open(ctx)
	created := mustList(t, registry)[0]

	if err := created.DetachSession(ctx, "s1"); err != nil {
		t.Fatalf("摘除不该失败：%v", err)
	}
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s2"}) {
		t.Fatalf("拿到 %v", got)
	}

	before := created.UpdatedAt()
	if err := created.DetachSession(ctx, "s1"); err != nil {
		t.Fatalf("摘一个不在账目里的该是空操作，拿到 %v", err)
	}
	if !created.UpdatedAt().Equal(before) {
		t.Fatal("空操作不该推进 UpdatedAt")
	}

	registry = h.reopen(ctx)
	if got := mustList(t, registry)[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s2"}) {
		t.Fatalf("摘除该落盘，重开之后拿到 %v", got)
	}
}

// -- 归属投影 --------------------------------------------------------------

func TestSessionIDs筛掉工作目录对不上的候选并在下一次真实写入时裁掉(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/a2")
	// s2 的工作目录此刻还指向 /a，所以它和 s1 归在同一个工作区。
	h.filesystem.alias("/a2", "/a")
	h.persistence.set(header("s1", "/a", 100), header("s2", "/a2", 200))
	registry := h.open(ctx)
	if got := mustList(t, registry)[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s2", "s1"}) {
		t.Fatalf("前置条件不成立，拿到 %v", got)
	}

	// /a2 不再是 /a 的别名：s2 从此落在另一个目标上。
	h.filesystem.alias("/a2", "/a2")
	registry = h.reopen(ctx)
	created := mustList(t, registry)[0]

	// 读投影当场就把它筛掉了……
	if got := created.SessionIDs(); !slices.Equal(got, []session.SessionID{"s1"}) {
		t.Fatalf("拿到 %v", got)
	}
	// ……但落盘的账目还留着，这是「不抹数据」那条。
	if got := mustRecord(t, registry, created.ID()).SessionIDs; !slices.Equal(got, []session.SessionID{"s2", "s1"}) {
		t.Fatalf("读操作不该抹数据，拿到 %v", got)
	}

	// 下一次真实写入才把它裁掉——读和写用的是同一条判据。
	if err := created.SetTitle(ctx, "换个名字"); err != nil {
		t.Fatalf("改标题不该失败：%v", err)
	}
	if got := mustRecord(t, registry, created.ID()).SessionIDs; !slices.Equal(got, []session.SessionID{"s1"}) {
		t.Fatalf("一次真实写入该把筛掉的候选裁掉，拿到 %v", got)
	}
}

func TestSessionIDs候选全被裁掉时那一次写照样落下去(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100))
	registry := h.open(ctx)
	created := mustList(t, registry)[0]

	// 会话头整个不见了：候选筛不出来，但账目本身没被任何 fn 改过。
	h.persistence.set()
	registry = h.reopen(ctx)
	created = mustList(t, registry)[0]
	before := created.UpdatedAt()

	// fn 说「我什么都没改」，可裁剪有所斩获，所以这一格不是空操作。
	if err := created.DetachSession(ctx, "不在账目里"); err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if got := mustRecord(t, registry, created.ID()).SessionIDs; len(got) != 0 {
		t.Fatalf("裁剪该落盘，拿到 %v", got)
	}
	if !created.UpdatedAt().After(before) {
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

	if got := alive.Status(ctx); got != StatusOK {
		t.Fatalf("目录还在，该是 %q，拿到 %q", StatusOK, got)
	}

	h.filesystem.removeDir("/b")
	if got := moved.Status(ctx); got != StatusMissingDir {
		t.Fatalf("目录不在了，该是 %q，拿到 %q", StatusMissingDir, got)
	}

	// 后端出故障时报「目录不在」而不是崩掉：这是一个诊断投影，不是一次操作。
	h.filesystem.failStat("/c", errBackend)
	if got := broken.Status(ctx); got != StatusMissingDir {
		t.Fatalf("拿到 %q", got)
	}

	// 目录变成一个文件也算不在。
	h.filesystem.removeDir("/a")
	h.filesystem.addFile("/a")
	if got := alive.Status(ctx); got != StatusMissingDir {
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
	if created.TargetKey() != target.TargetKey {
		t.Fatalf("身份该是 %q，拿到 %q", target.TargetKey, created.TargetKey())
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
	sessions := []session.SessionID{"a", "b", "c"}
	if got := insertBeforeSession(sessions, "c", "a"); !slices.Equal(got, []session.SessionID{"c", "a", "b"}) {
		t.Fatalf("拿到 %v", got)
	}
	if got := insertBeforeSession(sessions, "a", ""); !slices.Equal(got, []session.SessionID{"b", "c", "a"}) {
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
