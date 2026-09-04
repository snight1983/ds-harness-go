// 本文件的作用：一次活化从物化走到结清那整条路的测试——落盘日志的边界、种进去的
// 派发策略、所有权与唤醒、冷恢复剩下那几支、投不进去时的回滚，以及静止守望把孩子
// 放掉并把这份交代交回父的那一整段。

package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/subagent/internal/childseed"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// ---- 共用的小工具 ----

// watchEnds 挂一个全局的终止观察者，交回那条会收到每一次结清的通道。
//
// 这是本包唯一一处**不靠等**就能观察到「一次拆解真的跑完了」的地方：终止那条边由
// finishDisposal 收尾处的 observer.settle 发出来，所以读到它就等于那次拆解已经落定。
// 送是不阻塞的——一个塞住的观察者会把处置那条线钉死在发射器里。
func (f *continuationFixture) watchEnds(t *testing.T) <-chan RunEndInfo {
	t.Helper()
	ended := make(chan RunEndInfo, 16)
	remove := f.host.emitter.layers.Global().end.Append(func(info RunEndInfo, _ agent.Agent) {
		select {
		case ended <- info:
		default:
		}
	})
	t.Cleanup(remove)
	return ended
}

// plant 把一份手工造的活化摆进活表，收尾时再摘掉。
//
// 必须摘：这些活化的句柄没有 Dispose，留着的话拥有它的作用域一处置就会去拆一个
// 拆不了的东西。
func (f *continuationFixture) plant(t *testing.T, target *activation) {
	t.Helper()
	f.manager.mutex.Lock()
	f.manager.activations[target.childID] = target
	f.manager.mutex.Unlock()

	t.Cleanup(func() {
		f.manager.mutex.Lock()
		delete(f.manager.activations, target.childID)
		f.manager.mutex.Unlock()
	})
}

// announcedActivation 摆一份**已经通告过**的活化：调用方拿到过这个孩子的 id，
// 因此它的结清是父应得的一份交代。
func announcedActivation(childID, parentSession sessionlog.SessionID) *activation {
	return &activation{
		childID:       childID,
		parentSession: parentSession,
		announced:     true,
		ownedChildren: map[sessionlog.SessionID]struct{}{},
		accepted:      map[llm.MessageID]struct{}{},
		poke:          make(chan struct{}),
	}
}

// accepted 数一份活化此刻还有几条被接受、但还没看着它离开收件箱的消息。
func (f *continuationFixture) accepted(target *activation) int {
	f.manager.mutex.Lock()
	defer f.manager.mutex.Unlock()

	return len(target.accepted)
}

