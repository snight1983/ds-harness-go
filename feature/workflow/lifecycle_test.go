// 本文件的作用：那六条边的测试——登记要一个非空观察者、按作用域分层派发、
// 兜住观察者的 panic，以及每一条边都把自己那份负载原样递出去。

package workflow

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 登记 ----

// 六个登记方法都拒绝 nil：一个登记进去的 nil 观察者要到派发那一刻才炸，
// 那时候已经没人说得清是谁登记的。
func TestOnEveryEdgeRejectsANilObserver(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	owner := rootScope(t)
	ctx := context.Background()

	register := map[string]func() error{
		"OnStart":      func() error { _, err := lifecycle.OnStart(ctx, owner, nil); return err },
		"OnPhase":      func() error { _, err := lifecycle.OnPhase(ctx, owner, nil); return err },
		"OnLog":        func() error { _, err := lifecycle.OnLog(ctx, owner, nil); return err },
		"OnAgentStart": func() error { _, err := lifecycle.OnAgentStart(ctx, owner, nil); return err },
		"OnAgentEnd":   func() error { _, err := lifecycle.OnAgentEnd(ctx, owner, nil); return err },
		"OnEnd":        func() error { _, err := lifecycle.OnEnd(ctx, owner, nil); return err },
	}
	for name, attempt := range register {
		t.Run(name, func(t *testing.T) {
			if err := attempt(); err == nil {
				t.Fatal("登记一个 nil 观察者该当场报错")
			}
		})
	}
}

// 撤销之后那个观察者就不该再被叫到。
func TestDisposingARegistrationStopsTheObserver(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	ctx := context.Background()

	var seen int
	dispose, err := lifecycle.OnLog(ctx, rootScope(t), func(RunInfo, string, agent.Agent) { seen++ })
	if err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	lifecycle.EmitLog(startedRun(), "一句", nil)
	if err := dispose(ctx); err != nil {
		t.Fatalf("撤销登记失败：%v", err)
	}
	lifecycle.EmitLog(startedRun(), "又一句", nil)

	if seen != 1 {
		t.Fatalf("撤销之后不该再被叫到，实际叫了 %d 次", seen)
	}
}

// ---- 作用域分层 ----

// 挂在有身份作用域上的观察者只看得见由那个作用域（或它的子孙）发起的运行，
// 挂在无身份作用域上的看得见每一次。
func TestDispatchHonoursScopeLayering(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	ctx := context.Background()

	parent := newFakeAgent(t, "parent", nil)
	sibling := newFakeAgent(t, "sibling", nil)
	child := newFakeAgent(t, "child", parent.Scope().Key())

	var global, scoped []string
	if _, err := lifecycle.OnStart(ctx, rootScope(t), func(_ RunInfo, carrier agent.Agent) {
		global = append(global, carrierName(carrier))
	}); err != nil {
		t.Fatalf("登记全局观察者失败：%v", err)
	}
	if _, err := lifecycle.OnStart(ctx, parent.Scope(), func(_ RunInfo, carrier agent.Agent) {
		scoped = append(scoped, carrierName(carrier))
	}); err != nil {
		t.Fatalf("登记带作用域的观察者失败：%v", err)
	}

	for _, carrier := range []agent.Agent{parent, sibling, child, nil} {
		lifecycle.EmitStart(startedRun(), carrier)
	}

	if strings.Join(global, ",") != "parent,sibling,child,没有载体" {
		t.Fatalf("全局层该看见每一次派发，实际 %#v", global)
	}
	// 兄弟那次和没有载体那次都落在 parent 那条父链外面。
	if strings.Join(scoped, ",") != "parent,child" {
		t.Fatalf("带作用域的观察者只该看见自己那条链，实际 %#v", scoped)
	}
}

// 一个没有作用域的父解不出那把载体键，于是这条边退回只发全局层——解不出载体
// 不等于这条边发不出去。
func TestEdgesFallBackToTheGlobalLayerForAScopelessParent(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	var global int
	if _, err := lifecycle.OnStart(context.Background(), rootScope(t), func(RunInfo, agent.Agent) {
		global++
	}); err != nil {
		t.Fatalf("登记全局观察者失败：%v", err)
	}

	scopeless := &fakeAgent{id: "parent"}
	lifecycle.EmitStart(startedRun(), scopeless)
	if global != 1 {
		t.Fatalf("全局层那个观察者该收到这条边，实际 %d", global)
	}
}

// carrierName 把一个载体说成一个好断言的名字。
func carrierName(carrier agent.Agent) string {
	if carrier == nil {
		return "没有载体"
	}
	return string(carrier.ID())
}

// ---- 兜住异常 ----

