// 本文件的作用：把「装上去、摘下来」这一段钉在它真会出错的边上——少给一个协作者
// 会怎样、谁才配装这一套、装到一半失败时收不收得干净、那个空闲钩子替谁说话，以及
// 摘的时候那个确切的反序漏掉会怎样。
//
// # 这些测试防的是什么错
//
//   - **给一个子 agent 也装上**。提醒是会话本地的，而子 agent 的会话随它那次调用
//     一起结束——给它建出来的提醒不会有人收。
//   - **同一个 agent 被装两遍**。第二遍会在工具那里撞名，而撞名那一次的回滚会把
//     第一遍装上的也带下水。
//   - **装不上却放它上台**。模型会看到三件工具不在，或者更糟——工具在、投影不在，
//     于是提醒永远只写进日志、一次都不响。所以装不上必须把这次创建否掉。
//   - **装到一半失败时把半套留在那儿**。空闲钩子装不上时，前面已经装好的三件工具
//     必须整个收回去。
//   - **那个空闲钩子替别人说话**。一个挂在它下面的子 agent 转空闲也去触发一次重算，
//     是白折一遍日志；这条多问一句是有意加的。
//   - **一段从没用过提醒的会话每次转空闲都被叫醒**。那是一道纯省事的闸。
//   - **摘的时候先摘工具再摘钩子**。工具都没了却还留着一个会去请求重算的钩子，那次
//     重算折出来的是一段没人再改得动的日志。
//   - **投影先于工具被摘掉**。那一瞬间模型建出来的提醒会写进日志却永远没人来响，
//     而它拿到的是一个成功的回执。
//   - **收摊时只停观察者不立 stopping**。一次正卡在中途的创建会照样把自己塞进表里，
//     而那时候这里已经在遍历表了。

package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
)

// installWorld 是一次 [Install] 用例要的全部家当。
type installWorld struct {
	t        *testing.T
	root     *scope.Scope
	agents   *stubAgents
	sessions *stubSessions
	tools    *tools.Runtime
	config   Config
}

func newInstallWorld(t *testing.T) *installWorld {
	t.Helper()
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	world := &installWorld{
		t:        t,
		root:     scopeOf(t, "install-root", nil),
		agents:   newEmptyStubAgents(),
		sessions: newStubSessions(),
		tools:    runtime,
	}
	world.config = Config{
		Agents:   world.agents,
		Sessions: world.sessions,
		Tools:    runtime,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return baseNow },
	}
	return world
}

// newAgent 造一个 agent 并把它放上台当根。
func (w *installWorld) newAgent(id string) *stubAgent {
	w.t.Helper()
	created := newStubAgent(w.t, id, w.root, nil)
	w.agents.admit(created)
	return created
}

// newInstallation 造一份和 [Install] 里那份一模一样的安装态，但把它交出来让用例
// 直接调 onCreated / dispose 并回看 owners 表。
//
// 走这条路而不是走 [Install]，是因为几条规则要验的正是「表里到底装没装上」，而
// [Install] 只交回一个摘除函数。[Install] 自己那几条（少协作者、装观察者失败）在
// 下面单独验。
func (w *installWorld) newInstallation() *installation {
	return &installation{
		config:       w.config,
		base:         context.WithoutCancel(w.t.Context()),
		transactions: newTransactions(),
		owners:       map[agent.Agent]*ownerState{},
	}
}

// hasAllTools 问这三件工具此刻在不在这把钥匙上。
func (w *installWorld) hasAllTools(owner *stubAgent) bool {
	for _, name := range []string{CreateToolName, ListToolName, DeleteToolName} {
		if !hasTool(w.tools, owner.Scope(), name) {
			return false
		}
	}
	return true
}

// ---- Install 自己那几条 ----

func TestInstallRequiresEveryCollaborator(t *testing.T) {
	world := newInstallWorld(t)
	cases := []struct {
		what   string
		owner  *scope.Scope
		mutate func(*Config)
	}{
		{"少了作用域", nil, func(*Config) {}},
		{"少了 agent 注册表", world.root, func(c *Config) { c.Agents = nil }},
		{"少了会话仓库", world.root, func(c *Config) { c.Sessions = nil }},
		{"少了工具运行时", world.root, func(c *Config) { c.Tools = nil }},
	}
	for _, each := range cases {
		t.Run(each.what, func(t *testing.T) {
			config := world.config
			each.mutate(&config)
			undo, err := Install(t.Context(), each.owner, config)
			if err == nil {
				t.Fatal("本该报错，却装上去了")
			}
			if undo != nil {
				t.Fatal("报错的那次安装不该交回一个摘除函数")
			}
		})
	}
}

