// 本文件的作用：把这条循环和它朝外那一面钉在真会出错的边上——每一轮那段提示词里
// 到底带了什么、三种收场各自在哪一轮跳出来、一个交不出报告的孩子怎么收场，
// 以及开第一个孩子之前那几道关。
//
// # 这些测试防的是什么错
//
//   - **交接没跨过轮去**。下一轮那个孩子看不见上一份报告的话，Ralph 就退化成
//     「反复叫一个人从头干同一件事」，而且看不出来——它照样会跑完、照样会报完成。
//   - **父的上下文漏给了孩子**。一个 inherits 的提供方正好把这件工具存在的理由
//     抵消掉，所以那道关必须在开工之前把。
//   - **一次注定跑不成的调用先花了钱**。几道关全得在开第一个孩子之前过完。
//   - **孩子没放干净**。每一轮都得 Dispose，包括那一轮出错的时候；处置失败也不许
//     盖掉一次各自独立的结果失败。
//   - **一个非正常终态被当成一轮成功**。那份结构化结果可能是残的，拿它当交接
//     会让下一轮在半截东西上接着跑。
//   - **一份不合规矩的报告被当成「孩子挂了」糊过去**。那说明这条路上有人没按契约
//     办事，糊过去只会让下一次更难查。
//   - **模型挑的轮数被悄悄夹到天花板**。模型是按它以为的轮数在规划的，夹了它不知道。
//   - **半装上去**。工具装上了、提示词段没装上，或者反过来。

package toolralph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/core/tools"
	"ds-harness-go/invariants"
	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/subagent/subagent"
)

// ---- 假件 ----

// stubAgent 是一个只为满足 [agent.Agent] 契约而存在的假 agent。
type stubAgent struct {
	id  session.SessionID
	own *scope.Scope
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return nil }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                         { return nil }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Followup(llm.Message)                                   {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                 {}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubProvider 是一个假提供方。三个开关分别对应 [requireFreshProvider] 那三道关。
type stubProvider struct {
	name     string
	caps     subagent.Capabilities
	inherits bool
}

func (p *stubProvider) Name() string                        { return p.name }
func (p *stubProvider) Capabilities() subagent.Capabilities { return p.caps }
func (p *stubProvider) InheritsParentContext() bool         { return p.inherits }

func (p *stubProvider) Start(context.Context, subagent.ResolvedStartRequest) (subagent.Run, error) {
	return nil, errors.New("这个假提供方不直接开工")
}

// freshProvider 是一个过得了那三道关的提供方。
func freshProvider() *stubProvider {
	return &stubProvider{name: DefaultSubagentProvider, caps: subagent.Capabilities{OutputSchema: true}}
}

// roundScript 是台架给某一轮准备好的结局。
//
// 零值表示「跑成了、但什么结构化结果都没留下」——那正是 DSH 里 `rawReport === null`
// 的那种情况。
type roundScript struct {
	structured any
	stop       subagent.StopReason
	diagnostic string
	startErr   error
	resultErr  error
	disposeErr error
}

// completes 是一轮报完成的结局。
func completes(summary string, evidence ...string) roundScript {
	return succeeds(RoundReport{Status: RoundComplete, Summary: summary, Evidence: evidence})
}

// continues 是一轮报「还有活」的结局。
func continues(summary string, next ...string) roundScript {
	return succeeds(RoundReport{Status: RoundContinue, Summary: summary, NextSteps: next})
}

// blocks 是一轮报阻塞的结局。
func blocks(summary, blocker string) roundScript {
	return succeeds(RoundReport{Status: RoundBlocked, Summary: summary, Blocker: blocker})
}

func succeeds(report RoundReport) roundScript {
	return roundScript{structured: reportValue(report), stop: subagent.StopCompleted}
}

// scriptedRun 是一次照着脚本走的假运行。
type scriptedRun struct {
	seam   *scriptedSubagents
	script roundScript
}

func (r *scriptedRun) ID() session.SessionID   { return "child" }
func (r *scriptedRun) LocalAgent() agent.Agent { return nil }

func (r *scriptedRun) Result(context.Context) (subagent.Result, error) {
	if r.script.resultErr != nil {
		return subagent.Result{}, r.script.resultErr
	}
	return subagent.Result{
		Structured: r.script.structured,
		StopReason: r.script.stop,
		Diagnostic: r.script.diagnostic,
	}, nil
}

func (r *scriptedRun) Dispose(context.Context) error {
	r.seam.disposals++
	return r.script.disposeErr
}

// scriptedSubagents 是那条记账的假子 agent 接缝：第 n 次开工走第 n 条脚本。
type scriptedSubagents struct {
	provider  subagent.Provider
	scripts   []roundScript
	starts    []subagent.StartRequest
	disposals int
}

func seam(scripts ...roundScript) *scriptedSubagents {
	return &scriptedSubagents{provider: freshProvider(), scripts: scripts}
}

func (s *scriptedSubagents) GetProvider(name string) (subagent.Provider, bool) {
	if s.provider == nil || s.provider.Name() != name {
		return nil, false
	}
	return s.provider, true
}

func (s *scriptedSubagents) Start(
	_ context.Context,
	_ string,
	request subagent.StartRequest,
) (subagent.Run, error) {
	index := len(s.starts)
	s.starts = append(s.starts, request)
	if index >= len(s.scripts) {
		return nil, fmt.Errorf("台架没给第 %d 轮准备结局——这条循环多跑了一轮", index+1)
	}
	script := s.scripts[index]
	if script.startErr != nil {
		return nil, script.startErr
	}
	return &scriptedRun{seam: s, script: script}, nil
}

// promptOf 交出第 round 轮那个孩子拿到的那段提示词。
func (s *scriptedSubagents) promptOf(t *testing.T, round int) string {
	t.Helper()
	if round < 1 || round > len(s.starts) {
		t.Fatalf("第 %d 轮没开过工，一共开了 %d 轮", round, len(s.starts))
	}
	return contentText(s.starts[round-1].Prompt)
}

func contentText(content llm.Content) string {
	var builder strings.Builder
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			builder.WriteString(text.Text)
		}
	}
	return builder.String()
}

