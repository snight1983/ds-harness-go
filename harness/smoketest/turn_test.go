// 本文件的作用：把一棵真的树装起来——先决依赖、一条脚本化的模型流、一个循环
// 工厂、一个公布出来的 agent——然后验这一轮确实跑得完、文本和用量收得回来。
//
// 源: packages/test-support/loader-smoke/tests/agent-turn.spec.ts

package smoketest_test

import (
	"context"
	"iter"
	"log/slog"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/agentloop"
	"github.com/snight1983/ds-harness-go/harness/harnesstest"
	"github.com/snight1983/ds-harness-go/harness/smoketest"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// world 是一棵装完了的树：五样先决依赖、一条脚本化的模型流、一个循环工厂。
type world struct {
	deps *harnesstest.Dependencies
	loop *agentloop.AgentLoop
}

// newWorld 装一棵树，那段模型流按 chunks 吐。
func newWorld(t *testing.T, chunks []llm.StreamChunk) *world {
	t.Helper()
	ctx := context.Background()

	deps := harnesstest.Mount(t, harnesstest.Options{
		SystemPrompt: systemprompt.Options{OmitHarnessIdentity: true},
	})

	// 这条流规则不调 next，瀑布最里面那层适配器因此走不到——一次冒烟要模型说什么，
	// 由这里说了算，不必再登记一个假适配器。
	detach, err := deps.Models.OnStream(ctx, deps.Scope, func(
		_ context.Context,
		_ llm.GenerateOptions,
		_ func(context.Context) (iter.Seq2[llm.StreamChunk, error], error),
	) (iter.Seq2[llm.StreamChunk, error], error) {
		return func(yield func(llm.StreamChunk, error) bool) {
			for _, chunk := range chunks {
				if !yield(chunk, nil) {
					return
				}
			}
		}, nil
	})
	if err != nil {
		t.Fatalf("装流规则失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(ctx) })

	loop, unwind, err := agentloop.New(ctx, agentloop.Deps{
		Agents:       deps.Agents,
		Sessions:     deps.Sessions,
		LLM:          deps.Models,
		Tools:        deps.Tools,
		SystemPrompt: deps.SystemPrompt,
	}, deps.Scope, agentloop.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("装循环失败：%v", err)
	}
	t.Cleanup(func() { _ = unwind(ctx) })

	return &world{deps: deps, loop: loop}
}

// create 造一个公布出来的 agent。
func (w *world) create(t *testing.T, id sessionlog.SessionID) agent.Agent {
	t.Helper()
	live, err := w.loop.Create(context.Background(), id,
		agent.Options{Provider: "甲", Model: "m-1"}, "")
	if err != nil {
		t.Fatalf("造 agent %q 失败：%v", string(id), err)
	}
	return live
}

// run 驱一轮。
func (w *world) run(t *testing.T, options smoketest.TurnOptions) (smoketest.TurnResult, error) {
	t.Helper()
	return smoketest.RunTurn(context.Background(), smoketest.Deps{
		Agents:   w.deps.Agents,
		Sessions: w.deps.Sessions,
		Owner:    w.deps.Scope,
	}, options)
}

// textReply 是一段吐完一句话、报一次记账、正常收尾的流。
func textReply(text string, usage llm.TokenUsage) []llm.StreamChunk {
	return []llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: text},
		llm.UsageChunk{Usage: usage},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
}

// TestOneTurnRunsEndToEnd 是本包存在的理由那一条：装得起来、跑得完一轮、收得回结果。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:56-100
func TestOneTurnRunsEndToEnd(t *testing.T) {
	usage := llm.TokenUsage{InputTokens: 12, OutputTokens: 34}
	stage := newWorld(t, textReply("跑通了", usage))
	stage.create(t, "s")

	var seen []sessionlog.EventType
	result, err := stage.run(t, smoketest.TurnOptions{
		Task: "说句话",
		OnEvent: func(_ sessionlog.SessionID, event sessionlog.Event) {
			seen = append(seen, event.Type)
		},
	})
	if err != nil {
		t.Fatalf("驱一轮失败：%v", err)
	}

	if result.SessionID != "s" {
		t.Fatalf("会话身份不对：%q", string(result.SessionID))
	}
	if result.Output != "跑通了" {
		t.Fatalf("最终文本不对：%q", result.Output)
	}
	// 同一个步骤的记账走了两条路进来——先是那条 usage 分块，后是装配好的助手消息
	// 身上那一份。按步骤坐标去重之后应当只算一次。
	if result.Usage == nil {
		t.Fatal("这一轮报过记账，却没收到")
	}
	if *result.Usage != usage {
		t.Fatalf("记账被重复累加了：%+v", *result.Usage)
	}

	if !contains(seen, sessionlog.EventAssistantMessage) {
		t.Fatalf("观察者没看见助手消息：%v", seen)
	}
	// 那道认领起点的闸：第一条看得见的，必须是「这条消息进了收件箱」。
	if len(seen) == 0 || seen[0] != agent.EventInboxSpliced {
		t.Fatalf("观察者看见的第一条不是那次收件箱改动：%v", seen)
	}
}

// TestATurnWithoutReportedUsageLeavesUsageEmpty 钉住「没人报过记账」和
// 「报了一份全零」分得开。
func TestATurnWithoutReportedUsageLeavesUsageEmpty(t *testing.T) {
	stage := newWorld(t, []llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: "无账"},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	})
	stage.create(t, "s")

	result, err := stage.run(t, smoketest.TurnOptions{Task: "说句话"})
	if err != nil {
		t.Fatalf("驱一轮失败：%v", err)
	}
	if result.Output != "无账" {
		t.Fatalf("最终文本不对：%q", result.Output)
	}
	if result.Usage != nil {
		t.Fatalf("没人报过记账，却收到一份：%+v", *result.Usage)
	}
}

// TestATreeWithTwoRootsIsRefused 钉住那条「恰好一个顶层 agent」的要求：多出一个
// 就报错，而不是随便挑一个——挑错了后面所有断言验的都是另一棵树。
func TestATreeWithTwoRootsIsRefused(t *testing.T) {
	stage := newWorld(t, textReply("跑通了", llm.TokenUsage{}))
	stage.create(t, "甲")
	stage.create(t, "乙")

	if _, err := stage.run(t, smoketest.TurnOptions{Task: "说句话"}); err == nil {
		t.Fatal("两个顶层 agent 竟然驱得动")
	}
}

// TestAnEmptyTreeIsRefused 钉住同一条要求的另一头。
func TestAnEmptyTreeIsRefused(t *testing.T) {
	stage := newWorld(t, textReply("跑通了", llm.TokenUsage{}))

	if _, err := stage.run(t, smoketest.TurnOptions{Task: "说句话"}); err == nil {
		t.Fatal("一个 agent 都没有竟然驱得动")
	}
}

// TestMissingDepsAreRefused 钉住三样依赖缺一不可。
func TestMissingDepsAreRefused(t *testing.T) {
	stage := newWorld(t, textReply("跑通了", llm.TokenUsage{}))
	stage.create(t, "s")

	if _, err := smoketest.RunTurn(context.Background(), smoketest.Deps{
		Sessions: stage.deps.Sessions,
		Owner:    stage.deps.Scope,
	}, smoketest.TurnOptions{Task: "说句话"}); err == nil {
		t.Fatal("少了 agent 注册表竟然驱得动")
	}
}

func contains[T comparable](items []T, want T) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