// closed 说这条通道关没关。
func closed(channel chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// ---- 落盘日志的边界 ----

func TestChildOwnEventsSlicesOffTheInheritedPrefix(t *testing.T) {
	own := childOwnEvents(persistence.Inspection{
		Meta:   sessionlog.SessionHeader{SeedLength: 2},
		Events: []sessionlog.Event{{Seq: 0}, {Seq: 1}, {Seq: 2}},
	})
	if len(own) != 1 || own[0].Seq != 2 {
		t.Fatalf("该只剩这个孩子自己那截，实际 %#v", own)
	}
}

// 那个长度来自介质，所以两头都要夹住——越界在 Go 里是 panic，不是空数组。
func TestChildOwnEventsClampsABoundaryFromTheMedium(t *testing.T) {
	events := []sessionlog.Event{{Seq: 0}, {Seq: 1}}

	if own := childOwnEvents(persistence.Inspection{
		Meta:   sessionlog.SessionHeader{SeedLength: -5},
		Events: events,
	}); len(own) != 2 {
		t.Fatalf("负的边界该当成 0，实际 %#v", own)
	}
	if own := childOwnEvents(persistence.Inspection{
		Meta:   sessionlog.SessionHeader{SeedLength: 99},
		Events: events,
	}); len(own) != 0 {
		t.Fatalf("越过全长的边界该当成全长，实际 %#v", own)
	}
}

// 边界是**绝对 seq**，不是条数：一份被弹过头的日志上，拿 SeedLength 直接当下标
// 会把这个孩子自己的事件一条条当成继承来的丢掉。
func TestChildOwnEventsMeasuresTheBoundaryFromTheLogStart(t *testing.T) {
	own := childOwnEvents(persistence.Inspection{
		Meta:   sessionlog.SessionHeader{SeedBaseSeq: 40, SeedLength: 2},
		Events: []sessionlog.Event{{Seq: 40}, {Seq: 41}, {Seq: 42}, {Seq: 43}},
	})
	if len(own) != 2 || own[0].Seq != 42 {
		t.Fatalf("该从 seq 42 起，实际 %#v", own)
	}

	// 继承来的那一段整个被弹掉了：剩下的全归这个孩子自己。
	own = childOwnEvents(persistence.Inspection{
		Meta:   sessionlog.SessionHeader{SeedBaseSeq: 0, SeedLength: 2},
		Events: []sessionlog.Event{{Seq: 9}, {Seq: 10}},
	})
	if len(own) != 2 {
		t.Fatalf("继承的那段被弹光之后该整段都归自己，实际 %#v", own)
	}
}

// ---- 种进去的派发策略 ----

func TestChildSeedLeavesTheSeedAloneWhenThereIsNothingToSeed(t *testing.T) {
	seed := descriptorLog(t, 0, "查一下")
	staged, err := childseed.Seed("child", seed, 0, "")
	if err != nil {
		t.Fatalf("排演种子失败：%v", err)
	}
	if len(staged) != len(seed) {
		t.Fatalf("一条策略都没有就该原样交回，实际 %#v", staged)
	}
}

// 那条策略落在种子**之后**，所以它仍旧是这个孩子自己的历史。
func TestChildSeedAppendsAfterTheSeed(t *testing.T) {
	seed := descriptorLog(t, 0, "查一下")
	staged, err := childseed.Seed("child", seed, 0, "never")
	if err != nil {
		t.Fatalf("排演种子失败：%v", err)
	}
	if len(staged) <= len(seed) {
		t.Fatalf("该多出那条策略，实际 %#v", staged)
	}
	if staged[0].Type != EventDescriptor {
		t.Fatalf("原来那段种子该留在前面，实际 %#v", staged[0])
	}
	if last := staged[len(staged)-1]; last.Type != userapproval.EventPolicy {
		t.Fatalf("最后一条该是那条策略，实际 %#v", last)
	}
}

// brokenSeed 造一份**排演不出来**的创建种子：序号从起点断开，而活会话表要求种子
// 是从 baseSeq 起连续的。
func brokenSeed(t *testing.T) []sessionlog.Event {
	t.Helper()
	seed := descriptorLog(t, 0, "查一下")
	seed[0].Seq = 7
	return seed
}

// 种子不从 0 起时把 baseSeq 一起交下去，排演才走得通：父日志被弹过头之后，
// 一段合法的分叉前缀本来就不从 0 起。
func TestChildSeedStagesASeedThatDoesNotStartAtZero(t *testing.T) {
	seed := descriptorLog(t, 0, "查一下")
	for index := range seed {
		seed[index].Seq += 40
	}
	staged, err := childseed.Seed("child", seed, 40, "never")
	if err != nil {
		t.Fatalf("一段从 seq 40 起的种子本该排演得动，却报了 %v", err)
	}
	if len(staged) <= len(seed) {
		t.Fatalf("该多出那条策略，实际 %#v", staged)
	}
	if staged[0].Seq != 40 {
		t.Fatalf("排演出来的第一条该还在 seq 40 上，实际 %d", staged[0].Seq)
	}
}

// 排演不出来的种子当场把这一步拦下，而不是把那条策略悄悄丢掉：一个没种上策略的
// 孩子是一个不受这次派发约束的孩子。
func TestChildSeedRefusesASeedItCannotStage(t *testing.T) {
	if _, err := childseed.Seed("child", brokenSeed(t), 0, "never"); err == nil {
		t.Fatal("排演不出来的种子该被拒")
	}
}

// 这条失败落在建 agent **之前**：造法一次都没被叫到，也没有活化留下来。
func TestMaterializeStopsWhenTheDelegatedPolicySeedCannotBeStaged(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	_, err := fixture.manager.materialize(t.Context(), materializeInputs{
		childID:  "child",
		provider: "spawn",
		parent:   parent,
		create: &materializeCreate{
			seed:              brokenSeed(t),
			meta:              ChildSessionMeta(parent, 1, 0, nil),
			delegatedPolicies: DelegatedPolicyOverrides{ApprovalPolicy: "never"},
		},
	})
	if err == nil {
		t.Fatal("排演不出来的种子该把这次物化拦下")
	}
	if created := fixture.factory.created(); len(created) != 0 {
		t.Fatalf("不该走到建 agent 那一步，实际 %#v", created)
	}
	if fixture.resident("child") {
		t.Fatal("失败之后不该留下活化")
	}
}

// ---- 所有权、唤醒、销号 ----

// 一个顶层的、或者别的什么 agent 待在这张等待图之外，立不起也不算失败。
func TestAcquireOwnershipIgnoresAParentThatIsNotResident(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	if err := fixture.manager.acquireOwnership(parent, "child"); err != nil {
		t.Fatalf("不驻留的父该被无视，实际 %v", err)
	}
}

// 一个自己正在被处置的父底下立不起新孩子——立起来了也马上跟着一起被拆掉。
func TestAcquireOwnershipRefusesAParentThatIsAlreadyClosing(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	fixture.feignDisposal(t, "middle")

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "leaf",
		Request:  StartRequest{Parent: fixture.factory.child("middle"), Prompt: textContent("干活")},
	})
	if codeOf(err) != CodeActivationClosing {
		t.Fatalf("该报 %s，实际 %v", CodeActivationClosing, err)
	}
	if fixture.resident("leaf") {
		t.Fatal("没立起来的孩子不该留下一份活化")
	}
}

