// 本文件的作用：那些只有在**底层坏掉**时才走得到的路径——域打不开、域关不掉、
// 域在登记册底下被关掉、以及每一条内部写路径各自守着的那道「还没启动」。
//
// 这些用例大半要直接调本包的非导出函数或者直接改内部字段。这是有意的：
// 它们要造的局面按定义从公开面造不出来（公开面写不出一份坏介质，也不会
// 让一个还自认为开着的登记册手里握着一个已经关掉的域），而这些路径正是
// 「介质在脚下塌了会怎样」的全部答案。
//
// # 剩下那几条盖不到的分支
//
// 本包的语句覆盖率停在 99.1%，差的那几条都在 registry.go 里，且都要求
// **同一次调用里前一次读成功、后一次读失败**——而域这一层的读只在域关掉时失败，
// 那是个一次性的全局开关，没有第二次机会。逐条：
//
//   - start 里 table.Size 失败：域关掉的话，它前面那次 global.Get 就先失败了
//     （已由 [TestStart在一个已经关掉的域上读不到全局状态] 盖住）。
//   - start 末尾第二次 validateStoredState / rebuildEntities 失败：这两次之间只隔着
//     一次 indexLiveSessions，它既不写介质也不改状态，所以第一次过了第二次一定过。
//   - bootstrap 里 table.Get 失败、以及紧跟着的「记录从表里消失了」：那个 id 就是
//     同一次调用里 table.Entries 刚交出来的，域没关的话它一定还在。
//   - bootstrap 末尾 bootstrapOrder 失败：它唯一的失败来源也是 table.Entries，
//     而 bootstrap 开头已经成功读过一次。
//
// 这四处保留而不删，是因为它们的判据（域可能在两次读之间被关掉）在类型上成立，
// 删掉就等于把一个 error 返回值默默丢掉。

package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// -- 打不开 / 关不掉 --------------------------------------------------------

func TestOpen域打不开时报存储失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	// 先把这个域名占掉：一个域只能同时开一次。
	opened, err := h.facility.Open(ctx, Spec())
	if err != nil {
		t.Fatalf("占住域名不该失败：%v", err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })

	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestOpen启动失败时顺手关域而关不掉只留一条日志(t *testing.T) {
	ctx := t.Context()
	sink := &logSink{}
	h := newHarness(t)
	h.logger = sink.logger()
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustCreate(t, ctx, registry, "/a", "")

	// 把次序写成「同一个工作区出现两次」：下一次启动一定过不了状态校验。
	seedState(t, ctx, registry, DomainState{
		Initialized:  true,
		WorkspaceIDs: []WorkspaceID{created.ID(), created.ID()},
	})
	h.close(ctx)

	// 启动失败之后那一次关域也失败：原因必须是**启动**失败的那条，
	// 善后失败只留日志——调用方要看的是介质为什么用不了。
	h.backend.set(func(backend *flakyKV) { backend.closeErr = errBackend })
	_, err := h.tryOpen(ctx)
	if !errors.Is(err, CodeInconsistentState) {
		t.Fatalf("要的是 CodeInconsistentState，拿到 %v", err)
	}
	if !sink.contains("打开失败后关闭域也失败") {
		t.Fatalf("那次关不掉该留下日志，拿到 %v", sink.dump())
	}
}

func TestClose关域失败时报存储失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)

	h.backend.set(func(backend *flakyKV) { backend.closeErr = errBackend })
	if err := registry.Close(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	// 关不掉也照样算关了：再关一次是空操作，之后一切都报没启动。
	if err := registry.Close(ctx); err != nil {
		t.Fatalf("再关一次不该失败：%v", err)
	}
	if _, err := registry.List(ctx); !errors.Is(err, CodeNotStarted) {
		t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
	}
}