// ---- 台面 ----

type world struct {
	root       *scope.Scope
	agentScope *scope.Scope
	caller     *stubAgent
	tools      *tools.Runtime
	prompts    *systemprompt.Registry
}

func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	owner, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %s 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := scopeOf(t, "root", nil)
	agentScope := scopeOf(t, "agent", root)
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	prompts, err := systemprompt.NewRegistry(t.Context(), root, systemprompt.Options{})
	if err != nil {
		t.Fatalf("造提示词注册表失败：%v", err)
	}
	return &world{
		root:       root,
		agentScope: agentScope,
		caller:     &stubAgent{id: "caller", own: agentScope},
		tools:      runtime,
		prompts:    prompts,
	}
}

func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key != w.agentScope.Key() {
		return nil, errors.New("这把钥匙不属于任何一个 agent")
	}
	return w.caller, nil
}

// controller 造一个装配好默认值的控制器。
func (w *world) controller(t *testing.T, config Config) *Controller {
	t.Helper()
	config.AgentOf = w.agentOf
	controller, err := New(config)
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

// ---- 装配面 ----

// TestNewFillsTheDefaults 钉住四个零值各自取到那个部署默认值。
func TestNewFillsTheDefaults(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	controller := world.controller(t, Config{})
	if controller.provider != DefaultSubagentProvider ||
		controller.maxRounds != DefaultMaxRounds ||
		controller.maxHandoffChars != DefaultMaxHandoffChars ||
		controller.maxResultChars != DefaultMaxResultChars {
		t.Fatalf("默认值没填上：%#v", controller)
	}
}

// TestNewRejectsAnUnusableAssembly 钉住那几条装不成的配置在造的那一刻就报出来。
//
// 一份装不成的配置在这里报出来才找得到人；等到第一次调用才炸的话，那时候是模型
// 在等结果，运维根本不在场。
func TestNewRejectsAnUnusableAssembly(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	cases := []struct {
		name   string
		config Config
		want   string
	}{
		{"没给找 agent 的路", Config{}, "找回 agent 的路"},
		{"提供方名带空白", Config{SubagentProvider: " spawn "}, "不许带空白"},
		{"轮数是负的", Config{MaxRounds: -1}, "MaxRounds"},
		{"交接上限是负的", Config{MaxHandoffChars: -1}, "MaxHandoffChars"},
		{"终局上限是负的", Config{MaxResultChars: -1}, "MaxResultChars"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			config := each.config
			if each.name != "没给找 agent 的路" {
				config.AgentOf = world.agentOf
			}
			_, err := New(config)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
		})
	}
}

// ---- 每一轮那段提示词 ----

