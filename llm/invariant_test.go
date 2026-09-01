// 本文件验这个包自己那两条运行期不变量：流式协议的文法，和「一次已提交的
// 拓扑变动留下的注册表是读得通的」。
//
// 源: packages/llm/llm/tests/invariant.spec.ts:1-123
//
// 文法那一组一律**从 [Runtime.Stream] 走**，而不是直接调 [validateStream]：
// 检查装在瀑布的最外面，验的是消费方最终看到的那条流，所以用例里那条坏流是由
// 一层中间件交出来的——这正好一并钉住「一层中间件把流改坏了同样得被抓住」。

package llm

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
)

// armInvariants 造一条全开的注册表，把本包的检查装到运行时上，用例结束时注销。
func armInvariants(t *testing.T, runtime *Runtime) {
	t.Helper()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	dispose, err := RegisterInvariants(t.Context(), registry, runtime)
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
}

// violation 跑一次动作，把违例接下来；没有违例时返回 nil。
//
// 违例是 panic 出来的（[invariants.Fail] 的约定），所以只能这么接。
func violation(t *testing.T, dispatch func()) *invariants.Error {
	t.Helper()

	var thrown any
	func() {
		defer func() { thrown = recover() }()
		dispatch()
	}()

	if thrown == nil {
		return nil
	}
	failure, ok := thrown.(*invariants.Error)
	if !ok {
		t.Fatalf("该抛出 *invariants.Error，实际 %#v", thrown)
	}
	if failure.PackageName != PackageName {
		t.Errorf("违例该记在 %q 名下，实际 %q", PackageName, failure.PackageName)
	}
	return failure
}

// ---- 流式协议文法 ----

// streamingRuntime 造一个装好检查的运行时，并挂一层直接交出 source 的中间件。
//
// 这层中间件不往里走，所以整组用例一个适配器都不需要登记。
func streamingRuntime(t *testing.T, source iter.Seq2[StreamChunk, error]) *Runtime {
	t.Helper()
	runtime := newTestRuntime(t)
	armInvariants(t, runtime)

	rule := func(
		context.Context,
		GenerateOptions,
		func(context.Context) (iter.Seq2[StreamChunk, error], error),
	) (iter.Seq2[StreamChunk, error], error) {
		return source, nil
	}
	if _, err := runtime.OnStream(t.Context(), testScope(t), rule); err != nil {
		t.Fatalf("挂中间件失败：%v", err)
	}
	return runtime
}

// openStream 开一条流；这一步本身不该失败，失败都在读的时候才发生。
func openStream(t *testing.T, runtime *Runtime) iter.Seq2[StreamChunk, error] {
	t.Helper()
	stream, err := runtime.Stream(t.Context(), GenerateOptions{Provider: "甲", Model: "m-1"})
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	return stream
}

// grammarViolation 把这串分块当成一条流读干，交出被接住的那条文法违例。
func grammarViolation(t *testing.T, items ...StreamChunk) *invariants.Error {
	t.Helper()
	runtime := streamingRuntime(t, chunks(items...))
	return violation(t, func() {
		//nolint:revive // 这里只关心读的过程会不会抛，读到什么不重要。
		for range openStream(t, runtime) {
		}
	})
}

// requireGrammarViolation 断言这串分块违反了文法，并且那句话点到了 want。
func requireGrammarViolation(t *testing.T, want string, items ...StreamChunk) {
	t.Helper()
	failure := grammarViolation(t, items...)
	if failure == nil {
		t.Fatalf("该抓住一条文法违例，说的是 %q", want)
	}
	if !strings.Contains(failure.Message, want) {
		t.Fatalf("违例该点到 %q，实际 %q", want, failure.Message)
	}
}

