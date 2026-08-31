// 本文件的作用：登记册那些**跨越一次打开**的契约——打开与崩溃恢复、历史 bootstrap、
// 建与查、展示次序、登记册全局的归档集合，以及介质对不上时的报错。
//
// 这里的用例大半要么关掉再打开一次，要么在某一次落盘上注入失败：本包宣称的
// 「两次写中途崩掉之后还能收尾」只有在这两件事都能造出来的时候才是可验证的。

package workspace

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ds-harness-go/session"
)

// workspaceIDs 把一串工作区摊成 id，断言展示次序时读起来短一些。
func workspaceIDs(list []Workspace) []WorkspaceID {
	out := make([]WorkspaceID, 0, len(list))
	for _, one := range list {
		out = append(out, one.ID())
	}
	return out
}

// mustList 取一份展示次序，失败即用例失败。
func mustList(t *testing.T, registry *Registry) []Workspace {
	t.Helper()
	list, err := registry.List()
	if err != nil {
		t.Fatalf("列举工作区不该失败：%v", err)
	}
	return list
}

// mustCreate 建一个工作区，失败即用例失败。
func mustCreate(t *testing.T, ctx context.Context, registry *Registry, path, title string) Workspace {
	t.Helper()
	created, err := registry.Create(ctx, path, title)
	if err != nil {
		t.Fatalf("建工作区 %q 不该失败：%v", path, err)
	}
	return created
}

// seedState 绕过登记册直接把一份全局状态写到介质上。
//
// 造「介质被别处改过」那类局面只有这一条路：本包的公开面按定义写不出不一致的介质，
// 而 [Registry.validateStoredState] 要查的恰恰是别人写坏了的情况。
func seedState(t *testing.T, ctx context.Context, registry *Registry, state DomainState) {
	t.Helper()
	if err := registry.global.Set(ctx, state); err != nil {
		t.Fatalf("直接写全局状态不该失败：%v", err)
	}
}

// seedRecord 绕过登记册直接往工作区表里塞一条记录，理由同 [seedState]。
func seedRecord(t *testing.T, ctx context.Context, registry *Registry, id WorkspaceID, record Record) {
	t.Helper()
	if err := registry.workspace.Put(ctx, string(id), record); err != nil {
		t.Fatalf("直接写工作区记录不该失败：%v", err)
	}
}

// mustRecord 读一条落盘的工作区记录，失败或不存在即用例失败。
func mustRecord(t *testing.T, registry *Registry, id WorkspaceID) Record {
	t.Helper()
	record, found, err := registry.workspace.Get(string(id))
	if err != nil {
		t.Fatalf("读工作区 %q 不该失败：%v", id, err)
	}
	if !found {
		t.Fatalf("工作区 %q 应该在表里", id)
	}
	return record
}

// -- 打开与关闭 ------------------------------------------------------------

func TestOpen缺了必需依赖时报配置错误(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"没有域设施", func(config *Config) { config.Domain = nil }},
		{"没有会话持久化", func(config *Config) { config.Persistence = nil }},
		{"没有文件系统", func(config *Config) { config.FS = nil }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			config := h.config()
			one.mutate(&config)
			if _, err := Open(ctx, config); !errors.Is(err, CodeInvalidConfig) {
				t.Fatalf("要的是 CodeInvalidConfig，拿到 %v", err)
			}
		})
	}
}

func TestOpen可选依赖留空时用得起来(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	config := h.config()
	// 活会话表、发号器、时钟、logger 全部留空：这几项是可空的，
	// 留空的登记册必须照样能建工作区。
	config.Live = nil
	config.NewID = nil
	config.Now = nil
	config.Logger = nil

	registry, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("打开不该失败：%v", err)
	}
	defer func() { _ = registry.Close(ctx) }()

	h.filesystem.addDir("/a")
	created := mustCreate(t, ctx, registry, "/a", "")
	if created.ID() == "" {
		t.Fatal("回落的发号器应该给出一个非空 id")
	}
	if created.CreatedAt().IsZero() {
		t.Fatal("回落的时钟应该给出一个非零时刻")
	}
}

