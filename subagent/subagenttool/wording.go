// 本文件的作用：给模型看的那几段字——工具说明、参数说明、那段后台指引，
// 以及它们怎么跟着提供方和配置变。
//
// 源: packages/subagent/tool-subagent/src/index.ts:208-245, 306-334, 464-475
//
// 这些字是**模型的输入**，所以一律是英文；本仓库的注释和给人看的错误一律中文。

package subagenttool

// wording 是一次装配定下来的那两段措辞。
//
// 源: packages/subagent/tool-subagent/src/index.ts:220
type wording struct {
	// description 是那件工具自己的说明。
	description string
	// promptDescription 是 prompt 那个参数的说明。
	promptDescription string
}

// 这两组字的差别只有一件事：孩子看不看得见这段对话里那些已完成的回合。
//
// 源: packages/subagent/tool-subagent/src/index.ts:221-244
const (
	inheritingDescription = "Delegate a task to a subagent that inherits this conversation: a child agent " +
		"seeded with all completed turns so far (it does not see the current in-flight turn). Use this when " +
		"the subtask builds on this conversation's context — a follow-up analysis, a review, a continuation — " +
		"without consuming this conversation's context for the work itself. You receive its result, not its " +
		"intermediate steps."
	inheritingPromptDescription = "The task for the subagent. It already sees this conversation's completed " +
		"turns, so build on them freely and state only what is new."

	standaloneDescription = "Delegate a self-contained task to a subagent (a separate agent that works in " +
		"its own context) to offload focused, independent work — research, a scoped implementation, an " +
		"analysis — so it does not consume this conversation's context. The subagent returns its result, " +
		"not its intermediate steps. Give it a complete, standalone prompt: it does not see this conversation."
	standalonePromptDescription = "The complete, self-contained task for the subagent. It does not share " +
		"this conversation's context, so include everything it needs."
)

// 这三句接在工具说明后面，说清这一次调用默认走哪条路、结果从哪儿拿。
//
// 源: packages/subagent/tool-subagent/src/index.ts:313-315
const (
	continuableSuffix = " This tool runs in the background by default, immediately returns a durable " +
		"subagent id, and keeps the child conversation available for later turns. When that run settles, " +
		"the runtime sends the parent a notice containing its outcome and any final assistant message; " +
		"`send_message` starts a later turn in the same child conversation. Set `run_in_background: false` " +
		"only when your next action depends on receiving the result."
	oneShotSuffix = " This call waits for the result by default. Set `run_in_background: true` to return a " +
		"job id; collect with `job_output` and stop with `job_kill`."
	foregroundOnlySuffix = " This call waits for the subagent and returns its result."
)

// 这两句是 run_in_background 那个参数的说明，跟着后台走法变。
//
// 源: packages/subagent/tool-subagent/src/index.ts:330-332
const (
	continuableBackgroundDescription = "Whether to run in the background and return a durable subagent id " +
		"immediately. Defaults to true. Set false to wait for the result when your next action depends on it."
	oneShotBackgroundDescription = "Whether to run as a background job and return its id. Defaults to " +
		"false; collect with job_output or stop with job_kill."
)

// descriptionDescription 是 description 那个参数的说明。
//
// 源: packages/subagent/tool-subagent/src/index.ts:320
const descriptionDescription = "A short (3-5 word) description of the delegated task, for display."

// providerWording 按提供方那句「孩子看不看得到父的历史」挑一组措辞。
//
// 源: packages/subagent/tool-subagent/src/index.ts:220-245
//
// 一个 fork 出来的孩子已经看得见这段对话里那些已完成的回合。对着它说「它看不到
// 这段对话」是假话——模型会白白把它其实已经知道的东西重述一遍，而那正是 fork
// 这条路要省掉的开销。
func providerWording(inheritsConversation bool) wording {
	if inheritsConversation {
		return wording{description: inheritingDescription, promptDescription: inheritingPromptDescription}
	}
	return wording{description: standaloneDescription, promptDescription: standalonePromptDescription}
}

// toolDescription 把那句措辞和后台那条尾巴接起来。
//
// 源: packages/subagent/tool-subagent/src/index.ts:308-315
func toolDescription(base string, backgroundEnabled, continuable bool) string {
	switch {
	case !backgroundEnabled:
		return base + foregroundOnlySuffix
	case continuable:
		return base + continuableSuffix
	default:
		return base + oneShotSuffix
	}
}

// backgroundDescription 是 run_in_background 那个参数的说明。
//
// 源: packages/subagent/tool-subagent/src/index.ts:330-332
func backgroundDescription(continuable bool) string {
	if continuable {
		return continuableBackgroundDescription
	}
	return oneShotBackgroundDescription
}

// sectionText 是那段后台指引。它只在可续后台那条路上出现。
//
// 源: packages/subagent/tool-subagent/src/index.ts:473
func sectionText(toolName string) string {
	return "Use " + toolName + " in the background by default. Start independent delegations together in " +
		"one assistant message and continue useful work while they run. Set `run_in_background: false` only " +
		"when your next action depends on that subagent's result. When a background run settles, the " +
		"runtime sends you a notice containing its outcome and any final assistant message."
}