// TestStreamGrammarRejectsBadIndices 钉住下标那两条：非负，以及一个下标不重复开。
func TestStreamGrammarRejectsBadIndices(t *testing.T) {
	requireGrammarViolation(t, "must be non-negative",
		BlockStartChunk{Index: -1, BlockType: BlockText})
	requireGrammarViolation(t, "must be non-negative",
		BlockEndChunk{Index: -1, Block: TextBlock{}})
	requireGrammarViolation(t, "must be non-negative",
		TextDeltaChunk{Index: -1, Text: "坏的"})
	requireGrammarViolation(t, "repeated block-start index 0",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		BlockStartChunk{Index: 0, BlockType: BlockText})
}

// TestStreamGrammarRequiresAnOpenBlockForEveryDelta 把「增量只落在同类型的开着的
// 块上」在三种增量上各走一遍，并钉住那句诊断分得清「没开着」和「开着别的类型」。
func TestStreamGrammarRequiresAnOpenBlockForEveryDelta(t *testing.T) {
	requireGrammarViolation(t, "got no open block",
		TextDeltaChunk{Index: 0, Text: "没有开着的块"})
	requireGrammarViolation(t, "got no open block",
		ReasoningDeltaChunk{Index: 0, Text: "没有开着的块"})
	requireGrammarViolation(t, "got no open block",
		ToolCallDeltaChunk{Index: 0, ID: "c-1"})

	// 开着的是文本，来的是推理增量：那句诊断要把实际开着的类型说出来。
	requireGrammarViolation(t, "got text",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		ReasoningDeltaChunk{Index: 0, Text: "对不上"})
}

// TestStreamGrammarChecksBlockEnd 钉住 block-end 那两条：关的必须是开着的那一块，
// 类型必须对得上；块是 nil 时诊断说得出「没有块」。
func TestStreamGrammarChecksBlockEnd(t *testing.T) {
	requireGrammarViolation(t, "has no open block",
		BlockEndChunk{Index: 0, Block: TextBlock{}})
	requireGrammarViolation(t, "closes reasoning, expected text",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		BlockEndChunk{Index: 0, Block: ReasoningBlock{}})
	requireGrammarViolation(t, "closes no block",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		BlockEndChunk{Index: 0, Block: nil})
}

// TestStreamGrammarAllowsOnlyOneUsage 钉住用量最多一次。
func TestStreamGrammarAllowsOnlyOneUsage(t *testing.T) {
	requireGrammarViolation(t, "usage more than once",
		UsageChunk{Usage: TokenUsage{InputTokens: 1}},
		UsageChunk{Usage: TokenUsage{InputTokens: 2}})
}

// TestStreamGrammarChecksTermination 钉住收尾那三条：终止分块之后不再有别的分块、
// 一条正常结束的流必须以终止分块收尾、正常结束时不许留着开着的块。
func TestStreamGrammarChecksTermination(t *testing.T) {
	requireGrammarViolation(t, "after terminal finish",
		FinishChunk{Reason: StopFinish{}},
		TextDeltaChunk{Index: 0, Text: "结束之后还说话"})
	requireGrammarViolation(t, "without a terminal finish chunk",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		BlockEndChunk{Index: 0, Block: TextBlock{}})
	requireGrammarViolation(t, "finished with 1 open block(s)",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		FinishChunk{Reason: StopFinish{}})

	// 结束原因是 nil 时按 stop 算，所以照样不许留着开着的块。
	requireGrammarViolation(t, "finished with 1 open block(s)",
		BlockStartChunk{Index: 0, BlockType: BlockText},
		FinishChunk{})
}

// TestStreamGrammarAllowsOpenBlocksOnInterruptedFinish 钉住那条例外：error 和
// aborted 是**中途**停下来的两种结局，它们身上留着开着的块是这次中断本来的样子。
func TestStreamGrammarAllowsOpenBlocksOnInterruptedFinish(t *testing.T) {
	for _, reason := range []FinishReason{
		ErrorFinish{Failure: Failure{Code: "BOOM"}},
		AbortedFinish{Failure: Failure{Code: AbortedCode}},
	} {
		t.Run(string(reason.FinishKind()), func(t *testing.T) {
			failure := grammarViolation(t,
				BlockStartChunk{Index: 0, BlockType: BlockText},
				FinishChunk{Reason: reason})
			if failure != nil {
				t.Fatalf("中途停下来时留着开着的块不该算违例：%v", failure)
			}
		})
	}
}

