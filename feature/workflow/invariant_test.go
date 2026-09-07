// 本文件验这个包自己那条运行期不变量：六条边围着一次运行凑成的那份账。
//
// 源: packages/workflow/workflow/src/invariant.ts
//
// 检查是**panic**着报的（见 [github.com/snight1983/ds-harness-go/invariants.Fail]），
// 所以每一条用例都用 mustFail／mustNotFail 把那次 panic 接住再断言。这条接缝在本仓库
// 没有产出方，正常代码路径压根走不到它，于是用例直接往那份检查上发捏造的边——白盒
// 手法，同包测试拿得到。

package workflow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
)

// newRegistry 造一条全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// armed 给一个发射器装上这份检查，并把它那份状态交出来直接发边。
func armed(t *testing.T, lifecycle *Lifecycle) *lifecycleInvariant {
	t.Helper()
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), lifecycle)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}
	t.Cleanup(dispose)
	check := lifecycle.check.Load()
	if check == nil {
		t.Fatal("装完之后该留下一份检查")
	}
	return check
}

// mustFail 断言 act 报了一次违例，并把那句话交回来。
func mustFail(t *testing.T, act func()) string {
	t.Helper()
	var reported string
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("该报一次违例")
			}
			reported = fmt.Sprint(recovered)
		}()
		act()
	}()
	return reported
}

// mustNotFail 断言 act 一声不响。
func mustNotFail(t *testing.T, act func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("这条路不该报违例，实际 %v", recovered)
		}
	}()
	act()
}

// ---- 装与卸 ----

func TestRegisterInvariantsNeedsARegistryAndALifecycle(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	if _, err := RegisterInvariants(t.Context(), nil, lifecycle); err == nil {
		t.Error("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), nil); err == nil {
		t.Error("没有生命周期发射器该装不上")
	}
}

// 注销之后那份检查必须停下来，否则它会继续在别人的派发路径上抛。
func TestRegisterInvariantsStopsCheckingAfterRelease(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t), lifecycle)
	if err != nil {
		t.Fatalf("装检查失败：%v", err)
	}
	dispose()

	if lifecycle.check.Load() != nil {
		t.Fatal("注销之后不该再留着那份检查")
	}
	// 一条本来必定违例的边这时候什么都不该发生。
	mustNotFail(t, func() { lifecycle.EmitLog(startedRun(), "凭空", nil) })
}

// 那份检查有一个专属的调用点，跑在兜底观察者**之前**，所以违例不会被吞成一行日志。
func TestTheCheckWatchesRealDispatches(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	armed(t, lifecycle)
	reported := mustFail(t, func() { lifecycle.EmitEnd(startedRun(), ResultInfo{}, nil) })
	if !strings.Contains(reported, "no matching workflow/start") {
		t.Fatalf("该报配不上任何 start，实际 %q", reported)
	}
}

// ---- 正常路径 ----

// 一次身份齐全、派出与结清配好、干净收尾的运行一声不响。这是下面那些用例的地基。
func TestTheCheckIsQuietOnAWellFormedRun(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	child := AgentInfo{Seq: 1, Label: "第一轮", ChildID: "child-1"}

	mustNotFail(t, func() {
		check.runStarted(run)
		check.observed(run)
		check.agentStarted(run, child)
		check.agentEnded(run, AgentEndInfo{AgentInfo: child, Outcome: AgentCompleted})
		check.runEnded(run, ResultInfo{StopReason: StopCompleted, AgentsStarted: 1})
	})
	// 结清之后那个运行 id 就腾出来了，可以再开一次。
	mustNotFail(t, func() { check.runStarted(run) })
}

// ---- 一、运行的身份 ----

func TestStartRejectsAnIncompleteIdentity(t *testing.T) {
	for name, run := range map[string]RunInfo{
		"缺运行 id": {Meta: Meta{Name: "整理", Description: "说明"}},
		"缺名字":    {ID: "run-1", Meta: Meta{Description: "说明"}},
		"缺说明":    {ID: "run-1", Meta: Meta{Name: "整理"}},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			reported := mustFail(t, func() { check.runStarted(run) })
			if !strings.Contains(reported, "non-empty") {
				t.Fatalf("该报三样都不能为空，实际 %q", reported)
			}
		})
	}
}

func TestStartRejectsARepeatedRunID(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	check.runStarted(startedRun())
	reported := mustFail(t, func() { check.runStarted(startedRun()) })
	if !strings.Contains(reported, "repeated run id") {
		t.Fatalf("该报运行 id 重开，实际 %q", reported)
	}
}

