// 本文件的作用：两种进程内孩子共用的那套组装——派发深度预算、耐久的会话元数据、
// 解算出来的孩子 agent 选项、种进去的派发策略，以及一个孩子创建窗口里要挂的
// 那份带作用域的组合。
//
// 源: packages/subagent/subagent/src/child-agent.ts
//
// 一次性那条提供方驱动和续接管理器都从这里组装孩子，于是深度记账、血统盖章、
// 派发策略这三件事只有一处家。

package subagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/preset/agentpresets"
	"github.com/snight1983/ds-harness-go/session"
)

// DepthError 是「再起一个孩子就会越过请求方给的深度上限」。
//
// 源: packages/subagent/subagent/src/child-agent.ts:31-36
//
// 新增: DSH 那边是一个 SubagentDepthError 类，调用方靠 instanceof 认它。Go 里
// 认错误靠 [errors.As]，所以它是一个带那两个数的错误类型。它**有意**不裹
// [ErrInvalidRequest]：上限本身是合法的，越界是这次派发在运行期的结局，
// 不是调用方给错了东西。
type DepthError struct {
	// AttemptedDepth 是这个孩子算出来的派发深度。
	AttemptedDepth int
	// MaxDepth 是请求方给的绝对上限。
	MaxDepth int
}

// Error 交出这条失败的说法。
func (e *DepthError) Error() string {
	return fmt.Sprintf("subagent: 子 agent 深度 %d 越过了 maxDepth %d", e.AttemptedDepth, e.MaxDepth)
}

// ResolveChildDepth 从父身上解算出孩子的派发深度，并守住那个可选的上限。
// maxDepth 为 nil 表示不设上限。
//
// 源: packages/subagent/subagent/src/child-agent.ts:48-57
//
// 持久的父会话头是那条单调的地板，所以一个恢复回来的父不会像顶层一样重新数起。
//
// 新增: DSH 还要查一次 Number.isSafeInteger 并抛 RangeError——它那个深度是
// float64，加着加着会掉出安全整数区间。Go 的 int 没有这个区间，那道检查随之消失。
func ResolveChildDepth(parent agent.Agent, maxDepth *int) (int, error) {
	childDepth := DelegationDepthOf(parent) + 1
	if maxDepth != nil && childDepth > *maxDepth {
		return 0, &DepthError{AttemptedDepth: childDepth, MaxDepth: *maxDepth}
	}
	return childDepth, nil
}

// ResolveChildAgentOptions 解算孩子那份 agent 选项：父的提供方／模型／token 上限
// 那条路由，除非请求自己另有说法。
//
// 源: packages/subagent/subagent/src/child-agent.ts:87-119（resolveChildAgentOptions）
//
// 新增: DSH 还在返回值上盖一个 `subagentDepth: childDepth`——它的 AgentOptions 是
// 一个可声明合并的对象，depth.ts 往上贴了那个字段。Go 这边
// [github.com/snight1983/ds-harness-go/core/agent.Options] 是闭合结构体，贴不上；那份深度改走
// [github.com/snight1983/ds-harness-go/core/agent.CreateOptions.DelegationDepth] 写进会话头，
// 也就是 [ChildSessionMeta] 盖的那一下。理由和 [DelegationDepthOf] 上那条一样：
// 头是唯一的事实，所以这个函数不再收 childDepth 这个参数。
//
// 新增: DSH 用对象展开表达「请求里有这个键就盖掉父的」。Go 的零值就是「没给」
// （见 [github.com/snight1983/ds-harness-go/core/agent.Options] 上那条注释），所以逐字段判零值。
func ResolveChildAgentOptions(parent agent.Agent, requested agent.Options) agent.Options {
	resolved := parent.Options()
	if requested.Provider != "" {
		resolved.Provider = requested.Provider
	}
	if requested.Model != "" {
		resolved.Model = requested.Model
	}
	if requested.MaxTokens != 0 {
		resolved.MaxTokens = requested.MaxTokens
	}
	return resolved
}

