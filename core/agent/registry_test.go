// 本文件的作用：那张活 agent 表的全部行为——十二组观察者怎么登记、造法那一格、
// 登记／公布／摘除三步各自守着什么、四个查询口，以及八条通知边和三条瀑布的派发
// 与作用域过滤。

package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// newLoggedRegistry 造一张把事故写进缓冲区的表，好让「观察者 panic 了」这件事
// 验得出来，也不会把它印到测试输出上。
func newLoggedRegistry(t *testing.T) (*Registry, *bytes.Buffer) {
	t.Helper()
	var buffer bytes.Buffer
	registry, err := NewRegistry(RegistryOptions{
		Logger: slog.New(slog.NewTextHandler(&buffer, nil)),
	})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	return registry, &buffer
}

// keeper 交出一个「挂上去，挂不上就当场失败，用完自动撤销」的小工具。
//
// 它先收 t 再收那一对返回值，是因为 Go 只允许把一个多返回值调用**整个**当成实参
// 展开——写成 keep(t, registry.OnCreated(...)) 是编译不过的。
func keeper(t *testing.T) func(func(context.Context) error, error) func(context.Context) error {
	t.Helper()
	return func(undo func(context.Context) error, err error) func(context.Context) error {
		t.Helper()
		if err != nil {
			t.Fatalf("登记观察者失败：%v", err)
		}
		t.Cleanup(func() { _ = undo(context.Background()) })
		return undo
	}
}

// fakeFactory 是一份只记下调用、按吩咐返回的造法。
type fakeFactory struct {
	created []CreateOptions
	resumed []ResumeOptions
	handle  Handle
	err     error
}

func (f *fakeFactory) CreateAgent(_ context.Context, _ *scope.Scope, options CreateOptions) (Handle, error) {
	f.created = append(f.created, options)
	return f.handle, f.err
}

func (f *fakeFactory) Resume(_ context.Context, _ *scope.Scope, options ResumeOptions) (Handle, error) {
	f.resumed = append(f.resumed, options)
	return f.handle, f.err
}

// ---- 登记 ----

// TestRegistrarsRejectANilObserver 十二个登记口一个都不收 nil：一个 nil 观察者
// 挂进去要到派发那一刻才炸，而那时离登记点已经很远了。
func TestRegistrarsRejectANilObserver(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()

	registrars := map[string]func() (func(context.Context) error, error){
		"OnCreated":        func() (func(context.Context) error, error) { return registry.OnCreated(ctx, owner, nil) },
		"OnDisposed":       func() (func(context.Context) error, error) { return registry.OnDisposed(ctx, owner, nil) },
		"OnStatus":         func() (func(context.Context) error, error) { return registry.OnStatus(ctx, owner, nil) },
		"OnInboxInserted":  func() (func(context.Context) error, error) { return registry.OnInboxInserted(ctx, owner, nil) },
		"OnInboxDiscarded": func() (func(context.Context) error, error) { return registry.OnInboxDiscarded(ctx, owner, nil) },
		"OnInboxClaimed":   func() (func(context.Context) error, error) { return registry.OnInboxClaimed(ctx, owner, nil) },
		"OnSessionStart":   func() (func(context.Context) error, error) { return registry.OnSessionStart(ctx, owner, nil) },
		"OnPreStep":        func() (func(context.Context) error, error) { return registry.OnPreStep(ctx, owner, nil) },
		"OnRequest":        func() (func(context.Context) error, error) { return registry.OnRequest(ctx, owner, nil) },
		"OnRequestError":   func() (func(context.Context) error, error) { return registry.OnRequestError(ctx, owner, nil) },
		"OnTurnStopping":   func() (func(context.Context) error, error) { return registry.OnTurnStopping(ctx, owner, nil) },
		"OnError":          func() (func(context.Context) error, error) { return registry.OnError(ctx, owner, nil) },
	}
	for name, register := range registrars {
		t.Run(name, func(t *testing.T) {
			if _, err := register(); !errors.Is(err, ErrInvalidRegistration) {
				t.Fatalf("该报 ErrInvalidRegistration，得到 %v", err)
			}
		})
	}
}

// TestRegistrarsAcceptAnObserverAndUndo 十二个登记口都挂得上、也都撤得掉。
func TestRegistrarsAcceptAnObserverAndUndo(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()

	registrars := map[string]func() (func(context.Context) error, error){
		"OnCreated": func() (func(context.Context) error, error) {
			return registry.OnCreated(ctx, owner, func(context.Context, Agent) error { return nil })
		},
		"OnDisposed": func() (func(context.Context) error, error) {
			return registry.OnDisposed(ctx, owner, func(Agent) {})
		},
		"OnStatus": func() (func(context.Context) error, error) {
			return registry.OnStatus(ctx, owner, func(Agent, Status) {})
		},
		"OnInboxInserted": func() (func(context.Context) error, error) {
			return registry.OnInboxInserted(ctx, owner, func(Agent, llm.Message) {})
		},
		"OnInboxDiscarded": func() (func(context.Context) error, error) {
			return registry.OnInboxDiscarded(ctx, owner, func(Agent, llm.Message) {})
		},
		"OnInboxClaimed": func() (func(context.Context) error, error) {
			return registry.OnInboxClaimed(ctx, owner, func(Agent, llm.Message, int) {})
		},
		"OnSessionStart": func() (func(context.Context) error, error) {
			return registry.OnSessionStart(ctx, owner, func(Agent, SessionStartSource) {})
		},
		"OnPreStep": func() (func(context.Context) error, error) {
			return registry.OnPreStep(ctx, owner, func(
				ctx context.Context, _ PreStep, next func(context.Context) (PreStepDecision, error),
			) (PreStepDecision, error) {
				return next(ctx)
			})
		},
		"OnRequest": func() (func(context.Context) error, error) {
			return registry.OnRequest(ctx, owner, func(
				ctx context.Context, _ Request, next func(context.Context) (llm.CallConfig, error),
			) (llm.CallConfig, error) {
				return next(ctx)
			})
		},
		"OnRequestError": func() (func(context.Context) error, error) {
			return registry.OnRequestError(ctx, owner, func(
				ctx context.Context, _ RequestFailure, next func(context.Context) (RequestErrorAction, error),
			) (RequestErrorAction, error) {
				return next(ctx)
			})
		},
		"OnTurnStopping": func() (func(context.Context) error, error) {
			return registry.OnTurnStopping(ctx, owner, func(context.Context, Agent, int) error { return nil })
		},
		"OnError": func() (func(context.Context) error, error) {
			return registry.OnError(ctx, owner, func(TurnError) {})
		},
	}
	for name, register := range registrars {
		t.Run(name, func(t *testing.T) {
			undo, err := register()
			if err != nil {
				t.Fatalf("登记失败：%v", err)
			}
			if err := undo(ctx); err != nil {
				t.Fatalf("撤销失败：%v", err)
			}
		})
	}
}

