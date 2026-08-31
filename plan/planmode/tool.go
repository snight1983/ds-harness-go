// 本文件的作用：那件一直挂在工具表上的退出工具——它在动手之前要过哪几道门、
// 一次评审回来之后怎么读、以及为什么「同意」并不当场落盘。
//
// 源: packages/plan/plan-mode/src/index.ts:342-430

package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"ds-harness-go/core/tools"
	"ds-harness-go/interaction/userquestions"
	"ds-harness-go/llm"
)

// reviewID 是那道评审问题的标识，答案里按它归位。
//
// 源: packages/plan/plan-mode/src/index.ts:64
const reviewID = "plan-review"

// approveLabel 是「同意这份计划」的那个选项标签。
//
// 源: packages/plan/plan-mode/src/index.ts:65
//
// 它同时是回传的标识（见 [userquestions.Option.Label]），也是
// [userquestions.PlanReviewIntent.Approve] 指过去的那个标签。所以它只能有一处
// 定义：两边写岔了的话，界面会把一次同意画成同意、而这里读成「继续规划」。
const approveLabel = "Approve"

// keepPlanningLabel 是「留在计划模式里」的那个选项标签。
//
// 源: packages/plan/plan-mode/src/index.ts:66
const keepPlanningLabel = "Keep planning"

// exitDescription 是给模型看的那段工具说明。
//
// 源: packages/plan/plan-mode/src/index.ts:74-78
//
// 它是面向模型的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线。
const exitDescription = "Use only in plan mode. Present your plan for the user's review and, on approval, leave plan mode. " +
	"Send the COMPLETE plan as markdown, starting with a # heading that names it. " +
	"The user may approve (carry out the plan from your next step) or keep " +
	"planning — their feedback comes back in the tool result; revise and present again."

// planHeadingPattern 要求这份计划以一个一级标题开头，而且那个标题得有正文。
//
// 源: packages/plan/plan-mode/src/index.ts:364
//
// 它比 [headingPattern] 严：那一个是「拿标题来做卡片名」，从一到六级里挑第一条；
// 这一个是准入门槛，只认开头那条 `# `。一份从二级标题起头的计划是模型没有照说明
// 写，当场退回去比让它进评审好——用户会看见一张标题空着的卡片。
var planHeadingPattern = regexp.MustCompile(`^#\s+\S`)

// exitArgs 是模型写出来的参数。
type exitArgs struct {
	// Plan 是那份完整的 markdown 计划。
	Plan string `json:"plan"`
}

// exitValue 是这件工具那份权威的返回值。
//
// 源: packages/plan/plan-mode/src/index.ts:348-357
//
// 只有 approved 一个字段，而且 schema 上钉死是 true：这件工具唯一的成功走向就是
// 被同意，「继续规划」走的是错误那条路（模型要从结果里读到用户的反馈）。
type exitValue struct {
	Approved bool `json:"approved"`
}

// exitFalse 是 [tools.Node.AdditionalProperties] 要的那个「显式的 false」。
var exitFalse = false

// exitTrue 是输出 schema 里 approved 那个 const 的字面量。
var exitTrue = json.RawMessage("true")

// exitDefinition 造这件退出工具的定义。
//
// 源: packages/plan/plan-mode/src/index.ts:342-430
func (c *Controller) exitDefinition() *tools.Definition {
	return &tools.Definition{
		Name:        ExitToolName,
		Description: exitDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name: "plan",
				Schema: tools.Node{
					Type:        tools.TypeString,
					Description: "The complete plan, as markdown, starting with a # heading that names it.",
				},
			}},
			Required: []string{"plan"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type:                 tools.TypeObject,
				AdditionalProperties: &exitFalse,
				Properties: []tools.Property{{
					Name:   "approved",
					Schema: tools.Node{Type: tools.TypeBoolean, Const: exitTrue},
				}},
				Required: []string{"approved"},
			},
			Render: renderExit,
		},
		Execute:       c.executeExit,
		PresentCall:   presentExitCall,
		PresentResult: presentExitResult,
	}
}

