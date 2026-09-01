// 本文件的作用：审批这一层的词汇——策略、请求标识、三条 approval/* 事件的类型与
// 负载、给模型看的那两句策略陈述，以及那两个从日志里折出结论的纯函数。
//
// 源: packages/interaction/user-approval/src/types.ts
// 源: packages/interaction/user-approval/src/index.ts:34-134

package userapproval

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// RequestID 把一条 approval/asked 和它那条 approval/decided 配成一对。
//
// 源: packages/interaction/user-approval/src/types.ts:10-14
//
// 每一次 [Service.Request] 现发一个新的，所以同一条日志里不会有两个还没结算的
// 同名请求——那正是 [Trace] 要盯的事。
//
// 新增: DSH 用 Branded<'ApprovalRequestId'> 造一个名义类型，再用一个同名函数把裸
// 字符串标上这个牌子。Go 的定义类型天生就是名义类型，转换本身就是那个「标牌子」
// 的函数，所以那个构造函数在 Go 里没有对应物。
type RequestID string

// Policy 是一条会话的审批策略——**在任何答复者看到之前**，一次询问会怎么样。
//
// 源: packages/interaction/user-approval/src/index.ts:49-59（ApprovalPolicy）
type Policy string

const (
	// PolicyAsk 是默认：交给接上来的那些答复者；一个都没接就落到失败关闭的
	// [github.com/snight1983/ds-harness-go/core/tools.ApprovalUnavailable]。
	PolicyAsk Policy = "ask"
	// PolicyNever 是「谁都不问」：每一次询问都确定地结算成
	// [github.com/snight1983/ds-harness-go/core/tools.ApprovalRejected]。
	//
	// 这是无人值守（CI、批跑）那个严格立场，也是唯一一个不问就知道答案的策略。
	PolicyNever Policy = "never"
)

// Policies 是全部合法的 [Policy]，供选项广告和运行期校验用。
//
// 源: packages/interaction/user-approval/src/index.ts:61-62（APPROVAL_POLICIES）
//
// 交出一份新切片而不是暴露一个包级变量：一个调用方排个序或者改一格，
// 就会把别人看到的词汇表也改了。
func Policies() []Policy { return []Policy{PolicyAsk, PolicyNever} }

// KnownPolicy 说明这个策略在不在封闭词汇表里。
//
// 源: packages/interaction/user-approval/src/index.ts:143
func KnownPolicy(policy Policy) bool {
	for _, known := range Policies() {
		if policy == known {
			return true
		}
	}
	return false
}

// Outcomes 是全部合法的答复，供运行期把答复者还回来的东西归一化用。
//
// 源: packages/interaction/user-approval/src/index.ts:81-82
//
// 词汇表本身在 [github.com/snight1983/ds-harness-go/core/tools]（见包文档），这里只是把那四个值列成
// 一份能遍历的单子。
func Outcomes() []tools.ApprovalOutcome {
	return []tools.ApprovalOutcome{
		tools.ApprovalAllowedOnce,
		tools.ApprovalRejected,
		tools.ApprovalCancelled,
		tools.ApprovalUnavailable,
	}
}

// KnownOutcome 说明这个答复在不在封闭词汇表里。
//
// 源: packages/interaction/user-approval/src/index.ts:290
func KnownOutcome(outcome tools.ApprovalOutcome) bool {
	for _, known := range Outcomes() {
		if outcome == known {
			return true
		}
	}
	return false
}

// 三条 approval/* 事件的类型。
//
// 源: packages/interaction/user-approval/src/index.ts:34-73
//
// 它们**都不上表面**：三条都只进日志，模型的抄本里一条都看不见。模型是从系统提示
// 那份运行期快照（[PolicyStatement]）和一次切换时注入的那条通知里知道当前策略的。
const (
	// EventAsked 记下一个审批问题被摆到了答复者链面前。
	//
	// 它和后面必然跟着的那条 [EventDecided] 靠 id 配对。
	EventAsked session.EventType = "approval/asked"
	// EventDecided 记下前面那条 [EventAsked]（同一个 id）的结局。
	//
	// 一问一答，一条不多一条不少：一次决定、一次取消、或者那个失败关闭的
	// unavailable，都在这里落地。
	EventDecided session.EventType = "approval/decided"
	// EventPolicy 记下这条会话的审批策略被切换了。
	//
	// **最后一条**就是这条会话的覆盖值（[EffectivePolicy]）。
	EventPolicy session.EventType = "approval/policy"
)

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: 理由和 [github.com/snight1983/ds-harness-go/compaction.EventTypes] 逐字相同——Go 没有声明合并，
// [session.Vocabulary] 是个闭合的值，所以由本包交出单子、装配方自己拼：
//
//	vocabulary := session.CoreVocabulary().With(userapproval.EventTypes()...)
//
// 不这么做的话，一段带审批审计的日志会被 [session.CheckVocabulary] 整个拒掉。
func EventTypes() []session.EventType {
	return []session.EventType{EventAsked, EventDecided, EventPolicy}
}