// TestUndoingARegistrationStopsTheDispatch 撤销之后那个观察者不再被叫到。
func TestUndoingARegistrationStopsTheDispatch(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var seen int
	undo, err := registry.OnStatus(context.Background(), owner, func(Agent, Status) { seen++ })
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := registry.ReportStatus(agent, StatusRunning); err != nil {
		t.Fatalf("报状态失败：%v", err)
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if err := registry.ReportStatus(agent, StatusIdle); err != nil {
		t.Fatalf("报状态失败：%v", err)
	}
	if seen != 1 {
		t.Fatalf("撤销之后不该再被叫到：%d", seen)
	}
}

// ---- 造法 ----

// TestSetFactoryRejectsNil 没有造法这件事由 [ErrNoFactory] 说，不是往格子里塞个 nil。
func TestSetFactoryRejectsNil(t *testing.T) {
	registry := newRegistry(t)
	if _, err := registry.SetFactory(nil); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("该报 ErrInvalidRegistration，得到 %v", err)
	}
}

// TestSetFactoryHoldsExactlyOne 一张表只认一份造法；撤销之后可以再登记，那是循环
// 那一层重载时的正常路径。
func TestSetFactoryHoldsExactlyOne(t *testing.T) {
	registry := newRegistry(t)
	first, second := &fakeFactory{}, &fakeFactory{}

	undo, err := registry.SetFactory(first)
	if err != nil {
		t.Fatalf("登记造法失败：%v", err)
	}
	if _, err := registry.SetFactory(second); !errors.Is(err, ErrFactoryAlreadySet) {
		t.Fatalf("该报 ErrFactoryAlreadySet，得到 %v", err)
	}
	if err := undo(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := registry.SetFactory(second); err != nil {
		t.Fatalf("撤销之后该能再登记：%v", err)
	}
}

// TestStaleFactoryUndoKeepsTheCurrentOne 一次过期的撤销清不掉后来那一份登记——
// 这正是那层 [factorySlot] 包装存在的理由。
func TestStaleFactoryUndoKeepsTheCurrentOne(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	first, second := &fakeFactory{}, &fakeFactory{}

	undoFirst, err := registry.SetFactory(first)
	if err != nil {
		t.Fatalf("登记造法失败：%v", err)
	}
	if err := undoFirst(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := registry.SetFactory(second); err != nil {
		t.Fatalf("登记造法失败：%v", err)
	}
	// 再跑一次那份过期的撤销。
	if err := undoFirst(ctx); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := registry.Create(ctx, rootScope(t), CreateOptions{SessionID: "one"}); err != nil {
		t.Fatalf("后登记那份造法该还在：%v", err)
	}
	if len(second.created) != 1 {
		t.Fatalf("转交给的不是后登记那一份：%d", len(second.created))
	}
}

// TestCreateAndResumeNeedAFactory 没有造法时两条路都当场报出来。
func TestCreateAndResumeNeedAFactory(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	owner := rootScope(t)

	if _, err := registry.Create(ctx, owner, CreateOptions{}); !errors.Is(err, ErrNoFactory) {
		t.Fatalf("该报 ErrNoFactory，得到 %v", err)
	}
	if _, err := registry.Resume(ctx, owner, ResumeOptions{}); !errors.Is(err, ErrNoFactory) {
		t.Fatalf("该报 ErrNoFactory，得到 %v", err)
	}
}

// TestCreateAndResumeDelegate 两条路原样转交，选项和错误都不改。
func TestCreateAndResumeDelegate(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	owner := rootScope(t)
	agent := newFakeAgent(t, "made", nil)
	factory := &fakeFactory{handle: Handle{Agent: agent}}
	if _, err := registry.SetFactory(factory); err != nil {
		t.Fatalf("登记造法失败：%v", err)
	}

	handle, err := registry.Create(ctx, owner, CreateOptions{SessionID: "made", WorkspaceID: testWorkspaceID})
	if err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}
	if handle.Agent != Agent(agent) {
		t.Fatalf("交回来的句柄不对：%v", handle.Agent)
	}
	if len(factory.created) != 1 || factory.created[0].SessionID != "made" {
		t.Fatalf("选项没原样转交：%+v", factory.created)
	}

	if _, err := registry.Resume(ctx, owner, ResumeOptions{ResumeSessionID: "made"}); err != nil {
		t.Fatalf("续跑失败：%v", err)
	}
	if len(factory.resumed) != 1 || factory.resumed[0].ResumeSessionID != "made" {
		t.Fatalf("选项没原样转交：%+v", factory.resumed)
	}

	factory.err = errors.New("造不出来")
	if _, err := registry.Create(ctx, owner, CreateOptions{}); !errors.Is(err, factory.err) {
		t.Fatalf("造法那条错误该原样交出来，得到 %v", err)
	}
	if _, err := registry.Resume(ctx, owner, ResumeOptions{}); !errors.Is(err, factory.err) {
		t.Fatalf("造法那条错误该原样交出来，得到 %v", err)
	}
}

// ---- 登记、公布、摘除 ----

// TestEnterRejectsAnUnusableAgent 逐条走进不来的那四种 agent。
func TestEnterRejectsAnUnusableAgent(t *testing.T) {
	registry := newRegistry(t)

	cases := map[string]struct {
		agent Agent
		want  error
	}{
		"agent 是 nil": {nil, ErrInvalidRegistration},
		"没有载体作用域": {
			&fakeAgent{id: "one", session: newFreeSession(t, "one", nil)},
			ErrInvalidRegistration,
		},
		"没有会话": {
			&fakeAgent{id: "one", scope: keyedScope(t, "one", nil)},
			ErrIdentityMismatch,
		},
		"身份和会话对不上": {
			&fakeAgent{id: "one", scope: keyedScope(t, "one", nil), session: newFreeSession(t, "另一个", nil)},
			ErrIdentityMismatch,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.Enter(testCase.agent, nil); !errors.Is(err, testCase.want) {
				t.Fatalf("该报 %v，得到 %v", testCase.want, err)
			}
		})
	}
}

