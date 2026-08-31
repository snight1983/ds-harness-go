// 本文件验「某个还开着的步骤当时路由到了哪个提供方」这件事读得准。
package llmretry

import (
	"errors"
	"testing"

	"ds-harness-go/session"
)

// TestAnOpenStepReportsItsRoutedProvider 钉住一个开着的步骤读得出它的提供方。
//
// 源: packages/llm/llm-retry/src/history.ts:14-33
func TestAnOpenStepReportsItsRoutedProvider(t *testing.T) {
	t.Parallel()

	provider, present, err := ProviderForOpenStep(openStepLog(t, "甲"), 1, 1)
	if err != nil || !present || provider != "甲" {
		t.Fatalf("该读出提供方「甲」，得到 %q（present=%v err=%v）", provider, present, err)
	}
}

// TestAClosedStepReportsNothing 钉住 step/end 之后就读不出了。
//
// 一次重试收拾的是**当下这次请求**的残局。步骤已经收掉了还读得出提供方的话，
// 一条写在步骤外的重试会被放行，而重放时它归属哪一次请求就没有答案了。
func TestAClosedStepReportsNothing(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"), stepEnd(t, 4, 1, 1))
	if _, present, err := ProviderForOpenStep(events, 1, 1); present || err != nil {
		t.Fatalf("收掉的步骤不该读得出（present=%v err=%v）", present, err)
	}
}

// TestATurnEndAlsoClosesTheStep 钉住 turn/end 同样算一次收尾。
//
// 源: packages/llm/llm-retry/src/history.ts:16-19
//
// DSH 那句 some(...) 把 step/end 和 turn/end 并列。回合都结束了，它里面那个没收尾
// 的步骤当然也不再开着——不然一条写在回合外的重试会因为翻到了上个回合的表头而通过。
func TestATurnEndAlsoClosesTheStep(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"), turnEnd(4))
	if _, present, err := ProviderForOpenStep(events, 1, 1); present || err != nil {
		t.Fatalf("回合结束之后不该读得出（present=%v err=%v）", present, err)
	}
}

// TestReopeningAStepMakesItOpenAgain 钉住同一个步骤再开一次就又算开着的。
//
// DSH 找的是**最后**那条匹配的 step/start，所以一个收掉之后又重开的步骤是开着的。
// 找第一条的话，一次续跑之后写下的重试会全被判成「落在步骤外」。
func TestReopeningAStepMakesItOpenAgain(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		stepEnd(t, 4, 1, 1),
		stepStart(t, 5, 1, 1),
	)
	provider, present, err := ProviderForOpenStep(events, 1, 1)
	if err != nil || !present || provider != "甲" {
		t.Fatalf("重开之后该又读得出（%q present=%v err=%v）", provider, present, err)
	}
}

// TestAnotherStepReportsNothing 钉住问的是别的步骤时读不出。
func TestAnotherStepReportsNothing(t *testing.T) {
	t.Parallel()

	events := openStepLog(t, "甲")
	if _, present, _ := ProviderForOpenStep(events, 1, 2); present {
		t.Error("步骤 1/2 从没开过，不该读得出")
	}
	if _, present, _ := ProviderForOpenStep(events, 2, 1); present {
		t.Error("步骤 2/1 从没开过，不该读得出")
	}
}

// TestAStepWithoutAnyHeaderReportsNothing 钉住没有表头时读不出。
//
// 没有表头就没有「路由到了哪个提供方」这件事。硬给一个空串的话，不变量那条
// 「提供方要对得上」会拿一个空串去比，于是一条没有提供方的重试反而通过了。
func TestAStepWithoutAnyHeaderReportsNothing(t *testing.T) {
	t.Parallel()

	events := []session.Event{turnStart(t, 1, 1), stepStart(t, 2, 1, 1)}
	if _, present, err := ProviderForOpenStep(events, 1, 1); present || err != nil {
		t.Fatalf("没有表头时不该读得出（present=%v err=%v）", present, err)
	}
}

// TestTheProviderComesFromTheLastHeaderInTheWholeLog 钉住取的是**整段日志里最后
// 一条**表头，不是这个步骤里的那条。
//
// 源: packages/llm/llm-retry/src/history.ts:24-31
//
// DSH 那个倒扫是从 events.length - 1 起的，不是从那条 step/start 起的。两者在正常
// 日志上是同一条（表头写在步骤内），这里逐字跟着 DSH 走，并把这件事钉下来——
// 哪天有人把它改成「从 step/start 往回扫」，这条会响。
func TestTheProviderComesFromTheLastHeaderInTheWholeLog(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"), header(4, "乙"))
	provider, present, err := ProviderForOpenStep(events, 1, 1)
	if err != nil || !present || provider != "乙" {
		t.Fatalf("该读出最后那条表头上的「乙」，得到 %q（present=%v err=%v）", provider, present, err)
	}
}

// TestAnUnreadableBoundaryPayloadIsReported 钉住坏掉的边界事件报出来，
// 而不是被当成「这个步骤没开着」。
//
// 咽下去的话，一份坏日志会让每一次重试都静静地变成「不重了」——一个看起来像是
// 模型在无故放弃的现象，而真正的毛病在日志上。
func TestAnUnreadableBoundaryPayloadIsReported(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		rawEvent(2, session.EventStepStart, `{"step":"一"}`),
	}
	if _, _, err := ProviderForOpenStep(events, 1, 1); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}
