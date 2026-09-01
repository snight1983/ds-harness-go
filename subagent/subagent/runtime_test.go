// 本文件的作用：这条接缝那台服务的测试——提供方注册表的登记与摘除、一次性派发
// 那几道开工期闸门，以及没组装续接能力时那几条操作各自的答复。

package subagent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
)

// codeOf 取一条带码失败上的那个码；不是带码失败就交回空串。
func codeOf(err error) string {
	var coded *llm.Error
	if errors.As(err, &coded) {
		return coded.Failure.Code
	}
	return ""
}

// saysAnywhere 说这条错误链上有没有哪一层的说法里带着这段话。
//
// 本包那些带码失败是 *llm.Error，而它的 Error() **不**把原因那一层的说法接进来。
// 于是一条汇总失败的顶层只说得出「在几个活化上失败了」，内层那句「在几处边界上
// 失败了」只有走一遍 Unwrap 才看得见。
func saysAnywhere(err error, phrase string) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), phrase) {
		return true
	}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() error }:
		return saysAnywhere(unwrapped.Unwrap(), phrase)
	case interface{ Unwrap() []error }:
		for _, each := range unwrapped.Unwrap() {
			if saysAnywhere(each, phrase) {
				return true
			}
		}
	}
	return false
}

func TestRegisterProviderKeepsRegistrationOrder(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)

	for _, name := range []string{"spawn", "fork", "acp"} {
		register(t, runtime, owner, &fakeProvider{name: name})
	}
	if got := runtime.List(); !reflect.DeepEqual(got, []string{"spawn", "fork", "acp"}) {
		t.Fatalf("该按登记次序列出，实际 %#v", got)
	}
}

// List 交出去的必须是副本，否则调用方改一下就改动了注册表自己那份次序。
func TestListReturnsACopy(t *testing.T) {
	runtime := newRuntime(t)
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn"})

	names := runtime.List()
	names[0] = "被改掉了"
	if runtime.List()[0] != "spawn" {
		t.Fatal("调用方改动交出去的切片不该影响注册表")
	}
}

func TestRegisterProviderRejectsADuplicateName(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	register(t, runtime, owner, &fakeProvider{name: "spawn"})

	_, err := runtime.RegisterProvider(context.Background(), owner, &fakeProvider{name: "spawn"})
	if codeOf(err) != CodeDuplicateProvider {
		t.Fatalf("重名该报 %s，实际 %v", CodeDuplicateProvider, err)
	}
}

func TestRegisterProviderNeedsAnOwnerAndAProvider(t *testing.T) {
	runtime := newRuntime(t)
	if _, err := runtime.RegisterProvider(context.Background(), nil, &fakeProvider{name: "spawn"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有持有作用域该被拒，实际 %v", err)
	}
	if _, err := runtime.RegisterProvider(context.Background(), rootScope(t), nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有提供方该被拒，实际 %v", err)
	}
}

// 四条登记路都不收 nil 观察者：收下的话那次登记会一直挂着，而发射到它头上会
// 当场 panic，离登记现场已经很远了。
func TestLifecycleRegistrationsRejectANilObserver(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	ctx := context.Background()

	for name, attempt := range map[string]func() (func(context.Context) error, error){
		"OnStart":           func() (func(context.Context) error, error) { return runtime.OnStart(ctx, owner, nil) },
		"OnEnd":             func() (func(context.Context) error, error) { return runtime.OnEnd(ctx, owner, nil) },
		"OnProviderAdded":   func() (func(context.Context) error, error) { return runtime.OnProviderAdded(ctx, owner, nil) },
		"OnProviderRemoved": func() (func(context.Context) error, error) { return runtime.OnProviderRemoved(ctx, owner, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			release, err := attempt()
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("nil 观察者该被拒，实际 %v", err)
			}
			if release != nil {
				t.Fatal("被拒的登记不该交回一个撤销函数")
			}
		})
	}
}