// TestEnterRejectsADuplicateIdentity 一个身份上只容得下一份活登记——这里是权威的
// 撞名边界，并发的 create／resume 可以都备好，但只有一份进得来。
func TestEnterRejectsADuplicateIdentity(t *testing.T) {
	registry := newRegistry(t)
	first := newFakeAgent(t, "same", nil)
	if _, err := registry.Enter(first, nil); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	second := newFakeAgent(t, "same", nil)
	if _, err := registry.Enter(second, nil); !errors.Is(err, ErrAgentAlreadyExists) {
		t.Fatalf("该报 ErrAgentAlreadyExists，得到 %v", err)
	}
}

// TestRegisterStopsWhenEnterFails 进表那一步就没过时不往下走：不公布，也不交出
// 一个会把**别人**那份登记摘掉的摘除函数。
func TestRegisterStopsWhenEnterFails(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	live(t, registry, newFakeAgent(t, "same", nil), nil)

	var announced int
	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(context.Context, Agent) error {
		announced++
		return nil
	}))

	detach, err := registry.Register(ctx, newFakeAgent(t, "same", nil), nil)
	if !errors.Is(err, ErrAgentAlreadyExists) {
		t.Fatalf("该报 ErrAgentAlreadyExists，得到 %v", err)
	}
	if detach != nil {
		t.Fatal("进不了表就不该交出摘除函数")
	}
	if announced != 0 {
		t.Fatalf("进不了表就不该公布：%d", announced)
	}
	if _, found := registry.Get("same"); !found {
		t.Fatal("先来那一份该原封不动地活着")
	}
}

// TestEnterDoesNotAnnounce 进表和公布是两件事：进表之后查得到，但一个创建观察者
// 都没被叫到。异步工厂靠这个缝把 setup 跑完、把摘除挂进拆除链。
func TestEnterDoesNotAnnounce(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	agent := newFakeAgent(t, "quiet", nil)

	var announced int
	keep := keeper(t)
	keep(registry.OnCreated(context.Background(), owner, func(context.Context, Agent) error {
		announced++
		return nil
	}))

	detach, err := registry.Enter(agent, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, found := registry.Get("quiet"); !found {
		t.Fatal("进表之后该查得到")
	}
	if announced != 0 {
		t.Fatalf("进表不该发公布：%d", announced)
	}
	if err := detach(context.Background()); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}
	if _, found := registry.Get("quiet"); found {
		t.Fatal("摘除之后不该还查得到")
	}
}

// TestDetachBeforeAnnounceEmitsNoDisposed 一次公布之前就撤掉的登记从来没有对外
// 创建过，为它发一条 disposed 等于凭空造出一条不可能的生命周期边。
func TestDetachBeforeAnnounceEmitsNoDisposed(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	agent := newFakeAgent(t, "quiet", nil)

	var disposed int
	keep := keeper(t)
	keep(registry.OnDisposed(context.Background(), owner, func(Agent) { disposed++ }))

	detach, err := registry.Enter(agent, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := detach(context.Background()); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}
	if disposed != 0 {
		t.Fatalf("没公布过就不该发 disposed：%d", disposed)
	}
}

// TestRegisterAnnouncesAndDetachEmitsDisposed 普通调用方那条路：一次调用走完进表
// 加公布，摘除时配对地发一次 disposed，而且摘除是幂等的。
func TestRegisterAnnouncesAndDetachEmitsDisposed(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)

	var created, disposed []sessionlog.SessionID
	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(_ context.Context, a Agent) error {
		created = append(created, a.ID())
		return nil
	}))
	keep(registry.OnDisposed(ctx, owner, func(a Agent) {
		disposed = append(disposed, a.ID())
	}))

	detach, err := registry.Register(ctx, agent, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if len(created) != 1 || created[0] != "one" {
		t.Fatalf("创建公布不对：%v", created)
	}

	if err := detach(ctx); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}
	if err := detach(ctx); err != nil {
		t.Fatalf("再摘一次失败：%v", err)
	}
	if len(disposed) != 1 || disposed[0] != "one" {
		t.Fatalf("摘除通知该恰好一次：%v", disposed)
	}
}

