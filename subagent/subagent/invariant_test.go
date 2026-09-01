// 本文件验这个包自己那两条运行期不变量：提供方注册表的收支平衡，和 start／end
// 那对生命周期边的配对。
//
// 源: packages/subagent/subagent/src/invariant.ts
//
// 检查是**panic**着报的（见 [github.com/snight1983/ds-harness-go/invariants.Fail]），所以每一条用例都
// 用 mustFail／mustNotFail 把那次 panic 接住再断言。正常代码路径违反不了它们，
// 于是绝大多数用例直接往那份检查上发捏造的边——白盒手法，同包测试拿得到。

package subagent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
)

// newRegistry 造一条全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// armed 给一台服务装上这份检查，并把它那份状态交出来直接发边。
func armed(t *testing.T, runtime *Runtime) *lifecycleInvariant {
	t.Helper()
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), runtime)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}
	t.Cleanup(dispose)
	check := runtime.emitLifecycle.check.Load()
	if check == nil {
		t.Fatal("装完之后该留下一份检查")
	}
	return check
}

// mustFail 断言 act 报了一次违例，并把那句话交回来。
func mustFail(t *testing.T, act func()) string {
	t.Helper()
	var reported string
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("该报一次违例")
			}
			reported = fmt.Sprint(recovered)
		}()
		act()
	}()
	return reported
}

// mustNotFail 断言 act 一声不响。
func mustNotFail(t *testing.T, act func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("这条路不该报违例，实际 %v", recovered)
		}
	}()
	act()
}

func TestRegisterInvariantsNeedsARegistryAndARuntime(t *testing.T) {
	runtime := newRuntime(t)
	if _, err := RegisterInvariants(t.Context(), nil, runtime); err == nil {
		t.Error("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), nil); err == nil {
		t.Error("没有运行时该装不上")
	}
}

// 这份检查可以装在若干次登记之后，那些提供方是既成事实，不是违例。
func TestRegisterInvariantsSeedsTheExistingRegistry(t *testing.T) {
	runtime := newRuntime(t)
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn"})
	check := armed(t, runtime)

	// 已经在表里的名字再登记一次才是违例；摘掉它不是。
	mustNotFail(t, func() { check.providerRemoved("spawn") })
}

// 注销之后那份检查必须停下来，否则它会继续在别人的登记路径上抛。
func TestRegisterInvariantsStopsCheckingAfterRelease(t *testing.T) {
	runtime := newRuntime(t)
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), runtime)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}
	dispose()

	if runtime.emitLifecycle.check.Load() != nil {
		t.Fatal("注销之后不该再留着那份检查")
	}
	// 一条本来必定违例的边这时候什么都不该发生。
	mustNotFail(t, func() { runtime.emitLifecycle.emitProviderRemoved("从来没有过") })
}

// ---- 提供方注册表的收支平衡 ----

// 正常路径一声不响，这是下面那些用例的地基。
func TestProviderInvariantIsQuietOnTheNormalPath(t *testing.T) {
	runtime := newRuntime(t)
	armed(t, runtime)
	owner := rootScope(t)

	mustNotFail(t, func() {
		dispose := register(t, runtime, owner, &fakeProvider{name: "spawn"})
		if err := dispose(t.Context()); err != nil {
			t.Fatalf("撤销登记失败：%v", err)
		}
		register(t, runtime, owner, &fakeProvider{name: "spawn"})
	})
}

func TestProviderInvariantRejectsAnEmptyName(t *testing.T) {
	check := armed(t, newRuntime(t))
	reported := mustFail(t, func() { check.providerAdded(&fakeProvider{name: ""}) })
	if !strings.Contains(reported, "non-empty") {
		t.Fatalf("该报名字不能为空，实际 %q", reported)
	}
}

func TestProviderInvariantRejectsARepeatedAddition(t *testing.T) {
	check := armed(t, newRuntime(t))
	check.providerAdded(&fakeProvider{name: "spawn"})
	reported := mustFail(t, func() { check.providerAdded(&fakeProvider{name: "spawn"}) })
	if !strings.Contains(reported, "repeated") {
		t.Fatalf("该报重复登记，实际 %q", reported)
	}
}

// 一条摘除边报出一个陌生的名字，说明某次登记的回滚跑了两遍。
func TestProviderInvariantRejectsAnUnknownRemoval(t *testing.T) {
	check := armed(t, newRuntime(t))
	reported := mustFail(t, func() { check.providerRemoved("从来没有过") })
	if !strings.Contains(reported, "unknown provider") {
		t.Fatalf("该报摘了个陌生名字，实际 %q", reported)
	}
}