func TestOpen一份空介质盖上初始化标记且不再白花一次列举(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	registry := h.open(ctx)

	if list := mustList(t, registry); len(list) != 0 {
		t.Fatalf("空介质上不该有工作区，拿到 %v", workspaceIDs(list))
	}
	if !registry.snapshotState().Initialized {
		t.Fatal("bootstrap 跑完之后必须盖上 initialized 标记")
	}
	if h.persistence.count() != 1 {
		t.Fatalf("第一次打开要列举一次已落地会话，实际列举了 %d 次", h.persistence.count())
	}

	h.reopen(ctx)
	// 已经 bootstrap 过、又一条工作区都没有：没有候选账目要筛，那次列举纯属白花 I/O。
	if h.persistence.count() != 1 {
		t.Fatalf("空登记册重开不该再列举，实际累计列举了 %d 次", h.persistence.count())
	}
}

func TestOpen已初始化且有工作区时仍然要索引会话头(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")

	before := h.persistence.count()
	h.reopen(ctx)
	if h.persistence.count() != before+1 {
		t.Fatalf("表里有记录时重开必须列举一次会话头，实际从 %d 变成 %d",
			before, h.persistence.count())
	}
}

func TestOpen列举已落地会话失败时把域名放掉(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.persistence.fail(errBackend)

	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	// 打开失败必须把已经开出来的域关掉，否则第二次打开会撞在域名占用上。
	h.persistence.fail(nil)
	h.open(ctx)
}

func TestClose幂等且关掉之后一切都报没启动(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)

	if err := registry.Close(ctx); err != nil {
		t.Fatalf("关闭不该失败：%v", err)
	}
	if err := registry.Close(ctx); err != nil {
		t.Fatalf("重复关闭应该是空操作，拿到 %v", err)
	}

	if _, err := registry.List(); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("List 要的是 CodeNotStarted，拿到 %v", err)
	}
	if _, err := registry.Create(ctx, "/a", ""); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("Create 要的是 CodeNotStarted，拿到 %v", err)
	}
	if _, err := registry.Delete(ctx, "ws-1"); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("Delete 要的是 CodeNotStarted，拿到 %v", err)
	}
	if _, err := registry.InsertBefore(ctx, "ws-1", ""); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("InsertBefore 要的是 CodeNotStarted，拿到 %v", err)
	}
	if err := registry.ArchiveSession(ctx, "s1"); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("ArchiveSession 要的是 CodeNotStarted，拿到 %v", err)
	}
	if _, err := registry.table(); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("table 要的是 CodeNotStarted，拿到 %v", err)
	}
}

// -- 历史 bootstrap --------------------------------------------------------

func TestOpenBootstrap把已落地会话按工作目录聚成工作区(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/home/alpha")
	h.filesystem.addDir("/home/beta")
	h.persistence.set(
		header("s1", "/home/alpha", 100),
		header("s2", "/home/alpha", 300),
		header("s3", "/home/beta", 200),
	)
	registry := h.open(ctx)

	list := mustList(t, registry)
	if got := workspaceIDs(list); !slices.Equal(got, []WorkspaceID{"ws-1", "ws-2"}) {
		t.Fatalf("展示次序该按「最新一条会话」从新到旧，拿到 %v", got)
	}
	if list[0].Path() != "/home/alpha" || list[1].Path() != "/home/beta" {
		t.Fatalf("路径对不上：%q、%q", list[0].Path(), list[1].Path())
	}
	if list[0].Title() != "alpha" || list[1].Title() != "beta" {
		t.Fatalf("默认标题该取路径最后一段，拿到 %q、%q", list[0].Title(), list[1].Title())
	}
	// 组内按创建时刻从新到旧。
	if got := list[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s2", "s1"}) {
		t.Fatalf("组内该按创建时刻从新到旧，拿到 %v", got)
	}
	// 收编的是历史，一份历史的年纪是它最新那条会话的时刻，不是被收编的那一刻。
	if want := time.UnixMilli(300).UTC(); !list[0].CreatedAt().Equal(want) {
		t.Fatalf("建时刻该是 %v，拿到 %v", want, list[0].CreatedAt())
	}
}

func TestOpenBootstrap同刻的会话按会话id定序(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(
		header("s-b", "/a", 100),
		header("s-a", "/a", 100),
	)
	registry := h.open(ctx)

	list := mustList(t, registry)
	if got := list[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s-a", "s-b"}) {
		t.Fatalf("同刻该按会话 id 定序好让结果可复现，拿到 %v", got)
	}
}