// 一个 panic 的观察者既不该穿出去，也不该饿着排在它后面的同侪，六条边都一样。
func TestEveryEdgeContainsAPanickingObserver(t *testing.T) {
	run := startedRun()
	child := AgentInfo{Seq: 1, Label: "第一轮", ChildID: "child"}

	edges := map[string]struct {
		register func(*Lifecycle, context.Context, *scope.Scope, func()) error
		emit     func(*Lifecycle)
	}{
		"workflow/start": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnStart(ctx, owner, func(RunInfo, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) { l.EmitStart(run, nil) },
		},
		"workflow/phase": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnPhase(ctx, owner, func(RunInfo, string, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) { l.EmitPhase(run, "盘点", nil) },
		},
		"workflow/log": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnLog(ctx, owner, func(RunInfo, string, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) { l.EmitLog(run, "一句", nil) },
		},
		"workflow/agent-start": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnAgentStart(ctx, owner, func(RunInfo, AgentInfo, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) { l.EmitAgentStart(run, child, nil) },
		},
		"workflow/agent-end": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnAgentEnd(ctx, owner, func(RunInfo, AgentEndInfo, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) {
				l.EmitAgentEnd(run, AgentEndInfo{AgentInfo: child, Outcome: AgentCompleted}, nil)
			},
		},
		"workflow/end": {
			func(l *Lifecycle, ctx context.Context, owner *scope.Scope, hit func()) error {
				_, err := l.OnEnd(ctx, owner, func(RunInfo, ResultInfo, agent.Agent) { hit() })
				return err
			},
			func(l *Lifecycle) { l.EmitEnd(run, ResultInfo{StopReason: StopCompleted}, nil) },
		},
	}

	for name, edge := range edges {
		t.Run(name, func(t *testing.T) {
			lifecycle, warnings := newLifecycle(t)
			owner := rootScope(t)
			ctx := context.Background()

			var later int
			if err := edge.register(lifecycle, ctx, owner, func() { panic("炸了") }); err != nil {
				t.Fatalf("登记观察者失败：%v", err)
			}
			if err := edge.register(lifecycle, ctx, owner, func() { later++ }); err != nil {
				t.Fatalf("登记观察者失败：%v", err)
			}
			edge.emit(lifecycle)

			if later != 1 {
				t.Fatalf("排在 panic 后面的观察者该照常被叫到，实际叫了 %d 次", later)
			}
			logged := warnings()
			if len(logged) != 1 || !strings.Contains(logged[0], name) {
				t.Fatalf("该记下一条 %s 观察者的 panic，实际 %#v", name, logged)
			}
		})
	}
}

// 发射器自带那条告警路把 panic 写进 logger，logger 为 nil 时写进 slog 的默认处。
// 本包别处的用例一律把 warn 整个换掉，所以这两支只有这里走得到。
func TestLifecycleWarnsThroughItsOwnLogger(t *testing.T) {
	for name, given := range map[string]bool{"给了 logger": true, "没给 logger": false} {
		t.Run(name, func(t *testing.T) {
			var written strings.Builder
			handler := slog.NewTextHandler(&written, nil)

			var (
				lifecycle *Lifecycle
				err       error
			)
			if given {
				lifecycle, err = NewLifecycle(slog.New(handler))
			} else {
				previous := slog.Default()
				slog.SetDefault(slog.New(handler))
				t.Cleanup(func() { slog.SetDefault(previous) })
				lifecycle, err = NewLifecycle(nil)
			}
			if err != nil {
				t.Fatalf("造发射器失败：%v", err)
			}

			remove := lifecycle.layers.Global().start.Append(func(RunInfo, agent.Agent) { panic("炸了") })
			t.Cleanup(remove)
			lifecycle.EmitStart(startedRun(), nil)

			if !strings.Contains(written.String(), "炸了") {
				t.Fatalf("那次 panic 该被记下来，实际 %q", written.String())
			}
		})
	}
}

// warn 为 nil 时那条 panic 只是被丢掉，不该反过来把发射器自己弄崩。
func TestContainWithoutAWarnSinkStillSwallowsThePanic(t *testing.T) {
	lifecycle, err := NewLifecycle(nil)
	if err != nil {
		t.Fatalf("造发射器失败：%v", err)
	}
	lifecycle.warn = nil
	lifecycle.contain("test", func() { panic("炸了") })
}

// ---- 负载 ----

// 每一条边都把自己那份负载原样递给观察者，一个字段都不改。
func TestEachEdgeHandsItsPayloadThrough(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	ctx := context.Background()
	owner := rootScope(t)
	parent := newFakeAgent(t, "parent", nil)
	run := startedRun()

	var (
		phase, logged string
		started       AgentInfo
		ended         AgentEndInfo
		result        ResultInfo
		carriers      []string
	)
	if _, err := lifecycle.OnPhase(ctx, owner, func(_ RunInfo, title string, c agent.Agent) {
		phase = title
		carriers = append(carriers, carrierName(c))
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := lifecycle.OnLog(ctx, owner, func(_ RunInfo, message string, _ agent.Agent) {
		logged = message
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := lifecycle.OnAgentStart(ctx, owner, func(_ RunInfo, c AgentInfo, _ agent.Agent) {
		started = c
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := lifecycle.OnAgentEnd(ctx, owner, func(_ RunInfo, c AgentEndInfo, _ agent.Agent) {
		ended = c
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}
	if _, err := lifecycle.OnEnd(ctx, owner, func(_ RunInfo, r ResultInfo, _ agent.Agent) {
		result = r
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	child := AgentInfo{Seq: 3, Label: "收尾", Phase: "盘点", ChildID: sessionlog.SessionID("child")}
	lifecycle.EmitPhase(run, "盘点", parent)
	lifecycle.EmitLog(run, "看见 12 个包", parent)
	lifecycle.EmitAgentStart(run, child, parent)
	lifecycle.EmitAgentEnd(run, AgentEndInfo{AgentInfo: child, Outcome: AgentFailed}, parent)
	lifecycle.EmitEnd(run, ResultInfo{StopReason: StopError, Error: "塌了", AgentsStarted: 3}, parent)

	if phase != "盘点" || logged != "看见 12 个包" {
		t.Fatalf("阶段和日志该原样递出去，实际 %q / %q", phase, logged)
	}
	if started != child {
		t.Fatalf("派出那条边该原样递出去，实际 %#v", started)
	}
	if ended.AgentInfo != child || ended.Outcome != AgentFailed {
		t.Fatalf("结清那条边该原样递出去，实际 %#v", ended)
	}
	if result.StopReason != StopError || result.Error != "塌了" || result.AgentsStarted != 3 {
		t.Fatalf("结局该原样递出去，实际 %#v", result)
	}
	if len(carriers) != 1 || carriers[0] != "parent" {
		t.Fatalf("发起方该原样递给观察者，实际 %#v", carriers)
	}
}
