// 本文件的作用：存储那一半的测试——登记、公布、摘除三步怎么配对，四组观察者
// 各自的失败语义，以及在公布／发布窗口里提出的摘除什么时候补上。

package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

func TestNewStoreFillsInTheDefaults(t *testing.T) {
	store, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if store.logger == nil || store.now == nil {
		t.Fatalf("默认没补上：logger=%v now 是否为 nil=%v", store.logger, store.now == nil)
	}
	if store.now() <= 0 {
		t.Fatalf("默认时钟给出 %d", store.now())
	}
	if sessions := store.List(); len(sessions) != 0 {
		t.Fatalf("新存储该是空的：%#v", sessions)
	}
}

func TestRegisteringANilObserverIsRejected(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	registrations := map[string]func() (func(context.Context) error, error){
		"created":  func() (func(context.Context) error, error) { return store.OnCreated(ctx, owner, nil) },
		"disposed": func() (func(context.Context) error, error) { return store.OnDisposed(ctx, owner, nil) },
		"event":    func() (func(context.Context) error, error) { return store.OnEvent(ctx, owner, nil) },
		"flush":    func() (func(context.Context) error, error) { return store.OnFlush(ctx, owner, nil) },
	}
	for name, register := range registrations {
		t.Run(name, func(t *testing.T) {
			if _, err := register(); err == nil {
				t.Fatal("nil 观察者该被拒")
			}
		})
	}
}

func TestRegisteringWithoutAnOwnerIsRejected(t *testing.T) {
	// 一次登记的寿命由作用域界定，没有作用域就没有「什么时候撤销」这回事。
	store := newStore(t)
	if _, err := store.OnEvent(context.Background(), nil, func(*Session, sessionlog.Event) {}); err == nil {
		t.Fatal("没有作用域该被拒")
	}
}

func TestCreateRegistersAnnouncesAndListsInOrder(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	var announced []sessionlog.SessionID
	undo, err := store.OnCreated(ctx, owner, func(_ context.Context, session *Session) error {
		announced = append(announced, session.ID())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = undo(ctx) }()

	first := liveSession(t, store, owner, "a", CreateOptions{Cwd: testAbsolutePath})
	second := liveSession(t, store, owner, "b", CreateOptions{})

	if got, ok := store.Get("a"); !ok || got != first {
		t.Fatalf("按标识查不到刚建的会话：ok=%v", ok)
	}
	if _, ok := store.Get("nope"); ok {
		t.Fatal("不存在的标识不该查得到")
	}
	sessions := store.List()
	if len(sessions) != 2 || sessions[0] != first || sessions[1] != second {
		t.Fatalf("清单该按登记先后：%#v", sessions)
	}
	if len(announced) != 2 || announced[0] != "a" || announced[1] != "b" {
		t.Fatalf("公布顺序是 %#v", announced)
	}
	// 头上带着创建时给的那几项。
	header := first.Header()
	if header.Cwd != testAbsolutePath || header.ID != "a" || header.CreatedAt == 0 {
		t.Fatalf("装出来的头是 %#v", header)
	}
}

func TestCreateStampsTheDurableHeaderFields(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	session := liveSession(t, store, owner, "child", CreateOptions{
		Seed:            seedOf(userEvent(t, "你好")),
		Cwd:             testAbsolutePath,
		ParentSession:   "parent",
		CreatedAt:       77,
		SeedLength:      1,
		Origin:          sessionlog.OriginSubagent,
		DelegationDepth: 2,
		AgentPreset:     "coder",
	})
	header := session.Header()
	want := sessionlog.SessionHeader{
		Version:         sessionlog.FormatVersion,
		ID:              "child",
		CreatedAt:       77,
		Cwd:             testAbsolutePath,
		ParentSession:   "parent",
		SeedLength:      1,
		Origin:          sessionlog.OriginSubagent,
		DelegationDepth: 2,
		AgentPreset:     "coder",
	}
	if header != want {
		t.Fatalf("头是 %#v，该是 %#v", header, want)
	}
}

func TestCreateFailsCleanlyWhenTheOwnerCannotHoldTheSession(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	// 没有载体作用域：登记那一步就过不去，会话造出来了但没进表。
	if _, err := store.Create(ctx, nil, "a", CreateOptions{}); err == nil {
		t.Fatal("没有载体作用域该被拒")
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("失败的创建留下了一份登记")
	}

	// 作用域已经释放：登记进得去，但挂不上摘除。这时候登记必须被回滚掉，
	// 否则这个会话就成了一份没人持有、也没人摘得掉的表项。
	disposed := scope.NewRoot()
	if err := disposed.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, disposed, "b", CreateOptions{}); !errors.Is(err, scope.ErrScopeDisposed) {
		t.Fatalf("诊断是 %v", err)
	}
	if _, ok := store.Get("b"); ok {
		t.Fatal("挂不上摘除的创建留下了一份登记")
	}
}

