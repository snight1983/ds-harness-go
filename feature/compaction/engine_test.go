// 本文件的作用：验人工压缩那条可预期的失败在 Go 这一侧还能按分类分派，
// 以及压缩这道接缝收的那一小片 agent 真的能由一个现成的 agent 填上。

package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
)

// # 这些测试防的是什么错
//
//   - 六个失败分类里有一个被改了字面量：它们会**原样进人工命令的结果**，
//     上层照着写提示语，改一个就是一次对外行为的变更。
//   - [Maintainer] 的签名飘了，于是 harness/agent 的 Agent 不再结构上满足它。
//     那样一来装配方就得现包一层适配，而包这一层的人多半会顺手把取消或者
//     那条错误吞掉——这正是把它写成单方法接口要避免的事。
//   - [ManualAgentContext] 嵌的那半读不到，或者 [Maintainer] 那条边没接上，
//     于是一次人工压缩根本没和驱动串起来。

func TestManualError按分类取出来(t *testing.T) {
	t.Parallel()

	// DSH 那边是一个 `extends Error` 的类，靠 instanceof 认。Go 这边调用方
	// 用 errors.As 取出 Code 再分派——所以包了几层之后还得取得出来。
	cause := errors.New("上游 502")
	wrapped := fmt.Errorf("这一步没做成：%w", NewManualError(ManualErrorSummary, "总结请求失败", cause))

	var manual *ManualError
	if !errors.As(wrapped, &manual) {
		t.Fatalf("取不出来：%v", wrapped)
	}
	if manual.Code != ManualErrorSummary {
		t.Fatalf("分类是 %q", manual.Code)
	}
	if manual.Message != "总结请求失败" {
		t.Fatalf("诊断被改写成了 %q", manual.Message)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("原本那条失败查不下去了")
	}
}

func TestManualError没有原因时也能用(t *testing.T) {
	t.Parallel()

	manual := NewManualError(ManualErrorBusy, "已经有一次压缩占着", nil)
	if errors.Unwrap(manual) != nil {
		t.Fatal("凭空多出一条原因")
	}
	if !strings.Contains(manual.Error(), string(ManualErrorBusy)) {
		t.Fatalf("那句话里看不出分类：%s", manual.Error())
	}
	if !strings.Contains(manual.Error(), "已经有一次压缩占着") {
		t.Fatalf("那句话里看不出诊断：%s", manual.Error())
	}
}

func TestManualErrorCode是一张封闭的单子(t *testing.T) {
	t.Parallel()

	// 这六个取值会**原样进人工命令的结果**，上层照着它们写提示语。
	// 改动其中任何一个都是一次对外行为的变更。
	for got, want := range map[ManualErrorCode]string{
		ManualErrorBusy:        "busy",
		ManualErrorCancelled:   "cancelled",
		ManualErrorChanged:     "changed",
		ManualErrorSummary:     "summary",
		ManualErrorCommit:      "commit",
		ManualErrorPersistence: "persistence",
	} {
		if string(got) != want {
			t.Fatalf("分类排成了 %q，要的是 %q", got, want)
		}
	}
}

func TestTrigger是那两种(t *testing.T) {
	t.Parallel()

	if TriggerPressure != "pressure" || TriggerContextOverflow != "context-overflow" {
		t.Fatalf("触发原因是 %q 和 %q", TriggerPressure, TriggerContextOverflow)
	}
}

// 一个现成的 agent 结构上就满足 [Maintainer]，装配方直接填进去即可。
// 这一条是 [Maintainer] 写成单方法接口的全部理由，所以钉在编译期。
var _ Maintainer = agent.Agent(nil)

// idleAgent 是一台只认「空闲期」这一件事的假 agent。
type idleAgent struct {
	// busy 非 nil 时这次认领当场失败。
	busy error
	// ran 记那件活儿有没有真的跑过。
	ran bool
}

func (a *idleAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	if a.busy != nil {
		return a.busy
	}
	a.ran = true
	return task(ctx)
}

func TestManualAgentContext把两半都带上(t *testing.T) {
	t.Parallel()

	live := &idleAgent{}
	// Session 这里给 nil：本用例问的是这个值把哪两半凑在一起，
	// 而不是压缩拿会话干了什么——后者归各个后端。
	target := ManualAgentContext{
		AgentContext: AgentContext{Provider: "openai", Model: "gpt-x"},
		Maintainer:   live,
	}
	// 嵌进来那半得直接读得到，否则每个后端都要多写一层 .AgentContext。
	if target.Provider != "openai" || target.Model != "gpt-x" {
		t.Fatalf("路由没带上：%q / %q", target.Provider, target.Model)
	}

	done := false
	if err := target.Maintainer.RunMaintenance(t.Context(), func(context.Context) error {
		done = true
		return nil
	}); err != nil {
		t.Fatalf("这件活儿该跑起来：%v", err)
	}
	if !live.ran || !done {
		t.Fatal("那条边没接上，活儿压根没进那个 agent")
	}
}

func TestManualAgentContext把占着那条错原样交回去(t *testing.T) {
	t.Parallel()

	// 「已经有回合在驱动」交回来的是 busy，而它正是 [ManualErrorBusy]
	// 那一类失败的来源之一——吞掉它，一次人工压缩就会和一个正在跑的回合并行。
	boom := NewManualError(ManualErrorBusy, "已经有一个回合占着", nil)
	target := ManualAgentContext{Maintainer: &idleAgent{busy: boom}}

	err := target.Maintainer.RunMaintenance(t.Context(), func(context.Context) error {
		t.Fatal("占着的时候不该把活儿放进去")
		return nil
	})
	var manual *ManualError
	if !errors.As(err, &manual) || manual.Code != ManualErrorBusy {
		t.Fatalf("该报占着，实际是 %v", err)
	}
}
