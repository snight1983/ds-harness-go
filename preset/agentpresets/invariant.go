// 本文件的作用：这个包自己拥有的那条持久不变量——一套组装了名册的部署，它的每一个
// agent 都必须是从名册里组装出来的；以及为什么 DSH 那条「有没有服务漏进根领域」
// 在 Go 里是一条写明了理由的空检查。
//
// 源: packages/preset/agent-presets/src/invariant.ts

package agentpresets

import (
	"context"
	"fmt"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/systemprompt"
	"ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/preset/agent-presets/src/invariant.ts:17
const PackageName = "@deepseek-ai/dsh-agent-presets"

// AssemblingAgent 说一次系统提示词装配到底是不是**一个 agent 在对模型说话**。
//
// 新增: DSH 那条检查读的是 `context.agent`——cordis 的装配上下文被 dsh-agent 用
// declare module 合进了一个 agent 字段。Go 的 [systemprompt.AssembleContext] 只有
// 一把作用域键（没有声明合并），所以这件事由装配方以函数交进来，做法和
// [ds-harness-go/plan/planmode.RegisterInvariants] 收 loaded/subscribe 相同。
//
// 两个条件各自都承重。「是不是 agent」承重，是因为一次**只有作用域**的装配——一次
// 在常驻键里解算呈现器的冷读、一次诊断——本来就不是 agent，不该拿「你认了谁」去判它。
// 而「装配」而不是「发布」才是要紧的那一刻，是因为一个没认预设的 agent 在它**对模型
// 说话之前**都是合法的：[Roster.Recompose] 就是把一个光身 agent 认进去的第一次绑定，
// 那个 agent 在那次调换之前的一辈子都没认过谁。
//
// 交回的 id 只进诊断文本。isAgent 为假时 id 被忽略。
type AssemblingAgent func(assemble systemprompt.AssembleContext) (id string, isAgent bool)

// checkJoined 判一次装配违不违反「组装了名册就得从名册里组装 agent」这条。
//
// 源: packages/preset/agent-presets/src/invariant.ts:60-71
//
// 一个一份预设都没认的 agent，它的 tools、system-prompt、skill 全都落在**空的全局层**
// 上解算，于是模型什么都收不到。[Roster.ComposedPreset] 是名册自己对「这个 agent 认
// 了没有」的回答，从活的作用域链上读。
//
// 交回空串表示这一次没有违例。
func checkJoined(roster *Roster, agentID string, agentKey *scope.Key) string {
	if roster == nil || len(roster.roots) == 0 {
		return ""
	}
	if roster.ComposedPreset(agentKey) != "" {
		return ""
	}
	return fmt.Sprintf(
		"agent %q addressed a model without joining any agent preset while a roster is "+
			"composed; its tools, prompt sections, and skill catalog resolve against the empty global layer",
		agentID)
}

// RegisterInvariants 装上这个包那条检查，返回注销函数。
//
// 源: packages/preset/agent-presets/src/invariant.ts:33-80
//
// # 没有照抄的那一条
//
// **新增: DSH 的 leakedServices 那条检查在 Go 里违反不了，所以这里没有它。** 那条查的是
// cordis 的服务领域：一行插件把服务发布进**根领域**就变成了进程级的，第二个会话装
// 同一份预设时会撞上第一个；而 cordis 的根领域是隐式的，一行插件不必拿到任何东西就
// 够得着它，所以那条只能靠反射审计 ctx.reflect.store 才查得出来。Go 这边一次登记要
// 显式地把持有者作用域**作为参数传进去**（`Register(ctx, owner, ...)`），一个
// [Composer] 手上只有它那个常驻作用域，**在结构上够不着根**。这不是漏掉的一条，是
// 一条被类型系统提前证掉的。
//
// registry 是不变量注册表，roster 是要判的那份名册，agentOf 见 [AssemblingAgent]，
// prompts 是这条规则挂上去的那台提示词注册表。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	roster *Roster,
	prompts *systemprompt.Registry,
	owner *scope.Scope,
	agentOf AssemblingAgent,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一个不变量注册表", ErrInvalidConfig)
	}
	if roster == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一份要判的名册", ErrInvalidConfig)
	}
	if prompts == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一台提示词注册表来挂那条装配规则", ErrInvalidConfig)
	}
	if owner == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一个持有那条装配规则的作用域", ErrInvalidConfig)
	}
	if agentOf == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一条判「这次装配是不是 agent」的路", ErrInvalidConfig)
	}

	install := func(installCtx context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		rule := func(
			_ context.Context,
			assembly systemprompt.PromptAssembly,
			assemble systemprompt.AssembleContext,
			next func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
		) (systemprompt.PromptAssembly, error) {
			if id, isAgent := agentOf(assemble); isAgent {
				if violation := checkJoined(roster, id, assemble.Scope); violation != "" {
					fail(violation)
				}
			}
			return next(assembly)
		}
		remove, err := prompts.OnAssemble(installCtx, owner, rule)
		if err != nil {
			return err
		}
		scope.Defer(func() {
			// 注销之后，一条不该再查的检查绝不许继续在别人的装配路上抛。
			_ = remove(context.WithoutCancel(installCtx))
		})
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
