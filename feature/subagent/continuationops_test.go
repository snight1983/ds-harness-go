// 本文件的作用：续接管理器那四件公开操作的测试——起一个可续后台孩子（占 id、
// 记描述符、深度、准入次序）、后续投递（热路与冷恢复）、打断（三种权与那次被接受的
// 空操作），以及孩子对父的汇报（授权、父在不在、两种排期）。

package subagent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// ---- 共用的小工具 ----

// persistChild 在存档里摆一个还没驻留过的可续孩子：一份头加上它自己那段日志。
func (f *continuationFixture) persistChild(
	t *testing.T,
	childID, parent sessionlog.SessionID,
	descriptor DescriptorData,
) {
	t.Helper()
	record := event(t, EventDescriptor, descriptor)
	record.Seq = 0
	f.store.put(sessionlog.SessionHeader{
		Version:         sessionlog.FormatVersion,
		ID:              childID,
		WorkspaceID:     testWorkspaceID,
		ParentSession:   parent,
		Origin:          sessionlog.OriginSubagent,
		DelegationDepth: 1,
	}, record)
}

// childDescriptor 折出一个活孩子那段日志里的描述符。
func childDescriptor(t *testing.T, child *childAgent) DescriptorData {
	t.Helper()
	descriptor, found, err := FoldDescriptor(child.Session().Events())
	if err != nil || !found {
		t.Fatalf("该折得出一份描述符，实际 found=%v err=%v", found, err)
	}
	return descriptor
}

// feignDisposal 在某个活化上摆一笔**没人推进**的处置事务，好把「正在拆」这个状态
// 单独摆出来看。
//
// 收尾时必须把它摘掉：那笔事务永远不会结清，而拥有它的作用域一处置就会去等它，
// 留着的话整个测试进程会挂死在清理里。
func (f *continuationFixture) feignDisposal(t *testing.T, childID sessionlog.SessionID) {
	t.Helper()
	live := f.livingActivation(t, childID)

	f.manager.mutex.Lock()
	live.disposal = newDisposalTx()
	f.manager.mutex.Unlock()

	t.Cleanup(func() {
		f.manager.mutex.Lock()
		live.disposal = nil
		f.manager.mutex.Unlock()
	})
}

// countingParent 是一个数着自己那条血统被解算过几次的父。
//
// 准入闸每次都要读一遍父的会话头去解血统（assertAdmitting → closingTeardown →
// liveLineage），所以这个钩子是「Followup 那个重试循环此刻走到第几圈」唯一的同步
// 接缝——不靠它就只能起协程去抢那个窗口，而那是一场竞速。
type countingParent struct {
	*childAgent

	onSession func(round int)
	armed     atomic.Bool
	calls     atomic.Int32
}

func (p *countingParent) Session() *coresession.Session {
	if p.armed.Load() {
		p.onSession(int(p.calls.Add(1)))
	}
	return p.childAgent.Session()
}

// rearm 上膛并把圈数归零，于是「第几圈」是从这一刻起算的。
//
// 上膛这一下是要紧的：立这个父、解它的深度和路由，一路上都会各自解算一次会话，
// 而那些圈数不属于被测的那一段。不挡住它们的话，一个只认第一圈的钩子会在被测的
// 调用还没开始时就打空了。
func (p *countingParent) rearm() {
	p.calls.Store(0)
	p.armed.Store(true)
}

// spawnCountingParent 造一个活着的父，并在它每一次被解算血统时叫一下 onSession。
//
// 不能复用 spawnParent：血统认证认的是注册表里那个**确切的对象**，所以进注册表的
// 必须是这层包装，而不是被它包住的那个孩子。
func (f *continuationFixture) spawnCountingParent(
	t *testing.T,
	id sessionlog.SessionID,
	onSession func(round int),
) *countingParent {
	t.Helper()
	agentScope := keyedScope(t, string(id), f.owner.Key())
	live, err := f.sessions.Create(t.Context(), agentScope, id, coresession.CreateOptions{
		WorkspaceID: testWorkspaceID,
	})
	if err != nil {
		t.Fatalf("建父会话失败：%v", err)
	}
	built := &countingParent{childAgent: newChildAgent(id, agentScope, live), onSession: onSession}
	detach, err := f.agents.Register(t.Context(), built, nil)
	if err != nil {
		t.Fatalf("登记父 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })
	return built
}

// onlyFollowup 断言这个孩子恰好收到一条后续投递，并交出它。
func onlyFollowup(t *testing.T, child *childAgent) llm.Message {
	t.Helper()
	followups, _, _ := child.delivered()
	if len(followups) != 1 {
		t.Fatalf("该恰好收到一条后续投递，实际 %d 条", len(followups))
	}
	return followups[0]
}

// ---- 起一个可续后台孩子 ----

func TestStartContinuableNeedsAParent(t *testing.T) {
	fixture := newContinuation(t)
	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{Provider: "spawn"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有父该被拒，实际 %v", err)
	}
}

// 没组装持久化就一个可续孩子都起不了：这个孩子的身份本来就是耐久的。
func TestStartContinuableNeedsPersistence(t *testing.T) {
	fixture := newContinuation(t)
	fixture.manager.deps.Persistence = nil
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodePersistenceUnavailable {
		t.Fatalf("该报 %s，实际 %v", CodePersistenceUnavailable, err)
	}
}

func TestStartContinuableRejectsANegativeCeiling(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	negative := -1

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Request:  StartRequest{Parent: parent, MaxDepth: &negative},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("负的上限该被拒，实际 %v", err)
	}
}

// 越过派发上限报的是 [DepthError]，而且一个孩子都没建出来。
func TestStartContinuableStopsAtTheDepthCeiling(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	ceiling := 0

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent, MaxDepth: &ceiling},
	})
	var depthErr *DepthError
	if !errors.As(err, &depthErr) {
		t.Fatalf("该报 DepthError，实际 %v", err)
	}
	if len(fixture.factory.created()) != 0 {
		t.Fatal("越界之后不该建出任何孩子")
	}
}

