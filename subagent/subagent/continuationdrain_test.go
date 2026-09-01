// 本文件的作用：那三种拆解的测试——整台管理器排干、按确切的宿主根排干后代、
// 点名放掉直系孩子；连同它们底下那几样判定：活血统、准入闸、驻留状态，
// 以及物化那道栅栏和分支失败的汇总。

package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ---- 活血统 ----

// 第一项始终是传进来的那个身份，哪怕它已经不在注册表里。
func TestLiveLineageStartsAtTheGivenIdentityEvenWhenItIsStale(t *testing.T) {
	fixture := newContinuation(t)
	stale := agentAtDepth(t, "stale", 0)

	lineage := fixture.manager.liveLineage(stale)
	if len(lineage) != 1 || lineage[0] != stale {
		t.Fatalf("该恰好是那个陈的身份自己，实际 %#v", lineage)
	}
}

// 往上一直爬到第一个断口为止：一个已经离开注册表的祖先切断这条链。
func TestLiveLineageClimbsUntilTheFirstGap(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	middle := fixture.spawnParent(t, "middle", "root")
	leaf := fixture.spawnParent(t, "leaf", "middle")

	lineage := fixture.manager.liveLineage(leaf)
	if len(lineage) != 3 || lineage[0] != leaf || lineage[1] != middle || lineage[2] != root {
		t.Fatalf("该是自下而上那三层，实际 %#v", lineage)
	}
	if !containsAgent(lineage, root) {
		t.Fatal("那条血统里该找得到根")
	}

	fixture.retire(t, "middle")
	cut := fixture.manager.liveLineage(leaf)
	if len(cut) != 1 || cut[0] != leaf {
		t.Fatalf("中间那层没了就该断在这儿，实际 %#v", cut)
	}
	if containsAgent(cut, root) {
		t.Fatal("断口之上那些祖先不该还算在这条血统里")
	}
}

// 一条指回自己的血统不许把这个循环走下去。
func TestLiveLineageStopsOnACycle(t *testing.T) {
	fixture := newContinuation(t)
	self := fixture.spawnParent(t, "self", "self")

	if lineage := fixture.manager.liveLineage(self); len(lineage) != 1 {
		t.Fatalf("绕回自己该停下，实际 %#v", lineage)
	}
}

// ---- 准入闸 ----

func TestAssertAdmittingLetsEverythingThroughWhenNothingIsClosing(t *testing.T) {
	fixture := newContinuation(t)
	if err := fixture.manager.assertAdmitting(fixture.spawnParent(t, "parent", "")); err != nil {
		t.Fatalf("没有谁在关时该放行，实际 %v", err)
	}
}

// 整台管理器排干时谁都不准入，而且交回来的根是「没有具体的根」。
func TestAssertAdmittingRefusesEveryoneWhileTheManagerDrains(t *testing.T) {
	fixture := newContinuation(t)
	subject := fixture.spawnParent(t, "parent", "")

	fixture.manager.mutex.Lock()
	fixture.manager.draining = true
	fixture.manager.mutex.Unlock()

	root, managerWide := fixture.manager.closingTeardown(subject)
	if !managerWide || root != nil {
		t.Fatalf("该报成整台管理器在排干，实际 root=%#v managerWide=%v", root, managerWide)
	}
	if code := codeOf(fixture.manager.assertAdmitting(subject)); code != CodeDraining {
		t.Fatalf("该报 %s，实际 %s", CodeDraining, code)
	}
}

// 带范围的闸认两样：这个身份被点名留在某个根的成员集里，或者那个根出现在它的血统里。
func TestAssertAdmittingRefusesASubjectInsideAClosingSubtree(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	descendant := fixture.spawnParent(t, "descendant", "root")
	stranger := fixture.spawnParent(t, "stranger", "")

	fixture.manager.mutex.Lock()
	fixture.manager.closingMembersLocked(root)[stranger] = struct{}{}
	fixture.manager.mutex.Unlock()

	// 血统里有那个根。
	if code := codeOf(fixture.manager.assertAdmitting(descendant)); code != CodeDraining {
		t.Fatalf("血统里有那个根该报 %s，实际 %s", CodeDraining, code)
	}
	// 被点名留住的成员——它自己的血统里并没有那个根。
	if code := codeOf(fixture.manager.assertAdmitting(stranger)); code != CodeDraining {
		t.Fatalf("被点名留住的成员该报 %s，实际 %s", CodeDraining, code)
	}
	// 那条带范围的说辞要点出是谁底下在排干。
	if err := fixture.manager.assertAdmitting(descendant); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("该点出那个根，实际 %v", err)
	}
	// 不相干的树照旧开着。
	if err := fixture.manager.assertAdmitting(fixture.spawnParent(t, "elsewhere", "")); err != nil {
		t.Fatalf("不相干的树该照旧准入，实际 %v", err)
	}
}

