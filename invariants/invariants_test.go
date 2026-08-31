// 本文件的作用：把 DSH 那份 310 行测试（packages/runtime-diagnostics/invariants/
// tests/service.spec.ts）钉住的**行为事实**逐条在 Go 侧重新钉一遍。
//
// 搬的是断言，不是断言的写法。DSH 用 vitest 的 vi.fn() 和 cordis 的 ctx.emit 来观察
// 「监听器有没有被调到」；这里用一个十几行的测试内事件总线做同一件事。观察手段换了，
// 被观察的事实一条不少——因为那些事实才是这个包的全部内容。
//
// 有一条是 DSH 那边不需要而 Go 这边必须有的：并发下同名注册只能有一个赢家。
package invariants

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// bus 是测试用的极简事件总线，用来观察 installer 装上的东西是否活着。
//
// 它替代 cordis 的 ctx.on / ctx.emit。emit 不吞 panic——Fail 的语义就是沿栈上抛，
// 吞掉的话就测不出违例到底有没有传到观察点。
type bus struct {
	mutex     sync.Mutex
	nextID    int
	listeners map[int]func()
}

func newBus() *bus { return &bus{listeners: map[int]func(){}} }

// on 装一个监听器，返回卸载函数。
func (b *bus) on(listener func()) func() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.nextID++
	id := b.nextID
	b.listeners[id] = listener
	return func() {
		b.mutex.Lock()
		defer b.mutex.Unlock()
		delete(b.listeners, id)
	}
}

func (b *bus) emit() {
	b.mutex.Lock()
	snapshot := make([]func(), 0, len(b.listeners))
	for _, listener := range b.listeners {
		snapshot = append(snapshot, listener)
	}
	b.mutex.Unlock()

	for _, listener := range snapshot {
		listener()
	}
}

// counter 数一个监听器被调了几次。
type counter struct {
	mutex sync.Mutex
	count int
}

func (c *counter) hit() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.count++
}

func (c *counter) value() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.count
}

// probe 注册一个「往总线上挂一个计数器」的 installer，是下面大多数用例的公共动作。
func probe(t *testing.T, registry *Registry, packageName string, events *bus) (*counter, func()) {
	t.Helper()

	hits := &counter{}
	dispose, err := registry.Register(context.Background(), packageName,
		func(_ context.Context, scope *Scope, _ Fail) error {
			scope.Defer(events.on(hits.hit))
			return nil
		})
	if err != nil {
		t.Fatalf("Register(%q) 意外失败：%v", packageName, err)
	}
	return hits, dispose
}

func mustNew(t *testing.T, config Config) *Registry {
	t.Helper()
	registry, err := New(config)
	if err != nil {
		t.Fatalf("New(%+v) 意外失败：%v", config, err)
	}
	return registry
}

func boolPtr(v bool) *bool { return &v }

// ---- 挑选规则 ----

// TestDefaultsAdmitEverything 钉住默认值：不配任何东西时检查是开着的，
// 两个空列表分别表示「全放行」和「不排除任何东西」。
//
// 这一条看着平凡，但它决定了「忘了配」的后果。默认关闭的话，
// 一整套不变量会在没人察觉的情况下一行都不跑。
func TestDefaultsAdmitEverything(t *testing.T) {
	t.Parallel()

	for _, config := range []Config{
		{},
		{PackageAllowlist: []string{}, PackageBlocklist: []string{}},
	} {
		events := newBus()
		registry := mustNew(t, config)
		hits, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)

		events.emit()
		if hits.value() != 1 {
			t.Errorf("配置 %+v 下检查应当装上并被调用一次，实际 %d 次", config, hits.value())
		}
	}
}

// TestDisabledStillReservesTheName 钉住一条容易被当成小事的行为：
// 总开关关掉时，installer 不装，但包名照样占住。
//
// 理由是这样「检查被过滤掉了」和「这个包压根没注册」才不会混淆。
// 如果关闭时连名字都不占，那么两个包抢同一个名字这种真错误，
// 会在关闭诊断的环境里静默消失，等到开启诊断时才炸——那时已经离现场很远了。
func TestDisabledStillReservesTheName(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{Enabled: boolPtr(false)})
	hits, dispose := probe(t, registry, "@deepseek-ai/dsh-session", events)

	events.emit()
	if hits.value() != 0 {
		t.Errorf("总开关关着时不该装上任何检查，实际被调用 %d 次", hits.value())
	}

	_, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(context.Context, *Scope, Fail) error { return nil })
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Errorf("关闭状态下同名注册也该被拒，实际 err = %v", err)
	}

	dispose()
	if names := registry.Registered(); len(names) != 0 {
		t.Errorf("注销之后不该还占着名额，实际 %v", names)
	}
}