// TestRoundPromptCarriesTheHandoffAcrossRounds 钉住那份交接真的跨了轮。
//
// 这是整件事的命门：孩子看不见父的对话、也看不见上一个孩子的会话，除了工作区之外
// 它唯一拿得到的东西就是这份交接。它没跨过去，Ralph 就退化成「反复叫一个人从头干
// 同一件事」——而且看不出来，它照样会跑完、照样会报完成。
func TestRoundPromptCarriesTheHandoffAcrossRounds(t *testing.T) {
	t.Parallel()
	previous := goodReport()
	first := roundPrompt("把测试补全", 1, 5, nil)
	second := roundPrompt("把测试补全", 2, 5, &previous)

	if !strings.Contains(first, firstRoundHandoff) {
		t.Fatalf("第一轮该明说没有上一份交接，实际 %q", first)
	}
	if strings.Contains(first, "读了那三个文件") {
		t.Fatalf("第一轮不该有任何交接内容")
	}
	if !strings.Contains(second, encodeReport(previous)) {
		t.Fatalf("第二轮该原样带上那份交接，实际 %q", second)
	}
	for _, want := range []string{
		"Ralph round: 2 of 5.",
		"Immutable objective:\n把测试补全",
		"Do not call the ralph tool",
		"long-term memory and source of truth",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("提示词里该有 %q，实际 %q", want, second)
		}
	}
}

// TestRoundPromptIsPure 钉住同样的入参排出一模一样的字。
//
// 这不是洁癖：这段提示词是**唯一**跨轮的东西，它要是带上了时间、随机数、或者
// 任何本轮之外的状态，「每一轮换一个干净的人」这件事就不成立了——两次同样的处境
// 会给出不一样的指令，而没有任何一条日志说得清差在哪儿。
func TestRoundPromptIsPure(t *testing.T) {
	t.Parallel()
	previous := goodReport()
	if roundPrompt("目标", 3, 9, &previous) != roundPrompt("目标", 3, 9, &previous) {
		t.Fatalf("同样的入参该排出同样的字")
	}
}

// ---- 那条循环 ----

// runLoopOf 把循环推一遍，交回结果和那条假接缝。
func runLoopOf(t *testing.T, scripts ...roundScript) (*world, *scriptedSubagents, runResult, int, error) {
	t.Helper()
	world := newWorld(t)
	controller := world.controller(t, Config{})
	subagents := seam(scripts...)
	result, started, err := controller.runLoop(
		t.Context(), subagents, world.caller, "把测试补全", len(scripts))
	return world, subagents, result, started, err
}

// TestLoopStopsAtTheFirstCompletion 钉住报完成当场跳出来，后面那几轮一个都不开。
//
// 多跑一轮就是多花一个孩子的钱，而且那一轮会在一个已经完成的目标上瞎折腾。
func TestLoopStopsAtTheFirstCompletion(t *testing.T) {
	t.Parallel()
	_, subagents, result, started, err := runLoopOf(t,
		continues("看了一圈", "把测试补上"),
		completes("补完了", "go test 全绿"),
		continues("不该跑到这一轮", "..."))
	if err != nil {
		t.Fatalf("这条循环该跑成：%v", err)
	}
	if result.Status != RunComplete || result.RoundsStarted != 2 || started != 2 {
		t.Fatalf("该在第二轮报完成，实际 %#v（起了 %d 个）", result, started)
	}
	if len(subagents.starts) != 2 {
		t.Fatalf("该只开两个孩子，实际 %d 个", len(subagents.starts))
	}
	if result.Report.Summary != "补完了" {
		t.Fatalf("交回的该是最后那一轮的报告，实际 %#v", result.Report)
	}
	// 第二轮那个孩子必须看得见第一轮留下的东西。
	if !strings.Contains(subagents.promptOf(t, 2), "看了一圈") {
		t.Fatalf("第二轮没拿到第一轮的交接：%q", subagents.promptOf(t, 2))
	}
}

// TestLoopStopsAtABlocker 钉住报阻塞同样当场跳出来。
//
// 一个说不动了的孩子，换一个人来也一样动不了——挡路的是外面那件事，不是这个孩子。
func TestLoopStopsAtABlocker(t *testing.T) {
	t.Parallel()
	_, subagents, result, _, err := runLoopOf(t,
		blocks("动不了", "缺一把仓库的写权限"),
		continues("不该跑到这一轮", "..."))
	if err != nil {
		t.Fatalf("这条循环该跑成：%v", err)
	}
	if result.Status != RunBlocked || result.RoundsStarted != 1 {
		t.Fatalf("该在第一轮报阻塞，实际 %#v", result)
	}
	if len(subagents.starts) != 1 {
		t.Fatalf("该只开一个孩子，实际 %d 个", len(subagents.starts))
	}
}

// TestLoopRunsOutOfRounds 钉住轮次用光时交回最后那份 continue 报告。
//
// 交回它而不是报错：那些活儿是真干了的，工作区上留着，父需要那份交接才接得下去。
func TestLoopRunsOutOfRounds(t *testing.T) {
	t.Parallel()
	_, subagents, result, started, err := runLoopOf(t,
		continues("第一轮", "接着干"),
		continues("第二轮", "还得接着干"))
	if err != nil {
		t.Fatalf("这条循环该跑成：%v", err)
	}
	if result.Status != RunBudgetLimited || result.RoundsStarted != 2 || started != 2 {
		t.Fatalf("该报轮次用光，实际 %#v", result)
	}
	if result.Report.Summary != "第二轮" {
		t.Fatalf("交回的该是最后那份交接，实际 %#v", result.Report)
	}
	if len(subagents.starts) != 2 {
		t.Fatalf("该正好开两个孩子，实际 %d 个", len(subagents.starts))
	}
}