func TestOpenBootstrap跳过工作目录用不了的会话头(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addFile("/f")
	h.filesystem.failResolve("/bad", errBackend)
	h.filesystem.failStat("/statfail", errBackend)
	h.persistence.set(
		header("s0", "", 10),
		header("s1", "/bad", 20),
		header("s2", "/statfail", 30),
		header("s3", "/f", 40),
		header("s4", "/nothere", 50),
		header("s5", "/a", 60),
	)
	registry := h.open(ctx)

	list := mustList(t, registry)
	if len(list) != 1 {
		t.Fatalf("只有 /a 那一条会话头是能用的，拿到 %v", workspaceIDs(list))
	}
	if got := list[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s5"}) {
		t.Fatalf("只该收编 s5，拿到 %v", got)
	}

	// 用不了的会话头一条都不该让登记册打不开，它们只留一条诊断理由。
	registry.mutex.RLock()
	invalid := len(registry.invalidSessions)
	noCwd := registry.invalidSessions["s0"]
	registry.mutex.RUnlock()
	if invalid != 5 {
		t.Fatalf("五条会话头的工作目录用不了，实际记下 %d 条", invalid)
	}
	if noCwd == "" {
		t.Fatal("没有工作目录的会话头也该留下一条理由")
	}
}

func TestOpenBootstrap被打断之后重跑会并进已有的工作区(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(
		header("s1", "/a", 100),
		header("s2", "/a", 200),
	)
	// bootstrap 的第二次全局写（盖 initialized 标记那一次）失败：
	// 介质上于是留下「记录已经写好、initialized 还是假」这个中间态。
	h.backend.armGlobal(map[int]error{2: errBackend})

	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}

	h.backend.armGlobal(nil)
	h.persistence.set(
		header("s1", "/a", 100),
		header("s2", "/a", 200),
		header("s3", "/a", 300),
	)
	registry := h.open(ctx)

	list := mustList(t, registry)
	if got := workspaceIDs(list); !slices.Equal(got, []WorkspaceID{"ws-1"}) {
		t.Fatalf("重跑不该再建一个工作区，拿到 %v", got)
	}
	if got := list[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s3", "s2", "s1"}) {
		t.Fatalf("历史候选该补到前面，拿到 %v", got)
	}
	// 建时刻是第一次收编时定下的，重跑不重定。
	if want := time.UnixMilli(200).UTC(); !list[0].CreatedAt().Equal(want) {
		t.Fatalf("建时刻该还是 %v，拿到 %v", want, list[0].CreatedAt())
	}
}

func TestOpenBootstrap候选已经全被别人认领的目录不建工作区(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.persistence.set(header("s1", "/a", 100))
	h.backend.armGlobal(map[int]error{2: errBackend})
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}

	// s1 的工作目录改指到 /b：它那一组落在一个还没有工作区的目标上，
	// 但它自己已经记在 ws-1 的账目里了，于是这一组一个候选都不剩。
	h.filesystem.alias("/a", "/b")
	h.backend.armGlobal(nil)
	registry := h.open(ctx)

	list := mustList(t, registry)
	if got := workspaceIDs(list); !slices.Equal(got, []WorkspaceID{"ws-1"}) {
		t.Fatalf("不该为一组空候选建工作区，拿到 %v", got)
	}
	if got := list[0].SessionIDs(); len(got) != 0 {
		t.Fatalf("s1 的工作目录已经不是这个工作区的了，该被筛掉，拿到 %v", got)
	}
	// 被筛掉的候选**不抹**：目录可能只是临时被移走了。
	if got := mustRecord(t, registry, "ws-1").SessionIDs; !slices.Equal(got, []session.SessionID{"s1"}) {
		t.Fatalf("落盘的候选账目不该被读操作抹掉，拿到 %v", got)
	}
}

func TestOpenBootstrap重跑时上一份次序里的工作区排在新收编的前面(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.persistence.set(header("s1", "/a", 100))
	h.backend.armGlobal(map[int]error{2: errBackend})
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}

	// s2 和 s1 同刻：两组的「最新时刻」打平，胜负由上一份次序里的位置定。
	h.backend.armGlobal(nil)
	h.persistence.set(
		header("s1", "/a", 100),
		header("s2", "/b", 100),
	)
	registry := h.open(ctx)

	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-1", "ws-2"}) {
		t.Fatalf("打平时该按上一份次序排，拿到 %v", got)
	}
}

