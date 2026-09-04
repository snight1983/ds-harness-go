// 本文件的作用：`/goal` 那一行字怎么断句、七种意思各自走哪条路、以及一次被目标
// 服务按状态拒掉的调用怎么回话。
//
// 源: packages/goal/command-goal/src/index.ts:15-44, 108-186

package goalcommand

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
)

// usage 是那句贴在几条错误后面的语法提示。
//
// 源: packages/goal/command-goal/src/index.ts:15
const usage = "Usage: /goal [<objective>|clear|edit <objective>|pause|resume]"

// attachmentRole 是跟在那几张图后面、写明它们身份的那句话。
//
// 源: packages/goal/command-goal/src/index.ts:119
//
// 它必须在，而且必须是文字：之后的目标轮次是从普通会话历史里读到这些图的，
// 没有这句话，模型只看见几张不知道从哪儿冒出来的图。
const attachmentRole = "Reference images for the goal objective."

// stateRejection 是一次「这条命令在当前状态下不成立」的回话。
//
// 源: packages/goal/command-goal/src/index.ts:180
//
// 它刻意笼统：具体是修订号过时了、还是阶段不对，是这一层和那台目标服务之间的事。
// 对人来说下一步都一样——敲一次 /goal 看看现在能干什么。
const stateRejection = "The goal command is not valid for the current state. Run /goal to view available commands."

// commandKind 是 `/goal` 认识的那七种意思。
//
// 源: packages/goal/command-goal/src/index.ts:17-25
type commandKind string

const (
	// kindShow 是看一眼当前目标。
	kindShow commandKind = "show"
	// kindCreate 是建一个新目标。
	kindCreate commandKind = "create"
	// kindEdit 是换掉当前目标的描述。
	kindEdit commandKind = "edit"
	// kindInvalidEdit 是光敲了 edit 却没给新目标。
	kindInvalidEdit commandKind = "invalid-edit"
	// kindPause 是把当前目标停下。
	kindPause commandKind = "pause"
	// kindResume 是把当前目标重新推起来。
	kindResume commandKind = "resume"
	// kindClear 是清掉当前目标。
	kindClear commandKind = "clear"
)

// parsedCommand 是断完句的那一行字。
//
// 源: packages/goal/command-goal/src/index.ts:17-25
//
// 新增: DSH 是一个可辨识联合，只有 create 和 edit 那两支带 objective。Go 这边合成
// 一个结构体：那个联合在 TS 里的作用是「别的分支上读不到 objective」，而这里
// 读它的只有本文件里那两条 case，多一个字段换不来任何真实的错误。
type parsedCommand struct {
	// kind 是这一行字的意思。
	kind commandKind
	// objective 是 create 和 edit 那两支带上来的新目标；别的支是空串。
	objective string
}

// parseCommand 只断 `/goal` 自己那点语法，别的任何一行字都当成一个新目标。
//
// 源: packages/goal/command-goal/src/index.ts:33-44
//
// 「别的都当目标」是刻意的：人敲 `/goal 把测试补齐` 的时候不该被要求先记住一个
// 子命令表。代价是「目标」这个位置上永远拿不到 `clear` 这类词——那正是它们被
// 收进这张表的原因。
//
// 那四个控制词大小写不敏感（DSH 先 toLowerCase 再比），而目标本身一个字都不动。
//
// 新增: 「空白」在这里是 [unicode.IsSpace]，而 DSH 用的是 JS 正则的 `\s`。两边
// 只在 U+0085 和 U+FEFF 这两个字上有出入，而它们都不是人会敲在 `edit` 后面的
// 分隔符；换成 Go 自己的那套是为了跟 [strings.TrimSpace] 用同一个口径，否则会出现
// 「trim 掉了但断句时不算空白」这种自相矛盾。
func parseCommand(rawInput string) parsedCommand {
	input := strings.TrimSpace(rawInput)
	if input == "" {
		return parsedCommand{kind: kindShow}
	}
	switch strings.ToLower(input) {
	case "clear":
		return parsedCommand{kind: kindClear}
	case "pause":
		return parsedCommand{kind: kindPause}
	case "resume":
		return parsedCommand{kind: kindResume}
	case "edit":
		return parsedCommand{kind: kindInvalidEdit}
	}
	if rest, ok := afterEdit(input); ok {
		return parsedCommand{kind: kindEdit, objective: rest}
	}
	return parsedCommand{kind: kindCreate, objective: input}
}

// afterEdit 说明这一行字是不是 `edit` 加空白开头，是的话交出后面那段。
//
// 源: packages/goal/command-goal/src/index.ts:42（/^edit(?=\s)/iu）
//
// 交出来的那段一定非空：进来的 input 已经 trim 过，所以 `edit` 后面既然还有空白，
// 空白后面就一定还有字——`edit   ` 那种在上一步就被 [strings.TrimSpace] 削成了
// `edit`，走的是 [kindInvalidEdit]。
func afterEdit(input string) (string, bool) {
	const prefix = "edit"
	if len(input) <= len(prefix) || !strings.EqualFold(input[:len(prefix)], prefix) {
		return "", false
	}
	separator, _ := utf8.DecodeRuneInString(input[len(prefix):])
	if !unicode.IsSpace(separator) {
		return "", false
	}
	return strings.TrimSpace(input[len(prefix):]), true
}

