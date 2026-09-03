// 本文件验那个循环工厂：配置怎么校验、启动器身份怎么盖上去、并行上限怎么读透、
// 一个 agent 的公布与拆除按什么次序走，以及配置驱动的那些启动失败怎么通报出去。
//
// 源: packages/core/agent-loop/src/index.ts:1-713
//
// 整组用例都**不注册任何 llm 适配器**：本文件里没有一个 agent 会真的跑起一个回合
// ——公布出来的 agent 收件箱是空的，驱动不会起步。模型那条路归 agent_test.go。

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/settings"
)

// ---- 舞台 ----

// quietLogger 是一个把话全咽下去的记录器。
//
// 本文件里有整整一组用例**刻意**制造配置驱动的启动失败，而
// [AgentLoop.reportConfiguredStartupFailure] 每一次都会 Warn 一条。走默认记录器的话，
// 一次正常的 go test 会喷出十几条看起来像事故的告警——而它们全都是用例要的东西。
func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// factoryWorld 是这一组用例的舞台：五样依赖，加上持有工厂的那个作用域。
type factoryWorld struct {
	owner   *scope.Scope
	agents  *agent.Registry
	store   *session.Store
	models  *llm.Runtime
	tools   *tools.Runtime
	prompts *systemprompt.Registry
	deps    Deps
}

// newFactoryWorld 造一个舞台，此时还没有工厂。
func newFactoryWorld(t *testing.T) *factoryWorld {
	t.Helper()

	owner := rootScope(t)
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	toolRuntime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	// OmitHarnessIdentity: 那段宿主身份和本文件要验的东西没有关系，而它会在每一次
	// 装配里多出一段文本。
	prompts, err := systemprompt.NewRegistry(context.Background(), owner,
		systemprompt.Options{OmitHarnessIdentity: true})
	if err != nil {
		t.Fatalf("造系统提示词注册表失败：%v", err)
	}

	world := &factoryWorld{
		owner:   owner,
		agents:  agents,
		store:   newStore(t),
		models:  llm.NewRuntime(llm.RuntimeOptions{}),
		tools:   toolRuntime,
		prompts: prompts,
	}
	world.deps = Deps{
		Agents:       world.agents,
		Sessions:     world.store,
		LLM:          world.models,
		Tools:        world.tools,
		SystemPrompt: world.prompts,
	}
	return world
}

// install 装一个工厂，用例结束时按它那条拆除链拆掉。
//
// 一个舞台上只装得下一个工厂：造法是注册表上独一份的，那三个系统提示词变量的名字
// 也是独一份的。要装第二个的用例另起一个舞台。
func (w *factoryWorld) install(t *testing.T, config Config) *AgentLoop {
	t.Helper()
	if config.Logger == nil {
		config.Logger = quietLogger()
	}
	loop, unwind, err := New(context.Background(), w.deps, w.owner, config)
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}
	t.Cleanup(func() { _ = unwind(context.Background()) })
	return loop
}

// create 走 [AgentLoop.Create] 造一个 agent。
func (w *factoryWorld) create(t *testing.T, loop *AgentLoop, id sessionlog.SessionID) agent.Agent {
	t.Helper()
	live, err := loop.Create(context.Background(), id, agent.Options{Provider: "甲", Model: "m-1"}, "")
	if err != nil {
		t.Fatalf("造 agent %q 失败：%v", string(id), err)
	}
	return live
}

// ---- 可编排的持久化 ----

// fakePersistence 是一份可编排的会话持久化：哪些身份落过地由用例说了算，
// 读取本身也可以被换掉（用来验慢读、坏读和竞速）。
type fakePersistence struct {
	store *session.Store

	mutex    sync.Mutex
	archived []sessionlog.SessionID
	prepare  func(ctx context.Context, id sessionlog.SessionID) (*session.Session, error)
	listErr  error
	listed   int
	released int
}

// newPersistence 造一份持久化，archived 是那些「已经落过地」的身份。
func newPersistence(store *session.Store, archived ...sessionlog.SessionID) *fakePersistence {
	return &fakePersistence{store: store, archived: archived}
}

func (p *fakePersistence) Prepare(
	ctx context.Context,
	id sessionlog.SessionID,
) (*session.Preparation, error) {
	p.mutex.Lock()
	prepare, archived := p.prepare, slices.Contains(p.archived, id)
	p.mutex.Unlock()

	var (
		live *session.Session
		err  error
	)
	switch {
	case prepare != nil:
		live, err = prepare(ctx, id)
	case !archived:
		err = fmt.Errorf("存档里没有会话 %q", string(id))
	default:
		live, err = p.store.Prepare(id, session.CreateOptions{})
	}
	if err != nil {
		return nil, err
	}
	// 真的持久化编排器在准备期里攥着一份预留，这里拿一个计数器替它：
	// 「释放到底有没有被调」是这个接缝上唯一测得到的东西。
	return session.NewPreparation(live, session.PreparationOptions{
		Release: func() {
			p.mutex.Lock()
			defer p.mutex.Unlock()
			p.released++
		},
	}), nil
}

// releaseCount 报这份持久化交出去的准备期被释放过几次。
func (p *fakePersistence) releaseCount() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.released
}

func (p *fakePersistence) List(context.Context) ([]sessionlog.SessionHeader, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.listed++
	if p.listErr != nil {
		return nil, p.listErr
	}
	headers := make([]sessionlog.SessionHeader, 0, len(p.archived))
	for _, id := range p.archived {
		headers = append(headers, sessionlog.SessionHeader{ID: id})
	}
	return headers, nil
}

// listCount 报这份持久化被列过几次。
func (p *fakePersistence) listCount() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.listed
}

// ---- 可写的内存设置后端 ----

// memoryBackend 是一份可写的、只活在内存里的设置后端。
type memoryBackend struct {
	mutex    sync.Mutex
	document map[string]any
}

func (b *memoryBackend) Writable() bool { return true }

func (b *memoryBackend) Load(context.Context) (map[string]any, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	// 脱钩地交出去：[settings.Provider] 会把这份文档当成自己那一份存着。
	copied := make(map[string]any, len(b.document))
	for key, value := range b.document {
		copied[key] = value
	}
	return copied, nil
}

func (b *memoryBackend) Persist(_ context.Context, ns settings.Namespace, section map[string]any) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.document == nil {
		b.document = map[string]any{}
	}
	b.document[string(ns)] = section
	return nil
}

// newSettings 造一个用内存后端的设置服务。
func newSettings(t *testing.T) *settings.Provider {
	t.Helper()
	provider, err := settings.New(context.Background(), &memoryBackend{}, quietLogger())
	if err != nil {
		t.Fatalf("造设置服务失败：%v", err)
	}
	return provider
}

// ---- 启动器身份 ----

// TestApplyLauncherIdentitiesLeavesConfiguredIdentitiesAloneWhenNobodyIsNamed 钉住
// 没有启动器身份时那份配置原样穿过。
//
// 源: packages/core/agent-loop/src/index.ts:213-215
func TestApplyLauncherIdentitiesLeavesConfiguredIdentitiesAloneWhenNobodyIsNamed(t *testing.T) {
	t.Parallel()

	configured := []ConfiguredAgent{{ID: "甲", SessionID: "配置里那个"}}
	applied := applyLauncherIdentities(configured, nil)
	if len(applied) != 1 || applied[0].SessionID != "配置里那个" {
		t.Errorf("没人被点名时该原样穿过：%#v", applied)
	}
}

