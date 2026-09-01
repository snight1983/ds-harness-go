// 本文件验这个包自己那条运行期不变量：四条检查各自会在什么时候响。
//
// 源: packages/settings/settings/tests/invariant.spec.ts:18-51

package settings

import (
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
)

// newRegistry 造一条全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// sabotage 在这条检查**之前**插一个订阅者，让它在提交路径上动服务的内部状态。
//
// 这是白盒手法，理由是：四条检查里有三条查的是「通知和状态分了叉」，
// 而正常代码路径永远不会让它们分叉——那正是这条不变量存在的意义。
// 从外面制造分叉只有两条路：要么在订阅者里回头写（[Provider.commitMutex] 会死锁），
// 要么就是这里做的，直接把状态改成分叉的样子。同包测试拿得到这个手法，就用它。
//
// 次序靠的是订阅表是切片：先订阅的先跑（见 [Provider.commit]）。
func sabotage(t *testing.T, p *Provider, tamper func(entry *registration, next, prev map[string]any)) {
	t.Helper()

	t.Cleanup(p.SubscribeUpdated(func(ns Namespace, next, prev map[string]any, _ Source) {
		p.mutex.Lock()
		entry := p.registrations[ns]
		p.mutex.Unlock()
		tamper(entry, next, prev)
	}))
}

// arm 装上这条检查，并返回一个「服务还活着吗」的开关。
func arm(t *testing.T, p *Provider) *bool {
	t.Helper()

	live := true
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), p, func() bool { return live })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
	return &live
}

// expectViolation 跑一次写，要求它以一条不变量违例 panic，且文案里带某个片段。
//
// 违例是**重新抛出**的（见 [fanOut]），所以它到得了发起方手里——
// 这条断言同时也是「重抛」那条规则的证据。
func expectViolation(t *testing.T, fragment string, write func()) {
	t.Helper()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("该抛出不变量违例，实际什么都没抛")
		}
		failure, isInvariant := recovered.(*invariants.Error)
		if !isInvariant {
			t.Fatalf("抛出来的该是 *invariants.Error，实际 %#v", recovered)
		}
		if failure.PackageName != PackageName {
			t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
		}
		if !strings.Contains(failure.Message, fragment) {
			t.Errorf("违例文案里该有 %q，实际 %q", fragment, failure.Message)
		}
	}()

	write()
}

// TestRegisterInvariantsNeedsAllThreeCollaborators 钉住三个参数一个都不能省。
//
// 三件事分开报，因为它们各自缺席的修法不一样：没注册表是装配漏了一行，
// 没服务是装配次序错了，没 live 是装配方没想清楚谁负责这个服务的生命周期。
func TestRegisterInvariantsNeedsAllThreeCollaborators(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	registry := newRegistry(t)

	if _, err := RegisterInvariants(t.Context(), nil, provider, func() bool { return true }); err == nil {
		t.Error("没有注册表该失败")
	}
	if _, err := RegisterInvariants(t.Context(), registry, nil, func() bool { return true }); err == nil {
		t.Error("没有服务该失败")
	}
	if _, err := RegisterInvariants(t.Context(), registry, provider, nil); err == nil {
		t.Error("没有 live 判据该失败")
	}
}

// TestRegisterInvariantsSurfacesARegistryRefusal 钉住注册表的拒绝会原样带出来。
//
// 同一个包名注册两次是装配写重了。静默接受的话，一次提交会抛两条一模一样的违例，
// 而排查的人会以为是两个不同的问题。
func TestRegisterInvariantsSurfacesARegistryRefusal(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	registry := newRegistry(t)
	live := func() bool { return true }

	dispose, err := RegisterInvariants(t.Context(), registry, provider, live)
	if err != nil {
		t.Fatalf("第一次装不该失败：%v", err)
	}
	t.Cleanup(dispose)

	if _, err := RegisterInvariants(t.Context(), registry, provider, live); !errors.Is(err, invariants.ErrAlreadyRegistered) {
		t.Fatalf("该报 ErrAlreadyRegistered，实际 %v", err)
	}
}