// TestRegisterRollsBackAVetoedAnnounce 一个创建观察者否决了公布，这次登记整个不
// 算数：表里查不到，而已经看见过它的那几个观察者会拿到配对的 disposed。
func TestRegisterRollsBackAVetoedAnnounce(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "vetoed", nil)

	var saw []sessionlog.SessionID
	boom := errors.New("我反对")
	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(_ context.Context, a Agent) error {
		saw = append(saw, a.ID())
		return nil
	}))
	keep(registry.OnCreated(ctx, owner, func(context.Context, Agent) error { return boom }))
	// 第三个在否决之后，不该被叫到。
	keep(registry.OnCreated(ctx, owner, func(_ context.Context, a Agent) error {
		saw = append(saw, "太晚了")
		return nil
	}))

	var disposed []sessionlog.SessionID
	keep(registry.OnDisposed(ctx, owner, func(a Agent) { disposed = append(disposed, a.ID()) }))

	if _, err := registry.Register(ctx, agent, nil); !errors.Is(err, boom) {
		t.Fatalf("该把否决那条错误交出来，得到 %v", err)
	}
	if len(saw) != 1 || saw[0] != "vetoed" {
		t.Fatalf("否决之后不该再往下跑：%v", saw)
	}
	if _, found := registry.Get("vetoed"); found {
		t.Fatal("被否决的登记不该留在表里")
	}
	if len(disposed) != 1 || disposed[0] != "vetoed" {
		t.Fatalf("已经看见过它的观察者该拿到配对的 disposed：%v", disposed)
	}
}

// TestAnnounceTurnsAPanicIntoAVeto 一个 panic 掉的创建观察者到底是「我反对」还是
// 「我这儿坏了」分不出来，一律当否决。
func TestAnnounceTurnsAPanicIntoAVeto(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "boom", nil)

	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(context.Context, Agent) error {
		panic("观察者炸了")
	}))
	if _, err := registry.Register(ctx, agent, nil); err == nil {
		t.Fatal("panic 该被当成否决")
	}
	if _, found := registry.Get("boom"); found {
		t.Fatal("被否决的登记不该留在表里")
	}
}

// TestAnnounceHappensExactlyOnce 公布过的登记再公布一次报 [ErrAlreadyAnnounced]：
// announced 记的是「开始过」，所以一次被否决掉的公布也算数。
func TestAnnounceHappensExactlyOnce(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	if err := registry.Announce(ctx, agent); !errors.Is(err, ErrAlreadyAnnounced) {
		t.Fatalf("该报 ErrAlreadyAnnounced，得到 %v", err)
	}
}

// TestAnnounceRejectsRecursion 一个创建观察者不能在回调里把同一份登记再公布一次
// ——announcing 在派发之前就置真，挡的就是这条。
func TestAnnounceRejectsRecursion(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)

	var inner error
	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(ctx context.Context, a Agent) error {
		inner = registry.Announce(ctx, a)
		return nil
	}))
	if _, err := registry.Register(ctx, agent, nil); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if !errors.Is(inner, ErrAlreadyAnnounced) {
		t.Fatalf("递归的公布该被拦下，得到 %v", inner)
	}
}

// TestDetachInsideAnnounceIsDeferred 从一个同步的创建观察者里提出的摘除，等那次
// 派发退栈之后再做——不然后面几个观察者会在一份已经被删掉的登记上收到创建通知。
func TestDetachInsideAnnounceIsDeferred(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)

	detach, err := registry.Enter(agent, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	var stillThere, laterSaw bool
	var disposed []sessionlog.SessionID
	keep := keeper(t)
	keep(registry.OnCreated(ctx, owner, func(ctx context.Context, _ Agent) error {
		if err := detach(ctx); err != nil {
			return err
		}
		_, stillThere = registry.Get("one")
		return nil
	}))
	keep(registry.OnCreated(ctx, owner, func(context.Context, Agent) error {
		laterSaw = true
		return nil
	}))
	keep(registry.OnDisposed(ctx, owner, func(a Agent) { disposed = append(disposed, a.ID()) }))

	if err := registry.Announce(ctx, agent); err != nil {
		t.Fatalf("公布失败：%v", err)
	}
	if !stillThere {
		t.Fatal("公布窗口里那次摘除该等窗口关掉再做")
	}
	if !laterSaw {
		t.Fatal("后面那个观察者照样该看见这次创建")
	}
	if _, found := registry.Get("one"); found {
		t.Fatal("窗口关掉之后该真的摘掉")
	}
	if len(disposed) != 1 {
		t.Fatalf("该配对地发一次 disposed：%v", disposed)
	}
}

// TestDisposedObserverPanicIsContainedAndLogged 摘除是配对通知，事情已经发生了：
// 一个坏掉的观察者既不能让它回头变成失败，也不能挡住后面的观察者看见它。
func TestDisposedObserverPanicIsContainedAndLogged(t *testing.T) {
	registry, logged := newLoggedRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)

	var later int
	keep := keeper(t)
	keep(registry.OnDisposed(ctx, owner, func(Agent) { panic("观察者炸了") }))
	keep(registry.OnDisposed(ctx, owner, func(Agent) { later++ }))

	detach, err := registry.Register(ctx, agent, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := detach(ctx); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}
	if later != 1 {
		t.Fatalf("后面那个观察者该照样被叫到：%d", later)
	}
	if !strings.Contains(logged.String(), "通知观察者 panic 了") {
		t.Fatalf("事故该被记下来：%s", logged.String())
	}
}