// TestLoopDisposesEveryRound 钉住每一轮那个孩子都放干净了。
//
// 漏一个就是漏一条还在跑的活。
func TestLoopDisposesEveryRound(t *testing.T) {
	t.Parallel()
	_, subagents, _, _, err := runLoopOf(t,
		continues("第一轮", "接着干"),
		completes("做完了", "证据"))
	if err != nil {
		t.Fatalf("这条循环该跑成：%v", err)
	}
	if subagents.disposals != 2 {
		t.Fatalf("两轮该放两次，实际 %d 次", subagents.disposals)
	}
}

// TestRoundFailureCarriesTheLastHandoff 钉住一个交不出报告的孩子把上一份交接
// 一起带上来。
//
// 这条路上有四种砸法（开不出来、等结果炸了、终态不正常、跑完了却没留下结构化
// 结果），每一种都得是同一个落点：一次带着交接的 [roundFailure]。
func TestRoundFailureCarriesTheLastHandoff(t *testing.T) {
	t.Parallel()
	broken := []struct {
		name   string
		script roundScript
		want   string
	}{
		{"开不出孩子", roundScript{startErr: errors.New("提供方拒了")}, "提供方拒了"},
		{"等结果炸了", roundScript{resultErr: errors.New("传输断了")}, "传输断了"},
		{"被取消了",
			roundScript{stop: subagent.StopAborted, diagnostic: "父把这一步掐了"},
			"Ralph round child was cancelled\nDiagnostic: 父把这一步掐了"},
		{"跑挂了", roundScript{stop: subagent.StopError}, "Ralph round child failed"},
		{"撞上 token 上限", roundScript{stop: subagent.StopMaxTokens}, "hit its token limit"},
		{"拒绝了这件事", roundScript{stop: subagent.StopRefusal}, "declined the task"},
		{"认不出的终态", roundScript{stop: "who-knows"}, "ended abnormally (who-knows)"},
		{"跑完了却什么都没留下",
			roundScript{stop: subagent.StopCompleted},
			"finished without a structured round report"},
	}
	for _, each := range broken {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, started, err := runLoopOf(t, continues("第一轮", "接着干"), each.script)
			var failure *roundFailure
			if !errors.As(err, &failure) {
				t.Fatalf("该是一次轮次失败，实际 %v", err)
			}
			if failure.round != 2 || started != 2 {
				t.Fatalf("该是第二轮砸的，实际第 %d 轮", failure.round)
			}
			if failure.lastReport == nil || failure.lastReport.Summary != "第一轮" {
				t.Fatalf("该带着第一轮那份交接，实际 %#v", failure.lastReport)
			}
			if !strings.Contains(err.Error(), each.want) {
				t.Fatalf("那段话里该有 %q，实际 %q", each.want, err.Error())
			}
		})
	}
}

// TestFirstRoundFailureHasNoHandoff 钉住第一轮就砸了的时候确实没有交接可带。
func TestFirstRoundFailureHasNoHandoff(t *testing.T) {
	t.Parallel()
	_, _, _, _, err := runLoopOf(t, roundScript{stop: subagent.StopError})
	var failure *roundFailure
	if !errors.As(err, &failure) {
		t.Fatalf("该是一次轮次失败，实际 %v", err)
	}
	if failure.round != 1 || failure.lastReport != nil {
		t.Fatalf("第一轮砸的时候不该有交接，实际 %#v", failure)
	}
	if !strings.Contains(err.Error(), "No previous handoff was available.") {
		t.Fatalf("该明说没有交接，实际 %q", err.Error())
	}
}

// TestDisposalFailureDoesNotHideTheRoundFailure 钉住两件各自要人去看的故障
// 都留在那一个错里。
//
// 只报后一个等于把前一个藏了。
func TestDisposalFailureDoesNotHideTheRoundFailure(t *testing.T) {
	t.Parallel()
	_, _, _, _, err := runLoopOf(t, roundScript{
		stop:       subagent.StopError,
		disposeErr: errors.New("孩子静不下来"),
	})
	if err == nil {
		t.Fatalf("该报错")
	}
	text := err.Error()
	if !strings.Contains(text, "Ralph round child failed") || !strings.Contains(text, "孩子静不下来") {
		t.Fatalf("两条原因都该在，实际 %q", text)
	}
}