// 一个报错的「提供方来了」观察者把这次登记整个卷回去：注册表里不留这个名字，
// 而且那句失败原因就是观察者自己的。
func TestARejectingProviderAddedObserverUnwindsTheRegistration(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	refused := errors.New("这个提供方不许进")

	if _, err := runtime.OnProviderAdded(context.Background(), owner,
		func(Provider) error { return refused }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	release, err := runtime.RegisterProvider(context.Background(), owner, &fakeProvider{name: "spawn"})
	if !errors.Is(err, refused) {
		t.Fatalf("观察者那句失败该原样出来，实际 %v", err)
	}
	if release != nil {
		t.Fatal("被卷回去的登记不该交回一个撤销函数")
	}
	if _, found := runtime.GetProvider("spawn"); found {
		t.Fatal("卷回去之后注册表里不该还有这个名字")
	}
	if len(runtime.List()) != 0 {
		t.Fatalf("那份次序里也该一个都不剩，实际 %#v", runtime.List())
	}
}

func TestDisposingTheOwnerRemovesTheProvider(t *testing.T) {
	runtime := newRuntime(t)
	owner, err := scope.New(scope.NewKey("holder"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	register(t, runtime, owner, &fakeProvider{name: "spawn"})

	if err := owner.Dispose(context.Background()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}
	if _, found := runtime.GetProvider("spawn"); found {
		t.Fatal("作用域一释放，那次登记就该跟着走")
	}
}

// 摘除认的是那个确切的提供方对象：一次登记撤销之后，同一个名字可能已经被后来的
// 另一个提供方占上了，把它误伤掉会让一次毫不相干的登记失效。
func TestRemovingAStaleRegistrationSparesTheSuccessor(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	first := &fakeProvider{name: "spawn"}
	dispose := register(t, runtime, owner, first)

	if err := dispose(context.Background()); err != nil {
		t.Fatalf("撤销登记失败：%v", err)
	}
	second := &fakeProvider{name: "spawn"}
	register(t, runtime, owner, second)

	// 再撤一次原来那次登记：它认不出当下这个提供方，所以什么都不该发生。
	if err := dispose(context.Background()); err != nil {
		t.Fatalf("重复撤销该是空操作，实际 %v", err)
	}
	current, found := runtime.GetProvider("spawn")
	if !found || current != Provider(second) {
		t.Fatalf("后来那次登记不该被误伤，实际 found=%v", found)
	}
}

// 挂不上作用域拆解的登记要把自己卷回去：注册表里绝不留一个没人负责摘除的提供方。
func TestRegisterProviderUnwindsWhenTheOwnerCannotHoldIt(t *testing.T) {
	runtime := newRuntime(t)
	owner, err := scope.New(scope.NewKey("holder"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	if err := owner.Dispose(context.Background()); err != nil {
		t.Fatalf("释放作用域失败：%v", err)
	}

	release, err := runtime.RegisterProvider(context.Background(), owner, &fakeProvider{name: "spawn"})
	if !errors.Is(err, scope.ErrScopeDisposed) {
		t.Fatalf("已经释放的作用域那句拒绝该原样出来，实际 %v", err)
	}
	if release != nil {
		t.Fatal("被卷回去的登记不该交回一个撤销函数")
	}
	if _, found := runtime.GetProvider("spawn"); found {
		t.Fatal("卷回去之后注册表里不该还有这个名字")
	}
}

// 摘一个根本没登记过的名字什么都不发生：那份「认确切对象」的判断先看在不在。
func TestRemoveProviderIgnoresANameThatWasNeverRegistered(t *testing.T) {
	runtime := newRuntime(t)
	removed := make(chan string, 1)
	if _, err := runtime.OnProviderRemoved(context.Background(), rootScope(t),
		func(name string) { removed <- name }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	runtime.removeProvider("从来没有过", &fakeProvider{name: "从来没有过"})
	select {
	case name := <-removed:
		t.Fatalf("没登记过的名字不该发出「提供方走了」，实际 %q", name)
	default:
	}
}

func TestGetProviderReportsAbsence(t *testing.T) {
	if _, found := newRuntime(t).GetProvider("没这个"); found {
		t.Fatal("没登记过的名字该报不在")
	}
}

func TestStartRejectsAnUnknownProvider(t *testing.T) {
	_, err := newRuntime(t).Start(context.Background(), "没这个", StartRequest{})
	if codeOf(err) != CodeNoProvider {
		t.Fatalf("提供方不认识该报 %s，实际 %v", CodeNoProvider, err)
	}
}

// 次序是固定的，于是一次要了好几样的请求报出来的永远是同一条。
func TestStartChecksCapabilitiesInAFixedOrder(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	register(t, runtime, owner, &fakeProvider{name: "bare"})

	depth := 1
	request := StartRequest{
		OutputSchema: &tools.Node{Type: tools.TypeObject},
		MaxDepth:     &depth,
		ToolFilter:   tools.Restriction{Allow: []string{"read"}},
		Persona:      "海盗",
	}
	_, err := runtime.Start(context.Background(), "bare", request)
	if codeOf(err) != CodeUnsupportedCapability {
		t.Fatalf("要了不支持的能力该报 %s，实际 %v", CodeUnsupportedCapability, err)
	}
	if !strings.Contains(err.Error(), "outputSchema") {
		t.Fatalf("四样都要了时该先报 outputSchema，实际 %v", err)
	}
}

func TestStartRejectsEachUnsupportedCapability(t *testing.T) {
	depth := 1
	cases := map[string]struct {
		capabilities Capabilities
		request      StartRequest
		wanted       string
	}{
		"depthLimit": {
			capabilities: Capabilities{OutputSchema: true},
			request:      StartRequest{MaxDepth: &depth},
			wanted:       "depthLimit",
		},
		"toolFilter": {
			capabilities: Capabilities{OutputSchema: true, DepthLimit: true},
			request:      StartRequest{ToolFilter: tools.Restriction{Deny: []string{"write"}}},
			wanted:       "toolFilter",
		},
		"persona": {
			capabilities: Capabilities{OutputSchema: true, DepthLimit: true, ToolFilter: true},
			request:      StartRequest{Persona: "海盗"},
			wanted:       "persona",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			runtime := newRuntime(t)
			register(t, runtime, rootScope(t), &fakeProvider{name: "p", capabilities: testCase.capabilities})
			_, err := runtime.Start(context.Background(), "p", testCase.request)
			if codeOf(err) != CodeUnsupportedCapability || !strings.Contains(err.Error(), testCase.wanted) {
				t.Fatalf("该报缺 %s，实际 %v", testCase.wanted, err)
			}
		})
	}
}

func TestStartRejectsANegativeMaxDepth(t *testing.T) {
	runtime := newRuntime(t)
	register(t, runtime, rootScope(t), &fakeProvider{name: "p", capabilities: Capabilities{DepthLimit: true}})

	depth := -1
	if _, err := runtime.Start(context.Background(), "p", StartRequest{MaxDepth: &depth}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("负的深度上限该被拒，实际 %v", err)
	}
}

func TestStartRejectsANonObjectOutputSchema(t *testing.T) {
	runtime := newRuntime(t)
	register(t, runtime, rootScope(t), &fakeProvider{name: "p", capabilities: Capabilities{OutputSchema: true}})

	schema := tools.Node{Type: tools.TypeString}
	if _, err := runtime.Start(context.Background(), "p", StartRequest{OutputSchema: &schema}); err == nil {
		t.Fatal("不以对象为根的 schema 该被拒")
	}
}

// 服务在派活之前把那份耐久描述符解算好，提供方拿到的是已经拍下来的那份。
func TestStartHandsTheProviderASnapshottedDescriptor(t *testing.T) {
	runtime := newRuntime(t)
	var seen ResolvedStartRequest
	provider := &fakeProvider{name: "spawn", onStart: func(request ResolvedStartRequest) { seen = request }}
	register(t, runtime, rootScope(t), provider)

	if _, err := runtime.Start(context.Background(), "spawn", StartRequest{Label: "查一下"}); err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	wanted := DescriptorData{Version: DescriptorVersion, Mode: ModeOneShot, Provider: "spawn", Label: "查一下"}
	if seen.Descriptor != wanted {
		t.Fatalf("提供方该拿到拍好的一次性描述符，实际 %#v", seen.Descriptor)
	}
}

// 那份描述符在派活之前就解算，所以一份拍不下来的描述符压根到不了提供方那儿。
func TestStartStopsWhenTheDescriptorCannotBeSnapshotted(t *testing.T) {
	runtime := newRuntime(t)
	// 名字是空串的提供方登记得进去——注册表只认这个名字有没有被占——可一份一次性
	// 描述符必须带提供方名字，于是拍快照那一步当场把这次开工拒掉。
	started := false
	register(t, runtime, rootScope(t), &fakeProvider{
		name:    "",
		onStart: func(ResolvedStartRequest) { started = true },
	})

	if _, err := runtime.Start(context.Background(), "", StartRequest{Label: "查一下"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("拍不下来的描述符该被拒，实际 %v", err)
	}
	if started {
		t.Fatal("描述符没过之前不该派活给提供方")
	}
}

// 提供方自己失败时，服务既不留下要调用方处置的运行，也不发任何生命周期边。
func TestStartSurfacesAProviderFailure(t *testing.T) {
	runtime := newRuntime(t)
	failure := errors.New("装不起来")
	register(t, runtime, rootScope(t), &fakeProvider{name: "spawn", startErr: failure})

	run, err := runtime.Start(context.Background(), "spawn", StartRequest{})
	if !errors.Is(err, failure) || run != nil {
		t.Fatalf("提供方的失败该原样出来，实际 run=%v err=%v", run, err)
	}
}

func TestContinuableOperationsFailWithoutTheAgentsService(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()

	if _, err := runtime.StartContinuable(ctx, ContinuableStartSpec{}); codeOf(err) != CodeContinuationUnavailable {
		t.Fatalf("起可续孩子该报 %s，实际 %v", CodeContinuationUnavailable, err)
	}
	if _, err := runtime.Followup(ctx, nil, "child", nil, FollowupOptions{}); codeOf(err) != CodeContinuationUnavailable {
		t.Fatalf("后续投递该报 %s，实际 %v", CodeContinuationUnavailable, err)
	}
	if _, err := runtime.ReportFrom(ctx, nil, nil, ReportOptions{}); codeOf(err) != CodeContinuationUnavailable {
		t.Fatalf("汇报该报 %s，实际 %v", CodeContinuationUnavailable, err)
	}
}

// 打断和排干是被接受的空操作：一台没有的管理器不可能攥着任何活化。
func TestTeardownOperationsAreNoOpsWithoutTheAgentsService(t *testing.T) {
	runtime := newRuntime(t)
	ctx := context.Background()

	if err := runtime.Interrupt("child", InterruptAuthority{Kind: AuthorityUser}); err != nil {
		t.Fatalf("打断该是空操作，实际 %v", err)
	}
	if err := runtime.DrainContinuableDescendants(ctx, nil); err != nil {
		t.Fatalf("按范围排干该是空操作，实际 %v", err)
	}
	if err := runtime.DrainContinuableChildren(ctx, nil, nil); err != nil {
		t.Fatalf("点名放孩子该是空操作，实际 %v", err)
	}
}

// 「这个提供方实现没实现 [ContinuablePreparer]」就是那个能力，所以没实现的会在
// 管理器占下任何孩子资源之前被拒掉。
func TestPrepareContinuableRequiresThePreparerCapability(t *testing.T) {
	runtime := newRuntime(t)
	owner := rootScope(t)
	register(t, runtime, owner, &fakeProvider{name: "oneshot"})

	_, err := runtime.prepareContinuable(context.Background(), "oneshot", ContinuableCreateRequest{})
	if codeOf(err) != CodeUnsupportedCapability {
		t.Fatalf("没有可续能力该报 %s，实际 %v", CodeUnsupportedCapability, err)
	}
	if _, err := runtime.prepareContinuable(context.Background(), "没这个", ContinuableCreateRequest{}); codeOf(err) != CodeNoProvider {
		t.Fatalf("提供方不认识该报 %s，实际 %v", CodeNoProvider, err)
	}
}

func TestPrepareContinuableDelegatesToThePreparer(t *testing.T) {
	runtime := newRuntime(t)
	provider := &preparingProvider{fakeProvider: fakeProvider{name: "spawn"}}
	provider.prepare = func(_ context.Context, request ContinuableCreateRequest) (ContinuableCreateSpec, error) {
		if request.SessionID != "child" {
			t.Fatalf("提供方该拿到占下来的孩子身份，实际 %q", request.SessionID)
		}
		return ContinuableCreateSpec{}, nil
	}
	register(t, runtime, rootScope(t), provider)

	if _, err := runtime.prepareContinuable(context.Background(), "spawn", ContinuableCreateRequest{SessionID: "child"}); err != nil {
		t.Fatalf("预备失败：%v", err)
	}
}

func TestRegisterContinuableSetupIsAvailableWithoutTheAgentsService(t *testing.T) {
	// 装配登记表归服务所有，不归管理器，所以这条路不受「有没有 agent 服务」影响。
	release, err := newRuntime(t).RegisterContinuableSetup(
		func(context.Context, *scope.Scope) (func(context.Context) error, error) {
			return func(context.Context) error { return nil }, nil
		},
	)
	if err != nil {
		t.Fatalf("登记装配失败：%v", err)
	}
	if err := release(context.Background()); err != nil {
		t.Fatalf("撤销装配失败：%v", err)
	}
}