// TestPatternsAreUnanchoredAndCaseSensitive 钉住正则的两条语义。
//
// 这两条如果搞反，后果是**沉默的**：不锚定被当成锚定，会让一批本该跑的检查不跑；
// 大小写不敏感被当成敏感，会让一批本该被排除的检查跑起来。两种都不会报错。
func TestPatternsAreUnanchoredAndCaseSensitive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		allowlist   []string
		packageName string
		wantHits    int
	}{
		{"不锚定：子串命中就算命中", []string{"session"}, "@deepseek-ai/dsh-session-extra", 1},
		{"写了 ^$ 才是精确匹配", []string{"^@deepseek-ai/dsh-session$"}, "@deepseek-ai/dsh-session-extra", 0},
		{"大小写敏感", []string{"Session"}, "@deepseek-ai/dsh-session", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			events := newBus()
			registry := mustNew(t, Config{PackageAllowlist: test.allowlist})
			hits, _ := probe(t, registry, test.packageName, events)

			events.emit()
			if hits.value() != test.wantHits {
				t.Errorf("白名单 %v 对 %q 应当命中 %d 次，实际 %d 次",
					test.allowlist, test.packageName, test.wantHits, hits.value())
			}
		})
	}
}

// TestBlocklistOverridesAllowlist 钉住优先级：黑名单赢。
//
// 包括两个列表写完全相同的模式这种情况——结果必须是排除。
// 有明确的赢家，「我到底配没配上」才有唯一答案。
func TestBlocklistOverridesAllowlist(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{
		PackageAllowlist: []string{"^@deepseek-ai/dsh-"},
		PackageBlocklist: []string{"session"},
	})
	sessionHits, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)
	agentHits, _ := probe(t, registry, "@deepseek-ai/dsh-agent", events)

	events.emit()
	if sessionHits.value() != 0 {
		t.Errorf("被黑名单命中的包不该装上检查，实际被调用 %d 次", sessionHits.value())
	}
	if agentHits.value() != 1 {
		t.Errorf("只命中白名单的包该装上检查，实际被调用 %d 次", agentHits.value())
	}

	sameSource := mustNew(t, Config{
		PackageAllowlist: []string{"agent"},
		PackageBlocklist: []string{"agent"},
	})
	sameEvents := newBus()
	sameHits, _ := probe(t, sameSource, "@deepseek-ai/dsh-agent", sameEvents)
	sameEvents.emit()
	if sameHits.value() != 0 {
		t.Errorf("同一条模式同时出现在黑白名单时该按黑名单算，实际被调用 %d 次", sameHits.value())
	}
}

// TestZeroMatchPatternIsLegal 钉住「一条现在谁都匹配不上的模式是合法的」。
//
// 不这样的话，配置的合法性就取决于此刻加载了哪些包——同一份配置在不同组合下
// 时灵时不灵。包是后加载的，模式必须能先写。
func TestZeroMatchPatternIsLegal(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{PackageAllowlist: []string{"^@later/invariants$"}})
	nowHits, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)
	laterHits, _ := probe(t, registry, "@later/invariants", events)

	events.emit()
	if nowHits.value() != 0 {
		t.Errorf("没命中白名单的包不该装上检查，实际被调用 %d 次", nowHits.value())
	}
	if laterHits.value() != 1 {
		t.Errorf("命中白名单的包该装上检查，实际被调用 %d 次", laterHits.value())
	}
}

// ---- 校验 ----

// TestNewRejectsMalformedFilters 钉住畸形配置在构造期就失败，而不是被静默忽略。
//
// 这是整个包里最重要的一条 fail-fast：一条编译不了的白名单如果被跳过，
// 「我配了白名单」和「我的白名单没生效」在现象上完全一样，
// 而后者意味着一整批检查悄悄没跑。宁可起不来。
func TestNewRejectsMalformedFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantHas string
	}{
		{"白名单有空串", Config{PackageAllowlist: []string{""}}, "非空"},
		{"白名单有纯空白", Config{PackageAllowlist: []string{" "}}, "非空"},
		{"白名单有前导空白", Config{PackageAllowlist: []string{" session"}}, "空白"},
		{"黑名单有尾随空白", Config{PackageBlocklist: []string{"session "}}, "空白"},
		{"白名单有重复项", Config{PackageAllowlist: []string{"session", "session"}}, "重复"},
		{"黑名单有重复项", Config{PackageBlocklist: []string{"agent", "agent"}}, "重复"},
		{"白名单正则编译不了", Config{PackageAllowlist: []string{"["}}, "编译不了"},
		{"黑名单正则编译不了", Config{PackageBlocklist: []string{"("}}, "编译不了"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registry, err := New(test.config)
			if err == nil {
				t.Fatalf("New(%+v) 本该失败，实际返回了 %v", test.config, registry)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("错误该能被 errors.Is(ErrInvalidConfig) 认出来，实际 %v", err)
			}
			if !strings.Contains(err.Error(), test.wantHas) {
				t.Errorf("报错该说清是哪种问题（含 %q），实际 %q", test.wantHas, err.Error())
			}
		})
	}
}