// ---- 查询 ----

// TestListAndRootsFollowRegistrationOrder 两个清单都按登记先后，Roots 只留下没有
// 运行期主人的那些，交回的切片改不动注册表。
func TestListAndRootsFollowRegistrationOrder(t *testing.T) {
	registry := newRegistry(t)
	first := newFakeAgent(t, "first", nil)
	second := newFakeAgent(t, "second", nil)
	child := newFakeAgent(t, "child", nil)
	live(t, registry, first, nil)
	live(t, registry, second, nil)
	live(t, registry, child, first)

	all := registry.List()
	if len(all) != 3 || all[0].ID() != "first" || all[1].ID() != "second" || all[2].ID() != "child" {
		t.Fatalf("清单次序不对：%v", idsOfAgents(all))
	}
	roots := registry.Roots()
	if len(roots) != 2 || roots[0].ID() != "first" || roots[1].ID() != "second" {
		t.Fatalf("根清单不对：%v", idsOfAgents(roots))
	}

	all[0] = nil
	if again := registry.List(); again[0] == nil {
		t.Fatal("改交出去的那份切片不该动到注册表")
	}
}

// TestGetAndIsOwnedBy 查得到活的、查不到没进过表的；归属问的是运行期那份主人。
func TestGetAndIsOwnedBy(t *testing.T) {
	registry := newRegistry(t)
	parent := newFakeAgent(t, "parent", nil)
	child := newFakeAgent(t, "child", nil)
	live(t, registry, parent, nil)
	live(t, registry, child, parent)

	got, found := registry.Get("child")
	if !found || got != Agent(child) {
		t.Fatalf("查回来的不对：%v %v", got, found)
	}
	if _, found := registry.Get("没这个"); found {
		t.Fatal("没进过表的不该查得到")
	}
	if !registry.IsOwnedBy("child", parent) {
		t.Fatal("child 该属于 parent")
	}
	if registry.IsOwnedBy("child", child) {
		t.Fatal("child 不该属于它自己")
	}
	if registry.IsOwnedBy("没这个", parent) {
		t.Fatal("没进过表的不该有归属")
	}
	if !registry.IsOwnedBy("parent", nil) {
		t.Fatal("顶层那个的主人是 nil")
	}
}

// idsOfAgents 把一串 agent 摊成身份，好拿来报错。
func idsOfAgents(agents []Agent) []sessionlog.SessionID {
	ids := make([]sessionlog.SessionID, len(agents))
	for index, agent := range agents {
		ids[index] = agent.ID()
	}
	return ids
}

// ---- 派发 ----

// TestDispatchRequiresThatExactLiveRegistration 每一条派发口都先确认「交进来的是
// 本表里那一份活登记」：nil、没进过表的、以及一个同名但不是这一份的，全都拦下。
func TestDispatchRequiresThatExactLiveRegistration(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	live(t, registry, newFakeAgent(t, "one", nil), nil)

	// 同名的另一个：身份查得到，但那不是这一份登记。
	impostor := newFakeAgent(t, "one", nil)
	stranger := newFakeAgent(t, "stranger", nil)

	dispatches := map[string]func(Agent) error{
		"Announce":     func(a Agent) error { return registry.Announce(ctx, a) },
		"ReportStatus": func(a Agent) error { return registry.ReportStatus(a, StatusRunning) },
		"ReportInboxInserted": func(a Agent) error {
			return registry.ReportInboxInserted(a, text("一"))
		},
		"ReportInboxDiscarded": func(a Agent) error {
			return registry.ReportInboxDiscarded(a, text("一"))
		},
		"ReportInboxClaimed": func(a Agent) error {
			return registry.ReportInboxClaimed(a, text("一"), 1)
		},
		"ReportSessionStart": func(a Agent) error {
			return registry.ReportSessionStart(a, StartStartup)
		},
		"ReportError":  func(a Agent) error { return registry.ReportError(TurnError{Agent: a}) },
		"TurnStopping": func(a Agent) error { return registry.TurnStopping(ctx, a, 1) },
		"ResolvePreStep": func(a Agent) error {
			_, err := registry.ResolvePreStep(ctx, PreStep{Agent: a},
				func(context.Context) (PreStepDecision, error) { return RejectStep(), nil })
			return err
		},
		"ResolveRequest": func(a Agent) error {
			_, err := registry.ResolveRequest(ctx, Request{Agent: a},
				func(context.Context) (llm.CallConfig, error) { return llm.CallConfig{}, nil })
			return err
		},
		"ResolveRequestError": func(a Agent) error {
			_, err := registry.ResolveRequestError(ctx, RequestFailure{Agent: a},
				func(context.Context) (RequestErrorAction, error) { return RequestErrorAction{}, nil })
			return err
		},
	}
	subjects := map[string]Agent{"nil": nil, "没进过表": stranger, "同名的另一个": impostor}

	for name, dispatch := range dispatches {
		t.Run(name, func(t *testing.T) {
			for subject, agent := range subjects {
				if err := dispatch(agent); !errors.Is(err, ErrAgentNotLive) {
					t.Fatalf("%s：该报 ErrAgentNotLive，得到 %v", subject, err)
				}
			}
		})
	}
}