// TestInvariantStaysQuietOnAnHonestCommit 钉住一次老老实实的提交不会响。
//
// 这条先立着：后面四条制造的都是「分叉」，而分叉之所以能被认出来，
// 前提是不分叉的时候这条检查一声不吭。
func TestInvariantStaysQuietOnAnHonestCommit(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)
	arm(t, provider)

	if err := scope.Update(t.Context(), map[string]any{"label": "改一下"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if scope.Get().Label != "改一下" {
		t.Fatalf("值该改过来了，实际 %q", scope.Get().Label)
	}
}

// TestInvariantCatchesACommitAfterTheServiceIsGone 钉住第一条检查。
//
// 源: packages/settings/settings/tests/invariant.spec.ts:18-27
//
// 服务都拆了还宣布「已提交」，那就没有「提交进哪里」可言。
func TestInvariantCatchesACommitAfterTheServiceIsGone(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)
	live := arm(t, provider)
	*live = false

	expectViolation(t, "已经不在了", func() {
		_ = scope.Update(t.Context(), map[string]any{"label": "拆了之后"})
	})
}

// TestInvariantCatchesACommitForAnUnregisteredNamespace 钉住第二条检查。
//
// 一个已经注销的命名空间不该再有人替它宣布变更：它的拥有者已经走了，
// 这条通知没有收件人，却会让每一个通用订阅者（配置界面、审计）当成真事记下来。
func TestInvariantCatchesACommitForAnUnregisteredNamespace(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)
	sabotage(t, provider, func(_ *registration, _, _ map[string]any) {
		provider.mutex.Lock()
		delete(provider.registrations, "core")
		provider.mutex.Unlock()
	})
	arm(t, provider)

	expectViolation(t, "不是登记着的", func() {
		_ = scope.Update(t.Context(), map[string]any{"label": "注销之后"})
	})
}

// TestInvariantCatchesANotificationThatDisagreesWithTheAuthoritativeValue 钉住第三条检查。
//
// 对不上意味着通知和状态分了叉：收到通知的那一方照着通知里的值走，
// 而后来读服务的人拿到的是另一个值——两边从此各说各话，且谁都不会发现。
func TestInvariantCatchesANotificationThatDisagreesWithTheAuthoritativeValue(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)
	sabotage(t, provider, func(entry *registration, _, _ map[string]any) {
		provider.mutex.Lock()
		entry.raw = map[string]any{"label": "第三个值"}
		provider.mutex.Unlock()
	})
	arm(t, provider)

	expectViolation(t, "对不上", func() {
		_ = scope.Update(t.Context(), map[string]any{"label": "通知里的值"})
	})
}

// TestInvariantCatchesANotificationThatChangedNothing 钉住第四条检查。
//
// 制造手法是就地把「变更前」改成和「变更后」一样：[Provider.commit] 在值没变时
// 压根不会走到通知那一步（另有用例钉），所以这一条只能从订阅者这一侧造出来。
//
// 它要拦的事是：一次「其实没变」的通知让每个观察者白跑一遍，
// 更糟的是把「变更」这个词的含义稀释掉——真变的那次就没人当回事了。
func TestInvariantCatchesANotificationThatChangedNothing(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Label: "原值"}, nil)
	sabotage(t, provider, func(_ *registration, next, prev map[string]any) {
		for key := range prev {
			delete(prev, key)
		}
		for key, value := range next {
			prev[key] = value
		}
	})
	arm(t, provider)

	expectViolation(t, "其实没变", func() {
		_ = scope.Update(t.Context(), map[string]any{"label": "看着变了"})
	})
}

// TestInvariantStopsCheckingAfterItIsDisposed 钉住注销之后这条检查真的摘干净了。
//
// 退订是登记进 [invariants.Scope] 的。漏摘的话，一条已经不该再查的检查
// 会继续在别人的提交路径上抛，而抛出来的包名指向一个早就注销了的注册。
func TestInvariantStopsCheckingAfterItIsDisposed(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	live := true
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), provider, func() bool { return live })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	dispose()
	live = false // 摘掉之后，这个开关不该再有任何作用。

	if err := scope.Update(t.Context(), map[string]any{"label": "摘掉之后"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
}