// TestApplyLauncherIdentitiesSwapsBothIdentityKeysTogether 钉住被点名的那一项
// **两个身份键一起换掉**。
//
// 源: packages/core/agent-loop/src/index.ts:216-232
//
// 只盖一个键的话，一份配置里写着 sessionId、启动器给的是 resumeSessionId 的条目，
// 会同时带上两个身份——而那正是 [validateConfiguredAgents] 判为互斥的那种形状。
// 更糟的是反过来：启动器说「新建这一个」，配置里那个 resumeSessionId 还留着，
// 于是它去续跑了一段启动器根本没要的历史。
func TestApplyLauncherIdentitiesSwapsBothIdentityKeysTogether(t *testing.T) {
	t.Parallel()

	configured := []ConfiguredAgent{
		{ID: "甲", SessionID: "配置里那个"},
		{ID: "乙", ResumeSessionID: "配置里续的那个"},
		{ID: "丙", SessionID: "没人动它"},
	}
	applied := applyLauncherIdentities(configured, ConfiguredAgentIdentities{
		"甲": {ID: "启动器要续的", Resume: true},
		"乙": {ID: "启动器要新建的"},
	})

	if applied[0].SessionID != "" || applied[0].ResumeSessionID != "启动器要续的" {
		t.Errorf("甲 该整个换成一次续跑：%#v", applied[0])
	}
	if applied[1].ResumeSessionID != "" || applied[1].SessionID != "启动器要新建的" {
		t.Errorf("乙 该整个换成一次新建：%#v", applied[1])
	}
	if applied[2].SessionID != "没人动它" {
		t.Errorf("没被点名的那一项该原样保留：%#v", applied[2])
	}
}

// TestApplyLauncherIdentitiesDoesNotTouchTheCallersSlice 钉住它交出来的是一份新表。
//
// 就地改的话，一份配置在工厂构造过程中被改掉，而调用方手里那份看起来没变——
// 之后任何一次按那份配置重建都会得到不一样的结果。
func TestApplyLauncherIdentitiesDoesNotTouchTheCallersSlice(t *testing.T) {
	t.Parallel()

	configured := []ConfiguredAgent{{ID: "甲", SessionID: "原来那个"}}
	applyLauncherIdentities(configured, ConfiguredAgentIdentities{"甲": {ID: "换的"}})
	if configured[0].SessionID != "原来那个" {
		t.Errorf("调用方那份表被就地改了：%#v", configured[0])
	}
}

// ---- 配置校验 ----

// TestResolveMaxParallelToolCallsTreatsZeroAsUnset 钉住 0 是「没给」而不是「零宽的池」。
//
// 源: packages/core/agent-loop/src/index.ts:132-139
//
// 见 [Config.MaxParallelToolCalls] 的字段说明：一个宽度为零的池一个工具调用都跑不动，
// 所以零值不和任何真实取值撞车，拿来当「没给」是安全的。当成真值的话，一份没写这一项
// 的配置会造出一个永远卡住的调度器。
func TestResolveMaxParallelToolCallsTreatsZeroAsUnset(t *testing.T) {
	t.Parallel()

	if got, err := resolveMaxParallelToolCalls(0); err != nil || got != DefaultMaxParallelToolCalls {
		t.Errorf("0 该解成默认值：got=%d err=%v", got, err)
	}
	if got, err := resolveMaxParallelToolCalls(1); err != nil || got != 1 {
		t.Errorf("1 是串行，该原样留下：got=%d err=%v", got, err)
	}
	if got, err := resolveMaxParallelToolCalls(7); err != nil || got != 7 {
		t.Errorf("正整数该原样留下：got=%d err=%v", got, err)
	}
	if _, err := resolveMaxParallelToolCalls(-1); err == nil {
		t.Error("负数该被拒")
	}
}

// TestAssertAgentOptionsRejectsANegativeOutputCap 钉住负的输出上限被拒、0 放行。
//
// 源: packages/core/agent-loop/src/index.ts:141-147
//
// 0 在本仓库里表示「不设上限」（见 [agent.Options].MaxTokens），对应 DSH 那边的
// undefined；把它一起拒掉的话，绝大多数不指定上限的配置都起不来。
func TestAssertAgentOptionsRejectsANegativeOutputCap(t *testing.T) {
	t.Parallel()

	if err := assertAgentOptions(agent.Options{}); err != nil {
		t.Errorf("不设上限该放行：%v", err)
	}
	if err := assertAgentOptions(agent.Options{MaxTokens: 1024}); err != nil {
		t.Errorf("正的上限该放行：%v", err)
	}
	if err := assertAgentOptions(agent.Options{MaxTokens: -1}); err == nil {
		t.Error("负的上限该被拒")
	}
}

// TestValidateConfiguredAgentsRejectsTwoIdentitiesOnOneEntry 钉住一项上两个身份键
// 互斥。
//
// 源: packages/core/agent-loop/src/index.ts:277-283
//
// 两个都给了的话，「这一项到底是新建还是续跑」没有答案，而这条分支决定了它会不会
// 去读一段已经落地的历史。
func TestValidateConfiguredAgentsRejectsTwoIdentitiesOnOneEntry(t *testing.T) {
	t.Parallel()

	err := validateConfiguredAgents([]ConfiguredAgent{
		{ID: "甲", SessionID: "这个", ResumeSessionID: "还有这个"},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("该说这两个键互斥，实际 %v", err)
	}
}

// TestValidateConfiguredAgentsRejectsADuplicateExactIdentity 钉住两项抢同一个确切身份
// 会被挡在任何一项起步之前。
//
// 源: packages/core/agent-loop/src/index.ts:284-291
//
// 放过去的话，两项里跑得快的那个占住这个身份，慢的那个在注册表上撞名失败——而
// 「哪一项赢」取决于启动次序，也就是说同一份配置每次开机的结果可能不一样。
// 这里刻意让一项走 sessionId、另一项走 resumeSessionId：撞的是**身份**，
// 不是那个键。
func TestValidateConfiguredAgentsRejectsADuplicateExactIdentity(t *testing.T) {
	t.Parallel()

	err := validateConfiguredAgents([]ConfiguredAgent{
		{ID: "甲", SessionID: "同一个"},
		{ID: "乙", ResumeSessionID: "同一个"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate exact session identity") {
		t.Errorf("该说这两项撞了身份，实际 %v", err)
	}
}

// TestValidateConfiguredAgentsLetsUnnamedEntriesCoexist 钉住没写确切身份的那些项
// 彼此不冲突。
//
// 它们各自会现铸一个随机身份（见 [AgentLoop.startFreshConfigured]），撞不上。
// 把它们也算进去的话，一份声明了三个同类 agent 的配置根本起不来。
func TestValidateConfiguredAgentsLetsUnnamedEntriesCoexist(t *testing.T) {
	t.Parallel()

	if err := validateConfiguredAgents([]ConfiguredAgent{{ID: "甲"}, {ID: "乙"}}); err != nil {
		t.Errorf("没写身份的那些项该共存：%v", err)
	}
}

// TestMustNamespacePanicsOnAnIllegalLiteral 钉住一个不合法的命名空间字面量当场炸。
//
// 见 [mustNamespace] 的说明：入参是包级字面量，它不合法说明本包写错了，
// 不是运行期可以恢复的情况。悄悄回落成某个能用的名字，会让本包的设置落在
// 一个谁都不认识的段里。
func TestMustNamespacePanicsOnAnIllegalLiteral(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("不合法的命名空间字面量该 panic")
		}
	}()
	mustNamespace("不 合 法")
}

// ---- 竞速与取消 ----

// TestAbortCauseStaysQuietOnALiveContext 钉住没取消时它不报错。
//
// 源: packages/core/agent-loop/src/index.ts:94-97
func TestAbortCauseStaysQuietOnALiveContext(t *testing.T) {
	t.Parallel()

	if err := abortCause(context.Background(), "甲"); err != nil {
		t.Errorf("活着的上下文不该有原因：%v", err)
	}
}

// TestAbortCauseNamesTheAgentWhenTheReasonIsPlain 钉住一次没有说明的取消被套上
// 这个 agent 的身份。
//
// 源: packages/core/agent-loop/src/index.ts:95-96
//
// 光一个 context.Canceled 说不出是谁被取消了。一次开机时并发起十个 agent 的启动，
// 十条一模一样的 "context canceled" 定位不到任何一个。
func TestAbortCauseNamesTheAgentWhenTheReasonIsPlain(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := abortCause(ctx, "甲")
	if err == nil || !strings.Contains(err.Error(), `agent "甲" creation aborted`) {
		t.Fatalf("该点出是哪个 agent，实际 %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Error("原来那个原因该还在链上")
	}
}

// TestAbortCausePassesARicherReasonThroughUntouched 钉住一个有信息量的原因原样交出去。
//
// 源: packages/core/agent-loop/src/index.ts:95
//
// 工厂拆除盖上去的是 [errLoopNotActive]，调用方靠 [errors.Is] 认它。再包一层
// 「creation aborted」的话，一次正常卸载看起来像是创建出了故障。
func TestAbortCausePassesARicherReasonThroughUntouched(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errLoopNotActive)

	if err := abortCause(ctx, "甲"); !errors.Is(err, errLoopNotActive) {
		t.Errorf("该原样交出那个原因，实际 %v", err)
	}
}

// TestRaceAbortHandsBackTheWorkThatWon 钉住活儿赢了竞速时结果原样返回。
//
// 源: packages/core/agent-loop/src/index.ts:99-118
func TestRaceAbortHandsBackTheWorkThatWon(t *testing.T) {
	t.Parallel()

	got, err := raceAbort(context.Background(), "甲",
		func() (string, error) { return "结果", nil }, nil)
	if err != nil || got != "结果" {
		t.Errorf("活儿赢了该交出它的结果：got=%q err=%v", got, err)
	}

	failure := errors.New("跑坏了")
	if _, err := raceAbort(context.Background(), "甲",
		func() (string, error) { return "", failure }, nil); !errors.Is(err, failure) {
		t.Errorf("活儿自己的错误该原样交出来，实际 %v", err)
	}
}

// TestRaceAbortRefusesBeforeStartingWorkOnADeadContext 钉住上下文已经死了时
// 那件活儿根本不起步。
//
// 源: packages/core/agent-loop/src/index.ts:100-102
//
// 起步再丢掉的话，一次已经被取消的创建照样会去敲一次持久化后端——而那正是这条
// 竞速要省掉的那次往返。
func TestRaceAbortRefusesBeforeStartingWorkOnADeadContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := false
	if _, err := raceAbort(ctx, "甲", func() (string, error) {
		started = true
		return "", nil
	}, nil); err == nil {
		t.Error("死掉的上下文该当场被拒")
	}
	if started {
		t.Error("上下文已经死了就不该起步那件活儿")
	}
}