func TestCreateRejectsADuplicateID(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	liveSession(t, store, owner, "a", CreateOptions{})

	_, err := store.Create(context.Background(), owner, "a", CreateOptions{})
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestCreateRefusesAHeaderItCannotBuild(t *testing.T) {
	// 装头这一步会验：一条相对路径的工作目录在这里就被挡下，会话根本没造出来。
	store := newStore(t)
	owner := rootScope(t)
	_, err := store.Create(context.Background(), owner, "a", CreateOptions{Cwd: "relative"})
	if !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("诊断是 %v", err)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("失败的创建留下了一份登记")
	}
}

func TestAnUnnamedSessionGetsAMintedIDThatSkipsTakenNames(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	// 先手工占掉铸造序列的第一个名字，逼铸造循环往下走。
	liveSession(t, store, owner, "session-1", CreateOptions{})

	minted := liveSession(t, store, owner, "", CreateOptions{})
	if minted.ID() != "session-2" {
		t.Fatalf("铸出来的标识是 %q", minted.ID())
	}
	next := liveSession(t, store, owner, "", CreateOptions{})
	if next.ID() != "session-3" {
		t.Fatalf("第二个铸出来的是 %q", next.ID())
	}
}

func TestPrepareEnterAnnounceIsTheSameThingSplitInThree(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 还没登记：查不到。
	if _, ok := store.Get("a"); ok {
		t.Fatal("prepare 不该登记")
	}
	detach, err := store.Enter(owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Get("a"); !ok || got != session {
		t.Fatalf("登记之后该查得到：ok=%v", ok)
	}
	if err := store.Announce(ctx, session); err != nil {
		t.Fatal(err)
	}
	// 公布过一次就不能再来一次。
	if err := store.Announce(ctx, session); !errors.Is(err, ErrAlreadyAnnounced) {
		t.Fatalf("诊断是 %v", err)
	}
	if err := detach(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("摘除之后不该还查得到")
	}
	// 摘除是一次性的，再调什么都不做。
	if err := detach(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRestoredTakesOwnershipOfTheStoredLog(t *testing.T) {
	store := newStore(t)
	header := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: "a", CreatedAt: 9, SeedLength: 1}
	seed := seedOf(userEvent(t, "你好"))
	session, err := store.PrepareRestored("a", RestoreOptions{Seed: seed, Header: header})
	if err != nil {
		t.Fatal(err)
	}
	if session.Header().CreatedAt != 9 {
		t.Fatalf("存下来的头该原样保管：%#v", session.Header())
	}
	if &session.Events()[0].Data[0] != &seed[0].Data[0] {
		t.Fatal("恢复路径不该复制事件")
	}
	// 铸造走的是同一段代码：重名一样被拒。
	if _, err := store.Enter(rootScope(t), session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareRestored("a", RestoreOptions{Header: header}); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestEnterNeedsBothAnOwnerAndASession(t *testing.T) {
	store := newStore(t)
	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enter(nil, session); err == nil {
		t.Fatal("没有载体作用域该被拒")
	}
	if _, err := store.Enter(rootScope(t), nil); err == nil {
		t.Fatal("nil 会话该被拒")
	}
}

func TestEnterRejectsADuplicateNameAndAnAlreadyAttachedSession(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	liveSession(t, store, owner, "a", CreateOptions{})

	// 同名：一个在别处 prepare 出来的同名会话进不来。
	other := newStore(t)
	shadow, err := other.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enter(owner, shadow); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("诊断是 %v", err)
	}

	// 同一个对象登记两次：第二次撞的是「这个对象已经有主了」，和上面那条是两回事。
	attached, err := other.Prepare("b", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Enter(owner, attached); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Enter(owner, attached); !errors.Is(err, ErrAlreadyAttached) {
		t.Fatalf("诊断是 %v", err)
	}
	if _, ok := store.Get("b"); ok {
		t.Fatal("被拒的登记留下了一份表项")
	}
}

func TestAnnounceAndFlushRefuseASessionThatIsNotLiveHere(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	if err := store.Announce(ctx, nil); !errors.Is(err, ErrNotLive) {
		t.Fatalf("诊断是 %v", err)
	}
	if _, err := store.Flush(ctx, nil); !errors.Is(err, ErrNotLive) {
		t.Fatalf("诊断是 %v", err)
	}
	// 一个游离的（prepare 了但没 enter 的）会话同样不算活着。
	loose, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Announce(ctx, loose); !errors.Is(err, ErrNotLive) {
		t.Fatalf("诊断是 %v", err)
	}
	// 一个登记在**别的**存储里的会话，在这个存储里也不算活着。
	other := newStore(t)
	elsewhere := liveSession(t, other, rootScope(t), "b", CreateOptions{})
	if _, err := store.Flush(ctx, elsewhere); !errors.Is(err, ErrNotLive) {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestAVetoingCreatedObserverRollsTheCreationBack(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	var seen []sessionlog.SessionID
	var disposed []sessionlog.SessionID
	if _, err := store.OnCreated(ctx, owner, func(_ context.Context, session *Session) error {
		seen = append(seen, session.ID())
		return errors.New("我不同意")
	}); err != nil {
		t.Fatal(err)
	}
	// 第二个观察者压根跑不到：第一个否决之后当场停下。
	if _, err := store.OnCreated(ctx, owner, func(_ context.Context, session *Session) error {
		seen = append(seen, "第二个")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OnDisposed(ctx, owner, func(session *Session) {
		disposed = append(disposed, session.ID())
	}); err != nil {
		t.Fatal(err)
	}

	_, err := store.Create(ctx, owner, "a", CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "session/created listener rejected") {
		t.Fatalf("诊断是 %v", err)
	}
	if len(seen) != 1 || seen[0] != "a" {
		t.Fatalf("否决之后不该继续派发：%#v", seen)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("被否决的创建留下了一份登记")
	}
	// 那个观察者看见过这个会话，所以配对的 disposed 必须补上。
	if len(disposed) != 1 || disposed[0] != "a" {
		t.Fatalf("配对的摘除通知是 %#v", disposed)
	}
}

func TestAPanickingCreatedObserverAlsoVetoes(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.OnCreated(ctx, owner, func(context.Context, *Session) error {
		panic("装到一半炸了")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, owner, "a", CreateOptions{}); err == nil {
		t.Fatal("panic 的创建观察者该否决这次创建")
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("被否决的创建留下了一份登记")
	}
}

func TestObserversAreRoutedByTheCarrierScope(t *testing.T) {
	store := newStore(t)
	global := rootScope(t)
	agent := agentScope(t, "agent")
	ctx := context.Background()

	var globalSaw, agentSaw []sessionlog.SessionID
	if _, err := store.OnCreated(ctx, global, func(_ context.Context, session *Session) error {
		globalSaw = append(globalSaw, session.ID())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OnCreated(ctx, agent, func(_ context.Context, session *Session) error {
		agentSaw = append(agentSaw, session.ID())
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	liveSession(t, store, global, "外面的", CreateOptions{})
	liveSession(t, store, agent, "里面的", CreateOptions{})

	if len(globalSaw) != 2 {
		t.Fatalf("全局层该看见每一个：%#v", globalSaw)
	}
	if len(agentSaw) != 1 || agentSaw[0] != "里面的" {
		t.Fatalf("按作用域登记的只该看见自己那一支：%#v", agentSaw)
	}
}

func TestObserversReachDescendantCarrierScopes(t *testing.T) {
	store := newStore(t)
	parentKey := scope.NewKey("parent")
	parent, err := scope.New(parentKey, scope.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Dispose(context.Background()) })
	child, err := scope.New(scope.NewKey("child"), scope.Options{Parent: parentKey})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Dispose(context.Background()) })

	ctx := context.Background()
	var saw []sessionlog.SessionID
	if _, err := store.OnCreated(ctx, parent, func(_ context.Context, session *Session) error {
		saw = append(saw, session.ID())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 载体是子作用域，登记在父作用域上的观察者照样看得见。
	liveSession(t, store, child, "a", CreateOptions{})
	if len(saw) != 1 || saw[0] != "a" {
		t.Fatalf("父作用域该看见子作用域的会话：%#v", saw)
	}
}

func TestUndoingARegistrationStopsTheObserver(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	count := 0
	undo, err := store.OnCreated(ctx, owner, func(context.Context, *Session) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	liveSession(t, store, owner, "a", CreateOptions{})
	if err := undo(ctx); err != nil {
		t.Fatal(err)
	}
	liveSession(t, store, owner, "b", CreateOptions{})
	if count != 1 {
		t.Fatalf("撤销之后还被调了：count=%d", count)
	}
}

func TestEventObserversSeeCommittedAppendsAndTheirPanicsAreContained(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.OnEvent(ctx, owner, func(*Session, sessionlog.Event) {
		panic("这个观察者坏了")
	}); err != nil {
		t.Fatal(err)
	}
	var seen []int
	if _, err := store.OnEvent(ctx, owner, func(_ *Session, event sessionlog.Event) {
		seen = append(seen, event.Seq)
	}); err != nil {
		t.Fatal(err)
	}

	session := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal("一个 panic 的观察者不该让已经提交的追加失败：" + err.Error())
	}
	// 前面那个炸了，后面这个照样收到。
	if len(seen) != 1 || seen[0] != 0 {
		t.Fatalf("收到的是 %#v", seen)
	}
	if session.Seq() != 1 {
		t.Fatalf("日志长度是 %d", session.Seq())
	}
}

func TestAnEventObserverCannotAppendReentrantly(t *testing.T) {
	// 发布窗口开着的时候日志不许再动：那会让观察者看见的次序和日志里的次序对不上。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	var reentrant error
	if _, err := store.OnEvent(ctx, owner, func(session *Session, _ sessionlog.Event) {
		_, reentrant = session.Append(userEvent(t, "递归的"))
	}); err != nil {
		t.Fatal(err)
	}
	session := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(reentrant, ErrInvalidAppend) {
		t.Fatalf("递归追加的诊断是 %v", reentrant)
	}
	if session.Seq() != 1 {
		t.Fatalf("递归那一条不该进日志：seq=%d", session.Seq())
	}
}

func TestADetachRequestedInsideACreatedObserverLandsAfterTheAnnouncement(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	detach, err := store.Enter(owner, session)
	if err != nil {
		t.Fatal(err)
	}

	var duringAnnounce bool
	var disposed []sessionlog.SessionID
	if _, err := store.OnCreated(ctx, owner, func(_ context.Context, current *Session) error {
		_ = detach(ctx)
		// 公布还没走完，登记必须还在。
		_, duringAnnounce = store.Get("a")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OnDisposed(ctx, owner, func(current *Session) {
		disposed = append(disposed, current.ID())
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Announce(ctx, session); err != nil {
		t.Fatal(err)
	}
	if !duringAnnounce {
		t.Fatal("公布进行中不该真摘")
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("公布结束之后该把那次摘除补上")
	}
	if len(disposed) != 1 || disposed[0] != "a" {
		t.Fatalf("配对的摘除通知是 %#v", disposed)
	}
}

func TestADetachRequestedInsideAnEventObserverLandsAfterThePublish(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	detach, err := store.Enter(owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Announce(ctx, session); err != nil {
		t.Fatal(err)
	}

	var duringPublish bool
	var disposed int
	if _, err := store.OnDisposed(ctx, owner, func(*Session) { disposed++ }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OnEvent(ctx, owner, func(*Session, sessionlog.Event) {
		_ = detach(ctx)
		_, duringPublish = store.Get("a")
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	if !duringPublish {
		t.Fatal("发布窗口里不该真摘")
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("发布窗口关掉之后该把那次摘除补上")
	}
	if disposed != 1 {
		t.Fatalf("配对的摘除通知发了 %d 次", disposed)
	}
}

func TestDetachingASessionThatWasNeverAnnouncedIsSilent(t *testing.T) {
	// 一次没公布过的登记摘掉时不发配对通知：没人看见过它。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	disposed := 0
	if _, err := store.OnDisposed(ctx, owner, func(*Session) { disposed++ }); err != nil {
		t.Fatal(err)
	}
	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	detach, err := store.Enter(owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := detach(ctx); err != nil {
		t.Fatal(err)
	}
	if disposed != 0 {
		t.Fatalf("没公布过的登记发了 %d 次摘除通知", disposed)
	}
}

func TestADisposedObserverPanicIsContained(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.OnDisposed(ctx, owner, func(*Session) { panic("摘除观察者坏了") }); err != nil {
		t.Fatal(err)
	}
	reached := false
	if _, err := store.OnDisposed(ctx, owner, func(*Session) { reached = true }); err != nil {
		t.Fatal(err)
	}
	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	detach, err := store.Enter(owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Announce(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := detach(ctx); err != nil {
		t.Fatal(err)
	}
	if !reached {
		t.Fatal("前一个 panic 不该挡住后一个观察者")
	}
}

func TestDisposingTheOwnerScopeTakesTheSessionWithIt(t *testing.T) {
	store := newStore(t)
	owner := scope.NewRoot()
	ctx := context.Background()

	liveSession(t, store, owner, "a", CreateOptions{})
	if err := owner.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("a"); ok {
		t.Fatal("作用域释放之后会话该跟着走")
	}
}

func TestFlushSaysWhetherAnybodyIsListening(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	session := liveSession(t, store, owner, "a", CreateOptions{})
	ran, err := store.Flush(ctx, session)
	if ran || err != nil {
		t.Fatalf("没有观察者时该是 (false, nil)：ran=%v err=%v", ran, err)
	}

	var mutex sync.Mutex
	var seen []sessionlog.SessionID
	if _, err := store.OnFlush(ctx, owner, func(_ context.Context, current *Session) error {
		mutex.Lock()
		defer mutex.Unlock()
		seen = append(seen, current.ID())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ran, err = store.Flush(ctx, session)
	if !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if len(seen) != 1 || seen[0] != "a" {
		t.Fatalf("观察者收到的是 %#v", seen)
	}
}

func TestFlushReportsTheFirstFailureInRegistrationOrder(t *testing.T) {
	// 观察者是并行跑的，「第一个」说的必须是登记顺序，否则同一批观察者每次跑出来的
	// 诊断都不一样。这里让先登记的那个慢一点，先返回的那个反而排在后面。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	slow := make(chan struct{})
	if _, err := store.OnFlush(ctx, owner, func(context.Context, *Session) error {
		<-slow
		return errors.New("先登记的这个")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OnFlush(ctx, owner, func(context.Context, *Session) error {
		close(slow)
		return errors.New("后登记的这个")
	}); err != nil {
		t.Fatal(err)
	}
	ran, err := store.Flush(ctx, liveSession(t, store, owner, "a", CreateOptions{}))
	if !ran || err == nil || err.Error() != "先登记的这个" {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
}

func TestAPanickingFlushObserverBecomesAFailure(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.OnFlush(ctx, owner, func(context.Context, *Session) error {
		panic("落盘炸了")
	}); err != nil {
		t.Fatal(err)
	}
	ran, err := store.Flush(ctx, liveSession(t, store, owner, "a", CreateOptions{}))
	if !ran || err == nil || !strings.Contains(err.Error(), "落盘炸了") {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
}

func TestConcurrentCreatesEachGetTheirOwnMintedID(t *testing.T) {
	// 铸标识和登记是两次分开拿锁，所以这条走的是「铸出来的名字在 Enter 之前被别人
	// 占掉」那道权威检查所防的那类交错。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	const count = 16
	ids := make([]sessionlog.SessionID, count)
	errs := make([]error, count)
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			session, err := store.Create(ctx, owner, "", CreateOptions{})
			if err != nil {
				errs[index] = err
				return
			}
			ids[index] = session.ID()
		}()
	}
	group.Wait()

	seen := map[sessionlog.SessionID]bool{}
	for index := range count {
		if errs[index] != nil {
			t.Fatalf("第 %d 次创建失败：%v", index, errs[index])
		}
		if seen[ids[index]] {
			t.Fatalf("标识 %q 铸重了", ids[index])
		}
		seen[ids[index]] = true
	}
	if len(store.List()) != count {
		t.Fatalf("存储里有 %d 个会话", len(store.List()))
	}
}

func TestOneWriterAndManyConcurrentReadersDoNotRace(t *testing.T) {
	// 这条主要是给 -race 跑的：追加拿会话的锁并在锁里取观察者名单，读取那几个方法
	// 拿同一把锁，而观察者名单那一侧走的是不拿存储锁的那条路。
	//
	// 只有一个写者：一份会话日志只该有一个写者，见 [Session] 上 publishing 那个
	// 字段的注释。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.OnEvent(ctx, owner, func(session *Session, _ sessionlog.Event) {
		_ = session.SurfaceNodes()
	}); err != nil {
		t.Fatal(err)
	}
	session := liveSession(t, store, owner, "a", CreateOptions{})

	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for index := range 8 {
			if _, err := session.Append(userEvent(t, fmt.Sprint(index))); err != nil {
				t.Error(err)
			}
		}
	}()
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = session.Events()
			_ = session.Seq()
			_ = store.List()
			_, _ = store.Get("a")
		}()
	}
	group.Wait()
	if session.Seq() != 8 {
		t.Fatalf("日志长度是 %d", session.Seq())
	}
}

func TestASecondWriterIsRejectedWhileAnAppendIsBeingPublished(t *testing.T) {
	// 发布窗口开着的时候，别的 goroutine 追加同一个会话也进不来——DSH 那个
	// appending 标记在单线程里只挡得住递归，Go 这边它还挡住这一条。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	var concurrent error
	if _, err := store.OnEvent(ctx, owner, func(session *Session, _ sessionlog.Event) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, concurrent = session.Append(userEvent(t, "另一个写者"))
		}()
		<-done
	}); err != nil {
		t.Fatal(err)
	}
	session := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(concurrent, ErrInvalidAppend) {
		t.Fatalf("并发追加的诊断是 %v", concurrent)
	}
	if session.Seq() != 1 {
		t.Fatalf("那一条不该进日志：seq=%d", session.Seq())
	}
}