// run 跑一次 `/goal`。
//
// 源: packages/goal/command-goal/src/index.ts:126-186
//
// 目标服务按状态拒掉的那一类失败折成一个**错误结果**（DSH 那边是 catch 住
// GoalError），别的失败原样抛给调用方——那说明装配或者会话日志出了事，不是人敲错了。
func (c *Controller) run(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
	if invocation.Agent == nil {
		return commands.Result{}, errors.New("goalcommand: /goal 需要一个发起这条命令的 agent")
	}
	target, err := c.agentOf(invocation.Agent)
	if err != nil {
		return commands.Result{}, err
	}
	if target == nil {
		return commands.Result{}, errors.New("goalcommand: /goal 找不到发起这条命令的 agent")
	}

	command := parseCommand(invocation.RawInput)
	if len(invocation.Attachments) > 0 && command.kind != kindCreate && command.kind != kindEdit {
		// 图留在编辑器里：这几条子命令用不上它们，而把它们默默丢掉等于替用户扔东西。
		return commands.Result{
			Kind: commands.ResultError,
			Text: "Image attachments only accompany a goal objective: /goal <objective> or /goal edit <objective>.",
		}, nil
	}

	result, err := c.dispatch(target, command, invocation.Attachments)
	if err != nil {
		var rejected *goal.Error
		if errors.As(err, &rejected) {
			return commands.Result{Kind: commands.ResultError, Text: stateRejection}, nil
		}
		return commands.Result{}, err
	}
	return result, nil
}

// dispatch 把一条断好句的命令交给那台拥有持久化的服务去办。
//
// 源: packages/goal/command-goal/src/index.ts:133-184
func (c *Controller) dispatch(
	target agent.Agent,
	command parsedCommand,
	attachments []llm.ImageBlock,
) (commands.Result, error) {
	current, err := c.service.Get(target)
	if err != nil {
		return commands.Result{}, err
	}
	switch command.kind {
	case kindShow:
		if current == nil {
			return commands.Result{
				Kind: commands.ResultSuccess,
				Text: "No goal is currently set.\n" + usage,
			}, nil
		}
		return renderGoal("Goal", current)

	case kindInvalidEdit:
		return commands.Result{
			Kind: commands.ResultError,
			Text: "Goal editing requires a replacement objective.\n" + usage,
		}, nil

	case kindCreate:
		if current != nil && current.Phase != goal.PhaseComplete {
			return commands.Result{
				Kind: commands.ResultError,
				Text: fmt.Sprintf(
					"A goal is already %s. Use /goal edit <objective> to change it or /goal clear before replacing it.",
					current.Phase),
			}, nil
		}
		return c.createGoal(target, command.objective, attachments)

	case kindEdit:
		if current == nil {
			return missingGoal("edit"), nil
		}
		// 已完成的那一个不走 edit 走 create：目标域不准改一个已完成的目标，而人敲
		// `/goal edit <新目标>` 的意思显然是「换一个新的接着干」。
		if current.Phase == goal.PhaseComplete {
			return c.createGoal(target, command.objective, attachments)
		}
		edited, err := c.service.Edit(target, current.Ref, goal.EditRequest{Objective: &command.objective})
		if err != nil {
			return commands.Result{}, err
		}
		submitAttachments(target, attachments)
		return renderGoal("Goal updated", edited)

	case kindPause:
		if current == nil {
			return missingGoal("pause"), nil
		}
		paused, err := c.service.Pause(target, current.Ref)
		if err != nil {
			return commands.Result{}, err
		}
		return renderGoal("Goal paused", paused)

	case kindResume:
		if current == nil {
			return missingGoal("resume"), nil
		}
		resumed, err := c.service.Resume(target, current.Ref)
		if err != nil {
			return commands.Result{}, err
		}
		return renderGoal("Goal resumed", resumed)

	case kindClear:
		if current == nil {
			return commands.Result{Kind: commands.ResultSuccess, Text: "No goal to clear."}, nil
		}
		if _, err := c.service.Clear(target, current.Ref); err != nil {
			return commands.Result{}, err
		}
		return commands.Result{Kind: commands.ResultSuccess, Text: "Goal cleared."}, nil
	}
	// 新增: DSH 这里是 assertNever——TS 的封闭联合让编译器保证走不到。Go 的
	// [commandKind] 是具名字符串类型，挡不住，所以留一句大声失败而不是悄悄
	// 交回一个零值结果。只有 [parseCommand] 造得出这个值，所以这条路真的走不到。
	return commands.Result{}, fmt.Errorf("goalcommand: 认不出的 /goal 意思 %q", command.kind)
}

// createGoal 建一个目标、把图发进去、再把结果排出来。
//
// 源: packages/goal/command-goal/src/index.ts:145-149, 154-158
//
// 新增: DSH 这三步在 create 和「换掉一个已完成目标」两支里各写了一遍。合成一个
// 函数不改变行为：那两支交回的标题本来就都是 'Goal created'。
func (c *Controller) createGoal(
	target agent.Agent,
	objective string,
	attachments []llm.ImageBlock,
) (commands.Result, error) {
	created, err := c.service.Create(target, goal.CreateRequest{Objective: objective})
	if err != nil {
		return commands.Result{}, err
	}
	submitAttachments(target, attachments)
	return renderGoal("Goal created", created)
}

// submitAttachments 把这次调用准入的那几张图作为一条普通的用户消息发进会话。
//
// 源: packages/goal/command-goal/src/index.ts:112-121
//
// 走 [agent.Agent.Followup] 而不是把它们存进目标域：之后的目标轮次是从普通会话
// 历史里读到这些图的，目标域因此一个字节的附件状态都不必存。图在前、那句写明
// 身份的话在后，和 DSH 同序。
func submitAttachments(target agent.Agent, attachments []llm.ImageBlock) {
	if len(attachments) == 0 {
		return
	}
	content := make(llm.Content, 0, len(attachments)+1)
	for _, attachment := range attachments {
		content = append(content, attachment)
	}
	content = append(content, llm.TextBlock{Text: attachmentRole})
	target.Followup(llm.NewUserMessage(content, llm.UserSource{}))
}