// TestRegisterRejectsMalformedPackageNames 钉住包名必须是一个能拿去比对的标识。
//
// 带空白的名字最坏的地方不是它难看，是 "dsh-session" 和 "dsh-session " 会被算成
// 两个不同的包，于是重名检测形同虚设。
func TestRegisterRejectsMalformedPackageNames(t *testing.T) {
	t.Parallel()

	for _, packageName := range []string{"", " ", " package", "pack age", "package\n"} {
		registry := mustNew(t, Config{})
		_, err := registry.Register(context.Background(), packageName,
			func(context.Context, *Scope, Fail) error { return nil })
		if !errors.Is(err, ErrInvalidPackageName) {
			t.Errorf("包名 %q 本该被拒，实际 err = %v", packageName, err)
		}
		if names := registry.Registered(); len(names) != 0 {
			t.Errorf("被拒的注册不该占住名额，实际 %v", names)
		}
	}
}

// ---- 违例归属 ----

// TestFailAttributesToTheRegisteringPackage 钉住这个包存在的核心理由：
// 一条违例必须带着「是谁的约定被违反了」。
//
// 违例是在别人的操作过程中被观察到的，报错现场那一层往往不是约定的拥有者。
// 一条不带归属的「seq must strictly increase」没人知道该去找谁。
func TestFailAttributesToTheRegisteringPackage(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{})
	_, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(_ context.Context, scope *Scope, fail Fail) error {
			scope.Defer(events.on(func() { fail("seq must strictly increase") }))
			return nil
		})
	if err != nil {
		t.Fatalf("Register 意外失败：%v", err)
	}

	var violation *Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("违例本该沿栈上抛到观察点，实际什么都没发生")
			}
			failure, ok := recovered.(*Error)
			if !ok {
				t.Fatalf("panic 的值必须是 *Error，实际 %T", recovered)
			}
			violation = failure
		}()
		events.emit()
	}()

	if violation.PackageName != "@deepseek-ai/dsh-session" {
		t.Errorf("归属该是注册方，实际 %q", violation.PackageName)
	}
	if violation.Message != "seq must strictly increase" {
		t.Errorf("消息该原样保留，实际 %q", violation.Message)
	}
	want := `invariant violated by "@deepseek-ai/dsh-session": seq must strictly increase`
	if violation.Error() != want {
		t.Errorf("错误文案该是 %q，实际 %q", want, violation.Error())
	}
	if Code != "INVARIANT" {
		t.Errorf("稳定代号该是 INVARIANT，实际 %q", Code)
	}
}

// ---- 生命周期 ----

// TestDisposeUnwindsCompletelyAndPermitsReRegistration 钉住注销确实拆干净了。
//
// 判据不是「注销函数返回了」，而是两件可观察的事：装上的监听器不再响应，
// 并且同一个包名能重新注册进来。后者是前者的独立证据——名额还占着的话就说明没拆完。
func TestDisposeUnwindsCompletelyAndPermitsReRegistration(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{})

	first, dispose := probe(t, registry, "@deepseek-ai/dsh-session", events)
	events.emit()
	dispose()
	events.emit()
	if first.value() != 1 {
		t.Errorf("注销后的 emit 不该再打到旧监听器，实际累计 %d 次", first.value())
	}

	second, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)
	events.emit()
	if first.value() != 1 {
		t.Errorf("旧监听器仍不该被调到，实际累计 %d 次", first.value())
	}
	if second.value() != 1 {
		t.Errorf("重新注册的监听器该被调到一次，实际 %d 次", second.value())
	}
}

// TestDisposeIsIdempotent 钉住注销函数可以被多调。
//
// 现实里它会同时出现在一个 defer 和一次显式关闭里，多跑一次不能把别人的东西拆了——
// 尤其是名额已经被下一个注册方占走之后。
func TestDisposeIsIdempotent(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{})

	unwound := 0
	dispose, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(_ context.Context, scope *Scope, _ Fail) error {
			scope.Defer(func() { unwound++ })
			return nil
		})
	if err != nil {
		t.Fatalf("Register 意外失败：%v", err)
	}

	dispose()
	dispose()
	dispose()
	if unwound != 1 {
		t.Errorf("清理动作只该跑一次，实际 %d 次", unwound)
	}

	// 名额腾出来之后再被别人占住，此时多余的 dispose 不许把别人踢掉。
	takeover, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)
	dispose()
	events.emit()
	if takeover.value() != 1 {
		t.Errorf("多余的 dispose 不该影响后来的注册方，实际被调用 %d 次", takeover.value())
	}
}

