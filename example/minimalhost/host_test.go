// 本文件的作用：从包外驱动一个真的回合，证明 [Assemble] 拼出来的那份闭环不只是
// 编译得过，而是跑得通——并且顺带钉住 [Host].Vocabulary 确实盖得住这个回合写下的
// 每一条事件。
//
// 新增: DSH 没有对应物，理由见包文档。

package minimalhost_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/example/minimalhost"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// scriptedAdapter 是一个照本子念的适配器：它不连任何提供方，只吐一段固定的文本流，
// 并把收到的每一份请求留下来。
//
// 它同时是本包第二件要证的事：[llm.Adapter] 这个接口，一个**包外**的实现者只用
// 导出的东西就能实现出来。
type scriptedAdapter struct {
	reply string

	mutex sync.Mutex
	calls []llm.GenerateOptions
}

func (a *scriptedAdapter) Stream(
	_ context.Context, options llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	a.mutex.Lock()
	a.calls = append(a.calls, options)
	a.mutex.Unlock()

	chunks := []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText},
		llm.TextDeltaChunk{Index: 0, Text: a.reply},
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: a.reply}},
		llm.UsageChunk{Usage: llm.TokenUsage{InputTokens: 11, OutputTokens: 7}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}
	return func(yield func(llm.StreamChunk, error) bool) {
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
	}, nil
}

// lastCall 交出最后一次派发出去的那份请求。
func (a *scriptedAdapter) lastCall(t *testing.T) llm.GenerateOptions {
	t.Helper()

	a.mutex.Lock()
	defer a.mutex.Unlock()

	if len(a.calls) == 0 {
		t.Fatal("一次都没派发到适配器")
	}
	return a.calls[len(a.calls)-1]
}

// world 是一份装好的最小闭环，外加拆除登记。
type world struct {
	host    *minimalhost.Host
	adapter *scriptedAdapter
}

// newWorld 装一份闭环，并把拆除挂到测试的清理上。
func newWorld(t *testing.T) *world {
	t.Helper()

	adapter := &scriptedAdapter{reply: "已经改好了。"}
	host, unwind, err := minimalhost.Assemble(t.Context(), minimalhost.Options{
		Provider: "甲",
		Model:    "m-1",
		Adapter:  adapter,
		Persona:  "你是一个话很少的助手。",
	})
	if err != nil {
		t.Fatalf("装最小闭环失败：%v", err)
	}
	t.Cleanup(func() {
		if err := unwind(context.Background()); err != nil {
			t.Errorf("拆除失败：%v", err)
		}
	})
	return &world{host: host, adapter: adapter}
}

// runTurn 造一个 agent、送一条后续消息、等它静下来，交出那份会话日志。
func (w *world) runTurn(t *testing.T, text string) []sessionlog.Event {
	t.Helper()

	handle, err := w.host.Agents.Create(t.Context(), w.host.Scope, agent.CreateOptions{
		SessionID:    "一次装配",
		Cwd:          t.TempDir(),
		AgentOptions: agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("造 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = handle.Dispose(context.Background()) })

	handle.Agent.Followup(llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}}, llm.UserSource{}))
	if err := handle.Agent.WhenIdle(t.Context()); err != nil {
		t.Fatalf("等空闲失败：%v", err)
	}
	return handle.Agent.Session().Events()
}

// TestAssembledHostRunsATurn 钉住这份闭环从包外真的跑得动一个回合。
//
// 这是本包存在的理由：[github.com/snight1983/ds-harness-go/core/agentloop] 自己的测试全在包内、用的是那个
// 包自备的替身，它们证明不了「一个外部宿主只靠导出面能不能把回合跑起来」。
func TestAssembledHostRunsATurn(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	events := w.runTurn(t, "帮我改一下")

	want := []sessionlog.EventType{
		sessionlog.EventTurnStart,
		sessionlog.EventUserMessage,
		sessionlog.EventStepStart,
		sessionlog.EventAssistantMessage,
		sessionlog.EventTurnEnd,
	}
	for _, kind := range want {
		if !containsType(events, kind) {
			t.Errorf("日志里没有 %s，实际有 %v", kind, typesOf(events))
		}
	}
}