// 一个孩子从**每一个**记着它的主人名下摘掉，而且每个主人都重新看一次结清。
func TestReleaseOwnershipDetachesFromEveryOwnerAndWakesIt(t *testing.T) {
	fixture := newContinuation(t)
	first, _ := bareActivation(t, "first")
	second, _ := bareActivation(t, "second")
	first.ownedChildren["child"] = struct{}{}
	second.ownedChildren["child"] = struct{}{}
	fixture.plant(t, first)
	fixture.plant(t, second)

	firstPoke, secondPoke := first.poke, second.poke
	fixture.manager.releaseOwnership("child")

	if len(first.ownedChildren) != 0 || len(second.ownedChildren) != 0 {
		t.Fatal("那个孩子该从两个主人名下都摘掉")
	}
	if !closed(firstPoke) || !closed(secondPoke) {
		t.Fatal("两个主人都该被叫醒重看一次")
	}
}

// 唤醒是「关掉现在这条、换上一条新的」：不换的话下一次唤醒无处可关。
func TestWakeSwapsThePokeChannel(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")

	before := target.poke
	fixture.manager.wake(target)
	if !closed(before) {
		t.Fatal("原来那条该被关掉")
	}
	if target.poke == before || closed(target.poke) {
		t.Fatal("该换上一条崭新的唤醒通道")
	}
}