func TestStart在一个已经关掉的域上读不到全局状态(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)
	h.close(ctx)

	// 拿着一个已经关掉的域再跑一次启动：读全局槽那一步就该报存储失败，
	// 而不是安静地读到一份初始状态然后照着它去 bootstrap。
	if err := registry.start(ctx, registry.dom); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestStart域的形状对不上时报存储失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)

	// 一个域的形状由它自己的 [domain.Spec] 定。拿到一个形状对不上的域时，
	// 取表和取全局槽这两步各自报存储失败——而不是让后面的代码抱着一个
	// 零值句柄往下走。
	noTable, err := h.facility.Open(ctx, domain.Spec{Name: "probe_a", Version: 1})
	if err != nil {
		t.Fatalf("打开域不该失败：%v", err)
	}
	if err := registry.start(ctx, noTable); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("没有那张表时要的是 CodeStorageFailed，拿到 %v", err)
	}

	wrongGlobal, err := h.facility.Open(ctx, domain.Spec{
		Name:    "probe_b",
		Version: 1,
		Global:  domain.DefineGlobal(0, nil),
		Tables: []domain.TableSpec{
			domain.DefineTable(TableName, func(Record) error { return nil }),
		},
	})
	if err != nil {
		t.Fatalf("打开域不该失败：%v", err)
	}
	if err := registry.start(ctx, wrongGlobal); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("全局槽类型对不上时要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestOpen已经初始化且有工作区时列举失败就打不开(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	mustCreate(t, ctx, registry, "/a", "")
	h.close(ctx)

	// 表里有记录，所以这一次打开必须去列举会话头给候选账目做筛选；列举失败就打不开。
	h.persistence.fail(errBackend)
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

// -- 收尾待恢复标记 --------------------------------------------------------

func TestOpen收尾待恢复标记时删记录失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustCreate(t, ctx, registry, "/a", "")

	// 造一个「建到一半」的局面：记录在表里，但不在次序里，标记点着它。
	seedState(t, ctx, registry, DomainState{
		Initialized:     true,
		WorkspaceIDs:    []WorkspaceID{},
		PendingMutation: &PendingMutation{Operation: OperationCreate, WorkspaceID: created.ID()},
	})
	h.close(ctx)

	h.backend.set(func(backend *flakyKV) { backend.deleteErr = errBackend })
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestEnqueue每次操作之前先收尾待恢复标记(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustCreate(t, ctx, registry, "/a", "")

	// 一个点着「还在次序里的工作区」的标记解释不了任何事，只能是别处改坏了介质。
	// 它必须在下一次操作**动手之前**就把那次操作拦下来。
	//
	// 新增: 这个标记原来是直接按在登记册内存里那份状态上的。状态搬回介质之后
	// （见 [Registry.readState]）内存里没有它了，只能绕过登记册往介质上写，
	// 而那正是 [seedState] 存在的理由——这个局面在生产上也只可能由别处写坏介质造成。
	state, err := registry.readState(ctx)
	if err != nil {
		t.Fatalf("读全局状态不该失败：%v", err)
	}
	state.PendingMutation = &PendingMutation{
		Operation:   OperationDelete,
		WorkspaceID: created.ID(),
	}
	seedState(t, ctx, registry, state)

	if _, err := registry.Delete(ctx, created.ID()); !errors.Is(err, CodeInconsistentState) {
		t.Fatalf("要的是 CodeInconsistentState，拿到 %v", err)
	}
}

// -- bootstrap 的失败路径 --------------------------------------------------

func TestOpenBootstrap写工作区失败时打不开(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100))

	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestOpenBootstrap次序写不下去时打不开(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	ids := h.seedWorkspaces(ctx, "/a", "/b")
	// 建出来的次序是「后建的排最前」，也就是 ids[1]、ids[0]；而按会话时刻排是反过来。
	// 次序确实要变，bootstrap 才会走那次「仍然 initialized=false 的次序提交」。
	h.persistence.set(
		header("s1", ids[0], 200),
		header("s2", ids[1], 100),
	)

	// 第一次全局写就是那次「仍然 initialized=false 的次序提交」。
	h.backend.armGlobal(map[int]error{1: errBackend})
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

// interruptedBootstrap 跑一次被打断的 bootstrap：账目都落了盘，
// 但 initialized 标记没盖上，所以下一次打开会对着这些记录重跑一遍。
//
// 新增: 这里点名的是**第一次**全局写。bootstrap 只在算出来的次序和上一份不同时
// 才多写一次；调用这个函数的两个用例都只有一个工作区，次序不会变，
// 所以整趟 bootstrap 只写一次全局槽，那一次就是盖 initialized 标记。
func interruptedBootstrap(t *testing.T, ctx context.Context, h *harness) {
	t.Helper()
	h.backend.armGlobal(map[int]error{1: errBackend})
	if _, err := h.tryOpen(ctx); err == nil {
		t.Fatal("这一次打开该在盖 initialized 标记那一步失败")
	}
	h.backend.armGlobal(nil)
}

func TestOpenBootstrap重跑时已有账目里的会话留在历史候选后面(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100))
	interruptedBootstrap(t, ctx, h)

	// 重跑时 s1 的会话头已经不在了，取而代之的是 s2。收编不该把人手排过的账目抹掉：
	// 新的历史候选补在前面，已有账目里没被历史提到的 s1 跟在后面留着。
	h.persistence.set(header("s2", ws, 200))
	registry := h.open(ctx)
	if got := mustRecord(t, ctx, registry, ws).SessionIDs; len(got) != 2 || got[0] != "s2" || got[1] != "s1" {
		t.Fatalf("历史候选该在前、已有账目在后，拿到 %v", got)
	}
	// 读投影另说：s1 没有会话头，筛不出来。
	if got := mustSessionIDs(t, ctx, mustGet(t, ctx, registry, ws)); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("拿到 %v", got)
	}
}

