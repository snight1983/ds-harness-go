// 本文件验这个包自己那条运行期不变量：文件系统事件带的身份得是能用的。
//
// 源: packages/fs/fs/tests/invariant.spec.ts:1-55
//
// DSH 那边是往容器上 emit 事件、看抛不抛。Go 这边没有容器，检查落在 [Policy]
// 的三个分发方法上，所以用例就直接调那三个方法。这不是白盒手法——那三个方法
// 本来就是这条接缝对外的分发入口。

package fs

import (
	"strings"
	"testing"

	"ds-harness-go/invariants"
)

// armed 造一条全开的注册表，把检查装到 policy 上，并在用例结束时注销。
func armed(t *testing.T, policy *Policy) {
	t.Helper()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	dispose, err := RegisterInvariants(t.Context(), registry, policy)
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
}

// violation 跑一次分发，把违例接下来。没有违例时返回 nil。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func violation(t *testing.T, dispatch func()) *invariants.Error {
	t.Helper()

	var thrown any
	func() {
		defer func() { thrown = recover() }()
		dispatch()
	}()

	if thrown == nil {
		return nil
	}
	failure, ok := thrown.(*invariants.Error)
	if !ok {
		t.Fatalf("该抛出 *invariants.Error，实际 %#v", thrown)
	}
	if failure.PackageName != PackageName {
		t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
	}
	return failure
}

// requireViolation 断言这次分发违例了，并且那句话点到了 want。
func requireViolation(t *testing.T, want string, dispatch func()) {
	t.Helper()

	failure := violation(t, dispatch)
	if failure == nil {
		t.Fatalf("该违例：%s", want)
	}
	if !strings.Contains(failure.Message, want) {
		t.Errorf("那句话该点明 %q，实际 %q", want, failure.Message)
	}
}

// TestRegisterInvariantsRequiresBothPieces 钉住装配方必须把两样都递进来。
//
// 新增: DSH 那边这两样都由 cordis 从上下文里取（invariant.ts:21 的 inject
// 和 apply 的 ctx），装配方什么都不用写。Go 里没有容器，两样都是显式入参。
// 尤其是 policy：DSH 挂的是容器的全局分发钩子，而 Go 这边必须指名道姓地说
// 「查的是这一条分发路径」——没有它，这条检查根本没有可挂的地方。
func TestRegisterInvariantsRequiresBothPieces(t *testing.T) {
	t.Parallel()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	if _, err := RegisterInvariants(t.Context(), nil, &Policy{}); err == nil {
		t.Error("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), registry, nil); err == nil {
		t.Error("没有策略通道该装不上")
	}
}

// TestUsableIdentitiesPassOnAllThreeChannels 钉住正常路径一声不响。
//
// 源: packages/fs/fs/tests/invariant.spec.ts:21-36
//
// 这条用例是下面那几条的地基：一条永远会响的检查抓到什么都不说明问题。
func TestUsableIdentitiesPassOnAllThreeChannels(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	if _, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil); err != nil {
		t.Fatalf("决定不该失败：%v", err)
	}
	if _, err := policy.DecideEditIntent(t.Context(), aTarget(), nil); err != nil {
		t.Fatalf("决定不该失败：%v", err)
	}
	if err := policy.NotifyObserved(aTarget(), Present{Version: Version("v1")}, nil); err != nil {
		t.Fatalf("观察不该失败：%v", err)
	}
	// 不在场的观察不带版本，那不是缺席，是这一支本来就没有这个字段。
	if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
		t.Fatalf("观察不该失败：%v", err)
	}
}

// TestAnEmptyTargetKeyIsAViolationOnEveryChannel 钉住空 key 在三条路径上都被抓。
//
// 源: packages/fs/fs/tests/invariant.spec.ts:38-47
//
// 空 key 会让每一个目标看上去都是同一个，于是一次陈旧守卫会拿别人的版本来比。
// 三条路径都查，是因为 DSH 那边挂的是全局钩子——它天然覆盖三个事件，
// Go 这边分发方法是分开的，那就得逐个确认没有哪一条漏了这道检查。
func TestAnEmptyTargetKeyIsAViolationOnEveryChannel(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	nameless := Target{DisplayPath: "file.txt"}

	requireViolation(t, "targetKey", func() {
		_, _ = policy.DecideWriteIntent(t.Context(), nameless, nil)
	})
	requireViolation(t, "targetKey", func() {
		_, _ = policy.DecideEditIntent(t.Context(), nameless, nil)
	})
	requireViolation(t, "targetKey", func() {
		_ = policy.NotifyObserved(nameless, Present{Version: Version("v1")}, nil)
	})
}

