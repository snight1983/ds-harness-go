// 本文件验读的那一面和写的那一面各自的接线：permissions 这个投影单元折出来的
// 选项单、`/permission` 这条命令的三种结局，以及那条「记着的名字必须还认得出来」
// 的检查。
//
// 源: packages/interaction/permission-presets/src/index.ts:237-275
// 源: packages/interaction/permission-presets/src/invariant.ts

package permissionpresets

import (
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// fakeSession 是一个只把日志摆在那儿的会话视图。
type fakeSession struct {
	id     sessionlog.SessionID
	events []sessionlog.Event
}

func (s *fakeSession) ID() sessionlog.SessionID   { return s.id }
func (s *fakeSession) Events() []sessionlog.Event { return s.events }

func (s *fakeSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}

// projectionOf 把这些事件折一遍，交出 permissions 这个键上线的那个值。
func projectionOf(t *testing.T, service *Service, events ...sessionlog.Event) PermissionSelect {
	t.Helper()
	registry := projection.NewRegistry()
	dispose, err := RegisterProjection(registry, service)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)
	snapshot := registry.Snapshot(&fakeSession{id: "s1", events: events})
	value, ok := snapshot.Values[ProjectionKey]
	if !ok {
		t.Fatalf("permissions 这个键该在：%+v", snapshot.Values)
	}
	typed, ok := value.(PermissionSelect)
	if !ok {
		t.Fatalf("permissions 这个键该是一份 PermissionSelect，拿到 %T", value)
	}
	return typed
}

// TestTheProjectionFoldsTheSelectOutOfTheLogAlone 验界面那份选项单是纯回放量。
//
// 源: packages/interaction/permission-presets/src/index.ts:237-243
//
// 宿主重启、另一个标签页、一次冷读都要还原出同一份；任何一条只在活服务上成立的
// 断言都会让第二个界面看见一个和第一个不一样的当前值。
func TestTheProjectionFoldsTheSelectOutOfTheLogAlone(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	// 空日志走装配默认旋钮值。
	empty := projectionOf(t, service)
	if empty.CurrentValue != "guarded" {
		t.Fatalf("空日志该折出 guarded，拿到 %q", empty.CurrentValue)
	}

	// 一次打平手的选择在这条路上同样保得住。
	tied := projectionOf(t, service, preset(0, "guarded-alias"), policy(1, userapproval.PolicyAsk))
	if tied.CurrentValue != "guarded-alias" {
		t.Fatalf("该保住用户选的那个名字，拿到 %q", tied.CurrentValue)
	}
	if len(tied.Options) != 3 {
		t.Fatalf("配得上时不该追加那个派生值，拿到 %+v", tied.Options)
	}
}

// TestRegisteringTheProjectionNeedsBothArms 验两个必填参数各自被拦下。
func TestRegisteringTheProjectionNeedsBothArms(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	if _, err := RegisterProjection(nil, service); err == nil {
		t.Fatal("没有注册表该报错")
	}
	if _, err := RegisterProjection(projection.NewRegistry(), nil); err == nil {
		t.Fatal("没有服务该报错")
	}
}

// ---- 命令 ----

// runCommandFor 跑一次 `/permission`，输入由调用方给。
func runCommandFor(t *testing.T, service *Service, key *scope.Key, input string) commands.Result {
	t.Helper()
	definition, err := service.CommandDefinition()
	if err != nil {
		t.Fatalf("造命令定义失败：%v", err)
	}
	result, err := definition.Handler(t.Context(), commands.Invocation{Agent: key, RawInput: input})
	if err != nil {
		t.Fatalf("跑命令失败：%v", err)
	}
	return result
}

// TestAnEmptyInputReportsTheCurrentPresetAndTheTable 验空输入是一次查询。
//
// 源: packages/interaction/permission-presets/src/index.ts:266-268
func TestAnEmptyInputReportsTheCurrentPresetAndTheTable(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	result := runCommandFor(t, service, harness.key, "  ")
	if result.Kind != commands.ResultSuccess {
		t.Fatalf("查询该是成功结果，拿到 %+v", result)
	}
	if !strings.Contains(result.Text, "current preset guarded") {
		t.Fatalf("该报出当前预设，拿到 %q", result.Text)
	}
	if !strings.Contains(result.Text, "guarded, guarded-alias, trusted") {
		t.Fatalf("该列出可选的那些，拿到 %q", result.Text)
	}
	if got := harness.log.types(); len(got) != 0 {
		t.Fatalf("一次查询不该写任何东西，拿到 %v", got)
	}
}

// TestAnUnknownPresetIsAnErrorResultNotAnError 验打错的名字走的是错误结果这一支。
//
// 源: packages/interaction/permission-presets/src/index.ts:269-271
//
// 它是预期之内的失败：界面把这句话显示给用户，agent 循环不受影响。回一个 Go 错误
// 会让这次调用变成「处理器炸了」，那是另一件事。
func TestAnUnknownPresetIsAnErrorResultNotAnError(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	for _, name := range []string{"nope", CustomPreset} {
		result := runCommandFor(t, service, harness.key, name)
		if result.Kind != commands.ResultError {
			t.Fatalf("%q 该是错误结果，拿到 %+v", name, result)
		}
		if !strings.Contains(result.Text, "unknown preset") {
			t.Fatalf("该说清是哪一步不认识，拿到 %q", result.Text)
		}
	}
	if got := harness.log.types(); len(got) != 0 {
		t.Fatalf("被拒的切换不该留下任何字节，拿到 %v", got)
	}
}