// AskedData 是 [EventAsked] 的负载。
//
// 源: packages/interaction/user-approval/src/index.ts:44-49
//
// CallID 和 Reason 上的 omitempty 不是省字节：DSH 那边用 `...x !== undefined ? {k:x} : {}`
// 把没给的可选字段整个略掉，一条不带 callId 的审计事件在介质上就**没有**那个键。
// 这条日志是要持久保存并回放的，两边的字节必须对得上。
type AskedData struct {
	// ID 是这次询问的标识，[EventDecided] 会原样回引它。
	ID RequestID `json:"id"`
	// ToolName 是这个问题问的是哪件工具（呈现和审计都要）。
	ToolName string `json:"toolName"`
	// CallID 是被决定的那次具体调用；提问方有的时候才给。
	//
	// 它让界面能把这次询问贴到自己已经流式画出来的那次工具调用上。
	CallID llm.CallID `json:"callId,omitempty"`
	// Reason 是提问方自己那句「我为什么在问」，可以为空。
	Reason string `json:"reason,omitempty"`
}

// DecidedData 是 [EventDecided] 的负载。
//
// 源: packages/interaction/user-approval/src/index.ts:55-58
type DecidedData struct {
	// ID 是它回引的那条 [EventAsked]。
	ID RequestID `json:"id"`
	// Outcome 是结局。
	Outcome tools.ApprovalOutcome `json:"outcome"`
}

// PolicySourceDelegation 标记一条被派发时种进子 agent 的覆盖。
//
// 源: packages/interaction/user-approval/src/index.ts:69-70
const PolicySourceDelegation = "delegation"

// PolicyData 是 [EventPolicy] 的负载。
//
// 源: packages/interaction/user-approval/src/index.ts:67-71
type PolicyData struct {
	// Policy 是从这条事件起生效的策略。
	Policy Policy `json:"policy"`
	// Source 空着表示这是一次运行期切换；[PolicySourceDelegation] 表示这是派发时
	// 种进来的。
	Source string `json:"source,omitempty"`
}

// 给模型看的那两句策略陈述。
//
// 源: packages/interaction/user-approval/src/index.ts:99-102
//
// 它们是给模型看的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线。
const (
	// NeverStatement 说的是那个确定的 never 策略。
	NeverStatement = "Approval prompts are disabled in this session: actions that require " +
		"approval are rejected automatically — do not request sandbox escalation " +
		"(do not set `sandbox_permissions`)."
	// AskStatement 说的是一个仍然可能失败关闭的交互式策略。
	AskStatement = "Approval policy: ask. Operations that require approval may ask through " +
		"the configured answerers; without an available answerer, the request fails closed."
)

// PolicyStatement 交出这个策略对模型的那句陈述。
//
// 源: packages/interaction/user-approval/src/index.ts:210-213
//
// 新增: DSH 在构造函数里把它注册成一段 `systemPrompt.context`。系统提示服务在第 6 块，
// 所以这里只交出那句话，挂到哪个片段（DSH 用 name `approval:policy`、order 115）
// 由装配方决定。这么切还顺带把这两句话变成了纯函数。
//
// **完整的当前值**跟在保留历史后面走，所以切一次策略不会把系统提示那段稳定的
// 缓存前缀改写掉。
func PolicyStatement(policy Policy) string {
	if policy == PolicyNever {
		return NeverStatement
	}
	return AskStatement
}

// EffectivePolicy 折出这条会话自己的审批策略覆盖：日志里**最后**那条
// [EventPolicy]，一次都没切过就交出 false。
//
// 源: packages/interaction/user-approval/src/index.ts:69-83（effectiveApprovalPolicy）
//
// 这是那个纯折叠——恢复不需要任何补课机制，因为把日志回放一遍**就是**那个状态。
//
// 一条读不回来的负载当作没有这条切换（继续往前找）：这个函数是读路径上的，
// 它的职责是「当前策略是什么」，不是「这条日志合不合法」。后者是 [Trace] 的事，
// 而且那边会把同一条坏事件报成一条明确的违例。
func EffectivePolicy(events []session.Event) (Policy, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != EventPolicy {
			continue
		}
		var data PolicyData
		if err := json.Unmarshal(events[index].Data, &data); err != nil {
			continue
		}
		return data.Policy, true
	}
	return "", false
}

// hasOpenTurn 说明这条日志此刻是不是正处在一个打开的回合里——也就是有一条
// [session.EventTurnStart] 还没被 [session.EventTurnEnd] 关掉。
//
// 源: packages/interaction/user-approval/src/index.ts:120-134
//
// 它是 [Service.Request] 的前置条件，理由见包文档第一条：审计那一对必须被回合圈住。
func hasOpenTurn(events []session.Event) bool {
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Type {
		case session.EventTurnStart:
			return true
		case session.EventTurnEnd:
			return false
		}
	}
	return false
}