// -- 建与查 ----------------------------------------------------------------

func TestCreate同一个目标标识只建一个工作区(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)

	first := mustCreate(t, ctx, registry, "/a", "初号")
	if first.ID() != "ws-1" || first.Path() != "/a" || first.Title() != "初号" {
		t.Fatalf("新建的工作区对不上：%q %q %q", first.ID(), first.Path(), first.Title())
	}

	// 末尾斜杠折成同一个目标标识：交出已有的那一个，且**不改它的标题**。
	again := mustCreate(t, ctx, registry, "/a/", "贰号")
	if again.ID() != first.ID() {
		t.Fatalf("同一个目录该交出同一个工作区，拿到 %q 和 %q", first.ID(), again.ID())
	}
	if again.Title() != "初号" {
		t.Fatalf("重复建不该改标题，拿到 %q", again.Title())
	}
	if got := workspaceIDs(mustList(t, registry)); len(got) != 1 {
		t.Fatalf("只该有一个工作区，拿到 %v", got)
	}
}

func TestCreate标题留空时取展示路径最后一段(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/home/项目")
	registry := h.open(ctx)

	created := mustCreate(t, ctx, registry, "/home/项目", "")
	if created.Title() != "项目" {
		t.Fatalf("默认标题该是 %q，拿到 %q", "项目", created.Title())
	}
}

func TestCreate路径用不了时分类到位(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addFile("/f")
	h.filesystem.failResolve("/bad", errBackend)
	h.filesystem.failStat("/statfail", errBackend)
	registry := h.open(ctx)

	cases := []struct {
		name string
		path string
		code Code
	}{
		{"解析不出来", "/bad", CodeInvalidPath},
		{"目录不存在", "/nothere", CodeInvalidPath},
		{"那是一个文件", "/f", CodeInvalidPath},
		// 后端自己出故障是可以重试的，不该和「这条路径没用」混成一类。
		{"文件系统出故障", "/statfail", CodeStorageFailed},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if _, err := registry.Create(ctx, one.path, ""); !errors.Is(err, one.code) {
				t.Fatalf("要的是 %s，拿到 %v", one.code, err)
			}
		})
	}
}

func TestCreate新建的排在展示次序最前面(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.filesystem.addDir("/c")
	registry := h.open(ctx)

	mustCreate(t, ctx, registry, "/a", "")
	mustCreate(t, ctx, registry, "/b", "")
	mustCreate(t, ctx, registry, "/c", "")

	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-3", "ws-2", "ws-1"}) {
		t.Fatalf("新建的该排最前，拿到 %v", got)
	}
	// 重启之后次序照旧。
	registry = h.reopen(ctx)
	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-3", "ws-2", "ws-1"}) {
		t.Fatalf("次序该落盘，重开之后拿到 %v", got)
	}
}

func TestGet不认识的id报不在(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	registry := h.open(ctx)

	if _, ok := registry.Get("没有这个"); ok {
		t.Fatal("不认识的 id 该报不在")
	}
}

func TestResolveByPath只查不建(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.filesystem.failResolve("/bad", errBackend)
	registry := h.open(ctx)
	created := mustCreate(t, ctx, registry, "/a", "")

	found, ok, err := registry.ResolveByPath(ctx, "/a/")
	if err != nil || !ok || found.ID() != created.ID() {
		t.Fatalf("该找到 %q，拿到 ok=%v err=%v", created.ID(), ok, err)
	}
	if _, ok, err := registry.ResolveByPath(ctx, "/b"); err != nil || ok {
		t.Fatalf("没人认领的目录该报不在，拿到 ok=%v err=%v", ok, err)
	}
	if _, _, err := registry.ResolveByPath(ctx, "/bad"); !errors.Is(err, CodeInvalidPath) {
		t.Fatalf("要的是 CodeInvalidPath，拿到 %v", err)
	}
	if got := workspaceIDs(mustList(t, registry)); len(got) != 1 {
		t.Fatalf("查询不该建出东西来，拿到 %v", got)
	}
}

// -- 删除 ------------------------------------------------------------------