// 不给 id 就自己铸一个，而且那条路**不读介质**：一个铸出来的 UUID 撞不上任何东西。
func TestStartContinuableMintsAnIDWithoutReadingTheArchive(t *testing.T) {
	fixture := newContinuation(t)
	fixture.store.listErr = errors.New("这条路不该列举")
	parent := fixture.spawnParent(t, "parent", "")

	started, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		Request:  StartRequest{Parent: parent, Prompt: textContent("干活")},
	})
	if err != nil {
		t.Fatalf("起可续孩子失败：%v", err)
	}
	if started.ChildID == "" || started.MessageID == "" {
		t.Fatalf("该交回两个身份，实际 %#v", started)
	}
	if !fixture.resident(started.ChildID) {
		t.Fatal("那个铸出来的孩子该驻留着")
	}
}

// 调用方自带 id 就要查介质：存档里已经有这个身份时不许再起一个。
func TestStartContinuableChecksTheArchiveForACallerSuppliedID(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodeDuplicateChild {
		t.Fatalf("该报 %s，实际 %v", CodeDuplicateChild, err)
	}
}

// 列举报错就原样往上交：一次读不动的介质说明不了「这个 id 是空的」。
func TestStartContinuableSurfacesAFailedArchiveProbe(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	broken := errors.New("读不动")
	fixture.store.listErr = broken

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if !errors.Is(err, broken) {
		t.Fatalf("该原样交回那次列举失败，实际 %v", err)
	}
}

// 那次列举是一次真的介质读，也就是一个让出点：读完之后三道闸全部重验一遍。
func TestStartContinuableRecheckesEveryGateAfterTheArchiveProbe(t *testing.T) {
	for name, testCase := range map[string]struct {
		disturb func(*testing.T, *continuationFixture, context.CancelFunc)
		wanted  string
	}{
		"读完被取消": {
			disturb: func(_ *testing.T, _ *continuationFixture, cancel context.CancelFunc) { cancel() },
			wanted:  CodeCancelled,
		},
		"读完准入关了": {
			disturb: func(_ *testing.T, f *continuationFixture, _ context.CancelFunc) {
				f.manager.mutex.Lock()
				f.manager.draining = true
				f.manager.mutex.Unlock()
			},
			wanted: CodeDraining,
		},
		"读完这个 id 被占了": {
			disturb: func(t *testing.T, f *continuationFixture, _ context.CancelFunc) {
				// 一个活会话——而不是一个活 agent——就够了：这道闸认的是两者之一。
				if _, err := f.sessions.Create(
					context.Background(), keyedScope(t, "抢先", nil), "child",
					coresession.CreateOptions{WorkspaceID: testWorkspaceID},
				); err != nil {
					t.Fatalf("抢下这个 id 失败：%v", err)
				}
			},
			wanted: CodeDuplicateChild,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newContinuation(t)
			parent := fixture.spawnParent(t, "parent", "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture.store.onList = func() { testCase.disturb(t, fixture, cancel) }

			_, err := fixture.manager.StartContinuable(ctx, ContinuableStartSpec{
				Provider: "spawn",
				Label:    "查一下",
				ChildID:  "child",
				Request:  StartRequest{Parent: parent},
			})
			if codeOf(err) != testCase.wanted {
				t.Fatalf("该报 %s，实际 %v", testCase.wanted, err)
			}
		})
	}
}

// 一个活着的 agent 或者活着的会话已经占着这个 id 时，连介质都不必读。
func TestStartContinuableRefusesAnIDThatIsAlreadyLive(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.spawnParent(t, "taken", "")

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		ChildID:  "taken",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodeDuplicateChild {
		t.Fatalf("该报 %s，实际 %v", CodeDuplicateChild, err)
	}
	if probes := fixture.store.probes(); len(probes) != 0 {
		t.Fatalf("这道门之前不该读过介质，实际 %#v", probes)
	}
}

// 描述符记的是**解算之后**那份路由：父身上那套在孩子换过模型之前一直算数。
func TestStartContinuableRecordsTheResolvedRouting(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	parent.options = agent.Options{Provider: "父的", Model: "父的模型", MaxTokens: 100}

	fixture.startChild(t, parent, "child", "干活")
	descriptor := childDescriptor(t, fixture.factory.child("child"))

	if descriptor.Mode != ModeContinuable || descriptor.Provider != "spawn" {
		t.Fatalf("该是一份 spawn 立起来的可续描述符，实际 %#v", descriptor)
	}
	if descriptor.Label != "查一下" {
		t.Fatalf("该记下这次派发的名字，实际 %q", descriptor.Label)
	}
	if descriptor.AgentProvider != "父的" || descriptor.AgentModel != "父的模型" {
		t.Fatalf("没说的路由该从父身上解算下来，实际 %#v", descriptor)
	}
	// 没给工具范围就不记那一项，而不是记一份空的限制。
	if descriptor.ToolFilter != nil {
		t.Fatalf("没给工具范围就不该记，实际 %#v", descriptor.ToolFilter)
	}
}