// ---- 驻留状态 ----

// bareActivation 造一份**没有守望在盯着**的活化，好把三种驻留状态单独摆出来看。
func bareActivation(t *testing.T, id string) (*activation, *fakeAgent) {
	t.Helper()
	built := agentAtDepth(t, id, 1)
	built.status = agent.StatusIdle
	return &activation{
		childID:       session.SessionID(id),
		handle:        agent.Handle{Agent: built},
		ancestry:      map[agent.Agent]struct{}{},
		ownedChildren: map[session.SessionID]struct{}{},
		accepted:      map[llm.MessageID]struct{}{},
		poke:          make(chan struct{}),
	}, built
}

func TestStateOfLockedReadsRunningFromTheAgentItself(t *testing.T) {
	fixture := newContinuation(t)
	target, built := bareActivation(t, "child")
	built.status = agent.StatusRunning

	fixture.manager.mutex.Lock()
	defer fixture.manager.mutex.Unlock()
	if state := fixture.manager.stateOfLocked(target); state != stateRunning {
		t.Fatalf("该是 %v，实际 %v", stateRunning, state)
	}
}

// 一条已经被接受、还没被认领的消息也算在跑：那个空档里 agent 自己仍旧报 idle。
func TestStateOfLockedCountsAnAcceptedMessageAsRunning(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")
	target.accepted["m1"] = struct{}{}

	fixture.manager.mutex.Lock()
	defer fixture.manager.mutex.Unlock()
	if state := fixture.manager.stateOfLocked(target); state != stateRunning {
		t.Fatalf("该是 %v，实际 %v", stateRunning, state)
	}
}

// 自己安静了但名下还有孩子，那是在等，不是结清了。
func TestStateOfLockedWaitsOnOwnedChildren(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")
	target.ownedChildren["grandchild"] = struct{}{}

	fixture.manager.mutex.Lock()
	defer fixture.manager.mutex.Unlock()
	if state := fixture.manager.stateOfLocked(target); state != stateWaiting {
		t.Fatalf("该是 %v，实际 %v", stateWaiting, state)
	}
}

func TestStateOfLockedSettlesWhenItIsQuietAndChildless(t *testing.T) {
	fixture := newContinuation(t)
	target, _ := bareActivation(t, "child")

	fixture.manager.mutex.Lock()
	defer fixture.manager.mutex.Unlock()
	if state := fixture.manager.stateOfLocked(target); state != stateSettled {
		t.Fatalf("该是 %v，实际 %v", stateSettled, state)
	}
}

// ---- 物化那道栅栏 ----

func TestWaitMaterializationsPassesWhenEveryOneHasSettled(t *testing.T) {
	settled := make(chan struct{})
	close(settled)
	if err := waitMaterializations(context.Background(), []*materialization{{settled: settled}}); err != nil {
		t.Fatalf("都落定了该放行，实际 %v", err)
	}
	if err := waitMaterializations(context.Background(), nil); err != nil {
		t.Fatalf("一个都没有该放行，实际 %v", err)
	}
}

// 等的人被取消时报 [CodeCancelled]，而不是干等着。
func TestWaitMaterializationsStopsWhenTheWaiterIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitMaterializations(ctx, []*materialization{{settled: make(chan struct{})}})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// ---- 整台管理器排干 ----

// 排干之后准入永久关掉：后来的开工一律报 [CodeDraining]。
func TestDrainClosesAdmissionForGood(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")

	if err := fixture.manager.Drain(t.Context()); err != nil {
		t.Fatalf("排干失败：%v", err)
	}
	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "child",
		Request:  StartRequest{Parent: parent},
	})
	if codeOf(err) != CodeDraining {
		t.Fatalf("该报 %s，实际 %v", CodeDraining, err)
	}
}

// 只有**没被谁拥有**的活化算根，所以排干一次就把整片森林递归走完。
func TestDrainReleasesTheWholeForest(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	fixture.startChild(t, fixture.factory.child("middle"), "leaf", "干活")

	if err := fixture.manager.Drain(t.Context()); err != nil {
		t.Fatalf("排干失败：%v", err)
	}
	for _, id := range []session.SessionID{"middle", "leaf"} {
		if fixture.resident(id) {
			t.Fatalf("排干之后 %q 不该还驻留着", id)
		}
		if _, still := fixture.agents.Get(id); still {
			t.Fatalf("排干之后 %q 不该还在注册表里", id)
		}
	}
}