// TestStreamGrammarAcceptsAWellFormedStream 走一遍完全合规的那条流，确认检查
// 只**观察**：一块不改、一块不吞。
func TestStreamGrammarAcceptsAWellFormedStream(t *testing.T) {
	runtime := streamingRuntime(t, chunks(
		BlockStartChunk{Index: 0, BlockType: BlockText},
		TextDeltaChunk{Index: 0, Text: "你好"},
		BlockEndChunk{Index: 0, Block: TextBlock{Text: "你好"}},
		UsageChunk{Usage: TokenUsage{InputTokens: 3}},
		FinishChunk{Reason: StopFinish{}},
	))

	collected, err := drain(t, openStream(t, runtime))
	if err != nil {
		t.Fatalf("读流不该出错：%v", err)
	}
	if len(collected) != 5 {
		t.Fatalf("每一块都该原样放行，得到 %d 块", len(collected))
	}
}

// TestStreamGrammarPassesThroughAnErroredStream 钉住一条报错终止的流照原样放行，
// 而且不会因为「没有以终止分块收尾」再被告一状——那一句不适用于它。
func TestStreamGrammarPassesThroughAnErroredStream(t *testing.T) {
	boom := errors.New("流断了")
	runtime := streamingRuntime(t, failingStream(boom,
		BlockStartChunk{Index: 0, BlockType: BlockText}))

	var err error
	if failure := violation(t, func() {
		_, err = drain(t, openStream(t, runtime))
	}); failure != nil {
		t.Fatalf("报错终止不该算文法违例：%v", failure)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("那条错误该原样带出来，得到 %v", err)
	}
}

// TestStreamGrammarAllowsAnEarlyBreak 钉住消费方提前收手不算违例：那条流没有
// 「结束」过，最后那一句检查根本执行不到。
func TestStreamGrammarAllowsAnEarlyBreak(t *testing.T) {
	runtime := streamingRuntime(t, chunks(
		BlockStartChunk{Index: 0, BlockType: BlockText},
		TextDeltaChunk{Index: 0, Text: "读一块就走"},
		FinishChunk{Reason: StopFinish{}},
	))

	if failure := violation(t, func() {
		for range openStream(t, runtime) {
			break
		}
	}); failure != nil {
		t.Fatalf("提前收手不该算违例：%v", failure)
	}
}

// TestStreamGrammarStopsAfterDispose 钉住注销之后检查真的停下来了：一条不该再查的
// 检查继续在别人的流上抛，比不查更坏。
func TestStreamGrammarStopsAfterDispose(t *testing.T) {
	runtime := newTestRuntime(t)
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)
	dispose, err := RegisterInvariants(t.Context(), registry, runtime)
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}

	rule := func(
		context.Context,
		GenerateOptions,
		func(context.Context) (iter.Seq2[StreamChunk, error], error),
	) (iter.Seq2[StreamChunk, error], error) {
		return chunks(TextDeltaChunk{Index: -1, Text: "怎么看都不合文法"}), nil
	}
	if _, err := runtime.OnStream(t.Context(), testScope(t), rule); err != nil {
		t.Fatalf("挂中间件失败：%v", err)
	}

	dispose()
	if failure := violation(t, func() {
		//nolint:revive // 同上，只关心抛不抛。
		for range openStream(t, runtime) {
		}
	}); failure != nil {
		t.Fatalf("注销之后不该再查：%v", failure)
	}
}

// ---- 注册表拓扑 ----