// TestScopeUnwindsInReverseOrder 钉住清理按逆序执行。
//
// 后装上的东西可能依赖先装上的，拆的时候必须反着来，和 Go 的 defer 同理。
func TestScopeUnwindsInReverseOrder(t *testing.T) {
	t.Parallel()

	registry := mustNew(t, Config{})
	var order []string
	dispose, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(_ context.Context, scope *Scope, _ Fail) error {
			scope.Defer(func() { order = append(order, "第一个装的") })
			scope.Defer(func() { order = append(order, "第二个装的") })
			scope.Defer(func() { order = append(order, "第三个装的") })
			return nil
		})
	if err != nil {
		t.Fatalf("Register 意外失败：%v", err)
	}
	dispose()

	want := []string{"第三个装的", "第二个装的", "第一个装的"}
	if len(order) != len(want) {
		t.Fatalf("该跑 %d 个清理动作，实际 %d 个：%v", len(want), len(order), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("清理顺序该是 %v，实际 %v", want, order)
		}
	}
}

// TestFailedInstallerRollsBackAtomically 钉住这个包里最容易写漏的一条。
//
// installer 跑到一半失败时，它在失败之前已经装上的东西必须被拆掉，包名必须释放。
// 半装上的检查比没有检查更坏：它在一个不完整的视角上观察，然后误报。
//
// 这条也是 Scope 这个类型存在的全部理由——如果 installer 只在成功时返回清理函数，
// 半途失败的那些就永远拆不掉了。
func TestFailedInstallerRollsBackAtomically(t *testing.T) {
	t.Parallel()

	events := newBus()
	registry := mustNew(t, Config{})

	leaked := &counter{}
	dispose, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(_ context.Context, scope *Scope, _ Fail) error {
			scope.Defer(events.on(leaked.hit))
			return errors.New("installer failed")
		})
	if err == nil {
		t.Fatal("installer 返回错误时 Register 本该失败")
	}
	if dispose != nil {
		t.Error("失败的注册不该返回注销函数——没有东西需要注销")
	}
	if !strings.Contains(err.Error(), "installer failed") {
		t.Errorf("错误该把 installer 的原因带出来，实际 %q", err.Error())
	}

	events.emit()
	if leaked.value() != 0 {
		t.Errorf("失败前装上的监听器必须被拆掉，实际被调用 %d 次", leaked.value())
	}
	if names := registry.Registered(); len(names) != 0 {
		t.Errorf("失败的注册必须释放包名，实际还占着 %v", names)
	}

	// 名额确实放开了：同一个包名可以立刻重来。
	retry, _ := probe(t, registry, "@deepseek-ai/dsh-session", events)
	events.emit()
	if retry.value() != 1 {
		t.Errorf("重试的注册该正常装上，实际被调用 %d 次", retry.value())
	}
}

// TestRegisterAfterCloseIsRejected 钉住关掉之后不再收新注册。
func TestRegisterAfterCloseIsRejected(t *testing.T) {
	t.Parallel()

	registry := mustNew(t, Config{})
	registry.Close()

	_, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
		func(context.Context, *Scope, Fail) error { return nil })
	if !errors.Is(err, ErrRegistryClosed) {
		t.Errorf("关闭后的注册该返回 ErrRegistryClosed，实际 %v", err)
	}
}

// TestConcurrentRegistrationHasExactlyOneWinner 是 DSH 那边不存在的一条。
//
// 它是单线程 JS，登记簿天然安全。Go 里注册会来自不同的 goroutine，
// 如果预留不是原子的，两个 goroutine 会同时拿到同一个包名——
// 于是同一个包的两份检查并存，而「已注册」这个判断从此不可信。
//
// 这条测试的价值在于它只在 -race 下才可能露馅，靠读代码是看不出来的。
func TestConcurrentRegistrationHasExactlyOneWinner(t *testing.T) {
	t.Parallel()

	registry := mustNew(t, Config{})

	const racers = 32
	var (
		waitGroup sync.WaitGroup
		mutex     sync.Mutex
		succeeded int
		conflicts int
	)
	waitGroup.Add(racers)
	for range racers {
		go func() {
			defer waitGroup.Done()
			_, err := registry.Register(context.Background(), "@deepseek-ai/dsh-session",
				func(context.Context, *Scope, Fail) error { return nil })

			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrAlreadyRegistered):
				conflicts++
			default:
				t.Errorf("意外的错误：%v", err)
			}
		}()
	}
	waitGroup.Wait()

	if succeeded != 1 {
		t.Errorf("%d 个并发注册只该有 1 个成功，实际 %d 个", racers, succeeded)
	}
	if conflicts != racers-1 {
		t.Errorf("其余 %d 个都该报已注册，实际 %d 个", racers-1, conflicts)
	}
}