// TestAssembledHostSendsThePersonaToTheProvider 钉住系统提示词那一环真的接上了。
//
// 人设是 [minimalhost.Options] 里唯一一项会一路走到提供方请求上的配置。它没接上的话
// 装配照样跑得通、回合照样收尾，只是模型再也读不到部署方那份身份约束——一个不会
// 报错、只会悄悄变味的失败。
func TestAssembledHostSendsThePersonaToTheProvider(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.runTurn(t, "帮我改一下")

	system := w.adapter.lastCall(t).System
	if !strings.Contains(system, "话很少的助手") {
		t.Errorf("请求的系统提示词里没有那份人设：%q", system)
	}
}

// TestVocabularyKnowsEveryEventTheTurnWrote 钉住 [Host].Vocabulary 盖得住实际写下的事件。
//
// 这条不变量的代价落在**恢复**的时候，离现场隔着一次重启，见包文档。所以这里正着
// 验一遍：这个回合往日志里写了什么，那份词汇就得认识什么。
func TestVocabularyKnowsEveryEventTheTurnWrote(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	events := w.runTurn(t, "帮我改一下")
	if len(events) == 0 {
		t.Fatal("这个回合一条事件都没写下来")
	}

	for _, event := range events {
		if !w.host.Vocabulary.Knows(event.Type) {
			t.Errorf("拼出来的词汇不认识 %s——恢复时整段日志会被拒掉", event.Type)
		}
	}
}

// TestUnwindIsIdempotent 钉住拆除函数调第二次是空操作。
//
// 宿主多半会同时把它挂在 defer 和某条关停路径上，两边都跑到一次是常态。
func TestUnwindIsIdempotent(t *testing.T) {
	t.Parallel()

	_, unwind, err := minimalhost.Assemble(t.Context(), minimalhost.Options{
		Provider: "甲",
		Model:    "m-1",
		Adapter:  &scriptedAdapter{reply: "好"},
	})
	if err != nil {
		t.Fatalf("装最小闭环失败：%v", err)
	}
	if err := unwind(t.Context()); err != nil {
		t.Fatalf("第一次拆除失败：%v", err)
	}
	if err := unwind(t.Context()); err != nil {
		t.Errorf("第二次拆除该是空操作，实际 %v", err)
	}
}

// TestAssembleRejectsIncompleteOptions 钉住三项必填缺一样就当场报错。
//
// 缺了它们装配其实还能拼完，错要到第一次真发请求时才炸——那时调用栈上已经没有
// 装配那一段了。
func TestAssembleRejectsIncompleteOptions(t *testing.T) {
	t.Parallel()

	cases := map[string]minimalhost.Options{
		"没有提供方": {Model: "m-1", Adapter: &scriptedAdapter{}},
		"没有模型":  {Provider: "甲", Adapter: &scriptedAdapter{}},
		"没有适配器": {Provider: "甲", Model: "m-1"},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host, unwind, err := minimalhost.Assemble(t.Context(), options)
			if err == nil {
				_ = unwind(t.Context())
				t.Fatal("该被拒")
			}
			if host != nil || unwind != nil {
				t.Error("被拒的时候不该交出宿主或者拆除函数")
			}
		})
	}
}

// containsType 判断日志里有没有某个类型的事件。
func containsType(events []sessionlog.Event, kind sessionlog.EventType) bool {
	for _, event := range events {
		if event.Type == kind {
			return true
		}
	}
	return false
}

// typesOf 交出日志里各条事件的类型，只给断言失败时看。
func typesOf(events []sessionlog.Event) []sessionlog.EventType {
	kinds := make([]sessionlog.EventType, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Type)
	}
	return kinds
}