// TestReportStatusRejectsANoop 同一个状态连着报两次是循环那一层的缺陷，拦在这里，
// 而且不派发；第一次报任何状态都不算重复。
func TestReportStatusRejectsANoop(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var seen []Status
	keep := keeper(t)
	keep(registry.OnStatus(context.Background(), owner, func(_ Agent, status Status) {
		seen = append(seen, status)
	}))

	// 第一次报的就是 agent 当下那个状态，照样不算重复。
	if err := registry.ReportStatus(agent, StatusIdle); err != nil {
		t.Fatalf("第一次报状态不该失败：%v", err)
	}
	if err := registry.ReportStatus(agent, StatusIdle); !errors.Is(err, ErrStatusNoop) {
		t.Fatalf("该报 ErrStatusNoop，得到 %v", err)
	}
	if err := registry.ReportStatus(agent, StatusRunning); err != nil {
		t.Fatalf("真的跃迁不该失败：%v", err)
	}
	if len(seen) != 2 || seen[0] != StatusIdle || seen[1] != StatusRunning {
		t.Fatalf("不动的那次跃迁不该派发：%v", seen)
	}
}

// TestNotificationsReachTheirObservers 五条只通知的边各自把那几样原样带到。
func TestNotificationsReachTheirObservers(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var inserted, discarded []llm.MessageID
	var claimed []claimRecord
	var starts []SessionStartSource
	var failures []TurnError

	keep := keeper(t)
	keep(registry.OnInboxInserted(ctx, owner, func(_ Agent, m llm.Message) {
		inserted = append(inserted, m.ID)
	}))
	keep(registry.OnInboxDiscarded(ctx, owner, func(_ Agent, m llm.Message) {
		discarded = append(discarded, m.ID)
	}))
	keep(registry.OnInboxClaimed(ctx, owner, func(_ Agent, m llm.Message, turn int) {
		claimed = append(claimed, claimRecord{id: m.ID, turn: turn})
	}))
	keep(registry.OnSessionStart(ctx, owner, func(_ Agent, source SessionStartSource) {
		starts = append(starts, source)
	}))
	keep(registry.OnError(ctx, owner, func(failure TurnError) {
		failures = append(failures, failure)
	}))

	first, second, third := text("插进来的"), text("丢掉的"), text("认领的")
	boom := errors.New("步骤炸了")
	for _, report := range []func() error{
		func() error { return registry.ReportInboxInserted(agent, first) },
		func() error { return registry.ReportInboxDiscarded(agent, second) },
		func() error { return registry.ReportInboxClaimed(agent, third, 7) },
		func() error { return registry.ReportSessionStart(agent, StartResume) },
		func() error {
			return registry.ReportError(TurnError{Agent: agent, Turn: 2, Step: 3, Err: boom})
		},
	} {
		if err := report(); err != nil {
			t.Fatalf("通知失败：%v", err)
		}
	}

	if len(inserted) != 1 || inserted[0] != first.ID {
		t.Fatalf("插入报得不对：%v", inserted)
	}
	if len(discarded) != 1 || discarded[0] != second.ID {
		t.Fatalf("丢弃报得不对：%v", discarded)
	}
	if len(claimed) != 1 || claimed[0].id != third.ID || claimed[0].turn != 7 {
		t.Fatalf("认领报得不对：%+v", claimed)
	}
	if len(starts) != 1 || starts[0] != StartResume {
		t.Fatalf("会话开始报得不对：%v", starts)
	}
	if len(failures) != 1 || failures[0].Turn != 2 || failures[0].Step != 3 || !errors.Is(failures[0].Err, boom) {
		t.Fatalf("回合失败报得不对：%+v", failures)
	}
}

// TestNotifyObserverPanicIsContainedAndLogged 只通知那八条边上，一个坏掉的观察者
// 不能挡住后面的观察者看见这件已经发生的事。
func TestNotifyObserverPanicIsContainedAndLogged(t *testing.T) {
	registry, logged := newLoggedRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var later int
	keep := keeper(t)
	keep(registry.OnStatus(ctx, owner, func(Agent, Status) { panic("观察者炸了") }))
	keep(registry.OnStatus(ctx, owner, func(Agent, Status) { later++ }))

	if err := registry.ReportStatus(agent, StatusRunning); err != nil {
		t.Fatalf("一个 panic 掉的观察者不该让通知失败：%v", err)
	}
	if later != 1 {
		t.Fatalf("后面那个观察者该照样被叫到：%d", later)
	}
	if !strings.Contains(logged.String(), "通知观察者 panic 了") {
		t.Fatalf("事故该被记下来：%s", logged.String())
	}
}

// TestTurnStoppingRunsSeriallyAndStopsAtTheFirstFailure 回合收尾那条边界在提交
// 之前，一个失败的观察者说明收尾这件事本身出了问题，当场停下交出去。
func TestTurnStoppingRunsSeriallyAndStopsAtTheFirstFailure(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var order []string
	boom := errors.New("收不了尾")
	keep := keeper(t)
	keep(registry.OnTurnStopping(ctx, owner, func(_ context.Context, _ Agent, turn int) error {
		order = append(order, "第一个")
		if turn != 5 {
			t.Errorf("回合号没带到：%d", turn)
		}
		return nil
	}))
	keep(registry.OnTurnStopping(ctx, owner, func(context.Context, Agent, int) error {
		order = append(order, "第二个")
		return boom
	}))
	keep(registry.OnTurnStopping(ctx, owner, func(context.Context, Agent, int) error {
		order = append(order, "第三个")
		return nil
	}))

	if err := registry.TurnStopping(ctx, agent, 5); !errors.Is(err, boom) {
		t.Fatalf("该把那条失败交出来，得到 %v", err)
	}
	if len(order) != 2 || order[0] != "第一个" || order[1] != "第二个" {
		t.Fatalf("该在第一个失败上停下：%v", order)
	}
}