// TestRaceAbortHandsALateResultToTheReleaser 钉住竞速输掉之后才到的那份结果
// 交给了收尾函数。
//
// 源: packages/core/agent-loop/src/index.ts:169-186（releaseAbandoned）
//
// 这条路上那份结果是一个**备好但没公布**的会话。没人收的话它就是一次静默的泄漏；
// 而且那个 goroutine 会一直挂在发送上，连同它扣着的东西一起留下。
func TestRaceAbortHandsALateResultToTheReleaser(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})
	started := make(chan struct{})
	released := make(chan string, 1)

	// 取消必须落在这件活儿**起步之后**：进门那道 [abortCause] 的检查会把一个
	// 已经死掉的上下文当场拒掉（见 [TestRaceAbortRefusesBeforeStartingWorkOnADeadContext]），
	// 那条路上根本没有结果，也就验不到「迟到的结果有人收」这件事。
	go func() { <-started; cancel() }()

	_, err := raceAbort(ctx, "甲", func() (string, error) {
		close(started)
		<-gate
		return "迟到的", nil
	}, func(value string) { released <- value })
	if err == nil {
		t.Fatal("取消该赢下这次竞速")
	}

	close(gate)
	select {
	case got := <-released:
		if got != "迟到的" {
			t.Errorf("交给收尾函数的东西不对：%q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("迟到的那份结果该交给收尾函数")
	}
}

// TestRaceAbortDoesNotReleaseAResultThatFailed 钉住一份失败的结果不交给收尾函数。
//
// 源: packages/core/agent-loop/src/index.ts:113
//
// 失败时没有任何资源被造出来，把零值交下去等于让收尾函数去释放一个不存在的东西。
func TestRaceAbortDoesNotReleaseAResultThatFailed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})
	started := make(chan struct{})
	released := make(chan struct{}, 1)

	// 同样要让这件活儿先起步，否则它压根没跑过，这条用例就成了一次假通过。
	go func() { <-started; cancel() }()

	if _, err := raceAbort(ctx, "甲", func() (string, error) {
		close(started)
		<-gate
		return "", errors.New("跑坏了")
	}, func(string) { released <- struct{}{} }); err == nil {
		t.Fatal("取消该赢下这次竞速")
	}

	close(gate)
	select {
	case <-released:
		t.Error("失败的结果不该交给收尾函数")
	case <-time.After(200 * time.Millisecond):
	}
}

// ---- 工厂归属 ----