// 只有真的被接受过的 id 才惊动那个守望：认领一条从来没接受过的消息什么都不该动。
func TestRetireOnlyWakesForAMessageItHadAdmitted(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")
	target.accepted["m1"] = struct{}{}

	before := target.poke
	fixture.manager.retire(target, "从来没有过")
	if target.poke != before || closed(before) {
		t.Fatal("没被接受过的 id 不该惊动那个守望")
	}

	fixture.manager.retire(target, "m1")
	if len(target.accepted) != 0 {
		t.Fatalf("那条被接受的消息该销掉，实际 %#v", target.accepted)
	}
	if !closed(before) || target.poke == before {
		t.Fatal("销掉之后该叫醒守望并换上新通道")
	}
}

// ---- 冷恢复剩下那几支 ----

// 取消落在读存档那一下：报的是取消，而不是「这个孩子恢复不了」。
func TestColdResumeReportsCancellationFromTheArchiveProbe(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())

	ctx, cancel := context.WithCancel(t.Context())
	fixture.store.inspectErr["child"] = errors.New("读不动")
	fixture.store.onInspect = func(sessionlog.SessionID) { cancel() }

	_, err := fixture.manager.Followup(ctx, parent, "child", textContent("接着干"), FollowupOptions{})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 读存档是一个让出点，所以读完之后准入要重验一次。
func TestColdResumeRechecksAdmissionAfterReadingTheArchive(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())
	fixture.store.onInspect = func(sessionlog.SessionID) {
		fixture.manager.mutex.Lock()
		fixture.manager.draining = true
		fixture.manager.mutex.Unlock()
	}

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// 物化自己那道准入闸排在最前头：它挡在那份排干栅栏之前，于是一台正在排干的管理器
// 绝不会再多出一次要等的物化。
func TestMaterializeRefusesOnceTheManagerIsDraining(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	fixture.manager.mutex.Lock()
	fixture.manager.draining = true
	fixture.manager.mutex.Unlock()

	_, err := fixture.manager.materialize(t.Context(), materializeInputs{
		childID: "child", provider: "spawn", parent: parent,
	})
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
	if len(fixture.manager.materializations) != 0 {
		t.Fatal("没过闸的物化不该被挂进那份栅栏")
	}
}

// 那份栅栏挂上之后紧接着又查一次取消：调用方走了就别再去动 agent 注册表。
func TestMaterializeStopsWhenTheCallerAlreadyCancelled(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := fixture.manager.materialize(ctx, materializeInputs{
		childID: "child", provider: "spawn", parent: parent,
	})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if len(fixture.factory.created()) != 0 {
		t.Fatal("取消之后不该建出任何孩子")
	}
}

// 存档读成了、调用方却在这中间走了：报的是取消，不是「这个孩子恢复不了」。
//
// 和上面那条分得开：那一条是**读失败**之后回头认取消，这一条读是成功的。
func TestColdResumeStopsWhenTheCallerLeavesDuringASuccessfulProbe(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())

	ctx, cancel := context.WithCancel(t.Context())
	fixture.store.onInspect = func(sessionlog.SessionID) { cancel() }

	_, err := fixture.manager.Followup(ctx, parent, "child", textContent("接着干"), FollowupOptions{})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if len(fixture.factory.resumed()) != 0 {
		t.Fatal("取消之后不该续跑任何东西")
	}
}

// 没装持久化就冷恢复不了：这条路读的是那个孩子自己那份落盘日志。
func TestColdResumeNeedsPersistence(t *testing.T) {
	agents, err := agent.NewRegistry(agent.RegistryOptions{})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	manager, err := NewContinuationManager(ContinuationDeps{
		Owner:    keyedScope(t, "subagents", nil),
		Agents:   agents,
		Sessions: sessions,
		Setups:   NewActivationSetupRegistry(),
	}, &fakeHost{})
	if err != nil {
		t.Fatalf("造续接管理器失败：%v", err)
	}

	_, err = manager.coldResume(
		t.Context(), agentAtDepth(t, "parent", 0), "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodePersistenceUnavailable {
		t.Fatalf("该报 %s，实际 %v", CodePersistenceUnavailable, err)
	}
}