// 兄弟分支各排各的：每一条都被试到，那份汇总的失败等全部有结局之后才报。
func TestDrainReportsEveryBranchFailure(t *testing.T) {
	fixture := newContinuation(t)
	broken := errors.New("拆不干净")
	fixture.factory.disposeErr = broken
	first := fixture.spawnParent(t, "first", "")
	second := fixture.spawnParent(t, "second", "")
	fixture.startChild(t, first, "a", "干活")
	fixture.startChild(t, second, "b", "干活")

	err := fixture.manager.Drain(t.Context())
	if codeOf(err) != CodeActivationTeardownFailed {
		t.Fatalf("该报 %s，实际 %v", CodeActivationTeardownFailed, err)
	}
	if !errors.Is(err, broken) {
		t.Fatalf("每一条分支那个原因该还认得出来，实际 %v", err)
	}
	// 失败也要把活化摘掉：留着会把整条祖先链永远钉在 waiting 上。
	for _, id := range []session.SessionID{"a", "b"} {
		if fixture.resident(id) {
			t.Fatalf("拆失败之后 %q 也不该还驻留着", id)
		}
	}
}

// 一个停了却没真正静下来的孩子：那次刷盘**不做**，因为一个还在跑的回合会接着追加
// 这次刷盘覆盖不到的事件；而这条失败照旧汇总上去，句柄也照旧摘掉。
func TestDrainReportsAChildThatNeverComesToRest(t *testing.T) {
	fixture := newContinuation(t)
	broken := errors.New("停不干净")
	fixture.factory.idleErr = broken
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	err := fixture.manager.Drain(t.Context())
	if codeOf(err) != CodeActivationTeardownFailed {
		t.Fatalf("该报 %s，实际 %v", CodeActivationTeardownFailed, err)
	}
	if !errors.Is(err, broken) {
		t.Fatalf("那个原因该还认得出来，实际 %v", err)
	}
	// 只有这一处边界出事，所以报的是那条失败本身，不是那句「在几处边界上失败了」。
	if saysAnywhere(err, "处边界") {
		t.Fatalf("只有一处出事不该汇总成多处，实际 %v", err)
	}
	if fixture.resident("child") {
		t.Fatal("停不干净也不该还驻留着")
	}
}

// 一个孩子拆不掉时，那次失败要挂到它父亲这一层上，并且和这个父亲**自己**那次
// 句柄处置失败一起汇总——两处边界都出事时报的是那句「在几处边界上失败了」。
func TestDrainSummarisesANestedTeardownFailure(t *testing.T) {
	fixture := newContinuation(t)
	broken := errors.New("拆不干净")
	fixture.factory.disposeErr = broken
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	fixture.startChild(t, fixture.factory.child("middle"), "leaf", "干活")

	err := fixture.manager.Drain(t.Context())
	if codeOf(err) != CodeActivationTeardownFailed {
		t.Fatalf("该报 %s，实际 %v", CodeActivationTeardownFailed, err)
	}
	if !saysAnywhere(err, "2 处边界") {
		t.Fatalf("孩子和句柄两处都该数进去，实际 %q", err.Error())
	}
	if !errors.Is(err, broken) {
		t.Fatalf("那个原因该还认得出来，实际 %v", err)
	}
	// 失败也要把两层都摘掉：留着会把整条祖先链永远钉在 waiting 上。
	for _, id := range []session.SessionID{"middle", "leaf"} {
		if fixture.resident(id) {
			t.Fatalf("拆失败之后 %q 也不该还驻留着", id)
		}
	}
}