// TestFactoryOwnershipStopsAcceptingOnceDisposed 钉住拆过之后这个工厂不再接活。
//
// 源: packages/core/agent-loop/src/index.ts:55-57、81-84
func TestFactoryOwnershipStopsAcceptingOnceDisposed(t *testing.T) {
	t.Parallel()

	ownership := newFactoryOwnership()
	if !ownership.isActive() {
		t.Fatal("刚造出来该是接活的")
	}
	if err := ownership.dispose(context.Background()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	if ownership.isActive() {
		t.Error("拆过之后不该再接活")
	}
	if !errors.Is(context.Cause(ownership.teardown), errLoopNotActive) {
		t.Errorf("归属上下文该盖上那个原因，实际 %v", context.Cause(ownership.teardown))
	}
}

// TestFactoryOwnershipTearsDownEveryLiveAgentEvenWhenOneFails 钉住一份拆除失败
// 不会把其余的落下。
//
// 源: packages/core/agent-loop/src/index.ts:85-88
//
// 每一个 agent 的拆除都要摘掉它在两张注册表上的登记。漏掉一个，那个身份就永远
// 占着——之后任何一次用同一个身份的创建都会撞名失败，而现场看不出是谁占的。
func TestFactoryOwnershipTearsDownEveryLiveAgentEvenWhenOneFails(t *testing.T) {
	t.Parallel()

	ownership := newFactoryOwnership()
	var mutex sync.Mutex
	var torn []string
	record := func(name string, failure error) func(context.Context) error {
		return func(context.Context) error {
			mutex.Lock()
			torn = append(torn, name)
			mutex.Unlock()
			return failure
		}
	}

	boom := errors.New("拆坏了")
	ownership.track(record("甲", nil))
	ownership.track(record("乙", boom))
	ownership.track(record("丙", nil))

	err := ownership.dispose(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("那次失败该报上来，实际 %v", err)
	}
	mutex.Lock()
	count := len(torn)
	mutex.Unlock()
	if count != 3 {
		t.Errorf("三个都该拆到，实际拆了 %d 个", count)
	}
}

// TestFactoryOwnershipForgetsAnAgentThatAlreadyToreItselfDown 钉住 untrack 之后
// 工厂不会再拆它一次。
//
// 源: packages/core/agent-loop/src/index.ts:59-63
//
// 一份 agent 的拆除本身是一次性的（[AgentLoop.prepare] 里那个 disposeOnce），
// 所以再跑一遍不会出事——但那张表会一直长下去，一个开机后造过上万个短命 agent
// 的进程会把它们全留着。
func TestFactoryOwnershipForgetsAnAgentThatAlreadyToreItselfDown(t *testing.T) {
	t.Parallel()

	ownership := newFactoryOwnership()
	var ran int
	untrack := ownership.track(func(context.Context) error {
		ran++
		return nil
	})
	untrack()
	untrack() // 幂等：摘第二次不该出事。

	if err := ownership.dispose(context.Background()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	if ran != 0 {
		t.Errorf("摘掉的那一份不该再跑，实际跑了 %d 次", ran)
	}
}

// TestFactoryOwnershipWaitsForStartupWorkBeforeItFinishes 钉住拆除要等那些
// 在任何 agent 存在之前就开跑的启动活儿落定。
//
// 源: packages/core/agent-loop/src/index.ts:65-70、89
//
// 不等的话，一件启动活儿会在工厂已经拆完之后才去公布一个 agent——那个 agent
// 挂在一张谁都不再持有的注册表上，没有任何人会去拆它。
func TestFactoryOwnershipWaitsForStartupWorkBeforeItFinishes(t *testing.T) {
	t.Parallel()

	ownership := newFactoryOwnership()
	started := make(chan struct{})
	var finished bool
	ownership.trackStartup(func() {
		close(started)
		time.Sleep(20 * time.Millisecond)
		finished = true
	})
	<-started

	if err := ownership.dispose(context.Background()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	if !finished {
		t.Error("拆除该等那件启动活儿落定")
	}
}

// TestWaitWhileActiveStopsWaitingWhenTeardownBegins 钉住拆除一开始就不再等了。
//
// 源: packages/core/agent-loop/src/index.ts:77-79
//
// 这里等的是「一个同名的旧生命周期把注册表登记摘干净」。那个旧的可能正卡在自己
// 的拆除里，而这边的工厂已经在拆了——继续等下去就是一次死锁：拆除等这件启动活儿，
// 这件活儿等一个再也不会来的释放。
func TestWaitWhileActiveStopsWaitingWhenTeardownBegins(t *testing.T) {
	t.Parallel()

	ownership := newFactoryOwnership()
	returned := make(chan struct{})
	// 一个永远不会关掉的通道：唯一的出路只能是那次拆除。
	go func() {
		ownership.waitWhileActive(make(chan struct{}))
		close(returned)
	}()

	if err := ownership.dispose(context.Background()); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("拆除开始之后该停止等待")
	}
}

// ---- 装配 ----

// TestNewNeedsEveryCollaboratorAndAnOwner 钉住六件必需品缺一不可。
//
// 少了任何一样，这个工厂造出来的 agent 都缺一条腿——而那条腿是在**第一次跑步骤**
// 时才用到的，也就是说故障会离这里很远。
func TestNewNeedsEveryCollaboratorAndAnOwner(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	ctx := context.Background()
	config := Config{Logger: quietLogger()}

	missing := map[string]func(*Deps){
		"注册表":   func(d *Deps) { d.Agents = nil },
		"会话存储":  func(d *Deps) { d.Sessions = nil },
		"模型":    func(d *Deps) { d.LLM = nil },
		"工具":    func(d *Deps) { d.Tools = nil },
		"系统提示词": func(d *Deps) { d.SystemPrompt = nil },
	}
	for label, strip := range missing {
		t.Run(label, func(t *testing.T) {
			deps := world.deps
			strip(&deps)
			if _, _, err := New(ctx, deps, world.owner, config); err == nil {
				t.Errorf("少了%s该被拒", label)
			}
		})
	}
	if _, _, err := New(ctx, world.deps, nil, config); err == nil {
		t.Error("没有持有它的作用域该被拒")
	}
}

// TestNewRejectsABadConfigBeforeStartingAnything 钉住坏配置在任何 agent 起步之前
// 就被拒掉。
//
// 起一半再报错的话，那些已经公布出去的 agent 没有任何人持有它们的拆除——
// [New] 那条错误路上返回的是 nil 而不是 unwind。
func TestNewRejectsABadConfigBeforeStartingAnything(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	bad := newFactoryWorld(t)
	if _, _, err := New(ctx, bad.deps, bad.owner, Config{
		MaxParallelToolCalls: -1,
		Logger:               quietLogger(),
		Agents:               []ConfiguredAgent{{ID: "甲"}},
	}); err == nil {
		t.Error("负的并行上限该被拒")
	}
	if _, live := bad.agents.Get("甲"); live {
		t.Error("配置被拒时不该有 agent 起步")
	}

	clashing := newFactoryWorld(t)
	if _, _, err := New(ctx, clashing.deps, clashing.owner, Config{
		Logger: quietLogger(),
		Agents: []ConfiguredAgent{
			{ID: "甲", SessionID: "同一个"},
			{ID: "乙", SessionID: "同一个"},
		},
	}); err == nil {
		t.Error("撞了身份的配置该被拒")
	}
	if _, live := clashing.store.Get("同一个"); live {
		t.Error("配置被拒时不该有会话起步")
	}
}

// TestNewLetsTheLauncherIdentityBeatTheConfiguredOne 钉住启动器给的身份盖过配置里那个。
//
// 源: packages/core/agent-loop/src/index.ts:213-233、380
//
// 见 [ConfiguredAgentIdentities] 的说明：这正是它存在的理由——一份改模型路由的
// 覆盖配置不能顺手把启动器选定的会话身份也换掉。
func TestNewLetsTheLauncherIdentityBeatTheConfiguredOne(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	world.install(t, Config{
		Agents:             []ConfiguredAgent{{ID: "甲", SessionID: "配置里那个"}},
		LauncherIdentities: ConfiguredAgentIdentities{"甲": {ID: "启动器那个"}},
	})

	if _, live := world.agents.Get("启动器那个"); !live {
		t.Error("该用启动器给的那个身份起步")
	}
	if _, live := world.agents.Get("配置里那个"); live {
		t.Error("配置里那个身份该被盖掉")
	}
}

// TestNewInstallsItselfAsTheRegistryFactory 钉住工厂把自己登记成了造法。
//
// 源: packages/core/agent-loop/src/index.ts:336
//
// 注册表那条 [agent.Registry.Create] 是消费方（ACP 桥、子 agent、作业）唯一的入口，
// 它们只对着注册表编程、拿不到这个工厂对象。没登记上的话，整套东西装好了却一个
// agent 都造不出来。
func TestNewInstallsItselfAsTheRegistryFactory(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	world.install(t, Config{})

	handle, err := world.agents.Create(context.Background(), world.owner, agent.CreateOptions{
		SessionID:    "经注册表造的",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("经注册表造 agent 失败：%v", err)
	}
	if handle.Agent.ID() != "经注册表造的" {
		t.Errorf("造出来的身份不对：%q", string(handle.Agent.ID()))
	}
	if err := handle.Dispose(context.Background()); err != nil {
		t.Errorf("拆除失败：%v", err)
	}
}

// TestUnwindTakesTheFactoryAndItsVariablesBackOff 钉住那条拆除链把造法和三个变量
// 都摘干净。
//
// 摘不掉的话，同一张注册表上装第二个工厂会撞名失败——而热重载正是这么走的：
// 先拆旧的，再装新的。
func TestUnwindTakesTheFactoryAndItsVariablesBackOff(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	ctx := context.Background()
	_, unwind, err := New(ctx, world.deps, world.owner, Config{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}
	if err := unwind(ctx); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}

	if _, err := world.agents.Create(ctx, world.owner, agent.CreateOptions{
		SessionID: "拆完之后",
	}); err == nil {
		t.Error("造法该已经摘掉了")
	}

	// 同一个舞台上能再装一个，说明那两个变量的名字也让出来了。
	if _, second, err := New(ctx, world.deps, world.owner, Config{Logger: quietLogger()}); err != nil {
		t.Errorf("拆干净之后该能再装一个：%v", err)
	} else {
		t.Cleanup(func() { _ = second(ctx) })
	}
}

// ---- 系统提示词变量 ----

// TestTheAgentVariablesReadFromTheAgentOnThatScope 钉住那两个变量从**这个作用域上
// 那个 agent** 身上读。
//
// 源: packages/core/agent-loop/src/index.ts:377-379
//
// 新增: DSH 那边是三个，第三个 `cwd` 本仓库没有登记，理由见 [New] 里那条说明。
//
// 系统提示词里 `{{model}}` 这种引用，说的是「正在跑这一步的那个 agent 的模型」。
// 读错对象的话，一个进程里同时跑着两个走不同模型的 agent，其中一个的提示词里会
// 写着另一个的模型名——而那份提示词看起来完全正常。
func TestTheAgentVariablesReadFromTheAgentOnThatScope(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})

	ctx := context.Background()
	live, err := loop.Create(ctx, "带变量的",
		agent.Options{Provider: "甲", Model: "m-1"}, "ws-带变量的")
	if err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}

	assembly, err := world.prompts.Assemble(ctx,
		systemprompt.AssembleContext{Scope: live.Scope().Key()})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	want := map[string]string{"provider": "甲", "model": "m-1"}
	for name, expected := range want {
		value, registered := assembly.Variables[name]
		if !registered {
			t.Errorf("变量 %q 该登记过", name)
			continue
		}
		if value == nil || *value != expected {
			t.Errorf("变量 %q 该是 %q，实际 %v", name, expected, value)
		}
	}
}

