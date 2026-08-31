// 本文件的作用：`/plan` 这条面向用户的命令——它怎么分叉、四种结局各回哪句话、
// 以及跟在后面的那段话为什么走 Steer 而不是当场发给模型。
//
// 源: packages/plan/plan-mode/src/index.ts:294-340

package planmode

import (
	"context"
	"errors"
	"strings"

	"ds-harness-go/core/agent"
	"ds-harness-go/interaction/commands"
	"ds-harness-go/llm"
)

// offArgument 是把计划模式关掉的那个参数。
//
// 源: packages/plan/plan-mode/src/index.ts:302
const offArgument = "off"

// commandDefinition 造 `/plan` 这条命令的定义。
//
// 源: packages/plan/plan-mode/src/index.ts:296-339
//
// 输入是记的（[commands.Definition.SkipInputRecord] 保持零值）：plan 那个投影单元
// 正是从 command/run 里的 args 折出挂起状态的，见 [applyProjection]。
func (c *Controller) commandDefinition() commands.Definition {
	return commands.Definition{
		Name:        CommandName,
		Description: "Enter or leave plan mode",
		Input:       &commands.InputDescriptor{Hint: "[off|message]", Images: true},
		Handler:     c.runCommand,
	}
}

// runCommand 跑一次 `/plan`。
//
// 源: packages/plan/plan-mode/src/index.ts:300-338
//
// 这些回话是给人看的，所以每一种结局都单独有一句：「已经关了」和「正在关，下一步
// 生效」对用户是两件事，合成一句会让他以为自己那一下没按上。
func (c *Controller) runCommand(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
	if invocation.Agent == nil {
		return commands.Result{}, errors.New("planmode: /plan 需要一个发起这条命令的 agent")
	}
	// 新增: DSH 的处理器直接收到那个 agent 对象。Go 这边它是一把不透明的作用域键，
	// 所以走 [Config.AgentOf]。认不出这把钥匙时返回错误而不是一个错误结果：那不是
	// 用户能改的事（他敲的这条命令本身没毛病），是装配没接对，该一路抛给调用方。
	target, err := c.agentOf(invocation.Agent)
	if err != nil {
		return commands.Result{}, err
	}
	if target == nil {
		return commands.Result{}, errors.New("planmode: /plan 找不到发起这条命令的 agent")
	}

	message := strings.TrimSpace(invocation.RawInput)
	if message == offArgument && len(invocation.Attachments) > 0 {
		// 图留在编辑器里：一次退出用不上它们，而把它们默默丢掉等于替用户扔东西。
		return commands.Result{
			Kind: commands.ResultError,
			Text: "Image attachments cannot accompany /plan off.",
		}, nil
	}

	if message == offArgument {
		outcome, err := c.Set(target, false)
		if err != nil {
			return commands.Result{}, err
		}
		return commands.Result{Kind: commands.ResultSuccess, Text: offText(target, outcome)}, nil
	}

	outcome, err := c.Set(target, true)
	if err != nil {
		return commands.Result{}, err
	}
	if message != "" || len(invocation.Attachments) > 0 {
		// 走 Steer 而不是 Send：这段话是跟着这次模式切换一起来的同一个意图，它该在
		// 下一步就被读到，而不是排到收件箱后面去。
		target.Steer(llm.NewUserMessage(commandContent(message, invocation.Attachments), llm.UserSource{}))
	}
	text := "Entering plan mode (applies from the next step). Use /plan off to leave."
	if outcome == OutcomeCommitted {
		text = "Plan mode on. Use /plan off to leave."
	}
	return commands.Result{Kind: commands.ResultSuccess, Text: text}, nil
}

// offText 挑一次 `/plan off` 该回哪句话。
//
// 源: packages/plan/plan-mode/src/index.ts:306-320
//
// [OutcomeNoop] 那一支要再查一次日志：一次已经挂起的退出会让 Set 判成空操作
// （想要的状态和挂起的那个一样），但那时日志上仍然开着，回「已经关了」是错的。
// 只有一个日志上就关着的会话才读作幂等。
func offText(target agent.Agent, outcome Outcome) string {
	switch outcome {
	case OutcomeCommitted:
		return "Plan mode off."
	case OutcomeQueued:
		return "Leaving plan mode (applies from the next step)."
	case OutcomeCancelled:
		return "Plan mode entry cancelled."
	default:
		if FoldMode(target.Session().Events()) {
			return "Leaving plan mode (applies from the next step)."
		}
		return "Plan mode is already inactive."
	}
}

// commandContent 把附件和那段话拼成一条用户消息的内容。
//
// 源: packages/plan/plan-mode/src/index.ts:325-328
//
// 图在前、文字在后，和 DSH 同序：那段话通常是在说这些图。
func commandContent(message string, attachments []llm.ImageBlock) llm.Content {
	content := make(llm.Content, 0, len(attachments)+1)
	for _, attachment := range attachments {
		content = append(content, attachment)
	}
	if message != "" {
		content = append(content, llm.TextBlock{Text: message})
	}
	return content
}