// ChildSessionMeta 建出孩子会话那份耐久的创建元数据：父的工作目录、它那条直系
// 血统、粗粒度的产品来源、那份必须活过持久化的递归预算、把继承来的父历史和孩子
// 自己的活儿分开的那条种子边界，以及孩子跑在哪份组合上。
//
// 源: packages/subagent/subagent/src/child-agent.ts:121-156（childSessionMeta）
//
// 预设从父**活着的**那条作用域链上读，而不是从它的头上读：一个空着的时候换过
// 预设的父跑在更新的那份组合上，而它的头还写着旧的那个。记下这件事正是孩子的
// 历史可重建的前提——不记，一次对孩子的冷读就会解出部署默认值，然后拿一套这个
// 孩子从来没有过的工具集去重建它的回合。
//
// presets 为 nil（一套不组装名册的部署）时不记预设：那里模型可见的那些行本来就在
// 宿主组合里，孩子透过全局层看得见它们。
//
// 新增: DSH 交回的是 CreateAgentOptions 里那个嵌套的 `meta` 对象。Go 这边
// [github.com/snight1983/ds-harness-go/core/agent.CreateOptions] 把那层嵌套摊平了，所以这里交回的是一份
// **只填了血统那几项**的 CreateOptions；SessionID、Seed、AgentOptions、Setup
// 由调用方补上。
func ChildSessionMeta(
	parent agent.Agent,
	childDepth int,
	lineageSeedLength int,
	presets *agentpresets.Roster,
) agent.CreateOptions {
	parentHeader := parent.Session().Header()
	options := agent.CreateOptions{
		Cwd:           parentHeader.Cwd,
		ParentSession: parentHeader.ID,
		// 只作导航分类用；模式和能不能续，权威始终是那条描述符。
		Origin: session.OriginSubagent,
		// 耐久：那份递归预算必须活过持久化和恢复。
		DelegationDepth: childDepth,
		SeedLength:      lineageSeedLength,
	}
	if presets != nil {
		options.AgentPreset = presets.ComposedPreset(scopeKeyOf(parent))
	}
	return options
}

// scopeKeyOf 交出一个 agent 那把作用域键；没有作用域时交回 nil。
func scopeKeyOf(a agent.Agent) *scope.Key {
	agentScope := a.Scope()
	if agentScope == nil {
		return nil
	}
	return agentScope.Key()
}

// ChildComposition 是一个孩子创建窗口里要挂的那份带作用域的组合。
//
// 源: packages/subagent/subagent/src/child-agent.ts:158-164（ChildComposition）
type ChildComposition struct {
	// Persona 是只给这个孩子、盖掉部署人设的那份人设；空串表示不换。
	Persona string
	// ToolFilter 是只给这个孩子的工具范围；零值表示不过滤。
	ToolFilter tools.Restriction
}

// DelegationContext 是每一个进程内孩子都看得到的那句派发范围陈述。
//
// 源: packages/subagent/subagent/src/child-agent.ts:166-172（SUBAGENT_DELEGATION_CONTEXT）
//
// 它是一份运行期上下文贡献、而不是一段系统提示词，于是部署那份系统提示词在父和
// 孩子之间保持一模一样。
//
// 给模型看的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线
// （成例见 [github.com/snight1983/ds-harness-go/interaction/userapproval.NeverStatement]）。
const DelegationContext = "You are a delegated subagent: your permission scope was fixed when you were started and cannot be " +
	"widened from inside this session — operations that require approval are rejected automatically. " +
	"When the task needs access beyond that scope, do not retry the denied operation; state the " +
	"limitation in your reply so the delegating agent can handle it."

const (
	// delegationContextName 是那句派发范围陈述占的上下文名字。
	delegationContextName = "subagent:delegation"
	// delegationContextOrder 把它排在审批策略那句（115）后面。
	//
	// 源: packages/subagent/subagent/src/child-agent.ts:169
	//
	// 新增: DSH 那条注释说的是「排在 sandbox:policy（110）和 approval:policy（115）
	// 后面」。沙箱那条线不在本次移植范围内，110 那句话根本不存在，但这个数照抄：
	// 它要保住的是和 115 的相对次序，改小了只会让两句话的先后翻过来。
	delegationContextOrder = 120
)