// TestTheAgentVariablesAreAbsentOffAnyAgent 钉住一次不属于任何 agent 的装配里
// 那两个变量**没有值**。
//
// 源: packages/core/agent-loop/src/index.ts:377-379（DSH 那三行的 `?.` 短路）
//
// 回落成空串的话，一份引用了 `{{model}}` 的提示词会静静地渲染出一句
// 「你正在用 模型」——而 [systemprompt.RenderPrompt] 本来会为「变量在、值没有」
// 报一个错，正好把这种误用挡在发请求之前。
func TestTheAgentVariablesAreAbsentOffAnyAgent(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	world.install(t, Config{})

	assembly, err := world.prompts.Assemble(context.Background(),
		systemprompt.AssembleContext{Scope: scope.NewKey("跟 agent 无关的")})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	for _, name := range []string{"provider", "model"} {
		value, registered := assembly.Variables[name]
		if !registered {
			t.Errorf("变量 %q 该登记过", name)
			continue
		}
		if value != nil {
			t.Errorf("没有 agent 时变量 %q 该没有值，实际 %q", name, *value)
		}
	}
}

// ---- 并行上限 ----

// TestMaxParallelToolCallsLocksToTheStaticCapWithoutSettings 钉住没有设置服务时
// 上限锁在配置解出来的那个值上。
//
// 源: packages/core/agent-loop/src/index.ts:330-334
func TestMaxParallelToolCallsLocksToTheStaticCapWithoutSettings(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{MaxParallelToolCalls: 3})
	if got := loop.maxParallelToolCalls(); got != 3 {
		t.Errorf("该锁在 3 上，实际 %d", got)
	}

	other := newFactoryWorld(t)
	fallback := other.install(t, Config{})
	if got := fallback.maxParallelToolCalls(); got != DefaultMaxParallelToolCalls {
		t.Errorf("没写就该是默认值，实际 %d", got)
	}
}

// TestMaxParallelToolCallsReadsThroughSettingsEveryTime 钉住设置在位时每次都读透。
//
// 源: packages/core/agent-loop/src/index.ts:330-334
//
// 缓存一次的话，用户改完这一项要重启才生效——而这一项声明的是 [settings.AppliesLive]，
// 界面上会告诉他「马上生效」。
func TestMaxParallelToolCallsReadsThroughSettingsEveryTime(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{MaxParallelToolCalls: 3, Settings: newSettings(t)})
	if got := loop.maxParallelToolCalls(); got != 3 {
		t.Fatalf("起手该是配置里那个 3，实际 %d", got)
	}

	ctx := context.Background()
	if err := loop.settingsScope.Update(ctx, map[string]any{"maxParallelToolCalls": 5}); err != nil {
		t.Fatalf("改设置失败：%v", err)
	}
	if got := loop.maxParallelToolCalls(); got != 5 {
		t.Errorf("改完该马上读到 5，实际 %d", got)
	}
}

// TestSettingsRejectABadParallelCapBeforeItIsCommitted 钉住一次坏改动在提交之前
// 就被拦下，调度器停在上一个好值上。
//
// 源: packages/core/agent-loop/src/index.ts:340-347
//
// 让它提交进去的话，症状要等到**下一组工具调用**才出现——那时现场离这次改动
// 已经很远了。而且那一组会拿一个非法的池宽度去跑。
func TestSettingsRejectABadParallelCapBeforeItIsCommitted(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{MaxParallelToolCalls: 3, Settings: newSettings(t)})

	if err := loop.settingsScope.Update(context.Background(),
		map[string]any{"maxParallelToolCalls": -1}); err == nil {
		t.Error("负的上限该被拒")
	}
	if got := loop.maxParallelToolCalls(); got != 3 {
		t.Errorf("被拒之后该停在上一个好值 3 上，实际 %d", got)
	}
}

// ---- 造与拆 ----

// TestCreatePublishesIntoBothRegistries 钉住造出来的 agent 在两张注册表上都查得到。
//
// 源: packages/core/agent-loop/src/index.ts:580-587
//
// 两张表缺一不可：消费方按身份找 agent，而会话那张表是持久化和查询这条路的入口。
// 只进一张的话，一个跑得好好的 agent 在另一半系统眼里根本不存在。
func TestCreatePublishesIntoBothRegistries(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	live := world.create(t, loop, "造出来的")

	if found, ok := world.agents.Get("造出来的"); !ok || found.ID() != "造出来的" {
		t.Error("agent 注册表上该查得到")
	}
	if _, ok := world.store.Get("造出来的"); !ok {
		t.Error("会话存储上该查得到")
	}
	if live.Options().Model != "m-1" {
		t.Errorf("路由没带上：%#v", live.Options())
	}
}

// TestCreateRefusesAnUnrepresentableOutputCap 钉住坏的 agent 选项在造出任何东西
// 之前就被拒。
//
// 源: packages/core/agent-loop/src/index.ts:454-455
func TestCreateRefusesAnUnrepresentableOutputCap(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})

	if _, err := loop.Create(context.Background(), "坏上限",
		agent.Options{Provider: "甲", Model: "m-1", MaxTokens: -1}, ""); err == nil {
		t.Fatal("负的输出上限该被拒")
	}
	if _, live := world.agents.Get("坏上限"); live {
		t.Error("被拒之后不该留下一个 agent")
	}
}