// 说了工具范围就记下来——一次冷恢复只有这份记录可读。
func TestStartContinuableRecordsAToolFilter(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	if _, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request: StartRequest{
			Parent:     parent,
			Prompt:     textContent("干活"),
			ToolFilter: tools.Restriction{Allow: []string{"read"}},
			Persona:    "只给这个孩子",
		},
	}); err != nil {
		t.Fatalf("起可续孩子失败：%v", err)
	}
	descriptor := childDescriptor(t, fixture.factory.child("child"))
	if descriptor.ToolFilter == nil || len(descriptor.ToolFilter.Allow) != 1 {
		t.Fatalf("该记下那份工具范围，实际 %#v", descriptor.ToolFilter)
	}
	if descriptor.Persona != "只给这个孩子" {
		t.Fatalf("该记下那份人设，实际 %q", descriptor.Persona)
	}
}

// 提供方那份创建贡献失败时整次开工作废，一个句柄都不留。
func TestStartContinuableStopsWhenTheProviderRefuses(t *testing.T) {
	fixture := newContinuation(t)
	refusal := errors.New("这个提供方立不起可续孩子")
	fixture.host.prepare = func(context.Context, string, ContinuableCreateRequest) (ContinuableCreateSpec, error) {
		return ContinuableCreateSpec{}, refusal
	}
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("提供方那次拒绝该保持权威，实际 %v", err)
	}
	if fixture.resident("child") {
		t.Fatal("拒绝之后不该有驻留")
	}
}

// 提供方那份 seed 定下孩子那条耐久血统边界，它不能由 seed 的全长推出来——
// 描述符那一回合是孩子自己的，不属于继承来的前缀。
func TestStartContinuableCarriesTheLineageBoundary(t *testing.T) {
	fixture := newContinuation(t)
	inherited := []sessionlog.Event{assistantMessage(t, 1, textContent("祖上说过的话"))}
	fixture.host.prepare = func(context.Context, string, ContinuableCreateRequest) (ContinuableCreateSpec, error) {
		return ContinuableCreateSpec{Seed: inherited}, nil
	}
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	created := fixture.factory.created()
	if len(created) != 1 {
		t.Fatalf("该恰好建一次，实际 %d 次", len(created))
	}
	if created[0].SeedLength != len(inherited) {
		t.Fatalf("血统边界该是继承那一段的长度 %d，实际 %d", len(inherited), created[0].SeedLength)
	}
	if len(created[0].Seed) <= len(inherited) {
		t.Fatalf("整份 seed 该比继承那一段长（多出描述符那一回合），实际 %d", len(created[0].Seed))
	}
	if created[0].Origin != sessionlog.OriginSubagent || created[0].DelegationDepth != 1 {
		t.Fatalf("该盖上子 agent 出身和深度，实际 %#v", created[0])
	}
}

// 提供方那一步之后调用方就撤了：那次物化不许发生。
func TestStartContinuableStopsWhenTheCallerCancelsAfterPreparing(t *testing.T) {
	fixture := newContinuation(t)
	ctx, cancel := context.WithCancel(t.Context())
	fixture.host.prepare = func(context.Context, string, ContinuableCreateRequest) (ContinuableCreateSpec, error) {
		cancel()
		return ContinuableCreateSpec{}, nil
	}
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.StartContinuable(ctx, ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if len(fixture.factory.created()) != 0 {
		t.Fatal("取消之后不该建出任何孩子")
	}
}

// 描述符自己那道校验在任何提供方的活开工之前就把这次请求拒掉。
func TestStartContinuableRefusesADescriptorItCannotSnapshot(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	// 一份可续描述符必须带 label，这里故意不给。
	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("该被拒，实际 %v", err)
	}
	if len(fixture.factory.created()) != 0 {
		t.Fatal("描述符没过之前不该建出任何孩子")
	}
}

// 提供方交回来的那段种子种不进一个游离会话时，这次开工在建出孩子之前就停下。
func TestStartContinuableStopsWhenTheProviderSeedCannotBeStaged(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	// 种子的持久契约要求从 seq 0 起连续，这一条摆在 7。
	broken := event(t, EventDescriptor, DescriptorData{})
	broken.Seq = 7
	fixture.host.prepare = func(context.Context, string, ContinuableCreateRequest) (ContinuableCreateSpec, error) {
		return ContinuableCreateSpec{Seed: []sessionlog.Event{broken}}, nil
	}

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if err == nil {
		t.Fatal("种不下去的种子该把这次开工停下来")
	}
	if len(fixture.factory.created()) != 0 {
		t.Fatal("种子没排演成之前不该建出任何孩子")
	}
}