// TestAnEmptyDisplayPathIsAViolation 钉住展示路径也得有。
//
// 源: packages/fs/fs/tests/invariant.spec.ts:44-47
//
// 它是 [Target] 里唯一能展示给人看的字段。空了之后，一条
// 「拒绝写入 ""」的诊断谁也读不懂——而那正是需要有人读懂的时刻。
func TestAnEmptyDisplayPathIsAViolation(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	requireViolation(t, "displayPath", func() {
		_ = policy.NotifyObserved(Target{TargetKey: TargetKey("file:1")}, Absent{}, nil)
	})
}

// TestAnEmptyPresentVersionIsAViolation 钉住在场观察必须带得动一次守卫。
//
// 源: packages/fs/fs/tests/invariant.spec.ts:48-50
//
// 空版本存进登记之后，后面那次 [ReplaceIfVersion] 拿它去比，
// 比的是一个从来不会相等的东西——于是那次写永远报 [CodeStaleVersion]，
// 而看上去像是别人一直在改这个文件。
func TestAnEmptyPresentVersionIsAViolation(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	requireViolation(t, "版本", func() {
		_ = policy.NotifyObserved(aTarget(), Present{}, nil)
	})
}

// TestANilObservationIsAViolation 钉住那条 DSH 没有、Go 才需要的检查。
//
// 源: packages/fs/fs/src/invariant.ts:27-38
//
// 新增: DSH 在这里查的是「kind 必须是 present 或者 absent」
// （invariant.spec.ts:51-53 那条用例）。Go 侧那一条查不出来也不用查：
// [Observation] 是封印接口，本包外面造不出第三种实现。
// 取而代之的是这一条——TS 那边 undefined 到不了这里（类型上不允许），
// 而 Go 的接口值可以是 nil，一路走到记录方那里才崩。
func TestANilObservationIsAViolation(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	requireViolation(t, "不能是 nil", func() {
		_ = policy.NotifyObserved(aTarget(), nil, nil)
	})
}

// TestTheCheckRunsBeforeAnySubscriberDoes 钉住检查在分发**之前**。
//
// 一个身份残缺的目标绝不该到得了任何一个决定方或记录方手里：
// 到了之后，那个记录方就已经拿一个空 key 当过键了，而登记表里
// 那条错误的记录会一直留着。
func TestTheCheckRunsBeforeAnySubscriberDoes(t *testing.T) {
	t.Parallel()

	var policy Policy
	armed(t, &policy)

	reached := false
	policy.SubscribeObserved(func(Target, Observation, any) error {
		reached = true
		return nil
	})

	requireViolation(t, "targetKey", func() {
		_ = policy.NotifyObserved(Target{DisplayPath: "file.txt"}, Absent{}, nil)
	})
	if reached {
		t.Error("残缺的身份不该到得了记录方手里")
	}
}

// TestUnregisteringStopsTheCheck 钉住注销之后这条检查真的停下来。
//
// 源: packages/fs/fs/src/invariant.ts:20-48
//
// 停不下来的话，一条不该再查的检查会继续在**别人**的分发路径上抛，
// 而那条路径的拥有者从来没装过它。
func TestUnregisteringStopsTheCheck(t *testing.T) {
	t.Parallel()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	var policy Policy
	dispose, err := RegisterInvariants(t.Context(), registry, &policy)
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}

	requireViolation(t, "targetKey", func() {
		_ = policy.NotifyObserved(Target{DisplayPath: "file.txt"}, Absent{}, nil)
	})

	dispose()

	// 同一条本该违例的分发，现在一声不响地过去。
	if failure := violation(t, func() {
		_ = policy.NotifyObserved(Target{DisplayPath: "file.txt"}, nil, nil)
	}); failure != nil {
		t.Errorf("注销之后不该还在查：%s", failure.Message)
	}
}

// TestWithoutTheCheckTheChannelsStayQuiet 钉住没装检查时那三个方法是空操作。
//
// 这条检查是可选装配的：不变量注册表本身就可以整个关掉。没装的时候，
// 分发路径必须完全不受影响——不是「查了但不抛」，而是根本不查。
func TestWithoutTheCheckTheChannelsStayQuiet(t *testing.T) {
	t.Parallel()

	var policy Policy

	if failure := violation(t, func() {
		_ = policy.NotifyObserved(Target{}, nil, nil)
	}); failure != nil {
		t.Errorf("没装检查时不该抛：%s", failure.Message)
	}
}