// TestCreateAgentRunsSetupAndItsCommitBeforePublishing 钉住 setup 跑在公布之前。
//
// 源: packages/core/agent-loop/src/index.ts:606-620
//
// setup 是「这个 agent 的世界」的组装：工具、提示词段落、观察者都在那里挂上去。
// 公布之后才跑的话，一个同步的创建观察者看到的是一个半装好的 agent，而它可能
// 当场就要读那些东西。
func TestCreateAgentRunsSetupAndItsCommitBeforePublishing(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})

	var order []string
	handle, err := loop.CreateAgent(context.Background(), world.owner, agent.CreateOptions{
		SessionID:    "带 setup 的",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
		Setup: func(_ context.Context, agentScope *scope.Scope) (func() error, error) {
			order = append(order, "setup")
			if _, live := world.agents.Get("带 setup 的"); live {
				t.Error("setup 跑的时候还不该公布")
			}
			if agentScope == nil {
				t.Error("setup 该拿到这个 agent 自己的作用域")
			}
			return func() error {
				order = append(order, "commit")
				return nil
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = handle.Dispose(context.Background()) })

	if len(order) != 2 || order[0] != "setup" || order[1] != "commit" {
		t.Errorf("该先 setup 再 commit，实际 %v", order)
	}
	if _, live := world.agents.Get("带 setup 的"); !live {
		t.Error("跑完 setup 该公布出去")
	}
}

// TestCreateAgentRollsBackWhenSetupFails 钉住 setup 失败时一切都退回去。
//
// 源: packages/core/agent-loop/src/index.ts:611-615
//
// 留下残渣的后果是那个身份被永久占住：之后任何一次同名的创建都撞名失败，而
// 占着它的那个 agent 从来没有公布过，谁都拿不到它的拆除。
func TestCreateAgentRollsBackWhenSetupFails(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	boom := errors.New("setup 炸了")

	_, err := loop.CreateAgent(context.Background(), world.owner, agent.CreateOptions{
		SessionID:    "装到一半",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
		Setup: func(context.Context, *scope.Scope) (func() error, error) {
			return nil, boom
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("该把 setup 那个错误交出来，实际 %v", err)
	}
	if _, live := world.agents.Get("装到一半"); live {
		t.Error("回滚之后 agent 注册表上不该有残渣")
	}
	if _, live := world.store.Get("装到一半"); live {
		t.Error("回滚之后会话存储上不该有残渣")
	}

	// 身份让出来了，才谈得上「回滚干净」。
	if _, err := loop.Create(context.Background(), "装到一半",
		agent.Options{Provider: "甲", Model: "m-1"}, ""); err != nil {
		t.Errorf("同一个身份该能再用一次：%v", err)
	}
}

// TestCreateAgentNeedsAnOwner 钉住造一个 agent 要有一个持有它的作用域。
//
// 源: packages/core/agent-loop/src/index.ts:590-591
//
// 没有主人的 agent 永远不会被拆：它的拆除挂在那个作用域上，那才是它唯一的尽头。
func TestCreateAgentNeedsAnOwner(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	if _, err := loop.CreateAgent(context.Background(), nil, agent.CreateOptions{
		SessionID: "没主人",
	}); err == nil {
		t.Error("没有作用域该被拒")
	}
}

// TestDisposingAHandleClearsBothRegistriesAndTheScope 钉住那份句柄拆完之后什么都不剩。
//
// 源: packages/core/agent-loop/src/index.ts:496-540
func TestDisposingAHandleClearsBothRegistriesAndTheScope(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	ctx := context.Background()

	handle, err := loop.CreateAgent(ctx, world.owner, agent.CreateOptions{
		SessionID:    "要拆的",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}
	built, ok := handle.Agent.(*ReactLoopAgent)
	if !ok {
		t.Fatalf("这个工厂该造出一个 %T，实际 %T", built, handle.Agent)
	}

	if err := handle.Dispose(ctx); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	if _, live := world.agents.Get("要拆的"); live {
		t.Error("agent 注册表上该摘干净")
	}
	if _, live := world.store.Get("要拆的"); live {
		t.Error("会话存储上该摘干净")
	}
	if loop.agentForScope(built.Scope().Key()) != nil {
		t.Error("作用域索引上该摘干净")
	}
	// 幂等：同一份句柄拆第二次是同一次静止，不是第二次拆除。
	if err := handle.Dispose(ctx); err != nil {
		t.Errorf("拆第二次该原样返回上一次的结果：%v", err)
	}
}

// TestCreateRefusesOnceTheFactoryIsGone 钉住工厂拆过之后造不出新的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:456-458
//
// 造出来的话，那个 agent 落在一份已经不再持有任何东西的归属上——没有人会拆它。
func TestCreateRefusesOnceTheFactoryIsGone(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	ctx := context.Background()
	loop, unwind, err := New(ctx, world.deps, world.owner, Config{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}
	if err := unwind(ctx); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}

	if _, err := loop.Create(ctx, "太晚了",
		agent.Options{Provider: "甲", Model: "m-1"}, ""); !errors.Is(err, errLoopNotActive) {
		t.Errorf("该说这个循环已经不活了，实际 %v", err)
	}
}

// TestDisposingTheOwnerScopeTakesTheAgentsWithIt 钉住持有它的作用域一拆，
// 那些 agent 跟着走。
//
// 那条登记就是「谁拥有这个 agent」的全部答案。断了的话，一个被拆掉的插件会把
// 它造的 agent 留在注册表上继续跑。
func TestDisposingTheOwnerScopeTakesTheAgentsWithIt(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	ctx := context.Background()

	holder := namedScope(t, "持有者")
	if _, err := loop.CreateAgent(ctx, holder, agent.CreateOptions{
		SessionID:    "跟着走的",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
	}); err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}
	if _, live := world.agents.Get("跟着走的"); !live {
		t.Fatal("该先公布出去")
	}

	if err := holder.Dispose(ctx); err != nil {
		t.Fatalf("拆作用域失败：%v", err)
	}
	if _, live := world.agents.Get("跟着走的"); live {
		t.Error("持有者一拆，这个 agent 该跟着走")
	}
	if _, live := world.store.Get("跟着走的"); live {
		t.Error("它那个会话也该跟着走")
	}
}

// ---- 续跑 ----

// TestResumeWithoutPersistenceSaysSo 钉住没接持久化时续跑报得明明白白。
//
// 源: packages/core/agent-loop/src/index.ts:625-630
//
// 这句话是给部署方看的：症状是「我的会话恢复不了」，而原因是这套部署压根没装
// 后端。含糊的报错会让人去查会话数据，而那边根本没有问题。
func TestResumeWithoutPersistenceSaysSo(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})

	_, err := loop.Resume(context.Background(), world.owner,
		agent.ResumeOptions{ResumeSessionID: "续不了"})
	if err == nil || !strings.Contains(err.Error(), "session persistence is not configured") {
		t.Errorf("该说没接持久化，实际 %v", err)
	}
}

// TestResumeRebuildsTheAgentOnThePersistedSession 钉住续跑走的是持久化交出来的
// 那个会话。
//
// 源: packages/core/agent-loop/src/index.ts:637-710
func TestResumeRebuildsTheAgentOnThePersistedSession(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store, "存过的")
	loop := world.install(t, Config{Persistence: persistence})

	handle, err := loop.Resume(context.Background(), world.owner, agent.ResumeOptions{
		ResumeSessionID: "存过的",
		AgentOptions:    agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("续跑失败：%v", err)
	}
	t.Cleanup(func() { _ = handle.Dispose(context.Background()) })

	if handle.Agent.ID() != "存过的" {
		t.Errorf("该在那个身份上续跑，实际 %q", string(handle.Agent.ID()))
	}
	if _, live := world.store.Get("存过的"); !live {
		t.Error("读回来的那个会话该公布进存储")
	}
	// 公布成了也要释放：提供方那份预留在公布那一步已经被接手，这一次是空操作,
	// 但它必须发生——DSH 那句 `using ownedPreparation = preparation` 说的就是
	// 「离开这个函数就结束这段准备期」，不分成没成。
	if got := persistence.releaseCount(); got != 1 {
		t.Errorf("公布成功之后该释放一次准备期，实际释放了 %d 次", got)
	}
}

// TestResumeReleasesThePreparationWhenSetupFails 钉住半路失败也要还回预留。
//
// 源: packages/core/agent-loop/src/index.ts:697-708
//
// 不还，提供方那边的准备池就一直扣着这个身份：之后任何一次同名的续跑都会撞上
// 「会话还活着」或者一直等下去，而现场只看得到「卡住了」。
func TestResumeReleasesThePreparationWhenSetupFails(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store, "装不起来的")
	loop := world.install(t, Config{Persistence: persistence})

	boom := errors.New("setup 自己炸了")
	_, err := loop.Resume(context.Background(), world.owner, agent.ResumeOptions{
		ResumeSessionID: "装不起来的",
		Setup: func(context.Context, *scope.Scope) (func() error, error) {
			return nil, boom
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("该把 setup 那个错带回来，实际 %v", err)
	}
	if got := persistence.releaseCount(); got != 1 {
		t.Errorf("setup 失败之后该释放一次准备期，实际释放了 %d 次", got)
	}
}

// TestResumeReleasesAPreparationThatArrivesAfterTheCallerGaveUp 钉住迟到值也要还。
//
// 源: packages/core/agent-loop/src/index.ts:747-753（那个 abandoned 回调）
//
// 这是这条路上最容易漏的一支：调用方已经拿着错误走了，而后端那次读还在跑。
// 它落定的时候没有任何调用栈在等它，如果这时候不还预留，这个身份就被一份
// **谁也拿不到**的准备期永久扣住了。
func TestResumeReleasesAPreparationThatArrivesAfterTheCallerGaveUp(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	gate := make(chan struct{})
	persistence := newPersistence(world.store, "迟到的")
	persistence.prepare = func(_ context.Context, id sessionlog.SessionID) (*session.Session, error) {
		<-gate
		return world.store.Prepare(id, session.CreateOptions{})
	}
	loop := world.install(t, Config{Persistence: persistence})

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, err := loop.Resume(ctx, world.owner, agent.ResumeOptions{ResumeSessionID: "迟到的"})
		failed <- err
	}()
	cancel()
	if err := <-failed; err == nil {
		t.Fatal("取消之后该报错回来")
	}

	// 调用方已经走了，现在才让那次读落定。
	close(gate)

	deadline := time.Now().Add(5 * time.Second)
	for persistence.releaseCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("迟到的准备期一直没被释放，这个会话身份被永久扣住了")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestResumeNeedsAnOwner 钉住续跑也要有一个持有它的作用域。
func TestResumeNeedsAnOwner(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{Persistence: newPersistence(world.store, "存过的")})
	if _, err := loop.Resume(context.Background(), nil,
		agent.ResumeOptions{ResumeSessionID: "存过的"}); err == nil {
		t.Error("没有作用域该被拒")
	}
}

// TestResumeHandsBackTheLoadFailure 钉住读取失败原样报上来。
//
// 源: packages/core/agent-loop/src/index.ts:686-689
func TestResumeHandsBackTheLoadFailure(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{Persistence: newPersistence(world.store)})

	_, err := loop.Resume(context.Background(), world.owner,
		agent.ResumeOptions{ResumeSessionID: "没存过"})
	if err == nil || !strings.Contains(err.Error(), "没存过") {
		t.Errorf("该报出读不到那个存档，实际 %v", err)
	}
}