// ChildCompositionServices 是挂一份孩子组合要用到的那几样服务。
//
// 新增: DSH 从孩子那个 cordis 上下文上直接取 `childCtx.systemPrompt`、
// `childCtx.tools`，预设名册则是 `childCtx.get('agentPresets')` 那种可有可无的取法。
// Go 没有那个容器，「在不在场」就是装配方手上有没有这个值，所以做成一个显式的
// 结构体（和 [ListingServices] 同一种做法）。
type ChildCompositionServices struct {
	// SystemPrompt 是系统提示词注册表，那句派发范围陈述必须挂得上去。必填。
	SystemPrompt *systemprompt.Registry
	// Tools 是工具运行时；只在 [ChildComposition.ToolFilter] 非零时用得着。
	Tools *tools.Runtime
	// Presets 是 agent 预设名册；nil 表示这套部署不组装名册，孩子不认亲也不报错。
	Presets *agentpresets.Roster
}

// ApplyChildComposition 在一个孩子的创建窗口里把它组装起来：先认到父那份预设上，
// 再登记那句固定的派发范围陈述，然后挂上孩子自己那份盖掉部署人设的人设和工具
// 限制——全都归孩子这个作用域所有，因此它的父和它的兄弟都看不见。创建和冷恢复
// 都从这里过。
//
// 源: packages/subagent/subagent/src/child-agent.ts:177-218（applyChildComposition）
//
// 认亲在前、孩子自己那几笔登记在后。这本来就是分层已经蕴含的次序——最近的那一层
// 赢下一个名字，一份只给这个孩子的限制和它整条链准入的东西求交——但在这里把它写
// 出来，免得这两步被读成互不相干的两件事。
//
// 认亲和那几笔登记在**同一次调用**里，是因为一个没认亲就组装好的孩子正是这个函数
// 要防的那个缺陷：模型可见的那些行全在 agent 那一层，一个一份预设都没认的孩子会
// 看到一个空的工具注册表、以及它父亲那些提示词段落一段都没有。把父收成参数，
// 就让那个遗漏在每一个调用点上都写不出来。
//
// 新增: DSH 那三笔登记都是 void，撤销靠 cordis 处置孩子那个上下文。Go 这边它们各自
// 交回一个撤销函数，而这里**有意**把它们丢掉：owner 就是孩子自己那个作用域，
// 作用域一处置这三笔就跟着没了，那正是 DSH 的语义（成例见
// [github.com/snight1983/ds-harness-go/core/systemprompt.NewRegistry] 里那几笔自己的登记）。
func ApplyChildComposition(
	ctx context.Context,
	childScope *scope.Scope,
	parent agent.Agent,
	composition ChildComposition,
	services ChildCompositionServices,
) error {
	if services.SystemPrompt == nil {
		return fmt.Errorf("%w：组装子 agent 需要系统提示词注册表", ErrInvalidRequest)
	}
	if services.Presets != nil {
		if _, err := services.Presets.ComposeFrom(childScope.Key(), scopeKeyOf(parent)); err != nil {
			return err
		}
	}
	if _, err := services.SystemPrompt.Context(ctx, childScope, systemprompt.PromptContext{
		Name:  delegationContextName,
		Order: delegationContextOrder,
		Text:  systemprompt.StaticText(DelegationContext),
	}); err != nil {
		return err
	}
	if composition.Persona != "" {
		if _, err := services.SystemPrompt.Section(ctx, childScope, systemprompt.PromptSection{
			Name:  systemprompt.PersonaSection,
			Order: systemprompt.PersonaOrder,
			Text:  systemprompt.StaticText(composition.Persona),
		}); err != nil {
			return err
		}
	}
	if composition.ToolFilter.Allow != nil || composition.ToolFilter.Deny != nil {
		if services.Tools == nil {
			return fmt.Errorf("%w：给子 agent 设工具范围需要工具运行时", ErrInvalidRequest)
		}
		if _, err := services.Tools.Restrict(ctx, childScope, composition.ToolFilter); err != nil {
			return err
		}
	}
	return nil
}