func TestDelete幂等且落盘(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	mustCreate(t, ctx, registry, "/b", "")

	deleted, err := registry.Delete(ctx, "ws-1")
	if err != nil || !deleted {
		t.Fatalf("删一条在的该返回真，拿到 %v %v", deleted, err)
	}
	deleted, err = registry.Delete(ctx, "ws-1")
	if err != nil || deleted {
		t.Fatalf("删一条不在的该是空操作，拿到 %v %v", deleted, err)
	}

	registry = h.reopen(ctx)
	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-2"}) {
		t.Fatalf("删除该落盘，重开之后拿到 %v", got)
	}
}

// -- 展示次序 --------------------------------------------------------------

func TestInsertBefore按锚点挪位并落盘(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	h.filesystem.addDir("/c")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	mustCreate(t, ctx, registry, "/b", "")
	mustCreate(t, ctx, registry, "/c", "")
	// 此刻次序是 ws-3、ws-2、ws-1。

	order, err := registry.InsertBefore(ctx, "ws-1", "ws-3")
	if err != nil {
		t.Fatalf("挪位不该失败：%v", err)
	}
	if !slices.Equal(order, []WorkspaceID{"ws-1", "ws-3", "ws-2"}) {
		t.Fatalf("挪到锚点前面，拿到 %v", order)
	}

	// 锚点为空串是「挪到末尾」。
	order, err = registry.InsertBefore(ctx, "ws-1", "")
	if err != nil {
		t.Fatalf("挪到末尾不该失败：%v", err)
	}
	if !slices.Equal(order, []WorkspaceID{"ws-3", "ws-2", "ws-1"}) {
		t.Fatalf("挪到末尾，拿到 %v", order)
	}

	registry = h.reopen(ctx)
	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-3", "ws-2", "ws-1"}) {
		t.Fatalf("次序该落盘，重开之后拿到 %v", got)
	}
}

func TestInsertBefore挪到原位是空操作(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	mustCreate(t, ctx, registry, "/b", "")
	// 此刻次序是 ws-2、ws-1。

	// 锚点就是自己：语义上无从执行，直接交出当前次序。
	order, err := registry.InsertBefore(ctx, "ws-2", "ws-2")
	if err != nil || !slices.Equal(order, []WorkspaceID{"ws-2", "ws-1"}) {
		t.Fatalf("拿到 %v %v", order, err)
	}
	// 挪到本来就在的位置：算出来的次序和现在一样，不写。
	order, err = registry.InsertBefore(ctx, "ws-2", "ws-1")
	if err != nil || !slices.Equal(order, []WorkspaceID{"ws-2", "ws-1"}) {
		t.Fatalf("拿到 %v %v", order, err)
	}
}

func TestInsertBefore点名不在次序里的工作区时不写(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")

	if _, err := registry.InsertBefore(ctx, "没有这个", ""); !errors.Is(err, CodeOrderInvalid) {
		t.Fatalf("要的是 CodeOrderInvalid，拿到 %v", err)
	}
	if _, err := registry.InsertBefore(ctx, "ws-1", "没有这个"); !errors.Is(err, CodeOrderInvalid) {
		t.Fatalf("锚点不在次序里也该报 CodeOrderInvalid，拿到 %v", err)
	}
	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-1"}) {
		t.Fatalf("失败的挪位不该改次序，拿到 %v", got)
	}
}

// -- 归档 ------------------------------------------------------------------

func TestArchiveSession收下活着的和已落地的会话(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("已落地", "/a", 100))
	h.live.add(header("活着的", "/a", 200))
	registry := h.open(ctx)

	if err := registry.ArchiveSession(ctx, "已落地"); err != nil {
		t.Fatalf("归档已落地会话不该失败：%v", err)
	}
	if err := registry.ArchiveSession(ctx, "活着的"); err != nil {
		t.Fatalf("归档活会话不该失败：%v", err)
	}
	// 重复归档是空操作。
	if err := registry.ArchiveSession(ctx, "已落地"); err != nil {
		t.Fatalf("重复归档不该失败：%v", err)
	}

	want := []session.SessionID{"已落地", "活着的"}
	if got := registry.ArchivedSessionIDs(); !slices.Equal(got, want) {
		t.Fatalf("归档集合该按归档顺序，拿到 %v", got)
	}
	registry = h.reopen(ctx)
	if got := registry.ArchivedSessionIDs(); !slices.Equal(got, want) {
		t.Fatalf("归档集合该落盘，重开之后拿到 %v", got)
	}
}