// 解算提供方那份贡献是一个真的让出点，所以它一回来就把闸重验一遍。
func TestStartContinuableRecheckesTheGatesAfterPreparing(t *testing.T) {
	for name, testCase := range map[string]struct {
		disturb func(*testing.T, *continuationFixture)
		wanted  string
	}{
		"解算完准入关了": {
			disturb: func(_ *testing.T, f *continuationFixture) {
				f.manager.mutex.Lock()
				f.manager.draining = true
				f.manager.mutex.Unlock()
			},
			wanted: CodeDraining,
		},
		"解算完这个 id 被占了": {
			disturb: func(t *testing.T, f *continuationFixture) {
				// 一个活会话——而不是一个活 agent——就够了：这道闸认的是两者之一。
				if _, err := f.sessions.Create(
					context.Background(), keyedScope(t, "抢先", nil), "child",
					coresession.CreateOptions{WorkspaceID: testWorkspaceID},
				); err != nil {
					t.Fatalf("抢下这个 id 失败：%v", err)
				}
			},
			wanted: CodeDuplicateChild,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newContinuation(t)
			parent := fixture.spawnParent(t, "parent", "")
			fixture.host.prepare = func(
				context.Context, string, ContinuableCreateRequest,
			) (ContinuableCreateSpec, error) {
				testCase.disturb(t, fixture)
				return ContinuableCreateSpec{}, nil
			}

			_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
				Provider: "spawn",
				Label:    "查一下",
				ChildID:  "child",
				Request:  StartRequest{Parent: parent},
			})
			if codeOf(err) != testCase.wanted {
				t.Fatalf("该报 %s，实际 %v", testCase.wanted, err)
			}
			if len(fixture.factory.created()) != 0 {
				t.Fatal("没过闸就不该建出任何孩子")
			}
		})
	}
}

// 拿到那把孩子锁之后还要再验一遍：等锁这一段里天可以变。
func TestStartContinuableRecheckesTheGatesAfterTakingTheChildLock(t *testing.T) {
	for name, testCase := range map[string]struct {
		// round 是「从解算完提供方贡献起算的第几次血统解算」。第一圈是那道
		// 解算完的准入闸，那时锁还没拿；第二圈就是拿到锁之后那道闸自己。
		round   int
		disturb func(*continuationFixture, context.CancelFunc)
		wanted  string
	}{
		// 在第一圈上取消，于是拿锁本身照旧成功——报出取消的是拿到锁之后那道检查。
		"拿到锁之后发现被取消了": {
			round:   1,
			disturb: func(_ *continuationFixture, cancel context.CancelFunc) { cancel() },
			wanted:  CodeCancelled,
		},
		// 血统在锁外解，所以这一下改的状态正好被同一次调用读到。
		"拿到锁之后准入关了": {
			round: 2,
			disturb: func(f *continuationFixture, _ context.CancelFunc) {
				f.manager.mutex.Lock()
				f.manager.draining = true
				f.manager.mutex.Unlock()
			},
			wanted: CodeDraining,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newContinuation(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			parent := fixture.spawnCountingParent(t, "parent", func(round int) {
				if round == testCase.round {
					testCase.disturb(fixture, cancel)
				}
			})
			fixture.host.prepare = func(
				context.Context, string, ContinuableCreateRequest,
			) (ContinuableCreateSpec, error) {
				parent.rearm()
				return ContinuableCreateSpec{}, nil
			}

			_, err := fixture.manager.StartContinuable(ctx, ContinuableStartSpec{
				Provider: "spawn",
				Label:    "查一下",
				ChildID:  "child",
				Request:  StartRequest{Parent: parent},
			})
			if codeOf(err) != testCase.wanted {
				t.Fatalf("该报 %s，实际 %v", testCase.wanted, err)
			}
			if len(fixture.factory.created()) != 0 {
				t.Fatal("没过闸就不该建出任何孩子")
			}
		})
	}
}

// 等那把孩子锁的当口被取消，报的是取消——而且这一路**没有**占上锁。
//
// 这条边和「拿到锁之后发现被取消了」分得开：那一条锁是空的，取消由锁**后面**那道
// 检查认出来；这一条锁被别人占着，认出取消的是等锁那一下自己。
func TestStartContinuableStopsWhileWaitingForAContendedChildLock(t *testing.T) {
	fixture := newContinuation(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	parent := fixture.spawnCountingParent(t, "parent", func(round int) {
		if round == 1 {
			cancel()
		}
	})
	release, err := fixture.manager.locks.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("先占下那把孩子锁失败：%v", err)
	}
	t.Cleanup(release)
	fixture.host.prepare = func(
		context.Context, string, ContinuableCreateRequest,
	) (ContinuableCreateSpec, error) {
		parent.rearm()
		return ContinuableCreateSpec{}, nil
	}

	_, err = fixture.manager.StartContinuable(ctx, ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 初始提示词走的是后续投递那条口，而且交回来的 id 就是它。
func TestStartContinuableSubmitsThePromptAndReturnsItsMessageID(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	started := fixture.startChild(t, parent, "child", "干活")

	child := fixture.factory.child("child")
	delivered := onlyFollowup(t, child)
	if delivered.ID != started.MessageID {
		t.Fatalf("交回来的该是那条被接受的消息 id，实际 %q／%q", started.MessageID, delivered.ID)
	}
	if textOf(delivered.Content) != "干活" {
		t.Fatalf("该原样投出那段提示词，实际 %q", textOf(delivered.Content))
	}
	// 那条消息在被认领之前算「唤醒中」，结清不许把这个空档当成静止。
	live := fixture.livingActivation(t, "child")
	fixture.manager.mutex.Lock()
	_, waking := live.accepted[started.MessageID]
	fixture.manager.mutex.Unlock()
	if !waking {
		t.Fatal("那条被接受的消息该记在唤醒窗口里")
	}
}

// agent 造不出来时整次开工作废，那份活化不留下。
func TestStartContinuableRollsBackWhenTheFactoryFails(t *testing.T) {
	fixture := newContinuation(t)
	broken := errors.New("造不出来")
	fixture.factory.createErr = broken
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if !errors.Is(err, broken) {
		t.Fatalf("造法那次失败该保持权威，实际 %v", err)
	}
	if fixture.resident("child") {
		t.Fatal("失败之后不该有驻留")
	}
}

// 那条初始提示词投不进去时整次开工作废：调用方拿到的是那次投递的失败，而不是一个
// 起来了却一句活儿都没接到的孩子。
func TestStartContinuableRollsBackWhenThePromptIsNotAccepted(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	// 趁物化和投递之间那道缝把父摘掉：投递那一刻的血统认证于是落空。
	fixture.factory.onPublished = func(id sessionlog.SessionID) {
		if id == "child" {
			fixture.retire(t, "parent")
		}
	}

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Prompt: textContent("干活"), Parent: parent},
	})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
	if fixture.resident("child") {
		t.Fatal("没接受成就不该留下一份活化")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("回滚之后那个孩子不该还在注册表里")
	}
}