// TestResumeStopsWaitingWhenTheCallerGivesUp 钉住调用方一取消就不再等那个后端。
//
// 源: packages/core/agent-loop/src/index.ts:657-672
//
// 一个永远不落定的后端会把这个身份扣住：之后任何一次同名的创建都撞名失败，
// 而现场只看得到「卡住了」。
func TestResumeStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })

	persistence := newPersistence(world.store, "读不完的")
	persistence.prepare = func(ctx context.Context, sessionID sessionlog.SessionID) (*session.Session, error) {
		<-gate
		return world.store.Prepare(sessionID, session.CreateOptions{})
	}
	loop := world.install(t, Config{Persistence: persistence})

	ctx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, err := loop.Resume(ctx, world.owner,
			agent.ResumeOptions{ResumeSessionID: "读不完的"})
		failed <- err
	}()
	cancel()

	select {
	case err := <-failed:
		if err == nil {
			t.Error("取消之后该报错回来")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("调用方取消之后不该继续等那个后端")
	}
}

// ---- 配置驱动的启动 ----

// TestAConfiguredAgentWithoutAnIdentityGetsAMintedOne 钉住没写身份的那一项现铸一个。
//
// 源: packages/core/agent-loop/src/index.ts:385-388
//
// 铸出来的身份带着这一项的 id 当前缀，这样一段日志里认得出它是哪一项起的；
// 后面那截随机数保证同一份配置重启多次不会撞上自己上一次留下的东西。
func TestAConfiguredAgentWithoutAnIdentityGetsAMintedOne(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	world.install(t, Config{Agents: []ConfiguredAgent{{ID: "记事本"}}})

	// 配置驱动的启动是异步的（见 [factoryOwnership.trackStartup]），装完的一瞬间
	// 注册表上多半还是空的。
	waitFor(t, func() bool { return len(world.agents.List()) == 1 })

	var found []sessionlog.SessionID
	for _, live := range world.agents.List() {
		found = append(found, live.ID())
	}
	if len(found) != 1 {
		t.Fatalf("该正好起一个 agent，实际 %v", found)
	}
	if !strings.HasPrefix(string(found[0]), "记事本-session-") {
		t.Errorf("铸出来的身份该带这一项的 id 当前缀，实际 %q", string(found[0]))
	}
}

// TestAMintedIdentityNeverTouchesPersistence 钉住现铸的身份不去敲持久化后端。
//
// 源: packages/core/agent-loop/src/index.ts:390-393
//
// 一个刚铸出来的随机身份不可能已经落过地。去读它只会白等一次后端往返——而开机
// 时那一次往返是串在启动路径上的。
func TestAMintedIdentityNeverTouchesPersistence(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store)
	persistence.prepare = func(context.Context, sessionlog.SessionID) (*session.Session, error) {
		t.Error("现铸的身份不该去读持久化")
		return nil, errors.New("不该走到这里")
	}
	world.install(t, Config{
		Persistence: persistence,
		Agents:      []ConfiguredAgent{{ID: "记事本"}},
	})

	if got := persistence.listCount(); got != 0 {
		t.Errorf("也不该去列存档，实际列了 %d 次", got)
	}
}

// TestAConfiguredIdentityIsRestoredWhenItAlreadyLanded 钉住重挂时读回已经落地的
// 那一份，而不是盖掉它。
//
// 源: packages/core/agent-loop/src/index.ts:406-419
//
// 这是配置里写死身份的**唯一**理由：这个 agent 的历史要熬过重启。新建一个同名的
// 空会话，那段历史就在下一次写回时被覆盖掉了。
func TestAConfiguredIdentityIsRestoredWhenItAlreadyLanded(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store, "记事本的会话")
	world.install(t, Config{
		Persistence: persistence,
		Agents:      []ConfiguredAgent{{ID: "记事本", SessionID: "记事本的会话"}},
	})
	// 那条启动活儿是异步的；拆除会等它落定（见 factoryOwnership.dispose）。
	waitForAgent(t, world, "记事本的会话")

	if _, live := world.agents.Get("记事本的会话"); !live {
		t.Error("该在那个确切身份上起步")
	}
	// 存档在，就不该回落到「第一次创建」——那条路要先列一次存档。
	if got := persistence.listCount(); got != 0 {
		t.Errorf("读得回来就不该再列存档，实际列了 %d 次", got)
	}
}

// TestAConfiguredIdentityIsCreatedTheFirstTimeItIsUsed 钉住存档确实不存在时
// 回落到第一次创建。
//
// 源: packages/core/agent-loop/src/index.ts:420-427
//
// 不回落的话，一份刚写好的配置第一次开机就起不来——而那时那个会话本来就还不存在。
func TestAConfiguredIdentityIsCreatedTheFirstTimeItIsUsed(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store)
	world.install(t, Config{
		Persistence: persistence,
		Agents:      []ConfiguredAgent{{ID: "记事本", SessionID: "头一回"}},
	})
	waitForAgent(t, world, "头一回")

	if got := persistence.listCount(); got != 1 {
		t.Errorf("该列一次存档来确认它真的不存在，实际 %d 次", got)
	}
}

// TestABrokenArchiveIsNotMistakenForAMissingOne 钉住列存档失败时**不**新建。
//
// 源: packages/core/agent-loop/src/index.ts:420-424
//
// 这是这条路上最贵的一次判断：后端临时故障和「这个存档不存在」长得一样，而认错了
// 的后果是拿一个空会话盖掉一段真实的历史。分不清的时候必须吵，不能猜。
func TestABrokenArchiveIsNotMistakenForAMissingOne(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store)
	persistence.listErr = errors.New("后端挂了")

	world.install(t, Config{
		Persistence: persistence,
		Agents:      []ConfiguredAgent{{ID: "记事本", SessionID: "读不了"}},
	})

	// 这条启动活儿是异步的，而「没起来」是它的初始状态，光等它是等不出结论的。
	// 那次列存档才是这条路真正走到分岔口的证据：读回失败之后才会去列一次，
	// 而列也失败正是本用例要制造的那个两难。
	waitFor(t, func() bool { return persistence.listCount() == 1 })
	if _, live := world.agents.Get("读不了"); live {
		t.Error("后端故障时不该新建一个同名的空会话")
	}
}