// TestTurnStoppingTurnsAPanicIntoAFailure 一个 panic 掉的收尾观察者到底是「我反对」
// 还是「我这儿坏了」分不出来，当成失败最坏是多跑一个步骤。
func TestTurnStoppingTurnsAPanicIntoAFailure(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	keep := keeper(t)
	keep(registry.OnTurnStopping(ctx, owner, func(context.Context, Agent, int) error {
		panic("观察者炸了")
	}))
	if err := registry.TurnStopping(ctx, agent, 1); err == nil {
		t.Fatal("panic 该被当成失败")
	}
}

// TestTurnStoppingWithoutObserversPasses 一个观察者都没有时它直接通过。
func TestTurnStoppingWithoutObserversPasses(t *testing.T) {
	registry := newRegistry(t)
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	if err := registry.TurnStopping(context.Background(), agent, 1); err != nil {
		t.Fatalf("没有观察者时该直接通过：%v", err)
	}
}

// ---- 三条瀑布 ----

// TestPreStepWaterfallNestsEarlierRegistrationsOutside 先登记的在外层，一层层往里
// 套，最里面那个 next 交出机器本来的提议。
func TestPreStepWaterfallNestsEarlierRegistrationsOutside(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var order []string
	keep := keeper(t)
	keep(registry.OnPreStep(ctx, owner, func(
		ctx context.Context, _ PreStep, next func(context.Context) (PreStepDecision, error),
	) (PreStepDecision, error) {
		order = append(order, "进外层")
		decision, err := next(ctx)
		order = append(order, "出外层")
		return decision, err
	}))
	keep(registry.OnPreStep(ctx, owner, func(
		ctx context.Context, step PreStep, next func(context.Context) (PreStepDecision, error),
	) (PreStepDecision, error) {
		order = append(order, "进里层")
		if step.Turn != 3 || step.Step != 4 {
			t.Errorf("提议没带到：%+v", step)
		}
		decision, err := next(ctx)
		order = append(order, "出里层")
		return decision, err
	}))

	message := text("唤醒")
	decision, err := registry.ResolvePreStep(ctx,
		PreStep{Agent: agent, Messages: []llm.Message{message}, Turn: 3, Step: 4},
		func(context.Context) (PreStepDecision, error) {
			order = append(order, "机器本来的")
			return EnterStep([]llm.Message{message}), nil
		},
	)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if !decision.Enter || len(decision.Messages) != 1 || decision.Messages[0].ID != message.ID {
		t.Fatalf("交出来的决定不对：%+v", decision)
	}
	want := []string{"进外层", "进里层", "机器本来的", "出里层", "出外层"}
	if len(order) != len(want) {
		t.Fatalf("嵌套次序不对：%v", order)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("嵌套次序不对：%v", order)
		}
	}
}

// TestPreStepWaterfallShortCircuits 不调 next 就否掉了后面所有人，包括机器本来
// 那个提议。
func TestPreStepWaterfallShortCircuits(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var reached bool
	keep := keeper(t)
	keep(registry.OnPreStep(ctx, owner, func(
		context.Context, PreStep, func(context.Context) (PreStepDecision, error),
	) (PreStepDecision, error) {
		return RejectStep(), nil
	}))
	keep(registry.OnPreStep(ctx, owner, func(
		ctx context.Context, _ PreStep, next func(context.Context) (PreStepDecision, error),
	) (PreStepDecision, error) {
		reached = true
		return next(ctx)
	}))

	decision, err := registry.ResolvePreStep(ctx, PreStep{Agent: agent},
		func(context.Context) (PreStepDecision, error) {
			reached = true
			return EnterStep(nil), nil
		},
	)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if decision.Enter {
		t.Fatalf("外层那个拒绝该说了算：%+v", decision)
	}
	if reached {
		t.Fatal("短路之后不该再往里走")
	}
}

// TestPreStepWaterfallPropagatesAFailure 里层交出来的错误原样往上走。
func TestPreStepWaterfallPropagatesAFailure(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	boom := errors.New("准入算不出来")
	if _, err := registry.ResolvePreStep(ctx, PreStep{Agent: agent},
		func(context.Context) (PreStepDecision, error) { return PreStepDecision{}, boom },
	); !errors.Is(err, boom) {
		t.Fatalf("该原样交出来，得到 %v", err)
	}
}

// TestRequestWaterfallReplacesTheConfig 一个观察者换掉那份已经定下来的调用配置。
func TestRequestWaterfallReplacesTheConfig(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	keep := keeper(t)
	keep(registry.OnRequest(ctx, owner, func(
		ctx context.Context, request Request, next func(context.Context) (llm.CallConfig, error),
	) (llm.CallConfig, error) {
		if request.Turn != 1 || request.Step != 2 {
			t.Errorf("请求没带到：%+v", request)
		}
		config, err := next(ctx)
		if err != nil {
			return llm.CallConfig{}, err
		}
		config.Model = "换过的"
		return config, nil
	}))

	got, err := registry.ResolveRequest(ctx, Request{Agent: agent, Turn: 1, Step: 2},
		func(context.Context) (llm.CallConfig, error) {
			return llm.CallConfig{Provider: "本来的", Model: "本来的模型"}, nil
		},
	)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if got.Provider != "本来的" || got.Model != "换过的" {
		t.Fatalf("交出来的配置不对：%+v", got)
	}
}