// 整台管理器排干同样停在物化那道栅栏上：那些拆解还没开出去，所以这一路必须报错，
// 而不是对着一片还没稳定下来的森林拍快照。
func TestDrainSurfacesACancelledMaterializationBarrier(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")

	// 一次**永远不落定**的在途物化，加一个已经取消的调用方。收尾必须把它摘掉，
	// 否则拥有它的作用域一处置就会永远等在这儿。
	tracked := &materialization{lineage: []agent.Agent{root}, settled: make(chan struct{})}
	fixture.manager.mutex.Lock()
	fixture.manager.materializations[tracked] = struct{}{}
	fixture.manager.mutex.Unlock()
	t.Cleanup(func() {
		fixture.manager.mutex.Lock()
		delete(fixture.manager.materializations, tracked)
		fixture.manager.mutex.Unlock()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if code := codeOf(fixture.manager.Drain(ctx)); code != CodeCancelled {
		t.Fatalf("该报 %s，实际 %s", CodeCancelled, code)
	}
}

// 一次**和这个根无关**的在途物化不进这个根的闸，也不必等它落定：带范围的排干
// 只管自己那棵树。
func TestDrainDescendantsIgnoresAnUnrelatedInFlightMaterialization(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	stranger := fixture.spawnParent(t, "stranger", "")

	// 血统里只有那个陌生人，而且**永远不落定**：这次排干要是等了它就会挂死。
	tracked := &materialization{lineage: []agent.Agent{stranger}, settled: make(chan struct{})}
	fixture.manager.mutex.Lock()
	fixture.manager.materializations[tracked] = struct{}{}
	fixture.manager.mutex.Unlock()
	t.Cleanup(func() {
		fixture.manager.mutex.Lock()
		delete(fixture.manager.materializations, tracked)
		fixture.manager.mutex.Unlock()
	})

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{root}); err != nil {
		t.Fatalf("按根排干失败：%v", err)
	}
	if err := fixture.manager.assertAdmitting(stranger); err != nil {
		t.Fatalf("不相干那条血统不该进闸，实际 %v", err)
	}
}

// ---- 按确切的宿主根排干后代 ----

// 一个 nil 的、或者身份已经陈旧的父点不动任何东西。
func TestDrainDescendantsIgnoresNilAndStaleParents(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{
		nil, agentAtDepth(t, "stale", 0),
	}); err != nil {
		t.Fatalf("点不动任何东西该是空操作，实际 %v", err)
	}
	if !fixture.resident("child") {
		t.Fatal("不该动到那个孩子")
	}
}

// 只要**严格**后代：那个根自己的句柄仍旧归它的宿主负责。
func TestDrainDescendantsLeavesTheRootItselfResident(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	middle := fixture.factory.child("middle")
	fixture.startChild(t, middle, "leaf", "干活")

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{middle}); err != nil {
		t.Fatalf("按根排干失败：%v", err)
	}
	if fixture.resident("leaf") {
		t.Fatal("那个后代该被放掉")
	}
	if !fixture.resident("middle") {
		t.Fatal("那个根自己该还驻留着")
	}
}

// 那道闸只关这棵树：不相干的树照旧准入，而且不相干的孩子一个都没动。
func TestDrainDescendantsClosesAdmissionUnderThatRootOnly(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	elsewhere := fixture.spawnParent(t, "elsewhere", "")
	fixture.startChild(t, root, "middle", "带一层")
	middle := fixture.factory.child("middle")
	fixture.startChild(t, middle, "leaf", "干活")
	fixture.startChild(t, elsewhere, "other", "干活")

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{middle}); err != nil {
		t.Fatalf("按根排干失败：%v", err)
	}
	_, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "again",
		Request:  StartRequest{Parent: middle},
	})
	if codeOf(err) != CodeDraining {
		t.Fatalf("这棵树底下该报 %s，实际 %v", CodeDraining, err)
	}
	if !fixture.resident("other") {
		t.Fatal("不相干的那棵树不该被动到")
	}
	if _, err := fixture.manager.Followup(
		t.Context(), elsewhere, "other", textContent("再来"), FollowupOptions{},
	); err != nil {
		t.Fatalf("不相干的那棵树该照旧准入，实际 %v", err)
	}
}

// 名下还带着后代的目标不算「目标根」：那一层的句柄由它自己那次孩子优先的释放
// 带走，再对它单开一次处置就是把同一次拆解做两遍。
func TestDrainDescendantsSkipsTargetsOwnedByAnotherTarget(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	middle := fixture.factory.child("middle")
	fixture.startChild(t, middle, "leaf", "再带一层")
	leaf := fixture.factory.child("leaf")
	fixture.startChild(t, leaf, "grand", "干活")

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{middle}); err != nil {
		t.Fatalf("按根排干失败：%v", err)
	}
	for _, id := range []session.SessionID{"leaf", "grand"} {
		if fixture.resident(id) {
			t.Fatalf("%q 该被放掉", id)
		}
	}
	if !fixture.resident("middle") {
		t.Fatal("那个根自己该还驻留着")
	}
}

// 一次在路上的物化，只要它那条血统上有这个根，那条血统整条都进这个根的闸。
func TestDrainDescendantsClosesAdmissionAlongAnInFlightMaterialization(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	// 一个和这个根没有血缘的父：它只可能经那次在途物化的血统进闸，所以这条断言
	// 认的确实是这一段。
	stranger := fixture.spawnParent(t, "stranger", "")

	// 摆一次**已经落定**的在途物化：那道栅栏于是当场就过，不必去抢真实的窗口。
	tracked := &materialization{lineage: []agent.Agent{stranger, root}, settled: make(chan struct{})}
	close(tracked.settled)
	fixture.manager.mutex.Lock()
	fixture.manager.materializations[tracked] = struct{}{}
	fixture.manager.mutex.Unlock()

	if err := fixture.manager.DrainDescendants(t.Context(), []agent.Agent{root}); err != nil {
		t.Fatalf("按根排干失败：%v", err)
	}
	if code := codeOf(fixture.manager.assertAdmitting(stranger)); code != CodeDraining {
		t.Fatalf("那条在途血统上的每一位都该进闸，实际 %s", code)
	}
}