// ---- 后续投递 ----

func TestFollowupNeedsAParent(t *testing.T) {
	fixture := newContinuation(t)
	_, err := fixture.manager.Followup(t.Context(), nil, "child", textContent("再来"), FollowupOptions{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有父该被拒，实际 %v", err)
	}
}

// 活化在就直接送，不重建。
func TestFollowupDeliversToAResidentChild(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	messageID, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("再来一件"), FollowupOptions{},
	)
	if err != nil {
		t.Fatalf("后续投递失败：%v", err)
	}
	if len(fixture.factory.resumed()) != 0 {
		t.Fatal("活化还在就不该重建")
	}
	followups, _, _ := fixture.factory.child("child").delivered()
	if len(followups) != 2 || followups[1].ID != messageID {
		t.Fatalf("该在原来那个孩子上再投一条，实际 %#v", followups)
	}
}

// 不是这个孩子的直系父就投不进去，哪怕它自己是活的。
func TestFollowupRefusesAnUnrelatedParent(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	stranger := fixture.spawnParent(t, "stranger", "")
	fixture.startChild(t, parent, "child", "干活")

	_, err := fixture.manager.Followup(
		t.Context(), stranger, "child", textContent("再来"), FollowupOptions{},
	)
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

// 没有驻留就从存档里重建一段轮次，再把这条消息投进去。
func TestFollowupColdResumesAChildThatIsNotResident(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	descriptor := continuableInput()
	descriptor.AgentProvider, descriptor.AgentModel = "存下来的", "存下来的模型"
	fixture.persistChild(t, "child", "parent", descriptor)

	messageID, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if err != nil {
		t.Fatalf("冷恢复失败：%v", err)
	}
	resumed := fixture.factory.resumed()
	if len(resumed) != 1 || resumed[0].ResumeSessionID != "child" {
		t.Fatalf("该续跑那个耐久孩子，实际 %#v", resumed)
	}
	// 路由从存下来那份描述符读，不从父身上重取。
	if resumed[0].AgentOptions.Provider != "存下来的" || resumed[0].AgentOptions.Model != "存下来的模型" {
		t.Fatalf("路由该从描述符里读，实际 %#v", resumed[0].AgentOptions)
	}
	if !fixture.resident("child") {
		t.Fatal("冷恢复之后该驻留着")
	}
	if onlyFollowup(t, fixture.factory.child("child")).ID != messageID {
		t.Fatal("那条消息该投进重建出来的孩子")
	}
}

// 冷恢复途中调用方走了：报的是取消，而不是把这个孩子判成「用不了」。
//
// 这两句话对调用方是两回事：[CodeNotResumable] 说的是「别拿这个 id 重试」，而这里
// 那份存档一个字都没坏，重试完全该继续。
func TestFollowupReportsCancellationRatherThanBlamingTheArchive(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// 在建孩子那一步里同时取消并让它失败：物化于是带着一个已经走了的调用方回来。
	if _, err := fixture.manager.deps.Setups.Register(
		func(context.Context, *scope.Scope) (func(context.Context) error, error) {
			cancel()
			return nil, errors.New("装不上")
		},
	); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	_, err := fixture.manager.Followup(ctx, parent, "child", textContent("接着干"), FollowupOptions{})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if fixture.resident("child") {
		t.Fatal("失败之后不该留下活化")
	}
}

// 存档里读不出这个孩子时报 [CodeNotResumable]，而不是含糊地失败。
func TestFollowupRefusesAChildThatIsNotInTheArchive(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.Followup(
		t.Context(), parent, "从来没有过", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeNotResumable {
		t.Fatalf("该报 %s，实际 %v", CodeNotResumable, err)
	}
}

// 存档里那份记录不是可续的（比如一次性那支）就恢复不了。
func TestFollowupRefusesANonContinuableRecord(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", DescriptorData{
		Version: DescriptorVersion, Mode: ModeOneShot, Provider: "spawn",
	})

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeNotResumable {
		t.Fatalf("该报 %s，实际 %v", CodeNotResumable, err)
	}
}

// 存档里那个孩子属于别人时，冷恢复这条路照样认血统。
func TestFollowupRefusesAColdChildOfAnotherParent(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "别人", continuableInput())

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

