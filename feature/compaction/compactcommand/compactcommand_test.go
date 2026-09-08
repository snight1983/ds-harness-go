// 本文件的作用：把 `/compact` 钉在它真会出错的几处——参数那道闸、六个分好类的失败
// 各自说哪一句、什么该说成回话什么该抛出去，以及摘除时那个「等干净」的次序。
//
// # 这些测试防的是什么错
//
//   - **参数被默默吃掉**。人敲 `/compact 最近十条`，心里想的是压那十条；这一层
//     压不了那十条，就必须当场告诉他，而不是照常压一段别的然后回一句「成了」。
//   - **一次故障被说成一句客气话**。装配没接对、后端自己炸了、单子上多出一个
//     认不得的分类码——这三样都不是「这次没压成」，把它们排成一句人读的提示，
//     等于让一次故障看起来像一次正常的拒绝。
//   - **哪一句配哪一个码错位**。busy 和 changed 对人的意思完全相反：一个是
//     「等会儿再来」，一个是「别再来了，去看看会话现在什么样」。
//   - **压成了却不指那条 compaction/summary**。那条事件带着被遮的一段、摘要本身
//     和那次调用的事实；不指过去，界面就只剩这一句干巴巴的话。
//   - **「还没有可压的历史」被说成失败**。那不是用户干错了什么。
//   - **摘除先等后注销**。那样等的过程里还能再进来一条，那个等永远等不完。
//   - **命令身份没往下传**。接缝要靠它把这次压缩和那条 command/run 配起来，
//     传丢了日志里那次压缩就成了没来由的。

package compactcommand

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/scope"
)

// ---- 手脚架 ----

// stubEngine 是一道说什么就是什么的压缩接缝。
//
// 打桩是对的：本包一个压缩决定都不做，它全部的行为就是「把接缝交上来的东西翻成
// 一句话」。真接缝在这里只会把被测的那点翻译埋进一堆和它无关的状态里。
type stubEngine struct {
	result    compaction.Result
	compacted bool
	err       error

	// calls 记下每一次 CompactNow 收到的命令身份。
	calls []string
	// before 在 CompactNow 干活之前跑一次，给并发那几个用例插一只手。
	before func()
}

func (e *stubEngine) CompactIfNeeded(
	context.Context, compaction.AgentContext, compaction.Trigger,
) (compaction.Result, bool, error) {
	return compaction.Result{}, false, errors.New("本包不该走这条路")
}

func (e *stubEngine) CompactNow(
	_ context.Context, _ compaction.ManualAgentContext, sourceCommandID string,
) (compaction.Result, bool, error) {
	if e.before != nil {
		e.before()
	}
	e.calls = append(e.calls, sourceCommandID)
	return e.result, e.compacted, e.err
}

func (e *stubEngine) CompactRegion(
	context.Context, int, int, compaction.AgentContext,
) (compaction.Result, error) {
	return compaction.Result{}, errors.New("本包不该走这条路")
}

// scopeOf 造一层作用域。
func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	own, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %q 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = own.Dispose(context.Background()) })
	return own
}

// live 是一台装好了控制器的东西：一道打桩的接缝、一把认得的作用域钥匙。
type live struct {
	engine     *stubEngine
	caller     *scope.Scope
	controller *Controller
}