// 在物化那道栅栏上被取消，这次排干就把它报出来：那几次拆解已经开出去了，只是
// 调用方不再等它们。
func TestDrainDescendantsSurfacesACancelledMaterializationBarrier(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")

	// 一次**永远不落定**的在途物化，加一个已经取消的调用方：这一路只可能从栅栏
	// 那条边出去。收尾必须把它摘掉，否则拥有它的作用域一处置就会永远等在这儿。
	tracked := &materialization{lineage: []agent.Agent{root}, settled: make(chan struct{})}
	fixture.manager.mutex.Lock()
	fixture.manager.materializations[tracked] = struct{}{}
	fixture.manager.mutex.Unlock()
	t.Cleanup(func() {
		fixture.manager.mutex.Lock()
		delete(fixture.manager.materializations, tracked)
		fixture.manager.mutex.Unlock()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if code := codeOf(fixture.manager.DrainDescendants(ctx, []agent.Agent{root})); code != CodeCancelled {
		t.Fatalf("该报 %s，实际 %s", CodeCancelled, code)
	}
}

// ---- 点名放掉直系孩子 ----

func TestDrainChildrenNeedsTheExactLiveParent(t *testing.T) {
	fixture := newContinuation(t)
	if err := fixture.manager.DrainChildren(t.Context(), nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有父该被拒，实际 %v", err)
	}
	err := fixture.manager.DrainChildren(t.Context(), agentAtDepth(t, "stale", 0), nil)
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("陈的父该报 %s，实际 %v", CodeUnauthorized, err)
	}
}

// 隔了一层的后代不是直系孩子，点名点不动。
func TestDrainChildrenRefusesAnythingThatIsNotADirectChild(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	fixture.startChild(t, fixture.factory.child("middle"), "leaf", "干活")

	err := fixture.manager.DrainChildren(t.Context(), root, []session.SessionID{"leaf"})
	if codeOf(err) != CodeUnauthorized {
		t.Fatalf("该报 %s，实际 %v", CodeUnauthorized, err)
	}
	if !fixture.resident("leaf") {
		t.Fatal("拒掉的点名不该放掉任何东西")
	}
}

// 重复的 id 只算一次，没驻留的 id 直接跳过——都不算失败。
func TestDrainChildrenSkipsDuplicatesAndAbsentIDs(t *testing.T) {
	fixture := newContinuation(t)
	parent := fixture.spawnParent(t, "parent", "")
	fixture.startChild(t, parent, "child", "干活")

	if err := fixture.manager.DrainChildren(t.Context(), parent, []session.SessionID{
		"child", "child", "从来没有过",
	}); err != nil {
		t.Fatalf("点名放掉失败：%v", err)
	}
	if fixture.resident("child") {
		t.Fatal("点到的孩子该被放掉")
	}
	if len(fixture.factory.child("child").cancelled()) == 0 {
		t.Fatal("点名放掉该先取消那个孩子")
	}
}

// 名下的后代跟着同一条生命周期一起走；而这个父其余孩子的准入**不**关掉。
func TestDrainChildrenReleasesDescendantsAndKeepsAdmissionOpen(t *testing.T) {
	fixture := newContinuation(t)
	root := fixture.spawnParent(t, "root", "")
	fixture.startChild(t, root, "middle", "带一层")
	fixture.startChild(t, fixture.factory.child("middle"), "leaf", "干活")

	if err := fixture.manager.DrainChildren(t.Context(), root, []session.SessionID{"middle"}); err != nil {
		t.Fatalf("点名放掉失败：%v", err)
	}
	for _, id := range []session.SessionID{"middle", "leaf"} {
		if fixture.resident(id) {
			t.Fatalf("%q 该跟着一起被放掉", id)
		}
	}
	if _, err := fixture.manager.StartContinuable(t.Context(), ContinuableStartSpec{
		Provider: "spawn",
		Label:    "查一下",
		ChildID:  "again",
		Request:  StartRequest{Parent: root, Prompt: textContent("再来一个")},
	}); err != nil {
		t.Fatalf("点名放掉不该关掉这个父的准入，实际 %v", err)
	}
}