// 一条折不开的描述符记录原样往上交：这不是「没有可续状态」，而是这份存档本身坏了。
func TestColdResumeSurfacesAnUnreadableDescriptorRecord(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.store.put(sessionlog.SessionHeader{
		Version:         sessionlog.FormatVersion,
		ID:              "child",
		WorkspaceID:     testWorkspaceID,
		ParentSession:   "parent",
		Origin:          sessionlog.OriginSubagent,
		DelegationDepth: 1,
	}, sessionlog.Event{Seq: 0, Type: EventDescriptor, Data: json.RawMessage(`这不是 JSON`)})

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if err == nil {
		t.Fatal("折不开的记录该把这次冷恢复停下来")
	}
	if codeOf(err) == CodeNotResumable {
		t.Fatalf("这不是「没有可续状态」，实际 %v", err)
	}
}

// 落盘的那份工具范围在冷恢复时照样生效：它是重建这个孩子的输入之一，不是只在
// 第一次开工时用过一回。
func TestColdResumeAppliesTheRecordedToolFilter(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	descriptor := continuableInput()
	// 点到一件装配里根本不存在的工具，于是这份范围会被工具运行时当场拒掉——
	// 那次拒绝就是「这份范围真的被拿去装了」的凭据。
	descriptor.ToolFilter = &tools.Restriction{Allow: []string{"根本没有这件"}}
	fixture.persistChild(t, "child", "parent", descriptor)

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeNotResumable {
		t.Fatalf("该报 %s，实际 %v", CodeNotResumable, err)
	}
}

// 本包自己那些带码的物化失败原样往上交：一律裹成 NOT_RESUMABLE 会把「谁都不许投递」
// 说成「这个孩子没了」，而后者会劝调用方别再拿这个 id 重试。
func TestColdResumePassesACodedMaterializationFailureThrough(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())
	fixture.factory.resumeErr = NewError("这一刻谁都不许起", CodeActivationClosing, nil)

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeActivationClosing {
		t.Fatalf("该原样交回 %s，实际 %v", CodeActivationClosing, err)
	}
}

// 认不出码的失败才裹成 [CodeNotResumable]，而且原因还认得出来。
func TestColdResumeWrapsAPlainMaterializationFailure(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())
	broken := errors.New("造不出来")
	fixture.factory.resumeErr = broken

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if codeOf(err) != CodeNotResumable {
		t.Fatalf("该报 %s，实际 %v", CodeNotResumable, err)
	}
	if !errors.Is(err, broken) {
		t.Fatalf("那个原因该还认得出来，实际 %v", err)
	}
}

// ---- 公布时装上的那两个收件箱观察者 ----

// 一条被接受的消息恰好离开收件箱一次：要么被认领、要么被丢弃。公布时装上的那两个
// 观察者是这件事唯一的来源，少哪一个都会让这份活化永远停在「还有活儿没准入」上，
// 于是它再也结不了清。
func TestPublishedActivationRetiresAMessageThatLeavesTheInbox(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	started := fixture.startChild(t, parent, "child", "干活")
	child := fixture.factory.child("child")
	live := fixture.livingActivation(t, "child")

	// 摆成在跑：销掉最后一条被接受的消息会叫醒那个守望，而一个静止又没有后代的
	// 孩子会当场被它拆掉——那样这里断言的就不是观察者，而是一场竞速。
	child.setStatus(agent.StatusRunning)

	second, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
	if err != nil {
		t.Fatalf("后续投递失败：%v", err)
	}
	if count := fixture.accepted(live); count != 2 {
		t.Fatalf("该有两条被接受的消息，实际 %d", count)
	}

	if err := fixture.agents.ReportInboxClaimed(child, llm.Message{ID: started.MessageID}, 0); err != nil {
		t.Fatalf("报收件箱认领失败：%v", err)
	}
	if count := fixture.accepted(live); count != 1 {
		t.Fatalf("被认领那条该销掉，实际还剩 %d", count)
	}

	if err := fixture.agents.ReportInboxDiscarded(child, llm.Message{ID: second}); err != nil {
		t.Fatalf("报收件箱丢弃失败：%v", err)
	}
	if count := fixture.accepted(live); count != 0 {
		t.Fatalf("被丢弃那条也该销掉，实际还剩 %d", count)
	}
}