func TestOpenBootstrap重跑时更新已有工作区失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	ws := h.seedWorkspaces(ctx, "/a")[0]
	h.persistence.set(header("s1", ws, 100))
	interruptedBootstrap(t, ctx, h)

	h.persistence.set(header("s1", ws, 100), header("s2", ws, 200))
	h.backend.set(func(backend *flakyKV) { backend.putErr = errBackend })
	if _, err := h.tryOpen(ctx); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
}

func TestOpenBootstrap一条历史都没有时按记录自己的建时刻和id定序(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)

	// 两条建时刻**完全一样**、且上一份次序里都没有的记录：三级判据里前两级都分不出，
	// 只剩下按 id 这一级。没有这一级的话，两次启动给出的次序可以不一样。
	at := h.now()
	for _, id := range []WorkspaceID{"ws-b", "ws-a"} {
		seedRecord(t, ctx, registry, id, Record{
			TargetKey:   fs.TargetKey("key:/" + string(id)),
			DisplayPath: "/" + string(id),
			Title:       string(id),
			SessionIDs:  []session.SessionID{},
			CreatedAt:   at,
			UpdatedAt:   at,
		})
	}
	// 一份还没 bootstrap 过的次序：这样下一次打开会对着这两条记录重跑。
	seedState(t, ctx, registry, DomainState{Initialized: false, WorkspaceIDs: []WorkspaceID{}})
	h.close(ctx)

	// 一条会话头都没有：两条记录都落不进任何一组，时刻只能取记录自己的建时刻。
	h.persistence.set()
	registry = h.open(ctx)
	if got := workspaceIDs(mustList(t, ctx, registry)); len(got) != 2 || got[0] != "ws-a" || got[1] != "ws-b" {
		t.Fatalf("该按 id 定序，拿到 %v", got)
	}
}

// -- 域在登记册底下被关掉 --------------------------------------------------