// 一次失败的登记会卷回去并发一条摘除边，那对边必须收支平衡。
func TestProviderInvariantToleratesAFailedRegistration(t *testing.T) {
	runtime := newRuntime(t)
	armed(t, runtime)
	owner := rootScope(t)
	release, err := runtime.OnProviderAdded(t.Context(), owner, func(Provider) error {
		return errors.New("不收")
	})
	if err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	mustNotFail(t, func() {
		if _, err := runtime.RegisterProvider(t.Context(), owner, &fakeProvider{name: "spawn"}); err == nil {
			t.Fatal("这次登记该失败")
		}
	})
	if err := release(t.Context()); err != nil {
		t.Fatalf("撤销观察者失败：%v", err)
	}
	// 卷回去之后这个名字该是干净的，可以重新登记。
	mustNotFail(t, func() { register(t, runtime, owner, &fakeProvider{name: "spawn"}) })
}

// ---- start／end 配对 ----

func TestRunInvariantIsQuietOnAPairedRun(t *testing.T) {
	check := armed(t, newRuntime(t))
	info := RunInfo{RunID: "r1", Provider: "spawn", ID: "child", Local: true}
	mustNotFail(t, func() {
		check.runStarted(info)
		check.runEnded(RunEndInfo{
			RunID: info.RunID, Provider: info.Provider, ID: info.ID, Local: info.Local,
			StopReason: StopCompleted,
		})
	})
	// 结清之后那个运行 id 就腾出来了，可以再开一次。
	mustNotFail(t, func() { check.runStarted(info) })
}

func TestRunInvariantRejectsIncompleteStartIdentities(t *testing.T) {
	for name, info := range map[string]RunInfo{
		"缺提供方":   {RunID: "r1", ID: "child"},
		"缺运行 id": {Provider: "spawn", ID: "child"},
		"缺孩子 id": {RunID: "r1", Provider: "spawn"},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, newRuntime(t))
			reported := mustFail(t, func() { check.runStarted(info) })
			if !strings.Contains(reported, "non-empty") {
				t.Fatalf("该报三样都不能为空，实际 %q", reported)
			}
		})
	}
}

func TestRunInvariantRejectsARepeatedRunID(t *testing.T) {
	check := armed(t, newRuntime(t))
	info := RunInfo{RunID: "r1", Provider: "spawn", ID: "child"}
	check.runStarted(info)
	reported := mustFail(t, func() { check.runStarted(info) })
	if !strings.Contains(reported, "repeated run id") {
		t.Fatalf("该报运行 id 重开，实际 %q", reported)
	}
}

func TestRunInvariantRejectsAnUnpairedEnd(t *testing.T) {
	check := armed(t, newRuntime(t))
	reported := mustFail(t, func() {
		check.runEnded(RunEndInfo{RunID: "r1", Provider: "spawn", ID: "child"})
	})
	if !strings.Contains(reported, "no matching") {
		t.Fatalf("该报配不上任何 start，实际 %q", reported)
	}
}

// 两边说的提供方、孩子和「是不是同进程」必须一致。
func TestRunInvariantRejectsADivergentEndIdentity(t *testing.T) {
	start := RunInfo{RunID: "r1", Provider: "spawn", ID: "child", Local: true}
	for name, end := range map[string]RunEndInfo{
		"提供方对不上": {RunID: "r1", Provider: "fork", ID: "child", Local: true},
		"孩子对不上":  {RunID: "r1", Provider: "spawn", ID: "别人", Local: true},
		"同进程对不上": {RunID: "r1", Provider: "spawn", ID: "child", Local: false},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, newRuntime(t))
			check.runStarted(start)
			reported := mustFail(t, func() { check.runEnded(end) })
			if !strings.Contains(reported, "diverges") {
				t.Fatalf("该报身份对不上，实际 %q", reported)
			}
		})
	}
}

// 这条检查**不**查「这个提供方还在不在注册表里」：一次已发布的运行可以活得比它的
// 提供方长，一次冷恢复出来的活化更是压根不经过它派发。
func TestRunInvariantIgnoresWhetherTheProviderIsStillRegistered(t *testing.T) {
	check := armed(t, newRuntime(t))
	mustNotFail(t, func() {
		check.runStarted(RunInfo{RunID: "r1", Provider: "从来没登记过", ID: "child"})
	})
}

// 一次真的运行走完整条路——发射器那个专属调用点跑在兜底观察者之前，所以违例
// 不会被吞成一行日志。
func TestRunInvariantWatchesRealDispatches(t *testing.T) {
	runtime := newRuntime(t)
	armed(t, runtime)
	// 一条捏造的、配不上任何 start 的终止边穿过发射器时必须抛出来。
	reported := mustFail(t, func() {
		runtime.emitLifecycle.emitEnd(RunEndInfo{RunID: "凭空", ID: "child"}, nil)
	})
	if !strings.Contains(reported, "no matching") {
		t.Fatalf("该报配不上任何 start，实际 %q", reported)
	}
}