func start(t *testing.T, engine *stubEngine) *live {
	t.Helper()
	caller := scopeOf(t, "caller", nil)
	controller, err := New(Config{
		Engine: engine,
		AgentOf: func(key *scope.Key) (compaction.ManualAgentContext, error) {
			if key != caller.Key() {
				return compaction.ManualAgentContext{}, errors.New("这把钥匙不认识")
			}
			return compaction.ManualAgentContext{}, nil
		},
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	return &live{engine: engine, caller: caller, controller: controller}
}

// run 跑一条 `/compact`，并且断言它没抛错。
func (l *live) run(t *testing.T, input string) commands.Result {
	t.Helper()
	result, err := l.controller.run(t.Context(), commands.Invocation{
		ID: "cmd-1", Agent: l.caller.Key(), RawInput: input,
	})
	if err != nil {
		t.Fatalf("`/compact %s` 不该抛错：%v", input, err)
	}
	return result
}

// newRuntime 造一张命令注册表。本包的测试不走 Execute，所以那条查日志的路用不着。
func newRuntime(t *testing.T) *commands.Runtime {
	t.Helper()
	runtime, err := commands.NewRuntime(commands.Options{
		LogOf: func(*scope.Key) (commands.Log, error) { return nil, errors.New("用不着") },
	})
	if err != nil {
		t.Fatalf("造命令注册表失败：%v", err)
	}
	return runtime
}

// ---- 装配 ----

// TestNewRejectsAnIncompleteAssembly 钉住缺哪一样都当场拒，而不是等人敲下第一条命令。
func TestNewRejectsAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	agentOf := func(*scope.Key) (compaction.ManualAgentContext, error) {
		return compaction.ManualAgentContext{}, nil
	}
	if _, err := New(Config{AgentOf: agentOf}); err == nil {
		t.Fatalf("没有压缩接缝该报错")
	}
	if _, err := New(Config{Engine: &stubEngine{}}); err == nil {
		t.Fatalf("没有那条查回 agent 的路该报错")
	}
	if _, err := New(Config{Engine: &stubEngine{}, AgentOf: agentOf}); err != nil {
		t.Fatalf("装配齐了不该报错：%v", err)
	}
}

// TestInstallPublishesTheCommandAndTakesItBack 钉住装上之后找得到、摘掉之后找不到。
func TestInstallPublishesTheCommandAndTakesItBack(t *testing.T) {
	t.Parallel()
	world := start(t, &stubEngine{})
	runtime := newRuntime(t)
	root := scopeOf(t, "root", nil)

	if _, err := world.controller.Install(t.Context(), root, Deps{}); err == nil {
		t.Fatalf("没有命令注册表该报错")
	}
	undo, err := world.controller.Install(t.Context(), root, Deps{Commands: runtime})
	if err != nil {
		t.Fatalf("装 /compact 失败：%v", err)
	}
	definition, found := runtime.Find(root.Key(), CommandName)
	if !found {
		t.Fatalf("装完之后该找得到 /%s", CommandName)
	}
	if definition.Description != commandDescription {
		t.Fatalf("摘要不对：%q", definition.Description)
	}
	// 没有输入描述符，所以注册表会把带图的调用挡在处理器之外。
	if definition.Input != nil {
		t.Fatalf("这条命令不收输入，不该有输入描述符")
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("摘 /compact 失败：%v", err)
	}
	if _, found := runtime.Find(root.Key(), CommandName); found {
		t.Fatalf("摘完之后不该还找得到 /%s", CommandName)
	}
}

// TestUninstallUnregistersBeforeItDrains 钉住摘除的次序：先注销，再等已经进去的那几次。
//
// 反过来先等再注销，等的过程中还能再进来一条，那个等永远等不完。这里让一次
// /compact 停在接缝里，摘除必须在它跑完之后才返回；而摘除已经开始之后，
// 注册表里那条命令必须已经不见了。
func TestUninstallUnregistersBeforeItDrains(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	engine := &stubEngine{compacted: false}
	engine.before = func() {
		close(entered)
		<-release
	}
	world := start(t, engine)
	runtime := newRuntime(t)
	root := scopeOf(t, "root", nil)
	undo, err := world.controller.Install(t.Context(), root, Deps{Commands: runtime})
	if err != nil {
		t.Fatalf("装 /compact 失败：%v", err)
	}

	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		if _, err := world.controller.run(context.Background(), commands.Invocation{
			ID: "cmd-1", Agent: world.caller.Key(),
		}); err != nil {
			t.Errorf("这次 /compact 不该抛错：%v", err)
		}
	}()
	<-entered

	drained := make(chan error, 1)
	go func() { drained <- undo(context.Background()) }()

	// 等到那条命令消失。它必须先消失——此刻那次 /compact 还卡在接缝里，
	// 而摘除还没返回，这两件同时成立才说明注销排在等待前面。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, found := runtime.Find(root.Key(), CommandName); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("摘除该先把 /%s 注销掉，不该一直等着", CommandName)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-drained:
		t.Fatalf("那次 /compact 还没跑完，摘除不该返回：%v", err)
	default:
	}

	close(release)
	if err := <-drained; err != nil {
		t.Fatalf("摘 /compact 失败：%v", err)
	}
	group.Wait()
}

// ---- 语法 ----

// TestAnyArgumentIsRefused 钉住这条命令只认「不带参数」。
//
// 关键是它**不去压**：人敲 `/compact 最近十条` 想的是那十条，照常压一段别的
// 再回一句「成了」，比拒绝他严重得多。
func TestAnyArgumentIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
	}{
		{"一个词", "now"},
		{"一段中文", "最近十条"},
		{"看着像区间", "3..7"},
		{"看着像子命令", "--force"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			engine := &stubEngine{compacted: true}
			result := start(t, engine).run(t, each.input)
			if result.Kind != commands.ResultError || result.Text != usage {
				t.Fatalf("该回用法提示，实际 %+v", result)
			}
			if len(engine.calls) != 0 {
				t.Fatalf("带参数的调用一次都不该走到接缝，实际走了 %d 次", len(engine.calls))
			}
		})
	}
}