// TestRequestWaterfallWithoutObserversUsesTheBase 一个观察者都没有时，交出来的就是
// 机器本来那一份。
func TestRequestWaterfallWithoutObserversUsesTheBase(t *testing.T) {
	registry := newRegistry(t)
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	base := llm.CallConfig{Provider: "本来的", Model: "本来的模型"}
	got, err := registry.ResolveRequest(context.Background(), Request{Agent: agent},
		func(context.Context) (llm.CallConfig, error) { return base, nil },
	)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if !llm.CallConfigEquals(got, base) {
		t.Fatalf("该原样交出来：%+v", got)
	}
}

// TestRequestErrorWaterfallClaimsRecovery 认领了恢复的观察者不调 next，直接说重试；
// 没人认领时那条默认的零值让这次失败成为终局。
func TestRequestErrorWaterfallClaimsRecovery(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	base := func(context.Context) (RequestErrorAction, error) { return RequestErrorAction{}, nil }
	failure := RequestFailure{Agent: agent, Turn: 1, Step: 1, Provider: "月球"}

	action, err := registry.ResolveRequestError(ctx, failure, base)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if action.Retry {
		t.Fatal("没人认领时这次失败该是终局")
	}

	keep := keeper(t)
	keep(registry.OnRequestError(ctx, owner, func(
		_ context.Context, got RequestFailure, _ func(context.Context) (RequestErrorAction, error),
	) (RequestErrorAction, error) {
		if got.Provider != "月球" {
			t.Errorf("失败没带到：%+v", got)
		}
		return RequestErrorAction{Retry: true}, nil
	}))

	action, err = registry.ResolveRequestError(ctx, failure, base)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if !action.Retry {
		t.Fatal("认领了恢复该说重试")
	}
}

// TestRequestErrorWaterfallPassesAlong 一个不认领的观察者把这次失败交给下一层，
// 一路交到 base 手上。
func TestRequestErrorWaterfallPassesAlong(t *testing.T) {
	registry := newRegistry(t)
	owner := rootScope(t)
	ctx := context.Background()
	agent := newFakeAgent(t, "one", nil)
	live(t, registry, agent, nil)

	var reachedBase bool
	base := func(context.Context) (RequestErrorAction, error) {
		reachedBase = true
		return RequestErrorAction{Retry: true}, nil
	}

	keep := keeper(t)
	keep(registry.OnRequestError(ctx, owner, func(
		ctx context.Context, _ RequestFailure, next func(context.Context) (RequestErrorAction, error),
	) (RequestErrorAction, error) {
		return next(ctx)
	}))

	action, err := registry.ResolveRequestError(ctx, RequestFailure{Agent: agent}, base)
	if err != nil {
		t.Fatalf("瀑布失败：%v", err)
	}
	if !reachedBase || !action.Retry {
		t.Fatalf("不认领的观察者该把决定交给 base：%v %+v", reachedBase, action)
	}
}

// ---- 作用域过滤 ----

// TestDispatchIsFilteredByScope 派发认的是那个 agent 自己的载体作用域：全局层看得见
// 每一个，祖先那一层看得见挂在它下面的，不相干的那一支一个都看不见。
func TestDispatchIsFilteredByScope(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	parent := keyedScope(t, "parent", nil)
	unrelated := keyedScope(t, "unrelated", nil)
	agent := newFakeAgent(t, "child", parent.Key())
	live(t, registry, agent, nil)

	var order []string
	record := func(label string) StatusObserver {
		return func(Agent, Status) { order = append(order, label) }
	}
	// 登记次序故意和期望的派发次序不同：定次序的是载体那条父链，不是登记先后。
	keep := keeper(t)
	keep(registry.OnStatus(ctx, agent.Scope(), record("载体")))
	keep(registry.OnStatus(ctx, unrelated, record("不相干")))
	keep(registry.OnStatus(ctx, parent, record("祖先")))
	keep(registry.OnStatus(ctx, rootScope(t), record("全局")))

	if err := registry.ReportStatus(agent, StatusRunning); err != nil {
		t.Fatalf("报状态失败：%v", err)
	}
	want := []string{"全局", "祖先", "载体"}
	if len(order) != len(want) {
		t.Fatalf("派发名单不对：%v", order)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("派发次序不对：%v", order)
		}
	}
}

// TestDispatchToAnIdentitylessCarrierSeesOnlyTheGlobalLayer 一个作用域没有身份的
// agent 的载体钥匙是 nil：它落在全局层，也就只看得见全局层那些登记。走这条的是
// [github.com/snight1983/ds-harness-go/core/scope.NewRoot] 造出来的顶层作用域。
func TestDispatchToAnIdentitylessCarrierSeesOnlyTheGlobalLayer(t *testing.T) {
	registry := newRegistry(t)
	ctx := context.Background()
	elsewhere := keyedScope(t, "elsewhere", nil)

	agent := newFakeAgent(t, "rootless", nil)
	agent.scope = rootScope(t)
	live(t, registry, agent, nil)
	if agent.Scope().Key() != nil {
		t.Fatal("这个测试要的是一个没有身份的作用域")
	}

	var seen []string
	record := func(label string) StatusObserver {
		return func(Agent, Status) { seen = append(seen, label) }
	}
	keep := keeper(t)
	keep(registry.OnStatus(ctx, rootScope(t), record("全局")))
	keep(registry.OnStatus(ctx, elsewhere, record("有身份的那一层")))

	if err := registry.ReportStatus(agent, StatusRunning); err != nil {
		t.Fatalf("报状态失败：%v", err)
	}
	if len(seen) != 1 || seen[0] != "全局" {
		t.Fatalf("没有身份的载体只该看得见全局层：%v", seen)
	}
}