// TestDisposalFailureAloneStillFailsTheRound 钉住只有处置砸了的时候那一轮照样算砸。
//
// 一个放不干净的孩子是一条还在跑的活。就算它那份报告拿到了，也不能假装这一轮
// 干干净净地过去了。
func TestDisposalFailureAloneStillFailsTheRound(t *testing.T) {
	t.Parallel()
	script := completes("做完了", "证据")
	script.disposeErr = errors.New("孩子静不下来")
	_, _, _, _, err := runLoopOf(t, script)
	if err == nil || !strings.Contains(err.Error(), "孩子静不下来") {
		t.Fatalf("处置砸了该报出来，实际 %v", err)
	}
}

// TestAMalformedReportIsAHardError 钉住一份不合规矩的报告**不**走轮次失败那条路。
//
// 那说明这条路上有人没按契约办事（提供方没验 schema、或者本包的 schema 和校验
// 对不上）。把它当成「孩子挂了」糊过去，只会让下一次更难查。
func TestAMalformedReportIsAHardError(t *testing.T) {
	t.Parallel()
	_, _, _, _, err := runLoopOf(t, roundScript{
		structured: map[string]any{"status": "complete", "summary": "做完了"},
		stop:       subagent.StopCompleted,
	})
	var failure *roundFailure
	if errors.As(err, &failure) {
		t.Fatalf("这该是个硬错，不是一次轮次失败：%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "Ralph round 1:") {
		t.Fatalf("该指出是哪一轮，实际 %v", err)
	}
}

// TestLoopRejectsAnOversizedHandoff 钉住一份撑爆上限的交接把整次调用拒掉。
//
// 这道闸是 Ralph 的另一半：不设上限的话，一个孩子可以把整段对话塞进 summary，
// 「每轮换一个干净的人」就退化成了「换个地方接着堆上下文」。
func TestLoopRejectsAnOversizedHandoff(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	controller := world.controller(t, Config{MaxHandoffChars: 64})
	subagents := seam(continues(strings.Repeat("啰", 200), "接着干"))
	_, _, err := controller.runLoop(t.Context(), subagents, world.caller, "目标", 1)
	if err == nil || !strings.Contains(err.Error(), "exceeds maxHandoffChars") {
		t.Fatalf("该拒掉这份交接，实际 %v", err)
	}
}

// TestEveryRoundGetsAFreshStructuredChild 钉住每一轮那次开工都要了那份 schema、
// 挂在同一个父下面、并且带着能认出是第几轮的名字。
//
// schema 少了就没有结构化输出，那份轮次报告是轮与轮之间唯一的载荷。
func TestEveryRoundGetsAFreshStructuredChild(t *testing.T) {
	t.Parallel()
	world, subagents, _, _, err := runLoopOf(t,
		continues("第一轮", "接着干"),
		completes("做完了", "证据"))
	if err != nil {
		t.Fatalf("这条循环该跑成：%v", err)
	}
	for index, request := range subagents.starts {
		if request.OutputSchema == nil || request.OutputSchema.AdditionalProperties == nil {
			t.Fatalf("第 %d 轮没要那份封闭的 schema", index+1)
		}
		if request.Parent != world.caller {
			t.Fatalf("第 %d 轮挂错了父", index+1)
		}
		want := fmt.Sprintf("Ralph round %d", index+1)
		if request.Label != want {
			t.Fatalf("第 %d 轮的名字是 %q，该是 %q", index+1, request.Label, want)
		}
	}
}

// ---- 那件工具 ----

// installed 是一次装好了的工具，连着它那条假接缝。
type installed struct {
	world     *world
	subagents *scriptedSubagents
	tool      *tools.Definition
	undo      func(context.Context) error
}

func install(t *testing.T, config Config, scripts ...roundScript) *installed {
	t.Helper()
	world := newWorld(t)
	controller := world.controller(t, config)
	subagents := seam(scripts...)
	undo, err := controller.Install(t.Context(), world.root, Deps{
		Tools:     world.tools,
		Subagents: subagents,
		Prompts:   world.prompts,
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	definition, ok := world.tools.Get(ToolName, world.root.Key())
	if !ok {
		t.Fatalf("装完了却找不到那件工具")
	}
	return &installed{world: world, subagents: subagents, tool: definition, undo: undo}
}

// call 拿一份入参调那件工具。
func (i *installed) call(t *testing.T, args string) (json.RawMessage, error) {
	t.Helper()
	return i.tool.Execute(t.Context(), json.RawMessage(args), &tools.RunContext{
		Execution: tools.Execution{ExecutionInput: tools.ExecutionInput{
			Agent: i.world.agentScope.Key(),
		}},
	})
}

// TestToolReturnsTheRunResult 钉住一次跑成的调用交回起了几个孩子和那份终局值。
func TestToolReturnsTheRunResult(t *testing.T) {
	t.Parallel()
	live := install(t, Config{},
		continues("第一轮", "接着干"),
		completes("做完了", "go test 全绿"))
	raw, err := live.call(t, `{"objective":"  把测试补全  "}`)
	if err != nil {
		t.Fatalf("这次调用该跑成：%v", err)
	}
	var value outputValue
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("交回来的值解不动：%v", err)
	}
	if value.AgentsStarted != 2 || value.Result.Status != RunComplete {
		t.Fatalf("交回来的值是 %#v", value)
	}
	// 目标前后那些空白得剪掉再进提示词——它们在提示词里是真的占位置的。
	if !strings.Contains(live.subagents.promptOf(t, 1), "Immutable objective:\n把测试补全\n") {
		t.Fatalf("目标没剪干净：%q", live.subagents.promptOf(t, 1))
	}
}

// TestToolRendersTheTerminalTextForTheModel 钉住那份权威值渲染成给模型看的那段话。
func TestToolRendersTheTerminalTextForTheModel(t *testing.T) {
	t.Parallel()
	live := install(t, Config{}, completes("做完了", "go test 全绿"))
	raw, err := live.call(t, `{"objective":"把测试补全"}`)
	if err != nil {
		t.Fatalf("这次调用该跑成：%v", err)
	}
	content, err := live.tool.Output.Render(nil, raw)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	text := contentText(content)
	if !strings.Contains(text, "Ralph worker reported completion after 1 round.") {
		t.Fatalf("渲染出来的是 %q", text)
	}
	if _, err := live.tool.Output.Render(nil, json.RawMessage(`{`)); err == nil {
		t.Fatalf("解不动的值该报错")
	}
}

// TestToolRefusesBeforeSpendingAnything 钉住那几道关全在开第一个孩子之前把。
//
// 一次注定跑不成的调用要在一分钱都还没花的时候就被拒掉。
func TestToolRefusesBeforeSpendingAnything(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args string
		want string
	}{
		{"入参解不动", `{`, "unexpected end"},
		{"目标是空的", `{"objective":"   "}`, "must be a non-empty string"},
		{"轮数是 0", `{"objective":"目标","maxRounds":0}`, "must be a positive safe integer"},
		{"轮数是负的", `{"objective":"目标","maxRounds":-3}`, "must be a positive safe integer"},
		{"轮数超过天花板", `{"objective":"目标","maxRounds":9}`, "exceeds the deployment ceiling 4"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			live := install(t, Config{MaxRounds: 4}, completes("做完了", "证据"))
			_, err := live.call(t, each.args)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
			if len(live.subagents.starts) != 0 {
				t.Fatalf("一个孩子都不该开，实际开了 %d 个", len(live.subagents.starts))
			}
		})
	}
}