// 等那把孩子锁的当口被取消，报的是取消：这一轮连活表都没读到，所以既不投递、
// 也不要求重来。
//
// 这条边和「拿到锁之后发现被取消了」分得开：那一条锁是空的，取消由锁**后面**那道
// 检查认出来；这一条锁被别人占着，认出取消的是等锁那一下自己。
func TestFollowupOnceReportsCancellationWhileWaitingForTheChildLock(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	release, err := fixture.manager.locks.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("先占下那把孩子锁失败：%v", err)
	}
	t.Cleanup(release)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, retry, err := fixture.manager.followupOnce(
		ctx, parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeCancelled || retry {
		t.Fatalf("该报 %s 且不要求重来，实际 retry=%v err=%v", CodeCancelled, retry, err)
	}
}

// 撞上一个正在拆的活化，这一轮既不投递也不报错：它等那次拆解退场，然后要求
// 调用方再来一轮。
func TestFollowupOnceAsksForAnotherRoundAfterATeardown(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	stale := announcedActivation("child", "parent")
	stale.disposal = newDisposalTx()
	stale.disposal.settle(nil)
	fixture.plant(t, stale)

	messageID, retry, err := fixture.manager.followupOnce(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if err != nil || !retry {
		t.Fatalf("该要求再来一轮，实际 retry=%v err=%v", retry, err)
	}
	if messageID != "" {
		t.Fatalf("这一轮不该接受任何消息，实际 %q", messageID)
	}
}

// 等那次拆解的当口被取消，报的是取消：这条消息没有被接受。
func TestFollowupOnceReportsCancellationWhileWaitingForATeardown(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	stale := announcedActivation("child", "parent")
	// 一笔**没人推进**的事务：这一路只可能从取消那条边出去。
	stale.disposal = newDisposalTx()
	fixture.plant(t, stale)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, retry, err := fixture.manager.followupOnce(
		ctx, parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeCancelled || retry {
		t.Fatalf("该报 %s 且不要求重来，实际 retry=%v err=%v", CodeCancelled, retry, err)
	}
}

// 整条重试路：第一轮撞上一个拆完的活化，等它退场；第二轮那个孩子已经不驻留了，
// 于是这次投递从存档冷恢复出一份新的活化。
func TestFollowupRetriesWithAColdResumeAfterTheTeardown(t *testing.T) {
	fixture := newContinuation(t)
	var stale *activation
	var swept atomic.Bool
	// 第二次解算血统正落在两轮之间——那次拆解已经等完、下一轮还没读活表。摘掉那份
	// 陈旧活化的时机只有这一个。
	parent := fixture.spawnCountingParent(t, "parent", func(round int) {
		if round != 2 {
			return
		}
		fixture.manager.mutex.Lock()
		defer fixture.manager.mutex.Unlock()

		// 认那个确切的对象：摆在这儿的要还是那份陈旧活化，才说明第一轮真的撞上了它，
		// 而不是绕过重试直接冷恢复了一份新的。
		if fixture.manager.activations["child"] != stale {
			return
		}
		delete(fixture.manager.activations, "child")
		swept.Store(true)
	})
	fixture.persistChild(t, "child", "parent", continuableInput())

	stale = announcedActivation("child", "parent")
	stale.disposal = newDisposalTx()
	stale.disposal.settle(nil)
	fixture.plant(t, stale)

	parent.rearm()
	messageID, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if err != nil {
		t.Fatalf("重来那一轮该冷恢复成功，实际 %v", err)
	}
	if !swept.Load() {
		t.Fatal("第一轮该撞上那份陈旧活化并等它退场")
	}
	if resumed := fixture.factory.resumed(); len(resumed) != 1 {
		t.Fatalf("该恰好续跑一次，实际 %#v", resumed)
	}
	if onlyFollowup(t, fixture.factory.child("child")).ID != messageID {
		t.Fatal("那条消息该投进重建出来的孩子")
	}
}

// 整台管理器排干之后，投递在读活表之前就被拒了：这一路一个孩子都不许再唤醒。
func TestFollowupRefusesOnceTheManagerIsDraining(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	fixture.manager.mutex.Lock()
	fixture.manager.draining = true
	fixture.manager.mutex.Unlock()

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// 两轮之间准入关掉了就不再重来：那次拆解等完了，但这次投递已经没有资格开一段新的
// 轮次。这条边和「一进门就被拒」不同——它落在活化已经退场之后。
func TestFollowupStopsRetryingWhenAdmissionClosesBetweenRounds(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnCountingParent(t, "parent", func(round int) {
		if round != 2 {
			return
		}
		fixture.manager.mutex.Lock()
		fixture.manager.draining = true
		fixture.manager.mutex.Unlock()
	})

	stale := announcedActivation("child", "parent")
	stale.disposal = newDisposalTx()
	stale.disposal.settle(nil)
	fixture.plant(t, stale)

	parent.rearm()
	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// 两轮之间的取消同样把这个循环停下来：准入还开着，但调用方已经不要这个结果了。
func TestFollowupStopsRetryingWhenTheCallerCancelsBetweenRounds(t *testing.T) {
	fixture := newContinuation(t)
	ctx, cancel := context.WithCancel(t.Context())
	parent := fixture.spawnCountingParent(t, "parent", func(round int) {
		if round == 2 {
			cancel()
		}
	})

	stale := announcedActivation("child", "parent")
	stale.disposal = newDisposalTx()
	stale.disposal.settle(nil)
	fixture.plant(t, stale)

	parent.rearm()
	_, err := fixture.manager.Followup(ctx, parent, "child", textContent("接着干"), FollowupOptions{})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 一个正在被拆的活化不接直投。这条边和 Followup 那条重试路不同：调用方手里已经
// 攥着这份活化，没有「等它拆完再来一轮」的余地。
func TestSubmitAdmittedRefusesAnActivationThatIsBeingDisposed(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	target := announcedActivation("child", "parent")
	target.disposal = newDisposalTx()

	_, err := fixture.manager.submitAdmitted(
		t.Context(), target, textContent("接着干"), llm.UserSource{}, parent,
	)
	if codeOf(err) != CodeActivationClosing {
		t.Fatalf("该报 %s，实际 %v", CodeActivationClosing, err)
	}
}

// 取消在这条路的最前面就认出来，此后一个字都进不了收件箱。
func TestSubmitAdmittedRefusesACancelledCaller(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fixture.manager.submitAdmitted(
		ctx, announcedActivation("child", "parent"), textContent("接着干"), llm.UserSource{}, parent,
	)
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 准入是这条路上最后一道闸：手里攥着一份好端端的活化，也照样进不去。它排在取消
// 之后、活化自己那些判断之前，所以一台排干中的管理器一个字都收不进来。
func TestSubmitAdmittedRefusesOnceTheManagerIsDraining(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	fixture.manager.mutex.Lock()
	fixture.manager.draining = true
	fixture.manager.mutex.Unlock()

	_, err := fixture.manager.submitAdmitted(
		t.Context(), announcedActivation("child", "parent"), textContent("接着干"),
		llm.UserSource{}, parent,
	)
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// ---- 打断 ----

// 目标没有驻留时这是一次**被接受的空操作**：本来就没有什么可打断的。
func TestInterruptIsAnAcceptedNoOpForAChildThatIsNotResident(t *testing.T) {
	fixture := newContinuation(t)
	if err := fixture.manager.Interrupt("从来没有过", InterruptAuthority{
		Kind: AuthorityUser, ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("没有驻留该是一次被接受的空操作，实际 %v", err)
	}
}

func TestInterruptNeedsTheExactLiveAncestor(t *testing.T) {
	fixture := newContinuation(t)
	if err := fixture.manager.Interrupt("child", InterruptAuthority{Kind: AuthorityAncestor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("不给祖先该被拒，实际 %v", err)
	}

	// 一个已经离开注册表的祖先出示不了这份权。
	stale := agentAtDepth(t, "stale", 0)
	err := fixture.manager.Interrupt("child", InterruptAuthority{Kind: AuthorityAncestor, Agent: stale})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("陈的祖先该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

// 自打断在任何驻留检查**之前**就被拒：那是调用方把两个身份搞混了。
func TestInterruptRefusesToLetAnAgentInterruptItself(t *testing.T) {
	fixture := newContinuation(t)
	caller := fixture.spawnParent(t, "parent", "")

	err := fixture.manager.Interrupt("parent", InterruptAuthority{Kind: AuthorityAncestor, Agent: caller})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

func TestInterruptRefusesAnAuthorityKindItDoesNotKnow(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	err := fixture.manager.Interrupt("child", InterruptAuthority{Kind: "从来没有过"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("认不出的权属该被拒，实际 %v", err)
	}
}

// 人类客户端出示的是那个耐久的直系父地址；对不上就不许打断。
func TestInterruptChecksTheDurableParentAddressForAUser(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	err := fixture.manager.Interrupt("child", InterruptAuthority{
		Kind: AuthorityUser, ParentSessionID: "别的父",
	})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
	if len(fixture.factory.child("child").cancelled()) != 0 {
		t.Fatal("没被准入的打断不该动到那个孩子")
	}
}

// 对上了就停掉当下这段活动，而且**留住收件箱**——排着的活儿不该被这一下丢掉。
func TestInterruptStopsTheCurrentActivityForAUser(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	if err := fixture.manager.Interrupt("child", InterruptAuthority{
		Kind: AuthorityUser, ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("打断失败：%v", err)
	}
	causes := fixture.factory.child("child").cancelled()
	if len(causes) != 1 {
		t.Fatalf("该恰好取消一次，实际 %d 次", len(causes))
	}
	if _, byUser := causes[0].(sessionlog.UserCancel); !byUser {
		t.Fatalf("人类出示的权该记成用户取消，实际 %#v", causes[0])
	}
}

// 一个活祖先必须出现在目标记下来的血统里。
func TestInterruptChecksTheRecordedLineageForAnAncestor(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	stranger := fixture.spawnParent(t, "stranger", "")
	fixture.startChild(t, parent, "child", "干活")

	err := fixture.manager.Interrupt("child", InterruptAuthority{Kind: AuthorityAncestor, Agent: stranger})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("不在血统里该报 %s，实际 %v", CodeUnauthorized, err)
	}

	if err := fixture.manager.Interrupt("child", InterruptAuthority{
		Kind: AuthorityAncestor, Agent: parent,
	}); err != nil {
		t.Fatalf("血统里那个祖先该打得断，实际 %v", err)
	}
	causes := fixture.factory.child("child").cancelled()
	if len(causes) != 1 {
		t.Fatalf("该恰好取消一次，实际 %d 次", len(causes))
	}
	if _, byParent := causes[0].(sessionlog.ParentCancel); !byParent {
		t.Fatalf("祖先出示的权该记成父取消，实际 %#v", causes[0])
	}
}

// 已经在拆的活化不再打断：取消这件事已经发生过了。
func TestInterruptIsANoOpOnceDisposalIsUnderway(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	fixture.feignDisposal(t, "child")

	if err := fixture.manager.Interrupt("child", InterruptAuthority{
		Kind: AuthorityUser, ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("正在拆时该是空操作，实际 %v", err)
	}
	if len(fixture.factory.child("child").cancelled()) != 0 {
		t.Fatal("正在拆的活化不该再收到一次打断")
	}
}

// ---- 孩子对父的汇报 ----

func TestReportFromNeedsTheReportingChild(t *testing.T) {
	fixture := newContinuation(t)
	_, err := fixture.manager.ReportFrom(t.Context(), nil, textContent("说一声"), ReportOptions{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有汇报方该被拒，实际 %v", err)
	}
}

func TestReportFromStopsWhenTheCallerAlreadyCancelled(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fixture.manager.ReportFrom(ctx, fixture.factory.child("child"), textContent("说一声"), ReportOptions{})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 排干里的管理器连一条汇报都不再收：这一路在认出汇报方之前就被拒了。
func TestReportFromRefusesOnceTheManagerIsDraining(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	fixture.manager.mutex.Lock()
	fixture.manager.draining = true
	fixture.manager.mutex.Unlock()

	_, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("child"), textContent("说一声"), ReportOptions{},
	)
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// 不是一个活着的可续孩子就汇报不了——这条口不给别的 agent 用。
func TestReportFromRefusesAnAgentThatIsNotAResidentChild(t *testing.T) {
	fixture := newContinuation(t)
	outsider := fixture.spawnParent(t, "outsider", "")

	_, err := fixture.manager.ReportFrom(t.Context(), outsider, textContent("说一声"), ReportOptions{})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

// 正在拆的活化不许再往上说话。
func TestReportFromRefusesWhileTheActivationIsClosing(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	fixture.feignDisposal(t, "child")

	_, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("child"), textContent("说一声"), ReportOptions{},
	)
	if codeOf(err) != CodeActivationClosing {
		t.Fatalf("该报 %s，实际 %v", CodeActivationClosing, err)
	}
}

// 直系父已经不在了：这次汇报没有去处，明说而不是无声地丢掉。
func TestReportFromRefusesWhenTheParentIsGone(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")
	fixture.retire(t, "parent")

	_, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("child"), textContent("说一声"), ReportOptions{},
	)
	if codeOf(err) != CodeParentUnavailable {
		t.Fatalf("该报 %s，实际 %v", CodeParentUnavailable, err)
	}
}

// 安静那一支注入父的下一个前置步骤，不唤醒它；打头那一行说清是谁在说话。
func TestReportFromInjectsAQuietReport(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	messageID, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("child"), textContent("干完一半了"), ReportOptions{Delivery: DeliveryQuiet},
	)
	if err != nil {
		t.Fatalf("汇报失败：%v", err)
	}
	_, steers, injects := parent.delivered()
	if len(steers) != 0 || len(injects) != 1 {
		t.Fatalf("安静那一支该只走注入，实际 steer=%d inject=%d", len(steers), len(injects))
	}
	if injects[0].ID != messageID {
		t.Fatalf("交回来的该是那条消息的 id，实际 %q／%q", messageID, injects[0].ID)
	}
	body := textOf(injects[0].Content)
	if !strings.HasPrefix(body, "Background subagent child reported:") {
		t.Fatalf("打头那一行该说清是谁在说话，实际 %q", body)
	}
	if !strings.Contains(body, "干完一半了") {
		t.Fatalf("该带上孩子说的话，实际 %q", body)
	}
	// 归属在来源上，而不是靠那一行文字。
	sender, found, err := SenderSessionIDOf(injects[0].Source)
	if err != nil || !found || sender != "child" {
		t.Fatalf("来源该认得出汇报方，实际 %q found=%v err=%v", sender, found, err)
	}
}

// next-step 那一支引导进父最近的那个步骤，会唤醒它。
func TestReportFromSteersANextStepReport(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	if _, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("child"), textContent("要紧的事"), ReportOptions{Delivery: DeliveryNextStep},
	); err != nil {
		t.Fatalf("汇报失败：%v", err)
	}
	_, steers, injects := parent.delivered()
	if len(steers) != 1 || len(injects) != 0 {
		t.Fatalf("next-step 那一支该只走引导，实际 steer=%d inject=%d", len(steers), len(injects))
	}
}

// 父自己也是一个可续孩子时，这次唤醒要记进它那扇结清窗口——不然父会在
// 「消息已接受、还没被认领」那个空档里被判成静止、拆掉。
func TestReportFromRecordsTheWakeUpWhenTheParentIsItselfAChild(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	middle := fixture.factory.child("middle")
	fixture.startChild(t, middle, "leaf", "干活")

	report, err := fixture.manager.ReportFrom(
		t.Context(), fixture.factory.child("leaf"), textContent("说一声"),
		ReportOptions{Delivery: DeliveryNextStep},
	)
	if err != nil {
		t.Fatalf("汇报失败：%v", err)
	}
	live := fixture.livingActivation(t, "middle")
	fixture.manager.mutex.Lock()
	_, waking := live.accepted[report]
	fixture.manager.mutex.Unlock()
	if !waking {
		t.Fatal("父自己是可续孩子时，这次唤醒该记进它那扇窗口")
	}
}