func TestInstallFillsInTheOptionalCollaborators(t *testing.T) {
	// Logger 和 Now 是可以不给的：不给就该退到 [slog.Default] 和 [time.Now]，而不是
	// 留一个空指针等着在第一次告警时炸。
	world := newInstallWorld(t)
	world.config.Logger = nil
	world.config.Now = nil

	undo, err := Install(t.Context(), world.root, world.config)
	if err != nil {
		t.Fatalf("装 schedule 失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	// 装上去之后放一个根进来：补不上默认值的话，这一步会在造投影时炸。
	created := world.newAgent("owner")
	if err := world.agents.emitCreated(t.Context(), created); err != nil {
		t.Fatalf("这次创建本该被接住：%v", err)
	}
	if !world.hasAllTools(created) {
		t.Fatal("三件工具没装上")
	}
}

func TestInstallFailsWhenTheCreatedObserverCannotBeAttached(t *testing.T) {
	world := newInstallWorld(t)
	world.agents.createdErr = errors.New("总线满了")
	if _, err := Install(t.Context(), world.root, world.config); err == nil {
		t.Fatal("装创建观察者失败时本该报错")
	}
}

func TestInstallDisposeStopsTheCreatedObserverAndReportsItsFailure(t *testing.T) {
	world := newInstallWorld(t)
	world.agents.stopCreatedErr = errors.New("退订失败")
	undo, err := Install(t.Context(), world.root, world.config)
	if err != nil {
		t.Fatalf("装 schedule 失败：%v", err)
	}
	if err := undo(t.Context()); !errors.Is(err, world.agents.stopCreatedErr) {
		t.Fatalf("摘的时候报的是 %v，本该把退订那次失败带出来", err)
	}
	if order := world.agents.stopOrder(); len(order) != 1 || order[0] != "created" {
		t.Fatalf("摘过的是 %v", order)
	}
}

// ---- 谁才配装这一套 ----

func TestOnCreatedSkipsNonRootsAndDuplicatesAndStopping(t *testing.T) {
	world := newInstallWorld(t)
	install := world.newInstallation()

	// 不是根：一个子 agent 的会话随它那次调用结束，给它装出来的提醒不会有人收。
	child := newStubAgent(t, "child", world.root, nil)
	if err := install.onCreated(t.Context(), child); err != nil {
		t.Fatalf("不是根的那个本该被静静放过：%v", err)
	}
	if len(install.owners) != 0 || world.hasAllTools(child) {
		t.Fatal("给一个不是根的 agent 装上了")
	}

	// 是根：装上。
	owner := world.newAgent("owner")
	if err := install.onCreated(t.Context(), owner); err != nil {
		t.Fatalf("装根 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = install.dispose(context.Background(), func(context.Context) error { return nil }) })
	if len(install.owners) != 1 || !world.hasAllTools(owner) {
		t.Fatal("根 agent 没装上")
	}

	// 再来一次：静静放过。不放过的话第二遍会在工具那里撞名，而撞名那次的回滚会把
	// 第一遍装上的也带下水。
	if err := install.onCreated(t.Context(), owner); err != nil {
		t.Fatalf("重复的那次本该被静静放过：%v", err)
	}
	if len(install.owners) != 1 || !world.hasAllTools(owner) {
		t.Fatal("重复的那次把第一遍装上的动了")
	}

	// 正在收摊：不再接新的。
	install.mutex.Lock()
	install.stopping = true
	install.mutex.Unlock()
	late := world.newAgent("late")
	if err := install.onCreated(t.Context(), late); err != nil {
		t.Fatalf("收摊之后那次本该被静静放过：%v", err)
	}
	if world.hasAllTools(late) {
		t.Fatal("收摊之后还在接新的 agent")
	}
}

func TestOnCreatedVetoesTheCreationWhenAttachFails(t *testing.T) {
	// 空闲钩子装不上：这次创建必须被否掉，而且前面已经装好的三件工具要整个收回去。
	world := newInstallWorld(t)
	world.agents.statusErr = errors.New("总线满了")
	install := world.newInstallation()
	owner := world.newAgent("owner")

	err := install.onCreated(t.Context(), owner)
	if !errors.Is(err, world.agents.statusErr) {
		t.Fatalf("报的是 %v，本该把装钩子那次失败带出来", err)
	}
	if len(install.owners) != 0 {
		t.Fatal("装失败的那个还留在表里")
	}
	for _, name := range []string{CreateToolName, ListToolName, DeleteToolName} {
		if hasTool(world.tools, owner.Scope(), name) {
			t.Fatalf("装到一半失败，%s 却留在那儿", name)
		}
	}
}

func TestOnCreatedUndoesItselfWhenTheCreationIsAlreadyCancelled(t *testing.T) {
	// 上面那几步全是同步的，但注册表的观察者是可以被取消的；取消之后再把这一套留在
	// 表里就没人来摘了。
	world := newInstallWorld(t)
	install := world.newInstallation()
	owner := world.newAgent("owner")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := install.onCreated(ctx, owner); !errors.Is(err, context.Canceled) {
		t.Fatalf("报的是 %v，本该是那次取消", err)
	}
	if len(install.owners) != 0 {
		t.Fatal("已经取消的那次创建还留在表里")
	}
	if world.hasAllTools(owner) {
		t.Fatal("已经取消的那次创建把工具留下了")
	}
}

// ---- 那个空闲钩子 ----

// requested 问这份投影此刻排没排着一次驱动请求，并把它取走。
func requested(runtime *Runtime) bool {
	select {
	case <-runtime.requests:
		return true
	default:
		return false
	}
}

func TestAttachIdleHookOnlyListensToItsOwnAgentGoingIdle(t *testing.T) {
	// 这里**不**走 onCreated：那条路会 Start 起驱动协程，协程会把请求令牌取走，
	// 于是下面每一条断言都变成一场和调度器的赛跑。attach 自己是同步的。
	world := newInstallWorld(t)
	install := world.newInstallation()
	owner := world.newAgent("owner")
	// 这段会话用过提醒，所以那道省事的闸是开的。
	if _, err := owner.log.Append(changeEvent(createJSON(atRecordJSON))); err != nil {
		t.Fatalf("往日志里塞事件失败：%v", err)
	}

	runtime := newRuntime(t.Context(), owner, runtimeDeps{
		agents:       world.agents,
		sessions:     world.sessions,
		logger:       world.config.Logger,
		now:          world.config.Now,
		transactions: install.transactions,
	})
	teardown, err := install.attach(owner, runtime)
	if err != nil {
		t.Fatalf("装那一套失败：%v", err)
	}
	t.Cleanup(func() { _ = teardown(context.Background()) })

	// 别人转空闲：不关这份投影的事。DSH 那个处理器不问是谁报的，于是一个挂在它下面
	// 的子 agent 转空闲也会白触发一次重算。
	other := newStubAgent(t, "other", world.root, nil)
	world.agents.emitStatus(other, agent.StatusIdle)
	if requested(runtime) {
		t.Fatal("别人转空闲也触发了一次重算")
	}

	// 自己转到别的状态：也不算。
	world.agents.emitStatus(owner, agent.StatusRunning)
	if requested(runtime) {
		t.Fatal("转到非空闲状态也触发了一次重算")
	}

	// 自己转空闲：这才算。
	world.agents.emitStatus(owner, agent.StatusIdle)
	if !requested(runtime) {
		t.Fatal("自己转空闲本该触发一次重算")
	}
}

func TestAttachIdleHookSkipsSessionsThatNeverUsedSchedule(t *testing.T) {
	// 一段从没写过 schedule/change 的会话，转空闲时不必把投影叫醒一遍。
	world := newInstallWorld(t)
	install := world.newInstallation()
	owner := world.newAgent("owner")

	runtime := newRuntime(t.Context(), owner, runtimeDeps{
		agents:       world.agents,
		sessions:     world.sessions,
		logger:       world.config.Logger,
		now:          world.config.Now,
		transactions: install.transactions,
	})
	teardown, err := install.attach(owner, runtime)
	if err != nil {
		t.Fatalf("装那一套失败：%v", err)
	}
	t.Cleanup(func() { _ = teardown(context.Background()) })

	world.agents.emitStatus(owner, agent.StatusIdle)
	if requested(runtime) {
		t.Fatal("一段没用过提醒的会话被叫醒了")
	}

	// 写过一条之后就该叫醒了——这道闸不按 seedLength 切，多醒一次只是白折一遍，
	// 漏掉一次的代价是一条提醒不响。
	if _, err := owner.log.Append(changeEvent(createJSON(atRecordJSON))); err != nil {
		t.Fatalf("往日志里塞事件失败：%v", err)
	}
	world.agents.emitStatus(owner, agent.StatusIdle)
	if !requested(runtime) {
		t.Fatal("用过提醒的会话转空闲本该被叫醒")
	}
}

func TestAttachFailsWhenTheToolsCannotBeRegistered(t *testing.T) {
	// 工具装不上（这里是撞名）时，attach 必须整个失败，而不是留一份「投影和钩子都
	// 在、工具不在」的半套——那种状态下提醒永远只写进日志、一次都不响。
	world := newInstallWorld(t)
	install := world.newInstallation()
	owner := world.newAgent("owner")
	newRuntimeFor := func() *Runtime {
		return newRuntime(t.Context(), owner, runtimeDeps{
			agents:       world.agents,
			sessions:     world.sessions,
			logger:       world.config.Logger,
			now:          world.config.Now,
			transactions: install.transactions,
		})
	}
	first := newRuntimeFor()
	teardown, err := install.attach(owner, first)
	if err != nil {
		t.Fatalf("第一次装失败：%v", err)
	}
	t.Cleanup(func() { _ = teardown(context.Background()) })

	if _, err := install.attach(owner, newRuntimeFor()); err == nil {
		t.Fatal("撞名那一次本该失败")
	}
	// 撞名那次不该顺手把第一套的钩子摘掉。
	world.agents.emitStatus(owner, agent.StatusIdle)
	if order := world.agents.stopOrder(); len(order) != 0 {
		t.Fatalf("撞名那一次摘了 %v", order)
	}
}

// ---- 摘的次序 ----

func TestTearDownRemovesTheHookBeforeTheTools(t *testing.T) {
	// 反过来的话，工具都没了却还留着一个会去请求重算的钩子，那次重算折出来的是一段
	// 没人再改得动的日志。
	world := newInstallWorld(t)
	install := world.newInstallation()
	owner := world.newAgent("owner")

	toolsStillThere := false
	world.agents.onStopStatus = func() { toolsStillThere = world.hasAllTools(owner) }

	if err := install.onCreated(t.Context(), owner); err != nil {
		t.Fatalf("装根 agent 失败：%v", err)
	}
	if err := install.dispose(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("收摊失败：%v", err)
	}
	if !toolsStillThere {
		t.Fatal("摘钩子的那一刻工具已经没了，次序反了")
	}
	if world.hasAllTools(owner) {
		t.Fatal("收摊之后工具没摘干净")
	}
	if len(install.owners) != 0 {
		t.Fatal("收摊之后表里还留着东西")
	}
}

func TestDisposeReportsEveryTeardownFailure(t *testing.T) {
	// 一个 agent 摘失败不许把别的吞掉：这几条错要一起带出来。
	world := newInstallWorld(t)
	world.agents.stopStatusErr = errors.New("摘钩子失败")
	install := world.newInstallation()
	owner := world.newAgent("owner")
	if err := install.onCreated(t.Context(), owner); err != nil {
		t.Fatalf("装根 agent 失败：%v", err)
	}

	stopFailure := errors.New("停创建观察者失败")
	err := install.dispose(t.Context(), func(context.Context) error { return stopFailure })
	if !errors.Is(err, stopFailure) {
		t.Fatalf("报的是 %v，本该带上停观察者那次失败", err)
	}
	if !errors.Is(err, world.agents.stopStatusErr) {
		t.Fatalf("报的是 %v，本该带上摘钩子那次失败", err)
	}
}

// ---- 那两个小判断 ----

func TestIsRootAnswersFromTheRegistry(t *testing.T) {
	world := newInstallWorld(t)
	owner := world.newAgent("owner")
	stranger := newStubAgent(t, "stranger", world.root, nil)
	if !isRoot(world.agents, owner) {
		t.Fatal("放上台的那个本该算根")
	}
	if isRoot(world.agents, stranger) {
		t.Fatal("没放上台的那个被算成了根")
	}
}