// TestToolSurfacesARoundFailure 钉住循环里那次失败原样交回给调用方。
//
// 它必须是一个 error 而不是一份「跑成了但内容是失败」的输出值：后者会让父那边的
// 工具循环把这次调用当成正常结果记进日志，而这次调用其实什么都没跑成。
func TestToolSurfacesARoundFailure(t *testing.T) {
	t.Parallel()
	live := install(t, Config{},
		continues("第一轮", "接着干"),
		roundScript{startErr: errors.New("提供方开不出孩子")})
	_, err := live.call(t, `{"objective":"目标"}`)
	if err == nil || !strings.Contains(err.Error(), "Ralph round 2 child failed") {
		t.Fatalf("该把那次轮次失败交回来，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "第一轮") {
		t.Fatalf("上一轮那份交接该跟着一起交回来，实际 %q", err.Error())
	}
}

// TestToolDefaultsToTheDeploymentCeiling 钉住模型没点名时用部署那个天花板。
func TestToolDefaultsToTheDeploymentCeiling(t *testing.T) {
	t.Parallel()
	live := install(t, Config{MaxRounds: 2}, continues("第一轮", "接着干"), continues("第二轮", "还得干"))
	raw, err := live.call(t, `{"objective":"目标"}`)
	if err != nil {
		t.Fatalf("这次调用该跑成：%v", err)
	}
	var value outputValue
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("交回来的值解不动：%v", err)
	}
	if value.Result.Status != RunBudgetLimited || value.AgentsStarted != 2 {
		t.Fatalf("该正好跑满两轮，实际 %#v", value)
	}
	if !strings.Contains(live.subagents.promptOf(t, 1), "Ralph round: 1 of 2.") {
		t.Fatalf("孩子该知道一共几轮：%q", live.subagents.promptOf(t, 1))
	}
}