// renderExit 把那份值折成给模型看的一句话。
//
// 源: packages/plan/plan-mode/src/index.ts:356
//
// 不看值：能走到这里的值一定满足上面那份 schema，approved 只可能是 true。
func renderExit(_ json.RawMessage, _ json.RawMessage) (llm.Content, error) {
	return llm.Content{llm.TextBlock{
		Text: "Plan approved — plan mode exited; carry out the plan starting with your next step.",
	}}, nil
}

// presentExitCall 是这次调用进行中在界面上的样子：整份计划本身。
//
// 源: packages/plan/plan-mode/src/index.ts:419-424
//
// 它必须是纯函数（实时流式和会话重放都会调它），所以只看 args。参数读不回来时
// 交出一张只有默认标题的卡片，绝不去碰别的地方。
func presentExitCall(args json.RawMessage) tools.CallView {
	view := tools.GenericCallView{Title: "Plan", Kind: tools.CallOther}
	var decoded exitArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return view
	}
	if heading := firstHeading(decoded.Plan); heading != "" {
		view.Title = heading
	}
	view.Content = llm.Content{llm.TextBlock{Text: decoded.Plan}}
	return view
}

// presentExitResult 是这次调用完成之后在界面上的样子。
//
// 源: packages/plan/plan-mode/src/index.ts:425-429
//
// 成功和失败共用同一张卡片：一次「继续规划」走的是错误那条路，但它在界面上仍然是
// 这次评审的结局，画成一张红色的通用错误卡片会让用户以为出了故障。
func presentExitResult(_ json.RawMessage, result tools.PresentedResult) tools.ResultView {
	return tools.GenericResultView{Title: "Plan review", Content: result.Content}
}

// executeExit 跑一次已经放行的调用：把计划摆给用户，等他裁决。
//
// 源: packages/plan/plan-mode/src/index.ts:358-418
//
// 五道门按 DSH 的顺序过，一道都不合并：每一道对应模型的一种不同错误，报出去的那句
// 话是模型下一步唯一的依据。
func (c *Controller) executeExit(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
	var decoded exitArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return nil, err
	}
	if exec.Agent == nil {
		return nil, fmt.Errorf("%s requires a calling agent (no session to switch)", ExitToolName)
	}
	// 新增: DSH 从 exec.agent 这个 agent 对象上直接摸到会话。Go 这边它是一把不透明的
	// 作用域键，所以走 [Config.AgentOf]。认不出这把钥匙和「没有调用方」是同一件事：
	// 两种情形下都没有可切换的会话。
	target, err := c.agentOf(exec.Agent)
	if err != nil || target == nil {
		return nil, fmt.Errorf("%s requires a calling agent (no session to switch)", ExitToolName)
	}
	sess := target.Session()
	if !FoldMode(sess.Events()) {
		return nil, fmt.Errorf("%s is only available in plan mode", ExitToolName)
	}
	if !planHeadingPattern.MatchString(strings.TrimSpace(decoded.Plan)) {
		return nil, fmt.Errorf("%s requires a non-empty markdown plan starting with a # heading", ExitToolName)
	}
	// 提问服务是调用这一刻才求的，不是注册那一刻：工具表在进出计划模式时必须纹丝不动
	// （见 [ExitToolName] 的注释），所以「有没有人能评审」只能推到这里来问。
	questions := c.question.Load()
	if questions == nil {
		return nil, errors.New("no user-questions channel is available to review the plan; " +
			"ask the user to switch the session mode instead")
	}

	answer, err := questions.Ask(ctx, userquestions.Request{
		Questions: []userquestions.Item{{
			ID:       reviewID,
			Header:   "Plan review",
			Question: "Approve this plan and leave plan mode?",
			Detail:   decoded.Plan,
			Options: []userquestions.Option{
				{Label: approveLabel, Description: "Leave plan mode; the plan is carried out from the next step."},
				{Label: keepPlanningLabel, Description: "Stay in plan mode; feedback goes back to the model."},
			},
			// 只改呈现：认得这个标记的界面把它画成一次计划裁决而不是一串通用选项，
			// 两边回来的答案编码一模一样。
			Intent: userquestions.PlanReviewIntent{Approve: approveLabel},
		}},
		Agent: exec.Agent,
	})
	if err != nil {
		return nil, translateAskError(err)
	}
	// 一次评审可能活得比这次装配还长。没有了那条步骤前置，一个被同意的选择就再也
	// 没有机会落进日志，所以这里失败掉、让模型再摆一次。
	if c.disposed.Load() {
		return nil, errors.New("the plan-mode service was reloaded while the plan was under review; " +
			"present the plan again")
	}
	if err := checkApproval(answer); err != nil {
		return nil, err
	}
	// 计划指引在这一批工具调用剩下的部分里继续有效。这次静默的选择会在下一个被接受的、
	// 回合之内的步骤前置上落盘，就在那次请求装配之前。
	c.setPending(sess.ID(), pendingIntent{active: false, narrate: false})
	return json.Marshal(exitValue{Approved: true})
}