func TestArchiveSession不动工作区账目(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.persistence.set(header("s1", "/a", 100), header("s2", "/a", 200))
	registry := h.open(ctx)

	if err := registry.ArchiveSession(ctx, "s1"); err != nil {
		t.Fatalf("归档不该失败：%v", err)
	}
	// 归档叠在账目**之上**：取消归档要能还原到原位，所以位置必须留着。
	if got := mustList(t, registry)[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s2", "s1"}) {
		t.Fatalf("归档不该动账目，拿到 %v", got)
	}
}

func TestArchiveSession认不出的会话报未知而后端故障报存储失败(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	registry := h.open(ctx)

	if err := registry.ArchiveSession(ctx, "没有这个"); !errors.Is(err, CodeUnknownSession) {
		t.Fatalf("要的是 CodeUnknownSession，拿到 %v", err)
	}

	// 后端掉线绝不许冒充「这个会话不存在」——那会让一次磁盘故障变成一次拒绝归档。
	h.persistence.fail(errBackend)
	err := registry.ArchiveSession(ctx, "另一个")
	if !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if errors.Is(err, CodeUnknownSession) {
		t.Fatal("后端故障不许塌成 CodeUnknownSession")
	}
}

// -- 建工作区那条两次写的崩溃恢复 ------------------------------------------

func TestCreate待恢复标记写不下去时什么都不留(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	h.backend.armGlobal(map[int]error{1: errBackend})

	if _, err := registry.Create(ctx, "/a", ""); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if _, ok := registry.Get("ws-1"); ok {
		t.Fatal("失败的建不该在缓存里留下实体")
	}
	if got := workspaceIDs(mustList(t, registry)); len(got) != 0 {
		t.Fatalf("失败的建不该留下工作区，拿到 %v", got)
	}
}

func TestCreate记录写不下去时把标记撤回去(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })

	if _, err := registry.Create(ctx, "/a", ""); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if registry.snapshotState().PendingMutation != nil {
		t.Fatal("标记该被撤回去")
	}
	// 撤干净之后还能接着建。
	mustCreate(t, ctx, registry, "/a", "")
}

func TestCreate记录写入和标记回滚都失败时两条错误都留下(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })
	h.backend.armGlobal(map[int]error{2: errBackend})

	_, err := registry.Create(ctx, "/a", "")
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeStorageFailed {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(coded.Cause, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("原本的失败和善后的失败必须都留在原因里，拿到 %v", coded.Cause)
	}
}

func TestCreate次序写不下去时把记录和标记都撤掉(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	h.backend.armGlobal(map[int]error{2: errBackend})

	if _, err := registry.Create(ctx, "/a", ""); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if registry.snapshotState().PendingMutation != nil {
		t.Fatal("标记该被撤回去")
	}
	registry = h.reopen(ctx)
	if got := workspaceIDs(mustList(t, registry)); len(got) != 0 {
		t.Fatalf("重开之后不该看见那条孤儿记录，拿到 %v", got)
	}
	size, err := registry.workspace.Size()
	if err != nil || size != 0 {
		t.Fatalf("表里该是空的，拿到 %d %v", size, err)
	}
}

