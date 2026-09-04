// 本文件的作用：把这台驱动钉在它那几条边上——公布、投提示词、等静止、读结果、
// 恰好一条描述符、取消在公布前后的两种下场、种进创建窗口的派发策略，以及处置
// 只报它自己那一件事。

package inprocessdriver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// answersWith 让每个造出来的孩子在提示词投进来的那一刻摆出「说了一句话然后正常
// 收尾」的日志并静下来。
//
// 挂在 onFollowup 而不是 onChild 上是有意的：这样那条日志确实是在提示词到达之后
// 才出现的，「先投提示词、再等静止」这条次序才真的被这些用例走过。
func answersWith(t *testing.T, answer string) func(*childAgent) {
	t.Helper()
	hook := completedTurn(t, answer)
	return func(child *childAgent) { child.onFollowup = hook }
}

// descriptorCount 数这个孩子日志上有几条子 agent 描述符。
func descriptorCount(child *childAgent) int {
	var found int
	for _, each := range child.session.Events() {
		if each.Type == subagent.EventDescriptor {
			found++
		}
	}
	return found
}

// enterStep 是那条瀑布最里面那一层「进」的决定。
func enterStep(context.Context) (agent.PreStepDecision, error) {
	return agent.EnterStep(nil), nil
}

func TestStartInProcessRunPublishesAndSettlesCompleted(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.factory.onChild = answersWith(t, "答案")

	run := fixture.start(t, t.Context(), fixture.request("干活"), RunOptions{})
	result, err := run.Result(t.Context())
	if err != nil {
		t.Fatalf("这次运行不该报基础设施故障：%v", err)
	}
	if result.StopReason != subagent.StopCompleted {
		t.Fatalf("正常收尾的孩子该算跑完了，实际 %q", result.StopReason)
	}
	if got := textOf(result.Output); got != "答案" {
		t.Fatalf("该读出孩子最后那段助手输出，实际 %q", got)
	}

	child := fixture.factory.only(t)
	delivered := child.delivered()
	if len(delivered) != 1 {
		t.Fatalf("该恰好投一次提示词，实际 %d 次", len(delivered))
	}
	if got := textOf(delivered[0].Content); got != "干活" {
		t.Fatalf("投下去的该是请求里那段提示词，实际 %q", got)
	}
	if run.ID() != child.ID() {
		t.Fatalf("运行 id 该就是孩子的会话 id：%q / %q", run.ID(), child.ID())
	}
	if run.LocalAgent() != agent.Agent(child) {
		t.Fatal("这次运行该交出它那个进程内的孩子")
	}

	// 反复读交出同一份结果。
	again, err := run.Result(t.Context())
	if err != nil || again.StopReason != result.StopReason {
		t.Fatalf("重复读该交出同一份结果：%+v / %v", again, err)
	}
}

func TestStartInProcessRunAppendsTheDescriptorOnceInsideTheFirstEnteredStep(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	// 孩子有意不静下来：这个用例要的是它公布之后、还没跑完的那个状态。
	fixture.start(t, t.Context(), fixture.request("干活"), RunOptions{})
	child := fixture.factory.only(t)

	if got := descriptorCount(child); got != 0 {
		t.Fatalf("一个步骤都还没进，日志上不该有描述符，实际 %d 条", got)
	}

	decision, err := fixture.agents.ResolvePreStep(t.Context(), agent.PreStep{Agent: child}, enterStep)
	if err != nil {
		t.Fatalf("解算前置步骤失败：%v", err)
	}
	if !decision.Enter {
		t.Fatal("这个步骤该进")
	}
	if got := descriptorCount(child); got != 1 {
		t.Fatalf("第一个进得去的步骤该追加恰好一条描述符，实际 %d 条", got)
	}

	// 后面的步骤不再写。
	if _, err := fixture.agents.ResolvePreStep(t.Context(), agent.PreStep{Agent: child}, enterStep); err != nil {
		t.Fatalf("解算前置步骤失败：%v", err)
	}
	if got := descriptorCount(child); got != 1 {
		t.Fatalf("描述符该只有一条，实际 %d 条", got)
	}

	var written subagent.DescriptorData
	for _, each := range child.session.Events() {
		if each.Type == subagent.EventDescriptor {
			if err := json.Unmarshal(each.Data, &written); err != nil {
				t.Fatalf("读描述符失败：%v", err)
			}
		}
	}
	if written != (subagent.DescriptorData{
		Version:  subagent.DescriptorVersion,
		Mode:     subagent.ModeOneShot,
		Provider: "spawn",
	}) {
		t.Fatalf("写下去的该是请求里那份解算好的描述符，实际 %+v", written)
	}
}