// TestRegistryInvariantCatchesADesyncedOrder 钉住第二条检查：路由次序表和路由表
// 必须一一对应。两张表一旦对不上，[Runtime.ListProviders] 就会对着一个不存在的键
// 解引用。
//
// 这里直接改那两张表，因为**没有任何一条正当的调用路径造得出这个状态**——
// 它要防的就是本包自己以后写错。
func TestRegistryInvariantCatchesADesyncedOrder(t *testing.T) {
	t.Run("条数对不上", func(t *testing.T) {
		runtime := newTestRuntime(t)
		armInvariants(t, runtime)
		runtime.adapterOrder = append(runtime.adapterOrder, "凭空多出来的")

		failure := violation(t, runtime.emitAdaptersUpdated)
		if failure == nil || !strings.Contains(failure.Message, "1 ordered routes but 0 registered ones") {
			t.Fatalf("该报条数对不上，得到 %v", failure)
		}
	})

	t.Run("次序里有表里没有的路由", func(t *testing.T) {
		runtime := newTestRuntime(t)
		armInvariants(t, runtime)
		registerFake(t, runtime, "甲", &fakeAdapter{})
		healthy := runtime.adapterOrder
		runtime.adapterOrder = []string{"另一条"}

		failure := violation(t, runtime.emitAdaptersUpdated)
		// 复原：用例收尾时作用域释放会再通知一次，两张表还对不上的话那一次也要抛。
		runtime.adapterOrder = healthy
		if failure == nil || !strings.Contains(failure.Message, `provider "另一条" has no readable registration`) {
			t.Fatalf("该报那条路由读不出来，得到 %v", failure)
		}
	})
}

// TestRegistryInvariantIsQuietOnAHealthyRegistry 钉住一张读得通的注册表不报。
func TestRegistryInvariantIsQuietOnAHealthyRegistry(t *testing.T) {
	runtime := newTestRuntime(t)
	armInvariants(t, runtime)
	registerFake(t, runtime, "甲", &fakeAdapter{})

	if failure := violation(t, runtime.emitAdaptersUpdated); failure != nil {
		t.Fatalf("正常的注册表不该报：%v", failure)
	}
}

// TestObserverInvariantFailureIsRethrownAfterEveryoneRan 钉住观察者里抛出来的
// 不变量违例的处理方式：留住第一条、让**每一个**观察者都跑到、跑完再抛。
//
// 一条违例不能被一个坏观察者吞掉，也不能因为它就少通知别人——那次改动已经提交了。
func TestObserverInvariantFailureIsRethrownAfterEveryoneRan(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := testScope(t)

	first := &invariants.Error{PackageName: PackageName, Message: "第一条"}
	for _, observer := range []AdaptersUpdatedObserver{
		func() { panic(first) },
		func() { panic(&invariants.Error{PackageName: PackageName, Message: "第二条"}) },
	} {
		if _, err := runtime.OnAdaptersUpdated(t.Context(), owner, observer); err != nil {
			t.Fatalf("登记观察者失败：%v", err)
		}
	}
	reached := false
	if _, err := runtime.OnAdaptersUpdated(t.Context(), owner, func() { reached = true }); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	failure := violation(t, runtime.emitAdaptersUpdated)
	if failure != first {
		t.Fatalf("该抛出留住的第一条，得到 %v", failure)
	}
	if !reached {
		t.Fatal("前面抛出的违例不该拦住后面的观察者")
	}
}

// TestRegisterInvariantsRejections 钉住两个必需参数缺一不可，以及同一条注册表上
// 装第二次会失败——注册表是按包名预留的。
func TestRegisterInvariantsRejections(t *testing.T) {
	runtime := newTestRuntime(t)

	if _, err := RegisterInvariants(t.Context(), nil, runtime); err == nil {
		t.Fatal("没有注册表该被拒")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	if _, err := RegisterInvariants(t.Context(), registry, nil); err == nil {
		t.Fatal("没有运行时该被拒")
	}
	dispose, err := RegisterInvariants(t.Context(), registry, runtime)
	if err != nil {
		t.Fatalf("装检查不该失败：%v", err)
	}
	t.Cleanup(dispose)
	if _, err := RegisterInvariants(t.Context(), registry, runtime); err == nil {
		t.Fatal("同一条注册表上装第二次该失败")
	}
}