func TestCreate次序写入和标记回滚都失败时两条错误都留下(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	// 第 2 次是提交次序，第 3 次是把标记撤回去；两次都失败。
	h.backend.armGlobal(map[int]error{2: errBackend, 3: errBackend})

	_, err := registry.Create(ctx, "/a", "")
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeStorageFailed {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(coded.Cause, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("两条错误都该留下，拿到 %v", coded.Cause)
	}
}

func TestCreate次序写入和记录回滚都失败时留着标记等下次收尾(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	h.backend.armGlobal(map[int]error{2: errBackend})
	h.backend.set(func(backend *flakyKV) { backend.deleteErr = errBackend })

	if _, err := registry.Create(ctx, "/a", ""); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	pending := registry.snapshotState().PendingMutation
	if pending == nil || pending.Operation != OperationCreate || pending.WorkspaceID != "ws-1" {
		t.Fatalf("标记该留着，拿到 %v", pending)
	}

	// 下一次启动照着标记把那条孤儿记录收掉。
	registry = h.reopen(ctx)
	if registry.snapshotState().PendingMutation != nil {
		t.Fatal("启动时该把标记收掉")
	}
	size, err := registry.workspace.Size()
	if err != nil || size != 0 {
		t.Fatalf("孤儿记录该被删掉，拿到 %d %v", size, err)
	}
}

// -- 删工作区那条两次写的崩溃恢复 ------------------------------------------

func TestDelete次序写不下去时不删(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	h.backend.armGlobal(map[int]error{1: errBackend})

	if _, err := registry.Delete(ctx, "ws-1"); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if _, ok := registry.Get("ws-1"); !ok {
		t.Fatal("次序都没写成，工作区该原封不动")
	}
}

func TestDelete记录删不掉时把工作区放回去(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	h.backend.set(func(backend *flakyKV) { backend.deleteErr = errBackend })

	if _, err := registry.Delete(ctx, "ws-1"); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	if _, ok := registry.Get("ws-1"); !ok {
		t.Fatal("删失败之后工作区该被放回缓存")
	}
	if got := workspaceIDs(mustList(t, registry)); !slices.Equal(got, []WorkspaceID{"ws-1"}) {
		t.Fatalf("次序该被还原，拿到 %v", got)
	}
}

func TestDelete记录删不掉且次序回滚也失败时站在可恢复的方向上(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	h.backend.set(func(backend *flakyKV) { backend.deleteErr = errBackend })
	h.backend.armGlobal(map[int]error{2: errBackend})

	_, err := registry.Delete(ctx, "ws-1")
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeStorageFailed {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	var joined interface{ Unwrap() []error }
	if !errors.As(coded.Cause, &joined) || len(joined.Unwrap()) != 2 {
		t.Fatalf("两条错误都该留下，拿到 %v", coded.Cause)
	}
	// 落盘的标记仍然说「这次删除要做完」，缓存必须站在那个方向上。
	if _, ok := registry.Get("ws-1"); ok {
		t.Fatal("缓存不该重新发布一条已经不在落盘次序里的记录")
	}

	registry = h.reopen(ctx)
	if got := workspaceIDs(mustList(t, registry)); len(got) != 0 {
		t.Fatalf("下一次启动该把这次删除做完，拿到 %v", got)
	}
}

func TestDelete删成了但标记没清掉时只记日志(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sink := &logSink{}
	h.logger = sink.logger()
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	h.backend.armGlobal(map[int]error{2: errBackend})

	deleted, err := registry.Delete(ctx, "ws-1")
	if err != nil || !deleted {
		t.Fatalf("删除已经提交了，不该报失败，拿到 %v %v", deleted, err)
	}
	if !sink.contains("待恢复标记没能清掉", "ws-1") {
		t.Fatalf("这件事必须留下痕迹，日志是：%v", sink.dump())
	}

	// 下一次操作之前会把这个标记收掉，它不能被下一次建/删覆盖过去。
	mustCreate(t, ctx, registry, "/b", "")
	if registry.snapshotState().PendingMutation != nil {
		t.Fatal("下一次操作之前该把标记收掉")
	}
}

// -- 介质对不上时报错而不修 ------------------------------------------------

func TestOpen介质对不上时报错而不修(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		seed func(t *testing.T, ctx context.Context, h *harness, registry *Registry)
	}{
		{
			name: "次序里同一个工作区出现两次",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				mustCreate(t, ctx, registry, "/a", "")
				seedState(t, ctx, registry, DomainState{
					Initialized:  true,
					WorkspaceIDs: []WorkspaceID{"ws-1", "ws-1"},
				})
			},
		},
		{
			name: "次序指向一条不存在的记录",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				seedState(t, ctx, registry, DomainState{
					Initialized:  true,
					WorkspaceIDs: []WorkspaceID{"ws-9"},
				})
			},
		},
		{
			name: "表里有一条次序外的记录",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				mustCreate(t, ctx, registry, "/a", "")
				mustCreate(t, ctx, registry, "/b", "")
				seedState(t, ctx, registry, DomainState{
					Initialized:  true,
					WorkspaceIDs: []WorkspaceID{"ws-2"},
				})
			},
		},
		{
			name: "一个目录被两个工作区认领",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				mustCreate(t, ctx, registry, "/a", "")
				seedRecord(t, ctx, registry, "ws-9", mustRecord(t, registry, "ws-1"))
				seedState(t, ctx, registry, DomainState{
					Initialized:  true,
					WorkspaceIDs: []WorkspaceID{"ws-1", "ws-9"},
				})
			},
		},
		{
			name: "一个会话同时记在两个账目里",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				mustCreate(t, ctx, registry, "/a", "")
				mustCreate(t, ctx, registry, "/b", "")
				for _, id := range []WorkspaceID{"ws-1", "ws-2"} {
					record := mustRecord(t, registry, id)
					record.SessionIDs = []session.SessionID{"s1"}
					seedRecord(t, ctx, registry, id, record)
				}
			},
		},
		{
			name: "待恢复标记点名的工作区还在次序里",
			seed: func(t *testing.T, ctx context.Context, h *harness, registry *Registry) {
				mustCreate(t, ctx, registry, "/a", "")
				seedState(t, ctx, registry, DomainState{
					Initialized:     true,
					WorkspaceIDs:    []WorkspaceID{"ws-1"},
					PendingMutation: &PendingMutation{Operation: OperationCreate, WorkspaceID: "ws-1"},
				})
			},
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			h := newHarness(t)
			h.filesystem.addDir("/a")
			h.filesystem.addDir("/b")
			registry := h.open(ctx)
			one.seed(t, ctx, h, registry)
			h.close(ctx)

			if _, err := h.tryOpen(ctx); !errors.Is(err, CodeInconsistentState) {
				t.Fatalf("要的是 CodeInconsistentState，拿到 %v", err)
			}
		})
	}
}