// closeDomainUnderneath 直接关掉登记册手里那个域，但**不动** started 标记。
//
// 这造出的是一个自认为开着、手里却握着一个死域的登记册。从公开面造不出来，
// 但它是「拿着过期句柄读到一张空表」那类静默故障的唯一防线，必须钉住。
func closeDomainUnderneath(t *testing.T, ctx context.Context, registry *Registry) {
	t.Helper()
	if err := registry.dom.Close(ctx); err != nil {
		t.Fatalf("关域不该失败：%v", err)
	}
}

func TestReadPaths域在底下关掉之后一律报存储失败而不是读到一张空表(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	created := mustCreate(t, ctx, registry, "/a", "")
	state, err := registry.readState(ctx)
	if err != nil {
		t.Fatalf("读全局状态不该失败：%v", err)
	}
	closeDomainUnderneath(t, ctx, registry)

	t.Run("状态校验里读记录", func(t *testing.T) {
		if err := registry.validateStoredState(ctx, state); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	t.Run("状态校验里读整张表", func(t *testing.T) {
		// 次序为空时前一段的逐条读跳过，第一次真正的读是整张表那一次。
		if err := registry.validateStoredState(ctx, DomainState{}); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	t.Run("读全局状态", func(t *testing.T) {
		// 新增: 这一格原来是「重建实体缓存」。缓存删掉之后（见 [Registry]）
		// 那一步没了，取而代之的是每次操作开头那次读全局状态——
		// 它是域死掉之后**第一个**该失败的地方。
		if _, err := registry.readState(ctx); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	t.Run("按目标标识找工作区", func(t *testing.T) {
		// 新增: 这一问原来读的是实体缓存，从不失败；现在它是一次整表读。
		if _, _, err := registry.entityByTargetKey(ctx, "key:/a"); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	t.Run("bootstrap 读整张表", func(t *testing.T) {
		if err := registry.bootstrap(ctx, nil); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	t.Run("排 bootstrap 次序", func(t *testing.T) {
		if _, err := registry.bootstrapOrder(ctx, registry.workspace, nil, nil); !errors.Is(err, CodeStorageFailed) {
			t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
		}
	})
	_ = created
}

// -- 每一条内部写路径各自守着的那道「还没启动」 ----------------------------

func TestNotStarted关掉之后每一条内部路径都各自守着(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)
	target, err := h.filesystem.Resolve(ctx, "/a", "")
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	h.close(ctx)

	// 公开面进不来这些函数（[Registry.enqueue] 在最外面就挡住了），
	// 但每一条内部路径**自己**也必须挡一道：少了任何一道，一次时序上的巧合
	// 就能让一个已经关掉的域被写进去。
	t.Run("建工作区", func(t *testing.T) {
		if _, err := registry.createCanonical(ctx, target, ""); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("删工作区", func(t *testing.T) {
		// 新增: 这一格原来要先往缓存里放一条记录才走得到取表那一步。
		// 缓存删掉之后「这个 id 认不认识」本身就是一次读表，第一步就该挡住。
		if _, err := registry.deleteKnown(ctx, "ws-1"); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("收尾待恢复标记", func(t *testing.T) {
		// 新增: 这一格原来要先按一个标记进内存那份状态。状态搬回介质之后
		// 这一步的第一个动作就是读它，读不到就报没启动——摆不摆标记都一样。
		if err := registry.recoverPendingMutation(ctx); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("bootstrap", func(t *testing.T) {
		if err := registry.bootstrap(ctx, nil); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("状态校验", func(t *testing.T) {
		if err := registry.validateStoredState(ctx, DomainState{}); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("读全局状态", func(t *testing.T) {
		if _, err := registry.readState(ctx); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
	t.Run("写全局状态", func(t *testing.T) {
		if err := registry.setState(ctx, DomainState{}); !errors.Is(err, CodeNotStarted) {
			t.Fatalf("要的是 CodeNotStarted，拿到 %v", err)
		}
	})
}

// -- 表与次序对不上的那几处 ------------------------------------------------

// 新增: 这一段原来还有一条 TestRebuildEntities次序指向一条不存在的记录时报不一致。
// 那次重建随实体缓存一起没了（见 [Registry]），而它钉的那条结论——「次序点着一条
// 表里没有的记录就报 [CodeInconsistentState]」——由 [Registry.validateStoredState]
// 接着钉，见下面这一条。

func TestValidateStoredState次序指向一条不存在的记录时报不一致(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)

	err := registry.validateStoredState(ctx, DomainState{WorkspaceIDs: []WorkspaceID{"幽灵"}})
	if !errors.Is(err, CodeInconsistentState) {
		t.Fatalf("要的是 CodeInconsistentState，拿到 %v", err)
	}
}

func TestReportFilteredCandidates表里一条候选都没有时什么都不报(t *testing.T) {
	ctx := t.Context()
	sink := &logSink{}
	h := newHarness(t)
	h.logger = sink.logger()
	registry := h.open(ctx)

	// 这是一个纯诊断投影，一张空表上它不该崩掉，也不该乱报。
	//
	// 新增: 这一条原来往内存次序里塞一个查不到实体的幽灵 id，压的是「次序里没有
	// 实体的那一条直接跳过」。次序不再参与这次诊断了——它现在直接扫整张表
	// （见 [Registry.reportFilteredCandidates]），一个只在次序里的 id 根本进不来。
	registry.reportFilteredCandidates(ctx)
	if len(sink.dump()) != 0 {
		t.Fatalf("不该报任何东西，拿到 %v", sink.dump())
	}
}

func TestEntityByTargetKey认得还没进次序的那一条(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)

	// 建工作区的三步之间，记录已经在表里、但还没进次序。这一段窗口里
	// 按目标标识找必须也找得到它，否则同一个目录会被建出第二个工作区。
	record := goodRecord()
	seedRecord(t, ctx, registry, "ws-在途", record)

	found, ok, err := registry.entityByTargetKey(ctx, record.TargetKey)
	if err != nil {
		t.Fatalf("按目标标识找不该失败：%v", err)
	}
	if !ok {
		t.Fatal("次序里还没有的那一条也该找得到")
	}
	if found.id != "ws-在途" {
		t.Fatalf("拿到 %q", found.id)
	}
	if _, ok, err := registry.entityByTargetKey(ctx, "key:谁也不是"); err != nil || ok {
		t.Fatalf("不认识的目标标识不该找出东西：%v %v", ok, err)
	}
}

// -- 挪位落盘失败 ----------------------------------------------------------

func TestInsertBefore次序写不下去时报存储失败(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	h.filesystem.addDir("/b")
	registry := h.open(ctx)
	first := mustCreate(t, ctx, registry, "/a", "")
	second := mustCreate(t, ctx, registry, "/b", "")

	before := workspaceIDs(mustList(t, ctx, registry))
	h.backend.armGlobal(map[int]error{1: errBackend})
	if _, err := registry.InsertBefore(ctx, second.ID(), ""); !errors.Is(err, CodeStorageFailed) {
		t.Fatalf("要的是 CodeStorageFailed，拿到 %v", err)
	}
	h.backend.armGlobal(nil)
	if got := workspaceIDs(mustList(t, ctx, registry)); !equalIDs(got, before) {
		t.Fatalf("写不下去的挪位不该改内存里的次序，从 %v 变成了 %v", before, got)
	}
	_ = first
}

// equalIDs 比两串工作区 id。
func equalIDs(left, right []WorkspaceID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// -- 域声明 ----------------------------------------------------------------

func TestSpec表名和域名在错误文本里认得出来(t *testing.T) {
	// 这三个常量一起构成「这份介质是不是本包写的」那个判断，改动它们等于换格式。
	if DomainName != "workspace" || TableName != "workspaces" || DomainVersion != 2 {
		t.Fatalf("域的身份被改了：%q/%q/%d", DomainName, TableName, DomainVersion)
	}
	var spec domain.Spec = Spec()
	if !strings.Contains(spec.Name, "workspace") {
		t.Fatalf("拿到 %q", spec.Name)
	}
}