func TestStartInProcessRunSkipsTheDescriptorWhenTheStepIsRejected(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.start(t, t.Context(), fixture.request("干活"), RunOptions{})
	child := fixture.factory.only(t)

	decision, err := fixture.agents.ResolvePreStep(
		t.Context(),
		agent.PreStep{Agent: child},
		func(context.Context) (agent.PreStepDecision, error) { return agent.RejectStep(), nil },
	)
	if err != nil {
		t.Fatalf("解算前置步骤失败：%v", err)
	}
	if decision.Enter {
		t.Fatal("这个步骤该被拒")
	}
	if got := descriptorCount(child); got != 0 {
		t.Fatalf("一个从没跑过的孩子日志上不该有身份事件，实际 %d 条", got)
	}
}

func TestStartInProcessRunRejectsWhenCancelledBeforePublication(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	run, err := StartInProcessRun(ctx, fixture.services(), fixture.request("干活"), RunOptions{})
	if run != nil {
		t.Fatal("这一路没有运行交出去，调用方不必处置任何东西")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("该把 ctx 那条取消原因包出来，实际 %v", err)
	}
	if created := fixture.factory.created(); len(created) != 0 {
		t.Fatalf("取消赢在公布之前，一个孩子都不该造，实际 %d 个", len(created))
	}
}

func TestStartInProcessRunCancelsThePublishedChild(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())

	// 孩子有意不自己静下来：只有那次取消能把它停到静止。
	run := fixture.start(t, ctx, fixture.request("干活"), RunOptions{})
	cancel()

	result, err := run.Result(t.Context())
	if err != nil {
		t.Fatalf("一次取消不是基础设施故障：%v", err)
	}
	if result.StopReason != subagent.StopAborted {
		t.Fatalf("被父取消掉的运行该算中止，实际 %q", result.StopReason)
	}

	child := fixture.factory.only(t)
	cancels := child.cancelled()
	if len(cancels) == 0 {
		t.Fatal("该把孩子那个回合停掉")
	}
	if _, ok := cancels[0].(sessionlog.ParentCancel); !ok {
		t.Fatalf("取消的理由该是「父反悔了」，实际 %#v", cancels[0])
	}
}

func TestStartInProcessRunSeedsDelegatedPolicies(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	approval, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return nil, errors.New("这一包用不上") },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造审批服务失败：%v", err)
	}
	services := fixture.services()
	services.Approval = approval

	fixture.factory.onChild = answersWith(t, "答案")
	run, err := StartInProcessRun(t.Context(), services, fixture.request("干活"), RunOptions{})
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}
	t.Cleanup(func() { _ = run.Dispose(context.Background()) })
	if _, err := run.Result(t.Context()); err != nil {
		t.Fatalf("这次运行不该报基础设施故障：%v", err)
	}

	var seeded userapproval.PolicyData
	var found int
	for _, each := range fixture.factory.only(t).session.Events() {
		if each.Type != userapproval.EventPolicy {
			continue
		}
		found++
		if err := json.Unmarshal(each.Data, &seeded); err != nil {
			t.Fatalf("读策略事件失败：%v", err)
		}
	}
	if found != 1 {
		t.Fatalf("该恰好种一条派发策略，实际 %d 条", found)
	}
	if seeded.Policy != userapproval.PolicyNever {
		t.Fatalf("被派发的孩子该被钉成「谁都不问」，实际 %q", seeded.Policy)
	}
	if seeded.Source != userapproval.PolicySourceDelegation {
		t.Fatalf("这条的来源该是 delegation，实际 %q", seeded.Source)
	}
}

func TestStartInProcessRunRejectsAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)

	noParent := fixture.request("干活")
	noParent.Parent = nil

	for _, testCase := range []struct {
		name     string
		services Services
		request  subagent.ResolvedStartRequest
	}{
		{
			name:     "没有 agent 注册表",
			services: Services{Owner: fixture.owner},
			request:  fixture.request("干活"),
		},
		{
			name:     "没有主人作用域",
			services: Services{Agents: fixture.agents},
			request:  fixture.request("干活"),
		},
		{
			name:     "没有父 agent",
			services: fixture.services(),
			request:  noParent,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			run, err := StartInProcessRun(t.Context(), testCase.services, testCase.request, RunOptions{})
			if run != nil {
				t.Fatal("装配不成立时不该有运行交出去")
			}
			if !errors.Is(err, subagent.ErrInvalidRequest) {
				t.Fatalf("该报这条接缝的哨兵错误，实际 %v", err)
			}
		})
	}
}

func TestRunDisposeReportsOnlyTheHandleFailure(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.factory.disposeErr = errors.New("放不掉那份句柄")

	run, err := StartInProcessRun(t.Context(), fixture.services(), fixture.request("干活"), RunOptions{})
	if err != nil {
		t.Fatalf("开工失败：%v", err)
	}

	first := run.Dispose(t.Context())
	if first == nil || !strings.Contains(first.Error(), "放不掉那份句柄") {
		t.Fatalf("处置该报放不掉句柄这件事，实际 %v", first)
	}
	// 重复调等着第一遍的结论。
	if again := run.Dispose(t.Context()); again == nil || again.Error() != first.Error() {
		t.Fatalf("重复处置该交出同一个结论，实际 %v", again)
	}
	// 处置把孩子停到静止，结果那一路也已经结清。
	if _, err := run.Result(t.Context()); err != nil {
		t.Fatalf("处置之后结果那一路该已经结清：%v", err)
	}
}

func TestToStopReasonMapsTurnEndings(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		reason sessionlog.TurnEndReason
		want   subagent.StopReason
	}{
		{name: "completed", reason: sessionlog.CompletedTurnEnd{}, want: subagent.StopCompleted},
		{name: "max tokens", reason: sessionlog.MaxTokensTurnEnd{}, want: subagent.StopMaxTokens},
		{
			name:   "aborted",
			reason: sessionlog.AbortedTurnEnd{Reason: sessionlog.ParentCancel{}},
			want:   subagent.StopAborted,
		},
		// 一次前置步骤的拒绝把认领下来的提示词丢掉了：这件活儿是被回绝了。
		{name: "blocked", reason: sessionlog.BlockedTurnEnd{}, want: subagent.StopRefusal},
		{
			name:   "error",
			reason: sessionlog.ErrorTurnEnd{Error: llm.Failure{Message: "炸了", Code: "provider_error"}},
			want:   subagent.StopError,
		},
		{name: "interrupted", reason: sessionlog.InterruptedTurnEnd{}, want: subagent.StopError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			work := agent.FoldConsumedWork(steppedTurn(t, 0, testCase.reason))
			if !work.HasEnd {
				t.Fatal("这个回合该交代得了消耗")
			}
			if got := toStopReason(work); got != testCase.want {
				t.Fatalf("该映成 %q，实际 %q", testCase.want, got)
			}
		})
	}

	// 一次连交代回合都没有的运行永远不会把结果说得比实情好。
	if got := toStopReason(agent.ConsumedWork{}); got != subagent.StopError {
		t.Fatalf("没有交代回合该落在出错上，实际 %q", got)
	}
}

func TestStartInProcessRunDemandsAStructuredCapture(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.factory.onChild = answersWith(t, "答案")

	schema := answerSchema()
	request := fixture.request("干活")
	request.OutputSchema = &schema

	run := fixture.start(t, t.Context(), request, RunOptions{})
	result, err := run.Result(t.Context())
	if err != nil {
		t.Fatalf("这次运行不该报基础设施故障：%v", err)
	}
	// 跑完了却一份合法的捕获都没留下：它没有兑现自己被要求的那份契约。
	if result.StopReason != subagent.StopError {
		t.Fatalf("没交出结构化结果的运行不该算跑完了，实际 %q", result.StopReason)
	}
	if result.Structured != nil {
		t.Fatalf("一份都没捕获时不该有结构化结果，实际 %v", result.Structured)
	}
	if got := textOf(result.Output); got != "答案" {
		t.Fatalf("助手输出照样读得出来，实际 %q", got)
	}
}