// TestAResumingEntryWithoutPersistenceIsReportedNotHung 钉住要续跑却没接持久化时
// 当场通报，而不是无限等下去。
//
// 源: packages/core/agent-loop/src/index.ts:398-404
//
// 见 [AgentLoop.startResumingConfigured] 的说明：Go 里装配在构造之前就定死了，
// 持久化不在位是一个**永久**状态。照 DSH 那样等服务到场的话，那些为这个身份
// 缓着活儿的消费方会永远等下去。
func TestAResumingEntryWithoutPersistenceIsReportedNotHung(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	reported := make(chan error, 1)

	// 观察者必须在这一项起步**之前**挂上：那次通报是同步发生在 New 里面的。
	// 所以这里不走 world.install，自己拼一遍装配次序。
	loop, unwind, err := New(context.Background(), world.deps, world.owner, Config{
		Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}
	t.Cleanup(func() { _ = unwind(context.Background()) })

	undo, err := loop.OnConfigStartFailed(func(_ sessionlog.SessionID, failure error) {
		select {
		case reported <- failure:
		default:
		}
	})
	if err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}
	defer undo()

	loop.startResumingConfigured(context.Background(),
		ConfiguredAgent{ID: "记事本", ResumeSessionID: "续不了"})

	select {
	case failure := <-reported:
		if !strings.Contains(failure.Error(), "cannot resume") {
			t.Errorf("该说续不了，实际 %v", failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("该当场通报，而不是等下去")
	}
}

// TestAConfiguredResumingEntryRebuildsOnTheArchivedSession 钉住配置里写了续跑身份的
// 那一项真的在那份存档上起了步。
//
// 源: packages/core/agent-loop/src/index.ts:405-410
//
// 这是 [ConfiguredAgent.ResumeSessionID] 存在的全部理由。断在这里的话，一份写着
// 「开机就把上次那个会话接着跑」的配置会静悄悄地什么都不做——注册表上空空如也，
// 而没有任何一条错误被报出来。
func TestAConfiguredResumingEntryRebuildsOnTheArchivedSession(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	persistence := newPersistence(world.store, "上次那个")
	world.install(t, Config{
		Persistence: persistence,
		Agents:      []ConfiguredAgent{{ID: "记事本", ResumeSessionID: "上次那个"}},
	})
	waitForAgent(t, world, "上次那个")

	// 续跑走的是 Prepare 那条路，不该回落到「列一次存档看看在不在」——那是
	// restore 那一支（写 SessionID 的那种）才有的动作。
	if got := persistence.listCount(); got != 0 {
		t.Errorf("续跑不该去列存档，实际列了 %d 次", got)
	}
}

// TestAConfiguredResumeThatFailsIsReported 钉住续跑失败通报出去，而不是咽下去。
//
// 源: packages/core/agent-loop/src/index.ts:405-410
//
// 这一支和 restore 那一支有一处关键的不同：restore 读不回来时会**回落到新建**
// （见 [AgentLoop.restoreOrCreateConfigured]），而续跑没有回落——配置说的是
// 「接着上次跑」，凭空造一个空会话不是它要的东西。所以这里除了通报之外什么都
// 不做，而那条通报就是消费方唯一的信号。
//
// 观察者得在这一项起步之前挂上，所以不走 world.install，自己拼一遍装配次序
// （同 [TestAResumingEntryWithoutPersistenceIsReportedNotHung]）。
func TestAConfiguredResumeThatFailsIsReported(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	reported := make(chan error, 1)

	// 存档里一个身份都没有：Prepare 会说「存档里没有会话」。
	loop, unwind, err := New(context.Background(), world.deps, world.owner, Config{
		Logger:      quietLogger(),
		Persistence: newPersistence(world.store),
	})
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}
	t.Cleanup(func() { _ = unwind(context.Background()) })

	undo, err := loop.OnConfigStartFailed(func(id sessionlog.SessionID, failure error) {
		if id != "没存过" {
			t.Errorf("该带上续不了的那个身份，实际 %q", string(id))
		}
		select {
		case reported <- failure:
		default:
		}
	})
	if err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}
	defer undo()

	loop.startResumingConfigured(context.Background(),
		ConfiguredAgent{ID: "记事本", ResumeSessionID: "没存过"})

	select {
	case failure := <-reported:
		if !strings.Contains(failure.Error(), "没存过") {
			t.Errorf("该说清是哪个身份续不了，实际 %v", failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("续跑失败该通报出去")
	}
	if _, live := world.agents.Get("没存过"); live {
		t.Error("续不回来就不该凭空造一个同名的空会话")
	}
}

// TestARestoringConfiguredIdentityWaitsForTheDrainingOccupant 钉住一个还占着注册表的
// 同名身份会把这次启动挡在门外，直到它摘干净。
//
// 源: packages/core/agent-loop/src/index.ts:430-451
//
// 这一步存在的理由是重挂：旧的那份还在排干（它的拆除是异步的），新的一份已经
// 开始起步了。不等的话，那次创建撞在一个还没摘掉的登记上，报出来的是一句
// 「这个身份已经有人了」——而真相只是「再等一会儿就没人了」。
//
// 直接调这个不导出的函数，因为它要的那个时序（先占住、等着、再放开）从配置那条
// 启动路上摆不出来：配置项在 [New] 里就起步了，用例根本来不及在那之前占住身份。
func TestARestoringConfiguredIdentityWaitsForTheDrainingOccupant(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	ctx := context.Background()

	handle, err := loop.CreateAgent(ctx, world.owner, agent.CreateOptions{
		SessionID:    "抢手的",
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("造占用者失败：%v", err)
	}

	waited := make(chan error, 1)
	go func() { waited <- loop.waitForDrainingConfiguredIdentity(ctx, "抢手的") }()

	select {
	case err := <-waited:
		t.Fatalf("身份还占着就不该放行：%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := handle.Dispose(ctx); err != nil {
		t.Fatalf("拆占用者失败：%v", err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Errorf("占用者走干净了该正常放行，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("占用者一摘干净该醒过来")
	}
}

// TestAFreeConfiguredIdentityIsNotWaitedOn 钉住没人占着的身份一步都不等。
//
// 源: packages/core/agent-loop/src/index.ts:430-433
//
// 开机那一次是常态，而那时注册表本来就是空的。这里要是挂上观察者去等一次通知，
// 每一个配置项的启动都会白等到超时——而开机路径是串着走的。
func TestAFreeConfiguredIdentityIsNotWaitedOn(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	if err := loop.waitForDrainingConfiguredIdentity(context.Background(), "没人要的"); err != nil {
		t.Errorf("没人占着该当场放行，实际 %v", err)
	}
}

// TestOnConfigStartFailedRefusesANilObserver 钉住 nil 观察者被拒。
func TestOnConfigStartFailedRefusesANilObserver(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})
	if _, err := loop.OnConfigStartFailed(nil); err == nil {
		t.Error("nil 观察者该被拒")
	}
}

// TestAPanickingObserverDoesNotStopTheRest 钉住一个炸了的观察者不影响后面的。
//
// 源: packages/core/agent-loop/src/index.ts:396-403
//
// 每一个观察者都在为这个身份缓着活儿。漏通知一个，那些活儿就永远等下去——
// 而原因是另一个毫不相干的观察者写坏了。
func TestAPanickingObserverDoesNotStopTheRest(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	loop := world.install(t, Config{})

	var reached bool
	first, err := loop.OnConfigStartFailed(func(sessionlog.SessionID, error) {
		panic("这个观察者写坏了")
	})
	if err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}
	defer first()
	second, err := loop.OnConfigStartFailed(func(sessionlog.SessionID, error) {
		reached = true
	})
	if err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}
	defer second()

	loop.reportConfiguredStartupFailure("记事本", "restore", "某个身份", errors.New("起不来"))
	if !reached {
		t.Error("前一个观察者炸了，后一个还是该收到")
	}
}

// TestNoStartupFailureIsReportedOnceTheFactoryIsDisposing 钉住工厂在拆的时候不再通报。
//
// 源: packages/core/agent-loop/src/index.ts:384-386
//
// 拆除本身会取消掉那些在飞的启动活儿。把那次取消当成故障报出去的话，消费方会把
// 一次正常卸载当成事故——而它们对故障的反应通常是拒掉手里所有缓着的活儿。
func TestNoStartupFailureIsReportedOnceTheFactoryIsDisposing(t *testing.T) {
	t.Parallel()

	world := newFactoryWorld(t)
	ctx := context.Background()
	loop, unwind, err := New(ctx, world.deps, world.owner, Config{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("装工厂失败：%v", err)
	}

	var reported bool
	undo, err := loop.OnConfigStartFailed(func(sessionlog.SessionID, error) { reported = true })
	if err != nil {
		t.Fatalf("挂观察者失败：%v", err)
	}
	defer undo()

	if err := unwind(ctx); err != nil {
		t.Fatalf("拆除失败：%v", err)
	}
	loop.reportConfiguredStartupFailure("记事本", "restore", "某个身份", errors.New("起不来"))
	if reported {
		t.Error("工厂在拆的时候不该再通报启动失败")
	}
}

// ---- 等待小工具 ----

// waitFor 等一个条件成立，超时当场失败。
//
// 配置驱动的那几条启动活儿是异步的（[factoryOwnership.trackStartup]），
// 用例只能等它们的结果显形。
func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等超时了")
}

// waitForAgent 等一个身份出现在 agent 注册表上。
func waitForAgent(t *testing.T, world *factoryWorld, id sessionlog.SessionID) {
	t.Helper()
	waitFor(t, func() bool {
		_, live := world.agents.Get(id)
		return live
	})
}