// TestBlankInputIsNoArgument 钉住空白不算参数。
//
// 编辑器把命令名后面那个分隔空白原样交上来，所以「不带参数」在这一层看到的
// 常常是几个空格而不是空串。
func TestBlankInputIsNoArgument(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", " ", "  \t\n "} {
		engine := &stubEngine{compacted: false}
		result := start(t, engine).run(t, input)
		if result.Kind != commands.ResultSuccess {
			t.Fatalf("%q 该被当成不带参数，实际 %+v", input, result)
		}
		if len(engine.calls) != 1 {
			t.Fatalf("%q 该走到接缝一次，实际 %d 次", input, len(engine.calls))
		}
	}
}

// ---- 成了的两种 ----

// TestNothingToCompactIsASuccess 钉住「还没有可压的历史」回的是成功。
//
// 成功而不是错误：那不是用户干错了什么。
func TestNothingToCompactIsASuccess(t *testing.T) {
	t.Parallel()
	result := start(t, &stubEngine{compacted: false}).run(t, "")
	if result.Kind != commands.ResultSuccess || result.Text != nothingToCompact {
		t.Fatalf("回话不对：%+v", result)
	}
	if result.SourceEventSeq != nil {
		t.Fatalf("什么都没压，不该指向任何一条事件")
	}
}

// TestACompactionReportsItsSizeAndPointsAtTheSummary 钉住那句话里的两个数，
// 以及它指向那条 compaction/summary。
func TestACompactionReportsItsSizeAndPointsAtTheSummary(t *testing.T) {
	t.Parallel()
	engine := &stubEngine{
		compacted: true,
		result: compaction.Result{
			SummarySeq:         42,
			ShadowedSeqs:       []int{7, 8, 9},
			ShadowedTokenCount: 1234,
		},
	}
	result := start(t, engine).run(t, "")
	if result.Kind != commands.ResultSuccess {
		t.Fatalf("该是一次成功，实际 %q", result.Kind)
	}
	if result.Text != "Compacted 3 history items (~1234 tokens)." {
		t.Fatalf("回话不对：%q", result.Text)
	}
	if result.SourceEventSeq == nil || *result.SourceEventSeq != 42 {
		t.Fatalf("该指向那条 compaction/summary（seq 42），实际 %v", result.SourceEventSeq)
	}
}