// translateAskError 把一次被撤掉的评审翻译成模型读得懂的一句话。
//
// 源: packages/plan/plan-mode/src/index.ts:388-399
//
// 一次被撤掉的评审不是一次失败的评审：用户把发言权收回去，是要说那两个选项覆盖不了
// 的话。这里必须说清楚，因为通用那句话里点的是 ask_user_question 这个名字，而模型
// 从来没调过它。中断（回合取消、上游拆掉）保留它自己那句话——那种情形下没有人在等。
func translateAskError(err error) error {
	var questionErr *userquestions.Error
	if errors.As(err, &questionErr) && questionErr.Code == userquestions.CodeAskCancelled {
		return errors.New("The user dismissed the plan review to speak instead; " +
			"stay in plan mode, stop here, and wait for their message.")
	}
	return err
}

// checkApproval 判这份答案算不算一次干干净净的同意；不算时交回那句要给模型看的话。
//
// 源: packages/plan/plan-mode/src/index.ts:405-412
//
// 「同意」的定义很窄：恰好一条属于这道评审的回答、恰好选中一个标签、那个标签就是
// [approveLabel]、而且自由文本那栏是空的。带着一句话的同意仍然按「继续规划」办——
// 用户写下那句话就是要模型先读它，把它连同一次退出一起吞掉是最坏的一种误读。
//
// 新增: DSH 查的是 `custom !== undefined`，所以一个显式的空串也算「写了东西」。
// Go 的 [userquestions.AnswerItem.Custom] 是带 omitempty 的 string，空串和「没写」
// 在介质上就是同一件事，这里只能查空串。差别只落在「选了同意、又留下一句空白反馈」
// 这一种界面上做不出来的答案上。
func checkApproval(answer userquestions.Answer) error {
	var item *userquestions.AnswerItem
	for index := range answer.Answers {
		if answer.Answers[index].ID != reviewID {
			continue
		}
		if item != nil {
			// 同一道评审回来两条：认不出哪一条算数，按没同意办。
			item = nil
			break
		}
		item = &answer.Answers[index]
	}
	approved := item != nil &&
		len(item.Selected) == 1 &&
		item.Selected[0] == approveLabel &&
		item.Custom == ""
	if approved {
		return nil
	}
	feedback := ""
	if item != nil {
		feedback = item.Custom
	}
	if feedback == "" {
		return errors.New("The user chose to keep planning; revise the plan and present it again.")
	}
	return fmt.Errorf("The user chose to keep planning; their feedback: %s", feedback)
}