// TestToolHonorsASmallerModelChosenCap 钉住模型可以在天花板以内挑一个更小的。
func TestToolHonorsASmallerModelChosenCap(t *testing.T) {
	t.Parallel()
	live := install(t, Config{MaxRounds: 8}, continues("第一轮", "接着干"))
	raw, err := live.call(t, `{"objective":"目标","maxRounds":1}`)
	if err != nil {
		t.Fatalf("这次调用该跑成：%v", err)
	}
	var value outputValue
	_ = json.Unmarshal(raw, &value)
	if value.AgentsStarted != 1 {
		t.Fatalf("该只跑一轮，实际 %d 轮", value.AgentsStarted)
	}
}

// TestToolRequiresACallingAgent 钉住查不回来那个父 agent 就是错。
//
// 那个父 agent 是每一轮那个孩子的属主——派发深度、血统、工作目录全从它推出来，
// 而工作区正是 Ralph 唯一的长期记忆。
func TestToolRequiresACallingAgent(t *testing.T) {
	t.Parallel()
	live := install(t, Config{}, completes("做完了", "证据"))
	stranger := &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: scope.NewKey("外人")},
	}}
	for _, exec := range []*tools.RunContext{nil, {}, stranger} {
		_, err := live.tool.Execute(t.Context(), json.RawMessage(`{"objective":"目标"}`), exec)
		if err == nil || !strings.Contains(err.Error(), "requires a calling agent") {
			t.Fatalf("该报缺 agent，实际 %v", err)
		}
	}
}

// TestToolRequiresAGenuinelyFreshProvider 钉住那三道提供方检查。
//
// 第三条是这件工具的立身之本：一个会把父上下文带给孩子的提供方，正好把 Ralph
// 存在的理由抵消掉了。
func TestToolRequiresAGenuinelyFreshProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider subagent.Provider
		want     string
	}{
		{"压根没登记", nil, "is not registered"},
		{"名字对不上", &stubProvider{name: "别的"}, "is not registered"},
		{"不支持结构化输出", &stubProvider{name: DefaultSubagentProvider},
			"does not support structured output"},
		{"会把父的上下文带过去", &stubProvider{
			name:     DefaultSubagentProvider,
			caps:     subagent.Capabilities{OutputSchema: true},
			inherits: true,
		}, "inherits parent context"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			live := install(t, Config{}, completes("做完了", "证据"))
			live.subagents.provider = each.provider
			_, err := live.call(t, `{"objective":"目标"}`)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
			if len(live.subagents.starts) != 0 {
				t.Fatalf("一个孩子都不该开，实际开了 %d 个", len(live.subagents.starts))
			}
		})
	}
}

// TestToolIsNotConcurrencySafe 钉住这件工具**不**声明自己可以并排跑。
//
// 它的每一轮都在那个共享工作区上真动手，而工作区正是它唯一的长期记忆。让两次
// Ralph 并排跑，等于让两条循环轮流覆盖对方刚写下的东西，而两边都会以为那是自己
// 上一轮的成果。
func TestToolIsNotConcurrencySafe(t *testing.T) {
	t.Parallel()
	live := install(t, Config{}, completes("做完了", "证据"))
	if live.tool.IsConcurrencySafe != nil {
		t.Fatalf("这件工具不该声明并发安全")
	}
}

// TestToolPresentsItselfAsAnExecution 钉住那张卡片的样子。
func TestToolPresentsItselfAsAnExecution(t *testing.T) {
	t.Parallel()
	live := install(t, Config{}, completes("做完了", "证据"))
	view, ok := live.tool.PresentCall(json.RawMessage(`{"objective":"目标"}`)).(tools.GenericCallView)
	if !ok || view.Title != ToolName || view.Kind != tools.CallExecute {
		t.Fatalf("那张卡片是 %#v", view)
	}
	// 解不动的入参也得给出一张卡片，不能让展示这一步把整次调用带走。
	if live.tool.PresentCall(json.RawMessage(`{`)) == nil {
		t.Fatalf("入参解不动的时候也该有一张卡片")
	}
}

// TestOutputSchemaDescribesTheResult 钉住那份输出契约是**写出来**的，不是一个
// 不透明的口子。
//
// 新增: DSH 那份里 result 是 `{ type: 'json' }`，形状全靠运行时兜。写出来之后，
// 工具运行时那道输出校验能替本包把关。runId 不在里面，理由见 [outputValue]。
func TestOutputSchemaDescribesTheResult(t *testing.T) {
	t.Parallel()
	schema := outputSchema()
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("那份输出契约该是封闭的")
	}
	names := make([]string, 0, len(schema.Properties))
	for _, property := range schema.Properties {
		names = append(names, property.Name)
	}
	if len(names) != 2 || !contains(names, "agentsStarted") || !contains(names, "result") {
		t.Fatalf("那份输出契约的字段是 %v", names)
	}
	if contains(names, "runId") {
		t.Fatalf("没有 workflow 运行可以指，runId 不该在里面")
	}
	result := schema.Properties[1].Schema
	if len(result.Properties) != 3 || result.Type != tools.TypeObject {
		t.Fatalf("result 该是一个写明形状的对象，实际 %#v", result)
	}
}