// TestTheCommandIdentityReachesTheSeam 钉住命令身份原样传下去。
//
// 接缝要靠它把 compaction/start 和那条 command/run 配起来；传丢了，日志里那次压缩
// 就成了没来由的。
func TestTheCommandIdentityReachesTheSeam(t *testing.T) {
	t.Parallel()
	engine := &stubEngine{compacted: false}
	world := start(t, engine)
	if _, err := world.controller.run(t.Context(), commands.Invocation{
		ID: "cmd-abcd1234-7", Agent: world.caller.Key(),
	}); err != nil {
		t.Fatalf("不该抛错：%v", err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "cmd-abcd1234-7" {
		t.Fatalf("接缝收到的命令身份不对：%v", engine.calls)
	}
}

// ---- 六种预期之内的失败 ----

// TestEveryManualErrorCodeGetsItsOwnSentence 钉住那张封闭的单子上每一项都配得上一句话，
// 而且六句互不相同。
//
// 互不相同是要紧的：busy 和 changed 对人的意思完全相反，一个是「等会儿再来」，
// 一个是「别再来了，先去看看会话现在什么样」。
func TestEveryManualErrorCodeGetsItsOwnSentence(t *testing.T) {
	t.Parallel()
	codes := []compaction.ManualErrorCode{
		compaction.ManualErrorBusy,
		compaction.ManualErrorCancelled,
		compaction.ManualErrorChanged,
		compaction.ManualErrorSummary,
		compaction.ManualErrorCommit,
		compaction.ManualErrorPersistence,
	}
	seen := map[string]compaction.ManualErrorCode{}
	for _, code := range codes {
		engine := &stubEngine{err: compaction.NewManualError(code, "后端那句诊断", nil)}
		result := start(t, engine).run(t, "")
		if result.Kind != commands.ResultError {
			t.Fatalf("%s 该回一次错误结果，实际 %q", code, result.Kind)
		}
		if strings.TrimSpace(result.Text) == "" {
			t.Fatalf("%s 那句话是空的", code)
		}
		// 后端那句诊断是给日志和调用方看的，不该出现在给人的一句话里。
		if strings.Contains(result.Text, "后端那句诊断") {
			t.Fatalf("%s 把后端的诊断漏给了人：%q", code, result.Text)
		}
		if other, clash := seen[result.Text]; clash {
			t.Fatalf("%s 和 %s 说的是同一句话：%q", code, other, result.Text)
		}
		seen[result.Text] = code
	}
}

// TestBusyAndChangedSayOppositeThings 钉住那两句话各自点出了人此刻该做的事。
func TestBusyAndChangedSayOppositeThings(t *testing.T) {
	t.Parallel()
	busy := start(t, &stubEngine{
		err: compaction.NewManualError(compaction.ManualErrorBusy, "占着", nil),
	}).run(t, "")
	if !strings.Contains(busy.Text, "not idle") {
		t.Fatalf("busy 那句该说清是谁占着：%q", busy.Text)
	}
	changed := start(t, &stubEngine{
		err: compaction.NewManualError(compaction.ManualErrorChanged, "变了", nil),
	}).run(t, "")
	if !strings.Contains(changed.Text, "unchanged") ||
		!strings.Contains(changed.Text, "recorded in the session log") {
		t.Fatalf("changed 那句该说清对话没动、而这次尝试留在日志里：%q", changed.Text)
	}
}

// TestAWrappedManualErrorIsStillExpected 钉住裹了一层的分类失败照样认得出来。
//
// 接缝那边随时可能给它加一层上下文；靠 errors.As 而不是类型断言，正是为了这个。
func TestAWrappedManualErrorIsStillExpected(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(
		errors.New("外面那层"),
		compaction.NewManualError(compaction.ManualErrorSummary, "摘要没做出来", nil),
	)
	result := start(t, &stubEngine{err: wrapped}).run(t, "")
	if result.Kind != commands.ResultError ||
		!strings.Contains(result.Text, "could not produce a useful summary") {
		t.Fatalf("裹了一层的分类失败该照样认出来，实际 %+v", result)
	}
}

// ---- 该抛出去的三种 ----

// TestAnUnclassifiedFailureIsRethrown 钉住后端自己炸了原样抛回去。
//
// 那不是「这次没压成」，是这套东西坏了。排成一句给人的提示，等于把一次故障
// 藏进一句客气话里。
func TestAnUnclassifiedFailureIsRethrown(t *testing.T) {
	t.Parallel()
	boom := errors.New("存储层炸了")
	world := start(t, &stubEngine{err: boom})
	_, err := world.controller.run(t.Context(), commands.Invocation{
		ID: "cmd-1", Agent: world.caller.Key(),
	})
	if !errors.Is(err, boom) {
		t.Fatalf("该原样抛回去，实际 %v", err)
	}
}

// TestAnUnknownManualErrorCodeIsRethrown 钉住单子上多出来的一项也照样抛。
//
// DSH 那边这一支由 TypeScript 保证走不到；[compaction.ManualErrorCode] 是个开放的
// 字符串类型，所以这里走得到。走到就说明有人加了一项而没管这边——那是一次故障。
func TestAnUnknownManualErrorCodeIsRethrown(t *testing.T) {
	t.Parallel()
	odd := compaction.NewManualError("something-new", "谁加的", nil)
	world := start(t, &stubEngine{err: odd})
	_, err := world.controller.run(t.Context(), commands.Invocation{
		ID: "cmd-1", Agent: world.caller.Key(),
	})
	if !errors.Is(err, odd) {
		t.Fatalf("认不得的分类码该原样抛回去，实际 %v", err)
	}
}

// TestAnUnknownAgentIsRethrown 钉住装配没接对是错，不是一个错误结果。
//
// 人敲的这行字本身没毛病，把它说成「你这条命令有问题」是在替一次装配失误顶罪。
func TestAnUnknownAgentIsRethrown(t *testing.T) {
	t.Parallel()
	engine := &stubEngine{compacted: true}
	world := start(t, engine)
	stranger := scopeOf(t, "stranger", nil)
	_, err := world.controller.run(t.Context(), commands.Invocation{
		ID: "cmd-1", Agent: stranger.Key(),
	})
	if err == nil {
		t.Fatalf("查不回 agent 该抛错")
	}
	if len(engine.calls) != 0 {
		t.Fatalf("查不回 agent 就不该走到接缝，实际走了 %d 次", len(engine.calls))
	}
}

// ---- 不变量 ----

// TestRegisterInvariantsReservesThePackage 钉住这个包在注册表里占住了位置，
// 尽管它一条检查都不装。
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
