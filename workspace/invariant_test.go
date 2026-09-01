// 本文件的作用：「实体缓存就是落盘表的镜像」这条检查——它拦得住什么、
// 什么时候不该拦、以及注销之后订阅确实跟着摘掉了。
//
// 这条检查的观察点在域的写路径上，而 [invariants.Fail] 是 panic，
// 所以这里的用例都要在 [domain.Table.Put] / [domain.Table.Delete] 外面接住那次 panic。

package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// mustInvariants 造一个全放行的不变量注册表。
func mustInvariants(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建不变量注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// catchViolation 跑 fn 并把它抛出来的不变量违例接回来；没抛就返回 nil。
//
// 接 panic 而不是让它炸掉用例，是因为「违例确实抛了」正是要断言的东西：
// 一条不抛的不变量和没有这条不变量是一回事。
func catchViolation(t *testing.T, fn func()) (violation *invariants.Error) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err, ok := recovered.(error)
		if !ok {
			panic(recovered)
		}
		var coded *invariants.Error
		if !errors.As(err, &coded) {
			panic(recovered)
		}
		violation = coded
	}()
	fn()
	return nil
}

func TestRegisterInvariants缺了参数就报配置错误(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	registry := h.open(ctx)
	checks := mustInvariants(t)
	live := func() bool { return true }

	cases := []struct {
		name string
		call func() (func(), error)
	}{
		{"没有不变量注册表", func() (func(), error) {
			return RegisterInvariants(ctx, nil, h.facility, registry, live)
		}},
		{"没有域设施", func() (func(), error) {
			return RegisterInvariants(ctx, checks, nil, registry, live)
		}},
		{"没有工作区登记册", func() (func(), error) {
			return RegisterInvariants(ctx, checks, h.facility, nil, live)
		}},
		{"没有存活判据", func() (func(), error) {
			return RegisterInvariants(ctx, checks, h.facility, registry, nil)
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			unregister, err := one.call()
			if !errors.Is(err, CodeInvalidConfig) {
				t.Fatalf("要的是 CodeInvalidConfig，拿到 %v", err)
			}
			if unregister != nil {
				t.Fatal("失败的注册不该交出注销函数")
			}
		})
	}
}

// invariantHarness 是一套装好了这条检查的夹具：登记册、不变量注册表、
// 一张直连的 workspaces 表（用来扮演「绕过登记册的写入路径」）。
type invariantHarness struct {
	h          *harness
	registry   *Registry
	table      *domain.Table[Record]
	unregister func()
	live       bool
}

func newInvariantHarness(t *testing.T, ctx context.Context) *invariantHarness {
	t.Helper()
	h := newHarness(t)
	h.filesystem.addDir("/a")
	registry := h.open(ctx)

	// 直接拿登记册手里那张表来写，绕开它的公开面——这就是这条检查要抓的那条路径。
	//
	// 一个域只能打开一次，所以扮不了「另一个持有者」；但那本来也不是重点：
	// 这条检查看的是**表被写了而缓存没跟上**，谁写的无所谓。
	fixture := &invariantHarness{h: h, registry: registry, table: registry.workspace, live: true}
	unregister, err := RegisterInvariants(
		ctx,
		mustInvariants(t),
		h.facility,
		registry,
		func() bool { return fixture.live },
	)
	if err != nil {
		t.Fatalf("注册不变量不该失败：%v", err)
	}
	fixture.unregister = unregister
	t.Cleanup(unregister)
	return fixture
}

func TestRegisterInvariants绕过登记册写记录时抓住(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)

	violation := catchViolation(t, func() {
		_ = fixture.table.Put(ctx, "谁写的", goodRecord())
	})
	if violation == nil {
		t.Fatal("缓存里没有这个实体，这一次写该被拦住")
	}
	if violation.PackageName != PackageName {
		t.Fatalf("归属该是 %q，拿到 %q", PackageName, violation.PackageName)
	}
	if !strings.Contains(violation.Message, "谁写的") {
		t.Fatalf("这句话该点出是哪条记录，拿到 %q", violation.Message)
	}
}

func TestRegisterInvariants绕过登记册删记录时抓住(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)
	created := mustCreate(t, ctx, fixture.registry, "/a", "")

	// 经登记册建出来的那一条，缓存里有、表里也有。直接从表上删掉它：
	// 记录没了而缓存还在发布它，这就是分叉。
	violation := catchViolation(t, func() {
		_, _ = fixture.table.Delete(ctx, string(created.ID()))
	})
	if violation == nil {
		t.Fatal("缓存里还留着这个实体，这一次删该被拦住")
	}
	if !strings.Contains(violation.Message, string(created.ID())) {
		t.Fatalf("这句话该点出是哪条记录，拿到 %q", violation.Message)
	}
}

func TestRegisterInvariants走登记册的写不触发(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)

	// 建、改、删走的都是登记册自己的写路径，次序上缓存和表始终对得上。
	violation := catchViolation(t, func() {
		created := mustCreate(t, ctx, fixture.registry, "/a", "甲")
		if err := created.SetTitle(ctx, "乙"); err != nil {
			t.Fatalf("改标题不该失败：%v", err)
		}
		if _, err := fixture.registry.Delete(ctx, created.ID()); err != nil {
			t.Fatalf("删除不该失败：%v", err)
		}
	})
	if violation != nil {
		t.Fatalf("登记册自己的写不该触发违例：%v", violation)
	}
}

func TestRegisterInvariants登记册不活着时不查(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)

	// 「还挂着吗」由装配方说了算：说不挂了，这条检查就该整个让开。
	// 少了这一条，一次正常的停机顺序（先摘登记册再关域）会被自己的不变量拦住。
	fixture.live = false
	violation := catchViolation(t, func() {
		_ = fixture.table.Put(ctx, "谁写的", goodRecord())
	})
	if violation != nil {
		t.Fatalf("登记册不活着时不该再查：%v", violation)
	}
}

func TestRegisterInvariants注销之后订阅跟着摘掉(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)

	fixture.unregister()

	violation := catchViolation(t, func() {
		_ = fixture.table.Put(ctx, "谁写的", goodRecord())
	})
	if violation != nil {
		t.Fatalf("注销之后这条检查不该还在写路径上抛：%v", violation)
	}
}

func TestRegisterInvariants不管别的域和别的表(t *testing.T) {
	ctx := t.Context()
	fixture := newInvariantHarness(t, ctx)

	// 换一个域名的写事件不该被这条检查看见——它只认自己那张表。
	other, err := fixture.h.facility.Open(ctx, domain.Spec{
		Name:    "other",
		Version: 1,
		Tables: []domain.TableSpec{
			domain.DefineTable("things", func(Record) error { return nil }),
		},
	})
	if err != nil {
		t.Fatalf("打开另一个域不该失败：%v", err)
	}
	table, err := domain.TableOf[Record](other, "things")
	if err != nil {
		t.Fatalf("取表不该失败：%v", err)
	}

	violation := catchViolation(t, func() {
		if err := table.Put(ctx, "随便一条", goodRecord()); err != nil {
			t.Fatalf("写另一个域不该失败：%v", err)
		}
	})
	if violation != nil {
		t.Fatalf("别的域的写不该触发违例：%v", violation)
	}
}