func TestList次序指到缓存外时报不一致(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	registry := h.open(ctx)

	// 这一步在真实运行里做不出来（打开时的校验会先拦住），所以只能直接摆出来：
	// 它钉的是「List 遇到这种局面报错，而不是安静地少给一条」。
	registry.mutex.Lock()
	registry.state.WorkspaceIDs = []WorkspaceID{"幽灵"}
	registry.mutex.Unlock()

	if _, err := registry.List(); !errors.Is(err, CodeInconsistentState) {
		t.Fatalf("要的是 CodeInconsistentState，拿到 %v", err)
	}
}

// -- 被筛掉的候选留下的那条诊断 --------------------------------------------

func TestOpen候选没通过归属判据时留下诊断且不抹数据(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		// arrange 在会话已经挂上来之后改动外部世界，让候选在下一次打开时筛不出来。
		arrange  func(h *harness)
		fragment string
	}{
		{
			name:     "工作目录解析不出来了",
			arrange:  func(h *harness) { h.filesystem.failResolve("/a", errBackend) },
			fragment: "解析不出来",
		},
		{
			name: "工作目录换到了别的地方",
			arrange: func(h *harness) {
				h.filesystem.alias("/a", "/b")
			},
			fragment: "不是这个工作区的",
		},
		{
			name:     "会话头整个不见了",
			arrange:  func(h *harness) { h.persistence.set() },
			fragment: "找不到它的会话头",
		},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			h := newHarness(t)
			sink := &logSink{}
			h.logger = sink.logger()
			h.filesystem.addDir("/a")
			h.filesystem.addDir("/b")
			h.persistence.set(header("s1", "/a", 100))
			registry := h.open(ctx)
			// bootstrap 已经把 s1 收进 /a 那个工作区了。
			if got := mustList(t, registry)[0].SessionIDs(); !slices.Equal(got, []session.SessionID{"s1"}) {
				t.Fatalf("前置条件不成立，拿到 %v", got)
			}

			one.arrange(h)
			registry = h.reopen(ctx)

			if got := mustList(t, registry)[0].SessionIDs(); len(got) != 0 {
				t.Fatalf("这个候选该被筛掉，拿到 %v", got)
			}
			if !sink.contains("没能通过归属判据", one.fragment) {
				t.Fatalf("该留下一条说得清原因的诊断，日志是：%v", sink.dump())
			}
			// 只记日志，**不抹数据**：目录可能只是临时被移走了。
			if got := mustRecord(t, registry, "ws-1").SessionIDs; !slices.Equal(got, []session.SessionID{"s1"}) {
				t.Fatalf("落盘的账目不该被读操作改掉，拿到 %v", got)
			}
		})
	}
}