// 除 start 以外每一条边都得配得上一条发过的 start。
func TestEveryOtherEdgeNeedsAKnownRun(t *testing.T) {
	unknown := RunInfo{ID: "凭空", Meta: Meta{Name: "整理", Description: "说明"}}
	child := AgentInfo{Seq: 1, ChildID: "child-1"}

	for name, emit := range map[string]func(*lifecycleInvariant){
		"phase 和 log": func(c *lifecycleInvariant) { c.observed(unknown) },
		"agent-start": func(c *lifecycleInvariant) { c.agentStarted(unknown, child) },
		"agent-end": func(c *lifecycleInvariant) {
			c.agentEnded(unknown, AgentEndInfo{AgentInfo: child, Outcome: AgentCompleted})
		},
		"end": func(c *lifecycleInvariant) { c.runEnded(unknown, ResultInfo{StopReason: StopCompleted}) },
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			reported := mustFail(t, func() { emit(check) })
			if !strings.Contains(reported, "no matching workflow/start") {
				t.Fatalf("该报配不上任何 start，实际 %q", reported)
			}
		})
	}
}

// 一次运行说着说着换了身份——观察方按 id 聚合时它会裂成两次。
func TestAnEdgeCannotDivergeFromTheStartMeta(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	check.runStarted(startedRun())

	renamed := startedRun()
	renamed.Meta.Name = "改了个名"
	reported := mustFail(t, func() { check.observed(renamed) })
	if !strings.Contains(reported, "meta diverges") {
		t.Fatalf("该报身份中途分岔，实际 %q", reported)
	}
}

// 阶段那一格也算身份的一部分：它排进同一份字节里。
func TestMetaComparisonCoversThePhases(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	check.runStarted(startedRun())

	rephased := startedRun()
	rephased.Meta.Phases = []Phase{{Title: "盘点"}}
	reported := mustFail(t, func() { check.observed(rephased) })
	if !strings.Contains(reported, "meta diverges") {
		t.Fatalf("阶段变了也算身份分岔，实际 %q", reported)
	}
}

// ---- 二、子 agent 调用的配对 ----

func TestAgentStartRejectsABadIdentity(t *testing.T) {
	for name, child := range map[string]AgentInfo{
		"序号从 0 起": {Seq: 0, ChildID: "child-1"},
		"序号是负的":   {Seq: -1, ChildID: "child-1"},
		"缺孩子 id":  {Seq: 1},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			check.runStarted(startedRun())
			reported := mustFail(t, func() { check.agentStarted(startedRun(), child) })
			if !strings.Contains(reported, "seq must be positive") {
				t.Fatalf("该报序号和孩子 id 的规矩，实际 %q", reported)
			}
		})
	}
}

func TestAgentStartRejectsARepeatedSeq(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	check.runStarted(run)
	check.agentStarted(run, AgentInfo{Seq: 1, ChildID: "child-1"})

	reported := mustFail(t, func() {
		check.agentStarted(run, AgentInfo{Seq: 1, ChildID: "child-2"})
	})
	if !strings.Contains(reported, "repeated seq") {
		t.Fatalf("该报序号重号，实际 %q", reported)
	}
}

func TestAgentStartRejectsASeqReusedAfterItsEnd(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	child := AgentInfo{Seq: 1, ChildID: "child-1"}
	check.runStarted(run)
	check.agentStarted(run, child)
	check.agentEnded(run, AgentEndInfo{AgentInfo: child, Outcome: AgentCompleted})

	reported := mustFail(t, func() {
		check.agentStarted(run, AgentInfo{Seq: 1, ChildID: "child-2"})
	})
	if !strings.Contains(reported, "repeated seq") {
		t.Fatalf("该报已经结清的序号被重用，实际 %q", reported)
	}
}

func TestAgentEndNeedsAMatchingStart(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	check.runStarted(run)

	reported := mustFail(t, func() {
		check.agentEnded(run, AgentEndInfo{
			AgentInfo: AgentInfo{Seq: 7, ChildID: "child-7"},
			Outcome:   AgentCompleted,
		})
	})
	if !strings.Contains(reported, "no matching start for seq") {
		t.Fatalf("该报配不上任何 agent-start，实际 %q", reported)
	}
}

// 两边说的标签、阶段和孩子 id 必须逐字一致。
func TestAgentEndRejectsADivergentIdentity(t *testing.T) {
	start := AgentInfo{Seq: 1, Label: "第一轮", Phase: "盘点", ChildID: "child-1"}
	for name, end := range map[string]AgentInfo{
		"标签对不上":     {Seq: 1, Label: "别的", Phase: "盘点", ChildID: "child-1"},
		"阶段对不上":     {Seq: 1, Label: "第一轮", Phase: "收尾", ChildID: "child-1"},
		"孩子 id 对不上": {Seq: 1, Label: "第一轮", Phase: "盘点", ChildID: "别人"},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			run := startedRun()
			check.runStarted(run)
			check.agentStarted(run, start)

			reported := mustFail(t, func() {
				check.agentEnded(run, AgentEndInfo{AgentInfo: end, Outcome: AgentCompleted})
			})
			if !strings.Contains(reported, "identity diverges") {
				t.Fatalf("该报身份对不上，实际 %q", reported)
			}
		})
	}
}