// ---- 装配 ----

// TestInstallMountsTheToolAndItsGuidance 钉住两样一起装上、又一起摘干净。
func TestInstallMountsTheToolAndItsGuidance(t *testing.T) {
	t.Parallel()
	live := install(t, Config{}, completes("做完了", "证据"))
	if err := live.undo(t.Context()); err != nil {
		t.Fatalf("摘不干净：%v", err)
	}
	if _, ok := live.world.tools.Get(ToolName, live.world.root.Key()); ok {
		t.Fatalf("摘完了那件工具还在")
	}
	// 摘干净之后再摘一遍不许炸——undo 会在 t.Cleanup 里再跑一次。
	if err := live.undo(t.Context()); err != nil {
		t.Fatalf("重复摘该是空操作：%v", err)
	}
}

// TestInstallRejectsAnIncompleteAssembly 钉住四样必需品少一样就装不上。
func TestInstallRejectsAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	controller := world.controller(t, Config{})
	full := Deps{Tools: world.tools, Subagents: seam(), Prompts: world.prompts}
	cases := []struct {
		name  string
		deps  Deps
		owner *scope.Scope
		want  string
	}{
		{"没有工具运行时", Deps{Subagents: full.Subagents, Prompts: full.Prompts}, world.root, "工具运行时"},
		{"没有子 agent 接缝", Deps{Tools: full.Tools, Prompts: full.Prompts}, world.root, "子 agent 接缝"},
		{"没有提示词注册表", Deps{Tools: full.Tools, Subagents: full.Subagents}, world.root, "提示词注册表"},
		{"没有作用域", full, nil, "作用域"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			_, err := controller.Install(t.Context(), each.owner, each.deps)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
		})
	}
}

// TestInstallUnwindsWhenTheGuidanceFails 钉住后一步砸了的时候前一步收得回来。
//
// 半装上去比装不上更糟：那件工具在，模型会去调它，而那段说明什么时候该用它的
// 指引不在。
func TestInstallUnwindsWhenTheGuidanceFails(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	controller := world.controller(t, Config{})
	// 先把那个段名占掉，于是装配走到第二步必然重名失败。
	occupy, err := world.prompts.Section(t.Context(), world.root, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText("占位"),
	})
	if err != nil {
		t.Fatalf("占位那一段装不上：%v", err)
	}
	t.Cleanup(func() { _ = occupy(context.Background()) })

	_, err = controller.Install(t.Context(), world.root, Deps{
		Tools:     world.tools,
		Subagents: seam(),
		Prompts:   world.prompts,
	})
	if err == nil || !strings.Contains(err.Error(), "装那段用法指引失败") {
		t.Fatalf("该在第二步砸，实际 %v", err)
	}
	if _, ok := world.tools.Get(ToolName, world.root.Key()); ok {
		t.Fatalf("第二步砸了，第一步装上的那件工具该收回去")
	}
}

// TestInstallUnwindsWhenTheToolFails 钉住第一步就砸的时候什么都没留下。
func TestInstallUnwindsWhenTheToolFails(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	controller := world.controller(t, Config{})
	occupy, err := world.tools.Register(t.Context(), world.root, &tools.Definition{
		Name:        ToolName,
		Description: "占位",
		Parameters:  tools.Node{Type: tools.TypeObject},
		Execute: func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeObject},
			Render: func(json.RawMessage, json.RawMessage) (llm.Content, error) {
				return llm.Content{llm.TextBlock{Text: "占位"}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("占位那件工具装不上：%v", err)
	}
	t.Cleanup(func() { _ = occupy(context.Background()) })

	_, err = controller.Install(t.Context(), world.root, Deps{
		Tools:     world.tools,
		Subagents: seam(),
		Prompts:   world.prompts,
	})
	if err == nil || !strings.Contains(err.Error(), "装那件 Ralph 工具失败") {
		t.Fatalf("该在第一步砸，实际 %v", err)
	}
}

// ---- 不变量 ----

// TestRegisterInvariantsReservesThePackage 钉住这个包在注册表里占住了位置，
// 尽管它一条检查都不装。
//
// 占住它是为了让「检查过了、结论是无需检查」和「这个包被漏掉了」区分得开。
func TestRegisterInvariantsReservesThePackage(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatalf("没有注册表该报错")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	unregister, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	unregister()
}
