// 本文件的作用：验这副脚手架凑出来的那五样确实**够装一个循环**，而且它自己
// 刻意没装的那几处（造法、工具、适配器）真的是空的。

package harnesstest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agentloop"
	"github.com/snight1983/ds-harness-go/harness/harnesstest"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/scope"
)

// TestMountedSpineActivatesLoop 是本包存在的理由那一条：凑出来的东西装得起一个循环。
//
// 源: packages/test-support/agent-loop-testkit/tests/agent-loop-testkit.spec.ts:8-19
func TestMountedSpineActivatesLoop(t *testing.T) {
	deps := harnesstest.Mount(t, harnesstest.Options{
		SystemPrompt: systemprompt.Options{
			OmitHarnessIdentity: true,
			Persona:             "这是一份用来验装配的人设。",
		},
	})

	assembly, err := deps.SystemPrompt.Assemble(context.Background(), systemprompt.AssembleContext{})
	if err != nil {
		t.Fatalf("装系统提示词失败：%v", err)
	}
	rendered, err := systemprompt.RenderPrompt(assembly)
	if err != nil {
		t.Fatalf("渲染系统提示词失败：%v", err)
	}
	if !strings.Contains(rendered, "这是一份用来验装配的人设。") {
		t.Fatalf("人设没进提示词：%q", rendered)
	}

	loop, unwind, err := agentloop.New(context.Background(), agentloop.Deps{
		Agents:       deps.Agents,
		Sessions:     deps.Sessions,
		LLM:          deps.Models,
		Tools:        deps.Tools,
		SystemPrompt: deps.SystemPrompt,
	}, deps.Scope, agentloop.Config{})
	if err != nil {
		t.Fatalf("装循环失败：%v", err)
	}
	if loop == nil {
		t.Fatal("装出来的循环是 nil")
	}
	if err := unwind(context.Background()); err != nil {
		t.Fatalf("拆循环失败：%v", err)
	}
}

// TestMountLeavesFactorySlotOpen 验它没有替用例把造法登记掉——那一格空着，
// 正是这副脚手架和 harness.New 的分界。
func TestMountLeavesFactorySlotOpen(t *testing.T) {
	deps := harnesstest.Mount(t, harnesstest.Options{})

	// 装第一个循环应当成功：造法那一格是空的。
	_, unwind, err := agentloop.New(context.Background(), agentloop.Deps{
		Agents:       deps.Agents,
		Sessions:     deps.Sessions,
		LLM:          deps.Models,
		Tools:        deps.Tools,
		SystemPrompt: deps.SystemPrompt,
	}, deps.Scope, agentloop.Config{})
	if err != nil {
		t.Fatalf("装第一个循环失败：%v", err)
	}
	t.Cleanup(func() { _ = unwind(context.Background()) })

	// 装第二个应当撞上「已经登记过一个造法」——这条断言同时证明第一次那一格
	// 本来是空的。
	if _, _, err := agentloop.New(context.Background(), agentloop.Deps{
		Agents:       deps.Agents,
		Sessions:     deps.Sessions,
		LLM:          deps.Models,
		Tools:        deps.Tools,
		SystemPrompt: deps.SystemPrompt,
	}, deps.Scope, agentloop.Config{}); err == nil {
		t.Fatal("同一个注册表上装了两个循环却没报错")
	}
}

// TestBorrowedScopeIsNotDisposed 验用例自己交进来的作用域不会被本包拆掉：
// 那个作用域的生死归交它进来的人管。
func TestBorrowedScopeIsNotDisposed(t *testing.T) {
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })

	deps, err := harnesstest.New(context.Background(), harnesstest.Options{Owner: owner})
	if err != nil {
		t.Fatalf("凑先决依赖失败：%v", err)
	}
	if deps.Scope != owner {
		t.Fatal("交进来的作用域没有被用上")
	}
	if err := deps.Dispose(context.Background()); err != nil {
		t.Fatalf("拆先决依赖失败：%v", err)
	}

	// 作用域还活着才登记得上东西。
	if _, err := deps.SystemPrompt.OnAssemble(context.Background(), owner,
		func(
			_ context.Context,
			assembly systemprompt.PromptAssembly,
			_ systemprompt.AssembleContext,
			next func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
		) (systemprompt.PromptAssembly, error) {
			return next(assembly)
		}); err != nil {
		t.Fatalf("拆完之后交进来的那个作用域不能用了：%v", err)
	}
}

// TestDisposeIsIdempotent 验重复拆是空操作——Mount 挂的那次 Cleanup 和用例
// 自己那次常常都会跑到。
func TestDisposeIsIdempotent(t *testing.T) {
	deps, err := harnesstest.New(context.Background(), harnesstest.Options{})
	if err != nil {
		t.Fatalf("凑先决依赖失败：%v", err)
	}
	if err := deps.Dispose(context.Background()); err != nil {
		t.Fatalf("第一次拆失败：%v", err)
	}
	if err := deps.Dispose(context.Background()); err != nil {
		t.Fatalf("第二次拆报了错：%v", err)
	}
}