// DelegatedPolicyOverrides 是在派发这条边上种进孩子日志的那份策略。
//
// 源: packages/subagent/subagent/src/child-agent.ts:220-230（DelegatedPolicyOverrides）
//
// 新增: DSH 这里还有一半是 `sandboxMode`——父会话那条显式的沙箱模式覆盖。沙箱那
// 整条线不在本次移植范围内，所以那一半连同它那次 `sandbox/mode` 追加一起去掉了，
// 只剩审批策略这一半。
type DelegatedPolicyOverrides struct {
	// ApprovalPolicy 在审批能力组装进来的时候是
	// [github.com/snight1983/ds-harness-go/interaction/userapproval.PolicyNever]，否则是空串：一个被派发的
	// 孩子只在派发那一刻定下的范围里行动，所以它的询问确定地被拒。
	ApprovalPolicy userapproval.Policy
}

// CaptureDelegatedPolicyOverrides 把要种进这一次派发的那份策略拍下来。
//
// 源: packages/subagent/subagent/src/child-agent.ts:232-247（captureDelegatedPolicyOverrides）
//
// 在这次孩子开工的第一个可中断点**之前**同步调它：父后来那次切换属于父的未来，
// 不属于这个孩子。审批策略被钉成「谁都不问」，不看父自己是什么策略。
//
// approval 为 nil 表示这套部署没组装审批能力，那就一条都不种。
//
// 新增: DSH 这个函数收的是 parent，因为它还要读父会话那条沙箱覆盖。沙箱出了范围，
// 剩下这一半根本不看父——那个钉法只问「审批这样东西在不在场」——所以参数换成了
// 那个服务本身。
func CaptureDelegatedPolicyOverrides(approval *userapproval.Service) DelegatedPolicyOverrides {
	if approval == nil {
		return DelegatedPolicyOverrides{}
	}
	return DelegatedPolicyOverrides{ApprovalPolicy: userapproval.PolicyNever}
}

// AppendDelegatedPolicyOverrides 把拍下来的那份派发策略以 `source: delegation`
// 追加到孩子**自己**那条日志上，落在还没公布的创建窗口里，于是这个孩子的有效策略
// 单看它自己的日志就重建得出来。这几笔落在分叉种子之后，所以新策略压过陈旧的种子
// 状态；孩子后来自己那些切换仍旧压过这几笔。
//
// 源: packages/subagent/subagent/src/child-agent.ts:249-268（appendDelegatedPolicyOverrides）
//
// 新增: DSH 调的是 `childSession.append(type, data)`。Go 的
// [github.com/snight1983/ds-harness-go/core/session.Session.Append] 收的是一条 Data 已经是
// json.RawMessage 的事件，所以负载在这里先编一次。
// [github.com/snight1983/ds-harness-go/interaction/userapproval.SetPolicy] 那条自由函数用不上：它写的
// PolicyData 不带 Source，而 `delegation` 这个来源正是这几笔和一次运行期切换的
// 唯一区别。
func AppendDelegatedPolicyOverrides(
	childSession *coresession.Session,
	overrides DelegatedPolicyOverrides,
) error {
	if overrides.ApprovalPolicy == "" {
		return nil
	}
	data, err := json.Marshal(userapproval.PolicyData{
		Policy: overrides.ApprovalPolicy,
		Source: userapproval.PolicySourceDelegation,
	})
	if err != nil {
		// 走不到：PolicyData 两个字段都是字符串。照实转出去比断言它不会失败诚实。
		return err
	}
	_, err = childSession.Append(session.Event{Type: userapproval.EventPolicy, Data: data})
	return err
}

// ChildCreateInputs 是每一次进程内孩子创建都共有的身份与血统输入。
//
// 源: packages/subagent/subagent/src/child-agent.ts:270-280（ChildCreateInputs）
type ChildCreateInputs struct {
	// SessionID 是给这个孩子占下来的会话 id。
	SessionID session.SessionID
	// Parent 是发起派发的那个父 agent。
	Parent agent.Agent
	// ChildDepth 是解算出来的派发深度。
	ChildDepth int
	// LineageSeedLength 是种子里有多少条打头的事件来自父的日志。
	LineageSeedLength int
}