// 观察者只认自己那个孩子：同一把作用域上的登记会听见别的 agent 的收件箱动静，
// 认错了就会把一份不相干的活化提前判成静止。
func TestPublishedActivationIgnoresAnotherAgentsInbox(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	started := fixture.startChild(t, parent, "child", "干活")
	live := fixture.livingActivation(t, "child")
	fixture.factory.child("child").setStatus(agent.StatusRunning)

	// 拿父去报同一条消息 id：观察者看见的不是它认的那个 agent。
	if err := fixture.agents.ReportInboxClaimed(parent, llm.Message{ID: started.MessageID}, 0); err != nil {
		t.Fatalf("报收件箱认领失败：%v", err)
	}
	if count := fixture.accepted(live); count != 1 {
		t.Fatalf("别人的收件箱动静该被无视，实际还剩 %d", count)
	}
}

// ---- 公布那道闸上的回滚 ----

// 建 agent 是一个让出点，所以句柄交接之后准入要重验一次。这条边和「投不进去」
// 那条不同：这一次连消息都还没投。
func TestMaterializeRollsBackWhenAdmissionClosesDuringPublication(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	// 趁孩子已经公布、句柄还没交回来的那一刻关掉准入。
	fixture.factory.onPublished = func(id sessionlog.SessionID) {
		if id != "child" {
			return
		}
		fixture.manager.mutex.Lock()
		fixture.manager.draining = true
		fixture.manager.mutex.Unlock()
	}

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Prompt: textContent("干活"), Parent: parent},
	})
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
	if fixture.resident("child") {
		t.Fatal("公布没过闸就不该留下一份活化")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("回滚之后那个孩子不该还在注册表里")
	}
}

// 同一道缝上的取消同样整个回滚，而且报的是取消——调用方拿不到这个孩子的 id，
// 所以它绝不许留下一个谁也够不着的驻留孩子。
func TestMaterializeRollsBackWhenTheCallerCancelsDuringPublication(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	ctx, cancel := context.WithCancel(t.Context())
	fixture.factory.onPublished = func(id sessionlog.SessionID) {
		if id == "child" {
			cancel()
		}
	}

	_, err := fixture.manager.StartContinuable(ctx, ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Prompt: textContent("干活"), Parent: parent},
	})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if fixture.resident("child") {
		t.Fatal("取消之后不该留下一份活化")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("回滚之后那个孩子不该还在注册表里")
	}
}

// 那两笔收件箱登记挂在孩子自己那把作用域上，作用域没了就挂不上去。挂不上去还照旧
// 公布的话，被接受的消息离开收件箱时没人来销号，这个孩子会永远停在「还在跑」上。
func TestMaterializeRollsBackWhenTheInboxHooksCannotBeRegistered(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	// 趁同一道缝把孩子那把作用域放掉：公布那一步于是在第一笔登记上就走不下去。
	fixture.factory.onPublished = func(id sessionlog.SessionID) {
		if id == "child" {
			_ = fixture.factory.child(id).Scope().Dispose(context.Background())
		}
	}

	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Prompt: textContent("干活"), Parent: parent},
	})
	if err == nil {
		t.Fatal("登记挂不上去该把这次开工拦下")
	}
	if fixture.resident("child") {
		t.Fatal("回滚之后不该留下一份活化")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("回滚之后那个孩子不该还在注册表里")
	}
}

// ---- 投不进去就整个回滚 ----