func TestAgentEndRejectsAnUnknownOutcome(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	child := AgentInfo{Seq: 1, ChildID: "child-1"}
	check.runStarted(run)
	check.agentStarted(run, child)

	reported := mustFail(t, func() {
		check.agentEnded(run, AgentEndInfo{AgentInfo: child, Outcome: "未来的变体"})
	})
	if !strings.Contains(reported, "unknown outcome") {
		t.Fatalf("该报说不出名字的结局，实际 %q", reported)
	}
	// 三个已知值都过。
	for _, outcome := range []AgentOutcome{AgentCompleted, AgentFailed, AgentCancelled} {
		fresh := armed(t, mustLifecycle(t))
		fresh.runStarted(run)
		fresh.agentStarted(run, child)
		mustNotFail(t, func() {
			fresh.agentEnded(run, AgentEndInfo{AgentInfo: child, Outcome: outcome})
		})
	}
}

// ---- 三、收尾时不许有挂着的调用 ----

// 引擎在被强杀那条路上偷懒不补一条 agent-end，界面上就会留下一个永远转圈的孩子。
func TestEndRejectsRunsWithOpenAgentCalls(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	check.runStarted(run)
	check.agentStarted(run, AgentInfo{Seq: 1, ChildID: "child-1"})

	reported := mustFail(t, func() {
		check.runEnded(run, ResultInfo{StopReason: StopCancelled, Error: "停了", AgentsStarted: 1})
	})
	if !strings.Contains(reported, "without workflow/agent-end") {
		t.Fatalf("该报还挂着孩子，实际 %q", reported)
	}
}

// ---- 四、计数说得通 ----

func TestEndRejectsAnUndercountOfStartedAgents(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	child := AgentInfo{Seq: 1, ChildID: "child-1"}
	check.runStarted(run)
	check.agentStarted(run, child)
	check.agentEnded(run, AgentEndInfo{AgentInfo: child, Outcome: AgentCompleted})

	reported := mustFail(t, func() {
		check.runEnded(run, ResultInfo{StopReason: StopCompleted, AgentsStarted: 0})
	})
	if !strings.Contains(reported, "must cover every observed agent start") {
		t.Fatalf("该报计数对不上，实际 %q", reported)
	}
}

// 报的数可以**大于**看见的：还排着队等并发槽的调用也算数，而那些调用从来不发边。
func TestEndAllowsMoreStartedAgentsThanObserved(t *testing.T) {
	check := armed(t, mustLifecycle(t))
	run := startedRun()
	check.runStarted(run)
	mustNotFail(t, func() {
		check.runEnded(run, ResultInfo{StopReason: StopCompleted, AgentsStarted: 5})
	})
}

// ---- 五、失败描述的有无跟着停止原因走 ----

func TestEndTiesTheErrorToTheStopReason(t *testing.T) {
	for name, result := range map[string]ResultInfo{
		"完成了却还带着失败描述": {StopReason: StopCompleted, Error: "塌了"},
		"出错了却说不出为什么":  {StopReason: StopError},
		"被取消了却说不出为什么": {StopReason: StopCancelled},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			run := startedRun()
			check.runStarted(run)
			reported := mustFail(t, func() { check.runEnded(run, result) })
			if !strings.Contains(reported, "error must be absent exactly for completed runs") {
				t.Fatalf("该报失败描述和停止原因对不上，实际 %q", reported)
			}
		})
	}

	for name, result := range map[string]ResultInfo{
		"干净收尾":   {StopReason: StopCompleted},
		"说得出为什么": {StopReason: StopError, Error: "塌了"},
	} {
		t.Run(name, func(t *testing.T) {
			check := armed(t, mustLifecycle(t))
			run := startedRun()
			check.runStarted(run)
			mustNotFail(t, func() { check.runEnded(run, result) })
		})
	}
}

// ---- 排不出去的 meta ----

// metaBytes 有一条排不出去时的退路。[Meta] 每个字段都排得出去，所以那一支
// 在生产路径上走不到；这里只确认它交出来的东西仍旧是可比对的。
func TestMetaBytesIsStableForEqualMetas(t *testing.T) {
	left := Meta{Name: "整理", Description: "说明", Phases: []Phase{{Title: "盘点"}}}
	right := Meta{Name: "整理", Description: "说明", Phases: []Phase{{Title: "盘点"}}}
	if metaBytes(left) != metaBytes(right) {
		t.Fatal("两份一样的 meta 该排出同一串字节")
	}
	if metaBytes(left) == metaBytes(Meta{Name: "整理", Description: "说明"}) {
		t.Fatal("阶段不同的 meta 不该排出同一串字节")
	}
}

// mustLifecycle 造一个发射器，只为拿它身上那个装检查的位置。
func mustLifecycle(t *testing.T) *Lifecycle {
	t.Helper()
	lifecycle, _ := newLifecycle(t)
	return lifecycle
}