// TestASuccessfulSwitchWritesAndNotifies 验命令那条路走的是会通知模型的写路径。
//
// 源: packages/interaction/permission-presets/src/index.ts:272-273
func TestASuccessfulSwitchWritesAndNotifies(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	result := runCommandFor(t, service, harness.key, " trusted ")
	if result.Kind != commands.ResultSuccess || result.Text != "preset trusted" {
		t.Fatalf("该回一句不带命令名的成功话，拿到 %+v", result)
	}
	want := []sessionlog.EventType{EventPreset, userapproval.EventPolicy}
	if got := harness.log.types(); !sameTypes(got, want) {
		t.Fatalf("该先记名字再写旋钮，拿到 %v", got)
	}
	if got := len(harness.notifications()); got != 1 {
		t.Fatalf("该把这次切换排进下一次模型步，排了 %d 条", got)
	}
}

// TestTheCommandIsNotRegisterableWithoutALogPath 验没接 LogOf 时这条命令根本登记不了。
//
// 登记一条每一支都答不出话的命令，比不登记它更糟：用户会在发现界面上看见它。
func TestTheCommandIsNotRegisterableWithoutALogPath(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) { config.LogOf = nil })

	if _, err := service.CommandDefinition(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("该报配置不成立，拿到 %v", err)
	}
}

// ---- 不变量 ----

// invariantHarness 是一次不变量测试要的家当：两条胳膊都由它扮。
type invariantHarness struct {
	registry     *invariants.Registry
	service      *Service
	loaded       []sessionlog.Event
	observers    []func(sessionlog.Event)
	unsubscribed int
}

// newInvariantHarness 造一个全开的注册表，带上一份已经装载的日志。
func newInvariantHarness(t *testing.T, loaded ...sessionlog.Event) *invariantHarness {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return &invariantHarness{
		registry: registry,
		service:  newService(t, newApproval(t, userapproval.PolicyAsk), nil),
		loaded:   loaded,
	}
}

// register 把本包的检查装进去。
func (h *invariantHarness) register(t *testing.T) func() {
	t.Helper()
	undo, err := RegisterInvariants(
		t.Context(),
		h.registry,
		h.service,
		func() []sessionlog.Event { return h.loaded },
		func(observer func(sessionlog.Event)) func() {
			h.observers = append(h.observers, observer)
			return func() { h.unsubscribed++ }
		},
	)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	return undo
}

// emit 把一条事件推给所有还在的观察者。
func (h *invariantHarness) emit(event sessionlog.Event) {
	for _, observer := range h.observers {
		observer(event)
	}
}

// violation 跑一段会违例的代码，交出那条违例。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func violation(t *testing.T, run func()) *invariants.Error {
	t.Helper()
	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		run()
	}()
	if caught == nil {
		t.Fatal("该抛出一条违例")
	}
	return caught
}

// TestTheInvariantCatchesANameThatLeftTheTable 验历史里引着一条已经不在的预设时
// 在装载这一刻就响。
//
// 源: packages/interaction/permission-presets/src/invariant.ts:14-19
//
// 这正是它要抓的场景：一次去掉某条预设的部署改动，会让旧会话里那个名字失效。
func TestTheInvariantCatchesANameThatLeftTheTable(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t, preset(0, "retired"))

	failure := violation(t, func() { h.register(t) })
	if failure.PackageName != PackageName {
		t.Fatalf("该报在本包名下，拿到 %q", failure.PackageName)
	}
	if !strings.Contains(failure.Message, `unknown preset "retired"`) {
		t.Fatalf("该带上那条违例本身，拿到 %q", failure.Message)
	}
}

// TestTheInvariantCatchesANameAppendedLater 验后来追加的那条同样被拦。
func TestTheInvariantCatchesANameAppendedLater(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	h.register(t)

	// 一条合法的追加、一条别的类型、一条读不回来的负载，都不该有任何动静——
	// 负载形状是 [applyKnob] 的事，不是这条检查的事。
	h.emit(preset(0, "trusted"))
	h.emit(policy(1, userapproval.PolicyNever))
	h.emit(presetEvent(2, `not json`))

	failure := violation(t, func() { h.emit(preset(3, "retired")) })
	if !strings.Contains(failure.Message, `unknown preset "retired"`) {
		t.Fatalf("该带上那条违例本身，拿到 %q", failure.Message)
	}
}

// TestUnregisteringTheInvariantStopsTheCheck 验注销之后那条检查不再在别人的写路径上抛。
func TestUnregisteringTheInvariantStopsTheCheck(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	undo()

	if h.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", h.unsubscribed)
	}
}

// TestRegisteringInvariantsNeedsAllFourArms 验四个必填参数各自被拦下。
func TestRegisteringInvariantsNeedsAllFourArms(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	loaded := func() []sessionlog.Event { return nil }
	subscribe := func(func(sessionlog.Event)) func() { return func() {} }

	cases := []struct {
		name string
		run  func() (func(), error)
	}{
		{"没有注册表", func() (func(), error) {
			return RegisterInvariants(t.Context(), nil, h.service, loaded, subscribe)
		}},
		{"没有服务", func() (func(), error) {
			return RegisterInvariants(t.Context(), h.registry, nil, loaded, subscribe)
		}},
		{"没有已装载日志", func() (func(), error) {
			return RegisterInvariants(t.Context(), h.registry, h.service, nil, subscribe)
		}},
		{"没有订阅", func() (func(), error) {
			return RegisterInvariants(t.Context(), h.registry, h.service, loaded, nil)
		}},
	}
	for _, testCase := range cases {
		if _, err := testCase.run(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s 该报配置不成立，拿到 %v", testCase.name, err)
		}
	}
}