// 接受是这次操作的成功边界：没接受成，那次物化就不许留下一个驻留的孩子。
func TestSubmitMaterializedRollsBackWhenTheMessageIsNotAccepted(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.persistChild(t, "child", "parent", continuableInput())
	// 趁物化和投递之间那道缝把父摘掉：投递那一刻的血统认证于是落空。
	fixture.factory.onPublished = func(id sessionlog.SessionID) {
		if id == "child" {
			fixture.retire(t, "parent")
		}
	}

	_, err := fixture.manager.Followup(
		t.Context(), parent, "child", textContent("接着干"), FollowupOptions{},
	)
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

// ---- 静止守望走到结清 ----

// 那条初始提示词一离开收件箱，这个孩子就既不在跑、也没有名下后代了：守望于是把它
// 放掉，并且在放掉所有权**之前**把这份交代投给父。
func TestSettlementReleasesTheChildAndTellsTheParent(t *testing.T) {
	fixture := newContinuation(t)
	ended := fixture.watchEnds(t)
	parent := fixture.spawnParent(t, "parent", "")
	started := fixture.startChild(t, parent, "child", "干活")
	child := fixture.factory.child("child")

	// 次序是要紧的。先让这个孩子自己的循环退出来：守望那一圈于是每次都从 WhenIdle
	// 落到「等下一次唤醒」上，而不是停在一个永远不返回的 WhenIdle 上——那样一次
	// 落在它两圈之间的唤醒就丢了。
	child.settle()
	// 再把那条初始提示词从收件箱里销掉。这才是「收件箱空了」，守望这时才判得出静止：
	// 在这之前它读到的是「安静但还有一条被接受的活儿」，也就是那个空档。
	fixture.manager.retire(fixture.livingActivation(t, "child"), started.MessageID)

	end := <-ended
	if end.ID != "child" || end.StopReason != StopCompleted {
		t.Fatalf("该发出这个孩子那条干净的终止边，实际 %#v", end)
	}
	if fixture.resident("child") {
		t.Fatal("结清之后不该还驻留着")
	}
	if _, still := fixture.agents.Get("child"); still {
		t.Fatal("结清之后那个孩子不该还在注册表里")
	}
	// 放掉之前先停下来：拆解走的是取消这条路，不是把句柄结构性地拆掉。
	if len(child.cancelled()) == 0 {
		t.Fatal("拆解该先取消那个孩子")
	}
	// 父是静止的，所以它得到一个普通回合。
	notice := onlyFollowup(t, parent)
	sender, ours, err := SenderSessionIDOf(notice.Source)
	if err != nil || !ours || sender != "child" {
		t.Fatalf("那条通知该记在这个孩子头上，实际 %q ours=%v err=%v", sender, ours, err)
	}
	if !strings.Contains(textOf(notice.Content), "child") {
		t.Fatalf("那句交代该点出是哪个孩子，实际 %q", textOf(notice.Content))
	}
}

// 守望自己开出来的那次拆解失败了：这条路上没有调用方可报，所以它记一条诊断就完事，
// 而这个孩子照旧被放掉——留着会把它整条祖先链永远钉在 waiting 上。
func TestSettlementLogsATeardownFailureAndStillReleasesTheChild(t *testing.T) {
	fixture := newContinuation(t)
	ended := fixture.watchEnds(t)
	fixture.factory.disposeErr = errors.New("拆不干净")
	parent := fixture.spawnParent(t, "parent", "")
	started := fixture.startChild(t, parent, "child", "干活")

	fixture.factory.child("child").settle()
	fixture.manager.retire(fixture.livingActivation(t, "child"), started.MessageID)

	if end := <-ended; end.ID != "child" {
		t.Fatalf("该发出这个孩子那条终止边，实际 %#v", end)
	}
	if fixture.resident("child") {
		t.Fatal("拆解失败也该把这份活化摘掉")
	}
}

// ---- 结清那份交代投给谁、怎么投 ----

// 一次在首次接受之前就回滚掉的物化保持沉默：调用方被告知的是那个孩子没有立起来。
func TestNotifySettlementStaysSilentForAChildTheCallerNeverGotAnIDFor(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	target := announcedActivation("child", "parent")
	target.announced = false

	fixture.manager.notifySettlement(target, activationTerminal{StopReason: StopCompleted})

	if followups, steers, injects := parent.delivered(); len(followups)+len(steers)+len(injects) != 0 {
		t.Fatal("没通告过的孩子结清时该一声不吭")
	}
}

// 父不在了不算错：孩子自己那份会话无论如何都是耐久记录。
func TestNotifySettlementStaysSilentWhenTheParentIsGone(t *testing.T) {
	fixture := newContinuation(t)
	fixture.manager.notifySettlement(
		announcedActivation("child", "从来没有过"),
		activationTerminal{StopReason: StopCompleted},
	)
}

// 静止的父得到一个普通回合，忙着的父被引导——收件箱一次认领会把整批 next-step
// 在同一道边界上取走，于是几个孩子一起结清只花一个步骤。
func TestNotifySettlementFollowsUpAnIdleParentAndSteersABusyOne(t *testing.T) {
	for name, busy := range map[string]bool{"父静止着": false, "父忙着": true} {
		t.Run(name, func(t *testing.T) {
			fixture := newContinuation(t)
			parent := fixture.spawnParent(t, "parent", "")
			if busy {
				parent.status = agent.StatusRunning
			}

			fixture.manager.notifySettlement(
				announcedActivation("child", "parent"),
				activationTerminal{StopReason: StopCompleted, Output: textContent("交差了")},
			)

			followups, steers, _ := parent.delivered()
			if busy && (len(steers) != 1 || len(followups) != 0) {
				t.Fatalf("忙着的父该被引导，实际 followups=%d steers=%d", len(followups), len(steers))
			}
			if !busy && (len(followups) != 1 || len(steers) != 0) {
				t.Fatalf("静止的父该得到一个回合，实际 followups=%d steers=%d", len(followups), len(steers))
			}
			delivered := append(followups, steers...)
			if !strings.Contains(textOf(delivered[0].Content), "交差了") {
				t.Fatalf("孩子最后那段话该带上，实际 %q", textOf(delivered[0].Content))
			}
		})
	}
}

// 一个自己血统已经在关的父收到这条交代，但**不**被唤醒：拆解不是开回合的理由。
func TestNotifySettlementInjectsIntoAParentThatIsItselfClosing(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	fixture.manager.mutex.Lock()
	fixture.manager.closingMembersLocked(parent)[parent] = struct{}{}
	fixture.manager.mutex.Unlock()

	fixture.manager.notifySettlement(
		announcedActivation("child", "parent"),
		activationTerminal{StopReason: StopCompleted},
	)

	followups, steers, injects := parent.delivered()
	if len(injects) != 1 || len(followups)+len(steers) != 0 {
		t.Fatalf("该只注入不唤醒，实际 followups=%d steers=%d injects=%d",
			len(followups), len(steers), len(injects))
	}
}

// 一个字都没留下也要说清楚，而不是让父自己去猜那段空白。
func TestNotifySettlementSaysSoWhenThereWasNoClosingMessage(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	fixture.manager.notifySettlement(
		announcedActivation("child", "parent"),
		activationTerminal{StopReason: StopAborted},
	)

	body := textOf(onlyFollowup(t, parent).Content)
	if !strings.Contains(body, "It left no closing message.") {
		t.Fatalf("该明说一个字都没留下，实际 %q", body)
	}
}

// ---- 最后那次刷盘 ----

// 刷盘是尽力而为的：它失败了也绝不挡着句柄处置或者所有权释放——留住一个孩子会把
// 它整条祖先链永久钉在 waiting 上。
func TestFlushFinalStateSwallowsAFailureFromAnUnregisteredSession(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")

	// 这个孩子那份会话从来没进过活会话表，所以刷盘一定不成。
	fixture.manager.flushFinalState(context.Background(), target)
}
