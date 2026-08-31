// 本文件验这个包自己那条运行期不变量：它在什么时候响、在什么时候不响。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// 它查的是「提交事件的生命周期约定」——一次已提交的引用变更只可能发生在一个
// 活着的凭据服务上。正常代码路径永远不会违反它，所以用例是直接在通知器上
// 发一条**捏造的**通知，而不是绕一次真的 Set。这是白盒手法，同包测试拿得到。

package credentials

import (
	"strings"
	"testing"

	"ds-harness-go/invariants"
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

// arm 装上这条检查，并返回一个「服务还活着吗」的开关。
func arm(t *testing.T, provider Provider) *bool {
	t.Helper()

	live := true
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), provider, func() bool { return live })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
	return &live
}

// TestRegisterInvariantsRequiresAllThreePieces 钉住装配方必须把三样都递进来。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// DSH 那边这三样都由 cordis 从上下文里取（invariant.ts:21 的 inject 和 apply 的 ctx），
// 装配方什么都不用写。Go 里没有容器，三样都是显式入参，缺一个就装不上。
// 「服务还活着吗」尤其只能由装配方给——它是那个 New 出提供方、也负责关掉它的人。
func TestRegisterInvariantsRequiresAllThreePieces(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	registry := newRegistry(t)
	alive := func() bool { return true }

	if _, err := RegisterInvariants(t.Context(), nil, provider, alive); err == nil {
		t.Error("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), registry, nil, alive); err == nil {
		t.Error("没有可订阅的提供方该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), registry, provider, nil); err == nil {
		t.Error("没有存活判据该装不上")
	}
}

// TestARealWriteSatisfiesTheInvariant 钉住正常路径一声不响。
//
// 这条用例是下一条的地基：一条永远会响的检查抓到什么都不说明问题。
func TestARealWriteSatisfiesTheInvariant(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	arm(t, provider)

	if err := provider.Set(t.Context(), Ref("DEEPSEEK_API_KEY"), "sk-live"); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if err := provider.Unset(t.Context(), Ref("DEEPSEEK_API_KEY")); err != nil {
		t.Fatalf("删不该失败：%v", err)
	}
}

// TestANotificationAfterTheServiceIsGoneIsAViolation 钉住这条检查本身。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// 事件陈述的是一次已经提交的变更，服务都没了就没有「提交进了哪里」可言——
// 说明某个提供方把工作漏到了自己的收尾静默期之后（见 [RegisterInvariants]）。
//
// 违例是**重新抛出**的（见 [Notifier.fanOut] 的第 3 条规则），所以它到得了发起方
// 手里；这里顺带也是那条重抛规则的证据。
func TestANotificationAfterTheServiceIsGoneIsAViolation(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	live := arm(t, provider)

	*live = false

	recovered := func() (thrown any) {
		defer func() { thrown = recover() }()
		provider.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))
		return nil
	}()

	failure, ok := recovered.(*invariants.Error)
	if !ok {
		t.Fatalf("该抛出 *invariants.Error，实际 %#v", recovered)
	}
	if failure.PackageName != PackageName {
		t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
	}
	if !strings.Contains(failure.Message, "凭据服务已经不在了") {
		t.Errorf("那句话该点明服务没了：%q", failure.Message)
	}
	if !strings.Contains(failure.Message, "DEEPSEEK_API_KEY") {
		t.Errorf("那句话该点明是哪个引用：%q", failure.Message)
	}
}

// TestTheRecordKeySpaceCarriesNoSuchCheck 钉住记录那一半**故意**没有这条检查。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// DSH 只在引用那一半装了（invariant.ts 里只订阅 credentials/reference-updated），
// Go 照此实现。钉一条的理由是：这是一处「有意的不对称」，而不对称最容易在下一次
// 「顺手补全」时被抹平——抹平之后这个包就开始为一件 DSH 没有主张过的事负责。
func TestTheRecordKeySpaceCarriesNoSuchCheck(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	live := arm(t, provider)

	*live = false
	// 服务已经没了，记录那一半照样一声不响地过去。
	provider.NotifyRecordUpdated(Key("llm-pi-ai/openai-codex"))
}

// TestUnregisteringAlsoDropsTheSubscription 钉住注销把订阅一起摘掉。
//
// 源: packages/credentials/credentials/src/invariant.ts:16-38
//
// 摘不掉的话，一条不该再查的检查会继续在**别人**的提交路径上抛。
func TestUnregisteringAlsoDropsTheSubscription(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	live := true
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), provider, func() bool { return live })
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}

	dispose()
	live = false

	// 注销之后，同一条本该违例的通知一声不响地过去。
	provider.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))
}
